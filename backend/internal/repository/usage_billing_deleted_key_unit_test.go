//go:build unit

package repository

import (
	"context"
	"database/sql"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestUsageBillingDeletedKey_CommitsBalanceAndUsageLog(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	cmd, log := atomicUsageBillingFixture()
	cmd.APIKeyQuotaCost = cmd.BalanceCost
	cmd.APIKeyRateLimitCost = cmd.BalanceCost
	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL synchronous_commit = on").WillReturnResult(sqlmock.NewResult(0, 0))
	expectNewUsageBillingClaim(mock)
	mock.ExpectQuery(conditionalBalanceDeductSQL).WithArgs(cmd.BalanceCost, cmd.UserID).
		WillReturnRows(sqlmock.NewRows([]string{"balance"}).AddRow(98.75))
	mock.ExpectQuery("UPDATE api_keys").
		WithArgs(cmd.BalanceCost, cmd.APIKeyID, service.StatusAPIKeyActive, service.StatusAPIKeyQuotaExhausted).
		WillReturnError(sql.ErrNoRows)
	mock.ExpectExec("UPDATE api_keys SET").WithArgs(cmd.BalanceCost, cmd.APIKeyID).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery("INSERT INTO usage_logs").
		WillReturnRows(sqlmock.NewRows([]string{"id", "created_at"}).AddRow(101, log.CreatedAt))
	mock.ExpectCommit()

	result, err := (&usageBillingRepository{db: db}).ApplyWithUsageLog(context.Background(), cmd, log)
	require.NoError(t, err)
	require.True(t, result.Applied)
	require.False(t, result.APIKeyQuotaExhausted)
	require.Equal(t, 98.75, *result.NewBalance)
	require.Equal(t, int64(101), log.ID)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestUsageBillingDeletedKey_DoesNotIgnoreDatabaseFailure(t *testing.T) {
	for _, counter := range []string{"quota", "rate"} {
		t.Run(counter, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			t.Cleanup(func() { _ = db.Close() })
			cmd, log := atomicUsageBillingFixture()
			mock.ExpectBegin()
			mock.ExpectExec("SET LOCAL synchronous_commit = on").WillReturnResult(sqlmock.NewResult(0, 0))
			expectNewUsageBillingClaim(mock)
			mock.ExpectQuery(conditionalBalanceDeductSQL).WithArgs(cmd.BalanceCost, cmd.UserID).
				WillReturnRows(sqlmock.NewRows([]string{"balance"}).AddRow(98.75))
			if counter == "quota" {
				cmd.APIKeyQuotaCost = cmd.BalanceCost
				mock.ExpectQuery("UPDATE api_keys").WillReturnError(sql.ErrConnDone)
			} else {
				cmd.APIKeyRateLimitCost = cmd.BalanceCost
				mock.ExpectExec("UPDATE api_keys SET").WillReturnError(sql.ErrConnDone)
			}
			mock.ExpectRollback()
			_, err = (&usageBillingRepository{db: db}).ApplyWithUsageLog(context.Background(), cmd, log)
			require.ErrorIs(t, err, sql.ErrConnDone)
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}
