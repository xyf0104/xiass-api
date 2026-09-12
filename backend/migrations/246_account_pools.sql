-- XIASS administrative pools do not alter business groups or runtime scheduling.
CREATE TABLE IF NOT EXISTS account_pools (
    id BIGSERIAL PRIMARY KEY,
    name VARCHAR(100) NOT NULL CHECK (char_length(btrim(name)) > 0),
    proxy_id BIGINT REFERENCES proxies(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE UNIQUE INDEX IF NOT EXISTS account_pools_name_unique ON account_pools (lower(name));
CREATE INDEX IF NOT EXISTS accounts_xiass_account_pool_idx
    ON accounts ((extra ->> 'xiass_account_pool')) WHERE deleted_at IS NULL;
