package repository

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
)

func openAIUsageAuthExpectedAccount() *service.Account {
	proxyID := int64(17)
	return &service.Account{
		ID:       42,
		Platform: service.PlatformOpenAI,
		Type:     service.AccountTypeOAuth,
		Credentials: map[string]any{
			"access_token":   "observed-access",
			"refresh_token":  "observed-refresh",
			"_token_version": int64(9),
			"nested":         map[string]any{"issuer": "synthetic"},
		},
		ProxyID:        &proxyID,
		QuotaDimension: service.QuotaDimensionGlobal,
	}
}

func TestUpdateOpenAIUsageAuthIfIdentityMatchesBindsCompleteIdentityAndAtomicOutbox(t *testing.T) {
	for _, tc := range []struct {
		name         string
		unauthorized bool
		affected     int64
	}{
		{name: "unauthorized applied", unauthorized: true, affected: 1},
		{name: "unauthorized stale identity", unauthorized: true, affected: 0},
		{name: "authorized applied", unauthorized: false, affected: 1},
		{name: "authorized stale identity", unauthorized: false, affected: 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			exec := &recordingSQLExecutor{result: rowsAffectedResult(tc.affected)}
			repo := newAccountRepositoryWithSQL(nil, exec, nil)
			expected := openAIUsageAuthExpectedAccount()
			credentials, err := json.Marshal(expected.Credentials)
			require.NoError(t, err)

			applied, err := repo.UpdateOpenAIUsageAuthIfIdentityMatches(context.Background(), expected, tc.unauthorized)

			require.NoError(t, err)
			require.Equal(t, tc.affected == 1, applied)
			require.Len(t, exec.execQueries, 1)
			require.Len(t, exec.execArgs, 1)
			require.Equal(t, []any{
				tc.unauthorized,
				service.OpenAIUsageUnauthorizedReason,
				expected.ID,
				expected.Platform,
				expected.Type,
				string(credentials),
				expected.ProxyID,
				service.SchedulerOutboxEventAccountChanged,
			}, exec.execArgs[0])

			normalized := normalizeSQLWhitespace(exec.execQueries[0])
			require.Contains(t, normalized, "WITH updated AS ( UPDATE accounts AS a")
			require.Contains(t, normalized, "a.deleted_at IS NULL")
			require.Contains(t, normalized, "a.platform = $4")
			require.Contains(t, normalized, "a.type = $5")
			require.Contains(t, normalized, "a.credentials = $6::jsonb")
			require.Contains(t, normalized, "a.proxy_id IS NOT DISTINCT FROM $7")
			require.Contains(t, normalized, "a.parent_account_id IS NULL")
			require.Contains(t, normalized, "a.quota_dimension = 'global'")
			require.Contains(t, normalized, "GREATEST(a.temp_unschedulable_until, NOW() + INTERVAL '10 minutes')")
			require.Contains(t, normalized, "a.temp_unschedulable_until IS NULL OR a.temp_unschedulable_until <= NOW()")
			require.Contains(t, normalized, "LEFT(LOWER(BTRIM(COALESCE(a.temp_unschedulable_reason, ''))), 10) = 'oauth_401:'")
			require.NotContains(t, normalized, "LIKE 'oauth_401:%'")
			require.Contains(t, normalized, "LIKE 'oauth 401:%'")
			require.Contains(t, normalized, "LIKE 'authentication failed (401)%'")
			require.Contains(t, normalized, "OR (NOT $1 AND a.temp_unschedulable_reason = $2)")
			require.Contains(t, normalized, "INSERT INTO scheduler_outbox")
			require.Contains(t, normalized, "SELECT $8, updated.id, NULL, NULL FROM updated")

			setClause := strings.SplitN(strings.SplitN(normalized, " SET ", 2)[1], " WHERE ", 2)[0]
			require.Contains(t, setClause, "temp_unschedulable_until =")
			require.Contains(t, setClause, "temp_unschedulable_reason =")
			for _, forbidden := range []string{
				"status =", "schedulable =", "credentials =", "extra =", "quota_", "rate_limit_", "overload_", "updated_at =",
			} {
				require.NotContains(t, setClause, forbidden)
			}
		})
	}
}

func TestUpdateOpenAIUsageAuthIfIdentityMatchesRejectsInvalidSnapshotsBeforeSQL(t *testing.T) {
	parentID := int64(7)
	valid := openAIUsageAuthExpectedAccount()
	tests := []struct {
		name    string
		account *service.Account
	}{
		{name: "nil", account: nil},
		{name: "zero id", account: func() *service.Account { a := *valid; a.ID = 0; return &a }()},
		{name: "wrong platform", account: func() *service.Account { a := *valid; a.Platform = service.PlatformGemini; return &a }()},
		{name: "wrong type", account: func() *service.Account { a := *valid; a.Type = service.AccountTypeAPIKey; return &a }()},
		{name: "agent identity", account: func() *service.Account {
			a := *valid
			a.Credentials = map[string]any{"access_token": "observed", "auth_mode": service.OpenAIAuthModeAgentIdentity}
			return &a
		}()},
		{name: "shadow", account: func() *service.Account { a := *valid; a.ParentAccountID = &parentID; return &a }()},
		{name: "non global", account: func() *service.Account { a := *valid; a.QuotaDimension = service.QuotaDimensionSpark; return &a }()},
		{name: "nil credentials", account: func() *service.Account { a := *valid; a.Credentials = nil; return &a }()},
		{name: "missing access token", account: func() *service.Account {
			a := *valid
			a.Credentials = map[string]any{"refresh_token": "refresh"}
			return &a
		}()},
		{name: "blank access token", account: func() *service.Account { a := *valid; a.Credentials = map[string]any{"access_token": " \t"}; return &a }()},
		{name: "non string access token", account: func() *service.Account { a := *valid; a.Credentials = map[string]any{"access_token": 123}; return &a }()},
		{name: "non encodable credentials", account: func() *service.Account {
			a := *valid
			a.Credentials = map[string]any{"access_token": "observed", "bad": make(chan int)}
			return &a
		}()},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			exec := &recordingSQLExecutor{result: rowsAffectedResult(1)}
			repo := newAccountRepositoryWithSQL(nil, exec, nil)

			applied, err := repo.UpdateOpenAIUsageAuthIfIdentityMatches(context.Background(), tc.account, true)

			require.Error(t, err)
			require.False(t, applied)
			require.Empty(t, exec.execQueries)
		})
	}
}

func TestUpdateOpenAIUsageAuthIfIdentityMatchesUsesEntTransactionExecutor(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	t.Cleanup(func() { _ = client.Close() })

	mock.ExpectBegin()
	tx, err := client.Tx(context.Background())
	require.NoError(t, err)

	expected := openAIUsageAuthExpectedAccount()
	expected.ProxyID = nil
	credentials, err := json.Marshal(expected.Credentials)
	require.NoError(t, err)
	mock.ExpectExec(regexp.QuoteMeta(updateOpenAIUsageAuthQuery)).
		WithArgs(
			true,
			service.OpenAIUsageUnauthorizedReason,
			expected.ID,
			expected.Platform,
			expected.Type,
			string(credentials),
			nil,
			service.SchedulerOutboxEventAccountChanged,
		).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectRollback()

	fallback := &recordingSQLExecutor{err: errors.New("fallback executor must not be used")}
	repo := newAccountRepositoryWithSQL(client, fallback, nil)
	ctx := dbent.NewTxContext(context.Background(), tx)
	applied, err := repo.UpdateOpenAIUsageAuthIfIdentityMatches(ctx, expected, true)

	require.NoError(t, err)
	require.True(t, applied)
	require.Empty(t, fallback.execQueries)
	require.NoError(t, tx.Rollback())
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestUpdateOpenAIUsageAuthIfIdentityMatchesPropagatesExecutorFailures(t *testing.T) {
	expected := openAIUsageAuthExpectedAccount()

	t.Run("missing executor", func(t *testing.T) {
		applied, err := (&accountRepository{}).UpdateOpenAIUsageAuthIfIdentityMatches(context.Background(), expected, true)
		require.False(t, applied)
		require.ErrorContains(t, err, "SQL executor")
	})

	t.Run("exec failure", func(t *testing.T) {
		exec := &recordingSQLExecutor{err: errors.New("write failed")}
		applied, err := newAccountRepositoryWithSQL(nil, exec, nil).
			UpdateOpenAIUsageAuthIfIdentityMatches(context.Background(), expected, true)
		require.False(t, applied)
		require.EqualError(t, err, "write failed")
	})
}
