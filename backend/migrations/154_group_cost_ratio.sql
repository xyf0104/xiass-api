-- Migration: 154_group_cost_ratio
-- 分组新增成本比例字段，用于前端将 rate_multiplier 换算为"成本价倍数"展示。
-- 该字段为纯展示用途，不影响实际计费逻辑。NULL 表示不启用成本倍率展示。

ALTER TABLE groups
    ADD COLUMN IF NOT EXISTS cost_ratio DECIMAL(10,4) DEFAULT NULL;
