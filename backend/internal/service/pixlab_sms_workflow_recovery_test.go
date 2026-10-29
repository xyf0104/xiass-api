package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
)

func expectPixlabWorkflowNoReservation(mock sqlmock.Sqlmock, owner int64, scope string) {
	mock.ExpectQuery(`SELECT session_id FROM xiass_sms_card_keys`).WithArgs(owner, scope).
		WillReturnRows(sqlmock.NewRows([]string{"session_id"}))
}

func expectPixlabWorkflowEmptyQueue(mock sqlmock.Sqlmock, owner int64, scope string) {
	expectPixlabWorkflowNoReservation(mock, owner, scope)
	mock.ExpectQuery(`UPDATE xiass_sms_card_keys AS card`).WithArgs(owner, sqlmock.AnyArg(), scope).
		WillReturnRows(sqlmock.NewRows([]string{"encrypted_key"}))
}

func expectPixlabWorkflowProvider(mock sqlmock.Sqlmock, session string, owner int64, scope, provider string) {
	mock.ExpectQuery(`SELECT provider FROM xiass_sms_card_keys`).WithArgs(session, owner, scope).
		WillReturnRows(sqlmock.NewRows([]string{"provider"}).AddRow(provider))
}

func expectPixlabWorkflowReplacement(mock sqlmock.Sqlmock, owner int64, scope string) {
	expectPixlabWorkflowNoReservation(mock, owner, scope)
	mock.ExpectQuery(`UPDATE xiass_sms_card_keys AS card`).WithArgs(owner, sqlmock.AnyArg(), scope).
		WillReturnRows(sqlmock.NewRows([]string{"encrypted_key"}).AddRow("enc:replacement-card"))
	mock.ExpectExec(`UPDATE xiass_sms_card_keys`).WithArgs(sqlmock.AnyArg(), owner).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery(`COUNT\(\*\) FILTER`).WithArgs(owner).
		WillReturnRows(sqlmock.NewRows([]string{"queued_count", "active_count"}).AddRow(0, 1))
	mock.ExpectQuery(`SELECT consumed_at`).WithArgs(sqlmock.AnyArg(), owner).
		WillReturnRows(sqlmock.NewRows([]string{"consumed_at"}).AddRow(time.Now()))
}

func TestPixlabWorkflowReplacementRecoversAfterTerminalReservationAndEmptyQueue(t *testing.T) {
	for _, terminal := range []string{"CANCELLED", "EXPIRED", "RECEIVED"} {
		t.Run(terminal, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			t.Cleanup(func() { _ = db.Close() })
			var cancels, redeems atomic.Int32
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/cancel":
					cancels.Add(1)
					result := map[string]any{"success": true, "status": terminal, "number": "12025550123"}
					if terminal == "RECEIVED" {
						result["code"] = "123456"
					}
					_ = json.NewEncoder(w).Encode(result)
				case "/redeem":
					redeems.Add(1)
					_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "status": "WAITING", "number": "12025550124"})
				default:
					w.WriteHeader(http.StatusBadRequest)
				}
			}))
			t.Cleanup(provider.Close)
			s := newPixlabSMSService(db, pixlabSMSPrefixEncryptor{}, provider.Client(), provider.URL)
			const scope = "workflow-recovery-0001"
			expectPixlabWorkflowProvider(mock, "old-session", 42, scope, SMSProviderPixlab)
			mock.ExpectQuery(`SELECT card.encrypted_key, card.consumed_at`).WithArgs("old-session", int64(42), false, scope).
				WillReturnRows(sqlmock.NewRows([]string{"encrypted_key", "consumed_at"}).AddRow("enc:old-card", time.Now()))
			if terminal == "RECEIVED" {
				mock.ExpectExec(`DELETE FROM xiass_sms_card_keys`).WithArgs("old-session", int64(42)).WillReturnResult(sqlmock.NewResult(0, 1))
			} else {
				mock.ExpectExec(`UPDATE xiass_sms_card_keys`).WithArgs("old-session", int64(42)).WillReturnResult(sqlmock.NewResult(0, 1))
			}
			mock.ExpectQuery(`COUNT\(\*\) FILTER`).WithArgs(int64(42)).
				WillReturnRows(sqlmock.NewRows([]string{"queued_count", "active_count"}).AddRow(0, 0))
			if terminal != "RECEIVED" {
				expectPixlabWorkflowEmptyQueue(mock, 42, scope)
			}
			result, err := s.WorkflowAction(context.Background(), 42, scope, "old-session", "change", true)
			if terminal == "RECEIVED" {
				require.NoError(t, err)
				require.Equal(t, "RECEIVED", result.Status)
				require.NotEmpty(t, result.Code)
				require.Empty(t, result.SessionID)
			} else {
				require.ErrorIs(t, err, ErrPixlabSMSNoCardKey)
			}
			require.Equal(t, int32(1), cancels.Load())
			require.Zero(t, redeems.Load())

			expectPixlabWorkflowNoReservation(mock, 42, scope)
			session, err := s.WorkflowSession(context.Background(), 42, scope)
			require.NoError(t, err)
			require.Empty(t, session)
			_, err = s.WorkflowAction(context.Background(), 42, scope, session, "change", false)
			require.Error(t, err)
			expectPixlabWorkflowEmptyQueue(mock, 42, scope)
			_, err = s.WorkflowAction(context.Background(), 42, scope, session, "change", true)
			require.ErrorIs(t, err, ErrPixlabSMSNoCardKey)
			expectPixlabWorkflowReplacement(mock, 42, scope)
			result, err = s.WorkflowAction(context.Background(), 42, scope, session, "change", true)
			require.NoError(t, err)
			require.Equal(t, "WAITING", result.Status)
			require.Equal(t, "12025550124", result.Number)
			require.NotEmpty(t, result.SessionID)
			require.Empty(t, result.Code)
			require.Equal(t, int32(1), cancels.Load())
			require.Equal(t, int32(1), redeems.Load())
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestPixlabWorkflowReplacementDoesNotTreatInaccessibleSessionAsAbsent(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	var calls atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.WriteHeader(500) }))
	t.Cleanup(provider.Close)
	s := newPixlabSMSService(db, pixlabSMSPrefixEncryptor{}, provider.Client(), provider.URL)
	for _, wrong := range []struct {
		owner int64
		scope string
	}{{43, "workflow-00000001"}, {42, "workflow-00000002"}} {
		mock.ExpectQuery(`SELECT provider FROM xiass_sms_card_keys`).WithArgs("foreign-session", wrong.owner, wrong.scope).
			WillReturnRows(sqlmock.NewRows([]string{"provider"}))
		_, err := s.WorkflowAction(context.Background(), wrong.owner, wrong.scope, "foreign-session", "change", true)
		require.ErrorIs(t, err, ErrPixlabSMSSession)
	}
	require.Zero(t, calls.Load())
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPixlabWorkflowReplacementWithAbsentIDResumesExistingOwnedReservation(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	var resumes atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/resume", r.URL.Path)
		resumes.Add(1)
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "status": "WAITING", "number": "12025550124"})
	}))
	t.Cleanup(provider.Close)
	s := newPixlabSMSService(db, pixlabSMSPrefixEncryptor{}, provider.Client(), provider.URL)
	const scope = "workflow-recovery-0001"
	mock.ExpectQuery(`SELECT session_id FROM xiass_sms_card_keys`).WithArgs(int64(42), scope).
		WillReturnRows(sqlmock.NewRows([]string{"session_id"}).AddRow("existing-replacement"))
	mock.ExpectQuery(`SELECT card.encrypted_key, card.consumed_at`).WithArgs("existing-replacement", int64(42), false, scope).
		WillReturnRows(sqlmock.NewRows([]string{"encrypted_key", "consumed_at"}).AddRow("enc:existing-card", time.Now()))
	mock.ExpectQuery(`COUNT\(\*\) FILTER`).WithArgs(int64(42)).
		WillReturnRows(sqlmock.NewRows([]string{"queued_count", "active_count"}).AddRow(0, 1))
	mock.ExpectQuery(`SELECT consumed_at`).WithArgs("existing-replacement", int64(42)).
		WillReturnRows(sqlmock.NewRows([]string{"consumed_at"}).AddRow(time.Now()))
	result, err := s.WorkflowAction(context.Background(), 42, scope, "", "change", true)
	require.NoError(t, err)
	require.Equal(t, "existing-replacement", result.SessionID)
	require.Equal(t, int32(1), resumes.Load())
	require.NoError(t, mock.ExpectationsWereMet())
}
