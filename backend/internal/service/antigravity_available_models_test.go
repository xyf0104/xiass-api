package service

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAvailableAntigravityModelsUsesAccountCatalog(t *testing.T) {
	for _, plan := range []string{"Pro", "Ultra"} {
		t.Run(plan, func(t *testing.T) {
			body := `{"models":{"gemini-3.8-flash-tiered":{"supportsThinking":true,"maxTokens":1048576},"gemini-4-argon-disabled":{},"gemini-4-carbon-disabled":{}}}`
			if plan == "Ultra" {
				body = `{"models":{"gemini-3.8-flash-tiered":{"supportsThinking":true,"maxTokens":1048576},"gemini-4-argon-medium":{"supportsThinking":true,"maxTokens":1048576},"gemini-4-carbon-high":{"supportsThinking":true,"maxTokens":1048576},"claude-opus-5-5-medium":{"supportsThinking":true,"maxTokens":200000}}}`
			}
			repo := &upstreamModelCapabilityRepo{}
			svc, account, registry := antigravityCapabilitySyncFixture(t, body, repo)
			account.Credentials["plan_type"] = plan
			before, err := json.Marshal(account.Credentials)
			require.NoError(t, err)
			models, err := svc.AvailableAntigravityModels(context.Background(), account)
			require.NoError(t, err)
			ids := make([]string, 0, len(models))
			for _, model := range models {
				ids = append(ids, model.ID)
				require.NotEmpty(t, model.DisplayName)
				require.Equal(t, "model", model.Type)
			}
			require.Contains(t, ids, "gemini-3.8-flash-high")
			require.Contains(t, ids, "gemini-3.8-flash-medium")
			require.Contains(t, ids, "gemini-3.8-flash-low")
			require.NotContains(t, ids, "gemini-3.8-flash-tiered")
			require.NotContains(t, ids, "gemini-4-carbon-disabled")
			if plan == "Ultra" {
				require.Contains(t, ids, "gemini-4-carbon-high")
				require.Contains(t, ids, "gemini-4-argon-medium")
				require.Contains(t, ids, "claude-opus-5-5-medium")
			} else {
				require.Len(t, ids, 3, "Pro must not inherit Ultra-only picker defaults")
			}
			after, err := json.Marshal(account.Credentials)
			require.NoError(t, err)
			require.Equal(t, before, after)
			require.Empty(t, registry.requests)
			require.Equal(t, "preserve", account.Extra["xiass_setting"])
		})
	}
}

func TestAntigravityAvailableModelsValidatesFinalMapping(t *testing.T) {
	account := &Account{Platform: PlatformAntigravity, Type: AccountTypeOAuth, Credentials: map[string]any{
		"plan_type": "Ultra", "model_mapping": map[string]any{
			"claude-opus-5-5-medium": "not-in-catalog",
			"custom-model":           "custom-model",
		},
	}}
	models := antigravityAvailableModelOptions(account, &UpstreamModelCatalog{
		Models:   []string{"claude-opus-5-5-medium", "custom-model", "gemini-4-carbon-high", "gemini-3.7-flash-tiered"},
		Metadata: map[string]UpstreamModelMetadata{"custom-model": {DisplayName: "Custom model"}},
	})
	ids := make([]string, 0, len(models))
	for _, model := range models {
		ids = append(ids, model.ID)
		if model.ID == "custom-model" {
			require.Equal(t, "Custom model", model.DisplayName)
		}
	}
	require.ElementsMatch(t, []string{"custom-model", "gemini-3.7-flash-high", "gemini-3.7-flash-medium", "gemini-3.7-flash-low"}, ids)
}

func TestAvailableAntigravityModelsDoesNotFallbackToDefaults(t *testing.T) {
	svc, account, _ := antigravityCapabilitySyncFixture(t, `{"error":"unavailable"}`, &upstreamModelCapabilityRepo{})
	models, err := svc.AvailableAntigravityModels(context.Background(), account)
	require.Error(t, err)
	require.Nil(t, models)
	models, err = (*AccountTestService)(nil).AvailableAntigravityModels(context.Background(), account)
	require.Error(t, err)
	require.Nil(t, models)
}

func TestAntigravityAvailableModelsDoesNotPromotePartialTiers(t *testing.T) {
	account := &Account{Platform: PlatformAntigravity, Type: AccountTypeOAuth}
	models := antigravityAvailableModelOptions(account, &UpstreamModelCatalog{Models: []string{"gemini-3.7-flash-low"}})
	require.Len(t, models, 1)
	require.Equal(t, "gemini-3.7-flash-low", models[0].ID)
	require.Empty(t, antigravityAvailableModelOptions(account, &UpstreamModelCatalog{Models: []string{"gemini-4-carbon-disabled"}}))
}

func TestAntigravityAvailableModelsKeepsCallablePublicAliases(t *testing.T) {
	account := &Account{Platform: PlatformAntigravity, Type: AccountTypeOAuth, Credentials: map[string]any{
		"model_mapping": map[string]any{
			"my-claude":    "claude-opus-5-5-medium",
			"absent-alias": "not-available",
			"wildcard-*":   "claude-opus-5-5-medium",
		},
	}}
	models := antigravityAvailableModelOptions(account, &UpstreamModelCatalog{
		Models: []string{"claude-opus-5-5-medium"},
	})
	require.Len(t, models, 1)
	require.Equal(t, "my-claude", models[0].ID)
}
