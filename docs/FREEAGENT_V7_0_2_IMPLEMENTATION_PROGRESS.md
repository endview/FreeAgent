# FreeAgent v7.0.2 Implementation Record

Date: 2026-10-25
Status: v7.0.2 acceptance passed; development prerelease
Baseline: v7.0.1-dev.1, `daa82a070fc5570526657151cc6974456abfe5ae`

## Changes

- Interactive CLI attaches to the current owner without opening another SQLite
  writer. With no owner it publishes the same authenticated local API from its
  own process; both terminal and GUI inputs pass through the same server gate.
- Existing session, model, memory, collaboration, file reference, multiline,
  limit and approval menus remain available through owner-side TaskService
  methods. Collaboration review consumes its saved planning Attempt.
- Multiple surfaces can inspect. Competing execution requests are rejected by
  the owner. Ctrl+C uses an exact request binding; client detachment does not
  cancel another surface's work. Expired event cursors resnapshot saved results
  without resubmission. Event polling reads a bounded in-memory projection.
- Native Scoop `.exe` + `.shim` resolution is bounded and never executes scripts
  to discover targets. Windows handle paths resolve `current` junctions to a
  concrete installation. Prefix argv and final executable are checked against
  command rules, allowlist and approval; changes before dispatch are blocked.
  Only the concrete application version receives the extra read-only grant.
- CLI collaboration defaults now use the shared control journal, avoiding an
  external-change conflict when a GUI owner already tracks configuration.

## Actual Validation

- PASS: initial CLI, ChatUI and handoff baseline tests.
- PASS: full non-live Go suite before the final regression additions.
- PASS: final focused GUI/CLI bidirectional history, pending exact approval,
  buffered input, saved collaboration review, cursor expiration, credential
  separation and stale cancellation tests.
- PASS: Scoop metadata bounds, target/argv approval binding, changed-target
  rejection and a real junction plus AppContainer sibling-resource read.
- PASS: `go mod verify` and `go vet ./...`.
- PASS: full Windows `go test -race -count=1 -timeout=5m -skip
  'Live|RealProvider' ./...`, then focused command/rule/Scoop race tests after
  the final canonical rule-path correction.
- PASS: all 64 ChatUI JS tests, Vite build and third-party notices check.
- PASS: portable build and `test-portable.ps1`, including two clean desktop
  launches, correct engine child, embedded UI HTTP, clean shutdown, manifest
  and checksum integrity, portable data/keys and extracted CLI smoke.
- Release asset: `FreeAgent-v7.0.2-dev.1-windows-amd64-portable.zip`, with adjacent
  `SHA256SUMS.txt`. The published clean build replaces the local dirty candidate;
  its embedded manifest records the exact source commit and payload hashes.
- NOT_RUN: real Provider requests, unnecessary for these transport/path changes.
- No failed, blocked or unresolved UNKNOWN acceptance cases remain for this
  gate. Cancellation tests deliberately retain UNKNOWN as the correct result.

## Deliberate Limits

- The CLI owner lives in the terminal process. Exiting that owner drains it;
  attaching a GUI does not automatically promote another process to owner.
- Drafts are local to each surface. Select shared history with `--resume` or
  `/resume`; another window's selected conversation is not inferred.
- Only native Scoop metadata pairs are resolved. Script shims, chains, unknown
  fields, missing metadata and targets outside the application's installation
  are blocked; there is no host fallback.
- v7.1 through v7.4 are not delivered by this candidate. Their independent
  acceptance gates remain required.
