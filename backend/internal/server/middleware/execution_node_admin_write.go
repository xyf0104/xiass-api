package middleware

import (
	"net/http"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
)

// ExecutionNodeSharedWriteGuard allows shared-state-verified paired nodes to
// write and retains the primary-only policy for unpaired deployments.
// Administrator authentication remains the responsibility of the parent group.
// Execution-node pairing and routing controls are deliberately outside this
// guard because weight changes must be possible from either connected node.
func ExecutionNodeSharedWriteGuard(settingService *service.SettingService) gin.HandlerFunc {
	return func(c *gin.Context) {
		if settingService == nil || isReadOnlyHTTPMethod(c.Request.Method) || isSharedRuntimeOperation(c) || settingService.CanWriteSharedAdminState(c.Request.Context()) {
			c.Next()
			return
		}
		AbortWithError(c, http.StatusForbidden, "EXECUTION_NODE_ADMIN_READ_ONLY", "Shared administrative writes are unavailable: pairing verification or legacy node permission is required")
	}
}

// SMS claims are short-lived, transactionally owned runtime operations. They
// must work from either paired XIASS node while card-key administration and all
// other shared settings remain protected by the shared-write permission check.
func isSharedRuntimeOperation(c *gin.Context) bool {
	if c == nil || !strings.EqualFold(c.Request.Method, http.MethodPost) {
		return false
	}
	route := c.FullPath()
	switch route {
	case "/api/v1/admin/settings/sms-receiver/redeem",
		"/api/v1/admin/settings/sms-receiver/sessions/:session_id/resume",
		"/api/v1/admin/settings/sms-receiver/sessions/:session_id/check",
		"/api/v1/admin/settings/sms-receiver/sessions/:session_id/change",
		"/api/v1/admin/settings/sms-receiver/sessions/:session_id/cancel":
		return true
	}

	// FullPath is populated for normal Gin routes. Keep a strict raw-path
	// fallback for focused middleware tests and compatible embedded routers.
	route = strings.TrimSuffix(c.Request.URL.Path, "/")
	const prefix = "/api/v1/admin/settings/sms-receiver"
	if route == prefix+"/redeem" {
		return true
	}
	if !strings.HasPrefix(route, prefix+"/sessions/") {
		return false
	}
	parts := strings.Split(strings.TrimPrefix(route, prefix+"/sessions/"), "/")
	if len(parts) != 2 || strings.TrimSpace(parts[0]) == "" {
		return false
	}
	switch parts[1] {
	case "resume", "check", "change", "cancel":
		return true
	}
	return false
}

func isReadOnlyHTTPMethod(method string) bool {
	switch strings.ToUpper(strings.TrimSpace(method)) {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return true
	default:
		return false
	}
}
