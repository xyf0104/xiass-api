package service

import (
	"context"
	"encoding/json"
	"github.com/stretchr/testify/require"
	"testing"
)

type trafficTestAgent struct {
	ProxySubscriptionAgent
	called bool
}

func (a *trafficTestAgent) Traffic(_ context.Context, days int) (*ProxyTrafficReport, error) {
	a.called = true
	return &ProxyTrafficReport{Days: days, Nodes: []ProxyTrafficNode{{ID: "jp", TotalBytes: 1000}}}, nil
}
func TestProxyTrafficAliasesCountOnce(t *testing.T) {
	cipher := &subscriptionTestCipher{}
	data, err := json.Marshal(proxySubscriptionConfig{Addresses: map[string]string{"jp": "127.0.0.1:39221"}})
	require.NoError(t, err)
	raw, err := cipher.Encrypt(string(data))
	require.NoError(t, err)
	agent := &trafficTestAgent{}
	s := &ProxySubscriptionService{store: &subscriptionTestStore{raw: raw}, encryptor: cipher, agent: agent}
	r, err := s.Traffic(context.Background(), 30, []Proxy{
		{ID: 84, Host: "127.0.0.1", Port: 39221, Protocol: "socks5h"},
		{ID: 163, Host: "127.0.0.1", Port: 39221, Protocol: "socks5h"},
		{ID: 164, Host: "example.com", Port: 39221, Protocol: "socks5h"},
	})
	require.NoError(t, err)
	require.Len(t, r.Nodes, 1)
	require.Equal(t, []int64{84, 163}, r.Nodes[0].ProxyIDs)
	require.EqualValues(t, 1000, r.Nodes[0].TotalBytes)
	agent.called = false
	_, err = s.Traffic(context.Background(), 999, nil)
	require.Error(t, err)
	require.False(t, agent.called)
}
