# FreeAgent Windows Portable Preview

For the separate Linux amd64 CLI/Web archive, see [Linux portable](https://github.com/endview/FreeAgent/blob/main/docs/LINUX_PORTABLE.md).

Current candidate: `v8.3.0-dev.1`, Windows amd64. This is an unsigned
development preview, not an installer or a certified release. Go and Node are
not required to run it. The Chat UI and KaTeX assets are bundled with the engine.

Settings includes optional Telegram, Lark/Feishu and official QQ connections.
Save channel credentials under Settings → Connections → Channels, select the matching account,
then enable it in Settings. Telegram uses /start applications and administrator approval; Lark/QQ retain pairing. See [Channel setup](https://github.com/endview/FreeAgent/blob/main/docs/CHANNELS.md).
The Owner Telegram private chat was verified in v8.0; multi-account/group and
real Lark/QQ acceptance remain NOT_RUN. This package is a development preview. Two foreground conversations share one workspace; command and service
quotas do not increase. Global configuration commits require idle execution,
while permission reductions still take effect immediately.

## Start

Extract the entire ZIP to a writable directory, then double-click
`FreeAgent.exe`. No console window is opened. The package creates its own
`data\Harness-pg-v1` store and `workspace\` directory on first start.

The desktop app uses Microsoft WebView2 when it is installed. If WebView2 is
unavailable, it opens the same local Chat in the default browser and shows a
notice. Closing the browser or notice does not cancel work. Use
`bin\freeagent.exe owner status` to inspect the Owner and
`bin\freeagent.exe owner stop` to stop it explicitly. The launch address and
token are private to that local process.

The first start has no configured model. Open Key management, save a Provider
profile, explicitly enable its switch, and add/select a model. Test connection
and model discovery are optional explicit requests; saving or enabling a
Provider makes no request. Keys are stored in
`data\keys.json`, encrypted with Windows DPAPI for the current Windows user.
Another Windows user or another computer must enter the Key again.

Stop does not undo completed side effects. An interrupted request can remain
UNKNOWN and is never automatically replayed.

The conversation header's context button provides manual Compact, Refresh and
Clear. `/compact` works in both Chat surfaces; the one-shot CLI is
`bin\freeagent.exe context compact --session <id>`. Compact and Refresh call
the selected model and consume its quota. Clear removes only the saved summary.
All original messages remain inspectable. New installations enable automatic
compaction with a 4096-byte summary. Existing configurations with a zero or
omitted `task_summary_bytes` remain off. Use Settings or
`config context --summary-bytes 4096` to enable it; 0 disables automatic
compaction without deleting an existing summary. These settings do not change
the limits already frozen into a Run.

Before updating an existing current-format installation, back up and resolve
unfinished command Runs: pre-v7.12 command snapshots cannot resume under the
changed rule interpretation. Unmatched rules follow the current permission
mode; explicit deny/ask rules and executable allowlists still apply in Full.
Telegram spaces/history remain, but access requires administrator approval in
the new Telegram page. Old `/pair` codes grant no access. See `docs/CHANNELS.md`.

The composer accepts text/code and verified PNG/JPEG attachments. Explicit
uploads can be read in None; image understanding requires a vision-capable
model and an enabled local declaration. Download published file results from
their cards, or save an assistant reply as Markdown. Existing permission rules
still protect computer files and exports. See `docs\ATTACHMENTS.md` in the ZIP.
Windows uses AppContainer for sandboxed commands and explicit Full for host
execution. The optional public HTTPS entrance is available on Linux servers.

## Data And Workspace

The package uses the independent `data\Harness-pg-v1` directory. Old stores,
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

For installation/configuration problems, run `.\bin\freeagent.exe doctor --text`.
It shows independent local checks and next actions, including disabled Provider
switches. It makes no Provider request and does not change settings. JSON remains
available as `doctor` without `--text`; see `docs\RECOVERY.md`.

`bin\freeagent.exe` is the professional and scripting entry point. It supports
Key management, model configuration, `run`, `preview`, `confirm`, `doctor`,
backup/restore and the optional bounded tools and collaboration controls.
Examples:

```powershell
.\bin\freeagent.exe key add --name main --url 'https://your-provider.example/v1'
.\bin\freeagent.exe key test main
.\bin\freeagent.exe key models main
.\bin\freeagent.exe config model --id main --key main --model MODEL_NAME --default
.\bin\freeagent.exe key enable main
.\bin\freeagent.exe web --open-browser
```

With the desktop app running, `chat --resume <session>` attaches to its owner.
Use `sessions` to find the session ID, or `/sessions` and `/resume` inside
Chat. With no owner, `chat` publishes the same API in the terminal process so
the desktop app can attach. Closing an attached client leaves the owner running;
exiting the owning terminal drains and stops it. Drafts are not shared.

Windows command execution resolves native Scoop `.exe` + `.shim` pairs to a
concrete application version and grants that installation read-only access.
The final executable and complete arguments still pass command policy and
approval. Unknown metadata and script shims are rejected without host fallback.

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
Current package, Windows race, Debian, attachment/UI and HTTPS evidence and
unrun external tests are recorded in
[current acceptance](https://github.com/endview/FreeAgent/blob/main/docs/FREEAGENT_V7_11_EXECUTION_DIAGNOSTICS_PROGRESS.md).
The v7.0 live DeepSeek record is historical evidence, not acceptance of every
later Provider or visual feature.


Provider profiles are disabled by default. Saving a Key, enabling a Provider or
starting FreeAgent makes no discovery request. Use the Provider page switch or
`freeagent key enable NAME --data-dir DATA`; `key disable` prevents the next
model request without replaying an in-flight call. `key add --enable` explicitly
opts in. Models stay saved when the Provider is off; select an enabled model.
Connection tests remain explicit and may be run while the Provider is off.

`abandon --run ID` and `backup --out ABSOLUTE_PATH` also work while the GUI/Web
Owner is running, using its interactive attach capability. A backup takes the
existing exclusive snapshot boundary and reports busy during active execution.
Observer credentials cannot perform these mutations.


## Current native data and optional AffectWeave

Keep the existing Harness-pg-v1 directory during updates. Stop the old Owner and
verify a backup before replacing programs; do not initialize or import an old
SQLite store. Layout and drafts belong to the current client; secrets are held
in dedicated memory while editing.

The React workbench groups product controls under Settings. AffectWeave is a
separate optional download, imported under Settings → Capabilities → Extensions.
It requires explicit independent PG initialization and enablement; ordinary Core
startup does not run it. Character state and space sharing changes are previewed
and confirmed. See docs/EXTENSION_DEVELOPMENT_GUIDE.md and
[the AffectWeave repository](https://github.com/endview/affectweave).
