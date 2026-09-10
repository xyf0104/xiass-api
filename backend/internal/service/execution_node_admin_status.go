package service

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
)

type ExecutionNodeAccountStats struct {
	Total       int64 `json:"total"`
	Active      int64 `json:"active"`
	Schedulable int64 `json:"schedulable"`
}

type ExecutionNodeAccountStatsReader interface {
	GetExecutionNodeAccountStats(ctx context.Context, legacyNodeID string) (map[string]ExecutionNodeAccountStats, error)
}

type ExecutionNodeAdminIssue struct {
	Code     string `json:"code"`
	Severity string `json:"severity"`
	Message  string `json:"message"`
}

type ExecutionNodeRuntimeStatus struct {
	Enabled                 bool   `json:"enabled"`
	NodeID                  string `json:"node_id"`
	DefaultProxyID          int64  `json:"default_proxy_id"`
	ControlPlane            bool   `json:"control_plane"`
	LegacyUnassignedNodeID  string `json:"legacy_unassigned_node_id"`
	LegacyUnassignedProxyID int64  `json:"legacy_unassigned_proxy_id"`
}

type ExecutionNodeAdminNode struct {
	NodeID       string                    `json:"node_id"`
	Weight       float64                   `json:"weight"`
	ProxyID      int64                     `json:"proxy_id"`
	ProxyName    string                    `json:"proxy_name,omitempty"`
	ProxyStatus  string                    `json:"proxy_status,omitempty"`
	ProxyValid   bool                      `json:"proxy_valid"`
	Online       bool                      `json:"online"`
	IsLocal      bool                      `json:"is_local"`
	AccountStats ExecutionNodeAccountStats `json:"account_stats"`
}

type ExecutionNodeAdminStatus struct {
	BalancingEnabled        bool                       `json:"balancing_enabled"`
	CanEnable               bool                       `json:"can_enable"`
	AdminWriteAllowed       bool                       `json:"admin_write_allowed"`
	AdminWriteMode          string                     `json:"admin_write_mode"`
	DatabaseReachable       bool                       `json:"database_reachable"`
	HeartbeatStoreReachable bool                       `json:"heartbeat_store_reachable"`
	Runtime                 ExecutionNodeRuntimeStatus `json:"runtime"`
	Nodes                   []ExecutionNodeAdminNode   `json:"nodes"`
	Issues                  []ExecutionNodeAdminIssue  `json:"issues"`
}

func (s *SettingService) GetExecutionNodeAdminStatus(ctx context.Context) (*ExecutionNodeAdminStatus, error) {
	status := &ExecutionNodeAdminStatus{
		DatabaseReachable: true,
		AdminWriteAllowed: true,
		AdminWriteMode:    "single_node",
		Nodes:             make([]ExecutionNodeAdminNode, 0),
		Issues:            make([]ExecutionNodeAdminIssue, 0),
	}
	if s != nil && s.cfg != nil {
		cfg := s.cfg.Gateway.ExecutionNode
		status.Runtime = ExecutionNodeRuntimeStatus{
			Enabled:                 cfg.Enabled,
			NodeID:                  strings.TrimSpace(cfg.ID),
			DefaultProxyID:          cfg.DefaultProxyID,
			ControlPlane:            cfg.ControlPlane,
			LegacyUnassignedNodeID:  strings.TrimSpace(cfg.LegacyUnassignedNodeID),
			LegacyUnassignedProxyID: cfg.LegacyUnassignedProxyID,
		}
		status.AdminWriteAllowed, status.AdminWriteMode = s.ExecutionNodeAdminWriteAccess(ctx)
	}
	addIssue := func(code, severity, message string) {
		status.Issues = append(status.Issues, ExecutionNodeAdminIssue{Code: code, Severity: severity, Message: message})
	}

	// The control panel must remain useful when an unrelated system setting is
	// malformed. Read only the three routing keys instead of loading the entire
	// settings document used by the general settings page.
	values := map[string]string{}
	settingKeys := []string{
		SettingKeyExecutionNodeBalancingEnabled,
		SettingKeyExecutionNodeWeights,
		SettingKeyExecutionNodeProxyIDs,
	}
	if s == nil || s.settingRepo == nil {
		status.DatabaseReachable = false
		addIssue("DATABASE_UNAVAILABLE", "error", "the settings repository is unavailable")
	} else if loaded, err := s.settingRepo.GetMultiple(ctx, settingKeys); err != nil {
		status.DatabaseReachable = false
		addIssue("DATABASE_UNAVAILABLE", "error", fmt.Sprintf("read execution node settings: %v", err))
	} else {
		values = loaded
	}
	status.BalancingEnabled = strings.EqualFold(strings.TrimSpace(values[SettingKeyExecutionNodeBalancingEnabled]), "true")
	// A regular single-node installation intentionally has no execution-node
	// identity, private egress mapping, heartbeat, or pairing state. Keep that
	// default mode healthy and quiet; these checks become blocking only when an
	// administrator has opted into the multi-node runtime or enabled routing.
	if !status.Runtime.Enabled && !status.BalancingEnabled {
		addIssue("MULTI_NODE_DISABLED", "info", "multi-node routing is not enabled; the current single-node runtime is operating normally")
		status.CanEnable = false
		return status, nil
	}

	weights := defaultExecutionNodeWeights()
	if raw := strings.TrimSpace(values[SettingKeyExecutionNodeWeights]); raw != "" {
		parsed, err := decodeExecutionNodeWeights(raw)
		if err != nil {
			addIssue("SHARED_WEIGHTS_INVALID", "error", err.Error())
		} else {
			weights = parsed
		}
	}

	proxyIDs := map[string]int64{}
	if raw := strings.TrimSpace(values[SettingKeyExecutionNodeProxyIDs]); raw != "" {
		parsed, err := decodeExecutionNodeProxyIDs(raw)
		if err != nil {
			addIssue("SHARED_PROXY_MAPPING_INVALID", "error", err.Error())
		} else {
			proxyIDs = parsed
		}
	}
	nodeSet := make(map[string]struct{}, len(weights)+len(proxyIDs)+2)
	for nodeID := range weights {
		nodeSet[nodeID] = struct{}{}
	}
	for nodeID := range proxyIDs {
		nodeSet[nodeID] = struct{}{}
	}
	if validExecutionNodeID(status.Runtime.NodeID) {
		nodeSet[status.Runtime.NodeID] = struct{}{}
	}
	if validExecutionNodeID(status.Runtime.LegacyUnassignedNodeID) {
		nodeSet[status.Runtime.LegacyUnassignedNodeID] = struct{}{}
	}
	nodeIDs := make([]string, 0, len(nodeSet))
	for nodeID := range nodeSet {
		nodeIDs = append(nodeIDs, nodeID)
	}
	sort.Strings(nodeIDs)

	healthy := make(map[string]bool, len(nodeIDs))
	if status.Runtime.Enabled && s != nil && s.executionNodeHealthReader != nil && len(nodeIDs) > 0 {
		if values, healthErr := s.executionNodeHealthReader.HealthyExecutionNodes(ctx, nodeIDs); healthErr != nil {
			addIssue("HEARTBEAT_STORE_UNAVAILABLE", "error", healthErr.Error())
		} else {
			status.HeartbeatStoreReachable = true
			healthy = values
		}
	}
	if status.Runtime.Enabled && validExecutionNodeID(status.Runtime.NodeID) {
		healthy[status.Runtime.NodeID] = true
	}

	proxyByID := make(map[int64]Proxy, len(proxyIDs))
	uniqueProxyIDs := make([]int64, 0, len(proxyIDs))
	seenProxyIDs := make(map[int64]struct{}, len(proxyIDs))
	for _, proxyID := range proxyIDs {
		if proxyID <= 0 {
			continue
		}
		if _, exists := seenProxyIDs[proxyID]; exists {
			continue
		}
		seenProxyIDs[proxyID] = struct{}{}
		uniqueProxyIDs = append(uniqueProxyIDs, proxyID)
	}
	if len(uniqueProxyIDs) > 0 && s != nil && s.proxyRepo != nil {
		proxies, proxyErr := s.proxyRepo.ListByIDs(ctx, uniqueProxyIDs)
		if proxyErr != nil {
			addIssue("PROXY_STATUS_UNAVAILABLE", "error", proxyErr.Error())
		} else {
			for _, proxy := range proxies {
				proxyByID[proxy.ID] = proxy
			}
		}
	}

	accountStats := make(map[string]ExecutionNodeAccountStats)
	if s != nil && s.executionNodeAccountStats != nil {
		values, statsErr := s.executionNodeAccountStats.GetExecutionNodeAccountStats(ctx, status.Runtime.LegacyUnassignedNodeID)
		if statsErr != nil {
			addIssue("ACCOUNT_STATS_UNAVAILABLE", "warning", statsErr.Error())
		} else {
			accountStats = values
			for nodeID := range values {
				if _, exists := nodeSet[nodeID]; !exists && validExecutionNodeID(nodeID) {
					nodeIDs = append(nodeIDs, nodeID)
				}
			}
			sort.Strings(nodeIDs)
		}
	}

	positiveWeights := 0
	for _, nodeID := range nodeIDs {
		weight := weights[nodeID]
		if weight > 0 {
			positiveWeights++
		}
		proxyID := proxyIDs[nodeID]
		proxy, proxyFound := proxyByID[proxyID]
		proxyValid := proxyFound && proxy.IsActive() && !proxy.IsExpired(time.Now())
		status.Nodes = append(status.Nodes, ExecutionNodeAdminNode{
			NodeID:       nodeID,
			Weight:       weight,
			ProxyID:      proxyID,
			ProxyName:    proxy.Name,
			ProxyStatus:  proxy.Status,
			ProxyValid:   proxyValid,
			Online:       healthy[nodeID],
			IsLocal:      nodeID == status.Runtime.NodeID,
			AccountStats: accountStats[nodeID],
		})
	}

	addError := func(code, message string) { addIssue(code, "error", message) }
	if !status.Runtime.Enabled {
		addError("LOCAL_RUNTIME_DISABLED", "gateway.execution_node is disabled on this instance")
	}
	if !validExecutionNodeID(status.Runtime.NodeID) {
		addError("LOCAL_NODE_ID_INVALID", "the local execution node ID is missing or invalid")
	}
	if status.Runtime.DefaultProxyID <= 0 {
		addError("LOCAL_DEFAULT_PROXY_MISSING", "the local execution node default proxy is not configured")
	}
	if status.Runtime.Enabled {
		if _, exists := weights[status.Runtime.NodeID]; !exists {
			addError("LOCAL_WEIGHT_MISSING", "the shared node weights do not contain this instance")
		}
		if proxyIDs[status.Runtime.NodeID] != status.Runtime.DefaultProxyID {
			addError("LOCAL_PROXY_MISMATCH", "the shared proxy mapping does not match this instance default proxy")
		}
	}
	if !validExecutionNodeID(status.Runtime.LegacyUnassignedNodeID) || status.Runtime.LegacyUnassignedProxyID <= 0 {
		addError("LEGACY_OWNER_INVALID", "the legacy account owner and proxy must be configured")
	} else if proxyIDs[status.Runtime.LegacyUnassignedNodeID] != status.Runtime.LegacyUnassignedProxyID {
		addError("LEGACY_PROXY_MISMATCH", "the legacy account proxy mapping does not match the deployment configuration")
	}
	if len(weights) == 0 || positiveWeights == 0 {
		addError("POSITIVE_WEIGHT_MISSING", "at least one execution node must have a positive weight")
	}
	if err := validateExecutionNodeProxyIDs(proxyIDs, weights); err != nil {
		addError("PROXY_MAPPING_INVALID", err.Error())
	}
	for _, node := range status.Nodes {
		if node.ProxyID <= 0 || !node.ProxyValid {
			addError("NODE_PROXY_UNAVAILABLE", "execution node "+node.NodeID+" does not have an active mapped proxy")
		}
		if status.BalancingEnabled && node.Weight > 0 && !node.Online {
			addIssue("NODE_OFFLINE", "warning", "execution node "+node.NodeID+" heartbeat is offline")
		}
	}
	if len(nodeIDs) > 1 && s != nil && s.executionNodePairingState != nil {
		pairing, pairingErr := s.GetExecutionNodePairingStatus(ctx)
		if pairingErr != nil || pairing == nil || !pairing.ProductionReady {
			addError("PAIRING_NOT_READY", "the execution nodes have not completed production-ready pairing")
		}
	}
	status.CanEnable = true
	for _, issue := range status.Issues {
		if issue.Severity == "error" {
			status.CanEnable = false
			break
		}
	}
	return status, nil
}
