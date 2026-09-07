package admin

import (
	"context"
	"net/http"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
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

func TestOpenAIAutoResetConfigHandlerRequiresExplicitSettingsAndOwner(t *testing.T) {
	for _, tc := range []struct {
		method, body          string
		denied                bool
		status, reads, writes int
	}{
		{"GET", "", false, 200, 1, 0},
		{"PUT", `{"enabled":true,"threshold_5h":1,"threshold_7d":1}`, false, 200, 0, 1},
		{"PUT", `{"enabled":false,"threshold_5h":1,"threshold_7d":1}`, false, 200, 0, 1},
		{"PUT", `{"threshold_5h":1,"threshold_7d":1}`, false, 400, 0, 0},
		{"PUT", `{"enabled":null,"threshold_5h":1,"threshold_7d":1}`, false, 400, 0, 0},
		{"PUT", `{"enabled":"true","threshold_5h":1,"threshold_7d":1}`, false, 400, 0, 0},
		{"PUT", `{"enabled":true}`, false, 400, 0, 0},
		{"GET", "", true, http.StatusForbidden, 0, 0},
		{"PUT", `{"enabled":true,"threshold_5h":1,"threshold_7d":1}`, true, http.StatusForbidden, 0, 0},
	} {
		t.Run(tc.method+tc.body+fmtBoolHandler(tc.denied), func(t *testing.T) {
			access := &accountManagementAccessSpy{denied: map[int64]error{}}
			if tc.denied {
				access.denied[42] = remoteAccountReadOnlyError()
			}
			quota := &autoResetHandlerQuota{}
			h := &OpenAIOAuthHandler{adminService: access, quotaService: quota}
			r := performAccountManagementRequest(t, tc.method, "/accounts/:id/auto-reset", "/accounts/42/auto-reset", tc.body, h.AutoResetConfig)
			require.Equal(t, tc.status, r.Code, r.Body.String())
			require.Equal(t, tc.reads, quota.reads)
			require.Equal(t, tc.writes, quota.writes)
		})
	}
}
func fmtBoolHandler(v bool) string {
	if v {
		return "/denied"
	}
	return "/owner"
}
