package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLoadConfigCreatesFirstRunDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "config.json")
	cfg, err := loadConfig(path)
	require.NoError(t, err)
	require.Equal(t, "127.0.0.1:34987", cfg.ListenAddress)
	require.Equal(t, "127.0.0.1:1455", cfg.CallbackAddress)
	require.NotEmpty(t, cfg.DeviceID)
	require.Empty(t, cfg.Servers)
	info, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm())
}

func TestSetupSavesLocalAPIKeyAndDirectSOCKSRoute(t *testing.T) {
	adsPower := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/status", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "msg": "Success", "data": map[string]any{}})
	}))
	defer adsPower.Close()

	path := filepath.Join(t.TempDir(), "config.json")
	cfg, err := loadConfig(path)
	require.NoError(t, err)
	cfg.AdsPowerBaseURL = adsPower.URL
	require.NoError(t, saveConfig(cfg))
	helper := newHelperServer(cfg)
	helper.adsPower.minInterval = 0
	helper.verifyProxy = func(_ context.Context, proxy adsPowerProxyConfig) (string, error) {
		require.Equal(t, "socks5", proxy.ProxyType)
		require.Equal(t, "api2.example.test", proxy.ProxyHost)
		require.Equal(t, "1104", proxy.ProxyPort)
		require.Equal(t, "proxy-user", proxy.ProxyUser)
		require.Equal(t, "proxy-password", proxy.ProxyPassword)
		return "203.0.113.24", nil
	}

	body := `{"adspower_base_url":"` + adsPower.URL + `","api_key":"local-api-key","server_origin":"https://api2.example.test","environment_key":"api2","proxy_host":"api2.example.test","proxy_port":"1104","proxy_user":"proxy-user","proxy_password":"proxy-password"}`
	request := localHelperRequest(http.MethodPost, "/api/setup", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	helper.routes().ServeHTTP(recorder, request)
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	require.Contains(t, recorder.Body.String(), `"exit_ip":"203.0.113.24"`)
	require.NotContains(t, recorder.Body.String(), "local-api-key")
	require.NotContains(t, recorder.Body.String(), "proxy-password")

	saved, err := loadConfig(path)
	require.NoError(t, err)
	require.Equal(t, "local-api-key", saved.APIKey)
	server := saved.Servers["https://api2.example.test"]
	require.Equal(t, "api2", server.EnvironmentKey)
	require.Equal(t, "api2.example.test", server.ProxyHost)
	require.Equal(t, "proxy-password", server.ProxyPassword)
}

func TestHelperRejectsNonLoopbackHost(t *testing.T) {
	cfg := &config{AdsPowerBaseURL: "http://127.0.0.1:50325", DeviceID: "device-1", Servers: map[string]serverConfig{}}
	helper := newHelperServer(cfg)
	request := httptest.NewRequest(http.MethodGet, "/setup", nil)
	request.Host = "attacker.example"
	recorder := httptest.NewRecorder()
	helper.routes().ServeHTTP(recorder, request)
	require.Equal(t, http.StatusForbidden, recorder.Code)
}
