package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/Wei-Shaw/sub2api/internal/benchmark"
)

type PelicanBenchmarkRepository struct{ db *sql.DB }

func NewPelicanBenchmarkRepository(db *sql.DB) benchmark.Store {
	return &PelicanBenchmarkRepository{db: db}
}

// Keep the result column out of every metadata query, including RETURNING.
const pelicanColumns = `id::text, batch_id::text, account_id, account_name, model, upstream_model,
status, error_code, created_at, started_at, finished_at, duration_ms, octet_length(html),
execution_node_id, COALESCE(source_id::text,''), action`

type pelicanScanner interface{ Scan(...any) error }

func scanPelican(row pelicanScanner, html *string) (*benchmark.Task, error) {
	var task benchmark.Task
	args := []any{&task.ID, &task.BatchID, &task.AccountID, &task.AccountName, &task.Model,
		&task.UpstreamModel, &task.Status, &task.ErrorCode, &task.CreatedAt, &task.StartedAt,
		&task.FinishedAt, &task.DurationMS, &task.HTMLBytes, &task.ExecutionNodeID, &task.SourceID, &task.Action}
	if html != nil {
		args = append(args, html)
	}
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
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = tx.Rollback() }()
	for _, task := range tasks {
		row := tx.QueryRowContext(ctx, `INSERT INTO pelican_benchmarks (id,batch_id,account_id,account_name,model,execution_node_id,source_id,action)
VALUES ($1,$2,$3,$4,$5,$6,NULLIF($7,'')::uuid,$8) ON CONFLICT (account_id) WHERE status IN ('queued','running','canceling')
DO NOTHING RETURNING `+pelicanColumns, task.ID, task.BatchID, task.AccountID, task.AccountName, task.Model, task.ExecutionNodeID, task.SourceID, task.Action)
		inserted, err := scanPelican(row, nil)
		if errors.Is(err, benchmark.ErrNotFound) {
			skipped = append(skipped, benchmark.Skipped{AccountID: task.AccountID, Reason: "account_busy"})
			continue
		}
		if err != nil {
			return nil, nil, err
		}
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
LIMIT 1 FOR UPDATE SKIP LOCKED) AND status='queued' RETURNING `+pelicanColumns, owner)
	task, err := scanPelican(row, nil)
	if errors.Is(err, benchmark.ErrNotFound) {
		return nil, nil
	}
	return task, err
}

func (r *PelicanBenchmarkRepository) Get(ctx context.Context, id string) (*benchmark.Task, error) {
	return scanPelican(r.db.QueryRowContext(ctx, `SELECT `+pelicanColumns+` FROM pelican_benchmarks WHERE id=$1`, id), nil)
}

func (r *PelicanBenchmarkRepository) Detail(ctx context.Context, id string) (*benchmark.Detail, error) {
	var html string
	task, err := scanPelican(r.db.QueryRowContext(ctx, `SELECT `+pelicanColumns+`,html FROM pelican_benchmarks WHERE id=$1`, id), &html)
	if err != nil {
		return nil, err
	}
	return &benchmark.Detail{Task: *task, HTML: html}, nil
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
		task, err := scanPelican(rows, nil)
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

func (r *PelicanBenchmarkRepository) Finish(ctx context.Context, id, owner, status, code, html string) error {
	if status != "succeeded" && status != "failed" && status != "interrupted" {
		return fmt.Errorf("invalid terminal status")
	}
	if len(html) > benchmark.MaxHTMLBytes {
		return benchmark.ErrTooLarge
	}
	// A concurrent stop wins over completion. The owner CAS also makes a retry
	// harmless and prevents another process from acknowledging a running call.
	_, err := r.db.ExecContext(ctx, `UPDATE pelican_benchmarks SET
status=CASE WHEN status='canceling' THEN 'canceled' ELSE $3 END,
error_code=CASE WHEN status='canceling' THEN '' ELSE $4 END,
html=$5,
finished_at=clock_timestamp(),
duration_ms=GREATEST(0,FLOOR(EXTRACT(EPOCH FROM (clock_timestamp()-started_at))*1000))::bigint
WHERE id=$1 AND executor_id=$2 AND status IN ('running','canceling')`, id, owner, status, code, html)
	return err
}

func (r *PelicanBenchmarkRepository) Stop(ctx context.Context, id string, accountID int64) (int64, error) {
	// Queued jobs have no upstream call and may finish immediately. Running
	// jobs keep their unique-account slot until the executor acknowledges exit.
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
