# Chat Contract

The Chat is an optional local caller of TaskService, not a restored control
platform.

## Authority

Chat snapshots project existing conversations, runs, messages and attempts.
They add no durable execution entity. Stored Markdown remains unchanged.
User and assistant text are answers, not proof of effects. Tool activity and
receipts come only from runtime records. Memory citations describe sources,
not independently verified facts.

## Keys And Models

The browser can manage local Key profiles and the model configurations that
reference them. The API has exactly these eight endpoints:

| Method | Path | Purpose |
| --- | --- | --- |
| GET | `/api/keys` | List non-secret Key profile views and presets |
| POST | `/api/keys` | Create a saved or loopback `none` profile |
| POST | `/api/keys/{id}/update` | Update a profile; an empty key keeps the stored secret |
| POST | `/api/keys/{id}/delete` | Delete a profile, optionally disabling its models |
| POST | `/api/keys/test` | Test saved or unsaved form input |
| POST | `/api/keys/{id}/models` | Fetch and record the provider model list |
| POST | `/api/models/enable` | Add or select a model configuration |
| POST | `/api/models/{id}/disable` | Disable a model configuration |

All JSON bodies use the existing origin, token, content-type, size, strict
field and trailing-data checks. Key input is consumed by the server and is
never returned in JSON, errors, events or environment projections. The
environment projection adds `key`, `key_name` and `missing` to each model.
Missing references are disabled in the model selector and must be repaired in
Key management before dispatch.

Key and model writes are serialized through the engine's existing
configuration boundary. Model enable/disable is rejected while an active run
holds the configuration; Key profile add, update and delete remain available.
`none` is accepted only for loopback addresses and sends no authorization
header. Provider acceptance is not established by local fixtures.

The UI maps Key-related failures to these stable codes:
`key_not_found`, `key_reentry`, `keys_busy`, `key_name_exists` and
`keys_format`. Provider authentication failures point the user to Key
management. The removed `/api/workspace` route is not part of this contract.

| Runtime | User state | Allowed next action |
| --- | --- | --- |
| running | generating / 生成中 | Stop |
| waiting_confirmation | awaiting_approval / 等待确认 | Preview exact operation |
| stopped, abandoned | stopped / 已停止 | Continue known stopped work, or new input after ending |
| limited | needs_attention / 需要关注 | Explicit finite limit adjustment |
| completed | completed / 已完成 | New input |
| failed | failed / 失败 | New input; no implicit retry |
| unknown, retained unknown attempts | unknown / 结果未知 | Inspect evidence; explicitly end without replay |

Disconnected transport does not change any runtime state. Stop requests are
not completion acknowledgements and never promise unchanged side effects.
UNKNOWN survives reconnect, restart, abandonment and backup restoration.
Abandonment frees the conversation, not the uncertain operation.

## Events And Recovery

Events identify their conversation, run and attempt where applicable.
`model_started`, `model_text`, `model_finished`, `model_interrupted` represent
stream lifecycle, not a second stored answer. Auxiliary model work is not
streamed as an answer. Only saved responses become final messages.
Tool start is dispatch progress; tool finish is not itself a successful check.
Snapshots and receipts determine the actual result.

The transport retains bounded, temporary stream text for same-process
reconnection. A new connection always requests an authoritative snapshot.
After server restart only durable records are recovered; lost partial text is
not invented. Backpressure requests a snapshot instead of replaying tasks.
Browser disconnect never cancels the task or resends input automatically.
Submission keys are retained until snapshot reconciliation. Confirmation is
bound to run, attempt, call, digest and exact preview; changes require review
again. Batch outcomes remain per file, including partial success and UNKNOWN.

## Page And Rendering

Collapsible conversation list plus Chat on desktop; conversation drawer on
mobile. Answer first, activity/sources/receipts/approval in disclosure sections.
No permanent dashboard or third column. IDs are opaque transport keys, not
required user input. Model selection affects future work and preserves history.
Optional collaboration management remains available in the CLI.

Structured Markdown tokens feed a bounded KaTeX renderer. HTML is literal,
external images never load, links accept only HTTP(S), and formulas use
`trust:false`, isolated macros, finite expansion/size/source/depth limits.
Code and incomplete math remain literal. Currency is not math. Rendering
errors preserve source with a small hint. Copy uses raw Markdown/code/LaTeX.
Code, tables and long display formulas scroll locally.

Enter sends; Shift+Enter inserts a newline; IME composition cannot send.
Unsent drafts persist per environment and conversation. Preview is read-only.
Scrolling upward suspends following; expanding activity preserves the viewport.
Only status changes, not token deltas, announce to assistive technology.

## Acceptance

Projection fixtures cover every state, retained UNKNOWN, raw-text preservation,
runtime-only receipts and reconnect. Renderer fixtures cover each delimiter,
currency, code, tables, invalid/incomplete math and malicious input. Browser
checks cover Chinese input/IME, drafts, scrolling, stop, stale approval, model
switching and desktop/mobile layout. Local tests cannot certify live Providers.
