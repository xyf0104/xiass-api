package routes

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/handler/admin"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestAccountPoolRoutesInheritAdminAuthentication(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	group := r.Group("/api/v1/admin")
	group.Use(gin.HandlerFunc(middleware.NewAdminAuthMiddleware(nil, nil, nil, nil)))
	registerAccountPoolRoutes(group, &handler.Handlers{Admin: &handler.AdminHandlers{Account: &admin.AccountHandler{}}})
	for _, route := range []struct{ method, path string }{
		{http.MethodGet, "/account-pools"}, {http.MethodPost, "/account-pools"},
		{http.MethodPut, "/account-pools/1"}, {http.MethodDelete, "/account-pools/1"},
		{http.MethodPost, "/account-pools/1/accounts"}, {http.MethodPut, "/account-pools/1/proxy"},
	} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(route.method, "/api/v1/admin"+route.path, nil))
		require.Equal(t, http.StatusUnauthorized, w.Code, route.path)
	}
}
