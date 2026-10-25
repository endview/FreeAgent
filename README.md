# FreeAgent Harness

A small local harness for persistent conversations and model-driven file work.
One CLI, one model/tool loop, one SQLite store. Built-in file tools work without
installing an MCP server. An optional loopback Chat uses the same TaskService.

This is the **v7.4.0-dev.1 development preview**. Download published Windows portable
builds from [Releases](https://github.com/endview/FreeAgent/releases).
It uses a new
current-only `Harness-v5` data directory and the v7 store format. Old
configurations, databases, backups, commands, and SDKs are not supported or
imported. Do not point it at an old data directory.
The v7 control service, GUI/CLI attach path, sandboxed Windows command
launcher and current provider adapters are implemented. The v7.0 acceptance
checks passed on 2026-10-21: real DeepSeek Chat, tools, Responses and web
search, the packaged CLI, the native Windows race suite and the manual
desktop checks. See [v7.0 progress](docs/FREEAGENT_V7_0_IMPLEMENTATION_PROGRESS.md).

v7.0.2 adds interactive GUI/CLI attach and resolves native Scoop shims inside the
Windows command sandbox. Its local, native race and clean portable startup
checks passed; real Provider tests were not rerun for this release. See
[v7.0.2 acceptance](docs/FREEAGENT_V7_0_2_IMPLEMENTATION_PROGRESS.md).

v7.1 adds durable conversation summaries, manual GUI/CLI `/compact` and optional
automatic compaction at the input limit. Original history remains intact.
Auxiliary model calls use the existing Run budgets and Attempts. See
[v7.1 acceptance](docs/FREEAGENT_V7_1_IMPLEMENTATION_PROGRESS.md).

## Start

The Windows portable preview starts by double-clicking `FreeAgent.exe`, needs no
Go or Node installation, and stores Keys encrypted for the current Windows
user. The CLI is `bin\freeagent.exe`. See
[Portable Instructions](docs/PORTABLE.md). It is unsigned and uses its own
`data/Harness-v5`; it does not import existing installations.

The v7 desktop build opens `FreeAgent.exe` without a console window and uses
the installed Microsoft WebView2 runtime. If WebView2 is unavailable, it
opens the same local Chat in the default browser and shows a stop prompt.
Some Windows 10 installations need the WebView2 Runtime installed separately.

Requires Go 1.26.5 to build; the executable needs no Go installation.
The checked-in Chat assets are embedded. After editing frontend source, use
Node 24 and pnpm to install the locked dependencies and rebuild before Go:

```powershell
pnpm --dir internal/chatui install --frozen-lockfile
pnpm --dir internal/chatui test
pnpm --dir internal/chatui build
```

```powershell
go build -o ./bin/freeagent.exe ./cmd/freeagent
$exe = (Resolve-Path ./bin/freeagent.exe).Path
$data = Join-Path $env:LOCALAPPDATA 'FreeAgent-Harness-Preview'
$project = (Get-Location).Path

& $exe init --data-dir $data --workspace $project
& $exe key add --name main --url 'https://your-provider.example/v1'
& $exe key test main
& $exe config model --data-dir $data --id main --key main --model 'your-tool-capable-model'
& $exe --data-dir $data
```

`init` creates a data directory with no models; tasks exit with code 2 until a
model is configured. A key profile holds a provider address and its key. Key
profiles live in one `keys.json` shared by every data directory of the same user
(`os.UserConfigDir()/FreeAgent/keys.json`, or `data/keys.json` in a portable
package; `FREEAGENT_KEYS_FILE` overrides it). The address may be a service root,
a `/v1` address or a complete `/chat/completions` or `/responses` URL.

`key add` reads the key with hidden input. `--key-env <variable>` references a
process variable instead, `--key-stdin` reads one line for scripts, and
`--no-key` is accepted only for loopback addresses. Keys are never accepted as
command arguments. On Windows a saved key is encrypted for the current user with
DPAPI. `key list` and `doctor` show the source and a short hint, never the key.
`key test` sends one `GET <base>/models`; `key models` lists the reported model
names. Both are explicit network requests; no other configuration command makes one.

`config model --key <key profile>` stores only the key profile id. The first
model becomes the default. `--provider echo` is an offline deterministic test
model; it checks persistence, not model reasoning or tool selection.

The adapter supports Chat Completions and Responses JSON/SSE responses,
function calls, stateless output-item continuation and explicit provider-side
`web_search_call` metadata. Each provider still needs its own real test. It does
not redirect credentials, retry failed calls or fall back. DeepSeek search uses
`deepseek-v4-pro` with Responses and `web_search=supported`; it is independent
of FreeAgent's optional local HTTP tool.
Optional explicit Auto selection belongs to TaskService, not the adapter.
Configuration commands do not make model requests, start MCP programs or resolve
keys. Profile edits leave existing Run bindings intact; changing a
bound profile stops its next dispatch until explicitly reconciled at a safe
boundary. Inside Chat, `/model <id>` selects subsequent tasks without exiting.
Use `chat --resume <session>` to continue an existing conversation.

## Local Chat

```powershell
& $exe web --data-dir $data
```

Open the launch address printed by that process. It binds only to `127.0.0.1`,
requires that launch's key, and is a foreground single-Agent caller, not an
HTTP control platform or hosted service. Keep the address private. Closing a
browser does not stop its task; use the page's Stop control. Closing the server
stops new dispatch but does not undo effects or resolve UNKNOWN calls.

Chat supports multiline input, Markdown, code, tables, bounded LaTeX, drafts,
raw-source copy, exact approvals, runtime evidence and same-Chat model changes.
Optional collaboration and advanced memory management remain in the CLI.
The initial Echo model verifies UI/persistence only, not real model quality.
See [Chat Contract](docs/CHAT_CONTRACT.md).

## Daily Commands

```text
key add --name <name> --url <address> [--note <text>] [--preset deepseek] [--key-stdin | --key-env <variable> | --no-key]
key list [--json]
key edit <id|name> [--name <name>] [--url <address>] [--note <text>] [--key-prompt | --key-stdin | --key-env <variable> | --no-key]
key remove <id|name> [--force | --disable-models [--new-default <id>]] [--data-dir <directory>]
key test <id|name> [--json]
key models <id|name> [--json]
config show
config model --id <name> [--default] --key <key profile id|name> --model <name>
config model --id <name> [--default] --provider echo
config model --id <name> --default
config model --id <name> --remove [--new-default <id>]
config workspace --path <directory>
config commands --enabled [--allow <absolute-executable>] [--timeout 120 --output-bytes 32768] [--inherit-environment] [--git-write]
config http --enabled --origin <scheme://host[:port]> [--timeout 20 --output-bytes 32768]
config context --summary-bytes <0-16384>
config collaboration --mode off|ask|auto
config memory [--knowledge] [--max-entries 16 --context-bytes 16384 --document-bytes 65536]
control <state|catalog|list|plan|confirm|reject|revert-plan|review> [--kind <kind> --arguments <json>] [--operation <id>] [--code <code>]
chat [--resume <session>]
web [--port <loopback-port>]
run [--session <id>] [--model <id>] [--permission ask|workspace|full] [--confirm-full-access] [--request <key>] [--template <workspace-file>] [--json] [--interactive] <task>
continue --run <id> [--model <id>] [--max-steps <n>] [--max-seconds <n>] [--interactive] [--json]
sessions [--show <id>]
inspect --run <id>
preview --run <id> [--json]
confirm --run <id> --digest <digest> [--approve] [--json]
abandon --run <id>
tools list
tools test <server>
ext <new|check|test|run|logs> [flags]
memory <scopes|list|read|save|save-result|update|disable|enable|delete|copy|import|search|sources|select|clear|agent> [flags]
collab create --plan <json-file>
collab continue --parent <id> [--interactive]
collab inspect --parent <id>
collab control --parent <id> [--child <name>] --action <pause|resume|cancel>
collab adjust --parent <id> --child <name> --spec <json-file>
collab preview --parent <id> --child <name>
collab confirm --parent <id> --child <name> --digest <sha256> [--approve]
collab resolve --parent <id> --digest <sha256> --decision <text>
doctor
backup --out <new-directory>
backup-verify --source <backup-directory>
restore --source <backup-directory> --data-dir <new-directory>
```

Use `--data-dir` on each runtime command. Otherwise the default is
`os.UserConfigDir()/FreeAgent/Harness-v5`. On Windows this is normally under
`APPDATA`, not the preview directory in the example above.
Flags must precede task text. `config model` updates one named local profile;
`--default` also changes the default used by unselected Chats. `--remove`
deletes a profile and its Auto/collaboration references; removing the default
requires `--new-default` while other models remain. `config workspace` changes
the workspace only when no Run is unfinished. `key remove` refuses while models
in the given data directory use the key; `--disable-models` removes those
models in the same step and `--force` leaves them to fail at dispatch. Other
data directories are not checked. `run --model`
binds only that new Run. `continue --model` switches an unfinished Run only at
a safe complete model/tool boundary; `--max-steps` and `--max-seconds` add
finite capacity without resetting consumption. Chat accepts `/model` to list or
select the model for subsequent tasks and `/more` to explicitly increase finite
limits, in addition to `/history`, `/sessions`, `/resume
<number>`, `/new`, `/status`, `/continue`, `/abandon`, `/file <path>`, `/begin`
... `/end`, and `/exit`. Ctrl+C during a task cancels that task and returns to
input when possible; Ctrl+C while idle exits chat.

In the default Ask mode, file changes, commands, HTTP GET and **every MCP
call** require explicit confirmation. `run --permission workspace` runs built-in
workspace file changes and sandboxed commands without asking;
`--permission full --confirm-full-access` skips tool approval and runs commands
on the host. Settings changes proposed by the model always need confirmation:
the CLI shows a short code, Chat shows a card. `control` changes the same settings
as the GUI and the AI conversation through one control service. Noninteractive
tasks stop at `waiting_confirmation`; continue them with `--interactive`, or
run `preview --run <id>` and then `confirm --run <id> --digest <digest>
--approve`. Without `--approve`, `confirm` declines the call. A digest that no
longer matches the pending call is refused; preview it again.
Request deduplication is scoped to the same `--session` and `--request`.
Omitting `--session` creates a new conversation, not a retry.

Exit codes: 0 completed, 1 failed, 2 usage or configuration error, 3 waiting
for confirmation, 4 stopped or limited, 5 unknown result.

## Memory

Chat `/memory` offers selected text, completed result, source inspection,
editing and explicit sharing. Saving asks for the destination: current Agent
(task-only or retained), current collaboration shared area, or personal/project
knowledge. Saving does not automatically select a retained entry for future
input; `/memory select` does. `/memory sources` expands actual used sources.

Knowledge is disabled by default. Enable it in `/settings memory` or
`config memory --knowledge`, then explicitly import a workspace text/Markdown
file and select its collection. The Agent retrieves bounded excerpts only when
it calls `knowledge_search`; selecting a collection does not inject all its
documents. There is no automatic history scan, background indexing or hidden
learning. See [Memory](docs/MEMORY.md) for commands and deletion boundaries.

## Boundaries

- File listing, bounded UTF-8 reading, literal search, editing, creation and
  concrete batch changes are restricted to the selected workspace unless allowed
  roots or all non-protected paths are selected. Links, hardlinks, `.git`
  and the harness data directory are excluded.
- Directory listing is sorted and paginated with `next_offset`. File reading
  returns whole UTF-8 characters, including when a requested page is smaller
  than one character. See [file tool limits](docs/CONFIGURATION.md#file-tools).
- Edits require the full SHA-256 returned by `file_read`, show the replacement
  and check for conflicts before publication. Do not concurrently edit the same
  file: this is not a transactional filesystem or hostile-local-process sandbox.
- Batch approval binds the exact targets, original hashes and proposed changes.
  Files are revalidated before dispatch; per-file progress is saved. Partial
  success is not rolled back or automatically replayed.
- Local commands are disabled by default. On Windows they run in an
  AppContainer sandbox: read-only in Ask mode, workspace writes without network
  in Workspace mode. Only Full access runs them on the host as the current user,
  recorded with `host_escape_hatch`. Command rules allow, ask or deny a program and
  its arguments; executable allowlists restrict which path may run, not its
  descendants. Cleanup is best effort, not undo.
- HTTP GET is disabled by default, origin-allowlisted and approved per URL.
  Remote targets require HTTPS and public addresses; explicitly configured
  loopback origins may use HTTP. No proxy, cookies, credentials, redirects or
  retry are used. GET and local endpoints may still have effects.
- Step, cumulative time, context, output and result limits persist across
  continuation. Context trimming removes complete prior runs from model input,
  never the stored history.
- Ordinary task templates and references are explicitly selected. Optional
  task-local recaps retain source IDs/hashes and do not create cross-task memory.
- Interrupted dispatched calls remain `UNKNOWN`. Ordinary continuation and
  duplicate requests never replay them. Abandoning a task retains its unknown
  records and warns later tasks in that workspace.
- Only one owner process writes a data directory. Desktop, `web` and interactive
  `chat` publish the same authenticated loopback API. Interactive `chat`, `run`,
  `continue`, `preview`, `confirm`, `control`, `config`, `key` and `memory` attach
  to an existing owner; `sessions` and `inspect` also work while it is running.
  Use `chat --resume <session>` for shared history. Multiple viewers are allowed;
  conflicting execution is rejected by the owner. Drafts stay in each surface.
  A CLI owner remains in its terminal process; exiting it drains that owner.
- MCP is opt-in trusted local code, **not a sandbox**. It can access anything its
  operating-system account can access. Installation and updates are the user's
  responsibility.
- Runtime evidence separates file changes, commands, HTTP response facts,
  checks that passed, checks not run and uncertainty from model explanations.
- The CLI and a second local caller share one task service. See
  [Local Composition](docs/COMPOSITION.md).
- Default-disabled collaboration adds bounded visible delegation, role
  profiles, independent child budgets, cumulative parent guards, conservative
  recovery and evidence-based merge. It has no recursive swarm or background
  scheduler. See [Collaboration](docs/COLLABORATION.md).
- Private, task-shared and selected knowledge memory have runtime-enforced
  scopes, sources, finite bounds and optimistic revisions. Memory content is
  data, not permission or proof that a check passed.

## Development

```text
go mod verify
go test -count=1 ./...
go vet ./...
go test -race -count=1 ./...
pnpm --dir internal/chatui test
pnpm --dir internal/chatui build
scripts/update-notices.ps1 -Check
```

Race testing requires a supported C compiler. Live acceptance is opt-in and
sends real model requests that edit only a temporary fixture workspace.
`scripts/run-live-acceptance-secure.ps1` reads the key with hidden input,
keeps it in the test process only and runs every `TestLiveProvider` test;
add `-Archive <portable zip>` to check the packaged CLI with the same key.
The tests read `FREEAGENT_LIVE_ENDPOINT`, `FREEAGENT_LIVE_MODEL`,
`FREEAGENT_LIVE_RESPONSES_MODEL` and `FREEAGENT_LIVE_KEY_ENV`.

Read [Configuration](docs/CONFIGURATION.md), [Recovery](docs/RECOVERY.md),
[Provider Contract](docs/PROVIDER_CONTRACT.md),
[v7.0 progress](docs/FREEAGENT_V7_0_IMPLEMENTATION_PROGRESS.md), [Security](SECURITY.md) and
[Contributing](CONTRIBUTING.md). Chinese overview: [简体中文](README.zh-CN.md).

Licensed under AGPL-3.0-only. See [LICENSE](LICENSE) and
[Third-party notices](THIRD_PARTY_NOTICES.md).
