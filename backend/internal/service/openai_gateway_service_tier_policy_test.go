package service

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/claude"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/pkg/openai_compat"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

type serviceTierPolicyCase struct {
	name        string
	action      string
	requestTier string
	wantTier    string
}

var serviceTierPolicyCases = []serviceTierPolicyCase{
	{
		name:        "filter yields no billing tier",
		action:      BetaPolicyActionFilter,
		requestTier: OpenAIFastTierPriority,
	},
	{
		name:        "force priority yields priority billing tier",
		action:      OpenAIFastPolicyActionForcePriority,
		requestTier: OpenAIFastTierFlex,
		wantTier:    OpenAIFastTierPriority,
	},
}

func TestOpenAIForward_GroupForceFastConsistency(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, entry := range []string{"responses", "responses_stream", "chat", "chat_stream", "raw_chat", "responses_chat_fallback", "passthrough"} {
		for _, tc := range []struct {
			name, tier, action, want string
		}{
			{"missing", "", BetaPolicyActionPass, "priority"},
			{"default", "default", BetaPolicyActionPass, "priority"},
			{"flex", "flex", BetaPolicyActionPass, "priority"},
			{"ultrafast", "ultrafast", BetaPolicyActionPass, "priority"},
			{"filter", "", BetaPolicyActionFilter, ""},
			{"block", "", BetaPolicyActionBlock, ""},
		} {
			t.Run(entry+"/"+tc.name, func(t *testing.T) {
				body := `{"model":"gpt-5.5","input":"hello","stream":false`
				path := "/v1/responses"
				if entry == "chat" || entry == "chat_stream" || entry == "raw_chat" {
					body = `{"model":"gpt-5.5","messages":[{"role":"user","content":"hello"}],"stream":false`
					path = "/v1/chat/completions"
				}
				if tc.tier != "" {
					body += `,"service_tier":"` + tc.tier + `"`
				}
				body += `}`
				if strings.HasSuffix(entry, "_stream") {
					body = strings.Replace(body, `"stream":false`, `"stream":true`, 1)
				}
				c, rec := newServiceTierPolicyTestContext(t, path, []byte(body))
				ctx := context.WithValue(context.Background(), ctxkey.Group, &Group{
					ID: 7, Platform: PlatformOpenAI, Hydrated: true, Status: StatusActive, ForceOpenAIFast: true,
				})
				c.Request = c.Request.WithContext(ctx)
				resp := serviceTierPolicyResponsesResponse("gpt-5.5")
				if entry == "raw_chat" || entry == "responses_chat_fallback" {
					resp = serviceTierPolicyChatCompletionsResponse("gpt-5.5")
				}
				responseBody, err := io.ReadAll(resp.Body)
				require.NoError(t, err)
				resp.Body = io.NopCloser(strings.NewReader(strings.ReplaceAll(string(responseBody), `"model":"gpt-5.5"`, `"model":"gpt-5.5","service_tier":"ultrafast"`)))
				upstream := &httpUpstreamRecorder{resp: resp}
				svc := newServiceTierPolicyGateway(t, tc.action, upstream)
				account := serviceTierPolicyAccount()
				account.Extra = map[string]any{openai_compat.ExtraKeyResponsesSupported: true}
				var result *OpenAIForwardResult
				switch entry {
				case "chat", "chat_stream":
					result, err = svc.ForwardAsChatCompletions(ctx, c, account, []byte(body), "", "")
				case "raw_chat":
					result, err = svc.forwardAsRawChatCompletions(ctx, c, account, []byte(body), "")
				default:
					if entry == "responses_chat_fallback" {
						account.Extra[openai_compat.ExtraKeyResponsesMode] = string(openai_compat.ResponsesSupportModeForceChatCompletions)
					}
					account.Extra["openai_passthrough"] = entry == "passthrough"
					result, err = svc.Forward(ctx, c, account, []byte(body))
				}
				if tc.action == BetaPolicyActionBlock {
					var blocked *OpenAIFastBlockedError
					require.ErrorAs(t, err, &blocked)
					require.Nil(t, result)
					require.Empty(t, upstream.lastBody)
					require.Equal(t, http.StatusForbidden, rec.Code)
					return
				}
				require.NoError(t, err)
				require.NotNil(t, result)
				requireServiceTierMatchesFinalBody(t, upstream.lastBody, result, tc.want)
				require.Equal(t, "ultrafast", result.UpstreamResponseServiceTier)
			})
		}
	}
}

func TestForwardAsChatCompletions_BillingServiceTierFollowsFinalBody(t *testing.T) {
	gin.SetMode(gin.TestMode)

	for _, tc := range serviceTierPolicyCases {
		t.Run(tc.name, func(t *testing.T) {
			body := []byte(`{"model":"gpt-5.5","messages":[{"role":"user","content":"hello"}],"service_tier":"` + tc.requestTier + `","stream":false}`)
			c, rec := newServiceTierPolicyTestContext(t, "/v1/chat/completions", body)
			upstream := &httpUpstreamRecorder{resp: serviceTierPolicyResponsesResponse("gpt-5.5")}
			svc := newServiceTierPolicyGateway(t, tc.action, upstream)
			account := serviceTierPolicyAccount()
			account.Extra = map[string]any{openai_compat.ExtraKeyResponsesSupported: true}

			result, err := svc.ForwardAsChatCompletions(context.Background(), c, account, body, "", "")

			require.NoError(t, err)
			require.NotNil(t, result)
			require.Equal(t, http.StatusOK, rec.Code)
			requireServiceTierMatchesFinalBody(t, upstream.lastBody, result, tc.wantTier)
		})
	}
}

func TestForwardAsRawChatCompletions_BillingServiceTierFollowsFinalBody(t *testing.T) {
	gin.SetMode(gin.TestMode)

	for _, tc := range serviceTierPolicyCases {
		t.Run(tc.name, func(t *testing.T) {
			body := []byte(`{"model":"gpt-5.5","messages":[{"role":"user","content":"hello"}],"service_tier":"` + tc.requestTier + `","stream":false}`)
			c, rec := newServiceTierPolicyTestContext(t, "/v1/chat/completions", body)
			upstream := &httpUpstreamRecorder{resp: serviceTierPolicyChatCompletionsResponse("gpt-5.5")}
			svc := newServiceTierPolicyGateway(t, tc.action, upstream)

			result, err := svc.forwardAsRawChatCompletions(context.Background(), c, serviceTierPolicyAccount(), body, "")

			require.NoError(t, err)
			require.NotNil(t, result)
			require.Equal(t, http.StatusOK, rec.Code)
			requireServiceTierMatchesFinalBody(t, upstream.lastBody, result, tc.wantTier)
		})
	}
}

func TestForwardAsAnthropic_BillingServiceTierFollowsFinalBody(t *testing.T) {
	gin.SetMode(gin.TestMode)

	for _, tc := range serviceTierPolicyCases {
		t.Run(tc.name, func(t *testing.T) {
			body := []byte(`{"model":"gpt-4o","max_tokens":16,"messages":[{"role":"user","content":"hello"}],"stream":false}`)
			c, rec := newServiceTierPolicyTestContext(t, "/v1/messages", body)
			c.Request.Header.Set("anthropic-beta", claude.BetaFastMode)
			upstream := &httpUpstreamRecorder{resp: serviceTierPolicyResponsesResponse("gpt-4o")}
			svc := newServiceTierPolicyGateway(t, tc.action, upstream)
			account := serviceTierPolicyAccount()
			account.Extra = map[string]any{openai_compat.ExtraKeyResponsesSupported: true}

			result, err := svc.ForwardAsAnthropic(context.Background(), c, account, body, "", "")

			require.NoError(t, err)
			require.NotNil(t, result)
			require.Equal(t, http.StatusOK, rec.Code)
			requireServiceTierMatchesFinalBody(t, upstream.lastBody, result, tc.wantTier)
		})
	}
}

func TestForwardResponsesViaRawChatCompletions_BillingServiceTierFollowsFinalBody(t *testing.T) {
	gin.SetMode(gin.TestMode)

	for _, tc := range serviceTierPolicyCases {
		t.Run(tc.name, func(t *testing.T) {
			body := []byte(`{"model":"gpt-5.5","input":"hello","service_tier":"` + tc.requestTier + `","stream":false}`)
			c, rec := newServiceTierPolicyTestContext(t, "/v1/responses", body)
			upstream := &httpUpstreamRecorder{resp: serviceTierPolicyChatCompletionsResponse("gpt-5.5")}
			svc := newServiceTierPolicyGateway(t, tc.action, upstream)
			account := serviceTierPolicyAccount()
			account.Extra = map[string]any{
				openai_compat.ExtraKeyResponsesMode: string(openai_compat.ResponsesSupportModeForceChatCompletions),
			}

			result, err := svc.Forward(context.Background(), c, account, body)

			require.NoError(t, err)
			require.NotNil(t, result)
			require.Equal(t, http.StatusOK, rec.Code)
			requireServiceTierMatchesFinalBody(t, upstream.lastBody, result, tc.wantTier)
		})
	}
}

type serviceTierPolicyRepoStub struct {
	*openAIFastPolicyRepoStub
}

func (r *serviceTierPolicyRepoStub) GetMultiple(_ context.Context, keys []string) (map[string]string, error) {
	values := make(map[string]string)
	for _, key := range keys {
		if value, ok := r.values[key]; ok {
			values[key] = value
		}
	}
	return values, nil
}

func newServiceTierPolicyGateway(t *testing.T, action string, upstream HTTPUpstream) *OpenAIGatewayService {
	t.Helper()
	raw, err := json.Marshal(&OpenAIFastPolicySettings{
		Rules: []OpenAIFastPolicyRule{{
			ServiceTier: OpenAIFastTierAny,
			Action:      action,
			Scope:       BetaPolicyScopeAll,
		}},
	})
	require.NoError(t, err)
	repo := &serviceTierPolicyRepoStub{&openAIFastPolicyRepoStub{values: map[string]string{SettingKeyOpenAIFastPolicySettings: string(raw)}}}
	return &OpenAIGatewayService{
		settingService: NewSettingService(repo, &config.Config{}),
		cfg:            &config.Config{},
		httpUpstream:   upstream,
	}
}

func serviceTierPolicyAccount() *Account {
	return &Account{
		ID:          901,
		Name:        "service-tier-policy",
		Platform:    PlatformOpenAI,
		Type:        AccountTypeAPIKey,
		Concurrency: 1,
		Credentials: map[string]any{"api_key": "sk-test"},
	}
}

func newServiceTierPolicyTestContext(t *testing.T, path string, body []byte) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	return c, rec
}

func requireServiceTierMatchesFinalBody(t *testing.T, upstreamBody []byte, result *OpenAIForwardResult, want string) {
	t.Helper()
	if want == "" {
		require.False(t, gjson.GetBytes(upstreamBody, "service_tier").Exists())
		require.Nil(t, result.ServiceTier)
		return
	}

	require.Equal(t, want, gjson.GetBytes(upstreamBody, "service_tier").String())
	require.NotNil(t, result.ServiceTier)
	require.Equal(t, want, *result.ServiceTier)
}

func serviceTierPolicyResponsesResponse(model string) *http.Response {
	body := strings.Join([]string{
		`data: {"type":"response.completed","response":{"id":"resp_service_tier","object":"response","model":"` + model + `","status":"completed","output":[{"type":"message","id":"msg_1","role":"assistant","status":"completed","content":[{"type":"output_text","text":"ok"}]}],"usage":{"input_tokens":5,"output_tokens":2,"total_tokens":7}}}`,
		"",
		"data: [DONE]",
		"",
	}, "\n")
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func serviceTierPolicyChatCompletionsResponse(model string) *http.Response {
	body := `{"id":"chatcmpl_service_tier","object":"chat.completion","model":"` + model + `","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":5,"completion_tokens":2,"total_tokens":7}}`
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}
