package service

import (
	"context"
	"database/sql/driver"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/stretchr/testify/require"
)

type giemaRuntimeRecordingEncryptor struct {
	plaintexts []string
}

func (e *giemaRuntimeRecordingEncryptor) Encrypt(value string) (string, error) {
	e.plaintexts = append(e.plaintexts, value)
	return "enc:" + value, nil
}

func (*giemaRuntimeRecordingEncryptor) Decrypt(value string) (string, error) {
	return strings.TrimPrefix(value, "enc:"), nil
}

type giemaRuntimeStateArg struct{}

func (giemaRuntimeStateArg) Match(value driver.Value) bool {
	text, ok := value.(string)
	return ok && strings.HasPrefix(text, "enc:{") && strings.Contains(text, `"version":1`)
}

func giemaRuntimeStateFixture(session string, attempt int, pollSeconds int) string {
	latest := &GiemaSMSResponse{
		Session:     GiemaSMSSession{Token: session},
		Status:      "wait",
		PhoneNumber: giemaSMSPtr("15550000000"),
		PhoneReady:  giemaSMSPtr(true),
		Attempt:     giemaSMSPtr(attempt),
	}
	state := &giemaSMSPersistedState{
		Version:     1,
		Session:     session,
		Latest:      latest,
		PollSeconds: pollSeconds,
	}
	payload, _ := json.Marshal(state)
	return "enc:" + string(payload)
}

func expectGiemaRuntimeStateLoad(mock sqlmock.Sqlmock, sessionArg any, ownerID int64, encryptedState string, pending bool) {
	mock.ExpectQuery("SELECT provider, provider_state, provider_pending").
		WithArgs(sessionArg, ownerID).
		WillReturnRows(sqlmock.NewRows([]string{"provider", "provider_state", "provider_pending"}).AddRow(SMSProviderGiema, encryptedState, pending))
}

func expectGiemaRuntimeLock(mock sqlmock.Sqlmock) {
	mock.ExpectExec("SELECT pg_advisory_lock").WithArgs(sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))
}

func expectGiemaRuntimeUnlock(mock sqlmock.Sqlmock) {
	mock.ExpectExec("SELECT pg_advisory_unlock").WithArgs(sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))
}

func expectGiemaRuntimeStateSave(mock sqlmock.Sqlmock, ownerID int64) {
	mock.ExpectExec("UPDATE xiass_sms_card_keys").
		WithArgs(sqlmock.AnyArg(), ownerID, giemaRuntimeStateArg{}).
		WillReturnResult(sqlmock.NewResult(0, 1))
}

func expectGiemaRuntimeQueue(mock sqlmock.Sqlmock, ownerID int64, queued, active int64) {
	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FILTER").
		WithArgs(ownerID).
		WillReturnRows(sqlmock.NewRows([]string{"queued", "active"}).AddRow(queued, active))
}

func TestGiemaRuntimeRedeemUsesRealStartAdapterAndEncryptedState(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	encryptor := &giemaRuntimeRecordingEncryptor{}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			require.Equal(t, "/api/v1/store/config", r.URL.Path)
			_, _ = w.Write([]byte(`{"provider":"sms688_manual","poll_seconds":5,"rotate_seconds":0,"max_attempts":0,"maintenance":false,"protocol_locked":true}`))
			return
		}
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "/api/v1/store/start", r.URL.Path)
		_, _ = w.Write([]byte(`{"session":"giema-session","status":"active","phoneReady":true,"phoneNumber":"15550000000","attempt":1}`))
	}))
	defer server.Close()

	mock.ExpectQuery("RETURNING card\\.encrypted_key").
		WithArgs(int64(42), sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"encrypted_key"}).AddRow("enc:" + giemaSMSCardEnvelopePrefix + "giema-card"))
	expectGiemaRuntimeLock(mock)
	expectGiemaRuntimeStateLoad(mock, sqlmock.AnyArg(), 42, "", false)
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT value FROM security_secrets WHERE key = $1`)).
		WithArgs(giemaSMSAPIKeySecretName).
		WillReturnRows(sqlmock.NewRows([]string{"value"}).AddRow("enc:provider-key"))
	expectGiemaRuntimeStateSave(mock, 42)
	expectGiemaRuntimeStateSave(mock, 42)
	expectGiemaRuntimeQueue(mock, 42, 2, 1)
	expectGiemaRuntimeUnlock(mock)

	svc := newPixlabSMSService(db, encryptor, server.Client(), "http://unused-pixlab")
	svc.giemaBaseURL = server.URL + "/api/v1"
	result, err := svc.Redeem(context.Background(), 42)
	require.NoError(t, err)
	require.Equal(t, SMSProviderGiema, result.provider)
	require.Equal(t, "ACTIVE", result.Status)
	require.Equal(t, "15550000000", result.Number)
	require.False(t, result.NumberPending)
	require.Equal(t, 5, result.PollSeconds)
	require.NotEmpty(t, result.SessionID)
	require.Len(t, encryptor.plaintexts, 2)
	var intent, completed map[string]any
	require.NoError(t, json.Unmarshal([]byte(encryptor.plaintexts[0]), &intent))
	require.NoError(t, json.Unmarshal([]byte(encryptor.plaintexts[1]), &completed))
	require.Equal(t, "start", intent["in_flight_action"])
	require.NotContains(t, encryptor.plaintexts[0], "giema-card")
	require.NotContains(t, encryptor.plaintexts[1], "giema-card")
	_, hasInFlightAction := completed["in_flight_action"]
	require.False(t, hasInFlightAction)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestGiemaRuntimeChangeCallsRotateWithoutCancelOrRedeem(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	paths := make([]string, 0, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		require.Equal(t, "/api/v1/store/rotate", r.URL.Path)
		_, _ = w.Write([]byte(`{"session":"giema-session","status":"active","phoneReady":true,"phoneNumber":"15550000001","attempt":2}`))
	}))
	defer server.Close()

	state := giemaRuntimeStateFixture("giema-session", 1, 5)
	mock.ExpectQuery(`SELECT provider FROM xiass_sms_card_keys`).
		WithArgs("giema-session", int64(42), "").
		WillReturnRows(sqlmock.NewRows([]string{"provider"}).AddRow(SMSProviderGiema))
	mock.ExpectQuery(`SELECT card\.encrypted_key, card\.consumed_at`).
		WithArgs("giema-session", int64(42), false, "").
		WillReturnRows(sqlmock.NewRows([]string{"encrypted_key", "consumed_at"}).AddRow("enc:"+giemaSMSCardEnvelopePrefix+"giema-card", time.Now()))
	expectGiemaRuntimeLock(mock)
	expectGiemaRuntimeStateLoad(mock, "giema-session", 42, state, false)
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT value FROM security_secrets WHERE key = $1`)).
		WithArgs(giemaSMSAPIKeySecretName).
		WillReturnRows(sqlmock.NewRows([]string{"value"}).AddRow("enc:provider-key"))
	expectGiemaRuntimeStateSave(mock, 42)
	expectGiemaRuntimeStateSave(mock, 42)
	expectGiemaRuntimeQueue(mock, 42, 2, 1)
	expectGiemaRuntimeUnlock(mock)

	svc := newPixlabSMSService(db, pixlabSMSPrefixEncryptor{}, server.Client(), "http://unused-pixlab")
	svc.giemaBaseURL = server.URL + "/api/v1"
	result, err := svc.ChangeNumber(context.Background(), 42, "giema-session")
	require.NoError(t, err)
	require.Equal(t, SMSProviderGiema, result.provider)
	require.Equal(t, "15550000001", result.Number)
	require.Equal(t, "giema-session", result.SessionID)
	require.Equal(t, []string{"/api/v1/store/rotate"}, paths)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestGiemaRuntimeCancelQuarantinesWithoutRelease(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v1/store/status", r.URL.Path)
		_, _ = w.Write([]byte(`{"session":"giema-session","status":"wait","phoneReady":true,"phoneNumber":"15550000000","attempt":1}`))
	}))
	defer server.Close()

	state := giemaRuntimeStateFixture("giema-session", 1, 5)
	mock.ExpectQuery(`SELECT card\.encrypted_key, card\.consumed_at`).
		WithArgs("giema-session", int64(42), false, "").
		WillReturnRows(sqlmock.NewRows([]string{"encrypted_key", "consumed_at"}).AddRow("enc:"+giemaSMSCardEnvelopePrefix+"giema-card", time.Now()))
	expectGiemaRuntimeLock(mock)
	expectGiemaRuntimeStateLoad(mock, "giema-session", 42, state, false)
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT value FROM security_secrets WHERE key = $1`)).
		WithArgs(giemaSMSAPIKeySecretName).
		WillReturnRows(sqlmock.NewRows([]string{"value"}).AddRow("enc:provider-key"))
	expectGiemaRuntimeStateSave(mock, 42)
	expectGiemaRuntimeStateSave(mock, 42)
	expectGiemaRuntimeQueue(mock, 42, 2, 1)
	mock.ExpectExec("SET provider_pending = true").
		WithArgs("giema-session", int64(42)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	expectGiemaRuntimeUnlock(mock)

	svc := newPixlabSMSService(db, pixlabSMSPrefixEncryptor{}, server.Client(), "http://unused-pixlab")
	svc.giemaBaseURL = server.URL + "/api/v1"
	result, err := svc.Cancel(context.Background(), 42, "giema-session")
	require.NoError(t, err)
	require.Equal(t, SMSProviderGiema, result.provider)
	require.Equal(t, "CANCELLED", result.Status)
	require.True(t, result.LocalStop)
	require.True(t, result.NumberPending)
	require.Empty(t, result.SessionID)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestGiemaRuntimeRejectsEnvelopeWhenDatabaseProviderIsPixlab(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	mock.ExpectQuery(`SELECT card\.encrypted_key, card\.consumed_at`).
		WithArgs("giema-session", int64(42), false, "").
		WillReturnRows(sqlmock.NewRows([]string{"encrypted_key", "consumed_at"}).AddRow("enc:"+giemaSMSCardEnvelopePrefix+"giema-card", time.Now()))
	expectGiemaRuntimeLock(mock)
	mock.ExpectQuery("SELECT provider, provider_state, provider_pending").
		WithArgs("giema-session", int64(42)).
		WillReturnRows(sqlmock.NewRows([]string{"provider", "provider_state", "provider_pending"}).AddRow(SMSProviderPixlab, "", false))
	expectGiemaRuntimeUnlock(mock)

	svc := newPixlabSMSService(db, pixlabSMSPrefixEncryptor{}, nil, "")
	_, err = svc.Check(context.Background(), 42, "giema-session")
	require.Error(t, err)
	require.Equal(t, "SMS_CARD_KEY_PROVIDER_CONFLICT", infraerrors.Reason(err))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestGiemaRuntimeStateNeverPersistsVerificationCode(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	encryptor := &giemaRuntimeRecordingEncryptor{}
	mock.ExpectExec("UPDATE xiass_sms_card_keys").
		WithArgs("session", int64(42), sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))

	state := &giemaSMSPersistedState{
		Version: 1,
		Session: "provider-session",
		Latest:  &GiemaSMSResponse{Code: "654321", Status: "ok"},
	}
	svc := &PixlabSMSService{db: db, encryptor: encryptor}
	require.NoError(t, svc.saveGiemaState(context.Background(), "session", 42, state))
	require.Len(t, encryptor.plaintexts, 1)
	require.NotContains(t, encryptor.plaintexts[0], "654321")
	require.NotContains(t, encryptor.plaintexts[0], `"code"`)
	require.Equal(t, "654321", state.Latest.Code, "persistence must not erase the response awaiting delivery")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestGiemaRuntimeDeliversCodeAndSettlesSession(t *testing.T) {
	for _, member := range []bool{false, true} {
		name := "oauth"
		if member {
			name = "member"
		}
		t.Run(name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer func() { _ = db.Close() }()
			encryptor := &giemaRuntimeRecordingEncryptor{}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				require.Equal(t, "/api/v1/store/status", r.URL.Path)
				_, _ = w.Write([]byte(`{"orderId":123,"platform":"sms688_manual","provider":"sms688_manual","status":"ok","phone":"15550000000","phoneNumber":"15550000000","code":"654321","consumed":true,"quotaCharged":true,"attempt":4,"attemptCount":4,"maxAttempts":5,"switchInSeconds":0,"canChange":false,"canRelease":false,"phoneReady":true,"codeExpiresAt":1900000000.086813,"codeExpired":false,"codePending":false,"needsPhoneChange":false,"waitingCancellation":false,"session":"upstream-session","remainingAttempts":1,"attemptsExhausted":false}`))
			}))
			defer server.Close()

			expectGiemaRuntimeLock(mock)
			expectGiemaRuntimeStateLoad(mock, "session", 42, giemaRuntimeStateFixture("upstream-session", 1, 5), false)
			mock.ExpectQuery("SELECT value FROM security_secrets").WithArgs(giemaSMSAPIKeySecretName).
				WillReturnRows(sqlmock.NewRows([]string{"value"}).AddRow("enc:provider-key"))
			expectGiemaRuntimeStateSave(mock, 42)
			expectGiemaRuntimeStateSave(mock, 42)
			expectPixlabSMSSettlement(mock, 42, 170, member, pixlabSettlementCapture, true)
			expectGiemaRuntimeQueue(mock, 42, 2, 0)
			expectGiemaRuntimeUnlock(mock)

			svc := newPixlabSMSService(db, encryptor, server.Client(), "http://unused-pixlab")
			svc.giemaBaseURL = server.URL + "/api/v1"
			result, err := svc.runGiemaSession(context.Background(), 42, "session", "card", "check", member, true)
			require.NoError(t, err)
			require.Equal(t, "RECEIVED", result.Status)
			require.Equal(t, "654321", result.Code)
			require.Equal(t, "15550000000", result.Number)
			require.Empty(t, result.SessionID)
			require.False(t, result.NumberPending)
			require.Len(t, encryptor.plaintexts, 3)
			for _, persisted := range encryptor.plaintexts[:2] {
				require.NotContains(t, persisted, "654321")
				require.NotContains(t, persisted, `"code"`)
			}
			// The existing short-lived, encrypted delivery cache remains available
			// if the browser loses this successful response.
			require.Contains(t, encryptor.plaintexts[2], "654321")
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestGiemaRuntimeAmbiguousMutationReconcilesBeforeRetry(t *testing.T) {
	for _, failedAction := range []string{"start", "rotate"} {
		t.Run(failedAction, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer func() { _ = db.Close() }()
			encryptor := &giemaRuntimeRecordingEncryptor{}
			state := emptyGiemaSMSState()
			require.NoError(t, json.Unmarshal([]byte(strings.TrimPrefix(giemaRuntimeStateFixture("provider-session", 2, 5), "enc:")), state))
			state.InFlightAction = failedAction
			state.ExpectedAttempt = giemaSMSPtr(2)
			encoded, err := json.Marshal(state)
			require.NoError(t, err)

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				require.Equal(t, "/api/v1/store/status", r.URL.Path)
				w.WriteHeader(http.StatusServiceUnavailable)
			}))
			defer server.Close()
			expectGiemaRuntimeLock(mock)
			expectGiemaRuntimeStateLoad(mock, "session", 42, "enc:"+string(encoded), false)
			mock.ExpectQuery("SELECT value FROM security_secrets").WithArgs(giemaSMSAPIKeySecretName).
				WillReturnRows(sqlmock.NewRows([]string{"value"}).AddRow("enc:provider-key"))
			expectGiemaRuntimeStateSave(mock, 42)
			expectGiemaRuntimeUnlock(mock)

			svc := newPixlabSMSService(db, encryptor, server.Client(), "http://unused-pixlab")
			svc.giemaBaseURL = server.URL + "/api/v1"
			_, err = svc.runGiemaSession(context.Background(), 42, "session", "card", "change", false, false)
			require.Error(t, err)
			require.Len(t, encryptor.plaintexts, 1)
			var persisted giemaSMSPersistedState
			require.NoError(t, json.Unmarshal([]byte(encryptor.plaintexts[0]), &persisted))
			require.Equal(t, failedAction, persisted.InFlightAction)
			require.Equal(t, 2, *persisted.ExpectedAttempt)
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestGiemaRuntimeRateLimitPersistsAndBlocksEarlyRetry(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	encryptor := &giemaRuntimeRecordingEncryptor{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v1/store/status", r.URL.Path)
		w.Header().Set("Retry-After", "120")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer server.Close()

	expectGiemaRuntimeLock(mock)
	expectGiemaRuntimeStateLoad(mock, "session", 42, giemaRuntimeStateFixture("provider-session", 2, 5), false)
	mock.ExpectQuery("SELECT value FROM security_secrets").WithArgs(giemaSMSAPIKeySecretName).
		WillReturnRows(sqlmock.NewRows([]string{"value"}).AddRow("enc:provider-key"))
	expectGiemaRuntimeStateSave(mock, 42)
	expectGiemaRuntimeStateSave(mock, 42)
	expectGiemaRuntimeUnlock(mock)

	svc := newPixlabSMSService(db, encryptor, server.Client(), "http://unused-pixlab")
	svc.giemaBaseURL = server.URL + "/api/v1"
	_, err = svc.runGiemaSession(context.Background(), 42, "session", "card", "check", false, false)
	require.Equal(t, "SMS_PROVIDER_RATE_LIMITED", infraerrors.Reason(err))
	require.Len(t, encryptor.plaintexts, 2)
	var persisted giemaSMSPersistedState
	require.NoError(t, json.Unmarshal([]byte(encryptor.plaintexts[1]), &persisted))
	require.WithinDuration(t, time.Now().Add(120*time.Second), persisted.RetryNotBefore, 5*time.Second)

	expectGiemaRuntimeLock(mock)
	expectGiemaRuntimeStateLoad(mock, "session", 42, "enc:"+encryptor.plaintexts[1], false)
	expectGiemaRuntimeUnlock(mock)
	_, err = svc.runGiemaSession(context.Background(), 42, "session", "card", "check", false, false)
	require.Equal(t, "SMS_PROVIDER_RATE_LIMITED", infraerrors.Reason(err))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestGiemaRuntimeHonorsProviderSwitchCountdown(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	encryptor := &giemaRuntimeRecordingEncryptor{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v1/store/status", r.URL.Path)
		_, _ = w.Write([]byte(`{"status":"active","phone":"15550000000","attempt":2,"switchInSeconds":120,"canChange":true}`))
	}))
	defer server.Close()

	expectGiemaRuntimeLock(mock)
	expectGiemaRuntimeStateLoad(mock, "session", 42, giemaRuntimeStateFixture("provider-session", 2, 5), false)
	mock.ExpectQuery("SELECT value FROM security_secrets").WithArgs(giemaSMSAPIKeySecretName).
		WillReturnRows(sqlmock.NewRows([]string{"value"}).AddRow("enc:provider-key"))
	expectGiemaRuntimeStateSave(mock, 42)
	expectGiemaRuntimeStateSave(mock, 42)
	expectGiemaRuntimeQueue(mock, 42, 1, 1)
	expectGiemaRuntimeUnlock(mock)
	svc := newPixlabSMSService(db, encryptor, server.Client(), "http://unused-pixlab")
	svc.giemaBaseURL = server.URL + "/api/v1"
	_, err = svc.runGiemaSession(context.Background(), 42, "session", "card", "check", false, false)
	require.NoError(t, err)
	var persisted giemaSMSPersistedState
	require.NoError(t, json.Unmarshal([]byte(encryptor.plaintexts[1]), &persisted))
	require.WithinDuration(t, time.Now().Add(120*time.Second), persisted.RotateNotBefore, 5*time.Second)

	expectGiemaRuntimeLock(mock)
	expectGiemaRuntimeStateLoad(mock, "session", 42, "enc:"+encryptor.plaintexts[1], false)
	expectGiemaRuntimeUnlock(mock)
	_, err = svc.runGiemaSession(context.Background(), 42, "session", "card", "change", false, false)
	require.Equal(t, "SMS_PROVIDER_ROTATE_WAIT", infraerrors.Reason(err))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestGiemaRuntimeQuarantinedSessionCanOnlyQueryStatus(t *testing.T) {
	for _, action := range []string{"redeem", "resume", "change"} {
		t.Run(action, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer func() { _ = db.Close() }()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				require.Equal(t, "/api/v1/store/status", r.URL.Path, "quarantine must survive missing vendor session data")
				_, _ = w.Write([]byte(`{"status":"wait","phone":"15550000000","attempt":2}`))
			}))
			defer server.Close()
			state := &giemaSMSPersistedState{Version: 1, PollSeconds: 5}
			encoded, err := json.Marshal(state)
			require.NoError(t, err)
			expectGiemaRuntimeLock(mock)
			expectGiemaRuntimeStateLoad(mock, "session", 42, "enc:"+string(encoded), true)
			mock.ExpectQuery("SELECT value FROM security_secrets").WithArgs(giemaSMSAPIKeySecretName).
				WillReturnRows(sqlmock.NewRows([]string{"value"}).AddRow("enc:provider-key"))
			expectGiemaRuntimeStateSave(mock, 42)
			expectGiemaRuntimeStateSave(mock, 42)
			expectGiemaRuntimeQueue(mock, 42, 2, 1)
			expectGiemaRuntimeUnlock(mock)

			svc := newPixlabSMSService(db, pixlabSMSPrefixEncryptor{}, server.Client(), "http://unused-pixlab")
			svc.giemaBaseURL = server.URL + "/api/v1"
			result, err := svc.runGiemaSession(context.Background(), 42, "session", "card", action, false, false)
			require.NoError(t, err)
			require.True(t, result.LocalStop)
			require.True(t, result.NumberPending)
			require.Empty(t, result.Number, "a stopped workflow must not receive the old number again")
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestGiemaWorkflowChangeSkipsProviderConfirmedUnusableCard(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v1/store/rotate", r.URL.Path)
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"detail":"card disabled"}`))
	}))
	defer server.Close()
	const scope = "workflow-giema-001"
	expectPixlabWorkflowProvider(mock, "session", 42, scope, SMSProviderGiema)
	mock.ExpectQuery(`SELECT card\.encrypted_key, card\.consumed_at`).
		WithArgs("session", int64(42), false, scope).
		WillReturnRows(sqlmock.NewRows([]string{"encrypted_key", "consumed_at"}).AddRow("enc:"+giemaSMSCardEnvelopePrefix+"card", time.Now()))
	expectGiemaRuntimeLock(mock)
	expectGiemaRuntimeStateLoad(mock, "session", 42, giemaRuntimeStateFixture("provider-session", 2, 5), false)
	mock.ExpectQuery("SELECT value FROM security_secrets").WithArgs(giemaSMSAPIKeySecretName).
		WillReturnRows(sqlmock.NewRows([]string{"value"}).AddRow("enc:provider-key"))
	expectGiemaRuntimeStateSave(mock, 42)
	expectGiemaRuntimeStateSave(mock, 42)
	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT id FROM xiass_sms_card_keys`).WithArgs("session", int64(42)).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(170))
	mock.ExpectExec(`SET status = 'exhausted', provider_state = '', provider_pending = false`).WithArgs(int64(170)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	expectGiemaRuntimeQueue(mock, 42, 0, 0)
	expectGiemaRuntimeUnlock(mock)
	expectPixlabWorkflowEmptyQueue(mock, 42, scope)

	svc := newPixlabSMSService(db, pixlabSMSPrefixEncryptor{}, server.Client(), "http://unused-pixlab")
	svc.giemaBaseURL = server.URL + "/api/v1"
	_, err = svc.WorkflowAction(context.Background(), 42, scope, "session", "change", true)
	require.ErrorIs(t, err, ErrPixlabSMSNoCardKey, "the confirmed change must proceed to the next queued card")
	require.NoError(t, mock.ExpectationsWereMet())
}
