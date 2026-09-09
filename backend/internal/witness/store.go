package witness

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

const schema = `
CREATE TABLE IF NOT EXISTS execution_node_leases (
	cluster_id TEXT PRIMARY KEY,
	holder_node_id TEXT NOT NULL,
	lease_id TEXT NOT NULL,
	generation INTEGER NOT NULL CHECK (generation >= 0),
	expires_at_ms INTEGER NOT NULL,
	updated_at_ms INTEGER NOT NULL
);
`

// Lease is the durable arbitration record for one XIASS cluster. Generation
// increases whenever ownership changes and never decreases, including after a
// witness restart.
type Lease struct {
	ClusterID    string    `json:"cluster_id"`
	HolderNodeID string    `json:"holder_node_id"`
	LeaseID      string    `json:"lease_id,omitempty"`
	Generation   int64     `json:"generation"`
	ExpiresAt    time.Time `json:"expires_at,omitempty"`
	Active       bool      `json:"active"`
}

type Store struct {
	db *sql.DB
}

func Open(path string) (*Store, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, errors.New("witness data path is empty")
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve witness data path: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(absPath), 0o700); err != nil {
		return nil, fmt.Errorf("create witness data directory: %w", err)
	}
	dsnURL := &url.URL{Scheme: "file", Path: filepath.ToSlash(absPath)}
	dsn := dsnURL.String() + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=synchronous(FULL)&_pragma=foreign_keys(ON)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open witness database: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := db.ExecContext(ctx, schema); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("initialize witness database: %w", err)
	}
	if err := os.Chmod(absPath, 0o600); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("protect witness database: %w", err)
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

// Acquire creates or renews an exact lease. An active lease owned by another
// process is never replaced. A new holder receives the next durable generation.
func (s *Store) Acquire(ctx context.Context, clusterID, nodeID, leaseID string, ttl time.Duration, now time.Time) (Lease, bool, error) {
	if s == nil || s.db == nil {
		return Lease{}, false, errors.New("witness store is unavailable")
	}
	now = now.UTC()
	expiresAt := now.Add(ttl)
	row := s.db.QueryRowContext(ctx, `
		INSERT INTO execution_node_leases (
			cluster_id, holder_node_id, lease_id, generation, expires_at_ms, updated_at_ms
		) VALUES (?, ?, ?, 1, ?, ?)
		ON CONFLICT(cluster_id) DO UPDATE SET
			holder_node_id = excluded.holder_node_id,
			lease_id = excluded.lease_id,
			generation = CASE
				WHEN execution_node_leases.holder_node_id = excluded.holder_node_id
				 AND execution_node_leases.lease_id = excluded.lease_id
				THEN execution_node_leases.generation
				ELSE execution_node_leases.generation + 1
			END,
			expires_at_ms = excluded.expires_at_ms,
			updated_at_ms = excluded.updated_at_ms
		WHERE execution_node_leases.expires_at_ms <= ?
		   OR (
			execution_node_leases.holder_node_id = excluded.holder_node_id
			AND execution_node_leases.lease_id = excluded.lease_id
		   )
		RETURNING cluster_id, holder_node_id, lease_id, generation, expires_at_ms
	`, clusterID, nodeID, leaseID, expiresAt.UnixMilli(), now.UnixMilli(), now.UnixMilli())
	lease, err := scanLease(row, now)
	if errors.Is(err, sql.ErrNoRows) {
		current, statusErr := s.Status(ctx, clusterID, now)
		return current, false, statusErr
	}
	if err != nil {
		return Lease{}, false, fmt.Errorf("acquire witness lease: %w", err)
	}
	return lease, true, nil
}

func (s *Store) Renew(ctx context.Context, clusterID, nodeID, leaseID string, generation int64, ttl time.Duration, now time.Time) (Lease, bool, error) {
	if s == nil || s.db == nil {
		return Lease{}, false, errors.New("witness store is unavailable")
	}
	now = now.UTC()
	expiresAt := now.Add(ttl)
	row := s.db.QueryRowContext(ctx, `
		UPDATE execution_node_leases
		SET expires_at_ms = ?, updated_at_ms = ?
		WHERE cluster_id = ?
		  AND holder_node_id = ?
		  AND lease_id = ?
		  AND generation = ?
		  AND expires_at_ms > ?
		RETURNING cluster_id, holder_node_id, lease_id, generation, expires_at_ms
	`, expiresAt.UnixMilli(), now.UnixMilli(), clusterID, nodeID, leaseID, generation, now.UnixMilli())
	lease, err := scanLease(row, now)
	if errors.Is(err, sql.ErrNoRows) {
		current, statusErr := s.Status(ctx, clusterID, now)
		return current, false, statusErr
	}
	if err != nil {
		return Lease{}, false, fmt.Errorf("renew witness lease: %w", err)
	}
	return lease, true, nil
}

func (s *Store) Release(ctx context.Context, clusterID, nodeID, leaseID string, generation int64, now time.Time) (Lease, bool, error) {
	if s == nil || s.db == nil {
		return Lease{}, false, errors.New("witness store is unavailable")
	}
	now = now.UTC()
	row := s.db.QueryRowContext(ctx, `
		UPDATE execution_node_leases
		SET holder_node_id = '', lease_id = '', expires_at_ms = 0, updated_at_ms = ?
		WHERE cluster_id = ?
		  AND holder_node_id = ?
		  AND lease_id = ?
		  AND generation = ?
		RETURNING cluster_id, holder_node_id, lease_id, generation, expires_at_ms
	`, now.UnixMilli(), clusterID, nodeID, leaseID, generation)
	lease, err := scanLease(row, now)
	if errors.Is(err, sql.ErrNoRows) {
		current, statusErr := s.Status(ctx, clusterID, now)
		return current, false, statusErr
	}
	if err != nil {
		return Lease{}, false, fmt.Errorf("release witness lease: %w", err)
	}
	return lease, true, nil
}

func (s *Store) Status(ctx context.Context, clusterID string, now time.Time) (Lease, error) {
	if s == nil || s.db == nil {
		return Lease{}, errors.New("witness store is unavailable")
	}
	row := s.db.QueryRowContext(ctx, `
		SELECT cluster_id, holder_node_id, lease_id, generation, expires_at_ms
		FROM execution_node_leases
		WHERE cluster_id = ?
	`, clusterID)
	lease, err := scanLease(row, now.UTC())
	if errors.Is(err, sql.ErrNoRows) {
		return Lease{ClusterID: clusterID}, nil
	}
	if err != nil {
		return Lease{}, fmt.Errorf("read witness lease: %w", err)
	}
	return lease, nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanLease(row rowScanner, now time.Time) (Lease, error) {
	var lease Lease
	var expiresAtMS int64
	if err := row.Scan(&lease.ClusterID, &lease.HolderNodeID, &lease.LeaseID, &lease.Generation, &expiresAtMS); err != nil {
		return Lease{}, err
	}
	if expiresAtMS > 0 {
		lease.ExpiresAt = time.UnixMilli(expiresAtMS).UTC()
	}
	lease.Active = lease.HolderNodeID != "" && lease.LeaseID != "" && expiresAtMS > now.UnixMilli()
	if !lease.Active {
		lease.LeaseID = ""
	}
	return lease, nil
}
