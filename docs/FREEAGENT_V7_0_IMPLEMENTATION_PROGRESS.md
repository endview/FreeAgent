# FreeAgent v7.0 Implementation Progress

This file records the v7.0 implementation checkpoints. The v7.0 acceptance
checks passed on 2026-10-21; the final section lists the release verification.

## Implemented In This Checkpoint

- Windows `CreateProcessW` launcher with AppContainer capability setup,
  explicit filesystem grants, protected paths and Job-before-resume ordering.
- Shared launcher use by command workers, MCP stdio and the desktop engine.
- Non-Windows `os/exec` fallback with Unix process-group cleanup.
- Current-only `Harness-v5` data directory, `freeagent-harness-v7` store
  format, and the v7 attach protocol boundary.
- Typed owner-side control service with a bounded journal, resource revisions,
  redacted state, plan/apply/reject/revert, exact reversible snapshots and
  stale revision rejection.
- Owner-memory ten-character confirmation codes with expiry, bounded retries,
  interactive capability checks and reissue after owner restart.
- Top-level foreground AI control read/proposal tools. Child, workflow and
  collaboration Runs do not expose them. Mixed control/tool responses fail
  deterministically without dispatch.
- Pending control proposals reuse `waiting_confirmation`, `Preview` and the
  existing tool Attempt. Applied proposals finish with
  `control_change_applied_next_run`.
- GUI control API and CLI `control` surface use the same core service.
- External config/Key/SQLite change detection with explicit review before a
  new control plan or Run.
- Web owner discovery through protected `engine.json`, `attach.secret` and
  separate Chat secret files. Desktop can reuse an existing Web owner instead
  of starting a competing Store writer.
- Authenticated attach client/server handoff:
  - bounded state, chat snapshot, inspect, request status, submit, continue,
    cancel, ordinary tool preview/confirm and graceful handoff;
  - generation-scoped events with explicit `generation_changed` and
    `cursor_expired` results;
  - one-shot lease metadata and owner stopping state;
  - CLI `run`, `continue`, `preview`, `confirm` and `control` attach to an
    existing Web owner instead of opening a second Store writer;
  - duplicate request keys are resolved before dispatch and owner promotion
    after handoff publishes a fresh generation;
  - attach.secret cannot reach ordinary mutation routes or create a control
    proposal; interactive control uses the current Chat key.
- Control catalog now has typed, journaled, revision-checked operations for
  model profiles/removal, Auto, HTTP, memory, collaboration, finite Run
  limits, Skill policy, Extension policy and safe MCP metadata in addition to
  execution and conversation settings.
- Saved Key payloads now contain a versioned profile/origin/protocol binding
  inside the protected envelope; mismatches require re-entry before dispatch.
- Saved Key model discovery and connection checks now use a typed, owner-side
  `keys.probe` operation. The bounded probe result is returned separately from
  the journal record, while `LastCheck` is written by the owner.
- CLI Key add/edit/remove/test/models now attach to an interactive owner when
  one exists. A regression covers the real Web owner path, including the
  secret-bearing add request, model discovery and removal without secret
  output. The no-owner one-shot path remains available for bootstrap use.
- MCP add/update/delete/import/restart/trust changes now use typed owner
  operations. Full MCP payloads are held in owner memory during apply and the
  control journal stores only safe profile metadata.
- Skill import/delete and Extension import/delete/restart/data-clear actions
  now reuse typed lifecycle operations. Extension settings and environment
  mappings use the existing typed Extension policy operation; model proposals
  cannot submit those user-only fields.
- Conversation capability selection and persistent memory selection now use
  revision-checked control operations. Existing memory entry receipts and
  exact content confirmation remain the content mutation boundary.

## Verification

## Latest Continuation: October 19, 2026

This continuation added and verified:

- CLI `config show` uses the owner's redacted diagnostic projection when an
  interactive owner is present. Every mutating `config` subcommand now detects
  an existing owner before opening the store and submits a typed control plan;
  the local no-owner path remains available for one-shot CLI use.
- A regression test starts a real loopback owner, runs CLI model configuration
  through attach, confirms the rendered short code, checks the updated owner
  state and verifies an `applied` control-journal entry.
- Anthropic Key metadata updates now normalize the address with the existing
  protocol when `--protocol` is omitted. The saved envelope remains resolvable.
- GUI settings, legacy command-policy endpoints, global execution-profile
  endpoints, conversation title/model/pin/permission endpoints, and model
  profile/default/remove endpoints use typed control operations. The complete
  execution profile operation keeps permission, file scope and network changes
  in one revision and one runtime-generation update.
- A control-catalog family coverage test now fails when a current mutable
  configuration family has no typed operation; full field-level coverage is
  still a remaining gate for newly added lifecycle payloads and derived
  persistent fields.
- The current docs, VERSION, portable metadata and settings reference now say
  `v7.0.0-dev.1`, `Harness-v5` and `freeagent-harness-v7` consistently.

Verification performed after this continuation:

```text
go test ./... -count=1 -timeout=300s       PASS
go vet ./...                               PASS
gofmt -l cmd internal examples extension-kit NO_OUTPUT
git diff --check                           PASS
pnpm test                                  59/59 PASS
pnpm run build                             PASS
scripts/build-portable.ps1                 PASS (v7.0.0-dev.1 archive)
scripts/test-portable.ps1                  PASS (all reported checks)
scripts/test-desktop-startup.ps1           PASS
```

Additional control-lifecycle continuation verification:

```text
go test ./... -count=1 -timeout=300s                                  PASS
go test ./cmd/freeagent -run TestCLIKeyMutationsAttachToExistingWebOwner PASS
go test ./internal/chatui -run 'TestMCPAPI|TestSkillsAPI|TestExtensionsAPI' PASS
go test ./internal/harness -run 'TestControl|TestKeyProbe|TestChatMemory' PASS
pnpm test (internal/chatui)                                           59/59 PASS
pnpm run build                                                        PASS
scripts/test-portable.ps1                                            PASS
scripts/test-desktop-startup.ps1                                     PASS
```

The added tests cover owner-side Key probe results, CLI Key attach mutation,
MCP profile journal redaction, typed Skill/Extension lifecycle calls, and
conversation capability/memory selection through the control service. This is
not a v7.0 completion claim; the native race, live Provider and remaining
sandbox acceptance gates below are still open.

The previous package from the earlier checkpoint was:

```text
bin/portable/20261019-072438-2a78c147/FreeAgent-v7.0.0-dev.1-windows-amd64-portable.zip
SHA-256: 047afaed0e8323a9f29ad1040be3f5448fb75a6e9b8058ece1caa14223916ef2
```

The latest package after the control-lifecycle continuation is:

```text
bin/portable/20261019-082927-3ba06c4d/FreeAgent-v7.0.0-dev.1-windows-amd64-portable.zip
SHA-256: 3f94f39c5d641f9dfa169616338d2fe3895b47ee339d37f738c88ea6fb5c8690
```

The native race attempt was made explicitly and could not build:
`CGO_ENABLED=1 go test -race ./internal/harness` returned
`cgo: C compiler "gcc" not found`. This remains `NOT_RUN`. No live Provider
request was dispatched in this checkpoint. A manual WebView2 inspection of
the exact latest portable package did run: the FreeAgent window loaded the
embedded Chat page, reported `已连接`, displayed the extracted workspace, and
exposed the Chat controls including Key management, settings, conversation
reader and layout handles. No blank WebView or startup error was observed.

Ran successfully on the native Windows workspace:

```text
go test ./...
go vet ./...       (clean)
```

Also compiled Linux test binaries for `internal/harness` and `internal/chatui`,
and compiled the Windows desktop test binary.

`go test -race` is currently **NOT_RUN** because the workspace Go runtime has
`CGO_ENABLED=0` and no `gcc` is available when CGO is enabled.

Additional native Windows evidence in this checkpoint:

```text
go test ./internal/winlaunch -count=1 -v
go test ./cmd/freeagent-desktop -count=1 -v
```

Both passed, including AppContainer identity, workspace grant, private-path
isolation, protected placeholder, Job cleanup, filtered host environment and
desktop engine lifecycle checks. A full `go test ./...` run passed after the
attach changes; the later control-catalog and Key-envelope edits have passed
their targeted Harness, CLI and handoff suites and require one final full
regression before release.

The expanded launcher matrix also passes natively:

- AppContainer `NetworkInternet` capability launch;
- junction/reparse grant rejection;
- two concurrent AppContainer workers with sibling-workspace isolation;
- AppContainer network-off rejection of an unexempt loopback connection;
- a 3,001-entry grant-root reparse scan measured at about 201 ms in this
  workspace. This is evidence for the current synchronous grant cost, not a
  universal performance guarantee.

## Latest Continuation: October 19, 2026 (control and final package)

This continuation added and verified:

- local no-owner CLI `config` mutations now use the same typed control service,
  journal, revision checks and confirmation path as an attached owner; `init`
  remains the only direct bootstrap write;
- nested persistent configuration catalog coverage, removal of the unused
  `allow_host_explicit` field, and exact runtime-limit revert coverage;
- conversation permission selections with roots, blocked paths and command
  selection now use `conversation.set_permission`, including exact revert to a
  prior global-default state;
- future-only control changes may be applied while a Run is waiting for an
  ordinary tool approval; continuation uses the Run's frozen snapshot, while
  external direct edits still fail the control-change check;
- node_modules-shaped grant acceptance now exercises an actual AppContainer
  launch, not only the reparse scan. The latest native measurement was about
  1.52 seconds for 2,001 directories and 2,000 files.

Final source verification for this continuation:

```text
go test ./... -count=1 -timeout=300s       PASS
go vet ./...                               PASS
gofmt -l cmd internal examples extension-kit NO_OUTPUT
git diff --check                           PASS
pnpm test (internal/chatui)                59/59 PASS
pnpm run build                             PASS
go test ./internal/winlaunch -count=1 -v   PASS
go test ./cmd/freeagent-desktop -count=1 -v PASS
scripts/test-portable.ps1                  PASS
scripts/test-desktop-startup.ps1           PASS
```

The exact final package used for packaged acceptance is:

```text
bin/portable/20261019-093847-27450be2/FreeAgent-v7.0.0-dev.1-windows-amd64-portable.zip
SHA-256: a4810ee939afac74570235b59fc2c8753daea3b9e3b3143fe12064d573acea99
```

The final package passed fresh desktop launch, desktop reopen, engine-path,
HTTP, clean close, portable initialization, key encryption, extracted-binary
smoke and embedded Chat acceptance. A manual WebView2 check against the same
package showed `已连接`, the exact package workspace and the reader, settings,
Key management and layout controls. The native folder picker opened from the
workspace button, but the Computer Use input action returned an unknown result
and the workspace was not changed. This manual workspace-switch item is
**UNKNOWN/NOT_RUN**, not a pass.

## Remaining v7.0 Gates

This list was corrected after the October 19 plan audit and reduced after the
over-design review the same day. Items the review removed are listed in
"Fix: October 19, 2026 (scope reduction and sandbox)" and are not gates.

- Complete the desktop workspace-switch manual attach check on the exact final
  package. The control route and desktop path are implemented; the native
  folder picker interaction remains UNKNOWN/NOT_RUN.
  October 21: reported working by the user on package `20261019-235038-fa8e10cc`.
- Key add/edit/remove/probe remain explicit no-owner bootstrap operations when
  no owner exists. Initialized `config`, memory and collaboration commands no
  longer open a competing Store writer: memory and collaboration now use the
  interactive owner attach API when discovery is present.
- Add live Responses/search Provider acceptance for this exact v7 package.
  It needs a Key from the user.
  October 21: PASS; see "Continuation: October 21, 2026".
- Run race tests on a machine with a working C compiler. The current machine
  still reports `cgo: C compiler "gcc" not found`, so `go test -race` is
  `NOT_RUN`, not a pass.
  October 21: PASS after installing gcc; see "Race run: October 21, 2026".
- Manually check the control card in the packaged desktop window. It is
  covered by tests only.
  October 21: reported working by the user.
- Additional roots are still plain path strings, not the per-root records in
  plan section 11.3. Their grant ID and grant status fields belonged to the
  removed grant ledger, so this item is a candidate for removal rather than
  implementation.

## Latest Continuation: October 19, 2026 (Memory/Collaboration attach)

This continuation added:

- owner-side `MemoryCommand` execution for the existing list/read/search,
  save/update/delete, import/copy, selection, agent and source operations;
- owner-side `CollaborationCommand` execution for create, inspect, continue,
  controls, revisions, plan edits, limits, conflict resolution and child
  preview/confirmation;
- interactive-key enforcement for attached memory and collaboration management;
  attach.secret-only clients remain unable to perform these persistent actions;
- owner activity tracking for attached collaboration `continue` and child
  `confirm`, so they participate in cancellation and handoff instead of
  running beside another foreground task;
- regression tests proving memory mutation and collaboration inspection use an
  existing owner without opening a second Store writer.

Verification after this continuation:

```text
go test -count=1 -timeout=300s ./...       PASS
go vet ./...                               PASS
gofmt -l cmd internal examples extension-kit NO_OUTPUT
git diff --check                           PASS
pnpm test (internal/chatui)                59/59 PASS
pnpm run build (internal/chatui)           PASS
scripts/test-portable.ps1                  PASS (all reported checks)
```

The current source package is:

```text
bin/portable/20261019-105429-b5b7238b/FreeAgent-v7.0.0-dev.1-windows-amd64-portable.zip
SHA-256: c311268376c1e5d660f561c26ca34764c12f391acf8331fa724663acad1fd9c9
```

The packaged acceptance reported integrity, no bundled state, PE subsystem,
fresh desktop launch, desktop reopen, engine path, desktop HTTP, clean close,
portable initialization, encrypted Key storage, invalid manifest refusal,
version checks, embedded Chat and extracted CLI smoke as passing. A native
Computer Use binding was unavailable in this environment for the final manual
folder-picker interaction, so workspace switching remains
`UNKNOWN/NOT_RUN`. No live Provider request was dispatched. The native race
attempt remains `NOT_RUN` because no `gcc` compiler is installed.

## Fix: October 19, 2026 (Explorer launch environment)

Package `20261019-105429-b5b7238b` is superseded. Double-clicking its
`FreeAgent.exe` failed with `environment must contain KEY=VALUE entries`
before the engine started, leaving an empty `engine.log`.

Cause: a desktop process started by Explorer inherits hidden per-drive
entries such as `=C:=C:\...` and `=::=::\`. The desktop forwards
`os.Environ()` to the engine launcher, and `winlaunch` treated the leading
`=` as an empty name and rejected the whole environment. The startup script
and unit tests launched from PowerShell, whose children do not carry these
entries, so the scripted acceptance passed while real double-click failed.
The same rejection applied to AppContainer `command_run`, which
deliberately forwards the hidden entries.

Changes:

- `winlaunch` parses environment names with Windows rules: a leading `=`
  belongs to the name and the separator is the next `=`. Validation and the
  environment block share that parser.
- Duplicate detection is case-insensitive and the block is sorted by name
  without regard to case, as `CreateProcess` expects.
- The extension host replaces an inherited variable when a manifest names it
  with different case, so the stricter duplicate check cannot reject it.
- Regression tests: `TestEnvironmentKey`,
  `TestEnvironmentBlockOrderAndDuplicates`,
  `TestHostLaunchAcceptsHiddenDriveEnvironment`,
  `TestStartEngineWithHiddenDriveEnvironment`; the AppContainer launch test
  now always includes hidden drive entries.
- `scripts/test-desktop-startup.ps1` injects `=C:` and `=::` into the
  desktop process. Against a fresh extract of the superseded package it now
  fails (`Desktop did not initialize configuration`), so it reproduces the
  double-click condition.

Verification:

```text
go test -count=1 -timeout=300s ./...       PASS
go vet ./...                               PASS
gofmt -l (changed packages)                NO_OUTPUT
git diff --check                           PASS
scripts/test-portable.ps1                  PASS (all reported checks)
Explorer launch of extracted package       PASS (parent explorer, engine
                                           child, one loopback listener,
                                           HTTP 200, engine.log written)
go test -race                              NOT_RUN (no C compiler)
```

The current source package is:

```text
bin/portable/20261019-183624-55c8897a/FreeAgent-v7.0.0-dev.1-windows-amd64-portable.zip
SHA-256: a6b28336ef05ccfec85f60b5a1910785c70109be4a5516aa04db638b2103e0be
```

Open gates are unchanged: native folder-picker workspace switching
`UNKNOWN/NOT_RUN`, live Responses/search Provider acceptance `NOT_RUN`, and
`go test -race` `NOT_RUN`.

## Fix: October 19, 2026 (model control proposal confirmation)

Package `20261019-183624-55c8897a` is superseded. The plan audit found that
a setting change proposed by the model could not be confirmed from either
surface:

1. The Chat UI card sent no confirmation code, and the core requires one for
   `model_proposed` changes, so the Run stayed `awaiting_approval`.
2. The CLI asked for a code it never printed. An attached `run --interactive`
   showed `[y/N]` and then failed on the control proposal; `preview` and
   `confirm` treated the proposal as an ordinary tool call.

Core changes:

- A Run-bound proposal can only be applied through its Run
  (`TaskService.ConfirmControl`). The direct control route returns
  `ErrControlRunConfirmation` (`control_run`, HTTP 409) instead of applying
  the change and leaving the Run waiting forever.
- `StopReasonControlProposal` names the waiting reason so surfaces can tell a
  setting change from an ordinary approval.

Chat UI changes:

- The card for a control proposal fetches the owner-rendered code and shows
  the kind, risk, preview, grouped code and expiry. Apply stays disabled until
  the code belongs to the shown operation. The code is sent with the decision
  and never enters the transcript.
- `/api/attach/confirm` accepts the code only from the interactive Chat key;
  an `attach.secret` client still gets `user_interaction_required`.

CLI changes:

- `run --interactive`, `continue --interactive` and `chat` show the
  operation, preview and `Confirmation code: XXXXX-XXXXX (valid until HH:MM)`.
  `y` or the code applies, Enter or `n` declines, and a wrong code is asked
  again up to three times. This works locally and when attached to an owner.
- `preview` labels a setting change. When attached, it shows the owner's code
  and a `confirm --code` command. Without an owner it explains that
  `confirm --approve` shows a code, because a code cannot outlive the process
  that rendered it.
- `confirm` takes `--code`. Without it, `--approve` shows the code and asks
  on stdin. A local `--code` is refused with a usage error, as is `--code` for
  an ordinary tool call.
- An unanswered prompt, a refused code or an expired code leaves the proposal
  waiting. The command prints why and exits 3 with the next step instead of
  failing.

Regression tests:

- `internal/chatui/control_confirm_test.go`: codeless confirm leaves the Run
  waiting; re-rendering returns the same code; the direct route returns
  `control_run`; a lowercase hyphenated code applies without a second model
  call; the code is absent from the transcript; reject needs no code;
  `attach.secret` cannot render or confirm.
- `cmd/freeagent/control_confirm_test.go`: local interactive apply with `y`
  and decline with Enter; local `preview`, stale `--code` refusal,
  unanswered prompt, wrong code then correct code; `confirm` without
  `--approve` declines; `--code` refused for an ordinary edit; attached
  interactive apply; attached `preview` code, stable re-preview, wrong code
  exits waiting and correct code applies.
- `internal/chatui/test/approval.test.mjs`: three card tests.

Verification:

```text
go test -count=1 -timeout=300s ./...       PASS (9 packages)
go vet ./...                               PASS
gofmt -l cmd internal                      NO_OUTPUT
git diff --check                           PASS
pnpm test (internal/chatui)                62/62 PASS
pnpm run build (internal/chatui)           PASS
scripts/test-portable.ps1                  PASS (all reported checks)
go test -race                              NOT_RUN (no C compiler)
```

The current source package is:

```text
bin/portable/20261019-201015-3b409b3e/FreeAgent-v7.0.0-dev.1-windows-amd64-portable.zip
SHA-256: c94cdc4349c75cc86504ea68408e987fe8964bf7a9d248e4ba7fcd82b62cb7e6
```

The card was verified by tests, not by a manual visual check of the
packaged desktop window. The other open gates are listed under Remaining v7.0
Gates.

## Fix: October 19, 2026 (confirmation code surface binding)

Package `20261019-201015-3b409b3e` is superseded. Its confirmation codes
were bound to the rendering principal (`chat-ui` or `cli`), so rendering a
pending proposal under another surface label issued a new code and silently
invalidated the one already shown. Plan section 8.1 binds the code to owner
generation, local user, conversation and operation, and excludes the surface
type.

Changes:

- The code MAC now covers owner generation, operation and conversation. The
  surface label is no longer part of the code state, the MAC or the check.
  The owner serves one local user, and every caller holding its interactive
  capability acts as that user.
- `RenderConfirmation` reissues only when no code exists, the conversation
  differs, the code expired or five attempts failed. A render on another
  surface returns the same code and expiry.
- The interactive capability is still bound to the principal that received
  it, so a capability cannot be presented under another principal.
- An owner restart still issues a new code epoch.

Regression tests:

- `TestControlCodeIsSharedAcrossSurfacesOfOneOwner`: a `cli` render after a
  `chat-ui` render keeps the code and expiry; another conversation can
  neither render nor use it; a capability is refused under another
  principal; the code shown under `chat-ui` is accepted, lowercase and
  hyphenated, from `cli`. The test fails on the previous code, which
  reissued on the second render.
- `TestAttachedPreviewShowsCodeForConfirm` now checks that the code the Chat
  UI card endpoint renders is the code the attached CLI `preview` shows.

Verification:

```text
go test -count=1 -timeout=300s ./...       PASS (9 packages)
go vet ./...                               PASS
gofmt -l cmd internal examples             NO_OUTPUT
git diff --check                           PASS
scripts/test-portable.ps1                  PASS (all reported checks)
go test -race                              NOT_RUN (no C compiler)
```

No frontend source changed in this fix; the ChatUI results from the previous
fix still apply.

The current source package is:

```text
bin/portable/20261019-212519-c38d97ef/FreeAgent-v7.0.0-dev.1-windows-amd64-portable.zip
SHA-256: ae0c394d6170471530d91ea7a94dde8962309955645d67eca938e7490ecba212
```

## Fix: October 19, 2026 (scope reduction and sandbox)

Package `20261019-212519-c38d97ef` is superseded.

Removed from v7.0 after the over-design review, and recorded in plan
sections 8.2, 11, 14, 16 and 19:

- typed chat confirmation and `superseded_by_user_message`; the GUI card is
  clicked and the CLI prompt shows the code;
- a counter for the host gate; the booleans are correct because only one Run
  is active and only read-only file children run in parallel;
- Job CPU, memory and process-count limits;
- sandbox readiness states, the multi-tool compatibility probe, the grant
  ledger and the cleanup command;
- content-hash binding for Skill and Extension trust;
- `external_control_change` on receipts;
- gitdir resolution for worktrees and submodules, and link resolution for the
  overlap check.

Moved out of v7.0: durable compaction is the v7.1 priority; `apply_patch`,
timeout and truncation standardization, Provider fixtures and diagnostics go
to v7.2; background command sessions come later. Generic lifecycle hooks
were dropped.

Fixed:

- The command catalog and settings page no longer say the sandbox is not
  installed. `winlaunch.SandboxSupported()` reports the platform; on Windows
  catalog rows show `sandboxed` and the sandboxed backend reports `ready`.
- Ending a Run, including the GUI end-and-send path, withdraws its pending
  control proposal with `run_abandoned`. Before, the proposal stayed pending
  forever and its card could still be rendered.
- A sandboxed command no longer gets the configured additional roots when the
  Run's file scope is workspace-only. Before, every configured root was
  granted to the AppContainer regardless of scope.

Sandbox changes:

- Links inside a grant root are allowed. Only a root or protected path that
  is itself a link is rejected. Native measurement in the previous turn showed
  that ACL propagation does not follow junctions; the new test
  `TestJunctionInsideGrantDoesNotExposeTarget` proves that a pnpm-style
  junction to a target inside the root is readable, a junction to an outside
  directory exposes neither read nor write, and the outside target never gets
  a container ACE.
- Root grants are kept for the owner session under one AppContainer profile
  per data directory and access mode, and released when the owner closes.
  `TestKeptGrantIsReusedAndReleased` measured 3,002 entries at about 0.42 s
  for the first launch, 0.04 s for the second, and 0.40 s to release; the
  grant was applied once and no container ACE was left afterwards. This
  repository (about 17,400 entries) is estimated, not measured, at roughly
  2.5 s for the first command of a session instead of about 7 s for every
  command.
- Grants add ACEs to the existing DACL and keep the root's inheritance
  setting. Revocation removes only the container's explicit ACEs, so user ACL
  changes made in between survive. A path with no DACL is refused instead of
  being given an ACL that would lock the user out.
- Protected paths use a protected DACL for each launch. The first attempt
  added inheritable deny ACEs and the native test showed writes still
  succeeding into `.git/objects`; an AppContainer can use only accesses
  granted to its package SID, so the protected DACL that does not inherit the
  workspace write grant is what enforces the mode. `.git` is now readable,
  so `git status` works in the sandbox; before, the whole of `.git` was
  denied.
- `commands.git_write` (default off; GUI switch, `config commands
  --git-write`, and the existing `commands.set_policy` AI operation) makes
  `.git` writable while `.git` itself cannot be deleted or renamed and
  `hooks`, `config`, `config.worktree`, `modules` and `worktrees` stay
  read-only. `TestProtectedGitPathsStayReadable` covers both modes.
- A sandboxed command that creates `.git` in a root that had none sets
  `git_metadata_created` on its receipt
  (`TestSandboxedCommandMarksCreatedGitMetadata`).
- `doctor` reports `sandbox.status` (`ready`, `unsupported` or
  `failed`) from a throwaway AppContainer `cmd.exe`. A failure fails doctor
  only when command_run uses the sandbox. The packaged CLI reported `ready`.

Observed limits, recorded in plan section 11.5: a Scoop shim fails inside the
sandbox because its real program is outside the granted directory; pointing
the command at the real `git.exe` worked (`git status --short` printed the
untracked file). `.git/config` is readable, so a token in a remote URL is
readable by sandboxed commands.

Verification:

```text
go test -count=1 -timeout=300s ./...       PASS (9 packages)
go test ./internal/winlaunch -count=1 -v   PASS (native AppContainer tests)
go vet ./...                               PASS
gofmt -l cmd internal examples             NO_OUTPUT
git diff --check                           PASS
pnpm test (internal/chatui)                62/62 PASS
pnpm run build (internal/chatui)           PASS
scripts/test-portable.ps1                  PASS (all reported checks)
packaged doctor sandbox check              PASS (status ready)
packaged config commands --git-write       PASS (git_write persisted)
go test -race                              NOT_RUN (no C compiler)
```

The current source package is:

```text
bin/portable/20261019-235038-fa8e10cc/FreeAgent-v7.0.0-dev.1-windows-amd64-portable.zip
SHA-256: f0cdfeaeee000147b369d3b6b21acf6a5ef96f57e16e827fe63267bf0b386865
```

## Continuation: October 21, 2026 (Responses/search live gate)

Manual desktop checks on the package above were reported by the user as
working: the native folder-picker workspace switch and the control
confirmation card. These are user-reported manual results, not automated
evidence.

The live Provider script previously ran only Chat, tool, approval and
interruption tests; no live test reached Responses or `web_search_call`.
This continuation added the missing gate:

- `internal/harness/provider_live_responses_test.go`:
  `TestLiveProviderResponsesJSONAndSSE` (JSON and SSE, usage, provider
  items), `TestLiveProviderResponsesWebSearch` (completed
  `web_search_call` in JSON and SSE, then a stateless continuation that
  replays the saved items) and
  `TestLiveProviderResponsesHarnessToolsSearchAndRestart` (function
  call round trip, saved search metadata, no local tool for server search,
  replay after reopening the Store, credential scan).
- The Responses model comes from `FREEAGENT_LIVE_RESPONSES_MODEL`; DeepSeek
  documents server-side search for `deepseek-v4-pro` only. Chat tests keep
  `deepseek-flash`.
- `run-live-acceptance-secure.ps1` sets that variable, runs from the
  repository root and, with `-Archive`, passes the same process-only key
  to `run-packaged-cli-acceptance-secure.ps1 -KeyEnv`, so the key is entered
  once.
- The packaged CLI check now also configures `deepseek-v4-pro` without
  capability flags, requires the suggested `responses` and
  `web_search=supported` capabilities, runs a search task and reads
  completed `web_search_calls` back through `inspect`.

Offline verification: the new tests skip without live variables;
`go test -count=1 ./...` PASS; `go vet ./internal/harness` PASS; both
scripts parse; a fake key reached the real 401 boundary in both scripts and
failed closed.

Live result (user ran the hidden-input script with `-Archive` against
`20261019-235038-fa8e10cc`; Chat model `deepseek-flash`, Responses model
`deepseek-v4-pro`):

```text
TestLiveProviderCollaboration                         PASS
TestLiveProviderTools                                 PASS (model_calls=11 approvals=1)
TestLiveProviderChatJSONAndSSE none/supported         PASS
TestLiveProviderToolErrorAndApprovalDecline           PASS
TestLiveProviderControlledInterruptionsRetainUNKNOWN  PASS (disconnect/cancel/timeout)
TestLiveProviderResponsesJSONAndSSE none/supported    PASS (status=completed items=2)
TestLiveProviderResponsesWebSearch none/supported     PASS (2 completed searches each; continuation PASS)
TestLiveProviderResponsesHarnessToolsSearchAndRestart PASS (2 completed searches saved; restart replay)
packaged CLI (read, approve, decline, search, doctor) PASS (completed_web_searches=2, no credential in output or files)
```

Fix found by this run: every live search reported an empty query. DeepSeek
does not use the OpenAI `action.query` field; it reports `action.type=search`
with a `queries` list and `action.type=open_page` with a `url`, and appends
an internal `ws_call_id=` marker to both. `webSearchCallFrom` in
`responses.go` now records `action`, the joined queries and the page URL without
that marker; the raw item replayed to DeepSeek is unchanged.
`TestResponsesDeepSeekSearchActions` covers the recorded shape in JSON and SSE.
The live gate and the packaged script now count a search as completed only
when it names its action and a query or URL. The packaged script also removes
the test key profile before it keeps the acceptance directory.

After the fix: `go test -count=1 ./...` PASS (9 packages), `go vet ./...`
PASS, `gofmt -l` no output, `test-portable.ps1` PASS on the rebuilt package:

```text
bin/portable/20261021-185949-3b88060f/FreeAgent-v7.0.0-dev.1-windows-amd64-portable.zip
SHA-256: bc59ebb4ca8ef1f0ed9ede15a8bc18415355bd0bf2f7e3477eb238bf5daec79e
```

The live run was on the previous package; the rebuilt package differs only by
the search metadata summary above, covered by the recorded-shape fixture. A
repeat live run on the rebuilt package is optional. `go test -race` stays
`NOT_RUN` until gcc is installed.

## Race run: October 21, 2026

GCC was installed with `scoop install mingw` (MinGW-Builds GCC 16.2.0,
`x86_64-posix-seh`, `%USERPROFILE%\scoop\apps\mingw\current\bin` on the user
PATH).

The first `CGO_ENABLED=1 go test -race ./...` reported no data race but one
deterministic failure: `TestMCPAPIManagesConfigurationWithoutReturningEnvironmentValues`
returned 400 on MCP restart after about 2.3 seconds. The fixture MCP server is
the race-built test binary, and the race runtime sleeps one second at exit by
default (`GORACE=atexit_sleep_ms=1000`). With the explicit MCP environment
that sleep outlasted the graceful close and the forced-kill path reported a
cleanup error. `internal/mcpstdio` tests already set `atexit_sleep_ms=0` for
their helpers; the ChatUI fixture now does the same. This is a test fixture
change, not a product change.

```text
go test -race -count=1 -timeout=30m ./...   PASS (9 packages, 0 DATA RACE reports)
go test -count=1 ./internal/chatui          PASS
gofmt -l cmd internal examples              NO_OUTPUT
```

### Post-install verification and MCP close fix

A second pass used a fresh PATH read from the registry, as a new terminal
would: `gcc` resolves from the Scoop directory, `go env CGO_ENABLED` is `1`
by default and a cgo program compiled and ran. The portable build still sets
`CGO_ENABLED=0`, so installing gcc does not change the shipped binaries.

That pass found an intermittent race-only failure:
`TestCommandTransportCanceledBlockedWriteClosesAndReaps` failed about 1 in 8
runs because a cancelled blocked Write took 30 seconds instead of under 3. The
goroutine dump showed `commandConnection.Close` stuck in
`internal/poll.(*FD).Close` on the stdin pipe while the writer stayed in
`WriteFile`: Go's pipe cancel did not release that synchronous write, so a
synchronous stdin Close waited until the never-reading server exited on its
own. This is a product bug (an MCP server that stops reading could stall a
cancelled call), made visible by race timing. `Close` now closes stdin in the
background, runs the existing graceful/terminate/kill sequence, and only then
waits, bounded, for the stdin close; killing the tree breaks the pipe and
releases the writer. The test passed 80 of 80 race runs after the fix.

```text
go vet ./...                                PASS
go test -count=1 ./...  (cgo default on)    PASS (9 packages)
go test -race -count=1 ./...                PASS (9 packages, 0 DATA RACE reports)
go test -race -count=80 blocked-write test  PASS (80/80)
go test [-race] -count=5 ./internal/mcpstdio PASS
pnpm test (internal/chatui)                 62/62 PASS
scripts/test-portable.ps1                   PASS (all reported checks)
```

Current package:

```text
bin/portable/20261021-195316-779f9138/FreeAgent-v7.0.0-dev.1-windows-amd64-portable.zip
SHA-256: fc513c24ddf266f42ea1139f42d3a227bff6b004c88c6c7a65041fed99703714
```

## Release verification: October 21, 2026

Running the CI commands under Linux (WSL Debian, Go 1.26.5) before the first
v7 push found three Linux-only problems and one real data race:

- `internal/winlaunch/launch_other.go`: Close no longer reports `os.ErrClosed`
  for pipes that `cmd.Wait` already closed, and always kills the process
  group, ignoring `os.ErrProcessDone`. This fixed the MCP "file already closed"
  failures and `TestCommandTransportCloseKillsManagedDescendants`.
- `TestWorkspacePermissionRequiresApprovalForWorkspaceConfig/edit_config_other_case`
  relies on case-insensitive backslash paths and now runs only on Windows.
- Data race: `ControlService` applies settings through a new `TaskService` on
  the shared core, so its `Config` write was not ordered with the Chat
  server's `TaskService.Config()` read. The core now has a shared `configMu`;
  reads use `currentConfig()`, writes use `setConfig()` and per-Run copies use
  `runCopy()`. Windows `-race` had passed only because of timing.

The CI race step timeout was raised from 5 to 10 minutes; the harness package
alone takes about 100 seconds under `-race` locally.

```text
Linux:   go mod verify / go vet ./...             PASS
Linux:   go test -count=1 ./...                   PASS (9 packages)
Linux:   go test -race -count=1 ./...             PASS (9 packages, 0 DATA RACE)
Linux:   go build ./cmd/freeagent                 PASS
Windows: go vet ./... / gofmt -l                  PASS / NO_OUTPUT
Windows: go test -count=1 ./...                   PASS (9 packages)
Windows: go test -race -count=1 ./...             PASS (9 packages, 0 DATA RACE)
Windows: update-notices.ps1 -Check                PASS
pnpm test / pnpm build (internal/chatui)          62/62 PASS / unchanged assets
```

The first GitHub Actions run on `windows-latest` found two more issues:

- Two Chat settings tests compared a `t.TempDir()` root verbatim, but the
  runner's `TEMP` is an 8.3 short name that root normalization expands. The
  tests now compare the canonical path; reproduced locally with a short `TEMP`.
- The extension `initialize` handshake used the 2-second call timeout, which
  also had to cover a cold Python start. The handshake now allows at least 10
  seconds; later calls keep the manifest timeout.

The release package is rebuilt from the tagged commit and attached to the
GitHub release `v7.0.0-dev.1` with its SHA-256 in `SHA256SUMS.txt`.
