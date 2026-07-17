//go:build windows

package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

func detectCodexInstallation() CodexInstallation {
	launchTargets := windowsCodexLaunchTargets()
	candidates := make([]string, 0)
	for _, executable := range runningCodexExecutables() {
		if isWindowsCodexExecutable(executable) {
			candidates = append(candidates, executable)
		}
	}
	candidates = append(candidates, registeredCodexExecutables()...)

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
		if isWindowsPackagedExecutable(executable) {
			if len(launchTargets) == 0 {
				continue
			}
			return CodexInstallation{
				AppPath:      "Microsoft Store / WindowsApps",
				Executable:   executable,
				LaunchTarget: launchTargets[0],
				Running:      isWindowsExecutableRunning(executable),
				Found:        true,
			}
		}
		return CodexInstallation{
			AppPath:    filepath.Dir(executable),
			Executable: executable,
			Running:    isWindowsExecutableRunning(executable),
			Found:      true,
		}
	}

	for _, launchTarget := range launchTargets {
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

func windowsCodexLaunchTargets() []string {
	command := `$targets = @(Get-StartApps -ErrorAction SilentlyContinue | Where-Object { ($_.Name -match '(?i)codex' -or $_.AppID -match '(?i)codex') -and $_.Name -notmatch '(?i)helper' } | Select-Object -ExpandProperty AppID); if ($targets.Count -eq 0) { Get-AppxPackage -ErrorAction SilentlyContinue | Where-Object { $_.Name -match '(?i)codex' -or $_.PackageFamilyName -match '(?i)codex' } | ForEach-Object { $package = $_; try { $manifest = $package | Get-AppxPackageManifest -ErrorAction Stop; foreach ($app in @($manifest.Package.Applications.Application)) { if ($app.Id -and $app.Id -notmatch '(?i)helper') { $targets += $package.PackageFamilyName + '!' + $app.Id } } } catch {} } }; $targets | Where-Object { $_ } | Sort-Object -Unique`
	return windowsPowerShellLines(command)
}

func selectCodexInstallation() (CodexInstallation, error) {
	command := `Add-Type -AssemblyName System.Windows.Forms; $dialog = New-Object System.Windows.Forms.OpenFileDialog; $dialog.Title = 'Select Codex App'; $dialog.Filter = 'Codex App|Codex.exe;ChatGPT.exe|Windows applications|*.exe'; $dialog.CheckFileExists = $true; $dialog.Multiselect = $false; if ($env:LOCALAPPDATA) { $dialog.InitialDirectory = $env:LOCALAPPDATA }; try { if ($dialog.ShowDialog() -eq [System.Windows.Forms.DialogResult]::OK) { $dialog.FileName } } finally { $dialog.Dispose() }`
	selected := windowsPowerShellDialogLines(command)
	if len(selected) == 0 {
		return CodexInstallation{}, errors.New("no Codex App was selected")
	}
	executable := normalizeWindowsExecutable(selected[0])
	if info, err := os.Stat(executable); err != nil || info.IsDir() {
		return CodexInstallation{}, errors.New("the selected Codex App does not exist")
	}
	if !isWindowsCodexExecutable(executable) {
		return CodexInstallation{}, errors.New("the selected application is not Codex App")
	}
	if isWindowsPackagedExecutable(executable) {
		launchTargets := windowsCodexLaunchTargets()
		if len(launchTargets) == 0 {
			return CodexInstallation{}, errors.New("the selected Microsoft Store Codex App has no registered launch target")
		}
		return CodexInstallation{
			AppPath:      "Microsoft Store / WindowsApps",
			Executable:   executable,
			LaunchTarget: launchTargets[0],
			Running:      isWindowsExecutableRunning(executable),
			Found:        true,
		}, nil
	}
	return CodexInstallation{
		AppPath:    filepath.Dir(executable),
		Executable: executable,
		Running:    isWindowsExecutableRunning(executable),
		Found:      true,
	}, nil
}

func appendWindowsCandidate(candidates *[]string, root string, parts ...string) {
	if strings.TrimSpace(root) == "" {
		return
	}
	*candidates = append(*candidates, filepath.Join(append([]string{root}, parts...)...))
}

func windowsPowerShellLines(command string) []string {
	return runWindowsPowerShellLines(false, command)
}

func windowsPowerShellDialogLines(command string) []string {
	return runWindowsPowerShellLines(true, command)
}

func runWindowsPowerShellLines(dialog bool, command string) []string {
	script := `[Console]::OutputEncoding = [System.Text.Encoding]::UTF8; ` + command
	arguments := []string{"-NoProfile"}
	if dialog {
		arguments = append(arguments, "-STA")
	} else {
		arguments = append(arguments, "-NonInteractive")
	}
	arguments = append(arguments, "-Command", script)
	output, err := hiddenWindowsCommand("powershell.exe", arguments...).Output()
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
	if err := stopCodex(installation); err != nil {
		return err
	}
	return startCodex(installation)
}

func stopCodex(installation CodexInstallation) error {
	if !installation.Found || (installation.Executable == "" && installation.LaunchTarget == "") {
		return errors.New("Codex App was not found")
	}
	processIDs, err := windowsInstallationProcessIDsWithError(installation)
	if err != nil {
		return fmt.Errorf("could not verify whether Codex is running: %w", err)
	}
	if len(processIDs) > 0 {
		for _, processID := range processIDs {
			_ = hiddenWindowsCommand("taskkill.exe", "/PID", processID, "/T").Run()
		}
		deadline := time.Now().Add(15 * time.Second)
		for len(processIDs) > 0 && time.Now().Before(deadline) {
			time.Sleep(250 * time.Millisecond)
			processIDs, err = windowsInstallationProcessIDsWithError(installation)
			if err != nil {
				return fmt.Errorf("could not verify that Codex exited: %w", err)
			}
		}
		if len(processIDs) > 0 {
			return errors.New("Codex did not exit within 15 seconds; configuration is saved but the app was not force-closed")
		}
	}
	return nil
}

func startCodex(installation CodexInstallation) error {
	if !installation.Found || (installation.Executable == "" && installation.LaunchTarget == "") {
		return errors.New("Codex App was not found")
	}
	if installation.LaunchTarget != "" {
		if err := exec.Command("explorer.exe", `shell:AppsFolder\`+installation.LaunchTarget).Start(); err != nil {
			return err
		}
		deadline := time.Now().Add(15 * time.Second)
		processIDs, statusErr := windowsStartedProcessIDsWithError(installation)
		if statusErr != nil {
			return fmt.Errorf("could not verify that Codex started: %w", statusErr)
		}
		for len(processIDs) == 0 && time.Now().Before(deadline) {
			time.Sleep(250 * time.Millisecond)
			processIDs, statusErr = windowsStartedProcessIDsWithError(installation)
			if statusErr != nil {
				return fmt.Errorf("could not verify that Codex started: %w", statusErr)
			}
		}
		if len(processIDs) == 0 {
			return errors.New("Codex did not start within 15 seconds")
		}
		return nil
	}
	command := exec.Command(installation.Executable)
	command.Dir = filepath.Dir(installation.Executable)
	if err := command.Start(); err != nil {
		return err
	}
	deadline := time.Now().Add(15 * time.Second)
	processIDs, statusErr := windowsProcessIDsWithError(installation.Executable)
	if statusErr != nil {
		return fmt.Errorf("could not verify that Codex started: %w", statusErr)
	}
	for len(processIDs) == 0 && time.Now().Before(deadline) {
		time.Sleep(250 * time.Millisecond)
		processIDs, statusErr = windowsProcessIDsWithError(installation.Executable)
		if statusErr != nil {
			return fmt.Errorf("could not verify that Codex started: %w", statusErr)
		}
	}
	if len(processIDs) == 0 {
		return errors.New("Codex did not start within 15 seconds")
	}
	return nil
}

func openBrowser(target string) error {
	return exec.Command("rundll32.exe", "url.dll,FileProtocolHandler", target).Start()
}

func windowsProcessIDs(executable string) []string {
	processIDs, _ := windowsProcessIDsWithError(executable)
	return processIDs
}

func windowsProcessIDsWithError(executable string) ([]string, error) {
	target := filepath.Clean(executable)
	targetName := filepath.Base(target)
	processes, err := snapshotWindowsProcesses()
	if err != nil {
		return nil, err
	}
	var processIDs []string
	for _, process := range processes {
		if !strings.EqualFold(process.Name, targetName) || process.Path == "" {
			continue
		}
		if strings.EqualFold(filepath.Clean(process.Path), target) {
			processIDs = append(processIDs, strconv.FormatUint(uint64(process.ID), 10))
		}
	}
	return processIDs, nil
}

func isWindowsExecutableRunning(executable string) bool {
	return len(windowsProcessIDs(executable)) > 0
}

func windowsCodexProcessIDsByName() []string {
	processIDs, _ := windowsCodexProcessIDsByNameWithError()
	return processIDs
}

func windowsCodexProcessIDsByNameWithError() ([]string, error) {
	processes, err := snapshotWindowsProcesses()
	if err != nil {
		return nil, err
	}
	var processIDs []string
	for _, process := range processes {
		if strings.EqualFold(process.Name, "Codex.exe") {
			processIDs = append(processIDs, strconv.FormatUint(uint64(process.ID), 10))
			continue
		}
		if !strings.EqualFold(process.Name, "ChatGPT.exe") {
			continue
		}
		path := strings.ToLower(filepath.Clean(process.Path))
		if strings.Contains(path, "openai.codex_") || strings.Contains(path, `\codex\`) {
			processIDs = append(processIDs, strconv.FormatUint(uint64(process.ID), 10))
		}
	}
	return processIDs, nil
}

func windowsInstallationProcessIDsWithError(installation CodexInstallation) ([]string, error) {
	if installation.Executable != "" {
		processIDs, err := windowsProcessIDsWithError(installation.Executable)
		if err != nil || len(processIDs) > 0 || installation.LaunchTarget == "" {
			return processIDs, err
		}
	}
	return windowsCodexProcessIDsByNameWithError()
}

func windowsStartedProcessIDsWithError(installation CodexInstallation) ([]string, error) {
	processIDs, err := windowsInstallationProcessIDsWithError(installation)
	if err == nil && len(processIDs) > 0 {
		return processIDs, nil
	}
	if installation.LaunchTarget == "" {
		return processIDs, err
	}
	return windowsCodexProcessIDsByNameWithError()
}

func runWindowsPowerShellLinesWithError(command string) ([]string, error) {
	script := `[Console]::OutputEncoding = [System.Text.Encoding]::UTF8; ` + command
	output, err := hiddenWindowsCommand("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", script).Output()
	if err != nil {
		return nil, err
	}
	lines := strings.FieldsFunc(string(output), func(r rune) bool { return r == '\r' || r == '\n' })
	for index := range lines {
		lines[index] = strings.TrimSpace(strings.TrimPrefix(lines[index], "\ufeff"))
	}
	return lines, nil
}

type windowsProcess struct {
	ID   uint32
	Name string
	Path string
}

func snapshotWindowsProcesses() ([]windowsProcess, error) {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil, err
	}
	defer windows.CloseHandle(snapshot)
	entry := windows.ProcessEntry32{Size: uint32(unsafe.Sizeof(windows.ProcessEntry32{}))}
	if err := windows.Process32First(snapshot, &entry); err != nil {
		return nil, err
	}
	var processes []windowsProcess
	for {
		processes = append(processes, windowsProcess{
			ID:   entry.ProcessID,
			Name: windows.UTF16ToString(entry.ExeFile[:]),
			Path: windowsProcessImagePath(entry.ProcessID),
		})
		err := windows.Process32Next(snapshot, &entry)
		if errors.Is(err, windows.ERROR_NO_MORE_FILES) {
			break
		}
		if err != nil {
			return nil, err
		}
	}
	return processes, nil
}

func windowsProcessImagePath(processID uint32) string {
	process, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, processID)
	if err != nil {
		return ""
	}
	defer windows.CloseHandle(process)
	buffer := make([]uint16, 32768)
	size := uint32(len(buffer))
	if err := windows.QueryFullProcessImageName(process, 0, &buffer[0], &size); err != nil {
		return ""
	}
	return windows.UTF16ToString(buffer[:size])
}

func hiddenWindowsCommand(name string, arguments ...string) *exec.Cmd {
	command := exec.Command(name, arguments...)
	command.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: windows.CREATE_NO_WINDOW,
	}
	return command
}

func isWindowsCodexAppRunning() bool {
	return len(windowsCodexProcessIDsByName()) > 0
}

func isWindowsCodexExecutable(executable string) bool {
	lower := strings.ToLower(filepath.Clean(executable))
	if isWindowsPackagedExecutable(executable) {
		return strings.Contains(lower, "openai.codex_") || strings.Contains(lower, `\codex_`)
	}
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

func isWindowsPackagedExecutable(executable string) bool {
	lower := strings.ToLower(filepath.Clean(executable))
	return strings.Contains(lower, `\windowsapps\`)
}
