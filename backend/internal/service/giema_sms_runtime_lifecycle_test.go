package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
)

func expectGiemaLifecycleSettlement(mock sqlmock.Sqlmock, member bool, status string) {
	mock.ExpectBegin()
	if member {
		mock.ExpectExec(`SELECT pg_advisory_xact_lock`).WithArgs(int64(42)).WillReturnResult(sqlmock.NewResult(0, 1))
	}
	mock.ExpectQuery(`SELECT id FROM xiass_sms_card_keys`).WithArgs(sqlmock.AnyArg(), int64(42)).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(170))
	if member {
		mock.ExpectQuery(`SELECT amount FROM xiass_sms_member_charges`).WithArgs(sqlmock.AnyArg(), int64(42)).
			WillReturnRows(sqlmock.NewRows([]string{"amount"}).AddRow(PixlabSMSMemberFee))
		mock.ExpectExec(`UPDATE users`).WithArgs(PixlabSMSMemberFee, int64(42)).WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectExec(`SET status = 'released'`).WithArgs(sqlmock.AnyArg(), int64(42)).WillReturnResult(sqlmock.NewResult(0, 1))
	}
	mock.ExpectExec(`SET status = '` + status + `', provider_state = '', provider_pending = false`).
		WithArgs(int64(170)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
}

func TestGiemaRuntimePendingResponseCannotSettleOrExposeTerminalStatus(t *testing.T) {
	for _, status := range []string{"exhausted", "disabled", "replace_ready"} {
		for _, pendingField := range []string{"codePending", "waitingCancellation"} {
			for _, action := range []string{"check", "cancel"} {
				t.Run(status+"/"+pendingField+"/"+action, func(t *testing.T) {
					db, mock, err := sqlmock.New()
					require.NoError(t, err)
					t.Cleanup(func() { _ = db.Close() })
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						require.Equal(t, "/api/v1/store/status", r.URL.Path)
						_ = json.NewEncoder(w).Encode(map[string]any{
							"status": status, pendingField: true, "consumed": pendingField == "codePending",
							"phone": "15550000000", "codeExpiresAt": time.Now().Add(time.Minute).Unix(),
						})
					}))
					t.Cleanup(server.Close)
					expectGiemaRuntimeLock(mock)
					expectGiemaRuntimeStateLoad(mock, "session", 42, giemaRuntimeStateFixture("provider-session", 2, 5), false)
					mock.ExpectQuery(`SELECT value FROM security_secrets`).WithArgs(giemaSMSAPIKeySecretName).
						WillReturnRows(sqlmock.NewRows([]string{"value"}).AddRow("enc:provider-key"))
					expectGiemaRuntimeStateSave(mock, 42)
					expectGiemaRuntimeStateSave(mock, 42)
					expectGiemaRuntimeQueue(mock, 42, 1, 1)
					if action == "cancel" {
						mock.ExpectExec(`SET provider_pending = true`).WithArgs("session", int64(42)).
							WillReturnResult(sqlmock.NewResult(0, 1))
					}
					expectGiemaRuntimeUnlock(mock)
					svc := newPixlabSMSService(db, pixlabSMSPrefixEncryptor{}, server.Client(), "http://unused-pixlab")
					svc.giemaBaseURL = server.URL + "/api/v1"
					result, err := svc.runGiemaSession(context.Background(), 42, "session", "card", action, true, false)
					require.NoError(t, err, "an unresolved provider operation must not refund or release a held card")
					require.True(t, result.NumberPending)
					require.Empty(t, result.Number)
					if action == "cancel" {
						require.Equal(t, "CANCELLED", result.Status)
						require.True(t, result.LocalStop)
						require.Empty(t, result.SessionID)
					} else {
						require.Empty(t, pixlabTerminalStatus(result.Status, ""))
						require.Equal(t, "WAITING", result.Status)
						require.Equal(t, "session", result.SessionID)
					}
					require.NoError(t, mock.ExpectationsWereMet())
				})
			}
		}
	}
}

func TestGiemaRuntimeReplaceReadyReleasesNumberWithoutExhaustingCard(t *testing.T) {
	for _, member := range []bool{false, true} {
		for _, pending := range []bool{false, true} {
			t.Run(fmt.Sprintf("member=%t/pending=%t", member, pending), func(t *testing.T) {
				db, mock, err := sqlmock.New()
				require.NoError(t, err)
				t.Cleanup(func() { _ = db.Close() })
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					require.Equal(t, "/api/v1/store/status", r.URL.Path)
					var request giemaSMSRequest
					require.NoError(t, json.NewDecoder(r.Body).Decode(&request))
					require.Equal(t, "provider-session", request.Session)
					// Same field set as the reported read-only response; all values are fixtures.
					_, _ = w.Write([]byte(`{"session":"provider-session","status":"replace_ready","attemptCount":1,"attemptsExhausted":false,"canChange":false,"code":"","consumed":false,"maxAttempts":5,"remainingAttempts":4}`))
				}))
				t.Cleanup(server.Close)
				expectGiemaRuntimeLock(mock)
				expectGiemaRuntimeStateLoad(mock, "session", 42, giemaRuntimeStateFixture("provider-session", 1, 5), pending)
				mock.ExpectQuery(`SELECT value FROM security_secrets`).WithArgs(giemaSMSAPIKeySecretName).
					WillReturnRows(sqlmock.NewRows([]string{"value"}).AddRow("enc:provider-key"))
				expectGiemaRuntimeStateSave(mock, 42)
				expectGiemaRuntimeStateSave(mock, 42)
				expectGiemaRuntimeQueue(mock, 42, 0, 1)
				expectGiemaLifecycleSettlement(mock, member, "queued")
				expectGiemaRuntimeUnlock(mock)
				svc := newPixlabSMSService(db, pixlabSMSPrefixEncryptor{}, server.Client(), "http://unused-pixlab")
				svc.giemaBaseURL = server.URL + "/api/v1"
				result, err := svc.runGiemaSession(context.Background(), 42, "session", "card", "check", member, false)
				require.NoError(t, err)
				require.Equal(t, "EXPIRED", result.Status)
				require.Empty(t, result.SessionID)
				require.Empty(t, result.Number)
				require.False(t, result.NumberPending)
				require.False(t, result.LocalStop)
				require.NoError(t, mock.ExpectationsWereMet())
			})
		}
	}
}

func TestGiemaWorkflowConfirmedChangeContinuesAfterReplaceReady(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		switch r.URL.Path {
		case "/api/v1/store/rotate":
			_, _ = w.Write([]byte(`{"session":"provider-session","status":"replace_ready","canChange":false,"consumed":false}`))
		case "/redeem":
			_, _ = w.Write([]byte(`{"success":true,"status":"WAITING","number":"15550000001"}`))
		default:
			t.Errorf("unexpected provider request: %s", r.URL.Path)
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
	t.Cleanup(server.Close)
	const scope = "workflow-giema-replace-ready"
	expectPixlabWorkflowProvider(mock, "session", 42, scope, SMSProviderGiema)
	mock.ExpectQuery(`SELECT card\.encrypted_key, card\.consumed_at`).WithArgs("session", int64(42), false, scope).
		WillReturnRows(sqlmock.NewRows([]string{"encrypted_key", "consumed_at"}).AddRow("enc:"+giemaSMSCardEnvelopePrefix+"card", time.Now()))
	expectGiemaRuntimeLock(mock)
	expectGiemaRuntimeStateLoad(mock, "session", 42, giemaRuntimeStateFixture("provider-session", 1, 5), false)
	mock.ExpectQuery(`SELECT value FROM security_secrets`).WithArgs(giemaSMSAPIKeySecretName).
		WillReturnRows(sqlmock.NewRows([]string{"value"}).AddRow("enc:provider-key"))
	expectGiemaRuntimeStateSave(mock, 42)
	expectGiemaRuntimeStateSave(mock, 42)
	expectGiemaRuntimeQueue(mock, 42, 1, 1)
	expectGiemaLifecycleSettlement(mock, false, "queued")
	expectGiemaRuntimeUnlock(mock)
	expectPixlabWorkflowReplacement(mock, 42, scope)
	svc := newPixlabSMSService(db, pixlabSMSPrefixEncryptor{}, server.Client(), server.URL)
	svc.giemaBaseURL = server.URL + "/api/v1"
	result, err := svc.WorkflowAction(context.Background(), 42, scope, "session", "change", true)
	require.NoError(t, err)
	require.Equal(t, "WAITING", result.Status)
	require.Equal(t, "15550000001", result.Number)
	require.NotEmpty(t, result.SessionID)
	require.Equal(t, []string{"/api/v1/store/rotate", "/redeem"}, paths)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestGiemaRuntimeChangeRestartsReplaceReadyWithExistingSession(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v1/store/start", r.URL.Path)
		var request giemaSMSRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&request))
		require.Equal(t, "provider-session", request.Session)
		_, _ = w.Write([]byte(`{"session":"provider-session","status":"wait","phoneReady":true,"phoneNumber":"15550000001","attempt":2,"canChange":true}`))
	}))
	t.Cleanup(server.Close)

	state := giemaRuntimeStateFixture("provider-session", 1, 5)
	var persisted giemaSMSPersistedState
	require.NoError(t, json.Unmarshal([]byte(strings.TrimPrefix(state, "enc:")), &persisted))
	persisted.Latest.Status = "replace_ready"
	payload, err := json.Marshal(&persisted)
	require.NoError(t, err)
	state = "enc:" + string(payload)

	mock.ExpectQuery(`SELECT provider FROM xiass_sms_card_keys`).
		WithArgs("session", int64(42), "").
		WillReturnRows(sqlmock.NewRows([]string{"provider"}).AddRow(SMSProviderGiema))
	mock.ExpectQuery(`SELECT card\.encrypted_key, card\.consumed_at`).
		WithArgs("session", int64(42), false, "").
		WillReturnRows(sqlmock.NewRows([]string{"encrypted_key", "consumed_at"}).AddRow("enc:"+giemaSMSCardEnvelopePrefix+"card", time.Now()))
	expectGiemaRuntimeLock(mock)
	expectGiemaRuntimeStateLoad(mock, "session", 42, state, false)
	mock.ExpectQuery(`SELECT value FROM security_secrets`).WithArgs(giemaSMSAPIKeySecretName).
		WillReturnRows(sqlmock.NewRows([]string{"value"}).AddRow("enc:provider-key"))
	expectGiemaRuntimeStateSave(mock, 42)
	expectGiemaRuntimeStateSave(mock, 42)
	expectGiemaRuntimeQueue(mock, 42, 0, 1)
	expectGiemaRuntimeUnlock(mock)

	svc := newPixlabSMSService(db, pixlabSMSPrefixEncryptor{}, server.Client(), "http://unused-pixlab")
	svc.giemaBaseURL = server.URL + "/api/v1"
	result, err := svc.ChangeNumber(context.Background(), 42, "session")
	require.NoError(t, err)
	require.Equal(t, "WAIT", result.Status)
	require.Equal(t, "15550000001", result.Number)
	require.Equal(t, "session", result.SessionID)
	require.NoError(t, mock.ExpectationsWereMet())
}
