package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/repository"
	"github.com/stretchr/testify/require"
)

func TestRefreshRuntimeEnvironmentPreservesExistingFiles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runtime.env")
	data := []byte("JWT_REFRESH_TOKEN_STORE=postgres\n")
	require.NoError(t, writeRefreshRuntimeEnvironment(path, data))
	info, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0600), info.Mode().Perm())
	require.NoError(t, writeRefreshRuntimeEnvironment(path, data))
	require.Error(t, writeRefreshRuntimeEnvironment(path, []byte("different")))
	got, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, data, got)
	link := path + ".link"
	require.NoError(t, os.Symlink(path, link))
	require.Error(t, writeRefreshRuntimeEnvironment(link, data))
	require.NoError(t, os.Chmod(path, 0644))
	require.Error(t, writeRefreshRuntimeEnvironment(path, data))
}

func TestRefreshRuntimeEnvironmentVersionCompatibility(t *testing.T) {
	for _, version := range []int{1, 2} {
		t.Run(strconv.Itoa(version), func(t *testing.T) {
			path, manifest := migrationTestManifest(t)
			manifest.Version = version
			manifest.Runtime = &refreshRuntimeManifest{AppPasswordFile: filepath.Join(filepath.Dir(path), "app.secret"), EnvironmentFile: filepath.Join(filepath.Dir(path), "runtime.env")}
			if version == 2 {
				manifest.SessionFence = &refreshSessionFenceManifest{PreserveRuntimeAccess: true, AuthEndpointsBlockedAndDrained: true}
			}
			password := strings.Repeat("cd", 32)
			require.NoError(t, os.WriteFile(manifest.Runtime.AppPasswordFile, []byte(password), 0600))
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer func() { _ = db.Close() }()
			for _, retry := range []bool{false, true} {
				rows := sqlmock.NewRows([]string{"transition_id"})
				if retry {
					rows.AddRow("review")
				}
				mock.ExpectQuery("SELECT transition_id::text FROM refresh_token_legacy_transition").WillReturnRows(rows)
				plan, err := prepareRefreshRuntime(context.Background(), db, &manifest, make([]byte, 32))
				require.NoError(t, err)
				expected := "JWT_REFRESH_TOKEN_STORE=postgres\n"
				if version == 2 {
					expected += "JWT_REFRESH_TOKEN_MIGRATION_READINESS=false\n"
				}
				expected += "REDIS_USERNAME=xiass-app-review\nREDIS_PASSWORD=" + password + "\n"
				require.Equal(t, expected, string(plan.environment("review")), "v1 bytes remain unchanged; v2 explicitly clears the startup opt-in")
				require.NoError(t, writeRefreshRuntimeEnvironment(manifest.Runtime.EnvironmentFile, plan.environment("review")))
				if version == 2 {
					cfg := &config.Config{JWT: config.JWTConfig{RefreshTokenStore: "redis", RefreshTokenMigrationReadiness: true}}
					for _, line := range strings.Split(string(plan.environment("review")), "\n") {
						key, value, _ := strings.Cut(line, "=")
						switch key {
						case "JWT_REFRESH_TOKEN_STORE":
							cfg.JWT.RefreshTokenStore = value
						case "JWT_REFRESH_TOKEN_MIGRATION_READINESS":
							cfg.JWT.RefreshTokenMigrationReadiness, err = strconv.ParseBool(value)
							require.NoError(t, err)
						}
					}
					mock.ExpectQuery("SELECT backend, pg_is_in_recovery").WillReturnRows(sqlmock.NewRows([]string{"backend", "recovery", "read_only"}).AddRow("postgres", false, false))
					mock.ExpectQuery("SELECT.*EXISTS").WillReturnRows(sqlmock.NewRows([]string{"ready"}).AddRow(true))
					store, err := repository.NewRefreshTokenStore(db, nil, cfg)
					require.NoError(t, err, "v2 output must replace an existing readiness=true configuration")
					require.IsType(t, &repository.PersistentRefreshTokenStore{}, store)
				}
			}
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestRefreshRuntimeOldUserFenceIsComplete(t *testing.T) {
	valid := map[string]any{"flags": []any{"off"}, "commands": "-@all", "keys": "", "channels": "", "passwords": []any{}, "selectors": []any{}}
	require.True(t, refreshRuntimeOldUserFenced(valid))
	for _, tc := range []struct {
		field string
		value any
	}{
		{"flags", []any{}}, {"flags", []any{"on"}}, {"flags", []any{"off", "nopass"}},
		{"commands", "+@all"}, {"keys", "~*"}, {"channels", "&*"}, {"passwords", []any{"hash"}},
		{"selectors", []any{map[string]any{"commands": "+@all"}}}, {"selectors", nil},
	} {
		state := maps.Clone(valid)
		state[tc.field] = tc.value
		require.False(t, refreshRuntimeOldUserFenced(state), tc.field)
	}
}

func TestRefreshRuntimePreflightRejectsExistingOutput(t *testing.T) {
	path, manifest := migrationTestManifest(t)
	secretPath := filepath.Join(filepath.Dir(path), "app.secret")
	require.NoError(t, os.WriteFile(secretPath, []byte(strings.Repeat("dc", 32)), 0600))
	output := filepath.Join(filepath.Dir(path), "existing.env")
	require.NoError(t, os.WriteFile(output, []byte("must not replace"), 0600))
	manifest.Runtime = &refreshRuntimeManifest{AppPasswordFile: secretPath, EnvironmentFile: output}
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	mock.ExpectQuery("SELECT transition_id::text FROM refresh_token_legacy_transition").WillReturnRows(sqlmock.NewRows([]string{"transition_id"}))
	_, err = prepareRefreshRuntime(context.Background(), db, &manifest, make([]byte, 32))
	require.ErrorContains(t, err, "refusing to replace")
	require.NoError(t, mock.ExpectationsWereMet())
	data, err := os.ReadFile(output)
	require.NoError(t, err)
	require.Equal(t, "must not replace", string(data))
}

func runtimeBackupTestManifest(t *testing.T) (refreshMigrationManifest, []byte) {
	t.Helper()
	path, manifest := migrationTestManifest(t)
	dir := filepath.Dir(path)
	manifest.Runtime = &refreshRuntimeManifest{
		AppPasswordFile: filepath.Join(dir, "app.secret"), ReplicaPasswordFile: filepath.Join(dir, "replica.secret"),
		BackupPasswordFile: filepath.Join(dir, "backup.secret"), BackupCredentialsFile: filepath.Join(dir, "backup.json"),
		EnvironmentFile: filepath.Join(dir, "runtime.env"),
	}
	for path, password := range map[string]string{
		manifest.Runtime.AppPasswordFile: strings.Repeat("cd", 32), manifest.Runtime.ReplicaPasswordFile: strings.Repeat("ef", 32),
		manifest.Runtime.BackupPasswordFile: strings.Repeat("12", 32),
	} {
		require.NoError(t, os.WriteFile(path, []byte(password), 0600))
	}
	recovery, err := hex.DecodeString(strings.Repeat("ab", 32))
	require.NoError(t, err)
	return manifest, recovery
}

func TestRefreshRuntimeBackupPreflightRejectsInvalidInputs(t *testing.T) {
	for _, name := range []string{
		"password-only", "credentials-only", "relative", "installation-env", "runtime-output", "app-input", "replica-input", "backup-input", "recovery-input",
		"app-password", "replica-password", "recovery-password", "short-password", "invalid-hex", "public-password", "symlink-password",
	} {
		t.Run(name, func(t *testing.T) {
			manifest, recovery := runtimeBackupTestManifest(t)
			r := manifest.Runtime
			switch name {
			case "password-only":
				r.BackupCredentialsFile = ""
			case "credentials-only":
				r.BackupPasswordFile = ""
			case "relative":
				r.BackupCredentialsFile = "backup.json"
			case "installation-env":
				r.BackupCredentialsFile = filepath.Join(filepath.Dir(r.EnvironmentFile), ".env")
			case "runtime-output":
				r.BackupCredentialsFile = r.EnvironmentFile
			case "app-input":
				r.BackupCredentialsFile = r.AppPasswordFile
			case "replica-input":
				r.BackupCredentialsFile = r.ReplicaPasswordFile
			case "backup-input":
				r.BackupCredentialsFile = r.BackupPasswordFile
			case "recovery-input":
				r.BackupCredentialsFile = manifest.RecoverySecretFile
			case "app-password":
				r.BackupPasswordFile = r.AppPasswordFile
			case "replica-password":
				r.BackupPasswordFile = r.ReplicaPasswordFile
			case "recovery-password":
				r.BackupPasswordFile = manifest.RecoverySecretFile
			case "short-password":
				require.NoError(t, os.WriteFile(r.BackupPasswordFile, []byte(strings.Repeat("12", 31)), 0600))
			case "invalid-hex":
				require.NoError(t, os.WriteFile(r.BackupPasswordFile, []byte(strings.Repeat("zz", 32)), 0600))
			case "public-password":
				require.NoError(t, os.Chmod(r.BackupPasswordFile, 0644))
			case "symlink-password":
				link := r.BackupPasswordFile + ".link"
				require.NoError(t, os.Symlink(r.BackupPasswordFile, link))
				r.BackupPasswordFile = link
			}
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer func() { _ = db.Close() }()
			plan, err := prepareRefreshRuntime(context.Background(), db, &manifest, recovery)
			require.Error(t, err)
			require.Nil(t, plan)
			for _, secret := range []string{strings.Repeat("12", 32), strings.Repeat("ab", 32), strings.Repeat("cd", 32), strings.Repeat("ef", 32)} {
				require.False(t, strings.Contains(err.Error(), secret), "errors must not echo test credentials")
			}
			require.NoError(t, mock.ExpectationsWereMet())
			require.NoFileExists(t, r.EnvironmentFile)
		})
	}
}

func TestRefreshRuntimeBackupOutputPreflightAndRetry(t *testing.T) {
	const id = "01234567-89ab-cdef-0123-456789abcdef"
	for _, name := range []string{"absent", "same", "different", "no-witness", "public", "symlink", "directory"} {
		t.Run(name, func(t *testing.T) {
			manifest, recovery := runtimeBackupTestManifest(t)
			output := manifest.Runtime.BackupCredentialsFile
			expected := (&refreshRuntimePlan{backupPassword: strings.Repeat("12", 32)}).backupCredentials(id)
			if name != "absent" && name != "directory" {
				data := expected
				if name == "different" {
					data = []byte("existing output must survive")
				}
				require.NoError(t, os.WriteFile(output, data, 0600))
			}
			if name == "public" {
				require.NoError(t, os.Chmod(output, 0644))
			}
			if name == "symlink" {
				output += ".link"
				require.NoError(t, os.Symlink(manifest.Runtime.BackupCredentialsFile, output))
				manifest.Runtime.BackupCredentialsFile = output
			}
			if name == "directory" {
				require.NoError(t, os.Mkdir(output, 0700))
			}
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer func() { _ = db.Close() }()
			rows := sqlmock.NewRows([]string{"transition_id"})
			if name != "no-witness" && name != "absent" {
				rows.AddRow(id)
			}
			mock.ExpectQuery("SELECT transition_id::text FROM refresh_token_legacy_transition").WillReturnRows(rows)
			plan, err := prepareRefreshRuntime(context.Background(), db, &manifest, recovery)
			if name == "absent" || name == "same" {
				require.NoError(t, err)
				require.True(t, string(expected) == string(plan.backupCredentials(id)))
				require.False(t, strings.Contains(string(plan.environment(id)), plan.backupPassword))
				require.NotContains(t, string(plan.environment(id)), "BACKUP")
				if name == "absent" {
					require.NoFileExists(t, output, "prepare must not publish credentials")
				}
				require.NoError(t, writeRefreshRuntimeEnvironment(output, plan.backupCredentials(id)))
				before, err := os.Stat(output)
				require.NoError(t, err)
				require.Equal(t, os.FileMode(0600), before.Mode().Perm())
				require.NoError(t, writeRefreshRuntimeEnvironment(output, plan.backupCredentials(id)))
				after, err := os.Stat(output)
				require.NoError(t, err)
				require.True(t, os.SameFile(before, after), "retry must not replace the file")
				var credentials map[string]string
				require.NoError(t, json.Unmarshal(plan.backupCredentials(id), &credentials))
				require.Len(t, credentials, 2)
				require.Equal(t, "xiass-backup-"+id, credentials["username"])
				require.True(t, credentials["password"] == plan.backupPassword)
			} else {
				require.ErrorContains(t, err, "refusing to replace")
				require.Nil(t, plan)
			}
			if name == "different" {
				data, err := os.ReadFile(output)
				require.NoError(t, err)
				require.Equal(t, "existing output must survive", string(data))
			}
			require.NoError(t, mock.ExpectationsWereMet())
			require.NoFileExists(t, manifest.Runtime.EnvironmentFile)
		})
	}
}

func TestRefreshRuntimeBackupOptionalManifest(t *testing.T) {
	manifest, recovery := runtimeBackupTestManifest(t)
	data, err := json.Marshal(manifest)
	require.NoError(t, err)
	require.Contains(t, string(data), `"backup_password_file"`)
	require.Contains(t, string(data), `"backup_credentials_file"`)
	var decoded refreshMigrationManifest
	require.NoError(t, json.Unmarshal(data, &decoded))
	require.Equal(t, manifest.Runtime, decoded.Runtime)
	manifest.Runtime.BackupPasswordFile, manifest.Runtime.BackupCredentialsFile = "", ""
	data, err = json.Marshal(manifest)
	require.NoError(t, err)
	require.NotContains(t, string(data), "backup_")
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	mock.ExpectQuery("SELECT transition_id::text FROM refresh_token_legacy_transition").WillReturnRows(sqlmock.NewRows([]string{"transition_id"}))
	plan, err := prepareRefreshRuntime(context.Background(), db, &manifest, recovery)
	require.NoError(t, err)
	require.Empty(t, plan.backupFile)
	require.Empty(t, plan.backupPassword)
	require.NoError(t, mock.ExpectationsWereMet())
}
