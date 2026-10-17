//go:build integration

package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func automaticReauthorizationPGAccount(t *testing.T, extra map[string]any) *service.Account {
	t.Helper()
	credentials := map[string]any{
		"access_token":   "synthetic-old-access",
		"refresh_token":  "synthetic-old-refresh",
		"_token_version": int64(4),
	}
	credentialsJSON, err := json.Marshal(credentials)
	require.NoError(t, err)
	extraJSON, err := json.Marshal(extra)
	require.NoError(t, err)

	account := &service.Account{
		Name:        fmt.Sprintf("openai-auto-reauth-pg-%d", time.Now().UnixNano()),
		Platform:    service.PlatformOpenAI,
		Type:        service.AccountTypeOAuth,
		Credentials: credentials,
		Extra:       extra,
	}
	require.NoError(t, integrationDB.QueryRow(`
		INSERT INTO accounts (name, platform, type, credentials, extra, temp_unschedulable_reason)
		VALUES ($1, $2, $3, $4::jsonb, $5::jsonb, 'oauth_401')
		RETURNING id, created_at, updated_at`,
		account.Name, account.Platform, account.Type, string(credentialsJSON), string(extraJSON),
	).Scan(&account.ID, &account.CreatedAt, &account.UpdatedAt))
	t.Cleanup(func() {
		_, _ = integrationDB.ExecContext(context.Background(), `DELETE FROM scheduler_outbox WHERE account_id = $1`, account.ID)
		_, _ = integrationDB.ExecContext(context.Background(), `DELETE FROM accounts WHERE id = $1`, account.ID)
	})
	return account
}

func automaticReauthorizationPGClaim(accountID int64, episodeKey string) (service.OpenAIReauthorizationAutoClaim, service.OpenAIReauthorizationState) {
	claimedAt := time.Date(2026, 10, 12, 1, 2, 3, 0, time.UTC)
	claim := service.OpenAIReauthorizationAutoClaim{
		EpisodeKey:         episodeKey,
		ClaimID:            fmt.Sprintf("claim-%d-%s", accountID, episodeKey),
		BrowserMode:        "server",
		ExecutionNodeID:    "api",
		ClaimedAt:          claimedAt,
		LeaseUntil:         claimedAt.Add(45 * time.Minute),
		RestoreSchedulable: true,
	}
	state := service.OpenAIReauthorizationState{
		LastResult:             service.OpenAIReauthorizationResultRunning,
		LastInitiator:          service.OpenAIReauthorizationInitiatorAutomatic,
		AutoEpisodeKey:         claim.EpisodeKey,
		AutoClaimID:            claim.ClaimID,
		AutoBrowserMode:        claim.BrowserMode,
		AutoExecutionNodeID:    claim.ExecutionNodeID,
		AutoClaimedAt:          &claimedAt,
		AutoLeaseUntil:         &claim.LeaseUntil,
		AutoRestoreSchedulable: claim.RestoreSchedulable,
	}
	return claim, state
}

func automaticReauthorizationPGOutboxCount(t *testing.T, accountID int64) int {
	t.Helper()
	var count int
	require.NoError(t, integrationDB.QueryRow(`SELECT count(*) FROM scheduler_outbox WHERE account_id = $1`, accountID).Scan(&count))
	return count
}

func TestClaimOpenAIReauthorizationAutoPGClaimsMissingAndLegacyStateOnce(t *testing.T) {
	stateKey := service.OpenAIReauthorizationStateExtraKey
	tests := []struct {
		name           string
		persistedExtra map[string]any
		nilSnapshot    bool
	}{
		{
			name:           "missing state and nil caller extra",
			persistedExtra: map[string]any{},
			nilSnapshot:    true,
		},
		{
			name:           "null state",
			persistedExtra: map[string]any{stateKey: nil},
		},
		{
			name:           "empty state",
			persistedExtra: map[string]any{stateKey: map[string]any{}},
		},
		{
			name: "legacy state without automatic identity",
			persistedExtra: map[string]any{stateKey: map[string]any{
				"version": 1, "attempt_count": 2, "last_result": "success", "history_source": "legacy_migration",
			}},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			account := automaticReauthorizationPGAccount(t, test.persistedExtra)
			require.Empty(t, account.QuotaDimension, "caller snapshot intentionally exercises the zero-value global dimension")
			if test.nilSnapshot {
				account.Extra = nil
			}
			repo := newAccountRepositoryWithSQL(integrationEntClient, integrationDB, nil)
			claim, next := automaticReauthorizationPGClaim(account.ID, "episode-first-401")

			claimed, err := repo.ClaimOpenAIReauthorizationAutoAttempt(context.Background(), account, claim, next)
			require.NoError(t, err)
			require.True(t, claimed)
			require.Equal(t, 1, automaticReauthorizationPGOutboxCount(t, account.ID))

			var initiator, episodeKey, result, claimID string
			require.NoError(t, integrationDB.QueryRow(`SELECT
				extra -> $2 ->> 'last_initiator',
				extra -> $2 ->> 'auto_episode_key',
				extra -> $2 ->> 'last_result',
				extra -> $2 ->> 'auto_claim_id'
				FROM accounts WHERE id = $1`, account.ID, stateKey).
				Scan(&initiator, &episodeKey, &result, &claimID))
			require.Equal(t, service.OpenAIReauthorizationInitiatorAutomatic, initiator)
			require.Equal(t, claim.EpisodeKey, episodeKey)
			require.Equal(t, service.OpenAIReauthorizationResultRunning, result)
			require.Equal(t, claim.ClaimID, claimID)

			claimed, err = repo.ClaimOpenAIReauthorizationAutoAttempt(context.Background(), account, claim, next)
			require.NoError(t, err)
			require.False(t, claimed, "the same automatic 401 episode must be claimed only once")
			require.Equal(t, 1, automaticReauthorizationPGOutboxCount(t, account.ID))
		})
	}
}

func TestClaimOpenAIReauthorizationAutoPGDeduplicatesHandledResults(t *testing.T) {
	for _, result := range []string{
		service.OpenAIReauthorizationResultRunning,
		service.OpenAIReauthorizationResultSuccess,
		service.OpenAIReauthorizationResultFailed,
		service.OpenAIReauthorizationResultBlocked,
		service.OpenAIReauthorizationResultCanceled,
	} {
		t.Run(result, func(t *testing.T) {
			episodeKey := "episode-already-handled"
			account := automaticReauthorizationPGAccount(t, map[string]any{
				service.OpenAIReauthorizationStateExtraKey: map[string]any{
					"last_initiator":   service.OpenAIReauthorizationInitiatorAutomatic,
					"auto_episode_key": episodeKey,
					"last_result":      result,
				},
			})
			repo := newAccountRepositoryWithSQL(integrationEntClient, integrationDB, nil)
			claim, next := automaticReauthorizationPGClaim(account.ID, episodeKey)

			claimed, err := repo.ClaimOpenAIReauthorizationAutoAttempt(context.Background(), account, claim, next)
			require.NoError(t, err)
			require.False(t, claimed)
			require.Zero(t, automaticReauthorizationPGOutboxCount(t, account.ID))
		})
	}
}

func TestClaimOpenAIReauthorizationAutoPGConcurrentClaimWinsOnce(t *testing.T) {
	account := automaticReauthorizationPGAccount(t, map[string]any{})
	repo := newAccountRepositoryWithSQL(integrationEntClient, integrationDB, nil)
	episodeKey := "episode-concurrent-first-401"
	claims := make([]service.OpenAIReauthorizationAutoClaim, 2)
	states := make([]service.OpenAIReauthorizationState, 2)
	for i := range claims {
		claims[i], states[i] = automaticReauthorizationPGClaim(account.ID, episodeKey)
		claims[i].ClaimID = fmt.Sprintf("claim-%d-concurrent-%d", account.ID, i)
		states[i].AutoClaimID = claims[i].ClaimID
	}

	type claimResult struct {
		claimed bool
		err     error
	}
	results := make(chan claimResult, len(claims))
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := range claims {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			<-start
			claimed, err := repo.ClaimOpenAIReauthorizationAutoAttempt(
				context.Background(), account, claims[index], states[index],
			)
			results <- claimResult{claimed: claimed, err: err}
		}(i)
	}
	close(start)
	wg.Wait()
	close(results)

	claimedCount := 0
	for result := range results {
		require.NoError(t, result.err)
		if result.claimed {
			claimedCount++
		}
	}
	require.Equal(t, 1, claimedCount, "concurrent workers must create one durable claim")
	require.Equal(t, 1, automaticReauthorizationPGOutboxCount(t, account.ID))

	var persistedClaimID string
	require.NoError(t, integrationDB.QueryRow(`SELECT extra -> $2 ->> 'auto_claim_id' FROM accounts WHERE id = $1`,
		account.ID, service.OpenAIReauthorizationStateExtraKey).Scan(&persistedClaimID))
	require.Contains(t, []string{claims[0].ClaimID, claims[1].ClaimID}, persistedClaimID)
}

func TestClaimOpenAIReauthorizationAutoPGRejectsReplacedCredentials(t *testing.T) {
	account := automaticReauthorizationPGAccount(t, map[string]any{})
	repo := newAccountRepositoryWithSQL(integrationEntClient, integrationDB, nil)
	claim, next := automaticReauthorizationPGClaim(account.ID, "episode-after-credential-replacement")
	replacement := `{"access_token":"synthetic-replacement-access","refresh_token":"synthetic-replacement-refresh","_token_version":5}`
	_, err := integrationDB.Exec(`UPDATE accounts SET credentials = $1::jsonb WHERE id = $2`, replacement, account.ID)
	require.NoError(t, err)
	beforeOutbox := automaticReauthorizationPGOutboxCount(t, account.ID)

	claimed, err := repo.ClaimOpenAIReauthorizationAutoAttempt(context.Background(), account, claim, next)
	require.NoError(t, err)
	require.False(t, claimed, "a stale 401 worker must not claim after credentials are replaced")
	require.Equal(t, beforeOutbox, automaticReauthorizationPGOutboxCount(t, account.ID))

	var hasState bool
	require.NoError(t, integrationDB.QueryRow(`SELECT extra ? $2 FROM accounts WHERE id = $1`, account.ID,
		service.OpenAIReauthorizationStateExtraKey).Scan(&hasState))
	require.False(t, hasState)
}
