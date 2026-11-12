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
	"github.com/Wei-Shaw/sub2api/migrations"
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
	require.NoError(t, r.Finish(ctx, winner.task.ID, uuid.NewString(), "succeeded", benchmark.Output{HTML: "<html>wrong owner</html>"}))
	current, err = r.Get(ctx, winner.task.ID)
	require.NoError(t, err)
	require.Equal(t, "canceling", current.Status)
	require.NoError(t, r.Finish(ctx, winner.task.ID, winner.owner, "succeeded", benchmark.Output{HTML: "<html>late success</html>"}))
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
	require.NoError(t, r.Finish(ctx, source.ID, owner, "failed", benchmark.Output{ErrorCode: "upstream_http_503", ErrorMessage: "Model capacity exhausted", HTML: "<html>partial"}))
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
	require.NoError(t, r.Finish(ctx, continued.ID, owner, "succeeded", benchmark.Output{HTML: "<html>complete</html>"}))
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
	require.NoError(t, r.Finish(ctx, task.ID, owner, "succeeded", benchmark.Output{HTML: html}))
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

func TestIQDatabaseConcurrentTypesAndCancellationSiblingIsolation(t *testing.T) {
	db := pelicanIntegrationDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	const parallel = 16
	created := make(chan benchmark.Task, parallel*2)
	errors := make(chan error, parallel)
	var wg sync.WaitGroup
	for i := range parallel {
		wg.Add(1)
		go func() {
			defer wg.Done()
			batch := uuid.NewString()
			kinds := []string{benchmark.TestPelican, benchmark.TestCandy}
			if i%2 == 0 {
				kinds[0], kinds[1] = kinds[1], kinds[0]
			}
			var tasks []benchmark.Task
			for _, kind := range kinds {
				tasks = append(tasks, benchmark.Task{ID: uuid.NewString(), BatchID: batch, AccountID: 77, AccountName: "test", Model: benchmark.DefaultModel, TestType: kind, Prompt: benchmark.ResolvePrompt(kind, "custom HTML")})
			}
			items, _, err := NewPelicanBenchmarkRepository(db).Create(ctx, tasks)
			if err != nil {
				errors <- err
				return
			}
			for _, item := range items {
				created <- item
			}
		}()
	}
	wg.Wait()
	close(created)
	close(errors)
	for err := range errors {
		require.NoError(t, err)
	}
	var tasks []benchmark.Task
	for item := range created {
		tasks = append(tasks, item)
	}
	require.Len(t, tasks, 2, "each type must be admitted exactly once across processes")
	require.Equal(t, tasks[0].BatchID, tasks[1].BatchID, "one accepted batch must contain both tests")
	type claimed struct {
		task  *benchmark.Task
		owner string
		err   error
	}
	claims := make(chan claimed, parallel)
	for range parallel {
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
	byType := map[string]claimed{}
	for claim := range claims {
		require.NoError(t, claim.err)
		if claim.task != nil {
			require.NotContains(t, byType, claim.task.TestType)
			byType[claim.task.TestType] = claim
		}
	}
	require.Len(t, byType, 2, "both types must be running concurrently without finishing either")
	for _, claim := range byType {
		require.Equal(t, "running", claim.task.Status)
	}
	r := NewPelicanBenchmarkRepository(db)
	candy, html := byType[benchmark.TestCandy], byType[benchmark.TestPelican]
	require.Equal(t, benchmark.CandyPrompt, candy.task.Prompt)
	require.Equal(t, "custom HTML", html.task.Prompt)
	_, err := r.Stop(ctx, candy.task.ID, 77)
	require.NoError(t, err)
	htmlState, err := r.Get(ctx, html.task.ID)
	require.NoError(t, err)
	require.Equal(t, "running", htmlState.Status, "stopping one type must not stop its sibling")
	for _, kind := range []string{benchmark.TestCandy, benchmark.TestPelican} {
		items, skipped, err := r.Create(ctx, []benchmark.Task{{ID: uuid.NewString(), BatchID: uuid.NewString(), AccountID: 77, AccountName: "test", Model: benchmark.DefaultModel, TestType: kind}})
		require.NoError(t, err)
		require.Empty(t, items)
		require.Len(t, skipped, 1)
	}
	require.NoError(t, r.Finish(ctx, candy.task.ID, candy.owner, "succeeded", benchmark.Output{TextOutput: "最终答案：21"}))
	candyDetail, err := r.Detail(ctx, candy.task.ID)
	require.NoError(t, err)
	require.Equal(t, "canceled", candyDetail.Status)
	require.Nil(t, candyDetail.Answer)
	require.Nil(t, candyDetail.Passed)
	require.Empty(t, candyDetail.HTML)
	retry := benchmark.Task{ID: uuid.NewString(), BatchID: uuid.NewString(), AccountID: 77, AccountName: "test", Model: benchmark.DefaultModel, TestType: benchmark.TestCandy, SourceID: candy.task.ID, Action: "retry"}
	items, _, err := r.Create(ctx, []benchmark.Task{retry})
	require.NoError(t, err)
	require.Len(t, items, 1, "candy guard releases independently while HTML is still running")
	require.NoError(t, r.Finish(ctx, html.task.ID, html.owner, "succeeded", benchmark.Output{HTML: "<html>3D</html>"}))
	htmlDetail, err := r.Detail(ctx, html.task.ID)
	require.NoError(t, err)
	require.Nil(t, htmlDetail.Answer)
	require.Nil(t, htmlDetail.Passed)
	require.Equal(t, "<html>3D</html>", htmlDetail.HTML)
}

func TestIQDatabaseCandyGradingDurableAndNoGradeOnFailure(t *testing.T) {
	db := pelicanIntegrationDB(t)
	r := NewPelicanBenchmarkRepository(db)
	ctx := context.Background()
	for i, tc := range []struct {
		status, text string
		answer       *int
		passed       *bool
	}{
		{"succeeded", "最终答案：21", iqInt(21), iqBool(true)},
		{"succeeded", "最终答案：22", iqInt(22), iqBool(false)},
		{"succeeded", "推理有21，但没有最终结论", nil, iqBool(false)},
		{"failed", "最终答案：21", nil, nil},
		{"interrupted", "最终答案：21", nil, nil},
	} {
		task := benchmark.Task{ID: uuid.NewString(), BatchID: uuid.NewString(), AccountID: int64(i + 1), AccountName: "test", Model: benchmark.DefaultModel, TestType: benchmark.TestCandy}
		_, _, err := r.Create(ctx, []benchmark.Task{task})
		require.NoError(t, err)
		owner := uuid.NewString()
		_, err = r.Claim(ctx, owner)
		require.NoError(t, err)
		require.NoError(t, r.Finish(ctx, task.ID, owner, tc.status, benchmark.Output{TextOutput: tc.text, HTML: "discarded"}))
		detail, err := NewPelicanBenchmarkRepository(db).Detail(ctx, task.ID)
		require.NoError(t, err)
		require.Equal(t, tc.status, detail.Status)
		require.Equal(t, tc.answer, detail.Answer)
		require.Equal(t, tc.passed, detail.Passed)
		require.Equal(t, tc.text, detail.TextOutput)
		require.Empty(t, detail.HTML)
		require.Zero(t, detail.HTMLBytes)
		require.Equal(t, benchmark.CandyPrompt, detail.Prompt)
		// A retried finalization cannot overwrite the acknowledged result.
		require.NoError(t, r.Finish(ctx, task.ID, owner, "succeeded", benchmark.Output{TextOutput: "最终答案：99"}))
		got, err := r.Get(ctx, task.ID)
		require.NoError(t, err)
		require.Equal(t, tc.answer, got.Answer)
		require.Equal(t, tc.passed, got.Passed)
	}
	page, err := r.List(ctx, benchmark.Filter{})
	require.NoError(t, err)
	encoded, err := json.Marshal(page)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "text_output")
	require.NotContains(t, string(encoded), "prompt")
	require.Len(t, page.Items, 5)
}

func iqInt(v int) *int    { return &v }
func iqBool(v bool) *bool { return &v }

func TestIQDatabaseLegacyDefaultsLimitsAndCandyRetention(t *testing.T) {
	db := pelicanIntegrationDB(t)
	r := &PelicanBenchmarkRepository{db: db}
	ctx := context.Background()
	id := uuid.NewString()
	_, err := db.ExecContext(ctx, `INSERT INTO pelican_benchmarks (id,batch_id,account_id,account_name,model,status,html) VALUES ($1,$2,1,'legacy','test','succeeded','<html>old</html>')`, id, uuid.NewString())
	require.NoError(t, err)
	detail, err := r.Detail(ctx, id)
	require.NoError(t, err)
	require.Equal(t, benchmark.TestPelican, detail.TestType)
	require.Equal(t, benchmark.Prompt, detail.Prompt)
	require.Equal(t, "<html>old</html>", detail.HTML)
	require.Nil(t, detail.Answer)
	require.Nil(t, detail.Passed)
	for _, query := range []string{
		`UPDATE pelican_benchmarks SET prompt=repeat('a',8001) WHERE id=$1`,
		`UPDATE pelican_benchmarks SET text_output=repeat('a',1048577) WHERE id=$1`,
		`UPDATE pelican_benchmarks SET passed=true WHERE id=$1`,
	} {
		_, err := db.ExecContext(ctx, query, id)
		require.Error(t, err, "database enforces limits independently of the handler")
	}
	candy := uuid.NewString()
	_, err = db.ExecContext(ctx, `INSERT INTO pelican_benchmarks (id,batch_id,account_id,account_name,model,test_type,status,text_output,answer,passed,finished_at)
VALUES ($1,$2,2,'test','test','candy','succeeded','最终答案：21',21,true,NOW()-INTERVAL '73 hours')`, candy, uuid.NewString())
	require.NoError(t, err)
	require.NoError(t, r.PurgeExpiredHTML(ctx))
	detail, err = r.Detail(ctx, candy)
	require.NoError(t, err)
	require.Empty(t, detail.TextOutput)
	require.Equal(t, iqInt(21), detail.Answer)
	require.Equal(t, iqBool(true), detail.Passed)
}

func TestIQDatabaseMigrationPreservesExistingRunningAndSuccessfulHTML(t *testing.T) {
	db := pelicanIntegrationDB(t)
	ctx := context.Background()
	// Reconstruct only the benchmark table in this test's isolated schema.
	_, err := db.ExecContext(ctx, `DROP TABLE pelican_benchmarks`)
	require.NoError(t, err)
	for _, name := range []string{"242_pelican_benchmarks.sql", "243_pelican_benchmark_followups.sql", "244_pelican_benchmark_retention.sql", "245_pelican_benchmark_error_message.sql"} {
		migration, err := migrations.FS.ReadFile(name)
		require.NoError(t, err)
		_, err = db.ExecContext(ctx, string(migration))
		require.NoError(t, err)
	}
	batch := uuid.NewString()
	ids := []string{uuid.NewString(), uuid.NewString()}
	for i, status := range []string{"running", "succeeded"} {
		_, err := db.ExecContext(ctx, `INSERT INTO pelican_benchmarks (id,batch_id,account_id,account_name,model,status,html) VALUES ($1,$2,$3,'legacy','test',$4,'<html>preserved</html>')`, ids[i], batch, i+1, status)
		require.NoError(t, err)
	}
	migration, err := migrations.FS.ReadFile("274_iq_benchmarks.sql")
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, string(migration))
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, string(migration))
	require.NoError(t, err, "migration must remain idempotent")
	r := NewPelicanBenchmarkRepository(db)
	for _, id := range ids {
		detail, err := r.Detail(ctx, id)
		require.NoError(t, err)
		require.Equal(t, benchmark.TestPelican, detail.TestType)
		require.Equal(t, benchmark.Prompt, detail.Prompt)
		require.Equal(t, "<html>preserved</html>", detail.HTML)
		require.Nil(t, detail.Answer)
		require.Nil(t, detail.Passed)
	}
	created, skipped, err := r.Create(ctx, []benchmark.Task{
		{ID: uuid.NewString(), BatchID: uuid.NewString(), AccountID: 1, AccountName: "test", Model: benchmark.DefaultModel, TestType: benchmark.TestPelican},
		{ID: uuid.NewString(), BatchID: uuid.NewString(), AccountID: 1, AccountName: "test", Model: benchmark.DefaultModel, TestType: benchmark.TestCandy},
	})
	require.NoError(t, err)
	require.Len(t, created, 1)
	require.Equal(t, benchmark.TestCandy, created[0].TestType)
	require.Len(t, skipped, 1, "existing running HTML must still retain its type guard after migration")
}
