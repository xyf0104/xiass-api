package admin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/benchmark"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

type pelicanHandlerAccounts struct {
	service.AdminService
	items     []*service.Account
	accessErr error
}

func (s *pelicanHandlerAccounts) GetAccountsByIDs(context.Context, []int64) ([]*service.Account, error) {
	return s.items, nil
}
func (s *pelicanHandlerAccounts) ListAccounts(context.Context, int, int, string, string, string, string, int64, string, string, string) ([]service.Account, int64, error) {
	items := []service.Account{}
	for _, a := range s.items {
		items = append(items, *a)
	}
	return items, int64(len(items)), nil
}

func (s *pelicanHandlerAccounts) CheckAccountManagementAccess(context.Context, int64) error {
	return s.accessErr
}

type pelicanHandlerStore struct {
	benchmark.Store
	tasks []benchmark.Task
	stops int
	html  string
}

func (s *pelicanHandlerStore) Create(_ context.Context, tasks []benchmark.Task) ([]benchmark.Task, []benchmark.Skipped, error) {
	s.tasks = tasks
	return tasks, nil, nil
}
func (s *pelicanHandlerStore) List(context.Context, benchmark.Filter) (*benchmark.Page, error) {
	return &benchmark.Page{Items: []benchmark.Task{{ID: "test", AccountID: 1, Status: "running", HTMLBytes: len(s.html)}}, Total: 1, Page: 1, PageSize: 100}, nil
}
func (s *pelicanHandlerStore) Detail(context.Context, string) (*benchmark.Detail, error) {
	return &benchmark.Detail{Task: benchmark.Task{ID: "test"}, HTML: s.html}, nil
}
func (s *pelicanHandlerStore) Get(context.Context, string) (*benchmark.Task, error) {
	return &benchmark.Task{ID: "test", AccountID: 1, Status: "canceling"}, nil
}
func (s *pelicanHandlerStore) Stop(context.Context, string, int64) (int64, error) {
	s.stops++
	return 1, nil
}

func pelicanHandlerRequest(method, path, body string, fn gin.HandlerFunc, params gin.Params) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(method, path, strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Params = params
	fn(c)
	return w
}

func TestPelicanHandlerCreateFiltersAndDefaults(t *testing.T) {
	for _, body := range []string{`{"account_ids":[1,1,2,3,4]}`, `{"all":true}`} {
		store := &pelicanHandlerStore{}
		accounts := &pelicanHandlerAccounts{items: []*service.Account{
			{ID: 1, Name: "paid", Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth, Credentials: map[string]any{"plan_type": "plus"}},
			{ID: 2, Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth, Credentials: map[string]any{"plan_type": "free"}},
			{ID: 3, Platform: service.PlatformAnthropic, Type: service.AccountTypeAPIKey},
			{ID: 4, Name: "upstream", Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey},
		}}
		h := &PelicanBenchmarkHandler{store: store, accounts: accounts}
		w := pelicanHandlerRequest("POST", "/", body, h.Create, nil)
		require.Equal(t, http.StatusAccepted, w.Code, w.Body.String())
		require.Len(t, store.tasks, 2)
		require.Equal(t, benchmark.DefaultModel, store.tasks[0].Model)
		require.Equal(t, int64(1), store.tasks[0].AccountID)
		require.Equal(t, int64(4), store.tasks[1].AccountID)
		require.NotContains(t, w.Body.String(), "credentials")
		require.Contains(t, w.Body.String(), "free_plan")
		require.Contains(t, w.Body.String(), "ineligible")
	}
}

func TestPelicanHandlerRejectsPromptAndAmbiguousSelectors(t *testing.T) {
	for _, body := range []string{`{"all":true,"prompt":"override"}`, `{"all":true,"account_ids":[1]}`, `{}`, `{"account_ids":[-1]}`, `{"all":true,"model":"invalid\nmodel"}`, `{"all":true} {}`, `null`} {
		store := &pelicanHandlerStore{}
		h := &PelicanBenchmarkHandler{store: store}
		w := pelicanHandlerRequest("POST", "/", body, h.Create, nil)
		require.Equal(t, http.StatusBadRequest, w.Code, body)
		require.Empty(t, store.tasks)
	}
}

func TestPelicanHandlerSingleListDetailAndStopContract(t *testing.T) {
	store := &pelicanHandlerStore{html: `<html><script>window.parent.alert("unsafe")</script></html>`}
	h := &PelicanBenchmarkHandler{store: store, accounts: &pelicanHandlerAccounts{items: []*service.Account{{ID: 1, Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey}}}}
	w := pelicanHandlerRequest("POST", "/", "", h.Create, gin.Params{{Key: "id", Value: "1"}})
	require.Equal(t, http.StatusAccepted, w.Code, w.Body.String())
	require.Len(t, store.tasks, 1)
	w = pelicanHandlerRequest("GET", "/", "", h.List, nil)
	require.Equal(t, http.StatusOK, w.Code)
	require.NotContains(t, w.Body.String(), "window.parent")
	var result struct {
		Data struct {
			Items []map[string]any `json:"items"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &result))
	require.NotContains(t, result.Data.Items[0], "html")
	id := gin.Params{{Key: "id", Value: uuid.NewString()}}
	w = pelicanHandlerRequest("GET", "/", "", h.Detail, id)
	require.Equal(t, http.StatusOK, w.Code)
	require.Contains(t, w.Header().Get("Content-Type"), "application/json")
	require.Equal(t, "no-store", w.Header().Get("Cache-Control"))
	require.Equal(t, "nosniff", w.Header().Get("X-Content-Type-Options"))
	require.NotContains(t, w.Body.String(), "<script>")
	require.Contains(t, w.Body.String(), `\u003cscript\u003e`)
	w = pelicanHandlerRequest("POST", "/", "", h.Stop, id)
	require.Equal(t, http.StatusOK, w.Code)
	require.Contains(t, w.Body.String(), "canceling")
	require.Equal(t, 1, store.stops)
	w = pelicanHandlerRequest("POST", "/", `{"all":true}`, h.StopAll, nil)
	require.Equal(t, http.StatusOK, w.Code)
	require.Contains(t, w.Body.String(), `"affected":1`)
}

func TestPelicanHandlerInvalidFilters(t *testing.T) {
	h := &PelicanBenchmarkHandler{}
	for _, query := range []string{"page=-1", "page_size=0", "account_id=abc", "batch_id=invalid", "status=unknown"} {
		w := pelicanHandlerRequest("GET", "/?"+query, "", h.List, nil)
		require.Equal(t, http.StatusBadRequest, w.Code, query)
	}
}

func TestPelicanHandlerStopHonorsAccountManagementAccess(t *testing.T) {
	store := &pelicanHandlerStore{}
	accounts := &pelicanHandlerAccounts{accessErr: errors.New("remote account is read-only")}
	h := &PelicanBenchmarkHandler{store: store, accounts: accounts}
	id := gin.Params{{Key: "id", Value: uuid.NewString()}}
	w := pelicanHandlerRequest("POST", "/", "", h.Stop, id)
	require.Equal(t, http.StatusInternalServerError, w.Code)
	require.Equal(t, 0, store.stops)
	w = pelicanHandlerRequest("POST", "/", `{"all":true}`, h.StopAll, nil)
	require.Equal(t, http.StatusInternalServerError, w.Code)
	require.Equal(t, 0, store.stops)
}
