# Historical release evidence matrix

> Status: `HISTORICAL_BASELINE_NON_NORMATIVE`
>
> This matrix preserves evidence claims from the previous architecture. It no
> longer guides current implementation or proves current production readiness.
> The current architecture authorities are
> [`CORE_RUNTIME_V1`](specs/CORE_RUNTIME_V1.md),
> [`CURRENT_STORE_V1`](specs/CURRENT_STORE_V1.md), and
> [`CUTOVER_ACCEPTANCE`](CUTOVER_ACCEPTANCE.md). The current Core and Current
> Store form an `S1_ACCEPTED_DEVELOPMENT_BASELINE`, while the schema remains a
> pre-release draft. The owner confirmed that no formal deployment exists, so
> production cutover is `NOT_APPLICABLE`; first deployment and public release
> remain separately unapproved.

This is the historical human-readable view of
`testdata/release/capabilities.v1.json`. Each anchor has exactly one matrix
item. The entries below are archival classifications only. They neither verify
current behavior nor prove S1 or S2 completion; current acceptance evidence is
defined by the current specifications and cutover checklist.

## Trusted core

<a id="workspace-identity-acl"></a>
### Workspace identity, ACL freeze, readonly references, and revocation [unverified]
<a id="workspace-fair-scheduling"></a>
### Durable multi-Workspace fair scheduling, restart fairness, and no duplicate claim [unverified]
<a id="frozen-run-assembly"></a>
### Immutable AgentVersion, Profile, RunManifest, and exact current assembly [unverified]
<a id="runtime-attempt-generation"></a>
### Run, LoopFrame, model Attempt, and lease-generation binding [unverified]
<a id="context-direct-eviction"></a>
### Mutually exclusive threshold selection and protected direct ordered eviction [unverified]

At predicted occupancy of 100 percent or more, direct ordered eviction takes precedence, does not call the compactor for that request, and removes the smallest complete old prefix toward the 85-percent target. Ordinary Task/turn units precede superseded summaries; protected history fails closed rather than being removed. If the raw recent-message cutoff would split a contiguous non-empty Task, the protected recent window aligns backward to the start of that Task.
<a id="context-summary-checkpoint"></a>
### Persisted context summary, checkpoint, and restart reuse [unverified]

At 85 percent or more but below 100 percent, normal compaction persists the summary/checkpoint used by restart.
<a id="model-reservation"></a>
### Model reservation and bounded immutable budget commit [unverified]
<a id="model-usage-observation"></a>
### Observed provider usage persists atomically without budget semantic drift [unverified]
<a id="model-reasoning-summary"></a>
### Reasoning usage summary semantics [unverified]
<a id="cache-singleflight-enforcement"></a>
### Cache-family isolation, bounded singleflight enforcement, and atomic auditable observations [unverified]

The trusted kernel derives isolation-safe cache families, serializes only already-admitted real requests within bounded policy generations, and persists cache/cost observations atomically. It never creates a provider request or activates a policy from observed traffic.
<a id="effect-model-ledger"></a>
### Model UNKNOWN reconciliation without semantic replay [unverified]
<a id="effect-tool-ledger"></a>
### Tool UNKNOWN suspension and no replay [unverified]
<a id="effect-channel-ledger"></a>
### Channel send UNKNOWN suspension and no semantic replay [unverified]
<a id="toolgateway-final-authorization"></a>
### ToolGateway authorization and exact terminal replay [unverified]
<a id="channel-cursor-cas"></a>
### Channel cursor compare-and-swap and durable identity [unverified]
<a id="channel-redirect-safety"></a>
### Single channel effect attempt does not follow redirects [unverified]
<a id="backup-complete"></a>
### Complete backup binds snapshot to referenced artifacts [unverified]
<a id="backup-cursor-restore"></a>
### Backup restores generic channel cursors [unverified]
<a id="privacy-deletion"></a>
### Privacy purge produces a complete backup without secrets [unverified]
<a id="lifecycle-graceful-drain"></a>
### Graceful lifecycle drain completes admitted effects before store close [unverified]
<a id="database-identity-migrations"></a>
### Current Store identity, frozen schema, and atomic initialization [unverified]

## Replaceable first-party behavior

<a id="pure-chat-execution"></a>
### Pure chat execution suppresses optional context and structured collaboration [unverified]
<a id="default-pure-profile"></a>
### New installations and implicit ingress select the audited pure_chat profile [unverified]
<a id="pure-multi-turn-continuity"></a>
### Pure multi-turn continuity without optional attachments [unverified]
<a id="static-persona-fidelity"></a>
### Static Persona fidelity in frozen ordinary execution [unverified]
<a id="role-module-fidelity"></a>
### Replaceable Role and declarative static Skill module fidelity [unverified]
<a id="shared-authorized-rag"></a>
### Shared authorized RAG, CJK recall, scope isolation, and cache invalidation [unverified]
<a id="rag-modes"></a>
### NO_RAG, LIGHT_RAG, and FRESH_RAG selection [unverified]
<a id="memory-delivery-lineage"></a>
### Post-delivery learning enqueue is ordered and idempotent [unverified]
<a id="memory-backfill"></a>
### Learning backfill scan is concurrent and restart-idempotent [unverified]
<a id="memory-lease-commit"></a>
### Memory learning lease commit replay and scope invalidation [unverified]
<a id="governance-rejected-source"></a>
### Governance approval merge and permanent rejected-source block [unverified]
<a id="governance-reports"></a>
### Governance reports can be disabled and persist window statistics [unverified]
<a id="governance-maintenance"></a>
### Domain maintenance defaults to 24 hours and avoids semantic no-change work [unverified]
<a id="team-selection"></a>
### Composite, specialist, and reviewer selection [unverified]
<a id="team-parallel-review"></a>
### Frozen parallel team execution publishes only reviewed draft [unverified]
<a id="team-bounded-repair"></a>
### Team repair is bounded and requires final review [unverified]
<a id="model-evaluation-routing"></a>
### Model profile evaluation, routing, and context policy [unverified]
<a id="cold-family-policy"></a>
### Replaceable default-off cold-family policy and manual Control Plane cost comparison [planned]

The future `freeagent.cache.cold-family-policy@1` module remains default-off and operator-controlled. It may propose bounded scheduling for an already-admitted real request but cannot self-activate, make automatic decisions, or synthesize warmup traffic.
<a id="local-chat-adapter"></a>
### Local chat and optional loopback Channel ingress validation [unverified]
<a id="telegram-adapter"></a>
### Telegram adapter [unverified]
<a id="lark-adapter"></a>
### Lark adapter [unverified]
<a id="qqbot-adapter"></a>
### QQ Bot adapter [unverified]
<a id="weixin-ilink-adapter"></a>
### Weixin iLink adapter [unverified]
## Open assembly and extension plane

<a id="public-schemas"></a>
### Workspace, Agent, Profile, and Task public schemas [planned]
<a id="member-exact-locks"></a>
### Exact per-member Module, Skill, and MCP locks [planned]
<a id="skill-governance-rollback"></a>
### Skill selection, governance, and rollback [planned]
<a id="mcp-transports"></a>
### MCP stdio and Streamable HTTP [planned]
<a id="mcp-lifecycle-recovery"></a>
### MCP lifecycle recovery and restart reproving [planned]
