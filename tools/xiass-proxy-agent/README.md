# XIASS Proxy Agent

`xiass-proxy-agent` is a local control service that turns validated proxy
subscription nodes into fixed loopback SOCKS5 listeners for XIASS API. The
service is intentionally foreground-only: XIASS owns process startup,
supervision, database persistence, refresh scheduling, and shutdown.

The parser, subscription downloader, local-file reader, filters, protocol
validation, node deduplication, stable identity, and outbound adapters come
from the vendored `luweiming1/ccodex-sleep-state` source at commit
`89294a1c723c7f061fa60afa42710e17a40ec6a6`. Exact retained source and hashes
are recorded in `UPSTREAM_SOURCE_MANIFEST.json`. XIASS stages inline input,
exposes the authenticated control API, and adapts upstream transports to
loopback SOCKS5 CONNECT listeners. The documented upstream adaptation also
adds explicit per-source consent for insecure node TLS, defaulting to off.

Supported parsed protocols are AnyTLS, SS, SSR, VMess, VLESS, Trojan,
Hysteria, Hysteria2, TUIC, HTTP, and SOCKS5. Tests validate parsing and adapter
construction; they do not claim live interoperability with every protocol or
provider.

## Build And Test

From this directory:

```bash
go test ./...
go test ./... -race
go build -trimpath -o ./bin/xiass-proxy-agent ./cmd/xiass-proxy-agent
```

The retained upstream tests are a nested module and are run separately:

```bash
cd third_party/ccodex-sleep-state
go test ./...
```

Run the binary in the foreground with an ephemeral backend-provided token and
an explicit loopback control address:

```bash
XIASS_PROXY_AGENT_TOKEN_FILE=/path/to/token-file \
  ./bin/xiass-proxy-agent -listen 127.0.0.1:0
```

The process does not daemonize, schedule subscription refreshes, or write
application state.

## Control API

Every endpoint requires either `Authorization: Bearer <token>` or
`X-XIASS-Proxy-Agent-Token: <token>`. The control listener accepts only a
literal loopback address.

```text
GET    /health
POST   /v1/preview/parse
GET    /v1/routes
POST   /v1/routes
DELETE /v1/routes/<id>
POST   /v1/subscriptions/preview
POST   /v1/subscriptions/sync
```

Subscription preview accepts up to 16 sources. Each source has `id`, optional
`name`, either `url` or inline `input`, optional `user_agent`,
`include_protocols`, and `exclude_keywords`. A `url` may contain multiple
newline-separated URLs. Inline input is staged as a private regular file and
passed to the upstream local-file loader.

`allow_insecure_tls` defaults to false. An administrator may explicitly enable
it for one source after the web confirmation dialog. It permits that source's
nodes to retain their supplied `skip-cert-verify` settings; it does not disable
verification for other nodes or for HTTPS subscription downloads. Local-file,
routing override and other validation restrictions remain enforced. The option
is persisted in the encrypted source configuration and portable snapshot so
restarting the application cannot silently change it. This permits node
impersonation/man-in-the-middle attacks and should only be used for trusted
providers when the risk is understood.

Preview returns redacted flat node metadata and an opaque snapshot containing
canonical node configurations and first-source attribution. The snapshot is
sensitive: it is intended only for the authenticated loopback service channel
and encrypted backend storage, and must never be returned to a browser.

Sync accepts:

```json
{
  "snapshot": "opaque snapshot",
  "selected_node_ids": ["route-id"],
  "listen_addresses": {"route-id": "127.0.0.1:39001"},
  "prune": true
}
```

Every snapshot node is rebuilt through upstream `Build` before reconciliation;
this constructs adapters but does not dial. Duplicate selected IDs are ignored.
`prune` defaults to `true`. With `false`, sync only ensures selected routes and
keeps all other listeners. With `true`, routes outside the selected set are
closed. An empty snapshot and empty selected list can therefore clear all
routes. Fixed addresses must be literal loopback IPs with ports 1-65535 and
must refer to nodes in the snapshot.

Sync operations are serialized. New listeners are fully staged before the
active set changes. Any validation, build, or bind failure closes only newly
created resources and leaves the previous listeners intact. `Close` shuts down
listeners, accepted connections, upstream transports, and protocol adapters.

Route and node responses expose only `id`, source attribution, sanitized name,
protocol, loopback `socks5`, and `stable_id`. They never expose node endpoints,
usernames, passwords, tokens, UUIDs, or canonical configurations. SOCKS5
listeners are unauthenticated and loopback-only; the returned fixed port is a
local process capability managed by XIASS.

The agent does not execute subscription rules, enable TUN, configure DNS,
install system routes, bind public listeners, modify backend/frontend/deploy
files, or publish artifacts.
