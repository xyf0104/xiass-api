package routes

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

const (
	iosReleaseAPIURL   = "https://api.github.com/repos/xyf0104/xiass-api/releases?per_page=30"
	iosPublicBaseURL   = "https://api.xiass.com/ios"
	iosReleaseCacheTTL = 5 * time.Minute
	iosManifestMaxSize = 1 << 20
)

type iosReleaseAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

type iosRelease struct {
	TagName    string            `json:"tag_name"`
	Draft      bool              `json:"draft"`
	Prerelease bool              `json:"prerelease"`
	Assets     []iosReleaseAsset `json:"assets"`
}

type iosInstallRelease struct {
	Version     string
	IPAFilename string
	IPAURL      string
	PlistURL    string
}

type iosInstallInfo struct {
	Version     string `json:"version"`
	ManifestURL string `json:"manifest_url"`
	PackageURL  string `json:"package_url"`
}

type iosInstallHandler struct {
	apiURL      string
	apiClient   *http.Client
	assetClient *http.Client
	now         func() time.Time

	mu          sync.Mutex
	cached      *iosInstallRelease
	cacheExpiry time.Time
}

func RegisterIOSInstallRoutes(r *gin.Engine) {
	apiClient := &http.Client{Timeout: 12 * time.Second}
	baseTransport, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		baseTransport = &http.Transport{}
	}
	assetTransport := baseTransport.Clone()
	assetTransport.TLSHandshakeTimeout = 10 * time.Second
	assetTransport.ResponseHeaderTimeout = 30 * time.Second
	assetTransport.IdleConnTimeout = 90 * time.Second
	assetClient := &http.Client{Transport: assetTransport}
	newIOSInstallHandler(iosReleaseAPIURL, apiClient, assetClient).register(r)
}

func newIOSInstallHandler(apiURL string, apiClient, assetClient *http.Client) *iosInstallHandler {
	if apiClient == nil {
		apiClient = http.DefaultClient
	}
	if assetClient == nil {
		assetClient = http.DefaultClient
	}
	handler := &iosInstallHandler{
		apiURL:      apiURL,
		apiClient:   apiClient,
		assetClient: assetClient,
		now:         time.Now,
	}
	assetClientCopy := *handler.assetClient
	assetClientCopy.CheckRedirect = func(request *http.Request, via []*http.Request) error {
		if len(via) >= 10 {
			return errors.New("too many iOS asset redirects")
		}
		if !handler.isTrustedAssetURL(request.URL) {
			return http.ErrUseLastResponse
		}
		return nil
	}
	handler.assetClient = &assetClientCopy
	return handler
}

func (h *iosInstallHandler) register(r gin.IRoutes) {
	r.GET("/ios/info", h.info)
	r.GET("/ios/manifest", h.manifest)
	r.GET("/ios/package", h.packageFile)
}

func (h *iosInstallHandler) info(c *gin.Context) {
	release, err := h.getRelease(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "iOS release unavailable"})
		return
	}
	c.Header("Cache-Control", "public, max-age=300")
	c.JSON(http.StatusOK, iosInstallInfo{
		Version:     release.Version,
		ManifestURL: iosPublicBaseURL + "/manifest",
		PackageURL:  iosPublicBaseURL + "/package",
	})
}

func (h *iosInstallHandler) manifest(c *gin.Context) {
	release, err := h.getRelease(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "iOS release unavailable"})
		return
	}
	manifestURL, err := url.Parse(release.PlistURL)
	if err != nil || !h.isTrustedAssetURL(manifestURL) {
		c.JSON(http.StatusBadGateway, gin.H{"error": "iOS manifest unavailable"})
		return
	}
	request, err := http.NewRequestWithContext(c.Request.Context(), http.MethodGet, manifestURL.String(), nil)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "iOS manifest unavailable"})
		return
	}
	request.Header.Set("User-Agent", "XIASS-iOS-Installer")
	response, err := h.assetClient.Do(request)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "iOS manifest unavailable"})
		return
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		c.JSON(http.StatusBadGateway, gin.H{"error": "iOS manifest unavailable"})
		return
	}
	rawManifest, err := io.ReadAll(io.LimitReader(response.Body, iosManifestMaxSize+1))
	if err != nil || len(rawManifest) > iosManifestMaxSize {
		c.JSON(http.StatusBadGateway, gin.H{"error": "iOS manifest unavailable"})
		return
	}
	rewritten, err := rewriteIOSManifestPackageURL(rawManifest, iosPublicBaseURL+"/package")
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "iOS manifest unavailable"})
		return
	}
	c.Header("Cache-Control", "public, max-age=300")
	c.Data(http.StatusOK, "application/xml; charset=utf-8", rewritten)
}

func (h *iosInstallHandler) packageFile(c *gin.Context) {
	release, err := h.getRelease(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "iOS package unavailable"})
		return
	}
	packageURL, err := url.Parse(release.IPAURL)
	if err != nil || !h.isTrustedAssetURL(packageURL) {
		c.JSON(http.StatusBadGateway, gin.H{"error": "iOS package unavailable"})
		return
	}
	request, err := http.NewRequestWithContext(c.Request.Context(), http.MethodGet, packageURL.String(), nil)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "iOS package unavailable"})
		return
	}
	request.Header.Set("User-Agent", "XIASS-iOS-Installer")
	if rangeHeader := c.GetHeader("Range"); rangeHeader != "" {
		request.Header.Set("Range", rangeHeader)
	}
	response, err := h.assetClient.Do(request)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "iOS package unavailable"})
		return
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK && response.StatusCode != http.StatusPartialContent {
		c.JSON(http.StatusBadGateway, gin.H{"error": "iOS package unavailable"})
		return
	}
	c.Header("Content-Type", "application/octet-stream")
	c.Header("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": release.IPAFilename}))
	c.Header("Cache-Control", "public, max-age=300")
	if response.ContentLength >= 0 {
		c.Header("Content-Length", fmt.Sprintf("%d", response.ContentLength))
	}
	for _, header := range []string{"Accept-Ranges", "Content-Range", "ETag", "Last-Modified"} {
		if value := response.Header.Get(header); value != "" {
			c.Header(header, value)
		}
	}
	c.Status(response.StatusCode)
	_, _ = io.Copy(c.Writer, response.Body)
}

func (h *iosInstallHandler) getRelease(ctx context.Context) (*iosInstallRelease, error) {
	h.mu.Lock()
	if h.cached != nil && h.now().Before(h.cacheExpiry) {
		cached := *h.cached
		h.mu.Unlock()
		return &cached, nil
	}
	h.mu.Unlock()

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, h.apiURL, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("User-Agent", "XIASS-iOS-Installer")
	response, err := h.apiClient.Do(request)
	if err != nil {
		return nil, err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub release API returned %d", response.StatusCode)
	}
	var releases []iosRelease
	if err := json.NewDecoder(io.LimitReader(response.Body, 4<<20)).Decode(&releases); err != nil {
		return nil, err
	}
	for _, release := range releases {
		if release.Draft || release.Prerelease {
			continue
		}
		if selected := selectIOSInstallAssets(release); selected != nil {
			h.mu.Lock()
			h.cached = selected
			h.cacheExpiry = h.now().Add(iosReleaseCacheTTL)
			cached := *selected
			h.mu.Unlock()
			return &cached, nil
		}
	}
	return nil, errors.New("no installable iOS release found")
}

func selectIOSInstallAssets(release iosRelease) *iosInstallRelease {
	if !strings.HasPrefix(strings.ToLower(release.TagName), "ios-v") {
		return nil
	}
	assets := make(map[string]iosReleaseAsset, len(release.Assets))
	for _, asset := range release.Assets {
		assets[strings.ToLower(asset.Name)] = asset
	}
	for _, ipa := range release.Assets {
		name := strings.ToLower(ipa.Name)
		if strings.Contains(ipa.Name, "/") || strings.Contains(ipa.Name, string(rune(92))) || !strings.HasPrefix(name, "xiassadmin-") || !strings.HasSuffix(name, ".ipa") {
			continue
		}
		extension := pathExt(ipa.Name)
		if extension == "" {
			continue
		}
		plistName := ipa.Name[:len(ipa.Name)-len(extension)] + ".plist"
		plist, ok := assets[strings.ToLower(plistName)]
		if !ok || !validIOSAssetURL(ipa.BrowserDownloadURL) || !validIOSAssetURL(plist.BrowserDownloadURL) {
			continue
		}
		version := strings.TrimSuffix(ipa.Name[len("XIASSAdmin-"):], pathExt(ipa.Name))
		if version == "" {
			version = strings.TrimPrefix(strings.TrimPrefix(strings.ToLower(release.TagName), "ios-v"), "v")
		}
		return &iosInstallRelease{
			Version:     version,
			IPAFilename: ipa.Name,
			IPAURL:      ipa.BrowserDownloadURL,
			PlistURL:    plist.BrowserDownloadURL,
		}
	}
	return nil
}

func validIOSAssetURL(rawURL string) bool {
	parsed, err := url.Parse(rawURL)
	return err == nil && parsed.Scheme == "https" && parsed.Hostname() != "" && parsed.User == nil
}

func pathExt(filename string) string {
	index := strings.LastIndexByte(filename, '.')
	if index < 0 {
		return ""
	}
	return filename[index:]
}

func (h *iosInstallHandler) isTrustedAssetURL(parsed *url.URL) bool {
	if parsed == nil || parsed.User != nil || parsed.Hostname() == "" {
		return false
	}
	apiURL, err := url.Parse(h.apiURL)
	if err != nil {
		return false
	}
	if parsed.Scheme == "https" && strings.EqualFold(parsed.Host, apiURL.Host) {
		return true
	}
	if parsed.Scheme != "https" {
		return false
	}
	if parsed.Port() != "" && parsed.Port() != "443" {
		return false
	}
	host := strings.ToLower(parsed.Hostname())
	return host == "github.com" || host == "release-assets.githubusercontent.com"
}

func rewriteIOSManifestPackageURL(rawManifest []byte, packageURL string) ([]byte, error) {
	decoder := xml.NewDecoder(bytes.NewReader(rawManifest))
	var output bytes.Buffer
	encoder := xml.NewEncoder(&output)
	var pendingKey string
	var insideKey bool
	var replaceURLString bool
	replaced := false

	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		switch value := token.(type) {
		case xml.StartElement:
			switch value.Name.Local {
			case "key":
				insideKey = true
			case "string":
				replaceURLString = pendingKey == "url"
			default:
				if pendingKey != "" {
					pendingKey = ""
				}
			}
		case xml.CharData:
			if insideKey {
				pendingKey = strings.TrimSpace(string(value))
			} else if replaceURLString && strings.Contains(strings.ToLower(string(value)), ".ipa") {
				token = xml.CharData(packageURL)
				replaced = true
			}
		case xml.EndElement:
			switch value.Name.Local {
			case "key":
				insideKey = false
			case "string":
				replaceURLString = false
				pendingKey = ""
			}
		}
		if err := encoder.EncodeToken(token); err != nil {
			return nil, err
		}
	}
	if !replaced {
		return nil, errors.New("software package URL not found in iOS manifest")
	}
	if err := encoder.Flush(); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}
