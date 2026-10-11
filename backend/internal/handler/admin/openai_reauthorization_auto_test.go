package admin

import (
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

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
		LastInitiator:       " AUTOMATIC ",
		AutoEpisodeKey:      " episode-a ",
		AutoClaimID:         " claim-a ",
		AutoBrowserMode:     " ADSPOWER ",
		AutoExecutionNodeID: " api2 ",
	}
	state.Normalize()
	require.Equal(t, service.OpenAIReauthorizationInitiatorAutomatic, state.LastInitiator)
	require.Equal(t, "episode-a", state.AutoEpisodeKey)
	require.Equal(t, "claim-a", state.AutoClaimID)
	require.Equal(t, "adspower", state.AutoBrowserMode)
	require.Equal(t, "api2", state.AutoExecutionNodeID)
}
