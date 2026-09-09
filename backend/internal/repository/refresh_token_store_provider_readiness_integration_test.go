//go:build integration

package repository

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestRefreshTokenProviderReadinessRollingAdoption(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	db := refreshTransitionPG(t)
	options, _, clients := refreshTransitionTestGroup(t, "redis:7.4-alpine", 1)
	options.PreserveRuntimeAccess = &LegacyRefreshRuntimeAccess{AuthEndpointsBlockedAndDrained: true,
		ReservedPasswordSHA256: []string{refreshTransitionDigest("provider-replacement-app-test-secret")}}
	secondOpts := *clients[0].Options()
	secondOpts.Dialer, secondOpts.Username, secondOpts.Password = nil, "app-cache", "test-cache-password"
	second := redis.NewClient(&secondOpts)
	t.Cleanup(func() { require.NoError(t, second.Close()) })
	runtimeClients := []*redis.Client{clients[0], second}
	cfg := &config.Config{JWT: config.JWTConfig{Secret: "provider-readiness-test-signing-secret",
		ExpireHour: 168, RefreshTokenExpireDays: 7, RefreshTokenStore: "redis", RefreshTokenMigrationReadiness: true}}
	user := mustCreateUser(t, testEntClient(t), &service.User{})
	otherUser := mustCreateUser(t, testEntClient(t), &service.User{})
	userRepo := NewUserRepository(testEntClient(t), integrationDB)
	newAuth := func(store service.RefreshTokenCache) *service.AuthService {
		return service.NewAuthService(testEntClient(t), userRepo, nil, store, cfg, nil, nil, nil, nil, nil, nil, nil, nil)
	}
	var stores []service.RefreshTokenCache
	var auths []*service.AuthService
	// Construct main/api2 once, BEFORE adoption. A one-slot pool also proves the
	// readiness observation does not hold a connection while invoking a delegate.
	db.SetMaxOpenConns(1)
	for _, client := range runtimeClients {
		store, err := NewRefreshTokenStore(db, client, cfg)
		require.NoError(t, err)
		stores = append(stores, store)
		auths = append(auths, newAuth(store))
	}
	ordinaryConfig := &config.Config{}
	ordinary, err := NewRefreshTokenStore(db, clients[0], ordinaryConfig)
	require.NoError(t, err)
	ordinaryConfig.JWT.RefreshTokenMigrationReadiness = true
	ordinaryConfig.JWT.RefreshTokenStore = "postgres" // Mutating cfg must not retrofit a provider.

	control, err := auths[0].GenerateTokenPair(ctx, user, "")
	require.NoError(t, err)
	preRotation, err := auths[1].RefreshTokenPair(ctx, control.RefreshToken)
	require.NoError(t, err, "readiness mode still rotates in Redis before migration")
	control = &preRotation.TokenPair
	revoked, err := auths[1].GenerateTokenPair(ctx, user, "")
	require.NoError(t, err)
	familySession, err := auths[0].GenerateTokenPair(ctx, user, "")
	require.NoError(t, err)
	userSession, err := auths[1].GenerateTokenPair(ctx, otherUser, "")
	require.NoError(t, err)
	preRevoked, err := auths[0].GenerateTokenPair(ctx, user, "")
	require.NoError(t, err)
	require.NoError(t, auths[1].RevokeRefreshToken(ctx, preRevoked.RefreshToken))
	controlHash := persistentRefreshTestHash(control.RefreshToken)
	original, err := stores[0].GetRefreshToken(ctx, controlHash)
	require.NoError(t, err)
	expires, err := clients[0].Do(ctx, "PEXPIRETIME", refreshTokenKey(controlHash)).Int64()
	require.NoError(t, err)
	for _, table := range []string{"refresh_tokens", "refresh_token_users", "refresh_token_families", "refresh_token_issuances"} {
		var count int
		require.NoError(t, db.QueryRowContext(ctx, `SELECT count(*) FROM `+table).Scan(&count))
		require.Zero(t, count, "Redis issuance cannot dirty the pristine adoption target")
	}
	require.NotContains(t, clients[0].Get(ctx, refreshTokenKey(controlHash)).Val(), refreshReadinessRedisAdmission)

	stop := make(chan struct{})
	done := make(chan error, len(runtimeClients))
	cycles := make([]atomic.Int64, len(runtimeClients))
	for i, client := range runtimeClients {
		conn := client.Conn()
		require.NoError(t, conn.Ping(ctx).Err())
		go func() {
			defer conn.Close()
			for {
				select {
				case <-stop:
					done <- nil
					return
				default:
				}
				if err := conn.Incr(ctx, fmt.Sprintf("billing:balance:provider-%d", i)).Err(); err != nil {
					done <- err
					return
				}
				if err := conn.Eval(ctx, `redis.call('SET',KEYS[1],'held','PX',10000); return redis.call('GET',KEYS[1])`, []string{fmt.Sprintf("concurrency:account:provider-%d", i)}).Err(); err != nil {
					done <- err
					return
				}
				cycles[i].Add(1)
				time.Sleep(time.Millisecond)
			}
		}()
	}
	stopTraffic := sync.OnceFunc(func() {
		close(stop)
		for range runtimeClients {
			require.NoError(t, <-done)
		}
	})
	t.Cleanup(stopTraffic)
	assertProgress := func() {
		t.Helper()
		before := []int64{cycles[0].Load(), cycles[1].Load()}
		require.Eventually(t, func() bool { return cycles[0].Load() > before[0]+25 && cycles[1].Load() > before[1]+25 }, 5*time.Second, time.Millisecond)
	}
	assertProgress()
	db.SetMaxOpenConns(8)
	gateID := int64(941000) + persistentRefreshTestSequence.Add(1)
	gate, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer gate.Rollback()
	_, err = gate.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, gateID)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `CREATE FUNCTION pause_provider_import() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
		PERFORM pg_advisory_xact_lock(`+strconv.FormatInt(gateID, 10)+`); RETURN NEW; END; $$;
		CREATE TRIGGER pause_provider_import BEFORE INSERT ON refresh_tokens FOR EACH ROW EXECUTE FUNCTION pause_provider_import()`)
	require.NoError(t, err)
	adopted := make(chan error, 1)
	var result *LegacyRefreshTransitionResult
	go func() {
		var err error
		result, err = NewPersistentRefreshTokenStore(db).AdoptLegacyRefreshTokens(ctx, options)
		adopted <- err
	}()
	require.Eventually(t, func() bool {
		var waiting bool
		err := db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pg_locks WHERE locktype='advisory' AND objid=$1 AND NOT granted)`, gateID).Scan(&waiting)
		return err == nil && waiting
	}, 20*time.Second, 20*time.Millisecond)
	// These are negative provider probes while ingress is closed, not auth flows
	// we claim to serve during drain. A timeout cannot consume or expose a token.
	for _, store := range stores {
		probe, stopProbe := context.WithTimeout(ctx, 80*time.Millisecond)
		value, err := store.ConsumeRefreshToken(probe, controlHash)
		stopProbe()
		require.Nil(t, value)
		require.ErrorIs(t, err, ErrRefreshTokenAuthority)
		require.False(t, store.(*migrationReadyRefreshStore).pgSelected.Load())
	}
	assertProgress()
	require.NoError(t, gate.Commit())
	require.NoError(t, <-adopted)
	require.EqualValues(t, 4, result.Imported)
	db.SetMaxOpenConns(1)
	for _, store := range stores {
		require.NoError(t, CheckRefreshTokenStoreReadiness(ctx, store))
		got, err := store.GetRefreshToken(ctx, controlHash)
		require.NoError(t, err)
		require.Equal(t, persistentRefreshPayload(original), got)
	}
	require.ErrorIs(t, CheckRefreshTokenStoreReadiness(ctx, ordinary), ErrRefreshTokenAuthority, "cfg mutation cannot enable an ordinary provider")
	var until time.Time
	require.NoError(t, db.QueryRowContext(ctx, `SELECT valid_until FROM refresh_tokens WHERE token_hash=$1`, controlHash).Scan(&until))
	expectedUntil := time.UnixMilli(expires).UTC()
	for _, deadline := range []time.Time{original.ExpiresAt, original.FamilyExpiresAt, original.CreatedAt.Add(7 * 24 * time.Hour)} {
		if deadline.Before(expectedUntil) {
			expectedUntil = deadline
		}
	}
	require.Equal(t, expectedUntil.Truncate(time.Microsecond), until, "adoption must preserve the earliest absolute deadline, not extend it to Redis TTL")

	rotated, err := auths[1].RefreshTokenPair(ctx, control.RefreshToken)
	require.NoError(t, err, "api2 rotates on its original provider object")
	rotatedData, err := stores[0].GetRefreshToken(ctx, persistentRefreshTestHash(rotated.RefreshToken))
	require.NoError(t, err)
	require.Equal(t, original.FamilyExpiresAt.Truncate(time.Microsecond), rotatedData.FamilyExpiresAt)
	require.NoError(t, auths[0].RevokeRefreshToken(ctx, revoked.RefreshToken))
	_, err = auths[1].RefreshTokenPair(ctx, revoked.RefreshToken)
	require.ErrorIs(t, err, service.ErrRefreshTokenInvalid)
	_, err = auths[1].RefreshTokenPair(ctx, preRevoked.RefreshToken)
	require.ErrorIs(t, err, service.ErrRefreshTokenInvalid, "pre-migration revocation is not resurrected")
	familyData, err := stores[0].GetRefreshToken(ctx, persistentRefreshTestHash(familySession.RefreshToken))
	require.NoError(t, err)
	require.NoError(t, stores[1].DeleteTokenFamily(ctx, familyData.FamilyID))
	_, err = stores[0].GetRefreshToken(ctx, persistentRefreshTestHash(familySession.RefreshToken))
	require.ErrorIs(t, err, service.ErrRefreshTokenNotFound)
	require.NoError(t, stores[0].DeleteUserRefreshTokens(ctx, otherUser.ID))
	_, err = stores[1].GetRefreshToken(ctx, persistentRefreshTestHash(userSession.RefreshToken))
	require.ErrorIs(t, err, service.ErrRefreshTokenNotFound)
	for _, store := range stores {
		require.NoError(t, store.AddToUserTokenSet(ctx, user.ID, persistentRefreshTestHash(rotated.RefreshToken), 24*time.Hour))
		require.NoError(t, store.AddToFamilyTokenSet(ctx, rotatedData.FamilyID, persistentRefreshTestHash(rotated.RefreshToken), 24*time.Hour))
		hashes, err := store.GetUserTokenHashes(ctx, user.ID)
		require.NoError(t, err)
		require.Contains(t, hashes, persistentRefreshTestHash(rotated.RefreshToken))
		hashes, err = store.GetFamilyTokenHashes(ctx, rotatedData.FamilyID)
		require.NoError(t, err)
		require.Equal(t, []string{persistentRefreshTestHash(rotated.RefreshToken)}, hashes)
		member, err := store.IsTokenInFamily(ctx, rotatedData.FamilyID, persistentRefreshTestHash(rotated.RefreshToken))
		require.NoError(t, err)
		require.True(t, member)
	}
	// No ACL write by the provider: after adoption, querying/rotating/revoking and
	// readiness checks must leave the exact entire saved ACL on each node intact.
	fenced := refreshTransitionGroupFenceClients(t, options, result.TransitionID)
	for i, client := range fenced {
		runID := options.ExpectedRunID
		if i > 0 {
			runID = options.Group.Replicas[i-1].ExpectedRunID
		}
		var aclHash string
		require.NoError(t, db.QueryRowContext(ctx, `SELECT acl_sha256 FROM refresh_token_transition_nodes WHERE run_id=$1`, runID).Scan(&aclHash))
		lines, err := client.ACLList(ctx).Result()
		require.NoError(t, err)
		require.Equal(t, aclHash, providerReadinessACLHash(lines))
	}
	for _, client := range runtimeClients {
		require.ErrorContains(t, client.Get(ctx, refreshTokenKey(controlHash)).Err(), "NOPERM")
		require.ErrorContains(t, client.Set(ctx, refreshTokenKey(controlHash), "blocked", 0).Err(), "NOPERM")
		require.ErrorContains(t, client.ACLUsers(ctx).Err(), "NOPERM")
	}
	assertProgress()
	// Final explicit PG config can replace api2 first; main's existing readiness
	// provider keeps working. No simultaneous restart is part of this operation.
	explicitPG, err := NewRefreshTokenStore(db, nil, &config.Config{JWT: config.JWTConfig{RefreshTokenStore: "postgres"}})
	require.NoError(t, err)
	finalPair, err := newAuth(explicitPG).RefreshTokenPair(ctx, rotated.RefreshToken)
	require.NoError(t, err)
	_, err = stores[0].GetRefreshToken(ctx, persistentRefreshTestHash(finalPair.RefreshToken))
	require.NoError(t, err)
	_, err = NewRefreshTokenStore(db, nil, cfg)
	require.NoError(t, err, "readiness startup after committed migration needs no session Redis client")
	stopTraffic()
	t.Logf("same main/api2 provider objects survived adoption; uninterrupted billing/concurrency cycles main=%d api2=%d; original expiry, cross-node rotation/revoke and unchanged ACL proofs verified", cycles[0].Load(), cycles[1].Load())
}

func providerReadinessACLHash(lines []string) string {
	slices.Sort(lines)
	return refreshTransitionDigest(strings.Join(lines, "\n"))
}

func TestRefreshTokenProviderReadinessRejectsUnprovenAuthority(t *testing.T) {
	ctx := context.Background()
	options, _, clients := refreshTransitionTestGroup(t, "redis:7.4-alpine", 0)
	db := refreshTransitionPG(t)
	_, err := NewPersistentRefreshTokenStore(db).AdoptLegacyRefreshTokens(ctx, options)
	require.NoError(t, err)
	cfg := &config.Config{JWT: config.JWTConfig{RefreshTokenMigrationReadiness: true}}
	for _, mutation := range []struct{ name, sql, restore string }{
		{"unfinished witness", `ALTER TABLE refresh_token_legacy_transition DISABLE TRIGGER USER; UPDATE refresh_token_legacy_transition SET state='fenced'`, `UPDATE refresh_token_legacy_transition SET state='completed'; ALTER TABLE refresh_token_legacy_transition ENABLE TRIGGER USER`},
		{"inconsistent activation", `ALTER TABLE refresh_token_authority DISABLE TRIGGER USER; UPDATE refresh_token_authority SET activated_at=activated_at+interval '1 second'`, `UPDATE refresh_token_authority SET activated_at=(SELECT completed_at FROM refresh_token_legacy_transition); ALTER TABLE refresh_token_authority ENABLE TRIGGER USER`},
		{"missing node proof", `ALTER TABLE refresh_token_transition_nodes DISABLE TRIGGER USER; UPDATE refresh_token_transition_nodes SET run_id=repeat('f',40)`, `UPDATE refresh_token_transition_nodes SET run_id=(SELECT source_run_id FROM refresh_token_legacy_transition); ALTER TABLE refresh_token_transition_nodes ENABLE TRIGGER USER`},
		{"missing schema", `ALTER TABLE refresh_token_issuances RENAME TO unavailable_issuances`, `ALTER TABLE unavailable_issuances RENAME TO refresh_token_issuances`},
	} {
		t.Run(mutation.name, func(t *testing.T) {
			// Deliberate fixture corruption; the real append-only triggers otherwise
			// prevent these changes. It is not a migration implementation.
			_, err := db.Exec(mutation.sql)
			require.NoError(t, err)
			defer func() { _, err := db.Exec(mutation.restore); require.NoError(t, err) }()
			store, err := NewRefreshTokenStore(db, clients[0], cfg)
			require.Nil(t, store)
			require.ErrorIs(t, err, ErrRefreshTokenAuthority)
		})
	}
	store, err := NewRefreshTokenStore(db, nil, cfg)
	require.NoError(t, err)
	_, err = db.Exec(`ALTER TABLE refresh_token_authority DISABLE TRIGGER USER; UPDATE refresh_token_authority SET backend='redis',activated_at=NULL`)
	require.NoError(t, err)
	require.ErrorIs(t, CheckRefreshTokenStoreReadiness(ctx, store), ErrRefreshTokenAuthority)
	_, err = store.GetRefreshToken(ctx, strings.Repeat("a", 64))
	require.ErrorIs(t, err, ErrRefreshTokenAuthority)
	require.True(t, store.(*migrationReadyRefreshStore).pgSelected.Load(), "reverse marker never resets the monotonic selection")
	_, err = db.Exec(`UPDATE refresh_token_authority SET backend='postgres',activated_at=(SELECT completed_at FROM refresh_token_legacy_transition); ALTER TABLE refresh_token_authority ENABLE TRIGGER USER`)
	require.NoError(t, err)
	require.NoError(t, CheckRefreshTokenStoreReadiness(ctx, store))
}
