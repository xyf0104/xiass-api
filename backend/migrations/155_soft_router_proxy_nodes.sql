-- Soft-router SOCKS node management.
-- This keeps OpenWrt/PassWall discovery and public SOCKS mappings separate
-- from the existing user-facing proxies table while allowing mappings to sync
-- into proxies through proxy_id.

CREATE TABLE IF NOT EXISTS soft_router_proxy_config (
    id                  SMALLINT PRIMARY KEY DEFAULT 1,
    enabled             BOOLEAN NOT NULL DEFAULT FALSE,
    public_host         VARCHAR(255) NOT NULL DEFAULT '',
    gateway_listen_host VARCHAR(255) NOT NULL DEFAULT '0.0.0.0',
    upstream_host       VARCHAR(255) NOT NULL DEFAULT '127.0.0.1',
    frp_server_host     VARCHAR(255) NOT NULL DEFAULT '',
    frp_server_port     INT NOT NULL DEFAULT 7010,
    frp_token           VARCHAR(255) NOT NULL DEFAULT '',
    raw_port_start      INT NOT NULL DEFAULT 12081,
    raw_port_end        INT NOT NULL DEFAULT 12150,
    public_port_start   INT NOT NULL DEFAULT 1081,
    public_port_end     INT NOT NULL DEFAULT 1100,
    default_username    VARCHAR(100) NOT NULL DEFAULT '',
    default_password    VARCHAR(255) NOT NULL DEFAULT '',
    agent_poll_seconds  INT NOT NULL DEFAULT 20,
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT soft_router_proxy_config_singleton CHECK (id = 1)
);

INSERT INTO soft_router_proxy_config (id)
VALUES (1)
ON CONFLICT (id) DO NOTHING;

CREATE TABLE IF NOT EXISTS soft_router_agents (
    id              BIGSERIAL PRIMARY KEY,
    name            VARCHAR(100) NOT NULL,
    token           VARCHAR(255) NOT NULL UNIQUE,
    hostname        VARCHAR(255) NOT NULL DEFAULT '',
    description     TEXT NOT NULL DEFAULT '',
    status          VARCHAR(20) NOT NULL DEFAULT 'offline',
    last_seen_at    TIMESTAMPTZ,
    last_error      TEXT NOT NULL DEFAULT '',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at      TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS soft_router_agents_status_idx ON soft_router_agents(status);
CREATE INDEX IF NOT EXISTS soft_router_agents_deleted_at_idx ON soft_router_agents(deleted_at);

CREATE TABLE IF NOT EXISTS soft_router_socks_nodes (
    id              BIGSERIAL PRIMARY KEY,
    agent_id        BIGINT NOT NULL REFERENCES soft_router_agents(id) ON DELETE CASCADE,
    openwrt_id      VARCHAR(100) NOT NULL,
    node_key        VARCHAR(160) NOT NULL,
    name            VARCHAR(255) NOT NULL,
    openwrt_port    INT NOT NULL,
    http_port       INT NOT NULL DEFAULT 0,
    node_ref        VARCHAR(100) NOT NULL DEFAULT '',
    listen_status   VARCHAR(20) NOT NULL DEFAULT 'unknown',
    enabled         BOOLEAN NOT NULL DEFAULT FALSE,
    last_seen_at    TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at      TIMESTAMPTZ,
    UNIQUE(agent_id, node_key)
);

CREATE INDEX IF NOT EXISTS soft_router_socks_nodes_agent_idx ON soft_router_socks_nodes(agent_id);
CREATE INDEX IF NOT EXISTS soft_router_socks_nodes_deleted_at_idx ON soft_router_socks_nodes(deleted_at);

CREATE TABLE IF NOT EXISTS soft_router_proxy_mappings (
    id              BIGSERIAL PRIMARY KEY,
    agent_id        BIGINT NOT NULL REFERENCES soft_router_agents(id) ON DELETE CASCADE,
    node_id         BIGINT REFERENCES soft_router_socks_nodes(id) ON DELETE SET NULL,
    name            VARCHAR(255) NOT NULL,
    openwrt_port    INT NOT NULL,
    raw_remote_port INT NOT NULL,
    public_port     INT NOT NULL,
    username        VARCHAR(100) NOT NULL,
    password        VARCHAR(255) NOT NULL,
    enabled         BOOLEAN NOT NULL DEFAULT TRUE,
    proxy_id        BIGINT REFERENCES proxies(id) ON DELETE SET NULL,
    status          VARCHAR(20) NOT NULL DEFAULT 'pending',
    last_error      TEXT NOT NULL DEFAULT '',
    last_test_at    TIMESTAMPTZ,
    last_exit_ip    VARCHAR(100) NOT NULL DEFAULT '',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at      TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS soft_router_proxy_mappings_agent_idx ON soft_router_proxy_mappings(agent_id);
CREATE INDEX IF NOT EXISTS soft_router_proxy_mappings_node_idx ON soft_router_proxy_mappings(node_id);
CREATE INDEX IF NOT EXISTS soft_router_proxy_mappings_proxy_idx ON soft_router_proxy_mappings(proxy_id);
CREATE INDEX IF NOT EXISTS soft_router_proxy_mappings_deleted_at_idx ON soft_router_proxy_mappings(deleted_at);
CREATE UNIQUE INDEX IF NOT EXISTS soft_router_proxy_mappings_raw_remote_port_active_idx
    ON soft_router_proxy_mappings(raw_remote_port)
    WHERE deleted_at IS NULL;
CREATE UNIQUE INDEX IF NOT EXISTS soft_router_proxy_mappings_public_port_active_idx
    ON soft_router_proxy_mappings(public_port)
    WHERE deleted_at IS NULL;
