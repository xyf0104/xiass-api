package admin

import (
	"context"
	"log/slog"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

const openAICreditsInitialRefreshTimeout = 30 * time.Second

type openAICreditsRefresher interface {
	QueryUsage(ctx context.Context, accountID int64) (*service.OpenAIQuotaUsage, error)
	CacheCreditsSnapshot(ctx context.Context, accountID int64, usage *service.OpenAIQuotaUsage) error
}

func scheduleOpenAICreditsRefresh(refresher any, account *service.Account) {
	if account == nil || !account.IsOpenAIOAuth() || account.ID <= 0 {
		return
	}
	creditsRefresher, ok := refresher.(openAICreditsRefresher)
	if !ok || creditsRefresher == nil {
		return
	}
	accountID := account.ID
	go func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				slog.Error("openai_credits_initial_refresh_panic", "account_id", accountID, "recover", recovered)
			}
		}()
		ctx, cancel := context.WithTimeout(context.Background(), openAICreditsInitialRefreshTimeout)
		defer cancel()
		usage, err := creditsRefresher.QueryUsage(ctx, accountID)
		if err != nil {
			slog.Warn("openai_credits_initial_query_failed", "account_id", accountID, "error", err)
			return
		}
		if usage == nil {
			slog.Warn("openai_credits_initial_query_empty", "account_id", accountID)
			return
		}
		if err := creditsRefresher.CacheCreditsSnapshot(ctx, accountID, usage); err != nil {
			slog.Warn("openai_credits_initial_cache_failed", "account_id", accountID, "error", err)
		}
	}()
}
