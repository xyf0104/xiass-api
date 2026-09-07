//go:build unit

package service

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type cluster429Repo struct {
	mockAccountRepoForGemini
	account  *Account
	writeErr error
}

func (r *cluster429Repo) SetRateLimited(_ context.Context, _ int64, until time.Time) error {
	if r.writeErr != nil {
		return r.writeErr
	}
	r.account.RateLimitResetAt = &until
	return nil
}

func (r *cluster429Repo) ClearRateLimit(context.Context, int64) error {
	r.account.RateLimitResetAt = nil
	return nil
}

func TestOpenAICluster429RecoveryKeepsSharedDeadline(t *testing.T) {
	cfg := &config.Config{}
	cfg.Gateway.ExecutionNode.Enabled = true
	account := &Account{ID: 354, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusActive, Schedulable: true}
	repo := &cluster429Repo{account: account}
	mainLimits := NewRateLimitService(repo, nil, cfg, nil, nil)
	main := &OpenAIGatewayService{cfg: cfg, rateLimitService: mainLimits}
	mainLimits.SetAccountRuntimeBlocker(main)
	ownerLimits := NewRateLimitService(repo, nil, cfg, nil, nil)
	owner := &OpenAIGatewayService{cfg: cfg, rateLimitService: ownerLimits}
	ownerLimits.SetAccountRuntimeBlocker(owner)
	headers := http.Header{}
	headers.Set("x-codex-primary-used-percent", "100")
	headers.Set("x-codex-primary-reset-after-seconds", "604800")
	headers.Set("x-codex-primary-window-minutes", "10080")
	main.handleOpenAIAccountUpstreamError(context.Background(), account, http.StatusTooManyRequests, headers, nil)
	require.NotNil(t, account.RateLimitResetAt)
	require.Greater(t, time.Until(*account.RateLimitResetAt), 6*24*time.Hour)
	value, ok := main.openaiAccountRuntimeBlockUntil.Load(account.ID)
	require.True(t, ok)
	until, valid := value.(time.Time)
	require.True(t, valid)
	require.WithinDuration(t, time.Now().Add(openAIStopSchedulingBridgeCooldown), until, time.Second)

	// Advancing the process-local bridge cannot bypass the shared upstream limit.
	main.openaiAccountRuntimeBlockUntil.Store(account.ID, time.Now().Add(-time.Second))
	require.False(t, main.isOpenAIAccountRuntimeBlocked(account))
	require.False(t, account.IsSchedulable())

	main.BlockAccountScheduling(account, *account.RateLimitResetAt, "429")
	require.NoError(t, ownerLimits.ClearRateLimit(context.Background(), account.ID))
	require.True(t, main.isOpenAIAccountRuntimeBlocked(account), "owner recovery must not directly clear another process's map")
	require.True(t, account.IsSchedulable())
	main.openaiAccountRuntimeBlockUntil.Store(account.ID, time.Now().Add(-time.Second))
	require.False(t, main.isOpenAIAccountRuntimeBlocked(account))
	require.True(t, account.IsSchedulable(), "recovered shared account becomes eligible after the bounded bridge")
	account.Schedulable = false
	require.False(t, account.IsSchedulable(), "manual suspension remains authoritative")
}

func TestOpenAICluster429BridgeScope(t *testing.T) {
	for _, tc := range []struct {
		name, platform, reason string
		cluster, capped        bool
	}{
		{"quota", PlatformOpenAI, "429", true, true},
		{"fallback", PlatformOpenAI, "429_fallback", true, true},
		{"single_node", PlatformOpenAI, "429", false, false},
		{"grok", PlatformGrok, "429", true, false},
		{"auth", PlatformOpenAI, "oauth_401", true, false},
		{"transport", PlatformOpenAI, "transport_error", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &config.Config{}
			cfg.Gateway.ExecutionNode.Enabled = tc.cluster
			svc := &OpenAIGatewayService{cfg: cfg}
			account := &Account{ID: 1, Platform: tc.platform, Type: AccountTypeOAuth}
			until := time.Now().Add(7 * 24 * time.Hour)
			svc.BlockAccountScheduling(account, until, tc.reason)
			value, ok := svc.openaiAccountRuntimeBlockUntil.Load(account.ID)
			require.True(t, ok)
			if tc.capped {
				until, valid := value.(time.Time)
				require.True(t, valid)
				require.WithinDuration(t, time.Now().Add(openAIStopSchedulingBridgeCooldown), until, time.Second)
			} else {
				require.Equal(t, until, value)
			}
		})
	}
	cfg := &config.Config{}
	cfg.Gateway.ExecutionNode.Enabled = true
	for _, accountType := range []string{AccountTypeOAuth, AccountTypeSetupToken, AccountTypeAPIKey} {
		t.Run(accountType, func(t *testing.T) {
			svc := &OpenAIGatewayService{cfg: cfg}
			account := &Account{ID: 2, Platform: PlatformOpenAI, Type: accountType}
			short := time.Now().Add(5 * time.Second)
			svc.BlockAccountScheduling(account, short, "429")
			value, _ := svc.openaiAccountRuntimeBlockUntil.Load(account.ID)
			require.Equal(t, short, value)
			long := time.Now().Add(time.Hour)
			svc.BlockAccountScheduling(account, long, "oauth_401")
			svc.BlockAccountScheduling(account, long, "429")
			value, _ = svc.openaiAccountRuntimeBlockUntil.Load(account.ID)
			require.Equal(t, long, value, "quota bridge must not shorten an independent auth block")
		})
	}
}

func TestOpenAICluster429PersistenceFailureRemainsBounded(t *testing.T) {
	cfg := &config.Config{}
	cfg.Gateway.ExecutionNode.Enabled = true
	account := &Account{ID: 353, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusActive, Schedulable: true}
	repo := &cluster429Repo{account: account, writeErr: errors.New("database unavailable")}
	limits := NewRateLimitService(repo, nil, cfg, nil, nil)
	svc := &OpenAIGatewayService{cfg: cfg, rateLimitService: limits}
	limits.SetAccountRuntimeBlocker(svc)
	headers := http.Header{}
	headers.Set("x-codex-primary-used-percent", "100")
	headers.Set("x-codex-primary-reset-after-seconds", "604800")
	headers.Set("x-codex-primary-window-minutes", "10080")
	for range 2 {
		svc.handleOpenAIAccountUpstreamError(context.Background(), account, http.StatusTooManyRequests, headers, nil)
		require.Nil(t, account.RateLimitResetAt)
		require.True(t, svc.isOpenAIAccountRuntimeBlocked(account))
		value, _ := svc.openaiAccountRuntimeBlockUntil.Load(account.ID)
		until, valid := value.(time.Time)
		require.True(t, valid)
		require.WithinDuration(t, time.Now().Add(openAIStopSchedulingBridgeCooldown), until, time.Second)
		svc.openaiAccountRuntimeBlockUntil.Store(account.ID, time.Now().Add(-time.Second))
		require.False(t, svc.isOpenAIAccountRuntimeBlocked(account))
	}
}
