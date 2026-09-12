package routes

import (
	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/gin-gonic/gin"
)

func registerAccountPoolRoutes(admin *gin.RouterGroup, h *handler.Handlers) {
	pools := admin.Group("/account-pools")
	pools.GET("", h.Admin.Account.ListAccountPools)
	pools.POST("", h.Admin.Account.CreateAccountPool)
	pools.PUT("/:id", h.Admin.Account.RenameAccountPool)
	pools.DELETE("/:id", h.Admin.Account.DeleteAccountPool)
	pools.POST("/:id/accounts", h.Admin.Account.AssignAccountPool)
	pools.PUT("/:id/proxy", h.Admin.Account.SetAccountPoolProxy)
}
