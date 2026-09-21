package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestLaunchCreatesDedicatedProfileAndReportsSafeBinding(t *testing.T) {
	var mu sync.Mutex
	profiles := []adsPowerProfile{{
		UserID: "template-api2", SerialNumber: "2", Name: "api2 template", GroupID: "0",
		UserProxyConfig: adsPowerProxyConfig{ProxySoft: "other", ProxyType: "socks5", ProxyHost: "proxy.example.test", ProxyPort: "1103", ProxyUser: "secret-user", ProxyPassword: "secret-password"},
	}}
	var createdBody map[string]any
	var startedBody map[string]any
	var stoppedBody map[string]any
	creations := 0
	adsPower := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/user/list":
			mu.Lock()
			defer mu.Unlock()
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "msg": "Success", "data": map[string]any{"list": profiles}})
		case "/api/v2/browser-profile/create":
			creations++
			_ = json.NewDecoder(r.Body).Decode(&createdBody)
			mu.Lock()
			profiles = append(profiles, adsPowerProfile{UserID: "created-profile", SerialNumber: "7", Name: "XIASS new-account", GroupID: "0", UserProxyConfig: profiles[0].UserProxyConfig})
			mu.Unlock()
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "msg": "Success", "data": map[string]any{"profile_id": "created-profile"}})
		case "/api/v2/browser-profile/update":
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "msg": "Success", "data": map[string]any{}})
		case "/api/v1/browser/active":
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{"status": "Inactive"}})
		case "/api/v2/browser-profile/start":
			_ = json.NewDecoder(r.Body).Decode(&startedBody)
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "msg": "Success", "data": map[string]any{}})
		case "/api/v2/browser-profile/stop":
			mu.Lock()
			_ = json.NewDecoder(r.Body).Decode(&stoppedBody)
			mu.Unlock()
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "msg": "Success", "data": map[string]any{}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer adsPower.Close()

	var report map[string]any
	var callbackReport map[string]any
	xiass := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/tools/adspower/launch-tickets/redeem":
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{
				"account_name": "new-account", "session_id": "0123456789abcdef0123456789abcdef",
				"auth_url": "https://auth.openai.com/oauth/authorize?state=s", "environment_key": "api2", "binding_token": "binding-token",
				"callback_token": "callback-token", "callback_expires_at": time.Now().Add(time.Minute),
			}})
		case "/api/v1/tools/adspower/bindings/report":
			_ = json.NewDecoder(r.Body).Decode(&report)
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": report})
		case "/api/v1/tools/adspower/callbacks/report":
			_ = json.NewDecoder(r.Body).Decode(&callbackReport)
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{"accepted": true}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer xiass.Close()

	origin, err := normalizeServerOrigin(xiass.URL)
	require.NoError(t, err)
	cfg := &config{
		AdsPowerBaseURL: adsPower.URL, DeviceID: "device-1",
		Servers: map[string]serverConfig{origin: {EnvironmentKey: "api2", NextFingerprintSlot: 1, TemplateProfileID: "template-api2"}},
		path:    filepath.Join(t.TempDir(), "config.json"),
	}
	require.NoError(t, saveConfig(cfg))
	helper := newHelperServer(cfg)
	helper.adsPower.minInterval = 0
	helper.closeDelay = 0
	helper.verifyProxy = func(context.Context, adsPowerProxyConfig) (string, error) { return "203.0.113.42", nil }
	request := localHelperRequest(http.MethodGet, launchURLFor(origin, "launch-ticket"), nil)
	recorder := httptest.NewRecorder()
	helper.routes().ServeHTTP(recorder, request)
	require.Equal(t, http.StatusOK, recorder.Code)
	require.Contains(t, recorder.Body.String(), "固定指纹环境已打开")
	require.Equal(t, "disabled", createdBody["fingerprint_config"].(map[string]any)["webrtc"])
	require.Equal(t, "created-profile", startedBody["profile_id"])
	require.NotContains(t, startedBody, "open_tabs")
	require.Contains(t, startedBody["launch_args"], "--force-webrtc-ip-handling-policy=disable_non_proxied_udp")
	require.Equal(t, "device-1", report["device_id"])
	require.Equal(t, float64(1), report["fingerprint_slot"])
	require.Equal(t, "203.0.113.42", report["proxy_exit_ip"])
	require.NotContains(t, report, "proxy_user")
	require.NotContains(t, report, "proxy_password")

	callback := httptest.NewRecorder()
	helper.callbackRoutes().ServeHTTP(callback, localHelperRequest(http.MethodGet, "/auth/callback?code=abc&state=s", nil))
	require.Equal(t, http.StatusOK, callback.Code)
	require.Contains(t, callback.Body.String(), "授权已返回 XIASS")
	require.Equal(t, "callback-token", callbackReport["callback_token"])
	require.Equal(t, "http://localhost:1455/auth/callback?code=abc&state=s", callbackReport["callback_url"])
	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		current, _ := helper.runtimeSnapshot()
		_, leased := current.ProfileLeases["created-profile"]
		return stoppedBody != nil && stoppedBody["profile_id"] == "created-profile" && !leased
	}, time.Second, 10*time.Millisecond)

	// Receipt is not account creation: keep the identity cache through token
	// exchange failures and helper restarts so the next attempt reuses the slot.
	saved, err := loadConfig(cfg.path)
	require.NoError(t, err)
	key := pendingProfileKey(origin, "api2", "new-account")
	require.Equal(t, "created-profile", saved.PendingProfiles[key].ProfileID)
	require.Equal(t, 2, saved.Servers[origin].NextFingerprintSlot)
	restarted := newHelperServer(saved)
	restarted.adsPower.minInterval = 0
	restarted.verifyProxy = helper.verifyProxy
	second := httptest.NewRecorder()
	restarted.routes().ServeHTTP(second, localHelperRequest(http.MethodGet, launchURLFor(origin, "new-ticket"), nil))
	require.Equal(t, http.StatusOK, second.Code, second.Body.String())
	require.Equal(t, 1, creations)
	require.Equal(t, "created-profile", startedBody["profile_id"])
	require.Equal(t, float64(1), report["fingerprint_slot"])
	require.Equal(t, 2, restarted.cfg.Servers[origin].NextFingerprintSlot)
}

func TestCallbackReceiverAndObserverShareOneDelivery(t *testing.T) {
	var reports, stops atomic.Int32
	xiass := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reports.Add(1)
		time.Sleep(10 * time.Millisecond)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"code":0,"data":{"accepted":true}}`)
	}))
	defer xiass.Close()
	ads := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		stops.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"code":0,"data":{}}`)
	}))
	defer ads.Close()
	helper := newHelperServer(&config{AdsPowerBaseURL: ads.URL, path: filepath.Join(t.TempDir(), "config.json")})
	helper.closeDelay = 0
	helper.adsPower.minInterval = 0
	launch := &launchPayload{AuthURL: "https://auth.openai.com/oauth/authorize?state=one-state", CallbackToken: "one-token", CallbackExpiresAt: time.Now().Add(time.Minute)}
	require.NoError(t, helper.reserveManagedProfile(xiass.URL, launch, &adsPowerProfile{UserID: "one-profile"}, 1))
	_, _, err := helper.registerCallback(xiass.URL, launch, "one-profile")
	require.NoError(t, err)
	var wg sync.WaitGroup
	errors := make(chan error, 2)
	wg.Add(2)
	go func() {
		defer wg.Done()
		result := httptest.NewRecorder()
		helper.callbackRoutes().ServeHTTP(result, localHelperRequest(http.MethodGet, "/auth/callback?code=abc&state=one-state", nil))
		if !strings.Contains(result.Body.String(), "授权已返回 XIASS") {
			errors <- fmt.Errorf("callback receiver did not acknowledge delivery")
		}
	}()
	go func() {
		defer wg.Done()
		errors <- helper.submitObservedCallback(context.Background(), launch, "http://localhost:1455/auth/callback?code=abc&state=one-state")
	}()
	wg.Wait()
	close(errors)
	for err := range errors {
		require.NoError(t, err)
	}
	require.Equal(t, int32(1), reports.Load())
	require.Eventually(t, func() bool {
		current, _ := helper.runtimeSnapshot()
		_, leased := current.ProfileLeases["one-profile"]
		return stops.Load() == 1 && !leased
	}, time.Second, 5*time.Millisecond)
	receipt, ok := helper.callbackRegistration("one-state")
	require.True(t, ok)
	require.True(t, receipt.Delivered)
	require.Empty(t, receipt.Token)
	_, err = helper.deliverCallback(context.Background(), "one-state", "http://localhost:1455/auth/callback?code=abc&state=other-state")
	require.Error(t, err)
	require.Equal(t, int32(1), reports.Load())
}

func TestLaunchTicketRetryOnlyAllowsConnectionEstablishmentFailure(t *testing.T) {
	require.True(t, retryableServerNetworkError(&url.Error{Err: &net.OpError{Op: "dial", Err: context.DeadlineExceeded}}))
	require.False(t, retryableServerNetworkError(&url.Error{Err: &net.OpError{Op: "read", Err: context.DeadlineExceeded}}))
	require.False(t, retryableServerNetworkError(context.DeadlineExceeded))
}

func TestPendingProfileKeyAndCloneKeepAccountsAndNodesIsolated(t *testing.T) {
	key := pendingProfileKey("https://api.example.test", "api", " Owner@Example.Test ")
	require.Equal(t, key, pendingProfileKey("https://api.example.test", "api.example.test", "owner@example.test"))
	require.NotEqual(t, key, pendingProfileKey("https://api.example.test", "api2", "owner@example.test"))
	require.NotEqual(t, key, pendingProfileKey("https://api.example.test", "api", "other@example.test"))
	require.NotContains(t, key, "owner")
	original := &config{PendingProfiles: map[string]pendingProfileBinding{key: {ProfileID: "original", EnvironmentKey: "api"}}}
	cloned := cloneConfig(original)
	cloned.PendingProfiles[key] = pendingProfileBinding{ProfileID: "changed", EnvironmentKey: "api"}
	require.Equal(t, "original", original.PendingProfiles[key].ProfileID)
}

func TestUnknownFingerprintSlotOnlyUpdatesPrivacyPolicy(t *testing.T) {
	var payload map[string]any
	ads := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&payload)
		_, _ = io.WriteString(w, `{"code":0,"data":{}}`)
	}))
	defer ads.Close()
	client := newAdsPowerClient(&config{AdsPowerBaseURL: ads.URL})
	client.minInterval = 0
	err := client.enforceProfilePolicy(context.Background(), &adsPowerProfile{UserID: "existing"}, &adsPowerProfile{ProxyID: "assigned-proxy"}, 0)
	require.NoError(t, err)
	require.Equal(t, map[string]any{"webrtc": "disabled"}, payload["fingerprint_config"])
	require.Equal(t, "assigned-proxy", payload["proxyid"])
}

func TestPendingProfileProxyMatchRejectsWrongNodeAndCredentials(t *testing.T) {
	template := &adsPowerProfile{UserProxyConfig: adsPowerProxyConfig{ProxyType: "socks5", ProxyHost: "node.example.test", ProxyPort: "1089", ProxyUser: "user", ProxyPassword: "password"}}
	profile := *template
	require.True(t, adsPowerProfileUsesTemplate(&profile, template))
	profile.UserProxyConfig.ProxyPort = "1085"
	require.False(t, adsPowerProfileUsesTemplate(&profile, template))
	profile = *template
	profile.UserProxyConfig.ProxyPassword = "wrong"
	require.False(t, adsPowerProfileUsesTemplate(&profile, template))
	template.ProxyID = "assigned"
	profile = *template
	profile.ProxyID = "other"
	require.False(t, adsPowerProfileUsesTemplate(&profile, template))
}

func TestProfileLookupPaginatesBeyondFirstHundred(t *testing.T) {
	pageOne := make([]adsPowerProfile, 100)
	for i := range pageOne {
		pageOne[i].UserID = "profile-" + strconv.Itoa(i+1)
	}
	target := adsPowerProfile{UserID: "profile-101", Name: "target"}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		profiles := []adsPowerProfile{}
		switch {
		case r.URL.Query().Get("user_id") != "":
			// Simulate an older AdsPower build that accepts but ignores the
			// direct filter without returning the requested profile.
		case r.URL.Query().Get("page") == "1":
			profiles = pageOne
		case r.URL.Query().Get("page") == "2":
			profiles = []adsPowerProfile{target}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "msg": "Success", "data": map[string]any{"list": profiles}})
	}))
	defer server.Close()

	client := newAdsPowerClient(&config{AdsPowerBaseURL: server.URL})
	client.minInterval = 0
	profile, err := client.profile(context.Background(), target.UserID)
	require.NoError(t, err)
	require.Equal(t, target.UserID, profile.UserID)
}

func TestAdsPowerClientRetriesExplicitRateLimit(t *testing.T) {
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		attempts++
		w.Header().Set("Content-Type", "application/json")
		if attempts == 1 {
			_ = json.NewEncoder(w).Encode(map[string]any{"code": -1, "msg": "Too many request per second, please check"})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "msg": "Success", "data": map[string]any{}})
	}))
	defer server.Close()

	client := newAdsPowerClient(&config{AdsPowerBaseURL: server.URL})
	client.minInterval = 0
	client.sleep = func(context.Context, time.Duration) error { return nil }
	require.NoError(t, client.status(context.Background()))
	require.Equal(t, 2, attempts)
}

func TestLaunchRejectsUnknownServerBeforeRedeemingTicket(t *testing.T) {
	cfg := &config{AdsPowerBaseURL: "http://127.0.0.1:50325", DeviceID: "device-1", Servers: map[string]serverConfig{}}
	helper := newHelperServer(cfg)
	request := localHelperRequest(http.MethodGet, "/launch?server=https%3A%2F%2Funknown.example&ticket=launch-ticket", nil)
	recorder := httptest.NewRecorder()
	helper.routes().ServeHTTP(recorder, request)
	require.Equal(t, http.StatusForbidden, recorder.Code)
	require.Contains(t, recorder.Body.String(), "未授权的 XIASS 服务器")
}

func TestNormalizeServerOrigin(t *testing.T) {
	got, err := normalizeServerOrigin("https://api.example.com/")
	require.NoError(t, err)
	require.Equal(t, "https://api.example.com", got)
	_, err = normalizeServerOrigin("http://api.example.com")
	require.Error(t, err)
	_, err = normalizeServerOrigin("https://api.example.com/path")
	require.Error(t, err)
}

func TestRandomizedFingerprintConfigPrefersMacAndDisablesWebRTC(t *testing.T) {
	config := randomizedFingerprintConfig(1, true)
	require.Equal(t, "disabled", config["webrtc"])
	require.Equal(t, "1920_1080", config["screen_resolution"])
	require.Equal(t, "4", config["hardware_concurrency"])

	randomUA, ok := config["random_ua"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, []string{"chrome"}, randomUA["ua_browser"])
	require.Equal(t, []string{"Mac OS X 12"}, randomUA["ua_system_version"])

	updateConfig := randomizedFingerprintConfig(1, false)
	require.NotContains(t, updateConfig, "random_ua")
	require.Equal(t, randomizedFingerprintConfig(1, true)["screen_resolution"], randomizedFingerprintConfig(53, true)["screen_resolution"])
}

func TestFingerprintSlotsWrapAfterFiftyTwoWithoutSharingProfiles(t *testing.T) {
	origin := "https://api2.example.test"
	cfg := &config{
		AdsPowerBaseURL: "http://127.0.0.1:50325",
		DeviceID:        "device-1",
		Servers: map[string]serverConfig{
			origin: {EnvironmentKey: "api2", NextFingerprintSlot: 52, ProxyID: "7"},
		},
		path: filepath.Join(t.TempDir(), "config.json"),
	}
	require.NoError(t, saveConfig(cfg))
	helper := newHelperServer(cfg)

	first, err := helper.allocateFingerprintSlot(origin)
	require.NoError(t, err)
	second, err := helper.allocateFingerprintSlot(origin)
	require.NoError(t, err)

	require.Equal(t, 52, first)
	require.Equal(t, 1, second)
	reloaded, err := loadConfig(cfg.path)
	require.NoError(t, err)
	require.Equal(t, 2, reloaded.Servers[origin].NextFingerprintSlot)
}

func TestServerForEnvironmentUsesAccountNodeBehindDifferentControlServer(t *testing.T) {
	cfg := &config{Servers: map[string]serverConfig{
		"https://api.example.test":  {EnvironmentKey: "api", ProxyID: "8"},
		"https://api2.example.test": {EnvironmentKey: "api2", ProxyID: "7"},
	}}

	origin, server, ok := cfg.serverForEnvironment("https://api2.example.test", "api")
	require.True(t, ok)
	require.Equal(t, "https://api.example.test", origin)
	require.Equal(t, "8", server.ProxyID)
}

func TestServerForEnvironmentAcceptsLegacyNodeHostnames(t *testing.T) {
	cfg := &config{Servers: map[string]serverConfig{
		"https://api.example.test":  {EnvironmentKey: "api", ProxyID: "8"},
		"https://api2.example.test": {EnvironmentKey: "api2", ProxyID: "7"},
	}}

	origin, server, ok := cfg.serverForEnvironment("https://api2.example.test", "api.example.test")
	require.True(t, ok)
	require.Equal(t, "https://api.example.test", origin)
	require.Equal(t, "8", server.ProxyID)

	origin, server, ok = cfg.serverForEnvironment("https://api.example.test", "https://api2.example.test")
	require.True(t, ok)
	require.Equal(t, "https://api2.example.test", origin)
	require.Equal(t, "7", server.ProxyID)
}

func TestSummarizeLaunchResponse(t *testing.T) {
	body := []byte(`<main><h1>服务器出口不匹配</h1><p>当前账号不属于这个 AdsPower 出口环境。</p></main>`)
	require.Equal(t, "服务器出口不匹配: 当前账号不属于这个 AdsPower 出口环境。", summarizeLaunchResponse(body))
}

func TestDirectSOCKSTemplateUsesAdsPowerDefaultGroup(t *testing.T) {
	server := serverConfig{
		EnvironmentKey: "api2",
		ProxyHost:      "api2.example.test",
		ProxyPort:      "1104",
		ProxyUser:      "proxy-user",
		ProxyPassword:  "proxy-password",
	}
	template, err := resolveAdsPowerTemplate(context.Background(), nil, server)
	require.NoError(t, err)
	require.Equal(t, "0", template.GroupID)
	require.Equal(t, "socks5", template.UserProxyConfig.ProxyType)
	require.Equal(t, "api2.example.test", template.UserProxyConfig.ProxyHost)
}

func TestSavedProxyTemplateIsResolvedThroughAdsPower(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v2/proxy-list/list", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "msg": "Success", "data": map[string]any{"list": []map[string]any{{
			"proxy_id": "7", "type": "socks5", "host": "192.0.2.10", "port": "1085",
		}}}})
	}))
	defer server.Close()

	client := newAdsPowerClient(&config{AdsPowerBaseURL: server.URL})
	client.minInterval = 0
	template, err := resolveAdsPowerTemplate(context.Background(), client, serverConfig{EnvironmentKey: "api2", ProxyID: "7"})
	require.NoError(t, err)
	require.Equal(t, "7", template.ProxyID)
	require.Equal(t, "socks5", template.UserProxyConfig.ProxyType)
	require.Equal(t, "192.0.2.10", template.UserProxyConfig.ProxyHost)
	require.Equal(t, "1085", template.UserProxyConfig.ProxyPort)
}

func TestCallbackPageKeepsCompleteCallbackURL(t *testing.T) {
	cfg := &config{AdsPowerBaseURL: "http://127.0.0.1:50325", DeviceID: "device-1", Servers: map[string]serverConfig{}}
	helper := newHelperServer(cfg)
	recorder := httptest.NewRecorder()
	request := localHelperRequest(http.MethodGet, "/auth/callback?code=abc&state=def", nil)
	helper.callbackRoutes().ServeHTTP(recorder, request)
	require.Equal(t, http.StatusOK, recorder.Code)
	require.Contains(t, recorder.Body.String(), "http://localhost:1455/auth/callback?code=abc&amp;state=def")
	parsed, err := url.Parse("http://localhost:1455/auth/callback?code=abc&state=def")
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(parsed.String(), "http://localhost:1455/"))
}

func localHelperRequest(method, target string, body io.Reader) *http.Request {
	request := httptest.NewRequest(method, target, body)
	request.Host = "127.0.0.1:34987"
	return request
}
