//go:build unit

package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestGatewayNativeCacheTTLResponseBoundary(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, stream := range []bool{false, true} {
		for _, details := range []string{"", `,"cache_creation":{"ephemeral_5m_input_tokens":20,"ephemeral_1h_input_tokens":200}`} {
			for _, target := range []string{"5m", "1h"} {
				t.Run(fmt.Sprintf("stream=%t/details=%t/%s", stream, details != "", target), func(t *testing.T) {
					message := `{"id":"msg_cache","model":"upstream-model","usage":{"input_tokens":500,"prompt_tokens":500,"cache_read_input_tokens":100,"cache_creation_input_tokens":150` + details + `}}`
					payload := message
					if stream {
						payload = "event: message_start\ndata: " + `{"type":"message_start","message":` + message + "}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"
					}
					rec := httptest.NewRecorder()
					ctx, _ := gin.CreateTestContext(rec)
					ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
					response := &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(payload))}
					account := &Account{ID: 1, Platform: PlatformAnthropic, Type: AccountTypeOAuth, Extra: map[string]any{"cache_ttl_override_enabled": true, "cache_ttl_override_target": target}}
					svc := newMinimalGatewayService()
					var usage *ClaudeUsage
					if stream {
						result, err := svc.handleStreamingResponse(context.Background(), response, ctx, account, time.Now(), "client-model", "upstream-model", false)
						require.NoError(t, err)
						require.NotNil(t, result)
						usage = result.usage
					} else {
						var err error
						usage, err = svc.handleNonStreamingResponse(context.Background(), response, ctx, account, "client-model", "upstream-model")
						require.NoError(t, err)
					}
					require.Equal(t, 250, usage.InputTokens)
					require.Equal(t, 150, usage.CacheCreationInputTokens)
					require.Equal(t, "upstream-model", observedUpstreamResponseModel(ctx))
					output := rec.Body.String()
					require.Contains(t, output, "client-model")
					require.Equal(t, details != "", strings.Contains(output, `"cache_creation":`))
					if !stream || details != "" {
						if target == "5m" {
							require.Equal(t, 150, usage.CacheCreation5mTokens)
							require.Zero(t, usage.CacheCreation1hTokens)
						} else {
							require.Equal(t, 150, usage.CacheCreation1hTokens)
							require.Zero(t, usage.CacheCreation5mTokens)
						}
					}
				})
			}
		}
	}
}

func TestResponsesTerminalUsageMatchesAnthropicOutput(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, stream := range []bool{false, true} {
		name := "buffered"
		if stream {
			name = "streaming"
		}
		t.Run(name, func(t *testing.T) {
			payload := "data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_cache\",\"model\":\"gpt-5.4\"}}\n\n" +
				`data: {"type":"response.completed","usage":{"input_tokens":20,"output_tokens":4,"cache_creation_input_tokens":4,"input_tokens_details":{"cached_tokens":5}},"response":{"id":"resp_cache","status":"completed","output":[],"usage":{"input_tokens":10,"output_tokens":2,"cache_creation_input_tokens":1,"input_tokens_details":{"cached_tokens":2}}}}` + "\n\n"
			recorder := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(recorder)
			ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
			response := &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(payload))}
			service := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig()}
			var result *OpenAIForwardResult
			var err error
			if stream {
				result, err = service.handleAnthropicStreamingResponse(response, ctx, rawChatCompletionsTestAccount(), "gpt-5.4", "gpt-5.4", "gpt-5.4", time.Now())
			} else {
				result, err = service.handleAnthropicBufferedStreamingResponse(response, ctx, rawChatCompletionsTestAccount(), "gpt-5.4", "gpt-5.4", "gpt-5.4", time.Now())
			}
			require.NoError(t, err)
			require.NotNil(t, result)
			require.Equal(t, 10, result.Usage.InputTokens)
			require.Equal(t, 2, result.Usage.OutputTokens)
			require.Equal(t, 1, result.Usage.CacheCreationInputTokens)
			require.Equal(t, 2, result.Usage.CacheReadInputTokens)
			tokens := UsageTokens{
				InputTokens:  result.Usage.InputTokens - result.Usage.CacheReadInputTokens - result.Usage.CacheCreationInputTokens,
				OutputTokens: result.Usage.OutputTokens, CacheCreationTokens: result.Usage.CacheCreationInputTokens,
				CacheReadTokens: result.Usage.CacheReadInputTokens,
			}
			require.Equal(t, 7, tokens.InputTokens)
			require.Equal(t, 1, tokens.CacheCreationTokens)
			outputUsage := gjson.Get(recorder.Body.String(), "usage")
			if stream {
				for _, line := range strings.Split(recorder.Body.String(), "\n") {
					if strings.HasPrefix(line, "data: ") {
						event := gjson.Parse(strings.TrimPrefix(line, "data: "))
						if event.Get("type").String() == "message_delta" {
							outputUsage = event.Get("usage")
						}
					}
				}
			}
			require.True(t, outputUsage.Exists())
			require.EqualValues(t, tokens.InputTokens, outputUsage.Get("input_tokens").Int())
			require.EqualValues(t, tokens.OutputTokens, outputUsage.Get("output_tokens").Int())
			require.EqualValues(t, tokens.CacheCreationTokens, outputUsage.Get("cache_creation_input_tokens").Int())
			require.EqualValues(t, tokens.CacheReadTokens, outputUsage.Get("cache_read_input_tokens").Int())
			require.False(t, outputUsage.Get("cache_creation").Exists())
		})
	}
}

func TestAnthropicCacheOnlyBufferedSSEUsage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, test := range []struct {
		name          string
		start         string
		delta         string
		wantAggregate int
	}{
		{"explicit zero transition", `{"cache_creation_input_tokens":100,"cache_creation":{"ephemeral_1h_input_tokens":200}}`, `{"cache_creation_input_tokens":0}`, 100},
		{"derived zero transition", `{"cache_creation":{"ephemeral_5m_input_tokens":20}}`, `{"cache_creation":{"ephemeral_5m_input_tokens":0}}`, 0},
		{"merged partial TTL", `{"cache_creation":{"ephemeral_5m_input_tokens":20}}`, `{"cache_creation_input_tokens":null,"cache_creation":{"ephemeral_5m_input_tokens":null,"ephemeral_1h_input_tokens":30}}`, 50},
		{"explicit zero partial TTL", `{"cache_creation_input_tokens":0,"cache_creation":{"ephemeral_5m_input_tokens":20}}`, `{"cache_creation_input_tokens":0,"cache_creation":{"ephemeral_1h_input_tokens":30}}`, 50},
	} {
		for _, adapter := range []string{"responses", "chat", "native responses", "native chat"} {
			for _, terminal := range []string{"message_stop"} {
				t.Run(test.name+"/"+adapter+"/"+terminal, func(t *testing.T) {
					stream := "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_cache\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"claude-sonnet-4\",\"content\":[],\"usage\":" + test.start + "}}\n\n"
					for range 2 {
						stream += "event: message_delta\ndata: {\"type\":\"message_delta\",\"usage\":" + test.delta + "}\n\n"
					}
					if terminal == "message_stop" {
						stream += "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"
					}
					recorder := httptest.NewRecorder()
					ctx, _ := gin.CreateTestContext(recorder)
					response := &http.Response{Body: io.NopCloser(strings.NewReader(stream))}
					var aggregate int
					var err error
					switch adapter {
					case "responses":
						var result *ForwardResult
						result, err = (&GatewayService{}).handleResponsesBufferedStreamingResponse(response, ctx, "claude-sonnet-4", "claude-sonnet-4", nil, time.Now(), apicompat.ResponsesClientToolMapping{})
						require.NotNil(t, result)
						aggregate = result.Usage.CacheCreationInputTokens
					case "chat":
						var result *ForwardResult
						result, err = (&GatewayService{}).handleCCBufferedFromAnthropic(response, ctx, "claude-sonnet-4", "claude-sonnet-4", nil, time.Now())
						require.NotNil(t, result)
						aggregate = result.Usage.CacheCreationInputTokens
					case "native responses":
						var result *OpenAIForwardResult
						result, err = (&OpenAIGatewayService{}).handleResponsesBufferedFromNativeAnthropic(response, ctx, "claude-sonnet-4", "claude-sonnet-4", "claude-sonnet-4", nil, time.Now(), apicompat.ResponsesClientToolMapping{})
						require.NotNil(t, result)
						aggregate = result.Usage.CacheCreationInputTokens
					case "native chat":
						var result *OpenAIForwardResult
						result, err = (&OpenAIGatewayService{}).handleCCBufferedFromNativeAnthropic(response, ctx, "claude-sonnet-4", "claude-sonnet-4", "claude-sonnet-4", nil, time.Now())
						require.NotNil(t, result)
						aggregate = result.Usage.CacheCreationInputTokens
					}
					require.Equal(t, test.wantAggregate, aggregate)
					require.NoError(t, err)
					require.Equal(t, http.StatusOK, recorder.Code)
					require.True(t, json.Valid(recorder.Body.Bytes()))
					inputPath := "usage.input_tokens"
					cachePath := "usage.cache_creation_input_tokens"
					if strings.HasSuffix(adapter, "chat") {
						inputPath = "usage.prompt_tokens"
						cachePath = "usage.prompt_tokens_details.cache_creation_tokens"
					}
					require.True(t, gjson.GetBytes(recorder.Body.Bytes(), inputPath).Exists())
					require.Equal(t, int64(test.wantAggregate), gjson.GetBytes(recorder.Body.Bytes(), inputPath).Int())
					require.Equal(t, int64(test.wantAggregate), gjson.GetBytes(recorder.Body.Bytes(), cachePath).Int())
				})
			}
		}
	}
}
