package service

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	modelsDevRegistryURL                = "https://models.dev/api.json"
	modelsDevRegistryTTL                = 6 * time.Hour
	UpstreamModelMetadataExtraKey       = "upstream_model_metadata"
	UpstreamModelMetadataIncompleteCode = "upstream_model_metadata_incomplete"
	UpstreamModelMetadataPartialCode    = "upstream_model_metadata_partial"
	UpstreamModelMetadataTooLargeCode   = "upstream_model_metadata_too_large"
	UpstreamModelListFallbackCode       = "upstream_model_list_mapping_fallback"

	upstreamModelMetadataMaxModels          = 512
	upstreamModelMetadataMaxIDBytes         = 256
	upstreamModelMetadataMaxDisplayName     = 512
	upstreamModelMetadataMaxDescription     = 4096
	upstreamModelMetadataMaxReasoningLevels = 16
	upstreamModelMetadataMaxModalities      = 8
	upstreamModelMetadataMaxToolKeys        = 64
	upstreamModelMetadataMaxToolBytes       = 64 * 1024
	upstreamModelMetadataMaxSnapshotBytes   = 512 * 1024
)

// UpstreamModelMetadata contains only capability data that was declared by the
// selected upstream or a provider registry matched to that account's base URL.
type UpstreamModelMetadata struct {
	ID                       string                     `json:"id"`
	DisplayName              string                     `json:"display_name,omitempty"`
	Description              string                     `json:"description,omitempty"`
	Reasoning                *bool                      `json:"reasoning,omitempty"`
	DefaultReasoningLevel    string                     `json:"default_reasoning_level,omitempty"`
	SupportedReasoningLevels []string                   `json:"supported_reasoning_levels,omitempty"`
	InputModalities          []string                   `json:"input_modalities,omitempty"`
	ContextWindow            int64                      `json:"context_window,omitempty"`
	MaxContextWindow         int64                      `json:"max_context_window,omitempty"`
	MaxOutputTokens          int64                      `json:"max_output_tokens,omitempty"`
	CodexToolCapabilities    map[string]json.RawMessage `json:"codex_tool_capabilities,omitempty"`
}

type UpstreamModelMetadataSnapshot struct {
	Identity string                           `json:"identity,omitempty"`
	Source   string                           `json:"source"`
	SyncedAt string                           `json:"synced_at"`
	Models   map[string]UpstreamModelMetadata `json:"models"`
}

type upstreamModelMetadataCASRepository interface {
	UpdateUpstreamModelMetadataIfIdentityMatches(context.Context, int64, string, string, map[string]any, *int64, any) (bool, error)
}

type UpstreamModelCatalog struct {
	Models   []string                         `json:"models"`
	Metadata map[string]UpstreamModelMetadata `json:"metadata,omitempty"`
	Warnings []UpstreamModelSyncWarning       `json:"warnings,omitempty"`
}

type UpstreamModelSyncWarning struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type modelsDevProvider struct {
	ID     string                    `json:"id"`
	Name   string                    `json:"name"`
	API    string                    `json:"api"`
	Models map[string]modelsDevModel `json:"models"`
}

type modelsDevModel struct {
	ID               string                     `json:"id"`
	Name             string                     `json:"name"`
	Description      string                     `json:"description"`
	Reasoning        *bool                      `json:"reasoning"`
	ReasoningOptions []modelsDevReasoningOption `json:"reasoning_options"`
	Modalities       modelsDevModalities        `json:"modalities"`
	Limit            modelsDevLimit             `json:"limit"`
}

type modelsDevReasoningOption struct {
	Type   string `json:"type"`
	Values []any  `json:"values"`
}

type modelsDevModalities struct {
	Input  []string `json:"input"`
	Output []string `json:"output"`
}

type modelsDevLimit struct {
	Context int64 `json:"context"`
	Output  int64 `json:"output"`
}

func (a *Account) SetUpstreamModelMetadataSnapshot(snapshot UpstreamModelMetadataSnapshot) {
	if a == nil {
		return
	}
	if strings.TrimSpace(snapshot.Identity) == "" {
		snapshot.Identity = upstreamModelCapabilityIdentity(a)
	}
	if a.Extra == nil {
		a.Extra = make(map[string]any)
	}
	a.Extra[UpstreamModelMetadataExtraKey] = snapshot
}

func (a *Account) GetUpstreamModelMetadataSnapshot() *UpstreamModelMetadataSnapshot {
	if a == nil || a.Extra == nil {
		return nil
	}
	raw, ok := a.Extra[UpstreamModelMetadataExtraKey]
	if !ok || raw == nil {
		return nil
	}
	body, err := json.Marshal(raw)
	if err != nil {
		return nil
	}
	var snapshot UpstreamModelMetadataSnapshot
	if err := json.Unmarshal(body, &snapshot); err != nil || len(snapshot.Models) == 0 {
		return nil
	}
	expectedIdentity := upstreamModelCapabilityIdentity(a)
	if expectedIdentity == "" || snapshot.Identity != expectedIdentity {
		return nil
	}
	return &snapshot
}

func (a *Account) GetUpstreamModelMetadata(modelID string) (UpstreamModelMetadata, bool) {
	snapshot := a.GetUpstreamModelMetadataSnapshot()
	if snapshot == nil {
		return UpstreamModelMetadata{}, false
	}
	metadata, ok := snapshot.Models[strings.TrimSpace(modelID)]
	return metadata, ok
}

func isOpenAICompatibleCapabilitySyncAccount(account *Account) bool {
	if account == nil {
		return false
	}
	if account.Type == AccountTypeAPIKey {
		return account.IsOpenAI() || account.IsCNProvider()
	}
	if account.Type != AccountTypeOAuth || !account.IsOpenAI() || account.Credentials == nil {
		return false
	}
	// OAuth picker manifests are shared product metadata and must not be persisted
	// as account capabilities. An explicit account model mapping is the opt-in that
	// makes the fetched catalog account-specific and safe to retain.
	switch mapping := account.Credentials["model_mapping"].(type) {
	case map[string]any:
		return len(mapping) > 0
	case map[string]string:
		return len(mapping) > 0
	default:
		return false
	}
}

func upstreamModelCapabilityIdentity(account *Account) string {
	if account == nil {
		return ""
	}
	credentials, err := json.Marshal(account.Credentials)
	if err != nil {
		return ""
	}
	proxyID := int64(0)
	if account.ProxyID != nil {
		proxyID = *account.ProxyID
	}
	payload, err := json.Marshal(struct {
		Platform    string          `json:"platform"`
		AccountType string          `json:"account_type"`
		BaseURL     string          `json:"base_url"`
		ProxyID     int64           `json:"proxy_id"`
		Credentials json.RawMessage `json:"credentials"`
	}{
		Platform:    strings.TrimSpace(account.Platform),
		AccountType: strings.TrimSpace(account.Type),
		BaseURL:     canonicalUpstreamCapabilityBaseURL(account.GetOpenAIFormatBaseURL()),
		ProxyID:     proxyID,
		Credentials: credentials,
	})
	if err != nil {
		return ""
	}
	digest := sha256.Sum256(payload)
	return fmt.Sprintf("sha256:%x", digest[:])
}

func canonicalUpstreamCapabilityBaseURL(raw string) string {
	if normalized, ok := normalizedModelRegistryURL(raw); ok {
		return normalized.String()
	}
	return strings.TrimSpace(raw)
}

// SyncUpstreamModelCatalog persists one namespaced extra field. AccountRepository.UpdateExtra
// performs an atomic JSONB merge, so node ownership, proxy, fingerprint and scheduler fields
// cannot be replaced by a stale account object from this request.
func (s *AccountTestService) SyncUpstreamModelCatalog(ctx context.Context, account *Account) (*UpstreamModelCatalog, error) {
	capabilityIdentity := upstreamModelCapabilityIdentity(account)
	models, body, err := s.fetchUpstreamModelList(ctx, account)
	if !isOpenAICompatibleCapabilitySyncAccount(account) {
		if err != nil {
			return nil, err
		}
		return &UpstreamModelCatalog{Models: models}, nil
	}

	mappingFallback := false
	if err != nil {
		configuredModels := configuredUpstreamModelsForCapabilitySync(account)
		if !upstreamModelListEndpointUnsupported(err) || len(configuredModels) == 0 {
			return nil, err
		}
		models = configuredModels
		body = nil
		mappingFallback = true
	}

	catalog := &UpstreamModelCatalog{
		Models:   models,
		Metadata: make(map[string]UpstreamModelMetadata),
	}
	if mappingFallback {
		catalog.Warnings = append(catalog.Warnings, UpstreamModelSyncWarning{
			Code:    UpstreamModelListFallbackCode,
			Message: "上游 /models 接口不可用，已按账号现有的具体模型映射同步能力。",
		})
	}
	if len(body) > 0 {
		_, directMetadata, parseErr := extractUpstreamModelCatalog(body, account != nil && account.IsGrok())
		if parseErr == nil {
			catalog.Metadata = directMetadata
		}
	}

	enrichIDs := dedupeAndSortModelIDs(append(append([]string(nil), models...), configuredUpstreamModelsForCapabilitySync(account)...))
	capabilityIDs := capabilitySyncModelIDs(enrichIDs)
	sourceParts := map[string]bool{"upstream": len(body) > 0}

	// A live ID-only response must not erase a previously verified snapshot.
	// Direct fields stay authoritative; the old snapshot only fills missing data.
	if previous := account.GetUpstreamModelMetadataSnapshot(); previous != nil &&
		capabilityIdentity != "" && previous.Identity == capabilityIdentity {
		for _, modelID := range capabilityIDs {
			old, ok := previous.Models[modelID]
			if !ok || !upstreamModelMetadataIsComplete(old) {
				continue
			}
			merged, changed := mergeUpstreamModelMetadata(catalog.Metadata[modelID], old)
			if changed || upstreamModelMetadataIsComplete(merged) {
				catalog.Metadata[modelID] = merged
				sourceParts["snapshot"] = true
			}
		}
	}

	if upstreamCatalogNeedsRegistry(capabilityIDs, catalog.Metadata) {
		registryMetadata, registryErr := s.fetchModelsDevMetadata(ctx, account, capabilityIDs)
		if registryErr == nil {
			for modelID, fallback := range registryMetadata {
				merged, changed := mergeUpstreamModelMetadata(catalog.Metadata[modelID], fallback)
				if changed {
					catalog.Metadata[modelID] = merged
					sourceParts["models.dev"] = true
				}
			}
		} else {
			slog.Warn("OpenAI-compatible model capability enrichment failed",
				"account_id", account.ID,
				"platform", account.Platform,
				"error", registryErr,
			)
		}
	}

	completeMetadata := completeUpstreamModelMetadataSubset(capabilityIDs, catalog.Metadata)
	persistedCapabilities := false
	if len(completeMetadata) > 0 && account.ID > 0 && s != nil && s.accountRepo != nil {
		snapshot := UpstreamModelMetadataSnapshot{
			Identity: capabilityIdentity,
			Source:   upstreamModelCapabilitySource(sourceParts),
			SyncedAt: time.Now().UTC().Format(time.RFC3339),
			Models:   completeMetadata,
		}
		if err := validateUpstreamModelMetadataSnapshot(snapshot); err != nil {
			catalog.Warnings = append(catalog.Warnings, UpstreamModelSyncWarning{
				Code:    UpstreamModelMetadataTooLargeCode,
				Message: "上游模型能力信息超过安全存储限制，已保留原有能力快照。",
			})
		} else if casRepo, ok := s.accountRepo.(upstreamModelMetadataCASRepository); ok {
			updated, updateErr := casRepo.UpdateUpstreamModelMetadataIfIdentityMatches(
				ctx, account.ID, account.Platform, account.Type, account.Credentials, account.ProxyID, snapshot,
			)
			if updateErr != nil {
				return nil, newUpstreamModelSyncInternalError("Failed to save upstream model metadata", updateErr)
			}
			if !updated {
				return nil, newUpstreamModelSyncConfigError("Account changed during model capability sync; retry the operation", nil)
			}
			account.SetUpstreamModelMetadataSnapshot(snapshot)
			persistedCapabilities = true
		} else {
			// Keep this update map intentionally single-keyed. UpdateExtra atomically
			// merges it into accounts.extra and preserves every XIASS-owned sibling key.
			if err := s.accountRepo.UpdateExtra(ctx, account.ID, map[string]any{UpstreamModelMetadataExtraKey: snapshot}); err != nil {
				return nil, newUpstreamModelSyncInternalError("Failed to save upstream model metadata", err)
			}
			account.SetUpstreamModelMetadataSnapshot(snapshot)
			persistedCapabilities = true
		}
	}

	if upstreamCatalogNeedsRegistry(capabilityIDs, catalog.Metadata) {
		if persistedCapabilities {
			catalog.Warnings = append(catalog.Warnings, UpstreamModelSyncWarning{
				Code:    UpstreamModelMetadataPartialCode,
				Message: "已保存部分模型的能力信息；其余模型能力仍不完整，将继续使用安全的保守配置。",
			})
		} else {
			catalog.Warnings = append(catalog.Warnings, UpstreamModelSyncWarning{
				Code:    UpstreamModelMetadataIncompleteCode,
				Message: "模型 ID 已同步，但未获得可验证的完整能力信息；现有能力快照未被覆盖。",
			})
		}
	}
	return catalog, nil
}

func validateUpstreamModelMetadataSnapshot(snapshot UpstreamModelMetadataSnapshot) error {
	if snapshot.Identity == "" {
		return errors.New("model metadata identity is empty")
	}
	if len(snapshot.Models) > upstreamModelMetadataMaxModels {
		return fmt.Errorf("model metadata contains %d models", len(snapshot.Models))
	}
	for modelID, metadata := range snapshot.Models {
		if len(modelID) == 0 || len(modelID) > upstreamModelMetadataMaxIDBytes || len(metadata.ID) > upstreamModelMetadataMaxIDBytes {
			return fmt.Errorf("model metadata ID exceeds the storage limit")
		}
		if len(metadata.DisplayName) > upstreamModelMetadataMaxDisplayName || len(metadata.Description) > upstreamModelMetadataMaxDescription {
			return fmt.Errorf("model metadata text exceeds the storage limit")
		}
		if len(metadata.SupportedReasoningLevels) > upstreamModelMetadataMaxReasoningLevels || len(metadata.InputModalities) > upstreamModelMetadataMaxModalities {
			return fmt.Errorf("model metadata capability list exceeds the storage limit")
		}
		if len(metadata.CodexToolCapabilities) > upstreamModelMetadataMaxToolKeys {
			return fmt.Errorf("model metadata tool capability count exceeds the storage limit")
		}
		toolBytes := 0
		for key, value := range metadata.CodexToolCapabilities {
			if len(key) > upstreamModelMetadataMaxIDBytes {
				return fmt.Errorf("model metadata tool capability key exceeds the storage limit")
			}
			toolBytes += len(key) + len(value)
		}
		if toolBytes > upstreamModelMetadataMaxToolBytes {
			return fmt.Errorf("model metadata tool capabilities exceed the storage limit")
		}
	}
	body, err := json.Marshal(snapshot)
	if err != nil {
		return fmt.Errorf("marshal model metadata snapshot: %w", err)
	}
	if len(body) > upstreamModelMetadataMaxSnapshotBytes {
		return fmt.Errorf("model metadata snapshot exceeds %d bytes", upstreamModelMetadataMaxSnapshotBytes)
	}
	return nil
}

func upstreamModelCapabilitySource(parts map[string]bool) string {
	ordered := []string{"upstream", "models.dev", "snapshot"}
	used := make([]string, 0, len(ordered))
	for _, source := range ordered {
		if parts[source] {
			used = append(used, source)
		}
	}
	if len(used) == 0 {
		return "unknown"
	}
	return strings.Join(used, "+")
}

func upstreamModelSyncStatusCode(err error) int {
	var syncErr *UpstreamModelSyncError
	if errors.As(err, &syncErr) {
		return syncErr.StatusCode
	}
	return 0
}

func upstreamModelListEndpointUnsupported(err error) bool {
	statusCode := upstreamModelSyncStatusCode(err)
	return statusCode == http.StatusNotFound || statusCode == http.StatusMethodNotAllowed
}

func configuredUpstreamModelsForCapabilitySync(account *Account) []string {
	if account == nil {
		return nil
	}
	models := make([]string, 0)
	for _, mappedModel := range account.GetModelMapping() {
		mappedModel = strings.TrimSpace(mappedModel)
		if mappedModel == "" || strings.Contains(mappedModel, "*") {
			continue
		}
		models = append(models, mappedModel)
	}
	return dedupeAndSortModelIDs(models)
}

func capabilitySyncModelIDs(modelIDs []string) []string {
	filtered := make([]string, 0, len(modelIDs))
	for _, modelID := range modelIDs {
		modelID = strings.TrimSpace(modelID)
		if modelID == "" || IsGPTImageGenerationModel(modelID) {
			continue
		}
		filtered = append(filtered, modelID)
	}
	return dedupeAndSortModelIDs(filtered)
}

func upstreamCatalogNeedsRegistry(modelIDs []string, metadata map[string]UpstreamModelMetadata) bool {
	for _, modelID := range modelIDs {
		entry, ok := metadata[strings.TrimSpace(modelID)]
		if !ok || !upstreamModelMetadataIsComplete(entry) {
			return true
		}
	}
	return false
}

func upstreamModelMetadataIsUseful(metadata UpstreamModelMetadata) bool {
	return strings.TrimSpace(metadata.DisplayName) != "" ||
		strings.TrimSpace(metadata.Description) != "" ||
		metadata.Reasoning != nil ||
		len(metadata.SupportedReasoningLevels) > 0 ||
		len(metadata.InputModalities) > 0 ||
		len(metadata.CodexToolCapabilities) > 0 ||
		metadata.ContextWindow > 0 ||
		metadata.MaxContextWindow > 0 ||
		metadata.MaxOutputTokens > 0
}

func upstreamModelMetadataIsComplete(metadata UpstreamModelMetadata) bool {
	if metadata.Reasoning == nil || metadata.ContextWindow <= 0 || len(normalizeCodexInputModalities(metadata.InputModalities)) == 0 {
		return false
	}
	return !*metadata.Reasoning || len(normalizeReasoningLevels(metadata.SupportedReasoningLevels)) > 0
}

func completeUpstreamModelMetadataSubset(modelIDs []string, metadata map[string]UpstreamModelMetadata) map[string]UpstreamModelMetadata {
	complete := make(map[string]UpstreamModelMetadata)
	for _, modelID := range modelIDs {
		modelID = strings.TrimSpace(modelID)
		entry, ok := metadata[modelID]
		if !ok || !upstreamModelMetadataIsComplete(entry) {
			continue
		}
		entry.ID = modelID
		entry.SupportedReasoningLevels = normalizeReasoningLevels(entry.SupportedReasoningLevels)
		entry.InputModalities = normalizeCodexInputModalities(entry.InputModalities)
		complete[modelID] = entry
	}
	if len(complete) == 0 {
		return nil
	}
	return complete
}

func mergeUpstreamModelMetadata(primary, fallback UpstreamModelMetadata) (UpstreamModelMetadata, bool) {
	merged := primary
	changed := false
	if strings.TrimSpace(merged.ID) == "" && strings.TrimSpace(fallback.ID) != "" {
		merged.ID = strings.TrimSpace(fallback.ID)
		changed = true
	}
	if strings.TrimSpace(merged.DisplayName) == "" && strings.TrimSpace(fallback.DisplayName) != "" {
		merged.DisplayName = strings.TrimSpace(fallback.DisplayName)
		changed = true
	}
	if strings.TrimSpace(merged.Description) == "" && strings.TrimSpace(fallback.Description) != "" {
		merged.Description = strings.TrimSpace(fallback.Description)
		changed = true
	}
	if merged.Reasoning == nil && fallback.Reasoning != nil {
		value := *fallback.Reasoning
		merged.Reasoning = &value
		changed = true
	}
	if strings.TrimSpace(merged.DefaultReasoningLevel) == "" && strings.TrimSpace(fallback.DefaultReasoningLevel) != "" {
		merged.DefaultReasoningLevel = strings.TrimSpace(fallback.DefaultReasoningLevel)
		changed = true
	}
	if len(merged.SupportedReasoningLevels) == 0 && len(fallback.SupportedReasoningLevels) > 0 {
		merged.SupportedReasoningLevels = append([]string(nil), fallback.SupportedReasoningLevels...)
		changed = true
	}
	if len(merged.InputModalities) == 0 && len(fallback.InputModalities) > 0 {
		merged.InputModalities = append([]string(nil), fallback.InputModalities...)
		changed = true
	}
	if merged.ContextWindow <= 0 && fallback.ContextWindow > 0 {
		merged.ContextWindow = fallback.ContextWindow
		changed = true
	}
	if merged.MaxContextWindow <= 0 && fallback.MaxContextWindow > 0 {
		merged.MaxContextWindow = fallback.MaxContextWindow
		changed = true
	}
	if merged.MaxOutputTokens <= 0 && fallback.MaxOutputTokens > 0 {
		merged.MaxOutputTokens = fallback.MaxOutputTokens
		changed = true
	}
	if merged.CodexToolCapabilities == nil && len(fallback.CodexToolCapabilities) > 0 {
		merged.CodexToolCapabilities = make(map[string]json.RawMessage, len(fallback.CodexToolCapabilities))
	}
	for key, value := range fallback.CodexToolCapabilities {
		if len(merged.CodexToolCapabilities[key]) != 0 {
			continue
		}
		merged.CodexToolCapabilities[key] = append(json.RawMessage(nil), value...)
		changed = true
	}
	return merged, changed
}

func (s *AccountTestService) fetchModelsDevMetadata(ctx context.Context, account *Account, modelIDs []string) (map[string]UpstreamModelMetadata, error) {
	if s == nil || s.httpUpstream == nil || account == nil {
		return nil, errors.New("model metadata registry is not configured")
	}
	registry, err := s.fetchModelsDevRegistry(ctx, account)
	if err != nil {
		return nil, err
	}
	provider, ok := matchModelsDevProvider(registry, account.GetOpenAIFormatBaseURL())
	if !ok {
		return nil, errors.New("no model metadata provider exactly matches the account base URL")
	}

	metadata := make(map[string]UpstreamModelMetadata)
	for _, modelID := range modelIDs {
		modelID = strings.TrimSpace(modelID)
		model, found := provider.Models[modelID]
		if !found {
			for candidateID, candidate := range provider.Models {
				if strings.EqualFold(strings.TrimSpace(candidateID), modelID) || strings.EqualFold(strings.TrimSpace(candidate.ID), modelID) {
					model = candidate
					found = true
					break
				}
			}
		}
		if !found {
			continue
		}
		entry := upstreamMetadataFromModelsDevModel(modelID, model)
		if upstreamModelMetadataIsUseful(entry) {
			metadata[modelID] = entry
		}
	}
	return metadata, nil
}

func (s *AccountTestService) fetchModelsDevRegistry(ctx context.Context, account *Account) (map[string]modelsDevProvider, error) {
	now := time.Now()
	s.modelMetadataRegistryMu.Lock()
	if len(s.modelMetadataRegistry) > 0 && now.Sub(s.modelMetadataRegistryAt) < modelsDevRegistryTTL {
		cached := s.modelMetadataRegistry
		s.modelMetadataRegistryMu.Unlock()
		return cached, nil
	}
	s.modelMetadataRegistryMu.Unlock()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, modelsDevRegistryURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := s.doUpstreamModelsRequest(req, upstreamModelsProxyURL(account), account)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("model metadata registry returned HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, upstreamModelsBodyLimit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > upstreamModelsBodyLimit {
		return nil, fmt.Errorf("model metadata registry response exceeds %d bytes", upstreamModelsBodyLimit)
	}
	var registry map[string]modelsDevProvider
	if err := json.Unmarshal(body, &registry); err != nil {
		return nil, fmt.Errorf("parse model metadata registry: %w", err)
	}
	if len(registry) == 0 {
		return nil, errors.New("model metadata registry is empty")
	}

	s.modelMetadataRegistryMu.Lock()
	s.modelMetadataRegistry = registry
	s.modelMetadataRegistryAt = now
	s.modelMetadataRegistryMu.Unlock()
	return registry, nil
}

func matchModelsDevProvider(registry map[string]modelsDevProvider, accountBaseURL string) (modelsDevProvider, bool) {
	accountURL, ok := normalizedModelRegistryURL(accountBaseURL)
	if !ok {
		return modelsDevProvider{}, false
	}

	bestKey := ""
	var best modelsDevProvider
	for key, provider := range registry {
		providerURL, valid := normalizedModelRegistryURL(provider.API)
		if !valid || !sameModelRegistryProvider(accountURL, providerURL) {
			continue
		}
		if bestKey == "" || key < bestKey {
			best = provider
			bestKey = key
		}
	}
	if bestKey != "" {
		if strings.TrimSpace(best.ID) == "" {
			best.ID = bestKey
		}
		return best, true
	}

	// Some registry entries omit api. Only the exact first-party OpenAI host may
	// use the provider ID fallback; custom compatible hosts never inherit it.
	if accountURL.Hostname() == "api.openai.com" && strings.TrimRight(accountURL.Path, "/") == "" {
		provider, found := registry["openai"]
		if found && len(provider.Models) > 0 {
			if strings.TrimSpace(provider.ID) == "" {
				provider.ID = "openai"
			}
			return provider, true
		}
	}
	return modelsDevProvider{}, false
}

func normalizedModelRegistryURL(raw string) (*url.URL, bool) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return nil, false
	}
	parsed.Scheme = strings.ToLower(parsed.Scheme)
	parsed.Host = strings.ToLower(parsed.Host)
	parsed.RawQuery = ""
	parsed.Fragment = ""
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	if strings.HasSuffix(strings.ToLower(parsed.Path), "/models") {
		parsed.Path = strings.TrimRight(parsed.Path[:len(parsed.Path)-len("/models")], "/")
	}
	if strings.HasSuffix(strings.ToLower(parsed.Path), "/v1") {
		parsed.Path = strings.TrimRight(parsed.Path[:len(parsed.Path)-len("/v1")], "/")
	}
	return parsed, true
}

func sameModelRegistryProvider(accountURL, providerURL *url.URL) bool {
	if accountURL == nil || providerURL == nil || accountURL.Scheme != providerURL.Scheme || accountURL.Host != providerURL.Host {
		return false
	}
	return strings.TrimRight(accountURL.Path, "/") == strings.TrimRight(providerURL.Path, "/")
}

func upstreamMetadataFromModelsDevModel(modelID string, model modelsDevModel) UpstreamModelMetadata {
	levels := reasoningLevelsFromModelsDevOptions(model.ReasoningOptions)
	reasoning := model.Reasoning
	if reasoning == nil && len(levels) > 0 {
		value := true
		reasoning = &value
	}
	metadata := UpstreamModelMetadata{
		ID:                       strings.TrimSpace(modelID),
		DisplayName:              strings.TrimSpace(model.Name),
		Description:              strings.TrimSpace(model.Description),
		Reasoning:                reasoning,
		SupportedReasoningLevels: levels,
		InputModalities:          normalizeCodexInputModalities(model.Modalities.Input),
		ContextWindow:            model.Limit.Context,
		MaxContextWindow:         model.Limit.Context,
		MaxOutputTokens:          model.Limit.Output,
	}
	if len(levels) > 0 {
		metadata.DefaultReasoningLevel = levels[0]
	}
	return metadata
}

func reasoningLevelsFromModelsDevOptions(options []modelsDevReasoningOption) []string {
	levels := make([]string, 0)
	for _, option := range options {
		if !strings.EqualFold(strings.TrimSpace(option.Type), "effort") {
			continue
		}
		for _, value := range option.Values {
			if value == nil {
				levels = append(levels, "none")
				continue
			}
			if effort, ok := value.(string); ok {
				levels = append(levels, effort)
			}
		}
	}
	return normalizeReasoningLevels(levels)
}

type upstreamModelCapabilityEntry struct {
	upstreamModelEntry
	DisplayName              string                     `json:"display_name"`
	Description              string                     `json:"description"`
	Reasoning                *bool                      `json:"reasoning"`
	DefaultReasoningLevel    string                     `json:"default_reasoning_level"`
	SupportedReasoningLevels []json.RawMessage          `json:"supported_reasoning_levels"`
	ReasoningOptions         []modelsDevReasoningOption `json:"reasoning_options"`
	InputModalities          []string                   `json:"input_modalities"`
	Modalities               modelsDevModalities        `json:"modalities"`
	ContextWindow            int64                      `json:"context_window"`
	MaxContextWindow         int64                      `json:"max_context_window"`
	MaxOutputTokens          int64                      `json:"max_output_tokens"`
	Limit                    modelsDevLimit             `json:"limit"`
}

func extractUpstreamModelCatalog(body []byte, grok bool) ([]string, map[string]UpstreamModelMetadata, error) {
	entries, err := extractUpstreamModelRawEntries(body)
	if err != nil {
		return nil, nil, err
	}
	selectID := upstreamModelEntryID
	if grok {
		selectID = grokUpstreamModelEntryID
	}
	models := make([]string, 0, len(entries))
	metadata := make(map[string]UpstreamModelMetadata)
	for _, raw := range entries {
		var capability upstreamModelCapabilityEntry
		if err := json.Unmarshal(raw, &capability); err != nil {
			continue
		}
		modelID := strings.TrimSpace(selectID(capability.upstreamModelEntry))
		if modelID == "" {
			continue
		}
		models = append(models, modelID)
		entry := upstreamMetadataFromCapabilityEntry(modelID, capability)
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(raw, &fields); err == nil {
			entry.CodexToolCapabilities = extractCodexToolCapabilities(fields)
		}
		if upstreamModelMetadataIsUseful(entry) {
			metadata[modelID] = entry
		}
	}
	return dedupeAndSortModelIDs(models), metadata, nil
}

func extractUpstreamModelRawEntries(body []byte) ([]json.RawMessage, error) {
	var response struct {
		Data   []json.RawMessage `json:"data"`
		Models []json.RawMessage `json:"models"`
	}
	if err := json.Unmarshal(body, &response); err == nil && (response.Data != nil || response.Models != nil) {
		entries := make([]json.RawMessage, 0, len(response.Data)+len(response.Models))
		entries = append(entries, response.Data...)
		entries = append(entries, response.Models...)
		return entries, nil
	}
	var entries []json.RawMessage
	if err := json.Unmarshal(body, &entries); err != nil {
		return nil, fmt.Errorf("parse upstream model catalog: %w", err)
	}
	return entries, nil
}

func upstreamMetadataFromCapabilityEntry(modelID string, entry upstreamModelCapabilityEntry) UpstreamModelMetadata {
	levels := reasoningLevelsFromRawEntries(entry.SupportedReasoningLevels)
	if len(levels) == 0 {
		levels = reasoningLevelsFromModelsDevOptions(entry.ReasoningOptions)
	}
	reasoning := entry.Reasoning
	if reasoning == nil && len(levels) > 0 {
		value := len(levels) != 1 || levels[0] != "none"
		reasoning = &value
	}
	modalities := entry.InputModalities
	if len(modalities) == 0 {
		modalities = entry.Modalities.Input
	}
	contextWindow := entry.ContextWindow
	if contextWindow <= 0 {
		contextWindow = entry.MaxContextWindow
	}
	if contextWindow <= 0 {
		contextWindow = entry.Limit.Context
	}
	maxContextWindow := entry.MaxContextWindow
	if maxContextWindow <= 0 {
		maxContextWindow = contextWindow
	}
	if maxContextWindow > 0 && (contextWindow <= 0 || contextWindow > maxContextWindow) {
		contextWindow = maxContextWindow
	}
	maxOutputTokens := entry.MaxOutputTokens
	if maxOutputTokens <= 0 {
		maxOutputTokens = entry.Limit.Output
	}
	defaultReasoningLevel := normalizeReasoningLevel(entry.DefaultReasoningLevel)
	if defaultReasoningLevel == "" && len(levels) > 0 {
		defaultReasoningLevel = levels[0]
	}
	displayName := strings.TrimSpace(entry.DisplayName)
	if displayName == "" && strings.TrimSpace(entry.Name) != "" && strings.TrimSpace(entry.Name) != modelID {
		displayName = strings.TrimSpace(entry.Name)
	}
	return UpstreamModelMetadata{
		ID:                       modelID,
		DisplayName:              displayName,
		Description:              strings.TrimSpace(entry.Description),
		Reasoning:                reasoning,
		DefaultReasoningLevel:    defaultReasoningLevel,
		SupportedReasoningLevels: levels,
		InputModalities:          normalizeCodexInputModalities(modalities),
		ContextWindow:            contextWindow,
		MaxContextWindow:         maxContextWindow,
		MaxOutputTokens:          maxOutputTokens,
	}
}

func reasoningLevelsFromRawEntries(entries []json.RawMessage) []string {
	levels := make([]string, 0, len(entries))
	for _, raw := range entries {
		var effort string
		if err := json.Unmarshal(raw, &effort); err == nil {
			levels = append(levels, effort)
			continue
		}
		var level struct {
			Effort string `json:"effort"`
		}
		if err := json.Unmarshal(raw, &level); err == nil {
			levels = append(levels, level.Effort)
		}
	}
	return normalizeReasoningLevels(levels)
}

func normalizeReasoningLevels(levels []string) []string {
	seen := make(map[string]struct{}, len(levels))
	normalized := make([]string, 0, len(levels))
	for _, level := range levels {
		level = normalizeReasoningLevel(level)
		if level == "" {
			continue
		}
		if _, exists := seen[level]; exists {
			continue
		}
		seen[level] = struct{}{}
		normalized = append(normalized, level)
	}
	return normalized
}

func normalizeReasoningLevel(level string) string {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "off", "disabled", "none":
		return "none"
	case "extra-high", "extra_high", "xhigh":
		return "xhigh"
	case "minimal", "low", "medium", "high", "max", "ultra":
		return strings.ToLower(strings.TrimSpace(level))
	default:
		return ""
	}
}

func normalizeCodexInputModalities(modalities []string) []string {
	seen := make(map[string]struct{}, len(modalities))
	normalized := make([]string, 0, len(modalities))
	for _, modality := range modalities {
		modality = strings.ToLower(strings.TrimSpace(modality))
		if modality != "text" && modality != "image" {
			continue
		}
		if _, exists := seen[modality]; exists {
			continue
		}
		seen[modality] = struct{}{}
		normalized = append(normalized, modality)
	}
	return normalized
}
