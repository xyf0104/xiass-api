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

type executionNodeAdminAccessHealth struct {
	values map[string]bool
	err    error
}

func (h executionNodeAdminAccessHealth) HealthyExecutionNodes(_ context.Context, nodeIDs []string) (map[string]bool, error) {
	if h.err != nil {
		return nil, h.err
	}
	result := make(map[string]bool, len(nodeIDs))
	for _, nodeID := range nodeIDs {
		if value, ok := h.values[nodeID]; ok {
			result[nodeID] = value
		}
	}
	return result, nil
}

func executionNodeAdminAccessService(nodeID string, primaryHealthy bool, takeover string) *SettingService {
	cfg := &config.Config{}
	cfg.Gateway.ExecutionNode = config.GatewayExecutionNodeConfig{
		Enabled:                true,
		ID:                     nodeID,
		EmergencyLocalEgress:   true,
		LegacyUnassignedNodeID: "api",
	}
	repo := newExecutionNodePairingRepo("shared-db")
	repo.values[executionNodeEmergencyEgressSettingKey(nodeID)] = takeover
	svc := NewSettingService(repo, cfg)
	svc.SetExecutionNodeHealthReader(executionNodeAdminAccessHealth{values: map[string]bool{"api": primaryHealthy}})
	return svc
}

func TestDefaultExecutionNodeWeightsPreferPrimary(t *testing.T) {
	require.Equal(t, map[string]float64{"api": 9, "api2": 1}, defaultExecutionNodeWeights())
	require.Equal(t, float64(9), defaultExecutionNodeWeights()["api"])
	require.Equal(t, float64(1), defaultExecutionNodeWeights()["api2"])

	explicit, err := normalizeExecutionNodeWeights(map[string]float64{"api": 4, "api2": 2})
	require.NoError(t, err)
	require.Equal(t, map[string]float64{"api": 4, "api2": 2}, explicit, "an existing custom policy must never be replaced by defaults")
}

type executionNodeDefaultWeightsMigratorStub struct {
	SettingRepository
	calls int
}

func (r *executionNodeDefaultWeightsMigratorStub) MigrateExecutionNodeDefaultWeights(context.Context, string) (bool, error) {
	r.calls++
	return true, nil
}

func TestExecutionNodeDefaultWeightsMigrationRunsOnlyOnConfiguredSource(t *testing.T) {
	for _, test := range []struct {
		name      string
		localID   string
		sourceID  string
		wantCalls int
	}{
		{name: "source with custom node ID", localID: "source-node", sourceID: "source-node", wantCalls: 1},
		{name: "joined node", localID: "api2", sourceID: "source-node", wantCalls: 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			repo := &executionNodeDefaultWeightsMigratorStub{}
			cfg := &config.Config{}
			cfg.Gateway.ExecutionNode = config.GatewayExecutionNodeConfig{
				Enabled:                true,
				ID:                     test.localID,
				LegacyUnassignedNodeID: test.sourceID,
			}
			svc := NewSettingService(repo, cfg)
			_, err := svc.MigrateExecutionNodeDefaultWeights(context.Background())
			require.NoError(t, err)
			require.Equal(t, test.wantCalls, repo.calls)
		})
	}
}

func TestExecutionNodeAdminWriteAccessPrimaryAlwaysWritable(t *testing.T) {
	svc := executionNodeAdminAccessService("api", false, "false")
	allowed, mode := svc.ExecutionNodeAdminWriteAccess(context.Background())
	require.True(t, allowed)
	require.Equal(t, "primary", mode)
}

func TestExecutionNodeAdminWriteAccessSecondaryReadOnlyWhilePrimaryHealthy(t *testing.T) {
	svc := executionNodeAdminAccessService("api2", true, "true")
	allowed, mode := svc.ExecutionNodeAdminWriteAccess(context.Background())
	require.False(t, allowed)
	require.Equal(t, "secondary_read_only", mode)
}

func TestExecutionNodeAdminWriteAccessSecondaryRequiresOfflineAndTakeover(t *testing.T) {
	disabled := executionNodeAdminAccessService("api2", false, "false")
	allowed, mode := disabled.ExecutionNodeAdminWriteAccess(context.Background())
	require.False(t, allowed)
	require.Equal(t, "secondary_read_only", mode)

	enabled := executionNodeAdminAccessService("api2", false, "true")
	allowed, mode = enabled.ExecutionNodeAdminWriteAccess(context.Background())
	require.True(t, allowed)
	require.Equal(t, "emergency_takeover", mode)
}

func TestExecutionNodeAdminWriteAccessFailsClosedWhenHeartbeatUnknown(t *testing.T) {
	svc := executionNodeAdminAccessService("api2", false, "true")
	svc.SetExecutionNodeHealthReader(executionNodeAdminAccessHealth{err: errors.New("redis unavailable")})
	allowed, mode := svc.ExecutionNodeAdminWriteAccess(context.Background())
	require.False(t, allowed)
	require.Equal(t, "secondary_read_only", mode)
}

type executionNodeAdminAccessSettingsError struct {
	SettingRepository
	err error
}

func (r executionNodeAdminAccessSettingsError) GetValue(context.Context, string) (string, error) {
	return "", r.err
}

func (r executionNodeAdminAccessSettingsError) GetMultiple(context.Context, []string) (map[string]string, error) {
	return nil, r.err
}

func TestExecutionNodeAdminWriteAccessDoesNotTreatPolicyReadErrorAsPermission(t *testing.T) {
	svc := executionNodeAdminAccessService("api2", false, "false")
	svc.settingRepo = executionNodeAdminAccessSettingsError{SettingRepository: svc.settingRepo, err: errors.New("database read unavailable")}
	allowed, mode := svc.ExecutionNodeAdminWriteAccess(context.Background())
	require.False(t, allowed, "the local deployment default true cannot override an unreadable shared choice")
	require.Equal(t, "pairing_unavailable", mode)
}

func verifiedPairedAdminService(t *testing.T, nodeID string) (*SettingService, *executionNodePairingRepo) {
	t.Helper()
	svc, repo := newExecutionNodePairingService(nodeID, "shared-db", "shared-redis")
	db, redis, auth, state, err := svc.localPairingMaterial(context.Background())
	require.NoError(t, err)
	peerID := "api"
	if nodeID == peerID {
		peerID = "api2"
	}
	peer := ExecutionNodePairingPeer{NodeID: peerID, ProtocolVersion: executionNodePairingProtocolVersion,
		DatabaseFingerprint: db, RedisFingerprint: redis, AuthFingerprint: auth, StateFingerprint: state, Ready: true}
	raw, err := json.Marshal(peer)
	require.NoError(t, err)
	require.NoError(t, repo.Set(context.Background(), executionNodePairingPeerKey(nodeID), string(raw)))
	return svc, repo
}

func TestExecutionNodeAdminPairedAccessIndependentOfRuntimeAuthority(t *testing.T) {
	for _, nodeID := range []string{"api", "api2"} {
		for _, health := range []executionNodeAdminAccessHealth{
			{values: map[string]bool{"api": true, "api2": true}},
			{values: map[string]bool{"api": false, "api2": false}},
			{err: errors.New("heartbeat unavailable")},
		} {
			svc, _ := verifiedPairedAdminService(t, nodeID)
			svc.cfg.Gateway.ExecutionNode.Witness.Enabled = true // No witness service or local authority.
			svc.cfg.Gateway.ExecutionNode.EmergencyLocalEgress = false
			svc.SetExecutionNodeHealthReader(health)
			allowed, mode := svc.ExecutionNodeAdminWriteAccess(context.Background())
			require.True(t, allowed)
			require.Equal(t, "paired_full_access", mode)
			require.True(t, svc.CanWriteSharedAdminState(context.Background()))
		}
	}
}

func TestExecutionNodeAdminPairedMismatchCannotFallBackToLegacyPermission(t *testing.T) {
	for _, nodeID := range []string{"api", "api2"} {
		for _, field := range []string{"protocol_version", "database_fingerprint", "redis_fingerprint", "auth_fingerprint", "state_fingerprint", "node_id", "malformed", "pending"} {
			t.Run(nodeID+"/"+field, func(t *testing.T) {
				svc, repo := verifiedPairedAdminService(t, nodeID)
				svc.cfg.Gateway.ExecutionNode.EmergencyLocalEgress = true
				svc.SetExecutionNodeHealthReader(executionNodeAdminAccessHealth{values: map[string]bool{"api": false}})
				key := executionNodePairingPeerKey(nodeID)
				var peer map[string]any
				require.NoError(t, json.Unmarshal([]byte(repo.values[key]), &peer))
				peer[field] = "mismatch"
				if field == "protocol_version" {
					peer[field] = -1
				}
				if field == "node_id" {
					peer[field] = nodeID
				}
				if field == "pending" {
					peer["ready"] = false
					peer["tunnel_proof_hash"] = "pending-proof"
				}
				raw, err := json.Marshal(peer)
				require.NoError(t, err)
				if field == "malformed" {
					raw = []byte("{")
				}
				repo.values[key] = string(raw)
				allowed, mode := svc.ExecutionNodeAdminWriteAccess(context.Background())
				require.False(t, allowed)
				require.Equal(t, "pairing_unavailable", mode)
			})
		}
	}
}

func TestExecutionNodeAdminPairedAccessRevalidatesSharedState(t *testing.T) {
	ctx := context.Background()
	svc, _ := verifiedPairedAdminService(t, "api2")
	allowed, _ := svc.ExecutionNodeAdminWriteAccess(ctx)
	require.True(t, allowed)
	svc.SetExecutionNodePairingStateReader(executionNodePairingState("different-redis"))
	allowed, mode := svc.ExecutionNodeAdminWriteAccess(ctx)
	require.False(t, allowed)
	require.Equal(t, "pairing_unavailable", mode)
	svc.SetExecutionNodePairingStateReader(nil)
	allowed, mode = svc.ExecutionNodeAdminWriteAccess(ctx)
	require.False(t, allowed)
	require.Equal(t, "pairing_unavailable", mode)
	svc.SetExecutionNodePairingStateReader(executionNodePairingState("shared-redis"))
	allowed, _ = svc.ExecutionNodeAdminWriteAccess(ctx)
	require.True(t, allowed)
	require.NoError(t, svc.UnpairExecutionNode(ctx))
	allowed, mode = svc.ExecutionNodeAdminWriteAccess(ctx)
	require.False(t, allowed)
	require.Equal(t, "secondary_read_only", mode)
}

func TestExecutionNodeAdminSingleNodeRetainsAccess(t *testing.T) {
	svc := NewSettingService(nil, &config.Config{})
	allowed, mode := svc.ExecutionNodeAdminWriteAccess(context.Background())
	require.True(t, allowed)
	require.Equal(t, "single_node", mode)
}

func TestExecutionNodeAdminWriteAccessRetainsExplicitLegacyConfigFallback(t *testing.T) {
	svc := executionNodeAdminAccessService("api2", false, "true")
	svc.settingRepo = executionNodeAdminAccessSettingsError{SettingRepository: svc.settingRepo, err: ErrSettingNotFound}
	allowed, mode := svc.ExecutionNodeAdminWriteAccess(context.Background())
	require.True(t, allowed, "only a missing setting may retain the explicit deployment choice")
	require.Equal(t, "emergency_takeover", mode)
}
