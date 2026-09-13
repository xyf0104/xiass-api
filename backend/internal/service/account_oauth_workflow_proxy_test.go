package service

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestOAuthWorkflowCreationPreservesChosenProxyAndLocalOwnership(t *testing.T) {
	cfg := &config.Config{}
	cfg.Gateway.ExecutionNode = config.GatewayExecutionNodeConfig{Enabled: true, ID: "api2", DefaultProxyID: 85}
	for _, id := range []int64{9, 85} {
		extra, actual := applyOAuthWorkflowNodeForCreate(cfg, map[string]any{AccountExecutionNodeExtraKey: "forged", AccountExecutionProxyExtraKey: "999"}, &id)
		require.Equal(t, id, *actual)
		require.Equal(t, "api2", extra[AccountExecutionNodeExtraKey])
		require.True(t, (&Account{ProxyID: actual, Extra: extra}).hasExplicitExecutionProxy())
		require.NotSame(t, &id, actual)
	}
	extra, actual := applyOAuthWorkflowNodeForCreate(cfg, nil, nil)
	require.Nil(t, actual)
	require.Equal(t, "api2", extra[AccountExecutionNodeExtraKey])
	require.NotContains(t, extra, AccountExecutionProxyExtraKey)
	// Generic imports retain the existing default-node policy.
	id := int64(9)
	_, actual = applyExecutionNodeForCreate(cfg, nil, &id)
	require.Equal(t, int64(85), *actual)
}

func TestOAuthWorkflowProxyCannotBeEnabledForGenericCreation(t *testing.T) {
	svc := &adminServiceImpl{}
	for _, input := range []CreateAccountInput{
		{Platform: PlatformOpenAI, Type: AccountTypeOAuth},
		{Platform: PlatformOpenAI, Type: AccountTypeAPIKey, AllowOpenAIReauthorizationCredentials: true},
		{Platform: PlatformAnthropic, Type: AccountTypeOAuth, AllowOpenAIReauthorizationCredentials: true},
	} {
		input.PreserveOAuthWorkflowProxy = true
		_, err := svc.CreateAccount(context.Background(), &input)
		require.ErrorContains(t, err, "trusted OpenAI OAuth workflow")
	}
}

type oauthWorkflowProxyRepo struct {
	AccountRepository
	account *Account
	groups  []int64
}

func (r *oauthWorkflowProxyRepo) Create(_ context.Context, account *Account) error {
	account.ID = 434
	r.account = account
	return nil
}

func (r *oauthWorkflowProxyRepo) BindGroups(_ context.Context, _ int64, groups []int64) error {
	r.groups = append([]int64(nil), groups...)
	return nil
}

func TestCreateAccountOAuthWorkflowPersistsSelectedConfiguration(t *testing.T) {
	for _, preserve := range []bool{false, true} {
		repo := &oauthWorkflowProxyRepo{}
		cfg := &config.Config{}
		cfg.Gateway.ExecutionNode = config.GatewayExecutionNodeConfig{Enabled: true, ID: "api2", DefaultProxyID: 85}
		svc := &adminServiceImpl{accountRepo: repo, settingService: &SettingService{cfg: cfg}}
		proxyID := int64(9)
		account, err := svc.CreateAccount(context.Background(), &CreateAccountInput{
			Name: "workflow-test", Platform: PlatformOpenAI, Type: AccountTypeOAuth,
			Credentials: map[string]any{}, Extra: map[string]any{"codex_fingerprint_mode": "off"},
			ProxyID: &proxyID, GroupIDs: []int64{7, 8}, Concurrency: 3, Priority: 1,
			AllowOpenAIReauthorizationCredentials: true, PreserveOAuthWorkflowProxy: preserve,
			SkipDefaultGroupBind: true, SkipMixedChannelCheck: true,
		})
		require.NoError(t, err)
		require.Same(t, repo.account, account)
		require.Equal(t, []int64{7, 8}, repo.groups)
		require.Equal(t, repo.groups, account.GroupIDs)
		require.Equal(t, 3, account.Concurrency)
		require.Equal(t, 1, account.Priority)
		require.Equal(t, "off", account.GetExtraString("codex_fingerprint_mode"))
		require.Equal(t, "api2", account.GetExtraString(AccountExecutionNodeExtraKey))
		if preserve {
			require.Equal(t, int64(9), *account.ProxyID)
			require.True(t, account.hasExplicitExecutionProxy())
		} else {
			require.Equal(t, int64(85), *account.ProxyID)
			require.False(t, account.hasExplicitExecutionProxy())
		}
	}
}
