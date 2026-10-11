package service

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// Covers upstream 8b95dc94f without bypassing XIASS ticket and identity handling.
func TestOpenAIBuildUpstreamRequestCallerBeta(t *testing.T) {
	tests := []struct {
		name        string
		accountType string
		path        string
		beta        []string
		want        []string
	}{
		{"multi_agent", AccountTypeOAuth, "/v1/responses", []string{"responses_multi_agent=v1"}, []string{"responses_multi_agent=v1"}},
		{"mixed", AccountTypeOAuth, "/v1/responses", []string{"responses_multi_agent=v1, responses=experimental", "future_feature=v2"}, []string{"responses_multi_agent=v1", "future_feature=v2"}},
		{"absent", AccountTypeOAuth, "/v1/responses", nil, nil},
		{"api_key", AccountTypeAPIKey, "/v1/responses", []string{"responses=experimental, responses_multi_agent=v1"}, []string{"responses=experimental, responses_multi_agent=v1"}},
		{"compact", AccountTypeOAuth, "/v1/responses/compact", []string{"responses=experimental, future_feature=v1"}, []string{"future_feature=v1"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := []byte(`{"model":"gpt-6.1-sol","input":"hello"}`)
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, tt.path, bytes.NewReader(body))
			for _, value := range tt.beta {
				c.Request.Header.Add("OpenAI-Beta", value)
			}
			account := &Account{Platform: PlatformOpenAI, Type: tt.accountType, Credentials: map[string]any{"chatgpt_account_id": "test-account", "api_key": "test-only"}}
			req, err := (&OpenAIGatewayService{}).buildUpstreamRequest(c.Request.Context(), c, account, body, "test-token", true, "", false)
			require.NoError(t, err)
			require.Equal(t, tt.want, req.Header.Values("OpenAI-Beta"))
		})
	}
}
