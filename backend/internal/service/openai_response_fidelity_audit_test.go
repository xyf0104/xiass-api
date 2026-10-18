package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// All upstream traffic is replaced by in-memory bodies or the fake WS transport.
func fidelityAuditContext(stream bool) (*gin.Context, *httptest.ResponseRecorder) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(
		fmt.Sprintf(`{"model":"gpt-5.1","stream":%t,"input":"hello"}`, stream)))
	return c, rec
}

func fidelityAuditAccount() *Account {
	return &Account{
		ID: 918271, Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
		Status: StatusActive, Schedulable: true, Concurrency: 1,
		Credentials: map[string]any{"api_key": "audit-placeholder", "base_url": "https://upstream.invalid"},
		Extra:       map[string]any{"responses_websockets_v2_enabled": true},
	}
}

func fidelityAuditHTTPService() *OpenAIGatewayService {
	return &OpenAIGatewayService{cfg: &config.Config{}, toolCorrector: NewCodexToolCorrector()}
}

func fidelityAuditResponse(body io.ReadCloser) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       body,
	}
}

func TestResponseFidelityAudit_NonStreamingIncompleteReturnsJSON(t *testing.T) {
	c, rec := fidelityAuditContext(false)
	body := `data: {"type":"response.incomplete","response":{"id":"resp_partial","object":"response","status":"incomplete","incomplete_details":{"reason":"max_output_tokens"},"output":[{"type":"message","id":"msg_partial","role":"assistant","status":"incomplete","content":[{"type":"output_text","text":"partial answer","annotations":[]}]}],"usage":{"input_tokens":10,"output_tokens":2}}}` + "\n\n"
	result, err := fidelityAuditHTTPService().handleNonStreamingResponse(context.Background(),
		fidelityAuditResponse(io.NopCloser(strings.NewReader(body))), c, fidelityAuditAccount(), "gpt-5.1", "gpt-5.1")
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, 2, result.usage.OutputTokens)
	assert.True(t, gjson.ValidBytes(rec.Body.Bytes()), "stream=false must return a JSON response, got %s", rec.Body.String())
	assert.Contains(t, rec.Header().Get("Content-Type"), "application/json")
	assert.Equal(t, "resp_partial", result.responseID)
	if gjson.ValidBytes(rec.Body.Bytes()) {
		assert.Equal(t, "incomplete", gjson.GetBytes(rec.Body.Bytes(), "status").String())
		assert.Equal(t, "max_output_tokens", gjson.GetBytes(rec.Body.Bytes(), "incomplete_details.reason").String())
		assert.Equal(t, "partial answer", gjson.GetBytes(rec.Body.Bytes(), "output.0.content.0.text").String())
	}
}

func TestResponseFidelityAudit_SSEEventNamePreservesOutput(t *testing.T) {
	c, rec := fidelityAuditContext(true)
	body := "event: response.output_text.delta\ndata: {\"item_id\":\"msg_audit\",\"output_index\":0,\"content_index\":0,\"delta\":\"kept text\"}\n\n" +
		"event: response.completed\ndata: {\"response\":{\"id\":\"resp_audit\",\"status\":\"completed\",\"output\":[],\"usage\":{\"input_tokens\":10,\"output_tokens\":2}}}\n\n"
	result, err := fidelityAuditHTTPService().handleStreamingResponse(context.Background(),
		fidelityAuditResponse(io.NopCloser(strings.NewReader(body))), c, fidelityAuditAccount(), time.Now(), "gpt-5.1", "gpt-5.1")
	assert.NoError(t, err, "SSE event: carries the event type when the JSON payload omits type")
	assert.Contains(t, rec.Body.String(), "kept text", "valid semantic output must not be discarded as pre-output failure")
	require.NotNil(t, result)
	assert.Equal(t, 2, result.usage.OutputTokens)
}

func TestResponseFidelityAudit_RecoverCompletedImageBeforeStreamError(t *testing.T) {
	body := "data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_image_recover\",\"model\":\"gpt-5.6-sol\"}}\n\n" +
		"data: {\"type\":\"response.output_item.done\",\"item\":{\"id\":\"ig_recover\",\"type\":\"image_generation_call\",\"status\":\"generating\",\"result\":\"dGVzdA==\",\"output_format\":\"png\"}}\n\n"

	for _, passthrough := range []bool{false, true} {
		name := "native"
		if passthrough {
			name = "passthrough"
		}
		t.Run(name, func(t *testing.T) {
			c, rec := fidelityAuditContext(true)
			resp := fidelityAuditResponse(&openAIStreamReadThenErrorCloser{
				reader: strings.NewReader(body),
				err:    errors.New("http2: client connection lost"),
			})
			svc := fidelityAuditHTTPService()
			if passthrough {
				result, err := svc.handleStreamingResponsePassthrough(context.Background(), resp, c, fidelityAuditAccount(), time.Now(), "gpt-5.6-sol", "gpt-5.6-sol")
				require.NoError(t, err)
				require.NotNil(t, result)
			} else {
				result, err := svc.handleStreamingResponse(context.Background(), resp, c, fidelityAuditAccount(), time.Now(), "gpt-5.6-sol", "gpt-5.6-sol")
				require.NoError(t, err)
				require.NotNil(t, result)
			}
			responseBody := rec.Body.String()
			require.Contains(t, responseBody, "event: response.completed")
			require.Contains(t, responseBody, `"type":"image_generation_call"`)
			require.Contains(t, responseBody, `"result":"dGVzdA=="`)
		})
	}
}

func TestResponseFidelityAudit_CompletedDoesNotBecomeIdleTimeout(t *testing.T) {
	for _, terminalType := range []string{"response.completed", "response.incomplete"} {
		t.Run(terminalType, func(t *testing.T) {
			c, rec := fidelityAuditContext(true)
			svc := fidelityAuditHTTPService()
			svc.cfg.Gateway.StreamDataIntervalTimeout = 1
			reader, writer := io.Pipe()
			writeDone := make(chan struct{})
			go func() {
				defer close(writeDone)
				_, _ = fmt.Fprintf(writer, "data: {\"type\":%q,\"response\":{\"id\":\"resp_finished\",\"output\":[{\"type\":\"message\",\"content\":[{\"type\":\"output_text\",\"text\":\"already delivered\"}]}],\"usage\":{\"input_tokens\":1,\"output_tokens\":2}}}\n\n", terminalType)
				// The HTTP connection stays open after a complete SSE terminal frame.
			}()
			result, err := svc.handleStreamingResponse(context.Background(), fidelityAuditResponse(reader),
				c, fidelityAuditAccount(), time.Now(), "gpt-5.1", "gpt-5.1")
			_ = reader.Close()
			_ = writer.Close()
			<-writeDone
			assert.NoError(t, err, "a delivered terminal must not turn into a stream timeout")
			assert.NotContains(t, rec.Body.String(), "stream_timeout")
			assert.Contains(t, rec.Body.String(), "already delivered")
			require.NotNil(t, result)
			assert.Equal(t, 2, result.usage.OutputTokens)
		})
	}
}

func fidelityAuditWSService(t *testing.T, events [][]byte) *OpenAIGatewayService {
	t.Helper()
	cfg := newOpenAIWSV2TestConfig()
	cfg.Security.URLAllowlist.Enabled = false
	cfg.Gateway.OpenAIWS.MinIdlePerAccount = 0
	pool := newOpenAIWSConnPool(cfg)
	pool.setClientDialerForTest(&openAIWSCaptureDialer{conn: &openAIWSCaptureConn{events: events}})
	svc := &OpenAIGatewayService{
		cfg: cfg, cache: &stubGatewayCache{}, toolCorrector: NewCodexToolCorrector(),
		openaiWSPool: pool, openaiWSResolver: NewOpenAIWSProtocolResolver(cfg),
	}
	t.Cleanup(svc.CloseOpenAIWSPool)
	return svc
}

func fidelityAuditForwardWS(svc *OpenAIGatewayService, c *gin.Context, stream bool) (*OpenAIForwardResult, error) {
	return svc.forwardOpenAIWSV2(context.Background(), c, fidelityAuditAccount(),
		map[string]any{"model": "gpt-5.1", "stream": stream, "input": "hello"},
		"audit-placeholder", OpenAIWSProtocolDecision{}, false, stream, "gpt-5.1", "gpt-5.1", time.Now(), 1, "", nil)
}

func TestResponseFidelityAudit_WSNonStreamingPreservesDoneItems(t *testing.T) {
	c, rec := fidelityAuditContext(false)
	svc := fidelityAuditWSService(t, [][]byte{
		[]byte(`{"type":"response.output_text.delta","item_id":"msg_ws","output_index":0,"content_index":0,"delta":"kept WS text"}`),
		[]byte(`{"type":"response.output_item.done","output_index":0,"item":{"type":"message","id":"msg_ws","role":"assistant","status":"completed","content":[{"type":"output_text","text":"kept WS text","annotations":[]}]}}`),
		[]byte(`{"type":"response.output_item.done","output_index":1,"item":{"type":"function_call","id":"fc_ws","call_id":"call_ws","name":"lookup","arguments":"{}","status":"completed"}}`),
		[]byte(`{"type":"response.completed","response":{"id":"resp_ws","status":"completed","output":[],"usage":{"input_tokens":3,"output_tokens":4}}}`),
	})
	result, err := fidelityAuditForwardWS(svc, c, false)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, 4, result.Usage.OutputTokens)
	assert.Len(t, gjson.GetBytes(rec.Body.Bytes(), "output").Array(), 2, "WS stream=false must retain reported text and tool items")
	assert.Contains(t, rec.Body.String(), "kept WS text")
	assert.Contains(t, rec.Body.String(), "call_ws")
}

func TestResponseFidelityAudit_WSDoneItemPreventsReplay(t *testing.T) {
	items := map[string]string{
		"tool": `{"type":"function_call","id":"fc_done","call_id":"call_done","name":"lookup","arguments":"{}","status":"completed"}`,
		"text": `{"type":"message","id":"msg_done","role":"assistant","status":"completed","content":[{"type":"output_text","text":"kept done text","annotations":[]}]}`,
	}
	for name, item := range items {
		t.Run(name, func(t *testing.T) {
			c, rec := fidelityAuditContext(true)
			svc := fidelityAuditWSService(t, [][]byte{
				[]byte(`{"type":"response.output_item.done","output_index":0,"item":` + item + `}`),
			})
			_, err := fidelityAuditForwardWS(svc, c, true)
			require.Error(t, err, "EOF without a terminal should still be reported")
			reason, retryable := classifyOpenAIWSReconnectReason(err)
			assert.False(t, retryable, "completed semantic output must prevent replacement by a fresh attempt; reason=%s", reason)
			assert.True(t, c.Writer.Written(), "done-only semantic output must commit the attempt")
			assert.Contains(t, rec.Body.String(), item)
		})
	}
}

func TestResponseFidelityAudit_WSBufferedTerminalsPreserveRawTools(t *testing.T) {
	item := `{"type":"custom_tool_call","id":"ct_raw","call_id":"call_raw","name":"apply_patch","namespace":"workspace","input":"patch body","status":"completed","vendor":{"opaque":true}}`
	for _, terminal := range []string{"response.completed", "response.done", "response.incomplete", "response.cancelled", "response.canceled"} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream=%t", terminal, stream), func(t *testing.T) {
				c, rec := fidelityAuditContext(stream)
				status := strings.TrimPrefix(terminal, "response.")
				svc := fidelityAuditWSService(t, [][]byte{
					[]byte(`{"type":"response.output_item.done","output_index":0,"item":` + item + `}`),
					[]byte(fmt.Sprintf(`{"type":%q,"response":{"id":"resp_raw","status":%q,"output":[],"incomplete_details":{"reason":"max_output_tokens"},"usage":{"input_tokens":3,"output_tokens":4}}}`, terminal, status)),
				})
				result, err := fidelityAuditForwardWS(svc, c, stream)
				require.NoError(t, err)
				require.NotNil(t, result)
				response := rec.Body.Bytes()
				if stream {
					_, payload, ok := extractOpenAISSETerminalEvent(rec.Body.String())
					require.True(t, ok)
					response = []byte(gjson.GetBytes(payload, "response").Raw)
				}
				require.JSONEq(t, item, gjson.GetBytes(response, "output.0").Raw)
				require.Equal(t, status, gjson.GetBytes(response, "status").String())
				require.Equal(t, "max_output_tokens", gjson.GetBytes(response, "incomplete_details.reason").String())
				require.Equal(t, 4, result.Usage.OutputTokens)
				require.Nil(t, result.FirstTokenMs, "done items commit output without changing TTFT classification")
			})
		}
	}
}

func TestResponseFidelityAudit_WSDeltaOnlyReconstruction(t *testing.T) {
	c, rec := fidelityAuditContext(false)
	svc := fidelityAuditWSService(t, [][]byte{
		[]byte(`{"type":"response.output_text.delta","output_index":0,"delta":"delta text"}`),
		[]byte(`{"type":"response.output_item.added","output_index":1,"item":{"type":"function_call","call_id":"call_delta","name":"lookup","arguments":""}}`),
		[]byte(`{"type":"response.function_call_arguments.delta","output_index":1,"delta":"{\"key\":1}"}`),
		[]byte(`{"type":"response.completed","response":{"id":"resp_delta","output":[],"usage":{"input_tokens":3,"output_tokens":4}}}`),
	})
	_, err := fidelityAuditForwardWS(svc, c, false)
	require.NoError(t, err)
	require.Len(t, gjson.GetBytes(rec.Body.Bytes(), "output").Array(), 2)
	require.Equal(t, "delta text", gjson.GetBytes(rec.Body.Bytes(), "output.0.content.0.text").String())
	require.Equal(t, "call_delta", gjson.GetBytes(rec.Body.Bytes(), "output.1.call_id").String())
	require.JSONEq(t, `{"key":1}`, gjson.GetBytes(rec.Body.Bytes(), "output.1.arguments").String())
}

func TestResponseFidelityAudit_WSFailureAfterOutputNeverReconnects(t *testing.T) {
	for _, stream := range []bool{false, true} {
		for _, tail := range []string{"", `{"type":"error","error":{"code":"server_error","message":"retry later"}}`, `{"type":`} {
			t.Run(fmt.Sprintf("stream=%t/tail=%s", stream, tail), func(t *testing.T) {
				c, _ := fidelityAuditContext(stream)
				events := [][]byte{[]byte(`{"type":"response.output_item.done","output_index":0,"item":{"type":"function_call","id":"fc_done","call_id":"call_done","name":"lookup","arguments":"{}","status":"completed"}}`)}
				if tail != "" {
					events = append(events, []byte(tail))
				}
				svc := fidelityAuditWSService(t, events)
				_, err := svc.Forward(context.Background(), c, fidelityAuditAccount(), []byte(fmt.Sprintf(`{"model":"gpt-5.1","stream":%t,"input":"hello"}`, stream)))
				require.Error(t, err)
				_, retryable := classifyOpenAIWSReconnectReason(err)
				require.False(t, retryable)
				require.Zero(t, svc.SnapshotOpenAIWSRetryMetrics().RetryAttemptsTotal)
			})
		}
	}
}

func TestResponseFidelityAudit_WSFailurePreservesPartialBilling(t *testing.T) {
	c, rec := fidelityAuditContext(true)
	svc := fidelityAuditWSService(t, [][]byte{
		[]byte(`{"type":"response.output_item.done","output_index":0,"item":{"type":"message","id":"msg_billed","role":"assistant","status":"completed","content":[{"type":"output_text","text":"billed text"}]}}`),
		[]byte(`{"type":"error","error":{"code":"server_error","message":"retry later"},"usage":{"input_tokens":7,"output_tokens":3,"input_tokens_details":{"cached_tokens":2}}}`),
	})
	result, err := svc.Forward(context.Background(), c, fidelityAuditAccount(), []byte(`{"model":"gpt-5.1","stream":true,"input":"hello"}`))
	require.Error(t, err)
	require.NotNil(t, result)
	require.Equal(t, 7, result.Usage.InputTokens)
	require.Equal(t, 3, result.Usage.OutputTokens)
	require.Equal(t, 2, result.Usage.CacheReadInputTokens)
	require.NotEmpty(t, result.BillingModel)
	require.False(t, result.SucceededForScheduling())
	require.Contains(t, rec.Body.String(), "billed text")
	require.Zero(t, svc.SnapshotOpenAIWSRetryMetrics().RetryAttemptsTotal)
}

func TestResponseFidelityAudit_WSPreambleStillAllowsReplay(t *testing.T) {
	c, rec := fidelityAuditContext(true)
	svc := fidelityAuditWSService(t, [][]byte{[]byte(`{"type":"response.created","response":{"id":"resp_preamble","output":[]}}`)})
	result, err := fidelityAuditForwardWS(svc, c, true)
	require.Error(t, err)
	_, retryable := classifyOpenAIWSReconnectReason(err)
	require.True(t, retryable)
	require.Nil(t, result)
	require.Empty(t, rec.Body.String())
}

func TestResponseFidelityAudit_BufferedSSEEventNamesAndIncomplete(t *testing.T) {
	c, rec := fidelityAuditContext(false)
	body := "event: response.output_item.done\ndata: {\"output_index\":0,\"item\":{\"type\":\"function_call\",\"id\":\"fc_named\",\"call_id\":\"call_named\",\"name\":\"lookup\",\"namespace\":\"local\",\"arguments\":\"{}\"}}\n\n" +
		"event: response.incomplete\ndata: {\"response\":{\"id\":\"resp_named\",\"status\":\"incomplete\",\"output\":[],\"incomplete_details\":{\"reason\":\"max_output_tokens\"},\"usage\":{\"input_tokens\":3,\"output_tokens\":4}}}\n\n"
	result, err := fidelityAuditHTTPService().handleNonStreamingResponse(context.Background(), fidelityAuditResponse(io.NopCloser(strings.NewReader(body))), c, fidelityAuditAccount(), "gpt-5.1", "gpt-5.1")
	require.NoError(t, err)
	require.Equal(t, "resp_named", result.responseID)
	require.Equal(t, 4, result.usage.OutputTokens)
	require.Equal(t, "call_named", gjson.GetBytes(rec.Body.Bytes(), "output.0.call_id").String())
	require.Equal(t, "local", gjson.GetBytes(rec.Body.Bytes(), "output.0.namespace").String())
	require.Equal(t, "incomplete", gjson.GetBytes(rec.Body.Bytes(), "status").String())
}

func TestResponseFidelityAudit_SSEEventNameResetsAndJSONTypeWins(t *testing.T) {
	body := "event: response.incomplete\n\ndata: {\"response\":{\"id\":\"untyped\"}}\n\n" +
		"event: response.failed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"typed\"}}\n\n" +
		"event: response.incomplete\ndata: {\"response\":{\"id\":\"named\"}}"
	var payloads []json.RawMessage
	forEachOpenAISSEDataPayload(body, func(payload []byte) { payloads = append(payloads, append(json.RawMessage(nil), payload...)) })
	require.Len(t, payloads, 3)
	require.False(t, gjson.GetBytes(payloads[0], "type").Exists(), "empty frames must reset event-name state")
	require.Equal(t, "response.completed", gjson.GetBytes(payloads[1], "type").String())
	require.Equal(t, "response.incomplete", gjson.GetBytes(payloads[2], "type").String(), "EOF dispatches the last event")
}

func TestResponseFidelityAudit_NativeSSEEmptyFrameResetsEventName(t *testing.T) {
	for _, timeout := range []int{0, 1} {
		t.Run(fmt.Sprintf("guard=%d", timeout), func(t *testing.T) {
			c, rec := fidelityAuditContext(true)
			svc := fidelityAuditHTTPService()
			svc.cfg.Gateway.OpenAIFirstOutputTimeoutSeconds = timeout
			body := "event: response.completed\n\ndata: {\"delta\":\"untyped content\"}\n\n" +
				"data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_final\",\"usage\":{\"input_tokens\":3,\"output_tokens\":4}}}\n\n"
			result, err := svc.handleStreamingResponse(context.Background(), fidelityAuditResponse(io.NopCloser(strings.NewReader(body))), c, fidelityAuditAccount(), time.Now(), "gpt-5.1", "gpt-5.1")
			require.NoError(t, err)
			require.Equal(t, 4, result.usage.OutputTokens)
			require.Contains(t, rec.Body.String(), "data: {\"delta\":\"untyped content\"}")
			require.Equal(t, 1, strings.Count(rec.Body.String(), `"type":"response.completed"`))
		})
	}
}

func TestResponseFidelityAudit_SSEErrorEventNamePreservesParam(t *testing.T) {
	c, rec := fidelityAuditContext(true)
	body := "event: error\ndata: {\"error\":{\"code\":\"invalid_request\",\"message\":\"bad tool parameter\",\"param\":\"tools.0.parameters\"}}\n\n" +
		"data: [DONE]\n\n"
	_, err := fidelityAuditHTTPService().handleStreamingResponse(context.Background(), fidelityAuditResponse(io.NopCloser(strings.NewReader(body))), c, fidelityAuditAccount(), time.Now(), "gpt-5.1", "gpt-5.1")
	require.NoError(t, err)
	var payload []byte
	forEachOpenAISSEDataPayload(rec.Body.String(), func(data []byte) { payload = append([]byte(nil), data...) })
	require.Equal(t, "error", gjson.GetBytes(payload, "type").String())
	require.Equal(t, "invalid_request", gjson.GetBytes(payload, "error.code").String())
	require.Equal(t, "tools.0.parameters", gjson.GetBytes(payload, "error.param").String())
}

func TestResponseFidelityAudit_SSETerminalStillDrainsTailUsage(t *testing.T) {
	c, rec := fidelityAuditContext(true)
	body := "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_tail\",\"usage\":{\"input_tokens\":3,\"output_tokens\":4}}}\n\n" +
		"data: {\"type\":\"response.done\",\"response\":{\"id\":\"resp_tail\",\"usage\":{\"input_tokens\":3,\"output_tokens\":7}}}\n\n" +
		"data: [DONE]\n\n"
	svc := fidelityAuditHTTPService()
	svc.cfg.Gateway.StreamDataIntervalTimeout = 1
	result, err := svc.handleStreamingResponse(context.Background(), fidelityAuditResponse(io.NopCloser(strings.NewReader(body))), c, fidelityAuditAccount(), time.Now(), "gpt-5.1", "gpt-5.1")
	require.NoError(t, err)
	require.Equal(t, 7, result.usage.OutputTokens)
	require.Contains(t, rec.Body.String(), `"type":"response.completed"`)
	require.Contains(t, rec.Body.String(), `"type":"response.done"`)
	require.Contains(t, rec.Body.String(), "data: [DONE]")
}
