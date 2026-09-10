package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestAPIKeyRepositoryAuthPricingFreshDB(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(fmt.Sprint(enabled), func(t *testing.T) {
			repo, client := newAPIKeyRepoSQLite(t)
			ctx := context.Background()
			user := mustCreateAPIKeyRepoUser(t, ctx, client, "auth-pricing@test.com")
			price, multiplier, max := 0.000003, 2.0, 200000
			pricing := []service.ChannelModelPricing{{
				Platform: service.PlatformOpenAI, Models: []string{"gpt-5.6-luna"}, BillingMode: service.BillingModeToken,
				InputPrice: &price, CacheReadPrice: &price,
				Intervals: []service.PricingInterval{
					{MaxTokens: &max, InputPrice: &price, TierLabel: "first"},
					{MinTokens: max, InputMultiplier: &multiplier, TierLabel: "long"},
				},
			}}
			payload, err := json.Marshal(pricing)
			require.NoError(t, err)
			group, err := client.Group.Create().SetName("auth-pricing").
				SetPlatform(service.PlatformOpenAI).SetLongContextPricingEnabled(enabled).
				SetModelPricing(payload).Save(ctx)
			require.NoError(t, err)
			key := &service.APIKey{UserID: user.ID, Key: "sk-auth-pricing-test", Name: "Auth Pricing", GroupID: &group.ID, Status: service.StatusActive}
			require.NoError(t, repo.Create(ctx, key))
			for _, state := range []bool{enabled, !enabled} {
				_, err = client.Group.UpdateOneID(group.ID).SetLongContextPricingEnabled(state).Save(ctx)
				require.NoError(t, err)
				fresh, err := repo.GetByKeyForAuth(ctx, key.Key)
				require.NoError(t, err)
				require.Equal(t, state, fresh.Group.LongContextPricingEnabled)
				require.Equal(t, pricing, fresh.Group.ModelPricing)
				full, err := repo.GetByKey(ctx, key.Key)
				require.NoError(t, err)
				require.Equal(t, full.Group.LongContextPricingEnabled, fresh.Group.LongContextPricingEnabled)
				require.Equal(t, full.Group.ModelPricing, fresh.Group.ModelPricing)
				fresh.Group.ModelPricing[0].Models[0] = "changed"
				*fresh.Group.ModelPricing[0].Intervals[0].InputPrice = 99
				reloaded, err := repo.GetByKeyForAuth(ctx, key.Key)
				require.NoError(t, err)
				require.Equal(t, pricing, reloaded.Group.ModelPricing)
				cache := &repositoryPricingAuthCache{}
				counted := &repositoryPricingAuthCounter{APIKeyRepository: repo}
				svc := service.NewAPIKeyService(counted, nil, nil, nil, nil, cache,
					&config.Config{APIKeyAuth: config.APIKeyAuthCacheConfig{L2TTLSeconds: 60}})
				for _, stage := range []string{"fresh-db-cache-miss", "json-cache-hit"} {
					authed, err := svc.GetByKey(ctx, key.Key)
					require.NoError(t, err, stage)
					require.Equal(t, state, authed.Group.LongContextPricingEnabled, stage)
					require.Equal(t, pricing, authed.Group.ModelPricing, stage)
					require.Equal(t, 1, counted.calls, stage)
					authed.Group.ModelPricing[0].Models[0] = "changed"
					*authed.Group.ModelPricing[0].Intervals[0].InputPrice = 99
				}
			}
		})
	}
}

type repositoryPricingAuthCounter struct {
	service.APIKeyRepository
	calls int
}

func (r *repositoryPricingAuthCounter) GetByKeyForAuth(ctx context.Context, key string) (*service.APIKey, error) {
	r.calls++
	return r.APIKeyRepository.GetByKeyForAuth(ctx, key)
}

type repositoryPricingAuthCache struct {
	service.APIKeyCache
	payload []byte
}

func (c *repositoryPricingAuthCache) GetAuthCache(context.Context, string) (*service.APIKeyAuthCacheEntry, error) {
	if c.payload == nil {
		return nil, nil
	}
	var entry service.APIKeyAuthCacheEntry
	err := json.Unmarshal(c.payload, &entry)
	return &entry, err
}

func (c *repositoryPricingAuthCache) SetAuthCache(_ context.Context, _ string, entry *service.APIKeyAuthCacheEntry, _ time.Duration) error {
	var err error
	c.payload, err = json.Marshal(entry)
	return err
}
