# FreeAgent Governance 4B2 rule wire 与成员治理解析（已替代草案）

> S0 状态：`HISTORICAL_NON_NORMATIVE`。仅保留旧审查轨迹，不再指导当前实现；当前架构权威见本目录 `README.md`。

状态：**禁止实施；已由 `2026-07-22-governance-rule-wire-v1-lock.md` 替代**。

本文件保留第一次审查轨迹。它存在 exact-every-source 阻断开放 Provider、DataScope
错误地把不同层级当原子集合求交、snapshot watermark 未进入父授权闭包、MCP identity
与 LockedMCP v3 不同源等问题，任何 schema、hash domain 或代码不得以本文件为依据。

## 1. 目的与原始设计影响

本文补全 `2026-07-21-runtime-catalog-authority-design.md` §10.3 尚未锁定的
Tool、Content、Authority、Budget leaf document、解析规则和成员治理快照 wire。

这不改变原始产品模型：

- Agent、Workspace、Profile、Skill、Module、MCP 仍可自由组合；
- Skill 仍是内容/工作流，不是 executable Provider；
- `pure_chat` 仍可在非空目录下显式选择零 Provider；
- 没有全局 Provider fallback；
- 本文只把“当时允许什么”变成可恢复、可验证的规范证据。

首版故意采用精确 selector 与 fail-closed 交集，不引入 wildcard、正则、
last-writer-wins 或无法证明包含关系的“更窄”推断。以后增加 selector lattice 必须提升
schema/hash domain，不能重解释 v1。

## 2. 通用 wire 规则

- 所有文档 `schema_version = 1`。
- 所有数组都必须出现；空集合编码为 `[]`，禁止 `null`。
- 所有 hash 是 64 位小写十六进制 SHA-256。
- 所有整数必须位于 `0..9007199254740991`；明确要求正数的字段从 1 开始。
- 文本必须是规范 UTF-8、去首尾空白、不得包含控制字符；opaque identity 最长
  512 bytes，名称/类别/标签最长 128 bytes。
- 集合先按规范 key 排序并拒绝重复；规则数组按本文指定 key 排序。
- 构造器可接受任意输入顺序并产生 RFC 8785 bytes；Restore 只接受与重新生成结果
  逐字节相等的文档，拒绝未知/缺失/重复 key、尾随值和非规范数字。
- 文档 hash 的 preimage 是删除且仅删除自身 hash 字段后的完整规范正文。
- 每个对象的 `CanonicalJSON()` 和所有 slice/bytes/view 都必须防别名。

## 3. 通用枚举与集合

### 3.1 RuleEffect 与 ApprovalRequirement

```text
RuleEffect          = ALLOW | DENY
ApprovalRequirement = NONE | REQUIRED
```

审批限制序为 `NONE < REQUIRED`，多层合并取更严格值。v1 不接受其他审批等级。

### 3.2 DataScope

```json
{
  "scope_kind": "TENANT|WORKSPACE|AGENT|MEMBER|TASK|RUN|PROVIDER|SOURCE",
  "scope_identity_hash": "<sha256>"
}
```

DataScope 集合按 `(scope_kind, scope_identity_hash)` 排序。多个 ceiling 取规范集合交集；
空集合是显式 deny-all。不得依据字符串前缀推断父子范围。

### 3.3 Permission 与集合 hash

Permission 沿用 `moduleapi.Permission` grammar。权限 ceiling 取交集；规则中的
`required_permissions` 是调用/读取所需条件，多层取并集，随后必须是最终 Authority
permission ceiling 的子集，否则该候选失败关闭。

集合 domain：

```text
freeagent.governance-permission-set.v1
freeagent.governance-data-scope-set.v1
```

### 3.4 BudgetCategory

BudgetCategory 使用小写 dotted identifier：每段为 `[a-z0-9][a-z0-9_-]*`，段间以
`.` 分隔，最长 128 bytes。没有隐式 `default`、父级继承或前缀匹配；规则引用的类别
必须精确存在于所有适用 Budget source document，缺失即 deny。

## 4. Provider 与 operation identity

### 4.1 ProviderSelector

Provider selector 是严格判别 union：

```json
{
  "provider_kind": "MODULE",
  "module_ref": {"id":"example.module","version":"1"},
  "mcp_server_ref": null
}
```

或：

```json
{
  "provider_kind": "MCP_SERVER",
  "module_ref": null,
  "mcp_server_ref": {"id":"example.mcp","version":"1"}
}
```

恰好一个分支非 `null`；MODULE 使用 `moduleapi.Ref`，MCP_SERVER 使用
`assembly.MCPServerRef`。selector 不授权目录 membership，解析后仍必须由
RuntimeCatalog 精确证明。

### 4.2 OperationIdentity

```json
{
  "operation_kind": "MODULE_CAPABILITY",
  "module_capability": {"capability":"example.read"},
  "mcp_tool": null
}
```

或：

```json
{
  "operation_kind": "MCP_TOOL",
  "module_capability": null,
  "mcp_tool": {"name":"lookup"}
}
```

Provider 与 operation 分支必须对应；MODULE 不能声明 MCP_TOOL，MCP_SERVER 不能声明
MODULE_CAPABILITY。MCP tool revision/descriptor digest 在 discovery/LockedMCP 阶段追加，
不由预发现政策猜测。

## 5. Tool policy source document

### 5.1 ToolPolicyRule

```json
{
  "rule_effect": "ALLOW",
  "provider_selector": {},
  "operation_identity": {},
  "allow_constraints": {
    "required_permissions": [],
    "max_effect_class": "none|read_only|reversible_write|irreversible_write",
    "approval_requirement": "NONE|REQUIRED",
    "allowed_data_scopes": [],
    "max_request_bytes": 1,
    "max_result_bytes": 1,
    "budget_category": "tool.default"
  },
  "rule_ordinal": 0,
  "rule_hash": "<sha256>"
}
```

DENY 分支必须令 `allow_constraints = null`；ALLOW 分支必须提供非 null constraints，
byte limits 必须为正数。Rule hash domain：
`freeagent.governance-tool-policy-rule.v1`。

### 5.2 ToolPolicyDocument

```json
{
  "schema_version": 1,
  "ordered_rules": [],
  "tool_policy_document_hash": "<sha256>"
}
```

规则按 `(provider canonical key, operation canonical key, rule_ordinal)` 排序。
`rule_ordinal` 在文档内必须从 0 连续且唯一；同一 Provider+operation 出现多次属于冲突，
即使两个规则字节相同也拒绝。空规则集是规范 deny-all。

## 6. Content selector 与 source document

### 6.1 SourceSelector 严格矩阵

`SourceSelector` 总是包含 `source_kind`、七个 nullable branch 和
`selector_identity_hash`；恰好与 `source_kind` 对应的一个 branch 非 null。hash domain
是 `freeagent.governance-content-source-selector.v1`，preimage 删除且仅删除
`selector_identity_hash`。

各分支 v1 wire：

```text
SKILL:
  skill_ref {skill_id, revision, content_hash}
  package_digest
  required_tags[]

KNOWLEDGE:
  collection_id / collection_version
  source_id / source_revision_hash
  required_tags[]

MEMORY:
  owner_kind = AGENT | WORKSPACE | USER
  owner_identity_hash
  scope_kind = PRIVATE | WORKSPACE | TENANT
  scope_identity_hash
  record_kind

MCP_RESOURCE:
  mcp ProviderSelector
  uri

MCP_RESOURCE_TEMPLATE_RESULT:
  mcp ProviderSelector
  template_uri
  argument_contract_hash
  max_expanded_uri_bytes

MCP_PROMPT:
  mcp ProviderSelector
  prompt_name
  argument_contract_hash
  max_argument_bytes

TOOL_RESULT:
  ProviderSelector
  OperationIdentity
  result_class
```

Tags 是规范、排序、去重的精确 required-tag 集合；匹配时必须是来源标签的子集。
其余字段精确相等。URI 使用已规范化的绝对 URI bytes；v1 不做前缀或模板猜测。
`record_kind`、`result_class` 使用 BudgetCategory 相同的 dotted identifier grammar。

### 6.2 ProjectionPolicy

```json
{
  "projection_kind": "FULL_TEXT|TEXT_ONLY|STRUCTURED_FIELDS",
  "ordered_fields": [],
  "projection_policy_hash": "<sha256>"
}
```

FULL_TEXT/TEXT_ONLY 必须使用空 fields；STRUCTURED_FIELDS 必须使用非空、排序、去重的
JSON Pointer 集合。domain：`freeagent.governance-projection-policy.v1`。

v1 没有可证明的 ProjectionPolicy 偏序。因此同一 selector 的多层 ALLOW 规则必须引用
完全相同的 ProjectionPolicyHash；不同即配置冲突并失败，不能按名称猜测哪个更窄。

### 6.3 ContentPolicyRule 与文档

```json
{
  "rule_effect": "ALLOW",
  "source_selector": {},
  "allow_constraints": {
    "required_permissions": [],
    "allowed_data_scopes": [],
    "max_source_bytes": 1,
    "max_projected_bytes": 1,
    "projection_policy": {}
  },
  "rule_ordinal": 0,
  "rule_hash": "<sha256>"
}
```

DENY 的 `allow_constraints` 必须为 null。`max_projected_bytes <= max_source_bytes`。
Rule/domain 与 document/domain：

```text
freeagent.governance-content-policy-rule.v1
freeagent.governance-content-policy-document.v1
```

ContentPolicyDocument wire 为
`{schema_version,ordered_rules,content_policy_document_hash}`。规则按
`(source_kind restriction order, selector_identity_hash, rule_ordinal)` 排序；SourceKind
顺序固定为本文 §6.1 的列出顺序，不使用 Go 声明顺序或 locale。同一
`(source_kind, selector_identity_hash)` 重复即冲突；ordinal 仍须从 0 连续且唯一。
空集合是 deny-all。Content 授权只允许读取/投影，永远不产生 Provider 调用权。

## 7. Authority 与 Budget source document

### 7.1 AuthorityPolicyDocument

```json
{
  "schema_version": 1,
  "permission_ceiling": [],
  "effect_ceiling": "none|read_only|reversible_write|irreversible_write",
  "data_scope_ceiling": [],
  "authority_policy_document_hash": "<sha256>"
}
```

空 permission/data scope 与 `effect_ceiling=none` 是显式 deny-all，不是 inherit。
domain：`freeagent.governance-authority-policy-document.v1`。

### 7.2 BudgetPolicyDocument

```json
{
  "schema_version": 1,
  "ordered_category_ceilings": [
    {
      "budget_category": "tool.default",
      "limits": {
        "max_model_calls": 0,
        "max_tool_calls": 0,
        "max_input_tokens": 0,
        "max_output_tokens": 0,
        "max_total_tokens": 0,
        "max_cost_micros": 0,
        "max_wall_time_ms": 0
      }
    }
  ],
  "budget_policy_document_hash": "<sha256>"
}
```

category 按规范 key 排序且唯一；七轴零值是显式零预算。空 categories 对所有 Provider
类别 deny，不影响 core-model 已有的独立 Model budget authority。domain：
`freeagent.governance-budget-policy-document.v1`。

## 8. ResolveMemberGovernance

输入必须包含：成员稳定 scope、完整 GovernancePolicyGeneration、与全部 refs 一一对应的
SourcePolicyDocument，以及每个 source 引用的四个 leaf documents。函数是纯函数，不读取
current、数据库、网络、环境、时钟或全局 registry。

固定流程：

1. 严格验证 generation、source 和四叶 hash DAG。
2. 按成员 scope 选择适用 source；任何 scope identity 歧义失败。
3. 按 `(PolicyLayer.RestrictionOrder, LayerOrdinal)` 排序并保留全部适用 refs。
4. Tool：候选 key 是所有 source 的规则 key 并集。只有每个适用 source 都存在同 key
   ALLOW 且没有 DENY 的 key 才进入 resolved policy；缺失精确 match 即 deny。合并时
   required permissions 取并集、effect/byte limits 取最小、approval 取最大、data scope
   取交集、budget category 必须完全相等。
5. Content：使用同样的 key-set 交集与 deny-wins；required permissions 取并集、data
   scope 取交集、byte limits 取最小、ProjectionPolicyHash 必须完全相等。
6. Authority：所有 source ceiling 取交集/最小；没有 source 时为规范 deny-all。
7. Budget：只有每个 source 都存在的 category 才保留，七轴逐项取最小；没有 source 时
   categories 为 `[]`。
8. Tool/Content resolved 规则必须再受最终 Authority/Budget 约束；超出 ceiling、引用缺失
   category、空 data-scope 交集或不满足 required permission 时失败关闭，不静默扩权。
9. 重新分配 resolved rule ordinal 为按规范 key 排序的零起连续序号，并保存每个规则的
   `ordered_source_rule_hashes`，使恢复能够重做交集。
10. 生成四个完整 resolved document 和成员 snapshot。

若没有适用 source，只有 `pure_chat` 可生成零 Tool/Content、deny-all Authority、空 Provider
Budget 的合法 snapshot；任何 executable requirement 返回固定错误。

## 9. Resolved documents 与成员 snapshot

四个 resolved document 必须保存完整正文，不能只保存 hash：

```text
freeagent.governance-tool-policy.v1
freeagent.governance-content-policy.v1
freeagent.governance-authority-policy.v1
freeagent.governance-budget-policy.v1
```

成员 snapshot wire：

```text
schema_version
tenant_id / workspace_id / run_id / member_id
agent_id / agent_version_id / assembly_profile_hash / task_scope_hash
policy_generation / policy_generation_hash
governance_policy_revocation_watermark
ordered_applicable_source_policy_refs
canonical_resolved_tool_policy
canonical_resolved_content_policy
canonical_resolved_authority_policy
canonical_resolved_budget_policy
tool_policy_hash / content_policy_hash / authority_policy_hash / budget_policy_hash
policy_hash
snapshot_hash
```

`PolicyHash` 使用 `freeagent.member-governance-policy.v1`，覆盖稳定政策投影：除
`run_id`、revocation watermark、四个嵌套正文和 `snapshot_hash` 外的 scope 身份、
generation/ref closure 与四个 component hash。

`SnapshotHash` 使用
`freeagent.resolved-member-governance-policy-snapshot.v1`，覆盖完整 snapshot 并只删除
自身字段。因此同一政策换 RunID 时 PolicyHash 不变而 SnapshotHash 必变；watermark
变化也只改变 SnapshotHash。

watermark 是 tenant-global `GovernancePolicyRevocationSeq`，`0` 表示尚无记录。它不进入
generation/source docs，也不进入 PolicyHash。

## 10. API 与测试门

实现至少提供：

```text
New*/Restore*/Validate/CanonicalJSON/Hash
ResolveMemberGovernance(input)
RestoreResolvedMemberGovernancePolicySnapshot(canonical, generation, sources, leaves)
```

必须覆盖：七层/同层多 source 全排列、规则 key 交集、missing-match deny、explicit deny、
approval/effect/permission/scope/budget 合并、Projection 冲突、typed-union 每个错误分支、
pure_chat 空政策、跨租户/成员替换、RunID 与 PolicyHash 隔离、watermark 与 SnapshotHash
隔离、strict restore、alias isolation、shuffle、jsonv2 和 vet。
