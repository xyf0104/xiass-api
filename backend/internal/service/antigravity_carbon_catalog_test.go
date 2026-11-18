package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAntigravityCarbonCatalogRouteSelection(t *testing.T) {
	const base = "gemini-4-carbon"
	for _, tc := range []struct {
		name, requested, effort, want string
		models, disabled              []string
	}{
		{"base wins", base + "-low", "high", base, []string{base, base + "-low"}, nil},
		{"explicit suffix wins", base + "-low", "high", base + "-low", []string{base + "-high", base + "-low"}, nil},
		{"effort selects", base, "low", base + "-low", []string{base + "-high", base + "-low"}, nil},
		{"default selects", base, "", base + "-high", []string{base + "-high", base + "-low"}, nil},
		{"available fallback", base, "medium", base + "-low", []string{base + "-low"}, nil},
		{"no invented suffix", base + "-medium", "", "", []string{base + "-low"}, nil},
		{"disabled family", base, "low", "", []string{base + "-low"}, []string{base + "-disabled"}},
		{"disabled tier", base + "-low", "", "", []string{base, base + "-low"}, []string{base + "-low-disabled"}},
		{"skip disabled tier", base, "high", base + "-low", []string{base + "-high", base + "-low"}, []string{base + "-high-disabled"}},
		{"unrelated catalog", base, "", "", []string{"gemini-4-argon-high"}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := newAntigravityTierAccount()
			models := make(map[string]UpstreamModelMetadata)
			for _, id := range tc.models {
				models[id] = UpstreamModelMetadata{ID: id}
			}
			a.SetUpstreamModelMetadataSnapshot(UpstreamModelMetadataSnapshot{Source: "upstream", Models: models, DisabledModels: tc.disabled})
			require.Equal(t, tc.want, (&AntigravityGatewayService{}).getMappedModelForThinkingLevel(a, tc.requested, tc.effort))
		})
	}
}

func TestAntigravityCarbonCatalogRefreshKeepsExactIDs(t *testing.T) {
	const base = "gemini-4-carbon"
	svc, account, _ := antigravityCapabilitySyncFixture(t, `{"models":{"gemini-4-carbon-low":{}}}`, &upstreamModelCapabilityRepo{})
	account.Credentials["model_mapping"] = map[string]any{base + "-high": base + "-high", base + "-low": base + "-low"}
	account.SetUpstreamModelMetadataSnapshot(UpstreamModelMetadataSnapshot{Source: "upstream", Models: map[string]UpstreamModelMetadata{
		base: completeUpstreamModelCapability(base, 1048576, 65536),
	}})
	catalog, err := svc.SyncUpstreamModelCatalog(context.Background(), account)
	require.NoError(t, err)
	require.Equal(t, []string{base}, catalog.Models)
	snapshot := account.GetUpstreamModelMetadataSnapshot()
	require.NotNil(t, snapshot)
	require.Equal(t, map[string]UpstreamModelMetadata{base + "-low": {ID: base + "-low"}}, snapshot.Models)
	require.Equal(t, base+"-low", mapAntigravityModel(account, base), "legacy tier-only whitelist must admit the public base through a real supported variant")
	require.Empty(t, mapAntigravityModel(account, base+"-high"))
	proxyID := int64(182)
	account.ProxyID = &proxyID
	require.Empty(t, mapAntigravityModel(account, base), "changed identity cannot reuse the old catalog")
}

func TestPelicanAntigravityCarbonCatalogRoutes(t *testing.T) {
	for _, id := range []string{"gemini-4-carbon", "gemini-4-carbon-low", "gemini-4-carbon-medium", "gemini-4-carbon-high"} {
		t.Run(id, func(t *testing.T) {
			a, upstream, svc := pelicanAdapterFixture(pelicanAdapterCase{platform: PlatformAntigravity, typ: AccountTypeOAuth, wire: "wrapped"}, "<html></html>")
			a.SetUpstreamModelMetadataSnapshot(UpstreamModelMetadataSnapshot{Source: "upstream", Models: map[string]UpstreamModelMetadata{id: {ID: id, MaxOutputTokens: 16384}}})
			var billedModel string
			result, err := svc.RunPelicanBenchmark(context.Background(), a.ID, "gemini-4-carbon", func(model string) error { billedModel = model; return nil })
			require.NoError(t, err)
			require.Equal(t, "<html></html>", result.HTML)
			require.Equal(t, id, upstream.body["model"])
			require.Equal(t, id, billedModel)
			request, ok := upstream.body["request"].(map[string]any)
			require.True(t, ok)
			generation, ok := request["generationConfig"].(map[string]any)
			require.True(t, ok)
			require.Equal(t, float64(16384), generation["maxOutputTokens"])
		})
	}
}
