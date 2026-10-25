-- Historical request egress snapshot. NULL preserves pre-migration rows and
-- requests for which no route snapshot was captured.
SET LOCAL lock_timeout = '2s';

ALTER TABLE usage_logs
    ADD COLUMN IF NOT EXISTS egress JSONB;
