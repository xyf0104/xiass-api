package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
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
	adsPower := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/user/list":
			mu.Lock()
			defer mu.Unlock()
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "msg": "Success", "data": map[string]any{"list": profiles}})
		case "/api/v2/browser-profile/create":
			_ = json.NewDecoder(r.Body).Decode(&createdBody)
			mu.Lock()
			profiles = append(profiles, adsPowerProfile{UserID: "created-profile", SerialNumber: "7", Name: "XIASS new-account", GroupID: "0", UserProxyConfig: profiles[0].UserProxyConfig})
			mu.Unlock()
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "msg": "Success", "data": map[string]any{"profile_id": "created-profile"}})
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
		Servers: map[string]serverConfig{origin: {EnvironmentKey: "api2", TemplateProfileID: "template-api2"}},
	}
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
	require.Contains(t, startedBody["launch_args"], "--force-webrtc-ip-handling-policy=disable_non_proxied_udp")
	require.Equal(t, "device-1", report["device_id"])
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
		return stoppedBody != nil && stoppedBody["profile_id"] == "created-profile"
	}, time.Second, 10*time.Millisecond)
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
	config := randomizedFingerprintConfig(true)
	require.Equal(t, "disabled", config["webrtc"])
	require.Equal(t, "random", config["screen_resolution"])

	randomUA, ok := config["random_ua"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, []string{"chrome"}, randomUA["ua_browser"])
	require.Equal(t, []string{"Mac OS X 12", "Mac OS X 13"}, randomUA["ua_system_version"])

	updateConfig := randomizedFingerprintConfig(false)
	require.NotContains(t, updateConfig, "random_ua")
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
