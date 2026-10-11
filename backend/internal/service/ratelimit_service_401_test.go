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

type rateLimitAccountRepoStub struct {
	mockAccountRepoForGemini
	setErrorCalls          int
	tempCalls              int
	usageAuthCalls         int
	updateCredentialsCalls int
	updateExtraCalls       int
	usageAuthReject        bool
	lastUsageAuthAccount   *Account
	lastCredentials        map[string]any
	lastExtraUpdates       map[string]any
	lastErrorMsg           string
	lastTempReason         string
	lastErrorID            int64
	lastTempID             int64
	tempErr                error
}

func (r *rateLimitAccountRepoStub) UpdateOpenAIUsageAuthIfIdentityMatches(_ context.Context, expected *Account, unauthorized bool) (bool, error) {
	r.usageAuthCalls++
	r.lastUsageAuthAccount = expected
	if r.usageAuthReject || !unauthorized {
		return false, nil
	}
	return true, nil
}

func (r *rateLimitAccountRepoStub) SetError(ctx context.Context, id int64, errorMsg string) error {
	r.setErrorCalls++
	r.lastErrorID = id
	r.lastErrorMsg = errorMsg
	return nil
}

func (r *rateLimitAccountRepoStub) SetTempUnschedulable(ctx context.Context, id int64, until time.Time, reason string) error {
	r.tempCalls++
	r.lastTempID = id
	r.lastTempReason = reason
	return r.tempErr
}

func (r *rateLimitAccountRepoStub) UpdateCredentials(ctx context.Context, id int64, credentials map[string]any) error {
	r.updateCredentialsCalls++
	r.lastCredentials = shallowCopyMap(credentials)
	return nil
}

func (r *rateLimitAccountRepoStub) UpdateExtra(ctx context.Context, id int64, updates map[string]any) error {
	r.updateExtraCalls++
	r.lastExtraUpdates = shallowCopyMap(updates)
	return nil
}

type tokenCacheInvalidatorRecorder struct {
	accounts []*Account
	err      error
}

type accountRuntimeBlockerRecorder struct {
	blockedAccountIDs []int64
	reasons           []string
}

func (r *accountRuntimeBlockerRecorder) BlockAccountScheduling(account *Account, _ time.Time, reason string) {
	r.blockedAccountIDs = append(r.blockedAccountIDs, account.ID)
	r.reasons = append(r.reasons, reason)
}

func (r *accountRuntimeBlockerRecorder) ClearAccountSchedulingBlock(int64) {}

type openAI403CounterCacheStub struct {
	counts     []int64
	resetCalls []int64
	err        error
}

func (s *openAI403CounterCacheStub) IncrementOpenAI403Count(_ context.Context, _ int64, _ int) (int64, error) {
	if s.err != nil {
		return 0, s.err
	}
	if len(s.counts) == 0 {
		return 1, nil
	}
	count := s.counts[0]
	s.counts = s.counts[1:]
	return count, nil
}

func (s *openAI403CounterCacheStub) ResetOpenAI403Count(_ context.Context, accountID int64) error {
	s.resetCalls = append(s.resetCalls, accountID)
	return nil
}

func (r *tokenCacheInvalidatorRecorder) InvalidateToken(ctx context.Context, account *Account) error {
	r.accounts = append(r.accounts, account)
	return r.err
}

func TestRateLimitService_HandleUpstreamError_OAuth401SetsTempUnschedulable(t *testing.T) {
	t.Run("gemini", func(t *testing.T) {
		repo := &rateLimitAccountRepoStub{}
		invalidator := &tokenCacheInvalidatorRecorder{}
		service := NewRateLimitService(repo, nil, &config.Config{}, nil, nil)
		service.SetTokenCacheInvalidator(invalidator)
		account := &Account{
			ID:       100,
			Platform: PlatformGemini,
			Type:     AccountTypeOAuth,
			Credentials: map[string]any{
				"refresh_token":              "rt-100",
				"temp_unschedulable_enabled": true,
				"temp_unschedulable_rules": []any{
					map[string]any{
						"error_code":       401,
						"keywords":         []any{"unauthorized"},
						"duration_minutes": 30,
						"description":      "custom rule",
					},
				},
			},
		}

		shouldDisable := service.HandleUpstreamError(context.Background(), account, 401, http.Header{}, []byte("unauthorized"))

		require.True(t, shouldDisable)
		require.Equal(t, 0, repo.setErrorCalls)
		require.Equal(t, 1, repo.tempCalls)
		require.Len(t, invalidator.accounts, 1)
	})

	t.Run("antigravity_401_uses_temp_unschedulable", func(t *testing.T) {
		// Antigravity OAuth 401 改为临时冷却（2分钟），不再永久 SetError。
		// fingerprint 变化导致的暂时性 401 可通过 token_refresh_service 自动恢复。
		repo := &rateLimitAccountRepoStub{}
		invalidator := &tokenCacheInvalidatorRecorder{}
		service := NewRateLimitService(repo, nil, &config.Config{}, nil, nil)
		service.SetTokenCacheInvalidator(invalidator)
		account := &Account{
			ID:       100,
			Platform: PlatformAntigravity,
			Type:     AccountTypeOAuth,
			Status:   StatusActive,
			Credentials: map[string]any{
				"access_token":  "expired-at",
				"refresh_token": "rt-ag-100",
			},
		}

		shouldDisable := service.HandleUpstreamError(context.Background(), account, 401, http.Header{}, []byte("unauthorized"))

		require.True(t, shouldDisable)
		require.Equal(t, 0, repo.setErrorCalls, "Antigravity OAuth 401 should NOT SetError")
		require.Equal(t, 1, repo.tempCalls, "Antigravity OAuth 401 should SetTempUnschedulable")
		require.Equal(t, int64(100), repo.lastTempID)
		require.Contains(t, repo.lastTempReason, "invalid or expired credentials")
		require.Equal(t, 1, repo.updateExtraCalls)
		require.Equal(t, true, repo.lastExtraUpdates[antigravityForceTokenRefreshExtraKey])
		require.Equal(t, "401_invalid", repo.lastExtraUpdates[antigravityForceTokenRefreshReasonExtraKey])
		require.Equal(t, true, account.Extra[antigravityForceTokenRefreshExtraKey])
		require.Len(t, invalidator.accounts, 1, "token cache should be invalidated")
		require.Equal(t, int64(100), invalidator.accounts[0].ID)
	})
}

// TestRateLimitService_HandleUpstreamError_SparkShadow401RedirectsToParent 外审第9轮:影子无独立凭据,
// 401(母账号 token 问题)必须重定向到凭据 owner(母账号)——母账号 temp-unschedulable + token cache 失效,
// 影子不得被永久禁用(否则母账号可恢复的 token 问题会把影子永久打死)。
func TestRateLimitService_HandleUpstreamError_SparkShadow401RedirectsToParent(t *testing.T) {
	repo := &rateLimitAccountRepoStub{}
	repo.accountsByID = map[int64]*Account{}
	invalidator := &tokenCacheInvalidatorRecorder{}
	service := NewRateLimitService(repo, nil, &config.Config{}, nil, nil)
	service.SetTokenCacheInvalidator(invalidator)

	const parentID = int64(500)
	mother := &Account{
		ID:          parentID,
		Platform:    PlatformOpenAI,
		Type:        AccountTypeOAuth,
		Credentials: map[string]any{"access_token": "at-mother", "refresh_token": "rt-mother"},
	}
	repo.accountsByID[parentID] = mother

	shadowParent := parentID
	shadow := &Account{
		ID:              501,
		Platform:        PlatformOpenAI,
		Type:            AccountTypeOAuth,
		ParentAccountID: &shadowParent,
		QuotaDimension:  QuotaDimensionSpark,
		// 影子不持凭据:GetCredential("refresh_token") == ""
	}

	shouldDisable := service.HandleUpstreamError(context.Background(), shadow, 401, http.Header{}, []byte("unauthorized"))

	require.True(t, shouldDisable)
	require.Equal(t, 0, repo.setErrorCalls, "spark shadow must not be permanently disabled on a parent-token 401")
	require.Equal(t, 0, repo.tempCalls)
	require.Equal(t, 1, repo.usageAuthCalls)
	require.Equal(t, parentID, repo.lastUsageAuthAccount.ID, "identity-bound marker must target the credential owner (parent)")
	require.Len(t, invalidator.accounts, 1)
	require.Equal(t, parentID, invalidator.accounts[0].ID, "token cache invalidation must target the parent")
}

// TestRateLimitService_HandleUpstreamError_OAuth401InvalidatorError
// OpenAI OAuth 401 缓存失效出错时仍走 temp_unschedulable。
// 注意：401 handler 不再回写 credentials(避免请求开始时的快照整列覆盖 DB
// 把另一个 worker 刚刷新出来的新 refresh_token 回滚为旧值),
// 因此 updateCredentialsCalls 应当为 0。
func TestRateLimitService_HandleUpstreamError_OAuth401InvalidatorError(t *testing.T) {
	repo := &rateLimitAccountRepoStub{}
	invalidator := &tokenCacheInvalidatorRecorder{err: errors.New("boom")}
	service := NewRateLimitService(repo, nil, &config.Config{}, nil, nil)
	service.SetTokenCacheInvalidator(invalidator)
	account := &Account{
		ID:       101,
		Platform: PlatformOpenAI,
		Type:     AccountTypeOAuth,
		Credentials: map[string]any{
			"access_token":  "at-101",
			"refresh_token": "rt-101",
		},
	}

	shouldDisable := service.HandleUpstreamError(context.Background(), account, 401, http.Header{}, []byte("unauthorized"))

	require.True(t, shouldDisable)
	require.Equal(t, 0, repo.setErrorCalls)
	require.Equal(t, 0, repo.tempCalls)
	require.Equal(t, 1, repo.usageAuthCalls)
	require.Equal(t, 0, repo.updateCredentialsCalls)
	require.Len(t, invalidator.accounts, 1)
}

func TestRateLimitService_HandleUpstreamError_NonOAuth401(t *testing.T) {
	repo := &rateLimitAccountRepoStub{}
	invalidator := &tokenCacheInvalidatorRecorder{}
	service := NewRateLimitService(repo, nil, &config.Config{}, nil, nil)
	service.SetTokenCacheInvalidator(invalidator)
	account := &Account{
		ID:       102,
		Platform: PlatformOpenAI,
		Type:     AccountTypeAPIKey,
	}

	shouldDisable := service.HandleUpstreamError(context.Background(), account, 401, http.Header{}, []byte("unauthorized"))

	require.True(t, shouldDisable)
	require.Equal(t, 1, repo.setErrorCalls)
	require.Empty(t, invalidator.accounts)
}

// TestRateLimitService_HandleUpstreamError_OAuth401DoesNotOverwriteCredentials
// 回归测试:确保 401 handler 不再使用请求开始时的 account 快照写回 credentials。
// 原实现会通过 persistAccountCredentials → UpdateCredentials → SetCredentials
// 整列覆盖 credentials JSONB,在另一个 worker 刚刷新完 refresh_token 的窄窗口内
// 会把新 refresh_token 回滚为快照中的旧值,导致下一周期拿 invalid_grant 被错误 disable。
func TestRateLimitService_HandleUpstreamError_OAuth401DoesNotOverwriteCredentials(t *testing.T) {
	repo := &rateLimitAccountRepoStub{}
	service := NewRateLimitService(repo, nil, &config.Config{}, nil, nil)
	account := &Account{
		ID:       103,
		Platform: PlatformOpenAI,
		Type:     AccountTypeOAuth,
		Credentials: map[string]any{
			"access_token":  "token",
			"refresh_token": "rt-103",
		},
	}

	shouldDisable := service.HandleUpstreamError(context.Background(), account, 401, http.Header{}, []byte("unauthorized"))

	require.True(t, shouldDisable)
	require.Equal(t, 0, repo.updateCredentialsCalls, "401 handler must not write credentials back from the request-start snapshot")
	require.Equal(t, 0, repo.updateExtraCalls, "OpenAI 401 must not set Antigravity force-refresh marker")
	require.Equal(t, 0, repo.tempCalls, "OpenAI 401 must use the identity-bound auth marker instead of an unbound cooldown write")
	require.Equal(t, 1, repo.usageAuthCalls)
	require.Nil(t, repo.lastCredentials, "no credentials should have been persisted")
}

// XIASS 工作台保存的重新授权方式不依赖当前 refresh_token 仍然有效；因此
// OpenAI OAuth 收到真实 401 后统一进入身份绑定的重新授权队列。
func TestRateLimitService_HandleUpstreamError_OpenAI401WithoutRefreshTokenQueuesReauthorization(t *testing.T) {
	t.Run("openai_no_refresh_token", func(t *testing.T) {
		repo := &rateLimitAccountRepoStub{}
		invalidator := &tokenCacheInvalidatorRecorder{}
		service := NewRateLimitService(repo, nil, &config.Config{}, nil, nil)
		service.SetTokenCacheInvalidator(invalidator)
		recorder := &openAIUnauthorizedHandlerRecorder{}
		service.SetOpenAIUnauthorizedHandler(recorder.handler)
		account := &Account{
			ID:       2881,
			Platform: PlatformOpenAI,
			Type:     AccountTypeOAuth,
			Credentials: map[string]any{
				"access_token": "expired-at",
				// no refresh_token
			},
		}

		shouldDisable := service.HandleUpstreamError(context.Background(), account, 401, http.Header{}, []byte("unauthorized"))

		require.True(t, shouldDisable)
		require.Equal(t, 0, repo.setErrorCalls, "workbench OAuth 401 must remain recoverable")
		require.Equal(t, 0, repo.tempCalls, "AT-only OAuth 401 must NOT temp-unschedule")
		require.Equal(t, 1, repo.usageAuthCalls)
		require.Equal(t, 0, repo.updateCredentialsCalls, "no point forcing expires_at when refresh is impossible")
		require.Equal(t, []int64{account.ID}, recorder.accountIDs)
		require.Len(t, invalidator.accounts, 1, "cache should still be invalidated")
	})

	t.Run("openai_blank_refresh_token_treated_as_missing", func(t *testing.T) {
		repo := &rateLimitAccountRepoStub{}
		service := NewRateLimitService(repo, nil, &config.Config{}, nil, nil)
		recorder := &openAIUnauthorizedHandlerRecorder{}
		service.SetOpenAIUnauthorizedHandler(recorder.handler)
		account := &Account{
			ID:       2882,
			Platform: PlatformOpenAI,
			Type:     AccountTypeOAuth,
			Credentials: map[string]any{
				"access_token":  "expired-at",
				"refresh_token": "   ",
			},
		}

		shouldDisable := service.HandleUpstreamError(context.Background(), account, 401, http.Header{}, []byte("unauthorized"))

		require.True(t, shouldDisable)
		require.Equal(t, 0, repo.setErrorCalls)
		require.Equal(t, 0, repo.tempCalls)
		require.Equal(t, 1, repo.usageAuthCalls)
		require.Equal(t, []int64{account.ID}, recorder.accountIDs)
	})

	t.Run("antigravity_no_refresh_token_sets_error", func(t *testing.T) {
		repo := &rateLimitAccountRepoStub{}
		invalidator := &tokenCacheInvalidatorRecorder{}
		service := NewRateLimitService(repo, nil, &config.Config{}, nil, nil)
		service.SetTokenCacheInvalidator(invalidator)
		account := &Account{
			ID:       2883,
			Platform: PlatformAntigravity,
			Type:     AccountTypeOAuth,
			Credentials: map[string]any{
				"access_token": "expired-at",
			},
		}

		shouldDisable := service.HandleUpstreamError(context.Background(), account, 401, http.Header{}, []byte("unauthorized"))

		require.True(t, shouldDisable)
		require.Equal(t, 1, repo.setErrorCalls, "Antigravity OAuth without refresh_token cannot self-recover")
		require.Equal(t, 0, repo.tempCalls)
		require.Contains(t, repo.lastErrorMsg, "refresh_token missing")
		require.Len(t, invalidator.accounts, 1)
	})
}

func TestRateLimitService_OpenAI401NotifiesOnlyAfterCurrentIdentityWasMarked(t *testing.T) {
	repo := &rateLimitAccountRepoStub{usageAuthReject: true}
	invalidator := &tokenCacheInvalidatorRecorder{}
	blocker := &accountRuntimeBlockerRecorder{}
	recorder := &openAIUnauthorizedHandlerRecorder{}
	service := NewRateLimitService(repo, nil, &config.Config{}, nil, nil)
	service.SetTokenCacheInvalidator(invalidator)
	service.SetAccountRuntimeBlocker(blocker)
	service.SetOpenAIUnauthorizedHandler(recorder.handler)
	account := &Account{
		ID: 400, Platform: PlatformOpenAI, Type: AccountTypeOAuth,
		Credentials: map[string]any{"access_token": "stale-at", "refresh_token": "stale-rt"},
	}

	require.True(t, service.HandleUpstreamError(context.Background(), account, http.StatusUnauthorized, nil, []byte(`{"error":"unauthorized"}`)))
	require.Equal(t, 1, repo.usageAuthCalls)
	require.Empty(t, recorder.accountIDs, "a stale 401 must not start reauthorization for the new credentials")
	require.Empty(t, blocker.blockedAccountIDs, "a stale 401 must not block the current scheduler identity")
}

func TestRateLimitService_OpenAI401MarksAndNotifiesCredentialCopyOwner(t *testing.T) {
	repo := &rateLimitAccountRepoStub{}
	repo.accountsByID = map[int64]*Account{}
	invalidator := &tokenCacheInvalidatorRecorder{}
	blocker := &accountRuntimeBlockerRecorder{}
	recorder := &openAIUnauthorizedHandlerRecorder{}
	service := NewRateLimitService(repo, nil, &config.Config{}, nil, nil)
	service.SetTokenCacheInvalidator(invalidator)
	service.SetAccountRuntimeBlocker(blocker)
	service.SetOpenAIUnauthorizedHandler(recorder.handler)

	owner := &Account{
		ID: 420, Platform: PlatformOpenAI, Type: AccountTypeOAuth,
		Credentials: map[string]any{"access_token": "owner-at", "refresh_token": "owner-rt"},
	}
	repo.accountsByID[owner.ID] = owner
	copy := &Account{
		ID: 421, Platform: PlatformOpenAI, Type: AccountTypeOAuth,
		Credentials: map[string]any{"model_mapping": map[string]any{"gpt-6-astra": "gpt-6-astra"}},
		Extra:       map[string]any{OpenAIOAuthCredentialSourceIDExtraKey: owner.ID},
	}

	require.True(t, service.HandleUpstreamError(context.Background(), copy, http.StatusUnauthorized, nil, []byte("unauthorized")))
	require.Equal(t, 1, repo.usageAuthCalls)
	require.Same(t, owner, repo.lastUsageAuthAccount)
	require.Equal(t, []int64{owner.ID}, recorder.accountIDs)
	require.Equal(t, []int64{owner.ID}, blocker.blockedAccountIDs)
	require.Equal(t, []string{OpenAIUsageUnauthorizedReason}, blocker.reasons)
	require.Len(t, invalidator.accounts, 1)
	require.Same(t, owner, invalidator.accounts[0])
}

func TestRateLimitService_OpenAI401BypassesPoolAndCustomPolicyButNotAPIKeyOrAgentIdentity(t *testing.T) {
	t.Run("oauth pool account", func(t *testing.T) {
		repo := &rateLimitAccountRepoStub{}
		recorder := &openAIUnauthorizedHandlerRecorder{}
		service := NewRateLimitService(repo, nil, &config.Config{}, nil, nil)
		service.SetOpenAIUnauthorizedHandler(recorder.handler)
		account := &Account{
			ID: 410, Platform: PlatformOpenAI, Type: AccountTypeOAuth,
			Credentials: map[string]any{
				"access_token": "at-410", "refresh_token": "rt-410",
				"pool_mode": true,
			},
		}
		require.True(t, service.HandleUpstreamError(context.Background(), account, http.StatusUnauthorized, nil, []byte("custom body")))
		require.Equal(t, 1, repo.usageAuthCalls)
		require.Equal(t, []int64{account.ID}, recorder.accountIDs)
	})

	t.Run("api key", func(t *testing.T) {
		repo := &rateLimitAccountRepoStub{}
		recorder := &openAIUnauthorizedHandlerRecorder{}
		service := NewRateLimitService(repo, nil, &config.Config{}, nil, nil)
		service.SetOpenAIUnauthorizedHandler(recorder.handler)
		account := &Account{ID: 411, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}
		require.True(t, service.HandleUpstreamError(context.Background(), account, http.StatusUnauthorized, nil, []byte("custom body")))
		require.Equal(t, 0, repo.usageAuthCalls)
		require.Empty(t, recorder.accountIDs)
	})

	t.Run("agent identity", func(t *testing.T) {
		repo := &rateLimitAccountRepoStub{}
		recorder := &openAIUnauthorizedHandlerRecorder{}
		service := NewRateLimitService(repo, nil, &config.Config{}, nil, nil)
		service.SetOpenAIUnauthorizedHandler(recorder.handler)
		account := &Account{
			ID: 412, Platform: PlatformOpenAI, Type: AccountTypeOAuth,
			Credentials: map[string]any{
				"access_token": "agent-at", "refresh_token": "agent-rt",
				openAIAuthModeCredentialKey: OpenAIAuthModeAgentIdentity,
			},
		}
		require.True(t, service.HandleUpstreamError(context.Background(), account, http.StatusUnauthorized, nil, []byte("unauthorized")))
		require.Equal(t, 0, repo.usageAuthCalls)
		require.Empty(t, recorder.accountIDs)
	})
}
