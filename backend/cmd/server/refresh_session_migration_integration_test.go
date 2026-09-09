//go:build integration

package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/middleware"
	"github.com/Wei-Shaw/sub2api/internal/repository"
	"github.com/Wei-Shaw/sub2api/internal/server/routes"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/Wei-Shaw/sub2api/migrations"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcexec "github.com/testcontainers/testcontainers-go/exec"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	tcredis "github.com/testcontainers/testcontainers-go/modules/redis"
)

type migrationFailedOutput struct{}

func (migrationFailedOutput) Write([]byte) (int, error) {
	return 0, errors.New("synthetic output failure")
}

func TestRefreshMigrationRealOfflineCommand(t *testing.T) {
	for _, replicas := range []bool{false, true} {
		t.Run(fmt.Sprintf("replica-%t", replicas), func(t *testing.T) { testRefreshMigrationRealOfflineCommand(t, replicas, false, false) })
	}
}

func TestRefreshRuntimeRealBackup(t *testing.T) {
	for _, replicas := range []bool{false, true} {
		t.Run(fmt.Sprintf("replica-%t", replicas), func(t *testing.T) { testRefreshMigrationRealOfflineCommand(t, replicas, true, false) })
	}
}

func TestRefreshMigrationRealRuntimeFenceCommand(t *testing.T) {
	for _, replicas := range []bool{false, true} {
		t.Run(fmt.Sprintf("replica-%t", replicas), func(t *testing.T) { testRefreshMigrationRealOfflineCommand(t, replicas, false, true) })
	}
}

func TestRefreshRuntimeRealPreflightPersistentConfig(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	nodes := map[string]refreshMigrationNode{}
	for _, mode := range []string{"writable", "missing", "readonly"} {
		options := []testcontainers.ContainerCustomizer{
			testcontainers.WithCmdArgs("--aclfile", "/data/migration.acl", "--save", ""),
			testcontainers.WithFiles(testcontainers.ContainerFile{
				Reader:            strings.NewReader("user default on >test-legacy-password ~* &* +@all\n"),
				ContainerFilePath: "/data/migration.acl", FileMode: 0644,
			}),
		}
		if mode != "missing" {
			options = append(options, testcontainers.WithCmd("redis-server", "/data/config/redis.conf"),
				testcontainers.WithFiles(testcontainers.ContainerFile{
					Reader:            strings.NewReader("aclfile /data/migration.acl\nsave \"\"\n"),
					ContainerFilePath: "/data/config/redis.conf", FileMode: 0644,
				}))
		}
		rc, err := tcredis.Run(ctx, "redis:7.4-alpine", options...)
		testcontainers.CleanupContainer(t, rc)
		require.NoError(t, err)
		endpoint, err := rc.ConnectionString(ctx)
		require.NoError(t, err)
		opts, err := redis.ParseURL(endpoint)
		require.NoError(t, err)
		opts.Password, opts.MaxRetries = "test-legacy-password", -1
		client := redis.NewClient(opts)
		defer client.Close()
		if mode == "readonly" {
			code, _, err := rc.Exec(ctx, []string{"chmod", "0555", "/data/config"})
			require.NoError(t, err)
			require.Zero(t, code)
			code, _, err = rc.Exec(ctx, []string{"chmod", "0444", "/data/config/redis.conf"})
			require.NoError(t, err)
			require.Zero(t, code)
			require.Error(t, client.ConfigRewrite(ctx).Err(), "fixture must reject actual Redis config persistence")
		}
		info, err := client.Info(ctx, "server").Result()
		require.NoError(t, err)
		nodes[mode] = refreshMigrationNode{URL: strings.Replace(endpoint, "redis://", "redis://default:test-legacy-password@", 1), RunID: refreshRuntimeInfo(info, "run_id"), ACLUsers: []string{"default"}}
	}
	for _, tc := range []struct {
		name, primary string
		replicas      []refreshMigrationNode
		wantError     bool
	}{
		{"standalone-no-config", "missing", nil, false},
		{"standalone-readonly", "readonly", nil, false},
		{"all-writable", "writable", []refreshMigrationNode{nodes["writable"]}, false},
		{"primary-no-config", "missing", []refreshMigrationNode{nodes["writable"]}, true},
		{"primary-readonly", "readonly", []refreshMigrationNode{nodes["writable"]}, true},
		{"replica-no-config", "writable", []refreshMigrationNode{nodes["missing"]}, true},
		{"replica-readonly", "writable", []refreshMigrationNode{nodes["readonly"]}, true},
		{"later-replica-no-config", "writable", []refreshMigrationNode{nodes["writable"], nodes["missing"]}, true},
		{"later-replica-readonly", "writable", []refreshMigrationNode{nodes["writable"], nodes["readonly"]}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path, manifest := migrationTestManifest(t)
			manifest.Primary, manifest.Replicas = nodes[tc.primary], tc.replicas
			manifest.Runtime = &refreshRuntimeManifest{
				AppPasswordFile: filepath.Join(filepath.Dir(path), "app.secret"), ReplicaPasswordFile: filepath.Join(filepath.Dir(path), "replica.secret"),
				EnvironmentFile: filepath.Join(filepath.Dir(path), "runtime.env"),
			}
			require.NoError(t, os.WriteFile(manifest.Runtime.AppPasswordFile, []byte(strings.Repeat("cd", 32)), 0600))
			require.NoError(t, os.WriteFile(manifest.Runtime.ReplicaPasswordFile, []byte(strings.Repeat("ef", 32)), 0600))
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer db.Close()
			mock.ExpectQuery("SELECT transition_id::text FROM refresh_token_legacy_transition").WillReturnRows(sqlmock.NewRows([]string{"transition_id"}))
			plan, err := prepareRefreshRuntime(ctx, db, &manifest, make([]byte, 32))
			if tc.wantError {
				require.ErrorContains(t, err, "writable persistent redis.conf")
				require.Nil(t, plan)
			} else {
				require.NoError(t, err)
				require.NotNil(t, plan)
			}
			require.NoError(t, mock.ExpectationsWereMet())
			require.NoFileExists(t, manifest.Runtime.EnvironmentFile)
			for _, node := range append([]refreshMigrationNode{manifest.Primary}, manifest.Replicas...) {
				client, err := refreshRuntimeClient(node, "", "")
				require.NoError(t, err)
				defer client.Close()
				users, err := client.ACLUsers(ctx).Result()
				require.NoError(t, err, "preflight must not fence legacy credentials")
				require.Equal(t, []string{"default"}, users)
				for _, key := range []string{"masteruser", "masterauth"} {
					credentials, err := client.ConfigGet(ctx, key).Result()
					require.NoError(t, err)
					require.Equal(t, map[string]string{key: ""}, credentials)
				}
			}
		})
	}
}

func testRefreshMigrationRealOfflineCommand(t *testing.T, withReplica, withBackup, preserve bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	pg, err := tcpostgres.Run(ctx, "postgres:18.4-alpine", tcpostgres.WithDatabase("migration"),
		tcpostgres.WithUsername("test"), tcpostgres.WithPassword("test-only-password"), tcpostgres.BasicWaitStrategies())
	testcontainers.CleanupContainer(t, pg)
	require.NoError(t, err)
	dsn, err := pg.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)
	db, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	defer db.Close()
	for _, name := range []string{"238_persistent_refresh_tokens.sql", "240_refresh_token_authority_transition.sql", "241_refresh_token_transition_group_fence.sql"} {
		body, err := migrations.FS.ReadFile(name)
		require.NoError(t, err)
		_, err = db.ExecContext(ctx, string(body))
		require.NoError(t, err)
	}
	primaryOptions := []testcontainers.ContainerCustomizer{
		testcontainers.WithCmdArgs("--aclfile", "/data/migration.acl", "--save", ""),
		testcontainers.WithFiles(testcontainers.ContainerFile{
			Reader:            strings.NewReader("user default on >test-legacy-password ~* &* +@all\n"),
			ContainerFilePath: "/data/migration.acl", FileMode: 0644,
		}),
	}
	if withReplica {
		primaryOptions = append(primaryOptions, testcontainers.WithCmd("redis-server", "/data/redis.conf"),
			testcontainers.WithFiles(testcontainers.ContainerFile{
				Reader:            strings.NewReader("aclfile /data/migration.acl\nsave \"\"\nmasteruser default\nmasterauth test-legacy-password\n"),
				ContainerFilePath: "/data/redis.conf", FileMode: 0644,
			}))
	}
	moduleCommand := "no"
	if withBackup {
		// Exercise module ACL denials without the global module switch masking them.
		moduleCommand = "yes"
		primaryOptions = append(primaryOptions, testcontainers.WithCmdArgs("--enable-module-command", moduleCommand))
	}
	rc, err := tcredis.Run(ctx, "redis:7.4-alpine", primaryOptions...)
	testcontainers.CleanupContainer(t, rc)
	require.NoError(t, err)
	endpoint, err := rc.ConnectionString(ctx)
	require.NoError(t, err)
	ro, err := redis.ParseURL(endpoint)
	require.NoError(t, err)
	ro.Password, ro.MaxRetries = "test-legacy-password", -1
	rdb := redis.NewClient(ro)
	defer rdb.Close()
	var replicaContainer *tcredis.RedisContainer
	var replica *redis.Client
	var replicaEndpoint, primaryAddress, replicaAddress string
	if withReplica {
		primaryIP, err := rc.ContainerIP(ctx)
		require.NoError(t, err)
		primaryAddress = net.JoinHostPort(primaryIP, "6379")
		require.NoError(t, rdb.ConfigSet(ctx, "repl-diskless-sync-delay", "0").Err())
		replicaContainer, err = tcredis.Run(ctx, "redis:7.4-alpine",
			testcontainers.WithCmdArgs("/data/redis.conf", "--enable-module-command", moduleCommand),
			testcontainers.WithFiles(
				testcontainers.ContainerFile{Reader: strings.NewReader("user default on >test-legacy-password ~* &* +@all\n"), ContainerFilePath: "/data/migration.acl", FileMode: 0644},
				testcontainers.ContainerFile{Reader: strings.NewReader("aclfile /data/migration.acl\nsave \"\"\nreplicaof " + primaryIP + " 6379\nmasteruser default\nmasterauth test-legacy-password\n"), ContainerFilePath: "/data/redis.conf", FileMode: 0644},
			))
		testcontainers.CleanupContainer(t, replicaContainer)
		require.NoError(t, err)
		replicaEndpoint, err = replicaContainer.ConnectionString(ctx)
		require.NoError(t, err)
		opts, err := redis.ParseURL(replicaEndpoint)
		require.NoError(t, err)
		opts.Password, opts.MaxRetries = "test-legacy-password", -1
		replica = redis.NewClient(opts)
		defer replica.Close()
		ip, err := replicaContainer.ContainerIP(ctx)
		require.NoError(t, err)
		replicaAddress = net.JoinHostPort(ip, "6379")
		require.Eventually(t, func() bool {
			info, err := replica.Info(ctx, "replication").Result()
			return err == nil && refreshRuntimeInfo(info, "master_link_status") == "up" && refreshRuntimeInfo(info, "master_sync_in_progress") == "0"
		}, 25*time.Second, 50*time.Millisecond)
	}
	legacy := repository.NewRefreshTokenCache(rdb)
	now := time.Now().UTC().Truncate(time.Microsecond)
	data := &service.RefreshTokenData{UserID: 17, TokenVersion: 3, FamilyID: strings.Repeat("a", 32), BindingHash: (&service.SessionBinding{IP: "192.0.2.1", UserAgent: "migration-command"}).Hash(),
		CreatedAt: now, ExpiresAt: now.Add(2 * time.Hour), FamilyExpiresAt: now.Add(7 * 24 * time.Hour)}
	liveHash, revokedHash := strings.Repeat("b", 64), strings.Repeat("c", 64)
	for _, hash := range []string{liveHash, revokedHash} {
		require.NoError(t, legacy.StoreRefreshToken(ctx, hash, data, 2*time.Hour))
		require.NoError(t, legacy.AddToUserTokenSet(ctx, data.UserID, hash, 2*time.Hour))
		require.NoError(t, legacy.AddToFamilyTokenSet(ctx, data.FamilyID, hash, 2*time.Hour))
	}
	require.NoError(t, legacy.DeleteRefreshToken(ctx, revokedHash))
	cacheKey := "cache:unchanged"
	if preserve {
		cacheKey = "billing:balance:unchanged"
	}
	require.NoError(t, rdb.Set(ctx, cacheKey, "kept", time.Hour).Err())
	originalExpiry, err := rdb.Do(ctx, "PEXPIRETIME", cacheKey).Int64()
	require.NoError(t, err)
	info, err := rdb.Info(ctx, "server", "replication").Result()
	require.NoError(t, err)
	fields := map[string]string{}
	for _, line := range strings.Split(info, "\n") {
		if key, value, ok := strings.Cut(strings.TrimSpace(line), ":"); ok {
			fields[key] = value
		}
	}
	path, manifest := migrationTestManifest(t)
	manifest.DatabaseURL = dsn
	manifest.Primary.URL = strings.Replace(endpoint, "redis://", "redis://default:test-legacy-password@", 1)
	manifest.Primary.RunID = fields["run_id"]
	manifest.PrimaryReplicationID = fields["master_replid"]
	manifest.PrimaryAddress = ro.Addr
	appSecretFile := filepath.Join(filepath.Dir(path), "application.secret")
	require.NoError(t, os.WriteFile(appSecretFile, []byte(strings.Repeat("cd", 32)), 0600))
	manifest.Runtime = &refreshRuntimeManifest{AppPasswordFile: appSecretFile, EnvironmentFile: filepath.Join(filepath.Dir(path), "runtime.env")}
	if preserve {
		manifest.Version = 2
		manifest.SessionFence = &refreshSessionFenceManifest{PreserveRuntimeAccess: true, AuthEndpointsBlockedAndDrained: true}
	}
	if withBackup {
		manifest.Runtime.BackupPasswordFile = filepath.Join(filepath.Dir(path), "backup.secret")
		manifest.Runtime.BackupCredentialsFile = filepath.Join(filepath.Dir(path), "backup.json")
		require.NoError(t, os.WriteFile(manifest.Runtime.BackupPasswordFile, []byte(strings.Repeat("12", 32)), 0600))
	}
	installationEnv := filepath.Join(filepath.Dir(path), ".env")
	installationContents := []byte("existing installation environment must not change\n")
	require.NoError(t, os.WriteFile(installationEnv, installationContents, 0600))
	if withReplica {
		info, err := replica.Info(ctx, "server").Result()
		require.NoError(t, err)
		manifest.PrimaryAddress = primaryAddress
		manifest.Replicas = []refreshMigrationNode{{URL: strings.Replace(replicaEndpoint, "redis://", "redis://default:test-legacy-password@", 1), RunID: refreshRuntimeInfo(info, "run_id"), ReplicaAddress: replicaAddress, ACLUsers: []string{"default"}}}
		manifest.Runtime.ReplicaPasswordFile = filepath.Join(filepath.Dir(path), "replication.secret")
		require.NoError(t, os.WriteFile(manifest.Runtime.ReplicaPasswordFile, []byte(strings.Repeat("ef", 32)), 0600))
	}
	migrationWriteManifest(t, path, manifest)
	var output bytes.Buffer
	if preserve {
		_, err = db.ExecContext(ctx, `CREATE TABLE settings(key TEXT PRIMARY KEY, value TEXT)`)
		require.NoError(t, err)
		var servers []*httptest.Server
		var stores []service.RefreshTokenCache
		for _, nodeID := range []string{"main", "api2"} {
			cfg := &config.Config{JWT: config.JWTConfig{RefreshTokenMigrationReadiness: true}}
			cfg.Gateway.ExecutionNode.Enabled, cfg.Gateway.ExecutionNode.ID = true, nodeID
			store, err := repository.NewRefreshTokenStore(db, rdb, cfg)
			require.NoError(t, err)
			stores = append(stores, store)
			router := gin.New()
			routes.RegisterCommonRoutes(router, db, rdb, cfg, store)
			server := httptest.NewServer(router)
			defer server.Close()
			server.Client().Timeout = time.Second
			servers = append(servers, server)
		}
		probeRounds := 0
		checkReadiness := func(want int) {
			t.Helper()
			results := make([]struct {
				status int
				body   []byte
				err    error
			}, len(servers))
			var probes sync.WaitGroup
			for i, server := range servers {
				probes.Add(1)
				go func() {
					defer probes.Done()
					resp, err := server.Client().Get(server.URL + "/readyz")
					results[i].err = err
					if err == nil {
						defer resp.Body.Close()
						results[i].status = resp.StatusCode
						results[i].body, results[i].err = io.ReadAll(io.LimitReader(resp.Body, 4096))
					}
				}()
			}
			probes.Wait()
			for _, result := range results {
				require.NoError(t, result.err)
				require.Equal(t, want, result.status, string(result.body))
			}
			probeRounds++
		}
		checkReadiness(http.StatusOK)
		conn := rdb.Conn()
		require.NoError(t, conn.Ping(ctx).Err())
		stop, trafficDone := make(chan struct{}), make(chan error, 1)
		var cycles atomic.Int64
		go func() {
			defer conn.Close()
			for {
				select {
				case <-stop:
					trafficDone <- nil
					return
				default:
				}
				if err := conn.Incr(ctx, "billing:balance:slow-import").Err(); err != nil {
					trafficDone <- err
					return
				}
				if err := conn.Eval(ctx, `redis.call('SET',KEYS[1],'held','PX',10000); return redis.call('GET',KEYS[1])`, []string{"concurrency:account:slow-import"}).Err(); err != nil {
					trafficDone <- err
					return
				}
				cycles.Add(1)
				time.Sleep(time.Millisecond)
			}
		}()
		stopTraffic := sync.OnceFunc(func() { close(stop); require.NoError(t, <-trafficDone) })
		defer stopTraffic()
		gate, err := db.BeginTx(ctx, nil)
		require.NoError(t, err)
		defer gate.Rollback()
		_, err = gate.ExecContext(ctx, `SELECT pg_advisory_xact_lock(943721)`)
		require.NoError(t, err)
		_, err = db.ExecContext(ctx, `CREATE FUNCTION pause_readiness_import() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
			PERFORM pg_advisory_xact_lock(943721); RETURN NEW; END; $$;
			CREATE TRIGGER pause_readiness_import BEFORE INSERT ON refresh_tokens FOR EACH ROW EXECUTE FUNCTION pause_readiness_import()`)
		require.NoError(t, err)
		migrationDone := make(chan struct{})
		var migrationErr error
		go func() { migrationErr = migrateRefreshSessions(ctx, path, &output); close(migrationDone) }()
		defer func() { cancel(); <-migrationDone }()
		require.Eventually(t, func() bool {
			var waiting bool
			err := db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pg_locks WHERE locktype='advisory' AND objid=943721 AND NOT granted)`).Scan(&waiting)
			return err == nil && waiting
		}, 20*time.Second, 20*time.Millisecond)
		// The real import holds authority FOR UPDATE for longer than two ingress
		// health intervals. HTTP readiness must observe the committed Redis marker.
		for until := time.Now().Add(11 * time.Second); time.Now().Before(until); {
			checkReadiness(http.StatusOK)
			time.Sleep(200 * time.Millisecond)
		}
		for _, store := range stores {
			probe, stopProbe := context.WithTimeout(ctx, 80*time.Millisecond)
			value, err := store.ConsumeRefreshToken(probe, liveHash)
			stopProbe()
			require.Nil(t, value)
			require.ErrorIs(t, err, repository.ErrRefreshTokenAuthority, "actual Redis session IO still waits for the authority lock")
		}
		require.ErrorContains(t, rdb.Get(ctx, "refresh_token:"+liveHash).Err(), "NOPERM")
		require.NoError(t, gate.Commit())
		<-migrationDone
		require.NoError(t, migrationErr)
		checkReadiness(http.StatusOK)
		for _, store := range stores {
			got, err := store.GetRefreshToken(ctx, liveHash)
			require.NoError(t, err)
			require.Equal(t, data, got, "same provider reads the imported session after committed PG selection")
		}
		for _, mutation := range []struct{ corrupt, restore string }{
			{`ALTER TABLE refresh_token_issuances RENAME TO unavailable_issuances`, `ALTER TABLE unavailable_issuances RENAME TO refresh_token_issuances`},
			{`ALTER TABLE refresh_token_authority DISABLE TRIGGER USER; UPDATE refresh_token_authority SET backend='redis',activated_at=NULL`, `UPDATE refresh_token_authority SET backend='postgres',activated_at=(SELECT completed_at FROM refresh_token_legacy_transition); ALTER TABLE refresh_token_authority ENABLE TRIGGER USER`},
		} {
			_, err := db.ExecContext(ctx, mutation.corrupt)
			require.NoError(t, err)
			checkReadiness(http.StatusServiceUnavailable)
			_, err = db.ExecContext(ctx, mutation.restore)
			require.NoError(t, err)
			checkReadiness(http.StatusOK)
		}
		stopTraffic()
		require.Positive(t, cycles.Load())
		t.Logf("slow real import >11s: %d dual HTTP readiness rounds; normal observations all 200, missing schema/reverse authority both 503; old authenticated billing/concurrency cycles=%d, zero errors", probeRounds, cycles.Load())
	} else {
		err = migrateRefreshSessions(ctx, path, &output)
	}
	if err != nil {
		var id string
		if db.QueryRowContext(ctx, `SELECT transition_id::text FROM refresh_token_legacy_transition`).Scan(&id) == nil {
			operator, connectionErr := refreshRuntimeClient(manifest.Primary, "xiass-refresh-transition-"+id, strings.Repeat("ab", 32))
			if connectionErr == nil {
				for _, args := range [][]any{{"GET", "refresh_token:probe"}, {"FLUSHDB"}} {
					value, dryErr := operator.Do(ctx, append([]any{"ACL", "DRYRUN", "xiass-app-" + id}, args...)...).Result()
					t.Logf("synthetic ACL dry-run %v returned value=%v error=%v", args, value, dryErr)
				}
				_ = operator.Close()
			}
		}
	}
	require.NoError(t, err)
	var result repository.LegacyRefreshTransitionResult
	require.NoError(t, json.Unmarshal(output.Bytes(), &result))
	require.EqualValues(t, 1, result.Imported)
	require.NotEmpty(t, result.TransitionID)
	require.NotContains(t, output.String(), "test-legacy-password")
	require.NotContains(t, output.String(), "test-only-password")
	require.NotContains(t, output.String(), liveHash)
	if preserve {
		require.NoError(t, rdb.Ping(ctx).Err())
		require.NoError(t, rdb.Incr(ctx, "billing:balance:old-runtime").Err())
		require.ErrorContains(t, rdb.Get(ctx, "refresh_token:"+liveHash).Err(), "NOPERM")
	} else {
		require.Error(t, rdb.Ping(ctx).Err(), "old Redis credentials must remain fenced")
	}
	runtimeEnv, err := readRefreshMigrationFile(manifest.Runtime.EnvironmentFile, 4096)
	require.NoError(t, err)
	env := map[string]string{}
	for _, line := range strings.Split(string(runtimeEnv), "\n") {
		if key, value, ok := strings.Cut(line, "="); ok {
			env[key] = value
		}
	}
	require.Equal(t, "postgres", env["JWT_REFRESH_TOKEN_STORE"])
	if preserve {
		require.Equal(t, "false", env["JWT_REFRESH_TOKEN_MIGRATION_READINESS"])
		require.Len(t, env, 4)
	} else {
		require.Len(t, env, 3, "v1 output remains unchanged; backup credentials must not be injected into the app environment")
	}
	backupUser, backupPassword := "xiass-backup-"+result.TransitionID, ""
	if withBackup {
		contents, err := readRefreshMigrationFile(manifest.Runtime.BackupCredentialsFile, 4096)
		require.NoError(t, err)
		var credentials map[string]string
		require.NoError(t, json.Unmarshal(contents, &credentials))
		require.Len(t, credentials, 2)
		require.Equal(t, backupUser, credentials["username"])
		backupPassword = credentials["password"]
		require.True(t, backupPassword == strings.Repeat("12", 32), "use only the independent synthetic backup password")
		require.False(t, strings.Contains(string(runtimeEnv), backupPassword))
		require.False(t, strings.Contains(output.String(), backupPassword))
		info, err := os.Stat(manifest.Runtime.BackupCredentialsFile)
		require.NoError(t, err)
		require.Equal(t, os.FileMode(0600), info.Mode().Perm())
	}
	appOptions := *ro
	appOptions.Dialer, appOptions.Username, appOptions.Password = nil, env["REDIS_USERNAME"], env["REDIS_PASSWORD"]
	app := redis.NewClient(&appOptions)
	defer app.Close()
	require.NoError(t, app.Ping(ctx).Err())
	for _, key := range []string{"refresh_token:" + liveHash, "user_refresh_tokens:17", "token_family:" + data.FamilyID} {
		require.ErrorContains(t, app.Get(ctx, key).Err(), "NOPERM")
		require.ErrorContains(t, app.Eval(ctx, `return redis.call('GET',KEYS[1])`, []string{key}).Err(), "NOPERM")
	}
	// Exercise the unchanged real HTTP limiter, not a fake successful auth route.
	limiter := middleware.NewRateLimiter(app)
	router := gin.New()
	router.POST("/limiter-probe", limiter.LimitWithOptions("refresh-token", 30, time.Minute, middleware.RateLimitOptions{FailureMode: middleware.RateLimitFailClose}), func(c *gin.Context) { c.Status(http.StatusNoContent) })
	for i := 0; i <= 30; i++ {
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/limiter-probe", nil))
		want := http.StatusNoContent
		if i == 30 {
			want = http.StatusTooManyRequests
		}
		require.Equal(t, want, recorder.Code)
	}
	runtimeCfg := &config.Config{JWT: config.JWTConfig{RefreshTokenStore: env["JWT_REFRESH_TOKEN_STORE"], RefreshTokenMigrationReadiness: preserve}}
	if value, ok := env["JWT_REFRESH_TOKEN_MIGRATION_READINESS"]; ok {
		runtimeCfg.JWT.RefreshTokenMigrationReadiness, err = strconv.ParseBool(value)
		require.NoError(t, err)
	}
	selected, err := repository.NewRefreshTokenStore(db, rdb, runtimeCfg)
	require.NoError(t, err)
	got, err := selected.GetRefreshToken(ctx, liveHash)
	require.NoError(t, err)
	require.Equal(t, data, got, "original absolute family expiry and identity must survive adoption")
	_, err = selected.GetRefreshToken(ctx, revokedHash)
	require.ErrorIs(t, err, service.ErrRefreshTokenNotFound)
	_, secret, err := readRefreshMigrationManifest(path)
	require.NoError(t, err)
	fencedOpts := *ro
	fencedOpts.Dialer = nil
	fencedOpts.Username = "xiass-refresh-transition-" + result.TransitionID
	fencedOpts.Password = hex.EncodeToString(secret)
	fenced := redis.NewClient(&fencedOpts)
	defer fenced.Close()
	plan, err := prepareRefreshRuntime(ctx, db, &manifest, secret)
	require.NoError(t, err)
	for _, entry := range append([]refreshMigrationNode{manifest.Primary}, manifest.Replicas...) {
		operator, err := refreshRuntimeClient(entry, fencedOpts.Username, fencedOpts.Password)
		require.NoError(t, err)
		defer operator.Close()
		users, err := operator.ACLUsers(ctx).Result()
		require.NoError(t, err)
		expectedUsers := []string{"default", env["REDIS_USERNAME"], fencedOpts.Username}
		if withReplica {
			expectedUsers = append(expectedUsers, "xiass-replica-"+result.TransitionID)
		}
		if withBackup {
			expectedUsers = append(expectedUsers, backupUser)
		}
		require.ElementsMatch(t, expectedUsers, users)
		unknownUsers := []string{"unlisted-test-user", "xiass-backup-another-transition"}
		if !withBackup {
			unknownUsers = append(unknownUsers, backupUser)
		}
		for _, unknown := range unknownUsers {
			require.NoError(t, operator.ACLSetUser(ctx, unknown, "reset", "off").Err())
			require.ErrorContains(t, plan.restore(ctx, &manifest, secret, result.TransitionID), "unexpected Redis principal")
			require.NoError(t, operator.ACLDelUser(ctx, unknown).Err())
		}
		if withBackup {
			// A retry must reset stale grants, key/channel patterns, selectors and passwords.
			require.NoError(t, operator.ACLSetUser(ctx, backupUser, "+@all", "~*", "&*", "nopass", "(+get ~*)").Err())
		}
	}
	require.Equal(t, "kept", fenced.Get(ctx, cacheKey).Val())
	require.Equal(t, originalExpiry, fenced.Do(ctx, "PEXPIRETIME", cacheKey).Val())
	require.NoError(t, selected.DeleteRefreshToken(ctx, liveHash))
	output.Reset()
	require.NoError(t, migrateRefreshSessions(ctx, path, &output))
	var repeated repository.LegacyRefreshTransitionResult
	require.NoError(t, json.Unmarshal(output.Bytes(), &repeated))
	require.Equal(t, result, repeated)
	_, err = selected.GetRefreshToken(ctx, liveHash)
	require.ErrorIs(t, err, service.ErrRefreshTokenNotFound, "retries must not reimport a revoked credential")
	err = migrateRefreshSessions(ctx, path, migrationFailedOutput{})
	require.ErrorContains(t, err, "migration committed but result output failed")
	checkBackup := func(container *tcredis.RedisContainer, operator *redis.Client) {
		t.Helper()
		for _, command := range []string{"SYNC", "PSYNC"} {
			args := []any{"ACL", "DRYRUN", env["REDIS_USERNAME"], command}
			if command == "PSYNC" {
				args = append(args, "?", "-1")
			}
			denied, err := operator.Do(ctx, args...).Text()
			require.NoError(t, err)
			require.True(t, strings.HasPrefix(denied, "User "+env["REDIS_USERNAME"]+" has no permissions to "))
		}
		if withBackup {
			assertRefreshRuntimeBackupExport(t, ctx, container, operator, backupUser, backupPassword)
		}
	}
	checkBackup(rc, fenced)
	unchanged, err := os.ReadFile(installationEnv)
	require.NoError(t, err)
	require.Equal(t, installationContents, unchanged)
	if withReplica {
		require.NoError(t, app.Set(ctx, "rate_limit:runtime-replication-probe", "kept", time.Hour).Err())
		checkReplica := func() (*redis.Client, *redis.Client) {
			endpoint, err := replicaContainer.ConnectionString(ctx)
			require.NoError(t, err)
			opts, err := redis.ParseURL(endpoint)
			require.NoError(t, err)
			opts.Username, opts.Password = env["REDIS_USERNAME"], env["REDIS_PASSWORD"]
			appReplica := redis.NewClient(opts)
			opts = &redis.Options{Addr: opts.Addr, Username: "xiass-refresh-transition-" + result.TransitionID, Password: hex.EncodeToString(secret)}
			operator := redis.NewClient(opts)
			require.Eventually(t, func() bool {
				info, err := operator.Info(ctx, "replication").Result()
				return err == nil && refreshRuntimeInfo(info, "master_link_status") == "up" && appReplica.Get(ctx, "rate_limit:runtime-replication-probe").Val() == "kept"
			}, 20*time.Second, 50*time.Millisecond)
			require.ErrorContains(t, appReplica.Get(ctx, "refresh_token:"+liveHash).Err(), "NOPERM")
			config, err := operator.ConfigGet(ctx, "masteruser").Result()
			require.NoError(t, err)
			require.Equal(t, "xiass-replica-"+result.TransitionID, config["masteruser"])
			return appReplica, operator
		}
		appReplica, operator := checkReplica()
		checkBackup(replicaContainer, operator)
		require.NoError(t, appReplica.Close())
		require.NoError(t, operator.Close())
		require.NoError(t, replicaContainer.Stop(ctx, nil))
		require.NoError(t, replicaContainer.Start(ctx))
		appReplica, operator = checkReplica()
		checkBackup(replicaContainer, operator)
		defer appReplica.Close()
		defer operator.Close()
		oldOpts := *appReplica.Options()
		oldOpts.Dialer, oldOpts.Username, oldOpts.Password = nil, "default", "test-legacy-password"
		old := redis.NewClient(&oldOpts)
		defer old.Close()
		if preserve {
			require.NoError(t, old.Ping(ctx).Err())
			require.ErrorContains(t, old.Get(ctx, "refresh_token:"+liveHash).Err(), "NOPERM")
		} else {
			require.Error(t, old.Ping(ctx).Err(), "old credentials remain fenced after actual replica restart")
		}

		assertIsolation := func(app, operator *redis.Client) {
			t.Helper()
			require.NoError(t, app.Ping(ctx).Err())
			for _, key := range []string{"refresh_token:" + liveHash, "user_refresh_tokens:17", "token_family:" + data.FamilyID} {
				require.ErrorContains(t, app.Get(ctx, key).Err(), "NOPERM")
				require.ErrorContains(t, app.Eval(ctx, `return redis.call('GET',KEYS[1])`, []string{key}).Err(), "NOPERM")
			}
			require.ErrorContains(t, app.ACLUsers(ctx).Err(), "NOPERM")
			require.ErrorContains(t, app.ConfigGet(ctx, "masterauth").Err(), "NOPERM")
			for _, args := range [][]any{{"FLUSHDB"}, {"REPLICAOF", "NO", "ONE"}, {"CONFIG", "REWRITE"}, {"CONFIG", "SET", "masteruser", "default"}} {
				dryRun, err := operator.Do(ctx, append([]any{"ACL", "DRYRUN", env["REDIS_USERNAME"]}, args...)...).Text()
				require.NoError(t, err)
				require.True(t, strings.HasPrefix(dryRun, "User "+env["REDIS_USERNAME"]+" has no permissions to "))
			}
			state := redis.NewMapStringInterfaceCmd(ctx, "ACL", "GETUSER", "default")
			require.NoError(t, operator.Process(ctx, state))
			if !preserve {
				require.True(t, refreshRuntimeOldUserFenced(state.Val()))
			}
			legacy := redis.NewClient(&redis.Options{Addr: operator.Options().Addr, Username: "default", Password: "test-legacy-password", MaxRetries: -1})
			defer legacy.Close()
			if preserve {
				require.NoError(t, legacy.Ping(ctx).Err())
				require.ErrorContains(t, legacy.Get(ctx, "refresh_token:"+liveHash).Err(), "NOPERM")
				require.ErrorContains(t, legacy.ACLUsers(ctx).Err(), "NOPERM")
			} else {
				require.ErrorContains(t, legacy.Ping(ctx).Err(), "WRONGPASS")
			}
			for key, want := range map[string]string{"masteruser": "xiass-replica-" + result.TransitionID, "masterauth": strings.Repeat("ef", 32)} {
				credentials, err := operator.ConfigGet(ctx, key).Result()
				require.NoError(t, err)
				require.Equal(t, map[string]string{key: want}, credentials)
			}
		}
		assertIsolation(app, fenced)
		assertIsolation(appReplica, operator)
		// Inspect migration persistence before a topology rewrite could hide a missing save.
		for _, container := range []*tcredis.RedisContainer{rc, replicaContainer} {
			file, err := container.CopyFileFromContainer(ctx, "/data/redis.conf")
			require.NoError(t, err)
			contents, err := io.ReadAll(file)
			require.NoError(t, err)
			require.NoError(t, file.Close())
			require.Regexp(t, `(?m)^masteruser "?xiass-replica-`+result.TransitionID+`"?\r?$`, string(contents))
			require.Regexp(t, `(?m)^masterauth "?`+strings.Repeat("ef", 32)+`"?\r?$`, string(contents))
		}
		require.NoError(t, operator.ConfigSet(ctx, "repl-diskless-sync-delay", "0").Err())
		require.NoError(t, operator.Do(ctx, "REPLICAOF", "NO", "ONE").Err())
		info, err := operator.Info(ctx, "replication").Result()
		require.NoError(t, err)
		require.Equal(t, "master", refreshRuntimeInfo(info, "role"))
		require.NoError(t, operator.ConfigRewrite(ctx).Err())
		replicaIP, err := replicaContainer.ContainerIP(ctx)
		require.NoError(t, err)
		require.NoError(t, fenced.Do(ctx, "REPLICAOF", replicaIP, "6379").Err())
		require.NoError(t, fenced.ConfigRewrite(ctx).Err())
		checkFormerPrimary := func(formerApp, formerOperator *redis.Client, value string) {
			t.Helper()
			require.NoError(t, appReplica.Set(ctx, "rate_limit:runtime-failover-probe", value, time.Hour).Err())
			require.Eventually(t, func() bool {
				info, err := formerOperator.Info(ctx, "replication").Result()
				got, readErr := formerApp.Get(ctx, "rate_limit:runtime-failover-probe").Result()
				return err == nil && refreshRuntimeInfo(info, "role") == "slave" && refreshRuntimeInfo(info, "master_host") == replicaIP && refreshRuntimeInfo(info, "master_link_status") == "up" && refreshRuntimeInfo(info, "master_sync_in_progress") == "0" && readErr == nil && got == value
			}, 25*time.Second, 50*time.Millisecond)
			assertIsolation(formerApp, formerOperator)
			assertIsolation(appReplica, operator)
		}
		checkFormerPrimary(app, fenced, "after-promotion")
		checkBackup(replicaContainer, operator)
		checkBackup(rc, fenced)
		require.NoError(t, rc.Stop(ctx, nil))
		require.NoError(t, rc.Start(ctx))
		endpoint, err := rc.ConnectionString(ctx)
		require.NoError(t, err)
		opts, err := redis.ParseURL(endpoint)
		require.NoError(t, err)
		opts.Username, opts.Password = env["REDIS_USERNAME"], env["REDIS_PASSWORD"]
		restartedApp := redis.NewClient(opts)
		defer restartedApp.Close()
		restartedOperator := redis.NewClient(&redis.Options{Addr: opts.Addr, Username: fencedOpts.Username, Password: fencedOpts.Password, MaxRetries: -1})
		defer restartedOperator.Close()
		checkFormerPrimary(restartedApp, restartedOperator, "after-former-primary-restart")
		checkBackup(rc, restartedOperator)
		info, err = restartedOperator.Info(ctx, "server").Result()
		require.NoError(t, err)
		require.NotEqual(t, manifest.Primary.RunID, refreshRuntimeInfo(info, "run_id"), "must exercise an actual Redis process restart")
	}
}

func assertRefreshRuntimeBackupExport(t *testing.T, ctx context.Context, container *tcredis.RedisContainer, operator *redis.Client, username, password string) {
	t.Helper()
	state := redis.NewMapStringInterfaceCmd(ctx, "ACL", "GETUSER", username)
	require.NoError(t, operator.Process(ctx, state))
	acl := state.Val()
	require.Contains(t, acl["flags"], "on")
	require.NotContains(t, acl["flags"], "nopass")
	require.Equal(t, "", acl["keys"])
	require.Equal(t, "", acl["channels"])
	require.Empty(t, acl["selectors"])
	passwords, ok := acl["passwords"].([]any)
	require.True(t, ok)
	require.Len(t, passwords, 1)
	require.True(t, passwords[0] == refreshRuntimeHash(password), "only the backup password hash may be stored")
	require.ElementsMatch(t, []string{"-@all", "+auth", "+hello", "+ping", "+info", "+role", "+sync", "+replconf", "+acl|list", "+module|list"}, strings.Fields(acl["commands"].(string)))
	client := redis.NewClient(&redis.Options{Addr: operator.Options().Addr, Username: username, Password: password, MaxRetries: -1})
	defer client.Close()
	require.NoError(t, client.Ping(ctx).Err())
	require.NoError(t, client.Do(ctx, "AUTH", username, password).Err())
	require.NoError(t, client.Do(ctx, "HELLO", 3).Err())
	require.NoError(t, client.Info(ctx, "server").Err())
	require.NoError(t, client.Do(ctx, "ROLE").Err())
	listing, err := client.Do(ctx, "ACL", "LIST").StringSlice()
	require.NoError(t, err)
	require.False(t, strings.Contains(strings.Join(listing, "\n"), password), "ACL export must not contain plaintext credentials")
	for _, args := range [][]string{{"INFO", "server"}, {"INFO", "replication"}, {"ROLE"}, {"ACL", "LIST"}, {"MODULE", "LIST"}} {
		code, output, err := container.Exec(ctx, append([]string{"redis-cli", "-e", "--json", "--user", username}, args...),
			tcexec.WithEnv([]string{"REDISCLI_AUTH=" + password}), tcexec.Multiplexed())
		require.NoError(t, err)
		require.Zero(t, code, "metadata export must allow %v", args)
		contents, err := io.ReadAll(output)
		require.NoError(t, err)
		require.False(t, bytes.Contains(contents, []byte(password)))
		// Redis 7.4 forces INFO to raw text even when --json is selected.
		if args[0] == "INFO" {
			field := "role"
			if args[1] == "server" {
				field = "redis_version"
				t.Logf("backup metadata exported with Redis %s", refreshRuntimeInfo(string(contents), field))
			}
			require.NotEmpty(t, refreshRuntimeInfo(string(contents), field))
			continue
		}
		var metadata any
		require.NoError(t, json.Unmarshal(contents, &metadata), "redis-cli metadata must be valid JSON")
		require.NotNil(t, metadata)
	}
	for _, args := range [][]any{
		{"GET", "cache:unchanged"}, {"SET", "cache:unchanged", "must-not-write"}, {"DEL", "cache:unchanged"},
		{"EVAL", "return 1", 0}, {"PUBLISH", "backup-test", "probe"}, {"SUBSCRIBE", "backup-test"},
		{"ACL", "SETUSER", "backup-must-not-create", "reset", "off"}, {"ACL", "DELUSER", "backup-must-not-create"},
		{"ACL", "SAVE"}, {"ACL", "GETUSER", username}, {"ACL", "USERS"},
		{"MODULE", "LOAD", "/data/must-not-load.so"}, {"MODULE", "LOADEX", "/data/must-not-load.so"}, {"MODULE", "UNLOAD", "must-not-unload"},
		{"CONFIG", "GET", "masterauth"}, {"CONFIG", "SET", "maxmemory", "0"}, {"CONFIG", "REWRITE"},
		{"REPLICAOF", "NO", "ONE"}, {"SLAVEOF", "NO", "ONE"}, {"FAILOVER"},
		{"FLUSHDB"}, {"FLUSHALL"}, {"SAVE"}, {"BGSAVE"}, {"PSYNC", "?", "-1"}, {"SELECT", "1"},
	} {
		require.ErrorContains(t, client.Do(ctx, args...).Err(), "NOPERM", "backup must reject %v", args)
	}
	before, err := operator.Info(ctx, "commandstats").Result()
	require.NoError(t, err)
	const rdbPath = "/data/runtime-backup.rdb"
	code, output, err := container.Exec(ctx, []string{"redis-cli", "--user", username, "--rdb", rdbPath},
		tcexec.WithEnv([]string{"REDISCLI_AUTH=" + password}), tcexec.Multiplexed())
	require.NoError(t, err)
	require.Zero(t, code, "redis-cli --rdb must succeed with the independent backup ACL")
	cliOutput, err := io.ReadAll(output)
	require.NoError(t, err)
	require.False(t, bytes.Contains(cliOutput, []byte(password)))
	require.True(t, bytes.Contains(cliOutput, []byte("Transfer finished with success")))
	after, err := operator.Info(ctx, "commandstats").Result()
	require.NoError(t, err)
	for _, command := range []string{"auth", "replconf", "sync"} {
		callsBefore, _, _ := strings.Cut(refreshRuntimeInfo(before, "cmdstat_"+command), ",")
		callsAfter, _, _ := strings.Cut(refreshRuntimeInfo(after, "cmdstat_"+command), ",")
		require.NotEmpty(t, callsAfter)
		require.NotEqual(t, callsBefore, callsAfter, "redis-cli --rdb must exercise %s", command)
	}
	file, err := container.CopyFileFromContainer(ctx, rdbPath)
	require.NoError(t, err)
	contents, err := io.ReadAll(file)
	require.NoError(t, err)
	require.NoError(t, file.Close())
	require.True(t, bytes.HasPrefix(contents, []byte("REDIS")), "export must have a real RDB header")
	require.True(t, bytes.Contains(contents, []byte("cache:unchanged")), "RDB must contain the unchanged synthetic cache fixture")
	code, output, err = container.Exec(ctx, []string{"redis-check-rdb", rdbPath}, tcexec.Multiplexed())
	require.NoError(t, err)
	require.Zero(t, code, "exported RDB must pass Redis's checksum and structure checks")
	checked, err := io.ReadAll(output)
	require.NoError(t, err)
	require.True(t, bytes.Contains(checked, []byte("RDB looks OK")))
	t.Log("backup: redis-cli -e --json INFO server/replication, ROLE, ACL LIST, MODULE LIST exit=0; redis-cli --rdb exit=0; AUTH/REPLCONF/SYNC observed; redis-check-rdb exit=0; 26 forbidden commands returned NOPERM")
}
