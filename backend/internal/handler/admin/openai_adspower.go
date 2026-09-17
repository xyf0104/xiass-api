package admin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
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

const (
	openAIAdsPowerLaunchTTL       = 5 * time.Minute
	openAIAdsPowerBindingTTL      = 15 * time.Minute
	openAIAdsPowerPendingTTL      = 45 * time.Minute
	openAIAdsPowerDefaultLoopback = "http://127.0.0.1:34987/launch"

	openAIAdsPowerLaunchRedisPrefix   = "xiass:openai:adspower:launch:"
	openAIAdsPowerBindingRedisPrefix  = "xiass:openai:adspower:binding:"
	openAIAdsPowerCallbackRedisPrefix = "xiass:openai:adspower:callback:"
	openAIAdsPowerPendingRedisPrefix  = "xiass:openai:adspower:pending:"
	openAIAdsPowerPairingRedisPrefix  = "xiass:openai:adspower:pairing:"
	openAIAdsPowerDeviceRedisPrefix   = "xiass:openai:adspower:device:"
	openAIAdsPowerDefaultRedisPrefix  = "xiass:openai:adspower:default:"
	openAIAdsPowerCommandRedisPrefix  = "xiass:openai:adspower:commands:"
)

type openAIAdsPowerBindingUpdater interface {
	UpdateOpenAIAdsPowerBinding(ctx context.Context, id int64, binding *service.OpenAIAdsPowerBinding) error
	UpdateOpenAIAdsPowerBindingFromLaunch(ctx context.Context, id int64, expectedExecutionNodeID string, binding *service.OpenAIAdsPowerBinding) error
}

type openAIAdsPowerLaunchStore struct {
	mu        sync.Mutex
	launches  map[string]openAIAdsPowerLaunchRecord
	bindings  map[string]openAIAdsPowerLaunchRecord
	callbacks map[string]openAIAdsPowerLaunchRecord
	pending   map[string]openAIAdsPowerPendingBinding
	pairings  map[string]openAIAdsPowerHelperPairing
	devices   map[string]openAIAdsPowerHelperDevice
	defaults  map[string]string
	commands  map[string][]string
	redis     *redisclient.Client
	now       func() time.Time
}

type openAIAdsPowerPendingBinding struct {
	AdminUserID int64                         `json:"admin_user_id"`
	Binding     service.OpenAIAdsPowerBinding `json:"binding"`
	ExpiresAt   time.Time                     `json:"expires_at,omitempty"`
}

type openAIAdsPowerLaunchRecord struct {
	AdminUserID        int64                          `json:"admin_user_id"`
	AccountID          int64                          `json:"account_id,omitempty"`
	AccountName        string                         `json:"account_name,omitempty"`
	SessionID          string                         `json:"session_id"`
	AuthURL            string                         `json:"auth_url"`
	EnvironmentKey     string                         `json:"environment_key"`
	Existing           *service.OpenAIAdsPowerBinding `json:"existing_binding,omitempty"`
	BindingToken       string                         `json:"binding_token"`
	CallbackToken      string                         `json:"callback_token,omitempty"`
	TaskID             string                         `json:"task_id,omitempty"`
	TaskMode           string                         `json:"task_mode,omitempty"`
	LoginEmail         string                         `json:"login_email,omitempty"`
	LoginMethod        string                         `json:"login_method,omitempty"`
	PasswordEncrypted  string                         `json:"password_encrypted,omitempty"`
	TOTPEncrypted      string                         `json:"totp_encrypted,omitempty"`
	EmailCodeEncrypted string                         `json:"email_code_encrypted,omitempty"`
	ExpiresAt          time.Time                      `json:"expires_at"`
	CallbackExpiresAt  time.Time                      `json:"callback_expires_at,omitempty"`
}

type openAIAdsPowerLaunchRequest struct {
	AccountID    int64  `json:"account_id"`
	SessionID    string `json:"session_id"`
	AuthURL      string `json:"auth_url"`
	ProfileLabel string `json:"profile_label"`
}

type openAIAdsPowerBindingClaimRequest struct {
	SessionID string `json:"session_id"`
}

type openAIAdsPowerLaunchResponse struct {
	HelperURL string    `json:"helper_url"`
	ExpiresAt time.Time `json:"expires_at"`
	Delivery  string    `json:"delivery"`
}

type openAIAdsPowerRedeemRequest struct {
	Ticket string `json:"ticket"`
}

type openAIAdsPowerRedeemResponse struct {
	AccountID         int64                          `json:"account_id,omitempty"`
	AccountName       string                         `json:"account_name,omitempty"`
	SessionID         string                         `json:"session_id"`
	AuthURL           string                         `json:"auth_url"`
	EnvironmentKey    string                         `json:"environment_key"`
	Existing          *service.OpenAIAdsPowerBinding `json:"existing_binding,omitempty"`
	BindingToken      string                         `json:"binding_token"`
	CallbackToken     string                         `json:"callback_token,omitempty"`
	LoginEmail        string                         `json:"login_email,omitempty"`
	LoginMethod       string                         `json:"login_method,omitempty"`
	Password          string                         `json:"password,omitempty"`
	TOTPSecret        string                         `json:"totp_secret,omitempty"`
	EmailCodeToken    string                         `json:"email_code_token,omitempty"`
	WorkflowMode      string                         `json:"workflow_mode,omitempty"`
	ExpiresAt         time.Time                      `json:"expires_at"`
	CallbackExpiresAt time.Time                      `json:"callback_expires_at,omitempty"`
}

type openAIAdsPowerBindingReport struct {
	BindingToken          string `json:"binding_token"`
	DeviceID              string `json:"device_id"`
	ProfileID             string `json:"profile_id"`
	ProfileNo             string `json:"profile_no"`
	ProfileName           string `json:"profile_name"`
	EnvironmentKey        string `json:"environment_key"`
	ProxyType             string `json:"proxy_type"`
	ProxyHost             string `json:"proxy_host"`
	ProxyPort             string `json:"proxy_port"`
	ProxyExitIP           string `json:"proxy_exit_ip"`
	WebRTCDisabled        bool   `json:"webrtc_disabled"`
	FingerprintRandomized bool   `json:"fingerprint_randomized"`
	FingerprintSlot       int    `json:"fingerprint_slot,omitempty"`
}

type openAIAdsPowerCallbackReport struct {
	CallbackToken string `json:"callback_token"`
	CallbackURL   string `json:"callback_url"`
}

type openAIAdsPowerProgressReport struct {
	CallbackToken string `json:"callback_token"`
	Status        string `json:"status"`
	Stage         string `json:"stage"`
	Reason        string `json:"reason,omitempty"`
}

type openAIAdsPowerSMSActionRequest struct {
	CallbackToken string `json:"callback_token"`
	Action        string `json:"action"`
}

type openAIAdsPowerSMSActionResponse struct {
	Status    string     `json:"status"`
	Number    string     `json:"number,omitempty"`
	Code      string     `json:"code,omitempty"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
}

func newOpenAIAdsPowerLaunchStore() *openAIAdsPowerLaunchStore {
	return &openAIAdsPowerLaunchStore{
		launches:  make(map[string]openAIAdsPowerLaunchRecord),
		bindings:  make(map[string]openAIAdsPowerLaunchRecord),
		callbacks: make(map[string]openAIAdsPowerLaunchRecord),
		pending:   make(map[string]openAIAdsPowerPendingBinding),
		pairings:  make(map[string]openAIAdsPowerHelperPairing),
		devices:   make(map[string]openAIAdsPowerHelperDevice),
		defaults:  make(map[string]string),
		commands:  make(map[string][]string),
		now:       time.Now,
	}
}

func (s *openAIAdsPowerLaunchStore) configureRedis(client *redisclient.Client) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.redis = client
	s.mu.Unlock()
}

func (s *openAIAdsPowerLaunchStore) redisClient() *redisclient.Client {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.redis
}

func (s *openAIAdsPowerLaunchStore) create(ctx context.Context, ticket string, record openAIAdsPowerLaunchRecord) error {
	if s == nil || ticket == "" || record.BindingToken == "" || record.SessionID == "" || record.ExpiresAt.IsZero() {
		return errors.New("AdsPower launch record is invalid")
	}
	if record.CallbackToken != "" && (record.TaskID == "" || record.TaskMode == "" || record.CallbackExpiresAt.IsZero()) {
		return errors.New("AdsPower callback record is invalid")
	}
	if client := s.redisClient(); client != nil {
		payload, err := json.Marshal(record)
		if err != nil {
			return err
		}
		launchTTL := time.Until(record.ExpiresAt)
		if launchTTL <= 0 {
			return errors.New("AdsPower launch record has expired")
		}
		bindingTTL := launchTTL + openAIAdsPowerBindingTTL
		_, err = client.TxPipelined(ctx, func(pipe redisclient.Pipeliner) error {
			pipe.Set(ctx, openAIAdsPowerLaunchRedisPrefix+ticket, payload, launchTTL)
			pipe.Set(ctx, openAIAdsPowerBindingRedisPrefix+record.BindingToken, payload, bindingTTL)
			if record.CallbackToken != "" {
				callbackTTL := time.Until(record.CallbackExpiresAt)
				if callbackTTL <= 0 {
					return errors.New("AdsPower callback record has expired")
				}
				pipe.Set(ctx, openAIAdsPowerCallbackRedisPrefix+record.CallbackToken, payload, callbackTTL)
			}
			return nil
		})
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneLocked(s.now())
	s.launches[ticket] = record
	s.bindings[record.BindingToken] = record
	if record.CallbackToken != "" {
		s.callbacks[record.CallbackToken] = record
	}
	return nil
}

func (s *openAIAdsPowerLaunchStore) consumeCallback(ctx context.Context, token string) (openAIAdsPowerLaunchRecord, bool, error) {
	if s == nil || token == "" {
		return openAIAdsPowerLaunchRecord{}, false, nil
	}
	if client := s.redisClient(); client != nil {
		payload, err := client.GetDel(ctx, openAIAdsPowerCallbackRedisPrefix+token).Bytes()
		if errors.Is(err, redisclient.Nil) {
			return openAIAdsPowerLaunchRecord{}, false, nil
		}
		if err != nil {
			return openAIAdsPowerLaunchRecord{}, false, err
		}
		var record openAIAdsPowerLaunchRecord
		if json.Unmarshal(payload, &record) != nil || !record.CallbackExpiresAt.After(s.now()) {
			return openAIAdsPowerLaunchRecord{}, false, nil
		}
		return record, true, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneLocked(s.now())
	record, ok := s.callbacks[token]
	delete(s.callbacks, token)
	return record, ok, nil
}

func (s *openAIAdsPowerLaunchStore) callbackRecord(ctx context.Context, token string) (openAIAdsPowerLaunchRecord, bool, error) {
	if s == nil || token == "" {
		return openAIAdsPowerLaunchRecord{}, false, nil
	}
	if client := s.redisClient(); client != nil {
		payload, err := client.Get(ctx, openAIAdsPowerCallbackRedisPrefix+token).Bytes()
		if errors.Is(err, redisclient.Nil) {
			return openAIAdsPowerLaunchRecord{}, false, nil
		}
		if err != nil {
			return openAIAdsPowerLaunchRecord{}, false, err
		}
		var record openAIAdsPowerLaunchRecord
		if json.Unmarshal(payload, &record) != nil || !record.CallbackExpiresAt.After(s.now()) {
			return openAIAdsPowerLaunchRecord{}, false, nil
		}
		return record, true, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneLocked(s.now())
	record, ok := s.callbacks[token]
	return record, ok, nil
}

func (s *openAIAdsPowerLaunchStore) consumeLaunch(ctx context.Context, ticket string) (openAIAdsPowerLaunchRecord, bool, error) {
	if s == nil || ticket == "" {
		return openAIAdsPowerLaunchRecord{}, false, nil
	}
	if client := s.redisClient(); client != nil {
		payload, err := client.GetDel(ctx, openAIAdsPowerLaunchRedisPrefix+ticket).Bytes()
		if errors.Is(err, redisclient.Nil) {
			return openAIAdsPowerLaunchRecord{}, false, nil
		}
		if err != nil {
			return openAIAdsPowerLaunchRecord{}, false, err
		}
		var record openAIAdsPowerLaunchRecord
		if json.Unmarshal(payload, &record) != nil || !record.ExpiresAt.After(s.now()) {
			return openAIAdsPowerLaunchRecord{}, false, nil
		}
		return record, true, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneLocked(s.now())
	record, ok := s.launches[ticket]
	delete(s.launches, ticket)
	return record, ok, nil
}

func (s *openAIAdsPowerLaunchStore) consumeBinding(ctx context.Context, token string) (openAIAdsPowerLaunchRecord, bool, error) {
	if s == nil || token == "" {
		return openAIAdsPowerLaunchRecord{}, false, nil
	}
	if client := s.redisClient(); client != nil {
		payload, err := client.GetDel(ctx, openAIAdsPowerBindingRedisPrefix+token).Bytes()
		if errors.Is(err, redisclient.Nil) {
			return openAIAdsPowerLaunchRecord{}, false, nil
		}
		if err != nil {
			return openAIAdsPowerLaunchRecord{}, false, err
		}
		var record openAIAdsPowerLaunchRecord
		if json.Unmarshal(payload, &record) != nil {
			return openAIAdsPowerLaunchRecord{}, false, nil
		}
		return record, true, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneLocked(s.now())
	record, ok := s.bindings[token]
	delete(s.bindings, token)
	return record, ok, nil
}

func (s *openAIAdsPowerLaunchStore) savePending(ctx context.Context, sessionID string, adminUserID int64, binding service.OpenAIAdsPowerBinding) error {
	if s == nil || sessionID == "" || adminUserID <= 0 {
		return errors.New("AdsPower pending binding is invalid")
	}
	pending := openAIAdsPowerPendingBinding{AdminUserID: adminUserID, Binding: binding}
	if client := s.redisClient(); client != nil {
		payload, err := json.Marshal(pending)
		if err != nil {
			return err
		}
		return client.Set(ctx, openAIAdsPowerPendingRedisPrefix+sessionID, payload, openAIAdsPowerPendingTTL).Err()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	pending.ExpiresAt = s.now().Add(openAIAdsPowerPendingTTL)
	s.pending[sessionID] = pending
	return nil
}

func (s *openAIAdsPowerLaunchStore) pendingBinding(ctx context.Context, sessionID string, adminUserID int64) (*service.OpenAIAdsPowerBinding, error) {
	if s == nil || sessionID == "" {
		return nil, nil
	}
	if client := s.redisClient(); client != nil {
		payload, err := client.Get(ctx, openAIAdsPowerPendingRedisPrefix+sessionID).Bytes()
		if errors.Is(err, redisclient.Nil) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		var pending openAIAdsPowerPendingBinding
		if json.Unmarshal(payload, &pending) != nil || pending.AdminUserID <= 0 {
			return nil, nil
		}
		if adminUserID <= 0 || pending.AdminUserID != adminUserID {
			return nil, errors.New("AdsPower pending binding belongs to another administrator")
		}
		binding := pending.Binding
		binding.Normalize()
		if service.ParseOpenAIAdsPowerBinding(binding) == nil {
			return nil, nil
		}
		return &binding, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneLocked(s.now())
	pending, ok := s.pending[sessionID]
	if !ok {
		return nil, nil
	}
	if adminUserID <= 0 || pending.AdminUserID != adminUserID {
		return nil, errors.New("AdsPower pending binding belongs to another administrator")
	}
	copy := pending.Binding
	return &copy, nil
}

func (s *openAIAdsPowerLaunchStore) deletePending(ctx context.Context, sessionID string) {
	if s == nil || sessionID == "" {
		return
	}
	if client := s.redisClient(); client != nil {
		_ = client.Del(ctx, openAIAdsPowerPendingRedisPrefix+sessionID).Err()
		return
	}
	s.mu.Lock()
	delete(s.pending, sessionID)
	s.mu.Unlock()
}

func (s *openAIAdsPowerLaunchStore) pruneLocked(now time.Time) {
	for key, record := range s.launches {
		if !record.ExpiresAt.After(now) {
			delete(s.launches, key)
		}
	}
	for key, record := range s.bindings {
		if !record.ExpiresAt.Add(openAIAdsPowerBindingTTL).After(now) {
			delete(s.bindings, key)
		}
	}
	for key, record := range s.callbacks {
		if !record.CallbackExpiresAt.After(now) {
			delete(s.callbacks, key)
		}
	}
	for key, pending := range s.pending {
		if !pending.ExpiresAt.After(now) {
			delete(s.pending, key)
		}
	}
}

func (h *OpenAIOAuthHandler) issueOpenAIAdsPowerLaunch(ctx context.Context, c *gin.Context, record openAIAdsPowerLaunchRecord) (*openAIAdsPowerLaunchResponse, error) {
	ticket, err := newTeamChildBrowserToken()
	if err != nil {
		return nil, errors.New("AdsPower launch ticket could not be created")
	}
	record.BindingToken, err = newTeamChildBrowserToken()
	if err != nil {
		return nil, errors.New("AdsPower binding ticket could not be created")
	}
	if record.TaskID != "" {
		record.CallbackToken, err = newTeamChildBrowserToken()
		if err != nil {
			return nil, errors.New("AdsPower callback ticket could not be created")
		}
	}
	record.ExpiresAt = h.adsPowerLaunchStore.now().UTC().Add(openAIAdsPowerLaunchTTL)
	if err := h.adsPowerLaunchStore.create(ctx, ticket, record); err != nil {
		return nil, errors.New("AdsPower launch ticket could not be stored")
	}
	delivery := "local"
	if queued, queueErr := h.adsPowerLaunchStore.enqueueHelperCommand(ctx, record, ticket, openAIAdsPowerLocalEnvironment(c)); queueErr != nil {
		return nil, errors.New("AdsPower helper command could not be queued")
	} else if queued {
		delivery = "queued"
	}
	helperURL, err := openAIAdsPowerHelperURL(openAIAdsPowerRequestOrigin(c), ticket)
	if err != nil {
		return nil, errors.New("AdsPower helper URL is invalid")
	}
	return &openAIAdsPowerLaunchResponse{HelperURL: helperURL, ExpiresAt: record.ExpiresAt, Delivery: delivery}, nil
}

// CreateOpenAIAdsPowerLaunchTicket produces an authenticated, one-time bridge
// ticket. The authorization URL never appears in the loopback URL or browser
// history; only the local helper can redeem it from the originating server.
func (h *OpenAIOAuthHandler) CreateOpenAIAdsPowerLaunchTicket(c *gin.Context) {
	if h == nil || h.adsPowerLaunchStore == nil || h.adminService == nil {
		response.Error(c, http.StatusServiceUnavailable, "AdsPower launch service is unavailable")
		return
	}
	var req openAIAdsPowerLaunchRequest
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 16<<10)
	if c.ShouldBindJSON(&req) != nil {
		response.BadRequest(c, "Invalid AdsPower launch request")
		return
	}
	req.SessionID = strings.TrimSpace(req.SessionID)
	req.AuthURL = strings.TrimSpace(req.AuthURL)
	if !validOpenAIAdsPowerSessionID(req.SessionID) || !validOpenAIAdsPowerAuthURL(req.AuthURL) {
		response.BadRequest(c, "OpenAI authorization session is invalid")
		return
	}
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok || subject.UserID <= 0 {
		response.Unauthorized(c, "administrator session is required")
		return
	}

	record := openAIAdsPowerLaunchRecord{
		AdminUserID: subject.UserID,
		SessionID:   req.SessionID,
		AuthURL:     req.AuthURL,
		AccountName: sanitizeOpenAIAdsPowerLabel(req.ProfileLabel),
	}
	if req.AccountID > 0 {
		if err := ensureAdminAccountManagementAccess(c.Request.Context(), h.adminService, req.AccountID); err != nil {
			response.ErrorFrom(c, err)
			return
		}
		account, err := h.adminService.GetAccount(c.Request.Context(), req.AccountID)
		if err != nil || account == nil || !account.IsOpenAIOAuth() || account.IsCredentialShadow() || account.IsOpenAIOAuthCredentialCopy() {
			response.BadRequest(c, "Only primary OpenAI OAuth accounts can use AdsPower")
			return
		}
		record.AccountID = account.ID
		record.AccountName = sanitizeOpenAIAdsPowerLabel(account.Name)
		record.Existing = service.OpenAIAdsPowerBindingFromAccount(account)
		record.EnvironmentKey = openAIAdsPowerAccountEnvironment(account)
		if record.EnvironmentKey != openAIAdsPowerLocalEnvironment(c) {
			response.Error(c, http.StatusConflict, "Open this account from its assigned XIASS server before launching AdsPower")
			return
		}
	} else {
		record.EnvironmentKey = openAIAdsPowerLocalEnvironment(c)
	}
	if record.AccountName == "" {
		record.AccountName = "OpenAI OAuth"
	}

	result, err := h.issueOpenAIAdsPowerLaunch(c.Request.Context(), c, record)
	if err != nil {
		response.InternalError(c, err.Error())
		return
	}
	response.Success(c, result)
}

func (h *OpenAIOAuthHandler) LaunchBatchOAuthTaskInAdsPower(c *gin.Context) {
	h.launchBatchOAuthTaskInAdsPower(c, batchOAuthModeCreate)
}

func (h *OpenAIOAuthHandler) LaunchOpenAIReauthorizationTaskInAdsPower(c *gin.Context) {
	h.launchBatchOAuthTaskInAdsPower(c, batchOAuthModeReauthorization)
}

func (h *OpenAIOAuthHandler) launchBatchOAuthTaskInAdsPower(c *gin.Context, mode string) {
	task, ok := h.ownedBatchTask(c, mode)
	if !ok {
		return
	}
	defer task.mu.Unlock()
	if h.adsPowerLaunchStore == nil || !task.usesAdsPower() || task.terminal() || task.sessionID == "" || task.authURL == "" || task.state == "" {
		response.Error(c, http.StatusConflict, "This task is not waiting for an AdsPower browser")
		return
	}
	if task.adsPowerLaunchIssued {
		if task.adsPowerLaunchHelperURL != "" && task.adsPowerLaunchExpiresAt.After(time.Now()) {
			response.Success(c, openAIAdsPowerLaunchResponse{
				HelperURL: task.adsPowerLaunchHelperURL, Delivery: task.adsPowerLaunchDelivery,
				ExpiresAt: task.adsPowerLaunchExpiresAt,
			})
			return
		}
		if task.Stage != "external_browser" {
			response.Error(c, http.StatusConflict, "The AdsPower authorization is already in progress")
			return
		}
		task.adsPowerLaunchIssued = false
		task.adsPowerLaunchHelperURL = ""
		task.adsPowerLaunchDelivery = ""
		task.adsPowerLaunchExpiresAt = time.Time{}
	}
	record := openAIAdsPowerLaunchRecord{
		AdminUserID: task.ownerID, AccountName: sanitizeOpenAIAdsPowerLabel(task.Email), SessionID: task.sessionID,
		AuthURL: task.authURL, TaskID: task.ID, TaskMode: task.normalizedMode(), CallbackExpiresAt: task.ExpiresAt,
		LoginEmail: task.Email, LoginMethod: task.LoginMethod,
		PasswordEncrypted: task.loginPasswordEncrypted, TOTPEncrypted: task.loginTOTPEncrypted,
		EmailCodeEncrypted: task.loginEmailCodeEncrypted,
	}
	if mode == batchOAuthModeReauthorization {
		account, err := h.adminService.GetAccount(c.Request.Context(), task.TargetAccountID)
		if err != nil || account == nil || !account.IsOpenAIOAuth() || account.IsCredentialShadow() || account.IsOpenAIOAuthCredentialCopy() {
			response.Error(c, http.StatusConflict, "OpenAI account is unavailable")
			return
		}
		record.AccountID = account.ID
		record.AccountName = sanitizeOpenAIAdsPowerLabel(account.Name)
		record.Existing = service.OpenAIAdsPowerBindingFromAccount(account)
		record.EnvironmentKey = openAIAdsPowerAccountEnvironment(account)
	} else {
		record.EnvironmentKey = openAIAdsPowerLocalEnvironment(c)
		pending, err := h.adsPowerLaunchStore.pendingBinding(c.Request.Context(), task.sessionID, task.ownerID)
		if err != nil {
			response.Error(c, http.StatusServiceUnavailable, "AdsPower browser binding is temporarily unavailable")
			return
		}
		record.Existing = pending
	}
	result, err := h.issueOpenAIAdsPowerLaunch(c.Request.Context(), c, record)
	if err != nil {
		response.InternalError(c, err.Error())
		return
	}
	task.adsPowerLaunchIssued = true
	task.adsPowerLaunchHelperURL = result.HelperURL
	task.adsPowerLaunchDelivery = result.Delivery
	task.adsPowerLaunchExpiresAt = result.ExpiresAt
	task.snapshotJSONLocked()
	response.Success(c, result)
}

func (h *OpenAIOAuthHandler) RedeemOpenAIAdsPowerLaunchTicket(c *gin.Context) {
	if h == nil || h.adsPowerLaunchStore == nil {
		response.Error(c, http.StatusServiceUnavailable, "AdsPower launch service is unavailable")
		return
	}
	var req openAIAdsPowerRedeemRequest
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 4<<10)
	if c.ShouldBindJSON(&req) != nil {
		response.BadRequest(c, "Invalid AdsPower launch ticket")
		return
	}
	record, ok, err := h.adsPowerLaunchStore.consumeLaunch(c.Request.Context(), strings.TrimSpace(req.Ticket))
	if err != nil {
		response.InternalError(c, "AdsPower launch ticket could not be read")
		return
	}
	if !ok {
		response.Error(c, http.StatusGone, "AdsPower launch ticket has expired or was already used")
		return
	}
	var password, totpSecret, emailCodeToken string
	if record.TaskID != "" {
		if h.secretEncryptor == nil {
			response.Error(c, http.StatusServiceUnavailable, "AdsPower login automation is unavailable")
			return
		}
		var decryptErr error
		switch normalizeBatchOAuthLoginMethod(record.LoginMethod) {
		case batchOAuthLoginEmailCode:
			if record.EmailCodeEncrypted == "" {
				decryptErr = errors.New("saved email login is unavailable")
			} else {
				emailCodeToken, decryptErr = h.secretEncryptor.Decrypt(record.EmailCodeEncrypted)
			}
		default:
			if record.PasswordEncrypted == "" {
				decryptErr = errors.New("saved password is unavailable")
			} else {
				password, decryptErr = h.secretEncryptor.Decrypt(record.PasswordEncrypted)
			}
			if decryptErr == nil && record.TOTPEncrypted != "" {
				totpSecret, decryptErr = h.secretEncryptor.Decrypt(record.TOTPEncrypted)
			}
		}
		if decryptErr != nil {
			response.Error(c, http.StatusConflict, "Saved login credentials could not be read")
			return
		}
	}
	response.Success(c, openAIAdsPowerRedeemResponse{
		AccountID: record.AccountID, AccountName: record.AccountName, SessionID: record.SessionID,
		AuthURL: record.AuthURL, EnvironmentKey: record.EnvironmentKey, Existing: record.Existing,
		BindingToken: record.BindingToken, CallbackToken: record.CallbackToken, ExpiresAt: record.ExpiresAt.Add(openAIAdsPowerBindingTTL),
		CallbackExpiresAt: record.CallbackExpiresAt, LoginEmail: record.LoginEmail,
		LoginMethod: normalizeBatchOAuthLoginMethod(record.LoginMethod), Password: password, TOTPSecret: totpSecret,
		EmailCodeToken: emailCodeToken, WorkflowMode: record.TaskMode,
	})
}

func (h *OpenAIOAuthHandler) ReportOpenAIAdsPowerProgress(c *gin.Context) {
	if h == nil || h.adsPowerLaunchStore == nil || h.batchOAuthStore == nil {
		response.Error(c, http.StatusServiceUnavailable, "AdsPower progress service is unavailable")
		return
	}
	var req openAIAdsPowerProgressReport
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 8<<10)
	if c.ShouldBindJSON(&req) != nil {
		response.BadRequest(c, "Invalid AdsPower progress report")
		return
	}
	req.CallbackToken = strings.TrimSpace(req.CallbackToken)
	req.Stage = batchOAuthPublicStage(strings.TrimSpace(req.Stage))
	req.Reason = batchOAuthPublicReason(strings.TrimSpace(req.Reason))
	if req.Status != "running" && req.Status != "failed" && req.Status != "blocked" {
		response.BadRequest(c, "Invalid AdsPower progress status")
		return
	}
	record, ok, err := h.adsPowerLaunchStore.callbackRecord(c.Request.Context(), req.CallbackToken)
	if err != nil {
		response.InternalError(c, "AdsPower progress ticket could not be read")
		return
	}
	if !ok {
		response.Error(c, http.StatusGone, "AdsPower progress ticket has expired")
		return
	}
	h.batchOAuthStore.mu.Lock()
	task := h.batchOAuthStore.tasks[record.TaskID]
	h.batchOAuthStore.mu.Unlock()
	if task == nil {
		response.Error(c, http.StatusGone, "Authorization task is no longer available")
		return
	}
	task.mu.Lock()
	defer task.mu.Unlock()
	if task.ownerID != record.AdminUserID || !task.matchesMode(record.TaskMode) || !task.usesAdsPower() ||
		task.terminal() || task.sessionID != record.SessionID || task.authURL != record.AuthURL {
		response.Error(c, http.StatusConflict, "AdsPower progress does not match the active authorization task")
		return
	}
	task.Status, task.Stage, task.Reason = req.Status, req.Stage, req.Reason
	if req.Reason == "phone_rejected" && task.submittedPhone != "" {
		if task.rejectedPhones == nil {
			task.rejectedPhones = make(map[string]bool)
		}
		task.rejectedPhones[task.submittedPhone] = true
	}
	// AdsPower tasks are driven by the resident helper. The browser page is the
	// live confirmation surface, so the admin frontend must not issue a second
	// SMS-provider action for the same task.
	task.RequiresSMSConfirmation = false
	if req.Status != "running" {
		task.markFinishedIfTerminal()
	}
	task.snapshotJSONLocked()
	response.Success(c, gin.H{"accepted": true})
}

func (h *OpenAIOAuthHandler) OpenAIAdsPowerSMSAction(c *gin.Context) {
	if h == nil || h.adsPowerLaunchStore == nil || h.batchOAuthStore == nil || h.batchSMSService == nil {
		response.Error(c, http.StatusServiceUnavailable, "AdsPower SMS automation is unavailable")
		return
	}
	var req openAIAdsPowerSMSActionRequest
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 8<<10)
	if c.ShouldBindJSON(&req) != nil {
		response.BadRequest(c, "Invalid AdsPower SMS action")
		return
	}
	req.CallbackToken = strings.TrimSpace(req.CallbackToken)
	req.Action = strings.ToLower(strings.TrimSpace(req.Action))
	if req.Action != "acquire" && req.Action != "change" && req.Action != "check" && req.Action != "cancel" {
		response.BadRequest(c, "Invalid AdsPower SMS action")
		return
	}
	record, ok, err := h.adsPowerLaunchStore.callbackRecord(c.Request.Context(), req.CallbackToken)
	if err != nil {
		response.InternalError(c, "AdsPower SMS ticket could not be read")
		return
	}
	if !ok {
		response.Error(c, http.StatusGone, "AdsPower SMS ticket has expired")
		return
	}
	h.batchOAuthStore.mu.Lock()
	task := h.batchOAuthStore.tasks[record.TaskID]
	h.batchOAuthStore.mu.Unlock()
	if task == nil {
		response.Error(c, http.StatusGone, "Authorization task is no longer available")
		return
	}
	task.mu.Lock()
	defer task.mu.Unlock()
	if task.ownerID != record.AdminUserID || !task.matchesMode(record.TaskMode) || !task.usesAdsPower() ||
		task.terminal() || task.sessionID != record.SessionID || task.authURL != record.AuthURL {
		response.Error(c, http.StatusConflict, "AdsPower SMS action does not match the active authorization task")
		return
	}
	if req.Action != "cancel" {
		if task.Status != "running" || (task.Stage != "phone_required" && task.Stage != "sms_waiting") {
			response.Error(c, http.StatusConflict, "Authorization task is not at a phone verification node")
			return
		}
		if (req.Action == "acquire" || req.Action == "change") && task.Stage != "phone_required" {
			response.Error(c, http.StatusConflict, "Authorization task does not need a phone number")
			return
		}
		if req.Action == "check" && task.Stage != "sms_waiting" {
			response.Error(c, http.StatusConflict, "Authorization task is not waiting for an SMS code")
			return
		}
	}

	ctx, cancel := context.WithTimeout(context.WithoutCancel(c.Request.Context()), 35*time.Second)
	defer cancel()
	sessionID, err := h.batchSMSService.WorkflowSession(ctx, task.ownerID, task.ID)
	if err != nil {
		response.Error(c, http.StatusBadGateway, "SMS reservation is unavailable")
		return
	}
	if sessionID == "" && (req.Action == "check" || req.Action == "cancel") {
		response.Success(c, openAIAdsPowerSMSActionResponse{Status: "WAITING"})
		return
	}
	if req.Action == "change" && sessionID == "" {
		response.Error(c, http.StatusConflict, "There is no submitted SMS reservation to replace")
		return
	}
	result, err := h.batchSMSService.WorkflowAction(ctx, task.ownerID, task.ID, sessionID, req.Action, true)
	if err != nil || result == nil {
		response.Error(c, http.StatusBadGateway, "SMS action failed; reservation retained for retry")
		return
	}
	phone := strings.TrimSpace(result.Number)
	if phone != "" {
		if !strings.HasPrefix(phone, "+") {
			phone = "+" + phone
		}
		if !teamChildWorkflowPhonePattern.MatchString(phone) {
			response.Error(c, http.StatusBadGateway, "SMS provider returned an invalid phone number")
			return
		}
		if task.rejectedPhones[phone] || (task.Reason == "phone_rejected" && req.Action != "change") {
			response.Error(c, http.StatusConflict, "SMS provider returned a previously rejected phone number")
			return
		}
		task.submittedPhone = phone
		task.Reason = ""
	}
	if req.Action == "cancel" {
		task.submittedPhone = ""
	}
	task.snapshotJSONLocked()
	response.Success(c, openAIAdsPowerSMSActionResponse{
		Status: result.Status, Number: phone, Code: strings.TrimSpace(result.Code), ExpiresAt: result.ExpiresAt,
	})
}

func (h *OpenAIOAuthHandler) ReportOpenAIAdsPowerCallback(c *gin.Context) {
	if h == nil || h.adsPowerLaunchStore == nil || h.batchOAuthStore == nil {
		response.Error(c, http.StatusServiceUnavailable, "AdsPower callback service is unavailable")
		return
	}
	var req openAIAdsPowerCallbackReport
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 16<<10)
	if c.ShouldBindJSON(&req) != nil {
		response.BadRequest(c, "Invalid AdsPower callback report")
		return
	}
	record, ok, err := h.adsPowerLaunchStore.consumeCallback(c.Request.Context(), strings.TrimSpace(req.CallbackToken))
	if err != nil {
		response.InternalError(c, "AdsPower callback ticket could not be read")
		return
	}
	if !ok {
		response.Error(c, http.StatusGone, "AdsPower callback ticket has expired or was already used")
		return
	}
	h.batchOAuthStore.mu.Lock()
	task := h.batchOAuthStore.tasks[record.TaskID]
	h.batchOAuthStore.mu.Unlock()
	if task == nil {
		response.Error(c, http.StatusGone, "Authorization task is no longer available")
		return
	}
	task.mu.Lock()
	defer task.mu.Unlock()
	if task.ownerID != record.AdminUserID || !task.matchesMode(record.TaskMode) || !task.usesAdsPower() ||
		task.terminal() || task.sessionID != record.SessionID || task.authURL != record.AuthURL {
		response.Error(c, http.StatusConflict, "AdsPower callback does not match the active authorization task")
		return
	}
	if task.normalizedMode() == batchOAuthModeCreate {
		binding, err := h.adsPowerLaunchStore.pendingBinding(c.Request.Context(), task.sessionID, task.ownerID)
		if err != nil {
			response.Error(c, http.StatusServiceUnavailable, "AdsPower browser binding is temporarily unavailable")
			return
		}
		if binding == nil || binding.EnvironmentKey != record.EnvironmentKey {
			response.Error(c, http.StatusConflict, "AdsPower browser binding has not been verified")
			return
		}
	} else {
		account, err := h.adminService.GetAccount(c.Request.Context(), task.TargetAccountID)
		binding := service.OpenAIAdsPowerBindingFromAccount(account)
		if err != nil || account == nil || binding == nil || binding.EnvironmentKey != record.EnvironmentKey {
			response.Error(c, http.StatusConflict, "AdsPower browser binding has not been verified")
			return
		}
	}
	callbackURL := strings.TrimSpace(req.CallbackURL)
	if _, err := validateBatchCallback(callbackURL, task.state); err != nil {
		response.BadRequest(c, "Invalid OAuth callback")
		return
	}
	task.externalCallbackURL = callbackURL
	task.Status, task.Stage, task.Reason = "ready", "callback_received", ""
	task.snapshotJSONLocked()
	response.Success(c, gin.H{"accepted": true})
}

func (h *OpenAIOAuthHandler) ReportOpenAIAdsPowerBinding(c *gin.Context) {
	if h == nil || h.adsPowerLaunchStore == nil || h.adminService == nil {
		response.Error(c, http.StatusServiceUnavailable, "AdsPower binding service is unavailable")
		return
	}
	var req openAIAdsPowerBindingReport
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 12<<10)
	if c.ShouldBindJSON(&req) != nil {
		response.BadRequest(c, "Invalid AdsPower binding report")
		return
	}
	record, ok, err := h.adsPowerLaunchStore.consumeBinding(c.Request.Context(), strings.TrimSpace(req.BindingToken))
	if err != nil {
		response.InternalError(c, "AdsPower binding ticket could not be read")
		return
	}
	if !ok {
		response.Error(c, http.StatusGone, "AdsPower binding ticket has expired or was already used")
		return
	}
	binding, err := normalizeOpenAIAdsPowerBindingReport(req, record)
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	if record.Existing != nil && (record.Existing.ProfileID != binding.ProfileID || record.Existing.DeviceID != binding.DeviceID) {
		response.Error(c, http.StatusConflict, "This account is already bound to a different AdsPower profile")
		return
	}
	if record.AccountID > 0 {
		updater, ok := h.adminService.(openAIAdsPowerBindingUpdater)
		if !ok || updater == nil {
			response.Error(c, http.StatusServiceUnavailable, "AdsPower binding storage is unavailable")
			return
		}
		if err := updater.UpdateOpenAIAdsPowerBindingFromLaunch(c.Request.Context(), record.AccountID, record.EnvironmentKey, binding); err != nil {
			response.Error(c, http.StatusConflict, err.Error())
			return
		}
	} else if err := h.adsPowerLaunchStore.savePending(c.Request.Context(), record.SessionID, record.AdminUserID, *binding); err != nil {
		response.InternalError(c, "AdsPower pending binding could not be stored")
		return
	}
	response.Success(c, binding)
}

func (h *OpenAIOAuthHandler) DeleteOpenAIAdsPowerBinding(c *gin.Context) {
	accountID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || accountID <= 0 {
		response.BadRequest(c, "Invalid account ID")
		return
	}
	if err := ensureAdminAccountManagementAccess(c.Request.Context(), h.adminService, accountID); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	updater, ok := h.adminService.(openAIAdsPowerBindingUpdater)
	if !ok || updater == nil {
		response.Error(c, http.StatusServiceUnavailable, "AdsPower binding storage is unavailable")
		return
	}
	if err := updater.UpdateOpenAIAdsPowerBinding(c.Request.Context(), accountID, nil); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"account_id": accountID, "unbound": true})
}

func (h *OpenAIOAuthHandler) ClaimOpenAIAdsPowerBinding(c *gin.Context) {
	if h == nil || h.adsPowerLaunchStore == nil || h.adminService == nil {
		response.Error(c, http.StatusServiceUnavailable, "AdsPower binding service is unavailable")
		return
	}
	accountID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || accountID <= 0 {
		response.BadRequest(c, "Invalid account ID")
		return
	}
	if err := ensureAdminAccountManagementAccess(c.Request.Context(), h.adminService, accountID); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	var req openAIAdsPowerBindingClaimRequest
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 4<<10)
	if c.ShouldBindJSON(&req) != nil || !validOpenAIAdsPowerSessionID(strings.TrimSpace(req.SessionID)) {
		response.BadRequest(c, "Invalid AdsPower OAuth session")
		return
	}
	account, err := h.adminService.GetAccount(c.Request.Context(), accountID)
	if err != nil || account == nil || !account.IsOpenAIOAuth() || account.IsCredentialShadow() || account.IsOpenAIOAuthCredentialCopy() {
		response.BadRequest(c, "Only primary OpenAI OAuth accounts can claim an AdsPower profile")
		return
	}
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok || subject.UserID <= 0 {
		response.Unauthorized(c, "administrator session is required")
		return
	}
	binding, err := h.adsPowerLaunchStore.pendingBinding(c.Request.Context(), strings.TrimSpace(req.SessionID), subject.UserID)
	if err != nil {
		response.Error(c, http.StatusServiceUnavailable, "AdsPower browser binding is temporarily unavailable")
		return
	}
	if binding == nil || binding.EnvironmentKey != openAIAdsPowerAccountEnvironment(account) {
		response.Error(c, http.StatusConflict, "AdsPower browser binding does not match this account server")
		return
	}
	updater, ok := h.adminService.(openAIAdsPowerBindingUpdater)
	if !ok || updater == nil {
		response.Error(c, http.StatusServiceUnavailable, "AdsPower binding storage is unavailable")
		return
	}
	if err := updater.UpdateOpenAIAdsPowerBinding(c.Request.Context(), accountID, binding); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	h.adsPowerLaunchStore.deletePending(c.Request.Context(), strings.TrimSpace(req.SessionID))
	response.Success(c, binding)
}

func normalizeOpenAIAdsPowerBindingReport(req openAIAdsPowerBindingReport, record openAIAdsPowerLaunchRecord) (*service.OpenAIAdsPowerBinding, error) {
	req.DeviceID = strings.TrimSpace(req.DeviceID)
	req.ProfileID = strings.TrimSpace(req.ProfileID)
	req.ProfileNo = strings.TrimSpace(req.ProfileNo)
	req.ProfileName = sanitizeOpenAIAdsPowerLabel(req.ProfileName)
	req.EnvironmentKey = strings.TrimSpace(req.EnvironmentKey)
	req.ProxyType = strings.ToLower(strings.TrimSpace(req.ProxyType))
	req.ProxyHost = strings.ToLower(strings.TrimSpace(req.ProxyHost))
	req.ProxyPort = strings.TrimSpace(req.ProxyPort)
	req.ProxyExitIP = strings.TrimSpace(req.ProxyExitIP)
	if !validOpenAIAdsPowerOpaqueID(req.DeviceID, 128) || !validOpenAIAdsPowerOpaqueID(req.ProfileID, 128) {
		return nil, errors.New("AdsPower device or profile identity is invalid")
	}
	if req.EnvironmentKey == "" || req.EnvironmentKey != record.EnvironmentKey {
		return nil, errors.New("AdsPower environment does not match the launch ticket")
	}
	if req.ProxyType != "socks5" || req.ProxyHost == "" || !validOpenAIAdsPowerPort(req.ProxyPort) {
		return nil, errors.New("AdsPower profile must use the assigned SOCKS5 proxy")
	}
	if req.ProxyExitIP != "" && net.ParseIP(req.ProxyExitIP) == nil {
		return nil, errors.New("AdsPower proxy exit IP is invalid")
	}
	if !req.WebRTCDisabled || !req.FingerprintRandomized {
		return nil, errors.New("AdsPower profile must use a randomized fingerprint with WebRTC disabled")
	}
	if req.FingerprintSlot < 0 || req.FingerprintSlot > 52 {
		return nil, errors.New("AdsPower fingerprint slot is invalid")
	}
	now := time.Now().UTC()
	boundAt := now
	if record.Existing != nil && record.Existing.BoundAt != nil {
		boundAt = record.Existing.BoundAt.UTC()
	}
	return &service.OpenAIAdsPowerBinding{
		Version: 1, DeviceID: req.DeviceID, ProfileID: req.ProfileID, ProfileNo: req.ProfileNo,
		ProfileName: req.ProfileName, EnvironmentKey: req.EnvironmentKey, ProxyType: req.ProxyType,
		ProxyHost: req.ProxyHost, ProxyPort: req.ProxyPort, ProxyExitIP: req.ProxyExitIP,
		WebRTCDisabled: true, FingerprintRandomized: true, FingerprintSlot: req.FingerprintSlot, BoundAt: &boundAt,
		LastVerifiedAt: &now, LastLaunchedAt: &now,
	}, nil
}

func validOpenAIAdsPowerSessionID(value string) bool {
	if len(value) != 32 {
		return false
	}
	for _, char := range value {
		if (char >= 'a' && char <= 'f') || (char >= '0' && char <= '9') {
			continue
		}
		return false
	}
	return true
}

func validOpenAIAdsPowerAuthURL(raw string) bool {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" || !strings.EqualFold(parsed.Hostname(), "auth.openai.com") || parsed.Path != "/oauth/authorize" {
		return false
	}
	query := parsed.Query()
	return query.Get("state") != "" && query.Get("code_challenge") != "" && query.Get("redirect_uri") != ""
}

func validOpenAIAdsPowerOpaqueID(value string, maxLength int) bool {
	if value == "" || len(value) > maxLength {
		return false
	}
	for _, char := range value {
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9') || char == '-' || char == '_' || char == '.' {
			continue
		}
		return false
	}
	return true
}

func validOpenAIAdsPowerPort(value string) bool {
	port, err := strconv.Atoi(value)
	return err == nil && port > 0 && port <= 65535
}

func sanitizeOpenAIAdsPowerLabel(value string) string {
	value = strings.TrimSpace(value)
	if len(value) > 120 {
		value = value[:120]
	}
	return strings.Map(func(char rune) rune {
		if char < 32 || char == 127 {
			return -1
		}
		return char
	}, value)
}

func openAIAdsPowerAccountEnvironment(account *service.Account) string {
	if account != nil {
		if value := strings.TrimSpace(account.GetExtraString(service.AccountExecutionNodeExtraKey)); value != "" {
			return value
		}
	}
	if value := strings.TrimSpace(os.Getenv("GATEWAY_EXECUTION_NODE_ID")); value != "" {
		return value
	}
	return "api"
}

func openAIAdsPowerLocalEnvironment(c *gin.Context) string {
	if value := strings.TrimSpace(os.Getenv("GATEWAY_EXECUTION_NODE_ID")); value != "" {
		return value
	}
	if c != nil {
		host := c.Request.Host
		if parsedHost, _, err := net.SplitHostPort(host); err == nil {
			host = parsedHost
		}
		host = strings.ToLower(strings.TrimSpace(host))
		if strings.HasPrefix(host, "api2.") {
			return "api2"
		}
	}
	return "api"
}

func openAIAdsPowerRequestOrigin(c *gin.Context) string {
	proto := "http"
	if c.Request.TLS != nil {
		proto = "https"
	}
	if forwarded := strings.TrimSpace(strings.Split(c.GetHeader("X-Forwarded-Proto"), ",")[0]); forwarded == "http" || forwarded == "https" {
		proto = forwarded
	}
	return (&url.URL{Scheme: proto, Host: c.Request.Host}).String()
}

func openAIAdsPowerHelperURL(serverOrigin, ticket string) (string, error) {
	baseRaw := strings.TrimSpace(os.Getenv("ADSPOWER_HELPER_LAUNCH_URL"))
	if baseRaw == "" {
		baseRaw = openAIAdsPowerDefaultLoopback
	}
	base, err := url.Parse(baseRaw)
	if err != nil || base.Scheme != "http" || base.Path == "" || !isOpenAIAdsPowerLoopbackHost(base.Hostname()) {
		return "", errors.New("AdsPower helper launch URL must use loopback HTTP")
	}
	server, err := url.Parse(serverOrigin)
	if err != nil || server.Host == "" || (server.Scheme != "https" && !isOpenAIAdsPowerLoopbackHost(server.Hostname())) {
		return "", fmt.Errorf("server origin is invalid")
	}
	query := base.Query()
	query.Set("server", server.String())
	query.Set("ticket", ticket)
	base.RawQuery = query.Encode()
	return base.String(), nil
}

func isOpenAIAdsPowerLoopbackHost(host string) bool {
	host = strings.Trim(strings.ToLower(strings.TrimSpace(host)), "[]")
	return host == "localhost" || host == "127.0.0.1" || host == "::1"
}
