package openai

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDefaultModelsIncludeBareGPT56Alias(t *testing.T) {
	require.Contains(t, DefaultModelIDs(), "gpt-5.6")
}

func TestDefaultModelsIncludeGPT6AstraAndAlias(t *testing.T) {
	require.Contains(t, DefaultModelIDs(), "gpt-6-astra")
	require.Contains(t, DefaultModelIDs(), "gpt-6")
}

func TestDefaultAccountTestModelsOnlyIncludeCurrentRunnableModels(t *testing.T) {
	models := DefaultAccountTestModels()
	ids := make([]string, 0, len(models))
	seen := make(map[string]struct{}, len(models))
	for _, model := range models {
		require.NotContains(t, seen, model.ID)
		seen[model.ID] = struct{}{}
		ids = append(ids, model.ID)
	}

	require.Equal(t, []string{
		"gpt-5.6-sol",
		"gpt-6-astra",
		"gpt-5.6-terra",
		"gpt-5.6-luna",
		"gpt-5.5",
		"gpt-5.4",
		"gpt-5.4-mini",
		"gpt-image-2",
		"gpt-image-2.5-flare",
		"gpt-image-2.5-sunburst",
	}, ids)
	require.NotContains(t, ids, "gpt-5.6")
	require.NotContains(t, ids, "gpt-6")
	require.NotContains(t, ids, "gpt-5.2")
	require.NotContains(t, ids, "gpt-image-1")
	require.NotContains(t, ids, "gpt-image-1.5")
}

func TestDefaultModelsPreferConcreteGPT56SolForAccountTests(t *testing.T) {
	require.NotEmpty(t, DefaultModels)
	require.Equal(t, "gpt-5.6-sol", DefaultModels[0].ID)
}
