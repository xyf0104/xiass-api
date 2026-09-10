//go:build unit

package service

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

type pricingAuthCache struct {
	authCacheStub
	payload []byte
}

func (c *pricingAuthCache) GetAuthCache(context.Context, string) (*APIKeyAuthCacheEntry, error) {
	if c.payload == nil {
		return nil, redis.Nil
	}
	var entry APIKeyAuthCacheEntry
	err := json.Unmarshal(c.payload, &entry)
	return &entry, err
}

func (c *pricingAuthCache) SetAuthCache(_ context.Context, _ string, entry *APIKeyAuthCacheEntry, _ time.Duration) error {
	var err error
	c.payload, err = json.Marshal(entry)
	return err
}

func pricingAuthFixture(enabled bool) *APIKey {
	price, multiplier, max, groupID := 0.000003, 2.0, 200000, int64(3)
	return &APIKey{
		ID: 1, UserID: 2, GroupID: &groupID, Status: StatusActive,
		User: &User{ID: 2, Status: StatusActive, Role: RoleUser},
		Group: &Group{ID: 3, Platform: PlatformOpenAI, Status: StatusActive,
			LongContextPricingEnabled: enabled,
			ModelPricing: []ChannelModelPricing{{
				Models: []string{"gpt-5.6-luna"}, BillingMode: BillingModeToken,
				InputPrice: &price, OutputPrice: &price, CacheWritePrice: &price, CacheReadPrice: &price,
				FastMultiplier: &multiplier, FlexMultiplier: &multiplier,
				ImageInputPrice: &price, ImageOutputPrice: &price, PerRequestPrice: &price,
				Intervals: []PricingInterval{{MaxTokens: &max, TierLabel: "first",
					InputPrice: &price, OutputPrice: &price, CacheWritePrice: &price, CacheReadPrice: &price,
					InputMultiplier: &multiplier, OutputMultiplier: &multiplier,
					CacheWriteMultiplier: &multiplier, CacheReadMultiplier: &multiplier, PerRequestPrice: &price,
				}, {MinTokens: max, TierLabel: "long", InputMultiplier: &multiplier}},
			}},
		},
	}
}

func TestAPIKeyAuthPricingMissL2AndL1Hit(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(fmt.Sprint(enabled), func(t *testing.T) {
			ctx := context.Background()
			source := pricingAuthFixture(enabled)
			source.GroupID = &source.Group.ID
			calls := 0
			repo := &authRepoStub{getByKeyForAuth: func(context.Context, string) (*APIKey, error) {
				calls++
				return source, nil
			}}
			cache := &pricingAuthCache{}
			cfg := &config.Config{APIKeyAuth: config.APIKeyAuthCacheConfig{L2TTLSeconds: 60}}
			svc := NewAPIKeyService(repo, nil, nil, nil, nil, cache, cfg)
			for _, stage := range []string{"miss", "l2-hit"} {
				got, err := svc.GetByKey(ctx, "pricing-key")
				require.NoError(t, err, stage)
				require.Equal(t, enabled, got.Group.LongContextPricingEnabled, stage)
				require.Equal(t, source.Group.ModelPricing, got.Group.ModelPricing, stage)
				require.Equal(t, 1, calls, stage)
				got.Group.ModelPricing[0].Models[0] = "mutated"
				*got.Group.ModelPricing[0].Intervals[0].InputPrice = 99
			}
			cfg.APIKeyAuth.L1Size, cfg.APIKeyAuth.L1TTLSeconds = 100, 60
			svc = NewAPIKeyService(repo, nil, nil, nil, nil, cache, cfg)
			t.Cleanup(svc.authCacheL1.Close)
			_, err := svc.GetByKey(ctx, "pricing-key")
			require.NoError(t, err)
			svc.authCacheL1.Wait()
			cached, ok := svc.authCacheL1.Get(svc.authCacheKey("pricing-key"))
			require.True(t, ok)
			require.NotNil(t, cached)
			cache.payload = nil // A hit must now come from L1, not Redis or the repo.
			for range 2 {
				got, err := svc.GetByKey(ctx, "pricing-key")
				require.NoError(t, err)
				require.Equal(t, enabled, got.Group.LongContextPricingEnabled)
				require.Equal(t, source.Group.ModelPricing, got.Group.ModelPricing)
				got.Group.ModelPricing[0].Intervals[0].TierLabel = "changed"
				*got.Group.ModelPricing[0].InputPrice = 99
			}
			require.Equal(t, 1, calls)
		})
	}
}

func TestAPIKeyAuthPricingRejectsV19AndReloads(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(fmt.Sprint(enabled), func(t *testing.T) {
			cache := &pricingAuthCache{payload: []byte(`{"snapshot":{"version":19,"api_key_id":1,"group":{"id":3}}}`)}
			source := pricingAuthFixture(enabled)
			calls := 0
			svc := NewAPIKeyService(&authRepoStub{getByKeyForAuth: func(context.Context, string) (*APIKey, error) {
				calls++
				return source, nil
			}}, nil, nil, nil, nil, cache, &config.Config{APIKeyAuth: config.APIKeyAuthCacheConfig{L2TTLSeconds: 60}})
			old, err := cache.GetAuthCache(context.Background(), "key")
			require.NoError(t, err)
			got, used, err := svc.applyAuthCacheEntry("key", old)
			require.NoError(t, err)
			require.False(t, used)
			require.Nil(t, got)
			for range 2 {
				got, err = svc.GetByKey(context.Background(), "key")
				require.NoError(t, err)
				require.Equal(t, enabled, got.Group.LongContextPricingEnabled)
				require.Equal(t, source.Group.ModelPricing, got.Group.ModelPricing)
			}
			require.Equal(t, 1, calls)
			entry, err := cache.GetAuthCache(context.Background(), "key")
			require.NoError(t, err)
			require.Equal(t, 20, entry.Snapshot.Version)
		})
	}
}

// Mutate all present scalar pointer fields, including future pricing fields,
// to catch accidental shallow copies at either side of the cache boundary.
func mutateAuthPricingPointers(value reflect.Value) {
	for i := 0; i < value.NumField(); i++ {
		field := value.Field(i)
		if field.Kind() != reflect.Pointer || field.IsNil() {
			continue
		}
		switch field.Elem().Kind() {
		case reflect.Float64:
			field.Elem().SetFloat(99)
		case reflect.Int:
			field.Elem().SetInt(99)
		}
	}
}

func TestAPIKeyAuthPricingSnapshotOwnsNestedData(t *testing.T) {
	svc := &APIKeyService{}
	source := pricingAuthFixture(true)
	expected := pricingAuthFixture(true).Group.ModelPricing
	snapshot := svc.snapshotFromAPIKey(context.Background(), source)
	mutate := func(pricing []ChannelModelPricing) {
		pricing[0].Models[0] = "changed"
		pricing[0].Intervals[0].TierLabel = "changed"
		mutateAuthPricingPointers(reflect.ValueOf(&pricing[0]).Elem())
		for i := range pricing[0].Intervals {
			mutateAuthPricingPointers(reflect.ValueOf(&pricing[0].Intervals[i]).Elem())
		}
	}
	mutate(source.Group.ModelPricing)
	require.Equal(t, expected, snapshot.Group.ModelPricing)
	first := svc.snapshotToAPIKey("key", snapshot)
	mutate(first.Group.ModelPricing)
	require.Equal(t, expected, snapshot.Group.ModelPricing)
	require.Equal(t, expected, svc.snapshotToAPIKey("key", snapshot).Group.ModelPricing)
	for _, pricing := range [][]ChannelModelPricing{nil, {}} {
		source.Group.ModelPricing = pricing
		got := svc.snapshotToAPIKey("key", svc.snapshotFromAPIKey(context.Background(), source))
		require.Equal(t, pricing, got.Group.ModelPricing)
	}
}
