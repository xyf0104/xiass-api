//go:build integration

package repository

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestUsageBillingDeletedKey_SettlesExactlyOnce(t *testing.T) {
	ctx := context.Background()
	repo := requireAtomicUsageBillingRepository(t)
	user, apiKey, account, cmd, usageLog := newAtomicUsageBillingFixture(t)
	_, err := integrationDB.ExecContext(ctx, "UPDATE api_keys SET deleted_at = NOW() WHERE id = $1", apiKey.ID)
	require.NoError(t, err)

	first, err := repo.ApplyWithUsageLog(ctx, cmd, usageLog)
	require.NoError(t, err)
	require.True(t, first.Applied)
	require.False(t, first.APIKeyQuotaExhausted)
	retry, err := repo.ApplyWithUsageLog(ctx, cmd, usageLog)
	require.NoError(t, err)
	require.False(t, retry.Applied)

	var balance, quotaUsed, usage5h, accountQuota, actualCost float64
	var count int
	require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT balance FROM users WHERE id = $1", user.ID).Scan(&balance))
	require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT quota_used, usage_5h FROM api_keys WHERE id = $1", apiKey.ID).Scan(&quotaUsed, &usage5h))
	require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT COALESCE((extra->>'quota_used')::numeric, 0) FROM accounts WHERE id = $1", account.ID).Scan(&accountQuota))
	require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT COUNT(*), COALESCE(SUM(actual_cost), 0) FROM usage_logs WHERE request_id = $1 AND api_key_id = $2", cmd.RequestID, apiKey.ID).Scan(&count, &actualCost))
	require.InDelta(t, 98.75, balance, 0.00000001)
	require.Zero(t, quotaUsed)
	require.Zero(t, usage5h)
	require.InDelta(t, 3.0, accountQuota, 0.00000001)
	require.Equal(t, 1, count)
	require.InDelta(t, 1.25, actualCost, 0.00000001)
}
