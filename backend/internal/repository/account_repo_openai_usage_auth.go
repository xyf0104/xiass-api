package repository

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

var _ service.OpenAIUsageAuthRepository = (*accountRepository)(nil)

const updateOpenAIUsageAuthQuery = `
	WITH updated AS (
		UPDATE accounts AS a
		SET temp_unschedulable_until = CASE
				WHEN $1 THEN CASE
					WHEN a.temp_unschedulable_until > NOW()
						THEN GREATEST(a.temp_unschedulable_until, NOW() + INTERVAL '10 minutes')
					ELSE NOW() + INTERVAL '10 minutes'
				END
			ELSE NULL
		END,
		temp_unschedulable_reason = CASE WHEN $1 THEN $2 ELSE NULL END
		WHERE a.id = $3
			AND a.deleted_at IS NULL
			AND a.platform = $4
			AND a.type = $5
			AND a.credentials = $6::jsonb
			AND a.proxy_id IS NOT DISTINCT FROM $7
			AND a.parent_account_id IS NULL
			AND a.quota_dimension = 'global'
			AND (
				($1 AND (
					a.temp_unschedulable_until IS NULL
					OR a.temp_unschedulable_until <= NOW()
					OR LOWER(BTRIM(COALESCE(a.temp_unschedulable_reason, ''))) = 'oauth_401'
					OR LEFT(LOWER(BTRIM(COALESCE(a.temp_unschedulable_reason, ''))), 10) = 'oauth_401:'
					OR LOWER(BTRIM(COALESCE(a.temp_unschedulable_reason, ''))) LIKE 'oauth 401:%'
					OR LOWER(BTRIM(COALESCE(a.temp_unschedulable_reason, ''))) LIKE 'authentication failed (401)%'
				))
				OR (NOT $1 AND a.temp_unschedulable_reason = $2)
			)
		RETURNING a.id
	)
	INSERT INTO scheduler_outbox (event_type, account_id, group_id, payload)
	SELECT $8, updated.id, NULL, NULL FROM updated
`

// UpdateOpenAIUsageAuthIfIdentityMatches applies one bounded authentication
// observation only while the complete OpenAI OAuth request identity is still
// current. The account mutation and scheduler invalidation commit together.
func (r *accountRepository) UpdateOpenAIUsageAuthIfIdentityMatches(
	ctx context.Context,
	expected *service.Account,
	unauthorized bool,
) (bool, error) {
	if expected == nil || expected.ID <= 0 || !expected.IsOpenAIOAuth() ||
		expected.IsOpenAIAgentIdentity() || expected.IsCredentialShadow() ||
		expected.QuotaDimensionOrDefault() != service.QuotaDimensionGlobal {
		return false, errors.New("OpenAI usage authentication update requires a global OpenAI OAuth credential snapshot")
	}
	accessToken, ok := expected.Credentials["access_token"].(string)
	if !ok || strings.TrimSpace(accessToken) == "" {
		return false, errors.New("OpenAI usage authentication update requires an access token snapshot")
	}
	credentials, err := json.Marshal(normalizeJSONMap(expected.Credentials))
	if err != nil {
		return false, errors.New("OpenAI usage authentication credentials are not JSON-encodable")
	}

	var exec sqlExecutor
	transactional := false
	if tx := dbent.TxFromContext(ctx); tx != nil {
		exec = tx.Client()
		transactional = true
	} else if r != nil {
		exec = r.sql
	}
	if exec == nil {
		return false, errors.New("account repository SQL executor is not configured")
	}

	result, err := exec.ExecContext(ctx, updateOpenAIUsageAuthQuery,
		unauthorized,
		service.OpenAIUsageUnauthorizedReason,
		expected.ID,
		expected.Platform,
		expected.Type,
		string(credentials),
		expected.ProxyID,
		service.SchedulerOutboxEventAccountChanged,
	)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	if affected == 0 {
		return false, nil
	}
	if !transactional {
		r.syncSchedulerAccountSnapshotDetached(ctx, expected.ID)
	}
	return true, nil
}
