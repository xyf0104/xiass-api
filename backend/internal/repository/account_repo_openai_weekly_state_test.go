package repository

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"regexp"
	"testing"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/DATA-DOG/go-sqlmock"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

// Any accidental scheduler call through this nil embedded interface fails the test.
type weeklyStateForbiddenScheduler struct{ service.SchedulerCache }

type weeklyStateFailingMarshaler struct{}

func (weeklyStateFailingMarshaler) MarshalJSON() ([]byte, error) {
	return nil, errors.New("synthetic-secret-must-not-appear-in-errors")
}

func weeklyStateUnitAccount() *service.Account {
	return &service.Account{
		ID: 71, Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth,
		Credentials: map[string]any{"refresh_token": "synthetic-test-credential"},
		Extra: map[string]any{
			openAIWeeklyStateEpochKey:        "test-epoch",
			openAIWeeklyStateBaselineKey:     map[string]any{"test_maximum": 100},
			openAIWeeklyStateUsageUpdatedKey: nil,
			"unrelated":                      "preserved",
		},
	}
}

func TestOpenAIWeeklyStateCASOptionalContractAndSQL(t *testing.T) {
	for _, affected := range []int64{0, 1} {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		t.Cleanup(func() { _ = db.Close() })
		repo, ok := NewAccountRepository(nil, db, &weeklyStateForbiddenScheduler{}).(service.OpenAIWeeklyStateRepository)
		require.True(t, ok, "the existing constructor exposes the optional capability")
		proxyID := int64(9)
		expected := weeklyStateUnitAccount()
		expected.ProxyID = &proxyID
		// Only the selected keys, not all Extra, belong to this CAS guard.
		expected.Extra["unrelated"] = func() {}
		mock.ExpectExec(regexp.QuoteMeta(compareAndSwapOpenAIWeeklyStateQuery)).
			WithArgs(`{"codex_7d_estimate_baseline":null}`, expected.ID, service.PlatformOpenAI, service.AccountTypeOAuth,
				`{"refresh_token":"synthetic-test-credential"}`, proxyID,
				`{"codex_7d_estimate_baseline":{"test_maximum":100},"codex_7d_estimate_epoch":"test-epoch","codex_usage_updated_at":null}`).
			WillReturnResult(sqlmock.NewResult(0, affected))
		applied, err := repo.CompareAndSwapOpenAIWeeklyState(context.Background(), expected, map[string]any{openAIWeeklyStateBaselineKey: nil})
		require.NoError(t, err)
		require.Equal(t, affected == 1, applied)
		require.NoError(t, mock.ExpectationsWereMet())
		require.Equal(t, "synthetic-test-credential", expected.Credentials["refresh_token"])
		require.Equal(t, "test-epoch", expected.Extra[openAIWeeklyStateEpochKey])
	}
	require.NotContains(t, compareAndSwapOpenAIWeeklyStateQuery, "synthetic-test-credential")
	require.NotContains(t, compareAndSwapOpenAIWeeklyStateQuery, "updated_at =")
	require.NotContains(t, compareAndSwapOpenAIWeeklyStateQuery, "credentials = $1")
}

func TestOpenAIWeeklyStateCASRejectsInvalidInputBeforeSQL(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	repo := newAccountRepositoryWithSQL(nil, db, &weeklyStateForbiddenScheduler{})
	validPatch := map[string]any{openAIWeeklyStateEpochKey: "new-test-epoch"}
	for _, edit := range []func(*service.Account){
		func(a *service.Account) { a.ID = 0 },
		func(a *service.Account) { a.ID = -1 },
		func(a *service.Account) { a.Platform = service.PlatformAnthropic },
		func(a *service.Account) { a.Platform = "OpenAI" },
		func(a *service.Account) { a.Type = service.AccountTypeAPIKey },
		func(a *service.Account) { a.Type = "setup-token" },
		func(a *service.Account) { a.Credentials["bad"] = weeklyStateFailingMarshaler{} },
		func(a *service.Account) { a.Extra[openAIWeeklyStateEpochKey] = json.RawMessage(`{"bad":`) },
		func(a *service.Account) { a.Extra[openAIWeeklyStateBaselineKey] = math.Inf(1) },
		func(a *service.Account) { a.Extra[openAIWeeklyStateUsageUpdatedKey] = func() {} },
	} {
		a := weeklyStateUnitAccount()
		edit(a)
		applied, err := repo.CompareAndSwapOpenAIWeeklyState(context.Background(), a, validPatch)
		require.False(t, applied)
		require.ErrorIs(t, err, service.ErrOpenAIWeeklyStateInvalidInput)
		require.NotContains(t, err.Error(), "synthetic-secret")
	}
	applied, err := repo.CompareAndSwapOpenAIWeeklyState(context.Background(), nil, validPatch)
	require.False(t, applied)
	require.ErrorIs(t, err, service.ErrOpenAIWeeklyStateInvalidInput)
	for _, key := range []string{"credentials", "access_token", "refresh_token", "proxy_id", "platform", "type",
		"xiass_execution_node_id", "codex_fingerprint_seed", "codex_fingerprint_mode", openAIWeeklyStateUsageUpdatedKey,
		"codex_7d_used_percent", "codex_7d_estimate_baseline.nested", "arbitrary"} {
		applied, err := repo.CompareAndSwapOpenAIWeeklyState(context.Background(), weeklyStateUnitAccount(),
			map[string]any{openAIWeeklyStateBaselineKey: nil, key: "forbidden"})
		require.False(t, applied)
		require.ErrorIs(t, err, service.ErrOpenAIWeeklyStateInvalidInput)
	}
	cycle := map[string]any{}
	cycle["self"] = cycle
	for _, patch := range []map[string]any{nil, {},
		{openAIWeeklyStateBaselineKey: math.NaN()},
		{openAIWeeklyStateBaselineKey: json.RawMessage(`invalid JSON`)},
		{openAIWeeklyStateBaselineKey: cycle},
		{openAIWeeklyStateEpochKey: weeklyStateFailingMarshaler{}},
	} {
		applied, err := repo.CompareAndSwapOpenAIWeeklyState(context.Background(), weeklyStateUnitAccount(), patch)
		require.False(t, applied)
		require.ErrorIs(t, err, service.ErrOpenAIWeeklyStateInvalidInput)
		require.NotContains(t, err.Error(), "synthetic-secret")
	}
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestOpenAIWeeklyStateCASPropagatesDatabaseErrors(t *testing.T) {
	for _, rowError := range []bool{false, true} {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		t.Cleanup(func() { _ = db.Close() })
		dbErr := errors.New("test database failure")
		expect := mock.ExpectExec(regexp.QuoteMeta(compareAndSwapOpenAIWeeklyStateQuery))
		if rowError {
			expect.WillReturnResult(sqlmock.NewErrorResult(dbErr))
		} else {
			expect.WillReturnError(dbErr)
		}
		repo := newAccountRepositoryWithSQL(nil, db, &weeklyStateForbiddenScheduler{})
		applied, err := repo.CompareAndSwapOpenAIWeeklyState(context.Background(), weeklyStateUnitAccount(), map[string]any{openAIWeeklyStateEpochKey: nil})
		require.False(t, applied)
		require.ErrorIs(t, err, dbErr)
		require.NoError(t, mock.ExpectationsWereMet())
	}
	var missing *accountRepository
	applied, err := missing.CompareAndSwapOpenAIWeeklyState(context.Background(), weeklyStateUnitAccount(), map[string]any{openAIWeeklyStateEpochKey: nil})
	require.False(t, applied)
	require.Error(t, err)
}

func TestOpenAIWeeklyStateCASUsesEntTransactionWithoutCommitting(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	defer func() { _ = client.Close() }()
	mock.ExpectBegin()
	tx, err := client.Tx(context.Background())
	require.NoError(t, err)
	mock.ExpectExec(regexp.QuoteMeta(compareAndSwapOpenAIWeeklyStateQuery)).WillReturnResult(sqlmock.NewResult(0, 1))
	// There is no configured base executor. The context transaction is sufficient.
	repo := newAccountRepositoryWithSQL(nil, nil, &weeklyStateForbiddenScheduler{})
	applied, err := repo.CompareAndSwapOpenAIWeeklyState(dbent.NewTxContext(context.Background(), tx), weeklyStateUnitAccount(),
		map[string]any{openAIWeeklyStateEpochKey: "new-test-epoch"})
	require.NoError(t, err)
	require.True(t, applied)
	mock.ExpectRollback()
	require.NoError(t, tx.Rollback())
	require.NoError(t, mock.ExpectationsWereMet())
}
