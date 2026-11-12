package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"

	"github.com/Wei-Shaw/sub2api/internal/benchmark"
)

type PelicanBenchmarkRepository struct{ db *sql.DB }

func NewPelicanBenchmarkRepository(db *sql.DB) benchmark.Store {
	return &PelicanBenchmarkRepository{db: db}
}

// Keep the result column out of every metadata query, including RETURNING.
const pelicanColumns = `id::text, batch_id::text, account_id, account_name, model, upstream_model,
status, error_code, created_at, started_at, finished_at, duration_ms, octet_length(html),
execution_node_id, COALESCE(source_id::text,''), action, error_message, test_type, answer, passed`

type pelicanScanner interface{ Scan(...any) error }

func scanPelican(row pelicanScanner, extra ...any) (*benchmark.Task, error) {
	var task benchmark.Task
	args := []any{&task.ID, &task.BatchID, &task.AccountID, &task.AccountName, &task.Model,
		&task.UpstreamModel, &task.Status, &task.ErrorCode, &task.CreatedAt, &task.StartedAt,
		&task.FinishedAt, &task.DurationMS, &task.HTMLBytes, &task.ExecutionNodeID, &task.SourceID, &task.Action, &task.ErrorMessage,
		&task.TestType, &task.Answer, &task.Passed}
	args = append(args, extra...)
	if err := row.Scan(args...); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, benchmark.ErrNotFound
		}
		return nil, err
	}
	task.CreatedAt = task.CreatedAt.UTC()
	if task.StartedAt != nil {
		t := task.StartedAt.UTC()
		task.StartedAt = &t
	}
	if task.FinishedAt != nil {
		t := task.FinishedAt.UTC()
		task.FinishedAt = &t
	}
	return &task, nil
}

func (r *PelicanBenchmarkRepository) Create(ctx context.Context, tasks []benchmark.Task) ([]benchmark.Task, []benchmark.Skipped, error) {
	created, skipped := []benchmark.Task{}, []benchmark.Skipped{}
	// All callers acquire unique-index slots in the same order, even when their
	// UI selected the tests in a different order. Do not mutate the caller's slice.
	tasks = append([]benchmark.Task(nil), tasks...)
	sort.SliceStable(tasks, func(i, j int) bool {
		if tasks[i].AccountID == tasks[j].AccountID {
			return benchmark.NormalizeTestType(tasks[i].TestType) < benchmark.NormalizeTestType(tasks[j].TestType)
		}
		return tasks[i].AccountID < tasks[j].AccountID
	})
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = tx.Rollback() }()
	for _, task := range tasks {
		task.TestType = benchmark.NormalizeTestType(task.TestType)
		if !benchmark.ValidTestType(task.TestType) || !benchmark.ValidPrompt(task.Prompt) || (task.TestType == benchmark.TestCandy && task.Action == "continue") {
			return nil, nil, fmt.Errorf("invalid benchmark test or prompt")
		}
		task.Prompt = benchmark.SnapshotPrompt(task.TestType, task.Prompt)
		row := tx.QueryRowContext(ctx, `INSERT INTO pelican_benchmarks (id,batch_id,account_id,account_name,model,execution_node_id,source_id,action,test_type,prompt)
VALUES ($1,$2,$3,$4,$5,$6,NULLIF($7,'')::uuid,$8,$9,$10) ON CONFLICT (account_id,test_type) WHERE status IN ('queued','running','canceling')
DO NOTHING RETURNING `+pelicanColumns, task.ID, task.BatchID, task.AccountID, task.AccountName, task.Model, task.ExecutionNodeID, task.SourceID, task.Action, task.TestType, task.Prompt)
		inserted, err := scanPelican(row)
		if errors.Is(err, benchmark.ErrNotFound) {
			skipped = append(skipped, benchmark.Skipped{AccountID: task.AccountID, Reason: "account_busy"})
			continue
		}
		if err != nil {
			return nil, nil, err
		}
		inserted.Prompt = task.Prompt
		created = append(created, *inserted)
	}
	if err := tx.Commit(); err != nil {
		return nil, nil, err
	}
	return created, skipped, nil
}

func (r *PelicanBenchmarkRepository) Claim(ctx context.Context, owner string) (*benchmark.Task, error) {
	row := r.db.QueryRowContext(ctx, `UPDATE pelican_benchmarks SET status='running',
executor_id=$1, started_at=clock_timestamp() WHERE id=(
SELECT id FROM pelican_benchmarks WHERE status='queued' ORDER BY created_at,id
LIMIT 1 FOR UPDATE SKIP LOCKED) AND status='queued' RETURNING `+pelicanColumns+`,prompt`, owner)
	var prompt string
	task, err := scanPelican(row, &prompt)
	if errors.Is(err, benchmark.ErrNotFound) {
		return nil, nil
	}
	if task != nil {
		task.Prompt = benchmark.SnapshotPrompt(task.TestType, prompt)
	}
	return task, err
}

func (r *PelicanBenchmarkRepository) Get(ctx context.Context, id string) (*benchmark.Task, error) {
	return scanPelican(r.db.QueryRowContext(ctx, `SELECT `+pelicanColumns+` FROM pelican_benchmarks WHERE id=$1`, id))
}

func (r *PelicanBenchmarkRepository) Detail(ctx context.Context, id string) (*benchmark.Detail, error) {
	var html, prompt, text string
	task, err := scanPelican(r.db.QueryRowContext(ctx, `SELECT `+pelicanColumns+`,html,prompt,text_output FROM pelican_benchmarks WHERE id=$1`, id), &html, &prompt, &text)
	if err != nil {
		return nil, err
	}
	task.Prompt = benchmark.SnapshotPrompt(task.TestType, prompt)
	return &benchmark.Detail{Task: *task, HTML: html, TextOutput: text}, nil
}

func (r *PelicanBenchmarkRepository) List(ctx context.Context, f benchmark.Filter) (*benchmark.Page, error) {
	if f.Page < 1 {
		f.Page = 1
	}
	if f.PageSize < 1 {
		f.PageSize = 20
	}
	if f.PageSize > 100 {
		f.PageSize = 100
	}
	where := ` WHERE ($1::bigint=0 OR account_id=$1) AND ($2='' OR batch_id::text=$2) AND ($3='' OR status=$3)`
	args := []any{f.AccountID, f.BatchID, f.Status}
	page := &benchmark.Page{Items: []benchmark.Task{}, Page: f.Page, PageSize: f.PageSize}
	if err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM pelican_benchmarks`+where, args...).Scan(&page.Total); err != nil {
		return nil, err
	}
	args = append(args, f.PageSize, (f.Page-1)*f.PageSize)
	rows, err := r.db.QueryContext(ctx, `SELECT `+pelicanColumns+` FROM pelican_benchmarks`+where+` ORDER BY created_at DESC,id DESC LIMIT $4 OFFSET $5`, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		task, err := scanPelican(rows)
		if err != nil {
			return nil, err
		}
		page.Items = append(page.Items, *task)
	}
	return page, rows.Err()
}

func (r *PelicanBenchmarkRepository) SetUpstreamModel(ctx context.Context, id, owner, model string) error {
	if !benchmark.ValidModel(model) {
		return fmt.Errorf("invalid upstream model")
	}
	result, err := r.db.ExecContext(ctx, `UPDATE pelican_benchmarks SET upstream_model=$3
WHERE id=$1 AND executor_id=$2 AND status='running'`, id, owner, model)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err == nil && n != 1 {
		return benchmark.ErrStopped
	}
	return err
}

func (r *PelicanBenchmarkRepository) Finish(ctx context.Context, id, owner, status string, output benchmark.Output) error {
	if status != "succeeded" && status != "failed" && status != "interrupted" {
		return fmt.Errorf("invalid terminal status")
	}
	if len(output.HTML) > benchmark.MaxHTMLBytes || len(output.TextOutput) > benchmark.MaxHTMLBytes {
		return benchmark.ErrTooLarge
	}
	answer, passed := benchmark.GradeCandy(output.TextOutput)
	// A concurrent stop wins over completion. The owner CAS also makes a retry
	// harmless and prevents another process from acknowledging a running call.
	_, err := r.db.ExecContext(ctx, `UPDATE pelican_benchmarks SET
status=CASE WHEN status='canceling' THEN 'canceled' ELSE $3 END,
error_code=CASE WHEN status='canceling' THEN '' ELSE $4 END,
error_message=CASE WHEN status='canceling' THEN '' ELSE $6 END,
html=CASE WHEN test_type='candy' THEN '' ELSE $5 END,
text_output=CASE WHEN test_type='candy' THEN $7 ELSE '' END,
answer=CASE WHEN test_type='candy' AND status='running' AND $3='succeeded' THEN $8::integer ELSE NULL END,
passed=CASE WHEN test_type='candy' AND status='running' AND $3='succeeded' THEN $9::boolean ELSE NULL END,
finished_at=clock_timestamp(),
duration_ms=GREATEST(0,FLOOR(EXTRACT(EPOCH FROM (clock_timestamp()-started_at))*1000))::bigint
WHERE id=$1 AND executor_id=$2 AND status IN ('running','canceling')`, id, owner, status, output.ErrorCode, output.HTML, output.ErrorMessage, output.TextOutput, answer, passed)
	return err
}

func (r *PelicanBenchmarkRepository) Stop(ctx context.Context, id string, accountID int64) (int64, error) {
	// Queued jobs have no upstream call and may finish immediately. Running
	// jobs keep their account/test-type slot until the executor acknowledges exit.
	result, err := r.db.ExecContext(ctx, `UPDATE pelican_benchmarks SET
status=CASE WHEN status='queued' THEN 'canceled' ELSE 'canceling' END,
finished_at=CASE WHEN status='queued' THEN clock_timestamp() ELSE NULL END,
duration_ms=CASE WHEN status='queued' THEN 0 ELSE NULL END
WHERE ($1='' OR id::text=$1) AND ($2::bigint=0 OR account_id=$2)
AND status IN ('queued','running')`, id, accountID)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

var _ benchmark.Store = (*PelicanBenchmarkRepository)(nil)

// Clear only terminal output. Active continuations pin their source until
// execution has finished, so retention never interrupts an upstream request.
func (r *PelicanBenchmarkRepository) PurgeExpiredHTML(ctx context.Context) error {
	for {
		result, err := r.db.ExecContext(ctx, `UPDATE pelican_benchmarks SET html='',text_output='' WHERE id IN (
SELECT old.id FROM pelican_benchmarks old
WHERE old.status IN ('succeeded','failed','canceled','interrupted')
AND (old.html <> '' OR old.text_output <> '')
AND old.finished_at < NOW() - INTERVAL '72 hours'
AND NOT EXISTS (SELECT 1 FROM pelican_benchmarks active
WHERE active.source_id=old.id AND active.status IN ('queued','running','canceling'))
ORDER BY old.finished_at,old.id LIMIT 500 FOR UPDATE OF old SKIP LOCKED)`)
		if err != nil {
			return err
		}
		n, err := result.RowsAffected()
		if err != nil || n < 500 {
			return err
		}
	}
}
