# FreeAgent Extension Protocol

The [lightweight Core principles](../docs/EXTENSION_DEVELOPMENT_PRINCIPLES.md)
govern interface design and ownership. This specification describes the actual
wire contract. State operations use an extension-owned bounded payload and an
explicit scope declaration, without persona fields in the host envelope.

State capability revision 2 adds explicit operations and declared single-schema
storage; existing non-state
extensions keep their methods and execution semantics. See
[the implemented state contract](../docs/EXTENSION_STATE_CONTRACT_V2.md).
`model_settings` explicitly lists scalar settings that may be proposed through
the Core control boundary. Environment/credential mappings remain user-only.
`identity` uses adopted conversation/message IDs and verified actors; it is
never model supplied. `state.apply` receives the original ControlOperation ID
and digest, and commits its local result with state in one PG transaction.
An uncertain response is reconciled with `state.result`, never blindly replayed.
Optional packages are limited to 256 regular files / 32 MiB; no symlinks or
implicit dependency installer. See the development guide for actual commands.

This directory describes `freeagent-ext/1`, the local extension protocol
introduced in FreeAgent v6.0. An extension is one local process started over JSON-RPC 2.0
NDJSON. It is not a package manager, a remote service, or a second execution
engine.

For current authoring commands, known SDK limitations, and capabilities not yet
implemented, see the [development guide](../docs/EXTENSION_DEVELOPMENT_GUIDE.md).

## Transport

One JSON object is exchanged per line on stdin and stdout. Stderr is reserved
for diagnostics and is captured by the Harness in a bounded, redacted log.

```json
{"jsonrpc":"2.0","id":"1","method":"initialize","params":{}}
{"jsonrpc":"2.0","id":"1","result":{}}
```

The protocol is bidirectional. The Harness may call an extension, and an
extension may call a Host API method with the invocation token received in the
current hook or workflow. Most older v1 inputs ignore unknown fields. The
`task.delegate` plan, delivery Host field names/candidate, and delivery review
result are checked strictly; use the corresponding method schemas.

## Lifecycle

`initialize` is sent before the first hook or entrypoint:

```json
{
  "protocol": "freeagent-ext/1",
  "host_version": "v7.0.1-dev.1",
  "workspace": "C:\\work",
  "data_dir": "C:\\data\\extensions\\com.example.demo\\data",
  "settings": {}
}
```

`host_version` is the release version of the host, or `dev` for a source build.
Use it for diagnostics only; compatibility is decided by `protocol`.

The extension must return its own identity and the capabilities it actually
registered:

```json
{
  "id": "com.example.demo",
  "version": "0.1.0",
  "provides": ["context"]
}
```

`provides` is a subset of `context`, `workflow`, `tools`, `state`, and
`delivery`. State and delivery each require their manifest opt-in. The
identity must match `extension.json`; `ext check` checks the registered
capabilities, events and workflows against the manifest.

`shutdown` asks the extension to release resources. `$/cancel` is a
notification with an `invocation` value; it has no response. Cancellation does
not promise to undo an already completed side effect.

## Manifest

The only required extension metadata file is `extension.json` in the extension
root. User extensions live below `<data-dir>/extensions/<id>`. Workspace
extensions live below `<workspace>/.freeagent/extensions/<id>` and require
explicit workspace trust.

The manifest schema is in `schema/extension.schema.json`. The important
fields are:

- `run.command`, `run.args`, and `run.cwd`: a direct local process launch.
- `needs`: bounded input selection such as `message`, `history:4`,
  `workspace`, and `tool_results`.
- `context`: hook phases, purposes, maximum returned characters, and allowed
  placements.
- `events`: `turn.finished`, `operation.check`, and, with `delivery: true`,
  `delivery.check`.
- `workflows`: user and agent-callable workflow declarations.
- `tools`: whether the extension exposes MCP-shaped tools.
- `delivery`: explicit opt-in to the scoped reply/delivery Host methods below;
  it does not authorize proactive messages by itself.
- `env`: names the extension needs. Values are mapped by the user from the
  process environment or a Key profile; the extension never receives the Key
  profile itself.

The Harness owns its Host API budgets, timeouts, permissions, cancellation,
data paths, process cleanup, and execution records. Extensions should keep
mutable local files below the supplied `data_dir`. The process runs as trusted
local code: input selection and Host API checks do not sandbox its direct OS
access. The default extension launcher does not create an AppContainer or
Bubblewrap sandbox.

## Hook: context.prepare

The Harness sends a bounded input:

```json
{
  "invocation": "token-123",
  "phase": "turn",
  "purpose": "chat",
  "step": 0,
  "message": {"role": "user", "content": "hello"},
  "history": [],
  "workspace": "C:\\work",
  "tool_results": [],
  "settings": {}
}
```

The extension returns:

```json
{
  "blocks": [
    {
      "id": "hint",
      "text": "Use the weekly report format.",
      "placement": "system",
      "label": "Format",
      "priority": 50
    }
  ],
  "activity": {}
}
```

The Harness validates block ids, UTF-8, placement, character limits, and
ordering. An extension can only append context; it cannot remove the core
execution invariant or rewrite message history. A failed optional extension
is recorded and skipped. A failed required extension stops the turn.

`turn.finished` always receives the settled `status` and `output` (bounded
to 64 KiB), whether or not `needs` lists them; `needs` only selects the other
bounded input. It may return an `activity` object for display. Activity is
informational only and is never execution evidence.

### 4.3 `operation.check`

Before dispatching a side-effecting `tool_call`, the Harness may consult every
enabled extension that declares this event:

```json
{
  "invocation": "token-123",
  "kind": "tool_call",
  "target": "command_run",
  "preview": "the exact bounded operation preview",
  "parameters": {},
  "items": []
}
```

The extension returns:

```json
{"decision":"allow","reason":"..."}
```

`decision` is `allow`, `ask`, or `deny`. The Harness owns the original tool
arguments and approval boundary: an extension may only keep an operation
allowed, require the existing user confirmation, or deny dispatch. It cannot
rewrite parameters. `ask` still requires confirmation when the selected
permission mode is Full access. `deny` is recorded as an ordinary tool error
without dispatching the operation.

The `preview`, `parameters`, and `items` fields are bounded and credential
redacted. Optional extension failures are recorded and skipped; required
extension failures stop the current request.

## Entrypoints

Workflow calls use:

```json
{"invocation":"token-123","name":"triage","input":{},"message":{},"history":[]}
```

and return `{"output":"...","activity":{}}`.

Tool discovery uses `tools/list` and returns definitions with `name`,
`description`, `inputSchema` and `readOnly`. Current Core tool calls use
`tool.call` with `invocation`, `name` and `arguments` and return:

```json
{"content":"result","is_error":false}
```

Read-only annotations are hints that the Harness may use for approval policy;
they cannot enlarge the selected permission mode.

This is not the full MCP tool protocol. SDKs and templates use this same shape
as of v8.0.1. Keep the two call directions clear: extension-to-Host
`tool.call` asks Core to execute one of its tools, while Core-to-extension
`tool.call` invokes a tool declared by that extension.

## Host API

An extension may call the Host with a request such as:

```json
{
  "jsonrpc": "2.0",
  "id": "host-1",
  "method": "model.generate",
  "params": {
    "invocation": "token-123",
    "model": "current",
    "messages": [],
    "max_tokens": 100
  }
}
```

General method shapes are `model.generate`, `model.embed`, `task.delegate`,
`tool.call`, and `activity.report`. Opted-in delivery methods are listed below.
Every request must include the current invocation. The Harness may return
`-32001` through `-32006` for budget,
cancellation, permission, availability, stale invocation, or unknown outcome.
The extension must not automatically replay an `outcome_unknown` response.

The model call and output budget belongs to one extension within one Run. It
is shared by every hook and invocation of that Run and is released when the
Run settles, so opening another hook does not grant a fresh budget. The Host
serves at most 16 concurrent requests per extension process; extra requests
get an immediate `-32000` reply ("too many concurrent host requests") and are
not executed.

`task.delegate` is available only while the extension is running inside an
active foreground Run. Its `plan` is the strict `CollaborationPlan` payload
described by `schema/methods/task.delegate.json`. The plan is checked against
the active Run's model, tools, workspace and finite limits. The active Run is
the parent budget record; no background task is created, and child Runs use
the existing Run, Attempt and Evidence records. A child cannot use extension
tools or call `task.delegate` again. One active Run accepts one delegation
plan. Child results are returned as bounded JSON with statuses, outputs,
runtime evidence, usage and unresolved gaps. If a child result is UNKNOWN,
the Host returns `-32006` and the extension must not replay the plan.

## Scoped replies and one-time delivery

These methods are implemented for the original Telegram conversation of a
Run, with a live channel bridge and `delivery: true` in the manifest. They
never accept an arbitrary recipient, Bot key or raw platform file ID.
`context.prepare` and `turn.finished` include a `delivery` object when this
scope is available:

```json
{"reply":true,"rule_ids":["follow"],"max_text_parts":3,"max_stickers":1}
```

It is a capability hint, not a send receipt. `rule_ids` contains enabled
rules for this extension/conversation while proactive delivery is enabled;
an empty list does not disable a concrete ordinary reply partition. The Host
envelope is three logical text parts and one sticker; a configured sticker
limit can be up to three. `workflow.run` does not currently include this
delivery context object.

### Extension-owned reply expression

Plain Core has no reply modes or semantic paragraph splitter. An installed,
enabled extension selected for the Run may declare/register `delivery` and
return `{"reply_parts":["First paragraph.\n\n","Second paragraph."]}` from
`turn.finished`. The parts must be nonempty UTF-8 slices that concatenate
byte-for-byte to the **complete original output**, with at most three parts
and 64 KiB in total. A truncated callback preview has `delivery.reply=false`.
Malformed, altered or unauthorized parts are rejected; ordinary delivery
continues with the original answer and an extension error is reported.

This is a return value, not another Host RPC or a send receipt. Core freezes
source identity, extension/version and ordered parts once, then applies
ordinary rendering, platform splitting and outbox receipts. Before the first
render/enqueue, disabled, removed or changed extensions fall back to the
original answer. Already frozen groups remain immutable and are never replayed
because a setting changed. Conflicting extension partitions cannot overwrite
the first accepted plan.

The extension's existing `model_settings` and `settings_schema` also describe
finite per-turn string choices (`enum`, or `oneOf` with string `const` and
`title`). Only installed/enabled/selected extensions expose these controls.
A capability selection may contain `extension_settings`, keyed by extension
ID then field name. Core rejects undeclared fields/values and validates the
merged settings against the extension schema. The settings are frozen in the
existing Run snapshot and passed to hooks as `settings`; queue/restart reuse
them without changing global defaults. GUI renders ordinary labelled selectors
from this metadata; it never executes extension UI code. CLI `run --capabilities`
and chat `/capabilities` use the same selection. Persistent defaults still use
`extensions.set_policy` and normal confirmation. Expression names, defaults
and algorithms belong to the extension, including AffectWeave's single and
paragraph modes. There is no built-in mode-selection model tool or
`reply.suggest` RPC.

| Extension → Host | Params in addition to `invocation` | Current behavior |
| --- | --- | --- |
| `delivery.reply` | `action` | Only completed turn.finished: choose an already authorized sticker/reaction, with a durable no-replay claim; see delivery.reply.json. |
| `delivery.resources` | None | Returns `{total,offset:0,items}` for up to 12 nonexpired remembered sticker references in this target/conversation. No set lookup or pagination parameters. `total` is the returned page count. |
| `delivery.propose` | `candidate` | Persists one fixed text or sticker under an enabled, user-authorized rule. Returns a receipt; does not generate later content. |
| `delivery.read` | `id` | Reads a candidate owned by this extension in the invocation's conversation, including a candidate from an earlier Run. |
| `delivery.withdraw` | `id` | Cancels an owned `scheduled`, `reviewing` or `pending` candidate only before the first transport attempt. Otherwise returns its unchanged receipt; inspect `state`. |

Method schemas live in `schema/methods/delivery.*.json` and
`turn.finished.json`; reusable candidate, receipt and context definitions are
in `delivery.common.json`. For propose/read/withdraw/resources, only `invocation`,
`candidate` and `id` are recognized; unknown field names are rejected.
The schemas show the fields used by each method. Candidate fields are strict:

```json
{"key":"followup-001","rule_id":"follow","text":"The fixed follow-up text.","after":"2026-11-08T09:00:00+08:00","expires":"2026-11-08T10:00:00+08:00","reason":"One authorized follow-up"}
```

Supply exactly one nonempty `text` or `sticker_ref`. A key is 1–128 UTF-8
bytes without control characters; reason is at most 1024 bytes. Text is
bounded to 32 KiB and must fit one rendered Telegram message and pass the
credential check. A sticker must be a nonexpired Core reference in the same
target/conversation and must be allowed by both the rule and channel.
`after` and `expires` are absolute RFC3339 timestamps with an offset; `after`
must fit the rule's maximum delay (at most seven days), and may be at most
five seconds in the past at validation. Expiry must be after now and after
`after`, no more than 24 hours after `after`. The authorized rule supplies
the timezone; an extension-supplied `timezone` cannot override it.

Core deduplicates by connection, conversation, Run, extension and source
`key`. Repeating the identical candidate returns the original receipt;
changing any candidate content under that key returns `-32020`, as do other
proposal validation failures. Unknown fields/invalid candidate JSON return
`-32602`. There is no content-update, periodic job or contact-discovery API.

A receipt is `{id,state,suppressed_reason,message_id,sent,after_ms,expires_ms}`.
The ID is 64 lowercase hex characters. Both time fields are Unix
milliseconds; `after_ms` is the current not-before timestamp, not a delay.
`sent` is true only for state `sent`. Acceptance, an activity report and an
extension's own model output do not prove transport success or user response.
An UNKNOWN candidate is never automatically sent again.

### Core → extension: delivery.check

Automatic follow-ups require `delivery: true`, `delivery.check` in manifest
`events`, and both `delivery` and the event in the initialized registration.
The Host calls it when a candidate is due, using a fresh invocation and the
original Run's identity:

```json
{"invocation":"fresh-review-token","identity":{"core_id":"...","space_id":"...","conversation_id":"...","run_id":"...","adopted_inputs":[{"id":"conversation/1","actor_id":"telegram:user:7","text":"Original adopted input","occurred_at":"..."}]},"candidate":{"id":"<64 lowercase hex characters>","key":"followup-001","rule_id":"follow","text":"The fixed follow-up text.","kind":"text","expires_ms":1794093600000}}
```

The callback contains `invocation`, `identity`, the original frozen `settings`
and `variant`, and `candidate`; it does not carry fresh history, a new user
message, a sticker reference or raw sticker file ID. For a sticker candidate,
`kind` is `sticker` and `text` is empty. The extension can use its original
source key to consult its own already stored business state.

Return `{"allow":true}` or `{"allow":false}`. Unknown result fields and
nonboolean values fail closed; an absent/false allow also cannot authorize
dispatch. This callback has a timeout of `min(timeout_ms, 5000)` milliseconds
(the default manifest timeout is 2000 ms). Only `activity.report`,
`delivery.read` and `delivery.withdraw` are allowed during it. Model
generation/embedding, tools, delegation, re-proposal, resource listing and
unsupported callbacks return `-32004` before execution. It cannot rewrite the
candidate or run a new model/tool job.

Every invocation expires when its callback finishes or is cancelled; no
token may be retained for a background timer. The candidate outlives that
invocation in Core's outbox. A later legitimate callback obtains a new token.
Passive context without delivery opt-in still permits only activity; with
explicit delivery opt-in it may use these scoped methods, subject to the
same rules, and still gains no model/tool/delegation authority.

Core rechecks expiry, channel access/revision, enabled extension, current
conversation selection, the exact authorized rule, new-input cancellation,
quiet hours, cooldown and daily limits before review and before transport
claim. `allow:true` does not override a later invalidation. Missing callbacks,
process errors, timeouts or denial cancel this optional expression without
creating a model or tool Run. The review is persisted with a 10-second lease;
an abandoned review is cancelled after that lease rather than replayed.
Quiet hours and cooldown can defer a candidate only within its expiry.
Each outbox poll reviews at most one due extension candidate, so a backlog
does not multiply the subprocess timeout within that poll. Other candidates
remain scheduled for later polls; bounded expiry/cancellation checks still
run after that review budget is spent.
Rules are configured/confirmed through Core's `delivery.set_policy` control
path; enabling an extension or Full access alone grants no follow-up rule.

## Reliability boundary

Stopping a process does not revoke completed effects. An unknown result is not
the same as failure and is never automatically replayed. Extension output is
context or activity, not proof that a file, command, or external operation
succeeded. Actual operations remain in the Harness execution record.

## Optional supplemental execution (v8.7)

The complete capability, budget, trigger identity, cancellation and no-replay contract is [SUPPLEMENTAL_EXTENSION_EXECUTION](../docs/SUPPLEMENTAL_EXTENSION_EXECUTION.md). `context_analysis` and `scheduled_generation` are separate manifest/initialize capabilities; default passive context gains no extra permission. A scheduled trigger has empty adopted_inputs. `wake.evaluate` permits one no-tool current-model request and activity only. The per-extension `delivery.generation_enabled` authorization defaults off. No configured rules means no scheduled timer. `turn.finished.wake_hint` and `delivery.reply` are optional bounded additions, not arbitrary background RPC access. Method schemas and runtime checks must be changed together.
