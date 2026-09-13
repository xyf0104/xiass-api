package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func setupAccountListRouter() (*gin.Engine, *stubAdminService) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	adminSvc := newStubAdminService()
	handler := NewAccountHandler(adminSvc, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	router.GET("/api/v1/admin/accounts", handler.List)
	return router, adminSvc
}

type accountPoolListStub struct {
	*stubAdminService
	pool *service.AccountPool
}

func (s *accountPoolListStub) GetAccountPool(_ context.Context, id int64) (*service.AccountPool, error) {
	if s.pool == nil || s.pool.ID != id {
		return nil, service.ErrAccountPoolNotFound
	}
	return s.pool, nil
}

func TestAccountHandlerListIncludesCreatedAt(t *testing.T) {
	router, adminSvc := setupAccountListRouter()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/accounts?page=1&page_size=20&sort_by=created_at&sort_order=desc", nil)
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "created_at", adminSvc.lastListAccounts.sortBy)

	var payload struct {
		Data struct {
			Items []struct {
				ID        int64  `json:"id"`
				CreatedAt string `json:"created_at"`
			} `json:"items"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &payload))
	require.Len(t, payload.Data.Items, 1)

	createdAt := payload.Data.Items[0].CreatedAt
	require.NotEmpty(t, createdAt)
	require.True(t, strings.HasSuffix(createdAt, "Z"), "created_at should be serialized as UTC")
	parsed, err := time.Parse(time.RFC3339Nano, createdAt)
	require.NoError(t, err)
	_, offset := parsed.Zone()
	require.Equal(t, 0, offset)
}

func TestAccountHandlerListDefaultsToRecentAccountActivity(t *testing.T) {
	router, adminSvc := setupAccountListRouter()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/accounts?page=1&page_size=20", nil)
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, service.AccountSortRecentActivity, adminSvc.lastListAccounts.sortBy)
	require.Equal(t, "desc", adminSvc.lastListAccounts.sortOrder)
}

func TestAccountHandlerListFiltersOneExactAccountID(t *testing.T) {
	router, adminSvc := setupAccountListRouter()
	adminSvc.accounts = []service.Account{
		{ID: 17, Name: "target", Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth},
		{ID: 18, Name: "other", Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth},
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/accounts?page=1&page_size=20&account_id=17", nil)
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, []int64{17}, adminSvc.lastListAccounts.accountIDs)
	var payload struct {
		Data struct {
			Items []struct {
				ID int64 `json:"id"`
			} `json:"items"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &payload))
	require.Len(t, payload.Data.Items, 1)
	require.Equal(t, []int64{17}, []int64{payload.Data.Items[0].ID})
}

func TestAccountHandlerListFiltersByAccountPoolBeforePagination(t *testing.T) {
	gin.SetMode(gin.TestMode)
	adminSvc := newStubAdminService()
	adminSvc.accounts = []service.Account{
		{ID: 17, Name: "outside", Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth},
		{ID: 18, Name: "pool-a", Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth},
		{ID: 19, Name: "pool-b", Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth},
	}
	poolSvc := &accountPoolListStub{stubAdminService: adminSvc, pool: &service.AccountPool{ID: 8, Name: "Pro pool", AccountIDs: []int64{19, 18}}}
	router := gin.New()
	router.GET("/api/v1/admin/accounts", NewAccountHandler(poolSvc, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil).List)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/accounts?page=1&page_size=1&account_pool=8", nil)
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, []int64{18, 19}, adminSvc.lastListAccounts.accountIDs)
	var payload struct {
		Data struct {
			Items []struct {
				ID int64 `json:"id"`
			} `json:"items"`
			Total int64 `json:"total"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &payload))
	require.Equal(t, int64(2), payload.Data.Total)
	require.Len(t, payload.Data.Items, 1)
	require.Equal(t, int64(18), payload.Data.Items[0].ID)
}

func TestAccountHandlerListRejectsInvalidAccountPoolFilter(t *testing.T) {
	router, _ := setupAccountListRouter()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/accounts?account_pool=invalid", nil)
	router.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestAccountHandlerListRejectsInvalidExactAccountID(t *testing.T) {
	router, _ := setupAccountListRouter()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/accounts?account_id=0", nil)
	router.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestAccountHandlerListLiteUsesStrictCredentialFreeAllowlist(t *testing.T) {
	router, adminSvc := setupAccountListRouter()
	notes := "visible note"
	adminSvc.accounts = []service.Account{
		{
			ID:          17,
			Name:        "safe picker entry",
			Notes:       &notes,
			Platform:    service.PlatformOpenAI,
			Type:        service.AccountTypeOAuth,
			Concurrency: 3,
			Priority:    2,
			Status:      service.StatusActive,
			Schedulable: true,
			Credentials: map[string]any{
				"accessToken":   "must-not-render",
				"refreshToken":  "must-not-render",
				"client_secret": "must-not-render",
				"nested":        map[string]any{"token": "must-not-render"},
			},
			Extra: map[string]any{
				"sessionToken":                       "must-not-render",
				"custom_headers":                     map[string]any{"Authorization": "must-not-render"},
				service.AccountExecutionNodeExtraKey: "api2",
			},
		},
		{
			ID:       18,
			Name:     "legacy picker entry",
			Platform: service.PlatformAnthropic,
			Type:     service.AccountTypeOAuth,
			Status:   service.StatusActive,
		},
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/accounts?page=1&page_size=20&lite=true", nil)
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "private, no-store, max-age=0", rec.Header().Get("Cache-Control"))
	require.Equal(t, "no-cache", rec.Header().Get("Pragma"))
	require.Equal(t, "0", rec.Header().Get("Expires"))
	require.NotContains(t, rec.Body.String(), "must-not-render")

	var payload struct {
		Data struct {
			Items []map[string]any `json:"items"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &payload))
	require.Len(t, payload.Data.Items, 2)
	item := payload.Data.Items[0]
	require.Equal(t, "safe picker entry", item["name"])
	require.Equal(t, "visible note", item["notes"])
	require.Equal(t, "api2", item["execution_node_id"])
	require.Equal(t, "", payload.Data.Items[1]["execution_node_id"])
	for _, listedAccount := range payload.Data.Items {
		for _, forbidden := range []string{
			"credentials", "credentials_status", "extra", "proxy", "proxy_id",
			"account_groups", "groups", "error_message", "rate_limit_reset_at",
		} {
			require.NotContains(t, listedAccount, forbidden)
		}
	}
}

func TestAccountHandlerListReturnsSchedulerScoresPerGroup(t *testing.T) {
	router, adminSvc := setupAccountListRouter()
	now := time.Now().UTC()
	groupID := int64(41)
	adminSvc.accounts = []service.Account{
		{
			ID:          101,
			Name:        "account-high-priority",
			Platform:    service.PlatformOpenAI,
			Type:        service.AccountTypeAPIKey,
			Status:      service.StatusActive,
			Schedulable: true,
			Concurrency: 10,
			Priority:    1,
			AccountGroups: []service.AccountGroup{
				{AccountID: 101, GroupID: groupID, Priority: 100, Group: &service.Group{ID: groupID, Name: "openai"}},
			},
			GroupIDs:  []int64{groupID},
			CreatedAt: now,
			UpdatedAt: now,
		},
		{
			ID:          102,
			Name:        "account-low-priority",
			Platform:    service.PlatformOpenAI,
			Type:        service.AccountTypeAPIKey,
			Status:      service.StatusActive,
			Schedulable: true,
			Concurrency: 10,
			Priority:    100000,
			AccountGroups: []service.AccountGroup{
				{AccountID: 102, GroupID: groupID, Priority: 1, Group: &service.Group{ID: groupID, Name: "openai"}},
			},
			GroupIDs:  []int64{groupID},
			CreatedAt: now,
			UpdatedAt: now,
		},
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/accounts?page=1&page_size=20&platform=openai&include_scheduler_score=1", nil)
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var payload struct {
		Data struct {
			Items []struct {
				ID             int64 `json:"id"`
				SchedulerScore struct {
					BaseScore float64 `json:"base_score"`
				} `json:"scheduler_score"`
				SchedulerScores []struct {
					GroupID       *int64  `json:"group_id"`
					GroupName     string  `json:"group_name"`
					GroupPriority *int    `json:"group_priority"`
					BaseScore     float64 `json:"base_score"`
				} `json:"scheduler_scores"`
			} `json:"items"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &payload))
	require.Len(t, payload.Data.Items, 2)

	var high, low *struct {
		ID             int64 `json:"id"`
		SchedulerScore struct {
			BaseScore float64 `json:"base_score"`
		} `json:"scheduler_score"`
		SchedulerScores []struct {
			GroupID       *int64  `json:"group_id"`
			GroupName     string  `json:"group_name"`
			GroupPriority *int    `json:"group_priority"`
			BaseScore     float64 `json:"base_score"`
		} `json:"scheduler_scores"`
	}
	for i := range payload.Data.Items {
		item := &payload.Data.Items[i]
		switch item.ID {
		case 101:
			high = item
		case 102:
			low = item
		}
	}
	require.NotNil(t, high)
	require.NotNil(t, low)
	require.Len(t, high.SchedulerScores, 1)
	require.Len(t, low.SchedulerScores, 1)
	require.Equal(t, groupID, *high.SchedulerScores[0].GroupID)
	require.Equal(t, "openai", high.SchedulerScores[0].GroupName)
	require.Equal(t, 100, *high.SchedulerScores[0].GroupPriority)
	require.Equal(t, 1, *low.SchedulerScores[0].GroupPriority)
	require.Greater(t, high.SchedulerScores[0].BaseScore, low.SchedulerScores[0].BaseScore)
}

func TestAccountHandlerListSkipsSchedulerScoresByDefault(t *testing.T) {
	router, adminSvc := setupAccountListRouter()
	now := time.Now().UTC()
	adminSvc.accounts = []service.Account{
		{
			ID:          110,
			Name:        "openai-account",
			Platform:    service.PlatformOpenAI,
			Type:        service.AccountTypeAPIKey,
			Status:      service.StatusActive,
			Schedulable: true,
			Concurrency: 10,
			Priority:    1,
			CreatedAt:   now,
			UpdatedAt:   now,
		},
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/accounts?page=1&page_size=20&platform=openai", nil)
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Zero(t, adminSvc.schedulerScoreFilterCalls)
	require.Zero(t, adminSvc.openAISchedulerScorePoolCalls)

	var payload struct {
		Data struct {
			Items []map[string]any `json:"items"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &payload))
	require.Len(t, payload.Data.Items, 1)
	require.NotContains(t, payload.Data.Items[0], "scheduler_score")
	require.NotContains(t, payload.Data.Items[0], "scheduler_scores")
}

func TestAccountHandlerListKeepsSchedulerScoreScopedToFilter(t *testing.T) {
	router, adminSvc := setupAccountListRouter()
	now := time.Now().UTC()
	groupID := int64(42)
	visibleAccount := service.Account{
		ID:          201,
		Name:        "visible-low-priority",
		Platform:    service.PlatformOpenAI,
		Type:        service.AccountTypeAPIKey,
		Status:      service.StatusActive,
		Schedulable: true,
		Concurrency: 10,
		Priority:    100000,
		AccountGroups: []service.AccountGroup{
			{AccountID: 201, GroupID: groupID, Priority: 1, Group: &service.Group{ID: groupID, Name: "openai"}},
		},
		GroupIDs:  []int64{groupID},
		CreatedAt: now,
		UpdatedAt: now,
	}
	hiddenGroupPeer := service.Account{
		ID:          202,
		Name:        "hidden-high-priority",
		Platform:    service.PlatformOpenAI,
		Type:        service.AccountTypeAPIKey,
		Status:      service.StatusActive,
		Schedulable: true,
		Concurrency: 10,
		Priority:    1,
		AccountGroups: []service.AccountGroup{
			{AccountID: 202, GroupID: groupID, Priority: 2, Group: &service.Group{ID: groupID, Name: "openai"}},
		},
		GroupIDs:  []int64{groupID},
		CreatedAt: now,
		UpdatedAt: now,
	}
	adminSvc.accounts = []service.Account{visibleAccount}
	adminSvc.accountSchedulerScoreFilterAccounts = []service.Account{visibleAccount, hiddenGroupPeer}
	adminSvc.openAISchedulerScorePoolAccounts = []service.Account{visibleAccount, hiddenGroupPeer}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/accounts?page=1&page_size=1&platform=openai&include_scheduler_score=1", nil)
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var payload struct {
		Data struct {
			Items []struct {
				ID             int64 `json:"id"`
				SchedulerScore struct {
					BaseScore float64 `json:"base_score"`
				} `json:"scheduler_score"`
				SchedulerScores []struct {
					GroupID   *int64  `json:"group_id"`
					BaseScore float64 `json:"base_score"`
				} `json:"scheduler_scores"`
			} `json:"items"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &payload))
	require.Len(t, payload.Data.Items, 1)
	item := payload.Data.Items[0]
	require.Equal(t, int64(201), item.ID)
	require.Len(t, item.SchedulerScores, 1)
	require.Equal(t, groupID, *item.SchedulerScores[0].GroupID)
	require.Equal(t, item.SchedulerScores[0].BaseScore, item.SchedulerScore.BaseScore)
}

func TestAccountHandlerListSchedulerScoreIgnoresPagination(t *testing.T) {
	router, adminSvc := setupAccountListRouter()
	now := time.Now().UTC()
	visibleAccount := service.Account{
		ID:          301,
		Name:        "visible-low-priority",
		Platform:    service.PlatformOpenAI,
		Type:        service.AccountTypeAPIKey,
		Status:      service.StatusActive,
		Schedulable: true,
		Concurrency: 10,
		Priority:    100000,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	hiddenFilterPeer := service.Account{
		ID:          302,
		Name:        "hidden-high-priority",
		Platform:    service.PlatformOpenAI,
		Type:        service.AccountTypeAPIKey,
		Status:      service.StatusActive,
		Schedulable: true,
		Concurrency: 10,
		Priority:    1,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	adminSvc.accounts = []service.Account{visibleAccount}
	adminSvc.accountSchedulerScoreFilterAccounts = []service.Account{visibleAccount, hiddenFilterPeer}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/accounts?page=1&page_size=1&platform=openai&include_scheduler_score=1", nil)
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var payload struct {
		Data struct {
			Items []struct {
				ID             int64 `json:"id"`
				SchedulerScore struct {
					BaseScore float64 `json:"base_score"`
				} `json:"scheduler_score"`
				SchedulerScores []struct {
					GroupID   *int64  `json:"group_id"`
					BaseScore float64 `json:"base_score"`
				} `json:"scheduler_scores"`
			} `json:"items"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &payload))
	require.Len(t, payload.Data.Items, 1)
	require.Equal(t, int64(301), payload.Data.Items[0].ID)
	require.Less(t, payload.Data.Items[0].SchedulerScore.BaseScore, 3.75)
	require.Empty(t, payload.Data.Items[0].SchedulerScores)
}
