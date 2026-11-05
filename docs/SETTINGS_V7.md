# FreeAgent v7.0 Settings

v7.0 keeps one lightweight execution core for the desktop Chat and CLI. Both
surfaces attach to the same owner when it is running; they do not open a second
database Owner. Persistent settings are applied through the typed control service
and take effect in the next Run.

## Control And Attach

The owner publishes a current-only `engine.json`, an observation `attach.secret`
and a separate interactive Chat secret. Attach state and events are generation
scoped. The observation secret cannot mutate settings; confirmation requires the
authenticated interactive user boundary and the current proposal credentials.
GUI users click the existing confirmation card; CLI users confirm its prompt.

Model-proposed persistent changes reuse the existing `waiting_confirmation`,
Preview and Confirm path. Ordinary file and command approvals remain tool
approvals under their permission mode.

Independent direct-setting proposals expire after 10 minutes; rendering one
again does not renew it. Model proposals remain attached to their Run. Opening
the card can issue fresh confirmation credentials after validating the resource
version and content; old credentials cannot confirm the proposal. Rejection or
resource invalidation also closes the waiting Attempt and Run.

## Context And Channels

`task_summary_bytes` is the only automatic-compaction switch: 0 disables it,
1–16384 enables it with that summary-byte bound. New installations use 4096;
existing omitted/zero settings are not changed. Settings, CLI
`config context --summary-bytes` and model-proposed `runtime.set_limits` share
the same control operation. Model-proposed persistent changes need confirmation.
Manual compaction does not enable automatic compaction or grant tools to None.
`auto_compact_percent` sets the shared 50–95% trigger (default 80). Model total
token windows remain separate from local byte limits and reported usage.
See [the v7.13 guide](CONTEXT_AND_CONTINUOUS_EXECUTION.md).

Telegram uses `/start` applications bound to numeric Bot/chat/person identities
and administrator approval. The short application code only locates a pending
request; it cannot grant access. Access and effective tool permission are shown
together. Disabling access retains the space/history and cancels active work;
prior effects and uncertain outcomes remain recorded. Lark/QQ retain pairing.
See [channels](CHANNELS.md) for setup and upgrade checks.

## Command Execution

`command_run` is optional and disabled by default. The Windows sandbox backend
is selected only when its readiness checks pass. Explicit host execution is a
same-user escape hatch, requires Full access and is recorded as such; it is not
evidence that the control plane prevents an authorized host process from
editing local files.

Unmatched command rules follow None/Ask/Workspace/Full instead of implicitly
turning a nonempty rule list into a deny list. Explicit deny/ask rules and a
nonempty executable allowlist still apply in Full. Old command snapshots are
readable but cannot continue under the changed rule interpretation. Runtime
facts distinguish the frozen Run, saved defaults and current blocking changes.

## Data Boundary

The package data directory is `Harness-pg-v1`, with store format
`freeagent-postgres-v1`. Older stores are rejected and are never migrated or
imported. Keys remain outside model-visible data and are resolved only for one
provider dispatch or probe.
