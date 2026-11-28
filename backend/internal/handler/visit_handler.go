package handler

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ip"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type visitRecorder interface {
	Record(*service.AuditLog)
}

type VisitHandler struct {
	auditService visitRecorder
}

func NewVisitHandler(auditService *service.AuditLogService) *VisitHandler {
	return &VisitHandler{auditService: auditService}
}

// Record tracks completed website navigation. Identity and time come from the server.
func (h *VisitHandler) Record(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 2048)
	var req struct {
		Path string `json:"path" binding:"required,max=512"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid page path")
		return
	}
	page, err := url.Parse(req.Path)
	if err != nil || page.IsAbs() || page.Host != "" || !strings.HasPrefix(page.Path, "/") || strings.HasPrefix(page.Path, "//") {
		response.BadRequest(c, "Invalid page path")
		return
	}
	entry := &service.AuditLog{
		Action:     service.AuditActionSiteVisit,
		Method:     http.MethodGet,
		Path:       page.EscapedPath(),
		ClientIP:   ip.GetTrustedClientIP(c),
		UserAgent:  c.Request.UserAgent(),
		StatusCode: http.StatusOK,
	}
	if subject, ok := middleware.GetAuthSubjectFromContext(c); ok {
		uid := subject.UserID
		entry.ActorUserID = &uid
		entry.ActorEmail = c.GetString(middleware.ContextKeyAuthEmail)
		entry.ActorRole, _ = middleware.GetUserRoleFromContext(c)
		entry.AuthMethod = service.AuditAuthMethodJWT
	}
	h.auditService.Record(entry)
	c.Status(http.StatusNoContent)
}
