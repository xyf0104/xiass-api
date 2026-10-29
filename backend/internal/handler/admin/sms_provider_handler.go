package admin

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

const smsProviderSettingsBodyLimit = 8 << 10

type smsProviderAPIKeyRequest struct {
	APIKey string `json:"api_key"`
}

func (h *SettingHandler) smsProviderServiceOrError(c *gin.Context) *service.PixlabSMSService {
	if h == nil || h.pixlabSMSService == nil {
		response.Error(c, http.StatusServiceUnavailable, "接码服务尚未初始化")
		return nil
	}
	return h.pixlabSMSService
}

func setSMSProviderNoStore(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
}

func writeSMSProviderSettings(c *gin.Context, settings *service.SMSProviderSettings) {
	if settings == nil {
		response.Error(c, http.StatusServiceUnavailable, "接码供应商配置不可用")
		return
	}
	response.Success(c, gin.H{"giema_api_key_configured": settings.KeyConfigured})
}

// GetSMSProviderSettings returns only the Giema key presence flag.
// GET /api/v1/admin/settings/sms-receiver/providers
func (h *SettingHandler) GetSMSProviderSettings(c *gin.Context) {
	setSMSProviderNoStore(c)
	svc := h.smsProviderServiceOrError(c)
	if svc == nil {
		return
	}
	settings, err := svc.GetSMSProviderSettings(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	writeSMSProviderSettings(c, settings)
}

func decodeSMSProviderAPIKey(c *gin.Context, request *smsProviderAPIKeyRequest) bool {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, smsProviderSettingsBodyLimit)
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(request); err != nil {
		response.BadRequest(c, "接码供应商配置请求无效")
		return false
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		response.BadRequest(c, "接码供应商配置请求无效")
		return false
	}
	return true
}

// SaveSMSProviderSettings stores the encrypted Giema key and returns only its
// configured status. The submitted key is never included in the response.
// PUT /api/v1/admin/settings/sms-receiver/providers
func (h *SettingHandler) SaveSMSProviderSettings(c *gin.Context) {
	setSMSProviderNoStore(c)
	svc := h.smsProviderServiceOrError(c)
	if svc == nil {
		return
	}
	var request smsProviderAPIKeyRequest
	if !decodeSMSProviderAPIKey(c, &request) {
		return
	}
	if strings.TrimSpace(request.APIKey) == "" {
		response.BadRequest(c, "请输入有效的 Giema 接码供应商密钥")
		return
	}
	if err := svc.SaveGiemaAPIKey(c.Request.Context(), request.APIKey); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	settings, err := svc.GetSMSProviderSettings(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	writeSMSProviderSettings(c, settings)
}
