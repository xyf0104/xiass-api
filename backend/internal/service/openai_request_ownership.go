package service

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/tidwall/gjson"
)

const openAIRequestOwnershipContextKey = "openai_request_ownership"

// Ownership is internal only. It must never be reconstructed from fingerprint
// headers or emitted as a replacement client identity.
type openAIRequestOwnership struct {
	client       string
	conversation string
}

type openAIWSOwnership struct {
	openAIRequestOwnership
	upstream string
}

func openAIOwnershipDigest(parts ...string) string {
	encoded, _ := json.Marshal(parts)
	return fmt.Sprintf("%x", sha256.Sum256(encoded))
}

// Capture once, before body transformations, and retain the original scope on
// account failover. Missing authentication/conversation signals are request-local.
func captureOpenAIRequestOwnership(c *gin.Context, body []byte) openAIRequestOwnership {
	if c != nil {
		if value, ok := c.Get(openAIRequestOwnershipContextKey); ok {
			if owner, ok := value.(openAIRequestOwnership); ok {
				return owner
			}
		}
	}
	owner := openAIRequestOwnership{}
	if c != nil {
		if value, ok := c.Get("api_key"); ok {
			if key, ok := value.(*APIKey); ok && key != nil && key.ID > 0 {
				owner.client = openAIOwnershipDigest("client-v1", fmt.Sprint(key.ID), fmt.Sprint(key.UserID), fmt.Sprint(getOpenAIGroupIDFromContext(c)))
			}
		}
	}
	if owner.client == "" {
		owner.client = "request:" + uuid.NewString()
	}
	var signals []string
	if c != nil && c.Request != nil {
		signals = append(signals, c.GetHeader("thread-id"), c.GetHeader("conversation_id"))
	}
	for _, path := range []string{"client_metadata.thread_id", "client_metadata.session_id"} {
		if value := gjson.GetBytes(body, path); value.Type == gjson.String {
			signals = append(signals, value.String())
		}
	}
	if c != nil && c.Request != nil {
		signals = append(signals, extractClientSessionID(c.Request.Header))
	}
	if value := gjson.GetBytes(body, "prompt_cache_key"); value.Type == gjson.String {
		signals = append(signals, value.String())
	}
	for _, signal := range signals {
		if signal = strings.TrimSpace(signal); signal != "" {
			owner.conversation = openAIOwnershipDigest("conversation-v1", signal)
			break
		}
	}
	if owner.conversation == "" {
		owner.conversation = "request:" + uuid.NewString()
	}
	if c != nil {
		c.Set(openAIRequestOwnershipContextKey, owner)
	}
	return owner
}

func openAIAccountOwnership(account *Account) string {
	if account == nil {
		return ""
	}
	parts := []string{"upstream-v1", fmt.Sprint(account.ID), account.Platform, account.Type,
		account.GetCredential("chatgpt_account_id"), account.GetCredential("chatgpt_user_id"),
		account.GetCredential("organization_id"), account.GetCredential("task_id"),
		account.GetOpenAIBaseURL()}
	// Ordinary OAuth access-token rotation does not change a known principal or
	// break its pinned response chains. Without a principal, isolate credentials.
	if account.Type != AccountTypeOAuth || (parts[4] == "" && parts[5] == "") {
		parts = append(parts, account.GetCredential("api_key"), account.GetCredential("access_token"))
	}
	// Keep the durable default proxy in the ownership boundary for legacy
	// accounts whose snapshot carries only ProxyID. A multi-proxy request can
	// additionally bind the actual request-selected exit below.
	if account.ProxyID != nil {
		parts = append(parts, "default-proxy", fmt.Sprint(*account.ProxyID))
	}
	if proxy := account.requestProxy(); proxy != nil {
		parts = append(parts, "request-proxy", fmt.Sprint(proxy.ID), proxy.URL())
	}
	return openAIOwnershipDigest(parts...)
}

func openAIWSOwnershipForRequest(c *gin.Context, account *Account) openAIWSOwnership {
	return openAIWSOwnership{captureOpenAIRequestOwnership(c, nil), openAIAccountOwnership(account)}
}

func (o openAIWSOwnership) sessionKey() string {
	return "owned-session-v1:" + openAIOwnershipDigest(o.client, o.conversation, o.upstream)
}

func (o openAIWSOwnership) responseKey(responseID string) string {
	if strings.TrimSpace(responseID) == "" {
		return ""
	}
	// A response chain may outlive ordinary session affinity or arrive without
	// session headers. Only an explicit response ID can bridge conversations.
	return "owned-response-v1:" + openAIOwnershipDigest(o.client, o.upstream, strings.TrimSpace(responseID))
}
