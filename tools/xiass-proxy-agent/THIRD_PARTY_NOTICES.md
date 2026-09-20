# Third-Party Notices

This component is distributed under GPL-3.0-only.

## luweiming1/ccodex-sleep-state

Vendored from commit `89294a1c723c7f061fa60afa42710e17a40ec6a6` under
GPL-3.0-only. Exact original copies of `internal/proxyroute/parse.go`,
`internal/proxyroute/route.go`, and `internal/settings/settings.go` are retained
under `third_party/ccodex-sleep-state/upstream-original/`. The build copies add
canonical node/source attribution metadata and an explicit per-subscription
TLS opt-in. This optional setting defaults to false, permits only supplied
node certificate-verification exceptions, and retains subscription-download
TLS verification and forbidden local-file/routing validation. Original parsing,
loading, filtering, stable identity and adapter construction remain in use.
These XIASS adaptations were updated on 2026-09-20. See
`UPSTREAM_SOURCE_MANIFEST.json` for the original and adapted source hashes.

The upstream GPL text is retained at
`third_party/ccodex-sleep-state/LICENSE`; the component-level GPL text is in
`LICENSE`.

## github.com/metacubex/mihomo v1.19.31

Used by the upstream code for proxy URI conversion and outbound protocol
adapters. License: GPL-3.0-only.

## gopkg.in/yaml.v3 v3.0.1

Used by the upstream parser for Clash/Mihomo YAML. License: MIT.

The upstream module's complete dependency versions are preserved in
`third_party/ccodex-sleep-state/go.mod` and `go.sum`. The root module resolves
that local module through a Go `replace` directive.

Distribution of a binary containing this code must satisfy GPL-3.0 source,
license, notice, and modification-marking obligations. Deployment as a
separate local process preserves a clear operational boundary, but does not
remove those distribution obligations.
