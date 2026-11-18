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
	if canonicalAntigravityCarbonModel(model) == "gemini-4-carbon" {
		return resolveAntigravityCarbonCatalogModel(account, model, model, "") != ""
	}
	if model != "gemini-4-argon" && !strings.HasPrefix(model, "gemini-4-argon-") {
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
		if len(snapshot.Models) > 0 {
			return allowed
		}
	}
	return true
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
