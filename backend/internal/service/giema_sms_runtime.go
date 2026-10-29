package service

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

type giemaSMSPersistedState struct {
	Version         int               `json:"version"`
	Session         string            `json:"session,omitempty"`
	Latest          *GiemaSMSResponse `json:"latest,omitempty"`
	InFlightAction  string            `json:"in_flight_action,omitempty"`
	ExpectedAttempt *int              `json:"expected_attempt,omitempty"`
	PollSeconds     int               `json:"poll_seconds,omitempty"`
	RetryNotBefore  time.Time         `json:"retry_not_before,omitempty"`
	RotateNotBefore time.Time         `json:"rotate_not_before,omitempty"`
	UpdatedAt       time.Time         `json:"updated_at"`
}

func emptyGiemaSMSState() *giemaSMSPersistedState {
	return &giemaSMSPersistedState{Version: 1}
}

func (s *PixlabSMSService) decodeSMSCardKey(encrypted string) (string, string, error) {
	if s == nil || s.encryptor == nil {
		return "", "", infraerrors.InternalServer("SMS_RECEIVER_UNAVAILABLE", "接码服务尚未初始化")
	}
	plaintext, err := s.encryptor.Decrypt(encrypted)
	if err != nil {
		return "", "", infraerrors.InternalServer("SMS_CARD_KEY_UNAVAILABLE", "无法读取接码卡密")
	}
	if strings.HasPrefix(plaintext, giemaSMSCardEnvelopePrefix) {
		card := strings.TrimPrefix(plaintext, giemaSMSCardEnvelopePrefix)
		if card == "" {
			return "", "", infraerrors.InternalServer("SMS_CARD_KEY_UNAVAILABLE", "接码卡密绑定无效")
		}
		return SMSProviderGiema, card, nil
	}
	return SMSProviderPixlab, plaintext, nil
}

func (s *PixlabSMSService) activeSessionProvider(ctx context.Context, ownerID int64, sessionID, workflowScope string) (string, error) {
	var provider string
	err := s.db.QueryRowContext(ctx, `
		SELECT provider
		FROM xiass_sms_card_keys
		WHERE session_id = $1 AND owner_user_id = $2 AND status = 'active' AND workflow_scope = $3`,
		sessionID, ownerID, workflowScope).Scan(&provider)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrPixlabSMSSession
	}
	if err != nil {
		return "", fmt.Errorf("load sms session provider: %w", err)
	}
	return strings.TrimSpace(provider), nil
}

func (s *PixlabSMSService) giemaState(ctx context.Context, sessionID string, ownerID int64) (*giemaSMSPersistedState, bool, error) {
	var (
		provider        string
		encryptedState  string
		providerPending bool
	)
	err := s.db.QueryRowContext(ctx, `
		SELECT provider, provider_state, provider_pending
		FROM xiass_sms_card_keys
		WHERE session_id = $1 AND owner_user_id = $2 AND status = 'active'`, sessionID, ownerID).
		Scan(&provider, &encryptedState, &providerPending)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, ErrPixlabSMSSession
	}
	if err != nil {
		return nil, false, fmt.Errorf("load giema sms card state: %w", err)
	}
	if provider != SMSProviderGiema {
		return nil, false, infraerrors.Conflict("SMS_CARD_KEY_PROVIDER_CONFLICT", "接码卡密供应商绑定不一致")
	}
	state := emptyGiemaSMSState()
	if strings.TrimSpace(encryptedState) != "" {
		plaintext, err := s.encryptor.Decrypt(encryptedState)
		if err != nil || json.Unmarshal([]byte(plaintext), state) != nil || state.Version != 1 {
			return nil, false, infraerrors.InternalServer("SMS_PROVIDER_STATE_UNAVAILABLE", "接码供应商会话状态无效，请稍后重试")
		}
		if state.Latest != nil {
			if state.Session == "" {
				state.Session = state.Latest.Session.Token
			}
			state.Latest.Session.Token = state.Session
		}
	}
	return state, providerPending, nil
}

func (s *PixlabSMSService) saveGiemaState(ctx context.Context, sessionID string, ownerID int64, state *giemaSMSPersistedState) error {
	if state == nil {
		return infraerrors.InternalServer("SMS_PROVIDER_STATE_UNAVAILABLE", "接码供应商会话状态无效，请稍后重试")
	}
	persisted := giemaSMSStateCopy(state)
	persisted.Version = 1
	redactGiemaSMSStateCode(persisted)
	persisted.UpdatedAt = time.Now().UTC()
	payload, err := json.Marshal(persisted)
	if err != nil {
		return infraerrors.InternalServer("SMS_PROVIDER_STATE_UNAVAILABLE", "无法保存接码供应商会话状态")
	}
	encrypted, err := s.encryptor.Encrypt(string(payload))
	if err != nil || encrypted == "" {
		return infraerrors.InternalServer("SMS_PROVIDER_STATE_UNAVAILABLE", "无法加密接码供应商会话状态")
	}
	result, err := s.db.ExecContext(ctx, `
		UPDATE xiass_sms_card_keys
		SET provider_state = $3, updated_at = NOW()
		WHERE session_id = $1 AND owner_user_id = $2 AND status = 'active' AND provider = 'giema'`, sessionID, ownerID, encrypted)
	if err != nil {
		return infraerrors.InternalServer("SMS_PROVIDER_STATE_UNAVAILABLE", "无法保存接码供应商会话状态")
	}
	updated, err := result.RowsAffected()
	if err != nil || updated != 1 {
		return ErrPixlabSMSSession
	}
	return nil
}

func (s *PixlabSMSService) withGiemaSessionLock(ctx context.Context, sessionID string, fn func() (*PixlabSMSResult, error)) (*PixlabSMSResult, error) {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, infraerrors.ServiceUnavailable("SMS_PROVIDER_UNAVAILABLE", "接码服务暂时无法连接，请稍后重试")
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, `SELECT pg_advisory_lock(hashtext($1))`, sessionID); err != nil {
		_ = conn.Raw(func(any) error { return driver.ErrBadConn })
		return nil, infraerrors.ServiceUnavailable("SMS_PROVIDER_UNAVAILABLE", "接码服务暂时无法锁定当前会话，请稍后重试")
	}
	defer func() {
		unlockCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if _, err := conn.ExecContext(unlockCtx, `SELECT pg_advisory_unlock(hashtext($1))`, sessionID); err != nil {
			// A session lock must not return to the pool on a connection whose
			// unlock failed. Closing the physical connection releases the lock.
			_ = conn.Raw(func(any) error { return driver.ErrBadConn })
		}
	}()
	return fn()
}

func (s *PixlabSMSService) giemaClient(ctx context.Context) (*GiemaSMSClient, error) {
	key, err := s.GiemaAPIKey(ctx)
	if err != nil {
		return nil, err
	}
	baseURL := strings.TrimRight(s.giemaBaseURL, "/")
	if baseURL == "" {
		baseURL = giemaSMSAPIBaseURL
	}
	client, err := newGiemaSMSClient(key, baseURL, s.client)
	if err != nil {
		return nil, infraerrors.ServiceUnavailable("SMS_PROVIDER_CONFIGURATION_UNAVAILABLE", "Giema 接码供应商配置不可用")
	}
	return client, nil
}

func giemaSMSStateCopy(state *giemaSMSPersistedState) *giemaSMSPersistedState {
	copyState := *state
	if state.Latest != nil {
		latest := *state.Latest
		latest.Session.Token = state.Latest.Session.Token
		copyState.Latest = &latest
	}
	return &copyState
}

func redactGiemaSMSStateCode(state *giemaSMSPersistedState) {
	if state != nil && state.Latest != nil {
		state.Latest.Code = ""
	}
}

func giemaSMSActionForRequest(action string, state *giemaSMSPersistedState) string {
	if state != nil && (state.InFlightAction == "start" || state.InFlightAction == "rotate") {
		return "status"
	}
	switch action {
	case "redeem", "resume":
		if state != nil && (state.Session != "" || state.Latest != nil) {
			return "status"
		}
		return "start"
	case "change":
		if state != nil && state.Latest != nil && giemaSMSReplaceReady(state.Latest) {
			// The provider UI restarts the same card session after replace_ready;
			// rotate is rejected once the previous number has already ended.
			if state.Session != "" {
				return "start"
			}
			return "status"
		}
		return "rotate"
	default:
		return "status"
	}
}

func giemaSMSResponseNumber(response *GiemaSMSResponse) string {
	if response == nil {
		return ""
	}
	if strings.TrimSpace(response.Code) != "" {
		return response.phone()
	}
	return response.Number()
}

func giemaSMSResultFromResponse(response *GiemaSMSResponse, state *giemaSMSPersistedState, sessionID string, pending bool, queued int64) *PixlabSMSResult {
	status := strings.ToUpper(strings.TrimSpace(response.Status))
	if status == "" {
		status = "WAITING"
	}
	if (giemaSMSTrue(response.CodePending) || response.WaitingForCancellation()) &&
		(status == "OK" || status == "EXHAUSTED" || status == "DISABLED" || status == "REPLACE_READY") {
		status = "WAITING"
	}
	number := giemaSMSResponseNumber(response)
	if pending && strings.TrimSpace(response.Code) == "" {
		number = ""
	}
	return &PixlabSMSResult{
		SessionID:     sessionID,
		Status:        status,
		Number:        number,
		NumberPending: strings.TrimSpace(response.Code) == "" && number == "",
		Code:          response.Code,
		PollSeconds:   state.PollSeconds,
		LocalStop:     pending && strings.TrimSpace(response.Code) == "",
		QueuedCount:   queued,
		ServerTime:    time.Now().UTC(),
		provider:      SMSProviderGiema,
	}
}

func giemaSMSCodeProviderResponse(response *GiemaSMSResponse) *pixlabSMSProviderResponse {
	return &pixlabSMSProviderResponse{
		Success: json.RawMessage("true"),
		Status:  "RECEIVED",
		Number:  response.phone(),
		Code:    response.Code,
	}
}

func giemaSMSCardUnusable(response *GiemaSMSResponse) bool {
	if response == nil {
		return false
	}
	if giemaSMSTrue(response.CodePending) || response.WaitingForCancellation() {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(response.Status)) {
	case "exhausted", "disabled":
		return true
	default:
		return false
	}
}

func giemaSMSConsumedWithoutCode(response *GiemaSMSResponse) bool {
	return response != nil && strings.TrimSpace(response.Code) == "" &&
		giemaSMSTrue(response.Consumed) && !giemaSMSTrue(response.CodePending) && !response.WaitingForCancellation()
}

func giemaSMSReplaceReady(response *GiemaSMSResponse) bool {
	return response != nil && !giemaSMSTrue(response.CodePending) && !response.WaitingForCancellation() &&
		strings.EqualFold(strings.TrimSpace(response.Status), "replace_ready")
}

func (s *PixlabSMSService) giemaProviderError(err error) error {
	var providerErr *GiemaSMSError
	if !errors.As(err, &providerErr) {
		return infraerrors.ServiceUnavailable("SMS_PROVIDER_UNAVAILABLE", "接码服务暂时无法完成该操作，请稍后重试")
	}
	switch providerErr.Reason {
	case GiemaSMSReasonCardUnusable:
		return infraerrors.BadRequest("SMS_PROVIDER_CARD_UNUSABLE", "接码卡密已不可用，请更换卡密")
	case GiemaSMSReasonAttemptLimit:
		return infraerrors.Conflict("SMS_PROVIDER_ATTEMPT_LIMIT", "当前号码已达到供应商换号上限，请继续使用当前号码")
	case GiemaSMSReasonNoSession:
		return infraerrors.Conflict("SMS_PROVIDER_SESSION_NOT_FOUND", "接码供应商会话不存在，请继续查询当前号码")
	}
	switch providerErr.Kind {
	case GiemaSMSAuthentication:
		return infraerrors.ServiceUnavailable("SMS_PROVIDER_AUTH_FAILED", "接码服务认证失败，请联系管理员")
	case GiemaSMSRateLimited:
		return infraerrors.ServiceUnavailable("SMS_PROVIDER_RATE_LIMITED", "接码服务请求过于频繁，请稍后重试")
	case GiemaSMSParameters, GiemaSMSInvalidRequest:
		return infraerrors.BadRequest("SMS_PROVIDER_REQUEST_INVALID", "接码服务请求参数无效")
	case GiemaSMSConflict:
		return infraerrors.Conflict("SMS_PROVIDER_CONFLICT", "接码供应商暂时拒绝该操作，请继续查询当前号码")
	case GiemaSMSInvalidResponse:
		return infraerrors.ServiceUnavailable("SMS_PROVIDER_INVALID_RESPONSE", "接码服务返回了无效数据，请稍后重试")
	default:
		return infraerrors.ServiceUnavailable("SMS_PROVIDER_UNAVAILABLE", "接码服务暂时无法完成该操作，请稍后重试")
	}
}

func isGiemaCardUnusableError(err error) bool {
	return infraerrors.Reason(err) == "SMS_PROVIDER_CARD_UNUSABLE"
}

func (s *PixlabSMSService) runGiemaSession(ctx context.Context, ownerUserID int64, sessionID, cardKey, action string, settleMemberFee, persistTerminal bool) (*PixlabSMSResult, error) {
	return s.withGiemaSessionLock(ctx, sessionID, func() (*PixlabSMSResult, error) {
		state, providerPending, err := s.giemaState(ctx, sessionID, ownerUserID)
		if err != nil {
			return nil, err
		}
		if time.Now().Before(state.RetryNotBefore) {
			return nil, infraerrors.ServiceUnavailable("SMS_PROVIDER_RATE_LIMITED", "接码服务请求过于频繁，请稍后重试")
		}
		providerAction := giemaSMSActionForRequest(action, state)
		if providerPending {
			providerAction = "status"
		}
		if providerAction == "rotate" && time.Now().Before(state.RotateNotBefore) {
			return nil, infraerrors.Conflict("SMS_PROVIDER_ROTATE_WAIT", "当前号码尚未到供应商允许的换号时间，请稍后重试")
		}
		client, err := s.giemaClient(ctx)
		if err != nil {
			return nil, err
		}
		if state.PollSeconds == 0 {
			config, configErr := client.Config(ctx)
			if configErr != nil {
				return nil, s.giemaProviderError(configErr)
			}
			state.PollSeconds = *config.PollSeconds
			if config.Maintenance != nil && *config.Maintenance {
				return nil, infraerrors.ServiceUnavailable("SMS_PROVIDER_MAINTENANCE", "Giema 接码服务正在维护")
			}
		}

		intent := giemaSMSStateCopy(state)
		if providerAction != "status" || intent.InFlightAction == "" {
			intent.InFlightAction = providerAction
			intent.ExpectedAttempt = nil
			if providerAction == "rotate" && state.Latest != nil {
				intent.ExpectedAttempt = state.Latest.Attempt
			}
		}
		if err := s.saveGiemaState(ctx, sessionID, ownerUserID, intent); err != nil {
			return nil, err
		}

		var response *GiemaSMSResponse
		switch providerAction {
		case "start":
			if state.Session != "" {
				response, err = client.Start(ctx, cardKey, GiemaSMSSession{Token: state.Session})
			} else {
				response, err = client.Start(ctx, cardKey)
			}
		case "rotate":
			if state.Latest == nil {
				err = &GiemaSMSError{Kind: GiemaSMSInvalidRequest}
			} else {
				previous := *state.Latest
				if !state.RotateNotBefore.IsZero() {
					previous.SwitchInSeconds = nil
				}
				response, err = client.Rotate(ctx, cardKey, &previous)
			}
		default:
			if state.Session != "" {
				response, err = client.Status(ctx, cardKey, GiemaSMSSession{Token: state.Session})
			} else {
				response, err = client.Status(ctx, cardKey)
			}
		}
		if err != nil {
			var providerErr *GiemaSMSError
			if errors.As(err, &providerErr) && providerErr.Ambiguous {
				return nil, s.giemaProviderError(err)
			}
			cleared := giemaSMSStateCopy(state)
			if providerErr != nil && providerErr.Reason == GiemaSMSReasonCardUnusable {
				cleared.InFlightAction = ""
				cleared.ExpectedAttempt = nil
				if clearErr := s.saveGiemaState(ctx, sessionID, ownerUserID, cleared); clearErr != nil {
					return nil, clearErr
				}
				if settleErr := s.settleGiemaSession(ctx, sessionID, ownerUserID, settleMemberFee, pixlabSettlementExhaust, nil); settleErr != nil {
					return nil, settleErr
				}
				queue, queueErr := s.queueStatus(ctx, ownerUserID)
				if queueErr != nil {
					return nil, queueErr
				}
				return &PixlabSMSResult{
					Status:      "EXHAUSTED",
					QueuedCount: queue.QueuedCount,
					ServerTime:  time.Now().UTC(),
					provider:    SMSProviderGiema,
				}, nil
			}
			if providerErr != nil && providerErr.Reason == GiemaSMSReasonNoSession {
				cleared.Session = ""
				cleared.Latest = nil
			}
			if providerAction != "status" || (providerErr != nil && providerErr.Reason == GiemaSMSReasonNoSession) {
				cleared.InFlightAction = ""
				cleared.ExpectedAttempt = nil
			}
			if providerErr != nil && providerErr.HasRetryAfter {
				cleared.RetryNotBefore = time.Now().Add(providerErr.RetryAfter)
			}
			if clearErr := s.saveGiemaState(ctx, sessionID, ownerUserID, cleared); clearErr != nil {
				return nil, clearErr
			}
			return nil, s.giemaProviderError(err)
		}

		completed := giemaSMSStateCopy(state)
		if response.Session.Token != "" {
			completed.Session = response.Session.Token
		}
		response.Session.Token = completed.Session
		completed.Latest = response
		completed.InFlightAction = ""
		completed.ExpectedAttempt = nil
		completed.RetryNotBefore = time.Time{}
		if response.SwitchInSeconds != nil {
			completed.RotateNotBefore = time.Now().Add(giemaSMSDelay(*response.SwitchInSeconds))
		}
		if err := s.saveGiemaState(ctx, sessionID, ownerUserID, completed); err != nil {
			return nil, err
		}

		if strings.TrimSpace(response.Code) != "" {
			result, finishErr := s.finishProviderResponse(ctx, sessionID, ownerUserID, giemaSMSCodeProviderResponse(response), settleMemberFee, persistTerminal)
			if finishErr != nil {
				return nil, finishErr
			}
			result.provider = SMSProviderGiema
			result.Number = response.phone()
			result.PollSeconds = completed.PollSeconds
			result.NumberPending = false
			return result, nil
		}

		queue, err := s.queueStatus(ctx, ownerUserID)
		if err != nil {
			return nil, err
		}
		result := giemaSMSResultFromResponse(response, completed, sessionID, providerPending, queue.QueuedCount)
		if giemaSMSCardUnusable(response) || giemaSMSConsumedWithoutCode(response) {
			if err := s.settleGiemaSession(ctx, sessionID, ownerUserID, settleMemberFee, pixlabSettlementExhaust, nil); err != nil {
				return nil, err
			}
			result.SessionID = ""
			result.Status = "EXHAUSTED"
			result.LocalStop = false
			result.NumberPending = false
			return result, nil
		}
		if giemaSMSReplaceReady(response) {
			if err := s.settleGiemaSession(ctx, sessionID, ownerUserID, settleMemberFee, pixlabSettlementRelease, nil); err != nil {
				return nil, err
			}
			result.SessionID = ""
			result.Status = "EXPIRED"
			result.LocalStop = false
			result.NumberPending = false
			return result, nil
		}
		if action == "cancel" {
			if err := s.quarantineGiemaSession(ctx, sessionID, ownerUserID); err != nil {
				return nil, err
			}
			result.SessionID = ""
			result.Status = "CANCELLED"
			result.Number = ""
			result.LocalStop = true
			result.NumberPending = true
		}
		return result, nil
	})
}

func giemaSMSDelay(seconds int) time.Duration {
	const maxSeconds = int64((1<<63 - 1) / time.Second)
	if int64(seconds) > maxSeconds {
		return time.Duration(maxSeconds) * time.Second
	}
	return time.Duration(seconds) * time.Second
}

func (s *PixlabSMSService) quarantineGiemaSession(ctx context.Context, sessionID string, ownerUserID int64) error {
	result, err := s.db.ExecContext(ctx, `
		UPDATE xiass_sms_card_keys
		SET provider_pending = true, updated_at = NOW()
		WHERE session_id = $1 AND owner_user_id = $2 AND status = 'active' AND provider = 'giema'`, sessionID, ownerUserID)
	if err != nil {
		return infraerrors.InternalServer("SMS_PROVIDER_STATE_UNAVAILABLE", "无法暂停接码供应商会话")
	}
	updated, err := result.RowsAffected()
	if err != nil || updated != 1 {
		return ErrPixlabSMSSession
	}
	return nil
}
