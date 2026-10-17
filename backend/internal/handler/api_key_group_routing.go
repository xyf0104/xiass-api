package handler

import (
	"context"
	"strings"

	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// selectAPIKeyGroupForModel resolves a multi-group API key to the group whose
// account catalog contains the requested model. Single-group keys retain the
// legacy path. A model absent from every explicit catalog falls back to the
// primary group so OAuth/透传 accounts keep their historical behavior.
func selectAPIKeyGroupForModel(
	ctx context.Context,
	c *gin.Context,
	apiKey *service.APIKey,
	requestedModel string,
	catalog service.APIKeyGroupModelCatalog,
	imageIntent bool,
) bool {
	if apiKey == nil || len(apiKey.BoundGroups()) <= 1 || catalog == nil {
		return true
	}

	requestedModel = strings.TrimSpace(requestedModel)
	if requestedModel == "" {
		return true
	}
	groups := visibleAPIKeyGroups(apiKey.BoundGroups(), "")
	modelKey := strings.ToLower(requestedModel)
	for _, group := range groups {
		models, err := catalog.ModelsForAPIKeyGroup(ctx, group)
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
			if group.AllowImageGeneration {
				applySelectedAPIKeyGroup(c, apiKey, group)
				return true
			}
		}
	}

	return true
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
		apiKey.User.UserGroupRPMOverride = nil
	}
	middleware2.SetGroupContext(c, group)
}

func selectedAPIKeySubscription(c *gin.Context, apiKey *service.APIKey) *service.UserSubscription {
	if apiKey != nil && apiKey.GroupID != nil {
		if subscription, ok := middleware2.GetGroupSubscriptionFromContext(c, *apiKey.GroupID); ok {
			return subscription
		}
	}
	subscription, _ := middleware2.GetSubscriptionFromContext(c)
	return subscription
}
