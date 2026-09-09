package repository

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/redis/go-redis/v9"
)

var executionNodeHeartbeatReleaseScript = redis.NewScript(`
if redis.call("GET", KEYS[1]) == ARGV[1] then
  return redis.call("DEL", KEYS[1])
end
return 0
`)

var executionNodeHeartbeatTouchScript = redis.NewScript(`
local current = redis.call("GET", KEYS[1])
if current == false or current == ARGV[1] then
  redis.call("SET", KEYS[1], ARGV[1], "PX", ARGV[2])
  return 1
end
return 0
`)

type executionNodeHeartbeatStore struct {
	rdb *redis.Client
}

func NewExecutionNodeHeartbeatStore(rdb *redis.Client) service.ExecutionNodeHeartbeatStore {
	if rdb == nil {
		return nil
	}
	return &executionNodeHeartbeatStore{rdb: rdb}
}

func (s *executionNodeHeartbeatStore) TouchExecutionNode(
	ctx context.Context,
	nodeID, owner string,
	ttl time.Duration,
) error {
	if ttl <= 0 {
		return fmt.Errorf("execution node heartbeat TTL must be positive")
	}
	result, err := executionNodeHeartbeatTouchScript.Run(
		ctx,
		s.rdb,
		[]string{service.ExecutionNodeHeartbeatKey(nodeID)},
		owner,
		ttl.Milliseconds(),
	).Int()
	if err != nil {
		return err
	}
	if result != 1 {
		return fmt.Errorf("execution node heartbeat owner changed")
	}
	return nil
}

func (s *executionNodeHeartbeatStore) ReleaseExecutionNode(ctx context.Context, nodeID, owner string) error {
	return executionNodeHeartbeatReleaseScript.Run(
		ctx,
		s.rdb,
		[]string{service.ExecutionNodeHeartbeatKey(nodeID)},
		owner,
	).Err()
}

func (s *executionNodeHeartbeatStore) HealthyExecutionNodes(
	ctx context.Context,
	nodeIDs []string,
) (map[string]bool, error) {
	result := make(map[string]bool, len(nodeIDs))
	if len(nodeIDs) == 0 {
		return result, nil
	}
	keys := make([]string, len(nodeIDs))
	for i, nodeID := range nodeIDs {
		keys[i] = service.ExecutionNodeHeartbeatKey(nodeID)
	}
	values, err := s.rdb.MGet(ctx, keys...).Result()
	if err != nil {
		return nil, err
	}
	for i, nodeID := range nodeIDs {
		result[nodeID] = i < len(values) && values[i] != nil && strings.TrimSpace(fmt.Sprint(values[i])) != ""
	}
	return result, nil
}

// EnsureExecutionNodeClusterIdentity creates the Redis-side identity exactly
// once for a shared Redis database. SETNX prevents two XIASS instances starting
// together from silently choosing different identities.
func (s *executionNodeHeartbeatStore) EnsureExecutionNodeClusterIdentity(ctx context.Context, candidate string) (string, error) {
	candidate = strings.TrimSpace(candidate)
	if candidate == "" {
		return "", fmt.Errorf("cluster identity candidate is empty")
	}
	if err := s.rdb.SetNX(ctx, service.ExecutionNodeClusterIdentityKey(), candidate, 0).Err(); err != nil {
		return "", err
	}
	value, err := s.rdb.Get(ctx, service.ExecutionNodeClusterIdentityKey()).Result()
	if err != nil {
		return "", err
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("shared Redis cluster identity is empty")
	}
	return value, nil
}
