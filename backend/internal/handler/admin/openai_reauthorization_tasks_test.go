package admin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type openAIReauthorizationInvalidator struct {
	accountIDs []int64
}

type openAIReauthorizationProxyRepo struct {
	service.ProxyRepository
	proxy *service.Proxy
}

func (r *openAIReauthorizationProxyRepo) GetByID(context.Context, int64) (*service.Proxy, error) {
	return r.proxy, nil
}

func configureOpenAIReauthorizationProxy(t *testing.T, f *batchOAuthFixture, proxy service.Proxy) {
	t.Helper()
	f.h.openaiOAuthService = service.NewOpenAIOAuthService(&openAIReauthorizationProxyRepo{proxy: &proxy}, f.client)
	t.Cleanup(f.h.openaiOAuthService.Stop)
}

func (i *openAIReauthorizationInvalidator) InvalidateToken(_ context.Context, account *service.Account) error {
	if account != nil {
		i.accountIDs = append(i.accountIDs, account.ID)
	}
	return nil
}

func openAIReauthorizationRouter(h *OpenAIOAuthHandler, owner int64) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: owner})
		c.Set(string(middleware.ContextKeyUserRole), "admin")
		c.Next()
	})
	r.POST("/tasks", h.StartOpenAIReauthorizationTask)
	r.GET("/tasks", h.ListOpenAIReauthorizationTasks)
	r.GET("/tasks/:task_id", h.GetOpenAIReauthorizationTask)
	r.DELETE("/tasks/:task_id", h.DeleteOpenAIReauthorizationTask)
	r.POST("/tasks/:task_id/complete", h.CompleteOpenAIReauthorizationTask)
	r.POST("/tasks/:task_id/cancel", h.CancelOpenAIReauthorizationTask)
	r.POST("/tasks/:task_id/restart", h.RestartOpenAIReauthorizationTask)
	r.POST("/tasks/:task_id/adspower-launch", h.LaunchOpenAIReauthorizationTaskInAdsPower)
	return r
}

func reauthorizationAccount(id int64) *service.Account {
	proxyID := int64(9)
	trackingStartedAt := time.Date(2026, time.September, 15, 0, 0, 0, 0, time.UTC)
	return &service.Account{
		ID: id, Name: "Preserved account", Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth,
		Status: service.StatusError, ErrorMessage: "401 unauthorized", Schedulable: true,
		Concurrency: 7, Priority: 3, ProxyID: &proxyID, GroupIDs: []int64{11, 12},
		Credentials: map[string]any{
			"email": "owner@example.test", "access_token": "old-access", "refresh_token": "old-refresh",
			"base_url": "https://upstream.example.test", "model_mapping": map[string]any{"gpt-public": "gpt-upstream"},
			service.OpenAIOAuthReauthorizationEmailCredentialKey:      "owner@example.test",
			service.OpenAIOAuthReauthorizationPasswordCredentialKey:   "enc:c3RvcmVkLXBhc3N3b3Jk",
			service.OpenAIOAuthReauthorizationTOTPSecretCredentialKey: "enc:SkJTV1kzRFBFSFBLM1BYUA",
		},
		Extra: map[string]any{
			service.AccountExecutionNodeExtraKey: "api2", service.AccountPoolExtraKey: "14",
			"codex_fingerprint_mode": "device", "codex_fingerprint_seed": "38fb7d63-5ef9-4c06-bb74-21a42464636a",
			service.OpenAIReauthorizationStateExtraKey: service.OpenAIReauthorizationState{
				Version: 1, TrackingStartedAt: &trackingStartedAt,
				HistorySource: "xiass_state", HistoryConfidence: service.OpenAIReauthorizationHistoryExact,
			},
		},
	}
}

func TestOpenAIReauthorizationTaskUsesSavedLoginAndAccountProxy(t *testing.T) {
	f := newBatchOAuthFixture(t)
	t.Setenv("GATEWAY_EXECUTION_NODE_ID", "api2")
	f.admin.getAccountResult = reauthorizationAccount(448)
	proxy := service.Proxy{ID: 9, Name: "account-egress", Protocol: "http", Host: "proxy.example.test", Port: 8080, Username: "user", Password: "proxy-secret", Status: service.StatusActive}
	f.admin.proxies = []service.Proxy{proxy}
	configureOpenAIReauthorizationProxy(t, f, proxy)
	r := openAIReauthorizationRouter(f.h, 42)
	w := batchOAuthRequest(r, http.MethodPost, "/tasks", `{"account_id":448,"confirmed":true}`)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.Contains(t, w.Body.String(), `"mode":"reauthorization"`)
	require.Contains(t, w.Body.String(), `"target_account_id":448`)
	for _, secret := range []string{"stored-password", "JBSWY3DPEHPK3PXP", "proxy-secret", "callback_url", "oauth_session_id"} {
		require.NotContains(t, w.Body.String(), secret)
	}
	require.Len(t, f.requests, 1)
	require.Equal(t, "owner@example.test", f.requests[0]["email"])
	require.Equal(t, "stored-password", f.requests[0]["password"])
	require.Equal(t, "JBSWY3DPEHPK3PXP", f.requests[0]["totp_secret"])
	require.Equal(t, map[string]any{"server": "http://proxy.example.test:8080", "username": "user", "password": "proxy-secret"}, f.requests[0]["proxy"])
	require.Equal(t, 1, f.admin.openAIReauthorizationState.last.AttemptCount)
	require.Equal(t, service.OpenAIReauthorizationResultRunning, f.admin.openAIReauthorizationState.last.LastResult)
	require.Equal(t, service.OpenAIReauthorizationHistoryExact, f.admin.openAIReauthorizationState.last.HistoryConfidence)
}

func TestOpenAIReauthorizationAdsPowerModeKeepsHistoryAndSkipsServerBrowser(t *testing.T) {
	f := newBatchOAuthFixture(t)
	t.Setenv("GATEWAY_EXECUTION_NODE_ID", "api2")
	f.admin.getAccountResult = reauthorizationAccount(448)
	proxy := service.Proxy{ID: 9, Name: "account-egress", Protocol: "http", Host: "proxy.example.test", Port: 8080, Status: service.StatusActive}
	f.admin.proxies = []service.Proxy{proxy}
	configureOpenAIReauthorizationProxy(t, f, proxy)
	r := openAIReauthorizationRouter(f.h, 42)

	started := batchOAuthRequest(r, http.MethodPost, "/tasks", `{"account_id":448,"confirmed":true,"browser_mode":"adspower"}`)
	require.Equal(t, http.StatusOK, started.Code, started.Body.String())
	require.Contains(t, started.Body.String(), `"browser_mode":"adspower"`)
	require.Contains(t, started.Body.String(), `"stage":"external_browser"`)
	require.Zero(t, f.sidecarCalls.Load())
	require.Equal(t, 1, f.admin.openAIReauthorizationState.last.AttemptCount)

	var envelope struct {
		Data batchOAuthTask `json:"data"`
	}
	require.NoError(t, json.Unmarshal(started.Body.Bytes(), &envelope))
	launch := httptest.NewRecorder()
	launchRequest := httptest.NewRequest(http.MethodPost, "/tasks/"+envelope.Data.ID+"/adspower-launch", strings.NewReader(`{}`))
	launchRequest.Host = "127.0.0.1"
	launchRequest.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(launch, launchRequest)
	require.Equal(t, http.StatusOK, launch.Code, launch.Body.String())
	require.Equal(t, http.StatusConflict, batchOAuthRequest(r, http.MethodPost, "/tasks/"+envelope.Data.ID+"/adspower-launch", `{}`).Code)
}

func TestOpenAIReauthorizationTaskRequiresConfirmation(t *testing.T) {
	f := newBatchOAuthFixture(t)
	t.Setenv("GATEWAY_EXECUTION_NODE_ID", "api2")
	f.admin.getAccountResult = reauthorizationAccount(448)
	proxy := service.Proxy{ID: 9, Protocol: "http", Host: "proxy.example.test", Port: 8080, Status: service.StatusActive}
	f.admin.proxies = []service.Proxy{proxy}
	configureOpenAIReauthorizationProxy(t, f, proxy)

	w := batchOAuthRequest(openAIReauthorizationRouter(f.h, 42), http.MethodPost, "/tasks", `{"account_id":448}`)
	require.Equal(t, http.StatusConflict, w.Code, w.Body.String())
	require.Contains(t, w.Body.String(), "确认本次 401")
	require.Empty(t, f.requests)
	require.Zero(t, f.admin.openAIReauthorizationState.calls)
}

func TestOpenAIReauthorizationTaskAllowsCooldownOverrideOnlyAfterHighRiskConfirmation(t *testing.T) {
	f := newBatchOAuthFixture(t)
	t.Setenv("GATEWAY_EXECUTION_NODE_ID", "api2")
	account := reauthorizationAccount(448)
	succeededAt := time.Now().UTC().Add(-3 * 24 * time.Hour)
	account.Extra[service.OpenAIReauthorizationStateExtraKey] = service.OpenAIReauthorizationState{
		Version: 1, AttemptCount: 1, SuccessCount: 1,
		FirstAttemptAt: &succeededAt, LastAttemptAt: &succeededAt,
		FirstSucceededAt: &succeededAt, LastSucceededAt: &succeededAt,
		LastResult:    service.OpenAIReauthorizationResultSuccess,
		HistorySource: "xiass_state", HistoryConfidence: service.OpenAIReauthorizationHistoryExact,
	}
	f.admin.getAccountResult = account
	proxy := service.Proxy{ID: 9, Protocol: "http", Host: "proxy.example.test", Port: 8080, Status: service.StatusActive}
	f.admin.proxies = []service.Proxy{proxy}
	configureOpenAIReauthorizationProxy(t, f, proxy)

	router := openAIReauthorizationRouter(f.h, 42)
	rejected := batchOAuthRequest(router, http.MethodPost, "/tasks", `{"account_id":448,"confirmed":true}`)
	require.Equal(t, http.StatusConflict, rejected.Code, rejected.Body.String())
	require.Contains(t, rejected.Body.String(), "7 天冷静期")
	require.Contains(t, rejected.Body.String(), "高风险二次确认")
	require.Empty(t, f.requests)

	accepted := batchOAuthRequest(router, http.MethodPost, "/tasks", `{"account_id":448,"confirmed":true,"acknowledged_second_reauthorization_risk":true}`)
	require.Equal(t, http.StatusOK, accepted.Code, accepted.Body.String())
	require.Contains(t, accepted.Body.String(), `"reauthorization_number":2`)
	require.Len(t, f.requests, 1)
}

func TestOpenAIReauthorizationTaskRequiresHighRiskAcknowledgementAfterCooldown(t *testing.T) {
	f := newBatchOAuthFixture(t)
	t.Setenv("GATEWAY_EXECUTION_NODE_ID", "api2")
	account := reauthorizationAccount(448)
	succeededAt := time.Now().UTC().Add(-8 * 24 * time.Hour)
	account.Extra[service.OpenAIReauthorizationStateExtraKey] = service.OpenAIReauthorizationState{
		Version: 1, AttemptCount: 1, SuccessCount: 1,
		FirstAttemptAt: &succeededAt, LastAttemptAt: &succeededAt,
		FirstSucceededAt: &succeededAt, LastSucceededAt: &succeededAt,
		LastResult:    service.OpenAIReauthorizationResultSuccess,
		HistorySource: "xiass_state", HistoryConfidence: service.OpenAIReauthorizationHistoryExact,
	}
	f.admin.getAccountResult = account
	proxy := service.Proxy{ID: 9, Protocol: "http", Host: "proxy.example.test", Port: 8080, Status: service.StatusActive}
	f.admin.proxies = []service.Proxy{proxy}
	configureOpenAIReauthorizationProxy(t, f, proxy)
	router := openAIReauthorizationRouter(f.h, 42)

	rejected := batchOAuthRequest(router, http.MethodPost, "/tasks", `{"account_id":448,"confirmed":true}`)
	require.Equal(t, http.StatusConflict, rejected.Code, rejected.Body.String())
	require.Contains(t, rejected.Body.String(), "高风险二次确认")

	accepted := batchOAuthRequest(router, http.MethodPost, "/tasks", `{"account_id":448,"confirmed":true,"acknowledged_second_reauthorization_risk":true}`)
	require.Equal(t, http.StatusOK, accepted.Code, accepted.Body.String())
	require.Contains(t, accepted.Body.String(), `"reauthorization_number":2`)
	require.Len(t, f.requests, 1)
}

func TestOpenAIReauthorizationTaskUsesSeparateSavedEmailCodeLogin(t *testing.T) {
	f := newBatchOAuthFixture(t)
	t.Setenv("GATEWAY_EXECUTION_NODE_ID", "api2")
	account := reauthorizationAccount(449)
	delete(account.Credentials, service.OpenAIOAuthReauthorizationPasswordCredentialKey)
	delete(account.Credentials, service.OpenAIOAuthReauthorizationTOTPSecretCredentialKey)
	token := strings.Repeat("b", 64)
	ciphertext, err := f.h.secretEncryptor.Encrypt(token)
	require.NoError(t, err)
	account.Credentials[service.OpenAIOAuthReauthorizationEmailCodeTokenCredentialKey] = ciphertext
	f.admin.getAccountResult = account
	proxy := service.Proxy{ID: 9, Name: "account-egress", Protocol: "http", Host: "proxy.example.test", Port: 8080, Status: service.StatusActive}
	f.admin.proxies = []service.Proxy{proxy}
	configureOpenAIReauthorizationProxy(t, f, proxy)

	r := openAIReauthorizationRouter(f.h, 42)
	w := batchOAuthRequest(r, http.MethodPost, "/tasks", `{"account_id":449,"confirmed":true}`)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.Contains(t, w.Body.String(), `"login_method":"email_code"`)
	require.NotContains(t, w.Body.String(), token)
	require.Len(t, f.requests, 1)
	require.Equal(t, batchOAuthModeReauthorization, f.requests[0]["workflow_mode"])
	require.Equal(t, batchOAuthLoginEmailCode, f.requests[0]["login_method"])
	require.Equal(t, token, f.requests[0]["email_code_token"])
	require.Empty(t, f.requests[0]["password"])
	require.Empty(t, f.requests[0]["totp_secret"])
}

func TestOpenAIReauthorizationTaskRejectsAnotherExecutionNode(t *testing.T) {
	f := newBatchOAuthFixture(t)
	t.Setenv("GATEWAY_EXECUTION_NODE_ID", "api")
	f.admin.getAccountResult = reauthorizationAccount(448)
	r := openAIReauthorizationRouter(f.h, 42)
	w := batchOAuthRequest(r, http.MethodPost, "/tasks", `{"account_id":448,"confirmed":true}`)
	require.Equal(t, http.StatusConflict, w.Code, w.Body.String())
	require.Empty(t, f.requests)
}

func TestOpenAIReauthorizationCompletionOnlyUpdatesMergedCredentials(t *testing.T) {
	f := newBatchOAuthFixture(t)
	t.Setenv("GATEWAY_EXECUTION_NODE_ID", "api2")
	account := reauthorizationAccount(448)
	f.admin.getAccountResult = account
	proxy := service.Proxy{ID: 9, Name: "account-egress", Protocol: "http", Host: "proxy.example.test", Port: 8080, Status: service.StatusActive}
	f.admin.proxies = []service.Proxy{proxy}
	configureOpenAIReauthorizationProxy(t, f, proxy)
	invalidator := &openAIReauthorizationInvalidator{}
	f.h.ConfigureTokenCacheInvalidator(invalidator)
	r := openAIReauthorizationRouter(f.h, 42)
	start := batchOAuthRequest(r, http.MethodPost, "/tasks", `{"account_id":448,"confirmed":true}`)
	require.Equal(t, http.StatusOK, start.Code, start.Body.String())
	var envelope struct {
		Data struct {
			ID string `json:"task_id"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(start.Body.Bytes(), &envelope))

	complete := batchOAuthRequest(r, http.MethodPost, "/tasks/"+envelope.Data.ID+"/complete", `{}`)
	require.Equal(t, http.StatusOK, complete.Code, complete.Body.String())
	require.Contains(t, complete.Body.String(), `"status":"completed"`)
	require.Contains(t, complete.Body.String(), `"account_id":448`)
	input := f.admin.lastUpdateAccountInput
	require.NotNil(t, input)
	require.Empty(t, input.Name)
	require.Nil(t, input.ProxyID)
	require.Nil(t, input.Concurrency)
	require.Nil(t, input.Priority)
	require.Nil(t, input.GroupIDs)
	require.Nil(t, input.Extra)
	require.True(t, input.AllowOpenAIReauthorizationCredentials)
	require.False(t, input.ResetOpenAIWeeklyEstimate)
	require.Equal(t, "https://upstream.example.test", input.Credentials["base_url"])
	require.Equal(t, map[string]any{"gpt-public": "gpt-upstream"}, input.Credentials["model_mapping"])
	require.Equal(t, account.Credentials[service.OpenAIOAuthReauthorizationPasswordCredentialKey], input.Credentials[service.OpenAIOAuthReauthorizationPasswordCredentialKey])
	require.Equal(t, account.Credentials[service.OpenAIOAuthReauthorizationTOTPSecretCredentialKey], input.Credentials[service.OpenAIOAuthReauthorizationTOTPSecretCredentialKey])
	require.Equal(t, []int64{448, 448}, invalidator.accountIDs)
	require.Equal(t, service.OpenAIReauthorizationResultSuccess, f.admin.openAIReauthorizationState.last.LastResult)
	require.Equal(t, 1, f.admin.openAIReauthorizationState.last.SuccessCount)
}

func TestOpenAIReauthorizationStatusShowsExactCooldownAndLegacyInference(t *testing.T) {
	f := newBatchOAuthFixture(t)
	now := time.Now().UTC()
	exact := reauthorizationAccount(501)
	succeededAt := now.Add(-2 * 24 * time.Hour)
	exact.Extra[service.OpenAIReauthorizationStateExtraKey] = service.OpenAIReauthorizationState{
		Version: 1, AttemptCount: 1, SuccessCount: 1,
		FirstAttemptAt: &succeededAt, LastAttemptAt: &succeededAt,
		FirstSucceededAt: &succeededAt, LastSucceededAt: &succeededAt,
		LastResult:    service.OpenAIReauthorizationResultSuccess,
		HistorySource: "xiass_state", HistoryConfidence: service.OpenAIReauthorizationHistoryExact,
	}
	legacy := reauthorizationAccount(502)
	legacyAttempt := now.Add(-10 * 24 * time.Hour)
	legacy.LastUsedAt = new(time.Time)
	*legacy.LastUsedAt = legacyAttempt.Add(time.Hour)
	f.admin.accounts = []service.Account{*exact, *legacy}
	evidence := openAIReauthorizationAuditEvidence{Attempts: []time.Time{legacyAttempt}}
	inferred := inferOpenAIReauthorizationState(legacy, evidence)
	require.Equal(t, service.OpenAIReauthorizationHistoryInferred, inferred.HistoryConfidence)
	require.Equal(t, 1, inferred.SuccessCount)

	status := buildOpenAIReauthorizationAccountStatus(exact, service.OpenAIReauthorizationStateFromAccount(exact), true, now)
	require.Equal(t, "cooldown", status.RiskLevel)
	require.Equal(t, 2, status.CurrentAuthorizationNumber)
	require.True(t, status.CanStart)
	require.Greater(t, status.CooldownRemainingSeconds, int64(4*24*time.Hour/time.Second))
}

func TestOpenAIReauthorizationUntrackedAccountStartsAsFirstAuthorization(t *testing.T) {
	f := newBatchOAuthFixture(t)
	account := reauthorizationAccount(503)
	delete(account.Extra, service.OpenAIReauthorizationStateExtraKey)
	account.CreatedAt = time.Date(2026, time.September, 1, 0, 0, 0, 0, time.UTC)
	now := time.Date(2026, time.September, 15, 8, 0, 0, 0, time.UTC)

	state := f.h.openAIReauthorizationState(context.Background(), account)
	require.Empty(t, state.LastResult)
	require.False(t, state.HasHistory())
	status := buildOpenAIReauthorizationAccountStatus(account, state, true, now)
	require.Equal(t, "first", status.RiskLevel)
	require.True(t, status.CanStart)
	require.False(t, status.RequiresRiskConfirmation)

	_, authorizationNumber, err := f.h.validateOpenAIReauthorizationStart(
		context.Background(), account,
		openAIReauthorizationAuthorizationRequest{Confirmed: true}, now,
	)
	require.NoError(t, err)
	require.Equal(t, 1, authorizationNumber)
}

func TestOpenAIReauthorizationCompletionRejectsChangedProxy(t *testing.T) {
	f := newBatchOAuthFixture(t)
	t.Setenv("GATEWAY_EXECUTION_NODE_ID", "api2")
	account := reauthorizationAccount(448)
	f.admin.getAccountResult = account
	proxy := service.Proxy{ID: 9, Protocol: "http", Host: "proxy.example.test", Port: 8080, Status: service.StatusActive}
	f.admin.proxies = []service.Proxy{proxy}
	configureOpenAIReauthorizationProxy(t, f, proxy)
	r := openAIReauthorizationRouter(f.h, 42)
	start := batchOAuthRequest(r, http.MethodPost, "/tasks", `{"account_id":448,"confirmed":true}`)
	require.Equal(t, http.StatusOK, start.Code, start.Body.String())
	var envelope struct {
		Data struct {
			ID string `json:"task_id"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(start.Body.Bytes(), &envelope))
	changedProxyID := int64(10)
	account.ProxyID = &changedProxyID

	complete := batchOAuthRequest(r, http.MethodPost, "/tasks/"+envelope.Data.ID+"/complete", `{}`)
	require.Equal(t, http.StatusOK, complete.Code, complete.Body.String())
	require.Contains(t, complete.Body.String(), `"reason":"account_configuration_changed"`)
	require.Zero(t, f.admin.updateAccountCalls)
}

func TestOpenAIReauthorizationRetriesOnlyAccountStateRecovery(t *testing.T) {
	f := newBatchOAuthFixture(t)
	t.Setenv("GATEWAY_EXECUTION_NODE_ID", "api2")
	account := reauthorizationAccount(448)
	f.admin.getAccountResult = account
	proxy := service.Proxy{ID: 9, Protocol: "http", Host: "proxy.example.test", Port: 8080, Status: service.StatusActive}
	f.admin.proxies = []service.Proxy{proxy}
	configureOpenAIReauthorizationProxy(t, f, proxy)
	f.admin.clearAccountErrorErr = errors.New("temporary database error")
	r := openAIReauthorizationRouter(f.h, 42)
	start := batchOAuthRequest(r, http.MethodPost, "/tasks", `{"account_id":448,"confirmed":true}`)
	require.Equal(t, http.StatusOK, start.Code, start.Body.String())
	var envelope struct {
		Data struct {
			ID string `json:"task_id"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(start.Body.Bytes(), &envelope))

	first := batchOAuthRequest(r, http.MethodPost, "/tasks/"+envelope.Data.ID+"/complete", `{}`)
	require.Equal(t, http.StatusOK, first.Code, first.Body.String())
	require.Contains(t, first.Body.String(), `"status":"failed"`)
	require.Contains(t, first.Body.String(), `"reason":"account_state_recovery_failed"`)
	require.Contains(t, first.Body.String(), `"account_id":448`)
	require.Equal(t, 1, f.admin.updateAccountCalls)

	f.admin.clearAccountErrorErr = nil
	f.admin.clearAccountErrorResult = account
	second := batchOAuthRequest(r, http.MethodPost, "/tasks/"+envelope.Data.ID+"/complete", `{}`)
	require.Equal(t, http.StatusOK, second.Code, second.Body.String())
	require.Contains(t, second.Body.String(), `"status":"completed"`)
	require.Equal(t, 1, f.admin.updateAccountCalls)
}

func TestOpenAIReauthorizationRepeatedStartReturnsExistingTask(t *testing.T) {
	f := newBatchOAuthFixture(t)
	t.Setenv("GATEWAY_EXECUTION_NODE_ID", "api2")
	f.admin.getAccountResult = reauthorizationAccount(448)
	proxy := service.Proxy{ID: 9, Protocol: "http", Host: "proxy.example.test", Port: 8080, Status: service.StatusActive}
	f.admin.proxies = []service.Proxy{proxy}
	configureOpenAIReauthorizationProxy(t, f, proxy)
	r := openAIReauthorizationRouter(f.h, 42)
	first := batchOAuthRequest(r, http.MethodPost, "/tasks", `{"account_id":448,"confirmed":true}`)
	require.Equal(t, http.StatusOK, first.Code, first.Body.String())
	var firstEnvelope struct {
		Data struct {
			ID string `json:"task_id"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(first.Body.Bytes(), &firstEnvelope))
	task := f.h.batchOAuthStore.tasks[firstEnvelope.Data.ID]
	task.mu.Lock()
	task.Status, task.Stage, task.Reason = "failed", "failed", "automation_start_failed"
	task.markFinishedIfTerminal()
	task.mu.Unlock()

	second := batchOAuthRequest(r, http.MethodPost, "/tasks", `{"account_id":448,"confirmed":true}`)
	require.Equal(t, http.StatusOK, second.Code, second.Body.String())
	require.Contains(t, second.Body.String(), `"task_id":"`+firstEnvelope.Data.ID+`"`)
	require.Equal(t, int32(1), f.sidecarCalls.Load())
}

func TestOpenAIReauthorizationRestrictedTaskRestartsOnlyAfterHighRiskConfirmation(t *testing.T) {
	f := newBatchOAuthFixture(t)
	t.Setenv("GATEWAY_EXECUTION_NODE_ID", "api2")
	f.admin.getAccountResult = reauthorizationAccount(448)
	proxy := service.Proxy{ID: 9, Protocol: "http", Host: "proxy.example.test", Port: 8080, Status: service.StatusActive}
	f.admin.proxies = []service.Proxy{proxy}
	configureOpenAIReauthorizationProxy(t, f, proxy)
	r := openAIReauthorizationRouter(f.h, 42)
	start := batchOAuthRequest(r, http.MethodPost, "/tasks", `{"account_id":448,"confirmed":true}`)
	require.Equal(t, http.StatusOK, start.Code, start.Body.String())
	var envelope struct {
		Data struct {
			ID string `json:"task_id"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(start.Body.Bytes(), &envelope))
	task := f.h.batchOAuthStore.tasks[envelope.Data.ID]
	task.mu.Lock()
	task.Status, task.Stage, task.Reason = "blocked", "blocked", "account_blocked"
	task.mu.Unlock()
	restart := batchOAuthRequest(r, http.MethodPost, "/tasks/"+envelope.Data.ID+"/restart", `{"confirmed":true}`)
	require.Equal(t, http.StatusConflict, restart.Code, restart.Body.String())
	require.Contains(t, restart.Body.String(), "高风险二次确认")
	require.Len(t, f.requests, 1)

	restart = batchOAuthRequest(r, http.MethodPost, "/tasks/"+envelope.Data.ID+"/restart", `{"confirmed":true,"acknowledged_second_reauthorization_risk":true}`)
	require.Equal(t, http.StatusOK, restart.Code, restart.Body.String())
	require.Len(t, f.requests, 2)
}
