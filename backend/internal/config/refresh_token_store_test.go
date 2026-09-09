package config

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLoadRefreshTokenStore(t *testing.T) {
	for _, value := range []string{"", "redis", "postgres", "POSTGRES", "other"} {
		t.Run(value, func(t *testing.T) {
			resetViperWithJWTSecret(t)
			if value != "" {
				t.Setenv("JWT_REFRESH_TOKEN_STORE", value)
			}
			cfg, err := Load()
			if value == "POSTGRES" || value == "other" {
				require.ErrorContains(t, err, "jwt.refresh_token_store")
				return
			}
			require.NoError(t, err)
			if value == "" {
				value = "redis"
			}
			require.Equal(t, value, cfg.JWT.RefreshTokenStore)
		})
	}
}

func TestLoadRefreshTokenMigrationReadiness(t *testing.T) {
	for _, tc := range []struct {
		name, store, readiness string
		want, wantError        bool
	}{
		{name: "ordinary Redis stays unchanged"},
		{name: "explicit false", readiness: "false"},
		{name: "opt-in Redis", store: "redis", readiness: "true", want: true},
		{name: "opt-in default store", readiness: "true", want: true},
		{name: "explicit PG final configuration", store: "postgres"},
		{name: "PG is not readiness", store: "postgres", readiness: "true", wantError: true},
		{name: "invalid readiness does not silently enable", readiness: "maybe", wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resetViperWithJWTSecret(t)
			t.Setenv("JWT_REFRESH_TOKEN_STORE", tc.store)
			t.Setenv("JWT_REFRESH_TOKEN_MIGRATION_READINESS", tc.readiness)
			cfg, err := Load()
			if tc.wantError {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.want, cfg.JWT.RefreshTokenMigrationReadiness)
		})
	}
}
