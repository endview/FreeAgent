# Recovery And Backup

`inspect --run <id>` shows the Run, model/tool Attempts and known responses.
It can include conversation and file contents; do not paste it into public
reports without reviewing it.

| Run state | Action |
| --- | --- |
| `completed` | Ask the next question in the same session |
| `waiting_confirmation` | `continue --run <id> --interactive` |
| `stopped` | Inspect, then explicitly continue if configuration still matches |
| `limited` | Inspect; explicitly increase finite limits, then continue, or abandon |
| `unknown` | Check effects yourself; ordinary continuation is forbidden |
| `failed` | Inspect definitive model error, then start a new request |
| `abandoned` | Task ended; unknown facts remain |

Use `--data-dir` on these commands. Only one unfinished Run is permitted per
conversation. Duplicate submission with the same session, request key and text
returns that Run, including its stopped/unknown state, without another call.
Use a different key for a genuinely new request, not to bypass recovery.

## Cancellation And UNKNOWN

For a context-limited conversation, `context inspect --session <id>` shows the
summary revision, source digest and any blocked work. `context compact --session
<id>` can reduce completed older groups even when automatic compaction is off.
Stopped/limited Runs keep their consumed budgets and must then be explicitly
continued. Refresh rebuilds a stale summary; Clear removes only that selection.
Running calls, unrelated pending approvals and UNKNOWN block these mutations.
Failed/cancelled compaction retains the previous summary and original history;
inspect its maintenance Run and model Attempts before deciding the next action.
Ordinary Stop leaves the owner available to other GUI/CLI requests.

Ctrl+C stops new steps and cancels current work. During chat tasks it returns
to input when possible; while idle it exits chat. One-shot commands exit.
Determinate
responses received before cancellation are retained. An interrupted call after
the durable dispatch marker can already have acted, even if no response arrived.
On restart it becomes UNKNOWN. A marker is not proof that the request was sent.
Command cleanup does not undo effects. HTTP headers can establish a known
response status even when the body is incomplete; such output is explicitly
marked incomplete/truncated, not a successful read or check. Missing HTTP
responses remain UNKNOWN. Inspect runtime receipts, not model claims.

`abandon --run <id>` ends the task but does not mark unknown attempts successful,
failed-with-no-effect or safe-to-retry. Later tasks in the same workspace receive
a warning, including across sessions. Inspect the old record and check actual
effects before manually repeating related work. There is no automatic retry or
generic semantic deduplication. This first slice offers abandonment and explicit
new work, not a dedicated forced-retry/reconciliation subsystem.

`run --model` binds a new Run to the named profile without changing the Chat's
selection or the default. `continue --model` adds a new model binding only
after all previous model/tool groups are complete; a pending tool approval,
dispatched call or UNKNOWN blocks the switch. Changing the model never clears
saved results or consumed limits.

Run time includes active processing, not user-confirmation waiting or normal
downtime between explicit continuations. Still-running commands and children
retain their own deadlines. After a crash, the last active interval is
conservatively charged through recovery, including downtime. Neither
continuation nor changed config grants the same Run a fresh budget.
Use `continue --max-steps <n> --max-seconds <n>` or Chat `/more` to explicitly
increase finite limits. This preserves steps, elapsed time, results and UNKNOWN,
and requires a separate continuation; it never resumes work implicitly.

## Backup

Exit chat and other writers, then:

```text
freeagent backup --data-dir <state> --out <new-backup>
freeagent backup-verify --source <new-backup>
freeagent restore --source <new-backup> --data-dir <new-restored-state>
freeagent doctor --data-dir <new-restored-state>
```

Targets must not already exist. Backup uses `VACUUM INTO`, so committed WAL
history is included; copying an active `.sqlite` file yourself is not equivalent.
It includes the database, config and only selected instruction files with
hashes. MCP executables, resolved environment secrets and project files are not
copied. Restore verifies the exact private staged copy before publishing it.
Failure does not overwrite an existing target.

Backup verification checks the exact current schema, SQLite/FK integrity,
config and instruction bounds/hashes. It does not authenticate who made the
backup or resolve secrets/start MCP/contact the model. The original workspace
path remains in config; restoring does not restore or relocate that workspace.
If the workspace is absent, restore can succeed but doctor/runtime will reject
the unavailable workspace until it is restored or explicitly reconfigured.

## Memory Lifecycle

Current-format backups include memory entries, body revisions, source references,
Chat selections, idempotent operation receipts and the local knowledge index.
Restore does not contact a Provider or re-import source files. A backup made
before deletion may restore content; this is content recovery, not proof that
deletion never happened. Older-format data and backups are rejected, not migrated.

Disable prevents new memory-tool reads and selected-body injection but preserves
content for explicit user management. Delete removes the current body, all body
revisions and the corresponding knowledge search row. Content-free operation
receipts remain; a duplicate mutation returns its saved receipt without
resurrecting content. A deleted entry cannot be enabled.

Deletion does not retract sent Provider requests, ordinary Chat history snippets,
other explicit copies or old backups. Already-dispatched requests remain frozen.
`/memory sources` and `memory sources --run <id>` show what was used and whether
the current source is active, updated, disabled or deleted. Clearing future
selection is not erasure of history or revocation of a saved Run grant.
See [Memory](MEMORY.md).
