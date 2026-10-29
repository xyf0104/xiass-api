package service

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"unicode/utf8"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const (
	SMSProviderPixlab = "pixlab"
	SMSProviderGiema  = "giema"

	giemaSMSCardEnvelopePrefix = "xiass-sms-provider:giema:"
	giemaSMSAPIKeySecretName   = "sms_provider_giema_api_key"
)

// SMSProviderSettings reports only whether a Giema credential has been stored.
// It never reads or decrypts the secret for the settings response.
type SMSProviderSettings struct {
	KeyConfigured bool `json:"key_configured"`
}

func (s *PixlabSMSService) GetSMSProviderSettings(ctx context.Context) (*SMSProviderSettings, error) {
	if s == nil || s.db == nil {
		return nil, infraerrors.InternalServer("SMS_RECEIVER_UNAVAILABLE", "接码服务尚未初始化")
	}
	settings := &SMSProviderSettings{}
	err := s.db.QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM security_secrets WHERE key = $1 AND value <> ''
		)`, giemaSMSAPIKeySecretName).Scan(&settings.KeyConfigured)
	if err != nil {
		return nil, infraerrors.InternalServer("SMS_PROVIDER_CONFIGURATION_UNAVAILABLE", "无法读取接码供应商配置")
	}
	return settings, nil
}

func validGiemaSMSAPIKey(key string) bool {
	return key != "" && !strings.ContainsFunc(key, func(r rune) bool { return r < 33 || r > 126 })
}

// GiemaAPIKey is for internal provider requests only. Do not include its return
// value in DTOs, logs or errors; raw storage/encryption errors are also suppressed.
func (s *PixlabSMSService) GiemaAPIKey(ctx context.Context) (string, error) {
	if s == nil || s.db == nil || s.encryptor == nil {
		return "", infraerrors.InternalServer("SMS_RECEIVER_UNAVAILABLE", "接码服务尚未初始化")
	}
	var ciphertext string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM security_secrets WHERE key = $1`, giemaSMSAPIKeySecretName).Scan(&ciphertext)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && ciphertext == "") {
		return "", infraerrors.BadRequest("SMS_PROVIDER_KEY_NOT_CONFIGURED", "请先配置 Giema 接码供应商密钥")
	}
	if err != nil {
		return "", infraerrors.InternalServer("SMS_PROVIDER_CONFIGURATION_UNAVAILABLE", "无法读取接码供应商配置")
	}
	key, err := s.encryptor.Decrypt(ciphertext)
	if err != nil || !validGiemaSMSAPIKey(key) {
		return "", infraerrors.InternalServer("SMS_PROVIDER_CONFIGURATION_UNAVAILABLE", "无法解密或使用接码供应商配置，请重新保存密钥")
	}
	return key, nil
}

// SaveGiemaAPIKey updates the existing encrypted-secret namespace. Blank input
// is rejected rather than silently deleting a credential used by active sessions.
func (s *PixlabSMSService) SaveGiemaAPIKey(ctx context.Context, key string) error {
	if s == nil || s.db == nil || s.encryptor == nil {
		return infraerrors.InternalServer("SMS_RECEIVER_UNAVAILABLE", "接码服务尚未初始化")
	}
	key = strings.TrimSpace(key)
	if !validGiemaSMSAPIKey(key) {
		return infraerrors.BadRequest("SMS_PROVIDER_KEY_INVALID", "请输入有效的 Giema 接码供应商密钥")
	}
	ciphertext, err := s.encryptor.Encrypt(key)
	if err != nil || ciphertext == "" {
		return infraerrors.InternalServer("SMS_PROVIDER_CONFIGURATION_UNAVAILABLE", "无法加密接码供应商配置")
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO security_secrets (key, value)
		VALUES ($1, $2)
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = NOW()`, giemaSMSAPIKeySecretName, ciphertext)
	if err != nil {
		return infraerrors.InternalServer("SMS_PROVIDER_CONFIGURATION_UNAVAILABLE", "无法保存接码供应商配置")
	}
	return nil
}

// AddCardKeysForProvider binds a fingerprint permanently to its provider during
// import. The batch rolls back on a conflicting binding or any storage failure.
// Explicit re-addition preserves history and provider state, and cannot release
// a pending provider operation back into the acquisition queue.
func (s *PixlabSMSService) AddCardKeysForProvider(ctx context.Context, raw, provider string) (int, *PixlabSMSQueueStatus, error) {
	if s == nil || s.db == nil || s.encryptor == nil {
		return 0, nil, infraerrors.InternalServer("SMS_RECEIVER_UNAVAILABLE", "接码服务尚未初始化")
	}
	provider = strings.TrimSpace(provider)
	if provider != SMSProviderPixlab && provider != SMSProviderGiema {
		return 0, nil, infraerrors.BadRequest("SMS_PROVIDER_INVALID", "不支持的接码供应商")
	}
	keys := normalizePixlabCardKeys(raw)
	if len(keys) == 0 {
		return 0, nil, infraerrors.BadRequest("SMS_CARD_KEY_REQUIRED", "请至少输入一个有效接码卡密")
	}
	if len(keys) > 500 {
		return 0, nil, infraerrors.BadRequest("SMS_CARD_KEY_LIMIT", "单次最多添加 500 个接码卡密")
	}
	for _, key := range keys {
		if strings.HasPrefix(key, giemaSMSCardEnvelopePrefix) {
			return 0, nil, infraerrors.BadRequest("SMS_CARD_KEY_INVALID", "请输入原始接码卡密")
		}
	}
	if provider == SMSProviderGiema {
		if _, err := s.GiemaAPIKey(ctx); err != nil {
			return 0, nil, err
		}
		for _, key := range keys {
			if !utf8.ValidString(key) || utf8.RuneCountInString(key) < 1 || utf8.RuneCountInString(key) > 64 {
				return 0, nil, infraerrors.BadRequest("SMS_CARD_KEY_INVALID", "Giema 卡密长度必须为 1-64 个字符")
			}
		}
	}

	storageError := func() error {
		return infraerrors.InternalServer("SMS_CARD_KEY_STORE_FAILED", "无法保存接码卡密，请稍后重试")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, nil, storageError()
	}
	defer func() { _ = tx.Rollback() }()

	inserted := 0
	for _, key := range keys {
		plaintext := key
		if provider == SMSProviderGiema {
			plaintext = giemaSMSCardEnvelopePrefix + key
		}
		ciphertext, err := s.encryptor.Encrypt(plaintext)
		if err != nil || ciphertext == "" {
			return 0, nil, infraerrors.InternalServer("SMS_CARD_KEY_ENCRYPT_FAILED", "无法加密接码卡密")
		}
		fingerprint := pixlabCardKeyFingerprint(key)
		var rowID int64
		err = tx.QueryRowContext(ctx, `
			INSERT INTO xiass_sms_card_keys AS card (encrypted_key, key_fingerprint, provider, queue_rank)
			VALUES ($1, $2, $3, (SELECT COALESCE(MAX(queue_rank), 0) + 1 FROM xiass_sms_card_keys))
			ON CONFLICT (key_fingerprint) DO UPDATE SET
				status = 'queued', queue_rank = EXCLUDED.queue_rank,
				provider_state = '', provider_pending = false,
				owner_user_id = NULL, session_id = NULL, workflow_scope = '',
				consumed_at = NULL, cleanup_attempted_at = NULL,
				cleanup_lease_token = NULL, cleanup_lease_until = NULL, updated_at = NOW()
			WHERE card.provider = EXCLUDED.provider
				AND card.status = 'exhausted' AND NOT card.provider_pending
			RETURNING id`, ciphertext, fingerprint, provider).Scan(&rowID)
		if errors.Is(err, sql.ErrNoRows) {
			// ON CONFLICT locks the existing row even when its WHERE clause is
			// false. Inspect the canonical binding in the same transaction.
			var existingProvider string
			if err := tx.QueryRowContext(ctx, `
				SELECT provider FROM xiass_sms_card_keys WHERE key_fingerprint = $1 FOR UPDATE`, fingerprint).Scan(&existingProvider); err != nil {
				return 0, nil, storageError()
			}
			if existingProvider != provider {
				return 0, nil, infraerrors.Conflict("SMS_CARD_KEY_PROVIDER_CONFLICT", "卡密已绑定其他接码供应商，不能重新绑定")
			}
			continue
		}
		if err != nil {
			return 0, nil, storageError()
		}
		inserted++
	}
	status := &PixlabSMSQueueStatus{}
	if err := tx.QueryRowContext(ctx, `
		SELECT COUNT(*) FILTER (WHERE status = 'queued'), COUNT(*) FILTER (WHERE status = 'active')
		FROM xiass_sms_card_keys`).Scan(&status.QueuedCount, &status.ActiveCount); err != nil {
		return 0, nil, storageError()
	}
	if err := tx.Commit(); err != nil {
		return 0, nil, storageError()
	}
	return inserted, status, nil
}
