package main

import (
	"context"
	"testing"

	"github.com/chromedp/cdproto/target"
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
		{name: "workspace", page: oauthPageSnapshot{URL: "https://auth.openai.com/authorize", Body: "Continue to Codex using your default workspace"}, kind: "workspace"},
		{name: "deleted", page: oauthPageSnapshot{URL: "https://auth.openai.com/log-in", Body: "This account has been deleted or disabled"}, kind: "account_deleted_or_disabled"},
		{name: "callback", page: oauthPageSnapshot{URL: "http://localhost:1455/auth/callback?code=abc&state=state"}, kind: "callback"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			state := inspectOAuthPage(test.page)
			require.Equal(t, test.kind, state.Kind)
		})
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

func TestMatchingOAuthTargetPrefersExactStateAndSkipsErrorPages(t *testing.T) {
	targets := []*target.Info{
		{TargetID: "error", Type: "page", URL: "https://chatgpt.com/api/auth/error"},
		{TargetID: "stale", Type: "page", URL: "https://auth.openai.com/oauth/authorize?state=stale"},
		{TargetID: "expected", Type: "page", URL: "https://auth.openai.com/oauth/authorize?client_id=codex&state=expected"},
	}
	matched := matchingOAuthTarget(targets, "https://auth.openai.com/oauth/authorize?state=expected&client_id=codex")
	require.NotNil(t, matched)
	require.Equal(t, target.ID("expected"), matched.TargetID)
}

func TestMatchingOAuthTargetFallsBackToRedirectedLoginPage(t *testing.T) {
	targets := []*target.Info{
		{TargetID: "blank", Type: "page", URL: "about:blank"},
		{TargetID: "stale", Type: "page", URL: "https://auth.openai.com/oauth/authorize?state=stale"},
		{TargetID: "login", Type: "page", URL: "https://auth.openai.com/log-in/password"},
	}
	matched := matchingOAuthTarget(targets, "https://auth.openai.com/oauth/authorize?state=expected")
	require.NotNil(t, matched)
	require.Equal(t, target.ID("login"), matched.TargetID)
}

func TestAutomationErrorReasonTreatsOpeningDeadlineAsNavigationTimeout(t *testing.T) {
	require.Equal(t, "navigation_timeout", automationErrorReason(context.DeadlineExceeded, "opening"))
	require.Equal(t, "task_expired", automationErrorReason(context.DeadlineExceeded, "password"))
}
