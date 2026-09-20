package admin

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

type proxySubscriptionRequest struct {
	Sources         []service.ProxySubscriptionSource `json:"sources"`
	SelectedNodeIDs []string                          `json:"selected_node_ids"`
	PreviewID       string                            `json:"preview_id"`
}

func (h *ProxyHandler) proxySubscriptionService(c *gin.Context) *service.ProxySubscriptionService {
	if h == nil || h.subscriptions == nil {
		response.Error(c, 503, "Proxy subscription service is unavailable")
		return nil
	}
	return h.subscriptions
}

// GetSubscriptions returns masked source URLs and safe node summaries only.
func (h *ProxyHandler) GetSubscriptions(c *gin.Context) {
	svc := h.proxySubscriptionService(c)
	if svc == nil {
		return
	}
	overview, err := svc.Overview(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, overview)
}

// PreviewSubscriptions downloads and parses all supplied sources without
// changing listeners, proxy records, account bindings, or stored settings.
func (h *ProxyHandler) PreviewSubscriptions(c *gin.Context) {
	svc := h.proxySubscriptionService(c)
	if svc == nil {
		return
	}
	var req proxySubscriptionRequest
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 40<<20)
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	overview, err := svc.Preview(c.Request.Context(), normalizeSubscriptionRequestSources(req.Sources))
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, overview)
}

// ApplySubscriptions persists the source list and reconciles only the selected
// nodes into ordinary XIASS SOCKS5H proxy records.
func (h *ProxyHandler) ApplySubscriptions(c *gin.Context) {
	svc := h.proxySubscriptionService(c)
	if svc == nil {
		return
	}
	var req proxySubscriptionRequest
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 40<<20)
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	overview, err := svc.Apply(c.Request.Context(), normalizeSubscriptionRequestSources(req.Sources), req.SelectedNodeIDs, req.PreviewID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, overview)
}

func (h *ProxyHandler) RefreshSubscriptions(c *gin.Context) {
	svc := h.proxySubscriptionService(c)
	if svc == nil {
		return
	}
	overview, err := svc.Refresh(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, overview)
}

func normalizeSubscriptionRequestSources(sources []service.ProxySubscriptionSource) []service.ProxySubscriptionSource {
	for index := range sources {
		sources[index].ID = strings.TrimSpace(sources[index].ID)
		sources[index].Name = strings.TrimSpace(sources[index].Name)
		sources[index].URL = strings.TrimSpace(sources[index].URL)
		sources[index].UserAgent = strings.TrimSpace(sources[index].UserAgent)
	}
	return sources
}
