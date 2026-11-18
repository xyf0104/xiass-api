//go:build unit

package service

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestGeminiCarbonHydrogenUpstreamPricing(t *testing.T) {
	body, err := os.ReadFile("../../resources/model-pricing/model_prices_and_context_window.json")
	require.NoError(t, err)
	for name, catalog := range map[string]*PricingService{
		"fallback": nil, "empty": {}, "bundled": newStubPricingServiceFromJSON(t, string(body)),
	} {
		bs := NewBillingService(nil, catalog)
		for _, tc := range []struct {
			model                string
			at                   time.Time
			input, output, cache float64
		}{
			{"gemini-4-carbon", carbonStandardPricingAt.Add(-time.Second), 2, 10, 0.1},
			{"gemini-4-carbon-high", carbonStandardPricingAt, 4, 20, 0.2},
			{"models/gemini-4-hydrogen", carbonStandardPricingAt, 1.5, 7.5, 0.075},
		} {
			t.Run(name+"/"+tc.model, func(t *testing.T) {
				for _, tier := range []string{"", "priority", "fast"} {
					ratio := 1.0
					if tier != "" {
						ratio = 1.8
					}
					cost, err := bs.CalculateCostUnified(CostInput{Ctx: context.Background(), Model: tc.model, PricingAt: tc.at,
						Resolver: NewModelPricingResolver(nil, bs), ServiceTier: tier, RateMultiplier: 0.4,
						Tokens: UsageTokens{InputTokens: 1_000_000, OutputTokens: 1_000_000, CacheReadTokens: 1_000_000}})
					require.NoError(t, err)
					require.InDelta(t, tc.input*ratio, cost.InputCost, 1e-12)
					require.InDelta(t, tc.output*ratio, cost.OutputCost, 1e-12)
					require.InDelta(t, tc.cache*ratio, cost.CacheReadCost, 1e-12)
					require.InDelta(t, (tc.input+tc.output+tc.cache)*ratio*0.4, cost.ActualCost, 1e-12)
					require.False(t, cost.LongContextBillingApplied)
					pricing, err := bs.getModelPricingAt(tc.model, tc.at)
					require.NoError(t, err)
					require.Zero(t, pricing.CacheCreationPricePerToken)
					require.False(t, pricing.SupportsCacheBreakdown)
				}
			})
		}
	}
}
