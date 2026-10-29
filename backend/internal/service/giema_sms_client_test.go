package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const giemaSMSTestKey = "test-only-provider-key"
const giemaSMSTestCard = "test-only-card"
const giemaSMSTestSession = "  test-only-session+/%\n\"\\  "
const giemaSMSTestConfig = `{"provider":"sms688_manual","poll_seconds":5,"rotate_seconds":0,"max_attempts":5,"maintenance":false,"protocol_locked":true}`

func giemaSMSPtr[T any](value T) *T { return &value }

func giemaSMSTestClient(t *testing.T, handler http.HandlerFunc) *GiemaSMSClient {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	client, err := newGiemaSMSClient(giemaSMSTestKey, server.URL+"/api/v1", server.Client())
	require.NoError(t, err)
	return client
}

func TestGiemaSMSClientRequestsPreserveSessionAndReturnedAttempt(t *testing.T) {
	var requests atomic.Int32
	client := giemaSMSTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "Bearer "+giemaSMSTestKey, r.Header.Get("Authorization"))
		assert.Equal(t, "application/json", r.Header.Get("Accept"))
		assert.Empty(t, r.URL.RawQuery)
		body, err := io.ReadAll(r.Body)
		assert.NoError(t, err)
		step := requests.Add(1)
		if step == 1 {
			assert.Equal(t, http.MethodGet, r.Method)
			assert.Equal(t, "/api/v1/store/config", r.URL.Path)
			assert.Empty(t, body)
			_, _ = io.WriteString(w, giemaSMSTestConfig)
			return
		}
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
		tokenJSON, _ := json.Marshal(giemaSMSTestSession)
		switch step {
		case 2:
			assert.Equal(t, "/api/v1/store/start", r.URL.Path)
			assert.JSONEq(t, `{"card":"`+giemaSMSTestCard+`"}`, string(body))
			_, _ = fmt.Fprintf(w, `{"session":%s,"consumed":false,"phoneNumber":"15550000000","orderId":123,"attempt":1,"status":"active","resumed":false,"maxAttempts":5,"attemptCount":1,"remainingAttempts":4,"attemptsExhausted":false,"canChange":true}`, tokenJSON)
		case 3:
			assert.Equal(t, "/api/v1/store/rotate", r.URL.Path)
			assert.JSONEq(t, fmt.Sprintf(`{"card":%q,"session":%s,"expectedAttempt":1}`, giemaSMSTestCard, tokenJSON), string(body))
			_, _ = io.WriteString(w, fmt.Sprintf(`{"session":%s,"phoneNumber":"15550000001","attempt":2,"consumed":false}`, tokenJSON))
		case 4:
			assert.Equal(t, "/api/v1/store/status", r.URL.Path)
			assert.JSONEq(t, fmt.Sprintf(`{"card":%q,"session":%s}`, giemaSMSTestCard, tokenJSON), string(body))
			_, _ = io.WriteString(w, `{"status":"ok","code":"123456","phone":"15550000000","consumed":true}`)
		default:
			t.Error("unexpected additional request")
		}
	})
	ctx := context.Background()
	config, err := client.Config(ctx)
	require.NoError(t, err)
	require.Equal(t, 5, *config.PollSeconds)
	require.Equal(t, 0, *config.RotateSeconds)
	require.Equal(t, 5, *config.MaxAttempts)
	require.False(t, *config.Maintenance)
	require.True(t, *config.ProtocolLocked)
	started, err := client.Start(ctx, giemaSMSTestCard)
	require.NoError(t, err)
	require.Equal(t, giemaSMSTestSession, started.Session.Token)
	require.Equal(t, int64(123), *started.OrderID)
	require.Equal(t, 1, *started.Attempt)
	require.Equal(t, "15550000000", started.Number())
	rotated, err := client.Rotate(ctx, giemaSMSTestCard, started)
	require.NoError(t, err)
	require.Equal(t, giemaSMSTestSession, rotated.Session.Token)
	require.Equal(t, 2, *rotated.Attempt)
	require.Equal(t, "15550000001", rotated.Number())
	require.Equal(t, 1, *started.Attempt, "the caller's snapshot is immutable")
	polled, err := client.Status(ctx, giemaSMSTestCard, rotated.Session)
	require.NoError(t, err)
	require.Equal(t, giemaSMSTestSession, polled.Session.Token, "status may omit session and preserve the supplied value")
	require.Equal(t, "123456", polled.Code)
	require.Empty(t, polled.Number(), "consumed status must not expose a stale phone")
	require.EqualValues(t, 4, requests.Load())
}

func TestGiemaSMSClientCodeExpiryAcceptsFractionalUnixSeconds(t *testing.T) {
	for _, timestamp := range []string{"1900000000", "1900000000.086813"} {
		t.Run(timestamp, func(t *testing.T) {
			client := giemaSMSTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
				_, _ = fmt.Fprintf(w, `{"status":"ok","code":"123456","consumed":true,"codeExpiresAt":%s}`, timestamp)
			})
			result, err := client.Status(context.Background(), giemaSMSTestCard)
			require.NoError(t, err)
			require.Equal(t, "123456", result.Code)
			require.NotNil(t, result.CodeExpiresAt)
			encoded, err := json.Marshal(result)
			require.NoError(t, err)
			var restored GiemaSMSResponse
			require.NoError(t, json.Unmarshal(encoded, &restored))
			require.Equal(t, *result.CodeExpiresAt, *restored.CodeExpiresAt)
		})
	}
}

func TestGiemaSMSClientPhoneAliasesAndStates(t *testing.T) {
	for _, tc := range []struct {
		name, fields, number string
		waiting              bool
	}{
		{"primary", `"phoneReady":true,"phoneNumber":"+15550000001","phone":"+15550000002"`, "+15550000001", false},
		{"alias", `"phoneReady":true,"phone":"+15550000002"`, "+15550000002", false},
		{"null primary", `"phoneReady":true,"phoneNumber":null,"phone":"+15550000002"`, "+15550000002", false},
		{"unready", `"phoneReady":false,"phoneNumber":"stale"`, "", false},
		{"allocating", `"status":"allocating","phoneNumber":"stale"`, "", false},
		{"changing", `"status":"changing","phoneReady":true,"phoneNumber":"stale"`, "", false},
		{"manual without readiness", `"provider":"sms688_manual","phoneNumber":"stale"`, "", false},
		{"platform without readiness", `"platform":"sms688_manual","phone":"stale"`, "", false},
		{"inferred manual without readiness", `"quotaCharged":false,"phone":"stale"`, "", false},
		{"unknown provider without readiness", `"phone":"stale"`, "stale", false},
		{"confirmed legacy provider", `"provider":"smsbower","phone":"+15550000003"`, "+15550000003", false},
		{"waiting cancellation", `"waitingCancellation":true,"phoneReady":true,"phoneNumber":"stale"`, "", true},
		{"canceling", `"status":"canceling","phoneNumber":"stale"`, "", true},
		{"pending code", `"consumed":true,"codePending":true,"codeExpiresAt":1900000000,"phoneNumber":"stale"`, "", false},
		{"consumed", `"consumed":true,"code":"123456","phoneNumber":"stale"`, "", false},
		{"expired code", `"consumed":true,"codeExpired":true,"phoneNumber":"stale"`, "", false},
		{"disabled", `"status":"disabled","phoneNumber":"stale"`, "", false},
		{"exhausted", `"status":"exhausted","phoneNumber":"stale"`, "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Merge test fields structurally so fixtures contain no duplicate JSON keys.
			var fields map[string]any
			require.NoError(t, json.Unmarshal([]byte("{"+tc.fields+"}"), &fields))
			fields["session"], fields["orderId"], fields["attempt"] = giemaSMSTestSession, 123, 1
			if fields["status"] == nil {
				fields["status"] = "wait"
			}
			var calls atomic.Int32
			client := giemaSMSTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
				calls.Add(1)
				_ = json.NewEncoder(w).Encode(fields)
			})
			result, err := client.Start(context.Background(), giemaSMSTestCard)
			require.NoError(t, err)
			require.Equal(t, tc.number, result.Number())
			require.Equal(t, tc.waiting, result.WaitingForCancellation())
			if tc.waiting {
				_, err = client.Rotate(context.Background(), giemaSMSTestCard, result)
				require.Error(t, err)
				require.EqualValues(t, 1, calls.Load())
			}
		})
	}
}

func TestGiemaSMSClientTerminalResponsesWithoutSession(t *testing.T) {
	for _, status := range []string{"exhausted", "disabled", "replace_ready"} {
		t.Run(status, func(t *testing.T) {
			client := giemaSMSTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
				_ = json.NewEncoder(w).Encode(map[string]string{"status": status})
			})
			result, err := client.Start(context.Background(), giemaSMSTestCard)
			require.NoError(t, err)
			require.Equal(t, status, result.Status)
			require.Empty(t, result.Session.Token)
			require.Empty(t, result.Number())
			result, err = client.Status(context.Background(), giemaSMSTestCard, GiemaSMSSession{Token: giemaSMSTestSession})
			require.NoError(t, err)
			require.Equal(t, giemaSMSTestSession, result.Session.Token)
		})
	}
}

func TestGiemaSMSClientAllowsAccountBoundRestoreWithoutSession(t *testing.T) {
	var bodies []string
	client := giemaSMSTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		bodies = append(bodies, string(body))
		_, _ = io.WriteString(w, `{"status":"disabled"}`)
	})
	ctx := context.Background()
	_, err := client.Start(ctx, giemaSMSTestCard)
	require.NoError(t, err)
	_, err = client.Status(ctx, giemaSMSTestCard)
	require.NoError(t, err)
	_, err = client.Rotate(ctx, giemaSMSTestCard, nil)
	require.NoError(t, err)
	require.Len(t, bodies, 3)
	for _, body := range bodies {
		require.JSONEq(t, fmt.Sprintf(`{"card":%q}`, giemaSMSTestCard), body)
	}
}

func TestGiemaSMSClientRejectsInvalidResponses(t *testing.T) {
	for name, body := range map[string]string{
		"empty": "", "null": "null", "array": "[]", "html": "<html>failure</html>",
		"empty object": `{}`, "trailing json": `{} {}`, "truncated": `{"session":`,
		"missing state":             `{"session":"test","orderId":"order"}`,
		"unknown status":            `{"session":"test","status":"unknown","orderId":"order"}`,
		"unconfirmed done":          `{"session":"test","done":true}`,
		"unconfirmed used":          `{"session":"test","used":true}`,
		"missing code":              `{"session":"test","status":"ok"}`,
		"string order":              `{"session":"test","status":"disabled","orderId":"order"}`,
		"numeric session":           `{"session":123,"status":"disabled"}`,
		"numeric phone":             `{"session":"test","status":"wait","orderId":"order","phone":123}`,
		"wrong boolean":             `{"session":"test","status":"disabled","phoneReady":"true"}`,
		"negative attempt":          `{"session":"test","status":"disabled","attempt":-1}`,
		"fractional count":          `{"session":"test","status":"disabled","attemptCount":1.5}`,
		"unsafe count":              `{"session":"test","status":"disabled","attempt":9007199254740992}`,
		"ready without phone":       `{"session":"test","status":"wait","orderId":"order","phoneReady":true}`,
		"empty primary beats alias": `{"session":"test","status":"wait","orderId":"order","phoneReady":true,"phoneNumber":"","phone":"123"}`,
		"oversize":                  `{"status":"disabled","padding":"` + strings.Repeat("x", giemaSMSResponseLimit) + `"}`,
	} {
		t.Run(name, func(t *testing.T) {
			client := giemaSMSTestClient(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, body) })
			result, err := client.Start(context.Background(), giemaSMSTestCard)
			require.Nil(t, result)
			var failure *GiemaSMSError
			require.ErrorAs(t, err, &failure)
			require.Equal(t, GiemaSMSInvalidResponse, failure.Kind)
			require.True(t, failure.Ambiguous)
		})
	}
}

func TestGiemaSMSClientConfigRequiresExplicitFields(t *testing.T) {
	for _, body := range []string{
		`{}`, `null`, `{"provider":"sms688_manual"}`,
		strings.Replace(giemaSMSTestConfig, `"poll_seconds":5`, `"poll_seconds":0`, 1),
		strings.Replace(giemaSMSTestConfig, `"rotate_seconds":0`, `"rotate_seconds":-1`, 1),
		strings.Replace(giemaSMSTestConfig, `"maintenance":false`, `"maintenance":null`, 1),
		strings.Replace(giemaSMSTestConfig, `"protocol_locked":true`, `"protocol_locked":"true"`, 1),
	} {
		client := giemaSMSTestClient(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, body) })
		result, err := client.Config(context.Background())
		require.Error(t, err)
		require.Nil(t, result)
	}
}

func TestGiemaSMSClientHTTPErrorsDoNotRetryOrMutateState(t *testing.T) {
	for _, tc := range []struct {
		status    int
		kind      GiemaSMSErrorKind
		ambiguous bool
	}{
		{401, GiemaSMSAuthentication, false}, {408, GiemaSMSHTTPError, true},
		{409, GiemaSMSConflict, false}, {422, GiemaSMSParameters, false},
		{429, GiemaSMSRateLimited, false}, {500, GiemaSMSHTTPError, true},
		{502, GiemaSMSHTTPError, true}, {503, GiemaSMSHTTPError, true},
	} {
		t.Run(fmt.Sprint(tc.status), func(t *testing.T) {
			var calls atomic.Int32
			client := giemaSMSTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
				calls.Add(1)
				w.Header().Set("Retry-After", "17")
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, "raw provider error "+giemaSMSTestKey+giemaSMSTestCard+giemaSMSTestSession)
			})
			previous := &GiemaSMSResponse{Session: GiemaSMSSession{Token: giemaSMSTestSession}, Attempt: giemaSMSPtr(2), Status: "wait", PhoneNumber: giemaSMSPtr("15550000000"), PhoneReady: giemaSMSPtr(true)}
			result, err := client.Rotate(context.Background(), giemaSMSTestCard, previous)
			require.Nil(t, result)
			var failure *GiemaSMSError
			require.ErrorAs(t, err, &failure)
			require.Equal(t, tc.kind, failure.Kind)
			require.Equal(t, tc.status, failure.StatusCode)
			require.Equal(t, tc.ambiguous, failure.Ambiguous)
			require.Equal(t, tc.status == 429, failure.HasRetryAfter)
			if tc.status == 429 {
				require.Equal(t, 17*time.Second, failure.RetryAfter)
			}
			require.EqualValues(t, 1, calls.Load())
			require.Equal(t, giemaSMSTestSession, previous.Session.Token)
			require.Equal(t, 2, *previous.Attempt)
			if tc.status == http.StatusConflict {
				require.Equal(t, "15550000000", previous.Number(), "a max-attempt conflict must leave the current number usable")
			}
			for _, secret := range []string{giemaSMSTestKey, giemaSMSTestCard, giemaSMSTestSession, "raw provider error"} {
				require.NotContains(t, fmt.Sprintf("%v %+v %#v", err, err, err), secret)
			}
		})
	}
}

func TestGiemaSMSErrorReasonsAreNarrowAndRedacted(t *testing.T) {
	tests := []struct {
		name   string
		status int
		path   string
		detail string
		want   GiemaSMSErrorReason
	}{
		{
			name:   "exact no session conflict",
			status: http.StatusConflict,
			path:   "/store/status",
			detail: "卡密尚无可恢复的 API 会话，请先调用 start 或提供原 session",
			want:   GiemaSMSReasonNoSession,
		},
		{
			name:   "explicit exhausted card",
			status: http.StatusConflict,
			path:   "/store/start",
			detail: "卡密已耗尽",
			want:   GiemaSMSReasonCardUnusable,
		},
		{
			name:   "explicit rotate limit",
			status: http.StatusConflict,
			path:   "/store/rotate",
			detail: "换号次数达到上限，当前号码仍可查询",
			want:   GiemaSMSReasonAttemptLimit,
		},
		{
			name:   "401 never card reason",
			status: http.StatusUnauthorized,
			path:   "/store/start",
			detail: "卡密已耗尽",
			want:   "",
		},
		{
			name:   "timeout never proves missing session",
			status: http.StatusRequestTimeout,
			path:   "/store/status",
			detail: "卡密尚无可恢复的 API 会话，请先调用 start 或提供原 session",
		},
		{
			name:   "generic conflict stays unknown",
			status: http.StatusConflict,
			path:   "/store/start",
			detail: "任务状态冲突",
			want:   "",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			client := giemaSMSTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.status)
				_, _ = fmt.Fprintf(w, `{"detail":%q}`, tc.detail)
			})
			var err error
			switch tc.path {
			case "/store/status":
				_, err = client.Status(context.Background(), giemaSMSTestCard)
			case "/store/rotate":
				_, err = client.Rotate(context.Background(), giemaSMSTestCard, &GiemaSMSResponse{Session: GiemaSMSSession{Token: giemaSMSTestSession}, Attempt: giemaSMSPtr(1), Status: "wait", PhoneNumber: giemaSMSPtr("15550000000"), PhoneReady: giemaSMSPtr(true)})
			default:
				_, err = client.Start(context.Background(), giemaSMSTestCard)
			}
			var failure *GiemaSMSError
			require.ErrorAs(t, err, &failure)
			require.Equal(t, tc.want, failure.Reason)
			require.NotContains(t, err.Error(), tc.detail)
		})
	}
}

func TestGiemaSMSClientRejectsRedirects(t *testing.T) {
	var leaked atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		leaked.Add(1)
		_, _ = io.WriteString(w, giemaSMSTestConfig)
	}))
	defer target.Close()
	for _, status := range []int{301, 302, 303, 307, 308} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			var calls atomic.Int32
			client := giemaSMSTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				http.Redirect(w, r, target.URL, status)
			})
			for _, call := range []func() error{
				func() error { _, err := client.Config(context.Background()); return err },
				func() error { _, err := client.Start(context.Background(), giemaSMSTestCard); return err },
				func() error {
					_, err := client.Status(context.Background(), giemaSMSTestCard, GiemaSMSSession{Token: giemaSMSTestSession})
					return err
				},
				func() error {
					_, err := client.Rotate(context.Background(), giemaSMSTestCard, &GiemaSMSResponse{Session: GiemaSMSSession{Token: giemaSMSTestSession}, Attempt: giemaSMSPtr(1)})
					return err
				},
			} {
				var failure *GiemaSMSError
				require.ErrorAs(t, call(), &failure)
				require.Equal(t, GiemaSMSRedirect, failure.Kind)
			}
			require.EqualValues(t, 4, calls.Load())
		})
	}
	require.Zero(t, leaked.Load(), "no redirect may receive a key, card or session")
}

type giemaSMSRoundTripper func(*http.Request) (*http.Response, error)

func (f giemaSMSRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestGiemaSMSClientTransportFailureIsRedactedAndNotReplayable(t *testing.T) {
	calls := 0
	client, err := newGiemaSMSClient(giemaSMSTestKey, giemaSMSAPIBaseURL, &http.Client{
		Transport: giemaSMSRoundTripper(func(r *http.Request) (*http.Response, error) {
			calls++
			require.Nil(t, r.GetBody)
			require.Empty(t, r.Header.Get("Idempotency-Key"))
			return nil, errors.New("private transport failure " + giemaSMSTestKey + giemaSMSTestCard + giemaSMSTestSession)
		}),
	})
	require.NoError(t, err)
	_, err = client.Start(context.Background(), giemaSMSTestCard)
	var failure *GiemaSMSError
	require.ErrorAs(t, err, &failure)
	require.Equal(t, GiemaSMSTransport, failure.Kind)
	require.True(t, failure.Ambiguous)
	require.Equal(t, 1, calls)
	require.Nil(t, errors.Unwrap(err))
	require.NotContains(t, err.Error(), "private transport failure")
	for _, value := range []any{client, GiemaSMSSession{Token: giemaSMSTestSession}, GiemaSMSResponse{Session: GiemaSMSSession{Token: giemaSMSTestSession}}, err} {
		formatted := fmt.Sprintf("%v %+v %#v %s %q", value, value, value, value, value)
		encoded, marshalErr := json.Marshal(value)
		require.NoError(t, marshalErr)
		for _, secret := range []string{giemaSMSTestKey, giemaSMSTestCard, "test-only-session"} {
			require.NotContains(t, formatted+string(encoded), secret)
		}
	}
}

func TestGiemaSMSRetryAfter(t *testing.T) {
	now := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		raw   string
		want  time.Duration
		valid bool
	}{
		{"17", 17 * time.Second, true}, {" 0 ", 0, true}, {"", 0, false},
		{"-1", 0, false}, {"+1", 0, false}, {"1.5", 0, false}, {"invalid", 0, false},
		{"99999999999999999999", 0, false}, {"9223372037", 0, false},
		{now.Add(time.Minute).Format(http.TimeFormat), time.Minute, true},
		{now.Add(-time.Minute).Format(http.TimeFormat), 0, true},
	} {
		t.Run(tc.raw, func(t *testing.T) {
			delay, valid := giemaSMSRetryAfter(tc.raw, now)
			require.Equal(t, tc.valid, valid)
			require.Equal(t, tc.want, delay)
		})
	}
}

func TestGiemaSMSClientRejectsInvalidInputsWithoutRequests(t *testing.T) {
	for _, key := range []string{"", " ", "test\nkey", "test key", "test\x7fkey"} {
		_, err := NewGiemaSMSClient(key)
		require.Error(t, err)
	}
	client := giemaSMSTestClient(t, func(_ http.ResponseWriter, _ *http.Request) { t.Error("unexpected request") })
	_, err := client.Start(context.Background(), "  ")
	require.Error(t, err)
	_, err = client.Status(context.Background(), giemaSMSTestCard, GiemaSMSSession{Token: strings.Repeat("s", 129)})
	require.Error(t, err)
	for _, previous := range []*GiemaSMSResponse{
		{Session: GiemaSMSSession{Token: giemaSMSTestSession}, Attempt: giemaSMSPtr(-1)},
		{Session: GiemaSMSSession{Token: giemaSMSTestSession}, Attempt: giemaSMSPtr(1), CanChange: giemaSMSPtr(false)},
	} {
		_, err = client.Rotate(context.Background(), giemaSMSTestCard, previous)
		require.Error(t, err)
	}
}
