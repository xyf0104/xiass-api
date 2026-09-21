package admin

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

const maxCodexTicketCaptureProxyIDs = service.MaxOpenAICodexTicketCaptureProxyIDs

type refreshCodexTicketRequest struct {
	Model    string   `json:"model"`
	ProxyIDs *[]int64 `json:"proxy_ids,omitempty"`
}

type setCodexTicketEnabledRequest struct {
	Enabled *bool `json:"enabled" binding:"required"`
}

// SetCodexTicketCaptureProxies saves only the automatic capture defaults. It
// neither enables tickets nor changes the account's business proxy bindings.
func (h *AccountHandler) SetCodexTicketCaptureProxies(c *gin.Context) {
	accountID, err := strconv.ParseInt(strings.TrimSpace(c.Param("id")), 10, 64)
	if err != nil || accountID <= 0 {
		response.BadRequest(c, "Invalid account ID")
		return
	}
	if err := h.ensureAccountManagementAccess(c.Request.Context(), accountID); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	var req struct {
		ProxyIDs *[]int64 `json:"proxy_ids"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.ProxyIDs == nil {
		response.BadRequest(c, "proxy_ids is required; use an empty array to follow the business exits")
		return
	}
	account, err := h.adminService.GetAccount(c.Request.Context(), accountID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	if !service.IsOpenAICodexTicketAccount(account) {
		response.BadRequest(c, "Account is not an eligible OpenAI OAuth account")
		return
	}
	ids := []int64{}
	if len(*req.ProxyIDs) > 0 {
		proxies, err := h.loadCodexTicketCaptureProxies(c.Request.Context(), *req.ProxyIDs)
		if err != nil {
			response.ErrorFrom(c, err)
			return
		}
		for _, proxy := range proxies {
			ids = append(ids, proxy.ID)
		}
	}
	if err := h.adminService.UpdateAccountExtra(c.Request.Context(), accountID, map[string]any{
		service.OpenAICodexTicketCaptureProxyIDsExtraKey: ids,
	}); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"proxy_ids": ids})
}

// SetCodexTicketEnabled changes the explicit per-account opt-in. Missing keys
// remain disabled, so upgrades and newly created accounts never start probing
// without an administrator action.
// PUT /api/v1/admin/accounts/:id/codex-ticket
func (h *AccountHandler) SetCodexTicketEnabled(c *gin.Context) {
	accountID, err := strconv.ParseInt(strings.TrimSpace(c.Param("id")), 10, 64)
	if err != nil || accountID <= 0 {
		response.BadRequest(c, "Invalid account ID")
		return
	}
	if err := h.ensureAccountManagementAccess(c.Request.Context(), accountID); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	var req setCodexTicketEnabledRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.Enabled == nil {
		response.BadRequest(c, "Invalid request: enabled is required")
		return
	}
	account, err := h.adminService.GetAccount(c.Request.Context(), accountID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	if !service.IsOpenAICodexTicketAccount(account) {
		response.BadRequest(c, "Account is not an eligible OpenAI OAuth account")
		return
	}
	if err := h.adminService.UpdateAccountExtra(c.Request.Context(), accountID, map[string]any{
		service.OpenAICodexTicketEnabledExtraKey: *req.Enabled,
	}); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"enabled": *req.Enabled})
}

// RefreshCodexTicket forces a complete per-egress probe for one account/model.
// The response contains only redacted status summaries.
// POST /api/v1/admin/accounts/:id/codex-ticket/refresh
func (h *AccountHandler) RefreshCodexTicket(c *gin.Context) {
	accountID, err := strconv.ParseInt(strings.TrimSpace(c.Param("id")), 10, 64)
	if err != nil || accountID <= 0 {
		response.BadRequest(c, "Invalid account ID")
		return
	}
	if err := h.ensureAccountManagementAccess(c.Request.Context(), accountID); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	if h == nil || h.codexTicketRefresher == nil {
		response.ErrorFrom(c, infraerrors.ServiceUnavailable("CODEX_TICKET_REFRESH_UNAVAILABLE", "Codex Ticket harvester is temporarily unavailable"))
		return
	}
	var req refreshCodexTicketRequest
	if c.Request.ContentLength != 0 {
		if err := c.ShouldBindJSON(&req); err != nil {
			response.BadRequest(c, "Invalid request: "+err.Error())
			return
		}
	}
	model := strings.TrimSpace(req.Model)
	if model == "" {
		response.BadRequest(c, "Model is required")
		return
	}
	var statuses []service.OpenAICodexTicketStatus
	if req.ProxyIDs == nil {
		statuses, err = h.codexTicketRefresher.RefreshOpenAICodexTicket(c.Request.Context(), accountID, model)
	} else {
		if len(*req.ProxyIDs) == 0 {
			response.BadRequest(c, "proxy_ids must contain at least one proxy")
			return
		}
		refresher, ok := h.codexTicketRefresher.(codexTicketProxyRefresher)
		if !ok {
			response.ErrorFrom(c, infraerrors.ServiceUnavailable("CODEX_TICKET_PROXY_REFRESH_UNAVAILABLE", "Codex Ticket proxy selection is temporarily unavailable"))
			return
		}
		proxies, validationErr := h.loadCodexTicketCaptureProxies(c.Request.Context(), *req.ProxyIDs)
		if validationErr != nil {
			response.ErrorFrom(c, validationErr)
			return
		}
		statuses, err = refresher.RefreshOpenAICodexTicketWithProxies(c.Request.Context(), accountID, model, proxies)
	}
	if err != nil {
		if errors.Is(err, service.ErrOpenAICodexTicketAccountDisabled) {
			response.BadRequest(c, "Enable Codex Ticket for this account before refreshing")
			return
		}
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"statuses": statuses})
}

func (h *AccountHandler) loadCodexTicketCaptureProxies(ctx context.Context, ids []int64) ([]*service.Proxy, error) {
	if len(ids) > maxCodexTicketCaptureProxyIDs {
		return nil, infraerrors.BadRequest("CODEX_TICKET_PROXY_LIMIT_EXCEEDED", fmt.Sprintf("proxy_ids supports at most %d entries", maxCodexTicketCaptureProxyIDs))
	}
	unique := make([]int64, 0, len(ids))
	seen := make(map[int64]struct{}, len(ids))
	for _, id := range ids {
		if id <= 0 {
			return nil, infraerrors.BadRequest("CODEX_TICKET_PROXY_INVALID", "proxy_ids must contain positive IDs")
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		unique = append(unique, id)
	}
	proxies, err := h.adminService.GetProxiesByIDs(ctx, unique)
	if err != nil {
		return nil, err
	}
	byID := make(map[int64]service.Proxy, len(proxies))
	for _, proxy := range proxies {
		byID[proxy.ID] = proxy
	}
	now := time.Now()
	out := make([]*service.Proxy, 0, len(unique))
	for _, id := range unique {
		proxy, ok := byID[id]
		if !ok {
			return nil, infraerrors.BadRequest("CODEX_TICKET_PROXY_NOT_FOUND", fmt.Sprintf("Proxy %d was not found", id))
		}
		if !proxy.IsActive() {
			return nil, infraerrors.BadRequest("CODEX_TICKET_PROXY_INACTIVE", fmt.Sprintf("Proxy %d is not active", id))
		}
		if proxy.IsExpired(now) {
			return nil, infraerrors.BadRequest("CODEX_TICKET_PROXY_EXPIRED", fmt.Sprintf("Proxy %d is expired", id))
		}
		proxyCopy := proxy
		out = append(out, &proxyCopy)
	}
	return out, nil
}
