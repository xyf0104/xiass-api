package routes

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/handler/admin"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestPelicanRoutesInheritAdminAuthentication(t *testing.T) {
	r := gin.New()
	group := r.Group("/api/v1/admin")
	group.Use(func(c *gin.Context) { c.AbortWithStatus(http.StatusUnauthorized) })
	registerPelicanBenchmarkRoutes(group, &handler.Handlers{Admin: &handler.AdminHandlers{PelicanBenchmark: &admin.PelicanBenchmarkHandler{}}})
	for _, route := range []struct{ method, path string }{
		{"GET", "/pelican-benchmarks"}, {"POST", "/pelican-benchmarks"}, {"GET", "/pelican-benchmarks/test"},
		{"POST", "/pelican-benchmarks/test/stop"}, {"POST", "/pelican-benchmarks/stop"}, {"POST", "/accounts/1/pelican-benchmark"},
	} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(route.method, "/api/v1/admin"+route.path, nil))
		require.Equal(t, http.StatusUnauthorized, w.Code, route.path)
	}
}
