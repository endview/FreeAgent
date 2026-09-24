# Install the Developer Preview

This document applies to FreeAgent `v0.1.1`. It is an early Developer Preview,
not a production-support distribution.

## Choose a package

Use the archive whose target matches the machine that will run it:

- Windows: `freeagent-v0.1.1-windows-amd64.zip` or
  `freeagent-v0.1.1-windows-arm64.zip`
- Linux: `freeagent-v0.1.1-linux-amd64.tar.gz` or
  `freeagent-v0.1.1-linux-arm64.tar.gz`
- macOS build-only: `freeagent-v0.1.1-darwin-amd64-build-only.tar.gz`
  or `freeagent-v0.1.1-darwin-arm64-build-only.tar.gz`

Obtain the archive and adjacent `SHA256SUMS` from the same
[v0.1.1 GitHub Release](https://github.com/endview/FreeAgent/releases/tag/v0.1.1).
Do not mix files from different releases or local build directories.
Verify the archive before extracting it; see `VERIFY_CHECKSUMS.md` for exact
commands. These checksums detect accidental or post-build changes but are not
signatures.

Extract into a new directory. Do not merge packages for different targets.
Each archive has one top-level directory named after its version and target.

On Windows PowerShell:

```powershell
Expand-Archive .\freeagent-v0.1.1-windows-amd64.zip -DestinationPath .\freeagent-preview
Set-Location .\freeagent-preview\freeagent-v0.1.1-windows-amd64
.\bin\freeagent-windows-amd64.exe --version
```

On Linux or macOS:

```sh
mkdir freeagent-preview
tar -xzf freeagent-v0.1.1-linux-amd64.tar.gz -C freeagent-preview
cd freeagent-preview/freeagent-v0.1.1-linux-amd64
./bin/freeagent-linux-amd64 --version
```

The version line must report `version=v0.1.1`, the 40-character release
commit, and the selected target. A mismatch means the package must not be used.

The package does not modify `PATH`, install a service, or write outside paths
provided on the command line. Keep the extracted package read-only if desired
and put runtime state in a separate operator-owned directory. The bundled
`data` directory is only a documented location convention; FreeAgent creates
the database, artifact root, backups, and runtime handoff only when explicitly
asked to do so.

Continue with `QUICKSTART.md` after the version and checksum checks pass.

## Upgrade

FreeAgent upgrades are side-by-side replacements; there is no in-place
installer. Verify and extract the new package into a new directory, confirm its
`--version` output, stop any running FreeAgent process, and run the new binary
against a copied data directory first. Keep the previous package, database, and
verified backup until the new version has completed the documented offline
workflow and reopened the restored database successfully.

Do not overwrite an existing extracted package or reuse a package directory as
a data directory. Configuration and data formats may change during the
release series; read the new package's `RELEASE_NOTES.md` and
`KNOWN_LIMITATIONS.md` before switching.

## Uninstall

FreeAgent does not register a service, modify `PATH`, or install files outside
the paths supplied by the operator. Stop all FreeAgent processes, preserve any
database or backup that must be retained, then remove the extracted package
directory. Remove the operator-owned data, artifact, backup, and handoff paths
separately only after confirming they are no longer needed.

The macOS archives are labeled `build-only` because this release process
cross-compiles them but does not claim native macOS install validation.
