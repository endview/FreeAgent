# Known limitations

FreeAgent `v0.1.1` is an early Developer Preview for local evaluation.

- There is no production support or availability, durability, compatibility,
  or upgrade SLA.
- Archives, SBOMs, provenance, and checksum manifests are unsigned. No package
  is code-signed, notarized, or published by this repository workflow.
- Windows and Linux packages require install-smoke evidence before they may be
  reported as runnable. Darwin packages are cross-built artifacts only unless
  native macOS evidence is supplied separately.
- The default bundled quickstart is the deterministic offline echo model. A
  real DeepSeek run is opt-in, requires a separately supplied runtime secret,
  may incur fees, and is outside the offline package smoke test.
- The HTTP chat and optional Control surfaces bind only to literal loopback
  addresses. They are not public network APIs and must not be exposed through
  a reverse proxy.
- Control is disabled by default, uses a process-local session, and requires an
  owner-only bootstrap handoff. The process refuses unsupported elevated or
  administrator execution contexts.
- The package has no installer, automatic updater, service manager, sandbox,
  migration assistant, shell completion, or automatic `PATH` integration.
- Runtime databases and artifact roots are operator-owned. Do not run two
  writers against one store, and stop the service before backup or restore.
- Module execution remains limited to the exact compiled and governed
  development slices documented by the project. This preview is not a general
  untrusted plug-in host.

Treat any failed checksum, version mismatch, incomplete evidence set, or smoke
test failure as a release failure. Do not infer success from the presence of an
archive alone.
