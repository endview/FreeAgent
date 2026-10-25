# FreeAgent v7.0.2 - v7.3 Development Plan

Date: 2026-10-23
Baseline: v7.0.1-dev.1, commit `daa82a070fc5570526657151cc6974456abfe5ae`
Status: v7.0.2 accepted on 2026-10-25; reviewed corrections incorporated;
v7.1/v7.2/v7.3 native gates accepted on 2026-10-25. v7.2 and v7.3
Windows/Linux CI passed. See implementation records.

## 1. Purpose

This plan completes the next four releases after v7.0:

1. v7.0.2: close the two known product gaps without changing the core model;
2. v7.1: persist bounded conversation compaction while keeping original history;
3. v7.2: standardize tool execution and Provider diagnostics, and add `apply_patch`;
4. v7.3: add owner-bound background command sessions.

The sequence follows the existing v7.0 decisions. Each release has its own
acceptance gate. A later release cannot use an unfinished earlier release as
its reliability foundation.

## 2. Current baseline

v7.0.1 already provides:

- one SQLite-backed task core shared by desktop Chat and CLI;
- owner discovery, attach secrets, generation checks and typed control;
- model-proposed control changes through Preview / Confirm;
- Ask, Workspace and Full execution profiles;
- Windows command execution through the current sandbox launcher and an
  explicit host escape hatch;
- OpenAI-compatible Chat, Anthropic and Responses adapters;
- DeepSeek Responses search metadata and continuation;
- current-only `Harness-v5` data and `freeagent-harness-v7` store format;
- live DeepSeek acceptance for Chat, tools, Responses and search;
- deterministic in-memory task-local recap controlled by
  `task_summary_bytes`.

The current known limits that this plan addresses are:

- interactive `freeagent chat` does not attach to an already running owner;
- a command launched through a Scoop shim may not reach its real executable
  inside the sandbox;
- compaction is not durable across Runs or owner restarts;
- timeout, cancellation and truncation behavior is implemented in several
  tool paths instead of one shared contract;
- `apply_patch` is not in the built-in catalog;
- long-running commands cannot be detached, polled or stopped through the
  shared GUI / CLI core.

## 3. Non-negotiable design rules

### 3.1 One core

GUI, CLI, and AI conversation continue to use the same `TaskService`, owner,
SQLite store, control service, ToolRunner and Provider adapters. No surface
gets a private implementation of execution, approval, persistence or recovery.

### 3.2 Existing records first

The implementation reuses `conversations`, `messages`, `runs`, `attempts`,
`meta`, existing receipts and the current control journal. It does not add
separate Task, Step, Checkpoint or Evidence entities.

Conversation summaries may be stored as bounded `meta` records because they
are context selection state, not execution records. A background command is
represented by its existing tool Attempt and receipt, not by a new session
table.

### 3.3 Explicit authority

Persistent settings and model-proposed persistent changes continue to use the
existing Preview / Confirm contract. File changes and commands continue to use
their existing permission profile and tool approval rules. A context summary
never grants file, command, network or Provider authority.

### 3.4 UNKNOWN remains conservative

Cancellation after dispatch, owner loss, process cleanup that cannot be
verified, incomplete Provider responses and uncertain background termination
remain UNKNOWN. UNKNOWN is inspectable and never replayed automatically.

### 3.5 No hidden platform growth

These releases do not add automatic RAG, implicit personality learning,
background knowledge governance, multi-tenancy, organization permissions,
plugin marketplace, generic lifecycle hooks, a scheduler, installer, updater,
or a second executor.

## 4. Release dependency map

```text
v7.0.2 attach and small fixes
        |
        v7.1 durable context compaction
        |
        v7.2 unified tool pipeline + apply_patch + Provider fixtures
        |
        v7.3 background command sessions
```

v7.0.2 is required first because v7.1 operations must be usable from both
surfaces. v7.1 precedes v7.2 so Provider and tool continuations receive one
well-defined context contract. v7.3 consumes the v7.2 normalized tool outcome
and cancellation contract.

## 5. v7.0.2 - small fixes

### 5.1 Goal

Make GUI and CLI interchangeable during normal local use, and resolve the
known sandbox executable-path problem. Do not change the store format or add a
new product subsystem.

### 5.2 Interactive Chat attach

Current behavior: a one-shot attached CLI command can use the owner, but the
interactive `chat` command still creates or uses its own local runtime.

Implementation:

- make `chat` perform owner discovery before opening a new core;
- when an owner is active, use its existing authenticated API without opening
  SQLite or starting a second runtime;
- select a conversation explicitly with `--resume` or `/resume`; `active_chat`
  describes execution, not the conversation selected in another window;
- preserve conversation, selected model, settings and pending approval records;
  GUI and CLI drafts remain surface-local;
- retain `/history`, `/sessions`, `/new`, `/model`, `/collaboration`, `/settings`,
  `/memory`, `/status`, `/continue`, `/more`, `/abandon`, `/file` and multiline
  input; menu actions reach existing owner TaskService and control methods;
- with no owner, the CLI serves the existing authenticated API in its own
  process. Its own inputs use the same server start/events/cancel paths;
- allow simultaneous viewers. The owner serializes execution requests and
  rejects conflicting submissions; there is no surface lease or heartbeat;
- detaching an attached client does not stop the owner or replay input. Ctrl+C
  targets the exact accepted request; idle Ctrl+C exits the client;
- exiting the CLI that owns the runtime drains and closes that owner. GUI
  attachment does not promote another process to owner; an owner-independent
  daemon or automatic promotion is outside this release;
- expired event cursors resnapshot saved state, and generation changes require
  reconnection; neither condition resubmits the request;
- do not create an interactive-session database object.

The handoff behavior is therefore:

| Situation | Result |
|---|---|
| No owner | CLI starts the owner as it does today |
| Owner active, Chat idle | CLI attaches as another user surface |
| Owner active, Run waiting for approval | CLI attaches and shows the same approval |
| Owner active, another request is running | Inspection works; conflicting execution reports busy |
| Owner generation changes during attach | attach fails and the CLI asks the user to retry |

### 5.3 Scoop shim handling

The sandbox must not silently fall back to host execution. The preflight path
will:

- detect a Windows shim or script entry point before the sandbox launch;
- resolve only known, absolute targets using a bounded host-side resolver;
- support native Scoop `.exe` + `.shim` metadata, including literal Windows
  argv prefixes; reject unknown fields, chains, scripts and missing metadata;
- resolve `current` junctions to a concrete application version directory;
  give that installation read-only access for sibling resources. Never grant
  the whole Scoop, apps or shims tree;
- check the resolved executable and complete argv against rules and allowlist,
  bind both to approval and recheck the shim mapping before dispatch;
- accept the resolved executable only if it is inside a granted root or an
  approved system install path;
- retain the original command and resolved target in a redacted receipt;
- reject unresolved, ambiguous, outside-root or script-chain targets with a
  structured `executable_target_unavailable` result;
- never execute `scoop`, a shell, or a shim inside the AppContainer merely to
  discover a target;
- never turn an unresolved shim into a host command.

The resolver is limited to the supported Windows shim formats. It is not a
general shell interpreter or PATH search service.

### 5.4 Documentation and packaging

- document the GUI / CLI attach behavior and busy case;
- document the supported shim resolution and its fail-closed cases;
- update the version, portable manifest and release notes to v7.0.2;
- keep the existing current-only data boundary and package layout.

### 5.5 v7.0.2 tests

- CLI attaches to an existing owner started by the GUI test harness;
- GUI/CLI/GUI handoff preserves conversation, selected model and pending
  confirmation;
- active Run remains inspectable after the interactive surface exits;
- owner generation change is rejected without a second writer;
- simultaneous viewers work; overlapping execution gets a deterministic busy response;
- Scoop-style shim resolution succeeds only for an allowed target;
- unresolved and outside-root shims are blocked without host fallback;
- existing Windows launcher, sandbox, attach, control and portable tests pass;
- `go test ./...`, `go test -race ./...`, `go vet ./...`, Chat UI tests and
  package smoke tests pass.

### 5.6 v7.0.2 exit criteria

PASS requires the same conversation to continue through GUI and CLI, no second
writer to open the SQLite store, no shim to escape the selected profile, and
zero regressions in current acceptance tests. No v7.1 code starts before this
gate is recorded.

## 6. v7.1 - durable context compaction

### 6.1 Goal

Turn the existing in-memory task recap into a bounded, restart-safe context
selection mechanism. Original messages remain intact and inspectable.

### 6.2 Durable summary record

Store one bounded current summary per conversation in `meta`, keyed by a
versioned conversation summary key. The JSON value contains only:

- conversation ID and summary revision;
- covered-through message ID, covered count and ordered prefix digest;
- summary text, body hash and byte limit;
- update time and the existing Run ID that wrote it. Owner generation is
  checked by the attach envelope and events; do not duplicate it as a new
  core identity.

The summary is an untrusted context excerpt. It is never execution evidence,
never a memory entry, and never a replacement for original history.

The existing source messages and attempts are never deleted or rewritten.
Because `meta` already supports atomic key/value updates, this remains a
current-format store change without a migration layer. If implementation
requires a schema change, the store version is bumped and older stores are
rejected as required by the current product boundary.

### 6.3 Compaction algorithm

1. Read the bounded current summary and messages after its covered boundary.
2. Verify the ordered source prefix with a streaming digest on first reuse by
   an owner and during explicit Refresh. Cache that verification only within
   the current owner; appended messages do not require rereading the prefix.
3. If the summary is valid, use it as the oldest context prefix.
4. Add the newest complete user/assistant/tool groups in order.
5. Never split a tool call from its tool result or an assistant response from
   its continuation boundary.
6. Keep the system message, active Run prompt, selected memory references and
   current tool definitions within their own limits.
7. For automatic compaction, advance the covered boundary only if the input
   still does not fit. For manual Compact, advance it even below that limit,
   retaining at least the newest complete group and the active Run's prompt
   and incomplete groups in full. Cover only complete older groups and write
   a replacement summary atomically.
8. If the newest group alone cannot fit, stop with `context_limit` and preserve
   the original history.

Manual Compact returns `unchanged` when there are no additional eligible older
groups or the replacement would not reduce the assembled context. Repeated
Compact calls over the same source range do not create needless revisions.
Refresh rebuilds the already-covered range; Compact may extend that range.

The existing 120-character excerpts are not a semantic summary. Use a bounded,
tool-less auxiliary model request with `Purpose: compaction`, persisted and
charged as an existing model Attempt. Preserve goals, constraints, decisions,
completed work, pending work, important files and next actions. Source content
is untrusted data; it cannot grant authority or overwrite the system policy.

Read and summarize the whole eligible conversation prefix before context
selection would otherwise drop older Runs. Long prefixes use bounded chunks
of complete message groups and the preceding summary; every model call is
recorded and charged. Chunking is forward progress, never an implicit retry.
Respect the existing model window, steps, output and active-time limits. An
oversized indivisible group returns `context_limit` without discarding history.
Commit the replacement summary only after all required chunks succeed. Model
failure, cancellation or UNKNOWN retains the previous summary and original
messages, and remains inspectable in Attempts.

### 6.4 Manual and automatic operations

Manual compaction is a first-class product operation. It is available even
when the current context still fits, so the user can reduce an old
conversation before starting a long task. Compact and Refresh require a
configured summarizing model; Clear and inspection do not call a Provider.
None of these operations delete or rewrite original history.

There are three explicit operations:

- `context compact`: manually create or replace the summary for a selected
  conversation, even when automatic compaction is not required;
- `context refresh`: manually rebuild the summary from current source messages
  and hashes after reviewing the covered range;
- `context clear`: manually remove only the summary selection; history remains.

The same operations are available through GUI controls, CLI commands and the
AI control catalog. The GUI offers "Compress context" in the conversation menu.
`/compact` is handled locally in GUI and interactive CLI before any model
dispatch; `freeagent context compact --session <id>` provides the one-shot CLI
entry. These entries use the configured model and credential for Compact and
Refresh, or a fixture adapter in tests. They show the
covered range, before/after input bytes and whether anything changed. CLI also
supports JSON output. Inspection exposes source hashes and summary revision.

A natural-language request such as "compress this conversation" may be handled
by the model through the same control catalog. All model-proposed Compact,
Refresh and Clear operations use Preview / Confirm through the existing GUI
card or CLI prompt. A direct user click or explicit CLI/slash command is
already a user decision and needs no additional model-proposal confirmation.

Implementation clarification: expose one `context_manage` core tool and reuse
the ordinary saved tool Attempt, exact Preview/Confirm and GUI/CLI approval.
Its mutation preparation requires user confirmation before permission-mode
auto-approval, including Full. Keep model I/O outside the control mutex and
SQLite transaction; this is not a new control journal or proposal lifecycle.

Manual operations acquire the existing execution lock and never change an
in-flight model request. Running model/tool calls, unrelated pending approvals
and UNKNOWN block the operation with an unchanged summary. A stopped or limited
Run may be compacted at a complete message-group boundary without continuing
the Run or resetting its consumed budget. This allows recovery from a context
limit with automatic compaction disabled.

A manual operation with no unfinished Run may use an ordinary maintenance Run
whose model Attempts have `Purpose: compaction`; it must not fabricate a user
message or a model answer in ordinary conversation history. A stopped/limited
Run retains its snapshot and accumulated consumption when used for compaction.

A Run waiting for confirmation of its own context operation is the narrow
exception to the pending-approval block: confirmation applies only to completed
earlier groups, preserves that Run's prompt and pending call, and revalidates
the source range/revision under the same execution gate. Other pending tool
calls still block it. It must not deadlock on its own confirmation or create a
second Run. Do not perform model I/O while holding a control mutex or SQLite
transaction that dispatch itself needs.

If the requested input bound cannot be satisfied without splitting the newest
group, return `context_limit` and leave the current summary unchanged.

Automatic compaction may occur only at the point where the
next dispatch would otherwise fail. It emits a visible `context_compacted`
event and changes no authority or user setting. Automatic compaction is a
fallback for dispatch continuity; it does not replace the manual Compact
action.

Setting `task_summary_bytes` to zero disables automatic creation/extension, but
retains manual Compact and Refresh and the use of a previously saved valid
summary. Manual Compact accepts a per-operation `--summary-bytes` value within
the existing positive range of 1-16384 bytes. Otherwise it uses the configured
positive bound, or 4096 bytes when automatic compaction is disabled. Refresh
defaults to the saved summary's bound. These choices never change the global
setting or enable automatic compaction. If automatic compaction is disabled,
a stale summary requires an explicit Refresh; it is not regenerated silently.
A disabled or failed automatic compaction returns a clear context-limit result
instead of silently dropping messages.

### 6.5 Recovery and integrity

- write the new summary with one SQLite transaction;
- retain the previous valid summary until the new value commits;
- on crash, reuse a verified previous summary; recomputation follows the
  automatic policy or an explicit manual operation;
- mark a hash mismatch as stale and reject that summary; rebuild only when
  automatic compaction is enabled or the user explicitly requests Refresh;
- bound summary bytes and JSON size; source count and digest have constant
  storage size rather than an ever-growing array of IDs and hashes;
- include summary revision and source hashes in inspect output;
- prevent a summary from being sent to a Provider if its source verification
  fails;
- keep existing Run snapshots and authority unchanged when a summary changes.

### 6.6 v7.1 UI / CLI behavior

GUI shows whether the conversation has no summary, a current summary or a stale
summary. It provides explicit Compact, Refresh and Clear actions and shows the
covered message range and byte bound. Controls follow the boundary rules above:
in-flight requests and unrelated approvals disable them with a reason; the
matching context confirmation remains usable. It never displays hidden secrets.

CLI provides JSON and human-readable output for the same state. Attached CLI
commands operate through the owner. The AI tool returns the operation result,
source range and whether the operation was automatic or explicit.

### 6.7 v7.1 tests

- manual Compact works before the context limit is reached;
- GUI Compact, local `/compact` and one-shot CLI reach the same bounded model
  dispatch; Clear and inspection work without a Provider key;
- automatic compaction disabled still permits manual Compact/Refresh with a
  finite byte bound, without changing settings;
- a valid manually saved summary is reused after restart with automatic
  compaction still disabled;
- repeated Compact or a short conversation returns `unchanged` without losing
  the newest group or creating unnecessary revisions;
- manual Compact, Refresh and Clear preserve every original message;
- in-flight model/tool calls, unrelated approvals and UNKNOWN reject manual
  operations without changing the current summary;
- a stopped/limited Run can be manually compacted at a complete boundary and
  then explicitly continued without resetting budget;
- a model-proposed Compact requires confirmation and can apply through its
  own waiting Run without self-blocking or changing its pending call;
- source ID/hash verification detects edits, corruption and stale summaries;
- tool-call groups are never split;
- summary plus recent groups obey message and byte limits;
- newest oversized group returns `context_limit` without data loss;
- transaction failure leaves the previous summary intact;
- owner restart reloads a valid summary;
- invalid summary is rejected; rebuilding from source history respects the
  automatic policy or an explicit Refresh;
- automatic compaction is visible and cannot change Run authority;
- GUI, CLI and AI operations reach the same owner and store;
- concurrent compaction and Run dispatch are serialized correctly;
- long-conversation memory and latency stay bounded;
- semantic continuity retains a goal or constraint placed after the first
  120 characters and across chunk boundaries, rather than just excerpts;
- model-summary failure/UNKNOWN retains the previous valid summary and
  records actual model usage without hidden retry;
- backup/restore preserves summaries and source hashes.

### 6.8 v7.1 exit criteria

PASS requires manual compaction below the context limit and with automatic
compaction disabled, restart-safe context selection, intact original history, no
summary-derived execution claims, deterministic stale detection and successful
GUI/CLI/AI operation coverage. A real Provider is not required for the core
compaction gate; fixture dispatch is sufficient.

## 7. v7.2 - unified tool pipeline and Provider layer

### 7.1 Goal

Make every tool follow one observable execution contract, add the built-in
`apply_patch` tool, and improve deterministic and live Provider acceptance.

This release does not create a generic plugin lifecycle engine. It strengthens
the existing ToolRunner and Attempt boundary.

### 7.2 Shared tool lifecycle

All built-in, MCP, HTTP, extension and command tools use the same conceptual
stages:

1. validate structured arguments;
2. check the Run's frozen permission and capability snapshot;
3. prepare a bounded request and exact approval preview when required;
4. persist the dispatch marker before an external side effect;
5. execute with the shared context and deadline;
6. capture bounded output and redacted receipt data;
7. persist the final or UNKNOWN outcome atomically;
8. emit one normalized event for GUI, CLI and AI conversation.

The stages are helpers and contracts inside the current runner. They are not a
new user-configurable lifecycle or a new persistent object.

### 7.3 Timeout, cancellation and truncation rules

Use the earliest applicable caller, Run or tool deadline. Classify the outcome
from retained dispatch and completion evidence; cancellation does not override
a definitively observed completion just because its signal arrives later.

Rules:

- cancellation before dispatch creates no external call;
- cancellation after the dispatch marker retains UNKNOWN unless a definitive
  response proves the outcome;
- timeout after dispatch is UNKNOWN when completion cannot be proven;
- process cleanup is reported separately from command exit status;
- UTF-8 output is truncated at a bounded byte limit with a `truncated` flag;
- a truncated result cannot be represented as a complete successful check;
- Provider SSE uses protocol-specific bounded framing and completion validation;
  tool output uses bounded capture. Neither may execute a tool from a partial
  or invalid Provider response;
- parallel tool calls are allowed only when the frozen Provider capability and
  ToolRunner policy allow them; otherwise calls are serialized;
- no automatic retry is introduced by a timeout, truncation or UNKNOWN result.

Normalize these facts in the existing Attempt response and ToolReceipt. Do not
add a metrics service or an evidence table.

### 7.4 `apply_patch` contract

Add `apply_patch` to the built-in catalog as a structured file-change tool.

Request fields:

- patch text;
- workspace-relative target paths only;
- required expected before hashes for edits/deletes, and expected absence for
  creates; freeze every computed before/after hash in the preview;
- bounded file count, patch bytes, hunk count and resulting file size.

Validation:

- reject absolute paths, traversal, links, protected `.freeagent`/`.git`, data
  directories and paths outside the frozen file scope using existing file
  protection rules; first release supports UTF-8 create/edit/delete only,
  with no rename, mode change or binary patch;
- parse the patch structurally before any write;
- compute the exact before and after hash and a bounded diff preview;
- reject a stale expected hash;
- preserve new, modified and deleted file intent explicitly;
- do not invoke a shell, git, Provider or external patch utility.

Approval and apply:

- patches are ordinary file tools. Ask requires the existing tool confirmation;
  Workspace and Full retain their normal file permission behavior. A patch is
  not a product-settings control proposal and needs no control confirmation code;
- GUI shows the exact paths, hashes, line counts and preview;
- CLI prints the same bounded preview and accepts the existing confirmation
  prompt;
- a model cannot approve its own patch; user decisions use the existing
  Run-bound tool confirmation contract where the permission mode requires it;
- confirmation is bound to the exact patch, Run, attempt and source hashes;
- before writing, revalidate all hashes and permission revisions;
- prevalidate every target, then apply through existing per-file staged writes;
  revalidate each target immediately before its write and stop on first failure;
- record every file result and any partial outcome in the existing receipt;
- preserve definite partial successes and definite failures individually. There
  is no existing whole-workspace rollback transaction and none is promised;
  UNKNOWN is reserved for an uncertain effect or publication outcome;
- never claim success from a preview alone.

### 7.5 Provider fixtures and diagnostics

Add deterministic loopback fixtures for:

- OpenAI-compatible Chat JSON and SSE;
- Anthropic JSON and SSE;
- OpenAI-compatible Responses JSON and SSE;
- function/tool calls and exact continuation;
- DeepSeek-style `web_search_call` query and page-open metadata;
- usage present, absent and malformed;
- length, refusal, filtering, invalid request, authentication, permission,
  quota, rate limit, redirect, timeout and disconnect;
- malformed, duplicate, out-of-order and incomplete SSE items;
- cancellation after headers and before headers;
- bounded redaction of credential-like fields.

Persist local usage, latency, response identity, stream origin and normalized
error category inside the current Attempt response. Diagnostics must remain
bounded and must not expose API keys.

Live Provider acceptance is reported separately for each Provider. Fixture
passing does not certify a real Provider. Without an explicitly supplied key,
the live result is `NOT_RUN`, never PASS.

### 7.6 v7.2 tests

- every ToolRunner implementation follows the lifecycle matrix;
- cancellation, timeout and truncation follow the same evidence-based contract
  across file, command, HTTP, MCP and extension tools, while retaining the
  actual effect/cleanup facts specific to each backend;
- `apply_patch` exact preview, approve, decline, stale hash, protected path,
  partial write and publication uncertainty;
- GUI / CLI / AI patch approval reaches the same Attempt;
- all Provider JSON/SSE fixtures and malformed-stream protections;
- Responses function-call continuation and search metadata regression tests;
- usage and diagnostics are bounded and redacted;
- race tests cover concurrent tool completion, cancellation and owner close;
- package tests verify the catalog and generated Chat UI assets.

### 7.7 v7.2 exit criteria

PASS requires one documented outcome matrix, no automatic replay of uncertain
calls, exact patch approval and complete fixture coverage for the three active
protocol families. Live Provider results are listed as PASS, NOT_RUN or
UNKNOWN per endpoint and never inferred from fixtures.

## 8. v7.3 - background command sessions

### 8.1 Goal

Allow an explicitly detached command to keep running under the same owner,
while GUI, CLI and AI can poll output or request stop. Keep ownership local
and bounded. Do not add a scheduler.

### 8.2 Persistence model

A background command starts as an existing command Tool Attempt with a
background flag. Its successful startup response and tool message are immutable.
The Attempt ID is the command identifier; a bounded `meta` projection keyed by
that original Attempt tracks subsequent process state and output. It is not
another persistent task or session entity.

The startup receipt and evolving process projection expose:

- command identity, working directory and frozen execution profile;
- owner generation and Run ID;
- launch time, last observation time and deadline;
- lifecycle state: running, completed, failed, stopped, timed_out or unknown;
- exit code only when proven;
- bounded stdout/stderr tail and truncation state;
- bounded file observations and cleanup detail;
- host escape-hatch marker when Full mode was used.

No process object, scheduler record or background-session table is persisted.
Operating-system handles remain in memory while the owner is alive.

### 8.3 Commands and AI tools

Extend the existing command tool surface with explicit operations:

- `command_run` with a bounded `background` option;
- `command_poll` by Attempt ID;
- `command_stop` by Attempt ID;
- existing inspect and control paths expose the same receipt.

GUI shows a compact command activity row with output tail, state and Stop.
CLI provides start, poll and stop with human-readable and JSON forms. AI can
request all three through the catalog. Starting uses existing command approval
and permission rules. Stopping an owned process remains immediately available
when command tools are subsequently disabled. Persistent settings still use
the existing confirmation path.

### 8.4 Process ownership and recovery

Windows commands use the existing Job-before-resume ordering. Linux uses the
existing process-group cleanup. The background process inherits the frozen Run
profile and cannot gain roots or network access later.

- foreground Run completion does not imply background command completion;
- poll is explicit and bounded; there is no periodic scheduler;
- stop sends the supported terminate sequence, waits a bounded interval, then
  kills only when the platform contract permits it;
- if termination or final state cannot be proven, receipt is UNKNOWN;
- owner clean shutdown terminates owned background processes and records the
  proven result or UNKNOWN;
- after crash or restart, nonterminal process projections become UNKNOWN.
  Do not rewrite the finished startup Attempt/tool message or reopen its
  completed parent Run; doing so could violate the unique active-Run index;
- projections are never reattached or replayed automatically. Windows Job
  cleanup is tested separately; Linux process groups cannot prove cleanup
  after owner crash. Neither backend proves that no side effects occurred;
- a new command may be started only after the user inspects the old UNKNOWN
  record and explicitly chooses a new request.

The process uses an owner-lifetime context, not the startup tool call's context
or the completed parent Run's context. Ordinary background commands have their
own configured timeout, at most the existing 600-second command ceiling, plus
owner shutdown as a stop condition. They do not continue charging active time
to an already completed Run. Starting a background command from a collaboration
child is prohibited in this version.

Allow only one active command per owner across foreground and background.
Shared AppContainer identities and temporary protections make overlapping
commands unsafe until v7.4's authorization compatibility work is accepted.
Owner shutdown and handoff must account for background processes even when no
foreground Run is active.

Output is bounded in memory and in the persisted process projection. The
startup Attempt response remains unchanged. Polling does not retain unbounded
logs in SQLite.

### 8.5 v7.3 tests

- start, poll, output-tail truncation and normal completion;
- non-zero exit and timeout classification;
- explicit stop, forced cleanup and uncertain cleanup;
- GUI and CLI attach to the same Attempt and see the same state;
- owner close, controlled restart and crash recovery produce conservative
  UNKNOWN results;
- process descendants are cleaned up on supported platforms;
- sandbox roots, blocked paths, network policy and Full host marker remain
  frozen for the entire command;
- concurrent poll/stop is idempotent and never sends duplicate termination;
- completed startup Attempt, tool message and parent Run remain unchanged as
  the process finishes or recovery marks its projection UNKNOWN;
- foreground/background overlap is rejected across conversations;
- a detached command survives startup-call cancellation but stops with the
  owner or its independent finite timeout; no collaboration child can detach;
- no automatic retry after UNKNOWN;
- Windows native and Linux process-group tests, plus race tests.

### 8.6 v7.3 exit criteria

PASS requires bounded detached execution, shared GUI/CLI/AI inspection, proven
stop/completion states, conservative restart behavior and no scheduler or
second executor.

## 9. Cross-release acceptance gate

Every release runs the checks appropriate to the changed surface:

```text
go test ./...
go test -race ./...
go vet ./...
pnpm test                         # internal/chatui
pnpm build                        # internal/chatui
go test ./internal/winlaunch -v  # Windows native launcher
scripts/test-portable.ps1        # packaged behavior
update-notices.ps1 -Check
```

Additional checks are required for each release:

| Release | Required focused evidence |
|---|---|
| v7.0.2 | GUI/CLI attach handoff, shim resolution and no host fallback |
| v7.1 | restart-safe summaries, source hash validation and context limits |
| v7.2 | tool outcome matrix, `apply_patch` approval and Provider fixtures |
| v7.3 | background lifecycle, process cleanup and restart UNKNOWN |

The packaged portable build is tested from a clean temporary data directory.
Packaging must not modify tracked source or user data; an existing dirty
implementation is recorded in the package manifest. Live Provider tests are
reported separately and require an explicitly selected endpoint and credential.

## 10. Performance targets

These are engineering targets, not claims until measured:

- v7.0.2 attach discovery and handshake: under 250 ms on a warm local owner;
- v7.1 warm summary load: under 50 ms for a normal conversation and bounded by
  the summary byte limit. Cold-owner prefix verification and explicit Refresh
  are measured separately against source size; they do not scan other chats;
- v7.2 patch preview: bounded by patch size and target count, with no repeated
  full-workspace scan;
- v7.2 SSE parsing: one bounded pass with no unbounded event buffer;
- v7.3 polling: no busy loop, bounded output copy and one SQLite write per
  meaningful state change;
- no release may add an unbounded goroutine, log, SQLite row stream or model
  call to make a control operation appear responsive.

## 11. Main risks and controls

| Risk | Control |
|---|---|
| CLI attach races with GUI | owner generation, exact request binding, one owner execution gate |
| Summary contains stale or misleading context | ordered prefix digest, body hash, visible stale state, original history retained |
| Compaction changes authority | summary only changes model input; Run snapshot remains frozen |
| Patch applies to changed files | exact before hashes and revalidation immediately before write |
| Partial patch leaves uncertain state | per-file staging and receipts; preserve definite partial effects, use UNKNOWN for uncertain effects |
| Provider stream is incomplete | bounded parser, no tool dispatch before validated completion |
| Background process survives owner crash | Job/process group cleanup and conservative UNKNOWN on restart |
| Full mode changes files outside policy | preserve explicit host marker and exclude it from sandbox security evidence |
| New features become a platform | reuse existing records and reject scheduler/lifecycle abstractions |

## 12. Explicit non-goals for v7.0.2-v7.3

- no old-platform compatibility or migration layer;
- no automatic RAG or background indexer;
- no implicit personality or preference learning;
- no organization, tenant or permission administration layer;
- no plugin marketplace or generic module lifecycle platform;
- no background scheduler or distributed worker pool;
- no automatic retry or semantic replay of UNKNOWN operations;
- no claim that Full/host execution is a Windows security boundary;
- no real Provider compatibility claim from fixture tests alone;
- no installer, signing or auto-update work in these four releases.

## 13. Delivery order

For each version, implementation follows the same short loop:

1. add or update the contract and focused tests;
2. implement the smallest core change;
3. connect CLI, GUI and AI catalog paths;
4. run focused tests and investigate failures;
5. run the full acceptance matrix;
6. build and inspect the portable package;
7. record PASS, NOT_RUN, UNKNOWN and blocked items;
8. stop at the version gate before beginning the next version.

The first implementation task is v7.0.2 interactive Chat attach, followed by
the supported shim resolver. No v7.1 work is required to ship v7.0.2.

## 14. Follow-up: v7.4 long-running services

After the reviewed v7.3 background command contract is implemented and accepted,
continue with the [v7.4 long-running services plan](FREEAGENT_V7_4_LONG_RUNNING_SERVICES_PLAN_2026-10-25.md).
It adds a separate finite service duration, explicit stop/extend behavior and
concurrent service/test execution. Reviewed changes to this plan remain
prerequisites; this link does not mark them as implemented or accepted.
