package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
	"github.com/stretchr/testify/require"
)

// Every unimplemented method panics, including all writes, error clearing and
// snapshot persistence. The bounded estimator read is implemented explicitly
// because passive views must recalculate from the latest aligned 1% endpoint.
type readOnlyUsageAccountRepo struct {
	AccountRepository
	account *Account
}

func (r *readOnlyUsageAccountRepo) GetByID(_ context.Context, id int64) (*Account, error) {
	if id != r.account.ID {
		return nil, errors.New("passive usage must not resolve another account")
	}
	return r.account, nil
}

type readOnlyUsageStatsRepo struct {
	UsageLogRepository
	stats      *usagestats.AccountStats
	rangeStats *usagestats.AccountStats
	err        error
	starts     []time.Time
	rangeCalls int
	rangeErr   error
}

func (r *readOnlyUsageStatsRepo) GetAccountWindowStats(_ context.Context, _ int64, start time.Time) (*usagestats.AccountStats, error) {
	r.starts = append(r.starts, start)
	return r.stats, r.err
}

func (r *readOnlyUsageStatsRepo) GetAccountWindowStatsRange(_ context.Context, _ int64, _, _ time.Time) (*usagestats.AccountStats, error) {
	r.rangeCalls++
	if r.rangeErr != nil {
		return nil, r.rangeErr
	}
	if r.rangeStats != nil {
		return r.rangeStats, r.err
	}
	return r.stats, r.err
}

func readOnlyUsageFixture() (*AccountUsageService, *Account, *readOnlyUsageStatsRepo) {
	now := time.Now().UTC().Truncate(time.Microsecond)
	resetAt := now.Add(5 * 24 * time.Hour)
	state := openAIWeeklyFrozenEstimateState{
		Mode: openAIWeeklyEstimateModeJoinAverage, BaselineSource: "observed_mid_join",
		BaselinePercent: 20, BaselineCost: 0, PercentBucket: 25,
		SnapshotPercent: 25, SnapshotCost: 120, CompletedPercent: 25, CompletedCost: 120,
		EstimateUSD: 2400, HasEstimate: true,
		ResetAt: resetAt, Identity: "read-only-account", ObservedAt: now.Add(-time.Hour),
	}
	account := &Account{
		ID: 42, Platform: PlatformOpenAI, Type: AccountTypeOAuth,
		Status: StatusError, ErrorMessage: "temporary upstream error",
		RateLimitResetAt: &resetAt,
		Credentials:      map[string]any{"access_token": "test-token", "chatgpt_account_id": state.Identity},
		Extra: map[string]any{
			"codex_5h_used_percent": 12.5,
			"codex_5h_reset_at":     now.Add(time.Hour).Format(time.RFC3339Nano),
			"codex_7d_used_percent": 25.4,
			"codex_7d_reset_at":     resetAt.Format(time.RFC3339Nano),
			// The current provider observation is newer than the persisted endpoint;
			// passive rendering must be able to calculate the next completed 1%.
			"codex_usage_updated_at":                       now.Add(-30 * time.Minute).Format(time.RFC3339Nano),
			"openai_oauth_responses_websockets_v2_enabled": true,
			openAIWeeklyEstimateBaselineKey:                openAIWeeklyFrozenEstimateStateUpdate(state)[openAIWeeklyEstimateBaselineKey],
		},
	}
	stats := &readOnlyUsageStatsRepo{stats: &usagestats.AccountStats{
		Requests: 20, Tokens: 300, Cost: 123.4567, StandardCost: 111.2222, UserCost: 45.6789,
	}}
	svc := &AccountUsageService{
		accountRepo: &readOnlyUsageAccountRepo{account: account}, usageLogRepo: stats, cache: NewUsageCache(),
	}
	return svc, account, stats
}

func TestOpenAIPassiveUsageReadsSharedSnapshotWithoutUpstreamOrWrites(t *testing.T) {
	for _, shadow := range []bool{false, true} {
		t.Run(strconv.FormatBool(shadow), func(t *testing.T) {
			svc, account, stats := readOnlyUsageFixture()
			var upstreamCalls atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				upstreamCalls.Add(1)
				w.WriteHeader(http.StatusProxyAuthRequired)
			}))
			defer server.Close()
			proxyURL, err := url.Parse(server.URL)
			require.NoError(t, err)
			port, err := strconv.Atoi(proxyURL.Port())
			require.NoError(t, err)
			proxyID := int64(1)
			account.ProxyID = &proxyID
			account.Proxy = &Proxy{Protocol: "http", Host: proxyURL.Hostname(), Port: port}
			if shadow {
				parentID := int64(1)
				account.ParentAccountID = &parentID
				account.QuotaDimension = QuotaDimensionSpark
				// QueryUsage would need to resolve the parent and fail this test.
				svc.openAIQuotaService = NewOpenAIQuotaService(svc.accountRepo, nil, nil, newQuotaRedirectingFactory(server))
			}
			before, err := json.Marshal(account)
			require.NoError(t, err)
			usage, err := svc.GetPassiveUsage(context.Background(), account.ID)
			require.NoError(t, err)
			require.Equal(t, "passive", usage.Source)
			require.NotNil(t, usage.UpdatedAt)
			require.Equal(t, account.Extra["codex_usage_updated_at"], usage.UpdatedAt.Format(time.RFC3339Nano))
			require.Equal(t, 12.5, usage.FiveHour.Utilization)
			require.Equal(t, 25.4, usage.SevenDay.Utilization)
			require.Equal(t, windowStatsFromAccountStats(stats.stats), usage.FiveHour.WindowStats)
			require.Equal(t, windowStatsFromAccountStats(stats.stats), usage.SevenDay.WindowStats)
			require.NotNil(t, usage.SevenDay.WeeklyEstimateUSD)
			require.Equal(t, 2400.0, *usage.SevenDay.WeeklyEstimateUSD)
			require.Equal(t, []time.Time{usage.FiveHour.ResetsAt.Add(-5 * time.Hour), usage.SevenDay.ResetsAt.Add(-7 * 24 * time.Hour)}, stats.starts)
			require.Zero(t, upstreamCalls.Load())
			svc.cache.openAIProbeCache.Range(func(_, _ any) bool { t.Error("passive usage populated probe cache"); return false })
			svc.openAIProbeStates.Range(func(_, _ any) bool { t.Error("passive usage created probe state"); return false })
			after, err := json.Marshal(account)
			require.NoError(t, err)
			require.JSONEq(t, string(before), string(after))
		})
	}
}

func TestOpenAIPassiveUsageRejectsInvalidOrReorderedEstimateState(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*Account, *readOnlyUsageStatsRepo)
	}{
		{"no state", func(a *Account, _ *readOnlyUsageStatsRepo) { delete(a.Extra, openAIWeeklyEstimateBaselineKey) }},
		{"identity changed", func(a *Account, _ *readOnlyUsageStatsRepo) { a.Credentials["chatgpt_account_id"] = "another-account" }},
		{"percentage regressed", func(a *Account, _ *readOnlyUsageStatsRepo) { a.Extra["codex_7d_used_percent"] = 24.9 }},
		{"cost regressed", func(_ *Account, r *readOnlyUsageStatsRepo) { r.stats.Cost = 100 }},
		{"expired window", func(a *Account, _ *readOnlyUsageStatsRepo) {
			a.Extra["codex_7d_reset_at"] = time.Now().Add(-time.Hour).Format(time.RFC3339Nano)
		}},
		{"new window", func(a *Account, _ *readOnlyUsageStatsRepo) {
			a.Extra["codex_7d_reset_at"] = time.Now().Add(7 * 24 * time.Hour).Format(time.RFC3339Nano)
		}},
		{"missing observation", func(a *Account, _ *readOnlyUsageStatsRepo) { delete(a.Extra, "codex_usage_updated_at") }},
		{"older observation", func(a *Account, _ *readOnlyUsageStatsRepo) {
			a.Extra["codex_usage_updated_at"] = time.Now().Add(-2 * time.Hour).Format(time.RFC3339Nano)
		}},
		{"legacy state", func(a *Account, _ *readOnlyUsageStatsRepo) {
			baseline, ok := a.Extra[openAIWeeklyEstimateBaselineKey].(map[string]any)
			require.True(t, ok)
			baseline["version"] = openAIWeeklyFrozenEstimateLegacyStateVersion
		}},
		{"stats unavailable", func(_ *Account, r *readOnlyUsageStatsRepo) { r.err = errors.New("database unavailable") }},
	} {
		t.Run(test.name, func(t *testing.T) {
			svc, account, stats := readOnlyUsageFixture()
			test.change(account, stats)
			before, err := json.Marshal(account)
			require.NoError(t, err)
			usage, err := svc.GetPassiveUsage(context.Background(), account.ID)
			require.NoError(t, err)
			require.NotNil(t, usage.SevenDay)
			require.Nil(t, usage.SevenDay.WeeklyEstimateUSD)
			after, err := json.Marshal(account)
			require.NoError(t, err)
			require.JSONEq(t, string(before), string(after))
		})
	}
}

func TestOpenAIPassiveUsageRecalculatesEveryCompletedPercentWithoutWriting(t *testing.T) {
	tests := []struct {
		name         string
		percent      float64
		cost         float64
		wantEstimate float64
	}{
		// The fixture is a mid-window join at 20%/$0 with a completed 25%/$120
		// endpoint. Each additional raw percentage point gets a new estimate.
		{name: "next percent", percent: 26, cost: 140, wantEstimate: 140.0 / 6 * 100},
		{name: "multiple percents", percent: 43, cost: 1012.8025122, wantEstimate: 1012.8025122 / 23 * 100},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			svc, account, stats := readOnlyUsageFixture()
			account.Extra["codex_7d_used_percent"] = tc.percent
			account.Extra["codex_usage_updated_at"] = time.Now().UTC().Add(-10 * time.Minute).Format(time.RFC3339Nano)
			stats.stats.Cost = tc.cost
			stats.rangeStats = &usagestats.AccountStats{Cost: tc.cost}

			before, err := json.Marshal(account)
			require.NoError(t, err)
			usage, err := svc.GetPassiveUsage(context.Background(), account.ID)
			require.NoError(t, err)
			require.NotNil(t, usage.SevenDay.WeeklyEstimateUSD)
			require.InDelta(t, tc.wantEstimate, *usage.SevenDay.WeeklyEstimateUSD, 1e-9)
			require.Equal(t, 1, stats.rangeCalls)

			after, err := json.Marshal(account)
			require.NoError(t, err)
			require.JSONEq(t, string(before), string(after), "passive recalculation must not persist account state")
		})
	}
}

func TestOpenAIPassiveUsageRecalculatesLegacyRuleBAtNewPercent(t *testing.T) {
	svc, account, stats := readOnlyUsageFixture()
	baseline, ok := account.Extra[openAIWeeklyEstimateBaselineKey].(map[string]any)
	require.True(t, ok)
	baseline["mode"] = openAIWeeklyEstimateModeLegacy
	baseline["baseline_source"] = "v14_persisted_zero_start"
	baseline["baseline_percent"] = 0.0
	baseline["baseline_cost"] = 0.0
	baseline["snapshot_percent"] = 29.0
	baseline["snapshot_cost"] = 690.4410962
	baseline["percent_bucket"] = 29.0
	baseline["completed_percent"] = 29.0
	baseline["completed_cost"] = 690.4410962
	baseline["estimate_usd"] = 690.4410962 / 0.28
	baseline["has_weekly_estimate"] = true
	baseline["awaiting_interval"] = false
	account.Extra["codex_7d_used_percent"] = 43.0
	account.Extra["codex_usage_updated_at"] = time.Now().UTC().Add(-10 * time.Minute).Format(time.RFC3339Nano)
	stats.stats.Cost = 1012.8025122
	stats.rangeStats = &usagestats.AccountStats{Cost: 1012.8025122}

	before, err := json.Marshal(account)
	require.NoError(t, err)
	usage, err := svc.GetPassiveUsage(context.Background(), account.ID)
	require.NoError(t, err)
	require.NotNil(t, usage.SevenDay.WeeklyEstimateUSD)
	require.InDelta(t, 1012.8025122/0.42, *usage.SevenDay.WeeklyEstimateUSD, 1e-9)

	after, err := json.Marshal(account)
	require.NoError(t, err)
	require.JSONEq(t, string(before), string(after), "passive recalculation must not persist account state")
}

func TestOpenAIPassiveUsageCompleteWindowIsExactIncludingZero(t *testing.T) {
	for _, cost := range []float64{0, 123.456789} {
		svc, account, stats := readOnlyUsageFixture()
		account.Extra["codex_7d_used_percent"] = 100.0
		delete(account.Extra, openAIWeeklyEstimateBaselineKey)
		stats.stats.Cost = cost
		usage, err := svc.GetPassiveUsage(context.Background(), account.ID)
		require.NoError(t, err)
		require.NotNil(t, usage.SevenDay.WeeklyEstimateUSD)
		require.Equal(t, cost, *usage.SevenDay.WeeklyEstimateUSD)
	}
}

func TestOpenAIPassiveUsageMissingWindowsDoesNotCreateWeeklyState(t *testing.T) {
	svc, account, stats := readOnlyUsageFixture()
	account.Extra = nil
	usage, err := svc.GetPassiveUsage(context.Background(), account.ID)
	require.NoError(t, err)
	require.Nil(t, usage.SevenDay)
	require.Nil(t, usage.UpdatedAt)
	require.Zero(t, usage.FiveHour.Utilization)
	require.Equal(t, windowStatsFromAccountStats(stats.stats), usage.FiveHour.WindowStats)
	require.Len(t, stats.starts, 1)
	require.Nil(t, account.Extra)
}

func TestOpenAIPassiveUsageSharedEndpointDoesNotDependOnLocalRangeRead(t *testing.T) {
	for _, mode := range []string{openAIWeeklyEstimateModeLegacy, openAIWeeklyEstimateModeJoinAverage} {
		t.Run(mode, func(t *testing.T) {
			svc, account, stats := readOnlyUsageFixture()
			state, ok := readOpenAIWeeklyFrozenEstimateState(account.Extra)
			require.True(t, ok)
			state.Mode = mode
			if mode == openAIWeeklyEstimateModeLegacy {
				state.BaselinePercent, state.BaselineCost = 0, 0
				state.EstimateUSD = state.SnapshotCost / 0.24
			}
			account.Extra[openAIWeeklyEstimateBaselineKey] = openAIWeeklyFrozenEstimateStateUpdate(state)[openAIWeeklyEstimateBaselineKey]
			account.Extra["codex_7d_used_percent"] = state.SnapshotPercent
			account.Extra["codex_usage_updated_at"] = state.ObservedAt.Format(time.RFC3339Nano)
			stats.rangeErr = errors.New("synthetic bounded aggregation unavailable")
			before, err := json.Marshal(account)
			require.NoError(t, err)

			usage, err := svc.GetPassiveUsage(context.Background(), account.ID)
			require.NoError(t, err)
			require.NotNil(t, usage.SevenDay.WeeklyEstimateUSD)
			require.Equal(t, state.EstimateUSD, *usage.SevenDay.WeeklyEstimateUSD)
			require.Zero(t, stats.rangeCalls, "the owner already saved this exact endpoint")
			after, err := json.Marshal(account)
			require.NoError(t, err)
			require.JSONEq(t, string(before), string(after))
		})
	}
}

func TestOpenAIPassiveUsageLocalCacheCannotRebaseSharedState(t *testing.T) {
	for _, cachedCost := range []float64{0, 100, 120, 900} {
		t.Run(strconv.FormatFloat(cachedCost, 'f', -1, 64), func(t *testing.T) {
			svc, account, stats := readOnlyUsageFixture()
			account.Extra["codex_7d_used_percent"] = 26.4
			stats.stats.Cost = 150
			stats.rangeStats = &usagestats.AccountStats{Cost: 140}
			observed, ok := openAICodexSnapshotObservationAt(account, time.Now())
			require.True(t, ok)
			resetText, ok := account.Extra["codex_7d_reset_at"].(string)
			require.True(t, ok)
			reset, err := parseTime(resetText)
			require.NoError(t, err)
			svc.storeOpenAIWeeklyEstimateStats(account.ID, reset.Add(-7*24*time.Hour), observed,
				&usagestats.AccountStats{Cost: cachedCost})
			before, err := json.Marshal(account)
			require.NoError(t, err)

			usage, err := svc.GetPassiveUsage(context.Background(), account.ID)
			require.NoError(t, err)
			require.NotNil(t, usage.SevenDay.WeeklyEstimateUSD)
			require.InDelta(t, 140.0/(26.4-20)*100, *usage.SevenDay.WeeklyEstimateUSD, 1e-9)
			require.Equal(t, 1, stats.rangeCalls)
			after, err := json.Marshal(account)
			require.NoError(t, err)
			require.JSONEq(t, string(before), string(after))
		})
	}
}

func TestOpenAIPassiveUsageKeepsFrozenPercentAcrossResetETAJitter(t *testing.T) {
	for _, mode := range []string{openAIWeeklyEstimateModeLegacy, openAIWeeklyEstimateModeJoinAverage} {
		for _, drift := range []time.Duration{19 * time.Second, 36 * time.Second} {
			t.Run(mode+"/"+drift.String(), func(t *testing.T) {
				svc, account, stats := readOnlyUsageFixture()
				state, ok := readOpenAIWeeklyFrozenEstimateState(account.Extra)
				require.True(t, ok)
				state.Mode, state.AwaitingInterval = mode, true
				state.BaselineSource, state.SnapshotCost = "cost_regression", 122
				if mode == openAIWeeklyEstimateModeLegacy {
					state.BaselinePercent, state.BaselineCost = 0, 0
					state.EstimateUSD = state.CompletedCost / 0.24
				}
				account.Extra[openAIWeeklyEstimateBaselineKey] = openAIWeeklyFrozenEstimateStateUpdate(state)[openAIWeeklyEstimateBaselineKey]
				account.Extra["codex_7d_used_percent"] = state.SnapshotPercent
				account.Extra["codex_7d_reset_at"] = state.ResetAt.Add(drift).Format(time.RFC3339Nano)
				// A later ETA moves the aggregation's lower bound. That different
				// range is not evidence that the owner's frozen endpoint regressed.
				stats.rangeStats = &usagestats.AccountStats{Cost: 100}
				before, err := json.Marshal(account)
				require.NoError(t, err)

				usage, err := svc.GetPassiveUsage(context.Background(), account.ID)
				require.NoError(t, err)
				require.NotNil(t, usage.SevenDay.WeeklyEstimateUSD)
				require.Equal(t, state.EstimateUSD, *usage.SevenDay.WeeklyEstimateUSD)
				require.Zero(t, stats.rangeCalls)
				after, err := json.Marshal(account)
				require.NoError(t, err)
				require.JSONEq(t, string(before), string(after))
			})
		}
	}
}

func TestOpenAIPassiveUsageFrozenPercentStillValidatesScopeAndRegression(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*Account, *readOnlyUsageStatsRepo)
	}{
		{"identity changed", func(a *Account, _ *readOnlyUsageStatsRepo) { a.Credentials["chatgpt_account_id"] = "other" }},
		{"new week", func(a *Account, _ *readOnlyUsageStatsRepo) {
			a.Extra["codex_7d_reset_at"] = time.Now().Add(7 * 24 * time.Hour).Format(time.RFC3339Nano)
		}},
		{"old observation", func(a *Account, _ *readOnlyUsageStatsRepo) {
			a.Extra["codex_usage_updated_at"] = time.Now().Add(-2 * time.Hour).Format(time.RFC3339Nano)
		}},
		{"missing observation", func(a *Account, _ *readOnlyUsageStatsRepo) { delete(a.Extra, "codex_usage_updated_at") }},
		{"actual cost regressed", func(_ *Account, r *readOnlyUsageStatsRepo) { r.stats.Cost = 100 }},
		{"missing baseline", func(a *Account, _ *readOnlyUsageStatsRepo) { delete(a.Extra, openAIWeeklyEstimateBaselineKey) }},
		{"unknown baseline", func(a *Account, _ *readOnlyUsageStatsRepo) {
			state, ok := readOpenAIWeeklyFrozenEstimateState(a.Extra)
			require.True(t, ok)
			state.Mode, state.HasEstimate = openAIWeeklyEstimateModeUnknown, false
			a.Extra[openAIWeeklyEstimateBaselineKey] = openAIWeeklyFrozenEstimateStateUpdate(state)[openAIWeeklyEstimateBaselineKey]
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, account, stats := readOnlyUsageFixture()
			account.Extra["codex_7d_used_percent"] = 25.0
			tc.change(account, stats)
			before, err := json.Marshal(account)
			require.NoError(t, err)
			usage, err := svc.GetPassiveUsage(context.Background(), account.ID)
			require.NoError(t, err)
			require.Nil(t, usage.SevenDay.WeeklyEstimateUSD)
			after, err := json.Marshal(account)
			require.NoError(t, err)
			require.JSONEq(t, string(before), string(after))
		})
	}
}
