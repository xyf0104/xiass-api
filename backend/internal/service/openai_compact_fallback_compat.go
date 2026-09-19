package service

import "strings"

func (s *OpenAIGatewayService) resolveOpenAICompactFallbackModel(account *Account, requestedModel string) string {
	requestedModel = strings.TrimSpace(requestedModel)
	if account != nil {
		if mapped, matched := account.ResolveCompactMappedModel(requestedModel); matched {
			if mapped = strings.TrimSpace(mapped); mapped != "" {
				return mapped
			}
		}
	}
	if s == nil || s.cfg == nil {
		return ""
	}
	fallback := strings.TrimSpace(s.cfg.Gateway.OpenAICompactModel)
	if fallback == "" {
		return ""
	}
	return strings.TrimSpace(resolveOpenAIAccountUpstreamModelForRequest(account, fallback, false))
}
