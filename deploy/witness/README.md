# XIASS Witness

XIASS Witness is the small independent arbiter used during temporary two-node
takeover. It stores only the current holder, lease expiry, and monotonic
generation in a local SQLite file. It never receives customer requests,
accounts, API keys, balances, usage logs, PostgreSQL data, or Redis data.

This optional service arbitrates shared administrative takeover only. It does
not promote or fence PostgreSQL/Redis, replicate data, or switch public DNS.
Customer API routing and `/readyz` do not depend on witness availability.
If the only shared database host fails, this service cannot keep requests working.
An existing etcd/Patroni deployment does not need a second arbiter for database
failover; retain its existing authority instead of deploying this as a substitute.

The Witness image is a separate, opt-in release artifact. It is not included in
the standard XIASS API image, installer, compose deployments, updater flow, or
the canonical `ghcr.io/xyf0104/xiass-api` image. The release workflow publishes
it only when a maintainer explicitly enables `publish_witness` for a manual
workflow run; ordinary tag releases leave it unpublished.

Witness is not PostgreSQL high availability, PostgreSQL replication or
promotion, Redis replication or Sentinel, DNS/Cloudflare failover, network/API
traffic failover, or fencing for old application/database writers. It does not
make either node's normal API invocation depend on Witness being reachable.

1. Point a dedicated HTTPS hostname at the witness machine.
2. Copy this directory to the machine and create a `.env` containing one random
   `XIASS_WITNESS_TOKEN` of at least 32 characters.
3. Run `docker compose up -d`.
4. Put the example Caddy site into the host Caddy configuration and replace the
   hostname. Keep port `8091` bound to loopback.
5. Configure the same HTTPS URL and token on both XIASS nodes. Pairing copies
   the source values to a newly joined node automatically.

Back up only the named volume `xiass_witness_data`. Restoring an older copy
while either XIASS node is active is unsafe because the fencing generation must
never move backwards.
