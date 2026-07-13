package migrations

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMigration176UpdatesOnlyExactNoWindDefaultSiteName(t *testing.T) {
	content, err := FS.ReadFile("176_update_default_site_name_to_xiass.sql")
	require.NoError(t, err)

	sql := string(content)
	require.Contains(t, sql, "UPDATE settings")
	require.Contains(t, sql, "SET value = 'XIASS API'")
	require.Contains(t, sql, "WHERE key = 'site_name'")
	require.Contains(t, sql, "AND value = 'NoWind API'")
	require.NotContains(t, sql, "LOWER(")
	require.NotContains(t, sql, "TRIM(")
	require.NotContains(t, sql, "INSERT INTO settings")
}
