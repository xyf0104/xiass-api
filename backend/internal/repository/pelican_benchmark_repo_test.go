package repository

import (
	"context"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/benchmark"
	"github.com/Wei-Shaw/sub2api/migrations"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestIQMigrationUsesTransactionalExecutionMode(t *testing.T) {
	const name = "274_iq_benchmarks.sql"
	content, err := migrations.FS.ReadFile(name)
	require.NoError(t, err)
	nonTx, err := validateMigrationExecutionMode(name, string(content))
	require.NoError(t, err)
	require.False(t, nonTx)
}

func TestPelicanRepositoryMetadataQueriesDoNotSelectHTML(t *testing.T) {
	db, mock := newSQLMock(t)
	r := NewPelicanBenchmarkRepository(db)
	mock.ExpectQuery(`SELECT COUNT\(\*\) FROM pelican_benchmarks`).WithArgs(int64(0), "", "").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT "+pelicanColumns+" FROM pelican_benchmarks")).WithArgs(int64(0), "", "", 20, 0).WillReturnRows(sqlmock.NewRows([]string{"id"}))
	page, err := r.List(context.Background(), benchmark.Filter{})
	require.NoError(t, err)
	require.Empty(t, page.Items)
	require.NotContains(t, strings.ReplaceAll(pelicanColumns, "octet_length(html)", ""), "html")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestIQRepositoryPairedPaginationCountsGroups(t *testing.T) {
	db, mock := newSQLMock(t)
	mock.ExpectQuery(`SELECT COUNT\(\*\) FROM \(SELECT batch_id,account_id,model,MAX\(created_at\).*GROUP BY batch_id,account_id,model\) AS pairs`).
		WithArgs(int64(0), "", "succeeded").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(3))
	mock.ExpectQuery(`WITH pairs AS .*GROUP BY batch_id,account_id,model ORDER BY newest DESC,batch_id DESC,account_id,model LIMIT \$4 OFFSET \$5\).*WHERE \(batch_id,account_id,model\) IN`).
		WithArgs(int64(0), "", "succeeded", 1, 1).WillReturnRows(sqlmock.NewRows([]string{"id"}))
	page, err := NewPelicanBenchmarkRepository(db).List(context.Background(), benchmark.Filter{Status: "succeeded", Page: 2, PageSize: 1, Paired: true})
	require.NoError(t, err)
	require.Equal(t, int64(3), page.Total)
	require.Equal(t, 1, page.PageSize)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPelicanRepositoryRejectsOversizeBeforeSQL(t *testing.T) {
	db, mock := newSQLMock(t)
	err := NewPelicanBenchmarkRepository(db).Finish(context.Background(), uuid.NewString(), uuid.NewString(), "succeeded", benchmark.Output{HTML: strings.Repeat("x", benchmark.MaxHTMLBytes+1)})
	require.ErrorIs(t, err, benchmark.ErrTooLarge)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPelicanRepositoryStopsWithoutReleasingRunningGuard(t *testing.T) {
	db, mock := newSQLMock(t)
	mock.ExpectExec(`UPDATE pelican_benchmarks SET\s+status=CASE WHEN status='queued' THEN 'canceled' ELSE 'canceling' END`).WithArgs("", int64(7)).WillReturnResult(sqlmock.NewResult(0, 1))
	n, err := NewPelicanBenchmarkRepository(db).Stop(context.Background(), "", 7)
	require.NoError(t, err)
	require.Equal(t, int64(1), n)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestIQRepositoryFinishGradesOnlySuccessfulCandyAndCancellationWins(t *testing.T) {
	for _, tc := range []struct {
		text   string
		answer any
		passed bool
	}{
		{"最终答案：21", 21, true},
		{"推理里提到21\n最终答案：22", 22, false},
		{"推理里提到21但无结论", nil, false},
	} {
		db, mock := newSQLMock(t)
		// The SQL uses the old row's status to make cancellation win and prevents
		// partial text from failed requests or HTML tasks receiving a grade.
		mock.ExpectExec(`UPDATE pelican_benchmarks SET\s+status=CASE WHEN status='canceling' THEN 'canceled'.*answer=CASE WHEN test_type='candy' AND status='running' AND \$3='succeeded' THEN \$8::integer ELSE NULL END,\s+passed=CASE WHEN test_type='candy' AND status='running' AND \$3='succeeded' THEN \$9::boolean ELSE NULL END,.*WHERE id=\$1 AND executor_id=\$2 AND status IN \('running','canceling'\)`).
			WithArgs("task", "owner", "succeeded", "", "", "", tc.text, tc.answer, tc.passed).
			WillReturnResult(sqlmock.NewResult(0, 1))
		err := NewPelicanBenchmarkRepository(db).Finish(context.Background(), "task", "owner", "succeeded", benchmark.Output{TextOutput: tc.text})
		require.NoError(t, err)
		require.NoError(t, mock.ExpectationsWereMet())
	}
}

func TestIQRepositoryTextLimitRejectedBeforeSQL(t *testing.T) {
	db, mock := newSQLMock(t)
	err := NewPelicanBenchmarkRepository(db).Finish(context.Background(), "task", "owner", "succeeded", benchmark.Output{TextOutput: strings.Repeat("x", benchmark.MaxHTMLBytes+1)})
	require.ErrorIs(t, err, benchmark.ErrTooLarge)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestIQRepositoryListScansGradingWithoutPromptOrOutput(t *testing.T) {
	db, mock := newSQLMock(t)
	mock.ExpectQuery(`SELECT COUNT\(\*\) FROM pelican_benchmarks`).WithArgs(int64(0), "", "").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	columns := []string{"id", "batch_id", "account_id", "account_name", "model", "upstream_model", "status", "error_code", "created_at", "started_at", "finished_at", "duration_ms", "html_bytes", "execution_node_id", "source_id", "action", "error_message", "test_type", "answer", "passed"}
	rows := sqlmock.NewRows(columns).AddRow("id", "batch", int64(1), "account", "model", "upstream", "succeeded", "", time.Now(), nil, nil, int64(10), 0, "", "", "", "", "candy", 22, false)
	mock.ExpectQuery(regexp.QuoteMeta("SELECT "+pelicanColumns+" FROM pelican_benchmarks")).WithArgs(int64(0), "", "", 20, 0).WillReturnRows(rows)
	page, err := NewPelicanBenchmarkRepository(db).List(context.Background(), benchmark.Filter{})
	require.NoError(t, err)
	require.Len(t, page.Items, 1)
	require.Equal(t, benchmark.TestCandy, page.Items[0].TestType)
	require.NotNil(t, page.Items[0].Answer)
	require.Equal(t, 22, *page.Items[0].Answer)
	require.NotNil(t, page.Items[0].Passed)
	require.False(t, *page.Items[0].Passed)
	require.Empty(t, page.Items[0].Prompt)
	require.NotContains(t, pelicanColumns, "prompt")
	require.NotContains(t, pelicanColumns, "text_output")
	require.NoError(t, mock.ExpectationsWereMet())
}
