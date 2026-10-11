package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func migrationTestManifest(t *testing.T) (string, refreshMigrationManifest) {
	t.Helper()
	dir := t.TempDir()
	secret := filepath.Join(dir, "recovery.secret")
	require.NoError(t, os.WriteFile(secret, []byte(strings.Repeat("ab", 32)), 0600))
	m := refreshMigrationManifest{
		Version: 1, DatabaseURL: "postgres://test:secret@127.0.0.1:1/isolated?sslmode=disable",
		RecoverySecretFile:   secret,
		Primary:              refreshMigrationNode{URL: "redis://default:test@127.0.0.1:1/0", RunID: strings.Repeat("a", 40), ACLUsers: []string{"default"}},
		PrimaryReplicationID: strings.Repeat("b", 40), PrimaryAddress: "127.0.0.1:1",
	}
	path := filepath.Join(dir, "manifest.json")
	migrationWriteManifest(t, path, m)
	return path, m
}

func migrationWriteManifest(t *testing.T, path string, m refreshMigrationManifest) {
	t.Helper()
	data, err := json.Marshal(m)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, data, 0600))
}

func TestRefreshMigrationPrivateManifest(t *testing.T) {
	path, expected := migrationTestManifest(t)
	m, secret, err := readRefreshMigrationManifest(path)
	require.NoError(t, err)
	require.Equal(t, expected, *m)
	require.Len(t, secret, 32)
	options, closeClients, err := m.options(secret)
	require.NoError(t, err)
	defer closeClients()
	require.Equal(t, expected.Primary.RunID, options.ExpectedRunID)
	require.Equal(t, expected.PrimaryReplicationID, options.Group.PrimaryReplicationID)
	require.Equal(t, expected.Primary.ACLUsers, options.Group.PrimaryACLUsers)
	require.Equal(t, "127.0.0.1:1", options.Source.Options().Addr)
}

func TestRefreshMigrationManifestVersionCompatibility(t *testing.T) {
	_, manifest := migrationTestManifest(t)
	require.NoError(t, manifest.validateSessionFence(), "version 1 without the opt-in fence remains the legacy offline mode")

	manifest.Version = 2
	for _, tc := range []struct {
		name   string
		mutate func(*refreshMigrationManifest)
	}{
		{"missing-opt-in", func(m *refreshMigrationManifest) {}},
		{"not-drained", func(m *refreshMigrationManifest) {
			m.SessionFence = &refreshSessionFenceManifest{PreserveRuntimeAccess: true}
			m.Runtime = &refreshRuntimeManifest{}
		}},
		{"missing-runtime", func(m *refreshMigrationManifest) {
			m.SessionFence = &refreshSessionFenceManifest{PreserveRuntimeAccess: true, AuthEndpointsBlockedAndDrained: true}
		}},
		{"disabled-preservation", func(m *refreshMigrationManifest) {
			m.SessionFence = &refreshSessionFenceManifest{AuthEndpointsBlockedAndDrained: true}
			m.Runtime = &refreshRuntimeManifest{}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			candidate := manifest
			candidate.SessionFence, candidate.Runtime = nil, nil
			tc.mutate(&candidate)
			require.ErrorContains(t, candidate.validateSessionFence(), "manifest v2")
		})
	}

	manifest.SessionFence = &refreshSessionFenceManifest{PreserveRuntimeAccess: true, AuthEndpointsBlockedAndDrained: true}
	manifest.Runtime = &refreshRuntimeManifest{}
	require.NoError(t, manifest.validateSessionFence(), "version 2 requires and accepts the explicit opt-in")
	manifest.Version = 1
	require.ErrorContains(t, manifest.validateSessionFence(), "manifest v2")
}

func TestRefreshMigrationV2OptionsCarryOnlyFixedRuntimeAccess(t *testing.T) {
	path, manifest := migrationTestManifest(t)
	appPassword := filepath.Join(filepath.Dir(path), "app.secret")
	require.NoError(t, os.WriteFile(appPassword, []byte(strings.Repeat("cd", 32)), 0600))
	manifest.Version = 2
	manifest.SessionFence = &refreshSessionFenceManifest{PreserveRuntimeAccess: true, AuthEndpointsBlockedAndDrained: true}
	manifest.Runtime = &refreshRuntimeManifest{AppPasswordFile: appPassword, EnvironmentFile: filepath.Join(filepath.Dir(path), "runtime.env")}

	options, closeClients, err := manifest.options(make([]byte, 32))
	require.NoError(t, err)
	defer closeClients()
	require.NotNil(t, options.PreserveRuntimeAccess)
	require.True(t, options.PreserveRuntimeAccess.AuthEndpointsBlockedAndDrained)
	require.Equal(t, []string{refreshRuntimeHash(strings.Repeat("cd", 32))}, options.PreserveRuntimeAccess.ReservedPasswordSHA256)
	rules, err := options.PreserveRuntimeAccess.ACL.Rules(0)
	require.NoError(t, err)
	require.NotContains(t, rules, "+@all")
	require.NotContains(t, rules, "~*")
	require.NotContains(t, rules, "&*")
}

func TestRefreshMigrationRejectsUnsafeFiles(t *testing.T) {
	for _, name := range []string{"public-manifest", "public-secret", "symlink", "directory", "oversized"} {
		t.Run(name, func(t *testing.T) {
			path, m := migrationTestManifest(t)
			switch name {
			case "public-manifest":
				require.NoError(t, os.Chmod(path, 0644))
			case "public-secret":
				require.NoError(t, os.Chmod(m.RecoverySecretFile, 0644))
			case "symlink":
				link := path + ".link"
				require.NoError(t, os.Symlink(path, link))
				path = link
			case "directory":
				path = filepath.Dir(path)
			case "oversized":
				require.NoError(t, os.WriteFile(path, bytes.Repeat([]byte("x"), 128<<10+1), 0600))
			}
			_, _, err := readRefreshMigrationManifest(path)
			require.Error(t, err)
		})
	}
}

func TestRefreshMigrationRejectsInvalidInputsWithoutSecretEcho(t *testing.T) {
	for _, name := range []string{"unknown-field", "trailing", "version", "database-url", "secret", "too-many-replicas"} {
		t.Run(name, func(t *testing.T) {
			path, m := migrationTestManifest(t)
			switch name {
			case "version":
				m.Version = 2
			case "database-url":
				m.DatabaseURL = "postgres://password-not-for-logs@%invalid"
			case "secret":
				require.NoError(t, os.WriteFile(m.RecoverySecretFile, []byte("password-not-for-logs"), 0600))
			case "too-many-replicas":
				m.Replicas = make([]refreshMigrationNode, 9)
			}
			migrationWriteManifest(t, path, m)
			if name == "unknown-field" || name == "trailing" {
				data, err := os.ReadFile(path)
				require.NoError(t, err)
				if name == "unknown-field" {
					data = append([]byte(`{"password-not-for-logs":true,`), data[1:]...)
				} else {
					data = append(data, []byte(` {"password-not-for-logs":true}`)...)
				}
				require.NoError(t, os.WriteFile(path, data, 0600))
			}
			var output bytes.Buffer
			err := migrateRefreshSessions(context.Background(), path, &output)
			require.Error(t, err)
			require.NotContains(t, err.Error(), "password-not-for-logs")
			require.Empty(t, output.String())
		})
	}
}

func TestRefreshMigrationRejectsImplicitRedisEndpoints(t *testing.T) {
	_, manifest := migrationTestManifest(t)
	for _, endpoint := range []string{"redis://", "unix:///private/redis.sock", "http://127.0.0.1:1", "redis://localhost:1?dial_timeout=1h", "rediss://localhost:1#ignored"} {
		manifest.Primary.URL = endpoint
		_, _, err := manifest.options(make([]byte, 32))
		require.Error(t, err, endpoint)
	}
}

func TestRefreshMigrationCommandDispatch(t *testing.T) {
	if os.Getenv("XIASS_TEST_MIGRATION_CHILD") == "1" {
		separator := 0
		for i, arg := range os.Args {
			if arg == "--" {
				separator = i
				break
			}
		}
		os.Args = append([]string{os.Args[0]}, os.Args[separator+1:]...)
		flag.CommandLine = flag.NewFlagSet("test-migration", flag.ExitOnError)
		main()
		return
	}
	for _, args := range [][]string{
		{"-migrate-refresh-sessions", "/missing-file"},
		{"-offline-maintenance"},
		{"-migrate-refresh-sessions", "/missing-file", "-offline-maintenance", "-version"},
		{"-migrate-refresh-sessions", "/missing-file", "-offline-maintenance", "-setup"},
		{"-migrate-refresh-sessions", "/missing-file", "-offline-maintenance"},
	} {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		command := exec.CommandContext(ctx, os.Args[0], append([]string{"-test.run=^TestRefreshMigrationCommandDispatch$", "--"}, args...)...)
		command.Env = append(os.Environ(), "XIASS_TEST_MIGRATION_CHILD=1")
		output, err := command.CombinedOutput()
		cancel()
		require.Error(t, err)
		require.NotContains(t, string(output), "setup wizard")
		require.NotContains(t, string(output), "tunnel runtime")
		require.True(t, strings.Contains(string(output), "Session migration requires") || strings.Contains(string(output), "migration input must"), string(output))
	}
}
