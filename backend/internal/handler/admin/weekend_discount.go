package admin

import (
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"time"
)

func (h *SettingHandler) GetWeekendDiscount(c *gin.Context) {
	s, err := h.settingService.GetWeekendDiscount(c.Request.Context())
	if err != nil {
		response.InternalError(c, "Unable to load weekend activity")
		return
	}
	c.Header("Cache-Control", "no-store")
	response.Success(c, s.Status(time.Now()))
}

func (h *SettingHandler) UpdateWeekendDiscount(c *gin.Context) {
	var s service.WeekendDiscountSettings
	if err := c.ShouldBindJSON(&s); err != nil {
		response.BadRequest(c, "Invalid activity settings")
		return
	}
	if err := s.Validate(); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	if err := h.settingService.SetWeekendDiscount(c.Request.Context(), s); err != nil {
		response.InternalError(c, "Unable to save weekend activity")
		return
	}
	c.Header("Cache-Control", "no-store")
	response.Success(c, s.Status(time.Now()))
}
