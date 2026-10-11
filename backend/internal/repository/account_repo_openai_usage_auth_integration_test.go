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

func openAIUsageAuthPGWithoutTempFields(record map[string]any) map[string]any {
	copy := make(map[string]any, len(record)-2)
	for key, value := range record {
		if key == "temp_unschedulable_until" || key == "temp_unschedulable_reason" {
			continue
		}
		copy[key] = value
	}
	return copy
}

func TestOpenAIUsageAuthPGMarksOnlyTempFieldsAndPreservesManualState(t *testing.T) {
	a := boundCodexPGAccount(t)
	_, err := integrationDB.Exec(`
		UPDATE accounts
		SET status = $2, schedulable = FALSE, error_message = 'manual disable',
			rate_limit_reset_at = NOW() + INTERVAL '30 minutes',
			overload_until = NOW() + INTERVAL '20 minutes'
		WHERE id = $1
	`, a.ID, service.StatusDisabled)
	require.NoError(t, err)
	before := boundCodexPGRecord(t, a.ID)
	started := time.Now().UTC()

	applied, err := boundCodexPGRepo().UpdateOpenAIUsageAuthIfIdentityMatches(context.Background(), a, true)

	require.NoError(t, err)
	require.True(t, applied)
	after := boundCodexPGRecord(t, a.ID)
	require.Equal(t, service.OpenAIUsageUnauthorizedReason, after["temp_unschedulable_reason"])
	until, err := time.Parse(time.RFC3339Nano, after["temp_unschedulable_until"].(string))
	require.NoError(t, err)
	require.WithinDuration(t, started.Add(10*time.Minute), until, 5*time.Second)
	require.Equal(t, service.StatusDisabled, after["status"])
	require.Equal(t, false, after["schedulable"])
	require.Equal(t, "manual disable", after["error_message"])
	require.Equal(t, openAIUsageAuthPGWithoutTempFields(before), openAIUsageAuthPGWithoutTempFields(after))
}

func TestOpenAIUsageAuthPGSuccessClearsOnlyExactOwnMarker(t *testing.T) {
	for _, tc := range []struct {
		name        string
		reason      string
		wantApply   bool
		wantCleared bool
	}{
		{name: "exact marker", reason: service.OpenAIUsageUnauthorizedReason, wantApply: true, wantCleared: true},
		{name: "other oauth401 marker", reason: "oauth_401: another authentication block"},
		{name: "unrelated marker", reason: "manual maintenance"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := boundCodexPGAccount(t)
			_, err := integrationDB.Exec(`
				UPDATE accounts
				SET temp_unschedulable_until = NOW() + INTERVAL '1 hour', temp_unschedulable_reason = $2
				WHERE id = $1
			`, a.ID, tc.reason)
			require.NoError(t, err)
			before := boundCodexPGRecord(t, a.ID)

			applied, err := boundCodexPGRepo().UpdateOpenAIUsageAuthIfIdentityMatches(context.Background(), a, false)

			require.NoError(t, err)
			require.Equal(t, tc.wantApply, applied)
			after := boundCodexPGRecord(t, a.ID)
			if tc.wantCleared {
				require.Nil(t, after["temp_unschedulable_until"])
				require.Nil(t, after["temp_unschedulable_reason"])
				require.Equal(t, openAIUsageAuthPGWithoutTempFields(before), openAIUsageAuthPGWithoutTempFields(after))
				return
			}
			require.Equal(t, before, after)
		})
	}
}

func TestOpenAIUsageAuthPGChangedCredentialsRefuseCAS(t *testing.T) {
	for _, tc := range []struct {
		name  string
		query string
	}{
		{name: "access token", query: `UPDATE accounts SET credentials = credentials || '{"access_token":"new-access"}'::jsonb WHERE id = $1`},
		{name: "token version", query: `UPDATE accounts SET credentials = credentials || '{"_token_version":2}'::jsonb WHERE id = $1`},
	} {
		for _, unauthorized := range []bool{true, false} {
			t.Run(tc.name+map[bool]string{true: " old401", false: " late success"}[unauthorized], func(t *testing.T) {
				a := boundCodexPGAccount(t)
				_, err := integrationDB.Exec(tc.query, a.ID)
				require.NoError(t, err)
				before := boundCodexPGRecord(t, a.ID)

				applied, err := boundCodexPGRepo().UpdateOpenAIUsageAuthIfIdentityMatches(context.Background(), a, unauthorized)

				require.NoError(t, err)
				require.False(t, applied)
				require.Equal(t, before, boundCodexPGRecord(t, a.ID))
			})
		}
	}
}

func TestOpenAIUsageAuthPGActiveUnrelatedReasonSurvives(t *testing.T) {
	for _, reason := range []string{"manual maintenance", "oauthX401: wildcard must not match"} {
		t.Run(reason, func(t *testing.T) {
			a := boundCodexPGAccount(t)
			_, err := integrationDB.Exec(`
				UPDATE accounts
				SET temp_unschedulable_until = NOW() + INTERVAL '1 hour', temp_unschedulable_reason = $2
				WHERE id = $1
			`, a.ID, reason)
			require.NoError(t, err)
			before := boundCodexPGRecord(t, a.ID)

			applied, err := boundCodexPGRepo().UpdateOpenAIUsageAuthIfIdentityMatches(context.Background(), a, true)

			require.NoError(t, err)
			require.False(t, applied)
			require.Equal(t, before, boundCodexPGRecord(t, a.ID))
		})
	}
}

func TestOpenAIUsageAuthPGLateOld401LosesToLockedReauthorization(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	oldRequest := boundCodexPGAccount(t)
	newCredentials := map[string]any{
		"access_token": "synthetic-new-access", "refresh_token": "synthetic-new-refresh", "_token_version": 2,
	}
	credentials, err := json.Marshal(newCredentials)
	require.NoError(t, err)

	tx, err := integrationEntClient.Tx(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()
	_, err = tx.Client().ExecContext(ctx, `UPDATE accounts SET credentials = $1::jsonb WHERE id = $2`, string(credentials), oldRequest.ID)
	require.NoError(t, err)

	conn, err := integrationDB.Conn(ctx)
	require.NoError(t, err)
	defer conn.Close()
	var pid int
	require.NoError(t, conn.QueryRowContext(ctx, `SELECT pg_backend_pid()`).Scan(&pid))
	type outcome struct {
		applied bool
		err     error
	}
	done := make(chan outcome, 1)
	go func() {
		applied, err := (&accountRepository{sql: conn}).UpdateOpenAIUsageAuthIfIdentityMatches(ctx, oldRequest, true)
		done <- outcome{applied: applied, err: err}
	}()

	require.Eventually(t, func() bool {
		var waiting bool
		err := integrationDB.QueryRowContext(ctx,
			`SELECT COALESCE(wait_event_type = 'Lock', false) FROM pg_stat_activity WHERE pid = $1`, pid).Scan(&waiting)
		return err == nil && waiting
	}, 3*time.Second, 10*time.Millisecond)
	require.NoError(t, tx.Commit())
	result := <-done
	require.NoError(t, result.err)
	require.False(t, result.applied)

	record := boundCodexPGRecord(t, oldRequest.ID)
	require.Equal(t, "synthetic-new-access", record["credentials"].(map[string]any)["access_token"])
	require.Equal(t, float64(2), record["credentials"].(map[string]any)["_token_version"])
	require.Nil(t, record["temp_unschedulable_until"])
	require.Nil(t, record["temp_unschedulable_reason"])
}
