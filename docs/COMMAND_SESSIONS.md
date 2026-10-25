# Owner-bound commands

`command_run` accepts `background: true` for a finite command owned by the
current GUI/web/chat engine. Its original tool Attempt is the command ID.
Startup completes its Run; the evolving process projection does not rewrite
that Attempt, tool message or completed Run. A background process is not a
completed check. Collaboration cannot detach commands.

The command panel in the chat header starts, lists, reads and stops the same
commands as `freeagent commands` and `/commands`. CLI example:

```powershell
freeagent commands start --session <id> --arguments '{"command":"node","args":["server.js"],"cwd":".","background":true}' --interactive
freeagent commands list --json
freeagent commands poll --id <attempt> --cursor 0 --json
freeagent commands stop --id <attempt>
```

All commands may use `--data-dir`. Keep the owning GUI/web/chat running;
one-shot start does not create a temporary detached owner. Ask uses the normal
Preview/Confirm flow. A pending CLI start without `--interactive` can be
confirmed in the GUI or with `preview`/`confirm`. AI cross-conversation Stop
requires confirmation. Direct authenticated Stop remains available independently
of the command enable switch and the host control gate.

Ordinary commands allow one active foreground or background command, with the
existing finite command timeout (at most 600 seconds). Output is continuously
drained into a bounded UTF-8 tail; pages are at most 16 KiB, with stream labels,
byte cursors and explicit gaps. It does not write a row per poll.

Owner shutdown/handoff stops its commands. Windows uses an owned kill-on-close
Job and verifies its active-process count; Linux uses a process group and cannot
promise cleanup after owner crash. PID and owner generation are diagnostic,
never credentials for attaching or killing a recovered process.

Recovery marks unfinished projections UNKNOWN, without reopening the parent
Run or replaying effects. New commands require explicit review of those exact
records even in Full. Uncertain cleanup retains the current owner's slot.
Stopping a process cannot undo file or external effects.

## Finite Services (v7.4)

Services use the same supervisor, original Attempt ID and bounded meta record.
They are available in explicitly selected Full/host mode. Enable command tools
and `commands.services.allow_local_listen` in Settings first. That setting is
an explicit request to allow service starts; Full cannot enforce a loopback-only
listener. Bind the server to `127.0.0.1` in its own arguments.

`command_run` accepts `mode: "service"`, optional `duration_seconds`, a literal
`ready_text` (at most 1024 UTF-8 bytes) and `ready_timeout_seconds`. The default
duration is 1800 seconds; the original total cap is 28800 seconds and may be
lowered. Readiness defaults to min(60 seconds, duration), at most 300 seconds.
Observing the text is separate from actually verifying HTTP reachability.
Services cannot be started by collaboration parents or children.

An owner permits two services plus one ordinary command. They must share the
same complete execution profile and pre-resolved installation directory set.
Authorization changes are blocked with affected command IDs until the commands
stop. Processes that have uncertain cleanup or persistence keep their slots;
current-owner uncertainty blocks new starts until inspected and owner restart.

The GUI command panel, `/services` and `freeagent services` are views of the
same owner. CLI extension uses an exact previously read revision and deadline:

```powershell
freeagent services poll --id <attempt> --json
freeagent services extend --id <attempt> --revision <n> --expires-at <RFC3339-target> --request <unique-key> --json
freeagent services stop --id <attempt>
```

Interactive CLI also accepts `/services extend <id> <additional-seconds>`.
AI uses `command_extend` and always needs normal user Preview/Confirm, including
Full. A stopped or expiring process cannot be renewed. Extension is finite
from its original start; wall clock changes never give it a fresh allocation.
Accepted request keys are retained in the existing projection, with at most 64
committed extensions per service. No receipt is discarded to permit key reuse.

Each service drains stdout/stderr into a combined latest 256 KiB tail. Output
pages are at most 16 KiB. Cursors identify observed UTF-8 bytes; gaps, lost logs
after restart and invalid partial characters remain explicit. Known key and
credential environment values are redacted across output chunks. This does
not promise detection of arbitrary business secrets. No row is written per poll.

Stop requests the platform termination mechanism, then forcibly cleans up the
controlled tree. Normal grace is up to five seconds, capped by remaining service
time. At expiry there is no extra grace allocation. Root and tree confirmation
have a five-second bound. Windows records best-effort CTRL_BREAK followed by
Job termination; that is not a promise of an application-level graceful shutdown.
An uncertain result stays UNKNOWN. Owner shutdown requests all stops before
waiting, so grace windows do not add up for concurrent services.

On recovery, even a command missing its first meta projection is recovered as
UNKNOWN from the existing dispatched Attempt. Neither its parent Run nor its
original message/Attempt is reopened or rewritten, and no old PID is attached
or killed. Only a newly confirmed request can start new work.

## Platform Support

Windows 11 build 26300: Full/host HTTP service, browser access, parallel test
command, GUI/CLI continuity and native WebView2 controls passed. AppContainer
network off and internet allowed same-profile command access, but host HTTP
access timed out without loopback exemptions. Sandbox service requests therefore
return NOT_SUPPORTED; there is no automatic privilege switch or exemption.
IPv6 service reachability was NOT_RUN and is not inferred from IPv4.

Windows shared-profile grants and protections use exact-signature leases;
one exit or failed start cannot revoke another process's authorization. Actual
Job owner-kill testing confirmed controlled child termination.

Linux supports explicit Full/host with active process-group cleanup. It does
not guarantee cleanup after owner crash or containment of a deliberately
escaping host process. A restart reports uncertainty and never reuses a PID.
No actual eight-hour endurance run or real Provider acceptance was performed
for this release. Controlled-time and real short expiry checks are separate.
