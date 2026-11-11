package openai

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestValidateGPT61ReasoningEffort(t *testing.T) {
	for _, tc := range []struct {
		model  string
		effort string
		reject bool
	}{
		{"gpt-6.1-sol", "none", true},
		{"gpt-6.1-sol", "minimal", true},
		{"openai/gpt-6.1-sol", "low", false},
		{"gpt-6.1-terra", "none", false},
		{"gpt-6.1-terra_minimal", "minimal", true},
		{"openai/gpt-6.1-terra", " MINIMAL ", true},
		{"gpt-6.1-terra-openai-compact", "max", false},
		{"gpt-5.6-luna", "none", false},
		{"gpt-6.1-terra-other", "minimal", false},
	} {
		t.Run(tc.model+"/"+tc.effort, func(t *testing.T) {
			require.Equal(t, tc.reject, ValidateGPT61ReasoningEffort(tc.model, tc.effort) != nil)
		})
	}
}
