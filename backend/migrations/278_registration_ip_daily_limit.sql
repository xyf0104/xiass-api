ALTER TABLE users ADD COLUMN IF NOT EXISTS registration_ip VARCHAR(64) NOT NULL DEFAULT '';

CREATE INDEX IF NOT EXISTS users_registration_ip_created_at_idx
    ON users (registration_ip, created_at);
