//go:build unit

package service

import (
	"archive/tar"
	"archive/zip"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type updateServiceCacheStub struct {
	data string
}

func (s *updateServiceCacheStub) GetUpdateInfo(context.Context) (string, error) {
	if s.data == "" {
		return "", errors.New("cache miss")
	}
	return s.data, nil
}

func (s *updateServiceCacheStub) SetUpdateInfo(_ context.Context, data string, _ time.Duration) error {
	s.data = data
	return nil
}

type updateServiceGitHubClientStub struct {
	release        *GitHubRelease
	recentReleases []*GitHubRelease
	recentErr      error
}

func (s *updateServiceGitHubClientStub) FetchLatestRelease(context.Context, string) (*GitHubRelease, error) {
	return s.release, nil
}

func (s *updateServiceGitHubClientStub) FetchRecentReleases(context.Context, string, int) ([]*GitHubRelease, error) {
	return s.recentReleases, s.recentErr
}

func (s *updateServiceGitHubClientStub) DownloadFile(context.Context, string, string, int64) error {
	panic("DownloadFile should not be called when no update is available")
}

func (s *updateServiceGitHubClientStub) FetchChecksumFile(context.Context, string) ([]byte, error) {
	panic("FetchChecksumFile should not be called when no update is available")
}

func TestUpdateServicePerformUpdateNoUpdateReturnsSentinel(t *testing.T) {
	svc := NewUpdateService(
		&updateServiceCacheStub{},
		&updateServiceGitHubClientStub{
			release: &GitHubRelease{
				TagName: "v0.1.132",
				Name:    "v0.1.132",
			},
		},
		"0.1.132",
		"release",
	)

	err := svc.PerformUpdate(context.Background())

	require.Error(t, err)
	require.True(t, errors.Is(err, ErrNoUpdateAvailable))
	require.ErrorIs(t, err, ErrNoUpdateAvailable)
}

func newRollbackTestService(current string, releases []*GitHubRelease) *UpdateService {
	return NewUpdateService(
		&updateServiceCacheStub{},
		&updateServiceGitHubClientStub{recentReleases: releases},
		current,
		"release",
	)
}

func TestUpdateServiceListRollbackVersionsFiltersAndCaps(t *testing.T) {
	releases := []*GitHubRelease{
		{TagName: "v0.1.148", PublishedAt: "2026-07-09T00:00:00Z"},                       // newer than current: excluded
		{TagName: "v0.1.147", PublishedAt: "2026-07-08T00:00:00Z"},                       // current: excluded
		{TagName: "v0.1.146-rc1", PublishedAt: "2026-07-07T12:00:00Z", Prerelease: true}, // prerelease: excluded
		{TagName: "v0.1.146", PublishedAt: "2026-07-07T00:00:00Z"},
		{TagName: "v0.1.145", PublishedAt: "2026-07-06T00:00:00Z", Draft: true}, // draft: excluded
		{TagName: "v0.1.144", PublishedAt: "2026-07-05T00:00:00Z"},
		{TagName: "v0.1.144", PublishedAt: "2026-07-05T00:00:00Z"}, // duplicate: excluded
		{TagName: "v0.1.143", PublishedAt: "2026-07-04T00:00:00Z"},
		{TagName: "v0.1.142", PublishedAt: "2026-07-03T00:00:00Z"}, // beyond cap of 3: excluded
	}
	svc := newRollbackTestService("0.1.147", releases)

	versions, err := svc.ListRollbackVersions(context.Background())

	require.NoError(t, err)
	require.Len(t, versions, 3)
	require.Equal(t, "0.1.146", versions[0].Version)
	require.Equal(t, "0.1.144", versions[1].Version)
	require.Equal(t, "0.1.143", versions[2].Version)
}

func TestUpdateServiceListRollbackVersionsSortsUnorderedInput(t *testing.T) {
	releases := []*GitHubRelease{
		{TagName: "v0.1.144"},
		{TagName: "v0.1.146"},
		{TagName: "v0.1.145"},
	}
	svc := newRollbackTestService("0.1.147", releases)

	versions, err := svc.ListRollbackVersions(context.Background())

	require.NoError(t, err)
	require.Len(t, versions, 3)
	require.Equal(t, "0.1.146", versions[0].Version)
	require.Equal(t, "0.1.145", versions[1].Version)
	require.Equal(t, "0.1.144", versions[2].Version)
}

func TestUpdateServiceListRollbackVersionsEmptyWhenNoneOlder(t *testing.T) {
	releases := []*GitHubRelease{
		{TagName: "v0.1.147"},
		{TagName: "v0.1.148"},
	}
	svc := newRollbackTestService("0.1.147", releases)

	versions, err := svc.ListRollbackVersions(context.Background())

	require.NoError(t, err)
	require.Empty(t, versions)
}

func TestUpdateServiceListRollbackVersionsPropagatesFetchError(t *testing.T) {
	svc := NewUpdateService(
		&updateServiceCacheStub{},
		&updateServiceGitHubClientStub{recentErr: errors.New("github unavailable")},
		"0.1.147",
		"release",
	)

	_, err := svc.ListRollbackVersions(context.Background())

	require.Error(t, err)
	require.Contains(t, err.Error(), "github unavailable")
}

func TestUpdateServiceRollbackToVersionRejectsDisallowedTargets(t *testing.T) {
	releases := []*GitHubRelease{
		{TagName: "v0.1.148"},
		{TagName: "v0.1.147"},
		{TagName: "v0.1.146"},
		{TagName: "v0.1.145"},
		{TagName: "v0.1.144"},
		{TagName: "v0.1.143"},
		{TagName: "v0.1.142"},
	}
	svc := newRollbackTestService("0.1.147", releases)

	for _, target := range []string{
		"",         // empty
		"0.1.147",  // current version
		"v0.1.147", // current version with prefix
		"0.1.148",  // newer than current
		"0.1.142",  // older than the 3 most recent
		"9.9.9",    // nonexistent
	} {
		err := svc.RollbackToVersion(context.Background(), target)
		require.ErrorIs(t, err, ErrRollbackVersionNotAllowed, "target %q should be rejected", target)
	}
}

func TestUpdateServiceRollbackToVersionAcceptsVPrefix(t *testing.T) {
	// No platform asset in the release: the target passes the allowlist check
	// and fails later at asset lookup, proving the version itself was accepted.
	releases := []*GitHubRelease{
		{TagName: "v0.1.147"},
		{TagName: "v0.1.146"},
	}
	svc := newRollbackTestService("0.1.147", releases)

	err := svc.RollbackToVersion(context.Background(), "v0.1.146")

	require.Error(t, err)
	require.NotErrorIs(t, err, ErrRollbackVersionNotAllowed)
	require.Contains(t, err.Error(), "no compatible release found")
}

func TestUpdateServiceExtractBinaryAcceptsCanonicalAndLegacyNames(t *testing.T) {
	for _, binaryName := range []string{canonicalBinaryName, previousBinaryName, legacyBinaryName} {
		t.Run(binaryName, func(t *testing.T) {
			tempDir := t.TempDir()
			archivePath := filepath.Join(tempDir, "release.tar")
			archiveFile, err := os.Create(archivePath)
			require.NoError(t, err)

			writer := tar.NewWriter(archiveFile)
			payload := []byte("nowind-binary")
			require.NoError(t, writer.WriteHeader(&tar.Header{
				Name: binaryName,
				Mode: 0o755,
				Size: int64(len(payload)),
			}))
			_, err = writer.Write(payload)
			require.NoError(t, err)
			require.NoError(t, writer.Close())
			require.NoError(t, archiveFile.Close())

			destination := filepath.Join(tempDir, canonicalBinaryName)
			svc := &UpdateService{}
			_, err = svc.extractReleaseBinaries(archivePath, destination, filepath.Join(filepath.Dir(destination), executableFileName(proxyAgentBinaryName)))
			require.NoError(t, err)

			extracted, err := os.ReadFile(destination)
			require.NoError(t, err)
			require.Equal(t, payload, extracted)
		})
	}
}

func TestUpdateServiceExtractReleaseBinariesIncludesProxyAgent(t *testing.T) {
	tempDir := t.TempDir()
	archivePath := filepath.Join(tempDir, "release.tar")
	writeTarArchive(t, archivePath, map[string][]byte{
		legacyBinaryName:     []byte("server"),
		proxyAgentBinaryName: []byte("agent"),
	})

	serverDest := filepath.Join(tempDir, canonicalBinaryName)
	agentDest := filepath.Join(tempDir, executableFileName(proxyAgentBinaryName))
	svc := &UpdateService{}
	hasAgent, err := svc.extractReleaseBinaries(archivePath, serverDest, agentDest)

	require.NoError(t, err)
	require.True(t, hasAgent)
	requireFileContent(t, serverDest, "server")
	requireFileContent(t, agentDest, "agent")
}

func TestUpdateServiceExtractReleaseBinariesAcceptsServerOnlyZip(t *testing.T) {
	tempDir := t.TempDir()
	archivePath := filepath.Join(tempDir, "release.zip")
	archiveFile, err := os.Create(archivePath)
	require.NoError(t, err)
	writer := zip.NewWriter(archiveFile)
	entry, err := writer.Create(previousBinaryName + ".exe")
	require.NoError(t, err)
	_, err = entry.Write([]byte("server"))
	require.NoError(t, err)
	require.NoError(t, writer.Close())
	require.NoError(t, archiveFile.Close())

	serverDest := filepath.Join(tempDir, canonicalBinaryName)
	agentDest := filepath.Join(tempDir, executableFileName(proxyAgentBinaryName))
	svc := &UpdateService{}
	hasAgent, err := svc.extractReleaseBinaries(archivePath, serverDest, agentDest)

	require.NoError(t, err)
	require.False(t, hasAgent)
	requireFileContent(t, serverDest, "server")
	require.NoFileExists(t, agentDest)
}

func TestReplaceAndRollbackReleaseBinariesKeepsServerAndAgentTogether(t *testing.T) {
	tempDir := t.TempDir()
	serverPath := filepath.Join(tempDir, canonicalBinaryName)
	agentPath := filepath.Join(tempDir, executableFileName(proxyAgentBinaryName))
	newServerPath := filepath.Join(tempDir, "new-server")
	newAgentPath := filepath.Join(tempDir, "new-agent")
	writeTestFile(t, serverPath, "old-server")
	writeTestFile(t, agentPath, "old-agent")
	writeTestFile(t, newServerPath, "new-server")
	writeTestFile(t, newAgentPath, "new-agent")

	require.NoError(t, replaceReleaseBinaries(serverPath, newServerPath, newAgentPath, true))
	requireFileContent(t, serverPath, "new-server")
	requireFileContent(t, agentPath, "new-agent")
	requireFileContent(t, serverPath+".backup", "old-server")
	requireFileContent(t, agentPath+".backup", "old-agent")

	require.NoError(t, rollbackReleaseBinaries(serverPath))
	requireFileContent(t, serverPath, "old-server")
	requireFileContent(t, agentPath, "old-agent")
}

func TestServerOnlyUpdateDisablesAgentAndRollbackRestoresIt(t *testing.T) {
	tempDir := t.TempDir()
	serverPath := filepath.Join(tempDir, canonicalBinaryName)
	agentPath := filepath.Join(tempDir, executableFileName(proxyAgentBinaryName))
	newServerPath := filepath.Join(tempDir, "new-server")
	writeTestFile(t, serverPath, "old-server")
	writeTestFile(t, agentPath, "old-agent")
	writeTestFile(t, newServerPath, "server-only")

	require.NoError(t, replaceReleaseBinaries(serverPath, newServerPath, "", false))
	requireFileContent(t, serverPath, "server-only")
	require.NoFileExists(t, agentPath)
	requireFileContent(t, agentPath+".backup", "old-agent")

	require.NoError(t, rollbackReleaseBinaries(serverPath))
	requireFileContent(t, serverPath, "old-server")
	requireFileContent(t, agentPath, "old-agent")
}

func TestRollbackRemovesAgentIntroducedByUpdate(t *testing.T) {
	tempDir := t.TempDir()
	serverPath := filepath.Join(tempDir, canonicalBinaryName)
	agentPath := filepath.Join(tempDir, executableFileName(proxyAgentBinaryName))
	newServerPath := filepath.Join(tempDir, "new-server")
	newAgentPath := filepath.Join(tempDir, "new-agent")
	writeTestFile(t, serverPath, "old-server")
	writeTestFile(t, newServerPath, "new-server")
	writeTestFile(t, newAgentPath, "new-agent")

	require.NoError(t, replaceReleaseBinaries(serverPath, newServerPath, newAgentPath, true))
	requireFileContent(t, agentPath, "new-agent")
	require.FileExists(t, agentPath+".backup.absent")

	require.NoError(t, rollbackReleaseBinaries(serverPath))
	requireFileContent(t, serverPath, "old-server")
	require.NoFileExists(t, agentPath)
}

func writeTarArchive(t *testing.T, archivePath string, files map[string][]byte) {
	t.Helper()
	archiveFile, err := os.Create(archivePath)
	require.NoError(t, err)
	writer := tar.NewWriter(archiveFile)
	for name, payload := range files {
		require.NoError(t, writer.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(payload))}))
		_, err = writer.Write(payload)
		require.NoError(t, err)
	}
	require.NoError(t, writer.Close())
	require.NoError(t, archiveFile.Close())
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	require.NoError(t, os.WriteFile(path, []byte(content), 0o755))
}

func requireFileContent(t *testing.T, path, expected string) {
	t.Helper()
	content, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, expected, string(content))
}
