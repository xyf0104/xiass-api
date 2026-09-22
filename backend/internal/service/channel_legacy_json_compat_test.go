package service

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestChannelModelPricingLegacyJSONKeepsExistingPrices(t *testing.T) {
	legacy := `{
		"ID":7,"ChannelID":9,"Platform":"openai","Models":["gpt-6-astra"],"BillingMode":"token",
		"InputPrice":0,"OutputPrice":0.000002,"CacheWritePrice":0.000003,"CacheWrite1hPrice":0.000006,"CacheReadPrice":0,
		"FastMultiplier":1.5,"FlexMultiplier":0.5,"MaxReasoningEffortMultiplier":2,
		"ImageInputPrice":0.000008,"ImageOutputPrice":0.000009,"PerRequestPrice":null,
		"Intervals":[{"ID":11,"PricingID":7,"MinTokens":0,"MaxTokens":128000,"TierLabel":"standard",
			"InputPrice":0,"OutputPrice":0.000004,"CacheWritePrice":0.000005,"CacheWrite1hPrice":0.000010,"CacheReadPrice":0,
			"InputMultiplier":1.1,"OutputMultiplier":1.2,"CacheWriteMultiplier":1.3,"CacheReadMultiplier":1.4,
			"PerRequestPrice":null,"SortOrder":3}],"CreatedAt":"2026-09-20T00:00:00Z"
	}`
	canonical := `{
		"id":7,"channel_id":9,"platform":"openai","models":["gpt-6-astra"],"billing_mode":"token",
		"input_price":0,"output_price":0.000002,"cache_write_price":0.000003,"cache_write_1h_price":0.000006,"cache_read_price":0,
		"fast_multiplier":1.5,"flex_multiplier":0.5,"max_reasoning_effort_multiplier":2,
		"image_input_price":0.000008,"image_output_price":0.000009,"per_request_price":null,
		"intervals":[{"id":11,"pricing_id":7,"min_tokens":0,"max_tokens":128000,"tier_label":"standard",
			"input_price":0,"output_price":0.000004,"cache_write_price":0.000005,"cache_write_1h_price":0.000010,"cache_read_price":0,
			"input_multiplier":1.1,"output_multiplier":1.2,"cache_write_multiplier":1.3,"cache_read_multiplier":1.4,
			"per_request_price":null,"sort_order":3}],"created_at":"2026-09-20T00:00:00Z"
	}`
	var current ChannelModelPricing
	require.NoError(t, json.Unmarshal([]byte(canonical), &current))
	for name, payload := range map[string]string{"legacy_database": legacy, "current_api": canonical} {
		t.Run(name, func(t *testing.T) {
			var pricing ChannelModelPricing
			require.NoError(t, json.Unmarshal([]byte(payload), &pricing))
			require.Equal(t, current, pricing, "old stored prices must load identically to current API prices")
			require.Equal(t, BillingModeToken, pricing.BillingMode)
			require.NotNil(t, pricing.InputPrice)
			require.Zero(t, *pricing.InputPrice, "explicit free input is not an unset price")
			require.NotNil(t, pricing.OutputPrice)
			require.InDelta(t, 0.000002, *pricing.OutputPrice, 1e-12)
			require.NotNil(t, pricing.CacheWrite1hPrice)
			require.InDelta(t, 0.000006, *pricing.CacheWrite1hPrice, 1e-12)
			require.Equal(t, time.Date(2026, 9, 20, 0, 0, 0, 0, time.UTC), pricing.CreatedAt)
			require.Len(t, pricing.Intervals, 1)
			require.Equal(t, "standard", pricing.Intervals[0].TierLabel)
			require.NotNil(t, pricing.Intervals[0].MaxTokens)
			require.Equal(t, 128000, *pricing.Intervals[0].MaxTokens)
			require.NotNil(t, pricing.Intervals[0].InputPrice)
			require.Zero(t, *pricing.Intervals[0].InputPrice)
			require.Equal(t, 3, pricing.Intervals[0].SortOrder)
			require.Nil(t, pricing.Intervals[0].PerRequestPrice)
			encoded, err := json.Marshal(pricing)
			require.NoError(t, err)
			var restored ChannelModelPricing
			require.NoError(t, json.Unmarshal(encoded, &restored))
			require.Equal(t, pricing, restored)
		})
	}
}

func TestChannelModelPricingFinalModeJSONKeepsZeroAndUnsetSeparate(t *testing.T) {
	var pricing ChannelModelPricing
	require.NoError(t, json.Unmarshal([]byte(`{"models":["gpt-6-astra"],"billing_mode":"token","price_mode":"final","input_price":0,"output_price":null}`), &pricing))
	require.Equal(t, PriceModeFinal, pricing.PriceMode)
	require.NotNil(t, pricing.InputPrice)
	require.Zero(t, *pricing.InputPrice)
	require.Nil(t, pricing.OutputPrice)
	require.Nil(t, pricing.CacheReadPrice)
	encoded, err := json.Marshal(pricing)
	require.NoError(t, err)
	var restored ChannelModelPricing
	require.NoError(t, json.Unmarshal(encoded, &restored))
	require.Equal(t, pricing, restored)
}
