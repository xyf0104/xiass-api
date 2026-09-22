package service

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

const openAIModelGuardSecret = "ORIGINAL_LUNA_TEXT"

func openAIModelGuardResponsesSSE(createdModel, completedModel string) string {
	created := `{"type":"response.created","response":{"id":"resp_guard","status":"in_progress","output":[]`
	if createdModel != "" {
		created += `,"model":` + fmt.Sprintf("%q", createdModel)
	}
	created += `}}`
	completed := fmt.Sprintf(`{"type":"response.completed","response":{"id":"resp_guard","model":%q,"status":"completed","output":[],"usage":{"input_tokens":7,"output_tokens":3,"total_tokens":10}}}`, completedModel)
	return "data: " + created + "\n\n" +
		`data: {"type":"response.output_text.delta","delta":"` + openAIModelGuardSecret + `"}` + "\n\n" +
		"data: " + completed + "\n\n"
}

func openAIModelGuardContext(t *testing.T, userID, keyID int64) (*gin.Context, *httptest.ResponseRecorder, context.Context) {
	t.Helper()
	ctx := modelRotationTestContext(userID, keyID)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewBufferString(`{"model":"gpt-6-astra","stream":true}`)).WithContext(ctx)
	return c, rec, ctx
}

func openAIModelGuardResponse(body string) *http.Response {
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(body))}
}

func runOpenAIModelGuardStream(t *testing.T, path string, enabled bool, lateModel bool) (string, OpenAIUsage) {
	t.Helper()
	previousForwardingSettings := gatewayForwardingCache.Load()
	t.Cleanup(func() {
		if previousForwardingSettings != nil {
			gatewayForwardingCache.Store(previousForwardingSettings)
		}
	})
	gatewayForwardingCache.Store(&cachedGatewayForwardingSettings{openAITTFTMode: OpenAITTFTModeSemantic, expiresAt: time.Now().Add(time.Minute).UnixNano()})
	svc, key, accounts := modelRotationTestGateway(t, "advanced")
	if !enabled {
		settings, err := svc.settingService.GetOpenAIModelPrioritySettings(context.Background())
		require.NoError(t, err)
		settings.SmartRotationEnabled = false
		require.NoError(t, svc.settingService.SetOpenAIModelPrioritySettings(context.Background(), settings))
	}
	account := &accounts[0]
	c, rec, ctx := openAIModelGuardContext(t, key.UserID, key.ID)
	createdModel := "gpt-5.6-luna"
	if lateModel {
		createdModel = ""
	}
	body := openAIModelGuardResponsesSSE(createdModel, "gpt-5.6-luna")
	var usage OpenAIUsage
	switch path {
	case "responses":
		result, err := svc.handleStreamingResponse(ctx, openAIModelGuardResponse(body), c, account, time.Now(), "gpt-6-astra", "gpt-6-astra")
		require.NoError(t, err)
		usage = *result.usage
	case "passthrough":
		result, err := svc.handleStreamingResponsePassthrough(ctx, openAIModelGuardResponse(body), c, account, time.Now(), "gpt-6-astra", "gpt-6-astra")
		require.NoError(t, err)
		usage = *result.usage
	case "chat":
		result, err := svc.handleChatStreamingResponse(openAIModelGuardResponse(body), c, account, "gpt-6-astra", "gpt-6-astra", "gpt-6-astra", time.Now(), 100)
		require.NoError(t, err)
		usage = result.Usage
	case "raw":
		raw := fmt.Sprintf("data: {\"id\":\"chat_guard\",\"object\":\"chat.completion.chunk\",\"model\":%q,\"choices\":[{\"index\":0,\"delta\":{\"content\":%q},\"finish_reason\":null}]}\n\n", createdModel, openAIModelGuardSecret)
		raw += `data: {"id":"chat_guard","object":"chat.completion.chunk","model":"gpt-5.6-luna","choices":[],"usage":{"prompt_tokens":7,"completion_tokens":3,"total_tokens":10}}` + "\n\n"
		raw += "data: [DONE]\n\n"
		result, err := svc.streamRawChatCompletions(c, openAIModelGuardResponse(raw), account, "gpt-6-astra", "gpt-6-astra", "gpt-6-astra", nil, nil, time.Now(), 100)
		require.NoError(t, err)
		usage = result.Usage
	default:
		t.Fatalf("unknown path %s", path)
	}
	return rec.Body.String(), usage
}

func TestOpenAIModelResponseGuardStreamingEmissionBoundary(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, path := range []string{"responses", "passthrough", "chat", "raw"} {
		t.Run(path+"/early_mismatch", func(t *testing.T) {
			body, usage := runOpenAIModelGuardStream(t, path, true, false)
			require.NotContains(t, body, openAIModelGuardSecret)
			require.Contains(t, body, "model_mismatch")
			require.Contains(t, body, "data: [DONE]")
			require.Equal(t, 7, usage.InputTokens)
			require.Equal(t, 3, usage.OutputTokens)
		})
		t.Run(path+"/disabled", func(t *testing.T) {
			body, _ := runOpenAIModelGuardStream(t, path, false, false)
			require.Contains(t, body, openAIModelGuardSecret)
			require.NotContains(t, body, "model_mismatch")
		})
		t.Run(path+"/late_model", func(t *testing.T) {
			body, _ := runOpenAIModelGuardStream(t, path, true, true)
			require.Contains(t, body, openAIModelGuardSecret)
			require.NotContains(t, body, "model_mismatch")
		})
	}
}

func TestOpenAIModelResponseMismatchObserverMakesNextSelectionImmediate(t *testing.T) {
	svc, key, accounts := modelRotationTestGateway(t, "advanced")
	baseCtx := modelRotationTestContext(key.UserID, key.ID)
	startedAt := time.Now().Add(-time.Second)
	observed := 0
	ctx := WithOpenAIModelResponseMismatchStartedAt(baseCtx, startedAt)
	ctx = WithOpenAIModelResponseMismatchObserver(ctx, func(requestedModel, upstreamModel, responseModel string, observedAt time.Time) {
		observed++
		svc.ObserveOpenAIModelRotationImmediate(baseCtx, key, &accounts[0], requestedModel, &OpenAIForwardResult{
			UpstreamModel: upstreamModel, UpstreamResponseModel: responseModel, Duration: time.Since(observedAt),
		})
	})
	notifyOpenAIModelResponseMismatch(ctx, "gpt-6-astra", "gpt-6-astra", "gpt-5.6-luna", time.Time{})
	require.Equal(t, 1, observed)

	selected, _, err := svc.SelectAccountWithScheduler(baseCtx, key.GroupID, "", "", "gpt-6-astra", nil, OpenAIUpstreamTransportAny, false)
	require.NoError(t, err)
	require.Equal(t, int64(32), selected.Account.ID)
	if selected.ReleaseFunc != nil {
		selected.ReleaseFunc()
	}

	// The handler's normal post-forward Observe call is a no-op after the
	// request-level callback has recorded the same attempt.
	svc.ObserveOpenAIModelRotation(ctx, key, &accounts[0], "gpt-6-astra", &OpenAIForwardResult{UpstreamResponseModel: "gpt-5.6-luna"})
	selected, _, err = svc.SelectAccountWithScheduler(baseCtx, key.GroupID, "", "", "gpt-6-astra", nil, OpenAIUpstreamTransportAny, false)
	require.NoError(t, err)
	require.Equal(t, int64(32), selected.Account.ID)
	if selected.ReleaseFunc != nil {
		selected.ReleaseFunc()
	}
}

func TestOpenAIModelResponseGuardNonStreamingPreservesImageAccountingWithoutResponseAffinity(t *testing.T) {
	gin.SetMode(gin.TestMode)
	jsonBody := `{"id":"resp_rejected_image","model":"gpt-5.6-luna","status":"completed","output":[{"id":"ig_1","type":"image_generation_call","result":"final-image","size":"1024x1024"}],"usage":{"input_tokens":7,"output_tokens":3,"total_tokens":10}}`
	sseBody := `data: {"type":"response.created","response":{"id":"resp_rejected_image","model":"gpt-5.6-luna","status":"in_progress","output":[]}}` + "\n\n" +
		`data: {"type":"response.completed","response":` + jsonBody + `}` + "\n\n"

	for _, path := range []string{"responses_json", "responses_sse", "passthrough_json", "passthrough_sse"} {
		t.Run(path, func(t *testing.T) {
			previousForwardingSettings := gatewayForwardingCache.Load()
			t.Cleanup(func() {
				if previousForwardingSettings != nil {
					gatewayForwardingCache.Store(previousForwardingSettings)
				}
			})
			gatewayForwardingCache.Store(&cachedGatewayForwardingSettings{openAITTFTMode: OpenAITTFTModeSemantic, expiresAt: time.Now().Add(time.Minute).UnixNano()})
			svc, key, accounts := modelRotationTestGateway(t, "advanced")
			account := &accounts[0]
			c, rec, ctx := openAIModelGuardContext(t, key.UserID, key.ID)
			var usage *OpenAIUsage
			var responseID string
			var imageCount int
			var imageSizes []string
			switch path {
			case "responses_json":
				result, err := svc.handleNonStreamingResponse(ctx, openAIModelGuardResponseJSON(jsonBody), c, account, "gpt-6-astra", "gpt-6-astra")
				require.NoError(t, err)
				usage, responseID, imageCount, imageSizes = result.usage, result.responseID, result.imageCount, result.imageOutputSizes
			case "responses_sse":
				observeOpenAISSEBody(beginUpstreamResponseModelObservation(c), sseBody)
				result, err := svc.handleSSEToJSON(openAIModelGuardResponse(sseBody), c, account, []byte(sseBody), "gpt-6-astra", "gpt-6-astra")
				require.NoError(t, err)
				usage, responseID, imageCount, imageSizes = result.usage, result.responseID, result.imageCount, result.imageOutputSizes
			case "passthrough_json":
				result, err := svc.handleNonStreamingResponsePassthrough(ctx, openAIModelGuardResponseJSON(jsonBody), c, "gpt-6-astra", "gpt-6-astra", account)
				require.NoError(t, err)
				usage, responseID, imageCount, imageSizes = result.usage, result.responseID, result.imageCount, result.imageOutputSizes
			case "passthrough_sse":
				observeOpenAISSEBody(beginUpstreamResponseModelObservation(c), sseBody)
				result, err := svc.handlePassthroughSSEToJSON(openAIModelGuardResponse(sseBody), c, account, []byte(sseBody), "gpt-6-astra", "gpt-6-astra")
				require.NoError(t, err)
				usage, responseID, imageCount, imageSizes = result.usage, result.responseID, result.imageCount, result.imageOutputSizes
			}
			require.Equal(t, http.StatusBadGateway, rec.Code)
			require.Empty(t, responseID)
			require.Equal(t, 1, imageCount)
			require.Equal(t, []string{"1024x1024"}, imageSizes)
			require.Equal(t, 7, usage.InputTokens)
			require.Equal(t, 3, usage.OutputTokens)
		})
	}
}

func openAIModelGuardResponseJSON(body string) *http.Response {
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}
}
