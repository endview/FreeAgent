# Compatibility Remediation B Design

> S0 状态：`HISTORICAL_NON_NORMATIVE`。仅供旧兼容与迁移语义回溯，不再指导当前实现；当前架构权威见本目录 `README.md`。

## Status

Selected by the user on 2026-07-18. Written-spec review is required before
implementation.

## Purpose

Close the five blockers found by the independent Compatibility Gate 0B review
without changing FreeAgent's original direction:

- lightweight Agents and shared authorized RAG;
- optional, replaceable Role, Knowledge, Memory, Skill, MCP, Cache Policy,
  Model, Channel, and Team policy modules;
- trusted kernel enforcement for identity, authority, budgets, scheduling,
  durable effects, UNKNOWN, reconciliation, and audit;
- deterministic 85/100 context-pressure behavior;
- no PAB or `character_chat` compatibility branch.

This remediation corrects behavior, ownership, protocol versioning, maturity
claims, and evidence. It does not implement the future MCP Host, Sidecar Host,
Role module, or Cache Policy module.

## 1. Context unit integrity

### 1.1 Threshold selection

Threshold selection remains mutually exclusive for each model request:

- below predicted 85 percent: pass through;
- at or above predicted 85 percent but below 100 percent: invoke normal
  compaction and persist the summary/checkpoint;
- at or above predicted 100 percent: do not invoke the compactor; perform
  direct ordered eviction.

For the direct-eviction branch:

```text
target_tokens = floor(input_budget_tokens * 0.85)
tokens_to_remove = predicted_input_tokens - target_tokens
```

Remove the smallest complete oldest eligible prefix that returns the measured
request to or below `target_tokens`. Ordinary old Task/turn units are eligible
before superseded summary artifacts.

### 1.2 Recent-window alignment

`KeepRecentMessages` is a minimum protected window, not permission to split a
Task:

1. Compute the raw cutoff as
   `max(0, ordinary_message_count - KeepRecentMessages)`.
2. If that cutoff lies inside a contiguous group with the same non-empty
   `TaskID`, move the cutoff backward to the first message in that group.
3. A message with an empty `TaskID` remains a one-message turn unit.
4. Deduplication behavior is otherwise unchanged.

The protected recent window may therefore contain more than the configured
message count. Unit integrity takes precedence over an exact count.

The aligned boundary applies to both normal compaction and direct eviction, so
neither branch can summarize or evict one side of a protected Task while
retaining the other side as recent history.

### 1.3 Protected floor

Current user input, the aligned recent window, current or unresolved Tool
protocol, frozen authority/assembly evidence, and current retrieval evidence
remain protected.

- If complete-unit protection prevents reaching 85 percent but the measured
  request is within the input budget, return the existing partial-eviction
  result and record the protected-floor reason.
- If the protected floor exceeds the input budget, block before model
  dispatch.
- Eviction changes only the current model request. Durable history, summaries,
  memory, audit data, and stored artifacts are not deleted.

### 1.4 Evidence

Add an integration regression named
`TestDirectEvictionProtectsWholeTaskAcrossRecentCutoff`. Its fixture must place
one Task across the raw recent-message cutoff and require enough removal that
the buggy implementation would remove only its older half. The fixed result
must retain both halves, remove only older complete units, avoid the compactor,
and report a partial result when the protected floor is above 85 percent but
within budget.

Reference the test from the stable direct-eviction capability item and from
the first-party required-evidence list.

## 2. OutcomeObserver wire v2

The new privacy/provenance payload is intentionally incompatible with the old
observer payload. It must not reuse schema version 1.

- Keep `SchemaVersion`, `ManifestSchemaVersion`, and `ContextSchemaVersion` at
  1.
- Set `ObserverSchemaVersion` to 2.
- Validate `OutcomeEvent` and `ObservationReceipt` against
  `ObserverSchemaVersion`.
- All current observer fixtures use version 2.
- A decoded legacy v1 observer event must fail validation.
- Do not restore top-level raw-text fields or add a v1 compatibility decoder.

The existing `OutcomeContent` requirements remain:

- `HASH_ONLY` contains no text and only shape-valid lowercase SHA-256 content
  hashes;
- `REDACTED_TEXT` uses exact accepted NFC bytes and the two specified
  domain-separated digests;
- policy identities are bounded, non-empty, trimmed, valid UTF-8, and NFC;
- all four content/provenance hashes and all eight event proof hashes are
  required lowercase SHA-256 values;
- durable delivery terminalization is enforced by the kernel; SDK validation
  proves wire shape only.

Strengthen evidence by:

- serializing non-empty valid `REDACTED_TEXT`;
- proving exact text values appear only under `content`, never at the event
  root;
- proving the Go event type has no direct `UserText` or `AssistantText` field;
- table-testing uppercase, non-hex, and wrong-length forms for every new hash;
- testing overlong and non-NFC policy ID and version values;
- including `SchemaVersion` in the documented `ObservationReceipt`.

## 3. Replaceable adapter versus trusted state machine

All public specifications and implementation plans use one boundary:

- Model provider modules replace protocol transport and response parsing.
  Kernel admission, `DispatchAttempt`, authority, budget, usage/cost/cache
  observation, result proof, UNKNOWN, and terminal commit remain trusted.
- Channel modules replace ingress/egress codecs and transport. Kernel durable
  ingress admission, identity resolution, cursor CAS, outbox, effect ledger,
  redirect enforcement, UNKNOWN, backup inclusion, and terminal commit remain
  trusted.
- Team/Orchestrator modules replace policy that proposes a member/graph plan.
  Kernel validation of the frozen roster, independent review, DAG, slots,
  leases, Workspace fairness, and durable transitions remains trusted.
- Protocol-specific retry classification may be proposed by an adapter; the
  kernel makes the final effect and replay decision.

Correct both kinds of overstatement:

- do not say the entire Provider transport, Channel ingress, or Team scheduler
  is kernel-owned and non-modular;
- do not say the entire Team, Provider, or Channel implementation becomes a
  module.

Task 6 of the open-assembly plan may defer implementation of the narrow
adapters, but deferral does not reclassify their future ownership.

## 4. MCP and Sidecar maturity

Maturity claims remain evidence-bound:

- aggregate MCP transport and Sidecar lifecycle capabilities remain `planned`
  in the capability matrix;
- MCP `2025-11-25` is the planned compatibility target, not a currently Stable
  interoperability claim;
- Context and Outcome are the first planned Sidecar adapter candidates, not
  Stable host adapters merely because their Go wire types exist;
- each independently implemented adapter receives its own matrix item and may
  become Stable only after its public interface, conformance fixtures, named
  local evidence, and any required external interoperability evidence pass.

This changes documentation truthfulness only. It removes no implemented
runtime behavior.

## 5. Cold-family ownership and migration

The current bundled implementation combines trusted enforcement with a future
replaceable policy. The capability matrix must separate them.

### 5.1 Stable kernel capability

Replace the conflated core item with a stable kernel item for:

- isolation-safe family derivation;
- bounded singleflight admission and serialization of existing real requests;
- policy-epoch/generation isolation;
- cache usage and cost observation;
- atomic auditable persistence.

Use this exact identity:

```text
core.cache.singleflight-enforcement
```

The stable item retains named tests for family isolation, bounded serialized
execution, generation isolation, and atomic observation/audit. The kernel
never creates a provider request and never activates a policy from observed
traffic.

### 5.2 Planned first-party policy

Add a separate planned first-party capability:

```text
first-party.cache.cold-family-policy
freeagent.cache.cold-family-policy@1
```

Its future canonical configuration contains only bounded policy input:

```json
{
  "schema_version": 1,
  "mode": "OFF",
  "max_serial_requests": 2,
  "max_wait_ms": 5000,
  "state_ttl_ms": 86400000,
  "max_tracked_families": 4096
}
```

Canonical rules:

- `schema_version` is a required integer and exactly `1`;
- `mode` is `OFF` or `SINGLEFLIGHT`, with default `OFF`;
- `max_serial_requests` is an integer in `[1,8]`, with default `2`;
- `max_wait_ms` is an integer in `[1,30000]`, with default `5000`;
- `state_ttl_ms` is an integer in `[60000,604800000]`, with default
  `86400000`;
- `max_tracked_families` is an integer in `[1,1000000]`, with default `4096`;
- unknown fields and floating-point values are rejected;
- the authoring decoder may accept an omitted policy field only by
  materializing the fixed default above before validation and hashing;
- explicit zero is never treated as omission and is rejected;
- the frozen attachment stores and hashes RFC 8785 canonical JSON with all six
  fields present.

Rules:

- default mode is `OFF`;
- no `AUTO`, `ACTIVE`, `PREWARM`, or synthetic-warmup mode exists;
- the module cannot activate itself, mutate global policy, widen authority, or
  create a model/provider request;
- it may validate/translate an explicitly operator-selected frozen config and
  return a bounded scheduling proposal for an already admitted real request;
- the kernel validates the proposal, enforces admission/serialization, and
  records all observations;
- the Control Plane alone imports and explicitly activates an approved policy;
- the Control Plane may estimate off/on cost from recorded observations and a
  frozen price version, but it does not automatically switch modes.

The existing `internal/cachecontrol` compatibility implementation remains in
place until the exact module reference, public capability contract, canonical
config, Control Plane mapping, adapter, and fidelity tests are green.

After the split the matrix has 51 items and still 35 Stable items; the new
policy claim is `planned`.

## 6. Sidecar Subject isolation evidence

The Sidecar plan already carries all required ScopeKey identities and hashes.
Make its future tests explicit:

- table-drive every lossless projected field mismatch;
- add pairs that differ only by `SubjectID`;
- independently prove state handles, cache entries, Subject-scoped fairness
  keys, deletion scope, and deletion receipts cannot cross Subjects.

This is a plan correction, not a claim that the Sidecar Host is implemented.

## 7. Files and scope

Expected implementation files:

- `internal/contextcompiler/compiler.go`
- `internal/contextcompiler/compiler_test.go`
- `sdk/moduleapi/manifest.go`
- `sdk/moduleapi/observer.go`
- `sdk/moduleapi/observer_test.go`
- `sdk/moduleapi/wire_test.go`
- `testdata/release/capabilities.v1.json`
- `docs/RELEASE_MATURITY.md`
- the five Compatibility Gate 0B public specifications/plans
- the Open Assembly Task 2 construction brief
- the Compatibility Gate 0B implementation report and review package

Do not modify the original private source tree. Do not initialize or use Git.
Do not implement MCP, Sidecar, Role, or Cache Policy runtime adapters in this
remediation.

## 8. Verification

Required focused evidence:

```text
go test -count=1 ./sdk/moduleapi ./internal/contextcompiler ./internal/cachecontrol
go test -count=1 ./internal/appprofile ./internal/team
Test-CapabilityMatrix.Tests.ps1
Test-CapabilityMatrix.ps1
```

Also run:

- the broadened prohibited context-clear wording scan;
- a contradiction scan for the obsolete whole-kernel/whole-module adapter
  statements;
- a maturity scan for Stable MCP/Sidecar claims;
- `go vet ./sdk/moduleapi ./internal/contextcompiler ./internal/cachecontrol`;
- `gofmt` verification for changed Go files;
- the full non-cached `go test -count=1 ./...` suite after focused review is
  clean.

An independent read-only reviewer must approve specification and code quality
before Open Assembly Task 2 begins.

## 9. Effect on the original design

This remediation preserves the original design. It makes four intended
boundaries enforceable:

- complete context units are more important than an exact ten-message window;
- open adapters cannot bypass trusted state machines;
- optional cold-family policy remains default-off and operator-controlled;
- maturity labels describe real evidence rather than future intent.

The context-runtime trade-off is that a recent window may expand to include
the start of a Task. This can retain additional input tokens. It never raises
the input budget: a protected floor over budget still blocks before dispatch.

There is also one intentional protocol compatibility break:
`OutcomeEvent`/`ObservationReceipt` move from observer schema v1 to v2, and
legacy v1 observer payloads are rejected. The observer API has no non-test
consumer in the current public tree, and silent v1 acceptance would weaken
the new privacy/provenance contract; therefore no raw-text compatibility
decoder is retained.
