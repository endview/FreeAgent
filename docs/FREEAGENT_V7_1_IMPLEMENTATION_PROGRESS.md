# FreeAgent v7.1 Implementation Record

Date: 2026-10-25
Candidate: v7.1.0-dev.1
Baseline: v7.0.2-dev.1, `012457474eaf332a930cb73717533a46b0aaf8c0`
Status: v7.1 acceptance passed; development prerelease

## Delivered

- One bounded summary per conversation in existing `meta`, with source cutoff,
  count, bytes, ordered prefix digest, text hash, revision and writing Run ID.
  Source messages and execution Attempts are never deleted or rewritten.
- Semantic, tool-less `Purpose: compaction` requests share normal model dispatch,
  persistence, step/output limits and active-time accounting. No memory injection
  or extension hook enters these requests. Long prefixes advance in bounded
  complete-group chunks; only the final verified result commits.
- Manual Compact, Refresh, Clear and inspection share TaskService across the
  GUI, local `/compact`, interactive CLI `/context` and one-shot CLI `context`.
  Automatic changes stay disabled by default. A valid manual summary is reused
  across Runs and restarts even with automatic compaction off.
- Automatic compaction runs at the input limit. A stale prefix and oversized
  tail are rebuilt/extended as one atomic replacement, without intermediate
  revisions. An indivisible oversized group returns a limit without dropping
  history. Source verification is cached only within the current owner.
- Idle manual compaction uses an ordinary maintenance Run without synthetic
  chat messages. Completed maintenance disappears from Chat; failed/UNKNOWN
  Attempts remain visible. Stopped/limited Runs retain snapshots, consumption
  and state, and require explicit continuation.
- AI-proposed context changes use one `context_manage` tool and existing exact
  Preview/Confirm. Mutations require user approval even in Full access.
  Observation credentials cannot mutate or confirm this tool; owner generation
  validation remains in the attach transport. No second control journal exists.
- GUI shows source coverage, byte bound, prefix bytes before/after, saved text,
  progress and actual model call count. Stop binds to the current request.
  Closing/reopening the dialog does not resubmit. Narrow headers use two rows.
- Ordinary GUI Stop cancels its task and keeps the owner alive. It no longer
  accidentally triggers the owner-handoff shutdown flag.

## Verification

- PASS: full Windows `go test -count=1 -timeout=5m -skip
  'Live|RealProvider' ./...`.
- PASS: full native Windows `go test -race -count=1 -timeout=10m -skip
  'Live|RealProvider' ./...`, followed by focused race coverage after final
  summary completion and UI-projection corrections.
- PASS: `go mod verify`, `go vet ./...` and third-party notices check.
- PASS: 69 ChatUI JS tests and production Vite build.
- PASS: deterministic semantic continuity after character 120, chunk boundaries,
  untouched history, restart, backup/restore, stopped/limited budget recovery,
  stale source/revision rejection and transaction failure.
- PASS: Provider failure, UNKNOWN, oversized and output-limited summaries retain
  the previous selection; actual model Attempts remain charged, without retry.
- PASS: GUI/CLI share the owner, local slash commands do not become model tasks,
  competing mutation is rejected, GUI Stop and client cancellation preserve the
  old summary/UNKNOWN and leave the owner usable, AI confirmation in Full.
- PASS: Edge browser at 1440x1000 and 390x844: Compact with automatic changes off,
  progress, close/reopen, Clear, local `/compact`, unchanged three-turn history,
  accessible controls within viewport and no console errors. Screenshots are
  local under `output/playwright/v71-*.png`.
- PASS: portable validation build, manifest/checksum integrity, clean first
  desktop launch and reopen, correct engine path, embedded UI HTTP, clean close,
  isolated portable keys/data and extracted CLI smoke. A clean source build is
  rechecked before uploading release assets.
- NOT_RUN: real Provider semantic-quality acceptance for compaction. The plan's
  v7.1 core gate uses deterministic fixture dispatch; this is not a live-quality
  claim. Existing v7.0 live Provider results are not relabeled as v7.1 tests.

## Limits And Follow-On Work

- Byte limits reserve approximate output bytes; they are not provider tokenizers
  or billing budgets. Displayed before/after bytes describe the replaced context
  prefix, not total conversation storage or billed tokens.
- Summaries remain model-generated and can omit nuance. Original history is
  retained for inspection; hashes verify provenance, not semantic truth.
- A lost response after dispatch remains UNKNOWN; no automatic compaction retry
  or automatic continuation is added. An oversized indivisible source group
  remains a context limit rather than being silently truncated.
- The summary writer is traced through its existing Run. There is no new owner
  identity, Task/Step/Evidence object, schema migration or platform service.
- v7.2 tool pipeline/apply_patch/Provider fixtures, v7.3 background commands and
  v7.4 long-running services retain independent implementation/acceptance gates.
