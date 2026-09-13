package admin

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type teamChildOAuthClientStub struct {
	email string
}

func (s teamChildOAuthClientStub) ExchangeCode(context.Context, string, string, string, string, string) (*openai.TokenResponse, error) {
	payload, _ := json.Marshal(map[string]any{
		"email": s.email,
		"exp":   time.Now().Add(time.Hour).Unix(),
		"https://api.openai.com/auth": map[string]any{
			"chatgpt_account_id": "account-team-test",
			"chatgpt_user_id":    "user-team-test",
			"chatgpt_plan_type":  "team",
		},
	})
	idToken := "e30." + base64.RawURLEncoding.EncodeToString(payload) + ".signature"
	return &openai.TokenResponse{AccessToken: "access-token", RefreshToken: "refresh-token", IDToken: idToken, ExpiresIn: 3600}, nil
}

func (teamChildOAuthClientStub) RefreshToken(context.Context, string, string) (*openai.TokenResponse, error) {
	return nil, errors.New("not implemented")
}

func (teamChildOAuthClientStub) RefreshTokenWithClientID(context.Context, string, string, string) (*openai.TokenResponse, error) {
	return nil, errors.New("not implemented")
}

type teamChildTestEncryptor struct{}

func (teamChildTestEncryptor) Encrypt(plaintext string) (string, error) {
	return "encrypted:" + plaintext, nil
}

func (teamChildTestEncryptor) Decrypt(ciphertext string) (string, error) {
	return ciphertext[len("encrypted:"):], nil
}

func teamChildAdminTestRouter(handler *OpenAIOAuthHandler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 42})
		c.Set(string(middleware.ContextKeyUserRole), "admin")
		c.Next()
	})
	router.GET("/workflows/:workflow_id/password", handler.RevealTeamChildWorkflowPassword)
	router.GET("/accounts/:account_id/password", handler.RevealTeamChildAccountPassword)
	return router
}

func TestRevealTeamChildWorkflowPasswordUsesAuthenticatedCurrentProtocolService(t *testing.T) {
	const workflowID = "workflow_1234567890"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/workflows/"+workflowID+"/secret", r.URL.Path)
		require.Equal(t, "service-token", r.Header.Get("X-XIASS-Team-Child-Token"))
		w.Header().Set(teamChildWorkflowProtocolHeader, teamChildWorkflowProtocolVersion)
		_, _ = w.Write([]byte(`{"email":"team1003@example.test","password":"Abc123456789!"}`))
	}))
	t.Cleanup(server.Close)
	t.Setenv("TEAM_CHILD_AUTOMATION_URL", server.URL)
	t.Setenv("TEAM_CHILD_AUTOMATION_TOKEN", "service-token")

	router := teamChildAdminTestRouter(&OpenAIOAuthHandler{})
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/workflows/"+workflowID+"/password", nil))
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), "team1003@example.test")
	require.Contains(t, rec.Body.String(), "Abc123456789!")
}

func TestRevealTeamChildWorkflowPasswordRejectsProtocolMismatch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set(teamChildWorkflowProtocolHeader, "1")
		_, _ = w.Write([]byte(`{"email":"team1003@example.test","password":"Abc123456789!"}`))
	}))
	t.Cleanup(server.Close)
	t.Setenv("TEAM_CHILD_AUTOMATION_URL", server.URL)
	t.Setenv("TEAM_CHILD_AUTOMATION_TOKEN", "service-token")

	router := teamChildAdminTestRouter(&OpenAIOAuthHandler{})
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/workflows/workflow_1234567890/password", nil))
	require.Equal(t, http.StatusConflict, rec.Code)
	require.NotContains(t, rec.Body.String(), "Abc123456789!")
}

func TestRevealTeamChildAccountPasswordRequiresTeamMarker(t *testing.T) {
	adminService := newStubAdminService()
	adminService.getAccountResult = &service.Account{
		ID:       91,
		Platform: service.PlatformOpenAI,
		Type:     service.AccountTypeOAuth,
		Credentials: map[string]any{
			service.OpenAITeamChildPasswordCredentialKey: "encrypted:Abc123456789!",
		},
		Extra: map[string]any{
			service.OpenAITeamChildExtraKey:      true,
			service.OpenAITeamChildEmailExtraKey: "team1003@example.test",
		},
	}
	handler := &OpenAIOAuthHandler{adminService: adminService, secretEncryptor: teamChildTestEncryptor{}}
	router := teamChildAdminTestRouter(handler)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/accounts/91/password", nil))
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), "Abc123456789!")

	adminService.getAccountResult.Extra = map[string]any{}
	rejected := httptest.NewRecorder()
	router.ServeHTTP(rejected, httptest.NewRequest(http.MethodGet, "/accounts/91/password", nil))
	require.Equal(t, http.StatusNotFound, rejected.Code)
	require.NotContains(t, rejected.Body.String(), "Abc123456789!")
}

func TestReauthorizeTeamChildAccountDecryptsPasswordOnlyForPrivateAutomation(t *testing.T) {
	const password = "Abc123456789!"
	var automationPayload map[string]any
	automation := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set(teamChildWorkflowProtocolHeader, teamChildWorkflowProtocolVersion)
		switch r.URL.Path {
		case "/healthz":
			_, _ = w.Write([]byte(`{"ok":true,"workflow_schema_version": 4}`))
		case "/workflows/reauthorize":
			require.Equal(t, http.MethodPost, r.Method)
			require.Equal(t, "service-token", r.Header.Get("X-XIASS-Team-Child-Token"))
			require.NoError(t, json.NewDecoder(r.Body).Decode(&automationPayload))
			_, _ = w.Write([]byte(`{"schema_version": 4,"id":"workflow_1234567890","status":"running","mode":"reauthorization","target_account_id":91,"nodes":[]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(automation.Close)
	t.Setenv("TEAM_CHILD_AUTOMATION_URL", automation.URL)
	t.Setenv("TEAM_CHILD_AUTOMATION_TOKEN", "service-token")

	adminService := newStubAdminService()
	adminService.getAccountResult = &service.Account{
		ID:       91,
		Platform: service.PlatformOpenAI,
		Type:     service.AccountTypeOAuth,
		Credentials: map[string]any{
			service.OpenAITeamChildPasswordCredentialKey: "encrypted:" + password,
		},
		Extra: map[string]any{
			service.OpenAITeamChildExtraKey:      true,
			service.OpenAITeamChildEmailExtraKey: "team1003@example.test",
		},
	}
	handler := &OpenAIOAuthHandler{adminService: adminService, secretEncryptor: teamChildTestEncryptor{}}
	router := teamChildAdminTestRouter(handler)
	router.POST("/accounts/:account_id/reauthorize", handler.ReauthorizeTeamChildAccount)

	recorder := httptest.NewRecorder()
	body := `{"auth_url":"` + testTeamChildAuthURL + `","oauth_session_id":"` + testTeamChildOAuthSessionID + `"}`
	request := httptest.NewRequest(http.MethodPost, "/accounts/91/reauthorize", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	require.Equal(t, float64(91), automationPayload["account_id"])
	require.Equal(t, "team1003@example.test", automationPayload["email"])
	require.Equal(t, password, automationPayload["password"])
	require.NotContains(t, recorder.Body.String(), password)
}

func TestSaveOpenAIAccountReauthorizationCredentialsEncryptsAndRedactsPassword(t *testing.T) {
	const password = "Abc123456789!"
	adminService := newStubAdminService()
	adminService.getAccountResult = &service.Account{
		ID:       92,
		Platform: service.PlatformOpenAI,
		Type:     service.AccountTypeOAuth,
		Credentials: map[string]any{
			"email": "ordinary@example.test",
		},
	}
	handler := &OpenAIOAuthHandler{adminService: adminService, secretEncryptor: teamChildTestEncryptor{}}
	router := teamChildAdminTestRouter(handler)
	router.POST("/accounts/:account_id/reauthorization-credentials", handler.SaveOpenAIAccountReauthorizationCredentials)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/accounts/92/reauthorization-credentials", strings.NewReader(`{"email":"ordinary@example.test","password":"`+password+`"}`))
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	require.Equal(t, "ordinary@example.test", adminService.lastUpdateAccountInput.Credentials[service.OpenAIOAuthReauthorizationEmailCredentialKey])
	require.Equal(t, "encrypted:"+password, adminService.lastUpdateAccountInput.Credentials[service.OpenAIOAuthReauthorizationPasswordCredentialKey])
	require.NotContains(t, recorder.Body.String(), password)
	require.NotContains(t, recorder.Body.String(), "encrypted:"+password)
}

func TestSaveOpenAIAccountReauthorizationCredentialsAllowsPasswordlessLogin(t *testing.T) {
	adminService := newStubAdminService()
	adminService.getAccountResult = &service.Account{
		ID:       93,
		Platform: service.PlatformOpenAI,
		Type:     service.AccountTypeOAuth,
		Credentials: map[string]any{
			"email": "ordinary@example.test",
			service.OpenAIOAuthReauthorizationPasswordCredentialKey: "encrypted:old-password",
		},
	}
	handler := &OpenAIOAuthHandler{adminService: adminService, secretEncryptor: teamChildTestEncryptor{}}
	router := teamChildAdminTestRouter(handler)
	router.POST("/accounts/:account_id/reauthorization-credentials", handler.SaveOpenAIAccountReauthorizationCredentials)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/accounts/93/reauthorization-credentials", strings.NewReader(`{"email":"ordinary@example.test","password":""}`))
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	require.Equal(t, "ordinary@example.test", adminService.lastUpdateAccountInput.Credentials[service.OpenAIOAuthReauthorizationEmailCredentialKey])
	_, passwordProvided := adminService.lastUpdateAccountInput.Credentials[service.OpenAIOAuthReauthorizationPasswordCredentialKey]
	require.True(t, passwordProvided)
	require.Nil(t, adminService.lastUpdateAccountInput.Credentials[service.OpenAIOAuthReauthorizationPasswordCredentialKey])
	require.True(t, adminService.lastUpdateAccountInput.AllowOpenAIReauthorizationCredentials)
}

func TestReauthorizeOpenAIAccountUsesOnlyDedicatedEncryptedCredentials(t *testing.T) {
	const password = "  Abc123456789!  "
	const totpSecret = "JBSWY3DPEHPK3PXP"
	var automationPayload map[string]any
	automation := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set(teamChildWorkflowProtocolHeader, teamChildWorkflowProtocolVersion)
		switch r.URL.Path {
		case "/healthz":
			_, _ = w.Write([]byte(`{"ok":true,"workflow_schema_version": 4}`))
		case "/workflows/reauthorize":
			require.Equal(t, http.MethodPost, r.Method)
			require.NoError(t, json.NewDecoder(r.Body).Decode(&automationPayload))
			_, _ = w.Write([]byte(`{"schema_version": 4,"id":"workflow_1234567890","status":"running","mode":"reauthorization","target_account_id":92,"nodes":[]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(automation.Close)
	t.Setenv("TEAM_CHILD_AUTOMATION_URL", automation.URL)
	t.Setenv("TEAM_CHILD_AUTOMATION_TOKEN", "service-token")

	adminService := newStubAdminService()
	adminService.getAccountResult = &service.Account{
		ID:       92,
		Platform: service.PlatformOpenAI,
		Type:     service.AccountTypeOAuth,
		Credentials: map[string]any{
			service.OpenAIOAuthReauthorizationEmailCredentialKey:      "ordinary@example.test",
			service.OpenAIOAuthReauthorizationPasswordCredentialKey:   "encrypted:" + password,
			service.OpenAIOAuthReauthorizationTOTPSecretCredentialKey: "encrypted:" + totpSecret,
		},
	}
	handler := &OpenAIOAuthHandler{adminService: adminService, secretEncryptor: teamChildTestEncryptor{}}
	router := teamChildAdminTestRouter(handler)
	router.POST("/accounts/:account_id/reauthorize", handler.ReauthorizeOpenAIAccount)

	recorder := httptest.NewRecorder()
	body := `{"auth_url":"` + testTeamChildAuthURL + `","oauth_session_id":"` + testTeamChildOAuthSessionID + `"}`
	request := httptest.NewRequest(http.MethodPost, "/accounts/92/reauthorize", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	require.Equal(t, float64(92), automationPayload["account_id"])
	require.Equal(t, "ordinary@example.test", automationPayload["email"])
	require.Equal(t, password, automationPayload["password"])
	require.Equal(t, totpSecret, automationPayload["totp_secret"])
	require.NotContains(t, recorder.Body.String(), totpSecret)
	require.NotContains(t, recorder.Body.String(), password)
}

func TestReauthorizeOpenAIAccountAllowsPasswordlessLogin(t *testing.T) {
	var automationPayload map[string]any
	automation := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set(teamChildWorkflowProtocolHeader, teamChildWorkflowProtocolVersion)
		switch r.URL.Path {
		case "/healthz":
			_, _ = w.Write([]byte(`{"ok":true,"workflow_schema_version":4}`))
		case "/workflows/reauthorize":
			require.Equal(t, http.MethodPost, r.Method)
			require.NoError(t, json.NewDecoder(r.Body).Decode(&automationPayload))
			_, _ = w.Write([]byte(`{"schema_version":4,"id":"workflow_1234567890","status":"running","mode":"reauthorization","target_account_id":93,"nodes":[]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(automation.Close)
	t.Setenv("TEAM_CHILD_AUTOMATION_URL", automation.URL)
	t.Setenv("TEAM_CHILD_AUTOMATION_TOKEN", "service-token")

	adminService := newStubAdminService()
	adminService.getAccountResult = &service.Account{
		ID:       93,
		Platform: service.PlatformOpenAI,
		Type:     service.AccountTypeOAuth,
		Credentials: map[string]any{
			service.OpenAIOAuthReauthorizationEmailCredentialKey: "ordinary@example.test",
		},
	}
	handler := &OpenAIOAuthHandler{adminService: adminService, secretEncryptor: teamChildTestEncryptor{}}
	router := teamChildAdminTestRouter(handler)
	router.POST("/accounts/:account_id/reauthorize", handler.ReauthorizeOpenAIAccount)

	recorder := httptest.NewRecorder()
	body := `{"auth_url":"` + testTeamChildAuthURL + `","oauth_session_id":"` + testTeamChildOAuthSessionID + `"}`
	request := httptest.NewRequest(http.MethodPost, "/accounts/93/reauthorize", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	require.Equal(t, float64(93), automationPayload["account_id"])
	require.Equal(t, "ordinary@example.test", automationPayload["email"])
	require.Equal(t, "", automationPayload["password"])
}

func TestCreateAccountFromOAuthEncryptsAndBindsTeamWorkflowPassword(t *testing.T) {
	const (
		workflowID = "workflow_1234567890"
		email      = "team1003@example.test"
		password   = "Abc123456789!"
	)
	automation := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/workflows/"+workflowID+"/secret", r.URL.Path)
		w.Header().Set(teamChildWorkflowProtocolHeader, teamChildWorkflowProtocolVersion)
		_, _ = w.Write([]byte(`{"email":"` + email + `","password":"` + password + `"}`))
	}))
	t.Cleanup(automation.Close)
	t.Setenv("TEAM_CHILD_AUTOMATION_URL", automation.URL)
	t.Setenv("TEAM_CHILD_AUTOMATION_TOKEN", "service-token")

	oauthService := service.NewOpenAIOAuthService(nil, teamChildOAuthClientStub{email: email})
	t.Cleanup(oauthService.Stop)
	auth, err := oauthService.GenerateAuthURL(context.Background(), nil, "", service.PlatformOpenAI)
	require.NoError(t, err)
	parsedAuth, err := url.Parse(auth.AuthURL)
	require.NoError(t, err)
	state := parsedAuth.Query().Get("state")

	adminService := newStubAdminService()
	handler := NewOpenAIOAuthHandler(oauthService, adminService, nil, nil)
	handler.ConfigureTeamChildSecrets(teamChildTestEncryptor{})
	router := gin.New()
	router.POST("/create", handler.CreateAccountFromOAuth)
	body := `{"session_id":"` + auth.SessionID + `","code":"oauth-code","state":"` + state + `","name":"team1003","concurrency":10,"priority":1,"group_ids":[],"team_child":true,"workflow_id":"` + workflowID + `"}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/create", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Len(t, adminService.createdAccounts, 1)
	created := adminService.createdAccounts[0]
	require.Equal(t, email, created.Name)
	require.Equal(t, "encrypted:"+password, created.Credentials[service.OpenAITeamChildPasswordCredentialKey])
	require.Equal(t, email, created.Credentials["email"])
	require.Equal(t, true, created.Extra[service.OpenAITeamChildExtraKey])
	require.Equal(t, email, created.Extra[service.OpenAITeamChildEmailExtraKey])
	require.True(t, created.SkipDefaultGroupBind)
	require.NotNil(t, created.Schedulable)
	require.True(t, *created.Schedulable)
	require.NotContains(t, rec.Body.String(), password)
}

func TestCreateAccountFromOAuthAllowsPasswordlessTeamWorkflow(t *testing.T) {
	const (
		workflowID = "workflow_1234567890"
		email      = "team1004@example.test"
	)
	automation := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/workflows/"+workflowID+"/secret", r.URL.Path)
		w.Header().Set(teamChildWorkflowProtocolHeader, teamChildWorkflowProtocolVersion)
		_, _ = w.Write([]byte(`{"email":"` + email + `","password":""}`))
	}))
	t.Cleanup(automation.Close)
	t.Setenv("TEAM_CHILD_AUTOMATION_URL", automation.URL)
	t.Setenv("TEAM_CHILD_AUTOMATION_TOKEN", "service-token")

	oauthService := service.NewOpenAIOAuthService(nil, teamChildOAuthClientStub{email: email})
	t.Cleanup(oauthService.Stop)
	auth, err := oauthService.GenerateAuthURL(context.Background(), nil, "", service.PlatformOpenAI)
	require.NoError(t, err)
	parsedAuth, err := url.Parse(auth.AuthURL)
	require.NoError(t, err)

	adminService := newStubAdminService()
	handler := NewOpenAIOAuthHandler(oauthService, adminService, nil, nil)
	router := gin.New()
	router.POST("/create", handler.CreateAccountFromOAuth)
	body := `{"session_id":"` + auth.SessionID + `","code":"oauth-code","state":"` + parsedAuth.Query().Get("state") + `","team_child":true,"workflow_id":"` + workflowID + `"}`
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/create", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	require.Len(t, adminService.createdAccounts, 1)
	created := adminService.createdAccounts[0]
	require.Equal(t, email, created.Name)
	require.Equal(t, email, created.Credentials["email"])
	_, hasPassword := created.Credentials[service.OpenAITeamChildPasswordCredentialKey]
	require.False(t, hasPassword)
	require.Equal(t, true, created.Extra[service.OpenAITeamChildExtraKey])
	require.Equal(t, email, created.Extra[service.OpenAITeamChildEmailExtraKey])
}

func TestCreateAccountFromOAuthRejectsWorkflowEmailMismatch(t *testing.T) {
	const workflowID = "workflow_1234567890"
	automation := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set(teamChildWorkflowProtocolHeader, teamChildWorkflowProtocolVersion)
		_, _ = w.Write([]byte(`{"email":"team1003@example.test","password":"Abc123456789!"}`))
	}))
	t.Cleanup(automation.Close)
	t.Setenv("TEAM_CHILD_AUTOMATION_URL", automation.URL)
	t.Setenv("TEAM_CHILD_AUTOMATION_TOKEN", "service-token")

	oauthService := service.NewOpenAIOAuthService(nil, teamChildOAuthClientStub{email: "different@example.test"})
	t.Cleanup(oauthService.Stop)
	auth, err := oauthService.GenerateAuthURL(context.Background(), nil, "", service.PlatformOpenAI)
	require.NoError(t, err)
	parsedAuth, _ := url.Parse(auth.AuthURL)
	adminService := newStubAdminService()
	handler := NewOpenAIOAuthHandler(oauthService, adminService, nil, nil)
	handler.ConfigureTeamChildSecrets(teamChildTestEncryptor{})
	router := gin.New()
	router.POST("/create", handler.CreateAccountFromOAuth)
	body := `{"session_id":"` + auth.SessionID + `","code":"oauth-code","state":"` + parsedAuth.Query().Get("state") + `","team_child":true,"workflow_id":"` + workflowID + `"}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/create", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Empty(t, adminService.createdAccounts)
}
