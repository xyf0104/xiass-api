package repository

import (
	"context"
	"testing"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
)

func TestRefreshAuthorityContractDefaultHasNoOperationGate(t *testing.T) {
	// Deliberately structural: this guards against accidentally opting old
	// configurations into the rejected operation-wide context capability.
	type operationGate interface {
		BeginRefreshTokenOperation(context.Context) (context.Context, func(), error)
	}
	for _, store := range []any{&authorityCheckedRedisRefreshStore{}, NewPersistentRefreshTokenStore(nil)} {
		_, gated := store.(operationGate)
		require.False(t, gated, "default stores must not expose a rotation-wide transaction gate")
	}
}

func TestRefreshAuthorityContractContextDoesNotAuthorizeAnotherStore(t *testing.T) {
	a, ma, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = a.Close() })
	b, mb, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = b.Close() })
	ma.ExpectBegin()
	ma.ExpectQuery("SELECT backend.*FOR SHARE").WillReturnRows(sqlmock.NewRows([]string{"backend"}).AddRow("redis"))
	ma.ExpectCommit()
	mb.ExpectBegin()
	mb.ExpectQuery("SELECT backend.*FOR SHARE").WillReturnRows(sqlmock.NewRows([]string{"backend"}).AddRow("postgres"))
	mb.ExpectRollback()

	value, err := withRedisRefreshAuthority(context.Background(), &authorityCheckedRedisRefreshStore{db: a}, func(ctx context.Context) (int, error) {
		// B has deliberately no Redis delegate. An inherited A capability must
		// neither bypass B's authority nor reach its delegate.
		data, rejected := (&authorityCheckedRedisRefreshStore{db: b}).GetRefreshToken(ctx, "synthetic")
		require.Nil(t, data)
		require.ErrorIs(t, rejected, ErrRefreshTokenAuthority)
		return 7, nil
	})
	require.NoError(t, err)
	require.Equal(t, 7, value)
	require.NoError(t, ma.ExpectationsWereMet())
	require.NoError(t, mb.ExpectationsWereMet())
}

func TestRefreshAuthorityContractReleasedContextRequiresFreshAuthority(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	store := &authorityCheckedRedisRefreshStore{db: db}
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT backend.*FOR SHARE").WillReturnRows(sqlmock.NewRows([]string{"backend"}).AddRow("redis"))
	mock.ExpectCommit()
	var captured context.Context
	_, err = withRedisRefreshAuthority(context.Background(), store, func(ctx context.Context) (int, error) {
		captured = ctx
		return 1, nil
	})
	require.NoError(t, err)
	require.ErrorIs(t, captured.Err(), context.Canceled)
	data, err := store.GetRefreshToken(captured, "synthetic")
	require.Nil(t, data)
	require.ErrorIs(t, err, ErrRefreshTokenAuthority)

	// WithoutCancel preserves values; stale context values must not authorize
	// Redis after the original transaction has ended and authority changed.
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT backend.*FOR SHARE").WillReturnRows(sqlmock.NewRows([]string{"backend"}).AddRow("postgres"))
	mock.ExpectRollback()
	data, err = store.GetRefreshToken(context.WithoutCancel(captured), "synthetic")
	require.Nil(t, data)
	require.ErrorIs(t, err, ErrRefreshTokenAuthority)
	require.NoError(t, mock.ExpectationsWereMet())
}
