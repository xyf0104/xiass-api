package service

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

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
}

func (u *pelicanRecordingUpstream) DoWithTLS(req *http.Request, proxy string, id int64, _ int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	u.called, u.proxy, u.accountID, u.url = true, proxy, id, req.URL.String()
	if err := json.NewDecoder(req.Body).Decode(&u.body); err != nil {
		return nil, err
	}
	return &http.Response{StatusCode: u.status, Header: http.Header{"X-Codex-Primary-Used-Percent": []string{"50"}}, Body: io.NopCloser(strings.NewReader(u.response))}, nil
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
			input := u.body["input"].([]any)[0].(map[string]any)["content"].([]any)[0].(map[string]any)
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
	require.Equal(t, benchmark.Prompt, u.body["messages"].([]any)[0].(map[string]any)["content"])
}

func TestPelicanDoesNotWriteAccountStateOnUpstreamFailure(t *testing.T) {
	for _, status := range []int{401, 429, 500} {
		a := &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Credentials: map[string]any{"access_token": "synthetic", "plan_type": "pro", "refresh_token": "synthetic"}}
		u := &pelicanRecordingUpstream{status: status, response: `{"error":{"plan_type":"free","resets_at":9999999999,"message":"secret"}}`}
		out, err := pelicanTestService(a, u).RunPelicanBenchmark(context.Background(), 1, benchmark.DefaultModel, func(string) error { return nil })
		require.Error(t, err)
		require.Equal(t, "upstream_failed", out.ErrorCode)
		require.Empty(t, out.HTML)
		require.NotContains(t, err.Error(), "secret")
		require.Equal(t, "pro", a.GetCredential("plan_type"))
	}
}

type pelicanEmptySettings struct{ SettingRepository }

func TestPelicanPairedNodeKeepsOwnerExitEvenWithEmergencyTakeover(t *testing.T) {
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
		EmergencyLocalEgress: true, LocalProxy: &Proxy{ID: 92, Protocol: "socks5", Host: "wrong-egress.example", Port: 1080, Status: StatusActive},
	}})
	_, err := s.RunPelicanBenchmark(context.Background(), 1, benchmark.DefaultModel, func(string) error { return nil })
	require.NoError(t, err)
	require.Equal(t, a.Proxy.URL(), u.proxy)
	cached := s.settingService.executionNodeRoutingCache.Load().(*cachedExecutionNodeRoutingSettings)
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
	for _, tc := range []struct{ html, code string }{{strings.Repeat("x", benchmark.MaxHTMLBytes+1), "html_too_large"}, {"<html>truncated", "invalid_html"}} {
		u := &pelicanRecordingUpstream{status: 200, response: pelicanResponses(tc.html)}
		out, err := pelicanTestService(a, u).RunPelicanBenchmark(context.Background(), 1, benchmark.DefaultModel, func(string) error { return nil })
		require.Error(t, err)
		require.Equal(t, tc.code, out.ErrorCode)
		require.Empty(t, out.HTML)
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
		require.Empty(t, out.HTML)
	}
}
