//go:build unit

package admin

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type openAICreditsRefreshStub struct {
	queryResult *service.OpenAIQuotaUsage
	queryErr    error
	cacheErr    error
	queryIDs    chan int64
	cacheIDs    chan int64
}

func newOpenAICreditsRefreshStub() *openAICreditsRefreshStub {
	return &openAICreditsRefreshStub{
		queryResult: &service.OpenAIQuotaUsage{FetchedAt: 123},
		queryIDs:    make(chan int64, 2),
		cacheIDs:    make(chan int64, 2),
	}
}

func (s *openAICreditsRefreshStub) QueryUsage(_ context.Context, accountID int64) (*service.OpenAIQuotaUsage, error) {
	s.queryIDs <- accountID
	return s.queryResult, s.queryErr
}

func (s *openAICreditsRefreshStub) CacheCreditsSnapshot(_ context.Context, accountID int64, _ *service.OpenAIQuotaUsage) error {
	s.cacheIDs <- accountID
	return s.cacheErr
}

func receiveCreditRefreshID(t *testing.T, ch <-chan int64) int64 {
	t.Helper()
	select {
	case id := <-ch:
		return id
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for asynchronous Credit refresh")
		return 0
	}
}

func TestScheduleOpenAICreditsRefresh(t *testing.T) {
	t.Run("queries and caches a created OpenAI OAuth account", func(t *testing.T) {
		stub := newOpenAICreditsRefreshStub()
		scheduleOpenAICreditsRefresh(stub, &service.Account{ID: 42, Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth})

		require.Equal(t, int64(42), receiveCreditRefreshID(t, stub.queryIDs))
		require.Equal(t, int64(42), receiveCreditRefreshID(t, stub.cacheIDs))
	})

	t.Run("query failure never attempts a cache write", func(t *testing.T) {
		stub := newOpenAICreditsRefreshStub()
		stub.queryErr = errors.New("upstream unavailable")
		scheduleOpenAICreditsRefresh(stub, &service.Account{ID: 43, Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth})

		require.Equal(t, int64(43), receiveCreditRefreshID(t, stub.queryIDs))
		select {
		case <-stub.cacheIDs:
			t.Fatal("cache write must not run after a query failure")
		case <-time.After(50 * time.Millisecond):
		}
	})

	t.Run("other account types are ignored", func(t *testing.T) {
		stub := newOpenAICreditsRefreshStub()
		scheduleOpenAICreditsRefresh(stub, &service.Account{ID: 44, Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey})

		select {
		case <-stub.queryIDs:
			t.Fatal("non OAuth account must not trigger Credit detection")
		case <-time.After(50 * time.Millisecond):
		}
	})
}

type openAICreditsCreateAdminStub struct {
	*stubAdminService
	account *service.Account
}

func (s *openAICreditsCreateAdminStub) CreateAccount(_ context.Context, _ *service.CreateAccountInput) (*service.Account, error) {
	return s.account, nil
}

func TestAccountCreateSchedulesOpenAICreditsDetection(t *testing.T) {
	gin.SetMode(gin.TestMode)
	adminStub := &openAICreditsCreateAdminStub{
		stubAdminService: newStubAdminService(),
		account:          &service.Account{ID: 51, Name: "oauth", Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth, Status: service.StatusActive},
	}
	creditsStub := newOpenAICreditsRefreshStub()
	handler := NewAccountHandler(adminStub, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	handler.SetOpenAICreditsRefresher(creditsStub)
	router := gin.New()
	router.POST("/accounts", handler.Create)
	request := httptest.NewRequest(http.MethodPost, "/accounts", bytes.NewBufferString(
		`{"name":"oauth","platform":"openai","type":"oauth","credentials":{"access_token":"token"},"extra":{"codex_credits_enabled":true}}`,
	))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()

	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	require.Equal(t, int64(51), receiveCreditRefreshID(t, creditsStub.queryIDs))
	require.Equal(t, int64(51), receiveCreditRefreshID(t, creditsStub.cacheIDs))
}

func TestAccountCreateRejectsCreditsFlagForOpenAIAPIKey(t *testing.T) {
	gin.SetMode(gin.TestMode)
	stub := newStubAdminService()
	handler := NewAccountHandler(stub, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	router := gin.New()
	router.POST("/accounts", handler.Create)
	request := httptest.NewRequest(http.MethodPost, "/accounts", bytes.NewBufferString(
		`{"name":"key","platform":"openai","type":"apikey","credentials":{"api_key":"test"},"extra":{"codex_credits_enabled":false}}`,
	))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()

	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusBadRequest, recorder.Code)
	require.Empty(t, stub.createdAccounts)
	require.Contains(t, recorder.Body.String(), "OPENAI_CREDITS_OPENAI_OAUTH_ONLY")
}

type openAICreditsQuotaWorkflowStub struct {
	*openAIQuotaWorkflowStub
	creditsCacheCalls int
	creditsCacheErr   error
}

func (s *openAICreditsQuotaWorkflowStub) CacheCreditsSnapshot(_ context.Context, _ int64, _ *service.OpenAIQuotaUsage) error {
	s.creditsCacheCalls++
	return s.creditsCacheErr
}

func TestOpenAIRefreshQuotaPersistsOrdinaryCreditsIndependently(t *testing.T) {
	t.Run("both snapshots persist", func(t *testing.T) {
		base := successfulOpenAIQuotaWorkflowStub()
		balance := "62500"
		base.queryResult.Credits = &service.OpenAICredits{HasCredits: true, Balance: &balance}
		quota := &openAICreditsQuotaWorkflowStub{openAIQuotaWorkflowStub: base}
		handler := &OpenAIOAuthHandler{adminService: &openAIResetAdminServiceStub{}, quotaService: quota}

		status, envelope := performOpenAIQuotaRefreshRequest(t, handler)

		require.Equal(t, http.StatusOK, status)
		require.True(t, envelope.Data.CachePersisted)
		require.True(t, envelope.Data.CreditsCachePersisted)
		require.Equal(t, 1, quota.creditsCacheCalls)
		require.NotNil(t, envelope.Data.Credits)
		require.Equal(t, "62500", *envelope.Data.Credits.Balance)
	})

	t.Run("Credit cache failure keeps reset cache and usage successful", func(t *testing.T) {
		quota := &openAICreditsQuotaWorkflowStub{
			openAIQuotaWorkflowStub: successfulOpenAIQuotaWorkflowStub(),
			creditsCacheErr:         errors.New("credit cache unavailable"),
		}
		handler := &OpenAIOAuthHandler{adminService: &openAIResetAdminServiceStub{}, quotaService: quota}

		status, envelope := performOpenAIQuotaRefreshRequest(t, handler)

		require.Equal(t, http.StatusOK, status)
		require.True(t, envelope.Data.CachePersisted)
		require.False(t, envelope.Data.CreditsCachePersisted)
		require.Equal(t, 1, quota.creditsCacheCalls)
	})

	t.Run("reset cache failure does not suppress Credit cache", func(t *testing.T) {
		base := successfulOpenAIQuotaWorkflowStub()
		base.cacheErr = errors.New("reset cache unavailable")
		quota := &openAICreditsQuotaWorkflowStub{openAIQuotaWorkflowStub: base}
		handler := &OpenAIOAuthHandler{adminService: &openAIResetAdminServiceStub{}, quotaService: quota}

		status, envelope := performOpenAIQuotaRefreshRequest(t, handler)

		require.Equal(t, http.StatusOK, status)
		require.False(t, envelope.Data.CachePersisted)
		require.True(t, envelope.Data.CreditsCachePersisted)
		require.Equal(t, 1, quota.creditsCacheCalls)
	})
}
