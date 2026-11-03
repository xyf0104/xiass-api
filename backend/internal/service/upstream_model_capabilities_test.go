package service

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type upstreamModelCapabilityRepo struct {
	AccountRepository
	accountIDs []int64
	updates    []map[string]any
	err        error
	conflict   bool
}

func upstreamModelCapabilityBool(value bool) *bool { return &value }

func (r *upstreamModelCapabilityRepo) UpdateExtra(_ context.Context, accountID int64, updates map[string]any) error {
	r.accountIDs = append(r.accountIDs, accountID)
	copyOfUpdates := make(map[string]any, len(updates))
	for key, value := range updates {
		copyOfUpdates[key] = value
	}
	r.updates = append(r.updates, copyOfUpdates)
	return r.err
}

func (r *upstreamModelCapabilityRepo) UpdateUpstreamModelMetadataIfIdentityMatches(
	_ context.Context,
	accountID int64,
	_ string,
	_ string,
	_ map[string]any,
	_ *int64,
	snapshot any,
) (bool, error) {
	if r.err != nil {
		return false, r.err
	}
	if r.conflict {
		return false, nil
	}
	return true, r.UpdateExtra(context.Background(), accountID, map[string]any{UpstreamModelMetadataExtraKey: snapshot})
}

func upstreamModelCapabilityAccount(baseURL string, mapping map[string]any) *Account {
	credentials := map[string]any{
		"api_key":  "upstream-key",
		"base_url": baseURL,
	}
	if mapping != nil {
		credentials["model_mapping"] = mapping
	}
	return &Account{
		ID:          73,
		Platform:    PlatformOpenAI,
		Type:        AccountTypeAPIKey,
		Credentials: credentials,
		Extra: map[string]any{
			"codex_fingerprint_seed": "keep-fingerprint-seed",
			"proxy_url":              "socks5://proxy.example:1080",
			"schedulable":            false,
			"custom_xiass_field":     map[string]any{"keep": true},
		},
	}
}

func completeUpstreamModelCapability(modelID string, contextWindow, maxOutputTokens int64) UpstreamModelMetadata {
	return UpstreamModelMetadata{
		ID:                       modelID,
		DisplayName:              "Provider model",
		Description:              "Provider-declared capabilities",
		Reasoning:                upstreamModelCapabilityBool(true),
		DefaultReasoningLevel:    "medium",
		SupportedReasoningLevels: []string{"low", "medium", "high"},
		InputModalities:          []string{"text", "image"},
		ContextWindow:            contextWindow,
		MaxOutputTokens:          maxOutputTokens,
	}
}

func capabilitySyncResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func TestSyncUpstreamModelCatalogPersistsOnlyCapabilitySnapshot(t *testing.T) {
	repo := &upstreamModelCapabilityRepo{}
	upstream := &httpUpstreamRecorder{resp: capabilitySyncResponse(http.StatusOK, `{
		"data":[{
			"id":"provider-coder",
			"display_name":"Provider Coder",
			"description":"Declared by provider",
			"reasoning":true,
			"default_reasoning_level":"medium",
			"supported_reasoning_levels":["low","medium","high"],
			"input_modalities":["text","image"],
			"context_window":131072,
			"max_output_tokens":16384,
			"supports_search_tool":true
		}]
	}`)}
	service := &AccountTestService{
		accountRepo:  repo,
		httpUpstream: upstream,
		cfg:          upstreamModelSyncTestConfig(),
	}
	account := upstreamModelCapabilityAccount("https://provider.example/v1", nil)

	catalog, err := service.SyncUpstreamModelCatalog(context.Background(), account)
	require.NoError(t, err)
	require.Equal(t, []string{"provider-coder"}, catalog.Models)
	require.Empty(t, catalog.Warnings)
	require.Len(t, repo.updates, 1)
	require.Equal(t, []int64{account.ID}, repo.accountIDs)
	require.Len(t, repo.updates[0], 1, "atomic update must not submit a stale copy of accounts.extra")
	require.Contains(t, repo.updates[0], UpstreamModelMetadataExtraKey)

	require.Equal(t, "keep-fingerprint-seed", account.Extra["codex_fingerprint_seed"])
	require.Equal(t, "socks5://proxy.example:1080", account.Extra["proxy_url"])
	require.Equal(t, false, account.Extra["schedulable"])
	require.Equal(t, map[string]any{"keep": true}, account.Extra["custom_xiass_field"])

	snapshot := account.GetUpstreamModelMetadataSnapshot()
	require.NotNil(t, snapshot)
	require.Equal(t, int64(131072), snapshot.Models["provider-coder"].ContextWindow)
	require.JSONEq(t, `true`, string(snapshot.Models["provider-coder"].CodexToolCapabilities["supports_search_tool"]))
}

func TestSyncUpstreamModelCatalogAllowsOnlyUnsupportedEndpointMappingFallback(t *testing.T) {
	for _, status := range []int{http.StatusNotFound, http.StatusMethodNotAllowed} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			repo := &upstreamModelCapabilityRepo{}
			upstream := &httpUpstreamRecorder{resp: capabilitySyncResponse(status, `{}`)}
			service := &AccountTestService{
				accountRepo:  repo,
				httpUpstream: upstream,
				cfg:          upstreamModelSyncTestConfig(),
				modelMetadataRegistry: map[string]modelsDevProvider{
					"provider": {
						ID:  "provider",
						API: "https://provider.example/v1",
						Models: map[string]modelsDevModel{
							"provider-coder": {
								ID:        "provider-coder",
								Reasoning: upstreamModelCapabilityBool(true),
								ReasoningOptions: []modelsDevReasoningOption{{
									Type:   "effort",
									Values: []any{"low", "medium", "high"},
								}},
								Modalities: modelsDevModalities{Input: []string{"text"}},
								Limit:      modelsDevLimit{Context: 65536, Output: 8192},
							},
						},
					},
				},
				modelMetadataRegistryAt: time.Now(),
			}
			account := upstreamModelCapabilityAccount("https://provider.example/v1", map[string]any{
				"client-coder": "provider-coder",
			})

			catalog, err := service.SyncUpstreamModelCatalog(context.Background(), account)
			require.NoError(t, err)
			require.Equal(t, []string{"provider-coder"}, catalog.Models)
			require.NotEmpty(t, catalog.Warnings)
			require.Equal(t, UpstreamModelListFallbackCode, catalog.Warnings[0].Code)
			require.Len(t, repo.updates, 1)
			require.Len(t, repo.updates[0], 1)
		})
	}
}

func TestSyncUpstreamModelCatalogRejectsFallbackWithoutConcreteMapping(t *testing.T) {
	for _, mapping := range []map[string]any{
		nil,
		{"client-model": "provider-*"},
		{"client-model": ""},
	} {
		repo := &upstreamModelCapabilityRepo{}
		service := &AccountTestService{
			accountRepo: repo,
			httpUpstream: &httpUpstreamRecorder{
				resp: capabilitySyncResponse(http.StatusNotFound, `{}`),
			},
			cfg: upstreamModelSyncTestConfig(),
		}

		_, err := service.SyncUpstreamModelCatalog(context.Background(), upstreamModelCapabilityAccount("https://provider.example/v1", mapping))
		require.Error(t, err)
		var syncErr *UpstreamModelSyncError
		require.ErrorAs(t, err, &syncErr)
		require.Equal(t, http.StatusNotFound, syncErr.StatusCode)
		require.Empty(t, repo.updates)
	}
}

func TestSyncUpstreamModelCatalogDoesNotFallbackOnUpstreamFailures(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		body       string
		requestErr error
	}{
		{name: "authentication", statusCode: http.StatusUnauthorized, body: `{}`},
		{name: "authorization", statusCode: http.StatusForbidden, body: `{}`},
		{name: "rate limit", statusCode: http.StatusTooManyRequests, body: `{}`},
		{name: "server error", statusCode: http.StatusBadGateway, body: `{}`},
		{name: "invalid response", statusCode: http.StatusOK, body: `{`},
		{name: "network error", requestErr: errors.New("dial failed")},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repo := &upstreamModelCapabilityRepo{}
			upstream := &httpUpstreamRecorder{err: test.requestErr}
			if test.requestErr == nil {
				upstream.resp = capabilitySyncResponse(test.statusCode, test.body)
			}
			service := &AccountTestService{
				accountRepo:  repo,
				httpUpstream: upstream,
				cfg:          upstreamModelSyncTestConfig(),
			}
			account := upstreamModelCapabilityAccount("https://provider.example/v1", map[string]any{
				"client-coder": "provider-coder",
			})

			_, err := service.SyncUpstreamModelCatalog(context.Background(), account)
			require.Error(t, err)
			var syncErr *UpstreamModelSyncError
			require.ErrorAs(t, err, &syncErr)
			require.Equal(t, UpstreamModelSyncErrorUpstream, syncErr.Kind)
			require.Empty(t, repo.updates)
		})
	}
}

func TestSyncUpstreamModelCatalogDoesNotEnrichAcrossProviders(t *testing.T) {
	repo := &upstreamModelCapabilityRepo{}
	service := &AccountTestService{
		accountRepo:  repo,
		httpUpstream: &httpUpstreamRecorder{resp: capabilitySyncResponse(http.StatusOK, `{"data":[{"id":"shared-name"}]}`)},
		cfg:          upstreamModelSyncTestConfig(),
		modelMetadataRegistry: map[string]modelsDevProvider{
			"wrong-provider": {
				ID:  "wrong-provider",
				API: "https://wrong.example/v1",
				Models: map[string]modelsDevModel{
					"shared-name": {
						ID:               "shared-name",
						Reasoning:        upstreamModelCapabilityBool(true),
						ReasoningOptions: []modelsDevReasoningOption{{Type: "effort", Values: []any{"medium"}}},
						Modalities:       modelsDevModalities{Input: []string{"text"}},
						Limit:            modelsDevLimit{Context: 128000, Output: 16000},
					},
				},
			},
		},
		modelMetadataRegistryAt: time.Now(),
	}

	catalog, err := service.SyncUpstreamModelCatalog(context.Background(), upstreamModelCapabilityAccount("https://right.example/v1", nil))
	require.NoError(t, err)
	require.Equal(t, []string{"shared-name"}, catalog.Models)
	require.Empty(t, catalog.Metadata)
	require.Empty(t, repo.updates)
	require.Len(t, catalog.Warnings, 1)
	require.Equal(t, UpstreamModelMetadataIncompleteCode, catalog.Warnings[0].Code)
}

func TestSyncUpstreamModelCatalogPreservesCompleteSnapshotForIDOnlyResponse(t *testing.T) {
	const modelID = "provider-coder"
	repo := &upstreamModelCapabilityRepo{}
	service := &AccountTestService{
		accountRepo:  repo,
		httpUpstream: &httpUpstreamRecorder{resp: capabilitySyncResponse(http.StatusOK, `{"data":[{"id":"provider-coder"}]}`)},
		cfg:          upstreamModelSyncTestConfig(),
	}
	account := upstreamModelCapabilityAccount("https://provider.example/v1", nil)
	previous := completeUpstreamModelCapability(modelID, 196000, 24000)
	previous.DisplayName = "Previously verified model"
	account.SetUpstreamModelMetadataSnapshot(UpstreamModelMetadataSnapshot{
		Identity: upstreamModelCapabilityIdentity(account),
		Source:   "upstream",
		SyncedAt: "2026-09-01T00:00:00Z",
		Models:   map[string]UpstreamModelMetadata{modelID: previous},
	})

	catalog, err := service.SyncUpstreamModelCatalog(context.Background(), account)
	require.NoError(t, err)
	require.Empty(t, catalog.Warnings)
	require.Len(t, repo.updates, 1)
	require.Equal(t, previous, catalog.Metadata[modelID])
	require.Equal(t, previous, account.GetUpstreamModelMetadataSnapshot().Models[modelID])
}

func TestSyncUpstreamModelCatalogDoesNotReuseSnapshotFromDifferentUpstream(t *testing.T) {
	const modelID = "provider-coder"
	repo := &upstreamModelCapabilityRepo{}
	service := &AccountTestService{
		accountRepo:  repo,
		httpUpstream: &httpUpstreamRecorder{resp: capabilitySyncResponse(http.StatusOK, `{"data":[{"id":"provider-coder"}]}`)},
		cfg:          upstreamModelSyncTestConfig(),
	}
	account := upstreamModelCapabilityAccount("https://new-provider.example/v1", nil)
	account.SetUpstreamModelMetadataSnapshot(UpstreamModelMetadataSnapshot{
		Identity: upstreamModelCapabilityIdentity(upstreamModelCapabilityAccount("https://old-provider.example/v1", nil)),
		Source:   "upstream",
		Models:   map[string]UpstreamModelMetadata{modelID: completeUpstreamModelCapability(modelID, 196000, 24000)},
	})

	catalog, err := service.SyncUpstreamModelCatalog(context.Background(), account)
	require.NoError(t, err)
	require.Empty(t, catalog.Metadata)
	require.Empty(t, repo.updates)
	require.Equal(t, UpstreamModelMetadataIncompleteCode, catalog.Warnings[0].Code)
}

func TestSyncUpstreamModelCatalogRejectsLateWriteAfterAccountEdit(t *testing.T) {
	repo := &upstreamModelCapabilityRepo{conflict: true}
	service := &AccountTestService{
		accountRepo: repo,
		httpUpstream: &httpUpstreamRecorder{resp: capabilitySyncResponse(http.StatusOK,
			`{"data":[{"id":"provider-coder","reasoning":true,"supported_reasoning_levels":["medium"],"input_modalities":["text"],"context_window":131072}]}`)},
		cfg: upstreamModelSyncTestConfig(),
	}

	_, err := service.SyncUpstreamModelCatalog(context.Background(), upstreamModelCapabilityAccount("https://provider.example/v1", nil))
	require.Error(t, err)
	var syncErr *UpstreamModelSyncError
	require.ErrorAs(t, err, &syncErr)
	require.Equal(t, UpstreamModelSyncErrorConfiguration, syncErr.Kind)
	require.Empty(t, repo.updates)
}

func TestSyncUpstreamModelCatalogDoesNotPersistOversizedMetadata(t *testing.T) {
	repo := &upstreamModelCapabilityRepo{}
	description := strings.Repeat("x", upstreamModelMetadataMaxDescription+1)
	service := &AccountTestService{
		accountRepo: repo,
		httpUpstream: &httpUpstreamRecorder{resp: capabilitySyncResponse(http.StatusOK,
			`{"data":[{"id":"provider-coder","description":"`+description+`","reasoning":true,"supported_reasoning_levels":["medium"],"input_modalities":["text"],"context_window":131072}]}`)},
		cfg: upstreamModelSyncTestConfig(),
	}

	catalog, err := service.SyncUpstreamModelCatalog(context.Background(), upstreamModelCapabilityAccount("https://provider.example/v1", nil))
	require.NoError(t, err)
	require.Empty(t, repo.updates)
	require.Equal(t, UpstreamModelMetadataTooLargeCode, catalog.Warnings[0].Code)
}

func TestSyncUpstreamModelCatalogNeverPersistsOAuthManifest(t *testing.T) {
	repo := &upstreamModelCapabilityRepo{}
	service := &AccountTestService{
		accountRepo: repo,
		httpUpstream: &httpUpstreamRecorder{resp: capabilitySyncResponse(http.StatusOK,
			`{"models":[{"slug":"gpt-5.6-sol","reasoning":true,"supported_reasoning_levels":["medium"],"input_modalities":["text"],"context_window":1000000}]}`)},
		cfg: upstreamModelSyncTestConfig(),
	}
	account := &Account{
		ID:       91,
		Platform: PlatformOpenAI,
		Type:     AccountTypeOAuth,
		Credentials: map[string]any{
			"access_token": "oauth-token",
		},
	}

	catalog, err := service.SyncUpstreamModelCatalog(context.Background(), account)
	require.NoError(t, err)
	require.Equal(t, []string{"gpt-5.6-sol"}, catalog.Models)
	require.Empty(t, catalog.Metadata)
	require.Empty(t, repo.updates)
}

func TestMatchModelsDevProviderRequiresExactCanonicalBaseURL(t *testing.T) {
	registry := map[string]modelsDevProvider{
		"exact": {
			ID:     "exact",
			API:    "https://provider.example/tenant-a/v1/models",
			Models: map[string]modelsDevModel{"shared-name": {ID: "shared-name"}},
		},
		"parent": {
			ID:     "parent",
			API:    "https://provider.example/v1",
			Models: map[string]modelsDevModel{"shared-name": {ID: "shared-name"}},
		},
	}

	provider, ok := matchModelsDevProvider(registry, "https://provider.example/tenant-a/v1")
	require.True(t, ok)
	require.Equal(t, "exact", provider.ID)

	_, ok = matchModelsDevProvider(registry, "https://provider.example/tenant-b/v1")
	require.False(t, ok, "a shared host or model name must not cross provider paths")

	openAIRegistry := map[string]modelsDevProvider{
		"openai": {
			ID:     "openai",
			Models: map[string]modelsDevModel{"shared-name": {ID: "shared-name"}},
		},
	}
	_, ok = matchModelsDevProvider(openAIRegistry, "https://api.openai.com/custom/v1")
	require.False(t, ok, "the first-party provider ID fallback must not match a custom path")
}
