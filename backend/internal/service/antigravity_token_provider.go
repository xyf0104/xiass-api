package service

import (
	"context"
	"errors"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	antigravityTokenRefreshSkew = 3 * time.Minute
	antigravityTokenCacheSkew   = 5 * time.Minute
	antigravityBackfillCooldown = 5 * time.Minute
	// antigravityRequestRefreshTimeout 请求路径上 token 刷新的最大等待时间。
	// 超过此时间直接放弃刷新、标记账号临时不可调度并触发 failover，
	// 让后台 TokenRefreshService 在下个周期继续重试。
	antigravityRequestRefreshTimeout = 8 * time.Second
)

// AntigravityTokenCache token cache interface.
type AntigravityTokenCache = GeminiTokenCache

// AntigravityTokenProvider manages access_token for antigravity accounts.
type AntigravityTokenProvider struct {
	accountRepo             AccountRepository
	tokenCache              AntigravityTokenCache
	antigravityOAuthService *AntigravityOAuthService
	backfillCooldown        sync.Map // key: accountID -> last attempt time
	refreshAPI              *OAuthRefreshAPI
	executor                OAuthRefreshExecutor
	refreshPolicy           ProviderRefreshPolicy
	tempUnschedCache        TempUnschedCache // 用于同步更新 Redis 临时不可调度缓存
}

type antigravityReadOnlyProbeContextKey struct{}

func withAntigravityReadOnlyProbeContext(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, antigravityReadOnlyProbeContextKey{}, true)
}

func isAntigravityReadOnlyProbeContext(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	readOnly, _ := ctx.Value(antigravityReadOnlyProbeContextKey{}).(bool)
	return readOnly
}

func NewAntigravityTokenProvider(
	accountRepo AccountRepository,
	tokenCache AntigravityTokenCache,
	antigravityOAuthService *AntigravityOAuthService,
) *AntigravityTokenProvider {
	return &AntigravityTokenProvider{
		accountRepo:             accountRepo,
		tokenCache:              tokenCache,
		antigravityOAuthService: antigravityOAuthService,
		refreshPolicy:           AntigravityProviderRefreshPolicy(),
	}
}

// SetRefreshAPI injects unified OAuth refresh API and executor.
func (p *AntigravityTokenProvider) SetRefreshAPI(api *OAuthRefreshAPI, executor OAuthRefreshExecutor) {
	p.refreshAPI = api
	p.executor = executor
}

// SetRefreshPolicy injects caller-side refresh policy.
func (p *AntigravityTokenProvider) SetRefreshPolicy(policy ProviderRefreshPolicy) {
	p.refreshPolicy = policy
}

// SetTempUnschedCache injects temp unschedulable cache for immediate scheduler sync.
func (p *AntigravityTokenProvider) SetTempUnschedCache(cache TempUnschedCache) {
	p.tempUnschedCache = cache
}

// GetAccessToken returns a valid access_token.
func (p *AntigravityTokenProvider) GetAccessToken(ctx context.Context, account *Account) (string, error) {
	if account == nil {
		return "", errors.New("account is nil")
	}
	if account.Platform != PlatformAntigravity {
		return "", errors.New("not an antigravity account")
	}

	// upstream accounts use static api_key and never refresh oauth token.
	if account.Type == AccountTypeUpstream {
		apiKey := account.GetCredential("api_key")
		if apiKey == "" {
			return "", errors.New("upstream account missing api_key in credentials")
		}
		return apiKey, nil
	}
	if account.Type != AccountTypeOAuth {
		return "", errors.New("not an antigravity oauth account")
	}
	readOnlyProbe := isAntigravityReadOnlyProbeContext(ctx)

	cacheKey := AntigravityTokenCacheKey(account)

	// 1) Try cache first.
	if p.tokenCache != nil {
		if token, err := p.tokenCache.GetAccessToken(ctx, cacheKey); err == nil && strings.TrimSpace(token) != "" {
			return token, nil
		}
	}

	// 2) Refresh if needed (pre-expiry skew).
	expiresAt := account.GetCredentialAsTime("expires_at")
	needsRefresh := expiresAt == nil || time.Until(*expiresAt) <= antigravityTokenRefreshSkew
	if needsRefresh && p.refreshAPI != nil && p.executor != nil {
		// 请求路径使用短超时，避免代理不通时阻塞过久（后台刷新服务会继续重试）
		refreshCtx, cancel := context.WithTimeout(ctx, antigravityRequestRefreshTimeout)
		defer cancel()
		result, err := p.refreshAPI.RefreshIfNeeded(refreshCtx, account, p.executor, antigravityTokenRefreshSkew)
		if err != nil {
			if !readOnlyProbe {
				// Only quarantine the exact credential identity used by the failed
				// attempt. A concurrent interactive reauthorization must win.
				attemptedAccount := account
				if result != nil && result.Account != nil {
					attemptedAccount = result.Account
				}
				p.markTempUnschedulable(attemptedAccount, err)
			}
			if p.refreshPolicy.OnRefreshError == ProviderRefreshErrorReturn {
				return "", err
			}
		} else if result.LockHeld {
			if p.refreshPolicy.OnLockHeld == ProviderLockHeldWaitForCache && p.tokenCache != nil {
				if token, cacheErr := p.tokenCache.GetAccessToken(ctx, cacheKey); cacheErr == nil && strings.TrimSpace(token) != "" {
					return token, nil
				}
			}
			// default policy: continue with existing token.
		} else {
			account = result.Account
			expiresAt = account.GetCredentialAsTime("expires_at")
		}
	} else if needsRefresh && p.tokenCache != nil {
		// Backward-compatible test path when refreshAPI is not injected.
		locked, err := p.tokenCache.AcquireRefreshLock(ctx, cacheKey, 30*time.Second)
		if err == nil && locked {
			defer func() { _ = p.tokenCache.ReleaseRefreshLock(ctx, cacheKey) }()
		}
	}

	accessToken := account.GetCredential("access_token")
	if strings.TrimSpace(accessToken) == "" {
		return "", errors.New("access_token not found in credentials")
	}

	// Backfill project_id online when missing, with cooldown to avoid hammering.
	if !readOnlyProbe && strings.TrimSpace(account.GetCredential("project_id")) == "" && p.antigravityOAuthService != nil {
		if p.shouldAttemptBackfill(account.ID) {
			p.markBackfillAttempted(account.ID)
			if projectID, err := p.antigravityOAuthService.FillProjectID(ctx, account, accessToken); err == nil && projectID != "" {
				if _, updateErr := p.persistProjectIDIfUnchanged(ctx, account, projectID); updateErr != nil {
					slog.Warn("antigravity_project_id_backfill_persist_failed",
						"account_id", account.ID,
						"error", updateErr,
					)
				}
			}
		}
	}

	// 3) Populate cache with TTL.
	if p.tokenCache != nil {
		latestAccount, isStale := CheckTokenVersion(ctx, account, p.accountRepo)
		if isStale && latestAccount != nil {
			slog.Debug("antigravity_token_version_stale_use_latest", "account_id", account.ID)
			accessToken = latestAccount.GetCredential("access_token")
			if strings.TrimSpace(accessToken) == "" {
				return "", errors.New("access_token not found after version check")
			}
		} else {
			ttl := 30 * time.Minute
			if expiresAt != nil {
				until := time.Until(*expiresAt)
				switch {
				case until > antigravityTokenCacheSkew:
					ttl = until - antigravityTokenCacheSkew
				case until > 0:
					ttl = until
				default:
					ttl = time.Minute
				}
			}
			_ = p.tokenCache.SetAccessToken(ctx, AntigravityTokenCacheKey(account), accessToken, ttl)
		}
	}

	return accessToken, nil
}

func (p *AntigravityTokenProvider) persistProjectIDIfUnchanged(ctx context.Context, account *Account, projectID string) (bool, error) {
	if p == nil || account == nil || strings.TrimSpace(projectID) == "" {
		return false, errors.New("antigravity project backfill input is invalid")
	}
	conditionalRepo, ok := p.accountRepo.(AntigravityOAuthRefreshSuccessRepository)
	if !ok {
		return false, errors.New("antigravity OAuth project backfill CAS repository is not configured")
	}
	attemptedAccount := snapshotOAuthRefreshAccount(account)
	credentials := shallowCopyMap(attemptedAccount.Credentials)
	credentials["project_id"] = strings.TrimSpace(projectID)
	applied, err := conditionalRepo.UpdateAntigravityOAuthCredentialsIfUnchanged(
		ctx,
		attemptedAccount.ID,
		attemptedAccount.Credentials,
		attemptedAccount.ProxyID,
		antigravityOAuthProxyUpdatedAt(attemptedAccount),
		credentials,
	)
	if err != nil || !applied {
		return applied, err
	}
	account.Credentials = credentials
	return true, nil
}

// shouldAttemptBackfill checks backfill cooldown.
func (p *AntigravityTokenProvider) shouldAttemptBackfill(accountID int64) bool {
	if v, ok := p.backfillCooldown.Load(accountID); ok {
		if lastAttempt, ok := v.(time.Time); ok {
			return time.Since(lastAttempt) > antigravityBackfillCooldown
		}
	}
	return true
}

// markTempUnschedulable 在请求路径上 token 刷新失败时标记账号临时不可调度。
// 同时写 DB 和 Redis 缓存，确保调度器立即跳过该账号。
// 使用 background context 因为请求 context 可能已超时。
func (p *AntigravityTokenProvider) markTempUnschedulable(account *Account, refreshErr error) {
	if p.accountRepo == nil || account == nil {
		return
	}
	now := time.Now()
	until := now.Add(tokenRefreshTempUnschedDuration)
	reason := "token refresh failed on request path: " + refreshErr.Error()
	bgCtx := context.Background()
	conditionalRepo, ok := p.accountRepo.(AntigravityOAuthRefreshMutationRepository)
	if !ok {
		slog.Warn("antigravity_token_provider.temp_unschedulable_cas_unavailable", "account_id", account.ID)
		return
	}
	applied, err := conditionalRepo.SetAntigravityOAuthRefreshTempUnschedulableIfCredentialsUnchanged(
		bgCtx,
		account.ID,
		shallowCopyMap(account.Credentials),
		cloneInt64Pointer(account.ProxyID),
		antigravityOAuthProxyUpdatedAt(account),
		until,
		reason,
	)
	if err != nil {
		slog.Warn("antigravity_token_provider.set_temp_unschedulable_failed",
			"account_id", account.ID,
			"error", err,
		)
		return
	}
	if !applied {
		slog.Debug("antigravity_token_provider.temp_unschedulable_skipped_stale_identity", "account_id", account.ID)
		return
	}
	slog.Warn("antigravity_token_provider.temp_unschedulable_set",
		"account_id", account.ID,
		"until", until.Format(time.RFC3339),
		"reason", reason,
	)
	// 同步写 Redis 缓存，调度器立即生效
	if p.tempUnschedCache != nil {
		state := &TempUnschedState{
			UntilUnix:       until.Unix(),
			TriggeredAtUnix: now.Unix(),
			ErrorMessage:    reason,
		}
		if err := p.tempUnschedCache.SetTempUnsched(bgCtx, account.ID, state); err != nil {
			slog.Warn("antigravity_token_provider.temp_unsched_cache_set_failed",
				"account_id", account.ID,
				"error", err,
			)
		}
	}
}

func (p *AntigravityTokenProvider) markBackfillAttempted(accountID int64) {
	p.backfillCooldown.Store(accountID, time.Now())
}

func AntigravityTokenCacheKey(account *Account) string {
	return "ag:account:" + strconv.FormatInt(account.ID, 10)
}
