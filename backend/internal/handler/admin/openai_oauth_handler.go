package admin

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/handler/dto"
	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
)

// OpenAIOAuthHandler handles OpenAI OAuth-related operations
type OpenAIOAuthHandler struct {
	openaiOAuthService       *service.OpenAIOAuthService
	adminService             service.AdminService
	secretEncryptor          service.SecretEncryptor
	quotaService             openAIQuotaService
	rateLimitService         openAIAccountStateRecoverer
	settingRepo              service.SettingRepository
	teamMailboxStore         *openAITeamMailboxStore
	teamMailboxShareStore    *openAITeamMailboxShareStore
	teamMailboxShareRegistry *openAITeamMailboxShareRegistry
	teamBrowserStore         *openAITeamBrowserStore
}

// ConfigureTeamChildSecrets attaches the application encryption boundary used
// for Team-child login passwords. Keeping it out of the constructor preserves
// the focused handler tests and makes a missing production wiring fail closed.
func (h *OpenAIOAuthHandler) ConfigureTeamChildSecrets(encryptor service.SecretEncryptor) {
	if h != nil {
		h.secretEncryptor = encryptor
	}
}

// ConfigureTeamChildSharedSettings attaches the database-backed configuration
// store shared by paired execution nodes. Provider credentials remain encrypted
// and are never returned by an admin status endpoint.
func (h *OpenAIOAuthHandler) ConfigureTeamChildSharedSettings(settingRepo service.SettingRepository) {
	if h != nil {
		h.settingRepo = settingRepo
		h.bootstrapTeamMailboxSharedConfig(context.Background())
	}
}

type openAIQuotaService interface {
	QueryUsage(ctx context.Context, accountID int64) (*service.OpenAIQuotaUsage, error)
	CacheResetCreditsSnapshot(ctx context.Context, accountID int64, credits *service.OpenAIRateLimitResetCredits) error
	CachePostResetSnapshot(ctx context.Context, accountID int64, usage *service.OpenAIQuotaUsage) error
	ResetCredit(ctx context.Context, accountID int64) (*service.OpenAIQuotaResetResult, error)
}

type openAIAccountStateRecoverer interface {
	RecoverAccountState(ctx context.Context, accountID int64, options service.AccountRecoveryOptions) (*service.SuccessfulTestRecoveryResult, error)
}

const (
	openAIQuotaResetWarningCacheRefreshFailed    = "reset_credit_cache_refresh_failed"
	openAIQuotaResetWarningAccountRecoveryFailed = "account_state_recovery_failed"
	openAIQuotaResetWarningAccountRefreshFailed  = "account_state_refresh_failed"
)

const (
	publicOpenAIOAuthBindingCookie = "xiass_openai_oauth_binding"
	publicOpenAIOAuthCookiePath    = "/api/v1/tools/openai-oauth"
	publicOpenAIOAuthBodyMaxBytes  = 16 * 1024
)

// openAIQuotaResetPostProcessTimeout bounds the work performed AFTER the
// (non-refundable) reset credit has already been consumed upstream. The whole
// request must stay comfortably inside the panel HTTP client timeout, otherwise
// the browser aborts a mutation that already succeeded and the operator retries
// it — spending a second credit.
const openAIQuotaResetPostProcessTimeout = 8 * time.Second

type openAIQuotaResetResponse struct {
	service.OpenAIQuotaResetResult
	Quota                 *service.OpenAIQuotaUsage `json:"quota,omitempty"`
	Account               *dto.Account              `json:"account,omitempty"`
	CacheRefreshed        bool                      `json:"cache_refreshed"`
	AccountStateRecovered bool                      `json:"account_state_recovered"`
	WarningCode           string                    `json:"warning_code,omitempty"`
}

// openAIQuotaRefreshResponse is the reset-credit-persisting variant of the quota
// query. The usage payload is embedded so the shape stays identical to the plain
// query; cache_persisted reports whether the snapshot write succeeded, because a
// failed display-cache write must never discard a successful upstream read.
type openAIQuotaRefreshResponse struct {
	service.OpenAIQuotaUsage
	CachePersisted bool `json:"cache_persisted"`
}

// openAIQuotaResetPostProcessContext detaches the post-reset bookkeeping from the
// client connection. The credit is already spent at that point, so account-state
// recovery must complete even if the operator closes the tab (mirrors
// systemUpdateContext, added for the same reason in #4504).
func openAIQuotaResetPostProcessContext(ctx context.Context) (context.Context, context.CancelFunc) {
	base := context.Background()
	if ctx != nil {
		base = context.WithoutCancel(ctx)
	}
	return context.WithTimeout(base, openAIQuotaResetPostProcessTimeout)
}

func oauthPlatformFromPath(c *gin.Context) string {
	return service.PlatformOpenAI
}

// NewOpenAIOAuthHandler creates a new OpenAI OAuth handler
func NewOpenAIOAuthHandler(
	openaiOAuthService *service.OpenAIOAuthService,
	adminService service.AdminService,
	quotaService *service.OpenAIQuotaService,
	rateLimitService *service.RateLimitService,
) *OpenAIOAuthHandler {
	h := &OpenAIOAuthHandler{
		openaiOAuthService:       openaiOAuthService,
		adminService:             adminService,
		teamMailboxStore:         newOpenAITeamMailboxStore(),
		teamMailboxShareStore:    newOpenAITeamMailboxShareStore(),
		teamMailboxShareRegistry: newOpenAITeamMailboxShareRegistry(),
		teamBrowserStore:         newOpenAITeamBrowserStore(),
	}
	// Assign through explicit nil checks: storing a nil *Service in an interface
	// field yields a non-nil interface, which would silently defeat the
	// `== nil` capability guards below and panic instead of returning 400.
	if quotaService != nil {
		h.quotaService = quotaService
	}
	if rateLimitService != nil {
		h.rateLimitService = rateLimitService
	}
	return h
}

// ConfigurePublicToolSessionStore attaches the Redis-backed transaction store
// used by the unauthenticated 401 re-authorization helper.
func (h *OpenAIOAuthHandler) ConfigurePublicToolSessionStore(store service.PublicOpenAIOAuthSessionStore) {
	if h != nil && h.openaiOAuthService != nil {
		h.openaiOAuthService.SetPublicSessionStore(store)
	}
}

// OpenAIGenerateAuthURLRequest represents the request for generating OpenAI auth URL
type OpenAIGenerateAuthURLRequest struct {
	ProxyID     *int64 `json:"proxy_id"`
	RedirectURI string `json:"redirect_uri"`
}

// GenerateAuthURL generates OpenAI OAuth authorization URL
// POST /api/v1/admin/openai/generate-auth-url
func (h *OpenAIOAuthHandler) GenerateAuthURL(c *gin.Context) {
	var req OpenAIGenerateAuthURLRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		// Allow empty body
		req = OpenAIGenerateAuthURLRequest{}
	}

	result, err := h.openaiOAuthService.GenerateAuthURL(
		c.Request.Context(),
		req.ProxyID,
		req.RedirectURI,
		oauthPlatformFromPath(c),
	)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	response.Success(c, result)
}

// OpenAIExchangeCodeRequest represents the request for exchanging OpenAI auth code
type OpenAIExchangeCodeRequest struct {
	SessionID   string `json:"session_id" binding:"required"`
	Code        string `json:"code" binding:"required"`
	State       string `json:"state" binding:"required"`
	RedirectURI string `json:"redirect_uri"`
	ProxyID     *int64 `json:"proxy_id"`
}

// ExchangeCode exchanges OpenAI authorization code for tokens
// POST /api/v1/admin/openai/exchange-code
func (h *OpenAIOAuthHandler) ExchangeCode(c *gin.Context) {
	var req OpenAIExchangeCodeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}

	tokenInfo, err := h.openaiOAuthService.ExchangeCode(c.Request.Context(), &service.OpenAIExchangeCodeInput{
		SessionID:   req.SessionID,
		Code:        req.Code,
		State:       req.State,
		RedirectURI: req.RedirectURI,
		ProxyID:     req.ProxyID,
	})
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	response.Success(c, tokenInfo)
}

// GeneratePublicToolAuthURL starts the free public OpenAI re-authorization
// flow. Public callers cannot select an administrator proxy or redirect URI.
func (h *OpenAIOAuthHandler) GeneratePublicToolAuthURL(c *gin.Context) {
	binding, err := publicOpenAIOAuthBrowserBinding(c)
	if err != nil {
		response.InternalError(c, "failed to create browser binding")
		return
	}
	setPublicOpenAIOAuthCookie(c, binding)
	setPublicOpenAIOAuthNoStoreHeaders(c)

	result, err := h.openaiOAuthService.GeneratePublicAuthURL(c.Request.Context(), hashPublicOpenAIOAuthBinding(binding))
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	response.Success(c, result)
}

// ExchangePublicToolCode completes the public OpenAI re-authorization flow.
// The service deliberately skips account enrichment, database writes and
// privacy-setting mutations for this endpoint.
func (h *OpenAIOAuthHandler) ExchangePublicToolCode(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, publicOpenAIOAuthBodyMaxBytes)
	var req struct {
		SessionID string `json:"session_id" binding:"required,len=32"`
		Code      string `json:"code" binding:"required,max=8192"`
		State     string `json:"state" binding:"required,len=64"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	binding, err := c.Cookie(publicOpenAIOAuthBindingCookie)
	if err != nil || strings.TrimSpace(binding) == "" {
		response.BadRequest(c, "Authorization was started in a different browser or has expired")
		return
	}
	setPublicOpenAIOAuthNoStoreHeaders(c)

	exchangeContext, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()
	tokenInfo, err := h.openaiOAuthService.ExchangeCodeForPublicTool(exchangeContext, &service.OpenAIExchangeCodeInput{
		SessionID:          strings.TrimSpace(req.SessionID),
		Code:               strings.TrimSpace(req.Code),
		State:              strings.TrimSpace(req.State),
		BrowserBindingHash: hashPublicOpenAIOAuthBinding(binding),
	})
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	response.Success(c, tokenInfo)
}

func publicOpenAIOAuthBrowserBinding(c *gin.Context) (string, error) {
	if existing, err := c.Cookie(publicOpenAIOAuthBindingCookie); err == nil {
		trimmed := strings.TrimSpace(existing)
		if len(trimmed) >= 32 && len(trimmed) <= 128 {
			return trimmed, nil
		}
	}
	randomBytes, err := openai.GenerateRandomBytes(32)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(randomBytes), nil
}

func hashPublicOpenAIOAuthBinding(binding string) string {
	digest := sha256.Sum256([]byte(binding))
	return hex.EncodeToString(digest[:])
}

func setPublicOpenAIOAuthCookie(c *gin.Context, binding string) {
	secure := c.Request.TLS != nil || strings.EqualFold(strings.TrimSpace(c.GetHeader("X-Forwarded-Proto")), "https")
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     publicOpenAIOAuthBindingCookie,
		Value:    binding,
		Path:     publicOpenAIOAuthCookiePath,
		MaxAge:   int(openai.SessionTTL / time.Second),
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteStrictMode,
	})
}

func setPublicOpenAIOAuthNoStoreHeaders(c *gin.Context) {
	c.Header("Cache-Control", "private, no-store, max-age=0")
	c.Header("Pragma", "no-cache")
	c.Header("Expires", "0")
	c.Header("Referrer-Policy", "no-referrer")
}

// OpenAIRefreshTokenRequest represents the request for refreshing OpenAI token
type OpenAIRefreshTokenRequest struct {
	RefreshToken string `json:"refresh_token"`
	RT           string `json:"rt"`
	ClientID     string `json:"client_id"`
	ProxyID      *int64 `json:"proxy_id"`
}

type OpenAICodexPATCreateRequest struct {
	AccessToken             string         `json:"access_token" binding:"required"`
	Name                    string         `json:"name"`
	Notes                   *string        `json:"notes"`
	GroupIDs                []int64        `json:"group_ids"`
	ProxyID                 *int64         `json:"proxy_id"`
	Concurrency             *int           `json:"concurrency"`
	Priority                *int           `json:"priority"`
	RateMultiplier          *float64       `json:"rate_multiplier"`
	LoadFactor              *int           `json:"load_factor"`
	ExpiresAt               *int64         `json:"expires_at"`
	AutoPauseOnExpired      *bool          `json:"auto_pause_on_expired"`
	CredentialExtras        map[string]any `json:"credential_extras"`
	Extra                   map[string]any `json:"extra"`
	SkipDefaultGroupBind    *bool          `json:"skip_default_group_bind"`
	ConfirmMixedChannelRisk *bool          `json:"confirm_mixed_channel_risk"`
}

// RefreshToken refreshes an OpenAI OAuth token
// POST /api/v1/admin/openai/refresh-token
func (h *OpenAIOAuthHandler) RefreshToken(c *gin.Context) {
	var req OpenAIRefreshTokenRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	refreshToken := strings.TrimSpace(req.RefreshToken)
	if refreshToken == "" {
		refreshToken = strings.TrimSpace(req.RT)
	}
	if refreshToken == "" {
		response.BadRequest(c, "refresh_token is required")
		return
	}

	var proxyURL string
	if req.ProxyID != nil {
		proxy, err := h.adminService.GetProxy(c.Request.Context(), *req.ProxyID)
		if err == nil && proxy != nil {
			proxyURL = proxy.URL()
		}
	}

	// 未指定 client_id 时，根据请求路径平台自动设置默认值，避免 repository 层盲猜
	clientID := strings.TrimSpace(req.ClientID)
	if clientID == "" {
		platform := oauthPlatformFromPath(c)
		clientID, _ = openai.OAuthClientConfigByPlatform(platform)
	}

	tokenInfo, err := h.openaiOAuthService.RefreshTokenWithClientID(c.Request.Context(), refreshToken, proxyURL, clientID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	response.Success(c, tokenInfo)
}

// RefreshAccountToken refreshes token for a specific OpenAI account
// POST /api/v1/admin/openai/accounts/:id/refresh
func (h *OpenAIOAuthHandler) RefreshAccountToken(c *gin.Context) {
	accountID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid account ID")
		return
	}
	if err := ensureAdminAccountManagementAccess(c.Request.Context(), h.adminService, accountID); err != nil {
		response.ErrorFrom(c, err)
		return
	}

	// Get account
	account, err := h.adminService.GetAccount(c.Request.Context(), accountID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	platform := oauthPlatformFromPath(c)
	if account.Platform != platform {
		response.BadRequest(c, "Account platform does not match OAuth endpoint")
		return
	}

	// Only refresh OAuth-based accounts
	if !account.IsOAuth() {
		response.BadRequest(c, "Cannot refresh non-OAuth account credentials")
		return
	}

	// spark 影子账号凭据透传母账号、自身恒空,刷新无意义;在调用上游前早拒,避免先打上游
	// 再被凭据写守卫拦下的无谓副作用(外审第6轮)。
	if account.IsCredentialShadow() {
		response.BadRequest(c, "Cannot refresh spark shadow account; its credentials are managed by the parent account")
		return
	}

	// Use OpenAI OAuth service to refresh token
	tokenInfo, err := h.openaiOAuthService.RefreshAccountToken(c.Request.Context(), account)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	// Build new credentials from token info
	newCredentials := h.openaiOAuthService.BuildAccountCredentials(tokenInfo)
	teamChildEmail, isTeamChild := teamChildAccountWorkflowEmail(account)
	if isTeamChild {
		// Token refresh cannot change a completed Team-child's mailbox identity.
		// Keep later account responses aligned with the verified mailbox/password
		// record created by the workflow.
		newCredentials["email"] = teamChildEmail
	}

	// Preserve non-token settings from existing credentials
	for k, v := range account.Credentials {
		if _, exists := newCredentials[k]; !exists {
			newCredentials[k] = v
		}
	}
	newCredentials = service.NormalizeOpenAIPersonalAccessTokenCredentials(account, tokenInfo, newCredentials)

	updateInput := &service.UpdateAccountInput{Credentials: newCredentials}
	if isTeamChild {
		updateInput.Name = teamChildEmail
	}
	updatedAccount, err := h.adminService.UpdateAccount(c.Request.Context(), accountID, updateInput)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	response.Success(c, dto.AccountFromService(updatedAccount))
}

// CreateAccountFromOAuth creates a new OpenAI OAuth account from token info
// POST /api/v1/admin/openai/create-from-oauth
func (h *OpenAIOAuthHandler) CreateAccountFromOAuth(c *gin.Context) {
	var req struct {
		SessionID   string  `json:"session_id" binding:"required"`
		Code        string  `json:"code" binding:"required"`
		State       string  `json:"state" binding:"required"`
		RedirectURI string  `json:"redirect_uri"`
		ProxyID     *int64  `json:"proxy_id"`
		Name        string  `json:"name"`
		Concurrency int     `json:"concurrency"`
		Priority    int     `json:"priority"`
		GroupIDs    []int64 `json:"group_ids"`
		// Team imports own their group binding. When the selected list is empty,
		// it means intentionally ungrouped rather than "bind a platform default".
		SkipDefaultGroupBind *bool `json:"skip_default_group_bind"`
		// New Team imports are usable immediately unless an explicit caller opts
		// out. Keep this pointer so other OAuth creation paths retain their
		// established default behavior.
		Schedulable *bool  `json:"schedulable"`
		TeamChild   bool   `json:"team_child"`
		WorkflowID  string `json:"workflow_id"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}

	var teamSecret *openAITeamChildWorkflowSecret
	var encryptedTeamPassword string
	if req.TeamChild {
		req.WorkflowID = strings.TrimSpace(req.WorkflowID)
		if !validTeamChildWorkflowID(req.WorkflowID) {
			response.BadRequest(c, "Team 子号工作流 ID 无效")
			return
		}
		var err error
		teamSecret, err = h.fetchTeamChildWorkflowSecret(c.Request.Context(), req.WorkflowID)
		if err != nil {
			response.Error(c, http.StatusConflict, "无法读取当前 Team 子号工作流登录信息，请确认自动化工作流仍有效")
			return
		}
		if teamSecret.Password != "" {
			if h.secretEncryptor == nil {
				response.InternalError(c, "Team 子号密码加密服务不可用")
				return
			}
			encryptedTeamPassword, err = h.secretEncryptor.Encrypt(teamSecret.Password)
			if err != nil {
				response.InternalError(c, "Team 子号密码加密失败")
				return
			}
		}
	}

	// Exchange code for tokens only after the workflow secret has been secured.
	tokenInfo, err := h.openaiOAuthService.ExchangeCode(c.Request.Context(), &service.OpenAIExchangeCodeInput{
		SessionID:   req.SessionID,
		Code:        req.Code,
		State:       req.State,
		RedirectURI: req.RedirectURI,
		ProxyID:     req.ProxyID,
	})
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	// Build credentials from token info
	credentials := h.openaiOAuthService.BuildAccountCredentials(tokenInfo)
	if teamSecret != nil {
		loginEmail := normalizeTeamChildWorkflowEmail(tokenInfo.Email)
		workflowEmail := normalizeTeamChildWorkflowEmail(teamSecret.Email)
		if !validTeamChildWorkflowEmail(loginEmail) || !strings.EqualFold(loginEmail, workflowEmail) {
			response.BadRequest(c, "OAuth 登录邮箱与当前 Team 子号工作流邮箱不一致")
			return
		}
		teamSecret.Email = workflowEmail
		credentials["email"] = workflowEmail
		if encryptedTeamPassword != "" {
			credentials[service.OpenAITeamChildPasswordCredentialKey] = encryptedTeamPassword
		}
	}

	platform := oauthPlatformFromPath(c)

	// Use email as default name if not provided
	name := strings.TrimSpace(req.Name)
	if teamSecret != nil {
		// The workflow mailbox is already verified against the OAuth subject
		// above. Use it as the visible account name too, so account management,
		// Team history, password recovery, and mailbox selection cannot drift.
		name = teamSecret.Email
	} else if name == "" && tokenInfo.Email != "" {
		name = tokenInfo.Email
	}
	if name == "" {
		name = "OpenAI OAuth Account"
	}

	var extra map[string]any
	if req.TeamChild {
		extra = map[string]any{
			service.OpenAITeamChildExtraKey:      true,
			service.OpenAITeamChildEmailExtraKey: teamSecret.Email,
		}
	}
	skipDefaultGroupBind := req.TeamChild
	if req.SkipDefaultGroupBind != nil {
		skipDefaultGroupBind = *req.SkipDefaultGroupBind
	}
	schedulable := true
	if req.Schedulable != nil {
		schedulable = *req.Schedulable
	}

	// Create account
	account, err := h.adminService.CreateAccount(c.Request.Context(), &service.CreateAccountInput{
		Name:                                  name,
		Platform:                              platform,
		Type:                                  "oauth",
		Credentials:                           credentials,
		Extra:                                 extra,
		ProxyID:                               req.ProxyID,
		Concurrency:                           req.Concurrency,
		Priority:                              req.Priority,
		GroupIDs:                              req.GroupIDs,
		Schedulable:                           &schedulable,
		AllowOpenAIReauthorizationCredentials: teamSecret != nil,
		SkipDefaultGroupBind:                  skipDefaultGroupBind,
	})
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	if teamSecret != nil {
		// A link can be generated while the mailbox is being used for OAuth.
		// Once import succeeds, bind that durable capability to the new account
		// without rotating its token or disturbing the administrator workflow.
		if bindErr := h.ensureTeamMailboxShareRegistry().attachAccount(teamSecret.Email, account.ID); bindErr != nil {
			slog.Warn("team_mailbox_share_account_bind_failed", "account_id", account.ID, "email", teamSecret.Email, "error", bindErr)
		}
	}

	response.Success(c, dto.AccountFromService(account))
}

// CreateAccountFromCodexPAT creates an OpenAI OAuth account from a Codex at-* personal access token.
// POST /api/v1/admin/openai/create-from-codex-pat
func (h *OpenAIOAuthHandler) CreateAccountFromCodexPAT(c *gin.Context) {
	var req OpenAICodexPATCreateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	if err := service.ValidateOpenAILongContextBillingExtra(service.PlatformOpenAI, req.Extra); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	if req.Concurrency != nil && *req.Concurrency < 0 {
		response.BadRequest(c, "concurrency must be >= 0")
		return
	}
	if req.Priority != nil && *req.Priority < 0 {
		response.BadRequest(c, "priority must be >= 0")
		return
	}
	if req.RateMultiplier != nil && *req.RateMultiplier < 0 {
		response.BadRequest(c, "rate_multiplier must be >= 0")
		return
	}
	if req.LoadFactor != nil && *req.LoadFactor > 10000 {
		response.BadRequest(c, "load_factor must be <= 10000")
		return
	}

	var proxyURL string
	if req.ProxyID != nil {
		proxy, err := h.adminService.GetProxy(c.Request.Context(), *req.ProxyID)
		if err != nil {
			response.ErrorFrom(c, err)
			return
		}
		if proxy != nil {
			proxyURL = proxy.URL()
		}
	}

	tokenInfo, err := h.openaiOAuthService.ValidateCodexPersonalAccessToken(c.Request.Context(), req.AccessToken, proxyURL)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	credentials := mergeCodexImportMap(
		h.openaiOAuthService.BuildAccountCredentials(tokenInfo),
		sanitizeCodexImportCredentialExtras(req.CredentialExtras),
	)
	extra := mergeCodexImportMap(req.Extra, map[string]any{
		"import_source":       "codex_personal_access_token",
		"auth_provider":       "codex_personal_access_token",
		"imported_at":         time.Now().UTC().Format(time.RFC3339),
		"access_token_sha256": codexTokenFingerprint(req.AccessToken),
	})

	concurrency := 3
	if req.Concurrency != nil {
		concurrency = *req.Concurrency
	}
	priority := 50
	if req.Priority != nil {
		priority = *req.Priority
	}
	skipDefaultGroupBind := false
	if req.SkipDefaultGroupBind != nil {
		skipDefaultGroupBind = *req.SkipDefaultGroupBind
	}

	account, err := h.adminService.CreateAccount(c.Request.Context(), &service.CreateAccountInput{
		Name:                  buildOpenAICodexPATAccountName(req.Name, tokenInfo),
		Notes:                 req.Notes,
		Platform:              service.PlatformOpenAI,
		Type:                  service.AccountTypeOAuth,
		Credentials:           credentials,
		Extra:                 extra,
		ProxyID:               req.ProxyID,
		Concurrency:           concurrency,
		Priority:              priority,
		RateMultiplier:        req.RateMultiplier,
		LoadFactor:            req.LoadFactor,
		GroupIDs:              req.GroupIDs,
		ExpiresAt:             req.ExpiresAt,
		AutoPauseOnExpired:    req.AutoPauseOnExpired,
		SkipDefaultGroupBind:  skipDefaultGroupBind,
		SkipMixedChannelCheck: req.ConfirmMixedChannelRisk != nil && *req.ConfirmMixedChannelRisk,
	})
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	response.Success(c, dto.AccountFromService(account))
}

func buildOpenAICodexPATAccountName(name string, tokenInfo *service.OpenAITokenInfo) string {
	name = strings.TrimSpace(name)
	if name != "" {
		return name
	}
	if tokenInfo != nil {
		for _, candidate := range []string{tokenInfo.Email, tokenInfo.ChatGPTAccountID, tokenInfo.ChatGPTUserID} {
			if candidate = strings.TrimSpace(candidate); candidate != "" {
				return candidate
			}
		}
	}
	return "Codex PAT Account"
}

// QueryQuota queries the rate-limit / quota usage for an OpenAI account.
// GET /api/v1/admin/openai/accounts/:id/quota
func (h *OpenAIOAuthHandler) QueryQuota(c *gin.Context) {
	accountID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid account ID")
		return
	}
	if h.quotaService == nil {
		response.BadRequest(c, "openai quota service is not enabled")
		return
	}

	usage, err := h.quotaService.QueryUsage(c.Request.Context(), accountID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, usage)
}

// RefreshQuota queries the rate-limit / quota usage AND persists the reset-credit
// snapshot so the card can be rehydrated without an upstream round-trip.
// POST /api/v1/admin/openai/accounts/:id/quota/refresh
//
// It is a POST (not a GET with a side-effect flag) because it writes account
// state: the audit middleware only records mutating verbs, so a persisting GET
// would mutate the database without an audit trail.
func (h *OpenAIOAuthHandler) RefreshQuota(c *gin.Context) {
	accountID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid account ID")
		return
	}
	if err := ensureAdminAccountManagementAccess(c.Request.Context(), h.adminService, accountID); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	if h.quotaService == nil {
		response.BadRequest(c, "openai quota service is not enabled")
		return
	}

	usage, err := h.quotaService.QueryUsage(c.Request.Context(), accountID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	if usage == nil {
		response.Error(c, http.StatusInternalServerError, "openai quota query returned an empty result")
		return
	}

	refreshResponse := openAIQuotaRefreshResponse{OpenAIQuotaUsage: *usage}
	// A failed snapshot write leaves the previous cache intact — report it as a
	// partial success instead of discarding the usage payload we just fetched,
	// which would leave the card without a credit count at all.
	if err := h.quotaService.CacheResetCreditsSnapshot(c.Request.Context(), accountID, usage.RateLimitResetCredits); err != nil {
		slog.Warn("openai_quota_reset_credit_cache_persist_failed", "account_id", accountID, "error", err)
		response.Success(c, refreshResponse)
		return
	}
	refreshResponse.CachePersisted = true
	response.Success(c, refreshResponse)
}

// CreateShadowRequest is the request body for CreateShadow.
type CreateShadowRequest struct {
	Name        string  `json:"name"`
	Priority    int     `json:"priority"`
	Concurrency int     `json:"concurrency"`
	GroupIDs    []int64 `json:"group_ids"`
}

// CreateShadow creates a spark-dimension shadow account for a parent OpenAI OAuth account.
// POST /api/v1/admin/accounts/:id/shadow
func (h *OpenAIOAuthHandler) CreateShadow(c *gin.Context) {
	parentID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid account ID")
		return
	}
	if err := ensureAdminAccountManagementAccess(c.Request.Context(), h.adminService, parentID); err != nil {
		response.ErrorFrom(c, err)
		return
	}

	var req CreateShadowRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}

	shadow, err := h.adminService.CreateShadow(c.Request.Context(), parentID, service.ShadowOptions{
		Name:        req.Name,
		Priority:    req.Priority,
		Concurrency: req.Concurrency,
		GroupIDs:    req.GroupIDs,
	})
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	response.Success(c, dto.AccountFromServiceShallow(shadow))
}

// ResetQuota consumes one rate-limit reset credit for an OpenAI account.
// POST /api/v1/admin/openai/accounts/:id/reset-quota
func (h *OpenAIOAuthHandler) ResetQuota(c *gin.Context) {
	accountID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid account ID")
		return
	}
	if err := ensureAdminAccountManagementAccess(c.Request.Context(), h.adminService, accountID); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	if h.quotaService == nil {
		response.BadRequest(c, "openai quota service is not enabled")
		return
	}
	result, err := h.quotaService.ResetCredit(c.Request.Context(), accountID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	if result == nil {
		response.Error(c, http.StatusInternalServerError, "openai quota reset returned an empty result")
		return
	}

	resetResponse := openAIQuotaResetResponse{OpenAIQuotaResetResult: *result}
	postCtx, cancelPost := openAIQuotaResetPostProcessContext(c.Request.Context())
	defer cancelPost()

	if result.PostResetQuota != nil {
		// Recovery and the identity-bound refresh already completed under the
		// shared manual/automatic lock. Do not mutate the account a second time.
		resetResponse.AccountStateRecovered = true
		resetResponse.CacheRefreshed = true
		resetResponse.Quota = result.PostResetQuota
		account, err := h.adminService.GetAccount(postCtx, accountID)
		if err != nil {
			resetResponse.WarningCode = openAIQuotaResetWarningAccountRefreshFailed
		} else {
			resetResponse.Account = dto.AccountFromService(account)
		}
		response.Success(c, resetResponse)
		return
	}

	// Step 1 — unblocking the account is the whole point of consuming a credit
	// (#3672 / #3740), so it runs FIRST and is never gated on the display cache.
	// Recovery is DB-only and leaves the manual `schedulable` switch untouched.
	if h.rateLimitService == nil {
		resetResponse.WarningCode = openAIQuotaResetWarningAccountRecoveryFailed
		response.Success(c, resetResponse)
		return
	}
	if _, err := h.rateLimitService.RecoverAccountState(postCtx, accountID, service.AccountRecoveryOptions{
		InvalidateToken: true,
		ForceCleanup:    true,
	}); err != nil {
		// Recovery failures are almost always storage-level; the remaining steps
		// share that dependency, so stop here instead of compounding the failure.
		slog.Warn("openai_quota_reset_account_recovery_failed", "account_id", accountID, "error", err)
		resetResponse.WarningCode = openAIQuotaResetWarningAccountRecoveryFailed
		response.Success(c, resetResponse)
		return
	}
	resetResponse.AccountStateRecovered = true

	// Step 2 — refresh the reset-credit display cache. A failure here is reported
	// but must not hide the recovered account row produced by step 3.
	usage, usageErr := h.quotaService.QueryUsage(postCtx, accountID)
	switch {
	case usageErr != nil || usage == nil:
		slog.Warn("openai_quota_reset_cache_refresh_failed", "account_id", accountID, "error", usageErr)
		resetResponse.WarningCode = openAIQuotaResetWarningCacheRefreshFailed
	default:
		if err := h.quotaService.CachePostResetSnapshot(postCtx, accountID, usage); err != nil {
			slog.Warn("openai_quota_reset_cache_refresh_failed", "account_id", accountID, "error", err)
			resetResponse.WarningCode = openAIQuotaResetWarningCacheRefreshFailed
		} else {
			resetResponse.Quota = usage
			resetResponse.CacheRefreshed = true
		}
	}

	// Step 3 — hand back the post-recovery account row so the list drops the
	// stale rate-limit badge without waiting for the next poll.
	account, err := h.adminService.GetAccount(postCtx, accountID)
	if err != nil {
		slog.Warn("openai_quota_reset_account_refresh_failed", "account_id", accountID, "error", err)
		if resetResponse.WarningCode == "" {
			resetResponse.WarningCode = openAIQuotaResetWarningAccountRefreshFailed
		}
		response.Success(c, resetResponse)
		return
	}
	resetResponse.Account = dto.AccountFromService(account)
	response.Success(c, resetResponse)
}
