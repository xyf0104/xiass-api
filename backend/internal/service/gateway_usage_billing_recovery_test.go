package service

import (
	"context"
	"database/sql/driver"
	"errors"
	"io"
	"net"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

type usageBillingRecoveryRepo struct {
	UsageBillingRepository
	apply func(context.Context, *UsageBillingCommand) (*UsageBillingApplyResult, error)
}

type usageBillingAtomicRecoveryRepo struct {
	usageBillingRecoveryRepo
	applyWithUsageLog func(context.Context, *UsageBillingCommand, *UsageLog) (*UsageBillingApplyResult, error)
}

func (r usageBillingAtomicRecoveryRepo) ApplyWithUsageLog(ctx context.Context, cmd *UsageBillingCommand, log *UsageLog) (*UsageBillingApplyResult, error) {
	return r.applyWithUsageLog(ctx, cmd, log)
}

func (r usageBillingRecoveryRepo) Apply(ctx context.Context, cmd *UsageBillingCommand) (*UsageBillingApplyResult, error) {
	return r.apply(ctx, cmd)
}

func TestUsageBillingRecoveryRetriesSameIdempotencyCommand(t *testing.T) {
	for _, applied := range []bool{true, false} {
		cmd := &UsageBillingCommand{RequestID: "fixed-response", APIKeyID: 9, BalanceCost: 0.178}
		cmd.Normalize()
		original := *cmd
		calls := 0
		repo := usageBillingRecoveryRepo{apply: func(_ context.Context, received *UsageBillingCommand) (*UsageBillingApplyResult, error) {
			calls++
			require.Same(t, cmd, received)
			require.Equal(t, original, *received)
			if calls == 1 {
				return nil, &pq.Error{Code: "57P01"}
			}
			return &UsageBillingApplyResult{Applied: applied}, nil
		}}
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		result, attempts, err := retryUsageBilling(ctx, repo, cmd)
		cancel()
		require.NoError(t, err)
		require.Equal(t, 2, attempts)
		require.Equal(t, applied, result.Applied, "an already-committed retry must not be reported as a new charge")
	}
}

func TestUsageBillingRecoveryRetriesAtomicUsageLogAfterLostCommitAck(t *testing.T) {
	cmd := &UsageBillingCommand{RequestID: "local:lost-commit", APIKeyID: 9, BalanceCost: 0.178}
	cmd.Normalize()
	usageLog := &UsageLog{RequestID: cmd.RequestID, APIKeyID: cmd.APIKeyID, ActualCost: cmd.BalanceCost}
	calls := 0
	repo := usageBillingAtomicRecoveryRepo{
		usageBillingRecoveryRepo: usageBillingRecoveryRepo{apply: func(context.Context, *UsageBillingCommand) (*UsageBillingApplyResult, error) {
			t.Fatal("atomic repository must not fall back to Apply")
			return nil, nil
		}},
		applyWithUsageLog: func(_ context.Context, receivedCmd *UsageBillingCommand, receivedLog *UsageLog) (*UsageBillingApplyResult, error) {
			calls++
			require.Same(t, cmd, receivedCmd)
			require.Same(t, usageLog, receivedLog)
			if calls == 1 {
				return nil, io.EOF
			}
			return &UsageBillingApplyResult{Applied: false}, nil
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	result, attempts, err := retryUsageBillingWithUsageLog(ctx, repo, cmd, usageLog)
	require.NoError(t, err)
	require.Equal(t, 2, attempts)
	require.Equal(t, 2, calls)
	require.False(t, result.Applied)
}

func TestWriteUsageLogBestEffort_SkipsAlreadyPersistedLog(t *testing.T) {
	repo := &openAIRecordUsageLogRepoStub{}
	writeUsageLogBestEffort(context.Background(), repo, &UsageLog{ID: 101}, "test")
	require.Zero(t, repo.calls)
}

func TestWriteUsageLogAfterBillingError_DoesNotCreateZeroCostConflict(t *testing.T) {
	for _, billingErr := range []error{ErrUsageBillingRequestConflict, ErrUsageBillingUsageLogMissing, ErrUsageBillingUsageLogConflict} {
		repo := &openAIRecordUsageLogRepoStub{}
		usageLog := &UsageLog{ActualCost: 1.25}
		writeUsageLogAfterBillingError(context.Background(), repo, usageLog, "test", billingErr)
		require.Zero(t, repo.calls)
		require.Equal(t, 1.25, usageLog.ActualCost)
	}
}

func TestWriteUsageLogAfterBillingError_PreservesLegacyZeroCostErrorLog(t *testing.T) {
	repo := &openAIRecordUsageLogRepoStub{inserted: true}
	usageLog := &UsageLog{ActualCost: 1.25}
	writeUsageLogAfterBillingError(context.Background(), repo, usageLog, "test", errors.New("billing failed before commit"))
	require.Equal(t, 1, repo.calls)
	require.Zero(t, usageLog.ActualCost)
}

func TestUsageBillingRecoveryDoesNotReplayPermanentFailure(t *testing.T) {
	for _, failure := range []error{ErrUsageBillingRequestConflict, &pq.Error{Code: "23503"}, &pq.Error{Code: "28P01"}, context.Canceled} {
		calls := 0
		repo := usageBillingRecoveryRepo{apply: func(context.Context, *UsageBillingCommand) (*UsageBillingApplyResult, error) {
			calls++
			return nil, failure
		}}
		_, attempts, err := retryUsageBilling(context.Background(), repo, &UsageBillingCommand{RequestID: "fixed"})
		require.ErrorIs(t, err, failure)
		require.Equal(t, 1, attempts)
		require.Equal(t, 1, calls)
	}
}

func TestUsageBillingRecoveryDeadlineStopsRetries(t *testing.T) {
	calls := 0
	repo := usageBillingRecoveryRepo{apply: func(context.Context, *UsageBillingCommand) (*UsageBillingApplyResult, error) {
		calls++
		return nil, io.EOF
	}}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, attempts, err := retryUsageBilling(ctx, repo, &UsageBillingCommand{RequestID: "fixed"})
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.Equal(t, 1, attempts)
	require.Equal(t, 1, calls)
	_, attempts, err = retryUsageBilling(context.Background(), repo, &UsageBillingCommand{})
	require.ErrorIs(t, err, ErrUsageBillingRequestIDRequired)
	require.Zero(t, attempts)
	require.Equal(t, 1, calls)
}

func TestUsageBillingRecoveryErrorClassification(t *testing.T) {
	for _, err := range []error{driver.ErrBadConn, io.EOF, io.ErrUnexpectedEOF, &net.OpError{Op: "dial", Err: errors.New("refused")}, &pq.Error{Code: "08006"}, &pq.Error{Code: "40001"}, &pq.Error{Code: "40P01"}, &pq.Error{Code: "25006"}, errors.Join(ErrUsageBillingPrimaryUnavailable, &pq.Error{Code: "0A000"})} {
		require.True(t, transientUsageBillingError(err))
	}
	for _, err := range []error{nil, errors.New("not a database connectivity error"), context.DeadlineExceeded, &pq.Error{Code: "42601"}, &pq.Error{Code: "0A000"}, errors.Join(ErrUsageBillingPrimaryUnavailable, context.Canceled)} {
		require.False(t, transientUsageBillingError(err))
	}
}

func TestUsageBillingRecoveryBudgetKeepsDefaults(t *testing.T) {
	for _, tc := range []struct {
		enabled       bool
		seconds, want int
	}{
		{false, 60, 15}, {true, 5, 15}, {true, 60, 60}, {true, 1000, 120},
	} {
		cfg := &config.Config{}
		cfg.Gateway.ExecutionNode.Enabled = tc.enabled
		cfg.Gateway.UsageRecord.TaskTimeoutSeconds = tc.seconds
		ctx, cancel := detachedUsageBillingRecoveryContext(context.Background(), cfg)
		deadline, ok := ctx.Deadline()
		require.True(t, ok)
		require.InDelta(t, tc.want, time.Until(deadline).Seconds(), 0.2)
		cancel()
	}
}

func TestUsageBillingRecoveryUsesOpenAIConfiguredBudget(t *testing.T) {
	cfg := &config.Config{}
	cfg.Gateway.ExecutionNode.Enabled = true
	cfg.Gateway.UsageRecord.TaskTimeoutSeconds = 60
	svc := &OpenAIGatewayService{cfg: cfg}
	require.True(t, cfg == svc.billingDeps().cfg, "OpenAI must forward its existing service configuration to billing")
	ctx, cancel := detachedUsageBillingRecoveryContext(context.Background(), svc.billingDeps().cfg)
	defer cancel()
	deadline, ok := ctx.Deadline()
	require.True(t, ok)
	require.InDelta(t, 60, time.Until(deadline).Seconds(), 0.2)
}
