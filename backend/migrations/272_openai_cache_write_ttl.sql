-- OpenAI cache lifetimes are independent of Anthropic 5m/1h buckets.
-- Existing usage remains aggregate-only; existing operator prices remain flat.
ALTER TABLE usage_logs
    ADD COLUMN IF NOT EXISTS cache_creation_30m_tokens INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS cache_creation_12h_tokens INTEGER NOT NULL DEFAULT 0;

ALTER TABLE channel_model_pricing
    ADD COLUMN IF NOT EXISTS cache_write_12h_price NUMERIC(20,12);
COMMENT ON COLUMN channel_model_pricing.cache_write_12h_price IS
    'OpenAI 12h write price per token; NULL preserves cache_write_price';

ALTER TABLE channel_pricing_intervals
    ADD COLUMN IF NOT EXISTS cache_write_12h_price NUMERIC(20,12);
COMMENT ON COLUMN channel_pricing_intervals.cache_write_12h_price IS
    'OpenAI 12h write price per token; NULL preserves cache_write_price';

ALTER TABLE channel_account_stats_model_pricing
    ADD COLUMN IF NOT EXISTS cache_write_12h_price NUMERIC(20,12);
COMMENT ON COLUMN channel_account_stats_model_pricing.cache_write_12h_price IS
    'OpenAI 12h write price per token; NULL preserves cache_write_price';

ALTER TABLE channel_account_stats_pricing_intervals
    ADD COLUMN IF NOT EXISTS cache_write_12h_price NUMERIC(20,12);
COMMENT ON COLUMN channel_account_stats_pricing_intervals.cache_write_12h_price IS
    'OpenAI 12h write price per token; NULL preserves cache_write_price';
