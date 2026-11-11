package service

import (
	"net/http"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestAccountTestAntigravityForwardsAdministratorPrompt(t *testing.T) {
	upstream := &queuedHTTPUpstreamStub{responses: []*http.Response{antigravityCompatSuccessResponse()}}
	svc := &AccountTestService{antigravityGatewayService: newAntigravityCompatService(
		config.GatewayConfig{MaxLineSize: defaultMaxLineSize}, upstream)}
	account := newAntigravityTierAccount()
	c := newAntigravityTierContext("/api/v1/admin/accounts/1/test", nil)
	require.NoError(t, svc.routeAntigravityTest(c, account, "gemini-3.7-flash-high", "Reply with the marker: admin-prompt-73"))
	require.Len(t, upstream.requestBodies, 1)
	require.Equal(t, "Reply with the marker: admin-prompt-73",
		gjson.GetBytes(upstream.requestBodies[0], "request.contents.0.parts.0.text").String())
}
