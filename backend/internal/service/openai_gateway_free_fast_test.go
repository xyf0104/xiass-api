//go:build unit

package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestOpenAIRecordUsage_FreeFastOnlyChangesActualCost(t *testing.T) {
	for _, tc := range []struct {
		name, tier, observed, groupPlatform, accountPlatform, accountType string
		wantStandard                                                      bool
	}{
		{"priority", "priority", "priority", PlatformOpenAI, PlatformOpenAI, AccountTypeAPIKey, true},
		{"fast_alias", "fast", "", PlatformOpenAI, PlatformOpenAI, AccountTypeAPIKey, true},
		{"composite", "priority", "", PlatformComposite, PlatformOpenAI, AccountTypeAPIKey, true},
		{"observed_ultrafast", "priority", "ultrafast", PlatformOpenAI, PlatformOpenAI, AccountTypeAPIKey, true},
		{"oauth_default", "priority", "default", PlatformOpenAI, PlatformOpenAI, AccountTypeOAuth, true},
		{"apikey_default_downgrade", "priority", "default", PlatformOpenAI, PlatformOpenAI, AccountTypeAPIKey, true},
		{"flex", "flex", "flex", PlatformOpenAI, PlatformOpenAI, AccountTypeAPIKey, false},
		{"standard", "default", "ultrafast", PlatformOpenAI, PlatformOpenAI, AccountTypeAPIKey, false},
		{"explicit_ultrafast", OpenAIFastTierUltrafast, "ultrafast", PlatformOpenAI, PlatformOpenAI, AccountTypeAPIKey, true},
		{"composite_ultrafast", OpenAIFastTierUltrafast, "ultrafast", PlatformComposite, PlatformOpenAI, AccountTypeAPIKey, true},
		{"other_account_ultrafast", OpenAIFastTierUltrafast, "ultrafast", PlatformComposite, PlatformGemini, AccountTypeAPIKey, false},
		{"other_account_platform", "priority", "", PlatformComposite, PlatformGemini, AccountTypeAPIKey, false},
		{"other_group_platform", "priority", "", PlatformGemini, PlatformOpenAI, AccountTypeAPIKey, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, withResolver := range []bool{false, true} {
				for _, statsMode := range []string{"raw", "catalog", "custom"} {
					group := &Group{ID: 71, Platform: tc.groupPlatform, RateMultiplier: 0.4, LongContextPricingEnabled: true}
					channel := &Channel{Status: StatusActive, ApplyPricingToAccountStats: statsMode == "raw"}
					if statsMode == "custom" {
						channel.AccountStatsPricingRules = []AccountStatsPricingRule{{
							GroupIDs: []int64{group.ID},
							Pricing:  []ChannelModelPricing{{Models: []string{"gpt-5.5"}, BillingMode: BillingModeToken, InputPrice: pricingMultiplier(30e-6)}},
						}}
					}
					record := func(free bool, tier string) (*UsageLog, *UsageBillingCommand, *OpenAIForwardResult) {
						groupCopy := *group
						groupCopy.FreeOpenAIFast = free
						return recordFreeFastUsage(t, &groupCopy, channel, withResolver, tc.accountPlatform, tc.accountType, tier, tc.observed)
					}
					paid, paidCmd, paidResult := record(false, tc.tier)
					free, freeCmd, freeResult := record(true, tc.tier)
					wantActual := paid.ActualCost
					if tc.wantStandard {
						standard, _, _ := record(false, "")
						wantActual = min(wantActual, standard.ActualCost)
					}
					require.InDelta(t, wantActual, free.ActualCost, 1e-10)
					require.InDelta(t, QuantizeUsageBillingAmount(wantActual), freeCmd.BalanceCost, 1e-10)
					require.Equal(t, paidCmd.AccountQuotaCost, freeCmd.AccountQuotaCost)
					require.NotNil(t, free.AccountStatsCost)
					require.Greater(t, *free.AccountStatsCost, 0.0)
					require.Equal(t, paidResult, freeResult, "free Fast must not rewrite either request or observed upstream tier")
					// Every persisted field except user cost and timestamp must stay identical.
					free.ActualCost, free.CreatedAt = paid.ActualCost, paid.CreatedAt
					require.Equal(t, paid, free, "resolver=%v stats=%s", withResolver, statsMode)
				}
			}
		})
	}
}

func TestOpenAIRecordUsage_FreeFastPreservesExplicitPrices(t *testing.T) {
	for _, tc := range []struct {
		name    string
		pricing ChannelModelPricing
	}{
		{"no_premium", ChannelModelPricing{FastMultiplier: pricingMultiplier(1)}},
		{"discounted_fast", ChannelModelPricing{FastMultiplier: pricingMultiplier(0.5)}},
		{"free_fast", ChannelModelPricing{FastMultiplier: pricingMultiplier(0)}},
		{"partial_final", ChannelModelPricing{PriceMode: PriceModeFinal, InputPrice: pricingMultiplier(2e-6)}},
		{"zero_final", ChannelModelPricing{PriceMode: PriceModeFinal, InputPrice: pricingMultiplier(0), OutputPrice: pricingMultiplier(0), CacheReadPrice: pricingMultiplier(0), CacheWritePrice: pricingMultiplier(0)}},
	} {
		for _, tier := range []string{"priority", OpenAIFastTierUltrafast} {
			t.Run(tc.name+"/"+tier, func(t *testing.T) {
				pricing := tc.pricing
				pricing.Models, pricing.BillingMode = []string{"gpt-5.5"}, BillingModeToken
				group := &Group{ID: 72, Platform: PlatformOpenAI, RateMultiplier: 0.4, LongContextPricingEnabled: true, ModelPricing: []ChannelModelPricing{pricing}}
				channel := &Channel{Status: StatusActive, ApplyPricingToAccountStats: true}
				paid, _, _ := recordFreeFastUsage(t, group, channel, true, PlatformOpenAI, AccountTypeAPIKey, tier, "ultrafast")
				standard, _, _ := recordFreeFastUsage(t, group, channel, true, PlatformOpenAI, AccountTypeAPIKey, "", "ultrafast")
				group.FreeOpenAIFast = true
				free, cmd, _ := recordFreeFastUsage(t, group, channel, true, PlatformOpenAI, AccountTypeAPIKey, tier, "ultrafast")
				require.InDelta(t, min(paid.ActualCost, standard.ActualCost), free.ActualCost, 1e-10)
				require.InDelta(t, QuantizeUsageBillingAmount(free.ActualCost), cmd.BalanceCost, 1e-10)
				require.Equal(t, paid.TotalCost, free.TotalCost)
				require.Equal(t, paid.AccountStatsCost, free.AccountStatsCost)
				if tc.name == "zero_final" || tc.name == "free_fast" {
					require.InDelta(t, 0.0, free.ActualCost, 1e-10)
				}
			})
		}
	}
}

func recordFreeFastUsage(t *testing.T, group *Group, channel *Channel, withResolver bool, platform, accountType, tier, observed string) (*UsageLog, *UsageBillingCommand, *OpenAIForwardResult) {
	t.Helper()
	usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
	billingRepo := &openAIRecordUsageBillingRepoStub{}
	svc := newOpenAIRecordUsageServiceWithBillingRepoForTest(usageRepo, billingRepo, &openAIRecordUsageUserRepoStub{}, &openAIRecordUsageSubRepoStub{}, nil)
	svc.channelService = newTestChannelServiceForStats(t, channel, group.ID, group.Platform)
	if withResolver {
		svc.resolver = NewModelPricingResolver(nil, svc.billingService)
	}
	result := &OpenAIForwardResult{
		RequestID: "free_fast_usage", Model: "gpt-5.5", UpstreamModel: "gpt-5.5",
		ServiceTier: optionalTrimmedStringPtr(tier), UpstreamResponseServiceTier: observed,
		Usage:    OpenAIUsage{InputTokens: 300000, OutputTokens: 1000, CacheReadInputTokens: 10000, CacheCreationInputTokens: 5000},
		Duration: time.Second,
	}
	require.NoError(t, svc.RecordUsage(context.Background(), &OpenAIRecordUsageInput{
		Result:  result,
		APIKey:  &APIKey{ID: 2, GroupID: &group.ID, Group: group},
		User:    &User{ID: 1},
		Account: &Account{ID: 3, Platform: platform, Type: accountType, Extra: map[string]any{openAILongContextBillingEnabledKey: true}},
	}))
	require.NotNil(t, usageRepo.lastLog)
	require.NotNil(t, billingRepo.lastCmd)
	return usageRepo.lastLog, billingRepo.lastCmd, result
}
