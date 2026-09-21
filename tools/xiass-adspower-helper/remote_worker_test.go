package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPairResidentHelperAcceptsLegacyEnvironmentAndPreservesProfiles(t *testing.T) {
	for _, environment := range []string{"api.xiass.com", "api2"} {
		t.Run(environment, func(t *testing.T) {
			called := false
			remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				called = true
				require.Equal(t, "/api/v1/tools/adspower/helpers/pairings/redeem", r.URL.Path)
				_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": helperPairingResponse{
					DeviceSecret: "new-device-secret", EnvironmentKey: environment,
				}})
			}))
			defer remote.Close()
			original := serverConfig{EnvironmentKey: "api", ProxyHost: "192.168.1.1", ProxyPort: "1089", NextFingerprintSlot: 12}
			cfg := &config{path: filepath.Join(t.TempDir(), "config.json"), DeviceID: "test-device",
				Servers:         map[string]serverConfig{remote.URL: original},
				PendingProfiles: map[string]pendingProfileBinding{"pending": {ProfileID: "fixed-profile", EnvironmentKey: "api", FingerprintSlot: 5}},
			}
			helper := newHelperServer(cfg)
			query := url.Values{"server": {remote.URL}, "environment": {environment}, "ticket": {"pairing-ticket"}}
			result := httptest.NewRecorder()
			helper.pair(result, httptest.NewRequest(http.MethodGet, "/pair?"+query.Encode(), nil))
			if environment == "api2" {
				require.Equal(t, http.StatusConflict, result.Code)
				require.False(t, called)
				return
			}
			require.Equal(t, http.StatusOK, result.Code)
			next, _ := helper.runtimeSnapshot()
			expected := original
			expected.EnvironmentKey = environment
			expected.DeviceSecret = "new-device-secret"
			require.Equal(t, expected, next.Servers[remote.URL])
			require.Equal(t, cfg.PendingProfiles, next.PendingProfiles)
			require.Equal(t, "api", cfg.Servers[remote.URL].EnvironmentKey)
		})
	}
}
