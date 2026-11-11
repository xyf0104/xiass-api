package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestModelsForAPIKeyGroupUsesExistingAvailableModelSource(t *testing.T) {
	groupID := int64(41)
	repo := &gatewayModelsCatalogRepoStub{accounts: []Account{
		{ID: 1, Platform: PlatformOpenAI, Status: StatusActive, Schedulable: true, Credentials: map[string]any{
			"model_mapping": map[string]any{"gpt-shared": "upstream-shared", "gpt-a": "upstream-a"},
		}},
		{ID: 2, Platform: PlatformOpenAI, Status: StatusDisabled, Schedulable: true, Credentials: map[string]any{
			"model_mapping": map[string]any{"gpt-disabled": "upstream-disabled"},
		}},
	}}
	svc := &GatewayService{accountRepo: repo}

	models, err := svc.ModelsForAPIKeyGroup(context.Background(), &Group{ID: groupID, Platform: PlatformOpenAI})
	require.NoError(t, err)
	require.Equal(t, []string{"gpt-a", "gpt-shared"}, models)
}

func TestModelsForAPIKeyGroupCarbonRequiresCapableAccount(t *testing.T) {
	pro := Account{ID: 1, Platform: PlatformAntigravity, Type: AccountTypeOAuth,
		Status: StatusActive, Schedulable: true, Credentials: map[string]any{"plan_type": "Pro"}}
	ultra := Account{ID: 2, Platform: PlatformAntigravity, Type: AccountTypeOAuth,
		Status: StatusActive, Schedulable: true, Credentials: map[string]any{"plan_type": "Ultra"}}
	for _, platform := range []string{"", PlatformAntigravity} {
		proModels := modelIDsFromAvailableAccounts([]Account{pro}, platform)
		mixedModels := modelIDsFromAvailableAccounts([]Account{pro, ultra}, platform)
		for _, tier := range []string{"high", "medium", "low"} {
			model := "gemini-4-carbon-" + tier
			require.NotContains(t, proModels, model)
			require.Contains(t, mixedModels, model)
		}
		require.Contains(t, proModels, "gemini-3.7-flash-high")
	}
}

type gatewayModelsCatalogRepoStub struct {
	AccountRepository
	accounts []Account
}

func (r *gatewayModelsCatalogRepoStub) ListSchedulableByGroupID(context.Context, int64) ([]Account, error) {
	return r.schedulableAccounts(), nil
}

func (r *gatewayModelsCatalogRepoStub) ListSchedulable(context.Context) ([]Account, error) {
	return r.schedulableAccounts(), nil
}

func (r *gatewayModelsCatalogRepoStub) schedulableAccounts() []Account {
	accounts := make([]Account, 0, len(r.accounts))
	for _, account := range r.accounts {
		if account.Status == StatusActive && account.Schedulable {
			accounts = append(accounts, account)
		}
	}
	return accounts
}
