package routes

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/handler"
	adminhandler "github.com/Wei-Shaw/sub2api/internal/handler/admin"
	servermiddleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

type paymentRateLimitSettingRepo struct {
	values map[string]string
}

func (r *paymentRateLimitSettingRepo) Get(_ context.Context, key string) (*service.Setting, error) {
	value, ok := r.values[key]
	if !ok {
		return nil, service.ErrSettingNotFound
	}
	return &service.Setting{Key: key, Value: value}, nil
}

func (r *paymentRateLimitSettingRepo) GetValue(_ context.Context, key string) (string, error) {
	value, ok := r.values[key]
	if !ok {
		return "", service.ErrSettingNotFound
	}
	return value, nil
}

func (r *paymentRateLimitSettingRepo) Set(_ context.Context, key, value string) error {
	r.values[key] = value
	return nil
}

func (r *paymentRateLimitSettingRepo) GetMultiple(_ context.Context, keys []string) (map[string]string, error) {
	values := make(map[string]string, len(keys))
	for _, key := range keys {
		if value, ok := r.values[key]; ok {
			values[key] = value
		}
	}
	return values, nil
}

func (r *paymentRateLimitSettingRepo) SetMultiple(_ context.Context, values map[string]string) error {
	for key, value := range values {
		r.values[key] = value
	}
	return nil
}

func (r *paymentRateLimitSettingRepo) GetAll(context.Context) (map[string]string, error) {
	values := make(map[string]string, len(r.values))
	for key, value := range r.values {
		values[key] = value
	}
	return values, nil
}

func (r *paymentRateLimitSettingRepo) Delete(_ context.Context, key string) error {
	delete(r.values, key)
	return nil
}

func TestAdminPaymentRoutesUsePanelRateLimiter(t *testing.T) {
	gin.SetMode(gin.TestMode)
	redisServer := miniredis.RunT(t)
	redisClient := redis.NewClient(&redis.Options{Addr: redisServer.Addr()})
	t.Cleanup(func() { _ = redisClient.Close() })

	settings := service.NewSettingService(&paymentRateLimitSettingRepo{values: map[string]string{
		service.SettingKeyPanelRateLimitSettings: `{"enabled":true,"user_rpm":1,"heavy_rpm":1,"exempt_admin":false,"public_ip_rpm":1}`,
	}}, &config.Config{})
	limiter := servermiddleware.NewPanelRateLimiter(redisClient, settings)

	const userID int64 = 42
	redisKey := "rate_limit:panel:global:user:42"
	require.NoError(t, redisServer.Set(redisKey, "1"))
	redisServer.SetTTL(redisKey, time.Minute)

	router := gin.New()
	v1 := router.Group("/api/v1")
	adminAuth := servermiddleware.AdminAuthMiddleware(func(c *gin.Context) {
		c.Set(string(servermiddleware.ContextKeyUser), servermiddleware.AuthSubject{UserID: userID})
		c.Set(string(servermiddleware.ContextKeyUserRole), service.RoleAdmin)
		c.Next()
	})
	pass := func(c *gin.Context) { c.Next() }
	RegisterPaymentRoutes(
		v1,
		&handler.PaymentHandler{},
		&handler.PaymentWebhookHandler{},
		&adminhandler.PaymentHandler{},
		servermiddleware.JWTAuthMiddleware(pass),
		adminAuth,
		servermiddleware.AuditLogMiddleware(pass),
		settings,
		limiter,
		redisClient,
	)

	request := httptest.NewRequest(http.MethodGet, "/api/v1/admin/payment/dashboard", nil)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	require.Equal(t, http.StatusTooManyRequests, response.Code)
	count, err := redisServer.Get(redisKey)
	require.NoError(t, err)
	require.Equal(t, "2", count)
}
