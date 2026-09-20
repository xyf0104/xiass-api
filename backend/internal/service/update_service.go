package service

import (
	"archive/tar"
	"archive/zip"
	"bufio"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

var (
	ErrNoUpdateAvailable         = infraerrors.Conflict("ALREADY_UP_TO_DATE", "no update available; current version is latest")
	ErrRollbackVersionNotAllowed = infraerrors.BadRequest("ROLLBACK_VERSION_NOT_ALLOWED", "version is not in the allowed rollback list")
)

const (
	updateCacheKey       = "update_check_cache"
	updateCacheTTL       = 1200 // 20 minutes
	githubRepo           = "xyf0104/xiass-api"
	canonicalBinaryName  = "xiass-api"
	previousBinaryName   = "nowind-api"
	legacyBinaryName     = "sub2api"
	proxyAgentBinaryName = "xiass-proxy-agent"

	// Security: allowed download domains for updates
	allowedDownloadHost = "github.com"
	allowedAssetHost    = "objects.githubusercontent.com"

	// Security: max download size (500MB)
	maxDownloadSize = 500 * 1024 * 1024

	// Rollback: expose at most the 3 most recent versions older than current
	maxRollbackVersions = 3
	// Fetch a few extra releases so filtering (current/newer/prerelease) still leaves enough candidates
	rollbackFetchPageSize = 15
)

// UpdateCache defines cache operations for update service
type UpdateCache interface {
	GetUpdateInfo(ctx context.Context) (string, error)
	SetUpdateInfo(ctx context.Context, data string, ttl time.Duration) error
}

// GitHubReleaseClient 获取 GitHub release 信息的接口
type GitHubReleaseClient interface {
	FetchLatestRelease(ctx context.Context, repo string) (*GitHubRelease, error)
	FetchRecentReleases(ctx context.Context, repo string, perPage int) ([]*GitHubRelease, error)
	DownloadFile(ctx context.Context, url, dest string, maxSize int64) error
	FetchChecksumFile(ctx context.Context, url string) ([]byte, error)
}

// UpdateService handles software updates
type UpdateService struct {
	cache          UpdateCache
	githubClient   GitHubReleaseClient
	currentVersion string
	buildType      string // "source" for manual builds, "release" for CI builds
}

// NewUpdateService creates a new UpdateService
func NewUpdateService(cache UpdateCache, githubClient GitHubReleaseClient, version, buildType string) *UpdateService {
	return &UpdateService{
		cache:          cache,
		githubClient:   githubClient,
		currentVersion: version,
		buildType:      buildType,
	}
}

// UpdateInfo contains update information
type UpdateInfo struct {
	CurrentVersion string       `json:"current_version"`
	LatestVersion  string       `json:"latest_version"`
	HasUpdate      bool         `json:"has_update"`
	ReleaseInfo    *ReleaseInfo `json:"release_info,omitempty"`
	Cached         bool         `json:"cached"`
	Warning        string       `json:"warning,omitempty"`
	BuildType      string       `json:"build_type"` // "source" or "release"
}

// ReleaseInfo contains GitHub release details
type ReleaseInfo struct {
	Name        string  `json:"name"`
	Body        string  `json:"body"`
	PublishedAt string  `json:"published_at"`
	HTMLURL     string  `json:"html_url"`
	Assets      []Asset `json:"assets,omitempty"`
}

// Asset represents a release asset
type Asset struct {
	Name        string `json:"name"`
	DownloadURL string `json:"download_url"`
	Size        int64  `json:"size"`
}

// GitHubRelease represents GitHub API response
type GitHubRelease struct {
	TagName     string        `json:"tag_name"`
	Name        string        `json:"name"`
	Body        string        `json:"body"`
	PublishedAt string        `json:"published_at"`
	HTMLURL     string        `json:"html_url"`
	Draft       bool          `json:"draft"`
	Prerelease  bool          `json:"prerelease"`
	Assets      []GitHubAsset `json:"assets"`
}

// RollbackVersion describes a release version the system can roll back to
type RollbackVersion struct {
	Version     string `json:"version"` // without "v" prefix, e.g. "0.1.146"
	PublishedAt string `json:"published_at"`
	HTMLURL     string `json:"html_url"`
}

type GitHubAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
	Size               int64  `json:"size"`
}

// CheckUpdate checks for available updates
func (s *UpdateService) CheckUpdate(ctx context.Context, force bool) (*UpdateInfo, error) {
	// Try cache first
	if !force {
		if cached, err := s.getFromCache(ctx); err == nil && cached != nil {
			return cached, nil
		}
	}

	// Fetch from GitHub
	info, err := s.fetchLatestRelease(ctx)
	if err != nil {
		// Return cached on error
		if cached, cacheErr := s.getFromCache(ctx); cacheErr == nil && cached != nil {
			cached.Warning = "Using cached data: " + err.Error()
			return cached, nil
		}
		return &UpdateInfo{
			CurrentVersion: s.currentVersion,
			LatestVersion:  s.currentVersion,
			HasUpdate:      false,
			Warning:        err.Error(),
			BuildType:      s.buildType,
		}, nil
	}

	// Cache result
	s.saveToCache(ctx, info)
	return info, nil
}

// PerformUpdate downloads and applies the update
// Uses atomic file replacement pattern for safe in-place updates
func (s *UpdateService) PerformUpdate(ctx context.Context) error {
	info, err := s.CheckUpdate(ctx, true)
	if err != nil {
		return err
	}

	if !info.HasUpdate {
		return ErrNoUpdateAvailable
	}

	return s.applyReleaseAssets(ctx, info.ReleaseInfo.Assets)
}

// applyReleaseAssets downloads the platform archive from the given release assets,
// verifies its checksum, and atomically swaps the running binary.
// Shared by PerformUpdate (latest) and RollbackToVersion (specific older version).
func (s *UpdateService) applyReleaseAssets(ctx context.Context, releaseAssets []Asset) error {
	// Find matching archive and checksum for current platform
	archiveName := s.getArchiveName()
	var downloadURL string
	var previousDownloadURL string
	var legacyDownloadURL string
	var checksumURL string

	for _, asset := range releaseAssets {
		if strings.Contains(asset.Name, archiveName) && !strings.HasSuffix(asset.Name, ".txt") {
			if strings.HasPrefix(asset.Name, canonicalBinaryName+"_") {
				downloadURL = asset.DownloadURL
			} else if strings.HasPrefix(asset.Name, previousBinaryName+"_") {
				previousDownloadURL = asset.DownloadURL
			} else if legacyDownloadURL == "" {
				legacyDownloadURL = asset.DownloadURL
			}
		}
		if asset.Name == "checksums.txt" {
			checksumURL = asset.DownloadURL
		}
	}
	if downloadURL == "" {
		downloadURL = previousDownloadURL
	}
	if downloadURL == "" {
		downloadURL = legacyDownloadURL
	}

	if downloadURL == "" {
		return fmt.Errorf("no compatible release found for %s/%s", runtime.GOOS, runtime.GOARCH)
	}

	// SECURITY: Validate download URL is from trusted domain
	if err := validateDownloadURL(downloadURL); err != nil {
		return fmt.Errorf("invalid download URL: %w", err)
	}
	if checksumURL != "" {
		if err := validateDownloadURL(checksumURL); err != nil {
			return fmt.Errorf("invalid checksum URL: %w", err)
		}
	}

	// Get current executable path
	exePath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to get executable path: %w", err)
	}
	exePath, err = filepath.EvalSymlinks(exePath)
	if err != nil {
		return fmt.Errorf("failed to resolve symlinks: %w", err)
	}

	exeDir := filepath.Dir(exePath)

	// Create temp directory in the SAME directory as executable
	// This ensures os.Rename is atomic (same filesystem)
	tempDir, err := os.MkdirTemp(exeDir, ".xiass-api-update-*")
	if err != nil {
		return fmt.Errorf("failed to create temp dir: %w", err)
	}
	defer func() { _ = os.RemoveAll(tempDir) }()

	// Download archive
	archivePath := filepath.Join(tempDir, filepath.Base(downloadURL))
	if err := s.downloadFile(ctx, downloadURL, archivePath); err != nil {
		return fmt.Errorf("download failed: %w", err)
	}

	// Verify checksum if available
	if checksumURL != "" {
		if err := s.verifyChecksum(ctx, archivePath, checksumURL); err != nil {
			return fmt.Errorf("checksum verification failed: %w", err)
		}
	}

	// Extract the release payload. Older archives can legitimately contain only
	// the server binary; in that case the installed agent is moved to rollback
	// state instead of being left behind as an unversioned capability.
	newBinaryPath := filepath.Join(tempDir, canonicalBinaryName)
	newAgentPath := filepath.Join(tempDir, executableFileName(proxyAgentBinaryName))
	hasAgent, err := s.extractReleaseBinaries(archivePath, newBinaryPath, newAgentPath)
	if err != nil {
		return fmt.Errorf("extraction failed: %w", err)
	}

	// Set executable permission before replacement
	if err := os.Chmod(newBinaryPath, 0755); err != nil {
		return fmt.Errorf("chmod failed: %w", err)
	}
	if hasAgent {
		if err := os.Chmod(newAgentPath, 0755); err != nil {
			return fmt.Errorf("agent chmod failed: %w", err)
		}
	}

	return replaceReleaseBinaries(exePath, newBinaryPath, newAgentPath, hasAgent)
}

// Rollback restores the previous version
func (s *UpdateService) Rollback() error {
	exePath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to get executable path: %w", err)
	}
	exePath, err = filepath.EvalSymlinks(exePath)
	if err != nil {
		return fmt.Errorf("failed to resolve symlinks: %w", err)
	}

	return rollbackReleaseBinaries(exePath)
}

// ListRollbackVersions returns up to maxRollbackVersions release versions that are
// strictly older than the current version (the current version itself is excluded),
// newest first. Draft and prerelease entries are skipped.
func (s *UpdateService) ListRollbackVersions(ctx context.Context) ([]RollbackVersion, error) {
	releases, err := s.fetchRollbackCandidates(ctx)
	if err != nil {
		return nil, err
	}

	versions := make([]RollbackVersion, 0, len(releases))
	for _, r := range releases {
		versions = append(versions, RollbackVersion{
			Version:     strings.TrimPrefix(r.TagName, "v"),
			PublishedAt: r.PublishedAt,
			HTMLURL:     r.HTMLURL,
		})
	}
	return versions, nil
}

// RollbackToVersion downloads and installs a specific older version.
// The target must be one of the versions returned by ListRollbackVersions;
// anything else (including the current version) is rejected.
func (s *UpdateService) RollbackToVersion(ctx context.Context, version string) error {
	target := strings.TrimPrefix(strings.TrimSpace(version), "v")
	if target == "" {
		return ErrRollbackVersionNotAllowed
	}

	releases, err := s.fetchRollbackCandidates(ctx)
	if err != nil {
		return err
	}

	var match *GitHubRelease
	for _, r := range releases {
		if strings.TrimPrefix(r.TagName, "v") == target {
			match = r
			break
		}
	}
	if match == nil {
		return ErrRollbackVersionNotAllowed
	}

	assets := make([]Asset, len(match.Assets))
	for i, a := range match.Assets {
		assets[i] = Asset{
			Name:        a.Name,
			DownloadURL: a.BrowserDownloadURL,
			Size:        a.Size,
		}
	}

	return s.applyReleaseAssets(ctx, assets)
}

// fetchRollbackCandidates fetches recent releases and keeps the newest
// maxRollbackVersions entries strictly older than the current version.
func (s *UpdateService) fetchRollbackCandidates(ctx context.Context) ([]*GitHubRelease, error) {
	releases, err := s.githubClient.FetchRecentReleases(ctx, githubRepo, rollbackFetchPageSize)
	if err != nil {
		return nil, err
	}

	seen := make(map[string]bool, len(releases))
	candidates := make([]*GitHubRelease, 0, maxRollbackVersions)
	for _, r := range releases {
		if r == nil || r.Draft || r.Prerelease {
			continue
		}
		v := strings.TrimPrefix(r.TagName, "v")
		if v == "" || seen[v] {
			continue
		}
		// Only versions strictly older than current (also excludes current itself)
		if compareVersions(v, s.currentVersion) >= 0 {
			continue
		}
		seen[v] = true
		candidates = append(candidates, r)
	}

	sort.SliceStable(candidates, func(i, j int) bool {
		return compareVersions(
			strings.TrimPrefix(candidates[i].TagName, "v"),
			strings.TrimPrefix(candidates[j].TagName, "v"),
		) > 0
	})

	if len(candidates) > maxRollbackVersions {
		candidates = candidates[:maxRollbackVersions]
	}
	return candidates, nil
}

func (s *UpdateService) fetchLatestRelease(ctx context.Context) (*UpdateInfo, error) {
	release, err := s.githubClient.FetchLatestRelease(ctx, githubRepo)
	if err != nil {
		return nil, err
	}

	latestVersion := strings.TrimPrefix(release.TagName, "v")

	assets := make([]Asset, len(release.Assets))
	for i, a := range release.Assets {
		assets[i] = Asset{
			Name:        a.Name,
			DownloadURL: a.BrowserDownloadURL,
			Size:        a.Size,
		}
	}

	return &UpdateInfo{
		CurrentVersion: s.currentVersion,
		LatestVersion:  latestVersion,
		HasUpdate:      compareVersions(s.currentVersion, latestVersion) < 0,
		ReleaseInfo: &ReleaseInfo{
			Name:        release.Name,
			Body:        release.Body,
			PublishedAt: release.PublishedAt,
			HTMLURL:     release.HTMLURL,
			Assets:      assets,
		},
		Cached:    false,
		BuildType: s.buildType,
	}, nil
}

func (s *UpdateService) downloadFile(ctx context.Context, downloadURL, dest string) error {
	return s.githubClient.DownloadFile(ctx, downloadURL, dest, maxDownloadSize)
}

func (s *UpdateService) getArchiveName() string {
	osName := runtime.GOOS
	arch := runtime.GOARCH
	return fmt.Sprintf("%s_%s", osName, arch)
}

// validateDownloadURL checks if the URL is from an allowed domain
// SECURITY: This prevents SSRF and ensures downloads only come from trusted GitHub domains
func validateDownloadURL(rawURL string) error {
	parsedURL, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid URL: %w", err)
	}

	// Must be HTTPS
	if parsedURL.Scheme != "https" {
		return fmt.Errorf("only HTTPS URLs are allowed")
	}

	// Check against allowed hosts
	host := parsedURL.Host
	// GitHub release URLs can be from github.com or objects.githubusercontent.com
	if host != allowedDownloadHost &&
		!strings.HasSuffix(host, "."+allowedDownloadHost) &&
		host != allowedAssetHost &&
		!strings.HasSuffix(host, "."+allowedAssetHost) {
		return fmt.Errorf("download from untrusted host: %s", host)
	}

	return nil
}

func (s *UpdateService) verifyChecksum(ctx context.Context, filePath, checksumURL string) error {
	// Download checksums file
	checksumData, err := s.githubClient.FetchChecksumFile(ctx, checksumURL)
	if err != nil {
		return fmt.Errorf("failed to download checksums: %w", err)
	}

	// Calculate file hash
	f, err := os.Open(filePath)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	actualHash := hex.EncodeToString(h.Sum(nil))

	// Find expected hash in checksums file
	fileName := filepath.Base(filePath)
	scanner := bufio.NewScanner(strings.NewReader(string(checksumData)))
	for scanner.Scan() {
		line := scanner.Text()
		parts := strings.Fields(line)
		if len(parts) == 2 && parts[1] == fileName {
			if parts[0] == actualHash {
				return nil
			}
			return fmt.Errorf("checksum mismatch: expected %s, got %s", parts[0], actualHash)
		}
	}

	return fmt.Errorf("checksum not found for %s", fileName)
}

func (s *UpdateService) extractReleaseBinaries(archivePath, serverDest, agentDest string) (bool, error) {
	f, err := os.Open(archivePath)
	if err != nil {
		return false, err
	}
	defer func() { _ = f.Close() }()

	if strings.HasSuffix(strings.ToLower(archivePath), ".zip") {
		return extractReleaseZip(archivePath, serverDest, agentDest)
	}

	var reader io.Reader = f

	// Handle gzip compression
	if strings.HasSuffix(archivePath, ".gz") || strings.HasSuffix(archivePath, ".tar.gz") || strings.HasSuffix(archivePath, ".tgz") {
		gzr, err := gzip.NewReader(f)
		if err != nil {
			return false, err
		}
		defer func() { _ = gzr.Close() }()
		reader = gzr
	}

	// Handle tar archive
	if strings.Contains(archivePath, ".tar") {
		tr := tar.NewReader(reader)
		serverFound := false
		agentFound := false
		for {
			hdr, err := tr.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				return false, err
			}

			// SECURITY: Prevent Zip Slip / Path Traversal attack
			// Only allow files with safe base names, no directory traversal
			baseName := filepath.Base(hdr.Name)

			// Check for path traversal attempts
			if strings.Contains(hdr.Name, "..") {
				return false, fmt.Errorf("path traversal attempt detected: %s", hdr.Name)
			}

			// Validate the entry is a regular file
			if hdr.Typeflag != tar.TypeReg {
				continue // Skip directories and special files
			}

			if isServerReleaseBinary(baseName) && !serverFound {
				if err := extractReleaseEntry(tr, hdr.Size, serverDest); err != nil {
					return false, err
				}
				serverFound = true
			} else if isProxyAgentReleaseBinary(baseName) && !agentFound {
				if err := extractReleaseEntry(tr, hdr.Size, agentDest); err != nil {
					return false, err
				}
				agentFound = true
			}
		}
		if !serverFound {
			return false, fmt.Errorf("binary not found in archive")
		}
		return agentFound, nil
	}

	info, err := f.Stat()
	if err != nil {
		return false, err
	}
	if err := extractReleaseEntry(reader, info.Size(), serverDest); err != nil {
		return false, err
	}
	return false, nil
}

func extractReleaseZip(archivePath, serverDest, agentDest string) (bool, error) {
	zr, err := zip.OpenReader(archivePath)
	if err != nil {
		return false, err
	}
	defer func() { _ = zr.Close() }()

	serverFound := false
	agentFound := false
	for _, entry := range zr.File {
		if strings.Contains(entry.Name, "..") {
			return false, fmt.Errorf("path traversal attempt detected: %s", entry.Name)
		}
		if !entry.FileInfo().Mode().IsRegular() {
			continue
		}
		baseName := filepath.Base(strings.ReplaceAll(entry.Name, "\\", "/"))
		var dest string
		switch {
		case isServerReleaseBinary(baseName) && !serverFound:
			dest = serverDest
			serverFound = true
		case isProxyAgentReleaseBinary(baseName) && !agentFound:
			dest = agentDest
			agentFound = true
		default:
			continue
		}

		r, err := entry.Open()
		if err != nil {
			return false, err
		}
		err = extractReleaseEntry(r, int64(entry.UncompressedSize64), dest)
		closeErr := r.Close()
		if err != nil {
			return false, err
		}
		if closeErr != nil {
			return false, closeErr
		}
	}
	if !serverFound {
		return false, fmt.Errorf("binary not found in archive")
	}
	return agentFound, nil
}

func extractReleaseEntry(reader io.Reader, size int64, dest string) error {
	const maxBinarySize = int64(500 * 1024 * 1024)
	if size < 0 || size > maxBinarySize {
		return fmt.Errorf("binary too large: %d bytes (max %d)", size, maxBinarySize)
	}
	out, err := os.OpenFile(dest, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	written, copyErr := io.Copy(out, io.LimitReader(reader, size+1))
	closeErr := out.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	if written != size {
		return fmt.Errorf("unexpected binary size: expected %d bytes, extracted %d", size, written)
	}
	return nil
}

func isServerReleaseBinary(name string) bool {
	for _, candidate := range []string{canonicalBinaryName, previousBinaryName, legacyBinaryName} {
		if name == candidate || name == candidate+".exe" {
			return true
		}
	}
	return false
}

func isProxyAgentReleaseBinary(name string) bool {
	return name == proxyAgentBinaryName || name == proxyAgentBinaryName+".exe"
}

func executableFileName(name string) string {
	if runtime.GOOS == "windows" {
		return name + ".exe"
	}
	return name
}

func replaceReleaseBinaries(serverPath, newServerPath, newAgentPath string, hasAgent bool) error {
	serverBackup := serverPath + ".backup"
	agentPath := filepath.Join(filepath.Dir(serverPath), executableFileName(proxyAgentBinaryName))
	agentBackup := agentPath + ".backup"
	agentAbsentMarker := agentBackup + ".absent"

	_ = os.Remove(serverBackup)
	_ = os.Remove(agentBackup)
	_ = os.Remove(agentAbsentMarker)

	hadAgent, err := pathExists(agentPath)
	if err != nil {
		return fmt.Errorf("inspect agent failed: %w", err)
	}
	if hadAgent {
		if err := os.Rename(agentPath, agentBackup); err != nil {
			return fmt.Errorf("agent backup failed: %w", err)
		}
	} else if err := os.WriteFile(agentAbsentMarker, nil, 0o600); err != nil {
		return fmt.Errorf("record absent agent failed: %w", err)
	}

	restoreAgent := func() error {
		_ = os.Remove(agentPath)
		if hadAgent {
			return os.Rename(agentBackup, agentPath)
		}
		return os.Remove(agentAbsentMarker)
	}
	if hasAgent {
		if err := os.Rename(newAgentPath, agentPath); err != nil {
			if restoreErr := restoreAgent(); restoreErr != nil {
				return fmt.Errorf("agent replace failed and restore failed: %w (restore error: %v)", err, restoreErr)
			}
			return fmt.Errorf("agent replace failed (restored backup): %w", err)
		}
	}

	if err := os.Rename(serverPath, serverBackup); err != nil {
		if restoreErr := restoreAgent(); restoreErr != nil {
			return fmt.Errorf("server backup failed and agent restore failed: %w (restore error: %v)", err, restoreErr)
		}
		return fmt.Errorf("server backup failed: %w", err)
	}
	if err := os.Rename(newServerPath, serverPath); err != nil {
		serverRestoreErr := os.Rename(serverBackup, serverPath)
		agentRestoreErr := restoreAgent()
		if serverRestoreErr != nil || agentRestoreErr != nil {
			return fmt.Errorf("server replace failed and restore was incomplete: %w (server restore: %v, agent restore: %v)", err, serverRestoreErr, agentRestoreErr)
		}
		return fmt.Errorf("server replace failed (restored backup): %w", err)
	}
	return nil
}

func rollbackReleaseBinaries(serverPath string) error {
	serverBackup := serverPath + ".backup"
	exists, err := pathExists(serverBackup)
	if err != nil {
		return fmt.Errorf("inspect backup failed: %w", err)
	}
	if !exists {
		return fmt.Errorf("no backup found")
	}

	currentServer := serverPath + ".rollback-current"
	_ = os.Remove(currentServer)
	if err := os.Rename(serverPath, currentServer); err != nil {
		return fmt.Errorf("stage current server failed: %w", err)
	}
	if err := os.Rename(serverBackup, serverPath); err != nil {
		_ = os.Rename(currentServer, serverPath)
		return fmt.Errorf("rollback failed: %w", err)
	}

	agentPath := filepath.Join(filepath.Dir(serverPath), executableFileName(proxyAgentBinaryName))
	if err := rollbackProxyAgent(agentPath); err != nil {
		serverBackupRestoreErr := os.Rename(serverPath, serverBackup)
		serverRestoreErr := os.Rename(currentServer, serverPath)
		return fmt.Errorf("agent rollback failed: %w (server backup restore: %v, server restore: %v)", err, serverBackupRestoreErr, serverRestoreErr)
	}
	_ = os.Remove(currentServer)
	return nil
}

func rollbackProxyAgent(agentPath string) error {
	agentBackup := agentPath + ".backup"
	agentAbsentMarker := agentBackup + ".absent"
	backupExists, err := pathExists(agentBackup)
	if err != nil {
		return err
	}
	markerExists, err := pathExists(agentAbsentMarker)
	if err != nil {
		return err
	}
	if !backupExists && !markerExists {
		return nil
	}

	currentAgent := agentPath + ".rollback-current"
	_ = os.Remove(currentAgent)
	currentExists, err := pathExists(agentPath)
	if err != nil {
		return err
	}
	if currentExists {
		if err := os.Rename(agentPath, currentAgent); err != nil {
			return err
		}
	}
	restoreCurrent := func() {
		if currentExists {
			_ = os.Rename(currentAgent, agentPath)
		}
	}

	if backupExists {
		if err := os.Rename(agentBackup, agentPath); err != nil {
			restoreCurrent()
			return err
		}
	} else if err := os.Remove(agentAbsentMarker); err != nil {
		restoreCurrent()
		return err
	}
	_ = os.Remove(agentAbsentMarker)
	_ = os.Remove(currentAgent)
	return nil
}

func pathExists(path string) (bool, error) {
	_, err := os.Stat(path)
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, err
}

func (s *UpdateService) getFromCache(ctx context.Context) (*UpdateInfo, error) {
	data, err := s.cache.GetUpdateInfo(ctx)
	if err != nil {
		return nil, err
	}

	var cached struct {
		Latest      string       `json:"latest"`
		ReleaseInfo *ReleaseInfo `json:"release_info"`
		Timestamp   int64        `json:"timestamp"`
	}
	if err := json.Unmarshal([]byte(data), &cached); err != nil {
		return nil, err
	}

	if time.Now().Unix()-cached.Timestamp > updateCacheTTL {
		return nil, fmt.Errorf("cache expired")
	}

	return &UpdateInfo{
		CurrentVersion: s.currentVersion,
		LatestVersion:  cached.Latest,
		HasUpdate:      compareVersions(s.currentVersion, cached.Latest) < 0,
		ReleaseInfo:    cached.ReleaseInfo,
		Cached:         true,
		BuildType:      s.buildType,
	}, nil
}

func (s *UpdateService) saveToCache(ctx context.Context, info *UpdateInfo) {
	cacheData := struct {
		Latest      string       `json:"latest"`
		ReleaseInfo *ReleaseInfo `json:"release_info"`
		Timestamp   int64        `json:"timestamp"`
	}{
		Latest:      info.LatestVersion,
		ReleaseInfo: info.ReleaseInfo,
		Timestamp:   time.Now().Unix(),
	}

	data, _ := json.Marshal(cacheData)
	_ = s.cache.SetUpdateInfo(ctx, string(data), time.Duration(updateCacheTTL)*time.Second)
}

// compareVersions compares two semantic versions
func compareVersions(current, latest string) int {
	currentParts := parseVersion(current)
	latestParts := parseVersion(latest)

	for i := 0; i < 3; i++ {
		if currentParts[i] < latestParts[i] {
			return -1
		}
		if currentParts[i] > latestParts[i] {
			return 1
		}
	}
	return 0
}

func parseVersion(v string) [3]int {
	v = strings.TrimPrefix(v, "v")
	if idx := strings.IndexByte(v, '-'); idx != -1 {
		v = v[:idx]
	}
	parts := strings.Split(v, ".")
	result := [3]int{0, 0, 0}
	for i := 0; i < len(parts) && i < 3; i++ {
		if parsed, err := strconv.Atoi(parts[i]); err == nil {
			result[i] = parsed
		}
	}
	return result
}
