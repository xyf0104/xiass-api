//go:build unit

package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type executionNodeAdminProxyRepo struct {
	ProxyRepository
	proxies map[int64]Proxy
}

func (r *executionNodeAdminProxyRepo) ListByIDs(_ context.Context, ids []int64) ([]Proxy, error) {
	result := make([]Proxy, 0, len(ids))
	for _, id := range ids {
		if proxy, ok := r.proxies[id]; ok {
			result = append(result, proxy)
		}
	}
	return result, nil
}

type executionNodeAdminHealthReader map[string]bool

func (r executionNodeAdminHealthReader) HealthyExecutionNodes(_ context.Context, nodeIDs []string) (map[string]bool, error) {
	result := make(map[string]bool, len(nodeIDs))
	for _, nodeID := range nodeIDs {
		result[nodeID] = r[nodeID]
	}
	return result, nil
}

type executionNodeAdminStatsReader map[string]ExecutionNodeAccountStats

func (r executionNodeAdminStatsReader) GetExecutionNodeAccountStats(_ context.Context, _ string) (map[string]ExecutionNodeAccountStats, error) {
	return r, nil
}

func executionNodeAdminTestConfig() *config.Config {
	cfg := &config.Config{}
	cfg.Gateway.ExecutionNode = config.GatewayExecutionNodeConfig{
		Enabled:                 true,
		ID:                      "api",
		DefaultProxyID:          84,
		LegacyUnassignedNodeID:  "api",
		LegacyUnassignedProxyID: 84,
	}
	return cfg
}

func executionNodeAdminProxyMap() *executionNodeAdminProxyRepo {
	return &executionNodeAdminProxyRepo{proxies: map[int64]Proxy{
		84: {ID: 84, Name: "US egress", Status: StatusActive},
		83: {ID: 83, Name: "JP egress", Status: StatusActive},
	}}
}

func TestGetExecutionNodeAdminStatusReportsRuntimeAndAccountPool(t *testing.T) {
	repo := &executionNodeSettingRepo{values: map[string]string{
		SettingKeyExecutionNodeBalancingEnabled: "true",
		SettingKeyExecutionNodeWeights:          `{"api":3,"api2":1}`,
		SettingKeyExecutionNodeProxyIDs:         `{"api":84,"api2":83}`,
		"execution_node_emergency_egress:api":   "invalid-retired-setting",
	}}
	svc := NewSettingService(repo, executionNodeAdminTestConfig())
	svc.SetProxyRepository(executionNodeAdminProxyMap())
	svc.SetExecutionNodeHealthReader(executionNodeAdminHealthReader{"api": true, "api2": true})
	svc.SetExecutionNodeAccountStatsReader(executionNodeAdminStatsReader{
		"api":  {Total: 5, Active: 4, Schedulable: 3},
		"api2": {Total: 2, Active: 2, Schedulable: 1},
	})

	status, err := svc.GetExecutionNodeAdminStatus(context.Background())
	require.NoError(t, err)
	require.True(t, status.DatabaseReachable)
	require.True(t, status.HeartbeatStoreReachable)
	require.True(t, status.BalancingEnabled)
	require.True(t, status.CanEnable)
	require.Len(t, status.Nodes, 2)
	raw, err := json.Marshal(status)
	require.NoError(t, err)
	require.NotContains(t, string(raw), `"failover"`)
	require.NotContains(t, string(raw), `"emergency_local_egress"`)
	require.Equal(t, "api", status.Nodes[0].NodeID)
	require.Equal(t, int64(5), status.Nodes[0].AccountStats.Total)
	require.True(t, status.Nodes[0].ProxyValid)
	require.True(t, status.Nodes[0].Online)
	require.Equal(t, "api2", status.Nodes[1].NodeID)
	require.Equal(t, int64(2), status.Nodes[1].AccountStats.Total)
}

func TestGetExecutionNodeAdminStatusSurfacesOfflineNodeAsWarning(t *testing.T) {
	repo := &executionNodeSettingRepo{values: map[string]string{
		SettingKeyExecutionNodeBalancingEnabled: "true",
		SettingKeyExecutionNodeWeights:          `{"api":1,"api2":1}`,
		SettingKeyExecutionNodeProxyIDs:         `{"api":84,"api2":83}`,
	}}
	svc := NewSettingService(repo, executionNodeAdminTestConfig())
	svc.SetProxyRepository(executionNodeAdminProxyMap())
	svc.SetExecutionNodeHealthReader(executionNodeAdminHealthReader{"api": true, "api2": false})

	status, err := svc.GetExecutionNodeAdminStatus(context.Background())
	require.NoError(t, err)
	require.True(t, status.CanEnable, "an offline peer is a warning; it must not make the switch look ready when configuration is otherwise valid")
	require.Contains(t, status.Issues, ExecutionNodeAdminIssue{Code: "NODE_OFFLINE", Severity: "warning", Message: "execution node api2 heartbeat is offline"})
}

func TestGetExecutionNodeAdminStatusRejectsMissingEgressMapping(t *testing.T) {
	repo := &executionNodeSettingRepo{values: map[string]string{
		SettingKeyExecutionNodeBalancingEnabled: "false",
		SettingKeyExecutionNodeWeights:          `{"api":1,"api2":1}`,
		SettingKeyExecutionNodeProxyIDs:         `{"api":84}`,
	}}
	svc := NewSettingService(repo, executionNodeAdminTestConfig())
	svc.SetProxyRepository(executionNodeAdminProxyMap())
	svc.SetExecutionNodeHealthReader(executionNodeAdminHealthReader{"api": true, "api2": true})

	status, err := svc.GetExecutionNodeAdminStatus(context.Background())
	require.NoError(t, err)
	require.False(t, status.CanEnable)
	require.Contains(t, status.Issues[0].Message, "execution node api2 must have a private egress proxy ID")
	require.Equal(t, "PROXY_MAPPING_INVALID", status.Issues[0].Code)
	require.Equal(t, "error", status.Issues[0].Severity)
}

func TestGetExecutionNodeAdminStatusKeepsDefaultSingleNodeModeNeutral(t *testing.T) {
	cfg := &config.Config{}
	svc := NewSettingService(&executionNodeSettingRepo{values: map[string]string{}}, cfg)

	status, err := svc.GetExecutionNodeAdminStatus(context.Background())
	require.NoError(t, err)
	require.False(t, status.BalancingEnabled)
	require.False(t, status.CanEnable)
	require.Contains(t, status.Issues, ExecutionNodeAdminIssue{
		Code: "MULTI_NODE_DISABLED", Severity: "info",
		Message: "multi-node routing is not enabled; the current single-node runtime is operating normally",
	})
	for _, issue := range status.Issues {
		require.NotEqual(t, "error", issue.Severity)
	}
	require.NotNil(t, status.Nodes)
	require.Empty(t, status.Nodes)
}

func TestGetExecutionNodeAdminStatusKeepsPanelVisibleWhenSettingsDatabaseFails(t *testing.T) {
	cfg := executionNodeAdminTestConfig()
	svc := NewSettingService(&executionNodeSettingRepo{err: errors.New("database unavailable")}, cfg)

	status, err := svc.GetExecutionNodeAdminStatus(context.Background())
	require.NoError(t, err)
	require.False(t, status.DatabaseReachable)
	require.False(t, status.BalancingEnabled)
	require.Contains(t, status.Issues, ExecutionNodeAdminIssue{Code: "DATABASE_UNAVAILABLE", Severity: "error", Message: "read execution node settings: database unavailable"})
}
