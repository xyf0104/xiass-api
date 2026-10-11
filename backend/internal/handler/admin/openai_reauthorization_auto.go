package admin

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

const (
	openAIReauthorizationAutoPollInterval = time.Second
	openAIReauthorizationAutoLease        = 45 * time.Minute
	openAIReauthorizationAutoMaxRuntime   = 35 * time.Minute
	openAIReauthorizationAutoHelperFresh  = 90 * time.Second
)

// openAIReauthorizationAutoStore is deliberately optional. The production
// AdminService implementation delegates these methods to a conditional account
// repository. Tests and installations without the CAS capability fail closed:
// they never start an unattended OAuth attempt and therefore cannot duplicate a
// 401 flow across replicas.
type openAIReauthorizationAutoStore interface {
	ClaimOpenAIReauthorizationAutoAttempt(context.Context, *service.Account, service.OpenAIReauthorizationAutoClaim) (service.OpenAIReauthorizationState, bool, error)
	FinishOpenAIReauthorizationAutoAttempt(context.Context, *service.Account, service.OpenAIReauthorizationAutoClaim, service.OpenAIReauthorizationState) (bool, error)
	ApplyOpenAIReauthorizationAutoCredentials(context.Context, *service.Account, service.OpenAIReauthorizationAutoClaim, map[string]any, service.OpenAIReauthorizationState) (bool, error)
}

type openAIReauthorizationAutoTask struct {
	task          *batchOAuthTask
	expected      *service.Account
	claim         service.OpenAIReauthorizationAutoClaim
	completed     bool
	accountUpdate bool
}

type openAIReauthorizationAutoRuntime struct {
	h       *OpenAIOAuthHandler
	ctx     context.Context
	cancel  context.CancelFunc
	queue   chan int64
	mu      sync.Mutex
	pending map[int64]bool
	tasks   map[string]*openAIReauthorizationAutoTask
	wg      sync.WaitGroup
}

var openAIReauthorizationAutoRuntimes sync.Map // map[*OpenAIOAuthHandler]*openAIReauthorizationAutoRuntime

// StartOpenAIReauthorizationAutoWorker starts the resident worker. It is safe
// to call more than once and is intended to be wired into server lifecycle. The
// worker also scans on startup so a 401 persisted while the process was down is
// not lost when the page is closed.
func (h *OpenAIOAuthHandler) StartOpenAIReauthorizationAutoWorker(parent context.Context) {
	if h == nil {
		return
	}
	if _, loaded := openAIReauthorizationAutoRuntimes.Load(h); loaded {
		return
	}
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithCancel(parent)
	r := &openAIReauthorizationAutoRuntime{
		h: h, ctx: ctx, cancel: cancel, queue: make(chan int64, 128),
		pending: make(map[int64]bool), tasks: make(map[string]*openAIReauthorizationAutoTask),
	}
	if _, loaded := openAIReauthorizationAutoRuntimes.LoadOrStore(h, r); loaded {
		cancel()
		return
	}
	r.wg.Add(1)
	go r.run()
}

// StopOpenAIReauthorizationAutoWorker stops only the automation worker. It does
// not revoke an in-flight OAuth session or change persisted account state; the
// durable claim prevents a later process from replaying the same 401 episode.
func (h *OpenAIOAuthHandler) StopOpenAIReauthorizationAutoWorker() {
	if h == nil {
		return
	}
	value, ok := openAIReauthorizationAutoRuntimes.LoadAndDelete(h)
	if !ok {
		return
	}
	r, ok := value.(*openAIReauthorizationAutoRuntime)
	if !ok || r == nil {
		return
	}
	r.cancel()
	r.wg.Wait()
}

// HandleOpenAIUnauthorized is the narrow event hook for the successful,
// identity-bound 401 persistence path. It never performs OAuth inline with a
// customer request. The first event starts the resident worker lazily so a
// missed server lifecycle hook cannot make the feature depend on an open page.
func (h *OpenAIOAuthHandler) HandleOpenAIUnauthorized(accountID int64) {
	h.HandleOpenAIUnauthorizedEvent(context.Background(), accountID)
}

// HandleOpenAIUnauthorizedEvent matches service.OpenAIUnauthorizedHandler so
// the rate-limit/CAS path can wire the method directly. The context is used
// only as the worker parent; no OAuth work is performed on the request stack.
func (h *OpenAIOAuthHandler) HandleOpenAIUnauthorizedEvent(parent context.Context, accountID int64) {
	if h == nil || accountID <= 0 {
		return
	}
	h.StartOpenAIReauthorizationAutoWorker(parent)
	value, ok := openAIReauthorizationAutoRuntimes.Load(h)
	if !ok {
		return
	}
	r, ok := value.(*openAIReauthorizationAutoRuntime)
	if !ok || r == nil {
		return
	}
	r.enqueue(accountID)
}

func (r *openAIReauthorizationAutoRuntime) enqueue(accountID int64) {
	if r == nil || accountID <= 0 {
		return
	}
	r.mu.Lock()
	if r.pending[accountID] {
		r.mu.Unlock()
		return
	}
	r.pending[accountID] = true
	r.mu.Unlock()
	select {
	case r.queue <- accountID:
	case <-r.ctx.Done():
		r.mu.Lock()
		delete(r.pending, accountID)
		r.mu.Unlock()
	}
}

func (r *openAIReauthorizationAutoRuntime) run() {
	defer r.wg.Done()
	initial := time.NewTimer(50 * time.Millisecond)
	defer initial.Stop()
	select {
	case <-initial.C:
		r.scan()
	case <-r.ctx.Done():
		return
	}
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-r.ctx.Done():
			return
		case accountID := <-r.queue:
			r.wg.Add(1)
			go func() {
				defer r.wg.Done()
				r.process(accountID)
			}()
		case <-ticker.C:
			r.scan()
		}
	}
}

func (r *openAIReauthorizationAutoRuntime) scan() {
	if r == nil || r.h == nil || r.h.adminService == nil {
		return
	}
	ctx, cancel := context.WithTimeout(r.ctx, 15*time.Second)
	defer cancel()
	accounts, err := r.h.listPrimaryOpenAIOAuthAccounts(ctx)
	if err != nil {
		slog.Warn("openai_reauthorization_auto_scan_failed", "error", err)
		return
	}
	for i := range accounts {
		if openAIAccountNeedsReauthorization(&accounts[i]) {
			r.enqueue(accounts[i].ID)
		}
	}
}

func (r *openAIReauthorizationAutoRuntime) process(accountID int64) {
	defer func() {
		r.mu.Lock()
		delete(r.pending, accountID)
		r.mu.Unlock()
	}()
	if r.ctx.Err() != nil || r.h == nil {
		return
	}
	if err := r.h.runOpenAIReauthorizationAutomatic(r.ctx, accountID, r); err != nil {
		slog.Warn("openai_reauthorization_auto_failed", "account_id", accountID, "error", err)
	}
}

func openAIReauthorizationAutoStoreFor(h *OpenAIOAuthHandler) (openAIReauthorizationAutoStore, bool) {
	if h == nil || h.adminService == nil {
		return nil, false
	}
	store, ok := h.adminService.(openAIReauthorizationAutoStore)
	return store, ok && store != nil
}

func openAIAccountSupportsAutomaticReauthorization(account *service.Account) bool {
	if account == nil || !account.IsOpenAIOAuth() || account.IsCredentialShadow() || account.IsOpenAIOAuthCredentialCopy() {
		return false
	}
	email, passwordCiphertext, _ := openAIAccountReauthorizationLogin(account)
	if strings.TrimSpace(email) == "" {
		return false
	}
	if strings.TrimSpace(account.GetCredential(service.OpenAIOAuthReauthorizationEmailCodeTokenCredentialKey)) != "" {
		return true
	}
	return strings.TrimSpace(passwordCiphertext) != ""
}

func openAIReauthorizationBrowserMode(account *service.Account) string {
	if account == nil {
		return batchOAuthBrowserServer
	}
	saved := strings.ToLower(strings.TrimSpace(account.GetExtraString(service.OpenAIReauthorizationBrowserModeExtraKey)))
	if saved == batchOAuthBrowserServer || saved == batchOAuthBrowserAdsPower {
		return saved
	}
	if service.OpenAIAdsPowerBindingFromAccount(account) != nil {
		return batchOAuthBrowserAdsPower
	}
	mode := normalizeBatchOAuthBrowserMode(saved)
	if mode == batchOAuthBrowserServer || mode == batchOAuthBrowserAdsPower {
		return mode
	}
	return batchOAuthBrowserServer
}

func openAIReauthorizationAutoEpisodeKey(account *service.Account) string {
	if account == nil {
		return ""
	}
	version := account.GetCredentialAsInt64("_token_version")
	access := account.GetCredential("access_token")
	refresh := account.GetCredential("refresh_token")
	sum := sha256.Sum256([]byte(fmt.Sprintf("%d:%d:%s:%s", account.ID, version, access, refresh)))
	return hex.EncodeToString(sum[:])
}

func openAIReauthorizationAutoClaimAlreadyRecorded(state service.OpenAIReauthorizationState, episodeKey string, now time.Time) bool {
	if state.LastInitiator != service.OpenAIReauthorizationInitiatorAutomatic || state.AutoEpisodeKey != episodeKey {
		return false
	}
	if state.LastResult == service.OpenAIReauthorizationResultSuccess || state.LastResult == service.OpenAIReauthorizationResultFailed || state.LastResult == service.OpenAIReauthorizationResultBlocked {
		return true
	}
	// A persisted running claim is also terminal for this credential episode.
	// If its lease expired, the worker records an interruption failure; it never
	// replays the OAuth flow after a crash or restart.
	return state.LastResult == service.OpenAIReauthorizationResultRunning
}

func (h *OpenAIOAuthHandler) runOpenAIReauthorizationAutomatic(ctx context.Context, accountID int64, runtime *openAIReauthorizationAutoRuntime) error {
	store, ok := openAIReauthorizationAutoStoreFor(h)
	if !ok {
		return errors.New("automatic reauthorization CAS storage is unavailable")
	}
	account, err := h.adminService.GetAccount(ctx, accountID)
	if err != nil || account == nil || !openAIAccountNeedsReauthorization(account) || !openAIAccountSupportsAutomaticReauthorization(account) {
		return nil
	}
	mode := openAIReauthorizationBrowserMode(account)
	localNodeID := strings.TrimSpace(os.Getenv("GATEWAY_EXECUTION_NODE_ID"))
	accountNodeID := strings.TrimSpace(account.GetExtraString(service.AccountExecutionNodeExtraKey))
	if accountNodeID != "" && localNodeID != "" && accountNodeID != localNodeID {
		return nil
	}
	if mode == batchOAuthBrowserAdsPower && service.OpenAIAdsPowerBindingFromAccount(account) == nil {
		return nil
	}
	if h.batchOAuthStore == nil {
		return nil
	}
	h.batchOAuthStore.mu.Lock()
	hasCapacity := h.batchOAuthStore.hasCapacityLocked(nil)
	h.batchOAuthStore.mu.Unlock()
	if !hasCapacity {
		return nil
	}

	now := time.Now().UTC()
	episodeKey := openAIReauthorizationAutoEpisodeKey(account)
	state := service.OpenAIReauthorizationStateFromAccount(account)
	if openAIReauthorizationAutoClaimAlreadyRecorded(state, episodeKey, now) {
		if state.LastResult == service.OpenAIReauthorizationResultRunning && (state.AutoLeaseUntil == nil || !now.Before(*state.AutoLeaseUntil)) {
			leaseUntil := now
			if state.AutoLeaseUntil != nil {
				leaseUntil = *state.AutoLeaseUntil
			}
			claim := service.OpenAIReauthorizationAutoClaim{
				EpisodeKey: state.AutoEpisodeKey, ClaimID: state.AutoClaimID,
				BrowserMode: state.AutoBrowserMode, ExecutionNodeID: state.AutoExecutionNodeID,
				ClaimedAt: dereferenceTime(state.AutoClaimedAt, now), LeaseUntil: leaseUntil,
				RestoreSchedulable: state.AutoRestoreSchedulable,
			}
			next := state
			next.LastResult = service.OpenAIReauthorizationResultFailed
			next.LastReason = "worker_interrupted"
			next.LastResultAt = &now
			next.AutoFinishedAt = &now
			next.AutoLeaseUntil = nil
			next.LastEventKey = claim.ClaimID + ":failed:worker_interrupted"
			next.Normalize()
			_, _ = store.FinishOpenAIReauthorizationAutoAttempt(ctx, account, claim, next)
		}
		return nil
	}
	claimID, err := newTeamChildBrowserToken()
	if err != nil {
		return err
	}
	restoreSchedulable := !account.Schedulable && account.Status == service.StatusError
	claim := service.OpenAIReauthorizationAutoClaim{
		EpisodeKey: episodeKey, ClaimID: claimID, BrowserMode: mode,
		ExecutionNodeID: accountNodeID, ClaimedAt: now,
		LeaseUntil: now.Add(openAIReauthorizationAutoLease), RestoreSchedulable: restoreSchedulable,
	}
	claimedState, claimed, err := store.ClaimOpenAIReauthorizationAutoAttempt(ctx, account, claim)
	if err != nil {
		return err
	}
	if !claimed {
		return nil
	}
	claimState := claimedState
	if claimState.AutoClaimID == "" {
		claimState = service.OpenAIReauthorizationStateFromAccount(account)
	}

	account, login, proxy, err := h.openAIReauthorizationLogin(ctx, accountID, mode)
	if err != nil {
		return h.finishOpenAIReauthorizationAutoFailure(ctx, store, account, claim, claimState, "saved_login_unavailable", nil)
	}
	ownerID := account.ID
	if mode == batchOAuthBrowserAdsPower {
		if h.adsPowerLaunchStore == nil {
			return h.finishOpenAIReauthorizationAutoFailure(ctx, store, account, claim, claimState, "adspower_helper_offline", nil)
		}
		binding := service.OpenAIAdsPowerBindingFromAccount(account)
		device, deviceOK, deviceErr := h.adsPowerLaunchStore.device(ctx, binding.DeviceID, binding.EnvironmentKey)
		if deviceErr != nil || !deviceOK || device.LastSeenAt.IsZero() || time.Since(device.LastSeenAt) > openAIReauthorizationAutoHelperFresh {
			return h.finishOpenAIReauthorizationAutoFailure(ctx, store, account, claim, claimState, "adspower_helper_offline", nil)
		}
		ownerID = device.AdminUserID
		if ownerID <= 0 {
			return h.finishOpenAIReauthorizationAutoFailure(ctx, store, account, claim, claimState, "adspower_helper_offline", nil)
		}
	}
	if ownerID <= 0 {
		ownerID = account.ID
	}

	taskID, err := newTeamChildBrowserToken()
	if err != nil {
		return h.finishOpenAIReauthorizationAutoFailure(ctx, store, account, claim, claimState, "automation_start_failed", nil)
	}
	taskConfig := openAIReauthorizationAccountConfig(account)
	taskConfig.BrowserMode = mode
	task := &batchOAuthTask{
		ID: taskID, Mode: batchOAuthModeReauthorization, Email: login.Email, LoginMethod: login.Method,
		TargetAccountID: account.ID, ownerID: ownerID, sidecarID: taskID, config: taskConfig,
		BrowserMode: mode, executionNodeID: accountNodeID, Status: "queued", Stage: "queued",
		CreatedAt: now, ExpiresAt: now.Add(30 * time.Minute), ReauthorizationNumber: max(1, claimState.AttemptCount),
		reauthStateEvent: claimState.LastEventKey,
	}
	task.snapshotJSONLocked()
	task.mu.Lock()
	h.batchOAuthStore.mu.Lock()
	h.batchOAuthStore.tasks[task.ID] = task
	h.batchOAuthStore.mu.Unlock()
	startCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 60*time.Second)
	startErr := h.startBatchAttempt(startCtx, task, login.Password, login.TOTPSecret, login.EmailCodeToken, proxy)
	if startErr != nil {
		cancel()
		task.markFinishedIfTerminal()
		task.mu.Unlock()
		return h.finishOpenAIReauthorizationAutoFailure(ctx, store, account, claim, claimState, task.Reason, task)
	}
	if mode == batchOAuthBrowserAdsPower {
		if err := h.startOpenAIReauthorizationAutoAdsPower(startCtx, task, account); err != nil {
			cancel()
			task.Status, task.Stage, task.Reason = "failed", "failed", "adspower_helper_offline"
			task.markFinishedIfTerminal()
			task.mu.Unlock()
			return h.finishOpenAIReauthorizationAutoFailure(ctx, store, account, claim, claimState, task.Reason, task)
		}
	}
	cancel()
	task.snapshotJSONLocked()
	task.mu.Unlock()

	autoTask := &openAIReauthorizationAutoTask{task: task, expected: account, claim: claim}
	runtime.mu.Lock()
	runtime.tasks[task.ID] = autoTask
	runtime.mu.Unlock()
	runtime.wg.Add(1)
	go func() {
		defer runtime.wg.Done()
		h.monitorOpenAIReauthorizationAutoTask(runtime, autoTask)
	}()
	return nil
}

func (h *OpenAIOAuthHandler) monitorOpenAIReauthorizationAutoTask(runtime *openAIReauthorizationAutoRuntime, item *openAIReauthorizationAutoTask) {
	if runtime == nil || item == nil || item.task == nil {
		return
	}
	defer func() {
		runtime.mu.Lock()
		delete(runtime.tasks, item.task.ID)
		runtime.mu.Unlock()
	}()
	deadline := time.Now().Add(openAIReauthorizationAutoMaxRuntime)
	ticker := time.NewTicker(openAIReauthorizationAutoPollInterval)
	defer ticker.Stop()
	for time.Now().Before(deadline) {
		if runtime.ctx.Err() != nil {
			return
		}
		item.task.mu.Lock()
		if item.task.terminal() {
			reason := item.task.Reason
			item.task.mu.Unlock()
			if reason == "" && item.completed {
				return
			}
			_ = h.finishOpenAIReauthorizationAutoFailure(context.Background(), mustAutoStore(h), item.expected, item.claim, service.OpenAIReauthorizationStateFromAccount(item.expected), reasonOr(reason, "automation_failed"), item.task)
			return
		}
		refreshCtx, cancel := context.WithTimeout(context.WithoutCancel(runtime.ctx), 12*time.Second)
		sidecar, refreshErr := item.task.refresh(refreshCtx)
		if sidecar != nil && strings.TrimSpace(sidecar.CallbackURL) != "" {
			item.task.externalCallbackURL = strings.TrimSpace(sidecar.CallbackURL)
		}
		ready := item.task.Status == "ready"
		human := item.task.Stage == "phone_required" || item.task.Stage == "sms_waiting" || item.task.Reason == "reauthorization_phone_required" || item.task.Reason == "manual_challenge" || item.task.Reason == "captcha_required" || item.task.Reason == "captcha"
		reason := item.task.Reason
		item.task.mu.Unlock()
		cancel()
		if refreshErr != nil {
			select {
			case <-ticker.C:
				continue
			case <-runtime.ctx.Done():
				return
			}
		}
		if human {
			item.task.mu.Lock()
			if !item.task.usesAdsPower() {
				_, _ = item.task.sidecar(context.WithoutCancel(runtime.ctx), "cancel", nil)
			}
			item.task.Status, item.task.Stage = "failed", "failed"
			if reason == "" {
				reason = "reauthorization_phone_required"
			}
			item.task.Reason = reason
			item.task.markFinishedIfTerminal()
			item.task.mu.Unlock()
			_ = h.finishOpenAIReauthorizationAutoFailure(runtime.ctx, mustAutoStore(h), item.expected, item.claim, service.OpenAIReauthorizationStateFromAccount(item.expected), reason, item.task)
			return
		}
		if ready {
			item.task.mu.Lock()
			err := h.completeOpenAIReauthorizationAutomatic(runtime.ctx, item)
			item.task.mu.Unlock()
			if err == nil && item.completed {
				return
			}
			return
		}
		select {
		case <-ticker.C:
		case <-runtime.ctx.Done():
			return
		}
	}
	item.task.mu.Lock()
	item.task.Status, item.task.Stage, item.task.Reason = "failed", "failed", "task_expired"
	item.task.markFinishedIfTerminal()
	item.task.mu.Unlock()
	_ = h.finishOpenAIReauthorizationAutoFailure(runtime.ctx, mustAutoStore(h), item.expected, item.claim, service.OpenAIReauthorizationStateFromAccount(item.expected), "task_expired", item.task)
}

func mustAutoStore(h *OpenAIOAuthHandler) openAIReauthorizationAutoStore {
	store, _ := openAIReauthorizationAutoStoreFor(h)
	return store
}

func reasonOr(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func dereferenceTime(value *time.Time, fallback time.Time) time.Time {
	if value == nil || value.IsZero() {
		return fallback
	}
	return value.UTC()
}

func (h *OpenAIOAuthHandler) finishOpenAIReauthorizationAutoFailure(ctx context.Context, store openAIReauthorizationAutoStore, expected *service.Account, claim service.OpenAIReauthorizationAutoClaim, current service.OpenAIReauthorizationState, reason string, task *batchOAuthTask) error {
	if store == nil || expected == nil {
		return errors.New("automatic reauthorization history storage is unavailable")
	}
	now := time.Now().UTC()
	state := current
	state.LastResult = service.OpenAIReauthorizationResultFailed
	state.LastReason = reasonOr(reason, "automation_failed")
	state.LastResultAt = &now
	state.LastInitiator = service.OpenAIReauthorizationInitiatorAutomatic
	state.LastEventKey = claim.ClaimID + ":failed:" + state.LastReason
	state.AutoFinishedAt = &now
	state.AutoLeaseUntil = nil
	state.Normalize()
	_, err := store.FinishOpenAIReauthorizationAutoAttempt(ctx, expected, claim, state)
	if task != nil {
		h.revokeTerminalBatchSession(task)
	}
	return err
}

func (h *OpenAIOAuthHandler) completeOpenAIReauthorizationAutomatic(ctx context.Context, item *openAIReauthorizationAutoTask) error {
	if item == nil || item.task == nil || item.expected == nil {
		return errors.New("automatic reauthorization task is invalid")
	}
	task := item.task
	if task.AccountID > 0 && task.Status == "completed" {
		item.completed = true
		return nil
	}
	if task.Status != "ready" {
		return errors.New("OAuth is not complete")
	}
	store, ok := openAIReauthorizationAutoStoreFor(h)
	if !ok {
		return errors.New("automatic reauthorization CAS storage is unavailable")
	}
	account, err := h.adminService.GetAccount(ctx, task.TargetAccountID)
	if err != nil || account == nil || !account.IsOpenAIOAuth() || account.IsCredentialShadow() || account.IsOpenAIOAuthCredentialCopy() {
		task.Status, task.Stage, task.Reason = "failed", "failed", "account_update_failed"
		return h.finishOpenAIReauthorizationAutoFailure(ctx, store, item.expected, item.claim, service.OpenAIReauthorizationStateFromAccount(item.expected), task.Reason, task)
	}
	if openAIReauthorizationAutoEpisodeKey(account) != item.claim.EpisodeKey || openAIReauthorizationConfigurationChanged(task, account, strings.TrimSpace(os.Getenv("GATEWAY_EXECUTION_NODE_ID"))) {
		task.Status, task.Stage, task.Reason = "failed", "failed", "account_configuration_changed"
		return h.finishOpenAIReauthorizationAutoFailure(ctx, store, item.expected, item.claim, service.OpenAIReauthorizationStateFromAccount(item.expected), task.Reason, task)
	}
	email, _, _ := openAIAccountReauthorizationLogin(account)
	if email == "" || !strings.EqualFold(email, task.Email) {
		task.Status, task.Stage, task.Reason = "failed", "failed", "oauth_identity_mismatch"
		return h.finishOpenAIReauthorizationAutoFailure(ctx, store, item.expected, item.claim, service.OpenAIReauthorizationStateFromAccount(item.expected), task.Reason, task)
	}
	code, err := validateBatchCallback(task.externalCallbackURL, task.state)
	if err != nil {
		task.Status, task.Stage, task.Reason = "failed", "failed", "oauth_exchange_failed"
		return h.finishOpenAIReauthorizationAutoFailure(ctx, store, item.expected, item.claim, service.OpenAIReauthorizationStateFromAccount(item.expected), task.Reason, task)
	}
	exchangeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 60*time.Second)
	defer cancel()
	token, err := h.openaiOAuthService.ExchangeWorkflowCode(exchangeCtx, &service.OpenAIExchangeCodeInput{SessionID: task.sessionID, Code: code, State: task.state})
	h.openaiOAuthService.RevokeWorkflowSession(task.sessionID)
	if err != nil || token == nil || strings.TrimSpace(token.AccessToken) == "" || !strings.EqualFold(strings.TrimSpace(token.Email), task.Email) {
		task.Status, task.Stage, task.Reason = "failed", "failed", "oauth_identity_mismatch"
		if err != nil || token == nil || strings.TrimSpace(token.AccessToken) == "" {
			task.Reason = "oauth_exchange_failed"
		}
		return h.finishOpenAIReauthorizationAutoFailure(ctx, store, item.expected, item.claim, service.OpenAIReauthorizationStateFromAccount(item.expected), task.Reason, task)
	}
	if oldID := strings.TrimSpace(item.expected.GetCredential("chatgpt_account_id")); oldID != "" && strings.TrimSpace(token.ChatGPTAccountID) != "" && oldID != strings.TrimSpace(token.ChatGPTAccountID) {
		task.Status, task.Stage, task.Reason = "failed", "failed", "oauth_identity_mismatch"
		return h.finishOpenAIReauthorizationAutoFailure(ctx, store, item.expected, item.claim, service.OpenAIReauthorizationStateFromAccount(item.expected), task.Reason, task)
	}
	credentials := service.MergeCredentials(item.expected.Credentials, h.openaiOAuthService.BuildAccountCredentials(token))
	credentials[service.OpenAIOAuthReauthorizationEmailCredentialKey] = task.Email
	// The repository CAS protects against stale credentials, while this version
	// stamp invalidates token caches and lets late refresh results lose cleanly.
	nextTokenVersion := nowUnixMilli()
	if nextTokenVersion <= item.expected.GetCredentialAsInt64("_token_version") {
		nextTokenVersion = item.expected.GetCredentialAsInt64("_token_version") + 1
	}
	credentials["_token_version"] = nextTokenVersion
	now := time.Now().UTC()
	state := service.OpenAIReauthorizationStateFromAccount(item.expected)
	state.LastResult = service.OpenAIReauthorizationResultSuccess
	state.LastReason = ""
	state.LastResultAt = &now
	state.LastInitiator = service.OpenAIReauthorizationInitiatorAutomatic
	state.LastEventKey = item.claim.ClaimID + ":success"
	state.AutoFinishedAt = &now
	state.AutoLeaseUntil = nil
	state.SuccessCount++
	if state.FirstSucceededAt == nil {
		state.FirstSucceededAt = &now
	}
	state.LastSucceededAt = &now
	state.SuccessfulAuthorizationTimes = append(state.SuccessfulAuthorizationTimes, now)
	state.Normalize()
	applied, err := store.ApplyOpenAIReauthorizationAutoCredentials(ctx, item.expected, item.claim, credentials, state)
	token = nil
	if err != nil || !applied {
		task.Status, task.Stage, task.Reason = "failed", "failed", "account_configuration_changed"
		if err == nil {
			err = errors.New("automatic reauthorization credentials were superseded")
		}
		_ = h.finishOpenAIReauthorizationAutoFailure(ctx, store, item.expected, item.claim, state, task.Reason, task)
		return err
	}
	if h.rateLimitService != nil {
		if _, recoveryErr := h.rateLimitService.RecoverAccountState(ctx, account.ID, service.AccountRecoveryOptions{
			InvalidateToken: true,
			ForceCleanup:    true,
		}); recoveryErr != nil {
			task.Status, task.Stage, task.Reason = "failed", "failed", "account_state_recovery_failed"
			return h.finishOpenAIReauthorizationAutoFailure(ctx, store, item.expected, item.claim, state, task.Reason, task)
		}
	}
	item.completed = true
	item.accountUpdate = true
	updated, clearErr := h.adminService.ClearAccountError(ctx, account.ID)
	if clearErr == nil && item.claim.RestoreSchedulable {
		updated, clearErr = h.adminService.SetAccountSchedulable(ctx, account.ID, true)
	}
	if clearErr != nil || updated == nil {
		task.AccountID = account.ID
		task.Status, task.Stage, task.Reason = "failed", "failed", "account_state_recovery_failed"
		task.AccountConfig = &batchOAuthConfig{BrowserMode: task.BrowserMode}
		task.markFinishedIfTerminal()
		return nil
	}
	task.AccountID = updated.ID
	task.Status, task.Stage, task.Reason = "completed", "completed", ""
	config := openAIReauthorizationAccountConfig(updated)
	config.BrowserMode = task.BrowserMode
	task.AccountConfig = &config
	if h.tokenCacheInvalidator != nil {
		_ = h.tokenCacheInvalidator.InvalidateToken(ctx, item.expected)
		_ = h.tokenCacheInvalidator.InvalidateToken(ctx, updated)
	}
	task.markFinishedIfTerminal()
	return nil
}

func nowUnixMilli() int64 {
	return time.Now().UnixMilli()
}

// startOpenAIReauthorizationAutoAdsPower queues the same one-time helper
// command used by the interactive AdsPower button. It intentionally does not
// fall back to the built-in browser when the paired helper is absent.
func (h *OpenAIOAuthHandler) startOpenAIReauthorizationAutoAdsPower(ctx context.Context, task *batchOAuthTask, account *service.Account) error {
	if h == nil || h.adsPowerLaunchStore == nil || task == nil || account == nil {
		return errors.New("AdsPower automation unavailable")
	}
	binding := service.OpenAIAdsPowerBindingFromAccount(account)
	if binding == nil {
		return errors.New("AdsPower binding unavailable")
	}
	device, ok, err := h.adsPowerLaunchStore.device(ctx, binding.DeviceID, binding.EnvironmentKey)
	if err != nil || !ok || device.LastSeenAt.IsZero() || time.Since(device.LastSeenAt) > openAIReauthorizationAutoHelperFresh {
		return errors.New("AdsPower helper offline")
	}
	record := openAIAdsPowerLaunchRecord{
		AdminUserID: device.AdminUserID, AccountID: account.ID, AccountName: sanitizeOpenAIAdsPowerLabel(account.Name),
		SessionID: task.sessionID, AuthURL: task.authURL, EnvironmentKey: binding.EnvironmentKey,
		Existing: binding, TaskID: task.ID, TaskMode: task.normalizedMode(), LoginEmail: task.Email, LoginMethod: task.LoginMethod,
		PasswordEncrypted: task.loginPasswordEncrypted, TOTPEncrypted: task.loginTOTPEncrypted, EmailCodeEncrypted: task.loginEmailCodeEncrypted,
		CallbackExpiresAt: task.ExpiresAt,
	}
	ticket, err := newTeamChildBrowserToken()
	if err != nil {
		return err
	}
	record.BindingToken, err = newTeamChildBrowserToken()
	if err != nil {
		return err
	}
	record.CallbackToken, err = newTeamChildBrowserToken()
	if err != nil {
		return err
	}
	record.ExpiresAt = h.adsPowerLaunchStore.now().UTC().Add(openAIAdsPowerLaunchTTL)
	unlock, err := h.lockOpenAIAdsPowerProfileLifecycle(ctx, binding.DeviceID, binding.EnvironmentKey, binding.ProfileID)
	if err != nil {
		return err
	}
	defer unlock()
	if err := h.adsPowerLaunchStore.create(ctx, ticket, record); err != nil {
		return err
	}
	queued, err := h.adsPowerLaunchStore.enqueueHelperCommand(ctx, record, ticket, binding.EnvironmentKey)
	if err != nil || !queued {
		return errors.New("AdsPower helper command could not be queued")
	}
	task.adsPowerLaunchIssued = true
	task.adsPowerLaunchDelivery = "queued"
	task.adsPowerLaunchExpiresAt = record.ExpiresAt
	task.snapshotJSONLocked()
	return nil
}
