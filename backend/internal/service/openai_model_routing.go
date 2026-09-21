package service

import (
	"context"
	"sort"
	"strconv"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
)

// openAIModelRoutingPreference is a soft tier above the normal account
// priority. It combines the existing group-scoped routing with the global
// model priority panel. An empty preference leaves scheduling unchanged.
type openAIModelRoutingPreference struct {
	accountIDs   map[int64]struct{}
	poolIDs      map[string]struct{}
	accountRanks map[int64]int
}

func newOpenAIModelRoutingPreference(accountIDs, poolIDs []int64) openAIModelRoutingPreference {
	preference := openAIModelRoutingPreference{}
	for _, accountID := range accountIDs {
		if accountID <= 0 {
			continue
		}
		if preference.accountIDs == nil {
			preference.accountIDs = make(map[int64]struct{}, len(accountIDs))
		}
		preference.accountIDs[accountID] = struct{}{}
	}
	for _, poolID := range poolIDs {
		if poolID <= 0 {
			continue
		}
		if preference.poolIDs == nil {
			preference.poolIDs = make(map[string]struct{}, len(poolIDs))
		}
		preference.poolIDs[strconv.FormatInt(poolID, 10)] = struct{}{}
	}
	return preference
}

func (p openAIModelRoutingPreference) configured() bool {
	return len(p.accountIDs) > 0 || len(p.poolIDs) > 0
}

func (p openAIModelRoutingPreference) matches(account *Account) bool {
	if account == nil || !p.configured() {
		return false
	}
	if _, ok := p.accountIDs[account.ID]; ok {
		return true
	}
	poolID := strings.TrimSpace(account.GetExtraString(AccountPoolExtraKey))
	if poolID == "" {
		return false
	}
	_, ok := p.poolIDs[poolID]
	return ok
}

func (s *OpenAIGatewayService) resolveOpenAIModelRoutingPreference(
	ctx context.Context,
	groupID *int64,
	platform string,
	requestedModel string,
) openAIModelRoutingPreference {
	if s == nil || strings.TrimSpace(requestedModel) == "" ||
		normalizeOpenAICompatiblePlatform(platform) != PlatformOpenAI {
		return openAIModelRoutingPreference{}
	}
	preference := openAIModelRoutingPreference{}
	if s.settingService != nil {
		preference = s.settingService.resolveOpenAIModelPriorityPreference(ctx, requestedModel)
	}
	if groupID == nil {
		return preference
	}

	var group *Group
	if ctx != nil {
		if contextual, ok := ctx.Value(ctxkey.Group).(*Group); ok &&
			IsGroupContextValid(contextual) && contextual.ID == *groupID {
			group = contextual
		}
	}
	if group == nil && s.schedulerSnapshot != nil {
		group, _ = s.schedulerSnapshot.GetGroupByID(ctx, *groupID)
	}
	if group == nil || !group.ModelRoutingEnabled ||
		(group.Platform != PlatformOpenAI && group.Platform != PlatformComposite) {
		return preference
	}

	groupPreference := newOpenAIModelRoutingPreference(
		group.GetRoutingAccountIDs(requestedModel),
		group.GetRoutingAccountPoolIDs(requestedModel),
	)
	for accountID := range preference.accountIDs {
		if groupPreference.accountIDs == nil {
			groupPreference.accountIDs = make(map[int64]struct{}, len(preference.accountIDs))
		}
		groupPreference.accountIDs[accountID] = struct{}{}
	}
	groupPreference.accountRanks = preference.accountRanks
	return groupPreference
}

// Explicit model order precedes account-global priority. Legacy preferences
// remain one tier; group/pool additions follow explicitly ordered accounts.
func (p openAIModelRoutingPreference) tier(account *Account) int {
	if account != nil {
		if rank, ok := p.accountRanks[account.ID]; ok {
			return rank
		}
	}
	if p.matches(account) {
		return len(p.accountRanks)
	}
	return len(p.accountRanks) + 1
}

func partitionOpenAIModelPreferenceTiers[T any](items []T, accountOf func(T) *Account, preference openAIModelRoutingPreference) [][]T {
	buckets := make(map[int][]T)
	for _, item := range items {
		tier := preference.tier(accountOf(item))
		buckets[tier] = append(buckets[tier], item)
	}
	keys := make([]int, 0, len(buckets))
	for tier := range buckets {
		keys = append(keys, tier)
	}
	sort.Ints(keys)
	result := make([][]T, 0, len(keys))
	for _, tier := range keys {
		result = append(result, buckets[tier])
	}
	return result
}

func modelRoutingPreferenceFirst(
	preference openAIModelRoutingPreference,
	left *Account,
	right *Account,
) (bool, bool) {
	if !preference.configured() {
		return false, false
	}
	leftTier, rightTier := preference.tier(left), preference.tier(right)
	if leftTier == rightTier {
		return false, false
	}
	return leftTier < rightTier, true
}

func sortOpenAIAccountsByModelPreference(
	accounts []*Account,
	preference openAIModelRoutingPreference,
) {
	sortAccountsByPriorityAndLastUsed(accounts, false)
	sort.SliceStable(accounts, func(i, j int) bool { return preference.tier(accounts[i]) < preference.tier(accounts[j]) })
}

func orderOpenAIExecutionNodeCandidatesByModelPreference[T any](
	items []T,
	accountOf func(T) *Account,
	preference openAIModelRoutingPreference,
	policy executionNodeRoutingPolicy,
	anchor string,
) []T {
	if !preference.configured() {
		return orderExecutionNodeCandidatesWithinPriorities(
			items,
			accountOf,
			func(item T) int { return openAIAccountSchedulingPriority(accountOf(item)) },
			policy,
			anchor,
		)
	}
	ordered := make([]T, 0, len(items))
	for _, tier := range partitionOpenAIModelPreferenceTiers(items, accountOf, preference) {
		ordered = append(ordered, orderExecutionNodeCandidatesWithinPriorities(
			tier, accountOf, func(item T) int { return openAIAccountSchedulingPriority(accountOf(item)) }, policy, anchor,
		)...)
	}
	return ordered
}
