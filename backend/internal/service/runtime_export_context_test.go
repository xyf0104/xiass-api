package service

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
)

func TestRuntimeExportContextRoundTripsEffectiveConfig(t *testing.T) {
	t.Parallel()
	cfg := &config.Config{
		RunMode: "standard", Timezone: "Asia/Shanghai",
		Database:  config.DatabaseConfig{Host: "actual-state.example.invalid", Port: 15432, User: "current-user", Password: "current$secret", DBName: "shared", SSLMode: "verify-full", MaxOpenConns: 17},
		Redis:     config.RedisConfig{Host: "cache.example.invalid", Port: 16379, DB: 4, Username: "xiass-app-test", Password: "new-cache-secret"},
		JWT:       config.JWTConfig{Secret: "generated-signing-secret", RefreshTokenStore: "postgres", ExpireHour: 168},
		Totp:      config.TotpConfig{EncryptionKey: strings.Repeat("a", 64), EncryptionKeyConfigured: true},
		Dashboard: config.DashboardCacheConfig{Enabled: true, KeyPrefix: "custom:"},
	}
	cfg.Ops.MetricsCollectorCache.TTL = 1500 * time.Millisecond
	snapshot, err := makeRuntimeExportContext(cfg, []string{"CONFIG_FILE=/external/custom.json", "DATABASE_HOST=old-value", "CUSTOM_INSTALL_SETTING=preserved=with=equals"})
	require.NoError(t, err)
	require.Equal(t, 2, snapshot.Version)
	require.Equal(t, "/external/custom.json", snapshot.Environment["CONFIG_FILE"])
	require.Equal(t, "preserved=with=equals", snapshot.Environment["CUSTOM_INSTALL_SETTING"])
	data, err := json.Marshal(snapshot.Config)
	require.NoError(t, err)
	v := viper.New()
	v.SetConfigType("json")
	require.NoError(t, v.ReadConfig(bytes.NewReader(data)))
	var restored config.Config
	require.NoError(t, v.Unmarshal(&restored))
	require.Equal(t, cfg.Database, restored.Database)
	require.Equal(t, cfg.Redis, restored.Redis)
	require.Equal(t, cfg.JWT, restored.JWT)
	require.Equal(t, cfg.Totp.EncryptionKey, restored.Totp.EncryptionKey)
	require.False(t, restored.Totp.EncryptionKeyConfigured, "runtime-only flags are recomputed, not serialized as configuration")
	require.Equal(t, cfg.Ops.MetricsCollectorCache.TTL, restored.Ops.MetricsCollectorCache.TTL)
	require.Equal(t, cfg.Dashboard, restored.Dashboard)
	require.Equal(t, cfg.Timezone, restored.Timezone)
}

func TestRuntimeExportContextIsPrivateAndRejectsMissingConfig(t *testing.T) {
	t.Parallel()
	_, err := makeRuntimeExportContext(nil, nil)
	require.Error(t, err)
	svc := &RuntimeExportService{directory: t.TempDir(), cfg: &config.Config{JWT: config.JWTConfig{Secret: "private-signing-secret"}}}
	path, err := svc.writeRuntimeContext()
	require.NoError(t, err)
	info, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0600), info.Mode().Perm())
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	var snapshot runtimeExportContext
	require.NoError(t, json.Unmarshal(data, &snapshot))
	jwt, ok := snapshot.Config["jwt"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "private-signing-secret", jwt["secret"])
}
