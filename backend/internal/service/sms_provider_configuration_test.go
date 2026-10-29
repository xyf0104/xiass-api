package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"regexp"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/stretchr/testify/require"
)

func TestSMSProviderSettingsReportsPresenceWithoutSecret(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	mock.ExpectQuery(regexp.QuoteMeta(`
		SELECT EXISTS (
			SELECT 1 FROM security_secrets WHERE key = $1 AND value <> ''
		)`)).WithArgs(giemaSMSAPIKeySecretName).
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))

	svc := newPixlabSMSService(db, pixlabSMSPrefixEncryptor{}, nil, "")
	settings, err := svc.GetSMSProviderSettings(context.Background())
	require.NoError(t, err)
	require.True(t, settings.KeyConfigured)
	body, err := json.Marshal(settings)
	require.NoError(t, err)
	require.NotContains(t, string(body), "secret")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestSaveGiemaAPIKeyEncryptsAndUpsertsWithoutReturningPlaintext(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	mock.ExpectExec(regexp.QuoteMeta(`
		INSERT INTO security_secrets (key, value)
		VALUES ($1, $2)
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = NOW()`)).
		WithArgs(giemaSMSAPIKeySecretName, "enc:provider-secret").
		WillReturnResult(sqlmock.NewResult(0, 1))

	svc := newPixlabSMSService(db, pixlabSMSPrefixEncryptor{}, nil, "")
	require.NoError(t, svc.SaveGiemaAPIKey(context.Background(), " provider-secret "))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestGiemaAPIKeyDecryptsStoredSecretAndSanitizesFailures(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	mock.ExpectQuery(regexp.QuoteMeta(`SELECT value FROM security_secrets WHERE key = $1`)).
		WithArgs(giemaSMSAPIKeySecretName).
		WillReturnRows(sqlmock.NewRows([]string{"value"}).AddRow("enc:provider-secret"))

	svc := newPixlabSMSService(db, pixlabSMSPrefixEncryptor{}, nil, "")
	key, err := svc.GiemaAPIKey(context.Background())
	require.NoError(t, err)
	require.Equal(t, "provider-secret", key)
	require.NoError(t, mock.ExpectationsWereMet())

	db2, mock2, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db2.Close() }()
	mock2.ExpectQuery(regexp.QuoteMeta(`SELECT value FROM security_secrets WHERE key = $1`)).
		WithArgs(giemaSMSAPIKeySecretName).
		WillReturnRows(sqlmock.NewRows([]string{"value"}).AddRow("enc: "))
	svc2 := newPixlabSMSService(db2, pixlabSMSPrefixEncryptor{}, nil, "")
	_, err = svc2.GiemaAPIKey(context.Background())
	require.Error(t, err)
	require.NotContains(t, err.Error(), "provider-secret")
	require.Equal(t, "SMS_PROVIDER_CONFIGURATION_UNAVAILABLE", infraerrors.Reason(err))
	require.NoError(t, mock2.ExpectationsWereMet())
}

func TestAddCardKeysForProviderGiemaEncryptsEnvelopeAndBindsProvider(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	mock.ExpectQuery(regexp.QuoteMeta(`SELECT value FROM security_secrets WHERE key = $1`)).
		WithArgs(giemaSMSAPIKeySecretName).
		WillReturnRows(sqlmock.NewRows([]string{"value"}).AddRow("enc:provider-secret"))
	mock.ExpectBegin()
	mock.ExpectQuery("INSERT INTO xiass_sms_card_keys AS card").
		WithArgs("enc:"+giemaSMSCardEnvelopePrefix+"giema-card", pixlabCardKeyFingerprint("giema-card"), SMSProviderGiema).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(170)))
	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FILTER ").
		WillReturnRows(sqlmock.NewRows([]string{"queued", "active"}).AddRow(int64(1), int64(0)))
	mock.ExpectCommit()

	svc := newPixlabSMSService(db, pixlabSMSPrefixEncryptor{}, nil, "")
	inserted, status, err := svc.AddCardKeysForProvider(context.Background(), "giema-card", SMSProviderGiema)
	require.NoError(t, err)
	require.Equal(t, 1, inserted)
	require.Equal(t, int64(1), status.QueuedCount)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestAddCardKeysForProviderRejectsCrossProviderDuplicateAndRollsBack(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	mock.ExpectBegin()
	mock.ExpectQuery("INSERT INTO xiass_sms_card_keys AS card").
		WithArgs("enc:legacy-card", pixlabCardKeyFingerprint("legacy-card"), SMSProviderPixlab).
		WillReturnError(sql.ErrNoRows)
	mock.ExpectQuery(regexp.QuoteMeta(`
			SELECT provider FROM xiass_sms_card_keys WHERE key_fingerprint = $1 FOR UPDATE`)).
		WithArgs(pixlabCardKeyFingerprint("legacy-card")).
		WillReturnRows(sqlmock.NewRows([]string{"provider"}).AddRow(SMSProviderGiema))
	mock.ExpectRollback()

	svc := newPixlabSMSService(db, pixlabSMSPrefixEncryptor{}, nil, "")
	_, _, err = svc.AddCardKeysForProvider(context.Background(), "legacy-card", SMSProviderPixlab)
	require.Error(t, err)
	require.Equal(t, http.StatusConflict, infraerrors.Code(err))
	require.Equal(t, "SMS_CARD_KEY_PROVIDER_CONFLICT", infraerrors.Reason(err))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestAddCardKeysForProviderRequeuesOnlySameProviderExhaustedCard(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	mock.ExpectBegin()
	mock.ExpectQuery("INSERT INTO xiass_sms_card_keys AS card").
		WithArgs("enc:pixlab-card", pixlabCardKeyFingerprint("pixlab-card"), SMSProviderPixlab).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(144)))
	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FILTER ").
		WillReturnRows(sqlmock.NewRows([]string{"queued", "active"}).AddRow(int64(1), int64(0)))
	mock.ExpectCommit()

	svc := newPixlabSMSService(db, pixlabSMSPrefixEncryptor{}, nil, "")
	inserted, _, err := svc.AddCardKeysForProvider(context.Background(), "pixlab-card", SMSProviderPixlab)
	require.NoError(t, err)
	require.Equal(t, 1, inserted)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestAddCardKeysForProviderDoesNotRequeuePendingDuplicate(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	mock.ExpectBegin()
	mock.ExpectQuery("INSERT INTO xiass_sms_card_keys AS card").
		WithArgs("enc:pixlab-card", pixlabCardKeyFingerprint("pixlab-card"), SMSProviderPixlab).
		WillReturnError(sql.ErrNoRows)
	mock.ExpectQuery(regexp.QuoteMeta(`
			SELECT provider FROM xiass_sms_card_keys WHERE key_fingerprint = $1 FOR UPDATE`)).
		WithArgs(pixlabCardKeyFingerprint("pixlab-card")).
		WillReturnRows(sqlmock.NewRows([]string{"provider"}).AddRow(SMSProviderPixlab))
	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FILTER ").
		WillReturnRows(sqlmock.NewRows([]string{"queued", "active"}).AddRow(int64(0), int64(1)))
	mock.ExpectCommit()

	svc := newPixlabSMSService(db, pixlabSMSPrefixEncryptor{}, nil, "")
	inserted, status, err := svc.AddCardKeysForProvider(context.Background(), "pixlab-card", SMSProviderPixlab)
	require.NoError(t, err)
	require.Zero(t, inserted)
	require.Equal(t, int64(1), status.ActiveCount)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestAddCardKeysForProviderRequiresGiemaKeyBeforeImport(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	mock.ExpectQuery(regexp.QuoteMeta(`SELECT value FROM security_secrets WHERE key = $1`)).
		WithArgs(giemaSMSAPIKeySecretName).
		WillReturnError(sql.ErrNoRows)

	svc := newPixlabSMSService(db, pixlabSMSPrefixEncryptor{}, nil, "")
	_, _, err = svc.AddCardKeysForProvider(context.Background(), "giema-card", SMSProviderGiema)
	require.Error(t, err)
	require.Equal(t, "SMS_PROVIDER_KEY_NOT_CONFIGURED", infraerrors.Reason(err))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestAddCardKeysForProviderRejectsInvalidProviderWithoutDatabaseAccess(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	svc := newPixlabSMSService(db, pixlabSMSPrefixEncryptor{}, nil, "")
	_, _, err = svc.AddCardKeysForProvider(context.Background(), "card", "unknown")
	require.Error(t, err)
	require.Equal(t, "SMS_PROVIDER_INVALID", infraerrors.Reason(err))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestSMSProviderConstantsAndEnvelopeAreStable(t *testing.T) {
	require.Equal(t, "pixlab", SMSProviderPixlab)
	require.Equal(t, "giema", SMSProviderGiema)
	require.Equal(t, "xiass-sms-provider:giema:", giemaSMSCardEnvelopePrefix)
	require.False(t, strings.Contains(giemaSMSCardEnvelopePrefix, "secret"))
}
