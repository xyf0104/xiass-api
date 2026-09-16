package service

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestParseOpenAIAdsPowerBindingNormalizesSafeMetadata(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	binding := ParseOpenAIAdsPowerBinding(map[string]any{
		"version":                9,
		"device_id":              " device-1 ",
		"profile_id":             " profile-1 ",
		"environment_key":        " api2 ",
		"proxy_type":             " SOCKS5 ",
		"proxy_host":             "PROXY.EXAMPLE.TEST ",
		"proxy_port":             "1103",
		"webrtc_disabled":        true,
		"fingerprint_randomized": true,
		"last_verified_at":       now,
	})
	require.NotNil(t, binding)
	require.Equal(t, 1, binding.Version)
	require.Equal(t, "device-1", binding.DeviceID)
	require.Equal(t, "profile-1", binding.ProfileID)
	require.Equal(t, "api2", binding.EnvironmentKey)
	require.Equal(t, "socks5", binding.ProxyType)
	require.Equal(t, "proxy.example.test", binding.ProxyHost)
	require.Equal(t, now, binding.LastVerifiedAt.UTC())
}

func TestParseOpenAIAdsPowerBindingRejectsIncompleteIdentity(t *testing.T) {
	require.Nil(t, ParseOpenAIAdsPowerBinding(map[string]any{
		"device_id":  "device-1",
		"profile_id": "profile-1",
	}))
}

func TestParseOpenAIAdsPowerBindingRejectsUnverifiedPrivacyPolicy(t *testing.T) {
	require.Nil(t, ParseOpenAIAdsPowerBinding(map[string]any{
		"device_id":              "device-1",
		"profile_id":             "profile-1",
		"environment_key":        "api2",
		"webrtc_disabled":        false,
		"fingerprint_randomized": true,
	}))
	require.Nil(t, ParseOpenAIAdsPowerBinding(map[string]any{
		"device_id":              "device-1",
		"profile_id":             "profile-1",
		"environment_key":        "api2",
		"webrtc_disabled":        true,
		"fingerprint_randomized": false,
	}))
}
