# Provider Contract

The adapter translates wire requests and responses. TaskService owns dispatch,
authorization, persistence, limits, cancellation, approvals, deduplication and
UNKNOWN. There is no Provider proxy, second executor or implicit retry layer.

## Local Declarations

`config model` and `config show` expose the following per-profile declarations.
They do not discover or certify capabilities through a network request.

| Field / CLI flag | Contract |
| --- | --- |
| `provider`, `model` / `--model` | Named profile and explicit model; `--key` implies `openai-compatible` |
| `key` / `--key` | Key profile id only; its base address and key source live in `keys.json` and are resolved before each dispatch |
| `protocol` / `--protocol` | `chat`, `responses` or `local`; Chat Completions, OpenAI-compatible Responses and local Echo dispatch are implemented |
| `streaming` / `--streaming` | `supported` requests SSE; `none` requests JSON |
| `tools` / `--tools` | `supported` or `none`; incompatible complete tool history is rejected, not split |
| `web_search` / `--web-search` | `supported` or `none`; explicitly enables a provider-side Responses search tool |
| `parallel_tools` / `--parallel-tools` | `supported` or `none`; parallel response/history violations are refused |
| `context_bytes` / `--context-bytes` | Finite serialized input bound; zero inherits Harness limits, not a tokenizer quota |
| `output_tokens` / `--output-tokens` | Finite requested output bound; zero inherits Harness limits |
| `finish_reason` / `--finish-reason` | `standard`; completed, tool calls, length, filtering and refusal are distinguished |
| `usage` / `--usage` | `optional`, `required` or `none`; missing is not invented as a zero report |
| `token_limit` / `--token-limit` | `max_tokens` or `max_completion_tokens` request field |
| `error_policy` / `--error-policy` | `manual`; retryability is advice for a new explicit task, never permission to replay |

A Responses declaration selects the Responses adapter before dispatch. It is
available for the OpenAI-compatible provider and sends the complete stateless
input history on every request. Models without tool support receive no tools.
Insufficient context or output capacity is refused before dispatch where
detectable. Responses output items are retained for the next request; a
`function_call` becomes a local tool call, while a `web_search_call` remains
provider metadata and is never treated as a local executable tool.

## Responses And Failures

JSON/SSE translation preserves tool-call IDs, contiguous item indexes/types,
Provider response ID/model/time/fingerprint, normalized and raw finish reasons,
streaming origin, usage status and bounded `web_search_call` metadata. Raw
Responses output items are retained so stateless function-call continuations
and server-side search context can be reconstructed. Metadata is bounded and
known credentials are redacted; it is protocol provenance, not tool execution
evidence.

SSE accepts multiline data, comments and usage-only events. Duplicate IDs,
changing identity, malformed items, incomplete streams and ambiguous completion
cannot produce executable tool calls. Calls run only after a validated complete
response has been saved. A length-limited or refused fragment is diagnostic
activity, not a final answer or executable tool request.

Errors distinguish authentication, permission, quota, rate limit, context,
capability, usage, output limit, filtering/refusal, invalid request, redirect,
unavailability and uncertain timeout/disconnection for both Chat Completions
and Responses. `Certain` distinguishes a definitive response from a possibly
processed request. `Retryable` never overrides UNKNOWN or automatic-replay
prohibition.

Failures persist their classification and bounded response fragment in the
existing Attempt response. UNKNOWN runtime diagnostics remain readable after
restart and current-format backup/restore. Failed and UNKNOWN guarded calls
retain their reserved output allowance; neither failure nor restart refunds
the parent/child budget. Successful saved outcomes settle atomically.

## Acceptance Layers

| Layer | Evidence |
| --- | --- |
| Adapter contract | Local request/response and capability validation tests |
| Deterministic Fixture | Loopback HTTP JSON/SSE, tools, errors, interruption, persistence and redaction tests |
| Real Provider protocol | Must send requests to an explicitly selected actual endpoint with its model and credential |
| Real model quality | Must observe task understanding, tool choice and continuation using that real model |

The first two layers cannot prove the last two. The live commands and results
are recorded in [v7.0 progress](FREEAGENT_V7_0_IMPLEMENTATION_PROGRESS.md).
There are no model quality, cost savings, latency or arbitrary Provider
compatibility claims from Echo or Fixture results.
