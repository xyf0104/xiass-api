package openai

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDefaultModelsIncludeBareGPT56Alias(t *testing.T) {
	require.Contains(t, DefaultModelIDs(), "gpt-5.6")
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
		"gpt-5.6-terra",
		"gpt-5.6-luna",
		"gpt-5.5",
		"gpt-5.4",
		"gpt-5.4-mini",
		"gpt-image-2",
	}, ids)
	require.NotContains(t, ids, "gpt-5.6")
	require.NotContains(t, ids, "gpt-5.2")
	require.NotContains(t, ids, "gpt-image-1")
	require.NotContains(t, ids, "gpt-image-1.5")
}
