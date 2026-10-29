package service

import (
	"context"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/stretchr/testify/require"
)

const pixlabExhaustionScope = "workflow-exhaustion-001"
const pixlabUnlimitedClaimQuery = `(?s)WITH next_key AS .*WHERE status = 'queued'\s+ORDER BY claim_count ASC.*claim_count = card.claim_count \+ 1`

func expectPixlabAcquireStart(mock sqlmock.Sqlmock, mode string) {
	if mode == "member" {
		expectPixlabSMSActiveMemberSession(mock, 42, "")
	}
	if mode == "workflow" {
		expectPixlabWorkflowNoReservation(mock, 42, pixlabExhaustionScope)
	}
}

func expectPixlabAcquireCard(mock sqlmock.Sqlmock, mode, card string) {
	if mode == "member" {
		mock.ExpectBegin()
		mock.ExpectExec(`SELECT pg_advisory_xact_lock`).WithArgs(int64(42)).WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectQuery(`SELECT EXISTS`).WithArgs(int64(42)).WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))
		mock.ExpectQuery(`SELECT member_fee`).WillReturnRows(sqlmock.NewRows([]string{"member_fee"}).AddRow(PixlabSMSMemberFee))
	}
	args := []driver.Value{int64(42), sqlmock.AnyArg()}
	if mode == "workflow" {
		args = append(args, pixlabExhaustionScope)
	}
	rows := sqlmock.NewRows([]string{"encrypted_key"})
	if card != "" {
		rows.AddRow("enc:" + card)
	}
	mock.ExpectQuery(pixlabUnlimitedClaimQuery).WithArgs(args...).WillReturnRows(rows)
	if card == "" {
		if mode == "member" {
			mock.ExpectRollback()
		}
		if mode == "manual" {
			expectPixlabSMSActiveAdminSession(mock, 42, "")
		}
		return
	}
	if mode == "member" {
		mock.ExpectQuery(`UPDATE users`).WithArgs(PixlabSMSMemberFee, int64(42)).WillReturnRows(sqlmock.NewRows([]string{"balance"}).AddRow(8.0))
		mock.ExpectExec(`INSERT INTO xiass_sms_member_charges`).WithArgs(sqlmock.AnyArg(), int64(42), PixlabSMSMemberFee).WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectCommit()
	}
}

func expectPixlabAcquireRetire(mock sqlmock.Sqlmock, mode string) {
	if mode == "member" {
		expectPixlabSMSSettlement(mock, 42, 1, true, pixlabSettlementExhaust)
	} else {
		mock.ExpectExec(`(?s)UPDATE xiass_sms_card_keys\s+SET status = 'exhausted'`).WithArgs(sqlmock.AnyArg(), int64(42)).WillReturnResult(sqlmock.NewResult(0, 1))
	}
}

func expectPixlabAcquireCode(mock sqlmock.Sqlmock, mode string) {
	if mode == "member" {
		expectPixlabSMSSettlement(mock, 42, 2, true, pixlabSettlementCapture)
	} else {
		mock.ExpectExec(`DELETE FROM xiass_sms_card_keys`).WithArgs(sqlmock.AnyArg(), int64(42)).WillReturnResult(sqlmock.NewResult(0, 1))
	}
	mock.ExpectQuery(`COUNT\(\*\) FILTER`).WithArgs(int64(42)).WillReturnRows(sqlmock.NewRows([]string{"queued", "active"}).AddRow(0, 0))
	if mode == "member" {
		mock.ExpectQuery(`SELECT balance FROM users`).WithArgs(int64(42)).WillReturnRows(sqlmock.NewRows([]string{"balance"}).AddRow(8.0))
		mock.ExpectQuery(`SELECT status, amount`).WithArgs(sqlmock.AnyArg(), int64(42)).WillReturnRows(sqlmock.NewRows([]string{"status", "amount"}).AddRow("captured", PixlabSMSMemberFee))
	}
}

func acquirePixlabForTest(s *PixlabSMSService, mode string) (string, error) {
	if mode == "member" {
		result, err := s.RedeemForMember(context.Background(), 42)
		if err != nil {
			return "", err
		}
		return result.Code, nil
	}
	var result *PixlabSMSResult
	var err error
	if mode == "workflow" {
		result, err = s.WorkflowAction(context.Background(), 42, pixlabExhaustionScope, "", "acquire", true)
	} else {
		result, err = s.Redeem(context.Background(), 42)
	}
	if err != nil {
		return "", err
	}
	return result.Code, nil
}

func expectPixlabExistingAcquisition(mock sqlmock.Sqlmock, mode string) {
	scope := ""
	switch mode {
	case "manual":
		mock.ExpectQuery(pixlabUnlimitedClaimQuery).WithArgs(int64(42), sqlmock.AnyArg()).WillReturnRows(sqlmock.NewRows([]string{"encrypted_key"}))
		expectPixlabSMSActiveAdminSession(mock, 42, "existing-session")
	case "member":
		expectPixlabSMSActiveMemberSession(mock, 42, "existing-session")
	case "workflow":
		scope = pixlabExhaustionScope
		mock.ExpectQuery(`SELECT session_id FROM xiass_sms_card_keys`).WithArgs(int64(42), scope).WillReturnRows(sqlmock.NewRows([]string{"session_id"}).AddRow("existing-session"))
	}
	mock.ExpectQuery(`SELECT card.encrypted_key, card.consumed_at`).WithArgs("existing-session", int64(42), mode == "member", scope).
		WillReturnRows(sqlmock.NewRows([]string{"encrypted_key", "consumed_at"}).AddRow("enc:RETAINED", time.Now()))
}

func TestPixlabAcquisitionRecoversAmbiguousReservationWithoutAnotherRedeemOrHold(t *testing.T) {
	for _, mode := range []string{"manual", "member", "workflow"} {
		t.Run(mode, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			t.Cleanup(func() { _ = db.Close() })
			var calls atomic.Int32
			client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				var input map[string]string
				require.NoError(t, json.NewDecoder(r.Body).Decode(&input))
				require.Equal(t, "RETAINED", input["cardKey"])
				if calls.Add(1) == 1 {
					require.Equal(t, "/redeem", r.URL.Path)
					return nil, errors.New("synthetic delivery timeout")
				}
				require.Equal(t, "/resume", r.URL.Path)
				w := httptest.NewRecorder()
				_, _ = w.Write([]byte(`{"success":true,"status":"RECEIVED","code":"123456"}`))
				return w.Result(), nil
			})}
			expectPixlabAcquireStart(mock, mode)
			expectPixlabAcquireCard(mock, mode, "RETAINED")
			s := newPixlabSMSService(db, pixlabSMSPrefixEncryptor{}, client, "http://fixture.invalid")
			_, err = acquirePixlabForTest(s, mode)
			require.Equal(t, "SMS_PROVIDER_UNAVAILABLE", infraerrors.Reason(err))
			expectPixlabExistingAcquisition(mock, mode)
			expectPixlabAcquireCode(mock, mode)
			code, err := acquirePixlabForTest(s, mode)
			require.NoError(t, err)
			require.NotEmpty(t, code)
			require.Equal(t, int32(2), calls.Load())
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestPixlabAcquisitionResumesOnlyDefinitivelyEndedReservations(t *testing.T) {
	for _, mode := range []string{"manual", "member", "workflow"} {
		for _, outcome := range []string{"no_active_number", "exhausted", "temporary_http_failure"} {
			t.Run(mode+"/"+outcome, func(t *testing.T) {
				db, mock, err := sqlmock.New()
				require.NoError(t, err)
				t.Cleanup(func() { _ = db.Close() })
				var calls atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if calls.Add(1) == 1 {
						require.Equal(t, "/resume", r.URL.Path)
						switch outcome {
						case "no_active_number":
							w.WriteHeader(http.StatusBadRequest)
							_, _ = w.Write([]byte(`{"error":"订单异常或未提取号码"}`))
						case "exhausted":
							_, _ = w.Write([]byte(`{"success":true,"status":"EXHAUSTED"}`))
						default:
							w.WriteHeader(http.StatusServiceUnavailable)
							_, _ = w.Write([]byte(`{"error":"订单异常或未提取号码","status":"EXHAUSTED"}`))
						}
						return
					}
					require.Equal(t, "/redeem", r.URL.Path)
					_, _ = w.Write([]byte(`{"success":true,"status":"RECEIVED","code":"123456"}`))
				}))
				t.Cleanup(server.Close)
				expectPixlabExistingAcquisition(mock, mode)
				if outcome != "temporary_http_failure" {
					if outcome == "exhausted" {
						if mode == "member" {
							expectPixlabSMSSettlement(mock, 42, 1, true, pixlabSettlementExhaust)
						} else {
							mock.ExpectExec(`(?s)UPDATE xiass_sms_card_keys\s+SET status = 'exhausted'`).WithArgs("existing-session", int64(42)).WillReturnResult(sqlmock.NewResult(0, 1))
						}
					} else if mode == "member" {
						expectPixlabSMSSettlement(mock, 42, 1, true, pixlabSettlementRelease)
					} else {
						mock.ExpectExec(`(?s)UPDATE xiass_sms_card_keys\s+SET status = 'queued'`).WithArgs("existing-session", int64(42)).WillReturnResult(sqlmock.NewResult(0, 1))
					}
					mock.ExpectQuery(`COUNT\(\*\) FILTER`).WithArgs(int64(42)).WillReturnRows(sqlmock.NewRows([]string{"queued", "active"}).AddRow(1, 0))
					expectPixlabAcquireCard(mock, mode, "AVAILABLE")
					expectPixlabAcquireCode(mock, mode)
				}
				s := newPixlabSMSService(db, pixlabSMSPrefixEncryptor{}, server.Client(), server.URL)
				_, err = acquirePixlabForTest(s, mode)
				if outcome == "temporary_http_failure" {
					require.Equal(t, "SMS_PROVIDER_HTTP_ERROR", infraerrors.Reason(err))
					require.Equal(t, int32(1), calls.Load())
				} else {
					require.NoError(t, err)
					require.Equal(t, int32(2), calls.Load())
				}
				require.NoError(t, mock.ExpectationsWereMet())
			})
		}
	}
}

func TestPixlabAcquisitionSkipsExplicitUnusableCardInSameCall(t *testing.T) {
	for _, mode := range []string{"manual", "member", "workflow"} {
		for _, reply := range []struct {
			name, body string
			status     int
		}{
			{"exhausted_success", `{"success":true,"status":"EXHAUSTED"}`, 200},
			{"invalid_success", `{"success":true,"status":"INVALID_CARD"}`, 200},
			{"invalid_rejection", `{"success":false,"error":"卡密无效"}`, 200},
			{"limit_rejection", `{"success":false,"message":"单卡连续换号上限"}`, 400},
		} {
			t.Run(mode+"/"+reply.name, func(t *testing.T) {
				db, mock, err := sqlmock.New()
				require.NoError(t, err)
				t.Cleanup(func() { _ = db.Close() })
				var calls atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					require.Equal(t, "/redeem", r.URL.Path)
					if calls.Add(1) == 1 {
						w.WriteHeader(reply.status)
						_, _ = w.Write([]byte(reply.body))
						return
					}
					_, _ = w.Write([]byte(`{"success":true,"status":"RECEIVED","number":"12025550124","code":"123456"}`))
				}))
				t.Cleanup(server.Close)
				expectPixlabAcquireStart(mock, mode)
				expectPixlabAcquireCard(mock, mode, "UNUSABLE")
				expectPixlabAcquireRetire(mock, mode)
				expectPixlabAcquireCard(mock, mode, "AVAILABLE")
				expectPixlabAcquireCode(mock, mode)
				s := newPixlabSMSService(db, pixlabSMSPrefixEncryptor{}, server.Client(), server.URL)
				code, err := acquirePixlabForTest(s, mode)
				require.NoError(t, err)
				require.NotEmpty(t, code)
				require.Equal(t, int32(2), calls.Load())
				require.NoError(t, mock.ExpectationsWereMet())
			})
		}
	}
}

func TestPixlabAcquisitionRetainsReservationOnAmbiguousFailure(t *testing.T) {
	for _, mode := range []string{"manual", "member", "workflow"} {
		for _, reply := range []struct {
			status       int
			body, reason string
		}{
			{0, "", "SMS_PROVIDER_UNAVAILABLE"},
			{200, "not-json", "SMS_PROVIDER_INVALID_RESPONSE"},
			{500, `{"success":true,"status":"EXHAUSTED"}`, "SMS_PROVIDER_HTTP_ERROR"},
			{401, `{"success":false,"message":"invalid card"}`, "SMS_PROVIDER_AUTH_FAILED"},
			{403, `{"success":true,"status":"EXHAUSTED"}`, "SMS_PROVIDER_AUTH_FAILED"},
			{408, `{"success":true,"status":"INVALID_CARD"}`, "SMS_PROVIDER_UNAVAILABLE"},
			{429, `{"success":true,"status":"EXHAUSTED"}`, "SMS_PROVIDER_RATE_LIMITED"},
		} {
			t.Run(fmt.Sprintf("%s/%d", mode, reply.status), func(t *testing.T) {
				db, mock, err := sqlmock.New()
				require.NoError(t, err)
				t.Cleanup(func() { _ = db.Close() })
				var calls atomic.Int32
				client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
					calls.Add(1)
					if reply.status == 0 {
						return nil, errors.New("synthetic network failure")
					}
					w := httptest.NewRecorder()
					w.WriteHeader(reply.status)
					_, _ = w.Write([]byte(reply.body))
					return w.Result(), nil
				})}
				expectPixlabAcquireStart(mock, mode)
				expectPixlabAcquireCard(mock, mode, "RETAINED")
				s := newPixlabSMSService(db, pixlabSMSPrefixEncryptor{}, client, "http://fixture.invalid")
				_, err = acquirePixlabForTest(s, mode)
				require.Equal(t, reply.reason, infraerrors.Reason(err))
				require.False(t, isPixlabCardUnusableError(err))
				require.Equal(t, int32(1), calls.Load())
				require.NoError(t, mock.ExpectationsWereMet(), "no release, refund or second claim is allowed")
			})
		}
	}
}

func TestPixlabAcquisitionFallbackIsBoundedAndExhaustedQueueStops(t *testing.T) {
	for _, mode := range []string{"manual", "member", "workflow"} {
		for _, readded := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/readded=%t", mode, readded), func(t *testing.T) {
				db, mock, err := sqlmock.New()
				require.NoError(t, err)
				t.Cleanup(func() { _ = db.Close() })
				var calls atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls.Add(1)
					_, _ = w.Write([]byte(`{"success":true,"status":"EXHAUSTED"}`))
				}))
				t.Cleanup(server.Close)
				expectPixlabAcquireStart(mock, mode)
				attempts := 1
				if readded {
					attempts = pixlabSMSMaxAcquireAttempts
				}
				for i := 0; i < attempts; i++ {
					expectPixlabAcquireCard(mock, mode, "SAME-READDED-CARD")
					expectPixlabAcquireRetire(mock, mode)
				}
				if !readded {
					expectPixlabAcquireCard(mock, mode, "")
				}
				s := newPixlabSMSService(db, pixlabSMSPrefixEncryptor{}, server.Client(), server.URL)
				_, err = acquirePixlabForTest(s, mode)
				if readded {
					require.ErrorIs(t, err, ErrPixlabSMSAttemptLimit)
				} else {
					require.ErrorIs(t, err, ErrPixlabSMSNoCardKey)
				}
				require.Equal(t, int32(attempts), calls.Load())
				require.NoError(t, mock.ExpectationsWereMet())
			})
		}
	}
}

func TestPixlabProviderClassificationAndRejectionRedaction(t *testing.T) {
	for _, message := range []string{"6次", "等待6次重试", "触发熔断", "服务熔断", "单卡连续换号剩余次数", "订单异常或未提取号码", "号码暂时不足"} {
		require.False(t, isPixlabCardUnusableMessage(message), message)
	}
	for _, message := range []string{"卡密无效", "卡密已耗尽", "card key has been used", "invalid card key", "card exhausted"} {
		require.True(t, isPixlabCardUnusableMessage(message), message)
	}
	for _, body := range []string{
		`{"success":false,"message":"failure CARD_SECRET PHONE_SECRET OTP_SECRET","error":"OTHER_SECRET"}`,
		`{"success":false,"error":"CARD_SECRET PHONE_SECRET OTP_SECRET"}`,
	} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(body)) }))
		s := newPixlabSMSService(nil, nil, server.Client(), server.URL)
		_, err := s.callProvider(context.Background(), "redeem", "synthetic-card")
		server.Close()
		require.Equal(t, "SMS_PROVIDER_REJECTED", infraerrors.Reason(err))
		require.NotContains(t, err.Error(), "SECRET")
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"success":true,"status":"EXHAUSTED","code":"123456","number":"12025550124"}`))
	}))
	t.Cleanup(server.Close)
	s := newPixlabSMSService(nil, nil, server.Client(), server.URL)
	result, err := s.callProvider(context.Background(), "redeem", "synthetic-card")
	require.NoError(t, err)
	require.True(t, pixlabProviderHasTrustedCode(result))
}

func TestPixlabExplicitReaddOnlyRequeuesExhaustedWithoutResettingStats(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherFunc(func(expected, actual string) error {
		if strings.Contains(actual, "ON CONFLICT") {
			updates := strings.Split(actual, "DO UPDATE SET")[1]
			if !strings.Contains(updates, "card.status = 'exhausted'") || strings.Contains(updates, "claim_count") || strings.Contains(updates, "last_claimed_at") {
				return errors.New("re-add must preserve claim history and only update exhausted rows")
			}
		}
		return sqlmock.QueryMatcherRegexp.Match(expected, actual)
	})))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	for _, card := range []string{"EXHAUSTED", "ACTIVE", "QUEUED"} {
		rows := sqlmock.NewRows([]string{"id"})
		if card == "EXHAUSTED" {
			rows.AddRow(144)
		}
		mock.ExpectQuery(`INSERT INTO xiass_sms_card_keys AS card`).WithArgs("enc:"+card, pixlabCardKeyFingerprint(card)).WillReturnRows(rows)
	}
	mock.ExpectQuery(`COUNT\(\*\) FILTER`).WithArgs(int64(0)).WillReturnRows(sqlmock.NewRows([]string{"queued", "active"}).AddRow(2, 1))
	s := newPixlabSMSService(db, pixlabSMSPrefixEncryptor{}, nil, "http://fixture.invalid")
	added, _, err := s.AddCardKeys(context.Background(), "EXHAUSTED\nEXHAUSTED\nACTIVE\nQUEUED")
	require.NoError(t, err)
	require.Equal(t, 1, added)
	mock.ExpectQuery(`SELECT id, encrypted_key, status, claim_count`).WillReturnRows(sqlmock.NewRows([]string{"id", "encrypted_key", "status", "claim_count", "queue_rank", "last_claimed_at", "created_at", "provider"}).AddRow(144, "enc:EXHAUSTED", "queued", 8, 1, time.Now(), time.Now(), SMSProviderPixlab))
	mock.ExpectQuery(`COUNT\(\*\) FILTER`).WithArgs(int64(0)).WillReturnRows(sqlmock.NewRows([]string{"queued", "active"}).AddRow(2, 1))
	cards, _, err := s.ListCardKeys(context.Background())
	require.NoError(t, err)
	raw, err := json.Marshal(cards)
	require.NoError(t, err)
	require.Contains(t, string(raw), `"claim_count":8`)
	require.NoError(t, mock.ExpectationsWereMet())
}
