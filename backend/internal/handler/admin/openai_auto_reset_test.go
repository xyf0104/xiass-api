package admin

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type autoResetHandlerQuota struct {
	openAIQuotaService
	reads, writes int
	enabled       bool
}

func (q *autoResetHandlerQuota) GetAutoResetConfig(context.Context, int64) (service.OpenAIAutoResetConfig, error) {
	q.reads++
	return service.OpenAIAutoResetConfig{Threshold5h: 1, Threshold7d: 1}, nil
}
func (q *autoResetHandlerQuota) SetAutoResetConfig(_ context.Context, _ int64, enabled bool, fiveHour, sevenDay float64) (service.OpenAIAutoResetConfig, error) {
	q.writes++
	q.enabled = enabled
	return service.OpenAIAutoResetConfig{Enabled: enabled, Threshold5h: fiveHour, Threshold7d: sevenDay}, nil
}

func TestOpenAIAutoResetConfigHandlerRequiresExplicitSettings(t *testing.T) {
	for _, tc := range []struct {
		method, body          string
		status, reads, writes int
	}{
		{"GET", "", 200, 1, 0},
		{"PUT", `{"enabled":true,"threshold_5h":1,"threshold_7d":1}`, 200, 0, 1},
		{"PUT", `{"enabled":false,"threshold_5h":1,"threshold_7d":1}`, 200, 0, 1},
		{"PUT", `{"threshold_5h":1,"threshold_7d":1}`, 400, 0, 0},
		{"PUT", `{"enabled":null,"threshold_5h":1,"threshold_7d":1}`, 400, 0, 0},
		{"PUT", `{"enabled":"true","threshold_5h":1,"threshold_7d":1}`, 400, 0, 0},
		{"PUT", `{"enabled":true}`, 400, 0, 0},
	} {
		t.Run(tc.method+tc.body, func(t *testing.T) {
			quota := &autoResetHandlerQuota{}
			h := &OpenAIOAuthHandler{quotaService: quota}
			gin.SetMode(gin.TestMode)
			router := gin.New()
			router.Handle(tc.method, "/accounts/:id/auto-reset", h.AutoResetConfig)
			request := httptest.NewRequest(tc.method, "/accounts/42/auto-reset", strings.NewReader(tc.body))
			request.Header.Set("Content-Type", "application/json")
			r := httptest.NewRecorder()
			router.ServeHTTP(r, request)
			require.Equal(t, tc.status, r.Code, r.Body.String())
			require.Equal(t, tc.reads, quota.reads)
			require.Equal(t, tc.writes, quota.writes)
		})
	}
}
