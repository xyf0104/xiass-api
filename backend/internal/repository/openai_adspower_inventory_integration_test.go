//go:build integration

package repository

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestOpenAIAdsPowerProfileReleaseCASRejectsCrossEnvironmentReference(t *testing.T) {
	ctx := context.Background()
	repo, ok := NewAccountRepository(integrationEntClient, integrationDB, nil).(service.OpenAIAdsPowerInventoryRepository)
	require.True(t, ok)
	now := time.Now().UTC().Truncate(time.Second)

	insertAccount := func(name, environmentKey, errorCode string) int64 {
		binding := map[string]any{
			"version": 1, "device_id": "device-cas", "profile_id": "profile-cas",
			"environment_key": environmentKey, "webrtc_disabled": true, "fingerprint_randomized": true,
			"bound_at": now.Add(-2 * time.Minute), "last_launched_at": now.Add(-2 * time.Minute),
			"last_verified_at": now.Add(-2 * time.Minute),
		}
		extraValues := map[string]any{
			service.AccountExecutionNodeExtraKey:  environmentKey,
			service.OpenAIAdsPowerBindingExtraKey: binding,
		}
		if errorCode != "" {
			extraValues["error_code"] = errorCode
		} else {
			extraValues[service.OpenAIReauthorizationStateExtraKey] = map[string]any{
				"last_result": "blocked", "last_reason": "account_deleted_or_disabled",
				"history_source": "xiass_state", "history_confidence": "exact",
				"last_attempt_at": now.Add(-time.Minute), "last_result_at": now,
			}
		}
		extra, err := json.Marshal(extraValues)
		require.NoError(t, err)
		var id int64
		err = integrationDB.QueryRowContext(ctx, `
			INSERT INTO accounts (name, platform, type, credentials, extra, status)
			VALUES ($1, $2, $3, '{}'::jsonb, $4::jsonb, $5)
			RETURNING id`, name, service.PlatformOpenAI, service.AccountTypeOAuth, string(extra), service.StatusError).Scan(&id)
		require.NoError(t, err)
		t.Cleanup(func() {
			_, _ = integrationDB.ExecContext(context.Background(), "DELETE FROM accounts WHERE id = $1", id)
		})
		return id
	}

	localID := insertAccount("adspower-cas-local", "api2", "")
	remoteID := insertAccount("adspower-cas-remote", "api", "account_banned")
	blockedCodes := []string{"account_banned", "account_deleted_or_disabled"}

	_, released, err := repo.ReleaseOpenAIAdsPowerProfileBindings(
		ctx, "device-cas", "api2", "profile-cas", "api", blockedCodes,
	)
	require.NoError(t, err)
	require.False(t, released)
	require.True(t, integrationAccountHasExtraKey(t, localID, service.OpenAIAdsPowerBindingExtraKey))
	require.True(t, integrationAccountHasExtraKey(t, remoteID, service.OpenAIAdsPowerBindingExtraKey))

	_, err = integrationDB.ExecContext(ctx, "UPDATE accounts SET deleted_at = NOW() WHERE id = $1", remoteID)
	require.NoError(t, err)
	ids, released, err := repo.ReleaseOpenAIAdsPowerProfileBindings(
		ctx, "device-cas", "api2", "profile-cas", "api", blockedCodes,
	)
	require.NoError(t, err)
	require.True(t, released)
	require.Equal(t, []int64{localID}, ids)
	require.False(t, integrationAccountHasExtraKey(t, localID, service.OpenAIAdsPowerBindingExtraKey))
}

func integrationAccountHasExtraKey(t *testing.T, accountID int64, key string) bool {
	t.Helper()
	var exists bool
	err := integrationDB.QueryRowContext(context.Background(), "SELECT extra ? $1 FROM accounts WHERE id = $2", key, accountID).Scan(&exists)
	require.NoError(t, err)
	return exists
}
