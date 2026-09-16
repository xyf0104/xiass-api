package main

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestInspectOAuthPageRecognizesLoginStages(t *testing.T) {
	tests := []struct {
		name string
		page oauthPageSnapshot
		kind string
	}{
		{name: "email", page: oauthPageSnapshot{URL: "https://auth.openai.com/log-in", Inputs: []oauthPageInput{{Index: 0, Metadata: "email username"}}}, kind: "email"},
		{name: "password", page: oauthPageSnapshot{URL: "https://auth.openai.com/log-in/password", Body: "owner@example.test", Inputs: []oauthPageInput{{Index: 0, Metadata: "password current-password"}}}, kind: "password"},
		{name: "totp", page: oauthPageSnapshot{URL: "https://auth.openai.com/mfa", Body: "Enter the code from your authenticator app", Inputs: []oauthPageInput{{Index: 0, Metadata: "one-time-code numeric"}}}, kind: "totp"},
		{name: "email code", page: oauthPageSnapshot{URL: "https://auth.openai.com/email-verification", Body: "Check your inbox for a verification code", Inputs: []oauthPageInput{{Index: 0, Metadata: "one-time-code numeric"}}}, kind: "email_code"},
		{name: "account choice", page: oauthPageSnapshot{URL: "https://auth.openai.com/choose-an-account", Body: "Choose an account to continue to Codex owner@example.test", Actions: []oauthPageAction{{Index: 4, Text: "Select account Owner owner@example.test"}}}, kind: "account_choice"},
		{name: "retry consent page", page: oauthPageSnapshot{URL: "https://auth.openai.com/sign-in-with-chatgpt/codex/consent", Body: "Oops, an error occurred! Unexpected token '<' is not valid JSON", Actions: []oauthPageAction{{Index: 3, Text: "Try again"}}}, kind: "retry_page"},
		{name: "workspace", page: oauthPageSnapshot{URL: "https://auth.openai.com/authorize", Body: "Continue to Codex using your default workspace"}, kind: "workspace"},
		{name: "deleted", page: oauthPageSnapshot{URL: "https://auth.openai.com/log-in", Body: "This account has been deleted or disabled"}, kind: "account_deleted_or_disabled"},
		{name: "banned", page: oauthPageSnapshot{URL: "https://auth.openai.com/log-in", Body: "This account has been suspended"}, kind: "account_banned"},
		{name: "restricted", page: oauthPageSnapshot{URL: "https://auth.openai.com/log-in", Body: "This account is restricted"}, kind: "unknown_error"},
		{name: "proxy failure", page: oauthPageSnapshot{URL: "chrome-error://chromewebdata/", Body: "ERR_PROXY_CONNECTION_FAILED"}, kind: "proxy_unavailable"},
		{name: "callback", page: oauthPageSnapshot{URL: "http://localhost:1455/auth/callback?code=abc&state=state"}, kind: "callback"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			state := inspectOAuthPage(test.page)
			require.Equal(t, test.kind, state.Kind)
		})
	}
}

func TestBlockedOnlyMakesDeletedOrBannedAccountsTerminal(t *testing.T) {
	for _, reason := range []string{"account_banned", "account_deleted_or_disabled"} {
		result := automationFailure(blocked(reason))
		require.Equal(t, "blocked", result.Status)
	}
	for _, reason := range []string{"manual_challenge", "unknown_error", "proxy_unavailable", "captcha_required"} {
		result := automationFailure(blocked(reason))
		require.Equal(t, "failed", result.Status)
	}
}

func TestInspectOAuthPageRejectsDifferentVisibleAccount(t *testing.T) {
	state := inspectOAuthPage(oauthPageSnapshot{
		URL:  "https://auth.openai.com/authorize",
		Body: "Continue with other@example.test to your workspace",
	})
	require.Equal(t, "workspace", state.Kind)
	require.Equal(t, "other@example.test", state.VisibleMail)
}

func TestExtractEmailCodeRequiresOpenAIContext(t *testing.T) {
	require.Equal(t, "543604", extractEmailCode(emailCodeMessage{
		From: "OpenAI", Subject: "Your verification code is 543604",
	}))
	require.Empty(t, extractEmailCode(emailCodeMessage{
		From: "Unrelated", Subject: "Your verification code is 543604",
	}))
}

func TestValidCallbackChecksStateAndCode(t *testing.T) {
	require.True(t, validCallback("http://localhost:1455/auth/callback?code=abc&state=state", "state"))
	require.False(t, validCallback("http://localhost:1455/auth/callback?code=abc&state=other", "state"))
	require.False(t, validCallback("https://example.com/auth/callback?code=abc&state=state", "state"))
}

func TestAutomationErrorReasonTreatsOpeningDeadlineAsNavigationTimeout(t *testing.T) {
	require.Equal(t, "navigation_timeout", automationErrorReason(context.DeadlineExceeded, "opening"))
	require.Equal(t, "task_expired", automationErrorReason(context.DeadlineExceeded, "password"))
}

func TestShouldResumeOAuthFromChatGPTHomepage(t *testing.T) {
	require.True(t, shouldResumeOAuthFromChatGPTHomepage("https://chatgpt.com/", true, false))
	require.True(t, shouldResumeOAuthFromChatGPTHomepage("https://www.chatgpt.com", true, false))
	require.False(t, shouldResumeOAuthFromChatGPTHomepage("https://chatgpt.com/", false, false))
	require.False(t, shouldResumeOAuthFromChatGPTHomepage("https://chatgpt.com/", true, true))
	require.False(t, shouldResumeOAuthFromChatGPTHomepage("https://chatgpt.com/api/auth/error", true, false))
	require.False(t, shouldResumeOAuthFromChatGPTHomepage("https://auth.openai.com/", true, false))
}

func TestAccountSwitchActionRecognizesAlternateAccountControls(t *testing.T) {
	action, ok := accountSwitchAction(oauthPageSnapshot{Actions: []oauthPageAction{
		{Index: 2, Text: "Continue"},
		{Index: 7, Text: "Log in to another account"},
	}})
	require.True(t, ok)
	require.Equal(t, 7, action)

	_, ok = accountSwitchAction(oauthPageSnapshot{Actions: []oauthPageAction{{Index: 1, Text: "Continue"}}})
	require.False(t, ok)
}

func TestOAuthSnapshotDiagnosticRedactsCredentials(t *testing.T) {
	diagnostic := summarizeOAuthSnapshot(oauthPageSnapshot{
		URL:     "https://auth.openai.com/oauth/authorize?state=secret-state-value-1234567890",
		Body:    "Continue as owner@example.test with code 123456 and token abcdefghijklmnopqrstuvwxyz123456",
		Inputs:  []oauthPageInput{{Metadata: "email owner@example.test"}},
		Actions: []oauthPageAction{{Text: "Use code 654321"}},
	})
	require.NotContains(t, diagnostic, "owner@example.test")
	require.NotContains(t, diagnostic, "123456")
	require.NotContains(t, diagnostic, "654321")
	require.NotContains(t, diagnostic, "abcdefghijklmnopqrstuvwxyz123456")
	require.False(t, strings.Contains(diagnostic, "secret-state-value"))
	require.Contains(t, diagnostic, "[email]")
	require.Contains(t, diagnostic, "[code]")
	require.Contains(t, diagnostic, "[token]")
}
