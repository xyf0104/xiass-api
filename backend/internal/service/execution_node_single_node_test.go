package service

import (
	"context"
	"errors"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type singleNodeUnexpectedHealthReader struct {
	t *testing.T
}

func (r singleNodeUnexpectedHealthReader) HealthyExecutionNodes(context.Context, []string) (map[string]bool, error) {
	r.t.Error("disabled multi-node routing must not query peer health")
	return nil, errors.New("retired peer unavailable")
}

func TestSingleNodeRoutingDoesNotReadClusterSettings(t *testing.T) {
	cfg := &config.Config{}
	repo := &executionNodeSettingRepo{err: errors.New("cluster settings unavailable")}
	svc := NewSettingService(repo, cfg)
	svc.SetExecutionNodeHealthReader(singleNodeUnexpectedHealthReader{t})

	settings := svc.GetExecutionNodeRoutingSettings(context.Background())
	require.True(t, settings.Available)
	require.False(t, settings.Enabled)
	policy := resolveExecutionNodeRoutingPolicy(context.Background(), cfg, svc)
	require.False(t, policy.enabled)
	require.False(t, policy.unavailable)
	require.Zero(t, repo.calls)
}

func TestDisabledNodeBalancingKeepsAccountEgressWithRetiredPeers(t *testing.T) {
	cfg := &config.Config{}
	cfg.Gateway.ExecutionNode = config.GatewayExecutionNodeConfig{
		Enabled: true,
		ID:      "primary",
	}
	repo := &executionNodeSettingRepo{values: map[string]string{
		SettingKeyExecutionNodeBalancingEnabled: "false",
		SettingKeyExecutionNodeWeights:          `{"primary":0,"retired":1}`,
		SettingKeyExecutionNodeProxyIDs:         `{"retired":85}`,
	}}
	svc := NewSettingService(repo, cfg)
	svc.SetExecutionNodeHealthReader(singleNodeUnexpectedHealthReader{t})
	ctx := context.Background()

	policy := resolveExecutionNodeRoutingPolicy(ctx, cfg, svc)
	require.False(t, policy.enabled)
	require.False(t, policy.unavailable)
	account := executionNodeTestAccount(1, "primary", 1)
	account.Proxy.Host = "proxy.example"
	account.Proxy.Port = 1080
	selected := filterExecutionNodeCandidates([]*Account{account}, func(a *Account) *Account { return a }, policy)
	require.Equal(t, []*Account{account}, selected)
	require.Equal(t, int64(84), *selected[0].ProxyID)
	require.Equal(t, "proxy.example", selected[0].Proxy.Host)
	require.Equal(t, 1080, selected[0].Proxy.Port)
}
