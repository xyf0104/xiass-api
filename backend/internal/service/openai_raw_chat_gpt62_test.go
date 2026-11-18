//go:build unit

package service

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestRawChatGPT62ToolCallsRejectedBeforeForward(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, model := range []string{"gpt-6.2-sol", "gpt-6.2-astra"} {
		for _, field := range []string{"tools", "functions"} {
			for _, mapped := range []bool{false, true} {
				name := model + "/" + field
				if mapped {
					name += "/alias"
				}
				t.Run(name, func(t *testing.T) {
					account := rawChatCompletionsTestAccount()
					requestedModel := model
					if mapped {
						requestedModel = "customer-model"
						account.Credentials["model_mapping"] = map[string]any{requestedModel: model}
					}
					function := map[string]any{"name": "lookup", "parameters": map[string]any{"type": "object"}}
					tool := function
					if field == "tools" {
						tool = map[string]any{"type": "function", "function": function}
					}
					body, err := json.Marshal(map[string]any{
						"model":    requestedModel,
						"messages": []map[string]string{{"role": "user", "content": "look up an item"}},
						field:      []map[string]any{tool},
					})
					require.NoError(t, err)
					w := httptest.NewRecorder()
					c, _ := gin.CreateTestContext(w)
					c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
					upstream := &httpUpstreamRecorder{}
					svc := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig(), httpUpstream: upstream}
					result, err := svc.forwardAsRawChatCompletions(context.Background(), c, account, body, "")
					require.Error(t, err)
					require.Nil(t, result)
					require.Equal(t, http.StatusBadRequest, w.Code)
					require.Contains(t, w.Body.String(), model+" requires Responses")
					require.Nil(t, upstream.lastReq)
				})
			}
		}
	}
}
