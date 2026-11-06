# FreeAgent Linux portable preview

Candidate: v8.3.0-dev.1, Linux amd64. CLI plus an embedded loopback Web UI;
the native Windows desktop/WebView2 executable is not included. Go, Node,
Python and a system web server are not needed to run this package.

## Start

Verify the outer SHA256SUMS.txt, extract the complete archive into a writable
directory, then start:

```sh
tar -xzf FreeAgent-v8.3.0-dev.1-linux-amd64-portable.tar.gz
cd FreeAgent-v8.3.0-dev.1-linux-amd64-portable
./FreeAgent
```

The first start creates `data/Harness-pg-v1` and `workspace`. Open the printed
`http://127.0.0.1:PORT/#key=...` address in a browser on the same computer.
Keep that address private. Ctrl+C or SIGTERM stops the Owner and its owned
commands/services. Closing a browser tab does not stop execution.

The package starts without a model. Add a Provider profile and model in the Web
UI, and explicitly enable the Provider switch. Saving/enabling does not probe
the network; connection tests and model discovery are explicit operations.
The default local space has None permission: it can chat but cannot operate
tools until explicitly granted. Keys live in `data/keys.json`. Linux saved
keys use a mode-0600 plaintext file; they are not Windows DPAPI-encrypted.
Environment-reference credentials are available through the existing key CLI.

For a headless server, leave the listener on loopback and forward its chosen
port through SSH. Use a private browser on your own computer. Do not expose the
Owner listener or launch token through a public reverse proxy.

```sh
# Run on your computer; substitute the printed remote port and SSH destination.
ssh -N -o ExitOnForwardFailure=yes -L 127.0.0.1:18765:127.0.0.1:REMOTE_PORT USER@SERVER
```

Open `http://127.0.0.1:18765/#key=PRINTED_KEY` locally. Different local and remote
ports are supported. Use the numeric loopback address, not `localhost`.
The local capability is sent in a header, never a cookie. Closing the SSH tunnel
does not stop the remote Owner; stop FreeAgent explicitly on the server.

For a persistent non-root systemd service, Python 3 is needed only for the
explicit installer:

```sh
# As the server administrator, from a clean extracted package:
python3 deploy/install-server.py --package "$PWD" --start
/usr/local/bin/freeagent owner status
```

See `docs/SERVER_DEPLOYMENT.md` for startup, logs, backup and safe updates.
Optional public Web stays off until locally configured with an administrator
password, exact HTTPS Origin and restricted Unix socket. Caddy supplies TLS;
the certificate must cover the actual domain or IP. Follow `docs/PUBLIC_WEB.md`
instead of forwarding the private Owner routes or token. Remote maximum
permission defaults to Full and intersects the space/requester's permission;
new spaces still default to None. The CLI remains a local recovery entrance.

## CLI and shared records

```sh
./bin/freeagent --version
./bin/freeagent chat
./bin/freeagent sessions
./bin/freeagent run --session SESSION_ID 'Continue the same conversation'
./bin/freeagent channel telegram status
```

CLI attaches to an existing Owner. Web and CLI use the same space, conversation,
permission, Run and approval records. Two foreground conversations share one
workspace and the same Owner quotas. No old-version migration is performed.

New installations enable automatic compaction with a 4096-byte summary. Existing
zero/omitted `task_summary_bytes` settings remain off. Use Settings or
`config context --summary-bytes 4096`; 0 disables automatic compaction and keeps
saved summaries. A Run retains its frozen limits.

Before updating current-format server data, back up and resolve unfinished
command Runs. Pre-v7.12 command snapshots cannot resume under the changed rule
interpretation: unmatched rules now follow the permission mode, while explicit
deny/ask and executable allowlists continue to constrain Full. Existing Telegram
spaces and history remain, but an administrator must approve access in the new
Telegram page; old `/pair` codes grant no access. See `docs/CHANNELS.md`.

The `FreeAgent` launcher also accepts `web` or `chat` plus normal arguments,
initializing the selected data directory when it has no configuration. Other
subcommands pass directly to `bin/freeagent`. The binary itself retains explicit
CLI initialization semantics; run `./FreeAgent` first or use `init --workspace`.
`abandon --run ID` and online `backup --out ABSOLUTE_PATH` reuse authenticated
interactive attach while the Owner is running. Backup reports busy during
active execution; it does not silently make a weaker copy. Observer credentials
cannot mutate or back up the instance.

The composer and CLI accept bounded text/code and validated PNG/JPEG inputs.
Explicit uploads work in None; images require declared model vision support.
Use `/attach`, `/attachments`, `/save-reply`, the `attachments` CLI commands or
GUI file cards to exchange results. `file_export` obeys the current file policy
and publishes an immutable snapshot. See `docs/ATTACHMENTS.md`.

## Linux execution limits

The binary is built with CGO disabled; it does not need a matching system glibc.
It still needs a supported Linux kernel and normal file/process/socket support.
Do not infer compatibility with untested architectures or distributions.

Debian 12 amd64 is the acceptance platform. Optional Bubblewrap isolation
requires a root-owned non-setuid `/usr/bin/bwrap`, working user/PID/mount/network
namespaces and kernel pidfds. Install `bubblewrap` explicitly as the
administrator, then inspect `./bin/freeagent doctor --text`. Missing capabilities refuse
sandbox dispatch without host fallback. Ask uses read-only authorization;
Workspace permits the selected writable roots. Protected application state is
hidden, `.git` is read-only, and sandbox processes use isolated HOME/tmp.

Linux sandbox Internet and host-visible sandbox service ports are NOT_SUPPORTED.
Explicit Full/host can execute commands with the current OS user's authority;
it has no sandbox guarantee. Ordinary host process-group cleanup cannot contain
a process deliberately escaping that group. Bubblewrap uses an owned PID
namespace and pidfds; the installed service also has its own systemd cgroup.
Stop cannot undo effects. Unproven cleanup remains UNKNOWN and is not replayed.

Command rules and executable allowlists still apply in Full. Normal Debian
program aliases such as `/bin/sh` resolve before launch; `readlink -f /bin/sh`
shows its regular target (normally `/usr/bin/dash`). Enable command tools and
grant the required space permission before submission. Workspace is sufficient
for supported sandbox work; Full explicitly selects host execution. Permission
does not enable a disabled tool. Ordinary project hardlinks can share writable
inodes outside the selected tree; use copied dependencies when isolation of
those files is required. See `docs/SERVER_DEPLOYMENT.md` for the full boundary.

Telegram/Lark/QQ are optional and disabled by default. See `docs/CHANNELS.md`.
Real platform and Provider acceptance are independent of offline portable
acceptance. No credentials or user data are shipped.


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
