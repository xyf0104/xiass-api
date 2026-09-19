package service

import (
	"log/slog"
	"sort"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/xai"
)

const (
	VideoPriceFamilyGrokImagineVideo   = "grok-imagine-video"
	VideoPriceFamilyGrokImagineVideo15 = "grok-imagine-video-1.5"
)

// CanonicalGrokImagineVideoPriceFamily maps aliases to stable price-family keys.
func CanonicalGrokImagineVideoPriceFamily(model string) string {
	if model == "" {
		return ""
	}
	if canonical := xai.CanonicalImagineVideoModel(model); canonical != "" {
		switch canonical {
		case xai.DefaultImagineVideo15Model:
			return VideoPriceFamilyGrokImagineVideo15
		case xai.DefaultImagineVideoModel:
			return VideoPriceFamilyGrokImagineVideo
		}
		if strings.HasPrefix(canonical, "grok-imagine-video-") {
			return canonical
		}
	}
	m := strings.ToLower(strings.TrimSpace(model))
	for _, prefix := range []string{"xai/", "x-ai/", "grok/"} {
		if strings.HasPrefix(m, prefix) {
			m = strings.TrimPrefix(m, prefix)
			break
		}
	}
	switch {
	case m == "grok-imagine-video-1.5" || m == "grok-imagine-video-1.5-preview" ||
		m == "grok-video-1.5" || strings.Contains(m, "video-1.5"):
		return VideoPriceFamilyGrokImagineVideo15
	case m == "grok-imagine-video" || m == "grok-imagine-video-preview" ||
		m == "grok-video" || m == "grok-video-latest":
		return VideoPriceFamilyGrokImagineVideo
	default:
		return ""
	}
}

// NormalizeVideoModelPrices cleans model and resolution keys while preserving
// zero prices. Negative prices and unknown resolution tiers are discarded.
func NormalizeVideoModelPrices(in map[string]map[string]float64) map[string]map[string]float64 {
	if len(in) == 0 {
		return nil
	}
	modelKeys := make([]string, 0, len(in))
	for modelKey := range in {
		modelKeys = append(modelKeys, modelKey)
	}
	sort.Strings(modelKeys)
	out := make(map[string]map[string]float64)
	for _, modelKey := range modelKeys {
		tierPrices := in[modelKey]
		if len(tierPrices) == 0 {
			continue
		}
		family := CanonicalGrokImagineVideoPriceFamily(modelKey)
		if family == "" {
			family = strings.ToLower(strings.TrimSpace(modelKey))
			if family == "" {
				continue
			}
		}
		normalizedTiers := out[family]
		if normalizedTiers == nil {
			normalizedTiers = make(map[string]float64)
		}
		tierKeys := make([]string, 0, len(tierPrices))
		for tierKey := range tierPrices {
			tierKeys = append(tierKeys, tierKey)
		}
		sort.Strings(tierKeys)
		for _, tierKey := range tierKeys {
			price := tierPrices[tierKey]
			if price < 0 {
				continue
			}
			tier, ok := LookupVideoBillingResolution(tierKey)
			if !ok {
				slog.Warn("video_model_prices_unknown_resolution_dropped", "model_key", modelKey, "family", family, "resolution", tierKey)
				continue
			}
			if previous, exists := normalizedTiers[tier]; exists && previous != price {
				slog.Warn("video_model_prices_conflicting_tier_price", "model_key", modelKey, "family", family, "resolution", tier, "previous_price", previous, "price", price)
			}
			normalizedTiers[tier] = price
		}
		if len(normalizedTiers) > 0 {
			out[family] = normalizedTiers
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func LookupVideoModelPrice(prices map[string]map[string]float64, model, resolution string) *float64 {
	if len(prices) == 0 {
		return nil
	}
	family := CanonicalGrokImagineVideoPriceFamily(model)
	if family == "" {
		family = strings.ToLower(strings.TrimSpace(model))
	}
	if family == "" {
		return nil
	}
	tierPrices, ok := prices[family]
	if !ok || len(tierPrices) == 0 {
		return nil
	}
	tier := NormalizeVideoBillingResolutionOrDefault(resolution)
	if price, ok := tierPrices[tier]; ok {
		value := price
		return &value
	}
	return nil
}
