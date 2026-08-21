# Stage 3 sealed Skill / content / RAG parent design lock

> S0 状态：`S2_SEMANTIC_SOURCE_ONLY`。仅供 S2 内容寻址语义参考，不是当前规范或生产完成证明；当前架构权威见本目录 `README.md`。

Date: 2026-07-28  
Status: **LOCKED FOR STAGED IMPLEMENTATION**  
Scope: Stage 3 lower-layer content authorities only

## 1. Purpose and authority

This document removes the Stage 2 / Stage 3 dependency cycle and locks the
minimum parent inventory required before a non-empty Skill or Knowledge/RAG
attachment can enter member assembly.

It is deliberately narrower than the historical runtime-catalog and governance
design documents. It does not lock final RunManifest V3 bytes, enable a
production read path, implement MCP execution, or turn content into executable
authority.

For the subjects named below, this document has priority over conflicting older
text:

- `docs/NEXT_PHASE_PLAN.md` no longer blocks Stage 3 on final V3 completion;
- the legacy projection in
  `2026-07-19-task3-lossless-persistence-design.md` that maps Persona into a
  Role attachment is compatibility behavior only, not the current modular
  identity model;
- Sidecar-, PAB-, legacy route-, or mutable-`latest`-based content designs in
  `2026-07-21-runtime-catalog-authority-design.md` are not implementation
  authority;
- the exact content wire descriptions in
  `2026-07-22-governance-rule-wire-v1-lock.md` remain useful only where they are
  consistent with this document, the current Stage 2 boundary, and existing
  immutable bytes.

This document does not rewrite an existing canonical document, hash domain,
database migration, or persisted row.

Normative terms `MUST`, `MUST NOT`, `SHOULD`, and `MAY` are used literally.

## 2. Stage boundary correction

### 2.1 Stage 2 baseline is sufficient to start Stage 3

The following completed Stage 2 boundary is the entry condition for Stage 3:

- `PureChatRunManifestV3ShadowV1` exists as diagnostic-only evidence;
- its generic zero-attachment union keeps Role, Persona, Memory, Module,
  Knowledge/RAG, Skill, MCP, Tool, Host, SecretRef, and Extension independent;
- `PURE_CHAT` does not resolve or create any optional attachment;
- current `MemberSnapshotV2` remains usable for pure chat and the already
  supported provider-only path;
- non-empty Skill/content claims remain fail-closed because their sealed
  resolver parents do not yet exist.

Stage 3 therefore **MAY start now**. It does not wait for final RunManifest V3.

### 2.2 Final V3 is cross-stage convergence

Final RunManifest V3 is not a Stage 2 prerequisite for Stage 3. It is a later
cross-stage convergence milestone requiring all of the following:

1. the Stage 2 baseline and legacy-free assembly parents;
2. the Stage 3 sealed Skill/content/RAG parents defined here;
3. the Stage 4 sealed MCP discovery, permission, Host, and provider parents;
4. a generalized authenticated compiler, receipt, governance decision,
   prewire, persistence, recovery, and process-local grant path.

No Stage 3 slice may create a shortcut final V3, promote the Stage 2 shadow, or
change the production dispatch path.

## 3. Non-negotiable modular invariants

### 3.1 Independent attachment families

The generic attachment families remain separate:

```text
ROLE
PERSONA
MEMORY
MODULE
KNOWLEDGE_RAG
SKILL
MCP
TOOL
HOST
SECRET_REF
EXTENSION
```

In particular:

- Role and Persona are not aliases and neither implies the other;
- Memory is not Persona state, Role state, Skill content, or a RAG collection;
- Knowledge/RAG is not embedded in Agent identity;
- Skill is not a Module, Tool, MCP server, Host, or permission grant;
- sharing one immutable `ContentPayload` storage primitive does not merge the
  authority, ownership, lifecycle, or attachment kind of its callers.

A compatibility adapter MAY project a legacy Persona string into an explicit
Role or Persona attachment, but the adapter MUST emit versioned compatibility
evidence. It MUST NOT establish canonical equality between Role and Persona.

PAB or any other affective/personality implementation is outside the core. A
future implementation may use ordinary optional Module/Role/Persona contracts
without adding a PAB-specific core branch.

### 3.2 Free Agent and Workspace composition

An AgentVersion carries lightweight identity, routing/capability hints, weights,
and explicit attachment requests. It does not own a private copy of shared
domain knowledge.

WorkspaceDefinition, AgentVersion, AssemblyProfile, and TaskRequirements may
each contribute or narrow optional attachments. Resolution occurs per member
and per Run:

```text
immutable AgentVersion
        + WorkspaceDefinition
        + AssemblyProfile
        + TaskRequirements
        + applicable governance
        -> ordered exact member attachments
```

The same AgentVersion MUST be usable in multiple Workspaces with different
ordered attachments, policies, and data scopes. That operation MUST NOT mutate
the AgentVersion or leak content, memory, permissions, retrieval cache entries,
or provenance between Workspaces.

Those contributions use independent, sealed contribution-generation parents.
They MUST NOT be implemented by adding fields to the current immutable
WorkspaceDefinition, AgentVersion, AssemblyProfile, or TaskRequirements v1
wires or by changing their existing canonical bytes and hash domains.

Specialist and composite Agents remain assembly/team concerns. Their domain
weights can influence a permitted selection request, but never create content
membership or permission.

### 3.3 Skill is content, not execution or permission

A Skill is an immutable, versioned instruction/workflow package. It MAY declare
required capabilities and exact Tool descriptor expectations, but these are
requirements only.

A Skill MUST NOT:

- grant Tool, network, filesystem, process, Workspace, Secret, Host, Module, or
  MCP authority;
- install or activate itself;
- cause implicit network download, package-manager execution, PATH lookup, or
  shell execution;
- convert text contained in `SKILL.md` or a resource into trusted code;
- bypass Module/MCP admission, ToolGateway, budget, effect, or audit gates.

If a Skill needs execution, a separately selected and authorized Module or MCP
provider MUST satisfy the declared requirement. Absence of that provider makes
a required Skill unavailable; it does not create a fallback execution path.

### 3.4 Shared RAG is decoupled from Agent identity

Knowledge content is tenant-scoped shared content with explicit Workspace,
collection, data-scope, and policy boundaries. Multiple authorized Agents may
reference the same immutable content body without duplicating it into each
Agent Workspace.

An Agent or Workspace stores only collection bindings, retrieval policy, exact
selection evidence, and small non-authoritative routing statistics. A category
counter, high-frequency-term counter, or retrieval cache MAY avoid redundant
retrieval, but it MUST NOT become knowledge authority or serve content without
exact source/version/provenance evidence.

The default professional flow is:

```text
task/context classification
        -> permitted collection bindings
        -> exact retrieval decision
        -> verified content versions
        -> bounded projection into context
        -> actual-use provenance
```

### 3.5 `pure_chat` performs zero optional resolution

For a `PURE_CHAT` member, the assembly/compiler/runtime path MUST NOT:

- enumerate, scan, match, restore, load, parse, index, or dereference Role,
  Persona, Memory, Module, Knowledge/RAG, Skill, MCP, Tool, optional Host,
  SecretRef, or Extension state;
- call a Role, Persona, Memory, Module, Skill, content, RAG, MCP, Tool, Host, or
  extension repository;
- read a mutable current pointer for any optional family;
- create an optional-attachment candidate, retrieval query, content projection,
  optional-content provenance record, Module/MCP/Tool provider request, Host
  process, or optional Secret lookup.

It MUST only freeze the explicit zero-attachment projection already defined by
Stage 2.

This restriction does not prohibit the mandatory core model request, model
usage/effect accounting, conversation persistence, or core audit evidence that
make ordinary pure chat work.

A common RuntimeCatalog header or Skill anchor may be carried as an already
validated opaque parent from the mandatory assembly boundary. The pure-chat
member path MUST NOT dereference that anchor or use it to obtain membership,
content, permission, or revocation state.

Tests MUST use counting or fail-on-call repository spies to prove zero optional
lookups. An empty result returned after an optional lookup is not zero
resolution.

## 4. Sealed parent inventory

Each production authority in this section MUST have a closed strict wire, a
separate hash domain, deterministic canonical encoding, bounded counts/bytes,
and:

```text
New / Restore / Validate / CanonicalJSON / Hash or sealed ResolutionView
```

Portable scalar references and self-hashed structural documents are not enough
to claim membership, lifecycle, selection, or use authority.

### 4.1 Common immutable content substrate

`ContentPayloadV1` in this document is shorthand for the already locked
`ContentPayloadDocumentV1` / `freeagent.content-payload.v1` exact wire. It is
the tenant-scoped, immutable content-addressed body primitive shared by Skill
and Knowledge/RAG storage.

Its V1 sealed identity remains exactly:

- SchemaVersion;
- TenantID;
- ContentDigest;
- ContentBytes, meaning exact byte length rather than body bytes;
- PayloadEncodingID (`RAW_BYTES_V1`);
- LogicalBlobID;
- ContentPayloadHash.

The body lives only in the managed content artifact addressed by
LogicalBlobID; the portable document does not duplicate it. Stage 3 MUST NOT
add media type, content type, inline body, or another field to this V1 preimage.
If a later parent needs those fields, it binds them outside V1 or defines a V2
wire and a new hash domain.

`ContentPayloadV1` proves body integrity only. It does not prove collection
membership, Skill publication, visibility, data scope, attachment selection,
model-context use, or execution permission.

Blob retention, purge, tombstone, and corruption behavior MUST be specified
before production persistence. A missing or digest-mismatched retained blob
fails closed.

The exact V1 byte rules are:

```text
LogicalBlobID =
  Digest(
    "freeagent.content-payload-blob-id.v1",
    RFC8785({"content_digest": ContentDigest, "tenant_id": TenantID})
  )

ContentPayloadHash =
  Digest(
    "freeagent.content-payload.v1",
    RFC8785({
      "schema_version": 1,
      "tenant_id": TenantID,
      "content_digest": ContentDigest,
      "content_bytes": ContentBytes,
      "payload_encoding_id": "RAW_BYTES_V1",
      "logical_blob_id": LogicalBlobID
    })
  )
```

`ContentDigest` is lowercase raw SHA-256 of the exact managed body bytes.
`Digest(domain, bytes)` is the existing `moduleapi.Digest` domain-separated
operation. These definitions add no field to the historical V1 document; they
remove the previous ambiguity in the phrase `TenantID/ContentDigest`.

### 4.2 Skill package and catalog parents

Stage 3 MUST provide the following one-way parent chain:

```text
ContentPayloadV1
    + validated skillcatalog artifact/version
    -> SkillPackageEvidenceV1
    -> SkillContentCatalogMembershipV1
    -> SkillContentCatalogGenerationV1
    -> SkillContentCatalogMembershipRefV1
    -> sealed SkillMembershipResolutionView
```

The chain has these rules:

- `SkillCatalogVersionRefV1` belongs in a neutral lower package that imports
  neither governance nor skillcatalog;
- the current `skillcatalog -> governance` proposal adapter MUST move to an
  upper bridge before governance also consumes sealed skillcatalog views;
- package evidence binds exact Skill ID, revision, content hash, manifest and
  package digests, deterministic source tags, and `ContentPayloadV1`;
- source tags are deterministically projected from the validated manifest and
  cannot be caller-supplied metadata;
- catalog membership is per tenant, immutable, ordered, and content addressed;
- a generation Restore operation receives and revalidates the complete member
  documents, not only an array of hashes;
- duplicate package bodies, Skill ID/revision identities, public assembly
  references, or ambiguous active revisions fail closed;
- RuntimeCatalog freezes the exact Skill catalog generation/hash anchor but
  does not turn Skill into an executable provider.

The Stage 3 neutral-reference conversion and every new sealed Skill authority
use the actual Skill catalog grammar for `SkillID`: 1..128 ASCII bytes, first
byte `a..z`, remaining bytes limited to lowercase letters, digits, dot,
underscore, and hyphen. Canonical inputs keep identical fields, bytes, and
hashes. Historical `modulebinding` V1 structural Restore retains its original
opaque UTF-8 grammar for audit compatibility, but a value outside the strict
catalog grammar cannot pass neutral conversion, become package evidence, or be
promoted into sealed authority. V2 has never admitted a non-empty Skill
authority.

Artifact identity and lifecycle authority are separate. Candidate, canary,
active, quarantined, revoked, and retired status MUST NOT be mutable fields
inside the immutable package hash. A trusted review/activation authority
selects an exact package/catalog generation.

Local import or upload, Agent-proposed candidate creation, PR-like review,
approval, activation, rollback, quarantine, revocation, retirement, permanent
submitted-source deduplication, and permanent rejected-source exclusion remain
separate auditable lifecycle operations. No lifecycle transition may be
inferred from portable content bytes.

#### 4.2.1 Skill digest and payload byte lock

For `SkillPackageEvidenceV1`, the Skill payload has one exact meaning:
`ContentPayloadV1` contains the exact UTF-8 bytes of the validated
`SkillVersion.SkillMD`. It is not an upload archive and does not contain
resource bodies.

The validated Skill artifact first produces two RFC 8785 documents:

```text
ManifestWire = RFC8785(SkillManifest)

PackageProjectionWire = RFC8785({
  "schema_version": SkillVersion.SchemaVersion,
  "manifest": SkillVersion.Manifest,
  "skill_md": SkillVersion.SkillMD,
  "resources": SkillVersion.Resources,
  "dependency_lock_digest": SkillVersion.DependencyLockDigest,
  "tool_bindings": SkillVersion.ToolBindings
})
```

The projection uses the already validated and canonically ordered manifest,
resource, capability, and Tool-binding values. It excludes lifecycle `Status`,
`SkillMDTokenEstimate`, and `ContentHash`.

```text
ManifestDigest = lowercase raw SHA256(ManifestWire)
PackageDigest  = lowercase raw SHA256(PackageProjectionWire)
```

`PackageDigest` is a semantic artifact digest, not the digest of an uploaded
ZIP, directory, or transport envelope. A future importer that needs to attest
original upload bytes MUST add separate `SkillPackageImportEvidence` and MUST
NOT reinterpret this digest.

ResourceDigest entries remain integrity requirements only. A resource body
cannot be loaded until a later exact resource-payload parent proves its path,
digest, byte length, tenant, package evidence, and governed use. Likewise,
required Tool bindings remain requirements and never become execution grants.

`SourceTagSetV1` uses
`freeagent.governance-source-tag-set.v1`. A generic constructor copies valid
tags and sorts them by raw UTF-8 bytes; it rejects rather than trims, normalizes,
or repairs invalid tags. Skill evidence accepts no caller tags: it projects
`domain:<canonical-domain>` and `keyword:<canonical-keyword>` exclusively from
the sealed validated manifest.

#### 4.2.2 Historical Skill v1 Unicode compatibility

Historical `SkillVersion` v1 applies NFC before full case folding, but does not
apply NFC again after the fold. A small set of valid Unicode scalars therefore
has a historically valid, non-NFC folded domain or keyword. Changing that
algorithm now would silently reinterpret existing `ContentHash` values and is
forbidden.

The compatibility contract is instead:

- historical `SkillVersion.Validate` and `DecodeCandidateChange` retain their
  audit semantics;
- new `NewCandidate` and new `CandidateChangeJSON` proposal authoring reject a
  folded domain/keyword that is not NFC;
- `SealImmutableArtifactViewV1` explicitly fails closed on such a historical
  artifact, so every successfully sealed view can form exact source tags;
- no caller or Restore path may normalize, repair, rehash, or promote the
  rejected historical value;
- supporting those values in active sealed content would require an explicit
  versioned Skill/tag projection and migration, not a v1 behavior change.

This is an audit-only compatibility carve-out, not evidence that a historical
artifact is corrupt.

#### 4.2.3 Membership/generation completion lock

The exact V1 membership, generation, mapping, and membership-ref fields and
hash domains remain those locked by
`2026-07-22-governance-rule-wire-v1-lock.md`. The Stage 3 implementation adds
complete typed-parent closure without reinterpreting those wires:

- the implementation package is `internal/skillcontentcatalog` and does not
  import governance, RuntimeCatalog, modulebinding, Store, network, or process
  packages;
- `SkillPublicInternalRefMappingV1` and
  `SkillContentCatalogMembershipV1` are derived only from an exact
  `assembly.SkillRef` and a complete validated `SkillPackageEvidenceV1`;
  callers cannot supply mapping, ordinal, package/tag/payload hashes, or
  neutral catalog identity;
- generation construction accepts unordered complete membership inputs, sorts
  the RFC 8785 membership base bodies after deleting only
  `membership_ordinal` and `membership_hash`, then assigns zero-based
  contiguous ordinals;
- a duplicate full base body, membership hash, `PackageDigest`, exact
  `(SkillID, Revision)`, or exact full `assembly.SkillRef`
  `{ID, Version, Digest}` fails closed;
- equal `ContentPayloadHash` values are explicitly allowed when distinct
  semantic packages share the same exact `SKILL.md` body; shared content is a
  design feature, not a duplicate package;
- distinct revisions of the same Skill ID may coexist. Lifecycle status is not
  in this immutable generation and no member is ACTIVE, current, selected, or
  loadable merely because it is present;
- an empty portable generation is valid history and produces no membership
  ref or resolution view. Whether an empty generation can become a current
  runtime anchor belongs to the later lifecycle/current-activation policy;
- generation Restore receives the complete ordered membership documents and
  revalidates their complete package-evidence parents. A hash array is
  insufficient;
- a portable membership ref remains non-authorizing even though its bytes and
  domain match the historical structural V1 document;
- the process-local sealed membership view has no public constructor, Restore,
  or canonical wire. Exact generation lookup is its only source, and it proves
  exact membership only—not review, activation, currentness, selection,
  loading, RAG use, Tool/MCP permission, or external effects.

Generation lineage, current-generation CAS, review/activation history,
revocation and ACTIVE/CANARY ambiguity are deliberately deferred to the next
lifecycle slice. They MUST NOT be inferred from immutable membership content.

### 4.3 Knowledge/RAG content parents

Stage 3 MUST provide the following shared RAG parent chain:

```text
ContentPayloadV1
    -> KnowledgeContentVersionV1
    -> KnowledgeCollectionItemVersionRefV1
    -> KnowledgeCollectionVersionAuthorityV1
    -> KnowledgeCollectionBindingAuthorityV1
    -> sealed KnowledgeCollectionResolutionView
```

The chain MUST bind TenantID, collection identity, exact collection version and
hash, ordered exact item/version references, content payload hashes, source
tags, source provenance, visibility/data scope, stable lifecycle target
identity, and retention-policy identity. Mutable activation, retirement,
retention enforcement, and revocation state are separate lifecycle parents
observed by the resolver and by each actual-use gate; they are not fields that
can mutate an immutable collection version.

A collection version is shared content authority. AgentVersion and
WorkspaceDefinition hold only explicit bindings to it. Copying collection text
into an Agent record is not an implementation of this contract.

The existing coarse `internal/knowledge` seed/search/cache path may remain a
compatibility implementation, but it MUST NOT be treated as this sealed
authority until it emits and verifies the complete parent chain.

### 4.4 Memory boundary

Memory remains an independent attachment family even when its immutable bodies
reuse `ContentPayloadV1`.

Any future `MemoryContentVersion` and `MemoryBindingAuthority` MUST bind an
explicit owner and scope:

- Agent-private;
- Workspace-scoped;
- Tenant-scoped.

Memory MUST NOT be smuggled through a Knowledge collection, Skill package,
Role, or Persona attachment. Stable Agent scope may preserve growth memory
across AgentVersion upgrades only through an explicit new-version selection
that rechecks current ACL, scope, and revocation state.

Stage 3 may build the common content substrate and binding interfaces needed by
Memory, but enabling non-empty Memory assembly requires its own sealed
resolution and use parents. Skill/RAG completion alone does not authorize
Memory.

### 4.5 Requested attachment and use parents

Selection and use require three distinct records:

1. a requested-binding authority proving that a specific owner requested or
   permitted an exact attachment;
2. a resolved admission authority, created before content is read or cost is
   incurred, proving that the exact member, Run, policy, content version,
   budget reservation, live revocation observation, and proposed operation are
   admissible;
3. a post-effect actual-use receipt/provenance record binding the admission to
   the exact observed results, usage, terminal state, and reconciliation
   evidence.

The third record is evidence, not authority. It cannot authorize the read that
produced it, replay an effect, or repair a missing pre-effect admission.

The following must not be conflated:

```text
available in catalog
!= active
!= requested
!= selected for member
!= permitted by policy
!= loaded into context
!= executed
!= post-effect evidence recorded
```

For Skill, the resolved parent freezes the exact catalog membership and package
evidence. For RAG, the resolved parent freezes the collection binding described
in section 5 and each actual retrieval freezes its exact evidence.

## 5. RAG two-layer freeze

RAG freshness and Run reproducibility are reconciled by two explicit layers.
No layer accepts a bare `latest` reference.

### 5.1 Layer 1: Run/member binding freeze

The Run/member assembly freezes:

- Tenant, Workspace, Agent, Profile, Task, and member identities;
- the allowed collection identities and data-scope ceilings;
- the retrieval mode;
- the retrieval/ranking/projection policy hashes and algorithm identities;
- context, token, latency, and cost budgets;
- cache policy and maximum acceptable staleness;
- the revocation watermark and required/optional failure policy.

Retrieval mode is one of:

- `SNAPSHOT`: Layer 1 also freezes one exact collection version/hash;
- `FRESH_WITHIN_FROZEN_POLICY`: Layer 1 freezes permission to perform a
  linearized current-activation read for the named collection under the frozen
  policy. It does not silently rewrite the Run parent.

`FRESH_WITHIN_FROZEN_POLICY` is explicit configuration, not a default. It
supports periodically updated professional knowledge while keeping the Agent
lightweight.

### 5.2 Layer 2: per-retrieval admission and exact-use receipt

Before any retrieval read or cost, `RetrievalAdmissionV1` freezes:

- the Layer 1 binding hash;
- a stable semantic RetrievalPosition and a distinct dynamic Attempt identity;
- the exact current-activation evidence observed, if fresh mode was selected;
- exact collection version/hash;
- a privacy-governed query payload reference, digest, byte length, and
  deterministic normalization hash/version; query text is never inlined in the
  portable admission;
- retriever, embedding/index, ranking, and projection implementation versions;
- the applicable policy/grant, data-scope proof, live revocation observation,
  budget reservation, and result/byte/token/latency/cost ceilings.

The portable admission is parent evidence and is never directly consumable.
After rechecking live revocation and atomically reserving the bound budget, a
trusted runtime boundary may derive one non-exportable, process-local,
single-consumer `RetrievalReadGrant`. Restore cannot recreate that grant. One
attempt consumes it at most once; after consumption the only legal durable
outcomes are an exact Terminal receipt or `UNKNOWN` reconciliation state, and
neither permits semantic replay.

Only that one-shot grant may authorize the bounded read. After the retrieval
attempt, `RetrievalUseReceiptV1` binds the admission and Attempt to:

- ordered result item/version/content payload references and scores;
- source and projected payload references, digests, and byte lengths; body
  bytes remain only in managed tenant content payload/artifact storage and are
  never copied into retrieval evidence;
- cache hit/miss identity and the exact source evidence behind a cache hit;
- actual token, latency, and cost observations;
- context-use provenance, terminal status, and any required `UNKNOWN`
  reconciliation evidence.

The receipt never authorizes a read or semantic replay. A replay consumes
recorded Layer 2 receipt evidence and MUST NOT repeat a fresh lookup and call
the result the same retrieval.

An old Run in `SNAPSHOT` mode cannot see a new collection version. An old Run in
explicit fresh mode may observe a later activated version, but each observation
is a new exact Admission/grant/Receipt evidence pair under the unchanged Layer
1 ceiling.

This is the only allowed interpretation of “the next retrieval can use the
actual current revision.”

## 6. Owner order, merge, and failure policy

### 6.1 Canonical owner order

The canonical owner order is:

```text
TENANT = 0
WORKSPACE = 1
AGENT_VERSION = 2
ASSEMBLY_PROFILE = 3
TASK = 4
```

This order controls deterministic origin serialization and evaluation. It is
not an unrestricted “last writer wins” override rule.

All applicable authority ceilings intersect, and any applicable deny wins.
Task, Profile, Agent, and Workspace content requests can narrow authority but
cannot expand Tenant or platform limits.

### 6.2 Deterministic merge

Resolution MUST:

1. obtain sealed current-at-construction owner/contribution views;
2. validate Tenant and owner ancestry;
3. collect all applicable exact origins;
4. merge byte-identical attachment identities into one requested-binding
   authority with the complete ordered origin set;
5. apply deny-first governance and data-scope ceilings;
6. produce a deterministic ordered exact selection.

An exact duplicate is deduplicated with all origins retained. Two different
versions for the same logical exclusive slot are a conflict unless an explicit
version-selection/replacement policy is itself frozen in the assembly input.
Owner order alone MUST NOT silently choose a winner.

### 6.3 `REQUIRED` and `OPTIONAL`

Each selected attachment has an exact failure policy:

- `REQUIRED`: missing, revoked, corrupt, unauthorized, over-budget, or
  unresolvable content fails member assembly/use closed;
- `OPTIONAL`: the member may continue without that exact attachment, but the
  unavailability and reason are recorded.

If multiple valid origins request the same exact attachment and any origin is
`REQUIRED`, the merged request is `REQUIRED`.

A missing required attachment MUST NOT degrade into pure chat. An optional
failure MUST NOT cause the resolver to select a different version, Skill,
collection, Tool, Module, or MCP provider unless that fallback set and order
were explicitly frozen.

## 7. Activation, revocation, and portable restore

Immutable history and local activation are separate.

- package, payload, membership, generation, collection-version, and binding
  documents are insert-once;
- a local current activation uses authenticated control-plane publication and
  linearizable CAS;
- activation records bind deployment trust domain, Tenant, backend owner epoch,
  activation ordinal, actor, exact target, and previous activation;
- concurrent publication has one winner; retry may only read back an
  byte-identical committed result;
- normal retirement prevents new selection while frozen historical Runs remain
  recoverable;
- emergency revocation prevents new use and follows the separately locked
  termination/reconciliation policy for affected Runs;
- rejected source identities remain in permanent negative history and are not
  reconsidered automatically.

`Restore` validates portable immutable history only. It MUST NOT:

- create or advance a current pointer;
- mark a candidate active;
- grant permission;
- bypass review;
- change a backend owner epoch;
- make a historical generation selectable by a new Run.

Importing history and activating an exact imported authority are two separate,
audited operations.

## 8. Stage 3 implementation order

### 8.1 Stage 3-A: immutable content and Skill catalog parents

Implement in this order:

1. neutral `SkillCatalogVersionRefV1`;
2. removal of the `skillcatalog -> governance` package dependency by moving the
   proposal adapter to an upper bridge;
3. `ContentPayloadV1` metadata/body integrity boundary;
4. `SkillPackageEvidenceV1`;
5. Skill membership, catalog generation, membership ref, and sealed resolution
   view;
6. lifecycle history, review result, current activation CAS, and revocation
   checks;
7. strict restore, canonical/golden, shuffle, limit, substitution, concurrency,
   and corruption tests.

Stage 3-A does not change `MemberSnapshotV2`, production assembly, or model
context loading.

Implementation checkpoint (2026-07-28):

- `sdk/catalogref` and the single lower typed conversion in
  `internal/skillrefbridge` are complete; the governance-facing bridge
  delegates to that primitive;
- `internal/contentparents` implements the exact `ContentPayloadV1` and
  `SourceTagSetV1` wires;
- `internal/skillcatalog.ImmutableArtifactViewV1` freezes exact manifest and
  semantic-package projections without lifecycle state;
- `internal/skillpackage.SkillPackageEvidenceV1` closes the full immutable
  artifact and exact `SKILL.md` payload, derives tags internally, and requires
  both complete parents on Restore;
- `internal/skillcontentcatalog` implements the canonical-compatible public /
  internal ref mapping, immutable membership, deterministically ordered
  generation, portable membership ref, and process-local exact-membership
  view. Restore requires complete parents, historical `modulebinding` V1
  mapping/ref canonical bytes and hashes remain equal for canonical inputs,
  and every view authorization method returns false;
- none of these packages is referenced by production Store, Runtime,
  MemberSnapshot, context-loading, or pure-chat paths.

Windows shuffled tests and `go vet` pass for all eight related packages. WSL
Debian `-race` passes for the same package set. The Windows repository suite
excluding the separately recorded `internal/store/sqlite` long-running
performance debt passes; no assertion failure is being hidden as a full-suite
success.

The lifecycle/current-resolution slice now builds a process-local exact index
after one complete generation validation and reuses it for selected-subset
lookups. The sealed resolution map contains only memberships selected by the
exact current activation; an inventory membership that exists in the
generation but is not selected returns `ErrNotFound`. Queries do not revalidate
or rescan the complete generation, and there is no nearest-version fallback.
The index does not replace portable validation, infer currentness, or grant
selection, loading, Tool/MCP, RAG, or external-effect authority.

Current-resolution checkpoint (2026-07-29):

- one independent repository method returns activation, package-lifecycle, and
  revocation material from one opaque backend-local read revision;
- the coordinator replays the complete activation and package parents, the
  complete local revocation page chain, and proves that the activation receipt
  tail is a real ancestor of that chain;
- the portable revocation chain remains global to trust-domain/Tenant while
  local current is owner-epoch scoped. A newer live owner may therefore have a
  portable tail ahead of the old local tail, but that path returns only
  `ErrOwnerFenceLost` and cannot produce a resolution view;
- error priority is structural corruption, owner-fence loss, permanent
  revocation, then valid lifecycle drift;
- closure, resolution, and availability views have private fields, no public
  scalar constructor or Restore path, and all five authorization-shaped
  methods return false;
- an empty activation target is valid, but it still traverses the optional
  Skill closure and is not the `pure_chat` zero-resolution path;
- Windows shuffled tests and `go vet` pass; Debian WSL `-race` passes. This
  checkpoint adds no Store, SQLite migration, Runtime, MemberSnapshot, context
  loading, or pure-chat wiring.

The remaining non-blocking performance debt is repeated deep validation while
sealing and projecting a closure. Exact lookup itself is already constant-time
over the private selected maps and does not repeat generation validation.

### 8.2 Stage 3-B: sealed attachment resolution

Implement:

1. sealed owner/contribution views for Workspace, AgentVersion,
   AssemblyProfile, and Task;
2. requested-binding authorities and complete origin sets;
3. deterministic merge, deny-first policy, required/optional semantics, and
   exact Skill selection;
4. sealed Skill/content resolver output and RuntimeCatalog Skill-anchor
   closure;
5. a new authority-aware, legacy-free member snapshot constructor and Restore
   API that accept the complete sealed resolver views; it MUST use a new wire
   and hash domain rather than reinterpret `MemberSnapshotV2`.

The current `ErrAuthorityUnavailable` guard remains until the Stage 3-B
acceptance gate passes and remains permanent for the V2 constructor/Restore
path. Stage 3-B may open non-empty Skill/content only through the new
authority-aware snapshot family; deleting the V2 guard is forbidden.

Implementation checkpoint — immutable owner closure prerequisite (2026-07-29):

- `internal/attachmentowner` now verifies complete immutable parent closure for
  WorkspaceDefinition, AgentVersion, AssemblyProfile, and TaskRequirements;
- Workspace and Agent closures include their exact stable scope authorities and
  exact version/definition associations. Profile remains an exact Tenant-bound
  immutable owner, and Task explicitly binds
  `TenantID + TaskID + TaskScopeHash + TaskRequirementsHash`;
- these are deliberately named `Verify*Closure` and `Verified*Identity`.
  They are process-local structural witnesses only: they do not authenticate a
  producer, prove current contribution state or selected-member ancestry, or
  authorize attachment request, selection, loading, retrieval, execution, or
  an external effect;
- the raw enum reserves zero for `INVALID`. A separate `CanonicalRank()` locks
  the normative order `TENANT=0, WORKSPACE=1, AGENT_VERSION=2,
  ASSEMBLY_PROFILE=3, TASK=4`; raw enum values must never enter a portable
  preimage;
- the package has no Restore or scalar/hash constructor, no attachment
  enumeration or repository. Its repository-wide dependency contract now
  permits exactly two reviewed structural consumers,
  `internal/attachmentassembly` and `internal/attachmentownerref`; every other
  production consumer remains forbidden;
- shuffled Windows tests and `go vet` pass, and Debian WSL `-race` passes.
  Tests cover parent substitution, alias isolation, zero-value failure, fixed
  canonical rank, and reuse of one AgentVersion identity across two Workspaces.

This checkpoint alone is not completion of item 1 above. The later
activation/current checkpoint now supplies the one-read fixed-slot current
prerequisite. The remaining item-1 work is the producer-authenticated join that
combines these immutable closures, exact Task/member selection, and current
contribution generations. Neither this checkpoint nor historical
`modulebinding` V1 structural documents may be promoted directly into
requested-binding or selection authority.

Implementation checkpoint — SelectionV2 member/owner compatibility join
(2026-07-29):

- `TrustedGovernanceRuntimeResolutionViewV1.VerifyFullInput` now replays the
  complete typed governance input and compares the exact canonical resolution.
  An upper coordinator no longer needs to parse the private resolution wire or
  trust scalar owner references;
- `internal/attachmentassembly.VerifySelectionV2MemberOwnerSetV1` replays the
  complete `TaskAssemblySelectionV2Input`, derives MemberID from the selected
  ordinal, and proves that governance and SelectionV2 identify the same
  Workspace, AgentVersion, AssemblyProfile, TaskScope/TaskRequirements, Run,
  and member;
- the process-local result keeps the four immutable owner identities in fixed
  `WORKSPACE, AGENT_VERSION, ASSEMBLY_PROFILE, TASK` order and keeps their
  common Tenant scope anchor. It has private fields, no portable wire, no
  Restore/scalar constructor, no repository, and no production consumer. All
  five authorization-shaped methods return false;
- semantic integration tests cover the valid join, fixed owner rank, detached
  projections, untrusted governance, full-input and Selection parent
  substitution, member bounds, zero values, and non-authorization. AST and
  repository-wide tests keep this package outside Store, Runtime,
  `modulebinding`, Skill lifecycle, and production readers. Windows shuffled
  tests and `go vet` pass; Debian WSL `-race` passes for the three domain
  packages and the targeted SQLite integration fixture.

This checkpoint remains a structural compatibility witness, not the
authenticated/current closure required by item 1. `TaskAssemblySelectionV2`
remains permanently attachment-free and legacy-bound. Its guard MUST NOT be
relaxed, and this witness MUST NOT be described or wired as the future
legacy-free authority-aware selection family. In particular:

- Selection, Activation, legacy binding, TaskRequirements seal, and authoring
  producers are not authenticated by this view;
- TaskRequirements are not a parent of the trusted governance resolution, so
  the next aggregate boundary must authenticate their exact producer/root
  rather than treating the resulting Task owner hash as sufficient;
- the implemented contribution-current boundary reads fixed owner slots with
  explicit `NONE|ONE` presence from one backend owner epoch and one opaque
  repository read revision. Four independent current lookups, a portable
  restored generation, or this structural witness cannot establish
  currentness.

Implementation checkpoint — portable Skill contribution prerequisite
(2026-07-29):

- `sdk/assembly` now exposes one standalone `CanonicalSkillAttachment` /
  `SkillAttachment.Validate` boundary that applies the same exact Skill ID,
  version, digest, config, ConfigHash, and FailurePolicy rules as the existing
  authoring documents;
- `internal/attachmentownerref` projects one complete verified immutable owner
  identity into a strict portable reference. New and Restore both require the
  complete process-local verified identity; the document has no scalar
  constructor, reverse Identity getter, current pointer, or authority;
- `internal/skillbindingcontribution` is deliberately Skill-specific. It does
  not re-create a generic Module/Role/Persona/Memory/RAG/MCP union and does not
  fabricate a catalog revision. Each contribution contains the complete owner
  reference and one exact standalone Skill attachment;
- generation construction is available only through typed initial and
  successor entry points. Initial is generation one with a fixed
  domain-separated `NO_PARENT`; successor generation and parent identity are
  derived from the complete direct predecessor. Restore requires the exact
  owner reference, every complete contribution in canonical ordinal order, and
  the complete direct predecessor for successors;
- complete contribution canonical bytes determine ordering. Exact duplicates
  and a second contribution for the same `(SkillID, Version)` fail closed,
  including changed digest, config, or FailurePolicy. Distinct versions of one
  Skill ID may coexist only as portable facts; a later merge must report an
  explicit exclusive-slot conflict and cannot choose a winner by owner order;
- nil and empty input normalize to one observable non-nil empty generation.
  That value is an immutable portable empty set, not a current activation,
  clear command, or selected result. Complete parent material is bounded by
  4096 contributions and 16 MiB;
- a generation ref embeds the full owner ref and can only be derived from, or
  restored with, the complete exact generation. Its process-local validation
  uses a private flat projection sealed after one complete generation
  validation; a detached generation/hash tuple is insufficient;
- owner ref, contribution, generation, and generation ref all return false for
  all five authority-shaped methods. Portable Restore cannot publish or
  advance backend-local current.

The canonical owner rank remains the five-level order
`TENANT=0 .. TASK=4`. The current aggregate nevertheless has exactly four
contribution slots: Workspace, AgentVersion, AssemblyProfile, and Task. Tenant
is their common authority anchor/ceiling, not a fifth contribution generation.

The reviewed Stage 3-B production-consumer contracts now permit only:

```text
attachmentowner -> attachmentassembly, attachmentownerref, taskmemberauth
attachmentassembly -> skillbindingcurrent
attachmentownerref -> skillbindingcontribution, skillbindingcurrent,
                      skillbindingresolver, membersnapshot
skillbindingcontribution -> skillbindingactivation, skillbindingcurrent
skillbindingactivation -> skillbindingcurrent
taskmemberauth -> skillbindingcurrent, skillbindingresolver
skillbindingcurrent -> skillbindingresolver
skillbindingresolver -> membersnapshot
membersnapshot -> no production consumer
```

The portable contribution checkpoint itself added no repository. The later
current checkpoint adds only one repository interface and process-local
sealer—no concrete persistence, SQLite migration, Runtime, MemberSnapshot,
context-loading, or `pure_chat` reader. Literal canonical/hash goldens, strict JSON,
parent substitution, rehashed tamper, deterministic ordering, all duplicate
axes, empty/successor lineage, deep SDK-valid config, bounded closure, alias,
concurrent read, zero-value, exact constructor signature, method-set, and
repository-wide dependency tests pass. Independent final review reports
P0/P1/P2 all zero. Windows shuffled tests/vet and Debian WSL race tests pass.

Implementation checkpoint — Skill contribution activation and atomic current
owner set (2026-07-29):

- `internal/skillbindingactivation` now provides Skill-specific portable
  `ACTIVATE|DEACTIVATE` history. Genesis uses a domain-separated `NO_PARENT`;
  typed successor constructors derive the adjacent ordinal, predecessor,
  immutable attachment owner, and exact ACTIVE backend-owner transition.
  `DEACTIVATE` retains the exact prior generation ref as its audit subject but
  produces no resulting generation. Consecutive deactivation and same-ref
  activation no-ops fail closed;
- Restore requires the exact generation ref, ACTIVE transition, and complete
  direct predecessor for successors. A private flat proof bounds validation
  depth. Actor/reason/time and the structural ACTIVE transition do not prove
  actor authentication, a durable owner fence, repository currentness, or any
  use authority;
- `internal/skillbindingcurrent` exposes one repository method and one loader.
  Its typed read key can only be derived from a complete
  `VerifiedSelectionV2MemberOwnerSetV1`; one successful repository call must
  return one nonzero opaque read revision, one live ACTIVE backend owner, and
  four named Workspace, AgentVersion, AssemblyProfile, and Task slots;
- each slot is exactly `NONE` or `ONE`. `NONE` retains its exact owner
  class/hash and carries no activation or generation material. `ONE` carries
  one complete ACTIVATE tail, generation ref, and generation. An explicitly
  activated empty generation remains `ONE` and is never normalized to
  `NONE`;
- a newer valid backend owner reports owner-fence loss. Every other successful
  but non-closing repository result is corrupt, while repository operational
  errors are propagated unchanged. Portable generations, activation history,
  imported history, or four independent reads cannot establish currentness;
- the sealed result is process-local, has no wire, Restore, scalar constructor,
  or production consumer, and all authorization-shaped methods return false.
  It remains based on the legacy zero-attachment SelectionV2 structural
  witness and does not authenticate the Selection or TaskRequirements
  producer;
- no SQLite current implementation or migration was added. A SQLite-package
  integration test only reuses the existing fixture to construct a real
  verified owner set, then exercises the loader with a fake atomic repository;
- after eliminating repeated owner-DAG and ONE-closure validation, independent
  review reports P0/P1/P2 all zero. Windows shuffle/vet and Debian WSL race
  tests pass.

Implementation checkpoint — public Task member producer authentication and
typed current join (2026-07-29):

- `internal/taskmemberauth` adds a new public-Agent-only
  `TaskMemberSelectionV1`. It replays the complete `TaskRequirementsSealInput`
  and every trusted governance full input, orders members by raw UTF-8
  MemberID bytes, and derives ordinals internally. It has no SelectionV2,
  ReceiptV2, TeamSnapshot, legacy Agent row, or `modulebinding` parent;
- the selection freezes exact DTD, Tenant, Workspace definition and scope
  authority/association, Profile, Task, Run-scope authority, Activation,
  RuntimeCatalog, TaskRequirements seal, Task authority, selection compiler,
  public AgentVersion refs, and governance resolution hashes. All members in
  one selection must share the same exact Workspace authority and association;
- its one-method producer-authentication repository is a TCB port. A successful
  result must authenticate the exact Task-authority and selection-compiler
  producers in one serializable authentication snapshot. Operational errors
  retain identity; malformed successful records become `ErrCorrupt`. Separate
  trust-anchor and authentication-evidence hashes are retained for both
  producers, and no forgeable `Authenticated` boolean exists;
- the producer-authenticated owner set is process-local, retains four complete
  immutable owner closures, and has no wire, Restore, or scalar constructor.
  It remains non-authorizing;
- `internal/skillbindingcurrent` preserves the legacy V2 loader and repository
  unchanged. Its new public-Task-member loader accepts only the authenticated
  owner set and uses a nominally distinct one-method combined repository. One
  serializable read returns both exact current and four named publication-
  authentication slots; the new aggregate and slots retain both the Task
  member producer witness and complete publication-authentication evidence;
- `internal/skillbindingresolver` adds a typed final join that accepts only the
  two new complete concrete witnesses. It closes the entire member scope, four
  exact owner refs, DTD/Tenant, live ACTIVE owner, exact owner epoch, and the
  independent producer-authentication and current-read revisions. It exposes
  only `Validate` plus five always-false authority-shaped methods. There is no
  scope/slot getter, unwrap, conversion, wire, hash, Restore, repository, or
  interface-based entry point;
- a real chain test reaches the join with one non-empty Workspace `ONE`
  containing one exact Skill contribution, its initial generation/ref, and an
  ACTIVE activation; the other three slots are `NONE`. Canonical/hash golden,
  Workspace-authority substitution, typed-nil, out-of-range zero-read, record
  mutation, one-read, owner-fence, alias, concurrency, zero-value, API/AST,
  dependency, and legacy nominal-isolation tests also pass;
- every value in this path remains non-authorizing. There is no concrete
  producer-auth adapter, production SQLite current repository, migration,
  Runtime, MemberSnapshot, context loading, or `pure_chat` read. The
  Activation/ProfileLink parents inside `TaskRequirementsSealInput` remain
  transitional debt, so this checkpoint is not Final V3 convergence.

The final independent review reports no remaining P0/P1/P2. Windows shuffled
tests, vet, Debian WSL race tests, and formatting checks pass. Key frozen
SHA-256 values are:

```text
taskmemberauth/selection_v1.go =
  E93AE34E0A315D5E5CC693C23CE6439F04D0DC5F5356B0A79B221B7C9F5CCDEE
taskmemberauth/repository_v1.go =
  5EF3C2C3045D3492F293BF505BD7D7F0515372D592B7F399AB3C63F645190935
taskmemberauth/owner_set_v1.go =
  6B8A67662F33E9728257353BCEF4063A9BDDD9110523E27EC4AEC5CD54C30E8B
skillbindingcurrent/repository_v1.go =
  81845FEDCE3E8571E19F983805E69DA63E4F9EAA31D516A4836ADA0B5A12BC1F
skillbindingcurrent/producer_authenticated_v1.go =
  5893BA3D2A6411561C9A36CA352E3ED46480FE83BC5C2DE4B8AFF348E1EB6ECD
skillbindingcurrent/publication_authentication_v1.go =
  311EB17C42BEE45008AF34E2CFEAA35B661A62EAF7851FB566BA532C6150D18E
skillbindingresolver/join_v1.go =
  55A68FED8802DC2F0BC49275FFE262E3F8E4C2486FC1EEC4E733E58685833959
skillbindingresolver/real_chain_v1_test.go =
  38B5EC8FCA6A0B7E2C0EAFD042CA779D59F64D1AE29D7D6318DD5F02DFD600FD
```

Publication-authentication correction before requested origins (2026-07-29):

- a portable contribution activation binds an actor label but does not
  authenticate that actor or its right to publish for an owner. Therefore a
  structurally current `ONE` is no longer sufficient for the new public-Task-
  member path;
- the legacy repository/loader remains unchanged. The new producer path uses
  `ProducerAuthenticatedTaskMemberCurrentSkillBindingContributionOwnerSetRepositoryV1`,
  whose sole combined read must obtain currentness and all four named
  publication-authentication records from one serializable snapshot;
- `NONE` retains only presence, owner class, and exact owner-ref hash. Every
  other authentication field must be zero. `ONE` must match the exact
  activation hash, generation-ref hash, generation hash, and producer
  principal, and must retain separate producer trust/authentication evidence,
  publication authority/trust anchor, and exact command authorization
  evidence;
- repository success is the TCB assertion that the exact producer was
  authenticated and entitled to issue that exact control-plane publication
  command for that exact owner. There is no `Authenticated` boolean, and the
  actor label, owner hash, or a historically authenticated activation cannot
  make the result valid by itself;
- the producer current aggregate and each named slot privately retain and
  replay this evidence. The typed final join therefore closes only over a
  current snapshot whose non-empty sidecar publications were authenticated in
  the same repository read;
- valid newer-owner fence loss has explicit priority over malformed
  publication-authentication material; every other malformed successful
  combined result is corrupt. Repository operational errors preserve identity;
- independent review initially found three P2 test-contract gaps. They are
  closed by a mixed fence/auth priority test, an AST rule preventing repository
  escape beyond the typed-nil check and sole direct combined read, two valid
  `ONE` owner-swap negatives, non-empty Config alias mutation, and concurrent
  sealed aggregate validation/getters. Final review reports P0/P1/P2 all zero.
  Windows shuffle x3, vet, formatting, and Debian WSL race tests pass for the
  two affected packages.

This correction replaces the new path's former single current read with one
combined read; it does not add a second round trip. There is still no concrete
production adapter, SQLite migration, Runtime, MemberSnapshot, context, or
`pure_chat` read. Independent contribution generations remain external modules
and are not written back into Workspace, Agent, Profile, or Task v1 wires. The
same AgentVersion remains reusable across Workspaces. Role, Persona, Memory,
RAG, MCP, context compression/drop rules, UNKNOWN replay rules, SQLite default,
and cold-family default-off behavior are unchanged.

Complete requested-binding origins checkpoint (2026-07-29):

- `CollectCompleteRequestedSkillBindingOriginsV1` accepts only the sealed
  `ProducerAuthenticatedTaskMemberCurrentSkillBindingJoinV1`. It replays the
  complete producer-authenticated Task member witness and the independent
  publication-authenticated four-owner current witness, then closes their exact
  scope, owner epoch, live owner, and four owner references;
- the opaque origin set privately retains that complete final join and a
  non-nil ordered origin slice. It has no origin getter, wire, hash, Restore,
  repository, scalar constructor, or runtime authority. All five
  authority-shaped methods remain false;
- the fixed nine logical groups are Workspace bindings, Workspace policy
  defaults, Workspace current contributions, Agent bindings, Agent current
  contributions, Profile defaults, Profile current contributions, Task exact
  requests, and Task current contributions. Owner, slot, and original ordinal
  order are retained exactly. Duplicate requests are not merged and historical
  generations are not consulted;
- every direct origin retains the exact owner-ref canonical bytes/hash, complete
  Skill ref, Config bytes/hash, and failure policy. Task requests retain their
  exact refs. Current origins additionally retain the exact activation,
  generation-ref, generation number/hash, and contribution hash. This
  checkpoint performs no allow/deny evaluation, version selection, catalog
  lookup, content load, or fallback;
- `NONE` and empty `ONE` both yield a non-nil zero-length origin group, but are
  not collapsed: the complete retained current parent still preserves their
  different presence, activation, generation, and publication-authentication
  closures. A tampered nil retained origin slice fails validation;
- owner provenance is validated once per owner and cached as a private
  canonical/hash projection. The current hot loop does not replay owner refs or
  complete generations per contribution. Attachment-owner assembly documents
  cross one full canonical/decode boundary per fused material projection, and
  the Task member read-key path reuses one validated selection projection.
  “One-pass” here means one replay of each complete document or large
  generation closure at that validation boundary; it does not mean that every
  lightweight parent getter in all lower packages performs no validation;
- the dedicated Profile test seam was removed and production caller sets are
  AST-locked. The generic private helper still gives component coverage for a
  non-empty Profile default, but the current legacy compatibility Profile is
  permanently empty. Therefore all nine groups are logically implemented, while
  only the other eight are presently reachable through the complete real-chain
  fixture. This limitation must not be reported as full production Profile
  reachability;
- nested attachment-owner relationship errors are translated to the
  taskmemberauth relationship sentinel; other nested material failures become
  the taskmemberauth document sentinel. Error identity is therefore preserved
  across the package boundary without exposing dependency-layer sentinels.

The final independent review reports P0/P1/P2 all zero. On the final file
hashes, Windows five-package shuffled tests pass three times
(`attachmentowner` 1.095s, `taskmemberauth` 1.684s,
`skillbindingcurrent` 10.414s, `skillbindingresolver` 11.279s, and
`compositeprewire` 89.897s); five-package vet and formatting checks pass. The
Debian WSL five-package race command also passes (`attachmentowner` 4.021s,
`skillbindingcurrent` 14.817s, and `compositeprewire` 161.805s; Go reused
exact-hash race results for `taskmemberauth` and `skillbindingresolver`, whose
separate final-hash race runs passed in 5.532s and 19.232s). This is not a claim
that the repository-wide SQLite heavy-evidence suite passes: its existing
long-running timeout remains separate performance debt, with no assertion
failure observed.

Key SHA-256 values at the time this origins checkpoint was accepted are shown
below. The later deterministic-merge checkpoint records current hashes for
files refactored to share one fused projection.

```text
attachmentowner/validated_material_v1.go =
  ABDFE7776C94A142307C5B36624C869CF7F67227969638A25D86E7B291337B5D
attachmentowner/workspace_owner_view_v1.go =
  E2F7F72122D0B69CB46948C9AB8216229749A5DA02FEDDBDFBD7FBCA4134A4CE
attachmentowner/task_owner_view_v1.go =
  8A011B5736115A1E66507BD4D6161C058A9744F8D722822B9B2443B1798C4D8C
taskmemberauth/selection_v1.go =
  CA117652F84CE0EF00801588F2184D8673C1DAF34E30D13BC83CA56745FBE81B
taskmemberauth/repository_v1.go =
  EA546A64EA23974184F4163B0603BEC92AA774F138B2BD2C8744A64F1976F69B
taskmemberauth/owner_set_v1.go =
  540AD47941CAC4E2CB377D7DBC93DF852FB4E3C183DAB6ACBAD5DB7BCC908675
skillbindingcurrent/producer_authenticated_v1.go =
  AC9BAF2EB0D456467A9F02B0FC2429ACCAB2C79B7784024C8B4780279032C172
skillbindingcurrent/publication_authentication_v1.go =
  7E6695C8AA3C26C2D946FD5D6FDBB14F30BE8285F952E9E53858D9A0705EF144
skillbindingcurrent/repository_v1.go =
  A8F88F7411E26F337C51143ED05DC94F21A406734BA6FA8F73A3438CE2DFC85D
skillbindingresolver/join_v1.go =
  9462797B86461477FBC6433B23C6D86A258D6DBEB57D5E2D63076B2D9A20A951
skillbindingresolver/origins_v1.go =
  E9C77B947103E667BEC5AF61033DC111F17FA5C70EA1A42A564AE2A344800804
skillbindingresolver/api_boundary_v1_test.go =
  F5450B2EC53AA7CC0DFF9131C3FACD4A1753DCF19B5AB24C92BF4FEE43C595C0
```

This checkpoint adds no migration, production adapter, SQLite current,
Runtime, trusted MemberSnapshot, context read, or `pure_chat` repository read.
It does not change Role, Persona, Memory, RAG, MCP, the 85% compression
threshold, the 100% minimal-oldest-prefix drop rule, UNKNOWN no-semantic-replay,
SQLite default, or cold-family default-off behavior. Skill remains an optional
external module and the same AgentVersion remains reusable across Workspaces.

Deterministic requested-binding merge checkpoint (2026-07-29):

- `MergeCompleteRequestedSkillBindingOriginsV1` accepts only the opaque
  complete origin set and privately retains that complete parent. The merged
  object has no getter, wire, hash, Restore path, repository, or scalar
  constructor, and all five authority-shaped methods remain false;
- the V1 logical exclusive slot is canonical SkillID. A configured attachment
  identity is exact `SkillRef + canonical Config bytes + ConfigHash`.
  FailurePolicy is excluded from identity so byte-identical duplicates retain
  every origin while REQUIRED dominates OPTIONAL. Different version, digest,
  or Config in one slot is an explicit conflict. Canonical owner order never
  chooses a winner;
- Task `RequestedSkills` is an exact REQUIRED selector over an already
  configured attachment. It carries no Config, never synthesizes `{}`, and
  cannot create a request. Only one exact configured backing can be narrowed
  to REQUIRED; a Task-only selector, near version, or multiple candidates
  fails closed;
- a non-empty Workspace `AllowedSkills` list is an exact full-`SkillRef`
  ceiling. The current V1 wire cannot distinguish absent from empty, so nil and
  empty both add no extra allow ceiling. Workspace and Agent denies are exact,
  and deny wins;
- global REQUIRED error priority is invalid/tampered complete parent, required
  deny, required allow miss, required exclusive conflict, then unbacked Task
  selector. OPTIONAL unavailability retains the canonical ordered reason set
  `DENIED -> NOT_ALLOWED -> EXCLUSIVE_CONFLICT`. Any reason records the entire
  slot as unavailable; it never selects another candidate;
- origins and policy are derived from one validated final-join projection.
  Candidate aggregation buckets by `SkillRef + ConfigHash` and then exact
  compares Config bytes for collision safety; Config is cloned only into final
  output. An origin bitmap proves every source index is covered exactly once;
- the real-chain fixture proves that the same AgentVersion identity can be
  reused in two Workspaces while their exact allow ceilings produce different
  results, including reverse-order validation without cross-Workspace bleed.
  Tests cover Task exact, REQUIRED/OPTIONAL, allow/deny, digest/version/Config
  conflict, direct/CURRENT conflict, ordered multi-reason unavailability,
  global error priority, tampering, alias isolation, and concurrent Validate.

The final independent read-only review initially retained one P3: the exact
Config-bytes comparison inside a ConfigHash bucket lacked a direct collision
regression. A private analyzer fixture now supplies the same Ref and ConfigHash
with different Config bytes and proves that two candidates and an explicit
conflict remain. Final P0/P1/P2/P3 are all zero. On final production hashes,
Windows five-package shuffled tests pass three times
(`attachmentowner` 1.177s, `taskmemberauth` 2.064s,
`skillbindingcurrent` 12.092s, `skillbindingresolver` 46.058s, and
`compositeprewire` 92.539s); five-package vet and formatting checks pass. The
Debian WSL five-package `-race -shuffle=on` command passes
(`attachmentowner` 9.163s, `taskmemberauth` 10.023s,
`skillbindingcurrent` 35.242s, `skillbindingresolver` 77.965s, and
`compositeprewire` 186.610s). After the test-only collision regression, the
final resolver test hash separately passes Windows shuffle times three
(50.360s), vet, and Debian WSL `-race -shuffle=on` (65.045s).

Key SHA-256 values for the deterministic-merge checkpoint are:

```text
skillbindingresolver/origins_v1.go =
  B0733BCE6D016A9D5D18F5BB5EFAF64563A789BAEAF27623ED421BA022C7803B
skillbindingresolver/merge_v1.go =
  DE7BA305E0B3DBFEBBDE7288A9CC88D3BE8F710A121BF9321DE3F92F13381CCC
skillbindingresolver/merge_v1_test.go =
  ECCE0C6E66B13252783022AB0037FDEA759AE2A59CDE6C31D5C9BC638B58B19F
skillbindingresolver/api_boundary_v1_test.go =
  651F28C2C0FAE32485C60244ECD696EEA1E1058DB4572DDEA7B8575E68AF8038
skillbindingresolver/real_chain_v1_test.go =
  AFCBABF6E4392981A47A557812FC234DB53E2CE6E810DD84D46530AAF17A0822
skillbindingresolver/doc.go =
  E2A0A74093386A4E0F540637112D7AD32B10022BCAC34BEF7C2D12BE7492E099
skillbindingresolver/dependency_contract_v1_test.go =
  926308BEFF1ADEE7E2EE23A8B3D6AD8A2081C61C1789295D7868A65853EB31E5
```

This merge still performs no catalog-current lookup, exact content
availability check, content load, selection/use authorization, Runtime,
trusted MemberSnapshot, migration, SQLite current, context read, or
`pure_chat` optional repository read. It keeps Skill optional and preserves
independent Role, Persona, Memory, RAG, MCP, context compression/drop,
UNKNOWN, SQLite-default, and cold-family-default-off decisions.

Exact Skill resolution checkpoint (2026-07-29):

- `RepositoryVerifiedCurrentSkillContentReadV1` is a process-local,
  non-authorizing exact assessment over one atomic lifecycle closure read.
  Activation `NONE` is a sealed negative result. Exact observations retain
  ordered `REVOKED -> LIFECYCLE_DRIFT` conditions and never expose inventory,
  nearest-version, or fallback lookup;
- `PlanExactSkillBindingResolutionV1` consumes only the complete deterministic
  merge and complete RuntimeCatalog generation, then exact-matches the
  Task-frozen Tenant, generation, and hash. No admissible binding or an absent
  anchor for OPTIONAL bindings selects a `NONE` read; an absent anchor for any
  REQUIRED binding fails closed;
- the read/decision boundary is the explicit sealed
  `ExactSkillContentReadAssessmentV1` `NONE|ONE` union. Its only public
  production constructor path is
  `LoadExactSkillContentReadAssessmentV1`: `NONE` makes zero repository calls
  and `ONE` makes exactly one lifecycle loader call. Both private sealers and
  the lifecycle loader have an AST-locked single production caller in that
  read-plane function;
- a `ONE` assessment exact-matches DTD, Tenant, source backend, and backend
  owner epoch during sealing, validation, and final resolution. Catalog
  generation/hash drift remains a resolver outcome rather than being
  misclassified as an invalid read scope;
- final resolution uses only `LookupAssemblyExact`. OPTIONAL revoked,
  catalog-drifted, lifecycle-drifted, and not-current bindings remain recorded
  unavailable results. REQUIRED failures use the fixed global priority
  `REVOKED -> LIFECYCLE_DRIFT -> NOT_CURRENT`; equal generation with a
  different catalog hash is corrupt identity and fails closed;
- plan, assessment, resolution, read, and observations expose no wire/hash,
  Restore, scalar constructor, selection/loading/retrieval/Tool/effect
  authority, Runtime path, Store, or migration.

On the final files, the Windows resolver package shuffle passes in 123.214
seconds, the real exact current/read-plane chain passes in 112.424 seconds,
vet and formatting pass, and the Debian WSL exact resolver race shuffle passes
in 642.788 seconds. The lifecycle combined race command exceeded its ten-minute
aggregate performance budget and is not reported as passed; split contract
groups pass in 393.009, 157.573, and 181.267 seconds with no race or assertion
failure. Independent final review reports no P0/P1.

Key SHA-256 values are:

```text
skillcontentlifecycle/current_resolution_exact_read_v1.go =
  12B8C9596E02F0C20ADBFCCABD2F0C855C919BD82CCDAFDE680F83178B069F9E
skillcontentlifecycle/current_resolution_repository_v1.go =
  21C6EB1D51DB1A3DD14A742686730C78D5C54D700C0DBE92BEFDFAFA0F9E5818
skillbindingresolver/exact_v1.go =
  5C4C2A8C7011DB09E4F8C680F9A725460FCFBF5DF801806BEA39F4FAF20AA7B7
skillbindingresolver/read_plane_v1.go =
  A163240C10762B11BEA361D64403CD91B81963A0E314347A2C23173A2A8AA2C2
skillbindingresolver/exact_read_chain_v1_test.go =
  91DFB7BDC32579532D5180B8153409F359AB4A4A033E9C2E100B769C49C53866
skillbindingresolver/api_boundary_v1_test.go =
  13A086A8F1B2EBEC3CA4B7F7698FC4E56147CDC4BB5EC99EB59F066705183E90
```

This checkpoint does not alter independent Role, Persona, Memory, RAG, MCP,
85% compression, the 100% minimal-oldest-prefix drop rule, UNKNOWN
no-semantic-replay, SQLite default, or cold-family default-off behavior. Skill
remains optional and one AgentVersion remains reusable across Workspaces. The
upper path must still bypass every Skill-current repository before
`PURE_CHAT`; an empty merge or empty resolved list is not proof of that mode.

Authority-aware exact Skill member snapshot checkpoint (2026-07-29):

- `internal/membersnapshot.ExactSkillMemberSnapshotV1` is a new legacy-free
  evidence family. New accepts only the complete sealed
  `ExactSkillBindingResolutionV1`. Restore requires both the portable document
  and the same complete exact resolution; a detached DTO, bare hash,
  SelectionV2, MemberSnapshotV2, or conversion function cannot substitute for
  that retained parent;
- the new canonical schema and hash domain bind exact DTD, Tenant, Workspace,
  Task, Run, member, Activation, public AgentVersion, Profile, RuntimeCatalog,
  four complete owner references in
  `WORKSPACE -> AGENT_VERSION -> ASSEMBLY_PROFILE -> TASK` order, the
  RuntimeCatalog Skill anchor `NONE|ONE`, content read `NONE|ONE`, complete
  origin indexes for every merged outcome, and READY evidence or unavailable
  diagnostics;
- the provenance validator requires the global union of origin indexes to be
  exactly `0..N-1`, with no duplicate, gap, out-of-range value, or rehashed
  parent substitution. Backend-local source, owner epoch, and read revision are
  deliberately excluded from the portable wire; the full exact resolution is
  privately retained and replayed by explicit `Validate`;
- the private retained wire must reproduce the canonical bytes exactly. The
  public projector performs the one complete deep validation; private sealed
  projections make later construction reads local, and AST tests prevent
  reintroducing `projection.Validate` into that hot path;
- `member_mode=MODULAR` is a non-authorizing evidence label, not trusted mode
  authority. An empty outcome list is `MODULAR-empty`, never `PURE_CHAT`.
  Selection, Skill loading, RAG retrieval, Tool execution, and external-effect
  authority all remain false;
- the package has no Runtime, Store, SQLite, migration, or downstream production
  consumer. The existing MemberSnapshotV2 non-empty fail-closed guard remains a
  permanent regression and cannot be bypassed by upgrade, conversion, fallback,
  or reinterpretation.

On the final files, the full Windows membersnapshot shuffle passes in 84.770
seconds and the related four-package vet passes. The final semantic files also
passed membersnapshot WSL race in 556.510 seconds, resolver projection
concurrency WSL race in 245.979 seconds, and the READY public chain in 75.199
seconds. A literal canonical/hash golden fixes the snapshot hash at
`429cbd0ea5efaf8b1426681c30a39f16b6597b6c7ab0778f8ab0f3d4d975ea52`.

Key current SHA-256 values are:

```text
membersnapshot/exact_skill_snapshot_v1.go =
  9AABEA3CA353D326655FC0DF18BAF53DC34835C5DA541772CF893CC7C2C7D30C
membersnapshot/wire_v1.go =
  3C1846588F3A922B7C739129CCF4FAA46FCD01636F0843025C4708CDEBD8532E
membersnapshot/document_v1.go =
  3B05167CADA45F24781164FB888564C65AE1E8F20D3CBE8DA78520E8BBA10133
membersnapshot/canonical_v1.go =
  D04963C830F26277E237DAEBE11450A549BD017A2F128AB6EBB577F1961DCE06
membersnapshot/api_boundary_v1_test.go =
  8F03461487A46EE40AC06CB88E8E9EE79BF348EE3009ADF3DD6B6904A549E921
membersnapshot/golden_v1_test.go =
  30CF7B0B8E1B62899DF1D61007B3760E5AACD211C9A180CF2BFAB5E25D9E0D7D
skillbindingresolver/member_snapshot_projection_v1.go =
  81359633CC95082C2656E80A31A7648B6A064675C561F99CDF4DE7FBA0BD6E72
skillbindingresolver/member_snapshot_projection_v1_test.go =
  1FA40D7E5F13A51A729552202B6D05FC7B423063958EB0FCCD5FE77944AFB459
```

This closes the non-authorizing snapshot evidence atom, not the complete Stage
3-B acceptance gate. Production wiring still requires a trusted legacy-free
`PURE_CHAT|MODULAR` mode parent and branching before every optional
Skill-current repository read. Snapshot-level public fixtures for one
AgentVersion across two Workspaces and for a complete unavailable result also
remain acceptance gaps. The snapshot is not selected/use/loading/execution
authority.

The remaining implementation order is now:

1. begin the first additive-only Stage 3-R atom: the unique public `Loop.Run`
   contract and internal `LoopFrameV2`, with no production consumer, Store,
   migration, or Runtime wiring;
2. retain the trusted-mode, `pure_chat` zero-read, dual-Workspace snapshot, and
   unavailable-snapshot gaps as mandatory gates before production wiring;
3. only then converge production runtime wiring without relaxing the permanent
   MemberSnapshotV2 guard.

### 8.3 Stage 3-C: shared RAG and actual-use provenance

Implement:

1. Knowledge content versions and collection version authorities;
2. authenticated collection current activation and revocation;
3. independent Workspace/Agent/Profile/Task collection
   contribution-generation authorities, without changing existing v1
   authoring wires;
4. the two-layer retrieval freeze;
5. retrieval, ranking, projection, cache, and context-use evidence;
6. multi-Agent, multi-Workspace, restart, retention, and corruption tests;
7. sealed output for the later generalized compiler/receipt.

Memory may reuse common Stage 3-A/C primitives, but its non-empty attachment
gate stays closed until separate Memory parents pass the same standard.

Final V3 convergence starts only after Stage 3-C, Stage 4, and the remaining
legacy-free assembly parent work are complete.

## 9. Persistence and migration lock

The embedded public SQLite migrations currently end at `0011`.

Migration `0012` remains reserved exclusively for the unified usage, cache,
reasoning-token, price, cost, and settlement accounting slice described by the
Stage 2 specification. Stage 3 MUST NOT reuse, rename, repurpose, or partially
populate migration `0012`.

This document assigns no Stage 3 migration number. Stage 3 persistence is
additive and may receive a number only after:

1. the exact portable wire/domain inventory for that sub-stage is locked;
2. migration `0012` is materialized with its authoritative fingerprint;
3. SQLite and the experimental PostgreSQL physical parent/FK inventory is
   reviewed;
4. fresh, upgrade, rollback-on-failure, backup/restore, and corruption tests
   are defined.

Existing V1/V2 bytes, hash domains, migration fingerprints, and repository
semantics remain immutable. Historical structural Skill/content documents may
be restored for audit, but MUST NOT be backfilled into active sealed authority
without strict neutral-reference conversion followed by an explicit import,
review, publication, and activation path. A historical opaque SkillID outside
the strict catalog grammar is audit-only and cannot be promoted.

## 10. Current fail-closed contract

At the time of this lock:

- `internal/modulebinding` can parse and self-hash structural Skill/content
  binding documents;
- it does not have sealed current membership and selection resolution views;
- `NewMemberSnapshotV2` and `RestoreMemberSnapshotV2` return
  `ErrAuthorityUnavailable` for every non-empty Skill/content authority claim;
- `PURE_CHAT` and the currently supported provider-only snapshots remain
  available;
- `PureChatRunManifestV3ShadowV1` remains diagnostic-only and has no
  repository, production reader, grant, promotion, or dispatch authority.

This is the required safe state during Stage 3-A and most of Stage 3-B. A
structural self-hash, valid JSON shape, scalar catalog reference, or current
in-memory `skillcatalog.CatalogProjection` is not sufficient reason to relax
the guard.

## 11. Acceptance gates

### 11.1 Stage 3-A

- strict canonical New/Restore round trips and literal golden vectors;
- duplicate-key, unknown-field, depth, byte, count, integer, and alias
  isolation negatives;
- package/content/membership/generation parent substitution negatives;
- lifecycle status cannot mutate immutable artifact identity;
- portable Restore cannot activate;
- current activation CAS has exactly one winner;
- Skill requirements never appear as granted permissions;
- no package import or test invokes network download, shell, or package
  manager behavior.

### 11.2 Stage 3-B

- the same AgentVersion in two Workspaces resolves different permitted
  attachments without mutation or cross-Workspace bleed;
- owner ordering is deterministic and is not last-writer-wins;
- complete origin sets, deny-wins, required-dominates-optional, explicit
  conflict, and no-fallback behavior pass;
- a content-only MODULAR member can select exact Skill parents with zero
  executable providers;
- a Skill cannot execute without an independent exact Module/MCP grant;
- pure chat performs zero optional repository calls under empty and non-empty
  catalogs;
- the pre-existing V2 non-empty fail-closed guard remains permanent; only the
  new authority-aware snapshot family may consume a complete sealed resolver
  view.

### 11.3 Stage 3-C

- two authorized Agents reuse one shared content payload while retaining
  distinct member/use authorities;
- Tenant, Workspace, Agent, collection, and item substitutions fail;
- `SNAPSHOT` never observes a later collection version;
- explicit fresh mode records a new exact Admission/grant/Receipt evidence pair
  and replay uses the receipt evidence instead of re-querying;
- cache hit evidence closes to the same exact source versions and permissions;
- retrieval/projection budgets and actual usage are persisted;
- revocation, missing blob, digest mismatch, stale activation, interrupted
  publication, restart, backup/restore, and concurrent update tests fail
  safely;
- no RAG content is copied into Agent identity or leaks across Workspace or
  Tenant boundaries.

### 11.4 Cross-stage final V3

Stage 3 evidence may become a parent of final V3 only after:

- Stage 3-A/B/C gates pass;
- Stage 4 sealed MCP parents pass;
- final generic attachment union and legacy-free compiler/receipt schemas are
  separately locked;
- shadow comparison, recovery, concurrency, multi-Workspace isolation, and
  fault-injection acceptance pass;
- no V2 or shadow byte/hash is promoted or reinterpreted.

## 12. Effect on the original design

| Decision | Effect |
|---|---|
| Start Stage 3 after the Stage 2 baseline/shadow | Neutral; removes a planning cycle without widening runtime authority |
| Treat final V3 as cross-stage convergence | Positive; prevents placeholder parents and premature production cutover |
| Keep Role, Persona, and Memory independent | Positive; preserves freely replaceable optional modules and future affective extensions |
| Keep Skill non-executable and non-authorizing | Positive; preserves the trusted-kernel permission boundary |
| Keep shared RAG outside Agent identity | Positive; preserves lightweight Agents, reuse, and Workspace-specific composition |
| Allow the same AgentVersion in different Workspaces | Positive; preserves free Agent/Workspace orchestration without mutation |
| Require pure-chat zero optional resolution | Positive; preserves the minimum chatbot mode and prevents hidden cost/context pollution |
| Use two-layer RAG freeze | Positive; permits explicitly fresh professional knowledge while keeping every actual use exact and auditable |
| Preserve migration `0012` for accounting | Neutral; avoids schema collision and leaves existing Stage 2 work unchanged |
| Keep non-empty Skill/content fail-closed until sealed views exist | Neutral to positive; delays capability but prevents structural hashes from becoming false authority |
| Share one lower typed Skill-ref conversion | Positive; prevents governance and package evidence from drifting while keeping governance out of lower packages |
| Preserve historical Skill v1 hashes and fail closed on non-NFC folded tags | Neutral; keeps audit compatibility without letting new authoring create unsealable content |
| Allow equal SkillMD payload hashes across distinct semantic packages | Positive; preserves shared content storage without merging package identity or authority |
| Keep immutable catalog generation lifecycle-neutral | Positive; separates content inventory from review, activation, currentness, selection, and execution |
| Verify immutable owner ancestry before adding contribution currentness | Positive; preserves the same AgentVersion across different Workspace compositions without mutating Agent identity or prematurely granting attachment authority |
| Join trusted governance and SelectionV2 only as a zero-attachment structural compatibility witness | Positive to neutral; catches cross-DAG owner/member substitution now while preserving the separate future legacy-free non-empty selection family |
| Use a generic verified Owner Ref but a Skill-specific portable contribution generation | Positive; lets optional attachment families reuse structural ownership without collapsing Role, Persona, Memory, RAG, Skill, or MCP semantics into one core union |
| Separate portable activation history from one-read fixed-slot current state | Positive; adds explicit deactivation and concurrent-read consistency without embedding Skill state in Agent/Workspace identity or allowing history to self-promote to current |
| Merge exact requested Skill bindings by identity, with deny-first conflicts and no fallback | Positive; makes Workspace-specific composition deterministic and auditable without making Skill core, mutating Agent identity, or granting catalog/load authority |

No decision in this document adds PAB, makes a specific Role/Persona mandatory,
embeds domain knowledge in Agent identity, grants Skill execution rights,
requires MCP for pure chat, or reduces Agent/Workspace composition freedom.
