package apicompat

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestIsReasoningModelUsesGPTGeneration(t *testing.T) {
	for _, tc := range []struct {
		model string
		want  bool
	}{
		{model: "gpt-5", want: true},
		{model: "gpt-5.6-luna", want: true},
		{model: "gpt-6-astra", want: true},
		{model: "gpt-6.1-sol", want: true},
		{model: "openai/gpt-6-astra", want: true},
		{model: "OPENAI/GPT-6_ASTRA", want: true},
		{model: "openai/gpt-5.6-luna", want: true},
		{model: " GPT-7-future ", want: true},
		{model: "gpt-4.1", want: false},
		{model: "gpt-image-1", want: false},
		{model: "claude-opus-5.5", want: false},
	} {
		t.Run(tc.model, func(t *testing.T) {
			require.Equal(t, tc.want, isReasoningModel(tc.model))
		})
	}
}

func TestAnthropicToResponsesStripsSamplingForGPT61(t *testing.T) {
	temperature := 0.7
	resp, err := AnthropicToResponses(&AnthropicRequest{
		Model:       "gpt-6.1-sol",
		Messages:    []AnthropicMessage{{Role: "user", Content: json.RawMessage(`"hello"`)}},
		Temperature: &temperature,
		TopP:        &temperature,
	})

	require.NoError(t, err)
	require.Nil(t, resp.Temperature)
	require.Nil(t, resp.TopP)
}

func TestAnthropicToResponsesStripsSamplingForXIASSModelAlias(t *testing.T) {
	temperature := 0.7
	resp, err := AnthropicToResponses(&AnthropicRequest{
		Model:       "OPENAI/GPT-6_ASTRA",
		Messages:    []AnthropicMessage{{Role: "user", Content: json.RawMessage(`"hello"`)}},
		Temperature: &temperature,
		TopP:        &temperature,
	})

	require.NoError(t, err)
	require.Nil(t, resp.Temperature)
	require.Nil(t, resp.TopP)
}
