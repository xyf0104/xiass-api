//go:build unit

package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

type accountMultiProxyRepoStub struct {
	proxyRepoStub
	proxies []Proxy
}

func (s *accountMultiProxyRepoStub) ListByIDs(_ context.Context, _ []int64) ([]Proxy, error) {
	return append([]Proxy(nil), s.proxies...), nil
}

func TestValidateAccountProxyBindingsRejectsMissingAndInactiveProxies(t *testing.T) {
	admin := &adminServiceImpl{proxyRepo: &accountMultiProxyRepoStub{proxies: []Proxy{
		{ID: 1, Status: StatusActive},
		{ID: 2, Status: StatusDisabled},
	}}}

	bindings, total, proxies, err := admin.validateAccountProxyBindings(context.Background(), []AccountProxyBindingInput{
		{ProxyID: 1, MaxConcurrency: 3},
	})
	require.NoError(t, err)
	require.Equal(t, 3, total)
	require.Equal(t, []AccountProxyBindingInput{{ProxyID: 1, MaxConcurrency: 3}}, bindings)
	require.Equal(t, StatusActive, proxies[1].Status)

	_, _, _, err = admin.validateAccountProxyBindings(context.Background(), []AccountProxyBindingInput{{ProxyID: 2, MaxConcurrency: 1}})
	require.ErrorContains(t, err, "inactive")

	_, _, _, err = admin.validateAccountProxyBindings(context.Background(), []AccountProxyBindingInput{{ProxyID: 3, MaxConcurrency: 1}})
	require.ErrorContains(t, err, "does not exist")
}
