package apicompat

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestUpstreamCompatAnthropicEffortAndOutputFloor(t *testing.T) {
	for _, model := range []string{"gpt-6.2-sol", "gpt-6.2-astra", "gpt-6.1-sol", "gpt-6.1-terra"} {
		t.Run(model, func(t *testing.T) {
			req := &AnthropicRequest{Model: model, MaxTokens: 10, OutputConfig: &AnthropicOutputConfig{Effort: "max"}}
			got, err := AnthropicToResponsesWithMinOutputTokens(req, 32)
			require.NoError(t, err)
			require.Equal(t, "max", got.Reasoning.Effort)
			require.Equal(t, 32, *got.MaxOutputTokens)
			req.Thinking = &AnthropicThinking{Type: "disabled"}
			if model != "gpt-6.1-terra" {
				_, err = AnthropicToResponses(req)
				require.Error(t, err)
			}
			got, err = AnthropicToResponsesWithOptions(req, AnthropicToResponsesOptions{
				MinOutputTokens: 32, DeferReasoningEffortValidation: true,
			})
			require.NoError(t, err)
			require.Equal(t, "none", got.Reasoning.Effort)
			require.Empty(t, got.Reasoning.Summary)
			require.Equal(t, "max", req.OutputConfig.Effort)
		})
	}

	req := &AnthropicRequest{Model: "gpt-5.2", MaxTokens: 10, Thinking: &AnthropicThinking{Type: "disabled"}, OutputConfig: &AnthropicOutputConfig{Effort: "high"}}
	got, err := AnthropicToChatCompletionsRequestWithMinOutputTokens(req, 32)
	require.NoError(t, err)
	require.Equal(t, "none", got.ReasoningEffort)
	require.Equal(t, 32, *got.MaxCompletionTokens)
}

func TestUpstreamCompatChatPreservesAuthoritativeTotalAndCacheUsage(t *testing.T) {
	for _, total := range []int{0, 77} {
		usage := &ResponsesUsage{
			InputTokens: 20, OutputTokens: 7, TotalTokens: total, CacheCreationInputTokens: 6,
			InputTokensDetails: &ResponsesInputTokensDetails{CachedTokens: 4},
		}
		wantTotal := total
		if wantTotal == 0 {
			wantTotal = 27
		}
		check := func(t *testing.T, got *ChatUsage) {
			t.Helper()
			require.NotNil(t, got)
			require.Equal(t, wantTotal, got.TotalTokens)
			require.Equal(t, 20, got.PromptTokens)
			require.Equal(t, 7, got.CompletionTokens)
			require.Equal(t, 4, got.PromptTokensDetails.CachedTokens)
			require.Equal(t, 6, got.PromptTokensDetails.CacheCreationTokens)
		}
		resp := &ResponsesResponse{ID: "resp_usage", Status: "completed", ServiceTier: "priority", Usage: usage}
		got := ResponsesToChatCompletions(resp, "test")
		check(t, got.Usage)
		require.Equal(t, "priority", got.ServiceTier)
		for _, nested := range []bool{false, true} {
			state := NewResponsesEventToChatState()
			state.IncludeUsage = true
			event := &ResponsesStreamEvent{Type: "response.completed", Usage: usage}
			if nested {
				event.Usage, event.Response = nil, resp
			}
			chunks := ResponsesEventToChatChunks(event, state)
			require.Len(t, chunks, 2)
			check(t, chunks[1].Usage)
			require.Empty(t, FinalizeResponsesChatStream(state))
		}
	}
}

func TestUpstreamCompatSignedThinkingRoundTrip(t *testing.T) {
	for _, model := range []string{"claude-opus-5-5", "claude-sonnet-5-5", "claude-haiku-5-5", "claude-fable-5-2", "claude-mythos-5-2"} {
		t.Run(model, func(t *testing.T) {
			original := &AnthropicResponse{ID: "msg_signed", Model: model, Content: []AnthropicContentBlock{
				{Type: "text", Text: "before"},
				{Type: "thinking", Thinking: "plan", Signature: "signed-by-provider"},
				{Type: "tool_use", ID: "tool_1", Name: "lookup", Input: json.RawMessage(`{"q":"value"}`)},
			}}
			for _, streaming := range []bool{false, true} {
				out := AnthropicToResponsesResponse(original)
				if streaming {
					state := NewAnthropicEventToResponsesState()
					completed := false
					AnthropicEventToResponsesEvents(&AnthropicStreamEvent{
						Type: "message_start", Message: &AnthropicResponse{ID: original.ID, Model: model},
					}, state)
					for i := range original.Content {
						idx := i
						AnthropicEventToResponsesEvents(&AnthropicStreamEvent{Type: "content_block_start", Index: &idx, ContentBlock: &original.Content[i]}, state)
						if original.Content[i].Type == "text" {
							AnthropicEventToResponsesEvents(&AnthropicStreamEvent{Type: "content_block_delta", Index: &idx, Delta: &AnthropicDelta{Type: "text_delta", Text: "before"}}, state)
						}
						AnthropicEventToResponsesEvents(&AnthropicStreamEvent{Type: "content_block_stop", Index: &idx}, state)
					}
					for _, event := range AnthropicEventToResponsesEvents(&AnthropicStreamEvent{Type: "message_stop"}, state) {
						if event.Type == "response.completed" {
							out = event.Response
							completed = true
						}
					}
					require.True(t, completed)
				}
				require.Len(t, out.Output, 3)
				require.Equal(t, "message", out.Output[0].Type)
				require.Equal(t, "reasoning", out.Output[1].Type)
				require.Equal(t, "function_call", out.Output[2].Type)
				input, err := json.Marshal(out.Output)
				require.NoError(t, err)
				var history []json.RawMessage
				require.NoError(t, json.Unmarshal(input, &history))
				history = append(history, json.RawMessage(`{"type":"function_call_output","call_id":"tool_1","output":"ok"}`))
				input, err = json.Marshal(history)
				require.NoError(t, err)
				req, err := ResponsesToAnthropicRequest(&ResponsesRequest{Model: model, Input: input, Reasoning: &ResponsesReasoning{Effort: "xhigh"}})
				require.NoError(t, err)
				require.Equal(t, "adaptive", req.Thinking.Type)
				require.Equal(t, "xhigh", req.OutputConfig.Effort)
				var blocks []AnthropicContentBlock
				require.NoError(t, json.Unmarshal(req.Messages[0].Content, &blocks))
				require.Len(t, blocks, 3)
				require.Equal(t, "before", blocks[0].Text)
				require.Equal(t, "signed-by-provider", blocks[1].Signature)
				require.JSONEq(t, `{"q":"value"}`, string(blocks[2].Input))
			}
		})
	}
}

func TestUpstreamCompatAnthropicSystemRoleOnDirectChat(t *testing.T) {
	got, err := AnthropicToChatCompletionsRequest(&AnthropicRequest{
		Model: "gpt-5.2", Messages: []AnthropicMessage{
			{Role: "user", Content: json.RawMessage(`"hello"`)},
			{Role: "system", Content: json.RawMessage(`"hook instructions"`)},
		},
	})
	require.NoError(t, err)
	require.Len(t, got.Messages, 2)
	require.Equal(t, "system", got.Messages[1].Role)
}

func TestUpstreamCompatThinkingSignatureFieldAlwaysPresent(t *testing.T) {
	body, err := json.Marshal(AnthropicContentBlock{Type: "thinking"})
	require.NoError(t, err)
	require.JSONEq(t, `{"type":"thinking","thinking":"","signature":""}`, string(body))
}
