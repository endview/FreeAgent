# Extension Authoring Rules

## Start small

Use one extension for one clear responsibility. Prefer a context hook for
bounded prompt additions and a workflow for a user-triggered multi-step
operation. Do not build a background service, package manager, or extension
registry inside an extension.

The extension must work with all optional capabilities disabled. FreeAgent
remains a useful file and chat Harness when the extension is turned off.

## Required checks

Before sharing an extension:

1. Run `freeagent ext check --path <extension-directory>`.
2. Add a fixture and run `freeagent ext run --path <extension-directory> --fixture <file>`.
3. Run the extension's own unit tests.
4. Document settings, environment mappings, defaults, and the data written
   below `data_dir`.

Node extensions can use `sdk/node/freeagent-ext.mjs` without installing a
package. Keep the SDK file inside the published kit or copy it into the
extension directory; the runtime has no dependency installation step.

`ext run --host live` executes a declared workflow through the configured
Harness model and finite run budget. It is a separate acceptance from fixture
replay and must be reported separately.

## Protocol rules

- Reply to every request with the same JSON-RPC id.
- Keep stdout protocol-only. Write diagnostics to stderr.
- Treat unknown input fields as forward-compatible.
- Return UTF-8 JSON and keep each frame below the Harness limit.
- Return only capabilities that are actually registered.
- Make every hook bounded by the manifest timeout and context character limit.
- Use the invocation supplied by the Harness for every Host API call.
- Stop or surface `-32001`, `-32002`, and `-32005` without retrying.
- Do not retry `-32006`; the remote operation may already have happened.
- On `-32000` (too many concurrent Host requests), wait for in-flight requests
  to finish and retry later; the rejected request was not executed.
- Keep mutable state under the supplied `data_dir`.
- Report writes and other durable changes through `activity`, never by claiming
  that a model response is proof of success.
- Treat `turn.finished` activity as a user-visible summary only; it cannot
  change the completed Turn status or prove an external side effect.
- Treat `operation.check` as a pure, repeatable policy check. Return `ask` or
  `deny` to tighten the Harness decision, but never assume that changing the
  supplied preview or parameters will change the operation.

## Context blocks

Each block needs a stable id, non-empty text, and an allowed placement.
Prioritize useful context because low-priority blocks can be dropped when the
combined limit is reached. Do not inject secrets, large documents, or
unbounded history. Request only the `needs` required by the handler.

## Permissions and safety

An extension is trusted code when the user enables it. The Harness still owns
the selected permission mode and execution boundary. An extension must not
attempt to bypass approval, change Workspace, discover Key values, or encode
secrets into context, logs, URLs, or activity.

## Data and recovery

Use atomic writes for extension-owned files where practical. Treat a stopped
or unknown Host call as unresolved. Never assume that process termination
rolled back an external effect. Keep data small enough that the user can
understand and clear it from the FreeAgent extension settings page.
