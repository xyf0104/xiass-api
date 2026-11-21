package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func weekendTime(value string) time.Time {
	at, err := time.Parse(time.RFC3339, value)
	if err != nil {
		panic(err)
	}
	return at
}

func TestWeekendDiscountBoundaries(t *testing.T) {
	s := WeekendDiscountSettings{Enabled: true}
	for _, tt := range []struct {
		at   string
		want float64
	}{
		{"2026-11-20T15:59:59Z", 1},
		{"2026-11-20T16:00:00Z", .8},
		{"2026-11-22T15:59:59Z", .8},
		{"2026-11-22T16:00:00Z", 1},
		{"2026-11-27T16:00:00Z", .8},
	} {
		t.Run(tt.at, func(t *testing.T) { require.Equal(t, tt.want, s.MultiplierAt(weekendTime(tt.at))) })
	}
	start, end := weekendTime("2026-11-21T12:00:00+08:00"), weekendTime("2026-11-22T12:00:00+08:00")
	s.StartsAt, s.EndsAt = &start, &end
	require.Equal(t, 1.0, s.MultiplierAt(start.Add(-time.Nanosecond)))
	require.Equal(t, .8, s.MultiplierAt(start))
	require.Equal(t, .8, s.MultiplierAt(end.Add(-time.Nanosecond)))
	require.Equal(t, 1.0, s.MultiplierAt(end))
	s.Enabled = false
	require.Equal(t, 1.0, s.MultiplierAt(start))
}

func TestWeekendDiscountCustomSchedule(t *testing.T) {
	start := weekendTime("2026-11-20T14:00:00+08:00")
	end := weekendTime("2026-11-23T15:00:00+08:00")
	s := WeekendDiscountSettings{Enabled: true, Mode: DiscountModeCustom, StartsAt: &start, EndsAt: &end}
	require.NoError(t, s.Validate())
	for _, tt := range []struct {
		at time.Time
		want float64
		next *time.Time
	}{
		{start.Add(-time.Nanosecond), 1, &start},
		{start, .8, &end},
		{weekendTime("2026-11-21T00:00:00+08:00"), .8, &end},
		{weekendTime("2026-11-23T00:00:00+08:00"), .8, &end},
		{end.Add(-time.Nanosecond), .8, &end},
		{end, 1, nil},
	} {
		t.Run(tt.at.String(), func(t *testing.T) {
			status := s.Status(tt.at)
			require.Equal(t, tt.want, status.AppliedMultiplier)
			require.Equal(t, tt.want < 1, status.Active)
			require.Equal(t, tt.next, status.NextChangeAt)
		})
	}
	s.StartsAt, s.EndsAt = nil, nil
	require.Equal(t, .8, s.MultiplierAt(start), "an unbounded custom activity starts immediately on a weekday")
	require.Nil(t, s.Status(start).NextChangeAt)
	s.Enabled = false
	require.Equal(t, 1.0, s.MultiplierAt(start))
	s.Enabled, s.Mode = true, ""
	require.Equal(t, 1.0, s.MultiplierAt(start), "legacy settings default to weekends")
	require.Equal(t, DiscountModeWeekends, s.Status(start).Mode)
	s.Mode = "unknown"
	require.Error(t, s.Validate())
	require.Equal(t, 1.0, s.MultiplierAt(start))
}

func TestWeekendDiscountCustomPersist(t *testing.T) {
	ctx := context.Background()
	repo := &panelRateLimitSettingRepo{}
	svc := NewSettingService(repo, nil)
	start := weekendTime("2026-11-20T14:00:00+08:00")
	want := WeekendDiscountSettings{Enabled: true, Mode: DiscountModeCustom, StartsAt: &start}
	require.NoError(t, svc.SetWeekendDiscount(ctx, want))
	restarted := NewSettingService(repo, nil)
	got, err := restarted.GetWeekendDiscount(ctx)
	require.NoError(t, err)
	require.Equal(t, want, got)
	require.Equal(t, .8, restarted.WeekendMultiplierAt(ctx, start))
	require.NoError(t, restarted.SetWeekendDiscount(ctx, WeekendDiscountSettings{Enabled: true, Mode: DiscountModeWeekends}))
	require.Equal(t, 1.0, restarted.WeekendMultiplierAt(ctx, start), "switching back restores weekday prices")
}

func TestWeekendDiscountNextTransition(t *testing.T) {
	s := WeekendDiscountSettings{Enabled: true}
	for _, tt := range []struct{ at, next string }{
		{"2026-11-20T12:00:00+08:00", "2026-11-21T00:00:00+08:00"},
		{"2026-11-21T00:00:00+08:00", "2026-11-23T00:00:00+08:00"},
		{"2026-11-22T23:59:59+08:00", "2026-11-23T00:00:00+08:00"},
		{"2026-11-23T00:00:00+08:00", "2026-11-28T00:00:00+08:00"},
	} {
		status := s.Status(weekendTime(tt.at))
		require.NotNil(t, status.NextChangeAt)
		require.True(t, weekendTime(tt.next).Equal(*status.NextChangeAt))
	}
	start, end := weekendTime("2026-11-21T12:00:00+08:00"), weekendTime("2026-11-21T13:00:00+08:00")
	s.StartsAt, s.EndsAt = &start, &end
	require.True(t, start.Equal(*s.Status(start.Add(-time.Hour)).NextChangeAt))
	require.Equal(t, end, *s.Status(start).NextChangeAt)
	require.Nil(t, s.Status(end).NextChangeAt)
	end = start.Add(-time.Hour)
	require.Error(t, s.Validate())
}

func TestWeekendDiscountSettingsPersistAndRefresh(t *testing.T) {
	ctx := context.Background()
	repo := &panelRateLimitSettingRepo{}
	svc := NewSettingService(repo, nil)
	s, err := svc.GetWeekendDiscount(ctx)
	require.NoError(t, err)
	require.False(t, s.Enabled)
	start := weekendTime("2026-11-21T00:00:00+08:00")
	want := WeekendDiscountSettings{Enabled: true, StartsAt: &start}
	require.NoError(t, svc.SetWeekendDiscount(ctx, want))
	s, err = svc.GetWeekendDiscount(ctx)
	require.NoError(t, err)
	require.Equal(t, want, s)
	restarted := NewSettingService(repo, nil)
	s, err = restarted.GetWeekendDiscount(ctx)
	require.NoError(t, err)
	require.Equal(t, .8, s.MultiplierAt(start))
	for i := 0; i < 20; i++ {
		_, err = restarted.GetWeekendDiscount(ctx)
		require.NoError(t, err)
	}
	require.Equal(t, 2, repo.getValueCalls)
	require.NoError(t, restarted.SetWeekendDiscount(ctx, WeekendDiscountSettings{}))
	require.Equal(t, 1.0, restarted.WeekendMultiplierAt(ctx, start))

	repo.getValueErr = errors.New("temporary database failure")
	svc.weekendDiscountCache.loadedAt = time.Now().Add(-time.Minute)
	s, err = svc.GetWeekendDiscount(ctx)
	require.NoError(t, err)
	require.Equal(t, .8, s.MultiplierAt(start))
	_, err = svc.GetWeekendDiscount(ctx)
	require.NoError(t, err)
	require.Equal(t, 3, repo.getValueCalls, "storage outages must not cause a query per request")
	_, err = NewSettingService(repo, nil).GetWeekendDiscount(ctx)
	require.Error(t, err, "do not silently charge full price when the schedule cannot be loaded")
}

func TestWeekendDiscountFinalSettlement(t *testing.T) {
	ctx := context.Background()
	svc := NewSettingService(&panelRateLimitSettingRepo{}, nil)
	require.NoError(t, svc.SetWeekendDiscount(ctx, WeekendDiscountSettings{Enabled: true}))
	for _, subscription := range []bool{false, true} {
		cost := &CostBreakdown{InputCost: 8, OutputCost: 2, TotalCost: 10, ActualCost: 3}
		log := &UsageLog{RateMultiplier: .3, TotalCost: 10, ActualCost: 3, CreatedAt: time.Now()}
		p := &postUsageBillingParams{
			PricingAt: weekendTime("2026-11-22T23:59:59+08:00"), Cost: cost,
			User: &User{ID: 1}, APIKey: &APIKey{ID: 2, Quota: 100, RateLimit5h: 10},
			Account:               &Account{ID: 3, Type: AccountTypeAPIKey, Extra: map[string]any{"quota_limit": 100.0}},
			AccountRateMultiplier: .25, APIKeyService: &openAIRecordUsageAPIKeyQuotaStub{},
			IsSubscriptionBill: subscription, Subscription: &UserSubscription{ID: 4},
		}
		require.NoError(t, applyWeekendUsageDiscount(ctx, svc, log, p))
		require.NoError(t, applyWeekendUsageDiscount(ctx, svc, log, p))
		require.InDelta(t, 2.4, log.ActualCost, 1e-10)
		require.InDelta(t, .24, log.RateMultiplier, 1e-10)
		require.Equal(t, 10.0, log.TotalCost)
		require.Equal(t, 3.0, cost.ActualCost, "caller-owned cost must remain unchanged")
		cmd := buildUsageBillingCommand("weekend", log, p)
		if subscription {
			require.InDelta(t, 2.4, cmd.SubscriptionCost, 1e-10)
			require.Zero(t, cmd.BalanceCost)
		} else {
			require.InDelta(t, 2.4, cmd.BalanceCost, 1e-10)
		}
		require.InDelta(t, 2.4, cmd.APIKeyQuotaCost, 1e-10)
		require.InDelta(t, 2.4, cmd.APIKeyRateLimitCost, 1e-10)
		require.InDelta(t, 2.5, cmd.AccountQuotaCost, 1e-10, "upstream cost is not discounted")
	}
}

func TestWeekendDiscountOpenAIRecordUsage(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, schedule := range []struct{ config, normal, discounted string }{
		{`{"enabled":true}`, "2026-11-20T12:00:00+08:00", "2026-11-21T12:00:00+08:00"},
		{`{"enabled":true,"mode":"custom","starts_at":"2026-11-20T13:00:00+08:00"}`, "2026-11-20T12:00:00+08:00", "2026-11-20T13:00:00+08:00"},
	} {
	for _, finalPrice := range []bool{false, true} {
		var normal *UsageLog
		for _, at := range []string{schedule.normal, schedule.discounted} {
			usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
			billRepo := &openAIRecordUsageBillingRepoStub{}
			svc := newOpenAIRecordUsageServiceWithBillingRepoForTest(usageRepo, billRepo, &openAIRecordUsageUserRepoStub{}, &openAIRecordUsageSubRepoStub{}, nil)
			svc.settingService = NewSettingService(&panelRateLimitSettingRepo{values: map[string]string{SettingKeyWeekendDiscount: schedule.config}}, nil)
			svc.resolver = NewModelPricingResolver(nil, svc.billingService)
			group := &Group{ID: 1, Platform: PlatformOpenAI, RateMultiplier: .218, LongContextPricingEnabled: true}
			if finalPrice {
				price := 2e-6
				group.ModelPricing = []ChannelModelPricing{{Models: []string{"gpt-5.1"}, BillingMode: BillingModeToken, PriceMode: PriceModeFinal, InputPrice: &price}}
			}
			err := svc.RecordUsage(ctx, &OpenAIRecordUsageInput{
				PricingAt: weekendTime(at),
				Result:    &OpenAIForwardResult{RequestID: at, Model: "gpt-5.1", Usage: OpenAIUsage{InputTokens: 500_000, OutputTokens: 1000, CacheReadInputTokens: 100_000}, Duration: time.Second},
				APIKey:    &APIKey{ID: 2, UserID: 3, GroupID: &group.ID, Group: group}, User: &User{ID: 3}, Account: &Account{ID: 4, Platform: PlatformOpenAI},
			})
			require.NoError(t, err)
			require.NotNil(t, billRepo.lastCmd)
			require.Greater(t, billRepo.lastCmd.BalanceCost, 0.0)
			require.NoError(t, billRepo.lastCtxErr, "billing survives a disconnected client")
			if normal == nil {
				normal = usageRepo.lastLog
				continue
			}
			require.InDelta(t, normal.ActualCost*.8, billRepo.lastCmd.BalanceCost, 1e-10)
			require.InDelta(t, normal.ActualCost*.8, usageRepo.lastLog.ActualCost, 1e-10)
			require.Equal(t, normal.TotalCost, usageRepo.lastLog.TotalCost)
			require.Equal(t, normal.CacheReadTokens, usageRepo.lastLog.CacheReadTokens)
		}
	}
	}
}
