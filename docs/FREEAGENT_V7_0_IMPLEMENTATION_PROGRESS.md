# FreeAgent v7.0 Implementation Record

v7.0 passed its acceptance checks on 2026-10-21 and was published as the
`v7.0.0-dev.1` pre-release. This file summarizes what v7.0 delivers, what was
cut, the evidence behind the acceptance and the known limits. The full
checkpoint log it replaces is in the Git history of this file.

## What v7.0 Delivers

- One task core and SQLite store shared by the desktop Chat and the CLI. The
  owner publishes `engine.json`, an observation `attach.secret` and a separate
  interactive Chat secret. `run`, `continue`, `preview`, `confirm`, `control`,
  `config`, `key`, `memory` and `collab` attach to a running owner instead of
  opening a second writer; events are generation scoped.
- A typed control service with a bounded journal, resource revisions, redacted
  state, plan/apply/reject/revert and stale-revision rejection. GUI settings,
  CLI `config` and `control`, and the AI conversation all use it.
- Model-proposed settings changes reuse `waiting_confirmation`, Preview and
  Confirm. They need a ten-character owner-held code with expiry and bounded
  retries: the Chat card shows it, the CLI prompt prints it. The code is bound
  to owner generation, conversation and operation, not to the surface.
- Ask / Workspace / Full execution profiles with file scope, additional roots,
  blocked paths and network policy, frozen in each Run snapshot.
- `command_run` in a Windows AppContainer through a `CreateProcessW` launcher
  with Job-before-resume ordering, explicit grants, protected `.freeagent`,
  `.git` and data paths, kept grants per owner session and an opt-in
  `git_write`. Full access runs commands on the host and records
  `host_escape_hatch`. Non-Windows builds use `os/exec` with process-group
  cleanup and have no command sandbox.
- Key profiles in `keys.json`, DPAPI-protected with an embedded
  origin/protocol/profile binding; probes and model discovery run owner-side.
- OpenAI-compatible, Anthropic and Responses adapters, including DeepSeek
  server-side `web_search_call` metadata and stateless continuation.
- MCP, Skill and Extension lifecycle through typed operations; MCP payloads
  stay in owner memory and the journal stores safe metadata only.
- Current-only `Harness-v5` data directory and `freeagent-harness-v7` store
  format. Older stores are rejected, never migrated.

## Scope Decisions

Removed after the October 19 over-design review: typed chat confirmation, a
host-gate counter, Job CPU/memory/process limits, sandbox readiness states, the
multi-tool compatibility probe, the grant ledger and cleanup command,
content-hash trust for Skills and Extensions, `external_control_change` on
receipts, and gitdir or link resolution for the overlap check. Generic
lifecycle hooks were dropped. Per-root access records for additional roots
were removed on October 21.

Moved to later versions: durable compaction (v7.1); `apply_patch`, timeout and
truncation standardization, Provider fixtures and diagnostics (v7.2);
background command sessions after that.

## Problems Found During Acceptance

- Explorer passes hidden `=C:=C:\...` environment entries. The launcher
  rejected them, so a double-clicked package failed before the engine
  started. Names are now parsed with Windows rules, and the startup script
  injects these entries.
- Model proposals could not be confirmed: the Chat card sent no code and the
  CLI never printed one. Both now render the owner code; a Run-bound proposal
  is confirmed only through its Run.
- Codes were bound to the rendering surface, so a second surface silently
  replaced the code already shown.
- Sandbox fixes: workspace-only Runs no longer grant additional roots,
  protected paths use a protected DACL instead of inheritable deny ACEs,
  ending a Run withdraws its pending proposal, and the catalog no longer
  claims the sandbox is missing.
- DeepSeek reports searches as `action.queries` and page opens as `open_page`
  with a `url`; search metadata now records both.
- A cancelled MCP write could stall Close for 30 seconds; stdin is now closed
  in the background after the terminate sequence.
- Linux CI: pipe and process-group cleanup errors in the non-Windows launcher,
  and a Windows-only path test.
- A real data race: control operations replaced the shared core's `Config`
  without ordering against Chat reads. A shared `configMu` now guards it.
- GitHub Actions on Windows: Chat tests compared an 8.3 short `TEMP` path, and
  the 2-second extension handshake could not cover a cold Python start; the
  handshake now allows at least 10 seconds.

## Acceptance Evidence (2026-10-21)

```text
GitHub Actions windows-latest / ubuntu-latest      PASS (commit 4778267)
Windows and Linux: go vet, go test, go test -race  PASS (9 packages, 0 DATA RACE)
pnpm test / pnpm build (internal/chatui)           62/62 PASS
update-notices.ps1 -Check                          PASS
go test ./internal/winlaunch -v (native sandbox)   PASS
scripts/test-portable.ps1                          PASS (18 checks)
packaged doctor sandbox check                      PASS (status ready)
```

Live DeepSeek checks (user-run hidden-input script; Chat model
`deepseek-flash`, Responses model `deepseek-v4-pro`): collaboration, tools with
approval, Chat JSON/SSE, tool error and approval decline, controlled
interruptions keeping UNKNOWN, Responses JSON/SSE, web search with
continuation, Responses tools with restart replay, and the packaged CLI
(read, approve, decline, search, doctor) all passed, with no credential in
output or files.

Manual checks reported by the user: the desktop workspace switch through the
native folder picker and the control confirmation card.

## Known Limits

- Only DeepSeek has live acceptance. The other Key presets are untested.
- The first sandboxed command of a session applies root grants: about 0.42 s
  for 3,000 entries measured, about 2.5 s estimated for 17,400.
- A Scoop shim fails inside the sandbox because its real program is outside
  the grant; point the command at the real executable.
- `.git/config` is readable by sandboxed commands, including tokens in remote
  URLs. Content a sandboxed command writes is untrusted input for later host
  tools; creating `.git` sets `git_metadata_created` on the receipt.
- Interactive `chat` does not attach to a running owner.
- The package is unsigned, without installer or auto-update.

## v7.0.1

- The Key form keeps unsaved input, including the typed key, when the window
  regains focus or the page is refreshed from the owner.
- README, configuration and security docs now describe permission modes, the
  command sandbox and attach behavior.
- `--version` and the extension `host_version` come from `VERSION` through
  `internal/buildinfo`; source builds report `dev`.
- Plan section 11.3 no longer specifies per-root access records.
