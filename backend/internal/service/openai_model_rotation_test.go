package service

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/stretchr/testify/require"
)

func modelRotationTestContext(userID, keyID int64) context.Context {
	ctx := context.WithValue(context.Background(), ctxkey.UserID, userID)
	return context.WithValue(ctx, ctxkey.APIKeyID, keyID)
}

func modelRotationTestGateway(t *testing.T, mode string) (*OpenAIGatewayService, *APIKey, []Account) {
	t.Helper()
	openAIAdvancedSchedulerSettingCache.Store(&cachedOpenAIAdvancedSchedulerSetting{
		enabled: mode == "advanced", expiresAt: time.Now().Add(time.Hour).UnixNano(),
	})
	t.Cleanup(resetOpenAIAdvancedSchedulerSettingCacheForTest)
	groupID := int64(12345)
	settings := NewSettingService(&openAIModelPriorityRepoStub{values: map[string]string{}}, &config.Config{})
	require.NoError(t, settings.SetOpenAIModelPrioritySettings(context.Background(), &OpenAIModelPrioritySettings{
		Enabled: true, SmartRotationEnabled: true, SmartRotationCooldownMinutes: 30,
		Rules: []OpenAIModelPriorityRule{
			{ModelPattern: "gpt-6-astra", AccountIDs: []int64{31, 32}, AccountOrder: []int64{31, 32}},
			{ModelPattern: "gpt-5.6-*", AccountIDs: []int64{31, 32}, AccountOrder: []int64{31, 32}},
		},
	}))
	accounts := []Account{
		openAIModelRoutingTestAccount(31, groupID, 5, ""),
		openAIModelRoutingTestAccount(32, groupID, 10, ""),
		openAIModelRoutingTestAccount(33, groupID, 0, ""),
	}
	cfg := &config.Config{}
	cfg.Gateway.Scheduling.LoadBatchEnabled = mode != "legacy_no_batch"
	cfg.Gateway.OpenAIWS.LBTopK = 3
	svc := &OpenAIGatewayService{
		accountRepo: schedulerGroupAwareOpenAIAccountRepo{schedulerTestOpenAIAccountRepo{accounts: accounts}},
		cache:       &schedulerTestGatewayCache{}, cfg: cfg, settingService: settings,
		concurrencyService: NewConcurrencyService(schedulerTestConcurrencyCache{}),
	}
	return svc, &APIKey{ID: 201, UserID: 101, GroupID: &groupID}, accounts
}

func selectModelRotationAccount(t *testing.T, svc *OpenAIGatewayService, key *APIKey, model, session string, excluded map[int64]struct{}) (int64, OpenAIAccountScheduleDecision) {
	t.Helper()
	selected, decision, err := svc.SelectAccountWithScheduler(modelRotationTestContext(key.UserID, key.ID), key.GroupID, "", session, model, excluded, OpenAIUpstreamTransportAny, false)
	require.NoError(t, err)
	require.NotNil(t, selected)
	if selected.ReleaseFunc != nil {
		selected.ReleaseFunc()
	}
	return selected.Account.ID, decision
}

func TestOpenAIModelRotationPerCallerOrderedFallbackAcrossSchedulers(t *testing.T) {
	for _, mode := range []string{"advanced", "legacy_batch", "legacy_no_batch"} {
		t.Run(mode, func(t *testing.T) {
			svc, key, accounts := modelRotationTestGateway(t, mode)
			ctx := modelRotationTestContext(key.UserID, key.ID)
			selected, _ := selectModelRotationAccount(t, svc, key, "gpt-6-astra", "", nil)
			require.Equal(t, int64(31), selected)
			svc.ObserveOpenAIModelRotation(ctx, key, &accounts[0], "gpt-6-astra", &OpenAIForwardResult{UpstreamResponseModel: "gpt-5.6-luna"})
			selected, _ = selectModelRotationAccount(t, svc, key, "gpt-6-astra", "", nil)
			require.Equal(t, int64(32), selected)
			otherUser := *key
			otherUser.UserID++
			selected, _ = selectModelRotationAccount(t, svc, &otherUser, "gpt-6-astra", "", nil)
			require.Equal(t, int64(31), selected)
			otherKey := *key
			otherKey.ID++
			selected, _ = selectModelRotationAccount(t, svc, &otherKey, "gpt-6-astra", "", nil)
			require.Equal(t, int64(31), selected)
			selected, _ = selectModelRotationAccount(t, svc, key, "gpt-5.6-sol", "", nil)
			require.Equal(t, int64(31), selected)
			svc.ObserveOpenAIModelRotation(ctx, key, &accounts[1], "gpt-6-astra", &OpenAIForwardResult{UpstreamResponseModel: "gpt-5.6-terra"})
			selected, _ = selectModelRotationAccount(t, svc, key, "gpt-6-astra", "", nil)
			require.Equal(t, int64(33), selected, "ordinary policy is used after every preferred account mismatches")
			svc.ObserveOpenAIModelRotation(ctx, key, &accounts[2], "gpt-6-astra", &OpenAIForwardResult{UpstreamResponseModel: "gpt-5.6-luna"})
			selected, decision := selectModelRotationAccount(t, svc, key, "gpt-6-astra", "", nil)
			require.Equal(t, int64(33), selected, "all mismatch fallback remains available and uses normal priority")
			require.True(t, decision.ModelRotationFallback)
			excluded := map[int64]struct{}{33: {}}
			selected, _ = selectModelRotationAccount(t, svc, key, "gpt-6-astra", "", excluded)
			require.NotEqual(t, int64(33), selected, "transport failures must never be revived by smart fallback")
			require.Len(t, excluded, 1, "caller-owned exclusion set must remain unchanged")
		})
	}
}

func TestOpenAIModelRotationOverridesOrdinaryStickyAndCanBeDisabled(t *testing.T) {
	for _, mode := range []string{"advanced", "legacy_batch", "legacy_no_batch"} {
		t.Run(mode, func(t *testing.T) {
			svc, key, accounts := modelRotationTestGateway(t, mode)
			ctx := modelRotationTestContext(key.UserID, key.ID)
			cache, ok := svc.cache.(*schedulerTestGatewayCache)
			require.True(t, ok)
			cache.sessionBindings = map[string]int64{"session": 31}
			svc.ObserveOpenAIModelRotation(ctx, key, &accounts[0], "gpt-6-astra", &OpenAIForwardResult{UpstreamResponseModel: "gpt-5.6-luna"})
			selected, _ := selectModelRotationAccount(t, svc, key, "gpt-6-astra", "session", nil)
			require.Equal(t, int64(32), selected)
			settings, err := svc.settingService.GetOpenAIModelPrioritySettings(ctx)
			require.NoError(t, err)
			settings.SmartRotationEnabled = false
			require.NoError(t, svc.settingService.SetOpenAIModelPrioritySettings(ctx, settings))
			selected, _ = selectModelRotationAccount(t, svc, key, "gpt-6-astra", "", nil)
			require.Equal(t, int64(31), selected)
		})
	}
}

func TestOpenAIModelRotationUnknownAndConflictingDeclarationsCannotClearFailure(t *testing.T) {
	svc, key, accounts := modelRotationTestGateway(t, "advanced")
	ctx := modelRotationTestContext(key.UserID, key.ID)
	svc.ObserveOpenAIModelRotation(ctx, key, &accounts[0], "gpt-6-astra", &OpenAIForwardResult{UpstreamResponseModel: "gpt-5.6-luna"})
	for _, result := range []*OpenAIForwardResult{
		nil, {}, {UpstreamModel: "gpt-6-astra"},
		{UpstreamResponseModel: "gpt-6-astra", UpstreamResponseModelConflict: true},
	} {
		svc.ObserveOpenAIModelRotation(ctx, key, &accounts[0], "gpt-6-astra", result)
		require.ElementsMatch(t, []int64{31}, svc.openAIModelRotationExclusions(ctx, key.GroupID, "gpt-6-astra"))
	}
	svc.ObserveOpenAIModelRotation(ctx, key, &accounts[0], "gpt-6-astra", &OpenAIForwardResult{UpstreamResponseModel: "openai/gpt-6-astra-2026-09-01"})
	require.Empty(t, svc.openAIModelRotationExclusions(ctx, key.GroupID, "gpt-6-astra"))
	groupID := *key.GroupID + 1
	require.Empty(t, svc.openAIModelRotationExclusions(ctx, &groupID, "gpt-6-astra"))
	require.Empty(t, svc.openAIModelRotationExclusions(context.Background(), key.GroupID, "gpt-6-astra"))
	require.Empty(t, svc.openAIModelRotationExclusions(ctx, key.GroupID, "unconfigured-model"))
}

func TestOpenAIModelRotationAPIKeyPoolCallerIsolation(t *testing.T) {
	for _, mode := range []string{"advanced", "legacy_batch", "legacy_no_batch"} {
		t.Run(mode, func(t *testing.T) {
			svc, key, accounts := modelRotationTestGateway(t, mode)
			for i := range accounts {
				accounts[i].Type = AccountTypeAPIKey
				accounts[i].Credentials = map[string]any{"model_mapping": map[string]any{"gpt-6-astra": "gpt-6-astra"}}
			}
			ctx := modelRotationTestContext(key.UserID, key.ID)
			svc.ObserveOpenAIModelRotation(ctx, key, &accounts[0], "gpt-6-astra", &OpenAIForwardResult{UpstreamResponseModel: "gpt-5.6-luna"})
			selected, _ := selectModelRotationAccount(t, svc, key, "gpt-6-astra", "", nil)
			require.Equal(t, int64(32), selected)
			other := *key
			other.UserID++
			other.ID++
			selected, _ = selectModelRotationAccount(t, svc, &other, "gpt-6-astra", "", nil)
			require.Equal(t, int64(31), selected)
		})
	}
}

func TestOpenAIModelRotationPreservesNonmigratableResponseAffinity(t *testing.T) {
	for _, mode := range []string{"advanced", "legacy_batch", "legacy_no_batch"} {
		t.Run(mode, func(t *testing.T) {
			svc, key, accounts := modelRotationTestGateway(t, mode)
			svc.cfg.Gateway.OpenAIWS.Enabled = true
			svc.cfg.Gateway.OpenAIWS.APIKeyEnabled = true
			svc.cfg.Gateway.OpenAIWS.ResponsesWebsocketsV2 = true
			svc.cfg.Gateway.OpenAIWS.StickyResponseIDTTLSeconds = 3600
			accounts[0].Type = AccountTypeAPIKey
			accounts[0].Extra["openai_apikey_responses_websockets_v2_enabled"] = true
			accounts[0].Credentials = map[string]any{"model_mapping": map[string]any{"gpt-6-astra": "gpt-6-astra"}}
			ctx := modelRotationTestContext(key.UserID, key.ID)
			require.NoError(t, svc.getOpenAIWSStateStore().BindResponseAccount(ctx, *key.GroupID, "resp_model_rotation_bound", 31, time.Hour))
			svc.ObserveOpenAIModelRotation(ctx, key, &accounts[0], "gpt-6-astra", &OpenAIForwardResult{UpstreamResponseModel: "gpt-5.6-luna"})
			selected, decision, err := svc.SelectAccountWithScheduler(ctx, key.GroupID, "resp_model_rotation_bound", "", "gpt-6-astra", nil, OpenAIUpstreamTransportAny, false)
			require.NoError(t, err)
			require.Equal(t, int64(31), selected.Account.ID)
			require.True(t, decision.StickyPreviousHit)
			if selected.ReleaseFunc != nil {
				selected.ReleaseFunc()
			}
		})
	}
}

func TestOpenAIModelRotationMemoryExpiryOrderingAndBounds(t *testing.T) {
	var memory openAIModelRotationMemory
	now := time.Now()
	mismatch := openAIModelRotationObservation{startedAt: now, blockedUntil: now.Add(time.Minute), expiresAt: now.Add(time.Minute)}
	memory.observe("caller", 1, mismatch, now)
	memory.observe("caller", 1, openAIModelRotationObservation{startedAt: now.Add(-time.Minute), expiresAt: now.Add(time.Minute)}, now)
	require.Equal(t, []int64{1}, memory.failures("caller", now))
	require.Empty(t, memory.failures("other", now))
	require.Empty(t, memory.failures("caller", now.Add(time.Minute)))
	for i := int64(0); i < openAIModelRotationMemoryLimit+3; i++ {
		memory.observe("bounded", i, mismatch, now)
	}
	require.LessOrEqual(t, len(memory.entries), openAIModelRotationMemoryLimit)
}

func TestOpenAIModelRotationMemoryConcurrentObservations(t *testing.T) {
	var memory openAIModelRotationMemory
	var wait sync.WaitGroup
	now := time.Now()
	for i := int64(1); i <= 32; i++ {
		wait.Add(1)
		go func(id int64) {
			defer wait.Done()
			memory.observe("caller", id, openAIModelRotationObservation{startedAt: now, blockedUntil: now.Add(time.Minute), expiresAt: now.Add(time.Minute)}, now)
			memory.failures("caller", now)
		}(i)
	}
	wait.Wait()
	require.Len(t, memory.failures("caller", now), 32)
}

func TestOpenAIModelRotationExpiredPendingRestoresFastPath(t *testing.T) {
	var memory openAIModelRotationMemory
	now := time.Now()
	memory.observe("caller", 31, openAIModelRotationObservation{
		startedAt: now, blockedUntil: now.Add(time.Minute), expiresAt: now.Add(time.Minute), pending: true,
	}, now)
	require.Equal(t, 1, memory.pendingCount)
	require.Empty(t, memory.pending("other", now.Add(2*time.Minute)))
	require.Zero(t, memory.pendingCount)
	require.Empty(t, memory.entries)
}

type modelRotationSharedCache struct {
	schedulerTestGatewayCache
	shared    *openAIModelRotationMemory
	fail      bool
	failWrite bool
}

func (c *modelRotationSharedCache) GetOpenAIModelRotationFailures(_ context.Context, scope string, now time.Time) ([]int64, error) {
	if c.fail {
		return nil, errors.New("cache offline")
	}
	return c.shared.failures(scope, now), nil
}

func (c *modelRotationSharedCache) ObserveOpenAIModelRotation(_ context.Context, scope string, accountID int64, startedAt, blockedUntil time.Time, ttl time.Duration) error {
	if c.fail || c.failWrite {
		return errors.New("cache offline")
	}
	c.shared.observe(scope, accountID, openAIModelRotationObservation{startedAt: startedAt, blockedUntil: blockedUntil, expiresAt: time.Now().Add(ttl)}, time.Now())
	return nil
}

func TestOpenAIModelRotationSharedStoreAndLocalOutageFallback(t *testing.T) {
	first, key, accounts := modelRotationTestGateway(t, "advanced")
	second, _, _ := modelRotationTestGateway(t, "advanced")
	shared := &openAIModelRotationMemory{}
	firstCache := &modelRotationSharedCache{shared: shared}
	first.cache = firstCache
	second.cache = &modelRotationSharedCache{shared: shared}
	ctx := modelRotationTestContext(key.UserID, key.ID)
	first.ObserveOpenAIModelRotation(ctx, key, &accounts[0], "gpt-6-astra", &OpenAIForwardResult{UpstreamResponseModel: "gpt-5.6-luna"})
	require.ElementsMatch(t, []int64{31}, second.openAIModelRotationExclusions(ctx, key.GroupID, "gpt-6-astra"))
	firstCache.fail = true
	require.ElementsMatch(t, []int64{31}, first.openAIModelRotationExclusions(ctx, key.GroupID, "gpt-6-astra"))
}

func TestNormalizeOpenAIModelRotationModelDoesNotConflateFamilies(t *testing.T) {
	for input, expected := range map[string]string{
		"GPT-6": "gpt-6-astra", "openai/gpt-6-astra-2026-09-01": "gpt-6-astra",
		"gpt-5.6": "gpt-5.6-sol", "gpt-5.6-luna": "gpt-5.6-luna",
		"gpt-5.6-terra": "gpt-5.6-terra", "gpt-6-other": "gpt-6-other",
		"not-gpt-6-astra": "not-gpt-6-astra", "": "",
	} {
		require.Equal(t, expected, normalizeOpenAIModelRotationModel(input))
	}
}

func TestOpenAIModelRotationRetainsPendingDecisionsWhenRedisReadsRecover(t *testing.T) {
	svc, key, accounts := modelRotationTestGateway(t, "advanced")
	shared := &openAIModelRotationMemory{}
	cache := &modelRotationSharedCache{shared: shared, failWrite: true}
	svc.cache = cache
	ctx := modelRotationTestContext(key.UserID, key.ID)
	svc.ObserveOpenAIModelRotation(ctx, key, &accounts[0], "gpt-6-astra", &OpenAIForwardResult{UpstreamResponseModel: "gpt-5.6-luna"})
	require.ElementsMatch(t, []int64{31}, svc.openAIModelRotationExclusions(ctx, key.GroupID, "gpt-6-astra"), "a successful empty read must not discard a failed mismatch write")
	cache.failWrite = false
	require.ElementsMatch(t, []int64{31}, svc.openAIModelRotationExclusions(ctx, key.GroupID, "gpt-6-astra"))
	scope := openAIModelRotationScope(ctx, key.GroupID, "gpt-6-astra")
	require.Empty(t, svc.openaiModelRotation.pending(scope, time.Now()))
	require.ElementsMatch(t, []int64{31}, shared.failures(scope, time.Now()))
	cache.failWrite = true
	svc.ObserveOpenAIModelRotation(ctx, key, &accounts[0], "gpt-6-astra", &OpenAIForwardResult{UpstreamResponseModel: "gpt-6-astra"})
	require.Empty(t, svc.openAIModelRotationExclusions(ctx, key.GroupID, "gpt-6-astra"), "a failed clear write must override a stale remote mismatch locally")
	cache.failWrite = false
	require.Empty(t, svc.openAIModelRotationExclusions(ctx, key.GroupID, "gpt-6-astra"))
	require.Empty(t, shared.failures(scope, time.Now()))
}

func TestOpenAIModelRotationRespectsExplicitAccountModelMappings(t *testing.T) {
	svc, key, accounts := modelRotationTestGateway(t, "advanced")
	ctx := modelRotationTestContext(key.UserID, key.ID)
	svc.ObserveOpenAIModelRotation(ctx, key, &accounts[0], "gpt-6-astra", &OpenAIForwardResult{
		UpstreamModel: "vendor-astra", UpstreamResponseModel: "vendor-astra",
	})
	require.Empty(t, svc.openAIModelRotationExclusions(ctx, key.GroupID, "gpt-6-astra"))
	svc.ObserveOpenAIModelRotation(ctx, key, &accounts[0], "gpt-6-astra", &OpenAIForwardResult{
		UpstreamModel: "vendor-astra", UpstreamResponseModel: "gpt-5.6-luna",
	})
	require.ElementsMatch(t, []int64{31}, svc.openAIModelRotationExclusions(ctx, key.GroupID, "gpt-6-astra"))
}
