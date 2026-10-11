//go:build unit

package service

import (
	"context"
	"net/http"
	"testing"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/stretchr/testify/require"
)

func TestNormalizeOpenAICreditsExtra(t *testing.T) {
	t.Run("OpenAI OAuth defaults disabled", func(t *testing.T) {
		extra, err := normalizeOpenAICreditsExtra(PlatformOpenAI, AccountTypeOAuth, nil)

		require.NoError(t, err)
		require.Equal(t, false, extra[OpenAICreditsEnabledExtraKey])
	})

	t.Run("explicit opt in is preserved", func(t *testing.T) {
		extra, err := normalizeOpenAICreditsExtra(PlatformOpenAI, AccountTypeOAuth, map[string]any{
			OpenAICreditsEnabledExtraKey: true,
		})

		require.NoError(t, err)
		require.Equal(t, true, extra[OpenAICreditsEnabledExtraKey])
	})

	t.Run("malformed value is rejected", func(t *testing.T) {
		_, err := normalizeOpenAICreditsExtra(PlatformOpenAI, AccountTypeOAuth, map[string]any{
			OpenAICreditsEnabledExtraKey: "true",
		})

		require.Equal(t, http.StatusBadRequest, infraerrors.Code(err))
		require.Equal(t, "OPENAI_CREDITS_ENABLED_INVALID", infraerrors.Reason(err))
	})

	for _, test := range []struct {
		name        string
		platform    string
		accountType string
	}{
		{name: "OpenAI API key", platform: PlatformOpenAI, accountType: AccountTypeAPIKey},
		{name: "other OAuth platform", platform: PlatformAnthropic, accountType: AccountTypeOAuth},
	} {
		t.Run(test.name+" rejects the field", func(t *testing.T) {
			_, err := normalizeOpenAICreditsExtra(test.platform, test.accountType, map[string]any{
				OpenAICreditsEnabledExtraKey: false,
			})

			require.Equal(t, http.StatusBadRequest, infraerrors.Code(err))
			require.Equal(t, "OPENAI_CREDITS_OPENAI_OAUTH_ONLY", infraerrors.Reason(err))
		})
	}

	t.Run("provider runtime snapshot is always removed", func(t *testing.T) {
		extra, err := normalizeOpenAICreditsExtra(PlatformOpenAI, AccountTypeOAuth, map[string]any{
			OpenAICreditsEnabledExtraKey:        true,
			"codex_credits_snapshot":            map[string]any{"credits": map[string]any{"balance": "62500"}},
			"codex_credits_snapshot_updated_at": "2026-10-11T00:00:00Z",
		})

		require.NoError(t, err)
		require.Equal(t, true, extra[OpenAICreditsEnabledExtraKey])
		require.NotContains(t, extra, "codex_credits_snapshot")
		require.NotContains(t, extra, "codex_credits_snapshot_updated_at")
	})
}

func TestAdminServiceOpenAICreditsLifecycle(t *testing.T) {
	t.Run("create strips injected snapshot and defaults the opt in off", func(t *testing.T) {
		repo := &longContextBillingRepoStub{}
		svc := &adminServiceImpl{accountRepo: repo}

		account, err := svc.CreateAccount(context.Background(), &CreateAccountInput{
			Name:        "oauth",
			Platform:    PlatformOpenAI,
			Type:        AccountTypeOAuth,
			Credentials: map[string]any{"access_token": "token"},
			Extra: map[string]any{
				"codex_credits_snapshot": map[string]any{"credits": map[string]any{"balance": "62500"}},
			},
			SkipDefaultGroupBind: true,
		})

		require.NoError(t, err)
		require.Equal(t, false, account.Extra[OpenAICreditsEnabledExtraKey])
		require.NotContains(t, account.Extra, "codex_credits_snapshot")
	})

	t.Run("edit preserves provider snapshot and administrator opt in", func(t *testing.T) {
		snapshot := map[string]any{"credits": map[string]any{"has_credits": true, "balance": "62500"}, "fetched_at": int64(123)}
		repo := &longContextBillingRepoStub{account: &Account{
			ID:          1,
			Platform:    PlatformOpenAI,
			Type:        AccountTypeOAuth,
			Credentials: map[string]any{"access_token": "token"},
			Extra: map[string]any{
				OpenAICreditsEnabledExtraKey:        true,
				"codex_credits_snapshot":            snapshot,
				"codex_credits_snapshot_updated_at": "2026-10-11T00:00:00Z",
				"custom":                            "old",
			},
		}}
		svc := &adminServiceImpl{accountRepo: repo}

		account, err := svc.UpdateAccount(context.Background(), 1, &UpdateAccountInput{
			Extra: map[string]any{
				"custom":                 "new",
				"codex_credits_snapshot": map[string]any{"credits": nil},
			},
		})

		require.NoError(t, err)
		require.Equal(t, true, account.Extra[OpenAICreditsEnabledExtraKey])
		require.Equal(t, snapshot, account.Extra["codex_credits_snapshot"])
		require.Equal(t, "2026-10-11T00:00:00Z", account.Extra["codex_credits_snapshot_updated_at"])
		require.Equal(t, "new", account.Extra["custom"])
	})

	t.Run("bulk update rejects a non OAuth target", func(t *testing.T) {
		repo := &longContextBillingRepoStub{account: &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}}
		svc := &adminServiceImpl{accountRepo: repo}

		result, err := svc.BulkUpdateAccounts(context.Background(), &BulkUpdateAccountsInput{
			AccountIDs: []int64{1},
			Extra:      map[string]any{OpenAICreditsEnabledExtraKey: true},
		})

		require.Nil(t, result)
		require.Equal(t, http.StatusBadRequest, infraerrors.Code(err))
		require.Equal(t, "OPENAI_CREDITS_OPENAI_OAUTH_ONLY", infraerrors.Reason(err))
		require.Zero(t, repo.bulkUpdateCalls)
	})

	t.Run("changing away from OAuth removes the opt in without an extra payload", func(t *testing.T) {
		repo := &longContextBillingRepoStub{account: &Account{
			ID:          1,
			Platform:    PlatformOpenAI,
			Type:        AccountTypeOAuth,
			Credentials: map[string]any{"access_token": "token"},
			Extra:       map[string]any{OpenAICreditsEnabledExtraKey: true},
		}}
		svc := &adminServiceImpl{accountRepo: repo}

		account, err := svc.UpdateAccount(context.Background(), 1, &UpdateAccountInput{Type: AccountTypeAPIKey})

		require.NoError(t, err)
		require.NotContains(t, account.Extra, OpenAICreditsEnabledExtraKey)
	})

	t.Run("changing to OAuth defaults the opt in off without an extra payload", func(t *testing.T) {
		repo := &longContextBillingRepoStub{account: &Account{
			ID:          1,
			Platform:    PlatformOpenAI,
			Type:        AccountTypeAPIKey,
			Credentials: map[string]any{"api_key": "test"},
		}}
		svc := &adminServiceImpl{accountRepo: repo}

		account, err := svc.UpdateAccount(context.Background(), 1, &UpdateAccountInput{Type: AccountTypeOAuth})

		require.NoError(t, err)
		require.Equal(t, false, account.Extra[OpenAICreditsEnabledExtraKey])
	})
}
