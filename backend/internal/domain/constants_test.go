package domain

import "testing"

func TestDefaultAntigravityModelMapping_ImageCompatibilityAliases(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"gemini-2.5-flash-image":         "gemini-2.5-flash-image",
		"gemini-2.5-flash-image-preview": "gemini-2.5-flash-image",
		"gemini-3.1-flash-image":         "gemini-3.1-flash-image",
		"gemini-3.1-flash-image-preview": "gemini-3.1-flash-image",
		"gemini-3-pro-image":             "gemini-3.1-flash-image",
		"gemini-3-pro-image-preview":     "gemini-3.1-flash-image",
	}

	for from, want := range cases {
		got, ok := DefaultAntigravityModelMapping[from]
		if !ok {
			t.Fatalf("expected mapping for %q to exist", from)
		}
		if got != want {
			t.Fatalf("unexpected mapping for %q: got %q want %q", from, got, want)
		}
	}
}

func TestDefaultAntigravityModelMapping_ContainsNewClaudeModels(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"claude-fable-5-1": "claude-fable-5-1",
		"claude-fable-5":   "claude-fable-5",
		"claude-opus-4-8":  "claude-opus-4-8",
	}
	for from, want := range cases {
		got, ok := DefaultAntigravityModelMapping[from]
		if !ok {
			t.Fatalf("expected mapping for %q to exist", from)
		}
		if got != want {
			t.Fatalf("unexpected mapping for %q: got %q want %q", from, got, want)
		}
	}
}

func TestDefaultAntigravityModelMapping_PreservesExplicitSonnet45AndMigratesLegacyAliases(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"claude-sonnet-4-5":          "claude-sonnet-4-5",
		"claude-sonnet-4-5-thinking": "claude-sonnet-4-5-thinking",
		"claude-sonnet-4-5-20250929": "claude-sonnet-4-5",
	}
	for model, want := range cases {
		if got := DefaultAntigravityModelMapping[model]; got != want {
			t.Fatalf("expected model %q to map to %q, got %q", model, want, got)
		}
	}
}

func TestDefaultAntigravityModelMapping_Gemini31ProAliases(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		AntigravityGemini31ProAgentModel: AntigravityGemini31ProAgentModel,
		"gemini-3.1-pro":                 AntigravityGemini31ProAgentModel,
		"gemini-3.1-pro-high":            AntigravityGemini31ProAgentModel,
		"gemini-3.1-pro-preview":         AntigravityGemini31ProAgentModel,
		"gemini-3.1-pro-low":             "gemini-3.1-pro-low",
	}

	for from, want := range cases {
		got, ok := DefaultAntigravityModelMapping[from]
		if !ok {
			t.Fatalf("expected mapping for %q to exist", from)
		}
		if got != want {
			t.Fatalf("unexpected mapping for %q: got %q want %q", from, got, want)
		}
	}
}

func TestDefaultAntigravityModelMapping_Gemini36FlashModels(t *testing.T) {
	cases := map[string]string{
		"gemini-3.6-flash":        AntigravityGemini36FlashMediumModel,
		"gemini-3.6-flash-high":   "gemini-3.6-flash-high",
		"gemini-3.6-flash-low":    "gemini-3.6-flash-low",
		"gemini-3.6-flash-medium": AntigravityGemini36FlashMediumModel,
		"gemini-3.6-flash-tiered": "gemini-3.6-flash-tiered",
	}
	for model, want := range cases {
		if got := DefaultAntigravityModelMapping[model]; got != want {
			t.Fatalf("expected %s to map to %q, got %q", model, want, got)
		}
	}
}

func TestDefaultAntigravityModelMapping_LiveCatalogAdditions(t *testing.T) {
	for _, model := range []string{"gemini-3-flash-agent", "gemini-3.1-flash-lite"} {
		if got := DefaultAntigravityModelMapping[model]; got != model {
			t.Fatalf("expected live model %s to map to itself, got %q", model, got)
		}
	}
}

func TestDefaultAntigravityModelMapping_Gemini37FlashModels(t *testing.T) {
	for _, model := range []string{"gemini-3.7-flash-high", "gemini-3.7-flash-medium", "gemini-3.7-flash-low"} {
		if got := DefaultAntigravityModelMapping[model]; got != model {
			t.Fatalf("expected public model %s to remain externally self-mapped, got %q", model, got)
		}
	}
	for _, internalModel := range []string{"gemini-3.7-flash", AntigravityGemini37FlashTieredModel} {
		if _, exists := DefaultAntigravityModelMapping[internalModel]; exists {
			t.Fatalf("internal compatibility model %s must not be advertised", internalModel)
		}
	}
}

func TestDefaultAntigravityModelMapping_Gemini38FlashModels(t *testing.T) {
	for _, model := range []string{"gemini-3.8-flash-high", "gemini-3.8-flash-medium", "gemini-3.8-flash-low"} {
		if got := DefaultAntigravityModelMapping[model]; got != model {
			t.Fatalf("expected public model %s to remain externally self-mapped, got %q", model, got)
		}
	}
	for _, internalModel := range []string{"gemini-3.8-flash", AntigravityGemini38FlashTieredModel} {
		if _, exists := DefaultAntigravityModelMapping[internalModel]; exists {
			t.Fatalf("internal compatibility model %s must not be advertised", internalModel)
		}
	}
}

func TestDefaultAntigravityModelMapping_VerifiedCompatibilityAliases(t *testing.T) {
	cases := map[string]string{
		"gemini-3.5-flash":           AntigravityGemini35FlashMediumModel,
		"gemini-3.5-flash-medium":    AntigravityGemini35FlashMediumModel,
		"gemini-3.5-flash-low":       AntigravityGemini35FlashLowModel,
		"claude-sonnet-4-6-thinking": "claude-sonnet-4-6",
	}
	for model, want := range cases {
		if got := DefaultAntigravityModelMapping[model]; got != want {
			t.Fatalf("expected %s to map to %q, got %q", model, want, got)
		}
	}
	if _, exists := DefaultAntigravityModelMapping["gemini-3.5-flash-high"]; exists {
		t.Fatal("gemini-3.5-flash-high must not be advertised without a verified upstream model")
	}
}

func TestDefaultBedrockModelMapping_ContainsNewClaudeModels(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"claude-fable-5-1": "anthropic.claude-fable-5-1",
		"claude-fable-5":   "anthropic.claude-fable-5",
		"claude-opus-4-8":  "us.anthropic.claude-opus-4-8-v1",
	}
	for from, want := range cases {
		got, ok := DefaultBedrockModelMapping[from]
		if !ok {
			t.Fatalf("expected Bedrock mapping for %q to exist", from)
		}
		if got != want {
			t.Fatalf("unexpected Bedrock mapping for %q: got %q want %q", from, got, want)
		}
	}
}
