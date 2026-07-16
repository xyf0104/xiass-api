//go:build windows

package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

func detectCodexInstallation() CodexInstallation {
	candidates := make([]string, 0)
	for _, executable := range runningCodexExecutables() {
		if isWindowsCodexExecutable(executable) {
			candidates = append(candidates, executable)
		}
	}
	localAppData := os.Getenv("LOCALAPPDATA")
	programFiles := os.Getenv("ProgramFiles")
	candidates = append(candidates,
		filepath.Join(localAppData, "Programs", "Codex", "Codex.exe"),
		filepath.Join(localAppData, "Programs", "ChatGPT", "ChatGPT.exe"),
		filepath.Join(localAppData, "Programs", "OpenAI", "Codex", "Codex.exe"),
		filepath.Join(programFiles, "Codex", "Codex.exe"),
		filepath.Join(programFiles, "ChatGPT", "ChatGPT.exe"),
	)

	seen := map[string]struct{}{}
	for _, executable := range candidates {
		executable = strings.TrimSpace(executable)
		if executable == "" {
			continue
		}
		lower := strings.ToLower(executable)
		if _, ok := seen[lower]; ok {
			continue
		}
		seen[lower] = struct{}{}
		if info, err := os.Stat(executable); err != nil || info.IsDir() {
			continue
		}
		if !isWindowsCodexExecutable(executable) {
			continue
		}
		return CodexInstallation{
			AppPath:    filepath.Dir(executable),
			Executable: executable,
			Running:    isWindowsExecutableRunning(executable),
			Found:      true,
		}
	}
	return CodexInstallation{}
}

func runningCodexExecutables() []string {
	command := `(Get-CimInstance Win32_Process | Where-Object { ($_.Name -eq 'Codex.exe' -or $_.Name -eq 'ChatGPT.exe') -and $_.ExecutablePath } | Select-Object -ExpandProperty ExecutablePath)`
	output, err := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", command).Output()
	if err != nil {
		return nil
	}
	return strings.FieldsFunc(string(output), func(r rune) bool { return r == '\r' || r == '\n' })
}

func restartCodex(installation CodexInstallation) error {
	if !installation.Found || installation.Executable == "" {
		return errors.New("Codex App was not found")
	}
	if installation.Running || isWindowsExecutableRunning(installation.Executable) {
		for _, processID := range windowsProcessIDs(installation.Executable) {
			_ = exec.Command("taskkill.exe", "/PID", processID, "/T").Run()
		}
		deadline := time.Now().Add(15 * time.Second)
		for isWindowsExecutableRunning(installation.Executable) && time.Now().Before(deadline) {
			time.Sleep(250 * time.Millisecond)
		}
		if isWindowsExecutableRunning(installation.Executable) {
			return errors.New("Codex did not exit within 15 seconds; configuration is saved but the app was not force-closed")
		}
	}
	command := exec.Command(installation.Executable)
	command.Dir = filepath.Dir(installation.Executable)
	if err := command.Start(); err != nil {
		return err
	}
	deadline := time.Now().Add(15 * time.Second)
	for !isWindowsExecutableRunning(installation.Executable) && time.Now().Before(deadline) {
		time.Sleep(250 * time.Millisecond)
	}
	if !isWindowsExecutableRunning(installation.Executable) {
		return errors.New("Codex did not start within 15 seconds")
	}
	return nil
}

func openBrowser(target string) error {
	return exec.Command("rundll32.exe", "url.dll,FileProtocolHandler", target).Start()
}

func windowsProcessIDs(executable string) []string {
	escapedPath := strings.ReplaceAll(executable, "'", "''")
	command := `Get-CimInstance Win32_Process | Where-Object { $_.ExecutablePath -eq '` + escapedPath + `' } | Select-Object -ExpandProperty ProcessId`
	output, err := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", command).Output()
	if err != nil {
		return nil
	}
	return strings.Fields(string(output))
}

func isWindowsExecutableRunning(executable string) bool {
	return len(windowsProcessIDs(executable)) > 0
}

func isWindowsCodexExecutable(executable string) bool {
	lower := strings.ToLower(filepath.Clean(executable))
	if strings.EqualFold(filepath.Base(executable), "Codex.exe") || strings.Contains(lower, `\codex\`) {
		return true
	}
	escapedPath := strings.ReplaceAll(executable, "'", "''")
	command := `(Get-Item -LiteralPath '` + escapedPath + `').VersionInfo.ProductName`
	output, err := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", command).Output()
	return err == nil && strings.Contains(strings.ToLower(string(output)), "codex")
}
