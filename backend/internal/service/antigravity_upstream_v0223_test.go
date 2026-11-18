package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/antigravity"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestAntigravityCarbonV023AllProtocols(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, catalogKind := range []string{"canonical", "variants"} {
		for _, tc := range []struct{ model, target, level string }{
			{"gemini-4-carbon", "gemini-4-carbon", "medium"},
			{"gemini-4-carbon", "gemini-4-carbon", "low"},
			{"gemini-4-carbon", "gemini-4-carbon", "high"},
			{"gemini-4-carbon-high", "gemini-4-carbon-high", "high"},
			{"gemini-4-carbon-medium", "gemini-4-carbon-medium", "medium"},
			{"gemini-4-carbon-low", "gemini-4-carbon-low", "low"},
			{"legacy-carbon", "gemini-4-carbon-low", "low"},
		} {
			for _, protocol := range []string{"messages", "native", "chat", "responses", "probe"} {
				t.Run(catalogKind+"/"+tc.model+"/"+tc.level+"/"+protocol, func(t *testing.T) {
					upstream := &queuedHTTPUpstreamStub{responses: []*http.Response{antigravityCompatSuccessResponse()}}
					svc := newAntigravityCompatService(config.GatewayConfig{MaxLineSize: defaultMaxLineSize}, upstream)
					account := newAntigravityTierAccount()
					account.Credentials["plan_type"] = "Pro"
					account.Credentials["model_mapping"] = map[string]any{tc.model: tc.target}
					models := map[string]UpstreamModelMetadata{"gemini-4-carbon": {ID: "gemini-4-carbon"}}
					if catalogKind == "variants" {
						models = map[string]UpstreamModelMetadata{}
						for _, level := range []string{"high", "medium", "low"} {
							id := "gemini-4-carbon-" + level
							models[id] = UpstreamModelMetadata{ID: id}
						}
					}
					account.SetUpstreamModelMetadataSnapshot(UpstreamModelMetadataSnapshot{Source: "upstream", Models: models})
					wantModel, wantLevel := "gemini-4-carbon", tc.level
					if protocol == "probe" && tc.model == "gemini-4-carbon" {
						wantLevel = ""
						if catalogKind == "variants" {
							wantLevel = "high"
						}
					}
					if catalogKind == "variants" {
						wantModel += "-" + wantLevel
					}
					var result *ForwardResult
					var err error
					switch protocol {
					case "messages":
						body := []byte(fmt.Sprintf(`{"model":%q,"messages":[{"role":"user","content":"ok"}],"max_tokens":64,"output_config":{"effort":%q},"stream":true}`, tc.model, tc.level))
						result, err = svc.Forward(context.Background(), newAntigravityTierContext("/v1/messages", body), account, body, false)
					case "native":
						body := []byte(fmt.Sprintf(`{"contents":[{"role":"user","parts":[{"text":"ok"}]}],"generationConfig":{"thinkingConfig":{"thinkingLevel":%q,"thinkingBudget":4096}}}`, tc.level))
						result, err = svc.ForwardGemini(context.Background(), newAntigravityTierContext("/v1beta/models/"+tc.model+":streamGenerateContent", body), account, tc.model, "streamGenerateContent", true, body, false)
					case "chat":
						body := []byte(fmt.Sprintf(`{"model":%q,"messages":[{"role":"user","content":"ok"}],"reasoning_effort":%q,"stream":true}`, tc.model, tc.level))
						result, err = svc.ForwardAsChatCompletions(context.Background(), newAntigravityTierContext("/v1/chat/completions", body), account, body, nil)
					case "responses":
						body := []byte(fmt.Sprintf(`{"model":%q,"input":"ok","reasoning":{"effort":%q},"stream":true}`, tc.model, tc.level))
						result, err = svc.ForwardAsResponses(context.Background(), newAntigravityTierContext("/v1/responses", body), account, body, nil)
					case "probe":
						probe, probeErr := svc.TestConnection(context.Background(), account, tc.model)
						err = probeErr
						if probe != nil {
							require.Equal(t, wantModel, probe.MappedModel)
						}
					}
					require.NoError(t, err)
					if protocol != "probe" {
						require.NotNil(t, result)
						require.Equal(t, tc.model, result.Model)
						require.Equal(t, wantModel, result.UpstreamModel)
					}
					require.Len(t, upstream.requestBodies, 1)
					body := upstream.requestBodies[0]
					require.Equal(t, wantModel, gjson.GetBytes(body, "model").String())
					cfg := gjson.GetBytes(body, "request.generationConfig.thinkingConfig")
					require.False(t, cfg.Get("thinkingBudget").Exists(), string(body))
					require.Equal(t, wantLevel, cfg.Get("thinkingLevel").String())
				})
			}
		}
	}
}

func TestAntigravityGemini38V023CatalogVariants(t *testing.T) {
	account := &Account{Platform: PlatformAntigravity, Type: AccountTypeOAuth, Credentials: map[string]any{
		"model_mapping": map[string]any{
			"gemini-3.8-flash-low":    "gemini-3.8-flash-low",
			"gemini-3.8-flash-medium": "gemini-3.8-flash-medium",
			"gemini-3.8-flash-high":   "gemini-3.8-flash-high",
		},
	}}
	svc := &AntigravityGatewayService{}
	for _, level := range []string{"low", "medium", "high"} {
		want := "gemini-3.8-flash-" + level
		require.Equal(t, want, svc.getMappedModelForThinkingLevel(account, "gemini-3.8-flash", level))
		require.Equal(t, want, mapAntigravityModel(account, want))
		require.Empty(t, resolveAntigravityWrappedModelProfile(want, want).upstreamModel)
	}
	account.Credentials["model_mapping"] = map[string]any{"gemini-3.8-flash-low": "gemini-3.8-flash-low"}
	require.Equal(t, "gemini-3.8-flash-low", svc.getMappedModelForThinkingLevel(account, "gemini-3.8-flash", "high"))
	require.Empty(t, mapAntigravityModel(account, "gemini-3.8-flash-high"))
	account.Credentials["model_mapping"] = map[string]any{"gemini-3.8-flash": "custom-target"}
	require.Equal(t, "custom-target", svc.getMappedModelForThinkingLevel(account, "gemini-3.8-flash", "high"))
}

func TestAntigravityCarbonV023CatalogIsAuthoritative(t *testing.T) {
	account := &Account{Platform: PlatformAntigravity, Type: AccountTypeOAuth, Credentials: map[string]any{"plan_type": "Ultra"}}
	account.SetUpstreamModelMetadataSnapshot(UpstreamModelMetadataSnapshot{Source: "upstream", Models: map[string]UpstreamModelMetadata{"gemini-3.8-flash-high": {ID: "gemini-3.8-flash-high"}}})
	require.False(t, account.IsModelSupported("gemini-4-carbon"))
	require.Empty(t, mapAntigravityModel(account, "gemini-4-carbon"))
	account.SetUpstreamModelMetadataSnapshot(UpstreamModelMetadataSnapshot{Source: "upstream", Models: map[string]UpstreamModelMetadata{"gemini-4-carbon": {ID: "gemini-4-carbon"}}})
	models := antigravityAvailableModelOptions(account, &UpstreamModelCatalog{Models: []string{"gemini-4-carbon"}})
	require.Len(t, models, 1)
	require.Equal(t, "gemini-4-carbon", models[0].ID)
}

func TestAntigravityV023PreflightCatalogCapabilities(t *testing.T) {
	for _, tc := range []struct {
		name   string
		carbon bool
	}{
		{"611", false}, {"612", false}, {"617", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			account := &Account{Platform: PlatformAntigravity, Type: AccountTypeOAuth, Credentials: map[string]any{"plan_type": "Ultra"}}
			catalog := map[string]UpstreamModelMetadata{
				"gemini-3.8-flash-high":   {ID: "gemini-3.8-flash-high"},
				"gemini-3.8-flash-medium": {ID: "gemini-3.8-flash-medium"},
				"gemini-3.8-flash-low":    {ID: "gemini-3.8-flash-low"},
				"claude-sonnet-4-5":       {ID: "claude-sonnet-4-5"},
			}
			if tc.carbon {
				for _, level := range []string{"high", "medium", "low"} {
					for _, family := range []string{"gemini-4-carbon-", "gemini-4-argon-"} {
						id := family + level
						catalog[id] = UpstreamModelMetadata{ID: id}
					}
				}
			}
			account.SetUpstreamModelMetadataSnapshot(UpstreamModelMetadataSnapshot{Source: "upstream", Models: catalog})
			for _, id := range []string{"gemini-4-carbon", "gemini-4-carbon-high", "gemini-4-carbon-medium", "gemini-4-carbon-low", "gemini-4-argon-high"} {
				require.Equal(t, tc.carbon, account.IsModelSupported(id), id)
				require.Equal(t, tc.carbon, mapAntigravityModel(account, id) != "", id)
			}
			if tc.carbon {
				profile := resolveAntigravityWrappedModelProfile("gemini-4-carbon-low", mapAntigravityModel(account, "gemini-4-carbon-low"))
				require.Equal(t, "gemini-4-carbon-low", profile.upstreamModel)
				require.Equal(t, "low", profile.thinkingLevel)
			}
		})
	}
}

func TestAntigravityCarbonV023RefreshLegacyCatalog(t *testing.T) {
	for _, catalogModel := range []string{"gemini-4-carbon-high", "gemini-4-carbon-medium", "gemini-4-carbon-low", "gemini-4-carbon"} {
		t.Run(catalogModel, func(t *testing.T) {
			svc, account, registry := antigravityCapabilitySyncFixture(t, fmt.Sprintf(`{"models":{%q:{"supportsThinking":true,"supportsImages":true,"maxTokens":1048576,"maxOutputTokens":65536}}}`, catalogModel), &upstreamModelCapabilityRepo{})
			account.Credentials["model_mapping"] = map[string]any{"gemini-4-carbon": "gemini-4-carbon"}
			account.SetUpstreamModelMetadataSnapshot(UpstreamModelMetadataSnapshot{Source: "upstream", Models: map[string]UpstreamModelMetadata{"gemini-4-argon-high": {ID: "gemini-4-argon-high"}}})
			models, err := svc.AvailableAntigravityModels(context.Background(), account)
			require.NoError(t, err)
			require.Len(t, models, 1)
			require.Equal(t, "gemini-4-carbon", models[0].ID)
			require.Empty(t, registry.requests)
			snapshot := account.GetUpstreamModelMetadataSnapshot()
			require.NotNil(t, snapshot)
			require.Contains(t, snapshot.Models, catalogModel)
			if catalogModel != "gemini-4-carbon" {
				require.NotContains(t, snapshot.Models, "gemini-4-carbon", "public projection must not invent a callable base route")
			}
			require.NotContains(t, snapshot.Models, "gemini-4-argon-high", "live catalog must replace stale capability IDs")
			require.Equal(t, int64(65536), snapshot.Models[catalogModel].MaxOutputTokens)
			require.Equal(t, catalogModel, mapAntigravityModel(account, "gemini-4-carbon"))
			require.True(t, account.IsModelSupported("gemini-4-carbon"), "old tier whitelist must admit the canonical route")
			require.False(t, account.IsModelSupported("gemini-4-argon-high"))
		})
	}
}

func TestAntigravityV023ConnectionProbeAutoEndpoints(t *testing.T) {
	require.GreaterOrEqual(t, len(antigravity.BaseURLs), 2)
	prod, daily := antigravity.BaseURLs[0], antigravity.BaseURLs[1]
	const bareEndpoint429 = `{"error":{"code":429,"status":"RESOURCE_EXHAUSTED","message":"Resource has been exhausted (e.g. check quota)."}}`
	t.Setenv(antigravityForwardBaseURLEnv, "auto")
	for _, model := range []string{"gemini-3.8-flash-low", "gemini-4-carbon-high"} {
		for _, succeeds := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/dailySuccess=%t", model, succeeds), func(t *testing.T) {
				limited := func() *http.Response {
					return &http.Response{StatusCode: http.StatusTooManyRequests, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(bareEndpoint429))}
				}
				lastResponse := limited()
				if succeeds {
					lastResponse = antigravityCompatSuccessResponse()
				}
				var urls []string
				upstream := &queuedHTTPUpstreamStub{responses: []*http.Response{limited(), lastResponse}, onCall: func(req *http.Request, _ *queuedHTTPUpstreamStub) {
					urls = append(urls, req.URL.Host)
				}}
				svc := newAntigravityCompatService(config.GatewayConfig{MaxLineSize: defaultMaxLineSize}, upstream)
				account := newAntigravityTierAccount()
				account.Credentials["plan_type"] = "Ultra"
				account.SetUpstreamModelMetadataSnapshot(UpstreamModelMetadataSnapshot{Source: "upstream", Models: map[string]UpstreamModelMetadata{model: {ID: model}}})
				before, err := json.Marshal(account)
				require.NoError(t, err)
				result, err := svc.TestConnection(context.Background(), account, model)
				if succeeds {
					require.NoError(t, err)
					require.NotEmpty(t, result.Text)
				} else {
					require.ErrorContains(t, err, "429")
				}
				require.Equal(t, []string{strings.TrimPrefix(prod, "https://"), strings.TrimPrefix(daily, "https://")}, urls)
				require.Len(t, upstream.requestBodies, 2)
				wantModel := model
				for _, body := range upstream.requestBodies {
					require.Equal(t, wantModel, gjson.GetBytes(body, "model").String())
				}
				require.Empty(t, svc.endpointPreferences)
				after, err := json.Marshal(account)
				require.NoError(t, err)
				require.JSONEq(t, string(before), string(after), "read-only probes must preserve credentials and scheduling state")
			})
		}
	}
}
