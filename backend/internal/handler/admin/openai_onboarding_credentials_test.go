package admin

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"net/http"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/handler/dto"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

// Account storage boundary for handler tests; service tests exercise the real
// trusted-capability and credential merging implementation separately.
type onboardingCredentialsAdmin struct {
	*stubAdminService
	persisted *service.Account
	readback  func(*service.Account)
	reads     int
}

func (s *onboardingCredentialsAdmin) CreateAccount(ctx context.Context, in *service.CreateAccountInput) (*service.Account, error) {
	a, err := s.stubAdminService.CreateAccount(ctx, in)
	if err != nil {
		return nil, err
	}
	a.Platform, a.Type = in.Platform, in.Type
	a.Credentials, a.Extra = maps.Clone(in.Credentials), maps.Clone(in.Extra)
	a.GroupIDs, a.ProxyID = in.GroupIDs, in.ProxyID
	a.Concurrency, a.Priority = in.Concurrency, in.Priority
	a.Schedulable = in.Schedulable != nil && *in.Schedulable
	s.persisted = a
	return a, nil
}

func (s *onboardingCredentialsAdmin) GetAccount(context.Context, int64) (*service.Account, error) {
	s.reads++
	if s.readback != nil {
		s.readback(s.persisted)
	}
	return s.persisted, nil
}

func (s *onboardingCredentialsAdmin) UpdateAccount(ctx context.Context, id int64, in *service.UpdateAccountInput) (*service.Account, error) {
	if _, err := s.stubAdminService.UpdateAccount(ctx, id, in); err != nil {
		return nil, err
	}
	if in.AllowOpenAIReauthorizationCredentials {
		merged := maps.Clone(s.persisted.Credentials)
		maps.Copy(merged, in.Credentials)
		s.persisted.Credentials = merged
	} else {
		s.persisted.Credentials = service.MergePreservingSensitiveCreds(s.persisted.Credentials, in.Credentials)
	}
	return s.persisted, nil
}

func (s *onboardingCredentialsAdmin) ClearAccountError(context.Context, int64) (*service.Account, error) {
	s.persisted.Status, s.persisted.ErrorMessage = service.StatusActive, ""
	return s.persisted, nil
}

type onboardingCountingEncryptor struct {
	service.SecretEncryptor
	decrypts    int
	failEncrypt bool
}

func (e *onboardingCountingEncryptor) Encrypt(value string) (string, error) {
	if e.failEncrypt {
		return "", errors.New("private-encryption-error")
	}
	return e.SecretEncryptor.Encrypt(value)
}

func (e *onboardingCountingEncryptor) Decrypt(value string) (string, error) {
	e.decrypts++
	return e.SecretEncryptor.Decrypt(value)
}

func TestBatchOAuthSavesEncryptedLoginAndSameAccount401Reauthorization(t *testing.T) {
	f := newBatchOAuthFixture(t)
	storage := &onboardingCredentialsAdmin{stubAdminService: f.admin}
	f.h.adminService = storage
	encryptor := &onboardingCountingEncryptor{SecretEncryptor: f.h.secretEncryptor}
	f.h.secretEncryptor = encryptor
	r := batchOAuthRouter(f.h, 42, "admin")
	const password = "  exact password  "
	const totp = "JBSWY3DPEHPK3PXP"
	w := batchOAuthRequest(r, "POST", "/tasks", `{"email":"OWNER@example.test","password":"`+password+`","totp_secret":"`+totp+`","name":"saved account","concurrency":7,"priority":9,"idempotency_key":"saved-login-operation-1"}`)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	id := f.requests[0]["task_id"].(string)
	task := f.h.batchOAuthStore.tasks[id]
	require.NotContains(t, task.loginPasswordEncrypted, password)
	require.NotContains(t, task.loginTOTPEncrypted, totp)
	require.Equal(t, password, f.requests[0]["password"])
	w = batchOAuthRequest(r, "POST", "/tasks/"+id+"/complete", "{}")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.Contains(t, w.Body.String(), `"status":"completed"`)
	require.Contains(t, w.Body.String(), `"account_config"`)
	require.Positive(t, storage.reads)
	require.Zero(t, encryptor.decrypts, "completion must compare ciphertext without decrypting")
	require.Len(t, f.admin.createdAccounts, 1)
	require.True(t, f.admin.createdAccounts[0].AllowOpenAIReauthorizationCredentials)
	a := storage.persisted
	require.Equal(t, task.loginPasswordEncrypted, a.GetCredential(service.OpenAIOAuthReauthorizationPasswordCredentialKey))
	require.Equal(t, task.loginTOTPEncrypted, a.GetCredential(service.OpenAIOAuthReauthorizationTOTPSecretCredentialKey))
	require.Equal(t, "owner@example.test", a.GetCredential(service.OpenAIOAuthReauthorizationEmailCredentialKey))
	for _, key := range []string{service.OpenAIOAuthReauthorizationPasswordCredentialKey, service.OpenAIOAuthReauthorizationTOTPSecretCredentialKey} {
		plain, err := encryptor.SecretEncryptor.Decrypt(a.GetCredential(key))
		require.NoError(t, err)
		if key == service.OpenAIOAuthReauthorizationPasswordCredentialKey {
			require.Equal(t, password, plain)
		} else {
			require.Equal(t, totp, plain)
		}
	}
	beforeCredentials := maps.Clone(a.Credentials)
	beforeConfig := *task.AccountConfig
	a.Status, a.ErrorMessage = service.StatusError, "401 unauthorized"
	accountHandler := NewAccountHandler(storage, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	r.POST("/accounts/:id/apply-oauth-credentials", accountHandler.ApplyOAuthCredentials)
	w = batchOAuthRequest(r, "POST", "/accounts/300/apply-oauth-credentials", `{"type":"oauth","credentials":{"email":"OWNER@example.test","access_token":"new-access","refresh_token":"new-refresh"}}`)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.Equal(t, int64(300), storage.persisted.ID)
	require.Equal(t, "new-access", storage.persisted.GetCredential("access_token"))
	for _, key := range []string{service.OpenAIOAuthReauthorizationEmailCredentialKey, service.OpenAIOAuthReauthorizationPasswordCredentialKey, service.OpenAIOAuthReauthorizationTOTPSecretCredentialKey} {
		require.Equal(t, beforeCredentials[key], storage.persisted.Credentials[key])
	}
	require.Equal(t, beforeConfig.Name, storage.persisted.Name)
	require.Equal(t, beforeConfig.Concurrency, storage.persisted.Concurrency)
	require.Equal(t, beforeConfig.Priority, storage.persisted.Priority)
	require.Contains(t, w.Body.String(), `"name":"saved account"`)
	require.Contains(t, w.Body.String(), `"concurrency":7`)
	for _, secret := range []string{password, totp, task.loginPasswordEncrypted, task.loginTOTPEncrypted, "new-access", "new-refresh"} {
		require.NotContains(t, w.Body.String(), secret)
	}
	w = batchOAuthRequest(r, "POST", "/accounts/300/apply-oauth-credentials", `{"type":"oauth","credentials":{"email":"other@example.test","access_token":"wrong-account"}}`)
	require.Equal(t, http.StatusBadRequest, w.Code)
	w = batchOAuthRequest(r, "POST", "/accounts/300/apply-oauth-credentials", `{"type":"oauth","credentials":{"access_token":"unidentified-account"}}`)
	require.Equal(t, http.StatusBadRequest, w.Code)
	require.Equal(t, "new-access", storage.persisted.GetCredential("access_token"))
	require.Len(t, f.admin.createdAccounts, 1)
}

func TestBatchOAuthReadbackRejectsDroppedLoginAndConfiguration(t *testing.T) {
	for _, field := range []string{"password", "totp", "name", "priority"} {
		t.Run(field, func(t *testing.T) {
			f := newBatchOAuthFixture(t)
			storage := &onboardingCredentialsAdmin{stubAdminService: f.admin, readback: func(a *service.Account) {
				switch field {
				case "password":
					delete(a.Credentials, service.OpenAIOAuthReauthorizationPasswordCredentialKey)
				case "totp":
					delete(a.Credentials, service.OpenAIOAuthReauthorizationTOTPSecretCredentialKey)
				case "name":
					a.Name = "unexpected"
				case "priority":
					a.Priority++
				}
			}}
			f.h.adminService = storage
			r, id := startFixtureTask(t, f)
			w := batchOAuthRequest(r, "POST", "/tasks/"+id+"/complete", "{}")
			require.Equal(t, http.StatusOK, w.Code)
			require.Contains(t, w.Body.String(), `"status":"failed"`)
			require.NotContains(t, w.Body.String(), `"account_config"`)
			require.Positive(t, storage.reads)
			_ = batchOAuthRequest(r, "POST", "/tasks/"+id+"/complete", "{}")
			require.Len(t, f.admin.createdAccounts, 1)
		})
	}
}

func TestBatchOAuthRestartUsesEncryptedLoginAndOptionalReplacement(t *testing.T) {
	f := newBatchOAuthFixture(t)
	encryptor := &onboardingCountingEncryptor{SecretEncryptor: f.h.secretEncryptor}
	f.h.secretEncryptor = encryptor
	r, id := startFixtureTask(t, f)
	w := batchOAuthRequest(r, "POST", "/tasks/"+id+"/restart", `{"confirmed":true}`)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.Equal(t, 2, encryptor.decrypts)
	require.Equal(t, "login-secret", f.requests[1]["password"])
	require.Equal(t, "JBSWY3DPEHPK3PXP", f.requests[1]["totp_secret"])
	w = batchOAuthRequest(r, "POST", "/tasks/"+id+"/restart", `{"password":"  changed  ","totp_secret":"","confirmed":true}`)
	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, "  changed  ", f.requests[2]["password"])
	require.Empty(t, f.requests[2]["totp_secret"])
	require.Empty(t, f.h.batchOAuthStore.tasks[id].loginTOTPEncrypted)
	password, err := encryptor.SecretEncryptor.Decrypt(f.h.batchOAuthStore.tasks[id].loginPasswordEncrypted)
	require.NoError(t, err)
	require.Equal(t, "  changed  ", password)
}

func TestBatchOAuthEncryptionFailureNeverStartsAutomation(t *testing.T) {
	f := newBatchOAuthFixture(t)
	f.h.secretEncryptor = &onboardingCountingEncryptor{SecretEncryptor: f.h.secretEncryptor, failEncrypt: true}
	r := batchOAuthRouter(f.h, 42, "admin")
	w := batchOAuthRequest(r, "POST", "/tasks", `{"email":"owner@example.test","password":"login-secret","idempotency_key":"encryption-failure-1"}`)
	require.Equal(t, http.StatusInternalServerError, w.Code)
	require.NotContains(t, w.Body.String(), "private-encryption-error")
	require.Zero(t, f.sidecarCalls.Load())
	require.Empty(t, f.h.batchOAuthStore.tasks)
}

func TestSaveOpenAIReauthorizationTOTPTristatePreservesAccountConfiguration(t *testing.T) {
	for _, value := range []string{"omitted", "", "JBSWY3DPEHPK3PXP", "invalid-secret"} {
		t.Run(value, func(t *testing.T) {
			f := newBatchOAuthFixture(t)
			oldCipher, err := f.h.secretEncryptor.Encrypt("JBSWY3DPEHPK3PXP")
			require.NoError(t, err)
			a := &service.Account{ID: 300, Name: "unchanged", Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth, Concurrency: 8, Priority: 12,
				Credentials: map[string]any{"email": "owner@example.test", "base_url": "https://example.test", "model_mapping": map[string]any{"public": "upstream"}, service.OpenAIOAuthReauthorizationTOTPSecretCredentialKey: oldCipher}, Extra: map[string]any{"codex_fingerprint_mode": "off"}}
			storage := &onboardingCredentialsAdmin{stubAdminService: f.admin, persisted: a}
			f.h.adminService = storage
			r := batchOAuthRouter(f.h, 42, "admin")
			r.POST("/accounts/:id/reauthorization-credentials", f.h.SaveOpenAIAccountReauthorizationCredentials)
			body := map[string]any{"email": "owner@example.test", "password": "  exact password  "}
			if value != "omitted" {
				body["totp_secret"] = value
			}
			raw, err := json.Marshal(body)
			require.NoError(t, err)
			w := batchOAuthRequest(r, "POST", "/accounts/300/reauthorization-credentials", string(raw))
			if value == "invalid-secret" {
				require.Equal(t, http.StatusBadRequest, w.Code)
				require.Zero(t, f.admin.updateAccountCalls)
				return
			}
			require.Equal(t, http.StatusOK, w.Code, w.Body.String())
			require.Equal(t, "no-store", w.Header().Get("Cache-Control"))
			require.Equal(t, "https://example.test", a.Credentials["base_url"])
			require.Equal(t, map[string]any{"public": "upstream"}, a.Credentials["model_mapping"])
			require.Equal(t, "unchanged", a.Name)
			require.Contains(t, w.Body.String(), `"concurrency":8`)
			password, err := f.h.secretEncryptor.Decrypt(a.GetCredential(service.OpenAIOAuthReauthorizationPasswordCredentialKey))
			require.NoError(t, err)
			require.Equal(t, "  exact password  ", password)
			switch value {
			case "omitted":
				require.Equal(t, oldCipher, a.GetCredential(service.OpenAIOAuthReauthorizationTOTPSecretCredentialKey))
			case "":
				require.Nil(t, a.Credentials[service.OpenAIOAuthReauthorizationTOTPSecretCredentialKey])
			default:
				plain, err := f.h.secretEncryptor.Decrypt(a.GetCredential(service.OpenAIOAuthReauthorizationTOTPSecretCredentialKey))
				require.NoError(t, err)
				require.Equal(t, value, plain)
			}
			require.NotContains(t, w.Body.String(), oldCipher)
			require.NotContains(t, w.Body.String(), "exact password")
			require.NotContains(t, w.Body.String(), "JBSWY3DPEHPK3PXP")
		})
	}
}

func TestOpenAIReauthorizationTOTPRedactedFromDTOAndExport(t *testing.T) {
	credentials := map[string]any{service.OpenAIOAuthReauthorizationEmailCredentialKey: "private@example.test", service.OpenAIOAuthReauthorizationPasswordCredentialKey: "password-ciphertext", service.OpenAIOAuthReauthorizationTOTPSecretCredentialKey: "totp-ciphertext", "base_url": "https://example.test"}
	out, status := dto.RedactCredentials(credentials)
	require.Equal(t, map[string]any{"base_url": "https://example.test"}, out)
	require.Equal(t, out, exportableAccountCredentials(credentials))
	require.True(t, status["has_"+service.OpenAIOAuthReauthorizationTOTPSecretCredentialKey])
	encoded, err := json.Marshal(dto.AccountFromService(&service.Account{Credentials: credentials}))
	require.NoError(t, err)
	for _, secret := range []string{"private@example.test", "password-ciphertext", "totp-ciphertext"} {
		require.False(t, strings.Contains(string(encoded), secret))
	}
}

func TestOpenAIOnboardingLoginIdentityMismatchRejectsSaveAndRunner(t *testing.T) {
	f := newBatchOAuthFixture(t)
	a := &service.Account{ID: 300, Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth, Credentials: map[string]any{
		"email": "owner@example.test", service.OpenAIOAuthReauthorizationEmailCredentialKey: "other@example.test",
	}}
	storage := &onboardingCredentialsAdmin{stubAdminService: f.admin, persisted: a}
	f.h.adminService = storage
	r := batchOAuthRouter(f.h, 42, "admin")
	r.POST("/accounts/:id/reauthorization-credentials", f.h.SaveOpenAIAccountReauthorizationCredentials)
	r.POST("/accounts/:id/reauthorize", f.h.ReauthorizeOpenAIAccount)
	w := batchOAuthRequest(r, "POST", "/accounts/300/reauthorization-credentials", `{"email":"other@example.test","password":"login-secret","totp_secret":"JBSWY3DPEHPK3PXP"}`)
	require.Equal(t, http.StatusBadRequest, w.Code)
	require.Zero(t, f.admin.updateAccountCalls)
	w = batchOAuthRequest(r, "POST", "/accounts/300/reauthorize", `{"auth_url":"`+testTeamChildAuthURL+`","oauth_session_id":"`+testTeamChildOAuthSessionID+`"}`)
	require.Equal(t, http.StatusBadRequest, w.Code)
	require.Zero(t, f.sidecarCalls.Load())
}

func TestOpenAIOnboardingUndecryptableTOTPDoesNotStartRunner(t *testing.T) {
	f := newBatchOAuthFixture(t)
	password, err := f.h.secretEncryptor.Encrypt("login-secret")
	require.NoError(t, err)
	storage := &onboardingCredentialsAdmin{stubAdminService: f.admin, persisted: &service.Account{ID: 300, Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth, Credentials: map[string]any{
		"email": "owner@example.test", service.OpenAIOAuthReauthorizationEmailCredentialKey: "owner@example.test",
		service.OpenAIOAuthReauthorizationPasswordCredentialKey: password, service.OpenAIOAuthReauthorizationTOTPSecretCredentialKey: "corrupt-private-ciphertext",
	}}}
	f.h.adminService = storage
	r := batchOAuthRouter(f.h, 42, "admin")
	r.POST("/accounts/:id/reauthorize", f.h.ReauthorizeOpenAIAccount)
	w := batchOAuthRequest(r, "POST", "/accounts/300/reauthorize", `{"auth_url":"`+testTeamChildAuthURL+`","oauth_session_id":"`+testTeamChildOAuthSessionID+`"}`)
	require.Equal(t, http.StatusInternalServerError, w.Code)
	require.NotContains(t, w.Body.String(), "corrupt-private-ciphertext")
	require.NotContains(t, w.Body.String(), "login-secret")
	require.Zero(t, f.sidecarCalls.Load())
}
