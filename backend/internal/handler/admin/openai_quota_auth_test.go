//go:build unit

package admin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type quotaAuthRefreshStub struct {
	openAIQuotaWorkflowStub
	refreshCalls int
}

func (s *quotaAuthRefreshStub) RefreshUsage(context.Context, int64) (*service.OpenAIQuotaUsage, error) {
	s.refreshCalls++
	return nil, infraerrors.New(401, "OPENAI_QUOTA_UPSTREAM_ERROR", "upstream returned 401")
}

func TestOpenAIQuotaAuthObservationOnlyOnExplicitRefresh(t *testing.T) {
	gin.SetMode(gin.TestMode)
	quota := &quotaAuthRefreshStub{openAIQuotaWorkflowStub: openAIQuotaWorkflowStub{
		queryErr: infraerrors.New(401, "OPENAI_QUOTA_UPSTREAM_ERROR", "upstream returned 401"),
	}}
	handler := &OpenAIOAuthHandler{quotaService: quota, adminService: &openAIResetAdminServiceStub{}}
	router := gin.New()
	router.GET("/openai/accounts/:id/quota", handler.QueryQuota)
	router.POST("/openai/accounts/:id/quota/refresh", handler.RefreshQuota)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/openai/accounts/42/quota", nil))
	require.Equal(t, http.StatusUnauthorized, w.Code)
	require.Equal(t, 1, quota.queryCalls)
	require.Zero(t, quota.refreshCalls)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/openai/accounts/42/quota/refresh", nil))
	require.Equal(t, http.StatusUnauthorized, w.Code)
	require.Contains(t, w.Body.String(), "OPENAI_QUOTA_UPSTREAM_ERROR")
	require.Equal(t, 1, quota.queryCalls)
	require.Equal(t, 1, quota.refreshCalls)
	require.Zero(t, quota.cacheCalls)
}

func TestOpenAIQuotaAuthMarkerEntersReauthorizationListAfterCooldown(t *testing.T) {
	for _, reason := range []string{
		service.OpenAIUsageUnauthorizedReason, "OAuth 401: expired token", "Authentication failed (401): expired token",
	} {
		t.Run(reason, func(t *testing.T) {
			account := reauthorizationAccount(436)
			account.Status, account.ErrorMessage = service.StatusActive, ""
			account.TempUnschedulableReason = reason
			account.TempUnschedulableUntil = nil
			require.True(t, openAIAccountNeedsReauthorization(account))
			account.TempUnschedulableReason = "SOCKS 401 proxy connection refused"
			require.False(t, openAIAccountNeedsReauthorization(account))
		})
	}
}
