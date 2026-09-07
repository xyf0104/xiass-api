package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type resetTestSettings struct {
	SettingRepository
	mu     sync.Mutex
	values map[string]string
	reads  atomic.Int32
}

func (r *resetTestSettings) GetAll(context.Context) (map[string]string, error) {
	r.reads.Add(1)
	r.mu.Lock()
	defer r.mu.Unlock()
	values := make(map[string]string, len(r.values))
	for key, value := range r.values {
		values[key] = value
	}
	return values, nil
}

func (r *resetTestSettings) GetValue(_ context.Context, key string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if v, ok := r.values[key]; ok {
		return v, nil
	}
	return "", ErrSettingNotFound
}
func (r *resetTestSettings) Set(_ context.Context, key, value string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.values[key] = value
	return nil
}

type resetTestAccounts struct {
	AccountRepository
	mu     sync.Mutex
	a      *Account
	denied atomic.Bool
	reads  atomic.Int32
	checks atomic.Int32
}

func (r *resetTestAccounts) GetByID(_ context.Context, id int64) (*Account, error) {
	r.reads.Add(1)
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.a == nil || r.a.ID != id {
		return nil, ErrAccountNotFound
	}
	raw, _ := json.Marshal(r.a)
	var a Account
	_ = json.Unmarshal(raw, &a)
	return &a, nil
}
func (r *resetTestAccounts) CheckAccountManagementAccess(context.Context, int64) error {
	r.checks.Add(1)
	if r.denied.Load() {
		return ErrOpenAIResetChanged
	}
	return nil
}

type resetTestQuota struct {
	accounts       *resetTestAccounts
	queries        atomic.Int32
	consumes       atomic.Int32
	caches         atomic.Int32
	recoveries     atomic.Int32
	onQuery        func()
	onConsume      func(context.Context) error
	credit         string
	redeem         string
	keepExhausted  bool
	postWindowJSON string
	beforeSend     func()
}

func (q *resetTestQuota) QueryUsage(ctx context.Context, id int64) (*OpenAIQuotaUsage, error) {
	n := q.queries.Add(1)
	a, err := q.accounts.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	percent := 100.0
	if n > 1 && !q.keepExhausted {
		percent = 0
	}
	if q.onQuery != nil {
		q.onQuery()
	}
	window := &OpenAIRateLimitWindow{LimitWindowSeconds: 5 * 3600, UsedPercent: percent}
	if n > 1 && q.postWindowJSON != "" {
		if err := json.Unmarshal([]byte(q.postWindowJSON), &window); err != nil {
			return nil, err
		}
	}
	return &OpenAIQuotaUsage{RateLimit: &OpenAIRateLimit{PrimaryWindow: window}, RateLimitResetCredits: &OpenAIRateLimitResetCredits{AvailableCount: 1}, requestIdentity: &openAIQuotaRequestIdentity{target: a, credential: a, observedAt: time.Now()}, resetCandidates: []openAIResetCreditCandidate{{ID: "synthetic-credit", ExpiresAt: time.Now().Add(24 * time.Hour).UTC().Format(time.RFC3339)}}}, nil
}
func (q *resetTestQuota) CachePostResetSnapshot(context.Context, int64, *OpenAIQuotaUsage) error {
	q.caches.Add(1)
	return nil
}
func (q *resetTestQuota) RecoverAccountState(_ context.Context, _ int64, opts AccountRecoveryOptions) (*SuccessfulTestRecoveryResult, error) {
	if !opts.ForceCleanup || !opts.InvalidateToken {
		panic("reset recovery options lost")
	}
	q.recoveries.Add(1)
	return &SuccessfulTestRecoveryResult{}, nil
}
func (q *resetTestQuota) consumeResetCredit(ctx context.Context, _ int64, credit, redeem string, check func(context.Context) error, onSend func()) (*OpenAIQuotaResetResult, error) {
	if q.beforeSend != nil {
		q.beforeSend()
	}
	if err := check(ctx); err != nil {
		return nil, err
	}
	onSend()
	q.consumes.Add(1)
	q.credit, q.redeem = credit, redeem
	if q.onConsume != nil {
		if err := q.onConsume(ctx); err != nil {
			return nil, err
		}
	}
	return &OpenAIQuotaResetResult{Code: "ok", WindowsReset: 2}, nil
}
func resetFixture(t *testing.T, enabled bool) (*OpenAIAutoResetService, *resetTestQuota) {
	t.Helper()
	accounts := &resetTestAccounts{a: &Account{ID: 42, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusActive, Schedulable: true, Credentials: map[string]any{"access_token": "synthetic", "chatgpt_account_id": "synthetic-owner", "_token_version": 7}, Extra: map[string]any{"xiass_execution_node_id": "owner.example", "auto_reset_credit_enabled": true, "codex_7d_estimate_baseline": map[string]any{"test": "untouched"}}}}
	quota := &resetTestQuota{accounts: accounts}
	s := &OpenAIAutoResetService{accounts: accounts, settings: &resetTestSettings{values: make(map[string]string)}, ledger: newInMemoryIdempotencyRepo(), access: accounts, quota: quota, recoverer: quota}
	if enabled {
		_, err := s.SetConfig(context.Background(), 42, true, 1, 1)
		require.NoError(t, err)
	}
	return s, quota
}

func TestOpenAIAutoResetDefaultOffIgnoresImportedExtra(t *testing.T) {
	s, q := resetFixture(t, false)
	c, err := s.GetConfig(context.Background(), 42)
	require.NoError(t, err)
	require.False(t, c.Enabled)
	require.Empty(t, c.Principal)
	result, err := s.reset(context.Background(), 42, true)
	require.NoError(t, err)
	require.Nil(t, result)
	require.Zero(t, q.queries.Load())
	require.Zero(t, q.consumes.Load())
}

func TestOpenAIAutoResetExplicitConfigScopeAndValidation(t *testing.T) {
	s, q := resetFixture(t, false)
	_, err := s.SetConfig(context.Background(), 42, true, 0, 1)
	require.Error(t, err)
	c, err := s.SetConfig(context.Background(), 42, true, .9, .95)
	require.NoError(t, err)
	require.True(t, c.Enabled)
	require.NotEmpty(t, c.Revision)
	require.Empty(t, c.Principal)
	q.accounts.mu.Lock()
	q.accounts.a.Credentials["chatgpt_account_id"] = "replacement"
	q.accounts.mu.Unlock()
	c, err = s.GetConfig(context.Background(), 42)
	require.NoError(t, err)
	require.False(t, c.Enabled, "a replacement identity has not consented")
	for _, kind := range []string{AccountTypeAPIKey, AccountTypeSetupToken} {
		q.accounts.mu.Lock()
		q.accounts.a.Type = kind
		q.accounts.mu.Unlock()
		_, err = s.SetConfig(context.Background(), 42, true, 1, 1)
		require.Error(t, err)
	}
	q.accounts.denied.Store(true)
	_, err = s.reset(context.Background(), 42, false)
	require.Error(t, err)
	require.Zero(t, q.queries.Load())
}

func TestOpenAIAutoResetConfirmedUsesExistingCardAndPreservesAccount(t *testing.T) {
	s, q := resetFixture(t, true)
	before, _ := q.accounts.GetByID(context.Background(), 42)
	result, err := s.reset(context.Background(), 42, true)
	require.NoError(t, err)
	require.Equal(t, "ok", result.Code)
	require.Equal(t, int32(1), q.consumes.Load())
	require.Equal(t, "synthetic-credit", q.credit)
	require.NotEmpty(t, q.redeem)
	require.Equal(t, int32(1), q.caches.Load())
	require.Equal(t, int32(1), q.recoveries.Load())
	after, _ := q.accounts.GetByID(context.Background(), 42)
	require.Equal(t, before, after, "no weekly baseline, credentials, or manual schedulable edits")
}

func TestOpenAIAutoResetAmbiguousResultBlocksManualAutomaticAndRestart(t *testing.T) {
	for _, fail := range []string{"timeout", "still exhausted"} {
		t.Run(fail, func(t *testing.T) {
			s, q := resetFixture(t, true)
			if fail == "timeout" {
				q.onConsume = func(context.Context) error { return context.DeadlineExceeded }
			} else {
				q.keepExhausted = true
			}
			_, err := s.reset(context.Background(), 42, true)
			require.ErrorIs(t, err, ErrOpenAIResetPending)
			_, err = s.SetConfig(context.Background(), 42, false, 1, 1)
			require.NoError(t, err)
			_, err = s.SetConfig(context.Background(), 42, true, .9, .9)
			require.NoError(t, err)
			fresh := &OpenAIAutoResetService{accounts: s.accounts, settings: s.settings, ledger: s.ledger, access: s.access, quota: q, recoverer: q}
			for _, auto := range []bool{false, true} {
				_, err = fresh.reset(context.Background(), 42, auto)
				require.ErrorIs(t, err, ErrOpenAIResetPending)
			}
			require.Equal(t, int32(1), q.consumes.Load())
			c, err := fresh.GetConfig(context.Background(), 42)
			require.NoError(t, err)
			require.True(t, c.Pending)
		})
	}
}

func TestOpenAIAutoResetManualAndAutomaticShareOneClaim(t *testing.T) {
	s, q := resetFixture(t, true)
	entered, release := make(chan struct{}), make(chan struct{})
	q.onConsume = func(context.Context) error { close(entered); <-release; return nil }
	result := make(chan error, 1)
	go func() { _, err := s.reset(context.Background(), 42, false); result <- err }()
	<-entered
	other := &OpenAIAutoResetService{accounts: s.accounts, settings: s.settings, ledger: s.ledger, access: s.access, quota: q, recoverer: q}
	_, err := other.reset(context.Background(), 42, true)
	require.ErrorIs(t, err, ErrOpenAIResetPending)
	close(release)
	require.NoError(t, <-result)
	require.Equal(t, int32(1), q.consumes.Load())
}

func TestOpenAIAutoResetChecksBeforeSendAndFencesPostProcessing(t *testing.T) {
	for _, change := range []string{"switch", "credentials", "owner", "permission", "schedulable"} {
		for _, during := range []bool{false, true} {
			t.Run(change+"/"+fmtBool(during), func(t *testing.T) {
				s, q := resetFixture(t, true)
				mutate := func() {
					switch change {
					case "switch":
						_, _ = s.SetConfig(context.Background(), 42, false, 1, 1)
					case "permission":
						q.accounts.denied.Store(true)
					default:
						q.accounts.mu.Lock()
						defer q.accounts.mu.Unlock()
						switch change {
						case "credentials":
							q.accounts.a.Credentials["access_token"] = "new-token"
						case "owner":
							q.accounts.a.Extra[AccountExecutionNodeExtraKey] = "new-owner.example"
						case "schedulable":
							q.accounts.a.Schedulable = false
						}
					}
				}
				if during {
					q.onConsume = func(ctx context.Context) error {
						mutate()
						return ctx.Err()
					}
				} else {
					q.onQuery = mutate
				}
				_, err := s.reset(context.Background(), 42, true)
				if during && (change == "switch" || change == "schedulable") {
					require.NoError(t, err, "already-sent exchanges must finish despite OFF")
					require.Equal(t, int32(1), q.consumes.Load())
					require.Equal(t, int32(1), q.caches.Load())
					return
				}
				require.Error(t, err)
				if during {
					require.Equal(t, int32(1), q.consumes.Load())
					require.ErrorIs(t, err, ErrOpenAIResetPending)
				} else {
					require.Zero(t, q.consumes.Load())
				}
				require.Zero(t, q.caches.Load())
			})
		}
	}
}

func fmtBool(v bool) string {
	if v {
		return "in flight"
	}
	return "before send"
}

func TestOpenAIAutoResetCreditsFailClosedAndKeepIDsPrivate(t *testing.T) {
	parsed, err := parseOpenAIRateLimitResetCreditDetails([]byte(`{"available_count":1,"credits":[{"id":"secret-credit","expires_at":"2099-01-01T00:00:00Z","status":"available"}]}`))
	require.NoError(t, err)
	usage := &OpenAIQuotaUsage{RateLimitResetCredits: &OpenAIRateLimitResetCredits{AvailableCount: 1, Credits: parsed.Credits}, resetCandidates: parsed.Candidates}
	id, err := selectOpenAIResetCredit(usage)
	require.NoError(t, err)
	require.Equal(t, "secret-credit", id)
	encoded, err := json.Marshal(usage)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "secret-credit")
	usage.resetCandidates[0].ID = ""
	_, err = selectOpenAIResetCredit(usage)
	require.Error(t, err)
	usage.resetCandidates = nil
	_, err = selectOpenAIResetCredit(usage)
	require.Error(t, err)
}

func TestOpenAIAutoResetMissingPostWindowNeverReleasesClaim(t *testing.T) {
	for _, body := range []string{`null`, `{}`, `{"limit_window_seconds":18000}`, `{"limit_window_seconds":18000,"used_percent":null}`, `{"limit_window_seconds":18000,"used_percent":-1}`, `{"limit_window_seconds":604800,"used_percent":0}`} {
		t.Run(body, func(t *testing.T) {
			s, q := resetFixture(t, true)
			q.postWindowJSON = body
			_, err := s.reset(context.Background(), 42, true)
			require.ErrorIs(t, err, ErrOpenAIResetPending)
			_, err = s.reset(context.Background(), 42, false)
			require.ErrorIs(t, err, ErrOpenAIResetPending)
			require.Equal(t, int32(1), q.consumes.Load())
		})
	}
	s, q := resetFixture(t, true)
	q.postWindowJSON = `{"limit_window_seconds":18000,"used_percent":0}`
	_, err := s.reset(context.Background(), 42, true)
	require.NoError(t, err)
}

func TestOpenAIAutoResetPreSendCancellationDoesNotLatch(t *testing.T) {
	s, q := resetFixture(t, true)
	q.beforeSend = func() { _, _ = s.SetConfig(context.Background(), 42, false, 1, 1) }
	_, err := s.reset(context.Background(), 42, true)
	require.ErrorIs(t, err, ErrOpenAIResetChanged)
	require.Zero(t, q.consumes.Load())
	c, err := s.GetConfig(context.Background(), 42)
	require.NoError(t, err)
	require.False(t, c.Pending)
}

func TestOpenAIAutoResetThresholdsUseActualWindowsAndRawPercent(t *testing.T) {
	config := OpenAIAutoResetConfig{Threshold5h: .905, Threshold7d: .957}
	for _, tc := range []struct {
		seconds int64
		percent float64
		want    bool
	}{{18000, 90.49, false}, {18000, 90.5, true}, {604800, 95.69, false}, {604800, 95.7, true}, {3600, 100, false}, {18000, -1, false}, {18000, 101, false}} {
		usage := &OpenAIQuotaUsage{RateLimit: &OpenAIRateLimit{SecondaryWindow: &OpenAIRateLimitWindow{LimitWindowSeconds: tc.seconds, UsedPercent: tc.percent}}}
		require.Equal(t, tc.want, openAIResetThresholdReached(usage, config))
	}
}

func TestOpenAIAutoResetScanAllOffOnlyReadsSettingsOnce(t *testing.T) {
	s, q := resetFixture(t, false)
	settings := s.settings.(*resetTestSettings)
	settings.values[openAIResetConfigKey(42)] = `{"enabled":false}`
	settings.values["unrelated"] = `{"enabled":true}`
	settings.values[openAIResetConfigKey(43)] = `{"enabled":true}`
	settings.values[openAIResetConfigKey(44)] = `invalid`
	s.scan(context.Background())
	require.Equal(t, int32(1), settings.reads.Load())
	require.Zero(t, q.accounts.reads.Load())
	require.Zero(t, q.accounts.checks.Load())
	require.Zero(t, q.queries.Load())
	require.Zero(t, q.consumes.Load())
}

func TestOpenAIAutoResetScanOnlyEnabledOwner(t *testing.T) {
	for _, denied := range []bool{false, true} {
		t.Run(fmtBool(denied), func(t *testing.T) {
			s, q := resetFixture(t, true)
			settings := s.settings.(*resetTestSettings)
			settings.values[openAIResetConfigKey(43)] = `{"enabled":false}`
			settings.values[openAIResetConfigPrefix+"042"] = settings.values[openAIResetConfigKey(42)]
			q.accounts.denied.Store(denied)
			q.accounts.reads.Store(0)
			q.accounts.checks.Store(0)
			s.scan(context.Background())
			require.Equal(t, int32(1), settings.reads.Load())
			if denied {
				require.Equal(t, int32(1), q.accounts.checks.Load())
				require.Zero(t, q.accounts.reads.Load())
				require.Zero(t, q.queries.Load())
				require.Zero(t, q.consumes.Load())
			} else {
				require.Equal(t, int32(1), q.consumes.Load())
			}
		})
	}
}

func TestOpenAIAutoResetManualKeepsExistingConsumeAndPostProcessing(t *testing.T) {
	s, q := resetFixture(t, false)
	s.settings = nil
	s.recoverer = nil
	q.accounts.a.Schedulable = false
	result, err := s.reset(context.Background(), 42, false)
	require.NoError(t, err)
	require.Equal(t, "ok", result.Code)
	require.Nil(t, result.PostResetQuota, "the manual handler retains recovery and partial-success reporting")
	require.Empty(t, q.credit, "manual reset lets upstream select the credit without requiring its detail endpoint")
	require.NotEmpty(t, q.redeem)
	require.Zero(t, q.queries.Load())
	require.Zero(t, q.recoveries.Load())
	require.Equal(t, int32(1), q.consumes.Load())
	_, err = s.reset(context.Background(), 42, false)
	require.ErrorIs(t, err, ErrOpenAIResetCooldown, "confirmed manual reset retains a bounded, non-pending cooldown")
}

func TestOpenAIAutoResetManualIgnoresOnlyAgentTaskRenewal(t *testing.T) {
	s, q := resetFixture(t, false)
	q.accounts.a.Credentials["auth_mode"] = OpenAIAuthModeAgentIdentity
	q.accounts.a.Credentials["task_id"] = "old-task"
	q.beforeSend = func() { q.accounts.a.Credentials["task_id"] = "renewed-task" }
	_, err := s.reset(context.Background(), 42, false)
	require.NoError(t, err)
	s, q = resetFixture(t, false)
	q.accounts.a.Credentials["auth_mode"] = OpenAIAuthModeAgentIdentity
	q.beforeSend = func() { q.accounts.a.Credentials["agent_private_key"] = "replacement" }
	_, err = s.reset(context.Background(), 42, false)
	require.ErrorIs(t, err, ErrOpenAIResetChanged)
	require.Zero(t, q.consumes.Load())
}

func TestOpenAIAutoResetOffDuringSendWaitsWithoutPolling(t *testing.T) {
	s, q := resetFixture(t, true)
	q.onConsume = func(ctx context.Context) error {
		_, err := s.SetConfig(context.Background(), 42, false, 1, 1)
		require.NoError(t, err)
		reads, checks := q.accounts.reads.Load(), q.accounts.checks.Load()
		select {
		case <-ctx.Done():
			t.Fatal("OFF cannot retract an already-sent exchange")
		case <-time.After(650 * time.Millisecond):
		}
		require.Equal(t, reads, q.accounts.reads.Load(), "no 4Hz account reads while transport is in flight")
		require.Equal(t, checks, q.accounts.checks.Load(), "no 4Hz permission checks while transport is in flight")
		return nil
	}
	_, err := s.reset(context.Background(), 42, true)
	require.NoError(t, err)
	c, err := s.GetConfig(context.Background(), 42)
	require.NoError(t, err)
	require.False(t, c.Enabled)
	require.False(t, c.Pending)
	result, err := s.reset(context.Background(), 42, true)
	require.NoError(t, err)
	require.Nil(t, result)
	require.Equal(t, int32(1), q.consumes.Load())
}

func TestOpenAIAutoResetManualHTTPRefusalVersusAmbiguousResult(t *testing.T) {
	for _, status := range []int{401, 403, 500, 502} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			s, q := resetFixture(t, false)
			account := q.accounts.a
			repo := &stubQuotaAccountRepo{accounts: map[int64]*Account{42: account}}
			tokenCache := &stubQuotaTokenCache{tokens: map[string]string{OpenAITokenCacheKey(account): "synthetic"}}
			calls := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				require.Equal(t, http.MethodPost, r.Method)
				w.Header().Set("content-type", "application/json")
				w.WriteHeader(status)
				_, _ = w.Write([]byte(`{"error":"synthetic rejection"}`))
			}))
			defer srv.Close()
			quota := NewOpenAIQuotaService(repo, nil, NewOpenAITokenProvider(repo, tokenCache, nil), newQuotaRedirectingFactory(srv))
			quota.autoReset, s.quota = s, quota
			_, err := quota.ResetCredit(context.Background(), 42)
			require.Error(t, err)
			c, configErr := s.GetConfig(context.Background(), 42)
			require.NoError(t, configErr)
			if status == 401 || status == 403 {
				require.False(t, c.Pending)
				require.ErrorContains(t, err, "no credit was consumed")
				_, err = quota.ResetCredit(context.Background(), 42)
				require.ErrorIs(t, err, ErrOpenAIResetCooldown)
				ledger := s.ledger.(*inMemoryIdempotencyRepo)
				ledger.mu.Lock()
				for _, record := range ledger.data {
					past := time.Now().Add(-time.Second)
					record.LockedUntil = &past
				}
				ledger.mu.Unlock()
				_, err = quota.ResetCredit(context.Background(), 42)
				require.ErrorContains(t, err, "no credit was consumed")
				require.Equal(t, 2, calls, "definite refusal is retryable after bounded cooldown")
			} else {
				require.True(t, c.Pending)
				require.ErrorIs(t, err, ErrOpenAIResetPending)
				_, err = quota.ResetCredit(context.Background(), 42)
				require.ErrorIs(t, err, ErrOpenAIResetPending)
				require.Equal(t, 1, calls, "uncertain server errors must not spend another credit")
			}
		})
	}
}

func TestOpenAIAutoResetManualDisconnectRemainsPending(t *testing.T) {
	s, q := resetFixture(t, false)
	q.onConsume = func(context.Context) error { return errors.New("connection lost after send") }
	_, err := s.reset(context.Background(), 42, false)
	require.ErrorIs(t, err, ErrOpenAIResetPending)
	c, err := s.GetConfig(context.Background(), 42)
	require.NoError(t, err)
	require.True(t, c.Pending)
	require.Equal(t, int32(1), q.consumes.Load())
}
