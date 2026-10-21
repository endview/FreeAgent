# FreeAgent v7.0 Settings

v7.0 keeps one lightweight execution core for the desktop Chat and CLI. Both
surfaces attach to the same owner when it is running; they do not open a second
SQLite writer. Persistent settings are applied through the typed control service
and take effect in the next Run.

## Control And Attach

The owner publishes a current-only `engine.json`, an observation `attach.secret`
and a separate interactive Chat secret. Attach state and events are generation
scoped. The observation secret cannot mutate settings; confirmation requires the
interactive user boundary and an expiring short code.

Model-proposed persistent changes reuse the existing `waiting_confirmation`,
Preview and Confirm path. Ordinary file and command approvals remain tool
approvals and do not use control confirmation codes.

## Command Execution

`command_run` is optional and disabled by default. The Windows sandbox backend
is selected only when its readiness checks pass. Explicit host execution is a
same-user escape hatch, requires Full access and is recorded as such; it is not
evidence that the control plane prevents an authorized host process from
editing local files.

## Data Boundary

The package data directory is `Harness-v5`, with store format
`freeagent-harness-v7`. Older stores are rejected and are never migrated or
imported. Keys remain outside model-visible data and are resolved only for one
provider dispatch or probe.
