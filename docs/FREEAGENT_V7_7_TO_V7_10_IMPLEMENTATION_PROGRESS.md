# FreeAgent v7.7–v7.10 implementation

Started: 2026-11-03. Plan: FREEAGENT_V7_7_TO_V7_10_SERVER_ATTACHMENTS_PLAN_2026-11-02.md.
Baseline: 6b9e0ae with retained v7.5–v7.7 working changes. Existing work is preserved.

| Stage | State |
|---|---|
| v7.7 continuation, safety, Provider switches | IMPLEMENTED; focused and ordinary regression PASS |
| v7.7 repeatable build and release | PASS; v7.7.0-dev.2 published from 421c25b |
| v7.8 server lifecycle and Linux sandbox | IN_PROGRESS; Debian namespace/negative tests PASS; unit/soak pending |
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

Final v7.7 packages: Windows SHA-256
`28192df3bfb03ddcb8939008e1cdc735d1e7549fdf06b6526c3195b822258bec`, Linux
`b1de5f3b4cd5b4e77302f818575c0d3e9c1840f45be0a6632af79708a789875d`.
Duplicate clean builds and download checks PASS; Windows native full race PASS
(harness 317.978s), Debian 12 ordinary-user full race PASS (harness 359.816s),
final Windows portable and all 16 Debian portable checks PASS. Native WebView2
GUI submission → CLI continuation → same GUI displayed both turns, PASS.
Release: https://github.com/endview/FreeAgent/releases/tag/v7.7.0-dev.2.

v7.8 adds service mode (no launch URL in journal), owner lifecycle commands,
explicit data-directory environment, Linux CLI ownership guard, systemd installer,
shared listener-port control and GUI runtime facts. Bubblewrap 0.8.0 was installed
on the authorized Debian 12 test host. Actual namespace probe and negative tests
PASS: workspace/metadata privacy, read-only mounts, host-loopback/Internet denial,
protected hardlink refusal, ordinary project hardlink sharing, setsid descendant
cleanup and Owner SIGKILL cleanup. pidfds track namespace init; no host fallback.
These are process-level tests; service-unit and full Harness checks remain gates.

The two builders now share deterministic archive generation with commit time,
fixed ordering/modes, a pinned Go version and complete source inventory including
dist, lockfiles and workflows. Clean rebuild equality remains a release gate.

## v7.8 functional gate and v7.9 implementation

Installed clean Linux v7.8 source ecb751c on the isolated Debian 12 systemd
service. Actual unit sandbox, setsid stop cleanup, crash/on-failure restart,
UNKNOWN model/Attempt retention without replay, finite service expiry, online
backup/verify/restore and journal credential absence PASS. Acceptance also found
and fixed a one-shot CLI approval race: it now waits for the accepted Owner
execution, using all active conversation bindings rather than only the first.
The real eight-hour soak started 2026-11-02T19:30:05Z and remains RUNNING until
28800 actual monotonic seconds have elapsed. No shortened soak is accepted.

v7.9 selects unused Harness-v8, config/schema 10, Store format
freeagent-harness-v10, backup-v4; launchers, manifest builders and current docs
are updated together. git_write is removed, with no migration layer.

Attachment input/storage/context/immutable export/reply-save/backup/GUI/CLI and
Telegram/Lark thin media adapters are implemented. Core focused tests PASS,
including None input, client/conversation binding, upload and Run idempotency,
changed export approval, immutable result, hardlink refusal, backup corruption,
three vision encoders and no persisted Base64, history image budget/omission,
background-without-download, None channel reply export and UNKNOWN file sends.
81 JS tests and production build PASS. Full initial Go run found four stale
format/catalog/tool-count expectations; these were corrected and targeted
regression PASS. Final full/race and actual package checks are still gates.
Real vision Provider and real channel file accounts remain NOT_RUN. QQ generic
file upload is explicitly NOT_SUPPORTED; no anonymous public file hosting added.
