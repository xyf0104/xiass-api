package admin

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type smsProviderHandlerTestEncryptor struct{}

func (smsProviderHandlerTestEncryptor) Encrypt(value string) (string, error) {
	return "encrypted:" + value, nil
}

func (smsProviderHandlerTestEncryptor) Decrypt(value string) (string, error) {
	return strings.TrimPrefix(value, "encrypted:"), nil
}

func newSMSProviderHandlerTest(t *testing.T) (*SettingHandler, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	svc := service.NewPixlabSMSService(db, smsProviderHandlerTestEncryptor{}, nil)
	t.Cleanup(func() {
		svc.Stop()
		_ = db.Close()
	})
	h := NewSettingHandler(nil, nil, nil, nil, nil, nil, nil)
	h.SetPixlabSMSService(svc)
	return h, mock
}

func callSettingHandler(t *testing.T, method, path, body string, handler gin.HandlerFunc) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Handle(method, path, handler)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	router.ServeHTTP(recorder, request)
	return recorder
}

func decodeSMSProviderResponse(t *testing.T, body []byte) map[string]any {
	t.Helper()
	var envelope struct {
		Data map[string]any `json:"data"`
	}
	require.NoError(t, json.Unmarshal(body, &envelope))
	return envelope.Data
}

func TestGetSMSProviderSettingsReturnsOnlyConfiguredFlag(t *testing.T) {
	h, mock := newSMSProviderHandlerTest(t)
	mock.ExpectQuery("SELECT EXISTS").WithArgs("sms_provider_giema_api_key").WillReturnRows(
		sqlmock.NewRows([]string{"exists"}).AddRow(true),
	)

	recorder := callSettingHandler(t, http.MethodGet, "/providers", "", h.GetSMSProviderSettings)
	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, "no-store", recorder.Header().Get("Cache-Control"))
	require.Equal(t, map[string]any{"giema_api_key_configured": true}, decodeSMSProviderResponse(t, recorder.Body.Bytes()))
	require.NotContains(t, recorder.Body.String(), "sms_provider_giema_api_key")
	require.NotContains(t, recorder.Body.String(), "encrypted:")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestSaveSMSProviderSettingsEncryptsAndReturnsOnlyConfiguredFlag(t *testing.T) {
	h, mock := newSMSProviderHandlerTest(t)
	mock.ExpectExec("INSERT INTO security_secrets").WithArgs(
		"sms_provider_giema_api_key", "encrypted:real-provider-key",
	).WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectQuery("SELECT EXISTS").WithArgs("sms_provider_giema_api_key").WillReturnRows(
		sqlmock.NewRows([]string{"exists"}).AddRow(true),
	)

	recorder := callSettingHandler(t, http.MethodPut, "/providers", `{"api_key":"real-provider-key"}`, h.SaveSMSProviderSettings)
	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, "no-store", recorder.Header().Get("Cache-Control"))
	require.Equal(t, map[string]any{"giema_api_key_configured": true}, decodeSMSProviderResponse(t, recorder.Body.Bytes()))
	require.NotContains(t, recorder.Body.String(), "real-provider-key")
	require.NotContains(t, recorder.Body.String(), "encrypted:")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestSaveSMSProviderSettingsRejectsMalformedStrictOrOversizedBodiesWithoutRawErrors(t *testing.T) {
	h, mock := newSMSProviderHandlerTest(t)
	for name, body := range map[string]string{
		"malformed":       `{"api_key":`,
		"unknown field":   `{"api_key":"key","secret":"should-not-be-accepted"}`,
		"multiple values": `{"api_key":"key"}{"api_key":"second"}`,
		"oversized":       `{"api_key":"` + strings.Repeat("x", smsProviderSettingsBodyLimit) + `"}`,
		"empty key":       `{"api_key":"   "}`,
	} {
		t.Run(name, func(t *testing.T) {
			recorder := callSettingHandler(t, http.MethodPut, "/providers", body, h.SaveSMSProviderSettings)
			require.Equal(t, http.StatusBadRequest, recorder.Code)
			require.Equal(t, "no-store", recorder.Header().Get("Cache-Control"))
			require.NotContains(t, recorder.Body.String(), "real-provider-key")
			require.NotContains(t, recorder.Body.String(), "should-not-be-accepted")
			require.NotContains(t, recorder.Body.String(), "MaxBytesError")
		})
	}
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestSaveSMSProviderSettingsDoesNotReturnServiceErrorDetails(t *testing.T) {
	h, mock := newSMSProviderHandlerTest(t)
	mock.ExpectExec("INSERT INTO security_secrets").WithArgs(
		"sms_provider_giema_api_key", "encrypted:provider-key",
	).WillReturnError(errors.New("private provider storage detail"))

	recorder := callSettingHandler(t, http.MethodPut, "/providers", `{"api_key":"provider-key"}`, h.SaveSMSProviderSettings)
	require.Equal(t, http.StatusInternalServerError, recorder.Code)
	require.NotContains(t, recorder.Body.String(), "private provider storage detail")
	require.NotContains(t, recorder.Body.String(), "provider-key")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestAddPixlabSMSCardKeysDefaultsProviderToPixlab(t *testing.T) {
	h, mock := newSMSProviderHandlerTest(t)
	mock.ExpectBegin()
	mock.ExpectQuery("INSERT INTO xiass_sms_card_keys").WithArgs(
		sqlmock.AnyArg(), sqlmock.AnyArg(), service.SMSProviderPixlab,
	).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(1))
	mock.ExpectQuery("SELECT COUNT").WillReturnRows(
		sqlmock.NewRows([]string{"queued", "active"}).AddRow(1, 0),
	)
	mock.ExpectCommit()

	recorder := callSettingHandler(t, http.MethodPost, "/card-keys", `{"card_keys":"card-170"}`, h.AddPixlabSMSCardKeys)
	require.Equal(t, http.StatusOK, recorder.Code)
	require.NotContains(t, recorder.Body.String(), "giema")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestSMSProviderHandlersReturnUnavailableWithoutService(t *testing.T) {
	h := NewSettingHandler(nil, nil, nil, nil, nil, nil, nil)
	for _, tc := range []struct {
		name, method string
		handler      gin.HandlerFunc
	}{
		{"get", http.MethodGet, h.GetSMSProviderSettings},
		{"put", http.MethodPut, h.SaveSMSProviderSettings},
	} {
		t.Run(tc.name, func(t *testing.T) {
			recorder := callSettingHandler(t, tc.method, "/providers", `{"api_key":"provider-key"}`, tc.handler)
			require.Equal(t, http.StatusServiceUnavailable, recorder.Code)
			require.Equal(t, "no-store", recorder.Header().Get("Cache-Control"))
			require.NotContains(t, recorder.Body.String(), "provider-key")
		})
	}
}
