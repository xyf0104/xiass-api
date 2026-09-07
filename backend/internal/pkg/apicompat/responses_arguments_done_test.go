package apicompat

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func argumentDoneEvent(kind, value string, index int) *ResponsesStreamEvent {
	event := &ResponsesStreamEvent{OutputIndex: index, Type: "response.function_call_arguments.done", Arguments: value}
	if kind == "custom_tool_call" {
		event.Type, event.Input = "response.custom_tool_call_input.done", value
	}
	return event
}

func TestResponsesArgumentsDone_StreamCompletion(t *testing.T) {
	for _, kind := range []string{"function_call", "custom_tool_call"} {
		for _, tc := range []struct{ name, delta, done, want string }{
			{"no deltas", "", `{"id":1}`, `{"id":1}`},
			{"partial deltas", `{"id":`, `{"id":1}`, `{"id":1}`},
			{"complete deltas", `{"id":1}`, `{"id":1}`, `{"id":1}`},
			{"conflicting done cannot rewrite streamed bytes", `{"id":2}`, `{"id":1}`, `{"id":2}`},
			{"empty done", "partial", "", "partial"},
			{"unicode suffix", "你好", "你好世界", "你好世界"},
		} {
			t.Run(kind+"/"+tc.name, func(t *testing.T) {
				state := NewResponsesEventToChatState()
				state.SentRole = true
				ResponsesEventToChatChunks(&ResponsesStreamEvent{Type: "response.output_item.added", OutputIndex: 3,
					Item: &ResponsesOutput{Type: kind, CallID: "call_one", Name: "lookup"}}, state)
				var received string
				appendChunks := func(event *ResponsesStreamEvent) {
					for _, chunk := range ResponsesEventToChatChunks(event, state) {
						for _, call := range chunk.Choices[0].Delta.ToolCalls {
							require.Equal(t, 0, *call.Index)
							received += call.Function.Arguments
						}
					}
				}
				deltaType := "response.function_call_arguments.delta"
				if kind == "custom_tool_call" {
					deltaType = "response.custom_tool_call_input.delta"
				}
				appendChunks(&ResponsesStreamEvent{Type: deltaType, OutputIndex: 3, Delta: tc.delta})
				appendChunks(argumentDoneEvent(kind, tc.done, 3))
				appendChunks(argumentDoneEvent(kind, tc.done, 3))
				require.Equal(t, tc.want, received)
				require.Empty(t, ResponsesEventToChatChunks(argumentDoneEvent(kind, "unknown", 99), state))
			})
		}
	}
}

func TestResponsesArgumentsDone_BufferedCompletion(t *testing.T) {
	for _, kind := range []string{"function_call", "custom_tool_call"} {
		for _, delta := range []string{"", `{"id":`, `{"id":1}`, `{"id":2}`} {
			t.Run(kind+"/"+delta, func(t *testing.T) {
				acc := NewBufferedResponseAccumulator()
				acc.ProcessEvent(&ResponsesStreamEvent{Type: "response.output_item.added", OutputIndex: 3,
					Item: &ResponsesOutput{Type: kind, CallID: "call_one", Name: "lookup"}})
				acc.ProcessEvent(&ResponsesStreamEvent{Type: "response.function_call_arguments.delta", OutputIndex: 3, Delta: delta})
				acc.ProcessEvent(argumentDoneEvent(kind, `{"id":1}`, 3))
				acc.ProcessEvent(argumentDoneEvent(kind, `{"id":1}`, 3))
				acc.ProcessEvent(argumentDoneEvent(kind, "other", 99))
				output := acc.BuildOutput()
				require.Len(t, output, 1)
				require.Equal(t, `{"id":1}`, output[0].Arguments)
				resp := &ResponsesResponse{Output: []ResponsesOutput{{Type: kind, CallID: "call_one", Name: "lookup"}}}
				acc.SupplementResponseOutput(resp)
				acc.SupplementResponseOutput(resp)
				if kind == "custom_tool_call" {
					require.Equal(t, `{"id":1}`, resp.Output[0].Input)
				} else {
					require.Equal(t, `{"id":1}`, resp.Output[0].Arguments)
				}
			})
		}
	}
}

func TestResponsesArgumentsDone_ParallelCallsMatchIdentityNotTerminalPosition(t *testing.T) {
	acc := NewBufferedResponseAccumulator()
	state := NewResponsesEventToChatState()
	for i, name := range []string{"first", "second"} {
		added := &ResponsesStreamEvent{Type: "response.output_item.added", OutputIndex: i,
			Item: &ResponsesOutput{Type: "function_call", CallID: name, Name: "lookup"}}
		acc.ProcessEvent(added)
		ResponsesEventToChatChunks(added, state)
		done := argumentDoneEvent("function_call", name, i)
		acc.ProcessEvent(done)
		chunks := ResponsesEventToChatChunks(done, state)
		require.Equal(t, i, *chunks[0].Choices[0].Delta.ToolCalls[0].Index)
		require.Equal(t, name, chunks[0].Choices[0].Delta.ToolCalls[0].Function.Arguments)
	}
	resp := &ResponsesResponse{Output: []ResponsesOutput{
		{Type: "function_call", CallID: "second"},
		{Type: "function_call", CallID: "first", Arguments: "terminal-authoritative"},
		{Type: "function_call", CallID: "not-seen"},
	}}
	acc.SupplementResponseOutput(resp)
	require.Equal(t, "second", resp.Output[0].Arguments)
	require.Equal(t, "terminal-authoritative", resp.Output[1].Arguments)
	require.Empty(t, resp.Output[2].Arguments)
	acc.SupplementResponseOutput(nil)
}
