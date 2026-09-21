package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
)

const (
	accountProxyAdaptiveStatsTTL   = 30 * time.Minute
	accountProxyAdaptiveProbeTTL   = 2 * time.Minute
	accountProxyAdaptiveStaleAfter = 10 * time.Minute
	accountProxyAdaptiveMinSamples = 2
)

type AccountProxyAdaptiveRouteKey struct {
	ExecutionNode string
	AccountID     int64
	ModelHash     string
	ProxyHash     string
}

func (k AccountProxyAdaptiveRouteKey) CacheID() string {
	return k.ExecutionNode + ":" + fmt.Sprintf("%d", k.AccountID) + ":" + k.ModelHash + ":" + k.ProxyHash
}

type AccountProxyAdaptiveRouteStats struct {
	Successes        int64
	TransientErrors  int64
	ExactModels      int64
	MismatchedModels int64
	RecentErrorRate  float64
	RecentModelScore float64
	LastModelKnown   bool
	EWMATTFTMs       float64
	EWMATTFTVariance float64
	LastObservedAt   time.Time
}

func (s AccountProxyAdaptiveRouteStats) Samples() int64 {
	return s.Successes + s.TransientErrors
}

type AccountProxyAdaptiveRouteOutcome struct {
	Route            AccountProxyAdaptiveRouteKey
	Success          bool
	TransientFailure bool
	ModelKnown       bool
	ModelExact       bool
	FirstTokenMs     *int
	TTL              time.Duration
}

type accountProxyAdaptiveRank struct {
	index         int
	seedPriority  int
	modelClass    int
	errorBucket   int
	latencyBucket int
	probe         bool
}

func (s *ConcurrencyService) rankAccountProxySlots(ctx context.Context, account *Account, bindings []AccountProxyBinding, slots []AccountProxySlotSpec) []AccountProxySlotSpec {
	cache, ok := s.cache.(AccountProxyAdaptiveRouteCache)
	if !ok || account == nil || len(bindings) < 2 || len(bindings) != len(slots) {
		return slots
	}
	model, _ := ctx.Value(ctxkey.Model).(string)
	model = strings.TrimSpace(model)
	if model == "" {
		return slots
	}
	routes := make([]AccountProxyAdaptiveRouteKey, len(bindings))
	for i := range bindings {
		routes[i] = s.accountProxyAdaptiveRouteKey(account, model, bindings[i].Proxy)
	}
	stats, err := cache.GetAccountProxyAdaptiveRouteStats(ctx, routes)
	if err != nil {
		return slots
	}

	now := time.Now()
	hasExact := false
	for _, route := range routes {
		st := stats[route.CacheID()]
		if st.LastModelKnown && st.RecentModelScore > 0.2 {
			hasExact = true
			break
		}
	}
	ranks := make([]accountProxyAdaptiveRank, 0, len(slots))
	probeCandidate := -1
	oldestCandidate := now
	for i := range slots {
		st := stats[routes[i].CacheID()]
		modelClass := 1
		if hasExact {
			switch {
			case st.LastModelKnown && st.RecentModelScore > 0.2:
				modelClass = 0
			case st.LastModelKnown && st.RecentModelScore < -0.2:
				modelClass = 2
			}
		}
		samples := st.Samples()
		errorRate := st.RecentErrorRate
		errorBucket := int(math.Round(errorRate * 20))
		latencyBucket := 10
		if st.EWMATTFTMs > 0 {
			stableTTFT := st.EWMATTFTMs + math.Sqrt(math.Max(st.EWMATTFTVariance, 0))
			latencyBucket = int(stableTTFT / 100)
		}
		poor := samples >= accountProxyAdaptiveMinSamples && (errorRate >= 0.5 || modelClass == 2)
		stale := !st.LastObservedAt.IsZero() && now.Sub(st.LastObservedAt) >= accountProxyAdaptiveStaleAfter
		if poor || stale {
			observedAt := st.LastObservedAt
			if observedAt.IsZero() {
				observedAt = time.Unix(0, 0)
			}
			if probeCandidate < 0 || observedAt.Before(oldestCandidate) {
				probeCandidate = i
				oldestCandidate = observedAt
			}
		}
		ranks = append(ranks, accountProxyAdaptiveRank{
			index: i, seedPriority: slots[i].RoutePriority, modelClass: modelClass,
			errorBucket: errorBucket, latencyBucket: latencyBucket,
		})
	}
	if probeCandidate >= 0 {
		if claimed, _ := cache.ClaimAccountProxyAdaptiveRouteProbe(ctx, routes[probeCandidate], accountProxyAdaptiveProbeTTL); claimed {
			ranks[probeCandidate].probe = true
		}
	}
	sort.SliceStable(ranks, func(i, j int) bool {
		a, b := ranks[i], ranks[j]
		if a.probe != b.probe {
			return a.probe
		}
		if a.modelClass != b.modelClass {
			return a.modelClass < b.modelClass
		}
		if a.errorBucket != b.errorBucket {
			return a.errorBucket < b.errorBucket
		}
		if a.latencyBucket != b.latencyBucket {
			return a.latencyBucket < b.latencyBucket
		}
		return adaptiveSeedPriority(a.seedPriority) < adaptiveSeedPriority(b.seedPriority)
	})
	result := append([]AccountProxySlotSpec(nil), slots...)
	priority := 1
	for pos, rank := range ranks {
		if pos > 0 && !sameAdaptiveRank(rank, ranks[pos-1]) {
			priority++
		}
		result[rank.index].RoutePriority = priority
	}
	return result
}

func sameAdaptiveRank(a, b accountProxyAdaptiveRank) bool {
	return a.probe == b.probe && a.modelClass == b.modelClass && a.errorBucket == b.errorBucket &&
		a.latencyBucket == b.latencyBucket && adaptiveSeedPriority(a.seedPriority) == adaptiveSeedPriority(b.seedPriority)
}

func adaptiveSeedPriority(priority int) int {
	if priority > 0 {
		return priority
	}
	return maxAccountProxyRoutePriority + 1
}

func (s *ConcurrencyService) accountProxyAdaptiveRouteKey(account *Account, model string, proxy *Proxy) AccountProxyAdaptiveRouteKey {
	node := "default"
	if account != nil {
		if value := strings.TrimSpace(account.ExecutionNodeID("")); value != "" {
			node = value
		} else if s != nil && s.executionNodeID != "" {
			node = s.executionNodeID
		}
	}
	proxyIdentity := "none"
	if proxy != nil {
		proxyIdentity = fmt.Sprintf("%d:%d", proxy.ID, proxy.UpdatedAt.UnixNano())
	}
	return AccountProxyAdaptiveRouteKey{
		ExecutionNode: shortAdaptiveHash(node), AccountID: account.ID,
		ModelHash: shortAdaptiveHash(strings.TrimSpace(model)), ProxyHash: shortAdaptiveHash(proxyIdentity),
	}
}

func shortAdaptiveHash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:12])
}

func (s *ConcurrencyService) ReportAccountProxyAdaptiveRouteOutcome(ctx context.Context, account *Account, model string, success, transientFailure bool, firstTokenMs *int, upstreamResponseModel string) {
	if s == nil || account == nil || account.RequestProxy == nil || !account.MultiProxyConfigured || !AccountMultiProxyAdaptiveEnabled(account.Extra) {
		return
	}
	cache, ok := s.cache.(AccountProxyAdaptiveRouteCache)
	if !ok || strings.TrimSpace(model) == "" || (!success && !transientFailure) {
		return
	}
	expectedModel := strings.TrimSpace(model)
	if contextModel, _ := ctx.Value(ctxkey.Model).(string); strings.TrimSpace(contextModel) != "" {
		model = strings.TrimSpace(contextModel)
	}
	actual := strings.TrimSpace(upstreamResponseModel)
	outcome := AccountProxyAdaptiveRouteOutcome{
		Route: s.accountProxyAdaptiveRouteKey(account, model, account.RequestProxy), Success: success,
		TransientFailure: transientFailure, ModelKnown: actual != "", ModelExact: actual != "" && actual == expectedModel,
		FirstTokenMs: firstTokenMs, TTL: accountProxyAdaptiveStatsTTL,
	}
	reportCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
	defer cancel()
	_ = cache.RecordAccountProxyAdaptiveRouteOutcome(reportCtx, outcome)
}

func (s *OpenAIGatewayService) ReportOpenAIAccountProxyRouteOutcome(ctx context.Context, account *Account, model string, success, transientFailure bool, result *OpenAIForwardResult) {
	if s == nil || s.concurrencyService == nil {
		return
	}
	if ctx == nil || ctx.Err() != nil || (result != nil && result.ClientDisconnect) {
		return
	}
	var firstTokenMs *int
	var upstreamResponseModel string
	if result != nil {
		firstTokenMs = result.FirstTokenMs
		if !result.UpstreamResponseModelConflict {
			upstreamResponseModel = result.UpstreamResponseModel
		}
	}
	if !success {
		transientFailure = true
	}
	if success && (result == nil || !result.Stream || firstTokenMs == nil) {
		return
	}
	s.concurrencyService.ReportAccountProxyAdaptiveRouteOutcome(ctx, account, model, success, transientFailure, firstTokenMs, upstreamResponseModel)
}
