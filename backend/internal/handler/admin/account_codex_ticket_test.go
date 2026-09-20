package admin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestAccountResponseCodexTicketsUsesConfiguredPolicy(t *testing.T) {
	account := &service.Account{ID: 41, Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth, Extra: map[string]any{service.OpenAICodexTicketEnabledExtraKey: true}}
	h := &AccountHandler{cfg: &config.Config{}}
	require.Len(t, h.accountResponseFromService(account).CodexTurnTickets, 3)
	require.Len(t, h.accountListResponseFromService(account).CodexTurnTickets, 3)
	h.cfg.Gateway.OpenAICodexTicket = config.OpenAICodexTicketConfig{Enabled: true, Models: []string{"configured-model"}, FailClosed: false}
	status := h.accountListResponseFromService(account).CodexTurnTickets
	require.Len(t, status, 1)
	require.Equal(t, "configured-model", status[0].Model)
	require.False(t, status[0].Blocked)
	h.cfg.Gateway.OpenAICodexTicket.FailClosed = true
	require.True(t, h.accountResponseFromService(account).CodexTurnTickets[0].Blocked)
}

func TestAccountResponseCodexTicketsReadsLiveSettingsAfterRestart(t *testing.T) {
	cfg := &config.Config{}
	repo := &settingHandlerRepoStub{values: map[string]string{service.SettingKeyOpenAICodexTicketEnabled: "true"}}
	settings := service.NewSettingService(repo, cfg)
	h := &AccountHandler{cfg: cfg}
	h.SetCodexTicketSettings(settings)
	account := &service.Account{ID: 41, Platform: service.PlatformOpenAI, Type: service.AccountTypeSetupToken}
	require.Len(t, h.accountListResponseFromService(account).CodexTurnTickets, 3)
	require.False(t, cfg.Gateway.OpenAICodexTicket.Enabled)
	repo.values[service.SettingKeyOpenAICodexTicketEnabled] = "false"
	settings.InvalidateOpenAICodexTicketEnabledCache()
	require.Len(t, h.accountResponseFromService(account).CodexTurnTickets, 3)
	require.False(t, h.accountResponseFromService(account).CodexTurnTickets[0].Blocked)
}

type recordingCodexTicketRefresher struct {
	legacyCalls int
	proxyCalls  int
	proxyIDs    []int64
}

func (r *recordingCodexTicketRefresher) RefreshOpenAICodexTicket(context.Context, int64, string) ([]service.OpenAICodexTicketStatus, error) {
	r.legacyCalls++
	return nil, nil
}

func (r *recordingCodexTicketRefresher) RefreshOpenAICodexTicketWithProxies(_ context.Context, _ int64, _ string, proxies []*service.Proxy) ([]service.OpenAICodexTicketStatus, error) {
	r.proxyCalls++
	r.proxyIDs = r.proxyIDs[:0]
	for _, proxy := range proxies {
		r.proxyIDs = append(r.proxyIDs, proxy.ID)
	}
	return nil, nil
}

func TestRefreshCodexTicketProxyIDsSemantics(t *testing.T) {
	gin.SetMode(gin.TestMode)
	adminSvc := &stubAdminService{proxies: []service.Proxy{
		{ID: 11, Name: "bound-or-unbound", Protocol: "socks5", Host: "127.0.0.1", Port: 1080, Status: service.StatusActive},
		{ID: 12, Name: "subscription-node", Protocol: "http", Host: "127.0.0.2", Port: 8080, Status: service.StatusActive},
	}}
	refresher := &recordingCodexTicketRefresher{}
	h := &AccountHandler{adminService: adminSvc, codexTicketRefresher: refresher}
	router := gin.New()
	router.POST("/accounts/:id/codex-ticket/refresh", h.RefreshCodexTicket)

	request := func(body string) *httptest.ResponseRecorder {
		recorder := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/accounts/41/codex-ticket/refresh", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		router.ServeHTTP(recorder, req)
		return recorder
	}

	require.Equal(t, http.StatusOK, request(`{"model":"gpt-6-astra"}`).Code)
	require.Equal(t, 1, refresher.legacyCalls)
	require.Zero(t, refresher.proxyCalls)

	require.Equal(t, http.StatusBadRequest, request(`{"model":"gpt-6-astra","proxy_ids":[]}`).Code)
	require.Zero(t, refresher.proxyCalls)

	require.Equal(t, http.StatusOK, request(`{"model":"gpt-6-astra","proxy_ids":[12,11,12]}`).Code)
	require.Equal(t, 1, refresher.proxyCalls)
	require.Equal(t, []int64{12, 11}, refresher.proxyIDs)
}

func TestLoadCodexTicketCaptureProxiesRejectsInactiveExpiredAndMissing(t *testing.T) {
	expired := time.Now().Add(-time.Minute)
	h := &AccountHandler{adminService: &stubAdminService{proxies: []service.Proxy{
		{ID: 1, Status: service.StatusActive},
		{ID: 2, Status: service.StatusDisabled},
		{ID: 3, Status: service.StatusActive, ExpiresAt: &expired},
	}}}

	proxies, err := h.loadCodexTicketCaptureProxies(context.Background(), []int64{1})
	require.NoError(t, err)
	require.Len(t, proxies, 1)
	for _, ids := range [][]int64{{2}, {3}, {4}, {0}} {
		_, err = h.loadCodexTicketCaptureProxies(context.Background(), ids)
		require.Error(t, err)
	}
	tooMany := make([]int64, maxCodexTicketCaptureProxyIDs+1)
	for i := range tooMany {
		tooMany[i] = int64(i + 1)
	}
	_, err = h.loadCodexTicketCaptureProxies(context.Background(), tooMany)
	require.ErrorContains(t, err, "at most 64")
}
