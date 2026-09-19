package dto

import (
	"encoding/json"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestAccountFromServiceRedactsMultiProxyCredentials(t *testing.T) {
	account := &service.Account{
		ID:                   1,
		MultiProxyConfigured: true,
		Extra: map[string]any{
			service.AccountMultiProxyExtraKey: map[string]any{"version": 1},
		},
		ProxyBindings: []service.AccountProxyBinding{{
			ProxyID:        8,
			MaxConcurrency: 4,
			Proxy: &service.Proxy{
				ID:       8,
				Name:     "IPv6 exit",
				Protocol: "socks5",
				Host:     "2001:db8::8",
				Port:     1080,
				Username: "visible-user",
				Password: "must-not-leak",
				Status:   service.StatusActive,
			},
		}},
	}

	out := AccountFromService(account)
	require.Len(t, out.ProxyBindings, 1)
	require.Equal(t, "2001:db8::8", out.ProxyBindings[0].Proxy.Host)
	payload, err := json.Marshal(out)
	require.NoError(t, err)
	require.NotContains(t, string(payload), "must-not-leak")
	require.NotContains(t, string(payload), service.AccountMultiProxyExtraKey)
}
