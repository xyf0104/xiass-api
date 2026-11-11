package service

import "fmt"

// Only fixed protocol enums enter diagnostics. Unknown upstream strings can
// contain credentials or generated content and must never be retained here.
type pelicanCompletion struct {
	protocol string
	reason   string
	terminal bool
	cause    string
}

func (d *pelicanCompletion) setReason(reason string) {
	d.reason = "unknown"
	switch d.protocol {
	case "claude":
		switch reason {
		case "end_turn", "max_tokens", "stop_sequence", "tool_use", "pause_turn", "refusal", "model_context_window_exceeded":
			d.reason = reason
		}
	case "gemini":
		switch reason {
		case "STOP", "MAX_TOKENS", "SAFETY", "RECITATION", "OTHER", "BLOCKLIST", "PROHIBITED_CONTENT", "SPII", "MALFORMED_FUNCTION_CALL", "UNEXPECTED_TOOL_CALL":
			d.reason = reason
		}
	case "chat":
		switch reason {
		case "stop", "length", "content_filter", "tool_calls", "function_call":
			d.reason = reason
		}
	case "responses":
		switch reason {
		case "completed", "incomplete", "failed", "max_output_tokens", "content_filter":
			d.reason = reason
		}
	}
	if reason == "" {
		d.reason = "missing"
	}
}

func (d *pelicanCompletion) stopCause() string {
	switch d.reason {
	case "max_tokens", "MAX_TOKENS", "length", "max_output_tokens", "model_context_window_exceeded":
		return "output_limit"
	case "", "missing":
		return "stop_reason_missing"
	default:
		return "unsuccessful_stop"
	}
}

func (d *pelicanCompletion) message(cause string) string {
	field := "status"
	switch d.protocol {
	case "claude":
		field = "stop_reason"
	case "gemini":
		field = "finishReason"
	case "chat":
		field = "finish_reason"
	}
	reason := d.reason
	if reason == "" {
		reason = "missing"
	}
	// Missing means not observed before the parser returned. An unsuccessful
	// stop reason still fails immediately, without waiting for unread events.
	terminal := "missing"
	if d.terminal {
		terminal = "received"
	}
	return fmt.Sprintf("cause=%s; protocol=%s; %s=%s; terminal=%s", cause, d.protocol, field, reason, terminal)
}

func (p *openAIPelicanProbe) recordCompletionError() {
	if p.completion == nil || p.errorCode == "" || p.errorMessage != "" {
		return
	}
	cause := p.completion.cause
	if cause == "" {
		cause = "terminal_missing"
		if p.errorCode == "upstream_stream_error" {
			cause = "stream_error"
		}
	}
	p.errorMessage = p.completion.message(cause)
}
