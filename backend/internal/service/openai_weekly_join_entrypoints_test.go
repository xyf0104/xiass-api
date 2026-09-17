//go:build unit

package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/imroc/req/v3"
	"github.com/stretchr/testify/require"
)

type weeklyJoinEntryRepo struct {
	AccountRepository
	mu              sync.Mutex
	rows            map[int64]*Account
	nextID          int64
	beforeCreate    func(*Account)
	casCalls        int
	casApplied      int
	ledgerUSD       float64
	fenceOnUpdate   bool
	postUpdateToken string
	returnedExtra   map[string]any
	lastCASRevision any
}

func cloneWeeklyJoinEntryAccount(a *Account) *Account {
	if a == nil {
		return nil
	}
	copy := *a
	copy.Credentials, _ = cloneAccountJSONMap(a.Credentials)
	copy.Extra, _ = cloneAccountJSONMap(a.Extra)
	copy.ProxyID = cloneAccountValuePointer(a.ProxyID)
	return &copy
}

func (r *weeklyJoinEntryRepo) GetByID(_ context.Context, id int64) (*Account, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return cloneWeeklyJoinEntryAccount(r.rows[id]), nil
}

func (r *weeklyJoinEntryRepo) GetByCRSAccountID(_ context.Context, id string) (*Account, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, a := range r.rows {
		if a.Extra["crs_account_id"] == id {
			return cloneWeeklyJoinEntryAccount(a), nil
		}
	}
	return nil, nil
}

func (r *weeklyJoinEntryRepo) Create(ctx context.Context, a *Account) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if r.beforeCreate != nil {
		r.beforeCreate(a)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.nextID++
	a.ID = r.nextID
	if r.rows == nil {
		r.rows = make(map[int64]*Account)
	}
	r.rows[a.ID] = cloneWeeklyJoinEntryAccount(a)
	return nil
}

func (r *weeklyJoinEntryRepo) Update(_ context.Context, a *Account) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	current := r.rows[a.ID]
	copy := cloneWeeklyJoinEntryAccount(a)
	copy.Extra = stripOpenAIQuotaRuntimeExtra(copy.Extra)
	principalChanged := openAIWeeklyJoinPrincipalOf(current).replacedBy(copy)
	if !principalChanged {
		for key, value := range current.Extra {
			if IsOpenAIQuotaRuntimeExtraKey(key) {
				copy.Extra[key] = value
			}
		}
	}
	a.Extra, _ = cloneAccountJSONMap(copy.Extra)
	r.returnedExtra, _ = cloneAccountJSONMap(a.Extra)
	if principalChanged && r.fenceOnUpdate {
		// Model a BEFORE trigger changing the durable row but not the caller's
		// object, which is exactly the entry-point integration boundary.
		copy.Extra["codex_7d_estimate_revision"] = current.Extra["codex_7d_estimate_revision"].(float64) + 1
	}
	if r.postUpdateToken != "" {
		copy.Credentials["access_token"] = r.postUpdateToken
	}
	r.rows[a.ID] = copy
	return nil
}

func (r *weeklyJoinEntryRepo) ListShadowsByParent(context.Context, int64) ([]*Account, error) {
	return nil, nil
}

func (r *weeklyJoinEntryRepo) UpdateOpenAICodexSnapshotIfIdentityMatches(_ context.Context, expected *Account, patch map[string]any) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.casCalls++
	r.lastCASRevision = expected.Extra["codex_7d_estimate_revision"]
	current := r.rows[expected.ID]
	if !quotaTestIdentityMatches(current, expected) {
		return false, nil
	}
	for key := range patch {
		if key == openAIWeeklyEstimateBaselineKey || key == "codex_7d_estimate_epoch" {
			panic("entry point must not invent a reused-row cost baseline")
		}
	}
	mergeAccountExtra(current, patch)
	r.casApplied++
	return true, nil
}

func (r *weeklyJoinEntryRepo) UpdateExtra(context.Context, int64, map[string]any) error {
	panic("unbound entry-point write")
}

type weeklyJoinEntryProxyRepo struct {
	ProxyRepository
	proxy *Proxy
}

func (r *weeklyJoinEntryProxyRepo) GetByID(_ context.Context, id int64) (*Proxy, error) {
	if r.proxy == nil || r.proxy.ID != id {
		return nil, fmt.Errorf("unresolved proxy")
	}
	copy := *r.proxy
	return &copy, nil
}

func (r *weeklyJoinEntryProxyRepo) ListActive(context.Context) ([]Proxy, error) {
	return []Proxy{*r.proxy}, nil
}

func weeklyJoinEntryQuotaServer(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodGet, r.Method)
		require.Equal(t, "/backend-api/wham/usage", r.URL.Path, "capture has no enrichment, privacy or refresh side effects")
		handler(w, r)
	}))
	t.Cleanup(server.Close)
	return server
}

func writeWeeklyJoinEntryQuota(w http.ResponseWriter, account, user string, percent float64) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"account_id": account, "user_id": user,
		"rate_limit": map[string]any{"secondary_window": map[string]any{
			"used_percent": percent, "limit_window_seconds": 604800, "reset_at": time.Now().Add(3 * 24 * time.Hour).Unix()}}})
}

func weeklyJoinCRSSource(id, principal, user string, extra map[string]any) map[string]any {
	return map[string]any{"kind": "openai", "id": id, "name": "join-test", "isActive": true, "schedulable": true,
		"credentials": map[string]any{"access_token": "synthetic-new-token", "chatgpt_account_id": principal, "chatgpt_user_id": user}, "extra": extra}
}

func runWeeklyJoinEntryCRS(t *testing.T, ctx context.Context, svc *CRSSyncService, sources []map[string]any, syncProxies bool) *SyncFromCRSResult {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/web/auth/login" {
			_, _ = w.Write([]byte(`{"success":true,"token":"synthetic-crs-token"}`))
			return
		}
		require.Equal(t, "/admin/sync/export-accounts", r.URL.Path)
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "data": map[string]any{"openaiOAuthAccounts": sources}})
	}))
	defer server.Close()
	svc.cfg = &config.Config{}
	svc.cfg.Security.URLAllowlist.AllowInsecureHTTP = true
	result, err := svc.SyncFromCRS(ctx, SyncFromCRSInput{BaseURL: server.URL, Username: "synthetic", Password: "synthetic", SyncProxies: syncProxies})
	require.NoError(t, err)
	return result
}

func TestWeeklyJoinEntryCRSCreatesOnlyObservedBaselineBeforeScheduling(t *testing.T) {
	for _, percent := range []float64{0, 20.4} {
		t.Run(strconv.FormatFloat(percent, 'f', -1, 64), func(t *testing.T) {
			var reads atomic.Int64
			server := weeklyJoinEntryQuotaServer(t, func(w http.ResponseWriter, r *http.Request) {
				reads.Add(1)
				require.Equal(t, "Bearer synthetic-new-token", r.Header.Get("Authorization"))
				writeWeeklyJoinEntryQuota(w, "new-account", "new-user", percent)
			})
			repo := &weeklyJoinEntryRepo{beforeCreate: func(a *Account) {
				require.EqualValues(t, 1, reads.Load())
				require.Zero(t, a.ID)
				state, ok := readOpenAIWeeklyFrozenEstimateState(a.Extra)
				require.True(t, ok)
				require.Equal(t, percent, state.BaselinePercent)
				require.Zero(t, state.BaselineCost)
				require.Equal(t, "pre_create_quota_read", state.BaselineSource)
				require.NotEqual(t, "imported-epoch", a.Extra["codex_7d_estimate_epoch"])
				require.True(t, a.Schedulable)
				require.Equal(t, true, a.Extra["custom"])
				require.Equal(t, "device", a.Extra[codexFingerprintModeExtraKey])
				require.NotEmpty(t, a.Extra[codexFingerprintSeedExtraKey])
				require.NotEqual(t, "untrusted-imported-seed", a.Extra[codexFingerprintSeedExtraKey])
			}}
			svc := &CRSSyncService{accountRepo: repo, weeklyJoinCaptureFactory: newQuotaRedirectingFactory(server)}
			source := weeklyJoinCRSSource("crs-new", "new-account", "new-user", map[string]any{
				"custom": true, "codex_7d_estimate_epoch": "imported-epoch", openAIWeeklyEstimateBaselineKey: map[string]any{"forged": true},
				"codex_7d_used_percent": 0.0, codexFingerprintModeExtraKey: "device", codexFingerprintSeedExtraKey: "untrusted-imported-seed"})
			result := runWeeklyJoinEntryCRS(t, context.Background(), svc, []map[string]any{source}, false)
			require.Equal(t, 1, result.Created)
		})
	}
}

func TestWeeklyJoinEntryCRSFailureAndUnresolvedProxyNeverInventBaseline(t *testing.T) {
	for _, reason := range []string{"upstream failure", "unresolved source proxy", "exhausted budget"} {
		t.Run(reason, func(t *testing.T) {
			var calls atomic.Int64
			server := weeklyJoinEntryQuotaServer(t, func(w http.ResponseWriter, _ *http.Request) { calls.Add(1); w.WriteHeader(http.StatusUnauthorized) })
			repo := &weeklyJoinEntryRepo{}
			svc := &CRSSyncService{accountRepo: repo, weeklyJoinCaptureFactory: newQuotaRedirectingFactory(server)}
			source := weeklyJoinCRSSource("crs-new", "new-account", "new-user", map[string]any{
				openAIWeeklyEstimateBaselineKey: map[string]any{"forged": true}, "codex_7d_estimate_epoch": "forged", "codex_7d_used_percent": 0.0, "custom": true})
			ctx := context.Background()
			if reason == "unresolved source proxy" {
				source["proxy"] = map[string]any{"protocol": "socks5", "host": "synthetic.invalid", "port": 1080}
			}
			if reason == "exhausted budget" {
				ctx = WithOpenAIWeeklyJoinCaptureBudget(ctx, -time.Second)
			}
			result := runWeeklyJoinEntryCRS(t, ctx, svc, []map[string]any{source}, false)
			require.Equal(t, 1, result.Created)
			if reason != "upstream failure" {
				require.Zero(t, calls.Load(), "unresolved proxy must not fall back to direct egress")
			}
			stored, _ := repo.GetByID(ctx, 1)
			for key := range stored.Extra {
				require.False(t, IsOpenAIQuotaRuntimeExtraKey(key), key)
			}
			require.True(t, stored.Schedulable)
			require.Equal(t, true, stored.Extra["custom"])
		})
	}
}

func weeklyJoinEntryExisting() *Account {
	return &Account{ID: 42, Name: "existing", Platform: PlatformOpenAI, Type: AccountTypeOAuth, Schedulable: true,
		Credentials: map[string]any{"access_token": "synthetic-old-token", "chatgpt_account_id": "old-account", "chatgpt_user_id": "old-user"},
		Extra: map[string]any{"crs_account_id": "crs-existing", "custom": true,
			"codex_7d_estimate_epoch": "old-epoch", openAIWeeklyEstimateBaselineKey: map[string]any{"historical": true},
			"codex_7d_used_percent": 70.0, codexFingerprintModeExtraKey: "device", codexFingerprintSeedExtraKey: "348f553b-094b-4f80-b85b-3507a0d560d6"}}
}

func TestWeeklyJoinEntryCRSReplacementAndSameIdentityRefresh(t *testing.T) {
	for _, sameIdentity := range []bool{false, true} {
		t.Run(strconv.FormatBool(sameIdentity), func(t *testing.T) {
			old := weeklyJoinEntryExisting()
			repo := &weeklyJoinEntryRepo{rows: map[int64]*Account{42: cloneWeeklyJoinEntryAccount(old)}, ledgerUSD: 853.5}
			var calls atomic.Int64
			server := weeklyJoinEntryQuotaServer(t, func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				require.Equal(t, "Bearer synthetic-new-token", r.Header.Get("Authorization"))
				writeWeeklyJoinEntryQuota(w, "new-account", "new-user", 11)
			})
			svc := &CRSSyncService{accountRepo: repo, weeklyJoinCaptureFactory: newQuotaRedirectingFactory(server)}
			principal, user := "new-account", "new-user"
			if sameIdentity {
				principal, user = "old-account", "old-user"
			}
			source := weeklyJoinCRSSource("crs-existing", principal, user, map[string]any{
				"codex_7d_used_percent": 0.0, openAIWeeklyEstimateBaselineKey: map[string]any{"forged": true}, codexFingerprintSeedExtraKey: "untrusted"})
			result := runWeeklyJoinEntryCRS(t, context.Background(), svc, []map[string]any{source}, false)
			require.Equal(t, 1, result.Updated)
			stored, _ := repo.GetByID(context.Background(), 42)
			require.Equal(t, 853.5, repo.ledgerUSD)
			require.Equal(t, old.Extra[codexFingerprintSeedExtraKey], stored.Extra[codexFingerprintSeedExtraKey])
			if sameIdentity {
				require.Zero(t, calls.Load())
				require.Equal(t, old.Extra[openAIWeeklyEstimateBaselineKey], stored.Extra[openAIWeeklyEstimateBaselineKey])
				require.Equal(t, "old-epoch", stored.Extra["codex_7d_estimate_epoch"])
			} else {
				require.EqualValues(t, 1, calls.Load())
				require.Equal(t, 1, repo.casApplied)
				require.Equal(t, 11.0, stored.Extra["codex_7d_used_percent"])
				require.NotContains(t, stored.Extra, openAIWeeklyEstimateBaselineKey, "old billed rows are not zero-cost new rows")
				require.NotContains(t, stored.Extra, "codex_7d_estimate_epoch")
			}
		})
	}
}

func TestWeeklyJoinEntryAdminDetectsActualPrincipalNotTokenRotation(t *testing.T) {
	for _, kind := range []string{"same principal reauth", "account replacement", "user replacement"} {
		t.Run(kind, func(t *testing.T) {
			old := weeklyJoinEntryExisting()
			repo := &weeklyJoinEntryRepo{rows: map[int64]*Account{42: cloneWeeklyJoinEntryAccount(old)}, ledgerUSD: 853.5}
			principal, user := "old-account", "old-user"
			if kind == "account replacement" {
				principal = "new-account"
			}
			if kind == "user replacement" {
				user = "new-user"
			}
			var calls atomic.Int64
			server := weeklyJoinEntryQuotaServer(t, func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				require.Equal(t, "Bearer synthetic-new-token", r.Header.Get("Authorization"))
				writeWeeklyJoinEntryQuota(w, principal, user, 20.4)
			})
			svc := &adminServiceImpl{accountRepo: repo, privacyClientFactory: newQuotaRedirectingFactory(server)}
			updated, err := svc.UpdateAccount(context.Background(), 42, &UpdateAccountInput{
				Credentials:               map[string]any{"access_token": "synthetic-new-token", "chatgpt_account_id": principal, "chatgpt_user_id": user},
				ResetOpenAIWeeklyEstimate: kind == "same principal reauth"})
			require.NoError(t, err)
			require.Equal(t, 853.5, repo.ledgerUSD)
			require.Equal(t, old.Extra[codexFingerprintSeedExtraKey], updated.Extra[codexFingerprintSeedExtraKey])
			if kind == "same principal reauth" {
				require.Zero(t, calls.Load())
				require.Equal(t, old.Extra[openAIWeeklyEstimateBaselineKey], updated.Extra[openAIWeeklyEstimateBaselineKey])
			} else {
				require.EqualValues(t, 1, calls.Load())
				require.Equal(t, 20.4, updated.Extra["codex_7d_used_percent"])
				require.NotContains(t, updated.Extra, openAIWeeklyEstimateBaselineKey)
			}
		})
	}
}

func TestWeeklyJoinEntryDuplicateDoesNotInheritAnyQuotaState(t *testing.T) {
	repo := newDuplicateAccountRepoStub()
	source := weeklyJoinEntryExisting()
	source.Type = AccountTypeAPIKey
	source.Extra[codexFingerprintModeExtraKey] = "off"
	source.Extra["codex_7d_estimate_future_version"] = map[string]any{"opaque": true}
	source.Extra["codex_reset_credit_snapshot"] = map[string]any{"available_count": 3.0}
	require.NoError(t, repo.Create(context.Background(), source))
	before := cloneWeeklyJoinEntryAccount(source)
	var calls atomic.Int64
	server := weeklyJoinEntryQuotaServer(t, func(http.ResponseWriter, *http.Request) { calls.Add(1) })
	svc := &adminServiceImpl{accountRepo: repo, accountDuplicateRepo: repo, privacyClientFactory: newQuotaRedirectingFactory(server)}
	duplicate, err := svc.DuplicateAccount(context.Background(), source.ID, "admin:join-test", "")
	require.NoError(t, err)
	for key := range duplicate.Extra {
		require.False(t, IsOpenAIQuotaRuntimeExtraKey(key), key)
	}
	require.Equal(t, true, duplicate.Extra["custom"])
	require.False(t, duplicate.Schedulable)
	require.Zero(t, calls.Load(), "non-OAuth copies do not probe OAuth quota")
	require.Equal(t, before.Credentials, source.Credentials)
	require.Equal(t, before.Extra, source.Extra)
	oauthSource := weeklyJoinEntryExisting()
	require.NoError(t, repo.Create(context.Background(), oauthSource))
	oauthCopy, err := svc.DuplicateAccount(context.Background(), oauthSource.ID, "admin:join-test", "")
	require.NoError(t, err)
	require.True(t, oauthCopy.IsOpenAIOAuthCredentialCopy())
	require.Equal(t, oauthSource.ID, oauthCopy.OpenAIOAuthCredentialSourceID())
	require.True(t, oauthCopy.Schedulable)
	for key := range oauthCopy.Extra {
		require.False(t, IsOpenAIQuotaRuntimeExtraKey(key), key)
	}
	require.NotContains(t, oauthCopy.Extra, openAIWeeklyEstimateBaselineKey)
	require.NotContains(t, oauthCopy.Extra, "codex_7d_estimate_epoch")
}

func TestWeeklyJoinEntryPrincipalCaptureUsesProxyAndRejectsLateTokenOrProxy(t *testing.T) {
	for _, replacement := range []string{"none", "token", "proxy"} {
		t.Run(replacement, func(t *testing.T) {
			old := weeklyJoinEntryExisting()
			proxy := &Proxy{ID: 17, Protocol: "socks5", Host: "synthetic-proxy.invalid", Port: 1080, Username: "synthetic", Password: "synthetic"}
			old.ProxyID = &proxy.ID
			repo := &weeklyJoinEntryRepo{rows: map[int64]*Account{42: cloneWeeklyJoinEntryAccount(old)}, ledgerUSD: 853.5}
			started, release := make(chan struct{}), make(chan struct{})
			server := weeklyJoinEntryQuotaServer(t, func(w http.ResponseWriter, r *http.Request) {
				require.Equal(t, "Bearer synthetic-new-token", r.Header.Get("Authorization"))
				require.Equal(t, "new-account", r.Header.Get("Chatgpt-Account-Id"))
				close(started)
				<-release
				writeWeeklyJoinEntryQuota(w, "new-account", "new-user", 25.4)
			})
			redirect := newQuotaRedirectingFactory(server)
			svc := &adminServiceImpl{accountRepo: repo, proxyRepo: &weeklyJoinEntryProxyRepo{proxy: proxy}, privacyClientFactory: func(url string) (*req.Client, error) {
				require.Equal(t, proxy.URL(), url)
				return redirect(url)
			}}
			done := make(chan error, 1)
			go func() {
				_, err := svc.UpdateAccount(context.Background(), 42, &UpdateAccountInput{Credentials: map[string]any{
					"access_token": "synthetic-new-token", "chatgpt_account_id": "new-account", "chatgpt_user_id": "new-user"}})
				done <- err
			}()
			select {
			case <-started:
			case err := <-done:
				t.Fatalf("update ended before quota request: %v", err)
			}
			if replacement != "none" {
				repo.mu.Lock()
				if replacement == "token" {
					repo.rows[42].Credentials["access_token"] = "synthetic-later-token"
				}
				if replacement == "proxy" {
					nextProxy := int64(99)
					repo.rows[42].ProxyID = &nextProxy
				}
				repo.rows[42].Extra["codex_7d_used_percent"] = 66.0
				repo.mu.Unlock()
			}
			close(release)
			require.NoError(t, <-done)
			stored, _ := repo.GetByID(context.Background(), 42)
			require.Equal(t, 1, repo.casCalls)
			if replacement == "none" {
				require.Equal(t, 1, repo.casApplied)
				require.Equal(t, 25.4, stored.Extra["codex_7d_used_percent"])
			} else {
				require.Zero(t, repo.casApplied)
				require.Equal(t, 66.0, stored.Extra["codex_7d_used_percent"], "late old observation cannot overwrite the new request identity")
			}
			require.Equal(t, 853.5, repo.ledgerUSD)
		})
	}
}

func TestWeeklyJoinEntryReplacementFailuresNeverProbeDirectOrSeedZero(t *testing.T) {
	for _, reason := range []string{"missing proxy repo", "proxy not found", "http failure"} {
		t.Run(reason, func(t *testing.T) {
			old := weeklyJoinEntryExisting()
			if reason != "http failure" {
				proxyID := int64(17)
				old.ProxyID = &proxyID
			}
			repo := &weeklyJoinEntryRepo{rows: map[int64]*Account{42: cloneWeeklyJoinEntryAccount(old)}}
			var calls atomic.Int64
			server := weeklyJoinEntryQuotaServer(t, func(w http.ResponseWriter, _ *http.Request) { calls.Add(1); w.WriteHeader(http.StatusUnauthorized) })
			svc := &adminServiceImpl{accountRepo: repo, privacyClientFactory: newQuotaRedirectingFactory(server)}
			if reason == "proxy not found" {
				svc.proxyRepo = &weeklyJoinEntryProxyRepo{}
			}
			updated, err := svc.UpdateAccount(context.Background(), 42, &UpdateAccountInput{Credentials: map[string]any{
				"access_token": "synthetic-new-token", "chatgpt_account_id": "new-account", "chatgpt_user_id": "new-user"}})
			require.NoError(t, err)
			require.True(t, updated.Schedulable)
			require.NotContains(t, updated.Extra, "codex_7d_used_percent")
			require.NotContains(t, updated.Extra, openAIWeeklyEstimateBaselineKey)
			require.NotContains(t, updated.Extra, "codex_7d_estimate_epoch")
			require.Equal(t, old.Extra[codexFingerprintSeedExtraKey], updated.Extra[codexFingerprintSeedExtraKey])
			if reason != "http failure" {
				require.Zero(t, calls.Load())
			}
			require.Zero(t, repo.casCalls)
		})
	}
}

func TestWeeklyJoinEntryCRSBatchSharesOptionalReadBudget(t *testing.T) {
	var calls atomic.Int64
	server := weeklyJoinEntryQuotaServer(t, func(_ http.ResponseWriter, r *http.Request) { calls.Add(1); <-r.Context().Done() })
	repo := &weeklyJoinEntryRepo{}
	svc := &CRSSyncService{accountRepo: repo, weeklyJoinCaptureFactory: newQuotaRedirectingFactory(server)}
	sources := make([]map[string]any, 5)
	for i := range sources {
		sources[i] = weeklyJoinCRSSource(fmt.Sprint(i), "new-account", "new-user", nil)
	}
	started := time.Now()
	ctx := WithOpenAIWeeklyJoinCaptureBudget(context.Background(), 100*time.Millisecond)
	result := runWeeklyJoinEntryCRS(t, ctx, svc, sources, false)
	require.Equal(t, len(sources), result.Created)
	require.NoError(t, ctx.Err())
	require.LessOrEqual(t, calls.Load(), int64(1))
	require.Less(t, time.Since(started), 2*time.Second)
}

func TestWeeklyJoinEntryCRSCaptureUsesResolvedLocalProxy(t *testing.T) {
	proxy := &Proxy{ID: 17, Protocol: "socks5", Host: "synthetic-proxy.invalid", Port: 1080, Status: StatusActive}
	server := weeklyJoinEntryQuotaServer(t, func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "Bearer synthetic-new-token", r.Header.Get("Authorization"))
		writeWeeklyJoinEntryQuota(w, "new-account", "new-user", 20.4)
	})
	redirect := newQuotaRedirectingFactory(server)
	var factories atomic.Int64
	repo := &weeklyJoinEntryRepo{beforeCreate: func(a *Account) {
		require.Equal(t, proxy.ID, *a.ProxyID)
		require.Equal(t, 20.4, a.Extra["codex_7d_used_percent"])
	}}
	svc := &CRSSyncService{accountRepo: repo, proxyRepo: &weeklyJoinEntryProxyRepo{proxy: proxy}, weeklyJoinCaptureFactory: func(url string) (*req.Client, error) {
		factories.Add(1)
		require.Equal(t, proxy.URL(), url)
		return redirect(url)
	}}
	source := weeklyJoinCRSSource("crs-proxy", "new-account", "new-user", nil)
	source["proxy"] = map[string]any{"protocol": "socks5", "host": proxy.Host, "port": proxy.Port}
	result := runWeeklyJoinEntryCRS(t, context.Background(), svc, []map[string]any{source}, true)
	require.Equal(t, 1, result.Created)
	require.EqualValues(t, 1, factories.Load())
}

func TestWeeklyJoinEntryReplacementUsesAuthoritativeTriggerRevision(t *testing.T) {
	for _, newerWrite := range []bool{false, true} {
		t.Run(strconv.FormatBool(newerWrite), func(t *testing.T) {
			old := weeklyJoinEntryExisting()
			old.Extra["codex_7d_estimate_revision"] = 7.0
			repo := &weeklyJoinEntryRepo{rows: map[int64]*Account{42: cloneWeeklyJoinEntryAccount(old)}, fenceOnUpdate: true}
			if newerWrite {
				repo.postUpdateToken = "synthetic-newer-token"
			}
			var calls atomic.Int64
			server := weeklyJoinEntryQuotaServer(t, func(w http.ResponseWriter, _ *http.Request) {
				calls.Add(1)
				saved, err := repo.GetByID(context.Background(), 42)
				require.NoError(t, err)
				require.Equal(t, 8.0, saved.Extra["codex_7d_estimate_revision"])
				require.NotContains(t, saved.Extra, "codex_7d_used_percent", "capture starts after the identity write and trigger")
				writeWeeklyJoinEntryQuota(w, "new-account", "new-user", 20.4)
			})
			svc := &adminServiceImpl{accountRepo: repo, privacyClientFactory: newQuotaRedirectingFactory(server)}
			updated, err := svc.UpdateAccount(context.Background(), 42, &UpdateAccountInput{Credentials: map[string]any{
				"access_token": "synthetic-new-token", "chatgpt_account_id": "new-account", "chatgpt_user_id": "new-user"}})
			require.NoError(t, err)
			require.NotContains(t, repo.returnedExtra, "codex_7d_estimate_revision", "Update's local object is intentionally not authoritative")
			require.Equal(t, 8.0, updated.Extra["codex_7d_estimate_revision"])
			require.NotContains(t, updated.Extra, openAIWeeklyEstimateBaselineKey)
			if newerWrite {
				require.Zero(t, calls.Load(), "do not recapture using an identity different from the requested update")
				require.Zero(t, repo.casCalls)
				require.NotContains(t, updated.Extra, "codex_7d_used_percent")
			} else {
				require.Equal(t, json.Number("8"), repo.lastCASRevision)
				require.Equal(t, 20.4, updated.Extra["codex_7d_used_percent"])
			}
		})
	}
}
