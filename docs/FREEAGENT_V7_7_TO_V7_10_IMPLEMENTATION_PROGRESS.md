# FreeAgent v7.7–v7.10 implementation

Started: 2026-11-03. Plan: FREEAGENT_V7_7_TO_V7_10_SERVER_ATTACHMENTS_PLAN_2026-11-02.md.
Baseline: 6b9e0ae with retained v7.5–v7.7 working changes. Existing work is preserved.

| Stage | State |
|---|---|
| v7.7 continuation, safety, Provider switches | IMPLEMENTED; focused and ordinary regression PASS |
| v7.7 repeatable build and release | PASS; v7.7.0-dev.2 published from 421c25b |
| v7.8 server lifecycle and Linux sandbox | IMPLEMENTED; actual unit functional gate PASS; first soak FAIL, complete retry RUNNING |
| v7.9 attachments, delivery and backup | IMPLEMENTED; Windows ordinary/race and Debian full race PASS; final clean package gate pending |
| v7.10 optional public Web and configurable login protection | IMPLEMENTED; API/auth/race and real Caddy HTTPS fixture checks PASS; final package gates pending |

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
The first eight-hour soak started 2026-11-02T19:30:05Z and FAILED after the
functional test's reused conversation exhausted its context limit (last sample
7747.678s). The process exited but its report incorrectly retained RUNNING; this
was corrected after verifying the process was gone. The script now records
exceptions atomically, uses two dedicated conversations with an explicit finite
256-message limit, and counts cleanup as part of acceptance. A new complete
28800-second soak started 2026-11-02T22:06:45Z. Its time is not combined with the
failed run. No shortened soak is accepted.

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

v7.9 final ordinary Go PASS (harness 191.488s); affected complete Windows race
PASS (harness 371.261s, ChatUI 37.482s), ordinary-user Debian full race PASS
(harness 384.179s). Native WebView2 selected a Chinese/special-name input file,
submitted its actual body in None, saved an assistant reply as Markdown and
displayed a CLI continuation of the same conversation, PASS. Download SHA-256:
`ae9d12e8bf8194bff3aa82dca23c9b9d65d194ac4c04187fd56a347171bd7dae`.
The initial race failures came from an incorrect SQL edit in this work's
channel recovery change; both UNKNOWN paths were corrected, with no relaxed
assertions. Export now uses live quota, CLI chat switches clear pending input,
and Anthropic hard image limits cannot be widened by a declaration. Attachment
turns explicitly require collaboration off, rather than silently disabling it.

## v7.10 implementation checkpoint

Optional Linux-only public Unix listener, single-admin Argon2id credential,
bounded memory sessions, CSRF, exact external Host/Origin, source-header trust,
cookie login detection, independent optional binding, IP lists and login
protection are implemented. Security events reuse a bounded existing SQLite
meta value. No local launch/attach capability is sent to the public browser.
Control and tool confirmations bind the Owner/session/Run/digest; permission
snapshots retain their original remote ceiling on local continuation. Ordinary
admitted mutations settle under the Owner lifetime while revoked readers close.

GUI public login/settings/password/recovery, local CLI recovery, installer
proxy-group preparation and a Caddy template are implemented. 83 JS tests and
production build PASS. Initial full ordinary Go PASS (harness 198.247s),
affected full Windows race PASS (harness 372.687s). Further targeted checks cover
subsequent security refinements. A Windows credential replacement/read-handle
race was found and fixed using serialized file access and bounded rename retry.
Actual Caddy/TLS, browser, final package and repeatable release checks remain
separate gates; no trusted public certificate or real platform result is claimed.

## v7.10 actual HTTPS checkpoint

An isolated Debian service runs source 30cf65c behind Caddy 2.11.7 on port 9443,
without changing existing server services or ports 80/443. The test client
validates the supplied temporary CA and IP SAN; certificate verification is not
disabled. All ten actual HTTPS checks PASS: safe login cookies, exact Origin and
CSRF, proxy-header overwrite and private-route refusal, idempotency and CLI
continuation, None attachment upload/reply export/image preview, remote ceiling
and local recovery, real Owner file_create/file_export via a local protocol
fixture, logout closing SSE while an admitted task completes, five-failure
login lock and local unlock, actual 62.217s idle expiry, and Owner restart
requiring reauthentication. The protocol fixture is not a real Provider.

Windows full race on c4e4b6d PASS (harness 375.874s); Debian ordinary-user full
race on 30cf65c PASS (harness 415.150s). Native Chromium with certificate errors
enabled and a dedicated user's temporary-CA trust actually selected and
submitted a Chinese/special-name text attachment in None, saved the reply,
previewed a validated PNG, and rejected undeclared image capability without
dispatch. Password rotation revoked its authenticated session. Browser work
also exposed the expired one-use login cookie UX issue; explicit password
submission now refreshes that cookie. Latest JS: 84 PASS plus production build.
Anthropic history now omits older images to respect its conditional 20-image
limit when the current input contains a large image, rather than rejecting the
whole otherwise valid turn.

The HTTPS script's fixture shutdown and final configuration restoration were
fixed; its clean repeat against the final package is still pending. The latest
login-cookie change is not yet deployed. A public system-trusted certificate,
real visual Provider semantics and real channel file accounts remain NOT_RUN.
Temporary-CA TLS acceptance is not a claim of publicly trusted deployment.

Source 6425353 passed two clean duplicate builds for each platform, Windows
portable startup/reopen/CLI checks, all sixteen ordinary-user Debian portable
checks including actual sandbox and explicit host execution, and Debian full
race (harness 418.854s). Its actual HTTPS repeat passed all ten checks and a real
62.275s idle expiry. Two test-only issues were corrected: successful sandbox
results must remain CompletedProcess values before decoding, and intentional
SSE revocation may raise Python HTTPException while closing chunked input.

The Windows full race found an intermittent setting-confirmation failure. A
held config reader reproduced it: destination replacement failed while the Run
remained waiting; a subsequent Preview could prepare another proposal and hide
the failed apply. Config publication now retries only bounded Windows sharing,
locking or access failures without removing the old file. Attached confirmation
reads the original operation receipt before any fresh preview; a sustained
failure returns an error. Short-reader and permanently-blocked cases each PASS
five native race repetitions; existing attached/confirm regression PASS.
One-shot ordinary confirmation and continuation also wait for Owner settlement.
These changes require rebuilding the final candidate; the earlier Windows race
failure is not relabeled PASS. Release workflows now pin verified action commits
and frontend/Python versions, rebuild and compare embedded dist before packaging.

Source 0d05f21 runtime gates PASS: Windows ordinary (harness 213.579s), complete
native race (harness 383.611s), Debian ordinary/vet and complete ordinary-user
race (harness 411.779s), notices, clean frontend installation/84 JS tests/build,
and byte-identical dist. Both platform archives repeat exactly. Their Windows
startup/reopen/CLI and sixteen Debian portable checks PASS. Native None input,
reply download with matching SHA-256 and displayed CLI continuation PASS on the
unchanged UI; the reply hash is
`798b95e6bff0b91293e59cce331d5167fbe7707089928e429335da6b163d5361`.

All ten HTTPS checks on the release candidate PASS with empty stderr and an
actual 62.372s idle expiry. An explicit browser login after 227 seconds on the
login page succeeds on its first submission. A separate Windows HTTPS client
validates the test CA/IP SAN over the real network, receives JSON and SSE run
events, and observes public → CLI → public continuation. During a controlled
Caddy pause, HTTPS times out while the local CLI completes; resuming Caddy
restores the authenticated session without changing the Owner generation.

The earlier GitHub v7.7 Windows run failed its native-window check, and another
run reported an AppContainer child exit 66 without stderr. The release workflow
now explicitly selects CLI/manifest checks on headless runners and marks native
checks NOT_RUN; actual desktop acceptance remains separate. The isolation
worker test now drains and reports stderr; ten local native race repetitions
PASS without weakening its assertions. Hosted CI rerun is pending. These last
workflow/test-only edits do not change the tested runtime, but final archives
will be rebuilt from their precise source commit.

Final sandbox inspection found a separate Linux defect: the private-directory
hardlink check named a nonexistent public credential file and only enumerated
fixed leaf names. Three actual Debian negative tests reproduced dispatch with a
hardlink to the administrator credential, attachment blob or blocked content.
Preflight now checks all private regular files using a cancellable walk capped
at 100,000 entries per private directory, without reading contents or following
directory symlinks. Critical state symlink checks remain. All Linux winlaunch
race tests PASS after the fix, including these three refusals, ordinary project
hardlink sharing, network denial, setsid cleanup and Owner death cleanup. This
Linux runtime change requires its own final regression and rebuilt release.

Hosted CI on 020c0c0 passed both repeatable-package jobs. Its Windows ordinary
test exposed another real sharing race: Owner handoff could leave discovery
files because removal errors were discarded. Config replacement, administrator
credential replacement and discovery replacement/removal now reuse one small
bounded Windows sharing-retry utility. Discovery close is idempotent; Owner
close retains cleanup errors. A held-reader discovery test, handoff promotion,
control confirmation and password regressions PASS five native race repetitions.

The 404da64 hosted Windows suite reached its five-minute global package timeout
while starting a final test and syncing SQLite; there was no blocked test loop
or failed assertion in that timeout. Its ordinary suite allowance is now ten
minutes, matching the release workflow; product Run/service limits and test
assertions are unchanged. Matrix fail-fast is disabled so an independent Linux
job can finish and report its own outcome when Windows fails.

Source d39fb3c passed Windows ordinary/full native race, Debian ordinary/vet/full
race (harness 409.756s), both repeatable archive builds, Windows CLI portable and
all sixteen Debian portable checks. Its native GUI retained both GUI/CLI turns
and the Echo configuration after closing and reopening. Hosted Windows race
reported 1408ms of real active work against a one-second approval test allowance;
the paused clock itself was empty. Approval coverage now directly verifies that
persisted elapsed time remains unchanged throughout the actual wait, allowing
bounded active I/O overhead; the separate one-second deadline test is retained.

Final private-state review reproduced file-tool access to WebView2 session
storage outside the Harness directory, and Windows short-name access to that
same protected directory. Application state now includes the enclosing portable
data or per-user config/cache directories and explicit data/key overrides, with
shared enforcement in the file broker and sandbox launch. Broad-root Windows
paths resolve short names; application directories containing a workspace are
rejected. Parent grants over absent private paths or individually replaceable
private files fail closed. Read/edit/delete/create/export/search and an actual
AppContainer read refusal have focused regressions. The crash helper now supplies
isolated application-directory environment values, without widening the MCP
environment whitelist. Five native race repetitions of the privacy, crash and
approval-clock regressions PASS. Final full regression and rebuilt archives are
required for this runtime change. The real eight-hour retry remains RUNNING.

The hosted runner's `RUNNER~1` paths revealed another actual alias defect:
normalizing private state without normalizing grant comparisons could leave its
AppContainer parent grant readable. A local test using a real short-name parent
reproduced the disclosure. Both launcher and Harness checks now compare physical
paths, including nonexistent suffixes, while retaining the caller's logical
workspace spelling in file labels and authorization. Nested private grants are
checked even after another grant already contains the private directory.
Blocked file-tool paths also reject short-name aliases. Actual short-parent
isolation, missing-state creation, blocked alias and nested/missing grant tests
are added. Ubuntu's missing optional Bubblewrap dependency marks the actual
sandbox privacy test skipped; existing unsupported/no-host-fallback tests remain
mandatory, and actual Debian sandbox acceptance stays a separate required gate.
