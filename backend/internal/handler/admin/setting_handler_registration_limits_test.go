package admin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestSettingsRegistrationLimitsRoundTrip(t *testing.T) {
	h, repo := newStepUpSwitchTestHandler(t, map[string]string{})
	for _, enabled := range []bool{true, false} {
		rec := doUpdateSettings(t, h, map[string]any{
			"registration_ip_daily_limit_enabled":     enabled,
			"registration_email_domain_quota_enabled": enabled,
		}, nil)
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		want := "false"
		if enabled {
			want = "true"
		}
		require.Equal(t, want, repo.values[service.SettingKeyRegistrationIPDailyLimitEnabled])
		require.Equal(t, want, repo.values[service.SettingKeyRegistrationEmailDomainQuotaEnabled])
		require.JSONEq(t, want, getRegistrationSetting(t, h, "registration_ip_daily_limit_enabled"))
		require.JSONEq(t, want, getRegistrationSetting(t, h, "registration_email_domain_quota_enabled"))
	}
}

func TestSettingsRegistrationLimitsPreserveOmitted(t *testing.T) {
	h, repo := newStepUpSwitchTestHandler(t, map[string]string{
		service.SettingKeyRegistrationIPDailyLimitEnabled:     "true",
		service.SettingKeyRegistrationEmailDomainQuotaEnabled: "true",
	})
	rec := doUpdateSettings(t, h, map[string]any{"registration_enabled": true}, nil)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "true", repo.values[service.SettingKeyRegistrationIPDailyLimitEnabled])
	require.Equal(t, "true", repo.values[service.SettingKeyRegistrationEmailDomainQuotaEnabled])
	rec = doUpdateSettings(t, h, map[string]any{"registration_ip_daily_limit_enabled": false}, nil)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "false", repo.values[service.SettingKeyRegistrationIPDailyLimitEnabled])
	require.Equal(t, "true", repo.values[service.SettingKeyRegistrationEmailDomainQuotaEnabled])
}

func getRegistrationSetting(t *testing.T, h *SettingHandler, key string) string {
	t.Helper()
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/admin/settings", nil)
	h.GetSettings(c)
	require.Equal(t, http.StatusOK, rec.Code)
	var payload struct {
		Data map[string]any `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &payload))
	value, err := json.Marshal(payload.Data[key])
	require.NoError(t, err)
	return string(value)
}
