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

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestForwardAsClaude52MappedModel(t *testing.T) {
	for _, model := range []string{"claude-fable-5-2", "claude-mythos-5-2"} {
		for _, chat := range []bool{false, true} {
			for _, stream := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/chat=%v/stream=%v", model, chat, stream), func(t *testing.T) {
					account := &Account{ID: 201, Platform: PlatformAnthropic, Type: AccountTypeAPIKey, Credentials: map[string]any{
						"api_key": "fixture-key", "model_mapping": map[string]any{"client-alias": model},
					}}
					upstream := &anthropicHTTPUpstreamRecorder{resp: &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(claude52SignedStream()))}}
					svc := &GatewayService{cfg: &config.Config{}, httpUpstream: upstream}
					body := fmt.Sprintf(`{"model":"client-alias","input":"hello","stream":%v}`, stream)
					path := "/v1/responses"
					if chat {
						body = fmt.Sprintf(`{"model":"client-alias","messages":[{"role":"user","content":"hello"}],"stream":%v}`, stream)
						path = "/v1/chat/completions"
					}
					rec := httptest.NewRecorder()
					c, _ := gin.CreateTestContext(rec)
					c.Request = httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
					var result *ForwardResult
					var err error
					if chat {
						result, err = svc.ForwardAsChatCompletions(context.Background(), c, account, []byte(body), nil)
					} else {
						result, err = svc.ForwardAsResponses(context.Background(), c, account, []byte(body), nil)
					}
					require.NoError(t, err)
					require.Equal(t, model, gjson.GetBytes(upstream.lastBody, "model").String())
					require.Equal(t, "adaptive", gjson.GetBytes(upstream.lastBody, "thinking.type").String())
					require.Equal(t, "high", gjson.GetBytes(upstream.lastBody, "output_config.effort").String())
					require.Equal(t, "client-alias", result.Model)
					require.Equal(t, model, result.UpstreamModel)
					require.Equal(t, 10, result.Usage.InputTokens)
					require.Equal(t, 5, result.Usage.OutputTokens)
					if !chat {
						require.Contains(t, rec.Body.String(), "anthropic-thinking-v1:")
					}
				})
			}
		}
	}
}

func claude52SignedStream() string {
	return strings.Join([]string{
		`event:message_start`,
		`data:{"type":"message_start","message":{"id":"msg_signed","model":"client-alias","content":[],"usage":{"input_tokens":10}}}`,
		``, `event:content_block_start`,
		`data:{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":""}}`,
		``, `event:content_block_delta`,
		`data:{"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"signed-"}}`,
		``, `event:content_block_delta`,
		`data:{"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"history"}}`,
		``, `event:content_block_stop`, `data:{"type":"content_block_stop","index":0}`,
		``, `event:message_delta`, `data:{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":5}}`,
		``, `event:message_stop`, `data:{"type":"message_stop"}`, ``,
	}, "\n")
}

func TestForwardAsClaude52RejectsInvalidEffortBeforeUpstream(t *testing.T) {
	for _, chat := range []bool{false, true} {
		account := &Account{ID: 201, Platform: PlatformAnthropic, Type: AccountTypeAPIKey, Credentials: map[string]any{
			"api_key": "fixture-key", "model_mapping": map[string]any{"client-alias": "claude-fable-5-2"},
		}}
		upstream := &anthropicHTTPUpstreamRecorder{}
		svc := &GatewayService{cfg: &config.Config{}, httpUpstream: upstream}
		body := `{"model":"client-alias","input":"hello","reasoning":{"effort":"none"}}`
		if chat {
			body = `{"model":"client-alias","messages":[{"role":"user","content":"hello"}],"reasoning_effort":"none"}`
		}
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
		var err error
		if chat {
			_, err = svc.ForwardAsChatCompletions(context.Background(), c, account, []byte(body), nil)
		} else {
			_, err = svc.ForwardAsResponses(context.Background(), c, account, []byte(body), nil)
		}
		require.ErrorContains(t, err, "reasoning effort")
		require.Equal(t, http.StatusBadRequest, rec.Code)
		require.Nil(t, upstream.lastReq)
	}
}

func TestClaude52NormalizationKeepsSamplingUnset(t *testing.T) {
	for _, model := range []string{"claude-fable-5-2", "claude-mythos-5-2"} {
		body := []byte(fmt.Sprintf(`{"model":%q,"messages":[],"top_p":0.99}`, model))
		out, _ := normalizeClaudeOAuthRequestBody(body, model, claudeOAuthNormalizeOptions{})
		require.False(t, gjson.GetBytes(out, "temperature").Exists())
		require.Equal(t, 0.99, gjson.GetBytes(out, "top_p").Float())
	}
}
