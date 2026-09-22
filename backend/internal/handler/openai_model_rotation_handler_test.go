//go:build unit

package handler

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

const (
	openAIModelRotationHandlerRequestedModel = "gpt-6-astra"
	openAIModelRotationHandlerFallbackModel  = "gpt-5.6-luna"
	openAIModelRotationHandlerOutput         = "raw-upstream-content-must-not-leak"
	openAIModelRotationHandlerMismatchNotice = "检测到该条回复已降智（luna模型），将不予采纳。请重新发起请求。下一次请求将轮询健康账号。"
)

type openAIModelRotationHandlerCall struct {
	accountID int64
	body      []byte
}

type openAIModelRotationHandlerUpstream struct {
	service.HTTPUpstream
	mu    sync.Mutex
	calls []openAIModelRotationHandlerCall
}

func (u *openAIModelRotationHandlerUpstream) Do(req *http.Request, _ string, accountID int64, _ int) (*http.Response, error) {
	body, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, err
	}
	u.mu.Lock()
	u.calls = append(u.calls, openAIModelRotationHandlerCall{accountID: accountID, body: append([]byte(nil), body...)})
	requestNumber := len(u.calls)
	u.mu.Unlock()

	declaredModel := openAIModelRotationHandlerFallbackModel
	if accountID == 2 {
		declaredModel = openAIModelRotationHandlerRequestedModel
	}
	responseID := fmt.Sprintf("resp_model_rotation_%d", requestNumber)
	usage := `"usage":{"input_tokens":2,"output_tokens":1,"total_tokens":3}`
	response := fmt.Sprintf(
		`{"id":%q,"object":"response","model":%q,"status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":%q}]}],%s}`,
		responseID,
		declaredModel,
		openAIModelRotationHandlerOutput,
		usage,
	)
	header := http.Header{"X-Request-Id": []string{fmt.Sprintf("rid-model-rotation-%d", requestNumber)}}
	if gjson.GetBytes(body, "stream").Bool() {
		header.Set("Content-Type", "text/event-stream")
		created := fmt.Sprintf(
			`{"type":"response.created","response":{"id":%q,"model":%q,"status":"in_progress","output":[]}}`,
			responseID,
			declaredModel,
		)
		completed := fmt.Sprintf(`{"type":"response.completed","response":%s}`, response)
		delta := fmt.Sprintf(`{"type":"response.output_text.delta","item_id":"msg_rotation","output_index":0,"content_index":0,"delta":%q}`, openAIModelRotationHandlerOutput)
		response = "event: response.created\ndata: " + created + "\n\n" +
			"event: response.output_text.delta\ndata: " + delta + "\n\n" +
			"event: response.completed\ndata: " + completed + "\n\n"
	} else {
		header.Set("Content-Type", "application/json")
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     header,
		Body:       io.NopCloser(bytes.NewBufferString(response)),
	}, nil
}

func (u *openAIModelRotationHandlerUpstream) snapshot() []openAIModelRotationHandlerCall {
	u.mu.Lock()
	defer u.mu.Unlock()
	out := make([]openAIModelRotationHandlerCall, len(u.calls))
	for i, call := range u.calls {
		out[i] = openAIModelRotationHandlerCall{accountID: call.accountID, body: append([]byte(nil), call.body...)}
	}
	return out
}

func newOpenAIModelRotationHandler(t *testing.T, rotationEnabled bool) (*OpenAIGatewayHandler, *openAIModelRotationHandlerUpstream, <-chan *service.UsageLog) {
	t.Helper()
	accounts := []service.Account{
		{
			ID: 1, Name: "rotation-account-1", Platform: service.PlatformOpenAI,
			Type: service.AccountTypeOAuth, Status: service.StatusActive, Schedulable: true,
			Priority: 10, Credentials: map[string]any{"access_token": "rotation-token-1"},
		},
		{
			ID: 2, Name: "rotation-account-2", Platform: service.PlatformOpenAI,
			Type: service.AccountTypeOAuth, Status: service.StatusActive, Schedulable: true,
			Priority: 0, Credentials: map[string]any{"access_token": "rotation-token-2"},
		},
	}
	accountRepo := openAIImagesFailoverAccountRepo{accounts: accounts}
	usageRepo := &openAIWSUsageHandlerUsageLogRepoStub{created: make(chan *service.UsageLog, 8)}
	settingRepo := &contentModerationHandlerSettingRepo{values: map[string]string{}}
	cfg := &config.Config{RunMode: config.RunModeSimple}
	cfg.Default.RateMultiplier = 1
	cfg.Security.URLAllowlist.Enabled = false
	settingService := service.NewSettingService(settingRepo, cfg)
	require.NoError(t, settingService.SetOpenAIModelPrioritySettings(context.Background(), &service.OpenAIModelPrioritySettings{
		Enabled:                      true,
		SmartRotationEnabled:         rotationEnabled,
		SmartRotationCooldownMinutes: 30,
		Rules: []service.OpenAIModelPriorityRule{{
			ModelPattern: openAIModelRotationHandlerRequestedModel,
			AccountIDs:   []int64{1, 2},
			AccountOrder: []int64{1, 2},
		}},
	}))

	upstream := &openAIModelRotationHandlerUpstream{}
	billingCacheService := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(billingCacheService.Stop)
	gatewayService := service.NewOpenAIGatewayService(
		accountRepo,
		usageRepo,
		nil,
		nil,
		nil,
		nil,
		nil,
		cfg,
		nil,
		nil,
		service.NewBillingService(cfg, nil),
		nil,
		billingCacheService,
		upstream,
		&service.DeferredService{},
		nil,
		nil,
		nil,
		nil,
		nil,
		settingService,
		nil,
	)
	handler := NewOpenAIGatewayHandler(
		gatewayService,
		service.NewConcurrencyService(nil),
		billingCacheService,
		&service.APIKeyService{},
		newUsageRecordTestPool(t),
		nil,
		nil,
		nil,
		cfg,
	)
	handler.maxAccountSwitches = 10
	return handler, upstream, usageRepo.created
}

func newOpenAIModelRotationHandlerContext(
	t *testing.T,
	userID int64,
	apiKeyID int64,
	stream bool,
) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	groupID := int64(7331)
	body := fmt.Sprintf(`{"model":%q,"stream":%t,"input":"hello"}`, openAIModelRotationHandlerRequestedModel, stream)
	requestContext := context.WithValue(context.Background(), ctxkey.UserID, userID)
	requestContext = context.WithValue(requestContext, ctxkey.APIKeyID, apiKeyID)
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewBufferString(body)).WithContext(requestContext)
	req.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = req
	user := &service.User{ID: userID, Status: service.StatusActive}
	c.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{
		ID: apiKeyID, UserID: userID, Status: service.StatusActive,
		GroupID: &groupID, User: user,
		Group: &service.Group{ID: groupID, Platform: service.PlatformOpenAI, Status: service.StatusActive},
	})
	c.Set(string(middleware2.ContextKeyUser), middleware2.AuthSubject{UserID: userID})
	return c, recorder
}

func receiveOpenAIModelRotationUsageLog(t *testing.T, usageLogs <-chan *service.UsageLog) *service.UsageLog {
	t.Helper()
	select {
	case usageLog := <-usageLogs:
		require.NotNil(t, usageLog)
		return usageLog
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for OpenAI model rotation usage log")
		return nil
	}
}

func TestOpenAIGatewayHandlerResponses_SmartRotationUsesRawResponseModelPerCaller(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, stream := range []bool{false, true} {
		name := "json"
		if stream {
			name = "sse"
		}
		t.Run(name, func(t *testing.T) {
			handler, upstream, usageLogs := newOpenAIModelRotationHandler(t, true)
			callers := []struct {
				userID   int64
				apiKeyID int64
			}{
				{userID: 1001, apiKeyID: 2001},
				{userID: 1001, apiKeyID: 2001},
				{userID: 1002, apiKeyID: 2002},
			}
			wantAccounts := []int64{1, 2, 1}
			wantDeclared := []string{
				openAIModelRotationHandlerFallbackModel,
				openAIModelRotationHandlerRequestedModel,
				openAIModelRotationHandlerFallbackModel,
			}
			usage := make([]*service.UsageLog, 0, len(callers))

			for i, caller := range callers {
				c, recorder := newOpenAIModelRotationHandlerContext(t, caller.userID, caller.apiKeyID, stream)
				handler.Responses(c)
				if wantDeclared[i] != openAIModelRotationHandlerRequestedModel {
					require.NotContains(t, recorder.Body.String(), openAIModelRotationHandlerOutput)
					require.Contains(t, recorder.Body.String(), openAIModelRotationHandlerMismatchNotice)
					if stream {
						require.Equal(t, http.StatusOK, recorder.Code)
						require.Contains(t, recorder.Body.String(), `"code":"model_mismatch"`)
						require.Equal(t, 1, bytes.Count(recorder.Body.Bytes(), []byte("data: [DONE]")))
						require.NotContains(t, recorder.Body.String(), `"type":"response.completed"`)
					} else {
						require.Equal(t, http.StatusBadGateway, recorder.Code)
						require.Contains(t, recorder.Header().Get("Content-Type"), "application/json")
						require.Equal(t, "model_mismatch", gjson.GetBytes(recorder.Body.Bytes(), "error.code").String())
					}
				} else if stream {
					require.Equal(t, http.StatusOK, recorder.Code)
					require.Contains(t, recorder.Body.String(), `"type":"response.completed"`)
					require.Contains(t, recorder.Body.String(), openAIModelRotationHandlerOutput)
				} else {
					require.Equal(t, http.StatusOK, recorder.Code)
					require.Equal(t, "completed", gjson.GetBytes(recorder.Body.Bytes(), "status").String())
					require.Equal(t, openAIModelRotationHandlerOutput, gjson.GetBytes(recorder.Body.Bytes(), "output.0.content.0.text").String())
				}
				calls := upstream.snapshot()
				require.Len(t, calls, i+1, "each HTTP request must produce exactly one upstream send")
				require.Equal(t, wantAccounts[i], calls[i].accountID)
				usage = append(usage, receiveOpenAIModelRotationUsageLog(t, usageLogs))
			}

			calls := upstream.snapshot()
			require.Len(t, calls, 3, "successful model mismatches must rotate only the next request, never replay the body")
			for i, call := range calls {
				require.Equal(t, openAIModelRotationHandlerRequestedModel, gjson.GetBytes(call.body, "model").String())
				require.True(t, gjson.GetBytes(call.body, "stream").Bool(), "OAuth Responses uses an upstream SSE transport for both client modes")
				input := gjson.GetBytes(call.body, "input")
				if input.IsArray() {
					require.Equal(t, "hello", gjson.GetBytes(call.body, "input.0.content").String())
				} else {
					require.Equal(t, "hello", input.String())
				}
				require.Equal(t, wantAccounts[i], call.accountID)
			}

			require.Len(t, usage, 3)
			for i, usageLog := range usage {
				require.Equal(t, callers[i].userID, usageLog.UserID)
				require.Equal(t, callers[i].apiKeyID, usageLog.APIKeyID)
				require.Equal(t, wantAccounts[i], usageLog.AccountID)
				require.Equal(t, openAIModelRotationHandlerRequestedModel, usageLog.RequestedModel)
				require.NotNil(t, usageLog.UpstreamResponseModel)
				require.Equal(t, wantDeclared[i], *usageLog.UpstreamResponseModel)
				require.Equal(t, stream, usageLog.Stream)
				require.Equal(t, 2, usageLog.InputTokens)
				require.Equal(t, 1, usageLog.OutputTokens)
			}
			select {
			case duplicate := <-usageLogs:
				t.Fatalf("handler recorded duplicate usage: %#v", duplicate)
			case <-time.After(150 * time.Millisecond):
			}
		})
	}
}

func TestOpenAIGatewayHandlerResponses_SmartRotationDisabledPreservesResponse(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprintf("stream=%t", stream), func(t *testing.T) {
			handler, upstream, usageLogs := newOpenAIModelRotationHandler(t, false)
			for i := 0; i < 2; i++ {
				c, recorder := newOpenAIModelRotationHandlerContext(t, 1001, 2001, stream)
				handler.Responses(c)
				require.Equal(t, http.StatusOK, recorder.Code)
				require.Contains(t, recorder.Body.String(), openAIModelRotationHandlerOutput)
				require.NotContains(t, recorder.Body.String(), openAIModelRotationHandlerMismatchNotice)
				calls := upstream.snapshot()
				require.Len(t, calls, i+1)
				require.Equal(t, int64(1), calls[i].accountID)
				usage := receiveOpenAIModelRotationUsageLog(t, usageLogs)
				require.Equal(t, int64(1), usage.AccountID)
				require.Equal(t, 1, usage.OutputTokens)
			}
		})
	}
}
