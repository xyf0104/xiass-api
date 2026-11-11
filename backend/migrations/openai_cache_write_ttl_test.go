package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOpenAICacheWriteTTLMigration(t *testing.T) {
	body, err := FS.ReadFile("272_openai_cache_write_ttl.sql")
	require.NoError(t, err)
	sql := strings.Join(strings.Fields(string(body)), " ")
	for _, field := range []string{"cache_creation_30m_tokens", "cache_creation_12h_tokens"} {
		require.Contains(t, sql, field+" INTEGER NOT NULL DEFAULT 0")
	}
	for _, table := range []string{"channel_model_pricing", "channel_pricing_intervals", "channel_account_stats_model_pricing", "channel_account_stats_pricing_intervals"} {
		require.Contains(t, sql, "ALTER TABLE "+table+" ADD COLUMN IF NOT EXISTS cache_write_12h_price NUMERIC(20,12)")
	}
	for _, destructive := range []string{"DROP ", "DELETE ", "UPDATE ", "TRUNCATE "} {
		require.NotContains(t, strings.ToUpper(sql), destructive)
	}
}
