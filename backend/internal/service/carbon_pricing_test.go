//go:build unit

package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestCarbonDatedFallbackBillingAndDisplay(t *testing.T) {
	cutoff := time.Date(2026, 11, 30, 16, 0, 0, 0, time.UTC)
	require.True(t, cutoff.Equal(carbonStandardPricingAt))
	catalog := &PricingService{pricingData: map[string]*LiteLLMModelPricing{}}
	bs := NewBillingService(nil, catalog)
	resolver := NewModelPricingResolver(nil, bs)
	display := &ChannelService{pricingService: catalog}
	for _, model := range []string{"gemini-4-carbon", "gemini-4-carbon-high", "gemini-4-carbon-medium", "gemini-4-carbon-low", "models/gemini-4-carbon-high"} {
		for _, tc := range []struct {
			name                 string
			at                   time.Time
			input, output, cache float64
		}{
			{"before", cutoff.Add(-time.Nanosecond), 2e-6, 10e-6, 0.1e-6},
			{"boundary", cutoff, 4e-6, 20e-6, 0.2e-6},
			{"after", cutoff.Add(time.Hour), 4e-6, 20e-6, 0.2e-6},
		} {
			t.Run(model+"/"+tc.name, func(t *testing.T) {
				p := catalog.GetModelPricingAt(model, tc.at)
				require.NotNil(t, p)
				require.InDelta(t, tc.input, p.InputCostPerToken, 1e-15)
				require.InDelta(t, tc.output, p.OutputCostPerToken, 1e-15)
				require.InDelta(t, tc.cache, p.CacheReadInputTokenCost, 1e-15)
				require.True(t, bs.HasIdentifiedTokenPricing(model))
				cost, err := bs.CalculateCostUnified(CostInput{
					Ctx: context.Background(), Model: model, PricingAt: tc.at, Resolver: resolver,
					Tokens: UsageTokens{InputTokens: 1_000_000, OutputTokens: 1_000_000, CacheReadTokens: 1_000_000}, RateMultiplier: 7,
				})
				require.NoError(t, err)
				want := (tc.input + tc.output + tc.cache) * 1e6
				require.InDelta(t, want, cost.TotalCost, 1e-10)
				require.InDelta(t, want*7, cost.ActualCost, 1e-10)
				models := []SupportedModel{{Name: model}}
				display.fillGlobalPricingFallbackAt(models, tc.at)
				require.InDelta(t, cost.InputCost/1e6, *models[0].Pricing.InputPrice, 1e-15)
				require.InDelta(t, cost.OutputCost/1e6, *models[0].Pricing.OutputPrice, 1e-15)
				require.InDelta(t, cost.CacheReadCost/1e6, *models[0].Pricing.CacheReadPrice, 1e-15)
				// The same service instance crosses the cutoff without catalog reload.
				require.Empty(t, catalog.pricingData)
			})
		}
	}
	for _, model := range []string{"gemini-4-carbon-disabled", "gemini-4-carbon-high-preview", "gemini-4-carbon-2"} {
		require.Nil(t, catalog.GetModelPricingAt(model, cutoff))
	}
	// No dynamic pricing service is required for the safety fallback.
	cost, err := NewBillingService(nil, nil).CalculateCostUnified(CostInput{
		Model: "gemini-4-carbon-low", PricingAt: cutoff, Tokens: UsageTokens{InputTokens: 1_000_000}, RateMultiplier: 1,
	})
	require.NoError(t, err)
	require.InDelta(t, 4, cost.TotalCost, 1e-12)
}

func TestCarbonDatedChannelAndGroupOverrides(t *testing.T) {
	const gid int64 = 45
	catalog := &PricingService{}
	bs := NewBillingService(nil, catalog)
	card := ChannelModelPricing{Platform: PlatformAntigravity, Models: []string{"gemini-4-carbon"}, BillingMode: BillingModeToken,
		InputPrice: testPtrFloat64(3e-6), Intervals: []PricingInterval{{MinTokens: 100, OutputMultiplier: testPtrFloat64(2)}}}
	channel := &ChannelService{pricingService: catalog}
	channel.cache.Store(populateChannelCache([]Channel{{ID: 1, Status: StatusActive, GroupIDs: []int64{gid}, ModelPricing: []ChannelModelPricing{card}}}, map[int64]string{gid: PlatformAntigravity}))
	resolver := NewModelPricingResolver(channel, bs)
	group := &Group{ID: gid, LongContextPricingEnabled: true}
	before := carbonStandardPricingAt.Add(-time.Second)
	stale := resolver.Resolve(context.Background(), PricingInput{Model: "gemini-4-carbon-high", GroupID: &group.ID, Group: group, PricingAt: before})
	for _, tc := range []struct {
		at            time.Time
		output, cache float64
	}{
		{before, 20, 0.1}, {carbonStandardPricingAt, 40, 0.2},
	} {
		input := CostInput{Ctx: context.Background(), Model: "gemini-4-carbon-high", GroupID: &group.ID, Group: group,
			PricingAt: tc.at, Resolved: stale, Resolver: resolver, RateMultiplier: 7,
			Tokens: UsageTokens{InputTokens: 1_000_000, OutputTokens: 1_000_000, CacheReadTokens: 1_000_000}}
		cost, err := bs.CalculateCostUnified(input)
		require.NoError(t, err)
		require.InDelta(t, 3+tc.output+tc.cache, cost.TotalCost, 1e-10)
		require.InDelta(t, cost.TotalCost*7, cost.ActualCost, 1e-10)
		models := []SupportedModel{{Name: input.Model, Pricing: &card}}
		channel.fillGlobalPricingFallbackAt(models, tc.at)
		p := models[0].Pricing
		displayed := intervalToModelPricing(&p.Intervals[0], &ModelPricing{InputPricePerToken: *p.InputPrice, OutputPricePerToken: *p.OutputPrice, CacheReadPricePerToken: *p.CacheReadPrice}, p)
		require.InDelta(t, cost.TotalCost, 1e6*(displayed.InputPricePerToken+displayed.OutputPricePerToken+displayed.CacheReadPricePerToken), 1e-10)
		require.Nil(t, card.OutputPrice, "display must not freeze inherited prices into stored cards")

		group.ModelPricing = []ChannelModelPricing{{Models: []string{"gemini-4-carbon"}, BillingMode: BillingModeToken, PriceMode: PriceModeFinal, InputPrice: testPtrFloat64(5e-6)}}
		input.Resolved = nil
		finalCost, err := bs.CalculateCostUnified(input)
		require.NoError(t, err)
		require.InDelta(t, cost.TotalCost, finalCost.TotalCost, 1e-10)
		require.InDelta(t, 5+(tc.output+tc.cache)*7, finalCost.ActualCost, 1e-10)
		group.ModelPricing[0].PriceMode = PriceModeBase
		baseCost, err := bs.CalculateCostUnified(input)
		require.NoError(t, err)
		require.InDelta(t, 5+tc.output/2+tc.cache, baseCost.TotalCost, 1e-10, "group base cards take precedence over channel ladders")
		group.ModelPricing = nil
	}
}

func TestCarbonCatalogOverrideSurvivesDateSwitch(t *testing.T) {
	catalog := newStubPricingServiceFromJSON(t, `{"gemini-4-carbon":{"input_cost_per_token":0.000007,"output_cost_per_token":0.000030,"cache_read_input_token_cost":0.0000007}}`)
	bs := NewBillingService(nil, catalog)
	for _, at := range []time.Time{carbonStandardPricingAt.Add(-time.Hour), carbonStandardPricingAt.Add(time.Hour)} {
		cost, err := bs.CalculateCostUnified(CostInput{Model: "gemini-4-carbon-medium", PricingAt: at,
			Tokens: UsageTokens{InputTokens: 1_000_000, OutputTokens: 1_000_000, CacheReadTokens: 1_000_000}, RateMultiplier: 1})
		require.NoError(t, err)
		require.InDelta(t, 37.7, cost.TotalCost, 1e-10)
	}
}

func TestCarbonChannelDisplayOverridePrecedence(t *testing.T) {
	const groupID int64 = 45
	const model = "gemini-4-carbon-high"
	base := ChannelModelPricing{Platform: PlatformAntigravity, Models: []string{"gemini-4-carbon"}, InputPrice: testPtrFloat64(3e-6)}
	wildcard := ChannelModelPricing{Platform: PlatformAntigravity, Models: []string{"gemini-4-carbon-*"}, InputPrice: testPtrFloat64(5e-6)}
	exact := ChannelModelPricing{Platform: PlatformAntigravity, Models: []string{model}, InputPrice: testPtrFloat64(7e-6)}
	for _, tc := range []struct {
		name  string
		cards []ChannelModelPricing
		want  float64
	}{
		{"base", []ChannelModelPricing{base}, 3},
		{"wildcard_before_base", []ChannelModelPricing{base, wildcard}, 5},
		{"exact_before_wildcard", []ChannelModelPricing{base, wildcard, exact}, 7},
	} {
		t.Run(tc.name, func(t *testing.T) {
			catalog := &PricingService{}
			bs := NewBillingService(nil, catalog)
			channel := Channel{ID: 1, Status: StatusActive, GroupIDs: []int64{groupID}, ModelPricing: tc.cards,
				ModelMapping: map[string]map[string]string{PlatformAntigravity: {model: model}}}
			svc := &ChannelService{pricingService: catalog}
			svc.cache.Store(populateChannelCache([]Channel{channel}, map[int64]string{groupID: PlatformAntigravity}))
			resolver := NewModelPricingResolver(svc, bs)
			for _, at := range []time.Time{carbonStandardPricingAt.Add(-time.Second), carbonStandardPricingAt} {
				gid := groupID
				cost, err := bs.CalculateCostUnified(CostInput{Ctx: context.Background(), Model: model, GroupID: &gid,
					PricingAt: at, Resolver: resolver, Tokens: UsageTokens{InputTokens: 1_000_000}, RateMultiplier: 1})
				require.NoError(t, err)
				require.InDelta(t, tc.want, cost.TotalCost, 1e-12)
				models := channel.SupportedModels()
				svc.fillGlobalPricingFallbackAt(models, at)
				found := false
				for _, displayed := range models {
					if displayed.Name == model {
						found = true
						require.NotNil(t, displayed.Pricing)
						require.NotNil(t, displayed.Pricing.InputPrice)
						require.InDelta(t, cost.TotalCost, *displayed.Pricing.InputPrice*1e6, 1e-12)
					}
				}
				require.True(t, found)
			}
		})
	}
}
