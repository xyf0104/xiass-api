package handler

import (
	"context"
	"fmt"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

// apiKeyBoundGroupsProvider is implemented by the multi-group API key model
// in the data layer. Keeping this optional lets the gateway retain the legacy
// primary Group behavior while that model is rolled out independently.
type apiKeyBoundGroupsProvider interface {
	BoundGroups() []*service.Group
}

func boundGroupsForAPIKey(apiKey *service.APIKey) []*service.Group {
	if apiKey == nil {
		return nil
	}

	groups := make([]*service.Group, 0, 1)
	if provider, ok := any(apiKey).(apiKeyBoundGroupsProvider); ok {
		groups = append(groups, provider.BoundGroups()...)
	}
	if len(groups) == 0 && apiKey.Group != nil {
		groups = append(groups, apiKey.Group)
	}

	seen := make(map[int64]struct{}, len(groups))
	unique := make([]*service.Group, 0, len(groups))
	for _, group := range groups {
		if group == nil {
			continue
		}
		if _, ok := seen[group.ID]; ok {
			continue
		}
		seen[group.ID] = struct{}{}
		unique = append(unique, group)
	}
	return unique
}

func visibleAPIKeyGroups(groups []*service.Group, platformOverride string) []*service.Group {
	platformOverride = strings.TrimSpace(platformOverride)
	visible := make([]*service.Group, 0, len(groups))
	for _, group := range groups {
		if group == nil {
			continue
		}
		// Empty status is retained for legacy in-memory callers and tests. A
		// persisted non-empty status must be active to participate in routing.
		if strings.TrimSpace(group.Status) != "" && !group.IsActive() {
			continue
		}
		if platformOverride != "" && group.Platform != platformOverride && group.Platform != service.PlatformComposite {
			continue
		}
		visible = append(visible, group)
	}
	return visible
}

func groupForCatalogPlatform(group *service.Group, platformOverride string) *service.Group {
	if group == nil || strings.TrimSpace(platformOverride) == "" || group.Platform != service.PlatformComposite {
		return group
	}
	clone := *group
	clone.Platform = strings.TrimSpace(platformOverride)
	return &clone
}

func unionAPIKeyGroupModels(
	ctx context.Context,
	catalog service.APIKeyGroupModelCatalog,
	groups []*service.Group,
	platformOverride string,
	filter func([]string, *service.Group) []string,
) ([]string, []*service.Group, error) {
	if catalog == nil {
		return nil, nil, fmt.Errorf("API key group model catalog is not configured")
	}

	groups = visibleAPIKeyGroups(groups, platformOverride)
	seen := make(map[string]struct{})
	models := make([]string, 0)
	for _, group := range groups {
		catalogGroup := groupForCatalogPlatform(group, platformOverride)
		groupModels, err := catalog.ModelsForAPIKeyGroup(ctx, catalogGroup)
		if err != nil {
			return nil, groups, err
		}
		if filter != nil {
			groupModels = filter(groupModels, catalogGroup)
		}
		for _, modelID := range groupModels {
			modelID = strings.TrimSpace(modelID)
			if modelID == "" {
				continue
			}
			key := strings.ToLower(modelID)
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			models = append(models, modelID)
		}
	}
	return models, groups, nil
}
