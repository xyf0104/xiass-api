package handler

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

// CodexModels serves the Codex models manifest for Codex clients.
//
// Codex CLI and the Codex desktop app refresh their model picker from
// GET {base_url}/models?client_version=... (custom provider mode) or
// GET /backend-api/codex/models (chatgpt_base_url mode). Both routes land
// here. Pinned discovery takes precedence over local account model mappings;
// when disabled, groups with explicit mappings are generated locally. Custom
// API key manifests retain their provider metadata and receive XIASS capability
// completion before the final group-specific ETag is evaluated.
func (h *OpenAIGatewayHandler) CodexModels(c *gin.Context) {
	if c.Request.Context().Err() != nil {
		return
	}
	apiKey, ok := middleware2.GetAPIKeyFromContext(c)
	if !ok || apiKey.Group == nil {
		h.errorResponse(c, http.StatusUnauthorized, "invalid_request_error", "API key group is required")
		return
	}
	if apiKey.Group.Platform != service.PlatformOpenAI && apiKey.Group.Platform != service.PlatformComposite {
		h.errorResponse(c, http.StatusNotFound, "not_found_error", "Codex models manifest is only available for OpenAI and Composite groups")
		return
	}

	clientETag := c.GetHeader("If-None-Match")
	if apiKey.Group.Platform == service.PlatformOpenAI && apiKey.Group.CodexModelsManifestConfig.Enabled {
		manifest, account, err := h.gatewayService.FetchPinnedCodexModelsManifest(
			c.Request.Context(), apiKey.Group, c.Query("client_version"),
		)
		if err == nil {
			setOpsSelectedAccount(c, account.ID, account.Platform)
			if err := h.finalizeCodexModelsManifest(c, apiKey.Group, manifest, account, clientETag); err != nil {
				h.errorResponse(c, http.StatusBadGateway, "upstream_error", "Failed to complete Codex model capabilities")
				return
			}
			writeOpenAIModelsResponse(c, manifest)
			return
		}
		if c.Request.Context().Err() != nil {
			return
		}
		if !apiKey.Group.CodexModelsManifestConfig.FallbackToScheduler {
			if errors.Is(err, service.ErrNoPinnedCodexModelsAccounts) {
				h.errorResponse(c, http.StatusServiceUnavailable, "upstream_error", "No available pinned OpenAI accounts")
				return
			}
			h.errorResponse(c, infraerrors.Code(err), "upstream_error", infraerrors.Message(err))
			return
		}
	}

	if !apiKey.Group.CodexModelsManifestConfig.Enabled {
		manifest, configured, err := h.gatewayService.BuildGroupConfiguredCodexModelsManifest(
			c.Request.Context(), apiKey.Group, clientETag,
		)
		if err != nil {
			if c.Request.Context().Err() != nil {
				return
			}
			h.errorResponse(c, http.StatusInternalServerError, "api_error", "Failed to build Codex models manifest")
			return
		}
		if configured {
			writeOpenAIModelsResponse(c, manifest)
			return
		}
	}

	maxAccountSwitches := h.maxAccountSwitches
	if maxAccountSwitches <= 0 {
		maxAccountSwitches = 3
	}
	failedAccountIDs := make(map[int64]struct{})
	switchCount := 0
	var lastUpstreamErr error
	groupID := apiKey.Group.ID

	for {
		account, err := h.gatewayService.SelectAccountForModelWithExclusions(c.Request.Context(), &groupID, "", "", failedAccountIDs)
		if err != nil {
			if c.Request.Context().Err() != nil {
				return
			}
			if lastUpstreamErr != nil {
				h.errorResponse(c, infraerrors.Code(lastUpstreamErr), "upstream_error", infraerrors.Message(lastUpstreamErr))
				return
			}
			h.errorResponse(c, http.StatusServiceUnavailable, "upstream_error", "No available OpenAI accounts")
			return
		}
		// 让 ops 错误日志携带实际选中的上游账号，便于定位失效账号（#4544）。
		setOpsSelectedAccount(c, account.ID, account.Platform)

		// The client ETag identifies the final group-specific representation,
		// not the shared source manifest.
		manifest, err := h.gatewayService.FetchCodexModelsManifest(c.Request.Context(), account, c.Query("client_version"), "")
		if err != nil {
			if c.Request.Context().Err() != nil {
				return
			}
			if service.IsRetryableCodexModelsManifestError(err) && switchCount < maxAccountSwitches {
				failedAccountIDs[account.ID] = struct{}{}
				switchCount++
				lastUpstreamErr = err
				continue
			}
			h.errorResponse(c, infraerrors.Code(err), "upstream_error", infraerrors.Message(err))
			return
		}
		if err := h.gatewayService.CompleteAPIKeyCodexModelsManifestForClient(manifest, account); err != nil {
			h.errorResponse(c, http.StatusInternalServerError, "api_error", "Failed to complete Codex models manifest")
			return
		}
		if err := service.ApplyPinnedCodexModelsMapping(manifest, account, apiKey.Group); err != nil {
			h.errorResponse(c, http.StatusInternalServerError, "api_error", "Failed to apply model mappings")
			return
		}
		if err := h.gatewayService.ApplyCodexBridgedRouteSearchCapability(
			c.Request.Context(), manifest, account, apiKey.Group.ID, "",
		); err != nil {
			h.errorResponse(c, http.StatusBadGateway, "upstream_error", "Failed to complete Codex model capabilities")
			return
		}
		if err := h.finalizeCodexModelsManifest(c, apiKey.Group, manifest, account, clientETag); err != nil {
			h.errorResponse(c, http.StatusBadGateway, "upstream_error", "Failed to complete Codex model capabilities")
			return
		}
		writeOpenAIModelsResponse(c, manifest)
		return
	}
}

func (h *OpenAIGatewayHandler) finalizeCodexModelsManifest(
	c *gin.Context,
	group *service.Group,
	manifest *service.OpenAIModelsResponse,
	account *service.Account,
	clientETag string,
) error {
	if err := h.gatewayService.MergeGroupConfiguredCodexModels(
		c.Request.Context(), group, manifest, clientETag,
	); err != nil {
		return err
	}
	h.gatewayService.FinalizeAPIKeyCodexModelsManifestForClient(manifest, account, clientETag)
	return nil
}
