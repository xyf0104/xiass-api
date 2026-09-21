# Smart Model Rotation

## Setup

Open the model-priority section in the load-balancing settings. Enable model
priority, select the requested model and its preferred accounts, and arrange
their order. Enable the smart-rotation toggle, choose a skip interval, and save.

Smart rotation is **off by default**, including after an upgrade. It applies only
to configured, matching OpenAI-compatible model-priority rules. The default skip
interval is 30 minutes; valid values are 1 through 1440 minutes. Disabling smart
rotation restores ordinary scheduling while retaining configured account order.

## Behavior

For a rule with accounts A, B and C:

1. A request goes through the ordinary permissions, group membership, model
   support, concurrency, quota and account-order checks.
2. If A explicitly declares a different response model, A is temporarily avoided
   for the same authenticated user, API key, group and scheduled model.
3. The next eligible request tries B. A mismatch from B makes the following
   request try C. A matching response leaves the working route available.
4. Other callers using A are unaffected. Different requested models and API keys
   have independent observations, including when A is an upstream API-key pool.
5. Once preferred accounts are unavailable, ordinary eligible accounts remain
   usable. If every candidate has a mismatch observation, normal scheduling is
   used as a final availability fallback, without reviving accounts excluded by
   transport failures, permissions or quota checks.
6. Expired observations allow a subsequent real request to test an account again.
   There are no synthetic probes or extra billable background model requests.

## Detection And Limits

- Detection uses the upstream-declared response model already captured from raw
  JSON, SSE or WebSocket messages. It does not use generated prose, ticket length
  or the requested/sent model as evidence of a successful match.
- Missing response-model information is unknown: it neither marks a new failure
  nor clears an existing failure. A conflicting declaration cannot establish a
  successful match. A declared mismatch still counts as a mismatch.
- Known spelling aliases and dated model snapshots are normalized conservatively.
  Distinct model families are not collapsed by broad substring matching.
- Explicit administrator model mappings remain intentional: comparison uses the
  model actually sent upstream, while rotation state and rule selection remain
  scoped to the scheduling model. A configured vendor alias is not a mismatch
  merely because it differs from the public model spelling.
- This checks model-label consistency, not internal model intelligence. An
  upstream that conceals or falsifies its model label cannot be independently
  verified by this feature. All-accounts fallback cannot guarantee a match.
- A response that has already been forwarded is not replayed, rewritten or billed
  twice. Rotation affects subsequent requests; already-running requests continue.
- Ordinary session affinity yields to a caller's mismatch observation. A fixed,
  nonmigratable `previous_response_id` chain retains its account to avoid breaking
  context or tool results.
- A WebSocket connection remains bound to one upstream account. A subsequent
  stateless turn on a mismatching connection is rejected before forwarding with
  a reconnect/retry signal. Fixed continuation turns keep their connection.

## State And Operations

Observations are saved before asynchronous usage recording, so rotation does not
wait for the usage-history queue. Shared Redis state keeps multiple application
instances consistent. Records are caller-scoped, expire automatically and contain
no API credentials. Late results from older requests cannot overwrite newer
observations within the retained observation window. A bounded local fallback is
available if Redis is temporarily unavailable.

Failed metadata writes remain pending locally, including successful-match clears.
They are retried with version checks when Redis recovers. Shared-state work is
bounded to 50 milliseconds per selection/observation so it cannot indefinitely
hold up forwarding during a cache outage.

The existing usage history retains the requested, sent and upstream-declared
models. Mismatches also emit `openai.smart_model_rotation_mismatch` operational
events. No account-wide scheduling switch, ticket, business egress, billing rule,
account quota or account credential is changed by rotation.

Settings extend the existing model-priority JSON. No database migration or manual
data conversion is required. Old settings without the new fields remain valid.
