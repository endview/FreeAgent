# FreeAgent Windows Portable Preview

Current candidate: `v7.0.0-dev.1`, Windows amd64. This is an unsigned
development preview, not an installer or a certified release. Go and Node are
not required to run it. The Chat UI and KaTeX assets are embedded in the
desktop executable.

## Start

Extract the entire ZIP to a writable directory, then double-click
`FreeAgent.exe`. No console window is opened. The package creates its own
`data\Harness-v5` store and `workspace\` directory on first start.

The desktop app uses Microsoft WebView2 when it is installed. If WebView2 is
unavailable, it opens the same local Chat in the default browser and shows a
stop prompt. Closing that prompt stops FreeAgent. The launch address and token
are private to that local process.

The first start has no configured model. Open the Key page, add a Key, test it,
fetch the provider's models, and enable one. Keys are stored in
`data\keys.json`, encrypted with Windows DPAPI for the current Windows user.
Another Windows user or another computer must enter the Key again.

Stop does not undo completed side effects. An interrupted request can remain
UNKNOWN and is never automatically replayed.

## Data And Workspace

The package uses the independent `data\Harness-v5` directory. Old stores,
backups and configurations are not imported, upgraded or removed. The default
workspace is `workspace\` beside the desktop executable.

The CLI can change the workspace when no Run is unfinished:

```powershell
.\bin\freeagent.exe config workspace --path 'C:\Projects\MyProject'
```

Workspace paths are absolute. If a moved package cannot start because its
saved workspace no longer exists, use the command above. Keep the package
outside shared or hostile writable directories.

## Command Line

`bin\freeagent.exe` is the professional and scripting entry point. It supports
Key management, model configuration, `run`, `preview`, `confirm`, `doctor`,
backup/restore and the optional bounded tools and collaboration controls.
Examples:

```powershell
.\bin\freeagent.exe key add --name main --url 'https://your-provider.example/v1'
.\bin\freeagent.exe key test main
.\bin\freeagent.exe key models main
.\bin\freeagent.exe web --open-browser
```

Double-clicking `bin\freeagent.exe` only displays a message pointing to
`FreeAgent.exe`. Run it from a terminal for CLI commands. Detailed options are
in `docs\CONFIGURATION.md`.

## Verification

`manifest.json` identifies the candidate, target, source base commit, dirty
source status, source digest and payload hashes. The package is not signed.
`SHA256SUMS.txt` inside the ZIP covers every payload and the manifest; the
adjacent checksum file covers the archive. Checksums detect corruption and are
not a publisher signature.

The live Provider evidence is maintained in the repository, not distributed
in the package. One Provider and one model passing does not certify all
Providers, model quality or every external failure mode.
The v7.0 live DeepSeek results, packaged CLI checklist, native Windows race
run and manual desktop checks are recorded in
`FREEAGENT_V7_0_IMPLEMENTATION_PROGRESS.md` in the repository.
