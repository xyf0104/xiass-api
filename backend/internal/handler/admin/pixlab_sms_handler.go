package admin

import (
	"math"
	"strconv"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type pixlabSMSCardKeysRequest struct {
	CardKeys string `json:"card_keys" binding:"required,max=1048576"`
	Provider string `json:"provider"`
}

type pixlabSMSMemberFeeRequest struct {
	MemberFee float64 `json:"member_fee"`
}

type pixlabSMSCardKeyOrderRequest struct {
	CardKeyIDs []int64 `json:"card_key_ids"`
}

func (h *SettingHandler) pixlabSMSServiceOrError(c *gin.Context) *service.PixlabSMSService {
	if h.pixlabSMSService == nil {
		response.Error(c, 503, "接码服务尚未初始化")
		return nil
	}
	return h.pixlabSMSService
}

func pixlabSMSOwnerID(c *gin.Context) (int64, bool) {
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	return subject.UserID, ok && subject.UserID > 0
}

// GetPixlabSMSStatus returns only server-side queue counters.
func (h *SettingHandler) GetPixlabSMSStatus(c *gin.Context) {
	svc := h.pixlabSMSServiceOrError(c)
	if svc == nil {
		return
	}
	ownerID, _ := pixlabSMSOwnerID(c)
	status, err := svc.QueueStatus(c.Request.Context(), ownerID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	fee, err := svc.MemberFee(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{
		"queued_count": status.QueuedCount,
		"active_count": status.ActiveCount,
		"fee_amount":   fee,
	})
}

// UpdatePixlabSMSMemberFee lets an administrator set the charge applied to
// new member sessions. A session already in progress keeps its own snapshot.
func (h *SettingHandler) UpdatePixlabSMSMemberFee(c *gin.Context) {
	svc := h.pixlabSMSServiceOrError(c)
	if svc == nil {
		return
	}
	var req pixlabSMSMemberFeeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	if math.IsNaN(req.MemberFee) || math.IsInf(req.MemberFee, 0) {
		response.BadRequest(c, "会员接码价格必须是有效数字")
		return
	}
	fee, err := svc.UpdateMemberFee(c.Request.Context(), req.MemberFee)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"fee_amount": fee})
}

// AddPixlabSMSCardKeys accepts raw keys, encrypts them at rest, and appends
// them to the operator-controlled queue. The route is excluded from request-body auditing.
func (h *SettingHandler) AddPixlabSMSCardKeys(c *gin.Context) {
	svc := h.pixlabSMSServiceOrError(c)
	if svc == nil {
		return
	}
	var req pixlabSMSCardKeysRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	provider := strings.TrimSpace(req.Provider)
	if provider == "" {
		provider = service.SMSProviderPixlab
	}
	added, status, err := svc.AddCardKeysForProvider(c.Request.Context(), req.CardKeys, provider)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{
		"added_count":  added,
		"queued_count": status.QueuedCount,
	})
}

func (h *SettingHandler) ClearPixlabSMSCardKeys(c *gin.Context) {
	svc := h.pixlabSMSServiceOrError(c)
	if svc == nil {
		return
	}
	deleted, status, err := svc.ClearQueuedCardKeys(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{
		"deleted_count": deleted,
		"queued_count":  status.QueuedCount,
	})
}

// ListPixlabSMSCardKeys returns plaintext card keys to the authenticated
// administrator-only settings screen for direct queue management.
func (h *SettingHandler) ListPixlabSMSCardKeys(c *gin.Context) {
	svc := h.pixlabSMSServiceOrError(c)
	if svc == nil {
		return
	}
	keys, status, err := svc.ListCardKeys(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{
		"card_keys":    keys,
		"queued_count": status.QueuedCount,
		"active_count": status.ActiveCount,
	})
}

func (h *SettingHandler) DeletePixlabSMSCardKey(c *gin.Context) {
	svc := h.pixlabSMSServiceOrError(c)
	if svc == nil {
		return
	}
	cardKeyID, err := strconv.ParseInt(strings.TrimSpace(c.Param("card_key_id")), 10, 64)
	if err != nil || cardKeyID <= 0 {
		response.BadRequest(c, "卡密编号无效")
		return
	}
	status, err := svc.DeleteCardKey(c.Request.Context(), cardKeyID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{
		"queued_count": status.QueuedCount,
		"active_count": status.ActiveCount,
	})
}

func (h *SettingHandler) ReorderPixlabSMSCardKeys(c *gin.Context) {
	svc := h.pixlabSMSServiceOrError(c)
	if svc == nil {
		return
	}
	var req pixlabSMSCardKeyOrderRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "排序请求无效: "+err.Error())
		return
	}
	status, err := svc.ReorderCardKeys(c.Request.Context(), req.CardKeyIDs)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{
		"queued_count": status.QueuedCount,
		"active_count": status.ActiveCount,
	})
}

func (h *SettingHandler) RedeemPixlabSMSNumber(c *gin.Context) {
	svc := h.pixlabSMSServiceOrError(c)
	if svc == nil {
		return
	}
	ownerID, ok := pixlabSMSOwnerID(c)
	if !ok {
		response.Error(c, 401, "无法识别当前管理员")
		return
	}
	result, err := svc.Redeem(c.Request.Context(), ownerID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, result)
}

func (h *SettingHandler) ResumePixlabSMSNumber(c *gin.Context) {
	h.withPixlabSMSAction(c, "resume")
}

func (h *SettingHandler) CheckPixlabSMSNumber(c *gin.Context) {
	h.withPixlabSMSAction(c, "check")
}

func (h *SettingHandler) ChangePixlabSMSNumber(c *gin.Context) {
	h.withPixlabSMSAction(c, "change")
}

func (h *SettingHandler) CancelPixlabSMSNumber(c *gin.Context) {
	h.withPixlabSMSAction(c, "cancel")
}

func (h *SettingHandler) withPixlabSMSAction(c *gin.Context, action string) {
	svc := h.pixlabSMSServiceOrError(c)
	if svc == nil {
		return
	}
	ownerID, ok := pixlabSMSOwnerID(c)
	if !ok {
		response.Error(c, 401, "无法识别当前管理员")
		return
	}
	sessionID := strings.TrimSpace(c.Param("session_id"))
	var (
		result *service.PixlabSMSResult
		err    error
	)
	switch action {
	case "resume":
		result, err = svc.Resume(c.Request.Context(), ownerID, sessionID)
	case "check":
		result, err = svc.Check(c.Request.Context(), ownerID, sessionID)
	case "change":
		result, err = svc.ChangeNumber(c.Request.Context(), ownerID, sessionID)
	case "cancel":
		result, err = svc.Cancel(c.Request.Context(), ownerID, sessionID)
	}
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, result)
}
