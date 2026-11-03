package repository

import (
	"context"
	"encoding/json"
	"testing"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/DATA-DOG/go-sqlmock"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func adsPowerInventoryTestExtra(t *testing.T, environment, errorCode string) []byte {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"xiass_execution_node_id": "retired-owner",
		"error_code":              errorCode,
		service.OpenAIAdsPowerBindingExtraKey: service.OpenAIAdsPowerBinding{
			DeviceID: "device", ProfileID: "profile", EnvironmentKey: environment,
			WebRTCDisabled: true, FingerprintRandomized: true,
		},
	})
	require.NoError(t, err)
	return raw
}

func TestOpenAIAdsPowerInventoryQueriesAllDeviceReferencesWithoutNodeOwnership(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	repo := &accountRepository{sql: db}
	rows := sqlmock.NewRows([]string{"id", "platform", "type", "parent_account_id", "status", "error_message", "extra", "deleted"}).
		AddRow(1, service.PlatformOpenAI, service.AccountTypeOAuth, nil, service.StatusError, nil, adsPowerInventoryTestExtra(t, "saved-pool", "account_banned"), false).
		AddRow(2, service.PlatformOpenAI, service.AccountTypeOAuth, nil, service.StatusActive, nil, adsPowerInventoryTestExtra(t, "another-pool", ""), true)
	mock.ExpectQuery(`SELECT id, platform, type, parent_account_id, status, error_message, extra, deleted_at IS NOT NULL FROM accounts WHERE BTRIM\(COALESCE\(extra -> \$1 ->> 'device_id', ''\)\) = \$2 ORDER BY id ASC LIMIT \$3`).
		WithArgs(service.OpenAIAdsPowerBindingExtraKey, "device", 11).WillReturnRows(rows).RowsWillBeClosed()
	records, complete, err := repo.ListOpenAIAdsPowerBindingAccounts(context.Background(), " device ", 10)
	require.NoError(t, err)
	require.True(t, complete)
	require.Len(t, records, 2)
	require.Equal(t, "saved-pool", records[0].Binding.EnvironmentKey)
	require.True(t, records[1].Deleted)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestOpenAIAdsPowerInventoryLimitRemainsIncomplete(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	repo := &accountRepository{sql: db}
	rows := sqlmock.NewRows([]string{"id", "platform", "type", "parent_account_id", "status", "error_message", "extra", "deleted"})
	for id := 1; id <= 2; id++ {
		rows.AddRow(id, service.PlatformOpenAI, service.AccountTypeOAuth, nil, service.StatusError, nil, adsPowerInventoryTestExtra(t, "saved-pool", "account_banned"), false)
	}
	mock.ExpectQuery("SELECT id, platform, type").WithArgs(service.OpenAIAdsPowerBindingExtraKey, "device", 2).WillReturnRows(rows).RowsWillBeClosed()
	records, complete, err := repo.ListOpenAIAdsPowerBindingAccounts(context.Background(), "device", 1)
	require.NoError(t, err)
	require.False(t, complete)
	require.Len(t, records, 1)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestOpenAIAdsPowerProfileReleaseChecksEveryReferenceAndPreservesOtherMetadata(t *testing.T) {
	for _, scenario := range []string{"cross environment", "ordinary 401", "blocked shared profile"} {
		t.Run(scenario, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			t.Cleanup(func() { _ = db.Close() })
			client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
			repo := &accountRepository{sql: db, client: client}
			rows := sqlmock.NewRows([]string{"id", "platform", "type", "parent_account_id", "status", "error_message", "extra"}).
				AddRow(1, service.PlatformOpenAI, service.AccountTypeOAuth, nil, service.StatusError, nil, adsPowerInventoryTestExtra(t, "saved-pool", "account_banned"))
			environment, errorCode := "saved-pool", "account_banned"
			switch scenario {
			case "cross environment":
				environment = "other-pool"
			case "ordinary 401":
				errorCode = "unauthenticated"
			}
			rows.AddRow(2, service.PlatformOpenAI, service.AccountTypeOAuth, nil, service.StatusError, nil, adsPowerInventoryTestExtra(t, environment, errorCode))
			mock.ExpectBegin()
			mock.ExpectQuery(`SELECT id, platform, type, parent_account_id, status, error_message, extra FROM accounts WHERE deleted_at IS NULL AND BTRIM\(COALESCE\(extra -> \$1 ->> 'device_id', ''\)\) = \$2 AND BTRIM\(COALESCE\(extra -> \$1 ->> 'profile_id', ''\)\) = \$3 ORDER BY id ASC FOR UPDATE`).
				WithArgs(service.OpenAIAdsPowerBindingExtraKey, "device", "profile").WillReturnRows(rows).RowsWillBeClosed()
			if scenario == "blocked shared profile" {
				mock.ExpectExec(`UPDATE accounts SET extra = COALESCE\(extra, '\{\}'::jsonb\) - \$1, updated_at = NOW\(\) WHERE id = ANY\(\$2\) AND deleted_at IS NULL AND BTRIM\(COALESCE\(extra -> \$1 ->> 'device_id', ''\)\) = \$3 AND BTRIM\(COALESCE\(extra -> \$1 ->> 'environment_key', ''\)\) = \$4 AND BTRIM\(COALESCE\(extra -> \$1 ->> 'profile_id', ''\)\) = \$5`).
					WithArgs(service.OpenAIAdsPowerBindingExtraKey, "{1,2}", "device", "saved-pool", "profile").WillReturnResult(sqlmock.NewResult(0, 2))
				mock.ExpectExec("INSERT INTO scheduler_outbox").WillReturnResult(sqlmock.NewResult(1, 1))
				mock.ExpectExec("INSERT INTO scheduler_outbox").WillReturnResult(sqlmock.NewResult(2, 1))
				mock.ExpectCommit()
			} else {
				mock.ExpectRollback()
			}
			ids, released, err := repo.ReleaseOpenAIAdsPowerProfileBindings(context.Background(), "device", "saved-pool", "profile", []string{"account_banned"})
			require.NoError(t, err)
			if scenario == "blocked shared profile" {
				require.True(t, released)
				require.Equal(t, []int64{1, 2}, ids)
			} else {
				require.False(t, released)
				require.Empty(t, ids)
			}
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}
