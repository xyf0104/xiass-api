-- Persist the verified original package so other XIASS instances can restore,
-- revalidate and unpack it without relying on one node's local filesystem.
ALTER TABLE sub2api_plugin_installations
    ADD COLUMN IF NOT EXISTS artifact_data BYTEA;
