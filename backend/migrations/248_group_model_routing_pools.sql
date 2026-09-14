-- Keep account-pool preferences separate from direct account preferences so
-- pool membership changes take effect without rewriting every group rule.
ALTER TABLE groups
    ADD COLUMN IF NOT EXISTS model_routing_pools JSONB DEFAULT '{}'::jsonb;

COMMENT ON COLUMN groups.model_routing_pools IS
    '模型优先路由号池：{"model_pattern": [account_pool_id1, account_pool_id2], ...}';
