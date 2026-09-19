package service

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/tidwall/gjson"
)

const (
	openCodeSessionHeader         = openCodeNativeSessionHeader
	openCodeInboundBodyContextKey = "opencode_inbound_body"
)

func rememberOpenCodeInboundBody(c *gin.Context, body []byte) {
	if c == nil || len(body) == 0 {
		return
	}
	c.Set(openCodeInboundBodyContextKey, body)
}

func openCodeInboundBodies(c *gin.Context) [][]byte {
	if c == nil {
		return nil
	}
	raw, ok := c.Get(openCodeInboundBodyContextKey)
	if !ok {
		return nil
	}
	body, ok := raw.([]byte)
	if !ok || len(body) == 0 {
		return nil
	}
	return [][]byte{body}
}

// applyOpenCodeSessionHeader forwards a stable conversation identifier only to
// OpenCode's official origin or an explicit OpenCode Go account. Caller-owned
// headers win, followed by documented body fields, account overrides, and a
// generated value only where the Go endpoint requires one.
func applyOpenCodeSessionHeader(c *gin.Context, account *Account, targetURL string, headers http.Header, bodies ...[]byte) {
	if account == nil || account.Type != AccountTypeAPIKey || headers == nil {
		return
	}
	if !shouldSendOpenCodeSessionHeader(account, targetURL) {
		return
	}

	payloads := append(openCodeInboundBodies(c), bodies...)
	sessionID := resolveOpenCodeSessionID(c, headers, shouldGenerateOpenCodeSession(account, targetURL), payloads...)
	if sessionID == "" {
		return
	}
	for key := range headers {
		if strings.EqualFold(key, openCodeSessionHeader) {
			delete(headers, key)
		}
	}
	headers.Set(openCodeSessionHeader, sessionID)
}

func shouldSendOpenCodeSessionHeader(account *Account, targetURL string) bool {
	return account != nil && account.IsOpenCodeGoPlan() || isOfficialOpenCodeHost(targetURL)
}

func shouldGenerateOpenCodeSession(account *Account, targetURL string) bool {
	if account != nil && account.IsOpenCodeGoPlan() {
		return true
	}
	parsed, err := url.Parse(targetURL)
	return err == nil && strings.EqualFold(parsed.Scheme, "https") &&
		strings.EqualFold(parsed.Hostname(), "opencode.ai") && strings.Contains(parsed.Path, "/zen/go")
}

func isOfficialOpenCodeHost(targetURL string) bool {
	parsed, err := url.Parse(targetURL)
	return err == nil && strings.EqualFold(parsed.Scheme, "https") && strings.EqualFold(parsed.Hostname(), "opencode.ai")
}

func resolveOpenCodeSessionID(c *gin.Context, headers http.Header, generate bool, bodies ...[]byte) string {
	if c != nil && c.Request != nil {
		if sessionID := sanitizeSessionID(c.GetHeader(openCodeSessionHeader)); sessionID != "" {
			return sessionID
		}
		if sessionID := sanitizeSessionID(explicitOpenAIHeaderSessionID(c)); sessionID != "" {
			return sessionID
		}
		if sessionID := sanitizeSessionID(ClaudeCodeSessionIDFromHeader(c)); sessionID != "" {
			return sessionID
		}
	}
	for _, body := range bodies {
		if sessionID := sanitizeSessionID(openCodeSessionIDFromPayload(body)); sessionID != "" {
			return sessionID
		}
	}
	if sessionID := sanitizeSessionID(existingOpenCodeSessionHeader(headers)); sessionID != "" {
		return sessionID
	}
	if generate {
		return uuid.NewString()
	}
	return ""
}

func openCodeSessionIDFromPayload(body []byte) string {
	if len(body) == 0 {
		return ""
	}
	if sessionID := strings.TrimSpace(gjson.GetBytes(body, "prompt_cache_key").String()); sessionID != "" {
		return sessionID
	}
	return openCodeSessionIDFromMetadataUserID(gjson.GetBytes(body, "metadata.user_id").String())
}

func openCodeSessionIDFromMetadataUserID(userID string) string {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return ""
	}
	if strings.HasPrefix(userID, "{") {
		if sessionID := strings.TrimSpace(gjson.Get(userID, "session_id").String()); sessionID != "" {
			return sessionID
		}
	}
	return userID
}

func openCodeSessionHintBody(promptCacheKey string) []byte {
	key := strings.TrimSpace(promptCacheKey)
	if key == "" {
		return nil
	}
	return []byte(`{"prompt_cache_key":` + strconv.Quote(key) + `}`)
}

func existingOpenCodeSessionHeader(headers http.Header) string {
	for key, values := range headers {
		if strings.EqualFold(key, openCodeSessionHeader) && len(values) > 0 {
			return values[0]
		}
	}
	return ""
}
