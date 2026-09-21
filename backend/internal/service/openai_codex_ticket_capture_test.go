package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/stretchr/testify/require"
)

type ticketCaptureProxyRepo struct {
	ProxyRepository
	proxies []Proxy
	err     error
}

func (r *ticketCaptureProxyRepo) ListByIDs(_ context.Context, ids []int64) ([]Proxy, error) {
	var out []Proxy
	for _, id := range ids {
		for _, proxy := range r.proxies {
			if proxy.ID == id {
				out = append(out, proxy)
			}
		}
	}
	return out, r.err
}

type ticketCaptureDefaultsUpstream struct {
	HTTPUpstream
	mu    sync.Mutex
	urls  []string
	state string
}

func (u *ticketCaptureDefaultsUpstream) DoWithTLS(req *http.Request, proxyURL string, _ int64, _ int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	u.mu.Lock()
	u.urls = append(u.urls, proxyURL)
	u.mu.Unlock()
	headers := make(http.Header)
	headers.Set(openAICodexTurnStateHeader, u.state)
	return &http.Response{StatusCode: http.StatusOK, Header: headers, Body: io.NopCloser(strings.NewReader(completedCodexTicketStream("gpt-6-astra")))}, nil
}

func TestCodexTicketCaptureDefaultsSeparateFromBusinessExits(t *testing.T) {
	account := ticketTestAccount(501)
	account.Status = StatusActive
	account.MultiProxyConfigured = true
	account.Extra[OpenAICodexTicketCaptureProxyIDsExtraKey] = []int64{3, 4, 5}
	proxies := make([]Proxy, 5)
	for i := range proxies {
		proxies[i] = Proxy{ID: int64(i + 1), Name: fmt.Sprintf("exit-%d", i+1), Protocol: "http", Host: "127.0.0.1", Port: 18001 + i, Status: StatusActive}
	}
	account.ProxyID, account.Proxy = &proxies[0].ID, &proxies[0]
	for i := 0; i < 2; i++ {
		account.ProxyBindings = append(account.ProxyBindings, AccountProxyBinding{ProxyID: proxies[i].ID, Proxy: &proxies[i], MaxConcurrency: 2})
	}
	beforeBindings := append([]AccountProxyBinding(nil), account.ProxyBindings...)
	beforeExtra := maps.Clone(account.Extra)
	settings := &SettingService{proxyRepo: &ticketCaptureProxyRepo{proxies: proxies}}
	upstream := &ticketCaptureDefaultsUpstream{state: syntheticCodexTicketState(11, time.Now(), 6)}
	newService := func() *OpenAIGatewayService {
		svc := ticketTestService(t, config.OpenAICodexTicketConfig{Models: []string{"gpt-6-astra"}}, upstream)
		svc.accountRepo = &codexTicketRefreshRepo{accounts: []Account{*account}}
		svc.settingService = settings
		return svc
	}
	// The background cycle and a fresh service both resolve persisted capture IDs.
	svc := newService()
	svc.refreshOpenAICodexTickets(context.Background())
	require.ElementsMatch(t, []string{proxies[2].URL(), proxies[3].URL(), proxies[4].URL()}, upstream.urls)
	upstream.urls = nil
	encoded, err := json.Marshal(account.Extra)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(encoded, &account.Extra))
	svc = newService()
	statuses, err := svc.RefreshOpenAICodexTicket(context.Background(), account.ID, "gpt-6-astra")
	require.NoError(t, err)
	require.True(t, statuses[0].Ready)
	require.ElementsMatch(t, []string{proxies[2].URL(), proxies[3].URL(), proxies[4].URL()}, upstream.urls)
	for _, binding := range account.ProxyBindings {
		business := *account
		business.RequestProxy = binding.Proxy
		headers := make(http.Header)
		require.NoError(t, svc.applyOpenAICodexTicket(context.Background(), &business, "gpt-6-astra", headers))
		require.Equal(t, upstream.state, headers.Get(openAICodexTurnStateHeader))
	}
	// The probe worker does not mutate settings; defaults are saved before dispatch.
	upstream.urls = nil
	_, err = svc.RefreshOpenAICodexTicketWithProxies(context.Background(), account.ID, "gpt-6-astra", []*Proxy{&proxies[1]})
	require.NoError(t, err)
	require.Equal(t, []string{proxies[1].URL()}, upstream.urls)
	ids, err := OpenAICodexTicketCaptureProxyIDs(account)
	require.NoError(t, err)
	require.Equal(t, []int64{3, 4, 5}, ids)
	require.Equal(t, beforeBindings, account.ProxyBindings)
	require.Equal(t, beforeExtra[OpenAICodexTicketEnabledExtraKey], account.Extra[OpenAICodexTicketEnabledExtraKey])
}

func TestCodexTicketCaptureDefaultsUnavailableDoesNotFallBack(t *testing.T) {
	account := ticketTestAccount(502)
	account.Extra[OpenAICodexTicketCaptureProxyIDsExtraKey] = []int64{3, 4, 5}
	account.Proxy = &Proxy{ID: 1, Protocol: "http", Host: "127.0.0.1", Port: 19000, Status: StatusActive}
	account.ProxyID = &account.Proxy.ID
	expired := time.Now().Add(-time.Hour)
	repo := &ticketCaptureProxyRepo{proxies: []Proxy{
		{ID: 3, Protocol: "http", Host: "127.0.0.1", Port: 19003, Status: StatusDisabled},
		{ID: 4, Protocol: "http", Host: "127.0.0.1", Port: 19004, Status: StatusActive, ExpiresAt: &expired},
	}}
	svc := ticketTestService(t, config.OpenAICodexTicketConfig{}, nil)
	svc.settingService = &SettingService{proxyRepo: repo}
	_, err := svc.defaultCodexTicketCaptureProxies(context.Background(), account)
	require.ErrorContains(t, err, "CODEX_TICKET_CAPTURE_NO_ACTIVE_PROXY")
	repo.proxies = append(repo.proxies, Proxy{ID: 5, Protocol: "http", Host: "127.0.0.1", Port: 19005, Status: StatusActive})
	got, err := svc.defaultCodexTicketCaptureProxies(context.Background(), account)
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.EqualValues(t, 5, got[0].ID)
	repo.err = errors.New("database unavailable")
	_, err = svc.defaultCodexTicketCaptureProxies(context.Background(), account)
	require.ErrorContains(t, err, "CODEX_TICKET_CAPTURE_UNAVAILABLE")
	account.Extra[OpenAICodexTicketCaptureProxyIDsExtraKey] = []int64{}
	got, err = svc.defaultCodexTicketCaptureProxies(context.Background(), account)
	require.NoError(t, err)
	require.Nil(t, got, "explicit reset should restore legacy routing")
	svc.settingService = nil
	account.Extra[OpenAICodexTicketCaptureProxyIDsExtraKey] = []int64{3}
	_, err = svc.defaultCodexTicketCaptureProxies(context.Background(), account)
	require.ErrorContains(t, err, "CODEX_TICKET_CAPTURE_UNAVAILABLE")
}

func TestCodexTicketCaptureDefaultsRejectMalformedSavedIDs(t *testing.T) {
	account := ticketTestAccount(503)
	for _, raw := range []any{nil, "3,4", []any{1.5}, []int64{0}, []int64{-1}, make([]int64, 65)} {
		account.Extra[OpenAICodexTicketCaptureProxyIDsExtraKey] = raw
		_, err := OpenAICodexTicketCaptureProxyIDs(account)
		require.Error(t, err)
	}
	account.Extra[OpenAICodexTicketCaptureProxyIDsExtraKey] = []any{float64(3), float64(4), float64(3)}
	ids, err := OpenAICodexTicketCaptureProxyIDs(account)
	require.NoError(t, err)
	require.Equal(t, []int64{3, 4}, ids)
}

func TestCodexTicketCaptureFlightIsolatesExitSelections(t *testing.T) {
	a := []openAICodexTicketProbeTarget{{ProxyID: 1, ProxyURL: "http://127.0.0.1:18001"}}
	b := []openAICodexTicketProbeTarget{{ProxyID: 2, ProxyURL: "http://127.0.0.1:18002"}}
	require.NotEqual(t, codexTicketCaptureFlightKey(1, "gpt-6-astra", a), codexTicketCaptureFlightKey(1, "gpt-6-astra", b))
	require.Equal(t, codexTicketCaptureFlightKey(1, "gpt-6-astra", a), codexTicketCaptureFlightKey(1, "gpt-6-astra", a))
}

func TestCodexTicketCaptureDefaultsSurviveAccountEdits(t *testing.T) {
	key := OpenAICodexTicketCaptureProxyIDsExtraKey
	current := map[string]any{key: []int64{3, 4, 5}, OpenAICodexTicketEnabledExtraKey: false}
	for _, edit := range []map[string]any{
		{"note": "ordinary edit"},
		{"note": "stale selection", key: []int64{1, 2}},
	} {
		merged := MergeOpenAICodexTicketExtra(edit, current)
		require.Equal(t, []int64{3, 4, 5}, merged[key])
		require.Equal(t, false, merged[OpenAICodexTicketEnabledExtraKey])
		require.Equal(t, edit["note"], merged["note"])
	}
	imported := MergeOpenAICodexTicketExtra(map[string]any{key: []int64{99}}, nil)
	require.NotContains(t, imported, key, "source-instance proxy IDs must not enter via generic imports")
	require.NotContains(t, RedactOpenAICodexTicketExtra(current), key)
	require.Equal(t, []int64{3, 4, 5}, current[key], "redaction must not mutate the live configuration")
}

func TestCodexTicketCaptureDefaultsAdminPersistence(t *testing.T) {
	const accountID int64 = 504
	key := OpenAICodexTicketCaptureProxyIDsExtraKey
	repo := &upstreamBillingProbeAccountRepo{accounts: map[int64]*Account{
		accountID: {ID: accountID, Platform: PlatformOpenAI, Type: AccountTypeOAuth,
			Extra: map[string]any{key: []int64{3, 4, 5}, OpenAICodexTicketEnabledExtraKey: false}},
	}}
	svc := &adminServiceImpl{accountRepo: repo}
	err := svc.UpdateAccountExtra(context.Background(), accountID, map[string]any{key: []int64{6, 7}})
	require.NoError(t, err)
	require.Equal(t, map[string]any{key: []int64{6, 7}}, repo.updates[accountID][0])
	updated, err := svc.UpdateAccount(context.Background(), accountID, &UpdateAccountInput{
		Extra: map[string]any{key: []int64{99}, "note": "ordinary edit"},
	})
	require.NoError(t, err)
	require.Equal(t, []int64{6, 7}, updated.Extra[key])
	require.Equal(t, false, updated.Extra[OpenAICodexTicketEnabledExtraKey])
}
