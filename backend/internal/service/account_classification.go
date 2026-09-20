package service

import "strings"

const (
	AccountSubscriptionPlanFree  = "free"
	AccountSubscriptionPlanPlus  = "plus"
	AccountSubscriptionPlanPro   = "pro"
	AccountSubscriptionPlanTeam  = "team"
	AccountSubscriptionPlanOther = "other"

	AccountLoginMethodPassword2FA  = "password_2fa"
	AccountLoginMethodEmailCode    = "email_code"
	AccountLoginMethodUnconfigured = "unconfigured"
)

var knownAccountSubscriptionPlanFilters = map[string]struct{}{
	AccountSubscriptionPlanFree:  {},
	AccountSubscriptionPlanPlus:  {},
	AccountSubscriptionPlanPro:   {},
	AccountSubscriptionPlanTeam:  {},
	AccountSubscriptionPlanOther: {},
}

var knownAccountLoginMethodFilters = map[string]struct{}{
	AccountLoginMethodPassword2FA:  {},
	AccountLoginMethodEmailCode:    {},
	AccountLoginMethodUnconfigured: {},
}

func IsValidAccountSubscriptionPlanFilter(value string) bool {
	if value = strings.TrimSpace(value); value == "" {
		return true
	}
	_, ok := knownAccountSubscriptionPlanFilters[value]
	return ok
}

func IsValidAccountLoginMethodFilter(value string) bool {
	if value = strings.TrimSpace(value); value == "" {
		return true
	}
	_, ok := knownAccountLoginMethodFilters[value]
	return ok
}

func OpenAISubscriptionPlanCategory(value string) string {
	normalized := strings.NewReplacer("_", "", "-", "", " ", "").Replace(strings.ToLower(strings.TrimSpace(value)))
	switch normalized {
	case "free", "basic", "chatgptfree":
		return AccountSubscriptionPlanFree
	case "plus", "chatgptplus":
		return AccountSubscriptionPlanPlus
	case "pro", "chatgptpro":
		return AccountSubscriptionPlanPro
	case "team", "chatgptteam", "business", "chatgptbusiness", "selfservebusiness", "selfservebusinessusagebased":
		return AccountSubscriptionPlanTeam
	default:
		return AccountSubscriptionPlanOther
	}
}

// OpenAIAccountSubscriptionPlan returns the most useful persisted plan field
// for account-management classification. Older imports used
// chatgpt_plan_type/subscription_plan while current OAuth writes plan_type.
func OpenAIAccountSubscriptionPlan(account *Account) string {
	if account == nil {
		return ""
	}
	for _, key := range []string{"plan_type", "chatgpt_plan_type", "subscription_plan"} {
		if value := strings.TrimSpace(account.GetCredential(key)); value != "" {
			return value
		}
	}
	for _, key := range []string{"plan_type", "chatgpt_plan_type", "subscription_plan"} {
		if value := strings.TrimSpace(account.GetExtraString(key)); value != "" {
			return value
		}
	}
	return ""
}

func AccountMatchesSubscriptionPlan(account *Account, filter string) bool {
	filter = strings.TrimSpace(filter)
	if filter == "" {
		return true
	}
	if account == nil || !account.IsOpenAIOAuthLike() {
		return false
	}
	return OpenAISubscriptionPlanCategory(OpenAIAccountSubscriptionPlan(account)) == filter
}

func AccountLoginMethodCategory(account *Account) string {
	if account == nil || !account.IsOpenAIOAuthLike() {
		return ""
	}
	hasEmail := accountHasNonEmptyCredential(account, OpenAIOAuthReauthorizationEmailCredentialKey)
	hasPassword2FA := hasEmail &&
		accountHasNonEmptyCredential(account, OpenAIOAuthReauthorizationPasswordCredentialKey) &&
		accountHasNonEmptyCredential(account, OpenAIOAuthReauthorizationTOTPSecretCredentialKey)
	if hasPassword2FA {
		return AccountLoginMethodPassword2FA
	}
	if hasEmail && accountHasNonEmptyCredential(account, OpenAIOAuthReauthorizationEmailCodeTokenCredentialKey) {
		return AccountLoginMethodEmailCode
	}
	return AccountLoginMethodUnconfigured
}

func AccountMatchesLoginMethod(account *Account, filter string) bool {
	filter = strings.TrimSpace(filter)
	if filter == "" {
		return true
	}
	return AccountLoginMethodCategory(account) == filter
}

func accountHasNonEmptyCredential(account *Account, key string) bool {
	if account == nil || account.Credentials == nil {
		return false
	}
	value, ok := account.Credentials[key].(string)
	return ok && strings.TrimSpace(value) != ""
}
