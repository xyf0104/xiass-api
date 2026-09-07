package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestCodexNormalizations_AllowedToolsPreservedAndAliased(t *testing.T) {
	for _, name := range []string{"lookup", "python"} {
		for _, placement := range []string{"tools", "additional_tools"} {
			for _, mode := range []string{"auto", "required"} {
				t.Run(name+"/"+placement+"/"+mode, func(t *testing.T) {
					declaration := map[string]any{"type": "function", "name": name}
					reference := map[string]any{"type": "function", "name": name}
					choice := map[string]any{"type": "allowed_tools", "mode": mode, "tools": []any{reference}}
					body := map[string]any{"model": "gpt-6-astra", "tool_choice": choice,
						"instructions": "client", "tools": []any{map[string]any{"type": "web_search"}}}
					if placement == "tools" {
						declared, ok := body["tools"].([]any)
						require.True(t, ok)
						body["tools"] = append(declared, declaration)
					} else {
						body["input"] = []any{map[string]any{"type": "additional_tools", "role": "developer", "tools": []any{declaration}}}
					}
					result := applyCodexOAuthTransform(body, true, false)
					require.NoError(t, result.Error)
					require.Equal(t, choice, body["tool_choice"])
					require.Equal(t, mode, choice["mode"])
					require.Equal(t, declaration["name"], reference["name"])
					if name == "python" {
						require.Equal(t, codexPythonToolAlias, reference["name"])
						require.Equal(t, name, result.ToolNameReverse[codexPythonToolAlias])
					} else {
						require.Equal(t, name, reference["name"])
					}
					require.Equal(t, "client", body["instructions"])
				})
			}
		}
	}
}

func TestCodexNormalizations_InvalidAllowedToolsNeverWidened(t *testing.T) {
	for _, choice := range []map[string]any{
		{"type": "allowed_tools"},
		{"type": "allowed_tools", "mode": "invalid", "tools": []any{}},
		{"type": "allowed_tools", "mode": "required", "tools": "invalid"},
		{"type": "allowed_tools", "tools": []any{map[string]any{"type": "function", "name": "missing"}}},
	} {
		body := map[string]any{"tool_choice": choice}
		before, err := json.Marshal(body)
		require.NoError(t, err)
		require.False(t, normalizeCodexToolChoice(body))
		after, err := json.Marshal(body)
		require.NoError(t, err)
		require.Equal(t, before, after)
	}
}

func TestCodexNormalizations_ForwardFinalModelInstructionsAndAPIKeyBoundary(t *testing.T) {
	for _, accountType := range []string{AccountTypeOAuth, AccountTypeAPIKey} {
		for _, model := range []string{"gpt-6-astra", "gpt-5.2"} {
			for _, explicit := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/explicit=%t", accountType, model, explicit), func(t *testing.T) {
					rec := httptest.NewRecorder()
					c, _ := gin.CreateTestContext(rec)
					c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
					c.Request.Header.Set("User-Agent", "curl/8.0")
					upstream := &httpUpstreamRecorder{resp: &http.Response{StatusCode: 200,
						Header: http.Header{"Content-Type": []string{"text/event-stream"}},
						Body:   io.NopCloser(strings.NewReader("data: {\"type\":\"response.completed\",\"response\":{\"id\":\"r1\",\"status\":\"completed\",\"output\":[],\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}}\n\ndata: [DONE]\n\n"))}}
					svc := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: upstream}
					account := &Account{ID: 123, Platform: PlatformOpenAI, Type: accountType, Concurrency: 1,
						Credentials: map[string]any{"access_token": "synthetic", "api_key": "synthetic", "chatgpt_account_id": "synthetic",
							"model_mapping": map[string]any{"public-alias": model}},
						Extra:  map[string]any{"openai_oauth_responses_websockets_v2_mode": OpenAIWSIngressModeOff},
						Status: StatusActive, Schedulable: true, RateMultiplier: f64p(1)}
					body := map[string]any{"model": "public-alias", "stream": true, "input": "hi",
						"tools":       []any{map[string]any{"type": "function", "name": "python", "parameters": map[string]any{"type": "object"}}},
						"tool_choice": map[string]any{"type": "allowed_tools", "mode": "required", "tools": []any{map[string]any{"type": "function", "name": "python"}}}}
					if explicit {
						body["instructions"] = "  client-owned instructions\n"
					}
					encoded, err := json.Marshal(body)
					require.NoError(t, err)
					_, err = svc.Forward(context.Background(), c, account, encoded)
					require.NoError(t, err)
					require.Equal(t, model, gjson.GetBytes(upstream.lastBody, "model").String())
					instructions := gjson.GetBytes(upstream.lastBody, "instructions")
					if explicit {
						require.Equal(t, body["instructions"], instructions.String())
					} else if accountType == AccountTypeOAuth {
						require.Equal(t, strings.TrimSpace(openai.CodexBaseInstructionsForModel(model)), strings.TrimSpace(instructions.String()))
					} else {
						require.False(t, instructions.Exists())
					}
					wantName := "python"
					if accountType == AccountTypeOAuth {
						wantName = codexPythonToolAlias
					}
					require.Equal(t, "allowed_tools", gjson.GetBytes(upstream.lastBody, "tool_choice.type").String())
					require.Equal(t, wantName, gjson.GetBytes(upstream.lastBody, "tool_choice.tools.0.name").String())
					require.Equal(t, wantName, gjson.GetBytes(upstream.lastBody, "tools.0.name").String())
				})
			}
		}
	}
}

func TestCodexNormalizations_TransformAstraInstructions(t *testing.T) {
	for _, cli := range []bool{false, true} {
		body := map[string]any{"model": "gpt-6-astra"}
		result := applyCodexOAuthTransform(body, cli, false)
		require.NoError(t, result.Error)
		instructions, ok := body["instructions"].(string)
		require.True(t, ok)
		require.True(t, strings.HasPrefix(instructions, "You are Codex, an agent based on GPT-6."))
	}
}

func TestCodexNormalizations_ReservedNameCollisionAndProtocolOnlyRewrite(t *testing.T) {
	body := map[string]any{"tools": []any{map[string]any{"type": "function", "name": "python"}},
		"tool_choice": map[string]any{"type": "allowed_tools", "tools": []any{map[string]any{"type": "function", "name": codexPythonToolAlias}}}}
	before, err := json.Marshal(body)
	require.NoError(t, err)
	_, changed, err := aliasOpenAIOAuthReservedToolNames(body)
	require.Error(t, err)
	require.False(t, changed)
	after, err := json.Marshal(body)
	require.NoError(t, err)
	require.Equal(t, before, after)

	raw := []byte(`{"tools":[{"type":"function","name":"python"}],"tool_choice":{"type":"allowed_tools","mode":"required","tools":[{"type":"function","function":{"name":"python"}}]},"input":[{"type":"function_call","name":"python","arguments":"python"}],"metadata":{"name":"python"},"sequence":900719925474099312345}`)
	aliased, reverse, changed, err := aliasOpenAIOAuthReservedToolNamesBody(raw)
	require.NoError(t, err)
	require.True(t, changed)
	for _, path := range []string{"tools.0.name", "tool_choice.tools.0.function.name", "input.0.name"} {
		require.Equal(t, codexPythonToolAlias, gjson.GetBytes(aliased, path).String())
	}
	require.Equal(t, "python", gjson.GetBytes(aliased, "metadata.name").String())
	require.Equal(t, "python", gjson.GetBytes(aliased, "input.0.arguments").String())
	require.Equal(t, "900719925474099312345", gjson.GetBytes(aliased, "sequence").Raw)
	restored := restoreCodexToolNamesInJSON([]byte(`{"type":"response.completed","response":{"output":[{"type":"function_call","name":"python__sub2api"}]},"metadata":{"name":"python__sub2api"},"sequence":900719925474099312345}`), reverse)
	require.Equal(t, "python", gjson.GetBytes(restored, "response.output.0.name").String())
	require.Equal(t, codexPythonToolAlias, gjson.GetBytes(restored, "metadata.name").String())
	require.Equal(t, "900719925474099312345", gjson.GetBytes(restored, "sequence").Raw)
}

func TestCodexNormalizations_WSSessionAndFailoverReverseIsolation(t *testing.T) {
	c, _ := gin.CreateTestContext(nil)
	reverse := map[string]string{codexPythonToolAlias: "python"}
	setCodexToolNameReverse(c, nil)
	updateCodexToolNameReverseForWSFrame(c, []byte(`{"type":"session.update","session":{"tools":[{"type":"function","name":"python"}]}}`), reverse)
	updateCodexToolNameReverseForWSFrame(c, []byte(`{"type":"response.create","input":"hi"}`), nil)
	event := []byte(`{"type":"response.output_item.added","item":{"type":"function_call","name":"python__sub2api"}}`)
	require.Equal(t, "python", gjson.GetBytes(restoreCodexToolNamesFromContext(c, event), "item.name").String())
	updateCodexToolNameReverseForWSFrame(c, []byte(`{"type":"session.update","session":{"tools":[]}}`), nil)
	require.Equal(t, "python", gjson.GetBytes(restoreCodexToolNamesFromContext(c, event), "item.name").String(), "session update must not rewrite the active turn")
	updateCodexToolNameReverseForWSFrame(c, []byte(`{"type":"response.create","input":"next"}`), nil)
	require.Equal(t, codexPythonToolAlias, gjson.GetBytes(restoreCodexToolNamesFromContext(c, event), "item.name").String())
	setCodexToolNameReverse(c, reverse)
	setCodexToolNameReverse(c, nil)
	require.Equal(t, event, restoreCodexToolNamesFromContext(c, event), "account failover clears reverse aliases")
}

func TestCodexNormalizations_EscapedReservedToolName(t *testing.T) {
	raw := []byte(`{"tools":[{"type":"function","name":"py\u0074hon"}],"tool_choice":{"type":"function","name":"\u0070ython"},"sequence":900719925474099312345}`)
	body, reverse, changed, err := aliasOpenAIOAuthReservedToolNamesBody(raw)
	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, codexPythonToolAlias, gjson.GetBytes(body, "tools.0.name").String())
	require.Equal(t, codexPythonToolAlias, gjson.GetBytes(body, "tool_choice.name").String())
	require.Equal(t, "python", reverse[codexPythonToolAlias])
	require.Equal(t, "900719925474099312345", gjson.GetBytes(body, "sequence").Raw)
}

func TestCodexNormalizations_WSSessionToolChoice(t *testing.T) {
	for _, choice := range []string{
		`{"type":"function","name":"python"}`,
		`{"type":"allowed_tools","mode":"required","tools":[{"type":"function","name":"python"}]}`,
	} {
		t.Run(choice, func(t *testing.T) {
			raw := []byte(`{"type":"session.update","session":{"tools":[{"type":"function","name":"python"}],"tool_choice":` + choice + `}}`)
			body, _, changed, err := aliasOpenAIOAuthReservedToolNamesBody(raw)
			require.NoError(t, err)
			require.True(t, changed)
			path := "session.tool_choice.name"
			if gjson.Get(choice, "type").String() == "allowed_tools" {
				path = "session.tool_choice.tools.0.name"
				require.Equal(t, "required", gjson.GetBytes(body, "session.tool_choice.mode").String())
			}
			require.Equal(t, codexPythonToolAlias, gjson.GetBytes(body, path).String())
		})
	}

	raw := []byte(`{"type":"session.update","session":{"tools":[{"type":"function","name":"python"}],"tool_choice":{"type":"function","name":"python__sub2api"}}}`)
	body, _, changed, err := aliasOpenAIOAuthReservedToolNamesBody(raw)
	require.Error(t, err)
	require.False(t, changed)
	require.Equal(t, raw, body)
}

func TestCodexNormalizations_SSETerminalWithoutInlineType(t *testing.T) {
	raw := []byte(`{"response":{"output":[{"type":"function_call","name":"python__sub2api","arguments":"python__sub2api"}]},"metadata":{"name":"python__sub2api"}}`)
	restored := restoreCodexToolNamesInJSON(raw, map[string]string{codexPythonToolAlias: "python"})
	require.Equal(t, "python", gjson.GetBytes(restored, "response.output.0.name").String())
	require.Equal(t, codexPythonToolAlias, gjson.GetBytes(restored, "response.output.0.arguments").String())
	require.Equal(t, codexPythonToolAlias, gjson.GetBytes(restored, "metadata.name").String())
}

func TestCodexNormalizations_PreserveAstraWorkflowAndFingerprintOff(t *testing.T) {
	for _, mode := range []string{"", "off"} {
		for _, passthrough := range []bool{false, true} {
			t.Run(fmt.Sprintf("mode=%s/passthrough=%t", mode, passthrough), func(t *testing.T) {
				rec := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(rec)
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
				upstream := &httpUpstreamRecorder{resp: &http.Response{StatusCode: http.StatusOK,
					Header: http.Header{"Content-Type": []string{"text/event-stream"}},
					Body:   io.NopCloser(strings.NewReader("data: {\"type\":\"response.completed\",\"response\":{\"id\":\"r1\",\"status\":\"completed\",\"output\":[],\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}}\n\ndata: [DONE]\n\n"))}}
				svc := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: upstream}
				account := &Account{ID: 123, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Concurrency: 1,
					Credentials: map[string]any{"access_token": "synthetic", "chatgpt_account_id": "synthetic"},
					Extra:       map[string]any{"openai_passthrough": passthrough, "openai_oauth_responses_websockets_v2_mode": OpenAIWSIngressModeOff},
					Status:      StatusActive, Schedulable: true, RateMultiplier: f64p(1)}
				if mode != "" {
					account.Extra[codexFingerprintModeExtraKey] = mode
				}
				body := []byte(`{"model":"gpt-6-astra","instructions":"client","stream":true,"reasoning":{"effort":"xhigh","mode":"pro"},"prompt_cache_key":"client-cache","client_metadata":{"x-codex-installation-id":"client-install","workflow":"ultra"},"tools":[{"type":"function","name":"python"}],"input":[{"type":"function_call_output","name":"delegate_summary","output":"delegated task"}]}`)
				_, err := svc.Forward(context.Background(), c, account, body)
				require.NoError(t, err)
				for _, path := range []string{"reasoning", "client_metadata", "prompt_cache_key", "input.0"} {
					require.JSONEq(t, gjson.GetBytes(body, path).Raw, gjson.GetBytes(upstream.lastBody, path).Raw, path)
				}
				require.Equal(t, codexPythonToolAlias, gjson.GetBytes(upstream.lastBody, "tools.0.name").String())
				require.Equal(t, codexFingerprintOff, account.GetCodexFingerprintMode())
				require.NotContains(t, account.Extra, codexFingerprintSeedExtraKey)
			})
		}
	}
}

func TestCodexNormalizations_NativeToolNameRoundTrip(t *testing.T) {
	for _, passthrough := range []bool{false, true} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("passthrough=%t/stream=%t", passthrough, stream), func(t *testing.T) {
				rec := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(rec)
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
				c.Request.Header.Set("User-Agent", "curl/8.0")
				response := "event: response.output_item.added\ndata: {\"item\":{\"id\":\"fc_one\",\"type\":\"function_call\",\"call_id\":\"call_one\",\"name\":\"python__sub2api\",\"arguments\":\"\"},\"output_index\":0}\n\n" +
					"data: {\"type\":\"response.function_call_arguments.done\",\"output_index\":0,\"arguments\":\"{}\"}\n\n" +
					"data: {\"type\":\"response.completed\",\"response\":{\"id\":\"r1\",\"status\":\"completed\",\"output\":[{\"type\":\"function_call\",\"call_id\":\"call_one\",\"name\":\"python__sub2api\",\"arguments\":\"{}\"}],\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}}\n\ndata: [DONE]\n\n"
				upstream := &httpUpstreamRecorder{resp: &http.Response{StatusCode: 200,
					Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(response))}}
				svc := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: upstream, toolCorrector: NewCodexToolCorrector()}
				account := &Account{ID: 123, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Concurrency: 1,
					Credentials: map[string]any{"access_token": "synthetic", "chatgpt_account_id": "synthetic"},
					Extra:       map[string]any{"openai_passthrough": passthrough, "openai_oauth_responses_websockets_v2_mode": OpenAIWSIngressModeOff},
					Status:      StatusActive, Schedulable: true, RateMultiplier: f64p(1)}
				body := []byte(fmt.Sprintf(`{"model":"gpt-6-astra","instructions":"client","stream":%t,"input":"hi","tools":[{"type":"function","name":"python","parameters":{"type":"object"}}],"tool_choice":{"type":"allowed_tools","mode":"required","tools":[{"type":"function","name":"python"}]}}`, stream))
				_, err := svc.Forward(context.Background(), c, account, body)
				require.NoError(t, err)
				require.Equal(t, codexPythonToolAlias, gjson.GetBytes(upstream.lastBody, "tools.0.name").String())
				require.Equal(t, codexPythonToolAlias, gjson.GetBytes(upstream.lastBody, "tool_choice.tools.0.name").String())
				require.Contains(t, rec.Body.String(), `"name":"python"`)
				require.NotContains(t, rec.Body.String(), `"name":"python__sub2api"`)
			})
		}
	}
}

func TestCodexNormalizations_CompactFinalModelInstructions(t *testing.T) {
	for _, passthrough := range []bool{false, true} {
		t.Run(fmt.Sprint(passthrough), func(t *testing.T) {
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses/compact", nil)
			upstream := &httpUpstreamRecorder{resp: &http.Response{StatusCode: 200,
				Header: http.Header{"Content-Type": []string{"application/json"}},
				Body:   io.NopCloser(strings.NewReader(`{"id":"cmp1","model":"gpt-6-astra","output":[],"usage":{"input_tokens":1,"output_tokens":1}}`))}}
			svc := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: upstream}
			account := &Account{ID: 123, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Concurrency: 1,
				Credentials: map[string]any{"access_token": "synthetic", "chatgpt_account_id": "synthetic",
					"compact_model_mapping": map[string]any{"gpt-5.3-codex": "gpt-6-astra"}},
				Extra:  map[string]any{"openai_passthrough": passthrough, "openai_oauth_responses_websockets_v2_mode": OpenAIWSIngressModeOff},
				Status: StatusActive, Schedulable: true, RateMultiplier: f64p(1)}
			_, err := svc.Forward(context.Background(), c, account, []byte(`{"model":"gpt-5.3-codex","input":"hi"}`))
			require.NoError(t, err)
			require.Equal(t, "gpt-6-astra", gjson.GetBytes(upstream.lastBody, "model").String())
			require.True(t, strings.HasPrefix(gjson.GetBytes(upstream.lastBody, "instructions").String(), "You are Codex, an agent based on GPT-6."))
		})
	}
}

func TestCodexNormalizations_WSForwardRoundTrip(t *testing.T) {
	for _, accountType := range []string{AccountTypeOAuth, AccountTypeAPIKey} {
		t.Run(accountType, func(t *testing.T) {
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
			SetOpenAIClientTransport(c, OpenAIClientTransportWS)
			cfg := &config.Config{}
			cfg.Gateway.OpenAIWS.Enabled = true
			cfg.Gateway.OpenAIWS.OAuthEnabled = true
			cfg.Gateway.OpenAIWS.APIKeyEnabled = true
			cfg.Gateway.OpenAIWS.ResponsesWebsocketsV2 = true
			cfg.Gateway.OpenAIWS.MaxConnsPerAccount = 1
			cfg.Gateway.OpenAIWS.MaxIdlePerAccount = 1
			cfg.Gateway.OpenAIWS.ReadTimeoutSeconds = 2
			cfg.Gateway.OpenAIWS.WriteTimeoutSeconds = 2
			name := "python"
			if accountType == AccountTypeOAuth {
				name = codexPythonToolAlias
			}
			conn := &openAIWSCaptureConn{events: [][]byte{[]byte(fmt.Sprintf(`{"type":"response.completed","response":{"id":"r1","status":"completed","output":[{"type":"function_call","call_id":"call_one","name":%q,"arguments":"{}"}],"usage":{"input_tokens":1,"output_tokens":1}}}`, name))}}
			pool := newOpenAIWSConnPool(cfg)
			pool.setClientDialerForTest(&openAIWSCaptureDialer{conn: conn})
			t.Cleanup(pool.Close)
			svc := &OpenAIGatewayService{cfg: cfg, cache: &stubGatewayCache{}, httpUpstream: &httpUpstreamRecorder{},
				openaiWSResolver: NewOpenAIWSProtocolResolver(cfg), openaiWSPool: pool, toolCorrector: NewCodexToolCorrector()}
			account := &Account{ID: 123, Platform: PlatformOpenAI, Type: accountType, Concurrency: 1,
				Credentials: map[string]any{"access_token": "synthetic", "api_key": "synthetic", "chatgpt_account_id": "synthetic"},
				Extra:       map[string]any{"responses_websockets_v2_enabled": true}, Status: StatusActive, Schedulable: true}
			_, err := svc.Forward(context.Background(), c, account, []byte(`{"model":"gpt-6-astra","instructions":"client","stream":false,"input":"hi","tools":[{"type":"function","name":"python","parameters":{"type":"object"}}],"tool_choice":{"type":"allowed_tools","mode":"required","tools":[{"type":"function","name":"python"}]}}`))
			require.NoError(t, err)
			conn.mu.Lock()
			written, marshalErr := json.Marshal(conn.lastWrite)
			conn.mu.Unlock()
			require.NoError(t, marshalErr)
			require.Equal(t, name, gjson.GetBytes(written, "tools.0.name").String())
			require.Equal(t, name, gjson.GetBytes(written, "tool_choice.tools.0.name").String())
			require.Equal(t, "python", gjson.GetBytes(rec.Body.Bytes(), "output.0.name").String())
		})
	}
}

func TestCodexNormalizations_CompatToolNameRoundTrip(t *testing.T) {
	for _, endpoint := range []string{"chat/completions", "messages"} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream=%t", endpoint, stream), func(t *testing.T) {
				rec := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(rec)
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/"+endpoint, nil)
				response := "data: {\"type\":\"response.output_item.added\",\"item\":{\"id\":\"fc_one\",\"type\":\"function_call\",\"call_id\":\"call_one\",\"name\":\"python__sub2api\",\"arguments\":\"\"},\"output_index\":0}\n\n" +
					"data: {\"type\":\"response.function_call_arguments.delta\",\"output_index\":0,\"delta\":\"{}\"}\n\n" +
					"data: {\"type\":\"response.function_call_arguments.done\",\"output_index\":0,\"arguments\":\"{}\"}\n\n" +
					"data: {\"type\":\"response.completed\",\"response\":{\"id\":\"r1\",\"status\":\"completed\",\"output\":[{\"type\":\"function_call\",\"call_id\":\"call_one\",\"name\":\"python__sub2api\",\"arguments\":\"{}\"}],\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}}\n\ndata: [DONE]\n\n"
				upstream := &httpUpstreamRecorder{resp: &http.Response{StatusCode: 200,
					Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(response))}}
				svc := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: upstream, cache: &stubGatewayCache{}, toolCorrector: NewCodexToolCorrector()}
				account := &Account{ID: 123, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Concurrency: 1,
					Credentials: map[string]any{"access_token": "synthetic", "chatgpt_account_id": "synthetic"},
					Status:      StatusActive, Schedulable: true, RateMultiplier: f64p(1)}
				var err error
				if endpoint == "chat/completions" {
					body := []byte(fmt.Sprintf(`{"model":"gpt-6-astra","stream":%t,"messages":[{"role":"user","content":"hi"}],"tools":[{"type":"function","function":{"name":"python","parameters":{"type":"object"}}}]}`, stream))
					_, err = svc.ForwardAsChatCompletions(context.Background(), c, account, body, "", "")
				} else {
					body := []byte(fmt.Sprintf(`{"model":"gpt-6-astra","max_tokens":100,"stream":%t,"messages":[{"role":"user","content":"hi"}],"tools":[{"name":"python","input_schema":{"type":"object"}}]}`, stream))
					_, err = svc.ForwardAsAnthropic(context.Background(), c, account, body, "", "")
				}
				require.NoError(t, err)
				require.Equal(t, codexPythonToolAlias, gjson.GetBytes(upstream.lastBody, "tools.0.name").String())
				require.Contains(t, rec.Body.String(), `"name":"python"`)
				require.NotContains(t, rec.Body.String(), `"name":"python__sub2api"`)
			})
		}
	}
}
