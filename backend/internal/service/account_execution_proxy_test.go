//go:build unit

package service

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type executionProxyGroupRepoStub struct{ GroupRepository }

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
	updated, err = svc.UpdateAccount(ctx, account.ID, &UpdateAccountInput{ProxyID: &zero})
	require.NoError(t, err)
	require.NotNil(t, updated.ProxyID)
	require.Equal(t, int64(84), *updated.ProxyID)
	require.False(t, updated.hasExplicitExecutionProxy())
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
	extra, _ := applyExecutionNodeForCreate(&config.Config{}, map[string]any{
		AccountExecutionProxyExtraKey:         "99",
		OpenAIOAuthCredentialSourceIDExtraKey: "888",
	}, &proxyID)
	require.NotContains(t, extra, AccountExecutionProxyExtraKey)
	require.NotContains(t, extra, OpenAIOAuthCredentialSourceIDExtraKey)
	copyAccount := &Account{ID: 200, Platform: PlatformOpenAI, Type: AccountTypeOAuth, ProxyID: &proxyID, Extra: map[string]any{
		OpenAIOAuthCredentialSourceIDExtraKey: "100",
	}}
	extra = preserveExecutionNodeOnUpdate(copyAccount, map[string]any{
		AccountExecutionProxyExtraKey:         "99",
		OpenAIOAuthCredentialSourceIDExtraKey: "888",
	})
	require.NotContains(t, extra, AccountExecutionProxyExtraKey)
	require.Equal(t, "100", extra[OpenAIOAuthCredentialSourceIDExtraKey])
}

func TestAdminCreateBindsPrivateEgressFromSharedNodeMapping(t *testing.T) {
	repo := newDuplicateAccountRepoStub()
	cfg := &config.Config{}
	cfg.Gateway.ExecutionNode = config.GatewayExecutionNodeConfig{
		Enabled:                true,
		ID:                     "api",
		LegacyUnassignedNodeID: "api",
	}
	settings := NewSettingService(&executionNodeSettingRepo{values: map[string]string{
		SettingKeyExecutionNodeBalancingEnabled: "true",
		SettingKeyExecutionNodeWeights:          `{"api":9,"api2":1}`,
		SettingKeyExecutionNodeProxyIDs:         `{"api":84,"api2":83}`,
	}}, cfg)
	svc := &adminServiceImpl{
		accountRepo:    repo,
		groupRepo:      &executionProxyGroupRepoStub{},
		settingService: settings,
	}

	created, err := svc.CreateAccount(context.Background(), &CreateAccountInput{
		Name:                 "imported",
		Platform:             PlatformOpenAI,
		Type:                 AccountTypeAPIKey,
		Credentials:          map[string]any{"api_key": "secret"},
		SkipDefaultGroupBind: true,
	})

	require.NoError(t, err)
	require.Equal(t, "api", created.ExecutionNodeID(""))
	require.NotNil(t, created.ProxyID)
	require.Equal(t, int64(84), *created.ProxyID)
}

func TestAdminTrustedOAuthCreateFallsBackToManagedNodeEgress(t *testing.T) {
	repo := newDuplicateAccountRepoStub()
	cfg := &config.Config{}
	cfg.Gateway.ExecutionNode = config.GatewayExecutionNodeConfig{
		Enabled:                true,
		ID:                     "api2",
		LegacyUnassignedNodeID: "api",
	}
	settings := NewSettingService(&executionNodeSettingRepo{values: map[string]string{
		SettingKeyExecutionNodeBalancingEnabled: "true",
		SettingKeyExecutionNodeWeights:          `{"api":9,"api2":1}`,
		SettingKeyExecutionNodeProxyIDs:         `{"api":84,"api2":83}`,
	}}, cfg)
	svc := &adminServiceImpl{
		accountRepo:    repo,
		groupRepo:      &executionProxyGroupRepoStub{},
		settingService: settings,
	}

	created, err := svc.CreateAccount(context.Background(), &CreateAccountInput{
		Name:                                  "oauth-workflow",
		Platform:                              PlatformOpenAI,
		Type:                                  AccountTypeOAuth,
		Credentials:                           map[string]any{"access_token": "secret"},
		AllowOpenAIReauthorizationCredentials: true,
		PreserveOAuthWorkflowProxy:            true,
		SkipDefaultGroupBind:                  true,
	})

	require.NoError(t, err)
	require.Equal(t, "api2", created.ExecutionNodeID("api"))
	require.NotNil(t, created.ProxyID)
	require.Equal(t, int64(83), *created.ProxyID)
	require.False(t, created.hasExplicitExecutionProxy())
}

func TestAdminBulkProxyResetRestoresEachAccountManagedNodeEgress(t *testing.T) {
	ctx := context.Background()
	apiAccount := executionNodeTestAccount(903, "api", 99)
	api2Account := executionNodeTestAccount(904, "api2", 98)
	apiAccount.Platform, apiAccount.Type = PlatformOpenAI, AccountTypeAPIKey
	api2Account.Platform, api2Account.Type = PlatformOpenAI, AccountTypeAPIKey
	apiAccount.Extra[AccountExecutionProxyExtraKey] = "99"
	api2Account.Extra[AccountExecutionProxyExtraKey] = "98"
	repo := &upstreamBillingProbeAccountRepo{accounts: map[int64]*Account{
		apiAccount.ID:  apiAccount,
		api2Account.ID: api2Account,
	}}
	settings, settingRepo := verifiedPairedAdminService(t, "api")
	settings.cfg.Gateway.ExecutionNode.LegacyUnassignedNodeID = "api"
	settingRepo.values[SettingKeyExecutionNodeBalancingEnabled] = "true"
	settingRepo.values[SettingKeyExecutionNodeWeights] = `{"api":9,"api2":1}`
	settingRepo.values[SettingKeyExecutionNodeProxyIDs] = `{"api":84,"api2":83}`
	svc := &adminServiceImpl{accountRepo: &upstreamBillingProbeAdminRepo{repo}, settingService: settings}
	zero := int64(0)

	result, err := svc.BulkUpdateAccounts(ctx, &BulkUpdateAccountsInput{
		AccountIDs: []int64{apiAccount.ID, api2Account.ID},
		ProxyID:    &zero,
	})

	require.NoError(t, err)
	require.Equal(t, 2, result.Success)
	require.Len(t, repo.bulkUpdates, 3)
	require.Nil(t, repo.bulkUpdates[0].ProxyID)
	require.Nil(t, repo.bulkUpdates[0].Extra[AccountExecutionProxyExtraKey])
	require.Equal(t, []int64{apiAccount.ID, api2Account.ID}, repo.bulkUpdateIDs[0])
	require.Equal(t, int64(84), *repo.bulkUpdates[1].ProxyID)
	require.Equal(t, []int64{apiAccount.ID}, repo.bulkUpdateIDs[1])
	require.Equal(t, int64(83), *repo.bulkUpdates[2].ProxyID)
	require.Equal(t, []int64{api2Account.ID}, repo.bulkUpdateIDs[2])
}
