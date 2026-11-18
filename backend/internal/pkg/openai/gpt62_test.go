package openai

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGPT62DescriptorsAndRouting(t *testing.T) {
	for model, data := range map[string][]byte{
		"gpt-6.2-sol":   CodexGPT62SolMetadata,
		"gpt-6.2-astra": CodexGPT62AstraMetadata,
	} {
		t.Run(model, func(t *testing.T) {
			var descriptor struct {
				Slug          string `json:"slug"`
				ModelMessages struct {
					InstructionsTemplate string `json:"instructions_template"`
				} `json:"model_messages"`
			}
			require.NoError(t, json.Unmarshal(data, &descriptor))
			require.Equal(t, model, descriptor.Slug)
			require.NotEmpty(t, descriptor.ModelMessages.InstructionsTemplate)
			require.Contains(t, DefaultModelIDs(), model)
			for _, suffix := range []string{"", "-low", "-high", "-max", "-openai-compact"} {
				spelling := "openai/" + model + suffix
				require.Equal(t, model, GPT62ModelID(spelling))
				require.Equal(t, descriptor.ModelMessages.InstructionsTemplate, CodexBaseInstructionsForModel(spelling))
			}
			require.Empty(t, GPT62ModelID(model+"-unknown"))
		})
	}
}
