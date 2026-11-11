package service

import (
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/antigravity"
)

// antigravityAccountAllowsModel validates the final target, including aliases.
// A disabled catalog placeholder is not a callable model or a quota shortage.
func antigravityAccountAllowsModel(account *Account, model string) bool {
	if account == nil || account.Platform != PlatformAntigravity {
		return true
	}
	model = normalizeAntigravityModelName(model)
	if strings.HasSuffix(model, "-disabled") {
		return false
	}
	if model != "gemini-4-carbon" && !strings.HasPrefix(model, "gemini-4-carbon-") {
		return true
	}
	const disabled = "gemini-4-carbon-disabled"
	// Inspect only a snapshot validated against the current credentials/proxy.
	if snapshot := account.GetUpstreamModelMetadataSnapshot(); snapshot != nil {
		for _, model := range snapshot.DisabledModels {
			if normalizeAntigravityModelName(model) == disabled {
				return false
			}
		}
		if _, found := snapshot.Models[disabled]; found {
			return false
		}
		if _, found := snapshot.Models[model]; found {
			return true
		}
	}
	for source, target := range stringMappingFromRaw(account.Credentials["model_mapping"]) {
		if normalizeAntigravityModelName(source) == disabled || normalizeAntigravityModelName(target) == disabled {
			return false
		}
	}
	// Only Ultra has verified default access. Other/unknown plans require an
	// affirmative exact-model entry bound to their current credentials/proxy.
	return strings.EqualFold(strings.TrimSpace(antigravity.TierIDToPlanType(account.GetCredential("plan_type"))), "Ultra")
}
