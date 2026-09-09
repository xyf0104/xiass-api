package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/Wei-Shaw/sub2api/internal/witness"
	"github.com/stretchr/testify/require"
)

const witnessClientTestToken = "synthetic-witness-test-token-0123456789"

func newWitnessHTTPTestClient(t *testing.T, target string) *executionNodeWitnessClient {
	t.Helper()
	cfg := &config.Config{}
	cfg.Gateway.ExecutionNode.Witness = config.GatewayExecutionNodeWitnessConfig{
		Enabled: true, URL: target, Token: witnessClientTestToken, RequestTimeoutSeconds: 1,
	}
	client, ok := NewExecutionNodeWitnessClient(cfg).(*executionNodeWitnessClient)
	require.True(t, ok)
	return client
}

func TestExecutionNodeWitnessClientHTTPStatuses(t *testing.T) {
	for _, operation := range []string{"acquire", "renew", "release", "status"} {
		for _, status := range []int{200, 409, 401, 500, 503} {
			t.Run(fmt.Sprintf("%s/%d", operation, status), func(t *testing.T) {
				lease := service.ExecutionNodeWitnessLease{
					ClusterID: "cluster-1", HolderNodeID: "api", LeaseID: "lease-1",
					Generation: 7, Active: true, ExpiresAt: time.Now().Add(15 * time.Second),
				}
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Header.Get("Authorization") != "Bearer "+witnessClientTestToken {
						t.Error("missing witness authorization")
					}
					if r.URL.Path != "/prefix/v1/lease/"+operation {
						t.Errorf("unexpected path: %s", r.URL.Path)
					}
					if operation == "status" {
						if r.Method != http.MethodGet || r.URL.Query().Get("cluster_id") != lease.ClusterID {
							t.Error("invalid status request")
						}
					} else {
						var request executionNodeWitnessRequest
						if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
							t.Error(err)
						}
						want := executionNodeWitnessRequest{ClusterID: lease.ClusterID, NodeID: lease.HolderNodeID, LeaseID: lease.LeaseID}
						if operation != "acquire" {
							want.Generation = lease.Generation
						}
						if operation != "release" {
							want.TTLSeconds = 15
						}
						if request != want || r.Method != http.MethodPost || r.Header.Get("Content-Type") != "application/json" {
							t.Errorf("invalid %s request: %+v", operation, request)
						}
					}
					w.WriteHeader(status)
					_ = json.NewEncoder(w).Encode(executionNodeWitnessResponse{
						ExecutionNodeWitnessLease: lease, RemainingMS: 15000,
						Acquired: true, Renewed: true, Released: true,
					})
				}))
				defer server.Close()
				client := newWitnessHTTPTestClient(t, server.URL+"/prefix/")
				var got service.ExecutionNodeWitnessLease
				var accepted bool
				var err error
				switch operation {
				case "acquire":
					got, accepted, err = client.Acquire(context.Background(), lease.ClusterID, lease.HolderNodeID, lease.LeaseID, 15*time.Second)
				case "renew":
					got, accepted, err = client.Renew(context.Background(), lease, 15*time.Second)
				case "release":
					got, accepted, err = client.Release(context.Background(), lease)
				case "status":
					got, err = client.Status(context.Background(), lease.ClusterID)
				}
				if status != 200 && status != 409 {
					require.ErrorContains(t, err, fmt.Sprintf("status %d", status))
					require.False(t, accepted)
					require.Zero(t, got)
					return
				}
				require.NoError(t, err)
				require.Equal(t, lease.Generation, got.Generation)
				require.Equal(t, lease.HolderNodeID, got.HolderNodeID)
				require.Equal(t, lease.LeaseID, got.LeaseID)
				require.Positive(t, got.Remaining)
				require.Less(t, got.Remaining, 15*time.Second)
				if operation != "status" {
					require.Equal(t, status == 200, accepted)
				}
			})
		}
	}
}

func TestExecutionNodeWitnessClientRejectsInvalidResponses(t *testing.T) {
	for _, body := range []string{
		`{`, strings.Repeat(" ", executionNodeWitnessMaxResponseBytes) + `{}`,
		`{"active":true,"remaining_ms":0}`, `{"active":true,"remaining_ms":60001}`,
	} {
		t.Run(fmt.Sprintf("body_length_%d_%s", len(body), strings.TrimSpace(body)[:1]), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(body)) }))
			defer server.Close()
			_, accepted, err := newWitnessHTTPTestClient(t, server.URL).Acquire(context.Background(), "cluster-1", "api", "lease-1", 15*time.Second)
			require.Error(t, err)
			require.False(t, accepted)
		})
	}
}

func TestExecutionNodeWitnessClientTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer server.Close()
	client := newWitnessHTTPTestClient(t, server.URL)
	client.client.Timeout = 30 * time.Millisecond
	_, err := client.Status(context.Background(), "cluster-1")
	require.Error(t, err)
	var timeout interface{ Timeout() bool }
	require.ErrorAs(t, err, &timeout)
	require.True(t, timeout.Timeout())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = client.Status(ctx, "cluster-1")
	require.ErrorIs(t, err, context.Canceled)
}

func TestExecutionNodeWitnessClientRealServerLifecycle(t *testing.T) {
	store, err := witness.Open(filepath.Join(t.TempDir(), "witness.db"))
	require.NoError(t, err)
	defer func() { require.NoError(t, store.Close()) }()
	handler, err := witness.NewServer(store, witnessClientTestToken)
	require.NoError(t, err)
	server := httptest.NewServer(handler.Handler())
	defer server.Close()
	client := newWitnessHTTPTestClient(t, server.URL)
	ctx := context.Background()
	first, acquired, err := client.Acquire(ctx, "cluster-1", "api", "process-1", 15*time.Second)
	require.NoError(t, err)
	require.True(t, acquired)
	denied, acquired, err := client.Acquire(ctx, "cluster-1", "api2", "process-2", 15*time.Second)
	require.NoError(t, err)
	require.False(t, acquired)
	require.Equal(t, first.Generation, denied.Generation)
	require.Equal(t, first.HolderNodeID, denied.HolderNodeID)
	renewed, ok, err := client.Renew(ctx, first, 15*time.Second)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, first.Generation, renewed.Generation)
	released, ok, err := client.Release(ctx, renewed)
	require.NoError(t, err)
	require.True(t, ok)
	require.False(t, released.Active)
	second, acquired, err := client.Acquire(ctx, "cluster-1", "api2", "process-2", 15*time.Second)
	require.NoError(t, err)
	require.True(t, acquired)
	require.Greater(t, second.Generation, first.Generation)
	for _, release := range []bool{false, true} {
		var got service.ExecutionNodeWitnessLease
		if release {
			got, ok, err = client.Release(ctx, first)
		} else {
			got, ok, err = client.Renew(ctx, first, 15*time.Second)
		}
		require.NoError(t, err)
		require.False(t, ok)
		require.Equal(t, second.Generation, got.Generation)
		require.Equal(t, second.HolderNodeID, got.HolderNodeID)
	}
	client.token = "incorrect-synthetic-token"
	_, err = client.Status(ctx, "cluster-1")
	require.ErrorContains(t, err, "status 401")
}
