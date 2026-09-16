package admin

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestOpenAIAdsPowerLaunchStoreTicketsAreOneTime(t *testing.T) {
	store := newOpenAIAdsPowerLaunchStore()
	record := openAIAdsPowerLaunchRecord{
		SessionID:      "0123456789abcdef0123456789abcdef",
		AuthURL:        "https://auth.openai.com/oauth/authorize?state=s&code_challenge=c&redirect_uri=http%3A%2F%2Flocalhost%3A1455%2Fauth%2Fcallback",
		EnvironmentKey: "api2",
		BindingToken:   "binding-token",
		ExpiresAt:      time.Now().Add(time.Minute),
	}
	require.NoError(t, store.create(context.Background(), "launch-ticket", record))
	got, ok, err := store.consumeLaunch(context.Background(), "launch-ticket")
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, record.SessionID, got.SessionID)
	_, ok, err = store.consumeLaunch(context.Background(), "launch-ticket")
	require.NoError(t, err)
	require.False(t, ok)
	_, ok, err = store.consumeBinding(context.Background(), "binding-token")
	require.NoError(t, err)
	require.True(t, ok)
	_, ok, err = store.consumeBinding(context.Background(), "binding-token")
	require.NoError(t, err)
	require.False(t, ok)
}

func TestOpenAIAdsPowerPendingBindingIsScopedToAdministrator(t *testing.T) {
	store := newOpenAIAdsPowerLaunchStore()
	binding := service.OpenAIAdsPowerBinding{
		Version: 1, DeviceID: "device-1", ProfileID: "profile-1", EnvironmentKey: "api2",
		WebRTCDisabled: true, FingerprintRandomized: true,
	}
	require.NoError(t, store.savePending(context.Background(), "0123456789abcdef0123456789abcdef", 42, binding))

	_, err := store.pendingBinding(context.Background(), "0123456789abcdef0123456789abcdef", 43)
	require.ErrorContains(t, err, "another administrator")
	got, err := store.pendingBinding(context.Background(), "0123456789abcdef0123456789abcdef", 42)
	require.NoError(t, err)
	require.Equal(t, binding.ProfileID, got.ProfileID)
}

func TestNormalizeOpenAIAdsPowerBindingReportRequiresSafeProfile(t *testing.T) {
	record := openAIAdsPowerLaunchRecord{EnvironmentKey: "api2"}
	request := openAIAdsPowerBindingReport{
		DeviceID: "device-1", ProfileID: "profile-1", ProfileName: "XIASS account",
		EnvironmentKey: "api2", ProxyType: "SOCKS5", ProxyHost: "PROXY.EXAMPLE.TEST",
		ProxyPort: "1103", ProxyExitIP: "203.0.113.42", WebRTCDisabled: true,
		FingerprintRandomized: true,
	}
	binding, err := normalizeOpenAIAdsPowerBindingReport(request, record)
	require.NoError(t, err)
	require.Equal(t, "socks5", binding.ProxyType)
	require.Equal(t, "proxy.example.test", binding.ProxyHost)

	request.WebRTCDisabled = false
	_, err = normalizeOpenAIAdsPowerBindingReport(request, record)
	require.ErrorContains(t, err, "WebRTC disabled")
}

func TestValidOpenAIAdsPowerAuthURL(t *testing.T) {
	require.True(t, validOpenAIAdsPowerAuthURL("https://auth.openai.com/oauth/authorize?state=s&code_challenge=c&redirect_uri=http%3A%2F%2Flocalhost%3A1455%2Fauth%2Fcallback"))
	require.False(t, validOpenAIAdsPowerAuthURL("https://example.com/oauth/authorize?state=s&code_challenge=c&redirect_uri=x"))
}
