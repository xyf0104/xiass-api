//go:build unit

package service

import (
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/xai"
	"github.com/stretchr/testify/require"
)

func TestGetBaseURL(t *testing.T) {
	tests := []struct {
		name     string
		account  Account
		expected string
	}{
		{
			name: "non-apikey type returns empty",
			account: Account{
				Type:     AccountTypeOAuth,
				Platform: PlatformAnthropic,
			},
			expected: "",
		},
		{
			name: "apikey without base_url returns default anthropic",
			account: Account{
				Type:        AccountTypeAPIKey,
				Platform:    PlatformAnthropic,
				Credentials: map[string]any{},
			},
			expected: "https://api.anthropic.com",
		},
		{
			name: "apikey with custom base_url",
			account: Account{
				Type:        AccountTypeAPIKey,
				Platform:    PlatformAnthropic,
				Credentials: map[string]any{"base_url": "https://custom.example.com"},
			},
			expected: "https://custom.example.com",
		},
		{
			name: "antigravity apikey auto-appends /antigravity",
			account: Account{
				Type:        AccountTypeAPIKey,
				Platform:    PlatformAntigravity,
				Credentials: map[string]any{"base_url": "https://upstream.example.com"},
			},
			expected: "https://upstream.example.com/antigravity",
		},
		{
			name: "antigravity apikey trims trailing slash before appending",
			account: Account{
				Type:        AccountTypeAPIKey,
				Platform:    PlatformAntigravity,
				Credentials: map[string]any{"base_url": "https://upstream.example.com/"},
			},
			expected: "https://upstream.example.com/antigravity",
		},
		{
			name: "antigravity non-apikey returns empty",
			account: Account{
				Type:        AccountTypeOAuth,
				Platform:    PlatformAntigravity,
				Credentials: map[string]any{"base_url": "https://upstream.example.com"},
			},
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.account.GetBaseURL()
			if result != tt.expected {
				t.Errorf("GetBaseURL() = %q, want %q", result, tt.expected)
			}
		})
	}
}

func TestGetGeminiBaseURL(t *testing.T) {
	const defaultGeminiURL = "https://generativelanguage.googleapis.com"

	tests := []struct {
		name     string
		account  Account
		expected string
	}{
		{
			name: "apikey without base_url returns default",
			account: Account{
				Type:        AccountTypeAPIKey,
				Platform:    PlatformGemini,
				Credentials: map[string]any{},
			},
			expected: defaultGeminiURL,
		},
		{
			name: "apikey with custom base_url",
			account: Account{
				Type:        AccountTypeAPIKey,
				Platform:    PlatformGemini,
				Credentials: map[string]any{"base_url": "https://custom-gemini.example.com"},
			},
			expected: "https://custom-gemini.example.com",
		},
		{
			name: "antigravity apikey auto-appends /antigravity",
			account: Account{
				Type:        AccountTypeAPIKey,
				Platform:    PlatformAntigravity,
				Credentials: map[string]any{"base_url": "https://upstream.example.com"},
			},
			expected: "https://upstream.example.com/antigravity",
		},
		{
			name: "antigravity apikey trims trailing slash",
			account: Account{
				Type:        AccountTypeAPIKey,
				Platform:    PlatformAntigravity,
				Credentials: map[string]any{"base_url": "https://upstream.example.com/"},
			},
			expected: "https://upstream.example.com/antigravity",
		},
		{
			name: "antigravity oauth does NOT append /antigravity",
			account: Account{
				Type:        AccountTypeOAuth,
				Platform:    PlatformAntigravity,
				Credentials: map[string]any{"base_url": "https://upstream.example.com"},
			},
			expected: "https://upstream.example.com",
		},
		{
			name: "oauth without base_url returns default",
			account: Account{
				Type:        AccountTypeOAuth,
				Platform:    PlatformAntigravity,
				Credentials: map[string]any{},
			},
			expected: defaultGeminiURL,
		},
		{
			name: "nil credentials returns default",
			account: Account{
				Type:     AccountTypeAPIKey,
				Platform: PlatformGemini,
			},
			expected: defaultGeminiURL,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.account.GetGeminiBaseURL(defaultGeminiURL)
			if result != tt.expected {
				t.Errorf("GetGeminiBaseURL() = %q, want %q", result, tt.expected)
			}
		})
	}
}

func TestGetGrokBaseURLRequiresExplicitOAuthEndpointOptIn(t *testing.T) {
	tests := []struct {
		name     string
		account  Account
		expected string
	}{
		{
			name: "oauth without base_url uses CLI subscription proxy",
			account: Account{
				Type:        AccountTypeOAuth,
				Platform:    PlatformGrok,
				Credentials: map[string]any{},
			},
			expected: xai.DefaultCLIBaseURL,
		},
		{
			name: "legacy oauth base_url remains on CLI without opt-in",
			account: Account{
				Type:     AccountTypeOAuth,
				Platform: PlatformGrok,
				Credentials: map[string]any{
					"base_url": xai.DefaultBaseURL,
				},
			},
			expected: xai.DefaultCLIBaseURL,
		},
		{
			name: "oauth official API is honored",
			account: Account{
				Type:     AccountTypeOAuth,
				Platform: PlatformGrok,
				Credentials: map[string]any{
					"base_url":                     xai.DefaultBaseURL,
					"grok_custom_base_url_enabled": true,
				},
			},
			expected: xai.DefaultBaseURL,
		},
		{
			name: "oauth regional API is honored",
			account: Account{
				Type:     AccountTypeOAuth,
				Platform: PlatformGrok,
				Credentials: map[string]any{
					"base_url":                     "https://us-west-2.api.x.ai/v1",
					"grok_custom_base_url_enabled": true,
				},
			},
			expected: "https://us-west-2.api.x.ai/v1",
		},
		{
			name: "oauth explicit CLI is honored",
			account: Account{
				Type:     AccountTypeOAuth,
				Platform: PlatformGrok,
				Credentials: map[string]any{
					"base_url":                     xai.DefaultCLIBaseURL,
					"grok_custom_base_url_enabled": true,
				},
			},
			expected: xai.DefaultCLIBaseURL,
		},
		{
			name: "oauth unparseable value falls back to CLI",
			account: Account{
				Type:     AccountTypeOAuth,
				Platform: PlatformGrok,
				Credentials: map[string]any{
					"base_url":                     "not a url",
					"grok_custom_base_url_enabled": true,
				},
			},
			expected: xai.DefaultCLIBaseURL,
		},
		{
			name: "oauth custom relay is honored",
			account: Account{
				Type:     AccountTypeOAuth,
				Platform: PlatformGrok,
				Credentials: map[string]any{
					"base_url":                     "https://relay.example.com/xai/v1",
					"grok_custom_base_url_enabled": true,
				},
			},
			expected: "https://relay.example.com/xai/v1",
		},
		{
			name: "API key without base_url uses official credit-backed API",
			account: Account{
				Type:        AccountTypeAPIKey,
				Platform:    PlatformGrok,
				Credentials: map[string]any{},
			},
			expected: xai.DefaultBaseURL,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.expected, tt.account.GetGrokBaseURL())
		})
	}
}

func TestGetGrokMediaBaseURLRoutesOnlyCLIToOfficialAPI(t *testing.T) {
	tests := []struct {
		name     string
		baseURL  string
		enabled  bool
		expected string
	}{
		{name: "default CLI", expected: xai.DefaultBaseURL},
		{name: "explicit CLI", baseURL: xai.DefaultCLIBaseURL, enabled: true, expected: xai.DefaultBaseURL},
		{name: "official API", baseURL: xai.DefaultBaseURL, enabled: true, expected: xai.DefaultBaseURL},
		{name: "regional API", baseURL: "https://eu-west-1.api.x.ai/v1", enabled: true, expected: "https://eu-west-1.api.x.ai/v1"},
		{name: "custom relay", baseURL: "https://relay.example.com/xai/v1", enabled: true, expected: "https://relay.example.com/xai/v1"},
		{name: "invalid falls back through CLI", baseURL: "not a url", enabled: true, expected: xai.DefaultBaseURL},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			account := Account{Type: AccountTypeOAuth, Platform: PlatformGrok, Credentials: map[string]any{
				"base_url":                     tt.baseURL,
				"grok_custom_base_url_enabled": tt.enabled,
			}}
			require.Equal(t, tt.expected, account.GetGrokMediaBaseURL())
		})
	}
}

func TestGetGrokMediaBaseURLRedirectsCLIGatewayToOfficialAPI(t *testing.T) {
	tests := []struct {
		name     string
		account  Account
		expected string
	}{
		{
			name: "oauth without base_url uses official media API",
			account: Account{
				Type:        AccountTypeOAuth,
				Platform:    PlatformGrok,
				Credentials: map[string]any{},
			},
			expected: xai.DefaultBaseURL,
		},
		{
			name: "oauth stored CLI proxy is separated from the media API",
			account: Account{
				Type:     AccountTypeOAuth,
				Platform: PlatformGrok,
				Credentials: map[string]any{
					"base_url": xai.DefaultCLIBaseURL,
				},
			},
			expected: xai.DefaultBaseURL,
		},
		{
			name: "oauth stored CLI proxy variant is canonicalized to the media API",
			account: Account{
				Type:     AccountTypeOAuth,
				Platform: PlatformGrok,
				Credentials: map[string]any{
					"base_url": "HTTPS://CLI-CHAT-PROXY.GROK.COM:443/%76%31/",
				},
			},
			expected: xai.DefaultBaseURL,
		},
		{
			name: "oauth unparseable base_url falls back to official media API",
			account: Account{
				Type:     AccountTypeOAuth,
				Platform: PlatformGrok,
				Credentials: map[string]any{
					"base_url": "not a url",
				},
			},
			expected: xai.DefaultBaseURL,
		},
		{
			name: "oauth stored official API endpoint is honored (manual endpoint switch)",
			account: Account{
				Type:     AccountTypeOAuth,
				Platform: PlatformGrok,
				Credentials: map[string]any{
					"base_url":                     xai.DefaultBaseURL,
					"grok_custom_base_url_enabled": true,
				},
			},
			expected: xai.DefaultBaseURL,
		},
		{
			name: "oauth stored regional API endpoint is honored for media",
			account: Account{
				Type:     AccountTypeOAuth,
				Platform: PlatformGrok,
				Credentials: map[string]any{
					"base_url":                     "https://us-west-2.api.x.ai/v1",
					"grok_custom_base_url_enabled": true,
				},
			},
			expected: "https://us-west-2.api.x.ai/v1",
		},
		{
			name: "oauth custom base_url redirects media traffic",
			account: Account{
				Type:     AccountTypeOAuth,
				Platform: PlatformGrok,
				Credentials: map[string]any{
					"base_url":                     "https://custom.example.com/v1",
					"grok_custom_base_url_enabled": true,
				},
			},
			expected: "https://custom.example.com/v1",
		},
		{
			name: "API key retains its configured media API",
			account: Account{
				Type:     AccountTypeAPIKey,
				Platform: PlatformGrok,
				Credentials: map[string]any{
					"base_url": "https://grok.example.com/v1",
				},
			},
			expected: "https://grok.example.com/v1",
		},
		{
			name: "non-Grok account has no Grok media base URL",
			account: Account{
				Type:        AccountTypeOAuth,
				Platform:    PlatformOpenAI,
				Credentials: map[string]any{},
			},
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.expected, tt.account.GetGrokMediaBaseURL())
		})
	}
}

func TestGetGrokMediaBaseURLHonorsOAuthCustomRegardlessOfUnsafeOverrides(t *testing.T) {
	t.Setenv(xai.EnvAllowUnsafeURLOverrides, "true")
	account := Account{
		Type:     AccountTypeOAuth,
		Platform: PlatformGrok,
		Credentials: map[string]any{
			"base_url":                     "https://custom.example.com/v1",
			"grok_custom_base_url_enabled": true,
		},
	}

	require.Equal(t, "https://custom.example.com/v1", account.GetGrokMediaBaseURL())
}
