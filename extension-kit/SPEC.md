# FreeAgent Extension Protocol

This directory describes `freeagent-ext/1`, the local extension protocol
introduced in FreeAgent v6.0. An extension is one local process started over JSON-RPC 2.0
NDJSON. It is not a package manager, a remote service, or a second execution
engine.

## Transport

One JSON object is exchanged per line on stdin and stdout. Stderr is reserved
for diagnostics and is captured by the Harness in a bounded, redacted log.

```json
{"jsonrpc":"2.0","id":"1","method":"initialize","params":{}}
{"jsonrpc":"2.0","id":"1","result":{}}
```

The protocol is bidirectional. The Harness may call an extension, and an
extension may call a Host API method with the invocation token received in the
current hook or workflow. Unknown fields are ignored in v1. The
`task.delegate` plan is the only strict payload and is documented by its
method schema when that entrypoint is enabled.

## Lifecycle

`initialize` is sent before the first hook or entrypoint:

```json
{
  "protocol": "freeagent-ext/1",
  "host_version": "6.0.0-dev.1",
  "workspace": "C:\\work",
  "data_dir": "C:\\data\\extensions\\com.example.demo\\data",
  "settings": {}
}
```

The extension must return its own identity and the capabilities it actually
registered:

```json
{
  "id": "com.example.demo",
  "version": "0.1.0",
  "provides": ["context"]
}
```

`provides` is a subset of `context`, `workflow`, and `tools`. The identity must
match `extension.json`.

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
- `events`: bounded callbacks such as `turn.finished` and `operation.check`.
- `workflows`: user and agent-callable workflow declarations.
- `tools`: whether the extension exposes MCP-shaped tools.
- `env`: names the extension needs. Values are mapped by the user from the
  process environment or a Key profile; the extension never receives the Key
  profile itself.

The Harness owns budgets, timeouts, permissions, cancellation, data paths,
process cleanup, and execution records. The extension owns only its local
implementation and files below the supplied `data_dir`.

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

Tool discovery uses `tools/list` and returns MCP-shaped definitions. Tool calls
use `tools/call` with `name` and `arguments` and return:

```json
{"content":[{"type":"text","text":"result"}],"isError":false}
```

Read-only annotations are hints that the Harness may use for approval policy;
they cannot enlarge the selected permission mode.

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

Available method shapes are `model.generate`, `model.embed`, `task.delegate`,
`tool.call`, and `activity.report`. Every request must include the current
invocation. The Harness may return `-32001` through `-32006` for budget,
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

## Reliability boundary

Stopping a process does not revoke completed effects. An unknown result is not
the same as failure and is never automatically replayed. Extension output is
context or activity, not proof that a file, command, or external operation
succeeded. Actual operations remain in the Harness execution record.
