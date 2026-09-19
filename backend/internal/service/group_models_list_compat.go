package service

import "strings"

// normalizeGroupModelsListConfig preserves the released XIASS request shape
// while the canonical persisted field moves to model_allowlist.
func normalizeGroupModelsListConfig(cfg GroupModelsListConfig) GroupModelsListConfig {
	out := GroupModelsListConfig{Enabled: cfg.Enabled}
	seen := make(map[string]struct{}, len(cfg.Models))
	for _, model := range cfg.Models {
		model = strings.TrimSpace(model)
		if model == "" {
			continue
		}
		key := strings.ToLower(model)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out.Models = append(out.Models, model)
	}
	if len(out.Models) == 0 {
		out.Models = nil
	}
	return out
}

func legacyModelsListFromAllowlist(cfg GroupModelAllowlist) GroupModelsListConfig {
	return GroupModelsListConfig{Enabled: cfg.Enabled, Models: append([]string(nil), cfg.Models...)}
}

func modelAllowlistFromLegacy(cfg GroupModelsListConfig) GroupModelAllowlist {
	return GroupModelAllowlist{Enabled: cfg.Enabled, Models: append([]string(nil), cfg.Models...)}
}

func (g *Group) SyncModelListCompatibility() {
	if g == nil {
		return
	}
	if g.ModelAllowlist.Enabled || len(g.ModelAllowlist.Models) > 0 {
		g.ModelsListConfig = legacyModelsListFromAllowlist(g.ModelAllowlist)
		return
	}
	if g.ModelsListConfig.Enabled || len(g.ModelsListConfig.Models) > 0 {
		g.ModelAllowlist = modelAllowlistFromLegacy(g.ModelsListConfig)
	}
}

func (g *Group) CustomModelsListEnabled() bool {
	if g == nil {
		return false
	}
	g.SyncModelListCompatibility()
	return g.ModelsListConfig.Enabled && len(g.ModelsListConfig.Models) > 0
}
