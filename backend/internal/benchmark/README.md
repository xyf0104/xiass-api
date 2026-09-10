# Pelican Benchmark Backend

All routes inherit the existing `/api/v1/admin` authentication, compliance and
audit middleware. Responses use the existing `{code, message, data}` envelope.

| Method / Path (relative to `/api/v1/admin`) | Request | `data` |
| --- | --- | --- |
| POST `/pelican-benchmarks` | `{account_ids: number[], model?: string}` OR `{all: true, model?: string}` | `{batch_id, tasks, skipped: [{account_id, reason}]}` (202) |
| POST `/pelican-benchmarks` | `{source_id: UUID, action: "continue" or "retry"}` | Same creation result; source must have stopped |
| POST `/accounts/:id/pelican-benchmark` | Empty body or `{model?: string}` | Same as bulk creation (202) |
| GET `/pelican-benchmarks` | Query: `page`, `page_size`, `account_id`, `batch_id`, `status` | `{items, total, page, page_size}` |
| GET `/pelican-benchmarks/:id` | None | Task plus `html` |
| POST `/pelican-benchmarks/:id/stop` | None | Task metadata |
| POST `/pelican-benchmarks/stop` | `{all: true}` OR `{account_id: number}` | `{affected}` |

Task fields: string `id`, `batch_id`, `account_name`, `model`, `upstream_model`,
`status`, `error_code`, `execution_node_id`, `source_id`, `action`; numeric `account_id`, `html_bytes`; nullable integer
`duration_ms`; UTC RFC3339 `created_at`; nullable UTC RFC3339 `started_at` and
`finished_at`; `thumbnail_url` is always null in this version. Public `model`
is distinct from the actual outbound `upstream_model` (empty before send).

Statuses: `queued`, `running`, `canceling`, `succeeded`, `failed`, `canceled`,
`interrupted`. A canceling job still occupies its account. Stopping an already
terminal job is idempotent. A queued cancellation has no start time and zero
duration. Partial bulk outcomes are reported in `skipped`, not silently lost.

Defaults and limits: `gpt-6-astra`, immutable `Prompt` from `pelican.go`, up to
2000 accounts per creation, 20 list rows by default (maximum 100), 10-minute
execution timeout, exactly 1 MiB maximum HTML in Go and PostgreSQL. Unknown
OAuth plans, Free plans, setup tokens, other platforms, credential shadows,
synthetic test accounts and state-mutating Agent Identity authentication are
excluded. Normal OAuth and API-key upstream accounts use the existing admin
test request construction, proxy/TLS transport and parsing.

## Isolation

No customer request interception, billing record, customer scheduler selection,
last-used write, 401/429 mutation, quota snapshot update or recovery call occurs.
Paired-node jobs require the account owner's configured fixed proxy and healthy
owner; no emergency local-egress replacement is allowed. Eligibility and proxy
availability are rechecked when the queued job actually executes.

PostgreSQL's partial unique index holds one slot per account across processes.
The conditional `FOR UPDATE SKIP LOCKED` claim admits one executor. Stop requests
are persisted globally; only the original executor, after upstream has returned
and its response body has closed, acknowledges cancellation and releases the
slot. A late success cannot overwrite cancellation.

The dispatcher wakes on creation and once on startup, starting each claimed
account independently without a three-worker bottleneck, up to 2000 concurrent
tests per process. A transient Claim error schedules a bounded, timer-driven
retry of the durable queue; a recovered queue is drained without another
creation or explicit Wake. Once the queue is empty it performs no idle database
polling. Cancellation reads occur only while a job is running. A coalescing
wakeup prevents a create/worker-exit race from losing queued work.

## Result Handling

Lists select metadata only. Detail returns JSON-escaped HTML, `no-store`,
`nosniff` and restrictive CSP headers, never a rendered HTML document. The
server never executes, tests or renders generated HTML. It does not generate
thumbnails. The frontend fetches successful visible results and renders uniform
full-document thumbnails in an isolated sandbox without same-origin access,
network access or parent navigation. Never inject into the admin DOM or use
`v-html`.

Only model-generated text is retained as bounded continuation context, including
partial output after failure or cancellation. Failed or canceled output is never
marked successful or rendered as a successful thumbnail. Continue uses the same
account/model, original prompt, previous output and a final user message of
`继续`; retry sends the original prompt. Both require the prior call to finish
before another account claim is admitted. Historical ownership uses the stored
node ID; migrated records retain an empty snapshot rather than a guessed owner.

## Recovery Boundary

Graceful shutdown cancels active calls and waits for return before finalizing
them. After upstream returns, a transient Finish error is retried while this
process remains alive with 1, 2, 4, 8, 16 and then 30-second delays; this is
not an idle queue poll. If shutdown interrupts that retry, the manager makes
one fresh-context Finish attempt bounded by five seconds and then stops. A
finalization write that still fails remains fail-closed and keeps its account
guard. Queued jobs survive restart and are drained once at startup.

Running jobs left by a hard process crash remain fail-closed and keep their
account guard. There is deliberately no lease expiry that unlocks a possibly
live upstream request. Fully automatic recovery of those claims still requires
authoritative confirmation that the old executor has exited and fenced its
upstream calls; database connection loss or node heartbeat expiry alone is not
such confirmation. This is an explicit hard-crash boundary, not a claim that
crash recovery is solved.

Terminal HTML is retained for 72 hours after completion; metadata stays available.
Startup and hourly bounded cleanup clear expired HTML in batches of 500;
active jobs and sources pinned by active followups are never cleared. Cleanup
normally occurs within the next hourly pass. Closing the admin modal
destroys previews and stops frontend polling, but does not cancel server jobs.
The current-results UI offers Continue only after cancellation and Retry only
after failure/interruption; running and successful jobs have neither action.

Apply migrations 242 through 244 before starting this build. Real PostgreSQL tests use the repository's existing
Docker integration harness and isolated test schemas, never a live XIASS DB.
