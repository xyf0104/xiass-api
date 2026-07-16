//go:build darwin

package main

import (
	"bufio"
	"errors"
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

func restartCodex(installation CodexInstallation) error {
	if !installation.Found || installation.AppPath == "" {
		return errors.New("Codex App was not found")
	}
	if installation.Running || processMatches(installation.Executable) {
		_ = exec.Command("osascript", "-e", `tell application id "com.openai.codex" to quit`).Run()
		deadline := time.Now().Add(15 * time.Second)
		for processMatches(installation.Executable) && time.Now().Before(deadline) {
			time.Sleep(250 * time.Millisecond)
		}
		if processMatches(installation.Executable) {
			return errors.New("Codex did not exit within 15 seconds; configuration is saved but the app was not force-closed")
		}
	}
	if err := exec.Command("open", installation.AppPath).Start(); err != nil {
		return err
	}
	deadline := time.Now().Add(15 * time.Second)
	for !processMatches(installation.Executable) && time.Now().Before(deadline) {
		time.Sleep(250 * time.Millisecond)
	}
	if !processMatches(installation.Executable) {
		return errors.New("Codex did not start within 15 seconds")
	}
	return nil
}

func openBrowser(target string) error {
	return exec.Command("open", target).Start()
}

func processMatches(executable string) bool {
	if executable == "" {
		return false
	}
	output, err := exec.Command("ps", "ax", "-o", "command=").Output()
	if err != nil {
		return false
	}
	scanner := bufio.NewScanner(strings.NewReader(string(output)))
	for scanner.Scan() {
		command := strings.TrimSpace(scanner.Text())
		if command == executable || strings.HasPrefix(command, executable+" ") {
			return true
		}
	}
	return false
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
