package migrations

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMigration175UpdatesOnlyExactLegacyDefaultSiteName(t *testing.T) {
	content, err := FS.ReadFile("175_update_default_site_name.sql")
	require.NoError(t, err)

	sql := string(content)
	require.Contains(t, sql, "UPDATE settings")
	require.Contains(t, sql, "SET value = 'NoWind API'")
	require.Contains(t, sql, "WHERE key = 'site_name'")
	require.Contains(t, sql, "AND value = 'Sub2API'")
	require.NotContains(t, sql, "LOWER(")
	require.NotContains(t, sql, "TRIM(")
	require.NotContains(t, sql, "INSERT INTO settings")
}
