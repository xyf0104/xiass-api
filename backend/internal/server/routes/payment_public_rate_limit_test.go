package routes

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/handler/admin"
	servermiddleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func newPaymentRoutesTestRouter(redisClient *redis.Client) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	v1 := router.Group("/api/v1")
	noop := func(c *gin.Context) { c.Next() }

	RegisterPaymentRoutes(
		v1,
		&handler.PaymentHandler{},
		&handler.PaymentWebhookHandler{},
		&admin.PaymentHandler{},
		servermiddleware.JWTAuthMiddleware(noop),
		servermiddleware.AdminAuthMiddleware(noop),
		servermiddleware.AuditLogMiddleware(noop),
		nil,
		nil,
		redisClient,
	)
	return router
}

func postPublicOrderVerify(router *gin.Engine, remoteAddr string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodPost, "/api/v1/payment/public/orders/verify", strings.NewReader(`{}`))
	request.Header.Set("Content-Type", "application/json")
	request.RemoteAddr = remoteAddr
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response
}

func TestPublicOrderVerifyRateLimitedPerIP(t *testing.T) {
	redisServer := miniredis.RunT(t)
	redisClient := redis.NewClient(&redis.Options{Addr: redisServer.Addr()})
	t.Cleanup(func() { _ = redisClient.Close() })

	router := newPaymentRoutesTestRouter(redisClient)
	for requestNumber := 1; requestNumber <= publicOrderVerifyRateLimit; requestNumber++ {
		response := postPublicOrderVerify(router, "198.51.100.20:1234")
		require.Equal(t, http.StatusBadRequest, response.Code, "request %d should reach the handler", requestNumber)
	}

	response := postPublicOrderVerify(router, "198.51.100.20:1234")
	require.Equal(t, http.StatusTooManyRequests, response.Code)
	require.Contains(t, response.Body.String(), "rate limit exceeded")

	response = postPublicOrderVerify(router, "198.51.100.21:1234")
	require.Equal(t, http.StatusBadRequest, response.Code)
}

func TestPublicOrderVerifyRateLimitFailsOpenWhenRedisUnavailable(t *testing.T) {
	redisClient := redis.NewClient(&redis.Options{
		Addr:         "127.0.0.1:1",
		DialTimeout:  50 * time.Millisecond,
		ReadTimeout:  50 * time.Millisecond,
		WriteTimeout: 50 * time.Millisecond,
	})
	t.Cleanup(func() { _ = redisClient.Close() })

	router := newPaymentRoutesTestRouter(redisClient)
	response := postPublicOrderVerify(router, "203.0.113.30:1234")
	require.Equal(t, http.StatusBadRequest, response.Code, "Redis outages must not block users who are mid-payment")
}
