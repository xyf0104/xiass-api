//go:build unit

package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAccountStatsPricingIntervals_ContextBoundary(t *testing.T) {
	pricing := ChannelModelPricing{
		Models: []string{"gpt-6-astra"}, BillingMode: BillingModeToken,
		InputPrice: testPtrFloat64(1e-6), OutputPrice: testPtrFloat64(2e-6),
		CacheWritePrice: testPtrFloat64(3e-6), CacheWrite1hPrice: testPtrFloat64(7e-6),
		CacheReadPrice: testPtrFloat64(.5e-6),
		Intervals: []PricingInterval{{MinTokens: 272000,
			InputPrice: testPtrFloat64(2e-6), OutputPrice: testPtrFloat64(3e-6),
			CacheWritePrice: testPtrFloat64(6e-6), CacheWrite1hPrice: testPtrFloat64(14e-6),
			CacheReadPrice: testPtrFloat64(1e-6)}},
	}
	channel := &Channel{ID: 13, Status: StatusActive, ApplyPricingToAccountStats: true,
		AccountStatsPricingRules: []AccountStatsPricingRule{{AccountIDs: []int64{621}, Pricing: []ChannelModelPricing{pricing}}}}
	cs := newTestChannelServiceForStats(t, channel, 33, PlatformOpenAI)
	for _, tc := range []struct {
		name   string
		tokens UsageTokens
		want   float64
	}{
		{"272000_with_huge_output_stays_short", UsageTokens{InputTokens: 272000, OutputTokens: 999999}, .272 + 999999*2e-6},
		{"272001_is_long", UsageTokens{InputTokens: 272001, OutputTokens: 13}, 272001*2e-6 + 13*3e-6},
		{"output_only_stays_short", UsageTokens{OutputTokens: 999999}, 999999 * 2e-6},
		{"cache_read_272000_with_huge_output_stays_short", UsageTokens{InputTokens: 1, CacheReadTokens: 271999, OutputTokens: 999999}, 1e-6 + 271999*.5e-6 + 999999*2e-6},
		{"cache_read_272001_is_long", UsageTokens{InputTokens: 1, CacheReadTokens: 272000, OutputTokens: 13}, 2e-6 + 272000*1e-6 + 13*3e-6},
		{"cache_write_272000_with_huge_output_stays_short", UsageTokens{InputTokens: 1, CacheCreationTokens: 271999, CacheCreation5mTokens: 1000, CacheCreation1hTokens: 270999, OutputTokens: 999999}, 1e-6 + 1000*3e-6 + 270999*7e-6 + 999999*2e-6},
		{"cache_write_272001_is_long", UsageTokens{InputTokens: 1, CacheCreationTokens: 272000, CacheCreation5mTokens: 1000, CacheCreation1hTokens: 271000, OutputTokens: 13}, 2e-6 + 1000*6e-6 + 271000*14e-6 + 13*3e-6},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Exercise rule precedence and cached channel lookup, not only the helper.
			cost := resolveAccountStatsCostForModels(context.Background(), cs, nil, 621, 33,
				"gpt-6-astra", "gpt-6-astra", tc.tokens, 1, 999, "priority")
			require.NotNil(t, cost)
			require.InDelta(t, tc.want, *cost, 1e-12)
		})
	}
}

func TestAccountStatsPricingIntervals_InheritAndExplicitZero(t *testing.T) {
	base := ChannelModelPricing{
		InputPrice: testPtrFloat64(1e-6), OutputPrice: testPtrFloat64(2e-6),
		CacheWritePrice: testPtrFloat64(3e-6), CacheWrite1hPrice: testPtrFloat64(7e-6),
		CacheReadPrice: testPtrFloat64(.5e-6), ImageOutputPrice: testPtrFloat64(11e-6),
	}
	tokens := UsageTokens{InputTokens: 3, OutputTokens: 5, CacheCreationTokens: 10,
		CacheCreation5mTokens: 4, CacheCreation1hTokens: 6, CacheReadTokens: 7, ImageOutputTokens: 2}
	for _, tc := range []struct {
		name     string
		interval PricingInterval
		want     float64
	}{
		{"nil_fields_and_image_inherit_base", PricingInterval{InputPrice: testPtrFloat64(2e-6)}, 95.5e-6},
		{"nil_1h_follows_explicit_interval_5m", PricingInterval{CacheWritePrice: testPtrFloat64(6e-6)}, 98.5e-6},
		{"explicit_1h_override", PricingInterval{CacheWrite1hPrice: testPtrFloat64(14e-6)}, 134.5e-6},
		{"zero_input_does_not_inherit", PricingInterval{InputPrice: testPtrFloat64(0)}, 89.5e-6},
		{"zero_output_does_not_inherit", PricingInterval{OutputPrice: testPtrFloat64(0)}, 82.5e-6},
		{"zero_cache_read_does_not_inherit", PricingInterval{CacheReadPrice: testPtrFloat64(0)}, 89e-6},
		{"zero_5m_also_zeros_missing_interval_1h", PricingInterval{CacheWritePrice: testPtrFloat64(0)}, 38.5e-6},
		{"zero_5m_keeps_explicit_interval_1h", PricingInterval{CacheWritePrice: testPtrFloat64(0), CacheWrite1hPrice: testPtrFloat64(7e-6)}, 80.5e-6},
		{"zero_1h_does_not_fall_back_to_5m", PricingInterval{CacheWrite1hPrice: testPtrFloat64(0)}, 50.5e-6},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pricing := base.Clone()
			pricing.Intervals = []PricingInterval{tc.interval}
			before := pricing.Clone()
			cost := calculateTokenStatsCost(&pricing, tokens)
			require.NotNil(t, cost)
			require.InDelta(t, tc.want, *cost, 1e-12)
			require.Equal(t, before, pricing, "shared pricing card must not mutate")
		})
	}
}

func TestAccountStatsPricingIntervals_MultipliersMatchBilling(t *testing.T) {
	base := ChannelModelPricing{
		Platform: PlatformOpenAI, Models: []string{"gpt-6-astra"}, BillingMode: BillingModeToken,
		InputPrice: testPtrFloat64(1e-6), OutputPrice: testPtrFloat64(2e-6),
		CacheWritePrice: testPtrFloat64(3e-6), CacheWrite1hPrice: testPtrFloat64(7e-6),
		CacheReadPrice: testPtrFloat64(.5e-6),
	}
	tokens := UsageTokens{InputTokens: 3, OutputTokens: 5, CacheCreationTokens: 10,
		CacheCreation5mTokens: 4, CacheCreation1hTokens: 6, CacheReadTokens: 7}
	for _, tc := range []struct {
		name     string
		interval PricingInterval
		want     float64
	}{
		{"nil_fields", PricingInterval{InputPrice: testPtrFloat64(1e-6)}, 70.5e-6},
		{"all_multipliers", PricingInterval{InputMultiplier: testPtrFloat64(2), OutputMultiplier: testPtrFloat64(1.5), CacheWriteMultiplier: testPtrFloat64(2), CacheReadMultiplier: testPtrFloat64(2)}, 136e-6},
		{"explicit_zero_input_beats_multiplier", PricingInterval{InputPrice: testPtrFloat64(0), InputMultiplier: testPtrFloat64(2)}, 67.5e-6},
		{"explicit_output_beats_multiplier", PricingInterval{OutputPrice: testPtrFloat64(4e-6), OutputMultiplier: testPtrFloat64(3)}, 80.5e-6},
		{"explicit_zero_read_beats_multiplier", PricingInterval{CacheReadPrice: testPtrFloat64(0), CacheReadMultiplier: testPtrFloat64(2)}, 67e-6},
		{"explicit_write_beats_multiplier_and_sets_1h", PricingInterval{CacheWritePrice: testPtrFloat64(6e-6), CacheWriteMultiplier: testPtrFloat64(3)}, 76.5e-6},
		{"zero_write_sets_missing_1h_zero", PricingInterval{CacheWritePrice: testPtrFloat64(0), CacheWriteMultiplier: testPtrFloat64(2)}, 16.5e-6},
		{"write_multiplier_preserves_independent_1h", PricingInterval{CacheWriteMultiplier: testPtrFloat64(2)}, 124.5e-6},
		{"zero_write_multiplier", PricingInterval{CacheWriteMultiplier: testPtrFloat64(0)}, 16.5e-6},
		{"explicit_zero_1h_beats_write_multiplier", PricingInterval{CacheWriteMultiplier: testPtrFloat64(2), CacheWrite1hPrice: testPtrFloat64(0)}, 40.5e-6},
		{"zero_multiplier_keeps_explicit_1h", PricingInterval{CacheWriteMultiplier: testPtrFloat64(0), CacheWrite1hPrice: testPtrFloat64(7e-6)}, 58.5e-6},
		{"zero_input_multiplier", PricingInterval{InputMultiplier: testPtrFloat64(0)}, 67.5e-6},
		{"zero_output_multiplier", PricingInterval{OutputMultiplier: testPtrFloat64(0)}, 60.5e-6},
		{"zero_read_multiplier", PricingInterval{CacheReadMultiplier: testPtrFloat64(0)}, 67e-6},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pricing := base.Clone()
			pricing.Intervals = []PricingInterval{tc.interval}
			before := pricing.Clone()
			stats := calculateTokenStatsCost(&pricing, tokens)
			require.NotNil(t, stats)
			require.InDelta(t, tc.want, *stats, 1e-12)
			cs := &ChannelService{}
			cs.cache.Store(populateChannelCache([]Channel{{ID: 13, Status: StatusActive, GroupIDs: []int64{33}, ModelPricing: []ChannelModelPricing{pricing}}}, map[int64]string{33: PlatformOpenAI}))
			bs := newTestBillingServiceWithPrices(map[string]*ModelPricing{"gpt-6-astra": {}})
			group := &Group{ID: 33, Platform: PlatformOpenAI, LongContextPricingEnabled: true}
			cost, err := bs.CalculateCostUnified(CostInput{Ctx: context.Background(), Model: "gpt-6-astra",
				GroupID: &group.ID, Group: group, Tokens: tokens, RateMultiplier: 1, Resolver: NewModelPricingResolver(cs, bs)})
			require.NoError(t, err)
			require.InDelta(t, tc.want, cost.TotalCost, 1e-12)
			require.Equal(t, before, pricing)
		})
	}
}

func TestAccountStatsPricingIntervals_NormalizeCacheCreation(t *testing.T) {
	for _, tc := range []struct {
		name      string
		tokens    UsageTokens
		hourPrice *float64
		want      float64
	}{
		{"unsplit_aggregate_uses_5m", UsageTokens{CacheCreationTokens: 100}, testPtrFloat64(7e-6), 300e-6},
		{"mixed_ttl_not_double_counted", UsageTokens{CacheCreationTokens: 100, CacheCreation5mTokens: 40, CacheCreation1hTokens: 60}, testPtrFloat64(7e-6), 540e-6},
		{"contradictory_details_capped", UsageTokens{CacheCreationTokens: 100, CacheCreation5mTokens: 80, CacheCreation1hTokens: 120}, testPtrFloat64(7e-6), 540e-6},
		{"details_without_aggregate", UsageTokens{CacheCreation5mTokens: 40, CacheCreation1hTokens: 60}, testPtrFloat64(7e-6), 540e-6},
		{"nil_1h_falls_back_to_5m", UsageTokens{CacheCreationTokens: 100, CacheCreation1hTokens: 100}, nil, 300e-6},
		{"zero_1h_is_free", UsageTokens{CacheCreationTokens: 100, CacheCreation1hTokens: 100}, testPtrFloat64(0), 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pricing := &ChannelModelPricing{InputPrice: testPtrFloat64(1e-6), CacheWritePrice: testPtrFloat64(3e-6), CacheWrite1hPrice: tc.hourPrice}
			tokens := tc.tokens
			tokens.InputTokens = 1
			cost := calculateTokenStatsCost(pricing, tokens)
			require.NotNil(t, cost)
			require.InDelta(t, 1e-6+tc.want, *cost, 1e-12)
		})
	}
}

func TestAccountStatsPricingIntervals_AllZeroKeepsLegacyNil(t *testing.T) {
	pricing := &ChannelModelPricing{InputPrice: testPtrFloat64(1),
		Intervals: []PricingInterval{{InputPrice: testPtrFloat64(0)}}}
	require.Nil(t, calculateTokenStatsCost(pricing, UsageTokens{InputTokens: 1}))
}
