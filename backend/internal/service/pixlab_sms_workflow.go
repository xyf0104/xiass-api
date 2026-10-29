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
	unlock, err := s.workflowLock(scope).lock(ctx)
	if err != nil {
		return nil, err
	}
	defer unlock()
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
		// A confirmed retry may outlive the canceled/expired reservation. Only
		// an absent ID can recover; never treat an inaccessible ID as absent.
		if sessionID == "" {
			return s.redeemWorkflow(ctx, ownerID, scope)
		}
		provider, providerErr := s.activeSessionProvider(ctx, ownerID, sessionID, scope)
		if providerErr != nil {
			return nil, providerErr
		}
		if provider == SMSProviderGiema {
			result, err := s.withActiveSession(ctx, ownerID, sessionID, "change", false)
			if err == nil && result != nil && (result.Status == "EXHAUSTED" || result.Status == "EXPIRED") {
				return s.redeemWorkflow(ctx, ownerID, scope)
			}
			return result, err
		}
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
		result, resumeErr := s.withActiveSession(ctx, ownerID, existing, "resume", false)
		if resumeErr != nil || result == nil || result.SessionID != "" || pixlabHasVerificationCode(result.Code) || pixlabTerminalStatus(result.Status, "") == "" {
			return result, resumeErr
		}
		// A provider-confirmed terminal reservation has already been settled.
		// Continue this same confirmed acquisition with the next queued card.
	} else if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	for attempt := 0; attempt < pixlabSMSMaxAcquireAttempts; attempt++ {
		sessionID := uuid.NewString()
		var encryptedKey string
		err := s.db.QueryRowContext(ctx, `WITH next_key AS (
			SELECT id FROM xiass_sms_card_keys
			WHERE status = 'queued'
			ORDER BY claim_count ASC, queue_rank ASC, last_claimed_at NULLS FIRST, id
			FOR UPDATE SKIP LOCKED LIMIT 1
		)
		UPDATE xiass_sms_card_keys AS card
		SET status = 'active', owner_user_id = $1, session_id = $2, workflow_scope = $3,
			consumed_at = NOW(), last_claimed_at = NOW(),
			cleanup_attempted_at = NULL, cleanup_lease_token = NULL, cleanup_lease_until = NULL,
			claim_count = card.claim_count + 1, updated_at = NOW()
		FROM next_key WHERE card.id = next_key.id RETURNING card.encrypted_key`,
			ownerID, sessionID, scope).Scan(&encryptedKey)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrPixlabSMSNoCardKey
		}
		if err != nil {
			return nil, err
		}
		providerName, key, err := s.decodeSMSCardKey(encryptedKey)
		if err != nil {
			_ = s.releaseSession(ctx, sessionID, ownerID)
			return nil, err
		}
		if providerName == SMSProviderGiema {
			result, giemaErr := s.runGiemaSession(ctx, ownerID, sessionID, key, "redeem", false, false)
			if giemaErr != nil {
				if isGiemaCardUnusableError(giemaErr) {
					if settleErr := s.settleGiemaSession(ctx, sessionID, ownerID, false, pixlabSettlementExhaust, nil); settleErr != nil {
						return nil, settleErr
					}
					continue
				}
				return nil, giemaErr
			}
			if result != nil && result.SessionID == "" && !pixlabHasVerificationCode(result.Code) && pixlabTerminalStatus(result.Status, "") != "" {
				continue
			}
			return result, nil
		}
		provider, err := s.callProvider(ctx, "redeem", key)
		if err != nil {
			if isPixlabCardUnusableError(err) {
				if err := s.exhaustSession(ctx, sessionID, ownerID); err != nil {
					return nil, err
				}
				continue
			}
			if isPixlabProviderSessionExpiredError(err) {
				if releaseErr := s.releaseSession(ctx, sessionID, ownerID); releaseErr != nil {
					return nil, releaseErr
				}
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
	return nil, ErrPixlabSMSAttemptLimit
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
