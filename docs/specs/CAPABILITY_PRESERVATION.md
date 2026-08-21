# Historical capability preservation notes

> Status: `HISTORICAL_BASELINE_NON_NORMATIVE`
>
> This file describes the previous architecture and is retained only as an S0
> semantic source. It is not a third current contract, does not prove current
> capability availability, and must not guide production wiring. Current
> normative contracts are `CORE_RUNTIME_V1` and `CURRENT_STORE_V1`; one-time
> cutover evidence belongs to `CUTOVER_ACCEPTANCE`.

## Purpose

The previous open-assembly design changed ownership and assembly rather than
generic behavior. Its replacement and compatibility statements below are
historical evidence inputs only.

The matrix labels below describe evidence recorded for the previous
architecture. They do not describe what the current S1 production candidate
registers or supports.

## Authority boundary

First-party and Operator-approved trusted implementations use narrow contracts and frozen exact references as `IN_PROCESS` modules; the historical design intended externally supplied or untrusted implementations to use an explicit MCP boundary. Neither mode was intended to receive a database bypass or an authority bypass. Model protocol/provider adapters are replaceable narrow transports and parsers; the kernel owns admission, `DispatchAttempt` persistence, authority, budgets, usage/cost/cache observation, result proof, UNKNOWN, and terminal commit. Channel protocol adapters are replaceable narrow ingress/egress codecs and transports; the kernel owns durable ingress admission, identity resolution, cursor CAS, outbox, effect ledger, redirect-policy enforcement, UNKNOWN, backup inclusion, and terminal commit. Team/Orchestrator policy is replaceable and may propose a member/graph plan; the kernel validates the frozen roster, independent-review constraints, DAG, slots, leases, fairness, and durable state transitions. An adapter may propose protocol-specific retry classification, but the kernel makes the final effect/replay decision. Implementations use the same narrow capability contracts and exact locks appropriate to their trust mode; unimplemented public conformance evidence is `planned` or `experimental`, never `stable`.

The historical MCP stdio and Streamable HTTP capability remains planned. The current normative contracts accept only a narrow development slice: MCP `2025-11-25`, official Go SDK `v1.6.0`, and `LOCAL_PROCESS + stdio + Tool-only` as an existing Action Adapter. It uses the existing Gateway and `DispatchAttempt`, requires an exact Operator artifact grant, and adds no MCP runtime, store, ledger, or port. The local process is not protected by an OS filesystem/network sandbox, so this slice is restricted to modules the Operator explicitly approves and fully trusts; untrusted third-party hosting and production-grade isolation remain unaccepted.

Current global startup recovery must not be inferred from the historical frozen-Run wording below. Before Catalog, Registry, SDK, Provider, or Universal Loop construction, the normative runtime performs only a Store-ledger safety transaction: it CASes the original PENDING Attempt to UNKNOWN and does not load member definitions, Config, Authority, or artifacts. A complete MCP artifact is verified only when that Adapter is explicitly selected for Describe or Execute.

## Profiles and compatibility

`pure_chat` is the default for new installations and for migration of the untouched historical built-in default. It has no mandatory Role, Persona, Knowledge/RAG, Memory, Skill, MCP, Tool, optional host, or extension SecretRef binding. `domain_expert` and `legacy_full` remain explicit compatibility profiles for operator selection and frozen historical Run recovery; neither may silently replace the default. An explicit operator activation is preserved and audited rather than overwritten by default migration.

Generic static Role/Persona and behavior style remain preservable through optional, versioned modules. The core does not require a particular personality implementation.

## Outcome observer privacy and provenance

`OutcomeEvent` carries a mandatory nested `Content` envelope and never exposes top-level user or assistant text. `HASH_ONLY` carries only syntactically valid content hashes and cannot verify their preimages; `REDACTED_TEXT` is permitted only when the frozen data policy permits it and each text hash binds the exact NFC redacted bytes. The envelope includes frozen redaction-policy ID, version, hash, and data-scope hash. The event also requires distinct run-manifest, member-snapshot, context-manifest, model-request, model-result, delivery-receipt, outbox-terminal, and config hashes. Kernel delivery terminalization controls when the event is emitted; SDK validation establishes the fail-closed wire shape and does not substitute for that runtime enforcement.

`OutcomeEvent` and `ObservationReceipt` both require observer schema version 2. Manifest and Context contracts remain at version 1. Legacy observer v1 fails closed; there is no raw-text compatibility decoder.

## Cache policy boundary

The currently admitted kernel capability is `core.cache.singleflight-enforcement`: isolation-safe family derivation, bounded serialization of already-admitted real requests, policy-generation isolation, cache/cost observation, and atomic audit persistence. The `planned` `freeagent.cache.cold-family-policy@1` module is a replaceable, default-off policy input. It cannot activate itself, synthesize provider requests, or change authority. Only an explicit persisted Control Plane action may activate an approved frozen policy; the kernel validates and enforces every resulting proposal.

## Context pressure invariant

Threshold selection is mutually exclusive per request: at or above predicted 100 percent, direct ordered minimal complete-prefix eviction takes precedence and the compactor is not invoked; otherwise compact at or above 85 percent. At or above predicted 100 percent, remove the smallest complete oldest eligible Task/turn prefix needed to return the request to or below 85 percent; ordinary old Task units are removed before superseded summary artifacts, and a Task/turn unit is never split.

`KeepRecentMessages` is a minimum protected window. If its raw cutoff falls inside a contiguous group with the same non-empty `TaskID`, the cutoff moves backward to the first message in that Task; this aligned boundary applies to compaction and direct eviction. Empty `TaskID` messages remain independent one-message turn units.

Current user input, protected recent history, current or unresolved Tool protocol, frozen authority/assembly evidence, and current retrieval evidence are never evicted. If the protected floor cannot fit, block before model dispatch. Eviction affects the current model request only, never durable history, memory, audit records, or artifacts. Audit records include evicted identities/hashes, estimated tokens, reason, target tokens, pre/post occupancy, and applicable summary/checkpoint hash.
