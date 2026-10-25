package service

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestModelPricingCatalogHasUniqueKeysAndMergedFamilyRates(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "resources", "model-pricing", "model_prices_and_context_window.json"))
	require.NoError(t, err)

	var pricing map[string]LiteLLMModelPricing
	require.NoError(t, json.Unmarshal(data, &pricing))

	tests := []struct {
		model                    string
		input, output            float64
		cacheRead, cacheCreation *float64
	}{
		{model: "gpt-6.1-sol", input: 2e-6, output: 10e-6, cacheRead: modelPricingFloatPtr(0.1e-6), cacheCreation: modelPricingFloatPtr(2.5e-6)},
		{model: "gemini-4-argon", input: 2e-6, output: 10e-6, cacheRead: modelPricingFloatPtr(0.1e-6)},
		{model: "claude-opus-5-5", input: 4e-6, output: 20e-6, cacheRead: modelPricingFloatPtr(0.2e-6), cacheCreation: modelPricingFloatPtr(5e-6)},
		{model: "claude-sonnet-5-5", input: 2e-6, output: 10e-6, cacheRead: modelPricingFloatPtr(0.2e-6), cacheCreation: modelPricingFloatPtr(2.5e-6)},
		{model: "claude-haiku-5-5", input: 0.1e-6, output: 0.5e-6, cacheRead: modelPricingFloatPtr(0.01e-6), cacheCreation: modelPricingFloatPtr(0.125e-6)},
		{model: "deepseek-flash", input: 0.15e-6, output: 0.6e-6, cacheRead: modelPricingFloatPtr(0.003e-6)},
		{model: "deepseek-v4-flash", input: 0.15e-6, output: 0.6e-6, cacheRead: modelPricingFloatPtr(0.003e-6)},
		{model: "deepseek-v4-flash-vision-exp", input: 0.15e-6, output: 0.6e-6, cacheRead: modelPricingFloatPtr(0.003e-6)},
		{model: "deepseek-v4-pro", input: 0.66e-6, output: 1.98e-6, cacheRead: modelPricingFloatPtr(0.022e-6)},
	}

	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			entry, ok := pricing[tt.model]
			require.True(t, ok, "missing model pricing entry")
			require.InDelta(t, tt.input, entry.InputCostPerToken, 1e-15)
			require.InDelta(t, tt.output, entry.OutputCostPerToken, 1e-15)
			require.NotNil(t, tt.cacheRead)
			require.InDelta(t, *tt.cacheRead, entry.CacheReadInputTokenCost, 1e-15)
			if tt.cacheCreation == nil {
				require.Zero(t, entry.CacheCreationInputTokenCost)
				return
			}
			require.InDelta(t, *tt.cacheCreation, entry.CacheCreationInputTokenCost, 1e-15)
		})
	}
}

func TestModelPricingFamilyAliasesResolveToBaseCards(t *testing.T) {
	svc := NewBillingService(&config.Config{}, nil)

	tests := []struct {
		model         string
		input, output float64
	}{
		{model: "gpt-6.1-sol-high", input: 2e-6, output: 10e-6},
		{model: "gemini-4-argon-high", input: 2e-6, output: 10e-6},
		{model: "gemini-4-argon-medium", input: 2e-6, output: 10e-6},
		{model: "gemini-4-argon-low", input: 2e-6, output: 10e-6},
		{model: "claude-opus-5-5-high", input: 4e-6, output: 20e-6},
		{model: "claude-sonnet-5-5-medium", input: 2e-6, output: 10e-6},
		{model: "claude-haiku-5-5", input: 0.1e-6, output: 0.5e-6},
		{model: "deepseek-v4-flash-vision-exp", input: 0.15e-6, output: 0.6e-6},
	}

	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			pricing, err := svc.GetModelPricing(tt.model)
			require.NoError(t, err)
			require.InDelta(t, tt.input, pricing.InputPricePerToken, 1e-15)
			require.InDelta(t, tt.output, pricing.OutputPricePerToken, 1e-15)
		})
	}
}

func modelPricingFloatPtr(value float64) *float64 {
	return &value
}
