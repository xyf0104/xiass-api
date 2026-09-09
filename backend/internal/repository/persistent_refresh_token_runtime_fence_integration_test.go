//go:build integration

package repository

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func runtimeFenceOldClient(t *testing.T, source *redis.Client, user, password string) *redis.Client {
	t.Helper()
	opts := *source.Options()
	opts.Dialer, opts.Username, opts.Password, opts.OnConnect = nil, user, password, nil
	c := redis.NewClient(&opts)
	t.Cleanup(func() { require.NoError(t, c.Close()) })
	return c
}

func runtimeFenceAssertSessionDenied(t *testing.T, ctx context.Context, conn *redis.Conn) {
	t.Helper()
	for _, key := range []string{"refresh_token:probe", "user_refresh_tokens:probe", "token_family:probe"} {
		require.ErrorContains(t, conn.Get(ctx, key).Err(), "NOPERM")
		require.ErrorContains(t, conn.Set(ctx, key, "must-not-write", 0).Err(), "NOPERM")
		for _, script := range []string{`return redis.call('GET',KEYS[1])`, `return redis.call('SET',KEYS[1],'must-not-write')`} {
			require.ErrorContains(t, conn.Eval(ctx, script, []string{key}).Err(), "NOPERM")
		}
		// Also exercise actual in-script key checks, not just declared KEYS.
		err := conn.Eval(ctx, `return redis.call('SET',ARGV[1],'must-not-write')`, nil, key).Err()
		require.Error(t, err)
		require.True(t, strings.Contains(err.Error(), "ACL failure") || strings.Contains(err.Error(), "can't access at least one of the keys"), err)
	}
	for _, args := range [][]any{{"ACL", "LIST"}, {"CONFIG", "GET", "masterauth"}, {"FLUSHDB"}, {"FLUSHALL"}, {"REPLICAOF", "NO", "ONE"}, {"SYNC"}, {"PSYNC", "?", "-1"}, {"JSON.SET", "refresh_token:probe", "$", `{}`}} {
		require.ErrorContains(t, conn.Process(ctx, redis.NewCmd(ctx, args...)), "NOPERM")
	}
}

func TestPersistentRefreshRuntimeFenceKeepsAuthenticatedInference(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	db := refreshTransitionPG(t)
	options, _, clients := refreshTransitionTestGroup(t, "redis:8.4-alpine", 2)
	options.PreserveRuntimeAccess = &LegacyRefreshRuntimeAccess{AuthEndpointsBlockedAndDrained: true, ReservedPasswordSHA256: []string{refreshTransitionDigest("new-runtime-password")}}
	d := refreshTransitionTestData()
	hash := persistentRefreshTestHash(t.Name())
	refreshTransitionSeed(t, clients[0], hash, d, 43*time.Minute)
	expires, err := clients[0].Do(ctx, "PEXPIRETIME", refreshTokenKey(hash)).Int64()
	require.NoError(t, err)
	require.NoError(t, clients[0].HSet(ctx, "billing:sub:fixture", "value", "preserved").Err())
	require.NoError(t, clients[0].Do(ctx, "JSON.SET", "billing:sub:module-fixture", "$", `{"kept":true}`).Err())
	var probes []*redis.Conn
	for _, client := range clients {
		require.NoError(t, client.ACLSetUser(ctx, "app-cache", ">second-cache-test-password").Err())
		for user, password := range map[string]string{"default": refreshTransitionTestPassword, "app-cache": "test-cache-password"} {
			conn := runtimeFenceOldClient(t, client, user, password).Conn()
			t.Cleanup(func() { require.NoError(t, conn.Close()) })
			require.NoError(t, conn.Ping(ctx).Err())
			probes = append(probes, conn)
		}
	}
	// Hold the unchanged import transaction so we can inspect the fenced state
	// with inference traffic still flowing, before PG authority can commit.
	gateID := int64(841000) + persistentRefreshTestSequence.Add(1)
	gate, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer gate.Rollback()
	_, err = gate.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, gateID)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `CREATE FUNCTION pause_runtime_import() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
		PERFORM pg_advisory_xact_lock(`+strconv.FormatInt(gateID, 10)+`); RETURN NEW; END; $$;
		CREATE TRIGGER pause_runtime_import BEFORE INSERT ON refresh_tokens FOR EACH ROW EXECUTE FUNCTION pause_runtime_import()`)
	require.NoError(t, err)
	trafficCtx, stop := context.WithCancel(ctx)
	defer stop()
	errors := make(chan error, len(clients)*2)
	var trafficCycles atomic.Int64
	for nodeIndex, client := range clients {
		for user, password := range map[string]string{"default": refreshTransitionTestPassword, "app-cache": "test-cache-password"} {
			conn := runtimeFenceOldClient(t, client, user, password).Conn()
			require.NoError(t, conn.Ping(ctx).Err())
			go func(nodeIndex int, user string, conn *redis.Conn) {
				defer conn.Close()
				for {
					if trafficCtx.Err() != nil {
						errors <- nil
						return
					}
					if nodeIndex == 0 {
						if err := conn.Incr(ctx, fmt.Sprintf("billing:balance:runtime-%d-%s", nodeIndex, user)).Err(); err != nil {
							errors <- err
							return
						}
						if err := conn.Eval(ctx, `redis.call('SET',KEYS[1],'held','PX',10000); redis.call('LPUSH',KEYS[2],'work'); return redis.call('RPOP',KEYS[2])`, []string{fmt.Sprintf("concurrency:account:runtime-%d-%s", nodeIndex, user), "batch_image:queue:ready"}).Err(); err != nil {
							errors <- err
							return
						}
					} else {
						if err := conn.Ping(ctx).Err(); err != nil {
							errors <- err
							return
						}
						if err := conn.Do(ctx, "ROLE").Err(); err != nil {
							errors <- err
							return
						}
					}
					trafficCycles.Add(1)
					time.Sleep(time.Millisecond)
				}
			}(nodeIndex, user, conn)
		}
	}
	require.Eventually(t, func() bool { return trafficCycles.Load() >= 20 }, 5*time.Second, time.Millisecond)
	store := NewPersistentRefreshTokenStore(db)
	adopted := make(chan error, 1)
	var result *LegacyRefreshTransitionResult
	go func() { var err error; result, err = store.AdoptLegacyRefreshTokens(ctx, options); adopted <- err }()
	require.Eventually(t, func() bool {
		var waiting bool
		err := db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pg_locks WHERE locktype='advisory' AND objid=$1 AND NOT granted)`, gateID).Scan(&waiting)
		return err == nil && waiting
	}, 25*time.Second, 20*time.Millisecond)
	before := trafficCycles.Load()
	for _, conn := range probes {
		runtimeFenceAssertSessionDenied(t, ctx, conn)
	}
	require.Eventually(t, func() bool { return trafficCycles.Load() >= before+100 }, 5*time.Second, time.Millisecond)
	var backend string
	require.NoError(t, db.QueryRowContext(ctx, `SELECT backend FROM refresh_token_authority`).Scan(&backend))
	require.Equal(t, "redis", backend)
	require.NoError(t, gate.Commit())
	require.NoError(t, <-adopted)
	before = trafficCycles.Load()
	require.Eventually(t, func() bool { return trafficCycles.Load() >= before+100 }, 5*time.Second, time.Millisecond)
	stop()
	for i := 0; i < len(clients)*2; i++ {
		require.NoError(t, <-errors, "old authenticated inference connection must never fail")
	}
	for _, client := range clients {
		c := runtimeFenceOldClient(t, client, "app-cache", "second-cache-test-password")
		require.NoError(t, c.Ping(ctx).Err(), "all old authentication hashes survive")
		require.ErrorContains(t, c.Get(ctx, refreshTokenKey(hash)).Err(), "NOPERM")
	}
	fenced := refreshTransitionGroupFenceClients(t, options, result.TransitionID)
	for _, key := range []string{"refresh_token:probe", "user_refresh_tokens:probe", "token_family:probe"} {
		require.Zero(t, fenced[0].Exists(ctx, key).Val())
	}
	require.Equal(t, "preserved", fenced[0].HGet(ctx, "billing:sub:fixture", "value").Val())
	require.JSONEq(t, `{"kept":true}`, fenced[0].Do(ctx, "JSON.GET", "billing:sub:module-fixture").Val().(string))
	var until time.Time
	require.NoError(t, db.QueryRowContext(ctx, `SELECT valid_until FROM refresh_tokens WHERE token_hash=$1`, hash).Scan(&until))
	require.Equal(t, time.UnixMilli(expires).UTC(), until)
	got, err := store.GetRefreshToken(ctx, hash)
	require.NoError(t, err)
	require.Equal(t, d, got)
	require.NoError(t, store.DeleteTokenFamily(ctx, d.FamilyID))
	retry, err := store.AdoptLegacyRefreshTokens(ctx, options)
	require.NoError(t, err)
	require.Equal(t, result, retry)
	_, err = store.GetRefreshToken(ctx, hash)
	require.ErrorIs(t, err, service.ErrRefreshTokenNotFound)
	t.Logf("primary old authenticated billing/concurrency/queue cycles plus all-node keepalives=%d; 6 old principals denied session/admin commands during blocked PG import; original absolute expiry retained; revoked family not reimported", trafficCycles.Load())
}

func TestPersistentRefreshRuntimeFencePartialRetryAndImmutableACL(t *testing.T) {
	ctx := context.Background()
	db := refreshTransitionPG(t)
	options, _, _ := refreshTransitionTestGroup(t, "redis:7.4-alpine", 2)
	options.PreserveRuntimeAccess = &LegacyRefreshRuntimeAccess{AuthEndpointsBlockedAndDrained: true, ReservedPasswordSHA256: []string{refreshTransitionDigest("new-runtime-password")}}
	_, err := db.Exec(`CREATE FUNCTION reject_runtime_proof() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected proof failure'; END $$;
		CREATE TRIGGER reject_runtime_proof BEFORE INSERT ON refresh_token_transition_nodes FOR EACH ROW EXECUTE FUNCTION reject_runtime_proof()`)
	require.NoError(t, err)
	store := NewPersistentRefreshTokenStore(db)
	_, err = store.AdoptLegacyRefreshTokens(ctx, options)
	require.ErrorContains(t, err, "injected proof failure")
	var id string
	require.NoError(t, db.QueryRow(`SELECT transition_id FROM refresh_token_legacy_transition`).Scan(&id))
	fenced := refreshTransitionGroupFenceClients(t, options, id)
	ordered := append([]LegacyRefreshTransitionReplica{}, options.Group.Replicas...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].ExpectedRunID < ordered[j].ExpectedRunID })
	var restricted *redis.Client
	for _, c := range fenced[1:] {
		if c.Options().Addr == ordered[0].Client.Options().Addr {
			restricted = c
		}
	}
	require.NotNil(t, restricted)
	rules, err := options.PreserveRuntimeAccess.ACL.Rules(0)
	require.NoError(t, err)
	for _, mutation := range [][]string{{">different-password"}, {"~*"}, {"+@all"}, {"(+get ~*)"}, {"nopass"}} {
		require.NoError(t, restricted.ACLSetUser(ctx, "app-cache", mutation...).Err())
		_, err := store.AdoptLegacyRefreshTokens(ctx, options)
		require.ErrorIs(t, err, ErrRefreshTransitionUnsafe)
		require.NotContains(t, err.Error(), "different-password")
		require.NoError(t, restricted.ACLSetUser(ctx, "app-cache", append([]string{"reset", "on", "resetchannels", ">test-cache-password"}, rules...)...).Err())
	}
	for _, change := range []string{"offline", "grant-config", "reserved-password"} {
		bad := options
		access := *options.PreserveRuntimeAccess
		bad.PreserveRuntimeAccess = &access
		switch change {
		case "offline":
			bad.PreserveRuntimeAccess = nil
		case "grant-config":
			access.ACL.QueueReadyKey = "other:queue"
		case "reserved-password":
			access.ReservedPasswordSHA256 = []string{strings.Repeat("a", 64)}
		}
		_, err := store.AdoptLegacyRefreshTokens(ctx, bad)
		require.ErrorContains(t, err, "inventory does not match")
	}
	_, err = db.Exec(`DROP TRIGGER reject_runtime_proof ON refresh_token_transition_nodes`)
	require.NoError(t, err)
	result, err := store.AdoptLegacyRefreshTokens(ctx, options)
	require.NoError(t, err)
	require.Equal(t, id, result.TransitionID)
	for _, c := range fenced {
		require.NoError(t, VerifyLegacyRefreshRuntimeFence(ctx, db, c, id, refreshRuntimeNodeRun(t, ctx, c), *options.PreserveRuntimeAccess))
	}
	require.NoError(t, fenced[0].ACLSetUser(ctx, "app-cache", "+@all").Err())
	require.ErrorContains(t, VerifyLegacyRefreshRuntimeFence(ctx, db, fenced[0], id, options.ExpectedRunID, *options.PreserveRuntimeAccess), "proof changed")
	for _, query := range []string{`UPDATE refresh_token_legacy_transition SET group_manifest='{}'`, `DELETE FROM refresh_token_transition_nodes`} {
		_, err := db.Exec(query)
		require.Error(t, err)
	}
}

func refreshRuntimeNodeRun(t *testing.T, ctx context.Context, c *redis.Client) string {
	t.Helper()
	info, err := refreshTransitionInfo(ctx, c)
	require.NoError(t, err)
	return info["run_id"]
}

func TestPersistentRefreshRuntimeFencePreflight(t *testing.T) {
	ctx := context.Background()
	options, _, clients := refreshTransitionTestGroup(t, "redis:7.4-alpine", 1)
	for _, name := range []string{"no-group", "not-drained", "unsafe-literal", "unknown-primary-key", "unknown-replica-key", "nopass", "selector", "recovery-reused", "reserved-password-reused"} {
		t.Run(name, func(t *testing.T) {
			bad := options
			access := &LegacyRefreshRuntimeAccess{AuthEndpointsBlockedAndDrained: true, ReservedPasswordSHA256: []string{refreshTransitionDigest("new-runtime-password")}}
			bad.PreserveRuntimeAccess = access
			switch name {
			case "no-group":
				bad.Group = nil
			case "not-drained":
				access.AuthEndpointsBlockedAndDrained = false
			case "unsafe-literal":
				access.ACL.InflightPrefix = "refresh_token:"
			case "unknown-primary-key":
				require.NoError(t, clients[0].Set(ctx, "unlisted:private-value-name", "kept", 0).Err())
				defer clients[0].Del(ctx, "unlisted:private-value-name")
			case "unknown-replica-key":
				require.NoError(t, clients[1].ConfigSet(ctx, "replica-read-only", "no").Err())
				require.NoError(t, clients[1].Set(ctx, "unlisted:replica-name", "kept", 0).Err())
				defer clients[1].Del(ctx, "unlisted:replica-name")
			case "nopass":
				require.NoError(t, clients[1].ACLSetUser(ctx, "app-cache", "nopass").Err())
			case "selector":
				require.NoError(t, clients[1].ACLSetUser(ctx, "app-cache", "(+get ~*)").Err())
			case "recovery-reused":
				require.NoError(t, clients[1].ACLSetUser(ctx, "app-cache", fmt.Sprintf(">%x", options.RecoverySecret)).Err())
			case "reserved-password-reused":
				access.ReservedPasswordSHA256 = []string{refreshTransitionDigest("test-cache-password")}
			}
			db := refreshTransitionPG(t)
			_, err := NewPersistentRefreshTokenStore(db).AdoptLegacyRefreshTokens(ctx, bad)
			require.ErrorIs(t, err, ErrRefreshTransitionUnsafe)
			require.NotContains(t, err.Error(), "private-value-name")
			var count int
			require.NoError(t, db.QueryRow(`SELECT count(*) FROM refresh_token_legacy_transition`).Scan(&count))
			require.Zero(t, count)
			for _, c := range clients {
				require.NoError(t, c.Ping(ctx).Err())
				require.Len(t, c.ACLUsers(ctx).Val(), 2)
			}
			require.NoError(t, clients[1].ACLSetUser(ctx, "app-cache", "reset", "on", ">test-cache-password", "~*", "+@all").Err())
		})
	}
}
