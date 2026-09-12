package repository

import (
	"context"
	"errors"
	"fmt"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
)

var _ service.AccountPoolRepository = (*accountRepository)(nil)

func (r *accountRepository) WithAccountPoolTransaction(ctx context.Context, fn func(context.Context, service.AccountPoolRepository) error) error {
	if r.client == nil || dbent.TxFromContext(ctx) != nil {
		return service.ErrAccountPoolUnavailable
	}
	tx, err := r.client.Tx(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	txCtx := dbent.NewTxContext(ctx, tx)
	// Pool operations are infrequent admin actions. A transaction-scoped lock
	// serializes membership moves with pool deletion/proxy application across nodes.
	if _, err := tx.Client().ExecContext(txCtx, "SELECT pg_advisory_xact_lock(246, 1)"); err != nil {
		return err
	}
	bound := &accountPoolTransactionRepository{
		accountRepository: &accountRepository{client: tx.Client(), sql: tx.Client(), schedulerCache: r.schedulerCache},
		changedIDs:        make(map[int64]bool),
	}
	if err := fn(txCtx, bound); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	// Publish only committed state. The durable bulk/shadow outbox is committed in
	// the same transaction and retries propagation if this eager refresh fails.
	ids := make([]int64, 0, len(bound.changedIDs))
	for id := range bound.changedIDs {
		ids = append(ids, id)
	}
	refreshCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer cancel()
	r.syncSchedulerAccountSnapshots(refreshCtx, ids)
	return nil
}

type accountPoolTransactionRepository struct {
	*accountRepository
	changedIDs map[int64]bool
}

func (r *accountPoolTransactionRepository) BulkUpdate(ctx context.Context, ids []int64, updates service.AccountBulkUpdate) (int64, error) {
	count, err := r.accountRepository.BulkUpdate(ctx, ids, updates)
	if err != nil {
		return count, err
	}
	if count != int64(len(ids)) {
		return count, fmt.Errorf("account pool update affected %d of %d locked accounts", count, len(ids))
	}
	for _, id := range ids {
		r.changedIDs[id] = true
	}
	return count, nil
}

func (r *accountPoolTransactionRepository) Update(ctx context.Context, account *service.Account) error {
	if err := r.accountRepository.Update(ctx, account); err != nil {
		return err
	}
	r.changedIDs[account.ID] = true
	return nil
}

const accountPoolSelect = `SELECT p.id, p.name, p.proxy_id, p.created_at, p.updated_at,
    COALESCE((SELECT array_agg(a.id ORDER BY a.id) FROM accounts a
        WHERE a.deleted_at IS NULL AND a.extra ->> 'xiass_account_pool' = p.id::text), '{}'::bigint[])
    FROM account_pools p`

func (r *accountRepository) queryAccountPools(ctx context.Context, suffix string, args ...any) ([]service.AccountPool, error) {
	rows, err := r.sql.QueryContext(ctx, accountPoolSelect+suffix, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	pools := make([]service.AccountPool, 0)
	for rows.Next() {
		var pool service.AccountPool
		if err := rows.Scan(&pool.ID, &pool.Name, &pool.ProxyID, &pool.CreatedAt, &pool.UpdatedAt, pq.Array(&pool.AccountIDs)); err != nil {
			return nil, err
		}
		if pool.AccountIDs == nil {
			pool.AccountIDs = []int64{}
		}
		pool.AccountCount = len(pool.AccountIDs)
		pools = append(pools, pool)
	}
	return pools, rows.Err()
}

func (r *accountRepository) ListAccountPools(ctx context.Context) ([]service.AccountPool, error) {
	return r.queryAccountPools(ctx, " ORDER BY lower(p.name), p.id")
}

func (r *accountRepository) GetAccountPool(ctx context.Context, id int64) (*service.AccountPool, error) {
	pools, err := r.queryAccountPools(ctx, " WHERE p.id = $1", id)
	if err != nil {
		return nil, err
	}
	if len(pools) == 0 {
		return nil, service.ErrAccountPoolNotFound
	}
	return &pools[0], nil
}

func accountPoolPersistenceError(err error) error {
	var pgErr *pq.Error
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return service.ErrAccountPoolNameTaken
	}
	return err
}

func (r *accountRepository) CreateAccountPool(ctx context.Context, name string, proxyID *int64) (*service.AccountPool, error) {
	rows, err := r.sql.QueryContext(ctx, `INSERT INTO account_pools (name, proxy_id) VALUES ($1, $2) RETURNING id`, name, proxyID)
	if err != nil {
		return nil, accountPoolPersistenceError(err)
	}
	var id int64
	if !rows.Next() {
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("pool insert returned no ID")
	}
	err = rows.Scan(&id)
	rows.Close()
	if err != nil {
		return nil, err
	}
	return r.GetAccountPool(ctx, id)
}

func (r *accountRepository) UpdateAccountPool(ctx context.Context, id int64, name string, proxyID *int64) error {
	result, err := r.sql.ExecContext(ctx, `UPDATE account_pools SET name=$2, proxy_id=$3, updated_at=NOW() WHERE id=$1`, id, name, proxyID)
	if err != nil {
		return accountPoolPersistenceError(err)
	}
	count, err := result.RowsAffected()
	if err == nil && count != 1 {
		return service.ErrAccountPoolNotFound
	}
	return err
}

func (r *accountRepository) DeleteAccountPool(ctx context.Context, id int64) error {
	result, err := r.sql.ExecContext(ctx, `DELETE FROM account_pools WHERE id=$1`, id)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err == nil && count != 1 {
		return service.ErrAccountPoolNotFound
	}
	return err
}

func (r *accountRepository) LockAccountPoolAccounts(ctx context.Context, ids []int64) ([]*service.Account, error) {
	if len(ids) == 0 {
		return []*service.Account{}, nil
	}
	if dbent.TxFromContext(ctx) == nil {
		return nil, service.ErrAccountPoolUnavailable
	}
	// Lock shadows too: existing propagation reads/merges their full state.
	rows, err := r.sql.QueryContext(ctx, `SELECT id FROM accounts WHERE deleted_at IS NULL
        AND (id = ANY($1) OR (parent_account_id = ANY($1) AND quota_dimension = 'spark'))
        ORDER BY id FOR UPDATE`, pq.Array(ids))
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	return r.GetByIDs(ctx, ids)
}

func (r *accountRepository) ValidateAccountPoolProxy(ctx context.Context, id int64) error {
	// Keep the proxy alive and unchanged until membership and proxy writes commit.
	rows, err := r.sql.QueryContext(ctx, `SELECT id FROM proxies WHERE id=$1 AND deleted_at IS NULL
        AND status='active' AND (expires_at IS NULL OR expires_at > NOW()) FOR SHARE`, id)
	if err != nil {
		return err
	}
	defer rows.Close()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return err
		}
		return infraerrors.BadRequest("INVALID_ACCOUNT_POOL_PROXY", "proxy does not exist, is disabled, or has expired")
	}
	return nil
}
