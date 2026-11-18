//go:build unit

package service

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func gpt62ForwardValidationAccount(kind string, passthrough bool, requested, upstream string) *Account {
	account := rawGPT56ResponsesAPIKeyAccount(requested, upstream)
	if kind == AccountTypeOAuth {
		account = rawGPT56ResponsesOAuthAccount(requested, upstream)
	}
	if account.Extra == nil {
		account.Extra = map[string]any{}
	}
	account.Extra["openai_passthrough"] = passthrough
	account.Extra["openai_oauth_responses_websockets_v2_mode"] = OpenAIWSIngressModeOff
	account.Extra[codexFingerprintModeExtraKey] = "off"
	return account
}

func TestGPT62ForwardAliasAndPassthroughPreserveReasoning(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, model := range []string{"gpt-6.2-sol", "gpt-6.2-astra"} {
		for _, kind := range []string{AccountTypeAPIKey, AccountTypeOAuth} {
			for _, passthrough := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/passthrough=%t", model, kind, passthrough), func(t *testing.T) {
					requested := "public"
					if passthrough {
						requested = model
					}
					account := gpt62ForwardValidationAccount(kind, passthrough, requested, model)
					body := []byte(fmt.Sprintf(`{"model":%q,"input":"hi","stream":true,"instructions":"client instructions","reasoning":{"effort":"max","mode":"pro"},"prompt_cache_key":"client-cache","client_metadata":{"workflow":"ultra","x-codex-installation-id":"client-install"},"tools":[{"type":"function","name":"lookup","parameters":{"type":"object","properties":{}}}],"tool_choice":{"type":"function","name":"lookup"}}`, requested))
					recorder := httptest.NewRecorder()
					c, _ := gin.CreateTestContext(recorder)
					c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(string(body)))
					upstream := &httpUpstreamRecorder{resp: openAICompatSSECompletedResponse("resp_gpt62_alias", model)}
					svc := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig(), httpUpstream: upstream}
					result, err := svc.Forward(context.Background(), c, account, body)
					require.NoError(t, err)
					require.NotNil(t, result)
					require.Equal(t, http.StatusOK, recorder.Code)
					require.Len(t, upstream.requests, 1)
					require.True(t, strings.HasSuffix(upstream.lastReq.URL.Path, "/responses"))
					require.Equal(t, model, gjson.GetBytes(upstream.lastBody, "model").String())
					for _, field := range []string{"instructions", "reasoning", "prompt_cache_key", "client_metadata", "tools", "tool_choice"} {
						require.JSONEq(t, gjson.GetBytes(body, field).Raw, gjson.GetBytes(upstream.lastBody, field).Raw, field)
					}
					require.NotNil(t, result.ReasoningEffort)
					require.Equal(t, "max", *result.ReasoningEffort)
					require.Equal(t, codexFingerprintOff, account.GetCodexFingerprintMode())
					require.NotContains(t, account.Extra, codexFingerprintSeedExtraKey)
				})
			}
		}
	}
}

func TestGPT62ForwardRejectsUnsupportedReasoningBeforeUpstream(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, model := range []string{"gpt-6.2-sol", "gpt-6.2-astra"} {
		for _, kind := range []string{AccountTypeAPIKey, AccountTypeOAuth} {
			for _, passthrough := range []bool{false, true} {
				for _, test := range []struct {
					name, fields, suffix string
				}{
					{name: "nested none", fields: `,"reasoning":{"effort":"none"}`},
					{name: "nested minimal", fields: `,"reasoning":{"effort":"minimal"}`},
					{name: "flat none", fields: `,"reasoning_effort":"none"`},
					{name: "flat minimal", fields: `,"reasoning_effort":"minimal"`},
					{name: "output config none", fields: `,"output_config":{"effort":"none"}`},
					{name: "disabled thinking", fields: `,"thinking":{"type":"disabled"}`},
					{name: "suffix none", suffix: "-none"},
					{name: "suffix minimal", suffix: "_minimal"},
				} {
					t.Run(fmt.Sprintf("%s/%s/passthrough=%t/%s", model, kind, passthrough, test.name), func(t *testing.T) {
						requested := "public"
						if passthrough || test.suffix != "" {
							requested = model + test.suffix
						}
						account := gpt62ForwardValidationAccount(kind, passthrough, requested, model)
						body := []byte(fmt.Sprintf(`{"model":%q,"input":"hi","stream":true,"instructions":"client instructions"%s}`, requested, test.fields))
						recorder := httptest.NewRecorder()
						c, _ := gin.CreateTestContext(recorder)
						c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(string(body)))
						upstream := &httpUpstreamRecorder{resp: openAICompatSSECompletedResponse("resp_must_not_forward", model)}
						svc := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig(), httpUpstream: upstream}
						result, err := svc.Forward(context.Background(), c, account, body)
						require.ErrorContains(t, err, "does not support reasoning effort")
						require.Nil(t, result)
						require.Equal(t, http.StatusBadRequest, recorder.Code, recorder.Body.String())
						require.Equal(t, "invalid_request_error", gjson.GetBytes(recorder.Body.Bytes(), "error.type").String())
						require.Empty(t, upstream.requests)
					})
				}
			}
		}
	}
}

func TestGPT62ForwardValidatesAfterSingleGroupPolicyApplication(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, model := range []string{"gpt-6.2-sol", "gpt-6.2-astra"} {
		for _, kind := range []string{AccountTypeAPIKey, AccountTypeOAuth} {
			for _, passthrough := range []bool{false, true} {
				for _, test := range []struct {
					name, effort, ceiling, overLimit, want string
					mappings                               []ReasoningEffortMapping
					status                                 int
				}{
					{name: "one hop", effort: "high", mappings: []ReasoningEffortMapping{{From: "high", To: "low"}, {From: "low", To: "minimal"}}, want: "low", status: 200},
					{name: "none mapped before service", effort: "none", mappings: []ReasoningEffortMapping{{From: "none", To: "low"}}, want: "low", status: 200},
					{name: "ceiling", effort: "max", ceiling: "medium", want: "medium", status: 200},
					{name: "invalid mapped target", effort: "high", mappings: []ReasoningEffortMapping{{From: "high", To: "minimal"}}, status: 400},
					{name: "invalid ceiling", effort: "max", ceiling: "minimal", status: 400},
					{name: "denied mapping", effort: "high", mappings: []ReasoningEffortMapping{{From: "high", To: "deny"}}, status: 403},
					{name: "denied ceiling", effort: "max", ceiling: "low", overLimit: ReasoningEffortOverLimitDeny, status: 403},
				} {
					t.Run(fmt.Sprintf("%s/%s/passthrough=%t/%s", model, kind, passthrough, test.name), func(t *testing.T) {
						requested := "public"
						if passthrough {
							requested = model
						}
						account := gpt62ForwardValidationAccount(kind, passthrough, requested, model)
						body := []byte(fmt.Sprintf(`{"model":%q,"input":"hi","stream":true,"instructions":"client instructions","reasoning":{"effort":%q,"mode":"pro"}}`, requested, test.effort))
						group := &Group{ID: 1, Platform: PlatformOpenAI, Hydrated: true, Status: StatusActive, MaxReasoningEffort: test.ceiling, ReasoningEffortMappings: test.mappings, MaxReasoningEffortOverLimit: test.overLimit}
						upstream := &httpUpstreamRecorder{resp: openAICompatSSECompletedResponse("resp_group_policy", model)}
						svc := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig(), httpUpstream: upstream}
						var forwardErr error
						router := gin.New()
						router.POST("/v1/responses", func(c *gin.Context) {
							// Native Responses applies the group policy before account forwarding.
							policyBody, _, err := ApplyOpenAIReasoningEffortPolicy(body, group.MaxReasoningEffort, group.ReasoningEffortMappings, group.MaxReasoningEffortOverLimit)
							if err != nil {
								forwardErr = err
								c.JSON(http.StatusForbidden, gin.H{"error": gin.H{"type": "permission_error", "message": err.Error()}})
								return
							}
							ctx := context.WithValue(c.Request.Context(), ctxkey.Group, group)
							_, forwardErr = svc.Forward(ctx, c, account, policyBody)
						})
						recorder := httptest.NewRecorder()
						router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(string(body))))
						require.Equal(t, test.status, recorder.Code, recorder.Body.String())
						if test.status != http.StatusOK {
							require.Error(t, forwardErr)
							require.Empty(t, upstream.requests)
							return
						}
						require.NoError(t, forwardErr)
						require.Len(t, upstream.requests, 1)
						require.Equal(t, model, gjson.GetBytes(upstream.lastBody, "model").String())
						require.Equal(t, test.want, gjson.GetBytes(upstream.lastBody, "reasoning.effort").String())
						require.Equal(t, "pro", gjson.GetBytes(upstream.lastBody, "reasoning.mode").String())
					})
				}
			}
		}
	}
}

func TestGPT62ForwardPassthroughDoesNotApplyUnusedAccountMapping(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, kind := range []string{AccountTypeAPIKey, AccountTypeOAuth} {
		t.Run(kind, func(t *testing.T) {
			account := gpt62ForwardValidationAccount(kind, true, "gpt-5.4", "gpt-6.2-sol")
			body := []byte(`{"model":"gpt-5.4","input":"hi","stream":true,"instructions":"client instructions","reasoning":{"effort":"none","mode":"pro"}}`)
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(string(body)))
			upstream := &httpUpstreamRecorder{resp: openAICompatSSECompletedResponse("resp_legacy_passthrough", "gpt-5.4")}
			svc := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig(), httpUpstream: upstream}
			_, err := svc.Forward(context.Background(), c, account, body)
			require.NoError(t, err)
			require.Len(t, upstream.requests, 1)
			require.Equal(t, "gpt-5.4", gjson.GetBytes(upstream.lastBody, "model").String())
			require.Equal(t, "none", gjson.GetBytes(upstream.lastBody, "reasoning.effort").String())
			require.Equal(t, kind == AccountTypeAPIKey, gjson.GetBytes(upstream.lastBody, "reasoning.mode").Exists())
		})
	}
}

func TestGPT62ForwardCompactValidatesActualModel(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, model := range []string{"gpt-6.2-sol", "gpt-6.2-astra"} {
		for _, kind := range []string{AccountTypeAPIKey, AccountTypeOAuth} {
			for _, passthrough := range []bool{false, true} {
				for _, effort := range []string{"max", "none", "minimal"} {
					t.Run(fmt.Sprintf("%s/%s/passthrough=%t/%s", model, kind, passthrough, effort), func(t *testing.T) {
						requested := "public"
						if passthrough {
							requested = "gpt-5.4"
						}
						account := gpt62ForwardValidationAccount(kind, passthrough, requested, "gpt-5.4")
						account.Credentials["compact_model_mapping"] = map[string]any{"gpt-5.4": model}
						body := []byte(fmt.Sprintf(`{"model":%q,"input":"hi","instructions":"compact instructions","reasoning":{"effort":%q,"mode":"pro"}}`, requested, effort))
						recorder := httptest.NewRecorder()
						c, _ := gin.CreateTestContext(recorder)
						c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses/compact", strings.NewReader(string(body)))
						upstream := &httpUpstreamRecorder{resp: &http.Response{
							StatusCode: http.StatusOK,
							Header:     http.Header{"Content-Type": []string{"application/json"}},
							Body:       io.NopCloser(strings.NewReader(fmt.Sprintf(`{"id":"resp_compact","status":"completed","model":%q,"output":[],"usage":{"input_tokens":1,"output_tokens":1}}`, model))),
						}}
						svc := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig(), httpUpstream: upstream}
						result, err := svc.Forward(context.Background(), c, account, body)
						if effort != "max" {
							require.ErrorContains(t, err, "does not support reasoning effort")
							require.Nil(t, result)
							require.Equal(t, http.StatusBadRequest, recorder.Code)
							require.Empty(t, upstream.requests)
							return
						}
						require.NoError(t, err)
						require.Equal(t, http.StatusOK, recorder.Code)
						require.Len(t, upstream.requests, 1)
						require.True(t, strings.HasSuffix(upstream.lastReq.URL.Path, "/responses/compact"))
						require.Equal(t, model, gjson.GetBytes(upstream.lastBody, "model").String())
						require.JSONEq(t, gjson.GetBytes(body, "reasoning").Raw, gjson.GetBytes(upstream.lastBody, "reasoning").Raw)
					})
				}
			}
		}
	}
}

func TestGPT62ForwardCompactAllowsLegacyTargetEffort(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, model := range []string{"gpt-6.2-sol", "gpt-6.2-astra"} {
		for _, kind := range []string{AccountTypeAPIKey, AccountTypeOAuth} {
			for _, passthrough := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/passthrough=%t", model, kind, passthrough), func(t *testing.T) {
					account := gpt62ForwardValidationAccount(kind, passthrough, model, model)
					account.Credentials["compact_model_mapping"] = map[string]any{model: "gpt-5.4"}
					body := []byte(fmt.Sprintf(`{"model":%q,"input":"hi","instructions":"compact instructions","reasoning":{"effort":"none"}}`, model))
					recorder := httptest.NewRecorder()
					c, _ := gin.CreateTestContext(recorder)
					c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses/compact", strings.NewReader(string(body)))
					upstream := &httpUpstreamRecorder{resp: &http.Response{
						StatusCode: http.StatusOK,
						Header:     http.Header{"Content-Type": []string{"application/json"}},
						Body:       io.NopCloser(strings.NewReader(`{"id":"resp_legacy_compact","status":"completed","model":"gpt-5.4","output":[],"usage":{"input_tokens":1,"output_tokens":1}}`)),
					}}
					svc := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig(), httpUpstream: upstream}
					result, err := svc.Forward(context.Background(), c, account, body)
					require.NoError(t, err)
					require.Equal(t, http.StatusOK, recorder.Code)
					require.Len(t, upstream.requests, 1)
					require.Equal(t, "gpt-5.4", result.UpstreamModel)
					require.Equal(t, "gpt-5.4", gjson.GetBytes(upstream.lastBody, "model").String())
				})
			}
		}
	}
}
