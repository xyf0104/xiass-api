//go:build integration

package repository

import (
	"context"
	"encoding/json"
	"math"
	"testing"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

// Payload fields below are opaque CAS fixtures, not a weekly reducer schema.
func weeklyStatePGExtra() map[string]any {
	return map[string]any{
		openAIWeeklyStateEpochKey:        "test-epoch",
		openAIWeeklyStateBaselineKey:     map[string]any{"test_maximum": 10.0},
		openAIWeeklyStateUsageUpdatedKey: "2026-09-06T01:02:03.123456789Z",
	}
}

func weeklyStatePGAccount(t *testing.T, credentials, extra map[string]any) *service.Account {
	t.Helper()
	credentialJSON, err := json.Marshal(credentials)
	require.NoError(t, err)
	// The existing OpenAI billing trigger requires an object container. Preserve
	// nil in the caller snapshot to test missing-key semantics, not invalid rows.
	persistedExtra := extra
	if persistedExtra == nil {
		persistedExtra = map[string]any{}
	}
	extraJSON, err := json.Marshal(persistedExtra)
	require.NoError(t, err)
	a := &service.Account{
		Name: "weekly-state-cas-test", Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth,
		Credentials: credentials, Extra: extra,
	}
	require.NoError(t, integrationDB.QueryRow(`INSERT INTO accounts (name, platform, type, credentials, extra)
		VALUES ($1, $2, $3, $4::jsonb, $5::jsonb) RETURNING id, created_at, updated_at`,
		a.Name, a.Platform, a.Type, string(credentialJSON), string(extraJSON)).Scan(&a.ID, &a.CreatedAt, &a.UpdatedAt))
	return a
}

func weeklyStatePGRecord(t *testing.T, id int64) map[string]any {
	t.Helper()
	var encoded []byte
	require.NoError(t, integrationDB.QueryRow(`SELECT to_jsonb(a) FROM accounts a WHERE id = $1`, id).Scan(&encoded))
	var row map[string]any
	require.NoError(t, json.Unmarshal(encoded, &row))
	return row
}

func weeklyStatePGRepo() service.OpenAIWeeklyStateRepository {
	return NewAccountRepository(integrationEntClient, integrationDB, &weeklyStateForbiddenScheduler{}).(service.OpenAIWeeklyStateRepository)
}

func TestOpenAIWeeklyStateCASPGMergesOnlyStateWithoutSideEffects(t *testing.T) {
	ctx := context.Background()
	extra := weeklyStatePGExtra()
	extra["xiass_execution_node_id"] = "test-owner-node"
	extra["codex_fingerprint_seed"] = "d4e2adab-8dd6-4af5-b2cb-6d74a68d781e"
	extra["codex_fingerprint_mode"] = "device"
	extra["unrelated"] = map[string]any{"nested": "keep"}
	a := weeklyStatePGAccount(t, map[string]any{"refresh_token": "synthetic-original", "_token_version": 5}, extra)
	// An unrelated concurrent Extra change is intentionally not a CAS conflict.
	_, err := integrationDB.Exec(`UPDATE accounts SET extra = extra || '{"concurrent_unrelated":true}'::jsonb WHERE id = $1`, a.ID)
	require.NoError(t, err)
	before := weeklyStatePGRecord(t, a.ID)
	var beforeOutbox int
	require.NoError(t, integrationDB.QueryRow(`SELECT count(*) FROM scheduler_outbox WHERE account_id = $1`, a.ID).Scan(&beforeOutbox))
	patch := map[string]any{openAIWeeklyStateBaselineKey: map[string]any{"test_maximum": 20.0, "opaque": []any{true, nil}}}
	applied, err := weeklyStatePGRepo().CompareAndSwapOpenAIWeeklyState(ctx, a, patch)
	require.NoError(t, err)
	require.True(t, applied)
	after := weeklyStatePGRecord(t, a.ID)
	newExtra := after["extra"].(map[string]any)
	oldExtra := before["extra"].(map[string]any)
	oldExtra[openAIWeeklyStateBaselineKey] = patch[openAIWeeklyStateBaselineKey]
	require.Equal(t, oldExtra, newExtra)
	delete(before, "extra")
	delete(after, "extra")
	require.Equal(t, before, after, "all non-Extra columns, including credentials and updated_at, remain untouched")
	var afterOutbox int
	require.NoError(t, integrationDB.QueryRow(`SELECT count(*) FROM scheduler_outbox WHERE account_id = $1`, a.ID).Scan(&afterOutbox))
	require.Equal(t, beforeOutbox, afterOutbox)
	require.Equal(t, 10.0, a.Extra[openAIWeeklyStateBaselineKey].(map[string]any)["test_maximum"], "caller snapshot is immutable")
}

func TestOpenAIWeeklyStateCASPGCompetingWritersOneWins(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	a := weeklyStatePGAccount(t, map[string]any{"refresh_token": "synthetic-race"}, weeklyStatePGExtra())
	type outcome struct {
		applied bool
		err     error
	}
	const writers = 24
	start := make(chan struct{})
	results := make(chan outcome, writers)
	for i := 0; i < writers; i++ {
		go func(maximum int) {
			<-start
			applied, err := weeklyStatePGRepo().CompareAndSwapOpenAIWeeklyState(ctx, a,
				map[string]any{openAIWeeklyStateBaselineKey: map[string]any{"test_maximum": maximum}})
			results <- outcome{applied: applied, err: err}
		}(20 + i)
	}
	close(start)
	wins := 0
	for i := 0; i < writers; i++ {
		result := <-results
		require.NoError(t, result.err)
		if result.applied {
			wins++
		}
	}
	require.Equal(t, 1, wins)
	record := weeklyStatePGRecord(t, a.ID)
	winner := record["extra"].(map[string]any)[openAIWeeklyStateBaselineKey].(map[string]any)["test_maximum"].(float64)
	require.GreaterOrEqual(t, winner, 20.0)
	require.Less(t, winner, 20.0+writers)
}

func TestOpenAIWeeklyStateCASPGStaleLowerMaximumWaitsThenLoses(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	a := weeklyStatePGAccount(t, map[string]any{}, weeklyStatePGExtra())
	tx, err := integrationEntClient.Tx(ctx)
	require.NoError(t, err)
	defer tx.Rollback()
	applied, err := weeklyStatePGRepo().CompareAndSwapOpenAIWeeklyState(dbent.NewTxContext(ctx, tx), a,
		map[string]any{openAIWeeklyStateBaselineKey: map[string]any{"test_maximum": 200.0}})
	require.NoError(t, err)
	require.True(t, applied)
	type outcome struct {
		applied bool
		err     error
	}
	started := make(chan struct{})
	done := make(chan outcome, 1)
	go func() {
		close(started)
		applied, err := weeklyStatePGRepo().CompareAndSwapOpenAIWeeklyState(ctx, a,
			map[string]any{openAIWeeklyStateBaselineKey: map[string]any{"test_maximum": 100.0}})
		done <- outcome{applied: applied, err: err}
	}()
	<-started
	select {
	case result := <-done:
		t.Fatalf("competing update escaped the row lock: applied=%v err=%v", result.applied, result.err)
	case <-time.After(75 * time.Millisecond):
	}
	require.NoError(t, tx.Commit())
	result := <-done
	require.NoError(t, result.err)
	require.False(t, result.applied)
	require.Equal(t, 200.0, weeklyStatePGRecord(t, a.ID)["extra"].(map[string]any)[openAIWeeklyStateBaselineKey].(map[string]any)["test_maximum"])
}

func TestOpenAIWeeklyStateCASPGIdentityEpochAndRawSnapshotGuards(t *testing.T) {
	proxy := mustCreateProxy(t, testEntClient(t), &service.Proxy{Name: "weekly-state-cas-proxy"})
	cleanupOpenAIQuotaTestProxy(t, proxy.ID)
	for _, tc := range []struct {
		name  string
		query string
	}{
		{"new epoch", `UPDATE accounts SET extra = extra || '{"codex_7d_estimate_epoch":"new-epoch"}'::jsonb WHERE id = $1`},
		{"new baseline", `UPDATE accounts SET extra = extra || '{"codex_7d_estimate_baseline":{"test_maximum":300}}'::jsonb WHERE id = $1`},
		{"reauthorized token", `UPDATE accounts SET credentials = credentials || '{"refresh_token":"synthetic-new"}'::jsonb WHERE id = $1`},
		{"credentials version", `UPDATE accounts SET credentials = credentials || '{"_token_version":2}'::jsonb WHERE id = $1`},
		{"credential null presence", `UPDATE accounts SET credentials = credentials || '{"new_field":null}'::jsonb WHERE id = $1`},
		{"proxy added", `UPDATE accounts SET proxy_id = $2 WHERE id = $1`},
		{"new raw snapshot", `UPDATE accounts SET extra = extra || '{"codex_usage_updated_at":"2026-09-06T01:02:04Z"}'::jsonb WHERE id = $1`},
		{"same instant different raw text", `UPDATE accounts SET extra = extra || '{"codex_usage_updated_at":"2026-09-06T01:02:03.123456789+00:00"}'::jsonb WHERE id = $1`},
		{"platform changed", `UPDATE accounts SET platform = 'anthropic' WHERE id = $1`},
		{"type changed", `UPDATE accounts SET type = 'apikey' WHERE id = $1`},
		{"deleted", `UPDATE accounts SET deleted_at = NOW() WHERE id = $1`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := weeklyStatePGAccount(t, map[string]any{"refresh_token": "synthetic-old"}, weeklyStatePGExtra())
			args := []any{a.ID}
			if tc.name == "proxy added" {
				args = append(args, proxy.ID)
			}
			_, err := integrationDB.Exec(tc.query, args...)
			require.NoError(t, err)
			before := weeklyStatePGRecord(t, a.ID)
			applied, err := weeklyStatePGRepo().CompareAndSwapOpenAIWeeklyState(context.Background(), a,
				map[string]any{openAIWeeklyStateBaselineKey: map[string]any{"test_maximum": 99}})
			require.NoError(t, err)
			require.False(t, applied)
			require.Equal(t, before, weeklyStatePGRecord(t, a.ID))
		})
	}
	a := weeklyStatePGAccount(t, map[string]any{}, weeklyStatePGExtra())
	a.ProxyID = &proxy.ID // A former non-null proxy must not match today's SQL NULL.
	applied, err := weeklyStatePGRepo().CompareAndSwapOpenAIWeeklyState(context.Background(), a, map[string]any{openAIWeeklyStateEpochKey: nil})
	require.NoError(t, err)
	require.False(t, applied)
	a.ProxyID = nil
	a.ID += 9000000000000000
	applied, err = weeklyStatePGRepo().CompareAndSwapOpenAIWeeklyState(context.Background(), a, map[string]any{openAIWeeklyStateEpochKey: nil})
	require.NoError(t, err)
	require.False(t, applied, "positive but absent ID is stale, not a create")
}

func TestOpenAIWeeklyStateCASPGMissingVersusJSONNull(t *testing.T) {
	for _, key := range []string{openAIWeeklyStateEpochKey, openAIWeeklyStateBaselineKey, openAIWeeklyStateUsageUpdatedKey} {
		for _, actual := range []string{"missing", "null", "value"} {
			for _, expected := range []string{"missing", "null", "value"} {
				t.Run(key+"/"+actual+"/"+expected, func(t *testing.T) {
					extra := weeklyStatePGExtra()
					set := func(m map[string]any, state string) {
						delete(m, key)
						if state == "null" {
							m[key] = nil
						} else if state == "value" {
							m[key] = "opaque-fixture-value"
						}
					}
					set(extra, actual)
					a := weeklyStatePGAccount(t, map[string]any{}, extra)
					a.Extra = weeklyStatePGExtra()
					set(a.Extra, expected)
					applied, err := weeklyStatePGRepo().CompareAndSwapOpenAIWeeklyState(context.Background(), a,
						map[string]any{openAIWeeklyStateEpochKey: "next-test-epoch", openAIWeeklyStateBaselineKey: nil})
					require.NoError(t, err)
					require.Equal(t, actual == expected, applied)
				})
			}
		}
	}
}

func TestOpenAIWeeklyStateCASPGPlainNilNullAndJSONBEquality(t *testing.T) {
	ctx := context.Background()
	for _, extra := range []map[string]any{nil, {}} {
		a := weeklyStatePGAccount(t, nil, extra)
		// Nil and empty caller maps have no tracked keys. Credentials null is NOT {}.
		wrong := *a
		wrong.Credentials = map[string]any{}
		applied, err := weeklyStatePGRepo().CompareAndSwapOpenAIWeeklyState(ctx, &wrong, map[string]any{openAIWeeklyStateEpochKey: nil})
		require.NoError(t, err)
		require.False(t, applied)
		applied, err = weeklyStatePGRepo().CompareAndSwapOpenAIWeeklyState(ctx, a,
			map[string]any{openAIWeeklyStateEpochKey: json.RawMessage(`null`), openAIWeeklyStateBaselineKey: (*map[string]any)(nil)})
		require.NoError(t, err)
		require.True(t, applied)
		stored := weeklyStatePGRecord(t, a.ID)["extra"].(map[string]any)
		require.Contains(t, stored, openAIWeeklyStateEpochKey)
		require.Contains(t, stored, openAIWeeklyStateBaselineKey)
		require.Nil(t, stored[openAIWeeklyStateEpochKey])
		require.Nil(t, stored[openAIWeeklyStateBaselineKey])
		applied, err = weeklyStatePGRepo().CompareAndSwapOpenAIWeeklyState(ctx, a, map[string]any{openAIWeeklyStateEpochKey: "next"})
		require.NoError(t, err)
		require.False(t, applied, "original missing keys no longer match explicit nulls")
	}
	a := weeklyStatePGAccount(t, map[string]any{"nested": json.RawMessage(`{"z":2.000, "a":1}`)},
		map[string]any{openAIWeeklyStateEpochKey: "test-epoch", openAIWeeklyStateBaselineKey: json.RawMessage(`{"z":[2,3.0],"a":1}`)})
	a.Credentials = map[string]any{"nested": json.RawMessage(`{ "a":1.0,"z":2 }`)}
	a.Extra[openAIWeeklyStateBaselineKey] = json.RawMessage(`{ "a":1.00, "z":[2.0,3] }`)
	applied, err := weeklyStatePGRepo().CompareAndSwapOpenAIWeeklyState(ctx, a, map[string]any{openAIWeeklyStateEpochKey: "next-test-epoch"})
	require.NoError(t, err)
	require.True(t, applied, "JSONB compares structure and numeric values, not serialized key order or number spelling")
}

func TestOpenAIWeeklyStateCASPGRejectsMalformedPatchesAndWrongAccounts(t *testing.T) {
	ctx := context.Background()
	a := weeklyStatePGAccount(t, map[string]any{}, weeklyStatePGExtra())
	before := weeklyStatePGRecord(t, a.ID)
	for _, updates := range []map[string]any{nil, {},
		{openAIWeeklyStateBaselineKey: json.RawMessage(`not-json`)},
		{openAIWeeklyStateEpochKey: math.NaN()},
		{openAIWeeklyStateEpochKey: "new", "credentials": map[string]any{"access_token": "forbidden"}},
		{"xiass_execution_node_id": "forbidden"},
		{"codex_fingerprint_seed": "forbidden"},
		{openAIWeeklyStateUsageUpdatedKey: "forbidden"},
	} {
		applied, err := weeklyStatePGRepo().CompareAndSwapOpenAIWeeklyState(ctx, a, updates)
		require.False(t, applied)
		require.ErrorIs(t, err, service.ErrOpenAIWeeklyStateInvalidInput)
		require.Equal(t, before, weeklyStatePGRecord(t, a.ID))
	}
	for _, identity := range [][2]string{{service.PlatformAnthropic, service.AccountTypeOAuth}, {service.PlatformOpenAI, service.AccountTypeAPIKey}, {service.PlatformOpenAI, "setup-token"}} {
		wrong := *a
		wrong.Platform, wrong.Type = identity[0], identity[1]
		applied, err := weeklyStatePGRepo().CompareAndSwapOpenAIWeeklyState(ctx, &wrong, map[string]any{openAIWeeklyStateEpochKey: nil})
		require.False(t, applied)
		require.ErrorIs(t, err, service.ErrOpenAIWeeklyStateInvalidInput)
	}
	// NUL is JSON-encodable but PostgreSQL JSONB rejects it. This is a real
	// database failure, not stale-state success or a swallowed validation error.
	applied, err := weeklyStatePGRepo().CompareAndSwapOpenAIWeeklyState(ctx, a, map[string]any{openAIWeeklyStateEpochKey: "\x00"})
	require.False(t, applied)
	require.Error(t, err)
	require.NotErrorIs(t, err, service.ErrOpenAIWeeklyStateInvalidInput)
	require.Equal(t, before, weeklyStatePGRecord(t, a.ID))
}

func TestOpenAIWeeklyStateCASPGEntTransactionRollbackAndCommit(t *testing.T) {
	ctx := context.Background()
	for _, commit := range []bool{false, true} {
		a := weeklyStatePGAccount(t, map[string]any{}, weeklyStatePGExtra())
		before := weeklyStatePGRecord(t, a.ID)
		tx, err := integrationEntClient.Tx(ctx)
		require.NoError(t, err)
		t.Cleanup(func() { _ = tx.Rollback() })
		txCtx := dbent.NewTxContext(ctx, tx)
		repo := newAccountRepositoryWithSQL(nil, nil, &weeklyStateForbiddenScheduler{})
		applied, err := repo.CompareAndSwapOpenAIWeeklyState(txCtx, a, map[string]any{openAIWeeklyStateEpochKey: "transaction-epoch"})
		require.NoError(t, err)
		require.True(t, applied)
		var inside string
		require.NoError(t, scanSingleRow(txCtx, tx.Client(), `SELECT extra ->> 'codex_7d_estimate_epoch' FROM accounts WHERE id = $1`, []any{a.ID}, &inside))
		require.Equal(t, "transaction-epoch", inside)
		require.Equal(t, before, weeklyStatePGRecord(t, a.ID), "no premature commit or base-connection write")
		if commit {
			require.NoError(t, tx.Commit())
			require.Equal(t, "transaction-epoch", weeklyStatePGRecord(t, a.ID)["extra"].(map[string]any)[openAIWeeklyStateEpochKey])
		} else {
			require.NoError(t, tx.Rollback())
			require.Equal(t, before, weeklyStatePGRecord(t, a.ID))
		}
	}
}
