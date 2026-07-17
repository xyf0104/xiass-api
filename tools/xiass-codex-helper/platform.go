package main

import (
	"os"
	"path/filepath"
)

type CodexInstallation struct {
	AppPath      string `json:"app_path"`
	Executable   string `json:"executable"`
	LaunchTarget string `json:"launch_target,omitempty"`
	Running      bool   `json:"running"`
	Found        bool   `json:"found"`
}

func defaultCodexHome() (string, error) {
	if configured := os.Getenv("CODEX_HOME"); configured != "" {
		return filepath.Abs(configured)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".codex"), nil
}
