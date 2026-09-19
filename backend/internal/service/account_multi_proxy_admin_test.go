//go:build unit

package service

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
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
	expiredAt := time.Now().Add(-time.Hour)
	admin := &adminServiceImpl{proxyRepo: &accountMultiProxyRepoStub{proxies: []Proxy{
		{ID: 1, Status: StatusActive},
		{ID: 2, Status: StatusDisabled},
		{ID: 4, Status: StatusActive, ExpiresAt: &expiredAt},
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

	_, _, _, err = admin.validateAccountProxyBindings(context.Background(), []AccountProxyBindingInput{{ProxyID: 4, MaxConcurrency: 1}})
	require.ErrorContains(t, err, "expired")
}

func TestValidateAccountProxyBindingsPreservesOnlyExistingUnavailableBindings(t *testing.T) {
	expiredAt := time.Now().Add(-time.Hour)
	admin := &adminServiceImpl{proxyRepo: &accountMultiProxyRepoStub{proxies: []Proxy{
		{ID: 1, Status: StatusDisabled},
		{ID: 2, Status: StatusActive, ExpiresAt: &expiredAt},
		{ID: 3, Status: StatusDisabled},
	}}}
	existing := []AccountProxyBindingInput{
		{ProxyID: 1, MaxConcurrency: 2},
		{ProxyID: 2, MaxConcurrency: 3},
	}
	ctx := WithPreservedAccountProxyBindings(context.Background(), existing)

	bindings, total, _, err := admin.validateAccountProxyBindings(ctx, []AccountProxyBindingInput{
		{ProxyID: 1, MaxConcurrency: 4},
		{ProxyID: 2, MaxConcurrency: 5},
	})
	require.NoError(t, err)
	require.Equal(t, 9, total)
	require.Equal(t, []AccountProxyBindingInput{
		{ProxyID: 1, MaxConcurrency: 4},
		{ProxyID: 2, MaxConcurrency: 5},
	}, bindings)

	_, _, _, err = admin.validateAccountProxyBindings(ctx, []AccountProxyBindingInput{
		{ProxyID: 1, MaxConcurrency: 4},
		{ProxyID: 3, MaxConcurrency: 1},
	})
	require.ErrorContains(t, err, "proxy 3 is inactive")

	_, _, _, err = admin.validateAccountProxyBindings(ctx, []AccountProxyBindingInput{{ProxyID: 99, MaxConcurrency: 1}})
	require.ErrorContains(t, err, "does not exist")
}

func TestDuplicateMultiProxyAccountKeepsManagedExecutionProxy(t *testing.T) {
	ctx := context.Background()
	repo := newDuplicateAccountRepoStub()
	cfg := &config.Config{}
	cfg.Gateway.ExecutionNode = config.GatewayExecutionNodeConfig{
		Enabled:                true,
		ID:                     "api2",
		LegacyUnassignedNodeID: "api",
		DefaultProxyID:         83,
	}
	settings := NewSettingService(&executionNodeSettingRepo{values: map[string]string{
		SettingKeyExecutionNodeBalancingEnabled: "true",
		SettingKeyExecutionNodeWeights:          `{"api":9,"api2":1}`,
		SettingKeyExecutionNodeProxyIDs:         `{"api":84,"api2":83}`,
	}}, cfg)
	bindings := []AccountProxyBindingInput{
		{ProxyID: 10, MaxConcurrency: 2},
		{ProxyID: 11, MaxConcurrency: 3},
	}
	managedProxyID := int64(83)
	source := &Account{
		Name:        "source",
		Platform:    PlatformAnthropic,
		Type:        AccountTypeAPIKey,
		Credentials: map[string]any{"api_key": "secret"},
		ProxyID:     &managedProxyID,
		Concurrency: 5,
		Extra: setAccountProxyBindingsExtra(map[string]any{
			AccountExecutionNodeExtraKey: "api2",
		}, bindings),
	}
	require.NoError(t, repo.Create(ctx, source))
	svc := &adminServiceImpl{
		accountRepo:          repo,
		accountDuplicateRepo: repo,
		proxyRepo: &accountMultiProxyRepoStub{proxies: []Proxy{
			{ID: 10, Status: StatusDisabled},
			{ID: 11, Status: StatusActive},
		}},
		settingService: settings,
	}

	duplicate, err := svc.DuplicateAccount(ctx, source.ID, "admin:1", "")

	require.NoError(t, err)
	require.NotNil(t, duplicate.ProxyID)
	require.Equal(t, managedProxyID, *duplicate.ProxyID)
	require.Equal(t, "api2", duplicate.Extra[AccountExecutionNodeExtraKey])
	require.Equal(t, 3, duplicate.Concurrency, "inactive bindings stay configured but contribute no runtime capacity")
	require.Equal(t, bindings, AccountProxyBindingInputsFromExtra(duplicate.Extra))
}

func TestExecutionNodeCreateAndUpdateKeepManagedProxyWithMultiProxy(t *testing.T) {
	ctx := context.Background()
	repo := newDuplicateAccountRepoStub()
	proxyRepo := &accountMultiProxyRepoStub{proxies: []Proxy{
		{ID: 10, Status: StatusActive},
		{ID: 11, Status: StatusActive},
		{ID: 12, Status: StatusDisabled},
	}}
	cfg := &config.Config{}
	cfg.Gateway.ExecutionNode = config.GatewayExecutionNodeConfig{
		Enabled:                true,
		ID:                     "api2",
		LegacyUnassignedNodeID: "api",
		DefaultProxyID:         83,
	}
	settings := NewSettingService(&executionNodeSettingRepo{values: map[string]string{
		SettingKeyExecutionNodeBalancingEnabled: "true",
		SettingKeyExecutionNodeWeights:          `{"api":9,"api2":1}`,
		SettingKeyExecutionNodeProxyIDs:         `{"api":84,"api2":83}`,
	}}, cfg)
	svc := &adminServiceImpl{
		accountRepo:    repo,
		groupRepo:      &executionProxyGroupRepoStub{},
		proxyRepo:      proxyRepo,
		settingService: settings,
	}
	bindings := []AccountProxyBindingInput{
		{ProxyID: 11, MaxConcurrency: 3},
		{ProxyID: 10, MaxConcurrency: 2},
	}

	created, err := svc.CreateAccount(ctx, &CreateAccountInput{
		Name:                 "multi",
		Platform:             PlatformOpenAI,
		Type:                 AccountTypeAPIKey,
		Credentials:          map[string]any{"api_key": "secret"},
		ProxyBindings:        bindings,
		SkipDefaultGroupBind: true,
	})
	require.NoError(t, err)
	require.NotNil(t, created.ProxyID)
	require.Equal(t, int64(83), *created.ProxyID)
	require.Equal(t, []AccountProxyBindingInput{
		{ProxyID: 10, MaxConcurrency: 2},
		{ProxyID: 11, MaxConcurrency: 3},
	}, AccountProxyBindingInputsFromExtra(created.Extra))

	proxyRepo.proxies[0].Status = StatusDisabled
	preserved := []AccountProxyBindingInput{
		{ProxyID: 10, MaxConcurrency: 4},
		{ProxyID: 11, MaxConcurrency: 3},
	}
	updated, err := svc.UpdateAccount(ctx, created.ID, &UpdateAccountInput{ProxyBindings: &preserved})
	require.NoError(t, err)
	require.NotNil(t, updated.ProxyID)
	require.Equal(t, int64(83), *updated.ProxyID)
	require.Equal(t, preserved, AccountProxyBindingInputsFromExtra(updated.Extra))

	withNewInactive := append(append([]AccountProxyBindingInput(nil), preserved...), AccountProxyBindingInput{ProxyID: 12, MaxConcurrency: 1})
	_, err = svc.UpdateAccount(ctx, created.ID, &UpdateAccountInput{ProxyBindings: &withNewInactive})
	require.ErrorContains(t, err, "proxy 12 is inactive")
}

func TestCreateAccountAllowsUnavailableBindingOnlyForTrustedRestore(t *testing.T) {
	ctx := context.Background()
	repo := newDuplicateAccountRepoStub()
	bindings := []AccountProxyBindingInput{{ProxyID: 10, MaxConcurrency: 2}}
	svc := &adminServiceImpl{
		accountRepo: repo,
		groupRepo:   &executionProxyGroupRepoStub{},
		proxyRepo: &accountMultiProxyRepoStub{proxies: []Proxy{
			{ID: 10, Status: StatusDisabled},
		}},
	}
	input := func(name string) *CreateAccountInput {
		return &CreateAccountInput{
			Name:                 name,
			Platform:             PlatformOpenAI,
			Type:                 AccountTypeAPIKey,
			Credentials:          map[string]any{"api_key": "secret"},
			ProxyBindings:        bindings,
			SkipDefaultGroupBind: true,
		}
	}

	_, err := svc.CreateAccount(ctx, input("new-binding"))
	require.ErrorContains(t, err, "proxy 10 is inactive")

	restored, err := svc.CreateAccount(WithPreservedAccountProxyBindings(ctx, bindings), input("restored-binding"))
	require.NoError(t, err)
	require.Equal(t, bindings, AccountProxyBindingInputsFromExtra(restored.Extra))
	require.NotNil(t, restored.ProxyID)
	require.Equal(t, int64(10), *restored.ProxyID)
	require.Zero(t, restored.Concurrency, "restored inactive binding must not become schedulable capacity")
}
