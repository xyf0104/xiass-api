package repository

import (
	"context"
	"crypto/tls"
	"net"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/servertiming"
	"github.com/alicebob/miniredis/v2"
	"github.com/alicebob/miniredis/v2/server"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestBuildRedisOptions(t *testing.T) {
	cfg := &config.Config{
		Redis: config.RedisConfig{
			Host:                "localhost",
			Port:                6379,
			Username:            "app-user",
			Password:            "secret",
			DB:                  2,
			DialTimeoutSeconds:  5,
			ReadTimeoutSeconds:  3,
			WriteTimeoutSeconds: 4,
			PoolSize:            100,
			MinIdleConns:        10,
		},
	}

	opts := buildRedisOptions(cfg)
	require.Equal(t, "localhost:6379", opts.Addr)
	require.Equal(t, "app-user", opts.Username)
	require.Equal(t, "secret", opts.Password)
	require.Equal(t, 2, opts.DB)
	require.Equal(t, 5*time.Second, opts.DialTimeout)
	require.Equal(t, 3*time.Second, opts.ReadTimeout)
	require.Equal(t, 4*time.Second, opts.WriteTimeout)
	require.Equal(t, 100, opts.PoolSize)
	require.Equal(t, 10, opts.MinIdleConns)
	require.Nil(t, opts.TLSConfig)

	// Test case with TLS enabled
	cfgTLS := &config.Config{
		Redis: config.RedisConfig{
			Host:      "localhost",
			EnableTLS: true,
		},
	}
	optsTLS := buildRedisOptions(cfgTLS)
	require.NotNil(t, optsTLS.TLSConfig)
	require.Equal(t, "localhost", optsTLS.TLSConfig.ServerName)
	require.Equal(t, uint16(tls.VersionTLS12), optsTLS.TLSConfig.MinVersion)
	require.False(t, optsTLS.TLSConfig.InsecureSkipVerify)
}

func redisDiscoveryTestConfig() *config.Config {
	return &config.Config{Redis: config.RedisConfig{
		Host: "127.0.0.1", Port: 6379, Username: "data-user", Password: "data-secret", DB: 2,
		DialTimeoutSeconds: 1, ReadTimeoutSeconds: 2, WriteTimeoutSeconds: 3,
		PoolSize: 7, MinIdleConns: 0,
	}}
}

func TestBuildRedisFailoverOptions(t *testing.T) {
	cfg := redisDiscoveryTestConfig()
	cfg.Redis.DiscoveryMode = config.RedisDiscoveryModeSentinel
	cfg.Redis.MinIdleConns = 2
	cfg.Redis.SentinelMasterName = "cache-primary"
	cfg.Redis.SentinelAddrs = []string{"sentinel.example.net:26379", "[::1]:26380"}
	cfg.Redis.SentinelUsername = "discovery-user"
	cfg.Redis.SentinelPassword = "sentinel-secret"
	base := buildRedisOptions(cfg)
	opts := buildRedisFailoverOptions(cfg)
	require.Equal(t, cfg.Redis.SentinelMasterName, opts.MasterName)
	require.Equal(t, cfg.Redis.SentinelAddrs, opts.SentinelAddrs)
	require.Equal(t, "discovery-user", opts.SentinelUsername)
	require.Equal(t, "sentinel-secret", opts.SentinelPassword)
	require.Equal(t, base.Username, opts.Username)
	require.Equal(t, base.Password, opts.Password)
	require.Equal(t, base.DB, opts.DB)
	require.Equal(t, base.DialTimeout, opts.DialTimeout)
	require.Equal(t, base.ReadTimeout, opts.ReadTimeout)
	require.Equal(t, base.WriteTimeout, opts.WriteTimeout)
	require.Equal(t, base.PoolSize, opts.PoolSize)
	require.Equal(t, base.MinIdleConns, opts.MinIdleConns)
	require.Nil(t, opts.TLSConfig)
	opts.SentinelAddrs[0] = "changed.example.net:26379"
	require.Equal(t, "sentinel.example.net:26379", cfg.Redis.SentinelAddrs[0])

	cfg.Redis.EnableTLS = true
	opts = buildRedisFailoverOptions(cfg)
	require.Equal(t, uint16(tls.VersionTLS12), opts.TLSConfig.MinVersion)
	require.Empty(t, opts.TLSConfig.ServerName, "TLS must verify each actual endpoint")
	require.False(t, opts.TLSConfig.InsecureSkipVerify)
	require.Equal(t, cfg.Redis.Host, buildRedisOptions(cfg).TLSConfig.ServerName)
}

func TestInitRedisDirectConnection(t *testing.T) {
	for _, timing := range []bool{false, true} {
		t.Run("timing="+strconv.FormatBool(timing), func(t *testing.T) {
			master := miniredis.RunT(t)
			master.RequireUserAuth("data-user", "data-secret")
			cfg := redisDiscoveryTestConfig()
			cfg.Redis.Port, _ = strconv.Atoi(master.Port())
			cfg.Server.EnableServerTiming = timing
			t.Setenv("REDIS_SENTINEL_ADDRS", "unavailable.invalid:26379")
			t.Setenv("REDIS_SENTINEL_MASTER_NAME", "removed")
			client := InitRedis(cfg)
			t.Cleanup(func() { _ = client.Close() })
			require.NoError(t, client.Ping(context.Background()).Err())
			collector := servertiming.New(time.Now())
			ctx := servertiming.WithCollector(context.Background(), collector)
			require.NoError(t, client.Set(ctx, "direct-key", "value", 0).Err())
			_, err := client.Pipelined(ctx, func(pipe redis.Pipeliner) error {
				pipe.Get(ctx, "direct-key")
				pipe.Exists(ctx, "direct-key")
				return nil
			})
			require.NoError(t, err)
			value, err := master.DB(2).Get("direct-key")
			require.NoError(t, err)
			require.Equal(t, "value", value)
			header := collector.HeaderValue(time.Now(), "bypass")
			require.Equal(t, timing, strings.Contains(header, "commands=3"), header)
			require.NotContains(t, header, "direct-key")
			require.Equal(t, 7, client.Options().PoolSize)
			require.Equal(t, 0, client.Options().MinIdleConns)
		})
	}
}

func TestInitRedisSentinelConnection(t *testing.T) {
	for _, timing := range []bool{false, true} {
		t.Run("timing="+strconv.FormatBool(timing), func(t *testing.T) {
			master := miniredis.RunT(t)
			master.RequireUserAuth("data-user", "data-secret")
			sentinel, _ := syntheticRedisSentinel(t, master.Addr(), nil)
			sentinel.RequireUserAuth("discovery-user", "sentinel-secret")
			cfg := redisDiscoveryTestConfig()
			cfg.Redis.DiscoveryMode = config.RedisDiscoveryModeSentinel
			cfg.Redis.Host = "unused-direct.example.invalid"
			cfg.Redis.SentinelAddrs = []string{sentinel.Addr()}
			cfg.Redis.SentinelMasterName = "cache-primary"
			cfg.Redis.SentinelUsername = "discovery-user"
			cfg.Redis.SentinelPassword = "sentinel-secret"
			cfg.Server.EnableServerTiming = timing

			client := InitRedis(cfg)
			t.Cleanup(func() { _ = client.Close() })
			require.NoError(t, client.Ping(context.Background()).Err())
			collector := servertiming.New(time.Now())
			ctx := servertiming.WithCollector(context.Background(), collector)
			require.NoError(t, client.Set(ctx, "sentinel-key", "value", 0).Err())
			_, err := client.Pipelined(ctx, func(pipe redis.Pipeliner) error {
				pipe.Get(ctx, "sentinel-key")
				pipe.Exists(ctx, "sentinel-key")
				return nil
			})
			require.NoError(t, err)
			value, err := master.DB(2).Get("sentinel-key")
			require.NoError(t, err)
			require.Equal(t, "value", value)
			header := collector.HeaderValue(time.Now(), "bypass")
			require.Equal(t, timing, strings.Contains(header, "commands=3"), header)
			require.Equal(t, 7, client.Options().PoolSize)
			require.Equal(t, time.Second, client.Options().DialTimeout)
			require.Equal(t, 2*time.Second, client.Options().ReadTimeout)
			require.Equal(t, 3*time.Second, client.Options().WriteTimeout)
		})
	}
}

// This fixture supplies Sentinel discovery and switch notifications. It is a
// unit-test double; the integration test exercises a real Redis role switch.
func syntheticRedisSentinel(t *testing.T, initialMaster string, tlsConfig *tls.Config) (*miniredis.Miniredis, func(string) int) {
	t.Helper()
	sentinel := miniredis.NewMiniRedis()
	if tlsConfig == nil {
		require.NoError(t, sentinel.Start())
	} else {
		require.NoError(t, sentinel.StartTLS(tlsConfig))
	}
	t.Cleanup(sentinel.Close)
	var master atomic.Value
	master.Store(initialMaster)
	require.NoError(t, sentinel.Server().Register("SENTINEL", func(peer *server.Peer, _ string, args []string) {
		if len(args) != 2 || args[1] != "cache-primary" {
			peer.WriteError("ERR unexpected Sentinel request")
			return
		}
		switch strings.ToLower(args[0]) {
		case "get-master-addr-by-name":
			addr, ok := master.Load().(string)
			require.True(t, ok)
			host, port, _ := net.SplitHostPort(addr)
			peer.WriteLen(2)
			peer.WriteBulk(host)
			peer.WriteBulk(port)
		case "sentinels":
			peer.WriteLen(0)
		default:
			peer.WriteError("ERR unsupported Sentinel command")
		}
	}))
	return sentinel, func(addr string) int {
		previous, ok := master.Swap(addr).(string)
		require.True(t, ok)
		oldHost, oldPort, _ := net.SplitHostPort(previous)
		newHost, newPort, _ := net.SplitHostPort(addr)
		return sentinel.Publish("+switch-master", strings.Join([]string{"cache-primary", oldHost, oldPort, newHost, newPort}, " "))
	}
}
