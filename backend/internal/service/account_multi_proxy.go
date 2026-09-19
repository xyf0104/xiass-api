package service

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"
)

const (
	AccountMultiProxyExtraKey = "xiass_multi_proxy"
	accountMultiProxyVersion  = 1
	maxAccountProxyBindings   = 128
	maxAccountProxyCapacity   = 10000
)

// AccountProxyBindingInput is the administrator-owned configuration for one
// egress. Proxy credentials remain in the shared proxies table.
type AccountProxyBindingInput struct {
	ProxyID        int64 `json:"proxy_id"`
	MaxConcurrency int   `json:"max_concurrency"`
}

// AccountProxyBinding is the hydrated runtime form. Proxy is internal and the
// DTO layer exposes only the redacted proxy object.
type AccountProxyBinding struct {
	ProxyID        int64  `json:"proxy_id"`
	MaxConcurrency int    `json:"max_concurrency"`
	Proxy          *Proxy `json:"proxy,omitempty"`
}

type preservedAccountProxyBindingsContextKey struct{}

// WithPreservedAccountProxyBindings allows trusted server-side copy, update,
// and restore flows to retain bindings that became unavailable after they were
// originally configured. It never permits missing proxies, and callers must
// pass only bindings already owned by the source account or backup record.
func WithPreservedAccountProxyBindings(ctx context.Context, bindings []AccountProxyBindingInput) context.Context {
	if ctx == nil || len(bindings) == 0 {
		return ctx
	}
	preserved := make(map[int64]struct{}, len(bindings))
	for _, binding := range bindings {
		if binding.ProxyID > 0 {
			preserved[binding.ProxyID] = struct{}{}
		}
	}
	if len(preserved) == 0 {
		return ctx
	}
	return context.WithValue(ctx, preservedAccountProxyBindingsContextKey{}, preserved)
}

func preservedAccountProxyBinding(ctx context.Context, proxyID int64) bool {
	if ctx == nil || proxyID <= 0 {
		return false
	}
	preserved, _ := ctx.Value(preservedAccountProxyBindingsContextKey{}).(map[int64]struct{})
	_, ok := preserved[proxyID]
	return ok
}

func normalizeAccountProxyBindingInputs(bindings []AccountProxyBindingInput) ([]AccountProxyBindingInput, int, error) {
	if len(bindings) == 0 {
		return nil, 0, nil
	}
	if len(bindings) > maxAccountProxyBindings {
		return nil, 0, fmt.Errorf("proxy_bindings supports at most %d proxies", maxAccountProxyBindings)
	}
	seen := make(map[int64]struct{}, len(bindings))
	normalized := make([]AccountProxyBindingInput, 0, len(bindings))
	total := 0
	for _, binding := range bindings {
		if binding.ProxyID <= 0 {
			return nil, 0, fmt.Errorf("proxy_id must be greater than 0")
		}
		if binding.MaxConcurrency <= 0 {
			return nil, 0, fmt.Errorf("max_concurrency must be greater than 0 for proxy %d", binding.ProxyID)
		}
		if _, exists := seen[binding.ProxyID]; exists {
			return nil, 0, fmt.Errorf("proxy %d is selected more than once", binding.ProxyID)
		}
		seen[binding.ProxyID] = struct{}{}
		total += binding.MaxConcurrency
		if total > maxAccountProxyCapacity {
			return nil, 0, fmt.Errorf("total proxy concurrency must be at most %d", maxAccountProxyCapacity)
		}
		normalized = append(normalized, binding)
	}
	sort.SliceStable(normalized, func(i, j int) bool { return normalized[i].ProxyID < normalized[j].ProxyID })
	return normalized, total, nil
}

func (s *adminServiceImpl) validateAccountProxyBindings(
	ctx context.Context,
	bindings []AccountProxyBindingInput,
) ([]AccountProxyBindingInput, int, map[int64]*Proxy, error) {
	normalized, total, err := normalizeAccountProxyBindingInputs(bindings)
	if err != nil {
		return nil, 0, nil, err
	}
	if len(normalized) == 0 {
		return nil, 0, map[int64]*Proxy{}, nil
	}
	if s == nil || s.proxyRepo == nil {
		return nil, 0, nil, fmt.Errorf("proxy repository is unavailable")
	}
	ids := make([]int64, 0, len(normalized))
	for _, binding := range normalized {
		ids = append(ids, binding.ProxyID)
	}
	proxies, err := s.proxyRepo.ListByIDs(ctx, ids)
	if err != nil {
		return nil, 0, nil, err
	}
	proxyMap := make(map[int64]*Proxy, len(proxies))
	now := time.Now()
	for i := range proxies {
		proxy := &proxies[i]
		proxyMap[proxy.ID] = proxy
	}
	for _, binding := range normalized {
		proxy := proxyMap[binding.ProxyID]
		if proxy == nil {
			return nil, 0, nil, fmt.Errorf("proxy %d does not exist", binding.ProxyID)
		}
		if !proxy.IsActive() && !preservedAccountProxyBinding(ctx, binding.ProxyID) {
			return nil, 0, nil, fmt.Errorf("proxy %d is inactive", binding.ProxyID)
		}
		if proxy.IsExpired(now) && !preservedAccountProxyBinding(ctx, binding.ProxyID) {
			return nil, 0, nil, fmt.Errorf("proxy %d is expired", binding.ProxyID)
		}
	}
	return normalized, total, proxyMap, nil
}

func setAccountProxyBindingsExtra(extra map[string]any, bindings []AccountProxyBindingInput) map[string]any {
	if extra == nil {
		extra = make(map[string]any)
	}
	delete(extra, AccountMultiProxyExtraKey)
	if len(bindings) == 0 {
		return extra
	}
	items := make([]any, 0, len(bindings))
	for _, binding := range bindings {
		items = append(items, map[string]any{
			"proxy_id":        binding.ProxyID,
			"max_concurrency": binding.MaxConcurrency,
		})
	}
	extra[AccountMultiProxyExtraKey] = map[string]any{
		"version":  accountMultiProxyVersion,
		"bindings": items,
	}
	return extra
}

func AccountProxyBindingsFromExtra(extra map[string]any) []AccountProxyBinding {
	if extra == nil {
		return nil
	}
	raw, ok := extra[AccountMultiProxyExtraKey].(map[string]any)
	if !ok {
		return nil
	}
	rawBindings := accountProxyBindingItems(raw["bindings"])
	inputs := make([]AccountProxyBindingInput, 0, len(rawBindings))
	for _, rawBinding := range rawBindings {
		item, ok := rawBinding.(map[string]any)
		if !ok {
			continue
		}
		proxyID, okID := accountProxyBindingNumber(item["proxy_id"])
		maxConcurrency, okConcurrency := accountProxyBindingNumber(item["max_concurrency"])
		if !okID || !okConcurrency {
			continue
		}
		inputs = append(inputs, AccountProxyBindingInput{ProxyID: proxyID, MaxConcurrency: int(maxConcurrency)})
	}
	normalized, _, err := normalizeAccountProxyBindingInputs(inputs)
	if err != nil {
		return nil
	}
	bindings := make([]AccountProxyBinding, 0, len(normalized))
	for _, binding := range normalized {
		bindings = append(bindings, AccountProxyBinding{
			ProxyID:        binding.ProxyID,
			MaxConcurrency: binding.MaxConcurrency,
		})
	}
	return bindings
}

func accountProxyBindingItems(value any) []any {
	switch typed := value.(type) {
	case []any:
		return typed
	case []map[string]any:
		items := make([]any, 0, len(typed))
		for _, item := range typed {
			items = append(items, item)
		}
		return items
	default:
		return nil
	}
}

func accountProxyBindingNumber(value any) (int64, bool) {
	switch typed := value.(type) {
	case int:
		return int64(typed), typed > 0
	case int64:
		return typed, typed > 0
	case float64:
		integer := int64(typed)
		return integer, typed == float64(integer) && integer > 0
	case json.Number:
		integer, err := typed.Int64()
		return integer, err == nil && integer > 0
	default:
		return 0, false
	}
}

func HydrateAccountProxyBindings(account *Account, proxies map[int64]*Proxy) {
	if account == nil {
		return
	}
	account.MultiProxyConfigured = accountMultiProxyConfiguredFromExtra(account.Extra)
	account.ProxyBindings = AccountProxyBindingsFromExtra(account.Extra)
	if !account.MultiProxyConfigured {
		return
	}
	for i := range account.ProxyBindings {
		binding := &account.ProxyBindings[i]
		proxy := proxies[binding.ProxyID]
		if proxy != nil {
			binding.Proxy = proxy
		}
	}
	// The account-level Redis slot is the aggregate of all currently usable
	// exits. An expired or disabled proxy stops contributing capacity without
	// changing the durable administrator configuration.
	account.Concurrency = account.MultiProxyConcurrency()
}

func accountMultiProxyConfiguredFromExtra(extra map[string]any) bool {
	if extra == nil {
		return false
	}
	raw, ok := extra[AccountMultiProxyExtraKey].(map[string]any)
	if !ok {
		return false
	}
	_, exists := raw["bindings"]
	return exists
}

func AccountProxyBindingInputsFromExtra(extra map[string]any) []AccountProxyBindingInput {
	bindings := AccountProxyBindingsFromExtra(extra)
	inputs := make([]AccountProxyBindingInput, 0, len(bindings))
	for _, binding := range bindings {
		inputs = append(inputs, AccountProxyBindingInput{
			ProxyID:        binding.ProxyID,
			MaxConcurrency: binding.MaxConcurrency,
		})
	}
	return inputs
}

func AccountProxyBindingIDsFromExtra(extra map[string]any) []int64 {
	bindings := AccountProxyBindingsFromExtra(extra)
	ids := make([]int64, 0, len(bindings))
	for _, binding := range bindings {
		ids = append(ids, binding.ProxyID)
	}
	return ids
}

func (a *Account) MultiProxyConcurrency() int {
	if a == nil {
		return 0
	}
	total := 0
	now := time.Now()
	for _, binding := range a.ProxyBindings {
		if binding.usable(now) {
			total += binding.MaxConcurrency
		}
	}
	return total
}

func (b AccountProxyBinding) usable(now time.Time) bool {
	return b.Proxy != nil && b.MaxConcurrency > 0 && b.Proxy.IsActive() && !b.Proxy.IsExpired(now)
}

func (a *Account) UsableProxyBindings() []AccountProxyBinding {
	if a == nil || !a.MultiProxyConfigured {
		return nil
	}
	now := time.Now()
	bindings := make([]AccountProxyBinding, 0, len(a.ProxyBindings))
	for _, binding := range a.ProxyBindings {
		if binding.usable(now) {
			bindings = append(bindings, binding)
		}
	}
	return bindings
}
