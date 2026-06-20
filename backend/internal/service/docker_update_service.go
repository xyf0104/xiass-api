package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

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

	// Call Watchtower HTTP API
	req, err := http.NewRequestWithContext(ctx, "GET", "http://sub2api-watchtower:8080/v1/update", nil)
	if err != nil {
		return fmt.Errorf("failed to create watchtower request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer sub2api-update-token")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to contact watchtower (is it running?): %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("watchtower returned status: %d", resp.StatusCode)
	}

	return nil
}

func (s *DockerUpdateService) Rollback() error {
	return fmt.Errorf("rollback is not supported in docker mode")
}

func (s *DockerUpdateService) GetCurrentVersion() string {
	return s.updateSvc.currentVersion
}
