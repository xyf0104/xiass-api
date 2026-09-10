package repository

import (
	"context"
	"crypto/tls"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/servertiming"
	"github.com/alicebob/miniredis/v2"
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
