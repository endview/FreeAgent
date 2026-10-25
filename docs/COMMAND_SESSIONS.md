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

v7.3 allows one active command across foreground and background, with the
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
