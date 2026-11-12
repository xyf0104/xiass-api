package admin

import (
	"context"
	"encoding/json"
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
	items    []*service.Account
	platform string
}

func (s *pelicanHandlerAccounts) GetAccountsByIDs(context.Context, []int64) ([]*service.Account, error) {
	return s.items, nil
}
func (s *pelicanHandlerAccounts) ListAccounts(_ context.Context, page, size int, platform, _, _, _ string, _ int64, _, _, _ string) ([]service.Account, int64, error) {
	s.platform = platform
	items := []service.Account{}
	for _, a := range s.items {
		items = append(items, *a)
	}
	total := len(items)
	start := min((page-1)*size, total)
	return items[start:min(start+size, total)], int64(total), nil
}

type pelicanHandlerStore struct {
	benchmark.Store
	tasks         []benchmark.Task
	stops         int
	html          string
	detailStatus  string
	textOutput    string
	source        *benchmark.Task
	stopAccountID int64
	filter        benchmark.Filter
}

func (s *pelicanHandlerStore) Create(_ context.Context, tasks []benchmark.Task) ([]benchmark.Task, []benchmark.Skipped, error) {
	s.tasks = tasks
	return tasks, nil, nil
}
func (s *pelicanHandlerStore) List(_ context.Context, filter benchmark.Filter) (*benchmark.Page, error) {
	s.filter = filter
	return &benchmark.Page{Items: []benchmark.Task{{ID: "test", AccountID: 1, ExecutionNodeID: "legacy", Status: "running", HTMLBytes: len(s.html)}}, Total: 1, Page: 1, PageSize: 100}, nil
}

func TestIQHandlerPairedPagination(t *testing.T) {
	for _, query := range []string{"", "?paired=false", "?paired=true&status=succeeded&page=2&page_size=5"} {
		store := &pelicanHandlerStore{}
		h := &PelicanBenchmarkHandler{store: store}
		w := pelicanHandlerRequest("GET", "/"+query, "", h.List, nil)
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		require.Equal(t, strings.Contains(query, "paired=true"), store.filter.Paired)
		if store.filter.Paired {
			require.Equal(t, "succeeded", store.filter.Status)
			require.Equal(t, 2, store.filter.Page)
			require.Equal(t, 5, store.filter.PageSize)
		}
	}
	w := pelicanHandlerRequest("GET", "/?paired=invalid", "", (&PelicanBenchmarkHandler{}).List, nil)
	require.Equal(t, http.StatusBadRequest, w.Code)
}
func (s *pelicanHandlerStore) Detail(context.Context, string) (*benchmark.Detail, error) {
	if s.source != nil {
		return &benchmark.Detail{Task: *s.source, HTML: s.html, TextOutput: s.textOutput}, nil
	}
	return &benchmark.Detail{Task: benchmark.Task{ID: "test", ExecutionNodeID: "legacy", Status: s.detailStatus}, HTML: s.html}, nil
}

func TestIQHandlerCreatesBothTypesInOneBatchWithPromptSnapshots(t *testing.T) {
	for _, selector := range []string{`"all":true`, `"account_ids":[7,7]`} {
		store := &pelicanHandlerStore{}
		h := &PelicanBenchmarkHandler{store: store, accounts: &pelicanHandlerAccounts{items: []*service.Account{
			{ID: 7, Name: "test", Platform: service.PlatformAnthropic, Type: service.AccountTypeAPIKey},
		}}}
		w := pelicanHandlerRequest("POST", "/", `{`+selector+`,"models":{"7":"claude-opus-5-5"},"test_types":["pelican","candy"],"prompt":"custom 3D HTML"}`, h.Create, nil)
		require.Equal(t, http.StatusAccepted, w.Code, w.Body.String())
		require.Len(t, store.tasks, 2)
		require.Equal(t, store.tasks[0].BatchID, store.tasks[1].BatchID)
		require.NotEqual(t, store.tasks[0].ID, store.tasks[1].ID)
		for _, task := range store.tasks {
			require.EqualValues(t, 7, task.AccountID)
			require.Equal(t, "claude-opus-5-5", task.Model)
			require.Equal(t, benchmark.ResolvePrompt(task.TestType, "custom 3D HTML"), task.Prompt)
		}
		require.Contains(t, w.Body.String(), `"test_type":"candy"`)
		require.Contains(t, w.Body.String(), `"test_type":"pelican"`)
		require.Contains(t, w.Body.String(), `"answer":null`)
		require.Contains(t, w.Body.String(), `"passed":null`)
		require.NotContains(t, w.Body.String(), "custom 3D HTML")
	}
}

func TestIQHandlerTestAndPromptValidation(t *testing.T) {
	for _, extra := range []string{
		`"test_types":[]`, `"test_types":["unknown"]`, `"test_types":["pelican","pelican"]`,
		`"test_types":["pelican","candy","candy"]`, `"test_types":"candy"`,
		`"prompt":"a\u0000b"`, `"prompt":42`, `"prompt":"` + strings.Repeat("糖", 8001) + `"`,
	} {
		h := &PelicanBenchmarkHandler{}
		w := pelicanHandlerRequest("POST", "/", `{"account_ids":[7],`+extra+`}`, h.Create, nil)
		require.Equal(t, http.StatusBadRequest, w.Code, extra[:min(len(extra), 100)])
	}
	store := &pelicanHandlerStore{}
	h := &PelicanBenchmarkHandler{store: store, accounts: &pelicanHandlerAccounts{items: []*service.Account{
		{ID: 7, Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey},
	}}}
	w := pelicanHandlerRequest("POST", "/", `{"test_types":["candy"],"prompt":"ignored HTML prompt"}`, h.Create, gin.Params{{Key: "id", Value: "7"}})
	require.Equal(t, http.StatusAccepted, w.Code)
	require.Len(t, store.tasks, 1)
	require.Equal(t, benchmark.CandyPrompt, store.tasks[0].Prompt)
	w = pelicanHandlerRequest("POST", "/", `{"prompt":"`+strings.Repeat("糖", 8000)+`"}`, h.Create, gin.Params{{Key: "id", Value: "7"}})
	require.Equal(t, http.StatusAccepted, w.Code)
	require.Equal(t, strings.Repeat("糖", 8000), store.tasks[0].Prompt)
}

func TestIQHandlerFollowupInheritsTypePromptAndRejectsCandyContinue(t *testing.T) {
	for _, testType := range []string{benchmark.TestCandy, benchmark.TestPelican} {
		for _, action := range []string{"retry", "continue"} {
			source := &benchmark.Task{ID: uuid.NewString(), AccountID: 7, Model: "claude-opus-5-5", Status: "failed", TestType: testType,
				Prompt: "stored question snapshot"}
			store := &pelicanHandlerStore{source: source}
			h := &PelicanBenchmarkHandler{store: store, accounts: &pelicanHandlerAccounts{items: []*service.Account{
				{ID: 7, Platform: service.PlatformAnthropic, Type: service.AccountTypeAPIKey},
			}}}
			w := pelicanHandlerRequest("POST", "/", `{"source_id":"`+source.ID+`","action":"`+action+`"}`, h.Create, nil)
			if testType == benchmark.TestCandy && action == "continue" {
				require.Equal(t, http.StatusBadRequest, w.Code)
				require.Empty(t, store.tasks)
			} else {
				require.Equal(t, http.StatusAccepted, w.Code, w.Body.String())
				require.Len(t, store.tasks, 1)
				require.Equal(t, source.TestType, store.tasks[0].TestType)
				require.Equal(t, source.Prompt, store.tasks[0].Prompt)
			}
		}
	}
	for _, override := range []string{`,"test_types":["candy"]`, `,"test_types":[]`, `,"prompt":""`, `,"prompt":"new"`} {
		w := pelicanHandlerRequest("POST", "/", `{"source_id":"`+uuid.NewString()+`","action":"retry"`+override+`}`, (&PelicanBenchmarkHandler{}).Create, nil)
		require.Equal(t, http.StatusBadRequest, w.Code)
	}
}

func TestIQHandlerCandyDetailDoesNotRepairOrExposeHTML(t *testing.T) {
	answer, passed := 22, false
	store := &pelicanHandlerStore{source: &benchmark.Task{ID: uuid.NewString(), TestType: benchmark.TestCandy, Status: "succeeded", Answer: &answer, Passed: &passed},
		html: "<html>must not expose</html>", textOutput: "最终答案：22"}
	h := &PelicanBenchmarkHandler{store: store}
	w := pelicanHandlerRequest("GET", "/", "", h.Detail, gin.Params{{Key: "id", Value: store.source.ID}})
	require.Equal(t, http.StatusOK, w.Code)
	var response struct {
		Data map[string]any `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
	require.Equal(t, "", response.Data["html"])
	require.NotContains(t, response.Data, "preview_html")
	require.Equal(t, "最终答案：22", response.Data["text_output"])
	require.Equal(t, benchmark.CandyPrompt, response.Data["prompt"])
	require.Equal(t, float64(22), response.Data["answer"])
	require.Equal(t, false, response.Data["passed"])
}
func (s *pelicanHandlerStore) Get(context.Context, string) (*benchmark.Task, error) {
	if s.source != nil {
		return s.source, nil
	}
	return &benchmark.Task{ID: "test", AccountID: 1, ExecutionNodeID: "legacy", Status: "canceling"}, nil
}

func TestPelicanFollowupUsesSourceAccountAndModel(t *testing.T) {
	for _, action := range []string{"continue", "retry"} {
		sourceID := uuid.NewString()
		store := &pelicanHandlerStore{source: &benchmark.Task{ID: sourceID, AccountID: 7, Model: "gpt-6-astra", Status: "failed"}}
		accounts := &pelicanHandlerAccounts{items: []*service.Account{{ID: 7, Name: "paid", Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey}}}
		h := &PelicanBenchmarkHandler{store: store, accounts: accounts}
		body := `{"source_id":"` + sourceID + `","action":"` + action + `"}`
		w := pelicanHandlerRequest("POST", "/", body, h.Create, nil)
		require.Equal(t, http.StatusAccepted, w.Code, w.Body.String())
		require.Len(t, store.tasks, 1)
		require.Equal(t, int64(7), store.tasks[0].AccountID)
		require.Equal(t, sourceID, store.tasks[0].SourceID)
		require.Equal(t, action, store.tasks[0].Action)
		require.Equal(t, store.source.Model, store.tasks[0].Model)
	}
}

func TestPelicanFollowupRejectsActiveSourceAndOverrides(t *testing.T) {
	id := uuid.NewString()
	for _, status := range []string{"queued", "running", "canceling"} {
		store := &pelicanHandlerStore{source: &benchmark.Task{ID: id, AccountID: 1, Status: status}}
		h := &PelicanBenchmarkHandler{store: store, accounts: &pelicanHandlerAccounts{}}
		w := pelicanHandlerRequest("POST", "/", `{"source_id":"`+id+`","action":"continue"}`, h.Create, nil)
		require.Equal(t, http.StatusConflict, w.Code)
		require.Empty(t, store.tasks)
	}
	for _, fields := range []string{`,"account_ids":[2]`, `,"all":true`, `,"model":"gpt-5.6-sol"`, `,"models":{"1":"gpt-5.6-sol"}`, `,"prompt":"override"`} {
		h := &PelicanBenchmarkHandler{}
		w := pelicanHandlerRequest("POST", "/", `{"source_id":"`+id+`","action":"retry"`+fields+`}`, h.Create, nil)
		require.Equal(t, http.StatusBadRequest, w.Code)
	}
}
func (s *pelicanHandlerStore) Stop(_ context.Context, _ string, accountID int64) (int64, error) {
	s.stops++
	s.stopAccountID = accountID
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
		require.Len(t, store.tasks, 3)
		require.Equal(t, benchmark.DefaultModel, store.tasks[0].Model)
		require.Equal(t, int64(1), store.tasks[0].AccountID)
		require.Equal(t, int64(3), store.tasks[1].AccountID)
		require.Equal(t, service.PelicanBenchmarkDefaultModel(accounts.items[2]), store.tasks[1].Model)
		require.Equal(t, int64(4), store.tasks[2].AccountID)
		require.Empty(t, accounts.platform)
		require.NotContains(t, w.Body.String(), "credentials")
		require.NotContains(t, w.Body.String(), "execution_node_id")
		require.Contains(t, w.Body.String(), "free_plan")
		require.NotContains(t, w.Body.String(), "ineligible")
	}
}

func TestPelicanHandlerRejectsAmbiguousSelectors(t *testing.T) {
	for _, body := range []string{`{"all":true,"account_ids":[1]}`, `{}`, `{"account_ids":[-1]}`, `{"all":true,"model":"invalid\nmodel"}`, `{"all":true} {}`, `null`} {
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
	require.NotContains(t, w.Body.String(), "execution_node_id")
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
	require.NotContains(t, w.Body.String(), "execution_node_id")
	w = pelicanHandlerRequest("POST", "/", "", h.Stop, id)
	require.Equal(t, http.StatusOK, w.Code)
	require.Contains(t, w.Body.String(), "canceling")
	require.Equal(t, 1, store.stops)
	require.Equal(t, int64(1), store.stopAccountID)
	require.NotContains(t, w.Body.String(), "execution_node_id")
	w = pelicanHandlerRequest("POST", "/", `{"all":true}`, h.StopAll, nil)
	require.Equal(t, http.StatusOK, w.Code)
	require.Contains(t, w.Body.String(), `"affected":1`)
	require.Equal(t, 2, store.stops)
	require.Zero(t, store.stopAccountID)
}

func TestPelicanHandlerInvalidFilters(t *testing.T) {
	h := &PelicanBenchmarkHandler{}
	for _, query := range []string{"page=-1", "page_size=0", "account_id=abc", "batch_id=invalid", "status=unknown"} {
		w := pelicanHandlerRequest("GET", "/?"+query, "", h.List, nil)
		require.Equal(t, http.StatusBadRequest, w.Code, query)
	}
}

func TestPelicanDetailRepairsLegacyAndCurrentPreviewsAndPreservesStoredOriginal(t *testing.T) {
	original := `<html><script>const drawing={stroke='red'};</script></html>`
	for _, status := range []string{"succeeded", "failed", "running", "interrupted", "canceled"} {
		t.Run(status, func(t *testing.T) {
			store := &pelicanHandlerStore{html: original, detailStatus: status}
			h := &PelicanBenchmarkHandler{store: store}
			w := pelicanHandlerRequest("GET", "/", "", h.Detail, gin.Params{{Key: "id", Value: uuid.NewString()}})
			require.Equal(t, http.StatusOK, w.Code)
			var result struct {
				Data map[string]any `json:"data"`
			}
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &result))
			require.Equal(t, status, result.Data["status"])
			if status == "succeeded" {
				expected := strings.Replace(original, "stroke=", "stroke:", 1)
				require.Equal(t, expected, result.Data["html"])
				require.Equal(t, expected, result.Data["preview_html"])
				var legacy struct {
					Data struct {
						HTML string `json:"html"`
					} `json:"data"`
				}
				require.NoError(t, json.Unmarshal(w.Body.Bytes(), &legacy))
				require.Equal(t, expected, legacy.Data.HTML)
			} else {
				require.Equal(t, original, result.Data["html"])
				require.NotContains(t, result.Data, "preview_html")
			}
			require.NotContains(t, w.Body.String(), "<script>")
			require.Equal(t, "no-store", w.Header().Get("Cache-Control"))
			require.Equal(t, original, store.html)
			require.Empty(t, store.tasks)
			require.Zero(t, store.stops)
		})
	}
}

func TestPelicanDetailLeavesValidAndUnsupportedHTMLUnchanged(t *testing.T) {
	for _, original := range []string{
		`<html><script>const drawing={stroke:'red'};</script></html>`,
		`<html><script>const drawing={stroke:};</script></html>`,
		``,
	} {
		store := &pelicanHandlerStore{html: original, detailStatus: "succeeded"}
		h := &PelicanBenchmarkHandler{store: store}
		w := pelicanHandlerRequest("GET", "/", "", h.Detail, gin.Params{{Key: "id", Value: uuid.NewString()}})
		require.Equal(t, http.StatusOK, w.Code)
		var result struct {
			Data map[string]any `json:"data"`
		}
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &result))
		require.Equal(t, original, result.Data["html"])
		require.NotContains(t, result.Data, "preview_html")
		require.Equal(t, original, store.html)
	}
}

func TestPelicanHandlerMixedModelsAndAllPlatforms(t *testing.T) {
	for _, selector := range []string{`"all":true`, `"account_ids":[1,2,3,4]`} {
		accounts := &pelicanHandlerAccounts{items: []*service.Account{
			{ID: 1, Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey},
			{ID: 2, Platform: service.PlatformAnthropic, Type: service.AccountTypeSetupToken},
			{ID: 3, Platform: service.PlatformGemini, Type: service.AccountTypeOAuth},
			{ID: 4, Platform: service.PlatformGrok, Type: service.AccountTypeOAuth, Credentials: map[string]any{"plan_type": "free"}},
		}}
		store := &pelicanHandlerStore{}
		h := &PelicanBenchmarkHandler{store: store, accounts: accounts}
		w := pelicanHandlerRequest("POST", "/", `{`+selector+`,"models":{"2":"claude-sonnet-4-5@20250929","3":"gemini-2.5-flash"}}`, h.Create, nil)
		require.Equal(t, http.StatusAccepted, w.Code, w.Body.String())
		require.Len(t, store.tasks, 4)
		require.Equal(t, benchmark.DefaultModel, store.tasks[0].Model)
		require.Equal(t, "claude-sonnet-4-5@20250929", store.tasks[1].Model)
		require.Equal(t, "gemini-2.5-flash", store.tasks[2].Model)
		require.Equal(t, service.PelicanBenchmarkDefaultModel(accounts.items[3]), store.tasks[3].Model)
		require.Empty(t, accounts.platform)
	}
}

func TestPelicanHandlerInvalidPerAccountModels(t *testing.T) {
	for _, body := range []string{
		`{"account_ids":[1],"models":{"2":"claude-sonnet-4-5"}}`,
		`{"all":true,"models":{"0":"grok-4"}}`,
		`{"all":true,"models":{"1":"bad\nmodel"}}`,
		`{"all":true,"models":{"1":""}}`,
		`{"all":true,"models":{"bad":"grok-4"}}`,
	} {
		h := &PelicanBenchmarkHandler{}
		w := pelicanHandlerRequest("POST", "/", body, h.Create, nil)
		require.Equal(t, http.StatusBadRequest, w.Code, body)
	}
}

func TestPelicanAllPlatformsPaginationAndQueueBound(t *testing.T) {
	for _, count := range []int{201, benchmark.MaxBatchSize + 1} {
		accounts := &pelicanHandlerAccounts{}
		for i := 1; i <= count; i++ {
			accounts.items = append(accounts.items, &service.Account{ID: int64(i), Platform: service.PlatformAnthropic, Type: service.AccountTypeAPIKey})
		}
		store := &pelicanHandlerStore{}
		h := &PelicanBenchmarkHandler{store: store, accounts: accounts}
		w := pelicanHandlerRequest("POST", "/", `{"all":true}`, h.Create, nil)
		if count > benchmark.MaxBatchSize {
			require.Equal(t, http.StatusBadRequest, w.Code)
			require.Empty(t, store.tasks)
		} else {
			require.Equal(t, http.StatusAccepted, w.Code)
			require.Len(t, store.tasks, count)
		}
		require.Empty(t, accounts.platform)
	}
}
