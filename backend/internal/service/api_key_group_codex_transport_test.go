package service

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMultiGroupCodexMetadataDoesNotAdvertiseUnsupportedWebSockets(t *testing.T) {
	for _, platform := range []string{PlatformAnthropic, PlatformAntigravity, PlatformGemini, PlatformOpenAI, PlatformComposite} {
		t.Run(platform, func(t *testing.T) {
			// Use a model whose official descriptor prefers WS to prove that an
			// alias/descriptor cannot override the selected provider's transport.
			model := "gpt-6.1-sol"
			group := &Group{ID: 1, Platform: platform}
			routes := []CompositeModelRoute{{ID: 1, GroupID: 1, PublicModel: model, UpstreamModel: "claude-sonnet-4-6", TargetPlatform: PlatformAnthropic, MatchType: CompositeRouteMatchExact, Endpoint: CompositeRouteEndpointResponses, Enabled: true}}
			body, err := buildCodexModelsManifestForAccounts(platform, []string{model}, nil, group, routes, true)
			require.NoError(t, err)
			var envelope struct {
				Models []map[string]any `json:"models"`
			}
			require.NoError(t, json.Unmarshal(body, &envelope))
			require.Len(t, envelope.Models, 1)
			require.Equal(t, platform == PlatformOpenAI, envelope.Models[0]["prefer_websockets"])
		})
	}
}
