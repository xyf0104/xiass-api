//go:build unit

package handler

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	middleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type compatSessionReleaseCache struct {
	service.SessionLimitCache
	registered   []string
	unregistered []string
}

func (c *compatSessionReleaseCache) RegisterSession(_ context.Context, _ int64, sessionID string, _ int, _ time.Duration) (bool, error) {
	c.registered = append(c.registered, sessionID)
	return true, nil
}

func (c *compatSessionReleaseCache) UnregisterSession(_ context.Context, _ int64, sessionID string) error {
	c.unregistered = append(c.unregistered, sessionID)
	return nil
}

type compatSessionBusyConcurrencyCache struct {
	*fakeConcurrencyCache
}

func (c *compatSessionBusyConcurrencyCache) AcquireAccountSlot(context.Context, int64, int, string) (bool, error) {
	return false, nil
}

func newCompatSessionReleaseHandler(t *testing.T) (*GatewayHandler, *compatSessionReleaseCache, *service.APIKey, *service.Group) {
	t.Helper()

	groupID := int64(9810)
	accountID := int64(9811)
	group := &service.Group{
		ID:       groupID,
		Hydrated: true,
		Platform: service.PlatformAnthropic,
		Status:   service.StatusActive,
	}
	account := &service.Account{
		ID:          accountID,
		Name:        "session-limited-oauth",
		Platform:    service.PlatformAnthropic,
		Type:        service.AccountTypeOAuth,
		Credentials: map[string]any{"access_token": "test-token"},
		Extra: map[string]any{
			"max_sessions":                 1,
			"session_idle_timeout_minutes": 5,
		},
		Concurrency:   1,
		Priority:      1,
		Status:        service.StatusActive,
		Schedulable:   true,
		AccountGroups: []service.AccountGroup{{AccountID: accountID, GroupID: groupID}},
	}

	cfg := &config.Config{RunMode: config.RunModeSimple}
	cfg.Gateway.Scheduling.FallbackWaitTimeout = 5 * time.Millisecond
	cfg.Gateway.Scheduling.FallbackMaxWaiting = 1
	schedulerCache := &fakeSchedulerCache{accounts: []*service.Account{account}}
	schedulerSnapshot := service.NewSchedulerSnapshotService(schedulerCache, nil, nil, nil, nil)
	concurrencyCache := &compatSessionBusyConcurrencyCache{fakeConcurrencyCache: &fakeConcurrencyCache{}}
	concurrencyService := service.NewConcurrencyService(concurrencyCache)
	sessionCache := &compatSessionReleaseCache{}
	gatewayService := service.NewGatewayService(
		nil, &fakeGroupRepo{group: group}, nil, nil, nil, nil, nil, nil, cfg,
		schedulerSnapshot, concurrencyService, nil, nil, nil, nil, nil, nil, nil,
		sessionCache, nil, nil, nil, nil, nil, nil, nil, nil, nil,
	)
	billingCacheService := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(billingCacheService.Stop)

	apiKey := &service.APIKey{
		ID:      9812,
		UserID:  9813,
		GroupID: &groupID,
		Group:   group,
		Status:  service.StatusActive,
		User:    &service.User{ID: 9813, Concurrency: 10, Balance: 100},
		Key:     "test-key",
	}
	handler := &GatewayHandler{
		gatewayService:      gatewayService,
		billingCacheService: billingCacheService,
		concurrencyHelper:   NewConcurrencyHelper(concurrencyService, SSEPingFormatClaude, time.Millisecond),
		maxAccountSwitches:  1,
		cfg:                 cfg,
	}
	return handler, sessionCache, apiKey, group
}

func runCompatSessionReleaseRequest(t *testing.T, handler *GatewayHandler, apiKey *service.APIKey, group *service.Group, path, body string, call func(*gin.Context)) {
	t.Helper()
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	ctx := context.WithValue(context.Background(), ctxkey.Group, group)
	request := httptest.NewRequest(http.MethodPost, path, bytes.NewBufferString(body)).WithContext(ctx)
	request.Header.Set("Content-Type", "application/json")
	c.Request = request
	c.Set(string(middleware.ContextKeyAPIKey), apiKey)
	c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: apiKey.UserID, Concurrency: 10})
	call(c)
}

func TestGatewayChatCompletionsReleasesRegisteredSessionAfterQueueTimeout(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler, sessionCache, apiKey, group := newCompatSessionReleaseHandler(t)
	runCompatSessionReleaseRequest(t, handler, apiKey, group, "/v1/chat/completions", `{
		"model":"claude-test",
		"messages":[{"role":"user","content":"hello"}],
		"metadata":{"user_id":"user_test_account__session_123e4567-e89b-12d3-a456-426614174000"},
		"stream":false
	}`, handler.ChatCompletions)

	require.Len(t, sessionCache.registered, 1)
	require.NotEmpty(t, sessionCache.registered[0])
	require.Equal(t, sessionCache.registered, sessionCache.unregistered, "failed request must release exactly once")
}

func TestGatewayResponsesReleasesRegisteredSessionAfterQueueTimeout(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler, sessionCache, apiKey, group := newCompatSessionReleaseHandler(t)
	runCompatSessionReleaseRequest(t, handler, apiKey, group, "/v1/responses", `{
		"model":"claude-test",
		"input":"hello",
		"metadata":{"user_id":"user_test_account__session_223e4567-e89b-12d3-a456-426614174000"},
		"stream":false
	}`, handler.Responses)

	require.Len(t, sessionCache.registered, 1)
	require.NotEmpty(t, sessionCache.registered[0])
	require.Equal(t, sessionCache.registered, sessionCache.unregistered, "failed request must release exactly once")
}
