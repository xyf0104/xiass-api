package service

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"
)

const (
	// OpenAIReauthorizationStateExtraKey is XIASS-managed account history. It
	// must survive ordinary account edits and must not be accepted from generic
	// bulk-edit payloads.
	OpenAIReauthorizationStateExtraKey = "xiass_openai_reauthorization_state"

	OpenAIReauthorizationResultRunning       = "running"
	OpenAIReauthorizationResultSuccess       = "success"
	OpenAIReauthorizationResultFailed        = "failed"
	OpenAIReauthorizationResultBlocked       = "blocked"
	OpenAIReauthorizationResultCanceled      = "canceled"
	OpenAIReauthorizationResultLegacyUnknown = "legacy_unknown"

	OpenAIReauthorizationHistoryExact    = "exact"
	OpenAIReauthorizationHistoryInferred = "inferred"
	OpenAIReauthorizationHistoryUnknown  = "unknown"

	// OpenAIReauthorizationBrowserModeExtraKey records the browser selected when
	// XIASS created the account. Older accounts do not have this key; callers may
	// infer AdsPower from its verified profile binding and otherwise use the local
	// server browser.
	OpenAIReauthorizationBrowserModeExtraKey = "xiass_openai_reauthorization_browser_mode"

	OpenAIReauthorizationInitiatorManual    = "manual"
	OpenAIReauthorizationInitiatorAutomatic = "automatic"
)

const OpenAIReauthorizationCooldown = 7 * 24 * time.Hour

// OpenAIReauthorizationState records only operational metadata. Login
// passwords, mailbox tokens and OAuth credentials remain in their existing
// encrypted storage and never enter this document.
type OpenAIReauthorizationState struct {
	Version                      int         `json:"version"`
	TrackingStartedAt            *time.Time  `json:"tracking_started_at,omitempty"`
	AttemptCount                 int         `json:"attempt_count"`
	SuccessCount                 int         `json:"success_count"`
	FirstAttemptAt               *time.Time  `json:"first_attempt_at,omitempty"`
	LastAttemptAt                *time.Time  `json:"last_attempt_at,omitempty"`
	FirstSucceededAt             *time.Time  `json:"first_succeeded_at,omitempty"`
	LastSucceededAt              *time.Time  `json:"last_succeeded_at,omitempty"`
	SuccessfulAuthorizationTimes []time.Time `json:"successful_authorization_times,omitempty"`
	LastResult                   string      `json:"last_result,omitempty"`
	LastReason                   string      `json:"last_reason,omitempty"`
	LastResultAt                 *time.Time  `json:"last_result_at,omitempty"`
	LastEventKey                 string      `json:"last_event_key,omitempty"`
	HistorySource                string      `json:"history_source,omitempty"`
	HistoryConfidence            string      `json:"history_confidence,omitempty"`
	LegacyEvidenceCount          int         `json:"legacy_evidence_count,omitempty"`
	LastInitiator                string      `json:"last_initiator,omitempty"`
	AutoEpisodeKey               string      `json:"auto_episode_key,omitempty"`
	AutoClaimID                  string      `json:"auto_claim_id,omitempty"`
	AutoBrowserMode              string      `json:"auto_browser_mode,omitempty"`
	AutoExecutionNodeID          string      `json:"auto_execution_node_id,omitempty"`
	AutoClaimedAt                *time.Time  `json:"auto_claimed_at,omitempty"`
	AutoLeaseUntil               *time.Time  `json:"auto_lease_until,omitempty"`
	AutoFinishedAt               *time.Time  `json:"auto_finished_at,omitempty"`
	AutoRestoreSchedulable       bool        `json:"auto_restore_schedulable,omitempty"`
}

// OpenAIReauthorizationAutoClaim is the durable, non-secret claim written
// before an unattended browser is opened. The repository implementation must
// compare the complete expected account identity and the current state in one
// database statement so two XIASS instances cannot run the same 401 episode.
type OpenAIReauthorizationAutoClaim struct {
	EpisodeKey         string
	ClaimID            string
	BrowserMode        string
	ExecutionNodeID    string
	ClaimedAt          time.Time
	LeaseUntil         time.Time
	RestoreSchedulable bool
}

// OpenAIReauthorizationAutoRepository is intentionally optional. Production
// repositories implement these operations with conditional SQL; narrow tests
// and integrations that do not run unattended reauthorization remain source
// compatible. Credential application must also clear the persisted 401 block,
// restore schedulability only when RestoreSchedulable was claimed, and enqueue
// the normal scheduler invalidation in the same transaction.
type OpenAIReauthorizationAutoRepository interface {
	ClaimOpenAIReauthorizationAutoAttempt(ctx context.Context, expected *Account, claim OpenAIReauthorizationAutoClaim, next OpenAIReauthorizationState) (bool, error)
	FinishOpenAIReauthorizationAutoAttempt(ctx context.Context, expected *Account, claim OpenAIReauthorizationAutoClaim, next OpenAIReauthorizationState) (bool, error)
	ApplyOpenAIReauthorizationAutoCredentials(ctx context.Context, expected *Account, claim OpenAIReauthorizationAutoClaim, credentials map[string]any, next OpenAIReauthorizationState) (bool, error)
}

// ClaimOpenAIReauthorizationAutoAttempt and its completion counterparts are
// optional AdminService capabilities. Keeping them outside AdminService avoids
// forcing every focused test double to implement unattended reauthorization.
func (s *adminServiceImpl) ClaimOpenAIReauthorizationAutoAttempt(ctx context.Context, expected *Account, claim OpenAIReauthorizationAutoClaim) (OpenAIReauthorizationState, bool, error) {
	if s == nil || s.accountRepo == nil || expected == nil {
		return OpenAIReauthorizationState{}, false, errors.New("account repository is unavailable")
	}
	repo, ok := s.accountRepo.(OpenAIReauthorizationAutoRepository)
	if !ok {
		return OpenAIReauthorizationState{}, false, errors.New("OpenAI automatic reauthorization storage is unavailable")
	}
	state := OpenAIReauthorizationStateFromAccount(expected)
	now := claim.ClaimedAt.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
		claim.ClaimedAt = now
	}
	if claim.LeaseUntil.IsZero() {
		claim.LeaseUntil = now.Add(45 * time.Minute)
	}
	next := state
	next.AttemptCount = max(next.AttemptCount, next.NextAuthorizationNumber())
	if next.FirstAttemptAt == nil {
		next.FirstAttemptAt = cloneOpenAIReauthorizationTime(&now)
	}
	next.LastAttemptAt = cloneOpenAIReauthorizationTime(&now)
	next.LastResult = OpenAIReauthorizationResultRunning
	next.LastReason = ""
	next.LastResultAt = cloneOpenAIReauthorizationTime(&now)
	next.LastEventKey = claim.ClaimID + ":running"
	next.HistorySource = "xiass_state"
	next.HistoryConfidence = OpenAIReauthorizationHistoryExact
	next.LastInitiator = OpenAIReauthorizationInitiatorAutomatic
	next.AutoEpisodeKey = strings.TrimSpace(claim.EpisodeKey)
	next.AutoClaimID = strings.TrimSpace(claim.ClaimID)
	next.AutoBrowserMode = strings.ToLower(strings.TrimSpace(claim.BrowserMode))
	next.AutoExecutionNodeID = strings.TrimSpace(claim.ExecutionNodeID)
	next.AutoClaimedAt = cloneOpenAIReauthorizationTime(&now)
	next.AutoLeaseUntil = cloneOpenAIReauthorizationTime(&claim.LeaseUntil)
	next.AutoFinishedAt = nil
	next.AutoRestoreSchedulable = claim.RestoreSchedulable
	next.Normalize()
	applied, err := repo.ClaimOpenAIReauthorizationAutoAttempt(ctx, expected, claim, next)
	return next, applied, err
}

func (s *adminServiceImpl) FinishOpenAIReauthorizationAutoAttempt(ctx context.Context, expected *Account, claim OpenAIReauthorizationAutoClaim, next OpenAIReauthorizationState) (bool, error) {
	if s == nil || s.accountRepo == nil || expected == nil {
		return false, errors.New("account repository is unavailable")
	}
	repo, ok := s.accountRepo.(OpenAIReauthorizationAutoRepository)
	if !ok {
		return false, errors.New("OpenAI automatic reauthorization storage is unavailable")
	}
	return repo.FinishOpenAIReauthorizationAutoAttempt(ctx, expected, claim, next)
}

func (s *adminServiceImpl) ApplyOpenAIReauthorizationAutoCredentials(ctx context.Context, expected *Account, claim OpenAIReauthorizationAutoClaim, credentials map[string]any, next OpenAIReauthorizationState) (bool, error) {
	if s == nil || s.accountRepo == nil || expected == nil {
		return false, errors.New("account repository is unavailable")
	}
	repo, ok := s.accountRepo.(OpenAIReauthorizationAutoRepository)
	if !ok {
		return false, errors.New("OpenAI automatic reauthorization storage is unavailable")
	}
	return repo.ApplyOpenAIReauthorizationAutoCredentials(ctx, expected, claim, credentials, next)
}

func OpenAIReauthorizationStateFromAccount(account *Account) OpenAIReauthorizationState {
	if account == nil || account.Extra == nil {
		return OpenAIReauthorizationState{Version: 1}
	}
	return ParseOpenAIReauthorizationState(account.Extra[OpenAIReauthorizationStateExtraKey])
}

func ParseOpenAIReauthorizationState(raw any) OpenAIReauthorizationState {
	state := OpenAIReauthorizationState{Version: 1}
	if raw == nil {
		return state
	}
	encoded, err := json.Marshal(raw)
	if err != nil || json.Unmarshal(encoded, &state) != nil {
		return OpenAIReauthorizationState{Version: 1}
	}
	state.Normalize()
	return state
}

func (s *OpenAIReauthorizationState) Normalize() {
	if s == nil {
		return
	}
	s.Version = 1
	if s.AttemptCount < 0 {
		s.AttemptCount = 0
	}
	if s.SuccessCount < 0 {
		s.SuccessCount = 0
	}
	if s.SuccessCount > s.AttemptCount {
		s.AttemptCount = s.SuccessCount
	}
	if s.LegacyEvidenceCount < 0 {
		s.LegacyEvidenceCount = 0
	}
	s.LastResult = normalizeOpenAIReauthorizationResult(s.LastResult)
	s.LastReason = strings.TrimSpace(s.LastReason)
	s.LastEventKey = strings.TrimSpace(s.LastEventKey)
	s.HistorySource = strings.TrimSpace(s.HistorySource)
	s.HistoryConfidence = normalizeOpenAIReauthorizationConfidence(s.HistoryConfidence)
	s.LastInitiator = normalizeOpenAIReauthorizationInitiator(s.LastInitiator)
	s.AutoEpisodeKey = strings.TrimSpace(s.AutoEpisodeKey)
	s.AutoClaimID = strings.TrimSpace(s.AutoClaimID)
	s.AutoBrowserMode = strings.ToLower(strings.TrimSpace(s.AutoBrowserMode))
	s.AutoExecutionNodeID = strings.TrimSpace(s.AutoExecutionNodeID)
	s.TrackingStartedAt = normalizedOpenAIReauthorizationTime(s.TrackingStartedAt)
	s.FirstAttemptAt = normalizedOpenAIReauthorizationTime(s.FirstAttemptAt)
	s.LastAttemptAt = normalizedOpenAIReauthorizationTime(s.LastAttemptAt)
	s.FirstSucceededAt = normalizedOpenAIReauthorizationTime(s.FirstSucceededAt)
	s.LastSucceededAt = normalizedOpenAIReauthorizationTime(s.LastSucceededAt)
	s.LastResultAt = normalizedOpenAIReauthorizationTime(s.LastResultAt)
	s.AutoClaimedAt = normalizedOpenAIReauthorizationTime(s.AutoClaimedAt)
	s.AutoLeaseUntil = normalizedOpenAIReauthorizationTime(s.AutoLeaseUntil)
	s.AutoFinishedAt = normalizedOpenAIReauthorizationTime(s.AutoFinishedAt)
	times := append([]time.Time(nil), s.SuccessfulAuthorizationTimes...)
	if s.FirstSucceededAt != nil {
		times = append(times, s.FirstSucceededAt.UTC())
	}
	if s.LastSucceededAt != nil {
		times = append(times, s.LastSucceededAt.UTC())
	}
	s.SuccessfulAuthorizationTimes = normalizeOpenAIReauthorizationTimes(times)
	if len(s.SuccessfulAuthorizationTimes) > 0 {
		first := s.SuccessfulAuthorizationTimes[0]
		last := s.SuccessfulAuthorizationTimes[len(s.SuccessfulAuthorizationTimes)-1]
		s.FirstSucceededAt = &first
		s.LastSucceededAt = &last
		if s.SuccessCount < len(s.SuccessfulAuthorizationTimes) {
			s.SuccessCount = len(s.SuccessfulAuthorizationTimes)
		}
		if s.AttemptCount < s.SuccessCount {
			s.AttemptCount = s.SuccessCount
		}
	}
	if s.FirstAttemptAt != nil && s.LastAttemptAt != nil && s.LastAttemptAt.Before(*s.FirstAttemptAt) {
		s.LastAttemptAt = cloneOpenAIReauthorizationTime(s.FirstAttemptAt)
	}
	if s.FirstSucceededAt != nil && s.LastSucceededAt != nil && s.LastSucceededAt.Before(*s.FirstSucceededAt) {
		s.LastSucceededAt = cloneOpenAIReauthorizationTime(s.FirstSucceededAt)
	}
}

func normalizeOpenAIReauthorizationInitiator(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case OpenAIReauthorizationInitiatorManual, OpenAIReauthorizationInitiatorAutomatic:
		return strings.ToLower(strings.TrimSpace(value))
	default:
		return ""
	}
}

func normalizeOpenAIReauthorizationTimes(values []time.Time) []time.Time {
	result := make([]time.Time, 0, len(values))
	for _, value := range values {
		if value.IsZero() {
			continue
		}
		result = append(result, value.UTC())
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Before(result[j]) })
	unique := result[:0]
	for _, value := range result {
		if len(unique) > 0 && unique[len(unique)-1].Equal(value) {
			continue
		}
		unique = append(unique, value)
	}
	return unique
}

func (s OpenAIReauthorizationState) IsTracked() bool {
	return s.TrackingStartedAt != nil && s.HistoryConfidence == OpenAIReauthorizationHistoryExact
}

func (s OpenAIReauthorizationState) HasHistory() bool {
	return s.AttemptCount > 0 || s.SuccessCount > 0 || s.FirstAttemptAt != nil ||
		s.FirstSucceededAt != nil || s.LastResult != "" || s.LegacyEvidenceCount > 0
}

func (s OpenAIReauthorizationState) NextAuthorizationNumber() int {
	if s.SuccessCount < 0 {
		return 1
	}
	return s.SuccessCount + 1
}

func (s OpenAIReauthorizationState) CooldownAnchor() *time.Time {
	if s.LastSucceededAt != nil {
		return cloneOpenAIReauthorizationTime(s.LastSucceededAt)
	}
	return cloneOpenAIReauthorizationTime(s.FirstSucceededAt)
}

func (s OpenAIReauthorizationState) CooldownUntil() *time.Time {
	anchor := s.CooldownAnchor()
	if anchor == nil {
		return nil
	}
	until := anchor.Add(OpenAIReauthorizationCooldown)
	return &until
}

// UpdateOpenAIReauthorizationState is deliberately outside AdminService. The
// handler reaches it through a narrow optional interface, while generic admin
// account updates are prevented from writing this XIASS-managed document.
func (s *adminServiceImpl) UpdateOpenAIReauthorizationState(ctx context.Context, id int64, state OpenAIReauthorizationState) error {
	if s == nil || s.accountRepo == nil {
		return errors.New("account repository is unavailable")
	}
	if err := s.ensureAccountManagementAccessByID(ctx, id); err != nil {
		return err
	}
	account, err := s.accountRepo.GetByID(ctx, id)
	if err != nil {
		return err
	}
	if account == nil || !account.IsOpenAIOAuth() || account.IsCredentialShadow() || account.IsOpenAIOAuthCredentialCopy() {
		return errors.New("only primary OpenAI OAuth accounts can store reauthorization history")
	}
	state.Normalize()
	return s.accountRepo.UpdateExtra(ctx, id, map[string]any{
		OpenAIReauthorizationStateExtraKey: state,
	})
}

func normalizeOpenAIReauthorizationResult(result string) string {
	switch strings.ToLower(strings.TrimSpace(result)) {
	case OpenAIReauthorizationResultRunning,
		OpenAIReauthorizationResultSuccess,
		OpenAIReauthorizationResultFailed,
		OpenAIReauthorizationResultBlocked,
		OpenAIReauthorizationResultCanceled,
		OpenAIReauthorizationResultLegacyUnknown:
		return strings.ToLower(strings.TrimSpace(result))
	default:
		return ""
	}
}

func normalizeOpenAIReauthorizationConfidence(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case OpenAIReauthorizationHistoryExact,
		OpenAIReauthorizationHistoryInferred,
		OpenAIReauthorizationHistoryUnknown:
		return strings.ToLower(strings.TrimSpace(value))
	default:
		return ""
	}
}

func normalizedOpenAIReauthorizationTime(value *time.Time) *time.Time {
	if value == nil || value.IsZero() {
		return nil
	}
	utc := value.UTC()
	return &utc
}

func cloneOpenAIReauthorizationTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}
