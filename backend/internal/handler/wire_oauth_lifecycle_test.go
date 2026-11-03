package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/imroc/req/v3"
	"github.com/stretchr/testify/require"
)

type oauthLifecycleStore struct {
	service.AccountRepository
	service.AdminService
	mu       sync.Mutex
	account  *service.Account
	claims   int
	finished chan service.OpenAIReauthorizationState
}

func sameOAuthLifecycleCredentials(left, right map[string]any) bool {
	l, leftErr := json.Marshal(left)
	r, rightErr := json.Marshal(right)
	return leftErr == nil && rightErr == nil && bytes.Equal(l, r)
}

func (s *oauthLifecycleStore) GetByID(context.Context, int64) (*service.Account, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	raw, err := json.Marshal(s.account)
	if err != nil {
		return nil, err
	}
	var snapshot service.Account
	err = json.Unmarshal(raw, &snapshot)
	return &snapshot, err
}

func (s *oauthLifecycleStore) GetAccount(ctx context.Context, id int64) (*service.Account, error) {
	return s.GetByID(ctx, id)
}

func (s *oauthLifecycleStore) ListAccounts(context.Context, int, int, string, string, string, string, int64, string, string, string) ([]service.Account, int64, error) {
	// Do not let the startup scan hide a missing event hook.
	return nil, 0, nil
}

func (s *oauthLifecycleStore) UpdateOpenAIUsageAuthIfIdentityMatches(_ context.Context, expected *service.Account, unauthorized bool) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if expected == nil || !sameOAuthLifecycleCredentials(expected.Credentials, s.account.Credentials) || !reflect.DeepEqual(expected.ProxyID, s.account.ProxyID) {
		return false, nil
	}
	if unauthorized {
		s.account.TempUnschedulableReason = service.OpenAIUsageUnauthorizedReason
	}
	return true, nil
}

func (s *oauthLifecycleStore) ClaimOpenAIReauthorizationAutoAttempt(_ context.Context, expected *service.Account, claim service.OpenAIReauthorizationAutoClaim) (service.OpenAIReauthorizationState, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.claims != 0 || expected == nil || !sameOAuthLifecycleCredentials(expected.Credentials, s.account.Credentials) {
		return service.OpenAIReauthorizationState{}, false, nil
	}
	s.claims++
	state := service.OpenAIReauthorizationState{LastResult: service.OpenAIReauthorizationResultRunning, LastInitiator: service.OpenAIReauthorizationInitiatorAutomatic,
		AutoEpisodeKey: claim.EpisodeKey, AutoClaimID: claim.ClaimID, AutoBrowserMode: claim.BrowserMode, AutoLeaseUntil: &claim.LeaseUntil}
	s.account.Extra[service.OpenAIReauthorizationStateExtraKey] = state
	return state, true, nil
}

func (s *oauthLifecycleStore) FinishOpenAIReauthorizationAutoAttempt(_ context.Context, expected *service.Account, _ service.OpenAIReauthorizationAutoClaim, state service.OpenAIReauthorizationState) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if expected == nil || !sameOAuthLifecycleCredentials(expected.Credentials, s.account.Credentials) {
		return false, nil
	}
	s.account.Extra[service.OpenAIReauthorizationStateExtraKey] = state
	s.finished <- state
	return true, nil
}

func (*oauthLifecycleStore) ApplyOpenAIReauthorizationAutoCredentials(context.Context, *service.Account, service.OpenAIReauthorizationAutoClaim, map[string]any, service.OpenAIReauthorizationState) (bool, error) {
	return false, errors.New("the test has no browser credentials to apply")
}

type oauthLifecycleEncryptor struct{}

func (oauthLifecycleEncryptor) Encrypt(string) (string, error) {
	return "", errors.New("no encryption in this test")
}

func (oauthLifecycleEncryptor) Decrypt(string) (string, error) {
	return "", errors.New("synthetic saved-login failure")
}

func TestOpenAIOAuthProviderRoutesConfirmed401IntoAutomaticHistory(t *testing.T) {
	t.Setenv("GATEWAY_EXECUTION_NODE_ID", "retired-node")
	for _, source := range []string{"gateway", "quota refresh"} {
		t.Run(source, func(t *testing.T) {
			store := &oauthLifecycleStore{account: &service.Account{
				ID: 42, Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth, Status: service.StatusActive, Schedulable: true,
				Credentials: map[string]any{"access_token": "synthetic-access", "chatgpt_account_id": "synthetic-owner", "_token_version": float64(1),
					"expires_at": time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
					service.OpenAIOAuthReauthorizationEmailCredentialKey: "test@example.test", service.OpenAIOAuthReauthorizationPasswordCredentialKey: "synthetic-encrypted-password"},
				Extra: map[string]any{"xiass_execution_node_id": "different-retired-owner"},
			}, finished: make(chan service.OpenAIReauthorizationState, 2)}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				require.Equal(t, "Bearer synthetic-access", r.Header.Get("Authorization"))
				w.WriteHeader(http.StatusUnauthorized)
			}))
			t.Cleanup(server.Close)
			target, err := url.Parse(server.URL)
			require.NoError(t, err)
			quota := service.NewOpenAIQuotaService(store, nil, service.NewOpenAITokenProvider(store, nil, nil), func(string) (*req.Client, error) {
				return req.C().WrapRoundTripFunc(func(rt req.RoundTripper) req.RoundTripFunc {
					return func(request *req.Request) (*req.Response, error) {
						request.URL.Scheme, request.URL.Host = target.Scheme, target.Host
						return rt.RoundTrip(request)
					}
				}), nil
			})
			rateLimit := service.NewRateLimitService(store, nil, &config.Config{}, nil, nil)
			usage := service.NewAccountUsageService(store, nil, nil, nil, nil, nil, nil, quota, nil, nil, nil)
			h := ProvideOpenAIOAuthHandler(nil, store, quota, rateLimit, nil, oauthLifecycleEncryptor{}, nil, nil, nil, nil, usage)
			t.Cleanup(h.StopOpenAIReauthorizationAutoWorker)
			if source == "gateway" {
				account, err := store.GetByID(context.Background(), 42)
				require.NoError(t, err)
				require.True(t, rateLimit.HandleUpstreamError(context.Background(), account, http.StatusUnauthorized, nil, []byte(`{"error":"unauthorized"}`)))
			} else {
				_, err := quota.RefreshUsage(context.Background(), 42)
				require.Error(t, err)
			}
			select {
			case state := <-store.finished:
				require.Equal(t, service.OpenAIReauthorizationInitiatorAutomatic, state.LastInitiator)
				require.Equal(t, service.OpenAIReauthorizationResultFailed, state.LastResult)
				require.Equal(t, "saved_login_unavailable", state.LastReason)
				require.NotEmpty(t, state.AutoClaimID)
				require.NotNil(t, state.AutoFinishedAt)
				require.Nil(t, state.AutoLeaseUntil)
			case <-time.After(2 * time.Second):
				t.Fatal("the confirmed upstream 401 did not reach automatic reauthorization history through provider wiring")
			}
			h.StopOpenAIReauthorizationAutoWorker()
			account, err := store.GetByID(context.Background(), 42)
			require.NoError(t, err)
			require.Equal(t, service.OpenAIUsageUnauthorizedReason, account.TempUnschedulableReason)
			require.Equal(t, "synthetic-access", account.GetCredential("access_token"))
			require.Equal(t, 1, store.claims)
		})
	}
}
