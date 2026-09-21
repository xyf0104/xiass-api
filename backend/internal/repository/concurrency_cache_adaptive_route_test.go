package repository

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestAdaptiveRouteStatsSharedAcrossInstancesAndExpire(t *testing.T) {
	server := miniredis.RunT(t)
	client1 := redis.NewClient(&redis.Options{Addr: server.Addr()})
	client2 := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client1.Close(); _ = client2.Close() })
	cache1, ok := NewConcurrencyCache(client1, 15, 0).(*concurrencyCache)
	require.True(t, ok)
	cache2, ok := NewConcurrencyCache(client2, 15, 0).(*concurrencyCache)
	require.True(t, ok)
	route := service.AccountProxyAdaptiveRouteKey{ExecutionNode: "nodehash", AccountID: 7, ModelHash: "modelhash", ProxyHash: "proxyhash"}
	ttft := 240
	require.NoError(t, cache1.RecordAccountProxyAdaptiveRouteOutcome(context.Background(), service.AccountProxyAdaptiveRouteOutcome{
		Route: route, Success: true, ModelKnown: true, ModelExact: true, FirstTokenMs: &ttft, TTL: 30 * time.Minute,
	}))
	stats, err := cache2.GetAccountProxyAdaptiveRouteStats(context.Background(), []service.AccountProxyAdaptiveRouteKey{route})
	require.NoError(t, err)
	require.EqualValues(t, 1, stats[route.CacheID()].Successes)
	require.True(t, stats[route.CacheID()].LastModelKnown)
	require.Greater(t, stats[route.CacheID()].RecentModelScore, 0.0)
	require.Equal(t, float64(240), stats[route.CacheID()].EWMATTFTMs)

	server.FastForward(31 * time.Minute)
	stats, err = cache2.GetAccountProxyAdaptiveRouteStats(context.Background(), []service.AccountProxyAdaptiveRouteKey{route})
	require.NoError(t, err)
	_, exists := stats[route.CacheID()]
	require.False(t, exists)
}

func TestAdaptiveRouteRecentModelAndErrorSignalsRecover(t *testing.T) {
	cache, ctx := newAccountProxyConcurrencyCacheTest(t)
	route := service.AccountProxyAdaptiveRouteKey{ExecutionNode: "n", AccountID: 8, ModelHash: "m", ProxyHash: "p"}
	ttft := 100
	require.NoError(t, cache.RecordAccountProxyAdaptiveRouteOutcome(ctx, service.AccountProxyAdaptiveRouteOutcome{
		Route: route, Success: true, ModelKnown: true, ModelExact: true, FirstTokenMs: &ttft,
	}))
	for i := 0; i < 6; i++ {
		require.NoError(t, cache.RecordAccountProxyAdaptiveRouteOutcome(ctx, service.AccountProxyAdaptiveRouteOutcome{
			Route: route, Success: true, ModelKnown: true, ModelExact: false, FirstTokenMs: &ttft,
		}))
	}
	stats, err := cache.GetAccountProxyAdaptiveRouteStats(ctx, []service.AccountProxyAdaptiveRouteKey{route})
	require.NoError(t, err)
	require.Less(t, stats[route.CacheID()].RecentModelScore, -0.2, "a formerly exact route must become recently mismatched")

	for i := 0; i < 5; i++ {
		require.NoError(t, cache.RecordAccountProxyAdaptiveRouteOutcome(ctx, service.AccountProxyAdaptiveRouteOutcome{Route: route, TransientFailure: true}))
	}
	stats, err = cache.GetAccountProxyAdaptiveRouteStats(ctx, []service.AccountProxyAdaptiveRouteKey{route})
	require.NoError(t, err)
	require.Greater(t, stats[route.CacheID()].RecentErrorRate, 0.5)
	for i := 0; i < 8; i++ {
		require.NoError(t, cache.RecordAccountProxyAdaptiveRouteOutcome(ctx, service.AccountProxyAdaptiveRouteOutcome{Route: route, Success: true}))
	}
	stats, err = cache.GetAccountProxyAdaptiveRouteStats(ctx, []service.AccountProxyAdaptiveRouteKey{route})
	require.NoError(t, err)
	require.Less(t, stats[route.CacheID()].RecentErrorRate, 0.2)
}

func TestAdaptiveRouteProbeLeaseSharedPerAccountModel(t *testing.T) {
	cache, ctx := newAccountProxyConcurrencyCacheTest(t)
	first := service.AccountProxyAdaptiveRouteKey{ExecutionNode: "n", AccountID: 9, ModelHash: "m", ProxyHash: "p1"}
	second := first
	second.ProxyHash = "p2"
	claimed, err := cache.ClaimAccountProxyAdaptiveRouteProbe(ctx, first, time.Minute)
	require.NoError(t, err)
	require.True(t, claimed)
	claimed, err = cache.ClaimAccountProxyAdaptiveRouteProbe(ctx, second, time.Minute)
	require.NoError(t, err)
	require.False(t, claimed)
}
