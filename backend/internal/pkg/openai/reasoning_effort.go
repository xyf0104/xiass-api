package openai

import (
	"fmt"
	"strings"
)

func IsGPT61TerraModelSpelling(model string) bool {
	canonical := CanonicalizeOpenAIModelAliasSpelling(model)
	if canonical == "gpt-6.1-terra" {
		return true
	}
	suffix, ok := strings.CutPrefix(canonical, "gpt-6.1-terra-")
	if !ok {
		return false
	}
	switch suffix {
	case "none", "minimal", "low", "medium", "high", "xhigh", "max", "openai-compact":
		return true
	default:
		return false
	}
}

func ValidateGPT61TerraReasoningEffort(model, effort string) error {
	if IsGPT61TerraModelSpelling(model) && strings.EqualFold(strings.TrimSpace(effort), "minimal") {
		return fmt.Errorf("gpt-6.1-terra does not support reasoning effort %q; use none, low, medium, high, xhigh or max", effort)
	}
	return nil
}

func ValidateGPT61ReasoningEffort(model, effort string) error {
	if err := ValidateGPT62ReasoningEffort(model, effort); err != nil {
		return err
	}
	if err := ValidateGPT61SolReasoningEffort(model, effort); err != nil {
		return err
	}
	return ValidateGPT61TerraReasoningEffort(model, effort)
}

func ValidateGPT62ReasoningEffort(model, effort string) error {
	switch strings.ToLower(strings.TrimSpace(effort)) {
	case "none", "minimal":
		if base := GPT62ModelID(model); base != "" {
			return fmt.Errorf("%s does not support reasoning effort %q; use low, medium, high, xhigh or max", base, effort)
		}
	}
	return nil
}
