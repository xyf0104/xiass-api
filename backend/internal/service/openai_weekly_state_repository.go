package service

import (
	"context"
	"errors"
	"strings"
)

var ErrOpenAIWeeklyStateInvalidInput = errors.New("invalid OpenAI weekly state CAS input")

// IsOpenAIQuotaRuntimeExtraKey excludes fingerprint and administrator settings.
// These fields are produced by quota observations, never by account form saves.
func IsOpenAIQuotaRuntimeExtraKey(key string) bool {
	return key == "codex_usage_updated_at" ||
		strings.HasPrefix(key, "codex_primary_") ||
		strings.HasPrefix(key, "codex_secondary_") ||
		strings.HasPrefix(key, "codex_5h_") ||
		strings.HasPrefix(key, "codex_7d_") ||
		strings.HasPrefix(key, "codex_reset_credit_") ||
		strings.HasPrefix(key, "codex_credits_snapshot")
}

func stripOpenAIQuotaRuntimeExtra(updates map[string]any) map[string]any {
	if updates == nil {
		return nil
	}
	clean := make(map[string]any, len(updates))
	for key, value := range updates {
		if !IsOpenAIQuotaRuntimeExtraKey(key) {
			clean[key] = value
		}
	}
	return clean
}

// OpenAIWeeklyStateRepository is an optional, state-only extension to the account
// repository. Callers must not fall back to UpdateExtra when it is unavailable.
// It does not implement the reducer, lifecycle transitions, or protection against
// other account-write paths; those remain the caller's integration responsibility.
type OpenAIWeeklyStateRepository interface {
	// CompareAndSwapOpenAIWeeklyState accepts a positive-ID OpenAI OAuth snapshot
	// and a nonempty patch containing codex_7d_estimate_epoch and/or
	// codex_7d_estimate_baseline, plus an optional expected-next revision. The
	// repository assigns/validates that revision for v15 and already-fenced rows.
	// A present nil writes JSON null, not key deletion. Expected.Extra must retain
	// the original presence/value of those keys, codex_7d_estimate_revision and
	// codex_usage_updated_at; nil or absent Extra means tracked keys are missing.
	// Migration 239 also rejects state downgrades/stale observations at the DB boundary.
	// Credentials use JSONB equality
	// (nil means JSON null, not an empty object), and proxy_id must match exactly.
	// Missing/deleted accounts or stale snapshots return false, nil. Invalid input
	// and database errors return false, error. Unrelated fields are preserved and
	// no scheduler/outbox/remote work is performed. In an ent transaction context,
	// true is tentative until the caller commits; this method never commits it.
	CompareAndSwapOpenAIWeeklyState(ctx context.Context, expected *Account, updates map[string]any) (bool, error)
}
