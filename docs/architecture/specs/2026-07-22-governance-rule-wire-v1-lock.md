# FreeAgent Governance rule wire v1 与成员治理解析锁定

> S0 状态：`HISTORICAL_NON_NORMATIVE`。仅供旧治理语义回溯，不再指导当前实现；当前架构权威见本目录 `README.md`。

状态：LOCKED（2026-07-22；模型/权限与内容/存储独立复审均为 0 Critical / 0 Important / 0 Minor）

## 1. 决策与原始设计影响

本文件只定义可恢复治理证据，不把治理目录、Skill 或内容选择器变成执行 Provider。
它保持：

- Agent、Workspace、Profile、Module、MCP、Skill 可自由组合；
- RuntimeCatalog membership 仍是 executable Provider 的唯一租户授权来源；
- Skill 只授内容，所需工具必须另有 Module/MCP 授权；
- `pure_chat` 明确冻结零 Provider、零 Provider Secret/Host；
- 不存在名称相似、版本邻近、全局 registry 或 wildcard Provider fallback。

为避免平台安全基线必须枚举未来每个第三方 Provider，v1 提供**显式 typed ANY
selector**。ANY 只表示一个治理规则适用于一类候选；最终 Provider identity、目录 membership、
Factory、Artifact 和成员绑定仍必须精确。所有匹配规则共同生效，没有 specificity
precedence，任一 DENY 胜，因此 ANY 不能覆盖 exact deny 或扩大目录权限。

## 2. 编码、限制与 hash

版本字段不是由 Go type 后缀猜测，固定矩阵如下：

| 家族 | `schema_version` | domain/type 说明 |
|---|---:|---|
| 本文件首次定义的 Governance leaf/scope/resolved/policy/binding/snapshot/proof/grant | 1 | `.v1` / `V1` |
| MCP Tool/Content Governance Mapping | 2 | `.v2` / `V2`，是 legacy mapping v1 的治理后继 |
| LockedMCP Governance Association | 1 | `.v1` / `V1`，首次定义 |
| 父规范的 PIA/MDA/CSA/Semantic/Evidence V2 | 2 | `.v2` / `V2`，既有 v1 的直接后继 |

所有文档：

- `schema_version` 必须逐文档等于上表；
- 使用 RFC 8785；数组必须存在，空集合为 `[]`，禁止 `null`；
- union 的非活动 object 分支必须编码 JSON `null`；
- Restore 拒绝未知/缺失/重复 key、尾随值、非 JCS bytes、stale hash；
- 所有进入 JSON 的整数（包括 `uint32`、`uint64`、byte/cost/time ceiling、generation、
  watermark、ordinal 和 count）都必须在 `[0, assembly.MaxJSONSafeInteger]`；版本、
  generation 和声明为正数的 limit 必须大于零；
- hash 为 64 个小写十六进制字符（32 bytes SHA-256）；
- 本文新增的 Governance/MappingV2/Association 文档，其自身 hash 的 preimage 删除且仅删除
  自身 hash 字段；嵌套对象保留其 hash。既有 RuntimeCatalog v1/Governance 4B1 继续使用各自
  已锁定的省略字段算法；既有 LockedMCP v3、Discovery 与 MCPToolApprovalMapping v1 继续
  使用“保留 JSON key、把自身 hash 置为空字符串”的既有算法；ToolGateway ArgsHash/
  ResultHash 继续使用无 domain 的 raw SHA-256。任何通用 helper 都不得跨这些契约重解释字节；
- slice/bytes/view 防别名；公开 authority view 使用私有字段和 getter。

固定限制：

```text
MaxPolicyLeafDocumentBytes       = 1 MiB
MaxResolvedPolicyDocumentBytes   = 4 MiB
MaxMemberGovernanceSnapshotBytes = 16 MiB
MaxResolutionScopeBytes          = 16 MiB
MaxGrantDocumentBytes            = 4 MiB
MaxDataScopeClauseSetBytes        = 4 MiB
MaxScopeUseContextBytes           = 256 KiB
MaxSourceAuthorityBytes           = 1 MiB
MaxRequestedBindingAuthorityBytes = 1 MiB
MaxResultClassifierDefinitionBytes = 1 MiB
MaxProjectionSourceJSONBytes       = 16 MiB
MaxRulesPerLeaf                  = 4096
MaxApplicableSources             = 4096
MaxPermissionsPerRule            = 256
MaxPermissionsPerGrant           = 4096
MaxScopeAlternativesPerClause    = 256
MaxDataScopeClauses              = 4096
MaxScopeAncestryEdgesPerPath     = 64
MaxScopeAncestryEdgesTotal       = 4096
MaxScopeAncestryProofBytes       = 4 MiB
MaxTagsPerRule                   = 64
MaxTagsPerSource                 = 256
MaxSourceTagBytes                = 136
MaxProjectionPointers            = 256
MaxCandidateProviders            = 4096
MaxCapabilitiesPerProvider       = 4096
MaxRequestedContentBindings      = 4096
MaxContentBindingContributionGenerationRefs = 5
MaxBindingOriginsPerBinding      = 64
MaxKnowledgeCollectionItemsPerVersion = 65536
MaxLegacyScopeIdentityBootstrapEntries = 65536
MaxResolvedRulesPerDocument      = 16384
MaxMatchedRulesPerGrant          = 4096
MaxSkillContentCatalogMemberships = 4096
MaxMCPToolGovernanceMappingEntries = 256
MaxMCPContentGovernanceMappingEntries = 16384
MaxMCPGovernanceMappingBytes     = 16 MiB
MaxLockedMCPAssociationBytes     = 64 KiB
MaxGovernanceJSONDepth           = 64
MaxGovernanceJSONNodes           = 262144
MaxIdentifierBytes               = 128
MaxOpaqueIdentityBytes           = 512
MaxMCPIdentityBytes              = assembly.MaxMCPDiscoveryEntryBytes
```

byte cap 分配固定为：四种 leaf 使用 MaxPolicyLeafDocumentBytes；四种 resolved component、
ResolvedMemberGovernancePolicy 与 RunMemberBinding 各使用 MaxResolvedPolicyDocumentBytes；
ResolutionScope 使用 MaxResolutionScopeBytes；GovernanceSnapshot/MemberSnapshotV2 使用
MaxMemberGovernanceSnapshotBytes；MatchedRuleSet/EvaluatedGrant 使用 MaxGrantDocumentBytes；
ClauseSet 使用 MaxDataScopeClauseSetBytes；ScopeUseContext 使用 MaxScopeUseContextBytes；source
object/scope/compatibility/locked-MCP-source authority 各使用 MaxSourceAuthorityBytes；binding origin/
authority/ref 使用 MaxRequestedBindingAuthorityBytes；Mapping/Association 使用其专用 cap。此外，
StaticCapability/SkillPackage/KnowledgeCollection evidence 使用 MaxSourceAuthorityBytes；
Tool activation/bridge 使用 MaxRequestedBindingAuthorityBytes。Candidate capabilities、binding origins、
content-binding contributions 与 knowledge collection item refs 分别使用上述专用 count cap。本文所有
新文档还同时受 MaxGovernanceJSONDepth/MaxGovernanceJSONNodes 约束。New/Restore 必须在 decode、
slice copy、sort/map allocation 与 JCS marshal 前执行相应 count/byte/depth/node preflight。
PermissionSet、Platform/Tenant/Run/Invocation-position authority、ReservationSemanticBinding、
Skill package/mapping/generation/ref、StaticCapabilityEvidence、MemberProviderSelectionPolicy、ResultClassificationAuthority 与
KnowledgeCollection item ref 也各自使用 MaxSourceAuthorityBytes；ResultClassifierDefinition 使用
MaxResultClassifierDefinitionBytes。OrderedCapabilities<=MaxCapabilitiesPerProvider、
OrderedBindingOrigins<=MaxBindingOriginsPerBinding、OrderedPermissions<=MaxPermissionsPerGrant、
OrderedResultClasses<=MaxTagsPerSource。不存在只依赖外围大文档 cap 的 standalone authority。
MCPContentAttachment、ContentBindingContribution authority/generation/generation-ref/contribution-ref
分别使用 MaxRequestedBindingAuthorityBytes；ResolutionScope generation refs 不超过
MaxContentBindingContributionGenerationRefs。
Legacy scope bootstrap entry/manifest 使用 MaxResolutionScopeBytes，entry count 使用
MaxLegacyScopeIdentityBootstrapEntries。
`MCPContentReadRequestIdentityV1` 使用 MaxSourceAuthorityBytes；其 canonical argument input 在 DOM
allocation 前也受该 cap 与 MaxGovernanceJSONDepth/MaxGovernanceJSONNodes 约束，实际 adapter payload
另按 Mapping 的 MaxRequestBytes 校验。

规则 ordinal 不由调用方决定：`New*PolicyDocument` 先对**完整 rule body**（删除且仅删除
`rule_ordinal` 与 `rule_hash`）做 RFC 8785 编码，再按该 UTF-8 byte string 升序排列，最后设置
`rule_ordinal = array index`。完全相同的完整 body 重复拒绝；相同 selector/effect 但不同
constraints 可以共存，并在 all-matches 中全部生效。Restore 要求 ordinal 精确等于 index，且
相邻 canonical body 严格递增。permission、tag、pointer、scope selector 和 exact scope alternative
都使用各自规范 byte key 严格升序、禁止重复；任何 Map 遍历或数据库默认 collation 均不得
决定 wire 顺序。这保证输入 shuffle 不改变 bytes/hash，同时不把不同 ceiling 错当成重复规则。
scope selector 按 `(DataScopeSelectorKind ordinal,SelectorHash)` 严格递增，语义重复或 hash 重复
均拒绝；grant 物化后的 exact alternative 再按 §3.1 的 identity tuple 排序。
Matched-rule ref 的唯一排序规则在 §10 定义为 source restriction tuple + RuleOrdinal，不使用
自身 JCS byte order。全部 aggregate count/byte limit 必须在复制 slice、排序、建 map 或编码
最终文档之前检查，不能靠最终 4/16 MiB marshal 才阻止巨大中间分配。

## 3. 通用类型

```go
type PolicyDocumentMode string // INHERIT | CEILING
type RuleEffect string         // ALLOW | DENY
type ApprovalRequirement string // NONE | REQUIRED
```

- `INHERIT` leaf 必须使用空规则/ceiling 数组；它不授予权限，只是不增加本层限制。
- `CEILING` leaf 的空规则/ceiling 是显式 deny-all。
- 至少一个适用 CEILING 必须明确允许候选；全 INHERIT 永远不能产生授权。
- Approval restriction order 固定为 `NONE < REQUIRED`，合并取最大。

Permission 沿用 `moduleapi.Permission`。多个匹配 ALLOW 的
`required_permissions` 取规范并集；最终必须是 Authority permission ceiling 的子集。
所有 permission 集合先按 `moduleapi.Permission` 的规范 ASCII/UTF-8 key 严格排序并拒绝重复，
单个 rule 不超过 MaxPermissionsPerRule，PermissionSet/Grant 的最终集合不超过
MaxPermissionsPerGrant。并集实现必须在每次插入前执行 aggregate count preflight，不能先分配
`MaxMatchedRulesPerGrant * MaxPermissionsPerRule` 再依赖文档 byte cap。随后形成：

```go
type PermissionSetV1 struct {
    SchemaVersion      uint32                 `json:"schema_version"`
    OrderedPermissions []moduleapi.Permission `json:"ordered_permissions"`
    PermissionSetHash  string                 `json:"permission_set_hash"`
}
```

`PermissionSetHash = H("freeagent.governance-permission-set.v1", JCS(body without
permission_set_hash))`。空权限集规范编码为 `ordered_permissions=[]`，仍产生非空 hash；所有父规范
`RequiredPermissionSetHash` 都指这一 exact wire，不能对逗号拼接字符串或 Go map 求 hash。

### 3.1 DataScope clause-set 与逐边 ancestry proof

```go
type DataScopeKind string
// PUBLIC | TENANT | WORKSPACE | AGENT | MEMBER | TASK | RUN | PROVIDER | SOURCE

type DataScopeIdentityAuthorityKind string
// PUBLIC_ROOT | TENANT_SCOPE_AUTHORITY | WORKSPACE_SCOPE_AUTHORITY | AGENT_SCOPE_AUTHORITY |
// RUN_MEMBER_BINDING | TASK_SCOPE | RUN_SCOPE_AUTHORITY | CANDIDATE_PROVIDER |
// EXACT_CONTENT_SOURCE

type DataScopeAlternativeV1 struct {
    ScopeKind             DataScopeKind                       `json:"scope_kind"`
    IdentityAuthorityKind DataScopeIdentityAuthorityKind      `json:"identity_authority_kind"`
    IdentityAuthorityHash string                              `json:"identity_authority_hash"`
    ScopeIdentityHash     string                              `json:"scope_identity_hash"`
}

type DataScopeSelectorKind string
// PUBLIC | CURRENT_TENANT | CURRENT_WORKSPACE | CURRENT_AGENT | CURRENT_MEMBER |
// CURRENT_TASK | CURRENT_RUN | CURRENT_PROVIDER | CURRENT_SOURCE

type DataScopeSelectorV1 struct {
    SelectorKind DataScopeSelectorKind `json:"selector_kind"`
    SelectorHash string                `json:"selector_hash"`
}

type DataScopeClauseOriginKind string // TOOL_RULE | CONTENT_RULE | AUTHORITY_DOCUMENT

type DataScopeClauseOriginV1 struct {
    OriginKind       DataScopeClauseOriginKind `json:"origin_kind"`
    SourcePolicyRef  SourcePolicyRefWireV1      `json:"source_policy_ref"`
    LeafDocumentHash string                     `json:"leaf_document_hash"`
    SourceRuleHash   *string                    `json:"source_rule_hash"`
    OriginHash       string                     `json:"origin_hash"`
}

type DataScopeClauseV1 struct {
    Origin                   DataScopeClauseOriginV1  `json:"origin"`
    OrderedAlternatives      []DataScopeAlternativeV1 `json:"ordered_alternatives"`
    ClauseHash               string                   `json:"clause_hash"`
}

type DataScopeClauseSetV1 struct {
    SchemaVersion         uint32              `json:"schema_version"`
    OrderedClauses        []DataScopeClauseV1 `json:"ordered_clauses"`
    DataScopeClauseSetHash string              `json:"data_scope_clause_set_hash"`
}
```

`DataScopeKind` 闭集与 wire ordinal 固定为：

```text
0 PUBLIC | 1 TENANT | 2 WORKSPACE | 3 AGENT | 4 MEMBER | 5 TASK | 6 RUN |
7 PROVIDER | 8 SOURCE
```

`DataScopeSelectorKind` 的 ordinal 按上面对应目标顺序固定为
`PUBLIC=0,CURRENT_TENANT=1,...,CURRENT_SOURCE=8`。`DataScopeKind.Validate/SortOrder` 与
`DataScopeSelectorKind.Validate/SortOrder` 是共享实现的唯一排序入口；Go 常量声明顺序、字符串
字典序和数据库 collation 都不构成 wire 语义。

Policy leaf 只保存 `DataScopeSelectorV1`，绝不保存依赖当前 Run/Member/Provider/Source 的 exact
alternative。selector 的 hash 使用 `freeagent.governance-data-scope-selector.v1` 覆盖删除且仅
删除自身 hash 的完整 body。Resolver 已经先形成 `RunMemberBinding` 后，才在某个 exact
Tool/Content grant 中把 selector 确定性物化为 `DataScopeAlternativeV1`；因此
`PolicyHash -> RunMemberBindingHash -> exact ClauseSet` 保持单向，不存在
`PolicyHash <-> RunMemberBindingHash` 环。`CURRENT_PROVIDER` 只允许用于有 exact Candidate 的
grant；`CURRENT_SOURCE` 只允许 Content grant；无法物化的 selector 使该 grant 失败关闭。
Tool/Content/Authority 中每个 `AllowedScopeSelectors` 数组最多
MaxScopeAlternativesPerClause 项，规范 key 固定为
`(DataScopeSelectorKind.SortOrder(), SelectorHash UTF-8 bytes)`；New 先拒绝相同完整 selector，再按
该 key 严格排序，Restore 要求已严格递增。不同 selector 不得因物化到同一 exact alternative 而
重复进入同一 clause；物化后按 alternative key 再拒绝重复。

identity 矩阵固定为：

| DataScopeKind | IdentityAuthorityKind | ScopeIdentityHash |
|---|---|---|
| PUBLIC | PUBLIC_ROOT | `H("freeagent.governance-public-data-scope.v1", {"schema_version":1})` |
| TENANT | TENANT_SCOPE_AUTHORITY | TenantScopeAuthorityHash |
| WORKSPACE | WORKSPACE_SCOPE_AUTHORITY | WorkspaceScopeAuthorityHash |
| AGENT | AGENT_SCOPE_AUTHORITY | AgentScopeAuthorityHash |
| MEMBER | RUN_MEMBER_BINDING | `H("freeagent.governance-member-scope.v1", {TenantID,RunID,MemberID,RunMemberBindingHash})` |
| TASK | TASK_SCOPE | TaskScopeHash |
| RUN | RUN_SCOPE_AUTHORITY | RunScopeAuthorityHash |
| PROVIDER | CANDIDATE_PROVIDER | CandidateHash |
| SOURCE | EXACT_CONTENT_SOURCE | ExactContentSourceIdentityV1.GovernedSourceIdentityHash |

IdentityAuthorityHash 必须等于该行不可变父文档的规范 hash；PUBLIC 的 authority 与 identity 都等于
固定 root。Verifier 必须接收对应 sealed authority view 并逐字段重算，不能只检查 64-hex。
一个 clause 内 alternatives 是 OR；所有
适用 clause 之间是 AND。alternative 按 `(scope_kind 的固定枚举序号,
scope_identity_hash, identity_authority_kind, identity_authority_hash 的 UTF-8 bytes)` 严格
排序；固定 identity matrix 还要求同一 kind/identity 不能以另一 authority 表示。clause 按
`(SourcePolicyRef restriction order/layer ordinal, OriginKind order,
leaf_document_hash, nullable source_rule_hash, clause_hash)` 严格排序。TOOL_RULE 与
CONTENT_RULE 的 SourceRuleHash 必须非 null 且 leaf hash 必须等于 SourcePolicyRef 对应字段；
AUTHORITY_DOCUMENT 要求 null，leaf hash 必须等于 AuthorityPolicyDocumentHash。解析不把
TENANT、WORKSPACE、RUN 等不同层级原子 hash 直接求交；它输出完整、非空
`DataScopeClauseSetV1`。deny-all 不伪造空 clause-set，而是不产生 grant。
OriginKind wire ordinal 固定为 `TOOL_RULE=0,CONTENT_RULE=1,AUTHORITY_DOCUMENT=2`；字符串
字典序、Go 常量声明顺序和数据库 collation 都不得替代该 ordinal。

用于 identity matrix 与 policy-layer scope 的三个先验父 authority 在任何 policy/grant 之前创建，
且都是独立 PA：

```go
type PlatformSafetyBaselineAuthorityV1 struct {
    SchemaVersion                       uint32 `json:"schema_version"`
    DeploymentTrustDomainID             string `json:"deployment_trust_domain_id"`
    BaselineID                          string `json:"baseline_id"`
    BaselineVersion                     uint64 `json:"baseline_version"`
    SourcePolicyVersion                 uint64 `json:"source_policy_version"`
    ToolPolicyDocumentHash              string `json:"tool_policy_document_hash"`
    ContentPolicyDocumentHash           string `json:"content_policy_document_hash"`
    AuthorityPolicyDocumentHash         string `json:"authority_policy_document_hash"`
    BudgetPolicyDocumentHash            string `json:"budget_policy_document_hash"`
    PlatformSafetyBaselineAuthorityHash string `json:"platform_safety_baseline_authority_hash"`
}

type ResultClassifierDefinitionV1 struct {
    SchemaVersion                uint32          `json:"schema_version"`
    DeploymentTrustDomainID      string          `json:"deployment_trust_domain_id"`
    ClassifierID                 string          `json:"classifier_id"`
    ClassifierVersion            string          `json:"classifier_version"`
    ClassifierKind               string          `json:"classifier_kind"`
    ImplementationArtifactDigest string          `json:"implementation_artifact_digest"`
    RulesetCanonicalBody         json.RawMessage `json:"ruleset_canonical_body"`
    RulesetHash                  string          `json:"ruleset_hash"`
    OrderedResultClasses         []string        `json:"ordered_result_classes"`
    ClassifierDefinitionHash     string          `json:"classifier_definition_hash"`
}

type TenantScopeAuthorityV1 struct {
    SchemaVersion            uint32 `json:"schema_version"`
    DeploymentTrustDomainID  string `json:"deployment_trust_domain_id"`
    TenantID                 string `json:"tenant_id"`
    TenantCreationOrdinal    uint64 `json:"tenant_creation_ordinal"`
    TenantScopeAuthorityHash string `json:"tenant_scope_authority_hash"`
}

type WorkspaceScopeAuthorityV1 struct {
    SchemaVersion               uint32 `json:"schema_version"`
    TenantID                    string `json:"tenant_id"`
    WorkspaceID                 string `json:"workspace_id"`
    WorkspaceCreationOrdinal    uint64 `json:"workspace_creation_ordinal"`
    TenantScopeAuthorityHash    string `json:"tenant_scope_authority_hash"`
    WorkspaceScopeAuthorityHash string `json:"workspace_scope_authority_hash"`
}

type AgentScopeAuthorityV1 struct {
    SchemaVersion            uint32 `json:"schema_version"`
    TenantID                 string `json:"tenant_id"`
    AgentID                  string `json:"agent_id"`
    AgentCreationOrdinal     uint64 `json:"agent_creation_ordinal"`
    TenantScopeAuthorityHash string `json:"tenant_scope_authority_hash"`
    AgentScopeAuthorityHash  string `json:"agent_scope_authority_hash"`
}

type WorkspaceDefinitionScopeAssociationV1 struct {
    SchemaVersion                     uint32 `json:"schema_version"`
    TenantID                          string `json:"tenant_id"`
    WorkspaceID                       string `json:"workspace_id"`
    WorkspaceDefinitionHash           string `json:"workspace_definition_hash"`
    WorkspaceScopeAuthorityHash       string `json:"workspace_scope_authority_hash"`
    WorkspaceScopeAssociationHash     string `json:"workspace_scope_association_hash"`
}

type AgentVersionScopeAssociationV1 struct {
    SchemaVersion                 uint32 `json:"schema_version"`
    TenantID                      string `json:"tenant_id"`
    AgentID                       string `json:"agent_id"`
    AgentVersionID                string `json:"agent_version_id"`
    AgentVersionHash              string `json:"agent_version_hash"`
    AgentScopeAuthorityHash       string `json:"agent_scope_authority_hash"`
    AgentScopeAssociationHash     string `json:"agent_scope_association_hash"`
}

type LegacyScopeIdentityBootstrapEntryV1 struct {
    SchemaVersion      uint32  `json:"schema_version"`
    IdentityKind       string  `json:"identity_kind"` // TENANT | WORKSPACE | AGENT
    TenantID           string  `json:"tenant_id"`
    WorkspaceID        *string `json:"workspace_id"`
    AgentID            *string `json:"agent_id"`
    ScopeAuthorityHash string  `json:"scope_authority_hash"`
    EntryHash          string  `json:"entry_hash"`
}

type LegacyScopeIdentityBootstrapManifestV1 struct {
    SchemaVersion        uint32 `json:"schema_version"`
    DeploymentTrustDomainID string `json:"deployment_trust_domain_id"`
    ManifestID           string `json:"manifest_id"`
    OrderedEntries       []LegacyScopeIdentityBootstrapEntryV1 `json:"ordered_entries"`
    OrderedEntriesRoot   string `json:"ordered_entries_root"`
    BootstrapManifestHash string `json:"bootstrap_manifest_hash"`
}

type LegacyScopeIdentityBootstrapConsumptionProofV1 struct {
    SchemaVersion                 uint32 `json:"schema_version"`
    DeploymentTrustDomainID       string `json:"deployment_trust_domain_id"`
    ManifestID                    string `json:"manifest_id"`
    BootstrapManifestHash         string `json:"bootstrap_manifest_hash"`
    OrderedEntriesRoot            string `json:"ordered_entries_root"`
    EntryCount                    uint32 `json:"entry_count"`
    BootstrapSequence             uint32 `json:"bootstrap_sequence"`
    ResultingScopeAuthoritySetRoot string `json:"resulting_scope_authority_set_root"`
    ConsumptionProofHash          string `json:"consumption_proof_hash"`
}

type LegacyScopeIdentityBootstrapClosedMarkerV1 struct {
    SchemaVersion           uint32 `json:"schema_version"`
    DeploymentTrustDomainID string `json:"deployment_trust_domain_id"`
    BootstrapSequence       uint32 `json:"bootstrap_sequence"`
    BootstrapManifestHash   string `json:"bootstrap_manifest_hash"`
    ConsumptionProofHash    string `json:"consumption_proof_hash"`
    State                   string `json:"state"`
    ClosedMarkerHash        string `json:"closed_marker_hash"`
}

type RunScopeAuthorityV1 struct {
    SchemaVersion         uint32 `json:"schema_version"`
    TenantID              string `json:"tenant_id"`
    TaskID                string `json:"task_id"`
    RunID                 string `json:"run_id"`
    TaskScopeHash         string `json:"task_scope_hash"`
    RunCreationOrdinal    uint64 `json:"run_creation_ordinal"`
    RunScopeAuthorityHash string `json:"run_scope_authority_hash"`
}

type InvocationCallPositionAuthorityV1 struct {
    SchemaVersion               uint32 `json:"schema_version"`
    TenantID                    string `json:"tenant_id"`
    TaskID                      string `json:"task_id"`
    RunID                       string `json:"run_id"`
    MemberID                    string `json:"member_id"`
    ParentCheckpointHash        string `json:"parent_checkpoint_hash"`
    PlanNodeID                  string `json:"plan_node_id"`
    CallOrdinal                 uint32 `json:"call_ordinal"`
    InvocationCallPositionHash  string `json:"invocation_call_position_hash"`
}

type InvocationSlotReservationAuthorityV1 struct {
    SchemaVersion               uint32 `json:"schema_version"`
    TenantID                    string `json:"tenant_id"`
    TaskID                      string `json:"task_id"`
    RunID                       string `json:"run_id"`
    MemberID                    string `json:"member_id"`
    InvocationSlotID            string `json:"invocation_slot_id"`
    InvocationCallPositionHash  string `json:"invocation_call_position_hash"`
    ParentCheckpointHash        string `json:"parent_checkpoint_hash"`
    PlanNodeID                  string `json:"plan_node_id"`
    CallOrdinal                 uint32 `json:"call_ordinal"`
    ReservationAuthorityHash    string `json:"reservation_authority_hash"`
}

type AdmissionKind string // BUSINESS_PROVIDER | RECONCILIATION_PROVIDER

type InvocationReservationSemanticBindingV1 struct {
    SchemaVersion                  uint32 `json:"schema_version"`
    TenantID                       string `json:"tenant_id"`
    TaskID                         string `json:"task_id"`
    RunID                          string `json:"run_id"`
    MemberID                       string `json:"member_id"`
    InvocationSlotID               string `json:"invocation_slot_id"`
    InvocationCallPositionHash     string `json:"invocation_call_position_hash"`
    ReservationAuthorityHash       string `json:"reservation_authority_hash"`
    AdmissionKind                  AdmissionKind `json:"admission_kind"`
    BindingAuthorityHash           string `json:"binding_authority_hash"`
    ReconciliationProviderRouteAuthorityHash string `json:"reconciliation_provider_route_authority_hash"`
    InvocationOperationKind        InvocationOperationKind `json:"invocation_operation_kind"`
    InvocationOperationBranchTupleHash string `json:"invocation_operation_branch_tuple_hash"`
    SemanticInvocationHash         string `json:"semantic_invocation_hash"`
    ReservationSemanticBindingHash string `json:"reservation_semantic_binding_hash"`
}
```

Platform baseline 使用 `freeagent.platform-safety-baseline-authority.v1`，覆盖删除且仅删除自身
hash 的完整 body。它是独立 root，不反向引用 policy generation；production verifier 只接受进程
启动前从只读部署配置加载并密封在 `TrustedPlatformSafetyBaselineAuthorityView` 中的完整
canonical bytes/hash 及四个 exact leaf document，运行时、租户或数据库 current 行均不能新增或
替换该清单。Authority 故意不保存 SourcePolicyDocumentHash：该 document 包含作为
ScopeIdentityHash 的 baseline authority hash，反向保存会形成 hash 环。每个租户的 PLATFORM
SourcePolicyDocument 必须以 `{TenantID,PLATFORM_SAFETY_BASELINE,baseline authority hash,
SourcePolicyVersion,四 leaf hash}` 唯一派生，SourcePolicyRef 再逐字段等于该 document；因此租户
既不能替换 leaf，也不能放宽 baseline。Tenant/Workspace/Agent scope authority 本身就是 exact、
insert-once creation authority；它们是稳定 ownership identity，不包含可变
definition/version hash。Workspace/Agent 的当前 Definition/Version 仍由 ResolutionScope 单独冻结，
并通过下面的 immutable Definition/Version↔Scope association 连接稳定 identity；不修改既有
assembly document bytes/hash。Run authority 复合引用同租户 TaskScope 与不可变
run-creation record（即 RunScopeAuthority 自身）。上述 authority 的 scope identity 就是各自 authority hash，不从 current 或
RunManifest 反推。Invocation position 的自然键固定为
`{TenantID,RunID,MemberID,ParentCheckpointHash,PlanNodeID,CallOrdinal}`。PlanNodeID 必须是
1..128 UTF-8 bytes 且匹配 `[A-Za-z0-9][A-Za-z0-9._:-]{0,127}`；CallOrdinal 是冻结 checkpoint
中从 0 连续的 uint32，且作为 JSON integer 不超过 `assembly.MaxJSONSafeInteger`。不存在另一个
字符串拼接形式的 CallPositionKey。position 在 Reservation 与 Semantic 之前 insert-once，二者都
引用它。Reservation authority 创建后不可变且不含 SemanticInvocationHash；Semantic 创建成功后
另行 insert-once 写入 `InvocationReservationSemanticBindingV1`，它复合引用同一 slot/position 的
Reservation 与 Semantic。Reservation authority 使用
`freeagent.invocation-slot-reservation-authority.v1`，完整 body 逐字段等于 position parent；它与
position 一样 insert-once，不保存 AttemptGeneration、状态、checkpoint CAS version 或任何
Semantic hash。上述可变字段只存在于由 ReservationAuthorityHash 锚定的 current/history ledger，
不进入 authority preimage；每次变更 CAS 并追加 history。position 永远不反向引用 Semantic，从而不存在
`Semantic -> ScopeUseContext -> Reservation -> Semantic` 环。binding 使用
`freeagent.governance-invocation-reservation-semantic-binding.v1`，同一 Reservation 与同一 Semantic
分别 UNIQUE；InvocationOperationKind 必须等于 concrete Semantic relation 的 TOOL_OPERATION 或
MCP_CONTENT_READ 分支，InvocationOperationBranchTupleHash 必须逐字段等于该 concrete Semantic 的
同名字段，并通过上述三列命名 parent 复合 FK 验证；禁止通过更新 Reservation 改写绑定。
该 V1 尚未发布过 canonical bytes，本轮首次冻结即包含 AdmissionKind、BindingAuthorityHash 与
ReconciliationProviderRouteAuthorityHash；不得按旧草案的缩短 struct 编码。三字段的 Tool/MCP Content/
business/reconciliation 判别矩阵，以及 `uq_business_pia_none_route_call_parent_v2`、
`uq_business_pia_catalog_route_call_parent_v2`、`uq_business_pia_core_route_call_parent_v2` 三个活动分支
parent 的适用矩阵和同列同序复合 FK，逐字采用父规范 §13.2.1，New/Restore 都必须重验。

稳定 scope creation registry 的唯一键固定为 Tenant
`{DeploymentTrustDomainID,TenantID}`、Workspace `{TenantID,WorkspaceID}`、Agent
`{TenantID,AgentID}`、Run `{TenantID,RunID}`；各 creation ordinal 为对应 registry/tenant 内由控制面
线性化分配的 JSON-safe 正整数，并各自 UNIQUE。Workspace/Agent 复合引用同 TenantID 的完整
TenantScopeAuthority；Run 复合引用同 TenantID/TaskID 的 TaskScope。只有认证后的 create/bootstrap
API 可 insert-once，production resolver 只接受 sealed creation resolution view，禁止接受裸 hash。
portable Restore 只恢复 audit PA，绝不替目标端创建 active identity；目标已有相同自然键时必须完整
bytes/hash 相等，否则 import 冲突。既有 legacy Tenant/Workspace/Agent 首次升级由一次性 bootstrap
manifest 枚举 exact ID 与预生成 scope authority hash。Entry 是 strict union：TENANT 两个 nullable
ID 都 null，WORKSPACE 只有 WorkspaceID 非 null，AGENT 只有 AgentID 非 null；kind ordinal
TENANT=0/WORKSPACE=1/AGENT=2，按 `(kind,TenantID,nullable ID,EntryHash)` 严格排序且自然身份/hash
分别唯一，最多 MaxLegacyScopeIdentityBootstrapEntries 条。OrderedEntriesRoot 使用独立
`freeagent.legacy-scope-identity-bootstrap-root.v1` 覆盖 ordered entry hashes；manifest/entry 各使用
同名 `.v1` domain。完整 manifest bytes/hash 必须在启动前部署 TCB allowlist，且逐项复合验证新
scope authority；该路径在任何 Run gate 打开前原子消费并写入完整
`LegacyScopeIdentityBootstrapConsumptionProofV1`。BootstrapSequence 固定为 1；EntryCount、root、
ManifestID/hash 必须逐字段等于 manifest，ResultingScopeAuthoritySetRoot 由所有实际插入的 exact
scope authority hashes 按 `(kind,TenantID,nullable ID,ScopeAuthorityHash)` 排序后使用
`freeagent.legacy-scope-identity-bootstrap-result-root.v1` 重算。result-root preimage 固定为
`JCS({"schema_version":1,"entry_count":N,"ordered_scope_authority_hashes":[...]})`；N 必须同时等于
manifest OrderedEntries 数、实际 insert count 与 proof.EntryCount，且从 manifest 每个 Entry 投影出的
expected authority hash 序列必须逐项等于实际插入序列后才能接受 root。proof 使用
`freeagent.legacy-scope-identity-bootstrap-consumption-proof.v1`；数据库对
DeploymentTrustDomainID、ManifestID、manifest hash 和 sequence 分别建立唯一约束，并在插入全部
authority、proof 与完整 `LegacyScopeIdentityBootstrapClosedMarkerV1` 的同一事务中提交。marker 的
State 固定为 `CLOSED`、BootstrapSequence 固定为 1，其他字段逐字段等于 proof/manifest，使用
`freeagent.legacy-scope-identity-bootstrap-closed-marker.v1`；DeploymentTrustDomainID 是 marker
自然主键并 UNIQUE，marker 以完整复合 FK 指向 proof。marker 是目标部署本地 D activation state，
不进入 portable root，imported marker/proof 绝不激活目标 registry。任何已有 proof/marker、
非 1 sequence、partial insert 或 root 不同都失败关闭。portable Restore 只恢复 audit PA，绝不消费或
关闭目标部署的 registry；之后永久关闭 bootstrap 写路径，不能按需为调用方补 identity。

Result classifier definition 使用 `freeagent.tool-result-classifier-definition.v1`，正文最多
MaxResultClassifierDefinitionBytes；RulesetCanonicalBody 必须是 RFC 8785 strict JSON object，
RulesetHash 按 `freeagent.tool-result-classifier-ruleset.v1` 重算，OrderedResultClasses 按 UTF-8
bytes 严格递增且无重复。v1 只接受闭集 `ClassifierKind=DETERMINISTIC_RULESET_V1`，其规范算法和
ImplementationArtifactDigest 必须由实现/golden 锁定。与 baseline 相同，production resolver 只可从
启动前密封的 `TrustedResultClassifierDefinitionView` 选择完整 definition；ResolutionScope、
RunMemberBinding、GovernanceSnapshot、MemberSnapshotV2 与 RunManifestV2 冻结同一个
ClassifierDefinitionHash，运行中不得读取 current classifier。

实际 Tool/Content 使用必须再保存以下 exact proof：

```go
type ScopeUseKind string // TOOL_OPERATION | CONTENT_SOURCE
type ScopeUsePositionKind string // INVOCATION_CALL_POSITION | MODEL_CALL_POSITION
type RequestedBindingPresence string
// PRESENT | NOT_APPLICABLE_TOOL_OPERATION | NOT_APPLICABLE_TOOL_RESULT

type RequestedBindingWitnessV1 struct {
    SchemaVersion        uint32                   `json:"schema_version"`
    Presence             RequestedBindingPresence `json:"presence"`
    RequestedBindingHash *string                  `json:"requested_binding_hash"`
    WitnessHash          string                   `json:"witness_hash"`
}

type ScopeUseContextV1 struct {
    SchemaVersion                      uint32               `json:"schema_version"`
    TenantID                           string               `json:"tenant_id"`
    TaskID                             string               `json:"task_id"`
    RunID                              string               `json:"run_id"`
    MemberID                           string               `json:"member_id"`
    MemberSnapshotV2Hash               string               `json:"member_snapshot_v2_hash"`
    RunMemberBindingHash               string               `json:"run_member_binding_hash"`
    GovernanceSnapshotHash             string               `json:"governance_snapshot_hash"`
    FrozenGovernanceRevocationWatermark uint64              `json:"frozen_governance_revocation_watermark"`
    MemberScopeIdentityHash            string               `json:"member_scope_identity_hash"`
    RunScopeIdentityHash               string               `json:"run_scope_identity_hash"`
    AgentScopeIdentityHash             string               `json:"agent_scope_identity_hash"`
    TaskScopeIdentityHash              string               `json:"task_scope_identity_hash"`
    WorkspaceScopeIdentityHash         string               `json:"workspace_scope_identity_hash"`
    TenantScopeIdentityHash            string               `json:"tenant_scope_identity_hash"`
    UseKind                            ScopeUseKind         `json:"use_kind"`
    UsePositionKind                    ScopeUsePositionKind `json:"use_position_kind"`
    UsePositionHash                    string               `json:"use_position_hash"`
    ExactCandidateHash                 *string              `json:"exact_candidate_hash"`
    SourceKind                         *SourceKind          `json:"source_kind"`
    GovernedSourceIdentityHash         *string              `json:"governed_source_identity_hash"`
    RequestedBindingWitness            RequestedBindingWitnessV1 `json:"requested_binding_witness"`
    ScopeUseContextHash                string               `json:"scope_use_context_hash"`
}

type ScopeProofSubjectKind string // MEMBER_CONTEXT | EXACT_PROVIDER | EXACT_SOURCE

type ScopeRelationshipKind string
// MEMBER_IN_RUN | MEMBER_REALIZES_AGENT | RUN_FOR_TASK | TASK_IN_WORKSPACE |
// WORKSPACE_IN_TENANT | TENANT_UNDER_PUBLIC

type ScopeRelationshipAuthorityKind string
// RUN_MEMBER_BINDING | MEMBER_SNAPSHOT | RUN_SCOPE_AUTHORITY | TASK_SCOPE |
// WORKSPACE_SCOPE_AUTHORITY | AGENT_SCOPE_AUTHORITY | TENANT_SCOPE_AUTHORITY

type ScopeAncestryEdgeV1 struct {
    EdgeOrdinal              uint32                         `json:"edge_ordinal"`
    Child                    DataScopeAlternativeV1         `json:"child"`
    Parent                   DataScopeAlternativeV1         `json:"parent"`
    RelationshipKind         ScopeRelationshipKind          `json:"relationship_kind"`
    RelationshipAuthorityKind ScopeRelationshipAuthorityKind `json:"relationship_authority_kind"`
    RelationshipAuthorityHash string                         `json:"relationship_authority_hash"`
    EdgeHash                 string                         `json:"edge_hash"`
}

type ScopeAncestryPathV1 struct {
    ActualScope              DataScopeAlternativeV1 `json:"actual_scope"`
    AllowedAlternative       DataScopeAlternativeV1 `json:"allowed_alternative"`
    OrderedEdges             []ScopeAncestryEdgeV1  `json:"ordered_edges"`
    PathHash                 string                 `json:"path_hash"`
}

type DataScopeClauseSatisfactionV1 struct {
    ClauseOrdinal             uint32              `json:"clause_ordinal"`
    ClauseHash                string              `json:"clause_hash"`
    MatchedAlternativeOrdinal uint32              `json:"matched_alternative_ordinal"`
    SubjectKind               ScopeProofSubjectKind `json:"subject_kind"`
    ActualScope               DataScopeAlternativeV1 `json:"actual_scope"`
    Path                      ScopeAncestryPathV1 `json:"path"`
    SatisfactionHash          string              `json:"satisfaction_hash"`
}

type ScopeAncestryProofV1 struct {
    SchemaVersion          uint32                           `json:"schema_version"`
    TenantID               string                           `json:"tenant_id"`
    ScopeUseContext        ScopeUseContextV1                `json:"scope_use_context"`
    DataScopeClauseSetHash string                           `json:"data_scope_clause_set_hash"`
    OrderedSatisfactions   []DataScopeClauseSatisfactionV1  `json:"ordered_satisfactions"`
    ScopeAncestryProofHash string                           `json:"scope_ancestry_proof_hash"`
}
```

每个 clause 必须恰好有一个 satisfaction，ordinal 与 clause-set index 相等；匹配
alternative 必须逐字段等于该 clause 中对应 index。每个 satisfaction 的 SubjectKind 与
ActualScope 不是调用方选择，而是由 matched alternative kind 和 ScopeUseContext 严格派生：

- PROVIDER：`EXACT_PROVIDER`，ActualScope 等于
  `{PROVIDER,CANDIDATE_PROVIDER,*ExactCandidateHash,*ExactCandidateHash}`；candidate 必须存在；
- SOURCE：`EXACT_SOURCE`，ActualScope 等于
  `{SOURCE,EXACT_CONTENT_SOURCE,*GovernedSourceIdentityHash,*GovernedSourceIdentityHash}`；source 必须存在；
- PUBLIC/TENANT/WORKSPACE/AGENT/MEMBER/TASK/RUN：`MEMBER_CONTEXT`，ActualScope 等于
  `{MEMBER,RUN_MEMBER_BINDING,RunMemberBindingHash,MemberScopeIdentityHash}`。

path 的首节点必须等于 satisfaction.ActualScope，末节点必须等于匹配 alternative；两者相等时
`ordered_edges=[]`，否则至少一
条且不超过 `MaxScopeAncestryEdgesPerPath`。edge ordinal 连续，前一 parent 必须逐字段等于
后一 child；每种 `RelationshipKind` 只接受下面固定的 child/parent kind 和 authority origin：
所有 path 的 edge 总数不得超过 MaxScopeAncestryEdgesTotal，proof 规范字节不得超过
MaxScopeAncestryProofBytes；两项在复制 path/edge 前检查。

| RelationshipKind | child → parent | authority kind |
|---|---|---|
| MEMBER_IN_RUN | MEMBER → RUN | RUN_MEMBER_BINDING |
| MEMBER_REALIZES_AGENT | MEMBER → AGENT | MEMBER_SNAPSHOT；parent=AgentScopeAuthorityHash |
| RUN_FOR_TASK | RUN → TASK | RUN_SCOPE_AUTHORITY |
| TASK_IN_WORKSPACE | TASK → WORKSPACE | TASK_SCOPE；parent=WorkspaceScopeAuthorityHash |
| WORKSPACE_IN_TENANT | WORKSPACE → TENANT | WORKSPACE_SCOPE_AUTHORITY |
| TENANT_UNDER_PUBLIC | TENANT → PUBLIC | TENANT_SCOPE_AUTHORITY；PUBLIC identity 还必须等于固定 root |

PROVIDER/SOURCE 只允许 exact identity 空路径，不允许伪造“来源已被成员使用”或“来源来自某
Provider”的 ancestry 边。来源归属、Provider binding、实际检索/调用结果由
EvaluatedGrant、MemberSnapshotV2、Mapping/Association 与 leaf evidence 另行复合验证；它们
不能反过来证明 DataScope 允许使用。这样共享 RAG/Memory 来源也不会因一次正在构造的 use
authority 被错误提升为成员私有来源。
WorkspaceDefinition/AgentVersion 只证明当前配置版本与稳定 identity 的关联，不能替代 ancestry
终点。MemberSnapshotV2 与 TaskScope 必须显式冻结对应 stable authority hash，Verifier 再复合
验证 immutable scope association 中 definition/version 的 WorkspaceID/AgentID 与 creation authority
相同；版本升级不改变
scope identity，也不为旧内容静默生成新的 owner。

本 DataScope 的 PUBLIC..RUN alternatives 明确定义为“本次访问者/运行上下文允许在哪里使用”，
不是来源所有权的隐式推断。来源自身的 owner/scope 由预先存在的
ContentSourceScopeAuthorityV1 验证；requested binding/MemberSnapshot 再证明该来源 family 可被
本成员装配。两条链都成立后才能创建 grant/use authority，任何一条都不能替代另一条。

ScopeUseContext 使用 `freeagent.governance-scope-use-context.v1`，必须先于 proof 存在。TOOL_OPERATION
要求 position=INVOCATION_CALL_POSITION、candidate 非 null、source 两字段为 null，binding witness
为 NOT_APPLICABLE_TOOL_OPERATION/null；
CONTENT_SOURCE 要求 position=MODEL_CALL_POSITION、source kind/governed identity 非 null，
Skill/Knowledge/Memory 的 candidate 为 null，MCP 三类与 ToolResult 的 candidate 非 null；前六类
可预装来源的 binding witness 必须为 PRESENT/non-null，ToolResult 必须为
NOT_APPLICABLE_TOOL_RESULT/null。WitnessHash 使用
`freeagent.governance-requested-binding-witness.v1` 覆盖 presence 与 nullable hash；null 只能用于
上述两个 typed absence，不能用空字符串。position hash 必须关闭到
已存在的 InvocationCallPositionAuthority 或 ModelCallPosition authority，不能引用正在构造的
Reservation/Semantic/CSA。
所有 scope identity、snapshot/binding/watermark 与 candidate/source 字段必须逐字段关闭到提供的
不可变父文档。

可信 verifier 必须按 `RelationshipAuthorityKind` 读取调用方已经提供的不可变父文档，并逐字段
重算 child、parent 和 authority hash；proof 自己不能声明一条没有父证据的边。production
验证器不得读取 current、不得用字符串前缀、名称相等或数据库包含关系猜 ancestry。
同一 proof 的全部 relationship authority 文档必须属于同一 Tenant/Run/Member 闭包；跨租户
边永远失败。实际使用证据必须同时绑定 `DataScopeClauseSetHash` 与
`ScopeAncestryProofHash`，不能继续只保存一个含义不明的 `DataScopeHash`。

domains：

```text
freeagent.governance-data-scope-clause.v1
freeagent.governance-data-scope-clause-origin.v1
freeagent.governance-data-scope-clause-set.v1
freeagent.governance-member-scope.v1
freeagent.governance-tenant-scope-authority.v1
freeagent.governance-run-scope-authority.v1
freeagent.governance-invocation-call-position.v1
freeagent.invocation-slot-reservation-authority.v1
freeagent.governance-invocation-reservation-semantic-binding.v1
freeagent.mcp-content-canonical-arguments.v1
freeagent.mcp-content-no-arguments.v1
freeagent.mcp-content-canonical-request.v1
freeagent.mcp-content-read-request-identity.v1
freeagent.mcp-content-read-semantic-invocation.v2
freeagent.legacy-scope-identity-bootstrap-result-root.v1
freeagent.legacy-scope-identity-bootstrap-consumption-proof.v1
freeagent.legacy-scope-identity-bootstrap-closed-marker.v1
freeagent.governance-requested-binding-witness.v1
freeagent.governance-scope-use-context.v1
freeagent.governance-scope-ancestry-edge.v1
freeagent.governance-scope-ancestry-path.v1
freeagent.governance-data-scope-clause-satisfaction.v1
freeagent.governance-scope-ancestry-proof.v1
freeagent.governance-permission-set.v1
```

## 4. Provider 与 operation selector

```go
type ProviderMatchMode string
// ANY_MODULE | EXACT_MODULE | ANY_MCP_SERVER | EXACT_MCP_SERVER

type ProviderSelectorV1 struct {
    MatchMode         ProviderMatchMode     `json:"match_mode"`
    ExactModuleRef    *moduleapi.Ref        `json:"exact_module_ref"`
    ExactMCPServerRef *assembly.MCPServerRef `json:"exact_mcp_server_ref"`
    SelectorHash      string                `json:"selector_hash"`
}
```

ANY 模式两个 exact 分支均为 null；EXACT_MODULE 只允许 ModuleRef；
EXACT_MCP_SERVER 只允许 MCPServerRef。Provider kind 直接来自 RuntimeCatalog
`RuntimeKind`，不另造第二套 kind。

```go
type OperationMatchMode string
// ANY_MODULE_CAPABILITY | EXACT_MODULE_CAPABILITY |
// ANY_MCP_TOOL | EXACT_MCP_TOOL

type ModuleCapabilityOperationV1 struct {
    Capability moduleapi.Capability `json:"capability"`
}

type MCPToolOperationV1 struct {
    Name string `json:"name"`
}

type OperationSelectorV1 struct {
    MatchMode            OperationMatchMode                `json:"match_mode"`
    ExactModuleCapability *ModuleCapabilityOperationV1      `json:"exact_module_capability"`
    ExactMCPTool          *MCPToolOperationV1               `json:"exact_mcp_tool"`
    SelectorHash          string                            `json:"selector_hash"`
}
```

Provider 与 operation kind 必须对应。MCP name 逐字节复用 LockedMCP v3
`validMCPDiscoveryName` 的 UTF-8/size 语义；不得 trim、URI-normalize 或额外缩到 128 bytes。
Stage 1 发现后的 descriptor/input-schema digest 继续由 LockedMCP/Mapping 追加，预发现政策
不能猜测。
`OperationSelectorV1` 的 null 矩阵锁定为：ANY_MODULE_CAPABILITY 与 ANY_MCP_TOOL 的两个 exact
分支都为 null；EXACT_MODULE_CAPABILITY 仅 module 分支非 null；EXACT_MCP_TOOL 仅 MCP 分支非
null。Provider/operation family 不一致在 New 与 Restore 都立即拒绝。

domains：

```text
freeagent.governance-provider-selector.v1
freeagent.governance-operation-selector.v1
```

## 5. Tool source policy wire

```go
type ToolAllowConstraintsV1 struct {
    RequiredPermissions []moduleapi.Permission  `json:"required_permissions"`
    MaxEffectClass      moduleapi.EffectClass   `json:"max_effect_class"`
    ApprovalRequirement ApprovalRequirement     `json:"approval_requirement"`
    AllowedScopeSelectors []DataScopeSelectorV1 `json:"allowed_scope_selectors"`
    MaxRequestBytes     uint64                  `json:"max_request_bytes"`
    MaxResultBytes      uint64                  `json:"max_result_bytes"`
    BudgetCategory      string                  `json:"budget_category"`
    MCPExecution        *MCPToolExecutionConstraintsV1 `json:"mcp_execution"`
}

type MCPToolExecutionConstraintsV1 struct {
    OperatorApprovalRevision string                        `json:"operator_approval_revision"`
    DeliverySemantics        assembly.MCPDeliverySemantics `json:"delivery_semantics"`
    LocalGatewayToolName     string                        `json:"local_gateway_tool_name"`
    LocalGatewayRevision     string                        `json:"local_gateway_revision"`
}

type ToolPolicyRuleV1 struct {
    RuleEffect        RuleEffect              `json:"rule_effect"`
    ProviderSelector  ProviderSelectorV1      `json:"provider_selector"`
    OperationSelector OperationSelectorV1     `json:"operation_selector"`
    AllowConstraints  *ToolAllowConstraintsV1 `json:"allow_constraints"`
    RuleOrdinal       uint32                  `json:"rule_ordinal"`
    RuleHash          string                  `json:"rule_hash"`
}

type ToolPolicyDocumentV1 struct {
    SchemaVersion          uint32             `json:"schema_version"`
    PolicyMode             PolicyDocumentMode `json:"policy_mode"`
    OrderedRules           []ToolPolicyRuleV1 `json:"ordered_rules"`
    ToolPolicyDocumentHash string             `json:"tool_policy_document_hash"`
}
```

DENY 必须 `allow_constraints=null`；ALLOW 必须非 null，两个 byte limit 为正且 JSON-safe，
scope selectors 非空。MODULE rule 的 `mcp_execution=null`；MCP rule 可以为 null，使平台
ANY safety ceiling 不必预知每个第三方工具，但一个最终获准的 exact MCP tool 必须从全部
匹配 ALLOW 中得到至少一个非 null 值，且所有非 null 值完全相等，否则失败关闭。
`OperatorApprovalRevision` 使用现有 `moduleapi.Ref.Version` 语法，DeliverySemantics 只能是
`NATIVE_IDEMPOTENT | RECONCILABLE | DUPLICATE_POSSIBLE`。远端 MCP tool name 继续使用
LockedMCP identity；`LocalGatewayToolName/Revision` 必须复用 ToolGateway 同源窄 validator：
name 1..128 bytes、revision 1..64 bytes，首字符 `[a-z0-9]`，后续 `[a-z0-9._-]`。
二者不得互相替代或靠名称相等猜映射。多个命中 ALLOW
若 operator revision、delivery semantics 或本地 gateway identity 不完全相等，求值失败；
它们不能由 MCP annotation、模型输出或 current gateway 临时生成。

现有 Gateway 当前只激活 `READ_ONLY + Approval NONE`；v1 policy 可以冻结更窄或面向未来的
ceiling，但 Stage 2 在 Gateway 没有对应可执行能力时必须失败关闭，不能把“可表达”标成“已可
执行”。完整 rule body 重复按 §2 拒绝，ANY 与 EXACT 可共存并全部参与 all-matches。
在独立可选 Approval 模块尚未定义 exact ApprovalGrant/subject/scope/expiry/revocation authority 前，
所有 `ApprovalRequirement=REQUIRED` 的 Module tool、MCP tool 与 MCP content read 只能被解析和审计，
进入任何 PREWIRE/读取时一律返回 `APPROVAL_MODULE_UNAVAILABLE`。UI 点击、布尔参数或模型文本不
构成批准，且不能静默降为 NONE。
现有 `mcphost` adapter 对远端 tool name 还只接受最多 128-byte ASCII
`[A-Za-z0-9_.-]`；LockedMCP/治理 wire 仍无损保留其 64 KiB UTF-8 identity，但超出 adapter
当前能力的名称在 Stage 2 必须明确 `UNSUPPORTED_ACTIVATION` fail closed，直到 adapter 使用
assembly 同源 validator 后才可宣称执行支持。
Tool rule 的 scope selectors 只允许 PUBLIC/CURRENT_TENANT/CURRENT_WORKSPACE/CURRENT_AGENT/
CURRENT_MEMBER/CURRENT_TASK/CURRENT_RUN/CURRENT_PROVIDER；CURRENT_SOURCE 在 leaf 构造时直接
拒绝，不能拖到 PREWIRE 才发现无法证明。

domains：

```text
freeagent.governance-tool-policy-rule.v1
freeagent.governance-tool-policy-document.v1
```

## 6. Content selector 与 source policy wire

### 6.1 Skill 唯一 membership identity

```go
type SourceTagSetV1 struct {
    SchemaVersion    uint32   `json:"schema_version"`
    OrderedTags      []string `json:"ordered_tags"`
    SourceTagSetHash string   `json:"source_tag_set_hash"`
}

// Owned by a neutral lower package (sdk/catalogref), not internal/governance.
type SkillCatalogVersionRefV1 struct {
    SkillID     string `json:"skill_id"`
    Revision    uint64 `json:"revision"`
    ContentHash string `json:"content_hash"`
}

type SkillPackageEvidenceV1 struct {
    SchemaVersion          uint32                  `json:"schema_version"`
    TenantID               string                  `json:"tenant_id"`
    SkillCatalogVersionRef catalogref.SkillCatalogVersionRefV1 `json:"skill_catalog_version_ref"`
    PackageDigest          string                  `json:"package_digest"`
    ManifestDigest         string                  `json:"manifest_digest"`
    SourceTagSet           SourceTagSetV1          `json:"source_tag_set"`
    ContentPayloadHash     string                  `json:"content_payload_hash"`
    SkillPackageEvidenceHash string                `json:"skill_package_evidence_hash"`
}

type SkillPublicInternalRefMappingV1 struct {
    SchemaVersion            uint32                  `json:"schema_version"`
    TenantID                 string                  `json:"tenant_id"`
    AssemblySkillRef         assembly.SkillRef       `json:"assembly_skill_ref"`
    SkillCatalogVersionRef   catalogref.SkillCatalogVersionRefV1 `json:"skill_catalog_version_ref"`
    SkillPackageEvidenceHash string                  `json:"skill_package_evidence_hash"`
    MappingHash              string                  `json:"mapping_hash"`
}

type SkillContentCatalogMembershipV1 struct {
    SchemaVersion            uint32                  `json:"schema_version"`
    TenantID                 string                  `json:"tenant_id"`
    MembershipOrdinal        uint32                  `json:"membership_ordinal"`
    AssemblySkillRef         assembly.SkillRef       `json:"assembly_skill_ref"`
    SkillCatalogVersionRef   catalogref.SkillCatalogVersionRefV1 `json:"skill_catalog_version_ref"`
    SkillPackageEvidenceHash string                  `json:"skill_package_evidence_hash"`
    CompatibilityMapping     SkillPublicInternalRefMappingV1 `json:"compatibility_mapping"`
    SourceTagSetHash         string                  `json:"source_tag_set_hash"`
    ContentPayloadHash       string                  `json:"content_payload_hash"`
    MembershipHash           string                  `json:"membership_hash"`
}

type SkillContentCatalogGenerationMemberV1 struct {
    MembershipOrdinal uint32 `json:"membership_ordinal"`
    MembershipHash    string `json:"membership_hash"`
}

type SkillContentCatalogGenerationV1 struct {
    SchemaVersion      uint32                                  `json:"schema_version"`
    TenantID           string                                  `json:"tenant_id"`
    Generation         uint64                                  `json:"generation"`
    OrderedMemberships []SkillContentCatalogGenerationMemberV1 `json:"ordered_memberships"`
    GenerationHash     string                                  `json:"generation_hash"`
}

type SkillContentCatalogMembershipRefV1 struct {
    SchemaVersion                 uint32            `json:"schema_version"`
    TenantID                      string            `json:"tenant_id"`
    SkillContentCatalogGeneration uint64            `json:"skill_content_catalog_generation"`
    SkillContentCatalogHash       string            `json:"skill_content_catalog_hash"`
    MembershipOrdinal             uint32            `json:"membership_ordinal"`
    MembershipHash                string            `json:"membership_hash"`
    SkillPackageEvidenceHash      string            `json:"skill_package_evidence_hash"`
    AssemblySkillRef              assembly.SkillRef `json:"assembly_skill_ref"`
    SkillCatalogVersionRef        catalogref.SkillCatalogVersionRefV1 `json:"skill_catalog_version_ref"`
    CompatibilityMapping          SkillPublicInternalRefMappingV1 `json:"compatibility_mapping"`
    SourceTagSetHash              string            `json:"source_tag_set_hash"`
    ContentPayloadHash            string            `json:"content_payload_hash"`
    MembershipRefHash             string            `json:"membership_ref_hash"`
}
```

`SkillPackageEvidenceV1` 使用 `freeagent.skill-package-evidence.v1`，复合引用同租户不可变
skillcatalog package/version row 与 ContentPayload；PackageDigest/ManifestDigest、完整
SourceTagSet/Hash 和 ContentPayloadHash 都属于 parent UNIQUE 与 portable relation。这样
`SkillPackageEvidenceHash` 不再是调用方可自报的裸 hash。
`catalogref.SkillCatalogVersionRefV1` 位于不 import governance/skillcatalog 的 neutral lower
package；它与当前 `skillcatalog.VersionRef` 三字段 byte-identical，唯一 conversion bridge 逐字段
验证并有双向 canonical round-trip/golden。`internal/governance` 不得 import skillcatalog，避免
`skillcatalog -> governance -> skillcatalog` 包环。
实施时把当前 `internal/skillcatalog/governance.go` 中依赖 governance 的 proposal adapter 上移到
`internal/skillgovernancebridge`，使 `internal/skillcatalog` 不再 import governance；另由上层
`internal/governanceresolution` 同时 import governance wire 与 skillcatalog sealed resolution view，
构造/验证 SkillPackageEvidence。neutral scalar ref 单独不构成 parent closure，production constructor
不得绕过这个双父 bridge。

Skill SourceTagSet 不接受调用方 metadata。它只能从已验证 SkillVersion.Manifest 确定性投影：每个
规范 Domain 生成 `domain:<value>`，每个规范 Keyword 生成 `keyword:<value>`，合并后按原始 UTF-8
bytes 严格排序、拒绝重复，数量仍受 MaxTagsPerSource 限制。Package evidence 的完整 tags/hash 必须
等于该投影；MaxSourceTagBytes=136 保证现有 128-byte keyword 加 8-byte `keyword:` 前缀仍可表达，
同一 SkillVersion 不能包装另一组 tags 来命中 Content rule。

`CompatibilityMapping.MappingHash` 使用
`freeagent.skill-public-internal-ref-mapping.v1` 覆盖 tenant、两个 ref 与 package evidence；禁止仅凭
ID/name 猜映射。Membership 使用
`freeagent.skill-content-catalog-membership.v1`；generation 使用
`freeagent.skill-content-catalog-generation.v1`；ref 使用
`freeagent.skill-content-catalog-membership-ref.v1`。generation 的 membership ordinal 必须
从零连续且等于数组 index，MembershipHash 唯一；ref 必须逐字段等于 generation 对应 ordinal
引用的完整 membership，TenantID/generation/hash 必须等于 ResolutionScope 冻结的 Skill
anchor。构造 generation 前，先按删除 `membership_ordinal`/`membership_hash` 的完整 membership
body JCS bytes 排序，再分配从 0 连续的 ordinal 并计算 MembershipHash；完整 body 重复、
AssemblySkillRef 重复或 SkillCatalogVersionRef 重复均拒绝，成员数不得超过
MaxSkillContentCatalogMemberships。Restore 必须同时接收完整 membership documents，验证其
排序/ordinal/hash 与 generation 的 OrderedMemberships 一一相等；只给 member hash 数组不能
恢复 authority。`SourceTagSetHash` 与 `ContentPayloadHash` 还必须闭合到不可变 package/content evidence。
该 membership ref 只证明内容目录成员关系，不产生工具或进程执行权；Skill 中描述的脚本仍
必须通过另一个精确 Module/MCP Provider 授权和 Gateway 执行。

### 6.2 SourceTargetSelector 严格 union

```go
type SkillMatchMode string // ANY_SKILL | EXACT_SKILL
type KnowledgeMatchMode string // ANY_KNOWLEDGE | EXACT_KNOWLEDGE
type MemoryMatchMode string // ANY_MEMORY | EXACT_MEMORY
type MCPResourceMatchMode string // ANY_MCP_RESOURCE | EXACT_MCP_RESOURCE
type MCPResourceTemplateMatchMode string
// ANY_MCP_RESOURCE_TEMPLATE_RESULT | EXACT_MCP_RESOURCE_TEMPLATE_RESULT
type MCPPromptMatchMode string // ANY_MCP_PROMPT | EXACT_MCP_PROMPT
type ToolResultMatchMode string // ANY_TOOL_RESULT | EXACT_TOOL_RESULT

type SkillTargetSelectorV1 struct {
    MatchMode       SkillMatchMode                           `json:"match_mode"`
    ExactMembership *SkillContentCatalogMembershipRefV1      `json:"exact_membership"`
}

type KnowledgeExactIdentityV1 struct {
    CollectionID       string `json:"collection_id"`
    CollectionVersion  uint64 `json:"collection_version"`
    SourceID           string `json:"source_id"`
    SourceRevisionHash string `json:"source_revision_hash"`
}

type KnowledgeTargetSelectorV1 struct {
    MatchMode KnowledgeMatchMode        `json:"match_mode"`
    Exact     *KnowledgeExactIdentityV1 `json:"exact"`
}

type MemoryOwnerKind string // AGENT | WORKSPACE
type MemoryScopeKind string // PRIVATE | WORKSPACE | TENANT

type MemoryExactIdentityV1 struct {
    OwnerKind        MemoryOwnerKind `json:"owner_kind"`
    OwnerIdentityHash string         `json:"owner_identity_hash"`
    ScopeKind        MemoryScopeKind `json:"scope_kind"`
    ScopeIdentityHash string         `json:"scope_identity_hash"`
    RecordKind       string          `json:"record_kind"`
}

type MemoryTargetSelectorV1 struct {
    MatchMode MemoryMatchMode        `json:"match_mode"`
    Exact     *MemoryExactIdentityV1 `json:"exact"`
}

type MCPResourceExactIdentityV1 struct {
    ProviderSelector ProviderSelectorV1 `json:"provider_selector"`
    URI              string             `json:"uri"`
}

type MCPResourceTargetSelectorV1 struct {
    MatchMode MCPResourceMatchMode         `json:"match_mode"`
    Exact     *MCPResourceExactIdentityV1  `json:"exact"`
}

type MCPTemplateResultExactIdentityV1 struct {
    ProviderSelector ProviderSelectorV1 `json:"provider_selector"`
    URITemplate      string             `json:"uri_template"`
}

type MCPTemplateResultTargetSelectorV1 struct {
    MatchMode MCPResourceTemplateMatchMode        `json:"match_mode"`
    Exact     *MCPTemplateResultExactIdentityV1   `json:"exact"`
}

type MCPPromptExactIdentityV1 struct {
    ProviderSelector ProviderSelectorV1 `json:"provider_selector"`
    Name             string             `json:"name"`
}

type MCPPromptTargetSelectorV1 struct {
    MatchMode MCPPromptMatchMode        `json:"match_mode"`
    Exact     *MCPPromptExactIdentityV1 `json:"exact"`
}

type ToolResultExactIdentityV1 struct {
    ProviderSelector  ProviderSelectorV1  `json:"provider_selector"`
    OperationSelector OperationSelectorV1 `json:"operation_selector"`
    ResultClass       string              `json:"result_class"`
}

type ToolResultTargetSelectorV1 struct {
    MatchMode ToolResultMatchMode        `json:"match_mode"`
    Exact     *ToolResultExactIdentityV1 `json:"exact"`
}

type SourceTargetSelectorV1 struct {
    SourceKind SourceKind `json:"source_kind"`
    Skill      *SkillTargetSelectorV1 `json:"skill"`
    Knowledge  *KnowledgeTargetSelectorV1 `json:"knowledge"`
    Memory     *MemoryTargetSelectorV1 `json:"memory"`
    MCPResource *MCPResourceTargetSelectorV1 `json:"mcp_resource"`
    MCPResourceTemplateResult *MCPTemplateResultTargetSelectorV1 `json:"mcp_resource_template_result"`
    MCPPrompt  *MCPPromptTargetSelectorV1 `json:"mcp_prompt"`
    ToolResult *ToolResultTargetSelectorV1 `json:"tool_result"`
    SelectorTargetHash string `json:"selector_target_hash"`
}
```

`SourceKind` 必须与恰好一个非 null branch 对应，另外六个 branch 明确编码 `null`。每个
ANY branch 的 `exact=null`，每个 EXACT branch 必须非 null；MCP exact identity 的
ProviderSelector 必须是 `EXACT_MCP_SERVER`。`EXACT_TOOL_RESULT` 不是宽约束 tuple：MODULE 分支
必须同时使用 `EXACT_MODULE + EXACT_MODULE_CAPABILITY`，MCP 分支必须同时使用
`EXACT_MCP_SERVER + EXACT_MCP_TOOL`；两个 nested ANY 或 family 不一致在 New/Restore 均拒绝。
`ANY_TOOL_RESULT` 的 Exact 必须 null。
Knowledge collection version 必须为 JSON-safe 正数；本 v1 明确拒绝 `OwnerKind=USER`：当前
DataScope 没有 authenticated-user identity/relationship，不能把 PRIVATE user memory 偷换为
MEMBER 或 AGENT。未来可以由独立、可选的 user-identity 模块在新 schema 中增加该 scope，不会
把它固化进核心。Tenant/Workspace/Agent/Task/Run/Member、collection/source/record 等已有身份字段
必须复用其父 authority 的 exact validator，不得由本包定义更宽 grammar。本文新引入的
Memory RecordKind、ResultClass、budget/local symbolic name 使用 1..`MaxIdentifierBytes` bytes、
ASCII 小写 dotted grammar：首尾为 `[a-z0-9]`，中间只允许 `[a-z0-9._-]`，禁止连续/空 dot
segment；NFC/trim/control 检查仍执行。只有明确标为 opaque 的既有外部 identity 才使用
1..`MaxOpaqueIdentityBytes`、有效 UTF-8、NFC、trim 不变、无 control 的 grammar，不能靠显示名相等。
DeploymentTrustDomainID、BaselineID、ClassifierID 与 ContentBindingContribution symbolic ID（若
未来新增）复用上述 1..MaxIdentifierBytes 小写 dotted grammar；ClassifierVersion 复用现有
`moduleapi.Ref.Version` 的 1..64-byte validator。ImplementationArtifactDigest、RulesetHash 与所有
authority hash 均为 64 个小写 hex 字符，不得用 identifier validator 代替。
SourceTagSet 的 tag 为 1..MaxSourceTagBytes UTF-8 bytes、NFC、trim 不变且无 control；按原始 UTF-8 bytes
严格排序并拒绝重复，数量不得超过 `MaxTagsPerSource`。空 tag set 编码 `[]` 且仍有非空规范 hash。Skill membership 和所有
Knowledge source evidence 保存/引用完整 SourceTagSetV1，不能只信任临时 metadata。

Governance 实现不得复制 LockedMCP 的私有 validator。实施 4B2 前先在 `sdk/assembly` 增加
只读窄 API，分别验证并返回不可伪造的 ToolName、ResourceURI、ResourceTemplate 和 PromptName
identity view，但不改 LockedMCP v3 bytes/hash：

- Tool/Prompt name 与 Resource URI：逐字 UTF-8、最多
  `assembly.MaxMCPDiscoveryEntryBytes`，不 trim、不 URI normalize、不要求 absolute URI；
- ResourceTemplate：1..2048 bytes、trim/NFC/control 检查，并通过现有 RFC 6570 parser；
- discovered descriptor、input-schema、argument-contract digest 不属于 pre-discovery selector；
  Stage 1 求值后由 LockedMCP catalog 与 Governance MappingV2 追加并闭合。

### 6.3 匹配约束、投影与 rule

Tags 不进入 `SelectorTargetHash`：

```go
type ContentMatchConstraintsV1 struct {
    RequiredTags []string `json:"required_tags"`
}
```

仅 Skill/Knowledge 可使用非空 tags。规则匹配要求 RequiredTags 是来源规范 tag set 的子集。
来源必须保存 `SourceTagSetHash`，并由 Content source authority/provenance 绑定；恢复时重算，
不能信任临时 metadata。

Projection 只允许两个确定性算法：

```go
type ProjectionAlgorithm string
// IDENTITY_UTF8_V1 | RFC6901_JSON_POINTER_SET_TO_RFC8785_V1

type ProjectionPolicyV1 struct {
    Algorithm       ProjectionAlgorithm `json:"algorithm"`
    OrderedPointers []string            `json:"ordered_pointers"`
    PolicyHash      string              `json:"projection_policy_hash"`
}
```

- IDENTITY_UTF8_V1：输入必须有效 UTF-8，输出是完全相同 bytes，pointers 必须 `[]`。
- RFC6901...：输入必须是一个 strict JSON value；pointer 严格遵守 RFC 6901。New 先拒绝
  重复再按原始 UTF-8 bytes 排序；Restore 只接受已严格递增且无重复的数组。root pointer `""`
  合法，但出现时必须是唯一 pointer。缺失 pointer
  失败。非 root 输出为按 pointer 顺序的
  `[{"pointer":<pointer>,"value":<selected-value>},...]` RFC 8785 bytes；root 输出为选中
  value 的 RFC 8785 bytes。

RFC6901 算法在 JSON parse/DOM allocation 前先要求 source bytes 同时不超过 MaxSourceBytes 与
MaxProjectionSourceJSONBytes，并用流式 preflight 限制 depth<=MaxGovernanceJSONDepth、
nodes<=MaxGovernanceJSONNodes；object member 与 array element 都计入 nodes。任一超限都在构造
projection evidence 前失败，不得先完整反序列化后再依赖 MaxProjectedBytes。

不再提供含义不确定的 TEXT_ONLY/STRUCTURED_FIELDS 标签。投影允许输出大于来源，两个
独立上限分别检查，不要求 `max_projected_bytes <= max_source_bytes`。

```go
type ContentAllowConstraintsV1 struct {
    RequiredPermissions []moduleapi.Permission `json:"required_permissions"`
    AllowedScopeSelectors []DataScopeSelectorV1 `json:"allowed_scope_selectors"`
    MaxSourceBytes       uint64             `json:"max_source_bytes"`
    MaxProjectedBytes    uint64             `json:"max_projected_bytes"`
    ProjectionPolicy    ProjectionPolicyV1 `json:"projection_policy"`
    MCPRead             *MCPContentReadConstraintsV1 `json:"mcp_read"`
}

type MCPContentReadConstraintsV1 struct {
    ApprovalRequirement ApprovalRequirement `json:"approval_requirement"`
    MaxRequestBytes     uint64              `json:"max_request_bytes"`
    MaxResultBytes      uint64              `json:"max_result_bytes"`
    BudgetCategory      string              `json:"budget_category"`
}

type ContentPolicyRuleV1 struct {
    RuleEffect       RuleEffect                `json:"rule_effect"`
    SourceTarget     SourceTargetSelectorV1    `json:"source_target"`
    MatchConstraints ContentMatchConstraintsV1 `json:"match_constraints"`
    AllowConstraints *ContentAllowConstraintsV1 `json:"allow_constraints"`
    RuleOrdinal      uint32                    `json:"rule_ordinal"`
    RuleHash         string                    `json:"rule_hash"`
}

type ContentPolicyDocumentV1 struct {
    SchemaVersion             uint32             `json:"schema_version"`
    PolicyMode                PolicyDocumentMode `json:"policy_mode"`
    OrderedRules              []ContentPolicyRuleV1 `json:"ordered_rules"`
    ContentPolicyDocumentHash string             `json:"content_policy_document_hash"`
}
```

DENY 的 allow_constraints 为 null，但 MatchConstraints 仍参与匹配。MCP_RESOURCE、
MCP_RESOURCE_TEMPLATE_RESULT、MCP_PROMPT 的 ALLOW 必须带非 null `mcp_read`；Skill、
Knowledge、Memory、ToolResult 必须为 null。MCP read 的 moduleapi effect 固定为
`EffectReadOnly ("read_only")`，但仍经过
ProviderInvocationAuthority、独立 DispatchAttempt、Budget reservation、UNKNOWN 和 Seal；
request/result limit 为 JSON-safe 正数，BudgetCategory 使用 §7 grammar。相同 target 可用不同
required tags 或 ceiling 共存并全部匹配；只拒绝 §2 定义的完整 canonical rule body 重复。
Content leaf/grant 的可证明 selector 矩阵固定为：Skill/Knowledge/Memory 允许
PUBLIC/CURRENT_TENANT/CURRENT_WORKSPACE/CURRENT_AGENT/CURRENT_MEMBER/CURRENT_TASK/CURRENT_RUN/
CURRENT_SOURCE，禁止 CURRENT_PROVIDER；MCP 三类和 ToolResult 允许上述全部 selector。每个实际形成的 clause必须至少有一个适用于该来源 family 的 exact alternative，
否则在 grant 构造时失败；不能生成必定无法形成 actual-use proof 的 grant。

domains：

```text
freeagent.governance-content-source-target.v1
freeagent.governance-source-tag-set.v1
freeagent.governance-projection-policy.v1
freeagent.governance-content-policy-rule.v1
freeagent.governance-content-policy-document.v1
```

## 7. Authority 与 Budget source policy wire

```go
type AuthorityPolicyDocumentV1 struct {
    SchemaVersion               uint32             `json:"schema_version"`
    PolicyMode                  PolicyDocumentMode `json:"policy_mode"`
    PermissionCeiling           []moduleapi.Permission `json:"permission_ceiling"`
    EffectCeiling               moduleapi.EffectClass `json:"effect_ceiling"`
    AllowedScopeSelectors       []DataScopeSelectorV1 `json:"allowed_scope_selectors"`
    AuthorityPolicyDocumentHash string             `json:"authority_policy_document_hash"`
}
```

INHERIT 要求 permission/scope 为空且 effect=`none` sentinel，但这些 sentinel 不参与合并；
CEILING 的空 permission/scope 或 effect none 是显式 deny-all。

Budget category 是小写 dotted identifier，最长 128 bytes，无前缀继承。selector：

```go
type BudgetCategoryMatchMode string // ANY_PROVIDER_CATEGORY | EXACT_CATEGORY

type BudgetCategorySelectorV1 struct {
    MatchMode    BudgetCategoryMatchMode `json:"match_mode"`
    ExactCategory *string                `json:"exact_category"`
    SelectorHash string                  `json:"selector_hash"`
}

type ProviderBudgetLimitsV1 struct {
    MaxCalls              uint64 `json:"max_calls"`
    MaxTotalRequestBytes  uint64 `json:"max_total_request_bytes"`
    MaxTotalResultBytes   uint64 `json:"max_total_result_bytes"`
    MaxCostMicros         uint64 `json:"max_cost_micros"`
    MaxReservedTimeMS     uint64 `json:"max_reserved_time_ms"`
}

type BudgetCategoryCeilingV1 struct {
    CategorySelector BudgetCategorySelectorV1 `json:"category_selector"`
    ProviderLimits   ProviderBudgetLimitsV1    `json:"provider_limits"`
    CeilingOrdinal   uint32                    `json:"ceiling_ordinal"`
    CeilingHash      string                    `json:"ceiling_hash"`
}

type BudgetPolicyDocumentV1 struct {
    SchemaVersion           uint32 `json:"schema_version"`
    PolicyMode              PolicyDocumentMode `json:"policy_mode"`
    OrderedCategoryCeilings []BudgetCategoryCeilingV1 `json:"ordered_category_ceilings"`
    BudgetPolicyDocumentHash string `json:"budget_policy_document_hash"`
}
```

ANY_PROVIDER_CATEGORY 要求 `exact_category=null`；EXACT_CATEGORY 要求非 null 且通过下述 dotted
identifier grammar。不得用空字符串表达 ANY，也不得在 Restore 时补默认值。

v1 Governance Budget 只覆盖 executable Provider；核心模型继续使用现有不可变 Model
budget ledger，不会被 Provider category 隐式映射。以后若把模型纳入治理类别，必须新增
明确 MODEL union 并提升版本，不能重解释 Provider limits。

Provider budget ledger scope 固定为 `(TenantID,RunID,MemberID,ExactBudgetCategory)`。
PREWIRE 前原子预留：calls=1、exact canonical request bytes、规则 MaxResultBytes、成本上限、
调用 timeout；成功终态用实际 canonical result bytes/实际成本/实际持续时间结算。
NOT_EXECUTED 释放；UNKNOWN 保留完整预留直到独立对账。并发调用共享同一 category ledger，
不能分别通过后再超额。

`ProviderBudgetLimitsV1` 的五个轴均须 `<= assembly.MaxJSONSafeInteger`；零表示显式零 ceiling，
不是 inherit。转换到现有 signed ledger 前必须先验证范围，禁止先 cast 再比较或发生溢出。
category ceiling 的 canonical body 删除 ordinal/hash 后排序；完整 body 重复拒绝，相同 selector
的多个不同 ceiling 共同生效并逐轴取最小。

domains：

```text
freeagent.governance-authority-policy-document.v1
freeagent.governance-budget-category-selector.v1
freeagent.governance-budget-category-ceiling.v1
freeagent.governance-budget-policy-document.v1
```

## 8. MemberGovernanceResolutionScopeV1

Resolver 不接受裸 `pure_chat bool` 或调用方拼装的 layer hash。控制面从已经验证的
Workspace/Agent/Profile/Task/Catalog candidate evidence 构造不可变 scope：

```go
type MemberGovernanceMode string // PURE_CHAT | MODULAR

type RuntimeCatalogGenerationRefV1 struct {
    SchemaVersion                 uint32 `json:"schema_version"`
    TenantID                      string `json:"tenant_id"`
    RuntimeCatalogGeneration      uint64 `json:"runtime_catalog_generation"`
    RuntimeCatalogGenerationHash  string `json:"runtime_catalog_generation_hash"`
    SkillContentCatalogGeneration uint64 `json:"skill_content_catalog_generation"`
    SkillContentCatalogHash       string `json:"skill_content_catalog_hash"`
    GenerationRefHash             string `json:"generation_ref_hash"`
}

type RuntimeCatalogMembershipRefV1 struct {
    SchemaVersion        uint32                        `json:"schema_version"`
    CatalogGenerationRef RuntimeCatalogGenerationRefV1 `json:"catalog_generation_ref"`
    MembershipOrdinal    uint32                        `json:"membership_ordinal"`
    RuntimeCatalogEntryHash string                     `json:"runtime_catalog_entry_hash"`
    MembershipRefHash    string                        `json:"membership_ref_hash"`
}

type ExactProviderIdentityV1 struct {
    RuntimeKind  runtimecatalog.RuntimeKind `json:"runtime_kind"`
    ModuleRef    *moduleapi.Ref              `json:"module_ref"`
    MCPServerRef *assembly.MCPServerRef      `json:"mcp_server_ref"`
    ProviderIdentityHash string              `json:"provider_identity_hash"`
}

type RuntimeCatalogFactoryKeyWireV1 struct {
    FactoryKind     runtimecatalog.FactoryKind     `json:"factory_kind"`
    FactoryID       string                         `json:"factory_id"`
    HostKind        assembly.RuntimeHostKind       `json:"host_kind"`
    TrustClass      runtimecatalog.TrustClass      `json:"trust_class"`
    ProtocolVersion runtimecatalog.ProtocolVersion `json:"protocol_version"`
    ArtifactDigest  string                         `json:"artifact_digest"`
}

type StaticCapabilityEvidenceV1 struct {
    SchemaVersion             uint32                         `json:"schema_version"`
    RuntimeKind               runtimecatalog.RuntimeKind     `json:"runtime_kind"`
    FactoryKey                RuntimeCatalogFactoryKeyWireV1  `json:"factory_key"`
    OrderedCapabilities       []moduleapi.Capability         `json:"ordered_capabilities"`
    CapabilitySetHash         string                         `json:"capability_set_hash"`
    StaticCapabilityEvidenceHash string                      `json:"static_capability_evidence_hash"`
}

type CandidateProviderV1 struct {
    SchemaVersion             uint32 `json:"schema_version"`
    CatalogMembership         RuntimeCatalogMembershipRefV1 `json:"catalog_membership"`
    ProviderIdentity          ExactProviderIdentityV1 `json:"provider_identity"`
    DeclarationHash           string `json:"declaration_hash"`
    FactoryKey                 RuntimeCatalogFactoryKeyWireV1 `json:"factory_key"`
    ArtifactDigest            string `json:"artifact_digest"`
    StaticCapabilityHash      string `json:"static_capability_hash"`
    StaticCapabilityEvidence  StaticCapabilityEvidenceV1 `json:"static_capability_evidence"`
    ConfigSchemaHash          string `json:"config_schema_hash"`
    CompatibilityHash         string `json:"compatibility_hash"`
    OrderedModuleCapabilities []moduleapi.Capability `json:"ordered_module_capabilities"`
    CapabilitySetHash         string `json:"capability_set_hash"`
    CandidateHash             string `json:"candidate_hash"`
}

type MemberProviderKind string // MODULE | MCP
type MemberProviderFailurePolicy string // REQUIRED | OPTIONAL
type MemberProviderExposureKind string // ORDINARY_TOOLVIEW | RECONCILIATION_ONLY

type MemberProviderSelectionPolicyV1 struct {
    SchemaVersion               uint32                      `json:"schema_version"`
    DeploymentTrustDomainID     string                      `json:"deployment_trust_domain_id"`
    TenantID                    string                      `json:"tenant_id"`
    TaskID                      string                      `json:"task_id"`
    MemberID                    string                      `json:"member_id"`
    ProviderKind                MemberProviderKind          `json:"provider_kind"`
    CatalogMembershipRefHash    string                      `json:"catalog_membership_ref_hash"`
    CandidateHash               string                      `json:"candidate_hash"`
    FailurePolicy               MemberProviderFailurePolicy `json:"failure_policy"`
    ExposureKind                MemberProviderExposureKind  `json:"exposure_kind"`
    ProviderSelectionPolicyHash string                      `json:"provider_selection_policy_hash"`
}

type ProviderCapabilityScopeRefV1 struct {
    SchemaVersion               uint32 `json:"schema_version"`
    CandidateHash               string `json:"candidate_hash"`
    ProviderCapabilityScopeHash string `json:"provider_capability_scope_hash"`
}

type SourceApplicabilityMode string // COMMON | EXACT_PROVIDER

type SourceApplicabilityV1 struct {
    Mode               SourceApplicabilityMode `json:"mode"`
    ExactCandidateHash *string                 `json:"exact_candidate_hash"`
    ApplicabilityHash  string                  `json:"applicability_hash"`
}

type ApplicableLayerScopeV1 struct {
    SourcePolicyRef SourcePolicyRefWireV1 `json:"source_policy_ref"`
    Applicability   SourceApplicabilityV1 `json:"applicability"`
}

type RequestedContentKind string
// SKILL | KNOWLEDGE | MEMORY | MCP_RESOURCE | MCP_RESOURCE_TEMPLATE | MCP_PROMPT

type BindingOriginSlotKind string
// WORKSPACE_DEFAULT_SKILL | WORKSPACE_KNOWLEDGE_COLLECTION |
// AGENT_SKILL | AGENT_KNOWLEDGE_COLLECTION | PROFILE_DEFAULT_SKILL |
// TASK_REQUESTED_SKILL | CONTENT_BINDING_CONTRIBUTION

type KnowledgeCollectionOriginEntryV1 struct {
    CollectionID string `json:"collection_id"`
}

type MemoryBindingOriginEntryV1 struct {
    OwnerKind         MemoryOwnerKind `json:"owner_kind"`
    OwnerIdentityHash string          `json:"owner_identity_hash"`
    ScopeKind         MemoryScopeKind `json:"scope_kind"`
    ScopeIdentityHash string          `json:"scope_identity_hash"`
    RecordKind        string          `json:"record_kind"`
}

type ContentBindingOwnerRefV1 struct {
    SchemaVersion         uint32                    `json:"schema_version"`
    AssemblyBindingSource *assembly.BindingSource   `json:"assembly_binding_source"`
    TenantScopeAuthority  *TenantScopeAuthorityV1   `json:"tenant_scope_authority"`
    OwnerRefHash          string                    `json:"owner_ref_hash"`
}

type ContentBindingContributionKind string // MCP_CONTENT | MEMORY

type MCPContentAttachmentV1 struct {
    SchemaVersion        uint32                 `json:"schema_version"`
    RequestedContentKind RequestedContentKind   `json:"requested_content_kind"`
    MCPServerRef         assembly.MCPServerRef  `json:"mcp_server_ref"`
    SelectorTarget       SourceTargetSelectorV1 `json:"selector_target"`
    AttachmentHash       string                 `json:"attachment_hash"`
}

type ContentBindingContributionAuthorityV1 struct {
    SchemaVersion    uint32                         `json:"schema_version"`
    TenantID         string                         `json:"tenant_id"`
    OwnerRef         ContentBindingOwnerRefV1        `json:"owner_ref"`
    ContributionKind ContentBindingContributionKind `json:"contribution_kind"`
    MCPContent       *MCPContentAttachmentV1         `json:"mcp_content"`
    Memory           *MemoryBindingOriginEntryV1     `json:"memory"`
    ContributionHash string                         `json:"contribution_hash"`
}

type ContentBindingContributionGenerationMemberV1 struct {
    ContributionOrdinal uint32 `json:"contribution_ordinal"`
    ContributionHash    string `json:"contribution_hash"`
}

type ContentBindingContributionGenerationV1 struct {
    SchemaVersion       uint32 `json:"schema_version"`
    TenantID            string `json:"tenant_id"`
    OwnerRef            ContentBindingOwnerRefV1 `json:"owner_ref"`
    Generation          uint64 `json:"generation"`
    ParentGenerationHash string `json:"parent_generation_hash"`
    OrderedContributions []ContentBindingContributionGenerationMemberV1 `json:"ordered_contributions"`
    GenerationHash      string `json:"generation_hash"`
}

type ContentBindingContributionGenerationRefV1 struct {
    SchemaVersion  uint32 `json:"schema_version"`
    TenantID       string `json:"tenant_id"`
    OwnerRef       ContentBindingOwnerRefV1 `json:"owner_ref"`
    Generation     uint64 `json:"generation"`
    GenerationHash string `json:"generation_hash"`
    GenerationRefHash string `json:"generation_ref_hash"`
}

type ContentBindingContributionRefV1 struct {
    SchemaVersion       uint32 `json:"schema_version"`
    GenerationRef      ContentBindingContributionGenerationRefV1 `json:"generation_ref"`
    ContributionOrdinal uint32 `json:"contribution_ordinal"`
    Contribution       ContentBindingContributionAuthorityV1 `json:"contribution"`
    ContributionRefHash string `json:"contribution_ref_hash"`
}

type RequestedBindingOriginEntryV1 struct {
    SchemaVersion       uint32                            `json:"schema_version"`
    OriginSlotKind      BindingOriginSlotKind             `json:"origin_slot_kind"`
    SkillAttachment     *assembly.SkillAttachment         `json:"skill_attachment"`
    SkillRef            *assembly.SkillRef                `json:"skill_ref"`
    KnowledgeCollection *KnowledgeCollectionOriginEntryV1 `json:"knowledge_collection"`
    ContentContribution *ContentBindingContributionRefV1  `json:"content_contribution"`
    SourceEntryHash     string                            `json:"source_entry_hash"`
}

type RequestedBindingOriginRefV1 struct {
    SchemaVersion    uint32                       `json:"schema_version"`
    OwnerRef         ContentBindingOwnerRefV1     `json:"owner_ref"`
    OriginSlotKind   BindingOriginSlotKind        `json:"origin_slot_kind"`
    SourceOrdinal    uint32                       `json:"source_ordinal"`
    SourceEntry      RequestedBindingOriginEntryV1 `json:"source_entry"`
    OriginRefHash    string                       `json:"origin_ref_hash"`
}

type RequestedContentBindingAuthorityV1 struct {
    SchemaVersion          uint32                        `json:"schema_version"`
    TenantID               string                        `json:"tenant_id"`
    RequestedContentKind   RequestedContentKind          `json:"requested_content_kind"`
    BindingBodyHash        string                        `json:"binding_body_hash"`
    OrderedBindingOrigins  []RequestedBindingOriginRefV1 `json:"ordered_binding_origins"`
    BindingAuthorityHash   string                        `json:"binding_authority_hash"`
}

type KnowledgeCollectionVersionAuthorityV1 struct {
    SchemaVersion          uint32 `json:"schema_version"`
    TenantID               string `json:"tenant_id"`
    CollectionID           string `json:"collection_id"`
    CollectionVersion      uint64 `json:"collection_version"`
    ItemVersionCount       uint32 `json:"item_version_count"`
    OrderedItemVersionRoot string `json:"ordered_item_version_root"`
    CollectionVersionHash  string `json:"collection_version_hash"`
}

type KnowledgeCollectionItemVersionRefV1 struct {
    SchemaVersion               uint32 `json:"schema_version"`
    TenantID                    string `json:"tenant_id"`
    CollectionID                string `json:"collection_id"`
    CollectionVersion           uint64 `json:"collection_version"`
    ItemOrdinal                 uint32 `json:"item_ordinal"`
    KnowledgeItemID             string `json:"knowledge_item_id"`
    KnowledgeItemVersion        uint64 `json:"knowledge_item_version"`
    KnowledgeContentVersionHash string `json:"knowledge_content_version_hash"`
    ItemVersionRefHash          string `json:"item_version_ref_hash"`
}

type KnowledgeContentBindingRefV1 struct {
    SchemaVersion        uint32 `json:"schema_version"`
    TenantID             string `json:"tenant_id"`
    CollectionID         string `json:"collection_id"`
    CollectionVersion    uint64 `json:"collection_version"`
    CollectionVersionHash string `json:"collection_version_hash"`
    CollectionVersionAuthority KnowledgeCollectionVersionAuthorityV1 `json:"collection_version_authority"`
    BindingRefHash       string `json:"binding_ref_hash"`
}

type MemoryContentBindingRefV1 struct {
    SchemaVersion        uint32          `json:"schema_version"`
    TenantID             string          `json:"tenant_id"`
    OwnerKind            MemoryOwnerKind `json:"owner_kind"`
    OwnerIdentityHash    string          `json:"owner_identity_hash"`
    ScopeKind            MemoryScopeKind `json:"scope_kind"`
    ScopeIdentityHash    string          `json:"scope_identity_hash"`
    RecordKind           string          `json:"record_kind"`
    BindingRefHash       string          `json:"binding_ref_hash"`
}

type RequestedMCPContentBindingV1 struct {
    SchemaVersion           uint32                        `json:"schema_version"`
    CatalogMembership       RuntimeCatalogMembershipRefV1 `json:"catalog_membership"`
    CandidateHash           string                        `json:"candidate_hash"`
    SelectorTarget          SourceTargetSelectorV1        `json:"selector_target"`
    BindingRefHash          string                        `json:"binding_ref_hash"`
}

type RequestedContentBindingV1 struct {
    SchemaVersion      uint32                              `json:"schema_version"`
    RequestedContentKind RequestedContentKind              `json:"requested_content_kind"`
    Skill              *SkillContentCatalogMembershipRefV1 `json:"skill"`
    Knowledge          *KnowledgeContentBindingRefV1       `json:"knowledge"`
    Memory             *MemoryContentBindingRefV1          `json:"memory"`
    MCPResource        *RequestedMCPContentBindingV1       `json:"mcp_resource"`
    MCPResourceTemplate *RequestedMCPContentBindingV1      `json:"mcp_resource_template"`
    MCPPrompt          *RequestedMCPContentBindingV1       `json:"mcp_prompt"`
    BindingAuthority   RequestedContentBindingAuthorityV1  `json:"binding_authority"`
    RequestedBindingHash string                            `json:"requested_binding_hash"`
}

type MemberGovernanceResolutionScopeV1 struct {
    SchemaVersion uint32 `json:"schema_version"`
    DeploymentTrustDomainID string `json:"deployment_trust_domain_id"`
    PlatformSafetyBaselineAuthorityHash string `json:"platform_safety_baseline_authority_hash"`
    ResultClassifierDefinitionHash string `json:"result_classifier_definition_hash"`
    TenantID string `json:"tenant_id"`
    TenantScopeAuthorityHash string `json:"tenant_scope_authority_hash"`
    WorkspaceID string `json:"workspace_id"`
    WorkspaceScopeAuthorityHash string `json:"workspace_scope_authority_hash"`
    WorkspaceDefinitionHash string `json:"workspace_definition_hash"`
    WorkspaceScopeAssociationHash string `json:"workspace_scope_association_hash"`
    MemberID string `json:"member_id"`
    AgentID string `json:"agent_id"`
    AgentScopeAuthorityHash string `json:"agent_scope_authority_hash"`
    AgentVersionID string `json:"agent_version_id"`
    AgentVersionHash string `json:"agent_version_hash"`
    AgentScopeAssociationHash string `json:"agent_scope_association_hash"`
    AssemblyProfileID string `json:"assembly_profile_id"`
    AssemblyProfileVersionID string `json:"assembly_profile_version_id"`
    AssemblyProfileHash string `json:"assembly_profile_hash"`
    TaskID string `json:"task_id"`
    TaskScopeHash string `json:"task_scope_hash"`
    TaskRequirementsHash string `json:"task_requirements_hash"`
    RuntimeCatalogGenerationRef RuntimeCatalogGenerationRefV1 `json:"runtime_catalog_generation_ref"`
    MemberGovernanceMode MemberGovernanceMode `json:"member_governance_mode"`
    OrderedCandidateProviders []CandidateProviderV1 `json:"ordered_candidate_providers"`
    OrderedProviderSelectionPolicies []MemberProviderSelectionPolicyV1 `json:"ordered_provider_selection_policies"`
    OrderedProviderCapabilityScopes []ProviderCapabilityScopeRefV1 `json:"ordered_provider_capability_scopes"`
    OrderedContentBindingContributionGenerationRefs []ContentBindingContributionGenerationRefV1 `json:"ordered_content_binding_contribution_generation_refs"`
    OrderedRequestedContentBindings []RequestedContentBindingV1 `json:"ordered_requested_content_bindings"`
    OrderedApplicableLayerScopes []ApplicableLayerScopeV1 `json:"ordered_applicable_layer_scopes"`
    ResolutionScopeHash string `json:"resolution_scope_hash"`
}
```

层级 scope identity domain：

```text
PLATFORM_SAFETY_BASELINE: PlatformSafetyBaselineAuthorityHash
TENANT:                  TenantScopeAuthorityHash
WORKSPACE:               WorkspaceScopeAuthorityHash（DefinitionHash 另作 config parent）
AGENT_VERSION:           AgentScopeAuthorityHash（AgentVersionHash 另作 config parent）
ASSEMBLY_PROFILE:        AssemblyProfileHash
TASK_SCOPE:              TaskScopeHash
PROVIDER_CAPABILITY:     matching ProviderCapabilityScopeRef.ProviderCapabilityScopeHash
```

`RuntimeCatalogGenerationRefV1` 必须从一个已完整验证的现有
`runtimecatalog.RuntimeCatalogGeneration` 派生，不修改其 v1 bytes/hash。RuntimeCatalog 实现
新增 sealed `RuntimeCatalogMembershipResolutionView` 和
`LookupProviderMembershipExact`：返回 TenantID、generation/hash、Skill anchor、membership
ordinal/entry hash 以及完整 Entry resolution view；不提供接收裸 hash/ordinal 的 production
authority constructor。`RuntimeCatalogMembershipRefV1` 必须逐字段闭合到该 view。
ResolutionScope.DeploymentTrustDomainID 是本次闭包唯一的 deployment parent identity。baseline、
classifier、TenantScopeAuthority 以及 legacy bootstrap manifest/consumption proof（若存在）的
DeploymentTrustDomainID 必须逐字段等于它；Workspace/Agent authority 经同一个
TenantScopeAuthorityHash 继承该 trust domain，Run authority 再经同租户 TaskScope 关闭。不能把来自
不同部署域但分别合法的 trusted roots 拼进同一 ResolutionScope。RunMemberBinding、
GovernanceSnapshot、MemberSnapshotV2 与 RunManifestV2 必须复制/复合引用同一字段和完整 parents。
ResolutionScope.PlatformSafetyBaselineAuthorityHash 必须逐字段等于启动时已密封 baseline view 中
唯一获准 authority；PLATFORM_SAFETY_BASELINE 的 SourcePolicyRef.ScopeIdentityHash 必须就是该
hash。ResolutionScope.TenantScopeAuthorityHash 必须逐字段等于同 TenantID 的先验
TenantScopeAuthorityV1；TENANT 的 SourcePolicyRef.ScopeIdentityHash 必须就是该 hash，不存在
`freeagent.governance-tenant-scope.v1` 别名。RunMemberBinding、GovernanceSnapshot、
MemberSnapshotV2 与 RunManifestV2 都复合冻结同一 baseline/tenant hash。CURRENT_TENANT 或
Skill/Knowledge tenant source scope 只能使用这一个 frozen tenant authority，不能选择另一历史
tenant generation。
ResolutionScope.WorkspaceScopeAuthorityHash/AgentScopeAuthorityHash 必须来自 sealed creation
authority view；WorkspaceDefinition/AgentVersion parent 必须与 association 逐字段具有同一 ID/hash。
具体闭包由 `WorkspaceDefinitionScopeAssociationV1` / `AgentVersionScopeAssociationV1` 完成，现有
assembly document 原字节不加字段。
WORKSPACE/AGENT_VERSION layer 的 SourcePolicyRef.ScopeIdentityHash 使用 stable scope hash，同时
SourcePolicyDocument 的版本/leaf hashes继续由 exact Definition/Version authoring view 证明。

`ExactProviderIdentityV1` 恰好一个 ref 非 null，RuntimeKind 与分支一致；Candidate 的
Declaration/Factory/Artifact/StaticCapability/ConfigSchema/Compatibility 必须逐字段等于该
Entry。`StaticCapabilityEvidenceV1` 使用 `freeagent.runtime-static-capability-evidence.v1`，只绑定
factory-scoped 的 RuntimeKind、FactoryKey 与 capabilities；其 EvidenceHash 必须精确等于
Entry/FactoryDescriptor.StaticCapabilityHash，不能把原字段当作无正文 digest。ProviderIdentity 与
DeclarationHash 只由 Candidate body 绑定，不能混入 factory-scoped digest；因此一个 FactoryKey
可由多个 Provider 合法共享相同证据。MODULE capabilities
逐项等于经验证 module manifest 的 Provides，按 `moduleapi.Capability` canonical key 严格排序；
MCP 必须 `[]`，其 discovery capability 以后由 LockedMCP 追加。
`CapabilitySetHash` 固定为
`H("freeagent.governance-capability-set.v1", JCS({"schema_version":1,
"ordered_capabilities":[...]}))` 并与规范 capability 数组逐字段相等。CandidateHash 使用
`freeagent.governance-candidate-provider.v1` 覆盖删除且仅删除 `candidate_hash` 的完整
CandidateProviderV1 body。随后才构造独立的 ProviderCapabilityScopeRef：

```text
H("freeagent.governance-provider-capability-scope.v1",
  JCS({"schema_version":1,"candidate_hash":CandidateHash}))
```

派生顺序固定为“验证 generation/member/entry → CandidateHash → scope ref”；scope ref 不得
嵌回 CandidateHash preimage。每个 candidate 恰好有一个同 index 的 scope ref，ref 的
CandidateHash 必须逐字段相等。这样共享相同 StaticCapabilityHash 的不同 Provider、租户或
目录代际不能互换。候选按
`(MembershipOrdinal, RuntimeKind, Provider canonical key, RuntimeCatalogEntryHash)` 排序；
scope refs 与候选同序。ordinal、entry、provider identity、MembershipRefHash、CandidateHash 或
ProviderCapabilityScopeHash 任一重复均拒绝。
RuntimeCatalog/Factory registry 必须新增 sealed `StaticCapabilityEvidenceResolutionView`；production
Candidate constructor 只接受该 view。已有仅含任意 StaticCapabilityDigest 而无可恢复 evidence 的
entry/factory descriptor 只能继续 audit，不能通过“补交正文”寻找旧 digest 的 preimage。升级必须
先用规范 evidence 计算新的 StaticCapabilityHash，按 registry 规则版本化 FactoryKey/ArtifactDigest
或 descriptor generation，再发布引用新 EntryHash 的新 RuntimeCatalog generation；旧 entry 永不
进入 4B2 Candidate 集合。
本文不定义 `CandidateScopeRef`：policy-layer 的 applicability identity 只能是
ProviderCapabilityScopeHash；actual DataScope/operation identity 只能是 CandidateHash。二者由
ProviderCapabilityScopeRefV1 的单向 `CandidateHash -> ProviderCapabilityScopeHash` 关系连接，
不得互换或再造第三个 alias hash。

`MemberProviderSelectionPolicyV1` 是每个 executable candidate 的成员级、运行前选择事实，不是
Provider 本身，也不授予目录 membership、permission、secret 或 host。其 `schema_version` 固定为
1；`ProviderKind` 是闭集 `MODULE | MCP`，`FailurePolicy` 是闭集 `REQUIRED | OPTIONAL`，
`ExposureKind` 是闭集 `ORDINARY_TOOLVIEW | RECONCILIATION_ONLY`。`DeploymentTrustDomainID`、
`TenantID`、`TaskID` 与 `MemberID` 必须逐字段等于所在 ResolutionScope；本类型和 ResolutionScope
故意都不含 `RunID` 或 `MemberOrdinal`，运行实例身份只由后续 RunMemberBinding/MemberSnapshotV2
冻结，不能把同一成员选择规则按调用方提供的 run/ordinal 重新解释。
wire ordinal 固定为：`ProviderKind MODULE=0/MCP=1`、
`FailurePolicy REQUIRED=0/OPTIONAL=1`、
`ExposureKind ORDINARY_TOOLVIEW=0/RECONCILIATION_ONLY=1`；不得使用字符串字典序、数据库 collation
或实现私有 enum 值决定 canonical 顺序。

每项 policy 必须和同 index 的 `OrderedCandidateProviders` 项形成 exact closure：

- `ProviderKind=MODULE` 当且仅当 candidate 的 RuntimeKind/identity 是 MODULE，`ProviderKind=MCP`
  当且仅当两者是 MCP；
- `CatalogMembershipRefHash` 必须等于 candidate 内完整
  `CatalogMembership.MembershipRefHash`，`CandidateHash` 必须等于 candidate 自身 hash；
- `ProviderSelectionPolicyHash` 固定为
  `H("freeagent.member-provider-selection-policy.v1", JCS(body 删除且仅删除
  provider_selection_policy_hash))`；不得用 candidate hash、display name 或全局默认策略代替；
- policy 正文必须来自已认证控制面的 exact member selection view；Restore 只恢复 portable audit
  authority，不获得 active assignment，也不得读取 current/default 补全 FailurePolicy 或 ExposureKind。

`OrderedProviderSelectionPolicies` 与 `OrderedCandidateProviders` 必须具有相同 count、相同 index 和
相同 candidate 排序；即每个 candidate 恰好一项 policy，并以
`(ProviderKind,CatalogMembershipRefHash,CandidateHash)` 做 typed set-equality。不存在遗漏、额外、
跨 kind 重包装、相同 candidate 的第二份策略或独立重排。两个数组均不超过
`MaxCandidateProviders`，并在复制、排序或建 map 前先做 count/aggregate-byte preflight。
`REQUIRED` 表示对应已冻结 exact Provider 在 seal/activation 阶段无法达到 READY 时成员 execution
seal 必须 BLOCKED；`OPTIONAL` 只允许把该 exact Provider 记录为 UNAVAILABLE，绝不选择近似
Provider 或扩大权限。
`ORDINARY_TOOLVIEW` 才可进入普通业务 ToolView；`RECONCILIATION_ONLY` 只能供父规范锁定的 UNKNOWN
只读对账路径使用，不能出现在普通 ToolView、模型 tool definition 或 Skill 执行路径。最终准入仍需
完整 membership、snapshot、seal、governance 与 permission parents；本 policy 不能替代它们。
PURE_CHAT 以及 MODULAR 的 zero-provider/content-only 成员都必须同时使用
`OrderedCandidateProviders=[]`、`OrderedProviderSelectionPolicies=[]` 和
`OrderedProviderCapabilityScopes=[]`；三个空数组均参与 ResolutionScope canonical bytes/hash，
不得省略或编码为 `null`。

`ApplicableLayerScopeV1.SourcePolicyRef` 必须逐字段等于 GovernancePolicyGeneration 中对应的
完整 SourcePolicyRef，不能省略 version 或四个 leaf hash。PLATFORM..TASK_SCOPE 的 applicability
只能是 COMMON 且 candidate=null；
PROVIDER_CAPABILITY 只能是 EXACT_PROVIDER，candidate 必须在候选集合中，ScopeIdentityHash
必须等于同 index ProviderCapabilityScopeRef 的 hash。applicability 由 resolver 派生，不由
调用方自报。顺序严格等于 generation 中适用 SourcePolicyRef 的既有
`(RestrictionOrder,LayerOrdinal)` 顺序。

`RequestedContentBindingV1` 是只允许 SKILL、KNOWLEDGE、MEMORY、MCP_RESOURCE、
MCP_RESOURCE_TEMPLATE、MCP_PROMPT 六类的严格 union：RequestedContentKind 与恰好一个非 null
branch 一致，其余五个 branch 必须显式为 null。它故意不复用运行期
`MCP_RESOURCE_TEMPLATE_RESULT`，模板 attachment 与一次带参数的结果是不同 wire 阶段。
Knowledge ref 内嵌完整 `KnowledgeCollectionVersionAuthorityV1`，其 Tenant/ID/version/hash 必须与
ref 相等；authority 使用 `freeagent.knowledge-collection-version-authority.v1`。每个 collection
version 另有 0..MaxKnowledgeCollectionItemsPerVersion 条
`KnowledgeCollectionItemVersionRefV1`，使用
`freeagent.knowledge-collection-item-version-ref.v1`，ordinal 从 0 连续且每条复合引用同租户、同
collection/version 的 immutable KnowledgeContentVersion parent。`ItemVersionCount` 必须等于 ref
数；`OrderedItemVersionRoot` 固定为
`H("freeagent.knowledge-collection-version-item-root.v1",
JCS({"schema_version":1,"ordered_item_version_ref_hashes":[...]}))`。item refs 按 ordinal 有序，
KnowledgeItemID、KnowledgeContentVersionHash 与 ItemVersionRefHash 分别禁止重复；完整 refs 重算
root。Collection authority 自身不进入 content-version hash preimage，只单向引用 content-version
refs，因此没有 collection/content hash 环。sealed collection resolution view 必须提供 authority
与完整 refs，且某个 Knowledge source 的 ContentVersionHash 必须在 refs 中精确出现一次。Memory ref 必须从
不可变 scope resolution view 派生；MCP ref
必须逐字段等于本 scope 中同一个 Candidate 的 catalog membership，并内嵌完整、同 family 的预发现
SourceTargetSelector（其 ProviderSelector 必须是该 exact MCP server）。不能只凭名称、裸 source
hash 或 current 指针构造。

Workspace/Agent 的 Knowledge origin 只声明 CollectionID，所以新 ResolutionScope 必须通过
Knowledge repository 对 `{TenantID,CollectionID}` 的线性化 current-at-construction 读取选择已发布
CollectionVersionAuthority；调用方不能传历史 version/hash。选中后完整 authority/item-root 冻结进
RequestedBinding 和 MemberSnapshot，既有 Run 的 replay 继续使用该版本。Restore portable history
不推进 collection current，也不能让新 Run 选择旧版本。未来若需要 owner 显式 pin 历史版本，必须
由新的 contribution kind/versioned wire 表达，不能重解释当前 KnowledgeCollectionOriginEntryV1。

每个 binding 还内嵌 `RequestedContentBindingAuthorityV1`。BindingBodyHash 对 Skill 等于完整
MembershipRefHash，对 Knowledge/Memory/MCP 等于对应 BindingRefHash；kind、tenant 和 body 必须
逐字段一致。每个 `RequestedBindingOriginRefV1` 必须用 sealed owner view 证明
`OwnerRef + OriginSlotKind + SourceOrdinal + 完整 SourceEntry` 正好指向
WorkspaceDefinition/AgentVersion/AssemblyProfile/TaskRequirements 中的 Skill/Knowledge 项，或该
owner 已发布的 ContentBindingContribution generation 中的 exact contribution；不接受孤立的
64-hex authority。`RequestedBindingOriginEntryV1` 是 strict union，使用
`freeagent.governance-requested-binding-origin-entry.v1` 覆盖删除且仅删除 SourceEntryHash 的完整
body。slot/branch 矩阵固定为：Workspace/Agent/Profile skill→SkillAttachment，Task skill→SkillRef；
Workspace/Agent knowledge→KnowledgeCollection；CONTENT_BINDING_CONTRIBUTION→完整 ContributionRef；
其余分支均 null。direct Skill/Knowledge 的 OwnerRef 必须使用 assembly branch；Contribution 的
OwnerRef 必须逐字段等于 Contribution.GenerationRef.OwnerRef。不存在接受任意 owner/policy hash 的
分支。

`ContentBindingContributionAuthorityV1` 是可选外挂 sidecar 的单条 strict-union authority，使用
`freeagent.content-binding-contribution-authority.v1`。MCP_CONTENT branch 的
`MCPContentAttachmentV1` 只允许 MCP_RESOURCE/MCP_RESOURCE_TEMPLATE/MCP_PROMPT；SelectorTarget
必须按 requested→runtime 映射使用同一 family；ProviderSelector 必须是
EXACT_MCP_SERVER 且 server ref 必须等于 attachment.MCPServerRef，ANY_MCP_SERVER 在 contribution
wire 中拒绝，不能越过 attachment 的 server 边界。发布 contribution 时，sealed owner view 还必须证明该 server ref
逐字段存在于 Workspace/Agent/Profile 的 MCPAttachment 或 Task RequestedMCPServers；“挂了 server”
本身不产生任何 content selector；Tenant owner 不允许 MCP_CONTENT。MEMORY branch 恰好保存一条
MemoryBindingOriginEntryV1，并执行 owner-grant 矩阵：PRIVATE 只允许 exact AGENT assembly owner，
且 owner association 的 AgentScopeAuthorityHash 同时等于 memory OwnerIdentityHash 与
ScopeIdentityHash；WORKSPACE 只允许 exact WORKSPACE assembly owner，且 association 的
WorkspaceScopeAuthorityHash 同时等于 owner/scope identity；TENANT 只允许 TenantScopeAuthority
owner 且其 hash 等于 ScopeIdentityHash。PROFILE/TASK 不得发布 MEMORY grant，也不能替所有者扩大
scope；未来跨 owner 使用只能新增显式 delegation authority。

`ContentBindingOwnerRefV1` 是 strict union：恰好一个 branch 非 null。assembly branch 只允许
WORKSPACE/AGENT/PROFILE/TASK BindingSource 并关闭到 exact immutable owner；tenant branch 保存完整
TenantScopeAuthorityV1。OwnerRef 使用 `freeagent.content-binding-owner-ref.v1`，不能用 tenant ID、
display name 或裸 owner hash 替代。

每个 owner 的 contributions 通过独立
`ContentBindingContributionGenerationV1` 发布：Generation 从 1 连续递增，第一代使用固定
`H("freeagent.content-binding-contribution-no-parent.v1",JCS({"schema_version":1}))` sentinel，
后续 generation 禁止使用 sentinel 且 ParentGenerationHash 必须等于
同 owner 前一代；current pointer 只以 `{TenantID,OwnerRefHash}` CAS 更新，历史不可改写。每代最多
MaxRequestedContentBindings 条，constructor 先按 `(ContributionKind ordinal MCP_CONTENT=0/MEMORY=1,
ContributionHash)` 排序再设置从 0 连续的 ordinal，完整 body/hash 分别禁止重复。generation/ref/
contribution-ref 使用各自 `.v1` domain；ResolutionScope 只接受 repository 返回的 sealed generation
view，按 governance OwnerClassOrder + OwnerRefHash 严格排序并冻结 refs。可选 sidecar
不修改旧 Workspace/Agent/Profile/Task hash；PURE_CHAT 的 generation refs 必须为 `[]`。
Generation Restore 必须同时接收 1:1 的完整 contribution documents，逐份重算 owner/kind/body/hash、
重新派生规范排序与连续 ordinal，再与 OrderedContributions 完全相等；只给 ordinal+hash 不能恢复或
证明 generation closure。
每个 ResolutionScope 对 Tenant/Workspace/Agent/Profile/Task 五个 exact owner 各允许 0..1 个
generation ref；OwnerRef 必须逐字段等于 scope 中对应 immutable owner/authority，重复或 foreign
owner 拒绝。构造
scope 时 ref 必须来自 repository 对该 owner 做线性化读取所得的 current-at-construction CAS view；
冻结后既有 Run 继续使用该 generation，新 Run 不得选择历史 current。只有通过现有控制面身份认证、
并获准发布对应 Workspace/Agent/Profile/Task 配置的 principal 才能调用 Publish/CAS API；API 写入
append-only publisher audit event。直接 Restore portable generation history 绝不创建/推进 current
pointer，也不获得 active assignment；OwnerRef+self-hash 单独不构成发布授权。

Contribution generation 与 Knowledge collection version 的“current-at-construction”都使用显式的
local activation + portable history，不能是未分类的内存指针：

```text
ContentBindingContributionCurrentActivationV1:
  schema_version = 1
  DeploymentTrustDomainID / TenantID / full OwnerRef / OwnerRefHash
  Generation / GenerationHash / GenerationRefHash
  SourceBackendID / BackendOwnerEpoch / ActivationOrdinal
  PreviousActivationPresent / PreviousActivationHash
  ActorPrincipalRef / ActivationHash

KnowledgeCollectionCurrentActivationV1:
  schema_version = 1
  DeploymentTrustDomainID / TenantID / CollectionID
  CollectionVersion / CollectionVersionHash
  SourceBackendID / BackendOwnerEpoch / ActivationOrdinal
  PreviousActivationPresent / PreviousActivationHash
  ActorPrincipalRef / ActivationHash
```

domains 分别为 `freeagent.content-binding-contribution-current-activation.v1` 与
`freeagent.knowledge-collection-current-activation.v1`。每个 local natural key 的 ActivationOrdinal 从 1
连续递增，previous 链必须同 backend owner/key；activation 以完整复合 FK 指向 generation ref 或 collection
authority，并作为 PH 进入 portable history/root。repository 的线性化 current view 只能来自本 backend
owner 对该 key 的 current CAS row；current row 是 root-excluded EL activation state，不是 source export
blocker。portable Restore 导入 PH 但目标 current 必须为空；bootstrap/cutover 完成后，只有认证控制面
显式 re-activate exact imported hash 才能写目标 backend 的新 PH/current。导入器、Run resolver 或数据
正文都不得自动推进它。既有 Run 继续使用冻结 ref；目标未 re-activate 时只阻止新 Run 选择该 current，
不破坏历史审计或纯聊天路径。

两类 activation 的 local natural key 分别固定为
`{DeploymentTrustDomainID,TenantID,OwnerRefHash,SourceBackendID,BackendOwnerEpoch}` 与
`{DeploymentTrustDomainID,TenantID,CollectionID,SourceBackendID,BackendOwnerEpoch}`。严格 previous 矩阵为：

- `ActivationOrdinal=1`：`PreviousActivationPresent=false`，PreviousActivationHash 分别固定为
  `345384d4f48b59367821c330bb15062aca93fa92a09c55d75f1826fe856108f0` 与
  `3bfcd76992e4c231d4af18ece6bfc56125e0961f36a22c8b4c47bb8e675d543c`；
- `ActivationOrdinal>1`：`PreviousActivationPresent=true`，PreviousActivationHash 必须逐字段引用同一
  natural key、`ActivationOrdinal-1` 的 ActivationHash；缺行、跳号、跨 key/backend/epoch 或 false+真实
  parent 一律失败关闭。

两个 genesis literal 分别为
`lowerhex(SHA256(UTF8("freeagent.content-binding-contribution-current-activation-no-parent.v1") ||
0x00 || JCS({"schema_version":1})))` 与 knowledge 对应的
`freeagent.knowledge-collection-current-activation-no-parent.v1` domain；golden 不得由被测实现生成。
New/Restore 都必须先执行上述矩阵、完整 parent FK 与重哈希，再接受 body。

`ActorPrincipalRef` 是审计字段而不是授权 hash：1..128 ASCII bytes，严格匹配
`[A-Za-z0-9][A-Za-z0-9._:@-]{0,127}`，不 trim/大小写折叠。Publish/CAS 接口不得从业务 payload 接受该值，
只能在 engine-local control plane 已认证并完成对应 owner/collection 写权限检查后注入；portable Restore
只保留该审计标签，绝不据此授权或推进 target current。

Contribution closure 必须使用逐层 exact equality 和同列同序复合 FK，而不是只验证单个 hash：
`ContributionAuthority -> Generation.OrderedContributions -> GenerationRef -> ContributionRef ->
ResolutionScope -> RequestedBindingOrigin -> MemberSnapshotV2` 的 TenantID、OwnerRef 完整 body/hash、
Generation、GenerationHash、ContributionOrdinal、ContributionKind 与 ContributionHash 必须逐字段相等。
每个 selected ContributionRef 的完整 GenerationRef 必须就是 ResolutionScope 对同一 owner 冻结的
唯一 ref；不得从该 owner 的历史 generation、另一 current view 或未被 scope 选择的完整合法文档取
contribution。RequestedBinding origin 再必须指向 MemberSnapshotV2 中同一个 selected ref，禁止用
相同 contribution hash 重新包装 generation/owner。

MemberSnapshotV2.OrderedSelectedContentBindingContributions 的 exact set 固定为：遍历
OrderedRequestedContentBindings 中每个 BindingAuthority.OrderedBindingOrigins，收集所有
OriginSlotKind=CONTENT_BINDING_CONTRIBUTION 的完整 ContributionRef，按 ContributionRefHash 去重后必须
恰好全量出现一次；数组不得多一项、少一项或保留未被任何 RequestedBinding 使用的 contribution。
规范顺序为 `(OwnerClassOrder,OwnerRefHash,Generation,ContributionOrdinal,ContributionRefHash)` 严格
递增，OwnerClassOrder 使用 §8 的固定 ordinal；相同 ref、相同 owner/generation/ordinal 或相同
ContributionHash 重复均拒绝。计数不得超过 MaxRequestedContentBindings，constructor 在任何数组分配
前完成 aggregate preflight。ResolutionScope 不单独携带 selected refs；MemberSnapshot 从已经关闭的
RequestedBinding origins 确定性派生并冻结该集合。

Workspace/Agent/Profile owner 的 TenantID 必须从其 exact immutable assembly parent 与 scope
association 关闭；Tenant owner 直接从 TenantScopeAuthority 关闭。TASK 的 assembly.BindingSource 与
TaskRequirements 原文不自带 TenantID，因此 sealed TASK owner view 必须额外复合绑定本
ResolutionScope 的 `{TenantID,TaskID,TaskScopeHash,TaskRequirementsHash}`，四字段完全相等后才能作为
OwnerRef。任一 nested TenantID/OwnerRefHash 不一致、跨 tenant 重包装或 Restore 后试图激活历史
generation 都失败关闭。

BindingOriginSlotKind ordinal 固定为：`WORKSPACE_DEFAULT_SKILL=0,
WORKSPACE_KNOWLEDGE_COLLECTION=1,AGENT_SKILL=2,AGENT_KNOWLEDGE_COLLECTION=3,
PROFILE_DEFAULT_SKILL=4,TASK_REQUESTED_SKILL=5,CONTENT_BINDING_CONTRIBUTION=6`。OwnerClassOrder 对 assembly
ref 在 governance wire 中显式固定为
`TENANT_AUTHORITY=0,WORKSPACE=1,AGENT=2,PROFILE=3,TASK=4`；TENANT 只可来自 owner-ref 的
authority branch，不允许 `assembly.BindingTenant` 冒充，也不复用当前
`assembly.BindingLayer` 的字典序或不存在的 restriction-order API。origins 按
`(OwnerClassOrder,OriginSlotKind ordinal,SourceOrdinal,OriginRefHash)` 严格排序、
非空、无重复。authority 与 origin 分别使用
`freeagent.governance-requested-content-binding-authority.v1` 和
`freeagent.governance-requested-binding-origin-ref.v1`；两者都是 portable authority relation，
生产 constructor 只接收上述 sealed owner/contribution views。
同一个 BindingBodyHash 的全部有效来源必须由 assembly resolution view 合并进唯一 authority；
不得用不同 origin 子集生成多个 RequestedBinding。partial origin set、重复 body 或同一 actual source
命中多个 binding authority 都失败关闭。

MEMORY contribution 的规范 key 固定为
`(OwnerKind ordinal AGENT=0/WORKSPACE=1,OwnerIdentityHash,ScopeKind ordinal
PRIVATE=0/WORKSPACE=1/TENANT=2,ScopeIdentityHash,RecordKind)`。PRIVATE 只允许 AGENT owner 且
scope identity 等于 exact AgentScopeAuthorityHash；WORKSPACE
scope identity 必须是 sealed WorkspaceScopeAuthorityHash；TENANT scope identity 必须是本
ResolutionScope 的 TenantScopeAuthorityHash。OwnerIdentityHash 逐字段关闭到 exact AgentVersion 或
WorkspaceDefinition parent 及其 immutable scope association 中的 AgentScopeAuthorityHash 或
WorkspaceScopeAuthorityHash，
不能只验证文本/hash 形状。

AgentVersion 升级不删除或改绑由稳定 AgentScopeAuthorityHash 拥有的 MemoryContentVersion。新旧
AgentVersion association 若逐字段指向同一 AgentScopeAuthority，新版本可以在自己的 exact OwnerRef
下发布一个新的 contribution generation，显式重选同一 memory binding；constructor 必须重新验证
最新 ACL/revocation、scope、record kind 与新版本权限，并生成新的 Contribution/Generation/Origin
hash。它不是旧 generation 的 current 激活，也不是跨 owner delegation。不同 AgentScope、Workspace
或 Tenant 之间禁止此路径。这样成长记忆跨版本保留，但旧版本的 attachment/权限不会静默穿透。

ToolResult 是运行时因果结果，不能预装成 requested binding。所有 branch/authority/origin hash 和
外围 RequestedBindingHash 均按各自 domain 删除且仅删除自身 hash 字段计算。RequestedContentKind
wire ordinal 固定为 `SKILL=0,KNOWLEDGE=1,MEMORY=2,MCP_RESOURCE=3,
MCP_RESOURCE_TEMPLATE=4,MCP_PROMPT=5`；bindings 按
`(RequestedContentKind ordinal, RequestedBindingHash)` 严格排序，重复拒绝。
requested→runtime source 映射固定为：SKILL→SKILL、KNOWLEDGE→KNOWLEDGE、MEMORY→MEMORY、
MCP_RESOURCE→MCP_RESOURCE、MCP_RESOURCE_TEMPLATE→MCP_RESOURCE_TEMPLATE_RESULT、
MCP_PROMPT→MCP_PROMPT。前三类没有 MCP selector；后三类内嵌 SelectorTarget.SourceKind 必须等于
映射后的 runtime kind。任何其他转换或按相同字符串猜 family 都拒绝。
MemberSnapshotV2 只能复制这些完整 binding ref；每个 snapshot content binding 必须在 scope
中逐字段相等地出现一次，不能用 source identity 的集合包含关系冒充子集证明。

PURE_CHAT 要求 candidates=`[]`、provider selection policies=`[]`、provider capability scopes=`[]`、
content-binding contribution generation refs=`[]`、requested content bindings=`[]`；后续
MemberSnapshotV2 还必须为零 Module、MCP、Skill、Knowledge、Memory、Provider Secret/Host。
它仍验证并冻结目录 header/Skill anchor，但不读取 anchor 内容来获得权限。MODULAR 允许
0..N executable candidates 和 0..N 内容绑定，所以可以表达“只有 Skill/RAG/Memory”的
成员。requested content 只是装配请求上限，治理不会创建来源；最终 MemberSnapshotV2 必须是
其精确子集。任何模式都不允许治理 resolver 自动选择模块或内容。

MCP tool 在 discovery 前未知，所以 candidate 精确绑定 MCP Provider/entry；resolved Tool
policy 保留该 Provider 的 ANY/EXACT tool rule program，Stage 1 只针对 LockedMCP 发现的精确
tool identity求值。

domain：`freeagent.member-governance-resolution-scope.v1`。

新增 domains：

```text
freeagent.platform-safety-baseline-authority.v1
freeagent.tool-result-classifier-definition.v1
freeagent.tool-result-classifier-ruleset.v1
freeagent.governance-tenant-scope-authority.v1
freeagent.governance-workspace-scope-authority.v1
freeagent.governance-agent-scope-authority.v1
freeagent.governance-workspace-definition-scope-association.v1
freeagent.governance-agent-version-scope-association.v1
freeagent.legacy-scope-identity-bootstrap-entry.v1
freeagent.legacy-scope-identity-bootstrap-root.v1
freeagent.legacy-scope-identity-bootstrap-manifest.v1
freeagent.governance-run-scope-authority.v1
freeagent.governance-invocation-call-position.v1
freeagent.runtime-catalog-generation-ref.v1
freeagent.runtime-catalog-membership-ref.v1
freeagent.governance-exact-provider-identity.v1
freeagent.governance-candidate-provider.v1
freeagent.member-provider-selection-policy.v1
freeagent.governance-provider-capability-scope.v1
freeagent.governance-capability-set.v1
freeagent.runtime-static-capability-evidence.v1
freeagent.governance-source-applicability.v1
freeagent.governance-requested-binding-origin-ref.v1
freeagent.governance-requested-binding-origin-entry.v1
freeagent.content-binding-owner-ref.v1
freeagent.mcp-content-attachment.v1
freeagent.content-binding-contribution-authority.v1
freeagent.content-binding-contribution-generation.v1
freeagent.content-binding-contribution-generation-ref.v1
freeagent.content-binding-contribution-ref.v1
freeagent.content-binding-contribution-no-parent.v1
freeagent.governance-requested-content-binding-authority.v1
freeagent.governance-knowledge-content-binding-ref.v1
freeagent.knowledge-collection-version-authority.v1
freeagent.knowledge-collection-item-version-ref.v1
freeagent.knowledge-collection-version-item-root.v1
freeagent.governance-memory-content-binding-ref.v1
freeagent.governance-requested-mcp-content-binding.v1
freeagent.governance-requested-content-binding.v1
```

## 9. ResolveMemberGovernance 与 all-matches 算法

输入为 ResolutionScope、完整 GovernancePolicyGeneration、与 refs 一一对应的 source
envelopes，以及每个 envelope 引用的四个 leaf document。纯函数不得读取 current、DB、
network、env、clock、global registry。

1. 重验 RuntimeCatalog generation/member/entry closure、Candidate/ProviderCapabilityScope 与
   MemberProviderSelectionPolicy 的同序、全量 set-equality、hash 和 exact member scope closure，
   以及 Governance generation/source/four-leaf hash DAG 与 Skill anchor。
2. 用 `OrderedApplicableLayerScopes` 精确选择 source，并逐项派生
   `COMMON | EXACT_PROVIDER(CandidateHash)` applicability；歧义、未声明 scope、candidate
   不存在或 ProviderCapabilityScopeHash 不等立即失败。
3. source group 顺序严格沿用 Governance generation 的
   `(PolicyLayer.RestrictionOrder, LayerOrdinal)`；不得为 candidate 重新排序。
4. 输出四个 resolved **rule program**：每组保存完整 SourcePolicyRef、applicability、mode、
   leaf hash 与规范 rules/ceilings。INHERIT 组保留为显式审计事实但不限制；四个 component
   都不得提前把所有 Provider 的 source 全局求交。
5. 求值具体 Tool 时，只取 COMMON + 该 Tool exact CandidateHash 的 groups。每个 CEILING
   group 收集全部匹配规则：任一 DENY 即拒绝；没有匹配 ALLOW 即拒绝；所有 ALLOW 的
   permissions 取并集、effect/byte limit 取最小、approval 取最大，每个规则的 selector 在
   当前 binding/candidate/source 下物化后形成独立 AND clause，budget category必须完全相等。MCP execution constraints 按 §5 得到唯一值。
   跨 group 再执行相同合并；没有任何 CEILING group 拒绝。
6. 求值 Content 前先执行唯一 requested-binding witness：Skill 的 membership ref 必须完全相等；
   Knowledge 的 collection ID/version/version hash 必须相等；Memory 的 owner/scope/record-kind
   必须相等；MCP 的 Candidate/catalog membership 必须相等，RequestedContentKind 必须按上表映射
   到 actual SourceKind，且实际 discovery
   locator 必须命中其内嵌 selector。零匹配拒绝，多于一个匹配也因 authority 歧义拒绝；命中的
   完整 RequestedBindingHash 进入 Exact identity、grant、MemberSnapshot parent 与后续 actual-use
   tuple。ToolResult 是唯一例外，只接受已确定的 prior terminal evidence，并使用 typed absence，
   禁止伪造 requested binding。
7. 求值 Skill/Knowledge/Memory 只允许 COMMON groups；任何 PROVIDER_CAPABILITY Content leaf
   包含这三类 rule 时，关闭 source envelope就失败。MCP Resource/Template/Prompt 与
   ToolResult 使用 COMMON + 来源绑定的 exact CandidateHash。对适用 Content groups执行相同
   all-matches；RequiredTags 必须是来源规范 tag set 子集，ProjectionPolicyHash 必须全部相等，
   否则失败；MCP 三类还必须得到唯一非空 MCPRead constraints。
8. Authority 针对当前候选/来源按相同 applicability 过滤后再求 permissions 交集、effect 最小；
   每个 CEILING 的 scope selectors 物化后形成独立 clause。全 INHERIT 或无适用 CEILING 为
   deny-all。不存在跨 candidate 的单一 EffectiveAuthority。
9. Budget 只用于 executable Tool 与 MCP Resource/Template/Prompt read：针对当前 exact
   candidate/category 过滤 groups 后逐 source all-matches；任一 CEILING group 无匹配 ceiling 即
   拒绝，多个 ceiling 逐轴取最小，全 INHERIT 拒绝。Skill/Knowledge/Memory/ToolResult 不进入本步，
   ProviderBudgetLimits 固定为 null，但仍必须满足 Authority。
10. Tool 与 Content 的规则 ceiling 必须再满足同一 candidate/source 的 Authority；仅上述预算适用
   family 再满足 Budget。不得静默
   删除 permission、scope clause、matched rule 或替换 category。结果保存完整 matched-rule
   refs 与 rule-set hash，不合成一个丢失来源的“最终 RuleHash”。
11. PURE_CHAT 输出四个规范空/deny-all resolved documents，source groups=`[]`，同时令
    `OrderedCandidateProviders=[]`、`OrderedProviderSelectionPolicies=[]`、
    `OrderedProviderCapabilityScopes=[]` 与
    `ResolvedMemberGovernancePolicy.OrderedApplicableSourcePolicyRefs=[]`；不读取 leaf
    正文获取权限；snapshot 仍绑定 catalog/governance generation ref closure。MODULAR 即使
    candidates=`[]` 仍可解析 COMMON 内容规则，但只会授权装配明确请求且拥有实际内容
    membership/use authority 的来源。

ANY 与 EXACT 无 precedence：两者同时匹配时都生效。explicit ANY 是治理 ceiling，不是
Provider、Skill 或内容 fallback；最终执行/读取仍要求 exact catalog membership、成员绑定与
actual-use proof。

## 10. Resolved document exact wire

```go
type SourcePolicyRefWireV1 struct {
    PolicyLayer                 PolicyLayer `json:"policy_layer"`
    LayerOrdinal                uint32      `json:"layer_ordinal"`
    ScopeIdentityHash           string      `json:"scope_identity_hash"`
    SourcePolicyVersion         uint64      `json:"source_policy_version"`
    SourcePolicyDocumentHash    string      `json:"source_policy_document_hash"`
    ToolPolicyDocumentHash      string      `json:"tool_policy_document_hash"`
    ContentPolicyDocumentHash   string      `json:"content_policy_document_hash"`
    AuthorityPolicyDocumentHash string      `json:"authority_policy_document_hash"`
    BudgetPolicyDocumentHash    string      `json:"budget_policy_document_hash"`
}

type ResolvedToolSourceGroupV1 struct {
    SourcePolicyRef SourcePolicyRefWireV1 `json:"source_policy_ref"`
    Applicability SourceApplicabilityV1 `json:"applicability"`
    PolicyMode PolicyDocumentMode `json:"policy_mode"`
    ToolPolicyDocumentHash string `json:"tool_policy_document_hash"`
    OrderedRules []ToolPolicyRuleV1 `json:"ordered_rules"`
}

type ResolvedToolPolicyV1 struct {
    SchemaVersion uint32 `json:"schema_version"`
    ResolutionScopeHash string `json:"resolution_scope_hash"`
    OrderedSourceGroups []ResolvedToolSourceGroupV1 `json:"ordered_source_groups"`
    ToolPolicyHash string `json:"tool_policy_hash"`
}

type ResolvedContentSourceGroupV1 struct {
    SourcePolicyRef SourcePolicyRefWireV1 `json:"source_policy_ref"`
    Applicability SourceApplicabilityV1 `json:"applicability"`
    PolicyMode PolicyDocumentMode `json:"policy_mode"`
    ContentPolicyDocumentHash string `json:"content_policy_document_hash"`
    OrderedRules []ContentPolicyRuleV1 `json:"ordered_rules"`
}

type ResolvedContentPolicyV1 struct {
    SchemaVersion uint32 `json:"schema_version"`
    ResolutionScopeHash string `json:"resolution_scope_hash"`
    OrderedSourceGroups []ResolvedContentSourceGroupV1 `json:"ordered_source_groups"`
    ContentPolicyHash string `json:"content_policy_hash"`
}

type ResolvedAuthoritySourceGroupV1 struct {
    SourcePolicyRef SourcePolicyRefWireV1 `json:"source_policy_ref"`
    Applicability SourceApplicabilityV1 `json:"applicability"`
    PolicyMode PolicyDocumentMode `json:"policy_mode"`
    AuthorityPolicyDocumentHash string `json:"authority_policy_document_hash"`
    PermissionCeiling []moduleapi.Permission `json:"permission_ceiling"`
    EffectCeiling moduleapi.EffectClass `json:"effect_ceiling"`
    AllowedScopeSelectors []DataScopeSelectorV1 `json:"allowed_scope_selectors"`
}

type ResolvedAuthorityPolicyV1 struct {
    SchemaVersion uint32 `json:"schema_version"`
    ResolutionScopeHash string `json:"resolution_scope_hash"`
    OrderedSourceGroups []ResolvedAuthoritySourceGroupV1 `json:"ordered_source_groups"`
    AuthorityPolicyHash string `json:"authority_policy_hash"`
}

type ResolvedBudgetSourceGroupV1 struct {
    SourcePolicyRef SourcePolicyRefWireV1 `json:"source_policy_ref"`
    Applicability SourceApplicabilityV1 `json:"applicability"`
    PolicyMode PolicyDocumentMode `json:"policy_mode"`
    BudgetPolicyDocumentHash string `json:"budget_policy_document_hash"`
    OrderedCategoryCeilings []BudgetCategoryCeilingV1 `json:"ordered_category_ceilings"`
}

type ResolvedBudgetPolicyV1 struct {
    SchemaVersion uint32 `json:"schema_version"`
    ResolutionScopeHash string `json:"resolution_scope_hash"`
    OrderedSourceGroups []ResolvedBudgetSourceGroupV1 `json:"ordered_source_groups"`
    BudgetPolicyHash string `json:"budget_policy_hash"`
}

type PolicyLeafKind string // TOOL | CONTENT

type MatchedPolicyRuleRefV1 struct {
    SourcePolicyRef SourcePolicyRefWireV1 `json:"source_policy_ref"`
    Applicability SourceApplicabilityV1 `json:"applicability"`
    PolicyLeafKind PolicyLeafKind `json:"policy_leaf_kind"`
    LeafDocumentHash string `json:"leaf_document_hash"`
    RuleOrdinal uint32 `json:"rule_ordinal"`
    RuleHash string `json:"rule_hash"`
    MatchedRuleRefHash string `json:"matched_rule_ref_hash"`
}

type MatchedPolicyRuleSetV1 struct {
    SchemaVersion uint32 `json:"schema_version"`
    PolicyLeafKind PolicyLeafKind `json:"policy_leaf_kind"`
    OrderedMatchedRuleRefs []MatchedPolicyRuleRefV1 `json:"ordered_matched_rule_refs"`
    MatchedRuleSetHash string `json:"matched_rule_set_hash"`
}

type ExactModuleCapabilityIdentityV1 struct {
    LockedModuleLockHash string               `json:"locked_module_lock_hash"`
    Capability          moduleapi.Capability `json:"capability"`
}

type ExactLockedMCPToolIdentityV1 struct {
    ServerBindingHash  string `json:"server_binding_hash"`
    LockedMCPLockHash  string `json:"locked_mcp_lock_hash"`
    RemoteToolName     string `json:"remote_tool_name"`
    DescriptorDigest   string `json:"descriptor_digest"`
    InputSchemaDigest  string `json:"input_schema_digest"`
}

type ExactToolOperationIdentityV1 struct {
    SchemaVersion       uint32                            `json:"schema_version"`
    CandidateHash       string                            `json:"candidate_hash"`
    RuntimeKind         runtimecatalog.RuntimeKind        `json:"runtime_kind"`
    ModuleCapability    *ExactModuleCapabilityIdentityV1  `json:"module_capability"`
    MCPTool             *ExactLockedMCPToolIdentityV1     `json:"mcp_tool"`
    OperationIdentityHash string                          `json:"operation_identity_hash"`
}

type SkillContentSourceIdentityV1 struct {
    Membership SkillContentCatalogMembershipRefV1 `json:"membership"`
}

type KnowledgeContentSourceIdentityV1 struct {
    Identity           KnowledgeExactIdentityV1 `json:"identity"`
    CollectionVersionHash string                 `json:"collection_version_hash"`
    ContentVersionHash string                   `json:"content_version_hash"`
    ContentPayloadHash string                   `json:"content_payload_hash"`
    SourceTagSet       SourceTagSetV1           `json:"source_tag_set"`
}

type MemoryContentSourceIdentityV1 struct {
    Identity           MemoryExactIdentityV1 `json:"identity"`
    RecordID           string                `json:"record_id"`
    RecordVersion      uint64                `json:"record_version"`
    ContentVersionHash string                `json:"content_version_hash"`
    ContentPayloadHash string                `json:"content_payload_hash"`
}

type MCPResourceContentSourceIdentityV1 struct {
    CandidateHash           string `json:"candidate_hash"`
    CatalogMembershipRefHash string `json:"catalog_membership_ref_hash"`
    ServerBindingHash       string `json:"server_binding_hash"`
    LockedMCPLockHash       string `json:"locked_mcp_lock_hash"`
    URI                     string `json:"uri"`
    Name                    string `json:"name"`
    DescriptorDigest        string `json:"descriptor_digest"`
}

type MCPTemplateContentSourceIdentityV1 struct {
    CandidateHash           string `json:"candidate_hash"`
    CatalogMembershipRefHash string `json:"catalog_membership_ref_hash"`
    ServerBindingHash       string `json:"server_binding_hash"`
    LockedMCPLockHash       string `json:"locked_mcp_lock_hash"`
    URITemplate             string `json:"uri_template"`
    Name                    string `json:"name"`
    DescriptorDigest        string `json:"descriptor_digest"`
}

type MCPPromptContentSourceIdentityV1 struct {
    CandidateHash           string `json:"candidate_hash"`
    CatalogMembershipRefHash string `json:"catalog_membership_ref_hash"`
    ServerBindingHash       string `json:"server_binding_hash"`
    LockedMCPLockHash       string `json:"locked_mcp_lock_hash"`
    Name                    string `json:"name"`
    DescriptorDigest        string `json:"descriptor_digest"`
    ArgumentContractDigest  string `json:"argument_contract_digest"`
}

type ResultClassificationAuthorityV1 struct {
    SchemaVersion             uint32 `json:"schema_version"`
    TenantID                  string `json:"tenant_id"`
    RunID                     string `json:"run_id"`
    MemberID                  string `json:"member_id"`
    ToolResultEvidenceHash    string `json:"tool_result_evidence_hash"`
    SemanticInvocationHash    string `json:"semantic_invocation_hash"`
    OperationIdentityHash     string `json:"operation_identity_hash"`
    RawResultDigest           string `json:"raw_result_digest"`
    ClassifierDefinition      ResultClassifierDefinitionV1 `json:"classifier_definition"`
    ResultClass               string `json:"result_class"`
    ClassificationAuthorityHash string `json:"classification_authority_hash"`
}

type ToolResultContentSourceIdentityV1 struct {
    CandidateHash         string `json:"candidate_hash"`
    SemanticInvocationHash string `json:"semantic_invocation_hash"`
    ToolResultEvidenceHash string `json:"tool_result_evidence_hash"`
    ResultClass           string `json:"result_class"`
    ResultClassificationAuthority ResultClassificationAuthorityV1 `json:"result_classification_authority"`
}

type ContentSourceObjectIdentityV1 struct {
    SchemaVersion       uint32                                     `json:"schema_version"`
    SourceKind          SourceKind                                 `json:"source_kind"`
    Skill               *SkillContentSourceIdentityV1              `json:"skill"`
    Knowledge           *KnowledgeContentSourceIdentityV1          `json:"knowledge"`
    Memory              *MemoryContentSourceIdentityV1             `json:"memory"`
    MCPResource         *MCPResourceContentSourceIdentityV1        `json:"mcp_resource"`
    MCPResourceTemplate *MCPTemplateContentSourceIdentityV1        `json:"mcp_resource_template"`
    MCPPrompt           *MCPPromptContentSourceIdentityV1          `json:"mcp_prompt"`
    ToolResult          *ToolResultContentSourceIdentityV1         `json:"tool_result"`
    SourceObjectIdentityHash string                                `json:"source_object_identity_hash"`
}

type LockedMCPSourceEntryRefV1 struct {
    SchemaVersion    uint32                              `json:"schema_version"`
    SourceKind       SourceKind                          `json:"source_kind"`
    EntryOrdinal     uint32                              `json:"entry_ordinal"`
    Resource         *assembly.LockedMCPResource         `json:"resource"`
    ResourceTemplate *assembly.LockedMCPResourceTemplate `json:"resource_template"`
    Prompt           *assembly.LockedMCPPrompt           `json:"prompt"`
    EntryRefHash     string                              `json:"entry_ref_hash"`
}

type LockedMCPSourceIdentityAuthorityV1 struct {
    SchemaVersion                 uint32     `json:"schema_version"`
    TenantID                      string     `json:"tenant_id"`
    SourceKind                    SourceKind `json:"source_kind"`
    CandidateHash                 string     `json:"candidate_hash"`
    CatalogMembershipRefHash      string     `json:"catalog_membership_ref_hash"`
    ServerBindingHash             string     `json:"server_binding_hash"`
    LockedMCPLockHash             string     `json:"locked_mcp_lock_hash"`
    DiscoveryEntry                LockedMCPSourceEntryRefV1 `json:"discovery_entry"`
    SourceObjectIdentityHash      string     `json:"source_object_identity_hash"`
    MCPSourceIdentityAuthorityHash string    `json:"mcp_source_identity_authority_hash"`
}

type ContentSourceScopeRelationKind string
// SOURCE_SCOPED_TO_PROVIDER | SOURCE_SCOPED_TO_MEMBER | SOURCE_SCOPED_TO_AGENT |
// SOURCE_SCOPED_TO_WORKSPACE | SOURCE_SCOPED_TO_TENANT

type SourceParentAuthorityKind string
// SKILL_MEMBERSHIP | KNOWLEDGE_CONTENT_VERSION | MEMORY_CONTENT_VERSION |
// LOCKED_MCP_SOURCE_IDENTITY | TOOL_RESULT_EVIDENCE

type ContentSourceScopeAuthorityV1 struct {
    SchemaVersion             uint32                         `json:"schema_version"`
    TenantID                  string                         `json:"tenant_id"`
    SourceKind                SourceKind                     `json:"source_kind"`
    SourceObjectIdentityHash  string                         `json:"source_object_identity_hash"`
    RelationKind              ContentSourceScopeRelationKind `json:"relation_kind"`
    ParentScope               DataScopeAlternativeV1         `json:"parent_scope"`
    SourceParentAuthorityKind SourceParentAuthorityKind      `json:"source_parent_authority_kind"`
    SourceParentAuthorityHash string                         `json:"source_parent_authority_hash"`
    ScopeAuthorityHash        string                         `json:"scope_authority_hash"`
}

type ExactContentSourceIdentityV1 struct {
    SchemaVersion                uint32                        `json:"schema_version"`
    SourceObjectIdentity         ContentSourceObjectIdentityV1 `json:"source_object_identity"`
    RequestedBindingWitness      RequestedBindingWitnessV1     `json:"requested_binding_witness"`
    SourceScopeAuthority         ContentSourceScopeAuthorityV1 `json:"source_scope_authority"`
    GovernedSourceIdentityHash   string                        `json:"governed_source_identity_hash"`
}

type SourceAccessCompatibilityProofV1 struct {
    SchemaVersion                  uint32                        `json:"schema_version"`
    TenantID                       string                        `json:"tenant_id"`
    RunID                          string                        `json:"run_id"`
    MemberID                       string                        `json:"member_id"`
    MemberSnapshotV2Hash           string                        `json:"member_snapshot_v2_hash"`
    ScopeUseContextHash            string                        `json:"scope_use_context_hash"`
    GovernedSourceIdentityHash     string                        `json:"governed_source_identity_hash"`
    SourceScopeAuthorityHash       string                        `json:"source_scope_authority_hash"`
    RequestedBindingWitnessHash    string                        `json:"requested_binding_witness_hash"`
    AccessPath                     ScopeAncestryPathV1           `json:"access_path"`
    CompatibilityProofHash        string                        `json:"compatibility_proof_hash"`
}

type ProviderBindingUseKind string // MODULE_TOOL | MCP_TOOL | MCP_CONTENT_READ

type ProviderBindingAdmissibilityProofV1 struct {
    SchemaVersion                       uint32                 `json:"schema_version"`
    UseKind                             ProviderBindingUseKind `json:"use_kind"`
    ResolutionScopeHash                 string                 `json:"resolution_scope_hash"`
    MemberSnapshotV2Hash                string                 `json:"member_snapshot_v2_hash"`
    CandidateHash                       string                 `json:"candidate_hash"`
    CatalogMembershipRefHash            string                 `json:"catalog_membership_ref_hash"`
    BindingAuthorityHash                string                 `json:"binding_authority_hash"`
    OperationIdentityHash               *string                `json:"operation_identity_hash"`
    GovernedSourceIdentityHash           *string                `json:"governed_source_identity_hash"`
    OperationContractPermissionSet       PermissionSetV1        `json:"operation_contract_permission_set"`
    PolicyRequiredPermissionSet          PermissionSetV1        `json:"policy_required_permission_set"`
    EffectiveRequiredPermissionSet       PermissionSetV1        `json:"effective_required_permission_set"`
    GrantedPermissionSet                 PermissionSetV1        `json:"granted_permission_set"`
    OperationContractEffect              moduleapi.EffectClass  `json:"operation_contract_effect"`
    EvaluatedMaxEffectClass              moduleapi.EffectClass  `json:"evaluated_max_effect_class"`
    BindingAdmissibilityProofHash         string                 `json:"binding_admissibility_proof_hash"`
}

type EvaluatedToolGrantV1 struct {
    SchemaVersion uint32 `json:"schema_version"`
    ResolutionScopeHash string `json:"resolution_scope_hash"`
    CandidateHash string `json:"candidate_hash"`
    OperationIdentity ExactToolOperationIdentityV1 `json:"operation_identity"`
    MatchedRules MatchedPolicyRuleSetV1 `json:"matched_rules"`
    RequiredPermissions []moduleapi.Permission `json:"required_permissions"`
    RequiredPermissionSet PermissionSetV1 `json:"required_permission_set"`
    BindingAdmissibilityProof ProviderBindingAdmissibilityProofV1 `json:"binding_admissibility_proof"`
    MaxEffectClass moduleapi.EffectClass `json:"max_effect_class"`
    ApprovalRequirement ApprovalRequirement `json:"approval_requirement"`
    DataScopeClauseSet DataScopeClauseSetV1 `json:"data_scope_clause_set"`
    MaxRequestBytes uint64 `json:"max_request_bytes"`
    MaxResultBytes uint64 `json:"max_result_bytes"`
    BudgetCategory string `json:"budget_category"`
    ProviderBudgetLimits ProviderBudgetLimitsV1 `json:"provider_budget_limits"`
    MCPExecution *MCPToolExecutionConstraintsV1 `json:"mcp_execution"`
    ToolGrantHash string `json:"tool_grant_hash"`
}

type EvaluatedContentGrantV1 struct {
    SchemaVersion uint32 `json:"schema_version"`
    ResolutionScopeHash string `json:"resolution_scope_hash"`
    SourceIdentity ExactContentSourceIdentityV1 `json:"source_identity"`
    RequestedBindingWitness RequestedBindingWitnessV1 `json:"requested_binding_witness"`
    MatchedRules MatchedPolicyRuleSetV1 `json:"matched_rules"`
    RequiredPermissions []moduleapi.Permission `json:"required_permissions"`
    RequiredPermissionSet PermissionSetV1 `json:"required_permission_set"`
    BindingAdmissibilityProof *ProviderBindingAdmissibilityProofV1 `json:"binding_admissibility_proof"`
    DataScopeClauseSet DataScopeClauseSetV1 `json:"data_scope_clause_set"`
    MaxSourceBytes uint64 `json:"max_source_bytes"`
    MaxProjectedBytes uint64 `json:"max_projected_bytes"`
    ProjectionPolicy ProjectionPolicyV1 `json:"projection_policy"`
    MCPRead *MCPContentReadConstraintsV1 `json:"mcp_read"`
    ProviderBudgetLimits *ProviderBudgetLimitsV1 `json:"provider_budget_limits"`
    ContentGrantHash string `json:"content_grant_hash"`
}
```

四个 resolved document 都保存完整 source program；Authority 不再保存跨 candidate 的
`EffectivePermissionCeiling/EffectiveEffectCeiling/OrderedScopeClauses`。Matched ref 唯一
规范顺序是
`(PolicyLayer.RestrictionOrder, SourcePolicyRef.LayerOrdinal, RuleOrdinal)` 且相邻 tuple 严格
递增；Applicability、leaf kind 与 hashes 参与 ref hash 但不改变顺序。该顺序逐项等于 resolved
group 顺序再接 leaf RuleOrdinal，不允许改用 MatchedPolicyRuleRef 的 JCS body 排序。每个命中的规则恰好出现一次，DENY 结果不产生
grant。MatchedRuleSet 不允许空数组，也不允许把多个 rule 合成为一个 synthetic RuleHash。
ExactToolOperationIdentity 与 ContentSourceObjectIdentity 都是严格 union；branch/RuntimeKind/
SourceKind 必须一致，所有引用必须关闭到 Candidate、LockedModule/LockedMCP discovery、Skill
membership、Knowledge/Memory immutable version 或 terminal ToolResultEvidence，调用方不能只传
opaque hash。SourceKind wire ordinal 固定为
`SKILL=0,KNOWLEDGE=1,MEMORY=2,MCP_RESOURCE=3,MCP_RESOURCE_TEMPLATE_RESULT=4,
MCP_PROMPT=5,TOOL_RESULT=6`，共享 `Validate/SortOrder` 是 Content rule、Mapping 与 provenance
排序的唯一入口。

`ContentSourceObjectIdentityV1` 是不含 scope/grant 的先验对象 wire，使用
`freeagent.governance-content-source-object-identity.v1`；SourceObjectIdentityHash 覆盖删除且仅
删除自身 hash 的完整 strict-union body。Knowledge branch 的 ContentVersion/Payload、collection/
item/source identity 与完整 SourceTagSet/Hash 必须逐字段等于 KnowledgeContentVersion parent；
CollectionVersionHash 则必须通过同一 collection/version 的 exact
`KnowledgeCollectionItemVersionRefV1` 与 `KnowledgeCollectionVersionAuthorityV1` 关闭，绝不进入
KnowledgeContentVersionHash preimage。Memory branch 的 RecordID/Version 分别等于
MemoryID/MemoryRecordVersion，owner/scope/RecordKind 必须逐字段等于 immutable Memory version
parent。ToolResult 的 ResultClass 不允许调用方自报：必须来自
`ResultClassificationAuthorityV1`，其 evidence/operation/raw digest 复合指向同一 terminal result，
SemanticInvocationHash 再复合指向携带同一 OperationIdentityHash 的 Semantic parent；完整
ClassifierDefinition 必须逐字段等于 RunMemberBinding 冻结的 trusted definition，ResultClass 必须在
OrderedResultClasses 中且由确定性分类器对 raw result 重算；它使用
`freeagent.tool-result-classification-authority.v1`。`LockedMCPSourceIdentityAuthorityV1` 是从 LockedMCP v3
同一 discovery epoch 的 Resource/Template/Prompt entry 确定性投影出的独立 PA：完整
Candidate/catalog membership/Binding/Lock 与 `LockedMCPSourceEntryRefV1` 必须逐字段相等。
EntryRef 是 strict union，内嵌 exact `assembly.LockedMCPResource/ResourceTemplate/Prompt` 与其分支内
ordinal，使用 `freeagent.locked-mcp-source-entry-ref.v1`；不引入 LockedMCP 中不存在的裸 entry hash；
entry 与 object branch 的 URI/template/name/digests 必须逐字段相等；
它使用 `freeagent.locked-mcp-source-identity-authority.v1`。这样 MCP object parent 不依赖后生成的
Mapping/Association，不形成 hash 环。

`ContentSourceScopeAuthorityV1` 的合法组合是闭表，不接受交叉组合：

| SourceKind/条件 | SourceParentAuthorityKind/Hash | RelationKind | ParentScope |
|---|---|---|---|
| SKILL | SKILL_MEMBERSHIP / MembershipHash | SOURCE_SCOPED_TO_TENANT | exact TENANT_SCOPE_AUTHORITY |
| KNOWLEDGE | KNOWLEDGE_CONTENT_VERSION / ContentVersionHash | SOURCE_SCOPED_TO_TENANT | exact TENANT_SCOPE_AUTHORITY |
| MEMORY + PRIVATE | MEMORY_CONTENT_VERSION / ContentVersionHash | SOURCE_SCOPED_TO_AGENT；OwnerKind 必须 AGENT | exact AGENT_SCOPE_AUTHORITY，identity=OwnerIdentityHash |
| MEMORY + WORKSPACE | MEMORY_CONTENT_VERSION / ContentVersionHash | SOURCE_SCOPED_TO_WORKSPACE | exact WORKSPACE_SCOPE_AUTHORITY，identity=ScopeIdentityHash |
| MEMORY + TENANT | MEMORY_CONTENT_VERSION / ContentVersionHash | SOURCE_SCOPED_TO_TENANT | exact TENANT_SCOPE_AUTHORITY，identity=ScopeIdentityHash |
| MCP_RESOURCE/TEMPLATE_RESULT/PROMPT | LOCKED_MCP_SOURCE_IDENTITY / MCPSourceIdentityAuthorityHash | SOURCE_SCOPED_TO_PROVIDER | exact CANDIDATE_PROVIDER，identity=CandidateHash |
| TOOL_RESULT | TOOL_RESULT_EVIDENCE / ToolResultEvidenceHash | SOURCE_SCOPED_TO_MEMBER | exact RUN_MEMBER_BINDING/MemberScopeIdentityHash |

表中 parent authority 必须提供完整 sealed body，逐字段重算 object hash 与 ParentScope；Knowledge/
Skill 不得自报成 member scope，MCP 不得自报成 tenant scope。每个 object 按表恰好产生一个
ScopeAuthority；不允许为同一 object 选择多个合法 scope 来制造多个 governed identity。
SourceScopeAuthority 必须在 grant/proof/use 之前存在、不得引用 EvaluatedContentGrant、
ScopeAncestryProof、Mapping、Association 或 ContextSourceAuthority。

构造顺序固定为 `source parent -> ContentSourceObjectIdentity -> SourceScopeAuthority ->
RequestedBindingWitness -> ExactContentSourceIdentity/GovernedSourceIdentityHash -> grant`。
Exact identity 的 object hash、scope authority object hash 和 SourceKind 必须完全相等；六种可预装
family 的 witness 必须 PRESENT 并复合指向 ResolutionScope 及最终 MemberSnapshotV2 中同一个完整
binding，ToolResult 必须使用 NOT_APPLICABLE_TOOL_RESULT。`GovernedSourceIdentityHash` 使用
`freeagent.governance-exact-content-source-identity.v1`，只表示调用前治理身份。

实际 CONTENT_SOURCE 使用还必须创建 `SourceAccessCompatibilityProofV1`。若 source parent 是
PROVIDER，AccessPath 的 actual/allowed 都等于 ScopeUseContext 中 exact Candidate 的 PROVIDER
alternative，且路径为空；其他 parent 以当前 MEMBER alternative 为 actual，沿 §3.1 的固定关系
走到 SourceScopeAuthority.ParentScope。Workspace A 来源不能由 Workspace B 的路径通过。proof
必须逐字段绑定同一 MemberSnapshotV2、ScopeUseContext、GovernedSourceIdentity、scope authority
与 requested-binding witness，使用
`freeagent.governance-source-access-compatibility-proof.v1`。这份 source-owner compatibility 与
policy `ScopeAncestryProof` 都必须成立；两者互不替代。

Provider-backed Tool/MCP content 还必须先生成
`ProviderBindingAdmissibilityProofV1`（domain
`freeagent.governance-provider-binding-admissibility-proof.v1`）。四个 PermissionSet 都保存完整
规范 wire；Grant.RequiredPermissions 必须逐项等于 RequiredPermissionSet.OrderedPermissions。
`PolicyRequiredPermissionSet` 必须由同一个 MatchedRuleSet 中全部命中 ALLOW rules 的
RequiredPermissions 做 canonical union 后重算，不能由调用方另报；DENY 不产生 grant。
`EffectiveRequired = canonical union(OperationContract, PolicyRequired)`，并且同时是 exact selected
binding GrantedPermissionSet 与 Authority permission ceiling 的子集，任何缺项都拒绝，不能静默
strip。MODULE 的 OperationContract 来自 LockedModule 对应 manifest 的完整 RequiredPermissions 与
MaxEffectClass，BindingAuthorityHash=LockedModuleLockHash；v1 保守要求整个 manifest.MaxEffectClass
不高于 EvaluatedMaxEffectClass/Authority effect ceiling，直到未来独立 module-operation descriptor
提供更窄 contract。MCP_TOOL 的 contract permissions 由 exact locked approval/mapping 补齐并必须含
`tool.execute`，MCP_CONTENT_READ 的 contract permissions 为规范空集；两者 GrantedPermissionSet
逐项等于 exact EffectiveMCPServer.GrantedPermissions，BindingAuthorityHash=ServerBindingHash。
MCP tool 的 operator effect、MCP content 的 `moduleapi.EffectReadOnly ("read_only")` 都不得超过
Authority ceiling。
strict union 要求 Tool 分支 OperationIdentityHash 非 null/source null，Content 分支相反。
MemberSnapshot/Candidate/catalog/binding 任一替换都使 proof 失效。
Grant.RequiredPermissionSet 必须逐字段等于 proof.EffectiveRequiredPermissionSet，绝不能等于仅含
policy rule 的 PolicyRequiredPermissionSet；RequiredPermissions 数组同序复制该 effective set。
Tool proof 的 EvaluatedMaxEffectClass 必须逐字段等于 EvaluatedToolGrant.MaxEffectClass，并由全部
命中 Tool ALLOW rules、Authority ceiling 与 operation contract 按下述 restriction order 重算；
MCP_CONTENT_READ proof 的 OperationContractEffect 与 EvaluatedMaxEffectClass 都固定为
`moduleapi.EffectReadOnly ("read_only")`，且 Authority ceiling 必须允许该值。只有投影到 Gateway
activation wire 时才映射为大写 `READ_ONLY`；不得让 proof 与 grant 各自保存一套可分歧的 effect。
Mapping、PIA、Seal、Semantic 复制/引用的也只能是这个 effective PermissionSetHash。

EffectClass wire restriction ordinal 固定为
`none=0 < read_only=1 < reversible_write=2 < irreversible_write=3`；“取最小”“不高于”和 ceiling
比较一律调用 moduleapi 公开的共享 Validate/Compare/Min API。禁止使用字符串字典序、Go 常量声明
顺序或数据库 locale 排序。该顺序必须有 literal golden 与所有两两比较测试。

`EvaluatedToolGrantV1` 的 MCPExecution 对 MODULE 必须 null、对获准 MCP tool 必须非 null；
Content 的 Skill/Knowledge/Memory object branch 不含 Candidate，MCP 三类和 ToolResult branch
必须带与 applicable group 相同的 CandidateHash。grant 的 DataScopeClauseSet 包含 Tool/Content
rule clauses 与同一 candidate/source 的 Authority clauses。
Tool grant 的 BindingAdmissibilityProof 必须非空；Content grant 只有 MCP 三类必须非空，
Skill/Knowledge/Memory/ToolResult 必须 null。ToolResult branch 的 Candidate 来自 producer evidence，
不代表本次内容读取有 Provider binding。
MCP 三类 ContentGrant 的 ProviderBudgetLimits 必须非 null，并等于按 MCPRead.BudgetCategory 对
COMMON + exact Candidate budget groups 求得的逐轴最小值；Skill/Knowledge/Memory/ToolResult
必须为 null。MCP Content MappingV2 逐字段复制该 limits，不能只保存 category 后在执行时读取
current budget。EvaluatedContentGrant 的 binding witness 必须逐字段等于 exact identity 的
witness；ToolResult 的 typed absence 是唯一 null 分支。

domains：

```text
freeagent.governance-tool-policy.v1
freeagent.governance-content-policy.v1
freeagent.governance-authority-policy.v1
freeagent.governance-budget-policy.v1
freeagent.governance-matched-policy-rule-ref.v1
freeagent.governance-matched-policy-rule-set.v1
freeagent.governance-exact-tool-operation-identity.v1
freeagent.governance-content-source-object-identity.v1
freeagent.tool-result-classification-authority.v1
freeagent.locked-mcp-source-entry-ref.v1
freeagent.locked-mcp-source-identity-authority.v1
freeagent.governance-content-source-scope-authority.v1
freeagent.governance-exact-content-source-identity.v1
freeagent.governance-source-access-compatibility-proof.v1
freeagent.governance-evaluated-tool-grant.v1
freeagent.governance-evaluated-content-grant.v1
```

每个 resolved document body 删除且仅删除自己的 component hash。source group 顺序严格等于
applicable source refs；组内规则/ceilings 是 leaf 的原规范顺序，不能重新解释。

## 11. 成员 snapshot 与父授权闭包

### 11.1 稳定 policy、RunMemberBinding 与 snapshot

```go
type ResolvedMemberGovernancePolicyV1 struct {
    SchemaVersion uint32 `json:"schema_version"`
    ResolutionScopeHash string `json:"resolution_scope_hash"`
    RuntimeCatalogGenerationRefHash string `json:"runtime_catalog_generation_ref_hash"`
    PolicyGeneration uint64 `json:"policy_generation"`
    PolicyGenerationHash string `json:"policy_generation_hash"`
    OrderedApplicableSourcePolicyRefs []SourcePolicyRefWireV1 `json:"ordered_applicable_source_policy_refs"`
    ToolPolicyHash string `json:"tool_policy_hash"`
    ContentPolicyHash string `json:"content_policy_hash"`
    AuthorityPolicyHash string `json:"authority_policy_hash"`
    BudgetPolicyHash string `json:"budget_policy_hash"`
    PolicyHash string `json:"policy_hash"`
}

type RunMemberBindingV1 struct {
    SchemaVersion uint32 `json:"schema_version"`
    DeploymentTrustDomainID string `json:"deployment_trust_domain_id"`
    PlatformSafetyBaselineAuthorityHash string `json:"platform_safety_baseline_authority_hash"`
    ResultClassifierDefinitionHash string `json:"result_classifier_definition_hash"`
    TenantID string `json:"tenant_id"`
    WorkspaceID string `json:"workspace_id"`
    WorkspaceScopeAuthorityHash string `json:"workspace_scope_authority_hash"`
    WorkspaceScopeAssociationHash string `json:"workspace_scope_association_hash"`
    TaskID string `json:"task_id"`
    RunID string `json:"run_id"`
    MemberOrdinal uint32 `json:"member_ordinal"`
    MemberID string `json:"member_id"`
    AgentScopeAuthorityHash string `json:"agent_scope_authority_hash"`
    AgentScopeAssociationHash string `json:"agent_scope_association_hash"`
    TenantScopeAuthorityHash string `json:"tenant_scope_authority_hash"`
    RunScopeAuthorityHash string `json:"run_scope_authority_hash"`
    ResolutionScopeHash string `json:"resolution_scope_hash"`
    RuntimeCatalogGenerationRefHash string `json:"runtime_catalog_generation_ref_hash"`
    PolicyGeneration uint64 `json:"policy_generation"`
    PolicyGenerationHash string `json:"policy_generation_hash"`
    MemberGovernancePolicyHash string `json:"member_governance_policy_hash"`
    FrozenGovernanceRevocationWatermark uint64 `json:"frozen_governance_revocation_watermark"`
    RunMemberBindingHash string `json:"run_member_binding_hash"`
}

type ResolvedMemberGovernancePolicySnapshotV1 struct {
    SchemaVersion uint32 `json:"schema_version"`
    ResolutionScope MemberGovernanceResolutionScopeV1 `json:"resolution_scope"`
    ResolvedPolicy ResolvedMemberGovernancePolicyV1 `json:"resolved_policy"`
    RunMemberBinding RunMemberBindingV1 `json:"run_member_binding"`
    CanonicalResolvedToolPolicy ResolvedToolPolicyV1 `json:"canonical_resolved_tool_policy"`
    CanonicalResolvedContentPolicy ResolvedContentPolicyV1 `json:"canonical_resolved_content_policy"`
    CanonicalResolvedAuthorityPolicy ResolvedAuthorityPolicyV1 `json:"canonical_resolved_authority_policy"`
    CanonicalResolvedBudgetPolicy ResolvedBudgetPolicyV1 `json:"canonical_resolved_budget_policy"`
    SnapshotHash string `json:"snapshot_hash"`
}
```

`PolicyHash` domain `freeagent.member-governance-policy.v1`，覆盖完整
`ResolvedMemberGovernancePolicyV1` body；它不含 RunID、MemberOrdinal 或 watermark，只用于
稳定等价、缓存和审计，不能单独作为运行授权 parent。

`RunMemberBindingHash` domain `freeagent.run-member-governance-binding.v1`，覆盖运行/成员顺序、
ResolutionScope/catalog generation ref、policy generation/hash、PolicyHash 和冻结 watermark。
MemberOrdinal 必须从已验证的 Team/Run canonical member order 派生；没有接受任意 ordinal 的
production constructor。`SnapshotHash` domain
`freeagent.resolved-member-governance-policy-snapshot.v1`，覆盖完整 scope、policy、binding 与
四个 resolved object，只删除自身字段。任一嵌套 hash 必须逐字段相等，不能用相同 PolicyHash
替换不同 RunMemberBinding/watermark 的 snapshot。

### 11.2 MCP MappingV2 与不修改 LockedMCP v3 的关联

现有 `assembly.MCPToolApprovalMapping` v1 与 LockedMCP v3 规范字节/hash 完全不变。Stage 1
新增以下治理文档：

```go
type MCPToolGovernanceApprovalV2 struct {
    RemoteToolName string `json:"remote_tool_name"`
    OperatorApprovalRevision string `json:"operator_approval_revision"`
    DescriptorDigest string `json:"descriptor_digest"`
    InputSchemaDigest string `json:"input_schema_digest"`
    OperationIdentityHash string `json:"operation_identity_hash"`
    LocalGatewayToolName string `json:"local_gateway_tool_name"`
    LocalGatewayRevision string `json:"local_gateway_revision"`
    RequiredPermissions []moduleapi.Permission `json:"required_permissions"`
    RequiredPermissionSetHash string `json:"required_permission_set_hash"`
    BindingAdmissibilityProofHash string `json:"binding_admissibility_proof_hash"`
    Effect moduleapi.EffectClass `json:"effect"`
    DeliverySemantics assembly.MCPDeliverySemantics `json:"delivery_semantics"`
    Approval ApprovalRequirement `json:"approval"`
    MaxArgsBytes uint64 `json:"max_args_bytes"`
    MaxResultBytes uint64 `json:"max_result_bytes"`
    BudgetCategory string `json:"budget_category"`
    DataScopeClauseSetHash string `json:"data_scope_clause_set_hash"`
    MatchedToolPolicyRuleSetHash string `json:"matched_tool_policy_rule_set_hash"`
    EvaluatedToolGrantHash string `json:"evaluated_tool_grant_hash"`
    ApprovalHash string `json:"approval_hash"`
}

type GatewayEffectV1 string // READ_ONLY | SIDE_EFFECT
type GatewayDeliverySemanticsV1 string
// NATIVE_IDEMPOTENT | RECONCILABLE | DUPLICATE_POSSIBLE
type GatewayApprovalModeV1 string // NONE | REQUIRED

type ToolGatewayDescriptorActivationV1 struct {
    SchemaVersion          uint32                    `json:"schema_version"`
    MemberSnapshotV2Hash   string                    `json:"member_snapshot_v2_hash"`
    CandidateHash          string                    `json:"candidate_hash"`
    CatalogMembershipRefHash string                  `json:"catalog_membership_ref_hash"`
    LockedMCPLockHash      string                    `json:"locked_mcp_lock_hash"`
    ToolGovernanceMappingHashV2 string                `json:"tool_governance_mapping_hash_v2"`
    LockedMCPGovernanceAssociationHash string         `json:"locked_mcp_governance_association_hash"`
    MemberExecutionSealV2Hash string                  `json:"member_execution_seal_v2_hash"`
    ApprovalHash           string                    `json:"approval_hash"`
    Name                   string                    `json:"name"`
    Revision               string                    `json:"revision"`
    Effect                 GatewayEffectV1           `json:"effect"`
    DeliverySemantics      GatewayDeliverySemanticsV1 `json:"delivery_semantics"`
    RequiredCapabilities   []string                  `json:"required_capabilities"`
    MaxArgsBytes           uint64                    `json:"max_args_bytes"`
    MaxResultBytes         uint64                    `json:"max_result_bytes"`
    Approval               GatewayApprovalModeV1     `json:"approval"`
    DescriptorActivationHash string                  `json:"descriptor_activation_hash"`
}

type ToolGatewayModelDefinitionActivationV1 struct {
    SchemaVersion          uint32 `json:"schema_version"`
    MemberSnapshotV2Hash   string `json:"member_snapshot_v2_hash"`
    LockedMCPLockHash      string `json:"locked_mcp_lock_hash"`
    ToolGovernanceMappingHashV2 string `json:"tool_governance_mapping_hash_v2"`
    LockedMCPGovernanceAssociationHash string `json:"locked_mcp_governance_association_hash"`
    MemberExecutionSealV2Hash string `json:"member_execution_seal_v2_hash"`
    ApprovalHash           string `json:"approval_hash"`
    LocalGatewayToolName   string `json:"local_gateway_tool_name"`
    RemoteToolName         string `json:"remote_tool_name"`
    Description            string `json:"description"`
    CanonicalInputSchema   json.RawMessage `json:"canonical_input_schema"`
    InputSchemaDigest      string `json:"input_schema_digest"`
    LockedDescriptorDigest string `json:"locked_descriptor_digest"`
    ModelDefinitionActivationHash string `json:"model_definition_activation_hash"`
}

type MCPApprovedToolActivationBridgeV1 struct {
    SchemaVersion          uint32 `json:"schema_version"`
    DescriptorActivationHash string `json:"descriptor_activation_hash"`
    ModelDefinitionActivationHash string `json:"model_definition_activation_hash"`
    RemoteToolName         string `json:"remote_tool_name"`
    CurrentAdapterDiscoveryDigest string `json:"current_adapter_discovery_digest"`
    BridgeHash             string `json:"bridge_hash"`
}

type MCPToolGovernanceMappingV2 struct {
    SchemaVersion uint32 `json:"schema_version"`
    TenantID string `json:"tenant_id"`
    RunID string `json:"run_id"`
    MemberID string `json:"member_id"`
    MemberSnapshotV2Hash string `json:"member_snapshot_v2_hash"`
    RunMemberBindingHash string `json:"run_member_binding_hash"`
    CandidateHash string `json:"candidate_hash"`
    CatalogMembershipRefHash string `json:"catalog_membership_ref_hash"`
    ServerBindingHash string `json:"server_binding_hash"`
    GovernanceSnapshotHash string `json:"governance_snapshot_hash"`
    FrozenGovernanceRevocationWatermark uint64 `json:"frozen_governance_revocation_watermark"`
    ToolPolicyHash string `json:"tool_policy_hash"`
    OrderedTools []MCPToolGovernanceApprovalV2 `json:"ordered_tools"`
    LegacyApprovalMappingHashV1 string `json:"legacy_approval_mapping_hash_v1"`
    MappingHash string `json:"mapping_hash"`
}

type MCPResourceApprovalIdentityV2 struct {
    URI              string `json:"uri"`
    Name             string `json:"name"`
    DescriptorDigest string `json:"descriptor_digest"`
}

type MCPResourceTemplateApprovalIdentityV2 struct {
    URITemplate      string `json:"uri_template"`
    Name             string `json:"name"`
    DescriptorDigest string `json:"descriptor_digest"`
}

type MCPPromptApprovalIdentityV2 struct {
    Name                   string `json:"name"`
    DescriptorDigest       string `json:"descriptor_digest"`
    ArgumentContractDigest string `json:"argument_contract_digest"`
}

type MCPContentGovernanceApprovalV2 struct {
    SourceKind SourceKind `json:"source_kind"`
    Resource *MCPResourceApprovalIdentityV2 `json:"resource"`
    ResourceTemplate *MCPResourceTemplateApprovalIdentityV2 `json:"resource_template"`
    Prompt *MCPPromptApprovalIdentityV2 `json:"prompt"`
    SourceObjectIdentityHash string `json:"source_object_identity_hash"`
    SourceScopeAuthorityHash string `json:"source_scope_authority_hash"`
    RequestedBindingHash string `json:"requested_binding_hash"`
    GovernedSourceIdentityHash string `json:"governed_source_identity_hash"`
    RequiredPermissions []moduleapi.Permission `json:"required_permissions"`
    RequiredPermissionSetHash string `json:"required_permission_set_hash"`
    BindingAdmissibilityProofHash string `json:"binding_admissibility_proof_hash"`
    Approval ApprovalRequirement `json:"approval"`
    MaxRequestBytes uint64 `json:"max_request_bytes"`
    MaxResultBytes uint64 `json:"max_result_bytes"`
    MaxSourceBytes uint64 `json:"max_source_bytes"`
    MaxProjectedBytes uint64 `json:"max_projected_bytes"`
    BudgetCategory string `json:"budget_category"`
    ProviderBudgetLimits ProviderBudgetLimitsV1 `json:"provider_budget_limits"`
    DataScopeClauseSetHash string `json:"data_scope_clause_set_hash"`
    ProjectionPolicyHash string `json:"projection_policy_hash"`
    MatchedContentPolicyRuleSetHash string `json:"matched_content_policy_rule_set_hash"`
    EvaluatedContentGrantHash string `json:"evaluated_content_grant_hash"`
    ApprovalHash string `json:"approval_hash"`
}

type MCPContentGovernanceMappingV2 struct {
    SchemaVersion uint32 `json:"schema_version"`
    TenantID string `json:"tenant_id"`
    RunID string `json:"run_id"`
    MemberID string `json:"member_id"`
    MemberSnapshotV2Hash string `json:"member_snapshot_v2_hash"`
    RunMemberBindingHash string `json:"run_member_binding_hash"`
    CandidateHash string `json:"candidate_hash"`
    CatalogMembershipRefHash string `json:"catalog_membership_ref_hash"`
    ServerBindingHash string `json:"server_binding_hash"`
    GovernanceSnapshotHash string `json:"governance_snapshot_hash"`
    FrozenGovernanceRevocationWatermark uint64 `json:"frozen_governance_revocation_watermark"`
    ContentPolicyHash string `json:"content_policy_hash"`
    OrderedEntries []MCPContentGovernanceApprovalV2 `json:"ordered_entries"`
    MappingHash string `json:"mapping_hash"`
}

type LockedMCPGovernanceAssociationV1 struct {
    SchemaVersion uint32 `json:"schema_version"`
    TenantID string `json:"tenant_id"`
    RunID string `json:"run_id"`
    MemberID string `json:"member_id"`
    MemberSnapshotV2Hash string `json:"member_snapshot_v2_hash"`
    RunMemberBindingHash string `json:"run_member_binding_hash"`
    CandidateHash string `json:"candidate_hash"`
    CatalogMembershipRefHash string `json:"catalog_membership_ref_hash"`
    ServerBindingHash string `json:"server_binding_hash"`
    LockedMCPLockHash string `json:"locked_mcp_lock_hash"`
    LegacyApprovalMappingHashV1 string `json:"legacy_approval_mapping_hash_v1"`
    ToolGovernanceMappingHashV2 string `json:"tool_governance_mapping_hash_v2"`
    ContentGovernanceMappingHashV2 string `json:"content_governance_mapping_hash_v2"`
    GovernanceSnapshotHash string `json:"governance_snapshot_hash"`
    FrozenGovernanceRevocationWatermark uint64 `json:"frozen_governance_revocation_watermark"`
    AssociationHash string `json:"association_hash"`
}
```

MCP Content approval 是严格 union，矩阵固定为：

| SourceKind | Resource | ResourceTemplate | Prompt |
|---|---:|---:|---:|
| MCP_RESOURCE | non-null | null | null |
| MCP_RESOURCE_TEMPLATE_RESULT | null | non-null | null |
| MCP_PROMPT | null | null | non-null |

Resource/Template 保存 LockedMCP 中的 URI/template、Name 与 DescriptorDigest；Prompt 另有必填
ArgumentContractDigest。所有字段逐字等于
同一 LockedMCP discovery epoch。ResourceTemplate 的运行时参数只在 actual fetch evidence 中
另存 ArgumentDigest 与 ExpandedURI，不能伪造成 discovery argument-contract。其他 SourceKind
不能进入该 Mapping。OrderedEntries 按 `(SourceKind order, URI/template/name 原始 UTF-8 bytes)`
严格排序；locator 或 GovernedSourceIdentityHash 重复均拒绝。同一个 RequestedBindingHash 可以
合法授权多个 discovery source，不能被误当作全局唯一键。entry 的
object/scope/binding/governed identity、matched set、clause set、projection、limits 与 grant hash
必须逐字段等于同一个 EvaluatedContentGrant；Mapping 绝不保存之后才可计算的
ContextSourceIdentityHash。

两个 MappingV2 与 Association 的 MemberSnapshotV2Hash、CandidateHash、
CatalogMembershipRefHash、RunMemberBindingHash、GovernanceSnapshotHash、watermark 和
ServerBindingHash 必须完全相等，并由同列同序 parent UNIQUE 证明 candidate/server 是最终
MemberSnapshot 选中的 attachment；空 tools/content mapping 也不能靠缺少 entry 绕过该闭包。
Tool mapping 在任何复制/排序前限制为 `MaxMCPToolGovernanceMappingEntries=256`（与 legacy
constructor 一致），Content mapping 限制为 `MaxMCPContentGovernanceMappingEntries`；二者都受
MaxMCPGovernanceMappingBytes 限制。

Tool MappingV2 必须确定性投影成现有 v1 `MCPToolApprovalMapping`；投影的完整字段、排序与既有
constructor 验证通过，所得 MappingHash 必须等于 `LegacyApprovalMappingHashV1`，再由
LockedMCP v3 原字段引用。descriptor/input-schema 与适用时的 argument-contract 必须来自同一
LockedMCP discovery evidence；Operator-owned revision/delivery/local gateway mapping 来自命中的治理事实，
不得从 Server annotation 猜测。三份新文档与 LockedMCP record 在同一 Stage 1 transaction
insert-once；Association 将 snapshot/watermark 绑定到未改动的 LockHash。任何替换均失败。

Tool V2 → legacy v1 的逐字段投影固定为：

```text
v1.Name                = V2.RemoteToolName
v1.Revision            = V2.OperatorApprovalRevision
v1.DescriptorDigest    = V2.DescriptorDigest
v1.InputSchemaDigest   = V2.InputSchemaDigest
v1.RequiredPermissions = V2.RequiredPermissions
v1.Effect              = V2.Effect
v1.DeliverySemantics   = V2.DeliverySemantics
v1.Approval            = exact NONE/REQUIRED enum conversion
v1.MaxArgsBytes        = checked int64(V2.MaxArgsBytes)
v1.MaxResultBytes      = checked int64(V2.MaxResultBytes)
```

OperationIdentityHash、LocalGatewayToolName/Revision、budget、scope、matched-set 与 grant hash
不进入 legacy mapping，但必须逐字段等于同一个 EvaluatedToolGrant/LockedMCP tool identity。
OrderedTools 按 RemoteToolName 原始 UTF-8 bytes 严格排序且唯一；投影前必须通过现有 v1
constructor 的 `tool.execute`、non-NONE effect 与 byte upper-bound 检查。为避免 hash 环，Stage 1
先从 evaluated constraints 形成仅含上述 v1 字段的 projection draft并算 legacy MappingHash，再
构造 LockedMCP LockHash；随后关闭 ExactLockedMCPToolIdentity/EvaluatedToolGrant、完整 MappingV2
和 Association。最终 validator 重新投影并逐字段比较，projection draft 本身不是独立 authority。
MappingV2 的 `schema_version=2`，Association 的 `schema_version=1`。

Stage 2 不能手写另一个 ToolGateway descriptor。只有独立 `internal/toolactivation` bridge 包拥有
三种 Activation wire；`internal/governance` 不 import `internal/toolgateway`，Gateway 也不 import
Governance，从而没有包循环。bridge 只能从已验证 MappingV2 entry + Association + SealV2 + 当前
同一 discovery entry 构造 sealed `ToolGatewayDescriptorActivationV1`，并执行固定投影：
Name/Revision 等于 LocalGateway 字段；
`read_only -> READ_ONLY`，`reversible_write|irreversible_write -> SIDE_EFFECT`，`none` 拒绝；
Delivery 与 Approval 做同名 enum exact conversion；RequiredCapabilities 是 RequiredPermissions 的
同序字符串投影且必须包含 `tool.execute`；两个 byte limit 先验证 JSON-safe，再 checked 转成 host
`int`，MaxResultBytes 还不得超过 `toolgateway.MaxToolResultBytes`。descriptor 通过现有
`toolgateway.Descriptor.Validate` 后，ActivationHash 使用
`freeagent.tool-gateway-descriptor-activation.v1` 覆盖完整 body。当前 Gateway 只接受
READ_ONLY+NONE，因此其他可表达组合必须返回 `UNSUPPORTED_ACTIVATION`，不得悄悄降级或放宽。

同一原始 discovered `mcp.Tool` 还必须经 assembly 新增的只读 sealed identity view 重算
LockedMCP DescriptorDigest/InputSchemaDigest，并同时经 `mcphost.DigestDiscoveredTool` 重算当前 adapter
whole-tool DiscoveryDigest。`ToolGatewayModelDefinitionActivationV1` 的 local Name 等于 descriptor
Name，Description 逐字来自该 locked descriptor（trim 后 1..2048 UTF-8 bytes），CanonicalInputSchema
是同一 discovery 的 RFC 8785 object bytes、最多 32 KiB，InputSchemaDigest 必须等于 LockedMCP；
不得用另一个 schema/描述替换。`MCPApprovedToolActivationBridgeV1` 同时绑定 descriptor/model
activation 与 current adapter digest；三个 hash 分别使用同名 `.v1` domain。任何 Lock/Association/
Seal、descriptor/input-schema、remote/local name 或 discovery digest 不等均拒绝注册。

三种 Activation/Bridge 明确分类为可重建 `D`，不是 PA/PH、不是 portable root、不得作为
ProviderInvocationAuthority/Semantic/Attempt 的授权父，也不跨重启导入。CurrentAdapterDiscoveryDigest
只用于当前进程 adapter 一致性和 cache invalidation；缓存 key 必须包含完整三种 hash、
MemberExecutionSealV2Hash 与 current digest。每次 Gateway invoke 必须由 `internal/toolactivation`
重新验证底层 MappingV2 normalized entry、Association、Seal、LockedMCP discovery 与当前 adapter，
或读取同一进程内经相同验证生成且 key 完全相等的 sealed cache；任一变化立即失效并失败关闭。
底层 PA 保持唯一授权来源，派生 descriptor 不能扩大权限。

一个 Mapping 内 RemoteToolName 与 LocalGatewayToolName 分别唯一；在构造整个 Member ToolView 时，
所有 MCP Mapping 和本地 Module tool 的 LocalGatewayToolName 还必须全局唯一。冲突确定性
`UNSUPPORTED_ACTIVATION`，不能用后注册覆盖、revision 或 Provider namespace 暗中消歧。

domains：

```text
freeagent.mcp-tool-governance-approval.v2
freeagent.mcp-tool-governance-mapping.v2
freeagent.mcp-content-governance-approval.v2
freeagent.mcp-content-governance-mapping.v2
freeagent.locked-mcp-governance-association.v1
freeagent.invocation-operation-branch-tuple.v2
freeagent.tool-gateway-descriptor-activation.v1
freeagent.tool-gateway-model-definition-activation.v1
freeagent.mcp-approved-tool-activation-bridge.v1
```

### 11.3 父授权必须绑定的 exact tuple

RunManifest v2 的每个 member ref 必须冻结下列 exact tuple：

```text
MemberOrdinal / MemberID / RunMemberBindingHash
AgentID / AgentVersionID / AgentVersionHash / CompatibilitySpecHash
MemberSnapshotV2Hash
DeploymentTrustDomainID
PlatformSafetyBaselineAuthorityHash
ResultClassifierDefinitionHash
TenantScopeAuthorityHash
WorkspaceScopeAuthorityHash
AgentScopeAuthorityHash
WorkspaceScopeAssociationHash
AgentScopeAssociationHash
RunScopeAuthorityHash
ResolutionScopeHash / RuntimeCatalogGenerationRefHash
PolicyGeneration / PolicyGenerationHash / PolicyHash
ToolPolicyHash / ContentPolicyHash / AuthorityPolicyHash / BudgetPolicyHash
GovernanceSnapshotHash
FrozenGovernanceRevocationWatermark
```

MemberSnapshotV2 冻结同一 tuple 中除 `MemberSnapshotV2Hash`（自身 hash）外的全部字段，并另绑定
Tenant/Task/Run/MemberOrdinal、RuntimeCatalog generation/hash 与 Skill
anchor，并令每个 Module/MCP binding 保存 RuntimeCatalogMembershipRefHash+CandidateHash+
ProviderSelectionPolicyHash+FailurePolicy+ExposureKind、每个
Skill 保存完整 SkillContentCatalogMembershipRef、每个可选 content-binding sidecar 保存完整
ContributionGenerationRef/selected ContributionRef、每个内容 attachment 保存完整
RequestedContentBindingV1/Authority。现有 MemberModuleSnapshot v1 只读兼容，
不得原地改字节/hash。

MemberSnapshotV2 六个 ordered arrays 的 exact set/order/cap 固定如下；所有比较在复制或分配目标
slice 前先做 count/aggregate-byte preflight：

- `OrderedModuleBindings` 元素精确为
  `{RuntimeCatalogMembershipRefHash,CandidateHash,ProviderSelectionPolicyHash,FailurePolicy,
  ExposureKind,LockedModuleLockHash,GrantedPermissionSetHash}`，按
  `(CandidateHash,RuntimeCatalogMembershipRefHash,ProviderSelectionPolicyHash,FailurePolicy ordinal,
  ExposureKind ordinal,LockedModuleLockHash)` 严格递增；
- `OrderedMCPBindings` 元素精确为
  `{RuntimeCatalogMembershipRefHash,CandidateHash,ProviderSelectionPolicyHash,FailurePolicy,
  ExposureKind,ServerBindingHash,GrantedPermissionSetHash}`，按
  `(CandidateHash,RuntimeCatalogMembershipRefHash,ProviderSelectionPolicyHash,FailurePolicy ordinal,
  ExposureKind ordinal,ServerBindingHash)` 严格递增；
- 上述两个数组分别不得重复 Candidate/ref，合计不超过 `MaxCandidateProviders`，其带 ProviderKind 的
  union 必须同时与 ResolutionScope.OrderedCandidateProviders 和
  ResolutionScope.OrderedProviderSelectionPolicies 全量 set-equality；每项
  RuntimeCatalogMembershipRefHash/CandidateHash/ProviderSelectionPolicyHash/FailurePolicy/ExposureKind
  必须逐字段等于同一 policy，不能省略、补入、跨 kind 重包装 candidate 或在 snapshot 中改变选择策略；
- `OrderedSkillBindings` 每项是完整 `SkillContentCatalogMembershipRefV1`，按
  `(MembershipOrdinal,MembershipHash,AssemblySkillRef.ID,AssemblySkillRef.Version,
  AssemblySkillRef.Digest)` 的 typed scalar、原始 UTF-8 bytes 严格递增；所选 subset 的 catalog ordinal 可以有间隔但
  不得重复，计数不超过
  `MaxSkillContentCatalogMemberships`，并与 locked assembly/profile 选择且属于同一 frozen Skill anchor 的
  ref 全量 set-equality；
- `OrderedContentBindingContributionGenerationRefs` 使用 §8 固定
  `(OwnerClassOrder,OwnerRefHash,Generation,GenerationRefHash)` 顺序，同 owner 最多一项，总数不超过
  `MaxContentBindingContributionGenerationRefs`，并与 ResolutionScope 的 refs 全量相等；
- `OrderedSelectedContentBindingContributions` 使用 §8 已锁定的派生全量集合与
  `(OwnerClassOrder,OwnerRefHash,Generation,ContributionOrdinal,ContributionRefHash)` 顺序，计数不超过
  `MaxRequestedContentBindings`；
- `OrderedRequestedContentBindings` 使用 §8 的
  `(RequestedContentKind ordinal,RequestedBindingHash)` 顺序，计数不超过
  `MaxRequestedContentBindings`，且每项完整 Authority/Origin closure 必须逐字段等于 ResolutionScope
  中同一 binding。

为使数据库证明 exact membership 而不是只检查 JSON/hash 形状，ResolutionScope 的 provider-policy
关系必须规范化为 insert-once parent rows，并创建具名 UNIQUE
`uq_resolution_scope_provider_selection_parent_v1`：

```text
{DeploymentTrustDomainID,TenantID,TaskID,MemberID,ResolutionScopeHash,
 ProviderKind,CatalogMembershipRefHash,CandidateHash,
 ProviderSelectionPolicyHash,FailurePolicy,ExposureKind}
```

上述 full parent UNIQUE 保留全部 policy/value 轴，专门供下游同列同序复合 FK 使用；它不是自然
唯一键，因为改变 ProviderSelectionPolicyHash、FailurePolicy 或 ExposureKind 后仍会形成另一条 full
tuple。为机械保证“同一 ResolutionScope 中每个 candidate 恰好一项 policy”，同一 relation 还必须创建
独立具名自然 UNIQUE：

```text
uq_resolution_scope_provider_selection_natural_v1 =
{DeploymentTrustDomainID,TenantID,TaskID,MemberID,ResolutionScopeHash,CandidateHash}
```

该 natural key 故意不包含 ProviderKind、CatalogMembershipRefHash、ProviderSelectionPolicyHash、
FailurePolicy 或 ExposureKind：前两项由 CandidateHash 的 exact candidate/membership parent 逐字段证明；
后三项是本 key 要防止出现第二种值的 policy axes。相同 scope/candidate 的 byte-identical 重交按
insert-once/read-back 处理；任一不同 policy hash、failure 或 exposure 都必须由 natural UNIQUE 冲突，
不能作为第二条 full parent 共存。完整 ResolutionScope canonical array 与 normalized rows 仍须双向
set-equality；natural UNIQUE 不能代替 full parent UNIQUE，full parent UNIQUE 也不能代替 natural UNIQUE。

每个 Module/MCP snapshot binding 必须用上述同列（Module/MCP array 分别提供固定 literal
`ProviderKind=MODULE|MCP`，且 `RuntimeCatalogMembershipRefHash` 映射到
`CatalogMembershipRefHash`）建立直接 composite FK，不能只 FK 到 ResolutionScopeHash 或
ProviderSelectionPolicyHash。snapshot normalized binding rows还必须分别创建供 Seal/PIA/Mapping 使用的
具名 UNIQUE `uq_member_snapshot_module_provider_parent_v2` 与
`uq_member_snapshot_mcp_provider_parent_v2`：

```text
MODULE =
{DeploymentTrustDomainID,TenantID,TaskID,RunID,MemberOrdinal,MemberID,
 MemberSnapshotV2Hash,RunMemberBindingHash,GovernanceSnapshotHash,
 FrozenGovernanceRevocationWatermark,ProviderKind=MODULE,
 RuntimeCatalogMembershipRefHash,CandidateHash,ProviderSelectionPolicyHash,
 FailurePolicy,ExposureKind,LockedModuleLockHash,GrantedPermissionSetHash}

MCP =
{DeploymentTrustDomainID,TenantID,TaskID,RunID,MemberOrdinal,MemberID,
 MemberSnapshotV2Hash,RunMemberBindingHash,GovernanceSnapshotHash,
 FrozenGovernanceRevocationWatermark,ProviderKind=MCP,
 RuntimeCatalogMembershipRefHash,CandidateHash,ProviderSelectionPolicyHash,
 FailurePolicy,ExposureKind,ServerBindingHash,GrantedPermissionSetHash}
```

两个 full snapshot parent UNIQUE 同样保留给 Seal/PIA/Mapping 的完整复合 FK；另外分别创建下列
自然 UNIQUE，保证一个 MemberSnapshotV2 内同一 candidate 在对应 typed binding relation 中至多一项：

```text
uq_member_snapshot_module_provider_natural_v2 =
{DeploymentTrustDomainID,TenantID,TaskID,RunID,MemberOrdinal,MemberID,
 MemberSnapshotV2Hash,CandidateHash}

uq_member_snapshot_mcp_provider_natural_v2 =
{DeploymentTrustDomainID,TenantID,TaskID,RunID,MemberOrdinal,MemberID,
 MemberSnapshotV2Hash,CandidateHash}
```

这两个 natural key 故意排除 ProviderSelectionPolicyHash、FailurePolicy、ExposureKind、
LockedModuleLockHash/ServerBindingHash 与 GrantedPermissionSetHash；它们正是同一 snapshot/candidate
不得以第二种值重包的 policy/value axes。ProviderKind 由 typed relation 的固定 literal 证明，
RuntimeCatalogMembershipRefHash 由 CandidateHash 的 exact membership parent 证明。跨 Module/MCP 的
CandidateHash 重包装还必须被 Candidate.ProviderKind parent 和两个数组 union 的全量 set-equality 拒绝。

本节六个具名 UNIQUE 的名称与列序都是 schema contract，不得合并、缩短或改名：

```text
uq_resolution_scope_provider_selection_natural_v1
uq_resolution_scope_provider_selection_parent_v1
uq_member_snapshot_module_provider_natural_v2
uq_member_snapshot_module_provider_parent_v2
uq_member_snapshot_mcp_provider_natural_v2
uq_member_snapshot_mcp_provider_parent_v2
```

列顺序是 contract；child FK 必须同列同序。不存在省略 FailurePolicy/ExposureKind 的兼容短键，也不能
由 CandidateHash 反查 current selection policy 后补字段。

对仍使用既有逻辑字段名 `BindingHash` 的 MCP child（包括父规范 PIA、Catalog reconciliation route、
Semantic/Attempt/Claim/Lease projection），其直接 FK 中该 child 列必须放在
`uq_member_snapshot_mcp_provider_parent_v2` 的 `ServerBindingHash` 位置，并满足：

```text
child.BindingHash == parent.ServerBindingHash
                  == MemberSnapshotV2.OrderedMCPBindings[i].ServerBindingHash
                  == EffectiveMCPServer.BindingHash
```

这是一个且仅一个锁定的 child-column → parent-column 名称映射，不是第二个 identity、alias hash 或
current lookup。数据库 schema、portable descriptor、New/Restore 与 schema introspection 都必须使用
上述同列 FK；空值、sentinel、`BindingHash`/`ServerBindingHash` 双列分歧、错位到其他 hash 列或跨
member/run 替换均失败关闭。新建 wire 若不受既有名称兼容约束，应直接使用 `ServerBindingHash`。

Catalog membership 也只有一个锁定的 child-column → parent-column 映射：下游 child 的
`CatalogMembershipRefHash` 以及 Catalog route strict pointer 内
`CatalogRouteProviderSelection.CatalogMembershipRefHash` 的活动 physical projection，在直接引用
`uq_member_snapshot_{module|mcp}_provider_parent_v2` 时，都必须位于 parent
`RuntimeCatalogMembershipRefHash` 的列位置并逐字节相等。它们不是第二种 membership identity，也不能
从 current Catalog 反查补值。父规范 `uq_provider_invocation_catalog_target_parent_v2` 自身的命名 parent
顺序是 `CandidateHash,CatalogMembershipRefHash`，而 snapshot parent 的对应顺序是
`RuntimeCatalogMembershipRefHash,CandidateHash`；实现必须为每个 FK 分别构造规范锁定的 ordered column
vector，不得复用另一 parent 的列序、交换两列或拼接多个短 UNIQUE。此映射不改变本节六个具名 UNIQUE
的名称、列序或自然键。

任一数组出现相同 sort key、相同稳定 identity 的第二种正文、要求连续的 generation/contribution ordinal
不连续（所选 Skill catalog subset 的 ordinal 明确允许有间隔）、非全量 set、超 cap 或
顺序差异都失败关闭。PURE_CHAT 的六个数组必须全部为 `[]`；空数组仍参与 MemberSnapshotV2 canonical
body/hash，不能省略或编码为 `null`。

ProviderInvocationAuthorityV2、ModelDispatchAuthorityV2、ContextSourceAuthorityV2、
MemberExecutionSealV2、Tool/Content MappingV2 与 association 必须复合绑定同一
`MemberSnapshotV2Hash + RunMemberBindingHash + GovernanceSnapshotHash +
FrozenGovernanceRevocationWatermark`。
Provider/Seal 还绑定 CandidateHash+CatalogMembershipRefHash+ProviderSelectionPolicyHash+
FailurePolicy+ExposureKind；BUSINESS admission 必须使用 `ORDINARY_TOOLVIEW`，RECONCILIATION admission
必须使用 `RECONCILIATION_ONLY`，两者不能互换。MCP 分支绑定 LockHash、legacy
Mapping v1、两个 MappingV2 与 AssociationHash。需要实时撤销检查的准入比较 current tenant
revocation seq；不能用相同 PolicyHash 替换不同 snapshot。核心模型继续走独立可信内核路径，
不会因 MDA V2 绑定治理 snapshot 而进入 RuntimeCatalog。
所有 Provider-backed PIA/Semantic 还必须逐字段携带同一个
BindingAdmissibilityProofHash+RequiredPermissionSetHash，并复合引用 MemberSnapshot 中的 exact
LockedModule/EffectiveMCPServer binding；Mapping entry 对 MCP 保存相同两列。Seal 按父规范 §14.1
覆盖 MemberSnapshot 选中的完整 Provider 集合，包括 `READY` 与 `UNAVAILABLE` entry：只有 READY MCP
entry 的 `Ready` closure 冻结 lock、legacy Mapping、两个 MappingV2 与 association，UNAVAILABLE entry
必须保留 exact failure branch 且 `Ready=null`。Seal 仍只冻结 selected binding、可用时的 ready closure、
failure 与 GrantedPermissionSetHash，不携带 operation/use 级 proof 或 RequiredPermissionSetHash；任何
具体调用缺失 proof 时不得仅凭 RequiredPermissions 数组继续执行。

本节的 exact-wire 所有权固定分层：本文拥有 Governance leaf/scope/resolution/binding、MemberSnapshotV2
六个 nested ordered arrays 及其六个具名 UNIQUE；父规范拥有 RunManifestV2/MemberSnapshotV2 外层 scalar
envelope，以及 PIA/MDA/CSA/Semantic/Evidence/MemberExecutionSealV2 的完整 outer wire。下列
`MCPContentReadSemanticInvocationV2` 是父规范 §13.2.1 exact outer wire 的 byte-equal 审核镜像，不是可
独立演化、改序或删字段的第二份定义；父规范也不得重解释本文拥有的 nested wire。双方任一镜像不一致都
阻断 locked/stable，而不允许以一方 current 实现覆盖另一方规范。

MCP content read 不复用 Tool 的 Semantic wire，新增独立 strict branch：

```go
type InvocationOperationKind string // TOOL_OPERATION | MCP_CONTENT_READ

type MCPResourceReadRequestV1 struct {
    URI              string `json:"uri"`
    Name             string `json:"name"`
    DescriptorDigest string `json:"descriptor_digest"`
    CanonicalArgumentsDigest string `json:"canonical_arguments_digest"`
    CanonicalArgumentsBytes  uint64 `json:"canonical_arguments_bytes"`
}

type MCPTemplateReadRequestV1 struct {
    URITemplate              string `json:"uri_template"`
    Name                     string `json:"name"`
    DescriptorDigest         string `json:"descriptor_digest"`
    ExpandedURI              string `json:"expanded_uri"`
    CanonicalArgumentsDigest string `json:"canonical_arguments_digest"`
    CanonicalArgumentsBytes  uint64 `json:"canonical_arguments_bytes"`
}

type MCPPromptReadRequestV1 struct {
    Name                     string `json:"name"`
    DescriptorDigest         string `json:"descriptor_digest"`
    ArgumentContractDigest   string `json:"argument_contract_digest"`
    CanonicalArgumentsDigest string `json:"canonical_arguments_digest"`
    CanonicalArgumentsBytes  uint64 `json:"canonical_arguments_bytes"`
}

type MCPContentReadRequestIdentityV1 struct {
    SchemaVersion       uint32                    `json:"schema_version"`
    SourceKind          SourceKind                `json:"source_kind"`
    Resource            *MCPResourceReadRequestV1 `json:"resource"`
    ResourceTemplate    *MCPTemplateReadRequestV1 `json:"resource_template"`
    Prompt              *MCPPromptReadRequestV1   `json:"prompt"`
    CanonicalRequestDigest string                 `json:"canonical_request_digest"`
    CanonicalRequestBytes  uint64                 `json:"canonical_request_bytes"`
    RequestIdentityHash string                    `json:"request_identity_hash"`
}

type MCPContentReadSemanticInvocationV2 struct {
    SchemaVersion                      uint32 `json:"schema_version"`
    InvocationOperationKind            InvocationOperationKind `json:"invocation_operation_kind"`
    DeploymentTrustDomainID            string `json:"deployment_trust_domain_id"`
    TenantID                           string `json:"tenant_id"`
    TaskID                             string `json:"task_id"`
    RunID                              string `json:"run_id"`
    MemberOrdinal                      uint32 `json:"member_ordinal"`
    MemberID                           string `json:"member_id"`
    ResolutionScopeHash                string `json:"resolution_scope_hash"`
    RunManifestHash                    string `json:"run_manifest_hash"`
    InvocationSlotID                   string `json:"invocation_slot_id"`
    InvocationCallPositionHash         string `json:"invocation_call_position_hash"`
    ParentCheckpointHash               string `json:"parent_checkpoint_hash"`
    AdmissionKind                      AdmissionKind `json:"admission_kind"`
    BindingAuthorityHash               string `json:"binding_authority_hash"`
    ReconciliationProviderRouteAuthorityHash string `json:"reconciliation_provider_route_authority_hash"`
    MemberSnapshotV2Hash               string `json:"member_snapshot_v2_hash"`
    RunMemberBindingHash               string `json:"run_member_binding_hash"`
    GovernanceSnapshotHash             string `json:"governance_snapshot_hash"`
    FrozenGovernanceRevocationWatermark uint64 `json:"frozen_governance_revocation_watermark"`
    CandidateHash                      string `json:"candidate_hash"`
    CatalogMembershipRefHash           string `json:"catalog_membership_ref_hash"`
    ServerBindingHash                  string `json:"server_binding_hash"`
    LockedMCPLockHash                  string `json:"locked_mcp_lock_hash"`
    ContentGovernanceMappingHashV2     string `json:"content_governance_mapping_hash_v2"`
    LockedMCPGovernanceAssociationHash string `json:"locked_mcp_governance_association_hash"`
    ContentMappingEntryApprovalHash    string `json:"content_mapping_entry_approval_hash"`
    ProviderInvocationAuthorityV2Hash  string `json:"provider_invocation_authority_v2_hash"`
    EvaluatedContentGrantHash          string `json:"evaluated_content_grant_hash"`
    MatchedContentPolicyRuleSetHash    string `json:"matched_content_policy_rule_set_hash"`
    BindingAdmissibilityProofHash      string `json:"binding_admissibility_proof_hash"`
    RequiredPermissionSetHash          string `json:"required_permission_set_hash"`
    GovernedSourceIdentityHash         string `json:"governed_source_identity_hash"`
    SourceScopeAuthorityHash           string `json:"source_scope_authority_hash"`
    RequestedBindingWitnessHash        string `json:"requested_binding_witness_hash"`
    SourceAccessCompatibilityProofHash string `json:"source_access_compatibility_proof_hash"`
    DataScopeClauseSetHash             string `json:"data_scope_clause_set_hash"`
    ScopeUseContextHash                string `json:"scope_use_context_hash"`
    ScopeAncestryProofHash             string `json:"scope_ancestry_proof_hash"`
    ProjectionPolicyHash               string `json:"projection_policy_hash"`
    MaxSourceBytes                     uint64 `json:"max_source_bytes"`
    MaxProjectedBytes                  uint64 `json:"max_projected_bytes"`
    MaxRequestBytes                    uint64 `json:"max_request_bytes"`
    MaxResultBytes                     uint64 `json:"max_result_bytes"`
    BudgetCategory                     string `json:"budget_category"`
    ProviderBudgetLimits               ProviderBudgetLimitsV1 `json:"provider_budget_limits"`
    ApprovalRequirement                ApprovalRequirement `json:"approval_requirement"`
    RequestIdentity                    MCPContentReadRequestIdentityV1 `json:"request_identity"`
    InvocationOperationBranchTupleHash string `json:"invocation_operation_branch_tuple_hash"`
    SemanticInvocationHash             string `json:"semantic_invocation_hash"`
}
```

RequestIdentity 是三分支 strict union，Resource/Template/Prompt descriptor fields 必须逐字段等于同一
Mapping normalized entry 与 LockedMCP discovery；ContentMappingEntryApprovalHash 必须等于该 exact
normalized entry 的 ApprovalHash；Template/Prompt canonical arguments、expanded URI、
request digest/bytes 在 PREWIRE 前由同源 canonicalizer 产生并受 Mapping limits 约束。Semantic 使用
`freeagent.mcp-content-read-semantic-invocation.v2`，`InvocationOperationKind` 必须严格等于
`MCP_CONTENT_READ`，只允许 BUSINESS_PROVIDER admission。
`AdmissionKind` 固定 `BUSINESS_PROVIDER`；BindingAuthorityHash 必须等于
ProviderInvocationAuthorityV2Hash，route hash 必须逐字段等于同一 BUSINESS PIA 的冻结 route hash 或
父规范 §13.0.1 的 field-specific sentinel。
ProviderInvocationAuthorityV2、DispatchAttempt、InvocationSlotClaim 与 EffectAdmissionLease 都新增
`InvocationOperationKind` strict union：TOOL branch 保存 ToolGrant/ToolRule/OperationIdentity，CONTENT
branch 保存上述 ContentGrant/ContentRule/GovernedSource/RequestIdentity；规范正文的非活动 pointer 固定为
JSON `null`，physical typed expansion 的非活动 branch columns 固定为 SQL `NULL`。不得使用 sentinel、
空字符串、全零或伪造父 hash 混淆不存在的 operation branch；父规范 §13.0.1 的 literal table 只服务于
非 pointer 判别矩阵的 inactive hash。
PIA 与 concrete Semantic 各自保存完整 branch tuple 的同序展开和
同一 tuple hash；ReservationSemanticBinding、Attempt、Claim、Lease 与 terminal evidence 保存
operation kind + tuple hash，并以
`{TenantID,TaskID,RunID,MemberID,SemanticInvocationHash,InvocationOperationKind,
InvocationOperationBranchTupleHash}` 的复合 FK
引用 concrete Semantic 的命名 parent。terminal Retrieval Evidence 的 request/result 字段还必须逐字段
等于 Semantic request；Attempt/Lease 只证明 ToolGrant 时绝不能生成 MCP ContentRetrievalEvidence。
父规范 §13.0 的 embedded `InvocationOperationBranchTupleV2` 是该严格分支的唯一规范展开，使用
`freeagent.invocation-operation-branch-tuple.v2`；PIA 与 concrete Semantic 保存同一 FULL tuple/hash，
后续记录按上一段复合引用同一 semantic/tuple parent。候选不定义第二套 flat/sentinel 编码，也不要求
每一层重复嵌入整份 tuple 正文。

Template/Prompt arguments 必须是 strict JSON object；在任何 map/DOM allocation 前同时检查
MaxSourceAuthorityBytes、MaxGovernanceJSONDepth/Nodes，再以 RFC 8785 生成 canonical argument bytes。
CanonicalArgumentsDigest 固定为
`H("freeagent.mcp-content-canonical-arguments.v1", canonical_argument_bytes)`；Resource 的
CanonicalArgumentsBytes=0 且 CanonicalArgumentsDigest 固定为
`H("freeagent.mcp-content-no-arguments.v1",JCS({"schema_version":1}))`。Template ExpandedURI 是对
LockedMCP 原始 URITemplate 执行 RFC 6570 expansion 得到的逐字 UTF-8 bytes，不 trim、不 URI
normalize，大小受 assembly MCP identity cap；同样的 args bytes 必须进入 Attempt request-payload
parent，Restore 重算 digest。`CanonicalRequestDigest/CanonicalRequestBytes` 计量的是实际交给 MCP
adapter 的 canonical operation payload，不是包含 descriptor/identity metadata 的 RequestIdentity JCS。
payload 固定为下列 strict JSON 之一并以 RFC 8785 编码：

```text
RESOURCE:          {"method":"resources/read","params":{"uri":Resource.URI}}
RESOURCE_TEMPLATE: {"method":"resources/read","params":{"uri":ResourceTemplate.ExpandedURI}}
PROMPT:            {"method":"prompts/get","params":{"arguments":<canonical arguments object>,
                                                       "name":Prompt.Name}}
```

`CanonicalRequestDigest = H("freeagent.mcp-content-canonical-request.v1",payload_bytes)`，
`CanonicalRequestBytes = len(payload_bytes)`，且该值才与 `MaxRequestBytes` 比较。Prompt payload 中的 arguments
bytes 必须逐字等于上述 canonical argument bytes；Resource/Template payload 不携带 arguments。
JSON-RPC request ID、session ID、headers 和 transport framing 不进入 semantic payload；它们只进入
Attempt observation，且不能改变 operation identity。RequestIdentityHash 使用
`freeagent.mcp-content-read-request-identity.v1` 覆盖完整三分支 identity body（包括已算出的实际 payload
digest/bytes）；identity body 自身的 JCS 长度不得冒充 provider request bytes。Semantic.ProviderBudgetLimits
必须逐字段等于 EvaluatedContentGrant 与 Content Mapping entry，不存在未定义的 digest。

### 11.4 actual-use authority 的版本边界

父规范中现有只含 `DataScopeHash` 或单一 `Matched*RuleHash` 的 v1 文档保持 audit-only 原字节，
不得原地重解释。新执行只写下列 V2 authority，并使用新的 `.v2` hash domain：

| authority | 必须新增/替换的治理字段 |
|---|---|
| ProviderInvocationAuthorityV2 | TaskID、MemberSnapshotV2Hash、RunMemberBindingHash、GovernanceSnapshotHash、Frozen watermark、CandidateHash、CatalogMembershipRefHash、ProviderSelectionPolicyHash、FailurePolicy、ExposureKind、BindingAdmissibilityProofHash、RequiredPermissionSetHash；BUSINESS 只接受 ORDINARY_TOOLVIEW，RECONCILIATION 只接受 RECONCILIATION_ONLY；MCP 分支再绑定 association 与两类 MappingV2；按 AdmissionPurpose 使用父规范 §13.0 的 `ReconciliationProviderRouteLinkV2` strict pointer 及 ReconciliationProviderRouteAuthorityHash/ReconciliationAuthorityHash/ReconciliationGateHash exact matrix |
| SemanticInvocationDocumentV2 | `InvocationOperationKind=TOOL_OPERATION`；Tool operation identity、EvaluatedToolGrantHash/MatchedToolPolicyRuleSetHash、BindingAdmissibilityProofHash、RequiredPermissionSetHash、DataScopeClauseSetHash、ScopeUseContextHash、ScopeAncestryProofHash、CanonicalRequestDigest/CanonicalRequestBytes/TargetHash、ExactBudgetCategory/full ProviderBudgetLimitsV1/ApprovalRequirement |
| MCPContentReadSemanticInvocationV2 | `InvocationOperationKind=MCP_CONTENT_READ`；三分支 RequestIdentity、EvaluatedContentGrantHash/MatchedContentPolicyRuleSetHash、BindingAdmissibilityProofHash、RequiredPermissionSetHash、DataScopeClauseSetHash、ScopeUseContextHash、ScopeAncestryProofHash、exact budget category/request digest |
| ModelDispatchAuthorityV2 | RunMemberBindingHash、GovernanceSnapshotHash、Frozen watermark；核心 ModelRoute 轴原样保留 |
| Skill/Knowledge/Memory use authority V2 | MemberSnapshotV2Hash、EvaluatedContentGrantHash、GovernedSourceIdentityHash、SourceScopeAuthorityHash、RequestedBindingWitness/Hash、SourceAccessCompatibilityProofHash、MatchedContentPolicyRuleSetHash、DataScopeClauseSetHash、ScopeUseContextHash、ScopeAncestryProofHash、ContextSourceIdentityHash 与原 leaf/content parent |
| MCPContentRetrievalEvidenceV2 | 上述完整 content tuple、association、ContentMappingV2、严格 runtime locator/arguments、ContextSourceIdentityHash 与 terminal Attempt/Lease |
| ToolResultEvidenceV2 | 原 Provider/Semantic/Attempt/Lease、EvaluatedToolGrantHash、clause-set/use-context/proof |
| ToolResultContentUseAuthorityV2 | terminal ToolResultEvidenceV2、typed binding absence、EvaluatedContentGrantHash、governed/context identity、source-scope、ResultSettledCheckpointOrdinal/Hash、ResultCheckpointKind、ResultBranchLineageHash、当前 ParentCheckpointHash 与 CheckpointAncestryProofHash，以及两种 actual-use scope proof；它不重放工具 |
| ContextSourceAuthorityV2 | MemberSnapshotV2Hash、RunMemberBindingHash、GovernanceSnapshotHash、Frozen watermark、EvaluatedContentGrantHash、GovernedSourceIdentityHash、ContextSourceIdentityHash、SourceScopeAuthorityHash、RequestedBindingWitness/Hash、SourceAccessCompatibilityProofHash、OrderedMatchedContentPolicyRuleRefs、MatchedContentPolicyRuleSetHash、DataScopeClauseSetHash、ScopeUseContextHash、ScopeAncestryProofHash |
| ContextContentProvenanceV2 | 镜像并复合引用上述 ContextSourceAuthorityV2 全部治理 tuple |

父规范当前的 PIA active projection parent 名称固定为
`uq_business_pia_none_route_call_parent_v2`、
`uq_business_pia_catalog_route_call_parent_v2`、
`uq_business_pia_core_route_call_parent_v2`、
`uq_reconciliation_pia_module_call_parent_v2` 与
`uq_reconciliation_pia_mcp_call_parent_v2`。这些是父规范拥有的 outer-wire/FK 名称；同步这些名称不把
route、reconciliation 或 Seal 字段加入任何 4B2 leaf、resolved policy、MemberSnapshot nested binding
或其 hash preimage。

`GovernedSourceIdentityHash` 是调用前 policy/grant/mapping 身份；它不包含 runtime arguments、
expanded URI、terminal evidence 或 Association。`ContextSourceIdentityHash` 是调用后、
family-specific 的实际上下文身份：每个 leaf evidence 按独立 `.v2` domain 覆盖同一个 governed
identity、EvaluatedContentGrantHash、实际 locator/arguments/result parent 与 terminal evidence，
再由 ContextSourceAuthority/Provenance 同时保存两者。Context identity preimage 使用父规范 §14.5
锁定的 common+strict branch fields，不包含正在构造的 actual-use leaf hash；leaf 随后把
ContextSourceIdentityHash 纳入自身 body/hash，CSA 再引用该 leaf hash，因此不存在自环。
ToolResult 必须经独立 ToolResultContentUseAuthorityV2，不能让更早的 ToolResultEvidence 反向依赖
后续 content grant。两种 identity 绝不要求数值相等，也不得只保留其中
一个；这个单向 `Governed -> terminal leaf -> Context` 关系避免 grant/Mapping 反向依赖结果。

v1 raw ToolResult 明确保持 producer MEMBER-private；它不建立 MEMBER→MEMBER ancestry，也不能因
同 Run/Team 自动共享。这不禁止多 Agent 协作：普通消息/任务产物仍走其既有显式交付路径。若要把
精确 raw result 或其投影作为另一成员的上下文来源，后续必须外挂独立
`contentpublication` 模块，产生带 producer、terminal evidence、recipient scope、projection、
revocation/retention 的新版本 authority/source kind；不得在本 v1 中把 TOOL_RESULT scope 静默放宽
到 RUN/WORKSPACE。该扩展是已知后续项，不阻塞私有 ToolResult 的 4B2 实施。

MEMBER-private 只解决 owner，不证明 checkpoint 因果顺序。ToolResult 的 pre-grant
ContentSourceScopeAuthority 仍只绑定 terminal evidence/member scope，不得反向引用后续 model call；
真正使用时，ToolResultContentUseAuthority、ContextSourceAuthority 与
provenance 必须逐字段保存父规范 §14.5 的 settled checkpoint ordinal/hash、checkpoint kind、branch
lineage、当前 ModelCall ParentCheckpointHash 与完整 CheckpointAncestryProofHash，并以复合 FK 证明
settled checkpoint 是当前 parent 的严格祖先。当前调用、自身未来结果、sibling branch、错误 lineage
或仅同 member 的结果一律拒绝。该 proof 位于 terminal evidence 之后、content use 之前，既不能塞回
ToolResultEvidence，也不能塞进 pre-grant SourceScope，从而保持单向 DAG。
ContextSourceIdentity 继续只绑定 ModelCall position 与 terminal ToolResult evidence，不包含 ancestry
proof；proof 由后置 use authority/CSA/provenance 复合关闭，避免产生第二套 identity preimage。

`ScopeAncestryProofHash` 是 actual-use policy 证明，SourceAccessCompatibilityProofHash 是来源
owner 与访问者兼容证明；两者都不能在 policy resolution 时预造。同一个
DataScopeClauseSet 可以在不同来源/调用使用不同 proof。每个 V2 authority 的复合 parent key、
复合 tuple 按生成阶段严格拆开：pre-use Mapping/Association/Seal 只绑定 MemberSnapshot、
RunBinding、GovernanceSnapshot、watermark、Candidate/catalog/Binding/Lock；Mapping entry 还可绑定
grant、GovernedSourceIdentity、requested binding、source scope、matched set 与 ClauseSet，但严禁
引用尚未发生的 ScopeUseContext、两种 proof 或 ContextSourceIdentity。actual-use leaf/CSA/
provenance/Manifest 才在上述 pre-use 父链后增加 ScopeUseContext、ScopeAncestryProof、
SourceAccessCompatibilityProof 与 ContextSourceIdentity。各阶段自己的 parent FK 都必须同列同序，
多个独立 UNIQUE 不能拼接代替一个完整 parent。v1 历史可以恢复、迁移和
审计，但不得创建新的 Provider/Model dispatch，也不得被自动 backfill 成 V2。

上述父 V2 的完整 body、自身 hash 字段和 `.v2` domain 由父规范
`2026-07-21-runtime-catalog-authority-design.md` 的 §13.0、§13.2.1、§14.1、§14.4 与 §14.5
共同锁定；本表只定义新增治理字段，不能替代完整 wire。若父 V2 从未发布，可在首次落盘前冻结
这些字段；若已有任何 V2 bytes/schema 兼容承诺，必须提升到 V3 和新 domain，禁止静默重写。
父规范尚未同步这些字段或复合 FK 时，本候选不得标记 stable。

## 12. byte/计量定义

- `MaxRequestBytes`：Provider-specific canonicalizer 完成后、transport framing 前的精确请求
  payload bytes；该 digest/byte count 进入 SemanticInvocationDocument。
- `MaxResultBytes`：Provider-specific canonicalizer 完成后、release/projection 前的精确结果
  payload bytes。
- `MaxSourceBytes`：进入 Projection 前、ContentPayload 中的精确 source bytes。
- `MaxProjectedBytes`：ProjectionPolicy 算法生成的精确输出 bytes。
- 任何 compression/base64/HTTP framing/数据库 JSON 表示都不能替换上述计量点。

Provider budget reservation/settlement 使用 §7 的同一 bytes/digest 与 DispatchAttempt，避免
同一 policy hash 在不同实现中产生不同成本判定。

## 13. API、golden 与测试门

每种 leaf/scope/resolved/grant/policy/binding/snapshot/MappingV2/association 类型都提供：

```text
New... / Restore... / Validate / CanonicalJSON / Hash or ResolutionView
```

生产 resolver 只接受已验证 closed types。必须提供 literal golden vectors：

1. 四个 INHERIT 空 leaf；
2. 四个 CEILING deny-all leaf；
3. ANY Module + ANY capability baseline；
4. exact MCP + ANY tool policy；
5. 非空 RuntimeCatalog/Skill anchor 下 pure_chat 零 candidate/零 provider-selection policy/零
   attachment 的空 resolved documents/snapshot；
6. MODULAR 零 executable Provider、仅 Skill+Knowledge+Memory 的完整内容闭包；
7. 一个含 Module+MCP 的 MODULAR member 的完整 catalog membership→source→generation→
   provider-selection policy→resolved→RunMemberBinding→snapshot DAG；
8. LockedMCP v3 + legacy Mapping v1 + Tool/Content MappingV2 + association 的固定字节投影；
9. REQUIRED/OPTIONAL × ORDINARY_TOOLVIEW/RECONCILIATION_ONLY 的 literal
   MemberProviderSelectionPolicyV1 vectors，以及 ResolutionScope selection、Module snapshot、MCP snapshot
   三组 natural/full-parent 共六个具名 UNIQUE 的同列同序 schema vectors；
10. legacy child `BindingHash` 到 snapshot parent `ServerBindingHash` 的固定列映射与 byte-identical
    EffectiveMCPServer binding vector；
11. 下游 `CatalogMembershipRefHash` 与 Catalog route
    `CatalogRouteProviderSelection.CatalogMembershipRefHash` 的活动 projection 到 snapshot parent
    `RuntimeCatalogMembershipRefHash` 的固定列映射，以及 PIA target parent 与 snapshot parent 两种不同
    column order 的 literal schema vectors；
12. 父规范 `MemberExecutionSealV2` READY/UNAVAILABLE exact outer wire、两个 branch natural UNIQUE、
    两个 READY parent UNIQUE，以及 BUSINESS NONE/CATALOG/CORE route parent 的 literal schema vectors；
13. 候选与父规范 `MCPContentReadSemanticInvocationV2` 的 byte-equal literal golden，明确包含
    DeploymentTrustDomainID、MemberOrdinal、ResolutionScopeHash 与 RunManifestHash 四个 outer axes。

测试至少覆盖：

- ANY+EXACT all-matches、无 precedence、DENY wins；
- PLATFORM ANY baseline 能约束未来 exact catalog Provider，而不授予 membership；
- INHERIT 不授予且不把旧 Profile inherit 误编译为 deny；
- 相同 selector/effect、不同 constraints 共存并全部收窄；完整 body 重复拒绝；
- RuntimeCatalog tenant/generation/hash/ordinal/entry/Skill anchor 任一替换失败；CandidateHash 与
  ProviderCapabilityScopeHash 重算，Provider A 的 EXACT group 不影响 Provider B；
- Provider selection policy 的 domain/hash、闭集/ordinal、同序 count/index 与 candidate 全量
  set-equality；遗漏/额外/重复/乱序、跨 kind/candidate/ref 重包装、FailurePolicy 或 ExposureKind
  替换、加入未知 RunID/MemberOrdinal key 均拒绝；同一 ResolutionScope/CandidateHash 插入第二条
  byte-identical policy 只能 read-back，替换 ProviderSelectionPolicyHash、FailurePolicy、ExposureKind、
  ProviderKind 或 CatalogMembershipRefHash 的第二条 relation 必须由 natural UNIQUE 或 exact candidate
  parent 失败关闭；
- snapshot Module/MCP binding 的 policy tuple 替换、BUSINESS/RECONCILIATION exposure 互换、
  REQUIRED failure 被当成 READY、OPTIONAL failure 触发 fallback，以及三个具名 composite parent key
  任一列缺失/换序/跨 member-run 拼接均拒绝；同一 MemberSnapshotV2/CandidateHash 的第二条 Module
  或 MCP binding 即使只替换 policy/failure/exposure、lock/server binding 或 granted permission 也必须由
  typed natural UNIQUE 冲突；schema introspection 必须证明本节六个具名 UNIQUE 全部存在、列名/列序
  精确且 natural key 未包含被排除的 policy/value axes；
- MCP child `BindingHash` 必须在 composite FK 的 `ServerBindingHash` 位置逐字段相等；缺映射、双列值
  分歧、映射到错误 parent 列、使用 sentinel/current 补值或跨 member/run 替换均拒绝；SQLite 与
  experimental PostgreSQL 的 schema introspection/round-trip 必须得到相同列序；
- PIA/Seal child 的 `CatalogMembershipRefHash` 与 Catalog route
  `CatalogRouteProviderSelection.CatalogMembershipRefHash` 的活动 projection 必须在 snapshot composite FK
  的 `RuntimeCatalogMembershipRefHash` 位置逐字段相等；把
  `uq_provider_invocation_catalog_target_parent_v2` 的 `CandidateHash,CatalogMembershipRefHash` 顺序误用于
  snapshot parent、交换两列、从 current 补值或拼接短 UNIQUE 均拒绝；
- schema introspection 必须同时证明父规范的
  `uq_member_execution_seal_module_provider_natural_v2`、
  `uq_member_execution_seal_mcp_provider_natural_v2`、
  `uq_member_execution_seal_ready_module_provider_parent_v2`、
  `uq_member_execution_seal_ready_mcp_provider_parent_v2`，以及
  `uq_business_pia_none_route_call_parent_v2`、
  `uq_business_pia_catalog_route_call_parent_v2`、
  `uq_business_pia_core_route_call_parent_v2`、
  `uq_reconciliation_pia_module_call_parent_v2`、
  `uq_reconciliation_pia_mcp_call_parent_v2` 的名称、活动 branch、列名与列序；READY/UNAVAILABLE、
  NONE/CATALOG/CORE 任一错分支、nullable base 宽 FK 或未分支的兼容短名称均拒绝；
- PURE_CHAT/MODULAR、零 Provider 内容成员、requested attachment/candidate-set/provider-policy-set 替换；
- DataScope OR-within/AND-across、每 clause 恰好一个 witness、零边自身命中、多边 ancestry，
  以及断链/环/unused edge/错误 relation kind/错误 authority origin/跨租户 proof 拒绝；
- tag narrowing、source tag evidence、MCP exact bytes 与 LockedMCP 同源；
- MCP Template 2048/RFC6570 边界；Tool/Prompt/Resource 64KiB 边界；远端/本地 Tool identity
  交换、缺 operator revision/delivery 或多个命中值冲突均失败；
- Projection 两算法的 literal input/output；
- Provider budget 并发 reservation、UNKNOWN 保留、NOT_EXECUTED 释放；
- 全部整数的 `assembly.MaxJSONSafeInteger` 边界与先验分配 count limit；
- MatchedRuleSet 跨 source/all-matches 顺序、单 rule 替换和 synthetic 单 hash 拒绝；
- PolicyHash/RunID 隔离、RunMemberBindingHash/member ordinal、SnapshotHash/watermark 与全部 V2
  parent 的复合绑定；
- Tool MappingV2 到 legacy Mapping v1 的确定性投影；Lock/legacy/tool/content/snapshot 任一
  association 替换失败；LockedMCP v3 与 Mapping v1 既有 golden bytes 一字节不变；
- strict restore、每个 union 错分支、null/empty、limits、alias isolation；
- MemberSnapshot 不含 operation proof、`snapshot -> proof -> snapshot` 自环构造失败，以及一个 binding
  派生多个 operation proofs 的合法基数；
- baseline/classifier/Tenant/bootstrap 任一 DeploymentTrustDomainID 替换或跨部署 roots 拼接失败；
- contribution current generation 冻结、历史代/foreign owner/跨 tenant TASK 重包装失败；selected
  contribution 全量集合缺项、多项、未使用项、乱序、重复均失败；portable Restore 不推进 current；
- bootstrap expected/actual result root、EntryCount、sequence、proof/marker 复合绑定与二次消费失败；
- PolicyRequiredPermissions 从 MatchedRuleSet 重算、effective union、proof/grant effect 相等；EffectClass
  全部两两 Compare/Min golden 及 `read_only -> READ_ONLY` gateway 投影；
- MCP content canonical arguments/no-args/request literal vectors、Tool/Content branch 混淆，以及 branch
  tuple 在 PIA→Semantic→ReservationBinding→Attempt→Claim/Lease→terminal evidence 任一替换失败；
- 候选镜像与父规范 `MCPContentReadSemanticInvocationV2` 的 strict restore/canonical bytes/hash 必须
  byte-equal；DeploymentTrustDomainID、MemberOrdinal、ResolutionScopeHash、RunManifestHash 任一缺失、
  替换、换序、跨 deployment/member/scope/run 拼接或只从 current 补值均失败；Mapping/Association/Approval
  顺序或字段集合分歧，以及 Tool Semantic 缺少 CanonicalRequestDigest/CanonicalRequestBytes/TargetHash
  或 full budget limits 也均失败；
- ToolResult settled checkpoint 必须是当前 ParentCheckpoint 的严格祖先；当前、未来、sibling branch、
  错 lineage 或只同 member 均失败，且 ContextSourceIdentity preimage 不含 ancestry proof；
- shuffle、`GOEXPERIMENT=jsonv2`、vet；race 在具备 CGO/GCC 的发布环境执行。

在上述 exact types、domains、limits和 golden tests 同时存在之前，4B2 不能被标记 stable，
也不能让 SQLite v2 或 RunManifest v2 引用未验证的 placeholder hash。
