//go:build unit

package repository

import (
	"context"
	"database/sql"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

var (
	usageLogStaticInsertShapeRe = regexp.MustCompile(`(?s)INSERT INTO usage_logs \((.*?)\) VALUES \((.*?)\)`)
	usageLogPlaceholderRe       = regexp.MustCompile(`\$(\d+)`)
)

type usageLogProxyLatencyCacheStub struct {
	calls int
	info  *service.ProxyLatencyInfo
}

func (s *usageLogProxyLatencyCacheStub) GetProxyLatencies(context.Context, []int64) (map[int64]*service.ProxyLatencyInfo, error) {
	s.calls++
	return map[int64]*service.ProxyLatencyInfo{17: s.info}, nil
}

func (*usageLogProxyLatencyCacheStub) SetProxyLatency(context.Context, int64, *service.ProxyLatencyInfo) error {
	return nil
}

// newSQLCapturingMock 返回把实际下发 SQL 记录到 captured 的 sqlmock；语句一律视为匹配，
// 参数仍由 WithArgs 校验。
func newSQLCapturingMock(t *testing.T, captured *[]string) (*sql.DB, sqlmock.Sqlmock) {
	t.Helper()
	matcher := sqlmock.QueryMatcherFunc(func(_, actualSQL string) error {
		*captured = append(*captured, actualSQL)
		return nil
	})
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(matcher))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return db, mock
}

// requireStaticInsertMatchesArgTypes 断言手写的 INSERT：列清单长度与 VALUES 占位符数量
// 都等于 usageLogInsertArgTypes，且占位符恰为 $1..$N 各出现一次。
func requireStaticInsertMatchesArgTypes(t *testing.T, query string) {
	t.Helper()
	m := usageLogStaticInsertShapeRe.FindStringSubmatch(query)
	require.Len(t, m, 3, "unrecognised INSERT shape:\n%s", query)

	want := len(usageLogInsertArgTypes)
	columns := 0
	for _, col := range strings.Split(m[1], ",") {
		if strings.TrimSpace(col) != "" {
			columns++
		}
	}
	require.Equal(t, want, columns, "INSERT column list must match usageLogInsertArgTypes")

	seen := make(map[int]struct{}, want)
	for _, ph := range usageLogPlaceholderRe.FindAllStringSubmatch(m[2], -1) {
		n, err := strconv.Atoi(ph[1])
		require.NoError(t, err)
		_, dup := seen[n]
		require.False(t, dup, "duplicate placeholder $%d", n)
		seen[n] = struct{}{}
	}
	require.Len(t, seen, want, "VALUES placeholder count must match usageLogInsertArgTypes")
	for i := 1; i <= want; i++ {
		_, ok := seen[i]
		require.True(t, ok, "missing placeholder $%d", i)
	}
}

// TestUsageLogStaticInsertShape_PlaceholdersMatchArgTypes 覆盖两条不经占位符生成器、
// 直接手写 $1..$N 的 INSERT 路径，防止加列后漏补占位符只在集成测试才暴露。
func TestUsageLogStaticInsertShape_PlaceholdersMatchArgTypes(t *testing.T) {
	upstreamRequestID := "20260902080329-oneapi"
	log := &service.UsageLog{
		UserID:            1,
		APIKeyID:          2,
		AccountID:         3,
		RequestID:         "client:insert-shape",
		UpstreamRequestID: &upstreamRequestID,
		Model:             "claude-3",
		InputTokens:       10,
		CreatedAt:         time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC),
	}
	prepared := prepareUsageLogInsert(log)
	args := anySliceToDriverValues(prepared.args)

	t.Run("createSingle", func(t *testing.T) {
		var captured []string
		db, mock := newSQLCapturingMock(t, &captured)
		repo := &usageLogRepository{sql: db}

		mock.ExpectQuery("INSERT INTO usage_logs").
			WithArgs(args...).
			WillReturnRows(sqlmock.NewRows([]string{"id", "created_at"}).AddRow(int64(1), log.CreatedAt))

		inserted, err := repo.Create(context.Background(), log)
		require.NoError(t, err)
		require.True(t, inserted)
		require.NoError(t, mock.ExpectationsWereMet())
		require.Len(t, captured, 1)
		requireStaticInsertMatchesArgTypes(t, captured[0])
	})

	t.Run("execUsageLogInsertNoResult", func(t *testing.T) {
		var captured []string
		db, mock := newSQLCapturingMock(t, &captured)

		mock.ExpectExec("INSERT INTO usage_logs").
			WithArgs(args...).
			WillReturnResult(sqlmock.NewResult(0, 1))

		require.NoError(t, execUsageLogInsertNoResult(context.Background(), db, prepared))
		require.NoError(t, mock.ExpectationsWereMet())
		require.Len(t, captured, 1)
		requireStaticInsertMatchesArgTypes(t, captured[0])
	})
}

// TestPrepareUsageLogInsert_UpstreamRequestIDArgWiring 把 upstream_request_id 钉在
// session_id 之前，与参数类型表保持同位；缺失时落 NULL 而不是空串。
func TestPrepareUsageLogInsert_UpstreamRequestIDArgWiring(t *testing.T) {
	upstreamRequestID := "req_upstream_123"
	prepared := prepareUsageLogInsert(&service.UsageLog{
		UserID:            1,
		APIKeyID:          2,
		RequestID:         "client:wiring",
		Model:             "gpt-5",
		UpstreamRequestID: &upstreamRequestID,
		CreatedAt:         time.Now().UTC(),
	})
	require.Len(t, prepared.args, len(usageLogInsertArgTypes))

	idx := len(prepared.args) - 5
	arg, ok := prepared.args[idx].(sql.NullString)
	require.True(t, ok, "upstream_request_id arg should be sql.NullString, got %T", prepared.args[idx])
	require.True(t, arg.Valid)
	require.Equal(t, upstreamRequestID, arg.String)
	require.Equal(t, "text", usageLogInsertArgTypes[idx])

	absent := prepareUsageLogInsert(&service.UsageLog{UserID: 1, APIKeyID: 2, RequestID: "client:absent", Model: "gpt-5", CreatedAt: time.Now().UTC()})
	nullArg, ok := absent.args[idx].(sql.NullString)
	require.True(t, ok)
	require.False(t, nullArg.Valid, "absent upstream request id must be NULL")

	require.Contains(t, usageLogSelectColumns, "upstream_request_id")
}

func TestPrepareUsageLogInsert_EgressJSONWiring(t *testing.T) {
	observedAt := time.Date(2026, 10, 24, 8, 30, 0, 0, time.UTC)
	prepared := prepareUsageLogInsert(&service.UsageLog{
		UserID:    1,
		APIKeyID:  2,
		AccountID: 3,
		RequestID: "client:egress",
		Model:     "gpt-5",
		Egress: &service.UsageEgress{
			Route:           "proxy",
			ProxyID:         17,
			ProxyName:       "edge-a",
			ProxyAddress:    "proxy.example:1080",
			IPAddress:       "203.0.113.17",
			IPObservedAt:    &observedAt,
			ExecutionNodeID: "node-a",
			ProxyUpdatedAt:  observedAt.Add(-time.Minute),
			CapturedAt:      observedAt.Add(time.Minute),
		},
		CreatedAt: observedAt,
	})
	require.Len(t, prepared.args, len(usageLogInsertArgTypes))

	idx := len(prepared.args) - 2
	raw, ok := prepared.args[idx].(string)
	require.True(t, ok, "egress arg should be JSON text, got %T", prepared.args[idx])
	require.Equal(t, "jsonb", usageLogInsertArgTypes[idx])
	require.JSONEq(t, `{
		"route":"proxy",
		"proxy_id":17,
		"proxy_name":"edge-a",
		"proxy_address":"proxy.example:1080",
		"ip_address":"203.0.113.17",
		"ip_observed_at":"2026-10-24T08:30:00Z",
		"execution_node_id":"node-a"
	}`, raw)
	require.NotContains(t, raw, "proxy_updated_at")
	require.NotContains(t, raw, "captured_at")

	absent := prepareUsageLogInsert(&service.UsageLog{
		UserID: 1, APIKeyID: 2, AccountID: 3, RequestID: "client:no-egress", Model: "gpt-5", CreatedAt: observedAt,
	})
	require.Nil(t, absent.args[idx], "absent egress must be SQL NULL")
	require.Contains(t, usageLogSelectColumns, "egress")
}

func TestUsageLogRepositoryEnrichesEgressOnceBeforeWrite(t *testing.T) {
	observedAt := time.Date(2026, 10, 24, 8, 30, 0, 0, time.UTC)

	for _, tc := range []struct {
		name  string
		write func(*usageLogRepository, *service.UsageLog) error
	}{
		{
			name: "create",
			write: func(repo *usageLogRepository, log *service.UsageLog) error {
				_, err := repo.Create(context.Background(), log)
				return err
			},
		},
		{
			name: "best_effort",
			write: func(repo *usageLogRepository, log *service.UsageLog) error {
				return repo.CreateBestEffort(context.Background(), log)
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, mock := newSQLMock(t)
			cache := &usageLogProxyLatencyCacheStub{info: &service.ProxyLatencyInfo{
				Success: true, IPAddress: "203.0.113.17", UpdatedAt: observedAt,
			}}
			repo := &usageLogRepository{sql: db, proxyLatencyCache: cache}
			log := &service.UsageLog{
				UserID: 1, APIKeyID: 2, AccountID: 3, Model: "gpt-5", CreatedAt: observedAt,
				Egress: &service.UsageEgress{
					Route: "proxy", ProxyID: 17, CapturedAt: observedAt.Add(time.Second),
				},
			}

			mock.ExpectQuery("INSERT INTO usage_logs").
				WillReturnRows(sqlmock.NewRows([]string{"id", "created_at"}).AddRow(int64(1), observedAt))

			require.NoError(t, tc.write(repo, log))
			require.Equal(t, 1, cache.calls)
			require.NotNil(t, log.Egress)
			require.Equal(t, "203.0.113.17", log.Egress.IPAddress)
			require.NotNil(t, log.Egress.IPObservedAt)
			require.Equal(t, observedAt, *log.Egress.IPObservedAt)
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}
