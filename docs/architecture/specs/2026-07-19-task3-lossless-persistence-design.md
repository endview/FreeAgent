# Task 3 Lossless Assembly Persistence Design

> S0 状态：`HISTORICAL_NON_NORMATIVE`。仅供无损持久化语义回溯，不再指导当前实现；当前架构权威见本目录 `README.md`。

**Status:** frozen for implementation after independent review remediation

**Scope:** the first public release of `freeagent.io/v1alpha1`; no published
wire or database compatibility promise exists yet.

## 1. Outcome

`WorkspaceDefinition` and `AgentVersion` become the single immutable
authorities for assembly configuration. Existing Workspace and Team runtime
tables remain deterministic compatibility projections during migration; they
must never become a second source of truth.

The migration must preserve:

- optional Role, Knowledge, Memory, Skill, MCP, Tool, Team, and Sidecar
  bindings;
- pure chat with zero optional bindings;
- composite, specialist, and reviewer selection;
- per-domain capability, authority, and weighted aliases;
- Agent trust, cost weight, knowledge collection scope, and Tool allow/deny;
- exact Workspace membership, routes, revision, and ACL epoch;
- tenant isolation, immutable versions, restart recovery, and frozen Runs.

PAB is not part of this core. A future PAB module may bind through the same
generic module contract.

## 2. Pre-release public contract correction

The current `RoutingHints` shape is not lossless. A legacy domain is one
relation:

```text
domain -> capability + authority + aliases(text, weight)
```

Flattening these into independent arrays loses that relation and changes Team
selection. Before the first release, correct the alpha contract to:

```go
type WeightedAliasHint struct {
    Text   string  `json:"text"`
    Weight float64 `json:"weight"`
}

type DomainRoutingHint struct {
    Name       string              `json:"name"`
    Capability float64             `json:"capability"`
    Authority  float64             `json:"authority"`
    Aliases    []WeightedAliasHint `json:"aliases"`
}

type RoutingHints struct {
    Domains      []DomainRoutingHint `json:"domains"`
    Capabilities []WeightedHint      `json:"capabilities"`
}
```

Rules:

- domain names use a dedicated `DomainID`, not the Module capability grammar.
  Canonicalization exactly matches the existing Team contract: trim Unicode
  whitespace, split/collapse Unicode whitespace to one ASCII space, then apply
  Unicode lowercase. The result may contain Unicode, spaces, punctuation, and
  dots. It must be non-empty, valid UTF-8, at most 1024 bytes, and contain no
  non-whitespace control character. A v1 row outside this newly bounded public
  contract fails preflight with an exportable record identity; it is never
  rewritten or dropped;
- capability and authority are finite values in `[0,1]`;
- alias text uses the same existing Team text normalization, is valid UTF-8,
  non-empty after normalization, at most 4096 bytes, and contains no
  unsupported control character;
- alias weight is finite and non-negative; zero canonicalizes to one to
  preserve the existing Team contract;
- every zero-valued float canonicalizes to positive zero so `-0` and `0`
  cannot produce distinct Go values with the same RFC 8785 bytes;
- domains sort by name, aliases sort by text, and duplicates fail;
- domain values do not share a cross-domain total-weight ceiling;
- generic `Capabilities` retains the existing basis-point contract. It is a
  separate non-domain hint set and has no effect on legacy Team projection.

The old top-level `RoutingHints.Authority` and `RoutingHints.Aliases` are
removed before release because their semantics cannot be made equivalent to
the existing runtime.

## 3. Optional Role attachment

Persona does not become a core Agent field. A non-empty existing Persona maps
to exactly one optional attachment:

```text
freeagent.role.prompt@1
```

with canonical config:

```json
{
  "placement": "REPLY_PERSONA",
  "prompt": "...",
  "schema_version": 1
}
```

The config schema is closed. `prompt` follows the existing valid UTF-8,
control-character, and 8192-byte rules. An empty Persona produces no Role
attachment. A public Agent without this attachment has no Persona and remains
valid pure chat.

The attachment is `OPTIONAL` at Agent authoring level. Profiles decide whether
Role context is selected or required. Runtime context records the effective
Persona content hash.

Before any projected Agent can activate, the trusted catalog must contain the
exact `freeagent.role.prompt@1` manifest, its closed config schema, and a
runtime factory. During the transition that factory delegates to the current
Agent-role ContextContributor; it is not treated as installed merely because
the Ref appears in an Agent definition.

Tenant-materialized legacy public Profiles use an audited first-party
compatibility map:

```text
context.agent-role@1        -> freeagent.role.prompt@1
context.shared-knowledge@1  -> freeagent.knowledge.shared-rag@1
observer.memory-learning@1  -> freeagent.memory.learning@1
orchestrator.legacy-team@1  -> freeagent.team.composer@1
```

The mapped `freeagent.team.composer@1` manifest declares its exact Role
dependency on `freeagent.role.prompt@1`; it never continues to request
`context.agent-role@1`. Each mapped factory delegates to the existing
implementation until its native first-party adapter replaces it. The source
and target Ref, manifests, artifact digests, config hashes, and attestation
receipt are persisted in the compatibility link, so reverse projection can
recover the original Profile Ref without guessing. Unknown/operator module
Refs are not renamed.

## 4. Core authority corrections

Tool selection is an authority ceiling, not Tool implementation content.
Add the following optional-by-empty policy structure to both Workspace and
Agent binding policies:

```go
type ToolCapabilityID string

type ToolCapabilityPolicy struct {
    Allow []ToolCapabilityID `json:"allow"`
    Deny  []ToolCapabilityID `json:"deny"`
}

type PolicyMode string

const (
    PolicyInherit PolicyMode = "INHERIT"
    PolicyCeiling PolicyMode = "CEILING"
)

type AuthorityPolicy struct {
    Mode              PolicyMode             `json:"mode"`
    PermissionCeiling []moduleapi.Permission `json:"permission_ceiling"`
    EffectCeiling     moduleapi.EffectClass  `json:"effect_ceiling"`
    DataScopeCeiling  []string               `json:"data_scope_ceiling"`
    Budget            BudgetPolicy           `json:"budget"`
}

type WorkspaceBindingPolicy struct {
    // existing fields...
    Tools ToolCapabilityPolicy `json:"tools"`
}

type AgentBindingPolicy struct {
    // existing fields...
    Tools ToolCapabilityPolicy `json:"tools"`
}
```

`ToolCapabilityID` deliberately matches the current ToolGateway identifier
grammar: 1..128 ASCII bytes, first byte lowercase letter or digit, remaining
bytes lowercase letter, digit, dot, underscore, or hyphen. Lists are canonical
sets. Deny wins during intersection. A Tool or MCP module is still required
before a capability can actually execute.

`AuthorityPolicy.Mode` removes empty/zero ambiguity. `INHERIT` means this layer
adds no authority or budget restriction; all nested fields use one canonical
inert representation and are ignored during intersection. `CEILING` means
empty permission/data sets and zero budgets deny the corresponding resource,
as they do today. `EffectCeiling` is evaluated only in `CEILING` mode. Legacy
Workspace synthesis uses `INHERIT`; pure-chat and materialized legacy Profiles
use `CEILING`.

Knowledge collection identities use a dedicated validator rather than the
catalog identifier grammar. They are NFC, trimmed, valid UTF-8, contain no
control characters, and occupy 1..256 bytes. This preserves existing
identities such as `domain:backend`, `shared:core`, and
`workspace:workspace-a`; it stores references only, never knowledge bodies.

Workspace `KnowledgePolicy` and Agent knowledge policy each carry the same
explicit `Mode`. A legacy Workspace uses `INHERIT` with canonical empty lists.
A legacy Agent uses `CEILING` with its exact allowed collections; an empty
legacy Agent list therefore continues to grant no knowledge collection. New
policy intersection never infers grant semantics from an empty slice alone.

The separate general `AgentBindingPolicy.Authority` of a legacy Agent uses
`INHERIT` with the canonical inert nested representation. Legacy Agents had no
general permission, effect, data-scope, or budget layer; synthesizing
`CEILING` with empty/zero fields would silently deny behavior that was
previously governed by the Workspace, Profile, knowledge collection, and Tool
policies. This does not widen authority because those existing layers still
intersect normally.

## 5. Lossless compatibility projection

Legacy -> public:

1. map legacy Kind to exactly one bundled `freeagent.io/*` Team label and
   union it with non-conflicting TypeLabels;
2. copy trust and cost weight;
3. map every domain tuple to one `DomainRoutingHint`;
4. map Persona to the optional Role attachment;
5. map allowed collections to `Policy.KnowledgeCollections`;
6. map ToolPolicy to `Policy.Tools`;
7. canonicalize and hash the complete public AgentVersion.

Public -> legacy:

1. only an Agent with exactly one bundled Team label is legacy-composable;
2. reconstruct domains only from relation-preserving domain hints;
3. reconstruct Persona only from the exact Role attachment and closed config;
4. copy knowledge and Tool policy from their authoritative policy fields;
5. reject unsupported or ambiguous public forms instead of filling defaults
   or dropping data.

Legacy `Kind`-only, bundled-label-only, and Kind-plus-label documents are
behaviorally equivalent but have different legacy bytes. Migration preserves
the original canonical `spec_json` and `SpecHash` in an immutable,
public-hash-bound compatibility link for already-frozen Runs. It does not
pretend those representations are derivable from the public definition.

New public-to-legacy projections use exactly one deterministic representation:
the derived legacy Kind is set and the matching bundled label remains in
TypeLabels. Tests compare all behavioral fields and the deterministic new
projection hash. Existing frozen Runs continue to verify their original
source projection hash. The compatibility link is never editable and cannot
authorize a public mutation.

Public VersionID is an arbitrary exact version string. Persistence therefore
stores an explicit mapping to the legacy integer/version-row surrogate and
never parses or guesses an integer from VersionID.

> Stage 2 implementation note (2026-07-28): the paragraph below describes the
> target public-authoring identity. The current `TeamSnapshot` used as a V2
> compatibility witness still stores the opaque legacy `agent_versions` row
> ID, while `TaskAssemblySelectionV2` separately freezes the public VersionID
> and the exact approved mapping. Code must not compare those two identifiers
> directly. `RunManifestV2` closes the shared MemberID, AgentID, compatibility
> SpecHash and complete parent hashes; SelectionV2/Issuance close the exact
> public-to-legacy relationship. The final legacy-free Team/Selection family
> remains part of the RunManifestV3 cutover.

Every `team.TeamMember`, `team.TeamSnapshot`,
`appprofile.AgentVersionRef`, and `appprofile.RunManifest` freezes and verifies:

```text
tenant_id + agent_id + public version_id + AgentVersion.Hash
legacy projection SpecHash (compatibility evidence only)
```

The compatibility SpecHash is always recomputed from the public definition
for a new Run. A frozen v1 Run may retain its migration-bound original hash.
Object-level `LegacyAgentLink.Verify` proves only that one tuple is internally
consistent. Persistence therefore inserts the link exactly once under
`(tenant_id, agent_id, public_version_id)`, forbids UPDATE/DELETE, and rejects
same-identity replacement even when the source bytes, public definition, and
all hashes are changed together. Every Run independently freezes the public
AgentVersion hash and compatibility SpecHash and verifies both against that
insert-only row.

## 6. Workspace compatibility projection

The public WorkspaceDefinition is authoritative for Workspace assembly. The
tenant-owned user, external channel identity, and revocation catalogs remain
separate kernel identity overlays; they are referenced by Workspace members
but are not copied into WorkspaceDefinition.

Legacy -> public synthesis for the current revision is deterministic:

- name, members, routes, revision, and ACL epoch copy from the verified live
  Workspace/catalog projection;
- legacy `workspaces.tool_policy_json` maps to
  `binding_policy.tools`;
- ACL permission allow/deny are empty, meaning no additional permission
  change beyond the existing authenticated role and Tool policy;
- Knowledge, roster, retention, bindings, module/Skill/MCP allow/deny,
  SecretRefs, and data-scope ceilings are empty, meaning no additional
  restriction or default that did not exist in v1; the corresponding
  Workspace authority and knowledge modes are explicitly `INHERIT`;
- Team policy is the exact canonical projection of the current bundled Team
  policy;
- the current trusted Profile activation is linked separately rather than
  guessed from Workspace catalog JSON.

Public -> legacy rewrites only the compatibility fields above. Public fields
with no v1 projection remain authoritative public-only policy and are enforced
by the new assembly path; they are never discarded merely because the old
table cannot store them.

Workspace import writes the public definition, legacy live projection,
catalog version, identity-overlay changes, and activation in one transaction.
Load/start rechecks members, routes, name, revision, ACL epoch, and Tool policy
against the deterministic projection. Any drift fails closed.

Historical v1 Workspace revisions do not contain enough data to invent full
public definitions. Existing Tasks therefore bind honest migration evidence
to their exact canonical `TaskScope` and ProfileSnapshot/legacy-profile marker
rather than a fabricated historical WorkspaceDefinition.

## 7. Profile authority and compatibility

The current `appprofile.ProfileSnapshot` remains the authority for already
frozen v1 Runs. New Tasks use a tenant-materialized public
`AssemblyProfile`. The alpha contract is corrected before persistence:

- `BudgetPolicy` adds `max_model_calls` and `max_tool_calls`;
- `RequiredBinding` is renamed to `ModuleSelection`, and `Required` is renamed
  to `Selections`; `OPTIONAL` means `IF_PRESENT`, while `REQUIRED` means one
  valid matching concrete attachment must exist before model execution;
- `AssemblyProfile.DisabledModules` is `[]moduleapi.Ref`: disabling is an
  identity decision and never carries instance config, SecretRefs, or a
  runtime failure policy;
- enabled legacy bindings with real provider-valid default config map to
  `Defaults.Modules`;
- an enabled legacy `context.agent-role@1` binding instead maps to an
  `OPTIONAL` exact `freeagent.role.prompt@1` selection. The concrete Role
  attachment and prompt for this compatibility projection come only from the
  Agent;
- disabled legacy bindings map only their audited target identities to
  `DisabledModules`;
- permission/effect ceilings and model/tool/input/output/wall-time budgets map
  exactly; seconds convert to milliseconds with overflow checks;
- global legacy profiles are materialized per tenant with an immutable link to
  the source ProfileSpec hash and ProfileSnapshot hash.

Legacy Profile identity includes its catalog source scope. A global and a
tenant-owned source may therefore have the same Profile ID/version but
different immutable content. Materialization accepts an explicit arbitrary
public `VersionID` and stores a source-scope-to-public-version mapping; it
never reuses, parses, hashes, truncates, or guesses a public VersionID from the
legacy version. The compatibility link binds the exact GLOBAL or TENANT source
scope as well as both identities and hashes.

Enabled/default legacy bindings other than Role use empty `SecretRefs` and
`FailurePolicy=REQUIRED`. Disabled configs and the old Role binding config are
never runtime attachments; their exact canonical bytes and ConfigHash remain
only in the immutable, public-hash-bound compatibility link. Reverse
projection requires that link, the exact selection/default/disabled identity
projection, and all source config evidence before rebuilding the original
`Enabled` boolean and legacy ConfigHash.

For module resolution, `Defaults` is used only when a more-specific layer has
no concrete attachment for the same exact Ref. Agent concrete attachments
replace a Profile default as a whole; JSON configs are never field-merged.
Equal attachment digests may be deduplicated with multi-source provenance,
while two non-default attachments with the same Ref and different digests fail
closed. Disabled/deny wins. A Selection and DisabledModules entry for the same
exact Ref is rejected during authoring, and the current single-provider
capability model allows at most one Selection per capability.

This legacy Role rule is not a hard-coded global ban on Profile-authored Role
modules. A newly authored Profile may intentionally provide a provider-valid
Role default, and a third-party Role may declare its own supported attachment
scopes through the RuntimeCatalog. Such a default is explicit Profile
behavior, not a projection of legacy `{}` and not part of the zero-module
pure-chat Profile. The compatibility projector must never manufacture it.

Legacy `MaxModelCalls`, `MaxToolCalls`, `MaxInputTokens`, and
`MaxOutputTokens` copy exactly. `MaxWallTimeSeconds` converts to
`MaxWallTimeMS` by checked multiplication by 1000. `MaxTotalTokens` is the
checked sum of the two legacy token ceilings, which is the exact maximum the
two independent limits can permit. The absent legacy cost ceiling maps to
`MaxCostMicros=MaxJSONSafeInteger`, the declared neutral public ceiling.
Materialized Profile authority uses `Mode=CEILING`; no zero is interpreted as
unbounded.

The old module Refs remain valid typed legacy RuntimeCatalog entries during
transition. The four audited first-party mappings above are explicit
exceptions with reversible source links and equivalent compatibility
factories; no other Ref is silently renamed. New first-party profiles may
select the new Refs explicitly.

The public profile repository verifies canonical bytes/hash and the source
projection link. The legacy ProfileSpec/ProfileSnapshot can be reconstructed
and compared from enabled/disabled bindings, configs, and budgets. Existing
v1 Tasks use `LegacyTaskScope` plus `LegacyProfileSnapshot` evidence inputs;
new Tasks use `WorkspaceDefinition` plus `AssemblyProfile`. Both evidence
forms are explicit schema enum values and domain-separated hashes.

The exact Task evidence alternatives are:

```text
NEW:
  exactly one WorkspaceDefinition
  exactly one AssemblyProfile
  plus exactly one OperatorRequest iff source=OPERATOR_REQUEST
  plus exactly one TrustedClassifier iff source=TRUSTED_CLASSIFIER

LEGACY_BACKFILL:
  source=WORKSPACE_DEFAULT only
  exactly one LegacyTaskScope
  exactly one LegacyProfileSnapshot
  no new-definition, operator, or classifier input
```

`LegacyTaskScope` uses ID=TaskID, Version=`1`, and the verified existing
TaskScope `ScopeHash`. `LegacyProfileSnapshot` uses the exact profile ID and
version and its verified `SnapshotHash`; a `profile_legacy=1` marker is first
resolved to the frozen built-in `legacy_full` ProfileSnapshot and that exact
snapshot is persisted as evidence. Evidence `SourceHash` is
`moduleapi.Digest("freeagent.task-requirements-evidence-source.v1",
canonicalOrderedInputs)`. Mixed new/legacy pairs, additional inputs, or a
legacy pair claiming operator/classifier provenance fail closed.

## 8. Persistence model

Schema version 2 adds:

- immutable canonical config blobs;
- immutable WorkspaceDefinition versions;
- immutable AgentVersion versions and explicit legacy projection links;
- immutable definition-to-config references;
- immutable tenant-materialized AssemblyProfile versions and source links;
- append-only RuntimeCatalog generations, typed Module/Skill/MCP entries,
  activations, Run leases, and emergency revocations;
- immutable TaskRequirements evidence;
- append-only run-scoped MCP discovery locks.

All composite foreign keys are tenant-first:

```text
(tenant_id, workspace_id) -> workspaces(tenant_id, id)
(tenant_id, agent_id)     -> agent_identities(tenant_id, id)
```

The migration creates the matching unique Agent identity index first.

Repository writes accept only SDK-canonical values. Repository loads decode
again, regenerate canonical bytes, compare byte-for-byte, recompute the domain
hash, verify identity columns, and return defensive copies.

Every attachment config is canonicalized, credential-scanned, inserted into
the content-addressed blob store, and linked to its owning definition in the
same transaction. Inline public config remains part of the authoring hash;
runtime snapshots use verified `ConfigBlobRef` identities.

`Attachment.ConfigHash` is
`moduleapi.Digest(moduleapi.ConfigHashDomain, canonicalConfigBytes)`.
`ConfigBlobRef.SHA256` is the raw SHA-256 of those same canonical bytes and
`Size` is their byte length. The immutable link row binds owner kind/hash,
typed attachment key, domain-separated attachment ConfigHash, raw blob
SHA-256, and size. The repository rejects swapping or confusing the two hash
domains.

## 9. Transaction and migration boundaries

- Existing Workspace/Agent imports write the public authority, every config
  blob/link, the legacy projection, and activation state in one transaction.
- Failure at either side rolls back all sides and leaves no orphan blobs.
- Existing data that cannot be proven by the exact compatibility rules aborts
  the entire upgrade; there is no silent downgrade.
- Version 2 uses a runner-owned Go migration hook inside the migration
  transaction for canonical projection and TaskRequirements evidence that SQL
  cannot calculate.
- The hook runs after DDL and before fingerprint/metadata update.
- The runner acquires a true `BEGIN IMMEDIATE` transaction on the same
  foreign-key-enabled connection; a competing writer test proves isolation.
- Foreign keys are enabled and checked before commit.
- A failed hook leaves DDL, data, application ID, user version, metadata
  version, and fingerprint at valid version 1.

The v1 object manifest/fingerprint remains an immutable historical test
fixture. `PublicSchemaFingerprint` represents the complete current v2 schema.

## 10. Trusted TaskRequirements boundary

The Store owns an injected trusted compiler. In ingress, after TaskID,
WorkspaceDefinition, Profile, and authenticated operator/classifier inputs are
known, the Store invokes the compiler inside the same transaction and inserts
Task, TaskScope, and canonical TaskRequirements together.

The in-transaction compiler is pure, deterministic, bounded,
cancellation-aware, and performs no network, model, filesystem, Store, or
external Tool call. Any external classifier runs before the transaction and
produces authenticated immutable input; the compiler verifies that input and
binds its hash. It cannot create an external effect while holding SQLite's
single writer.

No Channel, model output, or generic setter may author or replace
TaskRequirements.

## 11. RuntimeCatalog startup boundary

Startup is two phase:

1. open, migrate, and verify the database;
2. bind persisted runtime identities to the command-owned installed factory
   registry, persist/verify bundled generation 1, load latest plus every
   generation with an active Run lease, recover Runs, then admit work.

The catalog includes typed `MODULE`, `SKILL`, and `MCP_SERVER` entries.
`SKILL` identity is `SkillRef.CanonicalKey`, including its content digest;
Skill revocation uses that exact typed identity and artifact/content digest.
Sidecars are `MODULE` entries whose runtime factory kind is `SIDECAR`; they do
not create a fourth authoring binding kind.

Unknown or unapproved providers fail before activation. An old Run never
substitutes a newer factory. Ordinary retirement drains leased generations;
emergency revocation is append-only and explicitly terminates affected Runs.

Bundled generation 1 bootstrap and v1 Run lease backfill are one startup
transaction. It:

1. persists and verifies generation 1;
2. classifies every existing Run by exact durable status;
3. attaches generation-1 ACTIVE leases to eligible non-terminal frozen Runs;
4. leaves terminal Runs and not-yet-frozen Runs unleased;
5. commits before recovery or task admission.

`FreezeRunSingle` and `FreezeRunTeam` persist a new ACTIVE lease in the same
transaction as the frozen RunManifest. Every durable terminal path releases
the lease exactly once. A failed freeze creates no lease. Reopen loads the
latest generation plus every actively leased historical generation. Missing
or digest-mismatched factories make only affected Runs explicitly unavailable
and are never replaced by a newer artifact.

## 12. Verification gates

Implementation is incomplete until tests prove:

- maximal legacy Agent semantic projection, all three Kind/label source
  representations, source-link verification, deterministic new projection,
  negative-zero normalization, explicit non-legacy-composable behavior, and
  rejection of same-identity full-tuple replacement when source bytes, public
  bytes, and every object-local hash are changed together;
- maximal Workspace projection including Tool policy and separate identity
  overlays, same-transaction projection with injected failures, and drift
  rejection for members/routes/ACL/name/Tool policy;
- tenant-materialized Profile enabled/disabled modules, call/token/time
  budgets, explicit neutral ceilings, compatibility Ref mapping, source links,
  canonical load, reverse reconstruction, and tamper rejection;
- immutable config blobs and definition links, including tamper detection;
- raw SHA versus domain-separated ConfigHash confusion, non-canonical,
  duplicate/trailing JSON, credential-scanner bypass, wrong size/bytes/hash,
  and defensive-copy tests;
- schema 1 -> 2 atomicity, rollback, reopen, and exact fingerprints;
- cross-tenant and unknown provider rejection;
- exact Role provider availability before Agent activation;
- exact closed Role config to compatibility ContextContributor,
  `REPLY_PERSONA` placement, and `EffectivePersonaHash`, including rejection
  of malformed, multiple, or wrong-version Role attachments;
- RuntimeCatalog generation CAS, artifact immutability, failed-freeze behavior,
  same-transaction Run leases, restart latest-plus-leased loading, exact-once
  release across every terminal path, retirement, and revocation;
- TaskRequirements trusted compilation, immutability, and load-time evidence;
- separate new and legacy TaskRequirements evidence cardinality/source/hash
  tests, including rejection of mixed evidence forms;
- two different Agents may independently use VersionID `v1`; arbitrary
  VersionIDs such as `2026.07+build` survive import, restart, load, and freeze
  without integer parsing or cross-Agent collision;
- one immutable AgentVersion may be referenced by two WorkspaceDefinitions
  with different Workspace bindings/policies, with no Agent mutation or
  cross-Workspace bleed;
- pure chat still runs with no Role, Knowledge, Memory, Skill, MCP, or
  Sidecar binding.

## 13. Effect on the original design

This design does not change the original product direction. It corrects a
pre-release lossy data shape and strengthens modularity:

- Persona remains an optional Role module;
- knowledge remains external and RAG-oriented;
- Skill and MCP remain independently attachable;
- Tool policy limits authority without embedding Tool implementations;
- domain routing metadata remains generic and usable by first- or third-party
  composers;
- the kernel owns only identity, authority, persistence evidence, effects,
  recovery, and frozen assembly.
