package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const executionNodeBuiltinProxyNamePrefix = "XIASS 内置节点出口 - "

// ExecutionNodeBuiltinProxyNamePrefix identifies the private loopback egress
// created automatically for an execution node. Repositories use the same
// marker when distinguishing bootstrap runtime state from business proxies.
const ExecutionNodeBuiltinProxyNamePrefix = executionNodeBuiltinProxyNamePrefix

// InitializeExecutionNodeRuntime prepares the durable local egress record and
// launches the host-side controller that writes the deployment environment.
// Routing remains disabled until the administrator explicitly enables it from
// the existing panel.
func (s *SettingService) InitializeExecutionNodeRuntime(ctx context.Context, nodeID string) (*ExecutionNodeRuntimeConfig, error) {
	nodeID = strings.TrimSpace(nodeID)
	if !validExecutionNodeID(nodeID) {
		return nil, infraerrors.BadRequest("EXECUTION_NODE_RUNTIME_NODE_INVALID", "configure a valid local execution node ID")
	}
	if s == nil || s.settingRepo == nil || s.proxyRepo == nil {
		return nil, errors.New("execution-node runtime repositories are unavailable")
	}
	if s.executionNodeRuntimeInitializer == nil {
		return nil, infraerrors.BadRequest("EXECUTION_NODE_RUNTIME_APPLIER_UNAVAILABLE", "the deployment does not have a host runtime controller")
	}

	values, err := s.settingRepo.GetMultiple(ctx, []string{
		SettingKeyExecutionNodeWeights,
		SettingKeyExecutionNodeProxyIDs,
	})
	if err != nil {
		return nil, fmt.Errorf("read execution-node routing settings: %w", err)
	}

	tunnelToken := configuredExecutionNodeTunnelToken()
	if len(tunnelToken) != 64 {
		tunnelToken, err = newPairingRandomToken()
		if err != nil {
			return nil, fmt.Errorf("generate execution-node tunnel token: %w", err)
		}
	}
	proxy, tunnelToken, err := s.ensureExecutionNodeBuiltinProxy(ctx, nodeID, tunnelToken)
	if err != nil {
		return nil, err
	}

	proxyIDs := map[string]int64{}
	if raw := strings.TrimSpace(values[SettingKeyExecutionNodeProxyIDs]); raw != "" {
		proxyIDs, err = decodeExecutionNodeProxyIDs(raw)
		if err != nil {
			return nil, infraerrors.BadRequest("EXECUTION_NODE_PROXY_MAPPING_INVALID", "the existing node egress mapping is invalid; repair it before initializing this node")
		}
	}
	// The first machine prepared in an empty installation is the source node,
	// regardless of whether its browser-derived name happens to equal the
	// legacy placeholder "api". Joined peers receive their default later in the
	// source-authoritative pairing transaction.
	weights := map[string]float64{nodeID: 9}
	// A fresh installation may expose the legacy api/api2 defaults before any
	// execution-node runtime exists. Once an administrator prepares the first
	// real machine, replace those placeholders with its detected name. Existing
	// installations with a durable node-to-egress map keep their exact weights.
	if len(proxyIDs) > 0 {
		if raw := strings.TrimSpace(values[SettingKeyExecutionNodeWeights]); raw != "" {
			weights, err = decodeExecutionNodeWeights(raw)
			if err != nil {
				return nil, infraerrors.BadRequest("EXECUTION_NODE_WEIGHTS_INVALID", "the existing node weights are invalid; repair them before initializing this node")
			}
		}
	}
	if _, exists := weights[nodeID]; !exists {
		weights[nodeID] = 9
	}
	if old, exists := proxyIDs[nodeID]; exists && old != proxy.ID {
		return nil, infraerrors.Conflict("EXECUTION_NODE_RUNTIME_PROXY_CONFLICT", "this node ID is already mapped to a different fixed egress")
	}
	proxyIDs[nodeID] = proxy.ID
	encodedWeights, err := json.Marshal(weights)
	if err != nil {
		return nil, fmt.Errorf("encode execution-node weights: %w", err)
	}
	encodedProxyIDs, err := json.Marshal(proxyIDs)
	if err != nil {
		return nil, fmt.Errorf("encode execution-node egress mapping: %w", err)
	}
	if err := s.settingRepo.SetMultiple(ctx, map[string]string{
		SettingKeyExecutionNodeWeights:  string(encodedWeights),
		SettingKeyExecutionNodeProxyIDs: string(encodedProxyIDs),
	}); err != nil {
		return nil, fmt.Errorf("persist execution-node runtime mapping: %w", err)
	}

	legacyNodeID := nodeID
	legacyProxyID := proxy.ID
	if s.cfg != nil && s.cfg.Gateway.ExecutionNode.Enabled {
		if validExecutionNodeID(s.cfg.Gateway.ExecutionNode.LegacyUnassignedNodeID) {
			legacyNodeID = strings.TrimSpace(s.cfg.Gateway.ExecutionNode.LegacyUnassignedNodeID)
		}
		if s.cfg.Gateway.ExecutionNode.LegacyUnassignedProxyID > 0 {
			legacyProxyID = s.cfg.Gateway.ExecutionNode.LegacyUnassignedProxyID
		}
	}
	runtime := ExecutionNodeRuntimeConfig{
		NodeID: nodeID, TunnelToken: tunnelToken, DefaultProxyID: proxy.ID,
		LegacyUnassignedNodeID: legacyNodeID, LegacyUnassignedProxyID: legacyProxyID,
	}
	if err := s.executionNodeRuntimeInitializer.LaunchExecutionNodeRuntime(ctx, runtime); err != nil {
		return nil, infraerrors.BadRequest("EXECUTION_NODE_RUNTIME_APPLY_FAILED", "the host could not apply the local node runtime: "+err.Error())
	}
	return &runtime, nil
}

func (s *SettingService) ensureExecutionNodeBuiltinProxy(ctx context.Context, nodeID, token string) (*Proxy, string, error) {
	name := executionNodeBuiltinProxyNamePrefix + nodeID
	proxies, err := s.proxyRepo.ListAllForFallback(ctx)
	if err != nil {
		return nil, "", fmt.Errorf("list execution-node egress proxies: %w", err)
	}
	for i := range proxies {
		candidate := &proxies[i]
		if candidate.Name != name || candidate.Protocol != "socks5" || candidate.Host != "127.0.0.1" || candidate.Port != 19080 || candidate.Username != nodeID {
			continue
		}
		if len(strings.TrimSpace(candidate.Password)) == 64 {
			token = strings.TrimSpace(candidate.Password)
		}
		candidate.Status = StatusActive
		candidate.ExpiresAt = nil
		candidate.Password = token
		if err := s.proxyRepo.Update(ctx, candidate); err != nil {
			return nil, "", fmt.Errorf("repair execution-node egress proxy: %w", err)
		}
		return candidate, token, nil
	}
	proxy := &Proxy{
		Name: name, Protocol: "socks5", Host: "127.0.0.1", Port: 19080,
		Username: nodeID, Password: token, Status: StatusActive,
	}
	if err := s.proxyRepo.Create(ctx, proxy); err != nil {
		return nil, "", fmt.Errorf("create execution-node egress proxy: %w", err)
	}
	return proxy, token, nil
}

// ResolveExecutionNodePeerURL lets the early-started tunnel discover a peer
// after the application has loaded the shared settings repository.
func (s *SettingService) ResolveExecutionNodePeerURL(ctx context.Context, nodeID string) (*url.URL, error) {
	key := executionNodePairingPeerKey(strings.TrimSpace(s.localExecutionNodeID()))
	if key == "" || s == nil || s.settingRepo == nil {
		return nil, errors.New("execution-node peer is unavailable")
	}
	raw, err := s.settingRepo.GetValue(ctx, key)
	if err != nil {
		return nil, err
	}
	var peer ExecutionNodePairingPeer
	if err := json.Unmarshal([]byte(raw), &peer); err != nil || peer.NodeID != strings.TrimSpace(nodeID) {
		return nil, errors.New("execution-node peer is unavailable")
	}
	if strings.TrimSpace(peer.PeerURL) == "" {
		return nil, errors.New("execution-node peer URL is missing")
	}
	return normalizeExecutionNodePeerURL(peer.PeerURL)
}
