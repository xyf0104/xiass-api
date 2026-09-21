package service

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestCodexTicketFinalBuildersShareStateWithoutChangingBusinessProxy(t *testing.T) {
	state := fakeCodexTicketState(292)
	for _, transport := range []string{"http", "passthrough", "websocket"} {
		t.Run(transport, func(t *testing.T) {
			account := ticketTestAccount(41)
			account.RequestProxy = &Proxy{ID: 84, Protocol: "socks5", Host: "127.0.0.1", Port: 19080}
			svc := ticketTestService(t, config.OpenAICodexTicketConfig{FailClosed: true}, nil)
			svc.storeOpenAICodexTicket(context.Background(), account, &openAICodexTicket{
				Model: "gpt-6-astra", ObservedModel: "gpt-6-astra", State: state, Length: len(state),
				ProxyID: 91, CapturedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour),
			})
			body := []byte(`{"model":"gpt-6-astra","stream":true,"store":false,"input":"ping"}`)
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
			c.Request.Header.Set(openAIWSTurnStateHeader, "client-owned-old-state")
			var headers http.Header
			if transport == "websocket" {
				var err error
				headers, _, err = svc.buildOpenAIWSHeaders(context.Background(), c, account, "test-token",
					OpenAIWSProtocolDecision{Transport: OpenAIUpstreamTransportResponsesWebsocketV2}, true,
					"", "", "", "gpt-6-astra", "")
				require.NoError(t, err)
			} else {
				var request *http.Request
				var err error
				if transport == "passthrough" {
					request, err = svc.buildUpstreamRequestOpenAIPassthrough(context.Background(), c, account, body, "test-token")
				} else {
					request, err = svc.buildUpstreamRequest(context.Background(), c, account, body, "test-token", true, "", true)
				}
				require.NoError(t, err)
				headers = request.Header
				svc.httpUpstream = &codexTicketFuncUpstream{do: func(sent *http.Request) (*http.Response, error) {
					require.Equal(t, state, sent.Header.Get(openAIWSTurnStateHeader))
					payload, err := io.ReadAll(sent.Body)
					require.NoError(t, err)
					require.JSONEq(t, string(body), string(payload))
					return codexTicketResponse(), nil
				}}
				response, err := svc.doOpenAIUpstream(request, account.requestProxyURL(), account)
				require.NoError(t, err)
				require.NoError(t, response.Body.Close())
			}
			require.Equal(t, state, headers.Get(openAIWSTurnStateHeader))
			audit := inspectCodexTicketDispatch(account, headers, account.requestProxyURL())
			require.True(t, audit.Enabled)
			require.True(t, audit.Present)
			require.True(t, audit.ProxyMatches)
			require.Equal(t, int64(84), audit.ProxyID)
			require.Equal(t, len(state), audit.Length)
			require.Len(t, audit.Fingerprint, 16)
			require.NotContains(t, fmt.Sprint(audit), state)
			require.NotContains(t, fmt.Sprint(audit), "test-token")
		})
	}
}

func TestCodexTicketDispatchDoesNotExposeDisabledAccountClientState(t *testing.T) {
	account := ticketTestAccount(42)
	account.Extra[OpenAICodexTicketEnabledExtraKey] = false
	audit := inspectCodexTicketDispatch(account, http.Header{"X-Codex-Turn-State": {"private-client-state"}}, "")
	require.Equal(t, codexTicketDispatchSummary{}, audit)
	require.Equal(t, codexTicketDispatchSummary{}, inspectCodexTicketDispatch(nil, nil, ""))
}
