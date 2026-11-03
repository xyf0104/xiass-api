package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

type codexSearchCapabilityAccountRepo struct {
	AccountRepository
	accounts []Account
	err      error
}

func (r codexSearchCapabilityAccountRepo) ListModelAvailabilityCandidates(context.Context, *int64, []string, bool) ([]Account, error) {
	return append([]Account(nil), r.accounts...), r.err
}

type codexSearchCapabilityPolicy struct {
	allowed    map[int64]struct{}
	err        error
	gotGroupID int64
}

func (p *codexSearchCapabilityPolicy) FilterCandidates(_ context.Context, groupID *int64, accounts []Account) ([]Account, error) {
	if groupID != nil {
		p.gotGroupID = *groupID
	}
	if p.err != nil {
		return nil, p.err
	}
	filtered := make([]Account, 0, len(accounts))
	for i := range accounts {
		if _, ok := p.allowed[accounts[i].ID]; ok {
			filtered = append(filtered, accounts[i])
		}
	}
	return filtered, nil
}

func (p *codexSearchCapabilityPolicy) RequireCandidate(context.Context, *int64, int64) error {
	return nil
}

func newCodexSearchCapabilityAccount(id int64, bridge bool, supportedModels ...string) Account {
	mapping := make(map[string]any, len(supportedModels))
	for _, model := range supportedModels {
		mapping[model] = model
	}
	return Account{
		ID:       id,
		Platform: PlatformOpenAI,
		Type:     AccountTypeAPIKey,
		Credentials: map[string]any{
			"base_url":      "https://provider.example/v1",
			"model_mapping": mapping,
		},
		Extra: map[string]any{
			"openai_responses_supported": !bridge,
		},
	}
}

func decodeOpenAICodexCapabilityModels(t *testing.T, body []byte) []map[string]any {
	t.Helper()
	var envelope struct {
		Models []map[string]any `json:"models"`
	}
	require.NoError(t, json.Unmarshal(body, &envelope))
	return envelope.Models
}

func TestAllPersistentModelCandidatesUseChatCompletionsBridge(t *testing.T) {
	const model = "company-coding-model"
	firstBridge := newCodexSearchCapabilityAccount(1, true, model)
	secondBridge := newCodexSearchCapabilityAccount(2, true, model)
	native := newCodexSearchCapabilityAccount(3, false, model)
	unrelatedNative := newCodexSearchCapabilityAccount(4, false, "other-model")

	require.True(t, allPersistentModelCandidatesUseChatCompletionsBridge([]Account{firstBridge, secondBridge}, model))
	require.True(t, allPersistentModelCandidatesUseChatCompletionsBridge([]Account{firstBridge, unrelatedNative}, model), "account whitelist must exclude unrelated native routes")
	require.False(t, allPersistentModelCandidatesUseChatCompletionsBridge([]Account{firstBridge, native}, model), "mixed routes must fail closed")
	require.False(t, allPersistentModelCandidatesUseChatCompletionsBridge([]Account{native}, model), "native route must rely on upstream metadata")
	require.False(t, allPersistentModelCandidatesUseChatCompletionsBridge(nil, model), "empty candidate sets must fail closed")

	oauth := Account{ID: 5, Platform: PlatformOpenAI, Type: AccountTypeOAuth}
	require.False(t, allPersistentModelCandidatesUseChatCompletionsBridge([]Account{firstBridge, oauth}, model), "OAuth/native candidates must fail closed")
}

func TestApplyCodexBridgedRouteSearchCapability(t *testing.T) {
	const model = "company-coding-model"
	selected := newCodexSearchCapabilityAccount(1, true, model)
	alternate := newCodexSearchCapabilityAccount(2, true, model)
	svc := &OpenAIGatewayService{accountRepo: codexSearchCapabilityAccountRepo{accounts: []Account{selected, alternate}}}
	originalBody := []byte(`{"models":[{"slug":"company-coding-model"},{"slug":"provider-declared","supports_search_tool":true}]}`)
	manifest := &CodexModelsManifest{Body: append([]byte(nil), originalBody...)}

	require.NoError(t, svc.ApplyCodexBridgedRouteSearchCapability(context.Background(), manifest, &selected, 42, ""))
	models := decodeOpenAICodexCapabilityModels(t, manifest.Body)
	require.Equal(t, true, models[0]["supports_search_tool"])
	require.Equal(t, true, models[1]["supports_search_tool"], "explicit upstream capability must win")
	require.Equal(t, codexModelsManifestBodyETag(manifest.Body), manifest.ETag)

	second := &CodexModelsManifest{Body: append([]byte(nil), originalBody...)}
	require.NoError(t, svc.ApplyCodexBridgedRouteSearchCapability(context.Background(), second, &selected, 42, manifest.ETag))
	require.True(t, second.NotModified)
	require.Empty(t, second.Body)
	require.Equal(t, manifest.ETag, second.ETag)
}

func TestApplyCodexBridgedRouteSearchCapabilityFailsClosed(t *testing.T) {
	const model = "company-coding-model"
	bridge := newCodexSearchCapabilityAccount(1, true, model)
	native := newCodexSearchCapabilityAccount(2, false, model)
	body := []byte(`{"models":[{"slug":"company-coding-model"},{"slug":"provider-declared","supports_search_tool":true}]}`)

	for _, tc := range []struct {
		name string
		repo codexSearchCapabilityAccountRepo
	}{
		{name: "mixed protocol capabilities", repo: codexSearchCapabilityAccountRepo{accounts: []Account{bridge, native}}},
		{name: "candidate query error", repo: codexSearchCapabilityAccountRepo{err: errors.New("database unavailable")}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := &OpenAIGatewayService{accountRepo: tc.repo}
			manifest := &CodexModelsManifest{Body: append([]byte(nil), body...)}
			require.NoError(t, svc.ApplyCodexBridgedRouteSearchCapability(context.Background(), manifest, &bridge, 42, ""))
			models := decodeOpenAICodexCapabilityModels(t, manifest.Body)
			require.Equal(t, false, models[0]["supports_search_tool"])
			require.Equal(t, true, models[1]["supports_search_tool"], "explicit upstream capability must be preserved")
		})
	}

	oauth := Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth}
	manifest := &CodexModelsManifest{Body: append([]byte(nil), body...)}
	svc := &OpenAIGatewayService{accountRepo: codexSearchCapabilityAccountRepo{accounts: []Account{bridge}}}
	require.NoError(t, svc.ApplyCodexBridgedRouteSearchCapability(context.Background(), manifest, &oauth, 42, ""))
	require.Equal(t, string(body), string(manifest.Body), "OAuth manifests must remain byte-for-byte authoritative")
}

func TestApplyCodexBridgedRouteSearchCapabilityUsesRequestAccountAllowlist(t *testing.T) {
	const model = "company-coding-model"
	bridge := newCodexSearchCapabilityAccount(1, true, model)
	native := newCodexSearchCapabilityAccount(2, false, model)
	body := []byte(`{"models":[{"slug":"company-coding-model"}]}`)

	policy := &codexSearchCapabilityPolicy{allowed: map[int64]struct{}{bridge.ID: {}}}
	svc := &OpenAIGatewayService{
		accountRepo:     codexSearchCapabilityAccountRepo{accounts: []Account{bridge, native}},
		candidatePolicy: policy,
	}
	manifest := &CodexModelsManifest{Body: append([]byte(nil), body...)}
	require.NoError(t, svc.ApplyCodexBridgedRouteSearchCapability(context.Background(), manifest, &bridge, 42, ""))
	models := decodeOpenAICodexCapabilityModels(t, manifest.Body)
	require.Equal(t, true, models[0]["supports_search_tool"], "disallowed native routes must not affect this user's manifest")
	require.EqualValues(t, 42, policy.gotGroupID)

	policy.err = errors.New("allowlist unavailable")
	manifest = &CodexModelsManifest{Body: append([]byte(nil), body...)}
	require.NoError(t, svc.ApplyCodexBridgedRouteSearchCapability(context.Background(), manifest, &bridge, 42, ""))
	models = decodeOpenAICodexCapabilityModels(t, manifest.Body)
	require.Equal(t, false, models[0]["supports_search_tool"], "allowlist failures must fail closed")
}

func TestApplyCodexBridgedRouteSearchCapabilityKeepsExplicitManifestFieldsAuthoritative(t *testing.T) {
	const model = "company-coding-model"
	account := newCodexSearchCapabilityAccount(1, false, model)
	metadata := completeUpstreamModelCapability(model, 128000, 32000)
	metadata.DisplayName = "Persisted display name"
	metadata.Description = "Persisted description"
	metadata.CodexToolCapabilities = map[string]json.RawMessage{
		"supports_search_tool":  json.RawMessage(`true`),
		"apply_patch_tool_type": json.RawMessage(`"persisted"`),
	}
	account.SetUpstreamModelMetadataSnapshot(UpstreamModelMetadataSnapshot{
		Identity: upstreamModelCapabilityIdentity(&account),
		Source:   "upstream",
		Models:   map[string]UpstreamModelMetadata{model: metadata},
	})
	original := []byte(`{"models":[{"slug":"company-coding-model","display_name":"Provider name","description":"Provider description","default_reasoning_level":"ultra","supported_reasoning_levels":[{"effort":"ultra"}],"input_modalities":["audio"],"context_window":11,"max_context_window":12,"max_output_tokens":13,"supports_search_tool":false,"apply_patch_tool_type":"provider"}]}`)
	manifest := &CodexModelsManifest{Body: append([]byte(nil), original...)}
	service := &OpenAIGatewayService{accountRepo: codexSearchCapabilityAccountRepo{accounts: []Account{account}}}

	require.NoError(t, service.ApplyCodexBridgedRouteSearchCapability(context.Background(), manifest, &account, 42, ""))
	require.Equal(t, original, manifest.Body, "a complete third-party manifest must remain byte-for-byte authoritative")
	require.Empty(t, manifest.ETag)
}

func TestApplyCodexBridgedRouteSearchCapabilityUsesPersistentCandidateIntersection(t *testing.T) {
	const model = "company-coding-model"
	first := newCodexSearchCapabilityAccount(1, false, model)
	second := newCodexSearchCapabilityAccount(2, false, model)
	firstMetadata := completeUpstreamModelCapability(model, 128000, 32000)
	firstMetadata.DisplayName = "Provider A"
	firstMetadata.Description = "Shared description"
	firstMetadata.CodexToolCapabilities = map[string]json.RawMessage{
		"supports_search_tool":  json.RawMessage(`true`),
		"apply_patch_tool_type": json.RawMessage(`"custom"`),
	}
	secondMetadata := completeUpstreamModelCapability(model, 64000, 16000)
	secondMetadata.DisplayName = "Provider B"
	secondMetadata.Description = "Shared description"
	secondMetadata.DefaultReasoningLevel = "high"
	secondMetadata.SupportedReasoningLevels = []string{"low", "high"}
	secondMetadata.InputModalities = []string{"text"}
	secondMetadata.CodexToolCapabilities = map[string]json.RawMessage{
		"supports_search_tool":  json.RawMessage(`true`),
		"apply_patch_tool_type": json.RawMessage(`"custom"`),
	}
	first.SetUpstreamModelMetadataSnapshot(UpstreamModelMetadataSnapshot{Identity: upstreamModelCapabilityIdentity(&first), Source: "upstream", Models: map[string]UpstreamModelMetadata{model: firstMetadata}})
	second.SetUpstreamModelMetadataSnapshot(UpstreamModelMetadataSnapshot{Identity: upstreamModelCapabilityIdentity(&second), Source: "upstream", Models: map[string]UpstreamModelMetadata{model: secondMetadata}})
	manifest := &CodexModelsManifest{Body: []byte(`{"models":[{"slug":"company-coding-model"}]}`)}
	service := &OpenAIGatewayService{accountRepo: codexSearchCapabilityAccountRepo{accounts: []Account{first, second}}}

	require.NoError(t, service.ApplyCodexBridgedRouteSearchCapability(context.Background(), manifest, &first, 42, ""))
	models := decodeOpenAICodexCapabilityModels(t, manifest.Body)
	require.Len(t, models, 1)
	require.NotContains(t, models[0], "display_name", "provider-specific presentation must not be guessed across candidates")
	require.Equal(t, "Shared description", models[0]["description"])
	require.Equal(t, "low", models[0]["default_reasoning_level"])
	require.Equal(t, []any{map[string]any{"description": codexReasoningLevelDescription("low"), "effort": "low"}, map[string]any{"description": codexReasoningLevelDescription("high"), "effort": "high"}}, models[0]["supported_reasoning_levels"])
	require.Equal(t, []any{"text"}, models[0]["input_modalities"])
	require.EqualValues(t, 64000, models[0]["context_window"])
	require.EqualValues(t, 64000, models[0]["max_context_window"])
	require.EqualValues(t, 16000, models[0]["max_output_tokens"])
	require.Equal(t, true, models[0]["supports_search_tool"])
	require.Equal(t, "custom", models[0]["apply_patch_tool_type"])
}

func TestApplyCodexBridgedRouteSearchCapabilityRequiresCompleteSnapshotForEveryCandidate(t *testing.T) {
	const model = "company-coding-model"
	withSnapshot := newCodexSearchCapabilityAccount(1, false, model)
	withoutSnapshot := newCodexSearchCapabilityAccount(2, false, model)
	metadata := completeUpstreamModelCapability(model, 128000, 32000)
	withSnapshot.SetUpstreamModelMetadataSnapshot(UpstreamModelMetadataSnapshot{
		Identity: upstreamModelCapabilityIdentity(&withSnapshot),
		Source:   "upstream",
		Models:   map[string]UpstreamModelMetadata{model: metadata},
	})
	manifest := &CodexModelsManifest{Body: []byte(`{"models":[{"slug":"company-coding-model"}]}`)}
	service := &OpenAIGatewayService{accountRepo: codexSearchCapabilityAccountRepo{accounts: []Account{withSnapshot, withoutSnapshot}}}

	require.NoError(t, service.ApplyCodexBridgedRouteSearchCapability(context.Background(), manifest, &withSnapshot, 42, ""))
	models := decodeOpenAICodexCapabilityModels(t, manifest.Body)
	require.NotContains(t, models[0], "context_window")
	require.NotContains(t, models[0], "max_context_window")
	require.NotContains(t, models[0], "max_output_tokens")
	require.NotContains(t, models[0], "input_modalities")
	require.NotContains(t, models[0], "supported_reasoning_levels")
	require.Equal(t, false, models[0]["supports_search_tool"])
}

func TestIntersectPersistentCodexModelMetadataDoesNotRestoreUnknownMaxOutput(t *testing.T) {
	const model = "company-coding-model"
	first := completeUpstreamModelCapability(model, 128000, 32000)
	unknown := completeUpstreamModelCapability(model, 128000, 0)
	last := completeUpstreamModelCapability(model, 128000, 8000)

	metadata, ok := intersectPersistentCodexModelMetadata(model, []UpstreamModelMetadata{first, unknown, last})
	require.True(t, ok)
	require.Zero(t, metadata.MaxOutputTokens, "one unknown candidate must keep the intersection unknown")
}
