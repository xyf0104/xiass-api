package handler

import (
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/gin-gonic/gin"
	"time"
)

func (h *AvailableChannelHandler) WeekendDiscount(c *gin.Context) {
	s, err := h.settingService.GetWeekendDiscount(c.Request.Context())
	if err != nil {
		response.InternalError(c, "Unable to load weekend activity")
		return
	}
	c.Header("Cache-Control", "no-store")
	response.Success(c, s.Status(time.Now()))
}
