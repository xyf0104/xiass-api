package handler

import (
	"context"
	"net/http"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/config"
	pkghttputil "github.com/Wei-Shaw/sub2api/internal/pkg/httputil"
	"github.com/Wei-Shaw/sub2api/internal/pkg/requestmodel"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

const selectedAPIKeyGroupContextKey = "xiass_selected_api_key_group"

// APIKeyGroupRouting runs after authentication, before platform dispatch and
// composite model rewriting. WebSocket models are resolved from the first frame.
func (h *GatewayHandler) APIKeyGroupRouting() gin.HandlerFunc {
	return func(c *gin.Context) {
		apiKey, ok := middleware2.GetAPIKeyFromContext(c)
		if !ok || apiKey == nil || len(apiKey.BoundGroups()) <= 1 || c.Request == nil {
			c.Next()
			return
		}
		path := c.Request.URL.Path
		if c.Request.Method == http.MethodGet && (path == "/v1/responses" || path == "/responses" || path == "/backend-api/codex/responses") {
			c.Next()
			return
		}
		model := strings.TrimSpace(c.Param("model"))
		if action := strings.TrimPrefix(c.Param("modelAction"), "/"); model == "" && action != "" {
			model, _, _ = strings.Cut(action, ":")
		}
		var body []byte
		if model == "" && (c.Request.Method == http.MethodPost || c.Request.Method == http.MethodPut || c.Request.Method == http.MethodPatch) {
			var err error
			body, err = pkghttputil.ReadRequestBodyWithPrealloc(c.Request)
			contentType := strings.ToLower(c.GetHeader("Content-Type"))
			if err == nil && (strings.Contains(contentType, "json") || (contentType == "" && strings.HasPrefix(strings.TrimSpace(string(body)), "{"))) {
				body, err = pkghttputil.NormalizeLenientJSONRequestBody(body, gatewayMaxBodySize(h.cfg))
			}
			if err != nil {
				status := http.StatusBadRequest
				if _, tooLarge := extractMaxBytesError(err); tooLarge {
					status = http.StatusRequestEntityTooLarge
				}
				apiKeyGroupRoutingError(c, status, "Failed to read request body")
				return
			}
			requestmodel.ResetRequestBody(c.Request, body)
			model = requestmodel.FromBodyForRoute(c.FullPath(), c.GetHeader("Content-Type"), body)
		}
		if model == "" {
			model = strings.TrimSpace(c.Query("model"))
		}
		if model == "" {
			if !h.restoreAPIKeyGroupForFollowup(c, apiKey) {
				return
			}
			if _, selected := c.Get(selectedAPIKeyGroupContextKey); selected {
				selectedKey, _ := middleware2.GetAPIKeyFromContext(c)
				if status, message := middleware2.ValidateSelectedAPIKeyGroupSubscription(c, selectedKey); status != 0 {
					apiKeyGroupRoutingError(c, status, message)
					return
				}
			}
			c.Next()
			return
		}

		// Auth/cache snapshots and the stored group bindings remain unchanged.
		selected := *apiKey
		apiKey = &selected
		c.Set(string(middleware2.ContextKeyAPIKey), apiKey)
		middleware2.SetOpsFallbackAPIKey(c, apiKey)
		if !routeAPIKeyGroupForModel(c, apiKey, model, h.gatewayService, service.IsExplicitImageGenerationIntent(path, model, body)) {
			return
		}
		if apiKey.Group.IsSubscriptionType() && (h.cfg == nil || h.cfg.RunMode != config.RunModeSimple) && selectedAPIKeySubscription(c, apiKey) == nil {
			apiKeyGroupRoutingError(c, http.StatusForbidden, "No active subscription found for this group")
			return
		}
		// Admission must check every parser-visible model against the selected
		// group, even if another bound group would allow a different duplicate.
		for _, candidate := range requestmodel.FromBodyCandidates(c.FullPath(), c.GetHeader("Content-Type"), body) {
			if candidate != model {
				apiKeyGroupRoutingError(c, http.StatusBadRequest, "Conflicting model values in request")
				return
			}
		}
		if status, message := middleware2.ValidateSelectedAPIKeyGroupSubscription(c, apiKey); status != 0 {
			apiKeyGroupRoutingError(c, status, message)
			return
		}
		c.Next()
	}
}

func apiKeyGroupRoutingError(c *gin.Context, status int, message string) {
	writer := middleware2.OpenAIErrorWriter
	path := c.Request.URL.Path
	if strings.Contains(path, "/v1beta/") {
		writer = middleware2.GoogleErrorWriter
	} else if strings.Contains(path, "/messages") {
		writer = middleware2.AnthropicErrorWriter
	}
	if status == http.StatusNotFound {
		service.MarkOpsClientBusinessLimited(c, service.OpsClientBusinessLimitedReasonLocalModelConfiguration)
		middleware2.MarkIngressRejected(c, middleware2.IngressRejectModelNotAllowed)
	}
	writer(c, status, message)
	c.Abort()
}

func routeAPIKeyGroupForModel(c *gin.Context, apiKey *service.APIKey, model string, catalog service.APIKeyGroupModelCatalog, imageIntent bool) bool {
	if selectAPIKeyGroupForModel(c.Request.Context(), c, apiKey, model, catalog, imageIntent) {
		return true
	}
	apiKeyGroupRoutingError(c, http.StatusNotFound, "Model is not available for the selected API key groups")
	return false
}

// selectAPIKeyGroupForModel resolves a multi-group API key to the group whose
// account catalog contains the requested model. Single-group keys retain the
// legacy path. Unknown multi-group models must not enter a primary-group adapter
// that could replace them with an unrelated platform's default model.
func selectAPIKeyGroupForModel(
	ctx context.Context,
	c *gin.Context,
	apiKey *service.APIKey,
	requestedModel string,
	catalog service.APIKeyGroupModelCatalog,
	imageIntent bool,
) bool {
	if apiKey == nil || len(apiKey.BoundGroups()) <= 1 {
		return true
	}
	if selected, ok := c.Get(selectedAPIKeyGroupContextKey); ok && apiKey.Group != nil && selected == apiKey.Group.ID {
		return true
	}
	if catalog == nil {
		return false
	}

	requestedModel = strings.TrimSpace(requestedModel)
	if requestedModel == "" {
		return true
	}
	forcedPlatform, _ := middleware2.GetForcePlatformFromContext(c)
	groups := visibleAPIKeyGroups(boundGroupsForAPIKey(apiKey), forcedPlatform)
	if strings.HasPrefix(c.Request.URL.Path, "/v1beta/") || strings.HasPrefix(c.Request.URL.Path, "/antigravity/v1beta/") {
		compatible := make([]*service.Group, 0, len(groups))
		for _, group := range groups {
			switch group.Platform {
			case service.PlatformGemini, service.PlatformAntigravity:
				compatible = append(compatible, group)
			case service.PlatformComposite:
				if nativeCatalog, ok := catalog.(service.APIKeyGroupGeminiModelCatalog); ok {
					allowed, err := nativeCatalog.SupportsAPIKeyGroupGeminiModel(ctx, groupForCatalogPlatform(group, forcedPlatform), requestedModel)
					if err == nil && allowed {
						compatible = append(compatible, group)
					}
				}
			}
		}
		groups = compatible
	}
	modelKey := strings.ToLower(requestedModel)
	for _, group := range groups {
		if group.ModelAllowlistEnabled() && !group.ModelAllowlist.Allows(requestedModel) {
			continue
		}
		models, err := catalog.ModelsForAPIKeyGroup(ctx, groupForCatalogPlatform(group, forcedPlatform))
		if err != nil {
			continue
		}
		for _, model := range models {
			if strings.ToLower(strings.TrimSpace(model)) == modelKey {
				applySelectedAPIKeyGroup(c, apiKey, group)
				return true
			}
		}
	}
	// Image-only groups often expose a native image model whose catalog is
	// intentionally hidden from text model listings. Prefer that group for an
	// explicit image request when no catalog entry matched.
	if imageIntent || service.IsGPTImageGenerationModel(requestedModel) {
		for _, group := range groups {
			if group.AllowImageGeneration && (!group.ModelAllowlistEnabled() || group.ModelAllowlist.Allows(requestedModel)) {
				applySelectedAPIKeyGroup(c, apiKey, group)
				return true
			}
		}
	}
	if passthrough, ok := catalog.(service.APIKeyGroupModelPassthrough); ok {
		for _, group := range groups {
			allowed, err := passthrough.AllowsAPIKeyGroupModelPassthrough(ctx, groupForCatalogPlatform(group, forcedPlatform), requestedModel)
			if err == nil && allowed {
				applySelectedAPIKeyGroup(c, apiKey, group)
				return true
			}
		}
	}

	return false
}

func applySelectedAPIKeyGroup(c *gin.Context, apiKey *service.APIKey, group *service.Group) {
	if apiKey == nil || group == nil {
		return
	}
	previousGroupID := int64(0)
	if apiKey.GroupID != nil {
		previousGroupID = *apiKey.GroupID
	}
	apiKey.Group = group
	apiKey.GroupID = &group.ID
	if apiKey.User != nil && previousGroupID != 0 && previousGroupID != group.ID {
		// Auth snapshots may carry the primary group's user/group RPM override.
		// Once routing selects another group, let billing resolve that group's
		// override instead of applying the primary group's value.
		user := *apiKey.User
		user.UserGroupRPMOverride = nil
		apiKey.User = &user
	}
	middleware2.SetGroupContext(c, group)
	c.Set(selectedAPIKeyGroupContextKey, group.ID)
	c.Set(string(middleware2.ContextKeySubscription), selectedAPIKeySubscription(c, apiKey))
}

func selectedAPIKeySubscription(c *gin.Context, apiKey *service.APIKey) *service.UserSubscription {
	if apiKey != nil && apiKey.Group != nil && !apiKey.Group.IsSubscriptionType() {
		return nil
	}
	if apiKey != nil && apiKey.GroupID != nil {
		if subscription, ok := middleware2.GetGroupSubscriptionFromContext(c, *apiKey.GroupID); ok {
			return subscription
		}
	}
	subscription, _ := middleware2.GetSubscriptionFromContext(c)
	if subscription != nil && apiKey != nil && apiKey.GroupID != nil && subscription.GroupID != *apiKey.GroupID {
		return nil
	}
	return subscription
}
