package service

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
)

const (
	deploymentModeEnv       = "NOWIND_DEPLOYMENT_MODE"
	legacyDeploymentModeEnv = "SUB2API_DEPLOYMENT_MODE"
	watchtowerUpdateURL     = "http://watchtower:8080/v1/update"
	watchtowerTokenEnv      = "NOWIND_WATCHTOWER_TOKEN"
	legacyWatchtowerToken   = "sub2api-update-token"
)

// IsRunningInContainer selects the updater without changing existing Docker
// behavior. The explicit environment override also makes nonstandard runtimes
// deterministic (for example systemd inside a container host namespace).
func IsRunningInContainer() bool {
	mode := strings.TrimSpace(os.Getenv(deploymentModeEnv))
	if mode == "" {
		mode = strings.TrimSpace(os.Getenv(legacyDeploymentModeEnv))
	}
	switch strings.ToLower(mode) {
	case "docker", "container":
		return true
	case "binary", "systemd":
		return false
	}
	_, err := os.Stat("/.dockerenv")
	return err == nil
}

type DockerUpdateService struct {
	updateSvc *UpdateService
}

func NewDockerUpdateService(updateSvc *UpdateService) *DockerUpdateService {
	return &DockerUpdateService{
		updateSvc: updateSvc,
	}
}

func (s *DockerUpdateService) CheckUpdate(ctx context.Context, force bool) (*UpdateInfo, error) {
	return s.updateSvc.CheckUpdate(ctx, force)
}

func (s *DockerUpdateService) PerformUpdate(ctx context.Context) error {
	info, err := s.updateSvc.CheckUpdate(ctx, false)
	if err != nil {
		return fmt.Errorf("failed to check update: %w", err)
	}

	if !info.HasUpdate {
		return ErrNoUpdateAvailable
	}

	// Call Watchtower HTTP API through its stable Compose service DNS.
	req, err := newWatchtowerUpdateRequest(ctx)
	if err != nil {
		return fmt.Errorf("failed to create watchtower request: %w", err)
	}

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to contact watchtower (is it running?): %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("watchtower returned status: %d", resp.StatusCode)
	}

	return nil
}

func newWatchtowerUpdateRequest(ctx context.Context) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, watchtowerUpdateURL, nil)
	if err != nil {
		return nil, err
	}

	token := strings.TrimSpace(os.Getenv(watchtowerTokenEnv))
	if token == "" {
		token = legacyWatchtowerToken
	}
	req.Header.Set("Authorization", "Bearer "+token)
	return req, nil
}

func (s *DockerUpdateService) Rollback() error {
	return fmt.Errorf("rollback is not supported in docker mode")
}

func (s *DockerUpdateService) GetCurrentVersion() string {
	return s.updateSvc.currentVersion
}

// ListRollbackVersions 代理到 UpdateService，返回可回滚的历史版本列表
func (s *DockerUpdateService) ListRollbackVersions(ctx context.Context) ([]RollbackVersion, error) {
	return s.updateSvc.ListRollbackVersions(ctx)
}

func (s *DockerUpdateService) RollbackToVersion(ctx context.Context, version string) error {
	return fmt.Errorf("rollback to version %q is not supported in docker mode", version)
}
