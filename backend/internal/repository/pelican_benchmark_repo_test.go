package repository

import (
	"context"
	"regexp"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/benchmark"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

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

func TestPelicanRepositoryRejectsOversizeBeforeSQL(t *testing.T) {
	db, mock := newSQLMock(t)
	err := NewPelicanBenchmarkRepository(db).Finish(context.Background(), uuid.NewString(), uuid.NewString(), "succeeded", "", strings.Repeat("x", benchmark.MaxHTMLBytes+1))
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
