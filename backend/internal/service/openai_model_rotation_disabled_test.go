package service

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type modelRotationDisabledStore struct {
	schedulerTestGatewayCache
	reads  int
	writes int
}

func (c *modelRotationDisabledStore) GetOpenAIModelRotationFailures(context.Context, string, time.Time) ([]int64, error) {
	c.reads++
	return []int64{31, 32, 33}, nil
}

func (c *modelRotationDisabledStore) ObserveOpenAIModelRotation(context.Context, string, int64, time.Time, time.Time, time.Duration) error {
	c.writes++
	return nil
}

func TestOpenAIModelRotationDisabledBypassesStoreAndMatchesPreviousScheduler(t *testing.T) {
	for _, mode := range []string{"advanced", "legacy_batch", "legacy_no_batch"} {
		for _, flag := range []string{"priority_off", "rotation_off", "both_off"} {
			t.Run(mode+"/"+flag, func(t *testing.T) {
				svc, key, accounts := modelRotationTestGateway(t, mode)
				ctx := modelRotationTestContext(key.UserID, key.ID)
				store := &modelRotationDisabledStore{}
				svc.cache = store
				settings, err := svc.settingService.GetOpenAIModelPrioritySettings(ctx)
				require.NoError(t, err)
				settings.Enabled = flag == "rotation_off"
				settings.SmartRotationEnabled = flag == "priority_off"
				require.NoError(t, svc.settingService.SetOpenAIModelPrioritySettings(ctx, settings))
				now := time.Now()
				scope := openAIModelRotationScope(ctx, key.GroupID, "gpt-6-astra")
				for _, account := range accounts {
					svc.openaiModelRotation.observe(scope, account.ID, openAIModelRotationObservation{
						startedAt: now, blockedUntil: now.Add(time.Hour), expiresAt: now.Add(time.Hour), pending: true,
					}, now)
				}
				for _, excluded := range []map[int64]struct{}{nil, {31: {}}} {
					selected, decision := selectModelRotationAccount(t, svc, key, "gpt-6-astra", "", excluded)
					previous, _, err := svc.selectAccountWithSchedulerWithProxyFallback(ctx, key.GroupID, "", "", "gpt-6-astra", excluded,
						OpenAIUpstreamTransportAny, OpenAIEndpointCapability(""), OpenAIImagesCapability(""), false, PlatformOpenAI, false, false)
					require.NoError(t, err)
					require.NotNil(t, previous)
					if previous.ReleaseFunc != nil {
						previous.ReleaseFunc()
					}
					require.Equal(t, previous.Account.ID, selected)
					require.False(t, decision.ModelRotationFallback)
				}
				svc.ObserveOpenAIModelRotation(ctx, key, &accounts[0], "gpt-6-astra", &OpenAIForwardResult{UpstreamResponseModel: "gpt-5.6-luna"})
				require.False(t, svc.ShouldRotateOpenAIModelAccount(ctx, key.GroupID, "gpt-6-astra", 31))
				require.Zero(t, store.reads)
				require.Zero(t, store.writes)
			})
		}
	}
}

func TestOpenAIModelRotationColdSettingsNeverWaitForDatabase(t *testing.T) {
	gate := make(chan struct{})
	defer close(gate)
	settings := NewSettingService(&openAIModelPriorityRepoStub{values: map[string]string{}, getGate: gate}, &config.Config{})
	store := &modelRotationDisabledStore{}
	svc := &OpenAIGatewayService{settingService: settings, cache: store}
	ctx := modelRotationTestContext(101, 201)
	groupID := int64(12345)
	done := make(chan []int64, 1)
	go func() { done <- svc.openAIModelRotationExclusions(ctx, &groupID, "gpt-6-astra") }()
	select {
	case result := <-done:
		require.Empty(t, result)
	case <-time.After(200 * time.Millisecond):
		t.Fatal("rotation lookup waited for the settings database")
	}
	require.Zero(t, store.reads)
	require.Zero(t, store.writes)
}
