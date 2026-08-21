# Stage 2 Unified Assembly Cutover

> S0 状态：`HISTORICAL_NON_NORMATIVE`。仅供旧切换断点与风险回溯，不再指导当前实施；当前架构权威见本目录 `README.md`。

> Status: implementation in progress  
> Date: 2026-07-26  
> Scope: Core Loop, authoring definitions, RuntimeCatalog, Run freeze,
> activation, and online control plane

## 1. Purpose

Stage 2 removes the remaining split between the legacy application Profile
path and the public modular assembly path.

The target authority chain is:

```text
authenticated operator or trusted classifier input
  -> immutable WorkspaceDefinition / AgentVersion / AssemblyProfile
  -> immutable TaskRequirements
  -> current RuntimeCatalog Generation
  -> Assembly Compiler
  -> MemberSnapshot
  -> RunManifest
  -> runtime execution
```

`appprofile` may remain as a bounded compatibility projection for historical
Runs and the current executor during cutover. It must not remain a second
independently mutable source of new assembly decisions.

## 2. Non-negotiable invariants

- `pure_chat` has one built-in Core Loop and zero optional Module, Role,
  Knowledge/RAG, Memory, Skill, MCP, Tool, optional Host, or extension
  SecretRef attachment.
- RuntimeCatalog entries represent availability. Availability never implies
  selection, installation, loading, permission, or execution.
- AgentVersion, WorkspaceDefinition, AssemblyProfile, TaskRequirements, and
  RuntimeCatalog references are exact tenant-scoped version/hash tuples.
- A new activation affects only a new Task or Run. An active Run never drifts.
- Rollback creates a new immutable activation generation; it never overwrites
  historical bytes.
- A missing authority, compatibility link, task selection proof, catalog
  generation, or exact definition fails closed.
- No test-only label hash, random digest, or unrelated legacy hash may stand
  in for an authority document.
- This stage adds no model request and consumes no model token.
- `TaskRequirements.evidence.compiler_id/compiler_version` is an integrity and
  diagnostic label, not compiler authority. Its surrounding seal is admitted
  only after external authority authentication; the assembly compiler that
  produced runtime snapshots is identified by the artifact hash in
  `TaskAssemblyReceipt`.

These constraints preserve free Agent/Workspace composition and optional
Skill/MCP/RAG attachment. They narrow authority; they do not narrow the
available composition model.

## 3. Current boundary

The repository already contains:

- the attachment-free `core.single-agent-loop@1`;
- immutable public authoring definition storage;
- an attachment-free Core Loop compiler;
- RuntimeCatalog Generation and current-pointer CAS;
- V2 MemberSnapshot, RunManifest, FreezeBundle, lifecycle transition, and
  generation lease persistence;
- reversible legacy Profile and Agent compatibility projections.

The production ingress still freezes an `appprofile.ProfileSnapshot`, and the
V2 freeze path is not yet the runtime execution authority. Existing V2 tests
prove structural persistence, not a complete production authorization chain.

### 3.1 Implementation ledger

Completed foundations:

- explicit `LEGACY_COMPAT_V1` and `UNIFIED_CORE_V1` Run admission markers;
- execution and recovery gates that never infer legacy authority from missing
  evidence;
- immutable public authoring definitions, activation, task requirements, and
  RuntimeCatalog evidence;
- exact public AgentVersion to legacy executor-row compatibility bindings;
- `TaskAssemblySelectionV2` and `TaskAssemblyReceiptV2` domain values;
- additive schema-v8 persistence for the complete V2 selection, ordered
  members, zero-attachment SDK snapshots, and receipt closure;
- strict backup/restore and append-only transfer inventory coverage for that
  schema;
- portable governance scope, association, policy-generation, and revocation
  evidence;
- exact governance current-generation CAS primitives, narrow runtime
  repositories, and append-only revocation-chain verification;
- a material-aware runtime repository contract that removes generation-only
  publication and requires current CAS plus the complete policy material set
  to become visible atomically;
- an exact, provider-free, per-member governance material closure and trusted
  runtime resolver that verifies the complete genesis-to-frozen revocation
  history while granting no external effect or RAG authority;
- a content-addressed per-member `pure_chat` governance compiler that binds
  exact assembly, RuntimeCatalog, snapshot, and governance parents while
  producing only fixed deny-all/provider-not-applicable results and no live
  permission;
- portable core-model build, route, configuration, and execution route-set
  authority;
- an immutable startup model trust catalog and exact resolver that bind five
  independently loaded artifact byte digests, canonical non-secret
  configuration bytes, and the exact SecretRef provider-version set;
- an independent, deny-only core-model revocation domain with five exact typed
  targets and bounded contiguous-suffix verification;
- an exact route-set freeze binding that verifies the complete
  genesis-to-frozen model-revocation history for every selected entry while
  remaining portable and non-authorizing;
- a portable, non-authorizing core-model DispatchAttempt document derived from
  the exact trusted route checkpoint, selected entry, encoded request digest,
  owner epoch, protocol, and non-secret transport configuration;
- schema-v9 governance registry structure and transfer-v5 identities, with no
  historical backfill or trust promotion;
- schema-v9 atomic governance material publication and exact loading, including
  ordinal-aligned canonical parent verification, current-generation CAS,
  retry, restart, rollback, backup/restore, corruption, cancellation, and
  operational-error coverage;
- schema-v9 runtime-resolution and revocation repositories with atomic
  root/ordered-child persistence, exact typed-parent re-verification, strict
  tail CAS, verified suffix loading, restart/rollback/backup/corruption
  coverage, and separation of availability errors from durable corruption;
- schema-v10 structural authority/evidence storage and transfer-v6 identities:
  eleven additive STRICT append-only relations, no legacy model-table edge,
  no backfill, exact future-issuance composite keys, and a byte-for-byte frozen
  first-466 transfer prefix with DispatchAttempt classified as history;
- schema-v10 exact aggregate repositories for compiled governance decisions
  and model build/route/configuration/route-set/revocation/freeze/attempt
  evidence, including caller-owned transaction primitives, typed canonical
  parent restoration, exact retry, restart, rollback, backup, cancellation,
  corruption, and operational-error separation;
- a corrected model-revocation genesis representation using a SQL NULL
  predecessor mapped back to the typed zero reference, plus a V1 complete-chain
  limit of 4096 records; larger histories require a future checkpointed or
  segmented version and cannot be partially accepted by V1;
- one canonical owner-epoch integer domain across backend ownership,
  DispatchAttempt, Strict CJSON, and SQLite:
  `1..9007199254740991`;
- portable backend-wide owner-epoch transition evidence for the exact lifecycle
  `NONE -> CLAIMING -> ACTIVE -> RELEASED|FENCED` or
  `NONE -> CLAIMING -> FENCED`; the package-derived owner identity binds
  DTD/backend/epoch/owner instance, and an ACTIVE reference remains structural
  and non-authorizing;
- a portable, non-authorizing Unified model invocation identity domain:
  its stable position binds the exact Run/member/slot/checkpoint/plan-node/
  zero-based call ordinal and phase, its semantic hash is derived internally
  from that position plus the stable context digest, and dynamic runtime
  attempt/model-call IDs affect only the reservation hash;
- a process-local generic model PREWIRE governance evidence boundary that
  retains the compiler's exact revocation closure, rejects portable audit
  promotion, and remains non-authorizing;
- a process-local prepared core-model dispatch that closes the startup-sealed
  route, route-set freeze binding, exact five-target model-revocation closure,
  complete DispatchAttempt audit projection, and current startup-catalog read
  lease without granting transport authority; the lease uses one-way ownership
  transfer so ordinary copies cannot unlock the composite check/use window;
- a process-local one-use composite PREWIRE grant that joins the exact
  governance and prepared-model closures with the complete encoded request,
  transfers startup-catalog lease ownership internally, exposes both exact
  live-suffix verifiers only inside one callback, and expires retained copies
  before releasing the catalog claim; it has no JSON/restore/clone or generic
  authorizing surface;
- a process-local exclusive-binding claim that lets a durable coordinator
  atomically invalidate every retained raw PREWIRE-grant copy before binding;
  the winning binding is the only remaining consumer and every close path
  clears the retained request after active readers drain;
- the locked seven-relation schema-v11 effect evidence family, including
  Invocation Reservation, Prewire Issuance, FirstByteReceipt, Terminal, UNKNOWN
  reconciliation, owner transition history, and engine-local owner current;
- a Store-bound, production-shaped commit-before-start primitive that performs
  the exact owner-gate CAS, typed-parent closure, both live-suffix checks,
  Receipt/Terminal absence proof, receipt commit, and one-use transport-start
  handoff without exposing request bytes to the transaction callback;
- caller-owned transaction primitives for the future atomic dual-freeze,
  including strict retry, partial-row corruption, rollback, and cancellation
  handling.

Still incomplete:

- the general hard-ceiling compiler;
- runtime wiring from the real executor into the Store-bound
  commit-before-start primitive and a concrete no-redirect/no-retry transport
  starter; the implemented primitive currently has no non-test caller and
  therefore is not yet a production-wired final composite dispatch fence;
- one atomic composition transaction covering both compatibility and public
  evidence;
- `RunManifestV3`, production execution cutover, activation/control-plane
  APIs, and hot rollback acceptance.

Until those items close, Unified Runs remain blocked from model, Tool, MCP,
provider, and outbox effects. No portable constructor, restored row, legacy
route hash, random digest, or test fixture can substitute for the missing
authority.

## 4. Frozen V2 rule

`RunManifestV2` is already a content-addressed format. Its canonical fields,
hash domain, and restore semantics must not be changed in place.

Required compatibility and compilation evidence is persisted beside V2 as
append-only, hash-bound parents. Changing the V2 document itself would create
two incompatible meanings under one schema version.

V2 remains a transition and recovery format. The final public assembly
authority is introduced as `RunManifestV3`.

## 5. Cutover slices

### 5.1 P0-A: input closure in shadow mode

P0-A adds:

- exact Profile compatibility evidence;
- exact legacy Agent compatibility evidence;
- Workspace-scoped immutable activation generations and current-pointer CAS;
- a TaskAssemblySelection binding the real TaskScope, definitions, selected
  members, compatibility evidence, and RuntimeCatalog Generation;
- a TaskAssemblyReceipt binding compiler identity and every resulting
  MemberModuleSnapshot;
- exact-load and tamper checks at the V2 freeze boundary.

The initial compiler accepts only zero-attachment Core Loop selections.
Non-empty Module, Skill, MCP, Knowledge default, Profile selection, task
request, or required capability fails closed.

P0-A may calculate and persist shadow evidence, but the existing executor
remains the only execution authority. No historical Task is backfilled using
invented evidence.

#### 5.1.1 Task admission freeze

The Workspace activation used by a Task is frozen in the same transaction
that first creates its Task and Run. It is not read later by the composition
worker because a queued Run must not drift when a newer Workspace activation
becomes current.

That ingress transaction must:

- load the exact current Workspace activation and all of its authoring,
  RuntimeCatalog, and Profile compatibility parents without opening a nested
  transaction;
- validate the caller-supplied legacy Profile against the exact compatibility
  source, rather than treating a process-level Profile as assembly authority;
- construct canonical TaskRequirements only from already authenticated
  operator, admission, or trusted-classifier evidence;
- persist the TaskRequirements and TaskRequirementsSeal with the TaskScope;
- record an explicit immutable Run assembly mode.

Assembly mode is never inferred from missing evidence. Migrated historical
Runs are explicitly marked `LEGACY_COMPAT_V1`; this marker classifies their
authorized compatibility path and does not claim that an unfinished historical
Run already has a frozen manifest. New unified Core Loop Runs are explicitly
marked `UNIFIED_CORE_V1`. Duplicate ingress returns the original decision and
does not invent or backfill evidence. A rejected ingress does not create a
partial Task, Run, or seal.

#### 5.1.2 Composition dual-freeze

Single-agent and multi-agent composition share one internal transactional
freeze boundary. After validating the composition lease and admission marker,
that transaction writes or verifies:

- the exact TaskRequirementsSeal closure;
- every pre-approved legacy Agent compatibility link and approval;
- TaskAssemblySelection and its ordered members;
- every SDK MemberModuleSnapshot;
- TaskAssemblyReceipt and its ordered members;
- the legacy executor projection and TeamSnapshot;
- exact route and governance parents;
- the unchanged V2 freeze bundle during transition;
- the initial lifecycle transition and RuntimeCatalog lease;
- RunManifestV3 after P0-D is introduced;
- the composition-lease release and audit event.

An exact retry succeeds only when the entire closure is byte-identical.
Missing rows, partial rows, or a legacy-only snapshot are corruption, not an
idempotent success. No provider, model, Tool, or MCP wire call may occur before
the freeze transaction commits.

#### 5.1.3 Production bypass gates

Unified Runs must fail closed at every execution and recovery entry point, not
only in the main Runtime caller. Shared Store transaction helpers therefore
gate:

- legacy `ClaimNextTask` and manifest lazy-freeze paths;
- composition, ready-single, and ready-slot claims;
- direct run-slot start and input-load methods;
- model-resume claim and checkpoint application;
- startup recovery and slot recovery.

Recovery may re-arm, park, reconcile, or block work. It never generates
missing assembly evidence. A paid or externally effective result with missing
unified authority is parked for reconciliation and is never semantically
replayed. Only an explicit `LEGACY_COMPAT_V1` marker may use the historical
recovery path.

### 5.2 P0-B: real governance and model-route authority

P0-B provides repositories and resolvers for:

- platform safety baseline;
- result classifier definition;
- Tenant, Workspace, Agent, Task, and Run scope authority;
- Workspace/Agent scope associations;
- resolved Tool, Content, Authority, and Budget policy;
- policy generation and revocation watermark;
- JUDGE, REPLY, and REPLY_PRO model route identity, build authority, and
  configuration authority.

Each hash is derived from the exact canonical parent document. A route parent
may authorize execution only after its complete trusted closure is verified.

Core model build authority binds five independently loaded byte artifacts:

- the model adapter;
- the model implementation;
- the request encoder;
- the response decoder;
- the wall-time meter.

The adapter and implementation must not be collapsed into one artifact digest.
Configuration authority binds exact canonical non-secret configuration bytes
and exact versioned SecretRefs, never secret values. Portable constructors,
restored database documents, legacy candidate labels, and
`executor.RouteSet.Hash()` are audit or compatibility inputs only and never
authorize execution.

The real execution authority uses independent hash domains:

```text
freeagent.core-model-execution-route-set-entry.v1
freeagent.core-model-execution-route-set-authority.v1
```

The historical `RunManifestV2` structural parent retains its existing wire and
hash domains unchanged. Its existing
`core_model_route_set_authorities_v1` and
`core_model_route_set_entries_v1` relations are likewise structural V2
history only. They must not be altered, reused as execution-authority tables,
or backfilled into trusted authority.

The real portable authority is persisted in an independent
`core_model_execution_*_v1` table family. Only a startup-sealed view that
revalidates every loaded artifact byte, canonical non-secret configuration
byte, exact SecretRef provider version, scope, and route-set parent may
establish static eligibility.

Static eligibility is not permission to put bytes on a transport. Immediately
before a wire call, the runtime must also revalidate the exact current startup
catalog and the contiguous revocation suffix, bind the result to the exact
`DispatchAttempt`, and issue a one-use pre-wire grant. Only that grant may
authorize the first-byte transition. A stale trusted view, portable route set,
restored database row, or legacy structural parent remains non-authorizing.

#### 5.2.1 Model revocation and first-byte linearization

Core-model revocation uses an independent append-only chain scoped by exact
deployment trust domain and Tenant. Its closed target vocabulary is:

- core-model build authority;
- core-model route identity;
- core-model configuration authority;
- core-model execution route-set entry;
- core-model execution route-set authority.

Each target is derived from the complete Tenant-scoped selected-entry closure,
never from a caller-supplied bare hash. Startup-catalog identities and SecretRef
provider epochs are not portable revocation targets. They are revalidated
against the current startup catalog so a restored database value cannot promote
itself into startup trust.

Dual-freeze must also create an exact portable model-route freeze binding that
joins the execution route-set authority to the model-revocation tail observed
after verifying the complete genesis-to-frozen history. Issuance may not choose
an arbitrary `frozen=current` baseline, because that would allow older
revocations to be skipped. The freeze binding remains non-authorizing and is a
required companion parent of every later DispatchAttempt in the issuance and
pre-wire closure. It does not change the already locked
`CoreModelDispatchAttemptV1` bytes or hash.

The pre-wire grant is an opaque, process-local, one-use handle:

```text
ISSUED -> CONSUMING -> CONSUMED
   \          \
    -> BURNED  -> BURNED
```

It has no JSON form, restore function, clone constructor, or durable
representation. Copying a handle shares one atomic state. The corresponding
DispatchAttempt, issuance evidence, first-byte receipt, and terminal receipt
are durable audit records but grant no permission by themselves.

Before consuming a grant, the request is fully encoded and its exact body hash,
length, endpoint fingerprint, protocol, no-redirect/no-retry rule, route
closure, startup-catalog ref, SecretRef binding-set hash, semantic invocation,
owner epoch, Run, and DispatchAttempt are frozen. Consumption then:

1. atomically owns the live grant;
2. holds an exact current startup-catalog read lease;
3. loads the current model-revocation tail and verifies the complete contiguous
   suffix from the frozen tail;
4. verifies the exact durable DispatchAttempt, reservation, owner epoch,
   Unified admission, live Run, and absence of a prior receipt or terminal;
5. inserts the unique immutable first-byte receipt and commits;
6. releases the catalog lease and immediately starts the already-encoded wire
   operation without resolving any newer configuration or Secret.

The first-byte receipt commit is the logical wire linearization point. A
concurrent revocation or catalog rotation that commits first rejects the stale
grant; one that commits afterward does not rewrite the result of an already
linearized attempt.

Grant or transaction failure before that point is `NOT_EXECUTED`. Once the
first-byte receipt commits, a crash, timeout, transport ambiguity, or terminal
persistence uncertainty becomes `UNKNOWN`, never `NOT_EXECUTED`. `UNKNOWN`
allows read-only reconciliation evidence but never semantic replay or terminal
rewriting. A restart fences the old process owner and cannot restore a live
grant from issuance evidence.

No `AuthorizesModelExecution*` method returns true. The only successful
first-byte capability is the in-process result of consuming the exact live
grant under the current catalog, revocation, owner, and durable-attempt fences.

#### 5.2.2 Governance registry persistence boundary

Schema v9 must publish a complete `GovernancePolicyMaterialSetV1`, not only a
`GovernancePolicyGeneration`. A generation closes its SourcePolicy documents
and references but does not contain the canonical bytes of the four policy-leaf
families. Therefore the current-generation CAS and the complete exact material
set must become visible in one transaction. Publishing current first and
backfilling leaves asynchronously is forbidden.

The current ref may continue to identify the Tenant, generation, and generation
hash. Durable constraints must nevertheless ensure that every active
generation has exactly one complete material set and that exact load can
reconstruct the generation, every SourcePolicy document, and every Tool,
Content, Authority, and Budget policy leaf without fallback or inference.

The checked-in transfer inventory already reserves governance relation names
from an older wire design. A reserved name may be materialized only when the
current canonical wire, hash domain, natural key, and row-projection golden are
literally equivalent. In particular, the old resolved-policy, member-policy,
snapshot, and policy-revocation relation names are not equivalent to the new
single `GovernanceRuntimeResolutionV1` and typed predecessor-chain revocation
documents. They must not be reused or silently assigned a new meaning.

The new logical-schema tail uses distinct names for:

- SourcePolicy documents and complete material-set roots/members;
- portable governance runtime resolutions, applicable source refs, and
  revocation-target projections;
- typed governance revocation records;
- governance activation history.

The per-member compiled governance decision and its freeze checkpoint must also
be durable parents before model pre-wire evidence is persisted. They may be
introduced in the next additive migration together with the model dispatch
authority family; schema v9 bytes are not reopened or reinterpreted.

The first 454 transfer relation identities remain byte-for-byte unchanged.
New identities append only at the v5 tail; no existing name is appended twice.
Materializing a previously reserved, exactly equivalent relation does not add
another seed row. Structural seed availability still does not enable transfer:
descriptors, phases, roots, and zero-proof acceptance remain independently
required.

Migration v9 creates no baseline, classifier, scope, association, generation,
material, current, revocation, resolution, or activation row for existing
databases. It performs no scan, backfill, repair, or trust promotion. Database
documents remain portable evidence; trusted baseline, classifier, historical
revocation closure, and runtime resolution must still be reconstructed from
their independent startup and exact-parent authorities.

#### 5.2.3 Per-member pure-chat governance compilation

`GovernanceRuntimeResolutionV1` proves a static governance DAG but does not
identify a selected Run member, RuntimeCatalog lease, assembly receipt, or
MemberSnapshot. It therefore cannot be promoted directly into an execution
decision.

The initial compiler produces an independent, content-addressed
`PureChatCompiledGovernanceDecisionV1` for each member. It binds:

- Tenant, Workspace, Task, Run, member ordinal, and member ID;
- the exact reusable AgentVersion;
- the governance runtime-resolution hash and frozen policy generation,
  generation hash, material-set hash, revocation watermark, and tail;
- the exact RuntimeCatalog generation, hash, and reference hash frozen for the
  Run;
- `TaskAssemblySelectionV2`, `TaskAssemblyReceiptV2`, the exact
  `MemberModuleSnapshotV2`, Run-member binding, and governance snapshot.

The compiler accepts only `ZERO_ATTACHMENTS`. Every optional Module, Role,
Knowledge/RAG, Memory, Skill, MCP, Tool, optional Host, or extension SecretRef
binding must be absent. Its fixed result is:

- Tool: deny all, zero provider;
- Content/RAG: deny all because ContentPolicy v1 is inert;
- Authority: no permission, no effect, and no provider grant;
- Approval: not applicable; a path that needs approval fails because the
  Approval module is unavailable;
- provider budget: not applicable; core-model budget remains in the separate
  model ledger;
- core model: requires the later composite pre-wire fence.

This result is a stable compiled decision, not a live permission. Portable,
restored, and trusted decision views all return false for external-effect, RAG,
and model-execution authorization. DispatchAttempt and encoded-request fields
are intentionally excluded from its stable hash and belong to separate
pre-wire evidence.

Policy-generation and RuntimeCatalog current pointers are checked and frozen at
dual-freeze time. Dispatch does not chase newer ordinary policy or catalog
generations, because doing so would make an active Run drift. Dispatch does
recheck both governance and model revocation suffixes and the current startup
model catalog immediately before first-byte linearization.

The general layered compiler remains a later module. Its fixed order is
platform, Tenant, Workspace, AgentVersion, Profile, then Task; the order is not
override precedence. All matching rules apply, deny wins, ceilings intersect,
scope clauses are ANDed, approval takes the strictest value, and numeric limits
take the minimum. `ANY` can restrict an existing exact candidate but can never
create provider membership or permission.

#### 5.2.4 Additive schema-v10 authority evidence

Migration `0010_unified_model_execution.sql` is additive. It does not reopen
schema-v9 bytes or reuse the legacy structural route and dispatch tables.
It first persists the missing per-member compiled governance-decision parents,
then introduces only the independent `core_model_execution_*_v1` authority
families whose canonical documents and hash domains are already locked.

Schema v10 covers:

- compiled `pure_chat` governance decisions and freeze checkpoints;
- portable core-model build, route, configuration, execution route-set, and
  route-set freeze-binding evidence;
- model-revocation records and exact target-parent projections;
- portable DispatchAttempt documents.

Startup trust seals, current startup-catalog objects, Secret values, loaded
artifact bytes, live grants, and process-local grant state are never persisted.
Durable evidence can be restored for audit and exact-parent verification only.

The schema-v10 repositories use caller-owned transactions for aggregate
writes. An exact retry succeeds only when the complete root, every ordered child,
canonical byte, and parent projection match. Missing or extra children,
non-canonical JSON, hash mismatch, mixed scope, orphan rows, or fallback to
schema-v3 legacy model tables is corruption.

The V1 model-revocation chain is intentionally complete rather than truncated:
genesis stores a SQL NULL predecessor that restores to the typed zero
reference, successors bind the exact preceding record, and the total chain is
bounded to 4096 records. No row beyond that bound may be written because the
current/freeze loaders must be able to reconstruct the complete chain.

Transfer logical schema v6 preserves the first 466 v5 relation identities
byte-for-byte. The compiled governance-decision identity is appended first,
followed only by newly named execution-authority and DispatchAttempt relations
whose canonical domains are locked for schema v10. Transfer remains disabled
until its descriptors, phases, roots, and zero-proof acceptance are
independently complete.

#### 5.2.5 Additive schema-v11 effect evidence

Migration `0011_unified_model_effects.sql` is a completed separate additive
slice. It was written only after the corresponding canonical documents,
hashes, state machines, and exact owner-fence parents were locked. It covers:

- a Unified-only semantic invocation reservation, isolated from every legacy
  `model_call_reservations` or retry path and keyed by a stable
  Run/member/checkpoint/plan-node/call-ordinal position rather than dynamic
  attempt IDs;
- durable PREWIRE issuance evidence that joins the compiled governance
  decision, both frozen revocation baselines, DispatchAttempt, owner epoch,
  reservation, Run authority, and admission evidence;
- the unique first-byte receipt, including the live governance/model
  revocation tails and current startup-catalog reference observed in the same
  caller-owned transaction;
- a closed typed terminal union for `NOT_EXECUTED`, `SUCCEEDED`,
  `REPORTED_ERROR`, and `UNKNOWN`;
- append-only UNKNOWN reconciliation evidence that cannot create a retry,
  verdict, current pointer, or semantic replay permission.

The process-local one-use Grant is never a table. Encoded request bytes,
headers, prompt content, Secret values, loaded artifacts, non-secret config
bytes, and startup-catalog objects are never persisted by this family; only
their already-locked digests, lengths, and non-secret exact references may be
parents. Usage, cache, reasoning-token, price, and cost settlement are a later
schema-v12 accounting slice so accounting failure cannot erase or block typed
terminal evidence.

Schema v11 is closed at the seven effect-evidence relations defined below.
`RunManifestV3`, V2/V3 compatibility evidence, and
`UnifiedModelPrewireIssuanceV2` are not schema-v11 relations. Physical
schema-v12 remains reserved for Unified usage, cache, reasoning-token, price,
and cost accounting; none of the V3 cutover relations may claim that version.
The physical migration number and transfer logical-schema version for V3 are
intentionally unassigned until both the schema-v12 accounting slice and the
complete typed parent set for V3 and IssuanceV2 are locked.

Before migration v11 was frozen, its remaining canonical domains additionally
locked and implemented:

- owner equality is now fixed as
  `DispatchAttempt.ClaimOwnerID == OwnerInstanceID` and
  `DispatchAttempt.ClaimEpoch == BackendOwnerEpoch`;
  `SourceBackendID` is a separate backend reference and must never be compared
  with `ClaimOwnerID`;
- the governance member ordinal/ID and Run-member binding to the model
  reservation's slot, runtime attempt, and model call;
- composite attempt/freeze parents over the same
  DTD/Tenant/Task/Run/route-set, never two unrelated bare hashes;
- a bounded recoverable normalized success/error envelope in terminal
  evidence, while keeping usage and cost outside the terminal transaction;
- one production-shaped Store-bound contract in which request bytes cannot
  leave the process-local grant until the durable receipt commits; commit
  failure never starts transport, and an observed receipt after an uncertain
  commit yields `UNKNOWN`, never a resend. Runtime wiring to this contract
  remains a separate cutover item.

The four terminal branch envelopes are now locked independently of the
terminal root and storage schema. `SUCCEEDED` embeds and rebinds one complete
verified V1 executor canonical result; `REPORTED_ERROR`, `NOT_EXECUTED`, and
`UNKNOWN` use closed provider-independent classifications and bounded safe
text. All four restore strictly as portable audit evidence, expose no
authorization/retry/verdict API, and carry no optional-module dependency.
`FIRST_BYTE_COMMIT_FAILED` is the commit-failure classification after a durable
issuance exists; failure to persist the issuance itself cannot manufacture a
terminal row.

The pure-domain `UnifiedModelTerminalEvidenceV1` root is also implemented as a
closed union with four dedicated constructors. `NOT_EXECUTED` requires an
absent Receipt represented by a null hash; `SUCCEEDED`, `REPORTED_ERROR`, and
`UNKNOWN` require and close the exact typed Receipt against the issuance.
The root stores the stable scope/member/invocation closure, parent hashes, and
the complete branch-envelope bytes, but no usage, cost, retry, replay, or
verdict. Its fixed typed JSON encoder deliberately does not apply recursive
JCS to the nested branch `RawMessage`, so verified success-envelope bytes are
not reordered. Strict restore, Windows shuffle, `go vet`, and Debian WSL race
validation pass.

`UnifiedModelUnknownReconciliationEvidenceV1` is implemented as an append-only
typed predecessor chain attached only to an `UNKNOWN` terminal. Four dedicated
constructors record provider status, observed signed-provider material, local
transport facts, or operator notes; ordinals and predecessor hashes are
derived internally and bounded to 4096. Provider-backed observations carry
only a bounded opaque artifact reference and digest. “Signed” does not claim
that a signature was verified. Local and operator observations carry no
external material. The chain has no current pointer, verdict, resolution,
retry, replay, or provider-query capability and passes independent Windows
shuffle, `go vet`, and Debian WSL race validation.

`UnifiedModelPrewireIssuanceV1` treats `RunManifestV2` only as the exact
Run/member/admission parent. The V2 manifest's historical
`freeagent.core-model-route-set-authority.v1` hash and the new
`freeagent.core-model-execution-route-set-authority.v1` hash are deliberately
different domains and must not be compared, aliased, or used as substitutes.
The V1 execution route is independently closed by the prepared dispatch and
the exact execution route-set freeze. `RunManifestV3` plus a later
`UnifiedModelPrewireIssuanceV2` will add the typed execution-route parent
directly; V1 and V2 evidence are not reinterpreted.

The pure-domain `UnifiedModelPrewireIssuanceV1` implementation now accepts
only complete typed governance, assembly selection/receipt, member
binding/snapshot, Run scope/manifest, Unified admission, stable invocation
reservation, prepared dispatch, execution freeze, and ACTIVE owner parents.
It closes the governance member ordinal and identity through the exact model
slot, runtime attempt, and model call, and strict restore remains
non-authorizing. Its zero-attachment rule belongs only to the
`PURE_CHAT_V1` evidence format; it is not a platform restriction on Role,
Memory, RAG, Skill, MCP, or Tool modules. Focused Windows shuffle, `go vet`,
and Debian WSL race validation pass.

The V2 compatibility bridge keeps three identities separate:

- `MemberSnapshotV2.CompatibilitySpecHash` is the deterministic legacy
  `AgentSpec` projection hash for the selected public `AgentVersion`; it is
  not the `ProfileCompatibilityLink` hash;
- `TeamSnapshot.TeamMember.AgentVersionID` is the opaque legacy
  `agent_versions` row identity required by `TaskAssemblySelectionV2`;
- `MemberSnapshotV2.AgentVersionID` is the public `AgentVersion.VersionID`.

Governance compilation and persistence therefore compare the compatibility
Spec hash with the selected member's exact `LegacyRow.SpecHash`.
`RunManifestV2` continues to close MemberID, AgentID, compatibility Spec hash,
the complete TeamSnapshot hash, and the complete public member snapshot, but
does not compare the legacy row ID directly with the public VersionID. The
exact public-to-legacy mapping remains closed by SelectionV2 and the later
Issuance chain. This corrects an impossible three-parent closure without
weakening frozen Team, canonical evidence, or execution authorization.

The minimal physical v11 slice is limited to seven newly named relations. Its
logical-schema-v7 append order is parent-first and is now fixed after the
477-relation v6 prefix:

1. ordinal 478, `unified_runtime_owner_epoch_transitions_v1`, is portable
   history for the closed owner lifecycle `CLAIMING -> ACTIVE|FENCED` and
   `ACTIVE -> RELEASED|FENCED`.
2. ordinal 479, `unified_model_invocation_reservations_v1`, is portable,
   non-authorizing authority evidence. Its semantic identity is derived from
   stable Run/member/checkpoint/plan-node/call-ordinal position plus a stable
   context digest; runtime-attempt, model-call, owner, route, and time values
   cannot change that semantic identity.
3. ordinal 480, `unified_model_prewire_issuances_v1`, is portable history
   joining the reservation, typed governance decision kind, member evidence,
   DispatchAttempt, route-set freeze, owner, admission, Run authority, and
   both frozen revocation references.
4. ordinal 481, `unified_model_first_byte_receipts_v1`, is portable history for
   the durable send-armed linearization commit. “First-byte receipt” means the
   release gate committed before any request byte can become visible to
   transport; it does not claim that the provider received or returned a
   byte. It binds both live revocation tails, the current startup-catalog
   reference, owner transition, and running Run lifecycle.
5. ordinal 482, `unified_model_terminal_evidence_v1`, is one closed tagged
   union. Only its dedicated constructors may create `NOT_EXECUTED`,
   `SUCCEEDED`, `REPORTED_ERROR`, or `UNKNOWN`; success and reported error
   carry a bounded, recoverable, normalized and secret-free result/error
   envelope.
6. ordinal 483, `unified_model_unknown_reconciliation_evidence_v1`, is
   append-only portable history chained to one `UNKNOWN` terminal. It can
   record read-only observations but never a verdict, replacement terminal,
   retry flag, current pointer, or replay permission.
7. ordinal 484, `unified_runtime_owner_fence_current_v1`, is engine-local
   current state with a monotonic gate sequence. A non-empty current row
   blocks transfer.

No mutable dispatch-current relation is added. Recoverable state is derived
from immutable evidence:

| Durable evidence | Recovery result |
|---|---|
| Issuance, no receipt and no terminal | `PREWIRE`; write `NOT_EXECUTED` |
| Receipt, no terminal | `POSTWIRE`; write `UNKNOWN` and stop the Run for reconciliation |
| `NOT_EXECUTED` terminal plus any receipt | corruption |
| `SUCCEEDED`, `REPORTED_ERROR`, or `UNKNOWN` without a receipt | corruption |
| Any valid terminal | `SETTLED`; never create another attempt for that semantic position |

The first-byte path is strictly ordered:

1. consume the one-use process-local grant;
2. begin one caller-owned write transaction and obtain writer serialization by
   advancing the exact owner gate sequence;
3. exact-load Unified admission, reservation, issuance, attempt, route-set
   freeze, governance decision, Run/member/slot parents, and current Run tail;
4. prove member-to-slot/runtime-attempt/model-call closure, exact owner
   equality, both complete live revocation suffixes, the current startup
   catalog, `RUNNING` lifecycle, and absence of terminal/receipt evidence;
5. insert the unique receipt and commit;
6. expire the active command and release the startup-catalog claim;
7. only then expose the internally frozen request bytes once to the
   no-redirect/no-retry transport starter.

This ordering deliberately permits a conservative false-positive `UNKNOWN`:
the process can fail after the receipt commits but before transport writes its
first byte. Recovery still treats receipt-without-terminal as `UNKNOWN`,
because treating it as `NOT_EXECUTED` would reopen semantic replay after a
different crash point in which bytes did leave the process. A literal
post-provider “first byte observed” receipt is not used because SQLite and a
remote provider cannot share one atomic transaction.

The pure-domain `UnifiedModelFirstByteReceiptV1` implementation now closes the
issuance, prepared dispatch, ACTIVE owner, startup catalog, both live
revocation references, and the current `RUNNING` Run transition. It records
only request digest/length and non-secret exact references, never request
bytes or provider output. A new owner epoch permits its first gate advance
from zero to one; `max-1 -> max` is valid and max cannot wrap. The object and
its complete detached AuditView are non-authorizing and have passed
independent Windows shuffle, `go vet`, and Debian WSL race validation.

The SQLite V1 production-shaped persistence primitive is now a concrete
Store-bound Grant, an unexported active commit command, and a pointer-only
one-use `CommittedWireStart`. It does not expose request bytes to its
linearization callback and does not accept a generic Committer that could
claim success without a durable commit. The transaction helper sees only typed
audit parents, never request bytes or transport. Only `Commit()==nil` may
create a start; the startup-catalog claim is released before the start becomes
visible, and transport takeover releases the physical dispatch lease before
any network wait. This boundary is implemented and tested, but it is not yet
called by a non-test runtime executor and is therefore not claimed as
production-wired.

The first SQL statement remains the exact owner-gate CAS. In the same writer
transaction, the coordinator exact-loads the durable issuance and all typed
parents, verifies both live revocation suffixes, and proves that no Receipt or
Terminal alias already exists. Existing canonical Receipt evidence blocks a
new start as `UNKNOWN`; existing canonical Terminal evidence blocks it as
settled; contradictory evidence is corruption. This check occurs before
receipt construction and before request access, so even a stale owner gate
cannot turn an idempotent receipt insert into a second transport start.

Every consume error burns the underlying composite grant. This closes the
cancellation interval between the outer Store-bound one-use CAS and the inner
catalog-grant CAS, preventing a request capability or startup-catalog claim
from remaining live. A commit error never sends. If the transaction did commit
but the driver returned an error, the follow-up exact read finds the Receipt
and classifies the invocation as `UNKNOWN`.

The authoritative follow-up classifier first exact-loads the persisted
Issuance and its complete typed parent closure, then reads only the exact
Receipt/Terminal aliases for that issuance. Missing or contradictory durable
authority is `CORRUPT`, unavailable storage is `RECOVERY_REQUIRED`, Receipt is
`UNKNOWN`, a valid Terminal is settled, and only exact evidence absence plus
the unchanged owner tuple/gate permits `NOT_EXECUTED`. A later legitimate
owner-gate advance by another invocation is `OWNER_FENCE_LOST`, not corruption
and never replay authority.

Terminal and transitive UNKNOWN restoration also validates the Receipt's
referenced `RUNNING` transition through every exact predecessor to ordinal
zero. It never substitutes a mutable lifecycle current/tail. Exact
trust-domain keys are canonical UTF-8, trimmed, control-free, and bounded.

Post-hardening verification on 2026-07-28 passed on the final source state:
the combined Windows commit-before-start, Terminal, and UNKNOWN repository
suite passed in 866.829 seconds; `go vet ./internal/store/sqlite` passed; the
focused Debian WSL `-race` suite covering retained evidence, grant burn,
Terminal history, and UNKNOWN history passed in 566.862 seconds; and the
complete Windows `go test ./internal/store/sqlite -count=1 -timeout 60m`
regression passed in 1896.107 seconds. These results validate the repository
and process-local boundary primitives only. They do not imply that a real
Runtime caller or production transport starter has been wired.

The public-path bridge is now independently accepted with a real trusted
governance view, prepared dispatch, startup-catalog controller/read lease,
composite grant, exclusive binding, and public
`BindUnifiedModelCommitBeforeStartGrantV1`. Its starter is an in-memory
recording implementation only. The bridge proves that raw grant copies become
unavailable immediately after Bind, catalog rotation remains pinned until the
binding finishes, the request becomes visible only after the Receipt commit,
and a synthetic commit failure performs zero sends, rolls back gate/Receipt,
releases the catalog claim, and cannot be retried through either capability.
The two Windows bridge tests passed in 20.537 seconds, the corresponding
Debian WSL `-race` run passed in 112.878 seconds, the full
`compositeprewire` Windows suite passed in 5.760 seconds, its WSL `-race`
suite passed in 40.058 seconds, and `go vet` passed. This is still a test-only
in-process bridge: it adds no Runtime caller, provider adapter, network
request, schema relation, or production transport claim.

Transfer logical schema v7 appends the locked schema-v11 effect relations after
the complete v6 prefix. It cannot reinterpret or insert into the v6 prefix.
Unified repositories require explicit `UNIFIED_CORE_V1` admission at entry and
have no foreign key, fallback, dual-write, or reconciliation branch into the
legacy dispatch tables. Legacy reconciliation remains restricted to
`LEGACY_COMPAT_V1`.

Two implementation boundaries are explicit. First, the seven-relation slice
does not add an eighth deferred-receipt/current relation. SQLite constrains a
full ACTIVE owner tuple gate advance and validates the subsequent Receipt,
while the private physical dispatch lease, the sole caller-owned repository
transaction, and rollback tests prove their operational atomicity. Second,
logical-schema-v7 work is structural inventory only. Because a Receipt import
would require trusted target owner-current state, import remains fail-closed
until a future staging phase, roots, descriptors, and zero proof are complete;
the owner-current trigger must not be weakened to make import appear enabled.

The checked-in structural seed now records logical version 7 and 484 ordered
relations. Its frozen inventory hash is
`4bbd76d1980b80394460a8b1e88a72d1a4df7f228e27c24a56d47d0118c6baa6`.
An explicit logical-v6 prefix fixture proves that all first 477 relation
identities remain unchanged and still produce
`e88722570925ce0ab9a76eef11cf7cab01fbb7b112e817fe09db3cd96c06d559`.
This seed update is not transfer-schema or import enablement:
`LoadCheckedInSchemaAuthority` remains unavailable until the independent
descriptor vocabulary, phases, roots, and engine-local zero proof exist.

The physical schema-v11 migration is now implemented as exactly those seven
relations, with no backfill and no eighth deferred/current relation. Its
checked-in schema fingerprint is
`58a331f917a9771fec95073a1c9141fabde8ecd69e6619112a67a8e16807102a`.
Fresh creation, v10 upgrade with unchanged historical canonical bytes,
transaction rollback, owner gate limits/CAS, gate-plus-receipt rollback,
Receipt/NOT_EXECUTED conflict, UNKNOWN predecessor/material rules, Windows
`go vet`, and Debian WSL race checks have passed. Two adversarial closure
checks are explicit: Issuance binds the compiled decision's exact
selection/receipt pair, and Receipt binds the DispatchAttempt's exact
startup-catalog generation/hash.

These relations contain no Role, Memory, RAG, Skill, MCP, Tool, or optional
Profile attachment dependency. Their uniqueness is scoped to
Run/member/slot/semantic position, never to a globally unique AgentVersion or
Workspace, so they do not narrow free Agent/Workspace composition.

#### 5.2.6 Locked backend owner fence

The owner fence is backend-wide. Its only current-row key is
`(deployment_trust_domain_id, source_backend_id)`; Tenant, Workspace, Agent,
Task, and Run never participate in ownership identity.

The field mapping is exact:

- `DispatchAttempt.ClaimOwnerID == OwnerInstanceID`;
- `DispatchAttempt.ClaimEpoch == BackendOwnerEpoch`;
- `DispatchAttempt.DeploymentTrustDomainID == owner DTD`;
- `SourceBackendID` remains a separate physical-backend lineage identifier and
  is never compared with `ClaimOwnerID`.

Portable owner transition history records the package-derived owner identity
and the closed lifecycle. The engine-local current row stores the exact owner
identity, current transition ordinal/hash/state, and a positive monotonic gate
sequence. It stores neither canonical JSON nor a lock-proof digest. A
first-byte transaction must advance the complete ACTIVE current tuple by
exactly one gate sequence before writing its receipt.

The fence clock is the pair `(backend_owner_epoch, gate_sequence)`, not a
standalone sequence. `gate_sequence` is strictly monotonic and non-wrapping
within one owner epoch; a strictly newer epoch may initialize it again at one.
Every receipt binds both values and the exact ACTIVE transition hash, so a
sequence from an older epoch cannot satisfy a newer current row. This avoids a
hidden backend-global counter after a clean current-row deletion while
preserving strict fencing.

The filesystem/database ownership guard is an unexported process capability.
Only the primary runtime Store may derive it after obtaining physical database
ownership; owner-derived sibling handles share it, while offline maintenance
ownership cannot derive it. Loss of the Store ownership or a fatal fence
invalidates every copy. A digest, restored transition, ACTIVE reference,
current row, or guard in isolation never authorizes dispatch.

The physical guard base is implemented for file-backed and private-memory
Stores. It binds one self-identifying runtime-primary lease to one Store,
shares only with owner-derived siblings, rejects copied Store/lease/guard
lineages and offline ownership, invalidates the whole lineage on primary close
or fatal fence, and detaches only the closing sibling. Its focused Windows
shuffle/vet checks, WSL race check including the cross-process reopen path,
and the post-change full SQLite regression pass.

The second process-local slice, `runtimePhysicalDispatchLease`, retains the
exact Store/guard lineage only from the caller-owned first-byte transaction
through transport-starter takeover. Ordinary value copies share one
idempotent release,
while primary close, fatal fencing, and same-lineage sibling detach wait for
that release. The starter must release it before waiting for a network
response. It is unexported, non-portable, non-restorable, and non-persistent;
it has passed independent Windows shuffle, `go vet`, and Debian WSL race
validation.

Owner transition history is portable. A non-empty owner-current relation is
engine-local and blocks logical transfer. An offline physical backup may copy
that row, but restart or disaster takeover must first obtain physical
ownership, fence any abandoned non-terminal current, claim a strictly newer
epoch in `CLAIMING`, finish recovery and UNKNOWN reconciliation, and only then
transition to `ACTIVE`.

### 5.3 P0-C: atomic dual-freeze

At the synchronous composition lease boundary, one transaction writes or
verifies:

- the legacy compatibility projection still needed by the executor;
- TaskAssemblySelection and TaskAssemblyReceipt;
- governance parents;
- MemberSnapshot;
- model route parent;
- RunManifest;
- initial `FROZEN` lifecycle transition;
- RuntimeCatalog generation lease;
- composition lease release.

Any failure rolls back the entire transaction. Asynchronous post-ingress
backfill is not accepted because a queued Task could race the scheduler.

### 5.4 P0-D target: final RunManifestV3; current slice: non-authorizing shadow

Final V3 will freeze public assembly and execution-selection authority
directly. It will be durable, non-authorizing evidence: it will not grant
model, Tool, MCP, provider, outbox, or first-byte permission, and a restored V3
will never be promotable into the process-local one-use dispatch grant.

The final V3 canonical schema, hash domain, and constructor are deliberately
not locked yet because the legacy-free Team/Selection/Receipt, Activation, and
authenticated compiler authority inventory is incomplete. The current bounded
slice therefore uses a separately named and separately hashed shadow object;
that object is diagnostic evidence only and is not final V3 authority.

#### 5.4.1 Canonical parent closure

The final V3 constructor and restore path will accept the complete typed
`CoreModelRouteSetAuthorityV1` in the
`freeagent.core-model-execution-route-set-authority.v1` domain. They will never
accept the legacy `CoreModelRouteSetParentV1`, and will never compare, alias,
or substitute a `freeagent.core-model-route-set-*` hash for an
execution-route hash.

Final V3 will close and freeze:

- exact WorkspaceDefinition;
- ordered exact AgentVersions and member plan;
- exact AssemblyProfile;
- exact TaskRequirements and TaskScope;
- RuntimeCatalog Generation;
- legacy-free compiler receipt and member snapshots;
- legacy-free team/member-plan authority;
- exact execution CoreModelRouteSet authority;
- a generic ordered attachment-parent union, including an explicit
  zero-attachment projection for `PURE_CHAT`;
- governance, authority, and budget parents; RAG evidence exists only as a
  typed branch of the generic attachment-parent union for a `MODULAR` member
  that actually selected it.

Every constructor and restore operation must recompute the complete canonical
parent closure. The canonical body stores exact references, hashes, counts,
and ordered projections. It does not store request bytes, Secret values,
mutable "latest" references, timestamps, dynamic attempt IDs, or provider
responses. The legacy Profile is either reached through verified compatibility
evidence or absent.

#### 5.4.2 Attachment modes and current feasibility

Attachment mode is a per-member assembly property, not a global Agent-kind
permission:

- a `PURE_CHAT` member freezes one generic zero-attachment union and does not
  resolve or create Role, Persona, Memory, Module, Knowledge/RAG, Skill, MCP,
  Tool, optional Host, or extension SecretRef state; every corresponding
  binding array and attachment count is explicitly empty or zero;
- a `MODULAR` professional or composite member has ordered selected provider
  bindings, with exact `REQUIRED` or `OPTIONAL` failure policy, exposure,
  permission, configuration, and catalog references;
- specialist and composite Agent kinds remain AgentVersion, TeamSnapshot, and
  member-plan concerns. They do not silently grant provider authority, and a
  missing `REQUIRED` binding cannot degrade to pure chat.

A `pure_chat` V3 shadow is feasible with the currently sealed zero-attachment
selection, receipt, member snapshot, governance, and execution-route parents.
It may be constructed and compared without changing the production read path.
Production persistence, restart recovery, and cutover still wait for the
future additive schema described in section 5.4.6.

The shadow has an identity that cannot be confused with final V3:

- Go type: `PureChatRunManifestV3ShadowV1`;
- hash domain: `freeagent.pure-chat-run-manifest-v3-shadow.v1`;
- package boundary: `internal/runmanifestv3`;
- authority: diagnostic comparison only; every execution/effect authorizer is
  permanently false;
- persistence: none in this slice;
- upgrade: no promotion, conversion, or fallback into final V3,
  `UnifiedModelPrewireIssuanceV2`, or any dispatch parent.

The shadow does not use the future final V3 hash domain and never enters the
production read path. Current Activation, TeamSnapshot, SelectionV2,
ReceiptV2, MemberSnapshotV2, and governance views are accepted only as
transient typed witnesses used to construct and verify its projection. Their
legacy identities and hashes are excluded from the shadow wire.

The portable `PureChatCompiledGovernanceDecisionV1` now exposes a complete
detached `AuditView`: all canonical scope, member, catalog, selection,
receipt, binding, snapshot, attachment-decision, and self-hash facts can be
checked through typed accessors, while compiler provenance, revocation suffix
verification, and every authorization capability remain process-local. The
shadow constructor may use V2 selection/receipt and the current
Activation/Team snapshot only as transient compatibility witnesses; their
legacy route/profile semantics must not enter the V3 authority wire.

`MODULAR` V3 is deliberately fail-closed at this stage. The existing structural
SDK binding shapes do not prove provider selection authority. Stage 3 must
provide sealed Skill/content/RAG resolver, revision, and provenance parents;
Stage 4 must provide sealed MCP server, discovery, permission-set, Host, and
provider-resolution parents. A generalized assembly compiler/receipt,
governance decision, prewire evidence, and process-local grant must close
those parents before a professional or composite member may execute.

#### 5.4.3 V2 compatibility and evidence versioning

V2 canonical bytes, hash domains, and restore behavior remain frozen. A later,
separately versioned V2/V3 compatibility evidence object may prove exact
equality of non-route facts such as DTD, Tenant, Workspace, Task, Run,
TaskScope, activation, requirements, catalog, team, member identity, and
member order. Its final name and domain are not assigned here.

That compatibility evidence:

- is append-only, portable, and non-authorizing;
- does not prove legacy-route and execution-route equivalence;
- does not permit either route hash to replace the other;
- is not a parent required for V3 authority;
- never permits a failed V3 load, validation, or dispatch to fall back to V2.

Historical V2-only Runs continue on their frozen path. A V3 Run uses only its
exact V3 parents. Existing V1 Issuance, Receipt, Terminal, and UNKNOWN
reconciliation evidence remains frozen and is never reinterpreted, upgraded,
or accepted as V3 or IssuanceV2 authority.

#### 5.4.4 UnifiedModelPrewireIssuanceV2 boundary

`UnifiedModelPrewireIssuanceV2` requires a new canonical schema and hash
domain. It accepts the complete V3, the same complete execution route-set
authority, the exact route-set freeze binding, prepared dispatch and attempt,
trusted governance evidence, exact member, TaskAssemblyReceipt,
member-snapshot, run-scope, admission, reservation, and ACTIVE owner parents.

It must prove:

- V3, prepared dispatch, attempt, and freeze all name the same execution
  route-set authority;
- the selected ordinal exists in that route set and closes the exact entry
  hash, phase, candidate, route, build, and configuration;
- the freeze contains a verified genesis-to-frozen revocation baseline and the
  prepared dispatch refers to that exact freeze;
- activation, requirements, catalog, team, member, TaskAssemblyReceipt,
  governance, reservation, owner, and both revocation tails close without
  inference.

IssuanceV2 has no constructor or upgrader from IssuanceV1 or RunManifestV2.
Strictly restored IssuanceV2 remains audit-only. Because the current
first-byte Receipt, Terminal, and UNKNOWN chain references IssuanceV1
directly, production cutover also requires a separately versioned downstream
effect-evidence family and a generalized process-local grant. Adding an
IssuanceV2 row alone is insufficient.

#### 5.4.5 Activation, isolation, recovery, and rollback

Activation selects Workspace availability; it does not mutate a RunManifest.
At new-Run freeze, the runtime reads one exact current activation, loads its
exact generation parents, compiles the member plan, and creates V3 atomically.

- current activation remains keyed by Tenant and Workspace and is changed only
  by exact expected-generation/hash CAS;
- concurrent activation has one winner and failed CAS leaves current
  unchanged;
- new Runs observe the new generation, while active Runs retain their exact V3
  snapshot;
- rollback copies a prior exact state into a new generation rather than
  rewriting history;
- the same AgentVersion may participate in several Workspaces with different
  member bindings;
- TaskScope read-only Workspace references are same-Tenant read authority only
  and cannot borrow another Workspace's current activation or write authority.

Recovery loads V3 and every parent by exact scope, generation, and hash, then
recomputes canonical bytes and hashes. It never reads current, recompiles from
a newer catalog, reconstructs from V2, or crosses Workspace/Tenant scope.
Missing or corrupt parents fail closed and enter repair or reconciliation.
Backup/restore must include V3, compatibility evidence when present, and all
exact parents. Revocation independently blocks new admission or effects.

The current activation and selection formats require legacy compatibility in
places where final V3 permits it to be absent. Their legacy-free successors,
the V3 lifecycle/freeze and catalog-lease anchor, and the versioned downstream
effect chain are explicit typed-parent gaps and constitute a larger cutover
refactor; they must not be simulated with bare hashes or optional unchecked
fields.

The current `TeamSnapshot` is also a transitional witness: it still carries
legacy member/route semantics and has no public strict canonical authority
surface suitable for final V3. Formal V3 therefore additionally requires a
legacy-free Team/Selection/Receipt authority family (or an equivalent direct
typed authoring-parent closure) and, if compiler identity is authoritative, an
authenticated compiler-artifact authority rather than a metadata-only
`CompilerRef`.

#### 5.4.6 Persistence allocation and minimal implementation slices

The version allocation is fixed as follows:

- schema v11 remains the locked seven-relation V1 effect slice and contains no
  V3 relation;
- schema v12 remains reserved for Unified usage/cache/reasoning/price/cost
  accounting;
- V3 manifest, ordered-member, compatibility, IssuanceV2, lifecycle, and
  downstream V2 effect relations receive no physical migration number,
  transfer logical-schema version, ordinal, or frozen relation count in this
  document; in particular, this document neither assigns schema v12 nor
  pre-allocates schema v13 to them;
- those identifiers are assigned only after schema-v12 accounting and the
  complete V3/IssuanceV2 typed parent inventory are both locked.

The following order is internal to the V3 subplan. Stage 2 first completes a
test-only public-Bind bridge through the real startup-catalog claim and
composite grant; that bridge does not wire Runtime, provider, or network
execution.

Implementation proceeds in bounded V3 slices:

1. lock the independent shadow canonical bytes/domain and
   type/domain-confusion tests, and construct a zero-attachment `pure_chat`
   shadow without defining final V3 bytes, adding a migration, or changing a
   runtime read;
2. complete the Stage 3 and Stage 4 sealed resolver parents and the
   legacy-free Activation, Team/Selection/Receipt, member-snapshot, and
   authenticated compiler authority needed by final V3 and `MODULAR`;
3. after the complete typed-parent inventory is locked, define the final
   RunManifestV3 canonical schema/hash domain and constructor;
4. lock the full persistence inventory, allocate a future additive schema, and
   implement exact write/load/recompute, CAS, restart, backup, corruption, and
   partial-transaction tests;
5. add the V3 lifecycle and IssuanceV2/downstream effect family while keeping
   V1 immutable;
6. change the production read/dispatch path only after shadow comparison,
   concurrency, recovery, multi-Workspace isolation, and fault-injection
   acceptance passes.

Acceptance includes canonical/golden/hash shuffle tests, strict JSON rejection,
typed legacy/execution-route confusion tests, parent substitution across every
scope, explicit pure-chat empty-binding and zero Host/Secret tests, ordered
modular binding tests once sealed parents exist, unchanged V2 byte/hash
fixtures, compatibility-without-route-equivalence tests, no-fallback tests,
CAS contention, old-Run/new-Run snapshot isolation, multi-Workspace and
cross-Tenant negatives, exact restart/backup/corruption behavior, and
IssuanceV1 non-upgrade tests.

#### 5.4.7 Implemented pure-chat shadow boundary

The bounded `PureChatRunManifestV3ShadowV1` slice is now implemented in
`internal/runmanifestv3`. Its canonical body and self-hash use only the
independent `freeagent.pure-chat-run-manifest-v3-shadow.v1` domain. New and
Restore require the concrete ActivationGeneration, TeamSnapshot,
TaskAssemblySelectionV2, TaskAssemblyReceiptV2, ordered MemberSnapshotV2 and
compiled-governance AuditView witnesses, and the concrete
CoreModelRouteSetAuthorityV1. Restore accepts exact RFC 8785 bytes only and
then reconstructs the complete projection from those typed witnesses before
accepting byte equality.

The wire freezes public WorkspaceDefinition, AssemblyProfile,
TaskRequirements, RuntimeCatalog, AgentVersion, governance, scope, budget,
revocation, and exact execution-route projections. Activation, Team,
SelectionV2, ReceiptV2, MemberSnapshotV2, compiled-decision, compatibility,
legacy route, and metadata-only compiler identities remain transient and are
not serialized. The generic zero-attachment union contains separate
ROLE, PERSONA, MEMORY, MODULE, KNOWLEDGE_RAG, SKILL, MCP, TOOL, HOST,
SECRET_REF, and EXTENSION entries; every entry is exactly count zero with a
non-null empty ordered binding array. This keeps the optional module families
independent and does not reinterpret the legacy Team persona string as a
Persona module.

Every model, external-effect, RAG, Tool, MCP, provider-dispatch, first-byte,
Host, and Secret authorizer is permanently false. The package has no
repository, migration, production reader, provider adapter, grant, promotion,
upgrade, Issuance conversion, or fallback API. Its limits are 4 MiB, 256
members, 192 execution-route entries, JSON depth 32, and 65,536 JSON nodes.
The current canonical helper cannot distinguish depth/node-limit failures
from other malformed canonical JSON through typed errors, so those cases are
reported as document-invalid while still failing closed; byte and member
limits retain explicit limit errors.

The legacy TeamSnapshot remains a transient structural witness because the
team package has no public strict canonical verification surface. The shadow
checks its Selection hash relationship, legacy route relationship, and exact
member mapping without copying the private Team hash algorithm. Final V3
still requires the legacy-free Team/Selection/Receipt authority family
described above.

Windows package and shuffle tests, strict JSON/hash/domain confusion tests,
typed-parent substitution tests, golden shadow/canonical fingerprints,
defensive-copy and concurrent-construction tests pass. Debian WSL targeted
`-race` passes, as does `go vet` for `runmanifestv3` and
`compositeprewire`. These results do not claim final V3 persistence or
production runtime wiring.

Impact on the original modular design is positive to neutral. V3 strengthens
the default-light Core and permits the same AgentVersion to receive different
ordered attachments in different Workspaces without making those attachments
part of Agent identity. Role, persona, memory, RAG, Skill, MCP, and future
affective behavior remain generic versioned module/content parents rather than
hard-coded RunManifest fields. No PAB-specific schema, domain, branch, or
runtime dependency is introduced.

## 6. Activation semantics

An activation is scoped to one Tenant and Workspace and freezes exact
WorkspaceDefinition, AssemblyProfile, and RuntimeCatalog references.

```text
Import -> Validate -> Review -> Stage
       -> CAS Activate Generation -> New Run
       -> Rollback / Revoke / Retire
```

Rules:

- import does not activate;
- current is switched with an exact expected generation/hash CAS token;
- concurrent activation has exactly one winner;
- a failed activation leaves current unchanged;
- rollback copies a prior exact state into a new generation and records its
  source generation;
- a Workspace roster remains the availability boundary; a task-level member
  selection still requires authenticated selection evidence;
- emergency revocation blocks new admission immediately and follows the
  separate terminal policy for affected active Runs.

## 7. Online control-plane boundary

The control plane is disabled unless explicitly configured.

Every mutation requires:

- authenticated bearer identity mapped to a configured actor;
- operation-specific authorization;
- an idempotency key bound to the canonical request hash;
- bounded body and strict media type;
- append-only audit evidence;
- exact expected-current CAS where applicable.

Actor identity never comes from an untrusted request header. Import, review,
stage, activate, rollback, revoke, and restart remain distinct operations.
Offline CLI remains available for disaster recovery, backup/restore,
migration, and manual reconciliation.

## 8. Acceptance

Stage 2 is complete only when:

- a new `pure_chat` Run follows the unified authority chain in production;
- no optional attachment or Host is constructed for `pure_chat`;
- non-default Tenant and multi-Workspace activation are isolated;
- new Runs see a successful activation and active Runs retain old snapshots;
- concurrent CAS, rollback, restart, backup/restore, corruption, and injected
  transaction failures pass;
- V2 history restores without byte or hash changes;
- V3 is the only mutable path capable of selecting new runtime assembly;
- capability and release documents describe only production-wired behavior.
