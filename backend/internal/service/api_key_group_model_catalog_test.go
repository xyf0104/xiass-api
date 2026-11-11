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

func TestModelsForAPIKeyGroupOpenAIOAuthMatchesFirstFrameCatalog(t *testing.T) {
	group := &Group{ID: 46, Platform: PlatformOpenAI}
	for _, accountType := range []string{AccountTypeOAuth, AccountTypeAPIKey} {
		t.Run(accountType, func(t *testing.T) {
			repo := &gatewayModelsCatalogRepoStub{accounts: []Account{{ID: 1, Platform: PlatformOpenAI, Type: accountType, Status: StatusActive, Schedulable: true}}}
			gateway := &GatewayService{accountRepo: repo}
			openAI := &OpenAIGatewayService{accountRepo: repo}
			models, err := gateway.ModelsForAPIKeyGroup(context.Background(), group)
			require.NoError(t, err)
			wsModels, err := openAI.ModelsForAPIKeyGroup(context.Background(), group)
			require.NoError(t, err)
			require.Equal(t, wsModels, models)
			if accountType == AccountTypeOAuth {
				require.Contains(t, models, "gpt-5.6-luna")
			} else {
				require.Empty(t, models, "an undeclared upstream key must not inherit OAuth models")
			}
		})
	}
}

type gatewayModelsCatalogRepoStub struct {
	AccountRepository
	accounts []Account
}

func TestModelsForAPIKeyGroupNativeGeminiChecksCompositeDestination(t *testing.T) {
	for _, platform := range []string{PlatformOpenAI, PlatformAnthropic, PlatformGrok, PlatformGemini, PlatformAntigravity} {
		for _, explicit := range []bool{false, true} {
			t.Run(platform+map[bool]string{false: "/account", true: "/explicit"}[explicit], func(t *testing.T) {
				const model = "gemini-3.7-flash-high"
				accountPlatform := platform
				var resolver *CompositeRouteResolver
				if explicit {
					// Explicit routes must win even when a same-named account
					// mapping belongs to a different provider.
					accountPlatform = PlatformOpenAI
					resolver = NewCompositeRouteResolver(compositeRouteRepoStub{routes: []CompositeModelRoute{{ID: 1, GroupID: 1, PublicModel: model, MatchType: CompositeRouteMatchExact, TargetPlatform: platform, Endpoint: CompositeRouteEndpointGemini, Enabled: true}}})
				}
				repo := &gatewayModelsCatalogRepoStub{accounts: []Account{{ID: 1, Platform: accountPlatform, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Credentials: map[string]any{"model_mapping": map[string]any{model: model}}}}}
				svc := &GatewayService{accountRepo: repo, compositeResolver: resolver}
				allowed, err := svc.SupportsAPIKeyGroupGeminiModel(context.Background(), &Group{ID: 1, Platform: PlatformComposite}, model)
				require.NoError(t, err)
				require.Equal(t, platform == PlatformGemini || platform == PlatformAntigravity, allowed)
			})
		}
	}
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
