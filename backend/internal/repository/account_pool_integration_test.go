//go:build integration

package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func accountPoolPGService() service.AccountPoolService {
	r := &accountRepository{client: integrationEntClient, sql: integrationDB}
	return service.NewAdminService(nil, nil, r, nil, nil, nil, nil, nil, nil, nil, nil, nil,
		integrationEntClient, nil, nil, nil, nil, nil, nil, nil, nil)
}

func accountPoolPGProxy(t *testing.T) int64 {
	t.Helper()
	var id int64
	require.NoError(t, integrationDB.QueryRow(`INSERT INTO proxies (name, protocol, host, port)
        VALUES ('pool-test', 'socks5', '127.0.0.1', 1080) RETURNING id`).Scan(&id))
	t.Cleanup(func() {
		_, err := integrationDB.Exec(`DELETE FROM account_pools WHERE proxy_id=$1`, id)
		require.NoError(t, err)
		_, err = integrationDB.Exec(`DELETE FROM proxies WHERE id=$1`, id)
		require.NoError(t, err)
	})
	return id
}

func accountPoolPGAccount(t *testing.T, plan string) int64 {
	t.Helper()
	var id int64
	require.NoError(t, integrationDB.QueryRow(`INSERT INTO accounts
        (name, platform, type, credentials, extra, concurrency, priority)
        VALUES ($1::text, 'openai', 'oauth', jsonb_build_object('plan_type', $1::text),
        '{"unrelated":{"keep":true},"codex_fingerprint_mode":"off","codex_7d_used_percent":37}', 7, 23) RETURNING id`, plan).Scan(&id))
	t.Cleanup(func() {
		_, err := integrationDB.Exec(`DELETE FROM accounts WHERE id=$1`, id)
		require.NoError(t, err)
		_, err = integrationDB.Exec(`DELETE FROM scheduler_outbox WHERE account_id=$1::bigint OR payload->'account_ids' @> jsonb_build_array($1::bigint)`, id)
		require.NoError(t, err)
	})
	return id
}

func accountPoolPGRead(t *testing.T, id int64) map[string]any {
	t.Helper()
	var raw []byte
	require.NoError(t, integrationDB.QueryRow(`SELECT to_jsonb(a) FROM accounts a WHERE id=$1`, id).Scan(&raw))
	var result map[string]any
	require.NoError(t, json.Unmarshal(raw, &result))
	delete(result, "updated_at")
	return result
}

func TestAccountPoolPGMixedPlansLifecyclePreservesAccountConfiguration(t *testing.T) {
	ctx := context.Background()
	s := accountPoolPGService()
	p1, p2 := accountPoolPGProxy(t), accountPoolPGProxy(t)
	pool, err := s.CreateAccountPool(ctx, fmt.Sprintf("mixed-%d", time.Now().UnixNano()), &p1)
	require.NoError(t, err)
	require.Empty(t, pool.AccountIDs)
	_, err = s.CreateAccountPool(ctx, pool.Name, &p1)
	require.ErrorIs(t, err, service.ErrAccountPoolNameTaken)
	ids := []int64{accountPoolPGAccount(t, "pro"), accountPoolPGAccount(t, "plus"), accountPoolPGAccount(t, "team")}
	before := accountPoolPGRead(t, ids[0])
	pool, err = s.AssignAccountPool(ctx, pool.ID, ids, false)
	require.NoError(t, err)
	require.Equal(t, ids, pool.AccountIDs)
	require.Equal(t, 3, pool.AccountCount)
	actual := accountPoolPGRead(t, ids[0])
	before["proxy_id"] = float64(p1)
	extra := before["extra"].(map[string]any)
	extra[service.AccountPoolExtraKey] = strconv.FormatInt(pool.ID, 10)
	extra[service.AccountExecutionProxyExtraKey] = strconv.FormatInt(p1, 10)
	require.Equal(t, before, actual)
	pool, err = s.RenameAccountPool(ctx, pool.ID, pool.Name+"-renamed")
	require.NoError(t, err)
	pool, err = s.SetAccountPoolProxy(ctx, pool.ID, &p2)
	require.NoError(t, err)
	for _, id := range ids {
		require.Equal(t, float64(p2), accountPoolPGRead(t, id)["proxy_id"])
	}
	_, err = s.AssignAccountPool(ctx, pool.ID, ids[:1], true)
	require.NoError(t, err)
	require.Equal(t, float64(p2), accountPoolPGRead(t, ids[0])["proxy_id"])
	require.Nil(t, accountPoolPGRead(t, ids[0])["extra"].(map[string]any)[service.AccountPoolExtraKey])
	require.NoError(t, s.DeleteAccountPool(ctx, pool.ID))
	_, err = s.GetAccountPool(ctx, pool.ID)
	require.ErrorIs(t, err, service.ErrAccountPoolNotFound)
	for _, id := range ids {
		a := accountPoolPGRead(t, id)
		require.Equal(t, float64(p2), a["proxy_id"])
		require.Nil(t, a["extra"].(map[string]any)[service.AccountPoolExtraKey])
	}
}

func TestAccountPoolPGTransactionRollbackIncludesOutbox(t *testing.T) {
	ctx := context.Background()
	r := &accountRepository{client: integrationEntClient, sql: integrationDB}
	id := accountPoolPGAccount(t, "rollback")
	before := accountPoolPGRead(t, id)
	var outboxBefore int
	require.NoError(t, integrationDB.QueryRow(`SELECT count(*) FROM scheduler_outbox`).Scan(&outboxBefore))
	failure := errors.New("fail after account write")
	err := r.WithAccountPoolTransaction(ctx, func(ctx context.Context, tx service.AccountPoolRepository) error {
		pool, err := tx.CreateAccountPool(ctx, fmt.Sprintf("rollback-%d", id), nil)
		if err != nil {
			return err
		}
		_, err = tx.BulkUpdate(ctx, []int64{id}, service.AccountBulkUpdate{Extra: map[string]any{service.AccountPoolExtraKey: strconv.FormatInt(pool.ID, 10)}})
		if err != nil {
			return err
		}
		return failure
	})
	require.ErrorIs(t, err, failure)
	require.Equal(t, before, accountPoolPGRead(t, id))
	var outboxAfter, poolCount int
	require.NoError(t, integrationDB.QueryRow(`SELECT count(*) FROM scheduler_outbox`).Scan(&outboxAfter))
	require.Equal(t, outboxBefore, outboxAfter)
	require.NoError(t, integrationDB.QueryRow(`SELECT count(*) FROM account_pools WHERE name=$1`, fmt.Sprintf("rollback-%d", id)).Scan(&poolCount))
	require.Zero(t, poolCount)
}

func TestAccountPoolPGMissingAccountDoesNotPartiallyAssign(t *testing.T) {
	ctx := context.Background()
	s := accountPoolPGService()
	pool, err := s.CreateAccountPool(ctx, fmt.Sprintf("missing-%d", time.Now().UnixNano()), nil)
	require.NoError(t, err)
	id := accountPoolPGAccount(t, "plus")
	before := accountPoolPGRead(t, id)
	_, err = s.AssignAccountPool(ctx, pool.ID, []int64{id, 9223372036854775807}, false)
	require.ErrorIs(t, err, service.ErrAccountNotFound)
	require.Equal(t, before, accountPoolPGRead(t, id))
}
