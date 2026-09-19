package service

import (
	"encoding/json"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
	"go.uber.org/zap"
)

const OllamaCloudMaxTokensCapExtraKey = "ollama_max_tokens_cap"
const ollamaCloudDefaultMaxTokensCap = 65535

func clampOllamaCloudUpstreamMaxTokens(account *Account, body []byte) []byte {
	if account == nil || len(body) == 0 || !isOllamaCloudBaseURL(account.GetOpenAIBaseURL()) {
		return body
	}
	if !isDeepSeekModel(gjson.GetBytes(body, "model").String()) && !isOllamaCloudRawChatCompletionsAccount(account) {
		return body
	}
	return clampOllamaCloudMaxTokens(account, body)
}

func ollamaCloudResponsesUpstreamBaseURL(account *Account) string {
	if account != nil && account.UsesNativeCNResponses() && account.IsAdaptiveAPIProtocol() {
		return account.GetCNProtocolBaseURL(APIProtocolResponses)
	}
	if account == nil {
		return ""
	}
	return account.GetOpenAIBaseURL()
}

func ollamaCloudResponsesMaxOutputTokensClamp(account *Account, upstreamModel string, body []byte) (int64, bool) {
	if account == nil || account.Type != AccountTypeAPIKey || !isDeepSeekModel(upstreamModel) ||
		!isOllamaCloudBaseURL(ollamaCloudResponsesUpstreamBaseURL(account)) {
		return 0, false
	}
	value := gjson.GetBytes(body, "max_output_tokens")
	if !value.Exists() && account.Platform == PlatformOpenAI {
		value = gjson.GetBytes(body, "max_tokens")
	}
	cap := ollamaCloudMaxTokensCap(account)
	if cap <= 0 || !value.Exists() || value.Type != gjson.Number || value.Int() <= cap {
		return 0, false
	}
	return cap, true
}

func ollamaCloudMaxTokensCap(account *Account) int64 {
	if account == nil || account.Extra == nil {
		return ollamaCloudDefaultMaxTokensCap
	}
	value, ok := account.Extra[OllamaCloudMaxTokensCapExtraKey]
	if !ok {
		return ollamaCloudDefaultMaxTokensCap
	}
	switch number := value.(type) {
	case float64:
		return int64(number)
	case int64:
		return number
	case int:
		return int64(number)
	case json.Number:
		parsed, err := number.Int64()
		if err == nil {
			return parsed
		}
	}
	return ollamaCloudDefaultMaxTokensCap
}

func clampOllamaCloudMaxTokens(account *Account, body []byte) []byte {
	cap := ollamaCloudMaxTokensCap(account)
	if cap <= 0 || !gjson.ValidBytes(body) {
		return body
	}
	out := body
	clamped := false
	for _, key := range []string{"max_tokens", "max_completion_tokens"} {
		result := gjson.GetBytes(out, key)
		if !result.Exists() || result.Type != gjson.Number || result.Int() <= cap {
			continue
		}
		updated, err := sjson.SetBytes(out, key, cap)
		if err != nil {
			return body
		}
		out = updated
		clamped = true
	}
	if clamped && account != nil {
		logger.L().Debug("openai chat_completions raw: clamped max_tokens for ollama cloud account",
			zap.Int64("account_id", account.ID), zap.Int64("cap", cap))
	}
	return out
}
