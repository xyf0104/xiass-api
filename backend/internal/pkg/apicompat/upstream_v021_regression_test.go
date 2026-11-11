package apicompat

import (
	"encoding/base64"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

// These cases extend f2b0a32b1, 552d1ab4b and 2d6b64a18 to XIASS's
// terminal-output repair and namespace-aware SSE serializer.
func TestSealedFunctionMetadataSurvivesXIASSRepair(t *testing.T) {
	for _, marker := range []string{`null`, `[]`, `["sealed"]`, `{"format":"sealed"}`} {
		t.Run(marker, func(t *testing.T) {
			acc := NewBufferedResponseAccumulator()
			acc.ProcessEvent(&ResponsesStreamEvent{Type: "response.output_item.added", OutputIndex: 2, Item: &ResponsesOutput{
				Type: "function_call", CallID: "call_sealed", Name: "exec", Namespace: "terminal", EncryptedFunctionArgs: json.RawMessage(marker),
			}})
			acc.ProcessEvent(&ResponsesStreamEvent{Type: "response.function_call_arguments.delta", OutputIndex: 2, Delta: "ciphertext"})
			for _, output := range [][]ResponsesOutput{nil, {{Type: "function_call", CallID: "call_sealed", Name: "exec"}}} {
				resp := &ResponsesResponse{Output: output}
				acc.SupplementResponseOutput(resp)
				require.Len(t, resp.Output, 1)
				item := resp.Output[0]
				require.Equal(t, "ciphertext", item.Arguments)
				require.Equal(t, "terminal", item.Namespace)
				require.JSONEq(t, marker, string(item.EncryptedFunctionArgs))
				for _, value := range []any{item, ResponsesStreamEvent{Type: "response.output_item.done", Item: &item}} {
					raw, err := json.Marshal(value)
					require.NoError(t, err)
					require.Contains(t, string(raw), `"encrypted_function_args":`+marker)
					require.Contains(t, string(raw), `"namespace":"terminal"`)
				}
			}
			terminal := &ResponsesResponse{Output: []ResponsesOutput{{Type: "function_call", CallID: "call_sealed", Arguments: "authoritative", Namespace: "final", EncryptedFunctionArgs: json.RawMessage(`null`)}}}
			acc.SupplementResponseOutput(terminal)
			require.Equal(t, "authoritative", terminal.Output[0].Arguments)
			require.Equal(t, "final", terminal.Output[0].Namespace)
			require.Equal(t, "null", string(terminal.Output[0].EncryptedFunctionArgs))
		})
	}
}

func TestSealedMetadataFromDoneItem(t *testing.T) {
	acc := NewBufferedResponseAccumulator()
	acc.ProcessEvent(&ResponsesStreamEvent{Type: "response.output_item.added", Item: &ResponsesOutput{Type: "function_call", CallID: "call_1", Name: "exec"}})
	acc.ProcessEvent(&ResponsesStreamEvent{Type: "response.function_call_arguments.done", Arguments: "sealed-args"})
	acc.ProcessEvent(&ResponsesStreamEvent{Type: "response.output_item.done", Item: &ResponsesOutput{Type: "function_call", CallID: "other", Namespace: "wrong", EncryptedFunctionArgs: json.RawMessage(`null`)}})
	require.Empty(t, acc.BuildOutput()[0].Namespace)
	acc.ProcessEvent(&ResponsesStreamEvent{Type: "response.output_item.done", Item: &ResponsesOutput{Type: "function_call", CallID: "call_1", Namespace: "terminal", EncryptedFunctionArgs: json.RawMessage(`[]`)}})
	out := acc.BuildOutput()[0]
	require.Equal(t, "terminal", out.Namespace)
	require.Equal(t, "[]", string(out.EncryptedFunctionArgs))
}

func TestForeignCCSwitchEnvelopeKeepsToolHistory(t *testing.T) {
	envelope := "ccswitch-openai-reasoning-v1:" + base64.RawURLEncoding.EncodeToString([]byte(`{"type":"reasoning","encrypted_content":"gAAAA-foreign"}`))
	for _, signature := range []string{envelope, " \n" + envelope + "\t", "ccswitch-openai-reasoning-v1:not-base64!"} {
		blocks, err := json.Marshal([]AnthropicContentBlock{
			{Type: "thinking", Signature: signature}, {Type: "thinking", Signature: "native-ciphertext"},
			{Type: "text", Text: "Checking now."}, {Type: "tool_use", ID: "call_1", Name: "lookup", Input: json.RawMessage(`{"key":"value"}`)},
		})
		require.NoError(t, err)
		resp, err := AnthropicToResponses(&AnthropicRequest{Model: "gpt-6.1-sol", Messages: []AnthropicMessage{
			{Role: "user", Content: json.RawMessage(`"Check this"`)}, {Role: "assistant", Content: blocks},
			{Role: "user", Content: json.RawMessage(`[{"type":"tool_result","tool_use_id":"call_1","content":"found"}]`)},
		}})
		require.NoError(t, err)
		var items []ResponsesInputItem
		require.NoError(t, json.Unmarshal(resp.Input, &items))
		require.Len(t, items, 5)
		require.Equal(t, "native-ciphertext", items[1].EncryptedContent)
		require.JSONEq(t, `[{"type":"output_text","text":"Checking now."}]`, string(items[2].Content))
		require.Equal(t, "call_1", items[3].CallID)
		require.Equal(t, "call_1", items[4].CallID)
		require.Equal(t, "found", items[4].Output)
	}
}

func TestResponsesMessageRetainsAnnotationsAndRefusals(t *testing.T) {
	var item ResponsesOutput
	require.NoError(t, json.Unmarshal([]byte(`{"type":"message","content":[{"type":"output_text","text":"","annotations":[{"type":"url_citation","url":"https://example.test"}],"logprobs":[{"token":"a","logprob":-0.1}]},{"type":"refusal","refusal":"declined"}]}`), &item))
	for _, value := range []any{item, ResponsesStreamEvent{Type: "response.output_item.done", Item: &item}, ResponsesStreamEvent{Type: "response.completed", Response: &ResponsesResponse{Output: []ResponsesOutput{item}}}} {
		raw, err := json.Marshal(value)
		require.NoError(t, err)
		require.Contains(t, string(raw), `"text":""`)
		require.Contains(t, string(raw), `"url":"https://example.test"`)
		require.Contains(t, string(raw), `"logprob":-0.1`)
		require.Contains(t, string(raw), `"refusal":"declined"`)
	}
}
