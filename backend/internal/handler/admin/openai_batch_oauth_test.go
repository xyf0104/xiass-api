package admin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
	"github.com/Wei-Shaw/sub2api/internal/repository"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type batchOAuthClientStub struct {
	teamChildOAuthClientStub
	calls   atomic.Int32
	entered chan struct{}
	release chan struct{}
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

func newBatchOAuthFixture(t *testing.T) *batchOAuthFixture {
	t.Helper()
	f := &batchOAuthFixture{admin: newStubAdminService(), client: &batchOAuthClientStub{teamChildOAuthClientStub: teamChildOAuthClientStub{email: "owner@example.test"}}, states: map[string]string{}, status: "completed", stage: "completed"}
	f.h = NewOpenAIOAuthHandler(service.NewOpenAIOAuthService(nil, f.client), f.admin, nil, nil)
	cfg := &config.Config{}
	cfg.Totp.EncryptionKey = strings.Repeat("ab", 32)
	encryptor, err := repository.NewAESEncryptor(cfg)
	require.NoError(t, err)
	f.h.ConfigureTeamChildSecrets(encryptor)
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
			_ = json.NewDecoder(r.Body).Decode(&payload)
		}
		id := strings.TrimPrefix(r.URL.Path, "/batch-oauth/tasks/")
		id = strings.Split(id, "/")[0]
		status, stage := f.status, f.stage
		if r.URL.Path == "/batch-oauth/tasks" {
			f.requests = append(f.requests, payload)
			id, _ = payload["task_id"].(string)
			u, _ := url.Parse(payload["auth_url"].(string))
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
	r.POST("/tasks/:task_id/complete", h.CompleteBatchOAuthTask)
	r.POST("/tasks/:task_id/cancel", h.CancelBatchOAuthTask)
	r.POST("/tasks/:task_id/restart", h.RestartBatchOAuthTask)
	r.GET("/tasks/:task_id/sms", h.BatchOAuthSMSAction)
	r.POST("/tasks/:task_id/sms/:action", h.BatchOAuthSMSAction)
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
		{"GET", "", ""}, {"POST", "/complete", "{}"}, {"POST", "/cancel", `{"confirmed":true}`}, {"POST", "/restart", `{"password":"secret"}`}, {"POST", "/sms/acquire", `{"confirmed":true}`}, {"GET", "/sms", ""},
	} {
		w := batchOAuthRequest(other, route.method, "/tasks/"+id+route.path, route.body)
		require.Equal(t, 404, w.Code)
	}
	require.Equal(t, before, f.sidecarCalls.Load())
	member := batchOAuthRouter(f.h, 42, "user")
	require.Equal(t, 403, batchOAuthRequest(member, "GET", "/tasks/"+id, "").Code)
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
	for _, key := range []string{"batch-operation-0002", "batch-operation-0003"} {
		w = batchOAuthRequest(r, "POST", "/tasks", `{"email":"owner@example.test","password":"secret","idempotency_key":"`+key+`"}`)
		require.Equal(t, 200, w.Code)
	}
	w = batchOAuthRequest(r, "POST", "/tasks", `{"email":"owner@example.test","password":"secret","idempotency_key":"batch-operation-0004"}`)
	require.Equal(t, 409, w.Code)
	require.Len(t, f.requests, 3)
	require.NotEqual(t, f.requests[0]["auth_url"], f.requests[1]["auth_url"])
	require.NotEqual(t, f.requests[0]["oauth_session_id"], f.requests[1]["oauth_session_id"])
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

func TestBatchOAuthPoolValidationAndAssignmentFailureDoesNotDuplicate(t *testing.T) {
	f := newBatchOAuthFixture(t)
	poolID := int64(7)
	pools := &batchPoolAdminStub{stubAdminService: f.admin, pool: &service.AccountPool{ID: 7}, assignErr: errors.New("pool removed during creation")}
	f.h.adminService = pools
	r, id := startFixtureTask(t, f)
	f.h.batchOAuthStore.tasks[id].config.PoolID = &poolID
	w := batchOAuthRequest(r, "POST", "/tasks/"+id+"/complete", "{}")
	require.Equal(t, 200, w.Code)
	require.Contains(t, w.Body.String(), "pool_assignment_failed")
	require.Contains(t, w.Body.String(), `"account_id":300`)
	require.Equal(t, []int64{300}, pools.assigned)
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
