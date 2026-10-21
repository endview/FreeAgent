# Controlled Collaboration

This layer, added in v5.0, is optional and disabled by default. It runs only
inside an explicit foreground task or continuation. There is no daemon,
distributed worker, organization registry, recursive delegation tool or
background model traffic. The single-agent core remains usable without it.
After a collaboration finishes, the same Chat can submit ordinary single-agent
tasks again. Unfinished controlled work still requires its own continuation or
cancellation; changing a Chat model does not bypass it.

## Start

Select a configured model in Chat, express a goal, and choose collaboration
only when needed:

```text
/collaboration once
Compare the implementation options.
```

The selected model proposes a bounded expert suggestion. The prompt offers
accept, reject or edit objective/model without JSON editing. Rejecting uses
the selected single Agent under the same cumulative task limits. Off creates
no planning call. Ask reviews a suggestion with experts; auto allows necessary
experts within saved authority, not a forced split of every task.
The ordinary answer does not display internal identities, a ledger or a task
graph. Analysis conclusions are not advertised as execution verification.

`/collaboration off|ask|auto` sets the current Chat. `/collaboration once`
affects only the next task. `/settings advanced` edits authorized model pools,
expert tool/file scope and finite limits. Only an explicit default-save choice
changes future user defaults. Priority is task override, Chat, user default.
Closing collaboration cancels unfinished controlled work, not the Chat itself;
subsequent ordinary single-Agent tasks remain usable.

For an individual command or a caller-authored explicit plan:

```text
freeagent run --data-dir <data> --collaboration ask --interactive <goal>
freeagent config collaboration --data-dir <data> --mode ask
freeagent collab create --data-dir <data> --plan examples/tasks/collaboration.json
freeagent collab inspect --data-dir <data> --parent <returned-parent-run>
freeagent collab continue --data-dir <data> --parent <returned-parent-run>
```

`create` persists a plan but sends no model or tool calls. Runtime assembly
may handshake already configured MCP servers and resolve selected provider
credentials. `inspect`, `control`, `resolve`, `revisions` and `increase-limits`
are offline and do not require provider credentials or MCP discovery. A default echo model is useful for
single-Agent/store checks, but cannot produce structured planning suggestions.
Execution objectives cannot pass their evidence gate by repeating the goal.
`continue --interactive` reviews a saved ask-mode suggestion without a second
planning dispatch, including after reopening the store.

Request deduplication is scoped to `--session` and `--request`. Omitting
`--session` creates a new parent conversation, not a retry. Changed plans
cannot reuse the same key. Cancelled children cannot be replaced to reset
parent counters. An explicit new parent plan is new work.

## Plan

The example is ordinary JSON, not an installation manifest. It declares:

- A parent objective and bounded cumulative budget.
- Roles containing instructions, optional named `model_id`, exact tool names,
  workspace-relative file subtrees, output format and child budgets.
- Children with distinct names, objectives and explicit context.
- Acyclic dependencies in any declaration order and an optional `parallel` flag.
- A finite `max_replans` cap, plus the parent's active execution seconds.

Roles can be edited in ordinary files or removed from a new plan. They are not
permanent organization objects. `adjust` and `revise` can change undispatched
work, split or merge assignments, add bounded review work and retire unused
assignments. Started work stays immutable; definitive failures may be retired
with their result and consumption intact. UNKNOWN cannot be replaced.
Revision history includes source, reason and the originating model Attempt.

Children receive only their role instructions, objective, explicitly supplied
context and their own messages. Parent instruction files and conversation
history are not copied. Dependencies grant ordering, not automatic access to
another child's text. A child's `results` grants name a direct dependency,
one scope (`output`, `summary`, `findings`, `references`, `artifacts`,
`questions` or `evidence`), a byte offset and a finite `max_bytes`.
The runtime automatically resolves only those excerpts, preserving complete
UTF-8 characters. Delivery, the child prompt and its user message are committed
together. Before dispatch, the saved content, source and section hashes, range
and truncation state are revalidated from runtime records in one transaction.
Unavailable or UNKNOWN sources do not satisfy the grant.

File tools enforce the declared subtree scopes with exact case matching.
Use the same path spelling in the plan and tool requests. Path scopes do not
sandbox commands, HTTP or MCP. Those tools still need exact approval and may access
resources outside the workspace using the user's operating-system authority.
Only explicitly selected tools are advertised and callable. Children have no
delegation entry point.

## Budgets

At most 16 planned children and 4 concurrent children are admitted. Serial is
the default even when the concurrency cap is larger than one.

Every child has cumulative steps and seconds plus total and per-call output
byte limits. Child steps/time cannot exceed the selected core's bounds. A
parent separately caps child count, cumulative model calls, tool calls,
output bytes, concurrent children, finite replans and active seconds across
the whole plan. Active time is the union of actual work; an all-idle approval
wait is excluded, but another running child still keeps its deadline.

Model/tool dispatch markers and reservations are persisted in one SQLite
transaction. The complete per-call byte allowance must fit both child and
parent before dispatch. Definitive results and reservation settlement share a
transaction, so failed persistence cannot release uncertain capacity. Definitive
output settles unused reservation;
failed, interrupted and UNKNOWN output retains the greater of reserved and
observed bytes. Streaming/progress records also count. Call count is never
refunded, even when a provider proves failure or no external effect.

Output accounting uses serialized UTF-8 model/tool response bytes, or the
greater captured streamed/progress size. It is not provider token billing.
UNKNOWN reserves the whole allowance because final output is unmeasured.
An adapter that ignores its limits can produce an overrun; observed bytes
remain charged, streaming cancellation is attempted, and no next call is
dispatched. A definite overrun result is retained as limited, not rewritten
as an unknown external effect. An ambiguous interrupted call stays UNKNOWN.
Trusted custom adapters must honor cancellation and output bounds.

Only independent built-in file-list/read/search work may run in parallel.
Commands, MCP, HTTP and writes remain serial. Ready read-only children can
share a batch after their dependencies complete.
The foreground collaboration holds the ordinary workspace execution lease
for its duration; another core caller cannot execute effects beside it.
Read-only concurrency is not snapshot isolation from unrelated local programs.

## Controls

```text
freeagent collab control --data-dir <data> --parent <id> --child one --action pause
freeagent collab control --data-dir <data> --parent <id> --child one --action resume
freeagent collab control --data-dir <data> --parent <id> --child one --action cancel
freeagent collab control --data-dir <data> --parent <id> --action cancel
freeagent collab adjust --data-dir <data> --parent <id> --child one --spec child.json
freeagent collab revise --data-dir <data> --parent <id> --edit edit.json
freeagent collab revisions --data-dir <data> --parent <id>
freeagent collab increase-limits --data-dir <data> --parent <id> --limits limits.json
```

Pause prevents new dispatch and cancels an active call; it does not promise a
replayable result or undo. Resume is refused for UNKNOWN. Cancel is permanent
and retains history, receipts and uncertain effects. Parent cancellation
propagates to active foreground children; undispatched work is not started.

The CLI has one process owner per data directory. While `continue` owns it,
use its interrupt to cancel execution, or a local caller's same Collaboration
instance for named live controls. The separate `control` command is for the
idle persisted plan; it is not an IPC service. Interrupted execution must be
continued explicitly and never resumes in the background.

`revise` requires the current `expected_revision` and a bounded `reason`,
with optional `roles`, `upsert` assignments and `retire` names. User work
edits supersede unusable known replan proposals, not their charged Attempts.
Limit increases require finite complete parent limits, never reduce a limit
or clear consumption, and do not implicitly continue work. Model proposals
cannot increase them. Automatic replanning handles saved failures or bounded
`follow_up` requests, consumes normal calls/output/time, and never repeats
known, invalid or UNKNOWN proposals. A saved proposal is applied before
new child dispatch.

For consequential child work:

```text
freeagent collab continue --data-dir <data> --parent <id> --interactive
freeagent collab preview --data-dir <data> --parent <id> --child one
freeagent collab confirm --data-dir <data> --parent <id> --child one --digest <preview-digest> --approve
```

Omitting `--approve` declines the exact operation. Approval binds run, attempt,
saved call and current preview. Changed files or content need a new preview.
Approval does not grant authority to later child edits.

## Merge And Evidence

Inspection separates each child's model explanation from actual successful
tool observations, file/command/HTTP receipts, checks and uncertainty.
Parent synthesis is deterministic by default. Explicit `synthesis: true`
adds a guarded ordinary model call with no tools; it consumes cumulative
limits. The draft expert answer is saved first. Failed synthesis retains that
answer, and UNKNOWN is never automatically replayed. A saved source digest
binds the synthesis to the expert results and runtime evidence it interpreted.
A parent is completed only when all children complete, execution roles have
successful dispatched tool evidence, declared JSON contracts are satisfied, and no
runtime uncertainty or unresolved conflict remains.
An explicit `acceptance: "analysis"` allows reasoned results without a fabricated
tool call. Omitted acceptance currently uses the execution evidence gate.
AI-generated experts default to analysis unless the objective requires
execution. `required_evidence` may specify `read`, `file_change`, `command`,
`check` or `http` with an exact target. Analysis needs no fabricated tools;
execution requirements must match runtime records. The ordinary answer
separates opinions, reported sources/artifacts, actual receipts, checks and
remaining uncertainty. Model classifications do not create runtime facts.

Explicit same-child or dependency-chain writes with matching before/after
hashes establish a legal version chain. Stale reads/writes, different external
snapshots and incomparable writing branches remain conflicts. A matching hash
alone is not parallel snapshot isolation. Resolve against the exact current
digest and retain both facts with an explicit bounded decision:

```text
freeagent collab resolve --data-dir <data> --parent <id> --digest <inspect-digest> --decision "The implementation supersedes the earlier snapshot; retain both."
freeagent collab continue --data-dir <data> --parent <id>
```

This identifies concrete file evidence conflicts, not arbitrary semantic
disagreement between model prose. The report retains all interpretations;
users must evaluate unsupported conclusions themselves.

## Shared Memory And Knowledge

Role profiles may include `memory.shared` with `read`, `append`, `modify`,
`kinds` and optional `entries` restrictions. The grant is bound to this parent
and workspace; shared reads/writes use ordinary guarded tools. Already-authorized
task-local writes need no repeated user prompt. Expanding authority or copying
private content is a separate explicit user action.

Shared content uses the six `fact`, `hypothesis`, `finding`, `decision`,
`question` and `artifact` kinds. The kind is a source label, not proof of a
successful check. Author identity comes from the runtime. Updates require the
expected revision; concurrent stale edits fail rather than silently overwrite.

AI-generated experts inherit selected knowledge collections, never the parent's
retained private entries. An explicit user-authored plan can name its own
authorized selections. Temporary experts have distinct parent/child identities;
reusing a stable Agent identity or private entry requires explicit user selection.
Replanning cannot invent retained private grants or unselected collections.

Shared writes and tool receipts commit together with parent settlement.
`memory sources --run <parent>` includes child sources with revision and excerpt
bounds. Sources and memory cannot grant tools, widen paths or erase UNKNOWN.
Task completion does not promote shared content to cross-task memory.
See [Memory](MEMORY.md).

## Recovery

Existing conversation/run/message/attempt tables store execution. Existing
`meta` entries store the bounded plan, relationships, controls and counters.
No separate Task, Step, Checkpoint or Evidence objects are introduced.
Backup and restore include these records.

Restart clears active concurrency leases, but not dispatched-call counts,
output reservations or UNKNOWN facts. Known saved tool results can support
explicit continuation; unknown child outcomes stop dependent work and are
never automatically replayed. Cancelling a parent is not evidence that its
children had no effects.
