package admin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

const testTeamChildAuthURL = "https://auth.openai.com/oauth/authorize?client_id=app_EMoamEEZ73f0CkXaXp7hrann&code_challenge=test-challenge&code_challenge_method=S256&codex_cli_simplified_flow=true&id_token_add_organizations=true&redirect_uri=http%3A%2F%2Flocalhost%3A1455%2Fauth%2Fcallback&response_type=code&scope=openid+profile+email+offline_access&state=test-state"
const testTeamChildOAuthSessionID = "oauth-session-abcdefghijklmnop"

func setCurrentTeamChildWorkflowProtocol(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set(teamChildWorkflowProtocolHeader, teamChildWorkflowProtocolVersion)
}

func TestTeamChildWorkflowStartRequiresConfirmedSupportedOpenAIURL(t *testing.T) {
	t.Setenv("TEAM_CHILD_AUTOMATION_TOKEN", "service-token")
	called := false
	automation := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(automation.Close)
	t.Setenv("TEAM_CHILD_AUTOMATION_URL", automation.URL)

	gin.SetMode(gin.TestMode)
	handler := &OpenAIOAuthHandler{}
	router := gin.New()
	router.Use(teamChildAdminTestMiddleware("admin"))
	router.POST("/workflows", handler.StartTeamChildWorkflow)

	for _, payload := range []string{
		`{"seat_email":"member@example.test","invite_email":"new@example.test","auth_url":"` + testTeamChildAuthURL + `","confirmed":false}`,
		`{"seat_email":"member@example.test","invite_email":"new@example.test","auth_url":"https://example.test/authorize","confirmed":true}`,
		`{"seat_email":"member@example.test","invite_email":"member@example.test","auth_url":"` + testTeamChildAuthURL + `","confirmed":true}`,
		`{"invite_email":"new@example.test","auth_url":"` + testTeamChildAuthURL + `","confirmed":true}`,
		`{"seat_email":"member@example.test","invite_email":"new@example.test","auth_url":"` + testTeamChildAuthURL + `","seat_already_removed":true,"confirmed":true}`,
	} {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, "/workflows", strings.NewReader(payload))
		request.Header.Set("Content-Type", "application/json")
		router.ServeHTTP(recorder, request)
		require.Equal(t, http.StatusBadRequest, recorder.Code)
	}
	require.False(t, called)
}

func TestTeamChildWorkflowProxyPreservesOnlyConfirmedWorkflowRequest(t *testing.T) {
	t.Setenv("TEAM_CHILD_AUTOMATION_TOKEN", "service-token")
	var receivedToken string
	var receivedMethod string
	var receivedPath string
	var receivedBody map[string]any
	automation := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		setCurrentTeamChildWorkflowProtocol(w)
		if r.URL.Path == "/healthz" {
			_, _ = w.Write([]byte(`{"ok":true,"workflow_schema_version": 4}`))
			return
		}
		receivedToken = r.Header.Get("X-XIASS-Team-Child-Token")
		receivedMethod = r.Method
		receivedPath = r.URL.Path
		require.NoError(t, json.NewDecoder(r.Body).Decode(&receivedBody))
		_, _ = w.Write([]byte(`{"schema_version": 4,"id":"abcdefghijklmnoP","status":"running","nodes":[]}`))
	}))
	t.Cleanup(automation.Close)
	t.Setenv("TEAM_CHILD_AUTOMATION_URL", automation.URL)

	gin.SetMode(gin.TestMode)
	handler := &OpenAIOAuthHandler{}
	router := gin.New()
	router.Use(teamChildAdminTestMiddleware("admin"))
	router.POST("/workflows", handler.StartTeamChildWorkflow)

	payload := `{"seat_email":"member@example.test","invite_email":"new@example.test","auth_url":"` + testTeamChildAuthURL + `","oauth_session_id":"` + testTeamChildOAuthSessionID + `","confirmed":true}`
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/workflows", strings.NewReader(payload))
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, "service-token", receivedToken)
	require.Equal(t, http.MethodPost, receivedMethod)
	require.Equal(t, "/workflows", receivedPath)
	require.Equal(t, "member@example.test", receivedBody["seat_email"])
	require.Equal(t, "new@example.test", receivedBody["invite_email"])
	require.Equal(t, testTeamChildOAuthSessionID, receivedBody["oauth_session_id"])
	require.Equal(t, false, receivedBody["seat_already_removed"])
	_, hasStartStep := receivedBody["start_step"]
	_, hasRunOnlyStep := receivedBody["run_only_step"]
	require.False(t, hasStartStep)
	require.False(t, hasRunOnlyStep)
	require.Equal(t, true, receivedBody["confirmed"])
}

func TestTeamChildWorkflowRejectsLegacyAutomationBeforeStarting(t *testing.T) {
	t.Setenv("TEAM_CHILD_AUTOMATION_TOKEN", "service-token")
	workflowStarted := false
	automation := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/workflows" {
			workflowStarted = true
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	t.Cleanup(automation.Close)
	t.Setenv("TEAM_CHILD_AUTOMATION_URL", automation.URL)

	gin.SetMode(gin.TestMode)
	handler := &OpenAIOAuthHandler{}
	router := gin.New()
	router.Use(teamChildAdminTestMiddleware("admin"))
	router.POST("/workflows", handler.StartTeamChildWorkflow)

	payload := `{"seat_email":"member@example.test","invite_email":"new@example.test","auth_url":"` + testTeamChildAuthURL + `","oauth_session_id":"` + testTeamChildOAuthSessionID + `","confirmed":true}`
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/workflows", strings.NewReader(payload))
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusBadGateway, recorder.Code)
	require.Contains(t, recorder.Body.String(), "运行组件版本不匹配")
	require.False(t, workflowStarted)
}

func TestTeamChildWorkflowAllowsConfirmedManualSeatRelease(t *testing.T) {
	t.Setenv("TEAM_CHILD_AUTOMATION_TOKEN", "service-token")
	var receivedBody map[string]any
	automation := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		setCurrentTeamChildWorkflowProtocol(w)
		if r.URL.Path == "/healthz" {
			_, _ = w.Write([]byte(`{"ok":true,"workflow_schema_version": 4}`))
			return
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&receivedBody))
		_, _ = w.Write([]byte(`{"schema_version": 4,"id":"abcdefghijklmnoP","status":"running","nodes":[]}`))
	}))
	t.Cleanup(automation.Close)
	t.Setenv("TEAM_CHILD_AUTOMATION_URL", automation.URL)

	gin.SetMode(gin.TestMode)
	handler := &OpenAIOAuthHandler{}
	router := gin.New()
	router.Use(teamChildAdminTestMiddleware("admin"))
	router.POST("/workflows", handler.StartTeamChildWorkflow)

	payload := `{"invite_email":"new@example.test","auth_url":"` + testTeamChildAuthURL + `","oauth_session_id":"` + testTeamChildOAuthSessionID + `","seat_already_removed":true,"confirmed":true}`
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/workflows", strings.NewReader(payload))
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Empty(t, receivedBody["seat_email"])
	require.Equal(t, "new@example.test", receivedBody["invite_email"])
	require.Equal(t, true, receivedBody["seat_already_removed"])
	require.Equal(t, true, receivedBody["confirmed"])
}

func TestTeamChildWorkflowRejectsProtectedReplacementSeat(t *testing.T) {
	t.Setenv("TEAM_CHILD_AUTOMATION_TOKEN", "service-token")
	t.Setenv("TEAM_CHILD_PROTECTED_MEMBER_EMAILS", "protected-admin@example.test")
	called := false
	automation := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(automation.Close)
	t.Setenv("TEAM_CHILD_AUTOMATION_URL", automation.URL)

	gin.SetMode(gin.TestMode)
	handler := &OpenAIOAuthHandler{}
	router := gin.New()
	router.Use(teamChildAdminTestMiddleware("admin"))
	router.POST("/workflows", handler.StartTeamChildWorkflow)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/workflows", strings.NewReader(`{"seat_email":"protected-admin@example.test","invite_email":"new@example.test","auth_url":"`+testTeamChildAuthURL+`","oauth_session_id":"`+testTeamChildOAuthSessionID+`","confirmed":true}`))
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusForbidden, recorder.Code)
	require.False(t, called)
}

func TestTeamChildWorkflowContinueValidatesIDAndForwards(t *testing.T) {
	t.Setenv("TEAM_CHILD_AUTOMATION_TOKEN", "service-token")
	var receivedMethod string
	var receivedPath string
	automation := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedMethod = r.Method
		receivedPath = r.URL.Path
		setCurrentTeamChildWorkflowProtocol(w)
		_, _ = w.Write([]byte("{\"schema_version\":2,\"id\":\"abcdefghijklmnoP\",\"status\":\"running\",\"nodes\":[]}"))
	}))
	t.Cleanup(automation.Close)
	t.Setenv("TEAM_CHILD_AUTOMATION_URL", automation.URL)

	gin.SetMode(gin.TestMode)
	handler := &OpenAIOAuthHandler{}
	router := gin.New()
	router.Use(teamChildAdminTestMiddleware("admin"))
	router.POST("/workflows/:workflow_id/continue", handler.ContinueTeamChildWorkflow)

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/workflows/abcdefghijklmnoP/continue", nil))
	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, http.MethodPost, receivedMethod)
	require.Equal(t, "/workflows/abcdefghijklmnoP/continue", receivedPath)

	recorder = httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/workflows/too-short/continue", nil))
	require.Equal(t, http.StatusBadRequest, recorder.Code)
}

func TestTeamChildWorkflowStatusAndCancelValidateIDAndForward(t *testing.T) {
	t.Setenv("TEAM_CHILD_AUTOMATION_TOKEN", "service-token")
	var requests []string
	automation := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		setCurrentTeamChildWorkflowProtocol(w)
		_, _ = w.Write([]byte(`{"schema_version": 4,"id":"abcdefghijklmnoP","status":"manual_required","nodes":[]}`))
	}))
	t.Cleanup(automation.Close)
	t.Setenv("TEAM_CHILD_AUTOMATION_URL", automation.URL)

	gin.SetMode(gin.TestMode)
	handler := &OpenAIOAuthHandler{}
	router := gin.New()
	router.Use(teamChildAdminTestMiddleware("admin"))
	router.GET("/workflows/:workflow_id", handler.GetTeamChildWorkflow)
	router.DELETE("/workflows/:workflow_id", handler.CancelTeamChildWorkflow)

	for _, method := range []string{http.MethodGet, http.MethodDelete} {
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, httptest.NewRequest(method, "/workflows/abcdefghijklmnoP", nil))
		require.Equal(t, http.StatusOK, recorder.Code)
	}
	require.Equal(t, []string{
		"GET /workflows/abcdefghijklmnoP",
		"DELETE /workflows/abcdefghijklmnoP",
	}, requests)

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/workflows/too-short", nil))
	require.Equal(t, http.StatusBadRequest, recorder.Code)
}

func TestTeamChildWorkflowCallbackAndRestartForward(t *testing.T) {
	t.Setenv("TEAM_CHILD_AUTOMATION_TOKEN", "service-token")
	var requests []struct {
		method string
		path   string
		body   map[string]any
	}
	automation := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		request := struct {
			method string
			path   string
			body   map[string]any
		}{method: r.Method, path: r.URL.Path}
		if r.Body != nil {
			require.NoError(t, json.NewDecoder(r.Body).Decode(&request.body))
		}
		requests = append(requests, request)
		setCurrentTeamChildWorkflowProtocol(w)
		_, _ = w.Write([]byte(`{"schema_version": 4,"id":"abcdefghijklmnoP","status":"manual_required","nodes":[]}`))
	}))
	t.Cleanup(automation.Close)
	t.Setenv("TEAM_CHILD_AUTOMATION_URL", automation.URL)

	gin.SetMode(gin.TestMode)
	handler := &OpenAIOAuthHandler{}
	router := gin.New()
	router.Use(teamChildAdminTestMiddleware("admin"))
	router.POST("/workflows/:workflow_id/callback", handler.SubmitTeamChildWorkflowCallback)
	router.POST("/workflows/:workflow_id/restart-oauth", handler.RestartTeamChildWorkflowOAuth)

	for _, test := range []struct {
		path    string
		payload string
	}{
		{path: "/workflows/abcdefghijklmnoP/callback", payload: `{"callback_url":"http://localhost:1455/auth/callback?code=callback-code&state=callback-state"}`},
		{path: "/workflows/abcdefghijklmnoP/restart-oauth", payload: `{"auth_url":"` + testTeamChildAuthURL + `","oauth_session_id":"` + testTeamChildOAuthSessionID + `"}`},
	} {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, test.path, strings.NewReader(test.payload))
		request.Header.Set("Content-Type", "application/json")
		router.ServeHTTP(recorder, request)
		require.Equal(t, http.StatusOK, recorder.Code)
	}

	require.Len(t, requests, 2)
	require.Equal(t, "/workflows/abcdefghijklmnoP/callback", requests[0].path)
	require.Equal(t, "http://localhost:1455/auth/callback?code=callback-code&state=callback-state", requests[0].body["callback_url"])
	require.Equal(t, "/workflows/abcdefghijklmnoP/restart-oauth", requests[1].path)
	require.Equal(t, testTeamChildAuthURL, requests[1].body["auth_url"])
	require.Equal(t, testTeamChildOAuthSessionID, requests[1].body["oauth_session_id"])
	for _, test := range []struct {
		path    string
		payload string
	}{
		{path: "/workflows/abcdefghijklmnoP/callback", payload: `{"callback_url":"http://localhost:1455/auth/callback?code=only-code"}`},
		{path: "/workflows/abcdefghijklmnoP/restart-oauth", payload: `{"auth_url":"https://example.test/authorize"}`},
	} {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, test.path, strings.NewReader(test.payload))
		request.Header.Set("Content-Type", "application/json")
		router.ServeHTTP(recorder, request)
		require.Equal(t, http.StatusBadRequest, recorder.Code)
	}
	require.Len(t, requests, 2)
}

func TestTeamChildWorkflowAutomationInputsUseDistinctValidatedRoutes(t *testing.T) {
	t.Setenv("TEAM_CHILD_AUTOMATION_TOKEN", "service-token")
	type receivedRequest struct {
		path string
		body map[string]any
	}
	var received []receivedRequest
	automation := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		entry := receivedRequest{path: r.URL.Path}
		if r.ContentLength != 0 {
			require.NoError(t, json.NewDecoder(r.Body).Decode(&entry.body))
		}
		received = append(received, entry)
		setCurrentTeamChildWorkflowProtocol(w)
		_, _ = w.Write([]byte(`{"schema_version": 4,"id":"abcdefghijklmnoP","status":"running","nodes":[]}`))
	}))
	t.Cleanup(automation.Close)
	t.Setenv("TEAM_CHILD_AUTOMATION_URL", automation.URL)

	gin.SetMode(gin.TestMode)
	handler := &OpenAIOAuthHandler{}
	router := gin.New()
	router.Use(teamChildAdminTestMiddleware("admin"))
	router.POST("/workflows/:workflow_id/email-code", handler.SubmitTeamChildWorkflowEmailCode)
	router.POST("/workflows/:workflow_id/phone", handler.SubmitTeamChildWorkflowPhone)
	router.POST("/workflows/:workflow_id/sms-code", handler.SubmitTeamChildWorkflowSMSCode)
	router.POST("/workflows/:workflow_id/complete", handler.CompleteTeamChildWorkflow)

	for _, test := range []struct {
		path    string
		payload string
	}{
		{path: "/workflows/abcdefghijklmnoP/email-code", payload: `{"code":"123456"}`},
		{path: "/workflows/abcdefghijklmnoP/phone", payload: `{"phone":"+1 (415) 555-0123"}`},
		{path: "/workflows/abcdefghijklmnoP/sms-code", payload: `{"code":"654321"}`},
		{path: "/workflows/abcdefghijklmnoP/complete", payload: ``},
	} {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, test.path, strings.NewReader(test.payload))
		if test.payload != "" {
			request.Header.Set("Content-Type", "application/json")
		}
		router.ServeHTTP(recorder, request)
		require.Equal(t, http.StatusOK, recorder.Code, test.path)
	}

	require.Len(t, received, 4)
	require.Equal(t, "/workflows/abcdefghijklmnoP/email-code", received[0].path)
	require.Equal(t, "123456", received[0].body["code"])
	require.Equal(t, "/workflows/abcdefghijklmnoP/phone", received[1].path)
	require.Equal(t, "+14155550123", received[1].body["phone"])
	require.Equal(t, "/workflows/abcdefghijklmnoP/sms-code", received[2].path)
	require.Equal(t, "654321", received[2].body["code"])
	require.Equal(t, "/workflows/abcdefghijklmnoP/complete", received[3].path)

	for _, test := range []struct {
		path    string
		payload string
	}{
		{path: "/workflows/abcdefghijklmnoP/email-code", payload: `{"code":"not-code"}`},
		{path: "/workflows/abcdefghijklmnoP/phone", payload: `{"phone":"4155550123"}`},
		{path: "/workflows/abcdefghijklmnoP/sms-code", payload: `{"code":"12"}`},
	} {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, test.path, strings.NewReader(test.payload))
		request.Header.Set("Content-Type", "application/json")
		router.ServeHTTP(recorder, request)
		require.Equal(t, http.StatusBadRequest, recorder.Code, test.path)
	}
	require.Len(t, received, 4)
}

func TestValidateTeamChildWorkflowAuthURL(t *testing.T) {
	for _, raw := range []string{
		testTeamChildAuthURL,
	} {
		require.NoError(t, validateTeamChildWorkflowAuthURL(raw))
	}
	for _, raw := range []string{
		"http://auth.openai.com/oauth/authorize?client_id=x&response_type=code&redirect_uri=http%3A%2F%2Flocalhost%3A1455%2Fauth%2Fcallback&scope=openid&state=s&code_challenge=c&code_challenge_method=S256",
		"https://user:pass@auth.openai.com/oauth/authorize?client_id=x&response_type=code&redirect_uri=http%3A%2F%2Flocalhost%3A1455%2Fauth%2Fcallback&scope=openid&state=s&code_challenge=c&code_challenge_method=S256",
		"https://auth.openai.com.example.test/oauth/authorize?client_id=x&response_type=code&redirect_uri=http%3A%2F%2Flocalhost%3A1455%2Fauth%2Fcallback&scope=openid&state=s&code_challenge=c&code_challenge_method=S256",
		"https://auth.openai.com/authorize?client_id=example",
		"https://auth.openai.com/oauth/authorize?client_id=wrong&code_challenge=test-challenge&code_challenge_method=S256&codex_cli_simplified_flow=true&id_token_add_organizations=true&redirect_uri=http%3A%2F%2Flocalhost%3A1455%2Fauth%2Fcallback&response_type=code&scope=openid+profile+email+offline_access&state=test-state",
		"https://auth.openai.com/oauth/authorize?client_id=app_EMoamEEZ73f0CkXaXp7hrann&code_challenge=test-challenge&code_challenge_method=S256&codex_cli_simplified_flow=true&id_token_add_organizations=true&redirect_uri=https%3A%2F%2Fexample.test%2Fcallback&response_type=code&scope=openid+profile+email+offline_access&state=test-state",
		"https://login.openai.com/authorize",
		"not a URL",
	} {
		require.Error(t, validateTeamChildWorkflowAuthURL(raw))
	}
}

func TestNormalizeTeamChildWorkflowEmailAcceptsTemporaryMailboxAndDisplayText(t *testing.T) {
	for _, test := range []struct {
		input    string
		expected string
	}{
		{input: "nba0nfm7d7@ncml1.top", expected: "nba0nfm7d7@ncml1.top"},
		{input: "  成员邮箱：NBA0NFM7D7@NCML1.TOP  ", expected: "nba0nfm7d7@ncml1.top"},
	} {
		actual := normalizeTeamChildWorkflowEmail(test.input)
		require.Equal(t, test.expected, actual)
		require.True(t, validTeamChildWorkflowEmail(actual))
	}

	for _, input := range []string{"", "not-an-email", "name@localhost"} {
		require.False(t, validTeamChildWorkflowEmail(normalizeTeamChildWorkflowEmail(input)), input)
	}
}
