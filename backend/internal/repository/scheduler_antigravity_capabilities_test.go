//go:build unit

package repository

import (
	"encoding/json"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestSchedulerAntigravityCapabilities(t *testing.T) {
	for _, disabled := range []bool{false, true} {
		account := service.Account{
			ID: 617, Platform: service.PlatformAntigravity, Type: service.AccountTypeOAuth,
			Credentials: map[string]any{
				"access_token":  "test-secret",
				"model_mapping": map[string]any{"gemini-4-carbon-medium": "gemini-4-carbon-medium"},
			},
		}
		capabilities := service.UpstreamModelMetadataSnapshot{
			Models: map[string]service.UpstreamModelMetadata{
				"gemini-4-carbon-medium": {ID: "gemini-4-carbon-medium"},
			},
		}
		if disabled {
			capabilities.DisabledModels = []string{"gemini-4-carbon-disabled"}
		}
		account.SetUpstreamModelMetadataSnapshot(capabilities)
		originalIdentity := account.GetUpstreamModelMetadataSnapshot().Identity
		metadata := buildSchedulerMetadataAccount(account)
		payload, err := json.Marshal(metadata)
		require.NoError(t, err)
		require.NotContains(t, string(payload), "test-secret")
		var restored service.Account
		require.NoError(t, json.Unmarshal(payload, &restored))
		require.NotNil(t, restored.GetUpstreamModelMetadataSnapshot())
		require.Equal(t, !disabled, restored.IsModelSupported("gemini-4-carbon-medium"))
		require.Equal(t, originalIdentity, account.GetUpstreamModelMetadataSnapshot().Identity)

		// Changes since synchronization must not revive stale capabilities.
		account.Credentials["access_token"] = "changed-token"
		stale := buildSchedulerMetadataAccount(account)
		require.Nil(t, stale.GetUpstreamModelMetadataSnapshot())
		require.False(t, stale.IsModelSupported("gemini-4-carbon-medium"))
		proxyID := int64(182)
		restored.ProxyID = &proxyID
		require.Nil(t, restored.GetUpstreamModelMetadataSnapshot())
	}
}
