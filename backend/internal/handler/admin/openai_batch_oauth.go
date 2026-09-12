package admin

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

const batchOAuthMaxRestarts = 2

var errBatchOAuthMissing = errors.New("batch task unavailable")

type batchOAuthSMS interface {
	WorkflowAction(context.Context, int64, string, string, string, bool) (*service.PixlabSMSResult, error)
	WorkflowSession(context.Context, int64, string) (string, error)
}

type batchOAuthConfig struct {
	Name            string  `json:"name"`
	GroupIDs        []int64 `json:"group_ids"`
	ProxyID         *int64  `json:"proxy_id"`
	PoolID          *int64  `json:"pool_id"`
	Concurrency     int     `json:"concurrency"`
	Priority        int     `json:"priority"`
	FingerprintMode string  `json:"codex_fingerprint_mode"`
}

type batchOAuthStartRequest struct {
	batchOAuthConfig
	Email          string `json:"email"`
	ExpectedEmail  string `json:"expected_email"`
	Password       string `json:"password"`
	TOTPSecret     string `json:"totp_secret"`
	IdempotencyKey string `json:"idempotency_key"`
}

// Login material remains encrypted and excluded from JSON until account import.
// Tokens, callback URLs and page snapshots never enter this process-local store.
type batchOAuthTask struct {
	mu                      sync.Mutex
	ID                      string            `json:"task_id"`
	Email                   string            `json:"email"`
	Status                  string            `json:"status"`
	Stage                   string            `json:"stage"`
	Reason                  string            `json:"reason,omitempty"`
	AccountID               int64             `json:"account_id,omitempty"`
	AccountConfig           *batchOAuthConfig `json:"account_config,omitempty"`
	RestartCount            int               `json:"restart_count"`
	RequiresSMSConfirmation bool              `json:"requires_sms_confirmation"`
	CreatedAt               time.Time         `json:"created_at"`
	ExpiresAt               time.Time         `json:"expires_at"`
	ownerID                 int64
	config                  batchOAuthConfig
	requestHash             [32]byte
	idempotencyKey          string
	sidecarID               string
	sessionID               string
	state                   string
	createAttempted         bool
	submittedPhone          string
	rejectedPhones          map[string]bool
	loginPasswordEncrypted  string `json:"-"`
	loginTOTPEncrypted      string `json:"-"`
}

type batchOAuthStore struct {
	mu    sync.Mutex
	tasks map[string]*batchOAuthTask
}

func newBatchOAuthStore() *batchOAuthStore {
	return &batchOAuthStore{tasks: make(map[string]*batchOAuthTask)}
}

// Called with store.mu held. Never wait for a task lock while holding the store
// lock: restart already owns its task, and mutations may take up to a minute.
func (s *batchOAuthStore) hasCapacityLocked(except *batchOAuthTask) bool {
	active := 0
	for id, task := range s.tasks {
		if task == except {
			continue
		}
		if !task.mu.TryLock() {
			active++
			continue
		}
		if !task.terminal() {
			active++
		} else if time.Since(task.CreatedAt) > 24*time.Hour {
			delete(s.tasks, id)
		}
		task.mu.Unlock()
	}
	return active < 3
}

func (h *OpenAIOAuthHandler) ConfigureBatchOAuthSMS(svc *service.PixlabSMSService) {
	h.batchSMSService = svc
}

type batchOAuthSidecarTask struct {
	ID          string `json:"id"`
	OwnerID     int64  `json:"owner_id"`
	Status      string `json:"status"`
	Stage       string `json:"stage"`
	Reason      string `json:"reason"`
	CallbackURL string `json:"callback_url"`
}

// Never propagate sidecar error bodies: they may contain external page text.
func batchOAuthSidecarRequest(ctx context.Context, method, path string, payload any) (*batchOAuthSidecarTask, error) {
	config, err := loadTeamChildMemberAutomationConfig()
	token := strings.TrimSpace(os.Getenv("TEAM_CHILD_AUTOMATION_TOKEN"))
	if err != nil || token == "" {
		return nil, errors.New("batch automation unavailable")
	}
	var body io.Reader
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			return nil, errors.New("invalid batch request")
		}
		body = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, config.baseURL+"/batch-oauth/tasks"+path, body)
	if err != nil {
		return nil, errors.New("invalid batch request")
	}
	req.Header.Set("X-XIASS-Team-Child-Token", token)
	req.Header.Set("Content-Type", "application/json")
	config.client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := config.client.Do(req)
	if err != nil {
		return nil, errors.New("batch automation unavailable")
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusNotFound {
		return nil, errBatchOAuthMissing
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, errors.New("batch automation rejected action")
	}
	var result batchOAuthSidecarTask
	if json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&result) != nil {
		return nil, errors.New("invalid automation response")
	}
	return &result, nil
}

func (t *batchOAuthTask) sidecar(ctx context.Context, action string, payload map[string]any) (*batchOAuthSidecarTask, error) {
	path := "/" + url.PathEscape(t.sidecarID)
	method := http.MethodGet
	if action == "" {
		path += "?owner_id=" + strconv.FormatInt(t.ownerID, 10)
	} else {
		method = http.MethodPost
		path += "/" + action
		if payload == nil {
			payload = make(map[string]any)
		}
		payload["owner_id"] = t.ownerID
	}
	result, err := batchOAuthSidecarRequest(ctx, method, path, payload)
	if err != nil {
		if action == "cancel" && errors.Is(err, errBatchOAuthMissing) {
			return &batchOAuthSidecarTask{ID: t.sidecarID, OwnerID: t.ownerID, Status: "canceled", Stage: "canceled"}, nil
		}
		return nil, err
	}
	if result.ID != t.sidecarID || result.OwnerID != t.ownerID {
		return nil, errors.New("automation ownership mismatch")
	}
	return result, nil
}

func validateBatchLogin(email, password, secret string) error {
	if !validTeamChildWorkflowEmail(email) || email != strings.TrimSpace(email) || len(password) == 0 || len(password) > 2048 {
		return errors.New("valid account email and password are required")
	}
	return validateOpenAIReauthorizationTOTP(secret)
}

func (h *OpenAIOAuthHandler) encryptBatchLogin(password, secret string) (string, string, error) {
	if h.secretEncryptor == nil {
		return "", "", errors.New("login encryption unavailable")
	}
	passwordEncrypted, err := h.secretEncryptor.Encrypt(password)
	if err != nil || passwordEncrypted == "" {
		return "", "", errors.New("login encryption failed")
	}
	var totpEncrypted string
	if secret != "" {
		totpEncrypted, err = h.secretEncryptor.Encrypt(secret)
		if err != nil || totpEncrypted == "" {
			return "", "", errors.New("login encryption failed")
		}
	}
	return passwordEncrypted, totpEncrypted, nil
}

func (h *OpenAIOAuthHandler) validateBatchConfig(ctx context.Context, cfg *batchOAuthConfig) (map[string]string, error) {
	if cfg.Concurrency < 1 || cfg.Concurrency > 1000 || cfg.Priority < 0 || cfg.Priority > 1000 || len(cfg.Name) > 100 || len(cfg.GroupIDs) > 100 {
		return nil, errors.New("invalid account configuration")
	}
	if cfg.FingerprintMode == "" {
		cfg.FingerprintMode = "off"
	}
	switch cfg.FingerprintMode {
	case "off", "device", "session", "full":
	default:
		return nil, errors.New("invalid fingerprint mode")
	}
	for _, id := range cfg.GroupIDs {
		if id <= 0 {
			return nil, errors.New("invalid group")
		}
		group, err := h.adminService.GetGroup(ctx, id)
		if err != nil || group == nil || !group.IsActive() || group.Platform != service.PlatformOpenAI {
			return nil, errors.New("invalid OpenAI group")
		}
	}
	if len(cfg.GroupIDs) > 0 {
		if err := h.adminService.CheckMixedChannelRisk(ctx, 0, service.PlatformOpenAI, cfg.GroupIDs); err != nil {
			return nil, errors.New("group configuration requires review")
		}
	}
	if cfg.PoolID != nil {
		pools, ok := h.adminService.(service.AccountPoolService)
		if !ok || *cfg.PoolID <= 0 {
			return nil, errors.New("invalid account pool")
		}
		pool, err := pools.GetAccountPool(ctx, *cfg.PoolID)
		if err != nil || pool == nil {
			return nil, errors.New("invalid account pool")
		}
		cfg.ProxyID = nil
		if pool.ProxyID != nil {
			id := *pool.ProxyID
			cfg.ProxyID = &id
		}
	}
	if cfg.ProxyID == nil {
		return nil, nil
	}
	if *cfg.ProxyID <= 0 {
		return nil, errors.New("invalid proxy")
	}
	p, err := h.adminService.GetProxy(ctx, *cfg.ProxyID)
	if err != nil || p == nil || !p.IsActive() || p.IsExpired(time.Now()) {
		return nil, errors.New("proxy unavailable")
	}
	if p.Protocol != "http" && p.Protocol != "https" && p.Protocol != "socks5" {
		return nil, errors.New("browser proxy protocol unsupported")
	}
	u, err := url.Parse(p.URL())
	if err != nil || u.Hostname() == "" || p.Port <= 0 || p.Port > 65535 {
		return nil, errors.New("invalid proxy")
	}
	u.User = nil
	return map[string]string{"server": u.String(), "username": p.Username, "password": p.Password}, nil
}

func batchOAuthAuth(c *gin.Context) (int64, bool) {
	c.Header("Cache-Control", "no-store")
	if !requireTeamChildAdminSession(c) {
		return 0, false
	}
	subject, _ := middleware.GetAuthSubjectFromContext(c)
	return subject.UserID, true
}

func (h *OpenAIOAuthHandler) ownedBatchTask(c *gin.Context) (*batchOAuthTask, bool) {
	owner, ok := batchOAuthAuth(c)
	if !ok {
		return nil, false
	}
	if h.batchOAuthStore == nil {
		response.Error(c, 503, "Batch OAuth unavailable")
		return nil, false
	}
	h.batchOAuthStore.mu.Lock()
	task := h.batchOAuthStore.tasks[c.Param("task_id")]
	h.batchOAuthStore.mu.Unlock()
	if task == nil || task.ownerID != owner {
		response.NotFound(c, "Task not found")
		return nil, false
	}
	task.mu.Lock()
	return task, true
}

func (t *batchOAuthTask) terminal() bool {
	return t.Status == "completed" || t.Status == "canceled" || t.Status == "failed" || t.Status == "blocked"
}

func batchOAuthPublicReason(reason string) string {
	switch reason {
	case "sms_timeout", "sms_confirmation_timeout", "email_code_required", "captcha_required", "captcha", "account_blocked", "manual_challenge", "task_expired", "invalid_credentials", "authenticator_required", "phone_rejected":
		return reason
	case "":
		return ""
	default:
		return "manual_review_required"
	}
}

func (t *batchOAuthTask) refresh(ctx context.Context) (*batchOAuthSidecarTask, error) {
	if t.terminal() {
		return nil, nil
	}
	if !t.ExpiresAt.IsZero() && time.Now().After(t.ExpiresAt) {
		t.Status, t.Stage, t.Reason = "failed", "failed", "task_expired"
		t.RequiresSMSConfirmation = false
		_, _ = t.sidecar(ctx, "cancel", nil)
		return nil, nil
	}
	r, err := t.sidecar(ctx, "", nil)
	if err != nil {
		if errors.Is(err, errBatchOAuthMissing) {
			t.Status, t.Stage, t.Reason = "failed", "failed", "task_expired"
			return nil, nil
		}
		return nil, err
	}
	switch r.Status {
	case "queued", "running", "failed", "blocked", "canceled":
		t.Status = r.Status
	case "completed":
		t.Status = "ready"
	default:
		return nil, errors.New("invalid task status")
	}
	switch r.Stage {
	case "queued", "login", "phone_required", "sms_waiting", "completed", "failed", "blocked", "canceled":
		t.Stage = r.Stage
	default:
		t.Stage = "login"
	}
	t.Reason = batchOAuthPublicReason(r.Reason)
	if t.Reason == "phone_rejected" && t.submittedPhone != "" {
		if t.rejectedPhones == nil {
			t.rejectedPhones = make(map[string]bool)
		}
		t.rejectedPhones[t.submittedPhone] = true
	}
	t.RequiresSMSConfirmation = t.Stage == "phone_required" || t.Reason == "sms_timeout" || t.Reason == "sms_confirmation_timeout"
	return r, nil
}

func (h *OpenAIOAuthHandler) startBatchAttempt(ctx context.Context, t *batchOAuthTask, password, secret string, proxy map[string]string) error {
	t.Status, t.Stage, t.Reason = "failed", "failed", "oauth_session_failed"
	auth, err := h.openaiOAuthService.GenerateAuthURL(ctx, t.config.ProxyID, openai.DefaultRedirectURI, service.PlatformOpenAI)
	if err != nil {
		return errors.New("OAuth session creation failed")
	}
	u, err := url.Parse(auth.AuthURL)
	if err != nil {
		h.openaiOAuthService.RevokeWorkflowSession(auth.SessionID)
		return errors.New("OAuth URL invalid")
	}
	t.sessionID, t.state = auth.SessionID, u.Query().Get("state")
	t.ExpiresAt = time.Now().Add(openai.SessionTTL).UTC()
	payload := map[string]any{"task_id": t.sidecarID, "owner_id": t.ownerID, "email": t.Email, "expected_email": t.Email,
		"password": password, "totp_secret": secret, "auth_url": auth.AuthURL, "oauth_session_id": auth.SessionID}
	if proxy != nil {
		payload["proxy"] = proxy
	}
	r, err := batchOAuthSidecarRequest(ctx, http.MethodPost, "", payload)
	if err != nil || r.ID != t.sidecarID || r.OwnerID != t.ownerID {
		// Preserve the task ID after ambiguous delivery; cancel/restart can close
		// an accepted private context without replaying account creation.
		t.Status, t.Stage, t.Reason = "failed", "failed", "automation_start_failed"
		h.openaiOAuthService.RevokeWorkflowSession(t.sessionID)
		return errors.New("automation start failed; cancel before restarting")
	}
	t.Status, t.Stage, t.Reason = "running", "login", ""
	return nil
}

func (h *OpenAIOAuthHandler) StartBatchOAuthTask(c *gin.Context) {
	owner, ok := batchOAuthAuth(c)
	if !ok {
		return
	}
	if h.batchOAuthStore == nil || h.openaiOAuthService == nil || h.secretEncryptor == nil {
		response.Error(c, 503, "Batch OAuth unavailable")
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 32<<10)
	var req batchOAuthStartRequest
	if c.ShouldBindJSON(&req) != nil {
		response.BadRequest(c, "Invalid batch task")
		return
	}
	req.Email = strings.ToLower(strings.TrimSpace(req.Email))
	if req.ExpectedEmail != "" && !strings.EqualFold(strings.TrimSpace(req.ExpectedEmail), req.Email) {
		response.BadRequest(c, "expected_email must match the account login email")
		return
	}
	if err := validateBatchLogin(req.Email, req.Password, req.TOTPSecret); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	if len(req.IdempotencyKey) < 16 || len(req.IdempotencyKey) > 128 {
		response.BadRequest(c, "idempotency_key must contain 16-128 characters")
		return
	}
	if req.Concurrency == 0 {
		req.Concurrency = 3
	}
	passwordEncrypted, totpEncrypted, err := h.encryptBatchLogin(req.Password, req.TOTPSecret)
	if err != nil {
		response.InternalError(c, "Login encryption failed")
		return
	}
	// Only non-secret configuration participates in the retry identity.
	raw, _ := json.Marshal(struct {
		Email  string
		Config batchOAuthConfig
	}{req.Email, req.batchOAuthConfig})
	hash := sha256.Sum256(raw)
	store := h.batchOAuthStore
	store.mu.Lock()
	for _, task := range store.tasks {
		if task.ownerID == owner && task.idempotencyKey == req.IdempotencyKey {
			store.mu.Unlock()
			task.mu.Lock()
			defer task.mu.Unlock()
			if task.requestHash != hash {
				response.Error(c, 409, "Idempotency key already used for another task")
				return
			}
			response.Success(c, task)
			return
		}
	}
	if !store.hasCapacityLocked(nil) || len(store.tasks) >= 1000 {
		store.mu.Unlock()
		response.Error(c, 409, "Batch task capacity reached")
		return
	}
	id, err := newTeamChildBrowserToken()
	if err != nil {
		store.mu.Unlock()
		response.InternalError(c, "Task creation failed")
		return
	}
	t := &batchOAuthTask{ID: id, sidecarID: id, ownerID: owner, Email: req.Email, config: req.batchOAuthConfig,
		loginPasswordEncrypted: passwordEncrypted, loginTOTPEncrypted: totpEncrypted,
		Status: "queued", Stage: "queued", CreatedAt: time.Now().UTC(), ExpiresAt: time.Now().Add(30 * time.Minute).UTC(), requestHash: hash, idempotencyKey: req.IdempotencyKey}
	t.mu.Lock()
	store.tasks[id] = t
	store.mu.Unlock()
	defer t.mu.Unlock()
	proxy, err := h.validateBatchConfig(c.Request.Context(), &t.config)
	if err != nil {
		t.Status, t.Stage, t.Reason = "failed", "failed", "invalid_configuration"
		response.BadRequest(c, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(c.Request.Context()), 35*time.Second)
	defer cancel()
	_ = h.startBatchAttempt(ctx, t, req.Password, req.TOTPSecret, proxy)
	response.Success(c, t)
}

func (h *OpenAIOAuthHandler) ListBatchOAuthTasks(c *gin.Context) {
	owner, ok := batchOAuthAuth(c)
	if !ok {
		return
	}
	if h.batchOAuthStore == nil {
		response.Success(c, gin.H{"items": []any{}})
		return
	}
	h.batchOAuthStore.mu.Lock()
	tasks := make([]*batchOAuthTask, 0)
	for _, t := range h.batchOAuthStore.tasks {
		if t.ownerID == owner {
			tasks = append(tasks, t)
		}
	}
	h.batchOAuthStore.mu.Unlock()
	sort.Slice(tasks, func(i, j int) bool { return tasks[i].CreatedAt.Before(tasks[j].CreatedAt) })
	items := make([]json.RawMessage, len(tasks))
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()
	var wg sync.WaitGroup
	for i, t := range tasks {
		wg.Add(1)
		go func() {
			defer wg.Done()
			t.mu.Lock()
			defer t.mu.Unlock()
			_, _ = t.refresh(ctx)
			h.revokeTerminalBatchSession(t)
			items[i], _ = json.Marshal(t)
		}()
	}
	wg.Wait()
	response.Success(c, gin.H{"items": items, "max_concurrency": 3, "max_restarts": batchOAuthMaxRestarts})
}

func (h *OpenAIOAuthHandler) GetBatchOAuthTask(c *gin.Context) {
	t, ok := h.ownedBatchTask(c)
	if !ok {
		return
	}
	defer t.mu.Unlock()
	defer h.revokeTerminalBatchSession(t)
	if _, err := t.refresh(c.Request.Context()); err != nil {
		response.Error(c, 502, "Batch automation unavailable")
		return
	}
	response.Success(c, t)
}

func validateBatchCallback(raw, expectedState string) (string, error) {
	u, err := url.Parse(raw)
	want, _ := url.Parse(openai.DefaultRedirectURI)
	if err != nil || u.Scheme != want.Scheme || u.Host != want.Host || u.Path != want.Path || u.User != nil || u.Fragment != "" {
		return "", errors.New("invalid callback")
	}
	q, err := url.ParseQuery(u.RawQuery)
	if err != nil || len(q["code"]) != 1 || len(q["state"]) != 1 || q.Get("error") != "" || len(q.Get("code")) == 0 || len(q.Get("code")) > 8192 || expectedState == "" || subtle.ConstantTimeCompare([]byte(q.Get("state")), []byte(expectedState)) != 1 {
		return "", errors.New("invalid callback state")
	}
	return q.Get("code"), nil
}

func (h *OpenAIOAuthHandler) CompleteBatchOAuthTask(c *gin.Context) {
	t, ok := h.ownedBatchTask(c)
	if !ok {
		return
	}
	defer t.mu.Unlock()
	defer h.revokeTerminalBatchSession(t)
	if t.AccountID > 0 {
		h.verifyBatchAccount(c.Request.Context(), t)
		response.Success(c, t)
		return
	}
	if t.terminal() {
		response.Error(c, 409, "Task cannot create an account")
		return
	}
	r, err := t.refresh(c.Request.Context())
	if err != nil || r == nil || t.Status != "ready" {
		response.Error(c, 409, "OAuth is not complete")
		return
	}
	// Revalidate references BEFORE consuming the single-use code. A pool proxy
	// changed mid-flight requires a fresh OAuth context, not silent reassignment.
	cfg := t.config
	if _, err := h.validateBatchConfig(c.Request.Context(), &cfg); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	if !batchProxyEqual(cfg.ProxyID, t.config.ProxyID) {
		response.Error(c, 409, "Pool proxy changed; restart OAuth")
		return
	}
	if t.loginPasswordEncrypted == "" {
		response.Error(c, 409, "Saved login credentials unavailable; restart OAuth")
		return
	}
	code, err := validateBatchCallback(r.CallbackURL, t.state)
	if err != nil {
		response.BadRequest(c, "Invalid OAuth callback")
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(c.Request.Context()), 60*time.Second)
	defer cancel()
	token, err := h.openaiOAuthService.ExchangeWorkflowCode(ctx, &service.OpenAIExchangeCodeInput{SessionID: t.sessionID, Code: code, State: t.state})
	// Ambiguous token exchange cannot be retried as a second account creation.
	h.openaiOAuthService.RevokeWorkflowSession(t.sessionID)
	t.Status, t.Stage, t.Reason = "failed", "failed", "oauth_exchange_failed"
	if err != nil {
		response.Success(c, t)
		return
	}
	if token == nil || !strings.EqualFold(strings.TrimSpace(token.Email), t.Email) || token.AccessToken == "" {
		t.Reason = "oauth_identity_mismatch"
		response.Success(c, t)
		return
	}
	name := strings.TrimSpace(t.config.Name)
	if name == "" {
		name = t.Email
	}
	schedulable := true
	credentials := h.openaiOAuthService.BuildAccountCredentials(token)
	credentials[service.OpenAIOAuthReauthorizationEmailCredentialKey] = t.Email
	credentials[service.OpenAIOAuthReauthorizationPasswordCredentialKey] = t.loginPasswordEncrypted
	if t.loginTOTPEncrypted != "" {
		credentials[service.OpenAIOAuthReauthorizationTOTPSecretCredentialKey] = t.loginTOTPEncrypted
	}
	t.createAttempted = true
	account, err := h.adminService.CreateAccount(ctx, &service.CreateAccountInput{Name: name, Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth,
		Credentials: credentials, AllowOpenAIReauthorizationCredentials: true, Extra: map[string]any{"codex_fingerprint_mode": t.config.FingerprintMode},
		GroupIDs: t.config.GroupIDs, ProxyID: t.config.ProxyID, Concurrency: t.config.Concurrency, Priority: t.config.Priority, SkipDefaultGroupBind: true, Schedulable: &schedulable})
	token = nil
	if err != nil || account == nil || account.ID <= 0 {
		t.Reason = "account_creation_requires_review"
		response.Success(c, t)
		return
	}
	t.AccountID = account.ID
	if t.config.PoolID != nil {
		var assignErr error
		if pools, ok := h.adminService.(service.AccountPoolOAuthAssigner); ok {
			_, assignErr = pools.AssignOAuthAccountPool(ctx, *t.config.PoolID, account.ID, t.config.ProxyID)
		} else if pools, ok := h.adminService.(service.AccountPoolService); ok {
			_, assignErr = pools.AssignAccountPool(ctx, *t.config.PoolID, []int64{account.ID}, false)
		} else {
			assignErr = errors.New("account pool unavailable")
		}
		if assignErr != nil {
			t.Reason = "pool_assignment_failed"
			// Read the account even on partial success, without replaying creation.
			_, _ = h.adminService.GetAccount(ctx, t.AccountID)
			response.Success(c, t)
			return
		}
	}
	h.verifyBatchAccount(ctx, t)
	response.Success(c, t)
}

func (h *OpenAIOAuthHandler) revokeTerminalBatchSession(t *batchOAuthTask) {
	if t.terminal() && h.openaiOAuthService != nil {
		h.openaiOAuthService.RevokeWorkflowSession(t.sessionID)
	}
}

func (h *OpenAIOAuthHandler) verifyBatchAccount(ctx context.Context, t *batchOAuthTask) {
	t.AccountConfig = nil
	t.Status, t.Stage, t.Reason = "failed", "failed", "account_readback_failed"
	a, err := h.adminService.GetAccount(ctx, t.AccountID)
	if err != nil || a == nil || a.ID != t.AccountID {
		return
	}
	t.Reason = "account_configuration_mismatch"
	wantGroups, actualGroups := slices.Clone(t.config.GroupIDs), slices.Clone(a.GroupIDs)
	slices.Sort(wantGroups)
	slices.Sort(actualGroups)
	wantName := strings.TrimSpace(t.config.Name)
	if wantName == "" {
		wantName = t.Email
	}
	if !a.IsOpenAIOAuth() || a.IsCredentialShadow() || a.Name != wantName || !a.Schedulable || !batchProxyEqual(a.ProxyID, t.config.ProxyID) ||
		a.Concurrency != t.config.Concurrency || a.Priority != t.config.Priority ||
		a.GetExtraString("codex_fingerprint_mode") != t.config.FingerprintMode ||
		!slices.Equal(slices.Compact(wantGroups), slices.Compact(actualGroups)) {
		return
	}
	// Verify ciphertext directly: completion must not decrypt login material or
	// silently succeed when the trusted create path dropped private credentials.
	if !strings.EqualFold(strings.TrimSpace(a.GetCredential("email")), t.Email) ||
		a.GetCredential(service.OpenAIOAuthReauthorizationEmailCredentialKey) != t.Email ||
		t.loginPasswordEncrypted == "" || a.GetCredential(service.OpenAIOAuthReauthorizationPasswordCredentialKey) != t.loginPasswordEncrypted ||
		a.GetCredential(service.OpenAIOAuthReauthorizationTOTPSecretCredentialKey) != t.loginTOTPEncrypted {
		t.Reason = "account_login_credentials_mismatch"
		return
	}
	if t.config.FingerprintMode != "off" {
		seed := a.GetExtraString("codex_fingerprint_seed")
		parsed, err := uuid.Parse(seed)
		if err != nil || parsed == uuid.Nil || seed != parsed.String() {
			return
		}
	}
	if t.config.PoolID != nil {
		pool, poolErr := h.adminService.(service.AccountPoolService).GetAccountPool(ctx, *t.config.PoolID)
		if poolErr != nil || pool == nil || pool.ID != *t.config.PoolID ||
			!batchProxyEqual(pool.ProxyID, t.config.ProxyID) || !slices.Contains(pool.AccountIDs, a.ID) ||
			a.GetExtraString(service.AccountPoolExtraKey) != strconv.FormatInt(pool.ID, 10) {
			t.Reason = "pool_assignment_failed"
			return
		}
	} else if a.GetExtraString(service.AccountPoolExtraKey) != "" {
		return
	}
	t.Status, t.Stage, t.Reason = "completed", "completed", ""
	t.AccountConfig = &batchOAuthConfig{Name: a.Name, GroupIDs: slices.Clone(a.GroupIDs), ProxyID: a.ProxyID,
		PoolID: t.config.PoolID, Concurrency: a.Concurrency, Priority: a.Priority, FingerprintMode: a.GetExtraString("codex_fingerprint_mode")}
}

func batchProxyEqual(a, b *int64) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

func (h *OpenAIOAuthHandler) cancelBatchReservation(ctx context.Context, t *batchOAuthTask, confirmed bool) error {
	if h.batchSMSService == nil {
		return nil
	}
	id, err := h.batchSMSService.WorkflowSession(ctx, t.ownerID, t.ID)
	if err != nil {
		return errors.New("SMS reservation unavailable")
	}
	if id == "" {
		return nil
	}
	if !confirmed {
		return errors.New("confirm SMS cancellation in XIASS first")
	}
	if _, err := h.batchSMSService.WorkflowAction(ctx, t.ownerID, t.ID, id, "cancel", true); err != nil {
		return errors.New("SMS cancellation failed")
	}
	return nil
}

func (h *OpenAIOAuthHandler) CancelBatchOAuthTask(c *gin.Context) {
	t, ok := h.ownedBatchTask(c)
	if !ok {
		return
	}
	defer t.mu.Unlock()
	var req struct {
		Confirmed bool `json:"confirmed"`
	}
	if c.ShouldBindJSON(&req) != nil {
		response.BadRequest(c, "Invalid cancellation")
		return
	}
	if t.AccountID > 0 {
		response.Error(c, 409, "Account already created")
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(c.Request.Context()), 35*time.Second)
	defer cancel()
	if err := h.cancelBatchReservation(ctx, t, req.Confirmed); err != nil {
		response.Error(c, 409, err.Error())
		return
	}
	if t.Status != "canceled" {
		if _, err := t.sidecar(ctx, "cancel", nil); err != nil {
			response.Error(c, 502, "Browser cancellation not confirmed")
			return
		}
	}
	h.openaiOAuthService.RevokeWorkflowSession(t.sessionID)
	t.Status, t.Stage, t.Reason, t.RequiresSMSConfirmation = "canceled", "canceled", "", false
	response.Success(c, t)
}

func (h *OpenAIOAuthHandler) RestartBatchOAuthTask(c *gin.Context) {
	t, ok := h.ownedBatchTask(c)
	if !ok {
		return
	}
	defer t.mu.Unlock()
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 16<<10)
	var req struct {
		Password   *string `json:"password"`
		TOTPSecret *string `json:"totp_secret"`
		Confirmed  bool    `json:"confirmed"`
	}
	if c.ShouldBindJSON(&req) != nil {
		response.BadRequest(c, "Valid login credentials required")
		return
	}
	if t.AccountID > 0 || t.RestartCount >= batchOAuthMaxRestarts || t.createAttempted {
		response.Error(c, 409, "Task cannot restart")
		return
	}
	if h.secretEncryptor == nil {
		response.Error(c, 503, "Login encryption unavailable")
		return
	}
	var password, secret string
	var err error
	if req.Password != nil {
		password = *req.Password
	} else {
		password, err = h.secretEncryptor.Decrypt(t.loginPasswordEncrypted)
	}
	if err == nil {
		if req.TOTPSecret != nil {
			secret = *req.TOTPSecret
		} else if t.loginTOTPEncrypted != "" {
			secret, err = h.secretEncryptor.Decrypt(t.loginTOTPEncrypted)
		}
	}
	if err != nil || validateBatchLogin(t.Email, password, secret) != nil {
		response.BadRequest(c, "Valid login credentials required")
		return
	}
	passwordEncrypted, totpEncrypted, err := h.encryptBatchLogin(password, secret)
	if err != nil {
		response.InternalError(c, "Login encryption failed")
		return
	}
	store := h.batchOAuthStore
	store.mu.Lock()
	if !store.hasCapacityLocked(t) {
		store.mu.Unlock()
		response.Error(c, 409, "Batch task capacity reached")
		return
	}
	previousStatus := t.Status
	t.Status = "queued"
	store.mu.Unlock()
	defer func() {
		if t.Status == "queued" {
			t.Status = previousStatus
		}
	}()
	proxy, err := h.validateBatchConfig(c.Request.Context(), &t.config)
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(c.Request.Context()), 60*time.Second)
	defer cancel()
	if err := h.cancelBatchReservation(ctx, t, req.Confirmed); err != nil {
		response.Error(c, 409, err.Error())
		return
	}
	if _, err := t.sidecar(ctx, "cancel", nil); err != nil {
		response.Error(c, 502, "Browser cancellation not confirmed")
		return
	}
	h.openaiOAuthService.RevokeWorkflowSession(t.sessionID)
	id, err := newTeamChildBrowserToken()
	if err != nil {
		response.InternalError(c, "Task restart failed")
		return
	}
	t.sidecarID = id
	t.submittedPhone = ""
	t.RestartCount++
	t.RequiresSMSConfirmation = false
	t.loginPasswordEncrypted, t.loginTOTPEncrypted = passwordEncrypted, totpEncrypted
	_ = h.startBatchAttempt(ctx, t, password, secret, proxy)
	response.Success(c, t)
}

func (h *OpenAIOAuthHandler) BatchOAuthSMSAction(c *gin.Context) {
	t, ok := h.ownedBatchTask(c)
	if !ok {
		return
	}
	defer t.mu.Unlock()
	defer h.revokeTerminalBatchSession(t)
	if h.batchSMSService == nil {
		response.Error(c, 503, "SMS unavailable")
		return
	}
	action := c.Param("action")
	if c.Request.Method == http.MethodGet {
		action = "check"
	}
	if action != "check" && action != "acquire" && action != "change" && action != "cancel" {
		response.NotFound(c, "Unknown SMS action")
		return
	}
	confirmed := false
	if action != "check" {
		var req struct {
			Confirmed bool `json:"confirmed"`
		}
		if c.ShouldBindJSON(&req) != nil || !req.Confirmed {
			response.BadRequest(c, "Confirm the SMS action in XIASS first")
			return
		}
		confirmed = true
	}
	if _, err := t.refresh(c.Request.Context()); err != nil {
		response.Error(c, 502, "Cannot validate live workflow")
		return
	}
	if action != "cancel" && (t.terminal() || (t.Stage != "phone_required" && t.Stage != "sms_waiting")) {
		response.Error(c, 409, "Workflow is not at a phone verification node")
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(c.Request.Context()), 35*time.Second)
	defer cancel()
	id, err := h.batchSMSService.WorkflowSession(ctx, t.ownerID, t.ID)
	if err != nil {
		response.Error(c, 502, "SMS reservation unavailable")
		return
	}
	if id == "" && action == "check" {
		response.Success(c, gin.H{"task": t, "sms": nil})
		return
	}
	if id == "" && action == "cancel" {
		response.Success(c, gin.H{"task": t, "sms": nil})
		return
	}
	if action == "acquire" && t.Stage != "phone_required" {
		response.Error(c, 409, "Workflow does not require a phone number")
		return
	}
	if action == "change" && t.Stage != "phone_required" {
		response.Error(c, 409, "Restart OAuth before replacing a submitted phone")
		return
	}
	result, err := h.batchSMSService.WorkflowAction(ctx, t.ownerID, t.ID, id, action, confirmed)
	if err != nil {
		response.Error(c, 502, "SMS action failed; reservation retained for retry")
		return
	}
	if result == nil {
		response.Error(c, 502, "Invalid SMS response")
		return
	}
	if action != "cancel" {
		if result.Code != "" && t.Stage == "sms_waiting" {
			if _, err := t.sidecar(ctx, "sms-code", map[string]any{"code": result.Code}); err != nil {
				response.Error(c, 502, "SMS code delivery failed; restart required")
				return
			}
		} else if result.Number != "" && t.Stage == "phone_required" {
			phone := strings.TrimSpace(result.Number)
			if !strings.HasPrefix(phone, "+") {
				phone = "+" + phone
			}
			if !teamChildWorkflowPhonePattern.MatchString(phone) {
				response.Error(c, 502, "SMS provider returned an invalid phone number")
				return
			}
			if t.rejectedPhones[phone] || (t.Reason == "phone_rejected" && action != "change") {
				t.RequiresSMSConfirmation = true
				response.Error(c, 409, "Confirm a different phone number in XIASS first")
				return
			}
			// Remember ambiguous delivery too: subsequent rejection observations
			// must still identify the number that the browser may have submitted.
			t.submittedPhone = phone
			if _, err := t.sidecar(ctx, "phone", map[string]any{"phone": phone}); err != nil {
				response.Error(c, 502, "Phone delivery failed; retry current reservation")
				return
			}
		}
	}
	// Codes and provider session IDs are never accepted from or disclosed to UI.
	response.Success(c, gin.H{"task": t, "sms": gin.H{"status": result.Status, "number": result.Number, "expires_at": result.ExpiresAt}})
}
