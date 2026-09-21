package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type openAIAdsPowerInventoryRepoStub struct {
	AccountRepository
	records  []OpenAIAdsPowerBindingAccountRecord
	complete bool
	err      error
	released bool
}

func (s *openAIAdsPowerInventoryRepoStub) ListOpenAIAdsPowerBindingAccounts(context.Context, string, string, string, int) ([]OpenAIAdsPowerBindingAccountRecord, bool, error) {
	return s.records, s.complete, s.err
}

func (s *openAIAdsPowerInventoryRepoStub) ReleaseOpenAIAdsPowerProfileBindings(context.Context, string, string, string, string, []string) ([]int64, bool, error) {
	return nil, s.released, s.err
}

func TestOpenAIAdsPowerBindingExplicitlyBlockedRequiresCurrentEvidence(t *testing.T) {
	now := time.Now().UTC()
	bindingObservedAt := now.Add(-2 * time.Minute)
	binding := &OpenAIAdsPowerBinding{
		Version: 1, DeviceID: "device-1", ProfileID: "profile-1", EnvironmentKey: "api2",
		WebRTCDisabled: true, FingerprintRandomized: true, BoundAt: &bindingObservedAt,
		LastLaunchedAt: &bindingObservedAt, LastVerifiedAt: &bindingObservedAt,
	}
	for _, account := range []*Account{
		{Status: StatusError, ErrorMessage: "401 unauthorized", Extra: map[string]any{"error_code": "unauthenticated"}},
		{Status: StatusError, ErrorMessage: "account banned", Extra: map[string]any{}},
		{Status: StatusActive, Extra: map[string]any{"error_code": "account_banned"}},
	} {
		require.False(t, OpenAIAdsPowerBindingExplicitlyBlocked(account, binding))
	}
	require.True(t, OpenAIAdsPowerBindingExplicitlyBlocked(&Account{
		Status: StatusError, Extra: map[string]any{"error_code": "account_banned"},
	}, binding))

	state := map[string]any{
		"last_result": "blocked", "last_reason": "account_deleted_or_disabled",
		"history_source": "xiass_state", "history_confidence": "exact",
		"last_attempt_at": now.Add(-time.Minute), "last_result_at": now,
	}
	require.True(t, OpenAIAdsPowerBindingExplicitlyBlocked(&Account{
		Status: StatusError, Extra: map[string]any{OpenAIReauthorizationStateExtraKey: state},
	}, binding))
	missingTimestamp := *binding
	missingTimestamp.LastVerifiedAt = nil
	require.False(t, OpenAIAdsPowerBindingExplicitlyBlocked(&Account{
		Status: StatusError, Extra: map[string]any{OpenAIReauthorizationStateExtraKey: state},
	}, &missingTimestamp), "historical blocked evidence must not release a binding with incomplete lifecycle timestamps")
	require.False(t, OpenAIAdsPowerBindingExplicitlyBlocked(&Account{
		Status: StatusError, Extra: map[string]any{
			"error_code":                       "unauthenticated",
			OpenAIReauthorizationStateExtraKey: state,
		},
	}, binding), "a current non-ban error code must override historical blocked evidence")

	newBinding := *binding
	boundAfterBlock := now.Add(time.Second)
	newBinding.BoundAt = &boundAfterBlock
	require.False(t, OpenAIAdsPowerBindingExplicitlyBlocked(&Account{
		Status: StatusError, Extra: map[string]any{OpenAIReauthorizationStateExtraKey: state},
	}, &newBinding), "blocked evidence from before the current binding must be stale")

	recoveredState := make(map[string]any, len(state)+1)
	for key, value := range state {
		recoveredState[key] = value
	}
	recoveredState["last_succeeded_at"] = now
	require.False(t, OpenAIAdsPowerBindingExplicitlyBlocked(&Account{
		Status: StatusError, Extra: map[string]any{OpenAIReauthorizationStateExtraKey: recoveredState},
	}, binding), "a success at or after the blocked event invalidates release evidence")

	lateAttemptState := make(map[string]any, len(state))
	for key, value := range state {
		lateAttemptState[key] = value
	}
	lateAttemptState["last_attempt_at"] = now.Add(time.Second)
	require.False(t, OpenAIAdsPowerBindingExplicitlyBlocked(&Account{
		Status: StatusError, Extra: map[string]any{OpenAIReauthorizationStateExtraKey: lateAttemptState},
	}, binding), "blocked result must not predate the latest attempt")
}

func TestListOpenAIAdsPowerProfileInventorySeparatesDeletedRows(t *testing.T) {
	binding := OpenAIAdsPowerBinding{Version: 1, DeviceID: "device-1", ProfileID: "profile-1", EnvironmentKey: "api2", WebRTCDisabled: true, FingerprintRandomized: true, FingerprintSlot: 7, SharedProfile: true}
	repo := &openAIAdsPowerInventoryRepoStub{complete: true, records: []OpenAIAdsPowerBindingAccountRecord{
		{Account: Account{ID: 2, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusError, Extra: map[string]any{"error_code": "account_banned"}}, Binding: binding, ExecutionNodeID: "api2"},
		{Account: Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeOAuth}, Binding: OpenAIAdsPowerBinding{Version: 1, DeviceID: "device-1", ProfileID: "released-1", EnvironmentKey: "api2", WebRTCDisabled: true, FingerprintRandomized: true}, ExecutionNodeID: "api2", Deleted: true},
	}}
	svc := &adminServiceImpl{accountRepo: repo}
	got, err := svc.ListOpenAIAdsPowerProfileInventory(context.Background(), "device-1", "api2")
	require.NoError(t, err)
	require.True(t, got.Complete)
	require.Equal(t, []string{"released-1"}, got.ReleasedProfileIDs)
	require.Equal(t, []OpenAIAdsPowerInventoryProfile{{ProfileID: "profile-1", AccountID: 2, EnvironmentKey: "api2", FingerprintSlot: 7, Blocked: true, Shared: true}}, got.Profiles)
}

func TestListOpenAIAdsPowerProfileInventoryFailsClosed(t *testing.T) {
	svc := &adminServiceImpl{accountRepo: &openAIAdsPowerInventoryRepoStub{complete: false}}
	got, err := svc.ListOpenAIAdsPowerProfileInventory(context.Background(), "device-1", "api2")
	require.ErrorIs(t, err, ErrOpenAIAdsPowerInventoryIncomplete)
	require.Nil(t, got)
}

func TestListOpenAIAdsPowerProfileInventoryRejectsSharedCrossEnvironmentProfile(t *testing.T) {
	binding := OpenAIAdsPowerBinding{Version: 1, DeviceID: "device-1", ProfileID: "shared-profile", EnvironmentKey: "api2", WebRTCDisabled: true, FingerprintRandomized: true}
	remote := binding
	remote.EnvironmentKey = "api"
	repo := &openAIAdsPowerInventoryRepoStub{complete: true, records: []OpenAIAdsPowerBindingAccountRecord{
		{Account: Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeOAuth}, Binding: binding, ExecutionNodeID: "api2"},
		{Account: Account{ID: 2, Platform: PlatformOpenAI, Type: AccountTypeOAuth}, Binding: remote, ExecutionNodeID: "api"},
	}}
	svc := &adminServiceImpl{accountRepo: repo}
	got, err := svc.ListOpenAIAdsPowerProfileInventory(context.Background(), "device-1", "api2")
	require.ErrorIs(t, err, ErrOpenAIAdsPowerInventoryIncomplete)
	require.Nil(t, got)
}
