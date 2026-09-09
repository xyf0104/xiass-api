package repository

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func expectReadinessObservation(mock sqlmock.Sqlmock, backend string, ready bool, commitErr error) {
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT backend, pg_is_in_recovery.*WHERE singleton = TRUE$").
		WillReturnRows(sqlmock.NewRows([]string{"backend", "recovery", "read_only"}).AddRow(backend, false, false))
	if backend == "postgres" {
		mock.ExpectQuery("SELECT.*EXISTS.*refresh_token_legacy_transition").WillReturnRows(sqlmock.NewRows([]string{"ready"}).AddRow(ready))
		if !ready {
			mock.ExpectRollback()
			return
		}
	}
	commit := mock.ExpectCommit()
	if commitErr != nil {
		commit.WillReturnError(commitErr)
	}
}

func TestRefreshTokenProviderReadinessStartupGates(t *testing.T) {
	for _, name := range []string{"redis", "committed PG", "unproven PG", "standby", "read-only", "missing-marker", "unknown-marker", "ambiguous-observation", "no-redis", "explicit-PG", "sentinel"} {
		t.Run(name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer db.Close()
			cfg := &config.Config{JWT: config.JWTConfig{RefreshTokenMigrationReadiness: true}}
			rdb := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", MaxRetries: -1})
			defer rdb.Close()
			switch name {
			case "explicit-PG":
				cfg.JWT.RefreshTokenStore = "postgres"
			case "sentinel":
				cfg.Redis.SentinelAddrs = []string{"unreachable.example.invalid:26379"}
			case "committed PG", "unproven PG", "ambiguous-observation":
				var commitErr error
				if name == "ambiguous-observation" {
					commitErr = errors.New("private-connection-detail")
				}
				expectReadinessObservation(mock, "postgres", name != "unproven PG", commitErr)
				rdb = nil // A committed migration must not require session access to Redis.
			case "redis":
				expectReadinessObservation(mock, "redis", false, nil)
			default:
				mock.ExpectBegin()
				q := mock.ExpectQuery("SELECT backend, pg_is_in_recovery.*WHERE singleton = TRUE$")
				if name == "missing-marker" {
					q.WillReturnError(errors.New("private-connection-detail"))
				} else {
					backend := "redis"
					if name == "unknown-marker" {
						backend = "future"
					}
					q.WillReturnRows(sqlmock.NewRows([]string{"backend", "recovery", "read_only"}).AddRow(backend, name == "standby", name == "read-only"))
				}
				if name == "no-redis" {
					rdb = nil
				}
				mock.ExpectRollback()
			}
			store, err := NewRefreshTokenStore(db, rdb, cfg)
			if name == "redis" || name == "committed PG" {
				require.NoError(t, err)
				ready, ok := store.(*migrationReadyRefreshStore)
				require.True(t, ok)
				require.Equal(t, name == "committed PG", ready.pgSelected.Load())
				require.True(t, ready.RequiresRefreshTokenIssuanceAdmission())
			} else {
				require.Nil(t, store)
				require.ErrorIs(t, err, ErrRefreshTokenAuthority)
				require.NotContains(t, err.Error(), "private-connection-detail")
			}
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestRefreshTokenProviderReadinessFencesEveryOperation(t *testing.T) {
	ctx := context.Background()
	operations := map[string]func(*migrationReadyRefreshStore) error{
		"prepare": func(s *migrationReadyRefreshStore) error {
			v, e := s.PrepareRefreshTokenIssuance(ctx, 1)
			require.Nil(t, v)
			return e
		},
		"store": func(s *migrationReadyRefreshStore) error { return s.StoreRefreshToken(ctx, "hash", nil, time.Hour) },
		"get": func(s *migrationReadyRefreshStore) error {
			v, e := s.GetRefreshToken(ctx, "hash")
			require.Nil(t, v)
			return e
		},
		"consume": func(s *migrationReadyRefreshStore) error {
			v, e := s.ConsumeRefreshToken(ctx, "hash")
			require.Nil(t, v)
			return e
		},
		"revoke":        func(s *migrationReadyRefreshStore) error { return s.DeleteRefreshToken(ctx, "hash") },
		"revoke-user":   func(s *migrationReadyRefreshStore) error { return s.DeleteUserRefreshTokens(ctx, 1) },
		"revoke-family": func(s *migrationReadyRefreshStore) error { return s.DeleteTokenFamily(ctx, "family") },
		"add-user":      func(s *migrationReadyRefreshStore) error { return s.AddToUserTokenSet(ctx, 1, "hash", time.Hour) },
		"add-family": func(s *migrationReadyRefreshStore) error {
			return s.AddToFamilyTokenSet(ctx, "family", "hash", time.Hour)
		},
		"list-user": func(s *migrationReadyRefreshStore) error {
			v, e := s.GetUserTokenHashes(ctx, 1)
			require.Nil(t, v)
			return e
		},
		"list-family": func(s *migrationReadyRefreshStore) error {
			v, e := s.GetFamilyTokenHashes(ctx, "family")
			require.Nil(t, v)
			return e
		},
		"membership": func(s *migrationReadyRefreshStore) error {
			v, e := s.IsTokenInFamily(ctx, "family", "hash")
			require.False(t, v)
			return e
		},
	}
	for name, call := range operations {
		t.Run(name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer db.Close()
			expectReadinessObservation(mock, "postgres", false, nil)
			// Nil delegates make any bypass of the witness gate fail loudly.
			s := &migrationReadyRefreshStore{db: db}
			require.ErrorIs(t, call(s), ErrRefreshTokenAuthority)
			require.False(t, s.pgSelected.Load())
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestRefreshTokenProviderReadinessObservationRetriesWithoutFallback(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	s := &migrationReadyRefreshStore{db: db, postgres: NewPersistentRefreshTokenStore(db)}
	for _, failure := range []string{"begin", "witness", "commit"} {
		switch failure {
		case "begin":
			mock.ExpectBegin().WillReturnError(errors.New("unavailable"))
		case "witness":
			mock.ExpectBegin()
			mock.ExpectQuery("SELECT backend, pg_is_in_recovery.*WHERE singleton = TRUE$").WillReturnRows(sqlmock.NewRows([]string{"backend", "recovery", "read_only"}).AddRow("postgres", false, false))
			mock.ExpectQuery("SELECT.*EXISTS.*refresh_token_legacy_transition").WillReturnError(errors.New("unavailable"))
			mock.ExpectRollback()
		case "commit":
			expectReadinessObservation(mock, "postgres", true, errors.New("ambiguous"))
		}
		got, err := s.GetRefreshToken(context.Background(), strings.Repeat("a", 64))
		require.Nil(t, got)
		require.ErrorIs(t, err, ErrRefreshTokenAuthority)
		require.False(t, s.pgSelected.Load())
	}
	expectReadinessObservation(mock, "postgres", true, nil)
	mock.ExpectQuery("SELECT t.user_id, t.token_version").WillReturnError(errors.New("PG temporarily unavailable"))
	_, err = s.GetRefreshToken(context.Background(), strings.Repeat("a", 64))
	require.ErrorContains(t, err, "PG temporarily unavailable")
	require.True(t, s.pgSelected.Load())
	// Revalidate PG, but never fall back to Redis after a PG failure or miss.
	expectReadinessObservation(mock, "postgres", true, nil)
	mock.ExpectQuery("SELECT t.user_id, t.token_version").WillReturnRows(sqlmock.NewRows([]string{"user_id", "token_version", "family_id", "binding_hash", "created_at", "expires_at", "family_expires_at"}))
	_, err = s.GetRefreshToken(context.Background(), strings.Repeat("a", 64))
	require.ErrorIs(t, err, service.ErrRefreshTokenNotFound)
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT backend, pg_is_in_recovery.*WHERE singleton = TRUE$").WillReturnRows(sqlmock.NewRows([]string{"backend", "recovery", "read_only"}).AddRow("redis", false, false))
	mock.ExpectRollback()
	require.ErrorIs(t, CheckRefreshTokenStoreReadiness(context.Background(), s), ErrRefreshTokenAuthority)
	require.True(t, s.pgSelected.Load(), "reverse authority must not reset the selection")
	require.NoError(t, mock.ExpectationsWereMet())
}

type readinessFailureLegacy struct {
	service.RefreshTokenCache
	calls int
}

func (s *readinessFailureLegacy) ConsumeRefreshToken(context.Context, string) (*service.RefreshTokenData, error) {
	s.calls++
	return nil, errors.New("legacy consumption acknowledgment lost")
}

func TestRefreshTokenProviderReadinessNeverReplaysLegacyErrors(t *testing.T) {
	for _, activateGap := range []bool{false, true} {
		t.Run(map[bool]string{false: "ambiguous-consume", true: "activation-between-observation-and-guard"}[activateGap], func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer db.Close()
			legacy := &readinessFailureLegacy{}
			s := &migrationReadyRefreshStore{db: db, redis: &authorityCheckedRedisRefreshStore{db: db, legacy: legacy}}
			expectReadinessObservation(mock, "redis", false, nil)
			mock.ExpectBegin()
			backend := "redis"
			if activateGap {
				backend = "postgres"
			}
			mock.ExpectQuery("SELECT backend.*FOR SHARE").WillReturnRows(sqlmock.NewRows([]string{"backend"}).AddRow(backend))
			mock.ExpectRollback()
			got, err := s.ConsumeRefreshToken(context.Background(), "legacy-hash")
			require.Nil(t, got)
			if activateGap {
				require.ErrorIs(t, err, ErrRefreshTokenAuthority)
				require.Zero(t, legacy.calls)
			} else {
				require.ErrorContains(t, err, "acknowledgment lost")
				require.Equal(t, 1, legacy.calls)
			}
			require.False(t, s.pgSelected.Load())
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestRefreshTokenProviderReadinessCannotUpgradeLegacyIssuance(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	s := &migrationReadyRefreshStore{db: db, redis: &authorityCheckedRedisRefreshStore{db: db}, postgres: NewPersistentRefreshTokenStore(db)}
	expectReadinessObservation(mock, "redis", false, nil)
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT backend.*FOR SHARE").WillReturnRows(sqlmock.NewRows([]string{"backend"}).AddRow("redis"))
	mock.ExpectCommit()
	ticket, err := s.PrepareRefreshTokenIssuance(context.Background(), 1)
	require.NoError(t, err)
	require.Equal(t, refreshReadinessRedisAdmission, ticket.ID)
	expectReadinessObservation(mock, "postgres", true, nil)
	now := time.Now()
	d := &service.RefreshTokenData{UserID: 1, FamilyID: strings.Repeat("a", 32), CreatedAt: now, ExpiresAt: now.Add(time.Hour), FamilyExpiresAt: now.Add(time.Hour), Issuance: ticket}
	err = s.StoreRefreshToken(context.Background(), strings.Repeat("b", 64), d, time.Hour)
	require.ErrorIs(t, err, ErrPersistentRefreshTokenMetadata)
	require.Equal(t, refreshReadinessRedisAdmission, d.Issuance.ID, "Store must not silently obtain a PG ticket")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestRefreshTokenProviderReadinessPGPrepareRequiresCommit(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	s := &migrationReadyRefreshStore{db: db, postgres: NewPersistentRefreshTokenStore(db)}
	commitErr := errors.New("lost prepare commit acknowledgment")
	for _, fail := range []bool{true, false} {
		expectReadinessObservation(mock, "postgres", true, nil)
		mock.ExpectBegin()
		mock.ExpectExec("SET LOCAL synchronous_commit = on").WillReturnResult(sqlmock.NewResult(0, 0))
		mock.ExpectQuery("SELECT generation FROM refresh_token_revocation_state").WillReturnRows(sqlmock.NewRows([]string{"generation"}).AddRow(3))
		mock.ExpectExec("INSERT INTO refresh_token_users").WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectQuery("SELECT generation FROM refresh_token_users").WillReturnRows(sqlmock.NewRows([]string{"generation"}).AddRow(4))
		mock.ExpectQuery("INSERT INTO refresh_token_issuances").WillReturnRows(sqlmock.NewRows([]string{"ticket_id"}).AddRow("7a7aa320-7244-4526-8868-9c9fdf6b474e"))
		commit := mock.ExpectCommit()
		if fail {
			commit.WillReturnError(commitErr)
		}
		ticket, err := s.PrepareRefreshTokenIssuance(context.Background(), 1)
		if fail {
			require.Nil(t, ticket)
			require.ErrorIs(t, err, commitErr)
		} else {
			require.NoError(t, err)
			require.EqualValues(t, 3, ticket.GlobalGeneration)
			require.EqualValues(t, 4, ticket.UserGeneration)
		}
		require.True(t, s.pgSelected.Load())
	}
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestRefreshTokenProviderReadinessHelperRejectsUnknownProviders(t *testing.T) {
	for _, store := range []service.RefreshTokenCache{
		nil, (*migrationReadyRefreshStore)(nil), (*authorityCheckedRedisRefreshStore)(nil),
		(*PersistentRefreshTokenStore)(nil), &migrationReadyRefreshStore{},
		&authorityCheckedRedisRefreshStore{}, NewPersistentRefreshTokenStore(nil),
		&readinessFailureLegacy{}, NewRefreshTokenCache(nil),
	} {
		require.ErrorIs(t, CheckRefreshTokenStoreReadiness(context.Background(), store), ErrRefreshTokenAuthority)
	}
}

func TestRefreshTokenProviderReadinessHelperKeepsFixedAuthority(t *testing.T) {
	for _, tc := range []struct {
		name, fixed, actual string
		badSchema, readOnly bool
	}{
		{name: "fixed Redis", fixed: "redis", actual: "redis"},
		{name: "fixed PG", fixed: "postgres", actual: "postgres"},
		{name: "ordinary Redis never switches", fixed: "redis", actual: "postgres"},
		{name: "PG never reverses", fixed: "postgres", actual: "redis"},
		{name: "unknown authority", fixed: "redis", actual: "future"},
		{name: "missing schema", fixed: "postgres", actual: "postgres", badSchema: true},
		{name: "read only", fixed: "postgres", actual: "postgres", readOnly: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer db.Close()
			mock.ExpectQuery("SELECT backend, pg_is_in_recovery").WillReturnRows(
				sqlmock.NewRows([]string{"backend", "recovery", "read_only"}).AddRow(tc.actual, false, tc.readOnly))
			var store service.RefreshTokenCache = &authorityCheckedRedisRefreshStore{db: db}
			if tc.fixed == "postgres" {
				store = NewPersistentRefreshTokenStore(db)
				if tc.actual == "postgres" && !tc.readOnly {
					mock.ExpectQuery("SELECT.*EXISTS").WillReturnRows(sqlmock.NewRows([]string{"ready"}).AddRow(!tc.badSchema))
				}
			}
			err = CheckRefreshTokenStoreReadiness(context.Background(), store)
			if tc.fixed == tc.actual && !tc.badSchema && !tc.readOnly {
				require.NoError(t, err)
			} else {
				require.ErrorIs(t, err, ErrRefreshTokenAuthority)
			}
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}
