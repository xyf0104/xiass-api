//go:build integration

package repository

import (
	"context"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/go-connections/nat"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

// This test runs a real Redis master, replica and Sentinel, then asks Sentinel
// to promote the replica. Fixed matching host/container ports make Sentinel's
// advertised 127.0.0.1 endpoints reachable by the host-side application client.
func TestRedisSentinelActualRoleSwitchIntegration(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	masterPort := reserveRedisTestPort(t)
	replicaPort := reserveRedisTestPort(t)
	sentinelPort := reserveRedisTestPort(t)
	ports := []int{masterPort, replicaPort, sentinelPort}
	bindings := nat.PortMap{}
	exposed := make([]string, 0, len(ports))
	for _, port := range ports {
		containerPort := nat.Port(fmt.Sprintf("%d/tcp", port))
		exposed = append(exposed, string(containerPort))
		bindings[containerPort] = []nat.PortBinding{{HostIP: "127.0.0.1", HostPort: strconv.Itoa(port)}}
	}

	script := fmt.Sprintf(`set -eu
mkdir -p /tmp/primary /tmp/replica
redis-server --port %[1]d --bind 0.0.0.0 --protected-mode no --requirepass data-secret --masterauth data-secret --replica-announce-ip 127.0.0.1 --replica-announce-port %[1]d --dir /tmp/primary --logfile /tmp/primary.log --save '' --appendonly no --daemonize yes
redis-server --port %[2]d --bind 0.0.0.0 --protected-mode no --requirepass data-secret --masterauth data-secret --replica-announce-ip 127.0.0.1 --replica-announce-port %[2]d --dir /tmp/replica --logfile /tmp/replica.log --replicaof 127.0.0.1 %[1]d --save '' --appendonly no --daemonize yes
cat >/tmp/sentinel.conf <<'EOF'
port %[3]d
bind 0.0.0.0
protected-mode no
requirepass sentinel-secret
logfile /tmp/sentinel.log
sentinel monitor cache-primary 127.0.0.1 %[1]d 1
sentinel auth-pass cache-primary data-secret
sentinel down-after-milliseconds cache-primary 1000
sentinel failover-timeout cache-primary 10000
sentinel parallel-syncs cache-primary 1
EOF
redis-server /tmp/sentinel.conf --sentinel --daemonize yes
exec tail -f /tmp/sentinel.log
`, masterPort, replicaPort, sentinelPort)

	ctr, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        "redis:8.4-alpine",
			ExposedPorts: exposed,
			Cmd:          []string{"sh", "-ec", script},
			WaitingFor:   wait.ForListeningPort(nat.Port(fmt.Sprintf("%d/tcp", sentinelPort))).WithStartupTimeout(30 * time.Second),
			HostConfigModifier: func(hostConfig *container.HostConfig) {
				hostConfig.PortBindings = bindings
			},
		},
		Started: true,
	})
	if ctr != nil {
		t.Cleanup(func() {
			cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cleanupCancel()
			if t.Failed() {
				if output, err := ctr.Logs(cleanupCtx); err == nil {
					defer output.Close()
					data, _ := io.ReadAll(io.LimitReader(output, 12000))
					t.Logf("synthetic Sentinel diagnostics: %s", data)
				}
			}
			stopTimeout := time.Second
			require.NoError(t, ctr.Terminate(cleanupCtx, testcontainers.StopTimeout(stopTimeout)))
		})
	}
	require.NoError(t, err)

	masterAddr := net.JoinHostPort("127.0.0.1", strconv.Itoa(masterPort))
	replicaAddr := net.JoinHostPort("127.0.0.1", strconv.Itoa(replicaPort))
	sentinelAddr := net.JoinHostPort("127.0.0.1", strconv.Itoa(sentinelPort))
	oldMaster := redis.NewClient(&redis.Options{Addr: masterAddr, Password: "data-secret", DB: 2})
	newMaster := redis.NewClient(&redis.Options{Addr: replicaAddr, Password: "data-secret", DB: 2})
	sentinel := redis.NewSentinelClient(&redis.Options{Addr: sentinelAddr, Password: "sentinel-secret"})
	t.Cleanup(func() {
		_ = oldMaster.Close()
		_ = newMaster.Close()
		_ = sentinel.Close()
	})

	require.Eventually(t, func() bool {
		if oldMaster.Ping(ctx).Err() != nil || newMaster.Ping(ctx).Err() != nil || sentinel.Ping(ctx).Err() != nil {
			return false
		}
		info, err := newMaster.Info(ctx, "replication").Result()
		return err == nil && strings.Contains(info, "role:slave") && strings.Contains(info, "master_link_status:up")
	}, 20*time.Second, 100*time.Millisecond)
	require.Eventually(t, func() bool {
		replicas, err := sentinel.Replicas(ctx, "cache-primary").Result()
		return err == nil && len(replicas) == 1 && replicas[0]["ip"] == "127.0.0.1" &&
			replicas[0]["port"] == strconv.Itoa(replicaPort)
	}, 20*time.Second, 100*time.Millisecond)

	cfg := redisDiscoveryTestConfig()
	cfg.Redis.DiscoveryMode = config.RedisDiscoveryModeSentinel
	cfg.Redis.Username = ""
	cfg.Redis.Host = "unused-direct.example.invalid"
	cfg.Redis.SentinelAddrs = []string{sentinelAddr}
	cfg.Redis.SentinelMasterName = "cache-primary"
	cfg.Redis.SentinelPassword = "sentinel-secret"
	client := InitRedis(cfg)
	t.Cleanup(func() { _ = client.Close() })
	require.NoError(t, client.Set(ctx, "before-role-switch", "replicated", 0).Err())
	require.Eventually(t, func() bool {
		return newMaster.Get(ctx, "before-role-switch").Val() == "replicated"
	}, 10*time.Second, 50*time.Millisecond)

	require.NoError(t, sentinel.Failover(ctx, "cache-primary").Err())
	require.Eventually(t, func() bool {
		addr, err := sentinel.GetMasterAddrByName(ctx, "cache-primary").Result()
		return err == nil && len(addr) == 2 && addr[0] == "127.0.0.1" && addr[1] == strconv.Itoa(replicaPort)
	}, 20*time.Second, 100*time.Millisecond)
	require.Eventually(t, func() bool {
		return redisTestRole(ctx, newMaster) == "master" && redisTestRole(ctx, oldMaster) == "slave"
	}, 20*time.Second, 100*time.Millisecond)
	require.Eventually(t, func() bool {
		return client.Set(ctx, "after-role-switch", "new-master", 0).Err() == nil &&
			newMaster.Get(ctx, "after-role-switch").Val() == "new-master"
	}, 20*time.Second, 100*time.Millisecond)
	require.Equal(t, "replicated", newMaster.Get(ctx, "before-role-switch").Val())
}

func reserveRedisTestPort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := listener.Addr().(*net.TCPAddr).Port
	require.NoError(t, listener.Close())
	return port
}

func redisTestRole(ctx context.Context, client *redis.Client) string {
	values, err := client.Do(ctx, "ROLE").Slice()
	if err != nil || len(values) == 0 {
		return ""
	}
	return fmt.Sprint(values[0])
}
