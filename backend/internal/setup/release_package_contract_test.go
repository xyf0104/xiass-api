package setup

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bmatcuk/doublestar"
	"github.com/moby/patternmatcher"
	"github.com/moby/patternmatcher/ignorefile"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestReleaseArchivesKeepRuntimeAndExcludeLocalState(t *testing.T) {
	root, err := filepath.Abs("../../..")
	require.NoError(t, err)
	var baseline []string
	for _, name := range []string{".goreleaser.yaml", ".goreleaser.simple.yaml"} {
		t.Run(name, func(t *testing.T) {
			content, err := os.ReadFile(filepath.Join(root, name))
			require.NoError(t, err)
			var cfg struct {
				Archives []struct {
					Files []string `yaml:"files"`
				} `yaml:"archives"`
			}
			require.NoError(t, yaml.Unmarshal(content, &cfg))
			require.Len(t, cfg.Archives, 1)
			patterns := cfg.Archives[0].Files
			if baseline == nil {
				baseline = patterns
			} else {
				require.Equal(t, baseline, patterns, "all architectures must ship the same runtime files")
			}
			matches := func(path string) bool {
				for _, pattern := range patterns {
					matched, err := doublestar.Match(pattern, path)
					require.NoError(t, err)
					if matched {
						return true
					}
				}
				return false
			}
			for _, path := range []string{
				"deploy/.env.example", "deploy/config.example.yaml", "deploy/Caddyfile",
				"deploy/docker-compose.yml", "deploy/docker-compose.local.yml",
				"deploy/docker-compose.standalone.yml", "deploy/docker-compose.xiass.yml",
				"deploy/xiass-install.sh", "deploy/xiass-update.sh", "deploy/nowind-update.sh",
				"deploy/xiass-backup.sh", "deploy/xiass-restore.sh", "deploy/xiass-runtime-start.sh",
				"deploy/xiass-runtime-export.sh", "deploy/xiass-runtime-restore.sh",
				"deploy/xiass-cluster-runtime.sh", "deploy/xiass-cluster-join.sh",
				"deploy/xiass-frps-migrate.sh", "deploy/frps-soft-router-install.sh",
				"deploy/team-child-automation/package.json", "deploy/team-child-automation/server.mjs",
				"deploy/team-child-automation/Dockerfile", "deploy/xiass-updater/Dockerfile",
				"deploy/xiass-updater/xiass-updater-entrypoint.sh",
				"deploy/openwrt/xiass-soft-router-agent/install.sh",
				"deploy/openwrt/xiass-soft-router-agent/files/usr/bin/xiass-soft-router-agent",
				"deploy/openwrt/xiass-soft-router-agent/files/etc/init.d/xiass-soft-router-agent",
			} {
				require.FileExists(t, filepath.Join(root, path))
				require.True(t, matches(path), "runtime file missing: %s", path)
			}
			for _, path := range []string{
				"deploy/.env", "deploy/config.yaml", "deploy/docker-compose.override.yml",
				"deploy/data/dump.sql", "deploy/postgres_data/base/1", "deploy/redis_data/dump.rdb",
				"deploy/ha/secrets/pgpass", "deploy/backups/runtime.tar.gz",
				"deploy/xiass-update.sh.bak", "deploy/tests/test.sh", "deploy/test-caddyfile-cache.sh",
				"deploy/team-child-automation/server.test.mjs",
				"deploy/team-child-automation/node_modules/playwright/package.json",
				"deploy/openwrt/xiass-soft-router-agent/tests/fake_uci.py",
				"artifacts/report.json", "operations/rehearsal.sh", "backend/server", "frontend/node_modules/a.js",
			} {
				require.False(t, matches(path), "non-runtime file would enter archive: %s", path)
			}
			files := make(map[string]bool)
			var size int64
			for _, pattern := range patterns {
				paths, err := doublestar.Glob(filepath.Join(root, pattern))
				require.NoError(t, err)
				require.NotEmpty(t, paths, "stale runtime pattern: %s", pattern)
				for _, path := range paths {
					info, err := os.Stat(path)
					require.NoError(t, err)
					if info.Mode().IsRegular() && !files[path] {
						files[path] = true
						size += info.Size()
					}
				}
			}
			t.Logf("runtime support payload: %d files, %d bytes (binary excluded)", len(files), size)
		})
	}
}

func TestProxyAgentShipsWithEveryReleasePath(t *testing.T) {
	root, err := filepath.Abs("../../..")
	require.NoError(t, err)

	type buildConfig struct {
		ID     string   `yaml:"id"`
		Dir    string   `yaml:"dir"`
		Binary string   `yaml:"binary"`
		GOOS   []string `yaml:"goos"`
		GOARCH []string `yaml:"goarch"`
	}
	type archiveConfig struct {
		IDs   []string `yaml:"ids"`
		Files []string `yaml:"files"`
	}
	type dockerConfig struct {
		IDs        []string `yaml:"ids"`
		ExtraFiles []string `yaml:"extra_files"`
	}
	type releaseConfig struct {
		Builds   []buildConfig   `yaml:"builds"`
		Archives []archiveConfig `yaml:"archives"`
		Dockers  []dockerConfig  `yaml:"dockers"`
	}

	for _, name := range []string{".goreleaser.yaml", ".goreleaser.simple.yaml"} {
		t.Run(name, func(t *testing.T) {
			content, err := os.ReadFile(filepath.Join(root, name))
			require.NoError(t, err)
			var cfg releaseConfig
			require.NoError(t, yaml.Unmarshal(content, &cfg))

			builds := make(map[string]buildConfig, len(cfg.Builds))
			for _, build := range cfg.Builds {
				builds[build.ID] = build
			}
			server, ok := builds["xiass-api"]
			require.True(t, ok)
			agent, ok := builds["xiass-proxy-agent"]
			require.True(t, ok)
			require.Equal(t, "tools/xiass-proxy-agent", agent.Dir)
			require.Equal(t, "xiass-proxy-agent", agent.Binary)
			require.Equal(t, server.GOOS, agent.GOOS)
			require.Equal(t, server.GOARCH, agent.GOARCH)

			require.Len(t, cfg.Archives, 1)
			require.ElementsMatch(t, []string{"xiass-api", "xiass-proxy-agent"}, cfg.Archives[0].IDs)
			require.Contains(t, cfg.Archives[0].Files, "tools/xiass-proxy-agent/LICENSE")
			require.Contains(t, cfg.Archives[0].Files, "tools/xiass-proxy-agent/THIRD_PARTY_NOTICES.md")
			require.Contains(t, cfg.Archives[0].Files, "tools/xiass-proxy-agent/UPSTREAM_SOURCE_MANIFEST.json")

			require.NotEmpty(t, cfg.Dockers)
			for _, docker := range cfg.Dockers {
				require.ElementsMatch(t, []string{"xiass-api", "xiass-proxy-agent"}, docker.IDs)
				require.Contains(t, docker.ExtraFiles, "tools/xiass-proxy-agent/LICENSE")
				require.Contains(t, docker.ExtraFiles, "tools/xiass-proxy-agent/THIRD_PARTY_NOTICES.md")
				require.Contains(t, docker.ExtraFiles, "tools/xiass-proxy-agent/UPSTREAM_SOURCE_MANIFEST.json")
			}
		})
	}

	notices, err := os.ReadFile(filepath.Join(root, "tools/xiass-proxy-agent/THIRD_PARTY_NOTICES.md"))
	require.NoError(t, err)
	for _, name := range []string{"Dockerfile", "deploy/Dockerfile"} {
		content, err := os.ReadFile(filepath.Join(root, name))
		require.NoError(t, err)
		moduleCopy := "COPY tools/xiass-proxy-agent/third_party/ccodex-sleep-state/go.mod tools/xiass-proxy-agent/third_party/ccodex-sleep-state/go.sum ./third_party/ccodex-sleep-state/"
		require.Contains(t, string(content), moduleCopy)
		moduleCopyIndex := strings.Index(string(content), moduleCopy)
		require.Greater(t, strings.Index(string(content)[moduleCopyIndex:], "go mod download"), 0, "local replacement metadata must exist before dependency download")
		require.Contains(t, string(content), "/app/xiass-proxy-agent")
		require.Contains(t, string(content), "tools/xiass-proxy-agent/LICENSE")
		require.Contains(t, string(content), "tools/xiass-proxy-agent/UPSTREAM_SOURCE_MANIFEST.json")
		require.Equal(t, string(notices), dockerHeredoc(content, "/app/licenses/xiass-proxy-agent/THIRD_PARTY_NOTICES.md"))
	}

	goreleaserDockerfile, err := os.ReadFile(filepath.Join(root, "Dockerfile.goreleaser"))
	require.NoError(t, err)
	require.Contains(t, string(goreleaserDockerfile), "COPY --chown=xiass:xiass xiass-proxy-agent /app/xiass-proxy-agent")
	require.Contains(t, string(goreleaserDockerfile), "tools/xiass-proxy-agent/LICENSE")
	require.Contains(t, string(goreleaserDockerfile), "tools/xiass-proxy-agent/THIRD_PARTY_NOTICES.md")
	require.Contains(t, string(goreleaserDockerfile), "tools/xiass-proxy-agent/UPSTREAM_SOURCE_MANIFEST.json")

	entrypoint, err := os.ReadFile(filepath.Join(root, "deploy/docker-entrypoint.sh"))
	require.NoError(t, err)
	require.NotContains(t, string(entrypoint), "xiass-proxy-agent")
}

func dockerHeredoc(content []byte, destination string) string {
	text := string(content)
	marker := "<<'EOF' " + destination + "\n"
	start := strings.Index(text, marker)
	if start < 0 {
		return ""
	}
	start += len(marker)
	end := strings.Index(text[start:], "EOF\n")
	if end < 0 {
		return ""
	}
	return text[start : start+end]
}

func TestDockerBuildContextKeepsSourcesNotGeneratedState(t *testing.T) {
	file, err := os.Open("../../../.dockerignore")
	require.NoError(t, err)
	defer func() { _ = file.Close() }()
	patterns, err := ignorefile.ReadAll(file)
	require.NoError(t, err)
	matcher, err := patternmatcher.New(patterns)
	require.NoError(t, err)
	for _, path := range []string{
		"backend/cmd/server/main.go", "backend/cmd/server/VERSION", "backend/go.mod", "backend/go.sum",
		"backend/scripts/resolve-version.sh", "backend/migrations/240_refresh_token_authority_transition.sql",
		"backend/resources/model-pricing/model_prices_and_context_window.json",
		"frontend/src/main.ts", "frontend/package.json", "frontend/pnpm-lock.yaml",
		"frontend/.npmrc", "frontend/pnpm-workspace.yaml", "docs/legal/privacy.md",
		"deploy/docker-entrypoint.sh", "tools/xiass-proxy-agent/go.mod",
		"tools/xiass-proxy-agent/go.sum", "tools/xiass-proxy-agent/cmd/xiass-proxy-agent/main.go",
		"tools/xiass-proxy-agent/LICENSE",
		"tools/xiass-proxy-agent/UPSTREAM_SOURCE_MANIFEST.json",
	} {
		ignored, err := matcher.MatchesOrParentMatches(path)
		require.NoError(t, err)
		require.False(t, ignored, "build input excluded: %s", path)
	}
	for _, path := range []string{
		"artifacts/verification", "operations/rehearsal.sh", "historical/old/server",
		"backups/runtime.tar.gz", "infrastructure/ha/config.yml", "backend/.gocache/00/test",
		"backend/bin/server", "backend/internal/web/dist/assets/stale.js",
		"backend/internal/service/example_test.go", "frontend/src/__tests__/example.spec.ts",
		"frontend/node_modules/a.js", "frontend/coverage/index.html", "frontend/tsconfig.tsbuildinfo",
		"deploy/.env", "deploy/ha/secrets/pgpass", "deploy/backups/data.tar.gz",
		"deploy/postgres_data/base/1", "deploy/redis_data/dump.rdb", "backend/data/config.yaml",
	} {
		ignored, err := matcher.MatchesOrParentMatches(path)
		require.NoError(t, err)
		require.True(t, ignored, "non-build file included: %s", path)
	}
}
