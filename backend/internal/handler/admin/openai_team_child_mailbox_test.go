package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	redis "github.com/Wei-Shaw/sub2api/internal/pkg/redisclient"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type teamMailboxConfigSettingRepo struct {
	service.SettingRepository
	values map[string]string
}

func (r *teamMailboxConfigSettingRepo) GetValue(_ context.Context, key string) (string, error) {
	if value, ok := r.values[key]; ok {
		return value, nil
	}
	return "", service.ErrSettingNotFound
}

func (r *teamMailboxConfigSettingRepo) Set(_ context.Context, key, value string) error {
	if r.values == nil {
		r.values = make(map[string]string)
	}
	r.values[key] = value
	return nil
}

func setupTeamMailboxTestHandler(t *testing.T, provider http.HandlerFunc) (*OpenAIOAuthHandler, *gin.Engine) {
	t.Helper()
	server := httptest.NewServer(provider)
	t.Cleanup(server.Close)
	t.Setenv("TEAM_CHILD_MAIL_API_BASE", server.URL)
	t.Setenv("TEAM_CHILD_MAIL_AUTH_MODE", "x-api-key")
	t.Setenv("TEAM_CHILD_MAIL_API_KEY", "provider-api-key")
	t.Setenv("TEAM_CHILD_MAIL_DOMAIN", "example.test")
	t.Setenv("TEAM_CHILD_MAIL_CREATE_PATH", "/api/new_address")
	t.Setenv("TEAM_CHILD_MAIL_MESSAGES_PATH", "/api/mails")
	t.Setenv(teamMailboxSequenceFileEnv, filepath.Join(t.TempDir(), "team-child-mail-sequence"))

	gin.SetMode(gin.TestMode)
	handler := &OpenAIOAuthHandler{teamMailboxStore: newOpenAITeamMailboxStore()}
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 42})
		c.Set(string(middleware.ContextKeyUserRole), "admin")
		c.Next()
	})
	router.GET("/status", handler.TeamChildMailboxStatus)
	router.POST("/mailboxes", handler.CreateTeamChildMailbox)
	router.GET("/mailboxes", handler.ListTeamChildMailboxes)
	router.POST("/mailboxes/select", handler.SelectTeamChildMailbox)
	router.GET("/mailboxes/active", handler.GetActiveTeamChildMailbox)
	router.GET("/mailboxes/:session_id/code", handler.PollTeamChildMailboxCode)
	router.DELETE("/mailboxes/:session_id", handler.DeleteTeamChildMailboxSession)
	return handler, router
}

func TestTeamChildMailboxCanListAndReopenKnownAddressWithoutExposingJWT(t *testing.T) {
	_, router := setupTeamMailboxTestHandler(t, func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			Name   string `json:"name"`
			Domain string `json:"domain"`
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
		require.Equal(t, "example.test", payload.Domain)
		_, _ = fmt.Fprintf(w, `{"address":%q,"jwt":"mailbox-jwt-%s"}`, payload.Name+"@"+payload.Domain, payload.Name)
	})

	createRec := httptest.NewRecorder()
	router.ServeHTTP(createRec, httptest.NewRequest(http.MethodPost, "/mailboxes", nil))
	require.Equal(t, http.StatusOK, createRec.Code)
	require.NotContains(t, createRec.Body.String(), "mailbox-jwt")

	listRec := httptest.NewRecorder()
	router.ServeHTTP(listRec, httptest.NewRequest(http.MethodGet, "/mailboxes", nil))
	require.Equal(t, http.StatusOK, listRec.Code)
	require.Contains(t, listRec.Body.String(), "team1000@example.test")
	require.NotContains(t, listRec.Body.String(), "mailbox-jwt")

	selectRec := httptest.NewRecorder()
	selectReq := httptest.NewRequest(http.MethodPost, "/mailboxes/select", strings.NewReader(`{"email":"team1000@example.test"}`))
	selectReq.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(selectRec, selectReq)
	require.Equal(t, http.StatusOK, selectRec.Code)
	require.Contains(t, selectRec.Body.String(), "team1000@example.test")
	require.NotContains(t, selectRec.Body.String(), "mailbox-jwt")
}

func TestSelectTeamChildMailboxRejectsForeignAndNonXIASSAddresses(t *testing.T) {
	_, router := setupTeamMailboxTestHandler(t, func(w http.ResponseWriter, _ *http.Request) {
		t.Fatal("provider must not be called for an invalid selection")
	})
	for _, email := range []string{"someone@example.test", "team1000@foreign.test", "team0999@example.test", "team1x@example.test"} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/mailboxes/select", strings.NewReader(fmt.Sprintf(`{"email":%q}`, email)))
		req.Header.Set("Content-Type", "application/json")
		router.ServeHTTP(rec, req)
		require.Equal(t, http.StatusBadRequest, rec.Code, email)
	}
}

func TestTeamChildMailboxStatusIsDisabledWithoutProvider(t *testing.T) {
	t.Setenv("TEAM_CHILD_MAIL_API_BASE", "")
	t.Setenv("TEAM_CHILD_BROWSER_ENABLED", "false")
	gin.SetMode(gin.TestMode)
	handler := &OpenAIOAuthHandler{teamMailboxStore: newOpenAITeamMailboxStore()}
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 42})
		c.Set(string(middleware.ContextKeyUserRole), "admin")
		c.Next()
	})
	router.GET("/status", handler.TeamChildMailboxStatus)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/status", nil))
	require.Equal(t, http.StatusOK, rec.Code)
	var response struct {
		Data struct {
			Configured        bool `json:"configured"`
			BrowserConfigured bool `json:"browser_configured"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &response))
	require.False(t, response.Data.Configured)
	require.False(t, response.Data.BrowserConfigured)
}

func TestCreateTeamChildMailboxKeepsProviderTokenServerSide(t *testing.T) {
	_, router := setupTeamMailboxTestHandler(t, func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/new_address", r.URL.Path)
		require.Equal(t, "provider-api-key", r.Header.Get("X-API-Key"))
		require.Equal(t, "application/json", r.Header.Get("Content-Type"))
		_, _ = w.Write([]byte(`{"address":"team1000@example.test","jwt":"mailbox-jwt-secret"}`))
	})

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/mailboxes", nil))
	require.Equal(t, http.StatusOK, rec.Code)
	require.NotContains(t, rec.Body.String(), "mailbox-jwt-secret")
	var response struct {
		Data openAITeamMailboxCreateResponse `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &response))
	require.NotEmpty(t, response.Data.SessionID)
	require.Equal(t, "team1000@example.test", response.Data.Email)
}

func TestActiveTeamChildMailboxRestoresOnlyCurrentAdministratorHandle(t *testing.T) {
	_, router := setupTeamMailboxTestHandler(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"address":"team1000@example.test","jwt":"mailbox-jwt-secret"}`))
	})

	createRec := httptest.NewRecorder()
	router.ServeHTTP(createRec, httptest.NewRequest(http.MethodPost, "/mailboxes", nil))
	require.Equal(t, http.StatusOK, createRec.Code)
	var created struct {
		Data openAITeamMailboxCreateResponse `json:"data"`
	}
	require.NoError(t, json.Unmarshal(createRec.Body.Bytes(), &created))

	activeRec := httptest.NewRecorder()
	router.ServeHTTP(activeRec, httptest.NewRequest(http.MethodGet, "/mailboxes/active", nil))
	require.Equal(t, http.StatusOK, activeRec.Code)
	require.NotContains(t, activeRec.Body.String(), "mailbox-jwt-secret")
	var active struct {
		Data struct {
			Active  bool                            `json:"active"`
			Mailbox openAITeamMailboxCreateResponse `json:"mailbox"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(activeRec.Body.Bytes(), &active))
	require.True(t, active.Data.Active)
	require.Equal(t, created.Data.SessionID, active.Data.Mailbox.SessionID)
	require.Equal(t, created.Data.Email, active.Data.Mailbox.Email)
}

func TestCreateTeamChildMailboxUsesAdminWorkerPayload(t *testing.T) {
	t.Setenv("TEAM_CHILD_MAIL_DOMAIN", "example.test")
	_, router := setupTeamMailboxTestHandler(t, func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			Name         string `json:"name"`
			EnablePrefix bool   `json:"enablePrefix"`
			Domain       string `json:"domain"`
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
		require.Equal(t, "team1000", payload.Name)
		require.False(t, payload.EnablePrefix)
		require.Equal(t, "example.test", payload.Domain)
		_, _ = w.Write([]byte(`{"address":"team1000@example.test","jwt":"mailbox-jwt-secret"}`))
	})
	t.Setenv("TEAM_CHILD_MAIL_CREATE_PATH", "/admin/new_address")

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/mailboxes", nil))
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestTeamChildMailboxAddressSequencePersistsAndIncrements(t *testing.T) {
	sequencePath := filepath.Join(t.TempDir(), "team-child-mail-sequence")
	t.Setenv(teamMailboxSequenceFileEnv, sequencePath)
	config := teamMailboxProviderConfig{domain: "example.test"}
	first := &OpenAIOAuthHandler{teamMailboxStore: newOpenAITeamMailboxStore()}
	second := &OpenAIOAuthHandler{teamMailboxStore: newOpenAITeamMailboxStore()}

	address, err := first.nextTeamChildMailboxAddress(t.Context(), config)
	require.NoError(t, err)
	require.Equal(t, "team1000@example.test", address)
	address, err = first.nextTeamChildMailboxAddress(t.Context(), config)
	require.NoError(t, err)
	require.Equal(t, "team1001@example.test", address)

	// A new handler instance continues from the persisted server-side counter.
	address, err = second.nextTeamChildMailboxAddress(t.Context(), config)
	require.NoError(t, err)
	require.Equal(t, "team1002@example.test", address)
}

func TestTeamChildMailboxPollUsesMailboxJWTAndExtractsReadableVerificationCode(t *testing.T) {
	handler, router := setupTeamMailboxTestHandler(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/new_address":
			_, _ = w.Write([]byte(`{"address":"team1000@example.test","jwt":"mailbox-jwt-secret"}`))
		case "/api/mails":
			require.Equal(t, teamMailboxProviderUserAgent, r.Header.Get("User-Agent"))
			require.Equal(t, "Bearer mailbox-jwt-secret", r.Header.Get("Authorization"))
			require.Equal(t, "", r.Header.Get("X-API-Key"))
			require.Equal(t, "20", r.URL.Query().Get("limit"))
			require.NotEmpty(t, r.URL.Query().Get("_xiass_poll"))
			require.Equal(t, "no-cache, no-store, max-age=0", r.Header.Get("Cache-Control"))
			require.Equal(t, "no-cache", r.Header.Get("Pragma"))
			_, _ = w.Write([]byte(`{"messages":[{"id":"m-1","to":[{"address":"team1000@example.test"}],"subject":"OpenAI verification code: 418204"}]}`))
		default:
			t.Fatalf("unexpected request to %s", r.URL.Path)
		}
	})

	createRec := httptest.NewRecorder()
	router.ServeHTTP(createRec, httptest.NewRequest(http.MethodPost, "/mailboxes", nil))
	var createResponse struct {
		Data openAITeamMailboxCreateResponse `json:"data"`
	}
	require.NoError(t, json.Unmarshal(createRec.Body.Bytes(), &createResponse))

	pollRec := httptest.NewRecorder()
	router.ServeHTTP(pollRec, httptest.NewRequest(http.MethodGet, "/mailboxes/"+createResponse.Data.SessionID+"/code", nil))
	require.Equal(t, http.StatusOK, pollRec.Code)
	var pollResponse struct {
		Data openAITeamMailboxCodeResponse `json:"data"`
	}
	require.NoError(t, json.Unmarshal(pollRec.Body.Bytes(), &pollResponse))
	require.Equal(t, "received", pollResponse.Data.Status)
	require.Equal(t, "418204", pollResponse.Data.Code)

	// Ensure the actual session, not the HTTP response, owns the sensitive JWT.
	session, ok := handler.lookupTeamMailboxSession(createResponse.Data.SessionID)
	require.True(t, ok)
	require.Equal(t, "mailbox-jwt-secret", session.token)
}

func TestTeamChildMailboxCodeIgnoresStyleNoise(t *testing.T) {
	message := map[string]any{
		"html": `<style>.banner:after { content: "verification code 123456"; }</style><p>Welcome to OpenAI</p>`,
	}
	require.Empty(t, extractTeamMailboxVerificationCode(teamMailboxReadableText(message)))

	message["html"] = `<style>.banner { color: red; }</style><p>Your verification code is 654321</p>`
	require.Equal(t, "654321", extractTeamMailboxVerificationCode(teamMailboxReadableText(message)))

	message = map[string]any{
		"body": `<style>.banner:after { content: "verification code 222222"; }</style><p>Welcome</p>`,
	}
	require.Empty(t, extractTeamMailboxVerificationCode(teamMailboxReadableText(message)))
}

func TestTeamChildMailboxPollSupportsNestedMessagesAndRecipientAliases(t *testing.T) {
	_, router := setupTeamMailboxTestHandler(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/new_address":
			_, _ = w.Write([]byte(`{"address":"team1000@example.test","jwt":"mailbox-jwt-secret"}`))
		case "/api/mails":
			_, _ = w.Write([]byte(`{"data":{"items":[{"id":"nested-1","recipient_email":"team1000@example.test","from":{"name":"OpenAI","address":"noreply@openai.com"},"body":{"html":"<p>Your security code is <strong>4 182 04</strong></p>"}}]}}`))
		default:
			t.Fatalf("unexpected request to %s", r.URL.Path)
		}
	})

	createRec := httptest.NewRecorder()
	router.ServeHTTP(createRec, httptest.NewRequest(http.MethodPost, "/mailboxes", nil))
	var createResponse struct {
		Data openAITeamMailboxCreateResponse `json:"data"`
	}
	require.NoError(t, json.Unmarshal(createRec.Body.Bytes(), &createResponse))

	pollRec := httptest.NewRecorder()
	router.ServeHTTP(pollRec, httptest.NewRequest(http.MethodGet, "/mailboxes/"+createResponse.Data.SessionID+"/code", nil))
	require.Equal(t, http.StatusOK, pollRec.Code)
	var pollResponse struct {
		Data openAITeamMailboxCodeResponse `json:"data"`
	}
	require.NoError(t, json.Unmarshal(pollRec.Body.Bytes(), &pollResponse))
	require.Equal(t, "received", pollResponse.Data.Status)
	require.Equal(t, "418204", pollResponse.Data.Code)
}

func TestTeamMailboxMessageAddressMatchingRejectsDifferentExplicitRecipient(t *testing.T) {
	require.True(t, teamMailboxMessageMatchesAddress(
		map[string]any{"to_email": "target@example.test"},
		"target@example.test",
	))
	require.False(t, teamMailboxMessageMatchesAddress(
		map[string]any{"to_email": "other@example.test"},
		"target@example.test",
	))
	require.True(t, teamMailboxMessageMatchesAddress(
		map[string]any{"subject": "OpenAI verification code: 123456"},
		"target@example.test",
	))
}

func TestTeamMailboxCodeExtractionNormalizesSeparatedDigits(t *testing.T) {
	require.Equal(t, "123456", extractTeamMailboxVerificationCode("Your verification code is 12-34 56"))
	require.Equal(t, "654321", extractTeamMailboxVerificationCode("验证码：654321"))
}

func TestTeamMailboxCodeExtractionFallsBackForAnySender(t *testing.T) {
	message := map[string]any{
		"from":    "Test sender <tester@example.test>",
		"subject": "Test verification email",
		"text":    "Private test message with a standalone number: 418204",
	}
	require.Equal(t, "418204", extractTeamMailboxVerificationCodeFromMessage(message))
}

func TestTeamChildMailboxPollUsesOnlyOfficialOpenAIMail(t *testing.T) {
	_, router := setupTeamMailboxTestHandler(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/new_address":
			_, _ = w.Write([]byte(`{"address":"team1000@example.test","jwt":"mailbox-jwt-secret"}`))
		case "/api/mails":
			_, _ = w.Write([]byte(`{"messages":[
				{"id":"family-code","to_email":"team1000@example.test","from":"Family <family@example.test>","subject":"Dinner update","text":"The entry code is 654321."},
				{"id":"openai-code","to_email":"team1000@example.test","from":{"name":"OpenAI","address":"noreply@openai.com"},"subject":"Your verification code","text":"Your verification code is 418204."}
			]}`))
		default:
			t.Fatalf("unexpected request to %s", r.URL.Path)
		}
	})

	createRec := httptest.NewRecorder()
	router.ServeHTTP(createRec, httptest.NewRequest(http.MethodPost, "/mailboxes", nil))
	var created struct {
		Data openAITeamMailboxCreateResponse `json:"data"`
	}
	require.NoError(t, json.Unmarshal(createRec.Body.Bytes(), &created))

	pollRec := httptest.NewRecorder()
	router.ServeHTTP(pollRec, httptest.NewRequest(http.MethodGet, "/mailboxes/"+created.Data.SessionID+"/code", nil))
	require.Equal(t, http.StatusOK, pollRec.Code)
	var result struct {
		Data openAITeamMailboxCodeResponse `json:"data"`
	}
	require.NoError(t, json.Unmarshal(pollRec.Body.Bytes(), &result))
	require.Equal(t, "received", result.Data.Status)
	require.Equal(t, "418204", result.Data.Code)
}

func TestTeamMailboxOfficialOpenAIIdentityDetection(t *testing.T) {
	testCases := []struct {
		name    string
		message map[string]any
		want    bool
	}{
		{
			name:    "OpenAI domain",
			message: map[string]any{"from": "OpenAI <noreply@openai.com>"},
			want:    true,
		},
		{
			name:    "ChatGPT subdomain",
			message: map[string]any{"from": map[string]any{"address": "mailer@notify.chatgpt.com"}},
			want:    true,
		},
		{
			name:    "Nested sender metadata",
			message: map[string]any{"headers": map[string]any{"from": map[string]any{"name": "OpenAI", "email": "noreply@mailer.openai.com"}}},
			want:    true,
		},
		{
			name:    "Known non OpenAI sender wins over subject",
			message: map[string]any{"from": "Family <family@example.test>", "subject": "OpenAI verification code"},
			want:    false,
		},
		{
			name:    "Missing summary sender may use official subject",
			message: map[string]any{"subject": "OpenAI verification code"},
			want:    true,
		},
		{
			name:    "Unrelated mail",
			message: map[string]any{"from": "Family <family@example.test>", "subject": "Dinner update"},
			want:    false,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			require.Equal(t, testCase.want, teamMailboxMessageIsOfficialOpenAI(testCase.message))
		})
	}
}

func TestTeamChildMailboxExpiredSessionIsRejectedAndCanBeDeleted(t *testing.T) {
	handler := &OpenAIOAuthHandler{teamMailboxStore: newOpenAITeamMailboxStore()}
	now := time.Now()
	handler.teamMailboxStore.now = func() time.Time { return now }
	handler.teamMailboxStore.sessions["expired"] = openAITeamMailboxSession{expiresAt: now.Add(-time.Second)}
	handler.teamMailboxStore.sessions["active"] = openAITeamMailboxSession{expiresAt: now.Add(time.Minute)}

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 42})
		c.Set(string(middleware.ContextKeyUserRole), "admin")
		c.Next()
	})
	router.GET("/mailboxes/:session_id/code", handler.PollTeamChildMailboxCode)
	router.DELETE("/mailboxes/:session_id", handler.DeleteTeamChildMailboxSession)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/mailboxes/expired/code", nil))
	require.Equal(t, http.StatusBadRequest, rec.Code)

	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/mailboxes/active", nil))
	require.Equal(t, http.StatusOK, rec.Code)
	_, ok := handler.lookupTeamMailboxSession("active")
	require.False(t, ok)
}

func TestTeamMailboxQueryKeyAuthAddsKeyToCreationRequest(t *testing.T) {
	t.Setenv("TEAM_CHILD_MAIL_API_BASE", "https://mail.example.test")
	t.Setenv("TEAM_CHILD_MAIL_AUTH_MODE", "query-key")
	t.Setenv("TEAM_CHILD_MAIL_API_KEY", "query-secret")
	config, err := loadTeamMailboxProviderConfig(nil)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "https://mail.example.test/api/new_address", strings.NewReader(`{}`))
	applyTeamMailboxCreateAuth(req, config)
	require.Equal(t, "query-secret", req.URL.Query().Get("key"))
}

func TestImportTeamChildMailboxConfigStoresSecretServerSide(t *testing.T) {
	configPath := t.TempDir() + "/team-child-mail.env"
	t.Setenv("TEAM_CHILD_MAIL_CONFIG_FILE", configPath)
	gin.SetMode(gin.TestMode)
	handler := &OpenAIOAuthHandler{teamMailboxStore: newOpenAITeamMailboxStore()}
	settingRepo := &teamMailboxConfigSettingRepo{values: make(map[string]string)}
	handler.ConfigureTeamChildSecrets(teamMailboxShareTestEncryptor{})
	handler.ConfigureTeamChildSharedSettings(settingRepo)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 42})
		c.Set(string(middleware.ContextKeyUserRole), "admin")
		c.Next()
	})
	router.POST("/config", handler.ImportTeamChildMailboxConfig)

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", "config.json")
	require.NoError(t, err)
	_, err = part.Write([]byte(`{"cloudflare_api_base":"https://mail.example.test","cloudflare_api_key":"server-secret","cloudflare_auth_mode":"x-admin-auth","cloudflare_path_accounts":"/admin/new_address","cloudflare_path_messages":"/api/mails","defaultDomains":"example.test"}`))
	require.NoError(t, err)
	require.NoError(t, writer.Close())

	req := httptest.NewRequest(http.MethodPost, "/config", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.NotContains(t, rec.Body.String(), "server-secret")
	require.Contains(t, rec.Body.String(), `"cluster_shared":true`)
	stored, err := os.ReadFile(configPath)
	require.NoError(t, err)
	require.Contains(t, string(stored), `TEAM_CHILD_MAIL_API_KEY="server-secret"`)
	require.Equal(t, os.FileMode(0o600), func() os.FileMode { info, _ := os.Stat(configPath); return info.Mode().Perm() }())

	loaded, err := loadTeamMailboxProviderConfig(nil)
	require.NoError(t, err)
	require.Equal(t, "https://mail.example.test", loaded.baseURL.String())
	require.Equal(t, "server-secret", loaded.apiKey)

	sharedCiphertext := settingRepo.values[service.SettingKeyTeamChildMailboxConfig]
	require.NotEmpty(t, sharedCiphertext)
	require.NotContains(t, sharedCiphertext, "server-secret")

	// A paired node with no local mailbox file reads the encrypted provider
	// configuration from the same database settings row.
	t.Setenv("TEAM_CHILD_MAIL_CONFIG_FILE", filepath.Join(t.TempDir(), "missing.env"))
	paired := &OpenAIOAuthHandler{}
	paired.ConfigureTeamChildSecrets(teamMailboxShareTestEncryptor{})
	paired.ConfigureTeamChildSharedSettings(settingRepo)
	shared, err := paired.loadTeamMailboxProviderConfig(nil)
	require.NoError(t, err)
	require.Equal(t, "https://mail.example.test", shared.baseURL.String())
	require.Equal(t, "server-secret", shared.apiKey)

	pairedRouter := gin.New()
	pairedRouter.Use(func(c *gin.Context) {
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 42})
		c.Set(string(middleware.ContextKeyUserRole), "admin")
		c.Next()
	})
	pairedRouter.GET("/status", paired.TeamChildMailboxStatus)
	statusRec := httptest.NewRecorder()
	pairedRouter.ServeHTTP(statusRec, httptest.NewRequest(http.MethodGet, "/status", nil))
	require.Equal(t, http.StatusOK, statusRec.Code)
	require.Contains(t, statusRec.Body.String(), `"configured":true`)
}

func TestTeamMailboxBootstrapPublishesExistingLocalConfig(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "team-child-mail.env")
	t.Setenv("TEAM_CHILD_MAIL_CONFIG_FILE", configPath)
	require.NoError(t, os.WriteFile(configPath, []byte(strings.Join([]string{
		`TEAM_CHILD_MAIL_API_BASE="https://mail.example.test"`,
		`TEAM_CHILD_MAIL_AUTH_MODE="x-api-key"`,
		`TEAM_CHILD_MAIL_API_KEY="bootstrap-secret"`,
		`TEAM_CHILD_MAIL_CUSTOM_AUTH=""`,
		`TEAM_CHILD_MAIL_DOMAIN="example.test"`,
		`TEAM_CHILD_MAIL_CREATE_PATH="/api/new_address"`,
		`TEAM_CHILD_MAIL_MESSAGES_PATH="/api/mails"`,
	}, "\n")), 0o600))

	repo := &teamMailboxConfigSettingRepo{values: make(map[string]string)}
	handler := &OpenAIOAuthHandler{}
	handler.ConfigureTeamChildSecrets(teamMailboxShareTestEncryptor{})
	handler.ConfigureTeamChildSharedSettings(repo)

	stored := repo.values[service.SettingKeyTeamChildMailboxConfig]
	require.NotEmpty(t, stored)
	require.NotContains(t, stored, "bootstrap-secret")
	loaded, found, err := loadTeamMailboxSharedConfig(context.Background(), repo, teamMailboxShareTestEncryptor{})
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, "bootstrap-secret", loaded.apiKey)
}

func TestTeamChildMailboxRedisStoreEnforcesOwnerAcrossHandlers(t *testing.T) {
	miniRedis := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: miniRedis.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	baseURL, err := url.Parse("https://mail.example.test")
	require.NoError(t, err)

	first := newOpenAITeamMailboxStore()
	first.configureRedis(client)
	second := newOpenAITeamMailboxStore()
	second.configureRedis(client)
	firstHandler := &OpenAIOAuthHandler{teamMailboxStore: first}
	secondHandler := &OpenAIOAuthHandler{teamMailboxStore: second}
	session := openAITeamMailboxSession{
		adminUserID: 42,
		email:       "one@example.test",
		token:       "mailbox-token",
		config: teamMailboxProviderConfig{
			baseURL: baseURL,
		},
		expiresAt: time.Now().Add(time.Minute),
	}
	require.NoError(t, first.create(t.Context(), "mailbox-session", session))

	loaded, ok, err := secondHandler.lookupTeamMailboxSessionContext(t.Context(), "mailbox-session", 7)
	require.NoError(t, err)
	require.False(t, ok)
	require.Empty(t, loaded.token)

	loaded, ok, err = secondHandler.lookupTeamMailboxSessionContext(t.Context(), "mailbox-session", 42)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "mailbox-token", loaded.token)
	require.NoError(t, second.delete(t.Context(), "mailbox-session", 7))
	_, ok, err = firstHandler.lookupTeamMailboxSessionContext(t.Context(), "mailbox-session", 42)
	require.NoError(t, err)
	require.True(t, ok)
	require.NoError(t, second.delete(t.Context(), "mailbox-session", 42))
	_, ok, err = firstHandler.lookupTeamMailboxSessionContext(t.Context(), "mailbox-session", 42)
	require.NoError(t, err)
	require.False(t, ok)
	_, _, ok, err = first.active(t.Context(), 42)
	require.NoError(t, err)
	require.False(t, ok)
	_, err = client.Get(t.Context(), teamMailboxActiveKeyPrefix+"42").Result()
	require.ErrorIs(t, err, redis.Nil)
}

func TestTeamChildMailboxRedisStoreRecoversLegacyActiveSession(t *testing.T) {
	miniRedis := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: miniRedis.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	baseURL, err := url.Parse("https://mail.example.test")
	require.NoError(t, err)

	store := newOpenAITeamMailboxStore()
	store.configureRedis(client)
	session := openAITeamMailboxSession{
		adminUserID: 42,
		email:       "one@example.test",
		token:       "mailbox-token",
		config:      teamMailboxProviderConfig{baseURL: baseURL},
		expiresAt:   time.Now().Add(time.Minute),
	}
	payload, err := encodePersistedTeamMailboxSession(session)
	require.NoError(t, err)
	require.NoError(t, client.Set(t.Context(), teamMailboxRedisKeyPrefix+"legacy-session", payload, time.Minute).Err())

	id, restored, ok, err := store.active(t.Context(), 42)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "legacy-session", id)
	require.Equal(t, "one@example.test", restored.email)
	indexedID, err := client.Get(t.Context(), teamMailboxActiveKeyPrefix+"42").Result()
	require.NoError(t, err)
	require.Equal(t, "legacy-session", indexedID)
}

func TestTeamChildMailboxSequenceRejectsMissingDomainBeforeAllocating(t *testing.T) {
	sequencePath := filepath.Join(t.TempDir(), "team-child-mail-sequence")
	t.Setenv(teamMailboxSequenceFileEnv, sequencePath)
	handler := &OpenAIOAuthHandler{teamMailboxStore: newOpenAITeamMailboxStore()}

	_, err := handler.nextTeamChildMailboxAddress(t.Context(), teamMailboxProviderConfig{})
	require.ErrorContains(t, err, "TEAM_CHILD_MAIL_DOMAIN")
	_, readErr := os.ReadFile(sequencePath)
	require.ErrorIs(t, readErr, os.ErrNotExist)
}
