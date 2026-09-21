package service

import (
	"context"
	"fmt"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/stretchr/testify/require"
)

func openAIModelRoutingTestContext(groupID int64, direct, pools map[string][]int64) context.Context {
	return context.WithValue(context.Background(), ctxkey.Group, &Group{
		ID:                  groupID,
		Platform:            PlatformOpenAI,
		Status:              StatusActive,
		Hydrated:            true,
		ModelRoutingEnabled: true,
		ModelRouting:        direct,
		ModelRoutingPools:   pools,
	})
}

func openAIModelRoutingTestAccount(id, groupID int64, priority int, poolID string) Account {
	extra := map[string]any{}
	if poolID != "" {
		extra[AccountPoolExtraKey] = poolID
	}
	return Account{
		ID: id, Platform: PlatformOpenAI, Type: AccountTypeOAuth,
		Status: StatusActive, Schedulable: true, Concurrency: 1,
		Priority: priority, GroupIDs: []int64{groupID}, Extra: extra,
	}
}

func selectOpenAIModelRoutingTestAccount(
	t *testing.T,
	mode string,
	ctx context.Context,
	groupID int64,
	accounts []Account,
	cache schedulerTestConcurrencyCache,
	session string,
	settingServices ...*SettingService,
) *AccountSelectionResult {
	t.Helper()
	cfg := &config.Config{}
	cfg.Gateway.Scheduling.LoadBatchEnabled = mode != "legacy_no_batch"
	cfg.Gateway.OpenAIWS.LBTopK = len(accounts)
	svc := &OpenAIGatewayService{
		accountRepo:        schedulerGroupAwareOpenAIAccountRepo{schedulerTestOpenAIAccountRepo{accounts: accounts}},
		cache:              &schedulerTestGatewayCache{},
		cfg:                cfg,
		concurrencyService: NewConcurrencyService(cache),
	}
	if len(settingServices) > 0 {
		svc.settingService = settingServices[0]
	}
	if mode == "advanced" {
		selection, _, err := newDefaultOpenAIAccountScheduler(svc, nil).Select(ctx, OpenAIAccountScheduleRequest{
			GroupID: &groupID, Platform: PlatformOpenAI, SessionHash: session,
			RequestedModel: "gpt-5.6-luna", RequiredTransport: OpenAIUpstreamTransportAny,
		})
		require.NoError(t, err)
		require.NotNil(t, selection)
		return selection
	}
	selection, err := svc.selectAccountWithLoadAwareness(
		ctx,
		&groupID,
		PlatformOpenAI,
		session,
		"gpt-5.6-luna",
		nil,
		false,
		"",
		false,
	)
	require.NoError(t, err)
	require.NotNil(t, selection)
	return selection
}

func openAIModelPriorityTestService(t *testing.T, accountIDs ...int64) *SettingService {
	t.Helper()
	settings := NewSettingService(&openAIModelPriorityRepoStub{values: map[string]string{}}, &config.Config{})
	require.NoError(t, settings.SetOpenAIModelPrioritySettings(context.Background(), &OpenAIModelPrioritySettings{
		Enabled: true,
		Rules:   []OpenAIModelPriorityRule{{ModelPattern: "gpt-5.6-luna", AccountIDs: accountIDs}},
	}))
	return settings
}

func TestOpenAIModelRoutingPreferenceMatchesDirectAccountAndDynamicPool(t *testing.T) {
	groupID := int64(12001)
	ctx := openAIModelRoutingTestContext(
		groupID,
		map[string][]int64{"gpt-5.6-sol": {31}},
		map[string][]int64{"gpt-5.6-*": {7}},
	)
	svc := &OpenAIGatewayService{}

	direct := svc.resolveOpenAIModelRoutingPreference(ctx, &groupID, PlatformOpenAI, "gpt-5.6-sol")
	require.True(t, direct.matches(&Account{ID: 31}))

	pool := svc.resolveOpenAIModelRoutingPreference(ctx, &groupID, PlatformOpenAI, "gpt-5.6-luna")
	account := &Account{ID: 41, Extra: map[string]any{AccountPoolExtraKey: "7"}}
	require.True(t, pool.matches(account))
	account.Extra[AccountPoolExtraKey] = "8"
	require.False(t, pool.matches(account), "pool membership must be read from the current account snapshot")
}

func TestOpenAIModelRoutingPreferencePreemptsOrdinaryPriorityAcrossSchedulers(t *testing.T) {
	preferences := []struct {
		name   string
		direct map[string][]int64
		pools  map[string][]int64
	}{
		{name: "direct_account", direct: map[string][]int64{"gpt-5.6-luna": {52}}},
		{name: "account_pool", pools: map[string][]int64{"gpt-5.6-luna": {7}}},
	}
	for _, preference := range preferences {
		for _, mode := range []string{"advanced", "legacy_batch", "legacy_no_batch"} {
			t.Run(preference.name+"/"+mode, func(t *testing.T) {
				groupID := int64(12002)
				pro := openAIModelRoutingTestAccount(51, groupID, 1, "")
				plus := openAIModelRoutingTestAccount(52, groupID, 5, "7")
				acquired := []int64{}
				selection := selectOpenAIModelRoutingTestAccount(
					t,
					mode,
					openAIModelRoutingTestContext(groupID, preference.direct, preference.pools),
					groupID,
					[]Account{pro, plus},
					schedulerTestConcurrencyCache{acquiredIDs: &acquired},
					"",
				)
				require.Equal(t, plus.ID, selection.Account.ID)
				require.Equal(t, plus.ID, acquired[0])
				if selection.ReleaseFunc != nil {
					selection.ReleaseFunc()
				}
			})
		}
	}
}

func TestOpenAIModelRoutingFallsBackWhenPreferredPoolIsBusy(t *testing.T) {
	for _, mode := range []string{"advanced", "legacy_batch", "legacy_no_batch"} {
		t.Run(mode, func(t *testing.T) {
			groupID := int64(12003)
			pro := openAIModelRoutingTestAccount(61, groupID, 1, "")
			plus := openAIModelRoutingTestAccount(62, groupID, 5, "7")
			acquired := []int64{}
			selection := selectOpenAIModelRoutingTestAccount(
				t,
				mode,
				openAIModelRoutingTestContext(groupID, nil, map[string][]int64{"gpt-5.6-luna": {7}}),
				groupID,
				[]Account{pro, plus},
				schedulerTestConcurrencyCache{
					acquiredIDs:    &acquired,
					acquireResults: map[int64]bool{plus.ID: false, pro.ID: true},
					loadMap: map[int64]*AccountLoadInfo{
						plus.ID: {AccountID: plus.ID, CurrentConcurrency: 1, LoadRate: 100},
						pro.ID:  {AccountID: pro.ID, CurrentConcurrency: 0, LoadRate: 0},
					},
				},
				"",
			)
			require.Equal(t, pro.ID, selection.Account.ID)
			require.Contains(t, acquired, pro.ID)
			if selection.ReleaseFunc != nil {
				selection.ReleaseFunc()
			}
		})
	}
}

func TestOpenAIModelPriorityPreemptsOrdinaryPriorityAcrossSchedulers(t *testing.T) {
	for _, mode := range []string{"advanced", "legacy_batch", "legacy_no_batch"} {
		t.Run(mode, func(t *testing.T) {
			groupID := int64(12005)
			ordinary := openAIModelRoutingTestAccount(81, groupID, 1, "")
			preferred := openAIModelRoutingTestAccount(82, groupID, 9, "")
			ctx := openAIModelRoutingTestContext(groupID, nil, nil)
			group, ok := ctx.Value(ctxkey.Group).(*Group)
			require.True(t, ok)
			group.ModelRoutingEnabled = false
			selection := selectOpenAIModelRoutingTestAccount(
				t, mode, ctx, groupID, []Account{ordinary, preferred},
				schedulerTestConcurrencyCache{}, "", openAIModelPriorityTestService(t, preferred.ID),
			)
			require.Equal(t, preferred.ID, selection.Account.ID)
			if selection.ReleaseFunc != nil {
				selection.ReleaseFunc()
			}
		})
	}
}

func TestOpenAIModelPriorityFallsBackImmediatelyWhenPreferredAccountsAreBusy(t *testing.T) {
	for _, mode := range []string{"advanced", "legacy_batch", "legacy_no_batch"} {
		t.Run(mode, func(t *testing.T) {
			groupID := int64(12006)
			ordinary := openAIModelRoutingTestAccount(91, groupID, 1, "")
			preferred := openAIModelRoutingTestAccount(92, groupID, 9, "")
			ctx := openAIModelRoutingTestContext(groupID, nil, nil)
			group, ok := ctx.Value(ctxkey.Group).(*Group)
			require.True(t, ok)
			group.ModelRoutingEnabled = false
			acquired := []int64{}
			selection := selectOpenAIModelRoutingTestAccount(
				t, mode, ctx, groupID, []Account{ordinary, preferred},
				schedulerTestConcurrencyCache{
					acquiredIDs:    &acquired,
					acquireResults: map[int64]bool{preferred.ID: false, ordinary.ID: true},
					loadMap: map[int64]*AccountLoadInfo{
						preferred.ID: {AccountID: preferred.ID, CurrentConcurrency: 1, LoadRate: 100},
						ordinary.ID:  {AccountID: ordinary.ID, CurrentConcurrency: 0, LoadRate: 0},
					},
				},
				"", openAIModelPriorityTestService(t, preferred.ID),
			)
			require.Equal(t, ordinary.ID, selection.Account.ID)
			require.Contains(t, acquired, ordinary.ID)
			if selection.ReleaseFunc != nil {
				selection.ReleaseFunc()
			}
		})
	}
}

func TestOpenAIModelPriorityLeavesUnmatchedModelsOnOrdinaryScheduling(t *testing.T) {
	for _, mode := range []string{"advanced", "legacy_batch", "legacy_no_batch"} {
		t.Run(mode, func(t *testing.T) {
			groupID := int64(12007)
			ordinary := openAIModelRoutingTestAccount(101, groupID, 1, "")
			lunaOnly := openAIModelRoutingTestAccount(102, groupID, 9, "")
			settings := openAIModelPriorityTestService(t, lunaOnly.ID)
			ctx := openAIModelRoutingTestContext(groupID, nil, nil)
			group, ok := ctx.Value(ctxkey.Group).(*Group)
			require.True(t, ok)
			group.ModelRoutingEnabled = false

			cfg := &config.Config{}
			cfg.Gateway.Scheduling.LoadBatchEnabled = mode != "legacy_no_batch"
			cfg.Gateway.OpenAIWS.LBTopK = 2
			svc := &OpenAIGatewayService{
				accountRepo:        schedulerGroupAwareOpenAIAccountRepo{schedulerTestOpenAIAccountRepo{accounts: []Account{ordinary, lunaOnly}}},
				cache:              &schedulerTestGatewayCache{},
				cfg:                cfg,
				concurrencyService: NewConcurrencyService(schedulerTestConcurrencyCache{}),
				settingService:     settings,
			}

			var selection *AccountSelectionResult
			if mode == "advanced" {
				var err error
				selection, _, err = newDefaultOpenAIAccountScheduler(svc, nil).Select(ctx, OpenAIAccountScheduleRequest{
					GroupID: &groupID, Platform: PlatformOpenAI, RequestedModel: "gpt-6-astra",
					RequiredTransport: OpenAIUpstreamTransportAny,
				})
				require.NoError(t, err)
			} else {
				var err error
				selection, err = svc.selectAccountWithLoadAwareness(ctx, &groupID, PlatformOpenAI, "", "gpt-6-astra", nil, false, "", false)
				require.NoError(t, err)
			}
			require.Equal(t, ordinary.ID, selection.Account.ID)
			if selection.ReleaseFunc != nil {
				selection.ReleaseFunc()
			}
		})
	}
}

func TestOpenAIModelRoutingPreservesActiveStickySession(t *testing.T) {
	for _, mode := range []string{"advanced", "legacy_batch", "legacy_no_batch"} {
		t.Run(mode, func(t *testing.T) {
			groupID := int64(12004)
			pro := openAIModelRoutingTestAccount(71, groupID, 1, "")
			plus := openAIModelRoutingTestAccount(72, groupID, 5, "7")
			gatewayCache := &schedulerTestGatewayCache{sessionBindings: map[string]int64{"openai:sticky": pro.ID}}
			acquiredIDs := []int64{}
			concurrencyCache := schedulerTestConcurrencyCache{acquiredIDs: &acquiredIDs}
			cfg := &config.Config{}
			cfg.Gateway.Scheduling.LoadBatchEnabled = mode != "legacy_no_batch"
			cfg.Gateway.OpenAIWS.LBTopK = 2
			svc := &OpenAIGatewayService{
				accountRepo:        schedulerGroupAwareOpenAIAccountRepo{schedulerTestOpenAIAccountRepo{accounts: []Account{pro, plus}}},
				cache:              gatewayCache,
				cfg:                cfg,
				concurrencyService: NewConcurrencyService(concurrencyCache),
			}
			ctx := openAIModelRoutingTestContext(groupID, nil, map[string][]int64{"gpt-5.6-luna": {7}})

			var selection *AccountSelectionResult
			if mode == "advanced" {
				var err error
				selection, _, err = newDefaultOpenAIAccountScheduler(svc, nil).Select(ctx, OpenAIAccountScheduleRequest{
					GroupID: &groupID, Platform: PlatformOpenAI, SessionHash: "sticky",
					RequestedModel: "gpt-5.6-luna", RequiredTransport: OpenAIUpstreamTransportAny,
				})
				require.NoError(t, err)
			} else {
				var err error
				selection, err = svc.selectAccountWithLoadAwareness(
					ctx, &groupID, PlatformOpenAI, "sticky", "gpt-5.6-luna", nil, false, "", false,
				)
				require.NoError(t, err)
			}

			require.NotNil(t, selection)
			require.Equal(t, pro.ID, selection.Account.ID, "acquired=%v bindings=%v deleted=%v", acquiredIDs, gatewayCache.sessionBindings, gatewayCache.deletedSessions)
			if selection.ReleaseFunc != nil {
				selection.ReleaseFunc()
			}
		})
	}
}

func TestOpenAIModelPriorityExplicitOrderAcrossSchedulers(t *testing.T) {
	for _, mode := range []string{"advanced", "legacy_batch", "legacy_no_batch"} {
		for _, busyCount := range []int{0, 1, 2, 3} {
			t.Run(fmt.Sprintf("%s/busy_%d", mode, busyCount), func(t *testing.T) {
				groupID := int64(12008)
				accounts := []Account{
					openAIModelRoutingTestAccount(101, groupID, 1, ""),
					openAIModelRoutingTestAccount(102, groupID, 2, ""),
					openAIModelRoutingTestAccount(103, groupID, 9, ""),
					openAIModelRoutingTestAccount(104, groupID, 0, "7"),
				}
				order := []int64{103, 101, 102}
				settings := openAIModelPriorityTestService(t, order...)
				require.NoError(t, settings.SetOpenAIModelPrioritySettings(context.Background(), &OpenAIModelPrioritySettings{
					Enabled: true, Rules: []OpenAIModelPriorityRule{{ModelPattern: "gpt-5.6-luna", AccountIDs: order, AccountOrder: order}},
				}))
				cache := schedulerTestConcurrencyCache{acquireResults: map[int64]bool{}, loadMap: map[int64]*AccountLoadInfo{}}
				for _, account := range accounts {
					cache.acquireResults[account.ID] = true
					cache.loadMap[account.ID] = &AccountLoadInfo{AccountID: account.ID}
				}
				for _, id := range order[:busyCount] {
					cache.acquireResults[id] = false
					cache.loadMap[id] = &AccountLoadInfo{AccountID: id, CurrentConcurrency: 1, LoadRate: 100}
				}
				// Group pool preferences must not erase the explicit model order.
				selection := selectOpenAIModelRoutingTestAccount(t, mode,
					openAIModelRoutingTestContext(groupID, nil, map[string][]int64{"gpt-*": {7}}),
					groupID, accounts, cache, "", settings)
				want := append(append([]int64(nil), order...), 104)[busyCount]
				require.Equal(t, want, selection.Account.ID)
				if selection.ReleaseFunc != nil {
					selection.ReleaseFunc()
				}
				require.Equal(t, 9, accounts[2].Priority, "model ordering must not mutate global priority")
			})
		}
	}
}

func TestOpenAIModelPriorityWaitDoesNotEscapeToLowerRankStickyHint(t *testing.T) {
	groupID := int64(12009)
	first := openAIModelRoutingTestAccount(201, groupID, 1, "")
	second := openAIModelRoutingTestAccount(202, groupID, 1, "")
	svc := &OpenAIGatewayService{
		accountRepo:        schedulerGroupAwareOpenAIAccountRepo{schedulerTestOpenAIAccountRepo{accounts: []Account{first, second}}},
		concurrencyService: NewConcurrencyService(schedulerTestConcurrencyCache{}),
		cfg:                &config.Config{},
	}
	scheduler, ok := newDefaultOpenAIAccountScheduler(svc, nil).(*defaultOpenAIAccountScheduler)
	require.True(t, ok)
	req := OpenAIAccountScheduleRequest{
		GroupID: &groupID, Platform: PlatformOpenAI, RequestedModel: "gpt-5.6-luna",
		RequiredTransport: OpenAIUpstreamTransportAny, StickyWeighted: true,
		StickyPreviousAccountID: second.ID, PreviousResponseCanMove: true,
		modelRoutingPreference: openAIModelRoutingPreference{accountIDs: map[int64]struct{}{201: {}, 202: {}}, accountRanks: map[int64]int{201: 0, 202: 1}},
	}
	selection, _, _, _, err := scheduler.finishLoadBalanceSelectionFallback(
		openAIModelRoutingTestContext(groupID, nil, nil), req,
		openAIAccountLoadSelectionAttempt{selectionOrder: []openAIAccountCandidateScore{{account: &first}}},
		newOpenAISelectionProbeBudget(), openAISelectionFilterStats{},
	)
	require.NoError(t, err)
	require.NotNil(t, selection)
	require.Equal(t, first.ID, selection.Account.ID)
	require.NotNil(t, selection.WaitPlan)
}
