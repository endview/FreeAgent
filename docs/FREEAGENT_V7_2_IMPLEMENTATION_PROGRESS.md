# FreeAgent v7.2 Implementation Record

Date: 2026-10-25
Baseline: v7.1.0-dev.1, `27c8daf`
Status: native acceptance passed; cross-platform CI checked on the stage commit

## Implemented

- Strict bounded UTF-8 unified-diff `apply_patch`, with required per-target
  before hashes or expected absence. No fuzzy matching, external patch program,
  rename, mode or binary support. Workspace paths retain existing protection.
- Exact preview/confirmation reuses normal Ask/Workspace/Full and the existing
  staged file writer. Per-file definite successes/failures and uncertain
  publication remain distinct; execution stops on the first failed target.
- Shared ToolResult outcome records dispatch/completion, cancellation, timeout,
  truncation and elapsed-time facts. Backend receipts retain effect evidence.
  Truncated UTF-8 output cannot become a successful check.
- Command completion drains bounded output and independently observes root exit
  and process-tree cleanup. Provider Attempts retain local latency in addition
  to existing usage, response identity, stream origin and redacted failures.
- Anthropic SSE now rejects duplicate starts/stops, unclosed blocks, mismatched
  event types and deltas after closure. Chat and Responses retain their existing
  strict completion/identity/continuation contracts.

## Verification

PASS: focused patch create/edit/delete, hash drift, protected paths, partial
failure, exact Ask/Workspace approvals, CRLF/EOF and UTF-8 outcome checks.
PASS: Chat, Anthropic and Responses loopback JSON/SSE fixtures and cancellation
before dispatch, before response headers and after headers. An early fixture
deadlock and incorrect Responses fixture constructor were fixed and rerun.
PASS: ordinary full Go suite, final full native Windows race suite, vet/notices,
70 ChatUI JS tests and production UI build. Focused commands cover final output
drain and cleanup changes.
PASS: GUI exact patch preview, full hashes/line counts, approval and actual file
content. A duplicate preview request during approval was suppressed.
PASS: portable clean first-start/reopen, engine path, integrity and CLI smoke.
NOT_RUN: real Provider endpoints. No endpoint compatibility is inferred from
fixtures. UNKNOWN remains the expected result for unproven dispatched effects.

Contract: [Tool outcomes](TOOL_OUTCOMES.md). v7.3/v7.4 retain independent gates.

Stage commit `aa5259d`: Windows and Linux CI, including Linux race, passed:
https://github.com/endview/FreeAgent/actions/runs/40197417592
