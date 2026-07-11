package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestIsRunningInContainerHonorsExplicitDeploymentMode(t *testing.T) {
	t.Run("docker", func(t *testing.T) {
		t.Setenv(deploymentModeEnv, "docker")
		require.True(t, IsRunningInContainer())
	})

	t.Run("systemd", func(t *testing.T) {
		t.Setenv(deploymentModeEnv, "systemd")
		require.False(t, IsRunningInContainer())
	})

	t.Run("legacy environment fallback", func(t *testing.T) {
		t.Setenv(deploymentModeEnv, "")
		t.Setenv(legacyDeploymentModeEnv, "systemd")
		require.False(t, IsRunningInContainer())
	})
}

func TestNewWatchtowerUpdateRequest(t *testing.T) {
	t.Run("uses service DNS and configured token", func(t *testing.T) {
		t.Setenv(watchtowerTokenEnv, "nowind-token")

		req, err := newWatchtowerUpdateRequest(context.Background())
		require.NoError(t, err)
		require.Equal(t, watchtowerUpdateURL, req.URL.String())
		require.Equal(t, "watchtower", req.URL.Hostname())
		require.Equal(t, "Bearer nowind-token", req.Header.Get("Authorization"))
	})

	t.Run("falls back to v1.0.65 token", func(t *testing.T) {
		t.Setenv(watchtowerTokenEnv, "  ")

		req, err := newWatchtowerUpdateRequest(context.Background())
		require.NoError(t, err)
		require.Equal(t, "Bearer "+legacyWatchtowerToken, req.Header.Get("Authorization"))
	})
}
