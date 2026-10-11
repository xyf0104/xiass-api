package antigravity

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestStreamingProcessorHasContentIgnoresSignatureOnlyMalformedCall(t *testing.T) {
	processor := NewStreamingProcessor("gemini-test")
	processor.ProcessLine(`data: {"response":{"candidates":[{"content":{"parts":[{"thoughtSignature":"sig-only","text":""}]},"finishReason":"MALFORMED_FUNCTION_CALL"}]}}`)

	require.True(t, processor.MessageStartSent())
	require.False(t, processor.HasContent())
}

func TestStreamingProcessorHasContentTracksSubstantiveParts(t *testing.T) {
	tests := []struct {
		name string
		line string
	}{
		{
			name: "text with signature",
			line: `data: {"response":{"candidates":[{"content":{"parts":[{"thoughtSignature":"sig","text":"hello"}]}}]}}`,
		},
		{
			name: "thinking",
			line: `data: {"response":{"candidates":[{"content":{"parts":[{"thought":true,"text":"reasoning"}]}}]}}`,
		},
		{
			name: "function call",
			line: `data: {"response":{"candidates":[{"content":{"parts":[{"functionCall":{"name":"lookup","args":{"q":"x"}}}]}}]}}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			processor := NewStreamingProcessor("gemini-test")
			processor.ProcessLine(tt.line)
			require.True(t, processor.HasContent())
		})
	}
}
