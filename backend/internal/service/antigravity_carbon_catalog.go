package service

import "strings"

func resolveAntigravityAccountModelProfile(account *Account, requested, mapped string) antigravityWrappedModelProfile {
	profile := resolveAntigravityWrappedModelProfile(requested, mapped)
	if mapped == "gemini-4-carbon" && profile.thinkingLevel == "" {
		if target, ok := resolveRequestedModelInMapping(antigravityProtocolModelMapping(account, requested), requested); ok {
			aliasProfile := resolveAntigravityWrappedModelProfile(target, mapped)
			profile.thinkingLevel = aliasProfile.thinkingLevel
		}
	}
	return profile
}

// Resolve only IDs observed for the current credentials and proxy. Some paid
// catalogs expose the base ID, others expose only concrete thinking variants.
func resolveAntigravityCarbonCatalogModel(account *Account, requested, mapped, preferred string) string {
	snapshot := account.GetUpstreamModelMetadataSnapshot()
	if snapshot == nil {
		return ""
	}
	const base = "gemini-4-carbon"
	available := make(map[string]bool)
	disabled := make(map[string]bool)
	for id := range snapshot.Models {
		id = normalizeAntigravityCompatModel(id)
		if strings.HasSuffix(id, "-disabled") {
			disabled[strings.TrimSuffix(id, "-disabled")] = true
		} else {
			available[id] = true
		}
	}
	for _, id := range snapshot.DisabledModels {
		disabled[strings.TrimSuffix(normalizeAntigravityCompatModel(id), "-disabled")] = true
	}
	if disabled[base] {
		return ""
	}
	// Explicit suffixes take precedence over a body effort or the default.
	for _, candidate := range []string{requested, mapped} {
		candidate = normalizeAntigravityCompatModel(candidate)
		if candidate != base && canonicalAntigravityCarbonModel(candidate) == base {
			if disabled[candidate] {
				return ""
			}
			if available[base] {
				return base
			}
			if available[candidate] {
				return candidate
			}
			return ""
		}
	}
	if available[base] {
		return base
	}
	for _, level := range []string{preferred, "high", "medium", "low"} {
		if level != "high" && level != "medium" && level != "low" {
			continue
		}
		id := base + "-" + level
		if available[id] && !disabled[id] {
			return id
		}
	}
	return ""
}

func antigravityCarbonCatalogMetadata(ids []string, metadata map[string]UpstreamModelMetadata) map[string]UpstreamModelMetadata {
	result := make(map[string]UpstreamModelMetadata)
	for _, id := range ids {
		if canonicalAntigravityCarbonModel(id) != "gemini-4-carbon" {
			continue
		}
		entry := metadata[id]
		entry.ID = id
		result[id] = entry
	}
	return result
}

// Projection is for display only. The persisted snapshot keeps real route IDs.
func publicAntigravityCarbonMetadata(metadata map[string]UpstreamModelMetadata) map[string]UpstreamModelMetadata {
	const base = "gemini-4-carbon"
	for _, level := range []string{"high", "medium", "low"} {
		id := base + "-" + level
		if entry, exists := metadata[id]; exists {
			if _, hasBase := metadata[base]; !hasBase {
				entry.ID = base
				metadata[base] = entry
			}
			delete(metadata, id)
		}
	}
	return metadata
}
