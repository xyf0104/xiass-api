//go:build integration

package repository

import (
	"context"
	"encoding/hex"
	"io"
	"net"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	tcredis "github.com/testcontainers/testcontainers-go/modules/redis"
)

func refreshTransitionTestGroup(t *testing.T, image string, replicaCount int) (LegacyRefreshTransitionOptions, []*tcredis.RedisContainer, []*redis.Client) {
	t.Helper()
	ctx := context.Background()
	containers := []*tcredis.RedisContainer{}
	clients := []*redis.Client{}
	for i := 0; i <= replicaCount; i++ {
		container, client := refreshTransitionRedisImage(t, image)
		containers, clients = append(containers, container), append(clients, client)
		require.NoError(t, client.ACLSetUser(ctx, "app-cache", "reset", "on", ">test-cache-password", "~*", "+@all").Err())
	}
	ip, err := containers[0].ContainerIP(ctx)
	require.NoError(t, err)
	require.NoError(t, clients[0].ConfigSet(ctx, "repl-diskless-sync-delay", "0").Err())
	options := refreshTransitionOptions(t, clients[0])
	options.Group = &LegacyRefreshTransitionGroup{PrimaryAddress: net.JoinHostPort(ip, "6379"), PrimaryACLUsers: []string{"default", "app-cache"}}
	for i := 1; i < len(clients); i++ {
		require.NoError(t, clients[i].ConfigSet(ctx, "masterauth", refreshTransitionTestPassword).Err())
		require.NoError(t, clients[i].Do(ctx, "REPLICAOF", ip, "6379").Err())
		require.Eventually(t, func() bool {
			info, err := refreshTransitionInfo(ctx, clients[i])
			return err == nil && info["master_link_status"] == "up" && info["master_sync_in_progress"] == "0"
		}, 25*time.Second, 50*time.Millisecond)
		info, err := refreshTransitionInfo(ctx, clients[i])
		require.NoError(t, err)
		replicaIP, err := containers[i].ContainerIP(ctx)
		require.NoError(t, err)
		modules, err := refreshTransitionModules(ctx, clients[i])
		require.NoError(t, err)
		options.Group.Replicas = append(options.Group.Replicas, LegacyRefreshTransitionReplica{
			Client: clients[i], ExpectedRunID: info["run_id"], ReplicaAddress: net.JoinHostPort(replicaIP, "6379"),
			ACLUsers: []string{"app-cache", "default"}, Modules: modules,
		})
	}
	info, err := refreshTransitionInfo(ctx, clients[0])
	require.NoError(t, err)
	options.Group.PrimaryReplicationID = info["master_replid"]
	options.Group.PrimaryModules, err = refreshTransitionModules(ctx, clients[0])
	require.NoError(t, err)
	return options, containers, clients
}

func refreshTransitionGroupFenceClients(t *testing.T, options LegacyRefreshTransitionOptions, id string) []*redis.Client {
	t.Helper()
	clients := []*redis.Client{options.Source}
	for _, replica := range options.Group.Replicas {
		clients = append(clients, replica.Client)
	}
	for i, client := range clients {
		opts := *client.Options()
		opts.Dialer, opts.Username, opts.Password = nil, "xiass-refresh-transition-"+id, hex.EncodeToString(options.RecoverySecret)
		clients[i] = redis.NewClient(&opts)
		t.Cleanup(func() { require.NoError(t, clients[i].Close()) })
	}
	return clients
}

func TestPersistentRefreshTransitionGroupMixedReplicasProductionRecovery(t *testing.T) {
	ctx := context.Background()
	db := refreshTransitionPG(t)
	options, containers, clients := refreshTransitionTestGroup(t, "redis:8.4-alpine", 2)
	require.NotEmpty(t, options.Group.PrimaryModules, "exercise the actual mixed Redis image's bundled modules")
	require.NoError(t, clients[0].Set(ctx, "cache:string", "preserved", 48*time.Hour).Err())
	require.NoError(t, clients[0].HSet(ctx, "cache:hash", "field", "value").Err())
	require.NoError(t, clients[0].Do(ctx, "JSON.SET", "cache:json", "$", `{"value":17}`).Err())
	expiry, err := clients[0].Do(ctx, "PEXPIRETIME", "cache:string").Int64()
	require.NoError(t, err)
	otherOpts := *clients[0].Options()
	otherOpts.DB, otherOpts.Dialer = 4, nil
	otherDB := redis.NewClient(&otherOpts)
	defer otherDB.Close()
	require.NoError(t, otherDB.Set(ctx, "another-service", "untouched", 0).Err())

	user := mustCreateUser(t, testEntClient(t), &service.User{Balance: 11.25})
	userRepo := NewUserRepository(testEntClient(t), integrationDB)
	cfg := &config.Config{JWT: config.JWTConfig{Secret: "mixed-group-test-signing-secret", ExpireHour: 168, RefreshTokenExpireDays: 7, RefreshTokenStore: "redis"}}
	newAuth := func(cache service.RefreshTokenCache) *service.AuthService {
		return service.NewAuthService(testEntClient(t), userRepo, nil, cache, cfg, nil, nil, nil, nil, nil, nil, nil, nil)
	}
	legacy, err := NewRefreshTokenStore(db, clients[0], cfg)
	require.NoError(t, err)
	oldPair, err := newAuth(legacy).GenerateTokenPair(ctx, user, "")
	require.NoError(t, err)
	controlPair, err := newAuth(legacy).GenerateTokenPair(ctx, user, "")
	require.NoError(t, err)
	oldHash, controlHash := persistentRefreshTestHash(oldPair.RefreshToken), persistentRefreshTestHash(controlPair.RefreshToken)
	original, err := legacy.GetRefreshToken(ctx, controlHash)
	require.NoError(t, err)
	tokenExpiry, err := clients[0].Do(ctx, "PEXPIRETIME", refreshTokenKey(controlHash)).Int64()
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		for _, client := range clients[1:] {
			if client.Exists(ctx, refreshTokenKey(oldHash)).Val() != 1 || client.Exists(ctx, refreshTokenKey(controlHash)).Val() != 1 {
				return false
			}
		}
		return true
	}, 10*time.Second, 20*time.Millisecond)

	// A revoked/absent original-primary hash must never be imported just because
	// a replica holds stale metadata. Deliberately seed only this test replica.
	absent := refreshTransitionTestData()
	absentHash := persistentRefreshTestHash("replica-only-stale-revocation")
	require.NoError(t, clients[1].ConfigSet(ctx, "replica-read-only", "no").Err())
	refreshTransitionSeed(t, clients[1], absentHash, absent, time.Hour)
	require.EqualValues(t, 0, clients[0].Exists(ctx, refreshTokenKey(absentHash)).Val())
	oldConnections := []*redis.Conn{}
	for _, client := range clients {
		conn := client.Conn()
		defer conn.Close()
		require.NoError(t, conn.Ping(ctx).Err())
		oldConnections = append(oldConnections, conn)
		appOpts := *client.Options()
		appOpts.Dialer, appOpts.Username, appOpts.Password = nil, "app-cache", "test-cache-password"
		app := redis.NewClient(&appOpts)
		defer app.Close()
		appConn := app.Conn()
		defer appConn.Close()
		require.NoError(t, appConn.Ping(ctx).Err())
		oldConnections = append(oldConnections, appConn)
	}
	store := NewPersistentRefreshTokenStore(db)
	result, err := store.AdoptLegacyRefreshTokens(ctx, options)
	require.NoError(t, err)
	require.EqualValues(t, 2, result.Imported)
	for _, conn := range oldConnections {
		err := conn.Set(ctx, "cache:late", "denied", 0).Err()
		require.ErrorContains(t, err, "NOPERM", "all existing authenticated users on every node lose permissions")
		require.Error(t, conn.Process(ctx, redis.NewCmd(ctx, "REPLICAOF", "NO", "ONE")))
	}
	for _, client := range clients {
		require.Error(t, client.Ping(ctx).Err(), "new connections cannot authenticate with old credentials")
	}
	fenced := refreshTransitionGroupFenceClients(t, options, result.TransitionID)
	require.Equal(t, "preserved", fenced[0].Get(ctx, "cache:string").Val())
	require.Equal(t, expiry, fenced[0].Do(ctx, "PEXPIRETIME", "cache:string").Val())
	require.Equal(t, "value", fenced[0].HGet(ctx, "cache:hash", "field").Val())
	require.JSONEq(t, `{"value":17}`, fenced[0].Do(ctx, "JSON.GET", "cache:json").Val().(string))
	otherOpts = *fenced[0].Options()
	otherOpts.DB, otherOpts.Dialer = 4, nil
	otherFenced := redis.NewClient(&otherOpts)
	defer otherFenced.Close()
	require.Equal(t, "untouched", otherFenced.Get(ctx, "another-service").Val())
	var until time.Time
	require.NoError(t, db.QueryRow(`SELECT valid_until FROM refresh_tokens WHERE token_hash=$1`, controlHash).Scan(&until))
	expectedUntil := time.UnixMilli(tokenExpiry).UTC()
	for _, deadline := range []time.Time{original.ExpiresAt, original.FamilyExpiresAt, original.CreatedAt.Add(7 * 24 * time.Hour)} {
		deadline = deadline.Truncate(time.Microsecond)
		if deadline.Before(expectedUntil) {
			expectedUntil = deadline
		}
	}
	require.Equal(t, expectedUntil, until)
	imported, err := store.GetRefreshToken(ctx, controlHash)
	require.NoError(t, err)
	require.Equal(t, original.ExpiresAt.Truncate(time.Microsecond), imported.ExpiresAt)
	require.Equal(t, original.FamilyExpiresAt.Truncate(time.Microsecond), imported.FamilyExpiresAt)
	_, err = store.GetRefreshToken(ctx, absentHash)
	require.ErrorIs(t, err, service.ErrRefreshTokenNotFound)
	var proofCount int
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM refresh_token_transition_nodes`).Scan(&proofCount))
	require.Equal(t, 3, proofCount)
	for _, query := range []string{
		`UPDATE refresh_token_legacy_transition SET group_manifest='{}'`,
		`DELETE FROM refresh_token_transition_nodes`,
		`TRUNCATE refresh_token_transition_nodes`,
		`UPDATE refresh_token_transition_nodes SET fenced_at=clock_timestamp()`,
		`INSERT INTO refresh_token_transition_nodes(transition_id,run_id,acl_sha256) SELECT transition_id,repeat('a',40),repeat('a',64) FROM refresh_token_legacy_transition`,
	} {
		_, err := db.Exec(query)
		require.Error(t, err)
	}

	cfg.JWT.RefreshTokenStore = "postgres"
	selected, err := NewRefreshTokenStore(db, fenced[0], cfg)
	require.NoError(t, err)
	// The already-existing replicated group contains old+control after adoption.
	// PG revocation does not mutate Redis. Kill primary and promote that stale
	// replica only inside this disposable fault-injection environment.
	require.NoError(t, newAuth(selected).RevokeRefreshToken(ctx, oldPair.RefreshToken))
	require.EqualValues(t, 1, fenced[1].Exists(ctx, refreshTokenKey(oldHash)).Val())
	output, err := exec.Command("docker", "kill", "--signal", "KILL", containers[0].GetContainerID()).CombinedOutput()
	require.NoError(t, err, "%s", output)
	require.NoError(t, fenced[1].Do(ctx, "REPLICAOF", "NO", "ONE").Err())
	selected, err = NewRefreshTokenStore(db, fenced[1], cfg)
	require.NoError(t, err)
	invalid, err := newAuth(selected).RefreshTokenPair(ctx, oldPair.RefreshToken)
	require.Nil(t, invalid)
	require.ErrorIs(t, err, service.ErrRefreshTokenInvalid)
	valid, err := newAuth(selected).RefreshTokenPair(ctx, controlPair.RefreshToken)
	require.NoError(t, err)
	rotated, err := selected.GetRefreshToken(ctx, persistentRefreshTestHash(valid.RefreshToken))
	require.NoError(t, err)
	require.Equal(t, original.FamilyExpiresAt.Truncate(time.Microsecond), rotated.FamilyExpiresAt)
	retry, err := store.AdoptLegacyRefreshTokens(ctx, options)
	require.NoError(t, err, "completed retry is PG-only even with original primary dead")
	require.Equal(t, result, retry)
	_, err = selected.GetRefreshToken(ctx, oldHash)
	require.ErrorIs(t, err, service.ErrRefreshTokenNotFound)
	// Restart only an isolated, already-fenced replica to check saved ACLs, not
	// just in-memory permission removal. Port mappings may change on restart.
	require.NoError(t, containers[2].Stop(ctx, nil))
	require.NoError(t, containers[2].Start(ctx))
	endpoint, err := containers[2].ConnectionString(ctx)
	require.NoError(t, err)
	restartedOpts, err := redis.ParseURL(endpoint)
	require.NoError(t, err)
	restartedOpts.Username, restartedOpts.Password = "xiass-refresh-transition-"+result.TransitionID, hex.EncodeToString(options.RecoverySecret)
	restarted := redis.NewClient(restartedOpts)
	defer restarted.Close()
	require.NoError(t, restarted.Ping(ctx).Err())
	for _, user := range []string{"default", "app-cache"} {
		oldOpts := *restartedOpts
		oldOpts.Dialer, oldOpts.Username, oldOpts.Password = nil, user, refreshTransitionTestPassword
		if user == "app-cache" {
			oldOpts.Password = "test-cache-password"
		}
		old := redis.NewClient(&oldOpts)
		require.Error(t, old.Ping(ctx).Err())
		require.NoError(t, old.Close())
	}
}

func TestPersistentRefreshTransitionGroupPartialFenceRetry(t *testing.T) {
	ctx := context.Background()
	db := refreshTransitionPG(t)
	options, _, clients := refreshTransitionTestGroup(t, "redis:7.4-alpine", 2)
	d := refreshTransitionTestData()
	hash := persistentRefreshTestHash(t.Name())
	refreshTransitionSeed(t, clients[0], hash, d, time.Hour)
	_, err := db.Exec(`CREATE FUNCTION reject_node_proof() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected proof commit failure'; END $$;
		CREATE TRIGGER reject_node_proof BEFORE INSERT ON refresh_token_transition_nodes FOR EACH ROW EXECUTE FUNCTION reject_node_proof()`)
	require.NoError(t, err)
	store := NewPersistentRefreshTokenStore(db)
	result, err := store.AdoptLegacyRefreshTokens(ctx, options)
	require.Nil(t, result)
	require.Error(t, err)
	require.NoError(t, clients[0].Ping(ctx).Err(), "replicas are fenced before the original primary")
	ordered := append([]LegacyRefreshTransitionReplica{}, options.Group.Replicas...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].ExpectedRunID < ordered[j].ExpectedRunID })
	require.Error(t, ordered[0].Client.Ping(ctx).Err(), "first durable fence remains despite failed PG proof")
	require.NoError(t, ordered[1].Client.Ping(ctx).Err())
	var backend, state string
	require.NoError(t, db.QueryRow(`SELECT backend,state FROM refresh_token_authority CROSS JOIN refresh_token_legacy_transition`).Scan(&backend, &state))
	require.Equal(t, "redis", backend)
	require.Equal(t, "preparing", state)
	_, err = db.Exec(`UPDATE refresh_token_legacy_transition SET state='fenced',fenced_at=clock_timestamp(),acl_sha256=repeat('a',64)`)
	require.ErrorContains(t, err, "every durable node fence", "cannot bypass missing per-node proofs")
	badOptions := options
	badGroup := *options.Group
	badGroup.Replicas = options.Group.Replicas[:1]
	badOptions.Group = &badGroup
	_, err = store.AdoptLegacyRefreshTokens(ctx, badOptions)
	require.ErrorContains(t, err, "inventory does not match")
	_, err = db.Exec(`DROP TRIGGER reject_node_proof ON refresh_token_transition_nodes`)
	require.NoError(t, err)
	result, err = store.AdoptLegacyRefreshTokens(ctx, options)
	require.NoError(t, err)
	require.EqualValues(t, 1, result.Imported)
	got, err := store.GetRefreshToken(ctx, hash)
	require.NoError(t, err)
	require.Equal(t, d, got)
	require.NoError(t, store.DeleteTokenFamily(ctx, d.FamilyID))
	_, err = store.AdoptLegacyRefreshTokens(ctx, options)
	require.NoError(t, err)
	_, err = store.GetRefreshToken(ctx, hash)
	require.ErrorIs(t, err, service.ErrRefreshTokenNotFound)
}

func TestPersistentRefreshTransitionGroupPreflightRejections(t *testing.T) {
	ctx := context.Background()
	options, _, clients := refreshTransitionTestGroup(t, "redis:7.4-alpine", 2)
	for _, name := range []string{"missing-replica", "duplicate-run", "wrong-run", "wrong-repl-id", "wrong-peer", "wrong-upstream", "unknown-acl-user", "source-is-replica", "unknown-module", "missing-module"} {
		t.Run(name, func(t *testing.T) {
			db := refreshTransitionPG(t)
			bad := options
			group := *options.Group
			group.Replicas = append([]LegacyRefreshTransitionReplica{}, options.Group.Replicas...)
			bad.Group = &group
			switch name {
			case "missing-replica":
				group.Replicas = group.Replicas[:1]
			case "duplicate-run":
				group.Replicas[1].ExpectedRunID = group.Replicas[0].ExpectedRunID
			case "wrong-run":
				group.Replicas[0].ExpectedRunID = strings.Repeat("a", 40)
			case "wrong-repl-id":
				group.PrimaryReplicationID = strings.Repeat("a", 40)
			case "wrong-peer":
				group.Replicas[0].ReplicaAddress = "127.0.0.9:6379"
			case "wrong-upstream":
				group.PrimaryAddress = "127.0.0.9:6379"
			case "unknown-acl-user":
				group.Replicas[1].ACLUsers = []string{"default"}
			case "source-is-replica":
				bad.Source, bad.ExpectedRunID = clients[1], group.Replicas[0].ExpectedRunID
				group.Replicas = group.Replicas[1:]
			case "unknown-module":
				group.PrimaryModules = []LegacyRefreshTransitionModule{{Name: "custom-background-writer", Version: 1}}
			case "missing-module":
				group.PrimaryModules = []LegacyRefreshTransitionModule{{Name: "ReJSON", Version: 1}}
			}
			_, err := NewPersistentRefreshTokenStore(db).AdoptLegacyRefreshTokens(ctx, bad)
			require.ErrorIs(t, err, ErrRefreshTransitionUnsafe)
			for _, client := range clients {
				require.NoError(t, client.Ping(ctx).Err(), "preflight rejection must not disable any legacy node")
			}
			var n int
			require.NoError(t, db.QueryRow(`SELECT count(*) FROM refresh_token_legacy_transition`).Scan(&n))
			require.Zero(t, n)
		})
	}

	t.Run("promoted-replica-cannot-replace-original", func(t *testing.T) {
		db := refreshTransitionPG(t)
		require.NoError(t, clients[1].Do(ctx, "REPLICAOF", "NO", "ONE").Err())
		bad := options
		group := *options.Group
		bad.Group = &group
		bad.Source, bad.ExpectedRunID = clients[1], group.Replicas[0].ExpectedRunID
		group.Replicas = nil
		_, err := NewPersistentRefreshTokenStore(db).AdoptLegacyRefreshTokens(ctx, bad)
		require.ErrorIs(t, err, ErrRefreshTransitionUnsafe)
		require.NoError(t, clients[1].Ping(ctx).Err())
	})
}

func TestPersistentRefreshTransitionGroupAllNodeACLPreflight(t *testing.T) {
	ctx := context.Background()
	db := refreshTransitionPG(t)
	options, containers, clients := refreshTransitionTestGroup(t, "redis:7.4-alpine", 1)
	code, _, err := containers[1].Exec(ctx, []string{"rm", "/data/refresh-transition.acl"})
	require.NoError(t, err)
	require.Zero(t, code)
	code, _, err = containers[1].Exec(ctx, []string{"mkdir", "/data/refresh-transition.acl"})
	require.NoError(t, err)
	require.Zero(t, code)
	_, err = NewPersistentRefreshTokenStore(db).AdoptLegacyRefreshTokens(ctx, options)
	require.ErrorContains(t, err, "original ACL persistence preflight failed")
	for _, client := range clients {
		require.NoError(t, client.Ping(ctx).Err())
		users, err := client.ACLUsers(ctx).Result()
		require.NoError(t, err)
		require.ElementsMatch(t, []string{"default", "app-cache"}, users, "no recovery user created before every original ACL can be saved")
	}
}

func TestPersistentRefreshTransitionGroupMixedKeyBounds(t *testing.T) {
	ctx := context.Background()
	options, _, clients := refreshTransitionTestGroup(t, "redis:7.4-alpine", 0)
	t.Run("auth-in-other-db", func(t *testing.T) {
		opts := *clients[0].Options()
		opts.DB, opts.Dialer = 2, nil
		other := redis.NewClient(&opts)
		defer other.Close()
		key := refreshTokenKey(persistentRefreshTestHash(t.Name()))
		require.NoError(t, other.Set(ctx, key, "do not guess another auth database", 0).Err())
		_, err := NewPersistentRefreshTokenStore(refreshTransitionPG(t)).AdoptLegacyRefreshTokens(ctx, options)
		require.ErrorContains(t, err, "outside selected session database")
		require.NoError(t, clients[0].Ping(ctx).Err())
		require.NoError(t, other.Del(ctx, key).Err())
	})
	t.Run("oversized-mixed-db", func(t *testing.T) {
		require.NoError(t, clients[0].Eval(ctx, `for i=1,tonumber(ARGV[1]) do redis.call('SET','cache:'..i,'x') end return 1`, nil, refreshTransitionMaxMixedKeys+1).Err())
		_, err := NewPersistentRefreshTokenStore(refreshTransitionPG(t)).AdoptLegacyRefreshTokens(ctx, options)
		require.ErrorContains(t, err, "mixed keyspace exceeds")
		require.NoError(t, clients[0].Ping(ctx).Err())
		require.EqualValues(t, refreshTransitionMaxMixedKeys+1, clients[0].DBSize(ctx).Val(), "no key cleanup on budget failure")
	})
}

func TestPersistentRefreshTransitionGroupPromotionDuringCommitRollsBack(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	db := refreshTransitionPG(t)
	options, _, clients := refreshTransitionTestGroup(t, "redis:7.4-alpine", 1)
	d := refreshTransitionTestData()
	refreshTransitionSeed(t, clients[0], persistentRefreshTestHash(t.Name()), d, time.Hour)
	gateID := int64(741000) + persistentRefreshTestSequence.Add(1)
	gate, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer gate.Rollback()
	_, err = gate.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, gateID)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `CREATE FUNCTION pause_group_insert() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
		PERFORM pg_advisory_xact_lock(`+strconv.FormatInt(gateID, 10)+`); RETURN NEW; END; $$;
		CREATE TRIGGER pause_group_insert BEFORE INSERT ON refresh_tokens FOR EACH ROW EXECUTE FUNCTION pause_group_insert()`)
	require.NoError(t, err)
	adopted := make(chan error, 1)
	store := NewPersistentRefreshTokenStore(db)
	go func() { _, err := store.AdoptLegacyRefreshTokens(ctx, options); adopted <- err }()
	require.Eventually(t, func() bool {
		var waiting bool
		err := db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pg_locks WHERE locktype='advisory' AND objid=$1 AND NOT granted)`, gateID).Scan(&waiting)
		return err == nil && waiting
	}, 15*time.Second, 25*time.Millisecond)
	var id string
	require.NoError(t, db.QueryRow(`SELECT transition_id FROM refresh_token_legacy_transition`).Scan(&id))
	fenced := refreshTransitionGroupFenceClients(t, options, id)
	// Only the exclusive operator principal can inject this topology change.
	require.NoError(t, fenced[1].Do(ctx, "REPLICAOF", "NO", "ONE").Err())
	require.NoError(t, gate.Commit())
	require.ErrorIs(t, <-adopted, ErrRefreshTransitionUnsafe)
	var backend, state string
	require.NoError(t, db.QueryRow(`SELECT backend,state FROM refresh_token_authority CROSS JOIN refresh_token_legacy_transition`).Scan(&backend, &state))
	require.Equal(t, "redis", backend)
	require.Equal(t, "fenced", state)
	for _, table := range []string{"refresh_tokens", "refresh_token_families", "refresh_token_users", "refresh_token_issuances"} {
		var count int
		require.NoError(t, db.QueryRow(`SELECT count(*) FROM `+table).Scan(&count))
		require.Zero(t, count)
	}
	for _, client := range clients {
		require.Error(t, client.Ping(ctx).Err(), "topology failure must not restore old writers")
	}
	_, err = store.AdoptLegacyRefreshTokens(ctx, options)
	require.ErrorIs(t, err, ErrRefreshTransitionUnsafe, "retry cannot ignore a promoted inventoried replica")
}

func TestPersistentRefreshTransitionGroupFencedFullSyncConverges(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	options, containers, clients := refreshTransitionTestGroup(t, "redis:7.4-alpine", 1)
	options.PreserveRuntimeAccess = &LegacyRefreshRuntimeAccess{AuthEndpointsBlockedAndDrained: true,
		ReservedPasswordSHA256: []string{refreshTransitionDigest("test-replacement-app-password")}}
	db := refreshTransitionPG(t)
	hash := persistentRefreshTestHash(t.Name())
	refreshTransitionSeed(t, clients[0], hash, refreshTransitionTestData(), time.Hour)
	store := NewPersistentRefreshTokenStore(db)
	result, err := store.AdoptLegacyRefreshTokens(ctx, options)
	require.NoError(t, err)
	fenced := refreshTransitionGroupFenceClients(t, options, result.TransitionID)
	g, _, err := refreshTransitionBuildGroup(options)
	require.NoError(t, err)
	defer g.close()
	for _, node := range g.nodes {
		require.NoError(t, db.QueryRowContext(ctx, `SELECT acl_sha256 FROM refresh_token_transition_nodes WHERE transition_id=$1 AND run_id=$2`, result.TransitionID, node.pin.RunID).Scan(&node.aclHash))
	}
	// Restore replication with a separate narrow fixture credential, like runtime
	// restore. The old application credential remains denied session/admin access.
	require.NoError(t, fenced[0].ACLSetUser(ctx, "fixture-replica", "reset", "on", ">fixture-independent-replication-password", "-@all", "+ping", "+replconf", "+psync").Err())
	require.NoError(t, fenced[1].Do(ctx, "CONFIG", "SET", "masteruser", "fixture-replica", "masterauth", "fixture-independent-replication-password").Err())
	require.Eventually(t, func() bool {
		info, err := refreshTransitionInfo(ctx, fenced[1])
		return err == nil && info["master_link_status"] == "up" && info["master_sync_in_progress"] == "0"
	}, 15*time.Second, 25*time.Millisecond)
	require.NoError(t, g.topology(ctx, fenced, false))
	require.NoError(t, fenced[0].ConfigSet(ctx, "repl-backlog-size", "16384").Err())
	require.NoError(t, fenced[0].ConfigSet(ctx, "repl-diskless-sync", "yes").Err())
	require.NoError(t, fenced[0].ConfigSet(ctx, "repl-diskless-sync-delay", "0").Err())
	// Redis's RDB test hook holds the real transfer open long enough to observe;
	// delaying snapshot startup alone does not hold master_sync_in_progress=1.
	require.NoError(t, fenced[0].ConfigSet(ctx, "rdb-key-save-delay", "10000").Err())
	before, err := refreshTransitionInfo(ctx, fenced[0])
	require.NoError(t, err)
	lastOffset, err := strconv.ParseInt(before["master_repl_offset"], 10, 64)
	require.NoError(t, err)
	paused := false
	defer func() {
		if paused {
			cleanup, stop := context.WithTimeout(context.Background(), 10*time.Second)
			defer stop()
			out, err := exec.CommandContext(cleanup, "docker", "unpause", containers[1].GetContainerID()).CombinedOutput()
			require.NoError(t, err, "%s", out)
		}
	}()
	out, err := exec.CommandContext(ctx, "docker", "pause", containers[1].GetContainerID()).CombinedOutput()
	require.NoError(t, err, "%s", out)
	paused = true
	killed, err := fenced[0].Do(ctx, "CLIENT", "KILL", "TYPE", "replica").Int()
	require.NoError(t, err)
	require.Positive(t, killed)
	// Multiple commands retire replication buffer blocks; one oversized command
	// can remain in a single retained block despite a smaller configured backlog.
	payload := strings.Repeat("x", 8192)
	for i := 0; i < 128; i++ {
		require.NoError(t, fenced[0].Set(ctx, "billing:balance:full-sync-payload:"+strconv.Itoa(i), payload, time.Minute).Err())
	}
	after, err := refreshTransitionInfo(ctx, fenced[0])
	require.NoError(t, err)
	firstOffset, err := strconv.ParseInt(after["repl_backlog_first_byte_offset"], 10, 64)
	require.NoError(t, err)
	require.Greater(t, firstOffset, lastOffset+1, "fixture must prove PSYNC cannot cover the disconnected replica's offset")
	out, err = exec.CommandContext(ctx, "docker", "unpause", containers[1].GetContainerID()).CombinedOutput()
	require.NoError(t, err, "%s", out)
	paused = false
	var observed map[string]string
	defer func() {
		if t.Failed() {
			t.Logf("last full-sync fixture state: %v", g.replicaTopologyError(g.nodes[1], observed, false))
			log, err := containers[1].Logs(ctx)
			if err == nil {
				defer log.Close()
				body, _ := io.ReadAll(io.LimitReader(log, 16384))
				host, _, _ := net.SplitHostPort(options.Group.PrimaryAddress)
				t.Logf("disposable replica diagnostics: %s", strings.ReplaceAll(string(body), host, "<pinned-primary>"))
			}
		}
	}()
	require.Eventually(t, func() bool {
		observed, err = refreshTransitionInfo(ctx, fenced[1])
		return err == nil && observed["master_sync_in_progress"] == "1"
	}, 10*time.Second, 10*time.Millisecond)
	require.Equal(t, "slave", observed["role"])
	require.Equal(t, options.Group.PrimaryAddress, net.JoinHostPort(observed["master_host"], observed["master_port"]))
	require.Equal(t, g.nodes[1].pin.RunID, observed["run_id"])
	require.Equal(t, options.Group.PrimaryReplicationID, observed["master_replid"])
	require.Equal(t, "0", observed["connected_slaves"])
	t.Logf("real forced full-sync observation: %v", g.replicaTopologyError(g.nodes[1], observed, false))
	started := time.Now()
	require.NoError(t, g.topology(ctx, fenced, false), "one topology call must wait, never replay adoption")
	require.Greater(t, time.Since(started), 50*time.Millisecond)
	settled, err := refreshTransitionInfo(ctx, fenced[1])
	require.NoError(t, err)
	require.Equal(t, "0", settled["master_sync_in_progress"])
	require.Equal(t, "0", settled["loading"])
	require.NoError(t, fenced[0].ConfigSet(ctx, "rdb-key-save-delay", "0").Err())
	require.Eventually(t, func() bool {
		info, err := refreshTransitionInfo(ctx, fenced[1])
		return err == nil && info["master_link_status"] == "up" && info["master_sync_in_progress"] == "0" &&
			clients[1].Get(ctx, "billing:balance:full-sync-payload:127").Val() == payload
	}, 15*time.Second, 25*time.Millisecond, "the same replica must actually receive the payload after its backlog gap")
	require.NoError(t, clients[0].Incr(ctx, "billing:balance:post-full-sync").Err())
	require.ErrorContains(t, clients[0].Get(ctx, "refresh_token:"+hash).Err(), "NOPERM")
	require.ErrorContains(t, clients[1].Get(ctx, "refresh_token:"+hash).Err(), "NOPERM")
	require.NoError(t, store.DeleteRefreshToken(ctx, hash))
	_, err = store.GetRefreshToken(ctx, hash)
	require.ErrorIs(t, err, service.ErrRefreshTokenNotFound)
	t.Logf("same pinned replica converged in %s; old session ACLs remain fenced and PG revocation remains authoritative", time.Since(started))
}
