//go:build unit

package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOpenAICacheTTLFinalRMBPreservesUSDCost(t *testing.T) {
	bs := newTestBillingService()
	tokens := UsageTokens{CacheCreationTokens: 1000, CacheCreation30mTokens: 400, CacheCreation12hTokens: 600}
	base, err := bs.GetModelPricing("gpt-5.6-sol")
	require.NoError(t, err)
	baseline := bs.computeTokenBreakdown(base, tokens, 7, "fast", false)
	for _, tc := range []struct {
		name    string
		pricing ChannelModelPricing
		want    float64
	}{
		{"flat legacy", ChannelModelPricing{CacheWritePrice: testPtrFloat64(3e-6)}, 0.003},
		{"separate TTL", ChannelModelPricing{CacheWritePrice: testPtrFloat64(3e-6), CacheWrite12hPrice: testPtrFloat64(9e-6)}, 0.0066},
		{"free 12h", ChannelModelPricing{CacheWrite12hPrice: testPtrFloat64(0)}, baseline.cacheCreation30mCost * 7},
		{"only 12h", ChannelModelPricing{CacheWrite12hPrice: testPtrFloat64(9e-6)}, baseline.cacheCreation30mCost*7 + 0.0054},
		{"interval flat override", ChannelModelPricing{CacheWritePrice: testPtrFloat64(3e-6), CacheWrite12hPrice: testPtrFloat64(9e-6), Intervals: []PricingInterval{{MinTokens: 1, CacheWritePrice: testPtrFloat64(2e-6)}}}, 0.002},
		{"interval multiplier", ChannelModelPricing{CacheWritePrice: testPtrFloat64(3e-6), CacheWrite12hPrice: testPtrFloat64(9e-6), Intervals: []PricingInterval{{MinTokens: 1, CacheWriteMultiplier: testPtrFloat64(2)}}}, 0.0132},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := tc.pricing
			p.Models = []string{"gpt-5.6-sol"}
			p.BillingMode, p.PriceMode = BillingModeToken, PriceModeFinal
			group := &Group{ID: 1, Platform: PlatformOpenAI, LongContextPricingEnabled: true, ModelPricing: []ChannelModelPricing{p}}
			cost, err := bs.CalculateCostUnified(CostInput{Ctx: context.Background(), Model: "gpt-5.6-sol", Group: group, GroupID: &group.ID, Tokens: tokens, RateMultiplier: 7, ServiceTier: "fast", Resolver: NewModelPricingResolver(nil, bs)})
			require.NoError(t, err)
			require.InDelta(t, baseline.TotalCost, cost.TotalCost, 1e-12, "USD account cost must retain its price snapshot")
			require.InDelta(t, tc.want, cost.ActualCost, 1e-12, "final RMB prices must not be multiplied again")
		})
	}
}

func TestOpenAICacheTTLInvalidBillingFallsBack(t *testing.T) {
	bs := newTestBillingService()
	p := &ModelPricing{CacheCreationPricePerToken: 1e-6, CacheCreation12hPricePerToken: testPtrFloat64(2e-6)}
	for _, tokens := range []UsageTokens{
		{CacheCreationTokens: 10},
		{CacheCreationTokens: 10, CacheCreation30mTokens: 1, CacheCreation12hTokens: 10},
		{CacheCreationTokens: 10, CacheCreation30mTokens: -1, CacheCreation12hTokens: 11},
		{CacheCreationTokens: 10, CacheCreation30mTokens: 1, CacheCreation12hTokens: 8},
	} {
		cost := bs.computeTokenBreakdown(p, tokens, 1, "", false)
		require.InDelta(t, 10e-6, cost.CacheCreationCost, 1e-12)
	}
}

func TestOpenAICacheTTLAccountStatsInterval(t *testing.T) {
	tokens := UsageTokens{CacheCreationTokens: 100, CacheCreation30mTokens: 25, CacheCreation12hTokens: 75}
	for _, tc := range []struct {
		interval PricingInterval
		want     float64
	}{
		{PricingInterval{CacheWriteMultiplier: testPtrFloat64(3)}, 525e-6},
		{PricingInterval{CacheWritePrice: testPtrFloat64(4e-6)}, 400e-6},
		{PricingInterval{CacheWrite12hPrice: testPtrFloat64(0)}, 25e-6},
	} {
		cost := calculateTokenStatsCost(&ChannelModelPricing{CacheWritePrice: testPtrFloat64(1e-6), CacheWrite12hPrice: testPtrFloat64(2e-6), Intervals: []PricingInterval{tc.interval}}, tokens)
		require.NotNil(t, cost)
		require.InDelta(t, tc.want, *cost, 1e-12)
	}
}

func TestCarbonTierPricingPreservesConfiguredCatalog(t *testing.T) {
	for _, tier := range []string{"high", "medium", "low"} {
		model := "gemini-4-carbon-" + tier
		require.Equal(t, "gemini-4-carbon", normalizeGeminiThinkingTierAlias(model))
		catalog := newStubPricingServiceFromJSON(t, `{"gemini-4-argon":{"input_cost_per_token":1,"output_cost_per_token":2}}`)
		require.NotNil(t, catalog.GetModelPricing(model))
		require.NotEqual(t, catalog.pricingData["gemini-4-argon"], catalog.GetModelPricing(model), "Carbon uses its supplied business rates")
		catalog = newStubPricingServiceFromJSON(t, `{"gemini-4-carbon":{"input_cost_per_token":0.000007,"output_cost_per_token":0.000009}}`)
		require.Same(t, catalog.pricingData["gemini-4-carbon"], catalog.GetModelPricing(model))
		require.Same(t, catalog.pricingData["gemini-4-carbon"], catalog.GetIdentifiedModelPricing("models/"+model))
	}
	catalog := newStubPricingServiceFromJSON(t, `{"gemini-4-carbon":{"input_cost_per_token":1},"gemini-4-carbon-high":{"input_cost_per_token":2}}`)
	require.Same(t, catalog.pricingData["gemini-4-carbon-high"], catalog.GetModelPricing("gemini-4-carbon-high"))
}
