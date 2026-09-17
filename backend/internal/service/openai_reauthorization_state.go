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
	s.TrackingStartedAt = normalizedOpenAIReauthorizationTime(s.TrackingStartedAt)
	s.FirstAttemptAt = normalizedOpenAIReauthorizationTime(s.FirstAttemptAt)
	s.LastAttemptAt = normalizedOpenAIReauthorizationTime(s.LastAttemptAt)
	s.FirstSucceededAt = normalizedOpenAIReauthorizationTime(s.FirstSucceededAt)
	s.LastSucceededAt = normalizedOpenAIReauthorizationTime(s.LastSucceededAt)
	s.LastResultAt = normalizedOpenAIReauthorizationTime(s.LastResultAt)
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
