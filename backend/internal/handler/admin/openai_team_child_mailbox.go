package admin

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	stdhtml "html"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/redisclient"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
)

// The team-child mailbox helpers deliberately keep provider credentials and the
// short-lived mailbox JWT on the server. The browser only receives the mailbox
// address and a one-time local session identifier.
const (
	teamMailboxSessionTTL      = 20 * time.Minute
	teamMailboxKnownTTL        = 180 * 24 * time.Hour
	teamMailboxKnownLimit      = int64(50)
	teamMailboxBodyLimit       = 512 * 1024
	teamMailboxConfigBodyLimit = 256 * 1024
	teamMailboxRedisKeyPrefix  = "xiass:team-child:mailbox:"
	teamMailboxActiveKeyPrefix = "xiass:team-child:mailbox:active:"
	teamMailboxKnownKeyPrefix  = "xiass:team-child:mailbox:known:"
	teamMailboxLegacyScanLimit = 128
	teamMailboxSequenceKey     = teamMailboxRedisKeyPrefix + "address-sequence"
	teamMailboxSequenceStart   = int64(1000)
	teamMailboxSequenceFileEnv = "TEAM_CHILD_MAIL_SEQUENCE_FILE"
	teamMailboxSequenceFile    = "/app/data/team-child-mail-sequence"
	teamMailboxAddressPrefix   = "team"
	// The mailbox Worker blocks non-browser client signatures with Cloudflare
	// error 1010. Keep this on the server-side provider client only; it is not
	// exposed to the browser or copied into user-facing request metadata.
	teamMailboxProviderUserAgent  = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"
	teamMailboxSharedConfigPrefix = "v1:"
	teamMailboxSharedBootstrapTTL = 2 * time.Second
)

type openAITeamMailboxStore struct {
	mu            sync.Mutex
	sequenceMu    sync.Mutex
	sessions      map[string]openAITeamMailboxSession
	activeByAdmin map[int64]string
	knownByAdmin  map[int64]map[string]time.Time
	client        *http.Client
	redis         *redisclient.Client
	now           func() time.Time
}

type openAITeamMailboxSession struct {
	adminUserID int64
	email       string
	token       string
	config      teamMailboxProviderConfig
	expiresAt   time.Time
}

type persistedTeamMailboxConfig struct {
	BaseURL      string `json:"base_url"`
	CreatePath   string `json:"create_path"`
	MessagesPath string `json:"messages_path"`
	Domain       string `json:"domain"`
	APIKey       string `json:"api_key"`
	AuthMode     string `json:"auth_mode"`
	CustomAuth   string `json:"custom_auth"`
}

type persistedTeamMailboxSession struct {
	AdminUserID int64                      `json:"admin_user_id"`
	Email       string                     `json:"email"`
	Token       string                     `json:"token"`
	Config      persistedTeamMailboxConfig `json:"config"`
	ExpiresAt   time.Time                  `json:"expires_at"`
}

type teamMailboxProviderConfig struct {
	baseURL      *url.URL
	createPath   string
	messagesPath string
	domain       string
	apiKey       string
	authMode     string
	customAuth   string
}

type openAITeamMailboxCreateResponse struct {
	SessionID string `json:"session_id"`
	Email     string `json:"email"`
	ExpiresAt string `json:"expires_at"`
}

type openAITeamMailboxCodeResponse struct {
	Status    string `json:"status"`
	Code      string `json:"code,omitempty"`
	ExpiresAt string `json:"expires_at"`
}

type openAITeamMailboxListResponse struct {
	Emails []string `json:"emails"`
}

type openAITeamMailboxSelectRequest struct {
	Email string `json:"email" binding:"required"`
}

var (
	teamMailboxAddressRE            = regexp.MustCompile(`(?i)[a-z0-9._%+\-]+@[a-z0-9.\-]+\.[a-z]{2,}`)
	teamMailboxSemanticCodeRE       = regexp.MustCompile(`(?is)(?:verification|verify|confirmation|one[\s-]*time|security\s+code|login\s+code|验证码|验证(?:码)?)[^0-9]{0,80}([0-9](?:[\s-]?[0-9]){3,7})`)
	teamMailboxCodeLabelRE          = regexp.MustCompile(`(?is)(?:\bcode\b|验证码|验证(?:码)?)[^0-9]{0,32}([0-9](?:[\s-]?[0-9]){3,7})`)
	teamMailboxReverseCodeRE        = regexp.MustCompile(`(?is)\b([0-9](?:[\s-]?[0-9]){3,7})\b[^\n]{0,80}(?:verification|verify|confirmation|security|login|验证码|验证(?:码)?)`)
	teamMailboxStandaloneSixDigitRE = regexp.MustCompile(`(?is)(?:^|[^0-9])([0-9](?:[\s-]?[0-9]){5})(?:$|[^0-9])`)
	teamMailboxHTMLNoiseRE          = regexp.MustCompile(`(?is)<(?:style|script)\b[^>]*>.*?</(?:style|script)>|<!--.*?-->|<[^>]+>`)
)

func newOpenAITeamMailboxStore() *openAITeamMailboxStore {
	return &openAITeamMailboxStore{
		sessions:      make(map[string]openAITeamMailboxSession),
		activeByAdmin: make(map[int64]string),
		knownByAdmin:  make(map[int64]map[string]time.Time),
		client:        &http.Client{Timeout: 12 * time.Second},
		now:           time.Now,
	}
}

func (s *openAITeamMailboxStore) configureRedis(redisClient *redisclient.Client) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.redis = redisClient
	s.mu.Unlock()
}

// TeamChildMailboxStatus reports whether the server-side mailbox provider has
// been configured. It intentionally never returns a provider URL or credential.
// GET /api/v1/admin/openai/team-child/mailbox-status
func (h *OpenAIOAuthHandler) TeamChildMailboxStatus(c *gin.Context) {
	if !requireTeamChildAdminSession(c) {
		return
	}
	_, mailboxErr := h.loadTeamMailboxProviderConfig(c)
	_, browserErr := loadTeamChildBrowserConfig()
	response.Success(c, gin.H{
		"configured":         mailboxErr == nil,
		"browser_configured": browserErr == nil,
	})
}

// ImportTeamChildMailboxConfig accepts one legacy registration config.json or
// a XIASS TEAM_CHILD_MAIL_*.env fragment. The normalized fragment is stored in
// the persistent data directory and read on every mailbox request; secrets
// are never included in the response.
// POST /api/v1/admin/openai/team-child/mailbox-config
func (h *OpenAIOAuthHandler) ImportTeamChildMailboxConfig(c *gin.Context) {
	if !requireTeamChildAdminSession(c) {
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, teamMailboxConfigBodyLimit)
	file, header, err := c.Request.FormFile("file")
	if err != nil || file == nil {
		response.BadRequest(c, "请上传 Cloudflare config.json 或 XIASS 邮箱配置文件")
		return
	}
	defer func() { _ = file.Close() }()
	body, err := io.ReadAll(io.LimitReader(file, teamMailboxConfigBodyLimit+1))
	if err != nil || len(body) > teamMailboxConfigBodyLimit {
		response.BadRequest(c, "配置文件过大，最大支持 256 KB")
		return
	}
	values, err := parseTeamMailboxConfigImport(body, header.Filename)
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	config, err := validateTeamMailboxProviderConfig(values)
	if err != nil {
		response.BadRequest(c, "邮箱配置无效："+err.Error())
		return
	}
	shared, err := h.persistTeamMailboxSharedConfig(c.Request.Context(), values)
	if err != nil {
		response.InternalError(c, "无法保存集群共享邮箱配置")
		return
	}
	localErr := writeTeamMailboxConfigFile(values)
	if localErr != nil && !shared {
		response.InternalError(c, "无法保存邮箱配置，请检查服务器数据目录权限")
		return
	}
	// The public mailbox page caches only a short-lived provider session. Clear
	// that cache after a configuration import so its next poll uses the new
	// provider configuration without touching an administrator's active session.
	h.ensureTeamMailboxShareStore().reset()
	response.Success(c, gin.H{
		"configured":        true,
		"cluster_shared":    shared,
		"auth_mode":         config.authMode,
		"domain":            config.domain,
		"local_cache_saved": localErr == nil,
		"restart_required":  false,
	})
}

// CreateTeamChildMailbox provisions one address for the current, explicitly
// started Team child-account flow.
// POST /api/v1/admin/openai/team-child/mailboxes
func (h *OpenAIOAuthHandler) CreateTeamChildMailbox(c *gin.Context) {
	if !requireTeamChildAdminSession(c) {
		return
	}
	if h == nil || h.teamMailboxStore == nil {
		response.InternalError(c, "team child mailbox service is unavailable")
		return
	}
	provider, err := h.loadTeamMailboxProviderConfig(c)
	if err != nil {
		response.BadRequest(c, "Team child mailbox is not configured")
		return
	}

	requestedEmail, err := h.nextTeamChildMailboxAddress(c.Request.Context(), provider)
	if err != nil {
		response.InternalError(c, "temporary mailbox address sequence is unavailable")
		return
	}
	mailbox, err := h.provisionTeamChildMailboxSession(c.Request.Context(), teamChildRequestOwnerID(c), provider, requestedEmail)
	if err != nil {
		response.InternalError(c, "temporary mailbox session could not be stored")
		return
	}
	response.Success(c, mailbox)
}

// ListTeamChildMailboxes returns only addresses previously created or selected
// by the current administrator. Provider JWTs and XIASS mailbox session IDs are
// intentionally absent.
// GET /api/v1/admin/openai/team-child/mailboxes
func (h *OpenAIOAuthHandler) ListTeamChildMailboxes(c *gin.Context) {
	if !requireTeamChildAdminSession(c) {
		return
	}
	if h == nil || h.teamMailboxStore == nil {
		response.Success(c, openAITeamMailboxListResponse{Emails: []string{}})
		return
	}
	emails, err := h.teamMailboxStore.known(c.Request.Context(), teamChildRequestOwnerID(c))
	if err != nil {
		response.Error(c, http.StatusServiceUnavailable, "temporary mailbox service is temporarily unavailable")
		return
	}
	response.Success(c, openAITeamMailboxListResponse{Emails: emails})
}

// SelectTeamChildMailbox reopens one server-owned Team mailbox by asking the
// configured provider for a fresh scoped token and creating a new XIASS-only
// session. The provider token never crosses this handler.
// POST /api/v1/admin/openai/team-child/mailboxes/select
func (h *OpenAIOAuthHandler) SelectTeamChildMailbox(c *gin.Context) {
	if !requireTeamChildAdminSession(c) {
		return
	}
	if h == nil || h.teamMailboxStore == nil {
		response.InternalError(c, "team child mailbox service is unavailable")
		return
	}
	var req openAITeamMailboxSelectRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "请输入要重新打开的 Team 邮箱")
		return
	}
	provider, err := h.loadTeamMailboxProviderConfig(c)
	if err != nil {
		response.BadRequest(c, "Team child mailbox is not configured")
		return
	}
	email, err := validateSelectableTeamMailbox(req.Email, provider)
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	mailbox, err := h.provisionTeamChildMailboxSession(c.Request.Context(), teamChildRequestOwnerID(c), provider, email)
	if err != nil {
		response.InternalError(c, "temporary mailbox could not be reopened")
		return
	}
	response.Success(c, mailbox)
}

func validateSelectableTeamMailbox(raw string, provider teamMailboxProviderConfig) (string, error) {
	email := strings.ToLower(strings.TrimSpace(raw))
	localPart, domain, ok := strings.Cut(email, "@")
	configuredDomain, err := teamMailboxAddressDomain(provider)
	if err != nil {
		return "", errors.New("team 邮箱域名配置无效")
	}
	if !ok || !strings.EqualFold(domain, configuredDomain) || !strings.HasPrefix(localPart, teamMailboxAddressPrefix) {
		return "", errors.New("只能选择当前配置域名下由 XIASS 创建的 Team 邮箱")
	}
	numberText := strings.TrimPrefix(localPart, teamMailboxAddressPrefix)
	number, err := strconv.ParseInt(numberText, 10, 64)
	if err != nil || number < teamMailboxSequenceStart || fmt.Sprintf("%s%d", teamMailboxAddressPrefix, number) != localPart {
		return "", errors.New("team 邮箱必须使用 team 加数字的 XIASS 地址格式")
	}
	return localPart + "@" + configuredDomain, nil
}

func (h *OpenAIOAuthHandler) provisionTeamChildMailboxSession(ctx context.Context, ownerID int64, provider teamMailboxProviderConfig, requestedEmail string) (openAITeamMailboxCreateResponse, error) {
	if h == nil || h.teamMailboxStore == nil || ownerID <= 0 {
		return openAITeamMailboxCreateResponse{}, errors.New("temporary mailbox store is unavailable")
	}
	email, token, err := h.createTeamChildMailbox(ctx, provider, requestedEmail)
	if err != nil {
		return openAITeamMailboxCreateResponse{}, err
	}
	sessionID, err := newTeamMailboxSessionID()
	if err != nil {
		return openAITeamMailboxCreateResponse{}, err
	}
	expiresAt := h.teamMailboxStore.now().Add(teamMailboxSessionTTL)
	session := openAITeamMailboxSession{
		adminUserID: ownerID,
		email:       strings.ToLower(strings.TrimSpace(email)),
		token:       token,
		config:      provider,
		expiresAt:   expiresAt,
	}
	if err := h.teamMailboxStore.create(ctx, sessionID, session); err != nil {
		return openAITeamMailboxCreateResponse{}, err
	}
	if err := h.teamMailboxStore.remember(ctx, ownerID, session.email); err != nil {
		_ = h.teamMailboxStore.delete(ctx, sessionID, ownerID)
		return openAITeamMailboxCreateResponse{}, err
	}
	return openAITeamMailboxCreateResponse{
		SessionID: sessionID,
		Email:     session.email,
		ExpiresAt: expiresAt.UTC().Format(time.RFC3339),
	}, nil
}

// GetActiveTeamChildMailbox restores the current administrator's short-lived
// mailbox handle after a page refresh or an application update. Only the
// address, opaque session ID, and expiry are returned; the provider token stays
// in the server-side store.
// GET /api/v1/admin/openai/team-child/mailboxes/active
func (h *OpenAIOAuthHandler) GetActiveTeamChildMailbox(c *gin.Context) {
	if !requireTeamChildAdminSession(c) {
		return
	}
	if h == nil || h.teamMailboxStore == nil {
		response.Success(c, gin.H{"active": false})
		return
	}
	ownerID := teamChildRequestOwnerID(c)
	sessionID, session, ok, err := h.teamMailboxStore.active(c.Request.Context(), ownerID)
	if err != nil {
		response.Error(c, http.StatusServiceUnavailable, "temporary mailbox service is temporarily unavailable")
		return
	}
	if !ok {
		response.Success(c, gin.H{"active": false})
		return
	}
	response.Success(c, gin.H{
		"active": true,
		"mailbox": openAITeamMailboxCreateResponse{
			SessionID: sessionID,
			Email:     session.email,
			ExpiresAt: session.expiresAt.UTC().Format(time.RFC3339),
		},
	})
}

// PollTeamChildMailboxCode checks the provider once. The browser performs the
// polling, which keeps the server request bounded and lets the UI expose a
// truthful "waiting / received" state.
// GET /api/v1/admin/openai/team-child/mailboxes/:session_id/code
func (h *OpenAIOAuthHandler) PollTeamChildMailboxCode(c *gin.Context) {
	if !requireTeamChildAdminSession(c) {
		return
	}
	if h == nil || h.teamMailboxStore == nil {
		response.InternalError(c, "team child mailbox service is unavailable")
		return
	}
	ownerID := teamChildRequestOwnerID(c)
	session, ok, err := h.lookupTeamMailboxSessionContext(c.Request.Context(), c.Param("session_id"), ownerID)
	if err != nil {
		response.Error(c, http.StatusServiceUnavailable, "temporary mailbox service is temporarily unavailable")
		return
	}
	if !ok {
		response.BadRequest(c, "temporary mailbox session has expired")
		return
	}

	code, err := h.findTeamChildVerificationCode(c.Request.Context(), session)
	if err != nil {
		response.InternalError(c, "temporary mailbox could not be checked")
		return
	}
	result := openAITeamMailboxCodeResponse{
		Status:    "waiting",
		ExpiresAt: session.expiresAt.UTC().Format(time.RFC3339),
	}
	if code != "" {
		result.Status = "received"
		result.Code = code
	}
	response.Success(c, result)
}

// DeleteTeamChildMailboxSession only deletes XIASS's in-memory handle. It does
// not call an arbitrary remote delete endpoint and never exposes the mailbox JWT.
// DELETE /api/v1/admin/openai/team-child/mailboxes/:session_id
func (h *OpenAIOAuthHandler) DeleteTeamChildMailboxSession(c *gin.Context) {
	if !requireTeamChildAdminSession(c) {
		return
	}
	if h == nil || h.teamMailboxStore == nil {
		response.Success(c, gin.H{"deleted": true})
		return
	}
	ownerID := teamChildRequestOwnerID(c)
	if err := h.teamMailboxStore.delete(c.Request.Context(), c.Param("session_id"), ownerID); err != nil {
		response.Error(c, http.StatusServiceUnavailable, "temporary mailbox service is temporarily unavailable")
		return
	}
	response.Success(c, gin.H{"deleted": true})
}

func teamChildRequestOwnerID(c *gin.Context) int64 {
	if c == nil {
		return 0
	}
	if subject, ok := middleware.GetAuthSubjectFromContext(c); ok {
		return subject.UserID
	}
	return 0
}

func requireTeamChildAdminSession(c *gin.Context) bool {
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok || subject.UserID <= 0 {
		response.Unauthorized(c, "administrator session is required")
		return false
	}
	role, ok := middleware.GetUserRoleFromContext(c)
	if !ok || strings.TrimSpace(strings.ToLower(role)) != "admin" {
		response.Forbidden(c, "administrator access is required")
		return false
	}
	return true
}

type teamMailboxConfigValues struct {
	baseURL      string
	authMode     string
	apiKey       string
	customAuth   string
	domain       string
	createPath   string
	messagesPath string
}

type teamMailboxSharedConfigPayload struct {
	Version      int    `json:"version"`
	BaseURL      string `json:"base_url"`
	AuthMode     string `json:"auth_mode"`
	APIKey       string `json:"api_key"`
	CustomAuth   string `json:"custom_auth"`
	Domain       string `json:"domain"`
	CreatePath   string `json:"create_path"`
	MessagesPath string `json:"messages_path"`
}

func (h *OpenAIOAuthHandler) loadTeamMailboxProviderConfig(c *gin.Context) (teamMailboxProviderConfig, error) {
	return loadTeamMailboxProviderConfigShared(c, h.settingRepo, h.secretEncryptor)
}

func loadTeamMailboxProviderConfigShared(c *gin.Context, settingRepo service.SettingRepository, encryptor service.SecretEncryptor) (teamMailboxProviderConfig, error) {
	ctx := context.Background()
	if c != nil && c.Request != nil {
		ctx = c.Request.Context()
	}
	shared, found, sharedErr := loadTeamMailboxSharedConfig(ctx, settingRepo, encryptor)
	if sharedErr == nil && found {
		return validateTeamMailboxProviderConfig(shared)
	}

	values, err := loadLocalTeamMailboxConfigValues()
	if err != nil {
		if sharedErr != nil {
			return teamMailboxProviderConfig{}, sharedErr
		}
		return teamMailboxProviderConfig{}, err
	}
	config, err := validateTeamMailboxProviderConfig(values)
	if err != nil {
		if sharedErr != nil {
			return teamMailboxProviderConfig{}, sharedErr
		}
		return teamMailboxProviderConfig{}, err
	}
	// Existing single-node installations already have a persistent local file.
	// Publish it lazily after upgrade so the paired node can use the same rules
	// without asking the administrator to upload the secret a second time.
	if sharedErr == nil {
		_, _ = persistTeamMailboxSharedConfig(ctx, settingRepo, encryptor, values)
	}
	return config, nil
}

func loadTeamMailboxSharedConfig(ctx context.Context, settingRepo service.SettingRepository, encryptor service.SecretEncryptor) (teamMailboxConfigValues, bool, error) {
	if settingRepo == nil || encryptor == nil {
		return teamMailboxConfigValues{}, false, nil
	}
	stored, err := settingRepo.GetValue(ctx, service.SettingKeyTeamChildMailboxConfig)
	if err != nil {
		if errors.Is(err, service.ErrSettingNotFound) {
			return teamMailboxConfigValues{}, false, nil
		}
		return teamMailboxConfigValues{}, false, err
	}
	ciphertext, ok := strings.CutPrefix(strings.TrimSpace(stored), teamMailboxSharedConfigPrefix)
	if !ok || ciphertext == "" {
		return teamMailboxConfigValues{}, false, fmt.Errorf("shared Team mailbox configuration is invalid")
	}
	plaintext, err := encryptor.Decrypt(ciphertext)
	if err != nil {
		return teamMailboxConfigValues{}, false, fmt.Errorf("decrypt shared Team mailbox configuration: %w", err)
	}
	var payload teamMailboxSharedConfigPayload
	if err := json.Unmarshal([]byte(plaintext), &payload); err != nil || payload.Version != 1 {
		return teamMailboxConfigValues{}, false, fmt.Errorf("shared Team mailbox configuration payload is invalid")
	}
	return teamMailboxConfigValues{
		baseURL: payload.BaseURL, authMode: payload.AuthMode, apiKey: payload.APIKey,
		customAuth: payload.CustomAuth, domain: payload.Domain,
		createPath: payload.CreatePath, messagesPath: payload.MessagesPath,
	}, true, nil
}

func (h *OpenAIOAuthHandler) persistTeamMailboxSharedConfig(ctx context.Context, values teamMailboxConfigValues) (bool, error) {
	if h == nil {
		return false, nil
	}
	return persistTeamMailboxSharedConfig(ctx, h.settingRepo, h.secretEncryptor, values)
}

func persistTeamMailboxSharedConfig(ctx context.Context, settingRepo service.SettingRepository, encryptor service.SecretEncryptor, values teamMailboxConfigValues) (bool, error) {
	if settingRepo == nil || encryptor == nil {
		return false, nil
	}
	payload, err := json.Marshal(teamMailboxSharedConfigPayload{
		Version: 1, BaseURL: values.baseURL, AuthMode: values.authMode, APIKey: values.apiKey,
		CustomAuth: values.customAuth, Domain: values.domain,
		CreatePath: values.createPath, MessagesPath: values.messagesPath,
	})
	if err != nil {
		return false, err
	}
	ciphertext, err := encryptor.Encrypt(string(payload))
	if err != nil {
		return false, err
	}
	if err := settingRepo.Set(ctx, service.SettingKeyTeamChildMailboxConfig, teamMailboxSharedConfigPrefix+ciphertext); err != nil {
		return false, err
	}
	return true, nil
}

func (h *OpenAIOAuthHandler) bootstrapTeamMailboxSharedConfig(parent context.Context) {
	if h == nil || h.settingRepo == nil || h.secretEncryptor == nil {
		return
	}
	ctx, cancel := context.WithTimeout(parent, teamMailboxSharedBootstrapTTL)
	defer cancel()
	if _, found, err := loadTeamMailboxSharedConfig(ctx, h.settingRepo, h.secretEncryptor); err != nil || found {
		return
	}
	values, err := loadLocalTeamMailboxConfigValues()
	if err != nil {
		return
	}
	if _, err := validateTeamMailboxProviderConfig(values); err != nil {
		return
	}
	_, _ = persistTeamMailboxSharedConfig(ctx, h.settingRepo, h.secretEncryptor, values)
}

func loadTeamMailboxProviderConfig(_ *gin.Context) (teamMailboxProviderConfig, error) {
	values, err := loadLocalTeamMailboxConfigValues()
	if err != nil {
		return teamMailboxProviderConfig{}, err
	}
	return validateTeamMailboxProviderConfig(values)
}

func loadLocalTeamMailboxConfigValues() (teamMailboxConfigValues, error) {
	// Environment variables remain the baseline for existing deployments. A
	// normalized file written by the admin upload endpoint overrides them and is
	// stored under the persistent application data directory.
	values := teamMailboxConfigValues{
		baseURL:      os.Getenv("TEAM_CHILD_MAIL_API_BASE"),
		authMode:     os.Getenv("TEAM_CHILD_MAIL_AUTH_MODE"),
		apiKey:       os.Getenv("TEAM_CHILD_MAIL_API_KEY"),
		customAuth:   os.Getenv("TEAM_CHILD_MAIL_CUSTOM_AUTH"),
		domain:       os.Getenv("TEAM_CHILD_MAIL_DOMAIN"),
		createPath:   os.Getenv("TEAM_CHILD_MAIL_CREATE_PATH"),
		messagesPath: os.Getenv("TEAM_CHILD_MAIL_MESSAGES_PATH"),
	}
	if persisted, err := readTeamMailboxConfigFile(); err == nil {
		// The persisted fragment is normalized and contains every supported
		// field, including intentional empty values that must clear an older
		// environment setting.
		values = persisted
	} else if !errors.Is(err, os.ErrNotExist) {
		return teamMailboxConfigValues{}, err
	}
	return values, nil
}

func validateTeamMailboxProviderConfig(values teamMailboxConfigValues) (teamMailboxProviderConfig, error) {
	baseRaw := strings.TrimSpace(values.baseURL)
	if baseRaw == "" {
		return teamMailboxProviderConfig{}, fmt.Errorf("TEAM_CHILD_MAIL_API_BASE is required")
	}
	baseURL, err := url.Parse(baseRaw)
	if err != nil || baseURL.Scheme == "" || baseURL.Host == "" ||
		(baseURL.Scheme != "https" && baseURL.Scheme != "http") ||
		baseURL.RawQuery != "" || baseURL.Fragment != "" {
		return teamMailboxProviderConfig{}, fmt.Errorf("TEAM_CHILD_MAIL_API_BASE is invalid")
	}

	authMode := strings.ToLower(strings.TrimSpace(values.authMode))
	if authMode == "" {
		authMode = "none"
	}
	switch authMode {
	case "none", "x-api-key", "x-admin-auth", "bearer", "query-key":
	default:
		return teamMailboxProviderConfig{}, fmt.Errorf("TEAM_CHILD_MAIL_AUTH_MODE is invalid")
	}
	apiKey := strings.TrimSpace(values.apiKey)
	if authMode != "none" && apiKey == "" {
		return teamMailboxProviderConfig{}, fmt.Errorf("TEAM_CHILD_MAIL_API_KEY is required")
	}

	createPath, err := teamMailboxEndpointPath(values.createPath, "/api/new_address")
	if err != nil {
		return teamMailboxProviderConfig{}, err
	}
	messagesPath, err := teamMailboxEndpointPath(values.messagesPath, "/api/mails")
	if err != nil {
		return teamMailboxProviderConfig{}, err
	}
	return teamMailboxProviderConfig{
		baseURL:      baseURL,
		createPath:   createPath,
		messagesPath: messagesPath,
		domain:       strings.TrimSpace(values.domain),
		apiKey:       apiKey,
		authMode:     authMode,
		customAuth:   strings.TrimSpace(values.customAuth),
	}, nil
}

func teamMailboxConfigFilePath() string {
	if configured := strings.TrimSpace(os.Getenv("TEAM_CHILD_MAIL_CONFIG_FILE")); configured != "" {
		return configured
	}
	return "/app/data/team-child-mail.env"
}

func readTeamMailboxConfigFile() (teamMailboxConfigValues, error) {
	filePath := teamMailboxConfigFilePath()
	body, err := os.ReadFile(filePath)
	if err != nil {
		return teamMailboxConfigValues{}, err
	}
	return parseTeamMailboxConfigImport(body, filepath.Base(filePath))
}

func writeTeamMailboxConfigFile(values teamMailboxConfigValues) error {
	filePath := teamMailboxConfigFilePath()
	if err := os.MkdirAll(filepath.Dir(filePath), 0o750); err != nil {
		return err
	}
	content := strings.Join([]string{
		"# Managed by XIASS Team child mailbox import. Keep this file private.",
		"TEAM_CHILD_MAIL_API_BASE=" + strconv.Quote(values.baseURL),
		"TEAM_CHILD_MAIL_AUTH_MODE=" + strconv.Quote(values.authMode),
		"TEAM_CHILD_MAIL_API_KEY=" + strconv.Quote(values.apiKey),
		"TEAM_CHILD_MAIL_CUSTOM_AUTH=" + strconv.Quote(values.customAuth),
		"TEAM_CHILD_MAIL_DOMAIN=" + strconv.Quote(values.domain),
		"TEAM_CHILD_MAIL_CREATE_PATH=" + strconv.Quote(values.createPath),
		"TEAM_CHILD_MAIL_MESSAGES_PATH=" + strconv.Quote(values.messagesPath),
		"",
	}, "\n")
	if err := os.WriteFile(filePath, []byte(content), 0o600); err != nil {
		return err
	}
	return os.Chmod(filePath, 0o600)
}

func teamMailboxEndpointPath(raw, fallback string) (string, error) {
	endpoint := strings.TrimSpace(raw)
	if endpoint == "" {
		endpoint = fallback
	}
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.IsAbs() || parsed.Host != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", fmt.Errorf("team child mailbox endpoint path is invalid")
	}
	if !strings.HasPrefix(parsed.Path, "/") {
		parsed.Path = "/" + parsed.Path
	}
	return parsed.Path, nil
}

func (s *openAITeamMailboxStore) pruneLocked(now time.Time) {
	for sessionID, session := range s.sessions {
		if !session.expiresAt.After(now) {
			delete(s.sessions, sessionID)
			if s.activeByAdmin[session.adminUserID] == sessionID {
				delete(s.activeByAdmin, session.adminUserID)
			}
		}
	}
}

func (s *openAITeamMailboxStore) redisClient() *redisclient.Client {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.redis
}

func (s *openAITeamMailboxStore) create(ctx context.Context, sessionID string, session openAITeamMailboxSession) error {
	sessionID = strings.TrimSpace(sessionID)
	if s == nil || sessionID == "" || len(sessionID) > 256 || session.adminUserID <= 0 || session.email == "" || session.token == "" || session.config.baseURL == nil || session.expiresAt.IsZero() {
		return errors.New("temporary mailbox session is invalid")
	}
	if redisClient := s.redisClient(); redisClient != nil {
		payload, err := encodePersistedTeamMailboxSession(session)
		if err != nil {
			return err
		}
		ttl := time.Until(session.expiresAt)
		if ttl <= 0 {
			return errors.New("temporary mailbox session has expired")
		}
		sessionKey := teamMailboxRedisKeyPrefix + sessionID
		if err := redisClient.Set(ctx, sessionKey, payload, ttl).Err(); err != nil {
			return err
		}
		if err := redisClient.Set(ctx, teamMailboxActiveKeyPrefix+strconv.FormatInt(session.adminUserID, 10), sessionID, ttl).Err(); err != nil {
			// Do not leave a newly created, but unreachable, mailbox session behind
			// when the index write fails. A later request cannot safely recover it.
			_ = redisClient.Del(ctx, sessionKey).Err()
			return err
		}
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneLocked(s.now())
	s.sessions[sessionID] = session
	s.activeByAdmin[session.adminUserID] = sessionID
	return nil
}

func (s *openAITeamMailboxStore) remember(ctx context.Context, ownerID int64, email string) error {
	email = strings.ToLower(strings.TrimSpace(email))
	if s == nil || ownerID <= 0 || email == "" {
		return errors.New("temporary mailbox address is invalid")
	}
	now := s.now()
	if redisClient := s.redisClient(); redisClient != nil {
		key := teamMailboxKnownKeyPrefix + strconv.FormatInt(ownerID, 10)
		pipe := redisClient.TxPipeline()
		pipe.ZAdd(ctx, key, redisclient.Z{Score: float64(now.Unix()), Member: email})
		pipe.ZRemRangeByScore(ctx, key, "-inf", strconv.FormatInt(now.Add(-teamMailboxKnownTTL).Unix(), 10))
		pipe.Expire(ctx, key, teamMailboxKnownTTL)
		_, err := pipe.Exec(ctx)
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	known := s.knownByAdmin[ownerID]
	if known == nil {
		known = make(map[string]time.Time)
		s.knownByAdmin[ownerID] = known
	}
	known[email] = now
	for candidate, seenAt := range known {
		if seenAt.Before(now.Add(-teamMailboxKnownTTL)) {
			delete(known, candidate)
		}
	}
	return nil
}

func (s *openAITeamMailboxStore) known(ctx context.Context, ownerID int64) ([]string, error) {
	if s == nil || ownerID <= 0 {
		return []string{}, nil
	}
	now := s.now()
	if redisClient := s.redisClient(); redisClient != nil {
		key := teamMailboxKnownKeyPrefix + strconv.FormatInt(ownerID, 10)
		if err := redisClient.ZRemRangeByScore(ctx, key, "-inf", strconv.FormatInt(now.Add(-teamMailboxKnownTTL).Unix(), 10)).Err(); err != nil {
			return nil, err
		}
		return redisClient.ZRevRange(ctx, key, 0, teamMailboxKnownLimit-1).Result()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	known := s.knownByAdmin[ownerID]
	type mailboxSeen struct {
		email string
		at    time.Time
	}
	items := make([]mailboxSeen, 0, len(known))
	for email, seenAt := range known {
		if seenAt.Before(now.Add(-teamMailboxKnownTTL)) {
			delete(known, email)
			continue
		}
		items = append(items, mailboxSeen{email: email, at: seenAt})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].at.After(items[j].at) })
	if int64(len(items)) > teamMailboxKnownLimit {
		items = items[:teamMailboxKnownLimit]
	}
	emails := make([]string, 0, len(items))
	for _, item := range items {
		emails = append(emails, item.email)
	}
	return emails, nil
}

// isKnown is the authorization check used by the pre-import mailbox-link
// manager. It avoids letting an administrator turn a guessed address from the
// configured domain into a public inbox link; only a mailbox created or opened
// through that administrator's XIASS Team flow qualifies here.
func (s *openAITeamMailboxStore) isKnown(ctx context.Context, ownerID int64, email string) (bool, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	if s == nil || ownerID <= 0 || email == "" {
		return false, nil
	}
	now := s.now()
	if redisClient := s.redisClient(); redisClient != nil {
		key := teamMailboxKnownKeyPrefix + strconv.FormatInt(ownerID, 10)
		score, err := redisClient.ZScore(ctx, key, email).Result()
		if errors.Is(err, redisclient.Nil) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		if time.Unix(int64(score), 0).Before(now.Add(-teamMailboxKnownTTL)) {
			_ = redisClient.ZRem(ctx, key, email).Err()
			return false, nil
		}
		return true, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	seenAt, ok := s.knownByAdmin[ownerID][email]
	if !ok || seenAt.Before(now.Add(-teamMailboxKnownTTL)) {
		if ok {
			delete(s.knownByAdmin[ownerID], email)
		}
		return false, nil
	}
	return true, nil
}

func (s *openAITeamMailboxStore) active(ctx context.Context, ownerID int64) (string, openAITeamMailboxSession, bool, error) {
	if s == nil || ownerID <= 0 {
		return "", openAITeamMailboxSession{}, false, nil
	}
	now := s.now()
	if redisClient := s.redisClient(); redisClient != nil {
		indexKey := teamMailboxActiveKeyPrefix + strconv.FormatInt(ownerID, 10)
		sessionID, err := redisClient.Get(ctx, indexKey).Result()
		if errors.Is(err, redisclient.Nil) {
			// Sessions created before the active-session index was introduced are
			// still recoverable through a bounded, read-only prefix scan.
			return s.findActiveInRedis(ctx, redisClient, ownerID, now)
		}
		if err != nil {
			return "", openAITeamMailboxSession{}, false, err
		}
		payload, err := redisClient.Get(ctx, teamMailboxRedisKeyPrefix+sessionID).Bytes()
		if errors.Is(err, redisclient.Nil) {
			_ = redisClient.Del(ctx, indexKey).Err()
			return s.findActiveInRedis(ctx, redisClient, ownerID, now)
		}
		if err != nil {
			return "", openAITeamMailboxSession{}, false, err
		}
		session, err := decodePersistedTeamMailboxSession(payload)
		if err != nil || session.adminUserID != ownerID || !session.expiresAt.After(now) {
			_ = redisClient.Del(ctx, indexKey).Err()
			return s.findActiveInRedis(ctx, redisClient, ownerID, now)
		}
		return sessionID, session, true, nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneLocked(now)
	sessionID := s.activeByAdmin[ownerID]
	session, ok := s.sessions[sessionID]
	if !ok || !session.expiresAt.After(now) || session.adminUserID != ownerID {
		delete(s.activeByAdmin, ownerID)
		return "", openAITeamMailboxSession{}, false, nil
	}
	return sessionID, session, true, nil
}

func (s *openAITeamMailboxStore) findActiveInRedis(ctx context.Context, redisClient *redisclient.Client, ownerID int64, now time.Time) (string, openAITeamMailboxSession, bool, error) {
	var (
		cursor     uint64
		bestID     string
		best       openAITeamMailboxSession
		bestExpiry time.Time
		scanned    int
	)
	for {
		keys, next, err := redisClient.Scan(ctx, cursor, teamMailboxRedisKeyPrefix+"*", 100).Result()
		if err != nil {
			return "", openAITeamMailboxSession{}, false, err
		}
		for _, key := range keys {
			scanned++
			if scanned > teamMailboxLegacyScanLimit {
				break
			}
			if strings.HasPrefix(key, teamMailboxActiveKeyPrefix) || strings.HasPrefix(key, teamMailboxKnownKeyPrefix) || key == teamMailboxSequenceKey {
				continue
			}
			payload, err := redisClient.Get(ctx, key).Bytes()
			if err != nil {
				continue
			}
			session, err := decodePersistedTeamMailboxSession(payload)
			if err != nil || session.adminUserID != ownerID || !session.expiresAt.After(now) {
				continue
			}
			if bestID == "" || session.expiresAt.After(bestExpiry) {
				bestID, best, bestExpiry = strings.TrimPrefix(key, teamMailboxRedisKeyPrefix), session, session.expiresAt
			}
		}
		cursor = next
		if cursor == 0 || scanned >= teamMailboxLegacyScanLimit {
			break
		}
	}
	if bestID == "" {
		return "", openAITeamMailboxSession{}, false, nil
	}
	ttl := best.expiresAt.Sub(now)
	if ttl > 0 {
		_ = redisClient.Set(ctx, teamMailboxActiveKeyPrefix+strconv.FormatInt(ownerID, 10), bestID, ttl).Err()
	}
	return bestID, best, true, nil
}

func newTeamMailboxSessionID() (string, error) {
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func (h *OpenAIOAuthHandler) lookupTeamMailboxSession(sessionID string) (openAITeamMailboxSession, bool) {
	session, ok, _ := h.lookupTeamMailboxSessionContext(context.Background(), sessionID, 0)
	return session, ok
}

func (h *OpenAIOAuthHandler) lookupTeamMailboxSessionContext(ctx context.Context, sessionID string, ownerID int64) (openAITeamMailboxSession, bool, error) {
	if h == nil || h.teamMailboxStore == nil {
		return openAITeamMailboxSession{}, false, errors.New("temporary mailbox store is unavailable")
	}
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" || len(sessionID) > 256 {
		return openAITeamMailboxSession{}, false, nil
	}
	var (
		session openAITeamMailboxSession
		ok      bool
		err     error
	)
	if redisClient := h.teamMailboxStore.redisClient(); redisClient != nil {
		var payload []byte
		payload, err = redisClient.Get(ctx, teamMailboxRedisKeyPrefix+sessionID).Bytes()
		if errors.Is(err, redisclient.Nil) {
			return openAITeamMailboxSession{}, false, nil
		}
		if err != nil {
			return openAITeamMailboxSession{}, false, err
		}
		session, err = decodePersistedTeamMailboxSession(payload)
		if err != nil {
			_ = redisClient.Del(ctx, teamMailboxRedisKeyPrefix+sessionID).Err()
			return openAITeamMailboxSession{}, false, err
		}
		ok = session.expiresAt.After(h.teamMailboxStore.now())
		if !ok {
			_ = redisClient.Del(ctx, teamMailboxRedisKeyPrefix+sessionID).Err()
		}
	} else {
		h.teamMailboxStore.mu.Lock()
		now := h.teamMailboxStore.now()
		h.teamMailboxStore.pruneLocked(now)
		session, ok = h.teamMailboxStore.sessions[sessionID]
		ok = ok && session.expiresAt.After(now)
		h.teamMailboxStore.mu.Unlock()
	}
	if ok && ownerID > 0 && session.adminUserID > 0 && session.adminUserID != ownerID {
		return openAITeamMailboxSession{}, false, nil
	}
	return session, ok, nil
}

func (s *openAITeamMailboxStore) delete(ctx context.Context, sessionID string, ownerID int64) error {
	sessionID = strings.TrimSpace(sessionID)
	if s == nil || sessionID == "" || len(sessionID) > 256 {
		return nil
	}
	if redisClient := s.redisClient(); redisClient != nil {
		// Do not allow a caller to delete another administrator's mailbox handle.
		// The read + delete race is harmless for this cleanup-only operation, and
		// the owner check is repeated by the normal lookup path before use.
		payload, err := redisClient.Get(ctx, teamMailboxRedisKeyPrefix+sessionID).Bytes()
		if errors.Is(err, redisclient.Nil) {
			return nil
		}
		if err != nil {
			return err
		}
		session, err := decodePersistedTeamMailboxSession(payload)
		if err != nil {
			// The unreadable session cannot reveal an owner safely. Remove only the
			// payload; active() will discard a stale index on its next lookup.
			return redisClient.Del(ctx, teamMailboxRedisKeyPrefix+sessionID).Err()
		}
		if ownerID > 0 && session.adminUserID > 0 && session.adminUserID != ownerID {
			return nil
		}
		if err := redisClient.Del(ctx, teamMailboxRedisKeyPrefix+sessionID).Err(); err != nil {
			return err
		}
		indexKey := teamMailboxActiveKeyPrefix + strconv.FormatInt(session.adminUserID, 10)
		if currentID, getErr := redisClient.Get(ctx, indexKey).Result(); getErr == nil && currentID == sessionID {
			return redisClient.Del(ctx, indexKey).Err()
		}
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	session, ok := s.sessions[sessionID]
	if ok && ownerID > 0 && session.adminUserID > 0 && session.adminUserID != ownerID {
		return nil
	}
	delete(s.sessions, sessionID)
	if currentID := s.activeByAdmin[session.adminUserID]; currentID == sessionID {
		delete(s.activeByAdmin, session.adminUserID)
	}
	s.pruneLocked(s.now())
	return nil
}

func encodePersistedTeamMailboxSession(session openAITeamMailboxSession) ([]byte, error) {
	if session.config.baseURL == nil {
		return nil, errors.New("temporary mailbox configuration is invalid")
	}
	return json.Marshal(persistedTeamMailboxSession{
		AdminUserID: session.adminUserID,
		Email:       session.email,
		Token:       session.token,
		Config: persistedTeamMailboxConfig{
			BaseURL:      session.config.baseURL.String(),
			CreatePath:   session.config.createPath,
			MessagesPath: session.config.messagesPath,
			Domain:       session.config.domain,
			APIKey:       session.config.apiKey,
			AuthMode:     session.config.authMode,
			CustomAuth:   session.config.customAuth,
		},
		ExpiresAt: session.expiresAt.UTC(),
	})
}

func decodePersistedTeamMailboxSession(payload []byte) (openAITeamMailboxSession, error) {
	var persisted persistedTeamMailboxSession
	if err := json.Unmarshal(payload, &persisted); err != nil {
		return openAITeamMailboxSession{}, fmt.Errorf("decode temporary mailbox session: %w", err)
	}
	baseURL, err := url.Parse(persisted.Config.BaseURL)
	if err != nil || baseURL.Scheme == "" || baseURL.Host == "" || persisted.Email == "" || persisted.Token == "" || persisted.ExpiresAt.IsZero() {
		return openAITeamMailboxSession{}, errors.New("temporary mailbox session is invalid")
	}
	return openAITeamMailboxSession{
		adminUserID: persisted.AdminUserID,
		email:       persisted.Email,
		token:       persisted.Token,
		config: teamMailboxProviderConfig{
			baseURL:      baseURL,
			createPath:   persisted.Config.CreatePath,
			messagesPath: persisted.Config.MessagesPath,
			domain:       persisted.Config.Domain,
			apiKey:       persisted.Config.APIKey,
			authMode:     persisted.Config.AuthMode,
			customAuth:   persisted.Config.CustomAuth,
		},
		expiresAt: persisted.ExpiresAt,
	}, nil
}

func (h *OpenAIOAuthHandler) createTeamChildMailbox(ctx context.Context, config teamMailboxProviderConfig, requestedEmail string) (string, string, error) {
	localPart, domain, ok := strings.Cut(strings.TrimSpace(requestedEmail), "@")
	if !ok || localPart == "" || domain == "" {
		return "", "", errors.New("requested mailbox address is invalid")
	}
	// Both the public and administrator Cloudflare mailbox endpoints accept a
	// requested name. Disable the provider's global prefix so the address stays
	// exactly in the server-owned team1000, team1001, ... sequence.
	payload := map[string]any{
		"name":         localPart,
		"domain":       domain,
		"enablePrefix": false,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", "", err
	}
	endpoint := teamMailboxURL(config.baseURL, config.createPath)
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), bytes.NewReader(body))
	if err != nil {
		return "", "", err
	}
	request.Header.Set("Content-Type", "application/json")
	applyTeamMailboxCreateAuth(request, config)

	responseBody, status, err := h.doTeamMailboxRequest(request)
	if err != nil {
		return "", "", err
	}
	if status < http.StatusOK || status >= http.StatusMultipleChoices {
		return "", "", fmt.Errorf("mailbox create request failed with HTTP %d", status)
	}
	data, err := decodeTeamMailboxObject(responseBody)
	if err != nil {
		return "", "", err
	}
	data = nestedTeamMailboxData(data)
	email := strings.TrimSpace(teamMailboxString(data["address"]))
	if email == "" {
		email = strings.TrimSpace(teamMailboxString(data["email"]))
	}
	token := strings.TrimSpace(teamMailboxString(data["jwt"]))
	if token == "" {
		token = strings.TrimSpace(teamMailboxString(data["token"]))
	}
	if !teamMailboxAddressRE.MatchString(email) || token == "" {
		return "", "", fmt.Errorf("mailbox create response is incomplete")
	}
	parsedEmail := strings.ToLower(strings.TrimSpace(email))
	if parsedEmail != strings.ToLower(strings.TrimSpace(requestedEmail)) {
		return "", "", fmt.Errorf("mailbox provider returned %q instead of requested address %q", email, requestedEmail)
	}
	return email, token, nil
}

func (h *OpenAIOAuthHandler) nextTeamChildMailboxAddress(ctx context.Context, config teamMailboxProviderConfig) (string, error) {
	if h == nil || h.teamMailboxStore == nil {
		return "", errors.New("temporary mailbox store is unavailable")
	}
	domain, err := teamMailboxAddressDomain(config)
	if err != nil {
		return "", err
	}
	store := h.teamMailboxStore
	if redisClient := store.redisClient(); redisClient != nil {
		// Initialize and increment in one Redis script so two administrators (or
		// two application instances) cannot receive the same address.
		const script = `
			local current = redis.call('GET', KEYS[1])
			if not current then
				redis.call('SET', KEYS[1], ARGV[1])
			elseif tonumber(current) < tonumber(ARGV[1]) then
				redis.call('SET', KEYS[1], ARGV[1])
			end
			return redis.call('INCR', KEYS[1])
		`
		number, err := redisclient.NewScript(script).Run(ctx, redisClient, []string{teamMailboxSequenceKey}, teamMailboxSequenceStart-1).Int64()
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("%s%d@%s", teamMailboxAddressPrefix, number, domain), nil
	}

	store.sequenceMu.Lock()
	defer store.sequenceMu.Unlock()
	filePath := strings.TrimSpace(os.Getenv(teamMailboxSequenceFileEnv))
	if filePath == "" {
		filePath = teamMailboxSequenceFile
	}
	current := teamMailboxSequenceStart - 1
	if body, err := os.ReadFile(filePath); err == nil {
		value, parseErr := strconv.ParseInt(strings.TrimSpace(string(body)), 10, 64)
		if parseErr != nil || value < teamMailboxSequenceStart-1 {
			return "", errors.New("temporary mailbox sequence file is invalid")
		}
		current = value
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	next := current + 1
	if err := os.MkdirAll(filepath.Dir(filePath), 0o750); err != nil {
		return "", err
	}
	tmpPath := filePath + ".tmp"
	if err := os.WriteFile(tmpPath, []byte(strconv.FormatInt(next, 10)+"\n"), 0o600); err != nil {
		return "", err
	}
	if err := os.Rename(tmpPath, filePath); err != nil {
		_ = os.Remove(tmpPath)
		return "", err
	}
	return fmt.Sprintf("%s%d@%s", teamMailboxAddressPrefix, next, domain), nil
}

func teamMailboxAddressDomain(config teamMailboxProviderConfig) (string, error) {
	// The sequence must be derived from the administrator-imported provider
	// domain. Keeping this explicit avoids embedding a maintainer domain in the
	// release and prevents an address from being displayed before the provider
	// can actually create that exact mailbox.
	domain := strings.ToLower(strings.TrimSpace(config.domain))
	if domain == "" {
		return "", errors.New("TEAM_CHILD_MAIL_DOMAIN is required for sequential mailbox addresses")
	}
	if match := teamMailboxAddressRE.FindString("x@" + domain); !strings.EqualFold(match, "x@"+domain) {
		return "", errors.New("TEAM_CHILD_MAIL_DOMAIN is invalid")
	}
	return domain, nil
}

func (h *OpenAIOAuthHandler) findTeamChildVerificationCode(ctx context.Context, session openAITeamMailboxSession) (string, error) {
	endpoint := teamMailboxURL(session.config.baseURL, session.config.messagesPath)
	query := endpoint.Query()
	query.Set("limit", "20")
	query.Set("offset", "0")
	// Cloudflare Workers may serve a just-created inbox list from an edge cache.
	// Polling must observe newly delivered verification messages without relying on
	// an operator to reopen the mailbox, so every read has an opaque cache-buster
	// and explicit no-cache headers. Provider authentication remains unchanged.
	query.Set("_xiass_poll", strconv.FormatInt(time.Now().UnixNano(), 10))
	if session.config.authMode == "query-key" && session.config.apiKey != "" {
		query.Set("key", session.config.apiKey)
	}
	endpoint.RawQuery = query.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return "", err
	}
	applyTeamMailboxFreshReadHeaders(request)
	applyTeamMailboxReadAuth(request, session)
	body, status, err := h.doTeamMailboxRequest(request)
	if err != nil {
		return "", err
	}
	if status < http.StatusOK || status >= http.StatusMultipleChoices {
		return "", fmt.Errorf("mailbox list request failed with HTTP %d", status)
	}
	data, err := decodeTeamMailboxObject(body)
	if err != nil {
		return "", err
	}
	for _, message := range teamMailboxMessageList(data) {
		if !teamMailboxMessageMatchesAddress(message, session.email) {
			continue
		}

		// The Team workflow must only consume OpenAI authorization mail. The
		// same mailbox can now be shared as a complete inbox, so an unrelated
		// family, banking, or service email may legitimately contain six digits.
		// Never let that number advance the OpenAI browser flow.
		if teamMailboxMessageIsOfficialOpenAI(message) {
			if code := extractTeamMailboxVerificationCodeFromMessage(message); code != "" {
				return code, nil
			}
		} else if teamMailboxShareMessageSender(message) != "" {
			// A known non-OpenAI sender cannot become an OAuth code just because
			// its full message body happens to include a short numeric value.
			continue
		}

		messageID := strings.TrimSpace(teamMailboxString(message["id"]))
		if messageID == "" {
			messageID = strings.TrimSpace(teamMailboxString(message["msgid"]))
		}
		if messageID == "" {
			continue
		}
		if detail, detailErr := h.fetchTeamMailboxMessage(ctx, session, messageID); detailErr == nil {
			if teamMailboxMessageIsOfficialOpenAI(detail) {
				if code := extractTeamMailboxVerificationCodeFromMessage(detail); code != "" {
					return code, nil
				}
			}
		}
	}
	return "", nil
}

func (h *OpenAIOAuthHandler) fetchTeamMailboxMessage(ctx context.Context, session openAITeamMailboxSession, messageID string) (map[string]any, error) {
	candidates := []string{
		"/api/mail/" + url.PathEscape(messageID),
		strings.TrimSuffix(session.config.messagesPath, "/") + "/" + url.PathEscape(messageID),
	}
	var lastErr error
	for _, endpointPath := range candidates {
		endpoint := teamMailboxURL(session.config.baseURL, endpointPath)
		query := endpoint.Query()
		query.Set("_xiass_poll", strconv.FormatInt(time.Now().UnixNano(), 10))
		if session.config.authMode == "query-key" && session.config.apiKey != "" {
			query.Set("key", session.config.apiKey)
		}
		endpoint.RawQuery = query.Encode()
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
		if err != nil {
			lastErr = err
			continue
		}
		applyTeamMailboxFreshReadHeaders(request)
		applyTeamMailboxReadAuth(request, session)
		body, status, err := h.doTeamMailboxRequest(request)
		if err != nil {
			lastErr = err
			continue
		}
		if status < http.StatusOK || status >= http.StatusMultipleChoices {
			lastErr = fmt.Errorf("mailbox detail request failed with HTTP %d", status)
			continue
		}
		data, err := decodeTeamMailboxObject(body)
		if err != nil {
			lastErr = err
			continue
		}
		return nestedTeamMailboxData(data), nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("mailbox detail request failed")
	}
	return nil, lastErr
}

func (h *OpenAIOAuthHandler) doTeamMailboxRequest(request *http.Request) ([]byte, int, error) {
	if h == nil || h.teamMailboxStore == nil || h.teamMailboxStore.client == nil {
		return nil, 0, fmt.Errorf("mailbox client is unavailable")
	}
	if request.Header.Get("User-Agent") == "" {
		request.Header.Set("User-Agent", teamMailboxProviderUserAgent)
	}
	resp, err := h.teamMailboxStore.client.Do(request)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, teamMailboxBodyLimit+1))
	if err != nil {
		return nil, resp.StatusCode, err
	}
	if len(body) > teamMailboxBodyLimit {
		return nil, resp.StatusCode, fmt.Errorf("mailbox provider response is too large")
	}
	return body, resp.StatusCode, nil
}

func teamMailboxURL(base *url.URL, endpointPath string) *url.URL {
	resolved := *base
	resolved.Path = path.Join("/", base.Path, endpointPath)
	resolved.RawPath = ""
	return &resolved
}

func applyTeamMailboxCreateAuth(request *http.Request, config teamMailboxProviderConfig) {
	if config.customAuth != "" {
		request.Header.Set("x-custom-auth", config.customAuth)
	}
	switch config.authMode {
	case "x-api-key":
		request.Header.Set("X-API-Key", config.apiKey)
	case "x-admin-auth":
		request.Header.Set("x-admin-auth", config.apiKey)
	case "bearer":
		request.Header.Set("Authorization", "Bearer "+config.apiKey)
	case "query-key":
		query := request.URL.Query()
		query.Set("key", config.apiKey)
		request.URL.RawQuery = query.Encode()
	}
}

func applyTeamMailboxReadAuth(request *http.Request, session openAITeamMailboxSession) {
	request.Header.Set("Authorization", "Bearer "+session.token)
	if session.config.customAuth != "" {
		request.Header.Set("x-custom-auth", session.config.customAuth)
	}
}

func applyTeamMailboxFreshReadHeaders(request *http.Request) {
	if request == nil {
		return
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Cache-Control", "no-cache, no-store, max-age=0")
	request.Header.Set("Pragma", "no-cache")
}

func decodeTeamMailboxObject(body []byte) (map[string]any, error) {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil || value == nil {
		return nil, fmt.Errorf("mailbox provider returned invalid JSON")
	}
	if data, ok := value.(map[string]any); ok {
		return data, nil
	}
	if messages, ok := value.([]any); ok {
		// A few mailbox workers return the message list directly instead of
		// wrapping it in {data|messages|results: [...]}. Normalize that shape so
		// the rest of the parser follows one path.
		return map[string]any{"data": messages}, nil
	}
	return nil, fmt.Errorf("mailbox provider returned invalid JSON")
}

func nestedTeamMailboxData(data map[string]any) map[string]any {
	if nested, ok := data["data"].(map[string]any); ok {
		return nested
	}
	return data
}

func teamMailboxMessageList(data map[string]any) []map[string]any {
	for _, key := range []string{"results", "messages", "mails", "emails", "items", "inbox", "hydra:member", "data"} {
		value := data[key]
		if messages := teamMailboxObjectSlice(value); len(messages) > 0 {
			return messages
		}
		if nested, ok := value.(map[string]any); ok {
			if messages := teamMailboxMessageList(nested); len(messages) > 0 {
				return messages
			}
		}
	}
	return nil
}

func teamMailboxObjectSlice(value any) []map[string]any {
	switch list := value.(type) {
	case []any:
		messages := make([]map[string]any, 0, len(list))
		for _, item := range list {
			if message, ok := item.(map[string]any); ok {
				messages = append(messages, message)
			}
		}
		return messages
	case []map[string]any:
		return list
	default:
		return nil
	}
}

func teamMailboxMessageMatchesAddress(message map[string]any, email string) bool {
	target := strings.ToLower(strings.TrimSpace(email))
	if target == "" {
		return false
	}
	recipientMetadata := false
	for _, key := range []string{
		"to", "recipient", "recipients", "recipient_email", "to_email", "delivered_to", "envelope_to", "mail_to", "destination", "address",
	} {
		value, present := message[key]
		if !present || value == nil {
			continue
		}
		if len(teamMailboxAddresses(value)) > 0 || teamMailboxHasValue(value) {
			recipientMetadata = true
		}
		for _, candidate := range teamMailboxAddresses(value) {
			if candidate == target {
				return true
			}
		}
	}
	// Some providers put the envelope recipient under a headers/envelope
	// object. Only inspect recipient-shaped keys there; sender fields must not
	// make an unrelated message look like a verification email for this inbox.
	for _, containerKey := range []string{"headers", "envelope", "meta"} {
		container, ok := message[containerKey].(map[string]any)
		if !ok {
			continue
		}
		for _, key := range []string{"to", "recipient", "recipients", "delivered-to", "delivered_to", "envelope-to", "envelope_to"} {
			value, present := container[key]
			if !present || value == nil {
				continue
			}
			recipientMetadata = true
			for _, candidate := range teamMailboxAddresses(value) {
				if candidate == target {
					return true
				}
			}
		}
	}
	// Some Worker list endpoints omit recipients. The mailbox JWT scopes the
	// list to this session, so accepting an entry with no recipient metadata is
	// safer than discarding a legitimate verification message. If recipient
	// metadata is present and does not match, reject it explicitly.
	return !recipientMetadata
}

func teamMailboxAddresses(value any) []string {
	var values []string
	var collect func(any)
	collect = func(item any) {
		switch typed := item.(type) {
		case string:
			values = append(values, teamMailboxAddressRE.FindAllString(strings.ToLower(typed), -1)...)
		case map[string]any:
			for _, key := range []string{"address", "email", "value", "mail", "recipient", "to", "delivered-to", "delivered_to"} {
				collect(typed[key])
			}
		case []any:
			for _, child := range typed {
				collect(child)
			}
		}
	}
	collect(value)
	return values
}

func teamMailboxHasValue(value any) bool {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed) != ""
	case json.Number:
		return typed.String() != ""
	case []any:
		for _, item := range typed {
			if teamMailboxHasValue(item) {
				return true
			}
		}
	case map[string]any:
		for _, item := range typed {
			if teamMailboxHasValue(item) {
				return true
			}
		}
	}
	return false
}

func teamMailboxReadableText(message map[string]any) string {
	parts := make([]string, 0, 8)
	for _, key := range []string{
		"subject", "text", "content", "intro", "body", "snippet", "html", "plain", "plain_text", "body_text", "message", "preview",
	} {
		appendTeamMailboxReadableValue(&parts, message[key], 0)
	}
	return strings.Join(parts, "\n")
}

func appendTeamMailboxReadableValue(parts *[]string, value any, depth int) {
	if depth > 6 || value == nil {
		return
	}
	switch typed := value.(type) {
	case string:
		cleaned := strings.TrimSpace(teamMailboxTextFromHTML(stdhtml.UnescapeString(typed)))
		if cleaned != "" {
			*parts = append(*parts, cleaned)
		}
	case json.Number:
		if typed.String() != "" {
			*parts = append(*parts, typed.String())
		}
	case []any:
		for _, item := range typed {
			appendTeamMailboxReadableValue(parts, item, depth+1)
		}
	case map[string]any:
		// Nested bodies commonly use {text|html|content: ...}. Prefer those
		// fields, but fall back to all scalar values for provider-specific shapes.
		usedKnownField := false
		for _, key := range []string{
			"subject", "text", "content", "intro", "body", "snippet", "html", "plain", "plain_text", "body_text", "message", "preview",
		} {
			if nested, ok := typed[key]; ok {
				usedKnownField = true
				appendTeamMailboxReadableValue(parts, nested, depth+1)
			}
		}
		if !usedKnownField {
			for _, nested := range typed {
				appendTeamMailboxReadableValue(parts, nested, depth+1)
			}
		}
	}
}

func teamMailboxTextFromHTML(raw string) string {
	return strings.Join(strings.Fields(teamMailboxHTMLNoiseRE.ReplaceAllString(raw, " ")), " ")
}

func extractTeamMailboxVerificationCode(text string) string {
	for _, pattern := range []*regexp.Regexp{teamMailboxSemanticCodeRE, teamMailboxCodeLabelRE, teamMailboxReverseCodeRE} {
		if match := pattern.FindStringSubmatch(text); len(match) == 2 {
			code := strings.Map(func(r rune) rune {
				if r >= '0' && r <= '9' {
					return r
				}
				return -1
			}, match[1])
			if len(code) >= 4 && len(code) <= 8 {
				return code
			}
		}
	}
	return ""
}

// teamMailboxMessageIsOfficialOpenAI validates the message identity before the
// local Team automation can use a code. It intentionally accepts OpenAI and
// ChatGPT domains, including their operational subdomains. Some providers omit
// sender metadata in list responses, so an explicit OpenAI/ChatGPT subject is
// a narrow compatibility fallback only when there is no conflicting sender.
func teamMailboxMessageIsOfficialOpenAI(message map[string]any) bool {
	sender := strings.ToLower(strings.TrimSpace(teamMailboxShareMessageSender(message)))
	if sender != "" {
		addresses := teamMailboxAddressRE.FindAllString(sender, -1)
		if len(addresses) > 0 {
			for _, address := range addresses {
				at := strings.LastIndex(address, "@")
				if at < 1 || at == len(address)-1 {
					continue
				}
				domain := strings.ToLower(strings.TrimSpace(address[at+1:]))
				if domain == "openai.com" || domain == "chatgpt.com" ||
					strings.HasSuffix(domain, ".openai.com") || strings.HasSuffix(domain, ".chatgpt.com") {
					return true
				}
			}
			return false
		}

		// A few mailbox providers only retain the trusted display name. Do not
		// treat arbitrary names containing "openai" as official, and never let
		// this path override an explicitly non-OpenAI email address above.
		return sender == "openai" || sender == "chatgpt"
	}

	identity := strings.ToLower(teamMailboxDecodeMIMEHeader(teamMailboxShareMessageField(message, "subject", "title", "headline")))
	return strings.Contains(identity, "openai") || strings.Contains(identity, "chatgpt")
}

// A provider can omit the visible "code" label from a localized or
// template-driven body. Identity validation belongs to the caller: the local
// Team workflow first restricts this helper to official OpenAI mail, whereas
// the deliberately full-featured shared inbox may display any sender's code.
func extractTeamMailboxVerificationCodeFromMessage(message map[string]any) string {
	text := teamMailboxReadableText(message)
	if code := extractTeamMailboxVerificationCode(text); code != "" {
		return code
	}
	if decoded := teamMailboxDecodedMIMETextFromMessage(message); decoded != "" {
		if code := extractTeamMailboxVerificationCode(decoded); code != "" {
			return code
		}
		if code := extractTeamMailboxStandaloneSixDigitCode(decoded); code != "" {
			return code
		}
	}

	for _, key := range []string{
		"text", "plain", "plain_text", "body_text", "body", "content", "message", "preview", "subject", "html",
	} {
		var parts []string
		appendTeamMailboxReadableValue(&parts, message[key], 0)
		if code := extractTeamMailboxStandaloneSixDigitCode(strings.Join(parts, "\n")); code != "" {
			return code
		}
	}
	return ""
}

func extractTeamMailboxStandaloneSixDigitCode(text string) string {
	match := teamMailboxStandaloneSixDigitRE.FindStringSubmatch(text)
	if len(match) != 2 {
		return ""
	}
	return strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, match[1])
}

func teamMailboxString(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case json.Number:
		return typed.String()
	case float64:
		return fmt.Sprintf("%.0f", typed)
	default:
		return ""
	}
}
