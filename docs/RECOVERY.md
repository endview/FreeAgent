# Recovery And Backup

## Offline diagnosis

Run `doctor --text` before changing configuration or restoring data. JSON remains
the default for scripts (`doctor` without `--text`). In a Windows portable
directory use `.\bin\freeagent.exe doctor --text`; in Linux portable mode use
`./bin/freeagent doctor --text`. On a managed server, use the installer-provided
management command, normally `/usr/local/bin/freeagent doctor --text`.

The report checks configuration, database integrity, key metadata, selected
Provider switches and the command sandbox. Independent errors are retained in
one report, including when config.json is invalid. Each error or warning has a
next action. It does not resolve/decrypt saved credentials, send Provider
requests, start MCP, initialize missing data or change permissions/settings.
The sandbox probe is a temporary local process.

`ok` means no blocking local integrity/backend error was found. It does not
certify a Provider connection. No model, a disabled Provider or an unavailable
optional sandbox is explained separately; disabled features remain disabled.
JSON includes local paths and model/key-profile names, but no configuration
prompt bodies, environment values, credentials or database row contents.

| Check | Next action |
| --- | --- |
| Missing/invalid configuration | Check the data directory, current format, workspace and instruction paths; retain existing data before recovery |
| Store integrity/format error | Verify a backup and restore into a new empty directory; keep the original database |
| Unreadable key metadata | Check the keys-file override, permissions and current format; keep the original file |
| Provider disabled or model missing | Open Key management, select/configure the model and explicitly enable the intended Provider |
| Required sandbox unavailable | Check Bubblewrap/namespaces on Linux or AppContainer on Windows; an explicit Full/host choice uses the OS user's authority |
| Public Web configured | Check `public status`, `owner status`, the proxy configuration and unit journal; TLS/public reachability require separate verification |

For a server install/update, run the installer with `--check` first. It verifies
the package and installation prerequisites without modifying the service.
See [server deployment](SERVER_DEPLOYMENT.md).

## Run recovery

`inspect --run <id>` shows the Run, model/tool Attempts and known responses.
It can include conversation and file contents; do not paste it into public
reports without reviewing it.

| Run state | Action |
| --- | --- |
| `completed` | Ask the next question in the same session |
| `waiting_confirmation` | `continue --run <id> --interactive` |
| `stopped` | Inspect, then explicitly continue if configuration still matches |
| `limited` | Inspect the original reason and usage; a settled limit allows an explicit next round, while unresolved work must be recovered first |
| `unknown` | Check effects yourself; ordinary continuation is forbidden |
| `failed` | Inspect definitive model error, then start a new request |
| `abandoned` | Task ended; unknown facts remain |

Use `--data-dir` on these commands. Only one unresolved Run is permitted per
conversation. A limit with no pending/unknown calls or active commands can settle
and release occupancy while retaining `limited`, its reason and consumed budget.
An explicit whole-message “继续”, `/continue` or the corresponding Chat button
creates one linked successor with a fresh finite budget and the original
requester constrained by current permissions. Duplicate continuation does not
create multiple successors or replay finished tools. Collaboration children
cannot use this to escape the parent's total budget. An explicit increase of
the original Run's finite limits can still resume that Run if no successor exists.

Duplicate submission with the same session, request key and text
returns that Run, including its stopped/unknown state, without another call.
Use a different key for a genuinely new request, not to bypass recovery.

## Cancellation And UNKNOWN

For a context-limited conversation, `context inspect --session <id>` shows the
summary revision, source digest and any blocked work. `context compact --session
<id>` can reduce completed older groups even when automatic compaction is off.
Stopped/limited Runs keep their consumed budgets. A settled limited round may
start an explicit successor as described above; unresolved work still blocks it.
Refresh rebuilds a stale summary; Clear removes only that selection.
Running calls, unrelated pending approvals and UNKNOWN block these mutations.
Failed/cancelled compaction retains the previous summary and original history;
inspect its maintenance Run and model Attempts before deciding the next action.
Ordinary Stop leaves the owner available to other GUI/CLI requests.

In v7.12, a directly requested settings proposal expires after 10 minutes;
repeat the intended setting operation to create a new proposal. A model proposal
stays attached to its waiting Run: reopen its card for a resource/version check
and fresh confirmation credentials. Rejected or invalid proposals close their
waiting Attempt and Run rather than leaving permanent occupancy.

When upgrading existing current-format data, resolve unfinished command Runs
before switching binaries. Old command snapshots cannot continue with the new
unmatched-rule semantics; inspect their receipts and explicitly start new work.
Existing Telegram spaces/history are kept, but their old pairing codes do not
grant access. An administrator must verify and approve the numeric binding in
the Telegram settings page. Neither step automatically replays work or upgrades
permissions.

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
