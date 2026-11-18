//go:build unit

package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestGPT62RoutingIdentityAndEffort(t *testing.T) {
	for _, model := range []string{"gpt-6.2-sol", "gpt-6.2-astra"} {
		for _, suffix := range []string{"", "-low", "-medium", "-high", "-xhigh", "-max", "-openai-compact"} {
			alias := "openai/" + strings.ToUpper(model) + suffix
			require.Equal(t, model, normalizeKnownOpenAICodexModel(alias), alias)
			require.Equal(t, model, normalizeCodexModel(alias), alias)
			require.Equal(t, model, NormalizeOpenAICompatRequestedModel(alias), alias)
			require.True(t, shouldAutoInjectPromptCacheKeyForCompat(alias), alias)
			require.True(t, openAIModelRequiresResponsesForToolCalls(alias), alias)
			require.True(t, configuredCodexSupportsPriorityServiceTier(alias), alias)
		}
		for _, effort := range []string{"low", "medium", "high", "xhigh", "max"} {
			req := &apicompat.AnthropicRequest{Model: "openai/" + model + "-" + effort}
			applyOpenAICompatModelNormalization(req)
			require.Equal(t, model, req.Model)
			require.NotNil(t, req.OutputConfig)
			require.Equal(t, effort, req.OutputConfig.Effort)
			require.NoError(t, validateGPT61CompatRequest([]byte(`{"reasoning":{"effort":"`+effort+`"}}`), model))
		}
		for _, effort := range []string{"none", "minimal"} {
			for _, body := range []string{
				`{"reasoning":{"effort":"` + effort + `"}}`,
				`{"reasoning_effort":"` + effort + `"}`,
				`{"output_config":{"effort":"` + effort + `"}}`,
				`{"model":"` + model + `-` + effort + `"}`,
			} {
				require.ErrorContains(t, validateGPT61CompatRequest([]byte(body), model), "does not support reasoning effort")
			}
		}
		require.ErrorContains(t, validateGPT61CompatRequest([]byte(`{"thinking":{"type":"disabled"}}`), model), "does not support reasoning effort")
	}
	for _, model := range []string{"gpt-6.2", "gpt-6.2-unknown", "gpt-6.2-sol-preview", "gpt-6.2-astra-law"} {
		require.Empty(t, normalizeKnownOpenAICodexModel(model), model)
		require.False(t, openAIModelSupportsUltrafast(model), model)
	}
}

func TestGPT62RoutingOfficialDescriptor(t *testing.T) {
	for _, tc := range []struct {
		model            string
		window, priority int
	}{{"gpt-6.2-sol", 500000, 1}, {"gpt-6.2-astra", 272000, 2}} {
		t.Run(tc.model, func(t *testing.T) {
			descriptor := newConfiguredCodexModelDescriptor(tc.model)
			body, err := json.Marshal(descriptor)
			require.NoError(t, err)
			var model map[string]any
			require.NoError(t, json.Unmarshal(body, &model))
			require.EqualValues(t, tc.window, model["context_window"])
			require.EqualValues(t, 872000, model["max_context_window"])
			require.EqualValues(t, tc.priority, model["priority"])
			require.Equal(t, "low", model["default_reasoning_level"])
			require.Equal(t, []string{"low", "medium", "high", "xhigh", "max", "ultra"}, effortsFromManifestModel(t, model))
			require.Equal(t, "xhigh", model["multi_agent_reasoning_effort"])
			require.Equal(t, "v2", model["multi_agent_version"])
			require.Equal(t, true, model["use_responses_lite"])
			require.Equal(t, "code_mode_only", model["tool_mode"])
			var official map[string]json.RawMessage
			require.NoError(t, json.Unmarshal(descriptor.officialMetadata, &official))
			for field, value := range official {
				require.JSONEq(t, string(value), gjson.GetBytes(body, field).Raw, field)
			}
		})
	}
}

func TestGPT62RoutingPlanEntitlementManifestAndForce(t *testing.T) {
	const fastOnly = `[{"id":"priority","name":"Fast","description":"account Fast","provider_option":true}]`
	const both = `[{"id":"priority","name":"Fast"},{"id":"ultrafast","name":"Ultrafast","description":"workspace grant","provider_option":true}]`
	for _, tc := range []struct {
		plan, model, snapshot string
		want                  bool
	}{
		{"pro", "gpt-6.2-sol", fastOnly, true},
		{"pro", "gpt-6.2-astra", fastOnly, false},
		{"ChatGPT Pro", "gpt-6.2-sol", "", true},
		{"chatgpt_pro", "gpt-6.2-sol", "", true},
		{"Pro-Max", "gpt-6.2-astra", fastOnly, true},
		{"promax", "gpt-6.2-sol", "", true},
		{"prolite", "gpt-6.2-sol", fastOnly, false},
		{"plus", "gpt-6.2-sol", fastOnly, false},
		{"enterprise", "gpt-6.2-astra", both, true},
		{"edu", "gpt-6.2-sol", fastOnly, false},
		{"pro", "gpt-6.2-sol", both, true},
	} {
		t.Run(tc.plan+"/"+tc.model+"/"+tc.snapshot, func(t *testing.T) {
			account := Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeOAuth,
				Credentials: map[string]any{"plan_type": tc.plan, "model_mapping": map[string]any{"public": tc.model}}}
			if tc.snapshot != "" {
				account.SetUpstreamModelMetadataSnapshot(UpstreamModelMetadataSnapshot{Models: map[string]UpstreamModelMetadata{
					tc.model: {ID: tc.model, CodexToolCapabilities: map[string]json.RawMessage{"service_tiers": json.RawMessage(tc.snapshot)}},
				}})
			}
			group := &Group{ID: 1, Platform: PlatformOpenAI, Status: StatusActive, Hydrated: true, ForceOpenAIFast: true, ForceOpenAIUltrafast: true}
			gateway := &GatewayService{accountRepo: codexModelsVisibilityAccountRepo{byGroup: map[int64][]Account{1: {account}}}}
			body, err := gateway.BuildCodexModelsManifestForGroup(context.Background(), group, "", []string{"public"})
			require.NoError(t, err)
			models := decodeCodexManifestModels(t, body)
			require.Len(t, models, 1)
			require.Equal(t, "public", models[0]["slug"])
			tiers := models[0]["service_tiers"].([]any)
			require.Equal(t, "priority", tiers[0].(map[string]any)["id"])
			if tc.snapshot == fastOnly {
				require.Equal(t, "account Fast", tiers[0].(map[string]any)["description"])
				require.Equal(t, true, tiers[0].(map[string]any)["provider_option"])
			}
			ctx := context.WithValue(context.Background(), ctxkey.Group, group)
			forced := openAIGroupForcedServiceTier(ctx, &account, account.GetMappedModel("public")+"-max")
			if tc.want {
				require.Len(t, tiers, 2)
				require.Equal(t, "ultrafast", tiers[1].(map[string]any)["id"])
				require.Equal(t, OpenAIFastTierUltrafast, forced)
				if tc.snapshot == both {
					require.Equal(t, "workspace grant", tiers[1].(map[string]any)["description"])
					require.Equal(t, true, tiers[1].(map[string]any)["provider_option"])
				}
			} else {
				require.Len(t, tiers, 1)
				require.Equal(t, OpenAIFastTierPriority, forced)
			}
		})
	}
}

func TestGPT62RoutingAPIKeyCapabilitiesAndAllowlist(t *testing.T) {
	for _, model := range []string{"gpt-6.2-sol", "gpt-6.2-astra"} {
		for _, official := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/official=%t", model, official), func(t *testing.T) {
				account := rawGPT56ResponsesAPIKeyAccount("public", model)
				account.Credentials["plan_type"] = "promax"
				if official {
					account.Credentials["base_url"] = "https://api.openai.com/v1"
				}
				require.False(t, gpt62PlanIncludesUltrafast(account, model))
				caps := accountCodexToolCapabilities(account, model)
				require.Equal(t, "false", string(caps["use_responses_lite"]))
				require.Equal(t, official, strings.Contains(string(caps["service_tiers"]), "ultrafast"))
				if official {
					require.Contains(t, string(caps["service_tiers"]), fmt.Sprintf("%gx", openAIUltrafastMultiplier(model, time.Now())))
				}
				account.SetUpstreamModelMetadataSnapshot(UpstreamModelMetadataSnapshot{Models: map[string]UpstreamModelMetadata{
					model: {ID: model, CodexToolCapabilities: map[string]json.RawMessage{"use_responses_lite": json.RawMessage("true")}},
				}})
				group := &Group{ID: 1, Platform: PlatformOpenAI, Status: StatusActive, Hydrated: true}
				gateway := &GatewayService{accountRepo: codexModelsVisibilityAccountRepo{byGroup: map[int64][]Account{1: {*account}}}}
				body, err := gateway.BuildCodexModelsManifestForGroup(context.Background(), group, "", []string{"public"})
				require.NoError(t, err)
				models := decodeCodexManifestModels(t, body)
				require.Len(t, models, 1)
				require.Equal(t, "public", models[0]["slug"])
				require.Equal(t, false, models[0]["use_responses_lite"])
				require.False(t, account.IsModelSupported("gpt-6.2-sol"))
				require.False(t, account.IsModelSupported("gpt-6.2-astra"))
				require.False(t, account.IsModelSupported("gpt-5.6-luna"))
			})
		}
	}
}

func TestGPT62RoutingForcedTierHTTPWebSocketParity(t *testing.T) {
	for _, tc := range []struct {
		name, model, plan, accountType, want string
		ultrafast, fast, hydrated            bool
	}{
		{"off", "gpt-6.2-sol", "promax", AccountTypeOAuth, "flex", false, false, true},
		{"sol-pro", "gpt-6.2-sol", "pro", AccountTypeOAuth, "ultrafast", true, true, true},
		{"astra-pro-fast", "gpt-6.2-astra", "pro", AccountTypeOAuth, "priority", true, true, true},
		{"astra-pro-no-fallback", "gpt-6.2-astra", "pro", AccountTypeOAuth, "flex", true, false, true},
		{"astra-promax", "gpt-6.2-astra", "promax", AccountTypeOAuth, "ultrafast", true, true, true},
		{"api-key-force", "gpt-6.2-sol", "", AccountTypeAPIKey, "ultrafast", true, true, true},
		{"unsupported-model", "gpt-5.5", "promax", AccountTypeOAuth, "priority", true, true, true},
		{"unhydrated-group", "gpt-6.2-sol", "pro", AccountTypeOAuth, "flex", true, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			group := &Group{ID: 1, Platform: PlatformOpenAI, Status: StatusActive, Hydrated: tc.hydrated,
				ForceOpenAIFast: tc.fast, ForceOpenAIUltrafast: tc.ultrafast}
			account := &Account{Platform: PlatformOpenAI, Type: tc.accountType, Credentials: map[string]any{"plan_type": tc.plan}}
			ctx := context.WithValue(context.Background(), ctxkey.Group, group)
			svc := &OpenAIGatewayService{}
			body := []byte(`{"type":"response.create","model":"public","service_tier":"flex","temperature":0.7,"top_p":0.9,"logprobs":true,"top_logprobs":3,"include":["reasoning.encrypted_content","message.output_text.logprobs","other"],"prompt_cache_key":"client-cache","previous_response_id":"resp_previous","client_metadata":{"workflow":"ultra"},"metadata":{"client":"preserved"},"sequence":900719925474099312345}`)
			httpBody, err := svc.applyOpenAIFastPolicyToBody(ctx, account, tc.model, body)
			require.NoError(t, err)
			wsBody, _, err := svc.applyOpenAIFastPolicyToWSResponseCreate(ctx, account, tc.model, body)
			require.NoError(t, err)
			require.JSONEq(t, string(httpBody), string(wsBody))
			require.Equal(t, tc.want, gjson.GetBytes(httpBody, "service_tier").String())
			if strings.HasPrefix(tc.model, "gpt-6.2-") {
				for _, field := range []string{"temperature", "top_p", "logprobs", "top_logprobs"} {
					require.False(t, gjson.GetBytes(httpBody, field).Exists(), field)
				}
				require.JSONEq(t, `["reasoning.encrypted_content","other"]`, gjson.GetBytes(httpBody, "include").Raw)
			} else {
				require.True(t, gjson.GetBytes(httpBody, "temperature").Exists())
			}
			for _, field := range []string{"prompt_cache_key", "previous_response_id", "client_metadata", "metadata", "sequence"} {
				require.Equal(t, gjson.GetBytes(body, field).Raw, gjson.GetBytes(httpBody, field).Raw, field)
			}
			frame := []byte(`{"type":"session.update","service_tier":"flex","temperature":0.7}`)
			unchanged, _, err := svc.applyOpenAIFastPolicyToWSResponseCreate(ctx, account, tc.model, frame)
			require.NoError(t, err)
			require.Equal(t, frame, unchanged)
		})
	}
	require.Empty(t, openAIGroupForcedServiceTier(nil, nil, "gpt-6.2-sol"))
}

func TestGPT62RoutingChatOnlyFallbackGuards(t *testing.T) {
	for _, model := range []string{"gpt-6.2-sol", "gpt-6.2-astra"} {
		for _, protocol := range []string{"responses", "messages"} {
			for _, invalid := range []string{"tools", "none", "minimal"} {
				t.Run(model+"/"+protocol+"/"+invalid, func(t *testing.T) {
					body := `{"model":"public","input":"hi","messages":[{"role":"user","content":"hi"}],"max_tokens":16`
					if invalid == "tools" {
						body += `,"tools":[{"type":"function","name":"lookup","description":"lookup","input_schema":{"type":"object"},"parameters":{"type":"object"}}]}`
					} else {
						body += `,"reasoning":{"effort":"` + invalid + `"},"output_config":{"effort":"` + invalid + `"}}`
					}
					account := passthroughArgsFallbackAccount()
					account.Credentials["model_mapping"] = map[string]any{"public": model}
					rec := httptest.NewRecorder()
					c, _ := gin.CreateTestContext(rec)
					c.Request = httptest.NewRequest(http.MethodPost, "/v1/"+protocol, strings.NewReader(body))
					upstream := &httpUpstreamRecorder{err: errors.New("must not reach upstream")}
					svc := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig(), httpUpstream: upstream}
					var err error
					if protocol == "responses" {
						_, err = svc.forwardResponsesViaRawChatCompletions(context.Background(), c, account, []byte(body))
					} else {
						_, err = svc.forwardAnthropicViaRawChatCompletions(context.Background(), c, account, []byte(body), "")
					}
					if invalid == "tools" {
						require.ErrorContains(t, err, model+" requires Responses for tool calls")
					} else {
						require.ErrorContains(t, err, "does not support reasoning effort")
					}
					require.Equal(t, http.StatusBadRequest, rec.Code)
					require.Empty(t, upstream.requests)
				})
			}
		}
	}
}

func TestGPT62RoutingMappedChatFallbackSamplingAndTier(t *testing.T) {
	for _, protocol := range []string{"responses", "messages"} {
		for _, model := range []string{"gpt-6.2-sol", "gpt-6.2-astra"} {
			t.Run(protocol+"/"+model, func(t *testing.T) {
				account := passthroughArgsFallbackAccount()
				account.Credentials["model_mapping"] = map[string]any{"public": model}
				body := []byte(`{"model":"public","input":"hi","messages":[{"role":"user","content":"hi"}],"max_tokens":16,"temperature":0.3,"top_p":0.8,"reasoning":{"effort":"max"},"output_config":{"effort":"max"}}`)
				upstream := &httpUpstreamRecorder{err: errors.New("captured outbound request")}
				svc := &OpenAIGatewayService{cfg: passthroughArgsTestConfig(), httpUpstream: upstream}
				ctx := context.WithValue(context.Background(), ctxkey.Group, &Group{ID: 1, Platform: PlatformOpenAI, Status: StatusActive, Hydrated: true, ForceOpenAIUltrafast: true})
				c := adaptiveProtocolTestContext("/v1/"+protocol, body)
				var err error
				if protocol == "responses" {
					_, err = svc.forwardResponsesViaRawChatCompletions(ctx, c, account, body)
				} else {
					_, err = svc.forwardAnthropicViaRawChatCompletions(ctx, c, account, body, "")
				}
				require.Error(t, err)
				require.Equal(t, model, gjson.GetBytes(upstream.lastBody, "model").String())
				require.Equal(t, "max", gjson.GetBytes(upstream.lastBody, "reasoning_effort").String())
				require.Equal(t, "ultrafast", gjson.GetBytes(upstream.lastBody, "service_tier").String())
				require.False(t, gjson.GetBytes(upstream.lastBody, "temperature").Exists())
				require.False(t, gjson.GetBytes(upstream.lastBody, "top_p").Exists())
			})
		}
	}
}

func TestGPT62RoutingNativeResponsesToolsAndFingerprint(t *testing.T) {
	for _, kind := range []string{AccountTypeOAuth, AccountTypeAPIKey} {
		for _, model := range []string{"gpt-6.2-sol", "gpt-6.2-astra"} {
			t.Run(kind+"/"+model, func(t *testing.T) {
				account := rawGPT56ResponsesAPIKeyAccount("public", model)
				if kind == AccountTypeOAuth {
					account = rawGPT56ResponsesOAuthAccount("public", model)
				}
				account.Extra = map[string]any{"openai_oauth_responses_websockets_v2_mode": OpenAIWSIngressModeOff, codexFingerprintModeExtraKey: "off"}
				body := []byte(`{"model":"` + model + `","input":"hi","stream":true,"instructions":"client instructions","temperature":0.3,"top_p":0.8,"tools":[{"type":"function","name":"lookup","parameters":{"type":"object","properties":{}}}],"tool_choice":{"type":"function","name":"lookup"},"reasoning":{"effort":"max","mode":"pro"},"prompt_cache_key":"client-cache","client_metadata":{"workflow":"ultra","x-codex-installation-id":"client-install"}}`)
				rec := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(rec)
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(string(body)))
				c.Request.Header.Set("User-Agent", "curl/8.0")
				upstream := &httpUpstreamRecorder{resp: openAICompatSSECompletedResponse("resp_gpt62", model)}
				svc := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: upstream}
				result, err := svc.Forward(context.Background(), c, account, body)
				require.NoError(t, err)
				require.NotNil(t, result)
				require.Len(t, upstream.requests, 1)
				require.True(t, strings.HasSuffix(upstream.requests[0].URL.Path, "/responses"), upstream.requests[0].URL.String())
				require.Equal(t, model, gjson.GetBytes(upstream.lastBody, "model").String())
				require.Equal(t, "lookup", gjson.GetBytes(upstream.lastBody, "tools.0.name").String())
				require.Equal(t, "lookup", gjson.GetBytes(upstream.lastBody, "tool_choice.name").String())
				require.False(t, gjson.GetBytes(upstream.lastBody, "temperature").Exists())
				require.False(t, gjson.GetBytes(upstream.lastBody, "top_p").Exists())
				for _, field := range []string{"instructions", "reasoning", "prompt_cache_key", "client_metadata"} {
					require.JSONEq(t, gjson.GetBytes(body, field).Raw, gjson.GetBytes(upstream.lastBody, field).Raw, field)
				}
				require.Equal(t, codexFingerprintOff, account.GetCodexFingerprintMode())
				require.NotContains(t, account.Extra, codexFingerprintSeedExtraKey)
			})
		}
	}
}

func TestGPT62RoutingReasoningModeBeforeAliasMapping(t *testing.T) {
	for _, model := range []string{"gpt-6.2-sol", "gpt-6.2-astra"} {
		account := rawGPT56ResponsesOAuthAccount("public", model)
		body := []byte(`{"model":"public","reasoning":{"effort":"max","mode":"pro"},"metadata":{"user":"client"}}`)
		normalized, changed, err := normalizeOpenAIResponsesReasoningModeForAccount(account, body, false)
		require.NoError(t, err)
		require.False(t, changed)
		require.Equal(t, body, normalized)
	}
	legacy := []byte(`{"model":"legacy","reasoning":{"mode":"pro"}}`)
	account := rawGPT56ResponsesOAuthAccount("legacy", "gpt-5.5")
	normalized, changed, err := normalizeOpenAIResponsesReasoningModeForAccount(account, legacy, false)
	require.NoError(t, err)
	require.True(t, changed)
	require.JSONEq(t, `{"model":"legacy","reasoning":{"effort":"max"}}`, string(normalized))
}

func TestGPT62RoutingCompatToolsReachResponses(t *testing.T) {
	for _, model := range []string{"gpt-6.2-sol", "gpt-6.2-astra"} {
		for _, kind := range []string{AccountTypeOAuth, AccountTypeAPIKey} {
			for _, protocol := range []string{"chat/completions", "messages"} {
				t.Run(model+"/"+kind+"/"+protocol, func(t *testing.T) {
					account := rawGPT56ResponsesAPIKeyAccount("public", model)
					if kind == AccountTypeOAuth {
						account = rawGPT56ResponsesOAuthAccount("public", model)
					}
					account.Credentials["plan_type"] = "promax"
					group := &Group{ID: 1, Platform: PlatformOpenAI, Hydrated: true, Status: StatusActive, ForceOpenAIFast: true, ForceOpenAIUltrafast: true}
					ctx := context.WithValue(context.Background(), ctxkey.Group, group)
					body := `{"model":"public","messages":[{"role":"user","content":"lookup"}],"max_tokens":16,"temperature":0.4,"top_p":0.7,"reasoning_effort":"max","output_config":{"effort":"max"}`
					if protocol == "messages" {
						body += `,"tools":[{"name":"lookup","description":"lookup","input_schema":{"type":"object","properties":{}}}]}`
					} else {
						body += `,"tools":[{"type":"function","function":{"name":"lookup","description":"lookup","parameters":{"type":"object","properties":{}}}}]}`
					}
					upstream := &httpUpstreamRecorder{resp: openAICompatSSECompletedResponse("resp_gpt62_compat", model)}
					svc := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig(), httpUpstream: upstream}
					rec := httptest.NewRecorder()
					c, _ := gin.CreateTestContext(rec)
					c.Request = httptest.NewRequest(http.MethodPost, "/v1/"+protocol, strings.NewReader(body))
					var result *OpenAIForwardResult
					var err error
					if protocol == "messages" {
						result, err = svc.ForwardAsAnthropic(ctx, c, account, []byte(body), "", "")
					} else {
						result, err = svc.ForwardAsChatCompletions(ctx, c, account, []byte(body), "", "")
					}
					require.NoError(t, err)
					require.NotNil(t, result)
					require.Len(t, upstream.requests, 1)
					require.True(t, strings.HasSuffix(upstream.requests[0].URL.Path, "/responses"), upstream.requests[0].URL.String())
					require.Equal(t, model, gjson.GetBytes(upstream.lastBody, "model").String())
					require.Equal(t, "max", gjson.GetBytes(upstream.lastBody, "reasoning.effort").String())
					require.Equal(t, "ultrafast", gjson.GetBytes(upstream.lastBody, "service_tier").String())
					require.NotNil(t, result.ServiceTier)
					require.Equal(t, "ultrafast", *result.ServiceTier)
					require.Equal(t, "lookup", gjson.GetBytes(upstream.lastBody, "tools.0.name").String())
					require.False(t, gjson.GetBytes(upstream.lastBody, "temperature").Exists())
					require.False(t, gjson.GetBytes(upstream.lastBody, "top_p").Exists())
				})
			}
		}
	}
}
