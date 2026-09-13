package antigravity

import (
	"strings"
	"testing"
)

func TestDefaultModels_AdvertisesOnlyVerifiedCurrentModels(t *testing.T) {
	t.Parallel()

	models := DefaultModels()
	byID := make(map[string]ClaudeModel, len(models))
	for _, m := range models {
		byID[m.ID] = m
	}

	// 只验证经过服务器测试确认可用的核心模型
	requiredIDs := []string{
		"claude-fable-5-1",
		"claude-opus-4-6-thinking",
		"claude-sonnet-4-6",
		"gemini-2.5-flash",
		"gemini-2.5-flash-lite",
		"gemini-2.5-flash-thinking",
		"gemini-2.5-pro",
		"gemini-3-flash",
		"gemini-3-flash-agent",
		"gemini-3.1-flash-lite",
		"gemini-3.1-pro-low",
		"gemini-3.5-flash-medium",
		"gemini-3.5-flash-low",
		"gpt-oss-120b-medium",
		"gemini-3.6-flash-high",
		"gemini-3.6-flash-low",
		"gemini-3.6-flash-medium",
		"gemini-3.6-flash-tiered",
		"gemini-3.7-flash-high",
		"gemini-3.7-flash-medium",
		"gemini-3.7-flash-low",
		"gemini-3.8-flash-high",
		"gemini-3.8-flash-medium",
		"gemini-3.8-flash-low",
	}

	for _, id := range requiredIDs {
		if _, ok := byID[id]; !ok {
			t.Fatalf("expected model %q to be exposed in DefaultModels", id)
		}
	}

	removedIDs := []string{
		"gemini-2.5-flash-image",
		"gemini-2.5-flash-image-preview",
		"gemini-3-pro-low",
		"gemini-3-pro-high",
		"gemini-3-pro-preview",
		"gemini-3-pro-image",
		"gemini-3.1-flash-image-preview",
	}
	for _, id := range removedIDs {
		if _, ok := byID[id]; ok {
			t.Fatalf("obsolete or compatibility-only model %q must not be advertised", id)
		}
	}
}

func TestClaudeModels_Sonnet46UsesVerifiedBaseID(t *testing.T) {
	t.Parallel()

	models := make(map[string]modelDef, len(claudeModels))
	for _, model := range claudeModels {
		models[model.ID] = model
	}

	sonnet, exists := models["claude-sonnet-4-6"]
	if !exists {
		t.Fatal("expected verified claude-sonnet-4-6 model")
	}
	if sonnet.DisplayName != "Claude Sonnet 4.6 (Thinking)" || !sonnet.IsReasoning {
		t.Fatalf("unexpected Sonnet 4.6 metadata: %#v", sonnet)
	}
	if _, exists := models["claude-sonnet-4-6-thinking"]; exists {
		t.Fatal("nonexistent claude-sonnet-4-6-thinking upstream ID must not be advertised")
	}
}

func TestGeminiModels_Gemini37FlashMetadata(t *testing.T) {
	t.Parallel()

	models := make(map[string]modelDef, len(geminiModels))
	for _, model := range geminiModels {
		models[model.ID] = model
	}

	cases := map[string]struct {
		displayName string
		isReasoning bool
	}{
		"gemini-3.7-flash-high":   {displayName: "Gemini 3.7 Flash High", isReasoning: true},
		"gemini-3.7-flash-medium": {displayName: "Gemini 3.7 Flash Medium", isReasoning: true},
		"gemini-3.7-flash-low":    {displayName: "Gemini 3.7 Flash Low", isReasoning: true},
	}

	for id, want := range cases {
		got, ok := models[id]
		if !ok {
			t.Fatalf("expected model %q to exist", id)
		}
		if got.DisplayName != want.displayName {
			t.Errorf("unexpected display name for %q: got %q want %q", id, got.DisplayName, want.displayName)
		}
		if got.CreatedAt != "2026-08-18T00:00:00Z" {
			t.Errorf("unexpected creation date for %q: got %q", id, got.CreatedAt)
		}
		if got.IsReasoning != want.isReasoning {
			t.Errorf("unexpected reasoning flag for %q: got %t want %t", id, got.IsReasoning, want.isReasoning)
		}
	}
	if _, exists := models["gemini-3.7-flash-tiered"]; exists {
		t.Fatal("raw Gemini 3.7 tiered model must not replace the three public tiers")
	}
	if _, exists := models["gemini-3.7-flash"]; exists {
		t.Fatal("Gemini 3.7 base alias is compatibility-only and must not be a primary catalog entry")
	}
	if !IsGeminiReasoningModel("gemini-3.7-flash-tiered") {
		t.Fatal("internal Gemini 3.7 tiered route must retain reasoning-model request semantics")
	}
}

func TestGeminiModels_Gemini38FlashMetadata(t *testing.T) {
	t.Parallel()

	models := make(map[string]modelDef, len(geminiModels))
	for _, model := range geminiModels {
		models[model.ID] = model
	}

	for _, tier := range []string{"high", "medium", "low"} {
		id := "gemini-3.8-flash-" + tier
		got, ok := models[id]
		if !ok {
			t.Fatalf("expected model %q to exist", id)
		}
		if got.DisplayName != "Gemini 3.8 Flash "+strings.ToUpper(tier[:1])+tier[1:] {
			t.Errorf("unexpected display name for %q: %q", id, got.DisplayName)
		}
		if got.CreatedAt != "2026-09-02T00:00:00Z" || !got.IsReasoning {
			t.Errorf("unexpected Gemini 3.8 metadata for %q: %#v", id, got)
		}
	}
	if _, exists := models["gemini-3.8-flash"]; exists {
		t.Fatal("Gemini 3.8 base alias is compatibility-only and must not be advertised")
	}
	if _, exists := models["gemini-3.8-flash-tiered"]; exists {
		t.Fatal("raw Gemini 3.8 tiered model must not replace the three public tiers")
	}
	if !IsGeminiReasoningModel("gemini-3.8-flash-tiered") {
		t.Fatal("internal Gemini 3.8 tiered route must retain reasoning-model request semantics")
	}
}

func TestGeminiModels_Gemini35PublicTierLabels(t *testing.T) {
	models := make(map[string]modelDef, len(geminiModels))
	for _, model := range geminiModels {
		models[model.ID] = model
	}
	if got := models["gemini-3.5-flash-medium"].DisplayName; got != "Gemini 3.5 Flash Medium" {
		t.Fatalf("unexpected medium display name: %q", got)
	}
	if got := models["gemini-3.5-flash-low"].DisplayName; got != "Gemini 3.5 Flash Low" {
		t.Fatalf("unexpected low display name: %q", got)
	}
}
