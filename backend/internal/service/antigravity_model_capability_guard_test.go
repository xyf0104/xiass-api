package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestAntigravityCarbonCapabilityGuard(t *testing.T) {
	for _, plan := range []string{"Pro", "g1-pro-tier", "Ultra", "g1-ultra-tier", "g1-ultra-lite-tier", "Free", "free-tier", "", "enterprise"} {
		t.Run(plan, func(t *testing.T) {
			account := &Account{Platform: PlatformAntigravity, Type: AccountTypeOAuth,
				Credentials: map[string]any{"plan_type": plan, "model_mapping": map[string]any{
					"gemini-4-carbon-*": "gemini-4-carbon-high", "alias": "gemini-4-carbon-low",
					"gemini-3.7-flash-high": "gemini-3.7-flash-high",
				}}}
			ultra := plan == "Ultra" || plan == "g1-ultra-tier" || plan == "g1-ultra-lite-tier"
			for _, model := range []string{"gemini-4-carbon-high", "gemini-4-carbon-medium", "gemini-4-carbon-low", "alias"} {
				require.Equal(t, ultra, account.IsModelSupported(model), model)
				require.Equal(t, ultra, mapAntigravityModel(account, model) != "", model)
			}
			require.Equal(t, ultra, mapAntigravityModel(account, "models/gemini-4-carbon-high") != "")
			require.True(t, account.IsModelSupported("gemini-3.7-flash-high"))
			require.False(t, antigravityAccountAllowsModel(account, "gemini-4-carbon-disabled"))
			account.SetUpstreamModelMetadataSnapshot(UpstreamModelMetadataSnapshot{Source: "upstream", Models: map[string]UpstreamModelMetadata{
				"gemini-4-carbon-high": {ID: "gemini-4-carbon-high"},
			}})
			require.True(t, account.IsModelSupported("gemini-4-carbon-high"))
			account.SetUpstreamModelMetadataSnapshot(UpstreamModelMetadataSnapshot{Source: "upstream", DisabledModels: []string{"gemini-4-carbon-disabled"}, Models: map[string]UpstreamModelMetadata{
				"gemini-4-carbon-high": {ID: "gemini-4-carbon-high"},
			}})
			require.False(t, account.IsModelSupported("gemini-4-carbon-high"), "negative evidence wins over a positive entry")
			account.SetUpstreamModelMetadataSnapshot(UpstreamModelMetadataSnapshot{Source: "upstream", Models: map[string]UpstreamModelMetadata{
				"gemini-4-carbon-disabled": {ID: "gemini-4-carbon-disabled"},
			}})
			require.False(t, account.IsModelSupported("gemini-4-carbon-high"))
		})
	}
}

func TestAntigravityCarbonQuotaIsolation(t *testing.T) {
	for _, plan := range []string{"Pro", "Ultra"} {
		account := &Account{Platform: PlatformAntigravity, Credentials: map[string]any{"plan_type": plan}}
		for _, exhausted := range []string{"gemini-4-carbon-high", "gemini-4-argon-high", "gemini-3.7-flash-tiered"} {
			account.Extra = nil
			for _, key := range antigravityModelRateLimitKeys(account, exhausted) {
				setAccountModelRateLimitSnapshot(account, key, time.Now().Add(time.Hour), "", time.Now())
			}
			for _, candidate := range []string{"gemini-4-carbon-medium", "gemini-4-argon-low", "gemini-3.7-flash-tiered"} {
				if exhausted == "gemini-4-carbon-high" || candidate == "gemini-4-carbon-medium" {
					limited := false
					for _, key := range antigravityModelRateLimitKeys(account, candidate) {
						limited = limited || account.isRateLimitActiveForKey(key)
					}
					require.Equal(t, exhausted == "gemini-4-carbon-high" && candidate == "gemini-4-carbon-medium", limited)
				}
			}
		}
	}
}

func TestAntigravityCarbonCapabilityEvidenceIsExactAndIdentityBound(t *testing.T) {
	account := &Account{Platform: PlatformAntigravity, Type: AccountTypeOAuth, Credentials: map[string]any{
		"plan_type": "Pro", "access_token": "old-test-token", "model_mapping": map[string]any{
			"gemini-4-carbon-high": "gemini-4-carbon-high", "gemini-4-carbon-medium": "gemini-4-carbon-medium",
		},
	}}
	account.SetUpstreamModelMetadataSnapshot(UpstreamModelMetadataSnapshot{Source: "upstream", Models: map[string]UpstreamModelMetadata{
		"gemini-4-carbon-high": {ID: "gemini-4-carbon-high"},
	}})
	require.True(t, account.IsModelSupported("gemini-4-carbon-high"))
	require.False(t, account.IsModelSupported("gemini-4-carbon-medium"))
	account.Credentials["access_token"] = "new-test-token"
	require.False(t, account.IsModelSupported("gemini-4-carbon-high"), "stale credentials must invalidate affirmative capability evidence")
	account.SetUpstreamModelMetadataSnapshot(UpstreamModelMetadataSnapshot{Source: "upstream", Models: map[string]UpstreamModelMetadata{
		"gemini-4-carbon-high": {ID: "gemini-4-carbon-high"},
	}})
	proxyID := int64(123)
	account.ProxyID = &proxyID
	require.False(t, account.IsModelSupported("gemini-4-carbon-high"), "changed proxy must invalidate affirmative capability evidence")
}

func TestAntigravityCarbonDisabledOnlySnapshot(t *testing.T) {
	account := &Account{Platform: PlatformAntigravity, Type: AccountTypeOAuth,
		Credentials: map[string]any{"plan_type": "Ultra", "access_token": "test-token"}}
	account.SetUpstreamModelMetadataSnapshot(UpstreamModelMetadataSnapshot{
		Source: "upstream", DisabledModels: []string{"gemini-4-carbon-disabled"},
	})
	for _, model := range []string{"gemini-4-carbon-high", "gemini-4-carbon-medium", "gemini-4-carbon-low"} {
		require.False(t, account.IsModelSupported(model), model)
		require.Empty(t, mapAntigravityModel(account, model), model)
	}
	require.True(t, account.IsModelSupported("gemini-3.7-flash-high"))
}

func TestAntigravityCarbonDisabledOnlyCatalogPersistsEvidence(t *testing.T) {
	repo := &upstreamModelCapabilityRepo{}
	svc, account, _ := antigravityCapabilitySyncFixture(t, `{"models":{
		"gemini-4-carbon-disabled":{"displayName":"Carbon unavailable"}
	}}`, repo)
	account.Credentials["plan_type"] = "Ultra"
	catalog, err := svc.SyncUpstreamModelCatalog(context.Background(), account)
	require.NoError(t, err)
	require.Empty(t, catalog.Models)
	require.Empty(t, catalog.Metadata)
	snapshot := account.GetUpstreamModelMetadataSnapshot()
	require.NotNil(t, snapshot)
	require.Empty(t, snapshot.Models)
	require.Equal(t, []string{"gemini-4-carbon-disabled"}, snapshot.DisabledModels)
	require.False(t, account.IsModelSupported("gemini-4-carbon-high"))
}

func TestAntigravityCarbonDefaultCatalogRequiresCapability(t *testing.T) {
	for _, plan := range []string{"Pro", "Free", "", "unknown", "Ultra"} {
		t.Run(plan, func(t *testing.T) {
			account := &Account{Platform: PlatformAntigravity, Type: AccountTypeOAuth,
				Credentials: map[string]any{"plan_type": plan}}
			for _, model := range []string{"gemini-4-carbon-high", "gemini-4-carbon-medium", "gemini-4-carbon-low"} {
				require.Equal(t, plan == "Ultra", account.IsModelSupported(model), model)
				require.Equal(t, plan == "Ultra", mapAntigravityModel(account, model) != "", model)
			}
		})
	}
}

type antigravityCapabilityCatalogRepo struct {
	AccountRepository
	accounts []Account
}

func (r *antigravityCapabilityCatalogRepo) ListSchedulableUngroupedByPlatforms(context.Context, []string) ([]Account, error) {
	return r.accounts, nil
}

func TestAntigravityCarbonGeminiCatalogUsesAccountCapabilities(t *testing.T) {
	pro := Account{Platform: PlatformAntigravity, Type: AccountTypeOAuth,
		Credentials: map[string]any{"plan_type": "Pro"}}
	ultra := Account{Platform: PlatformAntigravity, Type: AccountTypeOAuth,
		Credentials: map[string]any{"plan_type": "Ultra"}}
	for _, tc := range []struct {
		name     string
		accounts []Account
		carbon   bool
	}{
		{name: "Pro only", accounts: []Account{pro}},
		{name: "Ultra available", accounts: []Account{pro, ultra}, carbon: true},
		{name: "Ultra disabled", accounts: []Account{ultra}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.name == "Ultra disabled" {
				tc.accounts[0].SetUpstreamModelMetadataSnapshot(UpstreamModelMetadataSnapshot{
					Source: "upstream", DisabledModels: []string{"gemini-4-carbon-disabled"},
				})
			}
			svc := &GeminiMessagesCompatService{accountRepo: &antigravityCapabilityCatalogRepo{accounts: tc.accounts}}
			models, err := svc.AntigravityGeminiModelIDs(context.Background(), nil, false)
			require.NoError(t, err)
			for _, model := range []string{"gemini-4-carbon-high", "gemini-4-carbon-medium", "gemini-4-carbon-low"} {
				if tc.carbon {
					require.Contains(t, models, model)
				} else {
					require.NotContains(t, models, model)
				}
			}
			require.Contains(t, models, "gemini-3.7-flash-high")
		})
	}
}
