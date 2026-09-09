package witness

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestStoreLeaseGenerationSurvivesRestartAndFencesStaleOwner(t *testing.T) {
	path := filepath.Join(t.TempDir(), "witness.db")
	store, err := Open(path)
	require.NoError(t, err)

	ctx := context.Background()
	started := time.Date(2026, time.September, 7, 12, 0, 0, 0, time.UTC)
	first, acquired, err := store.Acquire(ctx, "cluster-1", "api", "lease-api", 15*time.Second, started)
	require.NoError(t, err)
	require.True(t, acquired)
	require.Equal(t, int64(1), first.Generation)

	blocked, acquired, err := store.Acquire(ctx, "cluster-1", "api2", "lease-api2", 15*time.Second, started.Add(time.Second))
	require.NoError(t, err)
	require.False(t, acquired)
	require.Equal(t, "api", blocked.HolderNodeID)
	require.Equal(t, int64(1), blocked.Generation)

	second, acquired, err := store.Acquire(ctx, "cluster-1", "api2", "lease-api2", 15*time.Second, started.Add(16*time.Second))
	require.NoError(t, err)
	require.True(t, acquired)
	require.Equal(t, int64(2), second.Generation)
	require.Equal(t, "api2", second.HolderNodeID)

	_, released, err := store.Release(ctx, "cluster-1", "api", "lease-api", first.Generation, started.Add(17*time.Second))
	require.NoError(t, err)
	require.False(t, released, "a stale owner must not release its successor")
	require.NoError(t, store.Close())

	reopened, err := Open(path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = reopened.Close() })
	status, err := reopened.Status(ctx, "cluster-1", started.Add(17*time.Second))
	require.NoError(t, err)
	require.True(t, status.Active)
	require.Equal(t, int64(2), status.Generation)
	require.Equal(t, "api2", status.HolderNodeID)
}

func TestStoreConcurrentAcquireHasSingleWinner(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "witness.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })

	now := time.Date(2026, time.September, 7, 12, 0, 0, 0, time.UTC)
	start := make(chan struct{})
	type result struct {
		node     string
		lease    Lease
		acquired bool
		err      error
	}
	results := make(chan result, 2)
	var wg sync.WaitGroup
	for _, nodeID := range []string{"api", "api2"} {
		nodeID := nodeID
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			lease, acquired, err := store.Acquire(context.Background(), "cluster-1", nodeID, "lease-"+nodeID, 15*time.Second, now)
			results <- result{node: nodeID, lease: lease, acquired: acquired, err: err}
		}()
	}
	close(start)
	wg.Wait()
	close(results)

	winners := 0
	var winner string
	for got := range results {
		require.NoError(t, got.err)
		if got.acquired {
			winners++
			winner = got.node
		}
	}
	require.Equal(t, 1, winners)
	status, err := store.Status(context.Background(), "cluster-1", now)
	require.NoError(t, err)
	require.True(t, status.Active)
	require.Equal(t, winner, status.HolderNodeID)
	require.Equal(t, int64(1), status.Generation)
}

func TestServerRequiresAuthAndPreservesLeaseConflict(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "witness.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })
	server, err := NewServer(store, strings.Repeat("t", 32))
	require.NoError(t, err)
	server.now = func() time.Time {
		return time.Date(2026, time.September, 7, 12, 0, 0, 0, time.UTC)
	}
	httpServer := httptest.NewServer(server.Handler())
	t.Cleanup(httpServer.Close)

	request := func(nodeID, auth string) (*http.Response, leaseResponse) {
		body := `{"cluster_id":"cluster-1","node_id":"` + nodeID + `","lease_id":"lease-` + nodeID + `","ttl_seconds":15}`
		req, requestErr := http.NewRequest(http.MethodPost, httpServer.URL+"/v1/lease/acquire", strings.NewReader(body))
		require.NoError(t, requestErr)
		if auth != "" {
			req.Header.Set("Authorization", auth)
		}
		resp, requestErr := http.DefaultClient.Do(req)
		require.NoError(t, requestErr)
		var payload leaseResponse
		_ = json.NewDecoder(resp.Body).Decode(&payload)
		_ = resp.Body.Close()
		return resp, payload
	}

	unauthorized, _ := request("api", "")
	require.Equal(t, http.StatusUnauthorized, unauthorized.StatusCode)

	first, firstPayload := request("api", "Bearer "+strings.Repeat("t", 32))
	require.Equal(t, http.StatusOK, first.StatusCode)
	require.True(t, firstPayload.Acquired)
	require.Equal(t, "api", firstPayload.HolderNodeID)

	conflict, conflictPayload := request("api2", "Bearer "+strings.Repeat("t", 32))
	require.Equal(t, http.StatusConflict, conflict.StatusCode)
	require.False(t, conflictPayload.Acquired)
	require.Equal(t, "api", conflictPayload.HolderNodeID)
	require.Equal(t, int64(1), conflictPayload.Generation)
}
