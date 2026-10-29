package service

import (
	"context"
	"encoding/json"
	"net/http"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/stretchr/testify/require"
)

func TestPixlabSMSServiceDeleteGiemaCardRequiresResolvedState(t *testing.T) {
	for _, tc := range []struct {
		name            string
		preflightStatus string
		status          string
		pending         bool
		allowed         bool
	}{
		{name: "active", preflightStatus: "active", status: "active"},
		{name: "active_pending", preflightStatus: "active", status: "active", pending: true},
		{name: "cancelled_pending", preflightStatus: "cancelled", status: "cancelled", pending: true},
		{name: "exhausted_pending", preflightStatus: "exhausted", status: "exhausted", pending: true},
		{name: "claimed_after_preflight", preflightStatus: "queued", status: "active"},
		{name: "resolved_exhausted", preflightStatus: "exhausted", status: "exhausted", allowed: true},
		{name: "resolved_cancelled", preflightStatus: "cancelled", status: "cancelled", allowed: true},
		{name: "unused_queued", preflightStatus: "queued", status: "queued", allowed: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			t.Cleanup(func() { _ = db.Close() })

			var preflightSession, sessionID, ownerID any
			if tc.preflightStatus == "active" {
				preflightSession = "member-session"
			}
			if tc.status == "active" {
				sessionID, ownerID = "member-session", int64(7)
			}
			mock.ExpectQuery(regexp.QuoteMeta(`SELECT status, session_id FROM xiass_sms_card_keys WHERE id = $1`)).
				WithArgs(int64(12)).
				WillReturnRows(sqlmock.NewRows([]string{"status", "session_id"}).AddRow(tc.preflightStatus, preflightSession))
			mock.ExpectBegin()
			mock.ExpectQuery(regexp.QuoteMeta(`
				SELECT status, owner_user_id, session_id, provider, provider_pending
				FROM xiass_sms_card_keys WHERE id = $1 FOR UPDATE`)).
				WithArgs(int64(12)).
				WillReturnRows(sqlmock.NewRows([]string{"status", "owner_user_id", "session_id", "provider", "provider_pending"}).
					AddRow(tc.status, ownerID, sessionID, SMSProviderGiema, tc.pending))
			if tc.allowed {
				mock.ExpectExec(regexp.QuoteMeta(`DELETE FROM xiass_sms_card_keys WHERE id = $1`)).
					WithArgs(int64(12)).WillReturnResult(sqlmock.NewResult(0, 1))
				mock.ExpectCommit()
				mock.ExpectQuery(`SELECT COUNT\(\*\) FILTER`).WithArgs(int64(0)).
					WillReturnRows(sqlmock.NewRows([]string{"queued", "active"}).AddRow(2, 0))
			} else {
				// No charge lookup, refund or delete may precede this rollback.
				mock.ExpectRollback()
			}

			svc := newPixlabSMSService(db, pixlabSMSPrefixEncryptor{}, nil, "http://example.invalid")
			result, err := svc.DeleteCardKey(context.Background(), 12)
			if tc.allowed {
				require.NoError(t, err)
				require.Equal(t, int64(2), result.QueuedCount)
			} else {
				require.Nil(t, result)
				require.Equal(t, http.StatusConflict, infraerrors.Code(err))
				require.Equal(t, "SMS_CARD_KEY_PROVIDER_PENDING", infraerrors.Reason(err))
			}
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestPixlabSMSServiceMemberResultPreservesProviderState(t *testing.T) {
	for _, tc := range []struct {
		name   string
		result PixlabSMSResult
	}{
		{name: "changing", result: PixlabSMSResult{
			SessionID: "member-session", Status: "CHANGING", NumberPending: true, PollSeconds: 7,
		}},
		{name: "local_cancel", result: PixlabSMSResult{
			Status: "CANCELLED", NumberPending: true, PollSeconds: 7, LocalStop: true,
		}},
		{name: "pending_resume", result: PixlabSMSResult{
			SessionID: "member-session", Status: "WAIT", Number: "15550000000", PollSeconds: 7, LocalStop: true,
		}},
		{name: "legacy_pixlab", result: PixlabSMSResult{
			SessionID: "member-session", Status: "WAITING", Number: "15550000000",
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			t.Cleanup(func() { _ = db.Close() })
			mock.ExpectQuery(regexp.QuoteMeta(`SELECT balance FROM users WHERE id = $1 AND deleted_at IS NULL`)).
				WithArgs(int64(7)).WillReturnRows(sqlmock.NewRows([]string{"balance"}).AddRow(8.0))
			mock.ExpectQuery(regexp.QuoteMeta(`
				SELECT status, amount FROM xiass_sms_member_charges WHERE session_id = $1 AND user_id = $2`)).
				WithArgs("member-session", int64(7)).
				WillReturnRows(sqlmock.NewRows([]string{"status", "amount"}).AddRow("held", 2.0))
			if tc.result.SessionID != "" {
				mock.ExpectQuery(regexp.QuoteMeta(`
					SELECT consumed_at FROM xiass_sms_card_keys
					WHERE session_id = $1 AND owner_user_id = $2 AND status = 'active'`)).
					WithArgs("member-session", int64(7)).
					WillReturnRows(sqlmock.NewRows([]string{"consumed_at"}).AddRow(time.Now()))
			}

			svc := newPixlabSMSService(db, pixlabSMSPrefixEncryptor{}, nil, "http://example.invalid")
			result, err := svc.memberResult(context.Background(), 7, "member-session", &tc.result)
			require.NoError(t, err)
			require.Equal(t, tc.result.Status, result.Status)
			require.Equal(t, tc.result.SessionID, result.SessionID)
			require.Equal(t, tc.result.Number, result.Number)
			require.Equal(t, tc.result.NumberPending, result.NumberPending)
			require.Equal(t, tc.result.PollSeconds, result.PollSeconds)
			require.Equal(t, tc.result.LocalStop, result.LocalStop)
			require.Equal(t, "held", result.ChargeState)
			require.Equal(t, 2.0, result.FeeAmount)
			require.Equal(t, 8.0, result.Balance)

			payload, err := json.Marshal(result)
			require.NoError(t, err)
			var wire map[string]any
			require.NoError(t, json.Unmarshal(payload, &wire))
			if tc.result.NumberPending {
				require.Equal(t, true, wire["number_pending"])
			} else {
				require.NotContains(t, wire, "number_pending")
			}
			if tc.result.PollSeconds != 0 {
				require.Equal(t, float64(tc.result.PollSeconds), wire["poll_seconds"])
			} else {
				require.NotContains(t, wire, "poll_seconds")
			}
			if tc.result.LocalStop {
				require.Equal(t, true, wire["local_stop"])
			} else {
				require.NotContains(t, wire, "local_stop")
			}
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}
