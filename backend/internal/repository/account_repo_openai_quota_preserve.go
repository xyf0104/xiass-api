package repository

import (
	"context"
	"encoding/json"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

func openAIQuotaManagedExtraKey(key string) bool {
	return service.IsOpenAIQuotaRuntimeExtraKey(key)
}

// Full account edits share the row lock with quota CAS. Their stale Extra must
// not replace runtime state, even when a token or an ordinary setting changes.
func lockAndMergeOpenAIQuotaExtra(ctx context.Context, client *dbent.Client, account *service.Account, extra map[string]any) (map[string]any, error) {
	if account == nil || !account.IsOpenAIOAuth() {
		return extra, nil
	}
	credentials, err := json.Marshal(normalizeJSONMap(account.Credentials))
	if err != nil {
		return nil, service.ErrOpenAIWeeklyStateInvalidInput
	}
	rows, err := client.QueryContext(ctx, `
		SELECT COALESCE(platform = 'openai' AND type = 'oauth' AND (
			credentials = $2::jsonb OR (
				NULLIF(BTRIM(credentials ->> 'chatgpt_account_id'), '') IS NOT NULL
				AND credentials ->> 'chatgpt_account_id' = $2::jsonb ->> 'chatgpt_account_id'
				AND credentials ->> 'chatgpt_user_id' IS NOT DISTINCT FROM $2::jsonb ->> 'chatgpt_user_id'
			)
		), FALSE), extra
		FROM accounts WHERE id = $1 AND deleted_at IS NULL FOR NO KEY UPDATE
	`, account.ID, string(credentials))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return nil, err
		}
		return nil, service.ErrAccountNotFound
	}
	var sameIdentity bool
	var raw []byte
	if err := rows.Scan(&sameIdentity, &raw); err != nil {
		return nil, err
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	var current map[string]any
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &current); err != nil {
			return nil, err
		}
	}
	merged := copyJSONMap(normalizeJSONMap(extra))
	for key := range merged {
		if openAIQuotaManagedExtraKey(key) {
			delete(merged, key)
		}
	}
	if sameIdentity {
		for key, value := range current {
			if openAIQuotaManagedExtraKey(key) {
				merged[key] = value
			}
		}
	}
	return service.MergeOpenAICodexTicketExtra(merged, current), nil
}
