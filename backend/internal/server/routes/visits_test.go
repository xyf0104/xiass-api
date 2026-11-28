//go:build unit

package routes

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestOptionalVisitAuth(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		header string
		status int
	}{
		{"", http.StatusNoContent},
		{"Bearer valid", http.StatusNoContent},
		{"Bearer invalid", http.StatusUnauthorized},
	} {
		t.Run(tc.header, func(t *testing.T) {
			authCalls, handlerCalls := 0, 0
			auth := middleware.JWTAuthMiddleware(func(c *gin.Context) {
				authCalls++
				if c.GetHeader("Authorization") != "Bearer valid" {
					c.AbortWithStatus(http.StatusUnauthorized)
					return
				}
				c.Next()
			})
			r := gin.New()
			r.POST("/visits", optionalVisitAuth(auth), func(c *gin.Context) {
				handlerCalls++
				c.Status(http.StatusNoContent)
			})
			req := httptest.NewRequest(http.MethodPost, "/visits", nil)
			req.Header.Set("Authorization", tc.header)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			require.Equal(t, tc.status, w.Code)
			if tc.header == "" {
				require.Zero(t, authCalls)
			} else {
				require.Equal(t, 1, authCalls)
			}
			if tc.status == http.StatusNoContent {
				require.Equal(t, 1, handlerCalls)
			} else {
				require.Zero(t, handlerCalls)
			}
		})
	}
}
