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
// unpaired deployments retain the legacy primary-only policy.
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
	if localNodeID == primaryNodeID {
		return true, "primary"
	}

	return false, "secondary_read_only"
}

func (s *SettingService) CanWriteSharedAdminState(ctx context.Context) bool {
	allowed, _ := s.ExecutionNodeAdminWriteAccess(ctx)
	return allowed
}
