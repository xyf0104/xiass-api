package repository

import (
	"context"
	"errors"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestRefreshTokenStoreProviderAuthority(t *testing.T) {
	for _, tc := range []struct {
		name, configured, authority                       string
		recovery, readOnly, missing, badSchema, wantError bool
	}{
		{name: "default retains Redis", authority: "redis"},
		{name: "explicit Redis", configured: "redis", authority: "redis"},
		{name: "migrated persistent", configured: "postgres", authority: "postgres"},
		{name: "cannot enable without migration", configured: "postgres", authority: "redis", wantError: true},
		{name: "cannot roll back to stale Redis", configured: "redis", authority: "postgres", wantError: true},
		{name: "missing marker", missing: true, wantError: true},
		{name: "unknown marker", authority: "future", wantError: true},
		{name: "standby cannot serve authority", configured: "postgres", authority: "postgres", recovery: true, wantError: true},
		{name: "read only cannot serve authority", authority: "redis", readOnly: true, wantError: true},
		{name: "persistent schema incomplete", configured: "postgres", authority: "postgres", badSchema: true, wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer func() { _ = db.Close() }()
			q := mock.ExpectQuery("SELECT backend, pg_is_in_recovery")
			if tc.missing {
				q.WillReturnError(errors.New("synthetic schema unavailable"))
			} else {
				q.WillReturnRows(sqlmock.NewRows([]string{"backend", "recovery", "read_only"}).AddRow(tc.authority, tc.recovery, tc.readOnly))
			}
			if tc.configured == "postgres" && tc.authority == "postgres" && !tc.recovery && !tc.readOnly {
				mock.ExpectQuery("SELECT.*EXISTS").WillReturnRows(sqlmock.NewRows([]string{"ready"}).AddRow(!tc.badSchema))
			}
			rdb := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", MaxRetries: -1})
			defer func() { _ = rdb.Close() }()
			store, err := NewRefreshTokenStore(db, rdb, &config.Config{JWT: config.JWTConfig{RefreshTokenStore: tc.configured}})
			if tc.wantError {
				require.ErrorIs(t, err, ErrRefreshTokenAuthority)
				require.Nil(t, store)
			} else {
				require.NoError(t, err)
				if tc.configured == "postgres" {
					_, ok := store.(service.RefreshTokenIssuancePreparer)
					require.True(t, ok, "provider must retain required pre-generation admission")
				} else {
					policy, ok := store.(service.RefreshTokenIssuancePolicy)
					require.True(t, ok)
					require.True(t, policy.RequiresRefreshTokenIssuanceAdmission())
					_, prepared := store.(service.RefreshTokenIssuancePreparer)
					require.False(t, prepared, "legacy records do not have persistent issuance tickets")
				}
			}
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestRefreshTokenStoreProviderRejectsInvalidDependencies(t *testing.T) {
	store, err := NewRefreshTokenStore(nil, nil, nil)
	require.Nil(t, store)
	require.ErrorIs(t, err, ErrRefreshTokenAuthority)
}

func TestRefreshTokenStoreProviderAllowsExistingSentinelRedisMode(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	for _, mode := range []string{"", "redis"} {
		mock.ExpectQuery("SELECT backend, pg_is_in_recovery").
			WillReturnRows(sqlmock.NewRows([]string{"backend", "recovery", "read_only"}).AddRow("redis", false, false))
		rdb := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", MaxRetries: -1})
		store, err := NewRefreshTokenStore(db, rdb, &config.Config{
			JWT:   config.JWTConfig{RefreshTokenStore: mode},
			Redis: config.RedisConfig{SentinelAddrs: []string{"sentinel.example.invalid:26379"}},
		})
		require.NoError(t, err)
		require.NotNil(t, store)
		require.IsType(t, &authorityCheckedRedisRefreshStore{}, store)
		_ = rdb.Close()
	}
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestRefreshTokenLegacyGuardFencesEveryOperation(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name string
		run  func(service.RefreshTokenCache) error
	}{
		{"store", func(s service.RefreshTokenCache) error { return s.StoreRefreshToken(ctx, "hash", nil, time.Hour) }},
		{"get", func(s service.RefreshTokenCache) error {
			v, e := s.GetRefreshToken(ctx, "hash")
			require.Nil(t, v)
			return e
		}},
		{"consume", func(s service.RefreshTokenCache) error {
			v, e := s.ConsumeRefreshToken(ctx, "hash")
			require.Nil(t, v)
			return e
		}},
		{"delete", func(s service.RefreshTokenCache) error { return s.DeleteRefreshToken(ctx, "hash") }},
		{"delete user", func(s service.RefreshTokenCache) error { return s.DeleteUserRefreshTokens(ctx, 1) }},
		{"delete family", func(s service.RefreshTokenCache) error { return s.DeleteTokenFamily(ctx, "family") }},
		{"add user", func(s service.RefreshTokenCache) error { return s.AddToUserTokenSet(ctx, 1, "hash", time.Hour) }},
		{"add family", func(s service.RefreshTokenCache) error {
			return s.AddToFamilyTokenSet(ctx, "family", "hash", time.Hour)
		}},
		{"list user", func(s service.RefreshTokenCache) error {
			v, e := s.GetUserTokenHashes(ctx, 1)
			require.Nil(t, v)
			return e
		}},
		{"list family", func(s service.RefreshTokenCache) error {
			v, e := s.GetFamilyTokenHashes(ctx, "family")
			require.Nil(t, v)
			return e
		}},
		{"membership", func(s service.RefreshTokenCache) error {
			v, e := s.IsTokenInFamily(ctx, "family", "hash")
			require.False(t, v)
			return e
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer func() { _ = db.Close() }()
			mock.ExpectBegin()
			mock.ExpectQuery("SELECT backend.*FOR SHARE").WillReturnRows(sqlmock.NewRows([]string{"backend"}).AddRow("postgres"))
			mock.ExpectRollback()
			// A nil legacy store would panic if any operation crossed the fence.
			require.ErrorIs(t, tc.run(&authorityCheckedRedisRefreshStore{db: db}), ErrRefreshTokenAuthority)
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestRefreshTokenLegacyGuardDoesNotReturnDataAfterAmbiguousCommit(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT backend.*FOR SHARE").WillReturnRows(sqlmock.NewRows([]string{"backend"}).AddRow("redis"))
	mock.ExpectCommit().WillReturnError(errors.New("synthetic commit acknowledgment lost"))
	value, err := withRedisRefreshAuthority(context.Background(), &authorityCheckedRedisRefreshStore{db: db}, func(context.Context) (*service.RefreshTokenData, error) {
		return &service.RefreshTokenData{UserID: 7}, nil
	})
	require.Nil(t, value)
	require.ErrorIs(t, err, ErrRefreshTokenAuthority)
	require.NoError(t, mock.ExpectationsWereMet())
}
