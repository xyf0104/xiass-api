//go:build integration

package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/benchmark"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func pelicanIntegrationDB(t *testing.T) *sql.DB {
	t.Helper()
	schema := "pelican_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	_, err := integrationDB.Exec(`CREATE SCHEMA ` + schema)
	require.NoError(t, err)
	u, err := url.Parse(integrationPostgresDSN)
	require.NoError(t, err)
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	db, err := sql.Open("postgres", u.String())
	require.NoError(t, err)
	_, err = db.Exec(`CREATE TABLE pelican_benchmarks (LIKE public.pelican_benchmarks INCLUDING ALL)`)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, db.Close())
		_, err := integrationDB.Exec(`DROP SCHEMA ` + schema + ` CASCADE`)
		require.NoError(t, err)
	})
	return db
}

func TestPelicanDatabaseConcurrentCreateClaimAndCancellationGuard(t *testing.T) {
	db := pelicanIntegrationDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	const parallel = 24
	createdCh := make(chan benchmark.Task, parallel)
	errCh := make(chan error, parallel)
	var wg sync.WaitGroup
	for i := 0; i < parallel; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// Independent repository objects model different nodes sharing PostgreSQL.
			r := NewPelicanBenchmarkRepository(db)
			tasks, _, err := r.Create(ctx, []benchmark.Task{{ID: uuid.NewString(), BatchID: uuid.NewString(), AccountID: 10, AccountName: "synthetic", Model: benchmark.DefaultModel}})
			if err != nil {
				errCh <- err
				return
			}
			for _, task := range tasks {
				createdCh <- task
			}
		}()
	}
	wg.Wait()
	close(createdCh)
	close(errCh)
	for err := range errCh {
		require.NoError(t, err)
	}
	var created []benchmark.Task
	for task := range createdCh {
		created = append(created, task)
	}
	require.Len(t, created, 1, "cross-node unique guard must admit exactly one job")
	type claimed struct {
		task  *benchmark.Task
		owner string
		err   error
	}
	claims := make(chan claimed, parallel)
	for i := 0; i < parallel; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			owner := uuid.NewString()
			task, err := NewPelicanBenchmarkRepository(db).Claim(ctx, owner)
			claims <- claimed{task, owner, err}
		}()
	}
	wg.Wait()
	close(claims)
	var winner claimed
	count := 0
	for claim := range claims {
		require.NoError(t, claim.err)
		if claim.task != nil {
			winner = claim
			count++
		}
	}
	require.Equal(t, 1, count, "SKIP LOCKED + conditional UPDATE must claim once")
	r := NewPelicanBenchmarkRepository(db)
	require.NoError(t, r.SetUpstreamModel(ctx, winner.task.ID, winner.owner, "gpt-6-astra"))
	_, err := r.Stop(ctx, winner.task.ID, 0)
	require.NoError(t, err)
	current, err := r.Get(ctx, winner.task.ID)
	require.NoError(t, err)
	require.Equal(t, "canceling", current.Status)
	require.Nil(t, current.FinishedAt)
	next := benchmark.Task{ID: uuid.NewString(), BatchID: uuid.NewString(), AccountID: 10, AccountName: "synthetic", Model: benchmark.DefaultModel}
	tasks, skipped, err := r.Create(ctx, []benchmark.Task{next})
	require.NoError(t, err)
	require.Empty(t, tasks)
	require.Equal(t, "account_busy", skipped[0].Reason)
	require.ErrorIs(t, r.SetUpstreamModel(ctx, winner.task.ID, winner.owner, "late-model"), benchmark.ErrStopped)
	require.NoError(t, r.Finish(ctx, winner.task.ID, uuid.NewString(), "succeeded", "", "<html>wrong owner</html>"))
	current, err = r.Get(ctx, winner.task.ID)
	require.NoError(t, err)
	require.Equal(t, "canceling", current.Status)
	require.NoError(t, r.Finish(ctx, winner.task.ID, winner.owner, "succeeded", "", "<html>late success</html>"))
	detail, err := r.Detail(ctx, winner.task.ID)
	require.NoError(t, err)
	require.Equal(t, "canceled", detail.Status)
	require.Equal(t, "<html>late success</html>", detail.HTML, "canceled output is retained as continuation context, never marked successful")
	require.NotNil(t, detail.FinishedAt)
	require.NotNil(t, detail.DurationMS)
	tasks, _, err = r.Create(ctx, []benchmark.Task{next})
	require.NoError(t, err)
	require.Len(t, tasks, 1)
	_, err = r.Stop(ctx, "", 0)
	require.NoError(t, err)
	queued, err := r.Get(ctx, next.ID)
	require.NoError(t, err)
	require.Equal(t, "canceled", queued.Status)
	require.Nil(t, queued.StartedAt)
	require.EqualValues(t, 0, *queued.DurationMS)
}

func TestPelicanDatabaseFollowupMetadataAndSuccessfulFilter(t *testing.T) {
	db := pelicanIntegrationDB(t)
	r := NewPelicanBenchmarkRepository(db)
	ctx := context.Background()
	source := benchmark.Task{ID: uuid.NewString(), BatchID: uuid.NewString(), AccountID: 7, AccountName: "test", Model: benchmark.DefaultModel, ExecutionNodeID: "api2"}
	_, _, err := r.Create(ctx, []benchmark.Task{source})
	require.NoError(t, err)
	owner := uuid.NewString()
	_, err = r.Claim(ctx, owner)
	require.NoError(t, err)
	require.NoError(t, r.Finish(ctx, source.ID, owner, "failed", "upstream_incomplete", "<html>partial"))
	continued := source
	continued.ID, continued.SourceID, continued.Action = uuid.NewString(), source.ID, "continue"
	created, _, err := r.Create(ctx, []benchmark.Task{continued})
	require.NoError(t, err)
	require.Len(t, created, 1)
	require.Equal(t, "api2", created[0].ExecutionNodeID)
	require.Equal(t, source.ID, created[0].SourceID)
	require.Equal(t, "continue", created[0].Action)
	_, err = r.Claim(ctx, owner)
	require.NoError(t, err)
	require.NoError(t, r.Finish(ctx, continued.ID, owner, "succeeded", "", "<html>complete</html>"))
	page, err := r.List(ctx, benchmark.Filter{Status: "succeeded", PageSize: 1})
	require.NoError(t, err)
	require.EqualValues(t, 1, page.Total)
	require.Equal(t, continued.ID, page.Items[0].ID)
	old, err := r.Detail(ctx, source.ID)
	require.NoError(t, err)
	require.Equal(t, "<html>partial", old.HTML)
}

func TestPelicanDatabaseHTMLLimitMetadataAndDurableDetail(t *testing.T) {
	db := pelicanIntegrationDB(t)
	r := NewPelicanBenchmarkRepository(db)
	ctx := context.Background()
	task := benchmark.Task{ID: uuid.NewString(), BatchID: uuid.NewString(), AccountID: 1, AccountName: "test", Model: benchmark.DefaultModel}
	_, _, err := r.Create(ctx, []benchmark.Task{task})
	require.NoError(t, err)
	owner := uuid.NewString()
	_, err = r.Claim(ctx, owner)
	require.NoError(t, err)
	html := "<html><script>untrusted()</script></html>"
	require.NoError(t, r.Finish(ctx, task.ID, owner, "succeeded", "", html))
	detail, err := NewPelicanBenchmarkRepository(db).Detail(ctx, task.ID)
	require.NoError(t, err)
	require.Equal(t, html, detail.HTML)
	page, err := r.List(ctx, benchmark.Filter{BatchID: task.BatchID})
	require.NoError(t, err)
	require.Len(t, page.Items, 1)
	encoded, err := json.Marshal(page)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "untrusted")
	_, err = db.Exec(`UPDATE pelican_benchmarks SET html=$1 WHERE id=$2`, strings.Repeat("x", benchmark.MaxHTMLBytes+1), task.ID)
	require.Error(t, err, "database must enforce limit independently of Go validation")
}
