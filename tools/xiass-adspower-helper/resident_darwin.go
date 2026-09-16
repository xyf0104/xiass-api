//go:build darwin

package main

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
)

const residentLaunchAgentLabel = "com.xiass.adspower-helper"

func installResidentHelper(cfg *config) error {
	if cfg == nil || cfg.path == "" {
		return fmt.Errorf("helper config is unavailable")
	}
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	executable, err = filepath.EvalSymlinks(executable)
	if err != nil {
		return err
	}
	configDir := filepath.Dir(cfg.path)
	binDir := filepath.Join(configDir, "bin")
	binPath := filepath.Join(binDir, "xiass-adspower-helper")
	logDir := filepath.Join(os.Getenv("HOME"), "Library", "Logs", "XIASS")
	launchAgentsDir := filepath.Join(os.Getenv("HOME"), "Library", "LaunchAgents")
	plistPath := filepath.Join(launchAgentsDir, residentLaunchAgentLabel+".plist")
	if err := os.MkdirAll(binDir, 0o700); err != nil {
		return err
	}
	if err := os.MkdirAll(logDir, 0o700); err != nil {
		return err
	}
	if err := os.MkdirAll(launchAgentsDir, 0o700); err != nil {
		return err
	}
	if executable != binPath {
		if err := copyResidentBinary(executable, binPath); err != nil {
			return err
		}
	}
	if err := os.Chmod(binPath, 0o700); err != nil {
		return err
	}
	logPath := filepath.Join(logDir, "adspower-helper.log")
	plist := residentLaunchAgentPlist(binPath, cfg.path, logPath)
	temporary := plistPath + ".tmp"
	if err := os.WriteFile(temporary, []byte(plist), 0o600); err != nil {
		return err
	}
	if output, err := exec.Command("/usr/bin/plutil", "-lint", temporary).CombinedOutput(); err != nil {
		_ = os.Remove(temporary)
		return fmt.Errorf("validate launch agent: %s", bytes.TrimSpace(output))
	}
	if err := os.Rename(temporary, plistPath); err != nil {
		return err
	}
	domain := fmt.Sprintf("gui/%d", os.Getuid())
	_ = exec.Command("/bin/launchctl", "bootout", domain+"/"+residentLaunchAgentLabel).Run()
	if output, err := exec.Command("/bin/launchctl", "bootstrap", domain, plistPath).CombinedOutput(); err != nil {
		return fmt.Errorf("install launch agent: %s", bytes.TrimSpace(output))
	}
	if output, err := exec.Command("/bin/launchctl", "kickstart", "-k", domain+"/"+residentLaunchAgentLabel).CombinedOutput(); err != nil {
		return fmt.Errorf("start launch agent: %s", bytes.TrimSpace(output))
	}
	return nil
}

func copyResidentBinary(source, destination string) error {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	temporary := destination + ".tmp"
	output, err := os.OpenFile(temporary, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o700)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(output, input)
	closeErr := output.Close()
	if copyErr != nil {
		_ = os.Remove(temporary)
		return copyErr
	}
	if closeErr != nil {
		_ = os.Remove(temporary)
		return closeErr
	}
	return os.Rename(temporary, destination)
}

func residentLaunchAgentPlist(binaryPath, configPath, logPath string) string {
	escape := func(value string) string {
		var buffer bytes.Buffer
		_ = xml.EscapeText(&buffer, []byte(value))
		return buffer.String()
	}
	return `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>` + residentLaunchAgentLabel + `</string>
  <key>ProgramArguments</key>
  <array>
    <string>/usr/bin/caffeinate</string>
    <string>-i</string>
    <string>` + escape(binaryPath) + `</string>
    <string>-config</string>
    <string>` + escape(configPath) + `</string>
    <string>serve</string>
  </array>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><true/>
  <key>ProcessType</key><string>Background</string>
  <key>ThrottleInterval</key><integer>5</integer>
  <key>StandardOutPath</key><string>` + escape(logPath) + `</string>
  <key>StandardErrorPath</key><string>` + escape(logPath) + `</string>
</dict>
</plist>
`
}
