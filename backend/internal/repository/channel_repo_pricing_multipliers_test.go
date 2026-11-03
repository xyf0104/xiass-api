//go:build unit

package repository

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func pricingMultiplier(value float64) *float64 {
	return &value
}

func newChannelPricingMultiplierRepo(t *testing.T) (*channelRepository, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return &channelRepository{db: db}, mock
}

func TestChannelPricingMultipliersRoundTrip(t *testing.T) {
	repo, mock := newChannelPricingMultiplierRepo(t)
	now := time.Date(2026, 8, 20, 0, 0, 0, 0, time.UTC)
	mock.ExpectQuery(`SELECT id, channel_id, platform, models, billing_mode, input_price, output_price, cache_write_price, cache_write_1h_price, cache_read_price, fast_multiplier, flex_multiplier`).
		WithArgs(int64(7)).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "channel_id", "platform", "models", "billing_mode", "input_price", "output_price", "cache_write_price", "cache_write_1h_price", "cache_read_price",
			"fast_multiplier", "flex_multiplier", "image_input_price", "image_output_price", "per_request_price", "created_at", "updated_at",
		}).AddRow(
			int64(11), int64(7), "openai", `["gpt-5"]`, service.BillingModeToken,
			nil, nil, 5e-6, 8e-6, nil, 2.5, 0.5, nil, nil, nil, now, now,
		))
	mock.ExpectQuery(`SELECT id, pricing_id, min_tokens, max_tokens, tier_label`).
		WithArgs(sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "pricing_id", "min_tokens", "max_tokens", "tier_label", "input_price", "output_price", "cache_write_price", "cache_write_1h_price", "cache_read_price",
			"input_multiplier", "output_multiplier", "cache_write_multiplier", "cache_read_multiplier", "per_request_price", "sort_order", "created_at", "updated_at",
		}).AddRow(
			int64(21), int64(11), 272000, nil, "", nil, nil, 10e-6, 16e-6, nil, 2.0, 1.5, 2.0, 2.0, nil, 0, now, now,
		))

	pricing, err := repo.ListModelPricing(context.Background(), 7)
	require.NoError(t, err)
	require.Len(t, pricing, 1)
	require.Equal(t, 2.5, *pricing[0].FastMultiplier)
	require.Equal(t, 0.5, *pricing[0].FlexMultiplier)
	require.Equal(t, 5e-6, *pricing[0].CacheWritePrice)
	require.Equal(t, 8e-6, *pricing[0].CacheWrite1hPrice)
	require.Len(t, pricing[0].Intervals, 1)
	require.Equal(t, 2.0, *pricing[0].Intervals[0].InputMultiplier)
	require.Equal(t, 1.5, *pricing[0].Intervals[0].OutputMultiplier)
	require.Equal(t, 2.0, *pricing[0].Intervals[0].CacheWriteMultiplier)
	require.Equal(t, 2.0, *pricing[0].Intervals[0].CacheReadMultiplier)
	require.Equal(t, 16e-6, *pricing[0].Intervals[0].CacheWrite1hPrice)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestChannelPricingMultipliersWrite(t *testing.T) {
	pricing := &service.ChannelModelPricing{
		ID:                11,
		ChannelID:         7,
		Platform:          "openai",
		Models:            []string{"gpt-5"},
		BillingMode:       service.BillingModeToken,
		FastMultiplier:    pricingMultiplier(2.5),
		FlexMultiplier:    pricingMultiplier(0.5),
		CacheWrite1hPrice: pricingMultiplier(8e-6),
		Intervals: []service.PricingInterval{{
			MinTokens: 272000, CacheWrite1hPrice: pricingMultiplier(16e-6),
		}},
	}

	t.Run("create", func(t *testing.T) {
		repo, mock := newChannelPricingMultiplierRepo(t)
		mock.ExpectQuery(`INSERT INTO channel_model_pricing`).
			WithArgs(
				int64(7), "openai", []byte(`["gpt-5"]`), service.BillingModeToken,
				nil, nil, nil, pricing.CacheWrite1hPrice, nil, pricing.FastMultiplier, pricing.FlexMultiplier, nil, nil, nil,
			).
			WillReturnRows(sqlmock.NewRows([]string{"id", "created_at", "updated_at"}).AddRow(int64(11), time.Time{}, time.Time{}))
		mock.ExpectQuery(`INSERT INTO channel_pricing_intervals`).
			WithArgs(int64(11), 272000, nil, "", nil, nil, nil, pricing.Intervals[0].CacheWrite1hPrice, nil, nil, nil, nil, nil, nil, 0).
			WillReturnRows(sqlmock.NewRows([]string{"id", "created_at", "updated_at"}).AddRow(int64(21), time.Time{}, time.Time{}))

		require.NoError(t, repo.CreateModelPricing(context.Background(), pricing))
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("update", func(t *testing.T) {
		repo, mock := newChannelPricingMultiplierRepo(t)
		mock.ExpectExec(`(?s)UPDATE channel_model_pricing.*cache_write_1h_price = \$6.*fast_multiplier = \$8, flex_multiplier = \$9.*per_request_price = \$12, platform = \$13.*WHERE id = \$14`).
			WithArgs(
				[]byte(`["gpt-5"]`), service.BillingModeToken,
				nil, nil, nil, pricing.CacheWrite1hPrice, nil, pricing.FastMultiplier, pricing.FlexMultiplier, nil, nil, nil, "openai", int64(11),
			).
			WillReturnResult(sqlmock.NewResult(0, 1))

		require.NoError(t, repo.UpdateModelPricing(context.Background(), pricing))
		require.NoError(t, mock.ExpectationsWereMet())
	})
}
