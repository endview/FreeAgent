# FreeAgent v0.1.1

FreeAgent v0.1.1 is the first public release of the current P0-P4
development line. It remains an early Developer Preview for local evaluation;
this release does not claim production support, an availability SLA, or
general third-party module compatibility.

## What changed

### P0: remove money and budget semantics

- Removed the runtime budget, price, cost-estimation, and cost-reconciliation
  path from the FAC2 execution model.
- Kept the controls that bound execution without inventing monetary facts:
  token usage, output limits, deadlines, permissions, `UNKNOWN` reconciliation,
  and exact retry semantics.
- Added the FAC2 migration and release gates that reject stale FAC1 stores
  instead of silently rewriting them.

### W6.6: server-owned Upgrade Review

- Upgrade Review now consumes an inert, content-addressed Artifact held by the
  server-side Current Store.
- Review and Decision are durable, retry-stable, tenant-scoped, and fail
  closed on stale source/head state or physical tampering.
- Callers cannot supply artifact paths, URLs, signatures, or target facts.
- Review never installs, activates, binds, grants, applies, or executes a
  module.

### P2: Control UI internationalization

- Completed the existing Control UI catalog for `zh-CN` and `en-US`.
- Added catalog parity, interpolation, fallback, and untranslated-string
  static checks.
- Kept server facts, identifiers, digests, and protocol enums unchanged while
  localizing user-facing labels and formatting.

### P3: controlled Zhipu provider

- Added the opt-in `zhipu` Provider for the exact compiled
  `zhipu` / `glm-4.5` support binding.
- Covered non-streaming and SSE protocol parsing, bounded accumulation,
  missing usage, cancellation, truncation, `FAILED`, and `UNKNOWN` terminal
  states.
- Validated the provider through production composition and the Universal Loop
  with a redacted real experiment.
- This release does not support arbitrary endpoints, arbitrary GLM model
  selection, automatic model selection, cross-Provider fallback, Actions,
  vision, or user-visible streaming UI.

### P4: read-only module management

- Added read-only management projections for Upgrade Review and Decision,
  `UNKNOWN` attempts, Store verification, backup constraints, Artifact
  Admission, and server-owned Artifacts.
- Kept install, activate, bind, grant, apply, execute, online backup creation,
  and online restore outside the UI.
- Reused the existing application/control service so the UI does not create a
  second set of business rules.

### Release engineering

- Refreshed generated runtime facts and capability projections.
- Added the current release payload and checksum verification guidance.
- Revalidated Windows and Linux AMD64 offline install, exact retry, service
  restart, backup, verify, restore, and continue workflows.
- Revalidated the six-target archive set; Darwin artifacts remain build-only.

## Known limitations

- This package is intended for local evaluation and does not provide production support.
- Archives, SBOMs, provenance, and checksum manifests are unsigned.
- Windows and Linux AMD64 have native install evidence. ARM64 targets are
  cross-built. Darwin targets are build-only.
- The default quickstart uses the deterministic offline echo model.
- Real Provider access is opt-in and requires an operator-supplied secret.
- The Zhipu support claim is limited to the exact `glm-4.5` binding above.
- Control and HTTP surfaces are loopback-only and disabled by default where
  documented.
- No automatic updater, installer, service manager, sandbox, or in-place
  migration assistant is included.

## Upgrade guidance

Verify the adjacent `SHA256SUMS` file, extract the package into a new
directory, and confirm `--version` before use. Keep the previous package and a
verified backup until the new version has completed the documented offline
workflow. See `INSTALL.md`, `QUICKSTART.md`, and `KNOWN_LIMITATIONS.md` in the
archive for the full procedure.
