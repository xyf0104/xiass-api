package repository

import (
	"context"
	"regexp"
	"testing"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/DATA-DOG/go-sqlmock"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestRevertProxyFallbackClearsUpstreamBillingProbe(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	t.Cleanup(func() { _ = client.Close() })
	updateExpectation := mock.ExpectExec(`(?s)UPDATE accounts SET.*- 'upstream_billing_probe'.*proxy_id=proxy_fallback_origin_id`)
	updateExpectation.WithArgs(int64(17))
	updateExpectation.WillReturnResult(sqlmock.NewResult(0, 1))
	outboxExpectation := mock.ExpectExec(`(?s)` + regexp.QuoteMeta("INSERT INTO scheduler_outbox (event_type, account_id, group_id, payload, dedup_key)") + `.*ON CONFLICT \(dedup_key\) WHERE dedup_key IS NOT NULL DO NOTHING`)
	outboxExpectation.WithArgs(service.SchedulerOutboxEventAccountChanged, int64(17), nil, nil, sqlmock.AnyArg())
	outboxExpectation.WillReturnResult(sqlmock.NewResult(1, 1))
	repo := newAccountRepositoryWithSQL(client, db, nil)
	require.NoError(t, repo.RevertProxyFallback(context.Background(), 17))
	require.NoError(t, mock.ExpectationsWereMet())
}
