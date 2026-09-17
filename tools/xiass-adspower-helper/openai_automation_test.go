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
		{name: "phone", page: oauthPageSnapshot{URL: "https://auth.openai.com/add-phone", Body: "Phone number required", Inputs: []oauthPageInput{{Index: 0, Metadata: "tel phone mobile"}}}, kind: "phone"},
		{name: "phone rejected", page: oauthPageSnapshot{URL: "https://auth.openai.com/add-phone", Body: "This phone number is unavailable or has been used too many times", Inputs: []oauthPageInput{{Index: 0, Metadata: "tel phone mobile"}}}, kind: "phone_rejected"},
		{name: "sms code", page: oauthPageSnapshot{URL: "https://auth.openai.com/verify-phone", Body: "Enter the text message code sent to your phone", Inputs: []oauthPageInput{{Index: 0, Metadata: "one-time-code numeric"}}}, kind: "sms_code"},
		{name: "profile", page: oauthPageSnapshot{URL: "https://auth.openai.com/about-you", Body: "Tell us about yourself Name Age", Inputs: []oauthPageInput{{Index: 0, Metadata: "name"}, {Index: 1, Metadata: "age numeric"}}}, kind: "profile"},
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

func TestOAuthPhoneInputMatchesRequiresCompleteNumber(t *testing.T) {
	require.False(t, oauthPhoneInputMatches("+1 26", "12605550123", "1"))
	require.False(t, oauthPhoneInputMatches("26", "12605550123", "1"))
	require.True(t, oauthPhoneInputMatches("(260) 555-0123", "12605550123", "1"))
	require.True(t, oauthPhoneInputMatches("+1 260 555 0123", "12605550123", "1"))
	require.True(t, oauthPhoneInputMatches("532 357 21 27", "905323572127", "90"))
	require.True(t, oauthPhoneInputMatches("+90 532 357 21 27", "905323572127", "90"))
	require.False(t, oauthPhoneInputMatches("532 357 21 27", "905323572127", "1"))
}

func TestInspectOAuthPageDoesNotTreatSMSRadioAsVerificationCode(t *testing.T) {
	state := inspectOAuthPage(oauthPageSnapshot{
		URL:  "https://auth.openai.com/add-phone",
		Body: "Phone number required Text message WhatsApp",
		Inputs: []oauthPageInput{
			{Index: 0, Metadata: "tel phone"},
			{Index: 1, Metadata: "radio text-message code"},
			{Index: 2, Metadata: "radio whatsapp"},
		},
	})
	require.Equal(t, "phone", state.Kind)
	require.Equal(t, 0, state.Input)
}

func TestInspectOAuthPageDoesNotTreatNumericPhoneAsOTP(t *testing.T) {
	state := inspectOAuthPage(oauthPageSnapshot{
		URL:    "https://auth.openai.com/add-phone",
		Body:   "Phone number required",
		Inputs: []oauthPageInput{{Index: 0, Metadata: "tel phone inputmode numeric"}},
	})
	require.Equal(t, "phone", state.Kind)
	require.Equal(t, 0, state.Input)
}

func TestInspectOAuthPageRecognizesTeamPhoneRejections(t *testing.T) {
	for _, message := range []string{
		"Phone number is not valid.",
		"This phone number is already associated with another account.",
		"This phone number is already linked to an account.",
		"This phone number has already used the maximum number of accounts.",
		"Please use a different phone number.",
		"Try another phone number.",
		"This phone number is not supported.",
		"This phone number cannot be used.",
		"This phone number can't be used.",
		"Unable to send a verification code to this phone number.",
		"Too many accounts are associated with this number.",
		"\u8be5\u7535\u8bdd\u53f7\u7801\u5df2\u4f7f\u7528\uff0c\u8bf7\u66f4\u6362\u5176\u4ed6\u53f7\u7801\u3002",
		"\u8be5\u624b\u673a\u53f7\u4e0d\u53d7\u652f\u6301\u3002",
	} {
		t.Run(message, func(t *testing.T) {
			state := inspectOAuthPage(oauthPageSnapshot{
				URL:    "https://auth.openai.com/add-phone",
				Body:   message,
				Inputs: []oauthPageInput{{Index: 2, Metadata: "tel phone"}},
			})
			require.Equal(t, "phone_rejected", state.Kind)
			require.Equal(t, 2, state.Input)
		})
	}
	for _, message := range []string{
		"Phone number required Text message WhatsApp",
		"Unable to connect. Please try again later.",
		"Something went wrong. Please try again.",
	} {
		state := inspectOAuthPage(oauthPageSnapshot{
			URL:    "https://auth.openai.com/add-phone",
			Body:   message,
			Inputs: []oauthPageInput{{Index: 0, Metadata: "tel phone"}},
		})
		require.Equal(t, "phone", state.Kind, message)
	}
}

func TestInspectOAuthPageRequiresExplicitProfileFields(t *testing.T) {
	state := inspectOAuthPage(oauthPageSnapshot{
		URL:    "https://auth.openai.com/about-you",
		Body:   "Tell us about yourself",
		Inputs: []oauthPageInput{{Index: 0, Metadata: "text"}, {Index: 1, Metadata: "numeric"}},
	})
	require.Equal(t, "unknown", state.Kind)
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
