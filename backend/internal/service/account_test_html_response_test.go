//go:build unit

package service

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"testing/iotest"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestAccountTestService_ClaudeAPIKeyRejectsHTMLResponse(t *testing.T) {
	for _, contentType := range []string{
		"text/html; charset=utf-8",
		"TEXT/HTML; charset=UTF-8",
		"application/xhtml+xml",
		"text/event-stream",
		"text/plain",
		"",
	} {
		t.Run(contentType, func(t *testing.T) {
			account := &Account{
				ID: 1, Platform: PlatformAnthropic, Type: AccountTypeAPIKey, Concurrency: 1,
				Credentials: map[string]any{
					"api_key":  "test-only-key",
					"base_url": "https://claude.example.invalid//v1/message",
				},
			}
			response := newJSONResponse(http.StatusOK, "\ufeff  <!doctype html><html><body>private-page-marker</body></html>")
			response.Header.Set("Content-Type", contentType)
			upstream := &queuedHTTPUpstream{responses: []*http.Response{response}}
			svc := &AccountTestService{
				accountRepo:  &mockAccountRepoForGemini{accountsByID: map[int64]*Account{account.ID: account}},
				httpUpstream: upstream,
				cfg:          &config.Config{},
			}
			ctx, recorder := newTestContext()

			err := svc.TestAccountConnection(ctx, account.ID, "", "", AccountTestModeDefault)

			require.ErrorContains(t, err, "HTML")
			require.ErrorContains(t, err, "Base URL")
			require.Len(t, upstream.requests, 1)
			require.Equal(t, "//v1/message/v1/messages", upstream.requests[0].URL.Path)
			require.Equal(t, "beta=true", upstream.requests[0].URL.RawQuery)
			require.Contains(t, recorder.Body.String(), `"type":"error"`)
			require.NotContains(t, recorder.Body.String(), `"type":"test_complete"`)
			require.NotContains(t, recorder.Body.String(), "private-page-marker")
			require.NotContains(t, err.Error(), "private-page-marker")
		})
	}
}

func TestAccountTestService_ClaudeHTMLBackgroundTestFails(t *testing.T) {
	account := &Account{
		ID: 1, Platform: PlatformAnthropic, Type: AccountTypeAPIKey,
		Credentials: map[string]any{"api_key": "test-only-key", "base_url": "https://claude.example.invalid"},
	}
	response := newJSONResponse(http.StatusOK, "<!doctype html><html></html>")
	response.Header.Set("Content-Type", "text/html")
	svc := &AccountTestService{
		accountRepo:  &mockAccountRepoForGemini{accountsByID: map[int64]*Account{account.ID: account}},
		httpUpstream: &queuedHTTPUpstream{responses: []*http.Response{response}},
		cfg:          &config.Config{},
	}

	result, err := svc.RunTestBackground(context.Background(), account.ID, "")

	require.NoError(t, err)
	require.Equal(t, "failed", result.Status)
	require.Contains(t, result.ErrorMessage, "HTML")
	require.Contains(t, result.ErrorMessage, "Base URL")
}

func TestAccountTestService_ClaudeStreamRejectsInvalidCompletion(t *testing.T) {
	const stop = "data: {\"type\":\"message_stop\"}\n\n"
	for _, tt := range []struct {
		name string
		body string
		want string
	}{
		{name: "empty", want: "Base URL"},
		{name: "whitespace", body: " \n\r\n", want: "Base URL"},
		{name: "html", body: "<!DOCTYPE html>\n<html></html>\n", want: "HTML"},
		{name: "plain response", body: "upstream is healthy", want: "Base URL"},
		{name: "json response", body: `{"status":"ok"}`, want: "Base URL"},
		{name: "keepalive", body: ": keepalive\n\ndata: {\"type\":\"ping\"}\n\n", want: "Base URL"},
		{name: "done only", body: "data: [DONE]\n\n", want: "Base URL"},
		{name: "done without newline", body: "data: [DONE]", want: "Base URL"},
		{name: "malformed event before stop", body: "data: {invalid}\n\n" + stop, want: "Base URL"},
		{name: "start only", body: "data: {\"type\":\"message_start\",\"message\":{}}\n\n", want: "Base URL"},
		{name: "text then EOF", body: "data: {\"type\":\"content_block_delta\",\"delta\":{\"text\":\"partial\"}}\n\n", want: "Base URL"},
		{name: "text then done", body: "data: {\"type\":\"content_block_delta\",\"delta\":{\"text\":\"partial\"}}\n\ndata: [DONE]\n\n", want: "Base URL"},
		{name: "error", body: "data: {\"type\":\"error\",\"error\":{\"message\":\"overloaded\"}}\n\n" + stop, want: "overloaded"},
		{name: "error without newline", body: `data: {"type":"error","error":{"message":"overloaded"}}`, want: "overloaded"},
		{name: "error without type", body: "data: {\"error\":{\"message\":\"overloaded\"}}\n\n" + stop, want: "overloaded"},
		{name: "named error", body: "event: error\ndata: {}\n\n" + stop, want: "error"},
		{name: "named malformed error", body: "event: error\ndata: {invalid}\n\n" + stop, want: "Base URL"},
		{name: "error on stop", body: "data: {\"type\":\"message_stop\",\"error\":{\"message\":\"overloaded\"}}\n\n", want: "overloaded"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ctx, recorder := newTestContext()
			err := (&AccountTestService{}).processClaudeStream(ctx, strings.NewReader(tt.body))

			require.ErrorContains(t, err, tt.want)
			require.Equal(t, 1, strings.Count(recorder.Body.String(), `"type":"error"`))
			require.NotContains(t, recorder.Body.String(), `"type":"test_complete"`)
			require.NotContains(t, recorder.Body.String(), `"success":true`)
		})
	}
}

func TestAccountTestService_ClaudeStreamAcceptsValidCompletion(t *testing.T) {
	const start = "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"type\":\"message\",\"role\":\"assistant\",\"content\":[]}}\n\n"
	const stop = "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"
	for _, tt := range []struct {
		name string
		body string
		text string
	}{
		{name: "text", body: start + "data: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"hello\"}}\n\n" + stop, text: "hello"},
		{name: "stop without newline", body: start + `data:{"type":"message_stop"}`},
		{name: "crlf", body: strings.ReplaceAll(start+stop, "\n", "\r\n")},
		{name: "image without text", body: start + "data: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"image\",\"source\":{\"type\":\"base64\",\"media_type\":\"image/png\",\"data\":\"test-image\"}}}\n\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n" + stop},
		{name: "tool without text", body: start + "data: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"tool_use\",\"id\":\"tool-test\",\"name\":\"lookup\",\"input\":{}}}\n\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"{}\"}}\n\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"tool_use\"}}\n\n" + stop},
		{name: "unknown event before stop", body: start + "event: future_event\ndata: {\"type\":\"future_event\"}\n\n" + stop},
		{name: "done after stop", body: start + stop + "data: [DONE]\n\n"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ctx, recorder := newTestContext()
			err := (&AccountTestService{}).processClaudeStream(ctx, strings.NewReader(tt.body))

			require.NoError(t, err)
			require.Equal(t, 1, strings.Count(recorder.Body.String(), `"type":"test_complete"`))
			require.Contains(t, recorder.Body.String(), `"success":true`)
			require.NotContains(t, recorder.Body.String(), `"type":"error"`)
			text, errMsg := parseTestSSEOutput(recorder.Body.String())
			require.Equal(t, tt.text, text)
			require.Empty(t, errMsg)
		})
	}
}

func TestAccountTestService_ClaudeStreamReadError(t *testing.T) {
	ctx, recorder := newTestContext()
	err := (&AccountTestService{}).processClaudeStream(ctx, iotest.ErrReader(io.ErrUnexpectedEOF))

	require.ErrorContains(t, err, "Stream read error")
	require.Contains(t, recorder.Body.String(), `"type":"error"`)
	require.NotContains(t, recorder.Body.String(), `"type":"test_complete"`)
}
