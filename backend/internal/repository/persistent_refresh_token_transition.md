# Historical Legacy Refresh Transition

## Status and Scope

The server's offline migration CLI and its runtime credential-provisioning phase
have been removed. This document is a historical reference for the retained
repository transition contracts and persisted evidence, not an operator runbook
or an available server command. XIASS does not provide built-in whole-host disaster
recovery, Witness arbitration, or Redis Sentinel discovery. See
`deploy/MULTI_NODE.md` for the current shared-state/load-balancing boundary.

Existing installations must retain their selected refresh-session authority,
session data, issuance/revocation records, signing keys, deployed Redis credentials
and restricted ACLs. The application provider still reads already migrated
PostgreSQL sessions and validates their authority; it does not migrate sessions
at startup or fall back to stale Redis sessions. Keep any already integrated
runtime environment settings needed by that provider. Removing the CLI does not
authorize removing its previously deployed configuration or data.

Released migrations 238, 240 and 241 remain unchanged. Migration 240 initially
records Redis authority; 241 adds group inventory/fence evidence. Neither
migration activates PostgreSQL or contacts Redis. The implementation details
below describe the historical transition and are retained for compatibility
audits, not as instructions to start a new migration.

There are two modes of the SAME `AdoptLegacyRefreshTokens` operation. Omitting
`Group` retains the original dedicated-source gates. An explicit `Group` enables
mixed app/cache keyspaces and a bounded, inventoried original-primary/direct-
replica group. Passing isolated tests is not approval or execution of a live
installation's migration. This repository change makes no production contact.

Without `Group`, supported source shape is a dedicated, directly addressed Redis process with
only its default legacy ACL user, one nonempty database, and only the three
refresh-session namespaces. A nonempty other DB, unknown key, additional ACL
principal, module, Cluster mode, replica role, connected replica, active backlog,
nonzero replication offset or secondary replication ID rejects preflight.
The official Redis 8.4 image includes modules and still rejects in dedicated mode.
Group mode supports an exact name/version inventory of its bundled ReJSON,
search, timeseries, bf and vectorset modules; unknown/custom background-writer
modules reject. This is not blanket approval for arbitrary modules or modified
Redis binaries. Test fixtures exercise both module-free Redis 7.4 and Redis 8.4.

`ExpectedRunID` pins a process and prevents retries crossing a restart. It is
NOT a source-authority certificate. In particular, an old RDB restored into a
fresh standalone master can be indistinguishable here from a current source.
No claim is made to detect all restored snapshots or previous lost revocations.
Do not supply an untrusted backup/promoted replica as a source. Group pins must
come from the operator's preexisting original-primary identity and complete
instance inventory, not opportunistic discovery from a Sentinel/promoted endpoint.
The method verifies that inventory against every reachable process and the
original primary's live replica list; it never manufactures a trustworthy source
from an arbitrary fresh run ID. A supplied replica, mismatched replication ID,
promoted primary with a secondary replication ID, missing connected replica,
cascaded replica, wrong upstream, unreachable node or changed process rejects.
All replicas must initially be online and out of full synchronization.
After the immutable inventory exists, a pinned replica can briefly enter full
sync when a fenced connection drops or replication reconnects. Only recognized
full-sync states wait for convergence, sampled every 50 ms within the existing
five-minute operation deadline (or a shorter caller deadline). Every pass checks
the entire group: changed run/replication IDs, role, direct upstream, downstream
inventory, or unknown states still reject immediately. Success still requires
no loading/full sync and online primary-side peers; a fenced disconnected link
is allowed only under the pre-existing non-initial rule. No import, ACL change,
or complete migration is replayed by this observation loop. Replica rejection
diagnostics include the phase, normalized state and identity-match booleans;
upstream addresses are represented only by a digest prefix.

Redis cannot reveal a disconnected, omitted machine, an old copied ACL file,
an earlier acknowledged DEL already lost before inventory, or a privileged
external controller that can replace Redis binaries/configuration/volumes.
Those are explicit residual trust boundaries, not facts certified by this API.
When original-source provenance or inventory completeness cannot be established,
do not call adoption. There is no `fenced=true`/`trustReplica` escape hatch.

## Explicit API

`NewLegacyRefreshTransitionRecoverySecret() ([]byte, error)` creates 32 random
bytes. Retain them securely outside logs: the same bytes are needed after a crash
or uncertain commit. The method never stores the secret itself in PostgreSQL;
the witness stores its Redis password hash. Do not reuse an application password.

`(*PersistentRefreshTokenStore).AdoptLegacyRefreshTokens(ctx,
LegacyRefreshTransitionOptions) (*LegacyRefreshTransitionResult, error)` takes:

- `Source`: connection settings for the selected ORIGINAL primary. Its address
  and database are fixed; injected dialers are discarded, dynamic credential
  providers and caller connection hooks are rejected. An internal connection
  hook checks the pinned run ID on EVERY new physical connection, including
  reconnects, before any ACL command. It is not a Sentinel client API.
- `ExpectedRunID`: the inspected source process ID, not a boolean fencing claim.
- `RecoverySecret`: the generated recovery material, supplied explicitly.
- `Group` (optional): `*LegacyRefreshTransitionGroup`, containing the pinned
  `PrimaryReplicationID`, `PrimaryAddress` (the exact host:port configured as
  upstream on replicas), exact `PrimaryACLUsers`, exact `PrimaryModules`, and
  `Replicas` (at most eight). A group with no replicas supports a mixed standalone
  source; an incomplete connected-replica list still rejects.
- Each `LegacyRefreshTransitionReplica`: a direct `Client`, `ExpectedRunID`,
  `ReplicaAddress` (the primary's advertised replica IP:port), exact `ACLUsers`
  and `Modules`. Endpoint and advertised address are deliberately separate for
  NAT/container deployments. Node endpoints, run IDs and advertised peers must
  be unique. `LegacyRefreshTransitionModule` contains `Name` and `Version`.

The canonical group manifest stores these identity pins, addresses, selected
DBs and user/module inventories, never client passwords. Replica/user/module
ordering is normalized. Retries, including completed retries, must provide the
same manifest, primary endpoint/run ID and recovery secret. Omitting `Group`
cannot downgrade a group witness into dedicated mode.

The result contains `TransitionID`, `Imported`, `Expired`, `ActivatedAt`, and
`SnapshotSHA256`. Raw tokens, source JSON and Redis plaintext credentials are not
returned or persisted in the transition witness. Errors do not print Redis bodies.

### Opt-in Runtime Access Fence

The original `Group == nil` mode and version-1 manifest remain the dedicated
offline fence: every legacy ACL principal is reset `off`, including already
authenticated clients. Runtime continuity is a separate version-2 manifest
opt-in. It is accepted only with an explicit group inventory, `Runtime`,
`session_fence.preserve_runtime_access=true`, and the operator attestation that
login, refresh-token and revocation endpoints have been independently blocked
and drained. This flag is an admission assertion, not a claim that this
repository drained HTTP requests; no missing-auth-request queue is treated as
proof of zero downtime.

For the opt-in fence, each inventoried old principal must have a known,
password-protected, selector-free ACL state. Its password hashes are preserved,
but its permissions are replaced atomically with the single shared fixed
non-auth command/key/channel whitelist from `RefreshSessionRuntimeACL`.
That whitelist includes only the established cache, billing, concurrency,
queue, scheduler, monitoring, Team mailbox, and invalidation-channel names;
the complete range is the literal rule set returned by that builder, including
configured dashboard/batch/alert literals after protected-prefix checks. It
never includes `+@all`, `~*`, `&*`, ACL/config/flush/replication commands, or any
of `refresh_token:`, `user_refresh_tokens:`, or `token_family:`. Unknown key
names in any nonempty inventoried DB reject the transition with the key digest;
they are never silently admitted. Lua is retained only for the fixed command
set, and Redis still enforces key ACLs for declared and script-discovered keys.

The manifest records a versioned runtime policy, the builder digest, and a
before/after ACL proof for every old principal. The proof and every node's final
ACL digest are append-only and are checked on retries and by runtime restore.
Restore may create the new application/replication/backup roles only after the
same restricted state is re-proven; it never restores old session permissions.
Replacement credential hashes are required, sorted canonically, and rejected if
they match any old password or the recovery credential. The opt-in therefore
keeps existing authenticated non-auth inference work alive while session reads,
writes and all old admin operations remain denied.

## Mechanism and Operational Impact

1. Take an exclusive PG advisory lock. Verify the marker is Redis and the target
   refresh tables/generations are pristine. Existing PG issuance, consumption,
   or revocation history rejects adoption; this is not a merge/backfill API.
   The SQL pool must permit a control connection plus a transaction connection;
   a one-connection pool is rejected. An uncertain advisory unlock discards the
   control connection rather than returning a potentially locked session to the pool.
2. Check the selected source boundary and bounded metadata/index preflight.
   Require a configured ACL file and successfully `ACL SAVE` the ORIGINAL rules
   before creating or disabling any user. A mere nonempty `aclfile` path is not
   sufficient. Default installations without a writable persistent ACL file fail
   before losing legacy permissions.
   In group mode, ALL nodes must successfully save their original rules before
   ANY legacy principal is disabled. Initial topology and exact ACL/module
   inventories are verified before committing the immutable group manifest.
3. In the dedicated mode, create a new exclusive principal using the recovery
   secret on every node;
   save those ACLs before disabling any old user. In group mode, fence replicas
   first, then the original primary. Each node uses one `MULTI/EXEC` containing
   `ACL SETUSER <old-user> reset off` for every inventoried old user.
   This removes command permissions, passwords, keys, channels and selectors,
   so ALREADY AUTHENTICATED connections lose permissions too. It is not a lease,
   process-local boolean, or temporary `CLIENT PAUSE`.
   The exclusive principal must have exactly the recovery password hash, no
   second password, no `nopass`, and no selectors; a legacy administrator cannot
   smuggle another password into a fence that this verifier accepts.
4. `ACL SAVE` each fence, verify every old user has no permissions/passwords/
   selectors, and append each node's ACL digest and fence time to 241's
   `refresh_token_transition_nodes`. A group cannot be marked `fenced`/`completed`
   without every manifest node's proof. The manifest and node proofs cannot be
   rewritten/deleted/truncated. An uncertain node-proof commit is retryable:
   reconnect with the recovery secret, repeat/reverify the fence, never restore
   old permissions. After fencing, replication links may go down because the
   old replication credential is disabled. All nodes must remain reachable with
   their original run IDs/roles/upstreams; any still-connected peer must belong
   to the exact inventory. The tool never promotes or reconfigures a replica.
5. Re-read the bounded session snapshot ONLY from the original primary. Never
   union or fill misses from replicas, including a replica containing a hash
   absent/revoked on the primary. Recheck the whole group's real identity, ACL
   digests and module inventory inside the final PG transaction and again just
   before activation.
6. In one `synchronous_commit=on` PG transaction, lock the authority marker and
   global generation, recheck pristine state, insert hashed token metadata,
   immutable families and spent admission tickets, complete the audit witness,
   and change the marker to PostgreSQL. No success is returned before COMMIT.

**In the original dedicated mode, all old Redis access is disabled across ALL
databases on EVERY inventoried node, including previously authenticated
application and failover-controller connections.** Mixed mode keeps non-auth
keys, values, types and absolute expiry unchanged; it does not keep old
credentials usable. In the explicit runtime-access mode, old passwords remain
valid only for the fixed non-auth whitelist described above; session namespaces,
ACL/config/flush/replication operations, and unknown namespaces remain denied.
Redis ACLs do not safely express "all keys except these auth prefixes", so the
runtime mode never preserves old `~*` writers. Any business outside the listed
range needs separately provisioned Redis credentials and an audited
application/config rollout before its Redis work can resume. The repository
adoption method does not change `masteruser`, `masterauth`, `REPLICAOF`,
`redis.conf`/startup configuration, services, or traffic. The server command's
optional runtime phase provisions new app/replication users and persists
`masteruser`/`masterauth` on every replicated node after adoption; it does not
change roles, rewrite installation `.env`, restart apps or provision failover
controllers/backup identities. Do not grant old binaries the new transition
credential or restore their refresh permissions. External admission must block
and drain login/refresh/revocation endpoints before the runtime mode; the
method does not promise transparent continuity for already-consumed auth
rotations or for missing HTTP requests.

Once a fence has been applied, errors NEVER automatically restore old permissions.
Before activation the marker remains Redis. During a partial group fence, nodes
not yet reached can still accept old credentials; this is why external OFFLINE
traffic admission/drain is a prerequisite, not a zero-downtime promise. Nodes
already fenced remain denied. Runtime fails closed after the primary fence until
a valid retry finishes or an operator performs separately authorized recovery. A node restart
during an unfinished operation is rejected, even if its saved ACL remains fenced.
Keep the recovery secret: losing it after disabling the bootstrap user requires
external recovery, not an unsafe alternative-source or credential fallback.
Restart persistence is certified only for successfully saved ACL fences under
the same startup ACL-file configuration. A failed/uncertain `ACL SAVE` is not a
durability acknowledgment: keep maintenance admission closed and do not restart
that node to bypass the failure. No missing node proof can activate PostgreSQL.
Host administrators able to replace the saved file or startup arguments are
outside the Redis ACL fence's authority.

## Metadata and Retry Rules

- Every SCAN loop has a 4,096-page hard cap (including empty MATCH pages), in
  addition to a five-minute operation deadline. Dedicated mode keeps its 30,000
  namespace-key cap. Mixed source inspection scans names only across at most 16
  nonempty/selected DBs, with a shared 250,000 returned-key and 4,096-page budget,
  a per-DB `DBSIZE` admission cap, and a 1,024-byte key-name cap. Auth prefixes in
  a nonselected DB reject instead of silently dropping that DB's sessions.
- Token snapshots are bounded to 10,000 hashes and 4 KiB JSON per token. Unknown fields,
  raw-token fields, duplicate/null/missing required fields, malformed hashes,
  missing expiry, missing user/family membership and family conflicts reject.
- Redis GET/expiry/membership are checked in scripts after the fence. The original
  absolute `PEXPIRETIME` is used, not a TTL restarted when PostgreSQL commits.
- Preserve `ExpiresAt` and explicit `FamilyExpiresAt` at microsecond precision,
  truncating rather than extending. Effective validity is the earliest of both,
  Redis's original absolute expiry, and `CreatedAt + 7 days`.
  An explicit family deadline beyond `CreatedAt + 7 days` rejects instead of
  allowing a later rotation to escape the seven-day bound.
- For old payloads lacking a family deadline, the ONLY derivation is the earlier
  of existing `ExpiresAt` and `CreatedAt + 7 days`, matching the bounded legacy
  fallback. Missing creation/token deadlines never use migration-time `now`.
  Inconsistent deadlines within a family reject; metadata is not guessed.
- Expired metadata still present in Redis is counted and not activated. Ordinary
  Redis-expired keys already absent at the snapshot are not counted as imported.
- A failed final transaction rolls back all token/family/ticket inserts and the
  marker. Retry reads the same fenced source without extending deadlines.
- A completed retry returns the permanent PG witness without Redis access or
  INSERTs. It cannot recreate hashes consumed/revoked after adoption, and does
  not change user/global generations or family tombstones.
- Marker triggers prohibit PostgreSQL-to-Redis rollback, activation-time rewrite,
  deletion and TRUNCATE. Activation also requires a completed transition witness
  in that transaction. A DB superuser can defeat triggers/drop tables: controlling
  database administration, backup restores and migration privilege is a separate
  operational trust boundary, not something this repository can cryptographically
  enforce.

## Acceptance Still Required

No GET-miss fallback, Redis-only-session auto-import, automatic switch, or
PostgreSQL async-failover durability guarantee exists. Parent owns provider and
configuration selection, mixed-version operation, provisioning non-auth Redis
credentials, infrastructure/host-level HA controls, readiness, traffic drain, rollback policy and
deployment. Seven-day deadlines are preserved/capped, never restarted; this
cannot reconstruct previously deleted history or acknowledged revocations already
lost by Redis before the trusted source was selected.

The dedicated production-provider test remains unchanged: it introduces a stale
replica AFTER adoption and injects pause/disconnect/lost-DEL/promote. The new
`TestPersistentRefreshTransitionGroupMixedReplicasProductionRecovery` starts with
a Redis 8.4 original primary and TWO existing replicas, mixed strings/hashes/JSON
and another nonempty DB, issues actual AuthService sessions via the Redis
provider, adopts that replicated source, and uses the actual PG selector. It
then revokes in unchanged PG, kills the test primary and promotes a preexisting
stale replica: the old token is rejected, the control rotates without extending
the family deadline, and replica-only stale metadata is never imported. These
tests alone did not close the bare-legacy Redis regression. After the real offline
command and two application HTTP migration flows were verified, the original
`TestRedisPromotionMustNotResurrectAcknowledgedRefreshRevocation` fixture was
connected to this same production migration lifecycle. It retains the real
pause/disconnect/acknowledged-revoke/primary-loss/stale-replica-promotion fault,
proves stale metadata remains in Redis, and retains both acceptance conditions:
the revoked session is denied and the valid control still refreshes. Raw Redis
session authority without migration is still unsafe for automatic promotion;
Sentinel discovery is no longer an application configuration option.
