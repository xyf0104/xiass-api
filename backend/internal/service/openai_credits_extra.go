package service

import (
	"maps"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const OpenAICreditsEnabledExtraKey = openAICreditsEnabledExtraKey

// ValidateOpenAICreditsExtra keeps the administrator opt-in separate from the
// provider-owned Credit snapshot. Only OpenAI OAuth accounts can own the flag.
func ValidateOpenAICreditsExtra(platform, accountType string, extra map[string]any) error {
	if extra == nil {
		return nil
	}
	raw, exists := extra[OpenAICreditsEnabledExtraKey]
	if !exists {
		return nil
	}
	if _, ok := raw.(bool); !ok {
		return infraerrors.BadRequest(
			"OPENAI_CREDITS_ENABLED_INVALID",
			"codex_credits_enabled must be a boolean",
		)
	}
	if platform != PlatformOpenAI || accountType != AccountTypeOAuth {
		return infraerrors.BadRequest(
			"OPENAI_CREDITS_OPENAI_OAUTH_ONLY",
			"codex_credits_enabled is only available for OpenAI OAuth accounts",
		)
	}
	return nil
}

func normalizeOpenAICreditsExtra(platform, accountType string, extra map[string]any) (map[string]any, error) {
	normalized := stripOpenAIQuotaRuntimeExtra(extra)
	if err := ValidateOpenAICreditsExtra(platform, accountType, normalized); err != nil {
		return nil, err
	}
	if platform != PlatformOpenAI || accountType != AccountTypeOAuth {
		return normalized, nil
	}
	if normalized == nil {
		normalized = make(map[string]any, 1)
	} else {
		normalized = maps.Clone(normalized)
	}
	if _, exists := normalized[OpenAICreditsEnabledExtraKey]; !exists {
		normalized[OpenAICreditsEnabledExtraKey] = false
	}
	return normalized, nil
}

func normalizeOpenAICreditsUpdateExtra(account *Account, input *UpdateAccountInput, normalized map[string]any) (map[string]any, error) {
	if account == nil || input == nil {
		return normalized, nil
	}
	targetType := account.Type
	if input.Type != "" {
		targetType = input.Type
	}
	incoming := stripOpenAIQuotaRuntimeExtra(input.Extra)
	if err := ValidateOpenAICreditsExtra(account.Platform, targetType, incoming); err != nil {
		return nil, err
	}
	normalized = stripOpenAIQuotaRuntimeExtra(normalized)
	if account.Platform != PlatformOpenAI || targetType != AccountTypeOAuth {
		delete(normalized, OpenAICreditsEnabledExtraKey)
		return normalized, nil
	}
	if normalized == nil {
		normalized = make(map[string]any, 1)
	}
	if _, provided := incoming[OpenAICreditsEnabledExtraKey]; provided {
		return normalized, nil
	}
	if current, ok := account.Extra[OpenAICreditsEnabledExtraKey].(bool); ok {
		normalized[OpenAICreditsEnabledExtraKey] = current
	} else {
		normalized[OpenAICreditsEnabledExtraKey] = false
	}
	return normalized, nil
}
