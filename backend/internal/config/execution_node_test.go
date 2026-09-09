package config

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLoadExecutionNodeDefaultsDisabled(t *testing.T) {
	resetViperWithJWTSecret(t)
	cfg, err := Load()
	require.NoError(t, err)
	require.False(t, cfg.Gateway.ExecutionNode.Enabled)
	require.Empty(t, cfg.Gateway.ExecutionNode.ID)
	require.Zero(t, cfg.Gateway.ExecutionNode.DefaultProxyID)
	require.True(t, cfg.Gateway.ExecutionNode.EmergencyLocalEgress)
	require.True(t, cfg.Gateway.ExecutionNode.ControlPlane)
	require.Equal(t, "api", cfg.Gateway.ExecutionNode.LegacyUnassignedNodeID)
	require.Zero(t, cfg.Gateway.ExecutionNode.LegacyUnassignedProxyID)
	require.False(t, cfg.Gateway.ExecutionNode.Witness.Enabled)
	require.Empty(t, cfg.Gateway.ExecutionNode.Witness.URL)
	require.Empty(t, cfg.Gateway.ExecutionNode.Witness.Token)
	require.Empty(t, cfg.Gateway.ExecutionNode.Witness.ClusterID)
	require.Equal(t, 15, cfg.Gateway.ExecutionNode.Witness.LeaseTTLSeconds)
	require.Equal(t, 3, cfg.Gateway.ExecutionNode.Witness.RequestTimeoutSeconds)
}

func TestValidateExecutionNodeConfiguration(t *testing.T) {
	buildValid := func(t *testing.T) *Config {
		resetViperWithJWTSecret(t)
		cfg, err := Load()
		require.NoError(t, err)
		cfg.Gateway.ExecutionNode = GatewayExecutionNodeConfig{
			Enabled:                 true,
			ID:                      "api2",
			DefaultProxyID:          83,
			ControlPlane:            false,
			LegacyUnassignedNodeID:  "api",
			LegacyUnassignedProxyID: 84,
		}
		return cfg
	}

	t.Run("valid", func(t *testing.T) {
		require.NoError(t, buildValid(t).Validate())
	})

	for _, test := range []struct {
		name    string
		mutate  func(*Config)
		wantErr string
	}{
		{
			name: "missing node id",
			mutate: func(cfg *Config) {
				cfg.Gateway.ExecutionNode.ID = ""
			},
			wantErr: "gateway.execution_node.id",
		},
		{
			name: "unsafe node id",
			mutate: func(cfg *Config) {
				cfg.Gateway.ExecutionNode.ID = "api/other"
			},
			wantErr: "gateway.execution_node.id",
		},
		{
			name: "missing proxy",
			mutate: func(cfg *Config) {
				cfg.Gateway.ExecutionNode.DefaultProxyID = 0
			},
			wantErr: "gateway.execution_node.default_proxy_id",
		},
		{
			name: "missing legacy owner",
			mutate: func(cfg *Config) {
				cfg.Gateway.ExecutionNode.LegacyUnassignedNodeID = ""
			},
			wantErr: "gateway.execution_node.legacy_unassigned_node_id",
		},
		{
			name: "missing legacy proxy",
			mutate: func(cfg *Config) {
				cfg.Gateway.ExecutionNode.LegacyUnassignedProxyID = 0
			},
			wantErr: "gateway.execution_node.legacy_unassigned_proxy_id",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			cfg := buildValid(t)
			test.mutate(cfg)
			err := cfg.Validate()
			require.Error(t, err)
			require.True(t, strings.Contains(err.Error(), test.wantErr), err.Error())
		})
	}

	t.Run("valid witness", func(t *testing.T) {
		cfg := buildValid(t)
		cfg.Gateway.ExecutionNode.Witness = GatewayExecutionNodeWitnessConfig{
			Enabled: true, URL: "https://witness.example.com", Token: strings.Repeat("a", 32),
			ClusterID: "cluster-1", LeaseTTLSeconds: 15, RequestTimeoutSeconds: 3,
		}
		require.NoError(t, cfg.Validate())
	})

	for _, test := range []struct {
		name    string
		witness GatewayExecutionNodeWitnessConfig
		wantErr string
	}{
		{
			name:    "non-loopback http witness",
			witness: GatewayExecutionNodeWitnessConfig{Enabled: true, URL: "http://witness.example.com", Token: strings.Repeat("a", 32), LeaseTTLSeconds: 15, RequestTimeoutSeconds: 3},
			wantErr: "must use HTTPS",
		},
		{
			name:    "short witness token",
			witness: GatewayExecutionNodeWitnessConfig{Enabled: true, URL: "https://witness.example.com", Token: "short", LeaseTTLSeconds: 15, RequestTimeoutSeconds: 3},
			wantErr: "at least 32",
		},
		{
			name:    "unsafe witness cluster id",
			witness: GatewayExecutionNodeWitnessConfig{Enabled: true, URL: "https://witness.example.com", Token: strings.Repeat("a", 32), ClusterID: "cluster/1", LeaseTTLSeconds: 15, RequestTimeoutSeconds: 3},
			wantErr: "cluster_id",
		},
		{
			name:    "unsafe witness ttl",
			witness: GatewayExecutionNodeWitnessConfig{Enabled: true, URL: "https://witness.example.com", Token: strings.Repeat("a", 32), LeaseTTLSeconds: 5, RequestTimeoutSeconds: 3},
			wantErr: "lease_ttl_seconds",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			cfg := buildValid(t)
			cfg.Gateway.ExecutionNode.Witness = test.witness
			err := cfg.Validate()
			require.ErrorContains(t, err, test.wantErr)
		})
	}
}
