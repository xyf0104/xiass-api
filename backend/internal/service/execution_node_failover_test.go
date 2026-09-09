//go:build unit

package service

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type memoryExecutionNodeFailoverRepo struct {
	mu     sync.Mutex
	values map[string]string
	fence  ExecutionNodeFenceRecord
}

func newMemoryExecutionNodeFailoverRepo() *memoryExecutionNodeFailoverRepo {
	return &memoryExecutionNodeFailoverRepo{values: map[string]string{}}
}

func (r *memoryExecutionNodeFailoverRepo) Get(context.Context, string) (*Setting, error) {
	return nil, ErrSettingNotFound
}

func (r *memoryExecutionNodeFailoverRepo) GetValue(_ context.Context, key string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	value, ok := r.values[key]
	if !ok {
		return "", ErrSettingNotFound
	}
	return value, nil
}

func (r *memoryExecutionNodeFailoverRepo) Set(_ context.Context, key, value string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.values[key] = value
	return nil
}

func (r *memoryExecutionNodeFailoverRepo) GetMultiple(_ context.Context, keys []string) (map[string]string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	result := make(map[string]string, len(keys))
	for _, key := range keys {
		if value, ok := r.values[key]; ok {
			result[key] = value
		}
	}
	return result, nil
}

func (r *memoryExecutionNodeFailoverRepo) SetMultiple(_ context.Context, values map[string]string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for key, value := range values {
		r.values[key] = value
	}
	return nil
}

func (r *memoryExecutionNodeFailoverRepo) GetAll(context.Context) (map[string]string, error) {
	return map[string]string{}, nil
}

func (r *memoryExecutionNodeFailoverRepo) Delete(_ context.Context, key string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.values, key)
	return nil
}

func (r *memoryExecutionNodeFailoverRepo) ReadExecutionNodeFence(context.Context) (ExecutionNodeFenceRecord, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.fence.Generation == 0 {
		return ExecutionNodeFenceRecord{}, ErrSettingNotFound
	}
	return r.fence, nil
}

func (r *memoryExecutionNodeFailoverRepo) CommitExecutionNodeFence(_ context.Context, record ExecutionNodeFenceRecord) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.fence.Generation > record.Generation ||
		(r.fence.Generation == record.Generation && r.fence.Generation > 0 &&
			(r.fence.ClusterID != record.ClusterID || r.fence.HolderNodeID != record.HolderNodeID || r.fence.LeaseID != record.LeaseID)) {
		return false, nil
	}
	r.fence = record
	return true, nil
}

type memoryExecutionNodeWitness struct {
	mu    sync.Mutex
	lease ExecutionNodeWitnessLease
	err   error
}

func (w *memoryExecutionNodeWitness) Acquire(_ context.Context, clusterID, nodeID, leaseID string, ttl time.Duration) (ExecutionNodeWitnessLease, bool, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.err != nil {
		return ExecutionNodeWitnessLease{}, false, w.err
	}
	if w.lease.Active {
		if w.lease.ClusterID == clusterID && w.lease.HolderNodeID == nodeID && w.lease.LeaseID == leaseID {
			w.lease.Remaining = ttl
			w.lease.ExpiresAt = time.Now().Add(ttl)
			return w.lease, true, nil
		}
		return w.lease, false, nil
	}
	w.lease = ExecutionNodeWitnessLease{
		ClusterID: clusterID, HolderNodeID: nodeID, LeaseID: leaseID,
		Generation: w.lease.Generation + 1, ExpiresAt: time.Now().Add(ttl), Active: true, Remaining: ttl,
	}
	return w.lease, true, nil
}

func (w *memoryExecutionNodeWitness) Renew(_ context.Context, lease ExecutionNodeWitnessLease, ttl time.Duration) (ExecutionNodeWitnessLease, bool, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.err != nil {
		return ExecutionNodeWitnessLease{}, false, w.err
	}
	if !w.lease.Active || w.lease.ClusterID != lease.ClusterID || w.lease.HolderNodeID != lease.HolderNodeID ||
		w.lease.LeaseID != lease.LeaseID || w.lease.Generation != lease.Generation {
		return w.lease, false, nil
	}
	w.lease.ExpiresAt = time.Now().Add(ttl)
	w.lease.Remaining = ttl
	return w.lease, true, nil
}

func (w *memoryExecutionNodeWitness) Release(_ context.Context, lease ExecutionNodeWitnessLease) (ExecutionNodeWitnessLease, bool, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.err != nil {
		return ExecutionNodeWitnessLease{}, false, w.err
	}
	if !w.lease.Active || w.lease.ClusterID != lease.ClusterID || w.lease.HolderNodeID != lease.HolderNodeID ||
		w.lease.LeaseID != lease.LeaseID || w.lease.Generation != lease.Generation {
		return w.lease, false, nil
	}
	w.lease.Active = false
	w.lease.HolderNodeID = ""
	w.lease.LeaseID = ""
	w.lease.Remaining = 0
	w.lease.ExpiresAt = time.Time{}
	return w.lease, true, nil
}

func (w *memoryExecutionNodeWitness) Status(_ context.Context, _ string) (ExecutionNodeWitnessLease, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.err != nil {
		return ExecutionNodeWitnessLease{}, w.err
	}
	return w.lease, nil
}

func failoverTestConfig(nodeID string) *config.Config {
	cfg := &config.Config{}
	cfg.Gateway.ExecutionNode = config.GatewayExecutionNodeConfig{
		Enabled: true, ID: nodeID, EmergencyLocalEgress: true,
		LegacyUnassignedNodeID: "api",
		Witness: config.GatewayExecutionNodeWitnessConfig{
			Enabled: true, URL: "https://witness.example.com", Token: "01234567890123456789012345678901",
			ClusterID: "cluster-1", LeaseTTLSeconds: 15, RequestTimeoutSeconds: 3,
		},
	}
	return cfg
}

func TestExecutionNodeFailoverPrimarySecondaryTakeoverAndReturn(t *testing.T) {
	repo := newMemoryExecutionNodeFailoverRepo()
	repo.values[executionNodeEmergencyEgressSettingKey("api2")] = "true"
	witness := &memoryExecutionNodeWitness{}
	primaryHealth := &executionNodeAdminAccessHealth{values: map[string]bool{"api": true}}
	secondaryHealth := &executionNodeAdminAccessHealth{values: map[string]bool{"api": true}}

	primary := NewExecutionNodeFailoverService(repo, primaryHealth, witness, failoverTestConfig("api"))
	secondary := NewExecutionNodeFailoverService(repo, secondaryHealth, witness, failoverTestConfig("api2"))
	require.NoError(t, primary.Reconcile(context.Background()))
	require.True(t, primary.Ready())
	require.True(t, primary.LocalAuthority())
	require.Equal(t, int64(1), primary.Status().Generation)

	require.NoError(t, secondary.Reconcile(context.Background()))
	require.True(t, secondary.Ready(), "a non-holder connected to the same fenced database remains a healthy gateway")
	require.False(t, secondary.LocalAuthority())

	secondarySettings := NewSettingService(repo, failoverTestConfig("api2"))
	secondarySettings.SetExecutionNodeHealthReader(secondaryHealth)
	secondarySettings.SetExecutionNodeFailoverService(secondary)
	allowed, mode := secondarySettings.ExecutionNodeAdminWriteAccess(context.Background())
	require.False(t, allowed)
	require.Equal(t, "secondary_read_only", mode)

	primaryLease := primary.currentLease()
	_, released, err := witness.Release(context.Background(), primaryLease)
	require.NoError(t, err)
	require.True(t, released)
	secondaryHealth.values["api"] = false
	require.NoError(t, secondary.Reconcile(context.Background()))
	require.True(t, secondary.LocalAuthority())
	require.Equal(t, int64(2), secondary.Status().Generation)
	allowed, mode = secondarySettings.ExecutionNodeAdminWriteAccess(context.Background())
	require.True(t, allowed)
	require.Equal(t, "emergency_takeover", mode)

	secondaryHealth.values["api"] = true
	require.NoError(t, secondary.Reconcile(context.Background()))
	require.False(t, secondary.Ready(), "the old takeover generation is retired before the preferred node returns")
	require.NoError(t, primary.Reconcile(context.Background()))
	require.True(t, primary.LocalAuthority())
	require.Equal(t, int64(3), primary.Status().Generation)
	require.NoError(t, secondary.Reconcile(context.Background()))
	require.True(t, secondary.Ready())
	require.False(t, secondary.LocalAuthority())
}

func TestExecutionNodeFailoverWitnessFailureClosesAdminWrites(t *testing.T) {
	repo := newMemoryExecutionNodeFailoverRepo()
	witness := &memoryExecutionNodeWitness{err: errors.New("witness unavailable")}
	health := &executionNodeAdminAccessHealth{values: map[string]bool{"api": false}}
	failover := NewExecutionNodeFailoverService(repo, health, witness, failoverTestConfig("api2"))
	require.Error(t, failover.Reconcile(context.Background()))
	require.False(t, failover.Ready())

	settings := NewSettingService(repo, failoverTestConfig("api2"))
	settings.SetExecutionNodeHealthReader(health)
	settings.SetExecutionNodeFailoverService(failover)
	allowed, mode := settings.ExecutionNodeAdminWriteAccess(context.Background())
	require.False(t, allowed)
	require.Equal(t, "failover_unavailable", mode)
}

func TestExecutionNodeFailoverRejectsStaleDatabaseFence(t *testing.T) {
	repo := newMemoryExecutionNodeFailoverRepo()
	repo.fence = ExecutionNodeFenceRecord{
		ClusterID: "cluster-1", HolderNodeID: "api2", LeaseID: "newer",
		Generation: 9, ExpiresAt: time.Now().Add(time.Minute), UpdatedAt: time.Now(),
	}
	witness := &memoryExecutionNodeWitness{}
	primary := NewExecutionNodeFailoverService(repo, &executionNodeAdminAccessHealth{values: map[string]bool{"api": true}}, witness, failoverTestConfig("api"))
	err := primary.Reconcile(context.Background())
	require.ErrorContains(t, err, "rejected")
	require.False(t, primary.Ready())
	witness.mu.Lock()
	require.False(t, witness.lease.Active, "a node that cannot commit its generation must release the witness lease")
	witness.mu.Unlock()
}
