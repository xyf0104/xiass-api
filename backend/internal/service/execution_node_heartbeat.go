package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/google/uuid"
)

const (
	executionNodeHeartbeatKeyPrefix = "xiass:execution_node:heartbeat:"
	executionNodeClusterIdentityKey = "xiass:execution_node:cluster_identity"
	executionNodeHeartbeatInterval  = 5 * time.Second
	executionNodeHeartbeatTTL       = 20 * time.Second
)

// ExecutionNodeHealthReader exposes the shared liveness view used by account
// routing. Implementations must fail closed when the shared store is unavailable.
type ExecutionNodeHealthReader interface {
	HealthyExecutionNodes(ctx context.Context, nodeIDs []string) (map[string]bool, error)
}

// ExecutionNodeHeartbeatStore keeps Redis-specific lease operations behind the
// repository boundary while the service owns lifecycle and node validation.
type ExecutionNodeHeartbeatStore interface {
	TouchExecutionNode(ctx context.Context, nodeID, owner string, ttl time.Duration) error
	ReleaseExecutionNode(ctx context.Context, nodeID, owner string) error
	HealthyExecutionNodes(ctx context.Context, nodeIDs []string) (map[string]bool, error)
}

// ExecutionNodeHeartbeatService publishes one short-lived heartbeat per XIASS
// application instance. A missing heartbeat is the only condition that permits
// owner availability filtering; ordinary upstream or proxy errors continue
// through the existing request-level account failover path.
type ExecutionNodeHeartbeatService struct {
	store    ExecutionNodeHeartbeatStore
	cfg      *config.Config
	nodeID   string
	owner    string
	stopCh   chan struct{}
	stopOnce sync.Once
	wg       sync.WaitGroup
}

func NewExecutionNodeHeartbeatService(store ExecutionNodeHeartbeatStore, cfg *config.Config) *ExecutionNodeHeartbeatService {
	nodeID := ""
	if cfg != nil {
		nodeID = strings.TrimSpace(cfg.Gateway.ExecutionNode.ID)
	}
	return &ExecutionNodeHeartbeatService{
		store:  store,
		cfg:    cfg,
		nodeID: nodeID,
		owner:  uuid.NewString(),
		stopCh: make(chan struct{}),
	}
}

func (s *ExecutionNodeHeartbeatService) enabled() bool {
	return s != nil && s.store != nil && s.cfg != nil &&
		s.cfg.Gateway.ExecutionNode.Enabled && validExecutionNodeID(s.nodeID)
}

func (s *ExecutionNodeHeartbeatService) Start() {
	if !s.enabled() {
		return
	}
	s.touch()
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		ticker := time.NewTicker(executionNodeHeartbeatInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				s.touch()
			case <-s.stopCh:
				return
			}
		}
	}()
}

func (s *ExecutionNodeHeartbeatService) Stop() {
	if s == nil {
		return
	}
	s.stopOnce.Do(func() { close(s.stopCh) })
	s.wg.Wait()
	if !s.enabled() {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = s.store.ReleaseExecutionNode(ctx, s.nodeID, s.owner)
}

func (s *ExecutionNodeHeartbeatService) touch() {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = s.store.TouchExecutionNode(ctx, s.nodeID, s.owner, executionNodeHeartbeatTTL)
}

// ExecutionNodeHeartbeatKey is the shared Redis key contract used by the
// scheduler and readiness probes. Keeping it here prevents the public ingress
// health check from drifting away from the failover liveness source.
func ExecutionNodeHeartbeatKey(nodeID string) string {
	return executionNodeHeartbeatKeyPrefix + strings.TrimSpace(nodeID)
}

// ExecutionNodeClusterIdentityKey is a durable Redis key shared by all
// instances that intentionally use the same Redis database. It is used only
// during administrator pairing; it is not an authentication secret.
func ExecutionNodeClusterIdentityKey() string {
	return executionNodeClusterIdentityKey
}

// EnsureSharedStateIdentity asks the Redis-backed heartbeat store for the
// cluster identity. Keeping this optional preserves deterministic service
// tests and makes a missing Redis wiring fail closed in production pairing.
func (s *ExecutionNodeHeartbeatService) EnsureSharedStateIdentity(ctx context.Context, candidate string) (string, error) {
	if s == nil || s.store == nil {
		return "", errors.New("execution node shared-state store is unavailable")
	}
	provider, ok := s.store.(interface {
		EnsureExecutionNodeClusterIdentity(context.Context, string) (string, error)
	})
	if !ok {
		return "", errors.New("execution node shared-state identity is unavailable")
	}
	return provider.EnsureExecutionNodeClusterIdentity(ctx, candidate)
}

func (s *ExecutionNodeHeartbeatService) HealthyExecutionNodes(ctx context.Context, nodeIDs []string) (map[string]bool, error) {
	if !s.enabled() {
		return nil, errors.New("execution node heartbeat service is unavailable")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	normalized := make([]string, 0, len(nodeIDs))
	seen := make(map[string]struct{}, len(nodeIDs))
	for _, raw := range nodeIDs {
		nodeID := strings.TrimSpace(raw)
		if !validExecutionNodeID(nodeID) {
			return nil, fmt.Errorf("invalid execution node id %q", raw)
		}
		if _, ok := seen[nodeID]; ok {
			continue
		}
		seen[nodeID] = struct{}{}
		normalized = append(normalized, nodeID)
	}
	return s.store.HealthyExecutionNodes(ctx, normalized)
}
