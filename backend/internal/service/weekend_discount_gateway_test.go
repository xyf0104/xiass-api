//go:build unit

package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestWeekendDiscountSharedGatewayPlatforms(t *testing.T) {
	for _, platform := range []string{PlatformAnthropic, PlatformGemini, PlatformAntigravity, PlatformGrok, "deepseek"} {
		t.Run(platform, func(t *testing.T) {
			for _, schedule := range []struct{ config, normal, discounted string }{
				{`{"enabled":true}`, "2026-11-20T12:00:00+08:00", "2026-11-21T12:00:00+08:00"},
				{`{"enabled":true,"mode":"custom","starts_at":"2026-11-20T13:00:00+08:00"}`, "2026-11-20T12:00:00+08:00", "2026-11-20T13:00:00+08:00"},
			} {
			var normal *UsageLog
			for _, at := range []string{schedule.normal, schedule.discounted} {
				usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
				billRepo := &openAIRecordUsageBillingRepoStub{}
				svc := newGatewayRecordUsageServiceWithBillingRepoForTest(usageRepo, billRepo, &openAIRecordUsageUserRepoStub{}, &openAIRecordUsageSubRepoStub{})
				svc.settingService = NewSettingService(&panelRateLimitSettingRepo{values: map[string]string{SettingKeyWeekendDiscount: schedule.config}}, nil)
				group := &Group{ID: 1, Platform: platform, RateMultiplier: .4, SubscriptionType: SubscriptionTypeSubscription}
				err := svc.RecordUsage(context.Background(), &RecordUsageInput{
					PricingAt: weekendTime(at),
					Result:    &ForwardResult{RequestID: platform + at, Model: "claude-sonnet-4", Usage: ClaudeUsage{InputTokens: 20_000, OutputTokens: 1000, CacheReadInputTokens: 10_000, CacheCreationInputTokens: 1000}, Duration: time.Second},
					APIKey:    &APIKey{ID: 2, GroupID: &group.ID, Group: group}, User: &User{ID: 3}, Account: &Account{ID: 4, Platform: platform}, Subscription: &UserSubscription{ID: 5},
				})
				require.NoError(t, err)
				require.NotNil(t, billRepo.lastCmd)
				require.Greater(t, billRepo.lastCmd.SubscriptionCost, 0.0)
				if normal == nil {
					normal = usageRepo.lastLog
					continue
				}
				require.InDelta(t, normal.ActualCost*.8, billRepo.lastCmd.SubscriptionCost, 1e-10)
				require.InDelta(t, normal.ActualCost*.8, usageRepo.lastLog.ActualCost, 1e-10)
				require.Equal(t, normal.TotalCost, usageRepo.lastLog.TotalCost)
				require.Equal(t, normal.CacheReadCost, usageRepo.lastLog.CacheReadCost)
			}
			}
		})
	}
}
