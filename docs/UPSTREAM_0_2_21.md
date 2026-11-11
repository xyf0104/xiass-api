# XIASS API v1.2.81 Integration Notes

This release adds compatible changes from the read-only sub2api v0.2.19 to
v0.2.21 range onto XIASS main at 849a2d535. It does not replace XIASS with an
upstream checkout. XIASS migrations 272 and 273 follow the existing migration
sequence without rewriting released migrations.

## Included Changes

- Codex SSE metadata handling, later-turn WebSocket retry protection,
  same-account web-search connection activation, per-turn TTFT, stable tool-loop
  session seeds, Desktop page-context compatibility, UTF-8 validation, native
  image error status, and OAuth non-stream terminal response handling.
- Claude Fable/Mythos 5.2 model and effort support, native and compatibility
  thinking round trips, sealed function-call fields, compatibility error
  policies, and bounded Bedrock event decoding.
- Antigravity contiguous tool responses before media, Pro Argon quota isolation,
  actual quota group/window metadata, bounded Gemini SSE reading, response JSON
  schema compatibility, and separation of thinking from non-stream text.
- OpenAI 30-minute/12-hour cache-write token accounting and optional price-card
  fields across database, billing, account cost, API responses and UI. Existing
  explicit cache-write prices retain precedence. Free-input Fast calculations
  and pricing-download hash recovery are corrected.
- Key lists render before aggregate usage arrives. Account refresh retains
  existing rows. A concurrent covering index accelerates Key cost queries.
- Existing pending XIASS changes are retained: user-specific preferred account
  order and the wider, responsive announcement presentation.
- The server build toolchain is raised to Go 1.27.4 to address the reachable
  net/http issue GO-2026-6859 identified by the release vulnerability scan.

## Carbon

The public models are `gemini-4-carbon-high`, `gemini-4-carbon-medium`, and
`gemini-4-carbon-low`. Each tier is sent under its exact upstream ID. Disabled
catalog markers remain private capability evidence. Explicit disabled evidence
rejects the model; an identity-bound exact-model catalog allows it; otherwise
only an Ultra account can use the default Carbon capability.

Inference preserves XIASS prod-first automatic negotiation. Only the precise
bare RESOURCE_EXHAUSTED response permits daily fallback. Read-only catalog
discovery uses the paid-plan daily catalog without changing inference preference.

Carbon fallback rates use the operator-supplied schedule in USD per million
tokens. Before December 1, 2026 00:00 Asia/Shanghai, input/output/cached input are
2/10/0.10. From that boundary input/output are 4/20. Standard cached input is
assumed to retain the same 95% discount (0.20); that rate was not separately
specified in the supplied screenshot. Output includes thinking tokens.
Rates are resolved at request time without requiring a process restart;
explicit group/channel/account pricing and existing multipliers retain their
defined precedence. These are operator-supplied business rates, not an
independently verified vendor price announcement.

## XIASS Preservation

The customization inventory was checked against current XIASS rather than
upstream defaults. It includes hard account priority, model/user preferred order,
nonmigratable response chains, opt-in fingerprint convergence, multi-IP egress
snapshots, composite groups, USD account cost/RMB customer debit, OAuth expiry
and quota windows, and existing public model aliases.

OAuth reauthorization CAS, SMS confirmation surfaces, Team/browser runtime,
AdsPower ownership, subscriptions and proxy packaging, token conversion, iOS,
helper state preservation, branding, and installation/update data paths remain
XIASS-owned. Existing refresh-token atomic consumption and slot-release fixes
were already present and were not replaced.

Upstream-wide request-body memory/transport rewrites, payment changes, UI
branding/defaults, and unrelated operational behavior were not imported as part
of this focused protocol integration.

## Verification

Required release gates include the release contract, frontend lint/typecheck and
full tests/build, backend unit and real PostgreSQL/Redis integration tests,
helper tests, and the established release workflow. Live acceptance checks cover
public model discovery, Responses/Chat/Messages/Gemini calls, streaming,
continuation, billing reconciliation, and existing runtime health.
