package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/handler/dto"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

const teamChildSecretBodyLimit = 8 * 1024

type openAITeamChildWorkflowSecret struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type openAIAccountReauthorizationCredentialsRequest struct {
	Email string `json:"email" binding:"required"`
	// Password is optional because some OpenAI accounts transition directly from
	// email to an email challenge, workspace choice, or OAuth callback.
	// An explicit empty value clears any previously saved password.
	Password string `json:"password"`
}

// SaveOpenAIAccountReauthorizationCredentials saves an administrator-provided
// OpenAI OAuth login only for the private reauthorization runner. It does not
// expose the password through Account DTOs or accept arbitrary account fields.
// POST /api/v1/admin/openai/accounts/:id/reauthorization-credentials
func (h *OpenAIOAuthHandler) SaveOpenAIAccountReauthorizationCredentials(c *gin.Context) {
	if !requireTeamChildAdminSession(c) {
		return
	}
	if h == nil || h.adminService == nil || h.secretEncryptor == nil {
		response.InternalError(c, "OpenAI 重新授权密码服务不可用")
		return
	}
	accountID, err := parseOpenAIAccountRouteID(c)
	if err != nil || accountID <= 0 {
		response.BadRequest(c, "OpenAI OAuth 账号 ID 无效")
		return
	}
	if err := ensureAdminAccountManagementAccess(c.Request.Context(), h.adminService, accountID); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	var req openAIAccountReauthorizationCredentialsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "登录邮箱无效")
		return
	}
	email := normalizeTeamChildWorkflowEmail(req.Email)
	if !validTeamChildWorkflowEmail(email) {
		response.BadRequest(c, "登录邮箱格式无效")
		return
	}
	// Preserve the exact password submitted by the administrator. Leading or
	// trailing spaces can be a valid password character. A deliberately empty
	// value means this account should wait for manual entry if OpenAI asks for a
	// password, so only validate a non-empty submitted password.
	if req.Password != "" && (len(req.Password) < 8 || len(req.Password) > 256 || strings.TrimSpace(req.Password) == "") {
		response.BadRequest(c, "登录密码长度无效")
		return
	}

	account, err := h.adminService.GetAccount(c.Request.Context(), accountID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	if account.Platform != service.PlatformOpenAI || !account.IsOAuth() {
		response.BadRequest(c, "仅支持普通 OpenAI OAuth 账号")
		return
	}
	if account.IsCredentialShadow() {
		response.BadRequest(c, "Spark 影子账号使用母账号凭据，不能保存独立登录信息")
		return
	}
	if _, teamChild := teamChildAccountWorkflowEmail(account); teamChild {
		response.BadRequest(c, "Team 子号使用工作流生成的登录信息，不能覆盖")
		return
	}

	credentials := map[string]any{
		service.OpenAIOAuthReauthorizationEmailCredentialKey: email,
	}
	if req.Password == "" {
		// UpdateAccount preserves sensitive values that are omitted from an
		// ordinary update. The dedicated endpoint must explicitly overwrite this
		// key so an administrator can intentionally remove an old password.
		credentials[service.OpenAIOAuthReauthorizationPasswordCredentialKey] = nil
	} else {
		ciphertext, err := h.secretEncryptor.Encrypt(req.Password)
		if err != nil {
			response.InternalError(c, "登录密码加密失败")
			return
		}
		credentials[service.OpenAIOAuthReauthorizationPasswordCredentialKey] = ciphertext
	}
	updated, err := h.adminService.UpdateAccount(c.Request.Context(), accountID, &service.UpdateAccountInput{
		Credentials:                           credentials,
		AllowOpenAIReauthorizationCredentials: true,
	})
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, dto.AccountFromService(updated))
}

// fetchTeamChildWorkflowSecret is the only bridge from the isolated browser
// service into XIASS account storage. It is service-token authenticated,
// protocol pinned, bounded, and never returns the secret through an ordinary
// account response.
func (h *OpenAIOAuthHandler) fetchTeamChildWorkflowSecret(ctx context.Context, workflowID string) (*openAITeamChildWorkflowSecret, error) {
	workflowID = strings.TrimSpace(workflowID)
	if !validTeamChildWorkflowID(workflowID) {
		return nil, fmt.Errorf("invalid Team-child workflow ID")
	}
	config, err := loadTeamChildMemberAutomationConfig()
	if err != nil {
		return nil, err
	}
	serviceToken := strings.TrimSpace(teamChildAutomationServiceToken())
	if serviceToken == "" {
		return nil, fmt.Errorf("team-child automation token is unavailable")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, config.baseURL+"/workflows/"+url.PathEscape(workflowID)+"/secret", nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("X-XIASS-Team-Child-Token", serviceToken)
	result, err := config.client.Do(request)
	if err != nil {
		return nil, err
	}
	defer func() { _ = result.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(result.Body, teamChildSecretBodyLimit+1))
	if err != nil {
		return nil, err
	}
	if len(body) > teamChildSecretBodyLimit {
		return nil, fmt.Errorf("team-child secret response is too large")
	}
	if result.StatusCode < http.StatusOK || result.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("team-child secret request returned HTTP %d", result.StatusCode)
	}
	if result.Header.Get(teamChildWorkflowProtocolHeader) != teamChildWorkflowProtocolVersion {
		return nil, fmt.Errorf("team-child workflow protocol mismatch")
	}
	var secret openAITeamChildWorkflowSecret
	if err := json.Unmarshal(body, &secret); err != nil {
		return nil, err
	}
	secret.Email = normalizeTeamChildWorkflowEmail(secret.Email)
	if !validTeamChildWorkflowEmail(secret.Email) || (secret.Password != "" && (len(secret.Password) < 8 || len(secret.Password) > 256)) {
		return nil, fmt.Errorf("team-child workflow secret is invalid")
	}
	return &secret, nil
}

func teamChildAutomationServiceToken() string {
	return strings.TrimSpace(os.Getenv("TEAM_CHILD_AUTOMATION_TOKEN"))
}

// RevealTeamChildWorkflowPassword is retained for legacy Team-child workflows
// that already have a saved password. New email-code registrations return no
// password, and this step-up protected route reports that it is unavailable.
func (h *OpenAIOAuthHandler) RevealTeamChildWorkflowPassword(c *gin.Context) {
	if !requireTeamChildAdminSession(c) {
		return
	}
	workflowID := strings.TrimSpace(c.Param("workflow_id"))
	secret, err := h.fetchTeamChildWorkflowSecret(c.Request.Context(), workflowID)
	if err != nil || secret.Password == "" {
		response.Error(c, http.StatusConflict, "当前工作流登录密码不可用")
		return
	}
	response.Success(c, secret)
}

// RevealTeamChildAccountPassword decrypts only accounts explicitly imported by
// the Team-child workflow. It cannot be used as a generic credential export.
func (h *OpenAIOAuthHandler) RevealTeamChildAccountPassword(c *gin.Context) {
	if !requireTeamChildAdminSession(c) {
		return
	}
	if h == nil || h.adminService == nil || h.secretEncryptor == nil {
		response.InternalError(c, "Team 子号密码服务不可用")
		return
	}
	accountID, err := strconv.ParseInt(strings.TrimSpace(c.Param("account_id")), 10, 64)
	if err != nil || accountID <= 0 {
		response.BadRequest(c, "账号 ID 无效")
		return
	}
	account, err := h.adminService.GetAccount(c.Request.Context(), accountID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	email, teamChild := teamChildAccountWorkflowEmail(account)
	ciphertext, _ := account.Credentials[service.OpenAITeamChildPasswordCredentialKey].(string)
	if !teamChild || strings.TrimSpace(ciphertext) == "" {
		response.NotFound(c, "该账号没有可查看的 Team 子号登录密码")
		return
	}
	password, err := h.secretEncryptor.Decrypt(ciphertext)
	if err != nil || len(password) < 8 || len(password) > 256 {
		response.InternalError(c, "Team 子号登录密码无法解密")
		return
	}
	response.Success(c, openAITeamChildWorkflowSecret{Email: email, Password: password})
}
