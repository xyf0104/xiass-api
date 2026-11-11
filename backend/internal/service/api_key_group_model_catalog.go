package service

import (
	"context"
	"sort"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/antigravity"
	"github.com/Wei-Shaw/sub2api/internal/pkg/claude"
	"github.com/Wei-Shaw/sub2api/internal/pkg/geminicli"
	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
	"github.com/Wei-Shaw/sub2api/internal/pkg/xai"
)

// APIKeyGroupModelCatalog is the shared source for public model discovery.
// Implementations must return models supported by the same account candidates
// used by request routing. An empty result is meaningful: callers must not
// silently replace it with platform defaults for a multi-group API key.
type APIKeyGroupModelCatalog interface {
	ModelsForAPIKeyGroup(ctx context.Context, group *Group) ([]string, error)
}

// APIKeyGroupModelPassthrough is a routing-only capability. It must never add
// invented model IDs to discovery or guess the platform of an unknown alias.
type APIKeyGroupModelPassthrough interface {
	AllowsAPIKeyGroupModelPassthrough(context.Context, *Group, string) (bool, error)
}

// APIKeyGroupGeminiModelCatalog checks the actual destination of composite
// aliases before admitting them to a native Gemini endpoint.
type APIKeyGroupGeminiModelCatalog interface {
	SupportsAPIKeyGroupGeminiModel(context.Context, *Group, string) (bool, error)
}

func (s *GatewayService) SupportsAPIKeyGroupGeminiModel(ctx context.Context, group *Group, model string) (bool, error) {
	if group == nil {
		return false, nil
	}
	platform := group.Platform
	if platform == PlatformComposite {
		if s == nil {
			return false, nil
		}
		resolver := s.compositeResolver
		if resolver == nil {
			resolver = NewCompositeRouteResolver(nil)
			resolver.SetModelOwnershipResolver(s.resolveCompositeModelOwnership)
		}
		decision, err := resolver.Resolve(ctx, group.ID, model, CompositeRouteEndpointGemini)
		if err != nil || !decision.Matched {
			return false, err
		}
		platform = decision.TargetPlatform
	}
	return platform == PlatformGemini || platform == PlatformAntigravity, nil
}

func (s *GatewayService) AllowsAPIKeyGroupModelPassthrough(ctx context.Context, group *Group, model string) (bool, error) {
	if s == nil {
		return false, nil
	}
	return allowsAPIKeyGroupModelPassthrough(ctx, s.accountRepo, group, model, s.filterAccountCandidates)
}

func (s *OpenAIGatewayService) AllowsAPIKeyGroupModelPassthrough(ctx context.Context, group *Group, model string) (bool, error) {
	if s == nil {
		return false, nil
	}
	return allowsAPIKeyGroupModelPassthrough(ctx, s.accountRepo, group, model, s.filterAccountCandidates)
}

func allowsAPIKeyGroupModelPassthrough(ctx context.Context, repo AccountRepository, group *Group, model string, filter func(context.Context, *int64, []Account) ([]Account, error)) (bool, error) {
	platform, known := DetectModelPlatform(model)
	if !known || platform != PlatformOpenAI || group == nil || repo == nil || (group.Platform != PlatformOpenAI && group.Platform != PlatformComposite) {
		return false, nil
	}
	if group.ModelAllowlistEnabled() && !group.ModelAllowlist.Allows(model) {
		return false, nil
	}
	accounts, err := repo.ListSchedulableByGroupID(ctx, group.ID)
	if err != nil {
		return false, err
	}
	accounts, err = filter(ctx, &group.ID, accounts)
	if err != nil {
		return false, err
	}
	for i := range accounts {
		if accounts[i].IsOpenAIPassthroughEnabled() {
			return true, nil
		}
	}
	return false, nil
}

// ModelsForAPIKeyGroup returns the concrete catalog currently supported by the
// group's schedulable account candidates. It intentionally does not apply
// default-model fallback; single-group HTTP handlers retain that legacy
// fallback at their existing boundary, while multi-group callers must fail
// closed when a group has no usable catalog.
func (s *GatewayService) ModelsForAPIKeyGroup(ctx context.Context, group *Group) ([]string, error) {
	if s == nil || group == nil || strings.TrimSpace(group.Platform) == "" {
		return nil, nil
	}

	if group.Platform != PlatformComposite {
		models := s.GetAvailableModels(ctx, &group.ID, group.Platform)
		if len(models) == 0 && group.Platform == PlatformOpenAI {
			// OAuth has a supported built-in catalog even without an explicit
			// mapping. Match the OpenAI gateway's discovery/first-frame selector.
			accounts, err := s.accountRepo.ListSchedulableByGroupID(ctx, group.ID)
			if err != nil {
				return nil, err
			}
			accounts, err = s.filterAccountCandidates(ctx, &group.ID, accounts)
			if err != nil {
				return nil, err
			}
			if hasOpenAIOAuthCatalogAccount(accounts) {
				return openai.DefaultModelIDs(), nil
			}
		}
		return models, nil
	}

	platforms := []string{
		PlatformAnthropic,
		PlatformGemini,
		PlatformOpenAI,
		PlatformAntigravity,
		PlatformGrok,
		PlatformKimi,
		PlatformZhipu,
		PlatformDeepseek,
		PlatformMiniMax,
		PlatformOpenCodeGo,
	}
	schedulablePlatforms := s.GetSchedulablePlatforms(ctx, &group.ID)
	seen := make(map[string]struct{})
	models := make([]string, 0)
	for _, platform := range platforms {
		platformModels := s.GetAvailableModels(ctx, &group.ID, platform)
		if len(platformModels) == 0 {
			// Keep the existing composite behavior for providers whose routing
			// path has a stable built-in catalog. Provider-specific mappings
			// remain the source for multi-protocol providers.
			if _, ok := schedulablePlatforms[platform]; ok && !IsMultiProtocolAPIKeyProvider(platform) {
				platformModels = catalogDefaultModelIDs(platform)
			}
		}
		for _, modelID := range platformModels {
			modelID = strings.TrimSpace(modelID)
			if modelID == "" {
				continue
			}
			if _, ok := seen[modelID]; ok {
				continue
			}
			seen[modelID] = struct{}{}
			models = append(models, modelID)
		}
	}
	return models, nil
}

// ModelsForAPIKeyGroup exposes the same catalog contract from the OpenAI
// service used by OpenAIGatewayHandler. Keeping this method on both gateway
// services avoids a second handler-owned model discovery implementation.
func (s *OpenAIGatewayService) ModelsForAPIKeyGroup(ctx context.Context, group *Group) ([]string, error) {
	if s == nil || s.accountRepo == nil || group == nil || strings.TrimSpace(group.Platform) == "" {
		return nil, nil
	}

	accounts, err := s.accountRepo.ListSchedulableByGroupID(ctx, group.ID)
	if err != nil {
		return nil, err
	}
	accounts, err = s.filterAccountCandidates(ctx, &group.ID, accounts)
	if err != nil {
		return nil, err
	}
	if group.Platform != PlatformComposite {
		models := modelIDsFromAvailableAccounts(accounts, group.Platform)
		if len(models) == 0 && group.Platform == PlatformOpenAI && hasOpenAIOAuthCatalogAccount(accounts) {
			return openai.DefaultModelIDs(), nil
		}
		return models, nil
	}

	seen := make(map[string]struct{})
	models := make([]string, 0)
	for _, platform := range []string{
		PlatformAnthropic,
		PlatformGemini,
		PlatformOpenAI,
		PlatformAntigravity,
		PlatformGrok,
		PlatformKimi,
		PlatformZhipu,
		PlatformDeepseek,
		PlatformMiniMax,
		PlatformOpenCodeGo,
	} {
		for _, modelID := range modelIDsFromAvailableAccounts(accounts, platform) {
			if _, ok := seen[modelID]; ok {
				continue
			}
			seen[modelID] = struct{}{}
			models = append(models, modelID)
		}
	}
	return models, nil
}

func hasOpenAIOAuthCatalogAccount(accounts []Account) bool {
	for i := range accounts {
		if accounts[i].Platform == PlatformOpenAI && accounts[i].IsOpenAIOAuth() {
			return true
		}
	}
	return false
}

// modelIDsFromAvailableAccounts is the model-mapping half of
// GatewayService.GetAvailableModels. It is shared with the OpenAI service so
// both model discovery paths retain the routing catalog semantics.
func modelIDsFromAvailableAccounts(accounts []Account, platform string) []string {
	modelSet := make(map[string]struct{})
	hasAnyMapping := false
	hasOpenAIPassthrough := false

	for i := range accounts {
		account := &accounts[i]
		if platform != "" && account.Platform != platform {
			continue
		}
		if platform == PlatformOpenAI && account.IsOpenAIPassthroughEnabled() {
			hasOpenAIPassthrough = true
			continue
		}

		mapping := account.GetModelMapping()
		if len(mapping) == 0 {
			continue
		}
		hasAnyMapping = true
		modelIDs := make([]string, 0, len(mapping))
		for modelID := range mapping {
			modelIDs = append(modelIDs, modelID)
		}
		if account.Platform == PlatformAntigravity {
			modelIDs = publicAntigravityModelIDs(modelIDs)
		}
		for _, modelID := range modelIDs {
			modelID = strings.TrimSpace(modelID)
			if account.Platform == PlatformAntigravity && !account.IsModelSupported(modelID) {
				continue
			}
			if modelID != "" {
				modelSet[modelID] = struct{}{}
			}
		}
	}

	if !hasAnyMapping {
		return nil
	}
	if hasOpenAIPassthrough {
		for _, modelID := range openai.DefaultModelIDs() {
			modelSet[modelID] = struct{}{}
		}
	}

	models := make([]string, 0, len(modelSet))
	for modelID := range modelSet {
		models = append(models, modelID)
	}
	sort.Strings(models)
	return models
}

func catalogDefaultModelIDs(platform string) []string {
	switch platform {
	case PlatformAnthropic:
		return claude.DefaultModelIDs()
	case PlatformGemini:
		models := make([]string, 0, len(geminicli.DefaultModels))
		for _, model := range geminicli.DefaultModels {
			models = append(models, model.ID)
		}
		return models
	case PlatformOpenAI:
		return openai.DefaultModelIDs()
	case PlatformAntigravity:
		models := antigravity.DefaultModels()
		ids := make([]string, 0, len(models))
		for _, model := range models {
			ids = append(ids, model.ID)
		}
		return ids
	case PlatformGrok:
		return xai.DefaultModelIDs()
	case PlatformOpenCodeGo:
		return DefaultOpenCodeGoModelIDs()
	default:
		return nil
	}
}
