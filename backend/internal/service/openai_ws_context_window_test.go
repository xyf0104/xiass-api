package service

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestNormalizeOpenAIWSContextWindowBoundary(t *testing.T) {
	cases := []struct {
		name, payload, previous, window string
		changed, removed                bool
	}{
		{"same", `{"previous_response_id":"resp_a","client_metadata":{"x-codex-window-id":"a"}}`, "a", "a", false, false},
		{"changed", `{"previous_response_id":"resp_a","client_metadata":{"x-codex-window-id":"b"}}`, "a", "b", true, true},
		{"changed_without_anchor", `{"client_metadata":{"x-codex-window-id":"b"}}`, "a", "b", true, false},
		{"embedded", `{"previous_response_id":"resp_a","client_metadata":{"x-codex-turn-metadata":"{\"window_id\":\"b\"}"}}`, "a", "b", true, true},
		{"explicit_precedence", `{"previous_response_id":"resp_a","client_metadata":{"x-codex-window-id":"a","x-codex-turn-metadata":"{\"window_id\":\"b\"}"}}`, "a", "a", false, false},
		{"unknown_previous", `{"previous_response_id":"resp_a","client_metadata":{"x-codex-window-id":"b"}}`, "", "b", false, false},
		{"missing_metadata", `{"previous_response_id":"resp_a"}`, "a", "", false, false},
		{"malformed_embedded", `{"previous_response_id":"resp_a","client_metadata":{"x-codex-turn-metadata":"invalid"}}`, "a", "", false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			updated, boundary, err := normalizeOpenAIWSContextWindowBoundary([]byte(tc.payload), tc.previous)
			require.NoError(t, err)
			require.Equal(t, tc.window, boundary.WindowID)
			require.Equal(t, tc.changed, boundary.Changed)
			require.Equal(t, tc.removed, boundary.PreviousResponseIDRemoved)
			if tc.removed {
				require.False(t, gjson.GetBytes(updated, "previous_response_id").Exists())
			} else {
				require.Equal(t, tc.payload, string(updated))
			}
		})
	}
}
