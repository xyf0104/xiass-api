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
	require.True(t, cfg.Gateway.ExecutionNode.ControlPlane)
	require.Equal(t, "api", cfg.Gateway.ExecutionNode.LegacyUnassignedNodeID)
	require.Zero(t, cfg.Gateway.ExecutionNode.LegacyUnassignedProxyID)
}

func TestRetiredDisasterRecoveryEnvironmentDoesNotChangeSharedState(t *testing.T) {
	resetViperWithJWTSecret(t)
	t.Setenv("REDIS_HOST", "shared-cache.example.invalid")
	t.Setenv("REDIS_PORT", "16380")
	t.Setenv("REDIS_SENTINEL_ADDRS", "unavailable.example.invalid:26379")
	t.Setenv("REDIS_SENTINEL_MASTER_NAME", "retired-master")
	t.Setenv("GATEWAY_EXECUTION_NODE_WITNESS_ENABLED", "true")
	t.Setenv("GATEWAY_EXECUTION_NODE_WITNESS_URL", "https://retired.example.invalid")
	cfg, err := Load()
	require.NoError(t, err)
	require.Equal(t, "shared-cache.example.invalid:16380", cfg.Redis.Address())
	require.False(t, cfg.Gateway.ExecutionNode.Enabled)
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

}
