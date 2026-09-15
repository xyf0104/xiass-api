package service

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestOpenAIReauthorizationStateCooldownUsesMostRecentSuccess(t *testing.T) {
	first := time.Date(2026, 9, 1, 8, 0, 0, 0, time.UTC)
	last := time.Date(2026, 9, 8, 9, 30, 0, 0, time.UTC)
	state := OpenAIReauthorizationState{
		AttemptCount: 2, SuccessCount: 2,
		FirstSucceededAt: &first, LastSucceededAt: &last,
	}

	require.Equal(t, 3, state.NextAuthorizationNumber())
	require.Equal(t, last.Add(7*24*time.Hour), *state.CooldownUntil())
}

func TestParseOpenAIReauthorizationStateNormalizesInvalidCounters(t *testing.T) {
	state := ParseOpenAIReauthorizationState(map[string]any{
		"attempt_count":      -4,
		"success_count":      2,
		"last_result":        " SUCCESS ",
		"history_confidence": " INFERRED ",
	})

	require.Equal(t, 2, state.AttemptCount)
	require.Equal(t, 2, state.SuccessCount)
	require.Equal(t, OpenAIReauthorizationResultSuccess, state.LastResult)
	require.Equal(t, OpenAIReauthorizationHistoryInferred, state.HistoryConfidence)
}

func TestBuildOpenAIAccountForCreateEstablishesManagedReauthorizationBaseline(t *testing.T) {
	forged := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	account, err := buildAccountForCreate(&CreateAccountInput{
		Name: "new-openai", Platform: PlatformOpenAI, Type: AccountTypeOAuth,
	}, map[string]any{
		OpenAIReauthorizationStateExtraKey: OpenAIReauthorizationState{
			Version: 1, AttemptCount: 4, SuccessCount: 4,
			TrackingStartedAt: &forged, LastResult: OpenAIReauthorizationResultSuccess,
		},
	})
	require.NoError(t, err)

	state := OpenAIReauthorizationStateFromAccount(account)
	require.True(t, state.IsTracked())
	require.False(t, state.HasHistory())
	require.Zero(t, state.AttemptCount)
	require.Zero(t, state.SuccessCount)
	require.Empty(t, state.LastResult)
	require.NotNil(t, state.TrackingStartedAt)
	require.True(t, state.TrackingStartedAt.After(forged))
}
