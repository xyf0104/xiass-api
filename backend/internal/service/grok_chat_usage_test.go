//go:build unit

package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

const grokChatUsageFixture = `{"prompt_tokens":1233,"completion_tokens":1,"total_tokens":1677,"prompt_tokens_details":{"cached_tokens":1152},"completion_tokens_details":{"reasoning_tokens":443},"cost_in_usd_ticks":34020000}`

func TestForwardAsRawChatCompletions_GrokChatUsage(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name       string
		patch      map[string]any
		remove     string
		platform   string
		apiKey     bool
		wantOutput int
	}{
		{name: "separate_reasoning", wantOutput: 444},
		{name: "grok_api_key", apiKey: true, wantOutput: 444},
		{name: "already_included_reasoning", patch: map[string]any{"completion_tokens": 444}, wantOutput: 444},
		{name: "already_parsed_aggregate", patch: map[string]any{"output_tokens": 444}, wantOutput: 444},
		{name: "parsed_output_disagrees", patch: map[string]any{"output_tokens": 7}, wantOutput: 7},
		{name: "missing_total", remove: "total_tokens", wantOutput: 1},
		{name: "missing_prompt", remove: "prompt_tokens", patch: map[string]any{"total_tokens": 444}, wantOutput: 1},
		{name: "missing_completion", remove: "completion_tokens", patch: map[string]any{"total_tokens": 1676}, wantOutput: 0},
		{name: "missing_reasoning", remove: "completion_tokens_details.reasoning_tokens", wantOutput: 1},
		{name: "null_total", patch: map[string]any{"total_tokens": nil}, wantOutput: 1},
		{name: "string_total", patch: map[string]any{"total_tokens": "1677"}, wantOutput: 1},
		{name: "fractional_total", patch: map[string]any{"total_tokens": 1677.5}, wantOutput: 1},
		{name: "overflow_total", patch: map[string]any{"total_tokens": json.Number("18446744073709551616")}, wantOutput: 1},
		{name: "negative_prompt", patch: map[string]any{"prompt_tokens": -1, "total_tokens": 443}, wantOutput: 1},
		{name: "negative_completion", patch: map[string]any{"completion_tokens": -1, "total_tokens": 1675}, wantOutput: -1},
		{name: "negative_total", patch: map[string]any{"total_tokens": -1}, wantOutput: 1},
		{name: "negative_reasoning", patch: map[string]any{"completion_tokens_details.reasoning_tokens": -1, "total_tokens": 1233}, wantOutput: 1},
		{name: "zero_reasoning", patch: map[string]any{"completion_tokens_details.reasoning_tokens": 0, "total_tokens": 1234}, wantOutput: 1},
		{name: "fractional_prompt", patch: map[string]any{"prompt_tokens": 1233.5}, wantOutput: 1},
		{name: "fractional_completion", patch: map[string]any{"completion_tokens": 1.5}, wantOutput: 1},
		{name: "fractional_reasoning", patch: map[string]any{"completion_tokens_details.reasoning_tokens": 443.5}, wantOutput: 1},
		{name: "badcounts_gap", patch: map[string]any{"total_tokens": 1676}, wantOutput: 1},
		{name: "badcounts_total_below_prompt", patch: map[string]any{"total_tokens": 1232}, wantOutput: 1},
		{name: "badcounts_completion_exceeds_remainder", patch: map[string]any{"total_tokens": 1233}, wantOutput: 1},
		{name: "non_grok_openai", platform: PlatformOpenAI, wantOutput: 1},
	}

	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprintf("stream_%t", stream), func(t *testing.T) {
			for _, tt := range tests {
				t.Run(tt.name, func(t *testing.T) {
					usageJSON := grokChatUsageFixture
					for path, value := range tt.patch {
						var err error
						usageJSON, err = sjson.Set(usageJSON, path, value)
						require.NoError(t, err)
					}
					if tt.remove != "" {
						var err error
						usageJSON, err = sjson.Delete(usageJSON, tt.remove)
						require.NoError(t, err)
					}
					wantUsage, ok := openAIUsageFromGJSON(gjson.Parse(usageJSON))
					require.True(t, ok)
					wantUsage.OutputTokens = tt.wantOutput

					body := []byte(fmt.Sprintf(`{"model":"grok-4.7","messages":[{"role":"user","content":"hello"}],"stream":%t}`, stream))
					rec := httptest.NewRecorder()
					c, _ := gin.CreateTestContext(rec)
					c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
					c.Request.Header.Set("Content-Type", "application/json")

					contentType := "application/json"
					upstreamBody := `{"id":"chatcmpl_grok_usage","object":"chat.completion","model":"grok-4.7","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":` + usageJSON + `}`
					if stream {
						contentType = "text/event-stream"
						upstreamBody = "data: " + `{"id":"chatcmpl_grok_usage","object":"chat.completion.chunk","model":"grok-4.7","choices":[{"index":0,"delta":{"content":"ok"}}]}` + "\n\n" +
							"data: " + `{"id":"chatcmpl_grok_usage","object":"chat.completion.chunk","model":"grok-4.7","choices":[],"usage":` + usageJSON + "}\n\n" +
							"data: [DONE]\n\n"
					}
					upstream := &httpUpstreamRecorder{resp: &http.Response{
						StatusCode: http.StatusOK,
						Header:     http.Header{"Content-Type": []string{contentType}},
						Body:       io.NopCloser(strings.NewReader(upstreamBody)),
					}}
					svc := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig(), httpUpstream: upstream}
					account := healthyGrokOAuthGatewayTestAccount(471, "test-grok-access-token")
					if tt.platform != "" || tt.apiKey {
						account = rawChatCompletionsTestAccount()
						account.Platform = tt.platform
						if tt.apiKey {
							account.Platform = PlatformGrok
						}
					} else {
						repo := &grokQuotaAccountRepo{mockAccountRepoForPlatform: &mockAccountRepoForPlatform{
							accountsByID: map[int64]*Account{account.ID: account},
						}}
						svc.grokTokenProvider = NewGrokTokenProvider(repo, nil)
						svc.accountRepo = repo
					}

					result, err := svc.forwardAsRawChatCompletions(context.Background(), c, account, body, "")
					require.NoError(t, err)
					require.NotNil(t, result)
					require.NotNil(t, upstream.lastReq)
					require.True(t, strings.HasSuffix(upstream.lastReq.URL.Path, "/chat/completions"))
					require.Equal(t, wantUsage, result.Usage, "only the proven output count may change")
					require.Equal(t, 1152, result.Usage.CacheReadInputTokens)
					require.Equal(t, stream, result.Stream)
					require.Equal(t, http.StatusOK, rec.Code)
					require.Equal(t, upstreamBody, rec.Body.String(), "upstream JSON/SSE must remain byte-for-byte unchanged")
				})
			}
		})
	}
}

func TestNormalizeGrokChatUsage_LeavesOtherPlatformsUnchanged(t *testing.T) {
	for _, platform := range []string{PlatformOpenAI, PlatformAnthropic, PlatformGemini, PlatformAntigravity, "", "other"} {
		t.Run(platform, func(t *testing.T) {
			usage := OpenAIUsage{InputTokens: 1233, OutputTokens: 1, CacheReadInputTokens: 1152}
			before := usage
			normalizeGrokChatUsage(&Account{Platform: platform}, gjson.Parse(grokChatUsageFixture), &usage)
			require.Equal(t, before, usage)
		})
	}
}
