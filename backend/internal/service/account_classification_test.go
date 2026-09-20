package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOpenAISubscriptionPlanCategory(t *testing.T) {
	tests := map[string]string{
		"free":                            AccountSubscriptionPlanFree,
		"ChatGPT Plus":                    AccountSubscriptionPlanPlus,
		"chatgpt_pro":                     AccountSubscriptionPlanPro,
		"self_serve_business_usage_based": AccountSubscriptionPlanTeam,
		"enterprise":                      AccountSubscriptionPlanOther,
		"":                                AccountSubscriptionPlanOther,
	}
	for raw, want := range tests {
		t.Run(raw, func(t *testing.T) {
			require.Equal(t, want, OpenAISubscriptionPlanCategory(raw))
		})
	}
}

func TestAccountLoginMethodCategoryRequiresCompleteCredentials(t *testing.T) {
	account := &Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth, Credentials: map[string]any{
		OpenAIOAuthReauthorizationEmailCredentialKey:      "owner@example.test",
		OpenAIOAuthReauthorizationPasswordCredentialKey:   "encrypted-password",
		OpenAIOAuthReauthorizationTOTPSecretCredentialKey: "encrypted-totp",
	}}
	require.Equal(t, AccountLoginMethodPassword2FA, AccountLoginMethodCategory(account))
	require.True(t, AccountMatchesLoginMethod(account, AccountLoginMethodPassword2FA))

	account.Credentials[OpenAIOAuthReauthorizationPasswordCredentialKey] = ""
	require.Equal(t, AccountLoginMethodUnconfigured, AccountLoginMethodCategory(account))

	account.Credentials[OpenAIOAuthReauthorizationEmailCodeTokenCredentialKey] = "encrypted-email-code"
	require.Equal(t, AccountLoginMethodEmailCode, AccountLoginMethodCategory(account))

	account.Platform = PlatformAnthropic
	require.Empty(t, AccountLoginMethodCategory(account))
	require.False(t, AccountMatchesLoginMethod(account, AccountLoginMethodEmailCode))
}

func TestAccountClassificationFilterValidation(t *testing.T) {
	require.True(t, IsValidAccountSubscriptionPlanFilter(AccountSubscriptionPlanPlus))
	require.True(t, IsValidAccountLoginMethodFilter(AccountLoginMethodEmailCode))
	require.False(t, IsValidAccountSubscriptionPlanFilter("enterprise"))
	require.False(t, IsValidAccountLoginMethodFilter("password"))
}

func TestAccountSubscriptionPlanClassificationSupportsSetupTokenAndLegacyFields(t *testing.T) {
	account := &Account{
		Platform: PlatformOpenAI,
		Type:     AccountTypeSetupToken,
		Extra: map[string]any{
			"chatgpt_plan_type": "business",
		},
	}
	require.Equal(t, "business", OpenAIAccountSubscriptionPlan(account))
	require.True(t, AccountMatchesSubscriptionPlan(account, AccountSubscriptionPlanTeam))
}

func TestAccountSubscriptionPlanClassificationTreatsBusinessPremiumAsTeam(t *testing.T) {
	account := &Account{
		Platform: PlatformOpenAI,
		Type:     AccountTypeOAuth,
		Credentials: map[string]any{
			"plan_type": "self_serve_business_prolite",
		},
	}
	require.Equal(t, AccountSubscriptionPlanTeam, OpenAISubscriptionPlanCategory("self_serve_business_prolite"))
	require.True(t, AccountMatchesSubscriptionPlan(account, AccountSubscriptionPlanTeam))
}
