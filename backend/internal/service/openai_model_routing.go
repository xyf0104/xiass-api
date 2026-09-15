package service

import (
	"context"
	"strconv"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
)

// openAIModelRoutingPreference is a soft tier above the normal account
// priority. It combines the existing group-scoped routing with the global
// model priority panel. An empty preference leaves scheduling unchanged.
type openAIModelRoutingPreference struct {
	accountIDs map[int64]struct{}
	poolIDs    map[string]struct{}
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

func (p openAIModelRoutingPreference) withAccountIDSet(accountIDs map[int64]struct{}) openAIModelRoutingPreference {
	if len(accountIDs) == 0 {
		return p
	}
	if len(p.accountIDs) == 0 {
		p.accountIDs = accountIDs
		return p
	}
	merged := make(map[int64]struct{}, len(p.accountIDs)+len(accountIDs))
	for accountID := range p.accountIDs {
		merged[accountID] = struct{}{}
	}
	for accountID := range accountIDs {
		merged[accountID] = struct{}{}
	}
	p.accountIDs = merged
	return p
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
		preference = preference.withAccountIDSet(s.settingService.ResolveOpenAIModelPriorityAccountIDs(ctx, requestedModel))
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
	return groupPreference
}

func partitionOpenAIAccountsByModelPreference(
	accounts []*Account,
	preference openAIModelRoutingPreference,
) ([]*Account, []*Account) {
	if !preference.configured() {
		return nil, accounts
	}
	preferred := make([]*Account, 0, len(accounts))
	fallback := make([]*Account, 0, len(accounts))
	for _, account := range accounts {
		if preference.matches(account) {
			preferred = append(preferred, account)
		} else {
			fallback = append(fallback, account)
		}
	}
	return preferred, fallback
}

func modelRoutingPreferenceFirst(
	preference openAIModelRoutingPreference,
	left *Account,
	right *Account,
) (bool, bool) {
	if !preference.configured() {
		return false, false
	}
	leftPreferred := preference.matches(left)
	rightPreferred := preference.matches(right)
	if leftPreferred == rightPreferred {
		return false, false
	}
	return leftPreferred, true
}

func sortOpenAIAccountsByModelPreference(
	accounts []*Account,
	preference openAIModelRoutingPreference,
) {
	preferred, fallback := partitionOpenAIAccountsByModelPreference(accounts, preference)
	if len(preferred) == 0 {
		sortAccountsByPriorityAndLastUsed(accounts, false)
		return
	}
	sortAccountsByPriorityAndLastUsed(preferred, false)
	sortAccountsByPriorityAndLastUsed(fallback, false)
	copy(accounts, append(preferred, fallback...))
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
	preferred := make([]T, 0, len(items))
	fallback := make([]T, 0, len(items))
	for _, item := range items {
		if preference.matches(accountOf(item)) {
			preferred = append(preferred, item)
		} else {
			fallback = append(fallback, item)
		}
	}
	ordered := orderExecutionNodeCandidatesWithinPriorities(
		preferred,
		accountOf,
		func(item T) int { return openAIAccountSchedulingPriority(accountOf(item)) },
		policy,
		anchor,
	)
	return append(ordered, orderExecutionNodeCandidatesWithinPriorities(
		fallback,
		accountOf,
		func(item T) int { return openAIAccountSchedulingPriority(accountOf(item)) },
		policy,
		anchor,
	)...)
}
