package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"math/big"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

const (
	weeklyJoinAuditLimit     = 256
	weeklyJoinBodyLimit      = 16 * 1024
	weeklyJoinResetTolerance = time.Minute
	weeklyJoinWeek           = 7 * 24 * time.Hour
	weeklyJoinTimeout        = 5 * time.Second
)

var _ service.OpenAIWeeklyJoinEvidenceRepository = (*accountRepository)(nil)

const weeklyJoinAccountQuery = `SELECT created_at, CURRENT_TIMESTAMP FROM accounts
	WHERE id = $1 AND deleted_at IS NULL AND platform = 'openai' AND type = 'oauth'
	AND credentials -> 'chatgpt_account_id' = to_jsonb($2::text)`

// Bound both the indexed time range and the result size. Never cast request_body
// to JSON in SQL, nor transfer an oversized body just to discard it in Go.
const weeklyJoinAuditQuery = `SELECT id, created_at,
	CASE WHEN octet_length(request_body) <= $4 THEN request_body ELSE '' END
	FROM audit_logs
	WHERE created_at >= $2 AND created_at <= $3
	AND extra #>> '{params,id}' = $1
	AND status_code >= 200 AND status_code < 300
	AND method IN ('PUT', 'PATCH')
	AND path IN ('/api/v1/admin/accounts/:id', '/api/v1/admin/accounts/' || $1)
	ORDER BY created_at ASC, id ASC LIMIT $5`

func (r *accountRepository) FindOpenAIWeeklyJoinEvidence(ctx context.Context, expected *service.Account, currentResetAt time.Time) (*service.OpenAIWeeklyJoinEvidence, error) {
	if expected == nil || expected.ID <= 0 || expected.Platform != service.PlatformOpenAI || expected.Type != service.AccountTypeOAuth || currentResetAt.IsZero() {
		return nil, service.ErrOpenAIWeeklyJoinEvidenceInvalidInput
	}
	identity, ok := expected.Credentials["chatgpt_account_id"].(string)
	if !ok || !weeklyJoinIdentityValid(identity) {
		return nil, service.ErrOpenAIWeeklyJoinEvidenceInvalidInput
	}
	ctx, cancel := context.WithTimeout(ctx, weeklyJoinTimeout)
	defer cancel()
	if ambient := dbent.TxFromContext(ctx); ambient != nil {
		var isolation, readOnly string
		if err := scanSingleRow(ctx, ambient.Client(), `SELECT current_setting('transaction_isolation'), current_setting('transaction_read_only')`, nil, &isolation, &readOnly); err != nil {
			return nil, err
		}
		if readOnly != "on" || (isolation != "repeatable read" && isolation != "serializable") {
			return nil, service.ErrOpenAIWeeklyJoinEvidenceSnapshotRequired
		}
		return findWeeklyJoinEvidenceSnapshot(ctx, ambient.Client(), expected.ID, identity, currentResetAt)
	}
	if r == nil {
		return nil, service.ErrOpenAIWeeklyJoinEvidenceSnapshotRequired
	}
	beginner, ok := r.sql.(interface {
		BeginTx(context.Context, *sql.TxOptions) (*sql.Tx, error)
	})
	if !ok {
		return nil, service.ErrOpenAIWeeklyJoinEvidenceSnapshotRequired
	}
	tx, err := beginner.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	evidence, err := findWeeklyJoinEvidenceSnapshot(ctx, tx, expected.ID, identity, currentResetAt)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return evidence, nil
}

type weeklyJoinWindow struct {
	accountID               int64
	identity                string
	createdAt, now, resetAt time.Time
	firstPaid, firstNonzero sql.NullTime
}

func findWeeklyJoinEvidenceSnapshot(ctx context.Context, q sqlQueryer, accountID int64, identity string, resetAt time.Time) (*service.OpenAIWeeklyJoinEvidence, error) {
	w := weeklyJoinWindow{accountID: accountID, identity: identity, resetAt: resetAt}
	err := scanSingleRow(ctx, q, weeklyJoinAccountQuery, []any{accountID, identity}, &w.createdAt, &w.now)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !resetAt.After(w.now) || resetAt.Sub(w.now) > weeklyJoinWeek+weeklyJoinResetTolerance || w.createdAt.After(w.now) || w.createdAt.Before(resetAt.Add(-weeklyJoinWeek-weeklyJoinResetTolerance)) {
		return nil, nil
	}
	// Any nonzero/unknown account cost is disqualifying, not merely a positive
	// sum. Negative adjustments cannot cancel paid usage into a false zero.
	cost := usageLogAccountCostExpression("u")
	query := fmt.Sprintf(`SELECT MIN(u.created_at) FILTER (WHERE (%[1]s) > 0),
		MIN(u.created_at) FILTER (WHERE (%[1]s) IS DISTINCT FROM 0)
		FROM usage_logs u WHERE u.account_id = $1 AND u.created_at >= $2 AND u.created_at <= $3`, cost)
	if err := scanSingleRow(ctx, q, query, []any{accountID, w.createdAt, w.now}, &w.firstPaid, &w.firstNonzero); err != nil {
		return nil, err
	}
	rows, err := q.QueryContext(ctx, weeklyJoinAuditQuery, strconv.FormatInt(accountID, 10), w.createdAt, w.now, weeklyJoinBodyLimit, weeklyJoinAuditLimit+1)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var found *service.OpenAIWeeklyJoinEvidence
	count, ambiguous := 0, false
	for rows.Next() {
		var id int64
		var at time.Time
		var body string
		if err := rows.Scan(&id, &at, &body); err != nil {
			return nil, err
		}
		count++
		candidate := weeklyJoinCandidate(w, id, at, body)
		if candidate == nil {
			continue
		}
		if found != nil && (candidate.Percent != found.Percent || candidate.ResetAt.Sub(found.ResetAt).Abs() > weeklyJoinResetTolerance) {
			ambiguous = true
		}
		if found == nil || candidate.ObservedAt.Before(found.ObservedAt) {
			found = candidate
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if count > weeklyJoinAuditLimit || ambiguous {
		return nil, nil
	}
	return found, nil
}

func (w weeklyJoinWindow) localZeroAt(at time.Time) bool {
	return !at.Before(w.createdAt) && !at.After(w.now) &&
		(!w.firstPaid.Valid || at.Before(w.firstPaid.Time)) &&
		(!w.firstNonzero.Valid || at.Before(w.firstNonzero.Time))
}

func (w weeklyJoinWindow) sameWeek(at, reset time.Time) bool {
	return !at.IsZero() && !reset.IsZero() && reset.After(w.now) && at.Before(reset) &&
		!at.Before(reset.Add(-weeklyJoinWeek-weeklyJoinResetTolerance)) &&
		!at.Before(w.resetAt.Add(-weeklyJoinWeek-weeklyJoinResetTolerance)) &&
		reset.Sub(w.resetAt).Abs() <= weeklyJoinResetTolerance
}

func weeklyJoinCandidate(w weeklyJoinWindow, auditID int64, auditAt time.Time, body string) *service.OpenAIWeeklyJoinEvidence {
	if auditID <= 0 || auditAt.Before(w.createdAt) || auditAt.After(w.now) {
		return nil
	}
	root, ok := weeklyJoinBody(body)
	if !ok {
		return nil
	}
	for key, want := range map[string]string{"platform": service.PlatformOpenAI, "type": service.AccountTypeOAuth} {
		if value, present := root[key]; present && value != want {
			return nil
		}
	}
	// A supplied identity must agree even for a nested historical observation.
	credentials, _ := root["credentials"].(map[string]any)
	if value, present := credentials["chatgpt_account_id"]; present && value != w.identity {
		return nil
	}
	extra, _ := root["extra"].(map[string]any)
	baseline, _ := extra["codex_7d_estimate_baseline"].(map[string]any)
	if baseline["identity"] != w.identity {
		return nil
	}
	percentValue := baseline["snapshot_percent"]
	percent, ok := weeklyJoinNumber(percentValue)
	if !ok {
		// v13 stored the same aligned endpoint as an integer percent_bucket and
		// did not yet emit snapshot_percent. Accept only that exact historical
		// schema; missing percentages in every other version remain unproven.
		version, versionOK := weeklyJoinNumber(baseline["version"])
		bucket, bucketOK := weeklyJoinNumber(baseline["percent_bucket"])
		if versionOK && version == 13 && bucketOK && math.Trunc(bucket) == bucket {
			percentValue, percent, ok = baseline["percent_bucket"], bucket, true
		}
	}
	if !ok {
		return nil
	}
	cost, ok := weeklyJoinNumber(baseline["snapshot_cost"])
	if !ok || cost != 0 {
		return nil
	}
	observed := weeklyJoinTime(baseline["observed_at"])
	reset := weeklyJoinTime(baseline["reset_at"])
	if observed.After(auditAt) || !w.sameWeek(observed, reset) {
		return nil
	}
	e := &service.OpenAIWeeklyJoinEvidence{
		AccountID: w.accountID, Identity: w.identity, AuditID: auditID, AuditCreatedAt: auditAt,
		BaselineObservedAt: observed, ObservedAt: observed, ResetAt: reset,
		Percent: percent, Cost: 0, VerifiedAt: w.now,
		Kind:   service.OpenAIWeeklyJoinEvidenceLocalInception,
		Reason: "Explicit same-identity nested zero-cost observation after local creation, in the current week; no nonzero canonical local account cost through that observation in the retained snapshot.",
	}
	if !observed.Before(w.createdAt) {
		if !w.localZeroAt(observed) {
			return nil
		}
		return e
	}
	// An imported observation is not a local join. Only a same-body, explicitly
	// identity-bound local raw observation at exactly the imported percent can
	// corroborate it. Never borrow another audit's cost or changed raw percent.
	rawPercent, ok := weeklyJoinNumber(extra["codex_7d_used_percent"])
	if !ok || rawPercent != percent || credentials["chatgpt_account_id"] != w.identity ||
		!weeklyJoinNumbersEqual(extra["codex_7d_used_percent"], percentValue) {
		return nil
	}
	rawAt := weeklyJoinTime(extra["codex_usage_updated_at"])
	rawReset := weeklyJoinTime(extra["codex_7d_reset_at"])
	if rawAt.After(auditAt) || !w.localZeroAt(rawAt) || !w.sameWeek(rawAt, rawReset) || rawReset.Sub(reset).Abs() > weeklyJoinResetTolerance {
		return nil
	}
	e.Kind = service.OpenAIWeeklyJoinEvidenceImportedCorroboration
	e.Reason = "Pre-local imported zero-cost baseline is not used as a local timestamp; an explicit same-identity local raw observation has exactly the same percent and week, with no nonzero canonical local account cost through it in the retained snapshot."
	e.ObservedAt, e.ResetAt = rawAt, rawReset
	return e
}

func weeklyJoinIdentityValid(s string) bool {
	return s != "" && len(s) <= 256 && utf8.ValidString(s) && strings.TrimSpace(s) == s &&
		!strings.ContainsAny(s, "*<>\x00\r\n\t")
}

func weeklyJoinTime(value any) time.Time {
	s, ok := value.(string)
	if !ok {
		return time.Time{}
	}
	at, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}
	}
	return at
}

func weeklyJoinNumber(value any) (float64, bool) {
	n, ok := value.(json.Number)
	if !ok || len(n) > 128 {
		return 0, false
	}
	f, err := n.Float64()
	if err != nil || math.IsNaN(f) || math.IsInf(f, 0) || f < 0 || f > 99 {
		return 0, false
	}
	if f == 0 {
		mantissa, _, _ := strings.Cut(strings.ToLower(n.String()), "e")
		return 0, !strings.ContainsAny(mantissa, "123456789")
	}
	r, ok := new(big.Rat).SetString(n.String())
	return f, ok && r.Sign() >= 0 && r.Cmp(big.NewRat(99, 1)) <= 0
}

func weeklyJoinNumbersEqual(a, b any) bool {
	// Called only after both values pass bounded finite number validation.
	xn, okX := a.(json.Number)
	yn, okY := b.(json.Number)
	if !okX || !okY {
		return false
	}
	xf, errX := xn.Float64()
	yf, errY := yn.Float64()
	if errX != nil || errY != nil {
		return false
	}
	if xf == 0 || yf == 0 {
		return xf == yf
	}
	x, okX := new(big.Rat).SetString(xn.String())
	y, okY := new(big.Rat).SetString(yn.String())
	return okX && okY && x.Cmp(y) == 0
}

// Reject duplicate keys rather than letting last-key-wins parsing invent an
// unambiguous identity/cost. Audit placeholders remain invalid required fields.
func weeklyJoinBody(body string) (map[string]any, bool) {
	if len(body) == 0 || len(body) > weeklyJoinBodyLimit || !utf8.ValidString(body) {
		return nil, false
	}
	d := json.NewDecoder(strings.NewReader(body))
	d.UseNumber()
	v, err := weeklyJoinJSONValue(d, 0)
	if err != nil {
		return nil, false
	}
	if _, err := d.Token(); err != io.EOF {
		return nil, false
	}
	root, ok := v.(map[string]any)
	return root, ok
}

func weeklyJoinJSONValue(d *json.Decoder, depth int) (any, error) {
	if depth > 24 {
		return nil, errors.New("audit JSON depth exceeded")
	}
	token, err := d.Token()
	if err != nil {
		return nil, err
	}
	delim, compound := token.(json.Delim)
	if !compound {
		return token, nil
	}
	if delim == '{' {
		object := map[string]any{}
		for d.More() {
			key, err := d.Token()
			if err != nil {
				return nil, err
			}
			name, ok := key.(string)
			if !ok {
				return nil, errors.New("invalid audit JSON key")
			}
			if _, exists := object[name]; exists {
				return nil, errors.New("duplicate audit JSON key")
			}
			value, err := weeklyJoinJSONValue(d, depth+1)
			if err != nil {
				return nil, err
			}
			object[name] = value
		}
		_, err = d.Token()
		return object, err
	}
	if delim == '[' {
		var values []any
		for d.More() {
			value, err := weeklyJoinJSONValue(d, depth+1)
			if err != nil {
				return nil, err
			}
			values = append(values, value)
		}
		_, err = d.Token()
		return values, err
	}
	return nil, errors.New("invalid audit JSON delimiter")
}
