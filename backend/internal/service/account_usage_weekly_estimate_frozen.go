package service

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
)

const (
	// v15 stores raw completed endpoints and ordered terminal observations.
	openAIWeeklyFrozenEstimateStateVersion       = 15
	openAIWeeklyFrozenEstimatePreviousVersion    = 14
	openAIWeeklyFrozenEstimateLegacyStateVersion = 13
	openAIWeeklyEstimateModeLegacy               = "legacy_rule_b"
	openAIWeeklyEstimateModeJoinAverage          = "join_average"
)

// BaselineSource records evidence, not just the selected formula. A legacy
// compatibility state must not be interpreted as proof of a zero-percent join.
type openAIWeeklyFrozenEstimateState struct {
	Mode             string
	BaselineSource   string
	BaselinePercent  float64
	BaselineCost     float64
	PercentBucket    int
	SnapshotPercent  float64
	SnapshotCost     float64
	CompletedPercent float64
	CompletedCost    float64
	EstimateUSD      float64
	HasEstimate      bool
	AwaitingInterval bool
	ResetAt          time.Time
	Identity         string
	ObservedAt       time.Time
	needsPersist     bool
}

// applyOpenAIWeeklyEstimate calculates and persists the selected two-mode
// estimate. Prediction uses aligned XIASS cost; an accepted 100% observation
// uses current account cost exactly, including zero.
func (s *AccountUsageService) applyOpenAIWeeklyEstimate(ctx context.Context, account *Account, progress *UsageProgress, currentStats *usagestats.AccountStats, now time.Time) {
	if s == nil || account == nil || progress == nil || progress.WindowStats == nil || currentStats == nil {
		return
	}
	currentCost := currentStats.Cost
	if !validOpenAIWeeklyEstimateValue(currentCost) || !validOpenAIWeeklyEstimateValue(progress.Utilization) {
		progress.WeeklyEstimateUSD = nil
		return
	}
	snapshotAt, snapshotOK := openAICodexSnapshotObservationAt(account, now)
	if !snapshotOK {
		s.setOpenAIWeeklyFrozenEstimate(account, progress, 0, currentCost, false, time.Time{}, ctx)
		return
	}
	if progress.Utilization >= 100 {
		s.setOpenAIWeeklyFrozenEstimate(account, progress, 0, currentCost, false, snapshotAt, ctx)
		return
	}
	rangeReader, ok := s.usageLogRepo.(accountWindowStatsRangeReader)
	if !ok {
		s.setOpenAIWeeklyFrozenEstimate(account, progress, 0, currentCost, false, snapshotAt, ctx)
		return
	}
	startAt := codexWindowStatsStart(progress, 7*24*time.Hour, snapshotAt)
	if !startAt.Before(snapshotAt) {
		s.setOpenAIWeeklyFrozenEstimate(account, progress, 0, currentCost, false, snapshotAt, ctx)
		return
	}
	boundedStats := s.cachedOpenAIWeeklyEstimateStats(account.ID, startAt, snapshotAt)
	if boundedStats == nil {
		stats, err := rangeReader.GetAccountWindowStatsRange(ctx, account.ID, startAt, snapshotAt)
		if err == nil && stats != nil && validOpenAIWeeklyEstimateValue(stats.Cost) {
			boundedStats = stats
			s.storeOpenAIWeeklyEstimateStats(account.ID, startAt, snapshotAt, stats)
		}
	}
	if boundedStats == nil {
		s.setOpenAIWeeklyFrozenEstimate(account, progress, 0, currentCost, false, snapshotAt, ctx)
		return
	}
	s.setOpenAIWeeklyFrozenEstimate(account, progress, boundedStats.Cost, currentCost, true, snapshotAt, ctx)
}

// applyOpenAIWeeklyEstimateReadOnly is for passive execution-node reads. It
// reads shared usage but never writes account extra or advances saved state.
func (s *AccountUsageService) applyOpenAIWeeklyEstimateReadOnly(ctx context.Context, account *Account, progress *UsageProgress, currentStats *usagestats.AccountStats, now time.Time) {
	if s == nil || account == nil || progress == nil || progress.WindowStats == nil || currentStats == nil {
		return
	}
	currentCost := currentStats.Cost
	if !validOpenAIWeeklyEstimateValue(currentCost) || !validOpenAIWeeklyEstimateValue(progress.Utilization) {
		progress.WeeklyEstimateUSD = nil
		return
	}
	snapshotAt, ok := openAICodexSnapshotObservationAt(account, now)
	if !ok {
		progress.WeeklyEstimateUSD = nil
		return
	}
	if progress.Utilization >= 100 {
		estimate, _ := calculateOpenAIWeeklyFrozenEstimate(account, progress, 0, currentCost, false, snapshotAt)
		progress.WeeklyEstimateUSD = estimate
		return
	}
	// The owner has already frozen this raw percentage. A later observation at
	// the same percent (including a reset ETA revision) cannot form a new interval.
	// Do not replace that endpoint with a differently bounded local aggregation.
	if state, valid := readOpenAIWeeklyFrozenEstimateState(account.Extra); valid &&
		progress.ResetsAt != nil && state.matches(account.GetCredential("chatgpt_account_id"), *progress.ResetsAt, now) &&
		!snapshotAt.Before(state.ObservedAt) && progress.Utilization == state.SnapshotPercent &&
		currentCost >= state.SnapshotCost && state.value() != nil {
		progress.WeeklyEstimateUSD = state.value()
		return
	}
	rangeReader, ok := s.usageLogRepo.(accountWindowStatsRangeReader)
	if !ok {
		return
	}
	startAt := codexWindowStatsStart(progress, 7*24*time.Hour, snapshotAt)
	if !startAt.Before(snapshotAt) {
		return
	}
	// Local cached totals are not bound to shared state revisions. Pairing one
	// with a newer owner state can falsely signal cost regression/external use.
	stats, err := rangeReader.GetAccountWindowStatsRange(ctx, account.ID, startAt, snapshotAt)
	if err != nil || stats == nil || !validOpenAIWeeklyEstimateValue(stats.Cost) {
		return
	}
	estimate, _ := calculateOpenAIWeeklyFrozenEstimate(account, progress, stats.Cost, currentCost, true, snapshotAt)
	progress.WeeklyEstimateUSD = estimate
}

func (s *AccountUsageService) cachedOpenAIWeeklyEstimateStats(accountID int64, startAt, snapshotAt time.Time) *usagestats.AccountStats {
	if s == nil || s.cache == nil {
		return nil
	}
	cached, ok := s.cache.weeklyEstimateStatsCache.Load(accountID)
	if !ok {
		return nil
	}
	entry, ok := cached.(*weeklyEstimateStatsCache)
	if !ok || time.Since(entry.timestamp) >= windowStatsCacheTTL || !entry.startAt.Equal(startAt) || !entry.snapshotAt.Equal(snapshotAt) {
		return nil
	}
	return entry.stats
}

func (s *AccountUsageService) storeOpenAIWeeklyEstimateStats(accountID int64, startAt, snapshotAt time.Time, stats *usagestats.AccountStats) {
	if s == nil || s.cache == nil || stats == nil {
		return
	}
	s.cache.weeklyEstimateStatsCache.Store(accountID, &weeklyEstimateStatsCache{stats: stats, startAt: startAt, snapshotAt: snapshotAt, timestamp: time.Now()})
}

func (s *AccountUsageService) setOpenAIWeeklyFrozenEstimate(account *Account, progress *UsageProgress, snapshotCost, currentCost float64, snapshotMatched bool, observedAt time.Time, ctx context.Context) {
	calculationAccount := account
	if snapshotMatched {
		var err error
		calculationAccount, err = s.accountWithVerifiedOpenAIWeeklyJoin(ctx, account, progress, snapshotCost, observedAt)
		if err != nil {
			progress.WeeklyEstimateUSD = nil
			return
		}
	}
	estimate, updates := calculateOpenAIWeeklyFrozenEstimate(calculationAccount, progress, snapshotCost, currentCost, snapshotMatched, observedAt)
	if calculationAccount != account && len(updates) == 0 {
		updates = map[string]any{openAIWeeklyEstimateBaselineKey: calculationAccount.Extra[openAIWeeklyEstimateBaselineKey]}
	}
	if !s.persistOpenAIWeeklyEstimate(ctx, account, updates) {
		progress.WeeklyEstimateUSD = nil
		return
	}
	progress.WeeklyEstimateUSD = estimate
}

func calculateOpenAIWeeklyFrozenEstimate(account *Account, progress *UsageProgress, snapshotCost, currentCost float64, snapshotMatched bool, observedAt time.Time) (*float64, map[string]any) {
	if account == nil || progress == nil || progress.WindowStats == nil ||
		!validOpenAIWeeklyEstimateValue(progress.Utilization) || !validOpenAIWeeklyEstimateValue(currentCost) {
		return nil, nil
	}
	identity := account.GetCredential("chatgpt_account_id")
	if identity == "" {
		return nil, nil
	}
	now := time.Now().UTC()
	percent := math.Min(100, progress.Utilization)
	resetAt := time.Time{}
	if progress.ResetsAt != nil {
		resetAt = progress.ResetsAt.UTC()
	}
	state, stateOK := readOpenAIWeeklyFrozenEstimateState(account.Extra)
	if !stateOK {
		state, stateOK = readOpenAIWeeklyFrozenEstimateLegacyState(account.Extra)
	}
	if raw, ok := account.Extra[openAIWeeklyEstimateBaselineKey].(map[string]any); ok &&
		parseExtraInt(raw["version"]) > openAIWeeklyFrozenEstimateTwoRuleStateVersion {
		return nil, nil
	}
	matches := stateOK && state.matches(identity, resetAt, now)
	accepted := state
	keep := func() (*float64, map[string]any) {
		if matches {
			return accepted.value(), nil
		}
		return nil, nil
	}
	save := func(next openAIWeeklyFrozenEstimateState) (*float64, map[string]any) {
		return next.value(), openAIWeeklyFrozenEstimateUpdateWithEvidence(next, account.Extra)
	}

	// Ordering precedes the 100% shortcut and scope changes. A later completion
	// time is not proof of a new identity; callers must bind samples to credentials.
	if observedAt.IsZero() || observedAt.After(now.Add(5*time.Second)) {
		return keep()
	}
	observedAt = observedAt.UTC()
	if !resetAt.IsZero() && !now.Before(resetAt) {
		return nil, nil
	}
	if stateOK && !state.ObservedAt.IsZero() {
		if observedAt.Before(state.ObservedAt) {
			return keep()
		}
		if observedAt.Equal(state.ObservedAt) && (!matches || percent != state.SnapshotPercent) {
			return keep()
		}
	}
	if percent >= 100 {
		if stateOK && state.Identity != identity {
			// An old identity's raw 100% cannot authorize an exact result for its
			// replacement. The lifecycle owner must invalidate the old state.
			return nil, nil
		}
		if matches && observedAt.Equal(state.ObservedAt) && currentCost < state.SnapshotCost {
			return keep()
		}
		terminal := newOpenAIWeeklyFrozenEstimateState(100, currentCost, resetAt, identity, observedAt)
		terminal.Mode, terminal.BaselineSource = openAIWeeklyEstimateModeUnknown, "terminal_observation"
		terminal.EstimateUSD, terminal.HasEstimate, terminal.AwaitingInterval = currentCost, true, false
		if matches && state.SnapshotPercent == 100 && state.SnapshotCost == currentCost &&
			observedAt.Equal(state.ObservedAt) && !state.needsPersist {
			return keep()
		}
		return save(terminal)
	}
	if !snapshotMatched || !validOpenAIWeeklyEstimateValue(snapshotCost) ||
		snapshotCost > currentCost+openAIWeeklyEstimateEpsilon {
		return keep()
	}
	if !matches {
		next := newOpenAIWeeklyFrozenEstimateState(percent, snapshotCost, resetAt, identity, observedAt)
		if stateOK && state.Identity == identity && !state.ResetAt.IsZero() && !resetAt.IsZero() &&
			resetAt.After(state.ResetAt) && !sameOpenAIWeeklyEstimateWindow(state.ResetAt, resetAt, now) {
			next.Mode, next.BaselineSource = openAIWeeklyEstimateModeLegacy, "weekly_window_reset"
		}
		if !stateOK && account.Extra[openAIWeeklyEstimateBaselineKey] != nil {
			next.Mode, next.BaselineSource = openAIWeeklyEstimateModeUnknown, "unreadable_start_evidence"
		}
		return save(next)
	}
	if observedAt.Equal(state.ObservedAt) && snapshotCost <= state.SnapshotCost {
		if snapshotCost == state.SnapshotCost && state.needsPersist {
			return save(state)
		}
		return keep()
	}

	if percent < state.SnapshotPercent || snapshotCost < state.SnapshotCost {
		source := "cost_regression"
		if percent < state.SnapshotPercent {
			source = "percent_regression"
		}
		next := rebaseOpenAIWeeklyFrozenEstimate(state, percent, snapshotCost, resetAt, identity, observedAt, source)
		return save(next)
	}
	// A provider-only advance is conclusive at an aligned endpoint, or when the
	// cumulative XIASS cost has not passed the last completed endpoint. A partial
	// sample may carry local spend before the provider percentage catches up, so
	// do not misclassify that delayed update as external use. CompletedCost remains
	// the first cost aligned to the displayed endpoint and stays frozen.
	lastSampleAligned := math.Abs(state.SnapshotPercent-state.CompletedPercent) <= openAIWeeklyEstimateEpsilon
	if percent > state.SnapshotPercent && snapshotCost <= state.SnapshotCost &&
		(snapshotCost <= state.CompletedCost || lastSampleAligned) {
		next := rebaseOpenAIWeeklyFrozenEstimate(state, percent, snapshotCost, resetAt, identity, observedAt, "external_only_rebase")
		return save(next)
	}
	if state.HasEstimate && percent < state.CompletedPercent+1 {
		// Keep the displayed estimate and completed jump endpoint frozen until a
		// full percentage interval finishes. The latest raw sample is still saved
		// so ordered observations and external-use detection remain accurate.
		state.ResetAt, state.ObservedAt = resetAt, observedAt
		state.SnapshotPercent, state.SnapshotCost = percent, snapshotCost
		state.PercentBucket = openAIWeeklyEstimatePercentBucket(percent)
		state.AwaitingInterval = true
		return save(state)
	}

	state.ResetAt, state.ObservedAt = resetAt, observedAt
	state.SnapshotPercent, state.SnapshotCost = percent, snapshotCost
	state.PercentBucket = openAIWeeklyEstimatePercentBucket(percent)
	state.updateEstimate()
	if !validOpenAIWeeklyEstimateValue(state.EstimateUSD) {
		return keep()
	}
	return save(state)
}

// Keep the two-rule writer version explicit for integration and regression tests.
const (
	openAIWeeklyFrozenEstimateTwoRuleStateVersion = openAIWeeklyFrozenEstimateStateVersion
	openAIWeeklyEstimateModeUnknown               = "pending_unknown_start"
)

func newOpenAIWeeklyFrozenEstimateState(percent, cost float64, resetAt time.Time, identity string, observedAt time.Time) openAIWeeklyFrozenEstimateState {
	mode, source := openAIWeeklyEstimateModeJoinAverage, "first_aligned_observation"
	if percent == 0 {
		mode, source = openAIWeeklyEstimateModeLegacy, "observed_zero"
	}
	return openAIWeeklyFrozenEstimateState{
		Mode: mode, BaselineSource: source, BaselinePercent: percent, BaselineCost: cost,
		PercentBucket: openAIWeeklyEstimatePercentBucket(percent), SnapshotPercent: percent, SnapshotCost: cost,
		CompletedPercent: percent, CompletedCost: cost, AwaitingInterval: true,
		ResetAt: resetAt, Identity: identity, ObservedAt: observedAt.UTC(),
	}
}

// newOpenAIWeeklyFrozenEstimateStateFromBaseline initializes a pending state
// from an explicitly supplied, already verified baseline. The caller owns
// identity/week/evidence verification; a source label is not proof of login.
// This helper neither reads history nor mutates or persists an existing state.
func newOpenAIWeeklyFrozenEstimateStateFromBaseline(percent, cost float64, resetAt time.Time, identity string, observedAt time.Time, baselineSource string) (openAIWeeklyFrozenEstimateState, bool) {
	if !validOpenAIWeeklyEstimateValue(percent) || percent >= 100 || !validOpenAIWeeklyEstimateValue(cost) ||
		strings.TrimSpace(identity) == "" || strings.TrimSpace(baselineSource) == "" ||
		resetAt.IsZero() || observedAt.IsZero() || !observedAt.Before(resetAt) {
		return openAIWeeklyFrozenEstimateState{}, false
	}
	state := newOpenAIWeeklyFrozenEstimateState(percent, cost, resetAt.UTC(), identity, observedAt.UTC())
	state.BaselineSource = baselineSource
	return state, state.valid()
}

func rebaseOpenAIWeeklyFrozenEstimate(previous openAIWeeklyFrozenEstimateState, percent, cost float64, resetAt time.Time, identity string, observedAt time.Time, source string) openAIWeeklyFrozenEstimateState {
	next := newOpenAIWeeklyFrozenEstimateState(percent, cost, resetAt, identity, observedAt)
	next.BaselineSource = source
	if percent > 0 && previous.SnapshotPercent < 100 {
		next.Mode = previous.Mode
	}
	if source == "external_only_rebase" {
		// External consumption breaks Rule B's zero-start denominator.
		next.Mode = openAIWeeklyEstimateModeJoinAverage
	}
	if previous.Mode == openAIWeeklyEstimateModeUnknown && percent > 0 && previous.SnapshotPercent < 100 {
		next.Mode = openAIWeeklyEstimateModeUnknown
	}
	return next
}

func (state *openAIWeeklyFrozenEstimateState) updateEstimate() {
	if state == nil || state.Mode == openAIWeeklyEstimateModeUnknown || state.SnapshotPercent >= 100 {
		return
	}
	if state.Mode == openAIWeeklyEstimateModeLegacy {
		bucket := openAIWeeklyEstimatePercentBucket(state.SnapshotPercent)
		if bucket <= 1 || bucket <= openAIWeeklyEstimatePercentBucket(state.CompletedPercent) ||
			(!state.HasEstimate && state.SnapshotPercent < state.CompletedPercent+1) {
			state.AwaitingInterval = true
			return
		}
		state.EstimateUSD = state.SnapshotCost / (float64(bucket-1) / 100)
	} else {
		// Compare the raw endpoint to start+1, rather than rounding/flooring or
		// testing a subtraction that can turn 0.4 -> 1.4 into 0.9999999999999999.
		if state.SnapshotPercent < state.CompletedPercent+1 {
			state.AwaitingInterval = true
			return
		}
		deltaPercent := state.SnapshotPercent - state.BaselinePercent
		deltaCost := state.SnapshotCost - state.BaselineCost
		if deltaPercent <= 0 || deltaCost <= 0 {
			state.AwaitingInterval = true
			return
		}
		state.EstimateUSD = deltaCost / deltaPercent * 100
	}
	state.HasEstimate = validOpenAIWeeklyEstimateValue(state.EstimateUSD)
	state.AwaitingInterval = !state.HasEstimate
	if state.HasEstimate {
		state.CompletedPercent, state.CompletedCost = state.SnapshotPercent, state.SnapshotCost
	}
}

func openAIWeeklyEstimatePercentBucket(percent float64) int {
	if !validOpenAIWeeklyEstimateValue(percent) {
		return 0
	}
	return int(math.Floor(math.Min(99, percent)))
}

func (state openAIWeeklyFrozenEstimateState) matches(identity string, resetAt, now time.Time) bool {
	return state.valid() && state.Identity == identity && sameOpenAIWeeklyEstimateWindow(state.ResetAt, resetAt, now)
}

func (state openAIWeeklyFrozenEstimateState) valid() bool {
	if state.Mode != openAIWeeklyEstimateModeLegacy && state.Mode != openAIWeeklyEstimateModeJoinAverage &&
		state.Mode != openAIWeeklyEstimateModeUnknown {
		return false
	}
	for _, value := range []float64{state.BaselinePercent, state.BaselineCost, state.SnapshotPercent,
		state.SnapshotCost, state.CompletedPercent, state.CompletedCost} {
		if !validOpenAIWeeklyEstimateValue(value) {
			return false
		}
	}
	if state.BaselinePercent > state.SnapshotPercent || state.SnapshotPercent > 100 ||
		state.PercentBucket != openAIWeeklyEstimatePercentBucket(state.SnapshotPercent) ||
		state.SnapshotCost < state.BaselineCost || state.CompletedPercent < state.BaselinePercent ||
		state.CompletedPercent > state.SnapshotPercent || state.CompletedCost < state.BaselineCost ||
		state.CompletedCost > state.SnapshotCost {
		return false
	}
	if state.SnapshotPercent == 100 {
		return state.HasEstimate && validOpenAIWeeklyEstimateValue(state.EstimateUSD) &&
			state.EstimateUSD == state.SnapshotCost
	}
	if state.Mode == openAIWeeklyEstimateModeUnknown && state.HasEstimate {
		return false
	}
	if state.HasEstimate && (state.CompletedPercent < state.BaselinePercent+1 ||
		(state.Mode == openAIWeeklyEstimateModeLegacy && openAIWeeklyEstimatePercentBucket(state.CompletedPercent) <= 1)) {
		return false
	}
	return !state.HasEstimate || validOpenAIWeeklyEstimateValue(state.EstimateUSD)
}

func (state openAIWeeklyFrozenEstimateState) value() *float64 {
	if !state.HasEstimate || !validOpenAIWeeklyEstimateValue(state.EstimateUSD) ||
		(state.SnapshotPercent < 100 && state.EstimateUSD <= 0) {
		return nil
	}
	estimate := state.EstimateUSD
	return &estimate
}

func openAIWeeklyFrozenEstimateStateUpdate(state openAIWeeklyFrozenEstimateState) map[string]any {
	// The write boundary assigns revision from the original account, so staging
	// a historical seed and then calculating cannot consume two revisions.
	raw := map[string]any{
		"version": openAIWeeklyFrozenEstimateTwoRuleStateVersion,
		"mode":    state.Mode, "baseline_source": state.BaselineSource,
		"baseline_percent": state.BaselinePercent, "baseline_cost": state.BaselineCost,
		"percent_bucket": state.PercentBucket, "snapshot_percent": state.SnapshotPercent, "snapshot_cost": state.SnapshotCost,
		"completed_percent": state.CompletedPercent, "completed_cost": state.CompletedCost,
		"awaiting_interval": state.AwaitingInterval, "has_weekly_estimate": state.HasEstimate,
		"terminal": state.SnapshotPercent == 100,
		"reset_at": state.ResetAt.Format(time.RFC3339Nano), "identity": state.Identity,
	}
	if !state.ObservedAt.IsZero() {
		raw["observed_at"] = state.ObservedAt.Format(time.RFC3339Nano)
	}
	if state.HasEstimate {
		raw["estimate_usd"] = state.EstimateUSD
	}
	return map[string]any{openAIWeeklyEstimateBaselineKey: raw}
}

func openAIWeeklyFrozenEstimateUpdateWithEvidence(state openAIWeeklyFrozenEstimateState, extra map[string]any) map[string]any {
	updates := openAIWeeklyFrozenEstimateStateUpdate(state)
	raw, ok := updates[openAIWeeklyEstimateBaselineKey].(map[string]any)
	if !ok {
		return updates
	}
	previous, ok := extra[openAIWeeklyEstimateBaselineKey].(map[string]any)
	if !ok {
		return updates
	}
	for _, key := range []string{"legacy_evidence", "previous_sampling_baseline", "join_evidence"} {
		if evidence, exists := previous[key]; exists {
			raw[key] = evidence
		}
	}
	version := parseExtraInt(previous["version"])
	if version != openAIWeeklyFrozenEstimateTwoRuleStateVersion {
		// Keep the original raw record, including absent fields, without
		// upgrading its old mode/source into verified login evidence.
		raw["legacy_evidence"] = previous
	} else {
		baselinePercent, percentOK := parseOpenAIWeeklyEstimateNumber(previous, "baseline_percent")
		baselineCost, costOK := parseOpenAIWeeklyEstimateNumber(previous, "baseline_cost")
		identity, _ := previous["identity"].(string)
		source, _ := previous["baseline_source"].(string)
		if percentOK && costOK && baselinePercent == state.BaselinePercent && baselineCost == state.BaselineCost &&
			identity == state.Identity && source == state.BaselineSource {
			return updates
		}
		evidence := make(map[string]any)
		for _, key := range []string{"mode", "baseline_source", "baseline_percent", "baseline_cost", "snapshot_percent", "snapshot_cost", "observed_at", "identity", "reset_at"} {
			if value, exists := previous[key]; exists {
				evidence[key] = value
			}
		}
		raw["previous_sampling_baseline"] = evidence
	}
	return updates
}

func readOpenAIWeeklyFrozenEstimateState(extra map[string]any) (openAIWeeklyFrozenEstimateState, bool) {
	raw, ok := extra[openAIWeeklyEstimateBaselineKey].(map[string]any)
	if !ok {
		return openAIWeeklyFrozenEstimateState{}, false
	}
	version := parseExtraInt(raw["version"])
	if version != openAIWeeklyFrozenEstimateTwoRuleStateVersion && version != openAIWeeklyFrozenEstimatePreviousVersion {
		return openAIWeeklyFrozenEstimateState{}, false
	}
	baselinePercent, baselineOK := parseOpenAIWeeklyEstimateNumber(raw, "baseline_percent")
	baselineCost, baselineCostOK := parseOpenAIWeeklyEstimateNumber(raw, "baseline_cost")
	snapshotCost, snapshotCostOK := parseOpenAIWeeklyEstimateNumber(raw, "snapshot_cost")
	bucketValue, bucketOK := parseOpenAIWeeklyEstimateNumber(raw, "percent_bucket")
	snapshotPercent, snapshotPercentOK := parseOpenAIWeeklyEstimateNumber(raw, "snapshot_percent")
	if !baselineOK || !baselineCostOK || !snapshotCostOK || !bucketOK ||
		!validOpenAIWeeklyEstimateValue(bucketValue) || bucketValue > 99 || math.Trunc(bucketValue) != bucketValue {
		return openAIWeeklyFrozenEstimateState{}, false
	}
	if !snapshotPercentOK {
		if version == openAIWeeklyFrozenEstimateTwoRuleStateVersion {
			return openAIWeeklyFrozenEstimateState{}, false
		}
		snapshotPercent = bucketValue
	}
	resetText, resetOK := raw["reset_at"].(string)
	identity, identityOK := raw["identity"].(string)
	if !resetOK || !identityOK || identity == "" {
		return openAIWeeklyFrozenEstimateState{}, false
	}
	resetAt, err := parseTime(resetText)
	if err != nil {
		return openAIWeeklyFrozenEstimateState{}, false
	}
	mode, _ := raw["mode"].(string)
	source, _ := raw["baseline_source"].(string)
	state := openAIWeeklyFrozenEstimateState{
		Mode: mode, BaselineSource: source, BaselinePercent: baselinePercent, BaselineCost: baselineCost,
		PercentBucket: int(bucketValue), SnapshotPercent: snapshotPercent, SnapshotCost: snapshotCost,
		ResetAt: resetAt.UTC(), Identity: identity,
	}
	if rawObservedAt, exists := raw["observed_at"]; exists && rawObservedAt != nil {
		observedAt, err := parseTime(fmt.Sprint(rawObservedAt))
		if err != nil {
			return openAIWeeklyFrozenEstimateState{}, false
		}
		state.ObservedAt = observedAt.UTC()
	}
	if version == openAIWeeklyFrozenEstimatePreviousVersion {
		// v14 itself persisted the first aligned local baseline. A zero/zero
		// baseline is direct evidence that the account was observed from 0%; any
		// positive baseline is the saved mid-window join point. Reclassifying these
		// records as unknown made valid accounts remain "统计中" forever.
		state.CompletedPercent, state.CompletedCost = baselinePercent, baselineCost
		state.HasEstimate, state.EstimateUSD, state.AwaitingInterval = false, 0, true
		switch {
		case baselinePercent == 0 && baselineCost == 0:
			state.Mode, state.BaselineSource = openAIWeeklyEstimateModeLegacy, "v14_persisted_zero_start"
		case baselinePercent > 0:
			state.Mode, state.BaselineSource = openAIWeeklyEstimateModeJoinAverage, "v14_persisted_join_baseline"
		default:
			state.Mode, state.BaselineSource = openAIWeeklyEstimateModeUnknown, "legacy_start_unclassified_v14"
		}
		state.updateEstimate()
		state.needsPersist = true
		return state, state.valid()
	}
	var completedOK, completedCostOK, hasEstimateOK, awaitingOK bool
	state.CompletedPercent, completedOK = parseOpenAIWeeklyEstimateNumber(raw, "completed_percent")
	state.CompletedCost, completedCostOK = parseOpenAIWeeklyEstimateNumber(raw, "completed_cost")
	state.HasEstimate, hasEstimateOK = parseOpenAIWeeklyEstimateBool(raw, "has_weekly_estimate")
	state.AwaitingInterval, awaitingOK = parseOpenAIWeeklyEstimateBool(raw, "awaiting_interval")
	terminal, terminalOK := parseOpenAIWeeklyEstimateBool(raw, "terminal")
	if !completedOK || !completedCostOK || !hasEstimateOK || !awaitingOK || !terminalOK ||
		terminal != (snapshotPercent == 100) || state.ObservedAt.IsZero() {
		return openAIWeeklyFrozenEstimateState{}, false
	}
	if state.HasEstimate {
		estimate, estimateOK := parseOpenAIWeeklyEstimateNumber(raw, "estimate_usd")
		if !estimateOK {
			return openAIWeeklyFrozenEstimateState{}, false
		}
		state.EstimateUSD = estimate
	}
	if !state.valid() {
		return openAIWeeklyFrozenEstimateState{}, false
	}
	if recovered, ok := recoverOpenAIWeeklyV15LegacyEvidence(state, raw); ok {
		return recovered, true
	}
	return state, true
}

// recoverOpenAIWeeklyV15LegacyEvidence repairs v15 states written by v1.1.85
// after it incorrectly downgraded a valid v14 baseline to unknown. The original
// v14 object was retained verbatim, so recovery does not infer a join point from
// audit logs or from a later sample.
func recoverOpenAIWeeklyV15LegacyEvidence(current openAIWeeklyFrozenEstimateState, raw map[string]any) (openAIWeeklyFrozenEstimateState, bool) {
	if current.Mode != openAIWeeklyEstimateModeUnknown {
		return openAIWeeklyFrozenEstimateState{}, false
	}
	legacyRaw, ok := raw["legacy_evidence"].(map[string]any)
	if !ok || parseExtraInt(legacyRaw["version"]) != openAIWeeklyFrozenEstimatePreviousVersion {
		return openAIWeeklyFrozenEstimateState{}, false
	}
	legacy, ok := readOpenAIWeeklyFrozenEstimateState(map[string]any{openAIWeeklyEstimateBaselineKey: legacyRaw})
	if !ok || legacy.Mode == openAIWeeklyEstimateModeUnknown || legacy.Identity != current.Identity ||
		!sameOpenAIWeeklyEstimateWindow(legacy.ResetAt, current.ResetAt, time.Now().UTC()) {
		return openAIWeeklyFrozenEstimateState{}, false
	}

	// The retained v14 endpoint is the first cost aligned to its percentage.
	// Keep it when a later v15 read is still in that same percentage bucket.
	recovered := legacy
	if current.SnapshotPercent > legacy.SnapshotPercent && current.SnapshotCost >= legacy.SnapshotCost {
		recovered.ResetAt = current.ResetAt
		recovered.ObservedAt = current.ObservedAt
		recovered.SnapshotPercent = current.SnapshotPercent
		recovered.SnapshotCost = current.SnapshotCost
		recovered.PercentBucket = openAIWeeklyEstimatePercentBucket(current.SnapshotPercent)
		recovered.updateEstimate()
	}
	recovered.needsPersist = true
	if !recovered.valid() {
		return openAIWeeklyFrozenEstimateState{}, false
	}
	return recovered, true
}

func readOpenAIWeeklyFrozenEstimateLegacyState(extra map[string]any) (openAIWeeklyFrozenEstimateState, bool) {
	raw, ok := extra[openAIWeeklyEstimateBaselineKey].(map[string]any)
	if !ok || parseExtraInt(raw["version"]) != openAIWeeklyFrozenEstimateLegacyStateVersion {
		return openAIWeeklyFrozenEstimateState{}, false
	}
	bucketValue, bucketOK := parseOpenAIWeeklyEstimateNumber(raw, "percent_bucket")
	snapshotCost, costOK := parseOpenAIWeeklyEstimateNumber(raw, "snapshot_cost")
	resetText, resetOK := raw["reset_at"].(string)
	identity, identityOK := raw["identity"].(string)
	if !bucketOK || !costOK || !resetOK || !identityOK || identity == "" ||
		!validOpenAIWeeklyEstimateValue(bucketValue) || bucketValue > 99 || math.Trunc(bucketValue) != bucketValue {
		return openAIWeeklyFrozenEstimateState{}, false
	}
	resetAt, err := parseTime(resetText)
	if err != nil {
		return openAIWeeklyFrozenEstimateState{}, false
	}
	percent, percentOK := parseOpenAIWeeklyEstimateNumber(raw, "snapshot_percent")
	if !percentOK {
		percent = bucketValue
	}
	state := newOpenAIWeeklyFrozenEstimateState(percent, snapshotCost, resetAt.UTC(), identity, time.Time{})
	state.Mode, state.BaselineSource, state.needsPersist = openAIWeeklyEstimateModeUnknown, "legacy_start_unclassified_v13", true
	if rawObservedAt, exists := raw["observed_at"]; exists && rawObservedAt != nil {
		observedAt, err := parseTime(fmt.Sprint(rawObservedAt))
		if err != nil {
			return openAIWeeklyFrozenEstimateState{}, false
		}
		state.ObservedAt = observedAt.UTC()
	}
	return state, state.valid()
}
