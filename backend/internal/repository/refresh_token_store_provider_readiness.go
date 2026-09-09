package repository

import (
	"context"
	"database/sql"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

// A readiness provider lives across the migration; the auth ingress must still
// be blocked and drained externally. No background poller, import or Redis/PG
// dual write is involved. Once selected, PostgreSQL is the only delegate even
// on a miss, outage, ambiguous write or later marker inconsistency.
type migrationReadyRefreshStore struct {
	db         *sql.DB
	redis      *authorityCheckedRedisRefreshStore
	postgres   *PersistentRefreshTokenStore
	pgSelected atomic.Bool
}

var _ service.RefreshTokenCache = (*migrationReadyRefreshStore)(nil)
var _ service.RefreshTokenIssuancePolicy = (*migrationReadyRefreshStore)(nil)
var _ service.RefreshTokenIssuancePreparer = (*migrationReadyRefreshStore)(nil)

func (*migrationReadyRefreshStore) RequiresRefreshTokenIssuanceAdmission() bool { return true }

// CheckRefreshTokenStoreReadiness uses the provider constructed at startup, not
// mutable configuration. It does no session IO or writes. The opt-in provider
// may latch PostgreSQL only after a committed observation; future errors and
// reverse/unknown authorities fail closed without resetting that latch. Callers
// should map every error to unavailable (503), never report ready from cfg alone.
func CheckRefreshTokenStoreReadiness(ctx context.Context, store service.RefreshTokenCache) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	switch s := store.(type) {
	case *migrationReadyRefreshStore:
		if s != nil && s.db != nil {
			_, err := s.activeStore(ctx)
			return err
		}
	case *authorityCheckedRedisRefreshStore:
		if s != nil {
			return checkFixedRefreshStoreReadiness(ctx, s.db, "redis")
		}
	case *PersistentRefreshTokenStore:
		if s != nil {
			return checkFixedRefreshStoreReadiness(ctx, s.db, "postgres")
		}
	}
	return ErrRefreshTokenAuthority
}

// Mirrors the committed authority/witness contract in migrations 240/241. A
// bare backend='postgres' row or partial group fence is not permission to switch.
const refreshReadinessCommittedWitness = `SELECT
	EXISTS (SELECT 1 FROM refresh_token_legacy_transition t
		JOIN refresh_token_authority a ON a.singleton = t.singleton
		WHERE t.singleton = TRUE AND a.backend = 'postgres' AND t.state = 'completed'
		AND t.completed_at = a.activated_at AND t.fenced_at IS NOT NULL
		AND t.acl_sha256 ~ '^[0-9a-f]{64}$' AND t.snapshot_sha256 ~ '^[0-9a-f]{64}$'
		AND CASE WHEN t.group_manifest IS NULL THEN TRUE
			WHEN jsonb_typeof(t.group_manifest->'Nodes') = 'array' THEN
				jsonb_array_length(t.group_manifest->'Nodes') BETWEEN 1 AND 9
				AND NOT EXISTS (SELECT 1 FROM jsonb_array_elements(t.group_manifest->'Nodes') n
					WHERE NOT EXISTS (SELECT 1 FROM refresh_token_transition_nodes p
						WHERE p.transition_id = t.transition_id AND p.run_id = n->>'RunID'
						AND p.acl_sha256 ~ '^[0-9a-f]{64}$'))
			ELSE FALSE END)
	AND ` + refreshTokenPersistentSchemaCondition

func (s *migrationReadyRefreshStore) activeStore(ctx context.Context) (service.RefreshTokenCache, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return nil, fmt.Errorf("%w: cannot observe migration readiness", ErrRefreshTokenAuthority)
	}
	defer func() { _ = tx.Rollback() }()
	var backend string
	var recovery, readOnly bool
	// Observe only committed state without taking a row lock: a slow import owns
	// the authority's exclusive lock, but must not remove both inference nodes
	// from readiness. Actual Redis session IO still uses its own locking guard.
	err = tx.QueryRowContext(ctx, `SELECT backend, pg_is_in_recovery(), current_setting('transaction_read_only')::boolean
		FROM refresh_token_authority WHERE singleton = TRUE`).Scan(&backend, &recovery, &readOnly)
	if err != nil || recovery || readOnly || (backend != "redis" && backend != "postgres") {
		return nil, ErrRefreshTokenAuthority
	}
	if backend == "postgres" {
		var ready bool
		if err := tx.QueryRowContext(ctx, refreshReadinessCommittedWitness).Scan(&ready); err != nil || !ready {
			return nil, fmt.Errorf("%w: committed migration witness or persistent schema is not ready", ErrRefreshTokenAuthority)
		}
	} else if s.redis == nil || s.pgSelected.Load() {
		return nil, ErrRefreshTokenAuthority
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("%w: migration observation acknowledgment failed", ErrRefreshTokenAuthority)
	}
	// Release the observation connection BEFORE invoking a delegate; a one-slot
	// SQL pool must not deadlock. The unchanged Redis guard then checks authority
	// again and holds its own shared row lock through the actual Redis operation.
	// If activation wins this gap, that call fails closed and is never replayed.
	if backend == "postgres" {
		s.pgSelected.Store(true)
		return s.postgres, nil
	}
	if s.pgSelected.Load() {
		return nil, ErrRefreshTokenAuthority
	}
	return s.redis, nil
}

func withMigrationReadyRefreshStore[T any](ctx context.Context, s *migrationReadyRefreshStore, fn func(service.RefreshTokenCache) (T, error)) (T, error) {
	var zero T
	store, err := s.activeStore(ctx)
	if err != nil {
		return zero, err
	}
	// Never retry or reselect after the delegate has run: Consume/Revoke/Store
	// may already have committed even when their acknowledgment was lost.
	return fn(store)
}

// Deliberately not a UUID/PG ticket. Legacy admission does not touch the pristine
// PG issuance/generation tables. Store rechecks authority, and PG rejects this
// marker if preparation and storage straddle activation. It is never serialized.
const refreshReadinessRedisAdmission = "redis-migration-readiness"

func (s *migrationReadyRefreshStore) PrepareRefreshTokenIssuance(ctx context.Context, userID int64) (*service.RefreshTokenIssuance, error) {
	if userID <= 0 {
		return nil, ErrPersistentRefreshTokenMetadata
	}
	return withMigrationReadyRefreshStore(ctx, s, func(store service.RefreshTokenCache) (*service.RefreshTokenIssuance, error) {
		if store == s.postgres {
			return s.postgres.PrepareRefreshTokenIssuance(ctx, userID)
		}
		return withRedisRefreshAuthority(ctx, s.redis, func(context.Context) (*service.RefreshTokenIssuance, error) {
			return &service.RefreshTokenIssuance{ID: refreshReadinessRedisAdmission, UserID: userID}, nil
		})
	})
}

func (s *migrationReadyRefreshStore) mutation(ctx context.Context, fn func(service.RefreshTokenCache) error) error {
	_, err := withMigrationReadyRefreshStore(ctx, s, func(store service.RefreshTokenCache) (struct{}, error) {
		return struct{}{}, fn(store)
	})
	return err
}

func (s *migrationReadyRefreshStore) StoreRefreshToken(ctx context.Context, hash string, data *service.RefreshTokenData, ttl time.Duration) error {
	return s.mutation(ctx, func(store service.RefreshTokenCache) error {
		if store == s.redis && (data == nil || data.Issuance == nil || data.UserID <= 0 ||
			data.Issuance.ID != refreshReadinessRedisAdmission || data.Issuance.UserID != data.UserID ||
			data.Issuance.UserGeneration != 0 || data.Issuance.GlobalGeneration != 0) {
			return ErrPersistentRefreshTokenMetadata
		}
		return store.StoreRefreshToken(ctx, hash, data, ttl)
	})
}

func (s *migrationReadyRefreshStore) GetRefreshToken(ctx context.Context, hash string) (*service.RefreshTokenData, error) {
	return withMigrationReadyRefreshStore(ctx, s, func(store service.RefreshTokenCache) (*service.RefreshTokenData, error) {
		return store.GetRefreshToken(ctx, hash)
	})
}

func (s *migrationReadyRefreshStore) ConsumeRefreshToken(ctx context.Context, hash string) (*service.RefreshTokenData, error) {
	return withMigrationReadyRefreshStore(ctx, s, func(store service.RefreshTokenCache) (*service.RefreshTokenData, error) {
		return store.ConsumeRefreshToken(ctx, hash)
	})
}

func (s *migrationReadyRefreshStore) DeleteRefreshToken(ctx context.Context, hash string) error {
	return s.mutation(ctx, func(store service.RefreshTokenCache) error { return store.DeleteRefreshToken(ctx, hash) })
}

func (s *migrationReadyRefreshStore) DeleteUserRefreshTokens(ctx context.Context, userID int64) error {
	return s.mutation(ctx, func(store service.RefreshTokenCache) error { return store.DeleteUserRefreshTokens(ctx, userID) })
}

func (s *migrationReadyRefreshStore) DeleteTokenFamily(ctx context.Context, family string) error {
	return s.mutation(ctx, func(store service.RefreshTokenCache) error { return store.DeleteTokenFamily(ctx, family) })
}

func (s *migrationReadyRefreshStore) AddToUserTokenSet(ctx context.Context, userID int64, hash string, ttl time.Duration) error {
	return s.mutation(ctx, func(store service.RefreshTokenCache) error { return store.AddToUserTokenSet(ctx, userID, hash, ttl) })
}

func (s *migrationReadyRefreshStore) AddToFamilyTokenSet(ctx context.Context, family, hash string, ttl time.Duration) error {
	return s.mutation(ctx, func(store service.RefreshTokenCache) error { return store.AddToFamilyTokenSet(ctx, family, hash, ttl) })
}

func (s *migrationReadyRefreshStore) GetUserTokenHashes(ctx context.Context, userID int64) ([]string, error) {
	return withMigrationReadyRefreshStore(ctx, s, func(store service.RefreshTokenCache) ([]string, error) {
		return store.GetUserTokenHashes(ctx, userID)
	})
}

func (s *migrationReadyRefreshStore) GetFamilyTokenHashes(ctx context.Context, family string) ([]string, error) {
	return withMigrationReadyRefreshStore(ctx, s, func(store service.RefreshTokenCache) ([]string, error) {
		return store.GetFamilyTokenHashes(ctx, family)
	})
}

func (s *migrationReadyRefreshStore) IsTokenInFamily(ctx context.Context, family, hash string) (bool, error) {
	return withMigrationReadyRefreshStore(ctx, s, func(store service.RefreshTokenCache) (bool, error) {
		return store.IsTokenInFamily(ctx, family, hash)
	})
}
