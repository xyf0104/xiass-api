//go:build unit

package service

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestGPT62BillingRequestTimeTiersAndCacheTTL(t *testing.T) {
	cutoff := time.Date(2027, 3, 1, 8, 0, 0, 0, time.UTC)
	body, err := os.ReadFile("../../resources/model-pricing/model_prices_and_context_window.json")
	require.NoError(t, err)
	for _, model := range []string{"gpt-6.2-sol", "gpt-6.2-astra"} {
		unit := 2e-6
		if model == "gpt-6.2-astra" {
			unit = 8e-6
		}
		for name, catalog := range map[string]*PricingService{
			"fallback":        nil,
			"empty_catalog":   {pricingData: map[string]*LiteLLMModelPricing{}},
			"bundled_catalog": newStubPricingServiceFromJSON(t, string(body)),
		} {
			t.Run(model+"/"+name, func(t *testing.T) {
				bs := NewBillingService(nil, catalog)
				for _, resolver := range []*ModelPricingResolver{nil, NewModelPricingResolver(nil, bs)} {
					input := CostInput{Ctx: context.Background(), Model: model, RateMultiplier: 0.35, Resolver: resolver,
						Tokens: UsageTokens{InputTokens: 100, OutputTokens: 50, CacheReadTokens: 20,
							CacheCreationTokens: 40, CacheCreation30mTokens: 10, CacheCreation12hTokens: 30}}
					for _, tc := range []struct {
						tier  string
						at    time.Time
						ratio float64
					}{
						{"", cutoff.Add(-time.Second), 1}, {"fast", cutoff, 2}, {"priority", cutoff, 2},
						{"flex", cutoff, 0.5}, {"ultrafast", cutoff.Add(-time.Nanosecond), 3},
						{"ultrafast", cutoff, 6}, {"ultrafast", cutoff.Add(time.Hour), 6},
						{"ultrafast", cutoff.Add(-time.Hour), 3},
					} {
						input.PricingAt, input.ServiceTier = tc.at, tc.tier
						cost, err := bs.CalculateCostUnified(input)
						require.NoError(t, err)
						require.InDelta(t, 100*unit*tc.ratio, cost.InputCost, 1e-12)
						require.InDelta(t, 50*unit*5*tc.ratio, cost.OutputCost, 1e-12)
						require.InDelta(t, (10*1.25+30*2.5)*unit*tc.ratio, cost.CacheCreationCost, 1e-12)
						require.InDelta(t, 20*unit*0.05*tc.ratio, cost.CacheReadCost, 1e-12)
						require.InDelta(t, cost.TotalCost*0.35, cost.ActualCost, 1e-12)
					}
				}
				require.Zero(t, bs.fallbackPrices[model].UltrafastMultiplier)
			})
		}
	}
}

func TestGPT62BillingLongContextBoundaryAndSubscriptions(t *testing.T) {
	bs := NewBillingService(nil, nil)
	for _, model := range []string{"gpt-6.2-sol", "gpt-6.2-astra"} {
		unit := 2e-6
		if model == "gpt-6.2-astra" {
			unit = 8e-6
		}
		for _, resolver := range []*ModelPricingResolver{nil, NewModelPricingResolver(nil, bs)} {
			input := CostInput{Ctx: context.Background(), Model: model, Resolver: resolver, RateMultiplier: 1,
				PricingAt: time.Date(2027, 3, 1, 8, 0, 0, 0, time.UTC), ServiceTier: "ultrafast",
				Tokens: UsageTokens{InputTokens: 271_950, CacheReadTokens: 10, CacheCreationTokens: 40,
					CacheCreation30mTokens: 10, CacheCreation12hTokens: 30, OutputTokens: 50}}
			for _, extra := range []int{0, 1} {
				input.Tokens.InputTokens = 271_950 + extra
				cost, err := bs.CalculateCostUnified(input)
				require.NoError(t, err)
				inRatio, outRatio := 1.0, 1.0
				if extra == 1 {
					inRatio, outRatio = 2, 1.5
				}
				require.InDelta(t, float64(input.Tokens.InputTokens)*unit*6*inRatio, cost.InputCost, 1e-12)
				require.InDelta(t, 50*unit*5*6*outRatio, cost.OutputCost, 1e-12)
				require.InDelta(t, 10*unit*0.05*6*inRatio, cost.CacheReadCost, 1e-12)
				require.InDelta(t, (10*1.25+30*2.5)*unit*6*inRatio, cost.CacheCreationCost, 1e-12)
				require.Equal(t, extra == 1, cost.LongContextBillingApplied)
			}
			for _, kind := range []string{AccountTypeAPIKey, AccountTypeOAuth, AccountTypeSetupToken} {
				input.DisableModelLongContextPricing = openAISubscriptionLongContextExempt(
					&Account{Platform: PlatformOpenAI, Type: kind}, &OpenAIForwardResult{Model: "public", UpstreamModel: model})
				cost, err := bs.CalculateCostUnified(input)
				require.NoError(t, err)
				factor := 2.0
				if kind != AccountTypeAPIKey {
					factor = 1
				}
				require.InDelta(t, 271_951*unit*6*factor, cost.InputCost, 1e-12)
			}
		}
	}
}

func TestGPT62BillingXIASSOverridesAndStaleResolvedPricing(t *testing.T) {
	bs := NewBillingService(nil, nil)
	const model = "gpt-6.2-sol"
	group := &Group{ID: 17, Platform: PlatformOpenAI, LongContextPricingEnabled: true}
	card := ChannelModelPricing{Platform: PlatformOpenAI, Models: []string{model}, BillingMode: BillingModeToken,
		InputPrice: testPtrFloat64(3e-6), CacheWritePrice: testPtrFloat64(0), CacheWrite12hPrice: testPtrFloat64(7e-6),
		Intervals: []PricingInterval{{MinTokens: 272_001, InputMultiplier: testPtrFloat64(4)}}}
	channel := &ChannelService{}
	channel.cache.Store(populateChannelCache([]Channel{{ID: 1, Status: StatusActive, GroupIDs: []int64{group.ID}, ModelPricing: []ChannelModelPricing{card}}}, map[int64]string{group.ID: PlatformOpenAI}))
	resolver := NewModelPricingResolver(channel, bs)
	cutoff := time.Date(2027, 3, 1, 8, 0, 0, 0, time.UTC)
	stale := resolver.Resolve(context.Background(), PricingInput{Model: model, GroupID: &group.ID, Group: group, PricingAt: cutoff.Add(-time.Second)})
	input := CostInput{Ctx: context.Background(), Model: model, GroupID: &group.ID, Group: group, Resolver: resolver, Resolved: stale,
		PricingAt: cutoff, ServiceTier: "ultrafast", RateMultiplier: 0.35,
		Tokens: UsageTokens{InputTokens: 300_000, OutputTokens: 50, CacheCreationTokens: 40, CacheCreation30mTokens: 10, CacheCreation12hTokens: 30}}
	cost, err := bs.CalculateCostUnified(input)
	require.NoError(t, err)
	require.InDelta(t, 300_000*3e-6*4*6, cost.InputCost, 1e-12, "explicit interval must not be doubled again")
	require.InDelta(t, 30*7e-6*6, cost.CacheCreationCost, 1e-12, "explicit free 30m and paid 12h writes survive")
	require.InDelta(t, cost.TotalCost*0.35, cost.ActualCost, 1e-12)
	require.Equal(t, 3.0, stale.BasePricing.UltrafastMultiplier)
	input.DisableModelLongContextPricing = true
	exempt, err := bs.CalculateCostUnified(input)
	require.NoError(t, err)
	require.Equal(t, cost.TotalCost, exempt.TotalCost, "subscription exemption cannot erase an explicit XIASS ladder")
	input.Resolved = nil
	group.ModelPricing = []ChannelModelPricing{{Models: []string{model}, PriceMode: PriceModeFinal, InputPrice: testPtrFloat64(9e-6)}}
	final, err := bs.CalculateCostUnified(input)
	require.NoError(t, err)
	require.Equal(t, cost.TotalCost, final.TotalCost)
	require.InDelta(t, (cost.TotalCost-cost.InputCost)*0.35+300_000*9e-6, final.ActualCost, 1e-12)
	group.ModelPricing = []ChannelModelPricing{{Models: []string{model}, InputPrice: testPtrFloat64(5e-6), FastMultiplier: testPtrFloat64(4)}}
	base, err := bs.CalculateCostUnified(input)
	require.NoError(t, err)
	require.InDelta(t, 300_000*5e-6*6, base.InputCost, 1e-12, "Fast settings cannot undercharge the independent Ultrafast tier")
	input.ServiceTier = "fast"
	fast, err := bs.CalculateCostUnified(input)
	require.NoError(t, err)
	require.InDelta(t, 300_000*5e-6*4, fast.InputCost, 1e-12, "explicit group Fast multiplier still wins for Fast")
}

func TestGPT62BillingExplicitUltrafastMultiplier(t *testing.T) {
	bs := NewBillingService(nil, nil)
	for _, multiplier := range []float64{0, 1, 4.5} {
		card := ChannelModelPricing{Platform: PlatformOpenAI, Models: []string{"gpt-6.2-sol"}, InputPrice: testPtrFloat64(7e-6),
			FastMultiplier: testPtrFloat64(2), UltrafastMultiplier: testPtrFloat64(multiplier)}
		legacyPricing, err := bs.GetModelPricingWithChannel("gpt-6.2-sol", &card)
		require.NoError(t, err)
		require.Equal(t, multiplier, configuredServiceTierMultiplier("ultrafast", legacyPricing))
		group := &Group{ID: 17, Platform: PlatformOpenAI, ModelPricing: []ChannelModelPricing{card}}
		channel := &ChannelService{}
		channel.cache.Store(populateChannelCache([]Channel{{ID: 1, Status: StatusActive,
			GroupIDs: []int64{group.ID}, ModelPricing: []ChannelModelPricing{card}}}, map[int64]string{group.ID: PlatformOpenAI}))
		p := &ModelPricing{InputPricePerToken: 7e-6, FastMultiplier: testPtrFloat64(2),
			UltrafastMultiplierOverride: testPtrFloat64(multiplier)}
		for _, at := range []time.Time{time.Date(2026, 11, 18, 0, 0, 0, 0, time.UTC), time.Date(2027, 3, 2, 0, 0, 0, 0, time.UTC)} {
			priced := bs.applyModelSpecificPricingPolicyEx("gpt-6.2-sol", p, false, at)
			require.Equal(t, multiplier, configuredServiceTierMultiplier("ultrafast", priced))
			cost := bs.computeTokenBreakdown(priced, UsageTokens{InputTokens: 100}, 0.35, "ultrafast", false)
			require.InDelta(t, 100*7e-6*multiplier*0.35, cost.ActualCost, 1e-12)
			for _, configuredGroup := range []*Group{nil, group} {
				unified, err := bs.CalculateCostUnified(CostInput{Ctx: context.Background(), Model: "gpt-6.2-sol",
					Group: configuredGroup, GroupID: &group.ID, Resolver: NewModelPricingResolver(channel, bs),
					PricingAt: at, ServiceTier: "ultrafast", RateMultiplier: 0.35, Tokens: UsageTokens{InputTokens: 100}})
				require.NoError(t, err)
				require.InDelta(t, cost.ActualCost, unified.ActualCost, 1e-12)
			}
		}
	}
}

func TestGPT62PricingAliasesAndExplicitFreeCache(t *testing.T) {
	for _, model := range []string{"gpt-6.2-sol", "gpt-6.2-astra"} {
		catalog := newStubPricingServiceFromJSON(t, fmt.Sprintf(`{%q:{"input_cost_per_token":0.000003,"output_cost_per_token":0.000015,"cache_creation_input_token_cost":0}}`, model))
		bs := NewBillingService(nil, catalog)
		for _, alias := range []string{model, "openai/" + model + "-max", model + "-high"} {
			require.True(t, bs.HasIdentifiedTokenPricing(alias))
			require.NotNil(t, catalog.GetIdentifiedModelPricing(alias))
			cost, err := bs.CalculateCostUnified(CostInput{Model: alias, ServiceTier: "priority", RateMultiplier: 1,
				Tokens: UsageTokens{InputTokens: 10, CacheCreationTokens: 10}})
			require.NoError(t, err)
			require.InDelta(t, 10*3e-6*2, cost.InputCost, 1e-12)
			require.Zero(t, cost.CacheCreationCost)
		}
	}
}

func TestGPT62RecordUsagePricingInstantAndFreeFast(t *testing.T) {
	cutoff := time.Date(2027, 3, 1, 8, 0, 0, 0, time.UTC)
	for _, withResolver := range []bool{false, true} {
		for _, freeFast := range []bool{false, true} {
			for _, at := range []time.Time{cutoff.Add(-time.Second), cutoff} {
				repo := &openAIRecordUsageLogRepoStub{inserted: true}
				svc := newOpenAIRecordUsageServiceForTest(repo, &openAIRecordUsageUserRepoStub{}, &openAIRecordUsageSubRepoStub{}, nil)
				if withResolver {
					svc.resolver = NewModelPricingResolver(nil, svc.billingService)
				}
				tier := "ultrafast"
				group := &Group{ID: 17, Platform: PlatformOpenAI, RateMultiplier: 0.35, FreeOpenAIFast: freeFast}
				err := svc.RecordUsage(context.Background(), &OpenAIRecordUsageInput{
					Result: &OpenAIForwardResult{RequestID: fmt.Sprint(at.Unix(), withResolver, freeFast), Model: "public", UpstreamModel: "gpt-6.2-sol", ServiceTier: &tier, Usage: OpenAIUsage{InputTokens: 1000, OutputTokens: 500}},
					APIKey: &APIKey{ID: 1007, GroupID: &group.ID, Group: group}, User: &User{ID: 2007},
					Account: &Account{ID: 3007, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}, PricingAt: at,
				})
				require.NoError(t, err)
				require.NotNil(t, repo.lastLog)
				want := (1000*2e-6 + 500*10e-6) * openAIUltrafastMultiplier("gpt-6.2-sol", at)
				require.InDelta(t, want, repo.lastLog.TotalCost, 1e-12)
				if freeFast {
					want = 1000*2e-6 + 500*10e-6
				}
				require.InDelta(t, want*0.35, repo.lastLog.ActualCost, 1e-12)
				require.Equal(t, "gpt-6.2-sol", *repo.lastLog.UpstreamModel)
				require.Equal(t, "public", repo.lastLog.Model)
				require.WithinDuration(t, time.Now(), repo.lastLog.CreatedAt, time.Minute)
			}
		}
	}
}

func TestGPT62BillingLeavesLegacyUltrafastUnchanged(t *testing.T) {
	bs := NewBillingService(nil, nil)
	for _, model := range []string{"gpt-6-astra", "gpt-6.1-sol", "gpt-5.6-sol"} {
		for _, at := range []time.Time{time.Date(2026, 11, 18, 0, 0, 0, 0, time.UTC), time.Date(2027, 3, 2, 0, 0, 0, 0, time.UTC)} {
			p, err := bs.getModelPricingAt(model, at)
			require.NoError(t, err)
			require.Zero(t, p.UltrafastMultiplier)
			require.Equal(t, 2.0, configuredServiceTierMultiplier("ultrafast", p))
		}
	}
}

func TestGPT62BillingSubscriptionExemptionScope(t *testing.T) {
	oauth := &Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth}
	for _, tc := range []struct {
		name    string
		account *Account
		result  *OpenAIForwardResult
		want    bool
	}{
		{"nil account", nil, &OpenAIForwardResult{Model: "gpt-6.2-sol"}, false},
		{"nil result", oauth, nil, false},
		{"api key", &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey}, &OpenAIForwardResult{Model: "gpt-6.2-sol"}, false},
		{"other platform", &Account{Platform: PlatformGemini, Type: AccountTypeOAuth}, &OpenAIForwardResult{Model: "gpt-6.2-sol"}, false},
		{"setup token", &Account{Platform: PlatformOpenAI, Type: AccountTypeSetupToken}, &OpenAIForwardResult{Model: "gpt-6.2-astra"}, true},
		{"sent model wins", oauth, &OpenAIForwardResult{Model: "public", UpstreamModel: "gpt-6.2-sol-max"}, true},
		{"requested alias cannot grant exemption", oauth, &OpenAIForwardResult{Model: "gpt-6.2-sol", UpstreamModel: "gpt-6-astra"}, false},
		{"blank sent model falls back", oauth, &OpenAIForwardResult{Model: "gpt-6.2-astra", UpstreamModel: " "}, true},
		{"unknown model", oauth, &OpenAIForwardResult{Model: "gpt-6.2-unknown"}, false},
		{"legacy sol unchanged", oauth, &OpenAIForwardResult{Model: "gpt-6.1-sol"}, false},
		{"legacy terra unchanged", oauth, &OpenAIForwardResult{Model: "gpt-6.1-terra"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, openAISubscriptionLongContextExempt(tc.account, tc.result))
		})
	}
}

func TestGPT62RecordUsageSubscriptionLongContextAndXIASSIntervals(t *testing.T) {
	for _, model := range []string{"gpt-6.2-sol", "gpt-6.2-astra"} {
		unit := 2e-6
		if model == "gpt-6.2-astra" {
			unit = 8e-6
		}
		for _, kind := range []string{AccountTypeAPIKey, AccountTypeOAuth, AccountTypeSetupToken} {
			for _, explicitInterval := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/interval=%t", model, kind, explicitInterval), func(t *testing.T) {
					repo := &openAIRecordUsageLogRepoStub{inserted: true}
					svc := newOpenAIRecordUsageServiceForTest(repo, &openAIRecordUsageUserRepoStub{}, &openAIRecordUsageSubRepoStub{}, nil)
					group := &Group{ID: 17, Platform: PlatformOpenAI, RateMultiplier: 0.35, LongContextPricingEnabled: true}
					var channel *ChannelService
					if explicitInterval {
						channel = &ChannelService{}
						card := ChannelModelPricing{Platform: PlatformOpenAI, Models: []string{model}, BillingMode: BillingModeToken,
							InputPrice: testPtrFloat64(3e-6), Intervals: []PricingInterval{{MinTokens: 272_001, InputMultiplier: testPtrFloat64(4)}}}
						channel.cache.Store(populateChannelCache([]Channel{{ID: 1, Status: StatusActive, GroupIDs: []int64{group.ID}, ModelPricing: []ChannelModelPricing{card}}}, map[int64]string{group.ID: PlatformOpenAI}))
					}
					svc.resolver = NewModelPricingResolver(channel, svc.billingService)
					err := svc.RecordUsage(context.Background(), &OpenAIRecordUsageInput{
						Result: &OpenAIForwardResult{RequestID: t.Name(), Model: "public", UpstreamModel: model,
							Usage: OpenAIUsage{InputTokens: 300_000, OutputTokens: 100}},
						APIKey: &APIKey{ID: 1007, GroupID: &group.ID, Group: group}, User: &User{ID: 2007},
						Account:   &Account{ID: 3007, Platform: PlatformOpenAI, Type: kind},
						PricingAt: time.Date(2027, 3, 1, 8, 0, 0, 0, time.UTC),
					})
					require.NoError(t, err)
					inputPrice, outputPrice := unit, unit*5
					if explicitInterval {
						inputPrice = 3e-6 * 4
					} else if kind == AccountTypeAPIKey {
						inputPrice *= 2
						outputPrice *= 1.5
					}
					require.NotNil(t, repo.lastLog)
					require.InDelta(t, 300_000*inputPrice, repo.lastLog.InputCost, 1e-12)
					require.InDelta(t, 100*outputPrice, repo.lastLog.OutputCost, 1e-12)
					require.InDelta(t, (300_000*inputPrice+100*outputPrice)*0.35, repo.lastLog.ActualCost, 1e-12)
					require.Equal(t, model, *repo.lastLog.UpstreamModel)
				})
			}
		}
	}
}
