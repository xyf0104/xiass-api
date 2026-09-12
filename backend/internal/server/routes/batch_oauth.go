package routes

import (
	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/gin-gonic/gin"
)

func registerBatchOAuthRoutes(admin *gin.RouterGroup, h *handler.Handlers) {
	tasks := admin.Group("/openai/batch-oauth/tasks")
	oauth := h.Admin.OpenAIOAuth
	tasks.POST("", oauth.StartBatchOAuthTask)
	tasks.GET("", oauth.ListBatchOAuthTasks)
	tasks.GET("/:task_id", oauth.GetBatchOAuthTask)
	tasks.DELETE("/:task_id", oauth.DeleteBatchOAuthTask)
	tasks.POST("/:task_id/complete", oauth.CompleteBatchOAuthTask)
	tasks.POST("/:task_id/cancel", oauth.CancelBatchOAuthTask)
	tasks.POST("/:task_id/restart", oauth.RestartBatchOAuthTask)
	tasks.GET("/:task_id/sms", oauth.BatchOAuthSMSAction)
	tasks.POST("/:task_id/sms/:action", oauth.BatchOAuthSMSAction)
}
