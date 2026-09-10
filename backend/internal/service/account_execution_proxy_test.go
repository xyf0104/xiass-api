//go:build unit

package service

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestAdminExplicitExecutionProxyUpdate(t *testing.T) {
	ctx := context.Background()
	account := executionNodeTestAccount(901, "api", 1)
	account.Platform, account.Type = PlatformOpenAI, AccountTypeAPIKey
	repo := &upstreamBillingProbeAccountRepo{accounts: map[int64]*Account{account.ID: account}}
	cfg := &config.Config{}
	cfg.Gateway.ExecutionNode = config.GatewayExecutionNodeConfig{Enabled: true, ID: "api", LegacyUnassignedNodeID: "api", DefaultProxyID: 84}
	settings := NewSettingService(&executionNodeSettingRepo{values: map[string]string{
		SettingKeyExecutionNodeBalancingEnabled: "true",
		SettingKeyExecutionNodeWeights:          `{"api":9,"api2":1}`,
		SettingKeyExecutionNodeProxyIDs:         `{"api":84,"api2":83}`,
	}}, cfg)
	svc := &adminServiceImpl{accountRepo: &upstreamBillingProbeAdminRepo{repo}, settingService: settings}
	proxyID := int64(99)
	updated, err := svc.UpdateAccount(ctx, account.ID, &UpdateAccountInput{ProxyID: &proxyID})
	require.NoError(t, err)
	require.Equal(t, proxyID, *updated.ProxyID)
	require.True(t, updated.hasExplicitExecutionProxy())
	require.Equal(t, "api", updated.ExecutionNodeID("api"))
	updated, err = svc.UpdateAccount(ctx, account.ID, &UpdateAccountInput{Extra: map[string]any{AccountExecutionProxyExtraKey: "83", "custom": true}})
	require.NoError(t, err)
	require.True(t, updated.hasExplicitExecutionProxy())
	result, err := svc.BulkUpdateAccounts(ctx, &BulkUpdateAccountsInput{AccountIDs: []int64{account.ID}, ProxyID: &proxyID})
	require.NoError(t, err)
	require.Equal(t, 1, result.Success)
	require.Equal(t, "99", repo.bulkUpdates[0].Extra[AccountExecutionProxyExtraKey])
	zero := int64(0)
	_, err = svc.UpdateAccount(ctx, account.ID, &UpdateAccountInput{ProxyID: &zero})
	require.ErrorContains(t, err, "private egress proxy")
}

func TestExecutionNodeExplicitProxyPreservesHealthAndOwnerGuards(t *testing.T) {
	policy := executionNodeTestPolicy(map[string]float64{"api": 9, "api2": 1})
	account := executionNodeTestAccount(902, "api2", 1)
	proxyID := int64(99)
	account.ProxyID = &proxyID
	account.Proxy = &Proxy{ID: proxyID, Status: StatusActive}
	require.False(t, policy.hydratedAccountEgressAllowed(account))
	account.Extra[AccountExecutionProxyExtraKey] = "99"
	require.True(t, policy.candidateAccountEgressAllowed(account))
	require.Equal(t, proxyID, account.requestProxy().ID)
	require.Equal(t, "api2", policy.nodeID(account))
	account.Extra[AccountExecutionProxyExtraKey] = "98"
	require.False(t, policy.hydratedAccountEgressAllowed(account))
	account.Extra[AccountExecutionProxyExtraKey] = "99"
	account.Proxy.Status = StatusDisabled
	require.False(t, policy.hydratedAccountEgressAllowed(account))
	account.Proxy.Status = StatusActive
	policy.healthy["api2"] = false
	require.False(t, policy.hydratedAccountEgressAllowed(account))
	policy.healthy["api2"] = true
	delete(policy.proxyIDs, "api2")
	require.False(t, policy.hydratedAccountEgressAllowed(account))
}

func TestExecutionProxyMarkerCannotBeImported(t *testing.T) {
	proxyID := int64(99)
	extra, _ := applyExecutionNodeForCreate(&config.Config{}, map[string]any{AccountExecutionProxyExtraKey: "99"}, &proxyID)
	require.NotContains(t, extra, AccountExecutionProxyExtraKey)
	extra = preserveExecutionNodeOnUpdate(&Account{ProxyID: &proxyID}, map[string]any{AccountExecutionProxyExtraKey: "99"})
	require.NotContains(t, extra, AccountExecutionProxyExtraKey)
}
