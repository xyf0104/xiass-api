package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLoadRedisDiscoveryDefaultsDirect(t *testing.T) {
	resetViperWithJWTSecret(t)
	cfg, err := Load()
	require.NoError(t, err)
	require.Equal(t, RedisDiscoveryModeDirect, cfg.Redis.DiscoveryMode)
	require.False(t, cfg.Redis.UsesSentinel())
	require.Empty(t, cfg.Redis.SentinelAddrs)
	require.Empty(t, cfg.Redis.SentinelMasterName)
}

func TestLoadRedisRetiredSentinelEnvironmentDoesNotEnableDiscovery(t *testing.T) {
	resetViperWithJWTSecret(t)
	t.Setenv("REDIS_SENTINEL_ADDRS", "redis://user:do-not-log@invalid.example:26379")
	t.Setenv("REDIS_SENTINEL_MASTER_NAME", "retired-master")
	t.Setenv("REDIS_SENTINEL_USERNAME", "retired-user")
	t.Setenv("REDIS_SENTINEL_PASSWORD", "retired-secret")

	cfg, err := Load()
	require.NoError(t, err)
	require.Equal(t, RedisDiscoveryModeDirect, cfg.Redis.DiscoveryMode)
	require.False(t, cfg.Redis.UsesSentinel())
}

func TestValidateRedisEmptyDiscoveryModeRemainsDirectCompatible(t *testing.T) {
	resetViperWithJWTSecret(t)
	cfg, err := Load()
	require.NoError(t, err)
	cfg.Redis.DiscoveryMode = ""
	cfg.Redis.SentinelAddrs = []string{"not-a-host-port"}
	cfg.Redis.SentinelMasterName = ""
	cfg.Redis.SentinelPassword = "ignored-secret"
	require.NoError(t, cfg.Validate())
	require.False(t, cfg.Redis.UsesSentinel())
}

func TestLoadRedisSentinelFromEnvironmentRequiresExplicitMode(t *testing.T) {
	resetViperWithJWTSecret(t)
	t.Setenv("REDIS_DISCOVERY_MODE", " SENTINEL ")
	t.Setenv("REDIS_SENTINEL_ADDRS", " sentinel-1.example.net:26379 , [::1]:26380 ")
	t.Setenv("REDIS_SENTINEL_MASTER_NAME", " cache-primary ")
	t.Setenv("REDIS_SENTINEL_USERNAME", "discovery-user")
	t.Setenv("REDIS_SENTINEL_PASSWORD", " sentinel-secret ")
	t.Setenv("REDIS_USERNAME", "data-user")
	t.Setenv("REDIS_PASSWORD", " data-secret ")

	cfg, err := Load()
	require.NoError(t, err)
	require.True(t, cfg.Redis.UsesSentinel())
	require.Equal(t, []string{"sentinel-1.example.net:26379", "[::1]:26380"}, cfg.Redis.SentinelAddrs)
	require.Equal(t, "cache-primary", cfg.Redis.SentinelMasterName)
	require.Equal(t, "discovery-user", cfg.Redis.SentinelUsername)
	require.Equal(t, " sentinel-secret ", cfg.Redis.SentinelPassword)
	require.Equal(t, "data-user", cfg.Redis.Username)
	require.Equal(t, " data-secret ", cfg.Redis.Password)
}

func TestLoadRedisSentinelFromYAML(t *testing.T) {
	resetViperWithJWTSecret(t)
	path := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte(`redis:
  discovery_mode: sentinel
  sentinel_addrs: [" sentinel-1.example.net:26379 ", "127.0.0.1:26380"]
  sentinel_master_name: " cache-primary "
  sentinel_username: "discovery-user"
  sentinel_password: "sentinel-secret"
`), 0600))
	t.Setenv("CONFIG_FILE", path)

	cfg, err := Load()
	require.NoError(t, err)
	require.True(t, cfg.Redis.UsesSentinel())
	require.Equal(t, []string{"sentinel-1.example.net:26379", "127.0.0.1:26380"}, cfg.Redis.SentinelAddrs)
	require.Equal(t, "cache-primary", cfg.Redis.SentinelMasterName)
}

func TestLoadRedisSentinelRejectsIncompleteOrUnsafeConfiguration(t *testing.T) {
	for _, tc := range []struct {
		name, mode, addrs, master, want string
	}{
		{"unknown mode", "automatic", "", "", "redis.discovery_mode"},
		{"addresses only", "sentinel", "127.0.0.1:26379", "", "are required"},
		{"master only", "sentinel", "", "cache-primary", "are required"},
		{"empty list entry", "sentinel", "127.0.0.1:26379,", "cache-primary", "redis.sentinel_addrs[1]"},
		{"URL with credentials", "sentinel", "redis://user:do-not-log@localhost:26379", "cache-primary", "redis.sentinel_addrs[0]"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resetViperWithJWTSecret(t)
			t.Setenv("REDIS_DISCOVERY_MODE", tc.mode)
			t.Setenv("REDIS_SENTINEL_ADDRS", tc.addrs)
			t.Setenv("REDIS_SENTINEL_MASTER_NAME", tc.master)
			_, err := Load()
			require.ErrorContains(t, err, tc.want)
			require.NotContains(t, err.Error(), "do-not-log")
		})
	}
}
