# P3 Provider Contract V1

Status: P3 accepted development slice. This document records the shared
boundary and the narrow Zhipu GLM implementation evidence. It does not claim
all Zhipu models, all API features, public Beta, or `RELEASE_READY`.

## Boundary

The existing model port remains the only runtime entry point:

- `moduleapi.ModelGenerateRequestV1` is the frozen provider-neutral request.
- `moduleapi.ModelGenerateOutputV1` is the normalized successful output.
- `moduleapi.ModelUsageReceiptV2` preserves omitted values as `null` rather
  than fabricating zeroes.
- `modulehost.InvocationOutcome` is the delivery boundary: `SUCCEEDED`,
  `FAILED`, or `UNKNOWN`.
- `corecontract.ModelStreamEventV1` and
  `corecontract.ModelStreamAccumulatorV1` are the shared streaming boundary.

Concrete adapters own only protocol translation, endpoint policy, and the
trusted credential resolver call. They do not own Store access, retries,
fallback, provider selection, or lifecycle decisions.

## Frozen Rules

1. A new Run freezes one exact provider binding, model, and model build. The
   adapter never silently changes any of them.
2. An exact retry first queries the existing logical operation and its durable
   Attempt. A transport error after dispatch is `UNKNOWN`; it is not an
   authorization to resend or switch provider.
3. A stream delta is only a partial prefix. A successful model result requires
   a valid terminal event. EOF without a terminal event is `UNKNOWN`.
4. `LENGTH`, tool/action-required, and cancellation are non-text-success
   terminal states. They must not be persisted as a complete assistant answer.
5. Usage is optional and field-specific. Missing usage remains missing; a
   normalizer may not turn missing values into zero.
6. Raw provider bodies, provider credentials, prompts, and private reasoning
   are adapter-local. Only bounded error classes, normalized output, and the
   safe usage receipt may cross the boundary.
7. The shared accumulator has a hard text bound. Over-limit output is
   `TRUNCATED`, never a successful result.

## Secret Boundary

The current DeepSeek adapter already follows the intended minimum boundary:
`APIKeyResolver` is injected by trusted composition, `APIKeyIdentity` contains
only provider/model/build/SecretRef metadata, the adapter stores no key, and a
resolved copy is cleared after one dispatch. The second adapter must implement
the same shape. Full secret-management infrastructure is outside P3.

Configuration and backups may contain a non-secret `SecretRef`, but never the
resolved credential. Resolver failures are classified without propagating raw
resolver diagnostics.

## Zhipu GLM Support Matrix

The accepted representative binding is deliberately narrow:

| Fact | Accepted value |
| --- | --- |
| Provider | `zhipu` |
| Adapter | `freeagent.adapter.model.zhipu/v1` |
| Model | `glm-4.5` |
| Model build | `glm-4.5/public-alias-observed-2026-09-22` |
| Endpoint | the compiled official Chat Completions endpoint under `https://open.bigmodel.cn/api/paas/v4` |
| Request | system/user/assistant text messages; non-streaming and adapter-level SSE streaming |
| Parameters | `temperature`, `top_p`, bounded `max_tokens`, text/JSON response format, stop, and explicit `thinking` mode when accepted by the exact model binding |
| Success terminal | exact `finish_reason=stop` for non-streaming; shared `COMPLETED`/`[DONE]` terminal for streaming |
| Usage | provider-reported prompt/completion/total and cached-input fields when present; omitted fields remain absent |
| Not supported | tools/actions, vision, arbitrary endpoints, arbitrary models, automatic model selection, fallback, cost routing, and user-visible streaming UI |

SDK or API shape similarity does not imply support for unlisted models or
endpoints. The compiled seed and artifact identity freeze this matrix; a new
model requires a separate support decision and evidence.

No automatic model selection, cross-provider fallback, or cost routing is part
of this contract.

## Maintenance Effect

The shared event state machine and existing invocation outcome contract remove
the most expensive source of future provider drift: every adapter must map
protocol frames into one bounded semantic vocabulary. Provider-specific tests
remain in the adapter package; cross-provider behavior belongs in the shared
contract tests. Adding a provider therefore adds protocol evidence and a
small adapter surface instead of another retry, Store, or outcome system.

## Zhipu Evidence

The P3 real experiment ran through the production composition, FAC2 Current
Store, bootstrap seed, Universal Loop, and `glm-4.5`. It was opt-in and used a
process-only environment credential; the credential, raw response, prompt,
and reasoning content were not written to the repository or report.

- Outcome: `SUCCEEDED`
- Provider/model: `zhipu` / `glm-4.5`
- Input and output token fields: present in the persisted Usage receipt
- Reply: bounded text result
- Retry/fallback: no retry, provider search, or cross-provider fallback

During model selection, the provider exposed multiple GLM models. A separate
probe showed that `glm-5.3-flash` did not accept the selected disabled-thinking
shape and produced a short-request `length` result. That observation is not a
failure of the accepted `glm-4.5` binding and is retained as evidence that the
support matrix must remain explicit rather than provider-wide.

The current Universal Loop consumes the existing non-streaming `ModuleInvoker`
contract. `InvokeStream` is implemented and tested at the adapter/shared
contract boundary, but this phase does not add a new SSE route or token UI.
EOF without a terminal event is `UNKNOWN`; HTTP/provider rejection is
`FAILED`; transport ambiguity after dispatch remains `UNKNOWN`.
