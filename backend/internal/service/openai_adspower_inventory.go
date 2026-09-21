package service

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"
)

const OpenAIAdsPowerInventoryMaxBindings = 10000

var ErrOpenAIAdsPowerInventoryIncomplete = errors.New("AdsPower profile inventory is incomplete")

type OpenAIAdsPowerInventoryProfile struct {
	ProfileID       string `json:"profile_id"`
	AccountID       int64  `json:"account_id"`
	EnvironmentKey  string `json:"environment_key"`
	FingerprintSlot int    `json:"fingerprint_slot"`
	Blocked         bool   `json:"blocked"`
	Shared          bool   `json:"shared_profile"`
}

type OpenAIAdsPowerProfileInventory struct {
	Complete           bool                             `json:"complete"`
	Profiles           []OpenAIAdsPowerInventoryProfile `json:"profiles"`
	PendingProfileIDs  []string                         `json:"pending_profile_ids"`
	ReleasedProfileIDs []string                         `json:"released_profile_ids"`
}

type OpenAIAdsPowerBindingAccountRecord struct {
	Account         Account
	Binding         OpenAIAdsPowerBinding
	ExecutionNodeID string
	Deleted         bool
}

// OpenAIAdsPowerInventoryRepository is deliberately optional. Inventory and
// release callers must fail closed when the production repository does not
// provide an exact, bounded database view and an atomic compare-and-clear.
type OpenAIAdsPowerInventoryRepository interface {
	ListOpenAIAdsPowerBindingAccounts(ctx context.Context, deviceID, environmentKey, legacyNodeID string, limit int) ([]OpenAIAdsPowerBindingAccountRecord, bool, error)
	ReleaseOpenAIAdsPowerProfileBindings(ctx context.Context, deviceID, environmentKey, profileID, legacyNodeID string, blockedCodes []string) ([]int64, bool, error)
}

func (s *adminServiceImpl) ListOpenAIAdsPowerProfileInventory(ctx context.Context, deviceID, environmentKey string) (*OpenAIAdsPowerProfileInventory, error) {
	deviceID = strings.TrimSpace(deviceID)
	environmentKey = strings.TrimSpace(environmentKey)
	if s == nil || s.accountRepo == nil || deviceID == "" || environmentKey == "" {
		return nil, ErrOpenAIAdsPowerInventoryIncomplete
	}
	repo, ok := s.accountRepo.(OpenAIAdsPowerInventoryRepository)
	if !ok {
		return nil, ErrOpenAIAdsPowerInventoryIncomplete
	}
	records, complete, err := repo.ListOpenAIAdsPowerBindingAccounts(
		ctx, deviceID, environmentKey, s.legacyExecutionNodeID(), OpenAIAdsPowerInventoryMaxBindings,
	)
	if err != nil {
		return nil, err
	}
	if !complete {
		return nil, ErrOpenAIAdsPowerInventoryIncomplete
	}

	result := &OpenAIAdsPowerProfileInventory{
		Complete: true, Profiles: make([]OpenAIAdsPowerInventoryProfile, 0, len(records)),
		PendingProfileIDs: make([]string, 0), ReleasedProfileIDs: make([]string, 0),
	}
	released := make(map[string]struct{})
	liveProfiles := make(map[string]struct{})
	targetProfiles := make(map[string]struct{})
	for i := range records {
		record := &records[i]
		binding := record.Binding
		binding.Normalize()
		if binding.DeviceID != deviceID || binding.ProfileID == "" {
			return nil, ErrOpenAIAdsPowerInventoryIncomplete
		}
		if binding.EnvironmentKey == environmentKey || record.ExecutionNodeID == environmentKey {
			if binding.EnvironmentKey != environmentKey || record.ExecutionNodeID != environmentKey ||
				record.Account.Platform != PlatformOpenAI || record.Account.Type != AccountTypeOAuth || record.Account.ParentAccountID != nil {
				return nil, ErrOpenAIAdsPowerInventoryIncomplete
			}
			targetProfiles[binding.ProfileID] = struct{}{}
		}
	}
	for i := range records {
		record := &records[i]
		binding := record.Binding
		binding.Normalize()
		if _, target := targetProfiles[binding.ProfileID]; !target {
			continue
		}
		if binding.EnvironmentKey != environmentKey || record.ExecutionNodeID != environmentKey ||
			record.Account.Platform != PlatformOpenAI || record.Account.Type != AccountTypeOAuth || record.Account.ParentAccountID != nil {
			return nil, ErrOpenAIAdsPowerInventoryIncomplete
		}
		if record.Deleted {
			released[binding.ProfileID] = struct{}{}
			continue
		}
		liveProfiles[binding.ProfileID] = struct{}{}
		result.Profiles = append(result.Profiles, OpenAIAdsPowerInventoryProfile{
			ProfileID: binding.ProfileID, AccountID: record.Account.ID,
			EnvironmentKey: binding.EnvironmentKey, FingerprintSlot: binding.FingerprintSlot,
			Blocked: OpenAIAdsPowerBindingExplicitlyBlocked(&record.Account, &binding), Shared: binding.SharedProfile,
		})
	}
	for profileID := range released {
		if _, stillBound := liveProfiles[profileID]; !stillBound {
			result.ReleasedProfileIDs = append(result.ReleasedProfileIDs, profileID)
		}
	}
	sort.Slice(result.Profiles, func(i, j int) bool {
		if result.Profiles[i].ProfileID != result.Profiles[j].ProfileID {
			return result.Profiles[i].ProfileID < result.Profiles[j].ProfileID
		}
		return result.Profiles[i].AccountID < result.Profiles[j].AccountID
	})
	sort.Strings(result.ReleasedProfileIDs)
	return result, nil
}

func (s *adminServiceImpl) ReleaseOpenAIAdsPowerProfile(ctx context.Context, deviceID, environmentKey, profileID string) (bool, error) {
	deviceID = strings.TrimSpace(deviceID)
	environmentKey = strings.TrimSpace(environmentKey)
	profileID = strings.TrimSpace(profileID)
	if s == nil || s.accountRepo == nil || deviceID == "" || environmentKey == "" || profileID == "" {
		return false, ErrOpenAIAdsPowerInventoryIncomplete
	}
	repo, ok := s.accountRepo.(OpenAIAdsPowerInventoryRepository)
	if !ok {
		return false, ErrOpenAIAdsPowerInventoryIncomplete
	}
	_, released, err := repo.ReleaseOpenAIAdsPowerProfileBindings(
		ctx, deviceID, environmentKey, profileID, s.legacyExecutionNodeID(), openAIAdsPowerExplicitBlockedCodes(),
	)
	return released, err
}

func OpenAIAdsPowerBindingExplicitlyBlocked(account *Account, binding *OpenAIAdsPowerBinding) bool {
	allowed := make(map[string]struct{}, len(openAIAdsPowerExplicitBlockedCodes()))
	for _, code := range openAIAdsPowerExplicitBlockedCodes() {
		allowed[code] = struct{}{}
	}
	return OpenAIAdsPowerBindingExplicitlyBlockedWithCodes(account, binding, allowed)
}

// OpenAIAdsPowerBindingExplicitlyBlockedWithCodes is shared by the repository
// transaction so release uses the same current-evidence predicate as inventory.
func OpenAIAdsPowerBindingExplicitlyBlockedWithCodes(account *Account, binding *OpenAIAdsPowerBinding, allowed map[string]struct{}) bool {
	if account == nil || binding == nil || account.Status != StatusError {
		return false
	}
	if code := strings.ToLower(strings.TrimSpace(account.GetExtraString("error_code"))); code != "" {
		_, ok := allowed[code]
		return ok
	}

	state := OpenAIReauthorizationStateFromAccount(account)
	if state.LastResult != OpenAIReauthorizationResultBlocked ||
		state.HistorySource != "xiass_state" || state.HistoryConfidence != OpenAIReauthorizationHistoryExact ||
		state.LastResultAt == nil || state.LastResultAt.IsZero() {
		return false
	}
	if _, ok := allowed[strings.ToLower(strings.TrimSpace(state.LastReason))]; !ok {
		return false
	}
	blockedAt := state.LastResultAt.UTC()
	for _, observedAt := range []*time.Time{
		state.LastAttemptAt, binding.BoundAt, binding.LastLaunchedAt, binding.LastVerifiedAt,
	} {
		if observedAt == nil || observedAt.IsZero() || blockedAt.Before(observedAt.UTC()) {
			return false
		}
	}
	if state.LastSucceededAt != nil && !blockedAt.After(state.LastSucceededAt.UTC()) {
		return false
	}
	return true
}

func openAIAdsPowerExplicitBlockedCodes() []string {
	return []string{
		"account_banned",
		"account_deactivated",
		"account_deleted",
		"account_deleted_or_disabled",
		"account_disabled",
		"account_suspended",
	}
}
