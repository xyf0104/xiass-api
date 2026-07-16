# XIASS Codex Helper

XIASS Codex Helper is a portable local configurator for macOS and Windows. It
binds only to a random `127.0.0.1` port and asks the user to confirm their own
XIASS API website before opening it. The selected user API key is returned
through a URL fragment, so API keys are not placed in website requests or proxy
logs. The source code has no maintainer deployment URL default.

Before applying a configuration, the helper:

1. Locates the user-level Codex `config.toml`.
2. Validates the existing TOML.
3. Creates a byte-for-byte backup with a SHA-256 manifest.
4. Preserves unrelated MCP, plugin, project, desktop, and reasoning settings.
5. Writes the XIASS provider through an atomic file replacement.
6. Reads the file back and verifies every managed value.
7. Restarts Codex only after verification succeeds.

Restore operations validate the selected backup and create another safety
backup before replacing the current configuration.

## Local verification

```bash
GOCACHE=/tmp/xiass-go-build-cache GOSUMDB=off go test ./...
```

## Build

```bash
CGO_ENABLED=0 go build -trimpath -o xiass-codex-helper .
```

Release builds are produced by `.github/workflows/release.yml` as:

- `xiass-codex-helper-macos-universal.zip`
- `xiass-codex-helper-windows-x64.exe`
