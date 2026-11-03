package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	deploymentModeEnv          = "XIASS_DEPLOYMENT_MODE"
	previousDeploymentModeEnv  = "NOWIND_DEPLOYMENT_MODE"
	legacyDeploymentModeEnv    = "SUB2API_DEPLOYMENT_MODE"
	watchtowerUpdateURL        = "http://watchtower:8080/v1/update"
	watchtowerTokenEnv         = "XIASS_WATCHTOWER_TOKEN"
	previousWatchtowerTokenEnv = "NOWIND_WATCHTOWER_TOKEN"
	legacyWatchtowerToken      = "sub2api-update-token"
	dockerUpdateSocketPath     = "/var/run/docker.sock"
	dockerUpdateAPIVersion     = "v1.40"
	updaterImageEnv            = "XIASS_UPDATER_IMAGE"
	defaultUpdaterImage        = "ghcr.io/xyf0104/xiass-updater:latest"
	updaterContainerName       = "xiass-api-updater"
	updaterBackupDir           = "/root/xiass-backups"
	teamChildBrowserEnabledEnv = "TEAM_CHILD_BROWSER_ENABLED"
)

// IsRunningInContainer selects the updater without changing existing Docker
// behavior. The explicit environment override also makes nonstandard runtimes
// deterministic (for example systemd inside a container host namespace).
func IsRunningInContainer() bool {
	mode := strings.TrimSpace(os.Getenv(deploymentModeEnv))
	if mode == "" {
		mode = strings.TrimSpace(os.Getenv(previousDeploymentModeEnv))
	}
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

	return performDockerContainerUpdate(
		ctx,
		teamChildBrowserEnabled(),
		s.launchHostUpdater,
		s.performWatchtowerUpdate,
	)
}

type hostUpdaterLauncher func(context.Context) (bool, error)
type watchtowerUpdater func(context.Context) error

func performDockerContainerUpdate(
	ctx context.Context,
	teamBrowserEnabled bool,
	launchHostUpdater hostUpdaterLauncher,
	performWatchtowerUpdate watchtowerUpdater,
) error {
	started, launchErr := launchHostUpdater(ctx)
	if started {
		return nil
	}

	// A Team-enabled installation must update its Compose files, main app,
	// browser runtime, and automation image as one host-orchestrated operation.
	// Falling back to the app-only Watchtower target creates an incompatible
	// new frontend backed by an old workflow service. Keep the existing stack
	// untouched and surface the launch error instead.
	if teamBrowserEnabled {
		if launchErr != nil {
			return fmt.Errorf("failed to start XIASS host updater; existing containers were not changed: %w", launchErr)
		}
		return fmt.Errorf("failed to start XIASS host updater; existing containers were not changed")
	}

	// Historical lightweight installations without the Team browser retain the
	// original app-only compatibility path.
	return performWatchtowerUpdate(ctx)
}

func teamChildBrowserEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(teamChildBrowserEnabledEnv))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func (s *DockerUpdateService) performWatchtowerUpdate(ctx context.Context) error {
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

type dockerUpdateClient struct {
	client *http.Client
}

type dockerUpdateContainerInfo struct {
	Config struct {
		Labels map[string]string `json:"Labels"`
	} `json:"Config"`
	State struct {
		Running  bool   `json:"Running"`
		ExitCode int    `json:"ExitCode"`
		Error    string `json:"Error"`
	} `json:"State"`
}

type dockerUpdateContainerCreateRequest struct {
	Image      string            `json:"Image"`
	Cmd        []string          `json:"Cmd"`
	Env        []string          `json:"Env"`
	WorkingDir string            `json:"WorkingDir"`
	Labels     map[string]string `json:"Labels"`
	HostConfig struct {
		Binds       []string `json:"Binds"`
		NetworkMode string   `json:"NetworkMode"`
	} `json:"HostConfig"`
}

type dockerPullProgress struct {
	Error       string `json:"error"`
	ErrorDetail struct {
		Message string `json:"message"`
	} `json:"errorDetail"`
}

func newDockerUpdateClient() *dockerUpdateClient {
	return newDockerUpdateClientWithSocket(dockerUpdateSocketPath)
}

func newDockerUpdateClientWithSocket(socketPath string) *dockerUpdateClient {
	return &dockerUpdateClient{client: &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var dialer net.Dialer
			return dialer.DialContext(ctx, "unix", socketPath)
		},
	}, Timeout: 0}}
}

func (c *dockerUpdateClient) request(ctx context.Context, method, path string, body any) (*http.Response, error) {
	var reader io.Reader
	if body != nil {
		payload, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reader = bytes.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://docker/"+dockerUpdateAPIVersion+path, reader)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return c.client.Do(req)
}

func (c *dockerUpdateClient) requestOK(ctx context.Context, method, path string, body any, accepted ...int) ([]byte, error) {
	resp, err := c.request(ctx, method, path, body)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	payload, readErr := io.ReadAll(io.LimitReader(resp.Body, 2*1024*1024))
	if readErr != nil {
		return nil, readErr
	}
	for _, status := range accepted {
		if resp.StatusCode == status {
			return payload, nil
		}
	}
	if len(accepted) == 0 && resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return payload, nil
	}
	return nil, fmt.Errorf("docker request %s %s returned status %d", method, path, resp.StatusCode)
}

func (c *dockerUpdateClient) inspect(ctx context.Context, name string) (*dockerUpdateContainerInfo, error) {
	payload, err := c.requestOK(ctx, http.MethodGet, "/containers/"+url.PathEscape(name)+"/json", nil, http.StatusOK)
	if err != nil {
		return nil, err
	}
	var info dockerUpdateContainerInfo
	if err := json.Unmarshal(payload, &info); err != nil {
		return nil, err
	}
	return &info, nil
}

func (c *dockerUpdateClient) discoverInstallDir(ctx context.Context) (string, error) {
	candidates := []string{}
	if hostname, err := os.Hostname(); err == nil && strings.TrimSpace(hostname) != "" {
		candidates = append(candidates, hostname)
	}
	candidates = append(candidates, "xiass-api", "nowind-api", "sub2api")
	for _, candidate := range candidates {
		info, err := c.inspect(ctx, candidate)
		if err != nil {
			continue
		}
		workingDir := strings.TrimSpace(info.Config.Labels["com.docker.compose.project.working_dir"])
		if workingDir == "" {
			continue
		}
		if filepath.Base(filepath.Clean(workingDir)) == "deploy" {
			workingDir = filepath.Dir(filepath.Clean(workingDir))
		}
		if !validUpdaterMountPath(workingDir) {
			return "", fmt.Errorf("invalid Compose working directory")
		}
		return workingDir, nil
	}
	return "", fmt.Errorf("could not discover Compose working directory")
}

func validUpdaterMountPath(path string) bool {
	clean := filepath.Clean(strings.TrimSpace(path))
	return filepath.IsAbs(clean) && clean != "/" && clean != "." && !strings.Contains(clean, "\x00")
}

func updaterImage() string {
	image := strings.TrimSpace(os.Getenv(updaterImageEnv))
	if image == "" {
		return defaultUpdaterImage
	}
	return image
}

func (c *dockerUpdateClient) pullImage(ctx context.Context, image string) error {
	imageName, imageTag := image, "latest"
	if at := strings.LastIndex(image, ":"); at > strings.LastIndex(image, "/") {
		imageName, imageTag = image[:at], image[at+1:]
	}
	pullPath := "/images/create?fromImage=" + url.QueryEscape(imageName) + "&tag=" + url.QueryEscape(imageTag)
	pullCtx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	pullResp, err := c.request(pullCtx, http.MethodPost, pullPath, nil)
	if err != nil {
		return err
	}
	pullStreamErr := readDockerPullProgress(pullResp.Body)
	closeErr := pullResp.Body.Close()
	if pullResp.StatusCode != http.StatusOK {
		return fmt.Errorf("updater image pull returned status %d", pullResp.StatusCode)
	}
	if pullStreamErr != nil {
		return fmt.Errorf("updater image pull failed: %w", pullStreamErr)
	}
	if closeErr != nil {
		return fmt.Errorf("updater image pull response close failed: %w", closeErr)
	}
	return nil
}

func (s *DockerUpdateService) launchHostUpdater(ctx context.Context) (bool, error) {
	return s.launchHostUpdaterWithClient(ctx, newDockerUpdateClient())
}

func (s *DockerUpdateService) launchHostUpdaterWithClient(ctx context.Context, client *dockerUpdateClient) (bool, error) {
	installDir, err := client.discoverInstallDir(ctx)
	if err != nil {
		return false, err
	}

	if existing, inspectErr := client.inspect(ctx, updaterContainerName); inspectErr == nil {
		if existing.State.Running {
			return true, nil
		}
		_, _ = client.requestOK(ctx, http.MethodDelete, "/containers/"+url.PathEscape(updaterContainerName)+"?force=1", nil, http.StatusNoContent, http.StatusNotFound)
	}

	image := updaterImage()
	if err := client.pullImage(ctx, image); err != nil {
		return false, err
	}

	create := dockerUpdateContainerCreateRequest{
		Image:      image,
		Env:        []string{"INSTALL_DIR=" + installDir, "BACKUP_DIR=" + updaterBackupDir},
		WorkingDir: installDir,
		Labels: map[string]string{
			"com.xiass.role": "update-orchestrator",
		},
	}
	create.HostConfig.Binds = []string{
		dockerUpdateSocketPath + ":" + dockerUpdateSocketPath,
		installDir + ":" + installDir,
		updaterBackupDir + ":" + updaterBackupDir,
	}
	// The updater's backup, startup, and rollback gates probe the published
	// application health endpoint on the host loopback interface.
	create.HostConfig.NetworkMode = "host"

	createPayload, err := client.requestOK(ctx, http.MethodPost, "/containers/create?name="+url.QueryEscape(updaterContainerName), &create, http.StatusCreated)
	if err != nil {
		return false, err
	}
	var created struct {
		ID string `json:"Id"`
	}
	if err := json.Unmarshal(createPayload, &created); err != nil || strings.TrimSpace(created.ID) == "" {
		return false, fmt.Errorf("updater container creation returned no id")
	}
	if _, err := client.requestOK(ctx, http.MethodPost, "/containers/"+url.PathEscape(created.ID)+"/start", nil, http.StatusNoContent, http.StatusNotModified); err != nil {
		return false, err
	}
	return true, nil
}

func readDockerPullProgress(body io.Reader) error {
	decoder := json.NewDecoder(body)
	for {
		var progress dockerPullProgress
		if err := decoder.Decode(&progress); err != nil {
			if err == io.EOF {
				return nil
			}
			return fmt.Errorf("invalid Docker pull response: %w", err)
		}
		detail := strings.TrimSpace(progress.ErrorDetail.Message)
		if detail == "" {
			detail = strings.TrimSpace(progress.Error)
		}
		if detail != "" {
			return fmt.Errorf("%s", detail)
		}
	}
}

func newWatchtowerUpdateRequest(ctx context.Context) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, watchtowerUpdateURL, nil)
	if err != nil {
		return nil, err
	}

	token := strings.TrimSpace(os.Getenv(watchtowerTokenEnv))
	if token == "" {
		token = strings.TrimSpace(os.Getenv(previousWatchtowerTokenEnv))
	}
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
