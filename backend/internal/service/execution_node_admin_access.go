package service

import (
	"context"
	"errors"
	"strings"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

// pairedAdminAccess validates shared state, not peer liveness or egress authority.
// Missing pairing records retain legacy behavior, including initial pairing.
func (s *SettingService) pairedAdminAccess(ctx context.Context) (bool, error) {
	if s == nil || s.cfg == nil || s.settingRepo == nil {
		return false, nil
	}
	key := executionNodePairingPeerKey(s.localExecutionNodeID())
	if key == "" {
		return false, nil
	}
	_, err := s.settingRepo.GetValue(ctx, key)
	if errors.Is(err, ErrSettingNotFound) {
		return false, nil
	}
	if err != nil {
		return false, infraerrors.ServiceUnavailable("EXECUTION_NODE_PAIRING_UNAVAILABLE", "shared pairing state is unavailable")
	}
	status, err := s.GetExecutionNodePairingStatus(ctx)
	if err != nil || status == nil || !status.ProductionReady {
		return false, infraerrors.ServiceUnavailable("EXECUTION_NODE_PAIRING_UNAVAILABLE", "shared pairing state could not be verified")
	}
	return true, nil
}

// ExecutionNodeAdminWriteAccess reports whether this instance may change
// shared administrative data. Verified paired nodes have equal admin access;
// unpaired deployments retain the legacy primary/takeover policy.
//
// Routing weights are intentionally not covered by this decision: they are a
// shared cluster control and are allowed from either node.
func (s *SettingService) ExecutionNodeAdminWriteAccess(ctx context.Context) (bool, string) {
	if s == nil || s.cfg == nil || !s.cfg.Gateway.ExecutionNode.Enabled {
		return true, "single_node"
	}
	if allowed, err := s.pairedAdminAccess(ctx); err != nil {
		return false, "pairing_unavailable"
	} else if allowed {
		return true, "paired_full_access"
	}

	localNodeID := strings.TrimSpace(s.cfg.Gateway.ExecutionNode.ID)
	primaryNodeID := strings.TrimSpace(s.cfg.Gateway.ExecutionNode.LegacyUnassignedNodeID)
	if primaryNodeID == "" {
		primaryNodeID = "api"
	}
	if !validExecutionNodeID(localNodeID) || !validExecutionNodeID(primaryNodeID) {
		return false, "secondary_read_only"
	}
	if s.cfg.Gateway.ExecutionNode.Witness.Enabled {
		if s.executionNodeFailover == nil || !s.executionNodeFailover.Ready() {
			return false, "failover_unavailable"
		}
		if localNodeID == primaryNodeID {
			if s.executionNodeFailover.LocalAuthority() {
				return true, "primary"
			}
			return false, "primary_fenced"
		}
	}
	if localNodeID == primaryNodeID {
		return true, "primary"
	}

	// Unknown or unreadable shared state must never grant write access to a
	// secondary node. The explicit heartbeat result also prevents a startup or
	// Redis outage from being mistaken for a primary failure.
	if s.settingRepo == nil || s.executionNodeHealthReader == nil {
		return false, "secondary_read_only"
	}
	health, err := s.executionNodeHealthReader.HealthyExecutionNodes(ctx, []string{primaryNodeID})
	if err != nil {
		return false, "secondary_read_only"
	}
	primaryHealthy, known := health[primaryNodeID]
	if !known || primaryHealthy {
		return false, "secondary_read_only"
	}

	takeover, err := s.executionNodeTakeoverPermission(ctx)
	if err == nil && takeover && (!s.cfg.Gateway.ExecutionNode.Witness.Enabled || s.executionNodeFailover.LocalAuthority()) {
		return true, "emergency_takeover"
	}
	return false, "secondary_read_only"
}

// Administrative takeover needs a current permission read, not the scheduler's
// availability-oriented last-known policy fallback.
func (s *SettingService) executionNodeTakeoverPermission(ctx context.Context) (bool, error) {
	if s == nil || s.cfg == nil || s.settingRepo == nil {
		return false, errors.New("execution-node takeover policy is unavailable")
	}
	key := executionNodeEmergencyEgressSettingKey(s.cfg.Gateway.ExecutionNode.ID)
	if key == "" {
		return false, errors.New("execution-node identity is unavailable")
	}
	raw, err := s.settingRepo.GetValue(ctx, key)
	if err != nil && !errors.Is(err, ErrSettingNotFound) {
		return false, err
	}
	return decodeExecutionNodeEmergencyEgress(raw, s.cfg.Gateway.ExecutionNode.EmergencyLocalEgress)
}

func (s *SettingService) CanWriteSharedAdminState(ctx context.Context) bool {
	allowed, _ := s.ExecutionNodeAdminWriteAccess(ctx)
	return allowed
}
