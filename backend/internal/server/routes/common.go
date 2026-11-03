package routes

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/repository"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
)

const readinessProbeTimeout = 1500 * time.Millisecond

type readinessProbe struct {
	name string
	run  func(context.Context) error
}

type readinessProbeResult struct {
	name string
	err  error
}

// RegisterCommonRoutes 注册通用路由（健康检查、状态等）
func RegisterCommonRoutes(r *gin.Engine, db *sql.DB, redisClient *redis.Client, cfg *config.Config, stores ...service.RefreshTokenCache) {
	// 健康检查
	r.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	migrationReadiness := cfg != nil && cfg.JWT.RefreshTokenMigrationReadiness
	// Match the fixed startup selection in NewRefreshTokenStore, not later config
	// or environment changes: readiness must not pretend the running store switched.
	refreshTokenStore := "redis"
	if cfg != nil && cfg.JWT.RefreshTokenStore != "" {
		refreshTokenStore = cfg.JWT.RefreshTokenStore
	}
	registerReadinessRoute(r, []readinessProbe{
		{
			name: "postgres",
			run: func(ctx context.Context) error {
				if db == nil {
					return errors.New("database is not configured")
				}
				if len(stores) == 1 {
					return repository.CheckRefreshTokenStoreReadiness(ctx, stores[0])
				}
				if migrationReadiness || len(stores) > 1 {
					return errors.New("running refresh token provider is unavailable")
				}
				return checkWritablePostgres(ctx, db, refreshTokenStore)
			},
		},
		{
			name: "redis",
			run: func(ctx context.Context) error {
				if redisClient == nil {
					return errors.New("redis is not configured")
				}
				return checkWritableRedis(ctx, redisClient)
			},
		},
	}, readinessProbeTimeout)

	// Claude Code 遥测日志（忽略，直接返回200）
	r.POST("/api/event_logging/batch", func(c *gin.Context) {
		c.Status(http.StatusOK)
	})

	// Setup status endpoint (always returns needs_setup: false in normal mode)
	// This is used by the frontend to detect when the service has restarted after setup
	r.GET("/setup/status", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"code": 0,
			"data": gin.H{
				"needs_setup": false,
				"step":        "completed",
			},
		})
	})
}

// A reachable replica cannot serve billing or session writes. These probes
// reject standby connections; they do not replace external primary fencing.
func checkWritablePostgres(ctx context.Context, db *sql.DB, refreshTokenStore string) error {
	if refreshTokenStore != "redis" && refreshTokenStore != "postgres" {
		return errors.New("refresh token store is invalid")
	}
	// Keep the provider's persistent-store prerequisites in the same round trip
	// as the writable-role and authority checks. Redis mode needs no PG session tables.
	schemaCheck := "TRUE"
	if refreshTokenStore == "postgres" {
		schemaCheck = `EXISTS (SELECT 1 FROM refresh_token_revocation_state WHERE singleton = TRUE)
			AND to_regclass('refresh_tokens') IS NOT NULL
			AND to_regclass('refresh_token_families') IS NOT NULL
			AND to_regclass('refresh_token_users') IS NOT NULL
			AND to_regclass('refresh_token_issuances') IS NOT NULL`
	}
	var writable, schemaReady bool
	var authority string
	err := db.QueryRowContext(ctx, `
		SELECT NOT pg_is_in_recovery() AND current_setting('transaction_read_only') = 'off',
			backend, `+schemaCheck+`
		FROM refresh_token_authority WHERE singleton = TRUE
	`).Scan(&writable, &authority, &schemaReady)
	if err != nil {
		return err
	}
	if !writable {
		return errors.New("database is read-only")
	}
	if authority != refreshTokenStore || !schemaReady {
		return errors.New("refresh token authority or schema is unavailable")
	}
	return nil
}

func checkWritableRedis(ctx context.Context, client *redis.Client) error {
	role, err := client.Do(ctx, "ROLE").Slice()
	if err != nil {
		return err
	}
	if len(role) == 0 || role[0] != "master" {
		return errors.New("redis is not a writable primary")
	}
	return nil
}

func registerReadinessRoute(r *gin.Engine, probes []readinessProbe, timeout time.Duration) {
	r.GET("/readyz", func(c *gin.Context) {
		if timeout <= 0 {
			timeout = readinessProbeTimeout
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), timeout)
		defer cancel()

		results := make(chan readinessProbeResult, len(probes))
		for _, probe := range probes {
			probe := probe
			go func() {
				results <- readinessProbeResult{name: probe.name, err: probe.run(ctx)}
			}()
		}

		checks := make(map[string]string, len(probes))
		ready := true
		remaining := len(probes)
		for remaining > 0 {
			select {
			case result := <-results:
				remaining--
				if result.err != nil {
					checks[result.name] = "unavailable"
					ready = false
					continue
				}
				checks[result.name] = "ok"
			case <-ctx.Done():
				// A dependency probe that ignores cancellation must not keep a
				// load-balancer health request hanging indefinitely.
				for _, probe := range probes {
					if _, seen := checks[probe.name]; !seen {
						checks[probe.name] = "unavailable"
					}
				}
				ready = false
				remaining = 0
			}
		}

		c.Header("Cache-Control", "no-store")
		payload := gin.H{
			"status": "ok",
			"checks": checks,
		}
		if !ready {
			payload["status"] = "unavailable"
			c.JSON(http.StatusServiceUnavailable, payload)
			return
		}
		c.JSON(http.StatusOK, payload)
	})
}
