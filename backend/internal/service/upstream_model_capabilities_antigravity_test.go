package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/antigravity"
	"github.com/stretchr/testify/require"
)

func TestExtractUpstreamModelCatalogKeyedModelsUseExactKeys(t *testing.T) {
	for _, grok := range []bool{false, true} {
		models, metadata, err := extractUpstreamModelCatalog([]byte(`{
			"data":[{"id":"array-model","reasoning":false}],
			"models":{
				"gemini-4-argon-medium":{
					"id":"wrong-id","slug":"wrong-slug","model":"wrong-model",
					"name":"Human display name","displayName":"Argon Medium",
					"supportsThinking":true,"thinkingBudget":-1,
					"supportsImages":true,"maxTokens":1048576,"maxOutputTokens":65536
				},
				"models/Exact-Case-ID":{"id":"wrong-prefix-id","supportsThinking":false},
				"id-only":{},
				"null-entry":null,
				" ":{"id":"must-not-be-discovered"}
			}
		}`), grok)
		require.NoError(t, err)
		require.Equal(t, []string{"array-model", "gemini-4-argon-medium", "id-only", "models/Exact-Case-ID", "null-entry"}, models)
		require.Equal(t, "Argon Medium", metadata["gemini-4-argon-medium"].DisplayName)
		require.Equal(t, []string{"medium"}, metadata["gemini-4-argon-medium"].SupportedReasoningLevels)
		require.Equal(t, "models/Exact-Case-ID", metadata["models/Exact-Case-ID"].ID)
		require.NotContains(t, metadata, "id-only")
		require.NotContains(t, metadata, "null-entry")
	}
}

func TestExtractUpstreamModelCatalogRetainsArrayFormats(t *testing.T) {
	for _, body := range []string{
		`{"data":[{"id":"provider-model","reasoning":false,"input_modalities":["text"],"context_window":64000}]}`,
		`{"models":[{"name":"models/provider-model","reasoning":false,"input_modalities":["text"],"context_window":64000}]}`,
		`[{"slug":"provider-model","reasoning":false,"input_modalities":["text"],"context_window":64000}]`,
	} {
		models, metadata, err := extractUpstreamModelCatalog([]byte(body), false)
		require.NoError(t, err)
		require.Equal(t, []string{"provider-model"}, models)
		require.True(t, upstreamModelMetadataIsComplete(metadata["provider-model"]))
	}
	for _, body := range []string{`{"models":true}`, `{"models":"invalid"}`, `{"models":123}`, `{`} {
		_, _, err := extractUpstreamModelCatalog([]byte(body), false)
		require.Error(t, err)
	}
}

func TestExtractUpstreamModelCatalogAntigravityFixedTiers(t *testing.T) {
	for _, family := range []string{"gemini-4-argon", "gemini-4-carbon", "claude-opus-5-5", "claude-sonnet-5-5"} {
		for _, tier := range []string{"low", "medium", "high"} {
			t.Run(family+"-"+tier, func(t *testing.T) {
				modelID := family + "-" + tier
				body := fmt.Sprintf(`{"models":{%q:{
					"displayName":"Declared display name","supportsImages":true,
					"supportsThinking":true,"thinkingBudget":4000,
					"maxTokens":1048576,"maxOutputTokens":65536
				}}}`, modelID)
				models, metadata, err := extractUpstreamModelCatalog([]byte(body), false)
				require.NoError(t, err)
				require.Equal(t, []string{modelID}, models)
				entry := metadata[modelID]
				require.Equal(t, "Declared display name", entry.DisplayName)
				require.Equal(t, upstreamModelCapabilityBool(true), entry.Reasoning)
				require.Equal(t, []string{tier}, entry.SupportedReasoningLevels, "fixed generation IDs must override the default budget")
				require.Equal(t, tier, entry.DefaultReasoningLevel)
				require.Equal(t, []string{"text", "image"}, entry.InputModalities)
				require.Equal(t, int64(1048576), entry.ContextWindow)
				require.Equal(t, entry.ContextWindow, entry.MaxContextWindow)
				require.Equal(t, int64(65536), entry.MaxOutputTokens)
				require.True(t, upstreamModelMetadataIsComplete(entry))
			})
		}
	}
}

func TestExtractUpstreamModelCatalogAntigravityBudgetDefaults(t *testing.T) {
	for _, tc := range []struct {
		budget string
		level  string
	}{
		{"", "high"}, {`"thinkingBudget":-1,`, "high"},
		{`"thinkingBudget":0,`, "low"}, {`"thinkingBudget":1024,`, "low"},
		{`"thinkingBudget":1025,`, "medium"}, {`"thinkingBudget":4000,`, "medium"},
		{`"thinkingBudget":8192,`, "medium"}, {`"thinkingBudget":8193,`, "high"},
	} {
		t.Run(tc.budget, func(t *testing.T) {
			_, metadata, err := extractUpstreamModelCatalog([]byte(fmt.Sprintf(`{"models":{
				"gemini-3.7-flash-tiered":{%s"supportsThinking":true,"supportsImages":false,"maxTokens":1048576}
			}}`, tc.budget)), false)
			require.NoError(t, err)
			entry := metadata["gemini-3.7-flash-tiered"]
			require.Equal(t, []string{"low", "medium", "high"}, entry.SupportedReasoningLevels)
			require.Equal(t, tc.level, entry.DefaultReasoningLevel)
			require.Equal(t, []string{"text"}, entry.InputModalities)
		})
	}
}

func TestExtractUpstreamModelCatalogAntigravityPreservesExplicitFields(t *testing.T) {
	_, metadata, err := extractUpstreamModelCatalog([]byte(`{"models":{
		"explicit-model":{
			"display_name":"Explicit name","displayName":"Fallback name",
			"reasoning":false,"supportsThinking":false,
			"input_modalities":["text"],"supportsImages":true,
			"context_window":64000,"max_context_window":32000,"maxTokens":1048576,
			"max_output_tokens":4096,"maxOutputTokens":65536
		},
		"text-only":{"supportsThinking":false,"maxTokens":200000,"maxOutputTokens":64000},
		"unknown-model":{"displayName":"Only a label"}
	}}`), false)
	require.NoError(t, err)
	entry := metadata["explicit-model"]
	require.Equal(t, "Explicit name", entry.DisplayName)
	require.Equal(t, upstreamModelCapabilityBool(false), entry.Reasoning)
	require.Empty(t, entry.SupportedReasoningLevels)
	require.Equal(t, []string{"text"}, entry.InputModalities)
	require.Equal(t, int64(32000), entry.ContextWindow)
	require.Equal(t, int64(32000), entry.MaxContextWindow)
	require.Equal(t, int64(4096), entry.MaxOutputTokens)
	require.Equal(t, []string{"text"}, metadata["text-only"].InputModalities)
	require.Equal(t, upstreamModelCapabilityBool(false), metadata["text-only"].Reasoning)
	require.Nil(t, metadata["unknown-model"].Reasoning)
	require.Empty(t, metadata["unknown-model"].InputModalities)
	require.False(t, upstreamModelMetadataIsComplete(metadata["unknown-model"]))
}

func antigravityCapabilitySyncFixture(t *testing.T, body string, repo *upstreamModelCapabilityRepo) (*AccountTestService, *Account, *httpUpstreamRecorder) {
	t.Helper()
	t.Setenv(antigravityForwardBaseURLEnv, "prod")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(server.Close)
	previous := append([]string(nil), antigravity.BaseURLs...)
	antigravity.BaseURLs = []string{server.URL, server.URL}
	t.Cleanup(func() { antigravity.BaseURLs = previous })
	registry := &httpUpstreamRecorder{err: errors.New("registry must not be queried for Antigravity OAuth")}
	svc := &AccountTestService{
		accountRepo:  repo,
		httpUpstream: registry,
		cfg:          upstreamModelSyncTestConfig(),
		antigravityGatewayService: &AntigravityGatewayService{
			tokenProvider: NewAntigravityTokenProvider(nil, nil, nil),
		},
	}
	account := &Account{
		ID: 137, Platform: PlatformAntigravity, Type: AccountTypeOAuth,
		Credentials: map[string]any{
			"access_token": "capability-test-token",
			"expires_at":   time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
			"project_id":   "capability-test-project",
			"plan_type":    "Ultra",
		},
		Extra: map[string]any{"xiass_setting": "preserve", "schedulable": false},
	}
	return svc, account, registry
}

func TestSyncUpstreamModelCatalogAntigravityPersistsPublicProjection(t *testing.T) {
	for _, internalID := range []string{"gemini-3.7-flash-tiered", "gemini-3.7-flash", "gemini-3.8-flash-tiered"} {
		t.Run(internalID, func(t *testing.T) {
			repo := &upstreamModelCapabilityRepo{}
			svc, account, registry := antigravityCapabilitySyncFixture(t, fmt.Sprintf(`{"models":{
				%q:{"displayName":"Declared Flash","supportsThinking":true,"thinkingBudget":4000,
					"supportsImages":true,"maxTokens":1048576,"maxOutputTokens":65536},
				"gemini-4-argon-medium":{"supportsThinking":true,"thinkingBudget":-1,"maxTokens":1048576},
				"claude-opus-5-5-high":{"supportsThinking":true,"thinkingBudget":1000,"maxTokens":200000}
			}}`, internalID), repo)
			beforeCredentials, err := json.Marshal(account.Credentials)
			require.NoError(t, err)
			beforeMapping, err := json.Marshal(account.GetModelMapping())
			require.NoError(t, err)
			catalog, err := svc.SyncUpstreamModelCatalog(context.Background(), account)
			require.NoError(t, err)
			require.Empty(t, catalog.Warnings, "default mapping targets must not be treated as observed capabilities")
			require.Empty(t, registry.requests)
			require.Len(t, catalog.Models, 5)
			require.Len(t, catalog.Metadata, 5)
			require.Equal(t, []string{"medium"}, catalog.Metadata["gemini-4-argon-medium"].SupportedReasoningLevels)
			require.Equal(t, []string{"high"}, catalog.Metadata["claude-opus-5-5-high"].SupportedReasoningLevels)
			require.NotContains(t, catalog.Metadata, "claude-opus-5-5")
			family, ok := antigravityGeminiFlashFamilyForModel(internalID)
			require.True(t, ok)
			require.NotContains(t, catalog.Models, family.baseModel)
			require.NotContains(t, catalog.Models, family.tieredModel)
			require.NotContains(t, catalog.Metadata, family.baseModel)
			require.NotContains(t, catalog.Metadata, family.tieredModel)
			for _, modelID := range family.publicModels {
				entry := catalog.Metadata[modelID]
				require.Equal(t, modelID, entry.ID)
				require.Equal(t, "Declared Flash", entry.DisplayName)
				profile := resolveAntigravityWrappedModelProfile(modelID, family.tieredModel)
				require.NotNil(t, profile.thinkingBudget)
				level := "high"
				switch *profile.thinkingBudget {
				case 1000:
					level = "low"
				case 4000:
					level = "medium"
				}
				require.Equal(t, level, entry.DefaultReasoningLevel)
				require.Equal(t, []string{level}, entry.SupportedReasoningLevels)
				require.Equal(t, int64(1048576), entry.ContextWindow)
				require.Equal(t, int64(65536), entry.MaxOutputTokens)
				require.Equal(t, []string{"text", "image"}, entry.InputModalities)
			}
			snapshot := account.GetUpstreamModelMetadataSnapshot()
			require.NotNil(t, snapshot)
			require.Equal(t, "upstream", snapshot.Source)
			require.Equal(t, upstreamModelCapabilityIdentity(account), snapshot.Identity)
			require.Equal(t, catalog.Metadata, snapshot.Models)
			require.Len(t, repo.updates, 1)
			require.Len(t, repo.updates[0], 1)
			require.Contains(t, repo.updates[0], UpstreamModelMetadataExtraKey)
			require.Equal(t, "preserve", account.Extra["xiass_setting"])
			require.Equal(t, false, account.Extra["schedulable"])
			afterCredentials, err := json.Marshal(account.Credentials)
			require.NoError(t, err)
			require.JSONEq(t, string(beforeCredentials), string(afterCredentials))
			afterMapping, err := json.Marshal(account.GetModelMapping())
			require.NoError(t, err)
			require.JSONEq(t, string(beforeMapping), string(afterMapping))
		})
	}
}

func TestPublicAntigravityModelMetadataPrefersDeclaredPublicEntry(t *testing.T) {
	_, metadata, err := extractUpstreamModelCatalog([]byte(`{"models":{
		"gemini-3.7-flash-tiered":{"supportsThinking":true,"maxTokens":1048576},
		"gemini-3.7-flash-high":{"displayName":"Explicit High","supportsThinking":true,"maxTokens":200000}
	}}`), false)
	require.NoError(t, err)
	public := publicAntigravityModelMetadata(metadata)
	require.Len(t, public, 3)
	require.Equal(t, metadata["gemini-3.7-flash-high"], public["gemini-3.7-flash-high"])
	require.Equal(t, []string{"medium"}, public["gemini-3.7-flash-medium"].SupportedReasoningLevels)
	require.Equal(t, "medium", public["gemini-3.7-flash-medium"].DefaultReasoningLevel)
	require.Equal(t, []string{"low"}, public["gemini-3.7-flash-low"].SupportedReasoningLevels)
	require.Equal(t, "low", public["gemini-3.7-flash-low"].DefaultReasoningLevel)
}

func TestSyncAntigravityCatalogKeepsDisabledEvidencePrivate(t *testing.T) {
	repo := &upstreamModelCapabilityRepo{}
	svc, account, _ := antigravityCapabilitySyncFixture(t, `{"models":{
		"gemini-3.8-flash-tiered":{"supportsThinking":true,"maxTokens":1048576},
		"gemini-4-carbon-disabled":{"displayName":"Carbon unavailable"}
	}}`, repo)
	catalog, err := svc.SyncUpstreamModelCatalog(context.Background(), account)
	require.NoError(t, err)
	require.NotContains(t, catalog.Models, "gemini-4-carbon-disabled")
	require.NotContains(t, catalog.Metadata, "gemini-4-carbon-disabled")
	snapshot := account.GetUpstreamModelMetadataSnapshot()
	require.NotNil(t, snapshot)
	require.Equal(t, []string{"gemini-4-carbon-disabled"}, snapshot.DisabledModels)
	require.False(t, account.IsModelSupported("gemini-4-carbon-medium"))
	account.Credentials["access_token"] = "new-identity"
	require.Nil(t, account.GetUpstreamModelMetadataSnapshot())
}

func TestSyncUpstreamModelCatalogAntigravityDoesNotInventRegistryCapabilities(t *testing.T) {
	repo := &upstreamModelCapabilityRepo{}
	svc, account, registry := antigravityCapabilitySyncFixture(t, `{"models":{
		"gemini-4-argon-high":{},"gemini-3.7-flash-tiered":{}
	}}`, repo)
	svc.modelMetadataRegistry = map[string]modelsDevProvider{
		"openai": {Models: map[string]modelsDevModel{
			"gemini-4-argon-high": {
				Reasoning:        upstreamModelCapabilityBool(true),
				ReasoningOptions: []modelsDevReasoningOption{{Type: "effort", Values: []any{"high"}}},
				Modalities:       modelsDevModalities{Input: []string{"text", "image"}},
				Limit:            modelsDevLimit{Context: 1048576},
			},
		}},
	}
	svc.modelMetadataRegistryAt = time.Now()
	catalog, err := svc.SyncUpstreamModelCatalog(context.Background(), account)
	require.NoError(t, err)
	require.Len(t, catalog.Models, 4)
	require.Empty(t, catalog.Metadata)
	require.Empty(t, repo.updates)
	require.Nil(t, account.GetUpstreamModelMetadataSnapshot())
	require.Empty(t, registry.requests)
	require.Len(t, catalog.Warnings, 1)
	require.Equal(t, UpstreamModelMetadataIncompleteCode, catalog.Warnings[0].Code)
}

func TestSyncUpstreamModelCatalogAntigravityRejectsStaleCAS(t *testing.T) {
	repo := &upstreamModelCapabilityRepo{conflict: true}
	svc, account, _ := antigravityCapabilitySyncFixture(t, `{"models":{
		"gemini-4-argon-high":{"supportsThinking":true,"maxTokens":1048576}
	}}`, repo)
	_, err := svc.SyncUpstreamModelCatalog(context.Background(), account)
	require.Error(t, err)
	var syncErr *UpstreamModelSyncError
	require.ErrorAs(t, err, &syncErr)
	require.Equal(t, UpstreamModelSyncErrorConfiguration, syncErr.Kind)
	require.Empty(t, repo.updates)
	require.Nil(t, account.GetUpstreamModelMetadataSnapshot())
	require.Equal(t, "preserve", account.Extra["xiass_setting"])
}

func TestSyncUpstreamModelCatalogAntigravityReusesOnlyMatchingSnapshot(t *testing.T) {
	for _, changedIdentity := range []bool{false, true} {
		t.Run(fmt.Sprintf("changedIdentity=%t", changedIdentity), func(t *testing.T) {
			const modelID = "gemini-4-argon-high"
			repo := &upstreamModelCapabilityRepo{}
			svc, account, registry := antigravityCapabilitySyncFixture(t, `{"models":{"gemini-4-argon-high":{}}}`, repo)
			previous := completeUpstreamModelCapability(modelID, 1048576, 65536)
			previous.DefaultReasoningLevel = "high"
			previous.SupportedReasoningLevels = []string{"high"}
			account.SetUpstreamModelMetadataSnapshot(UpstreamModelMetadataSnapshot{
				Source: "upstream", Models: map[string]UpstreamModelMetadata{modelID: previous},
			})
			if changedIdentity {
				account.Credentials["project_id"] = "different-project"
			}
			catalog, err := svc.SyncUpstreamModelCatalog(context.Background(), account)
			require.NoError(t, err)
			require.Empty(t, registry.requests)
			if changedIdentity {
				require.Empty(t, catalog.Metadata)
				require.Empty(t, repo.updates)
				require.Nil(t, account.GetUpstreamModelMetadataSnapshot())
				require.Equal(t, UpstreamModelMetadataIncompleteCode, catalog.Warnings[0].Code)
			} else {
				require.Equal(t, previous, catalog.Metadata[modelID])
				require.Len(t, repo.updates, 1)
				require.Equal(t, "upstream+snapshot", account.GetUpstreamModelMetadataSnapshot().Source)
				require.Empty(t, catalog.Warnings)
			}
		})
	}
}
