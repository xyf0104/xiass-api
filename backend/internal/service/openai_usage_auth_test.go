package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/stretchr/testify/require"
)

type quotaAuthRepo struct {
	*stubQuotaAccountRepo
	authCalls []bool
	ownerIDs  []int64
}

type openAIUnauthorizedHandlerRecorder struct {
	accountIDs []int64
}

func (r *openAIUnauthorizedHandlerRecorder) handler(_ context.Context, accountID int64) {
	r.accountIDs = append(r.accountIDs, accountID)
}

func (r *quotaAuthRepo) UpdateOpenAIUsageAuthIfIdentityMatches(_ context.Context, expected *Account, unauthorized bool) (bool, error) {
	if !quotaTestIdentityMatches(r.accounts[expected.ID], expected) {
		return false, nil
	}
	r.authCalls = append(r.authCalls, unauthorized)
	r.ownerIDs = append(r.ownerIDs, expected.ID)
	account := r.accounts[expected.ID]
	if unauthorized {
		until := time.Now().Add(10 * time.Minute)
		account.TempUnschedulableUntil, account.TempUnschedulableReason = &until, OpenAIUsageUnauthorizedReason
	} else if account.TempUnschedulableReason == OpenAIUsageUnauthorizedReason {
		account.TempUnschedulableUntil, account.TempUnschedulableReason = nil, ""
	}
	return true, nil
}

func newQuotaAuthService(t *testing.T, handler http.HandlerFunc) (*OpenAIQuotaService, *quotaAuthRepo, *Account) {
	t.Helper()
	account := quotaTestRequestIdentity().target
	account.Status, account.Schedulable = StatusActive, true
	repo := &quotaAuthRepo{stubQuotaAccountRepo: &stubQuotaAccountRepo{accounts: map[int64]*Account{account.ID: account}}}
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	provider := NewOpenAITokenProvider(repo, &stubQuotaTokenCache{tokens: map[string]string{OpenAITokenCacheKey(account): account.GetOpenAIAccessToken()}}, nil)
	return NewOpenAIQuotaService(repo, nil, provider, newQuotaRedirectingFactory(server)), repo, account
}

func TestUpdateOpenAIUsageAuthAndNotifyOnlyAfterAppliedIdentityBoundWrite(t *testing.T) {
	account := quotaTestRequestIdentity().target
	repo := &quotaAuthRepo{stubQuotaAccountRepo: &stubQuotaAccountRepo{accounts: map[int64]*Account{account.ID: account}}}
	recorder := &openAIUnauthorizedHandlerRecorder{}

	require.True(t, updateOpenAIUsageAuthAndNotify(context.Background(), repo, account, true, recorder.handler))
	require.Equal(t, []int64{account.ID}, recorder.accountIDs)

	stale := *account
	stale.Credentials = shallowCopyMap(account.Credentials)
	stale.Credentials["access_token"] = "stale-access-token"
	require.False(t, updateOpenAIUsageAuthAndNotify(context.Background(), repo, &stale, true, recorder.handler))
	require.Equal(t, []int64{account.ID}, recorder.accountIDs, "stale credentials must not trigger auto reauthorization")

	require.True(t, updateOpenAIUsageAuthAndNotify(context.Background(), repo, account, false, recorder.handler))
	require.Equal(t, []int64{account.ID}, recorder.accountIDs, "successful probes clear state without scheduling reauthorization")
}

func TestOpenAIQuotaAuthRefreshRecordsOnlyConfirmedUpstream401(t *testing.T) {
	for _, status := range []int{200, 401, 403, 407, 429, 500} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			svc, repo, account := newQuotaAuthService(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if !strings.HasSuffix(r.URL.Path, "/usage") {
					// Reset-credit endpoint failure alone cannot invalidate a token
					// that the primary usage endpoint just accepted.
					w.WriteHeader(http.StatusUnauthorized)
					return
				}
				w.WriteHeader(status)
				if status == http.StatusOK {
					_ = json.NewEncoder(w).Encode(quotaTestResponse())
				} else {
					_, _ = w.Write([]byte(`{"error":{"code":"token_invalidated","message":"Encountered invalidated oauth token"}}`))
				}
			})
			beforeCredentials, _ := json.Marshal(account.Credentials)
			_, err := svc.RefreshUsage(context.Background(), account.ID)
			if status == 200 {
				require.NoError(t, err)
				require.Equal(t, []bool{false}, repo.authCalls)
			} else {
				require.Error(t, err)
				if status == 401 {
					require.True(t, isOpenAIUsageUnauthorized(err))
					require.Equal(t, []bool{true}, repo.authCalls)
					require.Equal(t, OpenAIUsageUnauthorizedReason, account.TempUnschedulableReason)
				} else {
					require.False(t, isOpenAIUsageUnauthorized(err))
					require.Empty(t, repo.authCalls)
				}
			}
			afterCredentials, _ := json.Marshal(account.Credentials)
			require.Equal(t, string(beforeCredentials), string(afterCredentials))
			require.Equal(t, StatusActive, account.Status)
			require.True(t, account.Schedulable)
			require.Empty(t, repo.extraUpdates)
		})
	}
}

func TestOpenAIQuotaAuthReadOnlyQueryDoesNotMutate401(t *testing.T) {
	svc, repo, account := newQuotaAuthService(t, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(401) })
	_, err := svc.QueryUsage(context.Background(), account.ID)
	require.True(t, isOpenAIUsageUnauthorized(err))
	require.Empty(t, repo.authCalls)
	require.Empty(t, account.TempUnschedulableReason)
}

func TestOpenAIQuotaAuthManualResetRejectionIsRecordedWithoutReplay(t *testing.T) {
	for _, status := range []int{401, 403} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			calls := 0
			svc, repo, account := newQuotaAuthService(t, func(w http.ResponseWriter, r *http.Request) {
				calls++
				require.Equal(t, http.MethodPost, r.Method)
				w.WriteHeader(status)
			})
			_, err := svc.consumeResetCredit(context.Background(), account.ID, "", "synthetic-idempotency-key", nil, nil)
			require.Equal(t, status, infraerrors.Code(err))
			require.Equal(t, "OPENAI_QUOTA_RESET_REJECTED", infraerrors.Reason(err))
			require.ErrorContains(t, err, "no credit was consumed")
			require.Equal(t, 1, calls, "authentication refusal must not replay credit consumption")
			if status == 401 {
				require.Equal(t, []bool{true}, repo.authCalls)
			} else {
				require.Empty(t, repo.authCalls)
			}
		})
	}
}

func TestOpenAIQuotaAuthManualResetMissingAccountFailsClosed(t *testing.T) {
	svc, repo, account := newQuotaAuthService(t, func(http.ResponseWriter, *http.Request) {
		t.Error("missing account must not send a reset request")
	})
	repo.accounts[account.ID] = nil
	_, err := svc.consumeResetCredit(context.Background(), account.ID, "", "synthetic-request", nil, nil)
	require.Equal(t, http.StatusNotFound, infraerrors.Code(err))
	require.Empty(t, repo.authCalls)
}

func TestOpenAIQuotaAuthSuccessClearsOnlyItsOwnMarker(t *testing.T) {
	for _, reason := range []string{OpenAIUsageUnauthorizedReason, "manual maintenance"} {
		t.Run(reason, func(t *testing.T) {
			svc, repo, account := newQuotaAuthService(t, func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(quotaTestResponse())
			})
			account.TempUnschedulableReason = reason
			_, err := svc.RefreshUsage(context.Background(), account.ID)
			require.NoError(t, err)
			require.Equal(t, []bool{false}, repo.authCalls)
			if reason == OpenAIUsageUnauthorizedReason {
				require.Empty(t, account.TempUnschedulableReason)
			} else {
				require.Equal(t, reason, account.TempUnschedulableReason)
			}
		})
	}
}

func TestOpenAIQuotaAuthLate401CannotMarkReauthorizedAccount(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	svc, repo, account := newQuotaAuthService(t, func(w http.ResponseWriter, _ *http.Request) {
		close(started)
		<-release
		w.WriteHeader(401)
	})
	done := make(chan error, 1)
	go func() { _, err := svc.RefreshUsage(context.Background(), account.ID); done <- err }()
	<-started
	account.Credentials = map[string]any{"access_token": "synthetic-new", "refresh_token": "synthetic-new-refresh", "_token_version": 2}
	close(release)
	require.True(t, isOpenAIUsageUnauthorized(<-done))
	require.Empty(t, repo.authCalls)
	require.Empty(t, account.TempUnschedulableReason)
}

func TestOpenAIUsageUnauthorizedDegradesBothForcedAndAutomaticQueries(t *testing.T) {
	for _, force := range []bool{false, true} {
		t.Run(strconv.FormatBool(force), func(t *testing.T) {
			quota, repo, parent := newQuotaAuthService(t, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(401) })
			shadow := &Account{ID: 200, ParentAccountID: &parent.ID, Platform: PlatformOpenAI, Type: AccountTypeOAuth,
				QuotaDimension: QuotaDimensionSpark, Status: StatusActive,
				Extra: map[string]any{"codex_7d_used_percent": 100.0, "codex_7d_reset_at": time.Now().Add(time.Hour).Format(time.RFC3339)}}
			repo.accounts[shadow.ID] = shadow
			svc := &AccountUsageService{accountRepo: repo, openAIQuotaService: quota, cache: NewUsageCache()}
			usage, err := svc.GetUsage(context.Background(), shadow.ID, force)
			require.NoError(t, err)
			require.True(t, usage.NeedsReauth)
			require.Equal(t, "unauthenticated", usage.ErrorCode)
			require.Equal(t, 100.0, usage.SevenDay.Utilization)
			require.Equal(t, []int64{parent.ID}, repo.ownerIDs)
			require.Empty(t, repo.extraUpdates, "401 must not overwrite the saved quota windows")
		})
	}
}

func TestOpenAIUsageUnauthorizedProbeDoesNotAcceptQuotaHeadersAsAuthSuccess(t *testing.T) {
	headers := http.Header{"X-Codex-Primary-Used-Percent": []string{"20"}, "X-Codex-Primary-Window-Minutes": []string{"300"}}
	updates, err := extractOpenAICodexProbeUpdates(&http.Response{StatusCode: 401, Header: headers})
	require.Empty(t, updates)
	require.True(t, isOpenAIUsageUnauthorized(fmt.Errorf("force refresh: %w", err)))
	usage, err := degradedOpenAIUnauthorizedUsage(&UsageInfo{SevenDay: &UsageProgress{Utilization: 92}}, err)
	require.NoError(t, err)
	require.True(t, usage.NeedsReauth)
	require.Equal(t, 92.0, usage.SevenDay.Utilization)
}

func TestOpenAIUsageUnauthorizedClassificationRejectsUnrelatedFailures(t *testing.T) {
	for _, err := range []error{
		nil, errors.New("SOCKS unauthorized"), errors.New("upstream returned 401"),
		errors.New("dial tcp 127.0.0.1:401: connection refused"), context.DeadlineExceeded,
		infraerrors.New(401, "ADMIN_TOKEN_EXPIRED", "administrator session expired"),
		infraerrors.New(502, "OPENAI_QUOTA_REQUEST_FAILED", "proxy unauthorized"),
		infraerrors.New(403, "OPENAI_QUOTA_UPSTREAM_ERROR", "forbidden"),
		infraerrors.New(429, "OPENAI_QUOTA_UPSTREAM_ERROR", "rate limited"),
	} {
		require.False(t, isOpenAIUsageUnauthorized(err), "%v", err)
	}
}

func TestOpenAIUsageUnauthorizedPassiveStateAndRefreshRecovery(t *testing.T) {
	for _, reason := range []string{OpenAIUsageUnauthorizedReason, "OAuth 401: expired", "Authentication failed (401): expired"} {
		svc, account, _ := readOnlyUsageFixture()
		account.TempUnschedulableReason = reason
		usage, err := svc.GetPassiveUsage(context.Background(), account.ID)
		require.NoError(t, err)
		require.True(t, usage.NeedsReauth)
		require.True(t, shouldClearTempUnschedulableAfterRefresh(account))
	}
}
