package repository

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

var _ service.OpenAICodexBoundSnapshotRepository = (*accountRepository)(nil)
var _ service.OpenAIQuotaBoundSnapshotRepository = (*accountRepository)(nil)

const updateOpenAICodexBoundSnapshotQuery = updateOpenAICodexSnapshotQuery + `
		AND platform = $4 AND type = $5
		AND credentials = $6::jsonb
		AND proxy_id IS NOT DISTINCT FROM $7
		AND parent_account_id IS NULL
		AND quota_dimension = 'global'
		AND (extra -> 'codex_7d_estimate_revision') IS NOT DISTINCT FROM ($8::jsonb -> 'codex_7d_estimate_revision')
		AND xiass_openai_weekly_raw_write_allowed(extra, COALESCE(extra, '{}'::jsonb) || $1::jsonb, FALSE)
`

// UpdateOpenAICodexSnapshotIfIdentityMatches accepts only runtime quota fields.
// One conditional UPDATE rechecks both identity and observation ordering after
// concurrent reauthorization/refresh writers release the account row lock.
func (r *accountRepository) UpdateOpenAICodexSnapshotIfIdentityMatches(ctx context.Context, expected *service.Account, updates map[string]any) (bool, error) {
	if expected == nil || expected.ID <= 0 || expected.Platform != service.PlatformOpenAI || expected.Type != service.AccountTypeOAuth {
		return false, errors.New("codex snapshot requires an existing OpenAI OAuth request identity")
	}
	if expected.IsShadow() || expected.QuotaDimensionOrDefault() != service.QuotaDimensionGlobal {
		return false, errors.New("spark shadow codex snapshot requires the captured parent request identity; global snapshot write refused")
	}
	if err := validateBoundCodexSnapshotFields(updates, false); err != nil {
		return false, err
	}
	codexUpdates, _, updatedAt, err := splitOpenAICodexSnapshotUpdates(updates)
	if err != nil {
		return false, err
	}
	payload, err := json.Marshal(codexUpdates)
	if err != nil {
		return false, errors.New("bound codex snapshot is not JSON-encodable")
	}
	credentials, err := json.Marshal(expected.Credentials)
	if err != nil {
		return false, errors.New("codex snapshot credentials are not JSON-encodable")
	}
	revision, err := openAIQuotaSnapshotRevisionJSON(expected.Extra)
	if err != nil {
		return false, err
	}
	return r.execBoundCodexSnapshot(ctx, expected.ID, updateOpenAICodexBoundSnapshotQuery,
		string(payload), expected.ID, updatedAt, expected.Platform, expected.Type, string(credentials), expected.ProxyID, revision)
}

// Revision belongs to the captured request, never to the RAW patch. Keep key
// absence distinct from JSON null, and ignore unrelated Extra fields.
func openAIQuotaSnapshotRevisionJSON(extra map[string]any) (string, error) {
	fence := make(map[string]any, 1)
	if revision, present := extra[service.OpenAIWeeklyStateRevisionKey]; present {
		fence[service.OpenAIWeeklyStateRevisionKey] = revision
	}
	payload, err := json.Marshal(fence)
	if err != nil {
		return "", errors.New("quota snapshot revision is not JSON-encodable")
	}
	return string(payload), nil
}

func validateBoundCodexSnapshotFields(updates map[string]any, allowCredits bool) error {
	for key := range updates {
		switch key {
		case "codex_usage_updated_at",
			"codex_primary_used_percent", "codex_primary_reset_after_seconds", "codex_primary_window_minutes",
			"codex_secondary_used_percent", "codex_secondary_reset_after_seconds", "codex_secondary_window_minutes",
			"codex_primary_over_secondary_percent",
			"codex_5h_used_percent", "codex_5h_reset_after_seconds", "codex_5h_window_minutes", "codex_5h_reset_at",
			"codex_7d_used_percent", "codex_7d_reset_after_seconds", "codex_7d_window_minutes", "codex_7d_reset_at":
		case "codex_reset_credit_snapshot", "codex_reset_credit_snapshot_updated_at", "codex_credits_snapshot", "codex_credits_snapshot_updated_at":
			if !allowCredits {
				return errors.New("bound codex snapshot contains a non-runtime quota field")
			}
		default:
			return errors.New("bound codex snapshot contains a non-runtime quota field")
		}
	}
	return nil
}

// Lock the credential row before checking/updating the target. A bare EXISTS
// would read an MVCC parent snapshot and could miss reauth concurrent with a
// shadow UPDATE. NO KEY UPDATE rechecks identity after waiting for writers,
// while allowing concurrent KEY SHARE locks for foreign-key inserts.
var updateOpenAIQuotaBoundSnapshotQuery = `
	WITH credential_identity AS MATERIALIZED (
		SELECT id FROM accounts
		WHERE id = $8 AND deleted_at IS NULL AND platform = $9 AND type = $10
			AND credentials = $11::jsonb AND proxy_id IS NOT DISTINCT FROM $12
			AND parent_account_id IS NULL AND quota_dimension = 'global'
			AND (extra -> 'codex_7d_estimate_revision') IS NOT DISTINCT FROM ($16::jsonb -> 'codex_7d_estimate_revision')
		FOR NO KEY UPDATE
	)
	UPDATE accounts
	SET extra = COALESCE(extra, '{}'::jsonb) || $1::jsonb, updated_at = NOW()
	WHERE id = $2 AND deleted_at IS NULL AND platform = $4 AND type = $5
		AND credentials = $6::jsonb AND proxy_id IS NOT DISTINCT FROM $7
		AND parent_account_id IS NOT DISTINCT FROM $13 AND quota_dimension = $14
		AND (extra -> 'codex_7d_estimate_revision') IS NOT DISTINCT FROM ($15::jsonb -> 'codex_7d_estimate_revision')
		AND EXISTS (SELECT 1 FROM credential_identity)
		AND (NOT ($1::jsonb ? 'codex_usage_updated_at') OR COALESCE(
			(` + openAICodexCurrentTimestampExpression + `)::timestamptz <= $3::timestamptz, TRUE))
		AND (NOT ($1::jsonb ? 'codex_reset_credit_snapshot_updated_at') OR COALESCE(
			(` + strings.ReplaceAll(openAICodexCurrentTimestampExpression, "codex_usage_updated_at", "codex_reset_credit_snapshot_updated_at") + `)::timestamptz <= $3::timestamptz, TRUE))
		AND (NOT ($1::jsonb ? 'codex_credits_snapshot_updated_at') OR COALESCE(
			(` + strings.ReplaceAll(openAICodexCurrentTimestampExpression, "codex_usage_updated_at", "codex_credits_snapshot_updated_at") + `)::timestamptz <= $3::timestamptz, TRUE))
		AND xiass_openai_weekly_raw_write_allowed(extra, COALESCE(extra, '{}'::jsonb) || $1::jsonb, FALSE)
		AND xiass_openai_weekly_raw_write_allowed(extra, COALESCE(extra, '{}'::jsonb) || $1::jsonb, TRUE)
`

func (r *accountRepository) UpdateOpenAIQuotaSnapshotIfIdentityMatches(ctx context.Context, target, credential *service.Account, observedAt time.Time, updates map[string]any) (bool, error) {
	for _, a := range []*service.Account{target, credential} {
		if a == nil || a.ID <= 0 || a.Platform != service.PlatformOpenAI || a.Type != service.AccountTypeOAuth {
			return false, errors.New("quota snapshot requires existing OpenAI OAuth target and credential identities")
		}
	}
	if credential.IsShadow() || credential.QuotaDimensionOrDefault() != service.QuotaDimensionGlobal ||
		(target.IsShadow() && (*target.ParentAccountID != credential.ID || target.ID == credential.ID || target.QuotaDimensionOrDefault() != service.QuotaDimensionSpark)) ||
		(!target.IsShadow() && (target.ID != credential.ID || target.QuotaDimensionOrDefault() != service.QuotaDimensionGlobal)) {
		return false, errors.New("quota snapshot target does not match its credential parent")
	}
	if observedAt.IsZero() || len(updates) == 0 {
		return false, errors.New("quota snapshot requires a response observation time and runtime fields")
	}
	if err := validateBoundCodexSnapshotFields(updates, true); err != nil {
		return false, err
	}
	observedAt = observedAt.UTC().Truncate(time.Microsecond)
	patch := make(map[string]any, len(updates))
	for key, value := range updates {
		patch[key] = value
	}
	for _, key := range []string{"codex_usage_updated_at", "codex_reset_credit_snapshot_updated_at", "codex_credits_snapshot_updated_at"} {
		if raw, present := patch[key]; present {
			value, ok := raw.(string)
			at, err := time.Parse(time.RFC3339Nano, value)
			if !ok || err != nil || !at.UTC().Truncate(time.Microsecond).Equal(observedAt) {
				return false, errors.New("quota snapshot timestamp does not match its captured response observation")
			}
			patch[key] = observedAt.Format(time.RFC3339Nano)
		}
	}
	_, hasRawTime := patch["codex_usage_updated_at"]
	_, hasCredits := patch["codex_reset_credit_snapshot"]
	_, hasCreditsTime := patch["codex_reset_credit_snapshot_updated_at"]
	if hasCredits != hasCreditsTime {
		return false, errors.New("quota credit snapshot requires its observation timestamp")
	}
	_, hasOrdinaryCredits := patch["codex_credits_snapshot"]
	_, hasOrdinaryCreditsTime := patch["codex_credits_snapshot_updated_at"]
	if hasOrdinaryCredits != hasOrdinaryCreditsTime {
		return false, errors.New("ordinary credit snapshot requires its observation timestamp")
	}
	for key := range patch {
		if key != "codex_reset_credit_snapshot" && key != "codex_reset_credit_snapshot_updated_at" && key != "codex_credits_snapshot" && key != "codex_credits_snapshot_updated_at" && !hasRawTime {
			return false, errors.New("raw quota snapshot requires its observation timestamp")
		}
	}
	payload, err := json.Marshal(patch)
	if err != nil {
		return false, errors.New("bound codex snapshot is not JSON-encodable")
	}
	targetCredentials, err := json.Marshal(target.Credentials)
	if err != nil {
		return false, errors.New("codex snapshot credentials are not JSON-encodable")
	}
	requestCredentials, err := json.Marshal(credential.Credentials)
	if err != nil {
		return false, errors.New("codex snapshot credentials are not JSON-encodable")
	}
	targetRevision, err := openAIQuotaSnapshotRevisionJSON(target.Extra)
	if err != nil {
		return false, err
	}
	credentialRevision, err := openAIQuotaSnapshotRevisionJSON(credential.Extra)
	if err != nil {
		return false, err
	}
	return r.execBoundCodexSnapshot(ctx, target.ID, updateOpenAIQuotaBoundSnapshotQuery,
		string(payload), target.ID, observedAt, target.Platform, target.Type, string(targetCredentials), target.ProxyID,
		credential.ID, credential.Platform, credential.Type, string(requestCredentials), credential.ProxyID,
		target.ParentAccountID, target.QuotaDimensionOrDefault(), targetRevision, credentialRevision)
}

func (r *accountRepository) execBoundCodexSnapshot(ctx context.Context, id int64, query string, args ...any) (bool, error) {
	var exec sqlExecutor
	if tx := dbent.TxFromContext(ctx); tx != nil {
		exec = tx.Client()
	} else if r != nil {
		exec = r.sql
	}
	if exec == nil {
		return false, errors.New("account repository SQL executor is not configured")
	}
	result, err := exec.ExecContext(ctx, query, args...)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	if err != nil || affected == 0 {
		return false, err
	}
	if dbent.TxFromContext(ctx) == nil {
		r.syncSchedulerAccountSnapshot(ctx, id)
	}
	return true, nil
}
