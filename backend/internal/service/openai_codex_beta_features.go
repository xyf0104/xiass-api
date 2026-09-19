package service

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

const openAIRemoteCompactionV2Feature = "remote_compaction_v2"

const openAINativeCompactionV2Key = "openai_native_compaction_v2"

// MarkOpenAINativeCompactionV2 records the handler's definitive body-level
// classification so downstream request builders can restore the negotiation
// header even when an intermediate gateway removed it.
func MarkOpenAINativeCompactionV2(c *gin.Context) {
	if c != nil {
		c.Set(openAINativeCompactionV2Key, true)
	}
}

func IsOpenAINativeCompactionV2(c *gin.Context) bool {
	return c != nil && c.GetBool(openAINativeCompactionV2Key)
}

func ensureOpenAIRemoteCompactionV2BetaFeature(h http.Header) {
	if h == nil {
		return
	}
	tokens := make([]string, 0, 4)
	for _, value := range h.Values("x-codex-beta-features") {
		for _, token := range strings.Split(value, ",") {
			token = strings.TrimSpace(token)
			if token == "" {
				continue
			}
			if token == openAIRemoteCompactionV2Feature {
				return
			}
			tokens = append(tokens, token)
		}
	}
	tokens = append(tokens, openAIRemoteCompactionV2Feature)
	h.Set("x-codex-beta-features", strings.Join(tokens, ","))
}

func hasOpenAICodexBetaFeaturesHeader(h http.Header) bool {
	if h == nil {
		return false
	}
	for _, value := range h.Values("x-codex-beta-features") {
		if strings.TrimSpace(value) != "" {
			return true
		}
	}
	return false
}

// applyOpenAICodexBetaFeatures keeps the capability declaration session-wide.
// OAuth requests default to the current Codex shape unless the client supplied
// a non-empty declaration. A body-level compaction trigger is definitive and
// always ensures remote_compaction_v2 is advertised.
func applyOpenAICodexBetaFeatures(c *gin.Context, account *Account, h http.Header, body ...[]byte) {
	if h == nil {
		return
	}
	nativeCompaction := IsOpenAINativeCompactionV2(c)
	for _, candidate := range body {
		if HasCompactionTriggerInInput(candidate) || gjson.GetBytes(candidate, "compaction_trigger").Exists() {
			nativeCompaction = true
			break
		}
	}
	if nativeCompaction {
		ensureOpenAIRemoteCompactionV2BetaFeature(h)
		return
	}
	if account == nil || !account.IsOpenAIOAuth() || hasOpenAICodexBetaFeaturesHeader(h) {
		return
	}
	h.Set("x-codex-beta-features", openAIRemoteCompactionV2Feature)
}

// stripOpenAILegacyResponsesBeta removes only the retired experiment token and
// preserves independent beta declarations, including multiple header lines.
func stripOpenAILegacyResponsesBeta(h http.Header) {
	if h == nil {
		return
	}
	kept := make([]string, 0, len(h.Values("OpenAI-Beta")))
	for key, values := range h {
		if !strings.EqualFold(strings.TrimSpace(key), "OpenAI-Beta") {
			continue
		}
		delete(h, key)
		for _, value := range values {
			for _, token := range strings.Split(value, ",") {
				token = strings.TrimSpace(token)
				if token == "" || strings.EqualFold(token, "responses=experimental") {
					continue
				}
				kept = append(kept, token)
			}
		}
	}
	for _, token := range kept {
		h.Add("OpenAI-Beta", token)
	}
}
