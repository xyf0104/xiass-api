package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLoadLoggingRotationDefaultsAndOverrides(t *testing.T) {
	for _, tc := range []struct {
		name string
		yaml string
		env  map[string]string
		want LogRotationConfig
	}{
		{
			name: "defaults with empty Compose passthrough",
			want: LogRotationConfig{MaxSizeMB: 10, MaxBackups: 3, MaxAgeDays: 7, Compress: true, LocalTime: true},
		},
		{
			name: "existing YAML limits survive empty Compose passthrough",
			yaml: "log:\n  rotation:\n    max_size_mb: 100\n    max_backups: 10\n    max_age_days: 14\n    compress: false\n    local_time: false\n",
			want: LogRotationConfig{MaxSizeMB: 100, MaxBackups: 10, MaxAgeDays: 14, Compress: false, LocalTime: false},
		},
		{
			name: "explicit environment overrides YAML including zero and false",
			yaml: "log:\n  rotation:\n    max_size_mb: 100\n    max_backups: 10\n    max_age_days: 14\n    compress: true\n    local_time: true\n",
			env: map[string]string{
				"LOG_ROTATION_MAX_SIZE_MB": "25", "LOG_ROTATION_MAX_BACKUPS": "0",
				"LOG_ROTATION_MAX_AGE_DAYS": "0", "LOG_ROTATION_COMPRESS": "false",
				"LOG_ROTATION_LOCAL_TIME": "false",
			},
			want: LogRotationConfig{MaxSizeMB: 25, MaxBackups: 0, MaxAgeDays: 0, Compress: false, LocalTime: false},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resetViperWithJWTSecret(t)
			for _, key := range []string{
				"LOG_OUTPUT_TO_STDOUT", "LOG_OUTPUT_TO_FILE", "LOG_OUTPUT_FILE_PATH",
				"LOG_ROTATION_MAX_SIZE_MB", "LOG_ROTATION_MAX_BACKUPS",
				"LOG_ROTATION_MAX_AGE_DAYS", "LOG_ROTATION_COMPRESS", "LOG_ROTATION_LOCAL_TIME",
			} {
				t.Setenv(key, "")
			}
			path := filepath.Join(t.TempDir(), "config.yaml")
			require.NoError(t, os.WriteFile(path, []byte(tc.yaml), 0600))
			t.Setenv("CONFIG_FILE", path)
			for key, value := range tc.env {
				t.Setenv(key, value)
			}
			cfg, err := Load()
			require.NoError(t, err)
			require.Equal(t, tc.want, cfg.Log.Rotation)
			require.True(t, cfg.Log.Output.ToFile)
			require.True(t, cfg.Log.Output.ToStdout)
		})
	}
}
