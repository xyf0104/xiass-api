//go:build unit

package service

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/antigravity"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// Native-path coverage for upstream eb2da3448, scoped to Fable/Mythos 5.2.
func TestClaude52NativeValidation(t *testing.T) {
	for _, model := range []string{"claude-fable-5-2", "claude-mythos-5-2"} {
		for _, body := range []string{
			`{}`, `{"thinking":{"type":"adaptive"}}`,
			`{"temperature":1}`, `{"top_p":0.99}`,
			`{"tool_choice":{"type":"auto"}}`, `{"tool_choice":{"type":"none"}}`,
			`{"output_config":{"effort":"low"}}`, `{"output_config":{"effort":"medium"}}`,
			`{"output_config":{"effort":"high"}}`, `{"output_config":{"effort":"xhigh"}}`, `{"output_config":{"effort":"max"}}`,
		} {
			require.NoError(t, validateClaude52Request([]byte(body), model), body)
		}
		for _, tc := range []struct{ body, errorPart string }{
			{`{"thinking":{"type":"disabled"}}`, "adaptive"},
			{`{"thinking":{"type":"enabled","budget_tokens":1000}}`, "adaptive"},
			{`{"thinking":{"type":"between_tools"}}`, "adaptive"},
			{`{"output_config":{"effort":"none"}}`, "reasoning effort"},
			{`{"output_config":{"effort":"minimal"}}`, "reasoning effort"},
			{`{"tool_choice":"required"}`, "forced tool_choice"},
			{`{"tool_choice":{"type":"any"}}`, "forced tool_choice"},
			{`{"tool_choice":{"type":"tool","name":"lookup"}}`, "forced tool_choice"},
			{`{"temperature":0.5}`, "temperature"},
			{`{"temperature":"1"}`, "temperature"},
			{`{"top_p":1}`, "top_p"},
			{`{"top_p":"0.99"}`, "top_p"},
			{`{"top_k":1}`, "top_k"},
			{`{"temperature":1,"top_p":0.99}`, "together"},
		} {
			require.ErrorContains(t, validateClaude52Request([]byte(tc.body), model), tc.errorPart, tc.body)
			for _, legacy := range []string{"claude-opus-4-6", "claude-sonnet-5-5", "deepseek-v4-pro", "custom-model"} {
				require.NoError(t, validateClaude52Request([]byte(tc.body), legacy), legacy)
			}
		}
	}
}

func TestClaude52NativeSignedHistory(t *testing.T) {
	for _, model := range []string{"claude-fable-5-2", "claude-mythos-5-2"} {
		body := []byte(`{"messages":[{"role":"assistant","content":[{"type":"thinking","thinking":"","signature":"signed"},{"type":"redacted_thinking","data":"opaque"},{"type":"text","text":"progress"},{"type":"tool_use","id":"toolu_1","name":"lookup","input":{}}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_1","content":"ok"}]}]}`)
		require.Equal(t, string(body), string(FilterThinkingBlocks(body, model)))
		for _, invalid := range []string{
			`{"type":"thinking","thinking":"unsigned"}`,
			`{"type":"thinking","signature":"` + antigravity.DummyThoughtSignature + `"}`,
			`{"type":"redacted_thinking","data":""}`,
		} {
			mixed := []byte(`{"messages":[{"role":"assistant","content":[{"type":"thinking","thinking":"","signature":"signed"},{"type":"redacted_thinking","data":"opaque"},{"type":"tool_use","id":"toolu_1","name":"lookup","input":{}}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_1","content":"ok"}]},{"role":"assistant","content":[` + invalid + `]}]}`)
			out := FilterThinkingBlocks(mixed, model)
			require.NotContains(t, string(out), `"type":"thinking"`)
			require.NotContains(t, string(out), `"type":"redacted_thinking"`)
			require.Equal(t, "toolu_1", gjson.GetBytes(out, "messages.0.content.0.id").String())
			require.Equal(t, "toolu_1", gjson.GetBytes(out, "messages.1.content.0.tool_use_id").String())
			require.Equal(t, "(assistant content removed)", gjson.GetBytes(out, "messages.2.content.0.text").String())
			require.Equal(t, string(mixed), string(FilterThinkingBlocks(mixed, "deepseek-v4-pro")))
		}
	}
}

func TestClaude52NativeRejectsBeforeDispatch(t *testing.T) {
	for _, model := range []string{"claude-fable-5-2", "claude-mythos-5-2"} {
		for _, accountType := range []string{AccountTypeAPIKey, AccountTypeOAuth, AccountTypeServiceAccount, AccountTypeBedrock} {
			t.Run(model+"/"+accountType, func(t *testing.T) {
				account := &Account{ID: 201, Platform: PlatformAnthropic, Type: accountType, Credentials: map[string]any{
					"model_mapping": map[string]any{"alias": model},
				}, Extra: map[string]any{"anthropic_passthrough": true}}
				requestModel := "alias"
				if accountType == AccountTypeOAuth {
					requestModel = model
				}
				body := []byte(fmt.Sprintf(`{"model":%q,"thinking":{"type":"enabled"},"messages":[{"role":"user","content":"hello"}]}`, requestModel))
				parsed, err := ParseGatewayRequest(NewRequestBodyRef(body), PlatformAnthropic)
				require.NoError(t, err)
				rec := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(rec)
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
				upstream := &anthropicHTTPUpstreamRecorder{}
				svc := &GatewayService{httpUpstream: upstream}
				_, err = svc.Forward(context.Background(), c, account, parsed)
				require.ErrorContains(t, err, "adaptive")
				require.Equal(t, http.StatusBadRequest, rec.Code)
				require.Equal(t, "invalid_request_error", gjson.Get(rec.Body.String(), "error.type").String())
				require.Nil(t, upstream.lastReq)
				require.Equal(t, body, parsed.Body.Bytes())
			})
		}
	}
}

func TestClaude52NativeForwardPreservesHistoryAndFastTier(t *testing.T) {
	for _, model := range []string{"claude-fable-5-2", "claude-mythos-5-2"} {
		for _, passthrough := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/passthrough=%v", model, passthrough), func(t *testing.T) {
				account := newAnthropicAPIKeyAccountForTest()
				account.Extra["anthropic_passthrough"] = passthrough
				account.Credentials["model_mapping"] = map[string]any{"alias": model}
				body := []byte(`{"model":"alias","speed":"fast","messages":[{"role":"assistant","content":[{"type":"thinking","thinking":"","signature":"signed"},{"type":"redacted_thinking","data":"opaque"},{"type":"text","text":"progress"}]},{"role":"user","content":"continue"}]}`)
				parsed, err := ParseGatewayRequest(NewRequestBodyRef(body), PlatformAnthropic)
				require.NoError(t, err)
				upstream := &anthropicHTTPUpstreamRecorder{resp: &http.Response{
					StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}},
					Body: io.NopCloser(strings.NewReader(`{"id":"msg_1","type":"message","role":"assistant","content":[{"type":"text","text":"ok"}],"usage":{"input_tokens":10,"output_tokens":5}}`)),
				}}
				svc := newForwardPartialUsageServiceForTest(upstream)
				rec := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(rec)
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
				result, err := svc.Forward(context.Background(), c, account, parsed)
				require.NoError(t, err)
				require.Equal(t, model, gjson.GetBytes(upstream.lastBody, "model").String())
				require.Equal(t, "signed", gjson.GetBytes(upstream.lastBody, "messages.0.content.0.signature").String())
				require.Equal(t, "opaque", gjson.GetBytes(upstream.lastBody, "messages.0.content.1.data").String())
				require.Equal(t, "alias", result.Model)
				require.Equal(t, model, result.UpstreamModel)
				require.NotNil(t, result.ServiceTier)
				require.Equal(t, "fast", *result.ServiceTier)
				require.Equal(t, 5, result.Usage.OutputTokens)
			})
		}
		bedrock := &Account{Platform: PlatformAnthropic, Type: AccountTypeBedrock}
		require.Nil(t, anthropicSpeedServiceTier(bedrock, "fast", model))
	}
	require.False(t, modelSupportsAnthropicFastMode("claude-opus-4-7"))
	require.True(t, modelSupportsAnthropicFastMode("claude-opus-4-8"))
}

func TestClaude52CodexCatalogMetadata(t *testing.T) {
	for _, model := range []string{"claude-fable-5-2", "claude-mythos-5-2", "claude-fable-5-2-thinking"} {
		descriptor := newConfiguredCodexModelDescriptor(model)
		require.EqualValues(t, 1_000_000, descriptor.ContextWindow)
		require.EqualValues(t, 1_000_000, descriptor.MaxContextWindow)
		require.NotNil(t, descriptor.DefaultReasoningLevel)
		require.Equal(t, "high", *descriptor.DefaultReasoningLevel)
		var efforts []string
		for _, level := range descriptor.SupportedReasoningLevels {
			efforts = append(efforts, level.Effort)
		}
		require.Equal(t, []string{"low", "medium", "high", "xhigh", "max"}, efforts)
	}
	legacy := newConfiguredCodexModelDescriptor("claude-opus-4-6")
	require.EqualValues(t, configuredCodexFallbackContext, legacy.ContextWindow)
}
