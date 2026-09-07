//go:build unit

package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type executionNodeWriteRepo struct{ values map[string]string }

func (r *executionNodeWriteRepo) Get(context.Context, string) (*service.Setting, error) {
	return nil, service.ErrSettingNotFound
}
func (r *executionNodeWriteRepo) GetValue(_ context.Context, key string) (string, error) {
	value, ok := r.values[key]
	if !ok {
		return "", service.ErrSettingNotFound
	}
	return value, nil
}
func (r *executionNodeWriteRepo) Set(context.Context, string, string) error { return nil }
func (r *executionNodeWriteRepo) GetMultiple(context.Context, []string) (map[string]string, error) {
	return map[string]string{}, nil
}
func (r *executionNodeWriteRepo) SetMultiple(context.Context, map[string]string) error { return nil }
func (r *executionNodeWriteRepo) GetAll(context.Context) (map[string]string, error) {
	return map[string]string{}, nil
}
func (r *executionNodeWriteRepo) Delete(context.Context, string) error { return nil }

type executionNodeWriteHealth bool

func (h executionNodeWriteHealth) HealthyExecutionNodes(_ context.Context, nodeIDs []string) (map[string]bool, error) {
	result := make(map[string]bool, len(nodeIDs))
	for _, nodeID := range nodeIDs {
		result[nodeID] = bool(h)
	}
	return result, nil
}

func TestExecutionNodeSharedWriteGuardKeepsReadsAndBlocksSecondaryWrites(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfg := &config.Config{}
	cfg.Gateway.ExecutionNode = config.GatewayExecutionNodeConfig{Enabled: true, ID: "api2", LegacyUnassignedNodeID: "api", EmergencyLocalEgress: true}
	svc := service.NewSettingService(&executionNodeWriteRepo{values: map[string]string{"execution_node_emergency_egress:api2": "true"}}, cfg)
	svc.SetExecutionNodeHealthReader(executionNodeWriteHealth(true))

	router := gin.New()
	router.Use(ExecutionNodeSharedWriteGuard(svc))
	router.GET("/groups", func(c *gin.Context) { c.Status(http.StatusOK) })
	router.PUT("/groups/1", func(c *gin.Context) { c.Status(http.StatusNoContent) })

	read := httptest.NewRecorder()
	router.ServeHTTP(read, httptest.NewRequest(http.MethodGet, "/groups", nil))
	require.Equal(t, http.StatusOK, read.Code)

	write := httptest.NewRecorder()
	router.ServeHTTP(write, httptest.NewRequest(http.MethodPut, "/groups/1", nil))
	require.Equal(t, http.StatusForbidden, write.Code)
	require.Contains(t, write.Body.String(), "EXECUTION_NODE_ADMIN_READ_ONLY")
}

func TestExecutionNodeSharedWriteGuardAllowsClusterSMSRuntimeOperations(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfg := &config.Config{}
	cfg.Gateway.ExecutionNode = config.GatewayExecutionNodeConfig{Enabled: true, ID: "api2", LegacyUnassignedNodeID: "api", EmergencyLocalEgress: true}
	svc := service.NewSettingService(&executionNodeWriteRepo{values: map[string]string{"execution_node_emergency_egress:api2": "true"}}, cfg)
	svc.SetExecutionNodeHealthReader(executionNodeWriteHealth(true))

	router := gin.New()
	router.Use(ExecutionNodeSharedWriteGuard(svc))
	router.POST("/api/v1/admin/settings/sms-receiver/redeem", func(c *gin.Context) { c.Status(http.StatusNoContent) })
	router.POST("/api/v1/admin/settings/sms-receiver/sessions/:session_id/resume", func(c *gin.Context) { c.Status(http.StatusNoContent) })
	router.POST("/api/v1/admin/settings/sms-receiver/sessions/:session_id/check", func(c *gin.Context) { c.Status(http.StatusNoContent) })
	router.POST("/api/v1/admin/settings/sms-receiver/sessions/:session_id/change", func(c *gin.Context) { c.Status(http.StatusNoContent) })
	router.POST("/api/v1/admin/settings/sms-receiver/sessions/:session_id/cancel", func(c *gin.Context) { c.Status(http.StatusNoContent) })
	router.POST("/api/v1/admin/settings/sms-receiver/card-keys", func(c *gin.Context) { c.Status(http.StatusNoContent) })

	for _, path := range []string{
		"/api/v1/admin/settings/sms-receiver/redeem",
		"/api/v1/admin/settings/sms-receiver/sessions/session-one/resume",
		"/api/v1/admin/settings/sms-receiver/sessions/session-one/check",
		"/api/v1/admin/settings/sms-receiver/sessions/session-one/change",
		"/api/v1/admin/settings/sms-receiver/sessions/session-one/cancel",
	} {
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, path, nil))
		require.Equal(t, http.StatusNoContent, recorder.Code, path)
	}

	cardKeyWrite := httptest.NewRecorder()
	router.ServeHTTP(cardKeyWrite, httptest.NewRequest(http.MethodPost, "/api/v1/admin/settings/sms-receiver/card-keys", nil))
	require.Equal(t, http.StatusForbidden, cardKeyWrite.Code)
	require.Contains(t, cardKeyWrite.Body.String(), "EXECUTION_NODE_ADMIN_READ_ONLY")
}

func TestExecutionNodeSharedWriteGuardAllowsEmergencyTakeover(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfg := &config.Config{}
	cfg.Gateway.ExecutionNode = config.GatewayExecutionNodeConfig{Enabled: true, ID: "api2", LegacyUnassignedNodeID: "api", EmergencyLocalEgress: true}
	svc := service.NewSettingService(&executionNodeWriteRepo{values: map[string]string{"execution_node_emergency_egress:api2": "true"}}, cfg)
	svc.SetExecutionNodeHealthReader(executionNodeWriteHealth(false))

	router := gin.New()
	router.Use(ExecutionNodeSharedWriteGuard(svc))
	router.PUT("/groups/1", func(c *gin.Context) { c.Status(http.StatusNoContent) })

	write := httptest.NewRecorder()
	router.ServeHTTP(write, httptest.NewRequest(http.MethodPut, "/groups/1", nil))
	require.Equal(t, http.StatusNoContent, write.Code)
}
