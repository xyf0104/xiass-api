package routes

import (
	"time"

	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/middleware"
	servermiddleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
)

func RegisterVisitRoutes(v1 *gin.RouterGroup, h *handler.Handlers, jwtAuth servermiddleware.JWTAuthMiddleware, redisClient *redis.Client) {
	limiter := middleware.NewRateLimiter(redisClient)
	v1.POST("/visits", limiter.Limit("site-visits", 120, time.Minute), optionalVisitAuth(jwtAuth), h.Visit.Record)
}

func optionalVisitAuth(jwtAuth servermiddleware.JWTAuthMiddleware) gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.GetHeader("Authorization") != "" {
			jwtAuth(c)
			return
		}
		c.Next()
	}
}
