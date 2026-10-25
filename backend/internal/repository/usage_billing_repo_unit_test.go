//go:build unit

package repository

import (
	"context"
	"database/sql"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

func atomicUsageBillingFixture() (*service.UsageBillingCommand, *service.UsageLog) {
	createdAt := time.Now().UTC().Truncate(time.Microsecond)
	cmd := &service.UsageBillingCommand{
		RequestID:    "local:atomic-billing",
		APIKeyID:     9,
		UserID:       42,
		AccountID:    77,
		Model:        "gpt-5.6-luna",
		BillingType:  service.BillingTypeBalance,
		InputTokens:  120,
		OutputTokens: 30,
		BalanceCost:  1.25,
	}
	cmd.Normalize()
	log := &service.UsageLog{
		UserID:       cmd.UserID,
		APIKeyID:     cmd.APIKeyID,
		AccountID:    cmd.AccountID,
		RequestID:    cmd.RequestID,
		Model:        cmd.Model,
		InputTokens:  cmd.InputTokens,
		OutputTokens: cmd.OutputTokens,
		TotalCost:    1.0,
		ActualCost:   cmd.BalanceCost,
		BillingType:  cmd.BillingType,
		CreatedAt:    createdAt,
	}
	return cmd, log
}

func expectNewUsageBillingClaim(mock sqlmock.Sqlmock) {
	mock.ExpectQuery("INSERT INTO usage_billing_dedup").
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(1))
	mock.ExpectQuery("SELECT request_fingerprint\\s+FROM usage_billing_dedup_archive").
		WillReturnError(sql.ErrNoRows)
}

func expectExistingUsageBillingClaim(mock sqlmock.Sqlmock, fingerprint string) {
	mock.ExpectQuery("INSERT INTO usage_billing_dedup").WillReturnError(sql.ErrNoRows)
	mock.ExpectQuery("SELECT request_fingerprint\\s+FROM usage_billing_dedup").
		WillReturnRows(sqlmock.NewRows([]string{"request_fingerprint"}).AddRow(fingerprint))
}

func TestUsageBillingApplyWithUsageLog_CommitsEffectsAndLogTogether(t *testing.T) {
	ctx := context.Background()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	cmd, log := atomicUsageBillingFixture()
	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL synchronous_commit = on").WillReturnResult(sqlmock.NewResult(0, 0))
	expectNewUsageBillingClaim(mock)
	mock.ExpectQuery(conditionalBalanceDeductSQL).
		WithArgs(cmd.BalanceCost, cmd.UserID).
		WillReturnRows(sqlmock.NewRows([]string{"balance"}).AddRow(98.75))
	mock.ExpectQuery("INSERT INTO usage_logs").
		WillReturnRows(sqlmock.NewRows([]string{"id", "created_at"}).AddRow(101, log.CreatedAt))
	mock.ExpectCommit()

	result, err := (&usageBillingRepository{db: db}).ApplyWithUsageLog(ctx, cmd, log)
	require.NoError(t, err)
	require.True(t, result.Applied)
	require.Equal(t, int64(101), log.ID)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestUsageBillingApplyWithUsageLog_LogConflictRollsBackBilling(t *testing.T) {
	ctx := context.Background()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	cmd, log := atomicUsageBillingFixture()
	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL synchronous_commit = on").WillReturnResult(sqlmock.NewResult(0, 0))
	expectNewUsageBillingClaim(mock)
	mock.ExpectQuery(conditionalBalanceDeductSQL).
		WithArgs(cmd.BalanceCost, cmd.UserID).
		WillReturnRows(sqlmock.NewRows([]string{"balance"}).AddRow(98.75))
	mock.ExpectQuery("INSERT INTO usage_logs").WillReturnError(sql.ErrNoRows)
	mock.ExpectQuery("SELECT id, created_at FROM usage_logs WHERE request_id").
		WillReturnRows(sqlmock.NewRows([]string{"id", "created_at"}).AddRow(88, log.CreatedAt.Add(-time.Minute)))
	mock.ExpectRollback()

	_, err = (&usageBillingRepository{db: db}).ApplyWithUsageLog(ctx, cmd, log)
	require.ErrorIs(t, err, service.ErrUsageBillingUsageLogConflict)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestUsageBillingApplyWithUsageLog_LostCommitRetryVerifiesExactLog(t *testing.T) {
	ctx := context.Background()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	cmd, log := atomicUsageBillingFixture()
	durationMs := 987
	firstTokenMs := 321
	userAgent := "retry-agent"
	ipAddress := "127.0.0.2"
	log.DurationMs = &durationMs
	log.FirstTokenMs = &firstTokenMs
	log.UserAgent = &userAgent
	log.IPAddress = &ipAddress
	log.CreatedAt = log.CreatedAt.Add(5 * time.Second)
	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL synchronous_commit = on").WillReturnResult(sqlmock.NewResult(0, 0))
	expectExistingUsageBillingClaim(mock, cmd.RequestFingerprint)
	mock.ExpectQuery("SELECT id, created_at FROM usage_logs WHERE user_id IS NOT DISTINCT FROM").
		WillReturnRows(sqlmock.NewRows([]string{"id", "created_at"}).AddRow(101, log.CreatedAt))
	mock.ExpectCommit()

	result, err := (&usageBillingRepository{db: db}).ApplyWithUsageLog(ctx, cmd, log)
	require.NoError(t, err)
	require.False(t, result.Applied)
	require.Equal(t, int64(101), log.ID)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestUsageBillingLogMatchColumns_ExcludeRetryMetadata(t *testing.T) {
	for _, column := range []string{"actual_cost", "total_cost", "billing_type", "input_tokens", "image_output_cost", "video_duration_seconds"} {
		_, ok := usageBillingLogMatchColumns[column]
		require.True(t, ok, "billing field %s must be verified", column)
	}
	for _, column := range []string{"created_at", "duration_ms", "first_token_ms", "user_agent", "ip_address", "upstream_request_id", "session_id"} {
		_, ok := usageBillingLogMatchColumns[column]
		require.False(t, ok, "retry metadata %s must not cause a billing conflict", column)
	}
}

func TestUsageBillingApplyWithUsageLog_OldZeroCostLogConflicts(t *testing.T) {
	ctx := context.Background()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	cmd, log := atomicUsageBillingFixture()
	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL synchronous_commit = on").WillReturnResult(sqlmock.NewResult(0, 0))
	expectExistingUsageBillingClaim(mock, cmd.RequestFingerprint)
	mock.ExpectQuery("SELECT id, created_at FROM usage_logs WHERE user_id IS NOT DISTINCT FROM").WillReturnError(sql.ErrNoRows)
	mock.ExpectQuery("SELECT id FROM usage_logs WHERE request_id").
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(88))
	mock.ExpectRollback()

	_, err = (&usageBillingRepository{db: db}).ApplyWithUsageLog(ctx, cmd, log)
	require.ErrorIs(t, err, service.ErrUsageBillingUsageLogConflict)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestUsageBillingApplyWithUsageLog_AppliedKeyWithoutLogFailsClosed(t *testing.T) {
	ctx := context.Background()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	cmd, log := atomicUsageBillingFixture()
	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL synchronous_commit = on").WillReturnResult(sqlmock.NewResult(0, 0))
	expectExistingUsageBillingClaim(mock, cmd.RequestFingerprint)
	mock.ExpectQuery("SELECT id, created_at FROM usage_logs WHERE user_id IS NOT DISTINCT FROM").WillReturnError(sql.ErrNoRows)
	mock.ExpectQuery("SELECT id FROM usage_logs WHERE request_id").WillReturnError(sql.ErrNoRows)
	mock.ExpectRollback()

	_, err = (&usageBillingRepository{db: db}).ApplyWithUsageLog(ctx, cmd, log)
	require.ErrorIs(t, err, service.ErrUsageBillingUsageLogMissing)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestUsageBillingApplyWithUsageLog_LostCommitAckRetryFindsCommittedLog(t *testing.T) {
	ctx := context.Background()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	cmd, log := atomicUsageBillingFixture()
	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL synchronous_commit = on").WillReturnResult(sqlmock.NewResult(0, 0))
	expectNewUsageBillingClaim(mock)
	mock.ExpectQuery(conditionalBalanceDeductSQL).
		WithArgs(cmd.BalanceCost, cmd.UserID).
		WillReturnRows(sqlmock.NewRows([]string{"balance"}).AddRow(98.75))
	mock.ExpectQuery("INSERT INTO usage_logs").
		WillReturnRows(sqlmock.NewRows([]string{"id", "created_at"}).AddRow(101, log.CreatedAt))
	mock.ExpectCommit().WillReturnError(io.ErrUnexpectedEOF)

	_, err = (&usageBillingRepository{db: db}).ApplyWithUsageLog(ctx, cmd, log)
	require.ErrorIs(t, err, io.ErrUnexpectedEOF)
	require.Zero(t, log.ID, "an unconfirmed commit must not publish the transactional log ID")

	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL synchronous_commit = on").WillReturnResult(sqlmock.NewResult(0, 0))
	expectExistingUsageBillingClaim(mock, cmd.RequestFingerprint)
	mock.ExpectQuery("SELECT id, created_at FROM usage_logs WHERE user_id IS NOT DISTINCT FROM").
		WillReturnRows(sqlmock.NewRows([]string{"id", "created_at"}).AddRow(101, log.CreatedAt))
	mock.ExpectCommit()

	result, err := (&usageBillingRepository{db: db}).ApplyWithUsageLog(ctx, cmd, log)
	require.NoError(t, err)
	require.False(t, result.Applied)
	require.Equal(t, int64(101), log.ID)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestUsageBillingApplyWithUsageLog_RolledBackCommitRetryReapplies(t *testing.T) {
	ctx := context.Background()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	cmd, log := atomicUsageBillingFixture()
	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL synchronous_commit = on").WillReturnResult(sqlmock.NewResult(0, 0))
	expectNewUsageBillingClaim(mock)
	mock.ExpectQuery(conditionalBalanceDeductSQL).
		WithArgs(cmd.BalanceCost, cmd.UserID).
		WillReturnRows(sqlmock.NewRows([]string{"balance"}).AddRow(98.75))
	mock.ExpectQuery("INSERT INTO usage_logs").
		WillReturnRows(sqlmock.NewRows([]string{"id", "created_at"}).AddRow(101, log.CreatedAt))
	mock.ExpectCommit().WillReturnError(io.ErrUnexpectedEOF)

	_, err = (&usageBillingRepository{db: db}).ApplyWithUsageLog(ctx, cmd, log)
	require.ErrorIs(t, err, io.ErrUnexpectedEOF)
	require.Zero(t, log.ID)

	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL synchronous_commit = on").WillReturnResult(sqlmock.NewResult(0, 0))
	expectNewUsageBillingClaim(mock)
	mock.ExpectQuery(conditionalBalanceDeductSQL).
		WithArgs(cmd.BalanceCost, cmd.UserID).
		WillReturnRows(sqlmock.NewRows([]string{"balance"}).AddRow(98.75))
	mock.ExpectQuery("INSERT INTO usage_logs").
		WillReturnRows(sqlmock.NewRows([]string{"id", "created_at"}).AddRow(202, log.CreatedAt))
	mock.ExpectCommit()

	result, err := (&usageBillingRepository{db: db}).ApplyWithUsageLog(ctx, cmd, log)
	require.NoError(t, err)
	require.True(t, result.Applied)
	require.Equal(t, int64(202), log.ID)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestValidateUsageBillingUsageLog_AllowsNumeric10PricingAgainstQuantized8Billing(t *testing.T) {
	subscriptionID := int64(11)
	accountMultiplier := 1.5
	tests := []struct {
		name string
		cmd  service.UsageBillingCommand
		log  service.UsageLog
	}{
		{
			name: "fine grained balance charge",
			cmd: service.UsageBillingCommand{
				RequestID: "local:fine", APIKeyID: 9, UserID: 42, AccountID: 77,
				Model: "gpt-5.6-luna", BalanceCost: 0.000078125,
			},
			log: service.UsageLog{
				RequestID: "local:fine", APIKeyID: 9, UserID: 42, AccountID: 77,
				Model: "gpt-5.6-luna", ActualCost: 0.000078125,
			},
		},
		{
			name: "custom discounted price",
			cmd: service.UsageBillingCommand{
				RequestID: "local:discount", APIKeyID: 9, UserID: 42, AccountID: 77,
				Model: "claude-sonnet-4-5", BalanceCost: 0.12345678905,
			},
			log: service.UsageLog{
				RequestID: "local:discount", APIKeyID: 9, UserID: 42, AccountID: 77,
				Model: "claude-sonnet-4-5", TotalCost: 0.2469135781, ActualCost: 0.12345678905,
			},
		},
		{
			name: "zero multiplier subscription",
			cmd: service.UsageBillingCommand{
				RequestID: "local:free-sub", APIKeyID: 9, UserID: 42, AccountID: 77,
				SubscriptionID: &subscriptionID, Model: "claude-sonnet-4-5", BillingType: service.BillingTypeSubscription,
			},
			log: service.UsageLog{
				RequestID: "local:free-sub", APIKeyID: 9, UserID: 42, AccountID: 77,
				SubscriptionID: &subscriptionID, Model: "claude-sonnet-4-5", BillingType: service.BillingTypeSubscription,
				TotalCost: 1.2345678901, ActualCost: 0, RateMultiplier: 0,
			},
		},
		{
			name: "image pricing",
			cmd: service.UsageBillingCommand{
				RequestID: "local:image", APIKeyID: 9, UserID: 42, AccountID: 77,
				Model: "gpt-image-1", ImageCount: 2, BalanceCost: 0.30150000004, AccountQuotaCost: 0.30150000005,
			},
			log: service.UsageLog{
				RequestID: "local:image", APIKeyID: 9, UserID: 42, AccountID: 77,
				Model: "gpt-image-1", ImageCount: 2, TotalCost: 0.20100000003, ActualCost: 0.30150000004,
				AccountRateMultiplier: &accountMultiplier,
			},
		},
		{
			name: "video pricing",
			cmd: service.UsageBillingCommand{
				RequestID: "local:video", APIKeyID: 9, UserID: 42, AccountID: 77,
				Model: "grok-imagine-video", BalanceCost: 0.80400000004, AccountQuotaCost: 0.80400000005,
			},
			log: service.UsageLog{
				RequestID: "local:video", APIKeyID: 9, UserID: 42, AccountID: 77,
				Model: "grok-imagine-video", VideoCount: 1, TotalCost: 0.53600000003, ActualCost: 0.80400000004,
				AccountRateMultiplier: &accountMultiplier,
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tc.cmd.Normalize()
			require.NoError(t, validateUsageBillingUsageLog(&tc.cmd, &tc.log))
		})
	}
}

func TestUsageBillingApply_RecoveryClassificationIsScopedToBegin(t *testing.T) {
	for _, tc := range []struct {
		name, code         string
		atBegin, wantRetry bool
	}{
		{"recovery rejects begin", "0A000", true, true},
		{"unsupported query", "0A000", false, false},
		{"invalid credentials", "28P01", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer db.Close()
			failure := &pq.Error{Code: pq.ErrorCode(tc.code)}
			if tc.atBegin {
				mock.ExpectBegin().WillReturnError(failure)
			} else {
				mock.ExpectBegin()
				mock.ExpectExec("SET LOCAL synchronous_commit = on").WillReturnResult(sqlmock.NewResult(0, 0))
				mock.ExpectQuery("INSERT INTO usage_billing_dedup").WillReturnError(failure)
				mock.ExpectRollback()
			}
			_, err = (&usageBillingRepository{db: db}).Apply(context.Background(), &service.UsageBillingCommand{RequestID: "fixed"})
			require.ErrorIs(t, err, failure)
			if tc.wantRetry {
				require.ErrorIs(t, err, service.ErrUsageBillingPrimaryUnavailable)
			} else {
				require.NotErrorIs(t, err, service.ErrUsageBillingPrimaryUnavailable)
			}
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

const (
	conditionalBalanceDeductSQL = `(?s)UPDATE users\s+SET balance = balance - \$1,\s+updated_at = NOW\(\)\s+WHERE id = \$2 AND deleted_at IS NULL AND balance >= \$1\s+RETURNING balance`
	overdraftBalanceDeductSQL   = `(?s)UPDATE users\s+SET balance = balance - \$1,\s+updated_at = NOW\(\)\s+WHERE id = \$2 AND deleted_at IS NULL\s+RETURNING balance`
	reserveBatchImageHoldSQL    = `(?s)UPDATE users\s+SET balance = balance - \$1,\s+frozen_balance = COALESCE\(frozen_balance, 0\) \+ \$1,\s+updated_at = NOW\(\)\s+WHERE id = \$2 AND deleted_at IS NULL AND balance >= \$1\s+RETURNING balance, frozen_balance`
	captureBatchImageHoldSQL    = `(?s)UPDATE users\s+SET balance = balance\s+\+ CASE WHEN \$1 > \$2 THEN \$1 - \$2 ELSE 0 END\s+- CASE WHEN \$2 > \$1 THEN \$2 - \$1 ELSE 0 END,\s+frozen_balance = COALESCE\(frozen_balance, 0\) - \$1,\s+updated_at = NOW\(\)\s+WHERE id = \$3 AND deleted_at IS NULL AND COALESCE\(frozen_balance, 0\) >= \$1\s+RETURNING balance, frozen_balance`
	releaseBatchImageHoldSQL    = `(?s)UPDATE users\s+SET balance = balance \+ \$1,\s+frozen_balance = COALESCE\(frozen_balance, 0\) - \$1,\s+updated_at = NOW\(\)\s+WHERE id = \$2 AND deleted_at IS NULL AND COALESCE\(frozen_balance, 0\) >= \$1\s+RETURNING balance, frozen_balance`
	userExistsForBillingSQL     = `(?s)SELECT 1\s+FROM users\s+WHERE id = \$1 AND deleted_at IS NULL`
	apiKeyQuotaIncrementSQL     = `(?s)UPDATE api_keys\s+SET quota_used = quota_used \+ \$1,.*WHERE id = \$2 AND deleted_at IS NULL\s+RETURNING`
	apiKeyRateLimitIncrementSQL = `(?s)UPDATE api_keys SET\s+usage_5h = .*WHERE id = \$2 AND deleted_at IS NULL`
	accountQuotaIncrementSQL    = `(?s)UPDATE accounts SET extra = .*WHERE id = \$2 AND deleted_at IS NULL.*RETURNING`
)

func TestDeductUsageBillingBalance_UsesSufficientBalanceGuard(t *testing.T) {
	ctx := context.Background()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	mock.ExpectBegin()
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	mock.ExpectQuery(conditionalBalanceDeductSQL).
		WithArgs(2.5, int64(42)).
		WillReturnRows(sqlmock.NewRows([]string{"balance"}).AddRow(7.5))
	mock.ExpectCommit()

	newBalance, sufficient, err := deductUsageBillingBalance(ctx, tx, 42, 2.5)
	require.NoError(t, err)
	require.True(t, sufficient)
	require.InDelta(t, 7.5, newBalance, 0.000001)
	require.NoError(t, tx.Commit())
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestDeductUsageBillingBalance_RecordsOverdraftWhenGuardMisses(t *testing.T) {
	ctx := context.Background()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	mock.ExpectBegin()
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	mock.ExpectQuery(conditionalBalanceDeductSQL).
		WithArgs(10.0, int64(42)).
		WillReturnError(sql.ErrNoRows)
	mock.ExpectQuery(overdraftBalanceDeductSQL).
		WithArgs(10.0, int64(42)).
		WillReturnRows(sqlmock.NewRows([]string{"balance"}).AddRow(-5.0))
	mock.ExpectCommit()

	newBalance, sufficient, err := deductUsageBillingBalance(ctx, tx, 42, 10)
	require.NoError(t, err)
	require.False(t, sufficient)
	require.InDelta(t, -5.0, newBalance, 0.000001)
	require.NoError(t, tx.Commit())
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestApplyUsageBillingEffects_FlagsBalanceOverdraft(t *testing.T) {
	ctx := context.Background()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	mock.ExpectBegin()
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	mock.ExpectQuery(conditionalBalanceDeductSQL).
		WithArgs(10.0, int64(42)).
		WillReturnError(sql.ErrNoRows)
	mock.ExpectQuery(overdraftBalanceDeductSQL).
		WithArgs(10.0, int64(42)).
		WillReturnRows(sqlmock.NewRows([]string{"balance"}).AddRow(-5.0))
	mock.ExpectCommit()

	result := &service.UsageBillingApplyResult{Applied: true}
	err = (&usageBillingRepository{}).applyUsageBillingEffects(ctx, tx, &service.UsageBillingCommand{
		UserID:      42,
		BalanceCost: 10,
	}, result)
	require.NoError(t, err)
	require.NotNil(t, result.NewBalance)
	require.InDelta(t, -5.0, *result.NewBalance, 0.000001)
	require.True(t, result.BalanceOverdrafted)
	require.NoError(t, tx.Commit())
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestApplyUsageBillingEffects_DeletedAPIKeyStillBillsBalance(t *testing.T) {
	ctx := context.Background()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	mock.ExpectBegin()
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	mock.ExpectQuery(conditionalBalanceDeductSQL).
		WithArgs(10.0, int64(42)).
		WillReturnRows(sqlmock.NewRows([]string{"balance"}).AddRow(90.0))
	mock.ExpectQuery(apiKeyQuotaIncrementSQL).
		WithArgs(10.0, int64(7), service.StatusAPIKeyActive, service.StatusAPIKeyQuotaExhausted).
		WillReturnError(sql.ErrNoRows)
	mock.ExpectExec(apiKeyRateLimitIncrementSQL).
		WithArgs(10.0, int64(7)).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectCommit()

	result := &service.UsageBillingApplyResult{Applied: true}
	err = (&usageBillingRepository{}).applyUsageBillingEffects(ctx, tx, &service.UsageBillingCommand{
		UserID:              42,
		APIKeyID:            7,
		BalanceCost:         10,
		APIKeyQuotaCost:     10,
		APIKeyRateLimitCost: 10,
	}, result)
	require.NoError(t, err)
	require.NotNil(t, result.NewBalance)
	require.InDelta(t, 90.0, *result.NewBalance, 0.000001)
	require.False(t, result.APIKeyQuotaExhausted)
	require.NoError(t, tx.Commit())
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestApplyUsageBillingEffects_DeletedAccountStillBillsBalance(t *testing.T) {
	ctx := context.Background()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	mock.ExpectBegin()
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	mock.ExpectQuery(conditionalBalanceDeductSQL).
		WithArgs(10.0, int64(42)).
		WillReturnRows(sqlmock.NewRows([]string{"balance"}).AddRow(90.0))
	mock.ExpectCommit()

	result := &service.UsageBillingApplyResult{Applied: true}
	err = (&usageBillingRepository{}).applyUsageBillingEffects(ctx, tx, &service.UsageBillingCommand{
		UserID:           42,
		AccountID:        9,
		AccountType:      service.AccountTypeServiceAccount,
		BalanceCost:      10,
		AccountQuotaCost: 4,
	}, result)
	require.NoError(t, err)
	require.NotNil(t, result.NewBalance)
	require.InDelta(t, 90.0, *result.NewBalance, 0.000001)
	require.Nil(t, result.QuotaState)
	require.NoError(t, tx.Commit())
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestApplyUsageBillingEffects_APIKeyCounterErrorStillFails(t *testing.T) {
	ctx := context.Background()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	mock.ExpectBegin()
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	mock.ExpectExec(apiKeyRateLimitIncrementSQL).
		WithArgs(10.0, int64(7)).
		WillReturnError(sql.ErrConnDone)
	mock.ExpectRollback()

	err = (&usageBillingRepository{}).applyUsageBillingEffects(ctx, tx, &service.UsageBillingCommand{
		UserID:              42,
		APIKeyID:            7,
		APIKeyRateLimitCost: 10,
	}, &service.UsageBillingApplyResult{Applied: true})
	require.ErrorIs(t, err, sql.ErrConnDone)
	require.NoError(t, tx.Rollback())
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestDeductUsageBillingBalance_ReturnsUserNotFoundWhenNoUserUpdated(t *testing.T) {
	ctx := context.Background()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	mock.ExpectBegin()
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	mock.ExpectQuery(conditionalBalanceDeductSQL).
		WithArgs(10.0, int64(42)).
		WillReturnError(sql.ErrNoRows)
	mock.ExpectQuery(overdraftBalanceDeductSQL).
		WithArgs(10.0, int64(42)).
		WillReturnError(sql.ErrNoRows)
	mock.ExpectRollback()

	_, _, err = deductUsageBillingBalance(ctx, tx, 42, 10)
	require.ErrorIs(t, err, service.ErrUserNotFound)
	require.NoError(t, tx.Rollback())
	require.NoError(t, mock.ExpectationsWereMet())
}

var accountQuotaStateColumns = []string{"quota_used", "quota_limit", "quota_daily_used", "quota_daily_limit", "quota_weekly_used", "quota_weekly_limit"}

func TestAccountQuotaIncrementSQLs_GuardOmittedDimensions(t *testing.T) {
	for _, daily := range []bool{false, true} {
		for _, weekly := range []bool{false, true} {
			query := accountQuotaIncrementSQLs[accountQuotaIncrementSQLIndex(daily, weekly)]
			set, where, ok := strings.Cut(query, "WHERE ")
			require.True(t, ok)
			require.Equal(t, !daily || !weekly, strings.Contains(where, accountQuotaExtraIsObjectCond), "daily=%v weekly=%v", daily, weekly)
			require.Equal(t, daily, strings.Contains(set, "'quota_daily_used'"), "daily=%v weekly=%v", daily, weekly)
			require.Equal(t, weekly, strings.Contains(set, "'quota_weekly_used'"), "daily=%v weekly=%v", daily, weekly)
			require.Equal(t, !daily, strings.Contains(where, accountQuotaDailyLimitUnsetCond), "daily=%v weekly=%v", daily, weekly)
			require.Equal(t, !weekly, strings.Contains(where, accountQuotaWeeklyLimitUnsetCond), "daily=%v weekly=%v", daily, weekly)
		}
	}
}

func TestIncrementUsageBillingAccountQuota_UsesStatementMatchingHints(t *testing.T) {
	ctx := context.Background()
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	mock.ExpectBegin()
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	mock.ExpectQuery(accountQuotaIncrementSQLs[accountQuotaIncrementSQLIndex(true, false)]).
		WithArgs(2.5, int64(9)).
		WillReturnRows(sqlmock.NewRows(accountQuotaStateColumns).AddRow(12.5, 100.0, 2.5, 50.0, 0.0, 0.0))
	mock.ExpectCommit()

	state, err := incrementUsageBillingAccountQuota(ctx, tx, 9, 2.5, true, false)
	require.NoError(t, err)
	require.NotNil(t, state)
	require.InDelta(t, 12.5, state.TotalUsed, 0.000001)
	require.InDelta(t, 2.5, state.DailyUsed, 0.000001)
	require.NoError(t, tx.Commit())
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestIncrementUsageBillingAccountQuota_FallsBackToFullStatementWhenGuardMisses(t *testing.T) {
	ctx := context.Background()
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	mock.ExpectBegin()
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	mock.ExpectQuery(accountQuotaIncrementSQLs[accountQuotaIncrementSQLIndex(false, false)]).
		WithArgs(2.5, int64(9)).
		WillReturnRows(sqlmock.NewRows(accountQuotaStateColumns))
	mock.ExpectQuery(accountQuotaIncrementSQLs[accountQuotaIncrementSQLIndex(true, true)]).
		WithArgs(2.5, int64(9)).
		WillReturnRows(sqlmock.NewRows(accountQuotaStateColumns).AddRow(2.5, 0.0, 0.0, 0.0, 2.5, 20.0))
	mock.ExpectCommit()

	state, err := incrementUsageBillingAccountQuota(ctx, tx, 9, 2.5, false, false)
	require.NoError(t, err)
	require.NotNil(t, state)
	require.InDelta(t, 2.5, state.WeeklyUsed, 0.000001)
	require.InDelta(t, 20.0, state.WeeklyLimit, 0.000001)
	require.NoError(t, tx.Commit())
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestIncrementUsageBillingAccountQuota_ReturnsNotFoundWhenFullStatementMisses(t *testing.T) {
	for _, hints := range [][2]bool{{false, false}, {true, false}, {false, true}, {true, true}} {
		ctx := context.Background()
		db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
		require.NoError(t, err)

		mock.ExpectBegin()
		tx, err := db.BeginTx(ctx, nil)
		require.NoError(t, err)
		mock.ExpectQuery(accountQuotaIncrementSQLs[accountQuotaIncrementSQLIndex(hints[0], hints[1])]).
			WithArgs(2.5, int64(9)).
			WillReturnRows(sqlmock.NewRows(accountQuotaStateColumns))
		if !(hints[0] && hints[1]) {
			mock.ExpectQuery(accountQuotaIncrementSQLs[accountQuotaIncrementSQLIndex(true, true)]).
				WithArgs(2.5, int64(9)).
				WillReturnRows(sqlmock.NewRows(accountQuotaStateColumns))
		}
		mock.ExpectRollback()

		state, err := incrementUsageBillingAccountQuota(ctx, tx, 9, 2.5, hints[0], hints[1])
		require.ErrorIs(t, err, service.ErrAccountNotFound, "hints=%v", hints)
		require.Nil(t, state)
		require.NoError(t, tx.Rollback())
		require.NoError(t, mock.ExpectationsWereMet(), "hints=%v", hints)
		_ = db.Close()
	}
}

func TestReserveUsageBillingBatchImageBalance_MovesAvailableToFrozen(t *testing.T) {
	ctx := context.Background()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	mock.ExpectBegin()
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	mock.ExpectQuery(reserveBatchImageHoldSQL).
		WithArgs(2.5, int64(42)).
		WillReturnRows(sqlmock.NewRows([]string{"balance", "frozen_balance"}).AddRow(7.5, 2.5))
	mock.ExpectCommit()

	result, err := reserveUsageBillingBatchImageBalance(ctx, tx, &service.BatchImageBalanceHoldCommand{UserID: 42, HoldAmount: 2.5})
	require.NoError(t, err)
	require.NotNil(t, result.NewBalance)
	require.NotNil(t, result.FrozenBalance)
	require.InDelta(t, 7.5, *result.NewBalance, 0.000001)
	require.InDelta(t, 2.5, *result.FrozenBalance, 0.000001)
	require.NoError(t, tx.Commit())
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestReserveUsageBillingBatchImageBalance_InsufficientBalance(t *testing.T) {
	ctx := context.Background()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	mock.ExpectBegin()
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	mock.ExpectQuery(reserveBatchImageHoldSQL).
		WithArgs(10.0, int64(42)).
		WillReturnError(sql.ErrNoRows)
	mock.ExpectQuery(userExistsForBillingSQL).
		WithArgs(int64(42)).
		WillReturnRows(sqlmock.NewRows([]string{"?column?"}).AddRow(1))
	mock.ExpectRollback()

	_, err = reserveUsageBillingBatchImageBalance(ctx, tx, &service.BatchImageBalanceHoldCommand{UserID: 42, HoldAmount: 10})
	require.ErrorIs(t, err, service.ErrBatchImageInsufficientBalance)
	require.NoError(t, tx.Rollback())
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCaptureUsageBillingBatchImageBalance_ReleasesRemainder(t *testing.T) {
	ctx := context.Background()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	mock.ExpectBegin()
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	mock.ExpectQuery(captureBatchImageHoldSQL).
		WithArgs(1.0, 0.25, int64(42)).
		WillReturnRows(sqlmock.NewRows([]string{"balance", "frozen_balance"}).AddRow(9.75, 0.0))
	mock.ExpectCommit()

	result, err := captureUsageBillingBatchImageBalance(ctx, tx, &service.BatchImageBalanceHoldCommand{UserID: 42, HoldAmount: 1, ActualAmount: 0.25})
	require.NoError(t, err)
	require.InDelta(t, 9.75, *result.NewBalance, 0.000001)
	require.InDelta(t, 0.0, *result.FrozenBalance, 0.000001)
	require.NoError(t, tx.Commit())
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCaptureUsageBillingBatchImageBalance_RejectsActualCostOverHold(t *testing.T) {
	ctx := context.Background()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	mock.ExpectBegin()
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	mock.ExpectRollback()

	_, err = captureUsageBillingBatchImageBalance(ctx, tx, &service.BatchImageBalanceHoldCommand{UserID: 42, HoldAmount: 0.5, ActualAmount: 1})
	require.ErrorIs(t, err, service.ErrBatchImageSettlementCostExceedsHold)
	require.NoError(t, tx.Rollback())
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestReleaseUsageBillingBatchImageBalance_ReturnsFrozenToAvailable(t *testing.T) {
	ctx := context.Background()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	mock.ExpectBegin()
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	mock.ExpectQuery(`SELECT 1\s+FROM usage_billing_dedup\s+WHERE request_id = \$1 AND api_key_id = \$2`).
		WithArgs(service.BatchImageHoldRequestID("imgbatch_release"), int64(7)).
		WillReturnRows(sqlmock.NewRows([]string{"?column?"}).AddRow(1))
	mock.ExpectQuery(releaseBatchImageHoldSQL).
		WithArgs(1.0, int64(42)).
		WillReturnRows(sqlmock.NewRows([]string{"balance", "frozen_balance"}).AddRow(10.0, 0.0))
	mock.ExpectCommit()

	result, err := releaseUsageBillingBatchImageBalance(ctx, tx, &service.BatchImageBalanceHoldCommand{UserID: 42, APIKeyID: 7, BatchID: "imgbatch_release", HoldAmount: 1})
	require.NoError(t, err)
	require.InDelta(t, 10.0, *result.NewBalance, 0.000001)
	require.InDelta(t, 0.0, *result.FrozenBalance, 0.000001)
	require.NoError(t, tx.Commit())
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestReleaseUsageBillingBatchImageBalance_SkipsWhenHoldNeverReserved(t *testing.T) {
	ctx := context.Background()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	mock.ExpectBegin()
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	// dedup 与归档表均无 hold claim：说明该 job 从未成功冻结，
	// 释放必须跳过，不得从他人冻结资金池中凭空生成余额。
	mock.ExpectQuery(`SELECT 1\s+FROM usage_billing_dedup\s+WHERE request_id = \$1 AND api_key_id = \$2`).
		WithArgs(service.BatchImageHoldRequestID("imgbatch_phantom"), int64(7)).
		WillReturnError(sql.ErrNoRows)
	mock.ExpectQuery(`SELECT 1\s+FROM usage_billing_dedup_archive\s+WHERE request_id = \$1 AND api_key_id = \$2`).
		WithArgs(service.BatchImageHoldRequestID("imgbatch_phantom"), int64(7)).
		WillReturnError(sql.ErrNoRows)
	mock.ExpectCommit()

	result, err := releaseUsageBillingBatchImageBalance(ctx, tx, &service.BatchImageBalanceHoldCommand{UserID: 42, APIKeyID: 7, BatchID: "imgbatch_phantom", HoldAmount: 1})
	require.NoError(t, err)
	require.Nil(t, result.NewBalance)
	require.Nil(t, result.FrozenBalance)
	require.NoError(t, tx.Commit())
	require.NoError(t, mock.ExpectationsWereMet())
}
