package repository

import (
	"context"
	"encoding/json"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestBoundCodexSnapshotSQLBindsIdentityAndTimestamp(t *testing.T) {
	for _, affected := range []int64{0, 1} {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		t.Cleanup(func() { _ = db.Close() })
		proxyID := int64(21)
		expected := &service.Account{ID: 42, Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth,
			Credentials: map[string]any{"access_token": "synthetic", "nested": map[string]any{"x": nil}}, ProxyID: &proxyID}
		credentials, err := json.Marshal(expected.Credentials)
		require.NoError(t, err)
		at := time.Date(2026, time.September, 6, 12, 0, 0, 123456000, time.UTC)
		mock.ExpectExec(regexp.QuoteMeta(updateOpenAICodexBoundSnapshotQuery)).
			WithArgs(`{"codex_7d_used_percent":20,"codex_usage_updated_at":"2026-09-06T12:00:00.123456Z"}`,
				expected.ID, at, expected.Platform, expected.Type, string(credentials), proxyID, `{}`).
			WillReturnResult(sqlmock.NewResult(0, affected))
		repo := &accountRepository{sql: db}
		applied, err := repo.UpdateOpenAICodexSnapshotIfIdentityMatches(context.Background(), expected,
			map[string]any{"codex_usage_updated_at": "2026-09-06T20:00:00.123456789+08:00", "codex_7d_used_percent": 20.0})
		require.NoError(t, err)
		require.Equal(t, affected == 1, applied)
		require.NoError(t, mock.ExpectationsWereMet())
	}
	require.Contains(t, updateOpenAICodexBoundSnapshotQuery, openAICodexCurrentTimestampExpression)
	require.Contains(t, updateOpenAICodexBoundSnapshotQuery, "proxy_id IS NOT DISTINCT FROM $7")
	require.Contains(t, updateOpenAICodexBoundSnapshotQuery, "parent_account_id IS NULL")
}

func TestBoundCodexSnapshotRejectsNonRuntimePatchesAndInvalidIdentityBeforeSQL(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	repo := &accountRepository{sql: db}
	account := &service.Account{ID: 42, Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth, Credentials: map[string]any{}}
	for _, field := range []string{"codex_7d_estimate_baseline", "codex_7d_estimate_epoch", service.OpenAIWeeklyStateRevisionKey, "codex_7d_estimate_new_field", "codex_fingerprint_seed", "codex_quota_reset_credits", "unrelated"} {
		t.Run(field, func(t *testing.T) {
			applied, err := repo.UpdateOpenAICodexSnapshotIfIdentityMatches(context.Background(), account,
				map[string]any{"codex_usage_updated_at": "2026-09-06T12:00:00Z", field: nil})
			require.False(t, applied)
			require.ErrorContains(t, err, "non-runtime quota field")
		})
	}
	for _, patch := range []map[string]any{nil, {}, {"codex_usage_updated_at": "bad"}, {"codex_usage_updated_at": 1}, {"codex_7d_used_percent": 2}} {
		applied, err := repo.UpdateOpenAICodexSnapshotIfIdentityMatches(context.Background(), account, patch)
		require.False(t, applied)
		require.Error(t, err)
	}
	parentID := int64(9)
	for _, invalid := range []*service.Account{
		nil, {}, {ID: 1, Platform: service.PlatformGemini, Type: service.AccountTypeOAuth},
		{ID: 1, Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey},
		{ID: 1, Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth, ParentAccountID: &parentID},
		{ID: 1, Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth, QuotaDimension: service.QuotaDimensionSpark},
	} {
		applied, err := repo.UpdateOpenAICodexSnapshotIfIdentityMatches(context.Background(), invalid,
			map[string]any{"codex_usage_updated_at": "2026-09-06T12:00:00Z"})
		require.False(t, applied)
		require.Error(t, err)
	}
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestBoundQuotaSnapshotRejectsInvalidMetadataAndEstimatePatches(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	repo := &accountRepository{sql: db}
	a := &service.Account{ID: 42, Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth, Credentials: map[string]any{}}
	at := time.Date(2026, 9, 6, 1, 2, 3, 0, time.UTC)
	for _, patch := range []map[string]any{
		nil,
		{"codex_7d_used_percent": 10},
		{"codex_usage_updated_at": at.Add(time.Hour).Format(time.RFC3339Nano), "codex_7d_used_percent": 10},
		{"codex_reset_credit_snapshot": map[string]any{"available_count": 0}},
		{"codex_reset_credit_snapshot_updated_at": at.Format(time.RFC3339Nano)},
		{"codex_credits_snapshot": map[string]any{"credits": nil}},
		{"codex_credits_snapshot_updated_at": at.Format(time.RFC3339Nano)},
		{"codex_credits_snapshot": map[string]any{"credits": nil}, "codex_credits_snapshot_updated_at": at.Add(time.Second).Format(time.RFC3339Nano)},
		{"codex_usage_updated_at": at.Format(time.RFC3339Nano), "codex_7d_estimate_baseline": nil},
		{"codex_usage_updated_at": at.Format(time.RFC3339Nano), service.OpenAIWeeklyStateRevisionKey: 2},
		{"codex_usage_updated_at": at.Format(time.RFC3339Nano), "codex_fingerprint_seed": "forbidden"},
	} {
		applied, err := repo.UpdateOpenAIQuotaSnapshotIfIdentityMatches(context.Background(), a, a, at, patch)
		require.Error(t, err)
		require.False(t, applied)
	}
	patch := map[string]any{"codex_usage_updated_at": at.Format(time.RFC3339Nano)}
	applied, err := repo.UpdateOpenAIQuotaSnapshotIfIdentityMatches(context.Background(), a, a, time.Time{}, patch)
	require.Error(t, err)
	require.False(t, applied)
	other := *a
	other.ID++
	applied, err = repo.UpdateOpenAIQuotaSnapshotIfIdentityMatches(context.Background(), a, &other, at, patch)
	require.Error(t, err)
	require.False(t, applied)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestBoundQuotaOrdinaryCreditsIndependentOfRawWindows(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	a := &service.Account{ID: 42, Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth, Credentials: map[string]any{}}
	at := time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)
	patch := map[string]any{"codex_credits_snapshot": map[string]any{"credits": nil, "fetched_at": at.Unix()}, "codex_credits_snapshot_updated_at": at.Format(time.RFC3339Nano)}
	payload, err := json.Marshal(patch)
	require.NoError(t, err)
	mock.ExpectExec(regexp.QuoteMeta(updateOpenAIQuotaBoundSnapshotQuery)).
		WithArgs(string(payload), a.ID, at, a.Platform, a.Type, `{}`, nil, a.ID, a.Platform, a.Type, `{}`, nil, nil, service.QuotaDimensionGlobal, `{}`, `{}`).
		WillReturnResult(sqlmock.NewResult(0, 1))
	applied, err := (&accountRepository{sql: db}).UpdateOpenAIQuotaSnapshotIfIdentityMatches(context.Background(), a, a, at, patch)
	require.NoError(t, err)
	require.True(t, applied)
	require.NoError(t, mock.ExpectationsWereMet())
	require.Contains(t, updateOpenAIQuotaBoundSnapshotQuery, "extra ->> 'codex_credits_snapshot_updated_at'")
	require.Error(t, validateBoundCodexSnapshotFields(patch, false))
}

func TestBoundQuotaSnapshotSQLBindsTargetAndParentRevisions(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	parent := &service.Account{ID: 41, Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth,
		Credentials: map[string]any{}, Extra: map[string]any{service.OpenAIWeeklyStateRevisionKey: json.Number("9007199254740991")}}
	target := &service.Account{ID: 42, Platform: parent.Platform, Type: parent.Type,
		Credentials: map[string]any{}, ParentAccountID: &parent.ID, QuotaDimension: service.QuotaDimensionSpark,
		Extra: map[string]any{service.OpenAIWeeklyStateRevisionKey: 3}}
	at := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	mock.ExpectExec(regexp.QuoteMeta(updateOpenAIQuotaBoundSnapshotQuery)).
		WithArgs(`{"codex_usage_updated_at":"2026-09-06T12:00:00Z"}`, target.ID, at, target.Platform, target.Type, `{}`, nil,
			parent.ID, parent.Platform, parent.Type, `{}`, nil, parent.ID, service.QuotaDimensionSpark,
			`{"codex_7d_estimate_revision":3}`, `{"codex_7d_estimate_revision":9007199254740991}`).
		WillReturnResult(sqlmock.NewResult(0, 1))
	applied, err := (&accountRepository{sql: db}).UpdateOpenAIQuotaSnapshotIfIdentityMatches(context.Background(), target, parent, at,
		map[string]any{"codex_usage_updated_at": at.Format(time.RFC3339Nano)})
	require.NoError(t, err)
	require.True(t, applied)
	require.NoError(t, mock.ExpectationsWereMet())
	require.Contains(t, updateOpenAIQuotaBoundSnapshotQuery, "IS NOT DISTINCT FROM ($15::jsonb -> 'codex_7d_estimate_revision')")
	require.Contains(t, updateOpenAIQuotaBoundSnapshotQuery, "IS NOT DISTINCT FROM ($16::jsonb -> 'codex_7d_estimate_revision')")
}

func TestBoundQuotaSnapshotRevisionPreservesPresenceAndIgnoresUnrelatedExtra(t *testing.T) {
	for _, tc := range []struct {
		extra map[string]any
		want  string
	}{
		{nil, `{}`},
		{map[string]any{"unrelated": make(chan int)}, `{}`},
		{map[string]any{service.OpenAIWeeklyStateRevisionKey: nil}, `{"codex_7d_estimate_revision":null}`},
		{map[string]any{service.OpenAIWeeklyStateRevisionKey: 7}, `{"codex_7d_estimate_revision":7}`},
	} {
		got, err := openAIQuotaSnapshotRevisionJSON(tc.extra)
		require.NoError(t, err)
		require.Equal(t, tc.want, got)
	}
	_, err := openAIQuotaSnapshotRevisionJSON(map[string]any{service.OpenAIWeeklyStateRevisionKey: make(chan int)})
	require.EqualError(t, err, "quota snapshot revision is not JSON-encodable")
}
