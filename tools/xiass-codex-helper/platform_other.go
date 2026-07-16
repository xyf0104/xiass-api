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

func restartCodex(CodexInstallation) error {
	return errors.New("automatic Codex restart is currently supported on macOS and Windows")
}

func openBrowser(target string) error {
	return exec.Command("xdg-open", target).Start()
}
