package admin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/handler/dto"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type openAIReauthorizationAuditReader interface {
	List(ctx context.Context, filter *service.AuditLogFilter) (*service.AuditLogList, error)
	GetByID(ctx context.Context, id int64) (*service.AuditLog, error)
}

type openAIReauthorizationStateUpdater interface {
	UpdateOpenAIReauthorizationState(ctx context.Context, id int64, state service.OpenAIReauthorizationState) error
}

type openAIReauthorizationAuthorizationRequest struct {
	Confirmed                             bool `json:"confirmed"`
	AcknowledgedSecondReauthorizationRisk bool `json:"acknowledged_second_reauthorization_risk"`
}

type openAIReauthorizationAccountStatus struct {
	Account                          *dto.Account `json:"account"`
	CurrentNeedsReauthorization      bool         `json:"current_needs_reauthorization"`
	CurrentAuthorizationNumber       int          `json:"current_authorization_number"`
	HasHistory                       bool         `json:"has_history"`
	HasAttempted                     bool         `json:"has_attempted"`
	HasReauthorized                  bool         `json:"has_reauthorized"`
	AttemptCount                     int          `json:"attempt_count"`
	SuccessCount                     int          `json:"success_count"`
	FirstAttemptAt                   *time.Time   `json:"first_attempt_at,omitempty"`
	LastAttemptAt                    *time.Time   `json:"last_attempt_at,omitempty"`
	FirstSucceededAt                 *time.Time   `json:"first_succeeded_at,omitempty"`
	LastSucceededAt                  *time.Time   `json:"last_succeeded_at,omitempty"`
	SuccessfulAuthorizationTimes     []time.Time  `json:"successful_authorization_times,omitempty"`
	LastResult                       string       `json:"last_result,omitempty"`
	LastReason                       string       `json:"last_reason,omitempty"`
	LastResultAt                     *time.Time   `json:"last_result_at,omitempty"`
	HistorySource                    string       `json:"history_source,omitempty"`
	HistoryConfidence                string       `json:"history_confidence,omitempty"`
	LegacyEvidenceCount              int          `json:"legacy_evidence_count,omitempty"`
	CooldownUntil                    *time.Time   `json:"cooldown_until,omitempty"`
	CooldownRemainingSeconds         int64        `json:"cooldown_remaining_seconds"`
	SecondsSinceFirstReauthorization int64        `json:"seconds_since_first_reauthorization,omitempty"`
	CanStart                         bool         `json:"can_start"`
	RequiresRiskConfirmation         bool         `json:"requires_risk_confirmation"`
	RiskLevel                        string       `json:"risk_level"`
}

type openAIReauthorizationAuditEvidence struct {
	Attempts  []time.Time
	Successes []time.Time
}

func (h *OpenAIOAuthHandler) ConfigureReauthorizationAuditReader(reader openAIReauthorizationAuditReader) {
	if h != nil {
		h.reauthAuditReader = reader
	}
}

func (h *OpenAIOAuthHandler) openAIReauthorizationState(ctx context.Context, account *service.Account) service.OpenAIReauthorizationState {
	state := service.OpenAIReauthorizationStateFromAccount(account)
	if account == nil || openAIReauthorizationStateIsAuthoritative(state) {
		return state
	}
	evidence := h.loadOpenAIReauthorizationAuditEvidence(ctx)
	if inferred := inferOpenAIReauthorizationState(account, evidence[account.ID]); inferred.HasHistory() {
		return inferred
	}
	return service.OpenAIReauthorizationState{Version: 1}
}

func openAIReauthorizationStateIsAuthoritative(state service.OpenAIReauthorizationState) bool {
	if state.LastResult == service.OpenAIReauthorizationResultLegacyUnknown ||
		state.HistoryConfidence == service.OpenAIReauthorizationHistoryUnknown {
		return false
	}
	return state.HasHistory() || state.IsTracked()
}

func (h *OpenAIOAuthHandler) validateOpenAIReauthorizationStart(ctx context.Context, account *service.Account, req openAIReauthorizationAuthorizationRequest, now time.Time) (service.OpenAIReauthorizationState, int, error) {
	if !req.Confirmed {
		return service.OpenAIReauthorizationState{}, 0, errors.New("请先确认本次 401 重新授权")
	}
	state := h.openAIReauthorizationState(ctx, account)
	if !openAIAccountNeedsReauthorization(account) {
		return state, 0, errors.New("该账号当前没有检测到需要重新授权的 401 状态")
	}
	if openAIReauthorizationBlocked(account, state) && !req.AcknowledgedSecondReauthorizationRisk {
		return state, 0, errors.New("OpenAI 页面显示该账号受限、删除或停用；如仍要手动授权，必须完成高风险二次确认")
	}
	if state.SuccessCount > 0 {
		if cooldownUntil := state.CooldownUntil(); cooldownUntil != nil && now.Before(*cooldownUntil) {
			if !req.AcknowledgedSecondReauthorizationRisk {
				return state, 0, fmt.Errorf("该账号仍在 7 天冷静期内，建议最早于 %s 再授权；如仍要继续，必须完成高风险二次确认", cooldownUntil.UTC().Format(time.RFC3339))
			}
		}
		if !req.AcknowledgedSecondReauthorizationRisk {
			return state, 0, errors.New("这是第二次或更多次 401 掉授权，建议不要继续授权；若仍要继续，必须完成高风险二次确认")
		}
	}
	return state, state.NextAuthorizationNumber(), nil
}

func (h *OpenAIOAuthHandler) persistOpenAIReauthorizationStart(ctx context.Context, account *service.Account, state service.OpenAIReauthorizationState, task *batchOAuthTask, now time.Time) error {
	updater, ok := h.adminService.(openAIReauthorizationStateUpdater)
	if !ok || updater == nil {
		return errors.New("OpenAI reauthorization history storage is unavailable")
	}
	number := task.ReauthorizationNumber
	if number <= 0 {
		number = state.NextAuthorizationNumber()
		task.ReauthorizationNumber = number
	}
	state.AttemptCount = max(state.AttemptCount, number)
	if state.FirstAttemptAt == nil {
		first := now.UTC()
		state.FirstAttemptAt = &first
	}
	last := now.UTC()
	state.LastAttemptAt = &last
	state.LastResult = service.OpenAIReauthorizationResultRunning
	state.LastReason = ""
	state.LastResultAt = &last
	state.LastEventKey = task.ID + ":running"
	state.HistorySource = "xiass_state"
	state.HistoryConfidence = service.OpenAIReauthorizationHistoryExact
	state.Normalize()
	if err := updater.UpdateOpenAIReauthorizationState(ctx, account.ID, state); err != nil {
		return err
	}
	task.reauthStateEvent = state.LastEventKey
	return nil
}

func (h *OpenAIOAuthHandler) persistOpenAIReauthorizationTaskState(ctx context.Context, task *batchOAuthTask) error {
	if h == nil || task == nil || !task.matchesMode(batchOAuthModeReauthorization) || !task.terminal() {
		return nil
	}
	result := service.OpenAIReauthorizationResultFailed
	if task.Status == "completed" || (task.AccountID > 0 && task.Reason == "account_state_recovery_failed") {
		result = service.OpenAIReauthorizationResultSuccess
	} else if task.Status == "blocked" || task.Reason == "account_blocked" {
		result = service.OpenAIReauthorizationResultBlocked
	} else if task.Status == "canceled" {
		result = service.OpenAIReauthorizationResultCanceled
	}
	eventKey := task.ID + ":" + result + ":" + task.Reason
	if task.reauthStateEvent == eventKey {
		return nil
	}
	account, err := h.adminService.GetAccount(ctx, task.TargetAccountID)
	if err != nil || account == nil {
		return errors.New("OpenAI account unavailable while saving reauthorization history")
	}
	state := service.OpenAIReauthorizationStateFromAccount(account)
	number := task.ReauthorizationNumber
	if number <= 0 {
		number = max(1, state.NextAuthorizationNumber())
	}
	state.AttemptCount = max(state.AttemptCount, number)
	now := time.Now().UTC()
	if task.FinishedAt != nil {
		now = task.FinishedAt.UTC()
	}
	if state.FirstAttemptAt == nil {
		first := task.CreatedAt.UTC()
		state.FirstAttemptAt = &first
	}
	lastAttempt := task.CreatedAt.UTC()
	if state.LastAttemptAt != nil && state.LastAttemptAt.After(lastAttempt) {
		lastAttempt = state.LastAttemptAt.UTC()
	}
	state.LastAttemptAt = &lastAttempt
	state.LastResult = result
	state.LastReason = task.Reason
	state.LastResultAt = &now
	state.LastEventKey = eventKey
	state.HistorySource = "xiass_state"
	state.HistoryConfidence = service.OpenAIReauthorizationHistoryExact
	if result == service.OpenAIReauthorizationResultSuccess {
		state.SuccessCount = max(state.SuccessCount, number)
		if state.FirstSucceededAt == nil {
			first := now
			state.FirstSucceededAt = &first
		}
		last := now
		state.LastSucceededAt = &last
		state.SuccessfulAuthorizationTimes = append(state.SuccessfulAuthorizationTimes, last)
	}
	state.Normalize()
	updater, ok := h.adminService.(openAIReauthorizationStateUpdater)
	if !ok || updater == nil {
		return errors.New("OpenAI reauthorization history storage is unavailable")
	}
	if err := updater.UpdateOpenAIReauthorizationState(ctx, account.ID, state); err != nil {
		return err
	}
	task.reauthStateEvent = eventKey
	return nil
}

func (h *OpenAIOAuthHandler) ListOpenAIReauthorizationAccounts(c *gin.Context) {
	if !requireTeamChildAdminSession(c) {
		return
	}
	requested := parseOpenAIReauthorizationAccountIDs(c.Query("account_ids"))
	accounts, err := h.listPrimaryOpenAIOAuthAccounts(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	var evidence map[int64]openAIReauthorizationAuditEvidence
	evidenceLoaded := false
	now := time.Now().UTC()
	items := make([]openAIReauthorizationAccountStatus, 0, len(accounts))
	for i := range accounts {
		account := &accounts[i]
		needsReauthorization := openAIAccountNeedsReauthorization(account)
		state := service.OpenAIReauthorizationStateFromAccount(account)
		if !openAIReauthorizationStateIsAuthoritative(state) {
			if !evidenceLoaded {
				evidence = h.loadOpenAIReauthorizationAuditEvidence(c.Request.Context())
				evidenceLoaded = true
			}
			if inferred := inferOpenAIReauthorizationState(account, evidence[account.ID]); inferred.HasHistory() {
				state = inferred
			} else {
				state = service.OpenAIReauthorizationState{Version: 1}
			}
		}
		if !needsReauthorization && !state.HasHistory() {
			if _, explicitlyRequested := requested[account.ID]; !explicitlyRequested {
				continue
			}
		}
		items = append(items, buildOpenAIReauthorizationAccountStatus(account, state, needsReauthorization, now))
	}
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].CurrentNeedsReauthorization != items[j].CurrentNeedsReauthorization {
			return items[i].CurrentNeedsReauthorization
		}
		return items[i].Account.ID < items[j].Account.ID
	})
	response.Success(c, gin.H{"items": items, "cooldown_seconds": int64(service.OpenAIReauthorizationCooldown.Seconds())})
}

func (h *OpenAIOAuthHandler) listPrimaryOpenAIOAuthAccounts(ctx context.Context) ([]service.Account, error) {
	const pageSize = 200
	all := make([]service.Account, 0, pageSize)
	for page := 1; ; page++ {
		items, total, err := h.adminService.ListAccounts(ctx, page, pageSize, service.PlatformOpenAI, service.AccountTypeOAuth, "", "", 0, "", "id", "asc")
		if err != nil {
			return nil, err
		}
		for i := range items {
			if items[i].IsOpenAIOAuth() && !items[i].IsCredentialShadow() {
				all = append(all, items[i])
			}
		}
		if len(items) == 0 || int64(page*pageSize) >= total {
			break
		}
	}
	return all, nil
}

func parseOpenAIReauthorizationAccountIDs(raw string) map[int64]struct{} {
	result := make(map[int64]struct{})
	for _, value := range strings.Split(raw, ",") {
		id, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
		if err == nil && id > 0 {
			result[id] = struct{}{}
		}
	}
	return result
}

func openAIAccountNeedsReauthorization(account *service.Account) bool {
	if account == nil || !account.IsOpenAIOAuth() || account.IsCredentialShadow() || account.Status != service.StatusError {
		return false
	}
	if value, ok := account.Extra["needs_reauth"].(bool); ok && value {
		return true
	}
	parts := []string{account.ErrorMessage, account.GetExtraString("error"), account.GetExtraString("error_code")}
	text := strings.ToLower(strings.Join(parts, " "))
	return strings.Contains(text, "401") || strings.Contains(text, "unauthorized") || strings.Contains(text, "unauthorised") ||
		strings.Contains(text, "token expired") || strings.Contains(text, "token invalid") || strings.Contains(text, "token 失效") || strings.Contains(text, "token 过期")
}

func openAIReauthorizationBlocked(account *service.Account, state service.OpenAIReauthorizationState) bool {
	if state.LastResult == service.OpenAIReauthorizationResultBlocked ||
		state.LastReason == "account_blocked" || state.LastReason == "account_deleted_or_disabled" {
		return true
	}
	if account == nil {
		return false
	}
	text := strings.ToLower(account.ErrorMessage + " " + account.GetExtraString("error") + " " + account.GetExtraString("error_code"))
	for _, marker := range []string{"account_blocked", "account blocked", "account_deleted_or_disabled", "deactivated", "disabled", "suspended", "封号", "账号受限", "账号已删除", "账号已停用"} {
		if strings.Contains(text, marker) {
			return true
		}
	}
	return false
}

func buildOpenAIReauthorizationAccountStatus(account *service.Account, state service.OpenAIReauthorizationState, needsReauthorization bool, now time.Time) openAIReauthorizationAccountStatus {
	state.Normalize()
	status := openAIReauthorizationAccountStatus{
		Account: dto.AccountFromService(account), CurrentNeedsReauthorization: needsReauthorization,
		CurrentAuthorizationNumber: state.NextAuthorizationNumber(), HasHistory: state.HasHistory(),
		HasAttempted: state.AttemptCount > 0, HasReauthorized: state.SuccessCount > 0,
		AttemptCount: state.AttemptCount, SuccessCount: state.SuccessCount,
		FirstAttemptAt: state.FirstAttemptAt, LastAttemptAt: state.LastAttemptAt,
		FirstSucceededAt: state.FirstSucceededAt, LastSucceededAt: state.LastSucceededAt,
		SuccessfulAuthorizationTimes: append([]time.Time(nil), state.SuccessfulAuthorizationTimes...),
		LastResult:                   state.LastResult, LastReason: state.LastReason, LastResultAt: state.LastResultAt,
		HistorySource: state.HistorySource, HistoryConfidence: state.HistoryConfidence,
		LegacyEvidenceCount: state.LegacyEvidenceCount,
	}
	if state.FirstSucceededAt != nil && now.After(*state.FirstSucceededAt) {
		status.SecondsSinceFirstReauthorization = int64(now.Sub(*state.FirstSucceededAt).Seconds())
	}
	if state.SuccessCount > 0 {
		status.CooldownUntil = state.CooldownUntil()
		if status.CooldownUntil != nil && now.Before(*status.CooldownUntil) {
			status.CooldownRemainingSeconds = int64(status.CooldownUntil.Sub(now).Seconds())
		}
	}
	blocked := openAIReauthorizationBlocked(account, state)
	status.RequiresRiskConfirmation = needsReauthorization && (state.SuccessCount > 0 || blocked)
	status.CanStart = needsReauthorization
	switch {
	case blocked:
		status.RiskLevel = "blocked"
	case needsReauthorization && status.CooldownRemainingSeconds > 0:
		status.RiskLevel = "cooldown"
	case needsReauthorization && state.SuccessCount > 0:
		status.RiskLevel = "repeated"
	case needsReauthorization:
		status.RiskLevel = "first"
	case state.LastResult == service.OpenAIReauthorizationResultSuccess:
		status.RiskLevel = "success"
	case state.LastResult == service.OpenAIReauthorizationResultFailed || state.LastResult == service.OpenAIReauthorizationResultCanceled:
		status.RiskLevel = "failed"
	default:
		status.RiskLevel = "history"
	}
	return status
}

func inferOpenAIReauthorizationState(account *service.Account, evidence openAIReauthorizationAuditEvidence) service.OpenAIReauthorizationState {
	state := service.OpenAIReauthorizationState{Version: 1}
	if len(evidence.Attempts) == 0 && len(evidence.Successes) == 0 {
		return state
	}
	evidence.Attempts = uniqueSortedReauthorizationTimes(evidence.Attempts)
	evidence.Successes = uniqueSortedReauthorizationTimes(evidence.Successes)
	allEvents := append(append([]time.Time(nil), evidence.Attempts...), evidence.Successes...)
	allEvents = uniqueSortedReauthorizationTimes(allEvents)
	first := allEvents[0].UTC()
	last := allEvents[len(allEvents)-1].UTC()
	state.AttemptCount = max(len(evidence.Attempts), len(evidence.Successes))
	state.FirstAttemptAt = &first
	state.LastAttemptAt = &last
	state.LastResult = service.OpenAIReauthorizationResultLegacyUnknown
	state.LastResultAt = &last
	state.HistorySource = "audit_log"
	state.HistoryConfidence = service.OpenAIReauthorizationHistoryInferred
	state.LegacyEvidenceCount = len(allEvents)
	if len(evidence.Successes) > 0 {
		firstSuccess := evidence.Successes[0].UTC()
		lastSuccess := evidence.Successes[len(evidence.Successes)-1].UTC()
		state.SuccessCount = len(evidence.Successes)
		state.FirstSucceededAt = &firstSuccess
		state.LastSucceededAt = &lastSuccess
		state.SuccessfulAuthorizationTimes = append([]time.Time(nil), evidence.Successes...)
		state.LastResult = service.OpenAIReauthorizationResultSuccess
		state.LastResultAt = &lastSuccess
	} else if account != nil && account.LastUsedAt != nil && account.LastUsedAt.After(first) {
		candidate := first
		for _, attempt := range evidence.Attempts {
			if account.LastUsedAt.After(attempt) {
				candidate = attempt.UTC()
			}
		}
		state.SuccessCount = 1
		state.FirstSucceededAt = &candidate
		state.LastSucceededAt = &candidate
		state.SuccessfulAuthorizationTimes = []time.Time{candidate}
		state.LastResult = service.OpenAIReauthorizationResultSuccess
		state.LastResultAt = &candidate
	}
	if openAIReauthorizationBlocked(account, state) {
		state.LastResult = service.OpenAIReauthorizationResultBlocked
		state.LastReason = "account_blocked"
		state.HistoryConfidence = service.OpenAIReauthorizationHistoryInferred
	}
	state.Normalize()
	return state
}

func uniqueSortedReauthorizationTimes(values []time.Time) []time.Time {
	sort.Slice(values, func(i, j int) bool { return values[i].Before(values[j]) })
	result := make([]time.Time, 0, len(values))
	for _, value := range values {
		value = value.UTC()
		if len(result) > 0 && result[len(result)-1].Equal(value) {
			continue
		}
		result = append(result, value)
	}
	return result
}

func (h *OpenAIOAuthHandler) loadOpenAIReauthorizationAuditEvidence(ctx context.Context) map[int64]openAIReauthorizationAuditEvidence {
	result := make(map[int64]openAIReauthorizationAuditEvidence)
	if h == nil || h.reauthAuditReader == nil {
		return result
	}
	success := true
	filters := []service.AuditLogFilter{
		{Action: "reauthor", Method: "POST", Success: &success},
		{Query: "apply-oauth-credentials", Method: "POST", Success: &success},
	}
	seen := make(map[int64]struct{})
	for _, baseFilter := range filters {
		for page := 1; page <= 10; page++ {
			filter := baseFilter
			filter.Page, filter.PageSize = page, 200
			list, err := h.reauthAuditReader.List(ctx, &filter)
			if err != nil || list == nil {
				break
			}
			for _, entry := range list.Logs {
				if entry == nil {
					continue
				}
				if _, duplicate := seen[entry.ID]; duplicate {
					continue
				}
				if !isOpenAIReauthorizationStartAudit(entry) && !isOpenAIReauthorizationCredentialAudit(entry) {
					continue
				}
				seen[entry.ID] = struct{}{}
				accountID := openAIReauthorizationAuditAccountID(entry)
				if accountID == 0 {
					detail, detailErr := h.reauthAuditReader.GetByID(ctx, entry.ID)
					if detailErr == nil {
						accountID = openAIReauthorizationAuditAccountID(detail)
					}
				}
				if accountID > 0 {
					evidence := result[accountID]
					if isOpenAIReauthorizationCredentialAudit(entry) {
						evidence.Successes = append(evidence.Successes, entry.CreatedAt.UTC())
					} else {
						evidence.Attempts = append(evidence.Attempts, entry.CreatedAt.UTC())
					}
					result[accountID] = evidence
				}
			}
			if len(list.Logs) == 0 || page*list.PageSize >= list.Total {
				break
			}
		}
	}
	return result
}

func isOpenAIReauthorizationStartAudit(entry *service.AuditLog) bool {
	if entry == nil || entry.Method != "POST" || entry.StatusCode >= 400 {
		return false
	}
	if entry.Path == "/api/v1/admin/openai/reauthorization/tasks" {
		return true
	}
	return entry.Path == "/api/v1/admin/openai/accounts/:id/reauthorize" ||
		entry.Path == "/api/v1/admin/openai/team-child/accounts/:account_id/reauthorize"
}

func isOpenAIReauthorizationCredentialAudit(entry *service.AuditLog) bool {
	return entry != nil && entry.Method == "POST" && entry.StatusCode < 400 &&
		entry.Path == "/api/v1/admin/accounts/:id/apply-oauth-credentials"
}

func openAIReauthorizationAuditAccountID(entry *service.AuditLog) int64 {
	if entry == nil {
		return 0
	}
	if id := auditScalarInt64(entry.Extra["account_id"]); id > 0 {
		return id
	}
	if params, ok := entry.Extra["params"].(map[string]any); ok {
		for _, key := range []string{"id", "account_id"} {
			if id := auditScalarInt64(params[key]); id > 0 {
				return id
			}
		}
	}
	if params, ok := entry.Extra["params"].(map[string]string); ok {
		for _, key := range []string{"id", "account_id"} {
			if id := auditScalarInt64(params[key]); id > 0 {
				return id
			}
		}
	}
	var body struct {
		AccountID int64 `json:"account_id"`
	}
	if json.Unmarshal([]byte(entry.RequestBody), &body) == nil && body.AccountID > 0 {
		return body.AccountID
	}
	return 0
}

func auditScalarInt64(value any) int64 {
	switch typed := value.(type) {
	case int64:
		return typed
	case int:
		return int64(typed)
	case float64:
		return int64(typed)
	case json.Number:
		parsed, _ := typed.Int64()
		return parsed
	case string:
		parsed, _ := strconv.ParseInt(strings.TrimSpace(typed), 10, 64)
		return parsed
	default:
		return 0
	}
}
