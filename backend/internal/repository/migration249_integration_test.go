//go:build integration

package repository

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	dbmigrations "github.com/Wei-Shaw/sub2api/migrations"
	"github.com/stretchr/testify/require"
)

func TestMigration249BackfillsAuthorizationHistoryWithoutOverwritingExactState(t *testing.T) {
	tx := testTx(t)
	ctx := context.Background()
	migrationSQL, err := dbmigrations.FS.ReadFile("249_backfill_openai_reauthorization_history.sql")
	require.NoError(t, err)

	insertAccount := func(name, extra string, lastUsedAt *time.Time) int64 {
		var id int64
		require.NoError(t, tx.QueryRowContext(ctx, `
INSERT INTO accounts (name, platform, type, credentials, extra, last_used_at)
VALUES ($1, 'openai', 'oauth', '{}'::jsonb, $2::jsonb, $3)
RETURNING id
`, name, extra, lastUsedAt).Scan(&id))
		return id
	}
	insertAudit := func(accountID int64, at time.Time, path string) {
		_, err := tx.ExecContext(ctx, `
INSERT INTO audit_logs (created_at, action, method, path, status_code, extra)
VALUES ($1, 'admin.openai.reauthorization', 'POST', $2, 200, jsonb_build_object('params', jsonb_build_object('id', $3::text)))
`, at, path, accountID)
		require.NoError(t, err)
	}
	readState := func(accountID int64) service.OpenAIReauthorizationState {
		var raw []byte
		require.NoError(t, tx.QueryRowContext(ctx, `
SELECT extra->'xiass_openai_reauthorization_state' FROM accounts WHERE id = $1
`, accountID).Scan(&raw))
		var state service.OpenAIReauthorizationState
		require.NoError(t, json.Unmarshal(raw, &state))
		state.Normalize()
		return state
	}

	first := time.Date(2026, time.September, 1, 8, 0, 0, 0, time.UTC)
	second := time.Date(2026, time.September, 9, 9, 30, 0, 0, time.UTC)
	twoSuccessesID := insertAccount("migration-249-two-successes", `{}`, nil)
	insertAudit(twoSuccessesID, first, "/api/v1/admin/accounts/:id/apply-oauth-credentials")
	insertAudit(twoSuccessesID, second, "/api/v1/admin/accounts/:id/apply-oauth-credentials")

	usedAfterAttempt := first.Add(2 * time.Hour)
	inferredID := insertAccount("migration-249-inferred", `{}`, &usedAfterAttempt)
	insertAudit(inferredID, first, "/api/v1/admin/openai/accounts/:id/reauthorize")

	baselineID := insertAccount("migration-249-baseline", `{}`, nil)
	exactID := insertAccount("migration-249-exact", `{
  "xiass_openai_reauthorization_state": {
    "version": 1,
    "attempt_count": 4,
    "success_count": 4,
    "last_result": "success",
    "last_event_key": "keep-me",
    "history_source": "xiass_state",
    "history_confidence": "exact"
  }
}`, nil)

	_, err = tx.ExecContext(ctx, string(migrationSQL))
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, string(migrationSQL))
	require.NoError(t, err)

	twoSuccesses := readState(twoSuccessesID)
	require.Equal(t, 2, twoSuccesses.SuccessCount)
	require.Equal(t, []time.Time{first, second}, twoSuccesses.SuccessfulAuthorizationTimes)
	require.Equal(t, second, *twoSuccesses.LastSucceededAt)
	require.Equal(t, "startup_audit_backfill", twoSuccesses.HistorySource)

	inferred := readState(inferredID)
	require.Equal(t, 1, inferred.SuccessCount)
	require.Equal(t, []time.Time{first}, inferred.SuccessfulAuthorizationTimes)

	baseline := readState(baselineID)
	require.True(t, baseline.IsTracked())
	require.False(t, baseline.HasHistory())
	require.Equal(t, "startup_baseline", baseline.HistorySource)

	exact := readState(exactID)
	require.Equal(t, 4, exact.SuccessCount)
	require.Equal(t, "keep-me", exact.LastEventKey)
	require.Equal(t, "xiass_state", exact.HistorySource)
}
