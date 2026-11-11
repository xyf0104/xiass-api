package routes

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/benchmark"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestPelicanPreviewPublicEmptyShell(t *testing.T) {
	router := gin.New()
	RegisterPelicanPreviewRoutes(router.Group("/api/v1"))
	routes := router.Routes()
	require.Len(t, routes, 1)
	require.Equal(t, http.MethodGet, routes[0].Method)
	require.Equal(t, "/api/v1/pelican-preview", routes[0].Path)

	for _, path := range []string{
		"/api/v1/pelican-preview",
		"/api/v1/pelican-preview?id=private-result-marker&html=query-html-marker&token=query-token-marker",
	} {
		t.Run(path, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, path, strings.NewReader("request-body-marker"))
			r.Header.Set("Authorization", "Bearer authorization-marker")
			r.AddCookie(&http.Cookie{Name: "session", Value: "cookie-marker"})
			w := httptest.NewRecorder()
			router.ServeHTTP(w, r)
			require.Equal(t, http.StatusOK, w.Code)
			require.Equal(t, string(pelicanPreviewShell), w.Body.String())
			require.Empty(t, w.Header().Values("Set-Cookie"))
			require.Contains(t, w.Body.String(), "const maxHTMLBytes = "+strconv.Itoa(benchmark.MaxHTMLBytes)+";")
			require.NotContains(t, w.Body.String(), "__MAX_HTML_BYTES__")
			for _, marker := range []string{"private-result-marker", "query-html-marker", "query-token-marker", "authorization-marker", "cookie-marker", "request-body-marker"} {
				require.NotContains(t, w.Body.String(), marker)
			}
		})
	}
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodHead} {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest(method, "/api/v1/pelican-preview", nil))
		require.Equal(t, http.StatusNotFound, w.Code, method)
	}
}

func TestPelicanPreviewOverridesOnlyItsSecurityHeaders(t *testing.T) {
	const wantPolicy = "default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; img-src data: blob:; font-src data:; connect-src 'none'; form-action 'none'; base-uri 'none'; object-src 'none'; frame-src 'none'; frame-ancestors 'self'; sandbox allow-scripts;"
	for _, enabled := range []bool{true, false} {
		t.Run(strconv.FormatBool(enabled), func(t *testing.T) {
			router := gin.New()
			router.Use(middleware.SecurityHeaders(config.CSPConfig{Enabled: enabled}, nil))
			// Also replace multiple inherited policies, rather than leaving a restrictive nonce policy beside ours.
			router.Use(func(c *gin.Context) {
				if c.Request.URL.Path == "/api/v1/pelican-preview" {
					c.Writer.Header().Add("Content-Security-Policy", "script-src 'nonce-inherited'")
				}
				c.Next()
			})
			RegisterPelicanPreviewRoutes(router.Group("/api/v1"))
			router.GET("/panel", func(c *gin.Context) { c.String(http.StatusOK, "panel") })
			for range 2 {
				w := httptest.NewRecorder()
				router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/pelican-preview", nil))
				require.Equal(t, http.StatusOK, w.Code)
				for header, value := range map[string]string{
					"Content-Type":            "text/html; charset=utf-8",
					"Content-Security-Policy": wantPolicy,
					"X-Frame-Options":         "SAMEORIGIN",
					"Cache-Control":           "no-store",
					"X-Content-Type-Options":  "nosniff",
					"Referrer-Policy":         "no-referrer",
				} {
					require.Equal(t, []string{value}, w.Header().Values(header), header)
				}

				panel := httptest.NewRecorder()
				router.ServeHTTP(panel, httptest.NewRequest(http.MethodGet, "/panel", nil))
				require.Equal(t, "DENY", panel.Header().Get("X-Frame-Options"))
				require.Equal(t, "strict-origin-when-cross-origin", panel.Header().Get("Referrer-Policy"))
				if enabled {
					require.Contains(t, panel.Header().Get("Content-Security-Policy"), "'nonce-")
					require.NotContains(t, panel.Header().Get("Content-Security-Policy"), "sandbox allow-scripts")
				} else {
					require.Empty(t, panel.Header().Get("Content-Security-Policy"))
				}
			}
		})
	}
}

func TestPelicanPreviewMessageProtocol(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is required to execute the embedded preview's message protocol test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, node, "pelican_preview_protocol_test.cjs", strconv.Itoa(benchmark.MaxHTMLBytes))
	cmd.Stdin = strings.NewReader(string(pelicanPreviewShell))
	output, err := cmd.CombinedOutput()
	require.NoError(t, err, "%s", output)
}
