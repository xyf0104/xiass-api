package service

import (
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
	"github.com/Wei-Shaw/sub2api/internal/pkg/timezone"
)

// Use the request instant for delayed settlement. March 1 is before Pacific DST.
func openAIUltrafastMultiplier(model string, at time.Time) float64 {
	if at.IsZero() {
		at = timezone.Now()
	}
	if openai.GPT62ModelID(model) != "" && at.Before(time.Date(2027, time.March, 1, 8, 0, 0, 0, time.UTC)) {
		return 3
	}
	return 6
}

func openAISubscriptionLongContextExempt(account *Account, result *OpenAIForwardResult) bool {
	if !account.IsOpenAIOAuthLike() || result == nil {
		return false
	}
	model := strings.TrimSpace(result.UpstreamModel)
	if model == "" {
		model = result.Model
	}
	return openai.GPT62ModelID(model) != ""
}
