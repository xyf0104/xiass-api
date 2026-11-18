package service

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/domain"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

type antigravityWrappedModelProfile struct {
	upstreamModel  string
	thinkingBudget *int
	thinkingLevel  string
	ensureThinking bool
}

type antigravityGeminiFlashFamily struct {
	baseModel    string
	tieredModel  string
	publicModels []string
}

var antigravityGeminiFlashFamilies = []antigravityGeminiFlashFamily{
	{
		baseModel:   "gemini-3.7-flash",
		tieredModel: domain.AntigravityGemini37FlashTieredModel,
		publicModels: []string{
			"gemini-3.7-flash-high",
			"gemini-3.7-flash-medium",
			"gemini-3.7-flash-low",
		},
	},
	{
		baseModel:   "gemini-3.8-flash",
		tieredModel: domain.AntigravityGemini38FlashTieredModel,
		publicModels: []string{
			"gemini-3.8-flash-high",
			"gemini-3.8-flash-medium",
			"gemini-3.8-flash-low",
		},
	},
}

func resolveAntigravityWrappedModelProfile(requestedModel, mappedModel string) antigravityWrappedModelProfile {
	requested := normalizeAntigravityCompatModel(requestedModel)
	mapped := normalizeAntigravityCompatModel(mappedModel)
	if canonicalAntigravityCarbonModel(mapped) == domain.AntigravityGemini4CarbonModel {
		profile := antigravityWrappedModelProfile{upstreamModel: domain.AntigravityGemini4CarbonModel}
		for _, candidate := range []string{requested, mapped} {
			if candidate != domain.AntigravityGemini4CarbonModel && canonicalAntigravityCarbonModel(candidate) == domain.AntigravityGemini4CarbonModel {
				profile.thinkingLevel = candidate[strings.LastIndex(candidate, "-")+1:]
				break
			}
		}
		return profile
	}

	if family, ok := antigravityGeminiFlashFamilyForModel(mapped); ok {
		// Only 3.7 requires the XIASS tiered route. For 3.8, preserve the
		// catalog's concrete variant unless the account explicitly maps to tiered.
		if family.baseModel == "gemini-3.8-flash" && mapped != family.tieredModel {
			return antigravityWrappedModelProfile{}
		}
		tier := mapped
		if _, requestedOK := antigravityGeminiFlashFamilyForModel(requested); requestedOK {
			tier = requested
		}

		profile := antigravityWrappedModelProfile{upstreamModel: family.tieredModel}
		switch {
		case strings.HasSuffix(tier, "-high"):
			profile.thinkingBudget = antigravityIntPtr(-1)
		case strings.HasSuffix(tier, "-low"):
			profile.thinkingBudget = antigravityIntPtr(1000)
		case tier == family.baseModel, strings.HasSuffix(tier, "-medium"):
			profile.thinkingBudget = antigravityIntPtr(4000)
		}
		return profile
	}

	if mapped == "claude-sonnet-4-6-thinking" ||
		(mapped == "claude-sonnet-4-6" && requested == "claude-sonnet-4-6-thinking") {
		return antigravityWrappedModelProfile{
			upstreamModel:  "claude-sonnet-4-6",
			ensureThinking: true,
		}
	}

	return antigravityWrappedModelProfile{}
}

func applyAntigravityWrappedModelProfile(body []byte, profile antigravityWrappedModelProfile) ([]byte, error) {
	if profile.upstreamModel == "" {
		return body, nil
	}
	if !json.Valid(body) {
		return nil, fmt.Errorf("invalid Antigravity wrapped request")
	}

	currentModel := normalizeAntigravityCompatModel(gjson.GetBytes(body, "model").String())
	if !profileAppliesToWrappedModel(profile, currentModel) {
		// TransformClaudeToGemini may intentionally switch web-search requests to
		// another model. Do not undo that established fallback.
		return body, nil
	}

	updated, err := sjson.SetBytes(body, "model", profile.upstreamModel)
	if err != nil {
		return nil, fmt.Errorf("set Antigravity upstream model: %w", err)
	}
	if profile.thinkingBudget != nil {
		updated, err = sjson.DeleteBytes(updated, "request.generationConfig.thinkingConfig.thinkingLevel")
		if err != nil {
			return nil, err
		}
		updated, err = sjson.SetBytes(updated, "request.generationConfig.thinkingConfig.thinkingBudget", *profile.thinkingBudget)
		if err != nil {
			return nil, fmt.Errorf("set Antigravity thinking budget: %w", err)
		}
	}
	if profile.upstreamModel == domain.AntigravityGemini4CarbonModel {
		level := profile.thinkingLevel
		config := gjson.GetBytes(updated, "request.generationConfig.thinkingConfig")
		if level == "" && !config.Get("thinkingLevel").Exists() && config.Get("thinkingBudget").Exists() {
			level = geminiThinkingLevelFromBody([]byte(gjson.GetBytes(updated, "request").Raw))
		}
		updated, err = sjson.DeleteBytes(updated, "request.generationConfig.thinkingConfig.thinkingBudget")
		if err != nil {
			return nil, err
		}
		if level != "" {
			updated, err = sjson.SetBytes(updated, "request.generationConfig.thinkingConfig.thinkingLevel", level)
			if err != nil {
				return nil, err
			}
		}
	}
	if profile.ensureThinking {
		budgetPath := "request.generationConfig.thinkingConfig.thinkingBudget"
		if !gjson.GetBytes(updated, budgetPath).Exists() {
			updated, err = sjson.SetBytes(updated, budgetPath, -1)
			if err != nil {
				return nil, fmt.Errorf("enable Antigravity thinking budget: %w", err)
			}
		}
		includePath := "request.generationConfig.thinkingConfig.includeThoughts"
		if !gjson.GetBytes(updated, includePath).Exists() {
			updated, err = sjson.SetBytes(updated, includePath, true)
			if err != nil {
				return nil, fmt.Errorf("enable Antigravity thinking output: %w", err)
			}
		}
	}
	return updated, nil
}

func profileAppliesToWrappedModel(profile antigravityWrappedModelProfile, currentModel string) bool {
	if profile.upstreamModel == domain.AntigravityGemini4CarbonModel {
		return canonicalAntigravityCarbonModel(currentModel) == domain.AntigravityGemini4CarbonModel
	}
	if family, ok := antigravityGeminiFlashFamilyForModel(profile.upstreamModel); ok {
		currentFamily, currentOK := antigravityGeminiFlashFamilyForModel(currentModel)
		return currentOK && currentFamily.baseModel == family.baseModel
	}
	return currentModel == "claude-sonnet-4-6" || currentModel == "claude-sonnet-4-6-thinking"
}

func isAntigravityGeminiFlashInternalModel(model string) bool {
	normalized := normalizeAntigravityCompatModel(model)
	family, ok := antigravityGeminiFlashFamilyForModel(normalized)
	return ok && (normalized == family.baseModel || normalized == family.tieredModel)
}

func antigravityGeminiFlashFamilyForModel(model string) (antigravityGeminiFlashFamily, bool) {
	normalized := normalizeAntigravityCompatModel(model)
	for _, family := range antigravityGeminiFlashFamilies {
		if normalized == family.baseModel || normalized == family.tieredModel {
			return family, true
		}
		for _, publicModel := range family.publicModels {
			if normalized == publicModel {
				return family, true
			}
		}
	}
	return antigravityGeminiFlashFamily{}, false
}

func publicAntigravityModelIDs(models []string) []string {
	publicModels := make([]string, 0, len(models)+4)
	for _, model := range models {
		model = strings.TrimSpace(model)
		if model == "" || strings.HasSuffix(strings.ToLower(model), "-disabled") {
			continue
		}
		if family, ok := antigravityGeminiFlashFamilyForModel(model); ok && isAntigravityGeminiFlashInternalModel(model) {
			publicModels = append(publicModels, family.publicModels...)
			continue
		}
		publicModels = append(publicModels, canonicalAntigravityCarbonModel(model))
	}
	return dedupeAndSortModelIDs(publicModels)
}

func antigravityWrappedRequestModel(body []byte) string {
	return strings.TrimSpace(gjson.GetBytes(body, "model").String())
}

func canonicalAntigravityCarbonModel(model string) string {
	switch normalizeAntigravityCompatModel(model) {
	case "gemini-4-carbon", "gemini-4-carbon-high", "gemini-4-carbon-medium", "gemini-4-carbon-low":
		return domain.AntigravityGemini4CarbonModel
	default:
		return model
	}
}

func antigravityProtocolModelMapping(account *Account, model string) map[string]string {
	if strings.HasPrefix(normalizeAntigravityCompatModel(model), "gemini-3.8-flash") {
		// Account.GetModelMapping retains old XIASS aliases for other consumers.
		// Forward the administrator's stored 3.8 targets without forcing tiered.
		if mapping := stringMappingFromRaw(account.Credentials["model_mapping"]); len(mapping) > 0 {
			// Historical subscriptions may store only the internal tiered route.
			// Expand that explicit capability, but keep concrete configured variants.
			target := mapping[domain.AntigravityGemini38FlashTieredModel]
			if target == "" && mapping["gemini-3.8-flash"] == domain.AntigravityGemini38FlashTieredModel {
				target = domain.AntigravityGemini38FlashTieredModel
			}
			if target != "" {
				for _, level := range []string{"high", "medium", "low"} {
					id := "gemini-3.8-flash-" + level
					if _, exists := mapping[id]; !exists {
						mapping[id] = target
					}
				}
			}
			return mapping
		}
		return domain.DefaultAntigravityModelMapping
	}
	return account.GetModelMapping()
}

func normalizeAntigravityCompatModel(model string) string {
	return strings.ToLower(strings.TrimPrefix(strings.TrimSpace(model), "models/"))
}

func antigravityIntPtr(value int) *int {
	return &value
}
