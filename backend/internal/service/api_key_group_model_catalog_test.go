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
