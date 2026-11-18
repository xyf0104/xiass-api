package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestChannelPricingUltrafastMultiplierMigration(t *testing.T) {
	content, err := FS.ReadFile("276_channel_pricing_ultrafast_multiplier.sql")
	require.NoError(t, err)
	sql := strings.Join(strings.Fields(string(content)), " ")
	require.Contains(t, sql, "ADD COLUMN IF NOT EXISTS ultrafast_multiplier NUMERIC(12,6)")
	require.Contains(t, sql, "conname = 'channel_model_pricing_ultrafast_multiplier_positive' AND conrelid = 'channel_model_pricing'::regclass")
	require.Contains(t, sql, "CHECK (ultrafast_multiplier IS NULL OR ultrafast_multiplier > 0)")
	require.NotContains(t, sql, "UPDATE channel_model_pricing")
}
