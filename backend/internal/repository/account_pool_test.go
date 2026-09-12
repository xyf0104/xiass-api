package repository

import (
	"context"
	"errors"
	"regexp"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/DATA-DOG/go-sqlmock"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

func accountPoolMockRepository(t *testing.T) (*accountRepository, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	t.Cleanup(func() { _ = client.Close() })
	return &accountRepository{client: client, sql: db}, mock
}

func TestAccountPoolRepositoryTransactionCommitsOnlyCompleteMutation(t *testing.T) {
	for _, outcome := range []string{"success", "callback error", "commit error", "short update"} {
		t.Run(outcome, func(t *testing.T) {
			r, mock := accountPoolMockRepository(t)
			mock.ExpectBegin()
			mock.ExpectExec(regexp.QuoteMeta("SELECT pg_advisory_xact_lock(246, 1)")).WillReturnResult(sqlmock.NewResult(0, 1))
			count := int64(1)
			if outcome == "short update" {
				count = 0
			}
			mock.ExpectExec(`UPDATE accounts SET extra = COALESCE\(extra, '\{\}'::jsonb\) \|\| \$1::jsonb, updated_at = NOW\(\) WHERE id = ANY\(\$2\) AND deleted_at IS NULL`).
				WithArgs([]byte(`{"xiass_account_pool":"8"}`), "{7}").WillReturnResult(sqlmock.NewResult(0, count))
			if count > 0 {
				mock.ExpectExec("INSERT INTO scheduler_outbox").WillReturnResult(sqlmock.NewResult(1, 1))
			}
			failure := errors.New("injected failure")
			switch outcome {
			case "success":
				mock.ExpectCommit()
			case "commit error":
				mock.ExpectCommit().WillReturnError(failure)
			default:
				mock.ExpectRollback()
			}
			err := r.WithAccountPoolTransaction(context.Background(), func(ctx context.Context, txRepo service.AccountPoolRepository) error {
				require.NotNil(t, dbent.TxFromContext(ctx))
				bound := txRepo.(*accountPoolTransactionRepository)
				require.Same(t, dbent.TxFromContext(ctx).Client(), bound.client)
				_, err := txRepo.BulkUpdate(ctx, []int64{7}, service.AccountBulkUpdate{Extra: map[string]any{service.AccountPoolExtraKey: "8"}})
				if err != nil {
					return err
				}
				if outcome == "callback error" {
					return failure
				}
				return nil
			})
			if outcome == "success" {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestAccountPoolRepositoryListsEmptyAndPopulatedPools(t *testing.T) {
	r, mock := accountPoolMockRepository(t)
	now := time.Now()
	mock.ExpectQuery(regexp.QuoteMeta(accountPoolSelect + " ORDER BY lower(p.name), p.id")).
		WillReturnRows(sqlmock.NewRows([]string{"id", "name", "proxy_id", "created_at", "updated_at", "members"}).
			AddRow(8, "empty", nil, now, now, "{}").AddRow(9, "mixed", 42, now, now, "{1,2,3}"))
	pools, err := r.ListAccountPools(context.Background())
	require.NoError(t, err)
	require.Len(t, pools, 2)
	require.NotNil(t, pools[0].AccountIDs)
	require.Empty(t, pools[0].AccountIDs)
	require.Nil(t, pools[0].ProxyID)
	require.Equal(t, []int64{1, 2, 3}, pools[1].AccountIDs)
	require.Equal(t, 3, pools[1].AccountCount)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestAccountPoolRepositoryRejectsInactiveMissingExpiredProxy(t *testing.T) {
	r, mock := accountPoolMockRepository(t)
	mock.ExpectQuery(`SELECT id FROM proxies WHERE id=\$1 AND deleted_at IS NULL AND status='active' AND \(expires_at IS NULL OR expires_at > NOW\(\)\) FOR SHARE`).
		WithArgs(int64(42)).WillReturnRows(sqlmock.NewRows([]string{"id"}))
	require.Error(t, r.ValidateAccountPoolProxy(context.Background(), 42))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestAccountPoolRepositoryMapsNameConflict(t *testing.T) {
	require.ErrorIs(t, accountPoolPersistenceError(&pq.Error{Code: "23505"}), service.ErrAccountPoolNameTaken)
	failure := errors.New("database unavailable")
	require.ErrorIs(t, accountPoolPersistenceError(failure), failure)
}
