package xai

import "testing"

func TestStripGrokProviderPrefix(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "xai", input: "xai/grok-4.5", want: "grok-4.5"},
		{name: "x ai case insensitive", input: " X-AI/Grok-4.5 ", want: "Grok-4.5"},
		{name: "grok", input: "grok/grok-build", want: "grok-build"},
		{name: "native", input: " grok-4.3 ", want: "grok-4.3"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := StripGrokProviderPrefix(tt.input); got != tt.want {
				t.Fatalf("StripGrokProviderPrefix(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestResolveGrokTextResponsesModelID(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		input       string
		defaultText []string
		want        string
	}{
		{name: "empty uses built in default", want: DefaultTextModel},
		{name: "empty uses caller default", defaultText: []string{"grok-4.6"}, want: "grok-4.6"},
		{name: "prefixed default alias uses caller default", input: "xai/grok-latest", defaultText: []string{"grok-4.6"}, want: "grok-4.6"},
		{name: "grok 4.7 exact", input: "grok-4.7", want: "grok-4.7"},
		{name: "grok 4.7 local latest alias", input: "xai/grok-4.7-latest", want: "grok-4.7"},
		{name: "build alias", input: "GROK/GROK-BUILD", want: "grok-build-0.1"},
		{name: "dated multi agent alias", input: "grok-4.20-multi-agent-latest", want: "grok-4.20-multi-agent-0309"},
		{name: "unknown prefixed model remains usable", input: "x-ai/custom-grok", want: "custom-grok"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := ResolveGrokTextResponsesModelID(tt.input, tt.defaultText...); got != tt.want {
				t.Fatalf("ResolveGrokTextResponsesModelID(%q, %v) = %q, want %q", tt.input, tt.defaultText, got, tt.want)
			}
		})
	}
}

func TestDefaultModelsIncludeGrok47AndPreserveExistingCatalog(t *testing.T) {
	t.Parallel()

	if DefaultTextModel != "grok-4.5" {
		t.Fatalf("DefaultTextModel = %q, want grok-4.5", DefaultTextModel)
	}

	modelsByID := make(map[string]Model)
	for _, model := range DefaultModels() {
		modelsByID[model.ID] = model
	}

	for _, modelID := range []string{
		"grok-4.7",
		"grok-4.5",
		"grok-4.3",
		"grok-build-0.1",
		"grok-composer-2.5-fast",
		"grok-4.20-0309-reasoning",
		"grok-4.20-0309-non-reasoning",
		"grok-4.20-multi-agent-0309",
		"grok-imagine",
		"grok-imagine-image",
		"grok-imagine-image-quality",
		"grok-imagine-edit",
		"grok-imagine-video",
		"grok-imagine-video-1.5",
	} {
		if _, ok := modelsByID[modelID]; !ok {
			t.Errorf("DefaultModels() missing %q", modelID)
		}
	}

	grok47 := modelsByID["grok-4.7"]
	if grok47.Object != "model" || grok47.OwnedBy != "xai" || grok47.DisplayName != "Grok 4.7" {
		t.Fatalf("grok-4.7 metadata = %#v", grok47)
	}

	mapping := DefaultModelMapping()
	for source, want := range map[string]string{
		"grok-4.7":        "grok-4.7",
		"grok-4.7-latest": "grok-4.7",
		"grok":            "grok-4.5",
		"grok-latest":     "grok-4.5",
		"grok-build":      "grok-build-0.1",
		"grok-composer":   "grok-composer-2.5-fast",
	} {
		if got := mapping[source]; got != want {
			t.Errorf("DefaultModelMapping()[%q] = %q, want %q", source, got, want)
		}
	}
}
