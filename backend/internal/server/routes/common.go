package routes

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/lib/pq"
	"github.com/redis/go-redis/v9"
)

const readinessProbeTimeout = 1500 * time.Millisecond

// Keep readiness validation aligned with the execution-node scheduler. A
// load-balancer probe that accepts a policy the scheduler rejects would retain
// an origin that can only return unavailable-account responses.
const (
	readinessMaxExecutionNodeCount  = 32
	readinessMaxExecutionNodeWeight = 1_000_000.0
)

type readinessProbe struct {
	name string
	run  func(context.Context) error
}

type readinessProbeResult struct {
	name string
	err  error
}

// RegisterCommonRoutes 注册通用路由（健康检查、状态等）
func RegisterCommonRoutes(r *gin.Engine, db *sql.DB, redisClient *redis.Client, cfg *config.Config) {
	// 健康检查
	r.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	nodeID := ""
	if cfg != nil && cfg.Gateway.ExecutionNode.Enabled {
		nodeID = strings.TrimSpace(cfg.Gateway.ExecutionNode.ID)
	}
	checkPrimary := cfg != nil && cfg.Gateway.ExecutionNode.Enabled
	registerReadinessRoute(r, []readinessProbe{
		{
			name: "postgres",
			run: func(ctx context.Context) error {
				if db == nil {
					return errors.New("database is not configured")
				}
				if !checkPrimary {
					return db.PingContext(ctx)
				}
				return checkWritablePostgres(ctx, db)
			},
		},
		{
			name: "redis",
			run: func(ctx context.Context) error {
				if redisClient == nil {
					return errors.New("redis is not configured")
				}
				if !checkPrimary {
					return redisClient.Ping(ctx).Err()
				}
				return checkWritableRedis(ctx, redisClient)
			},
		},
		{
			name: "execution_node",
			run:  func(ctx context.Context) error { return checkExecutionNodeReadiness(ctx, db, redisClient, cfg) },
		},
	}, nodeID, readinessProbeTimeout)

	// Claude Code 遥测日志（忽略，直接返回200）
	r.POST("/api/event_logging/batch", func(c *gin.Context) {
		c.Status(http.StatusOK)
	})

	// Setup status endpoint (always returns needs_setup: false in normal mode)
	// This is used by the frontend to detect when the service has restarted after setup
	r.GET("/setup/status", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"code": 0,
			"data": gin.H{
				"needs_setup": false,
				"step":        "completed",
			},
		})
	})
}

// A reachable replica cannot serve billing or session writes. These probes
// reject standby connections; they do not replace external primary fencing.
func checkWritablePostgres(ctx context.Context, db *sql.DB) error {
	var writable bool
	err := db.QueryRowContext(ctx, `
		SELECT NOT pg_is_in_recovery() AND current_setting('transaction_read_only') = 'off'
	`).Scan(&writable)
	if err != nil {
		return err
	}
	if !writable {
		return errors.New("database is read-only")
	}
	return nil
}

func checkWritableRedis(ctx context.Context, client *redis.Client) error {
	role, err := client.Do(ctx, "ROLE").Slice()
	if err != nil {
		return err
	}
	if len(role) == 0 || role[0] != "master" {
		return errors.New("redis is not a writable primary")
	}
	return nil
}

func checkExecutionNodeReadiness(ctx context.Context, db *sql.DB, redisClient *redis.Client, cfg *config.Config) error {
	if db == nil {
		return errors.New("database is not configured")
	}
	var enabled string
	err := db.QueryRowContext(ctx, `
		SELECT value FROM settings WHERE key = 'execution_node_balancing_enabled'
	`).Scan(&enabled)
	if errors.Is(err, sql.ErrNoRows) {
		// Single-node deployments and instances waiting for activation do not
		// require local execution-node settings.
		return nil
	}
	if err != nil {
		return err
	}
	if !strings.EqualFold(strings.TrimSpace(enabled), "true") {
		return nil
	}
	if cfg == nil || !cfg.Gateway.ExecutionNode.Enabled {
		return errors.New("local execution node configuration is disabled")
	}
	node := cfg.Gateway.ExecutionNode
	if !validExecutionNodeID(node.ID) || node.DefaultProxyID <= 0 {
		return errors.New("local execution node identity or proxy is invalid")
	}
	var weightsRaw, proxyIDsRaw string
	rows, err := db.QueryContext(ctx, `
		SELECT key, value FROM settings
		WHERE key IN ('execution_node_weights', 'execution_node_proxy_ids')
	`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			_ = rows.Close()
			return err
		}
		switch key {
		case "execution_node_weights":
			weightsRaw = value
		case "execution_node_proxy_ids":
			proxyIDsRaw = value
		}
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return err
	}
	_ = rows.Close()

	var weights map[string]float64
	if err := json.Unmarshal([]byte(weightsRaw), &weights); err != nil {
		return errors.New("execution node weights are unavailable")
	}
	if len(weights) == 0 || len(weights) > readinessMaxExecutionNodeCount {
		return errors.New("execution node weights are unavailable")
	}
	normalizedWeights := make(map[string]float64, len(weights))
	hasPositiveWeight := false
	for rawNodeID, weight := range weights {
		nodeID := strings.TrimSpace(rawNodeID)
		if !validExecutionNodeID(nodeID) || math.IsNaN(weight) || math.IsInf(weight, 0) || weight < 0 || weight > readinessMaxExecutionNodeWeight {
			return errors.New("execution node weights are invalid")
		}
		if _, exists := normalizedWeights[nodeID]; exists {
			return errors.New("execution node weights contain duplicate node IDs")
		}
		normalizedWeights[nodeID] = weight
		hasPositiveWeight = hasPositiveWeight || weight > 0
	}
	if !hasPositiveWeight {
		return errors.New("execution node weights have no active node")
	}
	localNodeID := strings.TrimSpace(node.ID)
	if _, exists := normalizedWeights[localNodeID]; !exists {
		return errors.New("local execution node is not present in the shared policy")
	}
	var proxyIDs map[string]int64
	if err := json.Unmarshal([]byte(proxyIDsRaw), &proxyIDs); err != nil || len(proxyIDs) == 0 || len(proxyIDs) > readinessMaxExecutionNodeCount {
		return errors.New("local execution node proxy mapping is unavailable")
	}
	normalizedProxyIDs := make(map[string]int64, len(proxyIDs))
	seenProxyIDs := make(map[int64]string, len(proxyIDs))
	for rawNodeID, proxyID := range proxyIDs {
		nodeID := strings.TrimSpace(rawNodeID)
		if !validExecutionNodeID(nodeID) || proxyID <= 0 {
			return errors.New("execution node proxy mapping is invalid")
		}
		if _, exists := normalizedProxyIDs[nodeID]; exists {
			return errors.New("execution node proxy mapping contains duplicate node IDs")
		}
		if _, exists := normalizedWeights[nodeID]; !exists {
			return errors.New("execution node proxy mapping contains unknown node")
		}
		if previousNodeID, exists := seenProxyIDs[proxyID]; exists {
			return fmt.Errorf("proxy %d is assigned to both execution nodes %s and %s", proxyID, previousNodeID, nodeID)
		}
		normalizedProxyIDs[nodeID] = proxyID
		seenProxyIDs[proxyID] = nodeID
	}
	for nodeID := range normalizedWeights {
		if _, exists := normalizedProxyIDs[nodeID]; !exists {
			return fmt.Errorf("execution node %s has no proxy mapping", strings.TrimSpace(nodeID))
		}
	}
	if normalizedProxyIDs[localNodeID] != node.DefaultProxyID {
		return errors.New("local execution node proxy mapping is unavailable")
	}
	legacyNodeID := strings.TrimSpace(node.LegacyUnassignedNodeID)
	if legacyNodeID == "" {
		legacyNodeID = "api"
	}
	if !validExecutionNodeID(legacyNodeID) || node.LegacyUnassignedProxyID <= 0 {
		return errors.New("legacy execution node configuration is invalid")
	}
	if _, exists := normalizedWeights[legacyNodeID]; !exists || normalizedProxyIDs[legacyNodeID] != node.LegacyUnassignedProxyID {
		return errors.New("legacy execution node mapping is unavailable")
	}

	proxyIDValues := make([]int64, 0, len(normalizedProxyIDs))
	for _, proxyID := range normalizedProxyIDs {
		proxyIDValues = append(proxyIDValues, proxyID)
	}
	sort.Slice(proxyIDValues, func(i, j int) bool { return proxyIDValues[i] < proxyIDValues[j] })
	rows, err = db.QueryContext(ctx, `
		SELECT id
		FROM proxies
		WHERE id = ANY($1::bigint[])
			AND deleted_at IS NULL
			AND status = 'active'
			AND (expires_at IS NULL OR expires_at > NOW())
	`, pq.Array(proxyIDValues))
	if err != nil {
		return err
	}
	activeProxyIDs := make(map[int64]bool, len(proxyIDValues))
	for rows.Next() {
		var proxyID int64
		if err := rows.Scan(&proxyID); err != nil {
			_ = rows.Close()
			return err
		}
		activeProxyIDs[proxyID] = true
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return err
	}
	_ = rows.Close()
	if len(activeProxyIDs) == 0 {
		return errors.New("no active execution node proxy is available")
	}

	// The process answering this probe is itself alive, so its own node does not
	// need to wait for the asynchronous heartbeat SET. Remote owners must have a
	// live shared lease before this ingress advertises them as executable.
	if normalizedWeights[localNodeID] > 0 && activeProxyIDs[normalizedProxyIDs[localNodeID]] {
		return nil
	}
	remoteNodeIDs := make([]string, 0, len(normalizedWeights))
	for nodeID, weight := range normalizedWeights {
		if nodeID != localNodeID && weight > 0 && activeProxyIDs[normalizedProxyIDs[nodeID]] {
			remoteNodeIDs = append(remoteNodeIDs, nodeID)
		}
	}
	if len(remoteNodeIDs) > 0 {
		if redisClient == nil {
			return errors.New("execution node heartbeat store is unavailable")
		}
		sort.Strings(remoteNodeIDs)
		keys := make([]string, len(remoteNodeIDs))
		for i, nodeID := range remoteNodeIDs {
			keys[i] = service.ExecutionNodeHeartbeatKey(nodeID)
		}
		values, err := redisClient.MGet(ctx, keys...).Result()
		if err != nil {
			return err
		}
		for _, value := range values {
			if value != nil && strings.TrimSpace(fmt.Sprint(value)) != "" {
				return nil
			}
		}
	}

	// A drained local node may remain a useful ingress while the selected owner
	// is offline, but only when emergency takeover is explicitly enabled and the
	// local private egress itself is healthy.
	if node.EmergencyLocalEgress && activeProxyIDs[node.DefaultProxyID] {
		return nil
	}
	return errors.New("no live execution node is available")
}

func validExecutionNodeID(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 64 {
		return false
	}
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') || r == '.' || r == '_' || r == '-' {
			continue
		}
		return false
	}
	return true
}

func registerReadinessRoute(r *gin.Engine, probes []readinessProbe, nodeID string, timeout time.Duration) {
	r.GET("/readyz", func(c *gin.Context) {
		if timeout <= 0 {
			timeout = readinessProbeTimeout
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), timeout)
		defer cancel()

		results := make(chan readinessProbeResult, len(probes))
		for _, probe := range probes {
			probe := probe
			go func() {
				results <- readinessProbeResult{name: probe.name, err: probe.run(ctx)}
			}()
		}

		checks := make(map[string]string, len(probes))
		ready := true
		remaining := len(probes)
		for remaining > 0 {
			select {
			case result := <-results:
				remaining--
				if result.err != nil {
					checks[result.name] = "unavailable"
					ready = false
					continue
				}
				checks[result.name] = "ok"
			case <-ctx.Done():
				// A dependency probe that ignores cancellation must not keep a
				// load-balancer health request hanging indefinitely.
				for _, probe := range probes {
					if _, seen := checks[probe.name]; !seen {
						checks[probe.name] = "unavailable"
					}
				}
				ready = false
				remaining = 0
			}
		}

		c.Header("Cache-Control", "no-store")
		if nodeID != "" {
			c.Header("X-XIASS-Execution-Node", nodeID)
		}
		payload := gin.H{
			"status": "ok",
			"checks": checks,
		}
		if nodeID != "" {
			payload["execution_node"] = nodeID
		}
		if !ready {
			payload["status"] = "unavailable"
			c.JSON(http.StatusServiceUnavailable, payload)
			return
		}
		c.JSON(http.StatusOK, payload)
	})
}
