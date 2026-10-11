package service

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	coderws "github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestOpenAIWSHTTPBridgeContextWindowRolloverResetsReplayHistory(t *testing.T) {
	gin.SetMode(gin.TestMode)

	completed := func(responseID string) *http.Response {
		body := "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"" + responseID + "\",\"model\":\"gpt-5.1\",\"output\":[],\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}}\n\n"
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body:       io.NopCloser(strings.NewReader(body)),
		}
	}
	upstream := &httpUpstreamRecorder{responses: []*http.Response{
		completed("resp_window_a_1"),
		completed("resp_window_a_2"),
		completed("resp_window_b_1"),
	}}
	cfg := &config.Config{}
	cfg.Security.URLAllowlist.Enabled = false
	cfg.Security.URLAllowlist.AllowInsecureHTTP = true
	cfg.Gateway.OpenAIWS.Enabled = true
	cfg.Gateway.OpenAIWS.OAuthEnabled = true
	cfg.Gateway.OpenAIWS.ResponsesWebsocketsV2 = true
	cfg.Gateway.OpenAIWS.HTTPBridgeEnabled = true
	cfg.Gateway.OpenAIWS.HTTPBridgeThresholdBytes = 1
	cfg.Gateway.OpenAIWS.ReadTimeoutSeconds = 3
	cfg.Gateway.OpenAIWS.WriteTimeoutSeconds = 3

	svc := &OpenAIGatewayService{
		cfg: cfg, httpUpstream: upstream, cache: &stubGatewayCache{},
		openaiWSResolver: NewOpenAIWSProtocolResolver(cfg), toolCorrector: NewCodexToolCorrector(),
	}
	account := &Account{
		ID: 9401, Name: "oauth-context-window-rollover", Platform: PlatformOpenAI, Type: AccountTypeOAuth,
		Credentials: map[string]any{"access_token": "test-token"},
		Extra: map[string]any{
			"responses_websockets_v2_enabled":           true,
			"openai_oauth_responses_websockets_v2_mode": OpenAIWSIngressModeHTTPBridge,
		},
		Concurrency: 1, Status: StatusActive, Schedulable: true,
	}

	errCh := make(chan error, 1)
	wsServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := coderws.Accept(w, r, nil)
		if err != nil {
			errCh <- err
			return
		}
		defer func() { _ = conn.CloseNow() }()
		readCtx, cancelRead := context.WithTimeout(r.Context(), 3*time.Second)
		_, firstMessage, err := conn.Read(readCtx)
		cancelRead()
		if err != nil {
			errCh <- err
			return
		}
		recorder := httptest.NewRecorder()
		ginCtx, _ := gin.CreateTestContext(recorder)
		ginCtx.Request = r.Clone(r.Context())
		errCh <- svc.ProxyResponsesWebSocketFromClient(r.Context(), ginCtx, conn, account, "test-token", firstMessage, nil)
	}))
	defer wsServer.Close()

	dialCtx, cancelDial := context.WithTimeout(context.Background(), 3*time.Second)
	clientConn, _, err := coderws.Dial(dialCtx, "ws"+strings.TrimPrefix(wsServer.URL, "http"), nil)
	cancelDial()
	require.NoError(t, err)
	defer func() { _ = clientConn.CloseNow() }()

	writeAndRead := func(payload string) {
		writeCtx, cancelWrite := context.WithTimeout(context.Background(), 3*time.Second)
		require.NoError(t, clientConn.Write(writeCtx, coderws.MessageText, []byte(payload)))
		cancelWrite()
		readCtx, cancelRead := context.WithTimeout(context.Background(), 3*time.Second)
		_, event, readErr := clientConn.Read(readCtx)
		cancelRead()
		require.NoError(t, readErr)
		require.Equal(t, "response.completed", gjson.GetBytes(event, "type").String())
	}

	writeAndRead(`{"type":"response.create","model":"gpt-5.1","client_metadata":{"x-codex-window-id":"window-a"},"input":"window-a-root"}`)
	writeAndRead(`{"type":"response.create","model":"gpt-5.1","previous_response_id":"resp_window_a_1","input":"window-a-without-metadata"}`)
	writeAndRead(`{"type":"response.create","model":"gpt-5.1","previous_response_id":"resp_window_a_2","client_metadata":{"x-codex-window-id":"window-b"},"input":"window-b-root"}`)

	require.NoError(t, clientConn.Close(coderws.StatusNormalClosure, "done"))
	select {
	case proxyErr := <-errCh:
		require.NoError(t, proxyErr)
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for websocket bridge proxy to finish")
	}

	require.Len(t, upstream.bodies, 3)
	secondInput := gjson.GetBytes(upstream.bodies[1], "input").Array()
	require.Len(t, secondInput, 2, "missing window metadata must preserve the existing replay chain")
	require.Equal(t, "window-a-root", secondInput[0].String())
	require.Equal(t, "window-a-without-metadata", secondInput[1].String())

	require.Equal(t, "window-b-root", gjson.GetBytes(upstream.bodies[2], "input").String())
	require.NotContains(t, string(upstream.bodies[2]), "window-a-root")
	require.NotContains(t, string(upstream.bodies[2]), "window-a-without-metadata")
	require.False(t, gjson.GetBytes(upstream.bodies[2], "previous_response_id").Exists())
}
