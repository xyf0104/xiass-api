# IQ Benchmark Backend

All routes inherit the existing `/api/v1/admin` authentication, compliance and
audit middleware. Responses use the existing `{code, message, data}` envelope.

| Method / Path (relative to `/api/v1/admin`) | Request | `data` |
| --- | --- | --- |
| POST `/pelican-benchmarks` | `{account_ids: number[], model?: string, models?: {[account_id]: string}, test_types?: ["pelican", "candy"], prompt?: string}` OR the same options with `all: true` instead of `account_ids` | `{batch_id, tasks, skipped: [{account_id, reason}]}` (202) |
| POST `/pelican-benchmarks` | `{source_id: UUID, action: "continue" or "retry"}` | Same creation result; source must have stopped |
| POST `/accounts/:id/pelican-benchmark` | Empty body or `{model?: string, test_types?: ["pelican", "candy"], prompt?: string}` | Same as bulk creation (202) |
| GET `/pelican-benchmarks` | Query: `page`, `page_size`, `account_id`, `batch_id`, `status` | `{items, total, page, page_size}` |
| GET `/pelican-benchmarks/:id` | None | Task plus `prompt`, `text_output`, `html`, optional `preview_html` |
| POST `/pelican-benchmarks/:id/stop` | None | Task metadata |
| POST `/pelican-benchmarks/stop` | `{all: true}` OR `{account_id: number}` | `{affected}` |

Task fields: string `id`, `batch_id`, `account_name`, `model`, `upstream_model`,
`status`, `error_code`, `error_message`, `source_id`, `action`, `test_type`; numeric `account_id`, `html_bytes`; nullable integer
`duration_ms`; UTC RFC3339 `created_at`; nullable UTC RFC3339 `started_at` and
`finished_at`; `thumbnail_url` is always null in this version. Public `model`
is distinct from the actual outbound `upstream_model` (empty before send).
`answer` is a nullable integer and `passed` is a nullable boolean. Both are null
for HTML tests and for any candy job that did not complete successfully.
Historical `execution_node_id` stays private to storage and is not in DTOs.

Statuses: `queued`, `running`, `canceling`, `succeeded`, `failed`, `canceled`,
`interrupted`. A canceling job still occupies its account/test-type slot. Stopping an already
terminal job is idempotent. A queued cancellation has no start time and zero
duration. Partial bulk outcomes are reported in `skipped`, not silently lost.

Omitting `test_types` preserves the original Pelican test and its exact default
`Prompt` from `pelican.go`. Select either type or both; empty, duplicate or unknown
types are rejected. `prompt` customizes only the HTML test, up to 8000 Unicode
characters and 32000 bytes, without NUL. The resolved prompt is stored with each
task. Candy always uses its fixed question, including shape selection by touch
and a final `最终答案：N` line, without disclosing the expected answer.

Limits: up to 2000 accounts per creation (4000 jobs when both types are selected),
20 list rows by default (maximum 100), 10-minute execution timeout, and 1 MiB
maximum HTML or candy text in both Go and PostgreSQL. Model defaults and
eligibility follow the existing platform-aware account test adapters. OpenAI
defaults to `gpt-6-astra`; unsupported authentication/model combinations,
credential shadows, synthetic accounts and state-mutating Agent Identity
authentication remain excluded. No alternate model-call path is introduced.

## Isolation

No customer request interception, billing record, customer scheduler selection,
last-used write, 401/429 mutation, quota snapshot update or recovery call occurs.
The selected account's direct or configured proxy egress is preserved.
Eligibility and proxy availability are rechecked when the queued job executes.

PostgreSQL's partial unique index holds one slot per `(account_id, test_type)`
across processes. Pelican and candy jobs in one batch may execute concurrently
on the same account; another job of the same type is skipped as `account_busy`.
The conditional `FOR UPDATE SKIP LOCKED` claim admits one executor. Stop requests
are persisted globally; only the original executor, after upstream has returned
and its response body has closed, acknowledges cancellation and releases the
slot. A late success cannot overwrite cancellation. Stopping one task by ID
does not stop its sibling test; account-wide and global stops still affect both.

The dispatcher wakes on creation and once on startup, starting each claimed
account independently without a three-worker bottleneck, up to 2000 concurrent
tests per process. A transient Claim error schedules a bounded, timer-driven
retry of the durable queue; a recovered queue is drained without another
creation or explicit Wake. Once the queue is empty it performs no idle database
polling. Cancellation reads occur only while a job is running. A coalescing
wakeup prevents a create/worker-exit race from losing queued work.

## Result Handling

Lists select metadata only, excluding prompts and generated text. Detail returns
the prompt snapshot, text output and JSON-escaped HTML, `no-store`,
`nosniff` and restrictive CSP headers, never a rendered HTML document. The
server never executes, tests or renders generated HTML. It does not generate
thumbnails. The frontend fetches successful visible results and renders uniform
full-document thumbnails in an isolated sandbox without same-origin access,
network access or parent navigation. Never inject into the admin DOM or use
`v-html`.

Candy detail has empty `html` and its original answer in `text_output`. Grading
accepts an unambiguous final answer line, a standalone number (optionally with
the candy unit), or a final boxed number. Merely mentioning 21 in reasoning
cannot pass. A completed answer of 21 stores `passed: true`; any other completed
answer stores `passed: false`, with `answer: null` if it cannot be parsed.
Wrong answers remain `succeeded`; transport/incomplete-stream failures remain
`failed` with null grading, even if a partial response contains 21.

Only model-generated text is retained as bounded continuation context, including
partial output after failure or cancellation. Failed or canceled output is never
marked successful or rendered as a successful thumbnail. Continue uses the same
account/model/type, saved prompt, previous output and a final user message of
`继续`; retry sends the original prompt. Both require the prior call to finish
before another claim of that type is admitted. Candy supports retry, never HTML
continuation. Follow-up requests cannot override prompt, type, account or model.
Historical ownership uses the stored
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

Terminal HTML and candy text are retained for 72 hours after completion;
metadata, grading and prompt snapshots stay available. Startup and hourly
bounded cleanup clear expired output in batches of 500;
active jobs and sources pinned by active followups are never cleared. Cleanup
normally occurs within the next hourly pass. Closing the admin modal
destroys previews and stops frontend polling, but does not cancel server jobs.
Execution attempt IDs remain distinct internally to preserve cancellation CAS.
HTTP status and bounded, redacted structured upstream error messages are stored
separately from generated HTML. Missing upstream messages are never invented.

Apply all pending migrations, including 274, before starting this build. Existing
rows retain type `pelican` and the original default prompt. Real PostgreSQL tests use the repository's existing
Docker integration harness and isolated test schemas, never a live XIASS DB.
