package service

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

var errOpenAIReadPastTerminal = errors.New("read attempted after semantic terminal")

type finiteOpenAIStreamReader struct {
	payload []byte
	reads   int
}

func (r *finiteOpenAIStreamReader) Read(p []byte) (int, error) {
	r.reads++
	if r.reads == 1 {
		return copy(p, r.payload), nil
	}
	return 0, errOpenAIReadPastTerminal
}

func (r *finiteOpenAIStreamReader) Close() error { return nil }

type stalledEOFOpenAIStreamReader struct {
	payload []byte
	sent    bool
	release chan struct{}
	once    sync.Once
}

type disconnectedOpenAIStreamWriter struct {
	gin.ResponseWriter
}

func (w *disconnectedOpenAIStreamWriter) Write([]byte) (int, error) {
	return 0, errors.New("client disconnected")
}

func (w *disconnectedOpenAIStreamWriter) WriteString(string) (int, error) {
	return 0, errors.New("client disconnected")
}

func (r *stalledEOFOpenAIStreamReader) Read(p []byte) (int, error) {
	if !r.sent {
		r.sent = true
		return copy(p, r.payload), nil
	}
	<-r.release
	return 0, io.EOF
}

func (r *stalledEOFOpenAIStreamReader) Close() error {
	r.once.Do(func() { close(r.release) })
	return nil
}

func newOpenAIStreamLifecycleContext(t *testing.T) (*OpenAIGatewayService, *gin.Context) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	return &OpenAIGatewayService{cfg: &config.Config{Gateway: config.GatewayConfig{
		MaxLineSize: defaultMaxLineSize,
	}}}, c
}

func TestOpenAIStreamingPassthroughStopsReadingAtSemanticTerminal(t *testing.T) {
	terminal := []byte(`data: {"type":"response.completed","response":{"id":"resp_finite","output":[{"type":"message"}],"usage":{"input_tokens":7,"output_tokens":3}}}` + "\n\n")
	reader := &finiteOpenAIStreamReader{payload: terminal}
	svc, c := newOpenAIStreamLifecycleContext(t)
	resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: reader}

	result, err := svc.handleStreamingResponsePassthrough(
		c.Request.Context(), resp, c, &Account{ID: 1, Platform: PlatformOpenAI}, time.Now(), "", "",
	)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, 7, result.usage.InputTokens)
	require.Equal(t, 3, result.usage.OutputTokens)
	require.Equal(t, 1, reader.reads, "handler must not probe for EOF after a complete terminal frame")
}

func TestOpenAIStreamingReturnsWhenEOFAfterTerminalStalls(t *testing.T) {
	terminal := []byte(`data: {"type":"response.completed","response":{"id":"resp_stalled","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"done"}]}],"usage":{"input_tokens":11,"output_tokens":5}}}` + "\n\n")
	for _, tc := range []struct {
		name      string
		configure func(*config.GatewayConfig)
	}{
		{
			name: "keepalive enabled",
			configure: func(cfg *config.GatewayConfig) {
				cfg.StreamKeepaliveInterval = 1
			},
		},
		{
			name: "first output timeout enabled",
			configure: func(cfg *config.GatewayConfig) {
				cfg.OpenAIFirstOutputTimeoutSeconds = 30
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reader := &stalledEOFOpenAIStreamReader{payload: terminal, release: make(chan struct{})}
			t.Cleanup(func() { _ = reader.Close() })
			svc, c := newOpenAIStreamLifecycleContext(t)
			tc.configure(&svc.cfg.Gateway)
			resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: reader}

			type streamResult struct {
				result *openaiStreamingResult
				err    error
			}
			done := make(chan streamResult, 1)
			go func() {
				result, err := svc.handleStreamingResponse(
					c.Request.Context(), resp, c, &Account{ID: 1, Platform: PlatformOpenAI}, time.Now(), "model", "model",
				)
				done <- streamResult{result: result, err: err}
			}()

			select {
			case got := <-done:
				require.NoError(t, got.err)
				require.NotNil(t, got.result)
				require.Equal(t, 11, got.result.usage.InputTokens)
				require.Equal(t, 5, got.result.usage.OutputTokens)
			case <-time.After(2 * time.Second):
				_ = reader.Close()
				got := <-done
				require.NoError(t, got.err)
				t.Fatal("stream waited for upstream EOF after a complete terminal frame")
			}
		})
	}
}

func TestOpenAIStreamingPassthroughDisconnectedWriterStopsAfterTerminalUsage(t *testing.T) {
	payload := []byte(
		`data: {"type":"response.output_text.delta","delta":"partial"}` + "\n\n" +
			`data: {"type":"response.completed","response":{"id":"resp_disconnected","usage":{"input_tokens":13,"output_tokens":8}}}` + "\n\n",
	)
	reader := &stalledEOFOpenAIStreamReader{payload: payload, release: make(chan struct{})}
	t.Cleanup(func() { _ = reader.Close() })
	svc, c := newOpenAIStreamLifecycleContext(t)
	c.Writer = &disconnectedOpenAIStreamWriter{ResponseWriter: c.Writer}
	resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: reader}

	type passthroughResult struct {
		result *openaiStreamingResultPassthrough
		err    error
	}
	done := make(chan passthroughResult, 1)
	go func() {
		result, err := svc.handleStreamingResponsePassthrough(
			c.Request.Context(), resp, c, &Account{ID: 1, Platform: PlatformOpenAI}, time.Now(), "", "",
		)
		done <- passthroughResult{result: result, err: err}
	}()

	select {
	case got := <-done:
		require.NoError(t, got.err)
		require.NotNil(t, got.result)
		require.Equal(t, 13, got.result.usage.InputTokens)
		require.Equal(t, 8, got.result.usage.OutputTokens)
	case <-time.After(2 * time.Second):
		_ = reader.Close()
		got := <-done
		require.NoError(t, got.err)
		t.Fatal("disconnected passthrough stream waited for EOF after terminal usage")
	}
}

func TestOpenAIStreamSemanticStatusHonorsErrorStatus(t *testing.T) {
	for _, tc := range []struct {
		name         string
		payload      string
		wantStatus   int
		wantAuth     bool
		wantFailover bool
	}{
		{
			name:         "response failed unauthorized",
			payload:      `{"type":"response.failed","response":{"error":{"code":"server_error","status":401,"message":"credential rejected"}}}`,
			wantStatus:   http.StatusUnauthorized,
			wantAuth:     true,
			wantFailover: true,
		},
		{
			name:         "bare error rate limited",
			payload:      `{"type":"error","error":{"code":"server_error","status":429,"message":"slow down"}}`,
			wantStatus:   http.StatusTooManyRequests,
			wantFailover: true,
		},
		{
			name:         "relay overload",
			payload:      `{"type":"error","error":{"code":"server_error","status":529,"message":"overloaded"}}`,
			wantStatus:   529,
			wantFailover: true,
		},
		{
			name:       "forbidden without account signal",
			payload:    `{"type":"response.failed","response":{"error":{"code":"server_error","status":403,"message":"request rejected"}}}`,
			wantStatus: http.StatusForbidden,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			payload := []byte(tc.payload)
			message := extractOpenAISSEErrorMessage(payload)
			require.Equal(t, tc.wantStatus, openAIStreamFailureStatus(payload, message))
			require.Equal(t, tc.wantAuth, openAIStreamCredentialAuthFailure(payload))
			if gjson.GetBytes(payload, "type").String() == "error" {
				require.Equal(t, tc.wantFailover, openAIStreamErrorEventShouldFailover(payload, message))
			} else {
				require.Equal(t, tc.wantFailover, openAIStreamFailedEventShouldFailover(payload, message))
			}
		})
	}

	payload := []byte(`{"type":"response.failed","response":{"error":{"code":"server_error","status":500,"message":"temporary failure"}}}`)
	require.Equal(t, http.StatusBadGateway, openAIStreamFailureStatus(payload, extractOpenAISSEErrorMessage(payload)))
	require.False(t, openAIStreamCredentialAuthFailure(payload))
	require.True(t, openAIStreamFailedEventShouldFailover(payload, extractOpenAISSEErrorMessage(payload)))
}
