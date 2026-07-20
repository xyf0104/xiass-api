//go:build darwin

package main

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

func detectCodexInstallation() CodexInstallation {
	home, _ := os.UserHomeDir()
	candidates := []string{
		"/Applications/ChatGPT.app",
		"/Applications/Codex.app",
		filepath.Join(home, "Applications", "ChatGPT.app"),
		filepath.Join(home, "Applications", "Codex.app"),
	}

	if output, err := exec.Command("mdfind", "kMDItemCFBundleIdentifier == 'com.openai.codex'").Output(); err == nil {
		scanner := bufio.NewScanner(strings.NewReader(string(output)))
		for scanner.Scan() {
			candidate := strings.TrimSpace(scanner.Text())
			if strings.HasSuffix(candidate, ".app") {
				candidates = append([]string{candidate}, candidates...)
			}
		}
	}

	seen := map[string]struct{}{}
	for _, appPath := range candidates {
		if appPath == "" {
			continue
		}
		if _, ok := seen[appPath]; ok {
			continue
		}
		seen[appPath] = struct{}{}
		if info, err := os.Stat(appPath); err != nil || !info.IsDir() {
			continue
		}
		if !isCodexBundle(appPath) {
			continue
		}
		executable := filepath.Join(appPath, "Contents", "MacOS", "ChatGPT")
		if _, err := os.Stat(executable); err != nil {
			executable = filepath.Join(appPath, "Contents", "MacOS", "Codex")
		}
		if _, err := os.Stat(executable); err != nil {
			continue
		}
		return CodexInstallation{
			AppPath:    appPath,
			Executable: executable,
			Running:    processMatches(executable),
			Found:      true,
		}
	}
	return CodexInstallation{}
}

func selectCodexInstallation() (CodexInstallation, error) {
	output, err := exec.Command("osascript", "-e", `POSIX path of (choose application with prompt "Select Codex App")`).Output()
	if err != nil {
		return CodexInstallation{}, errors.New("no Codex App was selected")
	}
	appPath := strings.TrimRight(strings.TrimSpace(string(output)), "/")
	if !isCodexBundle(appPath) {
		return CodexInstallation{}, errors.New("the selected application is not Codex App")
	}
	executable := filepath.Join(appPath, "Contents", "MacOS", "ChatGPT")
	if _, err := os.Stat(executable); err != nil {
		executable = filepath.Join(appPath, "Contents", "MacOS", "Codex")
	}
	if info, err := os.Stat(executable); err != nil || info.IsDir() {
		return CodexInstallation{}, errors.New("the selected Codex App executable does not exist")
	}
	return CodexInstallation{
		AppPath:    appPath,
		Executable: executable,
		Running:    processMatches(executable),
		Found:      true,
	}, nil
}

func restartCodex(installation CodexInstallation) error {
	if err := stopCodex(installation); err != nil {
		return err
	}
	return startCodex(installation)
}

func prepareCodexOperation() error {
	conflicts := []struct {
		bundleID    string
		executables []string
	}{
		{
			bundleID: "com.bigpizzav3.codexplusplus",
			executables: []string{
				"/Applications/Codex++.app/Contents/MacOS/CodexPlusPlus",
				filepath.Join(os.Getenv("HOME"), "Applications", "Codex++.app", "Contents", "MacOS", "CodexPlusPlus"),
			},
		},
		{
			bundleID: "com.bigpizzav3.codexplusplus.manager",
			executables: []string{
				"/Applications/Codex++ 管理工具.app/Contents/MacOS/CodexPlusPlusManager",
				filepath.Join(os.Getenv("HOME"), "Applications", "Codex++ 管理工具.app", "Contents", "MacOS", "CodexPlusPlusManager"),
			},
		},
		{
			bundleID: "com.jlcodes.cockpit-tools",
			executables: []string{
				"/Applications/Cockpit Tools.app/Contents/MacOS/cockpit-tools",
				filepath.Join(os.Getenv("HOME"), "Applications", "Cockpit Tools.app", "Contents", "MacOS", "cockpit-tools"),
			},
		},
	}

	for _, conflict := range conflicts {
		running := false
		for _, executable := range conflict.executables {
			if executable == "" {
				continue
			}
			matched, err := processStatus(executable)
			if err != nil {
				return err
			}
			if matched {
				running = true
				break
			}
		}
		if !running {
			continue
		}
		if err := exec.Command("osascript", "-e", `tell application id "`+conflict.bundleID+`" to quit`).Run(); err != nil {
			return err
		}
		deadline := time.Now().Add(10 * time.Second)
		for running && time.Now().Before(deadline) {
			time.Sleep(250 * time.Millisecond)
			running = false
			for _, executable := range conflict.executables {
				matched, err := processStatus(executable)
				if err != nil {
					return err
				}
				if matched {
					running = true
					break
				}
			}
		}
		if running {
			return errors.New("a conflicting Codex manager did not exit within 10 seconds")
		}
	}
	return nil
}

func stopCodex(installation CodexInstallation) error {
	if !installation.Found || installation.AppPath == "" {
		return errors.New("Codex App was not found")
	}
	running, err := processStatus(installation.Executable)
	if err != nil {
		return fmt.Errorf("could not verify whether Codex is running: %w", err)
	}
	if running {
		if err := exec.Command("osascript", "-e", `tell application id "com.openai.codex" to quit`).Run(); err != nil {
			return fmt.Errorf("could not ask Codex to quit: %w", err)
		}
		deadline := time.Now().Add(15 * time.Second)
		for running && time.Now().Before(deadline) {
			time.Sleep(250 * time.Millisecond)
			running, err = processStatus(installation.Executable)
			if err != nil {
				return fmt.Errorf("could not verify that Codex exited: %w", err)
			}
		}
		if running {
			return errors.New("Codex did not exit within 15 seconds; configuration is saved but the app was not force-closed")
		}
	}
	return nil
}

func startCodex(installation CodexInstallation) error {
	if !installation.Found || installation.AppPath == "" {
		return errors.New("Codex App was not found")
	}
	if err := exec.Command("open", installation.AppPath).Start(); err != nil {
		return err
	}
	deadline := time.Now().Add(15 * time.Second)
	running, statusErr := processStatus(installation.Executable)
	if statusErr != nil {
		return fmt.Errorf("could not verify that Codex started: %w", statusErr)
	}
	for !running && time.Now().Before(deadline) {
		time.Sleep(250 * time.Millisecond)
		running, statusErr = processStatus(installation.Executable)
		if statusErr != nil {
			return fmt.Errorf("could not verify that Codex started: %w", statusErr)
		}
	}
	if !running {
		return errors.New("Codex did not start within 15 seconds")
	}
	return nil
}

func openBrowser(target string) error {
	return exec.Command("open", target).Start()
}

func processMatches(executable string) bool {
	running, _ := processStatus(executable)
	return running
}

func processStatus(executable string) (bool, error) {
	if executable == "" {
		return false, errors.New("Codex executable path is empty")
	}
	output, err := exec.Command("ps", "ax", "-o", "command=").Output()
	if err != nil {
		return false, err
	}
	scanner := bufio.NewScanner(strings.NewReader(string(output)))
	for scanner.Scan() {
		command := strings.TrimSpace(scanner.Text())
		if command == executable || strings.HasPrefix(command, executable+" ") {
			return true, nil
		}
	}
	if err := scanner.Err(); err != nil {
		return false, err
	}
	return false, nil
}

func isCodexBundle(appPath string) bool {
	output, err := exec.Command("mdls", "-raw", "-name", "kMDItemCFBundleIdentifier", appPath).Output()
	if err == nil && strings.Trim(strings.TrimSpace(string(output)), `"`) == "com.openai.codex" {
		return true
	}
	infoPlist := filepath.Join(appPath, "Contents", "Info")
	output, err = exec.Command("defaults", "read", infoPlist, "CFBundleIdentifier").Output()
	return err == nil && strings.TrimSpace(string(output)) == "com.openai.codex"
}
