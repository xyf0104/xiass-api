package service

import (
	"encoding/json"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestGeminiResponseToChatCompletionsOmitsThoughtParts(t *testing.T) {
	rawData := []byte(`{"candidates":[{"content":{"role":"model","parts":[` +
		`{"text":"**Solving the puzzle**\n\nThe ball costs 0.05.\n\n\n","thought":true},` +
		`{"text":"0.05","thoughtSignature":"sig_1"}]},"finishReason":"STOP","index":0}],` +
		`"usageMetadata":{"promptTokenCount":36,"candidatesTokenCount":4,"thoughtsTokenCount":217,"totalTokenCount":257}}`)
	var geminiResp map[string]any
	require.NoError(t, json.Unmarshal(rawData, &geminiResp))

	got, usage, err := geminiResponseToChatCompletions(geminiResp, "gemini-test", rawData, nil)
	require.NoError(t, err)
	require.Len(t, got.Choices, 1)

	var content string
	require.NoError(t, json.Unmarshal(got.Choices[0].Message.Content, &content))
	require.Equal(t, "0.05", content)
	require.Empty(t, got.Choices[0].Message.ReasoningContent)
	require.Equal(t, "stop", got.Choices[0].FinishReason)
	require.Equal(t, 221, usage.OutputTokens)
}

func TestGeminiResponseToChatCompletionsThoughtOnlyResponse(t *testing.T) {
	rawData := []byte(`{"candidates":[{"content":{"role":"model","parts":[` +
		`{"text":"Still reasoning about","thought":true}]},"finishReason":"MAX_TOKENS","index":0}],` +
		`"usageMetadata":{"promptTokenCount":36,"candidatesTokenCount":0,"thoughtsTokenCount":64,"totalTokenCount":100}}`)
	var geminiResp map[string]any
	require.NoError(t, json.Unmarshal(rawData, &geminiResp))

	got, usage, err := geminiResponseToChatCompletions(geminiResp, "gemini-test", rawData, nil)
	require.NoError(t, err)
	require.Len(t, got.Choices, 1)
	require.Empty(t, got.Choices[0].Message.Content)
	require.Empty(t, got.Choices[0].Message.ReasoningContent)
	require.Equal(t, "length", got.Choices[0].FinishReason)
	require.Equal(t, 64, usage.OutputTokens)
}

func TestConvertGeminiToClaudeMessageOmitsThoughtParts(t *testing.T) {
	geminiResp := map[string]any{
		"candidates": []any{map[string]any{
			"content": map[string]any{"parts": []any{
				map[string]any{"text": "planning the call", "thought": true},
				map[string]any{"text": "checking", "thought": false},
				map[string]any{"functionCall": map[string]any{"name": "get_weather", "args": map[string]any{"city": "Paris"}}, "thoughtSignature": "sig_1"},
			}},
			"finishReason": "STOP",
		}},
	}
	rawData, err := json.Marshal(geminiResp)
	require.NoError(t, err)

	got, _ := convertGeminiToClaudeMessage(geminiResp, "gemini-test", rawData, false)
	content, ok := got["content"].([]any)
	require.True(t, ok)
	require.Len(t, content, 2)
	require.Equal(t, map[string]any{"type": "text", "text": "checking"}, content[0])
	toolUse, ok := content[1].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "tool_use", toolUse["type"])
	require.Equal(t, "get_weather", toolUse["name"])
	require.Equal(t, "tool_use", got["stop_reason"])
}
