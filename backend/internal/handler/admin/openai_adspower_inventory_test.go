package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type openAIAdsPowerInventoryAdminStub struct {
	service.AdminService
	inventory    *service.OpenAIAdsPowerProfileInventory
	inventoryErr error
	released     bool
	releaseCalls int
}

func (s *openAIAdsPowerInventoryAdminStub) ListOpenAIAdsPowerProfileInventory(context.Context, string, string) (*service.OpenAIAdsPowerProfileInventory, error) {
	return s.inventory, s.inventoryErr
}

func (s *openAIAdsPowerInventoryAdminStub) ReleaseOpenAIAdsPowerProfile(context.Context, string, string, string) (bool, error) {
	s.releaseCalls++
	return s.released, s.inventoryErr
}

func openAIAdsPowerInventoryTestHandler(t *testing.T, admin *openAIAdsPowerInventoryAdminStub) *OpenAIOAuthHandler {
	t.Helper()
	t.Setenv("GATEWAY_EXECUTION_NODE_ID", "api2")
	store := newOpenAIAdsPowerLaunchStore()
	now := time.Now().UTC()
	store.now = func() time.Time { return now }
	require.NoError(t, store.saveDevice(context.Background(), openAIAdsPowerHelperDevice{
		AdminUserID: 42, DeviceID: "device-1", EnvironmentKey: "api2",
		SecretHash: openAIAdsPowerHashSecret("device-secret"), PairedAt: now, LastSeenAt: now,
	}))
	return &OpenAIOAuthHandler{adminService: admin, adsPowerLaunchStore: store, batchOAuthStore: newBatchOAuthStore()}
}

func performOpenAIAdsPowerInventoryRequest(t *testing.T, handler gin.HandlerFunc, body string, bearer string) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/inventory", handler)
	req := httptest.NewRequest(http.MethodPost, "/inventory", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

func TestInventoryOpenAIAdsPowerProfilesRequiresExactPairedDeviceAndEnvironment(t *testing.T) {
	admin := &openAIAdsPowerInventoryAdminStub{inventory: &service.OpenAIAdsPowerProfileInventory{Complete: true}}
	handler := openAIAdsPowerInventoryTestHandler(t, admin)

	missing := performOpenAIAdsPowerInventoryRequest(t, handler.InventoryOpenAIAdsPowerProfiles, `{"device_id":"device-1","environment_key":"api2"}`, "")
	require.Equal(t, http.StatusUnauthorized, missing.Code)

	wrongEnvironment := performOpenAIAdsPowerInventoryRequest(t, handler.InventoryOpenAIAdsPowerProfiles, `{"device_id":"device-1","environment_key":"api"}`, "device-secret")
	require.Equal(t, http.StatusUnauthorized, wrongEnvironment.Code)

	valid := performOpenAIAdsPowerInventoryRequest(t, handler.InventoryOpenAIAdsPowerProfiles, `{"device_id":"device-1","environment_key":"api2"}`, "device-secret")
	require.Equal(t, http.StatusOK, valid.Code)
	require.Contains(t, valid.Body.String(), `"complete":true`)
}

func TestInventoryOpenAIAdsPowerProfilesFailsClosedWhenSnapshotIncomplete(t *testing.T) {
	admin := &openAIAdsPowerInventoryAdminStub{inventoryErr: errors.New("pagination changed")}
	handler := openAIAdsPowerInventoryTestHandler(t, admin)
	rec := performOpenAIAdsPowerInventoryRequest(t, handler.InventoryOpenAIAdsPowerProfiles, `{"device_id":"device-1","environment_key":"api2"}`, "device-secret")
	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
	require.NotContains(t, rec.Body.String(), `"complete":true`)
}

func TestInventoryOpenAIAdsPowerProfilesDoesNotProtectTerminalTaskPendingBinding(t *testing.T) {
	admin := &openAIAdsPowerInventoryAdminStub{inventory: &service.OpenAIAdsPowerProfileInventory{Complete: true}}
	handler := openAIAdsPowerInventoryTestHandler(t, admin)
	sessionID := "0123456789abcdef0123456789abcdef"
	binding := service.OpenAIAdsPowerBinding{Version: 1, DeviceID: "device-1", ProfileID: "profile-terminal", EnvironmentKey: "api2", WebRTCDisabled: true, FingerprintRandomized: true}
	require.NoError(t, handler.adsPowerLaunchStore.savePending(context.Background(), sessionID, 42, binding))
	handler.batchOAuthStore.tasks["task-terminal"] = &batchOAuthTask{
		ID: "task-terminal", ownerID: 42, Status: "failed", sessionID: sessionID,
		config: batchOAuthConfig{BrowserMode: batchOAuthBrowserAdsPower},
	}

	rec := performOpenAIAdsPowerInventoryRequest(t, handler.InventoryOpenAIAdsPowerProfiles, `{"device_id":"device-1","environment_key":"api2"}`, "device-secret")
	require.Equal(t, http.StatusOK, rec.Code)
	var payload struct {
		Data service.OpenAIAdsPowerProfileInventory `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &payload))
	require.Empty(t, payload.Data.PendingProfileIDs)
}

func TestReleaseOpenAIAdsPowerProfileRefusesProtectedProfile(t *testing.T) {
	admin := &openAIAdsPowerInventoryAdminStub{
		inventory: &service.OpenAIAdsPowerProfileInventory{Complete: true}, released: true,
	}
	handler := openAIAdsPowerInventoryTestHandler(t, admin)
	binding := service.OpenAIAdsPowerBinding{Version: 1, DeviceID: "device-1", ProfileID: "profile-running", EnvironmentKey: "api2", WebRTCDisabled: true, FingerprintRandomized: true}
	require.NoError(t, handler.adsPowerLaunchStore.savePending(context.Background(), "manual-session", 42, binding))
	rec := performOpenAIAdsPowerInventoryRequest(t, handler.ReleaseOpenAIAdsPowerProfile, `{"device_id":"device-1","environment_key":"api2","profile_id":"profile-running"}`, "device-secret")
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), `"released":false`)
	require.Zero(t, admin.releaseCalls)
}

func TestOpenAIAdsPowerUnknownRemoteTaskProtectsExistingWithoutFailingInventory(t *testing.T) {
	store := newOpenAIAdsPowerLaunchStore()
	now := time.Now().UTC()
	store.now = func() time.Time { return now }
	existing := &service.OpenAIAdsPowerBinding{
		Version: 1, DeviceID: "device-1", ProfileID: "profile-remote", EnvironmentKey: "api2",
		WebRTCDisabled: true, FingerprintRandomized: true,
	}
	store.launches["remote-existing"] = openAIAdsPowerLaunchRecord{
		AdminUserID: 42, TaskID: "task-on-another-node", EnvironmentKey: "api2",
		Existing: existing, ExpiresAt: now.Add(time.Minute),
	}
	store.launches["remote-create"] = openAIAdsPowerLaunchRecord{
		AdminUserID: 42, TaskID: "create-task-on-another-node", EnvironmentKey: "api2",
		ExpiresAt: now.Add(time.Minute),
	}
	activity := openAIAdsPowerTaskActivity{
		activeByID: make(map[string]bool), activeBySession: make(map[string]bool), accountIDs: make(map[int64]struct{}),
	}

	protected, complete, err := store.protectedProfileIDs(context.Background(), 42, "device-1", "api2", activity)
	require.NoError(t, err)
	require.True(t, complete)
	require.Equal(t, map[string]struct{}{"profile-remote": {}}, protected)
}
