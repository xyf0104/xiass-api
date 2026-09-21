package routes

import (
	"time"

	"github.com/Wei-Shaw/sub2api/internal/handler"
	basemiddleware "github.com/Wei-Shaw/sub2api/internal/middleware"
	ippkg "github.com/Wei-Shaw/sub2api/internal/pkg/ip"
	"github.com/Wei-Shaw/sub2api/internal/repository"
	servermiddleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
)

// RegisterToolRoutes registers public utility endpoints. The OpenAI OAuth
// helper is intentionally public and free, but bounded by fail-closed Redis
// rate limits because it performs outbound token exchanges.
func RegisterToolRoutes(
	v1 *gin.RouterGroup,
	h *handler.Handlers,
	redisClient *redis.Client,
	panelRateLimiter *servermiddleware.PanelRateLimiter,
) {
	h.Admin.OpenAIOAuth.ConfigurePublicToolSessionStore(repository.NewRedisPublicOpenAIOAuthSessionStore(redisClient))
	tools := v1.Group("/tools")

	rateLimiter := basemiddleware.NewRateLimiter(redisClient)
	strict := basemiddleware.RateLimitOptions{FailureMode: basemiddleware.RateLimitFailClose}
	openaiOAuth := tools.Group("/openai-oauth")
	openaiOAuth.Use(publicOpenAIOAuthSecurity())
	openaiOAuth.Use(panelRateLimiter.PublicIP())
	{
		openaiOAuth.POST(
			"/authorize",
			rateLimiter.LimitWithOptions("public-openai-oauth-authorize", 5, time.Minute, strict),
			rateLimiter.LimitWithOptions("public-openai-oauth-authorize-hourly", 30, time.Hour, strict),
			h.Admin.OpenAIOAuth.GeneratePublicToolAuthURL,
		)
		openaiOAuth.POST(
			"/exchange",
			rateLimiter.LimitWithOptions("public-openai-oauth-exchange", 10, time.Minute, strict),
			h.Admin.OpenAIOAuth.ExchangePublicToolCode,
		)
	}

	// AdsPower runs on the administrator's own computer. A short-lived ticket
	// minted by the authenticated admin page is the only authority accepted by
	// these endpoints; the local AdsPower API key never reaches XIASS.
	adsPower := tools.Group("/adspower")
	adsPower.Use(publicAdsPowerHelperSecurity())
	adsPower.Use(panelRateLimiter.PublicIP())
	{
		adsPower.POST(
			"/helpers/pairings/redeem",
			rateLimiter.LimitWithOptions("public-adspower-helper-pairing", 20, time.Minute, strict),
			h.Admin.OpenAIOAuth.RedeemOpenAIAdsPowerHelperPairing,
		)
		adsPower.POST(
			"/helpers/commands/next",
			rateLimiter.LimitWithOptions("public-adspower-helper-poll", 180, time.Minute, strict),
			h.Admin.OpenAIOAuth.PollOpenAIAdsPowerHelperCommand,
		)
		adsPower.POST(
			"/helpers/profiles/inventory",
			rateLimiter.LimitWithOptions("public-adspower-helper-inventory", 60, time.Minute, strict),
			h.Admin.OpenAIOAuth.InventoryOpenAIAdsPowerProfiles,
		)
		adsPower.POST(
			"/helpers/profiles/release",
			rateLimiter.LimitWithOptions("public-adspower-helper-release", 120, time.Minute, strict),
			h.Admin.OpenAIOAuth.ReleaseOpenAIAdsPowerProfile,
		)
		adsPower.POST(
			"/launch-tickets/redeem",
			rateLimiter.LimitWithOptions("public-adspower-ticket-redeem", 30, time.Minute, strict),
			h.Admin.OpenAIOAuth.RedeemOpenAIAdsPowerLaunchTicket,
		)
		adsPower.POST(
			"/bindings/report",
			rateLimiter.LimitWithOptions("public-adspower-binding-report", 30, time.Minute, strict),
			h.Admin.OpenAIOAuth.ReportOpenAIAdsPowerBinding,
		)
		adsPower.POST(
			"/progress/report",
			rateLimiter.LimitWithOptions("public-adspower-progress-report", 120, time.Minute, strict),
			h.Admin.OpenAIOAuth.ReportOpenAIAdsPowerProgress,
		)
		adsPower.POST(
			"/sms/action",
			rateLimiter.LimitWithOptions("public-adspower-sms-action", 120, time.Minute, strict),
			h.Admin.OpenAIOAuth.OpenAIAdsPowerSMSAction,
		)
		adsPower.POST(
			"/callbacks/report",
			rateLimiter.LimitWithOptions("public-adspower-callback-report", 30, time.Minute, strict),
			h.Admin.OpenAIOAuth.ReportOpenAIAdsPowerCallback,
		)
	}

	// This is intentionally a separate, no-login surface rather than an
	// extension of the XIASS admin UI. A long-lived random bearer token scopes
	// each request to one Team mailbox; rate limits and no-store headers apply
	// before any mailbox-provider request is made.
	teamMailbox := v1.Group("/public/team-mailbox")
	teamMailbox.Use(publicTeamMailboxShareSecurity())
	teamMailbox.Use(panelRateLimiter.PublicIP())
	{
		teamMailbox.GET(
			"/code",
			rateLimiter.LimitWithOptions("public-team-mailbox-code", 30, time.Minute, strict),
			rateLimiter.LimitWithOptions("public-team-mailbox-code-hourly", 1000, time.Hour, strict),
			h.Admin.OpenAIOAuth.PollPublicTeamChildMailboxShare,
		)
		teamMailbox.GET(
			"/messages",
			rateLimiter.LimitWithOptions("public-team-mailbox-messages", 30, time.Minute, strict),
			rateLimiter.LimitWithOptions("public-team-mailbox-messages-hourly", 1000, time.Hour, strict),
			h.Admin.OpenAIOAuth.ListPublicTeamChildMailboxShare,
		)
		teamMailbox.GET(
			"/messages/:message_id",
			rateLimiter.LimitWithOptions("public-team-mailbox-message", 60, time.Minute, strict),
			rateLimiter.LimitWithOptions("public-team-mailbox-message-hourly", 1000, time.Hour, strict),
			h.Admin.OpenAIOAuth.GetPublicTeamChildMailboxShareMessage,
		)
	}
}

func publicAdsPowerHelperSecurity() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Cache-Control", "private, no-store, max-age=0")
		c.Header("Pragma", "no-cache")
		c.Header("Expires", "0")
		c.Header("Referrer-Policy", "no-referrer")
		c.Header("Cross-Origin-Resource-Policy", "same-origin")
		c.Next()
	}
}

// publicOpenAIOAuthSecurity runs before every public OAuth limiter and handler.
// It keeps early errors private and forces security-sensitive IP consumers onto
// Gin's configured trusted-proxy chain instead of the legacy raw-header mode.
func publicOpenAIOAuthSecurity() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Cache-Control", "private, no-store, max-age=0")
		c.Header("Pragma", "no-cache")
		c.Header("Expires", "0")
		c.Header("Referrer-Policy", "no-referrer")

		trustedClientIP := ippkg.GetTrustedClientIP(c)
		ippkg.SetForwardedIPSettings(c, false, nil)
		binding := &service.SessionBinding{IP: trustedClientIP, UserAgent: c.Request.UserAgent()}
		if existing := service.SessionBindingFromContext(c.Request.Context()); existing != nil {
			binding.UserAgent = existing.UserAgent
		}
		c.Request = c.Request.WithContext(service.WithSessionBinding(c.Request.Context(), binding))
		c.Next()
	}
}

// publicTeamMailboxShareSecurity keeps the independent mailbox page out of
// browser and intermediary caches. Its bearer token is never read from a URL
// path or query string, and this route does not need an XIASS user session.
func publicTeamMailboxShareSecurity() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Cache-Control", "private, no-store, max-age=0")
		c.Header("Pragma", "no-cache")
		c.Header("Expires", "0")
		c.Header("Referrer-Policy", "no-referrer")
		c.Header("Vary", "Authorization")
		c.Header("Cross-Origin-Resource-Policy", "same-origin")
		c.Next()
	}
}
