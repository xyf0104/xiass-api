package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
)

func TestLoadIgnoresRetiredNodeConfiguration(t *testing.T) {
	for _, source := range []string{"environment", "yaml", "both"} {
		t.Run(source, func(t *testing.T) {
			resetViperWithJWTSecret(t)
			t.Setenv("DATABASE_HOST", "database.example.invalid")
			t.Setenv("REDIS_HOST", "cache.example.invalid")
			t.Setenv("JWT_REFRESH_TOKEN_STORE", "postgres")
			baseline, err := Load()
			require.NoError(t, err)

			if source != "yaml" {
				for key, value := range map[string]string{
					"GATEWAY_EXECUTION_NODE_ENABLED":                    "true",
					"GATEWAY_EXECUTION_NODE_ID":                         "retired-node",
					"GATEWAY_EXECUTION_NODE_DEFAULT_PROXY_ID":           "83",
					"GATEWAY_EXECUTION_NODE_CONTROL_PLANE":              "false",
					"GATEWAY_EXECUTION_NODE_LEGACY_UNASSIGNED_NODE_ID":  "retired-primary",
					"GATEWAY_EXECUTION_NODE_LEGACY_UNASSIGNED_PROXY_ID": "84",
					"GATEWAY_EXECUTION_NODE_WITNESS_ENABLED":            "true",
					"GATEWAY_EXECUTION_NODE_WITNESS_URL":                "https://retired.example.invalid",
					"GATEWAY_EXECUTION_NODE_EMERGENCY_LOCAL_EGRESS":     "true",
					"XIASS_CLUSTER_ENABLED":                             "true",
					"XIASS_CLUSTER_STATE_SOURCE_URL":                    "https://retired.example.invalid",
					"XIASS_CLUSTER_TUNNEL_TOKEN":                        "synthetic-fixture-token",
					"XIASS_CLUSTER_NODE_URLS_JSON":                      `{"retired-node":"https://retired.example.invalid"}`,
				} {
					t.Setenv(key, value)
				}
			}
			if source != "environment" {
				path := filepath.Join(t.TempDir(), "config.yaml")
				require.NoError(t, os.WriteFile(path, []byte(`gateway:
  execution_node:
    enabled: true
    id: retired-node
    default_proxy_id: 83
    control_plane: false
    legacy_unassigned_node_id: retired-primary
    legacy_unassigned_proxy_id: 84
    witness:
      enabled: true
      url: https://retired.example.invalid
    emergency_local_egress: true
`), 0600))
				t.Setenv("CONFIG_FILE", path)
			}

			cfg, err := Load()
			require.NoError(t, err)
			require.Equal(t, baseline.Gateway, cfg.Gateway)
			require.Equal(t, baseline.Database, cfg.Database)
			require.Equal(t, baseline.Redis, cfg.Redis)
			require.Equal(t, baseline.JWT, cfg.JWT)

			viper.Reset()
			setDefaults()
			for _, key := range viper.AllKeys() {
				require.NotContains(t, key, "execution_node")
			}
		})
	}
}
