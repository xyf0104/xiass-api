package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const openAIResetScope = "xiass.openai.reset.account.v1"
const openAIResetConfigPrefix = "xiass_openai_auto_reset_v1_"

var (
	ErrOpenAIResetUnavailable = infraerrors.ServiceUnavailable("OPENAI_RESET_UNAVAILABLE", "reset coordination is unavailable")
	ErrOpenAIResetPending     = infraerrors.Conflict("OPENAI_RESET_PENDING", "a reset is running or its result is unconfirmed; no further credit will be consumed")
	ErrOpenAIResetCooldown    = infraerrors.Conflict("OPENAI_RESET_COOLDOWN", "reset is cooling down after a confirmed result; retry after one minute")
	ErrOpenAIResetChanged     = infraerrors.Conflict("OPENAI_RESET_CHANGED", "account, authorization or reset settings changed; reset cancelled")
)

type openAIResetRejectedError struct{ error }

func (e *openAIResetRejectedError) Unwrap() error { return e.error }

// Stored separately from account Extra: importing/copying an account is never
// permission to spend its credits. A changed ChatGPT identity needs new consent.
type OpenAIAutoResetConfig struct {
	Enabled     bool    `json:"enabled"`
	Threshold5h float64 `json:"threshold_5h"`
	Threshold7d float64 `json:"threshold_7d"`
	Revision    string  `json:"revision"`
	Principal   string  `json:"principal,omitempty"`
	Pending     bool    `json:"pending,omitempty"`
}

func defaultOpenAIAutoResetConfig() OpenAIAutoResetConfig {
	return OpenAIAutoResetConfig{Threshold5h: 1, Threshold7d: 1}
}

func openAIResetConfigKey(id int64) string {
	return openAIResetConfigPrefix + strconv.FormatInt(id, 10)
}

func openAIResetPrincipal(a *Account) string {
	if a == nil {
		return ""
	}
	principal := strings.TrimSpace(a.GetCredential("chatgpt_account_id"))
	if principal == "" {
		principal = strings.TrimSpace(a.GetCredential("organization_id"))
	}
	if principal == "" {
		return ""
	}
	return HashIdempotencyKey(principal)
}

type openAIResetQuota interface {
	QueryUsage(context.Context, int64) (*OpenAIQuotaUsage, error)
	CachePostResetSnapshot(context.Context, int64, *OpenAIQuotaUsage) error
	consumeResetCredit(context.Context, int64, string, string, func(context.Context) error, func()) (*OpenAIQuotaResetResult, error)
}

type openAIResetRecoverer interface {
	RecoverAccountState(context.Context, int64, AccountRecoveryOptions) (*SuccessfulTestRecoveryResult, error)
}

// The existing durable idempotency ledger is also the per-account mutex shared
// by manual and automatic Reset. Processing records are NEVER lease-reclaimed:
// a crashed/ambiguous consume must not cause another card to be spent.
type OpenAIAutoResetService struct {
	accounts  AccountRepository
	settings  SettingRepository
	ledger    IdempotencyRepository
	access    AccountManagementAccessChecker
	quota     openAIResetQuota
	recoverer openAIResetRecoverer
	cancel    context.CancelFunc
	wg        sync.WaitGroup
	start     sync.Once
}

func (s *OpenAIAutoResetService) readConfig(ctx context.Context, a *Account) (OpenAIAutoResetConfig, error) {
	config := defaultOpenAIAutoResetConfig()
	if s == nil || s.settings == nil {
		return config, ErrOpenAIResetUnavailable
	}
	raw, err := s.settings.GetValue(ctx, openAIResetConfigKey(a.ID))
	if errors.Is(err, ErrSettingNotFound) {
		return config, nil
	}
	if err != nil {
		return config, err
	}
	if json.Unmarshal([]byte(raw), &config) != nil {
		return defaultOpenAIAutoResetConfig(), ErrOpenAIResetUnavailable
	}
	if config.Revision == "" || config.Principal == "" || config.Principal != openAIResetPrincipal(a) {
		config.Enabled = false
	}
	if err := validateOpenAIResetThresholds(config); err != nil {
		return defaultOpenAIAutoResetConfig(), err
	}
	return config, nil
}

func validateOpenAIResetThresholds(c OpenAIAutoResetConfig) error {
	for _, v := range []float64{c.Threshold5h, c.Threshold7d} {
		if math.IsNaN(v) || math.IsInf(v, 0) || v < .001 || v > 1 {
			return infraerrors.BadRequest("OPENAI_RESET_THRESHOLD_INVALID", "reset thresholds must be between 0.001 and 1")
		}
	}
	return nil
}

func (s *OpenAIAutoResetService) account(ctx context.Context, id int64) (*Account, error) {
	if s == nil || s.accounts == nil || s.access == nil || s.ledger == nil {
		return nil, ErrOpenAIResetUnavailable
	}
	if err := s.access.CheckAccountManagementAccess(ctx, id); err != nil {
		return nil, err
	}
	a, err := s.accounts.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if a == nil || !a.IsOpenAIOAuth() || a.IsShadow() || openAIResetPrincipal(a) == "" {
		return nil, infraerrors.BadRequest("OPENAI_RESET_ACCOUNT_INVALID", "reset requires an OpenAI OAuth parent account")
	}
	return a, nil
}

func (s *OpenAIAutoResetService) GetConfig(ctx context.Context, id int64) (OpenAIAutoResetConfig, error) {
	a, err := s.account(ctx, id)
	if err != nil {
		return OpenAIAutoResetConfig{}, err
	}
	c, err := s.readConfig(ctx, a)
	if err != nil {
		return c, err
	}
	record, err := s.ledger.GetByScopeAndKeyHash(ctx, openAIResetScope, HashIdempotencyKey(strconv.FormatInt(id, 10)))
	if err != nil {
		return c, err
	}
	c.Pending = record != nil && record.Status == IdempotencyStatusProcessing
	c.Principal = ""
	return c, nil
}

func (s *OpenAIAutoResetService) SetConfig(ctx context.Context, id int64, enabled bool, fiveHour, sevenDay float64) (OpenAIAutoResetConfig, error) {
	a, err := s.account(ctx, id)
	if err != nil {
		return OpenAIAutoResetConfig{}, err
	}
	c := OpenAIAutoResetConfig{Enabled: enabled, Threshold5h: fiveHour, Threshold7d: sevenDay, Principal: openAIResetPrincipal(a)}
	if err := validateOpenAIResetThresholds(c); err != nil {
		return c, err
	}
	c.Revision, err = generateRedeemRequestID()
	if err != nil {
		return c, err
	}
	raw, err := json.Marshal(c)
	if err != nil {
		return c, err
	}
	if s.settings == nil {
		return c, ErrOpenAIResetUnavailable
	}
	if err := s.settings.Set(ctx, openAIResetConfigKey(id), string(raw)); err != nil {
		return c, err
	}
	return s.GetConfig(ctx, id)
}

func (s *OpenAIAutoResetService) Start() {
	s.start.Do(func() {
		ctx, cancel := context.WithCancel(context.Background())
		s.cancel = cancel
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			ticker := time.NewTicker(time.Minute)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					s.scan(ctx)
				}
			}
		}()
	})
}

func (s *OpenAIAutoResetService) Stop() {
	if s != nil && s.cancel != nil {
		s.cancel()
		s.wg.Wait()
	}
}

func (s *OpenAIAutoResetService) scan(ctx context.Context) {
	if s == nil || s.settings == nil || ctx.Err() != nil {
		return
	}
	settings, err := s.settings.GetAll(ctx)
	if err != nil {
		return
	}
	// All-OFF needs only this settings read, not an account-table scan. The
	// reset path rechecks current consent and ownership before any quota call.
	for key, raw := range settings {
		if ctx.Err() != nil {
			return
		}
		if !strings.HasPrefix(key, openAIResetConfigPrefix) {
			continue
		}
		id, err := strconv.ParseInt(strings.TrimPrefix(key, openAIResetConfigPrefix), 10, 64)
		if err != nil || id <= 0 || key != openAIResetConfigKey(id) {
			continue
		}
		var c OpenAIAutoResetConfig
		if json.Unmarshal([]byte(raw), &c) != nil || !c.Enabled || c.Revision == "" || c.Principal == "" || validateOpenAIResetThresholds(c) != nil {
			continue
		}
		_, _ = s.reset(ctx, id, true)
	}
}

func sameOpenAIResetAccount(a, b *Account) bool {
	return a != nil && b != nil && a.Platform == b.Platform && a.Type == b.Type &&
		sameOpenAIResetCredentials(a, b) &&
		reflect.DeepEqual(a.Proxy, b.Proxy) && reflect.DeepEqual(a.Extra[AccountExecutionNodeExtraKey], b.Extra[AccountExecutionNodeExtraKey])
}

func sameOpenAIResetCredentials(a, b *Account) bool {
	if a == nil || b == nil || a.ID != b.ID || !reflect.DeepEqual(a.ProxyID, b.ProxyID) {
		return false
	}
	left, err := json.Marshal(a.Credentials)
	if err != nil {
		return false
	}
	right, err := json.Marshal(b.Credentials)
	return err == nil && bytes.Equal(left, right)
}

func sameOpenAIManualResetAccount(a, b *Account) bool {
	if a != nil && b != nil && a.IsOpenAIAgentIdentity() && b.IsOpenAIAgentIdentity() {
		// Task renewal is part of the existing manual assertion-auth flow, not
		// a change of account authorization. All other credentials stay fenced.
		left, right := *a, *b
		left.Credentials, right.Credentials = shallowCopyMap(a.Credentials), shallowCopyMap(b.Credentials)
		delete(left.Credentials, "task_id")
		delete(right.Credentials, "task_id")
		return sameOpenAIResetAccount(&left, &right)
	}
	return sameOpenAIResetAccount(a, b)
}

func openAIResetActive(a *Account) bool {
	return a != nil && a.IsActive() && a.Schedulable && (a.ExpiresAt == nil || a.ExpiresAt.After(time.Now()))
}

func openAIResetThresholdReached(usage *OpenAIQuotaUsage, c OpenAIAutoResetConfig) bool {
	if usage == nil || usage.RateLimit == nil {
		return false
	}
	for _, w := range []*OpenAIRateLimitWindow{usage.RateLimit.PrimaryWindow, usage.RateLimit.SecondaryWindow} {
		if !validOpenAIResetWindow(w) {
			continue
		}
		threshold := 0.0
		switch w.LimitWindowSeconds {
		case 5 * 60 * 60:
			threshold = c.Threshold5h
		case 7 * 24 * 60 * 60:
			threshold = c.Threshold7d
		}
		if threshold > 0 && w.UsedPercent/100 >= threshold {
			return true
		}
	}
	return false
}

func validOpenAIResetWindow(w *OpenAIRateLimitWindow) bool {
	return w != nil && !w.invalidUsedPercent && !math.IsNaN(w.UsedPercent) && !math.IsInf(w.UsedPercent, 0) && w.UsedPercent >= 0 && w.UsedPercent <= 100
}

func openAIResetRecoveryObserved(before, after *OpenAIQuotaUsage, config OpenAIAutoResetConfig) bool {
	if before == nil || before.RateLimit == nil || after == nil || after.RateLimit == nil || after.RateLimit.LimitReached || openAIResetThresholdReached(after, config) {
		return false
	}
	observed := false
	for _, prior := range []*OpenAIRateLimitWindow{before.RateLimit.PrimaryWindow, before.RateLimit.SecondaryWindow} {
		if !validOpenAIResetWindow(prior) || (prior.LimitWindowSeconds != 5*3600 && prior.LimitWindowSeconds != 7*24*3600) {
			continue
		}
		matched := false
		for _, current := range []*OpenAIRateLimitWindow{after.RateLimit.PrimaryWindow, after.RateLimit.SecondaryWindow} {
			if validOpenAIResetWindow(current) && current.LimitWindowSeconds == prior.LimitWindowSeconds {
				matched = true
			}
		}
		if !matched {
			return false
		}
		observed = true
	}
	return observed
}

func selectOpenAIResetCredit(usage *OpenAIQuotaUsage) (string, error) {
	if usage == nil || usage.RateLimitResetCredits == nil || usage.RateLimitResetCredits.AvailableCount <= 0 {
		return "", infraerrors.Conflict("OPENAI_RESET_NO_CREDIT", "no reset credit is available")
	}
	candidates := append([]openAIResetCreditCandidate(nil), usage.resetCandidates...)
	if len(candidates) != usage.RateLimitResetCredits.AvailableCount {
		return "", infraerrors.Conflict("OPENAI_RESET_DETAILS_INCOMPLETE", "reset credit details are incomplete")
	}
	seen := make(map[string]bool)
	for _, c := range candidates {
		expires, err := time.Parse(time.RFC3339, c.ExpiresAt)
		if c.ID == "" || seen[c.ID] || err != nil || !expires.After(time.Now()) {
			return "", infraerrors.Conflict("OPENAI_RESET_DETAILS_INVALID", "reset credit details are invalid or expired")
		}
		seen[c.ID] = true
	}
	sort.Slice(candidates, func(i, j int) bool {
		a, _ := time.Parse(time.RFC3339, candidates[i].ExpiresAt)
		b, _ := time.Parse(time.RFC3339, candidates[j].ExpiresAt)
		return a.Before(b)
	})
	return candidates[0].ID, nil
}

func (s *OpenAIAutoResetService) reset(ctx context.Context, id int64, automatic bool) (*OpenAIQuotaResetResult, error) {
	a, err := s.account(ctx, id)
	if err != nil {
		return nil, err
	}
	if s.quota == nil {
		return nil, ErrOpenAIResetUnavailable
	}
	config := defaultOpenAIAutoResetConfig()
	if automatic {
		config, err = s.readConfig(ctx, a)
		if err != nil {
			return nil, err
		}
	}
	if automatic && (!config.Enabled || !openAIResetActive(a)) {
		return nil, nil
	}
	expected, err := cloneOpenAICodexSnapshotIdentity(a)
	if err != nil {
		return nil, err
	}
	expected.Extra = map[string]any{AccountExecutionNodeExtraKey: a.Extra[AccountExecutionNodeExtraKey]}
	if a.Proxy != nil {
		proxy := *a.Proxy
		expected.Proxy = &proxy
	}
	checkCurrent := func(callCtx context.Context, beforeSend bool) error {
		latest, err := s.account(callCtx, id)
		if err != nil {
			return err
		}
		if !automatic {
			if !sameOpenAIManualResetAccount(expected, latest) {
				return ErrOpenAIResetChanged
			}
			return callCtx.Err()
		}
		if !sameOpenAIResetAccount(expected, latest) {
			return ErrOpenAIResetChanged
		}
		if beforeSend {
			current, err := s.readConfig(callCtx, latest)
			if err != nil {
				return err
			}
			if current.Revision != config.Revision || !current.Enabled || !openAIResetActive(latest) {
				return ErrOpenAIResetChanged
			}
		}
		return callCtx.Err()
	}
	check := func(callCtx context.Context) error { return checkCurrent(callCtx, true) }
	// Keep ambiguous records beyond normal cleanup horizons, independent of
	// moving quota reset ETAs. Only a proven pre-send failure is retryable.
	forever := time.Date(9999, 1, 1, 0, 0, 0, 0, time.UTC)
	now := time.Now()
	record := &IdempotencyRecord{Scope: openAIResetScope, IdempotencyKeyHash: HashIdempotencyKey(strconv.FormatInt(id, 10)), RequestFingerprint: fmt.Sprint(id), Status: IdempotencyStatusProcessing, LockedUntil: &forever, ExpiresAt: forever}
	claimed, err := s.ledger.CreateProcessing(ctx, record)
	if err != nil {
		return nil, ErrOpenAIResetUnavailable
	}
	if !claimed {
		old, err := s.ledger.GetByScopeAndKeyHash(ctx, record.Scope, record.IdempotencyKeyHash)
		if err != nil || old == nil {
			return nil, ErrOpenAIResetUnavailable
		}
		if old.Status != IdempotencyStatusFailedRetryable {
			return nil, ErrOpenAIResetPending
		}
		if old.LockedUntil != nil && old.LockedUntil.After(now) {
			return nil, ErrOpenAIResetCooldown
		}
		claimed, err = s.ledger.TryReclaim(ctx, old.ID, old.Status, now, forever, forever)
		if err != nil {
			return nil, ErrOpenAIResetUnavailable
		}
		if !claimed {
			return nil, ErrOpenAIResetPending
		}
		record.ID = old.ID
	}
	if record.ID <= 0 {
		return nil, ErrOpenAIResetUnavailable
	}
	sent := false
	rejected := false
	defer func() {
		if sent && !rejected {
			return
		}
		cleanup, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		reason := "RESET_NOT_SENT"
		if rejected {
			reason = "RESET_REJECTED"
		}
		_ = s.ledger.MarkFailedRetryable(cleanup, record.ID, reason, time.Now().Add(5*time.Second), forever)
	}()
	if err := check(ctx); err != nil {
		return nil, err
	}
	if !automatic {
		// Keep the manual consume contract and handler's detached recovery /
		// partial-success reporting. Auto settings and credit-list availability
		// must not become prerequisites for a deliberate administrator action.
		redeemID, err := generateRedeemRequestID()
		if err != nil {
			return nil, err
		}
		result, err := s.quota.consumeResetCredit(ctx, id, "", redeemID, check, func() { sent = true })
		if err != nil {
			var refusal *openAIResetRejectedError
			if errors.As(err, &refusal) {
				rejected = true
				return nil, refusal.error
			}
			if sent {
				return nil, ErrOpenAIResetPending
			}
			return nil, err
		}
		if result == nil || !strings.EqualFold(result.Code, "ok") || result.WindowsReset <= 0 {
			return nil, ErrOpenAIResetPending
		}
		cleanup, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		// A storage failure keeps the protective latch, but must not hide a
		// confirmed consume from the existing manual recovery handler.
		_ = s.ledger.MarkFailedRetryable(cleanup, record.ID, "RESET_CONFIRMED", time.Now().Add(time.Minute), forever)
		return result, nil
	}
	usage, err := s.quota.QueryUsage(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := check(ctx); err != nil {
		return nil, err
	}
	if usage == nil || usage.requestIdentity == nil || !sameOpenAIResetCredentials(expected, usage.requestIdentity.credential) {
		return nil, ErrOpenAIResetChanged
	}
	if automatic && !openAIResetThresholdReached(usage, config) {
		return nil, nil
	}
	creditID, err := selectOpenAIResetCredit(usage)
	if err != nil {
		return nil, err
	}
	redeemID, err := generateRedeemRequestID()
	if err != nil {
		return nil, err
	}
	// OFF prevents a new send, but cannot retract an exchange already sent.
	// The transport owns its timeout; no background polling is needed here.
	callCtx := ctx
	result, err := s.quota.consumeResetCredit(callCtx, id, creditID, redeemID, check, func() { sent = true })
	if err != nil {
		var refusal *openAIResetRejectedError
		if errors.As(err, &refusal) {
			rejected = true
			return nil, refusal.error
		}
		if !sent {
			return nil, err
		}
		return nil, ErrOpenAIResetPending
	}
	if result == nil || !strings.EqualFold(result.Code, "ok") || result.WindowsReset <= 0 {
		return nil, ErrOpenAIResetPending
	}
	if err := checkCurrent(callCtx, false); err != nil {
		return nil, ErrOpenAIResetPending
	}
	if s.recoverer == nil {
		return nil, ErrOpenAIResetPending
	}
	if _, err := s.recoverer.RecoverAccountState(callCtx, id, AccountRecoveryOptions{InvalidateToken: true, ForceCleanup: true}); err != nil {
		return nil, ErrOpenAIResetPending
	}
	post, err := s.quota.QueryUsage(callCtx, id)
	if err != nil || post == nil || post.requestIdentity == nil ||
		!sameOpenAIResetCredentials(expected, post.requestIdentity.credential) ||
		!post.requestIdentity.observedAt.After(usage.requestIdentity.observedAt) {
		return nil, ErrOpenAIResetPending
	}
	if err := checkCurrent(callCtx, false); err != nil {
		return nil, ErrOpenAIResetPending
	}
	// A still-exhausted refreshed snapshot is not proof that another card is
	// needed. Require recovery before allowing a later independent operation.
	if !openAIResetRecoveryObserved(usage, post, config) {
		return nil, ErrOpenAIResetPending
	}
	if err := s.quota.CachePostResetSnapshot(callCtx, id, post); err != nil {
		return nil, ErrOpenAIResetPending
	}
	if err := s.ledger.MarkFailedRetryable(callCtx, record.ID, "RESET_CONFIRMED", time.Now().Add(time.Minute), forever); err != nil {
		return nil, ErrOpenAIResetPending
	}
	result.PostResetQuota = post
	return result, nil
}
