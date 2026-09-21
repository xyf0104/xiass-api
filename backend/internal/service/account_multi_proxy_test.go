package service

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestAccountProxyBindingsExtraRoundTripWithoutJSONMarshal(t *testing.T) {
	extra := setAccountProxyBindingsExtra(nil, []AccountProxyBindingInput{
		{ProxyID: 9, MaxConcurrency: 3, RoutePriority: 2},
		{ProxyID: 4, MaxConcurrency: 2},
	})

	require.Equal(t, []AccountProxyBinding{
		{ProxyID: 4, MaxConcurrency: 2},
		{ProxyID: 9, MaxConcurrency: 3, RoutePriority: 2},
	}, AccountProxyBindingsFromExtra(extra))
}

func TestNormalizeAccountProxyBindingInputsRejectsInvalidConfiguration(t *testing.T) {
	_, _, err := normalizeAccountProxyBindingInputs([]AccountProxyBindingInput{
		{ProxyID: 4, MaxConcurrency: 1},
		{ProxyID: 4, MaxConcurrency: 2},
	})
	require.ErrorContains(t, err, "selected more than once")

	_, _, err = normalizeAccountProxyBindingInputs([]AccountProxyBindingInput{{ProxyID: 5, MaxConcurrency: 0}})
	require.ErrorContains(t, err, "max_concurrency")

	_, _, err = normalizeAccountProxyBindingInputs([]AccountProxyBindingInput{{ProxyID: 5, MaxConcurrency: 1, RoutePriority: -1}})
	require.ErrorContains(t, err, "route_priority")
}

func TestHydrateAccountProxyBindingsUsesOnlyAvailableCapacity(t *testing.T) {
	now := time.Now()
	expiredAt := now.Add(-time.Minute)
	extra := setAccountProxyBindingsExtra(nil, []AccountProxyBindingInput{
		{ProxyID: 1, MaxConcurrency: 2},
		{ProxyID: 2, MaxConcurrency: 3},
		{ProxyID: 3, MaxConcurrency: 4},
	})
	account := &Account{Status: StatusActive, Schedulable: true, Extra: extra, Concurrency: 99}
	HydrateAccountProxyBindings(account, map[int64]*Proxy{
		1: {ID: 1, Status: StatusActive},
		2: {ID: 2, Status: StatusDisabled},
		3: {ID: 3, Status: StatusActive, ExpiresAt: &expiredAt},
	})

	require.True(t, account.MultiProxyConfigured)
	require.Len(t, account.ProxyBindings, 3)
	require.Equal(t, 2, account.Concurrency)
	require.True(t, account.IsSchedulable())

	account.ProxyBindings[0].Proxy.Status = StatusDisabled
	require.Zero(t, account.MultiProxyConcurrency())
	require.False(t, account.IsSchedulable())
}

func TestRequestProxyOverridesEgressWithoutChangingPersistentProxyIdentity(t *testing.T) {
	baseID := int64(1)
	account := &Account{
		ProxyID:                    &baseID,
		Concurrency:                9,
		Proxy:                      &Proxy{ID: baseID, Name: "default", Protocol: "socks5", Host: "192.0.2.1", Port: 1080},
		RequestProxy:               &Proxy{ID: 2, Name: "selected", Protocol: "socks5", Host: "2001:db8::2", Port: 1080},
		RequestProxyMaxConcurrency: 3,
	}

	require.Equal(t, baseID, *account.ProxyID)
	require.Equal(t, int64(2), account.requestProxy().ID)
	require.Equal(t, "socks5://[2001:db8::2]:1080", account.requestProxyURL())
	proxyID, proxyName := opsUpstreamProxyAttribution(account)
	require.NotNil(t, proxyID)
	require.Equal(t, int64(2), *proxyID)
	require.Equal(t, "selected", proxyName)
}
