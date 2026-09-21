package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestSetupRebasesConcurrentLifecycleLease(t *testing.T) {
	validationStarted := make(chan struct{})
	continueValidation := make(chan struct{})
	adsPower := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/status", r.URL.Path)
		close(validationStarted)
		<-continueValidation
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{}})
	}))
	defer adsPower.Close()

	origin := "https://api.example.test"
	cfg := lifecycleTestConfig(t, adsPower.URL, origin)
	helper := newHelperServer(cfg)
	helper.verifyProxy = func(context.Context, adsPowerProxyConfig) (string, error) {
		return "203.0.113.8", nil
	}

	result := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		body := `{"adspower_base_url":"` + adsPower.URL + `","server_origin":"` + origin + `","environment_key":"api","proxy_host":"127.0.0.1","proxy_port":"1080"}`
		request := localHelperRequest(http.MethodPost, "/api/setup", strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		recorder := httptest.NewRecorder()
		helper.saveSetup(recorder, request)
		result <- recorder
	}()

	<-validationStarted
	leaseUntil := time.Now().Add(10 * time.Minute)
	require.NoError(t, helper.reserveManagedProfile(origin, &launchPayload{
		SessionID: "concurrent-session", EnvironmentKey: "api", CallbackExpiresAt: leaseUntil.Add(-time.Minute),
	}, &adsPowerProfile{UserID: "concurrent-profile", SerialNumber: "17"}, 17))
	close(continueValidation)

	recorder := <-result
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	assertLifecycleLeasePersisted(t, helper, "concurrent-profile", "concurrent-session")
}

func TestSetupRejectsAdsPowerConnectionChangeWithActiveLease(t *testing.T) {
	currentAdsPower := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer currentAdsPower.Close()
	newAdsPowerCalled := false
	newAdsPower := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		newAdsPowerCalled = true
	}))
	defer newAdsPower.Close()

	origin := "https://api.example.test"
	cfg := lifecycleTestConfig(t, currentAdsPower.URL, origin)
	cfg.ManagedProfiles["active-profile"] = managedProfile{ServerOrigin: origin, EnvironmentKey: "api", FingerprintSlot: 3}
	cfg.ProfileLeases["active-profile"] = profileLease{SessionID: "active-session", Until: time.Now().Add(time.Minute)}
	require.NoError(t, saveConfig(cfg))
	helper := newHelperServer(cfg)

	body := `{"adspower_base_url":"` + newAdsPower.URL + `","api_key":"new-key","server_origin":"` + origin + `","environment_key":"api","proxy_host":"127.0.0.1","proxy_port":"1080"}`
	request := localHelperRequest(http.MethodPost, "/api/setup", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	helper.saveSetup(recorder, request)

	require.Equal(t, http.StatusConflict, recorder.Code, recorder.Body.String())
	require.False(t, newAdsPowerCalled)
	assertLifecycleLeasePersisted(t, helper, "active-profile", "active-session")
}

func TestPairRebasesConcurrentLifecycleLease(t *testing.T) {
	pairStarted := make(chan struct{})
	continuePair := make(chan struct{})
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v1/tools/adspower/helpers/pairings/redeem", r.URL.Path)
		close(pairStarted)
		<-continuePair
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": helperPairingResponse{
			DeviceSecret: "paired-secret", EnvironmentKey: "api",
		}})
	}))
	defer remote.Close()

	cfg := lifecycleTestConfig(t, "http://127.0.0.1:50325", remote.URL)
	helper := newHelperServer(cfg)
	result := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		query := url.Values{"server": {remote.URL}, "environment": {"api"}, "ticket": {"pair-ticket"}}
		recorder := httptest.NewRecorder()
		helper.pair(recorder, httptest.NewRequest(http.MethodGet, "/pair?"+query.Encode(), nil))
		result <- recorder
	}()

	<-pairStarted
	require.NoError(t, helper.reserveManagedProfile(remote.URL, &launchPayload{
		SessionID: "pair-session", EnvironmentKey: "api", CallbackExpiresAt: time.Now().Add(5 * time.Minute),
	}, &adsPowerProfile{UserID: "pair-profile", SerialNumber: "9"}, 9))
	close(continuePair)

	recorder := <-result
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	current, _ := helper.runtimeSnapshot()
	require.Equal(t, "paired-secret", current.Servers[remote.URL].DeviceSecret)
	assertLifecycleLeasePersisted(t, helper, "pair-profile", "pair-session")
}

func lifecycleTestConfig(t *testing.T, adsPowerURL, origin string) *config {
	t.Helper()
	cfg := &config{
		path:            filepath.Join(t.TempDir(), "config.json"),
		ListenAddress:   "127.0.0.1:34987",
		CallbackAddress: "127.0.0.1:1455",
		AdsPowerBaseURL: adsPowerURL,
		DeviceID:        "lifecycle-device",
		Servers: map[string]serverConfig{origin: {
			EnvironmentKey: "api", ProxyHost: "127.0.0.1", ProxyPort: "1080", NextFingerprintSlot: 1,
		}},
		PendingProfiles: make(map[string]pendingProfileBinding),
		ManagedProfiles: make(map[string]managedProfile),
		ProfileLeases:   make(map[string]profileLease),
		ReuseCursors:    make(map[string]string),
	}
	require.NoError(t, saveConfig(cfg))
	return cfg
}

func assertLifecycleLeasePersisted(t *testing.T, helper *helperServer, profileID, sessionID string) {
	t.Helper()
	current, _ := helper.runtimeSnapshot()
	require.Equal(t, sessionID, current.ProfileLeases[profileID].SessionID)
	require.Contains(t, current.ManagedProfiles, profileID)

	persisted, err := loadConfig(current.path)
	require.NoError(t, err)
	require.Equal(t, sessionID, persisted.ProfileLeases[profileID].SessionID)
	require.Contains(t, persisted.ManagedProfiles, profileID)
}
