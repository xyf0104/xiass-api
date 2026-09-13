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

	if family, ok := antigravityGeminiFlashFamilyForModel(mapped); ok {
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
		updated, err = sjson.SetBytes(updated, "request.generationConfig.thinkingConfig.thinkingBudget", *profile.thinkingBudget)
		if err != nil {
			return nil, fmt.Errorf("set Antigravity thinking budget: %w", err)
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
	if family, ok := antigravityGeminiFlashFamilyForModel(profile.upstreamModel); ok {
		currentFamily, currentOK := antigravityGeminiFlashFamilyForModel(currentModel)
		return currentOK && currentFamily.baseModel == family.baseModel
	}
	return currentModel == "claude-sonnet-4-6" || currentModel == "claude-sonnet-4-6-thinking"
}

func isAntigravityGemini37FlashModel(model string) bool {
	family, ok := antigravityGeminiFlashFamilyForModel(model)
	return ok && family.baseModel == "gemini-3.7-flash"
}

func isAntigravityGemini37InternalModel(model string) bool {
	family, ok := antigravityGeminiFlashFamilyForModel(model)
	return ok && family.baseModel == "gemini-3.7-flash" && isAntigravityGeminiFlashInternalModel(model)
}

func isAntigravityGeminiFlashModel(model string) bool {
	_, ok := antigravityGeminiFlashFamilyForModel(model)
	return ok
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
		if model == "" {
			continue
		}
		if family, ok := antigravityGeminiFlashFamilyForModel(model); ok && isAntigravityGeminiFlashInternalModel(model) {
			publicModels = append(publicModels, family.publicModels...)
			continue
		}
		publicModels = append(publicModels, model)
	}
	return dedupeAndSortModelIDs(publicModels)
}

func antigravityWrappedRequestModel(body []byte) string {
	return strings.TrimSpace(gjson.GetBytes(body, "model").String())
}

func normalizeAntigravityCompatModel(model string) string {
	return strings.ToLower(strings.TrimPrefix(strings.TrimSpace(model), "models/"))
}

func antigravityIntPtr(value int) *int {
	return &value
}
