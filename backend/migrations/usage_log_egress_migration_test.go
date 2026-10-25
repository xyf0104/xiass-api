package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestUsageLogEgressMigrationIsNullableAndBackfillFree(t *testing.T) {
	content, err := FS.ReadFile("270_add_usage_log_egress.sql")
	require.NoError(t, err)

	sql := strings.ToLower(string(content))
	require.Contains(t, sql, "set local lock_timeout = '2s'")
	require.Contains(t, sql, "alter table usage_logs")
	require.Contains(t, sql, "add column if not exists egress jsonb")
	require.NotContains(t, sql, "not null")
	require.NotContains(t, sql, "default")
	require.NotContains(t, sql, "update usage_logs")
	require.NotContains(t, sql, "references")
}
