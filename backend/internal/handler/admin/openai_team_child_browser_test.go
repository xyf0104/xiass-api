package admin

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	redis "github.com/Wei-Shaw/sub2api/internal/pkg/redisclient"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func setupTeamChildBrowserTestRouter(t *testing.T, upstream http.HandlerFunc, middlewares ...gin.HandlerFunc) (*OpenAIOAuthHandler, *gin.Engine) {
	t.Helper()
	upstreamServer := httptest.NewServer(upstream)
	t.Cleanup(upstreamServer.Close)
	t.Setenv("TEAM_CHILD_BROWSER_ENABLED", "true")
	t.Setenv("TEAM_CHILD_BROWSER_UPSTREAM_URL", upstreamServer.URL)
	t.Setenv("TEAM_CHILD_BROWSER_SESSION_TTL_MINUTES", "60")

	gin.SetMode(gin.TestMode)
	handler := &OpenAIOAuthHandler{teamBrowserStore: newOpenAITeamBrowserStore()}
	router := gin.New()
	router.Use(middlewares...)
	router.POST("/sessions", func(c *gin.Context) {
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 42})
		c.Next()
	}, handler.CreateTeamChildBrowserSession)
	router.GET(teamChildBrowserProxyPrefix+"/*path", handler.ServeTeamChildBrowser)
	return handler, router
}

func TestTeamChildBrowserSessionRequiresConfiguredBrowser(t *testing.T) {
	t.Setenv("TEAM_CHILD_BROWSER_ENABLED", "false")
	gin.SetMode(gin.TestMode)
	handler := &OpenAIOAuthHandler{teamBrowserStore: newOpenAITeamBrowserStore()}
	router := gin.New()
	router.POST("/sessions", func(c *gin.Context) {
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 42})
		c.Next()
	}, handler.CreateTeamChildBrowserSession)

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/sessions", nil))
	require.Equal(t, http.StatusBadRequest, recorder.Code)
	require.NotContains(t, recorder.Body.String(), "UPSTREAM_URL")
}

func TestTeamChildBrowserTicketIsOneTimeAndExchangedForHttpOnlyCookie(t *testing.T) {
	_, router := setupTeamChildBrowserTestRouter(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})

	createRecorder := httptest.NewRecorder()
	router.ServeHTTP(createRecorder, httptest.NewRequest(http.MethodPost, "/sessions", nil))
	require.Equal(t, http.StatusOK, createRecorder.Code)
	var createResponse struct {
		Data teamChildBrowserSessionResponse `json:"data"`
	}
	require.NoError(t, json.Unmarshal(createRecorder.Body.Bytes(), &createResponse))
	require.NotEmpty(t, createResponse.Data.EmbedURL)
	require.NotContains(t, createResponse.Data.EmbedURL, "127.0.0.1")

	bootstrapURL, err := url.Parse(createResponse.Data.EmbedURL)
	require.NoError(t, err)
	require.NotEmpty(t, bootstrapURL.Query().Get("ticket"))

	bootstrapRecorder := httptest.NewRecorder()
	router.ServeHTTP(bootstrapRecorder, httptest.NewRequest(http.MethodGet, bootstrapURL.String(), nil))
	require.Equal(t, http.StatusFound, bootstrapRecorder.Code)
	require.Equal(t, teamChildBrowserProxyPrefix+"/", bootstrapRecorder.Header().Get("Location"))
	cookies := bootstrapRecorder.Result().Cookies()
	require.Len(t, cookies, 1)
	require.Equal(t, teamChildBrowserCookieName, cookies[0].Name)
	require.True(t, cookies[0].HttpOnly)
	require.Equal(t, http.SameSiteStrictMode, cookies[0].SameSite)
	require.Equal(t, teamChildBrowserProxyPrefix, cookies[0].Path)

	replayRecorder := httptest.NewRecorder()
	router.ServeHTTP(replayRecorder, httptest.NewRequest(http.MethodGet, bootstrapURL.String(), nil))
	require.Equal(t, http.StatusUnauthorized, replayRecorder.Code)
}

func TestTeamChildBrowserProxyRemovesPanelCredentialsAndFrameRestrictions(t *testing.T) {
	var receivedPath string
	var receivedAuthorization string
	var receivedAPIKey string
	var receivedCookie string
	_, router := setupTeamChildBrowserTestRouter(t, func(w http.ResponseWriter, r *http.Request) {
		receivedPath = r.URL.Path
		receivedAuthorization = r.Header.Get("Authorization")
		receivedAPIKey = r.Header.Get("X-API-Key")
		receivedCookie = r.Header.Get("Cookie")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; frame-ancestors 'none'; img-src data:")
		_, _ = w.Write([]byte("remote Chromium"))
	}, middleware.SecurityHeaders(config.CSPConfig{
		Enabled: true,
		Policy:  "default-src 'self'; frame-src https://checkout.example.test; frame-ancestors 'none'",
	}, nil))

	server := httptest.NewServer(router)
	t.Cleanup(server.Close)
	jar, err := cookiejar.New(nil)
	require.NoError(t, err)
	client := &http.Client{
		Jar: jar,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	createRequest, err := http.NewRequest(http.MethodPost, server.URL+"/sessions", nil)
	require.NoError(t, err)
	createResponseHTTP, err := client.Do(createRequest)
	require.NoError(t, err)
	t.Cleanup(func() { _ = createResponseHTTP.Body.Close() })
	require.Equal(t, http.StatusOK, createResponseHTTP.StatusCode)
	var createResponse struct {
		Data teamChildBrowserSessionResponse `json:"data"`
	}
	require.NoError(t, json.NewDecoder(createResponseHTTP.Body).Decode(&createResponse))

	bootstrapResponse, err := client.Get(server.URL + createResponse.Data.EmbedURL)
	require.NoError(t, err)
	t.Cleanup(func() { _ = bootstrapResponse.Body.Close() })
	require.Equal(t, http.StatusFound, bootstrapResponse.StatusCode)

	proxyRequest, err := http.NewRequest(http.MethodGet, server.URL+teamChildBrowserProxyPrefix+"/desktop/", nil)
	require.NoError(t, err)
	proxyRequest.Header.Set("Authorization", "Bearer panel-secret")
	proxyRequest.Header.Set("X-API-Key", "panel-api-key")
	proxyResponse, err := client.Do(proxyRequest)
	require.NoError(t, err)
	t.Cleanup(func() { _ = proxyResponse.Body.Close() })

	require.Equal(t, http.StatusOK, proxyResponse.StatusCode)
	require.Equal(t, teamChildBrowserProxyPrefix+"/desktop/", receivedPath)
	require.Empty(t, receivedAuthorization)
	require.Empty(t, receivedAPIKey)
	require.Empty(t, receivedCookie)
	require.Equal(t, "SAMEORIGIN", proxyResponse.Header.Get("X-Frame-Options"))
	csp := strings.Join(proxyResponse.Header.Values("Content-Security-Policy"), "; ")
	require.Contains(t, csp, "frame-ancestors 'self'")
	require.NotContains(t, csp, "frame-ancestors 'none'")
	require.Contains(t, csp, "default-src 'self'; img-src data:")
	require.Contains(t, proxyResponse.Header.Get("Cache-Control"), "no-store")
	proxyBody, err := io.ReadAll(proxyResponse.Body)
	require.NoError(t, err)
	require.Equal(t, "remote Chromium", string(proxyBody))
}

func TestTeamChildBrowserExpiredSessionIsRejected(t *testing.T) {
	gin.SetMode(gin.TestMode)
	now := time.Now()
	handler := &OpenAIOAuthHandler{teamBrowserStore: newOpenAITeamBrowserStore()}
	handler.teamBrowserStore.now = func() time.Time { return now }
	handler.teamBrowserStore.sessions["expired"] = openAITeamBrowserSession{
		expiresAt: now.Add(-time.Second),
	}
	router := gin.New()
	router.GET(teamChildBrowserProxyPrefix+"/*path", handler.ServeTeamChildBrowser)

	request := httptest.NewRequest(http.MethodGet, teamChildBrowserProxyPrefix+"/", nil)
	request.AddCookie(&http.Cookie{Name: teamChildBrowserCookieName, Value: "expired"})
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusUnauthorized, recorder.Code)
}

func TestTeamChildBrowserRedisStorePersistsAndConsumesTicketAcrossHandlers(t *testing.T) {
	miniRedis := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: miniRedis.Addr()})
	t.Cleanup(func() { _ = client.Close() })

	first := newOpenAITeamBrowserStore()
	first.configureRedis(client)
	second := newOpenAITeamBrowserStore()
	second.configureRedis(client)
	upstream, err := url.Parse("https://team-child-browser:3001")
	require.NoError(t, err)
	session := openAITeamBrowserSession{
		adminUserID: 42,
		upstreamURL: upstream,
		expiresAt:   time.Now().Add(time.Minute),
	}
	require.NoError(t, first.create(t.Context(), "session-token", "one-time-ticket", session, time.Minute))

	loaded, ok, err := second.lookup(t.Context(), "session-token")
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, int64(42), loaded.adminUserID)

	_, consumed, ok, err := second.consumeTicket(t.Context(), "one-time-ticket")
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, int64(42), consumed.adminUserID)
	_, _, ok, err = first.consumeTicket(t.Context(), "one-time-ticket")
	require.NoError(t, err)
	require.False(t, ok)
}

func TestManualBrowserOwnersAreIsolatedAndExpiredInMemory(t *testing.T) {
	store := newOpenAITeamBrowserStore()
	now := time.Now().UTC()
	store.now = func() time.Time { return now }
	firstID, secondID := strings.Repeat("a", 32), strings.Repeat("b", 32)
	first := persistedManualBrowserOwner{SessionID: firstID, ExpiresAt: now.Add(time.Minute)}
	second := persistedManualBrowserOwner{SessionID: secondID, ExpiresAt: now.Add(2 * time.Minute)}

	require.NoError(t, store.setManualOwner(t.Context(), 42, first, time.Minute))
	require.NoError(t, store.setManualOwner(t.Context(), 99, second, 2*time.Minute))
	loaded, found, err := store.getManualOwner(t.Context(), 42)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, firstID, loaded.SessionID)
	loaded, found, err = store.getManualOwner(t.Context(), 99)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, secondID, loaded.SessionID)

	now = now.Add(61 * time.Second)
	_, found, err = store.getManualOwner(t.Context(), 42)
	require.NoError(t, err)
	require.False(t, found)
	_, found, err = store.getManualOwner(t.Context(), 99)
	require.NoError(t, err)
	require.True(t, found)
}

func TestManualBrowserOwnerPersistsAcrossHandlersInRedis(t *testing.T) {
	miniRedis := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: miniRedis.Addr()})
	t.Cleanup(func() { _ = client.Close() })

	first := newOpenAITeamBrowserStore()
	first.configureRedis(client)
	second := newOpenAITeamBrowserStore()
	second.configureRedis(client)
	owner := persistedManualBrowserOwner{
		SessionID: strings.Repeat("c", 32),
		ProxyID:   int64Pointer(29),
		ProxyName: "JP exit",
		EgressIP:  "203.0.113.9",
	}
	require.NoError(t, first.setManualOwner(t.Context(), 42, owner, time.Minute))
	loaded, found, err := second.getManualOwner(t.Context(), 42)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, owner.SessionID, loaded.SessionID)
	require.Equal(t, owner.ProxyID, loaded.ProxyID)
	require.Equal(t, owner.ProxyName, loaded.ProxyName)
	require.Equal(t, owner.EgressIP, loaded.EgressIP)

	require.NoError(t, second.deleteManualOwner(t.Context(), 42))
	_, found, err = first.getManualOwner(t.Context(), 42)
	require.NoError(t, err)
	require.False(t, found)
}

func int64Pointer(value int64) *int64 { return &value }

func TestTeamChildBrowserControllerLeaseCoordinatesInMemorySessions(t *testing.T) {
	store := newOpenAITeamBrowserStore()
	now := time.Date(2026, time.August, 22, 12, 0, 0, 0, time.UTC)
	store.now = func() time.Time { return now }
	firstController := strings.Repeat("a", 32)
	secondController := strings.Repeat("b", 32)

	first, err := store.acquireControl(t.Context(), 42, firstController, time.Minute, false)
	require.NoError(t, err)
	require.Equal(t, now.Add(time.Minute), first.expiresAt)

	_, err = store.acquireControl(t.Context(), 42, secondController, time.Minute, false)
	require.ErrorIs(t, err, errTeamChildBrowserControlHeld)
	_, err = store.acquireControl(t.Context(), 99, secondController, time.Minute, false)
	require.ErrorIs(t, err, errTeamChildBrowserControlHeld)

	now = now.Add(20 * time.Second)
	renewed, ok, err := store.heartbeatControl(t.Context(), 42, firstController, time.Minute)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, now.Add(time.Minute), renewed.expiresAt)

	released, err := store.releaseControl(t.Context(), 99, firstController)
	require.NoError(t, err)
	require.False(t, released)

	oldSession := openAITeamBrowserSession{adminUserID: 42, controllerID: firstController}
	current, err := store.hasCurrentControl(t.Context(), oldSession)
	require.NoError(t, err)
	require.True(t, current)

	_, err = store.acquireControl(t.Context(), 99, secondController, time.Minute, true)
	require.NoError(t, err)
	current, err = store.hasCurrentControl(t.Context(), oldSession)
	require.NoError(t, err)
	require.False(t, current)
	current, err = store.hasCurrentControl(t.Context(), openAITeamBrowserSession{adminUserID: 99, controllerID: secondController})
	require.NoError(t, err)
	require.True(t, current)

	released, err = store.releaseControl(t.Context(), 42, firstController)
	require.NoError(t, err)
	require.False(t, released)
	released, err = store.releaseControl(t.Context(), 99, secondController)
	require.NoError(t, err)
	require.True(t, released)
}

func TestTeamChildBrowserControllerLeaseUsesRedisAcrossHandlers(t *testing.T) {
	miniRedis := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: miniRedis.Addr()})
	t.Cleanup(func() { _ = client.Close() })

	first := newOpenAITeamBrowserStore()
	first.configureRedis(client)
	second := newOpenAITeamBrowserStore()
	second.configureRedis(client)
	firstController := strings.Repeat("a", 32)
	secondController := strings.Repeat("b", 32)

	_, err := first.acquireControl(t.Context(), 42, firstController, time.Minute, false)
	require.NoError(t, err)
	_, err = second.acquireControl(t.Context(), 99, secondController, time.Minute, false)
	require.ErrorIs(t, err, errTeamChildBrowserControlHeld)

	_, renewed, err := second.heartbeatControl(t.Context(), 42, firstController, time.Minute)
	require.NoError(t, err)
	require.True(t, renewed)

	oldSession := openAITeamBrowserSession{adminUserID: 42, controllerID: firstController}
	current, err := second.hasCurrentControl(t.Context(), oldSession)
	require.NoError(t, err)
	require.True(t, current)

	_, err = second.acquireControl(t.Context(), 99, secondController, time.Minute, true)
	require.NoError(t, err)
	current, err = first.hasCurrentControl(t.Context(), oldSession)
	require.NoError(t, err)
	require.False(t, current)

	released, err := first.releaseControl(t.Context(), 42, firstController)
	require.NoError(t, err)
	require.False(t, released)
	released, err = second.releaseControl(t.Context(), 99, secondController)
	require.NoError(t, err)
	require.True(t, released)
}

func TestTeamChildBrowserSessionRejectsCompetingControllerUntilExplicitTakeover(t *testing.T) {
	var upstreamCalls int
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		upstreamCalls++
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(upstream.Close)
	t.Setenv("TEAM_CHILD_BROWSER_ENABLED", "true")
	t.Setenv("TEAM_CHILD_BROWSER_UPSTREAM_URL", upstream.URL)

	gin.SetMode(gin.TestMode)
	handler := &OpenAIOAuthHandler{teamBrowserStore: newOpenAITeamBrowserStore()}
	router := gin.New()
	router.POST("/sessions", func(c *gin.Context) {
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 42})
		c.Next()
	}, handler.CreateTeamChildBrowserSession)
	router.GET(teamChildBrowserProxyPrefix+"/*path", handler.ServeTeamChildBrowser)

	firstController := strings.Repeat("a", 32)
	first := createTeamChildBrowserSessionForControl(t, router, firstController, false)
	secondRequest := httptest.NewRequest(http.MethodPost, "/sessions", strings.NewReader(`{"controller_id":"`+strings.Repeat("b", 32)+`"}`))
	secondRequest.Header.Set("Content-Type", "application/json")
	secondRecorder := httptest.NewRecorder()
	router.ServeHTTP(secondRecorder, secondRequest)
	require.Equal(t, http.StatusConflict, secondRecorder.Code)

	bootstrapURL, err := url.Parse(first.EmbedURL)
	require.NoError(t, err)
	bootstrapRecorder := httptest.NewRecorder()
	router.ServeHTTP(bootstrapRecorder, httptest.NewRequest(http.MethodGet, bootstrapURL.String(), nil))
	require.Equal(t, http.StatusFound, bootstrapRecorder.Code)

	takeover := createTeamChildBrowserSessionForControl(t, router, strings.Repeat("b", 32), true)
	require.NotEmpty(t, takeover.ControllerID)
	staleProxyRequest := httptest.NewRequest(http.MethodGet, teamChildBrowserProxyPrefix+"/", nil)
	staleProxyRequest.AddCookie(bootstrapRecorder.Result().Cookies()[0])
	staleProxyRecorder := httptest.NewRecorder()
	router.ServeHTTP(staleProxyRecorder, staleProxyRequest)
	require.Equal(t, http.StatusConflict, staleProxyRecorder.Code)
	require.Equal(t, 0, upstreamCalls)
}

func createTeamChildBrowserSessionForControl(t *testing.T, router *gin.Engine, controllerID string, takeOver bool) teamChildBrowserSessionResponse {
	t.Helper()
	body := `{"controller_id":"` + controllerID + `"}`
	if takeOver {
		body = `{"controller_id":"` + controllerID + `","take_over":true}`
	}
	request := httptest.NewRequest(http.MethodPost, "/sessions", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	require.Equal(t, http.StatusOK, recorder.Code)
	var result struct {
		Data teamChildBrowserSessionResponse `json:"data"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &result))
	require.Equal(t, controllerID, result.Data.ControllerID)
	require.NotEmpty(t, result.Data.ControlExpiresAt)
	return result.Data
}
