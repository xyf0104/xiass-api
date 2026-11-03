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
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestReadinessRouteReportsDependencies(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	registerReadinessRoute(router, []readinessProbe{
		{name: "postgres", run: func(context.Context) error { return nil }},
		{name: "redis", run: func(context.Context) error { return nil }},
	}, time.Second)

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/readyz", nil))

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, "no-store", recorder.Header().Get("Cache-Control"))
	require.Empty(t, recorder.Header().Get("X-XIASS-Execution-Node"))
	var payload map[string]any
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &payload))
	require.Equal(t, "ok", payload["status"])
	_, hasExecutionNode := payload["execution_node"]
	require.False(t, hasExecutionNode)
	require.Equal(t, map[string]any{"postgres": "ok", "redis": "ok"}, payload["checks"])
}

func TestReadinessRouteFailsClosedWithoutExposingDependencyError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	registerReadinessRoute(router, []readinessProbe{
		{name: "postgres", run: func(context.Context) error { return errors.New("secret database address") }},
		{name: "redis", run: func(context.Context) error { return nil }},
	}, time.Second)

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/readyz", nil))

	require.Equal(t, http.StatusServiceUnavailable, recorder.Code)
	require.NotContains(t, recorder.Body.String(), "secret database address")
	var payload map[string]any
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &payload))
	require.Equal(t, "unavailable", payload["status"])
	require.Equal(t, map[string]any{"postgres": "unavailable", "redis": "ok"}, payload["checks"])
}

func TestReadinessRouteHonorsOverallTimeout(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	registerReadinessRoute(router, []readinessProbe{
		{name: "postgres", run: func(ctx context.Context) error {
			<-ctx.Done()
			return ctx.Err()
		}},
		{name: "redis", run: func(context.Context) error { return nil }},
	}, 20*time.Millisecond)

	recorder := httptest.NewRecorder()
	started := time.Now()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/readyz", nil))

	require.Equal(t, http.StatusServiceUnavailable, recorder.Code)
	require.Less(t, time.Since(started), 500*time.Millisecond)
}

func TestSingleNodeReadinessIgnoresLegacyNodeEnvironment(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("GATEWAY_EXECUTION_NODE_ENABLED", "true")
	t.Setenv("GATEWAY_EXECUTION_NODE_ID", "api2")
	t.Setenv("GATEWAY_EXECUTION_NODE_DEFAULT_PROXY_ID", "83")
	t.Setenv("XIASS_CLUSTER_ENABLED", "true")
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	// Only the session authority query is expected: no node settings or proxies.
	mock.ExpectQuery(readinessAuthorityQuery("redis")).
		WillReturnRows(sqlmock.NewRows([]string{"writable", "backend", "schema_ready"}).AddRow(true, "redis", true))
	router := gin.New()
	RegisterCommonRoutes(router, db, readinessAuthorityRedis(t), &config.Config{})
	assertAuthorityReadiness(t, router, http.StatusOK)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestSingleNodeReadinessMissingDependencies(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Run("postgres missing", func(t *testing.T) {
		router := gin.New()
		RegisterCommonRoutes(router, nil, readinessAuthorityRedis(t), nil)
		assertAuthorityReadiness(t, router, http.StatusServiceUnavailable)
	})
	t.Run("redis missing", func(t *testing.T) {
		db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
		require.NoError(t, err)
		t.Cleanup(func() { _ = db.Close() })
		mock.ExpectQuery(readinessAuthorityQuery("redis")).
			WillReturnRows(sqlmock.NewRows([]string{"writable", "backend", "schema_ready"}).AddRow(true, "redis", true))
		router := gin.New()
		RegisterCommonRoutes(router, db, nil, nil)
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/readyz", nil))
		require.Equal(t, http.StatusServiceUnavailable, recorder.Code)
		require.JSONEq(t, `{"status":"unavailable","checks":{"postgres":"ok","redis":"unavailable"}}`, recorder.Body.String())
		require.NoError(t, mock.ExpectationsWereMet())
	})
}
