package repository

import (
	"context"
	"errors"
	"testing"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestProxySubscriptionTransactionRejectsStalePreviewWithoutMutatingProxies(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	client := ent.NewClient(ent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	store := NewProxySubscriptionPersistence(client)
	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO settings").WithArgs(service.SettingKeyProxySubscriptions).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery("SELECT value FROM settings WHERE key=\\$1 FOR UPDATE").WithArgs(service.SettingKeyProxySubscriptions).WillReturnRows(sqlmock.NewRows([]string{"value"}).AddRow("new encrypted revision"))
	mock.ExpectRollback()
	err = store.Update(context.Background(), "old encrypted revision", func(service.ProxyRepository) (string, error) {
		t.Fatal("stale preview mutated proxies")
		return "", nil
	})
	require.ErrorIs(t, err, service.ErrProxySubscriptionChanged)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestProxySubscriptionTransactionRollsBackWhenMutationFails(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	client := ent.NewClient(ent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	store := NewProxySubscriptionPersistence(client)
	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO settings").WithArgs(service.SettingKeyProxySubscriptions).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("SELECT value FROM settings WHERE key=\\$1 FOR UPDATE").WithArgs(service.SettingKeyProxySubscriptions).WillReturnRows(sqlmock.NewRows([]string{"value"}).AddRow(""))
	mock.ExpectRollback()
	err = store.Update(context.Background(), "", func(repo service.ProxyRepository) (string, error) {
		require.IsType(t, &proxyRepository{}, repo)
		return "", errors.New("encryption failed")
	})
	require.EqualError(t, err, "encryption failed")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestProxySubscriptionTransactionCommitsSettingsAfterProxyMutation(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	client := ent.NewClient(ent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	store := NewProxySubscriptionPersistence(client)
	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO settings").WithArgs(service.SettingKeyProxySubscriptions).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("SELECT value FROM settings WHERE key=\\$1 FOR UPDATE").WithArgs(service.SettingKeyProxySubscriptions).WillReturnRows(sqlmock.NewRows([]string{"value"}).AddRow(""))
	mock.ExpectExec(`UPDATE "settings"`).WithArgs("new encrypted snapshot", sqlmock.AnyArg(), service.SettingKeyProxySubscriptions).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	err = store.Update(context.Background(), "", func(service.ProxyRepository) (string, error) { return "new encrypted snapshot", nil })
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}
