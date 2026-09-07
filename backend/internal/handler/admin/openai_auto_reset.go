package admin

import (
	"context"
	"strconv"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type openAIAutoResetConfigService interface {
	GetAutoResetConfig(context.Context, int64) (service.OpenAIAutoResetConfig, error)
	SetAutoResetConfig(context.Context, int64, bool, float64, float64) (service.OpenAIAutoResetConfig, error)
}

func (h *OpenAIOAuthHandler) AutoResetConfig(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "invalid account id")
		return
	}
	if err := ensureAdminAccountManagementAccess(c.Request.Context(), h.adminService, id); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	svc, ok := h.quotaService.(openAIAutoResetConfigService)
	if !ok {
		response.ErrorFrom(c, service.ErrOpenAIResetUnavailable)
		return
	}
	var result service.OpenAIAutoResetConfig
	if c.Request.Method == "PUT" {
		// Pointers make missing/null enablement distinct from an explicit OFF.
		var input struct {
			Enabled     *bool    `json:"enabled"`
			Threshold5h *float64 `json:"threshold_5h"`
			Threshold7d *float64 `json:"threshold_7d"`
		}
		if c.ShouldBindJSON(&input) != nil || input.Enabled == nil || input.Threshold5h == nil || input.Threshold7d == nil {
			response.BadRequest(c, "explicit enablement and both thresholds are required")
			return
		}
		result, err = svc.SetAutoResetConfig(c.Request.Context(), id, *input.Enabled, *input.Threshold5h, *input.Threshold7d)
	} else {
		result, err = svc.GetAutoResetConfig(c.Request.Context(), id)
	}
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, result)
}
