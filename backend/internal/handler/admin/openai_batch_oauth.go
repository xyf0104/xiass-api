package admin

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
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
	"sync/atomic"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
	"github.com/Wei-Shaw/sub2api/internal/pkg/redisclient"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

const batchOAuthMaxRestarts = 2
const batchOAuthAlreadyExistsReason = "account_already_exists"

const (
	batchOAuthModeCreate          = "create"
	batchOAuthModeReauthorization = "reauthorization"
	batchOAuthLoginPassword       = "password"
	batchOAuthLoginEmailCode      = "email_code"
	batchOAuthBrowserServer       = "server"
	batchOAuthBrowserAdsPower     = "adspower"
)

var errBatchOAuthMissing = errors.New("batch task unavailable")

var releaseBatchOAuthLock = redisclient.NewScript(`
if redis.call("GET", KEYS[1]) == ARGV[1] then
  return redis.call("DEL", KEYS[1])
end
return 0
`)

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
	BrowserMode     string  `json:"browser_mode,omitempty"`
}

type batchOAuthStartRequest struct {
	batchOAuthConfig
	Email          string `json:"email"`
	ExpectedEmail  string `json:"expected_email"`
	Password       string `json:"password"`
	TOTPSecret     string `json:"totp_secret"`
	LoginMethod    string `json:"login_method"`
	EmailCodeToken string `json:"email_code_token"`
	IdempotencyKey string `json:"idempotency_key"`
}

// Login material remains encrypted and excluded from JSON until account import.
// Tokens, callback URLs and page snapshots never enter this process-local store.
type batchOAuthTask struct {
	mu                      sync.Mutex
	ID                      string            `json:"task_id"`
	Mode                    string            `json:"mode"`
	Email                   string            `json:"email"`
	LoginMethod             string            `json:"login_method"`
	Status                  string            `json:"status"`
	Stage                   string            `json:"stage"`
	Reason                  string            `json:"reason,omitempty"`
	AccountID               int64             `json:"account_id,omitempty"`
	TargetAccountID         int64             `json:"target_account_id,omitempty"`
	AccountConfig           *batchOAuthConfig `json:"account_config,omitempty"`
	RestartCount            int               `json:"restart_count"`
	ReauthorizationNumber   int               `json:"reauthorization_number,omitempty"`
	BrowserMode             string            `json:"browser_mode,omitempty"`
	RequiresSMSConfirmation bool              `json:"requires_sms_confirmation"`
	CreatedAt               time.Time         `json:"created_at"`
	ExpiresAt               time.Time         `json:"expires_at"`
	FinishedAt              *time.Time        `json:"finished_at,omitempty"`
	ownerID                 int64
	config                  batchOAuthConfig
	requestHash             [32]byte
	idempotencyKey          string
	sidecarID               string
	sessionID               string
	state                   string
	authURL                 string
	externalCallbackURL     string
	adsPowerLaunchIssued    bool
	createAttempted         bool
	submittedPhone          string
	rejectedPhones          map[string]bool
	loginPasswordEncrypted  string `json:"-"`
	loginTOTPEncrypted      string `json:"-"`
	loginEmailCodeEncrypted string `json:"-"`
	loginTOTPConfigured     bool
	executionNodeID         string
	reauthStateEvent        string
	publicSnapshot          atomic.Pointer[[]byte]
}

type batchOAuthStore struct {
	mu           sync.Mutex
	poolNamingMu sync.Mutex
	tasks        map[string]*batchOAuthTask
}

func newBatchOAuthStore() *batchOAuthStore {
	return &batchOAuthStore{tasks: make(map[string]*batchOAuthTask)}
}

func (h *OpenAIOAuthHandler) acquireBatchOAuthLock(ctx context.Context, key string, wait time.Duration) (func(), error) {
	if h == nil || h.teamMailboxStore == nil {
		return func() {}, nil
	}
	client := h.teamMailboxStore.redisClient()
	if client == nil {
		return func() {}, nil
	}
	token, err := newTeamChildBrowserToken()
	if err != nil {
		return nil, errors.New("authorization lock unavailable")
	}
	deadline := time.Now().Add(wait)
	for {
		acquired, err := client.SetNX(ctx, key, token, 2*time.Minute).Result()
		if err != nil {
			return nil, errors.New("authorization lock unavailable")
		}
		if acquired {
			return func() {
				releaseCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				defer cancel()
				_, _ = releaseBatchOAuthLock.Run(releaseCtx, client, []string{key}, token).Result()
			}, nil
		}
		if time.Now().After(deadline) {
			return nil, errors.New("authorization finalization is busy")
		}
		select {
		case <-ctx.Done():
			return nil, errors.New("authorization finalization is busy")
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func batchOAuthLockKey(kind, value string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(value)))
	return "xiass:openai:batch-oauth:" + kind + ":" + hex.EncodeToString(sum[:16])
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
			task.clearLoginCredentials()
			delete(s.tasks, id)
		}
		task.mu.Unlock()
	}
	return active < 3
}

func (s *batchOAuthStore) pruneTerminalDuplicatesLocked(owner int64) {
	type candidate struct {
		id      string
		score   int
		created time.Time
	}
	keep := make(map[string]candidate)
	busy := make(map[string]bool)
	for id, task := range s.tasks {
		if task.ownerID != owner {
			continue
		}
		key := task.duplicateKey()
		if !task.mu.TryLock() {
			// A concurrent refresh/restart owns this task. Defer pruning every
			// record for the mailbox so the busy task cannot be misclassified.
			busy[key] = true
			continue
		}
		score := 1
		if !task.terminal() {
			score = 3
		} else if task.Status == "completed" {
			score = 2
		}
		current, found := keep[key]
		if !found || score > current.score || (score == current.score && task.CreatedAt.After(current.created)) {
			keep[key] = candidate{id: id, score: score, created: task.CreatedAt}
		}
		task.mu.Unlock()
	}
	for id, task := range s.tasks {
		key := task.duplicateKey()
		if task.ownerID != owner || busy[key] || keep[key].id == id || !task.mu.TryLock() {
			continue
		}
		if task.terminal() {
			task.clearLoginCredentials()
			delete(s.tasks, id)
		}
		task.mu.Unlock()
	}
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

func normalizeBatchOAuthLoginMethod(method string) string {
	if strings.TrimSpace(method) == "" {
		return batchOAuthLoginPassword
	}
	return strings.TrimSpace(method)
}

func validateBatchEmailCodeLogin(email, token string) error {
	if !validTeamChildWorkflowEmail(email) || email != strings.TrimSpace(email) || len(token) != 64 {
		return errors.New("valid account email and email-code token are required")
	}
	for _, char := range token {
		if !strings.ContainsRune("0123456789abcdefABCDEF", char) {
			return errors.New("valid account email and email-code token are required")
		}
	}
	return nil
}

func validateBatchLoginMethod(email, method, password, secret, emailCodeToken string) error {
	switch normalizeBatchOAuthLoginMethod(method) {
	case batchOAuthLoginPassword:
		if emailCodeToken != "" {
			return errors.New("password login cannot include an email-code token")
		}
		return validateBatchLogin(email, password, secret)
	case batchOAuthLoginEmailCode:
		if password != "" || secret != "" {
			return errors.New("email-code login cannot include a password or authenticator secret")
		}
		return validateBatchEmailCodeLogin(email, emailCodeToken)
	default:
		return errors.New("unsupported OpenAI login method")
	}
}

func (t *batchOAuthTask) normalizedMode() string {
	if t != nil && t.Mode == batchOAuthModeReauthorization {
		return batchOAuthModeReauthorization
	}
	return batchOAuthModeCreate
}

func normalizeBatchOAuthBrowserMode(mode string) string {
	if strings.TrimSpace(mode) == "" {
		return batchOAuthBrowserServer
	}
	return strings.ToLower(strings.TrimSpace(mode))
}

func (t *batchOAuthTask) usesAdsPower() bool {
	return t != nil && normalizeBatchOAuthBrowserMode(t.config.BrowserMode) == batchOAuthBrowserAdsPower
}

func (t *batchOAuthTask) duplicateKey() string {
	if t.normalizedMode() == batchOAuthModeReauthorization && t.TargetAccountID > 0 {
		return batchOAuthModeReauthorization + ":" + strconv.FormatInt(t.TargetAccountID, 10)
	}
	return batchOAuthModeCreate + ":" + strings.ToLower(strings.TrimSpace(t.Email))
}

func (t *batchOAuthTask) matchesMode(mode string) bool {
	return t != nil && t.normalizedMode() == mode
}

func (h *OpenAIOAuthHandler) encryptBatchLogin(method, password, secret, emailCodeToken string) (string, string, string, error) {
	if h.secretEncryptor == nil {
		return "", "", "", errors.New("login encryption unavailable")
	}
	if normalizeBatchOAuthLoginMethod(method) == batchOAuthLoginEmailCode {
		tokenEncrypted, err := h.secretEncryptor.Encrypt(emailCodeToken)
		if err != nil || tokenEncrypted == "" {
			return "", "", "", errors.New("login encryption failed")
		}
		return "", "", tokenEncrypted, nil
	}
	passwordEncrypted, err := h.secretEncryptor.Encrypt(password)
	if err != nil || passwordEncrypted == "" {
		return "", "", "", errors.New("login encryption failed")
	}
	var totpEncrypted string
	if secret != "" {
		totpEncrypted, err = h.secretEncryptor.Encrypt(secret)
		if err != nil || totpEncrypted == "" {
			return "", "", "", errors.New("login encryption failed")
		}
	}
	return passwordEncrypted, totpEncrypted, "", nil
}

func (h *OpenAIOAuthHandler) validateBatchConfig(ctx context.Context, cfg *batchOAuthConfig) (map[string]string, error) {
	if cfg.Concurrency < 1 || cfg.Concurrency > 1000 || cfg.Priority < 0 || cfg.Priority > 1000 || len(cfg.Name) > 100 || len(cfg.GroupIDs) > 100 {
		return nil, errors.New("invalid account configuration")
	}
	if cfg.FingerprintMode == "" {
		cfg.FingerprintMode = "off"
	}
	cfg.BrowserMode = normalizeBatchOAuthBrowserMode(cfg.BrowserMode)
	if cfg.BrowserMode != batchOAuthBrowserServer && cfg.BrowserMode != batchOAuthBrowserAdsPower {
		return nil, errors.New("invalid browser mode")
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
	proxy, err := h.batchOAuthBrowserProxy(ctx, cfg.ProxyID)
	if err != nil {
		return nil, err
	}
	if cfg.BrowserMode == batchOAuthBrowserAdsPower {
		return nil, nil
	}
	return proxy, nil
}

func (h *OpenAIOAuthHandler) batchOAuthBrowserProxy(ctx context.Context, proxyID *int64) (map[string]string, error) {
	if proxyID == nil {
		return nil, nil
	}
	if *proxyID <= 0 {
		return nil, errors.New("invalid proxy")
	}
	p, err := h.adminService.GetProxy(ctx, *proxyID)
	if err != nil || p == nil || !p.IsActive() || p.IsExpired(time.Now()) {
		return nil, errors.New("proxy unavailable")
	}
	if p.Protocol != "http" && p.Protocol != "https" && p.Protocol != "socks5" {
		return nil, errors.New("browser proxy protocol unsupported")
	}
	// The built-in execution-node proxy is loopback inside xiass-api. The
	// browser sidecar runs on the same host but in a different container, so its
	// equivalent local-node route is direct Docker egress from that same host.
	localNodeID := strings.TrimSpace(os.Getenv("GATEWAY_EXECUTION_NODE_ID"))
	if strings.HasPrefix(p.Name, service.ExecutionNodeBuiltinProxyNamePrefix) &&
		p.Protocol == "socks5" && p.Host == "127.0.0.1" && p.Port == 19080 && strings.TrimSpace(p.Password) != "" {
		if localNodeID == "" || p.Name != service.ExecutionNodeBuiltinProxyNamePrefix+localNodeID || p.Username != localNodeID {
			return nil, errors.New("proxy belongs to another execution node")
		}
		return nil, nil
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

func (h *OpenAIOAuthHandler) ownedBatchTask(c *gin.Context, mode string) (*batchOAuthTask, bool) {
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
	if task == nil || task.ownerID != owner || !task.matchesMode(mode) {
		response.NotFound(c, "Task not found")
		return nil, false
	}
	task.mu.Lock()
	return task, true
}

func (t *batchOAuthTask) terminal() bool {
	return t.Status == "completed" || t.Status == "canceled" || t.Status == "failed" || t.Status == "blocked"
}

func batchOAuthAccountMatchesEmail(account *service.Account, email string) bool {
	if account == nil || !account.IsOpenAIOAuth() || account.IsCredentialShadow() {
		return false
	}
	for _, candidate := range []string{
		account.Name,
		account.GetCredential("email"),
		account.GetCredential(service.OpenAIOAuthReauthorizationEmailCredentialKey),
	} {
		if strings.EqualFold(strings.TrimSpace(candidate), email) {
			return true
		}
	}
	if workflowEmail, ok := teamChildAccountWorkflowEmail(account); ok {
		return strings.EqualFold(strings.TrimSpace(workflowEmail), email)
	}
	return false
}

func (h *OpenAIOAuthHandler) existingBatchOAuthAccount(ctx context.Context, email string) (*service.Account, error) {
	const pageSize = 200
	for page := 1; ; page++ {
		accounts, total, err := h.adminService.ListAccounts(ctx, page, pageSize, service.PlatformOpenAI, service.AccountTypeOAuth, "", "", 0, "", "id", "asc")
		if err != nil {
			return nil, err
		}
		for i := range accounts {
			if batchOAuthAccountMatchesEmail(&accounts[i], email) {
				account := accounts[i]
				return &account, nil
			}
		}
		if len(accounts) == 0 || int64(page*pageSize) >= total {
			return nil, nil
		}
	}
}

func normalizeBatchOAuthPlanLabel(plan string) string {
	switch strings.ToLower(strings.TrimSpace(plan)) {
	case "pro":
		return "pro"
	case "prolite", "pro_lite", "pro-lite", "pro lite":
		return "prolite"
	case "plus":
		return "plus"
	case "team", "business", "self_serve_business", "self_serve_business_usage_based":
		return "team"
	case "free":
		return "free"
	default:
		return "oauth"
	}
}

func batchOAuthGeneratedAccountName(poolName, plan string, sequence int) string {
	suffix := normalizeBatchOAuthPlanLabel(plan) + strconv.Itoa(max(sequence, 1))
	poolRunes := []rune(strings.TrimSpace(poolName))
	maxPoolRunes := 100 - len([]rune(suffix))
	if maxPoolRunes < 0 {
		maxPoolRunes = 0
	}
	if len(poolRunes) > maxPoolRunes {
		poolRunes = poolRunes[:maxPoolRunes]
	}
	return string(poolRunes) + suffix
}

func (h *OpenAIOAuthHandler) nextBatchOAuthPoolAccountName(ctx context.Context, poolID int64, credentials map[string]any) (string, error) {
	pools, ok := h.adminService.(service.AccountPoolService)
	if !ok {
		return "", service.ErrAccountPoolUnavailable
	}
	pool, err := pools.GetAccountPool(ctx, poolID)
	if err != nil {
		return "", err
	}
	plan, _ := credentials["plan_type"].(string)
	planLabel := normalizeBatchOAuthPlanLabel(plan)
	prefix := strings.TrimSpace(pool.Name) + planLabel
	maxSequence := 0
	matchingPlanCount := 0
	const pageSize = 200
	for page := 1; ; page++ {
		lister, ok := h.adminService.(activeConcurrencyAccountLister)
		if !ok {
			return "", errors.New("account pool member listing unavailable")
		}
		accounts, total, listErr := lister.ListAccountsByIDs(ctx, page, pageSize, pool.AccountIDs, service.PlatformOpenAI, service.AccountTypeOAuth, "", "", 0, "", "id", "asc")
		if listErr != nil {
			return "", listErr
		}
		for i := range accounts {
			account := &accounts[i]
			if normalizeBatchOAuthPlanLabel(account.GetCredential("plan_type")) != planLabel {
				continue
			}
			matchingPlanCount++
			if suffix := strings.TrimPrefix(account.Name, prefix); suffix != account.Name {
				if sequence, parseErr := strconv.Atoi(suffix); parseErr == nil && sequence > maxSequence {
					maxSequence = sequence
				}
			}
		}
		if len(accounts) == 0 || int64(page*pageSize) >= total {
			break
		}
	}
	if matchingPlanCount > maxSequence {
		maxSequence = matchingPlanCount
	}
	return batchOAuthGeneratedAccountName(pool.Name, planLabel, maxSequence+1), nil
}

func (t *batchOAuthTask) markFinishedIfTerminal() {
	if !t.terminal() {
		return
	}
	if t.FinishedAt == nil {
		now := time.Now().UTC()
		t.FinishedAt = &now
	}
	// A terminal workflow must not remain a reusable credential store. The
	// encrypted login is copied to the account only after OAuth succeeds.
	t.clearLoginCredentials()
}

func (t *batchOAuthTask) snapshotJSONLocked() json.RawMessage {
	raw, err := json.Marshal(t)
	if err != nil {
		return nil
	}
	stored := append([]byte(nil), raw...)
	t.publicSnapshot.Store(&stored)
	return json.RawMessage(stored)
}

func (t *batchOAuthTask) cachedSnapshotJSON() json.RawMessage {
	stored := t.publicSnapshot.Load()
	if stored == nil {
		return nil
	}
	return json.RawMessage(append([]byte(nil), (*stored)...))
}

func (t *batchOAuthTask) clearLoginCredentials() {
	t.loginPasswordEncrypted = ""
	t.loginTOTPEncrypted = ""
	t.loginEmailCodeEncrypted = ""
}

func batchOAuthPublicStage(stage string) string {
	switch stage {
	case "queued", "opening", "login", "email", "password", "totp", "phone_required", "phone_submitting",
		"email_code_waiting", "email_code_submitting", "sms_waiting", "sms_submitting", "workspace", "callback_waiting", "callback_received", "completed",
		"profile", "external_browser", "failed", "blocked", "canceled":
		return stage
	default:
		return "login"
	}
}

func batchOAuthPublicReason(reason string) string {
	switch reason {
	case "sms_timeout", "sms_confirmation_timeout", "email_code_required", "email_code_timeout", "email_code_access_denied", "email_code_unavailable", "invalid_email_code", "reauthorization_phone_required", "captcha_required", "captcha", "account_blocked", "account_banned", "account_deleted_or_disabled", "unknown_error", "manual_challenge", "task_expired", "invalid_credentials", "authenticator_required", "phone_rejected":
		return reason
	case "proxy_unavailable", "navigation_timeout", "browser_context_lost", "page_interaction_failed", "invalid_totp", "invalid_sms_code", "openai_route_error", "oauth_session_expired":
		return reason
	case "oauth_exchange_failed", "oauth_identity_mismatch", "account_update_failed", "account_configuration_changed", "account_state_recovery_failed", "invalid_configuration", "automation_start_failed", "oauth_session_failed":
		return reason
	case batchOAuthAlreadyExistsReason:
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
		if !t.usesAdsPower() {
			_, _ = t.sidecar(ctx, "cancel", nil)
		}
		return nil, nil
	}
	if t.usesAdsPower() {
		if t.externalCallbackURL == "" {
			t.Status = "running"
			if t.Stage == "" || t.Stage == "queued" || t.Stage == "external_browser" {
				t.Stage, t.Reason = "external_browser", ""
			}
			return &batchOAuthSidecarTask{ID: t.ID, OwnerID: t.ownerID, Status: t.Status, Stage: t.Stage, Reason: t.Reason}, nil
		}
		t.Status, t.Stage, t.Reason = "ready", "callback_received", ""
		return &batchOAuthSidecarTask{ID: t.ID, OwnerID: t.ownerID, Status: "completed", Stage: "callback_received", CallbackURL: t.externalCallbackURL}, nil
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
	t.Stage = batchOAuthPublicStage(r.Stage)
	t.Reason = batchOAuthPublicReason(r.Reason)
	if t.Reason == "phone_rejected" && t.submittedPhone != "" {
		if t.rejectedPhones == nil {
			t.rejectedPhones = make(map[string]bool)
		}
		t.rejectedPhones[t.submittedPhone] = true
	}
	t.RequiresSMSConfirmation = t.Stage == "phone_required" || t.Reason == "sms_timeout" || t.Reason == "sms_confirmation_timeout"
	t.markFinishedIfTerminal()
	return r, nil
}

func (h *OpenAIOAuthHandler) startBatchAttempt(ctx context.Context, t *batchOAuthTask, password, secret, emailCodeToken string, proxy map[string]string) error {
	t.Status, t.Stage, t.Reason = "failed", "failed", "oauth_session_failed"
	oauthProxyID := t.config.ProxyID
	if t.usesAdsPower() {
		// The local AdsPower profile exits through the current XIASS server's
		// dedicated SOCKS route. Keep the OAuth code exchange on that same server
		// instead of silently switching to an unrelated account runtime proxy.
		oauthProxyID = nil
	}
	auth, err := h.openaiOAuthService.GenerateAuthURL(ctx, oauthProxyID, openai.DefaultRedirectURI, service.PlatformOpenAI)
	if err != nil {
		return errors.New("OAuth session creation failed")
	}
	u, err := url.Parse(auth.AuthURL)
	if err != nil || u.Query().Get("state") == "" {
		h.openaiOAuthService.RevokeWorkflowSession(auth.SessionID)
		return errors.New("OAuth URL invalid")
	}
	t.sessionID, t.state, t.authURL = auth.SessionID, u.Query().Get("state"), auth.AuthURL
	t.externalCallbackURL = ""
	t.adsPowerLaunchIssued = false
	t.ExpiresAt = time.Now().Add(openai.SessionTTL).UTC()
	t.BrowserMode = normalizeBatchOAuthBrowserMode(t.config.BrowserMode)
	if t.usesAdsPower() {
		if (t.LoginMethod == batchOAuthLoginEmailCode && t.loginEmailCodeEncrypted == "") ||
			(t.LoginMethod != batchOAuthLoginEmailCode && t.loginPasswordEncrypted == "") {
			passwordEncrypted, totpEncrypted, emailCodeEncrypted, encryptErr := h.encryptBatchLogin(
				t.LoginMethod, password, secret, emailCodeToken,
			)
			if encryptErr != nil {
				h.openaiOAuthService.RevokeWorkflowSession(t.sessionID)
				return errors.New("AdsPower login encryption failed")
			}
			t.loginPasswordEncrypted = passwordEncrypted
			t.loginTOTPEncrypted = totpEncrypted
			t.loginEmailCodeEncrypted = emailCodeEncrypted
			t.loginTOTPConfigured = secret != ""
		}
		t.Status, t.Stage, t.Reason = "running", "external_browser", ""
		t.FinishedAt = nil
		return nil
	}
	payload := map[string]any{"task_id": t.sidecarID, "owner_id": t.ownerID, "email": t.Email, "expected_email": t.Email,
		"workflow_mode": t.normalizedMode(),
		"login_method":  t.LoginMethod, "password": password, "totp_secret": secret, "email_code_token": emailCodeToken,
		"auth_url": auth.AuthURL, "oauth_session_id": auth.SessionID}
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
	t.Status, t.Stage, t.Reason = "running", batchOAuthPublicStage(r.Stage), ""
	t.FinishedAt = nil
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
	req.LoginMethod = normalizeBatchOAuthLoginMethod(req.LoginMethod)
	if req.ExpectedEmail != "" && !strings.EqualFold(strings.TrimSpace(req.ExpectedEmail), req.Email) {
		response.BadRequest(c, "expected_email must match the account login email")
		return
	}
	if err := validateBatchLoginMethod(req.Email, req.LoginMethod, req.Password, req.TOTPSecret, req.EmailCodeToken); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	if len(req.IdempotencyKey) < 16 || len(req.IdempotencyKey) > 128 {
		response.BadRequest(c, "idempotency_key must contain 16-128 characters")
		return
	}
	if req.Concurrency == 0 {
		req.Concurrency = 1
	}
	// Only non-secret configuration participates in the retry identity.
	raw, _ := json.Marshal(struct {
		Email       string
		LoginMethod string
		Config      batchOAuthConfig
	}{req.Email, req.LoginMethod, req.batchOAuthConfig})
	hash := sha256.Sum256(raw)
	existingAccount, err := h.existingBatchOAuthAccount(c.Request.Context(), req.Email)
	if err != nil {
		response.InternalError(c, "Unable to check existing OpenAI accounts")
		return
	}
	var passwordEncrypted, totpEncrypted, emailCodeEncrypted string
	if existingAccount == nil {
		passwordEncrypted, totpEncrypted, emailCodeEncrypted, err = h.encryptBatchLogin(req.LoginMethod, req.Password, req.TOTPSecret, req.EmailCodeToken)
		if err != nil {
			response.InternalError(c, "Login encryption failed")
			return
		}
	}
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
	// One mailbox may have only one live workflow. Starting a new attempt also
	// removes older terminal task rows; the durable account remains untouched.
	for id, task := range store.tasks {
		if task.ownerID != owner || !task.matchesMode(batchOAuthModeCreate) || !strings.EqualFold(task.Email, req.Email) {
			continue
		}
		if !task.mu.TryLock() {
			store.mu.Unlock()
			response.Error(c, 409, "An authorization task for this email is busy")
			return
		}
		if !task.terminal() {
			task.mu.Unlock()
			store.mu.Unlock()
			response.Error(c, 409, "An authorization task for this email is already running")
			return
		}
		task.clearLoginCredentials()
		delete(store.tasks, id)
		task.mu.Unlock()
	}
	if (existingAccount == nil && !store.hasCapacityLocked(nil)) || len(store.tasks) >= 1000 {
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
	t := &batchOAuthTask{ID: id, Mode: batchOAuthModeCreate, sidecarID: id, ownerID: owner, Email: req.Email, LoginMethod: req.LoginMethod, config: req.batchOAuthConfig,
		loginPasswordEncrypted: passwordEncrypted, loginTOTPEncrypted: totpEncrypted, loginEmailCodeEncrypted: emailCodeEncrypted, loginTOTPConfigured: req.TOTPSecret != "",
		BrowserMode: normalizeBatchOAuthBrowserMode(req.BrowserMode), Status: "queued", Stage: "queued", CreatedAt: time.Now().UTC(), ExpiresAt: time.Now().Add(30 * time.Minute).UTC(), requestHash: hash, idempotencyKey: req.IdempotencyKey}
	if existingAccount != nil {
		now := time.Now().UTC()
		t.Status, t.Stage, t.Reason, t.AccountID = "completed", "completed", batchOAuthAlreadyExistsReason, existingAccount.ID
		t.FinishedAt = &now
		t.ExpiresAt = now.Add(24 * time.Hour)
	}
	t.snapshotJSONLocked()
	t.mu.Lock()
	store.tasks[id] = t
	store.mu.Unlock()
	defer t.mu.Unlock()
	defer t.snapshotJSONLocked()
	defer t.markFinishedIfTerminal()
	if existingAccount != nil {
		response.Success(c, t)
		return
	}
	proxy, err := h.validateBatchConfig(c.Request.Context(), &t.config)
	if err != nil {
		t.Status, t.Stage, t.Reason = "failed", "failed", "invalid_configuration"
		response.BadRequest(c, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(c.Request.Context()), 35*time.Second)
	defer cancel()
	_ = h.startBatchAttempt(ctx, t, req.Password, req.TOTPSecret, req.EmailCodeToken, proxy)
	response.Success(c, t)
}

func (h *OpenAIOAuthHandler) ListBatchOAuthTasks(c *gin.Context) {
	h.listBatchOAuthTasks(c, batchOAuthModeCreate)
}

func (h *OpenAIOAuthHandler) listBatchOAuthTasks(c *gin.Context, mode string) {
	owner, ok := batchOAuthAuth(c)
	if !ok {
		return
	}
	if h.batchOAuthStore == nil {
		response.Success(c, gin.H{"items": []any{}})
		return
	}
	h.batchOAuthStore.mu.Lock()
	h.batchOAuthStore.pruneTerminalDuplicatesLocked(owner)
	tasks := make([]*batchOAuthTask, 0)
	for _, t := range h.batchOAuthStore.tasks {
		if t.ownerID == owner && t.matchesMode(mode) {
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
			if !t.mu.TryLock() {
				items[i] = t.cachedSnapshotJSON()
				return
			}
			defer t.mu.Unlock()
			_, _ = t.refresh(ctx)
			t.markFinishedIfTerminal()
			if mode == batchOAuthModeReauthorization {
				_ = h.persistOpenAIReauthorizationTaskState(ctx, t)
			}
			h.revokeTerminalBatchSession(t)
			items[i] = t.snapshotJSONLocked()
		}()
	}
	wg.Wait()
	items = slices.DeleteFunc(items, func(item json.RawMessage) bool { return len(item) == 0 })
	response.Success(c, gin.H{"items": items, "max_concurrency": 3, "max_restarts": batchOAuthMaxRestarts})
}

func (h *OpenAIOAuthHandler) GetBatchOAuthTask(c *gin.Context) {
	h.getBatchOAuthTask(c, batchOAuthModeCreate)
}

func (h *OpenAIOAuthHandler) getBatchOAuthTask(c *gin.Context, mode string) {
	t, ok := h.ownedBatchTask(c, mode)
	if !ok {
		return
	}
	defer t.mu.Unlock()
	defer t.markFinishedIfTerminal()
	defer h.revokeTerminalBatchSession(t)
	if _, err := t.refresh(c.Request.Context()); err != nil {
		response.Error(c, 502, "Batch automation unavailable")
		return
	}
	if mode == batchOAuthModeReauthorization {
		_ = h.persistOpenAIReauthorizationTaskState(c.Request.Context(), t)
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
	t, ok := h.ownedBatchTask(c, batchOAuthModeCreate)
	if !ok {
		return
	}
	defer t.mu.Unlock()
	defer t.markFinishedIfTerminal()
	defer h.revokeTerminalBatchSession(t)
	if t.AccountID > 0 {
		if t.Status == "completed" && t.Reason == batchOAuthAlreadyExistsReason {
			response.Success(c, t)
			return
		}
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
	emailUnlock, err := h.acquireBatchOAuthLock(c.Request.Context(), batchOAuthLockKey("email", strings.ToLower(t.Email)), 15*time.Second)
	if err != nil {
		response.Error(c, 409, err.Error())
		return
	}
	defer emailUnlock()
	if t.config.PoolID != nil {
		poolUnlock, lockErr := h.acquireBatchOAuthLock(c.Request.Context(), batchOAuthLockKey("pool", strconv.FormatInt(*t.config.PoolID, 10)), 15*time.Second)
		if lockErr != nil {
			response.Error(c, 409, lockErr.Error())
			return
		}
		defer poolUnlock()
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
	var pendingAdsPowerBinding *service.OpenAIAdsPowerBinding
	if t.usesAdsPower() {
		pendingAdsPowerBinding, err = h.adsPowerLaunchStore.pendingBinding(c.Request.Context(), t.sessionID, t.ownerID)
		if err != nil {
			response.Error(c, http.StatusServiceUnavailable, "AdsPower browser binding is temporarily unavailable")
			return
		}
		if pendingAdsPowerBinding == nil {
			response.Error(c, http.StatusConflict, "AdsPower browser binding has not been verified")
			return
		}
	}
	if existing, err := h.existingBatchOAuthAccount(c.Request.Context(), t.Email); err != nil {
		response.InternalError(c, "Unable to check existing OpenAI accounts")
		return
	} else if existing != nil {
		t.Status, t.Stage, t.Reason, t.AccountID = "completed", "completed", batchOAuthAlreadyExistsReason, existing.ID
		response.Success(c, t)
		return
	}
	if (t.LoginMethod == batchOAuthLoginEmailCode && t.loginEmailCodeEncrypted == "") ||
		(t.LoginMethod != batchOAuthLoginEmailCode && t.loginPasswordEncrypted == "") {
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
	if existing, lookupErr := h.existingBatchOAuthAccount(ctx, t.Email); lookupErr != nil {
		t.Reason = "account_creation_requires_review"
		response.Success(c, t)
		return
	} else if existing != nil {
		t.Status, t.Stage, t.Reason, t.AccountID = "completed", "completed", batchOAuthAlreadyExistsReason, existing.ID
		response.Success(c, t)
		return
	}
	schedulable := true
	credentials := h.openaiOAuthService.BuildAccountCredentials(token)
	credentials[service.OpenAIOAuthReauthorizationEmailCredentialKey] = t.Email
	if t.LoginMethod == batchOAuthLoginEmailCode {
		credentials[service.OpenAIOAuthReauthorizationEmailCodeTokenCredentialKey] = t.loginEmailCodeEncrypted
	} else {
		credentials[service.OpenAIOAuthReauthorizationPasswordCredentialKey] = t.loginPasswordEncrypted
		if t.loginTOTPEncrypted != "" {
			credentials[service.OpenAIOAuthReauthorizationTOTPSecretCredentialKey] = t.loginTOTPEncrypted
		}
	}
	if t.config.PoolID != nil {
		h.batchOAuthStore.poolNamingMu.Lock()
		defer h.batchOAuthStore.poolNamingMu.Unlock()
		generatedName, nameErr := h.nextBatchOAuthPoolAccountName(ctx, *t.config.PoolID, credentials)
		if nameErr != nil {
			t.Reason = "account_creation_requires_review"
			response.Success(c, t)
			return
		}
		t.config.Name = generatedName
	}
	name := strings.TrimSpace(t.config.Name)
	if name == "" {
		name = t.Email
	}
	t.createAttempted = true
	extra := map[string]any{"codex_fingerprint_mode": t.config.FingerprintMode}
	if pendingAdsPowerBinding != nil {
		extra[service.OpenAIAdsPowerBindingExtraKey] = pendingAdsPowerBinding
	}
	account, err := h.adminService.CreateAccount(ctx, &service.CreateAccountInput{Name: name, Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth,
		Credentials: credentials, AllowOpenAIReauthorizationCredentials: true, AllowOpenAIAdsPowerBinding: pendingAdsPowerBinding != nil, PreserveOAuthWorkflowProxy: true, Extra: extra,
		GroupIDs: t.config.GroupIDs, ProxyID: t.config.ProxyID, Concurrency: t.config.Concurrency, Priority: t.config.Priority, SkipDefaultGroupBind: true, Schedulable: &schedulable})
	token = nil
	if errors.Is(err, service.ErrOpenAIOAuthEmailExists) {
		if existing, lookupErr := h.existingBatchOAuthAccount(ctx, t.Email); lookupErr == nil && existing != nil {
			t.Status, t.Stage, t.Reason, t.AccountID = "completed", "completed", batchOAuthAlreadyExistsReason, existing.ID
			response.Success(c, t)
			return
		}
	}
	if err != nil || account == nil || account.ID <= 0 {
		t.Reason = "account_creation_requires_review"
		response.Success(c, t)
		return
	}
	if pendingAdsPowerBinding != nil {
		h.adsPowerLaunchStore.deletePending(ctx, t.sessionID)
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
	passwordCiphertext := a.GetCredential(service.OpenAIOAuthReauthorizationPasswordCredentialKey)
	totpCiphertext := a.GetCredential(service.OpenAIOAuthReauthorizationTOTPSecretCredentialKey)
	emailCodeCiphertext := a.GetCredential(service.OpenAIOAuthReauthorizationEmailCodeTokenCredentialKey)
	credentialsMismatch := !strings.EqualFold(strings.TrimSpace(a.GetCredential("email")), t.Email) ||
		a.GetCredential(service.OpenAIOAuthReauthorizationEmailCredentialKey) != t.Email
	if t.LoginMethod == batchOAuthLoginEmailCode {
		credentialsMismatch = credentialsMismatch || t.loginEmailCodeEncrypted == "" || emailCodeCiphertext != t.loginEmailCodeEncrypted ||
			passwordCiphertext != "" || totpCiphertext != ""
	} else if t.loginPasswordEncrypted != "" {
		credentialsMismatch = credentialsMismatch || passwordCiphertext != t.loginPasswordEncrypted || totpCiphertext != t.loginTOTPEncrypted
	} else if t.loginTOTPConfigured {
		credentialsMismatch = credentialsMismatch || totpCiphertext == ""
	}
	if credentialsMismatch {
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
	if t.usesAdsPower() && service.OpenAIAdsPowerBindingFromAccount(a) == nil {
		return
	}
	if t.config.PoolID != nil {
		poolService, ok := h.adminService.(service.AccountPoolService)
		if !ok {
			t.Reason = "pool_assignment_failed"
			return
		}
		pool, poolErr := poolService.GetAccountPool(ctx, *t.config.PoolID)
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
		PoolID: t.config.PoolID, Concurrency: a.Concurrency, Priority: a.Priority, FingerprintMode: a.GetExtraString("codex_fingerprint_mode"), BrowserMode: t.BrowserMode}
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
	h.cancelBatchOAuthTask(c, batchOAuthModeCreate)
}

func (h *OpenAIOAuthHandler) cancelBatchOAuthTask(c *gin.Context, mode string) {
	t, ok := h.ownedBatchTask(c, mode)
	if !ok {
		return
	}
	defer t.mu.Unlock()
	defer t.markFinishedIfTerminal()
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
		if !t.usesAdsPower() {
			if _, err := t.sidecar(ctx, "cancel", nil); err != nil {
				response.Error(c, 502, "Browser cancellation not confirmed")
				return
			}
		}
	}
	h.openaiOAuthService.RevokeWorkflowSession(t.sessionID)
	t.Status, t.Stage, t.Reason, t.RequiresSMSConfirmation = "canceled", "canceled", "", false
	if mode == batchOAuthModeReauthorization {
		_ = h.persistOpenAIReauthorizationTaskState(c.Request.Context(), t)
	}
	response.Success(c, t)
}

// DeleteBatchOAuthTask removes only a finished workflow record. A completed
// account remains intact and keeps the login material copied during creation.
func (h *OpenAIOAuthHandler) DeleteBatchOAuthTask(c *gin.Context) {
	h.deleteBatchOAuthTask(c, batchOAuthModeCreate)
}

func (h *OpenAIOAuthHandler) deleteBatchOAuthTask(c *gin.Context, mode string) {
	owner, ok := batchOAuthAuth(c)
	if !ok {
		return
	}
	if h.batchOAuthStore == nil {
		response.Error(c, 503, "Batch OAuth unavailable")
		return
	}
	store := h.batchOAuthStore
	store.mu.Lock()
	t := store.tasks[c.Param("task_id")]
	store.mu.Unlock()
	if t == nil || t.ownerID != owner || !t.matchesMode(mode) {
		response.NotFound(c, "Task not found")
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.terminal() {
		response.Error(c, 409, "Only finished tasks can be deleted")
		return
	}

	ctx, cancel := context.WithTimeout(context.WithoutCancel(c.Request.Context()), 15*time.Second)
	defer cancel()
	if t.RequiresSMSConfirmation {
		if err := h.cancelBatchReservation(ctx, t, true); err != nil {
			response.Error(c, 409, err.Error())
			return
		}
	}
	h.revokeTerminalBatchSession(t)
	t.clearLoginCredentials()

	store.mu.Lock()
	if store.tasks[t.ID] == t {
		delete(store.tasks, t.ID)
	}
	store.mu.Unlock()
	response.Success(c, gin.H{"task_id": t.ID, "account_id": t.AccountID})
}

func (h *OpenAIOAuthHandler) RestartBatchOAuthTask(c *gin.Context) {
	h.restartBatchOAuthTask(c, batchOAuthModeCreate)
}

func (h *OpenAIOAuthHandler) restartBatchOAuthTask(c *gin.Context, mode string) {
	t, ok := h.ownedBatchTask(c, mode)
	if !ok {
		return
	}
	defer t.mu.Unlock()
	defer t.markFinishedIfTerminal()
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 16<<10)
	var req struct {
		LoginMethod    *string `json:"login_method"`
		Password       *string `json:"password"`
		TOTPSecret     *string `json:"totp_secret"`
		EmailCodeToken *string `json:"email_code_token"`
		Confirmed      bool    `json:"confirmed"`
		BrowserMode    *string `json:"browser_mode"`
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
	previousUsesAdsPower := t.usesAdsPower()
	method := normalizeBatchOAuthLoginMethod(t.LoginMethod)
	if req.LoginMethod != nil {
		method = normalizeBatchOAuthLoginMethod(*req.LoginMethod)
	}
	nextConfig := t.config
	if req.BrowserMode != nil {
		nextConfig.BrowserMode = normalizeBatchOAuthBrowserMode(*req.BrowserMode)
	}
	password := ""
	if req.Password != nil {
		password = *req.Password
	}
	secret := ""
	if req.TOTPSecret != nil {
		secret = *req.TOTPSecret
	}
	emailCodeToken := ""
	if req.EmailCodeToken != nil {
		emailCodeToken = *req.EmailCodeToken
	}
	if validateBatchLoginMethod(t.Email, method, password, secret, emailCodeToken) != nil {
		response.BadRequest(c, "Valid login credentials required")
		return
	}
	passwordEncrypted, totpEncrypted, emailCodeEncrypted, err := h.encryptBatchLogin(method, password, secret, emailCodeToken)
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
	proxy, err := h.validateBatchConfig(c.Request.Context(), &nextConfig)
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
	if !previousUsesAdsPower {
		if _, err := t.sidecar(ctx, "cancel", nil); err != nil {
			response.Error(c, 502, "Browser cancellation not confirmed")
			return
		}
	}
	h.openaiOAuthService.RevokeWorkflowSession(t.sessionID)
	id, err := newTeamChildBrowserToken()
	if err != nil {
		response.InternalError(c, "Task restart failed")
		return
	}
	t.sidecarID = id
	t.config = nextConfig
	t.authURL = ""
	t.externalCallbackURL = ""
	t.adsPowerLaunchIssued = false
	t.submittedPhone = ""
	t.RestartCount++
	t.RequiresSMSConfirmation = false
	t.FinishedAt = nil
	t.CreatedAt = time.Now().UTC()
	t.LoginMethod = method
	t.loginPasswordEncrypted, t.loginTOTPEncrypted = passwordEncrypted, totpEncrypted
	t.loginEmailCodeEncrypted = emailCodeEncrypted
	t.loginTOTPConfigured = secret != ""
	_ = h.startBatchAttempt(ctx, t, password, secret, emailCodeToken, proxy)
	response.Success(c, t)
}

func (h *OpenAIOAuthHandler) BatchOAuthSMSAction(c *gin.Context) {
	h.batchOAuthSMSAction(c, batchOAuthModeCreate)
}

func (h *OpenAIOAuthHandler) batchOAuthSMSAction(c *gin.Context, mode string) {
	t, ok := h.ownedBatchTask(c, mode)
	if !ok {
		return
	}
	defer t.mu.Unlock()
	defer h.revokeTerminalBatchSession(t)
	if t.usesAdsPower() {
		response.Error(c, http.StatusConflict, "Phone automation is unavailable in the fixed AdsPower browser")
		return
	}
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
