package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/gin-gonic/gin"
)

// Team child member operations are intentionally proxied through a separate
// service that controls the already-authenticated Chromium profile. XIASS
// never receives ChatGPT session cookies or stores page credentials.
const (
	teamChildMembersDefaultTimeout   = 30 * time.Second
	teamChildWorkflowProtocolHeader  = "X-XIASS-Team-Child-Protocol"
	teamChildWorkflowProtocolVersion = "4"
)

type teamChildMemberAutomationConfig struct {
	baseURL string
	client  *http.Client
}

func loadTeamChildMemberAutomationConfig() (teamChildMemberAutomationConfig, error) {
	baseURL := strings.TrimRight(strings.TrimSpace(os.Getenv("TEAM_CHILD_AUTOMATION_URL")), "/")
	if baseURL == "" {
		return teamChildMemberAutomationConfig{}, fmt.Errorf("TEAM_CHILD_AUTOMATION_URL is not configured")
	}
	parsed, err := http.NewRequest(http.MethodGet, baseURL, nil)
	if err != nil || parsed.URL.Scheme == "" || parsed.URL.Host == "" ||
		(parsed.URL.Scheme != "http" && parsed.URL.Scheme != "https") {
		return teamChildMemberAutomationConfig{}, fmt.Errorf("TEAM_CHILD_AUTOMATION_URL is invalid")
	}
	return teamChildMemberAutomationConfig{baseURL: baseURL, client: &http.Client{Timeout: teamChildMembersDefaultTimeout}}, nil
}

type teamChildMemberInviteRequest struct {
	Email string `json:"email" binding:"required"`
}

type teamChildMemberRoleRequest struct {
	Email string `json:"email" binding:"required"`
	Role  string `json:"role" binding:"required"`
}

type teamChildMemberRemoveRequest struct {
	Email string `json:"email" binding:"required"`
}

// teamChildProtectedMemberEmails is an instance-local deny list for the Team
// workspace owner/admin identity. The browser automation independently treats
// every upstream owner/admin row as protected; this value also protects a
// specific account if the upstream page temporarily labels it incorrectly.
func teamChildProtectedMemberEmails() map[string]struct{} {
	protected := make(map[string]struct{})
	for _, raw := range strings.FieldsFunc(os.Getenv("TEAM_CHILD_PROTECTED_MEMBER_EMAILS"), func(r rune) bool {
		return r == ',' || r == ';' || r == '\n' || r == '\r' || r == '\t' || r == ' '
	}) {
		email := strings.ToLower(strings.TrimSpace(raw))
		if email != "" {
			protected[email] = struct{}{}
		}
	}
	return protected
}

func isTeamChildProtectedMemberEmail(email string) bool {
	_, protected := teamChildProtectedMemberEmails()[strings.ToLower(strings.TrimSpace(email))]
	return protected
}

func (h *OpenAIOAuthHandler) teamChildMemberAutomationRequest(c *gin.Context, method, path string, payload any) {
	if !requireTeamChildAdminSession(c) {
		return
	}
	config, err := loadTeamChildMemberAutomationConfig()
	if err != nil {
		response.BadRequest(c, "Team child member automation is not configured")
		return
	}
	isWorkflowRequest := path == "/workflows" || strings.HasPrefix(path, "/workflows/")
	if (path == "/workflows" || path == "/workflows/reauthorize") && method == http.MethodPost {
		if err := requireCurrentTeamChildWorkflowProtocol(c.Request.Context(), config); err != nil {
			response.Error(c, http.StatusBadGateway, "Team 自动化运行组件版本不匹配，请完成运行组件更新后重试")
			return
		}
	}
	var body io.Reader
	if payload != nil {
		encoded, marshalErr := json.Marshal(payload)
		if marshalErr != nil {
			response.BadRequest(c, "invalid member operation payload")
			return
		}
		body = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(c.Request.Context(), method, config.baseURL+path, body)
	if err != nil {
		response.InternalError(c, "failed to create member operation request")
		return
	}
	if payload != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	serviceToken := strings.TrimSpace(os.Getenv("TEAM_CHILD_AUTOMATION_TOKEN"))
	if serviceToken == "" {
		response.BadRequest(c, "Team child member automation is not securely configured")
		return
	}
	request.Header.Set("X-XIASS-Team-Child-Token", serviceToken)
	result, err := config.client.Do(request)
	if err != nil {
		response.Error(c, http.StatusBadGateway, "服务器浏览器自动化服务不可用，请先登录或检查部署状态")
		return
	}
	defer func() { _ = result.Body.Close() }()
	resultBody, readErr := io.ReadAll(io.LimitReader(result.Body, 2<<20))
	if readErr != nil {
		response.Error(c, http.StatusBadGateway, "读取服务器浏览器操作结果失败")
		return
	}
	if result.StatusCode < http.StatusOK || result.StatusCode >= http.StatusMultipleChoices {
		message := teamChildAutomationErrorMessage(resultBody)
		response.Error(c, result.StatusCode, message)
		return
	}
	if isWorkflowRequest && result.Header.Get(teamChildWorkflowProtocolHeader) != teamChildWorkflowProtocolVersion {
		response.Error(c, http.StatusBadGateway, "Team 自动化运行组件版本不匹配，请完成运行组件更新后重试")
		return
	}
	var data any
	if len(resultBody) == 0 {
		data = gin.H{"ok": true}
	} else if json.Unmarshal(resultBody, &data) != nil {
		data = gin.H{"ok": true, "message": strings.TrimSpace(string(resultBody))}
	}
	response.Success(c, data)
}

func requireCurrentTeamChildWorkflowProtocol(ctx context.Context, config teamChildMemberAutomationConfig) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, config.baseURL+"/healthz", nil)
	if err != nil {
		return err
	}
	result, err := config.client.Do(request)
	if err != nil {
		return err
	}
	defer func() { _ = result.Body.Close() }()
	if result.StatusCode < http.StatusOK || result.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("automation health returned status %d", result.StatusCode)
	}
	if result.Header.Get(teamChildWorkflowProtocolHeader) != teamChildWorkflowProtocolVersion {
		return fmt.Errorf("automation workflow protocol mismatch")
	}
	return nil
}

func teamChildAutomationErrorMessage(body []byte) string {
	var payload struct {
		Error   string `json:"error"`
		Message string `json:"message"`
	}
	if json.Unmarshal(body, &payload) == nil {
		for _, candidate := range []string{payload.Error, payload.Message} {
			if message := strings.TrimSpace(candidate); message != "" && len(message) <= 512 {
				return message
			}
		}
	}
	return "服务器浏览器操作失败"
}

// TeamChildMemberAutomationStatus reports whether the module can talk to the
// persistent Chromium profile and whether ChatGPT members are currently ready.
func (h *OpenAIOAuthHandler) TeamChildMemberAutomationStatus(c *gin.Context) {
	h.teamChildMemberAutomationRequest(c, http.MethodGet, "/members", nil)
}

func (h *OpenAIOAuthHandler) ListTeamChildMembers(c *gin.Context) {
	h.teamChildMemberAutomationRequest(c, http.MethodGet, "/members", nil)
}

func (h *OpenAIOAuthHandler) RefreshTeamChildMembers(c *gin.Context) {
	h.teamChildMemberAutomationRequest(c, http.MethodPost, "/members/refresh", nil)
}

func (h *OpenAIOAuthHandler) InspectTeamChildSeat(c *gin.Context) {
	h.teamChildMemberAutomationRequest(c, http.MethodPost, "/members/inspect", nil)
}

func (h *OpenAIOAuthHandler) InviteTeamChildMember(c *gin.Context) {
	var req teamChildMemberInviteRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "请输入有效的成员邮箱")
		return
	}
	req.Email = normalizeTeamChildWorkflowEmail(req.Email)
	if !validTeamChildWorkflowEmail(req.Email) {
		response.BadRequest(c, "请输入有效的成员邮箱")
		return
	}
	h.teamChildMemberAutomationRequest(c, http.MethodPost, "/members/invite", req)
}

func (h *OpenAIOAuthHandler) UpdateTeamChildMember(c *gin.Context) {
	if !requireTeamChildAdminSession(c) {
		return
	}
	var req teamChildMemberRoleRequest
	if err := c.ShouldBindJSON(&req); err != nil || strings.TrimSpace(req.Role) == "" {
		response.BadRequest(c, "请输入有效的成员邮箱和角色")
		return
	}
	req.Email = normalizeTeamChildWorkflowEmail(req.Email)
	req.Role = strings.TrimSpace(req.Role)
	if !validTeamChildWorkflowEmail(req.Email) {
		response.BadRequest(c, "请输入有效的成员邮箱和角色")
		return
	}
	if err := validateTeamChildMemberRole(req.Role); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	if isTeamChildProtectedMemberEmail(req.Email) {
		response.Forbidden(c, "受保护的管理员账号不可编辑")
		return
	}
	h.teamChildMemberAutomationRequest(c, http.MethodPatch, "/members", req)
}

func (h *OpenAIOAuthHandler) RemoveTeamChildMember(c *gin.Context) {
	if !requireTeamChildAdminSession(c) {
		return
	}
	var req teamChildMemberRemoveRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "请输入有效的成员邮箱")
		return
	}
	req.Email = normalizeTeamChildWorkflowEmail(req.Email)
	if !validTeamChildWorkflowEmail(req.Email) {
		response.BadRequest(c, "请输入有效的成员邮箱")
		return
	}
	if isTeamChildProtectedMemberEmail(req.Email) {
		response.Forbidden(c, "受保护的管理员账号不可移除")
		return
	}
	h.teamChildMemberAutomationRequest(c, http.MethodDelete, "/members", req)
}

func validateTeamChildMemberRole(role string) error {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case "admin", "member":
		return nil
	default:
		return fmt.Errorf("成员角色无效")
	}
}
