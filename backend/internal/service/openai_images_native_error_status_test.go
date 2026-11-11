package service

import (
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestOpenAIImagesJSONKeepalive_CodexLateErrorPreservesStatus(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, path := range []string{"/v1/images/generations", "/v1/images/edits"} {
		t.Run(path, func(t *testing.T) {
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, path, nil)
			c.Request.Header.Set("X-Codex-Image-Turn-Id", "native-image-turn")
			stop := StartOpenAIImagesJSONKeepalive(c, 5*time.Millisecond)
			defer stop()

			// Reproduce an error arriving after a normal heartbeat would have
			// committed HTTP 200. Native Codex must see the real error status.
			time.Sleep(20 * time.Millisecond)
			wrote := writeOpenAIImagesUpstreamErrorResponse(c, &OpenAIImagesUpstreamError{
				StatusCode: http.StatusTooManyRequests,
				ErrorType:  "rate_limit_error",
				Code:       "image_rate_limit",
				Message:    "image quota exceeded",
			})
			require.True(t, wrote)
			require.Equal(t, http.StatusTooManyRequests, rec.Code)
			require.False(t, rec.Flushed)
			require.Equal(t, "image_rate_limit", gjson.Get(rec.Body.String(), "error.code").String())
			require.Equal(t, "image quota exceeded", gjson.Get(rec.Body.String(), "error.message").String())
		})
	}
}
