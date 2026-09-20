package admin

import (
	"strconv"
	"strings"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/gin-gonic/gin"
)

type refreshCodexTicketRequest struct {
	Model string `json:"model"`
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
	statuses, err := h.codexTicketRefresher.RefreshOpenAICodexTicket(c.Request.Context(), accountID, model)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"statuses": statuses})
}
