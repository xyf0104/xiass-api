package service

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestClaudeOAuthCompatibilityPreservesSignedModelOptions(t *testing.T) {
	for _, model := range []string{"claude-opus-5-5", "claude-sonnet-5-5", "claude-haiku-5-5", "claude-fable-5-2", "claude-mythos-5-2"} {
		t.Run(model, func(t *testing.T) {
			body := []byte(`{"top_p":0.99,"tool_choice":{"type":"none"},"output_config":{"effort":"xhigh"},"messages":[{"role":"user","content":"hi"}]}`)
			out, _ := normalizeClaudeOAuthRequestBody(body, model, claudeOAuthNormalizeOptions{})
			require.False(t, gjson.GetBytes(out, "temperature").Exists())
			require.Equal(t, "none", gjson.GetBytes(out, "tool_choice.type").String())
			require.Equal(t, "xhigh", gjson.GetBytes(out, "output_config.effort").String())
		})
	}
}

func TestClaudeOAuthCacheFastPathEscapedKeysAndTTL(t *testing.T) {
	for _, body := range []string{
		`{"system":[{"type":"text","text":"a","cache_control":{"type":"ephemeral","ttl":"5m"}},{"type":"text","text":"b","cache_control":{"type":"ephemeral","ttl":"1h"}}]}`,
		`{"system":[{"type":"text","text":"a","cache_\u0063ontrol":{"type":"ephemeral","ttl":"5m"}},{"type":"text","text":"b","cache_control":{"type":"ephemeral","ttl":"\u0031h"}}]}`,
	} {
		out := enforceCacheControlLimit([]byte(body))
		require.Equal(t, "1h", gjson.GetBytes(out, "system.0.cache_control.ttl").String())
		require.Equal(t, "1h", gjson.GetBytes(out, "system.1.cache_control.ttl").String())
		require.Equal(t, string(out), string(enforceCacheControlLimit(out)))
	}
}
