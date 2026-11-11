package service

import (
	"strings"
	"time"
)

// Business rates supplied by the operator's screenshot, not independently
// verified vendor prices. The cutoff is 2026-12-01 00:00 Asia/Shanghai (UTC+8),
// independent of the server's local timezone. Output includes thinking tokens.
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
// The standard cache price assumes the same 95% discount as the introductory
// screenshot. The screenshot does not quote a post-November cache price.
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
		LiteLLMProvider: "gemini", Mode: "chat",
	}
}
