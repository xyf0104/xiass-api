package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"log/slog"
	"math"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const (
	executionNodeRoutingCacheKey      = "execution_node_routing"
	executionNodeRoutingCacheTTL      = 3 * time.Second
	executionNodeRoutingErrorCacheTTL = time.Second
	executionNodeRoutingDBTimeout     = 500 * time.Millisecond
	maxExecutionNodeCount             = 32
	maxExecutionNodeWeight            = 1_000_000.0
)

var executionNodeSelectionSequence atomic.Uint64

// ExecutionNodeRoutingSettings is shared by every XIASS instance through the
// settings table. The node identity remains local deployment configuration;
// the proxy map is shared so all replicas can validate durable account egress.
type ExecutionNodeRoutingSettings struct {
	// Available distinguishes a successful read of the shared policy from a
	// local/default value. A configured multi-node instance must fail closed when
	// the shared policy cannot be read for the first time.
	Available bool               `json:"-"`
	Enabled   bool               `json:"enabled"`
	Weights   map[string]float64 `json:"weights"`
	ProxyIDs  map[string]int64   `json:"proxy_ids"`
	Healthy   map[string]bool    `json:"-"`
}

type cachedExecutionNodeRoutingSettings struct {
	settings  ExecutionNodeRoutingSettings
	expiresAt int64
}

func defaultExecutionNodeWeights() map[string]float64 {
	return map[string]float64{"api": 9, "api2": 1}
}

func cloneExecutionNodeWeights(weights map[string]float64) map[string]float64 {
	if len(weights) == 0 {
		return defaultExecutionNodeWeights()
	}
	cloned := make(map[string]float64, len(weights))
	for nodeID, weight := range weights {
		cloned[nodeID] = weight
	}
	return cloned
}

func cloneExecutionNodeProxyIDs(proxyIDs map[string]int64) map[string]int64 {
	if len(proxyIDs) == 0 {
		return map[string]int64{}
	}
	cloned := make(map[string]int64, len(proxyIDs))
	for nodeID, proxyID := range proxyIDs {
		cloned[nodeID] = proxyID
	}
	return cloned
}

func validExecutionNodeID(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 64 {
		return false
	}
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') || r == '.' || r == '_' || r == '-' {
			continue
		}
		return false
	}
	return true
}

func normalizeExecutionNodeWeights(weights map[string]float64) (map[string]float64, error) {
	if weights == nil {
		return defaultExecutionNodeWeights(), nil
	}
	if len(weights) == 0 {
		return nil, infraerrors.BadRequest("INVALID_EXECUTION_NODE_WEIGHTS", "execution node weights must contain between 1 and 32 nodes")
	}
	if len(weights) > maxExecutionNodeCount {
		return nil, infraerrors.BadRequest("INVALID_EXECUTION_NODE_WEIGHTS", "execution node weights must contain at most 32 nodes")
	}

	normalized := make(map[string]float64, len(weights))
	hasPositive := false
	for rawNodeID, weight := range weights {
		nodeID := strings.TrimSpace(rawNodeID)
		if !validExecutionNodeID(nodeID) {
			return nil, infraerrors.BadRequest("INVALID_EXECUTION_NODE_WEIGHTS", "execution node IDs may contain only letters, numbers, dots, underscores, or hyphens")
		}
		if math.IsNaN(weight) || math.IsInf(weight, 0) || weight < 0 || weight > maxExecutionNodeWeight {
			return nil, infraerrors.BadRequest("INVALID_EXECUTION_NODE_WEIGHTS", "execution node weights must be finite numbers between 0 and 1000000")
		}
		if _, exists := normalized[nodeID]; exists {
			return nil, infraerrors.BadRequest("INVALID_EXECUTION_NODE_WEIGHTS", "execution node IDs must be unique after trimming")
		}
		normalized[nodeID] = weight
		hasPositive = hasPositive || weight > 0
	}
	if !hasPositive {
		return nil, infraerrors.BadRequest("INVALID_EXECUTION_NODE_WEIGHTS", "at least one execution node weight must be greater than zero")
	}
	return normalized, nil
}

func normalizeExecutionNodeProxyIDs(proxyIDs map[string]int64) (map[string]int64, error) {
	if proxyIDs == nil {
		return map[string]int64{}, nil
	}
	if len(proxyIDs) > maxExecutionNodeCount {
		return nil, infraerrors.BadRequest("INVALID_EXECUTION_NODE_PROXY_IDS", "execution node proxy mapping must contain at most 32 nodes")
	}
	normalized := make(map[string]int64, len(proxyIDs))
	seenProxyIDs := make(map[int64]string, len(proxyIDs))
	for rawNodeID, proxyID := range proxyIDs {
		nodeID := strings.TrimSpace(rawNodeID)
		if !validExecutionNodeID(nodeID) {
			return nil, infraerrors.BadRequest("INVALID_EXECUTION_NODE_PROXY_IDS", "execution node IDs may contain only letters, numbers, dots, underscores, or hyphens")
		}
		if proxyID <= 0 {
			return nil, infraerrors.BadRequest("INVALID_EXECUTION_NODE_PROXY_IDS", "execution node proxy IDs must be positive integers")
		}
		if _, exists := normalized[nodeID]; exists {
			return nil, infraerrors.BadRequest("INVALID_EXECUTION_NODE_PROXY_IDS", "execution node IDs must be unique after trimming")
		}
		if previousNodeID, exists := seenProxyIDs[proxyID]; exists {
			return nil, infraerrors.BadRequest("INVALID_EXECUTION_NODE_PROXY_IDS", fmt.Sprintf("proxy %d cannot be assigned to both node %s and node %s", proxyID, previousNodeID, nodeID))
		}
		normalized[nodeID] = proxyID
		seenProxyIDs[proxyID] = nodeID
	}
	return normalized, nil
}

func parseExecutionNodeProxyIDs(raw string) map[string]int64 {
	if strings.TrimSpace(raw) == "" {
		return map[string]int64{}
	}
	proxyIDs, err := decodeExecutionNodeProxyIDs(raw)
	if err != nil {
		slog.Warn("parse execution node proxy IDs failed; using empty mapping", "error", err)
		return map[string]int64{}
	}
	return proxyIDs
}

func decodeExecutionNodeProxyIDs(raw string) (map[string]int64, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, errors.New("execution node proxy mapping is empty")
	}
	var proxyIDs map[string]int64
	if err := json.Unmarshal([]byte(raw), &proxyIDs); err != nil {
		return nil, err
	}
	return normalizeExecutionNodeProxyIDs(proxyIDs)
}

func validateExecutionNodeProxyIDs(proxyIDs map[string]int64, weights map[string]float64) error {
	seenProxyIDs := make(map[int64]string, len(proxyIDs))
	for nodeID := range weights {
		if proxyIDs[nodeID] <= 0 {
			return infraerrors.BadRequest("EXECUTION_NODE_PROXY_MAPPING_INCOMPLETE", fmt.Sprintf("execution node %s must have a private egress proxy ID", nodeID))
		}
	}
	for nodeID := range proxyIDs {
		if _, exists := weights[nodeID]; !exists {
			return infraerrors.BadRequest("EXECUTION_NODE_PROXY_MAPPING_UNKNOWN_NODE", fmt.Sprintf("proxy mapping contains unknown execution node %s", nodeID))
		}
		if previousNodeID, exists := seenProxyIDs[proxyIDs[nodeID]]; exists {
			return infraerrors.BadRequest("EXECUTION_NODE_PROXY_MAPPING_DUPLICATE_PROXY", fmt.Sprintf("proxy %d cannot be assigned to both node %s and node %s", proxyIDs[nodeID], previousNodeID, nodeID))
		}
		seenProxyIDs[proxyIDs[nodeID]] = nodeID
	}
	return nil
}

func parseExecutionNodeWeights(raw string) map[string]float64 {
	if strings.TrimSpace(raw) == "" {
		return defaultExecutionNodeWeights()
	}
	weights, err := decodeExecutionNodeWeights(raw)
	if err != nil {
		slog.Warn("parse execution node weights failed; using defaults", "error", err)
		return defaultExecutionNodeWeights()
	}
	return weights
}

func decodeExecutionNodeWeights(raw string) (map[string]float64, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, errors.New("execution node weights are empty")
	}
	var weights map[string]float64
	if err := json.Unmarshal([]byte(raw), &weights); err != nil {
		return nil, err
	}
	return normalizeExecutionNodeWeights(weights)
}

// GetExecutionNodeRoutingSettings is hot-path safe. The first read and each
// expired-cache refresh load shared settings with a short timeout. A successful
// refresh must take effect before admitting another new session; otherwise a
// zero-weight drain could still leak one placement per application instance.
// Read failures retain the last known policy for a short retry interval.
// Single-node installations never query storage.
func (s *SettingService) GetExecutionNodeRoutingSettings(ctx context.Context) ExecutionNodeRoutingSettings {
	defaults := ExecutionNodeRoutingSettings{Available: true, Weights: defaultExecutionNodeWeights()}
	if s == nil || s.cfg == nil || !s.cfg.Gateway.ExecutionNode.Enabled {
		return defaults
	}
	if s.settingRepo == nil {
		return ExecutionNodeRoutingSettings{Available: false}
	}

	now := time.Now().UnixNano()
	if cached, _ := s.executionNodeRoutingCache.Load().(*cachedExecutionNodeRoutingSettings); cached != nil {
		if now < cached.expiresAt {
			return cloneExecutionNodeRoutingSettings(cached.settings)
		}
		loaded, _, _ := s.executionNodeRoutingSF.Do(executionNodeRoutingCacheKey, func() (any, error) {
			return s.loadExecutionNodeRoutingSettings(ctx, cached)
		})
		if refreshed, ok := loaded.(*cachedExecutionNodeRoutingSettings); ok && refreshed != nil {
			return cloneExecutionNodeRoutingSettings(refreshed.settings)
		}
		return cloneExecutionNodeRoutingSettings(cached.settings)
	}

	loaded, _, _ := s.executionNodeRoutingSF.Do(executionNodeRoutingCacheKey, func() (any, error) {
		return s.loadExecutionNodeRoutingSettings(ctx, nil)
	})
	if cached, ok := loaded.(*cachedExecutionNodeRoutingSettings); ok && cached != nil {
		return cloneExecutionNodeRoutingSettings(cached.settings)
	}
	return defaults
}

func cloneExecutionNodeRoutingSettings(settings ExecutionNodeRoutingSettings) ExecutionNodeRoutingSettings {
	settings.Weights = cloneExecutionNodeWeights(settings.Weights)
	settings.ProxyIDs = cloneExecutionNodeProxyIDs(settings.ProxyIDs)
	if len(settings.Healthy) > 0 {
		healthyNodes := make(map[string]bool, len(settings.Healthy))
		for nodeID, healthy := range settings.Healthy {
			healthyNodes[nodeID] = healthy
		}
		settings.Healthy = healthyNodes
	}
	return settings
}

func (s *SettingService) loadExecutionNodeRoutingSettings(ctx context.Context, prior *cachedExecutionNodeRoutingSettings) (*cachedExecutionNodeRoutingSettings, error) {
	baseCtx := ctx
	if baseCtx == nil {
		baseCtx = context.Background()
	}
	dbCtx, cancel := context.WithTimeout(context.WithoutCancel(baseCtx), executionNodeRoutingDBTimeout)
	defer cancel()

	localNodeID := strings.TrimSpace(s.cfg.Gateway.ExecutionNode.ID)
	settingKeys := []string{
		SettingKeyExecutionNodeBalancingEnabled,
		SettingKeyExecutionNodeWeights,
		SettingKeyExecutionNodeProxyIDs,
	}
	values, err := s.settingRepo.GetMultiple(dbCtx, settingKeys)
	if err != nil {
		settings := ExecutionNodeRoutingSettings{Available: false}
		if prior != nil {
			settings = cloneExecutionNodeRoutingSettings(prior.settings)
		}
		entry := &cachedExecutionNodeRoutingSettings{
			settings:  settings,
			expiresAt: time.Now().Add(executionNodeRoutingErrorCacheTTL).UnixNano(),
		}
		s.executionNodeRoutingCache.Store(entry)
		return entry, err
	}

	enabled := strings.EqualFold(strings.TrimSpace(values[SettingKeyExecutionNodeBalancingEnabled]), "true")
	weights := parseExecutionNodeWeights(values[SettingKeyExecutionNodeWeights])
	proxyIDs := parseExecutionNodeProxyIDs(values[SettingKeyExecutionNodeProxyIDs])
	if enabled {
		var validationErr error
		weights, validationErr = decodeExecutionNodeWeights(values[SettingKeyExecutionNodeWeights])
		if validationErr == nil {
			proxyIDs, validationErr = decodeExecutionNodeProxyIDs(values[SettingKeyExecutionNodeProxyIDs])
		}
		if validationErr == nil {
			validationErr = validateExecutionNodeProxyIDs(proxyIDs, weights)
		}
		if validationErr != nil {
			slog.Error("active execution node routing policy is invalid; failing closed", "error", validationErr)
			entry := &cachedExecutionNodeRoutingSettings{
				settings:  ExecutionNodeRoutingSettings{Available: false},
				expiresAt: time.Now().Add(executionNodeRoutingErrorCacheTTL).UnixNano(),
			}
			s.executionNodeRoutingCache.Store(entry)
			return entry, validationErr
		}

		nodeIDs := make([]string, 0, len(weights))
		for nodeID := range weights {
			nodeIDs = append(nodeIDs, nodeID)
		}
		sort.Strings(nodeIDs)
		healthy := make(map[string]bool, len(nodeIDs))
		if s.executionNodeHealthReader == nil {
			// Constructor-level unit tests and alternate embeddings do not wire the
			// production heartbeat service. Preserve their deterministic behavior.
			for _, nodeID := range nodeIDs {
				healthy[nodeID] = true
			}
		} else {
			healthy, validationErr = s.executionNodeHealthReader.HealthyExecutionNodes(dbCtx, nodeIDs)
		}
		if validationErr != nil {
			entry := &cachedExecutionNodeRoutingSettings{
				settings:  ExecutionNodeRoutingSettings{Available: false},
				expiresAt: time.Now().Add(executionNodeRoutingErrorCacheTTL).UnixNano(),
			}
			s.executionNodeRoutingCache.Store(entry)
			return entry, validationErr
		}
		// The process serving this request is necessarily alive. Marking its own
		// heartbeat healthy avoids a startup race before the first Redis SET.
		healthy[localNodeID] = true
		entry := &cachedExecutionNodeRoutingSettings{
			settings: ExecutionNodeRoutingSettings{
				Available: true,
				Enabled:   true,
				Weights:   weights,
				ProxyIDs:  proxyIDs,
				Healthy:   healthy,
			},
			expiresAt: time.Now().Add(executionNodeRoutingCacheTTL).UnixNano(),
		}
		s.executionNodeRoutingCache.Store(entry)
		return entry, nil
	}

	entry := &cachedExecutionNodeRoutingSettings{
		settings: ExecutionNodeRoutingSettings{
			Available: true,
			Enabled:   enabled,
			Weights:   weights,
			ProxyIDs:  proxyIDs,
		},
		expiresAt: time.Now().Add(executionNodeRoutingCacheTTL).UnixNano(),
	}
	s.executionNodeRoutingCache.Store(entry)
	return entry, nil
}

type executionNodeRoutingPolicy struct {
	enabled      bool
	unavailable  bool
	legacyNodeID string
	localNodeID  string
	weights      map[string]float64
	proxyIDs     map[string]int64
	healthy      map[string]bool
}

func resolveExecutionNodeRoutingPolicy(ctx context.Context, cfg *config.Config, settingService *SettingService) executionNodeRoutingPolicy {
	if cfg == nil || !cfg.Gateway.ExecutionNode.Enabled {
		return executionNodeRoutingPolicy{}
	}
	legacyNodeID := strings.TrimSpace(cfg.Gateway.ExecutionNode.LegacyUnassignedNodeID)
	if legacyNodeID == "" {
		legacyNodeID = "api"
	}
	if settingService == nil {
		return executionNodeRoutingPolicy{
			enabled:      true,
			unavailable:  true,
			legacyNodeID: legacyNodeID,
		}
	}
	settings := settingService.GetExecutionNodeRoutingSettings(ctx)
	if !settings.Available {
		// Do not silently fall back to single-node selection while the shared
		// policy is unknown. Callers will see an empty candidate set and return a
		// normal unavailable-account response until the policy can be read.
		return executionNodeRoutingPolicy{
			enabled:      true,
			unavailable:  true,
			legacyNodeID: legacyNodeID,
		}
	}
	if !settings.Enabled {
		return executionNodeRoutingPolicy{}
	}
	return executionNodeRoutingPolicy{
		enabled:      true,
		legacyNodeID: legacyNodeID,
		localNodeID:  strings.TrimSpace(cfg.Gateway.ExecutionNode.ID),
		weights:      cloneExecutionNodeWeights(settings.Weights),
		proxyIDs:     cloneExecutionNodeProxyIDs(settings.ProxyIDs),
		healthy:      settings.Healthy,
	}
}

func (p executionNodeRoutingPolicy) nodeID(account *Account) string {
	if account == nil {
		return p.legacyNodeID
	}
	return account.ExecutionNodeID(p.legacyNodeID)
}

func (p executionNodeRoutingPolicy) weight(nodeID string) float64 {
	if !p.enabled {
		return 1
	}
	if p.unavailable {
		return 0
	}
	if weight, ok := p.weights[nodeID]; ok {
		return weight
	}
	// Unknown owners fail closed. A newly added node becomes schedulable only
	// after an administrator explicitly adds its weight to the shared policy.
	return 0
}

func (p executionNodeRoutingPolicy) accountEgressIDAllowed(account *Account) bool {
	if !p.enabled {
		return true
	}
	if p.unavailable || account == nil || account.ProxyID == nil || *account.ProxyID <= 0 {
		return false
	}
	expectedProxyID, ok := p.proxyIDs[p.nodeID(account)]
	return ok && expectedProxyID > 0 && (*account.ProxyID == expectedProxyID || account.hasExplicitExecutionProxy())
}

func (p executionNodeRoutingPolicy) nodeHealthy(nodeID string) bool {
	if !p.enabled {
		return true
	}
	return p.healthy[strings.TrimSpace(nodeID)]
}

func (p executionNodeRoutingPolicy) hydratedAccountEgressAllowed(account *Account) bool {
	if !p.accountEgressIDAllowed(account) {
		return false
	}
	if !p.enabled {
		return true
	}
	if !p.nodeHealthy(p.nodeID(account)) {
		return false
	}
	return account.Proxy != nil &&
		account.Proxy.ID == *account.ProxyID &&
		account.Proxy.IsActive() &&
		!account.Proxy.IsExpired(time.Now())
}

func (p executionNodeRoutingPolicy) candidateAccountEgressAllowed(account *Account) bool {
	if !p.accountEgressIDAllowed(account) {
		return false
	}
	if !p.enabled {
		return true
	}
	// Multi-node scheduler snapshots carry a credential-free proxy summary.
	// Requiring it here prevents selecting an account whose fixed node egress was
	// disabled or expired and then failing only after a concurrency slot was won.
	return p.hydratedAccountEgressAllowed(account)
}

func filterExecutionNodeCandidates[T any](items []T, accountOf func(T) *Account, policy executionNodeRoutingPolicy) []T {
	if !policy.enabled || len(items) == 0 {
		return items
	}
	filtered := make([]T, 0, len(items))
	for _, item := range items {
		account := accountOf(item)
		if policy.candidateAccountEgressAllowed(account) && policy.weight(policy.nodeID(account)) > 0 {
			filtered = append(filtered, item)
		}
	}
	return filtered
}

// executionNodeCandidateAllowed admits accounts on active nodes and, for an
// established request, the exact account IDs already bound to that request.
// A bound-account exemption must never reopen sibling accounts on the same
// drained node.
func executionNodeCandidateAllowed(policy executionNodeRoutingPolicy, account *Account, boundAccountIDs ...int64) bool {
	if !policy.enabled {
		return true
	}
	if !policy.candidateAccountEgressAllowed(account) {
		return false
	}
	if account != nil {
		for _, accountID := range boundAccountIDs {
			if accountID > 0 && account.ID == accountID {
				return true
			}
		}
	}
	return policy.weight(policy.nodeID(account)) > 0
}

// orderExecutionNodeCandidates groups an already ranked candidate list by a
// weighted node permutation while preserving the original ranking inside each
// node. It is used only for new/non-sticky work.
func orderExecutionNodeCandidates[T any](items []T, accountOf func(T) *Account, policy executionNodeRoutingPolicy, anchor string) []T {
	if !policy.enabled {
		return items
	}
	groups := make(map[string][]T)
	for _, item := range items {
		account := accountOf(item)
		nodeID := policy.nodeID(account)
		if !policy.candidateAccountEgressAllowed(account) || policy.weight(nodeID) <= 0 {
			continue
		}
		groups[nodeID] = append(groups[nodeID], item)
	}
	if len(groups) <= 1 {
		for _, group := range groups {
			return group
		}
		return nil
	}

	nodes := make([]string, 0, len(groups))
	for nodeID := range groups {
		nodes = append(nodes, nodeID)
	}
	sort.Strings(nodes)
	nodes = weightedExecutionNodePermutation(nodes, policy, anchor)

	ordered := make([]T, 0, len(items))
	for _, nodeID := range nodes {
		ordered = append(ordered, groups[nodeID]...)
	}
	return ordered
}

func partitionExecutionNodeCandidates[T any](items []T, accountOf func(T) *Account, policy executionNodeRoutingPolicy, anchor string) [][]T {
	if len(items) == 0 {
		return nil
	}
	if !policy.enabled {
		return [][]T{items}
	}
	groups := make(map[string][]T)
	for _, item := range items {
		account := accountOf(item)
		nodeID := policy.nodeID(account)
		if !policy.candidateAccountEgressAllowed(account) || policy.weight(nodeID) <= 0 {
			continue
		}
		groups[nodeID] = append(groups[nodeID], item)
	}
	if len(groups) == 0 {
		return nil
	}
	nodes := make([]string, 0, len(groups))
	for nodeID := range groups {
		nodes = append(nodes, nodeID)
	}
	sort.Strings(nodes)
	nodes = weightedExecutionNodePermutation(nodes, policy, anchor)
	partitions := make([][]T, 0, len(nodes))
	for _, nodeID := range nodes {
		partitions = append(partitions, groups[nodeID])
	}
	return partitions
}

func firstExecutionNodeCandidateGroup[T any](items []T, accountOf func(T) *Account, policy executionNodeRoutingPolicy, anchor string) []T {
	partitions := partitionExecutionNodeCandidates(items, accountOf, policy, anchor)
	if len(partitions) == 0 {
		return nil
	}
	return partitions[0]
}

func orderExecutionNodeCandidatesWithinPriorities[T any](items []T, accountOf func(T) *Account, priorityOf func(T) int, policy executionNodeRoutingPolicy, anchor string) []T {
	if !policy.enabled {
		return items
	}
	ordered := make([]T, 0, len(items))
	for start := 0; start < len(items); {
		end := start + 1
		priority := priorityOf(items[start])
		for end < len(items) && priorityOf(items[end]) == priority {
			end++
		}
		ordered = append(ordered, orderExecutionNodeCandidates(items[start:end], accountOf, policy, anchor)...)
		start = end
	}
	return ordered
}

func weightedExecutionNodePermutation(nodes []string, policy executionNodeRoutingPolicy, anchor string) []string {
	remaining := append([]string(nil), nodes...)
	weights := make([]float64, len(remaining))
	for i, nodeID := range remaining {
		weights[i] = policy.weight(nodeID)
	}
	seed := executionNodeSelectionSeed(anchor)
	rng := newOpenAISelectionRNG(seed)
	ordered := make([]string, 0, len(remaining))
	for len(remaining) > 0 {
		total := 0.0
		for _, weight := range weights {
			total += weight
		}
		if total <= 0 {
			break
		}
		selected := 0
		point := rng.nextFloat64() * total
		cumulative := 0.0
		for i, weight := range weights {
			cumulative += weight
			if point < cumulative {
				selected = i
				break
			}
		}
		ordered = append(ordered, remaining[selected])
		remaining = append(remaining[:selected], remaining[selected+1:]...)
		weights = append(weights[:selected], weights[selected+1:]...)
	}
	return ordered
}

func executionNodeSelectionSeed(anchor string) uint64 {
	hasher := fnv.New64a()
	anchor = strings.TrimSpace(anchor)
	if anchor != "" {
		_, _ = hasher.Write([]byte(anchor))
		if seed := hasher.Sum64(); seed != 0 {
			return seed
		}
	}
	sequence := executionNodeSelectionSequence.Add(1)
	seed := uint64(time.Now().UnixNano()) ^ (sequence * 0x9e3779b97f4a7c15)
	if seed == 0 {
		return 0x9e3779b97f4a7c15
	}
	return seed
}

func executionNodeSelectionAnchor(parts ...string) string {
	var builder strings.Builder
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if builder.Len() > 0 {
			_ = builder.WriteByte('|')
		}
		_, _ = builder.WriteString(part)
	}
	return builder.String()
}
