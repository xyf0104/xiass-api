package service

// MaxAPIKeyGroups is the maximum number of groups that may be bound to one API key.
const MaxAPIKeyGroups = 16

// EffectiveGroupIDs returns the API key's ordered group bindings. GroupIDs is
// preferred; the legacy primary GroupID is used when the new column was NULL.
func (k *APIKey) EffectiveGroupIDs() []int64 {
	if k == nil {
		return nil
	}
	if k.GroupIDs != nil {
		ids := make([]int64, len(k.GroupIDs))
		copy(ids, k.GroupIDs)
		return ids
	}
	if k.GroupID != nil {
		return []int64{*k.GroupID}
	}
	return nil
}

// BoundGroups returns the ordered groups bound to the key. Groups is preferred
// and the legacy primary Group is used as a one-element fallback.
func (k *APIKey) BoundGroups() []*Group {
	if k == nil {
		return nil
	}
	if k.Groups != nil {
		groups := make([]*Group, len(k.Groups))
		copy(groups, k.Groups)
		return groups
	}
	if k.Group != nil {
		return []*Group{k.Group}
	}
	return nil
}

// NormalizeAPIKeyGroupIDs removes invalid zero values and duplicate IDs while
// preserving the caller's first-seen order. The caller is responsible for
// checking group existence and authorization.
func NormalizeAPIKeyGroupIDs(ids []int64) []int64 {
	if ids == nil {
		return nil
	}
	out := make([]int64, 0, len(ids))
	seen := make(map[int64]struct{}, len(ids))
	for _, id := range ids {
		if id <= 0 {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}

func apiKeyPrimaryGroupID(ids []int64) *int64 {
	if len(ids) == 0 {
		return nil
	}
	id := ids[0]
	return &id
}

func apiKeyAuthGroupSnapshotFromService(group *Group) *APIKeyAuthGroupSnapshot {
	if group == nil {
		return nil
	}
	return &APIKeyAuthGroupSnapshot{
		LongContextPricingEnabled:       group.LongContextPricingEnabled,
		ModelPricing:                    cloneAuthGroupModelPricing(group.ModelPricing),
		ID:                              group.ID,
		Name:                            group.Name,
		Platform:                        group.Platform,
		IsExclusive:                     group.IsExclusive,
		Status:                          group.Status,
		SubscriptionType:                group.SubscriptionType,
		RateMultiplier:                  group.RateMultiplier,
		DailyLimitUSD:                   group.DailyLimitUSD,
		WeeklyLimitUSD:                  group.WeeklyLimitUSD,
		MonthlyLimitUSD:                 group.MonthlyLimitUSD,
		AllowImageGeneration:            group.AllowImageGeneration,
		AllowBatchImageGeneration:       group.AllowBatchImageGeneration,
		ImageRateIndependent:            group.ImageRateIndependent,
		ImageRateMultiplier:             group.ImageRateMultiplier,
		ImagePrice1K:                    group.ImagePrice1K,
		ImagePrice2K:                    group.ImagePrice2K,
		ImagePrice4K:                    group.ImagePrice4K,
		VideoRateIndependent:            group.VideoRateIndependent,
		VideoRateMultiplier:             group.VideoRateMultiplier,
		VideoPrice480P:                  group.VideoPrice480P,
		VideoPrice720P:                  group.VideoPrice720P,
		VideoPrice1080P:                 group.VideoPrice1080P,
		VideoModelPrices:                group.VideoModelPrices,
		WebSearchPricePerCall:           group.WebSearchPricePerCall,
		SearchPricePer1k:                group.SearchPricePer1k,
		AudioRealtimePricePerMin:        group.AudioRealtimePricePerMin,
		AudioTTSPricePerMillionChars:    group.AudioTTSPricePerMillionChars,
		AudioSTTPricePerHour:            group.AudioSTTPricePerHour,
		ClaudeCodeOnly:                  group.ClaudeCodeOnly,
		FallbackGroupID:                 group.FallbackGroupID,
		FallbackGroupIDOnInvalidRequest: group.FallbackGroupIDOnInvalidRequest,
		ModelRouting:                    group.ModelRouting,
		ModelRoutingPools:               group.ModelRoutingPools,
		ModelRoutingEnabled:             group.ModelRoutingEnabled,
		MCPXMLInject:                    group.MCPXMLInject,
		SupportedModelScopes:            group.SupportedModelScopes,
		AllowMessagesDispatch:           group.AllowMessagesDispatch,
		AllowLive:                       group.AllowLive,
		ForceOpenAIFast:                 group.ForceOpenAIFast,
		FreeOpenAIFast:                  group.FreeOpenAIFast,
		RequireOAuthOnly:                group.RequireOAuthOnly,
		RequirePrivacySet:               group.RequirePrivacySet,
		DefaultMappedModel:              group.DefaultMappedModel,
		MessagesDispatchModelConfig:     group.MessagesDispatchModelConfig,
		ModelAllowlist:                  group.ModelAllowlist,
		ModelsListConfig:                group.ModelsListConfig,
		CodexModelsManifestConfig:       group.CodexModelsManifestConfig,
		RPMLimit:                        group.RPMLimit,
		CostRatio:                       group.CostRatio,
		MaxReasoningEffort:              group.MaxReasoningEffort,
		MaxReasoningEffortOverLimit:     group.MaxReasoningEffortOverLimit,
		ReasoningEffortMappings:         group.ReasoningEffortMappings,
		PeakRateEnabled:                 group.PeakRateEnabled,
		PeakStart:                       group.PeakStart,
		PeakEnd:                         group.PeakEnd,
		PeakRateMultiplier:              group.PeakRateMultiplier,
		ProfitControlEnabled:            group.ProfitControlEnabled,
		ProfitMinMargin:                 group.ProfitMinMargin,
		ProfitSafetyBuffer:              group.ProfitSafetyBuffer,
	}
}

func apiKeyAuthGroupFromSnapshot(snapshot *APIKeyAuthGroupSnapshot) *Group {
	if snapshot == nil {
		return nil
	}
	return &Group{
		LongContextPricingEnabled:       snapshot.LongContextPricingEnabled,
		ModelPricing:                    cloneAuthGroupModelPricing(snapshot.ModelPricing),
		ID:                              snapshot.ID,
		Name:                            snapshot.Name,
		Platform:                        snapshot.Platform,
		IsExclusive:                     snapshot.IsExclusive,
		Status:                          snapshot.Status,
		Hydrated:                        true,
		SubscriptionType:                snapshot.SubscriptionType,
		RateMultiplier:                  snapshot.RateMultiplier,
		DailyLimitUSD:                   snapshot.DailyLimitUSD,
		WeeklyLimitUSD:                  snapshot.WeeklyLimitUSD,
		MonthlyLimitUSD:                 snapshot.MonthlyLimitUSD,
		AllowImageGeneration:            snapshot.AllowImageGeneration,
		AllowBatchImageGeneration:       snapshot.AllowBatchImageGeneration,
		ImageRateIndependent:            snapshot.ImageRateIndependent,
		ImageRateMultiplier:             snapshot.ImageRateMultiplier,
		ImagePrice1K:                    snapshot.ImagePrice1K,
		ImagePrice2K:                    snapshot.ImagePrice2K,
		ImagePrice4K:                    snapshot.ImagePrice4K,
		VideoRateIndependent:            snapshot.VideoRateIndependent,
		VideoRateMultiplier:             snapshot.VideoRateMultiplier,
		VideoPrice480P:                  snapshot.VideoPrice480P,
		VideoPrice720P:                  snapshot.VideoPrice720P,
		VideoPrice1080P:                 snapshot.VideoPrice1080P,
		VideoModelPrices:                snapshot.VideoModelPrices,
		WebSearchPricePerCall:           snapshot.WebSearchPricePerCall,
		SearchPricePer1k:                snapshot.SearchPricePer1k,
		AudioRealtimePricePerMin:        snapshot.AudioRealtimePricePerMin,
		AudioTTSPricePerMillionChars:    snapshot.AudioTTSPricePerMillionChars,
		AudioSTTPricePerHour:            snapshot.AudioSTTPricePerHour,
		ClaudeCodeOnly:                  snapshot.ClaudeCodeOnly,
		FallbackGroupID:                 snapshot.FallbackGroupID,
		FallbackGroupIDOnInvalidRequest: snapshot.FallbackGroupIDOnInvalidRequest,
		ModelRouting:                    snapshot.ModelRouting,
		ModelRoutingPools:               snapshot.ModelRoutingPools,
		ModelRoutingEnabled:             snapshot.ModelRoutingEnabled,
		MCPXMLInject:                    snapshot.MCPXMLInject,
		SupportedModelScopes:            snapshot.SupportedModelScopes,
		AllowMessagesDispatch:           snapshot.AllowMessagesDispatch,
		AllowLive:                       snapshot.AllowLive,
		ForceOpenAIFast:                 snapshot.ForceOpenAIFast,
		FreeOpenAIFast:                  snapshot.FreeOpenAIFast,
		RequireOAuthOnly:                snapshot.RequireOAuthOnly,
		RequirePrivacySet:               snapshot.RequirePrivacySet,
		DefaultMappedModel:              snapshot.DefaultMappedModel,
		MessagesDispatchModelConfig:     snapshot.MessagesDispatchModelConfig,
		ModelAllowlist:                  snapshot.ModelAllowlist,
		ModelsListConfig:                snapshot.ModelsListConfig,
		CodexModelsManifestConfig:       snapshot.CodexModelsManifestConfig,
		RPMLimit:                        snapshot.RPMLimit,
		CostRatio:                       snapshot.CostRatio,
		MaxReasoningEffort:              snapshot.MaxReasoningEffort,
		MaxReasoningEffortOverLimit:     snapshot.MaxReasoningEffortOverLimit,
		ReasoningEffortMappings:         snapshot.ReasoningEffortMappings,
		PeakRateEnabled:                 snapshot.PeakRateEnabled,
		PeakStart:                       snapshot.PeakStart,
		PeakEnd:                         snapshot.PeakEnd,
		PeakRateMultiplier:              snapshot.PeakRateMultiplier,
		ProfitControlEnabled:            snapshot.ProfitControlEnabled,
		ProfitMinMargin:                 snapshot.ProfitMinMargin,
		ProfitSafetyBuffer:              snapshot.ProfitSafetyBuffer,
	}
}
