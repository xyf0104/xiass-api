package routes

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/handler"
	servermiddleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestAuthRoutesRegistrationIPUsesTrustedProxyChain(t *testing.T) {
	for _, tc := range []struct {
		name      string
		trusted   []string
		peer      string
		forwarded string
		want      string
	}{
		{"untrusted header", nil, "192.0.2.10:1234", "198.51.100.1", "192.0.2.10"},
		{"trusted proxy", []string{"127.0.0.1/32"}, "127.0.0.1:1234", "198.51.100.1", "198.51.100.1"},
		{"spoofed prefix", []string{"127.0.0.1/32"}, "127.0.0.1:1234", "203.0.113.1, 198.51.100.1", "198.51.100.1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			router := gin.New()
			require.NoError(t, router.SetTrustedProxies(tc.trusted))
			var got service.RegistrationLimits
			RegisterAuthRoutes(router.Group("/api/v1"), &handler.Handlers{
				Auth: &handler.AuthHandler{}, Setting: &handler.SettingHandler{},
			}, servermiddleware.JWTAuthMiddleware(func(c *gin.Context) { c.Next() }),
				servermiddleware.AuditLogMiddleware(func(c *gin.Context) {
					got = service.RegistrationLimitsFromContext(c.Request.Context())
					c.AbortWithStatus(http.StatusNoContent)
				}), nil, nil, nil)
			req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/register", nil)
			req.RemoteAddr = tc.peer
			req.Header.Set("X-Forwarded-For", tc.forwarded)
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)
			require.Equal(t, http.StatusNoContent, rec.Code)
			require.Equal(t, tc.want, got.ClientIP)
		})
	}
}
