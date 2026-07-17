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
	candidates = append(candidates, registeredCodexExecutables()...)
	candidates = append(candidates, packagedCodexExecutables()...)

	localAppData := os.Getenv("LOCALAPPDATA")
	programFiles := os.Getenv("ProgramFiles")
	programFilesX86 := os.Getenv("ProgramFiles(x86)")
	appendWindowsCandidate(&candidates, localAppData, "Programs", "Codex", "Codex.exe")
	appendWindowsCandidate(&candidates, localAppData, "Programs", "ChatGPT", "ChatGPT.exe")
	appendWindowsCandidate(&candidates, localAppData, "Programs", "OpenAI", "Codex", "Codex.exe")
	appendWindowsCandidate(&candidates, localAppData, "Programs", "OpenAI", "Codex.exe")
	appendWindowsCandidate(&candidates, localAppData, "Codex", "Codex.exe")
	appendWindowsCandidate(&candidates, localAppData, "OpenAI", "Codex", "Codex.exe")
	appendWindowsCandidate(&candidates, localAppData, "Microsoft", "WindowsApps", "Codex.exe")
	appendWindowsCandidate(&candidates, programFiles, "Codex", "Codex.exe")
	appendWindowsCandidate(&candidates, programFiles, "ChatGPT", "ChatGPT.exe")
	appendWindowsCandidate(&candidates, programFiles, "OpenAI", "Codex", "Codex.exe")
	appendWindowsCandidate(&candidates, programFilesX86, "Codex", "Codex.exe")
	appendWindowsCandidate(&candidates, programFilesX86, "ChatGPT", "ChatGPT.exe")
	appendWindowsCandidate(&candidates, programFilesX86, "OpenAI", "Codex", "Codex.exe")

	for _, pattern := range []string{
		filepath.Join(localAppData, "Programs", "Codex*", "Codex.exe"),
		filepath.Join(localAppData, "Programs", "ChatGPT*", "ChatGPT.exe"),
		filepath.Join(localAppData, "Programs", "OpenAI*", "Codex.exe"),
		filepath.Join(localAppData, "Codex*", "Codex.exe"),
	} {
		if localAppData == "" {
			continue
		}
		matches, _ := filepath.Glob(pattern)
		candidates = append(candidates, matches...)
	}

	seen := map[string]struct{}{}
	for _, executable := range candidates {
		executable = normalizeWindowsExecutable(executable)
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

	for _, launchTarget := range windowsCodexLaunchTargets() {
		launchTarget = strings.TrimSpace(launchTarget)
		if launchTarget == "" {
			continue
		}
		return CodexInstallation{
			AppPath:      "Microsoft Store / WindowsApps",
			LaunchTarget: launchTarget,
			Running:      isWindowsCodexAppRunning(),
			Found:        true,
		}
	}
	return CodexInstallation{}
}

func normalizeWindowsExecutable(value string) string {
	value = strings.TrimSpace(strings.TrimPrefix(value, "\ufeff"))
	if strings.HasPrefix(value, `"`) {
		if closingQuote := strings.Index(value[1:], `"`); closingQuote >= 0 {
			value = value[1 : closingQuote+1]
		}
	}
	value = strings.Trim(value, `"`)
	if value == "" {
		return ""
	}
	return filepath.Clean(os.ExpandEnv(value))
}

func runningCodexExecutables() []string {
	command := `(Get-CimInstance Win32_Process -ErrorAction SilentlyContinue | Where-Object { ($_.Name -ieq 'Codex.exe' -or $_.Name -ieq 'ChatGPT.exe') -and $_.ExecutablePath } | Select-Object -ExpandProperty ExecutablePath)`
	return windowsPowerShellLines(command)
}

func registeredCodexExecutables() []string {
	command := `$keys = @('HKCU:\Software\Microsoft\Windows\CurrentVersion\App Paths\Codex.exe','HKLM:\Software\Microsoft\Windows\CurrentVersion\App Paths\Codex.exe','HKLM:\Software\WOW6432Node\Microsoft\Windows\CurrentVersion\App Paths\Codex.exe'); foreach ($key in $keys) { if (Test-Path -LiteralPath $key) { $value = (Get-Item -LiteralPath $key).GetValue(''); if ($value) { $value } } }`
	return windowsPowerShellLines(command)
}

func packagedCodexExecutables() []string {
	command := `Get-AppxPackage -ErrorAction SilentlyContinue | Where-Object { $_.Name -match '(?i)codex' -or $_.PackageFamilyName -match '(?i)codex' } | ForEach-Object { $package = $_; try { $manifest = $package | Get-AppxPackageManifest -ErrorAction Stop; foreach ($app in @($manifest.Package.Applications.Application)) { if ($app.Executable) { $candidate = Join-Path $package.InstallLocation $app.Executable; if (Test-Path -LiteralPath $candidate) { $candidate } } } } catch {}; if ($package.InstallLocation) { Get-ChildItem -LiteralPath $package.InstallLocation -Filter 'Codex.exe' -File -Recurse -ErrorAction SilentlyContinue | Select-Object -ExpandProperty FullName } }`
	return windowsPowerShellLines(command)
}

func windowsCodexLaunchTargets() []string {
	command := `Get-StartApps -ErrorAction SilentlyContinue | Where-Object { ($_.Name -match '(?i)codex' -or $_.AppID -match '(?i)codex') -and $_.Name -notmatch '(?i)helper' } | Select-Object -ExpandProperty AppID`
	return windowsPowerShellLines(command)
}

func appendWindowsCandidate(candidates *[]string, root string, parts ...string) {
	if strings.TrimSpace(root) == "" {
		return
	}
	*candidates = append(*candidates, filepath.Join(append([]string{root}, parts...)...))
}

func windowsPowerShellLines(command string) []string {
	script := `[Console]::OutputEncoding = [System.Text.Encoding]::UTF8; ` + command
	output, err := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", script).Output()
	if err != nil {
		return nil
	}
	lines := strings.FieldsFunc(string(output), func(r rune) bool { return r == '\r' || r == '\n' })
	for index := range lines {
		lines[index] = strings.TrimSpace(strings.TrimPrefix(lines[index], "\ufeff"))
	}
	return lines
}

func restartCodex(installation CodexInstallation) error {
	if !installation.Found || (installation.Executable == "" && installation.LaunchTarget == "") {
		return errors.New("Codex App was not found")
	}
	if installation.Executable == "" {
		if installation.Running {
			for _, processID := range windowsCodexProcessIDsByName() {
				_ = exec.Command("taskkill.exe", "/PID", processID, "/T").Run()
			}
			deadline := time.Now().Add(15 * time.Second)
			for isWindowsCodexAppRunning() && time.Now().Before(deadline) {
				time.Sleep(250 * time.Millisecond)
			}
			if isWindowsCodexAppRunning() {
				return errors.New("Codex did not exit within 15 seconds; configuration is saved but the app was not force-closed")
			}
		}
		if err := exec.Command("explorer.exe", `shell:AppsFolder\`+installation.LaunchTarget).Start(); err != nil {
			return err
		}
		deadline := time.Now().Add(15 * time.Second)
		for !isWindowsCodexAppRunning() && time.Now().Before(deadline) {
			time.Sleep(250 * time.Millisecond)
		}
		if !isWindowsCodexAppRunning() {
			return errors.New("Codex did not start within 15 seconds")
		}
		return nil
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

func windowsCodexProcessIDsByName() []string {
	command := `Get-Process -Name 'Codex' -ErrorAction SilentlyContinue | Select-Object -ExpandProperty Id`
	return windowsPowerShellLines(command)
}

func isWindowsCodexAppRunning() bool {
	return len(windowsCodexProcessIDsByName()) > 0
}

func isWindowsCodexExecutable(executable string) bool {
	lower := strings.ToLower(filepath.Clean(executable))
	if strings.EqualFold(filepath.Base(executable), "Codex.exe") {
		for _, cliPath := range []string{`\node_modules\`, `\npm\`, `\.cargo\`, `\scoop\apps\`, `\chocolatey\bin\`} {
			if strings.Contains(lower, cliPath) {
				return false
			}
		}
		return true
	}
	if strings.Contains(lower, `\codex\`) {
		return true
	}
	escapedPath := strings.ReplaceAll(executable, "'", "''")
	command := `(Get-Item -LiteralPath '` + escapedPath + `').VersionInfo.ProductName`
	output := windowsPowerShellLines(command)
	return len(output) > 0 && strings.Contains(strings.ToLower(strings.Join(output, " ")), "codex")
}
