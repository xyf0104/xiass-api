package admin

import (
	"errors"
	"strconv"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	servicepkg "github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type replaceUserGroupAccountAllowlistRequest struct {
	AccountIDs *[]int64 `json:"account_ids" binding:"required"`
}

func (h *GroupHandler) GetUserAccountRuntime(c *gin.Context) {
	c.Header("Cache-Control", "private, no-store, max-age=0")
	c.Header("Pragma", "no-cache")
	c.Header("Expires", "0")
	groupID, ok := parsePositiveGroupAllowlistID(c, "group ID")
	if !ok {
		return
	}
	allowlistService := h.userGroupAccountAllowlistService()
	if allowlistService == nil {
		response.ErrorFrom(c, infraerrors.New(503, "USER_GROUP_ACCOUNT_RUNTIME_UNAVAILABLE", "user-group account runtime is unavailable"))
		return
	}
	runtime, err := allowlistService.GetRuntime(c.Request.Context(), groupID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	accounts := make([]gin.H, 0, len(runtime.Accounts))
	for _, account := range runtime.Accounts {
		accounts = append(accounts, gin.H{
			"id": account.AccountID, "name": account.Name, "platform": account.Platform,
			"type": account.Type, "priority": account.Priority, "concurrency": account.Concurrency,
			"current_concurrency": account.CurrentConcurrency, "available": account.Available,
		})
	}
	users := make([]gin.H, 0, len(runtime.Users))
	for _, user := range runtime.Users {
		users = append(users, gin.H{
			"id": user.UserID, "username": user.Username, "email": user.Email,
			"current_concurrency": user.CurrentConcurrency,
			"active_account_ids":  user.ActiveAccountIDs,
		})
	}
	response.Success(c, gin.H{"snapshot_at": runtime.SnapshotAt, "accounts": accounts, "users": users})
}

func (h *GroupHandler) GetUserAccountAllowlist(c *gin.Context) {
	userID, groupID, ok := parseUserGroupAllowlistScope(c)
	if !ok {
		return
	}
	allowlistService := h.userGroupAccountAllowlistService()
	if allowlistService == nil {
		response.ErrorFrom(c, infraerrors.New(503, "USER_GROUP_ACCOUNT_RUNTIME_UNAVAILABLE", "user-group account runtime is unavailable"))
		return
	}
	selection, err := allowlistService.GetSelection(c.Request.Context(), userID, groupID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	candidates := make([]gin.H, 0, len(selection.Candidates))
	for _, account := range selection.Candidates {
		candidates = append(candidates, gin.H{"id": account.AccountID, "name": account.Name, "platform": account.Platform, "type": account.Type, "priority": account.Priority, "concurrency": account.Concurrency, "allowed": account.Allowed, "available": account.Available})
	}
	response.Success(c, gin.H{
		"restricted":  selection.Restricted,
		"account_ids": selection.AllowedAccountIDs,
		// Keep the descriptive alias for callers that consume the raw admin API.
		"allowed_account_ids": selection.AllowedAccountIDs,
		"candidates":          candidates,
	})
}

func (h *GroupHandler) ReplaceUserAccountAllowlist(c *gin.Context) {
	userID, groupID, ok := parseUserGroupAllowlistScope(c)
	if !ok {
		return
	}
	var request replaceUserGroupAccountAllowlistRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	allowlistService := h.userGroupAccountAllowlistService()
	if allowlistService == nil {
		response.ErrorFrom(c, infraerrors.New(503, "USER_GROUP_ACCOUNT_RUNTIME_UNAVAILABLE", "user-group account runtime is unavailable"))
		return
	}
	if err := allowlistService.Replace(c.Request.Context(), userID, groupID, *request.AccountIDs); err != nil {
		if errors.Is(err, servicepkg.ErrUserGroupAccountAllowlistInvalidID) || errors.Is(err, servicepkg.ErrUserGroupAccountAllowlistUnavailable) {
			response.BadRequest(c, err.Error())
			return
		}
		response.ErrorFrom(c, err)
		return
	}
	selection, err := allowlistService.GetSelection(c.Request.Context(), userID, groupID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{
		"restricted":          selection.Restricted,
		"account_ids":         selection.AllowedAccountIDs,
		"allowed_account_ids": selection.AllowedAccountIDs,
	})
}

func (h *GroupHandler) DeleteUserAccountAllowlist(c *gin.Context) {
	userID, groupID, ok := parseUserGroupAllowlistScope(c)
	if !ok {
		return
	}
	allowlistService := h.userGroupAccountAllowlistService()
	if allowlistService == nil {
		response.ErrorFrom(c, infraerrors.New(503, "USER_GROUP_ACCOUNT_RUNTIME_UNAVAILABLE", "user-group account runtime is unavailable"))
		return
	}
	if err := allowlistService.Restore(c.Request.Context(), userID, groupID); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"restricted": false, "allowed_account_ids": []int64{}})
}

func (h *GroupHandler) userGroupAccountAllowlistService() *servicepkg.AdminUserGroupAccountAllowlistService {
	return h.allowlistService
}

func parseUserGroupAllowlistScope(c *gin.Context) (int64, int64, bool) {
	groupID, ok := parsePositiveGroupAllowlistID(c, "group ID")
	if !ok {
		return 0, 0, false
	}
	userID, err := strconv.ParseInt(c.Param("user_id"), 10, 64)
	if err != nil || userID <= 0 {
		response.BadRequest(c, "Invalid user ID")
		return 0, 0, false
	}
	return userID, groupID, true
}

func parsePositiveGroupAllowlistID(c *gin.Context, name string) (int64, bool) {
	groupID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || groupID <= 0 {
		response.BadRequest(c, "Invalid "+name)
		return 0, false
	}
	return groupID, true
}
