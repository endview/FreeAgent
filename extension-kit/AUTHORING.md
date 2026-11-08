# Extension Authoring Rules

The [development guide](../docs/EXTENSION_DEVELOPMENT_GUIDE.md) provides current
commands, protocol examples, and verified limitations. SDK tool templates and
Core dispatch have contract regression coverage as of v8.0.1.

Follow the [lightweight Core development principles](../docs/EXTENSION_DEVELOPMENT_PRINCIPLES.md)
for responsibility, dependency, opt-in UI/runtime and acceptance boundaries.
Its known-deviations section distinguishes the design requirements from the
currently implemented protocol; it does not add callable methods.

## Start small

Use one extension for one clear responsibility. Prefer a context hook for
bounded prompt additions and a workflow for a user-triggered multi-step
operation. Do not build a background service, package manager, or extension
registry inside an extension.

FreeAgent must remain a useful file and chat Harness when the extension is
absent or turned off. An extension that needs an unavailable optional
capability must report that limitation, rather than enable it implicitly or
implement another execution engine.

## Required checks

Before sharing an extension:

1. Run `freeagent ext check --path <extension-directory>`.
2. For context/workflow entrypoints, add a fixture and run `freeagent ext test --path <extension-directory> --fixture <file>`.
3. Run the extension's own unit tests.
4. Document settings, environment mappings, defaults, and the data written
   below `data_dir`.
5. Exercise tools through the actual Core dispatch path; discovery and SDK unit
   tests alone do not verify protocol or permission compatibility.

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
- Activity is not approval. Model-proposed persistent settings, Persona,
  relationship, Memory or Skill changes require the Core confirmation path.
  If the necessary state proposal interface does not yet exist, keep a
  candidate unapplied; do not use an extension-owned file as an approval bypass.
- Treat `turn.finished` activity as a user-visible summary only; it cannot
  change the completed Turn status or prove an external side effect.
- Optional reply expression belongs to the extension. With explicit delivery
  declaration/registration, `turn.finished` may return `reply_parts` containing
  exact ordered slices of the completed output. Core checks lossless identity
  and transport bounds; it does not implement expression modes. Adjustable
  finite string settings in `model_settings` are exposed through the existing
  capability selector as `extension_settings`, frozen per Run. Respect these
  explicit values rather than overriding them with automatic suggestions.
- Treat `operation.check` as a pure, repeatable policy check. Return `ask` or
  `deny` to tighten the Harness decision, but never assume that changing the
  supplied preview or parameters will change the operation.
- Declare `delivery: true` separately from context/state and register
  `delivery.check` plus `provides: ["delivery"]` (alongside other implemented
  capabilities) before offering automatic follow-ups. Use only confirmed
  rules for the original conversation. See [the exact delivery contract](SPEC.md#scoped-replies-and-one-time-delivery).
- Use stable per-Run source keys and freeze the entire candidate, including
  absolute timestamps. Duplicate submission must read/reuse the original
  receipt; changing its content under the same key is an error.
- Keep `delivery.check` a bounded applicability check of existing state.
  It can only report activity, read or withdraw its own candidate; no models,
  tools, delegation, resource listing or new proposal may
  run inside it. Its invocation expires at callback completion/cancellation.
- Test a real subprocess against Core for allow, deny, invalid/timeout/exit
  responses, withdrawal, expired invocation and invalidation during review.
  Assert that review creates no model/tool/delegation attempts and that an
  UNKNOWN receipt never causes automatic re-delivery. Mock fixtures do not
  exercise this delivery path.

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

## Stateful extensions

Use [state capability v2](../docs/EXTENSION_STATE_CONTRACT_V2.md): explicit operation scopes and closed payload schemas, a single declared storage schema, and exact preview/confirmation. Character-specific fields belong to the extension payload. Do not inspect action suffixes or assume an `aw` schema in host code. Keep original operation receipts; an uncertain commit is queried, never replayed.

## v8.7 补充：分析与主动执行

见 [有界扩展执行](../docs/SUPPLEMENTAL_EXTENSION_EXECUTION.md)。人物/情绪/表达算法不得进入 Core；分析与主动执行分别声明能力、默认关闭。扩展通过原 model.generate 使用当前模型，不保存 Provider Key；原 Run/Attempt/outbox 保留唯一权威。未启用时不增加调度进程或定时器。AffectWeave 1.6 使用此契约，旧版宿主不做兼容分支。
