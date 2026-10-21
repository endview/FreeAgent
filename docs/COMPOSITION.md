# Local Composition

`internal/harness.TaskService` is a small local boundary, not a public SDK
platform. The CLI and `examples/local-caller` share it.

- `Submit` creates or continues a conversation with deduplicated input.
- `SubmitWithOptions` can bind a new Run to an explicitly selected model.
- `RunOptions.Collaboration` overrides only that task. Chat preferences are
  managed by `CollaborationSettings` / `SetCollaborationSettings`; explicit
  `SetDefaultCollaborationSettings` changes future defaults, not current runs.
- `RunOptions.Review` accepts, edits or rejects a bounded suggestion. It does
  not grant extra model/tool/file authority or silently write preferences.
  `ContinueWithOptions` reuses the saved suggestion after approval wait/reopen.
- `SelectedModel` / `SelectModel` manage the Chat choice for subsequent tasks.
- `ModelID: "auto"` explicitly selects bounded authorized routing; manual
  selection skips it. `Routing` returns durable model/revision decisions.
- `SwitchModel` adds a model/revision binding only between complete model/tool
  groups; active work, pending tools and UNKNOWN block it.
- `Limits` / `IncreaseLimits` allow explicit finite capacity increases without
  resetting consumption or implicitly continuing work.
- `Continue` / `ContinueLatest` resume known work without resetting budgets.
- `Preview` / `Confirm` bind a decision to one saved call and exact preview.
  The decision also binds the run and durable attempt identities and survives
  reopen only while configuration, tool call and preview still match.
  A confirmation does not cover another task or later model changes.
- `SetApproval` supports an interactive caller-owned confirmation adapter.
- `Cancel` propagates to the active operation. UNKNOWN is never replayed.
- `Inspect` projects run, attempts and runtime evidence from existing records.
- `SetEvents` supplies a caller-owned event renderer.

`Adapters` replaces the model client, tool runner, approval hook or event
renderer. The core still owns persistence, dispatch markers, budgets,
cancellation and UNKNOWN classification. Optional tools are selected explicitly;
no tool-specific state is added to the run state machine. Named profiles are
selected through `ModelFor`; actual model/revision facts remain on Attempts.
Tool-less models and context/output capability bounds are enforced before
dispatch, preserving complete tool-message groups.
Auxiliary model work uses the same dispatch/persistence boundary. Auto
assessments have no tools, do not stream into Chat history, and consume normal
Run and parent cumulative limits. Safe upgrades keep saved results and
consumption. Restricting Auto authorization freezes the Run's current binding;
new authorization never silently expands the saved pool.
Optional commands, MCP and HTTP all compose through the same ToolRunner.
HTTP receipts project from existing Attempt responses; no evidence table,
HTTP-specific run state, module registry or lifecycle is introduced.

The example uses the same runtime, receives events, asks for exact approval
and supports signal/deadline cancellation:

```text
go run ./examples/local-caller --data-dir <existing-directory> "inspect project"
```

Templates are ordinary UTF-8 workspace files, selected explicitly:

```text
freeagent run --template examples/tasks/review.md "review the current change"
```

Long tasks may explicitly enable an in-memory bounded recap:

```text
freeagent config context --summary-bytes 4096
```

Only complete older groups within the current run are compacted. Source message
IDs and hashes identify omitted records. Original history remains intact.
Recaps are untrusted excerpts, not execution evidence or cross-session memory.
Oversized original input or a newest tool group that cannot fit still stops
before model dispatch. Setting the bound to zero disables compaction.
Templates and explicit references require a read-only adapter; substituting a
consequential adapter cannot bypass the core approval contract.

## Explicit Memory

`AgentIdentity` / `SelectAgent` keep Agent identity separate from model selection.
Reuse is explicit; changing Agent clears retained private selections, while
existing Run snapshots retain their identity and authority.
`SelectedMemory` / `SelectMemory` set future Chat inputs; `RunOptions.Memory`
overrides only a new task. Run grants are frozen, and each model request freezes
and transactionally revalidates the selected revisions before dispatch.

`ReadMemory`, `ListMemory`, `SaveMemory`, `MemoryScopes` and `MemorySources`
provide offline user management. Copy and completed-result previews name exact
sources. `PreviewKnowledgeImport` validates an explicitly selected workspace
file; `SearchKnowledge` is an explicit local user search. `SetMemoryConfig`
requires an idle environment and never starts a model or background worker.

Runtime `memory_list/read/write` and `knowledge_search` use the same dispatch,
limits, cancellation and result records. Local memory mutation, receipt,
Attempt result and parent settlement commit together. A definite transaction
failure rolls back the mutation; saved outcomes are reused, and a lost outcome
is not blindly replayed. Source data does not become execution evidence.

Versioned memory entries/revisions, content-free operation receipts and the
local FTS index live in the same SQLite database. These are actual editable
content structures, not separate Task, Step, Checkpoint or Evidence objects.
No memory service, background indexer or model bypass is introduced.
See [Memory](MEMORY.md).

## Optional Collaboration

`NewCollaboration(tasks)` composes over this boundary. It is disabled by
default and has no background scheduler. Ordinary `SubmitWithOptions` can
plan and execute collaboration within one foreground operation, with one
optional suggestion review. An advanced caller can also explicitly create a
bounded plan, inspect it, and invoke `Continue`. Ordinary task continuation
cannot bypass the parent budget of a controlled child.
Routing, planning and single-Agent fallback share the parent guard. At most
one initial planning dispatch is admitted; saved failures, invalid responses
and UNKNOWN are not silently replanned. Turning a Chat off wins against plan
creation and cancels active controlled work, retaining known and UNKNOWN facts.

It retains the store's workspace execution lease while scheduling serial
children or limited read-only batches. Only atomic pre-dispatch reservations,
generic output accounting and conservative recovery extend shared reliability.
No new persistent task, step, checkpoint or evidence entities were added.

Use one collaboration instance for foreground execution and live control.
`Control` can pause or cancel the parent or a named child and propagates the
context cancellation to its active calls. `Adjust` only changes work that has
never dispatched. `ChildPreview` / `ChildConfirm` reuse exact saved-operation
approval. `Inspect` and `Resolve` expose runtime facts and exact conflict
decisions without another model synthesis call.

`Revise` accepts an explained, revision-bound edit of undispatched work.
Split/merge/retirement and appended review work retain original identities,
failed results and cumulative creation. `Revisions` returns durable history.
`IncreaseLimits` explicitly increases finite parent allowances without
resetting consumption, UNKNOWN reservations or starting work. Finite model
replanning uses the same guarded `generate` boundary. Saved proposals precede
new dispatch; invalid/failed/lost proposals cannot be silently redispatched.
Parent union active time excludes all-idle approval waits and includes
auxiliary calls and other active children.

A completed collaboration releases the Chat for ordinary single-agent work.
Control and cumulative limits bind its parent/children, not the Chat forever.

See [Collaboration](COLLABORATION.md) for the CLI, plan, budgets and limitations.
