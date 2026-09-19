package handler

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type codexModelsPinnedHTTPUpstream struct {
	service.HTTPUpstream
	mu       sync.Mutex
	calls    []int64
	bodies   map[int64]string
	statuses map[int64]int
}

func (u *codexModelsPinnedHTTPUpstream) Do(_ *http.Request, _ string, accountID int64, _ int) (*http.Response, error) {
	u.mu.Lock()
	u.calls = append(u.calls, accountID)
	u.mu.Unlock()
	if status, ok := u.statuses[accountID]; ok {
		return &http.Response{
			StatusCode: status,
			Status:     http.StatusText(status),
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{"error":{"message":"upstream boom"}}`)),
		}, nil
	}
	body := u.bodies[accountID]
	if body == "" {
		body = `{"models":[]}`
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Status:     "200 OK",
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}, nil
}

func (u *codexModelsPinnedHTTPUpstream) accountIDs() []int64 {
	u.mu.Lock()
	defer u.mu.Unlock()
	ids := append([]int64(nil), u.calls...)
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

func newPinnedCodexAccount(id int64, status string, schedulable bool, rateLimited bool) service.Account {
	account := service.Account{
		ID:          id,
		Name:        fmt.Sprintf("pinned-%d", id),
		Platform:    service.PlatformOpenAI,
		Type:        service.AccountTypeAPIKey,
		Status:      status,
		Schedulable: schedulable,
		Priority:    int(id),
		Concurrency: 1,
		Credentials: map[string]any{
			"api_key":  fmt.Sprintf("sk-pinned-%d", id),
			"base_url": fmt.Sprintf("https://pinned-%d.example/v1", id),
		},
	}
	if rateLimited {
		reset := time.Now().Add(10 * time.Minute)
		account.RateLimitResetAt = &reset
	}
	return account
}

func newPinnedCodexTestHandler(accounts []service.Account, upstream *codexModelsPinnedHTTPUpstream, maxSwitches int) *OpenAIGatewayHandler {
	gin.SetMode(gin.TestMode)
	cfg := &config.Config{RunMode: config.RunModeSimple}
	gatewayService := service.NewOpenAIGatewayService(
		codexModelsFailoverAccountRepo{accounts: accounts},
		nil, nil, nil, nil, nil, nil, cfg, nil, nil, nil, nil, nil,
		upstream,
		nil, nil, nil, nil, nil, nil, nil, nil,
	)
	return &OpenAIGatewayHandler{gatewayService: gatewayService, maxAccountSwitches: maxSwitches}
}

func performPinnedCodexModelsRequest(t *testing.T, handler *OpenAIGatewayHandler, group *service.Group, etag string) *httptest.ResponseRecorder {
	t.Helper()
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/models?client_version=0.144.0", nil)
	if etag != "" {
		c.Request.Header.Set("If-None-Match", etag)
	}
	c.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{
		GroupID: &group.ID,
		Group:   group,
	})

	handler.CodexModels(c)
	return recorder
}

func codexHandlerManifestSlugs(t *testing.T, recorder *httptest.ResponseRecorder) []string {
	t.Helper()

	var envelope struct {
		Models []struct {
			Slug string `json:"slug"`
		} `json:"models"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode body: %v; body=%s", err, recorder.Body.String())
	}
	slugs := make([]string, 0, len(envelope.Models))
	for _, model := range envelope.Models {
		slugs = append(slugs, model.Slug)
	}
	return slugs
}
