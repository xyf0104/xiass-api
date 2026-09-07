package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func weeklyJoinTestWindow() weeklyJoinWindow {
	created := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	return weeklyJoinWindow{
		accountID: 81, identity: "synthetic-identity", createdAt: created,
		now: created.Add(24 * time.Hour), resetAt: created.Add(6 * 24 * time.Hour),
		firstPaid:    sql.NullTime{Time: created.Add(10 * time.Minute), Valid: true},
		firstNonzero: sql.NullTime{Time: created.Add(10 * time.Minute), Valid: true},
	}
}

func weeklyJoinTestBody(w weeklyJoinWindow, percent float64, imported bool) map[string]any {
	observed := w.createdAt.Add(2 * time.Second)
	if imported {
		observed = w.createdAt.Add(-time.Hour)
	}
	return map[string]any{
		"platform": "openai", "type": "oauth",
		"credentials": map[string]any{"chatgpt_account_id": w.identity, "access_token": "***"},
		"extra": map[string]any{
			"codex_7d_used_percent":  percent,
			"codex_usage_updated_at": w.createdAt.Add(time.Minute).Format(time.RFC3339Nano),
			"codex_7d_reset_at":      w.resetAt.Format(time.RFC3339Nano),
			"codex_7d_estimate_baseline": map[string]any{
				"identity": w.identity, "snapshot_percent": percent, "snapshot_cost": 0,
				"observed_at": observed.Format(time.RFC3339Nano), "reset_at": w.resetAt.Format(time.RFC3339Nano),
			},
		},
	}
}

func weeklyJoinTestEncode(t *testing.T, body map[string]any) string {
	t.Helper()
	encoded, err := json.Marshal(body)
	require.NoError(t, err)
	return string(encoded)
}

func weeklyJoinTestBaseline(body map[string]any) map[string]any {
	extra, ok := body["extra"].(map[string]any)
	if !ok {
		panic("weekly join test extra must be a map")
	}
	baseline, ok := extra["codex_7d_estimate_baseline"].(map[string]any)
	if !ok {
		panic("weekly join test baseline must be a map")
	}
	return baseline
}

func TestOpenAIWeeklyJoinEvidenceSyntheticOrigins(t *testing.T) {
	w := weeklyJoinTestWindow()
	for _, tc := range []struct {
		name     string
		percent  float64
		imported bool
	}{
		{"zero local", 0, false}, {"seventeen local", 17, false},
		{"eleven imported corroborated", 11, true}, {"fractional join", 20.25, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := weeklyJoinTestBody(w, tc.percent, tc.imported)
			if !tc.imported {
				// A later outer snapshot after paid usage cannot move the nested join.
				extra, ok := body["extra"].(map[string]any)
				require.True(t, ok)
				extra["codex_usage_updated_at"] = w.createdAt.Add(time.Hour).Format(time.RFC3339Nano)
				extra["codex_7d_used_percent"] = 72
				extra["later_cost"] = 123.45
			}
			result := weeklyJoinCandidate(w, 123, w.now, weeklyJoinTestEncode(t, body))
			require.NotNil(t, result)
			require.Equal(t, tc.percent, result.Percent)
			require.Zero(t, result.Cost)
			require.Equal(t, int64(123), result.AuditID)
			require.Equal(t, w.identity, result.Identity)
			require.Equal(t, w.resetAt, result.ResetAt)
			require.Equal(t, w.now, result.VerifiedAt)
			require.NotEmpty(t, result.Reason)
			if tc.imported {
				require.Equal(t, service.OpenAIWeeklyJoinEvidenceImportedCorroboration, result.Kind)
				require.Equal(t, w.createdAt.Add(time.Minute), result.ObservedAt)
				require.True(t, result.BaselineObservedAt.Before(w.createdAt))
			} else {
				require.Equal(t, service.OpenAIWeeklyJoinEvidenceLocalInception, result.Kind)
				require.Equal(t, w.createdAt.Add(2*time.Second), result.ObservedAt)
			}
		})
	}
}

func TestOpenAIWeeklyJoinEvidenceAcceptsExplicitV13ImportedEndpoint(t *testing.T) {
	w := weeklyJoinTestWindow()
	body := weeklyJoinTestBody(w, 11, true)
	baseline := weeklyJoinTestBaseline(body)
	delete(baseline, "snapshot_percent")
	baseline["version"] = 13
	baseline["percent_bucket"] = 11

	result := weeklyJoinCandidate(w, 123, w.now, weeklyJoinTestEncode(t, body))
	require.NotNil(t, result)
	require.Equal(t, 11.0, result.Percent)
	require.Equal(t, service.OpenAIWeeklyJoinEvidenceImportedCorroboration, result.Kind)
}

func TestOpenAIWeeklyJoinEvidenceRejectsUnversionedOrMalformedLegacyPercent(t *testing.T) {
	w := weeklyJoinTestWindow()
	for _, tc := range []struct {
		name    string
		version any
		bucket  any
	}{
		{name: "missing version", bucket: 11},
		{name: "wrong version", version: 14, bucket: 11},
		{name: "missing bucket", version: 13},
		{name: "fractional bucket", version: 13, bucket: 11.5},
		{name: "string bucket", version: 13, bucket: "11"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := weeklyJoinTestBody(w, 11, true)
			baseline := weeklyJoinTestBaseline(body)
			delete(baseline, "snapshot_percent")
			if tc.version != nil {
				baseline["version"] = tc.version
			}
			if tc.bucket != nil {
				baseline["percent_bucket"] = tc.bucket
			}
			require.Nil(t, weeklyJoinCandidate(w, 123, w.now, weeklyJoinTestEncode(t, body)))
		})
	}
}

func TestOpenAIWeeklyJoinEvidenceRejectsUnprovenCandidates(t *testing.T) {
	w := weeklyJoinTestWindow()
	for _, tc := range []struct {
		name     string
		imported bool
		edit     func(map[string]any, map[string]any, map[string]any)
	}{
		{"missing cost", false, func(_, _, b map[string]any) { delete(b, "snapshot_cost") }},
		{"null cost", false, func(_, _, b map[string]any) { b["snapshot_cost"] = nil }},
		{"string zero", false, func(_, _, b map[string]any) { b["snapshot_cost"] = "0" }},
		{"redacted cost", false, func(_, _, b map[string]any) { b["snapshot_cost"] = "***" }},
		{"positive cost", false, func(_, _, b map[string]any) { b["snapshot_cost"] = 0.01 }},
		{"negative cost", false, func(_, _, b map[string]any) { b["snapshot_cost"] = -1 }},
		{"underflow cost", false, func(_, _, b map[string]any) { b["snapshot_cost"] = json.Number("1e-999") }},
		{"missing percent", false, func(_, _, b map[string]any) { delete(b, "snapshot_percent") }},
		{"null percent", false, func(_, _, b map[string]any) { b["snapshot_percent"] = nil }},
		{"string percent", false, func(_, _, b map[string]any) { b["snapshot_percent"] = "17" }},
		{"bool percent", false, func(_, _, b map[string]any) { b["snapshot_percent"] = false }},
		{"negative percent", false, func(_, _, b map[string]any) { b["snapshot_percent"] = -0.1 }},
		{"one hundred", false, func(_, _, b map[string]any) { b["snapshot_percent"] = 100 }},
		{"over ninety nine", false, func(_, _, b map[string]any) { b["snapshot_percent"] = json.Number("99.00000000000000001") }},
		{"overflow percent", false, func(_, _, b map[string]any) { b["snapshot_percent"] = json.Number("1e999") }},
		{"underflow percent", false, func(_, _, b map[string]any) { b["snapshot_percent"] = json.Number("1e-999") }},
		{"missing identity", false, func(_, _, b map[string]any) { delete(b, "identity") }},
		{"other identity", false, func(_, _, b map[string]any) { b["identity"] = "other" }},
		{"redacted identity", false, func(_, _, b map[string]any) { b["identity"] = "***" }},
		{"other credentials identity", false, func(r, _, _ map[string]any) {
			credentials, ok := r["credentials"].(map[string]any)
			require.True(t, ok)
			credentials["chatgpt_account_id"] = "other"
		}},
		{"other platform", false, func(r, _, _ map[string]any) { r["platform"] = "anthropic" }},
		{"key type", false, func(r, _, _ map[string]any) { r["type"] = "apikey" }},
		{"missing observation", false, func(_, _, b map[string]any) { delete(b, "observed_at") }},
		{"no timezone", false, func(_, _, b map[string]any) { b["observed_at"] = "2026-08-10T12:00:02" }},
		{"future observation", false, func(_, _, b map[string]any) { b["observed_at"] = w.now.Add(time.Second).Format(time.RFC3339Nano) }},
		{"late paid observation", false, func(_, _, b map[string]any) { b["observed_at"] = w.createdAt.Add(time.Hour).Format(time.RFC3339Nano) }},
		{"same timestamp as paid", false, func(_, _, b map[string]any) { b["observed_at"] = w.firstPaid.Time.Format(time.RFC3339Nano) }},
		{"missing reset", false, func(_, _, b map[string]any) { delete(b, "reset_at") }},
		{"other week", false, func(_, _, b map[string]any) { b["reset_at"] = w.resetAt.Add(-weeklyJoinWeek).Format(time.RFC3339Nano) }},
		{"reset drift uncertain", false, func(_, _, b map[string]any) {
			b["reset_at"] = w.resetAt.Add(time.Minute + time.Nanosecond).Format(time.RFC3339Nano)
		}},
		{"pre week observation", true, func(_, _, b map[string]any) {
			b["observed_at"] = w.resetAt.Add(-weeklyJoinWeek - 2*time.Minute).Format(time.RFC3339Nano)
		}},
		{"import alone", true, func(_, e, _ map[string]any) { delete(e, "codex_usage_updated_at") }},
		{"import changed raw percent", true, func(_, e, _ map[string]any) { e["codex_7d_used_percent"] = 18 }},
		{"import tiny changed raw percent", true, func(_, e, _ map[string]any) { e["codex_7d_used_percent"] = json.Number("17.000000000000000001") }},
		{"import missing raw percent", true, func(_, e, _ map[string]any) { delete(e, "codex_7d_used_percent") }},
		{"import redacted raw percent", true, func(_, e, _ map[string]any) { e["codex_7d_used_percent"] = "***" }},
		{"import raw predates creation", true, func(_, e, _ map[string]any) {
			e["codex_usage_updated_at"] = w.createdAt.Add(-time.Second).Format(time.RFC3339Nano)
		}},
		{"import raw late paid", true, func(_, e, _ map[string]any) {
			e["codex_usage_updated_at"] = w.createdAt.Add(time.Hour).Format(time.RFC3339Nano)
		}},
		{"import raw future", true, func(_, e, _ map[string]any) {
			e["codex_usage_updated_at"] = w.now.Add(time.Hour).Format(time.RFC3339Nano)
		}},
		{"import raw other week", true, func(_, e, _ map[string]any) {
			e["codex_7d_reset_at"] = w.resetAt.Add(weeklyJoinWeek).Format(time.RFC3339Nano)
		}},
		{"import raw missing reset", true, func(_, e, _ map[string]any) { delete(e, "codex_7d_reset_at") }},
		{"import raw lacks identity", true, func(r, _, _ map[string]any) { delete(r, "credentials") }},
		{"import raw redacted identity", true, func(r, _, _ map[string]any) { r["credentials"] = "***" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := weeklyJoinTestBody(w, 17, tc.imported)
			extra, ok := body["extra"].(map[string]any)
			require.True(t, ok)
			tc.edit(body, extra, weeklyJoinTestBaseline(body))
			require.Nil(t, weeklyJoinCandidate(w, 12, w.now, weeklyJoinTestEncode(t, body)))
		})
	}
}

func TestOpenAIWeeklyJoinEvidenceJSONAndTimeBounds(t *testing.T) {
	w := weeklyJoinTestWindow()
	body := weeklyJoinTestEncode(t, weeklyJoinTestBody(w, 0, false))
	for _, invalid := range []string{
		"", "null", "[]", "<credential-bearing body omitted>", "<redacted>",
		body[:len(body)-2], body + "...<truncated>", body + " {}",
		strings.Replace(body, `"snapshot_cost":0`, `"snapshot_cost":1,"snapshot_cost":0`, 1),
		strings.Replace(body, `"snapshot_cost":0`, `"snapshot_cost":0,"snapshot_cost":1`, 1),
		strings.Repeat(" ", weeklyJoinBodyLimit) + body,
		strings.Repeat(`[`, 26) + `0` + strings.Repeat(`]`, 26),
		`{"extra":"` + string([]byte{0xff}) + `"}`,
	} {
		require.Nil(t, weeklyJoinCandidate(w, 12, w.now, invalid))
	}
	require.Nil(t, weeklyJoinCandidate(w, 0, w.now, body))
	require.Nil(t, weeklyJoinCandidate(w, 12, w.createdAt.Add(-time.Second), body))
	require.Nil(t, weeklyJoinCandidate(w, 12, w.createdAt.Add(time.Second), body), "audit cannot precede nested observation")
	require.Nil(t, weeklyJoinCandidate(w, 12, w.now.Add(time.Second), body))
	w.firstNonzero = sql.NullTime{Valid: true, Time: w.createdAt.Add(time.Second)}
	require.Nil(t, weeklyJoinCandidate(w, 12, w.now, body), "negative/unknown earlier account costs disqualify too")
	for _, n := range []string{"0", "-0", "0.0", "0e999999999"} {
		v, ok := weeklyJoinNumber(json.Number(n))
		require.True(t, ok)
		require.Zero(t, v)
		require.True(t, weeklyJoinNumbersEqual(json.Number(n), json.Number("0")))
	}
}

func weeklyJoinMockAccount() *service.Account {
	w := weeklyJoinTestWindow()
	return &service.Account{ID: w.accountID, Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth, Credentials: map[string]any{"chatgpt_account_id": w.identity}}
}

func TestOpenAIWeeklyJoinEvidenceInvalidInput(t *testing.T) {
	for _, change := range []func(*service.Account){
		func(a *service.Account) { a.ID = 0 }, func(a *service.Account) { a.ID = -1 },
		func(a *service.Account) { a.Platform = "anthropic" }, func(a *service.Account) { a.Type = "apikey" }, func(a *service.Account) { a.Type = "setup-token" },
		func(a *service.Account) { a.Credentials = nil }, func(a *service.Account) { a.Credentials["chatgpt_account_id"] = nil },
		func(a *service.Account) { a.Credentials["chatgpt_account_id"] = 123 }, func(a *service.Account) { a.Credentials["chatgpt_account_id"] = "***" },
		func(a *service.Account) { a.Credentials["chatgpt_account_id"] = " " },
	} {
		a := weeklyJoinMockAccount()
		change(a)
		var r *accountRepository
		e, err := r.FindOpenAIWeeklyJoinEvidence(context.Background(), a, weeklyJoinTestWindow().resetAt)
		require.Nil(t, e)
		require.ErrorIs(t, err, service.ErrOpenAIWeeklyJoinEvidenceInvalidInput)
	}
	var r *accountRepository
	_, err := r.FindOpenAIWeeklyJoinEvidence(context.Background(), nil, weeklyJoinTestWindow().resetAt)
	require.ErrorIs(t, err, service.ErrOpenAIWeeklyJoinEvidenceInvalidInput)
	_, err = r.FindOpenAIWeeklyJoinEvidence(context.Background(), weeklyJoinMockAccount(), time.Time{})
	require.ErrorIs(t, err, service.ErrOpenAIWeeklyJoinEvidenceInvalidInput)
	_, err = r.FindOpenAIWeeklyJoinEvidence(context.Background(), weeklyJoinMockAccount(), weeklyJoinTestWindow().resetAt)
	require.ErrorIs(t, err, service.ErrOpenAIWeeklyJoinEvidenceSnapshotRequired)
}

func TestOpenAIWeeklyJoinEvidenceDatabaseFailures(t *testing.T) {
	w := weeklyJoinTestWindow()
	for _, stage := range []string{"begin", "account", "usage", "audit", "scan", "rows", "close", "commit"} {
		t.Run(stage, func(t *testing.T) {
			db, m, err := sqlmock.New()
			require.NoError(t, err)
			defer func() { _ = db.Close() }()
			failure := errors.New("synthetic database failure")
			begin := m.ExpectBegin()
			if stage == "begin" {
				begin.WillReturnError(failure)
			} else {
				account := m.ExpectQuery(regexp.QuoteMeta(weeklyJoinAccountQuery))
				if stage == "account" {
					account.WillReturnError(failure)
				} else {
					account.WillReturnRows(sqlmock.NewRows([]string{"created_at", "now"}).AddRow(w.createdAt, w.now))
					usage := m.ExpectQuery("SELECT MIN\\(u.created_at\\)")
					if stage == "usage" {
						usage.WillReturnError(failure)
					} else {
						usage.WillReturnRows(sqlmock.NewRows([]string{"first_paid", "first_nonzero"}).AddRow(w.firstPaid.Time, w.firstNonzero.Time))
						audit := m.ExpectQuery(regexp.QuoteMeta(weeklyJoinAuditQuery))
						if stage == "audit" {
							audit.WillReturnError(failure)
						} else {
							rows := sqlmock.NewRows([]string{"id", "created_at", "body"})
							switch stage {
							case "scan":
								rows.AddRow("not-an-id", w.now, "")
							case "rows":
								rows.AddRow(12, w.now, "").RowError(0, failure)
							case "close":
								rows.CloseError(failure)
							default:
								rows.AddRow(12, w.now, weeklyJoinTestEncode(t, weeklyJoinTestBody(w, 0, false)))
							}
							audit.WillReturnRows(rows)
						}
					}
				}
				if stage == "commit" {
					m.ExpectCommit().WillReturnError(failure)
				} else {
					m.ExpectRollback()
				}
			}
			r := newAccountRepositoryWithSQL(nil, db, nil)
			e, err := r.FindOpenAIWeeklyJoinEvidence(context.Background(), weeklyJoinMockAccount(), w.resetAt)
			require.Nil(t, e)
			require.Error(t, err)
			if stage != "scan" {
				require.ErrorIs(t, err, failure)
			}
			require.NoError(t, m.ExpectationsWereMet())
		})
	}
}
