//go:build unit

package service

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/antigravity"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

const bareAntigravityEndpoint429 = `{
	"error": {
		"code": 429,
		"status": "RESOURCE_EXHAUSTED",
		"message": "Resource has been exhausted (e.g. check quota)."
	}
}`

type antigravityEndpointStep struct {
	status int
	body   string
	err    error
}

type sequencedAntigravityEndpointUpstream struct {
	steps []antigravityEndpointStep
	calls []string
}

func (s *sequencedAntigravityEndpointUpstream) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	s.calls = append(s.calls, req.URL.String())
	if len(s.steps) == 0 {
		return nil, errors.New("unexpected antigravity endpoint request")
	}
	step := s.steps[0]
	s.steps = s.steps[1:]
	if step.err != nil {
		return nil, step.err
	}
	return &http.Response{
		StatusCode: step.status,
		Header:     http.Header{},
		Body:       io.NopCloser(strings.NewReader(step.body)),
	}, nil
}

func (s *sequencedAntigravityEndpointUpstream) DoWithTLS(req *http.Request, proxyURL string, accountID int64, accountConcurrency int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return s.Do(req, proxyURL, accountID, accountConcurrency)
}

func withAntigravityEndpointURLs(t *testing.T) (string, string) {
	t.Helper()
	t.Setenv(antigravityForwardBaseURLEnv, "")
	oldBaseURLs := append([]string(nil), antigravity.BaseURLs...)
	prodURL := "https://prod.antigravity.test"
	dailyURL := "https://daily.antigravity.test"
	antigravity.BaseURLs = []string{prodURL, dailyURL}
	t.Cleanup(func() { antigravity.BaseURLs = oldBaseURLs })
	return prodURL, dailyURL
}

func antigravityEndpointTestParams(upstream HTTPUpstream) antigravityRetryLoopParams {
	return antigravityRetryLoopParams{
		ctx:          context.Background(),
		prefix:       "[endpoint-test]",
		account:      &Account{ID: 71, Name: "endpoint-test", Platform: PlatformAntigravity, Status: StatusActive, Schedulable: true, Concurrency: 1},
		accessToken:  "token-a",
		action:       "generateContent",
		body:         []byte(`{"project":"project-a","model":"gemini-3.7-flash-tiered"}`),
		httpUpstream: upstream,
		handleError: func(context.Context, string, *Account, int, http.Header, []byte, string, int64, string, bool) *handleModelRateLimitResult {
			return nil
		},
		requestedModel: "gemini-3.7-flash-high",
	}
}

func TestClassifyAntigravityEndpointFallback_BareExhaustionOnly(t *testing.T) {
	prodURL, dailyURL := withAntigravityEndpointURLs(t)

	require.Equal(t, antigravityEndpointFallbackExhausted,
		classifyAntigravityEndpointFallback(prodURL, http.StatusTooManyRequests, []byte(bareAntigravityEndpoint429)))
	require.Equal(t, antigravityEndpointFallbackNone,
		classifyAntigravityEndpointFallback(dailyURL, http.StatusTooManyRequests, []byte(bareAntigravityEndpoint429)))
	require.Equal(t, antigravityEndpointFallbackAuthCompatibility,
		classifyAntigravityEndpointFallback(dailyURL, http.StatusUnauthorized, []byte(`{"error":{"status":"UNAUTHENTICATED","message":"Invalid bearer token"}}`)))

	structuredLimit := []byte(`{
		"error": {
			"status": "RESOURCE_EXHAUSTED",
			"message": "Resource has been exhausted (e.g. check quota).",
			"details": [{"@type":"type.googleapis.com/google.rpc.ErrorInfo","reason":"RATE_LIMIT_EXCEEDED"}]
		}
	}`)
	require.Equal(t, antigravityEndpointFallbackNone,
		classifyAntigravityEndpointFallback("https://prod.antigravity.test", http.StatusTooManyRequests, structuredLimit))
	require.Equal(t, antigravityEndpointFallbackNone,
		classifyAntigravityEndpointFallback("https://prod.antigravity.test", http.StatusUnauthorized, []byte(`{"error":{"status":"UNAUTHENTICATED"}}`)))
}

func TestConfiguredAntigravityForwardBaseURLs_HardOverrides(t *testing.T) {
	prodURL, dailyURL := withAntigravityEndpointURLs(t)

	require.Equal(t, []string{prodURL, dailyURL}, configuredAntigravityForwardBaseURLs())
	t.Setenv(antigravityForwardBaseURLEnv, "prod")
	require.Equal(t, []string{prodURL}, configuredAntigravityForwardBaseURLs())
	t.Setenv(antigravityForwardBaseURLEnv, "daily")
	require.Equal(t, []string{dailyURL}, configuredAntigravityForwardBaseURLs())
}

func TestAntigravityEndpointPreference_IsolatedByTokenAndModel(t *testing.T) {
	prodURL, dailyURL := withAntigravityEndpointURLs(t)
	svc := &AntigravityGatewayService{}
	p := antigravityEndpointTestParams(nil)

	svc.rememberAntigravityForwardBaseURL(p, dailyURL)
	require.Equal(t, []string{dailyURL, prodURL}, svc.antigravityForwardBaseURLs(p))

	rotatedToken := p
	rotatedToken.accessToken = "token-b"
	require.Equal(t, []string{prodURL, dailyURL}, svc.antigravityForwardBaseURLs(rotatedToken))

	otherModel := p
	otherModel.body = []byte(`{"project":"project-a","model":"claude-sonnet-4-6"}`)
	require.Equal(t, []string{prodURL, dailyURL}, svc.antigravityForwardBaseURLs(otherModel))

	otherProject := p
	otherProject.body = []byte(`{"project":"project-b","model":"gemini-3.7-flash-tiered"}`)
	require.Equal(t, []string{prodURL, dailyURL}, svc.antigravityForwardBaseURLs(otherProject))
}

func TestAntigravityRetryLoop_ProdBare429DailyFailureRestoresProdResponse(t *testing.T) {
	prodURL, dailyURL := withAntigravityEndpointURLs(t)
	upstream := &sequencedAntigravityEndpointUpstream{steps: []antigravityEndpointStep{
		{status: http.StatusTooManyRequests, body: bareAntigravityEndpoint429},
		{status: http.StatusUnauthorized, body: `{"error":{"status":"UNAUTHENTICATED","message":"Invalid bearer token"}}`},
	}}
	handleErrorCalled := false
	p := antigravityEndpointTestParams(upstream)
	p.handleError = func(context.Context, string, *Account, int, http.Header, []byte, string, int64, string, bool) *handleModelRateLimitResult {
		handleErrorCalled = true
		return nil
	}
	svc := &AntigravityGatewayService{}

	result, err := svc.antigravityRetryLoop(p)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotNil(t, result.resp)
	defer result.resp.Body.Close()
	require.Equal(t, http.StatusTooManyRequests, result.resp.StatusCode)
	body, err := io.ReadAll(result.resp.Body)
	require.NoError(t, err)
	require.JSONEq(t, bareAntigravityEndpoint429, string(body))
	require.False(t, handleErrorCalled)
	require.Len(t, svc.endpointPreferences, 0)
	require.Equal(t, []string{prodURL + "/v1internal:generateContent", dailyURL + "/v1internal:generateContent"}, upstream.calls)
}

func TestAntigravityRetryLoop_CachedDaily401FallsBackToProd(t *testing.T) {
	prodURL, dailyURL := withAntigravityEndpointURLs(t)
	upstream := &sequencedAntigravityEndpointUpstream{steps: []antigravityEndpointStep{
		{status: http.StatusUnauthorized, body: `{"error":{"status":"UNAUTHENTICATED","message":"Invalid bearer token"}}`},
		{status: http.StatusOK, body: "ok"},
	}}
	p := antigravityEndpointTestParams(upstream)
	svc := &AntigravityGatewayService{}
	svc.rememberAntigravityForwardBaseURL(p, dailyURL)

	result, err := svc.antigravityRetryLoop(p)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, result.resp.StatusCode)
	defer result.resp.Body.Close()
	require.Equal(t, []string{dailyURL + "/v1internal:generateContent", prodURL + "/v1internal:generateContent"}, upstream.calls)
	require.Equal(t, []string{prodURL, dailyURL}, svc.antigravityForwardBaseURLs(p))
}

func TestAntigravityRetryLoop_CachedDaily401ClearsPreferenceWhenProdFails(t *testing.T) {
	prodURL, dailyURL := withAntigravityEndpointURLs(t)
	upstream := &sequencedAntigravityEndpointUpstream{steps: []antigravityEndpointStep{
		{status: http.StatusUnauthorized, body: `{"error":{"status":"UNAUTHENTICATED","message":"Invalid bearer token"}}`},
		{status: http.StatusBadRequest, body: `{"error":{"status":"INVALID_ARGUMENT","message":"bad request"}}`},
	}}
	p := antigravityEndpointTestParams(upstream)
	svc := &AntigravityGatewayService{}
	svc.rememberAntigravityForwardBaseURL(p, dailyURL)

	result, err := svc.antigravityRetryLoop(p)
	require.NoError(t, err)
	require.Equal(t, http.StatusBadRequest, result.resp.StatusCode)
	defer result.resp.Body.Close()
	require.Equal(t, []string{dailyURL + "/v1internal:generateContent", prodURL + "/v1internal:generateContent"}, upstream.calls)
	require.Equal(t, []string{prodURL, dailyURL}, svc.antigravityForwardBaseURLs(p))
	require.Empty(t, svc.endpointPreferences)
}

func TestAntigravityRetryLoop_CachedDailyBare429FailsOverToNextAccount(t *testing.T) {
	prodURL, dailyURL := withAntigravityEndpointURLs(t)
	upstream := &sequencedAntigravityEndpointUpstream{steps: []antigravityEndpointStep{
		{status: http.StatusTooManyRequests, body: bareAntigravityEndpoint429},
	}}
	p := antigravityEndpointTestParams(upstream)
	svc := &AntigravityGatewayService{}
	svc.rememberAntigravityForwardBaseURL(p, dailyURL)

	result, err := svc.antigravityRetryLoop(p)

	require.Nil(t, result)
	var switchErr *AntigravityAccountSwitchError
	require.ErrorAs(t, err, &switchErr)
	require.Equal(t, p.account.ID, switchErr.OriginalAccountID)
	require.Equal(t, p.requestedModel, switchErr.RateLimitedModel)
	require.Equal(t, []string{dailyURL + "/v1internal:generateContent"}, upstream.calls)
	require.Equal(t, []string{dailyURL, prodURL}, svc.antigravityForwardBaseURLs(p))
}

func TestAntigravityRetryLoop_FallbackCancellationWinsOverSaved429(t *testing.T) {
	_, _ = withAntigravityEndpointURLs(t)
	upstream := &sequencedAntigravityEndpointUpstream{steps: []antigravityEndpointStep{
		{status: http.StatusTooManyRequests, body: bareAntigravityEndpoint429},
		{err: context.Canceled},
	}}
	p := antigravityEndpointTestParams(upstream)
	svc := &AntigravityGatewayService{}

	result, err := svc.antigravityRetryLoop(p)
	require.Nil(t, result)
	require.ErrorIs(t, err, context.Canceled)
}

func TestAntigravityReadOnlyProbe_DoesNotRecordPreference(t *testing.T) {
	prodURL, dailyURL := withAntigravityEndpointURLs(t)
	upstream := &sequencedAntigravityEndpointUpstream{steps: []antigravityEndpointStep{
		{status: http.StatusTooManyRequests, body: bareAntigravityEndpoint429},
		{status: http.StatusOK, body: "ok"},
	}}
	p := antigravityEndpointTestParams(upstream)
	p.readOnlyProbe = true
	svc := &AntigravityGatewayService{}

	result, err := svc.antigravityRetryLoop(p)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, result.resp.StatusCode)
	defer result.resp.Body.Close()
	require.Len(t, svc.endpointPreferences, 0)
	require.Equal(t, []string{prodURL + "/v1internal:generateContent", dailyURL + "/v1internal:generateContent"}, upstream.calls)
}

func TestAntigravityReadOnlyProbe_IgnoresRuntimePreference(t *testing.T) {
	prodURL, dailyURL := withAntigravityEndpointURLs(t)
	upstream := &sequencedAntigravityEndpointUpstream{steps: []antigravityEndpointStep{
		{status: http.StatusOK, body: "ok"},
	}}
	p := antigravityEndpointTestParams(upstream)
	p.readOnlyProbe = true
	svc := &AntigravityGatewayService{}
	svc.rememberAntigravityForwardBaseURL(p, dailyURL)

	result, err := svc.antigravityRetryLoop(p)

	require.NoError(t, err)
	require.Equal(t, http.StatusOK, result.resp.StatusCode)
	defer result.resp.Body.Close()
	require.Equal(t, []string{prodURL + "/v1internal:generateContent"}, upstream.calls)
	require.Equal(t, []string{dailyURL, prodURL}, svc.antigravityForwardBaseURLs(p))
}

func TestAntigravityReadOnlyProbe_StructuredLimitDoesNotFallback(t *testing.T) {
	prodURL, _ := withAntigravityEndpointURLs(t)
	structuredLimit := `{
		"error": {
			"status": "RESOURCE_EXHAUSTED",
			"message": "Resource has been exhausted (e.g. check quota).",
			"details": [{"@type":"type.googleapis.com/google.rpc.ErrorInfo","reason":"RATE_LIMIT_EXCEEDED"}]
		}
	}`
	upstream := &sequencedAntigravityEndpointUpstream{steps: []antigravityEndpointStep{
		{status: http.StatusTooManyRequests, body: structuredLimit},
		{status: http.StatusOK, body: "must not be requested"},
	}}
	p := antigravityEndpointTestParams(upstream)
	p.readOnlyProbe = true
	svc := &AntigravityGatewayService{}

	result, err := svc.antigravityRetryLoop(p)
	require.NoError(t, err)
	require.Equal(t, http.StatusTooManyRequests, result.resp.StatusCode)
	defer result.resp.Body.Close()
	require.Equal(t, []string{prodURL + "/v1internal:generateContent"}, upstream.calls)
}

func TestAntigravityEndpointPreference_Expires(t *testing.T) {
	prodURL, dailyURL := withAntigravityEndpointURLs(t)
	svc := &AntigravityGatewayService{}
	p := antigravityEndpointTestParams(nil)
	key, tokenHash := antigravityEndpointPreferenceIdentity(p)
	svc.endpointPreferences = map[antigravityEndpointPreferenceKey]antigravityEndpointPreference{
		key: {tokenHash: tokenHash, baseURL: dailyURL, expiresAt: time.Now().Add(-time.Second)},
	}

	require.Equal(t, []string{prodURL, dailyURL}, svc.antigravityForwardBaseURLs(p))
	require.Empty(t, svc.endpointPreferences)
}

func TestRememberAntigravityForwardBaseURL_SweepsUnrelatedExpiredPreferences(t *testing.T) {
	_, dailyURL := withAntigravityEndpointURLs(t)
	svc := &AntigravityGatewayService{}
	p := antigravityEndpointTestParams(nil)
	staleKey := antigravityEndpointPreferenceKey{accountID: 999, projectID: "removed-project"}
	svc.endpointPreferences = map[antigravityEndpointPreferenceKey]antigravityEndpointPreference{
		staleKey: {baseURL: dailyURL, expiresAt: time.Now().Add(-time.Minute)},
	}

	svc.rememberAntigravityForwardBaseURL(p, dailyURL)

	require.NotContains(t, svc.endpointPreferences, staleKey)
	require.Len(t, svc.endpointPreferences, 1)
}

func TestAntigravityForwardGemini_BareEndpoint429DoesNotWriteCooldown(t *testing.T) {
	_, _ = withAntigravityEndpointURLs(t)
	gin.SetMode(gin.TestMode)
	upstream := &sequencedAntigravityEndpointUpstream{steps: []antigravityEndpointStep{
		{status: http.StatusTooManyRequests, body: bareAntigravityEndpoint429},
		{status: http.StatusUnauthorized, body: `{"error":{"status":"UNAUTHENTICATED","message":"Invalid bearer token"}}`},
	}}
	repo := &stubAntigravityAccountRepo{}
	svc := &AntigravityGatewayService{
		accountRepo:   repo,
		tokenProvider: NewAntigravityTokenProvider(repo, nil, nil),
		httpUpstream:  upstream,
	}
	account := &Account{
		ID:          72,
		Name:        "no-endpoint-cooldown",
		Platform:    PlatformAntigravity,
		Type:        AccountTypeOAuth,
		Status:      StatusActive,
		Schedulable: true,
		Concurrency: 1,
		Credentials: map[string]any{
			"access_token":  "token",
			"refresh_token": "refresh",
			"project_id":    "project-a",
			"expires_at":    time.Now().Add(time.Hour).Format(time.RFC3339),
		},
	}
	body := []byte(`{"contents":[{"role":"user","parts":[{"text":"ok"}]}]}`)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1beta/models/gemini-3.7-flash-high:generateContent", strings.NewReader(string(body)))

	result, err := svc.ForwardGemini(context.Background(), c, account, "gemini-3.7-flash-high", "generateContent", false, body, false)
	require.Nil(t, result)
	var failoverErr *UpstreamFailoverError
	require.ErrorAs(t, err, &failoverErr)
	require.Empty(t, repo.modelRateLimitCalls)
	require.Empty(t, repo.rateCalls)
	require.Empty(t, repo.extraUpdateCalls)
}

type antigravityProbeRefreshRepo struct {
	AccountRepository
	account   *Account
	tempCalls int
}

func (r *antigravityProbeRefreshRepo) GetByID(context.Context, int64) (*Account, error) {
	return r.account, nil
}

func (r *antigravityProbeRefreshRepo) SetTempUnschedulable(context.Context, int64, time.Time, string) error {
	r.tempCalls++
	return nil
}

type antigravityProbeRefreshExecutor struct{}

func (antigravityProbeRefreshExecutor) CanRefresh(*Account) bool { return true }
func (antigravityProbeRefreshExecutor) NeedsRefresh(*Account, time.Duration) bool {
	return true
}
func (antigravityProbeRefreshExecutor) Refresh(context.Context, *Account) (map[string]any, error) {
	return nil, errors.New("probe refresh failed")
}
func (antigravityProbeRefreshExecutor) CacheKey(*Account) string { return "ag:probe" }

func TestAntigravityTokenProvider_ReadOnlyProbeRefreshFailureDoesNotStopScheduling(t *testing.T) {
	account := &Account{
		ID:          73,
		Platform:    PlatformAntigravity,
		Type:        AccountTypeOAuth,
		Status:      StatusActive,
		Schedulable: true,
		Credentials: map[string]any{
			"access_token":  "old-token",
			"refresh_token": "refresh-token",
			"project_id":    "project-a",
			"expires_at":    time.Now().Add(time.Minute).Format(time.RFC3339),
		},
	}
	repo := &antigravityProbeRefreshRepo{account: account}
	provider := NewAntigravityTokenProvider(repo, nil, nil)
	provider.SetRefreshAPI(NewOAuthRefreshAPI(repo, nil), antigravityProbeRefreshExecutor{})

	token, err := provider.GetAccessToken(withAntigravityReadOnlyProbeContext(context.Background()), account)
	require.Empty(t, token)
	require.ErrorContains(t, err, "probe refresh failed")
	require.Zero(t, repo.tempCalls)
}
