package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type openAIAdsPowerBindingRepoStub struct {
	AccountRepository
	account *Account
	writes  int
}

func (r *openAIAdsPowerBindingRepoStub) GetByID(context.Context, int64) (*Account, error) {
	return r.account, nil
}

func (r *openAIAdsPowerBindingRepoStub) UpdateExtra(_ context.Context, _ int64, updates map[string]any) error {
	r.writes++
	if r.account.Extra == nil {
		r.account.Extra = make(map[string]any)
	}
	for key, value := range updates {
		r.account.Extra[key] = value
	}
	return nil
}

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

func TestOpenAIAdsPowerBindingLaunchUsesProfileIdentityAndPreservesProxy(t *testing.T) {
	t.Setenv("GATEWAY_EXECUTION_NODE_ID", "retired-node")
	for _, direct := range []bool{false, true} {
		t.Run(map[bool]string{false: "selected proxy", true: "direct"}[direct], func(t *testing.T) {
			binding := &OpenAIAdsPowerBinding{DeviceID: "device-1", EnvironmentKey: "existing-pool", ProfileID: "profile-1", WebRTCDisabled: true, FingerprintRandomized: true}
			account := &Account{ID: 42, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Extra: map[string]any{
				"xiass_execution_node_id":     "another-retired-node",
				OpenAIAdsPowerBindingExtraKey: binding,
			}}
			if !direct {
				id := int64(7)
				account.ProxyID = &id
				account.Proxy = &Proxy{ID: id, Protocol: "socks5", Host: "proxy.example.test", Port: 1080}
			}
			originalProxyID, originalProxy := account.ProxyID, account.Proxy
			repo := &openAIAdsPowerBindingRepoStub{account: account}
			svc := &adminServiceImpl{accountRepo: repo}
			report := *binding
			report.ProfileName = "updated profile name"
			require.NoError(t, svc.UpdateOpenAIAdsPowerBindingFromLaunch(context.Background(), 42, binding, &report))
			require.Equal(t, 1, repo.writes)
			require.Equal(t, "updated profile name", OpenAIAdsPowerBindingFromAccount(account).ProfileName)
			require.Equal(t, originalProxyID, account.ProxyID)
			require.Equal(t, originalProxy, account.Proxy)
		})
	}
}

func TestOpenAIAdsPowerBindingLaunchRejectsChangedIdentity(t *testing.T) {
	binding := &OpenAIAdsPowerBinding{DeviceID: "device-1", EnvironmentKey: "pool-1", ProfileID: "profile-1", WebRTCDisabled: true, FingerprintRandomized: true}
	for _, change := range []string{"device", "environment", "profile", "unbound", "new binding", "reported profile"} {
		t.Run(change, func(t *testing.T) {
			current, expected, report := *binding, *binding, *binding
			account := &Account{ID: 42, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Extra: map[string]any{OpenAIAdsPowerBindingExtraKey: &current}}
			expectedBinding := &expected
			switch change {
			case "device":
				current.DeviceID = "device-2"
			case "environment":
				current.EnvironmentKey = "pool-2"
			case "profile":
				current.ProfileID = "profile-2"
			case "unbound":
				delete(account.Extra, OpenAIAdsPowerBindingExtraKey)
			case "new binding":
				expectedBinding = nil
			case "reported profile":
				report.ProfileID = "profile-2"
			}
			repo := &openAIAdsPowerBindingRepoStub{account: account}
			svc := &adminServiceImpl{accountRepo: repo}
			require.Error(t, svc.UpdateOpenAIAdsPowerBindingFromLaunch(context.Background(), 42, expectedBinding, &report))
			require.Zero(t, repo.writes)
		})
	}
}
