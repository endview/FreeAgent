# Linux server deployment (Debian 12)

Portable mode remains available. The optional systemd installer uses a dedicated
non-root user, an explicit data directory and a separate mode-0600 key file.
It does not expose the Owner listener on the public network.

Extract a clean Linux release (without its own data/workspace), then as root:

```sh
python3 deploy/install-server.py --package "$PWD" --start
/usr/local/bin/freeagent owner status
/usr/local/bin/freeagent owner url
```

The management command runs as the service user. It sets `FREEAGENT_DATA_DIR`
and `FREEAGENT_KEYS_FILE`; an explicit `--data-dir` still overrides the former.
Do not run the raw CLI as root against service-owned files: it refuses the
ownership mismatch. `web --service` itself refuses root. The default installation
uses `/opt/freeagent/releases/VERSION`, `/opt/freeagent/current`,
`/var/lib/freeagent/data/Harness-v8`, `/var/lib/freeagent/workspace` and
`/var/lib/freeagent/keys.json`. Runtime files use `/run/freeagent`.

Use `systemctl status/start/stop/restart freeagent` and
`journalctl -u freeagent`. Service logs contain readiness/generation and errors,
never the private launch URL. `systemctl active` does not certify a Provider or
channel; check `freeagent owner status` for Owner readiness. `owner stop` requests
draining/cancellation through the existing handoff; it does not undo effects.

For remote use before the optional public entrance is enabled:

```sh
ssh -N -o ExitOnForwardFailure=yes -L 127.0.0.1:18765:127.0.0.1:8765 USER@SERVER
```

Open the private `owner url` value using local port 18765. Numeric loopback Host
and exact Origin are required; local and server ports may differ. Keep the
fragment capability private. Closing SSH does not stop a system service.

The installer refuses unknown/custom units, managers, accounts and existing
release directories. `--user`, `--unit`, `--prefix`, `--manager` and `--port`
allow a separate validation installation; the program prefix must be under
`/opt`, with its manager under `/usr/local/bin`. The chosen port is stored through
`server.set_policy`. GUI Settings and CLI/AI control proposals use that same
operation. Changes require an explicit Owner restart; no automatic restart or
account privilege change is implied. A direct `web --port` is a launch override.

For updates: settle/stop active work, create and verify an online backup, stop
the unit and confirm its cgroup is empty, install/select a new clean program,
then start and inspect readiness. Retain the earlier program and its own data.
Uninstalling a unit never authorizes deletion of data. The v7.9 attachment
release deliberately uses a new data format/directory; v7.8 data is a preview
and will not be migrated.

## Optional Linux command sandbox

Install Debian `bubblewrap` explicitly (`apt-get install bubblewrap`), then run
`freeagent doctor`. A root-owned, non-setuid `/usr/bin/bwrap`, working user/PID/
mount/network namespaces, namespace restriction support and kernel pidfds are
required. A missing dependency or denied namespace is NOT_SUPPORTED; a sandbox
request fails without host fallback. Provider/transport networking is separate.

Ask uses a read-only workspace; Workspace uses authorized writable roots. System
programs/libraries and the declared installation are read-only; private HOME,
host `/run`, agents/sockets and application state are not mounted. Environment
variables use a small whitelist. `.git` and `.freeagent` are read-only, including
when a missing metadata directory needs a guard before launch. Sandbox `git init`
and commits are unavailable; use an explicit Full host operation for those.
Linux sandbox Internet and host-visible service ports are NOT_SUPPORTED in this
release. Full is an explicit same-user host escape hatch, limited by the service
user and outer systemd restrictions; it is never described as root/admin access.

Private state includes the application's portable `data` directory or per-user
FreeAgent config/cache directories, plus explicit data/key overrides. Selecting
a parent root never authorizes browser session storage or management credentials.
Choose a narrower root when a grant overlaps absent application state or an
individually replaceable private file. An explicit Full host operation keeps its
documented same-user authority.

Protected application files must each have exactly one hardlink. Preflight
checks private-directory regular files, including administrator credentials,
attachments and blocked directory contents. It does not read their bodies or
follow directory symlinks. The check is cancellable and capped at 100,000
entries per private directory; unreadable or over-limit trees refuse sandbox
dispatch instead of falling back to host execution. Ordinary
project hardlinks remain supported: editing a pnpm/Git shared inode also changes
its host aliases. Use copied dependencies when that sharing is unwanted. Trusted
host activity can change mount sources concurrently; the sandbox does not claim
to resist an administrator modifying the filesystem outside isolation.

The provided unit keeps `NoNewPrivileges`, `PrivateTmp`, `ProtectSystem=strict`
and `ProtectHome`, and includes AF_NETLINK. It does not forbid namespaces/mounts
or JIT executable memory. Additional writable roots need explicit
`ReadWritePaths=` entries in the OS unit as well as the Harness grant. Actual
availability and failures are shown in Settings and `doctor`.
