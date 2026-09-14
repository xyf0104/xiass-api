package service

import (
	"context"
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
