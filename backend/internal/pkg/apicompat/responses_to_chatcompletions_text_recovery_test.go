package apicompat

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSupplementResponseOutputRecoversStreamedText(t *testing.T) {
	for _, tc := range []struct {
		name      string
		output    []ResponsesOutput
		wantItems int
		wantText  string
	}{
		{
			name:      "empty message",
			output:    []ResponsesOutput{{Type: "message", Role: "assistant"}},
			wantItems: 1,
			wantText:  "streamed text",
		},
		{
			name:      "blank output text",
			output:    []ResponsesOutput{{Type: "message", Content: []ResponsesContentPart{{Type: "output_text", Text: "  "}}}},
			wantItems: 1,
			wantText:  "streamed text",
		},
		{
			name:      "tool only terminal output",
			output:    []ResponsesOutput{{Type: "function_call", CallID: "call_1", Name: "lookup", Arguments: `{}`}},
			wantItems: 2,
			wantText:  "streamed text",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			acc := NewBufferedResponseAccumulator()
			acc.ProcessEvent(&ResponsesStreamEvent{Type: "response.output_text.delta", Delta: "streamed "})
			acc.ProcessEvent(&ResponsesStreamEvent{Type: "response.output_text.delta", Delta: "text"})
			resp := &ResponsesResponse{Output: tc.output}

			acc.SupplementResponseOutput(resp)
			acc.SupplementResponseOutput(resp)

			require.Len(t, resp.Output, tc.wantItems)
			var got string
			for _, item := range resp.Output {
				if item.Type != "message" {
					continue
				}
				for _, part := range item.Content {
					if part.Type == "output_text" {
						got += part.Text
					}
				}
			}
			require.Equal(t, tc.wantText, got)
		})
	}
}

func TestSupplementResponseOutputKeepsTerminalTextAuthoritative(t *testing.T) {
	acc := NewBufferedResponseAccumulator()
	acc.ProcessEvent(&ResponsesStreamEvent{Type: "response.output_text.delta", Delta: "streamed"})
	resp := &ResponsesResponse{Output: []ResponsesOutput{{
		Type:    "message",
		Content: []ResponsesContentPart{{Type: "output_text", Text: "terminal"}},
	}}}

	acc.SupplementResponseOutput(resp)

	require.Equal(t, "terminal", resp.Output[0].Content[0].Text)
}

func TestSupplementResponseOutputRecoversDoneOnlyText(t *testing.T) {
	acc := NewBufferedResponseAccumulator()
	acc.ProcessEvent(&ResponsesStreamEvent{
		Type: "response.output_text.done", OutputIndex: 1, ContentIndex: 2, Text: "done-only text",
	})
	resp := &ResponsesResponse{Output: []ResponsesOutput{{Type: "message", Role: "assistant"}}}

	acc.SupplementResponseOutput(resp)

	require.Equal(t, "done-only text", resp.Output[0].Content[0].Text)
}

func TestSupplementResponseOutputDoneTextDoesNotDuplicateDeltas(t *testing.T) {
	acc := NewBufferedResponseAccumulator()
	acc.ProcessEvent(&ResponsesStreamEvent{
		Type: "response.output_text.delta", OutputIndex: 0, ContentIndex: 0, Delta: "Hello",
	})
	acc.ProcessEvent(&ResponsesStreamEvent{
		Type: "response.output_text.done", OutputIndex: 0, ContentIndex: 0, Text: "Hello",
	})
	resp := &ResponsesResponse{}

	acc.SupplementResponseOutput(resp)

	require.Equal(t, "Hello", resp.Output[0].Content[0].Text)
}

func TestSupplementResponseOutputDoneTextAddsOnlyMissingSuffix(t *testing.T) {
	acc := NewBufferedResponseAccumulator()
	acc.ProcessEvent(&ResponsesStreamEvent{
		Type: "response.output_text.delta", OutputIndex: 0, ContentIndex: 0, Delta: "Hel",
	})
	acc.ProcessEvent(&ResponsesStreamEvent{
		Type: "response.output_text.done", OutputIndex: 0, ContentIndex: 0, Text: "Hello",
	})
	resp := &ResponsesResponse{}

	acc.SupplementResponseOutput(resp)

	require.Equal(t, "Hello", resp.Output[0].Content[0].Text)
}

func TestSupplementResponseOutputRecoversMultipleDoneOnlyParts(t *testing.T) {
	acc := NewBufferedResponseAccumulator()
	acc.ProcessEvent(&ResponsesStreamEvent{
		Type: "response.output_text.done", OutputIndex: 0, ContentIndex: 0, Text: "first",
	})
	acc.ProcessEvent(&ResponsesStreamEvent{
		Type: "response.output_text.done", OutputIndex: 0, ContentIndex: 1, Text: "second",
	})
	resp := &ResponsesResponse{}

	acc.SupplementResponseOutput(resp)

	require.Equal(t, "firstsecond", resp.Output[0].Content[0].Text)
}

func TestSupplementResponseOutputKeepsDeltasWhenDoneDiverges(t *testing.T) {
	acc := NewBufferedResponseAccumulator()
	acc.ProcessEvent(&ResponsesStreamEvent{
		Type: "response.output_text.delta", OutputIndex: 0, ContentIndex: 0, Delta: "streamed",
	})
	acc.ProcessEvent(&ResponsesStreamEvent{
		Type: "response.output_text.done", OutputIndex: 0, ContentIndex: 0, Text: "different",
	})
	resp := &ResponsesResponse{}

	acc.SupplementResponseOutput(resp)

	require.Equal(t, "streamed", resp.Output[0].Content[0].Text)
}
