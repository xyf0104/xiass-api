//go:build unit

package service

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestOpenAIRecordUsageNativeClaudeCacheTTL(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, ttl := range []struct {
		name        string
		short, long int
		billedShort int
	}{
		{"mixed", 20, 30, 20},
		{"5m only", 50, 0, 50},
		{"1h only", 0, 50, 0},
		{"aggregate only", 0, 0, 50},
	} {
		for _, route := range []string{"messages", "chat", "responses"} {
			for _, stream := range []bool{false, true} {
				for _, withResolver := range []bool{false, true} {
					t.Run(fmt.Sprintf("%s/%s/stream=%t/resolver=%t", ttl.name, route, stream, withResolver), func(t *testing.T) {
						usage := `{"input_tokens":10,"output_tokens":2,"cache_read_input_tokens":7,"cache_creation_input_tokens":50`
						if ttl.short+ttl.long > 0 {
							usage += fmt.Sprintf(`,"cache_creation":{"ephemeral_5m_input_tokens":%d,"ephemeral_1h_input_tokens":%d}`, ttl.short, ttl.long)
						}
						message := `{"id":"msg_ttl","type":"message","role":"assistant","model":"k3","content":[],"usage":` + usage + `}}`
						payload, contentType := message, "application/json"
						if stream || route != "messages" {
							payload = "event: message_start\ndata: {\"type\":\"message_start\",\"message\":" + message + "}\n\n" +
								"event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":2}}\n\n" +
								"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"
							contentType = "text/event-stream"
						}
						upstream := &httpUpstreamRecorder{resp: &http.Response{
							StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{contentType}, "X-Request-Id": []string{"native-ttl"}},
							Body: io.NopCloser(strings.NewReader(payload)),
						}}
						forwarder := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig(), httpUpstream: upstream}
						account := nativeAnthropicReasoningTestAccount()
						body := []byte(fmt.Sprintf(`{"model":"k3","max_tokens":32,"messages":[{"role":"user","content":"hi"}],"stream":%t}`, stream))
						var result *OpenAIForwardResult
						var err error
						switch route {
						case "messages":
							result, err = forwarder.ForwardAsAnthropic(context.Background(), adaptiveProtocolTestContext("/v1/messages", body), account, body, "", "")
						case "chat":
							result, err = forwarder.ForwardAsChatCompletions(context.Background(), adaptiveProtocolTestContext("/v1/chat/completions", body), account, body, "", "")
						case "responses":
							body = []byte(fmt.Sprintf(`{"model":"k3","input":"hi","stream":%t}`, stream))
							result, err = forwarder.Forward(context.Background(), adaptiveProtocolTestContext("/v1/responses", body), account, body)
						}
						require.NoError(t, err)
						require.NotNil(t, result)
						require.Equal(t, ttl.short, result.Usage.CacheCreation5mTokens)
						require.Equal(t, ttl.long, result.Usage.CacheCreation1hTokens)

						repo := &openAIRecordUsageLogRepoStub{inserted: true}
						billingRepo := &openAIRecordUsageBillingRepoStub{result: &UsageBillingApplyResult{Applied: true}}
						svc := newOpenAIRecordUsageServiceForTest(repo, &openAIRecordUsageUserRepoStub{}, &openAIRecordUsageSubRepoStub{}, nil)
						svc.usageBillingRepo = billingRepo
						svc.billingService = NewBillingService(nil, newStubPricingServiceFromJSON(t, `{"k3":{"input_cost_per_token":0.000002,"output_cost_per_token":0.00001,"cache_read_input_token_cost":0.0000002,"cache_creation_input_token_cost":0.0000025,"cache_creation_input_token_cost_above_1hr":0.000004}}`))
						if withResolver {
							svc.resolver = NewModelPricingResolver(nil, svc.billingService)
						}
						group := &Group{ID: 17, Platform: PlatformKimi, RateMultiplier: 0.35}
						require.NoError(t, svc.RecordUsage(context.Background(), &OpenAIRecordUsageInput{
							Result: result, Account: account, User: &User{ID: 2007},
							APIKey: &APIKey{ID: 1007, GroupID: &group.ID, Group: group},
						}))
						require.NotNil(t, repo.lastLog)
						log := repo.lastLog
						cacheCost := float64(ttl.billedShort)*2.5e-6 + float64(ttl.long)*4e-6
						require.InDelta(t, cacheCost, log.CacheCreationCost, 1e-12)
						require.Equal(t, 10, log.InputTokens)
						require.Equal(t, 50, log.CacheCreationTokens)
						require.Equal(t, 7, log.CacheReadTokens)
						require.Equal(t, ttl.short, log.CacheCreation5mTokens)
						require.Equal(t, ttl.long, log.CacheCreation1hTokens)
						require.Zero(t, log.CacheCreation30mTokens)
						require.Zero(t, log.CacheCreation12hTokens)
						require.InDelta(t, 10*2e-6, log.InputCost, 1e-12)
						require.InDelta(t, 2*10e-6, log.OutputCost, 1e-12)
						require.InDelta(t, 7*0.2e-6, log.CacheReadCost, 1e-12)
						total := 10*2e-6 + 2*10e-6 + 7*0.2e-6 + cacheCost
						require.InDelta(t, total, log.TotalCost, 1e-12)
						require.InDelta(t, total*0.35, log.ActualCost, 1e-12)
						require.NotNil(t, billingRepo.lastCmd)
						require.InDelta(t, QuantizeUsageBillingAmount(total*0.35), billingRepo.lastCmd.BalanceCost, 1e-12)
					})
				}
			}
		}
	}
}
