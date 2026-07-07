package admin

import (
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type SoftRouterProxyHandler struct {
	service *service.SoftRouterProxyService
}

func NewSoftRouterProxyHandler(service *service.SoftRouterProxyService) *SoftRouterProxyHandler {
	return &SoftRouterProxyHandler{service: service}
}

type softRouterConfigRequest struct {
	Enabled           bool   `json:"enabled"`
	PublicHost        string `json:"public_host"`
	GatewayListenHost string `json:"gateway_listen_host"`
	UpstreamHost      string `json:"upstream_host"`
	FRPServerHost     string `json:"frp_server_host"`
	FRPServerPort     int    `json:"frp_server_port"`
	FRPToken          string `json:"frp_token"`
	RawPortStart      int    `json:"raw_port_start"`
	RawPortEnd        int    `json:"raw_port_end"`
	PublicPortStart   int    `json:"public_port_start"`
	PublicPortEnd     int    `json:"public_port_end"`
	DefaultUsername   string `json:"default_username"`
	DefaultPassword   string `json:"default_password"`
	AgentPollSeconds  int    `json:"agent_poll_seconds"`
}

type softRouterAgentRequest struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

type softRouterMappingRequest struct {
	AgentID       int64  `json:"agent_id"`
	NodeID        *int64 `json:"node_id"`
	Name          string `json:"name"`
	OpenWrtPort   int    `json:"openwrt_port"`
	RawRemotePort int    `json:"raw_remote_port"`
	PublicPort    int    `json:"public_port"`
	Username      string `json:"username"`
	Password      string `json:"password"`
	Enabled       *bool  `json:"enabled"`
}

func (h *SoftRouterProxyHandler) Overview(c *gin.Context) {
	out, err := h.service.GetOverview(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, out)
}

func (h *SoftRouterProxyHandler) UpdateConfig(c *gin.Context) {
	var req softRouterConfigRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	cfg, err := h.service.UpdateConfig(c.Request.Context(), &service.SoftRouterProxyConfig{
		Enabled:           req.Enabled,
		PublicHost:        req.PublicHost,
		GatewayListenHost: req.GatewayListenHost,
		UpstreamHost:      req.UpstreamHost,
		FRPServerHost:     req.FRPServerHost,
		FRPServerPort:     req.FRPServerPort,
		FRPToken:          req.FRPToken,
		RawPortStart:      req.RawPortStart,
		RawPortEnd:        req.RawPortEnd,
		PublicPortStart:   req.PublicPortStart,
		PublicPortEnd:     req.PublicPortEnd,
		DefaultUsername:   req.DefaultUsername,
		DefaultPassword:   req.DefaultPassword,
		AgentPollSeconds:  req.AgentPollSeconds,
	})
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, cfg)
}

func (h *SoftRouterProxyHandler) FRPStatus(c *gin.Context) {
	status, err := h.service.GetFRPStatus(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, status)
}

func (h *SoftRouterProxyHandler) InstallFRP(c *gin.Context) {
	var req softRouterConfigRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	result, err := h.service.InstallFRP(c.Request.Context(), service.SoftRouterFRPInstallInput{
		PublicHost:        req.PublicHost,
		GatewayListenHost: req.GatewayListenHost,
		UpstreamHost:      req.UpstreamHost,
		FRPServerHost:     req.FRPServerHost,
		FRPServerPort:     req.FRPServerPort,
		FRPToken:          req.FRPToken,
		RawPortStart:      req.RawPortStart,
		RawPortEnd:        req.RawPortEnd,
		PublicPortStart:   req.PublicPortStart,
		PublicPortEnd:     req.PublicPortEnd,
		DefaultUsername:   req.DefaultUsername,
		DefaultPassword:   req.DefaultPassword,
		AgentPollSeconds:  req.AgentPollSeconds,
	})
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, result)
}

func (h *SoftRouterProxyHandler) CreateAgent(c *gin.Context) {
	var req softRouterAgentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	agent, err := h.service.CreateAgent(c.Request.Context(), &service.SoftRouterAgent{
		Name:        req.Name,
		Description: req.Description,
	})
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, agent)
}

func (h *SoftRouterProxyHandler) UpdateAgent(c *gin.Context) {
	id, ok := parseIDParam(c, "id")
	if !ok {
		return
	}
	var req softRouterAgentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	agent, err := h.service.UpdateAgent(c.Request.Context(), id, &service.SoftRouterAgent{
		Name:        req.Name,
		Description: req.Description,
	})
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, agent)
}

func (h *SoftRouterProxyHandler) DeleteAgent(c *gin.Context) {
	id, ok := parseIDParam(c, "id")
	if !ok {
		return
	}
	if err := h.service.DeleteAgent(c.Request.Context(), id); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"message": "agent deleted"})
}

func (h *SoftRouterProxyHandler) RotateAgentToken(c *gin.Context) {
	id, ok := parseIDParam(c, "id")
	if !ok {
		return
	}
	agent, err := h.service.RotateAgentToken(c.Request.Context(), id)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, agent)
}

func (h *SoftRouterProxyHandler) CreateMapping(c *gin.Context) {
	var req softRouterMappingRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	mapping, err := h.service.CreateMapping(c.Request.Context(), mappingRequestToService(req))
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, mapping)
}

func (h *SoftRouterProxyHandler) UpdateMapping(c *gin.Context) {
	id, ok := parseIDParam(c, "id")
	if !ok {
		return
	}
	var req softRouterMappingRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	mapping, err := h.service.UpdateMapping(c.Request.Context(), id, mappingRequestToService(req))
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, mapping)
}

func (h *SoftRouterProxyHandler) DeleteMapping(c *gin.Context) {
	id, ok := parseIDParam(c, "id")
	if !ok {
		return
	}
	if err := h.service.DeleteMapping(c.Request.Context(), id); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"message": "mapping deleted"})
}

func (h *SoftRouterProxyHandler) Reconcile(c *gin.Context) {
	if err := h.service.Reconcile(c.Request.Context()); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"message": "reconciled"})
}

func (h *SoftRouterProxyHandler) AgentReport(c *gin.Context) {
	token := bearerToken(c)
	if token == "" {
		response.Unauthorized(c, "missing agent token")
		return
	}
	var req struct {
		Hostname         string `json:"hostname"`
		SnapshotComplete *bool  `json:"snapshot_complete"`
		Socks            []struct {
			ID           string `json:"id"`
			NodeKey      string `json:"node_key"`
			Name         string `json:"name"`
			OpenWrtPort  int    `json:"openwrt_port"`
			Port         int    `json:"port"`
			HTTPPort     int    `json:"http_port"`
			NodeRef      string `json:"node_ref"`
			Node         string `json:"node"`
			ListenStatus string `json:"listen_status"`
			Listen       string `json:"listen"`
			Enabled      bool   `json:"enabled"`
		} `json:"socks"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	nodes := make([]service.SoftRouterSocksNodeReport, 0, len(req.Socks))
	for _, item := range req.Socks {
		port := item.OpenWrtPort
		if port == 0 {
			port = item.Port
		}
		listen := firstNonEmptyString(item.ListenStatus, item.Listen)
		nodes = append(nodes, service.SoftRouterSocksNodeReport{
			ID:           item.ID,
			NodeKey:      item.NodeKey,
			Name:         item.Name,
			OpenWrtPort:  port,
			HTTPPort:     item.HTTPPort,
			NodeRef:      firstNonEmptyString(item.NodeRef, item.Node),
			ListenStatus: listen,
			Enabled:      item.Enabled,
		})
	}
	err := h.service.ReportAgent(c.Request.Context(), token, service.SoftRouterAgentReportInput{
		Hostname:         req.Hostname,
		SnapshotComplete: req.SnapshotComplete == nil || *req.SnapshotComplete,
		Nodes:            nodes,
	})
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"message": "reported"})
}

func (h *SoftRouterProxyHandler) AgentConfig(c *gin.Context) {
	token := bearerToken(c)
	if token == "" {
		response.Unauthorized(c, "missing agent token")
		return
	}
	cfg, err := h.service.GetDesiredConfig(c.Request.Context(), token)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, cfg)
}

func mappingRequestToService(req softRouterMappingRequest) *service.SoftRouterProxyMapping {
	mapping := &service.SoftRouterProxyMapping{
		AgentID:       req.AgentID,
		NodeID:        req.NodeID,
		Name:          req.Name,
		OpenWrtPort:   req.OpenWrtPort,
		RawRemotePort: req.RawRemotePort,
		PublicPort:    req.PublicPort,
		Username:      req.Username,
		Password:      req.Password,
	}
	if req.Enabled != nil {
		mapping.Enabled = *req.Enabled
		mapping.EnabledSet = true
	}
	return mapping
}

func bearerToken(c *gin.Context) string {
	header := strings.TrimSpace(c.GetHeader("Authorization"))
	if header == "" {
		return ""
	}
	const prefix = "Bearer "
	if strings.HasPrefix(strings.ToLower(header), strings.ToLower(prefix)) {
		return strings.TrimSpace(header[len(prefix):])
	}
	return header
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
