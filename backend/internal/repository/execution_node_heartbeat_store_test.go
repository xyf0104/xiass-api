package repository

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestExecutionNodeHeartbeatTouchUsesOwnerCAS(t *testing.T) {
	redisServer := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: redisServer.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	store := &executionNodeHeartbeatStore{rdb: client}
	ctx := context.Background()

	require.NoError(t, store.TouchExecutionNode(ctx, "api2", "owner-a", time.Minute))
	value, err := redisServer.Get("xiass:execution_node:heartbeat:api2")
	require.NoError(t, err)
	require.Equal(t, "owner-a", value)

	err = store.TouchExecutionNode(ctx, "api2", "owner-b", time.Minute)
	require.Error(t, err)
	value, err = redisServer.Get("xiass:execution_node:heartbeat:api2")
	require.NoError(t, err)
	require.Equal(t, "owner-a", value)

	require.NoError(t, store.TouchExecutionNode(ctx, "api2", "owner-a", time.Minute))
	require.NoError(t, store.ReleaseExecutionNode(ctx, "api2", "owner-a"))
	require.NoError(t, store.TouchExecutionNode(ctx, "api2", "owner-b", time.Minute))
	value, err = redisServer.Get("xiass:execution_node:heartbeat:api2")
	require.NoError(t, err)
	require.Equal(t, "owner-b", value)
}
