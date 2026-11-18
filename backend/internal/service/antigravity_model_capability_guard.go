package service

import "strings"

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
	isCarbon := canonicalAntigravityCarbonModel(model) == "gemini-4-carbon"
	if !isCarbon && model != "gemini-4-argon" && !strings.HasPrefix(model, "gemini-4-argon-") {
		return true
	}
	// Inspect only a snapshot validated against the current credentials/proxy.
	if snapshot := account.GetUpstreamModelMetadataSnapshot(); snapshot != nil {
		for _, disabled := range snapshot.DisabledModels {
			if antigravityCatalogModelMatches(strings.TrimSuffix(normalizeAntigravityModelName(disabled), "-disabled"), model) {
				return false
			}
		}
		allowed := false
		for id := range snapshot.Models {
			id = normalizeAntigravityModelName(id)
			if strings.HasSuffix(id, "-disabled") {
				if antigravityCatalogModelMatches(strings.TrimSuffix(id, "-disabled"), model) {
					return false
				}
				continue
			}
			allowed = allowed || antigravityCatalogModelMatches(id, model)
		}
		if len(snapshot.Models) > 0 || isCarbon {
			return allowed
		}
	}
	if !isCarbon {
		return true
	}
	for source, target := range stringMappingFromRaw(account.Credentials["model_mapping"]) {
		if normalizeAntigravityModelName(source) == "gemini-4-carbon-disabled" || normalizeAntigravityModelName(target) == "gemini-4-carbon-disabled" {
			return false
		}
	}
	// A plan name is not evidence that this identity can call Carbon.
	return false
}

func antigravityCatalogModelMatches(catalogModel, target string) bool {
	catalogModel = canonicalAntigravityCarbonModel(normalizeAntigravityModelName(catalogModel))
	target = canonicalAntigravityCarbonModel(normalizeAntigravityModelName(target))
	if catalogModel == target {
		return true
	}
	family, ok := antigravityGeminiFlashFamilyForModel(catalogModel)
	if !ok {
		return false
	}
	targetFamily, targetOK := antigravityGeminiFlashFamilyForModel(target)
	return targetOK && family.baseModel == targetFamily.baseModel &&
		(isAntigravityGeminiFlashInternalModel(catalogModel) || isAntigravityGeminiFlashInternalModel(target))
}
