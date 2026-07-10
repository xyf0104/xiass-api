package service

import (
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
}
