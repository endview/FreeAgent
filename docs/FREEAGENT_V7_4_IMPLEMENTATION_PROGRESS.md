# FreeAgent v7.4 Implementation Record

Date: 2026-10-25. Baseline: v7.3 `04032f5`.
Status: implementation complete; final platform/package gates in progress.

## Implemented

- One supervisor for background commands and services; no Service table,
  scheduler, task subsystem, compatibility layer or process replay.
- Explicit service policy, 1800-second default and 28800-second original total
  hard cap; two services plus one ordinary command per owner. Complete profile
  and installation set compatibility, affected-ID busy errors and counted gates.
- Named AppContainer grants/protections use shared exact-signature leases.
  Last-reference cleanup and cleanup errors are explicit. No ACL restoration
  runs while the associated tree can still use that process's grant.
- Monotonic expiry without polling; exact revision/key/deadline extension with
  finite 64-receipt bound; AI extension confirmation even Full. No renewal of
  expired/stopping processes. Normal stop grace is capped by remaining lifetime.
- Root exit, controlled-tree termination, output drain and storage success stay
  separate. UNKNOWN holds capacity. Missing startup projection is reconstructed
  from the original dispatch Attempt, without rewriting or replaying it.
- Literal readiness matching across chunks; bounded UTF-8 stdout/stderr tail,
  256 KiB retained, 16 KiB pages, honest gaps and split-credential redaction.
- GUI start/list/log/extend/Stop and command policy controls; attached one-shot
  CLI and `/services` use the same owner as AI command tools. Raw execution JSON
  remains in evidence; direct user commands have a concise chat presentation.

## Actual Verification

PASS: real HTTP services, browser/host access and concurrent actual check command;
owner-wide quotas, exact extension/dedup/stale/total bounds, readiness timeout,
recovery, immutable parent/Attempt, backup/restore, failed startup/terminal
persistence, current uncertainty gates, concurrent extension/Stop and UTF-8
cursor/credential cases. Controlled tests cover 8h limits and wall clock rollback.

PASS: shared Windows grant/protection overlap, incompatible network rejection,
failed launch rollback, interleaved close and owner-kill Job termination.
Native installed Scoop Git 2.51.0 resolved to its real version directory;
two sandbox runs took 2.830s and 1.901s including grant/observation work.
The separate 3002-entry kept-grant fixture measured 334.7ms first launch,
32.0ms reuse and 326.2ms release. These are measurements, not latency promises.

PASS: burst output produced about 6.55 MB with exactly 262144 bytes retained and
a 16384-byte page. Sustained-output verification is included in final tests.

PASS: native WebView2 (Edg 155.0.4283.62, Windows 11 Pro 10.0.26300) start,
readiness/list/logs, extension, page reload to the same service ID, confirmed
Stop and clean window close. A real portable CLI inspected the same command
and executed a parallel HTTP check; an Edge browser displayed its actual body.
These checks used an isolated Echo configuration and no real Provider credentials.
Later display-only polishing is checked again with the final package.

PASS: 74 ChatUI JS tests, production build, vet and notices. Full ordinary
Windows tests passed before final fault-path additions; their focused repeats
passed. A full Windows race run had intermittent existing loopback TCP timeouts
in the CLI package, without race reports; its complete package repeat passed
(55.523s). Other packages passed, including Harness (212.753s). Final recovery
and cursor changes are receiving another complete Harness race and ordinary run.
No failed attempt is counted as a pass.

## Limits and Pending Evidence

| Scope | Result |
|---|---|
| Windows Full/host IPv4 service, parallel check and browser | PASS; same-user escape hatch |
| AppContainer off/internet, same-profile command to server | PASS in raw platform probe |
| AppContainer off/internet, host to server | NOT_SUPPORTED: actual host HTTP timeout; no exemptions added |
| Product sandbox service start | Rejected as NOT_SUPPORTED; never falls back to Full |
| IPv6 service network matrix | NOT_RUN |
| Linux Full/group cleanup and race | Awaiting v7.4 commit CI |
| Linux crash cleanup/host escape containment | Not guaranteed by this backend |
| Actual eight-hour endurance/soak | NOT_RUN; simulated limits are labeled separately |
| Real Providers/Responses/search reacceptance | NOT_RUN; unrelated endpoints were not called |
| Production signing | Unsigned development preview |

Final portable integrity/clean-start evidence, clean source commit and CI links
will be recorded at the release gate. The intermediate native UI archive is
dirty by design and is not the published release artifact.
