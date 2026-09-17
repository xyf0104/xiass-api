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

func AccountMatchesSubscriptionPlan(account *Account, filter string) bool {
	filter = strings.TrimSpace(filter)
	if filter == "" {
		return true
	}
	if account == nil || !account.IsOpenAIOAuth() {
		return false
	}
	return OpenAISubscriptionPlanCategory(account.GetCredential("plan_type")) == filter
}

func AccountLoginMethodCategory(account *Account) string {
	if account == nil || !account.IsOpenAIOAuth() {
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
