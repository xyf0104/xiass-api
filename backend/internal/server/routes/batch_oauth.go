package routes

import (
	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/gin-gonic/gin"
)

func registerBatchOAuthRoutes(admin *gin.RouterGroup, h *handler.Handlers) {
	oauth := h.Admin.OpenAIOAuth
	tasks := admin.Group("/openai/batch-oauth/tasks")
	tasks.POST("", oauth.StartBatchOAuthTask)
	tasks.GET("", oauth.ListBatchOAuthTasks)
	tasks.GET("/:task_id", oauth.GetBatchOAuthTask)
	tasks.DELETE("/:task_id", oauth.DeleteBatchOAuthTask)
	tasks.POST("/:task_id/complete", oauth.CompleteBatchOAuthTask)
	tasks.POST("/:task_id/cancel", oauth.CancelBatchOAuthTask)
	tasks.POST("/:task_id/restart", oauth.RestartBatchOAuthTask)
	tasks.GET("/:task_id/sms", oauth.BatchOAuthSMSAction)
	tasks.POST("/:task_id/sms/:action", oauth.BatchOAuthSMSAction)

	reauthorization := admin.Group("/openai/reauthorization/tasks")
	admin.GET("/openai/reauthorization/accounts", oauth.ListOpenAIReauthorizationAccounts)
	reauthorization.POST("", oauth.StartOpenAIReauthorizationTask)
	reauthorization.GET("", oauth.ListOpenAIReauthorizationTasks)
	reauthorization.GET("/:task_id", oauth.GetOpenAIReauthorizationTask)
	reauthorization.DELETE("/:task_id", oauth.DeleteOpenAIReauthorizationTask)
	reauthorization.POST("/:task_id/complete", oauth.CompleteOpenAIReauthorizationTask)
	reauthorization.POST("/:task_id/cancel", oauth.CancelOpenAIReauthorizationTask)
	reauthorization.POST("/:task_id/restart", oauth.RestartOpenAIReauthorizationTask)
	reauthorization.GET("/:task_id/sms", oauth.OpenAIReauthorizationSMSAction)
	reauthorization.POST("/:task_id/sms/:action", oauth.OpenAIReauthorizationSMSAction)
}
