package service

import (
	"context"
	"errors"
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

func TestOpenAIGatewayServiceForward_CompactionTriggerSkipsCodexImageBridge(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name          string
		bridgeEnabled bool
		body          string
		wantInjected  bool
	}{
		{
			name:          "ordinary response keeps image bridge",
			bridgeEnabled: true,
			body:          `{"model":"gpt-5.4","stream":false,"input":[{"type":"message","role":"user","content":"draw a cat"}]}`,
			wantInjected:  true,
		},
		{
			name:          "compaction trigger skips enabled image bridge",
			bridgeEnabled: true,
			body:          `{"model":"gpt-5.4","stream":false,"input":[{"type":"message","role":"user","content":"compact this conversation"},{"type":"compaction_trigger"}]}`,
		},
		{
			name:          "compaction trigger remains clean with image bridge disabled",
			bridgeEnabled: false,
			body:          `{"model":"gpt-5.4","stream":false,"input":[{"type":"message","role":"user","content":"compact this conversation"},{"type":"compaction_trigger"}]}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			upstream := &httpUpstreamRecorder{
				resp: &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": []string{"application/json"}},
					Body:       io.NopCloser(strings.NewReader(`{"id":"resp_compaction_image_bridge","model":"gpt-5.4","usage":{"input_tokens":1,"output_tokens":1}}`)),
				},
			}
			svc := newOpenAIImageGenerationControlTestService(upstream)
			svc.cfg.Gateway.CodexImageGenerationBridgeEnabled = tt.bridgeEnabled
			c, _ := newOpenAIImageGenerationControlTestContext(true, "codex_cli_rs/0.98.0")
			account := newOpenAIImageGenerationControlTestAccount()

			result, err := svc.Forward(context.Background(), c, account, []byte(tt.body))

			require.NoError(t, err)
			require.NotNil(t, result)
			require.NotNil(t, upstream.lastReq)
			assertCodexImageBridgeInjection(t, upstream.lastBody, tt.wantInjected)
			if strings.Contains(tt.body, "compaction_trigger") {
				require.Equal(t, "compaction_trigger", gjson.GetBytes(upstream.lastBody, `input.#(type=="compaction_trigger").type`).String())
			}
		})
	}
}

func TestOpenAIGatewayServiceProxyResponsesWebSocketFromClient_CompactionTriggerSkipsCodexImageBridge(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name          string
		bridgeEnabled bool
		body          string
		wantInjected  bool
	}{
		{
			name:          "ordinary response keeps image bridge",
			bridgeEnabled: true,
			body:          `{"type":"response.create","model":"gpt-5.5","stream":false,"input":[{"type":"message","role":"user","content":"draw a cat"}]}`,
			wantInjected:  true,
		},
		{
			name:          "compaction trigger skips enabled image bridge",
			bridgeEnabled: true,
			body:          `{"type":"response.create","model":"gpt-5.5","stream":false,"input":[{"type":"message","role":"user","content":"compact this conversation"},{"type":"compaction_trigger"}]}`,
		},
		{
			name:          "compaction trigger remains clean with image bridge disabled",
			bridgeEnabled: false,
			body:          `{"type":"response.create","model":"gpt-5.5","stream":false,"input":[{"type":"message","role":"user","content":"compact this conversation"},{"type":"compaction_trigger"}]}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			upstreamBody := runCodexImageBridgeWSTurn(t, tt.bridgeEnabled, []byte(tt.body))

			assertCodexImageBridgeInjection(t, upstreamBody, tt.wantInjected)
			if strings.Contains(tt.body, "compaction_trigger") {
				require.Equal(t, "compaction_trigger", gjson.GetBytes(upstreamBody, `input.#(type=="compaction_trigger").type`).String())
			}
		})
	}
}

func assertCodexImageBridgeInjection(t *testing.T, body []byte, wantInjected bool) {
	t.Helper()

	hasImageTool := gjson.GetBytes(body, `tools.#(type=="image_generation")`).Exists()
	require.Equal(t, wantInjected, hasImageTool)
	toolChoice := gjson.GetBytes(body, "tool_choice")
	require.Equal(t, wantInjected, toolChoice.Exists())
	instructions := gjson.GetBytes(body, "instructions").String()
	require.Equal(t, wantInjected, strings.Contains(instructions, codexImageGenerationBridgeMarker))
	if wantInjected {
		require.Equal(t, "auto", toolChoice.String())
	}
}

func runCodexImageBridgeWSTurn(t *testing.T, bridgeEnabled bool, firstMessage []byte) []byte {
	t.Helper()

	cfg := &config.Config{}
	cfg.Security.URLAllowlist.Enabled = false
	cfg.Security.URLAllowlist.AllowInsecureHTTP = true
	cfg.Gateway.CodexImageGenerationBridgeEnabled = bridgeEnabled
	cfg.Gateway.OpenAIWS.Enabled = true
	cfg.Gateway.OpenAIWS.OAuthEnabled = true
	cfg.Gateway.OpenAIWS.APIKeyEnabled = true
	cfg.Gateway.OpenAIWS.ResponsesWebsocketsV2 = true
	cfg.Gateway.OpenAIWS.MaxConnsPerAccount = 1
	cfg.Gateway.OpenAIWS.MinIdlePerAccount = 0
	cfg.Gateway.OpenAIWS.MaxIdlePerAccount = 1
	cfg.Gateway.OpenAIWS.QueueLimitPerConn = 8
	cfg.Gateway.OpenAIWS.DialTimeoutSeconds = 3
	cfg.Gateway.OpenAIWS.ReadTimeoutSeconds = 3
	cfg.Gateway.OpenAIWS.WriteTimeoutSeconds = 3

	captureConn := &openAIWSCaptureConn{
		events: [][]byte{
			[]byte(`{"type":"response.completed","response":{"id":"resp_compaction_image_bridge_ws","model":"gpt-5.5","usage":{"input_tokens":1,"output_tokens":1}}}`),
		},
	}
	captureDialer := &openAIWSCaptureDialer{conn: captureConn}
	pool := newOpenAIWSConnPool(cfg)
	pool.setClientDialerForTest(captureDialer)
	t.Cleanup(pool.Close)

	svc := &OpenAIGatewayService{
		cfg:              cfg,
		httpUpstream:     &httpUpstreamRecorder{},
		cache:            &stubGatewayCache{},
		openaiWSResolver: NewOpenAIWSProtocolResolver(cfg),
		toolCorrector:    NewCodexToolCorrector(),
		openaiWSPool:     pool,
	}

	groupID := int64(7001)
	apiKey := &APIKey{
		ID:      7002,
		UserID:  7003,
		GroupID: &groupID,
		Group: &Group{
			ID:                   groupID,
			AllowImageGeneration: true,
		},
	}
	account := &Account{
		ID:          7004,
		Name:        "openai-compaction-image-bridge-ws",
		Platform:    PlatformOpenAI,
		Type:        AccountTypeOAuth,
		Status:      StatusActive,
		Schedulable: true,
		Concurrency: 1,
		Credentials: map[string]any{
			"access_token": "test-token",
		},
		Extra: map[string]any{
			"openai_oauth_responses_websockets_v2_enabled": true,
			featureKeyCodexImageGenerationBridge:           bridgeEnabled,
		},
	}

	serverErrCh := make(chan error, 1)
	wsServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := coderws.Accept(w, r, &coderws.AcceptOptions{
			CompressionMode: coderws.CompressionContextTakeover,
		})
		if err != nil {
			serverErrCh <- err
			return
		}
		defer func() { _ = conn.CloseNow() }()

		recorder := httptest.NewRecorder()
		ginCtx, _ := gin.CreateTestContext(recorder)
		req := r.Clone(r.Context())
		req.Header = req.Header.Clone()
		req.Header.Set("User-Agent", "codex_cli_rs/0.98.0")
		ginCtx.Request = req
		ginCtx.Set("api_key", apiKey)

		readCtx, cancelRead := context.WithTimeout(r.Context(), 3*time.Second)
		msgType, message, readErr := conn.Read(readCtx)
		cancelRead()
		if readErr != nil {
			serverErrCh <- readErr
			return
		}
		if msgType != coderws.MessageText && msgType != coderws.MessageBinary {
			serverErrCh <- errors.New("unsupported websocket client message type")
			return
		}

		serverErrCh <- svc.ProxyResponsesWebSocketFromClient(r.Context(), ginCtx, conn, account, "test-token", message, nil)
	}))
	t.Cleanup(wsServer.Close)

	dialCtx, cancelDial := context.WithTimeout(context.Background(), 3*time.Second)
	clientConn, _, err := coderws.Dial(dialCtx, "ws"+strings.TrimPrefix(wsServer.URL, "http"), nil)
	cancelDial()
	require.NoError(t, err)
	t.Cleanup(func() { _ = clientConn.CloseNow() })

	writeCtx, cancelWrite := context.WithTimeout(context.Background(), 3*time.Second)
	err = clientConn.Write(writeCtx, coderws.MessageText, firstMessage)
	cancelWrite()
	require.NoError(t, err)

	readCtx, cancelRead := context.WithTimeout(context.Background(), 3*time.Second)
	msgType, message, err := clientConn.Read(readCtx)
	cancelRead()
	require.NoError(t, err)
	require.Equal(t, coderws.MessageText, msgType)
	require.Equal(t, "resp_compaction_image_bridge_ws", gjson.GetBytes(message, "response.id").String())

	_ = clientConn.Close(coderws.StatusNormalClosure, "done")
	select {
	case serverErr := <-serverErrCh:
		require.NoError(t, serverErr)
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for ingress websocket to finish")
	}

	captureConn.mu.Lock()
	defer captureConn.mu.Unlock()
	require.Len(t, captureConn.writes, 1)
	return []byte(requestToJSONString(captureConn.writes[0]))
}
