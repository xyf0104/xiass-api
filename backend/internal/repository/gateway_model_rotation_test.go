package repository

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func newOpenAIModelRotationTestCache(t *testing.T) (*gatewayCache, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	return &gatewayCache{rdb: client}, mr
}

func TestGatewayOpenAIModelRotationScopeAndAccountIndependence(t *testing.T) {
	cache, _ := newOpenAIModelRotationTestCache(t)
	ctx := context.Background()
	now := time.Now()
	scopeA := strings.Repeat("a", 64)
	scopeB := strings.Repeat("b", 64)

	require.NoError(t, cache.ObserveOpenAIModelRotation(ctx, scopeA, 11, now, now.Add(time.Hour), time.Hour))
	require.NoError(t, cache.ObserveOpenAIModelRotation(ctx, scopeA, 12, now.Add(time.Millisecond), now.Add(2*time.Hour), 2*time.Hour))
	require.NoError(t, cache.ObserveOpenAIModelRotation(ctx, scopeB, 11, now, now.Add(3*time.Hour), 3*time.Hour))

	gotA, err := cache.GetOpenAIModelRotationFailures(ctx, scopeA, now.Add(time.Second))
	require.NoError(t, err)
	require.ElementsMatch(t, []int64{11, 12}, gotA)
	gotB, err := cache.GetOpenAIModelRotationFailures(ctx, scopeB, now.Add(time.Second))
	require.NoError(t, err)
	require.Equal(t, []int64{11}, gotB)

	// A matching response clears only its own account in its own caller scope.
	require.NoError(t, cache.ObserveOpenAIModelRotation(ctx, scopeA, 11, now.Add(2*time.Millisecond), time.Time{}, time.Hour))
	gotA, err = cache.GetOpenAIModelRotationFailures(ctx, scopeA, now.Add(time.Second))
	require.NoError(t, err)
	require.Equal(t, []int64{12}, gotA)
	gotB, err = cache.GetOpenAIModelRotationFailures(ctx, scopeB, now.Add(time.Second))
	require.NoError(t, err)
	require.Equal(t, []int64{11}, gotB)
}

func TestGatewayOpenAIModelRotationExpiresPerAccount(t *testing.T) {
	cache, mr := newOpenAIModelRotationTestCache(t)
	ctx := context.Background()
	now := time.Now()
	scope := strings.Repeat("c", 64)

	require.NoError(t, cache.ObserveOpenAIModelRotation(ctx, scope, 21, now, now.Add(time.Minute), 2*time.Minute))
	require.NoError(t, cache.ObserveOpenAIModelRotation(ctx, scope, 22, now.Add(time.Millisecond), now.Add(2*time.Minute), 3*time.Minute))

	got, err := cache.GetOpenAIModelRotationFailures(ctx, scope, now.Add(90*time.Second))
	require.NoError(t, err)
	require.Equal(t, []int64{22}, got)

	blockedKey, _, _, err := openAIModelRotationKeys(scope)
	require.NoError(t, err)
	members, err := mr.ZMembers(blockedKey)
	require.NoError(t, err)
	require.NotContains(t, members, "21", "expired account should be removed from Redis")
}

func TestGatewayOpenAIModelRotationRejectsOlderResults(t *testing.T) {
	cache, _ := newOpenAIModelRotationTestCache(t)
	ctx := context.Background()
	now := time.Now()

	t.Run("older match cannot clear newer mismatch", func(t *testing.T) {
		scope := strings.Repeat("d", 64)
		require.NoError(t, cache.ObserveOpenAIModelRotation(ctx, scope, 31, now.Add(2*time.Second), now.Add(time.Hour), time.Hour))
		require.NoError(t, cache.ObserveOpenAIModelRotation(ctx, scope, 31, now.Add(time.Second), time.Time{}, time.Hour))

		got, err := cache.GetOpenAIModelRotationFailures(ctx, scope, now.Add(3*time.Second))
		require.NoError(t, err)
		require.Equal(t, []int64{31}, got)
	})

	t.Run("older mismatch cannot restore after newer match", func(t *testing.T) {
		scope := strings.Repeat("1", 64)
		require.NoError(t, cache.ObserveOpenAIModelRotation(ctx, scope, 32, now.Add(4*time.Second), time.Time{}, time.Hour))
		require.NoError(t, cache.ObserveOpenAIModelRotation(ctx, scope, 32, now.Add(3*time.Second), now.Add(time.Hour), time.Hour))

		got, err := cache.GetOpenAIModelRotationFailures(ctx, scope, now.Add(5*time.Second))
		require.NoError(t, err)
		require.Empty(t, got)
	})
}

func TestGatewayOpenAIModelRotationBoundsStorageTTL(t *testing.T) {
	cache, mr := newOpenAIModelRotationTestCache(t)
	ctx := context.Background()
	now := time.Now()

	shortScope := strings.Repeat("e", 64)
	require.NoError(t, cache.ObserveOpenAIModelRotation(ctx, shortScope, 41, now, now.Add(time.Second), time.Nanosecond))
	shortBlockedKey, shortVersionsKey, shortExpiryKey, err := openAIModelRotationKeys(shortScope)
	require.NoError(t, err)
	for _, key := range []string{shortBlockedKey, shortVersionsKey, shortExpiryKey} {
		ttl := mr.TTL(key)
		require.GreaterOrEqual(t, ttl, openAIModelRotationMinStorageTTL)
		require.LessOrEqual(t, ttl, openAIModelRotationMinStorageTTL+openAIModelRotationStorageMargin)
	}

	longScope := strings.Repeat("f", 64)
	require.NoError(t, cache.ObserveOpenAIModelRotation(ctx, longScope, 42, now, now.Add(72*time.Hour), 72*time.Hour))
	longBlockedKey, longVersionsKey, longExpiryKey, err := openAIModelRotationKeys(longScope)
	require.NoError(t, err)
	for _, key := range []string{longBlockedKey, longVersionsKey, longExpiryKey} {
		ttl := mr.TTL(key)
		require.Greater(t, ttl, openAIModelRotationMaxFailureTTL)
		require.LessOrEqual(t, ttl, openAIModelRotationMaxStorageTTL)
	}

	require.ErrorIs(t, cache.ObserveOpenAIModelRotation(ctx, shortScope, 43, now, now.Add(time.Minute), 0), errOpenAIModelRotationTTL)
}

func TestGatewayOpenAIModelRotationValidatesInputsAndRedis(t *testing.T) {
	ctx := context.Background()
	now := time.Now()
	validScope := strings.Repeat("0", 64)

	var nilCache *gatewayCache
	_, err := nilCache.GetOpenAIModelRotationFailures(ctx, validScope, now)
	require.ErrorIs(t, err, errOpenAIModelRotationUnavailable)
	require.ErrorIs(t, nilCache.ObserveOpenAIModelRotation(ctx, validScope, 1, now, time.Time{}, time.Minute), errOpenAIModelRotationUnavailable)

	cache, _ := newOpenAIModelRotationTestCache(t)
	_, err = cache.GetOpenAIModelRotationFailures(ctx, "../not-a-scope", now)
	require.ErrorIs(t, err, errOpenAIModelRotationScope)
	require.ErrorIs(t, cache.ObserveOpenAIModelRotation(ctx, validScope, 0, now, time.Time{}, time.Minute), errOpenAIModelRotationAccountID)

	unavailableClient := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", MaxRetries: -1})
	t.Cleanup(func() { require.NoError(t, unavailableClient.Close()) })
	unavailableCache := &gatewayCache{rdb: unavailableClient}
	unavailableCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	_, err = unavailableCache.GetOpenAIModelRotationFailures(unavailableCtx, validScope, now)
	require.Error(t, err)
	require.Error(t, unavailableCache.ObserveOpenAIModelRotation(unavailableCtx, validScope, 1, now, time.Time{}, time.Minute))
}
