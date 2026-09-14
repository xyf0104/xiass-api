package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type opsConcurrencyAccountRepoStub struct {
	AccountRepository
	accounts []Account
}

func (s *opsConcurrencyAccountRepoStub) ListOpsAccountsForStats(context.Context, string, *int64) ([]Account, error) {
	return s.accounts, nil
}

type opsConcurrencyCacheStub struct {
	ConcurrencyCache
	globalAccountCounts map[int64]int
	groupCounts         map[int64]int
	groupAccountCounts  map[int64]map[int64]int
}

func (s *opsConcurrencyCacheStub) GetAccountsLoadBatch(_ context.Context, accounts []AccountWithConcurrency) (map[int64]*AccountLoadInfo, error) {
	result := make(map[int64]*AccountLoadInfo, len(accounts))
	for _, account := range accounts {
		result[account.ID] = &AccountLoadInfo{
			AccountID:          account.ID,
			CurrentConcurrency: s.globalAccountCounts[account.ID],
		}
	}
	return result, nil
}

func (s *opsConcurrencyCacheStub) TrackGroupSlot(context.Context, int64, int64, string) error {
	return nil
}

func (s *opsConcurrencyCacheStub) ReleaseGroupSlot(context.Context, int64, int64, string) error {
	return nil
}

func (s *opsConcurrencyCacheStub) GetGroupConcurrencyBatch(_ context.Context, groupIDs []int64) (map[int64]int, error) {
	result := make(map[int64]int, len(groupIDs))
	for _, groupID := range groupIDs {
		result[groupID] = s.groupCounts[groupID]
	}
	return result, nil
}

func (s *opsConcurrencyCacheStub) GetGroupAccountConcurrency(_ context.Context, groupID int64) (map[int64]int, bool, time.Time, error) {
	return s.groupAccountCounts[groupID], true, time.Unix(1_789_345_600, 0).UTC(), nil
}

func TestOpsConcurrencyGroupFilterUsesGroupScopedAccountSlots(t *testing.T) {
	group7 := &Group{ID: 7, Name: "Group 7", Platform: PlatformOpenAI}
	group8 := &Group{ID: 8, Name: "Group 8", Platform: PlatformOpenAI}
	repo := &opsConcurrencyAccountRepoStub{accounts: []Account{
		{ID: 10, Name: "Shared", Platform: PlatformOpenAI, Concurrency: 5, Groups: []*Group{group7, group8}},
		{ID: 11, Name: "Group 7 only", Platform: PlatformOpenAI, Concurrency: 5, Groups: []*Group{group7}},
	}}
	cache := &opsConcurrencyCacheStub{
		globalAccountCounts: map[int64]int{10: 3, 11: 2},
		groupCounts:         map[int64]int{7: 3, 8: 2},
		groupAccountCounts:  map[int64]map[int64]int{7: {10: 1, 11: 2}, 8: {10: 2}},
	}
	service := &OpsService{
		accountRepo:        repo,
		concurrencyService: NewConcurrencyService(cache),
	}

	groupID := int64(7)
	_, groups, accounts, _, err := service.GetConcurrencyStats(context.Background(), "", &groupID)
	require.NoError(t, err)
	require.Equal(t, int64(1), accounts[10].CurrentInUse)
	require.Equal(t, int64(2), accounts[11].CurrentInUse)
	require.Equal(t, int64(3), groups[7].CurrentInUse)

	_, groups, accounts, _, err = service.GetConcurrencyStats(context.Background(), "", nil)
	require.NoError(t, err)
	require.Equal(t, int64(3), accounts[10].CurrentInUse)
	require.Equal(t, int64(2), accounts[11].CurrentInUse)
	require.Equal(t, int64(3), groups[7].CurrentInUse)
	require.Equal(t, int64(2), groups[8].CurrentInUse)
}
