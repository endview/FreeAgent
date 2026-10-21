# Security

This is an unreleased local single-user harness. It has no production security
support window, multi-user isolation, hostile-code sandbox or exactly-once
execution guarantee.

## Boundaries

- Protect the data directory using operating-system account permissions.
  Conversations, tool inputs/results and backups can contain private data.
  Secrets pasted by a user remain part of their history.
- Built-in file tools are limited to the selected workspace unless allowed
  roots or all non-protected paths are explicitly selected. They reject links,
  hardlinks, `.git` and harness state. Select an appropriate, non-sensitive
  workspace. Avoid concurrent edits to the same file.
- The default Ask mode requires confirmation for file changes, commands, HTTP
  GET and MCP calls. Workspace mode allows built-in file changes inside the
  workspace without asking, except FreeAgent configuration under the workspace.
  Full access skips tool approval. Confirmation authorizes an operation, not its
  correctness or reversibility.
- Model-proposed persistent settings changes always need user confirmation
  through the Chat card, the CLI prompt or a short expiring code. The attach
  observation secret cannot change settings.
- MCP programs are trusted local processes. They can ignore workspace boundaries
  and use their account's files and network. They do not run in the command
  sandbox.
  The child environment is an explicit small base plus `env` and `envFrom`;
  MCP installation and update are outside the harness.
- Key profiles live in `keys.json` outside the data directory, or in
  `data\keys.json` for the portable build. Saved keys are encrypted with DPAPI for
  the current Windows user and are resolved only for one provider dispatch or
  probe. HTTP redirects are not followed. Custom model endpoints receive the
  selected context and must be trusted. Known configured secrets are redacted
  from provider/MCP responses; this is not a guarantee of general secret
  detection.
- `command_run` is disabled by default. On Windows, sandboxed commands run in
  an AppContainer only after its readiness checks pass. Host execution requires
  Full access, runs as the current user and is recorded with
  `host_escape_hatch`; it can change anything that user can change, including
  FreeAgent settings, and is not a control-plane boundary. The default
  environment excludes arbitrary variables; explicit inheritance can expose
  credentials. Absolute
  executable allowlists are path policies, not executable signatures, argument
  policies, descendant restrictions or sandboxes. Do not replace an approved
  executable or concurrently change its working directory during a task.
  Timeouts attempt process-tree cleanup, but cannot undo file/network effects.
- Optional HTTP GET accepts only selected origins and exact approved URLs.
  Remote HTTPS addresses are checked and pinned before connecting; private,
  link-local and special-purpose destinations are rejected. Explicit loopback
  origins are local integrations, not sandboxes. Redirects, proxies, cookies,
  arbitrary headers and credential forwarding are disabled. Queries and
  returned text enter history; never place credentials in URLs. GET requests
  can trigger external effects, and interrupted results are not replayable.
- Dispatch markers can outlive a failed process. UNKNOWN pauses dependent work
  and cannot be continued automatically. Abandoning preserves the fact and
  warns subsequent workspace tasks.
- Current-format backups use SQLite snapshots, hashes and private staging.
  Hashes detect accidental damage; they are not authenticated signatures.
  Restore only trusted backups into a new directory.
- There is no HTTP control platform, implicit shell, automatic installer or
  background worker. Explicit `web` starts a foreground loopback-only Chat with
  a per-launch key, same-origin checks and no CORS. Keep that launch address
  private; it authorizes access to this local caller. Browser storage holds
  drafts and pending submission keys and may contain sensitive text. Closing
  the browser does not cancel work. Unknown stores are rejected, not upgraded.
- Markdown HTML and external images remain literal; math has trust disabled
  and finite expansion/source/depth/size limits. Links can open HTTP(S) targets
  only by user action and do not authorize execution. This is not an HTML app
  runner, remote hosting service or multi-user security boundary.
- Optional collaboration shares these same boundaries. Roles are explicit plan
  data, not OS identities or sandboxes. Built-in file scopes restrict assigned
  paths; commands, HTTP and MCP still need exact approval and can use their
  user's external authority. Parent/child limits are local dispatch guards,
  not provider billing guarantees. Cancelled or UNKNOWN work remains recorded.
  No child may recursively delegate. See [Collaboration](docs/COLLABORATION.md).

## Reporting

Do not post credentials, private conversations, database copies or unpatched
exploit details in a public issue. Use the repository's private vulnerability
reporting route where available. Include the source snapshot, synthetic
reproduction, selected workspace/data directories and affected boundary.
Do not assume historical release security claims apply to this rewrite.
