package handler

import (
	"net/http"
	"net/http/httptest"
	"testing"

	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestGatewayModels_PassthroughKeepsConfiguredNewModels(t *testing.T) {
	gin.SetMode(gin.TestMode)
	groupID := int64(14)
	h := newGatewayModelsHandlerForTest(&gatewayModelsAccountRepoStub{byGroup: map[int64][]service.Account{
		groupID: {
			{ID: 1, Platform: service.PlatformOpenAI, Extra: map[string]any{"openai_passthrough": true},
				Credentials: map[string]any{"model_mapping": map[string]any{"stale-alias": "missing-model"}}},
			{ID: 2, Platform: service.PlatformOpenAI,
				Credentials: map[string]any{"model_mapping": map[string]any{"gpt-6.1-sol": "gpt-6.1-sol"}}},
		},
		15: {{ID: 3, Platform: service.PlatformOpenAI,
			Credentials: map[string]any{"model_mapping": map[string]any{"gpt-6-sol": "gpt-6-sol"}}}},
	}})
	for _, tc := range []struct {
		name       string
		groupID    int64
		allowlist  service.GroupModelAllowlist
		model      string
		wantStatus int
		wantNew    bool
		wantOld    bool
	}{
		{name: "mixed group", groupID: groupID, wantStatus: http.StatusOK, wantNew: true, wantOld: true},
		{name: "allowlist preserves selected new model", groupID: groupID,
			allowlist: service.GroupModelAllowlist{Enabled: true, Models: []string{"gpt-6.1-sol"}}, wantStatus: http.StatusOK, wantNew: true},
		{name: "allowlist still excludes new model", groupID: groupID,
			allowlist: service.GroupModelAllowlist{Enabled: true, Models: []string{"gpt-6-sol"}}, wantStatus: http.StatusOK, wantOld: true},
		{name: "other group is isolated", groupID: 15, wantStatus: http.StatusOK, wantOld: true},
		{name: "retrieve new model", groupID: groupID, model: "gpt-6.1-sol", wantStatus: http.StatusOK},
		{name: "retrieve respects allowlist", groupID: groupID, model: "gpt-6.1-sol",
			allowlist: service.GroupModelAllowlist{Enabled: true, Models: []string{"gpt-6-sol"}}, wantStatus: http.StatusNotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			group := &service.Group{ID: tc.groupID, Platform: service.PlatformOpenAI, ModelAllowlist: tc.allowlist}
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodGet, "/v1/models", nil)
			if tc.model != "" {
				c.Params = gin.Params{{Key: "model", Value: tc.model}}
			}
			c.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{GroupID: &group.ID, Group: group})
			h.Models(c)
			require.Equal(t, tc.wantStatus, recorder.Code, recorder.Body.String())
			if tc.model != "" {
				if tc.wantStatus == http.StatusOK {
					require.Contains(t, recorder.Body.String(), `"id":"gpt-6.1-sol"`)
				}
				return
			}
			ids := ordinaryPinnedModelIDs(t, recorder)
			require.NotContains(t, ids, "stale-alias")
			if tc.wantNew {
				require.Contains(t, ids, "gpt-6.1-sol")
			} else {
				require.NotContains(t, ids, "gpt-6.1-sol")
			}
			if tc.wantOld {
				require.Contains(t, ids, "gpt-6-sol")
			} else {
				require.NotContains(t, ids, "gpt-6-sol")
			}
		})
	}
}
