# Tool Execution Contract

All built-in, command, HTTP, MCP and extension calls pass through the same saved
Attempt dispatch boundary and frozen Run capability/permission checks.
`ToolResult.Outcome` records dispatch-marker, completion, cancellation, timeout,
truncation and local duration facts. Backend receipts retain their own evidence.

| Situation | Saved outcome | Replay |
|---|---|---|
| Validation/decline before marker | known failure, dispatched=false | explicit new request |
| Definitive result, then cancellation | retain known result; cancellation diagnostic | no automatic replay |
| Cancellation/timeout after marker without proof | UNKNOWN | forbidden automatically |
| Definite partial batch | per-file success/failure/skipped | no batch replay |
| Uncertain file publication | UNKNOWN, previous definite items retained | inspect first |
| Truncated output | flagged, bounded UTF-8, not a successful check | explicit new request |
| Command root exit | retain proven exit; independently verify tree cleanup | no side-effect rollback claim |

Tools execute serially in the current core even if a Provider can return
parallel calls. Provider partial/invalid output never dispatches tools.
Provider Attempts record usage, local latency, response identity, stream origin
and normalized redacted failures. There is no retry loop or separate evidence
store. Local transaction tools commit their result and effect together.

`apply_patch` accepts strict numbered unified diffs, with an exact
`expected_sha256` map for every target (`absent` for creation). UTF-8 only,
32 files, 512 KiB patch text, 1024 hunks per file, 256 KiB resulting files;
no rename/mode/binary/fuzzy operations. All targets are prevalidated and each
write revalidates its source. The normal Ask/Workspace/Full policy applies.
Original partial successes are never rolled back or silently replayed.
