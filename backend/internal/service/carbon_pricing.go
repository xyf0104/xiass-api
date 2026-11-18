package service

import (
	"strings"
	"time"
)

// Keep the XIASS business-date cutoff while using the v0.2.23 token rates.
var carbonStandardPricingAt = time.Date(2026, 12, 1, 0, 0, 0, 0, time.FixedZone("Asia/Shanghai", 8*60*60))

func isCarbonPricingModel(model string) bool {
	return normalizeModelNameForPricing(strings.ToLower(strings.TrimSpace(model))) == "gemini-4-carbon"
}

func carbonIntroductoryPricingAt(at time.Time) bool {
	if at.IsZero() {
		at = time.Now()
	}
	return at.Before(carbonStandardPricingAt)
}

// Resolve on every lookup: never freeze introductory rates in the loaded catalog.
func carbonFallbackPricingAt(model string, at time.Time) *LiteLLMModelPricing {
	if !isCarbonPricingModel(model) {
		return nil
	}
	input, output, cache := 4e-6, 20e-6, 0.2e-6
	if carbonIntroductoryPricingAt(at) {
		input, output, cache = 2e-6, 10e-6, 0.1e-6
	}
	return &LiteLLMModelPricing{
		InputCostPerToken: input, OutputCostPerToken: output,
		CacheReadInputTokenCost: cache, SupportsPromptCaching: true,
		InputCostPerTokenPriority: input * 1.8, OutputCostPerTokenPriority: output * 1.8,
		CacheReadInputTokenCostPriority: cache * 1.8, SupportsServiceTier: true,
		LiteLLMProvider: "gemini", Mode: "chat",
	}
}
