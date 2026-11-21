# Proxy node traffic

Account Management -> Node Traffic (or an account's menu / proxy name) shows
upload, download and total bytes for subscription exit nodes. Windows include
today and the preceding 6 or 29 calendar days in Asia/Shanghai. The optional
price-per-GB input estimates the cost of the filtered total using decimal GB.

These are shared node totals, not per-account attribution. Two proxy records
pointing to the same subscription listener share one counter. Native SOCKS and
HTTP proxy entries that bypass the subscription engine, and direct egress,
are not counted by this feature.

The counter wraps the outbound tunnel connection after SOCKS negotiation. It
counts successful writes to the tunnel and reads from it, including reads on
requests that later fail or are canceled. API HTTP/SSE/WebSocket traffic and
browser traffic traversing that listener are included. This measures tunnel
payload (including inner TLS when used), not outer HY2/QUIC/IP framing,
retransmission or provider-specific billing multipliers. It is not an invoice.

## Persistence and operation

- The proxy agent writes `DATA_DIR/proxy-traffic.json` (`data/` by default), or
  the file specified by `XIASS_PROXY_TRAFFIC_FILE` / `-traffic-file`.
- Use a durable volume and a separate file per running proxy agent instance.
  The normal XIASS Docker working directory makes the default `/app/data/`.
- A snapshot is written every five seconds and on normal agent shutdown. A
  temporary file is synced and atomically renamed; forwarding never waits for
  disk I/O. A sudden process kill or power loss can lose the latest unsaved
  interval. Storage failures appear as a warning without stopping forwarding.
- Daily buckets older than 35 days are removed. Node identity and collection
  start time persist across restarts, subscription reorder and display renames.
- Existing history is never invented from token logs. Each node and server
  reports its actual collection start. A corrupt history file is preserved and
  collection disabled rather than silently resetting its counters.
- `GET /api/v1/admin/proxies/traffic?days=1|7|30` is admin-authenticated and
  read-only. Its agent call does not reconcile or restart listeners on failure.
- The dialog fetches once when opened, on period changes, and on manual refresh.
  Account-list requests do not fetch counters per row.

Backend, frontend and proxy-agent builds must be upgraded together. The feature
starts collecting after the new proxy agent is running; deploying only the
frontend does not enable collection. Follow the normal service drain/update
procedure to avoid terminating customer streams for this optional feature.
