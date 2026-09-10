package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/redis/go-redis/v9"
)

var ErrRefreshTokenAuthority = errors.New("refresh token authority is unavailable or differs from configuration")

// NewRefreshTokenStore never migrates sessions as a startup side effect. The
// persistent mode needs a committed cutover record, not merely a changed env.
func NewRefreshTokenStore(db *sql.DB, rdb *redis.Client, cfg *config.Config) (service.RefreshTokenCache, error) {
	backend := "redis"
	if cfg != nil && cfg.JWT.RefreshTokenStore != "" {
		backend = cfg.JWT.RefreshTokenStore
	}
	if db == nil || (backend != "redis" && backend != "postgres") {
		return nil, ErrRefreshTokenAuthority
	}
	// Keep existing Redis/Sentinel installations startable. A Sentinel
	// configuration alone does not mean this process is performing the
	// PostgreSQL session cutover; that transition remains an explicit,
	// separately fenced operation.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if cfg != nil && cfg.JWT.RefreshTokenMigrationReadiness {
		if backend != "redis" {
			return nil, fmt.Errorf("%w: migration readiness requires redis configuration", ErrRefreshTokenAuthority)
		}
		s := &migrationReadyRefreshStore{db: db, postgres: NewPersistentRefreshTokenStore(db)}
		if rdb != nil {
			s.redis = &authorityCheckedRedisRefreshStore{db: db, legacy: NewRefreshTokenCache(rdb)}
		}
		if _, err := s.activeStore(ctx); err != nil {
			return nil, err
		}
		return s, nil
	}
	if err := checkFixedRefreshStoreReadiness(ctx, db, backend); err != nil {
		return nil, err
	}
	if backend == "postgres" {
		// Keep the concrete store and its pre-generation issuance interface.
		return NewPersistentRefreshTokenStore(db), nil
	}
	if rdb == nil {
		return nil, ErrRefreshTokenAuthority
	}
	return &authorityCheckedRedisRefreshStore{db: db, legacy: NewRefreshTokenCache(rdb)}, nil
}

const refreshTokenPersistentSchemaCondition = `EXISTS (SELECT 1 FROM refresh_token_revocation_state WHERE singleton = TRUE)
	AND to_regclass('refresh_tokens') IS NOT NULL
	AND to_regclass('refresh_token_families') IS NOT NULL
	AND to_regclass('refresh_token_users') IS NOT NULL
	AND to_regclass('refresh_token_issuances') IS NOT NULL`

func checkFixedRefreshStoreReadiness(ctx context.Context, db *sql.DB, backend string) error {
	if db == nil || (backend != "redis" && backend != "postgres") {
		return ErrRefreshTokenAuthority
	}
	var actual string
	var recovery, readOnly bool
	err := db.QueryRowContext(ctx, `SELECT backend, pg_is_in_recovery(),
		current_setting('transaction_read_only')::boolean
		FROM refresh_token_authority WHERE singleton = TRUE`).Scan(&actual, &recovery, &readOnly)
	if err != nil {
		return fmt.Errorf("%w: cannot read migration state", ErrRefreshTokenAuthority)
	}
	if actual != backend || recovery || readOnly {
		return ErrRefreshTokenAuthority
	}
	if backend == "postgres" {
		var ready bool
		err = db.QueryRowContext(ctx, "SELECT "+refreshTokenPersistentSchemaCondition).Scan(&ready)
		if err != nil || !ready {
			return fmt.Errorf("%w: persistent schema is not ready", ErrRefreshTokenAuthority)
		}
	}
	return nil
}

// This guard covers upgraded Redis-mode nodes. Pre-upgrade binaries still need
// an external writer fence before migration; they do not understand this row.
type authorityCheckedRedisRefreshStore struct {
	db     *sql.DB
	legacy service.RefreshTokenCache
}

var _ service.RefreshTokenCache = (*authorityCheckedRedisRefreshStore)(nil)
var _ service.RefreshTokenIssuancePolicy = (*authorityCheckedRedisRefreshStore)(nil)

func (*authorityCheckedRedisRefreshStore) RequiresRefreshTokenIssuanceAdmission() bool { return true }

// A shared row lock spans the Redis operation, so activation's exclusive lock
// waits for in-flight legacy operations. No Redis access follows a failed check.
// Keep the transaction local to this call and require its commit acknowledgment.
// A context marker cannot prove a lock is still held, and a rotation-wide SQL
// transaction can starve the shared user-repository pool. This per-call guard
// is not a cross-store transaction: migration still requires offline fencing.
func withRedisRefreshAuthority[T any](ctx context.Context, s *authorityCheckedRedisRefreshStore, fn func(context.Context) (T, error)) (T, error) {
	var zero T
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return zero, fmt.Errorf("%w: cannot check active store", ErrRefreshTokenAuthority)
	}
	defer func() { _ = tx.Rollback() }()
	var backend string
	err = tx.QueryRowContext(ctx, `SELECT backend FROM refresh_token_authority WHERE singleton = TRUE FOR SHARE`).Scan(&backend)
	if err != nil || backend != "redis" {
		return zero, ErrRefreshTokenAuthority
	}
	value, err := fn(ctx)
	if err != nil {
		return zero, err
	}
	if err = tx.Commit(); err != nil {
		return zero, fmt.Errorf("%w: operation acknowledgment failed", ErrRefreshTokenAuthority)
	}
	return value, nil
}

func (s *authorityCheckedRedisRefreshStore) mutation(ctx context.Context, fn func(context.Context) error) error {
	_, err := withRedisRefreshAuthority(ctx, s, func(ctx context.Context) (struct{}, error) {
		return struct{}{}, fn(ctx)
	})
	return err
}

func (s *authorityCheckedRedisRefreshStore) StoreRefreshToken(ctx context.Context, hash string, data *service.RefreshTokenData, ttl time.Duration) error {
	return s.mutation(ctx, func(ctx context.Context) error { return s.legacy.StoreRefreshToken(ctx, hash, data, ttl) })
}

func (s *authorityCheckedRedisRefreshStore) GetRefreshToken(ctx context.Context, hash string) (*service.RefreshTokenData, error) {
	return withRedisRefreshAuthority(ctx, s, func(ctx context.Context) (*service.RefreshTokenData, error) {
		return s.legacy.GetRefreshToken(ctx, hash)
	})
}

func (s *authorityCheckedRedisRefreshStore) ConsumeRefreshToken(ctx context.Context, hash string) (*service.RefreshTokenData, error) {
	return withRedisRefreshAuthority(ctx, s, func(ctx context.Context) (*service.RefreshTokenData, error) {
		return s.legacy.ConsumeRefreshToken(ctx, hash)
	})
}

func (s *authorityCheckedRedisRefreshStore) DeleteRefreshToken(ctx context.Context, hash string) error {
	return s.mutation(ctx, func(ctx context.Context) error { return s.legacy.DeleteRefreshToken(ctx, hash) })
}

func (s *authorityCheckedRedisRefreshStore) DeleteUserRefreshTokens(ctx context.Context, userID int64) error {
	return s.mutation(ctx, func(ctx context.Context) error { return s.legacy.DeleteUserRefreshTokens(ctx, userID) })
}

func (s *authorityCheckedRedisRefreshStore) DeleteTokenFamily(ctx context.Context, family string) error {
	return s.mutation(ctx, func(ctx context.Context) error { return s.legacy.DeleteTokenFamily(ctx, family) })
}

func (s *authorityCheckedRedisRefreshStore) AddToUserTokenSet(ctx context.Context, userID int64, hash string, ttl time.Duration) error {
	return s.mutation(ctx, func(ctx context.Context) error { return s.legacy.AddToUserTokenSet(ctx, userID, hash, ttl) })
}

func (s *authorityCheckedRedisRefreshStore) AddToFamilyTokenSet(ctx context.Context, family, hash string, ttl time.Duration) error {
	return s.mutation(ctx, func(ctx context.Context) error { return s.legacy.AddToFamilyTokenSet(ctx, family, hash, ttl) })
}

func (s *authorityCheckedRedisRefreshStore) GetUserTokenHashes(ctx context.Context, userID int64) ([]string, error) {
	return withRedisRefreshAuthority(ctx, s, func(ctx context.Context) ([]string, error) { return s.legacy.GetUserTokenHashes(ctx, userID) })
}

func (s *authorityCheckedRedisRefreshStore) GetFamilyTokenHashes(ctx context.Context, family string) ([]string, error) {
	return withRedisRefreshAuthority(ctx, s, func(ctx context.Context) ([]string, error) { return s.legacy.GetFamilyTokenHashes(ctx, family) })
}

func (s *authorityCheckedRedisRefreshStore) IsTokenInFamily(ctx context.Context, family, hash string) (bool, error) {
	return withRedisRefreshAuthority(ctx, s, func(ctx context.Context) (bool, error) { return s.legacy.IsTokenInFamily(ctx, family, hash) })
}
