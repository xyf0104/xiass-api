package admin

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

type openAIReauthorizationAutoAdminStub struct {
	*stubAdminService
	afterClaim func()
	expected   *service.Account
	finished   service.OpenAIReauthorizationState
}

func (s *openAIReauthorizationAutoAdminStub) ClaimOpenAIReauthorizationAutoAttempt(_ context.Context, expected *service.Account, claim service.OpenAIReauthorizationAutoClaim) (service.OpenAIReauthorizationState, bool, error) {
	state := service.OpenAIReauthorizationState{LastInitiator: service.OpenAIReauthorizationInitiatorAutomatic, LastResult: service.OpenAIReauthorizationResultRunning,
		AutoEpisodeKey: claim.EpisodeKey, AutoClaimID: claim.ClaimID, AutoBrowserMode: claim.BrowserMode, AutoLeaseUntil: &claim.LeaseUntil}
	s.expected = expected
	if s.afterClaim != nil {
		s.afterClaim()
	}
	return state, true, nil
}

func (s *openAIReauthorizationAutoAdminStub) FinishOpenAIReauthorizationAutoAttempt(_ context.Context, expected *service.Account, _ service.OpenAIReauthorizationAutoClaim, next service.OpenAIReauthorizationState) (bool, error) {
	s.expected = expected
	s.finished = next
	return true, nil
}

func (s *openAIReauthorizationAutoAdminStub) ApplyOpenAIReauthorizationAutoCredentials(context.Context, *service.Account, service.OpenAIReauthorizationAutoClaim, map[string]any, service.OpenAIReauthorizationState) (bool, error) {
	return false, nil
}

func TestOpenAIReauthorizationAutoEpisodeChangesWithCredentialGeneration(t *testing.T) {
	account := &service.Account{
		ID:       42,
		Platform: service.PlatformOpenAI,
		Type:     service.AccountTypeOAuth,
		Credentials: map[string]any{
			"access_token":   "access-a",
			"refresh_token":  "refresh-a",
			"_token_version": int64(3),
		},
	}
	first := openAIReauthorizationAutoEpisodeKey(account)
	require.Len(t, first, 64)

	account.Credentials["_token_version"] = int64(4)
	require.NotEqual(t, first, openAIReauthorizationAutoEpisodeKey(account))
	account.Credentials["_token_version"] = int64(3)
	account.Credentials["access_token"] = "access-b"
	require.NotEqual(t, first, openAIReauthorizationAutoEpisodeKey(account))
}

func TestOpenAIReauthorizationAutoBrowserModePreservesSavedModeAndLegacyAdsBinding(t *testing.T) {
	server := &service.Account{Extra: map[string]any{service.OpenAIReauthorizationBrowserModeExtraKey: "server"}}
	require.Equal(t, batchOAuthBrowserServer, openAIReauthorizationBrowserMode(server))

	ads := &service.Account{Extra: map[string]any{
		service.OpenAIAdsPowerBindingExtraKey: service.OpenAIAdsPowerBinding{
			DeviceID: "device", ProfileID: "profile", EnvironmentKey: "api",
			WebRTCDisabled: true, FingerprintRandomized: true,
		},
	}}
	require.Equal(t, batchOAuthBrowserAdsPower, openAIReauthorizationBrowserMode(ads))

	legacy := &service.Account{Extra: map[string]any{service.OpenAIReauthorizationBrowserModeExtraKey: "invalid"}}
	require.Equal(t, batchOAuthBrowserServer, openAIReauthorizationBrowserMode(legacy))
}

func TestOpenAIReauthorizationAutoEpisodeIsAttemptedOnlyOnce(t *testing.T) {
	now := time.Now().UTC()
	state := service.OpenAIReauthorizationState{
		LastInitiator:  service.OpenAIReauthorizationInitiatorAutomatic,
		AutoEpisodeKey: "episode-a",
		LastResult:     service.OpenAIReauthorizationResultFailed,
	}
	require.True(t, openAIReauthorizationAutoClaimAlreadyRecorded(state, "episode-a", now))
	require.False(t, openAIReauthorizationAutoClaimAlreadyRecorded(state, "episode-b", now))

	state.LastResult = service.OpenAIReauthorizationResultRunning
	lease := now.Add(time.Minute)
	state.AutoLeaseUntil = &lease
	require.True(t, openAIReauthorizationAutoClaimAlreadyRecorded(state, "episode-a", now))
	lease = now.Add(-time.Minute)
	state.AutoLeaseUntil = &lease
	require.True(t, openAIReauthorizationAutoClaimAlreadyRecorded(state, "episode-a", now), "an expired running claim must be finalized, never replayed")
}

func TestOpenAIReauthorizationStateNormalizesAutomaticClaimMetadata(t *testing.T) {
	state := service.OpenAIReauthorizationState{
		LastInitiator:   " AUTOMATIC ",
		AutoEpisodeKey:  " episode-a ",
		AutoClaimID:     " claim-a ",
		AutoBrowserMode: " ADSPOWER ",
	}
	state.Normalize()
	require.Equal(t, service.OpenAIReauthorizationInitiatorAutomatic, state.LastInitiator)
	require.Equal(t, "episode-a", state.AutoEpisodeKey)
	require.Equal(t, "claim-a", state.AutoClaimID)
	require.Equal(t, "adspower", state.AutoBrowserMode)
}

func TestOpenAIReauthorizationAutoStartFailuresPreserveClaimIdentityAndHistory(t *testing.T) {
	for _, failure := range []string{"saved_login_unavailable", "adspower_binding_unavailable", "account_configuration_changed"} {
		t.Run(failure, func(t *testing.T) {
			f := newBatchOAuthFixture(t)
			account := reauthorizationAccount(448)
			account.Extra[service.OpenAIReauthorizationBrowserModeExtraKey] = batchOAuthBrowserAdsPower
			account.Extra[service.OpenAIAdsPowerBindingExtraKey] = service.OpenAIAdsPowerBinding{DeviceID: "device", ProfileID: "profile", EnvironmentKey: "saved-environment", WebRTCDisabled: true, FingerprintRandomized: true}
			f.admin.getAccountResult = account
			admin := &openAIReauthorizationAutoAdminStub{stubAdminService: f.admin}
			f.h.adminService = admin
			switch failure {
			case "saved_login_unavailable":
				account.Credentials[service.OpenAIOAuthReauthorizationPasswordCredentialKey] = "invalid-ciphertext"
			case "adspower_binding_unavailable":
				admin.afterClaim = func() { delete(account.Extra, service.OpenAIAdsPowerBindingExtraKey) }
			case "account_configuration_changed":
				admin.afterClaim = func() {
					replacement := *account
					replacement.ProxyID = nil
					f.admin.getAccountResult = &replacement
				}
			}
			require.NoError(t, f.h.runOpenAIReauthorizationAutomatic(context.Background(), account.ID, nil))
			require.Same(t, account, admin.expected)
			require.Equal(t, service.OpenAIReauthorizationResultFailed, admin.finished.LastResult)
			require.Equal(t, failure, admin.finished.LastReason)
			require.NotEmpty(t, admin.finished.AutoClaimID)
			require.NotNil(t, admin.finished.AutoFinishedAt)
			require.Nil(t, admin.finished.AutoLeaseUntil)
			require.Empty(t, f.requests)
		})
	}
}

func TestOpenAIReauthorizationAutoQueueOverflowCanRetry(t *testing.T) {
	runtime := &openAIReauthorizationAutoRuntime{ctx: context.Background(), queue: make(chan int64, 1), pending: make(map[int64]bool)}
	runtime.enqueue(1)
	done := make(chan struct{})
	go func() {
		runtime.enqueue(2)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("a full automatic reauthorization queue blocked its caller")
	}
	require.False(t, runtime.pending[2])
	require.Equal(t, int64(1), <-runtime.queue)
	runtime.enqueue(2)
	require.True(t, runtime.pending[2])
	require.Equal(t, int64(2), <-runtime.queue)
}
