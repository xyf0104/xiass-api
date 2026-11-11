package handler

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/config"
	middleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type apiKeyFollowupGroupResolver interface {
	ResolveGrokVideoRequestGroup(context.Context, string, int64, int64, []int64) (int64, error)
	ResolveLiveCallGroup(context.Context, string, int64, int64) (int64, error)
}

// restoreAPIKeyGroupForFollowup is called by APIKeyGroupRouting's no-model
// branch before selected-group subscription window validation and c.Next.
// False means a response was written and the chain was aborted. Unrelated
// routes are left to their existing handlers.
func (h *GatewayHandler) restoreAPIKeyGroupForFollowup(c *gin.Context, apiKey *service.APIKey) bool {
	simpleMode := h.cfg != nil && h.cfg.RunMode == config.RunModeSimple
	return restoreAPIKeyGroupForFollowup(c, apiKey, h.openAIGatewayService, simpleMode)
}

func restoreAPIKeyGroupForFollowup(c *gin.Context, apiKey *service.APIKey, resolver apiKeyFollowupGroupResolver, simpleMode bool) bool {
	if c == nil || c.Request == nil || c.Request.Method != http.MethodGet {
		return true
	}
	var requestID, platform string
	switch c.FullPath() {
	case "/v1/videos/:request_id", "/v1/videos/:request_id/content", "/videos/:request_id", "/videos/:request_id/content":
		requestID, platform = c.Param("request_id"), service.PlatformGrok
	case "/v1/live/:call_id", "/backend-api/codex/:call_id":
		requestID, platform = c.Param("call_id"), service.PlatformOpenAI
	default:
		return true
	}
	if apiKey == nil || len(apiKey.BoundGroups()) <= 1 {
		return true
	}
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok || apiKey.ID <= 0 || apiKey.User == nil || subject.UserID <= 0 || subject.UserID != apiKey.UserID || subject.UserID != apiKey.User.ID {
		return apiKeyFollowupRoutingError(c, http.StatusUnauthorized, "Invalid API key identity")
	}
	forcedPlatform, _ := middleware.GetForcePlatformFromContext(c)
	forcedPlatform = strings.TrimSpace(forcedPlatform)
	if strings.TrimSpace(requestID) == "" || (forcedPlatform != "" && forcedPlatform != platform) {
		return apiKeyFollowupRoutingError(c, http.StatusNotFound, "Request not found")
	}
	groups := apiKey.BoundGroups()
	if len(groups) > service.MaxAPIKeyGroups {
		return apiKeyFollowupRoutingError(c, http.StatusNotFound, "Request not found")
	}
	groupIDs := make([]int64, 0, len(groups))
	for _, group := range groups {
		if group != nil && group.ID > 0 {
			groupIDs = append(groupIDs, group.ID)
		}
	}
	if resolver == nil {
		return apiKeyFollowupRoutingError(c, http.StatusServiceUnavailable, "Unable to resolve request ownership")
	}
	var groupID int64
	var err error
	if platform == service.PlatformGrok {
		groupID, err = resolver.ResolveGrokVideoRequestGroup(c.Request.Context(), requestID, subject.UserID, apiKey.ID, groupIDs)
	} else {
		groupID, err = resolver.ResolveLiveCallGroup(c.Request.Context(), requestID, subject.UserID, apiKey.ID)
	}
	if errors.Is(err, service.ErrAPIKeyFollowupNotFound) || (err == nil && groupID <= 0) {
		return apiKeyFollowupRoutingError(c, http.StatusNotFound, "Request not found")
	}
	if err != nil {
		return apiKeyFollowupRoutingError(c, http.StatusServiceUnavailable, "Unable to resolve request ownership")
	}

	// Ownership cannot grant access to a removed, disabled or hidden group.
	// Subscription groups retain the existing entitlement-based access rule.
	var selectedGroup *service.Group
	for _, group := range visibleAPIKeyGroups(boundGroupsForAPIKey(apiKey), forcedPlatform) {
		if group.ID == groupID && (group.Platform == platform || group.Platform == service.PlatformComposite) {
			selectedGroup = group
			break
		}
	}
	if selectedGroup == nil {
		return apiKeyFollowupRoutingError(c, http.StatusNotFound, "Request not found")
	}
	selected := *apiKey
	selected.Group, selected.GroupID = selectedGroup, &selectedGroup.ID
	subscription := selectedAPIKeySubscription(c, &selected)
	if selectedGroup.IsSubscriptionType() && !simpleMode {
		if subscription == nil || subscription.ID <= 0 || subscription.UserID != subject.UserID || subscription.GroupID != groupID || subscription.DeletedAt != nil || !subscription.IsActive() {
			return apiKeyFollowupRoutingError(c, http.StatusForbidden, "No active subscription found for this group")
		}
	}
	// Apply against the original group so its cached RPM override is cleared.
	selected = *apiKey
	applySelectedAPIKeyGroup(c, &selected, selectedGroup)
	c.Set(string(middleware.ContextKeyAPIKey), &selected)
	middleware.SetOpsFallbackAPIKey(c, &selected)
	if selectedGroup.Platform == service.PlatformComposite {
		c.Request = c.Request.WithContext(service.WithResolvedTargetPlatform(c.Request.Context(), platform))
	}
	return true
}

func apiKeyFollowupRoutingError(c *gin.Context, status int, message string) bool {
	middleware.OpenAIErrorWriter(c, status, message)
	c.Abort()
	return false
}
