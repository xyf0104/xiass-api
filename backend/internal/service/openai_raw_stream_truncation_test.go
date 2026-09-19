package service

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func newRawStreamTruncationContext(t *testing.T) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	return context, recorder
}

func newRawStreamTruncationResponse(body string) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header: http.Header{
			"Content-Type": []string{"text/event-stream"},
			"X-Request-Id": []string{"rid-raw-truncation"},
		},
		Body: io.NopCloser(strings.NewReader(body)),
	}
}

func newRawStreamTruncationAccount() *Account {
	return &Account{ID: 73, Name: "raw-upstream", Platform: PlatformOpenAI, Type: AccountTypeAPIKey}
}

func TestRawChatStreamEmptyBeforeOutputFailsOver(t *testing.T) {
	context, recorder := newRawStreamTruncationContext(t)
	service := &OpenAIGatewayService{}

	result, err := service.streamRawChatCompletions(context, newRawStreamTruncationResponse(""), newRawStreamTruncationAccount(), "model", "model", "model", nil, nil, time.Now(), 0)

	require.Nil(t, result)
	var failoverErr *UpstreamFailoverError
	require.ErrorAs(t, err, &failoverErr)
	require.Equal(t, http.StatusBadGateway, failoverErr.StatusCode)
	require.Equal(t, OpenAIUpstreamStreamTruncatedCode, gjson.GetBytes(failoverErr.ResponseBody, "error.code").String())
	require.True(t, failoverErr.ShouldRetryNextAccount())
	require.False(t, context.Writer.Written())
	require.Empty(t, recorder.Body.String())
}

func TestRawChatStreamTruncatedAfterOutputIsClassified(t *testing.T) {
	context, recorder := newRawStreamTruncationContext(t)
	service := &OpenAIGatewayService{}
	body := "data: {\"id\":\"chatcmpl-cut\",\"choices\":[{\"delta\":{\"content\":\"partial\"},\"finish_reason\":null}]}\n\n"

	result, err := service.streamRawChatCompletions(context, newRawStreamTruncationResponse(body), newRawStreamTruncationAccount(), "model", "model", "model", nil, nil, time.Now(), 0)

	require.NotNil(t, result)
	require.Error(t, err)
	var failoverErr *UpstreamFailoverError
	require.False(t, errors.As(err, &failoverErr))
	code, _, ok := OpenAIUpstreamStreamReadErrorDetails(err)
	require.True(t, ok)
	require.Equal(t, OpenAIUpstreamStreamTruncatedCode, code)
	require.Contains(t, recorder.Body.String(), `"content":"partial"`)
}

func TestRawChatStreamFinishReasonWithoutDoneSucceeds(t *testing.T) {
	context, _ := newRawStreamTruncationContext(t)
	service := &OpenAIGatewayService{}
	body := "data: {\"id\":\"chatcmpl-done\",\"choices\":[{\"delta\":{\"content\":\"done\"},\"finish_reason\":\"stop\"}]}\n\n"

	result, err := service.streamRawChatCompletions(context, newRawStreamTruncationResponse(body), newRawStreamTruncationAccount(), "model", "model", "model", nil, nil, time.Now(), 0)

	require.NoError(t, err)
	require.NotNil(t, result)
}
