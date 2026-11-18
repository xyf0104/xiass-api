//go:build integration

package repository

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestChannelUltrafastPricingPersistsAndClears(t *testing.T) {
	ctx := context.Background()
	repo := NewChannelRepository(integrationDB)
	fast, ultra, input := 2.0, 4.0, 2e-6
	channel := &service.Channel{
		Name: fmt.Sprintf("ultrafast-%d", time.Now().UnixNano()), Status: service.StatusActive,
		BillingModelSource: service.BillingModelSourceRequested,
		ModelPricing: []service.ChannelModelPricing{{
			Platform: service.PlatformOpenAI, Models: []string{"gpt-6.2-sol"},
			BillingMode: service.BillingModeToken, InputPrice: &input,
			FastMultiplier: &fast, UltrafastMultiplier: &ultra,
		}},
	}
	require.NoError(t, repo.Create(ctx, channel))
	t.Cleanup(func() {
		_, err := integrationDB.ExecContext(ctx, "DELETE FROM channels WHERE id = $1", channel.ID)
		require.NoError(t, err)
	})
	prices, err := repo.ListModelPricing(ctx, channel.ID)
	require.NoError(t, err)
	require.Len(t, prices, 1)
	require.Equal(t, ultra, *prices[0].UltrafastMultiplier)
	require.Equal(t, fast, *prices[0].FastMultiplier)
	prices[0].UltrafastMultiplier = nil
	require.NoError(t, repo.UpdateModelPricing(ctx, &prices[0]))
	reloaded, err := repo.GetByID(ctx, channel.ID)
	require.NoError(t, err)
	require.Len(t, reloaded.ModelPricing, 1)
	require.Nil(t, reloaded.ModelPricing[0].UltrafastMultiplier)
	require.Equal(t, fast, *reloaded.ModelPricing[0].FastMultiplier)
	require.Equal(t, input, *reloaded.ModelPricing[0].InputPrice)
}
