# FreeAgent v7.7–v7.10 implementation

Started: 2026-11-03. Plan: FREEAGENT_V7_7_TO_V7_10_SERVER_ATTACHMENTS_PLAN_2026-11-02.md.
Baseline: 6b9e0ae with retained v7.5–v7.7 working changes. Existing work is preserved.

| Stage | State |
|---|---|
| v7.7 continuation, safety, Provider switches | IMPLEMENTED; focused and ordinary regression PASS |
| v7.7 repeatable build and release | IN_PROGRESS; native race and clean-build gates pending |
| v7.8 server lifecycle and Linux sandbox | PENDING |
| v7.9 attachments, delivery and backup | PENDING |
| v7.10 optional public Web and configurable login protection | PENDING |

Historical acceptance is not acceptance of these changes. Real Provider/channel,
live HTTPS and eight-hour service checks must be recorded separately. Runtime
UNKNOWN remains distinct from tests that have not run. No old data migration,
independent task system or automatic replay is authorized by this work.

## v7.7 closeout (2026-11-03)

Online one-shot abandon/backup reuse interactive attach and the Store snapshot
boundary. Regression verifies repeated abandon, independent conversations,
backup validation, existing-target rejection and observer refusal. Loopback
Host supports distinct SSH-forwarded ports with exact request-Origin checking;
local cookie authentication was removed and SSE uses header-authenticated fetch.
Git metadata is entirely read-only; the old writable mode/UI/flag is removed.
Provider switches are default-off configuration with GUI, CLI and AI controls;
dispatch checks current state, including continued Runs and collaboration.
Disabled providers are excluded from menus/Auto and no secret is resolved for
their model dispatch. No automatic discovery, fallback or replay was added.

Ordinary harness regression PASS (184.450s); CLI failures from old default-on
fixtures corrected and focused regression PASS; ChatUI and Windows launcher
regression PASS; 78 JS tests and production build PASS; vet/modules PASS.
Preview ZIP integrity/startup/reopen/engine/CLI smoke PASS; actual native WebView2
first startup and Provider editor checked. These preview checks precede the
clean source release build and do not substitute for its final package checks.

The two builders now share deterministic archive generation with commit time,
fixed ordering/modes, a pinned Go version and complete source inventory including
dist, lockfiles and workflows. Clean rebuild equality remains a release gate.
