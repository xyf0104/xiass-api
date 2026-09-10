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

func TestPelicanRetentionClearsOnlyExpiredTerminalHTML(t *testing.T) {
	db := pelicanIntegrationDB(t)
	ctx := context.Background()
	batch := uuid.NewString()
	old, recent, pinned, active := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	for i, row := range []struct{ id, status, age, source string }{
		{old, "succeeded", "73 hours", ""},
		{recent, "failed", "71 hours", ""},
		{pinned, "canceled", "80 hours", ""},
		{active, "running", "80 hours", pinned},
	} {
		_, err := db.ExecContext(ctx, `INSERT INTO pelican_benchmarks
(id,batch_id,account_id,account_name,model,status,finished_at,html,source_id)
VALUES ($1,$2,$3,'fixture','test',$4,NOW()-$5::interval,'<html>saved</html>',NULLIF($6,'')::uuid)`, row.id, batch, i+1, row.status, row.age, row.source)
		require.NoError(t, err)
	}
	repo := &PelicanBenchmarkRepository{db: db}
	require.NoError(t, repo.PurgeExpiredHTML(ctx))
	expired, err := repo.Detail(ctx, old)
	require.NoError(t, err)
	require.Empty(t, expired.HTML)
	require.Zero(t, expired.HTMLBytes)
	require.Equal(t, "succeeded", expired.Status)
	for _, id := range []string{recent, pinned, active} {
		detail, err := repo.Detail(ctx, id)
		require.NoError(t, err)
		require.NotEmpty(t, detail.HTML)
	}
	_, err = db.ExecContext(ctx, `UPDATE pelican_benchmarks SET status='succeeded',finished_at=NOW() WHERE id=$1`, active)
	require.NoError(t, err)
	require.NoError(t, repo.PurgeExpiredHTML(ctx))
	expired, err = repo.Detail(ctx, pinned)
	require.NoError(t, err)
	require.Empty(t, expired.HTML)
	_, err = repo.Detail(ctx, active)
	require.NoError(t, err)
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
	require.NoError(t, r.Finish(benchmark.WithErrorMessage(ctx, "Model capacity exhausted"), source.ID, owner, "failed", "upstream_http_503", "<html>partial"))
	detail, err := r.Detail(ctx, source.ID)
	require.NoError(t, err)
	require.Equal(t, "Model capacity exhausted", detail.ErrorMessage)
	require.Equal(t, "upstream_http_503", detail.ErrorCode)
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
