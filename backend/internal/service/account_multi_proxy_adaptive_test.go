//go:build unit

package service

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/stretchr/testify/require"
)

type adaptiveRouteCacheStub struct {
	ConcurrencyCache
	stats      map[string]AccountProxyAdaptiveRouteStats
	claimProbe bool
	claims     []AccountProxyAdaptiveRouteKey
	outcomes   []AccountProxyAdaptiveRouteOutcome
}

func (c *adaptiveRouteCacheStub) GetAccountProxyAdaptiveRouteStats(_ context.Context, routes []AccountProxyAdaptiveRouteKey) (map[string]AccountProxyAdaptiveRouteStats, error) {
	result := make(map[string]AccountProxyAdaptiveRouteStats, len(routes))
	for _, route := range routes {
		result[route.CacheID()] = c.stats[route.CacheID()]
	}
	return result, nil
}

func (c *adaptiveRouteCacheStub) RecordAccountProxyAdaptiveRouteOutcome(_ context.Context, outcome AccountProxyAdaptiveRouteOutcome) error {
	c.outcomes = append(c.outcomes, outcome)
	return nil
}

func (c *adaptiveRouteCacheStub) ClaimAccountProxyAdaptiveRouteProbe(_ context.Context, route AccountProxyAdaptiveRouteKey, _ time.Duration) (bool, error) {
	c.claims = append(c.claims, route)
	return c.claimProbe, nil
}

func adaptiveTestAccount() (*Account, []AccountProxyBinding, []AccountProxySlotSpec) {
	account := &Account{ID: 91, Extra: map[string]any{AccountMultiProxyAdaptiveEnabledExtraKey: true}, MultiProxyConfigured: true}
	bindings := []AccountProxyBinding{
		{ProxyID: 1, MaxConcurrency: 2, Proxy: &Proxy{ID: 1, Status: StatusActive, UpdatedAt: time.Unix(10, 0)}},
		{ProxyID: 2, MaxConcurrency: 2, Proxy: &Proxy{ID: 2, Status: StatusActive, UpdatedAt: time.Unix(20, 0)}},
	}
	slots := []AccountProxySlotSpec{{ProxyID: 1, MaxConcurrency: 2}, {ProxyID: 2, MaxConcurrency: 2}}
	return account, bindings, slots
}

func TestAdaptiveRouteRankingPrefersRecentExactThenStableTTFT(t *testing.T) {
	account, bindings, slots := adaptiveTestAccount()
	ctx := context.WithValue(context.Background(), ctxkey.Model, "model-any-name")
	route1 := NewConcurrencyService(nil).accountProxyAdaptiveRouteKey(account, "model-any-name", bindings[0].Proxy)
	route2 := NewConcurrencyService(nil).accountProxyAdaptiveRouteKey(account, "model-any-name", bindings[1].Proxy)
	cache := &adaptiveRouteCacheStub{stats: map[string]AccountProxyAdaptiveRouteStats{
		route1.CacheID(): {LastModelKnown: true, RecentModelScore: -0.8, RecentErrorRate: 0, EWMATTFTMs: 50},
		route2.CacheID(): {LastModelKnown: true, RecentModelScore: 0.8, RecentErrorRate: 0.1, EWMATTFTMs: 400},
	}}
	ranked := NewConcurrencyService(cache).rankAccountProxySlots(ctx, account, bindings, slots)
	require.Greater(t, ranked[0].RoutePriority, ranked[1].RoutePriority)

	cache.stats[route1.CacheID()] = AccountProxyAdaptiveRouteStats{LastModelKnown: true, RecentModelScore: 0.8, EWMATTFTMs: 120, EWMATTFTVariance: 25}
	cache.stats[route2.CacheID()] = AccountProxyAdaptiveRouteStats{LastModelKnown: true, RecentModelScore: 0.8, EWMATTFTMs: 300, EWMATTFTVariance: 2500}
	ranked = NewConcurrencyService(cache).rankAccountProxySlots(ctx, account, bindings, slots)
	require.Less(t, ranked[0].RoutePriority, ranked[1].RoutePriority)
}

func TestAdaptiveRouteRankingUnknownUsesStaticBaselineAndDoesNotLookInstant(t *testing.T) {
	account, bindings, slots := adaptiveTestAccount()
	slots[0].RoutePriority = 2
	slots[1].RoutePriority = 1
	ctx := context.WithValue(context.Background(), ctxkey.Model, "custom/model")
	route1 := NewConcurrencyService(nil).accountProxyAdaptiveRouteKey(account, "custom/model", bindings[0].Proxy)
	cache := &adaptiveRouteCacheStub{stats: map[string]AccountProxyAdaptiveRouteStats{
		route1.CacheID(): {EWMATTFTMs: 200, LastObservedAt: time.Now()},
	}}
	ranked := NewConcurrencyService(cache).rankAccountProxySlots(ctx, account, bindings, slots)
	require.Less(t, ranked[0].RoutePriority, ranked[1].RoutePriority, "known fast route should beat the neutral unknown-latency baseline")

	cache.stats = nil
	ranked = NewConcurrencyService(cache).rankAccountProxySlots(ctx, account, bindings, slots)
	require.Greater(t, ranked[0].RoutePriority, ranked[1].RoutePriority, "unknown routes retain administrator static priority")
}

func TestAdaptiveRouteRankingClaimsOnlyOneSharedRecoveryProbe(t *testing.T) {
	account, bindings, slots := adaptiveTestAccount()
	ctx := context.WithValue(context.Background(), ctxkey.Model, "m")
	cache := &adaptiveRouteCacheStub{claimProbe: true, stats: map[string]AccountProxyAdaptiveRouteStats{}}
	for _, binding := range bindings {
		route := NewConcurrencyService(nil).accountProxyAdaptiveRouteKey(account, "m", binding.Proxy)
		cache.stats[route.CacheID()] = AccountProxyAdaptiveRouteStats{
			TransientErrors: 4, RecentErrorRate: 0.9, LastObservedAt: time.Now().Add(-20 * time.Minute),
		}
	}
	ranked := NewConcurrencyService(cache).rankAccountProxySlots(ctx, account, bindings, slots)
	require.Len(t, cache.claims, 1)
	require.NotEqual(t, ranked[0].RoutePriority, ranked[1].RoutePriority)
}

func TestAdaptiveRouteDisabledLeavesLegacyPrioritiesUntouched(t *testing.T) {
	account, _, slots := adaptiveTestAccount()
	delete(account.Extra, AccountMultiProxyAdaptiveEnabledExtraKey)
	slots[0].RoutePriority = 0
	slots[1].RoutePriority = 0
	// The opt-in guard lives at the acquire call site; disabled accounts never
	// enter the ranker and preserve the all-zero Redis balancing contract.
	require.False(t, AccountMultiProxyAdaptiveEnabled(account.Extra))
	require.Equal(t, []AccountProxySlotSpec{{ProxyID: 1, MaxConcurrency: 2}, {ProxyID: 2, MaxConcurrency: 2}}, slots)
}

func adaptiveOutcomeTestService(cache *adaptiveRouteCacheStub, nodeID string) (*OpenAIGatewayService, *Account) {
	concurrency := NewConcurrencyService(cache)
	concurrency.SetExecutionNodeID(nodeID)
	account := &Account{
		ID: 301, Extra: map[string]any{AccountMultiProxyAdaptiveEnabledExtraKey: true}, MultiProxyConfigured: true,
		RequestProxy: &Proxy{ID: 41, Status: StatusActive, UpdatedAt: time.Unix(100, 0)},
	}
	return &OpenAIGatewayService{concurrencyService: concurrency}, account
}

func TestReportOpenAIAccountProxyRouteOutcomeRecordsStreamingExactUsingContextModel(t *testing.T) {
	cache := &adaptiveRouteCacheStub{}
	svc, account := adaptiveOutcomeTestService(cache, "api-node-a")
	ttft := 145
	ctx := context.WithValue(context.Background(), ctxkey.Model, "public-model-alias")
	result := &OpenAIForwardResult{
		Stream: true, FirstTokenMs: &ttft, UpstreamResponseModel: "effective/model-name",
	}

	svc.ReportOpenAIAccountProxyRouteOutcome(ctx, account, "effective/model-name", true, false, result)

	require.Len(t, cache.outcomes, 1)
	outcome := cache.outcomes[0]
	require.True(t, outcome.Success)
	require.True(t, outcome.ModelKnown)
	require.True(t, outcome.ModelExact)
	require.Equal(t, &ttft, outcome.FirstTokenMs)
	require.Equal(t,
		svc.concurrencyService.accountProxyAdaptiveRouteKey(account, "public-model-alias", account.RequestProxy),
		outcome.Route,
	)
}

func TestReportOpenAIAccountProxyRouteOutcomeSkipsClientCancellationAndDisconnect(t *testing.T) {
	cache := &adaptiveRouteCacheStub{}
	svc, account := adaptiveOutcomeTestService(cache, "api-node-a")
	ttft := 80
	result := &OpenAIForwardResult{Stream: true, FirstTokenMs: &ttft}

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	svc.ReportOpenAIAccountProxyRouteOutcome(canceled, account, "model", false, true, result)
	result.ClientDisconnect = true
	svc.ReportOpenAIAccountProxyRouteOutcome(context.Background(), account, "model", false, true, result)

	require.Empty(t, cache.outcomes)
}

func TestReportOpenAIAccountProxyRouteOutcomeTreatsConflictAsUnknown(t *testing.T) {
	cache := &adaptiveRouteCacheStub{}
	svc, account := adaptiveOutcomeTestService(cache, "api-node-a")
	ttft := 95
	result := &OpenAIForwardResult{
		Stream: true, FirstTokenMs: &ttft, UpstreamResponseModel: "model", UpstreamResponseModelConflict: true,
	}

	svc.ReportOpenAIAccountProxyRouteOutcome(context.Background(), account, "model", true, false, result)

	require.Len(t, cache.outcomes, 1)
	require.False(t, cache.outcomes[0].ModelKnown)
	require.False(t, cache.outcomes[0].ModelExact)
}

func TestReportOpenAIAccountProxyRouteOutcomeRecordsHTTP200TerminalFailure(t *testing.T) {
	cache := &adaptiveRouteCacheStub{}
	svc, account := adaptiveOutcomeTestService(cache, "api-node-a")
	ttft := 110
	result := &OpenAIForwardResult{
		Stream: true, OpenAIWSMode: true, UpstreamTerminalEvent: "response.failed", FirstTokenMs: &ttft,
	}
	require.False(t, result.SucceededForScheduling())

	svc.ReportOpenAIAccountProxyRouteOutcome(context.Background(), account, "model", result.SucceededForScheduling(), false, result)

	require.Len(t, cache.outcomes, 1)
	require.False(t, cache.outcomes[0].Success)
	require.True(t, cache.outcomes[0].TransientFailure)
}

func TestReportOpenAIAccountProxyRouteOutcomeIsolatesExecutionNodeAndProxyRevision(t *testing.T) {
	cache := &adaptiveRouteCacheStub{}
	svcA, account := adaptiveOutcomeTestService(cache, "api-node-a")
	ttft := 70
	result := &OpenAIForwardResult{Stream: true, FirstTokenMs: &ttft}
	svcA.ReportOpenAIAccountProxyRouteOutcome(context.Background(), account, "model", true, false, result)

	svcB, _ := adaptiveOutcomeTestService(cache, "api-node-b")
	svcB.ReportOpenAIAccountProxyRouteOutcome(context.Background(), account, "model", true, false, result)
	account.RequestProxy.UpdatedAt = time.Unix(101, 0)
	svcB.ReportOpenAIAccountProxyRouteOutcome(context.Background(), account, "model", true, false, result)

	require.Len(t, cache.outcomes, 3)
	require.NotEqual(t, cache.outcomes[0].Route.ExecutionNode, cache.outcomes[1].Route.ExecutionNode)
	require.NotEqual(t, cache.outcomes[1].Route.ProxyHash, cache.outcomes[2].Route.ProxyHash)
}
