package main

import (
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/repository"
)

// Keep the server entry point; migration and runtime share one fixed grant list.
func refreshSessionRuntimeACL(cfg *config.Config, alertLockKey string) ([]string, error) {
	return repository.RefreshSessionRuntimeACL(cfg, alertLockKey)
}
