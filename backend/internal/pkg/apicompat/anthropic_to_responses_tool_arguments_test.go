package apicompat

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAnthropicToolArgumentsPreservedAcrossStreamShapes(t *testing.T) {
	for _, tc := range []struct {
		name   string
		input  json.RawMessage
		deltas []string
		want   string
	}{
		{name: "streamed", deltas: []string{`{"path":`, `".","pattern":"TODO"}`}, want: `{"path":".","pattern":"TODO"}`},
		{name: "inline on start", input: json.RawMessage(`{"path":".","pattern":"TODO"}`), want: `{"path":".","pattern":"TODO"}`},
		{name: "deltas override inline seed", input: json.RawMessage(`{"stale":true}`), deltas: []string{`{"fresh":`, `true}`}, want: `{"fresh":true}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			state := NewAnthropicEventToResponsesState()
			idx := 0
			var events []ResponsesStreamEvent
			feed := func(event *AnthropicStreamEvent) {
				events = append(events, AnthropicEventToResponsesEvents(event, state)...)
			}

			feed(&AnthropicStreamEvent{Type: "message_start", Message: &AnthropicResponse{ID: "msg_1"}})
			feed(&AnthropicStreamEvent{Type: "content_block_start", Index: &idx, ContentBlock: &AnthropicContentBlock{
				Type: "tool_use", ID: "toolu_1", Name: "grep_search", Input: tc.input,
			}})
			for _, delta := range tc.deltas {
				feed(&AnthropicStreamEvent{Type: "content_block_delta", Index: &idx, Delta: &AnthropicDelta{
					Type: "input_json_delta", PartialJSON: delta,
				}})
			}
			feed(&AnthropicStreamEvent{Type: "content_block_stop", Index: &idx})

			var streamed, done string
			var outputDone *ResponsesOutput
			for i := range events {
				switch events[i].Type {
				case "response.function_call_arguments.delta":
					streamed += events[i].Delta
				case "response.function_call_arguments.done":
					done = events[i].Arguments
				case "response.output_item.done":
					if events[i].Item != nil && events[i].Item.Type == "function_call" {
						outputDone = events[i].Item
					}
				}
			}

			require.Equal(t, tc.want, streamed)
			require.Equal(t, tc.want, done)
			require.NotNil(t, outputDone)
			require.Equal(t, tc.want, outputDone.Arguments)
		})
	}
}
