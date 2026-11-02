# FreeAgent v7.5–v7.7 local implementation and acceptance

Date: 2026-10-29. Candidate: v7.7.0-dev.1. Source base: 6b9e0ae plus retained
working-tree changes. No existing code/documents were rolled back. No migration
or old-platform compatibility was added. No commit, push or Release was made.

The user explicitly authorized local implementation and simulated acceptance
until live platform apps/chats/accounts are available. Completion of this local
candidate is distinct from formal real-platform release acceptance.

## Implemented

- v7.5-A: spaces, mandatory conversation membership, local space at None,
  default/current pointers, explicit multi-conversation, GUI grouping/movement,
  CLI and typed AI controls. Harness-v7, schema/config 9, store v9 are current
  only. Existing v7.5-A evidence is in FREEAGENT_V7_5_IMPLEMENTATION_PROGRESS.md.
- Telegram private/group text, local pairing, read-only mode/status, stop,
  continue, abandon, compact and explicit single/multiple-conversation routing.
  Polling remains free to record background while a foreground request runs.
  Pair codes are local interactive capabilities; observer attach credentials
  cannot see or generate them. Model-proposed product settings stay local.
- Shared channel session around existing Owner/TaskService/Store. It does not
  execute tools itself. Stable request deduplication and trigger-to-Run receipt
  association commit together; collaboration parents claim the trigger and
  children do not reuse it. Incoming background has no fabricated Run.
- Exact Telegram buttons and Lark cards bind current pending content, requester,
  platform/app/tenant/chat, actual sent message, expiry and grants. Wrong people
  do not consume buttons. Repeated/restarted/stale actions do not execute.
- Lark app websocket, token caching, private/group text, scoped identities and
  card actions. Frames, fragments, deadlines, ack and buffers are bounded. One
  small websocket dependency is used; no generated platform SDK is embedded.
- QQ official signed webhook, challenge/heartbeat/dispatch acknowledgements,
  C2C and group @ text. Signature covers timestamp and exact body; stale/forged
  bodies are rejected. Listener is numeric loopback; users supply any HTTPS
  ingress. Guild/channel messages, ordinary group text, edits and approval
  buttons are NOT_SUPPORTED. Existing local confirmation handles QQ approvals.
- Final messages are paced, split and bounded. A durable pre-send claim prevents
  resend after uncertain dispatch/crash. Failed and unknown delivery are distinct
  from execution state. Only confirmed parts become public text. There is no
  automatic replay of a model, tool or unknown send to recover a reply.
  GUI/CLI evidence reads those receipt states with an indexed prefix lookup;
  a no-op compression or failure before Run creation still gets a response.
- Frozen public history cutoff, visible group summaries, no implicit local
  prompts/Skills/memory/private tool history. Local prompts in an external
  conversation remain visible to the model and their replies retain the target.
  GUI displays the latest 100 unassociated received messages as background.
  Explicit moves into an external space create deduplicated public text
  mirrors in bounded batches. Original messages, tool bodies and synthetic
  command prompts remain private. Repeated moves do not duplicate text.
- v7.7: two foreground conversations per Owner; each conversation retains one
  unfinished Run. Scoped cancellation, independent bounded streams/events,
  per-conversation attach bindings and all-operation handoff/shutdown. Pending
  approval/UNKNOWN retain their conversation without occupying execution slots.
- Store-level global commits/backups/workspace boundary and specific blocking
  conversation IDs. A Run's confirmed config proposal can upgrade its own lease
  only if no other conversation executes. Revocation bypasses ordinary busy
  checks and stops affected owned commands/services through existing records.
  Cancelling an inactive collaboration or adjusting an inactive Run uses its
  own conversation lease and is not blocked by another conversation's model.
- Owner-wide two model calls in flight, including collaboration/embedding calls;
  existing parent cumulative budgets and one-command/two-service quotas remain.
  Incompatible AppContainer profiles still refuse; no host fallback is added.
- One short shared publication lock spans final hash verification to file-tool
  publish/delete across separate tools and path aliases; batch outcomes remain
  per-file. External programs/commands have no transactional conflict guarantee.
- GUI, CLI and AI settings cover all three connections, saved key references,
  state and pairing without creating another permission/task subsystem. Optional
  connection failures do not prevent local startup and do not inherit an HTTP
  request's lifetime. Credentials remain in existing key storage.

## Actual checks

Environment: Windows amd64, Go 1.26.5, native MinGW GCC with CGO for race;
Debian 13 in WSL2, Go 1.26.5 linux/amd64, GCC 14.2.0; existing pnpm/Node runtime.
All channel secrets used by automated fixtures are synthetic loopback values.

| Check | Actual result and scope |
|---|---|
| Complete ordinary Windows Go suite | PASS; frozen final full run Harness 144.358 seconds |
| Complete Windows native race | PASS; Harness 280.030 seconds; no race report |
| Complete Linux race | PASS; Harness 163.539 seconds; no race report |
| Pairing visibility, pacing, connection binding | PASS; Windows targeted race Harness 21.446 s, Linux 18.996 s |
| Final publication/cancellation boundaries | PASS; Windows race Harness 66.066 s, Linux 51.888 s; exact publication/no-tool-leak and cancellation-under-other-Run regressions PASS |
| Final channel feedback and visible delivery evidence | PASS; Windows race Harness 26.922 s and indexed-query regression 3.646 s; Linux race 23.958 s |
| ChatUI JS and production assets | PASS; 77 tests and final production build |
| Static checks and dependencies | go vet PASS, go mod verify PASS; notices regenerated for gorilla/websocket |
| Telegram/Lark/QQ official-wire mocks | PASS: identity scopes, pairing, None, group background, exact approvals, duplicate delivery, signature, token caching, websocket cancel/ack, passive window, no replay |
| Owner parallel acceptance | PASS: two streams, third refusal, wrong binding refused, stop A keeps B, drain all on handoff, local result uses shared delivery |
| File publication contention | PASS: two tool instances with one old hash yield exactly one successful edit |
| Portable and native UI | Initial v7.7 package gates PASS; native WebView2 forms/key types/None chat and GUI→CLI→GUI continuation PASS; final package receipt recorded separately |

Final frozen package: `bin/v77-delivery-final-20261029/FreeAgent-v7.7.0-dev.1-windows-amd64-portable.zip`.
Its SHA256 and gate receipt are in SHA256SUMS.txt and LOCAL_ACCEPTANCE.json beside
the archive. Final frozen clean-package gate: PASS.
SHA256: `a2d73710798b2a40b206e33722808f3d223d9fe50c52009240ab65afd1a7c031`.

Build source digest: `94b21c8195466c206d5b19c10424d9ca0a64036ac9763133e164354c88f19e65`.
Unsigned local preview, `source_dirty=true`; exact build source inventory is
beside it in SOURCE_SHA256SUMS.txt. Acceptance documentation was updated after
the build; that does not change the compiled source snapshot or ZIP checksum.

Linux amd64 now also has a CLI/Web portable archive. On a real Debian 12 amd64
server, the complete Go race suite and the final 16-check extracted-package gate
passed as an unprivileged user. See
[Linux acceptance](FREEAGENT_V7_7_LINUX_ACCEPTANCE_2026-10-29.md) and
[Linux startup/limits](LINUX_PORTABLE.md). Linux AppContainer and a native desktop
entry remain NOT_SUPPORTED; explicit Full/host command execution was tested.

Final frozen clean portable acceptance PASS: archive/integrity, no bundled state,
desktop/CLI PE types, first launch, close, reopen, correct engine path, loopback
HTTP, init/refusal of existing store, saved DPAPI keys, corrupt-manifest refusal,
CLI version, web Echo, stdin-close shutdown and extracted-binary smoke. It ran
under Windows PowerShell 5 in a new directory containing spaces, `&` and `!`.
No native test window or FreeAgent test engine was left running.

The native WebView2 check used the same final frontend assets in a separate
extracted v7.7 package. It inspected all channel forms and new credential types,
sent a None/Echo message, ran a CLI attach turn, and observed that turn in the
same GUI conversation. The last backend publication/cancellation fixes were
covered by the separate final Windows/Linux race checks and rebuilt package.

Routine commands: `go test -count=1 -timeout=5m -skip 'Live|RealProvider' ./...`,
`go test -race -count=1 -timeout=10m -skip 'Live|RealProvider' ./...`, `go vet ./...`,
`go mod verify`, `pnpm --dir internal/chatui test`, `pnpm --dir internal/chatui build`,
`scripts/update-notices.ps1 -Check`. Linux tests use a separate source snapshot,
not Windows test binaries. Tail changes are retested on both OSes.

An early portable stdin investigation found Windows PowerShell 5 emitting a
UTF-8 BOM before key input. Both acceptance scripts now set BOM-free input
encoding and write exact bytes. The prior package gate passed; the final v7.7
package must pass again. A baseline test assuming one global foreground Run was
updated to check the same conversation, with explicit new capacity/cancel tests.

## Efficiency observation

Actual Windows process plus a bounded 300 ms mock model workload: serial 730 ms,
parallel 371 ms, overlapping interval 271 ms. Process memory allocated before/
after: 1,168,096 / 2,203,272 bytes; command output bound 1,024 bytes. This is one
local fixture observation, not a Provider benchmark or a speed guarantee. The
real command used Full/host; it is not sandbox isolation acceptance. Command
quota remains one, so two simultaneous command requests may correctly refuse.

## External acceptance and limits

| Item | Status | Needed |
|---|---|---|
| Telegram private/group with two identities | NOT_RUN | Valid Bot, authorized targets and two controlled members |
| Lark app/private/group/cards | NOT_RUN | Official internal app, event/card permissions and test members |
| QQ C2C/group/passive window | NOT_RUN | Official enabled app, permissions, test users/groups and HTTPS ingress |
| Representative real Provider group chat/summary | NOT_RUN | Current valid credential through key storage and authorized test content |
| Additional QQ types, attachments, high-frequency token edits | NOT_SUPPORTED | Separate scope; no personal-account protocol or hosted platform added |
| Inherited IPv6/service matrix and actual eight-hour run | NOT_RUN | Dedicated long-duration/platform acceptance |
| Sandboxed service access from host loopback | NOT_SUPPORTED | Retains the v7.4 boundary |

Mock PASS does not mean these real items passed. No historical chat API keys
were used. There is no unknown production execution under this task; automated
interruption/delivery cases intentionally retain runtime UNKNOWN and test that
it cannot replay. Full remains a same-Windows-user escape hatch. Shared group
background can influence a permitted requester's model; source text cannot
grant authority. Platform visibility, rate quotas and card permissions still
require real accounts. An unknown send may have delivered text; it is retained
without retry, so there can be missing replies until an explicit status query.
