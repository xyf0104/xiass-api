package apicompat

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestServiceTierPassthroughNonStreaming(t *testing.T) {
	responses := &ResponsesResponse{
		ID: "resp_1", Model: "gpt-6.1-sol", Status: "completed", ServiceTier: "priority",
		Output: []ResponsesOutput{{Type: "message", Role: "assistant", Content: []ResponsesContentPart{{Type: "output_text", Text: "ok"}}}},
	}
	chat := ResponsesToChatCompletions(responses, responses.Model)
	require.Equal(t, "priority", chat.ServiceTier)

	back := ChatCompletionsResponseToResponses(&ChatCompletionsResponse{
		ID: "chat_1", Model: responses.Model, ServiceTier: "flex",
		Choices: []ChatChoice{{Message: ChatMessage{Role: "assistant", Content: json.RawMessage(`"ok"`)}}},
	}, responses.Model, nil, false, nil)
	require.Equal(t, "flex", back.ServiceTier)
}

func TestServiceTierPassthroughStreaming(t *testing.T) {
	toChat := NewResponsesEventToChatState()
	chunks := ResponsesEventToChatChunks(&ResponsesStreamEvent{
		Type:     "response.created",
		Response: &ResponsesResponse{ID: "resp_1", Model: "gpt-6.1-sol", ServiceTier: "priority"},
	}, toChat)
	require.NotEmpty(t, chunks)
	require.Equal(t, "priority", chunks[0].ServiceTier)

	toResponses := NewChatCompletionsToResponsesStreamState("gpt-6.1-sol")
	events := ChatCompletionsChunkToResponsesEvents(&ChatCompletionsChunk{
		ID: "chat_1", Model: "gpt-6.1-sol", ServiceTier: "flex",
		Choices: []ChatChunkChoice{{Index: 0, Delta: ChatDelta{Content: stringPointer("ok")}}},
	}, toResponses)
	created := protocolCompatEvent(events, "response.created")
	require.NotNil(t, created)
	require.NotNil(t, created.Response)
	require.Equal(t, "flex", created.Response.ServiceTier)

	completed := protocolCompatEvent(FinalizeChatCompletionsResponsesStream(toResponses), "response.completed")
	require.NotNil(t, completed)
	require.Equal(t, "flex", completed.Response.ServiceTier)
}

func stringPointer(value string) *string { return &value }

func protocolCompatEvent(events []ResponsesStreamEvent, eventType string) *ResponsesStreamEvent {
	for i := range events {
		if events[i].Type == eventType {
			return &events[i]
		}
	}
	return nil
}
