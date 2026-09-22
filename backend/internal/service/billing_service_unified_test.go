//go:build unit

package service

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// CalculateCostUnified
// ---------------------------------------------------------------------------

func TestCalculateCostUnified_NilResolver_FallsBackToOldPath(t *testing.T) {
	svc := newTestBillingService()

	tokens := UsageTokens{InputTokens: 1000, OutputTokens: 500}
	input := CostInput{
		Model:          "claude-sonnet-4",
		Tokens:         tokens,
		RateMultiplier: 1.0,
		Resolver:       nil, // no resolver
	}
	cost, err := svc.CalculateCostUnified(input)
	require.NoError(t, err)

	// Should match the old-path result exactly
	expected, err := svc.calculateCostInternal("claude-sonnet-4", tokens, 1.0, "", nil)
	require.NoError(t, err)
	require.InDelta(t, expected.TotalCost, cost.TotalCost, 1e-10)
	require.InDelta(t, expected.ActualCost, cost.ActualCost, 1e-10)
	// BillingMode is NOT set by old path through CalculateCostUnified (resolver == nil)
	require.Empty(t, cost.BillingMode)
}

func TestCalculateCostUnified_FinalGroupPricingUsesRMBDirectlyAndKeepsAccountCost(t *testing.T) {
	bs := newTestBillingService()
	resolver := NewModelPricingResolver(nil, bs)
	inputPrice := 2e-6
	outputPrice := 0.0
	group := &Group{ID: 991, Platform: PlatformOpenAI, RateMultiplier: 7, ModelPricing: []ChannelModelPricing{{
		Models: []string{"claude-sonnet-4"}, BillingMode: BillingModeToken, PriceMode: PriceModeFinal,
		InputPrice: &inputPrice, OutputPrice: &outputPrice,
	}}}
	cost, err := bs.CalculateCostUnified(CostInput{
		Ctx: context.Background(), Model: "claude-sonnet-4", Group: group,
		Tokens:         UsageTokens{InputTokens: 1_000_000, OutputTokens: 100_000},
		RateMultiplier: 7, Resolver: resolver,
	})
	require.NoError(t, err)
	base, err := bs.CalculateCost("claude-sonnet-4", UsageTokens{InputTokens: 1_000_000, OutputTokens: 100_000}, 1)
	require.NoError(t, err)
	// Account/upstream cost stays on the catalog price; user billing is direct
	// RMB/token and does not receive group or user multiplier again.
	require.InDelta(t, base.TotalCost, cost.TotalCost, 1e-9)
	require.InDelta(t, 2.0, cost.ActualCost, 1e-9)
}

func TestCalculateCostUnified_FinalGroupPricingNilInheritsZeroIsFreeAndKeepsImageCacheLegacy(t *testing.T) {
	bs := newTestBillingService()
	resolver := NewModelPricingResolver(nil, bs)
	inputPrice := 0.0
	group := &Group{ID: 992, Platform: PlatformOpenAI, RateMultiplier: 5, ModelPricing: []ChannelModelPricing{{
		Models: []string{"claude-sonnet-4"}, BillingMode: BillingModeToken, PriceMode: PriceModeFinal,
		InputPrice: &inputPrice,
	}}}
	cost, err := bs.CalculateCostUnified(CostInput{
		Ctx: context.Background(), Model: "claude-sonnet-4", Group: group,
		Tokens: UsageTokens{InputTokens: 100, ImageInputTokens: 40, CacheReadTokens: 100, ImageCacheReadTokens: 20,
			CacheCreationTokens: 100, CacheCreation5mTokens: 40, CacheCreation1hTokens: 60},
		RateMultiplier: 5, Resolver: resolver,
	})
	require.NoError(t, err)
	// Text input is explicitly free; nil output/cache fields inherit legacy
	// user billing, while image input/cache-read and both cache-write TTLs keep
	// their existing account/user path.
	require.NotEqual(t, 0.0, cost.TotalCost)
	require.Greater(t, cost.ActualCost, 0.0)
	require.NotEqual(t, cost.TotalCost*5, cost.ActualCost)
}

func TestCalculateCostUnified_FinalGroupPricingCoversAllTokenDimensions(t *testing.T) {
	groupID := int64(993)
	fastMultiplier := 2.0
	maxEffortMultiplier := 3.0
	channelPricing := &ChannelModelPricing{
		Platform:                     PlatformAnthropic,
		BillingMode:                  BillingModeToken,
		InputPrice:                   testPtrFloat64(10e-6),
		OutputPrice:                  testPtrFloat64(20e-6),
		CacheWritePrice:              testPtrFloat64(30e-6),
		CacheWrite1hPrice:            testPtrFloat64(60e-6),
		CacheReadPrice:               testPtrFloat64(4e-6),
		FastMultiplier:               &fastMultiplier,
		MaxReasoningEffortMultiplier: &maxEffortMultiplier,
	}
	cs := newTestChannelServiceWithCache(t, &channelCache{
		pricingByGroupModel: map[channelModelKey]*ChannelModelPricing{
			{groupID: groupID, platform: PlatformAnthropic, model: "claude-fable-5-1"}: channelPricing,
		},
		channelByGroupID:        map[int64]*Channel{groupID: {ID: groupID, Status: StatusActive}},
		groupPlatform:           map[int64]string{groupID: PlatformAnthropic},
		wildcardByGroupPlatform: map[channelGroupPlatformKey][]*wildcardPricingEntry{},
		mappingByGroupModel:     map[channelModelKey]string{},
		wildcardMappingByGP:     map[channelGroupPlatformKey][]*wildcardMappingEntry{},
		byID:                    map[int64]*Channel{},
	})
	bs := newTestBillingService()
	resolver := NewModelPricingResolver(cs, bs)
	tokens := UsageTokens{
		InputTokens: 100, OutputTokens: 200, CacheReadTokens: 300,
		CacheCreationTokens: 900, CacheCreation5mTokens: 400, CacheCreation1hTokens: 500,
	}
	finalInput := 1e-6
	finalOutput := 2e-6
	finalRead := 4e-6
	finalWrite := 3e-6
	finalWrite1h := 6e-6

	t.Run("explicit 1h price ignores user tier and effort multipliers", func(t *testing.T) {
		group := &Group{ID: groupID, Platform: PlatformAnthropic, ModelPricing: []ChannelModelPricing{{
			Models: []string{"claude-fable-5-1"}, BillingMode: BillingModeToken, PriceMode: PriceModeFinal,
			InputPrice: &finalInput, OutputPrice: &finalOutput, CacheReadPrice: &finalRead,
			CacheWritePrice: &finalWrite, CacheWrite1hPrice: &finalWrite1h,
		}}}
		cost, err := bs.CalculateCostUnified(CostInput{
			Ctx: context.Background(), Model: "claude-fable-5-1", GroupID: &groupID, Group: group,
			Tokens: tokens, RateMultiplier: 7, ServiceTier: "priority", ReasoningEffort: "max", Resolver: resolver,
		})
		require.NoError(t, err)

		rawAccountCost := 100*10e-6 + 200*20e-6 + 300*4e-6 + 400*30e-6 + 500*60e-6
		expectedFinalRMB := 100*finalInput + 200*finalOutput + 300*finalRead + 400*finalWrite + 500*finalWrite1h
		require.InDelta(t, rawAccountCost*fastMultiplier*maxEffortMultiplier, cost.TotalCost, 1e-12)
		require.InDelta(t, expectedFinalRMB, cost.ActualCost, 1e-12)
	})

	t.Run("missing final 1h price falls back to final cache write", func(t *testing.T) {
		group := &Group{ID: groupID, Platform: PlatformAnthropic, ModelPricing: []ChannelModelPricing{{
			Models: []string{"claude-fable-5-1"}, BillingMode: BillingModeToken, PriceMode: PriceModeFinal,
			InputPrice: &finalInput, OutputPrice: &finalOutput, CacheReadPrice: &finalRead,
			CacheWritePrice: &finalWrite,
		}}}
		cost, err := bs.CalculateCostUnified(CostInput{
			Ctx: context.Background(), Model: "claude-fable-5-1", GroupID: &groupID, Group: group,
			Tokens: tokens, RateMultiplier: 7, ServiceTier: "priority", ReasoningEffort: "max", Resolver: resolver,
		})
		require.NoError(t, err)

		expectedFinalRMB := 100*finalInput + 200*finalOutput + 300*finalRead + 900*finalWrite
		require.InDelta(t, expectedFinalRMB, cost.ActualCost, 1e-12)
	})

	t.Run("nil final output inherits channel policy", func(t *testing.T) {
		group := &Group{ID: groupID, Platform: PlatformAnthropic, ModelPricing: []ChannelModelPricing{{
			Models: []string{"claude-fable-5-1"}, BillingMode: BillingModeToken, PriceMode: PriceModeFinal,
			InputPrice: &finalInput, CacheReadPrice: &finalRead, CacheWritePrice: &finalWrite,
		}}}
		cost, err := bs.CalculateCostUnified(CostInput{
			Ctx: context.Background(), Model: "claude-fable-5-1", GroupID: &groupID, Group: group,
			Tokens: tokens, RateMultiplier: 7, ServiceTier: "priority", ReasoningEffort: "max", Resolver: resolver,
		})
		require.NoError(t, err)

		inheritedOutput := 200 * 20e-6 * fastMultiplier * maxEffortMultiplier * 7
		expectedFinalRMB := 100*finalInput + inheritedOutput + 300*finalRead + 900*finalWrite
		require.InDelta(t, expectedFinalRMB, cost.ActualCost, 1e-12)
	})
}

func TestCalculateCostUnified_Fable51MaxEffortUsesDefaultMultiplier(t *testing.T) {
	bs := newTestBillingService()
	tokens := UsageTokens{InputTokens: 1000, OutputTokens: 10}

	standard, err := bs.CalculateCostUnified(CostInput{
		Model: "claude-fable-5-1", Tokens: tokens, RateMultiplier: 1, ReasoningEffort: "xhigh",
	})
	require.NoError(t, err)
	maxCost, err := bs.CalculateCostUnified(CostInput{
		Model: "claude-fable-5-1", Tokens: tokens, RateMultiplier: 1, ReasoningEffort: "max",
	})
	require.NoError(t, err)

	require.InDelta(t, standard.TotalCost*3, maxCost.TotalCost, 1e-12)
	require.InDelta(t, standard.ActualCost*3, maxCost.ActualCost, 1e-12)
	require.InDelta(t, standard.InputCost*3, maxCost.InputCost, 1e-12)
	require.InDelta(t, standard.OutputCost*3, maxCost.OutputCost, 1e-12)
}

func TestCalculateCostUnified_ChannelOverridesFable51MaxEffortMultiplier(t *testing.T) {
	configured := 1.5
	groupID := int64(51)
	cs := newTestChannelServiceWithCache(t, &channelCache{
		pricingByGroupModel: map[channelModelKey]*ChannelModelPricing{
			{groupID: groupID, platform: PlatformAnthropic, model: "claude-fable-5-1"}: {
				Platform:                     PlatformAnthropic,
				BillingMode:                  BillingModeToken,
				InputPrice:                   testPtrFloat64(10e-6),
				OutputPrice:                  testPtrFloat64(50e-6),
				MaxReasoningEffortMultiplier: &configured,
			},
		},
		channelByGroupID: map[int64]*Channel{
			groupID: {ID: groupID, Status: StatusActive},
		},
		groupPlatform:           map[int64]string{groupID: PlatformAnthropic},
		wildcardByGroupPlatform: map[channelGroupPlatformKey][]*wildcardPricingEntry{},
		mappingByGroupModel:     map[channelModelKey]string{},
		wildcardMappingByGP:     map[channelGroupPlatformKey][]*wildcardMappingEntry{},
		byID:                    map[int64]*Channel{},
	})
	bs := newTestBillingService()
	resolver := NewModelPricingResolver(cs, bs)
	group := &Group{ID: groupID, Platform: PlatformAnthropic}

	cost, err := bs.CalculateCostUnified(CostInput{
		Ctx: context.Background(), Model: "claude-fable-5-1", GroupID: &groupID, Group: group,
		Tokens: UsageTokens{InputTokens: 1000}, RateMultiplier: 1, ReasoningEffort: "max", Resolver: resolver,
	})
	require.NoError(t, err)
	require.InDelta(t, 1000*10e-6*configured, cost.TotalCost, 1e-12)
	require.InDelta(t, cost.TotalCost, cost.ActualCost, 1e-12)
}

func TestCalculateCostUnified_TokenMode(t *testing.T) {
	bs := newTestBillingService()
	resolver := NewModelPricingResolver(nil, bs)

	tokens := UsageTokens{InputTokens: 1000, OutputTokens: 500}
	input := CostInput{
		Ctx:            context.Background(),
		Model:          "claude-sonnet-4",
		Tokens:         tokens,
		RateMultiplier: 1.5,
		Resolver:       resolver,
	}
	cost, err := bs.CalculateCostUnified(input)
	require.NoError(t, err)
	require.NotNil(t, cost)

	// Verify token billing: Input: 1000*3e-6=0.003, Output: 500*15e-6=0.0075
	expectedTotal := 1000*3e-6 + 500*15e-6
	require.InDelta(t, expectedTotal, cost.TotalCost, 1e-10)
	require.InDelta(t, expectedTotal*1.5, cost.ActualCost, 1e-10)
	require.Equal(t, string(BillingModeToken), cost.BillingMode)
}

func TestCalculateCostUnified_TokenModeAppliesRateMultiplierToImageTokens(t *testing.T) {
	bs := newTestBillingService()
	resolver := NewModelPricingResolver(nil, bs)

	tokens := UsageTokens{InputTokens: 1000, OutputTokens: 600, ImageOutputTokens: 100}
	cost, err := bs.CalculateCostUnified(CostInput{
		Ctx:            context.Background(),
		Model:          "claude-sonnet-4",
		Tokens:         tokens,
		RateMultiplier: 3.0,
		Resolver:       resolver,
	})
	require.NoError(t, err)

	textInput := 1000 * 3e-6
	textOutput := 500 * 15e-6
	imageOutput := 100 * 15e-6
	require.InDelta(t, textInput+textOutput+imageOutput, cost.TotalCost, 1e-10)
	require.InDelta(t, (textInput+textOutput+imageOutput)*3.0, cost.ActualCost, 1e-10)
	require.InDelta(t, imageOutput, cost.ImageOutputCost, 1e-10)
}

func TestCalculateCostUnified_PerRequestMode(t *testing.T) {
	// Set up a ChannelService with a per-request pricing channel
	cs := newTestChannelServiceWithCache(t, &channelCache{
		pricingByGroupModel: map[channelModelKey]*ChannelModelPricing{
			{groupID: 1, model: "claude-sonnet-4"}: {
				BillingMode:     BillingModePerRequest,
				PerRequestPrice: testPtrFloat64(0.05),
			},
		},
		channelByGroupID: map[int64]*Channel{
			1: {ID: 1, Status: StatusActive},
		},
		groupPlatform:           map[int64]string{1: ""},
		wildcardByGroupPlatform: map[channelGroupPlatformKey][]*wildcardPricingEntry{},
		mappingByGroupModel:     map[channelModelKey]string{},
		wildcardMappingByGP:     map[channelGroupPlatformKey][]*wildcardMappingEntry{},
		byID:                    map[int64]*Channel{},
	})

	bs := newTestBillingService()
	resolver := NewModelPricingResolver(cs, bs)
	groupID := int64(1)

	input := CostInput{
		Ctx:            context.Background(),
		Model:          "claude-sonnet-4",
		GroupID:        &groupID,
		Tokens:         UsageTokens{InputTokens: 100, OutputTokens: 50},
		RequestCount:   3,
		RateMultiplier: 2.0,
		Resolver:       resolver,
	}
	cost, err := bs.CalculateCostUnified(input)
	require.NoError(t, err)
	require.NotNil(t, cost)

	// 3 requests * $0.05 = $0.15
	require.InDelta(t, 0.15, cost.TotalCost, 1e-10)
	// ActualCost = 0.15 * 2.0 = 0.30
	require.InDelta(t, 0.30, cost.ActualCost, 1e-10)
	require.Equal(t, string(BillingModePerRequest), cost.BillingMode)
}

func TestCalculateCostUnified_ImageMode(t *testing.T) {
	cs := newTestChannelServiceWithCache(t, &channelCache{
		pricingByGroupModel: map[channelModelKey]*ChannelModelPricing{
			{groupID: 2, model: "gemini-image"}: {
				BillingMode:     BillingModeImage,
				PerRequestPrice: testPtrFloat64(0.10),
			},
		},
		channelByGroupID: map[int64]*Channel{
			2: {ID: 2, Status: StatusActive},
		},
		groupPlatform:           map[int64]string{2: ""},
		wildcardByGroupPlatform: map[channelGroupPlatformKey][]*wildcardPricingEntry{},
		mappingByGroupModel:     map[channelModelKey]string{},
		wildcardMappingByGP:     map[channelGroupPlatformKey][]*wildcardMappingEntry{},
		byID:                    map[int64]*Channel{},
	})

	bs := &BillingService{
		cfg:            &config.Config{},
		fallbackPrices: map[string]*ModelPricing{},
	}
	resolver := NewModelPricingResolver(cs, bs)
	groupID := int64(2)

	input := CostInput{
		Ctx:            context.Background(),
		Model:          "gemini-image",
		GroupID:        &groupID,
		Tokens:         UsageTokens{},
		RequestCount:   2,
		RateMultiplier: 1.0,
		Resolver:       resolver,
	}
	cost, err := bs.CalculateCostUnified(input)
	require.NoError(t, err)
	require.NotNil(t, cost)

	// 2 * $0.10 = $0.20
	require.InDelta(t, 0.20, cost.TotalCost, 1e-10)
	require.InDelta(t, 0.20, cost.ActualCost, 1e-10)
	require.Equal(t, string(BillingModeImage), cost.BillingMode)
}

// TestCalculateCostUnified_RateMultiplierZeroProducesZero 锁定新行为：
// 保存时强制 > 0；若 0 仍泄漏到计费层，按 0 计费（而非历史上的 1.0）。
func TestCalculateCostUnified_RateMultiplierZeroProducesZero(t *testing.T) {
	bs := newTestBillingService()
	resolver := NewModelPricingResolver(nil, bs)

	tokens := UsageTokens{InputTokens: 1000, OutputTokens: 500}

	cost, err := bs.CalculateCostUnified(CostInput{
		Ctx:            context.Background(),
		Model:          "claude-sonnet-4",
		Tokens:         tokens,
		RateMultiplier: 0,
		Resolver:       resolver,
	})
	require.NoError(t, err)
	require.Greater(t, cost.TotalCost, 0.0)
	require.InDelta(t, 0.0, cost.ActualCost, 1e-10)
}

// TestCalculateCostUnified_NegativeRateMultiplierClampedToZero 锁定新行为：
// 负数倍率按 0 计费，避免历史的 <=0 → 1.0 把配置异常静默按标准价扣费。
func TestCalculateCostUnified_NegativeRateMultiplierClampedToZero(t *testing.T) {
	bs := newTestBillingService()
	resolver := NewModelPricingResolver(nil, bs)

	tokens := UsageTokens{InputTokens: 1000}

	cost, err := bs.CalculateCostUnified(CostInput{
		Ctx:            context.Background(),
		Model:          "claude-sonnet-4",
		Tokens:         tokens,
		RateMultiplier: -5.0,
		Resolver:       resolver,
	})
	require.NoError(t, err)
	require.Greater(t, cost.TotalCost, 0.0)
	require.InDelta(t, 0.0, cost.ActualCost, 1e-10)
}

func TestCalculateCostUnified_BillingModeFieldFilled(t *testing.T) {
	bs := newTestBillingService()
	resolver := NewModelPricingResolver(nil, bs)

	cost, err := bs.CalculateCostUnified(CostInput{
		Ctx:            context.Background(),
		Model:          "claude-sonnet-4",
		Tokens:         UsageTokens{InputTokens: 100},
		RateMultiplier: 1.0,
		Resolver:       resolver,
	})
	require.NoError(t, err)
	require.Equal(t, "token", cost.BillingMode)
}

func TestCalculateCostUnified_UsesPreResolvedPricing(t *testing.T) {
	bs := newTestBillingService()
	resolver := NewModelPricingResolver(nil, bs)

	// Pre-resolve with per_request mode to verify it's used instead of re-resolving
	preResolved := &ResolvedPricing{
		Mode:                   BillingModePerRequest,
		DefaultPerRequestPrice: 0.07,
	}

	cost, err := bs.CalculateCostUnified(CostInput{
		Ctx:            context.Background(),
		Model:          "claude-sonnet-4",
		Tokens:         UsageTokens{InputTokens: 100},
		RequestCount:   2,
		RateMultiplier: 1.0,
		Resolver:       resolver,
		Resolved:       preResolved,
	})
	require.NoError(t, err)
	require.NotNil(t, cost)

	// 2 * $0.07 = $0.14
	require.InDelta(t, 0.14, cost.TotalCost, 1e-10)
	require.Equal(t, string(BillingModePerRequest), cost.BillingMode)
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// newTestChannelServiceWithCache creates a ChannelService with a pre-populated
// cache snapshot, bypassing the repository layer entirely.
func newTestChannelServiceWithCache(t *testing.T, cache *channelCache) *ChannelService {
	t.Helper()
	cs := &ChannelService{}
	cache.loadedAt = time.Now()
	cs.cache.Store(cache)
	return cs
}
