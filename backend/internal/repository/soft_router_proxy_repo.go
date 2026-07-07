package repository

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

type softRouterRepository struct {
	sql sqlExecutor
}

func NewSoftRouterRepository(sqlDB *sql.DB) service.SoftRouterRepository {
	return &softRouterRepository{sql: sqlDB}
}

func (r *softRouterRepository) GetConfig(ctx context.Context) (*service.SoftRouterProxyConfig, error) {
	cfg := service.SoftRouterProxyConfig{}
	err := scanSingleRow(ctx, r.sql, `
		SELECT enabled, public_host, gateway_listen_host, upstream_host,
		       frp_server_host, frp_server_port, frp_token,
		       raw_port_start, raw_port_end, public_port_start, public_port_end,
		       default_username, default_password, agent_poll_seconds, updated_at
		FROM soft_router_proxy_config WHERE id = 1`,
		nil,
		&cfg.Enabled,
		&cfg.PublicHost,
		&cfg.GatewayListenHost,
		&cfg.UpstreamHost,
		&cfg.FRPServerHost,
		&cfg.FRPServerPort,
		&cfg.FRPToken,
		&cfg.RawPortStart,
		&cfg.RawPortEnd,
		&cfg.PublicPortStart,
		&cfg.PublicPortEnd,
		&cfg.DefaultUsername,
		&cfg.DefaultPassword,
		&cfg.AgentPollSeconds,
		&cfg.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	return &cfg, nil
}

func (r *softRouterRepository) UpdateConfig(ctx context.Context, cfg *service.SoftRouterProxyConfig) (*service.SoftRouterProxyConfig, error) {
	err := scanSingleRow(ctx, r.sql, `
		UPDATE soft_router_proxy_config SET
		    enabled = $1,
		    public_host = $2,
		    gateway_listen_host = $3,
		    upstream_host = $4,
		    frp_server_host = $5,
		    frp_server_port = $6,
		    frp_token = $7,
		    raw_port_start = $8,
		    raw_port_end = $9,
		    public_port_start = $10,
		    public_port_end = $11,
		    default_username = $12,
		    default_password = $13,
		    agent_poll_seconds = $14,
		    updated_at = NOW()
		WHERE id = 1
		RETURNING enabled, public_host, gateway_listen_host, upstream_host,
		          frp_server_host, frp_server_port, frp_token,
		          raw_port_start, raw_port_end, public_port_start, public_port_end,
		          default_username, default_password, agent_poll_seconds, updated_at`,
		[]any{
			cfg.Enabled,
			cfg.PublicHost,
			cfg.GatewayListenHost,
			cfg.UpstreamHost,
			cfg.FRPServerHost,
			cfg.FRPServerPort,
			cfg.FRPToken,
			cfg.RawPortStart,
			cfg.RawPortEnd,
			cfg.PublicPortStart,
			cfg.PublicPortEnd,
			cfg.DefaultUsername,
			cfg.DefaultPassword,
			cfg.AgentPollSeconds,
		},
		&cfg.Enabled,
		&cfg.PublicHost,
		&cfg.GatewayListenHost,
		&cfg.UpstreamHost,
		&cfg.FRPServerHost,
		&cfg.FRPServerPort,
		&cfg.FRPToken,
		&cfg.RawPortStart,
		&cfg.RawPortEnd,
		&cfg.PublicPortStart,
		&cfg.PublicPortEnd,
		&cfg.DefaultUsername,
		&cfg.DefaultPassword,
		&cfg.AgentPollSeconds,
		&cfg.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	return cfg, nil
}

func (r *softRouterRepository) ListAgents(ctx context.Context) ([]service.SoftRouterAgent, error) {
	rows, err := r.sql.QueryContext(ctx, `
		SELECT id, name, token, hostname, description, status, last_seen_at, last_error, created_at, updated_at
		FROM soft_router_agents
		WHERE deleted_at IS NULL
		ORDER BY id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []service.SoftRouterAgent
	for rows.Next() {
		var item service.SoftRouterAgent
		if err := scanSoftRouterAgent(rows, &item); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (r *softRouterRepository) GetAgentByID(ctx context.Context, id int64) (*service.SoftRouterAgent, error) {
	agent := service.SoftRouterAgent{}
	err := scanSingleRow(ctx, r.sql, `
		SELECT id, name, token, hostname, description, status, last_seen_at, last_error, created_at, updated_at
		FROM soft_router_agents
		WHERE id = $1 AND deleted_at IS NULL`,
		[]any{id},
		&agent.ID, &agent.Name, &agent.Token, &agent.Hostname, &agent.Description, &agent.Status,
		&agent.LastSeenAt, &agent.LastError, &agent.CreatedAt, &agent.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, service.ErrSoftRouterAgentNotFound
	}
	if err != nil {
		return nil, err
	}
	return &agent, nil
}

func (r *softRouterRepository) GetAgentByToken(ctx context.Context, token string) (*service.SoftRouterAgent, error) {
	agent := service.SoftRouterAgent{}
	err := scanSingleRow(ctx, r.sql, `
		SELECT id, name, token, hostname, description, status, last_seen_at, last_error, created_at, updated_at
		FROM soft_router_agents
		WHERE token = $1 AND deleted_at IS NULL`,
		[]any{token},
		&agent.ID, &agent.Name, &agent.Token, &agent.Hostname, &agent.Description, &agent.Status,
		&agent.LastSeenAt, &agent.LastError, &agent.CreatedAt, &agent.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, service.ErrSoftRouterAgentNotFound
	}
	if err != nil {
		return nil, err
	}
	return &agent, nil
}

func (r *softRouterRepository) CreateAgent(ctx context.Context, agent *service.SoftRouterAgent) error {
	return scanSingleRow(ctx, r.sql, `
		INSERT INTO soft_router_agents (name, token, hostname, description, status)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, name, token, hostname, description, status, last_seen_at, last_error, created_at, updated_at`,
		[]any{agent.Name, agent.Token, agent.Hostname, agent.Description, agent.Status},
		&agent.ID, &agent.Name, &agent.Token, &agent.Hostname, &agent.Description, &agent.Status,
		&agent.LastSeenAt, &agent.LastError, &agent.CreatedAt, &agent.UpdatedAt,
	)
}

func (r *softRouterRepository) UpdateAgent(ctx context.Context, agent *service.SoftRouterAgent) error {
	err := scanSingleRow(ctx, r.sql, `
		UPDATE soft_router_agents SET
		    name = $2,
		    token = $3,
		    hostname = $4,
		    description = $5,
		    status = $6,
		    last_error = $7,
		    updated_at = NOW()
		WHERE id = $1 AND deleted_at IS NULL
		RETURNING id, name, token, hostname, description, status, last_seen_at, last_error, created_at, updated_at`,
		[]any{agent.ID, agent.Name, agent.Token, agent.Hostname, agent.Description, agent.Status, agent.LastError},
		&agent.ID, &agent.Name, &agent.Token, &agent.Hostname, &agent.Description, &agent.Status,
		&agent.LastSeenAt, &agent.LastError, &agent.CreatedAt, &agent.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return service.ErrSoftRouterAgentNotFound
	}
	return err
}

func (r *softRouterRepository) DeleteAgent(ctx context.Context, id int64) error {
	result, err := r.sql.ExecContext(ctx, `UPDATE soft_router_agents SET deleted_at = NOW(), updated_at = NOW() WHERE id = $1 AND deleted_at IS NULL`, id)
	return rowsAffectedOrNotFound(result, err, service.ErrSoftRouterAgentNotFound)
}

func (r *softRouterRepository) TouchAgent(ctx context.Context, id int64, hostname string, seenAt time.Time) error {
	_, err := r.sql.ExecContext(ctx, `
		UPDATE soft_router_agents
		SET hostname = $2, status = $3, last_seen_at = $4, last_error = '', updated_at = NOW()
		WHERE id = $1 AND deleted_at IS NULL`,
		id, hostname, service.SoftRouterAgentStatusOnline, seenAt)
	return err
}

func (r *softRouterRepository) UpsertReportedNodes(ctx context.Context, agentID int64, nodes []service.SoftRouterSocksNodeReport, seenAt time.Time) error {
	for _, node := range nodes {
		openwrtID := strings.TrimSpace(node.ID)
		nodeKey := strings.TrimSpace(node.NodeKey)
		if nodeKey == "" {
			nodeKey = openwrtID
		}
		if nodeKey == "" {
			nodeKey = "port-" + strconvItoa(node.OpenWrtPort)
		}
		name := strings.TrimSpace(node.Name)
		if name == "" {
			name = "SOCKS " + strconvItoa(node.OpenWrtPort)
		}
		listen := strings.TrimSpace(node.ListenStatus)
		if listen == "" {
			listen = "unknown"
		}
		_, err := r.sql.ExecContext(ctx, `
			INSERT INTO soft_router_socks_nodes (
			    agent_id, openwrt_id, node_key, name, openwrt_port, http_port, node_ref,
			    listen_status, enabled, last_seen_at
			)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
			ON CONFLICT (agent_id, node_key)
			DO UPDATE SET
			    openwrt_id = EXCLUDED.openwrt_id,
			    name = EXCLUDED.name,
			    openwrt_port = EXCLUDED.openwrt_port,
			    http_port = EXCLUDED.http_port,
			    node_ref = EXCLUDED.node_ref,
			    listen_status = EXCLUDED.listen_status,
			    enabled = EXCLUDED.enabled,
			    last_seen_at = EXCLUDED.last_seen_at,
			    updated_at = NOW(),
			    deleted_at = NULL`,
			agentID, openwrtID, nodeKey, name, node.OpenWrtPort, node.HTTPPort,
			strings.TrimSpace(node.NodeRef), listen, node.Enabled, seenAt)
		if err != nil {
			return err
		}
	}
	return nil
}

func (r *softRouterRepository) ListNodes(ctx context.Context) ([]service.SoftRouterSocksNode, error) {
	rows, err := r.sql.QueryContext(ctx, `
		SELECT id, agent_id, openwrt_id, node_key, name, openwrt_port, http_port, node_ref,
		       listen_status, enabled, last_seen_at, created_at, updated_at
		FROM soft_router_socks_nodes
		WHERE deleted_at IS NULL
		ORDER BY agent_id ASC, openwrt_port ASC, id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []service.SoftRouterSocksNode
	for rows.Next() {
		var item service.SoftRouterSocksNode
		if err := scanSoftRouterNode(rows, &item); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (r *softRouterRepository) GetNodeByID(ctx context.Context, id int64) (*service.SoftRouterSocksNode, error) {
	node := service.SoftRouterSocksNode{}
	err := scanSingleRow(ctx, r.sql, `
		SELECT id, agent_id, openwrt_id, node_key, name, openwrt_port, http_port, node_ref,
		       listen_status, enabled, last_seen_at, created_at, updated_at
		FROM soft_router_socks_nodes
		WHERE id = $1 AND deleted_at IS NULL`,
		[]any{id},
		&node.ID, &node.AgentID, &node.OpenWrtID, &node.NodeKey, &node.Name, &node.OpenWrtPort,
		&node.HTTPPort, &node.NodeRef, &node.ListenStatus, &node.Enabled, &node.LastSeenAt,
		&node.CreatedAt, &node.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, service.ErrSoftRouterNodeNotFound
	}
	if err != nil {
		return nil, err
	}
	return &node, nil
}

func (r *softRouterRepository) ListMappings(ctx context.Context) ([]service.SoftRouterProxyMapping, error) {
	return r.listMappings(ctx, false)
}

func (r *softRouterRepository) ListEnabledMappings(ctx context.Context) ([]service.SoftRouterProxyMapping, error) {
	return r.listMappings(ctx, true)
}

func (r *softRouterRepository) listMappings(ctx context.Context, enabledOnly bool) ([]service.SoftRouterProxyMapping, error) {
	where := "deleted_at IS NULL"
	if enabledOnly {
		where += " AND enabled = TRUE"
	}
	rows, err := r.sql.QueryContext(ctx, `
		SELECT id, agent_id, node_id, name, openwrt_port, raw_remote_port, public_port,
		       username, password, enabled, proxy_id, status, last_error, last_test_at,
		       last_exit_ip, created_at, updated_at
		FROM soft_router_proxy_mappings
		WHERE `+where+`
		ORDER BY public_port ASC, id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []service.SoftRouterProxyMapping
	for rows.Next() {
		var item service.SoftRouterProxyMapping
		if err := scanSoftRouterMapping(rows, &item); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (r *softRouterRepository) GetMappingByID(ctx context.Context, id int64) (*service.SoftRouterProxyMapping, error) {
	mapping := service.SoftRouterProxyMapping{}
	err := scanSingleRow(ctx, r.sql, `
		SELECT id, agent_id, node_id, name, openwrt_port, raw_remote_port, public_port,
		       username, password, enabled, proxy_id, status, last_error, last_test_at,
		       last_exit_ip, created_at, updated_at
		FROM soft_router_proxy_mappings
		WHERE id = $1 AND deleted_at IS NULL`,
		[]any{id},
		&mapping.ID, &mapping.AgentID, &mapping.NodeID, &mapping.Name, &mapping.OpenWrtPort,
		&mapping.RawRemotePort, &mapping.PublicPort, &mapping.Username, &mapping.Password,
		&mapping.Enabled, &mapping.ProxyID, &mapping.Status, &mapping.LastError,
		&mapping.LastTestAt, &mapping.LastExitIP, &mapping.CreatedAt, &mapping.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, service.ErrSoftRouterMappingNotFound
	}
	if err != nil {
		return nil, err
	}
	return &mapping, nil
}

func (r *softRouterRepository) CreateMapping(ctx context.Context, mapping *service.SoftRouterProxyMapping) error {
	return scanSingleRow(ctx, r.sql, `
		INSERT INTO soft_router_proxy_mappings (
		    agent_id, node_id, name, openwrt_port, raw_remote_port, public_port,
		    username, password, enabled, proxy_id, status, last_error
		)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
		RETURNING id, agent_id, node_id, name, openwrt_port, raw_remote_port, public_port,
		          username, password, enabled, proxy_id, status, last_error, last_test_at,
		          last_exit_ip, created_at, updated_at`,
		[]any{
			mapping.AgentID, mapping.NodeID, mapping.Name, mapping.OpenWrtPort,
			mapping.RawRemotePort, mapping.PublicPort, mapping.Username, mapping.Password,
			mapping.Enabled, mapping.ProxyID, mapping.Status, mapping.LastError,
		},
		&mapping.ID, &mapping.AgentID, &mapping.NodeID, &mapping.Name, &mapping.OpenWrtPort,
		&mapping.RawRemotePort, &mapping.PublicPort, &mapping.Username, &mapping.Password,
		&mapping.Enabled, &mapping.ProxyID, &mapping.Status, &mapping.LastError,
		&mapping.LastTestAt, &mapping.LastExitIP, &mapping.CreatedAt, &mapping.UpdatedAt,
	)
}

func (r *softRouterRepository) UpdateMapping(ctx context.Context, mapping *service.SoftRouterProxyMapping) error {
	err := scanSingleRow(ctx, r.sql, `
		UPDATE soft_router_proxy_mappings SET
		    agent_id = $2,
		    node_id = $3,
		    name = $4,
		    openwrt_port = $5,
		    raw_remote_port = $6,
		    public_port = $7,
		    username = $8,
		    password = $9,
		    enabled = $10,
		    status = $11,
		    last_error = $12,
		    updated_at = NOW()
		WHERE id = $1 AND deleted_at IS NULL
		RETURNING id, agent_id, node_id, name, openwrt_port, raw_remote_port, public_port,
		          username, password, enabled, proxy_id, status, last_error, last_test_at,
		          last_exit_ip, created_at, updated_at`,
		[]any{
			mapping.ID, mapping.AgentID, mapping.NodeID, mapping.Name, mapping.OpenWrtPort,
			mapping.RawRemotePort, mapping.PublicPort, mapping.Username, mapping.Password,
			mapping.Enabled, mapping.Status, mapping.LastError,
		},
		&mapping.ID, &mapping.AgentID, &mapping.NodeID, &mapping.Name, &mapping.OpenWrtPort,
		&mapping.RawRemotePort, &mapping.PublicPort, &mapping.Username, &mapping.Password,
		&mapping.Enabled, &mapping.ProxyID, &mapping.Status, &mapping.LastError,
		&mapping.LastTestAt, &mapping.LastExitIP, &mapping.CreatedAt, &mapping.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return service.ErrSoftRouterMappingNotFound
	}
	return err
}

func (r *softRouterRepository) DeleteMapping(ctx context.Context, id int64) error {
	result, err := r.sql.ExecContext(ctx, `UPDATE soft_router_proxy_mappings SET deleted_at = NOW(), updated_at = NOW(), enabled = FALSE WHERE id = $1 AND deleted_at IS NULL`, id)
	return rowsAffectedOrNotFound(result, err, service.ErrSoftRouterMappingNotFound)
}

func (r *softRouterRepository) UpdateMappingProxy(ctx context.Context, id int64, proxyID *int64, status, lastError string) error {
	_, err := r.sql.ExecContext(ctx, `
		UPDATE soft_router_proxy_mappings
		SET proxy_id = $2, status = $3, last_error = $4, updated_at = NOW()
		WHERE id = $1 AND deleted_at IS NULL`,
		id, proxyID, status, lastError)
	return err
}

func scanSoftRouterAgent(scanner interface{ Scan(dest ...any) error }, item *service.SoftRouterAgent) error {
	return scanner.Scan(
		&item.ID, &item.Name, &item.Token, &item.Hostname, &item.Description, &item.Status,
		&item.LastSeenAt, &item.LastError, &item.CreatedAt, &item.UpdatedAt,
	)
}

func scanSoftRouterNode(scanner interface{ Scan(dest ...any) error }, item *service.SoftRouterSocksNode) error {
	return scanner.Scan(
		&item.ID, &item.AgentID, &item.OpenWrtID, &item.NodeKey, &item.Name, &item.OpenWrtPort,
		&item.HTTPPort, &item.NodeRef, &item.ListenStatus, &item.Enabled, &item.LastSeenAt,
		&item.CreatedAt, &item.UpdatedAt,
	)
}

func scanSoftRouterMapping(scanner interface{ Scan(dest ...any) error }, item *service.SoftRouterProxyMapping) error {
	return scanner.Scan(
		&item.ID, &item.AgentID, &item.NodeID, &item.Name, &item.OpenWrtPort,
		&item.RawRemotePort, &item.PublicPort, &item.Username, &item.Password,
		&item.Enabled, &item.ProxyID, &item.Status, &item.LastError,
		&item.LastTestAt, &item.LastExitIP, &item.CreatedAt, &item.UpdatedAt,
	)
}

func rowsAffectedOrNotFound(result sql.Result, err error, notFound error) error {
	if err != nil {
		return err
	}
	if result == nil {
		return nil
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return notFound
	}
	return nil
}

func strconvItoa(v int) string {
	if v == 0 {
		return "0"
	}
	negative := v < 0
	if negative {
		v = -v
	}
	buf := [20]byte{}
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	if negative {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
