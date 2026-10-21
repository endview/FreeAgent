# Configuration

`init` writes `config.json`, `state.sqlite`, `instructions/` and `backups/` in a
new empty data directory, with no models. `owner.lock` protects the sole
writer. It never probes old installations. Configuration version and store
format are current-only; unsupported formats are rejected without migration.
Key profiles are stored outside the data directory in `keys.json`; see
[Key Profiles](#key-profiles).

The initial config is the complete editable example. Important fields:

| Field | Default | Meaning |
| --- | --- | --- |
| `models` | empty | Named local model profiles |
| `default_model` | empty | Profile used when a Chat or Run has no explicit selection; the first added model |
| `default_selection` | absent | Explicit profile or opt-in `auto`; absent uses `default_model` |
| `auto.enabled` | false | Bounded Auto routing; manual model choices always take priority |
| `auto.models` | absent | Authorized configured profiles; absent uses the configured pool |
| `auto.assessment_model` | absent | Optional authorized profile for one structured assessment |
| `auto.max_candidates` | 8 | Finite candidate pool, 1-32 while enabled |
| `auto.max_assessments` | 1 | At most one recorded assessment; 0 disables it |
| `auto.max_upgrades` | 1 | At most two safe-boundary upgrades; 0 disables them |
| `models.<id>.provider` | required | `openai-compatible`, or `echo` for offline tests |
| `models.<id>.key` | absent | Key profile id; the address and key live in `keys.json` |
| `models.<id>.model` | absent | Explicit provider model name |
| `models.<id>.capabilities` | absent | Local protocol, streaming, tool, web-search, context, output and strength declaration |
| `workspace` | selected | Absolute existing directory; linked ancestors rejected; change with `config workspace` |
| `max_steps` | 12 | Total model/tool dispatches in one Run |
| `max_messages` | 40 | Historical messages sent, excluding system message |
| `max_context_bytes` | 131072 | Serialized request plus four bytes per reserved output token |
| `max_output_tokens` | 4096 | Model output request limit |
| `max_run_seconds` | 300 | Cumulative active execution time across continuation; approval waiting is excluded |
| `max_tool_result_bytes` | 65536 | Persisted tool output bound |
| `system_prompt` | supplied | User instructions |
| `instructions` | absent | Explicit filenames under `instructions/` |
| `mcpServers` | absent | Optional trusted local programs |
| `commands.enabled` | false | Explicit approved local command execution |
| `commands.allowed_executables` | absent | Optional exact absolute-path policy |
| `http.enabled` | false | Explicit approved HTTP GET |
| `http.allowed_origins` | absent | Exact origins; required when HTTP is enabled |
| `task_summary_bytes` | 0 | Optional task-local recap; 0 disables |
| `collaboration.mode` | `off` | `off`, `ask` or `auto`; optional foreground collaboration |
| `collaboration.limits` | finite | Parent cumulative children/calls/output/concurrency/active seconds |
| `collaboration.child_limits` | finite | Per-expert steps, active seconds and output bounds |
| `collaboration.models/tools/paths` | absent | Authorized configured models and explicit expert tool/file scope |
| `collaboration.max_replans` | 1 | Finite automatic revision cap; 0 disables replanning |
| `collaboration.synthesis` | false | Optional guarded, tool-less final synthesis; preserves deterministic expert results and evidence |
| `collaboration.shared_memory` | task-scoped read/append/modify | Explicit role grants, limited to shared entry kinds |
| `memory.knowledge_enabled` | false | Explicit local text/Markdown import and selected search |
| `memory.max_entries` | 16 | Selected private entries and memory-tool listing bound, 1-64 |
| `memory.max_context_bytes` | 16384 | Selected memory input and search-result bound, 1024-65536 bytes |
| `memory.max_document_bytes` | 65536 | Explicit save/import body bound, 1024-65536 bytes |

The context budget is a byte bound with an approximate output reservation, not
a provider tokenizer or billing quota. Original history is never deleted by
trimming. Instructions are at most 16 simple filenames, each bounded UTF-8 text
of at most 32 KiB. They cannot enable tools or grant permissions.

HTTPS is required for remote addresses; plain HTTP is limited to loopback.
The adapter handles the tested function-call Chat Completions shape, not every
API described as compatible. SSE calls execute only after a completed stream.
Provider usage is stored when supplied; missing usage is not invented.

## Key Profiles

```text
freeagent key add --name main --url https://your-provider.example/v1
freeagent key add --preset deepseek --key-env DEEPSEEK_API_KEY
freeagent key list [--json]
freeagent key edit main --key-prompt
freeagent key test main
freeagent key models main
freeagent key remove main --data-dir <data> [--force | --disable-models [--new-default <id>]]
```

A key profile has an id, name, base address, optional note and one key source:
`saved` (entered with hidden input or `--key-stdin`), `env` (a process variable
named with `--key-env`, resolved before each dispatch) or `none` (loopback
addresses only). Keys are never accepted as command arguments. On Windows a
saved key is encrypted with DPAPI for the current user; another account or
machine must enter it again with `key edit <id|name> --key-prompt`.

The address may be a service root, a `/v1` address or a complete
`/chat/completions` or `/responses` URL; it is normalized to the base used for
`<base>/chat/completions`, `<base>/responses` and `<base>/models`. Queries,
fragments and user information are rejected.

`keys.json` is shared by every data directory of the same user:
`os.UserConfigDir()/FreeAgent/keys.json`, or `data/keys.json` inside a portable
package. `FREEAGENT_KEYS_FILE` selects another file. Writers serialize through
`keys.json.lock`. `key list` and `doctor` show the source, environment
variable name and a short hint, never the key. `doctor` reports the
keys file, whether saved keys are encrypted and each profile's last check; an
unreadable keys file makes it report `ok: false` and exit nonzero.

`key test` sends one `GET <base>/models` and records the result category (`ok`,
`auth`, `not_found`, `rate_limited`, `server`, network failures and so on) as
the profile's last check. `key models` sends the same request and lists the
reported model names. These two commands are the only configuration commands
that make network requests. A passing check does not prove chat or tool
compatibility.

`key remove` checks the models of the data directory it is given and refuses
while any of them use the key. `--disable-models` removes those models in the
same step; `--force` keeps them, and they cannot dispatch until they are
reconfigured with another key. Other data directories are not checked.

## Model Selection

After initialization, use these commands on the same data directory:

```text
freeagent config show --data-dir <data-directory>
freeagent config model --data-dir <data-directory> --id strong --default --key main --model <name>
freeagent config model --data-dir <data-directory> --id offline --provider echo
freeagent config model --data-dir <data-directory> --id offline --default
freeagent config model --data-dir <data-directory> --id strong --remove [--new-default <id>]
freeagent config workspace --data-dir <data-directory> --path <directory>
freeagent run --data-dir <data-directory> --model strong <task>
freeagent continue --data-dir <data-directory> --run <id> --model offline
```

`config show` returns a JSON summary of the model pool, default, paths, limits, selected
instruction names and enabled MCP server names. It omits instruction content,
the system prompt, MCP arguments and environment values. It is read-only and
can run while chat owns the data directory.

`config model` replaces one named model profile, preserving conversations,
workspace, limits, instructions and MCP settings. Use `--default` when the
profile should become the default for new or otherwise unselected work; the
first model added becomes the default automatically. `--key` names a key
profile by id or name and implies `openai-compatible`; it stores only the key
profile id. `--model` is required with it. `--provider echo` is for offline
tests and cannot be combined with `--key` or `--model`. `--id <name> --default`
without other flags only switches the default.

`--remove` accepts only `--id` and `--new-default`. It deletes the profile and
its references in Auto and collaboration settings. Removing the default while
other models remain requires `--new-default`. Tasks exit with code 2 when no
model is configured.

`config workspace --path` changes the workspace. Like other safety settings, it
is refused while any Run is running, waiting for confirmation, stopped,
unknown or limited; finish or abandon those Runs first.
A Chat can select a profile with `/model`; `run --model` selects only that Run.

The Windows desktop uses the folder button beside the current workspace to
request this change. It closes and restarts the local engine and reloads the
page. The chat list is filtered by the selected workspace: chats from the
previous workspace disappear during the switch and reappear when that
workspace is selected again. If the engine cannot reuse its previous port,
the local web origin changes; unsent drafts stored by the browser may then be
unavailable because browser storage is origin-scoped.

Stopping a Run only stops further execution. It does not undo file changes,
commands, requests, or other effects that already happened. The saved record
remains the authority and can be `stopped` or `unknown`; inspect its activity
and evidence before deciding what to do next.

Neither command makes network requests, starts MCP servers or reads credential
values. Keys are resolved immediately before each dispatch. The output reports
`network_tested: false` and
`credentials_resolved: false`; success does not prove provider compatibility.

While the desktop app or `web` owns the data directory, profile edits attach to
that owner and use the same typed control operations as the GUI; `config show`
shows the owner's redacted view. Without an owner they open the directory
directly. Interactive `chat` still needs the directory to itself.
Unfinished runs do not block adding profiles or changing the default. Their
saved bindings are not rewritten; changing a bound profile stops the next
dispatch until an explicit safe-boundary switch adopts the new revision.
A Chat selection affects subsequent tasks. `continue --model` is allowed only at a
complete model/tool boundary and never makes UNKNOWN replayable. Pending
confirmation, stopped, unknown and limited runs retain their saved model and
must be resolved according to [Recovery](RECOVERY.md). Capabilities are local
declarations, not provider certifications: `--protocol chat|responses`,
`--tools supported|none`, `--web-search supported|none`,
`--context-bytes`, `--output-tokens` and `--strength light|standard|strong`.
Zero context/output bounds inherit the finite Harness limits. A tool-less
model receives no tools and rejects incompatible tool history rather than
silently dropping half a tool group.

## DeepSeek Responses And Search

DeepSeek server-side search is not the same as FreeAgent's optional local HTTP
tool. It does not require enabling `http` or `command_run`. Configure an API
key, select the Responses protocol, and explicitly declare web search for a
model that supports it:

```powershell
freeagent config model --data-dir <data> --id deepseek-pro --default `
  --key deepseek --model deepseek-v4-pro --protocol responses `
  --web-search supported
```

The DeepSeek Responses endpoint is derived as `<base>/responses`; with the
official base URL this is `https://api.deepseek.com/responses`. The built-in
`web_search` tool is sent only when `--web-search supported` is configured.
DeepSeek documents `deepseek-v4-pro` as the model that performs server-side
search; `deepseek-flash` may accept the request but ignores that tool. Search
events and `web_search_call` output items are shown as provider metadata and
are saved for stateless continuation. Search results do not grant FreeAgent
local file, command or HTTP permissions.

## Optional Auto

```text
freeagent config auto --data-dir <data> --enabled --allow-model strong --allow-model light
freeagent run --data-dir <data> --model auto <task>
freeagent config auto --data-dir <data> --enabled --default --assessment-model strong --allow-model strong --allow-model light
freeagent config auto --data-dir <data> --enabled=false
```

`/model auto` selects Auto for later Chat tasks. Explicit manual Chat choices
and Run overrides skip it. `config model --default` replaces an Auto default
only when the user explicitly requests that change. Auto does not discover
providers, resolve credentials during configuration or alter tool authority.

Lightweight rules consider explicit reasoning/risk/tool needs. Uncertain
intent keeps the stronger suitable authorized profile. Input length or domain
count alone is not a complexity rule. Filtering uses the actual serialized
request, tool definitions, output reservation and complete saved message
groups, not just the prompt. Capability declarations are local information.

An explicitly configured assessment makes at most one tool-less structured
model call for uncertain intent. The call consumes a Run step and any parent
cumulative model/output allowance; its response stays in Attempts rather
than Chat history. Invalid JSON, unconfigured models and proposed policy
changes do not override the conservative choice. UNKNOWN is never retried.
Safe capability/context upgrades are finite, never revisit a binding or
reset consumption. `Routing` records chosen profiles, revisions and short
reasons, not private model reasoning.

Disabling Auto or restricting its pool/bounds stops later assessments and
upgrades for that Run, durably retaining its saved model as manual selection.
It does not redirect an already dispatched call or grant permission to replay.
An explicit `continue --model` also disables Auto for that Run. Local fixture
acceptance does not certify real model choice quality, latency or savings.

## File Tools

All paths are relative to the selected workspace. Tools exclude `.git` and the
Harness data directory and do not follow links. Reads and edits reject
hardlinked, binary, NUL-containing and non-UTF-8 files.

| Tool | Arguments and limits |
| --- | --- |
| `file_list` | `path`; optional `offset` (default 0) and `max_entries` (default 100, range 1-200). Directories of more than 10,000 entries are rejected. |
| `file_read` | `path`; optional byte `offset` (default 0) and `max_bytes` (default 4096, range 1-32768). Whole file must be at most 256 KiB. |
| `file_search` | `path` and literal `query` (1-4096 bytes); at most 100 matching lines, with bounded traversal. |
| `file_edit` | `path`, `expected_sha256`, `old_text` and `new_text`; exactly one replacement in an existing file, with confirmation and conflict checking. |
| `file_create` | `path`, `content`; absent target in an existing directory; exact confirmation, never overwrites. |
| `file_batch_edit` | `changes`, 1-32 exact edits/creations. Each entry selects `operation`, `path` and edit/create arguments. All targets are revalidated; per-file progress is saved. Partial success is neither rolled back nor replayed. |

Listing returns sorted visible `entries`, `offset`, `next_offset` and
`truncated`. Pass `next_offset` to fetch the next page. Restart at offset zero
if the directory changes; pagination is not a directory snapshot.

Reading returns `content`, the actual byte `offset`, `next_offset`,
`truncated` and the full-file `sha256`. An offset inside a UTF-8 character
rounds back to that character's start. Pages contain whole characters and
return at least one character when data remains, so a tiny `max_bytes` may be
exceeded by up to three bytes. Reassemble using returned offsets; reread from
zero if the hash changes between pages.

Omit optional arguments to use defaults; explicit `null` and zero page sizes
are errors. An empty `new_text` deletes the matched text, but `null` does not.

## Optional Commands

```text
freeagent config commands --data-dir <data> --enabled --allow <absolute-executable> --timeout 120 --output-bytes 32768 [--inherit-environment] [--git-write]
freeagent config commands --data-dir <data> --enabled=false
```

On Windows, `command_run` runs in an AppContainer sandbox and the execution
profile decides its access. Ask runs commands read-only and asks for each one.
Workspace lets them write the workspace, and selected roots when the file scope
includes them, with network off and without asking. Full access runs them on
the host as the current user; the receipt records `host_escape_hatch`. Builds
without the sandbox report sandboxed commands as unsupported. Command rules,
edited in Settings or through the `commands.set_policy` control operation, allow,
ask or deny a program with given arguments: `deny` always blocks and `ask` always
asks. `--git-write` lets sandboxed commands write `.git`; `.git` itself, hooks and
config stay protected.

Repeat `--allow` for up to 32 executables. The command selection replaces the
complete command policy; omitting `--allow` selects no path restriction.
An allowlist never grants approval. The tool accepts exact executable/argument
vectors, a relative working directory and optional `check: true`. It launches
no implicit shell or dependency installer. A listed executable could itself
be a shell, install packages, launch children or access outside the workspace;
only explicitly approve arguments you intend to run.

Timeout is 1-600 seconds, output 1024-1048576 bytes, and the task's remaining
time may be shorter. The default environment is PATH, HOME, USERPROFILE,
SystemRoot, WINDIR, TEMP and TMP when present. `--inherit-environment` explicitly
inherits all parent variables and may expose credentials.

Process-tree cleanup is best effort, not undo. Runtime evidence records start,
exit, timeout, interruption, truncation and bounded observed file changes.
An exit-zero check counts as passed only when the finished receipt is not
truncated. Observation is not sole attribution or proof of unchanged effects.
Paths are policies, not signatures; do not replace executable contents during a
task. Executable spelling in the call must exactly match the configured path.

## Optional HTTP GET

```text
freeagent config http --data-dir <data> --enabled --origin https://docs.example --origin http://127.0.0.1:8080 --timeout 20 --output-bytes 32768
freeagent config http --data-dir <data> --enabled=false
```

Origins are canonical lowercase `scheme://host[:port]`, without path, query,
fragment, credentials or trailing slash. Repeat `--origin` for up to 16
origins. Explicit ports distinguish origins, including default ports.
This command replaces the complete HTTP selection and does not probe any URL.
`tools list` also lists selected commands/HTTP offline, without starting MCP.

`http_get` accepts only `{"url":"<complete-url>"}`. Outside Full access, every exact
URL requires confirmation, including queries. Remote targets require HTTPS and public
addresses; explicitly selected localhost/loopback origins may use HTTP.
DNS answers are checked together and the first approved address is pinned for
the request. There is no address fallback, redirect, proxy, retry, cookie jar,
arbitrary header or inherited credential. Special-purpose address ranges are
conservatively rejected, even when some are otherwise routable.

The timeout is 1-60 seconds and response-body limit is 1024-65536 bytes.
Response headers are bounded. Only UTF-8 text, JSON and XML are retained;
encoded, unsupported or invalid bodies are omitted, incomplete/oversized
bodies marked truncated. Tool-result/context limits may impose a tighter bound.
The tool returns source URL, received status and bounded body, not rendered
browser content. HTTP errors and redirects are definite responses, not
successful fetches. A missing response is UNKNOWN, never automatically retried.
GET can have effects; origin selection is not a sandbox or correctness proof.
URLs and bodies are stored in history: do not put secrets in URL queries.

## Task Context

`run --template <workspace-file>` explicitly selects an ordinary bounded
UTF-8 template. Chat's `/file <path>` selects source-visible references for
the next task, not background retrieval.

`config context --summary-bytes <0-16384>` explicitly selects a bounded,
in-memory task-local recap. Zero disables it. Original history and complete
tool groups are preserved; source IDs/hashes identify omitted messages.
It is not cross-session memory. See [Local Composition](COMPOSITION.md).

## Explicit Memory And Knowledge

```text
freeagent config memory --data-dir <data> --knowledge
freeagent config memory --data-dir <data> --max-entries 8 --context-bytes 8192 --document-bytes 32768
freeagent config memory --data-dir <data> --knowledge=false
```

Unmentioned memory fields are preserved. Configuration is offline; changing
memory settings requires no unfinished Run. Chat `/settings memory` offers the
same finite bounds and enable switch. No scanning or background model work is
started. The current format is `freeagent-harness-v7`, configuration version
`7`, backup format `freeagent-harness-backup-v3`, with default data directory
`os.UserConfigDir()/FreeAgent/Harness-v5`; older formats are rejected. The
current SQLite store format is `freeagent-harness-v7`.

Private entries are explicitly selected per Chat, or supplied as a task override
by a local caller. Selection fixes input authority in the Run; each model request
freezes and revalidates the actual used revisions before dispatch. Selected
knowledge collections grant a bounded `knowledge_search` tool, not automatic
document injection. Disabling knowledge prevents new retrieval grants.

Memory tools exist only under a saved scope grant. Agents may write task-only
private notes and authorized task-shared entries; retained private entries and
knowledge are user-managed, not implicitly learned. Clearing a Chat selection
affects future tasks, not saved Run grants or ordinary Chat history.
See [Memory](MEMORY.md) for the complete flow and deletion boundary.

## MCP

Add an explicit server to the existing config; do not replace the other fields:

```json
{
  "mcpServers": {
    "local-tool": {
      "enabled": true,
      "command": "C:/Tools/trusted-mcp-server.exe",
      "args": [],
      "cwd": "C:/Work/project",
      "env": {"MODE": "local"},
      "envFrom": {"SERVICE_TOKEN": "FREEAGENT_SERVICE_TOKEN"}
    }
  }
}
```

`command` and `cwd` are absolute. If `cwd` is absent, the workspace is used.
Arguments are separate, never assembled into a shell command. No package
installation or PATH lookup occurs. On startup, enabled servers are started for
handshake/discovery. Disabled servers are not probed and their secrets are not
resolved. Only the configured mapping values plus available `SystemRoot`,
`WINDIR`, `TEMP` and `TMP` are inherited, not the whole parent environment.
Use `env` only for non-sensitive constants.

`tools list` is offline. `tools test <server>` intentionally starts that program
for handshake and discovery but makes no tool calls. It is not supply-chain
certification. The locked SDK and local fixture negotiate tested tool-only
MCP stdio; server-initiated requests/notifications are rejected.

Names are mapped to stable `mcp_<hash>` aliases. Tool descriptions show original
names. Outside Full access, every MCP call requires confirmation regardless of
annotations.
Unsupported schemas are rejected individually; external references, unknown
keywords and `format` are rejected, not weakened. Text and structured results
are supported; unsupported content is reported as an error with possible effects.

## Configuration Changes

Runs freeze a non-secret execution-policy/tool/instruction snapshot plus
model binding history. Editing execution policy or selected instructions stops
the next dispatch. Changing another profile or the default does not rewrite
existing bindings. `continue --model` explicitly adopts a model/revision only
at a safe boundary. `continue --max-steps <n> --max-seconds <n>` increases
finite limits without resetting consumption; Chat `/more` asks before the
same adjustment. Neither operation replays UNKNOWN or grants tool approval.
Do not change executable contents during a task: snapshots are not executable
signatures or a local program sandbox.

## Optional Collaboration

```text
freeagent config collaboration --data-dir <data> --mode off|ask|auto
freeagent config collaboration --data-dir <data> --allow-model strong --allow-tool file_read --allow-path docs
freeagent config collaboration --data-dir <data> --max-children 6 --concurrency 2 --model-calls 40 --tool-calls 40 --output-bytes 524288 --active-seconds 600 --max-replans 1
freeagent run --data-dir <data> --collaboration ask --interactive <goal>
```

Flag edits preserve unmentioned collaboration settings. These are future user
defaults, not a revocation of active work. Configuration is offline and starts
no workers. Chat `/collaboration off|ask|auto` selects the Chat preference;
`/collaboration once` reviews collaboration for only the next task.
Task overrides take priority over Chat preferences, then user defaults.
`/settings` opens the model/collaboration/advanced sections. Advanced selections
can be saved to this Chat or explicitly to future defaults.

Turning the Chat off cancels unfinished controlled work in that Chat, including
planning and single-agent fallback. Saved results, consumed allowances and
UNKNOWN attempts remain. Cancellation does not promise undo. Model suggestions
cannot add tools, widen file scope, introduce endpoints or increase allowances.
Suggestions are reviewed once at the collaboration entry, not for every
expert; consequential tool calls still need exact operation confirmation.
Replanning is finite, authority-preserving and uses guarded ordinary model
Attempts. Parent active seconds measure actual union execution, not all-idle
approval waits. Limit increases are explicit and preserve prior consumption.
Optional synthesis consumes the same parent model/output/time limits, has no
tools and preserves the deterministic expert answer and runtime evidence.
Saved results are reused; UNKNOWN synthesis is not replayed.
See [Collaboration](COLLABORATION.md).
