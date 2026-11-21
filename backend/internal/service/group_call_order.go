package service

import (
	"context"
	"sort"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
)

func groupCallRanks(order []int64) map[int64]int {
	if len(order) == 0 {
		return nil
	}
	ranks := make(map[int64]int, len(order))
	for _, id := range order {
		if _, ok := ranks[id]; !ok && id > 0 {
			ranks[id] = len(ranks)
		}
	}
	return ranks
}

// The generic schedulers compare account.Priority. Project ranks onto a request-local
// copy only; unranked members retain their normal priority relative to one another.
func groupOrderedCandidates(ctx context.Context, groupID *int64, accounts []Account, repo GroupRepository) []Account {
	if groupID == nil || len(accounts) < 2 {
		return accounts
	}
	g, _ := ctx.Value(ctxkey.Group).(*Group)
	if g == nil || g.ID != *groupID {
		if repo == nil {
			return accounts
		}
		g, _ = repo.GetByIDLite(ctx, *groupID)
	}
	if g == nil || len(g.AccountCallOrder) == 0 {
		return accounts
	}
	ranks := groupCallRanks(g.AccountCallOrder)
	var unranked []int
	for i := range accounts {
		if _, ok := ranks[accounts[i].ID]; !ok {
			unranked = append(unranked, accounts[i].Priority)
		}
	}
	sort.Ints(unranked)
	out := append([]Account(nil), accounts...)
	for i := range out {
		if rank, ok := ranks[out[i].ID]; ok {
			out[i].Priority = rank
		} else {
			out[i].Priority = len(ranks) + sort.SearchInts(unranked, out[i].Priority)
		}
	}
	return out
}
