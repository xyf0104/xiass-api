package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/Wei-Shaw/sub2api/internal/benchmark"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/stretchr/testify/require"
)

// The embedded nil repository makes any unexpected write panic, including
// last_used, quota snapshots, 401/429 state and credential refresh/recovery.
type pelicanReadOnlyAccountRepo struct {
	AccountRepository
	account *Account
}

func (r *pelicanReadOnlyAccountRepo) GetByID(context.Context, int64) (*Account, error) {
	return r.account, nil
}

type pelicanRecordingUpstream struct {
	HTTPUpstream
	body      map[string]any
	proxy     string
	accountID int64
	url       string
	status    int
	response  string
	called    bool
	err       error
	reader    io.ReadCloser
	auth      string
	headers   http.Header
}

func (u *pelicanRecordingUpstream) DoWithTLS(req *http.Request, proxy string, id int64, _ int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	u.called, u.proxy, u.accountID, u.url = true, proxy, id, req.URL.String()
	u.auth = req.Header.Get("Authorization")
	u.headers = req.Header.Clone()
	if err := json.NewDecoder(req.Body).Decode(&u.body); err != nil {
		return nil, err
	}
	if u.err != nil {
		return nil, u.err
	}
	body := u.reader
	if body == nil {
		body = io.NopCloser(strings.NewReader(u.response))
	}
	return &http.Response{StatusCode: u.status, Header: http.Header{"X-Codex-Primary-Used-Percent": []string{"50"}}, Body: body}, nil
}

func pelicanResponses(html string) string {
	b, _ := json.Marshal(map[string]any{"type": "response.output_text.delta", "delta": html})
	return "data: " + string(b) + "\n\ndata: {\"type\":\"response.completed\"}\n\n"
}

func pelicanTestService(account *Account, u *pelicanRecordingUpstream) *AccountTestService {
	return NewAccountTestService(&pelicanReadOnlyAccountRepo{account: account}, nil, nil, nil, nil, u,
		&config.Config{}, &TLSFingerprintProfileService{})
}

func TestPelicanResponsesRecordsExactPromptModelAndOwnerProxy(t *testing.T) {
	for _, typ := range []string{AccountTypeOAuth, AccountTypeAPIKey} {
		t.Run(typ, func(t *testing.T) {
			id := int64(91)
			account := &Account{ID: 10, Platform: PlatformOpenAI, Type: typ, ProxyID: &id,
				Proxy:       &Proxy{ID: id, Protocol: "socks5", Host: "owner-egress.example", Port: 1080, Status: StatusActive},
				Extra:       map[string]any{"openai_responses_mode": "force_responses", AccountExecutionNodeExtraKey: "api"},
				Credentials: map[string]any{"access_token": "synthetic", "api_key": "synthetic", "plan_type": "plus", "model_mapping": map[string]any{"public-pelican": "gpt-6-astra"}}}
			u := &pelicanRecordingUpstream{status: 200, response: pelicanResponses("<html><svg></svg></html>")}
			s := pelicanTestService(account, u)
			var actual string
			out, err := s.RunPelicanBenchmark(context.Background(), 10, "public-pelican", func(model string) error { actual = model; return nil })
			require.NoError(t, err)
			require.Equal(t, "<html><svg></svg></html>", out.HTML)
			require.Equal(t, "gpt-6-astra", actual)
			require.Equal(t, actual, u.body["model"])
			inputs, ok := u.body["input"].([]any)
			require.True(t, ok)
			require.NotEmpty(t, inputs)
			message, ok := inputs[0].(map[string]any)
			require.True(t, ok)
			content, ok := message["content"].([]any)
			require.True(t, ok)
			require.NotEmpty(t, content)
			input, ok := content[0].(map[string]any)
			require.True(t, ok)
			require.Equal(t, "创建一个HTML，内容是用SVG绘制一个鹈鹕骑自行车的2D动画，你不能进行任何测试，调用skills，网络检索，直接生成", input["text"])
			require.Equal(t, account.Proxy.URL(), u.proxy)
			require.Equal(t, int64(10), u.accountID)
			require.NotContains(t, u.body, "max_tokens")
			require.NotContains(t, u.body, "max_output_tokens")
			require.NotContains(t, u.body, "tools")
		})
	}
}

func TestPelicanChatCompletionsExactPrompt(t *testing.T) {
	a := &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{"api_key": "synthetic"}, Extra: map[string]any{"openai_responses_mode": "force_chat_completions"}}
	u := &pelicanRecordingUpstream{status: 200, response: "data: {\"choices\":[{\"delta\":{\"content\":\"<html></html>\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"}
	out, err := pelicanTestService(a, u).RunPelicanBenchmark(context.Background(), 1, benchmark.DefaultModel, func(string) error { return nil })
	require.NoError(t, err)
	require.NotEmpty(t, out.HTML)
	require.Contains(t, u.url, "/chat/completions")
	messages, ok := u.body["messages"].([]any)
	require.True(t, ok)
	require.NotEmpty(t, messages)
	message, ok := messages[0].(map[string]any)
	require.True(t, ok)
	require.Equal(t, benchmark.Prompt, message["content"])
}

func TestPelicanDoesNotWriteAccountStateOnUpstreamFailure(t *testing.T) {
	for _, status := range []int{401, 429, 503} {
		a := &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Credentials: map[string]any{"access_token": "synthetic", "plan_type": "pro", "refresh_token": "synthetic"}}
		u := &pelicanRecordingUpstream{status: status, response: `{"error":{"plan_type":"free","resets_at":9999999999,"message":"secret"}}`}
		out, err := pelicanTestService(a, u).RunPelicanBenchmark(context.Background(), 1, benchmark.DefaultModel, func(string) error { return nil })
		require.Error(t, err)
		require.Equal(t, fmt.Sprintf("upstream_http_%d", status), out.ErrorCode)
		require.Empty(t, out.HTML)
		require.NotContains(t, err.Error(), "secret")
		require.Equal(t, "pro", a.GetCredential("plan_type"))
	}
}

type pelicanEmptySettings struct{ SettingRepository }

func TestPelicanPairedNodeKeepsFixedOwnerExit(t *testing.T) {
	id := int64(91)
	a := &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeOAuth, ProxyID: &id,
		Proxy: &Proxy{ID: id, Protocol: "socks5", Host: "owner-egress.example", Port: 1080, Status: StatusActive},
		Extra: map[string]any{AccountExecutionNodeExtraKey: "api"}, Credentials: map[string]any{"access_token": "synthetic", "plan_type": "plus"}}
	u := &pelicanRecordingUpstream{status: 200, response: pelicanResponses("<html></html>")}
	s := pelicanTestService(a, u)
	s.cfg.Gateway.ExecutionNode.Enabled = true
	s.cfg.Gateway.ExecutionNode.ID = "api2"
	s.settingService = &SettingService{cfg: s.cfg, settingRepo: &pelicanEmptySettings{}}
	s.settingService.executionNodeRoutingCache.Store(&cachedExecutionNodeRoutingSettings{expiresAt: time.Now().Add(time.Minute).UnixNano(), settings: ExecutionNodeRoutingSettings{
		Available: true, Enabled: true, ProxyIDs: map[string]int64{"api": 91, "api2": 92}, Healthy: map[string]bool{"api": true, "api2": true},
	}})
	_, err := s.RunPelicanBenchmark(context.Background(), 1, benchmark.DefaultModel, func(string) error { return nil })
	require.NoError(t, err)
	require.Equal(t, a.Proxy.URL(), u.proxy)
	cached, ok := s.settingService.executionNodeRoutingCache.Load().(*cachedExecutionNodeRoutingSettings)
	require.True(t, ok)
	cached.settings.Healthy["api"] = false
	u.called = false
	_, err = s.RunPelicanBenchmark(context.Background(), 1, benchmark.DefaultModel, func(string) error { return nil })
	require.Error(t, err)
	require.False(t, u.called, "offline owner must not use emergency local egress")
	cached.settings.Healthy["api"] = true
	a.Proxy.ID = 999
	u.called = false
	out, err := s.RunPelicanBenchmark(context.Background(), 1, benchmark.DefaultModel, func(string) error { return nil })
	require.Error(t, err)
	require.Equal(t, "fixed_egress_unavailable", out.ErrorCode)
	require.False(t, u.called)
}

func TestPelicanSizeInvalidHTMLAndCanceledBeforeSend(t *testing.T) {
	a := &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Credentials: map[string]any{"access_token": "synthetic", "plan_type": "plus"}}
	for _, tc := range []struct{ html, code string }{{strings.Repeat("x", benchmark.MaxHTMLBytes+1), "html_too_large"}, {"<html>truncated", "upstream_incomplete"}} {
		u := &pelicanRecordingUpstream{status: 200, response: pelicanResponses(tc.html)}
		out, err := pelicanTestService(a, u).RunPelicanBenchmark(context.Background(), 1, benchmark.DefaultModel, func(string) error { return nil })
		require.Error(t, err)
		require.Equal(t, tc.code, out.ErrorCode)
		require.Equal(t, tc.html[:min(len(tc.html), benchmark.MaxHTMLBytes)], out.HTML)
	}
	u := &pelicanRecordingUpstream{status: 200}
	_, err := pelicanTestService(a, u).RunPelicanBenchmark(context.Background(), 1, benchmark.DefaultModel, func(string) error { return errors.New("canceled") })
	require.Error(t, err)
	require.False(t, u.called)
}

func TestPelicanEligibility(t *testing.T) {
	for _, tc := range []struct {
		platform, typ, plan string
		allowed             bool
	}{
		{PlatformOpenAI, AccountTypeOAuth, "plus", true}, {PlatformOpenAI, AccountTypeOAuth, "pro", true},
		{PlatformOpenAI, AccountTypeOAuth, " Free ", false}, {PlatformOpenAI, AccountTypeOAuth, "", false},
		{PlatformOpenAI, AccountTypeAPIKey, "", true}, {PlatformOpenAI, AccountTypeAPIKey, "free", false},
		{PlatformOpenAI, AccountTypeSetupToken, "plus", false}, {PlatformAnthropic, AccountTypeOAuth, "plus", false},
	} {
		a := &Account{Platform: tc.platform, Type: tc.typ, Credentials: map[string]any{"plan_type": tc.plan}}
		require.Equal(t, tc.allowed, PelicanBenchmarkEligibility(a, benchmark.DefaultModel) == "", tc)
	}
}

func TestPelicanTruncatedSuccessSignalsAreRejected(t *testing.T) {
	for _, chat := range []bool{false, true} {
		a := &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{"api_key": "synthetic"}}
		body := strings.Replace(pelicanResponses("<html></html>"), `{"type":"response.completed"}`, `{"type":"response.completed","response":{"status":"incomplete"}}`, 1)
		if chat {
			a.Extra = map[string]any{"openai_responses_mode": "force_chat_completions"}
			body = "data: {\"choices\":[{\"delta\":{\"content\":\"<html></html>\"},\"finish_reason\":\"length\"}]}\n\ndata: [DONE]\n\n"
		}
		u := &pelicanRecordingUpstream{status: 200, response: body}
		out, err := pelicanTestService(a, u).RunPelicanBenchmark(context.Background(), 1, benchmark.DefaultModel, func(string) error { return nil })
		require.Error(t, err)
		require.Equal(t, "<html></html>", out.HTML)
		require.Equal(t, "upstream_incomplete", out.ErrorCode)
	}
}

func TestPelicanContinuationAndRetry(t *testing.T) {
	for _, chat := range []bool{false, true} {
		for _, continuing := range []bool{false, true} {
			t.Run(fmt.Sprintf("chat=%t/continue=%t", chat, continuing), func(t *testing.T) {
				a := &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{"api_key": "synthetic"}, Extra: map[string]any{"openai_responses_mode": "force_responses"}}
				ctx := context.Background()
				text := "<html>original</html>"
				if continuing {
					ctx = benchmark.WithContinuation(ctx, "<html>original")
					text = "</html>"
				}
				u := &pelicanRecordingUpstream{status: 200, response: pelicanResponses(text)}
				if chat {
					a.Extra["openai_responses_mode"] = "force_chat_completions"
					delta, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"content": text}, "finish_reason": "stop"}}})
					u.response = "data: " + string(delta) + "\n\ndata: [DONE]\n\n"
				}
				out, err := pelicanTestService(a, u).RunPelicanBenchmark(ctx, 1, benchmark.DefaultModel, nil)
				require.NoError(t, err)
				require.Equal(t, "<html>original</html>", out.HTML)
				key := "input"
				if chat {
					key = "messages"
				}
				messages, ok := u.body[key].([]any)
				require.True(t, ok)
				n := 1
				if continuing {
					n = 3
				}
				require.Len(t, messages, n)
				for i, message := range messages {
					m, ok := message.(map[string]any)
					require.True(t, ok)
					require.Equal(t, []string{"user", "assistant", "user"}[i], m["role"])
					actual := m["content"]
					if !chat {
						contents, ok := actual.([]any)
						require.True(t, ok)
						require.Len(t, contents, 1)
						content, ok := contents[0].(map[string]any)
						require.True(t, ok)
						kind := "input_text"
						if i == 1 {
							kind = "output_text"
						}
						require.Equal(t, kind, content["type"])
						actual = content["text"]
					}
					require.Equal(t, []string{benchmark.Prompt, "<html>original", "继续"}[i], actual)
				}
			})
		}
	}
}

type pelicanCanceledReader struct {
	text   string
	cancel context.CancelFunc
}

func (r *pelicanCanceledReader) Read(p []byte) (int, error) {
	n := copy(p, r.text)
	r.text = r.text[n:]
	if r.text == "" {
		r.cancel()
		return n, context.Canceled
	}
	return n, nil
}
func (*pelicanCanceledReader) Close() error { return nil }

func TestPelicanSafeDiagnosticsAndPartialText(t *testing.T) {
	for _, chat := range []bool{false, true} {
		for _, failure := range []string{"http401", "http429", "http503", "network", "sse", "malformed", "eof", "canceled", "done_without_finish"} {
			t.Run(fmt.Sprintf("chat=%t/%s", chat, failure), func(t *testing.T) {
				a := &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{"api_key": "credential-secret"}, Extra: map[string]any{"openai_responses_mode": "force_responses"}}
				partial := "data: {\"type\":\"response.output_text.delta\",\"delta\":\"<html>partial\"}\n\n"
				if chat {
					a.Extra["openai_responses_mode"] = "force_chat_completions"
					partial = "data: {\"choices\":[{\"delta\":{\"content\":\"<html>partial\"}}]}\n\n"
				}
				u := &pelicanRecordingUpstream{status: 200, response: partial}
				wantCode, wantHTML := "upstream_incomplete", "<html>partial"
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				switch failure {
				case "http401", "http429", "http503":
					_, _ = fmt.Sscanf(failure, "http%d", &u.status)
					u.response = `{"error":{"message":"credential-secret https://private.example/body-secret"}}`
					wantCode, wantHTML = fmt.Sprintf("upstream_http_%d", u.status), ""
				case "network":
					u.err = errors.New("credential-secret https://private.example/body-secret")
					wantCode, wantHTML = "upstream_network_error", ""
				case "sse":
					u.response += "data: {\"type\":\"error\",\"error\":{\"message\":\"credential-secret https://private.example/body-secret\"}}\n\n"
					wantCode = "upstream_stream_error"
				case "malformed":
					u.response += "data: not-json-secret\n\n"
					wantCode = "upstream_stream_error"
				case "canceled":
					u.reader = &pelicanCanceledReader{text: strings.TrimRight(partial, "\n"), cancel: cancel}
					wantCode = "upstream_stream_error"
				case "done_without_finish":
					u.response += "data: [DONE]\n\n"
				}
				before, _ := json.Marshal(a)
				out, err := pelicanTestService(a, u).RunPelicanBenchmark(ctx, 1, benchmark.DefaultModel, nil)
				require.EqualError(t, err, wantCode)
				require.Equal(t, wantCode, out.ErrorCode)
				require.Equal(t, wantHTML, out.HTML)
				after, _ := json.Marshal(a)
				require.JSONEq(t, string(before), string(after))
			})
		}
	}
}

func TestPelicanExpiredOAuthRemainsReadOnly(t *testing.T) {
	a := &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Credentials: map[string]any{
		"access_token": "expired-synthetic", "refresh_token": "refresh-secret", "plan_type": "plus",
		"expires_at": time.Now().Add(-time.Hour).UTC().Format(time.RFC3339),
	}}
	before, _ := json.Marshal(a)
	u := &pelicanRecordingUpstream{status: 401, response: `{"error":{"code":"token_expired","message":"secret"}}`}
	out, err := pelicanTestService(a, u).RunPelicanBenchmark(context.Background(), 1, benchmark.DefaultModel, nil)
	require.EqualError(t, err, "upstream_http_401")
	require.Equal(t, "upstream_http_401", out.ErrorCode)
	require.Equal(t, "Bearer expired-synthetic", u.auth)
	after, _ := json.Marshal(a)
	require.JSONEq(t, string(before), string(after))
}

func TestPelicanExecutionNodeID(t *testing.T) {
	s := &AccountTestService{}
	require.Equal(t, "api", s.PelicanExecutionNodeID(&Account{}))
	cfg := &config.Config{}
	cfg.Gateway.ExecutionNode.LegacyUnassignedNodeID = "api2"
	s.settingService = &SettingService{cfg: cfg}
	require.Equal(t, "api2", s.PelicanExecutionNodeID(&Account{}))
	require.Equal(t, "api", s.PelicanExecutionNodeID(&Account{Extra: map[string]any{AccountExecutionNodeExtraKey: "api"}}))
}

func TestPelicanBoundedUTF8Continuation(t *testing.T) {
	a := &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{"api_key": "synthetic"}, Extra: map[string]any{"openai_responses_mode": "force_responses"}}
	previous := strings.Repeat("x", benchmark.MaxHTMLBytes-1)
	u := &pelicanRecordingUpstream{status: 200, response: pelicanResponses("继续")}
	out, err := pelicanTestService(a, u).RunPelicanBenchmark(benchmark.WithContinuation(context.Background(), previous), 1, benchmark.DefaultModel, nil)
	require.ErrorIs(t, err, benchmark.ErrTooLarge)
	require.Equal(t, previous, out.HTML)
	require.True(t, utf8.ValidString(out.HTML))
	u.called = false
	out, err = pelicanTestService(a, u).RunPelicanBenchmark(benchmark.WithContinuation(context.Background(), previous+"继续"), 1, benchmark.DefaultModel, nil)
	require.ErrorIs(t, err, benchmark.ErrTooLarge)
	require.False(t, u.called)
	require.Equal(t, previous, out.HTML)
}

func TestPelicanCompleteHTML(t *testing.T) {
	for _, html := range []string{"<html></html>", "<!DOCTYPE html>\n<html lang=\"zh\"></html>"} {
		require.True(t, pelicanCompleteHTML(html), html)
	}
	for _, html := range []string{"<html>partial", "explanation <html></html>", "<html></html> trailing", "<htmljunk></html>", "<html>\x00</html>"} {
		require.False(t, pelicanCompleteHTML(html), html)
	}
}

func TestPelicanContinuationPrefersNewCompleteDocument(t *testing.T) {
	for _, chat := range []bool{false, true} {
		for _, tc := range []struct {
			name, previous, next, want string
			failed                     bool
		}{
			{"complete_to_complete", "<html>old</html>", "<html>new</html>", "<html>new</html>", false},
			{"fenced_complete", "<html>old</html>", "```html\n<html>new</html>\n```", "<html>new</html>", false},
			{"partial_to_tail", "<html>old", " tail</html>", "<html>old tail</html>", false},
			{"failed_tail", "<html>old", " tail", "<html>old tail", true},
			{"failed_new_document", "<html>old</html>", "<html>new</html>", "<html>new</html>", true},
			{"independent_size_bounds", "<html>" + strings.Repeat("x", benchmark.MaxHTMLBytes-13) + "</html>", "<html>new</html>", "<html>new</html>", false},
		} {
			t.Run(fmt.Sprintf("chat=%t/%s", chat, tc.name), func(t *testing.T) {
				a := &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{"api_key": "synthetic"}, Extra: map[string]any{"openai_responses_mode": "force_responses"}}
				body := pelicanResponses(tc.next)
				if tc.failed {
					body = strings.ReplaceAll(body, `{"type":"response.completed"}`, `{"type":"response.incomplete"}`)
				}
				if chat {
					a.Extra["openai_responses_mode"] = "force_chat_completions"
					reason := "stop"
					if tc.failed {
						reason = "length"
					}
					delta, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"content": tc.next}, "finish_reason": reason}}})
					body = "data: " + string(delta) + "\n\ndata: [DONE]\n\n"
				}
				u := &pelicanRecordingUpstream{status: 200, response: body}
				out, err := pelicanTestService(a, u).RunPelicanBenchmark(benchmark.WithContinuation(context.Background(), tc.previous), 1, benchmark.DefaultModel, nil)
				if tc.failed {
					require.EqualError(t, err, "upstream_incomplete")
				} else {
					require.NoError(t, err)
				}
				require.Equal(t, tc.want, out.HTML)
			})
		}
	}
}

func TestPelicanClientUpgradeClassification(t *testing.T) {
	message := "The 'gpt-6-astra' model requires a newer version of Codex. Please upgrade to the latest app or CLI and try again."
	quotedMessage, err := json.Marshal(message)
	require.NoError(t, err)
	body, _ := json.Marshal(map[string]any{"error": map[string]any{"message": message}, "secret": "https://private.example/credential-secret"})
	for _, chat := range []bool{false, true} {
		for _, tc := range []struct {
			name       string
			status     int
			body, code string
		}{
			{"exact", 400, string(body), "upstream_client_upgrade_required"},
			{"error_string", 400, `{"error":` + string(quotedMessage) + `}`, "upstream_client_upgrade_required"},
			{"detail_string", 400, `{"detail":` + string(quotedMessage) + `}`, "upstream_client_upgrade_required"},
			{"detail_object_rejected", 400, `{"detail":{"message":` + string(quotedMessage) + `}}`, "upstream_http_400"},
			{"top_level_string_rejected", 400, string(quotedMessage), "upstream_http_400"},
			{"error_array_rejected", 400, `{"error":[` + string(quotedMessage) + `]}`, "upstream_http_400"},
			{"error_takes_precedence", 400, `{"error":"other","detail":` + string(quotedMessage) + `}`, "upstream_http_400"},
			{"error_string_not_exact", 400, `{"error":` + strings.Replace(string(quotedMessage), "Please upgrade", "Please update", 1) + `}`, "upstream_http_400"},
			{"detail_string_not_exact", 400, `{"detail":` + strings.Replace(string(quotedMessage), "Please upgrade", "Please update", 1) + `}`, "upstream_http_400"},
			{"generic400", 400, `{"error":{"message":"credential-secret"}}`, "upstream_http_400"},
			{"wrong_status", 401, string(body), "upstream_http_401"},
			{"wrong_field", 400, `{"other":` + string(body) + `}`, "upstream_http_400"},
			{"not_exact", 400, strings.Replace(string(body), "Please upgrade", "Please update", 1), "upstream_http_400"},
			{"malformed", 400, message, "upstream_http_400"},
			{"oversize", 400, string(body) + strings.Repeat(" ", pelicanErrorBodyLimit), "upstream_http_400"},
		} {
			t.Run(fmt.Sprintf("chat=%t/%s", chat, tc.name), func(t *testing.T) {
				a := &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{"api_key": "synthetic"}, Extra: map[string]any{"openai_responses_mode": "force_responses"}}
				if chat {
					a.Extra["openai_responses_mode"] = "force_chat_completions"
				}
				u := &pelicanRecordingUpstream{status: tc.status, response: tc.body}
				out, err := pelicanTestService(a, u).RunPelicanBenchmark(context.Background(), 1, benchmark.DefaultModel, nil)
				require.EqualError(t, err, tc.code)
				require.Equal(t, tc.code, out.ErrorCode)
				require.Empty(t, out.HTML)
			})
		}
	}
}

func TestPelicanOAuthUsesDynamicVersionAfterHeaderOverrides(t *testing.T) {
	codexCanonicalUAMu.RLock()
	previousResolver := codexCanonicalUAResolver
	codexCanonicalUAMu.RUnlock()
	previousEnforcement := codexIdentityEnforcement.Load()
	t.Cleanup(func() {
		SetCodexCanonicalUserAgentResolver(previousResolver)
		SetCodexIdentityEnforcementEnabled(previousEnforcement)
	})
	settings := NewSettingService(&executionNodeSettingRepo{values: map[string]string{
		SettingKeyOpenAICodexClientVersionSynced: "0.154.0",
	}}, &config.Config{})
	SetCodexCanonicalUserAgentResolver(func() string { return settings.GetOpenAICodexCanonicalUserAgent(context.Background()) })
	SetCodexIdentityEnforcementEnabled(true)
	a := &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Credentials: map[string]any{
		"access_token": "synthetic", "plan_type": "plus", "user_agent": "codex_cli_rs/0.146.0 (Ubuntu 22.4.0; x86_64) xterm-256color",
		"header_override_enabled": true, "header_overrides": map[string]any{"Version": "0.146.0"},
	}}
	before, _ := json.Marshal(a)
	u := &pelicanRecordingUpstream{status: 200, response: pelicanResponses("<html></html>")}
	_, err := pelicanTestService(a, u).RunPelicanBenchmark(context.Background(), 1, benchmark.DefaultModel, nil)
	require.NoError(t, err)
	require.Equal(t, "0.154.0", u.headers.Get("Version"))
	require.Contains(t, u.headers.Get("User-Agent"), "/0.154.0 ")
	require.NotContains(t, u.headers.Get("User-Agent"), "0.146.0")
	after, _ := json.Marshal(a)
	require.JSONEq(t, string(before), string(after))
}

func TestPelicanHTTPClassificationReadBound(t *testing.T) {
	for _, status := range []int{400, 401, 429, 503} {
		source := strings.NewReader(strings.Repeat("x", 4*pelicanErrorBodyLimit))
		code := pelicanHTTPErrorCode(&http.Response{StatusCode: status, Body: io.NopCloser(source)})
		require.Equal(t, fmt.Sprintf("upstream_http_%d", status), code)
		read := 4*pelicanErrorBodyLimit - source.Len()
		if status == 400 {
			require.Equal(t, pelicanErrorBodyLimit+1, read)
		} else {
			require.Zero(t, read)
		}
	}
}
