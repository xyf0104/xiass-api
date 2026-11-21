package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type weekendSettingsRepo struct {
	service.SettingRepository
	value string
}

func (r *weekendSettingsRepo) GetValue(context.Context, string) (string, error) { return r.value, nil }
func (r *weekendSettingsRepo) Set(_ context.Context, _, value string) error {
	r.value = value
	return nil
}

func TestWeekendSettingsHTTP(t *testing.T) {
	gin.SetMode(gin.TestMode)
	repo := &weekendSettingsRepo{}
	h := &SettingHandler{settingService: service.NewSettingService(repo, nil)}
	router := gin.New()
	router.GET("/activity", h.GetWeekendDiscount)
	router.PUT("/activity", h.UpdateWeekendDiscount)
	for _, body := range []string{
		`{"enabled":true,"starts_at":"2026-11-21T00:00:00+08:00","ends_at":"2026-11-20T00:00:00+08:00"}`,
		`{"enabled":true,"starts_at":"bad-date"}`,
		`{"enabled":true,"mode":"not-a-mode"}`,
	} {
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPut, "/activity", strings.NewReader(body)))
		require.Equal(t, http.StatusBadRequest, recorder.Code)
		require.Empty(t, repo.value)
	}
	body := `{"enabled":true,"starts_at":"2026-11-21T00:00:00+08:00","ends_at":null}`
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPut, "/activity", strings.NewReader(body)))
	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, "no-store", recorder.Header().Get("Cache-Control"))
	var result struct {
		Data service.WeekendDiscountStatus `json:"data"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &result))
	require.True(t, result.Data.Enabled)
	require.Equal(t, "Asia/Shanghai", result.Data.Timezone)
	require.Equal(t, .8, result.Data.DiscountMultiplier)
	require.Equal(t, service.DiscountModeWeekends, result.Data.Mode)
	h.settingService = service.NewSettingService(repo, nil)
	recorder = httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/activity", nil))
	require.Equal(t, http.StatusOK, recorder.Code)
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &result))
	require.True(t, result.Data.Enabled, "a new service must restore the saved activity")

	body = `{"enabled":true,"mode":"custom","starts_at":null,"ends_at":null}`
	recorder = httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPut, "/activity", strings.NewReader(body)))
	require.Equal(t, http.StatusOK, recorder.Code)
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &result))
	require.True(t, result.Data.Active, "custom activities can start immediately on any weekday")
	require.Equal(t, .8, result.Data.AppliedMultiplier)
	h.settingService = service.NewSettingService(repo, nil)
	recorder = httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/activity", nil))
	require.Equal(t, http.StatusOK, recorder.Code)
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &result))
	require.Equal(t, service.DiscountModeCustom, result.Data.Mode)
	require.True(t, result.Data.Active)
}
