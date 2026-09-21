package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
)

func (r *accountRepository) ListOpenAIAdsPowerBindingAccounts(ctx context.Context, deviceID, environmentKey, legacyNodeID string, limit int) ([]service.OpenAIAdsPowerBindingAccountRecord, bool, error) {
	if r == nil || limit <= 0 {
		return nil, false, errors.New("AdsPower inventory query is unavailable")
	}
	exec := txAwareSQLExecutor(ctx, r.sql, r.client)
	if exec == nil {
		return nil, false, errors.New("account repository SQL executor is unavailable")
	}
	rows, err := exec.QueryContext(ctx, `
		SELECT id, platform, type, parent_account_id, status, error_message, extra, deleted_at IS NOT NULL,
		       COALESCE(NULLIF(BTRIM(extra ->> $3), ''), $4)
		FROM accounts
		WHERE BTRIM(COALESCE(extra -> $1 ->> 'device_id', '')) = $2
		ORDER BY id ASC
		LIMIT $5`,
		service.OpenAIAdsPowerBindingExtraKey, strings.TrimSpace(deviceID), service.AccountExecutionNodeExtraKey,
		strings.TrimSpace(legacyNodeID), limit+1,
	)
	if err != nil {
		return nil, false, fmt.Errorf("query AdsPower inventory: %w", err)
	}
	defer func() { _ = rows.Close() }()

	result := make([]service.OpenAIAdsPowerBindingAccountRecord, 0, limit)
	for rows.Next() {
		var id int64
		var platform, accountType string
		var parentAccountID sql.NullInt64
		var status string
		var errorMessage sql.NullString
		var rawExtra []byte
		var deleted bool
		var executionNodeID string
		if err := rows.Scan(&id, &platform, &accountType, &parentAccountID, &status, &errorMessage, &rawExtra, &deleted, &executionNodeID); err != nil {
			return nil, false, fmt.Errorf("scan AdsPower inventory: %w", err)
		}
		if len(result) >= limit {
			return result, false, nil
		}
		var extra map[string]any
		if err := json.Unmarshal(rawExtra, &extra); err != nil {
			return nil, false, fmt.Errorf("decode AdsPower binding metadata: %w", err)
		}
		binding := service.ParseOpenAIAdsPowerBinding(extra[service.OpenAIAdsPowerBindingExtraKey])
		if binding == nil {
			return nil, false, errors.New("AdsPower binding metadata is incomplete")
		}
		account := service.Account{ID: id, Platform: platform, Type: accountType, Status: status, Extra: extra}
		if parentAccountID.Valid {
			parentID := parentAccountID.Int64
			account.ParentAccountID = &parentID
		}
		if errorMessage.Valid {
			account.ErrorMessage = errorMessage.String
		}
		result = append(result, service.OpenAIAdsPowerBindingAccountRecord{
			Account: account, Binding: *binding, ExecutionNodeID: strings.TrimSpace(executionNodeID), Deleted: deleted,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, false, fmt.Errorf("iterate AdsPower inventory: %w", err)
	}
	return result, true, nil
}

func (r *accountRepository) ReleaseOpenAIAdsPowerProfileBindings(ctx context.Context, deviceID, environmentKey, profileID, legacyNodeID string, blockedCodes []string) ([]int64, bool, error) {
	if r == nil || r.client == nil {
		return nil, false, errors.New("AdsPower binding release is unavailable")
	}
	tx, err := r.client.Tx(ctx)
	if err != nil {
		return nil, false, err
	}
	rollback := func() { _ = tx.Rollback() }
	defer rollback()
	txCtx := dbent.NewTxContext(ctx, tx)
	exec := txAwareSQLExecutor(txCtx, nil, tx.Client())
	if exec == nil {
		return nil, false, errors.New("account repository transaction is unavailable")
	}
	rows, err := exec.QueryContext(txCtx, `
		SELECT id, platform, type, parent_account_id, status, error_message, extra,
		       COALESCE(NULLIF(BTRIM(extra ->> $4), ''), $5)
		FROM accounts
		WHERE deleted_at IS NULL
		  AND BTRIM(COALESCE(extra -> $1 ->> 'device_id', '')) = $2
		  AND BTRIM(COALESCE(extra -> $1 ->> 'profile_id', '')) = $3
		ORDER BY id ASC
		FOR UPDATE`,
		service.OpenAIAdsPowerBindingExtraKey, strings.TrimSpace(deviceID), strings.TrimSpace(profileID),
		service.AccountExecutionNodeExtraKey, strings.TrimSpace(legacyNodeID),
	)
	if err != nil {
		return nil, false, fmt.Errorf("lock AdsPower bindings: %w", err)
	}
	defer func() { _ = rows.Close() }()
	ids := make([]int64, 0)
	allowed := make(map[string]struct{}, len(blockedCodes))
	for _, code := range blockedCodes {
		allowed[strings.ToLower(strings.TrimSpace(code))] = struct{}{}
	}
	for rows.Next() {
		var id int64
		var platform, accountType string
		var parentAccountID sql.NullInt64
		var status string
		var errorMessage sql.NullString
		var rawExtra []byte
		var executionNodeID string
		if err := rows.Scan(&id, &platform, &accountType, &parentAccountID, &status, &errorMessage, &rawExtra, &executionNodeID); err != nil {
			return nil, false, fmt.Errorf("scan AdsPower binding: %w", err)
		}
		var extra map[string]any
		if err := json.Unmarshal(rawExtra, &extra); err != nil {
			return nil, false, fmt.Errorf("decode AdsPower binding: %w", err)
		}
		account := &service.Account{ID: id, Platform: platform, Type: accountType, Status: status, Extra: extra}
		if parentAccountID.Valid {
			parentID := parentAccountID.Int64
			account.ParentAccountID = &parentID
		}
		if errorMessage.Valid {
			account.ErrorMessage = errorMessage.String
		}
		binding := service.ParseOpenAIAdsPowerBinding(extra[service.OpenAIAdsPowerBindingExtraKey])
		if binding == nil || binding.EnvironmentKey != strings.TrimSpace(environmentKey) || strings.TrimSpace(executionNodeID) != strings.TrimSpace(environmentKey) ||
			account.Platform != service.PlatformOpenAI || account.Type != service.AccountTypeOAuth || account.ParentAccountID != nil ||
			!service.OpenAIAdsPowerBindingExplicitlyBlockedWithCodes(account, binding, allowed) {
			return nil, false, nil
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, false, fmt.Errorf("iterate AdsPower bindings: %w", err)
	}
	if len(ids) == 0 {
		if err := tx.Commit(); err != nil {
			return nil, false, err
		}
		return nil, true, nil
	}
	result, err := exec.ExecContext(txCtx, `
		UPDATE accounts
		SET extra = COALESCE(extra, '{}'::jsonb) - $1, updated_at = NOW()
		WHERE id = ANY($2) AND deleted_at IS NULL
		  AND BTRIM(COALESCE(extra -> $1 ->> 'device_id', '')) = $3
		  AND BTRIM(COALESCE(extra -> $1 ->> 'environment_key', '')) = $4
		  AND BTRIM(COALESCE(extra -> $1 ->> 'profile_id', '')) = $5
		  AND COALESCE(NULLIF(BTRIM(extra ->> $6), ''), $4) = $4`,
		service.OpenAIAdsPowerBindingExtraKey, pq.Array(ids), strings.TrimSpace(deviceID), strings.TrimSpace(environmentKey), strings.TrimSpace(profileID), service.AccountExecutionNodeExtraKey,
	)
	if err != nil {
		return nil, false, fmt.Errorf("clear AdsPower bindings: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil || affected != int64(len(ids)) {
		return nil, false, errors.New("AdsPower binding changed during release")
	}
	for _, id := range ids {
		if err := enqueueSchedulerOutbox(txCtx, exec, service.SchedulerOutboxEventAccountChanged, &id, nil, nil); err != nil {
			return nil, false, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, false, err
	}
	return ids, true, nil
}
