package service

import (
	"context"
	"log/slog"
	"time"
)

// ApplyAccountSchedulingThreshold evaluates admin-configured per-platform
// utilization thresholds and, when breached, parks the account as temp-
// unschedulable until the winning window resets. Returns true when the account
// is blocked (either newly or already paused for the same threshold reason).
func (s *RateLimitService) ApplyAccountSchedulingThreshold(ctx context.Context, account *Account) bool {
	if s == nil || s.settingService == nil || s.accountRepo == nil || account == nil || account.ID <= 0 {
		return false
	}
	if !account.IsActive() || !account.Schedulable {
		return false
	}

	now := time.Now().UTC()
	thresholds := s.settingService.GetAccountSchedulingThresholds(ctx)
	decision := EvaluateAccountSchedulingThreshold(account, thresholds, now)
	if !decision.ShouldPause || decision.Until == nil || !decision.Until.After(now) {
		s.applyAnthropicFableSchedulingThreshold(ctx, account, thresholds, now)
		return false
	}

	reason := BuildDetailedAccountSchedulingThresholdReason(AccountSchedulingThresholdReasonInput{
		Platform:         decision.Platform,
		Window:           decision.Window,
		Scope:            decision.Scope,
		ThresholdPercent: decision.ThresholdPercent, UsedPercent: decision.UsedPercent,
		Until: *decision.Until,
		Now:   now,
	})

	if accountHasSameSchedulingThresholdPause(account, *decision.Until, reason) {
		return true
	}
	if !account.IsSchedulable() {
		return false
	}

	account.TempUnschedulableUntil = cloneTimePtr(decision.Until)
	account.TempUnschedulableReason = reason
	s.notifyAccountSchedulingBlocked(account, *decision.Until, "account_scheduling_threshold")

	if err := s.accountRepo.SetTempUnschedulable(ctx, account.ID, *decision.Until, reason); err != nil {
		slog.Warn("account_scheduling_threshold_set_temp_unsched_failed",
			"account_id", account.ID,
			"platform", decision.Platform,
			"window", decision.Window,
			"scope", decision.Scope,
			"threshold_percent", decision.ThresholdPercent,
			"used_percent", decision.UsedPercent,
			"until", decision.Until.UTC(),
			"error", err)
	} else if s.tempUnschedCache != nil {
		if state := tempUnschedStateFromStoredReason(reason, decision.Until.Unix()); state != nil {
			if err := s.tempUnschedCache.SetTempUnsched(ctx, account.ID, state); err != nil {
				slog.Warn("account_scheduling_threshold_cache_set_failed", "account_id", account.ID, "error", err)
			}
		}
	}

	slog.Info("account_scheduling_threshold_temp_unschedulable",
		"account_id", account.ID,
		"platform", decision.Platform,
		"window", decision.Window,
		"scope", decision.Scope,
		"threshold_percent", decision.ThresholdPercent,
		"used_percent", decision.UsedPercent,
		"until", decision.Until.UTC())
	return true
}

func (s *RateLimitService) applyAnthropicFableSchedulingThreshold(ctx context.Context, account *Account, thresholds map[string]int, now time.Time) {
	decision := evaluateAnthropicFableSchedulingThreshold(account, thresholds, now)
	if !decision.ShouldPause || decision.Until == nil || !decision.Until.After(now) {
		return
	}
	if account.isRateLimitActiveForKey(anthropicFableRateLimitKey) {
		return
	}

	reason := BuildDetailedAccountSchedulingThresholdReason(AccountSchedulingThresholdReasonInput{
		Platform:         decision.Platform,
		Window:           decision.Window,
		Scope:            decision.Scope,
		ThresholdPercent: decision.ThresholdPercent,
		UsedPercent:      decision.UsedPercent,
		Until:            *decision.Until,
		Now:              now,
	})
	setAccountModelRateLimitSnapshot(account, anthropicFableRateLimitKey, *decision.Until, reason, now)
	if err := s.accountRepo.SetModelRateLimit(ctx, account.ID, anthropicFableRateLimitKey, *decision.Until, reason); err != nil {
		slog.Warn("anthropic_fable_scheduling_threshold_set_model_limit_failed",
			"account_id", account.ID,
			"threshold_percent", decision.ThresholdPercent,
			"used_percent", decision.UsedPercent,
			"until", decision.Until.UTC(),
			"error", err)
		return
	}

	slog.Info("anthropic_fable_scheduling_threshold_model_limited",
		"account_id", account.ID,
		"scope", anthropicFableRateLimitKey,
		"threshold_percent", decision.ThresholdPercent,
		"used_percent", decision.UsedPercent,
		"until", decision.Until.UTC())
}

func accountHasSameSchedulingThresholdPause(account *Account, until time.Time, reason string) bool {
	if account == nil || account.TempUnschedulableUntil == nil {
		return false
	}
	if account.TempUnschedulableUntil.UTC().Unix() != until.UTC().Unix() {
		return false
	}

	existing, ok := parseTempUnschedReasonPayload(account.TempUnschedulableReason)
	if !ok || existing.Source != AccountSchedulingThresholdReasonSource {
		return false
	}
	next, ok := parseTempUnschedReasonPayload(reason)
	if !ok || next.Source != AccountSchedulingThresholdReasonSource {
		return false
	}

	existing.TriggeredAtUnix = 0
	next.TriggeredAtUnix = 0
	return existing == next
}
