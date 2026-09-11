# FreeAgent v0.1.0-dev.2

This Developer Preview is a release-readiness update. It packages the existing
development slices and does not add a runtime feature wave.

Release-readiness changes:

- extend the generic persistence grace window from 5 to 10 seconds and the run
  lease-tail grace from 30 to 40 seconds;
- support Windows long executable and working-directory paths when starting
  MCP stdio subprocesses, including a temporary DOS-device mapping when short
  paths are unavailable;
- extend the Linux race-test timeout to 120 minutes with a 135-minute runner
  budget;
- refresh authenticated workflow-controller pins for the updated job script.

Windows and Linux AMD64 packages require native install validation before they
are reported as runnable. ARM64 packages are cross-built. Darwin packages are
build-only unless independent native evidence is attached. Archives, SBOMs,
provenance, and checksum manifests remain unsigned.
