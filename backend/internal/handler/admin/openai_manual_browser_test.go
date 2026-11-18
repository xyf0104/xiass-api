package admin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestManualBrowserRequiresAdminAndOwnedSession(t *testing.T) {
	gin.SetMode(gin.TestMode)
	id := strings.Repeat("a", 32)
	h := &OpenAIOAuthHandler{teamBrowserStore: newOpenAITeamBrowserStore()}
	require.NoError(t, h.teamBrowserStore.setManualOwner(t.Context(), 42, persistedManualBrowserOwner{SessionID: id}, time.Hour))
	for _, test := range []struct {
		name string
		user int64
		role string
		code int
	}{
		{"anonymous", 0, "", 401},
		{"ordinary user", 42, "user", 403},
		{"another admin", 99, "admin", 404},
	} {
		t.Run(test.name, func(t *testing.T) {
			router := gin.New()
			router.Use(func(c *gin.Context) {
				if test.user > 0 {
					c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: test.user})
					c.Set(string(middleware.ContextKeyUserRole), test.role)
				}
			})
			router.GET("/sessions/:id/screenshot", h.ManualBrowserScreenshot)
			router.POST("/sessions/:id/:action", h.ManualBrowserCommand)
			for _, method := range []string{http.MethodGet, http.MethodPost} {
				endpoint := "/sessions/" + id + "/screenshot"
				if method == http.MethodPost {
					endpoint = "/sessions/" + id + "/action"
				}
				recorder := httptest.NewRecorder()
				router.ServeHTTP(recorder, httptest.NewRequest(method, endpoint, strings.NewReader(`{"type":"type","text":"private"}`)))
				require.Equal(t, test.code, recorder.Code)
			}
		})
	}
}

func TestManualBrowserLifecyclePreservesOwnerAndForwardsTabs(t *testing.T) {
	gin.SetMode(gin.TestMode)
	id := strings.Repeat("b", 32)
	expires := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	var actions []string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "test-browser-token", r.Header.Get("X-XIASS-Team-Child-Token"))
		require.Equal(t, "42", r.URL.Query().Get("owner_id"))
		actions = append(actions, r.Method+" "+r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost && r.URL.Path == "/manual-browser/sessions" {
			var body manualBrowserAutomationSessionRequest
			require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
			require.Equal(t, int64(42), body.OwnerID)
			require.Nil(t, body.Proxy)
		}
		if strings.HasSuffix(r.URL.Path, "/screenshot") {
			w.Header().Set("Content-Type", "image/jpeg")
			_, _ = w.Write([]byte{0xff, 0xd8, 0xff})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"session_id": id, "expires_at": expires, "egress_ip": "203.0.113.9", "tabs": []any{}})
	}))
	defer upstream.Close()
	t.Setenv("TEAM_CHILD_BROWSER_ENABLED", "true")
	t.Setenv("TEAM_CHILD_BROWSER_UPSTREAM_URL", upstream.URL)
	t.Setenv("TEAM_CHILD_AUTOMATION_URL", upstream.URL)
	t.Setenv("TEAM_CHILD_AUTOMATION_TOKEN", "test-browser-token")
	h := &OpenAIOAuthHandler{teamBrowserStore: newOpenAITeamBrowserStore()}
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 42})
		c.Set(string(middleware.ContextKeyUserRole), "admin")
	})
	router.POST("/session", h.CreateManualBrowserSession)
	router.GET("/session", h.ManualBrowserSession)
	router.DELETE("/session", h.EndManualBrowserSession)
	router.GET("/sessions/:id/tabs", h.ManualBrowserCommand)
	router.GET("/sessions/:id/screenshot", h.ManualBrowserScreenshot)
	for _, test := range []struct{ method, path string }{
		{http.MethodPost, "/session"}, {http.MethodGet, "/session"},
		{http.MethodGet, "/sessions/" + id + "/tabs"}, {http.MethodGet, "/sessions/" + id + "/screenshot"},
		{http.MethodDelete, "/session"},
	} {
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, httptest.NewRequest(test.method, test.path, strings.NewReader(`{}`)))
		require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
		require.NotContains(t, recorder.Body.String(), "test-browser-token")
	}
	require.Contains(t, actions, "GET /manual-browser/sessions/"+id+"/tabs")
	_, found, err := h.teamBrowserStore.getManualOwner(t.Context(), 42)
	require.NoError(t, err)
	require.False(t, found)
}
