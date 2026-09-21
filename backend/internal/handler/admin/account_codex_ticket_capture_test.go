package admin

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/handler/dto"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type captureDefaultsAdminStub struct {
	*stubAdminService
	updates   map[string]any
	updateErr error
	accessErr error
}

func (s *captureDefaultsAdminStub) UpdateAccountExtra(_ context.Context, _ int64, updates map[string]any) error {
	s.updateAccountExtraCalls++
	if s.updateErr != nil {
		return s.updateErr
	}
	s.updates = maps.Clone(updates)
	maps.Copy(s.getAccountResult.Extra, updates)
	return nil
}

func (s *captureDefaultsAdminStub) CheckAccountManagementAccess(context.Context, int64) error {
	return s.accessErr
}

func TestCodexTicketCaptureDefaultsHandler(t *testing.T) {
	gin.SetMode(gin.TestMode)
	expired := time.Now().Add(-time.Minute)
	account := &service.Account{ID: 41, Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth,
		Extra: map[string]any{"unrelated": "keep", service.OpenAICodexTicketEnabledExtraKey: false}}
	svc := &captureDefaultsAdminStub{stubAdminService: &stubAdminService{getAccountResult: account, proxies: []service.Proxy{
		{ID: 11, Status: service.StatusActive}, {ID: 12, Status: service.StatusActive},
		{ID: 13, Status: service.StatusDisabled}, {ID: 14, Status: service.StatusActive, ExpiresAt: &expired},
	}}}
	refresher := &recordingCodexTicketRefresher{}
	h := &AccountHandler{adminService: svc, codexTicketRefresher: refresher, cfg: &config.Config{}}
	router := gin.New()
	router.PUT("/accounts/:id/codex-ticket/capture-proxies", h.SetCodexTicketCaptureProxies)
	request := func(body string) *httptest.ResponseRecorder {
		recorder := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPut, "/accounts/41/codex-ticket/capture-proxies", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		router.ServeHTTP(recorder, req)
		return recorder
	}

	res := request(`{"proxy_ids":[12,11,12]}`)
	require.Equal(t, http.StatusOK, res.Code, res.Body.String())
	require.JSONEq(t, `{"code":0,"message":"success","data":{"proxy_ids":[12,11]}}`, res.Body.String())
	require.Equal(t, map[string]any{service.OpenAICodexTicketCaptureProxyIDsExtraKey: []int64{12, 11}}, svc.updates)
	require.Equal(t, false, account.Extra[service.OpenAICodexTicketEnabledExtraKey])
	require.Equal(t, "keep", account.Extra["unrelated"])
	require.Zero(t, refresher.legacyCalls)
	require.Zero(t, refresher.proxyCalls)
	require.Equal(t, []int64{12, 11}, h.accountResponseFromService(account).CodexTicketCaptureProxyIDs)
	list := dto.AccountListItemFromAccount(h.accountListResponseFromService(account))
	require.Equal(t, []int64{12, 11}, list.CodexTicketCaptureProxyIDs)

	for _, body := range []string{`{}`, `{"proxy_ids":null}`, `{"proxy_ids":[0]}`, `{"proxy_ids":[11,99]}`, `{"proxy_ids":[13]}`, `{"proxy_ids":[14]}`, `{"proxy_ids":[1.5]}`} {
		t.Run(body, func(t *testing.T) {
			before := svc.updateAccountExtraCalls
			require.Equal(t, http.StatusBadRequest, request(body).Code)
			require.Equal(t, before, svc.updateAccountExtraCalls)
			require.Equal(t, []int64{12, 11}, account.Extra[service.OpenAICodexTicketCaptureProxyIDsExtraKey])
		})
	}
	tooMany := make([]int64, service.MaxOpenAICodexTicketCaptureProxyIDs+1)
	for i := range tooMany {
		tooMany[i] = 11
	}
	body, err := json.Marshal(map[string]any{"proxy_ids": tooMany})
	require.NoError(t, err)
	require.Equal(t, http.StatusBadRequest, request(string(body)).Code)

	account.Platform = service.PlatformAnthropic
	require.Equal(t, http.StatusBadRequest, request(`{"proxy_ids":[11]}`).Code)
	account.Platform = service.PlatformOpenAI
	svc.accessErr = infraerrors.New(http.StatusForbidden, "DENIED", "Account access denied")
	require.Equal(t, http.StatusForbidden, request(`{"proxy_ids":[11]}`).Code)
	svc.accessErr = nil
	svc.updateErr = errors.New("write failed")
	require.Equal(t, http.StatusInternalServerError, request(`{"proxy_ids":[11]}`).Code)
	require.Equal(t, []int64{12, 11}, account.Extra[service.OpenAICodexTicketCaptureProxyIDsExtraKey])
	svc.updateErr = nil

	res = request(`{"proxy_ids":[]}`)
	require.Equal(t, http.StatusOK, res.Code)
	require.Contains(t, res.Body.String(), `"proxy_ids":[]`)
	require.Equal(t, []int64{}, account.Extra[service.OpenAICodexTicketCaptureProxyIDsExtraKey])
	require.Equal(t, false, account.Extra[service.OpenAICodexTicketEnabledExtraKey])
}
