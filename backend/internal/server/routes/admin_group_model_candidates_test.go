package routes

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/handler"
	adminhandler "github.com/Wei-Shaw/sub2api/internal/handler/admin"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestRegisterGroupRoutesModelCandidates(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	// Invalid IDs must reach the real handlers and return before any service call.
	handlers := &handler.Handlers{Admin: &handler.AdminHandlers{Group: adminhandler.NewGroupHandler(nil, nil, nil)}}
	registerGroupRoutes(router.Group("/api/v1/admin"), handlers, nil)

	for _, endpoint := range []string{"models-list-candidates", "model-allowlist-candidates"} {
		for _, id := range []string{"invalid", "-1"} {
			t.Run(endpoint+"/"+id, func(t *testing.T) {
				path := "/api/v1/admin/groups/" + id + "/" + endpoint
				recorder := httptest.NewRecorder()
				router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))

				require.Equal(t, http.StatusBadRequest, recorder.Code, "GET %s: %s", path, recorder.Body.String())
				require.JSONEq(t, `{"code":400,"message":"Invalid group ID"}`, recorder.Body.String())
			})
		}
	}
}
