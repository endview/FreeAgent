# FreeAgent v7.5 implementation record

Subsequent implementation has reached v7.7.0-dev.1. See
[v7.7 implementation and current acceptance](FREEAGENT_V7_7_IMPLEMENTATION_PROGRESS.md)
for the completed local channel/parallel work and the resolved portable stdin
failure. This earlier record retains the evidence from its original stage;
its work-in-progress and portable-failure statements are historical.

Date: 2026-10-29. Source base: `6b9e0ae`, working-tree changes.
Status: v7.5-A local milestone accepted; v7.5-B local implementation and simulated acceptance are in progress. This is not acceptance
of the complete v7.5 Telegram private/group release.

## Implemented locally

- Current-only `Harness-v7`, SQLite schema 9, config version 9 and
  `freeagent-harness-v9` attach/discovery. The nullable message Run association
  is part of this new format; no migration or compatibility reader exists.
- One spaces table and mandatory conversation space ownership. Local is seeded
  at None and cannot be deleted. Existing message, Run and Attempt records remain
  the execution history. Default/active membership and single-conversation
  routing are checked; moving/deleting refuses unfinished Runs and active or
  unresolved owned processes.
- Space permission replaces conversation permission metadata. New spaces and
  unset platform people are None even when the execution configuration is Full.
  Space/person minimum, source and original limits travel in existing snapshots.
  Per-turn selections can narrow authority; they do not silently grant the space
  additional authority.
- None can chat, but tool definitions and actual preparation/execution reject
  operations. Responses native search is disabled per request; compaction
  requests prohibit native operations too. Downgraded Runs cannot approve,
  continue or dispatch using their old grant.
- Revocation requests termination of affected owned background commands and
  services through the existing supervisor. Their original effects and receipts
  are retained; termination uncertainty is not treated as success.
- Public context excludes historical private tool bodies, local memory and
  automatic Skill/extension context. External contexts also exclude local
  system/instruction inputs. Public summaries are marked and private summaries
  are rebuilt; moving into an external space drops local input references.
- Space organization, permissions and person settings use the existing typed
  control journal and Preview/Confirm flow. GUI folders, global pins, drag/menu
  movement, explicit publication confirmation and space management share the
  owner with CLI `spaces`, `/spaces`, `/space` and `/move`.

## Actual checks so far

PASS: targeted Go tests for space identity/reopen, pointer repair, None chat,
forged tool refusal and no file effect, all 16 space/person combinations,
actual file effects under those combinations, stale approval after revocation,
unfinished-Run movement/deletion refusal, and native Responses search omission.

PASS: actual owned HTTP service starts, then permission reduction through the
control surface terminates it with confirmed cleanup and preserves the earlier
address file. This does not claim reversal of prior effects.

PASS: GUI HTTP and attached CLI use the same space/conversation identities and
grants. AI space creation waits in the existing Run and applies only after exact
confirmation, without another model call. CLI creates a space, starts a None
chat there, lists only that space, moves it and finds the same ID after reopen.

PASS: public context and actual manual compaction exclude seeded private tool,
system-prompt and instruction text.

PASS: 76 ChatUI JS tests and final production build. Actual in-app browser
interaction created a space at None, sent an Echo chat, pinned it globally,
accepted a CLI continuation in that same conversation and restored both turns
after page reload. Screenshot: `output/v75-ui-spaces-handoff.png`.

The first broad Go run found old fixtures depending on global/default or
per-turn grants, a catalog/table-count expectation, a reversion dispatch gap,
and collaboration snapshot propagation. They were corrected and retested;
the failed run is not counted as PASS. The portable smoke fixture was also
updated to assert initial None, then explicitly grant Ask for input-source tests.

PASS: complete ordinary Windows Go suite, Harness 135.666s
(`output/v75-full-tests-2.log`); complete native Windows race with GCC/CGO,
Harness 284.891s (`output/v75-windows-race.log`). Module verification, vet and
notices check passed. Linux tests/race were NOT_RUN in this local milestone.

PASS: final portable integrity/no bundled state; actual fresh desktop launch
and reopen; correct child CLI engine, loopback frontend and clean idle close;
existing/nonempty init refusal, invalid manifest refusal, isolated key storage,
web Echo chat, stdin-close shutdown and extracted-binary smoke.
See `output/v75-final-portable-test.log`. The UI inspection found a suppressed
native input prompt; creation now uses the space dialog's labeled input.

The package in `bin/v75-spaces-preview-20261028-final/` is an unsigned local
v7.5-A preview with `source_dirty=true`, not a GitHub release or complete
Telegram delivery. Its manifest and sibling checksums describe its exact source
snapshot. Documentation-only follow-up packaging may use a later directory.

## v7.5-B work in progress

The first thin Telegram HTTP adapter is present in `internal/channels`.
Its ordinary tests, native race tests and vet passed. Fixtures exercise official
poll/identity/reply/edit/callback shapes, no implicit delivery retry, 429 delay,
uncertain delivery, no redirect/token disclosure, real event sender identity,
anonymous senders, UTF-16 mention offsets, old edits and bounded text/buttons.
It is now wired through the owner-bound `TelegramRuntime`: local configuration,
volatile pairing codes, private/group routing, channel message deduplication,
Run origin snapshots, public context projection, delivery receipts, and exact
approval callback identity checks share the existing TaskService and Store.
GUI exposes status and pairing, CLI exposes `channel telegram status|pair-code`,
and `config channels` plus the typed `channels.set_policy` control operation
manage the connection. Local simulated runtime tests cover pairing, None chat,
duplicate delivery, Ask approval and wrong-user callback rejection; these are
not real Telegram acceptance.
Official source notes: [Telegram transport contract](TELEGRAM_TRANSPORT.md).

Current-source checks after the B/C work: complete ordinary Windows Go suite
PASS (`go test -count=1 -timeout=10m -skip 'Live|RealProvider' ./...`, Harness
127.515s); complete native Windows race with GCC/CGO PASS (Harness 257.881s);
`go vet ./...`, `go mod verify`, 76 ChatUI JS tests and the production build
PASS. Targeted Telegram runtime and probe tests also pass under race.

A new unsigned local archive was built at
`bin/v75-telegram-preview-20261029-2/` with SHA256
`39cd085e7ba0e002086c5cecfe8da0387346edf7d976643fee369bd8bb6ab2e0`.
The portable integrity/startup gate currently FAILS at the scripted saved-key
stdin step (`key must be 1 to 4096 visible ASCII characters`) even though the
same packaged CLI key path succeeds in an isolated direct process check. The
archive is therefore not a release candidate; this test-harness discrepancy
needs investigation before packaging is called PASS.

## Not implemented or not accepted yet

Real Telegram account/message acceptance, Lark and QQ adapters, and v7.7
parallel foreground Runs are not delivered. No platform support is inferred
from the space fields.

User confirmed that Bot, authorized test chats and two controlled accounts are
not currently available. Work proceeds with local implementation and mocks.
Real platform requests/messages and real Provider acceptance are NOT_RUN.

No GitHub push or Release was created during this implementation.
