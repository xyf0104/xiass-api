//go:build unit

package service

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestGetModelPricing_Grok47OfficialFallback(t *testing.T) {
	svc := newTestBillingService()

	for _, model := range []string{"grok-4.7", "grok-4.7-latest"} {
		model := model
		t.Run(model, func(t *testing.T) {
			assertGrok47Pricing(t, svc, model)
		})
	}
}

func TestCalculateCost_Grok47LongContextBoundary(t *testing.T) {
	testGrok47LongContextBoundary(t, newTestBillingService(), "grok-4.7")
}

func TestBundledPricingIncludesGrok47OfficialRates(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "resources", "model-pricing", "model_prices_and_context_window.json"))
	require.NoError(t, err)

	var catalog map[string]map[string]any
	require.NoError(t, json.Unmarshal(data, &catalog))
	entry, ok := catalog["grok-4.7"]
	require.True(t, ok)
	require.Equal(t, float64(500000), entry["max_input_tokens"])
	_, hasMaxOutputTokens := entry["max_output_tokens"]
	require.False(t, hasMaxOutputTokens)
	_, hasMaxTokens := entry["max_tokens"]
	require.False(t, hasMaxTokens)

	pricingSvc := newStubPricingServiceFromJSON(t, string(data))
	svc := NewBillingService(&config.Config{}, pricingSvc)
	assertGrok47Pricing(t, svc, "grok-4.7")
	testGrok47LongContextBoundary(t, svc, "grok-4.7")
}

func assertGrok47Pricing(t *testing.T, svc *BillingService, model string) {
	t.Helper()
	pricing, err := svc.GetModelPricing(model)
	require.NoError(t, err)
	require.InDelta(t, 2e-6, pricing.InputPricePerToken, 1e-12)
	require.InDelta(t, 6e-6, pricing.OutputPricePerToken, 1e-12)
	require.InDelta(t, 0.5e-6, pricing.CacheReadPricePerToken, 1e-12)
	require.Equal(t, 200000, pricing.LongContextInputThreshold)
	require.True(t, pricing.LongContextThresholdInclusive)
	require.InDelta(t, 2.0, pricing.LongContextInputMultiplier, 1e-12)
	require.InDelta(t, 2.0, pricing.LongContextOutputMultiplier, 1e-12)
	require.False(t, pricing.SupportsCacheBreakdown)
}

func testGrok47LongContextBoundary(t *testing.T, svc *BillingService, model string) {
	t.Helper()

	tests := []struct {
		name            string
		cacheReadTokens int
		priceMultiplier float64
	}{
		{name: "199999 prompt tokens use base rates", cacheReadTokens: 99999, priceMultiplier: 1},
		{name: "200000 prompt tokens use doubled rates", cacheReadTokens: 100000, priceMultiplier: 2},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tokens := UsageTokens{
				InputTokens:     100000,
				CacheReadTokens: tt.cacheReadTokens,
				OutputTokens:    1000,
			}
			cost, err := svc.CalculateCost(model, tokens, 1)
			require.NoError(t, err)
			require.InDelta(t, float64(tokens.InputTokens)*2e-6*tt.priceMultiplier, cost.InputCost, 1e-12)
			require.InDelta(t, float64(tokens.CacheReadTokens)*0.5e-6*tt.priceMultiplier, cost.CacheReadCost, 1e-12)
			require.InDelta(t, float64(tokens.OutputTokens)*6e-6*tt.priceMultiplier, cost.OutputCost, 1e-12)
			require.Equal(t, tt.priceMultiplier == 2, cost.LongContextBillingApplied)
		})
	}
}
