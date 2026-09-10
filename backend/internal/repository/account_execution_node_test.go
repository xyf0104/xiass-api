package repository

import (
	"context"
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestPrepareExecutionNodeRoutingMigratesLegacyAccountsAndPublishesRebuild(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	repo := newAccountRepositoryWithSQL(nil, db, nil)

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT 1 FROM[\\s\\S]+pg_advisory_xact_lock").
		WillReturnRows(sqlmock.NewRows([]string{"?column?"}).AddRow(1))
	mock.ExpectExec("UPDATE accounts").
		WithArgs(service.AccountExecutionNodeExtraKey, "api", int64(84)).
		WillReturnResult(sqlmock.NewResult(0, 7))
	mock.ExpectExec("INSERT INTO scheduler_outbox").
		WithArgs(service.SchedulerOutboxEventFullRebuild, nil, nil, nil, sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectQuery("SELECT[\\s\\S]+missing_proxy_count").
		WithArgs(service.AccountExecutionNodeExtraKey, "api").
		WillReturnRows(sqlmock.NewRows([]string{"node_id", "missing_proxy_count", "invalid_proxy_count"}).
			AddRow("api", 0, 0).
			AddRow("api2", 0, 0))
	mock.ExpectCommit()

	migrated, err := repo.PrepareExecutionNodeRouting(context.Background(), "api", 84, []string{"api", "api2"})
	require.NoError(t, err)
	require.Equal(t, int64(7), migrated)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPrepareAndEnableExecutionNodeRoutingCommitsSettingsWithMigration(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	repo := newAccountRepositoryWithSQL(nil, db, nil)

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT 1 FROM[\\s\\S]+pg_advisory_xact_lock").
		WillReturnRows(sqlmock.NewRows([]string{"?column?"}).AddRow(1))
	mock.ExpectExec("UPDATE accounts").
		WithArgs(service.AccountExecutionNodeExtraKey, "api", int64(84)).
		WillReturnResult(sqlmock.NewResult(0, 7))
	mock.ExpectExec("INSERT INTO scheduler_outbox").
		WithArgs(service.SchedulerOutboxEventFullRebuild, nil, nil, nil, sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectQuery("SELECT[\\s\\S]+missing_proxy_count").
		WithArgs(service.AccountExecutionNodeExtraKey, "api").
		WillReturnRows(sqlmock.NewRows([]string{"node_id", "missing_proxy_count", "invalid_proxy_count"}).
			AddRow("api", 0, 0).
			AddRow("api2", 0, 0))
	mock.ExpectExec("INSERT INTO settings").
		WithArgs(service.SettingKeyExecutionNodeBalancingEnabled, service.SettingKeyExecutionNodeWeights, `{"api":1,"api2":2}`).
		WillReturnResult(sqlmock.NewResult(1, 2))
	mock.ExpectCommit()

	migrated, err := repo.PrepareAndEnableExecutionNodeRouting(context.Background(), "api", 84, []string{"api", "api2"}, map[string]float64{"api": 1, "api2": 2})
	require.NoError(t, err)
	require.Equal(t, int64(7), migrated)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPrepareAndEnableExecutionNodeRoutingRollsBackSettingsFailure(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	repo := newAccountRepositoryWithSQL(nil, db, nil)

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT 1 FROM[\\s\\S]+pg_advisory_xact_lock").
		WillReturnRows(sqlmock.NewRows([]string{"?column?"}).AddRow(1))
	mock.ExpectExec("UPDATE accounts").
		WithArgs(service.AccountExecutionNodeExtraKey, "api", int64(84)).
		WillReturnResult(sqlmock.NewResult(0, 2))
	mock.ExpectExec("INSERT INTO scheduler_outbox").
		WithArgs(service.SchedulerOutboxEventFullRebuild, nil, nil, nil, sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectQuery("SELECT[\\s\\S]+missing_proxy_count").
		WithArgs(service.AccountExecutionNodeExtraKey, "api").
		WillReturnRows(sqlmock.NewRows([]string{"node_id", "missing_proxy_count", "invalid_proxy_count"}).
			AddRow("api", 0, 0))
	mock.ExpectExec("INSERT INTO settings").
		WillReturnError(errors.New("settings unavailable"))
	mock.ExpectRollback()

	_, err = repo.PrepareAndEnableExecutionNodeRouting(context.Background(), "api", 84, []string{"api"}, map[string]float64{"api": 1})
	require.Error(t, err)
	require.Contains(t, err.Error(), "enable execution node routing settings")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPrepareAndEnableExecutionNodeRoutingWithProxyIDsRejectsCrossNodeProxy(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	repo := newAccountRepositoryWithSQL(nil, db, nil)

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT 1 FROM[\\s\\S]+pg_advisory_xact_lock").
		WillReturnRows(sqlmock.NewRows([]string{"?column?"}).AddRow(1))
	mock.ExpectExec("UPDATE accounts").
		WithArgs(service.AccountExecutionNodeExtraKey, "api", int64(84)).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("INSERT INTO scheduler_outbox").
		WithArgs(service.SchedulerOutboxEventFullRebuild, nil, nil, nil, sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM proxies").
		WithArgs(sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(2))
	mock.ExpectQuery("SELECT node_id,[\\s\\S]+mismatched_proxy_count").
		WithArgs(service.AccountExecutionNodeExtraKey, "api", `{"api":84,"api2":83}`, service.AccountExecutionProxyExtraKey).
		WillReturnRows(sqlmock.NewRows([]string{"node_id", "missing_proxy_count", "invalid_proxy_count", "mismatched_proxy_count"}).
			AddRow("api", 0, 0, 1))
	mock.ExpectRollback()

	_, err = repo.PrepareAndEnableExecutionNodeRoutingWithProxyIDs(
		context.Background(),
		"api",
		84,
		[]string{"api", "api2"},
		map[string]float64{"api": 1, "api2": 1},
		map[string]int64{"api": 84, "api2": 83},
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), "mismatched_proxies")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPrepareAndEnableExecutionNodeRoutingWithProxyIDsPersistsAtomicMapping(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	repo := newAccountRepositoryWithSQL(nil, db, nil)

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT 1 FROM[\\s\\S]+pg_advisory_xact_lock").
		WillReturnRows(sqlmock.NewRows([]string{"?column?"}).AddRow(1))
	mock.ExpectExec("UPDATE accounts").
		WithArgs(service.AccountExecutionNodeExtraKey, "api", int64(84)).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("INSERT INTO scheduler_outbox").
		WithArgs(service.SchedulerOutboxEventFullRebuild, nil, nil, nil, sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM proxies").
		WithArgs(sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(2))
	mock.ExpectQuery("SELECT node_id,[\\s\\S]+mismatched_proxy_count").
		WithArgs(service.AccountExecutionNodeExtraKey, "api", `{"api":84,"api2":83}`, service.AccountExecutionProxyExtraKey).
		WillReturnRows(sqlmock.NewRows([]string{"node_id", "missing_proxy_count", "invalid_proxy_count", "mismatched_proxy_count"}).
			AddRow("api", 0, 0, 0))
	mock.ExpectExec("INSERT INTO settings").
		WithArgs(service.SettingKeyExecutionNodeBalancingEnabled, service.SettingKeyExecutionNodeWeights, `{"api":1,"api2":1}`).
		WillReturnResult(sqlmock.NewResult(1, 2))
	mock.ExpectExec("INSERT INTO settings").
		WithArgs(service.SettingKeyExecutionNodeProxyIDs, `{"api":84,"api2":83}`).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	migrated, err := repo.PrepareAndEnableExecutionNodeRoutingWithProxyIDs(
		context.Background(),
		"api",
		84,
		[]string{"api", "api2"},
		map[string]float64{"api": 1, "api2": 1},
		map[string]int64{"api": 84, "api2": 83},
	)
	require.NoError(t, err)
	require.Zero(t, migrated)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPrepareExecutionNodeRoutingRejectsUnknownOwnerOrMissingProxy(t *testing.T) {
	for _, test := range []struct {
		name         string
		nodeID       string
		missingProxy int64
	}{
		{name: "unknown_owner", nodeID: "api3", missingProxy: 0},
		{name: "missing_proxy", nodeID: "api", missingProxy: 2},
	} {
		t.Run(test.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			t.Cleanup(func() { _ = db.Close() })
			repo := newAccountRepositoryWithSQL(nil, db, nil)

			mock.ExpectBegin()
			mock.ExpectQuery("SELECT 1 FROM[\\s\\S]+pg_advisory_xact_lock").
				WillReturnRows(sqlmock.NewRows([]string{"?column?"}).AddRow(1))
			mock.ExpectExec("UPDATE accounts").
				WithArgs(service.AccountExecutionNodeExtraKey, "api", int64(84)).
				WillReturnResult(sqlmock.NewResult(0, 0))
			mock.ExpectExec("INSERT INTO scheduler_outbox").
				WithArgs(service.SchedulerOutboxEventFullRebuild, nil, nil, nil, sqlmock.AnyArg()).
				WillReturnResult(sqlmock.NewResult(1, 1))
			mock.ExpectQuery("SELECT[\\s\\S]+missing_proxy_count").
				WithArgs(service.AccountExecutionNodeExtraKey, "api").
				WillReturnRows(sqlmock.NewRows([]string{"node_id", "missing_proxy_count", "invalid_proxy_count"}).
					AddRow(test.nodeID, test.missingProxy, 0))
			mock.ExpectRollback()

			_, err = repo.PrepareExecutionNodeRouting(context.Background(), "api", 84, []string{"api", "api2"})
			require.Error(t, err)
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestPrepareExecutionNodeRoutingRollsBackAccountMigrationWhenRebuildPublishFails(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	repo := newAccountRepositoryWithSQL(nil, db, nil)

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT 1 FROM[\\s\\S]+pg_advisory_xact_lock").
		WillReturnRows(sqlmock.NewRows([]string{"?column?"}).AddRow(1))
	mock.ExpectExec("UPDATE accounts").
		WithArgs(service.AccountExecutionNodeExtraKey, "api", int64(84)).
		WillReturnResult(sqlmock.NewResult(0, 2))
	mock.ExpectExec("INSERT INTO scheduler_outbox").
		WithArgs(service.SchedulerOutboxEventFullRebuild, nil, nil, nil, sqlmock.AnyArg()).
		WillReturnError(errors.New("outbox unavailable"))
	mock.ExpectRollback()

	migrated, err := repo.PrepareExecutionNodeRouting(context.Background(), "api", 84, []string{"api", "api2"})
	require.Error(t, err)
	require.Zero(t, migrated)
	require.Contains(t, err.Error(), "publish execution node scheduler rebuild")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestGenericAccountExtraUpdatesCannotMoveExecutionNodeOwnership(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	repo := newAccountRepositoryWithSQL(nil, db, nil)

	// The ownership field is server-managed. A runtime JSONB update carrying
	// only that field is a no-op and must not reach PostgreSQL.
	require.NoError(t, repo.UpdateExtra(context.Background(), 11, map[string]any{
		service.AccountExecutionNodeExtraKey: "api2",
	}))
	affected, err := repo.BulkUpdate(context.Background(), []int64{11}, service.AccountBulkUpdate{
		Extra: map[string]any{
			service.AccountExecutionNodeExtraKey: "api2",
		},
	})
	require.NoError(t, err)
	require.Zero(t, affected)
	require.NoError(t, mock.ExpectationsWereMet())
}
