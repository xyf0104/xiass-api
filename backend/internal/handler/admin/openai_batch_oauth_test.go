package admin

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
	"github.com/Wei-Shaw/sub2api/internal/pkg/redisclient"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type batchOAuthClientStub struct {
	teamChildOAuthClientStub
	calls   atomic.Int32
	entered chan struct{}
	release chan struct{}
}

type batchOAuthTestEncryptor struct{}

func TestBatchOAuthPublicReasonPreservesRetryableOpenAIErrors(t *testing.T) {
	for _, reason := range []string{"openai_route_error", "oauth_session_expired", "reauthorization_phone_required"} {
		require.Equal(t, reason, batchOAuthPublicReason(reason))
	}
}

func TestBatchOAuthAdsPowerRefreshPreservesReportedAutomationStage(t *testing.T) {
	task := &batchOAuthTask{
		ID:          "ads-task",
		ownerID:     42,
		BrowserMode: batchOAuthBrowserAdsPower,
		Status:      "running",
		Stage:       "totp",
		ExpiresAt:   time.Now().Add(time.Minute),
		config:      batchOAuthConfig{BrowserMode: batchOAuthBrowserAdsPower},
	}
	sidecar, err := task.refresh(context.Background())
	require.NoError(t, err)
	require.Equal(t, "totp", task.Stage)
	require.Equal(t, "totp", sidecar.Stage)

	task.Stage = "queued"
	sidecar, err = task.refresh(context.Background())
	require.NoError(t, err)
	require.Equal(t, "external_browser", task.Stage)
	require.Equal(t, "external_browser", sidecar.Stage)
}

func (batchOAuthTestEncryptor) Encrypt(value string) (string, error) {
	return "enc:" + base64.RawStdEncoding.EncodeToString([]byte(value)), nil
}

func (batchOAuthTestEncryptor) Decrypt(value string) (string, error) {
	decoded, err := base64.RawStdEncoding.DecodeString(strings.TrimPrefix(value, "enc:"))
	if err != nil {
		return "", err
	}
	return string(decoded), nil
}

func (s *batchOAuthClientStub) ExchangeCode(ctx context.Context, code, verifier, redirect, proxy, client string) (*openai.TokenResponse, error) {
	s.calls.Add(1)
	if s.entered != nil {
		close(s.entered)
		<-s.release
	}
	return s.teamChildOAuthClientStub.ExchangeCode(ctx, code, verifier, redirect, proxy, client)
}

type batchOAuthFixture struct {
	h            *OpenAIOAuthHandler
	admin        *stubAdminService
	client       *batchOAuthClientStub
	mu           sync.Mutex
	requests     []map[string]any
	states       map[string]string
	status       string
	stage        string
	sidecarCalls atomic.Int32
}

func TestBatchOAuthExistingAccountIsSkippedBeforeAutomation(t *testing.T) {
	f := newBatchOAuthFixture(t)
	f.admin.accounts = []service.Account{{
		ID: 777, Name: "Existing account", Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth,
		Credentials: map[string]any{"email": "existing@example.test"},
	}}
	r := batchOAuthRouter(f.h, 42, "admin")
	w := batchOAuthRequest(r, "POST", "/tasks", `{"email":"EXISTING@example.test","password":"login-secret","totp_secret":"JBSWY3DPEHPK3PXP","idempotency_key":"existing-account-0001"}`)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.Contains(t, w.Body.String(), `"status":"completed"`)
	require.Contains(t, w.Body.String(), `"reason":"account_already_exists"`)
	require.Contains(t, w.Body.String(), `"account_id":777`)
	require.Zero(t, f.sidecarCalls.Load())
	require.Empty(t, f.requests)
	require.Empty(t, f.admin.createdAccounts)
}

func TestBatchOAuthDefaultsAccountConcurrencyToOne(t *testing.T) {
	f := newBatchOAuthFixture(t)
	r, id := startFixtureTask(t, f)
	require.NotNil(t, r)
	require.Equal(t, 1, f.h.batchOAuthStore.tasks[id].config.Concurrency)
}

func TestBatchOAuthDistributedLockSerializesFinalizationAcrossNodes(t *testing.T) {
	redisServer := miniredis.RunT(t)
	client := redisclient.NewClient(&redisclient.Options{Addr: redisServer.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	first := NewOpenAIOAuthHandler(nil, nil, nil, nil)
	second := NewOpenAIOAuthHandler(nil, nil, nil, nil)
	first.ConfigureTeamChildSessionStore(client)
	second.ConfigureTeamChildSessionStore(client)
	key := batchOAuthLockKey("email", "owner@example.test")
	unlock, err := first.acquireBatchOAuthLock(context.Background(), key, time.Second)
	require.NoError(t, err)
	result := make(chan error, 1)
	go func() {
		secondUnlock, lockErr := second.acquireBatchOAuthLock(context.Background(), key, time.Second)
		if lockErr == nil {
			secondUnlock()
		}
		result <- lockErr
	}()
	select {
	case err := <-result:
		require.Failf(t, "second lock returned before release", "error: %v", err)
	case <-time.After(150 * time.Millisecond):
	}
	unlock()
	select {
	case err := <-result:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		require.Fail(t, "second lock did not acquire after release")
	}
}

func newBatchOAuthFixture(t *testing.T) *batchOAuthFixture {
	t.Helper()
	f := &batchOAuthFixture{admin: newStubAdminService(), client: &batchOAuthClientStub{teamChildOAuthClientStub: teamChildOAuthClientStub{email: "owner@example.test"}}, states: map[string]string{}, status: "completed", stage: "completed"}
	f.h = NewOpenAIOAuthHandler(service.NewOpenAIOAuthService(nil, f.client), f.admin, nil, nil)
	f.h.ConfigureTeamChildSecrets(batchOAuthTestEncryptor{})
	t.Cleanup(f.h.openaiOAuthService.Stop)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.sidecarCalls.Add(1)
		if r.Header.Get("X-XIASS-Team-Child-Token") != "batch-test-token" {
			w.WriteHeader(401)
			return
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		var payload map[string]any
		if r.Method == http.MethodPost {
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				http.Error(w, "invalid request", http.StatusBadRequest)
				return
			}
		}
		id := strings.TrimPrefix(r.URL.Path, "/batch-oauth/tasks/")
		id = strings.Split(id, "/")[0]
		status, stage := f.status, f.stage
		if r.URL.Path == "/batch-oauth/tasks" {
			f.requests = append(f.requests, payload)
			id, _ = payload["task_id"].(string)
			authURL, ok := payload["auth_url"].(string)
			require.True(t, ok)
			u, err := url.Parse(authURL)
			require.NoError(t, err)
			f.states[id] = u.Query().Get("state")
			status, stage = "running", "login"
		}
		if strings.HasSuffix(r.URL.Path, "/cancel") {
			status, stage = "canceled", "canceled"
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"id": id, "owner_id": 42, "status": status, "stage": stage,
			"callback_url": openai.DefaultRedirectURI + "?code=private-code&state=" + f.states[id]})
	}))
	t.Cleanup(server.Close)
	t.Setenv("TEAM_CHILD_AUTOMATION_URL", server.URL)
	t.Setenv("TEAM_CHILD_AUTOMATION_TOKEN", "batch-test-token")
	return f
}

func batchOAuthRouter(h *OpenAIOAuthHandler, owner int64, role string) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: owner})
		c.Set(string(middleware.ContextKeyUserRole), role)
		c.Next()
	})
	r.POST("/tasks", h.StartBatchOAuthTask)
	r.GET("/tasks", h.ListBatchOAuthTasks)
	r.GET("/tasks/:task_id", h.GetBatchOAuthTask)
	r.DELETE("/tasks/:task_id", h.DeleteBatchOAuthTask)
	r.POST("/tasks/:task_id/complete", h.CompleteBatchOAuthTask)
	r.POST("/tasks/:task_id/cancel", h.CancelBatchOAuthTask)
	r.POST("/tasks/:task_id/restart", h.RestartBatchOAuthTask)
	r.POST("/tasks/:task_id/adspower-launch", h.LaunchBatchOAuthTaskInAdsPower)
	r.GET("/tasks/:task_id/sms", h.BatchOAuthSMSAction)
	r.POST("/tasks/:task_id/sms/:action", h.BatchOAuthSMSAction)
	r.POST("/tools/adspower/launch-tickets/redeem", h.RedeemOpenAIAdsPowerLaunchTicket)
	r.POST("/tools/adspower/bindings/report", h.ReportOpenAIAdsPowerBinding)
	r.POST("/tools/adspower/progress/report", h.ReportOpenAIAdsPowerProgress)
	r.POST("/tools/adspower/callbacks/report", h.ReportOpenAIAdsPowerCallback)
	return r
}

func batchOAuthRequest(r http.Handler, method, path, body string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	return w
}

func startFixtureTask(t *testing.T, f *batchOAuthFixture) (*gin.Engine, string) {
	t.Helper()
	r := batchOAuthRouter(f.h, 42, "admin")
	w := batchOAuthRequest(r, "POST", "/tasks", `{"email":"owner@example.test","password":"login-secret","totp_secret":"JBSWY3DPEHPK3PXP","idempotency_key":"batch-operation-0001"}`)
	require.Equal(t, 200, w.Code, w.Body.String())
	var envelope struct {
		Data struct {
			ID string `json:"task_id"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &envelope))
	require.NotEmpty(t, envelope.Data.ID)
	return r, envelope.Data.ID
}

func TestBatchOAuthTaskOwnershipAndSecretRedaction(t *testing.T) {
	f := newBatchOAuthFixture(t)
	r, id := startFixtureTask(t, f)
	for _, path := range []string{"/tasks", "/tasks/" + id} {
		w := batchOAuthRequest(r, "GET", path, "")
		require.Equal(t, 200, w.Code)
		for _, secret := range []string{"login-secret", "JBSWY3DPEHPK3PXP", "private-code", "callback_url", "session_id", "access_token"} {
			require.NotContains(t, w.Body.String(), secret)
		}
		require.Equal(t, "no-store", w.Header().Get("Cache-Control"))
	}
	before := f.sidecarCalls.Load()
	other := batchOAuthRouter(f.h, 43, "admin")
	for _, route := range []struct{ method, path, body string }{
		{"GET", "", ""}, {"DELETE", "", ""}, {"POST", "/complete", "{}"}, {"POST", "/cancel", `{"confirmed":true}`}, {"POST", "/restart", `{"password":"secret"}`}, {"POST", "/sms/acquire", `{"confirmed":true}`}, {"GET", "/sms", ""},
	} {
		w := batchOAuthRequest(other, route.method, "/tasks/"+id+route.path, route.body)
		require.Equal(t, 404, w.Code)
	}
	require.Equal(t, before, f.sidecarCalls.Load())
	member := batchOAuthRouter(f.h, 42, "user")
	require.Equal(t, 403, batchOAuthRequest(member, "GET", "/tasks/"+id, "").Code)
}

func TestBatchOAuthAdsPowerModeUsesOneTaskBoundCallbackAndPersistsProfile(t *testing.T) {
	f := newBatchOAuthFixture(t)
	r := batchOAuthRouter(f.h, 42, "admin")
	start := batchOAuthRequest(r, http.MethodPost, "/tasks", `{"email":"owner@example.test","password":"login-secret","totp_secret":"JBSWY3DPEHPK3PXP","browser_mode":"adspower","idempotency_key":"adspower-batch-0001"}`)
	require.Equal(t, http.StatusOK, start.Code, start.Body.String())
	var started struct {
		Data batchOAuthTask `json:"data"`
	}
	require.NoError(t, json.Unmarshal(start.Body.Bytes(), &started))
	require.Equal(t, batchOAuthBrowserAdsPower, started.Data.BrowserMode)
	require.Equal(t, "external_browser", started.Data.Stage)
	require.Zero(t, f.sidecarCalls.Load())

	launch := httptest.NewRecorder()
	launchRequest := httptest.NewRequest(http.MethodPost, "/tasks/"+started.Data.ID+"/adspower-launch", strings.NewReader(`{}`))
	launchRequest.Host = "127.0.0.1"
	launchRequest.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(launch, launchRequest)
	require.Equal(t, http.StatusOK, launch.Code, launch.Body.String())
	require.Equal(t, http.StatusConflict, batchOAuthRequest(r, http.MethodPost, "/tasks/"+started.Data.ID+"/adspower-launch", `{}`).Code)
	var launchEnvelope struct {
		Data openAIAdsPowerLaunchResponse `json:"data"`
	}
	require.NoError(t, json.Unmarshal(launch.Body.Bytes(), &launchEnvelope))
	helperURL, err := url.Parse(launchEnvelope.Data.HelperURL)
	require.NoError(t, err)
	ticket := helperURL.Query().Get("ticket")
	require.NotEmpty(t, ticket)

	redeem := batchOAuthRequest(r, http.MethodPost, "/tools/adspower/launch-tickets/redeem", `{"ticket":"`+ticket+`"}`)
	require.Equal(t, http.StatusOK, redeem.Code, redeem.Body.String())
	var redeemed struct {
		Data openAIAdsPowerRedeemResponse `json:"data"`
	}
	require.NoError(t, json.Unmarshal(redeem.Body.Bytes(), &redeemed))
	require.NotEmpty(t, redeemed.Data.BindingToken)
	require.NotEmpty(t, redeemed.Data.CallbackToken)
	require.Equal(t, "api", redeemed.Data.EnvironmentKey)
	require.Equal(t, "owner@example.test", redeemed.Data.LoginEmail)
	require.Equal(t, batchOAuthLoginPassword, redeemed.Data.LoginMethod)
	require.Equal(t, "login-secret", redeemed.Data.Password)
	require.Equal(t, "JBSWY3DPEHPK3PXP", redeemed.Data.TOTPSecret)
	require.NotContains(t, launch.Body.String(), "login-secret")
	require.NotContains(t, launch.Body.String(), "JBSWY3DPEHPK3PXP")

	progressBody, err := json.Marshal(openAIAdsPowerProgressReport{
		CallbackToken: redeemed.Data.CallbackToken, Status: "running", Stage: "password",
	})
	require.NoError(t, err)
	progress := batchOAuthRequest(r, http.MethodPost, "/tools/adspower/progress/report", string(progressBody))
	require.Equal(t, http.StatusOK, progress.Code, progress.Body.String())
	task := f.h.batchOAuthStore.tasks[started.Data.ID]
	task.mu.Lock()
	require.Equal(t, "password", task.Stage)
	task.mu.Unlock()

	bindingBody := `{"binding_token":"` + redeemed.Data.BindingToken + `","device_id":"device-1","profile_id":"profile-1","profile_no":"7","profile_name":"XIASS owner","environment_key":"api","proxy_type":"socks5","proxy_host":"proxy.example.test","proxy_port":"1104","proxy_exit_ip":"203.0.113.42","webrtc_disabled":true,"fingerprint_randomized":true}`
	binding := batchOAuthRequest(r, http.MethodPost, "/tools/adspower/bindings/report", bindingBody)
	require.Equal(t, http.StatusOK, binding.Code, binding.Body.String())
	pendingBinding, err := f.h.adsPowerLaunchStore.pendingBinding(context.Background(), redeemed.Data.SessionID, 42)
	require.NoError(t, err)
	require.NotNil(t, pendingBinding)
	f.admin.getAccountResult = &service.Account{
		ID: 300, Name: "owner@example.test", Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth,
		Status: service.StatusActive, Schedulable: true, Concurrency: 1, Priority: 0,
		Credentials: map[string]any{
			"email": "owner@example.test",
			service.OpenAIOAuthReauthorizationEmailCredentialKey:      "owner@example.test",
			service.OpenAIOAuthReauthorizationPasswordCredentialKey:   "enc:bG9naW4tc2VjcmV0",
			service.OpenAIOAuthReauthorizationTOTPSecretCredentialKey: "enc:SkJTV1kzRFBFSFBLM1BYUA",
		},
		Extra: map[string]any{"codex_fingerprint_mode": "off", service.OpenAIAdsPowerBindingExtraKey: pendingBinding},
	}

	task.mu.Lock()
	callbackURL := openai.DefaultRedirectURI + "?code=private-code&state=" + task.state
	task.mu.Unlock()
	callbackBody, err := json.Marshal(openAIAdsPowerCallbackReport{CallbackToken: redeemed.Data.CallbackToken, CallbackURL: callbackURL})
	require.NoError(t, err)
	callback := batchOAuthRequest(r, http.MethodPost, "/tools/adspower/callbacks/report", string(callbackBody))
	require.Equal(t, http.StatusOK, callback.Code, callback.Body.String())
	require.Equal(t, http.StatusGone, batchOAuthRequest(r, http.MethodPost, "/tools/adspower/callbacks/report", string(callbackBody)).Code)

	completed := batchOAuthRequest(r, http.MethodPost, "/tasks/"+started.Data.ID+"/complete", `{}`)
	require.Equal(t, http.StatusOK, completed.Code, completed.Body.String())
	require.Contains(t, completed.Body.String(), `"status":"completed"`)
	require.Len(t, f.admin.createdAccounts, 1)
	stored := service.ParseOpenAIAdsPowerBinding(f.admin.createdAccounts[0].Extra[service.OpenAIAdsPowerBindingExtraKey])
	require.NotNil(t, stored)
	require.Equal(t, "profile-1", stored.ProfileID)
	require.Equal(t, "api", stored.EnvironmentKey)
	require.Zero(t, f.sidecarCalls.Load())
}

func TestBatchOAuthStartIdempotencyAndCapacity(t *testing.T) {
	f := newBatchOAuthFixture(t)
	r, id := startFixtureTask(t, f)
	w := batchOAuthRequest(r, "POST", "/tasks", `{"email":"owner@example.test","password":"different-secret","idempotency_key":"batch-operation-0001"}`)
	require.Equal(t, 200, w.Code)
	require.Contains(t, w.Body.String(), id)
	require.Len(t, f.requests, 1)
	w = batchOAuthRequest(r, "POST", "/tasks", `{"email":"other@example.test","password":"secret","idempotency_key":"batch-operation-0001"}`)
	require.Equal(t, 409, w.Code)
	for i, key := range []string{"batch-operation-0002", "batch-operation-0003"} {
		w = batchOAuthRequest(r, "POST", "/tasks", `{"email":"owner`+strconv.Itoa(i+2)+`@example.test","password":"secret","idempotency_key":"`+key+`"}`)
		require.Equal(t, 200, w.Code)
	}
	w = batchOAuthRequest(r, "POST", "/tasks", `{"email":"owner4@example.test","password":"secret","idempotency_key":"batch-operation-0004"}`)
	require.Equal(t, 409, w.Code)
	require.Len(t, f.requests, 3)
	require.NotEqual(t, f.requests[0]["auth_url"], f.requests[1]["auth_url"])
	require.NotEqual(t, f.requests[0]["oauth_session_id"], f.requests[1]["oauth_session_id"])
}

func TestBatchOAuthTerminalTaskWipesLoginAndCanBeDeleted(t *testing.T) {
	f := newBatchOAuthFixture(t)
	r, id := startFixtureTask(t, f)
	task := f.h.batchOAuthStore.tasks[id]
	require.NotEmpty(t, task.loginPasswordEncrypted)
	f.mu.Lock()
	f.status, f.stage = "failed", "failed"
	f.mu.Unlock()

	w := batchOAuthRequest(r, "GET", "/tasks/"+id, "")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.Empty(t, task.loginPasswordEncrypted)
	require.Empty(t, task.loginTOTPEncrypted)

	w = batchOAuthRequest(r, "DELETE", "/tasks/"+id, "")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.Nil(t, f.h.batchOAuthStore.tasks[id])
	require.Empty(t, f.admin.createdAccounts)
}

func TestBatchOAuthDeleteCompletedTaskKeepsCreatedAccount(t *testing.T) {
	f := newBatchOAuthFixture(t)
	r, id := startFixtureTask(t, f)
	require.Equal(t, http.StatusOK, batchOAuthRequest(r, "POST", "/tasks/"+id+"/complete", "{}").Code)
	require.Len(t, f.admin.createdAccounts, 1)

	w := batchOAuthRequest(r, "DELETE", "/tasks/"+id, "")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.Nil(t, f.h.batchOAuthStore.tasks[id])
	require.Len(t, f.admin.createdAccounts, 1)
}

func TestBatchOAuthSameEmailRejectsLiveAndReplacesOldFailure(t *testing.T) {
	f := newBatchOAuthFixture(t)
	r, firstID := startFixtureTask(t, f)
	w := batchOAuthRequest(r, "POST", "/tasks", `{"email":"owner@example.test","password":"secret","idempotency_key":"same-email-live-0002"}`)
	require.Equal(t, http.StatusConflict, w.Code, w.Body.String())

	task := f.h.batchOAuthStore.tasks[firstID]
	task.mu.Lock()
	task.Status, task.Stage = "failed", "failed"
	task.markFinishedIfTerminal()
	task.mu.Unlock()
	w = batchOAuthRequest(r, "POST", "/tasks", `{"email":"owner@example.test","password":"secret","idempotency_key":"same-email-new-0003"}`)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.Nil(t, f.h.batchOAuthStore.tasks[firstID])
	require.Len(t, f.h.batchOAuthStore.tasks, 1)
}

func TestBatchOAuthDuplicatePruneDefersBusyMailbox(t *testing.T) {
	store := newBatchOAuthStore()
	now := time.Now().UTC()
	busy := &batchOAuthTask{ID: "busy", ownerID: 42, Email: "owner@example.test", Status: "running", CreatedAt: now}
	failed := &batchOAuthTask{ID: "failed", ownerID: 42, Email: "OWNER@example.test", Status: "failed", CreatedAt: now.Add(-time.Minute)}
	store.tasks[busy.ID] = busy
	store.tasks[failed.ID] = failed

	busy.mu.Lock()
	store.mu.Lock()
	store.pruneTerminalDuplicatesLocked(42)
	store.mu.Unlock()
	require.Contains(t, store.tasks, busy.ID)
	require.Contains(t, store.tasks, failed.ID)
	busy.mu.Unlock()

	store.mu.Lock()
	store.pruneTerminalDuplicatesLocked(42)
	store.mu.Unlock()
	require.Contains(t, store.tasks, busy.ID)
	require.NotContains(t, store.tasks, failed.ID)
}

func TestBatchOAuthListReturnsCachedTaskWhileMutationOwnsLock(t *testing.T) {
	f := newBatchOAuthFixture(t)
	r, id := startFixtureTask(t, f)
	task := f.h.batchOAuthStore.tasks[id]
	task.mu.Lock()
	defer task.mu.Unlock()

	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- batchOAuthRequest(r, http.MethodGet, "/tasks", "") }()

	select {
	case response := <-done:
		require.Equal(t, http.StatusOK, response.Code, response.Body.String())
		require.Contains(t, response.Body.String(), id)
		require.Contains(t, response.Body.String(), "owner@example.test")
	case <-time.After(500 * time.Millisecond):
		t.Fatal("task list blocked behind an unrelated task mutation")
	}
}

func TestBatchOAuthIdentityAndValidationBeforeCodeConsumption(t *testing.T) {
	t.Run("wrong identity", func(t *testing.T) {
		f := newBatchOAuthFixture(t)
		f.client.email = "wrong@example.test"
		r, id := startFixtureTask(t, f)
		w := batchOAuthRequest(r, "POST", "/tasks/"+id+"/complete", "{}")
		require.Equal(t, 200, w.Code)
		require.Contains(t, w.Body.String(), "oauth_identity_mismatch")
		require.Empty(t, f.admin.createdAccounts)
	})
	t.Run("invalid config preserves code", func(t *testing.T) {
		f := newBatchOAuthFixture(t)
		r, id := startFixtureTask(t, f)
		f.h.batchOAuthStore.tasks[id].config.FingerprintMode = "invalid"
		require.Equal(t, 400, batchOAuthRequest(r, "POST", "/tasks/"+id+"/complete", "{}").Code)
		require.Zero(t, f.client.calls.Load())
		f.h.batchOAuthStore.tasks[id].config.FingerprintMode = "off"
		w := batchOAuthRequest(r, "POST", "/tasks/"+id+"/complete", "{}")
		require.Equal(t, 200, w.Code)
		require.Contains(t, w.Body.String(), `"account_id":300`)
		require.Len(t, f.admin.createdAccounts, 1)
		require.True(t, f.admin.createdAccounts[0].PreserveOAuthWorkflowProxy)
		require.Equal(t, "off", f.admin.createdAccounts[0].Extra["codex_fingerprint_mode"])
		require.NotContains(t, f.admin.createdAccounts[0].Extra, "codex_fingerprint_seed")
		require.NotContains(t, f.admin.createdAccounts[0].Credentials, "password")
	})
}

func TestBatchOAuthCompleteCancelRaceAndDuplicateCompletion(t *testing.T) {
	f := newBatchOAuthFixture(t)
	r, id := startFixtureTask(t, f)
	f.client.entered = make(chan struct{})
	f.client.release = make(chan struct{})
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- batchOAuthRequest(r, "POST", "/tasks/"+id+"/complete", "{}") }()
	<-f.client.entered
	cancelDone := make(chan *httptest.ResponseRecorder, 1)
	go func() { cancelDone <- batchOAuthRequest(r, "POST", "/tasks/"+id+"/cancel", `{"confirmed":true}`) }()
	close(f.client.release)
	require.Equal(t, 200, (<-done).Code)
	require.Equal(t, 409, (<-cancelDone).Code)
	require.Equal(t, 200, batchOAuthRequest(r, "POST", "/tasks/"+id+"/complete", "{}").Code)
	require.Equal(t, int32(1), f.client.calls.Load())
	require.Len(t, f.admin.createdAccounts, 1)
}

func TestBatchOAuthCancelWinsBeforeCreation(t *testing.T) {
	f := newBatchOAuthFixture(t)
	r, id := startFixtureTask(t, f)
	require.Equal(t, 200, batchOAuthRequest(r, "POST", "/tasks/"+id+"/cancel", `{"confirmed":true}`).Code)
	require.Equal(t, 409, batchOAuthRequest(r, "POST", "/tasks/"+id+"/complete", "{}").Code)
	require.Zero(t, f.client.calls.Load())
	require.Empty(t, f.admin.createdAccounts)
}

func TestBatchOAuthAmbiguousCreateCannotReplay(t *testing.T) {
	f := newBatchOAuthFixture(t)
	f.admin.createAccountErr = errors.New("commit reply lost")
	r, id := startFixtureTask(t, f)
	w := batchOAuthRequest(r, "POST", "/tasks/"+id+"/complete", "{}")
	require.Contains(t, w.Body.String(), "account_creation_requires_review")
	require.Equal(t, 409, batchOAuthRequest(r, "POST", "/tasks/"+id+"/restart", `{"password":"secret","confirmed":true}`).Code)
	require.Equal(t, 409, batchOAuthRequest(r, "POST", "/tasks/"+id+"/complete", "{}").Code)
	require.Len(t, f.admin.createdAccounts, 1)
	require.Equal(t, 200, batchOAuthRequest(r, "POST", "/tasks/"+id+"/cancel", `{"confirmed":true}`).Code)
	require.Equal(t, 409, batchOAuthRequest(r, "POST", "/tasks/"+id+"/restart", `{"password":"secret","confirmed":true}`).Code)
}

func TestBatchOAuthConcurrentEmailCreateResolvesToExistingAccount(t *testing.T) {
	f := newBatchOAuthFixture(t)
	r, id := startFixtureTask(t, f)
	f.admin.createAccountErr = service.ErrOpenAIOAuthEmailExists
	f.admin.createAccountErrExisting = &service.Account{
		ID: 778, Name: "concurrent winner", Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth,
		Credentials: map[string]any{"email": "owner@example.test"},
	}
	w := batchOAuthRequest(r, "POST", "/tasks/"+id+"/complete", "{}")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.Contains(t, w.Body.String(), `"reason":"account_already_exists"`)
	require.Contains(t, w.Body.String(), `"account_id":778`)
	require.Len(t, f.admin.createdAccounts, 1)
}

func TestBatchOAuthCallbackStateAndRedirect(t *testing.T) {
	valid := openai.DefaultRedirectURI + "?code=code&state=state"
	code, err := validateBatchCallback(valid, "state")
	require.NoError(t, err)
	require.Equal(t, "code", code)
	for _, raw := range []string{valid + "&state=state", valid + "&code=second", valid + "#fragment", valid + "&error=denied", strings.Replace(valid, "localhost", "evil.test", 1), strings.Replace(valid, "state=state", "state=other", 1)} {
		_, err := validateBatchCallback(raw, "state")
		require.Error(t, err)
	}
}

type batchSMSStub struct {
	calls   int
	session string
	owners  []int64
	scopes  []string
	result  *service.PixlabSMSResult
}

func (s *batchSMSStub) WorkflowSession(_ context.Context, owner int64, scope string) (string, error) {
	s.owners = append(s.owners, owner)
	s.scopes = append(s.scopes, scope)
	return s.session, nil
}
func (s *batchSMSStub) WorkflowAction(_ context.Context, owner int64, scope, id, action string, confirmed bool) (*service.PixlabSMSResult, error) {
	s.calls++
	s.owners = append(s.owners, owner)
	s.scopes = append(s.scopes, scope)
	return s.result, nil
}

func TestBatchOAuthSMSConfirmationAndScope(t *testing.T) {
	f := newBatchOAuthFixture(t)
	f.status, f.stage = "running", "phone_required"
	r, id := startFixtureTask(t, f)
	sms := &batchSMSStub{result: &service.PixlabSMSResult{Number: "+12025550123", Status: "WAITING"}}
	f.h.batchSMSService = sms
	for _, action := range []string{"acquire", "change", "cancel"} {
		require.Equal(t, 400, batchOAuthRequest(r, "POST", "/tasks/"+id+"/sms/"+action, `{"confirmed":false}`).Code)
	}
	require.Zero(t, sms.calls)
	w := batchOAuthRequest(r, "POST", "/tasks/"+id+"/sms/acquire", `{"confirmed":true,"owner_id":999,"workflow_scope":"another-task","session_id":"foreign"}`)
	require.Equal(t, 200, w.Code, w.Body.String())
	require.Equal(t, 1, sms.calls)
	for _, owner := range sms.owners {
		require.Equal(t, int64(42), owner)
	}
	for _, scope := range sms.scopes {
		require.Equal(t, id, scope)
	}
	f.mu.Lock()
	f.status, f.stage = "failed", "failed"
	f.mu.Unlock()
	require.Equal(t, 409, batchOAuthRequest(r, "POST", "/tasks/"+id+"/sms/acquire", `{"confirmed":true}`).Code)
	require.Equal(t, 1, sms.calls)
}

func TestBatchOAuthRestartIsFreshBoundedAndRequiresCancellation(t *testing.T) {
	f := newBatchOAuthFixture(t)
	r, id := startFixtureTask(t, f)
	sms := &batchSMSStub{session: "reserved", result: &service.PixlabSMSResult{Status: "CANCELLED"}}
	f.h.batchSMSService = sms
	require.Equal(t, 409, batchOAuthRequest(r, "POST", "/tasks/"+id+"/restart", `{"password":"secret"}`).Code)
	require.Zero(t, sms.calls)
	require.Len(t, f.requests, 1)
	for i := 0; i < 2; i++ {
		w := batchOAuthRequest(r, "POST", "/tasks/"+id+"/restart", `{"password":"fresh-secret","confirmed":true}`)
		require.Equal(t, 200, w.Code, w.Body.String())
	}
	require.Len(t, f.requests, 3)
	require.NotEqual(t, f.requests[0]["task_id"], f.requests[1]["task_id"])
	require.NotEqual(t, f.requests[0]["oauth_session_id"], f.requests[1]["oauth_session_id"])
	require.Equal(t, 409, batchOAuthRequest(r, "POST", "/tasks/"+id+"/restart", `{"password":"secret","confirmed":true}`).Code)
	require.Equal(t, 2, sms.calls)
}

func TestBatchOAuthPublicSerializationOmitsInternalState(t *testing.T) {
	task := &batchOAuthTask{ID: "task", Email: "owner@example.test", ownerID: 42, sidecarID: "private-sidecar", sessionID: "private-session", state: "private-state", CreatedAt: time.Now()}
	raw, err := json.Marshal(task)
	require.NoError(t, err)
	for _, private := range []string{"private-sidecar", "private-session", "private-state", "ownerID", "config"} {
		require.NotContains(t, string(raw), private)
	}
}

type batchPoolAdminStub struct {
	*stubAdminService
	service.AccountPoolService
	pool      *service.AccountPool
	assignErr error
	assigned  []int64
}

func (s *batchPoolAdminStub) GetAccountPool(context.Context, int64) (*service.AccountPool, error) {
	return s.pool, nil
}
func (s *batchPoolAdminStub) AssignAccountPool(_ context.Context, _ int64, ids []int64, _ bool) (*service.AccountPool, error) {
	s.assigned = ids
	return s.pool, s.assignErr
}

func TestBatchOAuthPoolAccountNameContinuesPlanSequence(t *testing.T) {
	f := newBatchOAuthFixture(t)
	f.admin.accounts = []service.Account{
		{ID: 1, Name: "沐念云plus1", Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth, Credentials: map[string]any{"plan_type": "plus"}},
		{ID: 2, Name: "legacy-plus", Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth, Credentials: map[string]any{"plan_type": "plus"}},
		{ID: 3, Name: "沐念云pro7", Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth, Credentials: map[string]any{"plan_type": "pro"}},
	}
	f.h.adminService = &batchPoolAdminStub{stubAdminService: f.admin, pool: &service.AccountPool{ID: 7, Name: "沐念云", AccountIDs: []int64{1, 2, 3}}}

	plusName, err := f.h.nextBatchOAuthPoolAccountName(context.Background(), 7, map[string]any{"plan_type": "plus"})
	require.NoError(t, err)
	require.Equal(t, "沐念云plus3", plusName)

	proName, err := f.h.nextBatchOAuthPoolAccountName(context.Background(), 7, map[string]any{"plan_type": "pro"})
	require.NoError(t, err)
	require.Equal(t, "沐念云pro8", proName)
}

func TestBatchOAuthPoolValidationAndAssignmentFailureDoesNotDuplicate(t *testing.T) {
	f := newBatchOAuthFixture(t)
	poolID := int64(7)
	pools := &batchPoolAdminStub{stubAdminService: f.admin, pool: &service.AccountPool{ID: 7, Name: "沐念云"}, assignErr: errors.New("pool removed during creation")}
	f.h.adminService = pools
	r, id := startFixtureTask(t, f)
	f.h.batchOAuthStore.tasks[id].config.PoolID = &poolID
	w := batchOAuthRequest(r, "POST", "/tasks/"+id+"/complete", "{}")
	require.Equal(t, 200, w.Code)
	require.Contains(t, w.Body.String(), "pool_assignment_failed")
	require.Contains(t, w.Body.String(), `"account_id":300`)
	require.Equal(t, []int64{300}, pools.assigned)
	require.Equal(t, "沐念云team1", f.admin.createdAccounts[0].Name)
	require.Equal(t, 200, batchOAuthRequest(r, "POST", "/tasks/"+id+"/complete", "{}").Code)
	require.Len(t, f.admin.createdAccounts, 1)
}

func TestBatchOAuthPoolProxyChangeRequiresFreshOAuthBeforeCode(t *testing.T) {
	f := newBatchOAuthFixture(t)
	poolID, proxyID := int64(7), int64(8)
	f.admin.proxies = []service.Proxy{{ID: 8, Protocol: "http", Host: "127.0.0.1", Port: 8080, Status: service.StatusActive}}
	f.h.adminService = &batchPoolAdminStub{stubAdminService: f.admin, pool: &service.AccountPool{ID: 7, ProxyID: &proxyID}}
	r, id := startFixtureTask(t, f)
	f.h.batchOAuthStore.tasks[id].config.PoolID = &poolID
	w := batchOAuthRequest(r, "POST", "/tasks/"+id+"/complete", "{}")
	require.Equal(t, 409, w.Code)
	require.Zero(t, f.client.calls.Load())
}

func TestBatchOAuthConfigurationRejectsInvalidReferencesBeforeAutomation(t *testing.T) {
	f := newBatchOAuthFixture(t)
	r := batchOAuthRouter(f.h, 42, "admin")
	for i, config := range []string{`"group_ids":[-1]`, `"proxy_id":-1`, `"pool_id":-1`, `"codex_fingerprint_mode":"enabled"`, `"concurrency":-1`, `"priority":-1`, `"expected_email":"wrong@example.test"`} {
		body := `{"email":"owner@example.test","password":"secret","idempotency_key":"invalid-config-000` + string(rune('0'+i)) + `",` + config + `}`
		w := batchOAuthRequest(r, "POST", "/tasks", body)
		require.Equal(t, 400, w.Code, w.Body.String())
	}
	require.Zero(t, f.sidecarCalls.Load())
}

func TestBatchOAuthBrowserProxyKeepsAccountProxyButUsesEquivalentLocalNodeEgress(t *testing.T) {
	t.Run("local built-in execution node", func(t *testing.T) {
		f := newBatchOAuthFixture(t)
		t.Setenv("GATEWAY_EXECUTION_NODE_ID", "api2")
		f.admin.proxies = []service.Proxy{{ID: 85, Name: service.ExecutionNodeBuiltinProxyNamePrefix + "api2", Protocol: "socks5", Host: "127.0.0.1", Port: 19080,
			Username: "api2", Password: strings.Repeat("a", 64), Status: service.StatusActive}}
		proxyID := int64(85)
		cfg := batchOAuthConfig{ProxyID: &proxyID, Concurrency: 3, Priority: 1, FingerprintMode: "off"}
		proxy, err := f.h.validateBatchConfig(context.Background(), &cfg)
		require.NoError(t, err)
		require.Nil(t, proxy)
		require.NotNil(t, cfg.ProxyID)
		require.Equal(t, int64(85), *cfg.ProxyID)
	})

	t.Run("ordinary proxy", func(t *testing.T) {
		f := newBatchOAuthFixture(t)
		t.Setenv("GATEWAY_EXECUTION_NODE_ID", "api2")
		f.admin.proxies = []service.Proxy{{ID: 9, Name: "external", Protocol: "http", Host: "proxy.example.test", Port: 8080,
			Username: "user", Password: "password", Status: service.StatusActive}}
		proxyID := int64(9)
		cfg := batchOAuthConfig{ProxyID: &proxyID, Concurrency: 3, Priority: 1, FingerprintMode: "off"}
		proxy, err := f.h.validateBatchConfig(context.Background(), &cfg)
		require.NoError(t, err)
		require.Equal(t, "http://proxy.example.test:8080", proxy["server"])
		require.Equal(t, "user", proxy["username"])
		require.Equal(t, "password", proxy["password"])
	})

	t.Run("remote built-in execution node", func(t *testing.T) {
		f := newBatchOAuthFixture(t)
		t.Setenv("GATEWAY_EXECUTION_NODE_ID", "api")
		f.admin.proxies = []service.Proxy{{ID: 86, Name: service.ExecutionNodeBuiltinProxyNamePrefix + "api2", Protocol: "socks5", Host: "127.0.0.1", Port: 19080,
			Username: "api2", Password: strings.Repeat("b", 64), Status: service.StatusActive}}
		proxyID := int64(86)
		cfg := batchOAuthConfig{ProxyID: &proxyID, Concurrency: 1, Priority: 1, FingerprintMode: "off"}
		_, err := f.h.validateBatchConfig(context.Background(), &cfg)
		require.ErrorContains(t, err, "another execution node")
	})
}

func TestBatchOAuthExpiredWorkflowClosesContextWithoutCancellingSMS(t *testing.T) {
	f := newBatchOAuthFixture(t)
	r, id := startFixtureTask(t, f)
	sms := &batchSMSStub{session: "reserved"}
	f.h.batchSMSService = sms
	f.h.batchOAuthStore.tasks[id].ExpiresAt = time.Now().Add(-time.Second)
	w := batchOAuthRequest(r, "GET", "/tasks/"+id, "")
	require.Equal(t, 200, w.Code)
	require.Contains(t, w.Body.String(), "task_expired")
	require.Zero(t, sms.calls)
	require.Equal(t, 409, batchOAuthRequest(r, "POST", "/tasks/"+id+"/complete", "{}").Code)
}
