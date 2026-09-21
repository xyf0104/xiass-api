package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func poolIDs(profiles []adsPowerProfile) []string {
	ids := make([]string, len(profiles))
	for i, p := range profiles {
		ids[i] = p.UserID
	}
	return ids
}

func TestPoolRoundRobinPreservesScopeAndSkipsLeasesAndTemplates(t *testing.T) {
	now := time.Now()
	cfg := &config{
		Servers:         map[string]serverConfig{"https://api.test": {TemplateProfileID: "template"}},
		ManagedProfiles: map[string]managedProfile{},
		ProfileLeases:   map[string]profileLease{"busy": {Until: now.Add(time.Minute)}},
		ReuseCursors:    map[string]string{},
	}
	profiles := []adsPowerProfile{{UserID: "second", SerialNumber: "10"}, {UserID: "first", SerialNumber: "2"}, {UserID: "third", SerialNumber: "11"}, {UserID: "busy", SerialNumber: "1"}, {UserID: "template", SerialNumber: "3"}, {UserID: "manual", SerialNumber: "4"}, {UserID: "api2", SerialNumber: "5"}}
	for _, id := range []string{"first", "second", "third", "busy", "template"} {
		cfg.ManagedProfiles[id] = managedProfile{ServerOrigin: "https://api.test", EnvironmentKey: "api"}
	}
	cfg.ManagedProfiles["api2"] = managedProfile{ServerOrigin: "https://api2.test", EnvironmentKey: "api2"}
	require.Equal(t, []string{"first", "second", "third"}, poolIDs(orderedPoolCandidates(cfg, "https://api.test", "api.test", profiles, now)))
	cfg.ReuseCursors["https://api.test"] = "second"
	require.Equal(t, []string{"third", "first", "second"}, poolIDs(orderedPoolCandidates(cfg, "https://api.test", "api", profiles, now)))
	cfg.ReuseCursors["https://api.test"] = "third"
	require.Equal(t, []string{"first", "second", "third"}, poolIDs(orderedPoolCandidates(cfg, "https://api.test", "api", profiles, now)))
	managed := cfg.ManagedProfiles["first"]
	managed.PendingUntil = now.Add(time.Minute)
	cfg.ManagedProfiles["first"] = managed
	require.Equal(t, []string{"second", "third"}, poolIDs(orderedPoolCandidates(cfg, "https://api.test", "api", profiles, now)))
}

func TestPoolCleanupOnlyDeletesAuthorizedIdleManagedOrphans(t *testing.T) {
	profiles := []adsPowerProfile{}
	for i, id := range []string{"live401", "shared", "banned", "deleted", "orphan", "running", "leased", "pending", "grace", "manual", "refused", "other"} {
		profiles = append(profiles, adsPowerProfile{UserID: id, SerialNumber: string(rune('a' + i))})
	}
	deleted := []string{}
	ads := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var data any = map[string]any{}
		switch r.URL.Path {
		case "/api/v1/user/list":
			data = map[string]any{"list": profiles}
		case "/api/v1/browser/active":
			status := "Inactive"
			if r.URL.Query().Get("user_id") == "running" {
				status = "Active"
			}
			data = map[string]string{"status": status}
		case "/api/v1/user/delete":
			var req struct {
				IDs []string `json:"user_ids"`
			}
			require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
			deleted = append(deleted, req.IDs...)
		default:
			t.Errorf("unexpected AdsPower mutation %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": data})
	}))
	defer ads.Close()
	inventory := profileInventory{Complete: true, Profiles: []profileInventoryRef{
		{ProfileID: "live401", AccountID: 1, EnvironmentKey: "api", Blocked: false},
		{ProfileID: "shared", AccountID: 2, EnvironmentKey: "api", Blocked: true},
		{ProfileID: "shared", AccountID: 3, EnvironmentKey: "api", Blocked: false},
		{ProfileID: "banned", AccountID: 4, EnvironmentKey: "api", Blocked: true},
	}, PendingProfileIDs: []string{"pending"}, ReleasedProfileIDs: []string{"deleted"}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "Bearer device-secret", r.Header.Get("Authorization"))
		var data any = inventory
		if r.URL.Path == "/api/v1/tools/adspower/helpers/profiles/release" {
			var req map[string]string
			require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
			data = map[string]bool{"released": req["profile_id"] != "refused"}
		} else {
			require.Equal(t, "/api/v1/tools/adspower/helpers/profiles/inventory", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": data})
	}))
	defer server.Close()
	cfg := &config{path: filepath.Join(t.TempDir(), "config.json"), DeviceID: "device", AdsPowerBaseURL: ads.URL,
		Servers:         map[string]serverConfig{server.URL: {EnvironmentKey: "api", DeviceSecret: "device-secret"}},
		ManagedProfiles: map[string]managedProfile{}, ProfileLeases: map[string]profileLease{"leased": {SessionID: "active-session", Until: time.Now().Add(time.Minute)}},
		PendingProfiles: map[string]pendingProfileBinding{"orphan-account": {ProfileID: "orphan"}},
	}
	for _, id := range []string{"orphan", "running", "leased", "pending", "grace", "refused"} {
		cfg.ManagedProfiles[id] = managedProfile{ServerOrigin: server.URL, EnvironmentKey: "api"}
	}
	cfg.ManagedProfiles["grace"] = managedProfile{ServerOrigin: server.URL, EnvironmentKey: "api", PendingUntil: time.Now().Add(time.Minute)}
	cfg.ManagedProfiles["other"] = managedProfile{ServerOrigin: "https://other.test", EnvironmentKey: "api2"}
	helper := newHelperServer(cfg)
	helper.adsPower.minInterval = 0
	count, err := helper.reconcileProfiles(context.Background(), server.URL, helper.adsPower)
	require.NoError(t, err)
	require.Equal(t, 3, count)
	sort.Strings(deleted)
	require.Equal(t, []string{"banned", "deleted", "orphan"}, deleted)
	after, _ := helper.runtimeSnapshot()
	require.NotContains(t, after.PendingProfiles, "orphan-account")
	require.NotContains(t, after.ManagedProfiles, "orphan")
	require.Contains(t, after.ManagedProfiles, "live401")
	require.Contains(t, after.ManagedProfiles, "shared")
	require.True(t, after.ManagedProfiles["shared"].Shared, "server references reconstruct cookie isolation after config restore")
}

func TestPoolMissingOrIncompleteInventoryNeverDeletes(t *testing.T) {
	for _, complete := range []bool{false, true} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if complete {
				http.Error(w, "old server", http.StatusNotFound)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": profileInventory{Complete: false}})
		}))
		cfg := &config{DeviceID: "device", Servers: map[string]serverConfig{server.URL: {EnvironmentKey: "api", DeviceSecret: "secret"}}, ManagedProfiles: map[string]managedProfile{"owned": {ServerOrigin: server.URL}}}
		helper := newHelperServer(cfg)
		count, err := helper.reconcileProfiles(context.Background(), server.URL, helper.adsPower)
		require.Error(t, err)
		require.Zero(t, count)
		require.Contains(t, helper.cfg.ManagedProfiles, "owned")
		server.Close()
	}
}

func TestPoolLeaseSurvivesRestartAndCannotBeStolen(t *testing.T) {
	cfg := &config{path: filepath.Join(t.TempDir(), "config.json"), DeviceID: "device", Servers: map[string]serverConfig{}, ManagedProfiles: map[string]managedProfile{}}
	helper := newHelperServer(cfg)
	launch := &launchPayload{SessionID: "session-one", EnvironmentKey: "api", CallbackExpiresAt: time.Now().Add(time.Minute)}
	profile := &adsPowerProfile{UserID: "fixed", SerialNumber: "1"}
	require.NoError(t, helper.reserveManagedProfile("https://api.test", launch, profile, 7))
	var persisted config
	raw, err := os.ReadFile(cfg.path)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(raw, &persisted))
	persisted.path = cfg.path
	restarted := newHelperServer(&persisted)
	require.ErrorIs(t, restarted.reserveManagedProfile("https://api.test", &launchPayload{SessionID: "two"}, profile, 7), errAdsPowerProfileBusy)
	require.Equal(t, "session-one", restarted.cfg.ProfileLeases["fixed"].SessionID)
	require.NoError(t, restarted.releaseProfileLease("fixed"))
	require.NoError(t, restarted.reserveManagedProfile("https://api.test", launch, profile, 7))
}

func TestPoolUnknownActivityNeverCountsAsIdle(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]string{}})
	}))
	defer server.Close()
	helper := newHelperServer(&config{AdsPowerBaseURL: server.URL})
	require.Error(t, helper.checkProfileIdle(context.Background(), helper.adsPower, "profile"))
}

func TestPoolReuseDoesNotCreateOrRandomizeExistingFingerprint(t *testing.T) {
	proxy := adsPowerProxyConfig{ProxySoft: "other", ProxyType: "socks5", ProxyHost: "proxy.test", ProxyPort: "1089"}
	profiles := []adsPowerProfile{{UserID: "oldest", SerialNumber: "1", UserProxyConfig: proxy}, {UserID: "next", SerialNumber: "2", UserProxyConfig: proxy}}
	updates := []string{}
	ads := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var data any = map[string]any{}
		switch r.URL.Path {
		case "/api/v1/user/list":
			list := profiles
			if id := r.URL.Query().Get("user_id"); id != "" {
				list = nil
				for _, p := range profiles {
					if p.UserID == id {
						list = append(list, p)
					}
				}
			}
			data = map[string]any{"list": list}
		case "/api/v1/browser/active":
			data = map[string]string{"status": "Inactive"}
		case "/api/v2/browser-profile/update":
			var req map[string]any
			require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
			fingerprint := req["fingerprint_config"].(map[string]any)
			require.NotContains(t, fingerprint, "random_ua")
			require.NotContains(t, fingerprint, "webgl")
			require.Equal(t, "disabled", fingerprint["webrtc"])
			updates = append(updates, req["profile_id"].(string))
		default:
			t.Errorf("reuse must not create/delete profiles: %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": data})
	}))
	defer ads.Close()
	origin := "https://api.test"
	server := serverConfig{EnvironmentKey: "api", ProxyHost: "proxy.test", ProxyPort: "1089"}
	cfg := &config{path: filepath.Join(t.TempDir(), "config.json"), DeviceID: "device", AdsPowerBaseURL: ads.URL, Servers: map[string]serverConfig{origin: server}, ManagedProfiles: map[string]managedProfile{
		"oldest": {ServerOrigin: origin, EnvironmentKey: "api", FingerprintSlot: 5},
		"next":   {ServerOrigin: origin, EnvironmentKey: "api", FingerprintSlot: 9},
	}}
	helper := newHelperServer(cfg)
	helper.adsPower.minInterval = 0
	helper.verifyProxy = func(context.Context, adsPowerProxyConfig) (string, error) { return "203.0.113.5", nil }
	launch := &launchPayload{SessionID: "new-session", EnvironmentKey: "api", AccountName: "new-account"}
	profile, _, _, slot, err := helper.reusePoolProfile(context.Background(), origin, launch, server, helper.adsPower)
	require.NoError(t, err)
	require.Equal(t, "oldest", profile.UserID)
	require.Equal(t, 5, slot)
	require.True(t, helper.cfg.ManagedProfiles["oldest"].Shared)
	require.NoError(t, helper.reserveManagedProfile(origin, launch, profile, slot))
	profile, _, _, slot, err = helper.reusePoolProfile(context.Background(), origin, launch, server, helper.adsPower)
	require.NoError(t, err)
	require.Equal(t, "next", profile.UserID)
	require.Equal(t, 9, slot)
	require.Equal(t, []string{"oldest", "next"}, updates)
}

func TestPoolLateCallbackCannotStopNewerLease(t *testing.T) {
	called := false
	ads := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		http.Error(w, "unexpected old stop", http.StatusInternalServerError)
	}))
	defer ads.Close()
	helper := newHelperServer(&config{AdsPowerBaseURL: ads.URL, ProfileLeases: map[string]profileLease{"shared": {SessionID: "new-session", Until: time.Now().Add(time.Minute)}}})
	require.NoError(t, helper.stopOwnedProfile(context.Background(), "shared", "old-session"))
	require.False(t, called)
	require.Equal(t, "new-session", helper.cfg.ProfileLeases["shared"].SessionID)
}
