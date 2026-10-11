package repository

import (
	"context"
	"regexp"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/DATA-DOG/go-sqlmock"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestSweepOneExpiredProxySkipsStaleSnapshot(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	t.Cleanup(func() { _ = client.Close() })
	now := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	expiresAt := now.Add(-time.Hour)
	backupID := int64(42)
	snapshot := service.Proxy{ID: 7, Status: service.StatusActive, ExpiresAt: &expiresAt, FallbackMode: service.FallbackModeProxy, BackupProxyID: &backupID}
	execExpectation := mock.ExpectExec(regexp.QuoteMeta("UPDATE proxies SET status=$1, updated_at=NOW()"))
	execExpectation.WithArgs(service.StatusExpired, snapshot.ID, service.StatusActive, now, expiresAt, snapshot.FallbackMode, backupID)
	execExpectation.WillReturnResult(sqlmock.NewResult(0, 0))
	repo := newProxyRepositoryWithSQL(client, db)
	changed, err := repo.sweepOneExpiredProxyOnExec(context.Background(), db, snapshot, now, &backupID, true, "")
	require.NoError(t, err)
	require.Empty(t, changed)
	require.NoError(t, mock.ExpectationsWereMet())
}
