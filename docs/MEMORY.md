# Explicit Memory And Knowledge

Normal Chat persists its own conversation without automatically learning or
creating retained memory. Memory is source data, not a system instruction,
tool authorization or proof that work happened.

## Chat Flow

`/memory` opens numbered choices for managing entries, saving selected text or
a completed result, selecting future input, importing knowledge, viewing sources,
clearing future selection or explicitly reusing an Agent. Ordinary interaction
uses titles and numbered choices, not internal identities or revision fields.

Saving offers current Agent (task-only or retained), current collaboration
shared area, personal/project knowledge, or no save. It previews the exact body,
destination and source, then asks for confirmation. Saving a retained entry does
not automatically select it for future model input; `/memory select` does.

`/settings memory`, also available under the advanced menu, edits finite memory
bounds and the knowledge switch. Knowledge is off by default. After enabling,
`/memory import` selects a workspace text/Markdown file and destination collection.
`/memory select` grants that collection to future tasks. The model retrieves
only when it calls `knowledge_search`; selection is not injection of all files.
`/memory sources` expands the latest task's actual sources, including its children.

## Scopes And Identity

| Scope | Owner and lifecycle | Agent access |
| --- | --- | --- |
| Private | Stable local Agent identity; optionally one Run only | Exact selected retained entries are read-only; task-only notes may be read/written when explicitly enabled |
| Shared | Current collaboration parent and project | Read, append and modify grants, with kind and optional entry restrictions |
| Knowledge | Named personal or project collection | Explicitly selected, read-only retrieval; user manages imports and changes |

Project scopes are isolated before retrieval. Personal scopes use an empty
project and still require explicit selection. The local user may manage the
current project's or personal content; this is not a multi-user permission model.
Changing a model does not change Agent identity. Same-name temporary experts
have distinct identities; user-selected stable identity reuse is explicit.
Dependencies alone do not grant another Agent's private context.

## CLI

For scripting, obtain a session from `run --json` and an Agent identity with
`memory agent --session <chat>`. Every command below also accepts `--data-dir`.
Flags precede task text. Without `--yes`, content/state mutations return a preview
and do not save.

```text
memory save --session <chat> --title "Working preference" --kind preference --text "Keep reports concise." --request pref-1
memory save --session <chat> --title "Working preference" --kind preference --text "Keep reports concise." --request pref-1 --yes
memory list --session <chat>
memory read --session <chat> --entry <entry>
memory select --session <chat> --entry <entry>
memory sources --run <run>
memory update --session <chat> --entry <entry> --revision 1 --text "Report checks and remaining risk." --request pref-2 --yes
memory disable --session <chat> --entry <entry> --revision 2 --yes
memory enable --session <chat> --entry <entry> --revision 3 --yes
memory delete --session <chat> --entry <entry> --revision 4 --yes
memory clear --session <chat>
```

Private owner defaults to the current Agent, project to the current workspace.
`--personal` selects personal scope. `--task <run>` selects task-only private
content for management. `memory select --task-private` grants private working
notes for future tasks; it does not permit automatic retained writes.
`memory select` replaces the complete future selection, so include all intended
entries and collections in that call.

Copy and completed-result saving are separate explicit actions:

```text
memory copy --session <chat> --from-entry <entry> --scope shared --owner <parent> --kind finding --request share-1
memory save-result --session <chat> --run <completed-run> --title "Selected analysis" --request result-1
```

Copy revalidates the exact active source revision at save time. A completed
output remains a model result, not independent verification. Incomplete,
failed, abandoned and UNKNOWN runs cannot supply a completed-result preview.

## Knowledge

```text
config memory --knowledge
memory import --session <chat> --scope knowledge --owner project-notes --file <workspace-notes.md>
memory import --session <chat> --scope knowledge --owner project-notes --file <workspace-notes.md> --request import-1 --yes
memory select --session <chat> --collection project-notes
memory search --session <chat> --scope knowledge --owner project-notes --query "release boundary" --limit 4 --max-bytes 8192
memory import --session <chat> --scope knowledge --owner project-notes --file <workspace-notes.md> --update --entry <entry> --revision 1 --request import-2 --yes
config memory --knowledge=false
```

Imports accept only explicitly selected workspace `.txt`, `.md` and `.markdown`
files, bounded valid UTF-8 with the usual link/hardlink/data-directory exclusions.
The source path, hash, complete text and destination are previewed and checked
again before save. Duplicate file content is recognized within the same collection;
changed content at an existing source path requires an explicit revision-bound
update. Disabled duplicates are not automatically re-enabled. Deletion is not
followed by an automatic re-import.

Local SQLite FTS5 trigram search uses a quoted literal rather than arbitrary
query operators; short queries use a literal substring fallback. It is not
semantic vector retrieval. Results carry entry/document title, scope, revision,
updated time, full-content hash, sources and UTF-8 byte ranges. Both result count
and byte volume are finite. Model tool output also includes excerpt hashes.
Explicit user search does not require the Chat's model selection; model search
requires its saved collection grant.

## Consistency And Limits

Chat or task selections fix a Run's input grants. Each dispatched model request
freezes its actual memory revision and sources; concurrent changes before
dispatch cause a conflict, not silent substitution. Later requests use current
active revisions and retain disabled/deleted-reference status without old bodies.
Scope validation happens before retrieval, not after loading all private data.

Runtime authors cannot be supplied by a model. Shared/task-only updates carry
expected revisions and content sources. Model writes, idempotent receipts, saved
tool results and parent settlement share the SQLite transaction; known results
are reused. Definite failures roll back edits; UNKNOWN is not blindly replayed.
All model and tool work retains ordinary task and cumulative parent limits.
No model-assisted consolidation bypass exists.

Private preferences remain distinct from model findings. A `fact`, `hypothesis`,
`finding`, `decision`, `question` or `artifact` label does not fabricate a passed
check. Exporting retained private content or promoting shared work to knowledge
requires explicit user confirmation. Models cannot write retained private
entries or knowledge in this version.

## Disable, Delete And Restore

Disable removes content from new memory reads/injection but keeps it for user
management and explicit enable. Delete removes the current title/body, body
revisions and search row. Content-free tombstones and idempotent receipts remain;
deleted entries cannot be enabled or silently resurrected.

Already-sent Provider requests, historical Chat/tool snippets, explicit copies
and old backups are not retracted or comprehensively erased. Source inspection
shows used revisions and current active/updated/disabled/deleted status; it does
not rewrite old requests. Restoring a pre-deletion backup can restore its saved
content. Current-format backup/restore includes scopes, versions and operations;
no previous format is accepted or migrated.

Clearing future selection leaves ordinary history and saved Run grants intact.
It is not full privacy erasure. Knowledge off means no new retrieval/index update
or background work; existing persisted content remains manageable.
There is no automatic history scan, filesystem watcher, embedding service,
background Agent, implicit personality learning or knowledge-governance platform.
