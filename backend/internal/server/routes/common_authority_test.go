package routes

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/repository"
	"github.com/alicebob/miniredis/v2"
	"github.com/alicebob/miniredis/v2/server"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func readinessAuthorityQuery(store string) string {
	schema := "TRUE"
	if store == "postgres" {
		schema = `EXISTS (SELECT 1 FROM refresh_token_revocation_state WHERE singleton = TRUE)
			AND to_regclass('refresh_tokens') IS NOT NULL
			AND to_regclass('refresh_token_families') IS NOT NULL
			AND to_regclass('refresh_token_users') IS NOT NULL
			AND to_regclass('refresh_token_issuances') IS NOT NULL`
	}
	return `SELECT NOT pg_is_in_recovery() AND current_setting('transaction_read_only') = 'off',
		backend, ` + schema + ` FROM refresh_token_authority WHERE singleton = TRUE`
}

func readinessAuthorityRedis(t *testing.T) *redis.Client {
	t.Helper()
	mr := miniredis.RunT(t)
	require.NoError(t, mr.Server().Register("ROLE", func(peer *server.Peer, _ string, _ []string) {
		peer.WriteLen(3)
		peer.WriteBulk("master")
		peer.WriteInt(0)
		peer.WriteLen(0)
	}))
	client := redis.NewClient(&redis.Options{Addr: mr.Addr(), MaxRetries: -1})
	t.Cleanup(func() {
		require.Empty(t, mr.Keys(), "readiness must not write Redis data")
		_ = client.Close()
	})
	return client
}

func assertAuthorityReadiness(t *testing.T, router *gin.Engine, status int) {
	t.Helper()
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	require.Equal(t, status, recorder.Code, recorder.Body.String())
	require.Equal(t, "no-store", recorder.Header().Get("Cache-Control"))
	require.Equal(t, "api2", recorder.Header().Get("X-XIASS-Execution-Node"))
	var payload struct {
		Status        string            `json:"status"`
		ExecutionNode string            `json:"execution_node"`
		Checks        map[string]string `json:"checks"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &payload))
	want := "ok"
	if status != http.StatusOK {
		want = "unavailable"
	}
	require.Equal(t, want, payload.Status)
	require.Equal(t, "api2", payload.ExecutionNode)
	require.Equal(t, map[string]string{"postgres": want, "redis": "ok", "execution_node": "ok"}, payload.Checks)
	for _, private := range []string{"synthetic-secret", "refresh_token", "permission denied", "postgres://", "does not exist"} {
		require.NotContains(t, recorder.Body.String(), private)
	}
}

func TestClusterReadinessRefreshAuthority(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		name, store string
		authority   any
		schemaReady any
		missing     bool
		queryError  error
		wantStatus  int
	}{
		{name: "default Redis", authority: "redis", schemaReady: true, wantStatus: http.StatusOK},
		{name: "explicit Redis", store: "redis", authority: "redis", schemaReady: true, wantStatus: http.StatusOK},
		{name: "Postgres", store: "postgres", authority: "postgres", schemaReady: true, wantStatus: http.StatusOK},
		{name: "Redis instance after migration", store: "redis", authority: "postgres", schemaReady: true},
		{name: "PG configured before migration", store: "postgres", authority: "redis", schemaReady: true},
		{name: "missing Redis authority", store: "redis", missing: true},
		{name: "missing PG authority", store: "postgres", missing: true},
		{name: "null authority", store: "postgres", schemaReady: true},
		{name: "unknown authority", store: "redis", authority: "unknown", schemaReady: true},
		{name: "incomplete persistent schema or revocation row", store: "postgres", authority: "postgres", schemaReady: false},
		{name: "null schema result", store: "postgres", authority: "postgres"},
		{name: "malformed schema result", store: "postgres", authority: "postgres", schemaReady: "not-a-boolean"},
		{name: "missing authority relation", store: "redis", queryError: errors.New("relation refresh_token_authority does not exist")},
		{name: "missing persistent relation", store: "postgres", queryError: errors.New("relation refresh_token_revocation_state does not exist")},
		{name: "unreadable schema", store: "postgres", queryError: errors.New("permission denied for refresh_token_authority")},
		{name: "connection failure", store: "redis", queryError: errors.New("postgres://test:synthetic-secret@fixture.invalid unavailable")},
		{name: "invalid configured store", store: "unknown"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual), sqlmock.MonitorPingsOption(true))
			require.NoError(t, err)
			t.Cleanup(func() { _ = db.Close() })
			mock.MatchExpectationsInOrder(false)
			if tc.store != "unknown" {
				query := mock.ExpectQuery(readinessAuthorityQuery(tc.store))
				if tc.queryError != nil {
					query.WillReturnError(tc.queryError)
				} else {
					rows := sqlmock.NewRows([]string{"writable", "backend", "schema_ready"})
					if !tc.missing {
						rows.AddRow(true, tc.authority, tc.schemaReady)
					}
					query.WillReturnRows(rows)
				}
			}
			mock.ExpectQuery("SELECT value FROM settings WHERE key = 'execution_node_balancing_enabled'").
				WillReturnRows(sqlmock.NewRows([]string{"value"}).AddRow("false"))
			cfg := &config.Config{JWT: config.JWTConfig{RefreshTokenStore: tc.store}}
			cfg.Gateway.ExecutionNode.Enabled = true
			cfg.Gateway.ExecutionNode.ID = " api2 "
			router := gin.New()
			RegisterCommonRoutes(router, db, readinessAuthorityRedis(t), cfg)
			status := tc.wantStatus
			if status == 0 {
				status = http.StatusServiceUnavailable
			}
			assertAuthorityReadiness(t, router, status)
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestClusterReadinessRefreshAuthorityKeepsStartupStore(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, store := range []string{"redis", "postgres"} {
		t.Run(store, func(t *testing.T) {
			db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual), sqlmock.MonitorPingsOption(true))
			require.NoError(t, err)
			t.Cleanup(func() { _ = db.Close() })
			mock.MatchExpectationsInOrder(false)
			cfg := &config.Config{JWT: config.JWTConfig{RefreshTokenStore: store}}
			cfg.Gateway.ExecutionNode.Enabled = true
			cfg.Gateway.ExecutionNode.ID = "api2"
			router := gin.New()
			RegisterCommonRoutes(router, db, readinessAuthorityRedis(t), cfg)
			other := "postgres"
			if store == "postgres" {
				other = "redis"
			}
			for i, authority := range []string{store, other} {
				if i == 1 {
					// Neither changing cfg nor the environment replaces the live provider.
					cfg.JWT.RefreshTokenStore = other
					t.Setenv("JWT_REFRESH_TOKEN_STORE", other)
				}
				mock.ExpectQuery(readinessAuthorityQuery(store)).
					WillReturnRows(sqlmock.NewRows([]string{"writable", "backend", "schema_ready"}).AddRow(true, authority, true))
				mock.ExpectQuery("SELECT value FROM settings WHERE key = 'execution_node_balancing_enabled'").
					WillReturnRows(sqlmock.NewRows([]string{"value"}).AddRow("false"))
				status := http.StatusOK
				if i == 1 {
					status = http.StatusServiceUnavailable
				}
				assertAuthorityReadiness(t, router, status)
				require.NoError(t, mock.ExpectationsWereMet())
			}
		})
	}
}

func TestClusterReadinessRefreshAuthorityHonorsContext(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	mock.ExpectQuery(readinessAuthorityQuery("postgres")).WillDelayFor(time.Second).
		WillReturnRows(sqlmock.NewRows([]string{"writable", "backend", "schema_ready"}).AddRow(true, "postgres", true))
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	started := time.Now()
	require.Error(t, checkWritablePostgres(ctx, db, "postgres"))
	require.Less(t, time.Since(started), 500*time.Millisecond)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestClusterReadinessUsesRunningRefreshProvider(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		name, authority string
		wantStatus      int
	}{
		{"real Redis provider overrides changed config", "redis", http.StatusOK},
		{"changed config cannot upgrade a running Redis provider", "postgres", http.StatusServiceUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			t.Cleanup(func() { _ = db.Close() })
			client := readinessAuthorityRedis(t)
			mock.ExpectQuery("SELECT backend, pg_is_in_recovery").
				WillReturnRows(sqlmock.NewRows([]string{"backend", "recovery", "read_only"}).AddRow("redis", false, false))
			provider, err := repository.NewRefreshTokenStore(db, client, &config.Config{})
			require.NoError(t, err)
			cfg := &config.Config{JWT: config.JWTConfig{RefreshTokenStore: "postgres"}}
			cfg.Gateway.ExecutionNode.Enabled = true
			cfg.Gateway.ExecutionNode.ID = "api2"
			router := gin.New()
			RegisterCommonRoutes(router, db, client, cfg, provider)
			cfg.JWT.RefreshTokenStore = "redis"
			cfg.JWT.RefreshTokenMigrationReadiness = true
			mock.MatchExpectationsInOrder(false)
			mock.ExpectQuery("SELECT backend, pg_is_in_recovery").
				WillReturnRows(sqlmock.NewRows([]string{"backend", "recovery", "read_only"}).AddRow(tc.authority, false, false))
			mock.ExpectQuery("SELECT value FROM settings").
				WillReturnRows(sqlmock.NewRows([]string{"value"}).AddRow("false"))
			assertAuthorityReadiness(t, router, tc.wantStatus)
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestClusterReadinessMigrationRequiresRunningProvider(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, withLegacyCache := range []bool{false, true} {
		t.Run(map[bool]string{false: "provider absent", true: "unguarded legacy cache"}[withLegacyCache], func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			t.Cleanup(func() { _ = db.Close() })
			cfg := &config.Config{JWT: config.JWTConfig{RefreshTokenMigrationReadiness: true}}
			cfg.Gateway.ExecutionNode.Enabled = true
			cfg.Gateway.ExecutionNode.ID = "api2"
			client := readinessAuthorityRedis(t)
			router := gin.New()
			if withLegacyCache {
				RegisterCommonRoutes(router, db, client, cfg, repository.NewRefreshTokenCache(client))
			} else {
				RegisterCommonRoutes(router, db, client, cfg)
			}
			mock.ExpectQuery("SELECT value FROM settings").
				WillReturnRows(sqlmock.NewRows([]string{"value"}).AddRow("false"))
			assertAuthorityReadiness(t, router, http.StatusServiceUnavailable)
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}
