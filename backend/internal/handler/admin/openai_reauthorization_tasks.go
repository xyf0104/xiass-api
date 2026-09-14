package admin

import (
	"context"
	"errors"
	"net/http"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type openAIReauthorizationStartRequest struct {
	AccountID int64 `json:"account_id"`
}

type openAIReauthorizationLoginMaterial struct {
	Email          string
	Method         string
	Password       string
	TOTPSecret     string
	EmailCodeToken string
}

func cloneBatchOAuthInt64(value *int64) *int64 {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func openAIReauthorizationAccountConfig(account *service.Account) batchOAuthConfig {
	if account == nil {
		return batchOAuthConfig{}
	}
	return batchOAuthConfig{
		Name:            account.Name,
		GroupIDs:        slices.Clone(account.GroupIDs),
		ProxyID:         cloneBatchOAuthInt64(account.ProxyID),
		Concurrency:     account.Concurrency,
		Priority:        account.Priority,
		FingerprintMode: account.GetExtraString("codex_fingerprint_mode"),
	}
}

func (h *OpenAIOAuthHandler) openAIReauthorizationLogin(ctx context.Context, accountID int64) (*service.Account, *openAIReauthorizationLoginMaterial, map[string]string, error) {
	if h == nil || h.adminService == nil || h.secretEncryptor == nil {
		return nil, nil, nil, errors.New("OpenAI reauthorization is unavailable")
	}
	account, err := h.adminService.GetAccount(ctx, accountID)
	if err != nil || account == nil {
		return nil, nil, nil, errors.New("OpenAI account not found")
	}
	if !account.IsOpenAIOAuth() || account.IsCredentialShadow() {
		return nil, nil, nil, errors.New("only OpenAI OAuth accounts can be reauthorized")
	}
	localNodeID := strings.TrimSpace(os.Getenv("GATEWAY_EXECUTION_NODE_ID"))
	accountNodeID := strings.TrimSpace(account.GetExtraString(service.AccountExecutionNodeExtraKey))
	if localNodeID != "" && accountNodeID != "" && accountNodeID != localNodeID {
		return nil, nil, nil, errors.New("open this account on its assigned XIASS server")
	}
	email, ciphertext, _ := openAIAccountReauthorizationLogin(account)
	if email == "" {
		return nil, nil, nil, errors.New("saved OpenAI login email is unavailable")
	}
	if encrypted, _ := account.Credentials[service.OpenAIOAuthReauthorizationEmailCodeTokenCredentialKey].(string); encrypted != "" {
		emailCodeToken, decryptErr := h.secretEncryptor.Decrypt(encrypted)
		if decryptErr != nil || validateBatchEmailCodeLogin(email, emailCodeToken) != nil {
			return nil, nil, nil, errors.New("saved OpenAI email-code token cannot be decrypted")
		}
		proxy, proxyErr := h.batchOAuthBrowserProxy(ctx, account.ProxyID)
		if proxyErr != nil {
			return nil, nil, nil, errors.New("the account proxy is unavailable for browser authorization")
		}
		return account, &openAIReauthorizationLoginMaterial{
			Email: email, Method: batchOAuthLoginEmailCode, EmailCodeToken: emailCodeToken,
		}, proxy, nil
	}
	if strings.TrimSpace(ciphertext) == "" {
		return nil, nil, nil, errors.New("saved OpenAI login password is unavailable")
	}
	password, err := h.secretEncryptor.Decrypt(ciphertext)
	if err != nil || len(password) == 0 || len(password) > 2048 {
		return nil, nil, nil, errors.New("saved OpenAI login password cannot be decrypted")
	}
	var totpSecret string
	if encrypted, _ := account.Credentials[service.OpenAIOAuthReauthorizationTOTPSecretCredentialKey].(string); encrypted != "" {
		totpSecret, err = h.secretEncryptor.Decrypt(encrypted)
		if err != nil || validateOpenAIReauthorizationTOTP(totpSecret) != nil {
			return nil, nil, nil, errors.New("saved OpenAI 2FA secret cannot be decrypted")
		}
	}
	if err := validateBatchLogin(email, password, totpSecret); err != nil {
		return nil, nil, nil, errors.New("saved OpenAI login information is invalid")
	}
	proxy, err := h.batchOAuthBrowserProxy(ctx, account.ProxyID)
	if err != nil {
		return nil, nil, nil, errors.New("the account proxy is unavailable for browser authorization")
	}
	return account, &openAIReauthorizationLoginMaterial{
		Email: email, Method: batchOAuthLoginPassword, Password: password, TOTPSecret: totpSecret,
	}, proxy, nil
}

func (h *OpenAIOAuthHandler) StartOpenAIReauthorizationTask(c *gin.Context) {
	owner, ok := batchOAuthAuth(c)
	if !ok {
		return
	}
	if h.batchOAuthStore == nil || h.openaiOAuthService == nil || h.secretEncryptor == nil {
		response.Error(c, http.StatusServiceUnavailable, "OpenAI reauthorization is unavailable")
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 4<<10)
	var req openAIReauthorizationStartRequest
	if c.ShouldBindJSON(&req) != nil || req.AccountID <= 0 {
		response.BadRequest(c, "A valid OpenAI account is required")
		return
	}
	if err := ensureAdminAccountManagementAccess(c.Request.Context(), h.adminService, req.AccountID); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	account, login, proxy, err := h.openAIReauthorizationLogin(c.Request.Context(), req.AccountID)
	if err != nil {
		response.Error(c, http.StatusConflict, err.Error())
		return
	}

	store := h.batchOAuthStore
	store.mu.Lock()
	for _, task := range store.tasks {
		if !task.matchesMode(batchOAuthModeReauthorization) || task.TargetAccountID != req.AccountID {
			continue
		}
		if !task.mu.TryLock() {
			store.mu.Unlock()
			response.Error(c, http.StatusConflict, "The reauthorization task is busy")
			return
		}
		store.mu.Unlock()
		defer task.mu.Unlock()
		if task.ownerID != owner {
			response.Error(c, http.StatusConflict, "This account already has an authorization task")
			return
		}
		response.Success(c, task)
		return
	}
	if !store.hasCapacityLocked(nil) || len(store.tasks) >= 1000 {
		store.mu.Unlock()
		response.Error(c, http.StatusConflict, "Browser authorization capacity reached")
		return
	}
	id, err := newTeamChildBrowserToken()
	if err != nil {
		store.mu.Unlock()
		response.InternalError(c, "Task creation failed")
		return
	}
	now := time.Now().UTC()
	task := &batchOAuthTask{
		ID: id, Mode: batchOAuthModeReauthorization, Email: login.Email, LoginMethod: login.Method, TargetAccountID: req.AccountID,
		ownerID: owner, sidecarID: id, config: openAIReauthorizationAccountConfig(account),
		executionNodeID: strings.TrimSpace(account.GetExtraString(service.AccountExecutionNodeExtraKey)),
		Status:          "queued", Stage: "queued", CreatedAt: now, ExpiresAt: now.Add(30 * time.Minute),
	}
	task.mu.Lock()
	store.tasks[id] = task
	store.mu.Unlock()
	defer task.mu.Unlock()
	defer task.markFinishedIfTerminal()
	ctx, cancel := context.WithTimeout(context.WithoutCancel(c.Request.Context()), 35*time.Second)
	defer cancel()
	_ = h.startBatchAttempt(ctx, task, login.Password, login.TOTPSecret, login.EmailCodeToken, proxy)
	response.Success(c, task)
}

func (h *OpenAIOAuthHandler) ListOpenAIReauthorizationTasks(c *gin.Context) {
	h.listBatchOAuthTasks(c, batchOAuthModeReauthorization)
}

func (h *OpenAIOAuthHandler) GetOpenAIReauthorizationTask(c *gin.Context) {
	h.getBatchOAuthTask(c, batchOAuthModeReauthorization)
}

func (h *OpenAIOAuthHandler) CancelOpenAIReauthorizationTask(c *gin.Context) {
	h.cancelBatchOAuthTask(c, batchOAuthModeReauthorization)
}

func (h *OpenAIOAuthHandler) DeleteOpenAIReauthorizationTask(c *gin.Context) {
	h.deleteBatchOAuthTask(c, batchOAuthModeReauthorization)
}

func (h *OpenAIOAuthHandler) OpenAIReauthorizationSMSAction(c *gin.Context) {
	h.batchOAuthSMSAction(c, batchOAuthModeReauthorization)
}

func (h *OpenAIOAuthHandler) CompleteOpenAIReauthorizationTask(c *gin.Context) {
	task, ok := h.ownedBatchTask(c, batchOAuthModeReauthorization)
	if !ok {
		return
	}
	defer task.mu.Unlock()
	defer task.markFinishedIfTerminal()
	defer h.revokeTerminalBatchSession(task)
	if task.AccountID > 0 {
		if task.Status == "completed" {
			response.Success(c, task)
			return
		}
		if task.Reason == "account_state_recovery_failed" {
			updated, err := h.adminService.ClearAccountError(c.Request.Context(), task.AccountID)
			if err == nil && updated != nil {
				finishOpenAIReauthorizationTask(task, updated)
			}
			response.Success(c, task)
			return
		}
	}
	if task.terminal() {
		response.Error(c, http.StatusConflict, "Task cannot update this account")
		return
	}
	sidecar, err := task.refresh(c.Request.Context())
	if err != nil || sidecar == nil || task.Status != "ready" {
		response.Error(c, http.StatusConflict, "OAuth is not complete")
		return
	}
	account, err := h.adminService.GetAccount(c.Request.Context(), task.TargetAccountID)
	if err != nil || account == nil || !account.IsOpenAIOAuth() || account.IsCredentialShadow() {
		task.Status, task.Stage, task.Reason = "failed", "failed", "account_update_failed"
		response.Success(c, task)
		return
	}
	email, _, _ := openAIAccountReauthorizationLogin(account)
	if email == "" || !strings.EqualFold(email, task.Email) {
		task.Status, task.Stage, task.Reason = "failed", "failed", "oauth_identity_mismatch"
		response.Success(c, task)
		return
	}
	currentNodeID := strings.TrimSpace(account.GetExtraString(service.AccountExecutionNodeExtraKey))
	localNodeID := strings.TrimSpace(os.Getenv("GATEWAY_EXECUTION_NODE_ID"))
	if currentNodeID != task.executionNodeID || !batchProxyEqual(account.ProxyID, task.config.ProxyID) ||
		(localNodeID != "" && currentNodeID != "" && currentNodeID != localNodeID) {
		task.Status, task.Stage, task.Reason = "failed", "failed", "account_configuration_changed"
		response.Success(c, task)
		return
	}
	code, err := validateBatchCallback(sidecar.CallbackURL, task.state)
	if err != nil {
		response.BadRequest(c, "Invalid OAuth callback")
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(c.Request.Context()), 60*time.Second)
	defer cancel()
	token, err := h.openaiOAuthService.ExchangeWorkflowCode(ctx, &service.OpenAIExchangeCodeInput{
		SessionID: task.sessionID,
		Code:      code,
		State:     task.state,
	})
	h.openaiOAuthService.RevokeWorkflowSession(task.sessionID)
	task.Status, task.Stage, task.Reason = "failed", "failed", "oauth_exchange_failed"
	if err != nil || token == nil || token.AccessToken == "" {
		response.Success(c, task)
		return
	}
	if !strings.EqualFold(strings.TrimSpace(token.Email), task.Email) {
		task.Reason = "oauth_identity_mismatch"
		response.Success(c, task)
		return
	}
	credentials := service.MergeCredentials(account.Credentials, h.openaiOAuthService.BuildAccountCredentials(token))
	credentials[service.OpenAIOAuthReauthorizationEmailCredentialKey] = task.Email
	updated, err := h.adminService.UpdateAccount(ctx, account.ID, &service.UpdateAccountInput{
		Credentials:                           credentials,
		AllowOpenAIReauthorizationCredentials: true,
	})
	token = nil
	if err != nil || updated == nil {
		task.Reason = "account_update_failed"
		response.Success(c, task)
		return
	}
	task.AccountID = account.ID
	if h.tokenCacheInvalidator != nil {
		_ = h.tokenCacheInvalidator.InvalidateToken(ctx, account)
		_ = h.tokenCacheInvalidator.InvalidateToken(ctx, updated)
	}
	cleared, clearErr := h.adminService.ClearAccountError(ctx, account.ID)
	if clearErr != nil || cleared == nil {
		task.Status, task.Stage, task.Reason = "failed", "failed", "account_state_recovery_failed"
		config := openAIReauthorizationAccountConfig(updated)
		task.AccountConfig = &config
		response.Success(c, task)
		return
	}
	finishOpenAIReauthorizationTask(task, cleared)
	response.Success(c, task)
}

func finishOpenAIReauthorizationTask(task *batchOAuthTask, account *service.Account) {
	task.AccountID = account.ID
	task.Status, task.Stage, task.Reason = "completed", "completed", ""
	config := openAIReauthorizationAccountConfig(account)
	task.AccountConfig = &config
}

func (h *OpenAIOAuthHandler) RestartOpenAIReauthorizationTask(c *gin.Context) {
	task, ok := h.ownedBatchTask(c, batchOAuthModeReauthorization)
	if !ok {
		return
	}
	defer task.mu.Unlock()
	defer task.markFinishedIfTerminal()
	if task.AccountID > 0 || task.RestartCount >= batchOAuthMaxRestarts || task.Reason == "account_blocked" {
		response.Error(c, http.StatusConflict, "Task cannot restart")
		return
	}
	if !task.terminal() {
		response.Error(c, http.StatusConflict, "Stop the current task before restarting")
		return
	}
	if err := ensureAdminAccountManagementAccess(c.Request.Context(), h.adminService, task.TargetAccountID); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	account, login, proxy, err := h.openAIReauthorizationLogin(c.Request.Context(), task.TargetAccountID)
	if err != nil {
		response.Error(c, http.StatusConflict, err.Error())
		return
	}
	store := h.batchOAuthStore
	store.mu.Lock()
	if !store.hasCapacityLocked(task) {
		store.mu.Unlock()
		response.Error(c, http.StatusConflict, "Browser authorization capacity reached")
		return
	}
	store.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.WithoutCancel(c.Request.Context()), 60*time.Second)
	defer cancel()
	if err := h.cancelBatchReservation(ctx, task, true); err != nil {
		response.Error(c, http.StatusConflict, err.Error())
		return
	}
	if _, err := task.sidecar(ctx, "cancel", nil); err != nil {
		response.Error(c, http.StatusBadGateway, "Browser cancellation not confirmed")
		return
	}
	h.openaiOAuthService.RevokeWorkflowSession(task.sessionID)
	id, err := newTeamChildBrowserToken()
	if err != nil {
		response.InternalError(c, "Task restart failed")
		return
	}
	task.sidecarID = id
	task.Email = login.Email
	task.LoginMethod = login.Method
	task.config = openAIReauthorizationAccountConfig(account)
	task.executionNodeID = strings.TrimSpace(account.GetExtraString(service.AccountExecutionNodeExtraKey))
	task.submittedPhone = ""
	task.RestartCount++
	task.RequiresSMSConfirmation = false
	task.FinishedAt = nil
	task.Status, task.Stage, task.Reason = "queued", "queued", ""
	_ = h.startBatchAttempt(ctx, task, login.Password, login.TOTPSecret, login.EmailCodeToken, proxy)
	response.Success(c, task)
}
