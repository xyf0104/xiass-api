CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_usage_logs_api_key_created_cost
    ON usage_logs (api_key_id, created_at)
    INCLUDE (actual_cost);
