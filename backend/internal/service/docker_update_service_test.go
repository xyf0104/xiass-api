package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
		t.Setenv(previousDeploymentModeEnv, "")
		t.Setenv(legacyDeploymentModeEnv, "systemd")
		require.False(t, IsRunningInContainer())
	})

	t.Run("previous XIASS deployment variable remains supported", func(t *testing.T) {
		t.Setenv(deploymentModeEnv, "")
		t.Setenv(previousDeploymentModeEnv, "docker")
		t.Setenv(legacyDeploymentModeEnv, "systemd")
		require.True(t, IsRunningInContainer())
	})
}

func TestTeamChildBrowserEnabled(t *testing.T) {
	for _, value := range []string{"1", "true", "TRUE", "yes", "on"} {
		t.Run(value, func(t *testing.T) {
			t.Setenv(teamChildBrowserEnabledEnv, value)
			require.True(t, teamChildBrowserEnabled())
		})
	}

	for _, value := range []string{"", "0", "false", "off", "unexpected"} {
		t.Run("disabled_"+value, func(t *testing.T) {
			t.Setenv(teamChildBrowserEnabledEnv, value)
			require.False(t, teamChildBrowserEnabled())
		})
	}
}

func TestPerformDockerContainerUpdate(t *testing.T) {
	t.Run("host updater success never invokes watchtower", func(t *testing.T) {
		watchtowerCalled := false
		err := performDockerContainerUpdate(
			context.Background(),
			true,
			func(context.Context) (bool, error) { return true, nil },
			func(context.Context) error {
				watchtowerCalled = true
				return nil
			},
		)
		require.NoError(t, err)
		require.False(t, watchtowerCalled)
	})

	t.Run("team installation rejects partial watchtower fallback", func(t *testing.T) {
		watchtowerCalled := false
		err := performDockerContainerUpdate(
			context.Background(),
			true,
			func(context.Context) (bool, error) { return false, fmt.Errorf("updater unavailable") },
			func(context.Context) error {
				watchtowerCalled = true
				return nil
			},
		)
		require.ErrorContains(t, err, "existing containers were not changed")
		require.ErrorContains(t, err, "updater unavailable")
		require.False(t, watchtowerCalled)
	})

	t.Run("team installation rejects empty launcher result", func(t *testing.T) {
		watchtowerCalled := false
		err := performDockerContainerUpdate(
			context.Background(),
			true,
			func(context.Context) (bool, error) { return false, nil },
			func(context.Context) error {
				watchtowerCalled = true
				return nil
			},
		)
		require.ErrorContains(t, err, "existing containers were not changed")
		require.False(t, watchtowerCalled)
	})

	t.Run("lightweight installation retains watchtower compatibility", func(t *testing.T) {
		watchtowerCalled := false
		err := performDockerContainerUpdate(
			context.Background(),
			false,
			func(context.Context) (bool, error) { return false, fmt.Errorf("updater unavailable") },
			func(context.Context) error {
				watchtowerCalled = true
				return nil
			},
		)
		require.NoError(t, err)
		require.True(t, watchtowerCalled)
	})
}

func TestReadDockerPullProgress(t *testing.T) {
	t.Run("accepts successful progress stream", func(t *testing.T) {
		stream := strings.NewReader("{\"status\":\"Pulling from xyf0104/xiass-updater\"}\n{\"status\":\"Downloaded newer image\"}\n")
		require.NoError(t, readDockerPullProgress(stream))
	})

	t.Run("returns an error embedded in a successful HTTP stream", func(t *testing.T) {
		stream := strings.NewReader("{\"status\":\"Pulling\"}\n{\"errorDetail\":{\"message\":\"manifest denied\"},\"error\":\"manifest denied\"}\n")
		require.ErrorContains(t, readDockerPullProgress(stream), "manifest denied")
	})

	t.Run("rejects malformed daemon output", func(t *testing.T) {
		require.ErrorContains(t, readDockerPullProgress(strings.NewReader("not-json")), "invalid Docker pull response")
	})
}

func TestNewWatchtowerUpdateRequest(t *testing.T) {
	t.Run("uses service DNS and configured token", func(t *testing.T) {
		t.Setenv(watchtowerTokenEnv, "xiass-token")

		req, err := newWatchtowerUpdateRequest(context.Background())
		require.NoError(t, err)
		require.Equal(t, watchtowerUpdateURL, req.URL.String())
		require.Equal(t, "watchtower", req.URL.Hostname())
		require.Equal(t, "Bearer xiass-token", req.Header.Get("Authorization"))
	})

	t.Run("uses the previous token variable as a compatibility fallback", func(t *testing.T) {
		t.Setenv(watchtowerTokenEnv, "")
		t.Setenv(previousWatchtowerTokenEnv, "nowind-token")

		req, err := newWatchtowerUpdateRequest(context.Background())
		require.NoError(t, err)
		require.Equal(t, "Bearer nowind-token", req.Header.Get("Authorization"))
	})

	t.Run("falls back to v1.0.65 token", func(t *testing.T) {
		t.Setenv(watchtowerTokenEnv, "  ")
		t.Setenv(previousWatchtowerTokenEnv, "")

		req, err := newWatchtowerUpdateRequest(context.Background())
		require.NoError(t, err)
		require.Equal(t, "Bearer "+legacyWatchtowerToken, req.Header.Get("Authorization"))
	})
}

func TestValidUpdaterMountPath(t *testing.T) {
	tests := []struct {
		name  string
		path  string
		valid bool
	}{
		{name: "absolute install directory", path: "/opt/xiass-api", valid: true},
		{name: "root is rejected", path: "/", valid: false},
		{name: "relative path is rejected", path: "opt/xiass-api", valid: false},
		{name: "empty path is rejected", path: "", valid: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.valid, validUpdaterMountPath(tt.path))
		})
	}
}

func TestLaunchHostUpdaterCreatesScopedUpdaterContainer(t *testing.T) {
	socketPath := filepath.Join(os.TempDir(), fmt.Sprintf("xiass-docker-%d.sock", time.Now().UnixNano()))
	t.Cleanup(func() { _ = os.Remove(socketPath) })
	listener, err := net.Listen("unix", socketPath)
	require.NoError(t, err)
	payloadCh := make(chan dockerUpdateContainerCreateRequest, 1)
	decodeErrCh := make(chan error, 1)
	unexpectedCh := make(chan string, 1)
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/containers/xiass-api/json"):
			_, _ = io.WriteString(w, `{"Config":{"Labels":{"com.docker.compose.project.working_dir":"/opt/xiass-api/deploy"}},"State":{"Running":true}}`)
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/containers/"):
			w.WriteHeader(http.StatusNotFound)
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/images/create"):
			_, _ = io.WriteString(w, "{}")
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/containers/create"):
			var payload dockerUpdateContainerCreateRequest
			decodeErrCh <- json.NewDecoder(r.Body).Decode(&payload)
			payloadCh <- payload
			w.WriteHeader(http.StatusCreated)
			_, _ = io.WriteString(w, `{"Id":"updater-id"}`)
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/containers/updater-id/start"):
			w.WriteHeader(http.StatusNoContent)
		default:
			unexpectedCh <- r.Method + " " + r.URL.RequestURI()
			http.NotFound(w, r)
		}
	}))
	oldListener := server.Listener
	require.NoError(t, oldListener.Close())
	server.Listener = listener
	server.Start()
	defer server.Close()

	t.Setenv(updaterImageEnv, "ghcr.io/test/xiass-updater:latest")
	client := newDockerUpdateClientWithSocket(socketPath)
	started, err := (&DockerUpdateService{}).launchHostUpdaterWithClient(context.Background(), client)
	require.NoError(t, err)
	require.True(t, started)
	select {
	case unexpected := <-unexpectedCh:
		t.Fatalf("unexpected Docker API request: %s", unexpected)
	default:
	}
	select {
	case decodeErr := <-decodeErrCh:
		require.NoError(t, decodeErr)
	case <-time.After(time.Second):
		t.Fatal("updater create request was not received")
	}
	select {
	case payload := <-payloadCh:
		require.Equal(t, "ghcr.io/test/xiass-updater:latest", payload.Image)
		require.Empty(t, payload.Cmd)
		require.Equal(t, "/opt/xiass-api", payload.WorkingDir)
		require.Contains(t, payload.HostConfig.Binds, "/var/run/docker.sock:/var/run/docker.sock")
		require.Contains(t, payload.HostConfig.Binds, "/opt/xiass-api:/opt/xiass-api")
		require.Contains(t, payload.HostConfig.Binds, "/root/xiass-backups:/root/xiass-backups")
		require.Equal(t, "host", payload.HostConfig.NetworkMode)
	case <-time.After(time.Second):
		t.Fatal("updater create payload was not captured")
	}
}
