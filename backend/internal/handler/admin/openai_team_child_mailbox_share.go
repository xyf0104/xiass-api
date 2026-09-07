package admin

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	stdhtml "html"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
)

// A mailbox share is a long-lived, revocable bearer capability for exactly one
// XIASS-created Team mailbox. The browser keeps the token in a URL fragment and
// submits it only in an Authorization header, so it is absent from normal page
// requests, reverse-proxy logs, and referrers.
const (
	teamMailboxShareTokenPrefix       = "tm2"
	teamMailboxShareTokenEntropyBytes = 32
	teamMailboxShareIDBytes           = 18
	teamMailboxSharePollInterval      = 5 * time.Second
	teamMailboxShareStateIdleTTL      = 24 * time.Hour
	teamMailboxShareStateLimit        = 512
	teamMailboxShareMessageLimit      = 100
	teamMailboxShareFileEnv           = "TEAM_CHILD_MAIL_SHARE_FILE"
	teamMailboxShareFile              = "/app/data/team-child-mail-shares.json"
)

var (
	errTeamMailboxShareAlreadyActive   = errors.New("mailbox share is already active")
	errTeamMailboxShareMessageNotFound = errors.New("mailbox share message is not available")
)

type teamMailboxShareRequest struct {
	Email     string `json:"email"`
	AccountID *int64 `json:"account_id,omitempty"`
	Replace   bool   `json:"replace"`
}

type teamMailboxShareStatusResponse struct {
	Active    bool   `json:"active"`
	Email     string `json:"email"`
	CreatedAt string `json:"created_at,omitempty"`
	// Token is only returned to an authenticated administrator. It is encrypted
	// at rest so the same long-lived link can be copied again after reopening
	// the Team mailbox panel.
	Token string `json:"token,omitempty"`
}

type teamMailboxShareCodeResponse struct {
	Email       string `json:"email"`
	Status      string `json:"status"`
	Code        string `json:"code,omitempty"`
	CheckedAt   string `json:"checked_at"`
	PollAfterMS int    `json:"poll_after_ms"`
}

// teamMailboxShareMessageResponse intentionally contains only mail data for
// the linked inbox. It never carries provider URLs, service API keys, mailbox
// JWTs, XIASS account credentials, or any state from the administrator UI.
type teamMailboxShareMessageResponse struct {
	ID         string `json:"id"`
	From       string `json:"from,omitempty"`
	Subject    string `json:"subject,omitempty"`
	Preview    string `json:"preview,omitempty"`
	ReceivedAt string `json:"received_at,omitempty"`
	Code       string `json:"code,omitempty"`
}

type teamMailboxShareMessagesResponse struct {
	Email       string                            `json:"email"`
	Messages    []teamMailboxShareMessageResponse `json:"messages"`
	CheckedAt   string                            `json:"checked_at"`
	PollAfterMS int                               `json:"poll_after_ms"`
}

type teamMailboxShareMessageDetailResponse struct {
	teamMailboxShareMessageResponse
	Body string `json:"body"`
	// HTML is stripped to a deliberately small email-safe subset before this
	// public response is created. It never contains scripts, forms, provider
	// credentials, or mailbox session data. Images and layout styles are
	// constrained to an inert subset and rendered in an isolated iframe.
	HTML string `json:"html,omitempty"`
}

// The registry is intentionally separate from the 20-minute administrator
// mailbox session. It is persisted under the existing XIASS data volume, so a
// link can be created immediately after a Team mailbox is allocated, then keep
// working through OAuth import, application restart, and normal upgrades.
// The registry stores the token hash used for public validation plus an
// application-encrypted copy that only an authenticated administrator can
// recover for repeated copying. Plaintext bearer tokens are never persisted.
type openAITeamMailboxShareRegistry struct {
	mu sync.Mutex
}

type persistedTeamMailboxShareRegistry struct {
	Version int                                  `json:"version"`
	Shares  map[string]persistedTeamMailboxShare `json:"shares"`
}

type persistedTeamMailboxShare struct {
	Email           string `json:"email"`
	TokenHash       string `json:"token_hash"`
	TokenCiphertext string `json:"token_ciphertext,omitempty"`
	CreatedAt       string `json:"created_at"`
	AccountID       int64  `json:"account_id,omitempty"`
}

func newOpenAITeamMailboxShareRegistry() *openAITeamMailboxShareRegistry {
	return &openAITeamMailboxShareRegistry{}
}

func teamMailboxShareFilePath() string {
	if configured := strings.TrimSpace(os.Getenv(teamMailboxShareFileEnv)); configured != "" {
		return configured
	}
	return teamMailboxShareFile
}

func (r *openAITeamMailboxShareRegistry) status(email string) (persistedTeamMailboxShare, bool, error) {
	if r == nil {
		return persistedTeamMailboxShare{}, false, errors.New("mailbox share registry is unavailable")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	registry, err := r.loadLocked()
	if err != nil {
		return persistedTeamMailboxShare{}, false, err
	}
	_, record, ok := registry.shareForEmail(email)
	return record, ok, nil
}

func (r *openAITeamMailboxShareRegistry) create(email string, accountID int64, shareID, tokenHash, tokenCiphertext string, replace bool) (oldTokenHash string, record persistedTeamMailboxShare, err error) {
	if r == nil {
		return "", persistedTeamMailboxShare{}, errors.New("mailbox share registry is unavailable")
	}
	if !validTeamMailboxShareID(shareID) || !validTeamMailboxShareHash(tokenHash) || !validTeamMailboxShareCiphertext(tokenCiphertext) {
		return "", persistedTeamMailboxShare{}, errors.New("mailbox share token is invalid")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	registry, err := r.loadLocked()
	if err != nil {
		return "", persistedTeamMailboxShare{}, err
	}
	if oldID, existing, ok := registry.shareForEmail(email); ok {
		if !replace {
			return "", persistedTeamMailboxShare{}, errTeamMailboxShareAlreadyActive
		}
		oldTokenHash = existing.TokenHash
		delete(registry.Shares, oldID)
	}
	record = persistedTeamMailboxShare{
		Email:           email,
		TokenHash:       tokenHash,
		TokenCiphertext: tokenCiphertext,
		CreatedAt:       time.Now().UTC().Format(time.RFC3339),
		AccountID:       accountID,
	}
	registry.Shares[shareID] = record
	if err := r.writeLocked(registry); err != nil {
		return "", persistedTeamMailboxShare{}, err
	}
	return oldTokenHash, record, nil
}

func (r *openAITeamMailboxShareRegistry) revoke(email string) (string, error) {
	if r == nil {
		return "", errors.New("mailbox share registry is unavailable")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	registry, err := r.loadLocked()
	if err != nil {
		return "", err
	}
	shareID, record, ok := registry.shareForEmail(email)
	if !ok {
		return "", nil
	}
	delete(registry.Shares, shareID)
	if err := r.writeLocked(registry); err != nil {
		return "", err
	}
	return record.TokenHash, nil
}

// attachAccount records the imported account without changing the bearer
// capability. The public path later verifies the account still exists, making
// a link invalid automatically if its Team account is deleted.
func (r *openAITeamMailboxShareRegistry) attachAccount(email string, accountID int64) error {
	if r == nil || accountID <= 0 {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	registry, err := r.loadLocked()
	if err != nil {
		return err
	}
	shareID, record, ok := registry.shareForEmail(email)
	if !ok || record.AccountID == accountID {
		return nil
	}
	if record.AccountID != 0 {
		return fmt.Errorf("mailbox share belongs to another account")
	}
	record.AccountID = accountID
	registry.Shares[shareID] = record
	return r.writeLocked(registry)
}

func (r *openAITeamMailboxShareRegistry) resolve(shareID, token string) (persistedTeamMailboxShare, bool, error) {
	if r == nil {
		return persistedTeamMailboxShare{}, false, errors.New("mailbox share registry is unavailable")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	registry, err := r.loadLocked()
	if err != nil {
		return persistedTeamMailboxShare{}, false, err
	}
	record, ok := registry.Shares[shareID]
	if !ok || !validPersistedTeamMailboxShare(record) {
		return persistedTeamMailboxShare{}, false, nil
	}
	actual := teamMailboxShareTokenDigest(token)
	if subtle.ConstantTimeCompare([]byte(record.TokenHash), []byte(actual)) != 1 {
		return persistedTeamMailboxShare{}, false, nil
	}
	return record, true, nil
}

func (r *openAITeamMailboxShareRegistry) loadLocked() (persistedTeamMailboxShareRegistry, error) {
	registry := persistedTeamMailboxShareRegistry{Version: 1, Shares: make(map[string]persistedTeamMailboxShare)}
	body, err := os.ReadFile(teamMailboxShareFilePath())
	if errors.Is(err, os.ErrNotExist) {
		return registry, nil
	}
	if err != nil {
		return registry, err
	}
	if err := json.Unmarshal(body, &registry); err != nil {
		return persistedTeamMailboxShareRegistry{}, fmt.Errorf("decode mailbox share registry: %w", err)
	}
	if registry.Version != 1 {
		return persistedTeamMailboxShareRegistry{}, errors.New("mailbox share registry version is unsupported")
	}
	if registry.Shares == nil {
		registry.Shares = make(map[string]persistedTeamMailboxShare)
	}
	for shareID, record := range registry.Shares {
		if !validTeamMailboxShareID(shareID) || !validPersistedTeamMailboxShare(record) {
			return persistedTeamMailboxShareRegistry{}, errors.New("mailbox share registry contains invalid data")
		}
	}
	return registry, nil
}

func (r *openAITeamMailboxShareRegistry) writeLocked(registry persistedTeamMailboxShareRegistry) error {
	if registry.Version == 0 {
		registry.Version = 1
	}
	if registry.Shares == nil {
		registry.Shares = make(map[string]persistedTeamMailboxShare)
	}
	filePath := teamMailboxShareFilePath()
	if err := os.MkdirAll(filepath.Dir(filePath), 0o750); err != nil {
		return err
	}
	body, err := json.MarshalIndent(registry, "", "  ")
	if err != nil {
		return err
	}
	body = append(body, '\n')
	tmp, err := os.CreateTemp(filepath.Dir(filePath), ".team-mailbox-shares-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(body); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, filePath); err != nil {
		return err
	}
	return os.Chmod(filePath, 0o600)
}

func (r persistedTeamMailboxShareRegistry) shareForEmail(email string) (string, persistedTeamMailboxShare, bool) {
	for shareID, record := range r.Shares {
		if strings.EqualFold(record.Email, email) {
			return shareID, record, true
		}
	}
	return "", persistedTeamMailboxShare{}, false
}

func validPersistedTeamMailboxShare(record persistedTeamMailboxShare) bool {
	_, err := normalizeTeamMailboxShareEmail(record.Email)
	if err != nil || !validTeamMailboxShareHash(record.TokenHash) {
		return false
	}
	if record.TokenCiphertext != "" && !validTeamMailboxShareCiphertext(record.TokenCiphertext) {
		return false
	}
	_, err = time.Parse(time.RFC3339, strings.TrimSpace(record.CreatedAt))
	return err == nil && record.AccountID >= 0
}

func validTeamMailboxShareCiphertext(value string) bool {
	value = strings.TrimSpace(value)
	return value != "" && len(value) <= 8192 && teamMailboxHTMLHasNoControls(value)
}

func validTeamMailboxShareHash(value string) bool {
	value = strings.TrimSpace(value)
	if len(value) != base64.RawURLEncoding.EncodedLen(sha256.Size) {
		return false
	}
	_, err := base64.RawURLEncoding.DecodeString(value)
	return err == nil
}

// openAITeamMailboxShareStore is process-local by design. It contains only
// short-lived provider JWTs and up-to-five-second message snapshots. Durable
// authorization is owned by openAITeamMailboxShareRegistry.
type openAITeamMailboxShareStore struct {
	mu     sync.Mutex
	states map[string]*openAITeamMailboxShareState
	now    func() time.Time
}

type openAITeamMailboxShareState struct {
	mu                    sync.Mutex
	email                 string
	session               openAITeamMailboxSession
	lastMessagesCheckedAt time.Time
	messages              []teamMailboxShareMessageRecord
	details               map[string]teamMailboxShareMessageDetailResponse
	lastAccessedAt        time.Time
}

type teamMailboxShareMessageRecord struct {
	summary teamMailboxShareMessageResponse
	raw     map[string]any
}

func newOpenAITeamMailboxShareStore() *openAITeamMailboxShareStore {
	return &openAITeamMailboxShareStore{
		states: make(map[string]*openAITeamMailboxShareState),
		now:    time.Now,
	}
}

func (s *openAITeamMailboxShareStore) stateFor(tokenHash, email string) *openAITeamMailboxShareState {
	if s == nil {
		return nil
	}
	now := s.now()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneLocked(now)
	if state := s.states[tokenHash]; state != nil && state.email == email {
		return state
	}
	state := &openAITeamMailboxShareState{
		email:          email,
		details:        make(map[string]teamMailboxShareMessageDetailResponse),
		lastAccessedAt: now,
	}
	s.states[tokenHash] = state
	return state
}

func (s *openAITeamMailboxShareStore) forget(tokenHash string) {
	if s == nil || tokenHash == "" {
		return
	}
	s.mu.Lock()
	delete(s.states, tokenHash)
	s.mu.Unlock()
}

func (s *openAITeamMailboxShareStore) reset() {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.states = make(map[string]*openAITeamMailboxShareState)
	s.mu.Unlock()
}

func (s *openAITeamMailboxShareStore) pruneLocked(now time.Time) {
	for tokenHash, state := range s.states {
		if state == nil || (!state.lastAccessedAt.IsZero() && now.Sub(state.lastAccessedAt) > teamMailboxShareStateIdleTTL) {
			delete(s.states, tokenHash)
		}
	}
	for len(s.states) > teamMailboxShareStateLimit {
		var oldestKey string
		var oldestAt time.Time
		for tokenHash, state := range s.states {
			if oldestKey == "" || state.lastAccessedAt.Before(oldestAt) {
				oldestKey, oldestAt = tokenHash, state.lastAccessedAt
			}
		}
		if oldestKey == "" {
			break
		}
		delete(s.states, oldestKey)
	}
}

func (h *OpenAIOAuthHandler) ensureTeamMailboxShareStore() *openAITeamMailboxShareStore {
	if h == nil {
		return nil
	}
	if h.teamMailboxShareStore == nil {
		h.teamMailboxShareStore = newOpenAITeamMailboxShareStore()
	}
	return h.teamMailboxShareStore
}

func (h *OpenAIOAuthHandler) ensureTeamMailboxShareRegistry() *openAITeamMailboxShareRegistry {
	if h == nil {
		return nil
	}
	if h.teamMailboxShareRegistry == nil {
		h.teamMailboxShareRegistry = newOpenAITeamMailboxShareRegistry()
	}
	return h.teamMailboxShareRegistry
}

// GetTeamChildMailboxShare exposes the current account's standalone mailbox
// link state, including the original long-lived link when its encrypted
// server-side copy is still available.
// GET /api/v1/admin/openai/team-child/accounts/:account_id/mailbox-share
func (h *OpenAIOAuthHandler) GetTeamChildMailboxShare(c *gin.Context) {
	if !requireTeamChildAdminSession(c) {
		return
	}
	account, email, ok := h.teamMailboxShareAccount(c)
	if !ok {
		return
	}
	h.respondTeamMailboxShareStatus(c, email, account.ID)
}

// CreateTeamChildMailboxShare creates or explicitly replaces an imported
// Team account's long-lived mailbox link.
// POST /api/v1/admin/openai/team-child/accounts/:account_id/mailbox-share
func (h *OpenAIOAuthHandler) CreateTeamChildMailboxShare(c *gin.Context) {
	if !requireTeamChildAdminSession(c) {
		return
	}
	var req teamMailboxShareRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "接码链接请求无效")
		return
	}
	account, email, ok := h.teamMailboxShareAccount(c)
	if !ok {
		return
	}
	if err := ensureAdminAccountManagementAccess(c.Request.Context(), h.adminService, account.ID); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	h.createTeamMailboxShare(c, email, account.ID, req.Replace)
}

// RevokeTeamChildMailboxShare removes an imported Team account's public inbox
// capability immediately.
// DELETE /api/v1/admin/openai/team-child/accounts/:account_id/mailbox-share
func (h *OpenAIOAuthHandler) RevokeTeamChildMailboxShare(c *gin.Context) {
	if !requireTeamChildAdminSession(c) {
		return
	}
	account, email, ok := h.teamMailboxShareAccount(c)
	if !ok {
		return
	}
	if err := ensureAdminAccountManagementAccess(c.Request.Context(), h.adminService, account.ID); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	h.revokeTeamMailboxShare(c, email, account.ID)
}

// GetPendingTeamChildMailboxShare lets an administrator manage a mailbox link
// immediately after a Team address is created, before OAuth account import.
// GET /api/v1/admin/openai/team-child/mailbox-share?email=...&account_id=...
func (h *OpenAIOAuthHandler) GetPendingTeamChildMailboxShare(c *gin.Context) {
	if !requireTeamChildAdminSession(c) {
		return
	}
	email, accountID, ok := h.teamMailboxShareRequestIdentity(c, false)
	if !ok {
		return
	}
	h.respondTeamMailboxShareStatus(c, email, accountID)
}

// CreatePendingTeamChildMailboxShare creates a long-lived link for the current
// Team mailbox before the OAuth account is imported.
// POST /api/v1/admin/openai/team-child/mailbox-share
func (h *OpenAIOAuthHandler) CreatePendingTeamChildMailboxShare(c *gin.Context) {
	if !requireTeamChildAdminSession(c) {
		return
	}
	email, accountID, replace, ok := h.teamMailboxShareJSONRequestIdentity(c, true)
	if !ok {
		return
	}
	if accountID > 0 {
		if err := ensureAdminAccountManagementAccess(c.Request.Context(), h.adminService, accountID); err != nil {
			response.ErrorFrom(c, err)
			return
		}
	}
	h.createTeamMailboxShare(c, email, accountID, replace)
}

// RevokePendingTeamChildMailboxShare revokes a mailbox link before or after
// account import. It never deletes or changes a mailbox-provider session.
// DELETE /api/v1/admin/openai/team-child/mailbox-share
func (h *OpenAIOAuthHandler) RevokePendingTeamChildMailboxShare(c *gin.Context) {
	if !requireTeamChildAdminSession(c) {
		return
	}
	email, accountID, _, ok := h.teamMailboxShareJSONRequestIdentity(c, false)
	if !ok {
		return
	}
	if accountID > 0 {
		if err := ensureAdminAccountManagementAccess(c.Request.Context(), h.adminService, accountID); err != nil {
			response.ErrorFrom(c, err)
			return
		}
	}
	h.revokeTeamMailboxShare(c, email, accountID)
}

func (h *OpenAIOAuthHandler) respondTeamMailboxShareStatus(c *gin.Context, email string, accountID int64) {
	record, active, err := h.ensureTeamMailboxShareRegistry().status(email)
	if err != nil {
		response.InternalError(c, "无法读取接码链接状态")
		return
	}
	if active && accountID > 0 && record.AccountID == 0 {
		if err := ensureAdminAccountManagementAccess(c.Request.Context(), h.adminService, accountID); err != nil {
			response.ErrorFrom(c, err)
			return
		}
		if err := h.ensureTeamMailboxShareRegistry().attachAccount(email, accountID); err != nil {
			response.InternalError(c, "无法关联接码链接")
			return
		}
		record.AccountID = accountID
	}
	response.Success(c, teamMailboxShareStatusResponse{
		Active:    active,
		Email:     email,
		CreatedAt: record.CreatedAt,
		Token:     h.reusableTeamMailboxShareToken(record),
	})
}

func (h *OpenAIOAuthHandler) createTeamMailboxShare(c *gin.Context, email string, accountID int64, replace bool) {
	if h == nil || h.secretEncryptor == nil {
		response.InternalError(c, "接码链接加密服务不可用")
		return
	}
	shareID, token, tokenHash, err := newTeamMailboxShareToken()
	if err != nil {
		response.InternalError(c, "无法生成接码链接")
		return
	}
	tokenCiphertext, err := h.secretEncryptor.Encrypt(token)
	if err != nil || !validTeamMailboxShareCiphertext(tokenCiphertext) {
		response.InternalError(c, "无法安全保存接码链接")
		return
	}
	oldTokenHash, record, err := h.ensureTeamMailboxShareRegistry().create(email, accountID, shareID, tokenHash, tokenCiphertext, replace)
	if errors.Is(err, errTeamMailboxShareAlreadyActive) {
		response.Error(c, http.StatusConflict, "该邮箱已经有可用的接码链接，请确认后替换")
		return
	}
	if err != nil {
		response.InternalError(c, "无法保存接码链接")
		return
	}
	if oldTokenHash != "" {
		h.ensureTeamMailboxShareStore().forget(oldTokenHash)
	}
	response.Success(c, teamMailboxShareStatusResponse{
		Active:    true,
		Email:     email,
		CreatedAt: record.CreatedAt,
		Token:     token,
	})
}

func (h *OpenAIOAuthHandler) reusableTeamMailboxShareToken(record persistedTeamMailboxShare) string {
	if h == nil || h.secretEncryptor == nil || strings.TrimSpace(record.TokenCiphertext) == "" {
		return ""
	}
	token, err := h.secretEncryptor.Decrypt(record.TokenCiphertext)
	if err != nil || teamMailboxShareTokenDigest(token) != record.TokenHash {
		return ""
	}
	if _, _, err := publicTeamMailboxShareToken("Bearer " + token); err != nil {
		return ""
	}
	return token
}

func (h *OpenAIOAuthHandler) revokeTeamMailboxShare(c *gin.Context, email string, accountID int64) {
	record, active, err := h.ensureTeamMailboxShareRegistry().status(email)
	if err != nil {
		response.InternalError(c, "无法读取接码链接状态")
		return
	}
	if active && accountID > 0 && record.AccountID != 0 && record.AccountID != accountID {
		response.Forbidden(c, "接码链接不属于当前 Team 账号")
		return
	}
	tokenHash, err := h.ensureTeamMailboxShareRegistry().revoke(email)
	if err != nil {
		response.InternalError(c, "无法撤销接码链接")
		return
	}
	h.ensureTeamMailboxShareStore().forget(tokenHash)
	response.Success(c, teamMailboxShareStatusResponse{Email: email})
}

func (h *OpenAIOAuthHandler) teamMailboxShareRequestIdentity(c *gin.Context, requireProvider bool) (string, int64, bool) {
	email, err := normalizeTeamMailboxShareEmail(c.Query("email"))
	if err != nil {
		response.BadRequest(c, "Team 邮箱无效")
		return "", 0, false
	}
	accountID, err := h.teamMailboxShareRequestedAccount(c.Request.Context(), email, c.Query("account_id"))
	if err != nil {
		response.ErrorFrom(c, err)
		return "", 0, false
	}
	if accountID == 0 && !h.teamMailboxShareCurrentAdminOwnsEmail(c, email) {
		return "", 0, false
	}
	if requireProvider {
		provider, providerErr := h.loadTeamMailboxProviderConfig(c)
		if providerErr != nil {
			response.BadRequest(c, "Team 子号邮箱服务尚未配置")
			return "", 0, false
		}
		if email, providerErr = validateSelectableTeamMailbox(email, provider); providerErr != nil {
			response.BadRequest(c, providerErr.Error())
			return "", 0, false
		}
	}
	return email, accountID, true
}

func (h *OpenAIOAuthHandler) teamMailboxShareJSONRequestIdentity(c *gin.Context, requireProvider bool) (string, int64, bool, bool) {
	var req teamMailboxShareRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "接码链接请求无效")
		return "", 0, false, false
	}
	email, err := normalizeTeamMailboxShareEmail(req.Email)
	if err != nil {
		response.BadRequest(c, "Team 邮箱无效")
		return "", 0, false, false
	}
	var rawAccountID string
	if req.AccountID != nil {
		rawAccountID = strconv.FormatInt(*req.AccountID, 10)
	}
	accountID, err := h.teamMailboxShareRequestedAccount(c.Request.Context(), email, rawAccountID)
	if err != nil {
		response.ErrorFrom(c, err)
		return "", 0, false, false
	}
	if accountID == 0 && !h.teamMailboxShareCurrentAdminOwnsEmail(c, email) {
		return "", 0, false, false
	}
	if requireProvider {
		provider, providerErr := h.loadTeamMailboxProviderConfig(c)
		if providerErr != nil {
			response.BadRequest(c, "Team 子号邮箱服务尚未配置")
			return "", 0, false, false
		}
		if email, providerErr = validateSelectableTeamMailbox(email, provider); providerErr != nil {
			response.BadRequest(c, providerErr.Error())
			return "", 0, false, false
		}
	}
	return email, accountID, req.Replace, true
}

func (h *OpenAIOAuthHandler) teamMailboxShareCurrentAdminOwnsEmail(c *gin.Context, email string) bool {
	if h == nil || h.teamMailboxStore == nil {
		response.InternalError(c, "Team 邮箱服务暂不可用")
		return false
	}
	known, err := h.teamMailboxStore.isKnown(c.Request.Context(), teamChildRequestOwnerID(c), email)
	if err != nil {
		response.Error(c, http.StatusServiceUnavailable, "Team 邮箱服务暂不可用")
		return false
	}
	if !known {
		response.Forbidden(c, "只能管理当前管理员已创建的 Team 邮箱")
		return false
	}
	return true
}

func (h *OpenAIOAuthHandler) teamMailboxShareRequestedAccount(ctx context.Context, email, rawAccountID string) (int64, error) {
	rawAccountID = strings.TrimSpace(rawAccountID)
	if rawAccountID == "" {
		return 0, nil
	}
	accountID, err := strconv.ParseInt(rawAccountID, 10, 64)
	if err != nil || accountID <= 0 {
		return 0, service.ErrAccountNotFound
	}
	if h == nil || h.adminService == nil {
		return 0, errors.New("account service is unavailable")
	}
	account, err := h.adminService.GetAccount(ctx, accountID)
	if err != nil {
		return 0, err
	}
	accountEmail, valid := teamMailboxShareableEmail(account)
	if !valid || !strings.EqualFold(accountEmail, email) {
		return 0, errors.New("team account does not match mailbox")
	}
	return accountID, nil
}

func (h *OpenAIOAuthHandler) teamMailboxShareAccount(c *gin.Context) (*service.Account, string, bool) {
	if h == nil || h.adminService == nil {
		response.InternalError(c, "Team 账号服务暂不可用")
		return nil, "", false
	}
	accountID, err := strconv.ParseInt(strings.TrimSpace(c.Param("account_id")), 10, 64)
	if err != nil || accountID <= 0 {
		response.BadRequest(c, "Team 账号无效")
		return nil, "", false
	}
	account, err := h.adminService.GetAccount(c.Request.Context(), accountID)
	if err != nil {
		response.NotFound(c, "未找到 Team 账号")
		return nil, "", false
	}
	email, valid := teamMailboxShareableEmail(account)
	if !valid {
		response.BadRequest(c, "只有已导入的 Team 子号可以管理接码链接")
		return nil, "", false
	}
	return account, email, true
}

func teamMailboxShareableEmail(account *service.Account) (string, bool) {
	if account == nil || account.Platform != service.PlatformOpenAI || !account.IsOAuth() {
		return "", false
	}
	teamChild, _ := account.Extra[service.OpenAITeamChildExtraKey].(bool)
	email, _ := account.Extra[service.OpenAITeamChildEmailExtraKey].(string)
	email, err := normalizeTeamMailboxShareEmail(email)
	if !teamChild || err != nil {
		return "", false
	}
	return email, true
}

func normalizeTeamMailboxShareEmail(raw string) (string, error) {
	email := strings.ToLower(strings.TrimSpace(raw))
	localPart, _, ok := strings.Cut(email, "@")
	if !ok || teamMailboxAddressRE.FindString(email) != email || !strings.HasPrefix(localPart, teamMailboxAddressPrefix) {
		return "", errors.New("mailbox address is invalid")
	}
	numberText := strings.TrimPrefix(localPart, teamMailboxAddressPrefix)
	number, err := strconv.ParseInt(numberText, 10, 64)
	if err != nil || number < teamMailboxSequenceStart || fmt.Sprintf("%s%d", teamMailboxAddressPrefix, number) != localPart {
		return "", errors.New("mailbox address is invalid")
	}
	return email, nil
}

// PollPublicTeamChildMailboxShare preserves the compact verification-code API
// for clients that only need a direct copy target. It derives the code from the
// full inbox snapshot and never applies an OpenAI sender or subject filter.
// GET /api/v1/public/team-mailbox/code
func (h *OpenAIOAuthHandler) PollPublicTeamChildMailboxShare(c *gin.Context) {
	tokenHash, email, provider, ok := h.resolvePublicTeamMailboxShare(c)
	if !ok {
		return
	}
	messages, checkedAt, err := h.listPublicTeamMailboxShare(c.Request.Context(), tokenHash, email, provider)
	if err != nil {
		response.Error(c, http.StatusServiceUnavailable, "邮箱暂时无法读取，请稍后重试")
		return
	}
	code := ""
	for _, message := range messages {
		if message.Code != "" {
			code = message.Code
			break
		}
	}
	response.Success(c, teamMailboxShareCodeResponse{
		Email:       email,
		Status:      teamMailboxShareCodeStatus(code),
		Code:        code,
		CheckedAt:   checkedAt.UTC().Format(time.RFC3339),
		PollAfterMS: int(teamMailboxSharePollInterval / time.Millisecond),
	})
}

// ListPublicTeamChildMailboxShare returns every message currently available to
// the linked inbox, regardless of sender or subject. It is read-only and
// shares the same five-second provider cache as the compact code endpoint.
// GET /api/v1/public/team-mailbox/messages
func (h *OpenAIOAuthHandler) ListPublicTeamChildMailboxShare(c *gin.Context) {
	tokenHash, email, provider, ok := h.resolvePublicTeamMailboxShare(c)
	if !ok {
		return
	}
	messages, checkedAt, err := h.listPublicTeamMailboxShare(c.Request.Context(), tokenHash, email, provider)
	if err != nil {
		response.Error(c, http.StatusServiceUnavailable, "邮箱暂时无法读取，请稍后重试")
		return
	}
	response.Success(c, teamMailboxShareMessagesResponse{
		Email:       email,
		Messages:    messages,
		CheckedAt:   checkedAt.UTC().Format(time.RFC3339),
		PollAfterMS: int(teamMailboxSharePollInterval / time.Millisecond),
	})
}

// GetPublicTeamChildMailboxShareMessage reads one message selected from the
// already-scoped inbox. The arbitrary path ID is always verified against the
// current mailbox list before any provider detail request is sent.
// GET /api/v1/public/team-mailbox/messages/:message_id
func (h *OpenAIOAuthHandler) GetPublicTeamChildMailboxShareMessage(c *gin.Context) {
	tokenHash, email, provider, ok := h.resolvePublicTeamMailboxShare(c)
	if !ok {
		return
	}
	messageID := strings.TrimSpace(c.Param("message_id"))
	if messageID == "" || len(messageID) > 512 {
		response.NotFound(c, "邮件不存在")
		return
	}
	detail, err := h.readPublicTeamMailboxShareMessage(c.Request.Context(), tokenHash, email, provider, messageID)
	if errors.Is(err, errTeamMailboxShareMessageNotFound) {
		response.NotFound(c, "邮件不存在")
		return
	}
	if err != nil {
		response.Error(c, http.StatusServiceUnavailable, "邮件暂时无法读取，请稍后重试")
		return
	}
	response.Success(c, detail)
}

func (h *OpenAIOAuthHandler) resolvePublicTeamMailboxShare(c *gin.Context) (string, string, teamMailboxProviderConfig, bool) {
	setPublicTeamMailboxShareHeaders(c)
	token, shareID, err := publicTeamMailboxShareToken(c.GetHeader("Authorization"))
	if err != nil {
		response.NotFound(c, "接码链接不可用")
		return "", "", teamMailboxProviderConfig{}, false
	}
	if h == nil || h.teamMailboxStore == nil || h.adminService == nil {
		response.Error(c, http.StatusServiceUnavailable, "邮箱服务暂不可用，请稍后重试")
		return "", "", teamMailboxProviderConfig{}, false
	}
	record, found, err := h.ensureTeamMailboxShareRegistry().resolve(shareID, token)
	if err != nil {
		response.Error(c, http.StatusServiceUnavailable, "邮箱服务暂不可用，请稍后重试")
		return "", "", teamMailboxProviderConfig{}, false
	}
	if !found {
		response.NotFound(c, "接码链接不可用")
		return "", "", teamMailboxProviderConfig{}, false
	}
	// Once imported, a record is tied to that Team account. Account deletion or
	// a mismatched identity invalidates the link without exposing any identity.
	if record.AccountID > 0 {
		account, accountErr := h.adminService.GetAccount(c.Request.Context(), record.AccountID)
		accountEmail, valid := teamMailboxShareableEmail(account)
		if accountErr != nil || !valid || !strings.EqualFold(accountEmail, record.Email) {
			response.NotFound(c, "接码链接不可用")
			return "", "", teamMailboxProviderConfig{}, false
		}
	}
	provider, err := h.loadTeamMailboxProviderConfig(c)
	if err != nil {
		response.Error(c, http.StatusServiceUnavailable, "邮箱服务暂不可用，请稍后重试")
		return "", "", teamMailboxProviderConfig{}, false
	}
	return teamMailboxShareTokenDigest(token), record.Email, provider, true
}

func setPublicTeamMailboxShareHeaders(c *gin.Context) {
	if c == nil {
		return
	}
	c.Header("Cache-Control", "private, no-store, max-age=0")
	c.Header("Pragma", "no-cache")
	c.Header("Expires", "0")
	c.Header("Referrer-Policy", "no-referrer")
	c.Header("Vary", "Authorization")
	c.Header("Cross-Origin-Resource-Policy", "same-origin")
}

func newTeamMailboxShareToken() (shareID, token, tokenHash string, err error) {
	idBytes := make([]byte, teamMailboxShareIDBytes)
	secret := make([]byte, teamMailboxShareTokenEntropyBytes)
	if _, err = rand.Read(idBytes); err != nil {
		return "", "", "", err
	}
	if _, err = rand.Read(secret); err != nil {
		return "", "", "", err
	}
	shareID = base64.RawURLEncoding.EncodeToString(idBytes)
	token = teamMailboxShareTokenPrefix + "." + shareID + "." + base64.RawURLEncoding.EncodeToString(secret)
	return shareID, token, teamMailboxShareTokenDigest(token), nil
}

func publicTeamMailboxShareToken(authorization string) (string, string, error) {
	fields := strings.Fields(strings.TrimSpace(authorization))
	if len(fields) != 2 || !strings.EqualFold(fields[0], "Bearer") {
		return "", "", errors.New("missing bearer token")
	}
	token := strings.TrimSpace(fields[1])
	parts := strings.Split(token, ".")
	if len(parts) != 3 || parts[0] != teamMailboxShareTokenPrefix || !validTeamMailboxShareID(parts[1]) || parts[2] == "" {
		return "", "", errors.New("share token is malformed")
	}
	secret, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || len(secret) != teamMailboxShareTokenEntropyBytes {
		return "", "", errors.New("share token entropy is invalid")
	}
	return token, parts[1], nil
}

func validTeamMailboxShareID(value string) bool {
	decoded, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(value))
	return err == nil && len(decoded) == teamMailboxShareIDBytes
}

func teamMailboxShareTokenDigest(token string) string {
	digest := sha256.Sum256([]byte(token))
	return base64.RawURLEncoding.EncodeToString(digest[:])
}

func (h *OpenAIOAuthHandler) listPublicTeamMailboxShare(ctx context.Context, tokenHash, email string, provider teamMailboxProviderConfig) ([]teamMailboxShareMessageResponse, time.Time, error) {
	store := h.ensureTeamMailboxShareStore()
	if store == nil {
		return nil, time.Time{}, errors.New("mailbox share store is unavailable")
	}
	state := store.stateFor(tokenHash, email)
	if state == nil {
		return nil, time.Time{}, errors.New("mailbox share state is unavailable")
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	now := store.now()
	state.lastAccessedAt = now
	if err := h.ensurePublicTeamMailboxShareSession(ctx, state, provider, email, now); err != nil {
		return nil, time.Time{}, err
	}
	if state.lastMessagesCheckedAt.IsZero() || now.Sub(state.lastMessagesCheckedAt) >= teamMailboxSharePollInterval {
		if err := h.refreshPublicTeamMailboxShareMessages(ctx, state, now); err != nil {
			return nil, time.Time{}, err
		}
	}
	return teamMailboxShareSummaries(state.messages), state.lastMessagesCheckedAt, nil
}

func (h *OpenAIOAuthHandler) readPublicTeamMailboxShareMessage(ctx context.Context, tokenHash, email string, provider teamMailboxProviderConfig, messageID string) (teamMailboxShareMessageDetailResponse, error) {
	store := h.ensureTeamMailboxShareStore()
	if store == nil {
		return teamMailboxShareMessageDetailResponse{}, errors.New("mailbox share store is unavailable")
	}
	state := store.stateFor(tokenHash, email)
	if state == nil {
		return teamMailboxShareMessageDetailResponse{}, errors.New("mailbox share state is unavailable")
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	now := store.now()
	state.lastAccessedAt = now
	if err := h.ensurePublicTeamMailboxShareSession(ctx, state, provider, email, now); err != nil {
		return teamMailboxShareMessageDetailResponse{}, err
	}
	if state.lastMessagesCheckedAt.IsZero() || now.Sub(state.lastMessagesCheckedAt) >= teamMailboxSharePollInterval {
		if err := h.refreshPublicTeamMailboxShareMessages(ctx, state, now); err != nil {
			return teamMailboxShareMessageDetailResponse{}, err
		}
	}
	record, found := teamMailboxShareFindMessage(state.messages, messageID)
	if !found {
		// A message can arrive between the browser's list render and its click.
		// Perform exactly one forced scoped refresh before reporting it absent.
		if err := h.refreshPublicTeamMailboxShareMessages(ctx, state, now); err != nil {
			return teamMailboxShareMessageDetailResponse{}, err
		}
		record, found = teamMailboxShareFindMessage(state.messages, messageID)
		if !found {
			return teamMailboxShareMessageDetailResponse{}, errTeamMailboxShareMessageNotFound
		}
	}
	if cached, ok := state.details[messageID]; ok {
		return cached, nil
	}
	detailRaw, detailErr := h.fetchTeamMailboxMessage(ctx, state.session, messageID)
	if detailErr != nil {
		// Some provider implementations return complete bodies in the list but
		// do not expose a detail endpoint. Falling back remains scoped to the
		// already verified list record and never exposes a provider response.
		detailRaw = record.raw
	}
	if !teamMailboxMessageMatchesAddress(detailRaw, email) {
		return teamMailboxShareMessageDetailResponse{}, errTeamMailboxShareMessageNotFound
	}
	detail := teamMailboxShareMessageDetail(detailRaw, record.summary)
	state.details[messageID] = detail
	return detail, nil
}

func (h *OpenAIOAuthHandler) ensurePublicTeamMailboxShareSession(ctx context.Context, state *openAITeamMailboxShareState, provider teamMailboxProviderConfig, email string, now time.Time) error {
	if state.session.email == email && state.session.expiresAt.After(now) {
		return nil
	}
	mailboxEmail, mailboxToken, err := h.createTeamChildMailbox(ctx, provider, email)
	if err != nil {
		return err
	}
	state.session = openAITeamMailboxSession{
		email:     strings.ToLower(strings.TrimSpace(mailboxEmail)),
		token:     mailboxToken,
		config:    provider,
		expiresAt: now.Add(teamMailboxSessionTTL),
	}
	return nil
}

func (h *OpenAIOAuthHandler) refreshPublicTeamMailboxShareMessages(ctx context.Context, state *openAITeamMailboxShareState, now time.Time) error {
	messages, err := h.fetchTeamMailboxShareMessages(ctx, state.session)
	if err != nil {
		return err
	}
	state.messages = messages
	state.details = make(map[string]teamMailboxShareMessageDetailResponse)
	state.lastMessagesCheckedAt = now
	return nil
}

func (h *OpenAIOAuthHandler) fetchTeamMailboxShareMessages(ctx context.Context, session openAITeamMailboxSession) ([]teamMailboxShareMessageRecord, error) {
	endpoint := teamMailboxURL(session.config.baseURL, session.config.messagesPath)
	query := endpoint.Query()
	query.Set("limit", strconv.Itoa(teamMailboxShareMessageLimit))
	query.Set("offset", "0")
	query.Set("_xiass_poll", strconv.FormatInt(time.Now().UnixNano(), 10))
	if session.config.authMode == "query-key" && session.config.apiKey != "" {
		query.Set("key", session.config.apiKey)
	}
	endpoint.RawQuery = query.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, err
	}
	applyTeamMailboxFreshReadHeaders(request)
	applyTeamMailboxReadAuth(request, session)
	body, status, err := h.doTeamMailboxRequest(request)
	if err != nil {
		return nil, err
	}
	if status < http.StatusOK || status >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("mailbox list request failed with HTTP %d", status)
	}
	data, err := decodeTeamMailboxObject(body)
	if err != nil {
		return nil, err
	}
	return teamMailboxShareMessageRecords(teamMailboxMessageList(data), session.email), nil
}

func teamMailboxShareMessageRecords(messages []map[string]any, email string) []teamMailboxShareMessageRecord {
	seen := make(map[string]struct{}, len(messages))
	result := make([]teamMailboxShareMessageRecord, 0, len(messages))
	for _, message := range messages {
		if !teamMailboxMessageMatchesAddress(message, email) {
			continue
		}
		summary, ok := teamMailboxShareMessageSummary(message)
		if !ok {
			continue
		}
		if _, duplicated := seen[summary.ID]; duplicated {
			continue
		}
		seen[summary.ID] = struct{}{}
		result = append(result, teamMailboxShareMessageRecord{summary: summary, raw: message})
	}
	return result
}

func teamMailboxShareMessageSummary(message map[string]any) (teamMailboxShareMessageResponse, bool) {
	messageID := teamMailboxShareMessageID(message)
	if messageID == "" {
		return teamMailboxShareMessageResponse{}, false
	}
	body := teamMailboxShareReadableBody(message)
	subject := teamMailboxDecodeMIMEHeader(teamMailboxShareMessageField(message, "subject", "title", "headline"))
	if subject == "" {
		subject = "无主题邮件"
	}
	return teamMailboxShareMessageResponse{
		ID:         messageID,
		From:       teamMailboxDecodeMIMEHeader(teamMailboxShareMessageSender(message)),
		Subject:    subject,
		Preview:    teamMailboxSharePreview(body),
		ReceivedAt: teamMailboxShareMessageField(message, "received_at", "receivedAt", "created_at", "createdAt", "date", "timestamp", "time"),
		Code:       extractTeamMailboxVerificationCodeFromMessage(message),
	}, true
}

func teamMailboxShareMessageDetail(message map[string]any, fallback teamMailboxShareMessageResponse) teamMailboxShareMessageDetailResponse {
	summary, ok := teamMailboxShareMessageSummary(message)
	if !ok {
		summary = fallback
	} else {
		if summary.From == "" {
			summary.From = fallback.From
		}
		if summary.Subject == "无主题邮件" && fallback.Subject != "" {
			summary.Subject = fallback.Subject
		}
		if summary.ReceivedAt == "" {
			summary.ReceivedAt = fallback.ReceivedAt
		}
		if summary.Code == "" {
			summary.Code = fallback.Code
		}
	}
	body, html := teamMailboxShareMessageContent(message)
	if body == "" {
		body = fallback.Preview
	}
	summary.Preview = teamMailboxSharePreview(body)
	return teamMailboxShareMessageDetailResponse{teamMailboxShareMessageResponse: summary, Body: body, HTML: html}
}

func teamMailboxShareMessageID(message map[string]any) string {
	for _, key := range []string{"id", "msgid", "message_id", "messageId", "uid"} {
		value := strings.TrimSpace(teamMailboxString(message[key]))
		if value != "" && len(value) <= 512 {
			return value
		}
	}
	return ""
}

func teamMailboxShareMessageField(message map[string]any, keys ...string) string {
	for _, key := range keys {
		value := strings.TrimSpace(teamMailboxString(message[key]))
		if value != "" {
			return value
		}
	}
	for _, containerKey := range []string{"headers", "meta", "envelope"} {
		container, ok := message[containerKey].(map[string]any)
		if !ok {
			continue
		}
		for _, key := range keys {
			value := strings.TrimSpace(teamMailboxString(container[key]))
			if value != "" {
				return value
			}
		}
	}
	return ""
}

func teamMailboxShareMessageSender(message map[string]any) string {
	for _, key := range []string{"from", "sender", "from_email", "sender_email", "author"} {
		if value := teamMailboxShareContact(message[key]); value != "" {
			return value
		}
	}
	for _, containerKey := range []string{"headers", "meta", "envelope"} {
		container, ok := message[containerKey].(map[string]any)
		if !ok {
			continue
		}
		for _, key := range []string{"from", "sender"} {
			if value := teamMailboxShareContact(container[key]); value != "" {
				return value
			}
		}
	}
	return ""
}

func teamMailboxShareContact(value any) string {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case map[string]any:
		name := strings.TrimSpace(teamMailboxString(typed["name"]))
		address := strings.TrimSpace(teamMailboxString(typed["address"]))
		if address == "" {
			address = strings.TrimSpace(teamMailboxString(typed["email"]))
		}
		if name != "" && address != "" {
			return name + " <" + address + ">"
		}
		if name != "" {
			return name
		}
		return address
	case []any:
		for _, item := range typed {
			if value := teamMailboxShareContact(item); value != "" {
				return value
			}
		}
	}
	return ""
}

func teamMailboxShareReadableBody(message map[string]any) string {
	body, _ := teamMailboxShareMessageContent(message)
	return body
}

func teamMailboxShareMessageContent(message map[string]any) (string, string) {
	textParts := make([]string, 0, 8)
	htmlParts := make([]string, 0, 4)
	for _, key := range teamMailboxMIMEContentKeys {
		appendTeamMailboxShareContent(&textParts, &htmlParts, message[key], 0)
	}
	body := strings.TrimSpace(strings.Join(uniqueTeamMailboxShareStrings(textParts), "\n\n"))
	html := strings.TrimSpace(strings.Join(uniqueTeamMailboxMIMEStrings(htmlParts), "\n"))
	if body == "" && html != "" {
		body = strings.TrimSpace(teamMailboxTextFromHTML(html))
	}
	return body, html
}

func appendTeamMailboxShareContent(textParts, htmlParts *[]string, value any, depth int) {
	if depth > teamMailboxMIMEMaxDepth || value == nil || textParts == nil || htmlParts == nil {
		return
	}
	switch typed := value.(type) {
	case string:
		decoded := teamMailboxDecodeMIMEContent(typed)
		if decoded.Recognized {
			if decoded.Text != "" {
				*textParts = append(*textParts, decoded.Text)
			}
			if decoded.HTML != "" {
				*htmlParts = append(*htmlParts, decoded.HTML)
			}
			return
		}
		unescaped := strings.TrimSpace(stdhtml.UnescapeString(typed))
		if teamMailboxLooksLikeHTML(unescaped) {
			if html := teamMailboxSanitizeHTML(unescaped); html != "" {
				*htmlParts = append(*htmlParts, html)
				return
			}
		}
		cleaned := strings.TrimSpace(teamMailboxTextFromHTML(unescaped))
		if cleaned != "" {
			*textParts = append(*textParts, cleaned)
		}
	case []any:
		for _, item := range typed {
			appendTeamMailboxShareContent(textParts, htmlParts, item, depth+1)
		}
	case map[string]any:
		for _, key := range teamMailboxMIMEContentKeys {
			if nested, ok := typed[key]; ok {
				appendTeamMailboxShareContent(textParts, htmlParts, nested, depth+1)
			}
		}
	}
}

func uniqueTeamMailboxShareStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func teamMailboxSharePreview(value string) string {
	const maxRunes = 240
	value = teamMailboxSharePreviewText(value)
	if len([]rune(value)) <= maxRunes {
		return value
	}
	return string([]rune(value)[:maxRunes]) + "..."
}

// teamMailboxSharePreviewText keeps inbox rows useful when a provider exposes
// a partially flattened HTML body. Some providers remove the style element's
// tags but leave the stylesheet text in their list response. That text is not
// part of the email preview and can otherwise hide the actual mail content.
func teamMailboxSharePreviewText(value string) string {
	value = strings.TrimSpace(teamMailboxTextFromHTML(value))
	lower := strings.ToLower(value)
	cutoff := len(value)
	for _, marker := range []string{
		"google webfonts", "@font-face", "@media", "@supports", "@keyframes",
		"font-family:", "font-family :", "src: url(", "src:url(",
	} {
		if index := strings.Index(lower, marker); index >= 0 && index < cutoff {
			cutoff = index
		}
	}
	if cutoff < len(value) {
		value = strings.TrimRight(value[:cutoff], " \t\r\n/*")
	}
	return strings.Join(strings.Fields(value), " ")
}

func teamMailboxShareFindMessage(messages []teamMailboxShareMessageRecord, messageID string) (teamMailboxShareMessageRecord, bool) {
	for _, message := range messages {
		if message.summary.ID == messageID {
			return message, true
		}
	}
	return teamMailboxShareMessageRecord{}, false
}

func teamMailboxShareSummaries(records []teamMailboxShareMessageRecord) []teamMailboxShareMessageResponse {
	result := make([]teamMailboxShareMessageResponse, len(records))
	for index, record := range records {
		result[index] = record.summary
	}
	return result
}

func teamMailboxShareCodeStatus(code string) string {
	if strings.TrimSpace(code) != "" {
		return "received"
	}
	return "waiting"
}
