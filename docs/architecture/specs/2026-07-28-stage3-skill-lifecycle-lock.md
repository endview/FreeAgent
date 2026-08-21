# Stage 3 Skill review, lifecycle, activation, and revocation lock

> S0 状态：`S2_SEMANTIC_SOURCE_ONLY`。仅供 S2 Skill 语义参考，不是当前规范或生产完成证明；当前架构权威见本目录 `README.md`。

> Status: normative implementation lock  
> Date: 2026-07-28  
> Scope: Stage 3-A control-plane evidence only  
> Production status: no review/publication/lifecycle evidence is wired to
> Store, Runtime, MemberSnapshot, or context loading; the existing SQLite
> generic governance endpoints only gain fail-closed Skill bypass guards

## 1. Decision

`SkillContentCatalogGenerationV1` remains an immutable, lifecycle-neutral
inventory. It is not sufficient to identify the Skill versions that a new Run
may select because:

- one generation may contain multiple revisions of the same Skill;
- generation membership has no `ACTIVE`, `CANARY`, `QUARANTINED`, or
  `RETIRED` state;
- portable Restore cannot prove that a generation or package transition is
  current;
- content integrity cannot grant Workspace, Agent, Tool, network, file,
  Secret, RAG, or external-effect authority.

Stage 3 therefore uses the following minimum chain:

```text
SkillPackageEvidenceV1
        |
        v
governance SUBMITTED proposal + eligible independent review
        |
        v
SkillPackageReviewResultV1
        |
        v
governance MERGED proposal
        |
        v
SkillPublicationEvidenceV1
        |
        v
SkillPackageLifecycleTransitionV1
        |
        v
SkillCatalogActivationTargetV1
        |
        v
SkillCatalogActivationV1 + current CAS

SkillContentRevocationRecordV1 append-only chain
        |
        +--> overrides new selection and later actual use
```

Review, publication, package rollout, catalog currentness, revocation, and
attachment permission are deliberately separate facts.

## 2. Original-design invariants

This slice MUST preserve all of the following:

1. Role, Persona, Memory, Skill, MCP, Tool, Knowledge/RAG, and Team remain
   optional modules.
2. AgentVersion remains a lightweight identity and capability/request
   description. Skill content and lifecycle state are not copied into it.
3. The same AgentVersion may resolve different permitted Skills in different
   Workspaces without mutating the AgentVersion.
4. `pure_chat` performs zero Skill repository, current-pointer, index,
   lifecycle, and revocation queries.
5. A Skill may describe a Tool requirement but cannot grant Tool, network,
   file, Workspace, Secret, RAG, or external-effect authority.
6. A portable document, valid hash, review result, publication, lifecycle
   state, or current catalog alone never authorizes attachment or execution.
7. New configuration affects new Runs only. A frozen Run keeps exact package
   and activation evidence, subject to immediate revocation checks before
   later actual use.
8. `UNKNOWN` external effects are never semantically replayed. Skill
   revocation or fallback cannot weaken that rule.
9. No PAB-specific type or dependency is introduced. A future affective
   module remains an ordinary optional attachment.
10. Stage 3 does not allocate migration `0012`.

## 3. Package and dependency boundary

### 3.1 `internal/skillgovernancebridge`

This upper bridge owns the Skill-specific projection of the existing generic
governance workflow:

- `SkillPackageReviewResultV1`;
- `SkillPublicationEvidenceV1`;
- exact proposal/review/reviewer/merge parent digests;
- source and content deduplication keys derived from complete parents.

It may import:

- `internal/governance`;
- `internal/skillcatalog`;
- `internal/skillpackage`;
- `internal/assemblyauthority`;
- lower neutral reference and canonical helpers already allowed by the
  repository boundary.

It MUST NOT publish a catalog current pointer or grant runtime authority.

### 3.2 `internal/skillcontentcatalog`

This package continues to own lifecycle-neutral immutable content inventory.
It adds only a process-local, once-validated exact index:

- `SkillContentCatalogGenerationIndexV1`;
- exact neutral-catalog-ref lookup;
- exact public-assembly-ref lookup.

It MUST NOT import governance or lifecycle packages.

### 3.3 `internal/skillcontentlifecycle`

This new package owns:

- package rollout history;
- activation targets;
- catalog activation history and current CAS tokens;
- Skill content revocation history and snapshots;
- current lifecycle-aware availability resolution.

It may import `skillgovernancebridge`, `skillcontentcatalog`,
`skillpackage`, `backendowner`, `assemblyauthority`, `tenantid`, and SDK
identity packages.

It MUST NOT import:

- `internal/governance`;
- `internal/runtimecatalog`;
- `internal/assemblyactivation`;
- `internal/lifecycle`;
- `internal/store`;
- `internal/runtime`.

This is a direct-import boundary enforced by AST tests. Because
`skillgovernancebridge` consumes the existing generic governance workflow,
`skillcontentlifecycle` has a transitive build dependency on that bridge's
dependencies. It MUST NOT use or expose governance/runtimecatalog types in its
own API. If that transitive dependency becomes a binary/build-size problem,
publication evidence will move behind a separately versioned neutral evidence
package; V1 wire meanings will not be changed in place.

This direction keeps generic governance above the immutable Skill content
types while preventing Store and Runtime from becoming domain dependencies.

## 4. Locked V1 compatibility

No existing V1/V2 canonical bytes, field meanings, hash domains, constructors,
Restore behavior, or historical audit exceptions may change. This includes:

- `SkillVersion` content identity;
- `SKILL_CANDIDATE_V1`;
- `ContentPayloadV1`;
- `SourceTagSetV1`;
- `ImmutableArtifactViewV1`;
- `SkillPackageEvidenceV1`;
- `SkillPublicInternalRefMappingV1`;
- `SkillContentCatalogMembershipV1`;
- `SkillContentCatalogGenerationV1`;
- `SkillContentCatalogMembershipRefV1`;
- historical `modulebinding` V1 mapping/ref bytes and hashes;
- existing governance proposal, source, review, merge, and permanent blocked
  source semantics;
- the permanent `MemberSnapshotV2` non-empty Skill/content fail-closed guard.

The new types use new names and domains. They do not reinterpret an old
status, hash, or migration row.

## 5. Common document rules

All new portable documents use:

- `schema_version = 1`;
- RFC 8785 canonical JSON;
- exact lowercase SHA-256 values through the named domain;
- maximum document size `1 MiB`;
- maximum JSON depth `64`;
- maximum JSON nodes `262144`;
- JSON-safe positive integers, at most `2^53 - 1`;
- strict decoding, no unknown or duplicate keys, and no trailing value;
- NFC, trimmed, valid UTF-8 audit text without control characters;
- maximum opaque identity text `512` bytes;
- maximum reason/rationale text `4096` bytes;
- maximum ordered collection size `4096`.

The count limit is only a ceiling, not a promise that 4096 maximum-sized
entries fit in one document. Both count and byte limits apply; construction
fails with `ErrLimitExceeded` as soon as either is exceeded.

For every self-hashed document:

```text
BodyCanonical = RFC8785(JSON(body_without_self_hash))
SelfHash      = moduleapi.Digest(locked_domain, BodyCanonical)
Document      = RFC8785(JSON(body_fields + self_hash))
```

The field lists below define the typed body, not textual key order. RFC 8785
orders object keys lexicographically. Implementations MUST marshal the typed
body and document and MUST NOT concatenate fields manually.

Every no-parent value is:

```text
moduleapi.Digest(no_parent_domain, UTF8("NO_PARENT"))
```

The locked constants are:

```text
SkillPackageLifecycleNoParentHash =
  f526b71138818db2fbba732eede14ef0555c42339ee11b8024de0fc768b5eca4
SkillCatalogActivationNoParentHash =
  3b424e879d433f64e9415d9524571a39dc1279cb54a25fef61722883455922b4
SkillContentRevocationNoParentHash =
  0cb41e4b27d4011d38d76e110f010dcf79920548dec4ca36b8796dba04118b7f
```

Empty ordered collections are canonical non-null `[]`. Genesis
`from_state` is the empty string. Optional activation/rollback refs use a
non-null object with `present=false` and every other field at its zero value;
when `present=true`, every exact field is required and non-zero.

`schema_version` and existing membership/owner-transition ordinals are
`uint32`. Catalog generations, lifecycle ordinals, activation ordinals,
revocation watermarks, governance versions, and Unix-millisecond times are
`uint64` within the JSON-safe range. Successor lifecycle/activation/revocation
times must be greater than or equal to their predecessor time.

`actor_id` uses the existing ActorPrincipalRef grammar: 1..128 ASCII bytes,
`[A-Za-z0-9][A-Za-z0-9._:@-]{0,127}`, with no trimming or case folding.
Every `authority_ref` field is exactly
`assemblyauthority.ApprovalAuthorityRef`.

Every `New` validates and defensively retains complete typed parents. Every
trusted `Restore` requires those same complete parents and rebuilds the exact
canonical bytes. A hash-only or scalar-only Restore is insufficient.

Where a relationship depends on a durable commit/current fact, raw Restore is
explicitly audit-only. A separate `Verify...` promotion requires the
repository-issued committed/current view and complete typed parents before a
resolution-shaped view becomes available.

Portable Restore proves immutable historical relationships only. It never
proves database currentness, live backend ownership, authenticated operator
identity, or runtime permission.

### 5.1 Active-owner wire projection

`backendowner.UnifiedRuntimeActiveOwnerRefV1` has private fields and MUST NOT be
marshaled directly. Every lifecycle document uses this derived nested wire:

```text
deployment_trust_domain_id
source_backend_id
backend_owner_epoch
owner_instance_id
owner_identity_hash
owner_active_transition_ordinal
owner_active_transition_hash
```

Construction and trusted Restore require the complete exact
`backendowner.UnifiedRuntimeOwnerEpochTransitionV1`, validate that its
to-state is ACTIVE, derive `UnifiedRuntimeActiveOwnerRefV1` through
`ActiveOwnerRef`, and then project every field through accessors. The nested
deployment trust domain must equal the containing document. The nested wire
remains structural and cannot prove the durable owner fence.

## 6. Review result

### 6.1 Existing governance remains authoritative

V1 review uses the existing:

- `governance.CanonicalProposal`;
- `governance.CanonicalReview`;
- `governance.ReviewerEligible`;
- `governance.ApplyReview`.

The submitted proposal MUST contain a strict `SKILL_CANDIDATE_V1` change. Its
decoded candidate is first passed through
`skillcatalog.SealImmutableArtifactViewV1`; that immutable view MUST close
exactly to the supplied
`SkillPackageEvidenceV1`:

- Tenant is equal;
- neutral Skill ID and revision are equal;
- content hash is equal;
- package digest is equal;
- manifest digest is equal.

The commit coordinator must also replay the existing bridge invariant that
`proposal.Domain` is an exact member of the validated candidate manifest's
canonical `Domains` set. It does not trust that the proposal was created by a
bridge constructor. Replacing only the proposal domain, including with another
valid governance domain, fails the committed review closure.

The package evidence is required for both approve and reject. A reviewer
therefore decides an exact inspected package, not an unbound source label.
V1 review handles only candidates that crossed the strict ingestion and
immutable-seal boundary. Malformed/unsealable input is rejected before a Skill
proposal exists; historical corrupt input remains audit-only and does not use
a forged reject result.

V1 requires the existing high-trust, current `REVIEWER` eligibility check and
forbids self-review. The existing governance merge path also requires a
current eligible reviewer identity; publication therefore retains and hashes
that complete merger identity as well as the MergeRequest. An Operator may
authenticate and perform activation or revocation operations but cannot
bypass review or forge a reviewer/merger in V1. A future human-review or
human-merge policy requires a new typed authority version.

### 6.2 Locked parent and fingerprint digest domains

The bridge defines these internal complete-parent and derived-content
digests:

```text
freeagent.skill-governance-proposal-parent.v1
freeagent.skill-governance-review-parent.v1
freeagent.skill-governance-reviewer-parent.v1
freeagent.skill-governance-merge-parent.v1
freeagent.skill-candidate-content-fingerprint.v1
freeagent.skill-source-identity.v1
```

The first four hash the following dedicated projections after the existing
validators succeed:

- proposal parent: all `ChangeProposal` fields, including status, version,
  title, summary, complete SourceKey, submitter version, and ChangeJSON;
- review parent: all `Review` fields, including decision, expected proposal
  version, and rationale;
- reviewer parent: all `ReviewerIdentity` fields, including kind, status,
  trust, and current;
- merge parent: all `MergeRequest` fields, including merger Agent/version and
  expected version.

Dedicated projection structs contain no `omitempty` tags. Empty optional
strings are encoded as `""`, not omitted. Proposal `ChangeJSON` is first
strictly decoded as one object and embedded as its RFC 8785 canonical object;
its input whitespace is not identity. No other field is excluded.

The proposal-before and proposal-after hashes use the same proposal-parent
domain over their respective complete states. Review and merger identities
use the same reviewer-parent domain but hash their respective complete
values. A MergeRequest is trimmed, defaults schema zero to V1, and validates
all four external IDs and a positive JSON-safe expected version before its
projection is hashed.

The candidate-content domain hashes this revision-independent projection:

```text
schema_version
manifest_without_revision {
  schema_version
  skill_id
  name
  description
  domains
  keywords
  required_capabilities
}
skill_md
resources
dependency_lock_digest
tool_bindings
```

All arrays use the already canonical order from the validated candidate. Only
manifest revision, lifecycle status, derived token estimate, and exact content
hash are excluded. These digests are relationship or deduplication evidence,
not new governance state.

The source-identity domain hashes:

```text
schema_version
mechanism
origin_descriptor_digest
```

where mechanism is one of `LOCAL_IMPORT`, `UPLOAD`, `REMOTE_URL`,
`AGENT_GENERATED`, or `MAINTENANCE`, and origin descriptor digest is the
lowercase SHA-256 of the trusted adapter's separately locked canonical origin
descriptor bytes. The document adds `skill_source_identity_hash`; review and
publication bind that hash and require the proposal SourceKey to equal the
exact derived ASCII key.

`NewSkillSourceIdentityV1` requires the canonical origin-descriptor bytes and
derives the digest; there is no digest-only constructor.
`RestoreSkillSourceIdentityV1(document, canonical_origin_descriptor_bytes)`
also requires that complete parent and recomputes the digest and identity hash.
The portable identity document does not inline the descriptor, but the typed
value retains a defensive copy so `Validate` and committed-view reconstruction
can close the relationship after restart.

### 6.3 `SkillPackageReviewResultV1`

Hash domain:

```text
freeagent.skill-package-review-result.v1
```

The canonical body contains, in this exact semantic order:

```text
schema_version
tenant_id
proposal_id
proposal_version_before
proposal_version_after
proposal_before_hash
proposal_after_hash
review_id
review_hash
reviewer_agent_id
reviewer_agent_version_id
reviewer_hash
decision
resulting_source_disposition
source_key_digest
skill_source_identity_hash
skill_catalog_version_ref
package_digest
manifest_digest
skill_content_fingerprint
skill_package_evidence_hash
authority_ref
reviewed_at_unix_millis
```

The document adds `skill_package_review_result_hash`.

For `APPROVE`, `ApplyReview` must produce `APPROVED` and the resulting source
disposition is `APPROVED`. For `REJECT`, it must produce `REJECTED`, the
returned `RejectedSourceUpdate` must close to the same proposal/source, and
the resulting source disposition is permanently `BLOCKED`.

`APPROVE` does not mean published, active, current, selectable, loadable, or
authorized.

`authority_ref` is exactly
`assemblyauthority.ApprovalAuthorityRef`. It is a structural record of the
authority authenticated by the caller and does not authenticate itself.

### 6.4 Repository-issued committed review view

A structurally valid set of proposal/review/reviewer values can be evaluated
without ever being committed. It therefore cannot become a lifecycle parent
directly.

`CommitSkillReview` asks a trusted repository to atomically persist and return
a complete committed record containing:

```text
proposal_before
skill_source_identity
canonical_origin_descriptor_bytes
review
reviewer_identity_as_verified_in_the_transaction
proposal_after
source_before
source_after
package_evidence
authority_ref_as_authenticated_by_the_control_plane
committed_at_unix_millis_generated_in_the_transaction
```

The review command must carry the complete `SkillSourceIdentityV1` together
with the canonical origin-descriptor parent bytes from which it was created.
Inside the transaction the coordinator revalidates the descriptor bytes,
recomputes `OriginDescriptorDigest`, identity hash, and derived SourceKey, then
requires exact equality with `proposal_before.SourceKey` and every source
record it reads or writes. The durable committed record retains both the
identity document and those descriptor parent bytes so an exact load after
process restart can perform the same recomputation.

The bridge replays every rule and derives
`SkillPackageReviewResultV1` from that returned record.
`CommittedSkillReviewViewV1` is a process-local private-field wrapper over the
result and complete committed parents, including the source identity and
canonical origin descriptor. It has no Restore, canonical wire, or public
scalar constructor. It can only be returned by the commit coordinator or by
an exact repository load that revalidates the same durable closure. Its
`Result()` accessor returns a defensive copy of the portable
`SkillPackageReviewResultV1`; the portable value alone cannot be promoted back
to a committed view.

Restoring the portable review-result JSON produces audit evidence only. It
does not produce a committed review view.

## 7. Publication and deduplication

### 7.1 `SkillPublicationEvidenceV1`

Hash domain:

```text
freeagent.skill-publication-evidence.v1
```

Publication requires a repository-issued approved
`CommittedSkillReviewViewV1`, the exact approved proposal retained by that
view, a canonical
`governance.MergeRequest`, the complete current high-trust merger
`governance.ReviewerIdentity`, `governance.ReviewerEligible`, and
`governance.ApplyMerge`. The result must be `MERGED`; `APPROVED` alone is
insufficient.

V1 permits the eligible reviewer who approved a proposal to merge it because
the existing rule separates the submitter from review/merge, not review from
merge. The original submitter remains ineligible for both operations.

The canonical body contains:

```text
schema_version
tenant_id
proposal_id
proposal_version_before_merge
proposal_version_after_merge
approved_proposal_hash
merge_request_hash
merger_agent_id
merger_agent_version_id
merger_hash
merged_proposal_hash
review_result_hash
source_key_digest
skill_source_identity_hash
resulting_source_disposition
skill_catalog_version_ref
package_digest
manifest_digest
skill_content_fingerprint
skill_package_evidence_hash
authority_ref
published_at_unix_millis
```

The document adds `skill_publication_evidence_hash`.

The resulting source disposition is exactly `MERGED`. Reject results,
non-approved proposals, mismatched merge actors, versions, tenants, sources,
packages, or authorities fail closed.

V1 has only one `authority_ref` field in publication evidence, so it is
exactly equal to the committed review result's `authority_ref`. It cannot be
silently replaced by another structurally valid authority at merge time. A
future workflow that deliberately separates review and publication authorities
requires a new wire version with two explicitly named fields.

`published_at_unix_millis` is generated by the publication transaction and
must be greater than or equal to the committed review's
`reviewed_at_unix_millis`.

Publication proves that one exact package completed the PR-like workflow. It
does not create package rollout state or catalog currentness.

`CommitSkillPublication` performs merge and publication in one repository
transaction and returns the complete committed merge record:

```text
complete_committed_review_record
approved_proposal
skill_source_identity_and_canonical_origin_descriptor_from_review
merge_request
merger_identity_as_verified_in_the_transaction
merged_proposal
source_before
source_after_merged
package_evidence
authority_ref_as_authenticated_by_the_control_plane
committed_at_unix_millis_generated_in_the_transaction
```

The publication coordinator first replays the nested
`CommittedSkillReviewRecordV1` and seals its review view, then replays and
hashes the outer record and returns one process-local private
`CommittedSkillPublicationViewV1`. A Store adapter never has to construct or
embed the private review view. `Evidence()` returns a defensive copy of the
portable `SkillPublicationEvidenceV1`. Package lifecycle construction requires
the committed publication view, not a portable publication document; portable
evidence is never a second return value that can stand in for the committed
view.

Historical data produced through the old generic merge API may contain a
legitimate `MERGED_BUT_UNPUBLISHED` proposal. It is not active. It may be
repaired only by exact compensation that reconstructs and verifies the
original committed merge facts and atomically claims all publication dedup
keys; it cannot be repaired by issuing a second merge or inventing a new
review time.

Normal `CommitSkillPublication` never compensates such a row. If proposal and
source are `MERGED` but the exact publication is absent, it returns
`ErrMergedButUnpublished` with zero writes. If only some publication/dedup
facts exist, it returns `ErrCorrupt`. A future
`CompensateHistoricalSkillPublication` is a separate operation with separate
authorization and must reconstruct the original committed merge facts before
atomically claiming all publication keys.

Before Stage 3-B production wiring, every generic governance repository/call
site must inspect the strict change envelope. Generic review rejects
`SKILL_CANDIDATE_V1` with `ErrSkillReviewCoordinatorRequired` for both APPROVE
and REJECT; generic merge rejects it with
`ErrSkillPublicationCoordinatorRequired`. Both guards return before mutating a
proposal, review row, source disposition, or event. Pure
`governance.ApplyReview`/`ApplyMerge` remain available inside the dedicated
coordinators after complete parent validation. The guards must not silently
route or partially commit: only `CommitSkillReview` and
`CommitSkillPublication` may perform their respective atomic Skill
transactions. They do not rewrite historical `MERGED_BUT_UNPUBLISHED` rows
and do not affect generic non-Skill or separately governed knowledge
proposals.

### 7.2 Independent source, exact-package, and semantic-content axes

Source deduplication remains exclusively in the existing governance source
repository:

- `(TenantID, normalized SourceKey)` becomes permanently occupied when
  submitted;
- rejected sources become permanently `BLOCKED`;
- no unlock or automatic reconsideration API exists;
- lifecycle and activation cannot rewrite source disposition.

Content deduplication uses separately derived keys:

```text
(TenantID, SkillPackageEvidenceHash)
(TenantID, PackageDigest)
(TenantID, SkillID, SkillContentFingerprint)
```

The first is exact package evidence identity. `PackageDigest` is also an exact
package key, but it includes the manifest revision and therefore cannot by
itself detect a revision-only bump.

The bridge additionally derives
`SkillContentFingerprint = Digest(freeagent.skill-candidate-content-fingerprint.v1, ...)`
from the complete canonical candidate while excluding only lifecycle status,
derived token estimate, exact content hash, and manifest revision. The
projection retains SkillID, all other manifest semantics, `SKILL.md`,
resources, dependency lock, and Tool requirements. The no-change key is:

```text
(TenantID, SkillID, SkillContentFingerprint)
```

This prevents a new revision whose effective content is unchanged from
creating another publication. Equal `ContentPayloadHash` alone remains
allowed because multiple distinct semantic packages may share the same
`SKILL.md` payload.

The bridge only derives and validates these keys. The later repository
transaction enforces uniqueness and returns the existing exact publication on
an idempotent retry. A conflicting proposal, source, or package alias is not
treated as a retry.

New sealed Skill proposals use `SkillSourceIdentityV1`, not an arbitrary
caller string. Its governance SourceKey is derived exactly as:

```text
skill-source:v1:<lowercase-mechanism>:sha256:<origin_descriptor_digest>
```

The result is lowercase ASCII, so the existing whole-string case fold cannot
merge case-sensitive paths, object keys, or URL paths. A URL, uploaded object,
local import, and generated candidate cannot share a mechanism namespace.

Each production import adapter must separately lock and test its canonical
origin descriptor schema before it can create this identity. The bridge checks
RFC 8785 bytes and recomputes the digest; it does not accept a digest detached
from those descriptor bytes. The old arbitrary `Source string` proposal API
remains candidate-only compatibility and the proposal it returns is not, by
itself, evidence of eligibility for committed Skill review/publication. Such a
proposal can qualify only if the commit command separately supplies the
complete typed source identity and canonical descriptor parent and the
transaction recomputes an exact match. Eligibility is determined only by that
complete parent. It is never inferred from constructor provenance or from the
textual shape of `Proposal.SourceKey`; therefore a legacy caller supplying a
string that happens to equal a derived key gains nothing without the same
typed parent. Publication inherits and revalidates the exact committed source
parent; it cannot replace it with a lookalike SourceKey.

## 8. Package lifecycle

### 8.1 State vocabulary

The package lifecycle vocabulary is:

```text
CANARY
ACTIVE
QUARANTINED
RETIRED
```

It deliberately excludes:

- `CANDIDATE`, `APPROVED`, `REJECTED`, and `MERGED`, which belong to
  governance;
- `REVOKED`, which is derived from the separate permanent revocation chain.

Allowed transitions are:

```text
<genesis> -> CANARY
<genesis> -> ACTIVE
CANARY    -> ACTIVE
ACTIVE    -> CANARY
CANARY    -> QUARANTINED
ACTIVE    -> QUARANTINED
CANARY    -> RETIRED
ACTIVE    -> RETIRED
```

`QUARANTINED` and `RETIRED` are terminal in V1. Reintroduction requires a new
revision, new review, new publication, and new lifecycle chain. Same-state
no-op transitions are invalid.

CANARY is deterministic and explicit. V1 has no random, percentage, or
synthetic traffic generation. A later selector may use CANARY only when the
Workspace/Agent/Profile/Task freeze explicitly opts into that exact fallback.

### 8.2 Portable package history and backend-local current

One package history is scoped by:

```text
DeploymentTrustDomainID + TenantID + SkillPackageEvidenceHash
```

Portable transitions are not partitioned by backend owner epoch. Every
transition records a complete `UnifiedRuntimeActiveOwnerRefV1`. After a
verified bootstrap or storage cutover adopts an exact imported tail, a new
active owner may continue the portable ordinal and predecessor chain.

Currentness is different: every physical backend/owner has a local current key
that includes `SourceBackendID + BackendOwnerEpoch`. Two physical databases do
not gain a shared linearizable CAS merely because their logical Tenant key is
equal. A new owner never infers currentness from imported portable history.

The active-owner ref is structural only. The repository transaction must
separately prove that it is still the durable current active owner before
publishing.

### 8.3 `SkillPackageLifecycleTransitionV1`

Hash domains:

```text
freeagent.skill-package-lifecycle-no-parent.v1
freeagent.skill-package-lifecycle-transition.v1
```

The canonical body contains:

```text
schema_version
deployment_trust_domain_id
tenant_id
skill_package_evidence_hash
package_digest
skill_catalog_version_ref
lifecycle_ordinal
previous_transition_hash
from_state
to_state
publication_evidence_hash
active_owner_ref
authority_ref
actor_id
reason
occurred_at_unix_millis
```

The document adds `skill_package_lifecycle_transition_hash`.

Genesis uses the deterministic no-parent hash and an explicit absent
`from_state`. Successors derive ordinal, previous hash, and `from_state` from
the complete previous transition. Caller-supplied scalar predecessor fields
are forbidden.

Every newly publishable transition retains and revalidates the same complete
package and repository-issued committed publication view. Portable Restore
without that committed view yields audit-only transition evidence; it cannot
produce a current lifecycle ref. Successors may have a different active owner,
but trust domain, Tenant, package, publication root, and predecessor chain
must remain exact.

### 8.4 Current package lifecycle CAS token

`CurrentSkillPackageLifecycleRefV1` is private-field, process-local structural
state scoped by trust domain, Tenant, exact package evidence hash,
SourceBackendID, and BackendOwnerEpoch:

- no-current: ordinal `0` and the deterministic no-parent hash;
- current: exact ordinal, transition hash, and lifecycle state.

It has no public scalar constructor for a current value. It is derived from a
fully verified transition, or sealed by the lifecycle coordinator after it
revalidates a complete structural record returned by a repository adapter.
The repository implementation itself does not construct private lifecycle
values.

On a truly empty deployment, the first local current may be published from a
new genesis transition. If portable history already exists, the target local
current must remain empty until the later persistence/cutover lock supplies
`AdoptImportedPackageLifecycleCurrent(exact_tail, verified_cutover_view)`.
That operation is a required future storage primitive, not an interface or
acceptance obligation of this slice. Until its repository-issued
`verified_cutover_view` type is locked and implemented, imported package
history can be audited but cannot become current. Adoption creates no semantic
lifecycle transition and cannot change its state.

Repository publication is:

```text
PublishCurrentPackageLifecycle(expected, next)
```

Only one exact expected value wins. A byte-identical retry with the original
expected may read back the committed result. A stale expected, hash alias,
natural-key alias, partial token, terminal successor, or owner-fence loss
fails closed.

## 9. Once-validated immutable catalog index

`SealSkillContentCatalogGenerationIndexV1(generation)`:

1. performs one complete `generation.Validate()`;
2. defensively snapshots the validated generation and memberships;
3. builds one exact map keyed by `SkillCatalogVersionRefV1`;
4. builds one exact map keyed by the full public `assembly.SkillRef`;
5. rejects every duplicate or inconsistent mapping even if generation
   validation would already reject it.

`LookupSkillMembershipExact` and `LookupAssemblyMembershipExact` return the
new process-local `IndexedSkillMembershipViewV1`:

- are `O(1)` expected-time lookups;
- validate the query key;
- never revalidate or clone the whole generation;
- return a defensive exact membership ref/view through a package-private
  constructor;
- perform no ID-only, revision-only, hash-only, kind-only, case-folded,
  nearest-version, ACTIVE, CANARY, or current fallback.

`IndexedSkillMembershipViewV1.Validate` rechecks only its exact
membership-ref wire/hash, sealed package-digest scalar, and private immutable
index anchor; the complete membership closure was established during index
sealing and can be explicitly re-proved by `index.Validate`. The lightweight
view does not retain the complete generation or package evidence and never
calls `Generation.Validate`. The historical `SkillMembershipResolutionView`
and generation-owned lookup methods retain their prior deep-validation
behavior for compatibility and are not used by lifecycle current resolution.

The index:

- has no Restore or canonical wire;
- has no current pointer, review result, lifecycle state, revocation snapshot,
  permission, constructor callback, payload body, or Secret;
- is immutable after sealing and safe for concurrent reads;
- the index itself and every returned membership view expose the five locked
  `AuthorizesSelection/SkillLoading/ToolExecution/ExternalEffect/RAGRetrieval`
  methods, and every method returns false;
- cannot replace portable generation validation.

Before Stage 3-B production wiring, the only additional production consumer
allowed by the catalog boundary test is
`internal/skillcontentlifecycle`. Store, Runtime, context loading, and
MemberSnapshot remain forbidden consumers.

## 10. Activation target

### 10.1 Purpose

`SkillCatalogActivationTargetV1` is an immutable selection-eligibility
snapshot over one exact catalog generation. The generation may contain
historical inventory not selected by the target.

Hash domain:

```text
freeagent.skill-catalog-activation-target.v1
```

The target body contains:

```text
schema_version
deployment_trust_domain_id
tenant_id
skill_content_catalog_generation
skill_content_catalog_hash
ordered_selected_memberships
```

Each selected entry contains:

```text
membership_ordinal
membership_hash
assembly_skill_ref
skill_catalog_version_ref
skill_package_evidence_hash
package_digest
package_lifecycle_ordinal
package_lifecycle_transition_hash
lifecycle_state
```

The document adds `skill_catalog_activation_target_hash`.

Entries are derived from a complete generation and complete current package
lifecycle transitions, then ordered strictly by membership ordinal. Caller
order has no identity significance.

Rules:

- a target is a subset of one exact generation;
- every selected membership has one exact package lifecycle transition;
- every selected transition has the target's exact trust domain and Tenant;
- activation trust domain and Tenant must equal the target scope;
- only `ACTIVE` and `CANARY` transitions may be selected;
- one SkillID has at most one ACTIVE revision and at most one CANARY revision;
- ACTIVE and CANARY may coexist for a SkillID only as two distinct exact
  revisions;
- duplicate membership, package, transition, SkillID/revision, or public
  assembly ref is invalid;
- empty selected entries are valid, including for a non-empty inventory;
- an empty generation may have only an empty target;
- an empty target still binds an explicit validated trust domain supplied by
  the control plane; it is not inferred from an absent entry;
- the target is lifecycle/currentness evidence only and grants no attachment
  or execution permission.

Portable construction validates the supplied transition documents. Durable
activation additionally proves that each transition is the repository's
current package lifecycle value at publication time.

## 11. Catalog activation and current CAS

### 11.1 Portable activation history and local current scope

The portable activation history has the logical scope:

```text
DeploymentTrustDomainID + TenantID
```

There is no V1 `CatalogID`. Every activation records its exact active owner.
After an authenticated bootstrap/cutover adoption, a later owner may continue
the portable activation ordinal and predecessor chain.

The engine-local current key is:

```text
DeploymentTrustDomainID + TenantID + SourceBackendID + BackendOwnerEpoch
```

A new owner starts with no local current. It cannot derive currentness from the
maximum imported activation ordinal.

### 11.2 `SkillCatalogActivationV1`

Hash domains:

```text
freeagent.skill-catalog-activation-no-parent.v1
freeagent.skill-catalog-activation.v1
```

Actions:

```text
ACTIVATE
ROLLBACK
```

The canonical body contains:

```text
schema_version
deployment_trust_domain_id
tenant_id
activation_ordinal
previous_activation_hash
action
rollback_source
activation_target
active_owner_ref
authority_ref
actor_id
reason
activated_at_unix_millis
```

The document adds `skill_catalog_activation_hash`.

`activation_target` carries exact target hash, catalog generation, and catalog
generation hash. `rollback_source` is absent for ACTIVATE and carries exact
source activation ordinal/hash/target/generation for ROLLBACK.

The nested wires are exactly:

```text
activation_target {
  skill_catalog_activation_target_hash
  skill_content_catalog_generation
  skill_content_catalog_hash
}

rollback_source {
  present
  activation_ordinal
  skill_catalog_activation_hash
  skill_catalog_activation_target_hash
  skill_content_catalog_generation
  skill_content_catalog_hash
}
```

`present` is JSON boolean. Its false/zero and true/complete matrices follow
section 5; `null` is never accepted.

Rules:

- genesis is ACTIVATE with ordinal `1` and the deterministic no-parent hash;
- on a truly empty portable history, genesis may activate any valid positive
  generation; imported non-empty history uses local adoption instead;
- every successor derives ordinal and previous hash from the complete previous
  activation;
- every successor target generation is strictly greater than the predecessor
  target generation;
- ROLLBACK references an earlier exact activation in the same portable
  history;
- the new rollback target has the same ordered semantic selected set as the
  rollback source, but belongs to the new higher generation;
- current never points backward to an old generation;
- ACTIVATE has no rollback source;
- owner epochs may change only through explicit local adoption of the exact
  predecessor tail; adoption does not reset or append portable history;
- an explicit empty target is valid and can safely disable all new Skill
  selection;
- empty Skill current is not `pure_chat`; pure chat still performs zero
  optional reads.

The strictly higher content generation is deliberate: every activated Skill
configuration revision, including a lifecycle-only change, empty target, or
rollback, receives a new immutable inventory generation even when its
membership set is copied byte-for-byte. Immutable generations may exist before
activation, but one old generation is never used as two different activated
configuration revisions.

For rollback comparison, the ordered semantic selected-set projection is:

```text
assembly_skill_ref
skill_catalog_version_ref
skill_package_evidence_hash
package_digest
lifecycle_state
```

It excludes catalog generation/hash, membership ordinal/hash, target hash,
and package lifecycle ordinal/transition hash. The new target must use the
current exact lifecycle transition for the same package and the same
ACTIVE/CANARY state. This permits a later lifecycle transition to re-establish
the requested state while preventing any package, revision, digest, or public
reference substitution.

The repository must prove that a rollback source is an ancestor in the exact
durable history. A portable scalar source ref cannot prove ancestry.

### 11.3 `CurrentSkillCatalogActivationRefV1`

This private-field CAS token contains:

- trust domain and Tenant;
- SourceBackendID and BackendOwnerEpoch;
- no-current/current discriminator;
- exact activation ordinal/hash;
- exact target hash;
- exact catalog generation/hash.

No-current uses ordinal `0` and the deterministic no-parent hash. A current
token has no public scalar constructor and is derived from a fully verified
activation, or sealed by the lifecycle coordinator after it revalidates a
complete structural record returned by a repository adapter.

`AdoptImportedCatalogActivation(exact_activation,
verified_cutover_view)` may establish a target backend's initially empty local
current without creating a new activation or changing the imported target.
Until adoption succeeds, new Skill resolution remains closed. The source
current must have been explicitly deactivated and zero-proved, the target
current must be empty, and the cutover proof must bind both backend identities
and the imported root. Those cutover proof types belong to the later storage
persistence lock; Stage 3-A does not fabricate them, expose this operation in
its repository interface, or claim imported activation adoption as an
implemented acceptance gate.

Publication is:

```text
PublishCurrentCatalogActivation(expected, next)
```

The transaction validates:

1. durable active-owner fence;
2. complete immutable target/generation/membership/package parents;
3. current package lifecycle refs for every selected entry;
4. current revocation tail and no matching target;
5. exact predecessor and monotonic generation;
6. immutable insert/readback;
7. append-only current-history CAS.

Only one concurrent publisher wins. A byte-identical retry requires the
original expected token. Ambiguous commit recovery reads the exact natural key
and compares the full canonical command/result; it never generates a second
activation.

## 12. Permanent Skill content revocation

### 12.1 Typed targets

Skill revocation is a separate V1 domain. The closed target vocabulary is:

```text
SKILL_PACKAGE_EVIDENCE
SKILL_CONTENT_CATALOG_GENERATION
```

Adding these kinds to the existing governance revocation V1 would reinterpret
its closed vocabulary and is forbidden.

Hash domain:

```text
freeagent.skill-content-revocation-target.v1
```

The target body is one fixed tagged wire:

```text
schema_version
deployment_trust_domain_id
tenant_id
target_kind
skill_package_evidence_hash
skill_content_catalog_generation
skill_content_catalog_hash
```

The document adds `skill_content_revocation_target_hash`.

For `SKILL_PACKAGE_EVIDENCE`, the package hash is valid and both catalog fields
are the exact zero/empty absent values. For
`SKILL_CONTENT_CATALOG_GENERATION`, the package hash is empty and both catalog
fields are present and valid. Mixed, partial, all-empty, and unknown-kind
unions are invalid.

There is no generic `(kind, hash)` constructor. Typed constructors require the
complete exact `SkillPackageEvidenceV1` or
`SkillContentCatalogGenerationV1` parent and derive the target identity.

A package target invalidates that exact package wherever it appears. A
generation target is an emergency kill switch for the entire exact
generation. Revocation is permanent. A package target requires a new immutable
package/revision and review chain before equivalent capability can return; a
generation target requires a new higher catalog generation, while its
non-revoked package parents may be reused.

### 12.2 Append-only revocation chain

Hash domains:

```text
freeagent.skill-content-revocation-no-parent.v1
freeagent.skill-content-revocation-record.v1
```

The portable chain is scoped by trust domain and Tenant and does not reset at
a backend owner epoch. Its engine-local current tail is separately scoped by
SourceBackendID and BackendOwnerEpoch.

Each record contains:

```text
schema_version
deployment_trust_domain_id
tenant_id
revocation_watermark
previous_revocation_hash
target
active_owner_ref
authority_ref
actor_id
reason
occurred_at_unix_millis
skill_content_revocation_record_hash
```

Watermarks are contiguous and positive. The repository enforces permanent
uniqueness of `(scope, target_kind, exact_target_hash)`. Repeating an existing
revocation is idempotent only when the entire original command/result is
byte-identical; a different actor, reason, time, owner, or authority is a
conflict, not a new record.

`CurrentSkillContentRevocationRefV1` is a private no-current/current tail token
with trust domain, Tenant, SourceBackendID, BackendOwnerEpoch, exact watermark,
and hash. Publication uses CAS and a durable active-owner fence.

Imported revocation history never initializes that token automatically.
`AdoptImportedRevocationTail(exact_tail, verified_cutover_view)` must establish
the initially empty local tail before any Skill current is adopted or any new
Skill selection is enabled. It creates no new revocation record. Missing,
partial, or stale revocation-tail adoption closes all Skill selection rather
than temporarily treating the imported set as empty. As with the other two
adoption operations, this is a locked future requirement whose proof type and
storage primitive are deferred together; this slice does not expose a
scalar-placeholder substitute.

### 12.3 Snapshot and suffix rules

`SkillContentRevocationCursorV1` is a private-field, process-local cursor
containing the exact trust/Tenant/backend/owner-epoch scope plus:

```text
watermark
record_hash
```

The initial cursor is exactly watermark `0` plus
`SkillContentRevocationNoParentHash`. Every positive cursor is derived from a
fully validated record or a previously sealed page. There is no public scalar
constructor, hash-only constructor, nearest-watermark lookup, or cursor that
omits the predecessor hash.

`SkillContentRevocationPageRecordV1` is the exported, authority-free DTO a
cross-package repository adapter may return. It contains:

```text
deployment_trust_domain_id
tenant_id
source_backend_id
backend_owner_epoch
through_tail_watermark
through_tail_hash
after_watermark
after_hash
ordered_complete_persisted_records
next_watermark
next_hash
complete
```

`SkillContentRevocationPageViewV1` is the private-field page sealed by the
lifecycle coordinator after replaying that DTO. It retains the exact pinned
through-tail current ref, after cursor, defensively copied ordered typed
records, next cursor, and `complete` flag. Its validation rules are:

1. page scope, after cursor, and through-tail scope are exactly equal;
2. `after` is not later than `through_tail`, and the initial cursor uses the
   locked no-parent hash;
3. the first record has watermark `after + 1` and predecessor hash
   `after.record_hash`;
4. every subsequent record has the next contiguous watermark and the prior
   record's exact hash;
5. `next` equals `after` for an empty page or the final record's exact
   watermark/hash for a non-empty page;
6. no record or `next` passes the pinned through-tail;
7. `complete=true` if and only if `next` exactly equals the pinned
   through-tail watermark/hash;
8. if incomplete, the page contains exactly the requested limit; a byte limit
   that prevents this returns `ErrLimitExceeded` so the caller can retry with
   a smaller limit, rather than accepting a premature partial page;
9. a page after a complete page, a changed pinned tail, a scope change, an
   empty incomplete page, a gap, reorder, duplicate, or hash alias fails
   closed.

The high-level coordinator exposes
`LoadFirstRevocationPage(ctx, current_tail, limit)` and
`LoadNextRevocationPage(ctx, previous_incomplete_page, limit)`. Callers never
supply raw `after_exclusive` scalars. The first operation uses the exact
no-parent cursor; the second derives `after` and the immutable through-tail
from the sealed prior page. A future actual-use suffix reader may derive its
starting cursor only from validated frozen-Run revocation evidence, never from
an unsealed watermark/hash pair.

`SkillContentRevocationSnapshotViewV1` is a process-local private-field value
containing:

```text
DeploymentTrustDomainID
TenantID
SourceBackendID
BackendOwnerEpoch
Watermark
TailHash
exact typed target set through that tail
```

It can be constructed only by replaying a complete contiguous chain from the
locked no-parent hash through the exact repository current tail. Pagination
does not weaken completeness. Stage 3-A defines no checkpoint shortcut; a
future checkpoint/root format requires a new version. Missing pages,
resource-limit failure, or inability to prove the exact tail closes Skill
selection.

A new selection seals that complete snapshot and rejects any selected package
or generation target in the set.

A frozen Run records both watermark and tail hash, plus the backend/owner
scope. Before every later Skill content load, context projection, or model call
that would consume the Skill, the runtime must pin a newer exact local tail and
validate the contiguous suffix whose first predecessor is the frozen tail
hash:

- a matching package or generation target blocks use immediately;
- no unrelated package, nearest revision, CANARY, or prior generation is
  substituted;
- no model, Tool, or other external effect is semantically replayed;
- interrupted external effects continue through the existing
  DispatchAttempt/UNKNOWN reconciliation path.

`RETIRED` and `QUARANTINED` are lifecycle states, not revocations. They prevent
new target selection but do not rewrite immutable history. Only the permanent
revocation chain supplies the immediate actual-use kill switch.

## 13. Current resolution view

`CurrentSkillContentResolutionViewV1` is process-local and has no Restore,
canonical JSON, or public scalar constructor. It is built only from:

- one repository-verified current catalog activation;
- its complete exact activation target and generation;
- a once-validated generation index;
- repository-verified current package lifecycle refs for all selected entries;
- one complete current revocation snapshot.

Those parents are read together by one repository-adapter operation under one
consistent database snapshot. The adapter returns an exported, structural
`CurrentSkillContentClosureRecordV1` DTO so a Store implementation in another
Go package can construct it. That DTO has no private seal, authority methods,
current-view meaning, or runtime permission. The
`skillcontentlifecycle.LoadCurrentSkillContentClosure` coordinator revalidates
the complete DTO and only then seals a private
`RepositoryVerifiedSkillContentClosureV1`. The sealed view contains:

- the exact backend owner scope and read revision;
- the current activation token, activation, target, and complete generation;
- every selected package's raw current record, exact lifecycle transition,
  complete `CommittedSkillPublicationRecordV1`, and package evidence;
- the revocation tail token and complete snapshot at exactly the same
  watermark and tail hash.

The repository adapter must read all natural keys, owner epochs, current
pointers, target bindings, and snapshot watermark relationships inside that
read transaction and include the exact persisted values in the DTO. The
process-local coordinator distrusts the DTO, replays those relationships, and
revalidates every portable parent, recursively seals each committed review and
publication record, then seals current refs before constructing the generation
index and resolution view. A sequence of granular `Load*Current` calls is
diagnostic only and cannot construct the current resolution view, because it
would permit a package, activation, or revocation TOCTOU change between reads.

It provides exact availability queries and returns sealed membership/lifecycle
evidence. It does not perform Workspace/Agent/Profile/Task policy
intersection.

Every target entry must still name the exact current package lifecycle
ordinal/hash/state. If even one entry has advanced to a different transition,
including `QUARANTINED` or `RETIRED`, construction of the entire current
resolution view fails closed as stale. The resolver does not silently remove
that entry, substitute its new transition, or choose another revision. The
control plane must publish a new higher generation/target/activation before
new Runs can resume selection.

All of the following remain false:

```text
AuthorizesSelection
AuthorizesSkillLoading
AuthorizesRAGRetrieval
AuthorizesToolExecution
AuthorizesExternalEffect
```

Stage 3-B must combine this availability view with sealed owner contributions,
deny-first policy, required/optional semantics, budgets, and complete origin
sets before creating an attachment selection receipt.

## 14. Repository contracts and persistence boundary

This slice defines narrow repository/coordinator boundaries only:

- exact review/publication load and append;
- exact publication dedup lookup;
- exact package lifecycle load/append/current CAS;
- exact activation target and activation load/append/current CAS;
- revocation exact/tail/snapshot/suffix/append;
- atomic read APIs that return complete current closures.

The locked high-level coordinator operations are:

```text
CommitSkillReview(ctx, complete review command)
  -> CommittedSkillReviewViewV1

CommitSkillPublication(ctx, complete merge/publication command)
  -> CommittedSkillPublicationViewV1

LoadSkillReviewExact(ctx, tenant, proposal_id, result_hash)
  -> CommittedSkillReviewViewV1
LoadSkillPublicationExact(ctx, tenant, proposal_id, publication_hash)
  -> CommittedSkillPublicationViewV1
FindPublicationByPackageEvidence(ctx, tenant, evidence_hash)
  -> CommittedSkillPublicationViewV1
FindPublicationByPackageDigest(ctx, tenant, package_digest)
  -> CommittedSkillPublicationViewV1
FindPublicationByContentFingerprint(ctx, tenant, skill_id, fingerprint)
  -> CommittedSkillPublicationViewV1

LoadPackageLifecycleCurrent(ctx, trust, tenant, package_hash, backend, epoch)
  -> CurrentSkillPackageLifecycleRefV1
LoadPackageLifecycleExact(ctx, trust, tenant, package_hash, ordinal, hash)
  -> SkillPackageLifecycleTransitionV1
PublishCurrentPackageLifecycle(ctx, expected_current, next_transition)
  -> CurrentSkillPackageLifecycleRefV1

LoadCatalogActivationCurrent(ctx, trust, tenant, backend, epoch)
  -> CurrentSkillCatalogActivationRefV1
LoadCatalogActivationExact(ctx, trust, tenant, ordinal, hash)
  -> SkillCatalogActivationV1
PublishCurrentCatalogActivation(ctx, expected_current, next_activation)
  -> CurrentSkillCatalogActivationRefV1

LoadRevocationCurrent(ctx, trust, tenant, backend, epoch)
  -> CurrentSkillContentRevocationRefV1
LoadRevocationExact(ctx, trust, tenant, watermark, hash)
  -> SkillContentRevocationRecordV1
LoadFirstRevocationPage(ctx, current_tail, limit)
  -> SkillContentRevocationPageViewV1
LoadNextRevocationPage(ctx, prior_incomplete_page, limit)
  -> SkillContentRevocationPageViewV1
AppendRevocation(ctx, expected_tail, next_record)
  -> CurrentSkillContentRevocationRefV1

LoadCurrentSkillContentClosure(ctx, trust, tenant, backend, epoch)
  -> RepositoryVerifiedSkillContentClosureV1
```

Cross-package repository adapters do not return or construct those private
views. Their corresponding low-level operations return complete exported
structural DTOs:

```text
CommitSkillReviewRecord(ctx, complete review command)
  -> CommittedSkillReviewRecordV1
LoadSkillReviewRecordExact(ctx, tenant, proposal_id, result_hash)
  -> CommittedSkillReviewRecordV1

CommitSkillPublicationRecord(ctx, SkillPublicationCommitPlanV1)
  -> CommittedSkillPublicationRecordV1
LoadSkillPublicationRecordExact(ctx, tenant, proposal_id, publication_hash)
  -> CommittedSkillPublicationRecordV1
FindSkillPublicationRecordByPackageEvidence(ctx, tenant, evidence_hash)
  -> CommittedSkillPublicationRecordV1
FindSkillPublicationRecordByPackageDigest(ctx, tenant, package_digest)
  -> CommittedSkillPublicationRecordV1
FindSkillPublicationRecordByContentFingerprint(ctx, tenant, skill_id, fingerprint)
  -> CommittedSkillPublicationRecordV1

ReadPackageLifecycleCurrentRecord(ctx, exact local scope)
  -> SkillPackageLifecycleCurrentRecordV1
ReadPackageLifecycleRecordExact(ctx, exact portable identity)
  -> SkillPackageLifecyclePersistedRecordV1
PublishPackageLifecycleRecord(ctx, expected current, complete next command)
  -> SkillPackageLifecycleCommitRecordV1

ReadCatalogActivationCurrentRecord(ctx, exact local scope)
  -> SkillCatalogActivationCurrentRecordV1
ReadCatalogActivationRecordExact(ctx, exact portable identity)
  -> SkillCatalogActivationPersistedRecordV1
PublishCatalogActivationRecord(ctx, expected current, complete next command)
  -> SkillCatalogActivationCommitRecordV1

ReadRevocationCurrentRecord(ctx, exact local scope)
  -> SkillContentRevocationCurrentRecordV1
ReadRevocationRecordExact(ctx, exact portable identity)
  -> SkillContentRevocationPersistedRecordV1
ReadRevocationPageRecord(ctx, exact scope, through-tail cursor,
  after watermark/hash, limit)
  -> SkillContentRevocationPageRecordV1
AppendRevocationRecord(ctx, expected tail, complete next command)
  -> SkillContentRevocationCommitRecordV1

ReadCurrentSkillContentClosureRecord(ctx, trust, tenant, backend, epoch)
  -> CurrentSkillContentClosureRecordV1
```

The DTOs retain the complete persisted canonical documents and complete parent
material required by the relevant bridge/lifecycle coordinator. They are
deliberately forgeable structural transport values and implement no
`Authorizes*` method. A DTO never satisfies an API that requires
`CommittedSkillReviewViewV1`, `CommittedSkillPublicationViewV1`,
`CurrentSkillPackageLifecycleRefV1`,
`CurrentSkillCatalogActivationRefV1`, or
`RepositoryVerifiedSkillContentClosureV1`. Only the owning coordinator's
unexported sealing path may create those views after a full replay. This
two-layer boundary applies equally to future SQLite, remote, and in-memory
repository adapters; placing a Store implementation in the domain package is
not required.

`SkillPublicationCommitPlanV1` is a process-local private-field plan created
only by the bridge coordinator from a valid approved
`CommittedSkillReviewViewV1`, canonical merge command, exact expected package,
and authority. It has no Restore, public scalar constructor, canonical wire,
or authority method. The repository adapter receives that plan, locks and
loads the transaction facts, authenticates the merger, generates the commit
time, and invokes the plan's checked material builder *inside the same
transaction*. The builder returns the publication document/hash and the exact
three dedup keys to persist. The adapter does not reimplement the hash
algorithm, and the coordinator never creates the first publication evidence
after the transaction has committed.

Each high-level `Load*Current`, `PublishCurrent*`, and `AppendRevocation`
operation is a coordinator around its correspondingly named low-level
`*Record` operation. On success the adapter returns the exact persisted
immutable row plus the exact resulting current row in the commit DTO; the
coordinator revalidates both and seals the next private CAS ref. The adapter
never fabricates a private current token, and the coordinator never treats a
pre-commit command value as proof that the transaction committed.

Each exact `FindPublicationBy*` operation returns the same sealed committed
publication view as an exact load. No match returns `ErrNotFound`; two
different durable rows for one supposedly unique dedup key return
`ErrCorrupt`, never an arbitrary winner.
`SkillContentRevocationPageRecordV1` is structural transport only;
`SkillContentRevocationPageViewV1` is validated page history only. Neither can
construct a complete snapshot until the coordinator has replayed every sealed
page through the pinned exact tail.

The three `AdoptImported*` operations remain required before backend migration
or disaster-recovery cutover is production-capable, but they are deliberately
absent from this interface version. Their common repository-issued cutover
proof and source-zero/target-empty transaction semantics must be locked in the
later persistence slice before the operations are added. Imported history
therefore never becomes current through any operation listed above.

Natural keys are:

```text
review/publication: TenantID + ProposalID
package lifecycle: TrustDomain + TenantID + PackageEvidenceHash + Ordinal
package lifecycle current: TrustDomain + TenantID + PackageEvidenceHash
  + SourceBackendID + BackendOwnerEpoch
activation: TrustDomain + TenantID + ActivationOrdinal
activation current: TrustDomain + TenantID
  + SourceBackendID + BackendOwnerEpoch
revocation: TrustDomain + TenantID + Watermark
revocation target uniqueness: TrustDomain + TenantID + TargetKind + TargetHash
revocation current: TrustDomain + TenantID
  + SourceBackendID + BackendOwnerEpoch
```

`CommitSkillReview` is one atomic boundary over the existing submitted
proposal CAS, review row, resulting proposal, approved/blocked source
disposition, and exact review-result evidence. `CommitSkillPublication` is one
atomic boundary over approved-proposal CAS, merge row/state, source disposition
`MERGED`, all three content dedup keys, and publication evidence. Calling the
old generic governance mutation and appending evidence in a later transaction
is not a conforming production implementation.

The publication transaction order is logically:

```text
lock review/proposal/source
-> validate committed review, merger, authority, package, and expected version
-> generate published_at
-> use SkillPublicationCommitPlanV1 to build publication bytes/hash/keys
-> lock and claim exact-package-evidence, package-digest, and
   (TenantID, SkillID, revision-independent fingerprint) uniqueness
-> write merge row, proposal MERGED, source MERGED, publication, and all keys
-> commit
```

The adapter cannot inspect private plan fields before invoking the checked
builder, so it MUST NOT guess or reimplement dedup keys in order to lock them
earlier. The three key locks/claims happen only after the builder returns the
complete checked material inside the same serializable transaction. Exact
unique constraints remain mandatory: if a backend cannot lock a missing key
directly, the unique claim is the serialization point. Any error rolls back
every row. A byte-identical retry returns the original record and original
transaction time. Same proposal with changed command material is
`ErrImmutableConflict`; another proposal/source that aliases a package or
fingerprint is `ErrConflict`; dedup keys that point to different
publications, publication without MERGED state, partial keys, or MERGED state
without a publication are `ErrCorrupt` except for the explicitly classified
historical `ErrMergedButUnpublished` case. Two concurrent different sources
publishing the same fingerprint have one winner and one conflict, never two
successes or a false idempotent retry.

`PublishCurrentPackageLifecycle` performs owner-fence validation, exact
predecessor validation, terminal-state checks, immutable transition insert,
and backend-local current-history CAS in one serializable critical section.
The transition must not become visible if the current CAS or owner fence
fails, and the current token must not advance without the exact immutable
transition.

`PublishCurrentCatalogActivation` performs owner-fence validation, all package
lifecycle-current comparisons, revocation-tail/non-membership checks,
immutable inserts, and current-history CAS in one serializable critical
section. No check may occur before the transaction and then be assumed still
current.

`AppendRevocation` performs owner-fence validation, exact tail CAS, contiguous
watermark assignment, permanent target-uniqueness claim, immutable record
insert, and backend-local tail-history update in one serializable critical
section. A uniqueness, tail, or owner failure rolls back the record and tail
together. Byte-identical retry readback is evaluated against the committed
record; it is not a second append.

`LoadCurrentSkillContentClosure` uses one serializable read transaction or an
equivalent backend snapshot token that remains valid through the complete
read. It cannot assemble its return value from separately committed granular
loads. Any missing, changed, corrupt, or owner-mismatched member closes the
whole read.

All repositories share this typed error taxonomy:

```text
ErrNotFound
ErrConflict
ErrImmutableConflict
ErrCorrupt
ErrMergedButUnpublished
ErrOwnerFenceLost
ErrLifecycleStale
ErrRevoked
ErrLimitExceeded
```

Operational I/O errors are wrapped but never reclassified as corruption.
Exact idempotent retry compares the persisted canonical result and every
caller-supplied command field, including expected token, owner, authority,
actor, reason, and any caller-supplied lifecycle/activation/revocation time.
Review and publication commit times are generated inside their repository
transactions: they are persisted result fields, not caller command fields. An
idempotent retry must return the original persisted result and timestamp; it
must not generate a replacement time. Matching only a natural key or content
hash is insufficient.

Revocation history is not capped at 4096 total records. Each non-final page has
the requested `1..4096` records and must be contiguous from the exact
`after.watermark + 1`, with the first predecessor equal to
`after.record_hash`, through the lesser of the immutable pinned tail and page
limit. The first read pins an exact tail watermark/hash; subsequent pages use
that same tail and the prior sealed page's next cursor. A gap, reorder, wrong
predecessor, premature end, or different exact tail is corruption. A later
actual-use check pins a newer tail and scans only the new suffix. The common
4096 collection limit applies to one document/page, not to the lifetime
append-only history.

No Store evidence repository, SQL table, migration number, Runtime reader,
MemberSnapshot constructor, or context loader is added in Stage 3-A. The only
existing Store-path changes are the zero-write generic Skill review/merge
bypass guards above; they disable an unsafe path and do not persist or expose
new evidence.

Consequently this slice may claim:

- canonical portable evidence;
- typed CAS tokens;
- dependency and method-set boundaries;
- in-memory repository contract behavior;
- deterministic and race-safe process views.

It MUST NOT claim durable, restart-safe, linearizable production currentness
until a later migration lock defines physical keys, foreign keys, triggers,
transactions, backup closure, corruption detection, and cross-process
contention tests. Migration `0012` remains reserved for accounting.

## 15. Failure and retry semantics

- Missing or invalid required parents fail closed.
- Review, merge, lifecycle, activation, and revocation conflicts never fall
  back to a semantically similar Skill.
- A current CAS conflict requires reread and a newly authorized decision; it
  cannot silently reuse stale intent.
- Ambiguous local persistence may perform exact readback and byte comparison.
- Ambiguous external effects remain `UNKNOWN` and cannot be retried
  semantically.
- Portable history import does not make any record current.
- A rejected or blocked source is not reconsidered.
- A revoked package or generation is not un-revoked.

## 16. Acceptance gates

Before Stage 3-A lifecycle is complete:

1. literal canonical/hash golden vectors pass for every new domain;
2. strict New/Restore round trips require all complete parents;
3. proposal, proposal-domain-versus-manifest, reviewer, source, package, merge,
   owner, predecessor, target, lifecycle, rollback, Tenant, and trust-domain
   substitutions fail;
4. self-review, ineligible reviewer, APPROVED-without-MERGED, rejected
   publication, and blocked-source reuse fail; generic APPROVE, generic REJECT,
   and generic merge bypasses for a strict `SKILL_CANDIDATE_V1` each fail
   without mutating proposal/source/review/event state;
5. lifecycle transition graph and terminal-state negatives pass;
6. exact content/source dedup and idempotent retry/conflict behavior pass;
7. target ACTIVE/CANARY uniqueness and empty-generation cases pass;
8. imported portable history alone leaves every local current empty; adoption
   API/proof tests remain a mandatory gate of the later persistence/cutover
   slice and cannot be simulated with scalar placeholders here;
9. current CAS has one winner and rejects stale/partial/aliased tokens;
10. rollback uses a new higher generation and an exact prior semantic set;
11. package and generation revocation, broken chain, cursor-hash substitution,
    changed pinned tail, truncation, reordering, duplicate target, premature
    page completion, snapshot, and suffix checks pass;
12. exact index rejects every fallback and performs one full validation per
    seal rather than per lookup;
13. zero/forged views fail, returned values are defensive, and concurrent
    read tests pass under `-race`;
14. every authority-shaped query remains false;
15. AST dependency, consumer allowlist, and exported method-set tests pass;
16. Windows shuffle/vet, WSL race, and the existing non-SQLite repository
    regression set pass;
17. `pure_chat`, Runtime, MemberSnapshot, and context loading have no new
    production connection, and Store has only the zero-write generic Skill
    review/merge guards rather than an evidence repository;
18. a counting-spy proves that `pure_chat` performs zero catalog-current,
    package-lifecycle, and revocation repository calls even when those stores
    contain non-empty state.

Durable SQLite acceptance, restart recovery, backup/restore, trigger
immutability, cross-handle CAS, and corruption replay are later gates after a
separate persistence specification and migration assignment.

## 17. Effect on the original design

| Decision | Effect |
|---|---|
| Keep generation lifecycle-neutral | Positive: content inventory stays reusable and does not become hidden authority |
| Add a separate activation target | Positive: composite and specialist Agents can select exact permitted revisions without fixing knowledge into Agent identity |
| Require independent review then MERGED publication | Positive: preserves self-learning proposals without self-approval |
| Keep Operator activation separate from Reviewer eligibility | Positive: maintains operational control without allowing review bypass |
| Use source, exact-package, and revision-independent content dedup axes | Positive: prevents repeats and revision-only churn while preserving shared payload reuse |
| Separate portable history from backend-local current/adoption | Positive: preserves migration audit while preventing false cross-database CAS and split-brain current |
| Make CANARY explicit and deterministic | Neutral to positive: keeps optional experimentation without hidden traffic or token cost |
| Allow an explicit empty current target | Positive: permits safe modular shutdown without changing pure-chat semantics |
| Keep revocation separate and permanent | Positive: provides immediate safety without rewriting lifecycle or frozen evidence |
| Add a process-local exact index | Positive: removes repeated validation cost without adding currentness or permission |
| Defer Store/Runtime wiring | Neutral: capability remains unavailable longer, but no structural document is mistaken for production authority |

No decision in this lock embeds a Role, Persona, Memory, domain knowledge, or
Skill in the core Agent identity; requires Skill for ordinary chat; couples
the project to PAB; grants a Tool through Skill metadata; or reduces free
Agent/Workspace composition.
