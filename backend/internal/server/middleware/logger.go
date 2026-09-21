package middleware

import (
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ip"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// Logger 请求日志中间件
func Logger() gin.HandlerFunc {
	return func(c *gin.Context) {
		// 开始时间
		startTime := time.Now()

		// 请求路径
		path := c.Request.URL.Path

		// 处理请求
		c.Next()

		// 跳过健康检查等高频探针路径的日志
		if path == "/health" || path == "/readyz" || path == "/setup/status" {
			return
		}

		endTime := time.Now()
		latency := endTime.Sub(startTime)

		method := c.Request.Method
		statusCode := c.Writer.Status()
		clientIP := ip.GetClientIP(c)
		protocol := c.Request.Proto
		accountID, hasAccountID := c.Request.Context().Value(ctxkey.AccountID).(int64)
		platform, _ := c.Request.Context().Value(ctxkey.Platform).(string)
		model, _ := c.Request.Context().Value(ctxkey.Model).(string)
		reason, rejected := GetIngressRejectReason(c)
		if rejected {
			recordIngressReject(c, reason)
			allowed, droppedSummary := globalIngressRejectAccessSampler.allow(endTime)
			if droppedSummary > 0 {
				logger.FromContext(c.Request.Context()).Info("ingress rejection access logs dropped",
					zap.String("component", "http.access"),
					zap.Uint64("dropped_count", droppedSummary),
					zap.Bool(logger.OpsSystemLogSkipField, true),
				)
			}
			if !allowed {
				return
			}
		}

		fields := []zap.Field{
			zap.String("component", "http.access"),
			zap.Int("status_code", statusCode),
			zap.Int64("latency_ms", latency.Milliseconds()),
			zap.String("client_ip", clientIP),
			zap.String("protocol", protocol),
			zap.String("method", method),
			zap.String("path", path),
		}
		if rejected {
			fields = append(fields,
				zap.String("ingress_reject_reason", string(reason)),
				zap.Bool(logger.OpsSystemLogSkipField, true),
			)
		}
		if hasAccountID && accountID > 0 {
			fields = append(fields, zap.Int64("account_id", accountID))
		}
		if platform != "" {
			fields = append(fields, zap.String("platform", platform))
		}
		if model != "" {
			fields = append(fields, zap.String("model", model))
		}
		if proxyID, proxyName, ok := service.OpsUpstreamProxyAttribution(c); ok {
			fields = append(fields, zap.String("proxy_name", proxyName))
			if proxyID != nil {
				fields = append(fields, zap.Int64("proxy_id", *proxyID))
			}
		}
		for _, latencyField := range []struct {
			key  string
			name string
		}{
			{service.OpsAuthLatencyMsKey, "auth_latency_ms"},
			{service.OpsRoutingLatencyMsKey, "routing_latency_ms"},
			{service.OpsUpstreamLatencyMsKey, "upstream_latency_ms"},
			{service.OpsResponseLatencyMsKey, "response_latency_ms"},
			{service.OpsTimeToFirstTokenMsKey, "time_to_first_token_ms"},
		} {
			if value, ok := contextLatencyMs(c, latencyField.key); ok {
				fields = append(fields, zap.Int64(latencyField.name, value))
			}
		}

		l := logger.FromContext(c.Request.Context()).With(fields...)
		l.Info("http request completed", zap.Time("completed_at", endTime))

		if len(c.Errors) > 0 {
			l.Warn("http request contains gin errors", zap.String("errors", c.Errors.String()))
		}
	}
}

func contextLatencyMs(c *gin.Context, key string) (int64, bool) {
	if c == nil {
		return 0, false
	}
	value, ok := c.Get(key)
	if !ok {
		return 0, false
	}
	switch typed := value.(type) {
	case int:
		return int64(typed), typed >= 0
	case int64:
		return typed, typed >= 0
	case int32:
		return int64(typed), typed >= 0
	case uint64:
		return int64(typed), typed <= uint64(^uint64(0)>>1)
	default:
		return 0, false
	}
}
