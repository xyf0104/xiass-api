package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

var codexToolCapabilityFields = []string{
	"supports_search_tool", "apply_patch_tool_type", "comp_hash", "tool_mode", "use_responses_lite",
	"multi_agent_reasoning_effort", "multi_agent_version",
}

// ApplyCodexBridgedRouteSearchCapability completes only missing fields in custom
// API key manifests. Explicit third-party manifest values remain authoritative;
// inferred fields come from the intersection of the complete persistently
// enabled candidate set. Transient runtime health must not change the advertised
// contract. OAuth/native manifests remain byte-for-byte authoritative.
func (s *OpenAIGatewayService) ApplyCodexBridgedRouteSearchCapability(
	ctx context.Context,
	manifest *CodexModelsManifest,
	selectedAccount *Account,
	groupID int64,
	ifNoneMatch string,
) error {
	if s == nil || manifest == nil || manifest.NotModified || len(manifest.Body) == 0 ||
		selectedAccount == nil || !selectedAccount.IsOpenAIApiKey() || groupID <= 0 {
		return nil
	}

	var candidates []Account
	if s.accountRepo != nil {
		listed, err := s.accountRepo.ListModelAvailabilityCandidates(ctx, &groupID, []string{PlatformOpenAI}, true)
		if err == nil {
			candidates, err = s.filterAccountCandidates(ctx, &groupID, listed)
			if err != nil {
				candidates = nil
			}
		}
	}
	updated, changed, err := applyCodexBridgedRouteSearchCapability(manifest.Body, candidates)
	if err != nil {
		return err
	}
	if !changed {
		return nil
	}

	manifest.Body = updated
	manifest.ETag = codexModelsManifestBodyETag(updated)
	if codexModelsManifestETagMatches(ifNoneMatch, manifest.ETag) {
		manifest.Body = nil
		manifest.NotModified = true
	}
	return nil
}

func applyCodexBridgedRouteSearchCapability(body []byte, candidates []Account) ([]byte, bool, error) {
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, false, fmt.Errorf("decode JSON object: %w", err)
	}
	var models []json.RawMessage
	if err := json.Unmarshal(envelope["models"], &models); err != nil {
		return nil, false, fmt.Errorf("decode top-level models array: %w", err)
	}

	changed := false
	for i, rawModel := range models {
		var model map[string]json.RawMessage
		if err := json.Unmarshal(rawModel, &model); err != nil || model == nil {
			continue
		}
		var slug string
		if err := json.Unmarshal(model["slug"], &slug); err != nil || strings.TrimSpace(slug) == "" {
			continue
		}
		slug = strings.TrimSpace(slug)

		metadata, metadataOK := persistentCodexModelMetadataIntersection(candidates, slug)
		modelChanged := false
		if metadataOK {
			var applyErr error
			modelChanged, applyErr = applyPersistedCodexModelMetadata(model, metadata)
			if applyErr != nil {
				return nil, false, fmt.Errorf("apply model %q capabilities: %w", slug, applyErr)
			}
		}

		// Preserve the existing bridge capability rule. If a verified snapshot
		// declares search support, use it only when the provider manifest omitted
		// the field and the local bridge rule did not already prove support.
		if !codexManifestFieldAuthoritative(model, "supports_search_tool") {
			supported := allPersistentModelCandidatesUseChatCompletionsBridge(candidates, slug)
			if !supported && metadataOK {
				_ = json.Unmarshal(metadata.CodexToolCapabilities["supports_search_tool"], &supported)
			}
			encoded, err := json.Marshal(supported)
			if err != nil {
				return nil, false, fmt.Errorf("encode model %q search capability: %w", slug, err)
			}
			model["supports_search_tool"] = encoded
			modelChanged = true
		}
		if !modelChanged {
			continue
		}
		updatedModel, err := json.Marshal(model)
		if err != nil {
			return nil, false, fmt.Errorf("encode model %q: %w", slug, err)
		}
		models[i] = updatedModel
		changed = true
	}
	if !changed {
		return body, false, nil
	}

	encodedModels, err := json.Marshal(models)
	if err != nil {
		return nil, false, fmt.Errorf("encode top-level models array: %w", err)
	}
	envelope["models"] = encodedModels
	updated, err := json.Marshal(envelope)
	if err != nil {
		return nil, false, fmt.Errorf("encode JSON object: %w", err)
	}
	return updated, true, nil
}

func extractCodexToolCapabilities(fields map[string]json.RawMessage) map[string]json.RawMessage {
	capabilities := make(map[string]json.RawMessage)
	for _, field := range codexToolCapabilityFields {
		value := bytes.TrimSpace(fields[field])
		if !validCodexToolCapability(field, value) {
			continue
		}
		capabilities[field] = append(json.RawMessage(nil), value...)
	}
	if len(capabilities) == 0 {
		return nil
	}
	return capabilities
}

func validCodexToolCapability(field string, value []byte) bool {
	if len(value) == 0 {
		return false
	}
	if bytes.Equal(value, []byte("null")) {
		return true
	}
	if field == "supports_search_tool" || field == "use_responses_lite" {
		return bytes.Equal(value, []byte("true")) || bytes.Equal(value, []byte("false"))
	}
	var text string
	return json.Unmarshal(value, &text) == nil
}

func codexManifestFieldAuthoritative(model map[string]json.RawMessage, field string) bool {
	current, exists := model[field]
	current = bytes.TrimSpace(current)
	return exists && len(current) > 0 && !bytes.Equal(current, []byte("null"))
}

func setCodexManifestFieldIfMissing(model map[string]json.RawMessage, field string, value any) (bool, error) {
	if codexManifestFieldAuthoritative(model, field) {
		return false, nil
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return false, err
	}
	model[field] = encoded
	return true, nil
}

func applyPersistedCodexModelMetadata(model map[string]json.RawMessage, metadata UpstreamModelMetadata) (bool, error) {
	changed := false
	set := func(field string, value any) error {
		fieldChanged, err := setCodexManifestFieldIfMissing(model, field, value)
		changed = changed || fieldChanged
		return err
	}
	if metadata.DisplayName != "" {
		if err := set("display_name", metadata.DisplayName); err != nil {
			return false, err
		}
	}
	if metadata.Description != "" {
		if err := set("description", metadata.Description); err != nil {
			return false, err
		}
	}
	if metadata.Reasoning != nil {
		levels := normalizeReasoningLevels(metadata.SupportedReasoningLevels)
		defaultLevel := normalizeReasoningLevel(metadata.DefaultReasoningLevel)
		if !*metadata.Reasoning {
			levels = []string{"none"}
			defaultLevel = "none"
		}
		if defaultLevel == "" || !stringSliceContains(levels, defaultLevel) {
			defaultLevel = levels[0]
		}
		levelDescriptors := make([]map[string]string, 0, len(levels))
		for _, level := range levels {
			levelDescriptors = append(levelDescriptors, map[string]string{
				"effort":      level,
				"description": codexReasoningLevelDescription(level),
			})
		}
		if err := set("default_reasoning_level", defaultLevel); err != nil {
			return false, err
		}
		if err := set("supported_reasoning_levels", levelDescriptors); err != nil {
			return false, err
		}
	}
	if modalities := normalizeCodexInputModalities(metadata.InputModalities); len(modalities) > 0 {
		if err := set("input_modalities", modalities); err != nil {
			return false, err
		}
	}
	if metadata.ContextWindow > 0 {
		if err := set("context_window", metadata.ContextWindow); err != nil {
			return false, err
		}
		if err := set("max_context_window", metadata.ContextWindow); err != nil {
			return false, err
		}
	}
	if metadata.MaxOutputTokens > 0 {
		if err := set("max_output_tokens", metadata.MaxOutputTokens); err != nil {
			return false, err
		}
	}
	for _, field := range codexToolCapabilityFields {
		if field == "supports_search_tool" || codexManifestFieldAuthoritative(model, field) {
			continue
		}
		value := bytes.TrimSpace(metadata.CodexToolCapabilities[field])
		if !validCodexToolCapability(field, value) {
			continue
		}
		model[field] = append(json.RawMessage(nil), value...)
		changed = true
	}
	return changed, nil
}

func persistentCodexModelMetadataIntersection(candidates []Account, modelID string) (UpstreamModelMetadata, bool) {
	matched := make([]UpstreamModelMetadata, 0)
	for i := range candidates {
		account := &candidates[i]
		if !account.IsModelSupported(modelID) {
			continue
		}
		if !account.IsOpenAIApiKey() {
			return UpstreamModelMetadata{}, false
		}
		upstreamModel := strings.TrimSpace(account.GetMappedModel(modelID))
		metadata, ok := account.GetUpstreamModelMetadata(upstreamModel)
		if !ok || !upstreamModelMetadataIsComplete(metadata) {
			return UpstreamModelMetadata{}, false
		}
		matched = append(matched, metadata)
	}
	if len(matched) == 0 {
		return UpstreamModelMetadata{}, false
	}
	return intersectPersistentCodexModelMetadata(modelID, matched)
}

func intersectPersistentCodexModelMetadata(modelID string, candidates []UpstreamModelMetadata) (UpstreamModelMetadata, bool) {
	if len(candidates) == 0 {
		return UpstreamModelMetadata{}, false
	}
	result := UpstreamModelMetadata{
		ID:                    strings.TrimSpace(modelID),
		DisplayName:           sharedMetadataString(candidates, func(item UpstreamModelMetadata) string { return item.DisplayName }),
		Description:           sharedMetadataString(candidates, func(item UpstreamModelMetadata) string { return item.Description }),
		CodexToolCapabilities: make(map[string]json.RawMessage),
	}

	reasoning := candidates[0].Reasoning
	if reasoning == nil {
		return UpstreamModelMetadata{}, false
	}
	for _, candidate := range candidates[1:] {
		if candidate.Reasoning == nil || *candidate.Reasoning != *reasoning {
			return UpstreamModelMetadata{}, false
		}
	}
	reasoningValue := *reasoning
	result.Reasoning = &reasoningValue
	if reasoningValue {
		levels := normalizeReasoningLevels(candidates[0].SupportedReasoningLevels)
		for _, candidate := range candidates[1:] {
			levels = intersectOrderedStrings(levels, normalizeReasoningLevels(candidate.SupportedReasoningLevels))
		}
		if len(levels) == 0 {
			return UpstreamModelMetadata{}, false
		}
		result.SupportedReasoningLevels = levels
		defaultLevel := normalizeReasoningLevel(candidates[0].DefaultReasoningLevel)
		for _, candidate := range candidates[1:] {
			if normalizeReasoningLevel(candidate.DefaultReasoningLevel) != defaultLevel {
				defaultLevel = ""
				break
			}
		}
		if !stringSliceContains(levels, defaultLevel) {
			defaultLevel = levels[0]
		}
		result.DefaultReasoningLevel = defaultLevel
	}

	modalities := normalizeCodexInputModalities(candidates[0].InputModalities)
	for _, candidate := range candidates[1:] {
		modalities = intersectOrderedStrings(modalities, normalizeCodexInputModalities(candidate.InputModalities))
	}
	if len(modalities) == 0 {
		return UpstreamModelMetadata{}, false
	}
	result.InputModalities = modalities

	maxOutputKnown := true
	for i, candidate := range candidates {
		if candidate.ContextWindow <= 0 {
			return UpstreamModelMetadata{}, false
		}
		if i == 0 || candidate.ContextWindow < result.ContextWindow {
			result.ContextWindow = candidate.ContextWindow
		}
		if candidate.MaxOutputTokens <= 0 {
			maxOutputKnown = false
			result.MaxOutputTokens = 0
		} else if maxOutputKnown && (i == 0 || candidate.MaxOutputTokens < result.MaxOutputTokens) {
			result.MaxOutputTokens = candidate.MaxOutputTokens
		}
	}

	for _, field := range codexToolCapabilityFields {
		value := bytes.TrimSpace(candidates[0].CodexToolCapabilities[field])
		if !validCodexToolCapability(field, value) {
			continue
		}
		shared := true
		for _, candidate := range candidates[1:] {
			other := bytes.TrimSpace(candidate.CodexToolCapabilities[field])
			if !validCodexToolCapability(field, other) || !bytes.Equal(value, other) {
				shared = false
				break
			}
		}
		if shared {
			result.CodexToolCapabilities[field] = append(json.RawMessage(nil), value...)
		}
	}
	if len(result.CodexToolCapabilities) == 0 {
		result.CodexToolCapabilities = nil
	}
	return result, true
}

func sharedMetadataString(candidates []UpstreamModelMetadata, field func(UpstreamModelMetadata) string) string {
	if len(candidates) == 0 {
		return ""
	}
	value := strings.TrimSpace(field(candidates[0]))
	if value == "" {
		return ""
	}
	for _, candidate := range candidates[1:] {
		if strings.TrimSpace(field(candidate)) != value {
			return ""
		}
	}
	return value
}

func codexReasoningLevelDescription(level string) string {
	switch level {
	case "none":
		return "Use the model without configurable reasoning"
	case "minimal":
		return "Minimal reasoning for the fastest responses"
	case "low":
		return "Fast responses with lighter reasoning"
	case "medium":
		return "Balanced reasoning for coding tasks"
	case "high":
		return "Greater reasoning depth for coding tasks"
	case "xhigh":
		return "Extra-high reasoning depth for difficult tasks"
	case "max", "ultra":
		return "Maximum reasoning depth for complex tasks"
	default:
		return "Reasoning effort supported by the upstream model"
	}
}

func intersectOrderedStrings(left, right []string) []string {
	rightSet := make(map[string]struct{}, len(right))
	for _, value := range right {
		rightSet[value] = struct{}{}
	}
	intersection := make([]string, 0, len(left))
	for _, value := range left {
		if _, ok := rightSet[value]; ok {
			intersection = append(intersection, value)
		}
	}
	return intersection
}

func stringSliceContains(values []string, target string) bool {
	for _, value := range values {
		if target != "" && value == target {
			return true
		}
	}
	return false
}

func allPersistentModelCandidatesUseChatCompletionsBridge(candidates []Account, modelID string) bool {
	modelID = strings.TrimSpace(modelID)
	if modelID == "" {
		return false
	}

	matched := 0
	for i := range candidates {
		account := &candidates[i]
		if !account.IsModelSupported(modelID) {
			continue
		}
		matched++
		if account.Platform != PlatformOpenAI || strings.TrimSpace(account.GetMappedModel(modelID)) == "" ||
			!shouldForwardOpenAIResponsesViaRawChatCompletions(account) {
			return false
		}
	}
	return matched > 0
}
