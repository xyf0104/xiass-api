//go:build integration

package repository

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

func executionNodeFenceTestRepository(t *testing.T) *settingRepository {
	t.Helper()
	schema := "witness_fence_" + uuid.New().String()[:8]
	_, err := integrationDB.Exec(`CREATE SCHEMA ` + schema)
	require.NoError(t, err)
	u, err := url.Parse(integrationPostgresDSN)
	require.NoError(t, err)
	query := u.Query()
	query.Set("search_path", schema)
	u.RawQuery = query.Encode()
	db, err := sql.Open("postgres", u.String())
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, db.Close())
		_, err := integrationDB.Exec(`DROP SCHEMA ` + schema + ` CASCADE`)
		require.NoError(t, err)
	})
	_, err = db.Exec(`CREATE TABLE settings (LIKE public.settings INCLUDING ALL)`)
	require.NoError(t, err)
	return &settingRepository{db: db}
}

func TestExecutionNodeFenceMonotonicAndHolderStable(t *testing.T) {
	repo := executionNodeFenceTestRepository(t)
	ctx := context.Background()
	_, err := repo.ReadExecutionNodeFence(ctx)
	require.ErrorIs(t, err, service.ErrSettingNotFound)
	first := service.ExecutionNodeFenceRecord{ClusterID: "cluster-1", HolderNodeID: "api", LeaseID: "process-1", Generation: 7, ExpiresAt: time.Now().UTC().Add(time.Minute)}
	ok, err := repo.CommitExecutionNodeFence(ctx, first)
	require.NoError(t, err)
	require.True(t, ok)
	first.ExpiresAt = first.ExpiresAt.Add(time.Second)
	ok, err = repo.CommitExecutionNodeFence(ctx, first)
	require.NoError(t, err)
	require.True(t, ok, "same identity can renew within its generation")
	baseline, err := repo.ReadExecutionNodeFence(ctx)
	require.NoError(t, err)
	require.Equal(t, first.ExpiresAt, baseline.ExpiresAt)
	for _, field := range []string{"generation", "holder", "lease", "cluster"} {
		t.Run(field, func(t *testing.T) {
			candidate := first
			switch field {
			case "generation":
				candidate.Generation--
			case "holder":
				candidate.HolderNodeID = "api2"
			case "lease":
				candidate.LeaseID = "process-2"
			case "cluster":
				candidate.ClusterID = "cluster-2"
				candidate.Generation++
			}
			ok, err := repo.CommitExecutionNodeFence(ctx, candidate)
			require.NoError(t, err)
			require.False(t, ok)
			got, err := repo.ReadExecutionNodeFence(ctx)
			require.NoError(t, err)
			require.Equal(t, baseline, got, "rejected writes must not mutate the fence")
		})
	}
	second := first
	second.Generation++
	second.HolderNodeID, second.LeaseID = "api2", "process-2"
	ok, err = repo.CommitExecutionNodeFence(ctx, second)
	require.NoError(t, err)
	require.True(t, ok)
	ok, err = repo.CommitExecutionNodeFence(ctx, first)
	require.NoError(t, err)
	require.False(t, ok)
	got, err := repo.ReadExecutionNodeFence(ctx)
	require.NoError(t, err)
	require.Equal(t, second.Generation, got.Generation)
	require.Equal(t, second.HolderNodeID, got.HolderNodeID)
}

func TestExecutionNodeFenceConcurrentSameGeneration(t *testing.T) {
	for _, existing := range []bool{false, true} {
		t.Run(fmt.Sprintf("existing_%t", existing), func(t *testing.T) {
			repo := executionNodeFenceTestRepository(t)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			base := service.ExecutionNodeFenceRecord{ClusterID: "cluster-1", HolderNodeID: "api", LeaseID: "base", Generation: 1, ExpiresAt: time.Now().UTC().Add(time.Minute)}
			if existing {
				ok, err := repo.CommitExecutionNodeFence(ctx, base)
				require.NoError(t, err)
				require.True(t, ok)
			}
			type result struct {
				record service.ExecutionNodeFenceRecord
				ok     bool
				err    error
			}
			results := make(chan result, 8)
			start := make(chan struct{})
			for i := 0; i < cap(results); i++ {
				candidate := base
				candidate.Generation = 2
				candidate.LeaseID = fmt.Sprintf("process-%d", i)
				go func() {
					<-start
					ok, err := repo.CommitExecutionNodeFence(ctx, candidate)
					results <- result{candidate, ok, err}
				}()
			}
			close(start)
			var winner service.ExecutionNodeFenceRecord
			accepted := 0
			for i := 0; i < cap(results); i++ {
				outcome := <-results
				if outcome.err != nil {
					var pgErr *pq.Error
					require.ErrorAs(t, outcome.err, &pgErr)
					require.Contains(t, []pq.ErrorCode{"40001", "23505"}, pgErr.Code)
					t.Logf("concurrent contender rejected with PostgreSQL %s", pgErr.Code)
				}
				if outcome.ok {
					accepted++
					winner = outcome.record
				}
			}
			require.Equal(t, 1, accepted)
			got, err := repo.ReadExecutionNodeFence(ctx)
			require.NoError(t, err)
			require.Equal(t, winner.Generation, got.Generation)
			require.Equal(t, winner.LeaseID, got.LeaseID)
		})
	}
}

func TestExecutionNodeFenceRejectsCorruptPersistedRecord(t *testing.T) {
	repo := executionNodeFenceTestRepository(t)
	ctx := context.Background()
	_, err := repo.db.Exec(`INSERT INTO settings (key, value, updated_at) VALUES ($1, $2, NOW())`, service.SettingKeyExecutionNodeFence, "not-json")
	require.NoError(t, err)
	_, err = repo.ReadExecutionNodeFence(ctx)
	require.ErrorContains(t, err, "decode")
	ok, err := repo.CommitExecutionNodeFence(ctx, service.ExecutionNodeFenceRecord{ClusterID: "cluster-1", HolderNodeID: "api", LeaseID: "process-1", Generation: 8, ExpiresAt: time.Now().Add(time.Minute)})
	require.ErrorContains(t, err, "decode")
	require.False(t, ok)
	var raw string
	require.NoError(t, repo.db.QueryRow(`SELECT value FROM settings WHERE key = $1`, service.SettingKeyExecutionNodeFence).Scan(&raw))
	require.Equal(t, "not-json", raw)
}
