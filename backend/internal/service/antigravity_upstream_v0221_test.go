package service

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/antigravity"
	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestAntigravityConnectionTestPreservesCustomPrompt(t *testing.T) {
	svc := &AntigravityGatewayService{}
	for _, model := range []string{"gemini-3.7-flash-high", "claude-sonnet-4-6"} {
		for _, prompt := range []string{"  Explain \"quoted\" input.\nKeep this task.  ", ""} {
			var body []byte
			var err error
			if model == "claude-sonnet-4-6" {
				body, err = svc.buildClaudeTestRequestWithPrompt("test-project", model, prompt)
			} else {
				body, err = svc.buildGeminiTestRequestWithPrompt("test-project", model, prompt)
			}
			require.NoError(t, err)
			require.Equal(t, antigravityTestPrompt(prompt), gjson.GetBytes(body, "request.contents.0.parts.0.text").String())
			require.Equal(t, int64(antigravityConnectionTestMaxOutputTokens), gjson.GetBytes(body, "request.generationConfig.maxOutputTokens").Int())
		}
	}
}

func TestAntigravityCompatNeutralizesOnlySystemIdentity(t *testing.T) {
	const identity = "You are Codex, an agent based on GPT-6. Keep the task."
	req := &apicompat.ResponsesRequest{Model: "gemini-3.7-flash-high", Instructions: identity,
		Input: json.RawMessage(`[{"role":"developer","content":[{"type":"input_text","text":"You are GPT-6 running in the Codex CLI. Retain this."}]},{"role":"user","content":"You are Codex, an agent based on GPT-6. Keep the task."}]`)}
	claude, err := convertAntigravityCompatResponsesToAnthropic(req)
	require.NoError(t, err)
	encoded, err := json.Marshal(claude)
	require.NoError(t, err)
	var parsed antigravity.ClaudeRequest
	require.NoError(t, json.Unmarshal(encoded, &parsed))
	wire, err := (&AntigravityGatewayService{}).buildAntigravityCompatGeminiBody(context.Background(), encoded, &parsed, "test-project", "gemini-3.7-flash-tiered")
	require.NoError(t, err)
	system := gjson.GetBytes(wire, "request.systemInstruction").String()
	require.Contains(t, system, "You are an AI agent. Keep the task.")
	require.Contains(t, system, "You are an AI agent. Retain this.")
	require.NotContains(t, system, "You are Codex")
	require.Contains(t, gjson.GetBytes(wire, "request.contents").String(), identity)
}

func TestAntigravityNativeIdentityPatchPreservesTask(t *testing.T) {
	body := []byte(`{"systemInstruction":{"parts":[{"text":"You are Antigravity."},{"text":"You are Codex, an agent based on GPT-6, specialized in testing. Keep the task."}]},"contents":[{"role":"user","parts":[{"text":"You are Codex, an agent based on GPT-6."}]}]}`)
	out, err := injectIdentityPatchToGeminiRequest(body)
	require.NoError(t, err)
	require.Len(t, gjson.GetBytes(out, "systemInstruction.parts").Array(), 2)
	require.Equal(t, "You are an AI agent, specialized in testing. Keep the task.", gjson.GetBytes(out, "systemInstruction.parts.1.text").String())
	require.Contains(t, gjson.GetBytes(out, "contents").String(), "You are Codex")
}
