//go:build !darwin && !windows

package main

import (
	"errors"
	"os/exec"
)

func detectCodexInstallation() CodexInstallation {
	executable, err := exec.LookPath("codex")
	if err != nil {
		return CodexInstallation{}
	}
	return CodexInstallation{Executable: executable, AppPath: executable, Found: true}
}

func selectCodexInstallation() (CodexInstallation, error) {
	return CodexInstallation{}, errors.New("manual Codex App selection is currently supported on macOS and Windows")
}

func restartCodex(CodexInstallation) error {
	return errors.New("automatic Codex restart is currently supported on macOS and Windows")
}

func stopCodex(CodexInstallation) error {
	return errors.New("automatic Codex shutdown is currently supported on macOS and Windows")
}

func startCodex(CodexInstallation) error {
	return errors.New("automatic Codex launch is currently supported on macOS and Windows")
}

func openBrowser(target string) error {
	return exec.Command("xdg-open", target).Start()
}
