//go:build unit

package repository

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

type usageBillingEgressCache struct {
	info *service.ProxyLatencyInfo
	err  error
}

func (c *usageBillingEgressCache) GetProxyLatencies(_ context.Context, ids []int64) (map[int64]*service.ProxyLatencyInfo, error) {
	return map[int64]*service.ProxyLatencyInfo{ids[0]: c.info}, c.err
}

func (*usageBillingEgressCache) SetProxyLatency(context.Context, int64, *service.ProxyLatencyInfo) error {
	return nil
}

func TestUsageBillingApplyWithUsageLog_EnrichesEgressBeforeAtomicInsert(t *testing.T) {
	for _, cacheFailure := range []bool{false, true} {
		t.Run(map[bool]string{false: "cached IP", true: "cache unavailable"}[cacheFailure], func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer db.Close()
			cmd, log := atomicUsageBillingFixture()
			probeAt := log.CreatedAt.Add(-time.Minute)
			log.Egress = &service.UsageEgress{Route: "proxy", ProxyID: 84, CapturedAt: log.CreatedAt, ProxyUpdatedAt: log.CreatedAt.Add(-time.Hour)}
			cache := &usageBillingEgressCache{info: &service.ProxyLatencyInfo{Success: true, IPAddress: "203.0.113.84", UpdatedAt: probeAt}}
			expected := *log
			expectedEgress := *log.Egress
			expected.Egress = &expectedEgress
			if cacheFailure {
				cache.err = errors.New("cache unavailable")
			} else {
				expected.Egress.IPAddress = "203.0.113.84"
				expected.Egress.IPObservedAt = &probeAt
			}
			prepared := prepareUsageLogInsert(&expected)
			mock.ExpectBegin()
			mock.ExpectExec("SET LOCAL synchronous_commit = on").WillReturnResult(sqlmock.NewResult(0, 0))
			expectNewUsageBillingClaim(mock)
			mock.ExpectQuery(conditionalBalanceDeductSQL).WithArgs(cmd.BalanceCost, cmd.UserID).
				WillReturnRows(sqlmock.NewRows([]string{"balance"}).AddRow(98.75))
			mock.ExpectQuery("INSERT INTO usage_logs").WithArgs(anySliceToDriverValues(prepared.args)...).
				WillReturnRows(sqlmock.NewRows([]string{"id", "created_at"}).AddRow(101, log.CreatedAt))
			mock.ExpectCommit()
			repo := NewUsageBillingRepositoryWithEgress(nil, db, cache).(service.UsageBillingWithUsageLogRepository)
			result, err := repo.ApplyWithUsageLog(context.Background(), cmd, log)
			require.NoError(t, err)
			require.True(t, result.Applied)
			require.Equal(t, expected.Egress.IPAddress, log.Egress.IPAddress)
			require.Equal(t, cmd.BalanceCost, log.ActualCost)
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}
