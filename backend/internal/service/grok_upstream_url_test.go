//go:build unit

package service

import (
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/xai"
	"github.com/stretchr/testify/require"
)

func TestGrokOAuthURLPolicy(t *testing.T) {
	restrictive := &config.Config{}
	restrictive.Security.URLAllowlist.Enabled = true
	restrictive.Security.URLAllowlist.UpstreamHosts = []string{"allowed-relay.example.test"}

	tests := []struct {
		name    string
		baseURL string
		want    string
		wantErr bool
	}{
		{name: "default CLI is trusted", want: xai.DefaultCLIBaseURL + "/responses"},
		{name: "official API is trusted", baseURL: xai.DefaultBaseURL, want: xai.DefaultBaseURL + "/responses"},
		{name: "regional API is trusted", baseURL: "https://us-west-2.api.x.ai/v1", want: "https://us-west-2.api.x.ai/v1/responses"},
		{name: "allowlisted relay preserves prefix", baseURL: "https://allowed-relay.example.test/xai/v1", want: "https://allowed-relay.example.test/xai/v1/responses"},
		{name: "relay rejected by allowlist", baseURL: "https://rejected-relay.example.test/v1", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			account := &Account{Platform: PlatformGrok, Type: AccountTypeOAuth, Credentials: map[string]any{
				"base_url":                     tt.baseURL,
				"grok_custom_base_url_enabled": true,
			}}
			got, err := buildGrokResponsesURL(account, restrictive)
			if tt.wantErr {
				require.EqualError(t, err, "invalid base url: base URL rejected by URL security policy")
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestGrokAPIKeyOfficialURLBypassesLegacyOperatorAllowlist(t *testing.T) {
	restrictive := &config.Config{}
	restrictive.Security.URLAllowlist.Enabled = true
	restrictive.Security.URLAllowlist.UpstreamHosts = []string{"api.openai.com"}

	account := &Account{Platform: PlatformGrok, Type: AccountTypeAPIKey, Credentials: map[string]any{}}
	got, err := buildGrokResponsesURL(account, restrictive)
	require.NoError(t, err)
	require.Equal(t, xai.DefaultBaseURL+"/responses", got)

	account.Credentials["base_url"] = "https://unapproved-relay.example.test/v1"
	_, err = buildGrokResponsesURL(account, restrictive)
	require.EqualError(t, err, "invalid base url: base URL rejected by URL security policy")
}

func TestGrokEndpointBuildersShareAccountPolicy(t *testing.T) {
	cfg := &config.Config{}
	cfg.Security.URLAllowlist.Enabled = false
	cfg.Security.URLAllowlist.AllowInsecureHTTP = true
	account := &Account{Platform: PlatformGrok, Type: AccountTypeAPIKey, Credentials: map[string]any{"base_url": "http://relay.example.test/xai/v1"}}

	responses, err := buildGrokResponsesURL(account, cfg)
	require.NoError(t, err)
	require.Equal(t, "http://relay.example.test/xai/v1/responses", responses)
	chat, err := buildGrokChatCompletionsURL(account, cfg)
	require.NoError(t, err)
	require.Equal(t, "http://relay.example.test/xai/v1/chat/completions", chat)
	media, err := buildGrokMediaURL(account, cfg, GrokMediaEndpointImagesGenerations, "")
	require.NoError(t, err)
	require.Equal(t, "http://relay.example.test/xai/v1/images/generations", media)
}
