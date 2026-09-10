package admin

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sort"
	"strconv"

	"github.com/Wei-Shaw/sub2api/internal/benchmark"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type PelicanBenchmarkHandler struct {
	store     benchmark.Store
	accounts  service.AdminService
	manager   *benchmark.Manager
	nodeOwner func(*service.Account) string
}

func NewPelicanBenchmarkHandler(store benchmark.Store, accounts service.AdminService, tester *service.AccountTestService) *PelicanBenchmarkHandler {
	h := &PelicanBenchmarkHandler{store: store, accounts: accounts, manager: benchmark.NewManager(store, tester), nodeOwner: tester.PelicanExecutionNodeID}
	h.manager.Start()
	return h
}

func (h *PelicanBenchmarkHandler) StopWorkers() {
	if h != nil && h.manager != nil {
		h.manager.Stop()
	}
}

func pelicanJSON(c *gin.Context, target any, allowEmpty bool) bool {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 64<<10)
	d := json.NewDecoder(c.Request.Body)
	d.DisallowUnknownFields()
	if err := d.Decode(target); err != nil {
		if allowEmpty && errors.Is(err, io.EOF) {
			return true
		}
		response.BadRequest(c, "invalid benchmark request")
		return false
	}
	var extra any
	if !errors.Is(d.Decode(&extra), io.EOF) {
		response.BadRequest(c, "expected one JSON object")
		return false
	}
	return true
}

func pelicanHeaders(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	c.Header("X-Content-Type-Options", "nosniff")
	c.Header("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'; sandbox")
}

func pelicanError(c *gin.Context, err error) {
	if errors.Is(err, benchmark.ErrNotFound) {
		response.NotFound(c, "benchmark not found")
		return
	}
	// Do not expose SQL, credential-bearing upstream errors or result text.
	response.Error(c, http.StatusServiceUnavailable, "benchmark storage unavailable")
}

func (h *PelicanBenchmarkHandler) Create(c *gin.Context) {
	pelicanHeaders(c)
	var req struct {
		AccountIDs []int64 `json:"account_ids"`
		All        bool    `json:"all"`
		Model      string  `json:"model"`
		SourceID   string  `json:"source_id"`
		Action     string  `json:"action"`
	}
	single := c.Param("id") != ""
	if !pelicanJSON(c, &req, single) {
		return
	}
	if req.SourceID != "" || req.Action != "" {
		if single || req.All || len(req.AccountIDs) > 0 || req.Model != "" ||
			(req.Action != "continue" && req.Action != "retry") {
			response.BadRequest(c, "invalid benchmark follow-up")
			return
		}
		if _, err := uuid.Parse(req.SourceID); err != nil {
			response.BadRequest(c, "invalid source benchmark")
			return
		}
		source, err := h.store.Get(c.Request.Context(), req.SourceID)
		if err != nil {
			pelicanError(c, err)
			return
		}
		if err := ensureAdminAccountManagementAccess(c.Request.Context(), h.accounts, source.AccountID); err != nil {
			response.ErrorFrom(c, err)
			return
		}
		if source.Status == "queued" || source.Status == "running" || source.Status == "canceling" {
			response.Error(c, http.StatusConflict, "stop the current test and wait for it to exit first")
			return
		}
		req.AccountIDs, req.Model = []int64{source.AccountID}, source.Model
	}
	if single {
		id, err := strconv.ParseInt(c.Param("id"), 10, 64)
		if err != nil || id <= 0 || req.All || len(req.AccountIDs) != 0 {
			response.BadRequest(c, "invalid single-account benchmark request")
			return
		}
		req.AccountIDs = []int64{id}
	}
	if req.All == (len(req.AccountIDs) > 0) {
		response.BadRequest(c, "select account_ids or all=true")
		return
	}
	if len(req.AccountIDs) > benchmark.MaxBatchSize {
		response.BadRequest(c, "benchmark batch exceeds 2000 accounts")
		return
	}
	if req.Model == "" {
		req.Model = benchmark.DefaultModel
	}
	if !benchmark.ValidModel(req.Model) {
		response.BadRequest(c, "invalid model")
		return
	}
	ctx := c.Request.Context()
	var accounts []*service.Account
	if req.All {
		for page := 1; ; page++ {
			items, total, err := h.accounts.ListAccounts(ctx, page, 200, service.PlatformOpenAI, "", "", "", 0, "", "id", "asc")
			if err != nil {
				pelicanError(c, err)
				return
			}
			if total > benchmark.MaxBatchSize || len(accounts)+len(items) > benchmark.MaxBatchSize {
				response.BadRequest(c, "benchmark batch exceeds 2000 accounts")
				return
			}
			for i := range items {
				accounts = append(accounts, &items[i])
			}
			if len(items) == 0 || int64(page*200) >= total {
				break
			}
		}
	} else {
		for _, id := range req.AccountIDs {
			if id <= 0 {
				response.BadRequest(c, "invalid account id")
				return
			}
		}
		var err error
		accounts, err = h.accounts.GetAccountsByIDs(ctx, req.AccountIDs)
		if err != nil {
			pelicanError(c, err)
			return
		}
	}
	result := benchmark.Created{BatchID: uuid.NewString(), Tasks: []benchmark.Task{}, Skipped: []benchmark.Skipped{}}
	seen := map[int64]bool{}
	tasks := []benchmark.Task{}
	for _, account := range accounts {
		if account == nil || seen[account.ID] {
			continue
		}
		if req.SourceID != "" && account.ID != req.AccountIDs[0] {
			continue
		}
		seen[account.ID] = true
		reason := service.PelicanBenchmarkEligibility(account, req.Model)
		if reason == "" && ensureAdminAccountManagementAccess(ctx, h.accounts, account.ID) != nil {
			reason = "account_access_denied"
		}
		if reason != "" {
			result.Skipped = append(result.Skipped, benchmark.Skipped{AccountID: account.ID, Reason: reason})
			continue
		}
		owner := account.ExecutionNodeID("")
		if h.nodeOwner != nil {
			owner = h.nodeOwner(account)
		}
		tasks = append(tasks, benchmark.Task{ID: uuid.NewString(), BatchID: result.BatchID, AccountID: account.ID, AccountName: account.Name, Model: req.Model,
			ExecutionNodeID: owner, SourceID: req.SourceID, Action: req.Action})
	}
	for _, id := range req.AccountIDs {
		if !seen[id] {
			result.Skipped = append(result.Skipped, benchmark.Skipped{AccountID: id, Reason: "account_not_found"})
			seen[id] = true
		}
	}
	// Stable insertion order avoids reversed multi-account unique-index waits.
	sort.Slice(tasks, func(i, j int) bool { return tasks[i].AccountID < tasks[j].AccountID })
	if len(tasks) > 0 {
		created, skipped, err := h.store.Create(ctx, tasks)
		if err != nil {
			pelicanError(c, err)
			return
		}
		result.Tasks = created
		result.Skipped = append(result.Skipped, skipped...)
		if h.manager != nil && len(created) > 0 {
			h.manager.Wake()
		}
	}
	response.Accepted(c, result)
}

func (h *PelicanBenchmarkHandler) List(c *gin.Context) {
	pelicanHeaders(c)
	f := benchmark.Filter{BatchID: c.Query("batch_id"), Status: c.Query("status"), Page: 1, PageSize: 20}
	for name, dest := range map[string]*int{"page": &f.Page, "page_size": &f.PageSize} {
		if raw := c.Query(name); raw != "" {
			value, err := strconv.Atoi(raw)
			if err != nil || value < 1 || value > 1000000 {
				response.BadRequest(c, "invalid pagination")
				return
			}
			*dest = value
		}
	}
	if f.PageSize > 100 {
		f.PageSize = 100
	}
	if raw := c.Query("account_id"); raw != "" {
		id, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || id <= 0 {
			response.BadRequest(c, "invalid account id")
			return
		}
		f.AccountID = id
	}
	if f.BatchID != "" {
		if _, err := uuid.Parse(f.BatchID); err != nil {
			response.BadRequest(c, "invalid batch id")
			return
		}
	}
	if f.Status != "" && !benchmark.ValidStatus(f.Status) {
		response.BadRequest(c, "invalid status")
		return
	}
	page, err := h.store.List(c.Request.Context(), f)
	if err != nil {
		pelicanError(c, err)
		return
	}
	response.Success(c, page)
}

func pelicanID(c *gin.Context) (string, bool) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "invalid benchmark id")
		return "", false
	}
	return id.String(), true
}

func (h *PelicanBenchmarkHandler) Detail(c *gin.Context) {
	pelicanHeaders(c)
	id, ok := pelicanID(c)
	if !ok {
		return
	}
	result, err := h.store.Detail(c.Request.Context(), id)
	if err != nil {
		pelicanError(c, err)
		return
	}
	// gin.JSON escapes HTML. Never render this string through a template or
	// expose a same-origin HTML document, including on the detail endpoint.
	response.Success(c, result)
}

func (h *PelicanBenchmarkHandler) Stop(c *gin.Context) {
	pelicanHeaders(c)
	id, ok := pelicanID(c)
	if !ok {
		return
	}
	task, err := h.store.Get(c.Request.Context(), id)
	if err != nil {
		pelicanError(c, err)
		return
	}
	if err := ensureAdminAccountManagementAccess(c.Request.Context(), h.accounts, task.AccountID); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	if _, err := h.store.Stop(c.Request.Context(), id, task.AccountID); err != nil {
		pelicanError(c, err)
		return
	}
	task, err = h.store.Get(c.Request.Context(), id)
	if err != nil {
		pelicanError(c, err)
		return
	}
	response.Success(c, task)
}

func (h *PelicanBenchmarkHandler) StopAll(c *gin.Context) {
	pelicanHeaders(c)
	var req struct {
		All       bool  `json:"all"`
		AccountID int64 `json:"account_id"`
	}
	if !pelicanJSON(c, &req, false) {
		return
	}
	if req.AccountID < 0 || req.All == (req.AccountID > 0) {
		response.BadRequest(c, "select account_id or all=true")
		return
	}
	ctx := c.Request.Context()
	accountIDs := map[int64]struct{}{}
	if req.AccountID > 0 {
		if err := ensureAdminAccountManagementAccess(ctx, h.accounts, req.AccountID); err != nil {
			response.ErrorFrom(c, err)
			return
		}
	} else {
		for _, status := range []string{"queued", "running"} {
			for page := 1; ; page++ {
				result, err := h.store.List(ctx, benchmark.Filter{Status: status, Page: page, PageSize: 100})
				if err != nil {
					pelicanError(c, err)
					return
				}
				for _, task := range result.Items {
					if task.AccountID > 0 {
						accountIDs[task.AccountID] = struct{}{}
					}
				}
				if len(result.Items) == 0 || int64(page*result.PageSize) >= result.Total {
					break
				}
			}
		}
		for accountID := range accountIDs {
			if err := ensureAdminAccountManagementAccess(ctx, h.accounts, accountID); err != nil {
				response.ErrorFrom(c, err)
				return
			}
		}
	}
	// Restrict the mutation to the account IDs that were both observed and
	// authorized above. Do not re-list while mutating: changing a paginated
	// result set can skip rows, and newly-created rows must not inherit an
	// authorization decision from this request.
	if req.AccountID > 0 {
		affected, err := h.store.Stop(ctx, "", req.AccountID)
		if err != nil {
			pelicanError(c, err)
			return
		}
		response.Success(c, gin.H{"affected": affected})
		return
	}
	authorized := make([]int64, 0, len(accountIDs))
	for accountID := range accountIDs {
		authorized = append(authorized, accountID)
	}
	sort.Slice(authorized, func(i, j int) bool { return authorized[i] < authorized[j] })
	affected := int64(0)
	for _, accountID := range authorized {
		n, err := h.store.Stop(ctx, "", accountID)
		if err != nil {
			pelicanError(c, err)
			return
		}
		affected += n
	}
	response.Success(c, gin.H{"affected": affected})
}
