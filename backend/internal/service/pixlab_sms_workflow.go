package service

import (
	"context"
	"database/sql"
	"errors"
	"regexp"
	"strings"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/google/uuid"
)

type pixlabWorkflowContextKey struct{}

var pixlabWorkflowScopePattern = regexp.MustCompile(`^[A-Za-z0-9_-]{16,128}$`)

func pixlabWorkflowScope(ctx context.Context) string {
	scope, _ := ctx.Value(pixlabWorkflowContextKey{}).(string)
	return scope
}

// WorkflowAction is an internal capability: callers must first lock and verify
// the live owned workflow. No public receiver accepts a caller-supplied scope.
// Mutating provider operations additionally require the XIASS modal confirmation.
func (s *PixlabSMSService) WorkflowAction(ctx context.Context, ownerID int64, scope, sessionID, action string, confirmed bool) (*PixlabSMSResult, error) {
	if ownerID <= 0 || !pixlabWorkflowScopePattern.MatchString(scope) {
		return nil, ErrPixlabSMSSession
	}
	if action != "check" && action != "acquire" && action != "cancel" && action != "change" {
		return nil, ErrPixlabSMSSession
	}
	if action != "check" && !confirmed {
		return nil, infraerrors.BadRequest("SMS_CONFIRMATION_REQUIRED", "Confirm the SMS action in XIASS first")
	}
	ctx = context.WithValue(ctx, pixlabWorkflowContextKey{}, scope)
	lock := s.sessionLock("workflow:" + scope)
	lock.Lock()
	defer lock.Unlock()
	switch action {
	case "acquire":
		return s.redeemWorkflow(ctx, ownerID, scope)
	case "check":
		// Workflow codes are never persisted, so the ordinary receiver's
		// terminal-result fallback must not be consulted from this scope.
		return s.withActiveSession(ctx, ownerID, sessionID, "check", false)
	case "cancel":
		return s.Cancel(ctx, ownerID, sessionID)
	case "change":
		result, err := s.Cancel(ctx, ownerID, sessionID)
		if err != nil || (result != nil && pixlabHasVerificationCode(result.Code)) {
			return result, err
		}
		return s.redeemWorkflow(ctx, ownerID, scope)
	}
	return nil, ErrPixlabSMSSession
}

func (s *PixlabSMSService) redeemWorkflow(ctx context.Context, ownerID int64, scope string) (*PixlabSMSResult, error) {
	var existing string
	err := s.db.QueryRowContext(ctx, `SELECT session_id FROM xiass_sms_card_keys
		WHERE owner_user_id = $1 AND workflow_scope = $2 AND status = 'active'`, ownerID, scope).Scan(&existing)
	if err == nil {
		return s.withActiveSession(ctx, ownerID, existing, "resume", false)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	// Exhausted provider keys are bounded by the finite queue, just like Redeem.
	for {
		sessionID := uuid.NewString()
		var encryptedKey string
		err := s.db.QueryRowContext(ctx, `WITH next_key AS (
			SELECT id FROM xiass_sms_card_keys
			WHERE status = 'queued' AND claim_count < $4
			ORDER BY claim_count ASC, queue_rank ASC, last_claimed_at NULLS FIRST, id
			FOR UPDATE SKIP LOCKED LIMIT 1
		)
		UPDATE xiass_sms_card_keys AS card
		SET status = 'active', owner_user_id = $1, session_id = $2, workflow_scope = $3,
			consumed_at = NOW(), last_claimed_at = NOW(),
			cleanup_attempted_at = NULL, cleanup_lease_token = NULL, cleanup_lease_until = NULL,
			claim_count = card.claim_count + 1, updated_at = NOW()
		FROM next_key WHERE card.id = next_key.id RETURNING card.encrypted_key`,
			ownerID, sessionID, scope, PixlabSMSCardKeyMaxClaims).Scan(&encryptedKey)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrPixlabSMSNoCardKey
		}
		if err != nil {
			return nil, err
		}
		key, err := s.encryptor.Decrypt(encryptedKey)
		if err != nil {
			_ = s.releaseSession(ctx, sessionID, ownerID)
			return nil, err
		}
		provider, err := s.callProvider(ctx, "redeem", key)
		if err != nil {
			if isPixlabCardUsageLimitError(err) {
				if err := s.exhaustSession(ctx, sessionID, ownerID); err != nil {
					return nil, err
				}
				continue
			}
			// Ambiguous network failures keep the reservation recoverable. A retry
			// resumes this exact scope instead of spending a second key.
			return nil, err
		}
		if !pixlabHasVerificationCode(provider.Code) && pixlabTerminalStatus(provider.Status, "") == "" {
			if err := s.syncRedeemedSessionStart(ctx, sessionID, ownerID, provider.CreatedAt); err != nil {
				return nil, err
			}
		}
		return s.finishProviderResponse(ctx, sessionID, ownerID, provider, false, false)
	}
}

// WorkflowSession recovers only the reservation of this exact workflow, never
// the administrator's ordinary OAuth receiver or another batch task.
func (s *PixlabSMSService) WorkflowSession(ctx context.Context, ownerID int64, scope string) (string, error) {
	if ownerID <= 0 || !pixlabWorkflowScopePattern.MatchString(scope) {
		return "", ErrPixlabSMSSession
	}
	var id string
	err := s.db.QueryRowContext(ctx, `SELECT session_id FROM xiass_sms_card_keys
		WHERE owner_user_id = $1 AND workflow_scope = $2 AND status = 'active'`, ownerID, scope).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return strings.TrimSpace(id), err
}
