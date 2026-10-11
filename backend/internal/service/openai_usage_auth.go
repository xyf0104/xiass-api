package service

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const OpenAIUsageUnauthorizedReason = "oauth_401: OpenAI usage authentication failed (401)"

const OpenAIUsageUnauthorizedCooldown = 10 * time.Minute

// OpenAIUnauthorizedHandler is invoked only after an identity-bound OAuth 401
// marker has been committed. Implementations should enqueue the account and
// return quickly; the authorization workflow itself must run asynchronously.
type OpenAIUnauthorizedHandler func(ctx context.Context, accountID int64)

// A late quota response must never invalidate newly refreshed credentials.
type OpenAIUsageAuthRepository interface {
	UpdateOpenAIUsageAuthIfIdentityMatches(ctx context.Context, expected *Account, unauthorized bool) (bool, error)
}

func updateOpenAIUsageAuth(ctx context.Context, repo AccountRepository, expected *Account, unauthorized bool) bool {
	if expected == nil || !expected.IsOpenAIOAuth() || expected.IsOpenAIAgentIdentity() {
		return false
	}
	bound, ok := repo.(OpenAIUsageAuthRepository)
	if !ok {
		return false
	}
	writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer cancel()
	applied, err := bound.UpdateOpenAIUsageAuthIfIdentityMatches(writeCtx, expected, unauthorized)
	if err != nil {
		slog.Warn("openai_usage_auth_state_failed", "account_id", expected.ID, "unauthorized", unauthorized, "error", err)
		return false
	}
	return applied
}

func updateOpenAIUsageAuthAndNotify(
	ctx context.Context,
	repo AccountRepository,
	expected *Account,
	unauthorized bool,
	handler OpenAIUnauthorizedHandler,
) bool {
	applied := updateOpenAIUsageAuth(ctx, repo, expected, unauthorized)
	if applied && unauthorized && handler != nil {
		handler(context.WithoutCancel(ctx), expected.ID)
	}
	return applied
}

// resolveOpenAIUsageAuthOwner maps every runtime OpenAI OAuth row to the
// primary row that owns its credentials. Spark shadows and OAuth copies must
// never receive their own 401 marker or authorization workflow.
func resolveOpenAIUsageAuthOwner(ctx context.Context, repo AccountRepository, account *Account) (*Account, error) {
	if account == nil || !account.IsOpenAIOAuth() {
		return account, nil
	}
	if account.IsOpenAIOAuthCredentialCopy() {
		return resolveOpenAIOAuthCredentialSourceAccount(ctx, repo, account)
	}
	if account.IsCredentialShadow() {
		return resolveCredentialAccount(ctx, repo, account)
	}
	return account, nil
}

func isOpenAIUsageUnauthorized(err error) bool {
	if infraerrors.Code(err) != http.StatusUnauthorized {
		return false
	}
	switch infraerrors.Reason(err) {
	case "OPENAI_QUOTA_UPSTREAM_ERROR", "OPENAI_CODEX_PROBE_UNAUTHORIZED":
		return true
	default:
		return false
	}
}

// The marker is retained after its cooldown ends until credentials recover.
func IsOpenAIOAuthUnauthorizedReason(reason string) bool {
	reason = strings.ToLower(strings.TrimSpace(reason))
	return reason == "oauth_401" || strings.HasPrefix(reason, "oauth_401:") ||
		strings.HasPrefix(reason, "oauth 401:") || strings.HasPrefix(reason, "authentication failed (401)")
}

func applyOpenAIUsageUnauthorized(usage *UsageInfo, account *Account) {
	if usage == nil || account == nil || !account.IsOpenAIOAuth() {
		return
	}
	if IsOpenAIOAuthUnauthorizedReason(account.TempUnschedulableReason) {
		usage.NeedsReauth = true
		usage.ErrorCode = "unauthenticated"
		usage.Error = OpenAIUsageUnauthorizedReason
	}
}

func degradedOpenAIUnauthorizedUsage(usage *UsageInfo, err error) (*UsageInfo, error) {
	usage.NeedsReauth = true
	usage.ErrorCode = "unauthenticated"
	usage.Error = infraerrors.Message(err)
	return usage, nil
}

// Explicit refresh/reset workflows may persist authentication observations;
// the read-only GET quota endpoint deliberately continues to use QueryUsage.
func queryOpenAIQuotaForMutation(ctx context.Context, quota interface {
	QueryUsage(context.Context, int64) (*OpenAIQuotaUsage, error)
}, id int64) (*OpenAIQuotaUsage, error) {
	if refresh, ok := quota.(interface {
		RefreshUsage(context.Context, int64) (*OpenAIQuotaUsage, error)
	}); ok {
		return refresh.RefreshUsage(ctx, id)
	}
	return quota.QueryUsage(ctx, id)
}
