-- API keys may bind multiple groups while preserving the legacy primary group_id.
-- NULL means the row still uses the legacy group_id contract; an empty JSON array
-- is an explicit unbound value for the new API.
ALTER TABLE api_keys
    ADD COLUMN IF NOT EXISTS group_ids JSONB;

CREATE INDEX IF NOT EXISTS idx_api_keys_group_ids_gin
    ON api_keys USING GIN (group_ids);
