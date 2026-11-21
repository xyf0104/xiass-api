package admin

import (
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/gin-gonic/gin"
	"strconv"
)

func (h *ProxyHandler) GetTraffic(c *gin.Context) {
	svc := h.proxySubscriptionService(c)
	if svc == nil {
		return
	}
	days, err := strconv.Atoi(c.DefaultQuery("days", "30"))
	if err != nil || (days != 1 && days != 7 && days != 30) {
		response.BadRequest(c, "days must be 1, 7 or 30")
		return
	}
	proxies, err := h.adminService.GetAllProxies(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	report, err := svc.Traffic(c.Request.Context(), days, proxies)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	response.Success(c, report)
}
