package service

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/stretchr/testify/require"
)

func executionNodeTestAccount(id int64, nodeID string, priority int) *Account {
	proxyID := int64(84)
	if nodeID == "api2" {
		proxyID = 83
	}
	return &Account{
		ID:       id,
		Priority: priority,
		ProxyID:  &proxyID,
		Proxy:    &Proxy{ID: proxyID, Status: StatusActive},
		Extra: map[string]any{
			AccountExecutionNodeExtraKey: nodeID,
		},
	}
}

func TestExecutionNodeConfiguredWithoutSettingServiceFailsClosed(t *testing.T) {
	cfg := &config.Config{}
	cfg.Gateway.ExecutionNode = config.GatewayExecutionNodeConfig{
		Enabled:                true,
		LegacyUnassignedNodeID: "api",
	}

	policy := resolveExecutionNodeRoutingPolicy(context.Background(), cfg, nil)

	require.True(t, policy.enabled)
	require.True(t, policy.unavailable)
	require.Empty(t, filterExecutionNodeCandidates(
		[]*Account{executionNodeTestAccount(1, "api", 1)},
		func(account *Account) *Account { return account },
		policy,
	))
}

func TestExecutionNodeCandidateRejectsInactiveOrExpiredFixedEgress(t *testing.T) {
	policy := executionNodeTestPolicy(map[string]float64{"api": 1, "api2": 1})
	account := executionNodeTestAccount(1, "api", 1)
	account.Proxy.Status = StatusDisabled
	require.False(t, executionNodeCandidateAllowed(policy, account))
	require.False(t, executionNodeCandidateAllowed(policy, account, account.ID), "sticky binding must not bypass disabled egress")

	account.Proxy.Status = StatusActive
	expired := time.Now().Add(-time.Minute)
	account.Proxy.ExpiresAt = &expired
	require.False(t, executionNodeCandidateAllowed(policy, account))
	require.False(t, executionNodeCandidateAllowed(policy, account, account.ID), "sticky binding must not bypass expired egress")

	account.Proxy.ExpiresAt = nil
	require.True(t, executionNodeCandidateAllowed(policy, account))
}

func TestOpenAIExecutionNodeBoundCandidateOnlyBypassesDrain(t *testing.T) {
	policy := executionNodeTestPolicy(map[string]float64{"api": 1, "api2": 0})
	account := executionNodeTestAccount(1, "api2", 1)
	req := OpenAIAccountScheduleRequest{
		StickyWeighted:  true,
		StickyAccountID: account.ID,
	}

	require.True(t, openAIExecutionNodeCandidateAllowed(policy, req, account), "exact sticky account may finish while its node drains")

	account.Proxy.Status = StatusDisabled
	require.False(t, openAIExecutionNodeCandidateAllowed(policy, req, account), "sticky account must still have a valid fixed egress")
}

func TestRecheckSelectedOpenAIAccountRejectsInvalidExecutionNodeEgress(t *testing.T) {
	ctx := context.Background()
	groupID := int64(908)
	account := executionNodeTestAccount(51, "api2", 1)
	account.Platform = PlatformOpenAI
	account.Type = AccountTypeOAuth
	account.Status = StatusActive
	account.Schedulable = true
	account.Concurrency = 1
	account.GroupIDs = []int64{groupID}
	account.Proxy.Status = StatusDisabled

	cfg := &config.Config{RunMode: config.RunModeStandard}
	cfg.Gateway.ExecutionNode = config.GatewayExecutionNodeConfig{
		Enabled:                true,
		ID:                     "api",
		DefaultProxyID:         84,
		LegacyUnassignedNodeID: "api",
	}
	settingService := NewSettingService(&executionNodeSettingRepo{values: map[string]string{
		SettingKeyExecutionNodeBalancingEnabled: "true",
		SettingKeyExecutionNodeWeights:          `{"api":1,"api2":0}`,
		SettingKeyExecutionNodeProxyIDs:         `{"api":84,"api2":83}`,
	}}, cfg)

	withoutSnapshot := &OpenAIGatewayService{cfg: cfg, settingService: settingService}
	require.Nil(t, withoutSnapshot.recheckSelectedOpenAIAccountFromDB(ctx, account, &groupID, PlatformOpenAI, "gpt-5.1", false, ""))

	withSnapshot := &OpenAIGatewayService{
		accountRepo:       schedulerTestOpenAIAccountRepo{accounts: []Account{*account}},
		cfg:               cfg,
		settingService:    settingService,
		schedulerSnapshot: &SchedulerSnapshotService{cache: &openAISnapshotCacheStub{}},
	}
	require.Nil(t, withSnapshot.recheckSelectedOpenAIAccountFromDB(ctx, account, &groupID, PlatformOpenAI, "gpt-5.1", false, ""))
}

func TestOpenAILegacyNewSessionSkipsInvalidHigherPriorityExecutionNodeEgress(t *testing.T) {
	ctx := context.Background()
	groupID := int64(909)
	invalidPrimary := executionNodeTestAccount(61, "api2", 1)
	validFallback := executionNodeTestAccount(62, "api", 2)
	for _, account := range []*Account{invalidPrimary, validFallback} {
		account.Platform = PlatformOpenAI
		account.Type = AccountTypeAPIKey
		account.Status = StatusActive
		account.Schedulable = true
		account.Concurrency = 1
		account.GroupIDs = []int64{groupID}
	}
	invalidPrimary.Proxy.Status = StatusDisabled

	cfg := &config.Config{RunMode: config.RunModeStandard}
	cfg.Gateway.Scheduling.LoadBatchEnabled = true
	cfg.Gateway.ExecutionNode = config.GatewayExecutionNodeConfig{
		Enabled:                 true,
		ID:                      "api",
		DefaultProxyID:          84,
		LegacyUnassignedNodeID:  "api",
		LegacyUnassignedProxyID: 84,
	}
	settingService := NewSettingService(&executionNodeSettingRepo{values: map[string]string{
		SettingKeyExecutionNodeBalancingEnabled: "true",
		SettingKeyExecutionNodeWeights:          `{"api":1,"api2":1}`,
		SettingKeyExecutionNodeProxyIDs:         `{"api":84,"api2":83}`,
	}}, cfg)
	svc := &OpenAIGatewayService{
		accountRepo: schedulerGroupAwareOpenAIAccountRepo{schedulerTestOpenAIAccountRepo{accounts: []Account{
			*invalidPrimary,
			*validFallback,
		}}},
		cache:              &schedulerTestGatewayCache{},
		cfg:                cfg,
		settingService:     settingService,
		concurrencyService: NewConcurrencyService(schedulerTestConcurrencyCache{}),
	}

	selection, err := svc.selectAccountWithLoadAwareness(ctx, &groupID, PlatformOpenAI, "", "gpt-5.1", nil, false, "", true)

	require.NoError(t, err)
	require.NotNil(t, selection)
	require.Equal(t, validFallback.ID, selection.Account.ID)
	require.True(t, selection.Acquired)
	require.Nil(t, selection.WaitPlan)
	if selection.ReleaseFunc != nil {
		selection.ReleaseFunc()
	}
}

func executionNodeTestPolicy(weights map[string]float64) executionNodeRoutingPolicy {
	return executionNodeRoutingPolicy{
		enabled:      true,
		legacyNodeID: "api",
		localNodeID:  "api",
		weights:      weights,
		proxyIDs:     map[string]int64{"api": 84, "api2": 83},
		healthy:      map[string]bool{"api": true, "api2": true},
	}
}

func TestExecutionNodeEmergencyTakeoverUsesRequestLocalProxyOnly(t *testing.T) {
	policy := executionNodeTestPolicy(map[string]float64{"api": 1, "api2": 1})
	policy.localNodeID = "api2"
	policy.emergencyLocalEgress = true
	policy.healthy["api"] = false
	policy.localProxy = &Proxy{ID: 83, Status: StatusActive, Host: "api2.internal", Port: 1080}
	account := executionNodeTestAccount(71, "api", 1)
	originalProxyID := *account.ProxyID
	originalProxy := account.Proxy

	require.True(t, executionNodeCandidateAllowed(policy, account))
	routed := policy.routeAccountForExecution(account)

	require.NotSame(t, account, routed)
	require.Equal(t, originalProxyID, *routed.ProxyID)
	require.Same(t, originalProxy, routed.Proxy)
	require.Equal(t, int64(83), routed.requestProxy().ID)
	require.Equal(t, originalProxyID, *account.ProxyID)
	require.Same(t, originalProxy, account.Proxy)
	require.Equal(t, "api", account.ExecutionNodeID("api"))
}

func TestExecutionNodeEmergencyTakeoverRequiresValidDurableProxyBinding(t *testing.T) {
	policy := executionNodeTestPolicy(map[string]float64{"api": 1, "api2": 1})
	policy.localNodeID = "api2"
	policy.emergencyLocalEgress = true
	policy.healthy["api"] = false
	policy.localProxy = &Proxy{ID: 83, Status: StatusActive}
	account := executionNodeTestAccount(74, "api", 1)
	account.Proxy = nil

	require.False(t, executionNodeCandidateAllowed(policy, account))
	require.Same(t, account, policy.routeAccountForExecution(account))

	account.Proxy = &Proxy{ID: *account.ProxyID, Status: StatusDisabled}
	require.False(t, executionNodeCandidateAllowed(policy, account))
	require.Same(t, account, policy.routeAccountForExecution(account))
}

func TestExecutionNodeHealthyOwnerNeverChangesEgress(t *testing.T) {
	policy := executionNodeTestPolicy(map[string]float64{"api": 1, "api2": 1})
	policy.localNodeID = "api2"
	policy.emergencyLocalEgress = true
	policy.localProxy = &Proxy{ID: 83, Status: StatusActive}
	account := executionNodeTestAccount(72, "api", 1)

	require.Same(t, account, policy.routeAccountForExecution(account))
	require.Equal(t, int64(84), *account.ProxyID)
}

func TestExecutionNodeDeadOwnerFailsClosedWhenTakeoverDisabled(t *testing.T) {
	policy := executionNodeTestPolicy(map[string]float64{"api": 1, "api2": 1})
	policy.localNodeID = "api2"
	policy.healthy["api"] = false
	policy.localProxy = &Proxy{ID: 83, Status: StatusActive}
	account := executionNodeTestAccount(73, "api", 1)

	require.False(t, executionNodeCandidateAllowed(policy, account))
	require.Same(t, account, policy.routeAccountForExecution(account))
}

func TestExecutionNodeTakeoverRequiresKnownOfflineOwner(t *testing.T) {
	policy := executionNodeTestPolicy(map[string]float64{"api": 9, "api2": 1})
	policy.localNodeID, policy.emergencyLocalEgress = "api2", true
	policy.localProxy = &Proxy{ID: 83, Status: StatusActive}
	delete(policy.healthy, "api")
	account := executionNodeTestAccount(75, "api", 0)
	require.False(t, policy.canTakeOver(account), "missing health evidence is not confirmation of an offline owner")
	require.False(t, executionNodeCandidateAllowed(policy, account))
	require.Same(t, account, policy.routeAccountForExecution(account))
}

func TestExecutionNodeTakeoverOrdersHealthyOwnersBeforeEveryOfflinePriority(t *testing.T) {
	policy := executionNodeTestPolicy(map[string]float64{"api": 9, "api2": 1})
	policy.localNodeID, policy.emergencyLocalEgress = "api2", true
	policy.localProxy = &Proxy{ID: 83, Status: StatusActive}
	policy.healthy["api"] = false
	remote := executionNodeTestAccount(76, "api", 0)
	local := executionNodeTestAccount(77, "api2", 10)
	lowerLocal := executionNodeTestAccount(78, "api2", 20)
	lowerRemote := executionNodeTestAccount(79, "api", 30)
	items := []*Account{remote, local, lowerLocal, lowerRemote}
	for i := 0; i < 100; i++ {
		anchor := fmt.Sprintf("takeover-%d", i)
		ordered := orderExecutionNodeCandidatesWithinPriorities(items,
			func(a *Account) *Account { return a }, func(a *Account) int { return a.Priority }, policy, anchor)
		require.Equal(t, []*Account{local, lowerLocal, remote, lowerRemote}, ordered)
		partitions := partitionExecutionNodeCandidates(items, func(a *Account) *Account { return a }, policy, anchor)
		require.Equal(t, [][]*Account{{local, lowerLocal}, {remote, lowerRemote}}, partitions)
		require.Equal(t, []string{"api2", "api"}, weightedExecutionNodePermutation([]string{"api", "api2"}, policy, anchor))
	}
	require.Equal(t, []*Account{remote, local, lowerLocal, lowerRemote}, items, "ordering must not mutate the input snapshot")
}

func TestExecutionNodeTakeoverHealthyNineToOnePlacementIsUnchanged(t *testing.T) {
	policy := executionNodeTestPolicy(map[string]float64{"api": 9, "api2": 1})
	policy.localNodeID = "api2"
	policy.localProxy = &Proxy{ID: 83, Status: StatusActive}
	const samples = 20000
	primaryCount := 0
	for i := 0; i < samples; i++ {
		anchor := fmt.Sprintf("healthy-nine-to-one-%d", i)
		ordinary := weightedExecutionNodePermutation([]string{"api", "api2"}, policy, anchor)
		emergency := policy
		emergency.emergencyLocalEgress = true
		require.Equal(t, ordinary, weightedExecutionNodePermutation([]string{"api", "api2"}, emergency, anchor), "healthy placement must match exactly, not just statistically")
		if ordinary[0] == "api" {
			primaryCount++
		}
	}
	require.InDelta(t, 0.9, float64(primaryCount)/samples, 0.02)
}

func TestExecutionNodeWeightedPlacementDistribution(t *testing.T) {
	accounts := []*Account{
		executionNodeTestAccount(1, "api", 1),
		executionNodeTestAccount(2, "api2", 1),
	}
	for _, test := range []struct {
		name       string
		weights    map[string]float64
		minAPIRate float64
		maxAPIRate float64
	}{
		{name: "one_to_one", weights: map[string]float64{"api": 1, "api2": 1}, minAPIRate: 0.47, maxAPIRate: 0.53},
		{name: "three_to_one", weights: map[string]float64{"api": 3, "api2": 1}, minAPIRate: 0.72, maxAPIRate: 0.78},
	} {
		t.Run(test.name, func(t *testing.T) {
			policy := executionNodeTestPolicy(test.weights)
			apiSelections := 0
			const samples = 20_000
			for i := 0; i < samples; i++ {
				selected := firstExecutionNodeCandidateGroup(
					accounts,
					func(account *Account) *Account { return account },
					policy,
					fmt.Sprintf("session-%d", i),
				)
				require.Len(t, selected, 1)
				if selected[0].ExecutionNodeID("api") == "api" {
					apiSelections++
				}
			}
			rate := float64(apiSelections) / samples
			require.GreaterOrEqual(t, rate, test.minAPIRate)
			require.LessOrEqual(t, rate, test.maxAPIRate)
		})
	}
}

func TestExecutionNodeZeroWeightDrainsOnlyNewPlacement(t *testing.T) {
	api := executionNodeTestAccount(1, "api", 1)
	api2 := executionNodeTestAccount(2, "api2", 1)
	policy := executionNodeTestPolicy(map[string]float64{"api": 1, "api2": 0})

	filtered := filterExecutionNodeCandidates(
		[]*Account{api, api2},
		func(account *Account) *Account { return account },
		policy,
	)
	require.Equal(t, []*Account{api}, filtered)

	// Established sticky requests bypass the new-placement policy and therefore
	// remain able to use their already-bound account while it drains.
	stickyPolicy := policy
	if openAIRequestHasStickyAnchor(OpenAIAccountScheduleRequest{StickyAccountID: api2.ID}) {
		stickyPolicy = executionNodeRoutingPolicy{}
	}
	require.Equal(t, []*Account{api2}, filterExecutionNodeCandidates(
		[]*Account{api2},
		func(account *Account) *Account { return account },
		stickyPolicy,
	))
}

func TestExecutionNodeOrderingDoesNotBypassSingleCandidateDrain(t *testing.T) {
	api2 := executionNodeTestAccount(2, "api2", 1)
	policy := executionNodeTestPolicy(map[string]float64{"api": 1, "api2": 0})

	require.Empty(t, orderExecutionNodeCandidates(
		[]*Account{api2},
		func(account *Account) *Account { return account },
		policy,
		"single-candidate-drain",
	))
	require.Empty(t, orderExecutionNodeCandidatesWithinPriorities(
		[]*Account{api2},
		func(account *Account) *Account { return account },
		func(account *Account) int { return account.Priority },
		policy,
		"single-candidate-drain",
	))
}

func TestExecutionNodeCompactRetryHonorsDrain(t *testing.T) {
	api2 := executionNodeTestAccount(2, "api2", 1)
	policy := executionNodeTestPolicy(map[string]float64{"api": 1, "api2": 0})

	ordered := sortOpenAICompactRetryCandidates([]openAIAccountCandidateScore{{account: api2}}, policy, OpenAIAccountScheduleRequest{})

	require.Empty(t, ordered)
}

func TestExecutionNodeCompactRetryKeepsExactBoundAccountDuringDrain(t *testing.T) {
	api2 := executionNodeTestAccount(2, "api2", 1)
	policy := executionNodeTestPolicy(map[string]float64{"api": 1, "api2": 0})
	req := OpenAIAccountScheduleRequest{StickyWeighted: true, StickyAccountID: api2.ID}

	ordered := sortOpenAICompactRetryCandidates([]openAIAccountCandidateScore{{account: api2}}, policy, req)

	require.Len(t, ordered, 1)
	require.Same(t, api2, ordered[0].account)
}

func TestExecutionNodeBoundAccountExemptionDoesNotReopenDrainedSiblings(t *testing.T) {
	bound := executionNodeTestAccount(1, "api2", 1)
	sibling := executionNodeTestAccount(2, "api2", 1)
	active := executionNodeTestAccount(3, "api", 1)
	policy := executionNodeTestPolicy(map[string]float64{"api": 1, "api2": 0})

	require.True(t, executionNodeCandidateAllowed(policy, bound, bound.ID))
	require.False(t, executionNodeCandidateAllowed(policy, sibling, bound.ID))
	require.True(t, executionNodeCandidateAllowed(policy, active, bound.ID))
}

func TestExecutionNodeDrainPreservesUnmovablePreviousResponseAccount(t *testing.T) {
	ctx := context.Background()
	groupID := int64(23)
	account := executionNodeTestAccount(2, "api2", 1)
	account.Platform = PlatformOpenAI
	account.Type = AccountTypeAPIKey
	account.Status = StatusActive
	account.Schedulable = true
	account.Concurrency = 2
	account.Extra["openai_apikey_responses_websockets_v2_enabled"] = true
	account.Proxy = &Proxy{ID: *account.ProxyID, Status: StatusActive}

	cfg := newOpenAIWSV2TestConfig()
	cfg.Gateway.ExecutionNode = config.GatewayExecutionNodeConfig{
		Enabled:                true,
		ID:                     "api",
		DefaultProxyID:         84,
		LegacyUnassignedNodeID: "api",
	}
	settingService := NewSettingService(&executionNodeSettingRepo{values: map[string]string{
		SettingKeyExecutionNodeBalancingEnabled: "true",
		SettingKeyExecutionNodeWeights:          `{"api":1,"api2":0}`,
		SettingKeyExecutionNodeProxyIDs:         `{"api":84,"api2":83}`,
	}}, cfg)
	cache := &stubGatewayCache{}
	store := NewOpenAIWSStateStore(cache)
	svc := &OpenAIGatewayService{
		accountRepo:        stubOpenAIAccountRepo{accounts: []Account{*account}},
		cache:              cache,
		cfg:                cfg,
		settingService:     settingService,
		concurrencyService: NewConcurrencyService(stubConcurrencyCache{}),
		openaiWSStateStore: store,
	}

	require.NoError(t, store.BindResponseAccount(ctx, groupID, "resp_drained_api2", account.ID, time.Hour))
	selection, err := svc.SelectAccountByPreviousResponseID(
		ctx,
		&groupID,
		"resp_drained_api2",
		"gpt-5.1",
		nil,
		false,
	)

	require.NoError(t, err)
	require.NotNil(t, selection)
	require.NotNil(t, selection.Account)
	require.Equal(t, account.ID, selection.Account.ID)
	if selection.ReleaseFunc != nil {
		selection.ReleaseFunc()
	}
}

func TestOpenAIMovablePreviousResponseDrainMigratesToActiveNode(t *testing.T) {
	bound := executionNodeTestAccount(1, "api2", 1)
	sibling := executionNodeTestAccount(2, "api2", 1)
	active := executionNodeTestAccount(3, "api", 1)
	policy := executionNodeTestPolicy(map[string]float64{"api": 1, "api2": 0})
	req := OpenAIAccountScheduleRequest{
		StickyWeighted:          true,
		PreviousResponseCanMove: true,
		StickyPreviousAccountID: bound.ID,
	}
	plan := openAIAccountLoadPlan{
		topK:                3,
		executionNodePolicy: policy,
		executionNodeAnchor: "movable-previous",
		candidates: []openAIAccountCandidateScore{
			{account: bound},
			{account: sibling},
			{account: active},
		},
	}

	service := &defaultOpenAIAccountScheduler{}
	ordered := service.buildOpenAISelectionOrder(req, plan)
	require.Len(t, ordered, 1)
	require.Same(t, active, ordered[0].account)
}

func TestExecutionNodeUnknownOwnerFailsClosed(t *testing.T) {
	policy := executionNodeTestPolicy(map[string]float64{"api": 1, "api2": 1})
	unknown := executionNodeTestAccount(3, "api3", 1)
	require.Empty(t, filterExecutionNodeCandidates(
		[]*Account{unknown},
		func(account *Account) *Account { return account },
		policy,
	))
}

func TestExecutionNodeWeightNeverBreaksHardPriority(t *testing.T) {
	apiHigh := executionNodeTestAccount(1, "api", 1)
	api2Low := executionNodeTestAccount(2, "api2", 2)
	policy := executionNodeTestPolicy(map[string]float64{"api": 1, "api2": 1_000_000})

	selected := selectGatewayLegacyExecutionNodeAccount(
		[]*Account{api2Low, apiHigh},
		false,
		false,
		policy,
		"weighted-session",
	)
	require.Same(t, apiHigh, selected)
}

func TestExecutionNodePermutationKeepsSamePriorityFailover(t *testing.T) {
	accounts := []*Account{
		executionNodeTestAccount(1, "api", 1),
		executionNodeTestAccount(2, "api2", 1),
	}
	policy := executionNodeTestPolicy(map[string]float64{"api": 3, "api2": 1})
	ordered := orderExecutionNodeCandidates(
		accounts,
		func(account *Account) *Account { return account },
		policy,
		"same-priority-failover",
	)
	require.Len(t, ordered, 2)
	require.NotEqual(t, ordered[0].ExecutionNodeID("api"), ordered[1].ExecutionNodeID("api"))
}

func TestExecutionNodeProxyMappingRequiresEveryNodeAndUniqueProxy(t *testing.T) {
	weights := map[string]float64{"api": 1, "api2": 1}

	err := validateExecutionNodeProxyIDs(map[string]int64{"api": 84}, weights)
	require.Error(t, err)
	require.Contains(t, err.Error(), "api2")

	err = validateExecutionNodeProxyIDs(map[string]int64{"api": 84, "api2": 84}, weights)
	require.Error(t, err)
	require.Contains(t, err.Error(), "cannot be assigned")

	require.NoError(t, validateExecutionNodeProxyIDs(
		map[string]int64{"api": 84, "api2": 83},
		weights,
	))
}

type executionNodeSettingRepo struct {
	SettingRepository
	values map[string]string
	err    error
	calls  int
}

func (r *executionNodeSettingRepo) GetValue(_ context.Context, key string) (string, error) {
	if r.err != nil {
		return "", r.err
	}
	value, ok := r.values[key]
	if !ok {
		return "", ErrSettingNotFound
	}
	return value, nil
}

func (r *executionNodeSettingRepo) GetMultiple(_ context.Context, keys []string) (map[string]string, error) {
	r.calls++
	if r.err != nil {
		return nil, r.err
	}
	out := make(map[string]string, len(keys))
	for _, key := range keys {
		out[key] = r.values[key]
	}
	return out, nil
}

func (r *executionNodeSettingRepo) Set(_ context.Context, key, value string) error {
	if r.err != nil {
		return r.err
	}
	if r.values == nil {
		r.values = make(map[string]string)
	}
	r.values[key] = value
	return nil
}

func TestExecutionNodeOfflineTakeoverCanBeChangedPerMachine(t *testing.T) {
	cfg := &config.Config{}
	cfg.Gateway.ExecutionNode = config.GatewayExecutionNodeConfig{
		Enabled:                true,
		ID:                     "api2",
		EmergencyLocalEgress:   true,
		LegacyUnassignedNodeID: "api",
	}
	repo := &executionNodeSettingRepo{values: map[string]string{
		executionNodeEmergencyEgressSettingKey("api"): "true",
	}}
	svc := NewSettingService(repo, cfg)

	require.NoError(t, svc.SetExecutionNodeEmergencyLocalEgress(context.Background(), false))
	require.Equal(t, "false", repo.values[executionNodeEmergencyEgressSettingKey("api2")])
	require.Equal(t, "true", repo.values[executionNodeEmergencyEgressSettingKey("api")])
}

func TestExecutionNodeExpiredCacheRefreshesBeforeNewPlacement(t *testing.T) {
	cfg := &config.Config{}
	cfg.Gateway.ExecutionNode.Enabled = true
	repo := &executionNodeSettingRepo{values: map[string]string{
		SettingKeyExecutionNodeBalancingEnabled: "true",
		SettingKeyExecutionNodeWeights:          `{"api":1,"api2":0}`,
		SettingKeyExecutionNodeProxyIDs:         `{"api":84,"api2":83}`,
	}}
	service := NewSettingService(repo, cfg)
	service.executionNodeRoutingCache.Store(&cachedExecutionNodeRoutingSettings{
		settings: ExecutionNodeRoutingSettings{
			Enabled: true,
			Weights: map[string]float64{"api": 1, "api2": 1},
		},
		expiresAt: time.Now().Add(-time.Second).UnixNano(),
	})

	settings := service.GetExecutionNodeRoutingSettings(context.Background())
	require.True(t, settings.Enabled)
	require.Equal(t, map[string]float64{"api": 1, "api2": 0}, settings.Weights)
	require.Equal(t, 1, repo.calls)
}

func TestExecutionNodeExpiredCacheFallsBackOnlyOnReadFailure(t *testing.T) {
	cfg := &config.Config{}
	cfg.Gateway.ExecutionNode.Enabled = true
	repo := &executionNodeSettingRepo{err: errors.New("database unavailable")}
	service := NewSettingService(repo, cfg)
	service.executionNodeRoutingCache.Store(&cachedExecutionNodeRoutingSettings{
		settings: ExecutionNodeRoutingSettings{
			Enabled: true,
			Weights: map[string]float64{"api": 1, "api2": 1},
		},
		expiresAt: time.Now().Add(-time.Second).UnixNano(),
	})

	settings := service.GetExecutionNodeRoutingSettings(context.Background())
	require.True(t, settings.Enabled)
	require.Equal(t, map[string]float64{"api": 1, "api2": 1}, settings.Weights)
	require.Equal(t, 1, repo.calls)
}

func TestExecutionNodeInitialPolicyReadFailureFailsClosed(t *testing.T) {
	cfg := &config.Config{}
	cfg.Gateway.ExecutionNode.Enabled = true
	repo := &executionNodeSettingRepo{err: errors.New("database unavailable")}
	service := NewSettingService(repo, cfg)

	settings := service.GetExecutionNodeRoutingSettings(context.Background())
	require.False(t, settings.Available)
	policy := resolveExecutionNodeRoutingPolicy(context.Background(), cfg, service)
	require.True(t, policy.enabled)
	require.True(t, policy.unavailable)
	require.Zero(t, policy.weight("api"))
	require.Empty(t, filterExecutionNodeCandidates(
		[]*Account{executionNodeTestAccount(1, "api", 1)},
		func(account *Account) *Account { return account },
		policy,
	))
}

func TestExecutionNodeInvalidActivePolicyFailsClosed(t *testing.T) {
	cfg := &config.Config{}
	cfg.Gateway.ExecutionNode.Enabled = true
	repo := &executionNodeSettingRepo{values: map[string]string{
		SettingKeyExecutionNodeBalancingEnabled: "true",
		SettingKeyExecutionNodeWeights:          `{"api":1,"api2":0}`,
		SettingKeyExecutionNodeProxyIDs:         `{"api":84}`,
	}}
	service := NewSettingService(repo, cfg)

	settings := service.GetExecutionNodeRoutingSettings(context.Background())
	require.False(t, settings.Available)
	policy := resolveExecutionNodeRoutingPolicy(context.Background(), cfg, service)
	require.True(t, policy.enabled)
	require.True(t, policy.unavailable)
	require.Empty(t, filterExecutionNodeCandidates(
		[]*Account{executionNodeTestAccount(1, "api", 1)},
		func(account *Account) *Account { return account },
		policy,
	))
}

func TestExecutionNodeProxyMutationGuardFailsClosedWhenPolicyUnavailable(t *testing.T) {
	cfg := &config.Config{}
	cfg.Gateway.ExecutionNode.Enabled = true

	unavailable := &adminServiceImpl{
		settingService: NewSettingService(
			&executionNodeSettingRepo{err: errors.New("database unavailable")},
			cfg,
		),
	}
	require.True(t, unavailable.executionNodeRoutingActive(context.Background()))

	disabled := &adminServiceImpl{
		settingService: NewSettingService(
			&executionNodeSettingRepo{values: map[string]string{
				SettingKeyExecutionNodeBalancingEnabled: "false",
			}},
			cfg,
		),
	}
	require.False(t, disabled.executionNodeRoutingActive(context.Background()))

	enabled := &adminServiceImpl{
		settingService: NewSettingService(
			&executionNodeSettingRepo{values: map[string]string{
				SettingKeyExecutionNodeBalancingEnabled: "true",
			}},
			cfg,
		),
	}
	require.True(t, enabled.executionNodeRoutingActive(context.Background()))
}

type executionNodeProxyRepo struct {
	ProxyRepository
	proxyID int64
}

func (r *executionNodeProxyRepo) GetByID(_ context.Context, id int64) (*Proxy, error) {
	if id != r.proxyID {
		return nil, ErrProxyNotFound
	}
	return &Proxy{ID: id, Status: StatusActive}, nil
}

type executionNodeHealthReaderStub struct{}

func (executionNodeHealthReaderStub) HealthyExecutionNodes(_ context.Context, nodeIDs []string) (map[string]bool, error) {
	healthy := make(map[string]bool, len(nodeIDs))
	for _, nodeID := range nodeIDs {
		healthy[nodeID] = true
	}
	return healthy, nil
}

type executionNodeHealthMapStub map[string]bool

func (s executionNodeHealthMapStub) HealthyExecutionNodes(_ context.Context, nodeIDs []string) (map[string]bool, error) {
	healthy := make(map[string]bool, len(nodeIDs))
	for _, nodeID := range nodeIDs {
		healthy[nodeID] = s[nodeID]
	}
	return healthy, nil
}

func TestExecutionNodeUnavailableLocalTakeoverProxyKeepsRemoteAccountsRoutable(t *testing.T) {
	cfg := &config.Config{}
	cfg.Gateway.ExecutionNode = config.GatewayExecutionNodeConfig{
		Enabled:                true,
		ID:                     "api2",
		DefaultProxyID:         83,
		EmergencyLocalEgress:   true,
		LegacyUnassignedNodeID: "api",
	}
	repo := &executionNodeSettingRepo{values: map[string]string{
		SettingKeyExecutionNodeBalancingEnabled: "true",
		SettingKeyExecutionNodeWeights:          `{"api":1,"api2":1}`,
		SettingKeyExecutionNodeProxyIDs:         `{"api":84,"api2":83}`,
	}}
	service := NewSettingService(repo, cfg)
	service.SetExecutionNodeHealthReader(executionNodeHealthReaderStub{})
	service.SetProxyRepository(&executionNodeProxyRepo{proxyID: 84})

	settings := service.GetExecutionNodeRoutingSettings(context.Background())
	require.True(t, settings.Available)
	require.True(t, settings.Enabled)
	require.Nil(t, settings.LocalProxy)

	policy := resolveExecutionNodeRoutingPolicy(context.Background(), cfg, service)
	remote := executionNodeTestAccount(1, "api", 1)
	require.True(t, executionNodeCandidateAllowed(policy, remote))
	require.Same(t, remote, policy.routeAccountForExecution(remote))
}

func TestOpenAIRecheckPreservesRequestLocalEmergencyEgress(t *testing.T) {
	ctx := context.Background()
	groupID := int64(912)
	account := executionNodeTestAccount(81, "api", 1)
	account.Platform = PlatformOpenAI
	account.Type = AccountTypeAPIKey
	account.Status = StatusActive
	account.Schedulable = true
	account.GroupIDs = []int64{groupID}

	cfg := &config.Config{RunMode: config.RunModeStandard}
	cfg.Gateway.ExecutionNode = config.GatewayExecutionNodeConfig{
		Enabled:                true,
		ID:                     "api2",
		DefaultProxyID:         83,
		EmergencyLocalEgress:   true,
		LegacyUnassignedNodeID: "api",
	}
	settings := NewSettingService(&executionNodeSettingRepo{values: map[string]string{
		SettingKeyExecutionNodeBalancingEnabled: "true",
		SettingKeyExecutionNodeWeights:          `{"api":1,"api2":1}`,
		SettingKeyExecutionNodeProxyIDs:         `{"api":84,"api2":83}`,
	}}, cfg)
	settings.SetExecutionNodeHealthReader(executionNodeHealthMapStub{"api": false, "api2": true})
	settings.SetProxyRepository(&executionNodeProxyRepo{proxyID: 83})
	svc := &OpenAIGatewayService{
		accountRepo:       schedulerTestOpenAIAccountRepo{accounts: []Account{*account}},
		cfg:               cfg,
		settingService:    settings,
		schedulerSnapshot: &SchedulerSnapshotService{cache: &openAISnapshotCacheStub{}},
	}

	routed := svc.recheckSelectedOpenAIAccountFromDB(ctx, account, &groupID, PlatformOpenAI, "gpt-5.1", false, "")
	require.NotNil(t, routed)
	require.Equal(t, int64(84), *routed.ProxyID)
	require.Equal(t, int64(84), routed.Proxy.ID)
	require.Equal(t, int64(83), routed.requestProxy().ID)
	require.Equal(t, int64(84), account.requestProxy().ID)
}

type executionNodeAccountPreparerStub struct {
	called        int
	legacyNodeID  string
	legacyProxyID int64
	allowed       []string
	proxyIDs      map[string]int64
	migrated      int64
	err           error
}

func (s *executionNodeAccountPreparerStub) PrepareExecutionNodeRouting(_ context.Context, legacyNodeID string, legacyProxyID int64, allowedNodeIDs []string) (int64, error) {
	s.called++
	s.legacyNodeID = legacyNodeID
	s.legacyProxyID = legacyProxyID
	s.allowed = append([]string(nil), allowedNodeIDs...)
	return s.migrated, s.err
}

func (s *executionNodeAccountPreparerStub) PrepareAndEnableExecutionNodeRoutingWithProxyIDs(_ context.Context, legacyNodeID string, legacyProxyID int64, allowedNodeIDs []string, _ map[string]float64, proxyIDs map[string]int64) (int64, error) {
	s.called++
	s.legacyNodeID = legacyNodeID
	s.legacyProxyID = legacyProxyID
	s.allowed = append([]string(nil), allowedNodeIDs...)
	s.proxyIDs = maps.Clone(proxyIDs)
	return s.migrated, s.err
}

func TestPrepareExecutionNodeRoutingActivationRunsMigrationBeforeEnable(t *testing.T) {
	cfg := &config.Config{}
	cfg.Gateway.ExecutionNode = config.GatewayExecutionNodeConfig{
		Enabled:                 true,
		ID:                      "api2",
		DefaultProxyID:          83,
		LegacyUnassignedNodeID:  "api",
		LegacyUnassignedProxyID: 84,
	}
	repo := &executionNodeSettingRepo{values: map[string]string{
		SettingKeyExecutionNodeBalancingEnabled: "false",
	}}
	preparer := &executionNodeAccountPreparerStub{migrated: 7}
	service := NewSettingService(repo, cfg)
	service.SetProxyRepository(&executionNodeProxyRepo{proxyID: 84})
	service.SetExecutionNodeAccountPreparer(preparer)
	settings := &SystemSettings{
		ExecutionNodeBalancingEnabled: true,
		ExecutionNodeWeights:          map[string]float64{"api2": 2, "api": 1},
		ExecutionNodeProxyIDs:         map[string]int64{"api2": 83, "api": 84},
	}

	err := service.prepareExecutionNodeRoutingActivation(context.Background(), settings, map[string]string{
		SettingKeyExecutionNodeBalancingEnabled: "true",
	})
	require.NoError(t, err)
	require.Equal(t, 1, preparer.called)
	require.Equal(t, "api", preparer.legacyNodeID)
	require.Equal(t, int64(84), preparer.legacyProxyID)
	require.Equal(t, []string{"api", "api2"}, preparer.allowed)
	require.Equal(t, map[string]int64{"api": 84, "api2": 83}, preparer.proxyIDs)
}

func TestPrepareExecutionNodeRoutingActivationRejectsUnconfiguredInstance(t *testing.T) {
	repo := &executionNodeSettingRepo{values: map[string]string{
		SettingKeyExecutionNodeBalancingEnabled: "false",
	}}
	service := NewSettingService(repo, &config.Config{})
	err := service.prepareExecutionNodeRoutingActivation(context.Background(), &SystemSettings{
		ExecutionNodeBalancingEnabled: true,
		ExecutionNodeWeights:          map[string]float64{"api": 1},
	}, map[string]string{SettingKeyExecutionNodeBalancingEnabled: "true"})
	require.Error(t, err)
}

func TestPrepareExecutionNodeRoutingActivationRejectsProxyMappingChangeAfterActivation(t *testing.T) {
	repo := &executionNodeSettingRepo{values: map[string]string{
		SettingKeyExecutionNodeBalancingEnabled: "true",
		SettingKeyExecutionNodeProxyIDs:         `{"api":84,"api2":83}`,
	}}
	service := NewSettingService(repo, &config.Config{})

	err := service.prepareExecutionNodeRoutingActivation(context.Background(), &SystemSettings{
		ExecutionNodeBalancingEnabled: true,
	}, map[string]string{
		SettingKeyExecutionNodeBalancingEnabled: "true",
		SettingKeyExecutionNodeProxyIDs:         `{"api":84,"api2":99}`,
	})
	require.Error(t, err)
	require.Equal(t, "EXECUTION_NODE_PROXY_MAPPING_IMMUTABLE", infraerrors.Reason(err))
}

func TestPrepareExecutionNodeRoutingActivationAllowsWeightOnlyUpdate(t *testing.T) {
	repo := &executionNodeSettingRepo{values: map[string]string{
		SettingKeyExecutionNodeBalancingEnabled: "true",
		SettingKeyExecutionNodeProxyIDs:         `{"api":84,"api2":83}`,
	}}
	service := NewSettingService(repo, &config.Config{})

	err := service.prepareExecutionNodeRoutingActivation(context.Background(), &SystemSettings{
		ExecutionNodeBalancingEnabled: true,
	}, map[string]string{
		SettingKeyExecutionNodeBalancingEnabled: "true",
		SettingKeyExecutionNodeProxyIDs:         `{"api2":83,"api":84}`,
	})
	require.NoError(t, err)
}

type executionNodeRevertAccountRepoStub struct {
	AccountRepository
	account      *Account
	revertCalled bool
}

func (r *executionNodeRevertAccountRepoStub) GetByID(_ context.Context, _ int64) (*Account, error) {
	return r.account, nil
}

func (r *executionNodeRevertAccountRepoStub) RevertProxyFallback(_ context.Context, _ int64) error {
	r.revertCalled = true
	return nil
}

func (r *executionNodeRevertAccountRepoStub) ListShadowsByParent(_ context.Context, _ int64) ([]*Account, error) {
	return nil, nil
}

func TestRevertProxyFallbackRejectsCrossNodeOrigin(t *testing.T) {
	cfg := &config.Config{}
	cfg.Gateway.ExecutionNode = config.GatewayExecutionNodeConfig{
		Enabled:                true,
		ID:                     "api2",
		DefaultProxyID:         83,
		LegacyUnassignedNodeID: "api",
	}
	originProxyID := int64(84)
	repo := &executionNodeRevertAccountRepoStub{account: &Account{
		ID:                    7,
		ProxyFallbackOriginID: &originProxyID,
		Extra:                 map[string]any{AccountExecutionNodeExtraKey: "api2"},
	}}
	settingsRepo := &executionNodeSettingRepo{values: map[string]string{
		SettingKeyExecutionNodeBalancingEnabled: "true",
		SettingKeyExecutionNodeWeights:          `{"api":1,"api2":1}`,
		SettingKeyExecutionNodeProxyIDs:         `{"api":84,"api2":83}`,
	}}
	settings := NewSettingService(settingsRepo, cfg)
	admin := &adminServiceImpl{accountRepo: repo, settingService: settings}

	err := admin.RevertAccountProxyFallback(context.Background(), repo.account.ID)

	require.Error(t, err)
	require.Equal(t, "EXECUTION_NODE_PROXY_FALLBACK_ORIGIN_INVALID", infraerrors.Reason(err))
	require.False(t, repo.revertCalled)
}

func TestExecutionNodeControlPlaneBackgroundPolicy(t *testing.T) {
	require.True(t, executionNodeControlPlaneEnabled(nil))
	require.True(t, executionNodeControlPlaneEnabled(&config.Config{}))

	control := &config.Config{}
	control.Gateway.ExecutionNode.Enabled = true
	control.Gateway.ExecutionNode.ControlPlane = true
	require.True(t, executionNodeControlPlaneEnabled(control))

	replica := &config.Config{}
	replica.Gateway.ExecutionNode.Enabled = true
	replica.Gateway.ExecutionNode.ControlPlane = false
	require.False(t, executionNodeControlPlaneEnabled(replica))
}
