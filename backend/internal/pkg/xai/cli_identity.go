package xai

import (
	"net/http"
	"os"
	"runtime"
	"strings"

	"golang.org/x/mod/semver"
)

const (
	CLIProxyHost        = "cli-chat-proxy.grok.com"
	CLIStableVersion    = "1.0.13"
	CLIVersionEnv       = "XAI_GROK_CLI_VERSION"
	CLITokenAuth        = "xai-grok-cli"
	CLIClientIdentifier = "grok-pager"
	CLIClientMode       = "interactive"
)

func ResolveCLIVersion() string {
	version := strings.TrimSpace(os.Getenv(CLIVersionEnv))
	if !IsSupportedCLIVersion(version) {
		return CLIClientVersion
	}
	return version
}

func IsSupportedCLIVersion(version string) bool {
	canonical := "v" + version
	minimum := "v" + CLIStableVersion
	return semver.IsValid(canonical) &&
		semver.Canonical(canonical) == canonical &&
		semver.Compare(canonical, minimum) >= 0
}

func CLIUserAgent(version string) string {
	if strings.TrimSpace(version) == "" {
		version = CLIClientVersion
	}
	platform, arch := runtime.GOOS, runtime.GOARCH
	if platform == "darwin" {
		platform = "macos"
	}
	switch arch {
	case "amd64":
		arch = "x86_64"
	case "arm64":
		arch = "aarch64"
	case "386":
		arch = "x86"
	}
	return "grok-pager/" + version + " grok-shell/" + version + " (" + platform + "; " + arch + ")"
}

func ApplyCLIProxyHeaders(req *http.Request) {
	if req == nil || req.URL == nil || !strings.EqualFold(strings.TrimSpace(req.URL.Hostname()), CLIProxyHost) {
		return
	}
	if req.Header == nil {
		req.Header = make(http.Header)
	}
	version := ResolveCLIVersion()
	req.Header.Set("X-XAI-Token-Auth", CLITokenAuth)
	req.Header.Set("x-grok-client-version", version)
	req.Header.Set("x-grok-client-identifier", CLIClientIdentifier)
	req.Header.Set("x-grok-client-mode", CLIClientMode)
	req.Header.Set("x-authenticateresponse", "authenticate-response")
	req.Header.Set("User-Agent", CLIUserAgent(version))
}
