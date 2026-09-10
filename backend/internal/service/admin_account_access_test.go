//go:build unit

package service

import (
	"context"
	"errors"
	"testing"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/stretchr/testify/require"
)

func TestUnpairedRemoteAccountManagementRemainsReadOnly(t *testing.T) {
	for _, tc := range []struct {
		name    string
		health  map[string]bool
		choice  string
		readErr error
		want    string
	}{
		{"healthy", map[string]bool{"api": true}, "true", nil, "ACCOUNT_REMOTE_NODE_READ_ONLY"},
		{"unknown", map[string]bool{}, "true", nil, "ACCOUNT_REMOTE_NODE_READ_ONLY"},
		{"disabled", map[string]bool{"api": false}, "false", nil, "ACCOUNT_REMOTE_NODE_READ_ONLY"},
		{"legacy permission ignored", map[string]bool{"api": false}, "true", nil, "ACCOUNT_REMOTE_NODE_READ_ONLY"},
		{"policy unavailable", map[string]bool{"api": false}, "true", errors.New("database unavailable"), "EXECUTION_NODE_PAIRING_UNAVAILABLE"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			settings := executionNodeAdminAccessService("api2", false, tc.choice)
			settings.SetExecutionNodeHealthReader(executionNodeAdminAccessHealth{values: tc.health})
			// An old routing cache may stay available during database failure.
			settings.executionNodeRoutingCache.Store(&cachedExecutionNodeRoutingSettings{
				settings:  ExecutionNodeRoutingSettings{Available: true},
				expiresAt: time.Now().Add(time.Minute).UnixNano(),
			})
			if tc.readErr != nil {
				settings.settingRepo = executionNodeAdminAccessSettingsError{SettingRepository: settings.settingRepo, err: tc.readErr}
			}
			svc := &adminServiceImpl{settingService: settings}
			account := &Account{ID: 7, Name: "remote", Extra: map[string]any{"xiass_execution_node_id": "api"}}
			err := svc.ensureAccountManagementAccess(context.Background(), account)
			if tc.want == "" {
				require.NoError(t, err)
			} else {
				require.Equal(t, tc.want, infraerrors.Reason(err))
			}
		})
	}
}

func TestPairedAccountManagementPreservesOwnershipAndRejectsMismatch(t *testing.T) {
	for _, nodeID := range []string{"api", "api2"} {
		settings, repo := verifiedPairedAdminService(t, nodeID)
		settings.SetExecutionNodeHealthReader(executionNodeAdminAccessHealth{err: errors.New("offline")})
		svc := &adminServiceImpl{settingService: settings}
		for _, ownerID := range []string{"api", "api2"} {
			proxyID := int64(42)
			account := &Account{ID: 7, ProxyID: &proxyID, Extra: map[string]any{"xiass_execution_node_id": ownerID}}
			require.NoError(t, svc.ensureAccountManagementAccess(context.Background(), account))
			require.Equal(t, ownerID, account.ExecutionNodeID("api"))
			require.Equal(t, int64(42), *account.ProxyID)
		}
		repo.values[executionNodePairingPeerKey(nodeID)] = `{"node_id":"other-node","protocol_version":-1}`
		for _, ownerID := range []string{"api", "api2"} {
			account := &Account{ID: 7, Extra: map[string]any{"xiass_execution_node_id": ownerID}}
			err := svc.ensureAccountManagementAccess(context.Background(), account)
			require.Equal(t, "EXECUTION_NODE_PAIRING_UNAVAILABLE", infraerrors.Reason(err))
		}
	}
}
