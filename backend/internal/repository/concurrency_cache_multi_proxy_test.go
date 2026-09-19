package repository

import (
	"context"
	"fmt"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func newAccountProxyConcurrencyCacheTest(t *testing.T) (*concurrencyCache, context.Context) {
	t.Helper()
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	cache, ok := NewConcurrencyCache(client, 15, 0).(*concurrencyCache)
	require.True(t, ok)
	return cache, context.Background()
}

func TestAccountProxyConcurrencyBalancesByCapacityRatio(t *testing.T) {
	cache, ctx := newAccountProxyConcurrencyCacheTest(t)
	slots := []service.AccountProxySlotSpec{
		{ProxyID: 11, MaxConcurrency: 2},
		{ProxyID: 22, MaxConcurrency: 4},
	}

	selected := make([]int64, 0, 6)
	for i := 0; i < 6; i++ {
		proxyID, acquired, err := cache.AcquireAccountProxySlot(ctx, 7, slots, fmt.Sprintf("req-%d", i))
		require.NoError(t, err)
		require.True(t, acquired)
		selected = append(selected, proxyID)
	}
	require.Equal(t, []int64{11, 22, 22, 22, 11, 22}, selected)

	counts, err := cache.GetAccountProxyConcurrency(ctx, 7, []int64{11, 22})
	require.NoError(t, err)
	require.Equal(t, 2, counts[11])
	require.Equal(t, 4, counts[22])

	_, acquired, err := cache.AcquireAccountProxySlot(ctx, 7, slots, "overflow")
	require.NoError(t, err)
	require.False(t, acquired)
}

func TestAccountProxyConcurrencyRotatesEqualLoadsAndReleasesIdempotently(t *testing.T) {
	cache, ctx := newAccountProxyConcurrencyCacheTest(t)
	slots := []service.AccountProxySlotSpec{
		{ProxyID: 31, MaxConcurrency: 2},
		{ProxyID: 32, MaxConcurrency: 2},
	}

	for i, expected := range []int64{31, 32, 31, 32} {
		proxyID, acquired, err := cache.AcquireAccountProxySlot(ctx, 8, slots, fmt.Sprintf("req-%d", i))
		require.NoError(t, err)
		require.True(t, acquired)
		require.Equal(t, expected, proxyID)
	}

	require.NoError(t, cache.ReleaseAccountProxySlot(ctx, 8, 31, "req-0"))
	require.NoError(t, cache.ReleaseAccountProxySlot(ctx, 8, 31, "req-0"))
	proxyID, acquired, err := cache.AcquireAccountProxySlot(ctx, 8, slots, "replacement")
	require.NoError(t, err)
	require.True(t, acquired)
	require.Equal(t, int64(31), proxyID)
}
