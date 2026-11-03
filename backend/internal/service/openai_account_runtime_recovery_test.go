//go:build unit

package service

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type runtimeRecoveryAccountRepo struct {
	mockAccountRepoForGemini
	account *Account
}

func (r *runtimeRecoveryAccountRepo) SetRateLimited(_ context.Context, _ int64, until time.Time) error {
	r.account.RateLimitResetAt = &until
	return nil
}

func (r *runtimeRecoveryAccountRepo) ClearRateLimit(context.Context, int64) error {
	r.account.RateLimitResetAt = nil
	return nil
}

func TestOpenAIRuntimeRecoveryClearsPersistedAndLocalQuotaBlock(t *testing.T) {
	cfg := &config.Config{}
	account := &Account{ID: 354, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true}
	repo := &runtimeRecoveryAccountRepo{account: account}
	limits := NewRateLimitService(repo, nil, cfg, nil, nil)
	svc := &OpenAIGatewayService{cfg: cfg, rateLimitService: limits}
	limits.SetAccountRuntimeBlocker(svc)
	headers := http.Header{}
	headers.Set("x-codex-primary-used-percent", "100")
	headers.Set("x-codex-primary-reset-after-seconds", "604800")
	headers.Set("x-codex-primary-window-minutes", "10080")
	svc.handleOpenAIAccountUpstreamError(context.Background(), account, http.StatusTooManyRequests, headers, nil)
	require.NotNil(t, account.RateLimitResetAt)
	require.Greater(t, time.Until(*account.RateLimitResetAt), 6*24*time.Hour)
	require.False(t, account.IsSchedulable())
	value, ok := svc.openaiAccountRuntimeBlockUntil.Load(account.ID)
	require.True(t, ok)
	require.Equal(t, *account.RateLimitResetAt, value, "local scheduling must retain the actual upstream deadline")

	for range 2 {
		require.NoError(t, limits.ClearRateLimit(context.Background(), account.ID))
		require.Nil(t, account.RateLimitResetAt)
		require.False(t, svc.isOpenAIAccountRuntimeBlocked(account))
		require.True(t, account.IsSchedulable())
	}
	account.Schedulable = false
	require.NoError(t, limits.ClearRateLimit(context.Background(), account.ID))
	require.False(t, account.IsSchedulable(), "recovery must preserve manual suspension")
}

func TestOpenAIRuntimeBlockKeepsExplicitDeadlineAcrossAccountTypes(t *testing.T) {
	for _, accountType := range []string{AccountTypeOAuth, AccountTypeSetupToken, AccountTypeAPIKey} {
		for _, reason := range []string{"429", "429_fallback", "oauth_401"} {
			t.Run(accountType+"/"+reason, func(t *testing.T) {
				svc := &OpenAIGatewayService{}
				account := &Account{ID: 1, Platform: PlatformOpenAI, Type: accountType}
				until := time.Now().Add(7 * 24 * time.Hour)
				svc.BlockAccountScheduling(account, until, reason)
				value, ok := svc.openaiAccountRuntimeBlockUntil.Load(account.ID)
				require.True(t, ok)
				require.Equal(t, until, value)
				svc.BlockAccountScheduling(account, time.Now().Add(time.Second), "429")
				value, _ = svc.openaiAccountRuntimeBlockUntil.Load(account.ID)
				require.Equal(t, until, value, "a later short cooldown must not shorten an existing block")
			})
		}
	}
}
