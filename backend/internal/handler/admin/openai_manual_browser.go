package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/redisclient"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/gin-gonic/gin"
)

const (
	manualBrowserSessionTTL = 12 * time.Hour
)

type persistedManualBrowserOwner struct {
	SessionID string    `json:"session_id"`
	ProxyID   *int64    `json:"proxy_id,omitempty"`
	ProxyName string    `json:"proxy_name,omitempty"`
	EgressIP  string    `json:"egress_ip,omitempty"`
	ExpiresAt time.Time `json:"expires_at"`
}

type manualBrowserStartRequest struct {
	ProxyID *int64 `json:"proxy_id"`
}

type manualBrowserAutomationSessionRequest struct {
	OwnerID int64             `json:"owner_id"`
	Proxy   map[string]string `json:"proxy,omitempty"`
}

type manualBrowserSessionResponse struct {
	Active     bool   `json:"active"`
	SessionID  string `json:"session_id,omitempty"`
	ProxyID    *int64 `json:"proxy_id,omitempty"`
	ProxyName  string `json:"proxy_name,omitempty"`
	ExpiresAt  string `json:"expires_at,omitempty"`
	CurrentURL string `json:"current_url,omitempty"`
	EgressIP   string `json:"egress_ip,omitempty"`
}

var errManualBrowserSessionMissing = errors.New("manual browser session is missing")

func (h *OpenAIOAuthHandler) ManualBrowserSession(c *gin.Context) {
	adminID, ok := manualBrowserAdminID(c)
	if !ok {
		return
	}
	owner, found, err := h.teamBrowserStore.getManualOwner(c.Request.Context(), adminID)
	if err != nil {
		response.Error(c, http.StatusServiceUnavailable, "临时浏览器会话暂时不可用")
		return
	}
	if !found {
		response.Success(c, manualBrowserSessionResponse{Active: false})
		return
	}
	data, err := h.manualBrowserAutomation(c.Request.Context(), adminID, http.MethodGet, "/manual-browser/sessions/"+owner.SessionID, nil)
	if errors.Is(err, errManualBrowserSessionMissing) {
		_ = h.teamBrowserStore.deleteManualOwner(c.Request.Context(), adminID)
		response.Success(c, manualBrowserSessionResponse{Active: false})
		return
	}
	if err != nil {
		response.Error(c, http.StatusBadGateway, "独立浏览器暂时不可用")
		return
	}
	response.Success(c, manualBrowserSessionResponse{
		Active: true, SessionID: owner.SessionID, ProxyID: owner.ProxyID,
		ProxyName: owner.ProxyName, ExpiresAt: owner.ExpiresAt.UTC().Format(time.RFC3339),
		CurrentURL: stringField(data, "current_url"),
		EgressIP:   stringField(data, "egress_ip"),
	})
}

func (h *OpenAIOAuthHandler) CreateManualBrowserSession(c *gin.Context) {
	adminID, ok := manualBrowserAdminID(c)
	if !ok {
		return
	}
	var input manualBrowserStartRequest
	if err := c.ShouldBindJSON(&input); err != nil && !errors.Is(err, io.EOF) {
		response.BadRequest(c, "浏览器出口选择无效")
		return
	}
	_, err := loadTeamChildBrowserConfig()
	if err != nil {
		response.BadRequest(c, "服务器 Chromium 尚未启用")
		return
	}
	if _, err := loadTeamChildMemberAutomationConfig(); err != nil || strings.TrimSpace(os.Getenv("TEAM_CHILD_AUTOMATION_TOKEN")) == "" {
		response.BadRequest(c, "服务器浏览器服务尚未配置")
		return
	}
	proxy, proxyName, err := h.manualBrowserProxy(c.Request.Context(), input.ProxyID)
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	owner, found, err := h.teamBrowserStore.getManualOwner(c.Request.Context(), adminID)
	if err != nil {
		response.Error(c, http.StatusServiceUnavailable, "临时浏览器会话暂时不可用")
		return
	}
	if found && !sameManualBrowserProxy(owner.ProxyID, input.ProxyID) {
		response.ErrorWithDetails(c, http.StatusConflict, "结束当前浏览器会话后才能切换代理出口。", "MANUAL_BROWSER_END_BEFORE_SWITCH", nil)
		return
	}
	if found {
		_, err = h.manualBrowserAutomation(c.Request.Context(), adminID, http.MethodGet, "/manual-browser/sessions/"+owner.SessionID, nil)
		if err != nil && !errors.Is(err, errManualBrowserSessionMissing) {
			response.Error(c, http.StatusBadGateway, "独立浏览器暂时不可用")
			return
		}
		if errors.Is(err, errManualBrowserSessionMissing) {
			_ = h.teamBrowserStore.deleteManualOwner(c.Request.Context(), adminID)
			found = false
		}
	}
	if !found {
		payload := manualBrowserAutomationSessionRequest{OwnerID: adminID, Proxy: proxy}
		data, err := h.manualBrowserAutomation(c.Request.Context(), adminID, http.MethodPost, "/manual-browser/sessions", payload)
		if err != nil {
			response.Error(c, http.StatusBadGateway, "独立浏览器启动失败；未回退到直连。请检查服务器浏览器和所选代理节点。")
			return
		}
		sessionID := stringField(data, "session_id")
		if !validTeamChildBrowserControllerID(sessionID) {
			response.Error(c, http.StatusBadGateway, "独立浏览器返回了无效会话")
			return
		}
		owner = persistedManualBrowserOwner{SessionID: sessionID, ProxyID: input.ProxyID, ProxyName: proxyName, EgressIP: stringField(data, "egress_ip")}
		owner.ExpiresAt, err = time.Parse(time.RFC3339, stringField(data, "expires_at"))
		if err != nil || !owner.ExpiresAt.After(time.Now()) || time.Until(owner.ExpiresAt) > 24*time.Hour {
			_, _ = h.manualBrowserAutomation(c.Request.Context(), adminID, http.MethodDelete, "/manual-browser/sessions/"+sessionID, nil)
			response.Error(c, http.StatusBadGateway, "独立浏览器返回了无效期限")
			return
		}
		if err := h.teamBrowserStore.setManualOwner(c.Request.Context(), adminID, owner, time.Until(owner.ExpiresAt)); err != nil {
			_, _ = h.manualBrowserAutomation(c.Request.Context(), adminID, http.MethodDelete, "/manual-browser/sessions/"+sessionID, nil)
			response.Error(c, http.StatusServiceUnavailable, "临时浏览器会话无法保存，请重试")
			return
		}
	}
	response.Success(c, manualBrowserSessionResponse{
		Active: true, SessionID: owner.SessionID, ProxyID: owner.ProxyID, ProxyName: owner.ProxyName,
		ExpiresAt: owner.ExpiresAt.UTC().Format(time.RFC3339),
		EgressIP:  owner.EgressIP,
	})
}

func (h *OpenAIOAuthHandler) EndManualBrowserSession(c *gin.Context) {
	adminID, ok := manualBrowserAdminID(c)
	if !ok {
		return
	}
	owner, found, err := h.teamBrowserStore.getManualOwner(c.Request.Context(), adminID)
	if err != nil {
		response.Error(c, http.StatusServiceUnavailable, "临时浏览器会话暂时不可用")
		return
	}
	if found {
		_, err := h.manualBrowserAutomation(c.Request.Context(), adminID, http.MethodDelete, "/manual-browser/sessions/"+owner.SessionID, nil)
		if err != nil && !errors.Is(err, errManualBrowserSessionMissing) {
			response.Error(c, http.StatusBadGateway, "结束浏览器会话失败，请稍后重试")
			return
		}
		if err := h.teamBrowserStore.deleteManualOwner(c.Request.Context(), adminID); err != nil {
			response.Error(c, http.StatusServiceUnavailable, "浏览器已关闭，但会话状态清理失败")
			return
		}
	}
	response.Success(c, gin.H{"ended": true})
}

func (h *OpenAIOAuthHandler) ManualBrowserCommand(c *gin.Context) {
	adminID, owner, ok := h.ownedManualBrowserSession(c)
	if !ok {
		return
	}
	// Keystrokes may be passwords; they must never enter the audit log.
	middleware.SkipAudit(c)
	action := c.Param("action")
	if c.Request.Method == http.MethodGet && strings.HasSuffix(c.FullPath(), "/tabs") {
		action = "tabs"
	}
	if action != "navigate" && action != "action" && action != "tabs" && action != "select-tab" && action != "resize" {
		response.NotFound(c, "browser operation not found")
		return
	}
	method := http.MethodPost
	var payload any
	if action == "tabs" {
		method = http.MethodGet
	} else {
		var body map[string]any
		if err := c.ShouldBindJSON(&body); err != nil {
			response.BadRequest(c, "浏览器操作参数无效")
			return
		}
		payload = body
	}
	data, err := h.manualBrowserAutomation(c.Request.Context(), adminID, method, "/manual-browser/sessions/"+owner.SessionID+"/"+action, payload)
	if err != nil {
		response.Error(c, http.StatusBadGateway, "浏览器操作失败，请检查当前页面和网络出口")
		return
	}
	response.Success(c, data)
}

func (h *OpenAIOAuthHandler) ManualBrowserScreenshot(c *gin.Context) {
	adminID, owner, ok := h.ownedManualBrowserSession(c)
	if !ok {
		return
	}
	body, contentType, err := h.manualBrowserAutomationRaw(c.Request.Context(), adminID, http.MethodGet, "/manual-browser/sessions/"+owner.SessionID+"/screenshot", nil)
	if err != nil || !strings.HasPrefix(contentType, "image/jpeg") {
		response.Error(c, http.StatusBadGateway, "读取浏览器画面失败")
		return
	}
	c.Header("Cache-Control", "no-store")
	c.Data(http.StatusOK, "image/jpeg", body)
}

func manualBrowserAdminID(c *gin.Context) (int64, bool) {
	c.Header("Cache-Control", "no-store")
	if !requireTeamChildAdminSession(c) {
		return 0, false
	}
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok || subject.UserID <= 0 {
		response.Unauthorized(c, "administrator session is required")
		return 0, false
	}
	return subject.UserID, true
}

func (h *OpenAIOAuthHandler) ownedManualBrowserSession(c *gin.Context) (int64, persistedManualBrowserOwner, bool) {
	adminID, ok := manualBrowserAdminID(c)
	if !ok {
		return 0, persistedManualBrowserOwner{}, false
	}
	sessionID := strings.TrimSpace(c.Param("id"))
	if !validTeamChildBrowserControllerID(sessionID) {
		response.NotFound(c, "browser session not found")
		return 0, persistedManualBrowserOwner{}, false
	}
	owner, found, err := h.teamBrowserStore.getManualOwner(c.Request.Context(), adminID)
	if err != nil {
		response.Error(c, http.StatusServiceUnavailable, "临时浏览器会话暂时不可用")
		return 0, persistedManualBrowserOwner{}, false
	}
	if !found || owner.SessionID != sessionID {
		response.NotFound(c, "browser session not found")
		return 0, persistedManualBrowserOwner{}, false
	}
	return adminID, owner, true
}

func (h *OpenAIOAuthHandler) manualBrowserProxy(ctx context.Context, proxyID *int64) (map[string]string, string, error) {
	if proxyID == nil {
		return nil, "服务器直连", nil
	}
	proxy, err := h.batchOAuthBrowserProxy(ctx, proxyID)
	if err != nil {
		return nil, "", fmt.Errorf("所选代理节点不可用：%w", err)
	}
	model, err := h.adminService.GetProxy(ctx, *proxyID)
	if err != nil || model == nil {
		return nil, "", errors.New("所选代理节点不存在或不可用")
	}
	return proxy, model.Name, nil
}

func sameManualBrowserProxy(left, right *int64) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func (h *OpenAIOAuthHandler) manualBrowserAutomation(ctx context.Context, ownerID int64, method, path string, payload any) (map[string]any, error) {
	body, contentType, err := h.manualBrowserAutomationRaw(ctx, ownerID, method, path, payload)
	if err != nil {
		return nil, err
	}
	if strings.HasPrefix(contentType, "image/") {
		return nil, errors.New("unexpected browser response")
	}
	var data map[string]any
	if len(body) > 0 && json.Unmarshal(body, &data) != nil {
		return nil, errors.New("browser service response is invalid")
	}
	return data, nil
}

func (h *OpenAIOAuthHandler) manualBrowserAutomationRaw(ctx context.Context, ownerID int64, method, path string, payload any) ([]byte, string, error) {
	config, err := loadTeamChildMemberAutomationConfig()
	if err != nil {
		return nil, "", err
	}
	config.client.Timeout = 60 * time.Second
	token := strings.TrimSpace(os.Getenv("TEAM_CHILD_AUTOMATION_TOKEN"))
	if token == "" {
		return nil, "", errors.New("browser service is not configured")
	}
	endpoint := strings.TrimRight(config.baseURL, "/") + path
	query := url.Values{"owner_id": []string{strconv.FormatInt(ownerID, 10)}}
	if strings.Contains(endpoint, "?") {
		endpoint += "&" + query.Encode()
	} else {
		endpoint += "?" + query.Encode()
	}
	var requestBody []byte
	if payload != nil {
		requestBody, err = json.Marshal(payload)
		if err != nil {
			return nil, "", err
		}
	}
	request, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(requestBody))
	if err != nil {
		return nil, "", err
	}
	request.Header.Set("X-XIASS-Team-Child-Token", token)
	if len(requestBody) > 0 {
		request.Header.Set("Content-Type", "application/json")
	}
	result, err := config.client.Do(request)
	if err != nil {
		return nil, "", err
	}
	defer result.Body.Close()
	body, err := io.ReadAll(io.LimitReader(result.Body, 16<<20))
	if err != nil {
		return nil, "", err
	}
	if result.StatusCode == http.StatusNotFound {
		return nil, "", errManualBrowserSessionMissing
	}
	if result.StatusCode < 200 || result.StatusCode >= 300 {
		var payload struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(body, &payload)
		if payload.Error != "" {
			return nil, "", fmt.Errorf("%s", payload.Error)
		}
		return nil, "", fmt.Errorf("browser service returned status %d", result.StatusCode)
	}
	return body, result.Header.Get("Content-Type"), nil
}

func stringField(value map[string]any, key string) string {
	field, _ := value[key].(string)
	return field
}

func (s *openAITeamBrowserStore) getManualOwner(ctx context.Context, adminID int64) (persistedManualBrowserOwner, bool, error) {
	if s == nil || adminID <= 0 {
		return persistedManualBrowserOwner{}, false, nil
	}
	key := manualBrowserOwnerPrefix + strconv.FormatInt(adminID, 10)
	if client := s.redisClient(); client != nil {
		payload, err := client.Get(ctx, key).Bytes()
		if errors.Is(err, redisclient.Nil) {
			return persistedManualBrowserOwner{}, false, nil
		}
		if err != nil {
			return persistedManualBrowserOwner{}, false, err
		}
		var owner persistedManualBrowserOwner
		if json.Unmarshal(payload, &owner) != nil || !validTeamChildBrowserControllerID(owner.SessionID) || !owner.ExpiresAt.After(time.Now()) {
			_ = client.Del(ctx, key).Err()
			return persistedManualBrowserOwner{}, false, nil
		}
		return owner, true, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneLocked(s.now())
	owner, ok := s.manualOwners[adminID]
	return owner, ok, nil
}

func (s *openAITeamBrowserStore) setManualOwner(ctx context.Context, adminID int64, owner persistedManualBrowserOwner, ttl time.Duration) error {
	if s == nil || adminID <= 0 || !validTeamChildBrowserControllerID(owner.SessionID) || ttl <= 0 {
		return errors.New("manual browser owner session is invalid")
	}
	if owner.ExpiresAt.IsZero() {
		owner.ExpiresAt = s.now().Add(ttl)
	}
	key := manualBrowserOwnerPrefix + strconv.FormatInt(adminID, 10)
	if client := s.redisClient(); client != nil {
		payload, err := json.Marshal(owner)
		if err != nil {
			return err
		}
		return client.Set(ctx, key, payload, ttl).Err()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.manualOwners[adminID] = owner
	return nil
}

func (s *openAITeamBrowserStore) deleteManualOwner(ctx context.Context, adminID int64) error {
	if s == nil || adminID <= 0 {
		return nil
	}
	key := manualBrowserOwnerPrefix + strconv.FormatInt(adminID, 10)
	if client := s.redisClient(); client != nil {
		return client.Del(ctx, key).Err()
	}
	s.mu.Lock()
	delete(s.manualOwners, adminID)
	s.mu.Unlock()
	return nil
}
