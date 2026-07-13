package repository

import (
	"context"
	"database/sql"
	"fmt"
	"testing"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/enttest"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	_ "modernc.org/sqlite"
)

func newGroupRepoCostRatioSQLite(t *testing.T) *groupRepository {
	t.Helper()

	db, err := sql.Open("sqlite", fmt.Sprintf("file:%s?mode=memory&cache=shared&_fk=1", t.Name()))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(1)

	_, err = db.Exec("PRAGMA foreign_keys = ON")
	require.NoError(t, err)

	drv := entsql.OpenDB(dialect.SQLite, db)
	client := enttest.NewClient(t, enttest.WithOptions(dbent.Driver(drv)))
	t.Cleanup(func() { _ = client.Close() })

	// cost_ratio must persist through Ent itself; no raw SQL executor is supplied.
	return newGroupRepositoryWithSQL(client, nil)
}

func newCostRatioTestGroup(name string, costRatio *float64) *service.Group {
	return &service.Group{
		Name:             name,
		Platform:         service.PlatformAnthropic,
		RateMultiplier:   1,
		Status:           service.StatusActive,
		SubscriptionType: service.SubscriptionTypeStandard,
		CostRatio:        costRatio,
	}
}

func TestGroupRepositoryCreatePersistsCostRatio(t *testing.T) {
	repo := newGroupRepoCostRatioSQLite(t)
	ctx := context.Background()
	costRatio := 0.125
	group := newCostRatioTestGroup("cost-ratio-create", &costRatio)

	require.NoError(t, repo.Create(ctx, group))

	got, err := repo.GetByIDLite(ctx, group.ID)
	require.NoError(t, err)
	require.NotNil(t, got.CostRatio)
	require.InDelta(t, costRatio, *got.CostRatio, 1e-12)
}

func TestGroupRepositoryUpdatePersistsZeroCostRatio(t *testing.T) {
	repo := newGroupRepoCostRatioSQLite(t)
	ctx := context.Background()
	initialCostRatio := 0.125
	group := newCostRatioTestGroup("cost-ratio-update", &initialCostRatio)
	require.NoError(t, repo.Create(ctx, group))

	zero := 0.0
	group.CostRatio = &zero
	require.NoError(t, repo.Update(ctx, group))

	got, err := repo.GetByIDLite(ctx, group.ID)
	require.NoError(t, err)
	require.NotNil(t, got.CostRatio)
	require.Zero(t, *got.CostRatio)
}

func TestGroupRepositoryUpdateClearsCostRatio(t *testing.T) {
	repo := newGroupRepoCostRatioSQLite(t)
	ctx := context.Background()
	initialCostRatio := 0.125
	group := newCostRatioTestGroup("cost-ratio-clear", &initialCostRatio)
	require.NoError(t, repo.Create(ctx, group))

	group.CostRatio = nil
	require.NoError(t, repo.Update(ctx, group))

	got, err := repo.GetByIDLite(ctx, group.ID)
	require.NoError(t, err)
	require.Nil(t, got.CostRatio)
}
