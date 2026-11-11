package service

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/benchmark"
	"github.com/stretchr/testify/require"
)

func pelicanClaudeResponse(text, reason string) string {
	delta, _ := json.Marshal(map[string]any{"type": "content_block_delta", "delta": map[string]any{"type": "text_delta", "text": text}})
	return "data: " + string(delta) + "\n\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"" + reason + "\"}}\n\ndata: {\"type\":\"message_stop\"}\n\n"
}

func pelicanGeminiResponse(text, reason string) string {
	data, _ := json.Marshal(map[string]any{"candidates": []any{map[string]any{
		"content":      map[string]any{"parts": []any{map[string]any{"thought": true, "text": "private reasoning"}, map[string]any{"text": text}}},
		"finishReason": reason,
	}}})
	return "data: " + string(data) + "\n\n"
}

type pelicanAdapterCase struct {
	name, platform, typ, model, protocol, wire string
}

func pelicanAdapterCases() []pelicanAdapterCase {
	return []pelicanAdapterCase{
		{"claude oauth", PlatformAnthropic, AccountTypeOAuth, "claude-sonnet-4-5", "", "claude"},
		{"claude setup token", PlatformAnthropic, AccountTypeSetupToken, "claude-sonnet-4-5", "", "claude"},
		{"claude key", PlatformAnthropic, AccountTypeAPIKey, "claude-sonnet-4-5", "", "claude"},
		{"bedrock key", PlatformAnthropic, AccountTypeBedrock, "claude-sonnet-4-5", "", "bedrock"},
		{"gemini oauth", PlatformGemini, AccountTypeOAuth, "gemini-2.5-flash", "", "gemini"},
		{"gemini key", PlatformGemini, AccountTypeAPIKey, "gemini-2.5-flash", "", "gemini"},
		{"antigravity claude oauth", PlatformAntigravity, AccountTypeOAuth, "claude-sonnet-4-5", "", "wrapped"},
		{"antigravity gemini oauth", PlatformAntigravity, AccountTypeOAuth, "gemini-3.7-flash-medium", "", "wrapped"},
		{"antigravity claude key", PlatformAntigravity, AccountTypeAPIKey, "claude-sonnet-4-5", "", "claude"},
		{"antigravity gemini key", PlatformAntigravity, AccountTypeAPIKey, "gemini-2.5-flash", "", "gemini"},
		{"antigravity upstream", PlatformAntigravity, AccountTypeUpstream, "claude-sonnet-4-5", "", "wrapped"},
		{"grok oauth", PlatformGrok, AccountTypeOAuth, grokDefaultResponsesModel, "", "responses"},
		{"grok key", PlatformGrok, AccountTypeAPIKey, grokDefaultResponsesModel, "", "responses"},
		{"kimi chat", PlatformKimi, AccountTypeAPIKey, "kimi-k2.5", APIProtocolChatCompletions, "chat"},
		{"zhipu adaptive", PlatformZhipu, AccountTypeAPIKey, "glm-5.3", APIProtocolAdaptive, "chat"},
		{"deepseek responses", PlatformDeepseek, AccountTypeAPIKey, "deepseek-flash", APIProtocolResponses, "responses"},
		{"minimax messages", PlatformMiniMax, AccountTypeAPIKey, "MiniMax-M2.5", APIProtocolAnthropic, "claude"},
		{"opencode chat", PlatformOpenCodeGo, AccountTypeAPIKey, "glm-5.3", APIProtocolChatCompletions, "chat"},
		{"opencode messages", PlatformOpenCodeGo, AccountTypeAPIKey, "claude-sonnet-4-5", APIProtocolAnthropic, "claude"},
		{"opencode responses", PlatformOpenCodeGo, AccountTypeAPIKey, "gpt-6-astra", APIProtocolResponses, "responses"},
	}
}

func pelicanAdapterFixture(tc pelicanAdapterCase, text string) (*Account, *pelicanRecordingUpstream, *AccountTestService) {
	proxyID := int64(91)
	a := &Account{ID: 7, Platform: tc.platform, Type: tc.typ, ProxyID: &proxyID,
		Proxy: &Proxy{ID: proxyID, Protocol: "socks5", Host: "selected.example", Port: 1080, Status: StatusActive},
		Credentials: map[string]any{"access_token": "synthetic-access", "refresh_token": "synthetic-refresh", "api_key": "synthetic-api",
			"project_id": "test-project", "plan_type": "free", "expires_at": time.Now().Add(-time.Hour).Unix(), "auth_mode": "apikey"},
	}
	if tc.protocol != "" {
		a.Credentials["api_protocol"] = tc.protocol
	}
	u := &pelicanRecordingUpstream{status: 200}
	switch tc.wire {
	case "claude":
		u.response = pelicanClaudeResponse(text, "end_turn")
	case "gemini", "wrapped":
		u.response = pelicanGeminiResponse(text, "STOP")
	case "bedrock":
		body, _ := json.Marshal(map[string]any{"content": []any{map[string]any{"type": "text", "text": text}}, "stop_reason": "end_turn"})
		u.response = string(body)
	case "responses":
		u.response = pelicanResponses(text)
	case "chat":
		body, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"content": text}, "finish_reason": "stop"}}})
		u.response = "data: " + string(body) + "\n\ndata: [DONE]\n\n"
	}
	s := pelicanTestService(a, u)
	s.antigravityGatewayService = &AntigravityGatewayService{httpUpstream: u}
	return a, u, s
}

func TestPelicanAllAdaptersPromptProxyCompletionAndContinuation(t *testing.T) {
	for _, tc := range pelicanAdapterCases() {
		for _, continuing := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/continue=%t", tc.name, continuing), func(t *testing.T) {
				ctx, text := context.Background(), "<html>animation</html>"
				if continuing {
					ctx, text = benchmark.WithContinuation(ctx, "<html>animation"), "</html>"
				}
				a, u, s := pelicanAdapterFixture(tc, text)
				before, _ := json.Marshal(a)
				actual := ""
				u.onSend = func(*http.Request) { require.NotEmpty(t, actual, "model must be committed before network send") }
				out, err := s.RunPelicanBenchmark(ctx, a.ID, tc.model, func(model string) error {
					require.False(t, u.called)
					actual = model
					return nil
				})
				require.NoError(t, err)
				require.Equal(t, "<html>animation</html>", out.HTML)
				require.Equal(t, 1, u.calls, "adaptive benchmark must generate only once")
				require.Equal(t, a.Proxy.URL(), u.proxy)
				require.Equal(t, a.ID, u.accountID)
				after, _ := json.Marshal(a)
				require.JSONEq(t, string(before), string(after))
				body, key := u.body, "messages"
				switch tc.wire {
				case "wrapped":
					require.Equal(t, actual, body["model"])
					wrapped, ok := body["request"].(map[string]any)
					require.True(t, ok)
					body = wrapped
					key = "contents"
					if tc.model == "gemini-3.7-flash-medium" {
						require.Equal(t, "gemini-3.7-flash-tiered", actual)
						config, ok := body["generationConfig"].(map[string]any)
						require.True(t, ok)
						thinking, ok := config["thinkingConfig"].(map[string]any)
						require.True(t, ok)
						require.Equal(t, float64(4000), thinking["thinkingBudget"])
					}
				case "gemini":
					if wrapped, ok := body["request"].(map[string]any); ok {
						require.Equal(t, actual, body["model"])
						body = wrapped
					} else {
						require.Contains(t, u.url, actual+":streamGenerateContent")
					}
					key = "contents"
				case "responses":
					key = "input"
					require.Equal(t, actual, body["model"])
				case "bedrock":
					endpoint, err := url.Parse(u.url)
					require.NoError(t, err)
					require.Equal(t, "/model/"+actual+"/invoke", endpoint.Path)
				default:
					require.Equal(t, actual, body["model"])
				}
				messages := body[key].([]any)
				want := []string{benchmark.Prompt}
				if continuing {
					want = append(want, "<html>animation", "继续")
				}
				require.Len(t, messages, len(want))
				for i, item := range messages {
					message := item.(map[string]any)
					role := []string{"user", "assistant", "user"}[i]
					if role == "assistant" && key == "contents" {
						role = "model"
					}
					require.Equal(t, role, message["role"])
					content := message["content"]
					if key == "contents" {
						content = message["parts"].([]any)[0].(map[string]any)["text"]
					} else if parts, ok := content.([]any); ok {
						content = parts[0].(map[string]any)["text"]
					}
					require.Equal(t, want[i], content)
				}
			})
		}
	}
}

func TestPelicanAllAdaptersReadOnlyFailuresAndBeforeSend(t *testing.T) {
	for _, tc := range pelicanAdapterCases() {
		for _, status := range []int{401, 402, 403, 429, 503} {
			t.Run(fmt.Sprintf("%s/%d", tc.name, status), func(t *testing.T) {
				a, u, s := pelicanAdapterFixture(tc, "")
				u.status, u.response = status, `{"error":{"message":"rejected synthetic-access synthetic-api synthetic-refresh","plan_type":"paid"}}`
				before, _ := json.Marshal(a)
				out, err := s.RunPelicanBenchmark(context.Background(), a.ID, tc.model, nil)
				require.Error(t, err)
				require.Equal(t, fmt.Sprintf("upstream_http_%d", status), out.ErrorCode)
				require.Contains(t, out.ErrorMessage, "rejected")
				require.NotContains(t, out.ErrorMessage, "synthetic-")
				after, _ := json.Marshal(a)
				require.JSONEq(t, string(before), string(after))
			})
		}
		t.Run(tc.name+"/before send rejects", func(t *testing.T) {
			a, u, s := pelicanAdapterFixture(tc, "")
			_, err := s.RunPelicanBenchmark(context.Background(), a.ID, tc.model, func(string) error { return benchmark.ErrStopped })
			require.Error(t, err)
			require.False(t, u.called)
		})
		t.Run(tc.name+"/oversize", func(t *testing.T) {
			a, _, s := pelicanAdapterFixture(tc, strings.Repeat("x", benchmark.MaxHTMLBytes+1))
			out, err := s.RunPelicanBenchmark(context.Background(), a.ID, tc.model, nil)
			require.ErrorIs(t, err, benchmark.ErrTooLarge)
			require.Equal(t, benchmark.MaxHTMLBytes, len(out.HTML))
		})
	}
}

func TestPelicanNativeStreamsRequireActualCompletion(t *testing.T) {
	for _, tc := range []struct{ platform, model, response, code string }{
		{PlatformAnthropic, "claude-sonnet-4-5", pelicanClaudeResponse("<html></html>", "max_tokens"), "upstream_incomplete"},
		{PlatformAnthropic, "claude-sonnet-4-5", strings.Replace(pelicanClaudeResponse("<html></html>", "end_turn"), `data: {"type":"message_stop"}`, "", 1), "upstream_incomplete"},
		{PlatformGemini, "gemini-2.5-flash", pelicanGeminiResponse("<html></html>", "MAX_TOKENS"), "upstream_incomplete"},
		{PlatformGemini, "gemini-2.5-flash", pelicanGeminiResponse("<html></html>", ""), "upstream_incomplete"},
		{PlatformGemini, "gemini-2.5-flash", "data: {\"response\":{\"error\":{\"message\":\"synthetic-api exhausted\"}}}\n\n", "upstream_stream_error"},
	} {
		t.Run(tc.platform+tc.code+tc.response[:min(40, len(tc.response))], func(t *testing.T) {
			a, u, s := pelicanAdapterFixture(pelicanAdapterCase{platform: tc.platform, typ: AccountTypeAPIKey}, "")
			u.response = tc.response
			out, err := s.RunPelicanBenchmark(context.Background(), a.ID, tc.model, nil)
			require.Error(t, err)
			require.Equal(t, tc.code, out.ErrorCode)
			require.NotContains(t, out.ErrorMessage, "synthetic-api")
		})
	}
}

func TestPelicanEligibilityOnlyExcludesNonTextAndOpenAISafeguards(t *testing.T) {
	for _, tc := range pelicanAdapterCases() {
		a, _, _ := pelicanAdapterFixture(tc, "")
		require.Empty(t, PelicanBenchmarkEligibility(a, tc.model), tc.name)
	}
	a := &Account{Platform: PlatformAnthropic, Type: AccountTypeAPIKey}
	for _, model := range []string{"gpt-image-2", "dall-e-3", "grok-imagine-image", "grok-video-1.5", "imagine-image", "gemini-3-pro-image", "imagen-4.0-generate-001", "veo-3.1-generate-preview", "text-embedding-3-large", "whisper-1", "gemini-2.5-flash-preview-tts", "rerank-v3.5", "chatgpt-image-latest", "textembedding-gecko@003", "multimodalembedding@001", "gemini-2.5-flash-native-audio-preview-12-2025", "gemini-2.5-flash-preview-native-audio-dialog"} {
		require.Equal(t, "model_not_supported", PelicanBenchmarkEligibility(a, model), model)
	}
	for _, model := range []string{"claude-sonnet-4-5@20250929", "us.anthropic.claude-sonnet-4-5-20250929-v1:0", "vendor/custom+reasoning", "vision-model", "gpt-4o", "gemini-2.5-flash", "vendor/image-understanding", "custom-audio-reasoning"} {
		require.True(t, benchmark.ValidModel(model), model)
		require.Empty(t, PelicanBenchmarkEligibility(a, model), model)
	}
	a.Credentials = map[string]any{"model_mapping": map[string]any{"text-alias": "gpt-image-2"}}
	require.Equal(t, "model_not_supported", PelicanBenchmarkEligibility(a, "text-alias"))
}

func TestPelicanNewAdapterNetworkFailureRetainsContinuation(t *testing.T) {
	for _, tc := range pelicanAdapterCases() {
		t.Run(tc.name, func(t *testing.T) {
			a, u, s := pelicanAdapterFixture(tc, "")
			u.err = errors.New("proxy synthetic-api failed")
			out, err := s.RunPelicanBenchmark(benchmark.WithContinuation(context.Background(), "<html>partial"), a.ID, tc.model, nil)
			require.Error(t, err)
			require.Equal(t, "<html>partial", out.HTML)
			require.NotContains(t, err.Error(), "synthetic-api")
			require.Empty(t, out.ErrorMessage)
		})
	}
}

type pelicanCancelBody struct {
	ctx  context.Context
	text io.Reader
}

func (r *pelicanCancelBody) Read(p []byte) (int, error) {
	if r.text != nil {
		n, err := r.text.Read(p)
		if err != io.EOF {
			return n, err
		}
		r.text = nil
	}
	<-r.ctx.Done()
	return 0, r.ctx.Err()
}
func (*pelicanCancelBody) Close() error { return nil }

func TestPelicanNewAdapterCancellationRetainsPartial(t *testing.T) {
	for _, tc := range pelicanAdapterCases() {
		if tc.wire == "bedrock" {
			continue
		}
		t.Run(tc.name, func(t *testing.T) {
			a, u, s := pelicanAdapterFixture(tc, "<html>partial")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			response := u.response
			switch tc.wire {
			case "claude", "responses":
				response = strings.Split(response, "\n\n")[0] + "\n\n"
			case "gemini", "wrapped":
				response = pelicanGeminiResponse("<html>partial", "")
			case "chat":
				response = strings.ReplaceAll(response, `"finish_reason":"stop"`, `"finish_reason":null`)
				response = strings.Split(response, "data: [DONE]")[0]
			}
			u.reader = &pelicanCancelBody{ctx: ctx, text: strings.NewReader(response)}
			u.onSend = func(*http.Request) { cancel() }
			out, err := s.RunPelicanBenchmark(ctx, a.ID, tc.model, nil)
			require.Error(t, err)
			require.Equal(t, "<html>partial", out.HTML)
		})
	}
}

type pelicanTokenTransport func(*http.Request) (*http.Response, error)

func (f pelicanTokenTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestPelicanVertexServiceAccountsUseEphemeralTokens(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	privateKey := string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}))
	previousTransport := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = previousTransport })
	exchanges := 0
	http.DefaultTransport = pelicanTokenTransport(func(req *http.Request) (*http.Response, error) {
		exchanges++
		require.Equal(t, vertexDefaultTokenURL, req.URL.String())
		require.NoError(t, req.ParseForm())
		require.Equal(t, "urn:ietf:params:oauth:grant-type:jwt-bearer", req.Form.Get("grant_type"))
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"access_token":"ephemeral-probe-token","expires_in":3600}`))}, nil
	})
	for _, platform := range []string{PlatformAnthropic, PlatformGemini} {
		model, wire := "claude-sonnet-4-5@20250929", "claude"
		if platform == PlatformGemini {
			model, wire = "gemini-2.5-flash", "gemini"
		}
		for _, status := range []int{200, 401} {
			t.Run(fmt.Sprintf("%s/%d", platform, status), func(t *testing.T) {
				a, u, s := pelicanAdapterFixture(pelicanAdapterCase{platform: platform, typ: AccountTypeServiceAccount, wire: wire}, "<html></html>")
				a.ProxyID, a.Proxy = nil, nil
				a.Credentials["service_account_json"] = map[string]any{"client_email": "svc@example.iam.gserviceaccount.com", "project_id": "test-project", "private_key": privateKey}
				before, _ := json.Marshal(a)
				u.status = status
				if status != 200 {
					u.response = `{"error":{"message":"rejected ephemeral-probe-token"}}`
				}
				actual := ""
				u.onSend = func(*http.Request) { require.NotEmpty(t, actual) }
				out, err := s.RunPelicanBenchmark(context.Background(), a.ID, model, func(value string) error { actual = value; return nil })
				if status == 200 {
					require.NoError(t, err)
					require.Equal(t, "<html></html>", out.HTML)
				} else {
					require.Error(t, err)
					require.NotContains(t, out.ErrorMessage, "ephemeral-probe-token")
				}
				require.Equal(t, "Bearer ephemeral-probe-token", u.auth)
				require.Contains(t, u.url, actual)
				after, _ := json.Marshal(a)
				require.JSONEq(t, string(before), string(after))
			})
		}
	}
	require.Equal(t, 4, exchanges, "no shared token/credential cache is populated by a benchmark")
}

func TestPelicanDefaultSkipsImageMappings(t *testing.T) {
	for _, platform := range []string{PlatformOpenAI, PlatformAnthropic, PlatformGemini, PlatformGrok, PlatformKimi} {
		a := &Account{Platform: platform, Type: AccountTypeAPIKey, Credentials: map[string]any{"model_mapping": map[string]any{
			benchmark.DefaultModel: "gpt-image-2", "a-image": "imagen-4.0-generate-001", "z-text": "actual-text-model",
		}}}
		require.Equal(t, "z-text", PelicanBenchmarkDefaultModel(a), platform)
		require.Empty(t, PelicanBenchmarkEligibility(a, PelicanBenchmarkDefaultModel(a)))
	}
	for _, platform := range []string{PlatformOpenAI, PlatformAnthropic, PlatformGemini, PlatformAntigravity, PlatformGrok, PlatformKimi, PlatformZhipu, PlatformDeepseek, PlatformMiniMax, PlatformOpenCodeGo} {
		a := &Account{Platform: platform, Type: AccountTypeAPIKey}
		require.Empty(t, PelicanBenchmarkEligibility(a, PelicanBenchmarkDefaultModel(a)), platform)
	}
}

func TestPelicanNativeModelMappingCommitsFinalID(t *testing.T) {
	for _, tc := range []struct{ platform, model, upstream, wire string }{
		{PlatformAnthropic, "public-alias", "claude-sonnet-4-5", "claude"},
		{PlatformGemini, "public-alias", "models/gemini-2.5-flash", "gemini"},
		{PlatformAntigravity, "public-alias", "models/gemini-2.5-flash", "gemini"},
		{PlatformGrok, "public-alias", "grok-4.5", "responses"},
	} {
		t.Run(tc.platform, func(t *testing.T) {
			a, u, s := pelicanAdapterFixture(pelicanAdapterCase{platform: tc.platform, typ: AccountTypeAPIKey, wire: tc.wire}, "<html></html>")
			a.Credentials["model_mapping"] = map[string]any{"public-*": tc.upstream}
			actual := ""
			out, err := s.RunPelicanBenchmark(context.Background(), a.ID, tc.model, func(model string) error { actual = model; return nil })
			require.NoError(t, err)
			require.Equal(t, "<html></html>", out.HTML)
			if tc.wire == "gemini" {
				require.Equal(t, strings.TrimPrefix(tc.upstream, "models/"), actual)
				require.Contains(t, u.url, "/models/"+actual+":streamGenerateContent")
			} else {
				require.Equal(t, tc.upstream, actual)
				require.Equal(t, actual, u.body["model"])
			}
		})
	}
}
