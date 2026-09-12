package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"regexp"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
)

func TestPixlabWorkflowRequiresConfirmationBeforeAnyProviderOrDBAction(t *testing.T) {
	s := &PixlabSMSService{}
	for _, action := range []string{"acquire", "change", "cancel"} {
		_, err := s.WorkflowAction(context.Background(), 42, "workflow-00000001", "session", action, false)
		require.Error(t, err)
	}
	for _, scope := range []string{"", "short", "../workflow-000001"} {
		_, err := s.WorkflowAction(context.Background(), 42, scope, "session", "acquire", true)
		require.Error(t, err)
	}
}

func TestPixlabWorkflowCannotReachAnotherScopeOrNormalReceiver(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.WriteHeader(500) }))
	t.Cleanup(server.Close)
	s := newPixlabSMSService(db, pixlabSMSPrefixEncryptor{}, server.Client(), server.URL)
	for _, scope := range []string{"", "workflow-00000001", "workflow-00000002"} {
		mock.ExpectQuery(`SELECT card.encrypted_key, card.consumed_at`).WithArgs("foreign-session", int64(42), false, scope).
			WillReturnRows(sqlmock.NewRows([]string{"encrypted_key", "consumed_at"}))
		if scope == "" {
			_, err = s.Cancel(context.Background(), 42, "foreign-session")
		} else {
			_, err = s.WorkflowAction(context.Background(), 42, scope, "foreign-session", "cancel", true)
		}
		require.ErrorIs(t, err, ErrPixlabSMSSession)
	}
	require.Zero(t, calls.Load())
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPixlabWorkflowRecoverReservationRequiresExactOwnerAndScope(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	s := &PixlabSMSService{db: db}
	mock.ExpectQuery(`SELECT session_id FROM xiass_sms_card_keys`).WithArgs(int64(42), "workflow-00000001").WillReturnRows(sqlmock.NewRows([]string{"session_id"}).AddRow("own-session"))
	mock.ExpectQuery(`SELECT session_id FROM xiass_sms_card_keys`).WithArgs(int64(43), "workflow-00000001").WillReturnRows(sqlmock.NewRows([]string{"session_id"}))
	id, err := s.WorkflowSession(context.Background(), 42, "workflow-00000001")
	require.NoError(t, err)
	require.Equal(t, "own-session", id)
	id, err = s.WorkflowSession(context.Background(), 43, "workflow-00000001")
	require.NoError(t, err)
	require.Empty(t, id)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPixlabWorkflowClaimsIndependentCardsForSameAdministrator(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/redeem", r.URL.Path)
		calls.Add(1)
		_, _ = w.Write([]byte(`{"success":true,"number":"12025550123","status":"WAITING"}`))
	}))
	t.Cleanup(server.Close)
	s := newPixlabSMSService(db, pixlabSMSPrefixEncryptor{}, server.Client(), server.URL)
	ids := make(map[string]bool)
	for _, scope := range []string{"workflow-00000001", "workflow-00000002"} {
		mock.ExpectQuery(`SELECT session_id FROM xiass_sms_card_keys`).WithArgs(int64(42), scope).
			WillReturnRows(sqlmock.NewRows([]string{"session_id"}))
		mock.ExpectQuery(`UPDATE xiass_sms_card_keys AS card`).WithArgs(int64(42), sqlmock.AnyArg(), scope, PixlabSMSCardKeyMaxClaims).
			WillReturnRows(sqlmock.NewRows([]string{"encrypted_key"}).AddRow("enc:key-" + scope))
		mock.ExpectExec(`UPDATE xiass_sms_card_keys`).WithArgs(sqlmock.AnyArg(), int64(42)).WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectQuery(`COUNT\(\*\) FILTER`).WithArgs(int64(42)).WillReturnRows(sqlmock.NewRows([]string{"queued_count", "active_count"}).AddRow(2, 1))
		mock.ExpectQuery(`SELECT consumed_at`).WithArgs(sqlmock.AnyArg(), int64(42)).WillReturnRows(sqlmock.NewRows([]string{"consumed_at"}).AddRow(time.Now()))
		result, err := s.WorkflowAction(context.Background(), 42, scope, "", "acquire", true)
		require.NoError(t, err)
		require.NotEmpty(t, result.SessionID)
		require.False(t, ids[result.SessionID])
		ids[result.SessionID] = true
	}
	require.Equal(t, int32(2), calls.Load())
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPixlabWorkflowCheckNeverFallsBackToOrdinaryTerminalResults(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	s := newPixlabSMSService(db, pixlabSMSPrefixEncryptor{}, http.DefaultClient, "http://127.0.0.1:1")
	mock.ExpectQuery(`SELECT card.encrypted_key, card.consumed_at`).WithArgs("normal-terminal", int64(42), false, "workflow-00000001").
		WillReturnRows(sqlmock.NewRows([]string{"encrypted_key", "consumed_at"}))
	_, err = s.WorkflowAction(context.Background(), 42, "workflow-00000001", "normal-terminal", "check", false)
	require.ErrorIs(t, err, ErrPixlabSMSSession)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPixlabWorkflowNaturalExpiryChecksWithoutCancelOrCodePersistence(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/check", r.URL.Path)
		_, _ = w.Write([]byte(`{"success":true,"status":"WAITING"}`))
	}))
	t.Cleanup(server.Close)
	s := newPixlabSMSService(db, pixlabSMSPrefixEncryptor{}, server.Client(), server.URL)
	expectPixlabSMSExpiredCleanupRows(mock, sqlmock.NewRows([]string{"session_id", "owner_user_id", "workflow_scope", "member_session"}).
		AddRow("expired-workflow", int64(42), "workflow-00000001", false))
	lease := expectPixlabSMSCleanupAttempt(mock, "expired-workflow", 42)
	mock.ExpectQuery(`SELECT card.encrypted_key, card.consumed_at`).WithArgs("expired-workflow", int64(42), false, "workflow-00000001").
		WillReturnRows(sqlmock.NewRows([]string{"encrypted_key", "consumed_at"}).AddRow("enc:key", time.Now().Add(-PixlabSMSSessionValidity-time.Minute)))
	expectPixlabSMSExpire(mock, "expired-workflow", 42, 13, false)
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT
			COUNT(*) FILTER (WHERE status = 'queued'),
			COUNT(*) FILTER (WHERE status = 'active' AND ($1 = 0 OR owner_user_id = $1))
		FROM xiass_sms_card_keys`)).WithArgs(int64(42)).WillReturnRows(sqlmock.NewRows([]string{"queued_count", "active_count"}).AddRow(1, 0))
	expectPixlabSMSCleanupLeaseRelease(mock, lease)
	require.NoError(t, s.cleanupExpiredSessions(context.Background()))
	require.NoError(t, mock.ExpectationsWereMet())
}
