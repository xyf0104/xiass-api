package repository

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

var _ service.OpenAIReauthorizationAutoRepository = (*accountRepository)(nil)

const openAIReauthorizationStateKey = service.OpenAIReauthorizationStateExtraKey

// Automatic reauthorization is claimed in PostgreSQL, rather than in process
// memory. The claim is bound to the complete OAuth identity and the current
// episode key, so api/api2 cannot both open a browser for one 401 episode.
const claimOpenAIReauthorizationAutoQuery = `
WITH updated AS (
	UPDATE accounts AS a
	SET extra = COALESCE(a.extra, '{}'::jsonb) || jsonb_build_object($1::text, $2::jsonb),
		updated_at = NOW()
	WHERE a.id = $4
		AND a.deleted_at IS NULL
		AND a.platform = $5
		AND a.type = $6
		AND a.credentials = $7::jsonb
		AND a.proxy_id IS NOT DISTINCT FROM $8
		AND a.parent_account_id IS NULL
		AND a.quota_dimension = 'global'
		AND (LOWER(BTRIM(COALESCE(a.temp_unschedulable_reason, ''))) = 'oauth_401'
			OR LEFT(LOWER(BTRIM(COALESCE(a.temp_unschedulable_reason, ''))), 10) = 'oauth_401:'
			OR LOWER(BTRIM(COALESCE(a.temp_unschedulable_reason, ''))) LIKE 'oauth 401:%'
			OR LOWER(BTRIM(COALESCE(a.temp_unschedulable_reason, ''))) LIKE 'authentication failed (401)%')
		AND NOT (
			a.extra -> $1 ->> 'last_initiator' = 'automatic'
			AND a.extra -> $1 ->> 'auto_episode_key' = $3
			AND (
				a.extra -> $1 ->> 'last_result' IN ('success', 'failed', 'blocked', 'canceled', 'running')
			)
		)
	RETURNING a.id
)
INSERT INTO scheduler_outbox (event_type, account_id, group_id, payload)
SELECT $9, updated.id, NULL, NULL FROM updated
`

const finishOpenAIReauthorizationAutoQuery = `
WITH updated AS (
	UPDATE accounts AS a
	SET extra = COALESCE(a.extra, '{}'::jsonb) || jsonb_build_object($1::text, $2::jsonb),
		updated_at = NOW()
	WHERE a.id = $4
		AND a.deleted_at IS NULL
		AND a.platform = $5
		AND a.type = $6
		AND a.credentials = $7::jsonb
		AND a.proxy_id IS NOT DISTINCT FROM $8
		AND a.parent_account_id IS NULL
		AND a.quota_dimension = 'global'
		AND a.extra -> $1 ->> 'last_initiator' = 'automatic'
		AND a.extra -> $1 ->> 'auto_claim_id' = $3
	RETURNING a.id
)
INSERT INTO scheduler_outbox (event_type, account_id, group_id, payload)
SELECT $9, updated.id, NULL, NULL FROM updated
`

const applyOpenAIReauthorizationAutoCredentialsQuery = `
WITH updated AS (
	UPDATE accounts AS a
	SET credentials = $1::jsonb,
		extra = COALESCE(a.extra, '{}'::jsonb) || jsonb_build_object($2::text, $3::jsonb),
		temp_unschedulable_until = NULL,
		temp_unschedulable_reason = NULL,
		schedulable = CASE WHEN $9 THEN TRUE ELSE a.schedulable END,
		status = CASE WHEN $9 THEN $10 ELSE a.status END,
		updated_at = NOW()
	WHERE a.id = $5
		AND a.deleted_at IS NULL
		AND a.platform = $6
		AND a.type = $7
		AND a.credentials = $8::jsonb
		AND a.proxy_id IS NOT DISTINCT FROM $11
		AND a.parent_account_id IS NULL
		AND a.quota_dimension = 'global'
		AND a.extra -> $2 ->> 'last_initiator' = 'automatic'
		AND a.extra -> $2 ->> 'auto_claim_id' = $4
	RETURNING a.id
)
INSERT INTO scheduler_outbox (event_type, account_id, group_id, payload)
SELECT $12, updated.id, NULL, NULL FROM updated
`

func openAIReauthorizationAutoIdentity(expected *service.Account) (string, string, string, any, error) {
	if expected == nil || expected.ID <= 0 || !expected.IsOpenAIOAuth() || expected.IsCredentialShadow() || expected.IsOpenAIOAuthCredentialCopy() || expected.QuotaDimensionOrDefault() != service.QuotaDimensionGlobal {
		return "", "", "", nil, errors.New("automatic OpenAI reauthorization requires a primary OAuth account")
	}
	credentials, err := json.Marshal(normalizeJSONMap(expected.Credentials))
	if err != nil {
		return "", "", "", nil, errors.New("automatic reauthorization credentials are not JSON-encodable")
	}
	return string(credentials), expected.Platform, expected.Type, expected.ProxyID, nil
}

func openAIReauthorizationStateJSON(state service.OpenAIReauthorizationState) (string, error) {
	state.Normalize()
	payload, err := json.Marshal(state)
	if err != nil {
		return "", errors.New("automatic reauthorization state is not JSON-encodable")
	}
	return string(payload), nil
}

func (r *accountRepository) autoReauthorizationExec(ctx context.Context) (sqlExecutor, bool, error) {
	if r == nil {
		return nil, false, errors.New("account repository is nil")
	}
	if tx := dbent.TxFromContext(ctx); tx != nil {
		return tx.Client(), true, nil
	}
	if r.sql == nil {
		return nil, false, errors.New("account repository SQL executor is not configured")
	}
	return r.sql, false, nil
}

func (r *accountRepository) ClaimOpenAIReauthorizationAutoAttempt(ctx context.Context, expected *service.Account, claim service.OpenAIReauthorizationAutoClaim, next service.OpenAIReauthorizationState) (bool, error) {
	credentials, platform, accountType, proxyID, err := openAIReauthorizationAutoIdentity(expected)
	if err != nil {
		return false, err
	}
	stateJSON, err := openAIReauthorizationStateJSON(next)
	if err != nil {
		return false, err
	}
	exec, transactional, err := r.autoReauthorizationExec(ctx)
	if err != nil {
		return false, err
	}
	result, err := exec.ExecContext(ctx, claimOpenAIReauthorizationAutoQuery,
		openAIReauthorizationStateKey, stateJSON, strings.TrimSpace(claim.EpisodeKey), expected.ID,
		platform, accountType, credentials, proxyID, service.SchedulerOutboxEventAccountChanged)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	if affected > 0 && !transactional {
		r.syncSchedulerAccountSnapshotDetached(ctx, expected.ID)
	}
	return affected > 0, nil
}

func (r *accountRepository) FinishOpenAIReauthorizationAutoAttempt(ctx context.Context, expected *service.Account, claim service.OpenAIReauthorizationAutoClaim, next service.OpenAIReauthorizationState) (bool, error) {
	credentials, platform, accountType, proxyID, err := openAIReauthorizationAutoIdentity(expected)
	if err != nil {
		return false, err
	}
	stateJSON, err := openAIReauthorizationStateJSON(next)
	if err != nil {
		return false, err
	}
	exec, transactional, err := r.autoReauthorizationExec(ctx)
	if err != nil {
		return false, err
	}
	result, err := exec.ExecContext(ctx, finishOpenAIReauthorizationAutoQuery,
		openAIReauthorizationStateKey, stateJSON, strings.TrimSpace(claim.ClaimID), expected.ID,
		platform, accountType, credentials, proxyID, service.SchedulerOutboxEventAccountChanged)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	if affected > 0 && !transactional {
		r.syncSchedulerAccountSnapshotDetached(ctx, expected.ID)
	}
	return affected > 0, nil
}

func (r *accountRepository) ApplyOpenAIReauthorizationAutoCredentials(ctx context.Context, expected *service.Account, claim service.OpenAIReauthorizationAutoClaim, credentials map[string]any, next service.OpenAIReauthorizationState) (bool, error) {
	oldCredentials, platform, accountType, proxyID, err := openAIReauthorizationAutoIdentity(expected)
	if err != nil {
		return false, err
	}
	newCredentials, err := json.Marshal(normalizeJSONMap(credentials))
	if err != nil {
		return false, errors.New("new automatic reauthorization credentials are not JSON-encodable")
	}
	stateJSON, err := openAIReauthorizationStateJSON(next)
	if err != nil {
		return false, err
	}
	exec, transactional, err := r.autoReauthorizationExec(ctx)
	if err != nil {
		return false, err
	}
	result, err := exec.ExecContext(ctx, applyOpenAIReauthorizationAutoCredentialsQuery,
		string(newCredentials), openAIReauthorizationStateKey, stateJSON, strings.TrimSpace(claim.ClaimID),
		expected.ID, platform, accountType, oldCredentials, claim.RestoreSchedulable, service.StatusActive,
		proxyID, service.SchedulerOutboxEventAccountChanged)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	if affected > 0 && !transactional {
		r.syncSchedulerAccountSnapshotDetached(ctx, expected.ID)
	}
	return affected > 0, nil
}
