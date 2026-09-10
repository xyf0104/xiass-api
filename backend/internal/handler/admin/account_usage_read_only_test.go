package admin

import (
	"context"
	"net/http"
	"testing"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

type passiveHandlerAccountRepo struct {
	service.AccountRepository
	reads int
}

func (r *passiveHandlerAccountRepo) GetByID(_ context.Context, id int64) (*service.Account, error) {
	r.reads++
	observedAt := time.Now().UTC().Add(-time.Second)
	return &service.Account{
		ID: id, Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth,
		Status: service.StatusError, ErrorMessage: "temporary upstream error",
		Credentials: map[string]any{"chatgpt_account_id": "synthetic-passive-account"},
		Extra: map[string]any{
			"codex_7d_used_percent":  100.0,
			"codex_usage_updated_at": observedAt.Format(time.RFC3339Nano),
			"codex_7d_reset_at":      observedAt.Add(24 * time.Hour).Format(time.RFC3339Nano),
		},
	}, nil
}

type passiveHandlerStatsRepo struct{ service.UsageLogRepository }

func (*passiveHandlerStatsRepo) GetAccountWindowStats(context.Context, int64, time.Time) (*usagestats.AccountStats, error) {
	return &usagestats.AccountStats{}, nil
}

func TestAccountGetUsageReadOnlyFallback(t *testing.T) {
	for _, reason := range []string{
		"ACCOUNT_REMOTE_NODE_READ_ONLY", "ACCOUNT_NODE_ACCESS_UNAVAILABLE",
		"UNRELATED_DENIAL",
	} {
		for _, query := range []string{"", "?force=false", "?source=passive", "?source=active", "?force=true", "?source=passive&force=true", "?source=invalid"} {
			t.Run(reason+query, func(t *testing.T) {
				status := http.StatusForbidden
				if reason == "ACCOUNT_NODE_ACCESS_UNAVAILABLE" {
					status = http.StatusServiceUnavailable
				}
				access := &accountManagementAccessSpy{denied: map[int64]error{42: infraerrors.New(status, reason, "management unavailable")}}
				repo := &passiveHandlerAccountRepo{}
				handler := &AccountHandler{
					adminService:        access,
					accountUsageService: service.NewAccountUsageService(repo, &passiveHandlerStatsRepo{}, nil, nil, nil, nil, nil, nil, service.NewUsageCache(), nil, nil),
				}
				recorder := performAccountManagementRequest(t, http.MethodGet, "/accounts/:id/usage", "/accounts/42/usage"+query, "", handler.GetUsage)
				allowed := query == "?source=passive" || ((query == "" || query == "?force=false") && reason != "UNRELATED_DENIAL")
				if allowed {
					require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
					require.Contains(t, recorder.Body.String(), `"source":"passive"`)
					require.Contains(t, recorder.Body.String(), `"weekly_estimate_usd":0`)
					require.Equal(t, 1, repo.reads)
				} else {
					require.Equal(t, status, recorder.Code, recorder.Body.String())
					require.Zero(t, repo.reads)
				}
				if query == "?source=passive" {
					require.Empty(t, access.calls)
				} else {
					require.Equal(t, []int64{42}, access.calls)
				}
			})
		}
	}
}
