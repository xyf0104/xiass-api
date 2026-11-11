package routes

import (
	_ "embed"
	"net/http"
	"strconv"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/benchmark"
	"github.com/gin-gonic/gin"
)

const pelicanPreviewCSP = "default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; img-src data: blob:; font-src data:; connect-src 'none'; form-action 'none'; base-uri 'none'; object-src 'none'; frame-src 'none'; frame-ancestors 'self'; sandbox allow-scripts;"

//go:embed pelican_preview.html
var pelicanPreviewTemplate string

var pelicanPreviewShell = []byte(strings.ReplaceAll(pelicanPreviewTemplate, "__MAX_HTML_BYTES__", strconv.Itoa(benchmark.MaxHTMLBytes)))

// RegisterPelicanPreviewRoutes serves an empty, public renderer without accessing saved results.
func RegisterPelicanPreviewRoutes(v1 *gin.RouterGroup) {
	v1.GET("/pelican-preview", func(c *gin.Context) {
		// Replace the panel's nonce policy and DENY header only for this isolated document.
		c.Header("Content-Security-Policy", pelicanPreviewCSP)
		c.Header("X-Frame-Options", "SAMEORIGIN")
		c.Header("Cache-Control", "no-store")
		c.Header("X-Content-Type-Options", "nosniff")
		c.Header("Referrer-Policy", "no-referrer")
		c.Data(http.StatusOK, "text/html; charset=utf-8", pelicanPreviewShell)
	})
}
