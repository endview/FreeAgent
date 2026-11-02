# FreeAgent Linux portable preview

Candidate: v7.7.0-dev.1, Linux amd64. CLI plus an embedded loopback Web UI;
the native Windows desktop/WebView2 executable is not included. Go, Node,
Python and a system web server are not needed to run this package.

## Start

Verify the outer SHA256SUMS.txt, extract the complete archive into a writable
directory, then start:

```sh
tar -xzf FreeAgent-v7.7.0-dev.1-linux-amd64-portable.tar.gz
cd FreeAgent-v7.7.0-dev.1-linux-amd64-portable
./FreeAgent
```

The first start creates `data/Harness-v7` and `workspace`. Open the printed
`http://127.0.0.1:PORT/#key=...` address in a browser on the same computer.
Keep that address private. Ctrl+C or SIGTERM stops the Owner and its owned
commands/services. Closing a browser tab does not stop execution.

The package starts without a model. Add a Provider key and model in the Web UI.
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

The `FreeAgent` launcher also accepts `web` or `chat` plus normal arguments,
initializing the selected data directory when it has no configuration. Other
subcommands pass directly to `bin/freeagent`. The binary itself retains explicit
CLI initialization semantics; run `./FreeAgent` first or use `init --workspace`.
While an Owner is running, use the Web end-task action or `/abandon` inside
interactive `chat` to end an uncertain task. The one-shot `abandon --run` command
currently requires the Owner to be stopped.

## Linux execution limits

The binary is built with CGO disabled; it does not need a matching system glibc.
It still needs a supported Linux kernel and normal file/process/socket support.
Do not infer compatibility with untested architectures or distributions.

The Windows AppContainer sandbox is NOT_SUPPORTED on Linux. Sandboxed command
requests refuse; there is no automatic host fallback. Explicit Full/host mode
can execute allowed commands as the current OS user. Prefer a normal user and
grant Full only deliberately. File publication locking, bounded output, stop,
runtime receipts and UNKNOWN handling remain shared with the core. Linux
crash-time process-tree cleanup is not a guaranteed containment boundary.

Command rules and the executable allowlist still apply in Full. Debian's
`/bin/sh` is a symlink; when configuring an exact executable allowlist, use
`readlink -f /bin/sh` to obtain its actual regular file (normally `/usr/bin/dash`).
Permit that exact executable in command rules, enable command tools, and grant
the space Full before submitting a command. Full does not enable disabled tools.

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
