//go:build unit

package repository

import (
	"context"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
)

func TestGetAllGroupUsageSummaryUsesHalfOpenDayWindow(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	start := time.Date(2026, 10, 11, 0, 0, 0, 0, time.FixedZone("CST", 8*60*60))
	end := start.AddDate(0, 0, 1)
	mock.ExpectQuery(regexp.QuoteMeta("WHEN ul.created_at >= $1 AND ul.created_at < $2")).
		WithArgs(start, end).
		WillReturnRows(sqlmock.NewRows([]string{"group_id", "total_cost", "today_cost"}).
			AddRow(int64(33), 104.4560531580, 0.8242453760))

	repo := &usageLogRepository{sql: db}
	rows, err := repo.GetAllGroupUsageSummary(context.Background(), start, end)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, int64(33), rows[0].GroupID)
	require.InDelta(t, 104.4560531580, rows[0].TotalCost, 1e-12)
	require.InDelta(t, 0.8242453760, rows[0].TodayCost, 1e-12)
	require.NoError(t, mock.ExpectationsWereMet())
}
