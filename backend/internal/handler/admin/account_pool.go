package admin

import (
	"encoding/json"
	"strconv"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

func (h *AccountHandler) accountPools(c *gin.Context) service.AccountPoolService {
	s, ok := h.adminService.(service.AccountPoolService)
	if !ok {
		response.ErrorFrom(c, service.ErrAccountPoolUnavailable)
		return nil
	}
	return s
}

func accountPoolID(c *gin.Context) (int64, bool) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "Invalid account pool ID")
		return 0, false
	}
	return id, true
}

func (h *AccountHandler) ListAccountPools(c *gin.Context) {
	s := h.accountPools(c)
	if s == nil {
		return
	}
	pools, err := s.ListAccountPools(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"items": pools})
}

func (h *AccountHandler) CreateAccountPool(c *gin.Context) {
	s := h.accountPools(c)
	if s == nil {
		return
	}
	var req struct {
		Name    string `json:"name" binding:"required"`
		ProxyID *int64 `json:"proxy_id"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	pool, err := s.CreateAccountPool(c.Request.Context(), req.Name, req.ProxyID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, pool)
}

func (h *AccountHandler) RenameAccountPool(c *gin.Context) {
	id, ok := accountPoolID(c)
	if !ok {
		return
	}
	s := h.accountPools(c)
	if s == nil {
		return
	}
	var req struct {
		Name string `json:"name" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	pool, err := s.RenameAccountPool(c.Request.Context(), id, req.Name)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, pool)
}

func (h *AccountHandler) DeleteAccountPool(c *gin.Context) {
	id, ok := accountPoolID(c)
	if !ok {
		return
	}
	s := h.accountPools(c)
	if s == nil {
		return
	}
	if err := s.DeleteAccountPool(c.Request.Context(), id); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"deleted": true})
}

func (h *AccountHandler) AssignAccountPool(c *gin.Context) {
	id, ok := accountPoolID(c)
	if !ok {
		return
	}
	s := h.accountPools(c)
	if s == nil {
		return
	}
	var req struct {
		AccountIDs []int64 `json:"account_ids" binding:"required,min=1,max=5000"`
		Remove     bool    `json:"remove"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	pool, err := s.AssignAccountPool(c.Request.Context(), id, req.AccountIDs, req.Remove)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, pool)
}

func (h *AccountHandler) SetAccountPoolProxy(c *gin.Context) {
	id, ok := accountPoolID(c)
	if !ok {
		return
	}
	s := h.accountPools(c)
	if s == nil {
		return
	}
	var req struct {
		ProxyID json.RawMessage `json:"proxy_id"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	// Missing is not direct: only explicit null clears the pool proxy.
	var proxyID *int64
	if len(req.ProxyID) == 0 || json.Unmarshal(req.ProxyID, &proxyID) != nil {
		response.BadRequest(c, "proxy_id must be an integer or explicit null")
		return
	}
	pool, err := s.SetAccountPoolProxy(c.Request.Context(), id, proxyID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, pool)
}
