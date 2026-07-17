# XIASS Codex Helper

XIASS Codex Helper is a portable local configurator for macOS and Windows. It
binds only to a random `127.0.0.1` port and asks the user to confirm their own
XIASS API website before opening it. The default site is
`https://api.xiass.com`. The selected user API key is returned
through a URL fragment, so API keys are not placed in website requests or proxy
logs.

Before applying a configuration, the helper:

1. Locates the user-level Codex `config.toml` and supports validated manual App selection when automatic discovery is unavailable.
2. Validates the existing TOML.
3. Stops Codex cleanly before changing configuration or conversation metadata.
4. Creates a byte-for-byte configuration backup with a SHA-256 manifest.
5. Discovers both legacy `~/.codex/state_5.sqlite` and current
   `~/.codex/sqlite/*` conversation databases.
6. Creates coherent SHA-256-verified SQLite snapshots that include committed WAL data, and also
   backs up session metadata, `session_index.jsonl`, and Codex desktop state
   before repairing history visibility.
7. Reuses an existing configured custom provider ID whenever possible. New
   configurations use `codex_local_access`; provider metadata in normal and
   archived rollouts plus compatible `threads.model_provider` columns is
   synchronized to the active provider so every conversation remains visible
   after switching providers.
8. Preserves unrelated MCP, plugin, project, desktop, and reasoning settings.
9. Uses atomic file replacement and SQLite transactions, then verifies database
   integrity, provider consistency, the exact thread-ID sets, and the rollout
   file set.
10. Records durable repair states, recovers interrupted operations on the next
    run, rolls back configuration/history on failure, and starts Codex only
    after every verification succeeds.
11. On Windows, Microsoft Store/WindowsApps installations are launched through
    their registered `shell:AppsFolder` target instead of executing the
    protected package binary directly. Optional SQLite files that cannot be
    confirmed as thread-provider databases are skipped; `state_*` databases
    remain strictly validated.
12. Windows process polling uses native Toolhelp APIs. Remaining PowerShell and
    task-control commands run with no-window flags, preventing repeated console
    flashes during shutdown and launch verification.

Restore operations validate the selected backup and create another safety
backup before replacing the current configuration. The local page also exposes
an immediate history-repair action. Every restart initiated by this helper runs
history verification first; later normal Codex launches retain the synchronized
provider metadata.

The repair behavior was cross-checked against the public
[CodexPlusPlus provider-sync implementation](https://github.com/BigPizzaV3/CodexPlusPlus)
and the [Codex cross-provider history issue](https://github.com/openai/codex/issues/15494).
The XIASS implementation is written
independently and adds stop-before-write, atomic rollout replacement, full
database rollback, and thread-count verification.

## Local verification

```bash
GOCACHE=/tmp/xiass-go-build-cache GOSUMDB=off go test ./...
```

## Build

```bash
CGO_ENABLED=0 go build -trimpath -o xiass-codex-helper .
```

Release builds are produced independently by
`.github/workflows/codex-helper-release.yml` as:

- `xiass-codex-helper-macos-universal.zip`
- `xiass-codex-helper-windows-x64.exe`

Both files are replaced in the `codex-helper-latest` prerelease so their public
download URLs remain stable without changing the XIASS API release version.
