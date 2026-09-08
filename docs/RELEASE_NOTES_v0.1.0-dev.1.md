# FreeAgent v0.1.0-dev.1

This Developer Preview packages the existing FreeAgent development slices into
target-specific archives. It does not add a new product feature wave.

Release packaging adds:

- deterministic `freeagent --version` metadata bound to version, commit, and
  target;
- Windows, Linux, and Darwin AMD64/ARM64 build targets;
- package-local install, quickstart, limitation, legal, configuration, and data
  guidance;
- a bundled offline bootstrap seed and its exact deterministic echo artifacts;
- SPDX SBOM, unsigned provenance, sealed-payload checksums, archive checksums,
  and install-smoke tooling.

Windows and Linux runnable status depends on passing native install validation.
Darwin output is build-only in this release unless independent native evidence
is attached. No remote tag, push, hosted download, signing, notarization, or
public release is performed by preparing these local artifacts.
