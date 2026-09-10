package main

import (
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/repository"
)

// Retain the fixed grant contract for installations with restricted Redis access.
func refreshSessionRuntimeACL(cfg *config.Config, alertLockKey string) ([]string, error) {
	return repository.RefreshSessionRuntimeACL(cfg, alertLockKey)
}
