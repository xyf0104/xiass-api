package routes

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/alicebob/miniredis/v2"
	"github.com/alicebob/miniredis/v2/server"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestClusterReadinessRejectsReachableReadOnlyStores(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		name       string
		cluster    bool
		pgWritable bool
		redisRole  string
		wantStatus int
		failed     string
	}{
		{"writable stores", true, true, "master", http.StatusOK, ""},
		{"postgres standby", true, false, "master", http.StatusServiceUnavailable, "postgres"},
		{"redis replica", true, true, "slave", http.StatusServiceUnavailable, "redis"},
		{"unknown redis role", true, true, "sentinel", http.StatusServiceUnavailable, "redis"},
		{"role ACL denied", true, true, "denied", http.StatusServiceUnavailable, "redis"},
		{"standalone retains ping compatibility", false, true, "denied", http.StatusOK, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, mock, err := sqlmock.New(sqlmock.MonitorPingsOption(true))
			require.NoError(t, err)
			t.Cleanup(func() { _ = db.Close() })
			mock.MatchExpectationsInOrder(false)
			if tc.cluster {
				mock.ExpectQuery("SELECT NOT pg_is_in_recovery").
					WillReturnRows(sqlmock.NewRows([]string{"writable", "backend", "schema_ready"}).AddRow(tc.pgWritable, "redis", true))
			} else {
				mock.ExpectPing()
			}
			mock.ExpectQuery("SELECT value FROM settings").
				WillReturnRows(sqlmock.NewRows([]string{"value"}).AddRow("false"))

			mr := miniredis.RunT(t)
			require.NoError(t, mr.Server().Register("ROLE", func(peer *server.Peer, _ string, _ []string) {
				if tc.redisRole == "denied" {
					peer.WriteError("NOPERM ROLE disabled")
					return
				}
				peer.WriteLen(3)
				peer.WriteBulk(tc.redisRole)
				peer.WriteInt(0)
				peer.WriteLen(0)
			}))
			client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
			t.Cleanup(func() { _ = client.Close() })
			cfg := &config.Config{}
			cfg.Gateway.ExecutionNode.Enabled = tc.cluster
			cfg.Gateway.ExecutionNode.ID = "api2"
			router := gin.New()
			RegisterCommonRoutes(router, db, client, cfg)
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/readyz", nil))
			require.Equal(t, tc.wantStatus, recorder.Code, recorder.Body.String())
			if tc.failed != "" {
				var payload struct {
					Checks map[string]string `json:"checks"`
				}
				require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &payload))
				require.Equal(t, "unavailable", payload.Checks[tc.failed])
			}
			require.NotContains(t, recorder.Body.String(), "NOPERM")
			require.Empty(t, mr.Keys(), "readiness must not mutate shared Redis state")
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}
