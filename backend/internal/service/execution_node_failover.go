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

const executionNodeFailoverPollInterval = 2 * time.Second

type ExecutionNodeWitnessLease struct {
	ClusterID    string        `json:"cluster_id"`
	HolderNodeID string        `json:"holder_node_id"`
	LeaseID      string        `json:"lease_id,omitempty"`
	Generation   int64         `json:"generation"`
	ExpiresAt    time.Time     `json:"expires_at,omitempty"`
	Active       bool          `json:"active"`
	Remaining    time.Duration `json:"-"`
}

type ExecutionNodeWitnessClient interface {
	Acquire(ctx context.Context, clusterID, nodeID, leaseID string, ttl time.Duration) (ExecutionNodeWitnessLease, bool, error)
	Renew(ctx context.Context, lease ExecutionNodeWitnessLease, ttl time.Duration) (ExecutionNodeWitnessLease, bool, error)
	Release(ctx context.Context, lease ExecutionNodeWitnessLease) (ExecutionNodeWitnessLease, bool, error)
	Status(ctx context.Context, clusterID string) (ExecutionNodeWitnessLease, error)
}

type ExecutionNodeFenceRecord struct {
	ClusterID    string    `json:"cluster_id"`
	HolderNodeID string    `json:"holder_node_id"`
	LeaseID      string    `json:"lease_id"`
	Generation   int64     `json:"generation"`
	ExpiresAt    time.Time `json:"expires_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type ExecutionNodeFenceStore interface {
	ReadExecutionNodeFence(ctx context.Context) (ExecutionNodeFenceRecord, error)
	CommitExecutionNodeFence(ctx context.Context, record ExecutionNodeFenceRecord) (bool, error)
}

type ExecutionNodeFailoverStatus struct {
	Enabled            bool      `json:"enabled"`
	Ready              bool      `json:"ready"`
	WitnessReachable   bool      `json:"witness_reachable"`
	DatabaseFenceReady bool      `json:"database_fence_ready"`
	LocalAuthority     bool      `json:"local_authority"`
	HolderNodeID       string    `json:"holder_node_id,omitempty"`
	Generation         int64     `json:"generation"`
	ExpiresAt          time.Time `json:"expires_at,omitempty"`
	LastError          string    `json:"last_error,omitempty"`
}

type ExecutionNodeFailoverService struct {
	cfg          *config.Config
	settingRepo  SettingRepository
	healthReader ExecutionNodeHealthReader
	witness      ExecutionNodeWitnessClient
	fenceStore   ExecutionNodeFenceStore
	nodeID       string
	primaryID    string
	leaseID      string
	ttl          time.Duration

	mu          sync.RWMutex
	lease       ExecutionNodeWitnessLease
	localExpiry time.Time
	status      ExecutionNodeFailoverStatus
	stopCh      chan struct{}
	stopOnce    sync.Once
	wg          sync.WaitGroup
}

func NewExecutionNodeFailoverService(
	settingRepo SettingRepository,
	healthReader ExecutionNodeHealthReader,
	witness ExecutionNodeWitnessClient,
	cfg *config.Config,
) *ExecutionNodeFailoverService {
	nodeID := ""
	primaryID := "api"
	ttl := 15 * time.Second
	if cfg != nil {
		nodeID = strings.TrimSpace(cfg.Gateway.ExecutionNode.ID)
		if configured := strings.TrimSpace(cfg.Gateway.ExecutionNode.LegacyUnassignedNodeID); configured != "" {
			primaryID = configured
		}
		if cfg.Gateway.ExecutionNode.Witness.LeaseTTLSeconds > 0 {
			ttl = time.Duration(cfg.Gateway.ExecutionNode.Witness.LeaseTTLSeconds) * time.Second
		}
	}
	return &ExecutionNodeFailoverService{
		cfg: cfg, settingRepo: settingRepo, healthReader: healthReader, witness: witness,
		fenceStore: executionNodeFenceStoreFrom(settingRepo), nodeID: nodeID, primaryID: primaryID,
		leaseID: uuid.NewString(), ttl: ttl, stopCh: make(chan struct{}),
	}
}

func executionNodeFenceStoreFrom(repo SettingRepository) ExecutionNodeFenceStore {
	store, _ := repo.(ExecutionNodeFenceStore)
	return store
}

func (s *ExecutionNodeFailoverService) enabled() bool {
	return s != nil && s.cfg != nil && s.cfg.Gateway.ExecutionNode.Enabled &&
		s.cfg.Gateway.ExecutionNode.Witness.Enabled && validExecutionNodeID(s.nodeID) &&
		s.witness != nil && s.settingRepo != nil && s.fenceStore != nil
}

func (s *ExecutionNodeFailoverService) Start() {
	if !s.enabled() {
		return
	}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		s.reconcileWithTimeout()
		ticker := time.NewTicker(executionNodeFailoverPollInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				s.reconcileWithTimeout()
			case <-s.stopCh:
				return
			}
		}
	}()
}

func (s *ExecutionNodeFailoverService) Stop() {
	if s == nil {
		return
	}
	s.stopOnce.Do(func() { close(s.stopCh) })
	s.wg.Wait()
	lease := s.currentLease()
	if !s.enabled() || !s.leaseOwnedLocally(lease) {
		return
	}
	timeout := time.Duration(s.cfg.Gateway.ExecutionNode.Witness.RequestTimeoutSeconds) * time.Second
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	_, _, _ = s.witness.Release(ctx, lease)
}

func (s *ExecutionNodeFailoverService) reconcileWithTimeout() {
	timeout := time.Duration(s.cfg.Gateway.ExecutionNode.Witness.RequestTimeoutSeconds) * time.Second
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	_ = s.Reconcile(ctx)
}

// Reconcile is exported for deterministic failover drills. Production calls
// it from the bounded background loop; tests can drive each state transition
// without sleeping.
func (s *ExecutionNodeFailoverService) Reconcile(ctx context.Context) error {
	if !s.enabled() {
		return errors.New("execution-node witness is not configured")
	}
	clusterID, err := s.clusterID(ctx)
	if err != nil {
		s.recordFailure(err)
		return err
	}
	shouldHold, err := s.shouldHoldLease(ctx)
	if err != nil {
		s.recordFailure(err)
		return err
	}

	current := s.currentLease()
	if !shouldHold && s.leaseOwnedLocally(current) {
		if _, released, releaseErr := s.witness.Release(ctx, current); releaseErr != nil {
			s.recordFailure(releaseErr)
			return releaseErr
		} else if !released {
			s.clearLocalLease()
		}
		current = ExecutionNodeWitnessLease{}
	}

	var witnessLease ExecutionNodeWitnessLease
	if shouldHold {
		if s.leaseOwnedLocally(current) && s.localLeaseValid() {
			var renewed bool
			witnessLease, renewed, err = s.witness.Renew(ctx, current, s.ttl)
			if err == nil && !renewed {
				// A former holder may retain a locally valid deadline after an
				// orderly hand-back. Confirming that the exact lease is gone lets it
				// compete for a fresh generation immediately instead of waiting for
				// the next poll; an active successor still rejects this acquire.
				witnessLease, renewed, err = s.witness.Acquire(ctx, clusterID, s.nodeID, s.leaseID, s.ttl)
				if err == nil && !renewed {
					err = fmt.Errorf("witness lease is held by %s", strings.TrimSpace(witnessLease.HolderNodeID))
				}
			}
		} else {
			var acquired bool
			witnessLease, acquired, err = s.witness.Acquire(ctx, clusterID, s.nodeID, s.leaseID, s.ttl)
			if err == nil && !acquired {
				err = fmt.Errorf("witness lease is held by %s", strings.TrimSpace(witnessLease.HolderNodeID))
			}
		}
	} else {
		witnessLease, err = s.witness.Status(ctx, clusterID)
	}
	if err != nil {
		s.recordFailure(err)
		return err
	}
	if witnessLease.ClusterID == "" {
		witnessLease.ClusterID = clusterID
	}
	s.setObservedLease(witnessLease)

	if s.leaseOwnedLocally(witnessLease) {
		committed, commitErr := s.fenceStore.CommitExecutionNodeFence(ctx, fenceRecordFromLease(witnessLease))
		if commitErr != nil || !committed {
			_, _, _ = s.witness.Release(ctx, witnessLease)
			s.clearLocalLease()
			if commitErr == nil {
				commitErr = errors.New("database rejected the witness generation")
			}
			s.recordFailure(commitErr)
			return commitErr
		}
	}

	fence, err := s.fenceStore.ReadExecutionNodeFence(ctx)
	if err != nil {
		s.recordFailure(err)
		return err
	}
	s.recordSuccess(witnessLease, fence)
	return nil
}

func (s *ExecutionNodeFailoverService) clusterID(ctx context.Context) (string, error) {
	if configured := strings.TrimSpace(s.cfg.Gateway.ExecutionNode.Witness.ClusterID); configured != "" {
		if !validExecutionNodeClusterID(configured) {
			return "", errors.New("configured witness cluster ID is invalid")
		}
		return configured, nil
	}
	value, err := s.settingRepo.GetValue(ctx, SettingKeyExecutionNodeClusterID)
	if err == nil && validExecutionNodeClusterID(value) {
		return strings.TrimSpace(value), nil
	}
	if err != nil && !errors.Is(err, ErrSettingNotFound) {
		return "", fmt.Errorf("read execution-node cluster ID: %w", err)
	}
	if repo, ok := s.settingRepo.(ExecutionNodePairingRepository); ok {
		value, err = repo.EnsureExecutionNodeClusterID(ctx, uuid.NewString())
		if err == nil && validExecutionNodeClusterID(value) {
			return strings.TrimSpace(value), nil
		}
	}
	return "", errors.New("execution-node cluster ID is unavailable")
}

func (s *ExecutionNodeFailoverService) shouldHoldLease(ctx context.Context) (bool, error) {
	if s.nodeID == s.primaryID {
		return true, nil
	}
	if s.healthReader == nil {
		return false, errors.New("primary heartbeat is unavailable")
	}
	health, err := s.healthReader.HealthyExecutionNodes(ctx, []string{s.primaryID})
	if err != nil {
		return false, fmt.Errorf("read primary heartbeat: %w", err)
	}
	primaryHealthy, known := health[s.primaryID]
	if !known {
		return false, errors.New("primary heartbeat is unknown")
	}
	if primaryHealthy {
		return false, nil
	}
	key := executionNodeEmergencyEgressSettingKey(s.nodeID)
	raw, err := s.settingRepo.GetValue(ctx, key)
	if err != nil && !errors.Is(err, ErrSettingNotFound) {
		return false, fmt.Errorf("read takeover policy: %w", err)
	}
	enabled, err := decodeExecutionNodeEmergencyEgress(raw, s.cfg.Gateway.ExecutionNode.EmergencyLocalEgress)
	if err != nil {
		return false, err
	}
	return enabled, nil
}

func fenceRecordFromLease(lease ExecutionNodeWitnessLease) ExecutionNodeFenceRecord {
	return ExecutionNodeFenceRecord{
		ClusterID: lease.ClusterID, HolderNodeID: lease.HolderNodeID, LeaseID: lease.LeaseID,
		Generation: lease.Generation, ExpiresAt: lease.ExpiresAt.UTC(), UpdatedAt: time.Now().UTC(),
	}
}

func validExecutionNodeClusterID(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 128 {
		return false
	}
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') || r == '.' || r == '_' || r == '-' {
			continue
		}
		return false
	}
	return true
}

func (s *ExecutionNodeFailoverService) currentLease() ExecutionNodeWitnessLease {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.lease
}

func (s *ExecutionNodeFailoverService) leaseOwnedLocally(lease ExecutionNodeWitnessLease) bool {
	return lease.Active && lease.HolderNodeID == s.nodeID && lease.LeaseID == s.leaseID && lease.Generation > 0
}

func (s *ExecutionNodeFailoverService) localLeaseValid() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.leaseOwnedLocally(s.lease) && time.Now().Before(s.localExpiry)
}

func (s *ExecutionNodeFailoverService) setObservedLease(lease ExecutionNodeWitnessLease) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lease = lease
	remaining := lease.Remaining
	if remaining > 0 {
		// Stop trusting the local copy before the witness deadline. This absorbs
		// transport latency and small wall-clock differences without extending it.
		margin := remaining / 5
		if margin < time.Second {
			margin = time.Second
		}
		s.localExpiry = time.Now().Add(remaining - margin)
	} else {
		s.localExpiry = time.Time{}
	}
}

func (s *ExecutionNodeFailoverService) clearLocalLease() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lease = ExecutionNodeWitnessLease{}
	s.localExpiry = time.Time{}
}

func (s *ExecutionNodeFailoverService) recordFailure(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status.Enabled = s.enabled()
	s.status.Ready = false
	s.status.WitnessReachable = false
	s.status.DatabaseFenceReady = false
	s.status.LocalAuthority = false
	if err != nil {
		s.status.LastError = err.Error()
	}
}

func (s *ExecutionNodeFailoverService) recordSuccess(lease ExecutionNodeWitnessLease, fence ExecutionNodeFenceRecord) {
	ready := lease.Active && lease.Generation > 0 && fence.ClusterID == lease.ClusterID &&
		fence.HolderNodeID == lease.HolderNodeID && fence.LeaseID == lease.LeaseID &&
		fence.Generation == lease.Generation
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status = ExecutionNodeFailoverStatus{
		Enabled: true, Ready: ready, WitnessReachable: true, DatabaseFenceReady: ready,
		LocalAuthority: ready && lease.HolderNodeID == s.nodeID && lease.LeaseID == s.leaseID,
		HolderNodeID:   lease.HolderNodeID, Generation: lease.Generation, ExpiresAt: lease.ExpiresAt,
	}
}

func (s *ExecutionNodeFailoverService) Status() ExecutionNodeFailoverStatus {
	if s == nil {
		return ExecutionNodeFailoverStatus{}
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	status := s.status
	if !s.enabled() {
		status.Enabled = false
		status.Ready = false
		return status
	}
	status.Enabled = true
	if status.Ready && (!s.lease.Active || time.Now().After(s.localExpiry)) {
		status.Ready = false
		status.LocalAuthority = false
		status.LastError = "witness lease observation expired"
	}
	return status
}

func (s *ExecutionNodeFailoverService) Ready() bool {
	return s != nil && s.Status().Ready
}

func (s *ExecutionNodeFailoverService) LocalAuthority() bool {
	status := s.Status()
	return status.Ready && status.LocalAuthority
}
