package service

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/benchmark"
	"github.com/stretchr/testify/require"
)

func TestPelicanPrefacedHTMLAndContinuationAcrossAdapters(t *testing.T) {
	for _, adapter := range pelicanAdapterCases() {
		for _, tc := range []struct {
			name, previous, next, want string
			incomplete                 bool
		}{
			{"complete with prose", "", "Here is the page:\n\n```html\n<html>animation</html>\n```\nSave it locally.", "<html>animation</html>", false},
			{"partial open fence", "", "Here is the page:\n```html\n<html><svg data-value=\"a ", "<html><svg data-value=\"a ", true},
			{"partial closed fence", "", "Here is the page:\n```html\n<html>unfinished\n```\nDone.", "<html>unfinished\n", true},
			{"legacy continuation", "Here is the page:\n```html\n<html>old ", "tail</html>\n```\nDone.", "<html>old tail</html>", false},
			{"fragment whitespace", "<html><svg data-value=\"a ", " b\"></svg></html>", "<html><svg data-value=\"a  b\"></svg></html>", false},
			{"fenced continuation tail", "<html>old ", "Remaining code:\n```html\n tail</html>\n```", "<html>old  tail</html>", false},
			{"replacement document", "<html>old", "Replacement:\n```html\n<html>new</html>\n```", "<html>new</html>", false},
			{"partial continuation", "Intro:\n```html\n<html>old ", "tail", "<html>old tail", true},
		} {
			t.Run(adapter.name+"/"+tc.name, func(t *testing.T) {
				ctx := context.Background()
				if tc.previous != "" {
					ctx = benchmark.WithContinuation(ctx, tc.previous)
				}
				a, u, s := pelicanAdapterFixture(adapter, tc.next)
				before, err := json.Marshal(a)
				require.NoError(t, err)
				out, err := s.RunPelicanBenchmark(ctx, a.ID, adapter.model, nil)
				if tc.incomplete {
					require.EqualError(t, err, "upstream_incomplete")
					require.Contains(t, out.ErrorMessage, "cause=html_incomplete;")
					require.Contains(t, out.ErrorMessage, "terminal=received")
				} else {
					require.NoError(t, err)
					require.Empty(t, out.ErrorMessage)
				}
				require.Equal(t, tc.want, out.HTML)
				require.Equal(t, 1, u.calls, "extraction must not generate a follow-up request")
				after, err := json.Marshal(a)
				require.NoError(t, err)
				require.JSONEq(t, string(before), string(after))
				switch adapter.wire {
				case "claude", "bedrock":
					budget := 32768
					if adapter.platform == PlatformMiniMax {
						budget = 8192
					}
					require.Equal(t, float64(budget), u.body["max_tokens"])
				case "wrapped":
					request, ok := u.body["request"].(map[string]any)
					require.True(t, ok)
					generation, ok := request["generationConfig"].(map[string]any)
					require.True(t, ok)
					require.Equal(t, float64(32768), generation["maxOutputTokens"])
				}
			})
		}
	}
}

func TestPelicanHTMLFenceBoundaries(t *testing.T) {
	for _, tc := range []struct{ name, input, want string }{
		{"unlabelled", "Intro\n```\n<html></html>\n```\nOutro", "<html></html>"},
		{"CRLF uppercase", "Intro\r\n```HTML\r\n<!DOCTYPE html>\r\n<html></html>\r\n```\r\nOutro", "<!DOCTYPE html>\r\n<html></html>"},
		{"tilde fence", "Intro\n~~~html\n<html></html>\n~~~", "<html></html>"},
		{"long fence", "Intro\n````html\n<html></html>\n`````", "<html></html>"},
		{"skip other language", "```js\nconst example = '<html></html>';\n```\n```html\n<html>real</html>\n```", "<html>real</html>"},
		{"no cherry picking", "```html\n<html>partial\n```\n```html\n<html>complete</html>\n```", "<html>partial\n"},
		{"raw document with fences", "<html><script>const example = `\n```html\ntext\n```\n`;</script></html>", "<html><script>const example = `\n```html\ntext\n```\n`;</script></html>"},
		{"no prose guessing", "explanation <html></html>", "explanation <html></html>"},
		{"inline fence is not a block", "example ```html\n<html></html>\n```", "example ```html\n<html></html>\n```"},
		{"invalid root", "Intro\n```html\n<htmljunk></html>\n```", "<htmljunk></html>\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, pelicanUnwrapHTML(tc.input))
		})
	}
}

func TestPelicanCompletionDiagnosticsAndStrictAcceptance(t *testing.T) {
	const html = "<html>private-generated-text</html>"
	const fenced = "Here is the page:\n```html\n" + html + "\n```\nDone."
	claude := pelicanAdapterCase{platform: PlatformAnthropic, typ: AccountTypeAPIKey, model: "claude-sonnet-4-5"}
	gemini := pelicanAdapterCase{platform: PlatformGemini, typ: AccountTypeAPIKey, model: "gemini-2.5-flash"}
	grok := pelicanAdapterCase{platform: PlatformGrok, typ: AccountTypeAPIKey, model: "grok-4.5"}
	antigravity := pelicanAdapterCase{platform: PlatformAntigravity, typ: AccountTypeOAuth, model: "gemini-3.7-flash-medium"}
	bedrock := pelicanAdapterCase{platform: PlatformAnthropic, typ: AccountTypeBedrock, model: "claude-sonnet-4-5"}
	claudePartial := strings.Split(pelicanClaudeResponse(fenced, "end_turn"), "\n\n")[0] + "\n\n"
	geminiPartial := pelicanGeminiResponse(fenced, "")
	responsesPartial := strings.Split(pelicanResponses(fenced), "\n\n")[0] + "\n\n"
	jsonResponse := func(reason string) string {
		body, err := json.Marshal(map[string]any{"content": []any{map[string]any{"type": "text", "text": fenced}}, "stop_reason": reason})
		require.NoError(t, err)
		return string(body)
	}
	for _, tc := range []struct {
		name, response, code, cause, reason, terminal string
		adapter                                       pelicanAdapterCase
	}{
		{"claude budget fails before message_stop", pelicanClaudeResponse(fenced, "max_tokens"), "upstream_incomplete", "output_limit", "stop_reason=max_tokens", "missing", claude},
		{"claude end_turn without message_stop", strings.Replace(pelicanClaudeResponse(fenced, "end_turn"), "data: {\"type\":\"message_stop\"}\n\n", "", 1), "upstream_incomplete", "terminal_missing", "stop_reason=end_turn", "missing", claude},
		{"claude message_stop without reason", claudePartial + "data: {\"type\":\"message_stop\"}\n\n", "upstream_incomplete", "stop_reason_missing", "stop_reason=missing", "received", claude},
		{"claude clean EOF", claudePartial, "upstream_incomplete", "terminal_missing", "stop_reason=missing", "missing", claude},
		{"claude malformed stream", claudePartial + "data: secret-not-json\n\n", "upstream_stream_error", "stream_error", "stop_reason=missing", "missing", claude},
		{"claude unknown reason is not retained", pelicanClaudeResponse(fenced, strings.Repeat("synthetic-api private-note https://private.example/ ", 200)), "upstream_incomplete", "unsuccessful_stop", "stop_reason=unknown", "missing", claude},
		{"gemini budget", pelicanGeminiResponse(fenced, "MAX_TOKENS"), "upstream_incomplete", "output_limit", "finishReason=MAX_TOKENS", "received", gemini},
		{"gemini clean EOF", geminiPartial, "upstream_incomplete", "terminal_missing", "finishReason=missing", "missing", gemini},
		{"gemini safety", pelicanGeminiResponse(fenced, "SAFETY"), "upstream_incomplete", "unsuccessful_stop", "finishReason=SAFETY", "received", gemini},
		{"gemini unknown reason is not retained", pelicanGeminiResponse(fenced, "synthetic-api private-note https://private.example/\n"), "upstream_incomplete", "unsuccessful_stop", "finishReason=unknown", "received", gemini},
		{"antigravity wrapped budget", "data: {\"response\":" + strings.TrimSpace(strings.TrimPrefix(pelicanGeminiResponse(fenced, "MAX_TOKENS"), "data:")) + "}\n\n", "upstream_incomplete", "output_limit", "finishReason=MAX_TOKENS", "received", antigravity},
		{"bedrock budget", jsonResponse("max_tokens"), "upstream_incomplete", "output_limit", "stop_reason=max_tokens", "received", bedrock},
		{"bedrock trailing data", jsonResponse("end_turn") + " {}", "upstream_incomplete", "invalid_response_trailer", "stop_reason=end_turn", "received", bedrock},
		{"grok missing completion", responsesPartial, "upstream_incomplete", "terminal_missing", "status=missing", "missing", grok},
		{"grok DONE is insufficient", responsesPartial + "data: [DONE]\n\n", "upstream_incomplete", "terminal_missing", "status=missing", "missing", grok},
		{"grok budget", responsesPartial + "data: {\"type\":\"response.incomplete\",\"response\":{\"incomplete_details\":{\"reason\":\"max_output_tokens\"}}}\n\n", "upstream_incomplete", "output_limit", "status=max_output_tokens", "received", grok},
		{"grok completed without finish_reason", pelicanResponses(fenced), "", "", "", "", grok},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, u, s := pelicanAdapterFixture(tc.adapter, "")
			u.response = tc.response
			out, err := s.RunPelicanBenchmark(context.Background(), a.ID, tc.adapter.model, nil)
			require.Equal(t, html, out.HTML)
			require.Equal(t, tc.code, out.ErrorCode)
			if tc.code == "" {
				require.NoError(t, err)
				require.Empty(t, out.ErrorMessage)
				return
			}
			require.EqualError(t, err, tc.code)
			require.Contains(t, out.ErrorMessage, "cause="+tc.cause+";")
			require.Contains(t, out.ErrorMessage, tc.reason+";")
			require.Contains(t, out.ErrorMessage, "terminal="+tc.terminal)
			require.Less(t, len(out.ErrorMessage), 200)
			for _, secret := range []string{"private-generated-text", "private reasoning", "synthetic-api", "private-note", "private.example", "secret-not-json"} {
				require.NotContains(t, out.ErrorMessage, secret)
			}
			require.Equal(t, 1, u.calls)
		})
	}
}
