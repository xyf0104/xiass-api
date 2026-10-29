package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/google/uuid"
	"github.com/lib/pq"
)

const pixlabSMSAPIBaseURL = "https://sms.pixlab.cc/api"

const (
	// PixlabSMSMemberFee is the first-installation default. Runtime charges are
	// read from the administrator-managed database setting for every new claim.
	PixlabSMSMemberFee = 2.00
	// PixlabSMSMemberMutationDelay protects a newly issued number from being
	// churned immediately. It applies to member changes and cancellations only.
	PixlabSMSMemberMutationDelay = time.Minute
	// PixlabSMSSessionValidity matches the 15-minute number lifetime enforced
	// by Pixlab. It is a local fallback for providers that return an HTTP error
	// instead of a terminal expiry response after the number has expired.
	PixlabSMSSessionValidity = 15 * time.Minute
	// PixlabSMSCleanupInterval keeps abandoned browser sessions from holding a
	// card or a member balance indefinitely.
	PixlabSMSCleanupInterval   = 30 * time.Second
	pixlabSMSTerminalResultTTL = 24 * time.Hour
	pixlabSMSCleanupLeaseTTL   = time.Minute
	// Bound one confirmed acquisition even if a retired card is concurrently re-added.
	pixlabSMSMaxAcquireAttempts = 32
)

var (
	ErrPixlabSMSNoCardKey    = infraerrors.NotFound("SMS_CARD_KEY_UNAVAILABLE", "暂无待用接码卡密")
	ErrPixlabSMSSession      = infraerrors.NotFound("SMS_SESSION_NOT_FOUND", "接码会话不存在或已结束")
	ErrPixlabSMSBalance      = infraerrors.BadRequest("SMS_BALANCE_INSUFFICIENT", "余额不足，无法领取授权接码号码")
	ErrPixlabSMSActive       = infraerrors.Conflict("SMS_ACTIVE_SESSION_EXISTS", "当前已有进行中的接码会话，请先完成、换号或取消")
	ErrPixlabSMSLocked       = infraerrors.Conflict("SMS_SESSION_ACTION_LOCKED", "领取号码后需等待 1 分钟才可换号或取消")
	ErrPixlabSMSAttemptLimit = infraerrors.BadRequest("SMS_CARD_KEY_ATTEMPT_LIMIT", "本次领取尝试的卡密过多，请检查卡密后重试")
)

// PixlabSMSQueueStatus intentionally contains counters only. Plaintext card
// keys are available exclusively from the administrator-only management API.
type PixlabSMSQueueStatus struct {
	QueuedCount int64 `json:"queued_count"`
	ActiveCount int64 `json:"active_count"`
}

// PixlabSMSCardKey is returned only from the administrator's settings API.
// CardKey remains encrypted at rest and is decrypted only for that explicitly
// authenticated management view; member and OAuth endpoints never use it.
type PixlabSMSCardKey struct {
	ID            int64      `json:"id"`
	CardKey       string     `json:"card_key"`
	Provider      string     `json:"provider"`
	Status        string     `json:"status"`
	ClaimCount    int        `json:"claim_count"`
	QueueRank     int64      `json:"queue_rank"`
	LastClaimedAt *time.Time `json:"last_claimed_at,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
}

// PixlabSMSResult is the browser-safe representation of a provider session.
// SessionID is opaque and is the only locally persisted value used for polling.
type PixlabSMSResult struct {
	SessionID     string     `json:"session_id,omitempty"`
	Status        string     `json:"status"`
	Number        string     `json:"number,omitempty"`
	NumberPending bool       `json:"number_pending,omitempty"`
	Country       string     `json:"country,omitempty"`
	Code          string     `json:"code,omitempty"`
	PollSeconds   int        `json:"poll_seconds,omitempty"`
	LocalStop     bool       `json:"local_stop,omitempty"`
	QueuedCount   int64      `json:"queued_count"`
	ExpiresAt     *time.Time `json:"expires_at,omitempty"`
	ServerTime    time.Time  `json:"server_time"`
	provider      string     `json:"-"`
}

// PixlabSMSMemberStatus intentionally omits global card-key queue totals so
// members cannot inspect operator inventory. The price and own balance are
// returned so the client can give an accurate pre-charge explanation.
type PixlabSMSMemberStatus struct {
	QueuedCount int64   `json:"queued_count"`
	ActiveCount int64   `json:"active_count"`
	Available   bool    `json:"available"`
	FeeAmount   float64 `json:"fee_amount"`
	Balance     float64 `json:"balance"`
}

// PixlabSMSMemberResult is the member-safe session response. It contains no
// card key or global queue information and reports how this session's charge
// is currently accounted for: held, captured, or released.
type PixlabSMSMemberResult struct {
	SessionID         string     `json:"session_id,omitempty"`
	Status            string     `json:"status"`
	Number            string     `json:"number,omitempty"`
	NumberPending     bool       `json:"number_pending,omitempty"`
	Country           string     `json:"country,omitempty"`
	Code              string     `json:"code,omitempty"`
	PollSeconds       int        `json:"poll_seconds,omitempty"`
	LocalStop         bool       `json:"local_stop,omitempty"`
	QueuedCount       int64      `json:"queued_count"`
	FeeAmount         float64    `json:"fee_amount"`
	ChargeState       string     `json:"charge_state"`
	Balance           float64    `json:"balance"`
	ActionAvailableAt *time.Time `json:"action_available_at,omitempty"`
	ExpiresAt         *time.Time `json:"expires_at,omitempty"`
	ServerTime        time.Time  `json:"server_time"`
}

// PixlabSMSService owns server-side card-key persistence and provider proxying.
// Card keys remain encrypted at rest and are only decrypted for the explicit
// administrator-only queue-management API.
type PixlabSMSService struct {
	db            *sql.DB
	encryptor     SecretEncryptor
	billingCache  *BillingCacheService
	client        *http.Client
	baseURL       string
	giemaBaseURL  string
	sessionLocks  [64]pixlabSMSContextLock
	workflowLocks [64]pixlabSMSContextLock
	cleanupMu     sync.Mutex
	cleanupStop   context.CancelFunc
	cleanupDone   chan struct{}
}

type pixlabSMSContextLock struct {
	once sync.Once
	gate chan struct{}
}

func (l *pixlabSMSContextLock) lock(ctx context.Context) (func(), error) {
	l.once.Do(func() {
		l.gate = make(chan struct{}, 1)
		l.gate <- struct{}{}
	})
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-l.gate:
		return func() { l.gate <- struct{}{} }, nil
	}
}

func NewPixlabSMSService(db *sql.DB, encryptor SecretEncryptor, billingCache *BillingCacheService) *PixlabSMSService {
	svc := newPixlabSMSService(db, encryptor, &http.Client{Timeout: 20 * time.Second}, pixlabSMSAPIBaseURL)
	svc.billingCache = billingCache
	svc.startCleanupWorker()
	return svc
}

func newPixlabSMSService(db *sql.DB, encryptor SecretEncryptor, client *http.Client, baseURL string) *PixlabSMSService {
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}
	return &PixlabSMSService{
		db:           db,
		encryptor:    encryptor,
		client:       client,
		baseURL:      strings.TrimRight(baseURL, "/"),
		giemaBaseURL: giemaSMSAPIBaseURL,
	}
}

func (s *PixlabSMSService) startCleanupWorker() {
	if s == nil || s.db == nil || s.encryptor == nil {
		return
	}
	s.cleanupMu.Lock()
	defer s.cleanupMu.Unlock()
	if s.cleanupStop != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	s.cleanupStop = cancel
	s.cleanupDone = done
	go func() {
		defer close(done)
		ticker := time.NewTicker(PixlabSMSCleanupInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				cleanupCtx, cleanupCancel := context.WithTimeout(ctx, 20*time.Second)
				if err := s.cleanupExpiredSessions(cleanupCtx); err != nil && !errors.Is(err, context.Canceled) {
					slog.Warn("pixlab_sms_expired_session_cleanup_failed", "error", err)
				}
				cleanupCancel()
			}
		}
	}()
}

func (s *PixlabSMSService) Stop() {
	if s == nil {
		return
	}
	s.cleanupMu.Lock()
	cancel := s.cleanupStop
	done := s.cleanupDone
	s.cleanupStop = nil
	s.cleanupDone = nil
	s.cleanupMu.Unlock()
	if cancel == nil {
		return
	}
	cancel()
	if done != nil {
		<-done
	}
}

func normalizePixlabCardKeys(raw string) []string {
	parts := strings.FieldsFunc(raw, func(r rune) bool {
		switch r {
		case ',', '，', ';', '；', '\n', '\r', '\t', ' ':
			return true
		default:
			return false
		}
	})
	keys := make([]string, 0, len(parts))
	seen := make(map[string]struct{}, len(parts))
	for _, part := range parts {
		key := strings.TrimSpace(part)
		if key == "" || len(key) > 2048 {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		keys = append(keys, key)
	}
	return keys
}

func pixlabCardKeyFingerprint(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])
}

// AddCardKeys stores new keys encrypted at rest. Explicitly re-added exhausted
// cards are requeued without resetting claim history; other duplicates stay put.
func (s *PixlabSMSService) AddCardKeys(ctx context.Context, raw string) (int, *PixlabSMSQueueStatus, error) {
	if s == nil || s.db == nil || s.encryptor == nil {
		return 0, nil, infraerrors.InternalServer("SMS_RECEIVER_UNAVAILABLE", "接码服务尚未初始化")
	}
	keys := normalizePixlabCardKeys(raw)
	if len(keys) == 0 {
		return 0, nil, infraerrors.BadRequest("SMS_CARD_KEY_REQUIRED", "请至少输入一个有效接码卡密")
	}
	if len(keys) > 500 {
		return 0, nil, infraerrors.BadRequest("SMS_CARD_KEY_LIMIT", "单次最多添加 500 个接码卡密")
	}

	inserted := 0
	for _, key := range keys {
		ciphertext, err := s.encryptor.Encrypt(key)
		if err != nil {
			return 0, nil, fmt.Errorf("encrypt sms card key: %w", err)
		}
		var rowID int64
		err = s.db.QueryRowContext(ctx, `
			INSERT INTO xiass_sms_card_keys AS card (encrypted_key, key_fingerprint, queue_rank)
			VALUES (
				$1,
				$2,
				(SELECT COALESCE(MAX(queue_rank), 0) + 1 FROM xiass_sms_card_keys)
			)
			ON CONFLICT (key_fingerprint) DO UPDATE SET
				status = 'queued', queue_rank = EXCLUDED.queue_rank,
				provider_state = '', provider_pending = false,
				owner_user_id = NULL, session_id = NULL, workflow_scope = '',
				consumed_at = NULL, cleanup_attempted_at = NULL,
				cleanup_lease_token = NULL, cleanup_lease_until = NULL, updated_at = NOW()
			WHERE card.provider = 'pixlab' AND card.status = 'exhausted' AND NOT card.provider_pending
			RETURNING id`, ciphertext, pixlabCardKeyFingerprint(key)).Scan(&rowID)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return 0, nil, fmt.Errorf("store sms card key: %w", err)
		}
		inserted++
	}
	status, err := s.queueStatus(ctx, 0)
	if err != nil {
		return 0, nil, err
	}
	return inserted, status, nil
}

func (s *PixlabSMSService) QueueStatus(ctx context.Context, ownerUserID int64) (*PixlabSMSQueueStatus, error) {
	if s == nil || s.db == nil {
		return nil, infraerrors.InternalServer("SMS_RECEIVER_UNAVAILABLE", "接码服务尚未初始化")
	}
	return s.queueStatus(ctx, ownerUserID)
}

func (s *PixlabSMSService) queueStatus(ctx context.Context, ownerUserID int64) (*PixlabSMSQueueStatus, error) {
	status := &PixlabSMSQueueStatus{}
	err := s.db.QueryRowContext(ctx, `
		SELECT
			COUNT(*) FILTER (WHERE status = 'queued'),
			COUNT(*) FILTER (WHERE status = 'active' AND ($1 = 0 OR owner_user_id = $1))
		FROM xiass_sms_card_keys`, ownerUserID).Scan(&status.QueuedCount, &status.ActiveCount)
	if err != nil {
		return nil, fmt.Errorf("count sms card keys: %w", err)
	}
	return status, nil
}

// ClearQueuedCardKeys does not touch an in-progress SMS number. Claimed cards
// remain usable until a code is received, changed, or explicitly cancelled.
func (s *PixlabSMSService) ClearQueuedCardKeys(ctx context.Context) (int64, *PixlabSMSQueueStatus, error) {
	if s == nil || s.db == nil {
		return 0, nil, infraerrors.InternalServer("SMS_RECEIVER_UNAVAILABLE", "接码服务尚未初始化")
	}
	result, err := s.db.ExecContext(ctx, `DELETE FROM xiass_sms_card_keys WHERE status = 'queued'`)
	if err != nil {
		return 0, nil, fmt.Errorf("clear queued sms card keys: %w", err)
	}
	deleted, err := result.RowsAffected()
	if err != nil {
		return 0, nil, fmt.Errorf("read cleared sms card keys: %w", err)
	}
	status, err := s.queueStatus(ctx, 0)
	if err != nil {
		return 0, nil, err
	}
	return deleted, status, nil
}

// ListCardKeys returns plaintext card keys only for the administrator's
// management screen. Keys remain encrypted in PostgreSQL at all times.
func (s *PixlabSMSService) ListCardKeys(ctx context.Context) ([]PixlabSMSCardKey, *PixlabSMSQueueStatus, error) {
	if s == nil || s.db == nil || s.encryptor == nil {
		return nil, nil, infraerrors.InternalServer("SMS_RECEIVER_UNAVAILABLE", "接码服务尚未初始化")
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, encrypted_key, status, claim_count, queue_rank, last_claimed_at, created_at, provider
		FROM xiass_sms_card_keys
		ORDER BY
			CASE status WHEN 'queued' THEN 0 WHEN 'active' THEN 1 ELSE 2 END,
			claim_count ASC,
			queue_rank ASC,
			last_claimed_at NULLS FIRST,
			id ASC`)
	if err != nil {
		return nil, nil, fmt.Errorf("list sms card keys: %w", err)
	}
	defer func() { _ = rows.Close() }()

	keys := make([]PixlabSMSCardKey, 0)
	for rows.Next() {
		var (
			entry       PixlabSMSCardKey
			encrypted   string
			lastClaimed sql.NullTime
		)
		if err := rows.Scan(
			&entry.ID,
			&encrypted,
			&entry.Status,
			&entry.ClaimCount,
			&entry.QueueRank,
			&lastClaimed,
			&entry.CreatedAt,
			&entry.Provider,
		); err != nil {
			return nil, nil, fmt.Errorf("scan sms card key: %w", err)
		}
		provider, plainKey, err := s.decodeSMSCardKey(encrypted)
		if err != nil {
			return nil, nil, err
		}
		if entry.Provider != provider {
			return nil, nil, infraerrors.Conflict("SMS_CARD_KEY_PROVIDER_CONFLICT", "接码卡密供应商绑定不一致")
		}
		entry.CardKey = plainKey
		if lastClaimed.Valid {
			claimedAt := lastClaimed.Time
			entry.LastClaimedAt = &claimedAt
		}
		keys = append(keys, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("iterate sms card keys: %w", err)
	}
	status, err := s.queueStatus(ctx, 0)
	if err != nil {
		return nil, nil, err
	}
	return keys, status, nil
}

// DeleteCardKey removes one operator-selected card. Unresolved Giema sessions
// retain their binding and held charge until provider reconciliation finishes.
// Active Pixlab member sessions are refunded in the deletion transaction.
func (s *PixlabSMSService) DeleteCardKey(ctx context.Context, cardKeyID int64) (*PixlabSMSQueueStatus, error) {
	if s == nil || s.db == nil {
		return nil, infraerrors.InternalServer("SMS_RECEIVER_UNAVAILABLE", "接码服务尚未初始化")
	}
	if cardKeyID <= 0 {
		return nil, infraerrors.BadRequest("SMS_CARD_KEY_ID_INVALID", "卡密编号无效")
	}

	for {
		var (
			preflightStatus  string
			preflightSession sql.NullString
		)
		err := s.db.QueryRowContext(ctx, `
			SELECT status, session_id
			FROM xiass_sms_card_keys
			WHERE id = $1`, cardKeyID).Scan(&preflightStatus, &preflightSession)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, infraerrors.NotFound("SMS_CARD_KEY_NOT_FOUND", "接码卡密不存在或已被删除")
		}
		if err != nil {
			return nil, fmt.Errorf("load sms card key for deletion: %w", err)
		}

		lockedSessionID := ""
		var unlockSession func()
		if preflightStatus == "active" && preflightSession.Valid {
			lockedSessionID = strings.TrimSpace(preflightSession.String)
			if lockedSessionID != "" {
				unlockSession, err = s.sessionLock(lockedSessionID).lock(ctx)
				if err != nil {
					return nil, err
				}
			}
		}

		status, retry, err := s.deleteCardKey(ctx, cardKeyID, lockedSessionID)
		if unlockSession != nil {
			unlockSession()
		}
		if err != nil {
			return nil, err
		}
		if retry {
			continue
		}
		return status, nil
	}
}

func (s *PixlabSMSService) deleteCardKey(ctx context.Context, cardKeyID int64, lockedSessionID string) (*PixlabSMSQueueStatus, bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, false, fmt.Errorf("begin delete sms card key: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var (
		status          string
		ownerUserID     sql.NullInt64
		sessionID       sql.NullString
		provider        string
		providerPending bool
	)
	err = tx.QueryRowContext(ctx, `
		SELECT status, owner_user_id, session_id, provider, provider_pending
		FROM xiass_sms_card_keys
		WHERE id = $1
		FOR UPDATE`, cardKeyID).Scan(&status, &ownerUserID, &sessionID, &provider, &providerPending)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, infraerrors.NotFound("SMS_CARD_KEY_NOT_FOUND", "接码卡密不存在或已被删除")
	}
	if err != nil {
		return nil, false, fmt.Errorf("lock sms card key for deletion: %w", err)
	}
	if provider == SMSProviderGiema && (status == "active" || providerPending) {
		return nil, false, infraerrors.Conflict("SMS_CARD_KEY_PROVIDER_PENDING", "Giema 接码会话尚未结束，请等待供应商确认后再删除")
	}
	activeSessionID := ""
	if status == "active" && sessionID.Valid {
		activeSessionID = strings.TrimSpace(sessionID.String)
	}
	if activeSessionID != "" && activeSessionID != lockedSessionID {
		return nil, true, nil
	}

	refundedUserID := int64(0)
	if status == "active" && ownerUserID.Valid && sessionID.Valid && strings.TrimSpace(sessionID.String) != "" {
		var amount float64
		err = tx.QueryRowContext(ctx, `
			SELECT amount
			FROM xiass_sms_member_charges
			WHERE session_id = $1 AND user_id = $2 AND status = 'held'
			FOR UPDATE`, sessionID.String, ownerUserID.Int64).Scan(&amount)
		switch {
		case errors.Is(err, sql.ErrNoRows):
		// Administrator sessions have no member charge. A captured member
		// charge also remains final and is never refunded here.
		case err != nil:
			return nil, false, fmt.Errorf("lock member sms charge for card deletion: %w", err)
		default:
			if err := execExactlyOne(ctx, tx, `
				UPDATE users
				SET balance = balance + $1, updated_at = NOW()
					WHERE id = $2 AND deleted_at IS NULL`, amount, ownerUserID.Int64); err != nil {
				return nil, false, fmt.Errorf("refund member balance for card deletion: %w", err)
			}
			if err := execExactlyOne(ctx, tx, `
				UPDATE xiass_sms_member_charges
				SET status = 'released', released_at = NOW(), updated_at = NOW()
					WHERE session_id = $1 AND user_id = $2 AND status = 'held'`, sessionID.String, ownerUserID.Int64); err != nil {
				return nil, false, fmt.Errorf("release member sms charge for card deletion: %w", err)
			}
			refundedUserID = ownerUserID.Int64
		}
	}

	if err := execExactlyOne(ctx, tx, `DELETE FROM xiass_sms_card_keys WHERE id = $1`, cardKeyID); err != nil {
		return nil, false, fmt.Errorf("delete sms card key: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, false, fmt.Errorf("commit delete sms card key: %w", err)
	}
	if refundedUserID > 0 {
		s.invalidateBalance(ctx, refundedUserID)
	}
	queue, err := s.queueStatus(ctx, 0)
	return queue, false, err
}

// ReorderCardKeys updates only queued cards. The scheduler still distributes
// claims by usage count first, then uses this operator-defined rank to break
// ties between equally used cards.
func (s *PixlabSMSService) ReorderCardKeys(ctx context.Context, cardKeyIDs []int64) (*PixlabSMSQueueStatus, error) {
	if s == nil || s.db == nil {
		return nil, infraerrors.InternalServer("SMS_RECEIVER_UNAVAILABLE", "接码服务尚未初始化")
	}
	if len(cardKeyIDs) == 0 || len(cardKeyIDs) > 500 {
		return nil, infraerrors.BadRequest("SMS_CARD_KEY_ORDER_INVALID", "请提交 1 到 500 个待用卡密编号")
	}
	seen := make(map[int64]struct{}, len(cardKeyIDs))
	for _, cardKeyID := range cardKeyIDs {
		if cardKeyID <= 0 {
			return nil, infraerrors.BadRequest("SMS_CARD_KEY_ID_INVALID", "卡密编号无效")
		}
		if _, exists := seen[cardKeyID]; exists {
			return nil, infraerrors.BadRequest("SMS_CARD_KEY_ORDER_INVALID", "排序中包含重复卡密")
		}
		seen[cardKeyID] = struct{}{}
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin sms card key reorder: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	for index, cardKeyID := range cardKeyIDs {
		result, err := tx.ExecContext(ctx, `
			UPDATE xiass_sms_card_keys
			SET queue_rank = $1, updated_at = NOW()
			WHERE id = $2 AND status = 'queued'`, int64(index+1), cardKeyID)
		if err != nil {
			return nil, fmt.Errorf("reorder sms card key: %w", err)
		}
		updated, err := result.RowsAffected()
		if err != nil {
			return nil, fmt.Errorf("read reordered sms card key count: %w", err)
		}
		if updated == 0 {
			return nil, infraerrors.Conflict("SMS_CARD_KEY_REORDER_CONFLICT", "待用卡密状态已变化，请刷新后重试")
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit sms card key reorder: %w", err)
	}
	return s.queueStatus(ctx, 0)
}

// MemberFee returns the current administrator-configured member price. Each
// member claim records this value on its own charge row, so later price edits
// never alter an in-progress session's reserve, capture, or refund amount.
func (s *PixlabSMSService) MemberFee(ctx context.Context) (float64, error) {
	if s == nil || s.db == nil {
		return 0, infraerrors.InternalServer("SMS_RECEIVER_UNAVAILABLE", "接码服务尚未初始化")
	}
	var fee float64
	if err := s.db.QueryRowContext(ctx, `
		SELECT member_fee
		FROM xiass_sms_receiver_settings
		WHERE id = 1`).Scan(&fee); err != nil {
		return 0, fmt.Errorf("load sms member fee: %w", err)
	}
	if fee <= 0 || fee > 10000 {
		return 0, infraerrors.InternalServer("SMS_MEMBER_FEE_INVALID", "接码会员价格配置无效")
	}
	return fee, nil
}

// UpdateMemberFee changes the price used by future member claims. Existing
// charge rows retain their originally reserved amount and are never repriced.
func (s *PixlabSMSService) UpdateMemberFee(ctx context.Context, fee float64) (float64, error) {
	if s == nil || s.db == nil {
		return 0, infraerrors.InternalServer("SMS_RECEIVER_UNAVAILABLE", "接码服务尚未初始化")
	}
	if fee <= 0 || fee > 10000 {
		return 0, infraerrors.BadRequest("SMS_MEMBER_FEE_INVALID", "会员接码价格必须在 ¥0.01 到 ¥10000.00 之间")
	}
	fee = float64(int64(fee*100+0.5)) / 100
	if _, err := s.db.ExecContext(ctx, `
		UPDATE xiass_sms_receiver_settings
		SET member_fee = $1, updated_at = NOW()
		WHERE id = 1`, fee); err != nil {
		return 0, fmt.Errorf("update sms member fee: %w", err)
	}
	return fee, nil
}

// MemberStatus exposes only a member's own active-session count and balance.
// Global card-key totals are operator-only data.
func (s *PixlabSMSService) MemberStatus(ctx context.Context, userID int64) (*PixlabSMSMemberStatus, error) {
	if s == nil || s.db == nil {
		return nil, infraerrors.InternalServer("SMS_RECEIVER_UNAVAILABLE", "接码服务尚未初始化")
	}
	if userID <= 0 {
		return nil, infraerrors.Unauthorized("SMS_OWNER_REQUIRED", "无法识别当前用户")
	}
	fee, err := s.MemberFee(ctx)
	if err != nil {
		return nil, err
	}
	status := &PixlabSMSMemberStatus{FeeAmount: fee}
	if err := s.db.QueryRowContext(ctx, `
		SELECT balance
		FROM users
		WHERE id = $1 AND deleted_at IS NULL`, userID).Scan(&status.Balance); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, infraerrors.NotFound("USER_NOT_FOUND", "用户不存在")
		}
		return nil, fmt.Errorf("load sms member balance: %w", err)
	}
	if err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM xiass_sms_card_keys AS card
		WHERE card.owner_user_id = $1 AND card.status = 'active'
			AND EXISTS (
				SELECT 1
				FROM xiass_sms_member_charges AS charge
				WHERE charge.session_id = card.session_id
					AND charge.user_id = $1
					AND charge.status = 'held'
			)`, userID).Scan(&status.ActiveCount); err != nil {
		return nil, fmt.Errorf("count member sms sessions: %w", err)
	}
	if err := s.db.QueryRowContext(ctx, `
		SELECT EXISTS(
			SELECT 1
			FROM xiass_sms_card_keys
			WHERE status = 'queued'
		)`).Scan(&status.Available); err != nil {
		return nil, fmt.Errorf("check sms card key availability: %w", err)
	}
	return status, nil
}

// Redeem atomically claims the next queued card before sending it to Pixlab.
// A card is consumed only after the provider returns a verification code. If
// an ambiguous request fails, retain its reservation for recovery.
func (s *PixlabSMSService) Redeem(ctx context.Context, ownerUserID int64) (*PixlabSMSResult, error) {
	if ownerUserID <= 0 {
		return nil, infraerrors.Unauthorized("SMS_OWNER_REQUIRED", "无法识别当前管理员")
	}
	for attempt := 0; attempt < pixlabSMSMaxAcquireAttempts; attempt++ {
		sessionID := uuid.NewString()
		var encryptedKey string
		err := s.db.QueryRowContext(ctx, `
			WITH next_key AS (
				SELECT id
				FROM xiass_sms_card_keys
				WHERE status = 'queued'
				ORDER BY claim_count ASC, queue_rank ASC, last_claimed_at NULLS FIRST, id
				FOR UPDATE SKIP LOCKED
				LIMIT 1
			)
			UPDATE xiass_sms_card_keys AS card
			SET status = 'active', owner_user_id = $1, session_id = $2,
				consumed_at = NOW(), last_claimed_at = NOW(),
				cleanup_attempted_at = NULL, cleanup_lease_token = NULL, cleanup_lease_until = NULL,
				claim_count = card.claim_count + 1, updated_at = NOW()
			FROM next_key
			WHERE card.id = next_key.id
			RETURNING card.encrypted_key`, ownerUserID, sessionID).Scan(&encryptedKey)
		if err != nil {
			// The database keeps one live session per user as a final guard
			// against concurrent browser tabs. OAuth can be opened from a new
			// tab after the browser-local session ID has been lost, so recover
			// the existing session instead of surfacing a misleading outage.
			if errors.Is(err, sql.ErrNoRows) || isPixlabActiveOwnerConflict(err) {
				existingSessionID, lookupErr := s.activeSessionID(ctx, ownerUserID)
				if lookupErr != nil {
					return nil, lookupErr
				}
				if existingSessionID != "" {
					result, resumeErr := s.Resume(ctx, ownerUserID, existingSessionID)
					if resumeErr == nil {
						if result.SessionID == "" && !pixlabHasVerificationCode(result.Code) && pixlabTerminalStatus(result.Status, "") != "" {
							continue
						}
						return result, nil
					}
					return nil, resumeErr
				}
				if errors.Is(err, sql.ErrNoRows) {
					return nil, ErrPixlabSMSNoCardKey
				}
				// The conflicting row belongs to the member site. Never recover or
				// settle it through an OAuth/admin request.
				return nil, ErrPixlabSMSActive
			}
			return nil, fmt.Errorf("claim sms card key: %w", err)
		}

		providerName, key, err := s.decodeSMSCardKey(encryptedKey)
		if err != nil {
			_ = s.releaseSession(ctx, sessionID, ownerUserID)
			return nil, err
		}
		if providerName == SMSProviderGiema {
			result, giemaErr := s.runGiemaSession(ctx, ownerUserID, sessionID, key, "redeem", false, false)
			if giemaErr != nil {
				if isGiemaCardUnusableError(giemaErr) {
					if settleErr := s.settleGiemaSession(ctx, sessionID, ownerUserID, false, pixlabSettlementExhaust, nil); settleErr != nil {
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
				if markErr := s.exhaustSession(ctx, sessionID, ownerUserID); markErr != nil {
					return nil, markErr
				}
				continue
			}
			if isPixlabProviderSessionExpiredError(err) {
				if releaseErr := s.releaseSession(ctx, sessionID, ownerUserID); releaseErr != nil {
					return nil, releaseErr
				}
			}
			return nil, err
		}
		if !pixlabHasVerificationCode(provider.Code) && pixlabTerminalStatus(provider.Status, "") == "" {
			if err := s.syncRedeemedSessionStart(ctx, sessionID, ownerUserID, provider.CreatedAt); err != nil {
				return nil, err
			}
		}
		return s.finishProviderResponse(ctx, sessionID, ownerUserID, provider, false, false)
	}
	return nil, ErrPixlabSMSAttemptLimit
}

func (s *PixlabSMSService) activeSessionID(ctx context.Context, ownerUserID int64) (string, error) {
	var sessionID string
	err := s.db.QueryRowContext(ctx, `
		SELECT card.session_id
		FROM xiass_sms_card_keys AS card
		WHERE card.owner_user_id = $1 AND card.status = 'active'
			AND card.workflow_scope = ''
			AND NOT EXISTS (
				SELECT 1
				FROM xiass_sms_member_charges AS charge
				WHERE charge.session_id = card.session_id
					AND charge.user_id = card.owner_user_id
					AND charge.status = 'held'
			)
		ORDER BY card.consumed_at DESC NULLS LAST, card.id DESC
		LIMIT 1`, ownerUserID).Scan(&sessionID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("load active sms session: %w", err)
	}
	return strings.TrimSpace(sessionID), nil
}

func (s *PixlabSMSService) activeMemberSessionID(ctx context.Context, userID int64) (string, error) {
	var sessionID string
	err := s.db.QueryRowContext(ctx, `
		SELECT card.session_id
		FROM xiass_sms_card_keys AS card
		JOIN xiass_sms_member_charges AS charge
			ON charge.session_id = card.session_id
			AND charge.user_id = card.owner_user_id
			AND charge.status = 'held'
		WHERE card.owner_user_id = $1 AND card.status = 'active'
		ORDER BY card.consumed_at DESC NULLS LAST, card.id DESC
		LIMIT 1`, userID).Scan(&sessionID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("load active member sms session: %w", err)
	}
	return strings.TrimSpace(sessionID), nil
}

func isPixlabActiveOwnerConflict(err error) bool {
	var pqErr *pq.Error
	return errors.As(err, &pqErr) &&
		pqErr.Code == "23505" &&
		pqErr.Constraint == "uq_xiass_sms_card_keys_active_owner"
}

func isPixlabProviderSessionExpiredError(err error) bool {
	return infraerrors.Reason(err) == "SMS_PROVIDER_SESSION_EXPIRED"
}

// RedeemForMember reserves ¥2.00 and claims one card in the same database
// transaction. The money remains reversible until a real verification code is
// reported by the provider; that final event captures the charge permanently.
func (s *PixlabSMSService) RedeemForMember(ctx context.Context, userID int64) (*PixlabSMSMemberResult, error) {
	if userID <= 0 {
		return nil, infraerrors.Unauthorized("SMS_OWNER_REQUIRED", "无法识别当前用户")
	}
	// Browser storage is only a convenience. Recover the server-side session
	// before reserving another card so a cleared browser cannot strand a number
	// or a held member charge.
	existingSessionID, err := s.activeMemberSessionID(ctx, userID)
	if err != nil {
		return nil, err
	}
	if existingSessionID != "" {
		existing, resumeErr := s.withActiveSession(ctx, userID, existingSessionID, "resume", true)
		if resumeErr != nil {
			// A transient provider failure must leave the existing session active;
			// claiming another card here would double-reserve the member's balance.
			if !errors.Is(resumeErr, ErrPixlabSMSSession) {
				return nil, resumeErr
			}
		} else if existing.SessionID != "" || pixlabHasVerificationCode(existing.Code) {
			return s.memberResult(ctx, userID, existingSessionID, existing)
		}
		// An explicitly terminal session without a code has already been
		// released and refunded by ResumeForMember, so a new claim may proceed.
	}
	for attempt := 0; attempt < pixlabSMSMaxAcquireAttempts; attempt++ {
		sessionID, encryptedKey, err := s.claimMemberSession(ctx, userID)
		if err != nil {
			return nil, err
		}
		providerName, key, err := s.decodeSMSCardKey(encryptedKey)
		if err != nil {
			if settleErr := s.settleSession(ctx, sessionID, userID, true, pixlabSettlementRelease, nil); settleErr != nil {
				return nil, settleErr
			}
			return nil, err
		}
		if providerName == SMSProviderGiema {
			result, giemaErr := s.runGiemaSession(ctx, userID, sessionID, key, "redeem", true, false)
			if giemaErr != nil {
				if isGiemaCardUnusableError(giemaErr) {
					if settleErr := s.settleGiemaSession(ctx, sessionID, userID, true, pixlabSettlementExhaust, nil); settleErr != nil {
						return nil, settleErr
					}
					continue
				}
				return nil, giemaErr
			}
			if result != nil && result.SessionID == "" && !pixlabHasVerificationCode(result.Code) && pixlabTerminalStatus(result.Status, "") != "" {
				continue
			}
			return s.memberResult(ctx, userID, sessionID, result)
		}
		provider, err := s.callProvider(ctx, "redeem", key)
		if err != nil {
			if isPixlabCardUnusableError(err) {
				if settleErr := s.settleSession(ctx, sessionID, userID, true, pixlabSettlementExhaust, nil); settleErr != nil {
					return nil, settleErr
				}
				continue
			}
			if isPixlabProviderSessionExpiredError(err) {
				if settleErr := s.settleSession(ctx, sessionID, userID, true, pixlabSettlementRelease, nil); settleErr != nil {
					return nil, settleErr
				}
			}
			return nil, err
		}
		if !pixlabHasVerificationCode(provider.Code) && pixlabTerminalStatus(provider.Status, "") == "" {
			if err := s.syncRedeemedSessionStart(ctx, sessionID, userID, provider.CreatedAt); err != nil {
				return nil, err
			}
		}
		result, err := s.finishProviderResponse(ctx, sessionID, userID, provider, true, false)
		if err != nil {
			return nil, err
		}
		return s.memberResult(ctx, userID, sessionID, result)
	}
	return nil, ErrPixlabSMSAttemptLimit
}

func (s *PixlabSMSService) ResumeForMember(ctx context.Context, userID int64, sessionID string) (*PixlabSMSMemberResult, error) {
	result, err := s.withActiveSession(ctx, userID, sessionID, "resume", true)
	if errors.Is(err, ErrPixlabSMSSession) {
		return s.loadTerminalMemberResult(ctx, userID, sessionID)
	}
	if err != nil {
		return nil, err
	}
	return s.memberResult(ctx, userID, sessionID, result)
}

func (s *PixlabSMSService) CheckForMember(ctx context.Context, userID int64, sessionID string) (*PixlabSMSMemberResult, error) {
	result, err := s.withActiveSession(ctx, userID, sessionID, "check", true)
	if errors.Is(err, ErrPixlabSMSSession) {
		return s.loadTerminalMemberResult(ctx, userID, sessionID)
	}
	if err != nil {
		return nil, err
	}
	return s.memberResult(ctx, userID, sessionID, result)
}

func (s *PixlabSMSService) ChangeNumberForMember(ctx context.Context, userID int64, sessionID string) (*PixlabSMSMemberResult, error) {
	if err := s.requireMemberMutationDelay(ctx, userID, sessionID); err != nil {
		return nil, err
	}
	// Check before releasing the number. A code that arrives between browser
	// polls is captured and returned instead of being lost to a change request.
	current, err := s.withActiveSession(ctx, userID, sessionID, "check", true)
	if err != nil {
		return nil, err
	}
	if current.SessionID == "" {
		if current.Status == "EXHAUSTED" {
			return s.RedeemForMember(ctx, userID)
		}
		return s.memberResult(ctx, userID, sessionID, current)
	}
	if current.provider == SMSProviderGiema {
		result, err := s.withActiveSession(ctx, userID, sessionID, "change", true)
		if err != nil {
			return nil, err
		}
		if result != nil && result.Status == "EXHAUSTED" {
			return s.RedeemForMember(ctx, userID)
		}
		return s.memberResult(ctx, userID, sessionID, result)
	}
	if released, err := s.cancel(ctx, userID, sessionID, true); err != nil {
		return nil, err
	} else if released.Status == "RECEIVED" {
		return s.memberResult(ctx, userID, sessionID, released)
	}
	return s.RedeemForMember(ctx, userID)
}

func (s *PixlabSMSService) CancelForMember(ctx context.Context, userID int64, sessionID string) (*PixlabSMSMemberResult, error) {
	if err := s.requireMemberMutationDelay(ctx, userID, sessionID); err != nil {
		return nil, err
	}
	// A pending provider-side code must win over a cancellation. This prevents
	// a late delivery from being discarded when the member clicks Cancel.
	current, err := s.withActiveSession(ctx, userID, sessionID, "check", true)
	if err != nil {
		return nil, err
	}
	if current.SessionID == "" {
		return s.memberResult(ctx, userID, sessionID, current)
	}
	result, err := s.cancel(ctx, userID, sessionID, true)
	if err != nil {
		return nil, err
	}
	return s.memberResult(ctx, userID, sessionID, result)
}

func (s *PixlabSMSService) Resume(ctx context.Context, ownerUserID int64, sessionID string) (*PixlabSMSResult, error) {
	result, err := s.withActiveSession(ctx, ownerUserID, sessionID, "resume", false)
	if errors.Is(err, ErrPixlabSMSSession) {
		return s.loadTerminalResult(ctx, ownerUserID, sessionID, false)
	}
	return result, err
}

func (s *PixlabSMSService) Check(ctx context.Context, ownerUserID int64, sessionID string) (*PixlabSMSResult, error) {
	result, err := s.withActiveSession(ctx, ownerUserID, sessionID, "check", false)
	if errors.Is(err, ErrPixlabSMSSession) {
		return s.loadTerminalResult(ctx, ownerUserID, sessionID, false)
	}
	return result, err
}

func (s *PixlabSMSService) Cancel(ctx context.Context, ownerUserID int64, sessionID string) (*PixlabSMSResult, error) {
	return s.cancel(ctx, ownerUserID, sessionID, false)
}

func (s *PixlabSMSService) cancel(ctx context.Context, ownerUserID int64, sessionID string, settleMemberFee bool) (*PixlabSMSResult, error) {
	if ownerUserID <= 0 {
		return nil, infraerrors.Unauthorized("SMS_OWNER_REQUIRED", "无法识别当前管理员")
	}
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" || len(sessionID) > 64 {
		return nil, ErrPixlabSMSSession
	}
	unlock, err := s.sessionLock(sessionID).lock(ctx)
	if err != nil {
		return nil, err
	}
	defer unlock()

	result, err := s.withActiveSessionLocked(ctx, ownerUserID, sessionID, "cancel", settleMemberFee, false)
	if err != nil {
		return nil, err
	}
	if result.provider == SMSProviderGiema {
		return result, nil
	}
	// A received code always wins over cancellation. The provider may deliver
	// it just as the member clicks the button; preserve it for the caller and
	// do not overwrite it with a cancelled result.
	if pixlabHasVerificationCode(result.Code) {
		result.SessionID = ""
		result.Status = "RECEIVED"
		return result, nil
	}
	// Cancellation does not invalidate a card key. A terminal response is
	// released by finishProviderResponse; a non-terminal response needs an
	// explicit release before the next redemption.
	if !pixlabHasVerificationCode(result.Code) && pixlabTerminalStatus(result.Status, "") == "" {
		if settleMemberFee {
			if err := s.settleSession(ctx, sessionID, ownerUserID, true, pixlabSettlementRelease, nil); err != nil {
				return nil, err
			}
		} else if err := s.releaseSession(ctx, sessionID, ownerUserID); err != nil {
			return nil, err
		}
		queue, err := s.queueStatus(ctx, ownerUserID)
		if err != nil {
			return nil, err
		}
		result.QueuedCount = queue.QueuedCount
	}
	result.SessionID = ""
	result.Status = "CANCELLED"
	result.Code = ""
	return result, nil
}

func (s *PixlabSMSService) requireMemberMutationDelay(ctx context.Context, userID int64, sessionID string) error {
	if s == nil || s.db == nil {
		return infraerrors.InternalServer("SMS_RECEIVER_UNAVAILABLE", "接码服务尚未初始化")
	}
	sessionID = strings.TrimSpace(sessionID)
	if userID <= 0 || sessionID == "" || len(sessionID) > 64 {
		return ErrPixlabSMSSession
	}
	var claimedAt time.Time
	err := s.db.QueryRowContext(ctx, `
		SELECT consumed_at
		FROM xiass_sms_card_keys
		WHERE session_id = $1 AND owner_user_id = $2 AND status = 'active'`, sessionID, userID).Scan(&claimedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrPixlabSMSSession
	}
	if err != nil {
		return fmt.Errorf("load member sms claim time: %w", err)
	}
	if time.Now().Before(claimedAt.Add(PixlabSMSMemberMutationDelay)) {
		return ErrPixlabSMSLocked
	}
	return nil
}

// ChangeNumber uses Giema's direct rotation or Pixlab's cancel-and-redeem flow.
// Provider-confirmed unusable cards are skipped during acquisition.
func (s *PixlabSMSService) ChangeNumber(ctx context.Context, ownerUserID int64, sessionID string) (*PixlabSMSResult, error) {
	provider, err := s.activeSessionProvider(ctx, ownerUserID, sessionID, pixlabWorkflowScope(ctx))
	if err != nil {
		return nil, err
	}
	if provider == SMSProviderGiema {
		result, err := s.withActiveSession(ctx, ownerUserID, sessionID, "change", false)
		if err != nil {
			return nil, err
		}
		if result != nil && result.Status == "EXHAUSTED" {
			return s.Redeem(ctx, ownerUserID)
		}
		return result, nil
	}
	cancelled, err := s.Cancel(ctx, ownerUserID, sessionID)
	if err != nil {
		return nil, err
	}
	// A verification code delivered during cancellation is terminal and must be
	// returned to the caller. Redeeming again here would both hide that code and
	// consume another card claim.
	if cancelled != nil && (pixlabHasVerificationCode(cancelled.Code) || strings.EqualFold(cancelled.Status, "RECEIVED")) {
		return cancelled, nil
	}
	return s.Redeem(ctx, ownerUserID)
}

func (s *PixlabSMSService) withActiveSession(ctx context.Context, ownerUserID int64, sessionID, action string, settleMemberFee bool) (*PixlabSMSResult, error) {
	return s.withActiveSessionMode(ctx, ownerUserID, sessionID, action, settleMemberFee, false)
}

func (s *PixlabSMSService) withActiveSessionMode(ctx context.Context, ownerUserID int64, sessionID, action string, settleMemberFee, persistTerminal bool) (*PixlabSMSResult, error) {
	if ownerUserID <= 0 {
		return nil, infraerrors.Unauthorized("SMS_OWNER_REQUIRED", "无法识别当前管理员")
	}
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" || len(sessionID) > 64 {
		return nil, ErrPixlabSMSSession
	}
	unlock, err := s.sessionLock(sessionID).lock(ctx)
	if err != nil {
		return nil, err
	}
	defer unlock()
	return s.withActiveSessionLocked(ctx, ownerUserID, sessionID, action, settleMemberFee, persistTerminal)
}

func (s *PixlabSMSService) withActiveSessionLocked(ctx context.Context, ownerUserID int64, sessionID, action string, settleMemberFee, persistTerminal bool) (*PixlabSMSResult, error) {
	var encryptedKey string
	var consumedAt time.Time
	err := s.db.QueryRowContext(ctx, `
		SELECT card.encrypted_key, card.consumed_at
		FROM xiass_sms_card_keys AS card
		WHERE card.session_id = $1 AND card.owner_user_id = $2 AND card.status = 'active'
			AND EXISTS (
				SELECT 1
				FROM xiass_sms_member_charges AS charge
				WHERE charge.session_id = card.session_id
					AND charge.user_id = card.owner_user_id
					AND charge.status = 'held'
			) = $3 AND card.workflow_scope = $4`, sessionID, ownerUserID, settleMemberFee, pixlabWorkflowScope(ctx)).Scan(&encryptedKey, &consumedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrPixlabSMSSession
	}
	if err != nil {
		return nil, fmt.Errorf("load sms session: %w", err)
	}
	providerName, key, err := s.decodeSMSCardKey(encryptedKey)
	if err != nil {
		return nil, err
	}
	if providerName == SMSProviderGiema {
		result, giemaErr := s.runGiemaSession(ctx, ownerUserID, sessionID, key, action, settleMemberFee, persistTerminal)
		if isGiemaCardUnusableError(giemaErr) {
			if settleErr := s.settleGiemaSession(ctx, sessionID, ownerUserID, settleMemberFee, pixlabSettlementExhaust, nil); settleErr != nil {
				return nil, settleErr
			}
			queue, queueErr := s.queueStatus(ctx, ownerUserID)
			if queueErr != nil {
				return nil, queueErr
			}
			return &PixlabSMSResult{Status: "EXHAUSTED", QueuedCount: queue.QueuedCount, ServerTime: time.Now().UTC(), provider: SMSProviderGiema}, nil
		}
		return result, giemaErr
	}
	if !time.Now().Before(consumedAt.Add(PixlabSMSSessionValidity)) {
		return s.checkExpiredSessionLocked(ctx, sessionID, ownerUserID, key, settleMemberFee, persistTerminal)
	}
	provider, err := s.callProvider(ctx, action, key)
	if err != nil {
		if isPixlabProviderSessionExpiredError(err) {
			return s.finishProviderResponse(ctx, sessionID, ownerUserID,
				&pixlabSMSProviderResponse{Success: json.RawMessage("true"), Status: "EXPIRED"}, settleMemberFee, persistTerminal)
		}
		if provider != nil && (pixlabProviderHasTrustedCode(provider) ||
			(pixlabSuccess(provider.Success) && pixlabTerminalStatus(provider.Status, "") != "")) {
			return s.finishProviderResponse(ctx, sessionID, ownerUserID, provider, settleMemberFee, persistTerminal)
		}
		if isPixlabCardUnusableError(err) {
			if settleMemberFee {
				if err := s.settleSession(ctx, sessionID, ownerUserID, true, pixlabSettlementExhaust, nil); err != nil {
					return nil, err
				}
			} else if err := s.exhaustSession(ctx, sessionID, ownerUserID); err != nil {
				return nil, err
			}
			queue, queueErr := s.queueStatus(ctx, ownerUserID)
			if queueErr != nil {
				return nil, queueErr
			}
			return &PixlabSMSResult{Status: "EXHAUSTED", QueuedCount: queue.QueuedCount, ServerTime: time.Now().UTC()}, nil
		}
		// A transient provider failure must not invalidate a card key. Keep the
		// session active so the operator can refresh or cancel it later.
		return nil, err
	}
	return s.finishProviderResponse(ctx, sessionID, ownerUserID, provider, settleMemberFee, persistTerminal)
}

// checkExpiredSessionLocked asks Pixlab once more before local expiry can
// release a card. A late code is finalized and returned to the caller; a
// successful no-code response confirms local expiry. Transient failures keep
// the session active so the caller can retry.
func (s *PixlabSMSService) checkExpiredSessionLocked(ctx context.Context, sessionID string, ownerUserID int64, key string, settleMemberFee, persistTerminal bool) (*PixlabSMSResult, error) {
	provider, err := s.callProvider(ctx, "check", key)
	if err != nil {
		if isPixlabCardUnusableError(err) {
			if settleMemberFee {
				if settleErr := s.settleSession(ctx, sessionID, ownerUserID, true, pixlabSettlementExhaust, nil); settleErr != nil {
					return nil, settleErr
				}
			} else if exhaustErr := s.exhaustSession(ctx, sessionID, ownerUserID); exhaustErr != nil {
				return nil, exhaustErr
			}
			queue, queueErr := s.queueStatus(ctx, ownerUserID)
			if queueErr != nil {
				return nil, queueErr
			}
			return &PixlabSMSResult{Status: "EXHAUSTED", QueuedCount: queue.QueuedCount, ServerTime: time.Now().UTC()}, nil
		}
		// An HTTP rejection can still carry Pixlab's code or explicit terminal
		// state. A known "session is gone" message also confirms expiry; other
		// failures remain retryable and keep the active session intact.
		if provider == nil {
			return nil, err
		}
		if pixlabProviderHasTrustedCode(provider) {
			return s.finishProviderResponse(ctx, sessionID, ownerUserID, provider, settleMemberFee, persistTerminal)
		}
		trustedTerminalStatus := pixlabSuccess(provider.Success) && pixlabTerminalStatus(provider.Status, "") != ""
		if !trustedTerminalStatus && !isPixlabTerminalSessionMessage(pixlabProviderMessage(provider)) {
			return nil, err
		}
	}
	if pixlabProviderHasTrustedCode(provider) {
		return s.finishProviderResponse(ctx, sessionID, ownerUserID, provider, settleMemberFee, persistTerminal)
	}
	if _, err := s.expireSession(ctx, sessionID, ownerUserID, settleMemberFee); err != nil {
		return nil, err
	}
	queue, err := s.queueStatus(ctx, ownerUserID)
	if err != nil {
		return nil, err
	}
	return &PixlabSMSResult{
		Status:      "EXPIRED",
		QueuedCount: queue.QueuedCount,
		ServerTime:  time.Now().UTC(),
	}, nil
}

func (s *PixlabSMSService) cleanupExpiredSessions(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM xiass_sms_terminal_results WHERE expires_at <= NOW()`); err != nil {
		return fmt.Errorf("delete expired sms terminal results: %w", err)
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT
			card.session_id,
			card.owner_user_id,
			card.workflow_scope,
			EXISTS (
				SELECT 1
				FROM xiass_sms_member_charges AS charge
				WHERE charge.session_id = card.session_id
					AND charge.user_id = card.owner_user_id
					AND charge.status = 'held'
			) AS member_session
		FROM xiass_sms_card_keys AS card
		WHERE card.status = 'active'
			AND card.session_id IS NOT NULL
			AND card.owner_user_id IS NOT NULL
			AND card.consumed_at + ($1 * INTERVAL '1 second') <= NOW()
			AND (card.cleanup_lease_until IS NULL OR card.cleanup_lease_until <= NOW())
		ORDER BY card.cleanup_attempted_at NULLS FIRST, card.consumed_at, card.id
		LIMIT 50`, int64(PixlabSMSSessionValidity/time.Second))
	if err != nil {
		return fmt.Errorf("load expired sms sessions: %w", err)
	}
	type expiredSession struct {
		sessionID string
		ownerID   int64
		scope     string
		member    bool
	}
	sessions := make([]expiredSession, 0)
	for rows.Next() {
		var session expiredSession
		if err := rows.Scan(&session.sessionID, &session.ownerID, &session.scope, &session.member); err != nil {
			_ = rows.Close()
			return fmt.Errorf("scan expired sms session: %w", err)
		}
		sessions = append(sessions, session)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close expired sms sessions: %w", err)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate expired sms sessions: %w", err)
	}

	var firstErr error
	for _, session := range sessions {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		leaseToken := uuid.NewString()
		leaseResult, err := s.db.ExecContext(ctx, `
			UPDATE xiass_sms_card_keys
			SET cleanup_attempted_at = NOW(),
				cleanup_lease_token = $3,
				cleanup_lease_until = NOW() + ($4 * INTERVAL '1 second'),
				updated_at = NOW()
			WHERE session_id = $1 AND owner_user_id = $2 AND status = 'active'
				AND consumed_at + ($5 * INTERVAL '1 second') <= NOW()
				AND (cleanup_lease_until IS NULL OR cleanup_lease_until <= NOW())`,
			session.sessionID,
			session.ownerID,
			leaseToken,
			int64(pixlabSMSCleanupLeaseTTL/time.Second),
			int64(PixlabSMSSessionValidity/time.Second),
		)
		if err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("mark expired sms cleanup attempt %s: %w", session.sessionID, err)
			}
			continue
		}
		acquired, err := leaseResult.RowsAffected()
		if err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("read expired sms cleanup lease %s: %w", session.sessionID, err)
			}
			continue
		}
		if acquired != 1 {
			continue
		}
		sessionCtx, sessionCancel := context.WithTimeout(ctx, 5*time.Second)
		// Natural expiry is independent of the 180-second workflow timeout.
		// Cleanup checks/settles only; it never cancels or acquires a number.
		sessionCtx = context.WithValue(sessionCtx, pixlabWorkflowContextKey{}, session.scope)
		_, err = s.withActiveSessionMode(sessionCtx, session.ownerID, session.sessionID, "check", session.member, session.scope == "")
		sessionCancel()
		releaseCtx, releaseCancel := context.WithTimeout(context.Background(), 2*time.Second)
		releaseErr := s.releaseCleanupLease(releaseCtx, leaseToken)
		releaseCancel()
		if releaseErr != nil && firstErr == nil {
			firstErr = fmt.Errorf("release expired sms cleanup lease %s: %w", session.sessionID, releaseErr)
		}
		if err == nil || errors.Is(err, ErrPixlabSMSSession) {
			continue
		}
		// Provider/network failures preserve the active session. Continue with
		// other expired rows and retry this one on the next cleanup tick.
		if firstErr == nil {
			firstErr = fmt.Errorf("finalize expired sms session %s: %w", session.sessionID, err)
		}
	}
	return firstErr
}

func (s *PixlabSMSService) releaseCleanupLease(ctx context.Context, leaseToken string) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE xiass_sms_card_keys
		SET cleanup_lease_token = NULL, cleanup_lease_until = NULL, updated_at = NOW()
		WHERE cleanup_lease_token = $1`, leaseToken)
	if err != nil {
		return fmt.Errorf("release sms cleanup lease: %w", err)
	}
	return nil
}

func (s *PixlabSMSService) sessionLock(sessionID string) *pixlabSMSContextLock {
	hash := fnv.New32a()
	_, _ = hash.Write([]byte(sessionID))
	return &s.sessionLocks[hash.Sum32()%uint32(len(s.sessionLocks))]
}

func (s *PixlabSMSService) workflowLock(scope string) *pixlabSMSContextLock {
	hash := fnv.New32a()
	_, _ = hash.Write([]byte(scope))
	return &s.workflowLocks[hash.Sum32()%uint32(len(s.workflowLocks))]
}

func (s *PixlabSMSService) finishProviderResponse(ctx context.Context, sessionID string, ownerUserID int64, provider *pixlabSMSProviderResponse, settleMemberFee, persistTerminal bool) (*PixlabSMSResult, error) {
	if provider == nil {
		return nil, infraerrors.ServiceUnavailable("SMS_PROVIDER_INVALID_RESPONSE", "接码服务返回了无效数据")
	}
	responseSessionID := sessionID
	if pixlabProviderHasTrustedCode(provider) {
		var encryptedResult *string
		if persistTerminal {
			encoded, err := s.encodeTerminalResult(provider)
			if err != nil {
				return nil, err
			}
			encryptedResult = &encoded
		}
		if settleMemberFee || persistTerminal {
			if err := s.settleSession(ctx, sessionID, ownerUserID, settleMemberFee, pixlabSettlementCapture, encryptedResult); err != nil {
				return nil, err
			}
		} else if err := s.finishSession(ctx, sessionID, ownerUserID); err != nil {
			return nil, err
		}
		responseSessionID = ""
	} else if pixlabTerminalStatus(provider.Status, "") != "" {
		// A terminal number session does not establish card exhaustion.
		if settleMemberFee {
			if err := s.settleSession(ctx, sessionID, ownerUserID, true, pixlabSettlementRelease, nil); err != nil {
				return nil, err
			}
		} else if err := s.releaseSession(ctx, sessionID, ownerUserID); err != nil {
			return nil, err
		}
		responseSessionID = ""
	}
	queue, err := s.queueStatus(ctx, ownerUserID)
	if err != nil {
		return nil, err
	}
	result := &PixlabSMSResult{
		SessionID:   responseSessionID,
		Status:      provider.Status,
		Number:      provider.Number,
		Country:     provider.Country,
		Code:        provider.Code,
		QueuedCount: queue.QueuedCount,
		ServerTime:  time.Now().UTC(),
	}
	if responseSessionID != "" {
		expiresAt, err := s.sessionExpiresAt(ctx, responseSessionID, ownerUserID)
		if err != nil {
			return nil, err
		}
		result.ExpiresAt = expiresAt
	}
	return result, nil
}

func (s *PixlabSMSService) encodeTerminalResult(provider *pixlabSMSProviderResponse) (string, error) {
	if provider == nil || !pixlabProviderHasTrustedCode(provider) {
		return "", ErrPixlabSMSSession
	}
	payload, err := json.Marshal(PixlabSMSResult{
		Status:     "RECEIVED",
		Number:     provider.Number,
		Country:    provider.Country,
		Code:       provider.Code,
		ServerTime: time.Now().UTC(),
	})
	if err != nil {
		return "", fmt.Errorf("encode sms terminal result: %w", err)
	}
	encryptedPayload, err := s.encryptor.Encrypt(string(payload))
	if err != nil {
		return "", fmt.Errorf("encrypt sms terminal result: %w", err)
	}
	return encryptedPayload, nil
}

type pixlabSMSTerminalPrepare func(context.Context, *sql.Tx, *PixlabSMSResult) error

func (s *PixlabSMSService) loadTerminalResult(ctx context.Context, ownerUserID int64, sessionID string, memberSession bool) (*PixlabSMSResult, error) {
	return s.consumeTerminalResult(ctx, ownerUserID, sessionID, memberSession, nil)
}

func (s *PixlabSMSService) loadTerminalMemberResult(ctx context.Context, userID int64, sessionID string) (*PixlabSMSMemberResult, error) {
	var prepared *PixlabSMSMemberResult
	_, err := s.consumeTerminalResult(ctx, userID, sessionID, true, func(ctx context.Context, tx *sql.Tx, result *PixlabSMSResult) error {
		memberResult, err := s.memberResultWithQueryer(ctx, tx, userID, sessionID, result)
		if err != nil {
			return err
		}
		prepared = memberResult
		return nil
	})
	if err != nil {
		return nil, err
	}
	return prepared, nil
}

func (s *PixlabSMSService) consumeTerminalResult(ctx context.Context, ownerUserID int64, sessionID string, memberSession bool, prepare pixlabSMSTerminalPrepare) (*PixlabSMSResult, error) {
	if s == nil || s.db == nil || s.encryptor == nil || ownerUserID <= 0 {
		return nil, ErrPixlabSMSSession
	}
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" || len(sessionID) > 64 {
		return nil, ErrPixlabSMSSession
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin sms terminal result consumption: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var encryptedPayload string
	err = tx.QueryRowContext(ctx, `
		SELECT encrypted_payload
		FROM xiass_sms_terminal_results
		WHERE session_id = $1 AND owner_user_id = $2 AND member_session = $3
			AND expires_at > NOW()
		FOR UPDATE`, sessionID, ownerUserID, memberSession).Scan(&encryptedPayload)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrPixlabSMSSession
	}
	if err != nil {
		return nil, fmt.Errorf("load sms terminal result: %w", err)
	}
	payload, err := s.encryptor.Decrypt(encryptedPayload)
	if err != nil {
		return nil, fmt.Errorf("decrypt sms terminal result: %w", err)
	}
	var result PixlabSMSResult
	if err := json.Unmarshal([]byte(payload), &result); err != nil {
		return nil, fmt.Errorf("decode sms terminal result: %w", err)
	}
	result.SessionID = ""
	result.ServerTime = time.Now().UTC()
	if prepare != nil {
		if err := prepare(ctx, tx, &result); err != nil {
			return nil, err
		}
	}
	if err := execExactlyOne(ctx, tx, `
		DELETE FROM xiass_sms_terminal_results
		WHERE session_id = $1 AND owner_user_id = $2 AND member_session = $3`, sessionID, ownerUserID, memberSession); err != nil {
		return nil, fmt.Errorf("consume sms terminal result: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit sms terminal result consumption: %w", err)
	}
	return &result, nil
}

// syncRedeemedSessionStart stores Pixlab's authoritative UTC creation time.
// If the provider omits it, NOW() is evaluated after the successful redeem so
// network latency cannot shorten the documented 15-minute number lifetime.
func (s *PixlabSMSService) syncRedeemedSessionStart(ctx context.Context, sessionID string, ownerUserID int64, rawCreatedAt string) error {
	createdAt, ok := parsePixlabCreatedAt(rawCreatedAt)
	var result sql.Result
	var err error
	if ok {
		result, err = s.db.ExecContext(ctx, `
			UPDATE xiass_sms_card_keys
			SET consumed_at = $3, updated_at = NOW()
			WHERE session_id = $1 AND owner_user_id = $2 AND status = 'active'`, sessionID, ownerUserID, createdAt)
	} else {
		result, err = s.db.ExecContext(ctx, `
			UPDATE xiass_sms_card_keys
			SET consumed_at = NOW(), updated_at = NOW()
			WHERE session_id = $1 AND owner_user_id = $2 AND status = 'active'`, sessionID, ownerUserID)
	}
	if err != nil {
		return fmt.Errorf("sync redeemed sms session start: %w", err)
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read redeemed sms session start update: %w", err)
	}
	if updated != 1 {
		return ErrPixlabSMSSession
	}
	return nil
}

func parsePixlabCreatedAt(raw string) (time.Time, bool) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return time.Time{}, false
	}
	if parsed, err := time.Parse("2006-01-02 15:04:05", value); err == nil {
		return parsed.UTC(), true
	}
	if parsed, err := time.Parse(time.RFC3339, value); err == nil {
		return parsed.UTC(), true
	}
	return time.Time{}, false
}

func (s *PixlabSMSService) sessionExpiresAt(ctx context.Context, sessionID string, ownerUserID int64) (*time.Time, error) {
	var consumedAt time.Time
	err := s.db.QueryRowContext(ctx, `
		SELECT consumed_at
		FROM xiass_sms_card_keys
		WHERE session_id = $1 AND owner_user_id = $2 AND status = 'active'`, sessionID, ownerUserID).Scan(&consumedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("load sms session expiry: %w", err)
	}
	expiresAt := consumedAt.Add(PixlabSMSSessionValidity)
	return &expiresAt, nil
}

type pixlabSettlement int

const (
	pixlabSettlementCapture pixlabSettlement = iota
	pixlabSettlementRelease
	pixlabSettlementExhaust
)

// settleSession commits every state transition for one provider session in a
// single transaction. Member charge state and card state can never diverge.
func (s *PixlabSMSService) settleSession(ctx context.Context, sessionID string, ownerUserID int64, settleMemberFee bool, settlement pixlabSettlement, encryptedTerminalResult *string) error {
	return s.settleSessionWithProviderPending(ctx, sessionID, ownerUserID, settleMemberFee, settlement, encryptedTerminalResult, false)
}

func (s *PixlabSMSService) settleGiemaSession(ctx context.Context, sessionID string, ownerUserID int64, settleMemberFee bool, settlement pixlabSettlement, encryptedTerminalResult *string) error {
	return s.settleSessionWithProviderPending(ctx, sessionID, ownerUserID, settleMemberFee, settlement, encryptedTerminalResult, true)
}

func (s *PixlabSMSService) settleSessionWithProviderPending(ctx context.Context, sessionID string, ownerUserID int64, settleMemberFee bool, settlement pixlabSettlement, encryptedTerminalResult *string, clearProviderPending bool) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin sms session settlement: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if settleMemberFee {
		if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, ownerUserID); err != nil {
			return fmt.Errorf("lock member sms session settlement: %w", err)
		}
	}

	if encryptedTerminalResult != nil {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO xiass_sms_terminal_results (
				session_id, owner_user_id, member_session, encrypted_payload, expires_at
			) VALUES ($1, $2, $3, $4, NOW() + ($5 * INTERVAL '1 second'))
			ON CONFLICT (session_id) DO UPDATE SET
				encrypted_payload = EXCLUDED.encrypted_payload,
				expires_at = EXCLUDED.expires_at`,
			sessionID, ownerUserID, settleMemberFee, *encryptedTerminalResult, int64(pixlabSMSTerminalResultTTL/time.Second)); err != nil {
			return fmt.Errorf("store sms terminal result: %w", err)
		}
	}

	var cardID int64
	err = tx.QueryRowContext(ctx, `
		SELECT id
		FROM xiass_sms_card_keys
		WHERE session_id = $1 AND owner_user_id = $2 AND status = 'active'
		FOR UPDATE`, sessionID, ownerUserID).Scan(&cardID)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrPixlabSMSSession
	}
	if err != nil {
		return fmt.Errorf("lock sms session settlement: %w", err)
	}

	refunded := false
	if settleMemberFee {
		var amount float64
		err = tx.QueryRowContext(ctx, `
			SELECT amount
			FROM xiass_sms_member_charges
			WHERE session_id = $1 AND user_id = $2 AND status = 'held'
			FOR UPDATE`, sessionID, ownerUserID).Scan(&amount)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrPixlabSMSSession
		}
		if err != nil {
			return fmt.Errorf("lock member sms charge settlement: %w", err)
		}

		if settlement == pixlabSettlementCapture {
			if err := execExactlyOne(ctx, tx, `
				UPDATE xiass_sms_member_charges
				SET status = 'captured', captured_at = NOW(), updated_at = NOW()
				WHERE session_id = $1 AND user_id = $2 AND status = 'held'`, sessionID, ownerUserID); err != nil {
				return fmt.Errorf("capture member sms charge: %w", err)
			}
		} else {
			if err := execExactlyOne(ctx, tx, `
				UPDATE users
				SET balance = balance + $1, updated_at = NOW()
				WHERE id = $2 AND deleted_at IS NULL`, amount, ownerUserID); err != nil {
				return fmt.Errorf("refund member sms balance: %w", err)
			}
			if err := execExactlyOne(ctx, tx, `
				UPDATE xiass_sms_member_charges
				SET status = 'released', released_at = NOW(), updated_at = NOW()
				WHERE session_id = $1 AND user_id = $2 AND status = 'held'`, sessionID, ownerUserID); err != nil {
				return fmt.Errorf("release member sms charge: %w", err)
			}
			refunded = true
		}
	}

	switch settlement {
	case pixlabSettlementCapture:
		if err := execExactlyOne(ctx, tx, `DELETE FROM xiass_sms_card_keys WHERE id = $1 AND status = 'active'`, cardID); err != nil {
			return fmt.Errorf("finish sms session: %w", err)
		}
	case pixlabSettlementExhaust:
		query := `
			UPDATE xiass_sms_card_keys
			SET status = 'exhausted', owner_user_id = NULL, session_id = NULL,
				consumed_at = NULL, updated_at = NOW()
			WHERE id = $1 AND status = 'active'`
		if clearProviderPending {
			query = `
				UPDATE xiass_sms_card_keys
				SET status = 'exhausted', provider_state = '', provider_pending = false, owner_user_id = NULL, session_id = NULL,
					consumed_at = NULL, updated_at = NOW()
				WHERE id = $1 AND status = 'active' AND provider = 'giema'`
		}
		if err := execExactlyOne(ctx, tx, query, cardID); err != nil {
			return fmt.Errorf("exhaust sms session: %w", err)
		}
	case pixlabSettlementRelease:
		query := `
			UPDATE xiass_sms_card_keys
			SET status = 'queued',
				owner_user_id = NULL, session_id = NULL,
				consumed_at = NULL, updated_at = NOW()
			WHERE id = $1 AND status = 'active'`
		if clearProviderPending {
			query = `
				UPDATE xiass_sms_card_keys
				SET status = 'queued', provider_state = '', provider_pending = false,
					owner_user_id = NULL, session_id = NULL,
					consumed_at = NULL, updated_at = NOW()
				WHERE id = $1 AND status = 'active' AND provider = 'giema'`
		}
		if err := execExactlyOne(ctx, tx, query, cardID); err != nil {
			return fmt.Errorf("release sms session: %w", err)
		}
	default:
		return fmt.Errorf("unknown sms session settlement %d", settlement)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit sms session settlement: %w", err)
	}
	if refunded {
		s.invalidateBalance(ctx, ownerUserID)
	}
	return nil
}

func execExactlyOne(ctx context.Context, tx *sql.Tx, query string, args ...any) error {
	result, err := tx.ExecContext(ctx, query, args...)
	if err != nil {
		return err
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if updated != 1 {
		return fmt.Errorf("expected one affected row, got %d", updated)
	}
	return nil
}

func (s *PixlabSMSService) finishSession(ctx context.Context, sessionID string, ownerUserID int64) error {
	result, err := s.db.ExecContext(ctx, `
		DELETE FROM xiass_sms_card_keys
		WHERE session_id = $1 AND owner_user_id = $2 AND status = 'active'`, sessionID, ownerUserID)
	if err != nil {
		return fmt.Errorf("finish sms session: %w", err)
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read finished sms session: %w", err)
	}
	if updated != 1 {
		return ErrPixlabSMSSession
	}
	return nil
}

// releaseSession returns an unused card to rotation. Claim count is history,
// not evidence that the provider can no longer use the card.
func (s *PixlabSMSService) releaseSession(ctx context.Context, sessionID string, ownerUserID int64) error {
	result, err := s.db.ExecContext(ctx, `
		UPDATE xiass_sms_card_keys
		SET status = 'queued',
			owner_user_id = NULL, session_id = NULL,
			consumed_at = NULL, updated_at = NOW()
		WHERE session_id = $1 AND owner_user_id = $2 AND status = 'active'`, sessionID, ownerUserID)
	if err != nil {
		return fmt.Errorf("release sms session: %w", err)
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read released sms session: %w", err)
	}
	if updated != 1 {
		return ErrPixlabSMSSession
	}
	return nil
}

// expireSession atomically refunds a held member fee and returns the card to
// rotation. The card is locked and its age is rechecked with PostgreSQL time,
// so concurrent status refreshes cannot double-refund or race a code capture.
func (s *PixlabSMSService) expireSession(ctx context.Context, sessionID string, ownerUserID int64, settleMemberFee bool) (bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("begin expired sms session release: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if settleMemberFee {
		if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, ownerUserID); err != nil {
			return false, fmt.Errorf("lock expired member sms session: %w", err)
		}
	}

	var cardID int64
	err = tx.QueryRowContext(ctx, `
		SELECT id
		FROM xiass_sms_card_keys
		WHERE session_id = $1 AND owner_user_id = $2 AND status = 'active'
			AND consumed_at + ($3 * INTERVAL '1 second') <= NOW()
		FOR UPDATE`, sessionID, ownerUserID, int64(PixlabSMSSessionValidity/time.Second)).Scan(&cardID)
	if errors.Is(err, sql.ErrNoRows) {
		return false, tx.Commit()
	}
	if err != nil {
		return false, fmt.Errorf("lock expired sms session: %w", err)
	}

	refunded := false
	if settleMemberFee {
		var amount float64
		err = tx.QueryRowContext(ctx, `
			SELECT amount
			FROM xiass_sms_member_charges
			WHERE session_id = $1 AND user_id = $2 AND status = 'held'
			FOR UPDATE`, sessionID, ownerUserID).Scan(&amount)
		switch {
		case errors.Is(err, sql.ErrNoRows):
		case err != nil:
			return false, fmt.Errorf("lock expired member sms charge: %w", err)
		default:
			if err := execExactlyOne(ctx, tx, `
				UPDATE users
				SET balance = balance + $1, updated_at = NOW()
				WHERE id = $2 AND deleted_at IS NULL`, amount, ownerUserID); err != nil {
				return false, fmt.Errorf("refund expired member sms balance: %w", err)
			}
			if err := execExactlyOne(ctx, tx, `
				UPDATE xiass_sms_member_charges
				SET status = 'released', released_at = NOW(), updated_at = NOW()
				WHERE session_id = $1 AND user_id = $2 AND status = 'held'`, sessionID, ownerUserID); err != nil {
				return false, fmt.Errorf("release expired member sms charge: %w", err)
			}
			refunded = true
		}
	}

	result, err := tx.ExecContext(ctx, `
		UPDATE xiass_sms_card_keys
		SET status = 'queued',
			owner_user_id = NULL, session_id = NULL,
			consumed_at = NULL, updated_at = NOW()
		WHERE id = $1 AND owner_user_id = $2 AND status = 'active'`, cardID, ownerUserID)
	if err != nil {
		return false, fmt.Errorf("release expired sms session: %w", err)
	}
	updated, err := result.RowsAffected()
	if err != nil || updated != 1 {
		if err != nil {
			return false, fmt.Errorf("read expired sms session release: %w", err)
		}
		return false, fmt.Errorf("release expired sms session: expected one active card, updated %d", updated)
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("commit expired sms session release: %w", err)
	}
	if refunded {
		s.invalidateBalance(ctx, ownerUserID)
	}
	return true, nil
}

// exhaustSession removes a card from rotation after Pixlab
// explicitly confirms that it has reached its per-card use limit.
func (s *PixlabSMSService) exhaustSession(ctx context.Context, sessionID string, ownerUserID int64) error {
	result, err := s.db.ExecContext(ctx, `
		UPDATE xiass_sms_card_keys
		SET status = 'exhausted', owner_user_id = NULL, session_id = NULL,
			consumed_at = NULL, updated_at = NOW()
		WHERE session_id = $1 AND owner_user_id = $2 AND status = 'active'`, sessionID, ownerUserID)
	if err != nil {
		return fmt.Errorf("exhaust sms card key: %w", err)
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read exhausted sms card key: %w", err)
	}
	if updated != 1 {
		return ErrPixlabSMSSession
	}
	return nil
}

func (s *PixlabSMSService) claimMemberSession(ctx context.Context, userID int64) (string, string, error) {
	if s == nil || s.db == nil || s.encryptor == nil {
		return "", "", infraerrors.InternalServer("SMS_RECEIVER_UNAVAILABLE", "接码服务尚未初始化")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", "", fmt.Errorf("begin member sms claim: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// Serialize claims per member. This prevents two browser tabs from each
	// reserving a card and balance before either request has committed.
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, userID); err != nil {
		return "", "", fmt.Errorf("lock member sms claim: %w", err)
	}
	var active bool
	if err := tx.QueryRowContext(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM xiass_sms_card_keys
			WHERE owner_user_id = $1 AND status = 'active' AND workflow_scope = ''
		)`, userID).Scan(&active); err != nil {
		return "", "", fmt.Errorf("check member active sms session: %w", err)
	}
	if active {
		return "", "", ErrPixlabSMSActive
	}
	var fee float64
	if err := tx.QueryRowContext(ctx, `
		SELECT member_fee
		FROM xiass_sms_receiver_settings
		WHERE id = 1
		FOR UPDATE`).Scan(&fee); err != nil {
		return "", "", fmt.Errorf("load member sms fee: %w", err)
	}
	if fee <= 0 || fee > 10000 {
		return "", "", infraerrors.InternalServer("SMS_MEMBER_FEE_INVALID", "接码会员价格配置无效")
	}

	sessionID := uuid.NewString()
	var encryptedKey string
	err = tx.QueryRowContext(ctx, `
		WITH next_key AS (
			SELECT id
			FROM xiass_sms_card_keys
			WHERE status = 'queued'
			ORDER BY claim_count ASC, queue_rank ASC, last_claimed_at NULLS FIRST, id
			FOR UPDATE SKIP LOCKED
			LIMIT 1
		)
		UPDATE xiass_sms_card_keys AS card
		SET status = 'active', owner_user_id = $1, session_id = $2,
			consumed_at = NOW(), last_claimed_at = NOW(),
			cleanup_attempted_at = NULL, cleanup_lease_token = NULL, cleanup_lease_until = NULL,
			claim_count = card.claim_count + 1, updated_at = NOW()
		FROM next_key
		WHERE card.id = next_key.id
		RETURNING card.encrypted_key`, userID, sessionID).Scan(&encryptedKey)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", ErrPixlabSMSNoCardKey
	}
	if err != nil {
		return "", "", fmt.Errorf("claim member sms card key: %w", err)
	}

	var balance float64
	err = tx.QueryRowContext(ctx, `
		UPDATE users
		SET balance = balance - $1, updated_at = NOW()
		WHERE id = $2 AND deleted_at IS NULL AND balance >= $1
		RETURNING balance`, fee, userID).Scan(&balance)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", ErrPixlabSMSBalance
	}
	if err != nil {
		return "", "", fmt.Errorf("reserve member sms balance: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO xiass_sms_member_charges (session_id, user_id, amount, status)
		VALUES ($1, $2, $3, 'held')`, sessionID, userID, fee); err != nil {
		return "", "", fmt.Errorf("record member sms charge: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return "", "", fmt.Errorf("commit member sms claim: %w", err)
	}
	s.invalidateBalance(ctx, userID)
	return sessionID, encryptedKey, nil
}

type pixlabSMSQueryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func (s *PixlabSMSService) memberResult(ctx context.Context, userID int64, sourceSessionID string, result *PixlabSMSResult) (*PixlabSMSMemberResult, error) {
	return s.memberResultWithQueryer(ctx, s.db, userID, sourceSessionID, result)
}

func (s *PixlabSMSService) memberResultWithQueryer(ctx context.Context, queryer pixlabSMSQueryer, userID int64, sourceSessionID string, result *PixlabSMSResult) (*PixlabSMSMemberResult, error) {
	if result == nil {
		return nil, infraerrors.ServiceUnavailable("SMS_PROVIDER_INVALID_RESPONSE", "接码服务返回了无效数据")
	}
	var balance float64
	if err := queryer.QueryRowContext(ctx, `
		SELECT balance FROM users WHERE id = $1 AND deleted_at IS NULL`, userID).Scan(&balance); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, infraerrors.NotFound("USER_NOT_FOUND", "用户不存在")
		}
		return nil, fmt.Errorf("load member sms balance: %w", err)
	}
	chargeState := ""
	feeAmount := 0.0
	if err := queryer.QueryRowContext(ctx, `
		SELECT status, amount
		FROM xiass_sms_member_charges
		WHERE session_id = $1 AND user_id = $2`, sourceSessionID, userID).Scan(&chargeState, &feeAmount); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("load member sms charge: %w", err)
	}
	var actionAvailableAt *time.Time
	if result.SessionID != "" {
		var claimedAt time.Time
		err := queryer.QueryRowContext(ctx, `
			SELECT consumed_at
			FROM xiass_sms_card_keys
			WHERE session_id = $1 AND owner_user_id = $2 AND status = 'active'`, result.SessionID, userID).Scan(&claimedAt)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("load member sms action time: %w", err)
		}
		if err == nil {
			availableAt := claimedAt.Add(PixlabSMSMemberMutationDelay)
			actionAvailableAt = &availableAt
		}
	}
	return &PixlabSMSMemberResult{
		SessionID:         result.SessionID,
		Status:            result.Status,
		Number:            result.Number,
		NumberPending:     result.NumberPending,
		Country:           result.Country,
		Code:              result.Code,
		PollSeconds:       result.PollSeconds,
		LocalStop:         result.LocalStop,
		QueuedCount:       0,
		FeeAmount:         feeAmount,
		ChargeState:       chargeState,
		Balance:           balance,
		ActionAvailableAt: actionAvailableAt,
		ExpiresAt:         result.ExpiresAt,
		ServerTime:        result.ServerTime,
	}, nil
}

func (s *PixlabSMSService) invalidateBalance(ctx context.Context, userID int64) {
	if s == nil || s.billingCache == nil || userID <= 0 {
		return
	}
	if err := s.billingCache.InvalidateUserBalance(ctx, userID); err != nil {
		// Cache invalidation must not undo a committed payment operation. The
		// next database-backed read still observes the correct balance.
		return
	}
}

type pixlabSMSProviderResponse struct {
	Success   json.RawMessage `json:"success"`
	Message   string          `json:"message"`
	Error     string          `json:"error"`
	Status    string          `json:"status"`
	Number    string          `json:"number"`
	Country   string          `json:"country"`
	Code      string          `json:"code"`
	CreatedAt string          `json:"created_at"`
}

// Pixlab's payload fields have been observed as both JSON strings and JSON
// numbers. Decode all provider values as raw JSON, then normalize locally so a
// numeric phone number or verification code cannot discard the whole payload.
type pixlabSMSProviderPayload struct {
	Success   json.RawMessage `json:"success"`
	Message   json.RawMessage `json:"message"`
	Error     json.RawMessage `json:"error"`
	Status    json.RawMessage `json:"status"`
	Number    json.RawMessage `json:"number"`
	Country   json.RawMessage `json:"country"`
	Code      json.RawMessage `json:"code"`
	CreatedAt json.RawMessage `json:"created_at"`
}

func (s *PixlabSMSService) callProvider(ctx context.Context, action, cardKey string) (*pixlabSMSProviderResponse, error) {
	body, err := json.Marshal(map[string]string{"cardKey": cardKey})
	if err != nil {
		return nil, fmt.Errorf("encode sms provider request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.baseURL+"/"+action, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create sms provider request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, infraerrors.ServiceUnavailable("SMS_PROVIDER_UNAVAILABLE", "接码服务暂时无法连接，请稍后重试")
	}
	defer func() { _ = resp.Body.Close() }()
	// Transport, provider authentication and rate limits do not establish card state.
	switch resp.StatusCode {
	case http.StatusUnauthorized, http.StatusForbidden:
		return nil, infraerrors.ServiceUnavailable("SMS_PROVIDER_AUTH_FAILED", "接码服务认证失败，请联系管理员")
	case http.StatusTooManyRequests:
		return nil, infraerrors.ServiceUnavailable("SMS_PROVIDER_RATE_LIMITED", "接码服务请求过于频繁，请稍后重试")
	case http.StatusRequestTimeout:
		return nil, infraerrors.ServiceUnavailable("SMS_PROVIDER_UNAVAILABLE", "接码服务请求超时，请稍后重试")
	}
	if resp.StatusCode >= http.StatusInternalServerError {
		return nil, infraerrors.ServiceUnavailable("SMS_PROVIDER_HTTP_ERROR", "接码服务请求失败，请稍后重试")
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, infraerrors.ServiceUnavailable("SMS_PROVIDER_UNAVAILABLE", "读取接码服务响应失败，请稍后重试")
	}
	payload := &pixlabSMSProviderPayload{}
	if len(data) > 0 {
		if err := json.Unmarshal(data, payload); err != nil {
			return nil, infraerrors.ServiceUnavailable("SMS_PROVIDER_INVALID_RESPONSE", "接码服务返回了无效数据")
		}
	}
	provider := &pixlabSMSProviderResponse{
		Success:   payload.Success,
		Message:   pixlabRawText(payload.Message),
		Error:     pixlabRawText(payload.Error),
		Status:    pixlabRawText(payload.Status),
		Number:    pixlabRawText(payload.Number),
		Country:   pixlabRawText(payload.Country),
		Code:      pixlabRawText(payload.Code),
		CreatedAt: pixlabRawText(payload.CreatedAt),
	}
	if pixlabProviderHasTrustedCode(provider) {
		return provider, nil
	}
	definitiveResponse := resp.StatusCode >= http.StatusOK && resp.StatusCode < http.StatusInternalServerError
	if definitiveResponse && isPixlabCardUnusableResponse(provider, action) {
		return nil, infraerrors.BadRequest("SMS_PROVIDER_CARD_UNUSABLE", "接码卡密已不可用，请更换卡密")
	}
	if definitiveResponse && (resp.StatusCode >= http.StatusMultipleChoices || !pixlabSuccess(provider.Success)) &&
		isPixlabTerminalSessionMessage(pixlabProviderMessage(provider)) {
		return provider, infraerrors.BadRequest("SMS_PROVIDER_SESSION_EXPIRED", "接码会话不存在或已结束")
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return provider, infraerrors.ServiceUnavailable("SMS_PROVIDER_HTTP_ERROR", "接码服务请求失败，请稍后重试")
	}
	if !pixlabSuccess(provider.Success) {
		return provider, infraerrors.BadRequest("SMS_PROVIDER_REJECTED", "接码服务未能完成该操作，请稍后重试")
	}
	provider.Status = strings.ToUpper(strings.TrimSpace(provider.Status))
	if provider.Status == "" {
		provider.Status = "WAITING"
	}
	return provider, nil
}

func pixlabSuccess(raw json.RawMessage) bool {
	value := strings.ToLower(strings.TrimSpace(pixlabRawText(raw)))
	return value == "true" || value == "1" || value == "ok" || value == "success"
}

func isPixlabCardUnusableError(err error) bool {
	if infraerrors.Reason(err) == "SMS_PROVIDER_CARD_UNUSABLE" {
		return true
	}
	if infraerrors.Reason(err) != "SMS_PROVIDER_REJECTED" {
		return false
	}
	return isPixlabCardUnusableMessage(infraerrors.Message(err))
}

func isPixlabCardUsageLimitMessage(raw string) bool {
	message := strings.ToLower(strings.TrimSpace(raw))
	compact := strings.NewReplacer(" ", "", "\t", "", "\n", "").Replace(message)
	return strings.Contains(compact, "连续换号上限") ||
		strings.Contains(compact, "卡密使用次数上限") ||
		strings.Contains(message, "card usage limit") ||
		strings.Contains(message, "card limit reached")
}

var pixlabUnusableCardPattern = regexp.MustCompile(`(?i)\b(?:(?:card(?:[ _-]*key)?|cdk) (?:is |has been |already )?(?:invalid|expired|exhausted|used|disabled|revoked|not found|does not exist)|(?:invalid|expired|exhausted|used|disabled|revoked) (?:card(?:[ _-]*key)?|cdk))\b`)

func isPixlabCardUnusableMessage(raw string) bool {
	message := strings.Join(strings.Fields(strings.ToLower(raw)), " ")
	if isPixlabCardUsageLimitMessage(message) || pixlabUnusableCardPattern.MatchString(message) {
		return true
	}
	for _, marker := range []string{"卡密已耗尽", "卡密已用完", "卡密无效", "无效卡密", "卡密不存在", "卡密已过期", "卡密已失效", "卡密已使用", "卡密已被使用", "卡密已禁用"} {
		if strings.Contains(message, marker) {
			return true
		}
	}
	return false
}

func isPixlabCardUnusableResponse(provider *pixlabSMSProviderResponse, action string) bool {
	switch strings.ToUpper(strings.TrimSpace(provider.Status)) {
	case "EXHAUSTED", "CARD_EXHAUSTED", "INVALID_CARD", "CARD_INVALID", "CARD_NOT_FOUND", "CARD_EXPIRED", "CARD_DISABLED", "CARD_USED":
		return true
	}
	return isPixlabCardUnusableMessage(provider.Message) || isPixlabCardUnusableMessage(provider.Error)
}

func isPixlabTerminalSessionMessage(raw string) bool {
	message := strings.ToLower(strings.TrimSpace(raw))
	compact := strings.NewReplacer(" ", "", "\t", "", "\n", "").Replace(message)
	return strings.Contains(message, "session is no longer available") ||
		strings.Contains(message, "session expired") ||
		strings.Contains(compact, "订单异常或未提取号码") ||
		strings.Contains(compact, "会话不存在") ||
		strings.Contains(compact, "会话已过期") ||
		strings.Contains(compact, "号码已失效")
}

func pixlabProviderMessage(provider *pixlabSMSProviderResponse) string {
	if provider == nil {
		return ""
	}
	if message := strings.TrimSpace(provider.Message); message != "" {
		return message
	}
	return strings.TrimSpace(provider.Error)
}

func pixlabRawText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return text
	}
	var number json.Number
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&number); err == nil {
		return number.String()
	}
	var flag bool
	if err := json.Unmarshal(raw, &flag); err == nil {
		if flag {
			return "true"
		}
		return "false"
	}
	return ""
}

func pixlabTerminalStatus(status, code string) string {
	if pixlabHasVerificationCode(code) {
		return "completed"
	}
	switch strings.ToUpper(strings.TrimSpace(status)) {
	case "RECEIVED", "COMPLETED", "USED":
		return "completed"
	case "EXPIRED", "TIMEOUT":
		return "expired"
	case "CANCELLED", "CANCELED":
		return "cancelled"
	case "FAILED", "ERROR":
		return "failed"
	case "EXHAUSTED":
		return "exhausted"
	default:
		return ""
	}
}

func pixlabHasVerificationCode(code string) bool {
	trimmed := strings.TrimSpace(code)
	return trimmed != "" && trimmed != "--"
}

func pixlabProviderHasTrustedCode(provider *pixlabSMSProviderResponse) bool {
	return provider != nil && pixlabSuccess(provider.Success) && pixlabHasVerificationCode(provider.Code)
}
