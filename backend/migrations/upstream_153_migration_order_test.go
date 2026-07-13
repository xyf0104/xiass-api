package migrations

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestUpstream153MigrationsFollowXIASSSequence(t *testing.T) {
	entries, err := FS.ReadDir(".")
	require.NoError(t, err)

	positions := map[string]int{}
	for i, entry := range entries {
		positions[entry.Name()] = i
	}

	previous := "176_update_default_site_name_to_xiass.sql"
	webSearch := "177_group_web_search_price_per_call.sql"
	latestIP := "178_add_usage_logs_api_key_latest_ip_index_notx.sql"
	require.Contains(t, positions, previous)
	require.Contains(t, positions, webSearch)
	require.Contains(t, positions, latestIP)
	require.Less(t, positions[previous], positions[webSearch])
	require.Less(t, positions[webSearch], positions[latestIP])

	content, err := FS.ReadFile(webSearch)
	require.NoError(t, err)
	require.Contains(t, string(content), "web_search_price_per_call")
}
