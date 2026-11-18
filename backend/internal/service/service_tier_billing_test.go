package service

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestResolveBillingServiceTierOnlyLowersCost(t *testing.T) {
	tests := []struct {
		name       string
		requested  string
		observed   string
		billing    string
		downgraded bool
	}{
		{name: "ultrafast served as priority", requested: "ultrafast", observed: "priority", billing: "priority", downgraded: true},
		{name: "ultrafast served as fast", requested: "ultrafast", observed: "fast", billing: "fast", downgraded: true},
		{name: "ultrafast served as default", requested: "ultrafast", observed: "default", billing: "default", downgraded: true},
		{name: "ultrafast served as flex", requested: "ultrafast", observed: "flex", billing: "flex", downgraded: true},
		{name: "ultrafast normalization", requested: " UltraFast ", observed: " PRIORITY ", billing: "priority", downgraded: true},
		{name: "priority is never raised to ultrafast", requested: "priority", observed: "ultrafast", billing: "priority"},
		{name: "ultrafast unchanged", requested: "ultrafast", observed: "ultrafast", billing: "ultrafast"},
		{name: "ultrafast without declaration", requested: "ultrafast", billing: "ultrafast"},
		{name: "ultrafast unknown declaration", requested: "ultrafast", observed: "turbo", billing: "ultrafast"},
		{name: "priority served as default", requested: "priority", observed: "default", billing: "default", downgraded: true},
		{name: "fast served as standard", requested: "fast", observed: "standard", billing: "standard", downgraded: true},
		{name: "flex is never raised", requested: "flex", observed: "default", billing: "flex"},
		{name: "untiered request stays untiered", observed: "priority", billing: ""},
		{name: "unknown response is ignored", requested: "priority", observed: "turbo", billing: "priority"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := ResolveBillingServiceTier(test.requested, test.observed)
			require.Equal(t, test.billing, got.Billing)
			require.Equal(t, test.downgraded, got.Downgraded)
		})
	}
}

func TestOpenAIServiceTierBillingHonorsCredentialProtocol(t *testing.T) {
	priority := "priority"

	apiKeyResult := &OpenAIForwardResult{
		ServiceTier:                 &priority,
		UpstreamResponseServiceTier: "default",
	}
	resolution := ApplyOpenAIServiceTierBillingResolution(
		&Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey},
		apiKeyResult,
	)
	require.True(t, resolution.Downgraded)
	require.Equal(t, "default", *apiKeyResult.ServiceTier)

	oauthResult := &OpenAIForwardResult{
		ServiceTier:                 &priority,
		UpstreamResponseServiceTier: "default",
	}
	resolution = ApplyOpenAIServiceTierBillingResolution(
		&Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth},
		oauthResult,
	)
	require.False(t, resolution.Downgraded)
	require.Equal(t, "priority", *oauthResult.ServiceTier)

	oauthFlexResult := &OpenAIForwardResult{
		ServiceTier:                 &priority,
		UpstreamResponseServiceTier: "flex",
	}
	resolution = ApplyOpenAIServiceTierBillingResolution(
		&Account{Platform: PlatformOpenAI, Type: AccountTypeSetupToken},
		oauthFlexResult,
	)
	require.True(t, resolution.Downgraded)
	require.Equal(t, "flex", *oauthFlexResult.ServiceTier)
}

func TestForwardServiceTierBillingUsesObservedTier(t *testing.T) {
	priority := "priority"
	result := &ForwardResult{
		ServiceTier:                 &priority,
		UpstreamResponseServiceTier: "standard",
	}
	require.True(t, ApplyForwardServiceTierBillingResolution(result).Downgraded)
	require.Equal(t, "standard", *result.ServiceTier)

	ultrafast := "ultrafast"
	result = &ForwardResult{ServiceTier: &ultrafast, UpstreamResponseServiceTier: "priority"}
	require.True(t, ApplyForwardServiceTierBillingResolution(result).Downgraded)
	require.Equal(t, "priority", *result.ServiceTier)
}

func TestUltrafastServiceTierBillingRecordUsage(t *testing.T) {
	cutoff := time.Date(2027, 3, 1, 8, 0, 0, 0, time.UTC)
	for _, model := range []string{"gpt-6.2-sol", "gpt-6.2-astra"} {
		unit := 2e-6
		if model == "gpt-6.2-astra" {
			unit = 8e-6
		}
		for _, kind := range []string{AccountTypeAPIKey, AccountTypeOAuth, AccountTypeSetupToken} {
			for _, observed := range []string{"priority", "default", "flex"} {
				for _, withResolver := range []bool{false, true} {
					for _, at := range []time.Time{cutoff.Add(-time.Second), cutoff} {
						t.Run(fmt.Sprintf("%s/%s/%s/resolver=%t/%d", model, kind, observed, withResolver, at.Unix()), func(t *testing.T) {
							repo := &openAIRecordUsageLogRepoStub{inserted: true}
							svc := newOpenAIRecordUsageServiceForTest(repo, &openAIRecordUsageUserRepoStub{}, &openAIRecordUsageSubRepoStub{}, nil)
							if withResolver {
								svc.resolver = NewModelPricingResolver(nil, svc.billingService)
							}
							tier := "ultrafast"
							group := &Group{ID: 17, Platform: PlatformOpenAI, RateMultiplier: 0.35}
							err := svc.RecordUsage(context.Background(), &OpenAIRecordUsageInput{
								Result: &OpenAIForwardResult{RequestID: t.Name(), Model: "public", UpstreamModel: model,
									ServiceTier: &tier, UpstreamResponseServiceTier: observed,
									Usage: OpenAIUsage{InputTokens: 1100, OutputTokens: 50, CacheReadInputTokens: 100}},
								APIKey: &APIKey{ID: 1007, GroupID: &group.ID, Group: group}, User: &User{ID: 2007},
								Account: &Account{ID: 3007, Platform: PlatformOpenAI, Type: kind}, PricingAt: at,
							})
							require.NoError(t, err)
							wantTier, multiplier := observed, 2.0
							switch observed {
							case "default":
								multiplier = 1
								if kind != AccountTypeAPIKey {
									wantTier, multiplier = "ultrafast", 3
									if !at.Before(cutoff) {
										multiplier = 6
									}
								}
							case "flex":
								multiplier = 0.5
							}
							require.NotNil(t, repo.lastLog)
							require.Equal(t, wantTier, *repo.lastLog.ServiceTier)
							require.InDelta(t, 1000*unit*multiplier, repo.lastLog.InputCost, 1e-12)
							require.InDelta(t, 50*unit*5*multiplier, repo.lastLog.OutputCost, 1e-12)
							require.InDelta(t, 100*unit*0.05*multiplier, repo.lastLog.CacheReadCost, 1e-12)
							want := (1000 + 50*5 + 100*0.05) * unit * multiplier
							require.InDelta(t, want, repo.lastLog.TotalCost, 1e-12)
							require.InDelta(t, want*0.35, repo.lastLog.ActualCost, 1e-12)
						})
					}
				}
			}
		}
	}
}
