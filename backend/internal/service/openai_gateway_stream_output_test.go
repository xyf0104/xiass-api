package service

import "testing"

func TestOpenAIStreamDataCountsAsFirstTokenExcludesTerminalEvents(t *testing.T) {
	tests := []struct {
		name      string
		data      string
		eventType string
		want      bool
	}{
		{name: "text delta", data: `{"type":"response.output_text.delta","delta":"ok"}`, eventType: "response.output_text.delta", want: true},
		{name: "function item", data: `{"type":"response.output_item.added"}`, eventType: "response.output_item.added", want: false},
		{name: "function arguments delta", data: `{"type":"response.function_call_arguments.delta","delta":"{}"}`, eventType: "response.function_call_arguments.delta", want: true},
		{name: "created preamble", data: `{"type":"response.created"}`, eventType: "response.created", want: false},
		{name: "completed payload", data: `{"type":"response.completed"}`, eventType: "response.completed", want: false},
		{name: "done event line", data: `{}`, eventType: "response.done", want: false},
		{name: "failed", data: `{"type":"response.failed"}`, eventType: "response.failed", want: false},
		{name: "generic error", data: `{"type":"error","error":{"message":"upstream failed"}}`, eventType: "error", want: false},
		{name: "done marker", data: "[DONE]", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := openAIStreamDataCountsAsFirstToken(tt.data, tt.eventType); got != tt.want {
				t.Fatalf("openAIStreamDataCountsAsFirstToken() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestOpenAIStreamTerminalStillStartsClientOutput(t *testing.T) {
	if !openAIStreamDataStartsClientOutput(`{"type":"response.completed"}`, "response.completed") {
		t.Fatal("terminal event must still be forwarded to the client")
	}
}
