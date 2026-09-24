# CORE_RUNTIME_V1

> Current phase override (2026-09-22): P2 Control UI i18n and the P3 Provider contract freeze are accepted development slices; the next entry is `P3_SECOND_PROVIDER_NEXT`. See [`P3_PROVIDER_CONTRACT_V1`](../P3_PROVIDER_CONTRACT_V1.md).

状态：S1–W5 已验收开发基线，以及 W2-D 本地显式装配 v1、W2-E2 受信 `text.stats` 统一 Apply、W2-E3 Workspace Channel Apply、W2-E4 DeepSeek Model replacement、W2-E5-A Requires/permission grant、W2-E5-B Document Insight 双 Port、W2-R1 窄 REMOTE Action Host、W2-R2 窄 WASM Action Host、W2-R3 第三方纯计算/停机撤权、W2-U2 Source/Snapshot observation、W2-U3 Upgrade Review、W2-U4 approved Declarative Profile Context replacement、W6-0 控制 API 纯合同、W6-1 Application Services / Read API、W6-2 confirmation/durable receipt/MODULE_DISABLE wiring、W6-3 Web Shell/read-only Overview、W6-4 Modules configuration UI、W6-5 server-owned module artifact ingress 与 W6.6 server-owned Upgrade Review、P2 Control UI i18n、P3 Provider contract freeze 已验收开发切片；当前下一入口为 `P3_SECOND_PROVIDER_NEXT`。W6.6 只消费 Store-owned Admission/Artifact 生成持久 Review/Decision，不自动 Install、Activate、Bind、Grant、Apply、Execute 或调用 Provider；P3 contract freeze 不代表第二 Provider 已接入。
Machine status: `S1_ACCEPTED_DEVELOPMENT_BASELINE`  
S2.1 status: `S2.1_CONTEXT_COMPILER_ACCEPTED_DEVELOPMENT_SLICE`  
ModelProfile status: `S2_MODEL_PROFILE_ACCEPTED_DEVELOPMENT_SLICE`  
RAG status: `S2_RAG_ACCEPTED_DEVELOPMENT_SLICE`  
Memory status: `S2_MEMORY_ACCEPTED_DEVELOPMENT_SLICE`  
Action status: `S2_ACTION_GATEWAY_ACCEPTED_DEVELOPMENT_SLICE`  
MCP status: `S2_MCP_LOCAL_PROCESS_STDIO_TOOL_ACCEPTED_DEVELOPMENT_SLICE`  
Channel status: `S2_CHANNEL_ACCEPTED_DEVELOPMENT_SLICE`  
Parent/Child + Composite status: `S2_PARENT_CHILD_COMPOSITE_ACCEPTED_DEVELOPMENT_SLICE`  
Fair Scheduler status: `S3_FAIR_SCHEDULER_ACCEPTED_DEVELOPMENT_SLICE`  
Reviewer status: `S3_COMPOSITE_REVIEW_GATE_ACCEPTED_DEVELOPMENT_SLICE`  
W1 Conversation status: `W1_COMPLETE_REAL_DEEPSEEK_50`  
W5 status: `W5_F1_COLLABORATION_STABILITY_ACCEPTED_DEVELOPMENT_SLICE / W5_COMPLETE_ACCEPTED_DEVELOPMENT_SLICE / W6_NEXT`  
W2-D Local Assembly status: `W2_D_LOCAL_ASSEMBLY_ACCEPTED_DEVELOPMENT_SLICE`  
W2-E2 trusted text.stats Apply status: `W2_E2_TRUSTED_TEXT_STATS_APPLY_ACCEPTED_DEVELOPMENT_SLICE`  
W2-E3 Workspace Channel Apply status: `W2_E3_WORKSPACE_CHANNEL_APPLY_ACCEPTED_DEVELOPMENT_SLICE`  
W2-E4 DeepSeek Model replacement status: `W2_E4_DEEPSEEK_MODEL_REPLACEMENT_ACCEPTED_DEVELOPMENT_SLICE`  
W2-E5-A Requires/permission grant status: `W2_E5_A_REQUIRES_PERMISSION_GRANT_ACCEPTED_DEVELOPMENT_SLICE / W2_E5_B_NEXT`  
W2-E5-B Document Insight dual-Port status: `W2_E5_B_DOCUMENT_INSIGHT_DUAL_PORT_ACCEPTED_DEVELOPMENT_SLICE`  
W2-R1 REMOTE Action Host status: `W2_R1_REMOTE_ACTION_HOST_ACCEPTED_DEVELOPMENT_SLICE`  
W2-R2 WASM Action Host status: `W2_R2_WASM_HOST_ACCEPTED_DEVELOPMENT_SLICE`  
W2-R3 untrusted WASM pure-compute status: `W2_R3_UNTRUSTED_MODULE_ISOLATION_ACCEPTED_DEVELOPMENT_SLICE`  
W2-U2 discovery snapshot status: `W2_U2_DISCOVERY_SNAPSHOT_ACCEPTED_DEVELOPMENT_SLICE`  
W2-U3 upgrade review historical status: `W2_U3_UPGRADE_REVIEW_ACCEPTED_DEVELOPMENT_SLICE / W2_U4_OPERATOR_APPLY_NEXT`  
W2-U4 approved upgrade Apply historical status: `W2_U4_OPERATOR_APPLY_ACCEPTED_DEVELOPMENT_SLICE / W6_0_CONTROL_API_CONTRACT_NEXT`  
W6-0 historical Control API contract status: `W6_0_CONTROL_API_CONTRACT_ACCEPTED_DEVELOPMENT_SLICE / W6_1_APPLICATION_SERVICES_READ_API_NEXT`  
W6-1 historical Control Application Services status: `W6_1_APPLICATION_SERVICES_READ_API_ACCEPTED_DEVELOPMENT_SLICE / W6_2_CONTROLLED_MUTATIONS_AUDIT_NEXT`  
W6-2 Control confirmation status: `W6_2_CONFIRMATION_CONTRACT_ACCEPTED_DEVELOPMENT_SLICE / W6_2_DURABLE_RECEIPT_SCHEMA_NEXT`  
W6-2 durable receipt Schema status: `W6_2_DURABLE_RECEIPT_SCHEMA_ACCEPTED_DEVELOPMENT_SLICE / W6_2_MODULE_DISABLE_MUTATION_WIRING_NEXT`  
W6-2 MODULE_DISABLE mutation wiring historical status: `W6_2_MODULE_DISABLE_MUTATION_WIRING_ACCEPTED_DEVELOPMENT_SLICE / W6_3_WEB_SHELL_READ_ONLY_OVERVIEW_NEXT`  
W6-3 Web Shell/read-only Overview historical status: `W6_3_WEB_SHELL_READ_ONLY_OVERVIEW_ACCEPTED_DEVELOPMENT_SLICE / W6_4_MODULES_CONFIGURATION_UI_NEXT`  
W6-4 Modules configuration UI historical status: `W6_4_MODULES_CONFIGURATION_UI_ACCEPTED_DEVELOPMENT_SLICE / W6_5_SERVER_OWNED_MODULE_ARTIFACT_INGRESS_NEXT`  
W6-5 server-owned module artifact ingress status: `W6_5_SERVER_OWNED_MODULE_ARTIFACT_INGRESS_ACCEPTED_DEVELOPMENT_SLICE / W6_6_SERVER_OWNED_MODULE_UPGRADE_REVIEW_NEXT`（历史 marker）
W6.6 server-owned Upgrade Review status: `W6_6_SERVER_OWNED_MODULE_UPGRADE_REVIEW_ACCEPTED_DEVELOPMENT_SLICE / P2_CONTROL_UI_I18N_ACCEPTED_DEVELOPMENT_SLICE / P3_SECOND_PROVIDER_NEXT`
适用范围：FreeAgent Core Runtime、W2-D 本地显式装配 v1、W2-E2/E3/E4/E5-A/E5-B/R1/R2/R3/U2/U3/U4 窄增量、W6-0 纯控制 API 合同、W6-1 默认关闭的本地双 listener/bootstrap/session/Modules read/Disable Dry-run、W6-2 stable confirmation/evaluation、process-local proof authority、durable receipt Schema/NO_CHANGE Store seam、窄 MODULE_DISABLE confirmation/mutate 与同事务 APPLIED publication、W6-3 scope-filtered read-only Overview observation/application/HTTP/Web Shell、W6-4 对同一现有 Modules/Application/HTTP surface 的严格浏览器 consumer，以及 W6-5 默认关闭的可信本地 Operator CLI、server-owned inert artifact publication/Admission 与 Backup closure、S1–W4 开发切片、W5 单 repair round Decision family、同 Tenant 跨 Workspace Transfer 及 F1 公平/取消/恢复稳定性  
规范词：本文中的“必须”“不得”“应当”均为实现约束

> 金额边界：`P0_MONEY_BUDGET_REMOVED_FAC2_BASELINE`；当前 Runtime 已删除 `BudgetPolicy`、`CostPolicy`、`PriceSnapshot`、费用估算与金额对账，只保留 token usage、输出上限、deadline、权限、UNKNOWN 对账与 exact retry。本文件中出现的 `CNY`、估算费用与 PriceSnapshot 字样一律是删除前 FAC1 时期的冻结历史验收证据，不描述当前运行语义；冻结记录见 [`P0_BASELINE_FREEZE`](../P0_BASELINE_FREEZE.md)

## 1. 目标与非目标

本规格只固定一条可执行主线：

```text
Control Plane + RuntimeCatalog
    → Assembly Compiler
    → MemberExecutionSnapshot + RunManifest
    → Universal Loop
    → PortPlan 中冻结的 Provider
    → Current Store / Ledger
```

核心不变量：

- 系统只有一个 Assembly Compiler、一个 Universal Loop 和一个可写 Current Store。
- Agent 与 Workspace 相互独立，通过编译输入自由组合。
- Role、Skill、RAG、Memory、MCP、人格和情感等能力只能作为模块经 Port 接入。
- Universal Loop 可以识别版本化 Port，不得识别具体模块 ID、品牌或实现名称。
- 已发布 Run 的装配不可变；运行时不得重新选择、升级或重排 Provider。
- 权限、预算、审计和副作用安全由 Core 授予并校验，模块不能自授权。
- 同一权威事实只有一个归属；其他位置只保存 `Ref + Version + Digest`。
- 每个 Run 仍只绑定一个 Workspace；W5 跨 Workspace 协作只通过冻结双边 grant 与有界
  REQUEST/RESULT envelope 连接不同 Run，不把多个 Workspace 合并进一个 Run。
- W5 Decision family 预冻结完整 `2N+3` 物理图，只有一次 repair round；模型不能动态增删、
  重排或重路由 Run。

本规格不定义数据库表结构、迁移步骤或备份格式；这些属于
`CURRENT_STORE_V1` 和 `CUTOVER_ACCEPTANCE`。本规格也不保留旧 Runtime、
旧 Store、Legacy Admission、shadow 或 evidence 兼容协议。

## 2. S0 历史断点与当前候选

S0 审计时，旧生产入口存在如下断点：

```text
backend.OpenSQLiteRuntime
    → 为 Pure Chat 写入 UNIFIED_CORE_V1 Admission
    → cmd/freeagent 仍构造旧 runtime.Runtime
    → Runtime.ProcessOne 进入 Legacy execution gate
    → ErrUnifiedAssemblyExecutionUnavailable
```

上述路径已在 S1 物理退役；代码块仅记录 S0 当时的断点，不是当前可调用入口或
兼容契约。

该链是 S1 必须一次性切断的历史基线，不是当前候选入口。切换约束保持不变：

- 不得让 Unified Run 回落到旧 Runtime。
- 不得新增 Unified-to-Legacy adapter。
- 不得继续维护 `UNIFIED_CORE_V1` 与 `LEGACY_COMPAT_V1` 两条生产路径。
- 新入口必须直接调用本规格定义的 Assembly Compiler 与 Universal Loop。

当前 S1 候选入口已经改为：

```text
cmd/freeagent
    → VerifyCurrentStore + OpenExistingCurrentStore
    → ChatService
    → Assembly Compiler
    → Universal Loop
    → Current Store terminal result
```

`init`、`backup`、`backup-verify` 和 `restore` 是显式离线运维命令；普通
`chat/serve` 不创建、迁移、修复或补种 Store。候选二进制的生产依赖闭包不得包含
旧 Runtime、旧 Store、旧 Channel 或 Legacy execution gate。

这证明 S1 实现链已经闭合。项目所有者已确认 FreeAgent 从未正式部署，因此本轮
生产切换为 `NOT_APPLICABLE`，S1 作为开发基线验收并允许进入 S2；这不等于首次
生产部署或公开发布已经批准。未来首次部署的路径、writer、备份、外部效果、竞态
检测和发布门禁仍以 `CUTOVER_ACCEPTANCE` 为准。

## 3. 版本、引用与规范编码

### 3.1 精确引用

```go
type PortRef struct {
    Name         string
    ExactVersion string
}
```

`AgentRef`、`ProfileRef`、`ModelProfileRef`、`WorkspaceRef`、`TaskRef`、`PolicyRef` 和
`CatalogSnapshotRef` 必须是不可互换的 typed opaque ref；共同 wire shape 为
`{id, version, digest}`。`MemberSnapshotRef` 的 wire shape 为
`{member_id, digest}`。不得用一个通用 `VersionedRef` 在这些类型之间传值。

约束：

- `PortRef` 必须使用精确版本，不接受运行时版本范围。
- Manifest 可以声明兼容范围，但 Assembly Compiler 必须在 Admission 前解析为
  精确 `PortRef`。
- `ID`、`Version`、`Digest` 均不能为空；摘要统一使用小写十六进制 SHA-256。
- 相等判断使用全部字段；不得只按名称或 ID 匹配。

S1 的 `PolicyRef` 必须由下列 canonical `PolicyDocument` 生成：

```text
PolicyDocument = {
  id,
  version,
  policy_type,
  body
}

PolicyRef.digest =
SHA256(
  "freeagent.content-record/v1\0"
  + "POLICY\0"
  + "application/json\0"
  + CanonicalJSON(PolicyDocument)
)
```

`PolicyRef.id/version` 必须与文档内字段逐字节一致。Policy alias 只允许存在于
seed 导入请求和 Control 的导入来源记录，不能进入
MemberExecutionSnapshot 或 RunManifest；Control 发布结果必须同时保存解析后的
PolicyRef。

S1 seed 中每个 operator policy input 必须有唯一 `alias`。导入器确定性构造：

```text
id          = "freeagent.policy." + alias
version     = "1"
policy_type = PERMISSION | RESOURCE | ACTIVATION | CONTEXT |
              SCHEDULING | CONFIGURATION
body        = 对应输入对象移除 alias 后的 Canonical JSON
```

alias 只允许小写 ASCII 字母、数字和 `.-`，不得包含路径、URI、版本范围或 Secret。
权限、调度、Context 和配置 policy 都复用这一规则；不为每种 policy
建立专用表或摘要协议。

### 3.2 AdmissionIntentV1

Admission 幂等只绑定稳定、显式的 User/Operator ingress intent：

```go
type AdmissionIntentV1 struct {
    SchemaVersion     string // admission-intent/v1
    TenantID          string
    AdmissionKey      string
    PrincipalID       string
    WorkspaceID       string
    AgentID           string
    ProfileID         string
    TaskInputRef      string
    RequestedPorts    []PortRef
    Deadline          time.Time
    CancellationScope string
    ExplicitLimits    json.RawMessage // canonical JSON object
    ConversationTurn *ConversationTurnIntentV1 // optional, omitempty
}

type ConversationTurnIntentV1 struct {
    SchemaVersion               string // conversation-turn-intent/v1
    ConversationID              string
    ExpectedConversationRevision uint64
    ExpectedHeadRunID           string // first turn only: omitted
}
```

`RequestedPorts` 在 intent 中是 capability set，按 `PortRef.Name/ExactVersion`
排序并拒绝重复；它不决定 Provider 或 Binding 顺序。`Deadline` 转为 UTC
RFC3339Nano。摘要为：

`CancellationScope` 的唯一合法值是 `run`、`family`、`inherited`。外部 ingress
只能为根 Run 提交 `run` 或 `family`；`inherited` 只允许由一次已经通过校验的
Composite family Admission 为 Child 确定性派生，不能由调用者提交。非 Composite
请求不得使用 `family` 或 `inherited`。Child 的 `AdmissionKey`、`RunID`、`MemberID`
与 `RecoveryRootRef` 均由 Parent 的 `AdmissionIntentDigest + slot_id` 按各自固定 domain
separator 派生，不得由调用者选择。Parent/Child manifest digest 不参与这些身份输入，
否则 Parent 需要冻结 Child identity、Child 又依赖 Parent digest，会形成摘要循环。重试必须
恢复原 family，不能创建替代 Child。

`ConversationTurn` 只用于显式 Conversation ingress。它必须进入
`AdmissionIntentDigest`，使同一 turn 的 client retry 恢复原 Run，而不是在同一
Conversation 追加替代 Run。首轮固定
`ExpectedConversationRevision=0` 且省略 `ExpectedHeadRunID`；后续轮次必须提交调用方
已读到的 exact Conversation revision 与 head RunID。调用方不能提交 turn index；它只由
Current Store 成功 CAS 后的 revision 派生。未使用 Conversation 时该字段必须省略，既有
Admission canonical bytes 保持不变。聊天语境中的 `Session` 只允许作为客户端进程内的
当前 Conversation 选择，不是持久对象，不得进入 Store、Manifest、摘要或恢复协议；MCP、
HTTP 等传输协议自身使用的 session 概念不受此命名约束。

```text
SHA256(
  "freeagent.admission-intent/v1\0"
  + CanonicalJSON(AdmissionIntentV1)
)
```

RunID、AttemptID、当前 Control/Catalog pointer、物化出的 version/digest、
PortBinding、MemberSnapshot、Manifest 和时间戳不得进入 AdmissionIntentDigest。
这样，同一显式 intent 在提交响应丢失、Control 更新或进程恢复后仍解析到原 Run；
这些动态事实仍由发布基线和冻结快照分别审计，不能用来改变幂等身份。

### 3.3 Canonical 编码

所有参与摘要的对象必须使用 RFC 8785 JSON Canonicalization Scheme，并附加本规格
指定的 domain separator：

- 普通字符串保持原始 Unicode scalar value，不做 NFC/NFD 改写。
- 只有模块包路径按 ArtifactDigest 规则做 Unicode 与分隔符规范化。
- 对象键和数字使用 RFC 8785 规则。
- 数组保持协议规定的顺序，不做隐式排序。
- 拒绝重复键、无穷值和 NaN。
- 时间统一转为 UTC `time.RFC3339Nano` 字符串。
- 缺失的 optional 字段省略；schema 明确允许的空值写 `null`；有语义的零值不得
  省略。
- Go 结构示例不是 wire field name 的来源；实现必须使用本规格给出的 snake_case
  wire name。
- 摘要输入必须带固定 domain separator。

`PortPlan` 按 `PortRef.Name`、`PortRef.ExactVersion` 升序写入成员快照；
`Bindings` 保持 Assembly Compiler 冻结的调用顺序。

最小测试向量：

```text
domain bytes = "freeagent.canonical-test/v1\0"
value        = {"run_id":"run-1","optional":null,"deadline":"2026-07-29T14:00:00Z"}
canonical    = {"deadline":"2026-07-29T14:00:00Z","optional":null,"run_id":"run-1"}
sha256       = 639b026885a2df8656d7081223b0db2efc06d3811a0aa3a57ccfa05cc26166b2
```

## 4. PortPlan

### 4.1 数据契约

```go
type FailurePolicy string

const (
    FailureRequired FailurePolicy = "REQUIRED"
    FailureOptional FailurePolicy = "OPTIONAL"
)

type PortPlan struct {
    Port     PortRef
    Bindings []PortBinding
}

type PortBinding struct {
    Provider            ActivatedModuleRef
    ConfigRef           string
    AuthorityCeilingRef string
    StaticContextRefs   []string
    FailurePolicy       FailurePolicy
}

type ActivatedModuleRef struct {
    ModuleID          string
    Version           string
    ArtifactDigest    string
    InstanceID        string
    ExecutionClass    ExecutionClass
    AdapterIdentity   string
    ActivationRevision uint64
}
```

`ConfigRef` 指向 Core Store 中内容寻址的规范配置，覆盖 SecretRef 身份但不包含
Secret 值。`AuthorityCeilingRef` 指向 Core 生成的不可变权限、资源范围和 Effect
上限记录。`StaticContextRefs` 是有序的 `STATIC_CONTEXT` ContentDigest 数组；
只有 `context.provide/v1` Binding 可以非空。空列表必须冻结为 `[]`，不得包含重复
或无效 digest，也不得在 Admission 之外另存一份恢复聚合副本。

`model.generate/v2` 的 `ConfigRef` 必须指向 canonical
`ModelBindingConfigV2{schema_version, provider, model, model_build_id,
parameters}`。`schema_version` 固定为
`model-binding-config/v2`，`parameters` 是有界 canonical JSON object。该配置是
品牌无关的 Core 通用合同，禁止 API key、endpoint 或其他动态 Secret；Core 递归
拒绝已知凭据/连接键族，Activated Provider 的精确 config schema 还必须 allowlist
其余 generation 参数。这里不是任意正文秘密扫描器，不能证明自造键名或普通字符串
不含秘密；第三方模型 Provider 在 schema 校验接入真实 Host 前不得 Activate/Publish。
真正凭据和 endpoint 只能由受控 Secret/运行环境注入。模型调用前必须
严格恢复该配置，并要求模型请求的
parameters 与配置一致，同时由其中的 provider、model 与 model build ID 形成唯一
模型闭包，调用方不得另行选择模型构建。

`model_build_id` 是本次从未部署基线上的发布前必填合同修订：它会改变所有模型
Binding CONFIG 的 canonical bytes/digest，即使成员没有 ModelProfile。方案 A 已确认
不存在需兼容的正式部署，因此 seed、artifact schema 与开发库一次性重建，不增加
旧 v1 decoder、可选回退或第二版本链；首次公开发布后不得再用同样方式改写 v1。

### 4.2 不变量

- 同一 `MemberExecutionSnapshot` 中，同一 `PortRef` 至多有一个 `PortPlan`。
- 已装配的 PortPlan 必须非空；未装配的 optional Port 不创建空计划。
- `Bindings` 的数组顺序是唯一调用顺序，Runtime 和 Host 均不得重排。
- 每个 Binding 内 `StaticContextRefs` 的数组顺序同样是语义顺序；Assembly
  Compiler 必须深拷贝并原样冻结到 MemberSnapshot。
- 单一裁决类 Port 必须恰有一个 Binding。
- 多 Binding、合并方式和错误处理由精确 Port 版本定义，不使用通用 reducer、
  merge、priority、负载均衡或 fallback DAG。
- `FailurePolicy` 只来自消费方配置并由 Assembly Compiler 冻结；Manifest 和
  Provider 无权设置。
- REQUIRED 的确定失败终止当前路径。
- OPTIONAL 只有在确定没有产生副作用的普通失败时才可跳过。
- UNKNOWN、权限拒绝、完整性错误和已开始的不可逆调用不得重试、跳过或切换
  Binding。
- 不单独计算 PlanDigest；`MemberSnapshotDigest` 覆盖全部 PortPlan。

### 4.3 当前已冻结 Port

当前只发布已经进入纵向工作包并具有真实消费者的 Port：

| PortRef | 基数 | 语义 |
|---|---:|---|
| `model.generate/v2` | 恰好 1 | 调用冻结的模型 Provider |
| `context.provide/v1` | 1..N，有序 | 按序提供 Role、Prompt、静态 Skill 等上下文块 |
| `action.provider/v1` | 1..N，有序，可选 | 同一 Binding 的公开 Describe/Prepare；ActionID 解析后禁止 fallback |
| `channel.transport/v1` | 恰好 1，可选 | Workspace Endpoint 的入站规范化与回复原会话；出站只由共享 Gateway 调用 |

`model.generate/v2` 必须恰有一个 `REQUIRED` Binding。首个 Action 工作包只允许本地
allowlist 内的 `TRUSTED_IN_PROCESS` Built-in Provider；随后验收的 §7.8 窄 MCP 切片
只额外允许 Operator 显式批准且完全信任的精确 `LOCAL_PROCESS` stdio Tool Adapter。
W2-R1 再只为 `action.provider/v1 + REMOTE/freeagent-action-http/v1` 注册第 8 个 exact
handler；该窄 Host 默认关闭，必须由 Operator 对 artifact、HTTPS endpoint 与 SecretRef
分别显式授权。所有 Action Binding 必须为 `REQUIRED` 且不得携带
`StaticContextRefs`。W2-R2 又只为
`action.provider/v1 + WASM/freeagent-action-wasm/v1` 注册第 9 个 exact handler；它要求独立
artifact grant、默认关闭且只执行 `EffectClass=none` 的纯计算 guest。其他 REMOTE/WASM 协议、
其他本地进程协议和完整 MCP 能力仍不注册；当前无 OS sandbox 的 `LOCAL_PROCESS` 不得用于
不可信第三方，R1 REMOTE 也不授予包内代码执行权，R2 也不冒充 OS/container 级隔离。

`channel.transport/v1` 属于 Workspace Endpoint，而不属于 Agent/Profile。显式装配时
必须恰有一个 `REQUIRED + TRUSTED_IN_PROCESS` Binding，且不得携带
`StaticContextRefs`；未启用 Channel 的装配不创建空 PortPlan。Port 的公开协议只提供
有界的 Decode/Prepare，不授予网络发送权；私有 executor 只能由既有 Gateway 调用。

系统约束、当前任务和原始 History 是 Core 输入，不伪装成模块。S2 新增能力时必须
发布新的精确 Port 定义，但不得新增第二套装配协议。

## 5. 最小 Module Protocol

### 5.1 包与 Manifest

```text
module-package/
  module.yaml
  schemas/
  content/          # 可选
  implementation/   # 可选
  README.md
  LICENSE
```

Manifest 只允许包含：

```text
api_version
id / version
runtime.mode / protocol / entrypoint
provides / requires
requested_permissions
config_schema
lifecycle / health
```

Manifest 是未受信的能力请求，不得声明最终 Trust、Core 控制权、Effect 上限、
FailurePolicy、运行时授权、摘要或签名。

S1 将 `module.yaml` 冻结为 YAML 1.2 兼容的 **RFC 8785 canonical JSON object**
子集，不引入通用 YAML 解析语义。精确规则为：

- `api_version` 必须为 `freeagent.module/v1`。
- 只接受上表字段；未知字段、旧字段和任何自授权字段一律拒绝。
- `provides`、`requires` 与 `requested_permissions` 保留发布者给出的数组顺序，
  但同一数组内不得重复；`provides` 与 `requires` 不得包含同一精确 Port。
- `runtime.mode` 是未受信请求，与 Core 最终授予的 `ExecutionClass` 使用不同类型。
- `DECLARATIVE` 请求必须使用 `static/v1`，且 entrypoint 是规范化的
  `content/` 相对路径。
- `TRUSTED_IN_PROCESS` 请求必须使用 `go-in-process/v1`；entrypoint 只是请求的
  adapter identity，是否允许仍由本地 allowlist 和 Activation 决定。
- `REMOTE` 请求的离线格式层可以把未知 protocol 作为不带授权的 opaque request 验证；
  只有精确 `freeagent-action-http/v1` 才有当前 Core handler。该协议的 entrypoint 必须是
  ArtifactDigest 覆盖的 canonical `content/` descriptor；descriptor 只能声明有界 Action
  definitions，不得声明 endpoint、Secret、proxy、redirect、retry、fallback 或动态发现。
- `config_schema`、`lifecycle`、`health` 如存在，必须是 canonical JSON object；
  S1 只保存声明，不因此启用生命周期命令、健康检查或额外权限。

Manifest canonical bytes 同时作为 `MODULE_MANIFEST` ContentRecord 的内容。
Manifest 不包含 `artifact_digest`、`manifest_digest` 或签名字段；包身份只有
5.2 定义的 `ArtifactDigest`，从而不存在循环摘要或第二身份。

### 5.2 ArtifactDigest

模块包只有一个构件摘要：

```text
ArtifactDigest =
SHA256(
  "freeagent.module-artifact/v1\0"
  + CanonicalJSON({
      manifest: CanonicalManifest,
      files: sorted[{normalized_path, sha256(file_content)}]
    })
)
```

`files` 排除 `module.yaml`、detached signature 和安装 Receipt，覆盖其余普通文件。
摘要中的路径必须使用 `/` 分隔并按 Unicode NFC 规范化。实现必须拒绝绝对路径、
`..`、重复规范路径、大小写碰撞、symlink、hardlink 和设备文件。`files` 数组按
NFC 后 `normalized_path` 的无符号 UTF-8 字节升序排列，不使用 locale、平台默认
大小写规则或文件系统枚举顺序。相同 Module ID + Version 出现不同 ArtifactDigest
时必须拒绝。Install 验证完整模块包；Activate 和 Host 每次加载时只复核同一个
ArtifactDigest，Host 不创建或修改 Activation Record。

#### 5.2.1 W2-U0 模块供应链纯合同

W2-U0 只冻结七份 authority-free canonical 合同；它不联网、不读取或写入 Current Store、
不扫描构件、不安装、不激活、不绑定，也不调用 Module Host：

```text
module-publisher-key/v1
module-signature/v1
module-source-policy/v1
module-discovery-index/v1
module-discovery-snapshot/v1
module-upgrade-candidate/v1
module-candidate-decision/v1
```

v1 唯一签名算法为 Go 标准库 Ed25519。发布者 Key ID 和签名输入固定为：

```text
PublisherKeyID =
SHA256(
  "freeagent.module-publisher-key-id/v1\0"
  + "ED25519\0"
  + raw_32_byte_public_key
)

SignatureInput =
UTF8("freeagent.module-signature/v1")
+ 0x00
+ ASCII(lowercase_64_hex_ArtifactDigest)
```

detached signature 使用 canonical padded base64 保存 64-byte Ed25519 signature；public key
使用同一 base64 规则保存 32 bytes。签名只覆盖域分隔后的 exact ArtifactDigest，既不重复签名
Manifest 字段，也不把 `publisher_key_id`、Source Policy 或 Authority 混入签名输入。Key ID 只是
本地公钥查找身份；Source Policy 必须另行要求 exact Key ID，因此替换 Key ID、Public Key、
ArtifactDigest 或 Signature 任一项都会失败。模块包不能导入或信任自己的公钥。

`module-source-policy/v1` 由 Operator 本地创建。它只保存 provider-normalized local root 或 exact
HTTPS origin 的域分隔摘要，并冻结 Source kind、网络模式、是否必须签名、Publisher Key ID、
允许的 Module ID 前缀及 index/package/candidate 上限。`LOCAL_DIRECTORY` 的网络模式固定为
`DENY`；`HTTPS_INDEX` 固定为 `EXACT_HTTPS_ONLY`。实际路径与 URL 规范化、安全打开、redirect、
proxy、DNS/special-use 地址和下载边界由后续 Source Provider 负责，不能从摘要反推或绕过。

Discovery Index 按 Module ID、精确不透明 Version 字节、ArtifactDigest 和 source-relative package
path 确定性排序；Version 不是 SemVer，v1 没有 `latest`、范围或自动升级。同一 Module ID +
Version 的重复项或不同 ArtifactDigest 一律冲突关闭。Snapshot 逐字节绑定 exact Source Policy、
Index 及其有序 entries。Upgrade Candidate 必须引用 Snapshot 中真实存在的 exact entry；其
`review_key` 只由 `{SourceID, ModuleRef, ArtifactDigest}` 派生，使同一被拒候选在后续 refresh 中
仍可被确定性抑制。Candidate 和 Decision 都不授予安装、Activation、Binding、ExecutionClass、
Trust、Authority、Secret 或 Effect。

所有七份合同仅接受有界 RFC 8785 canonical JSON object，拒绝未知字段和非 canonical 编码，
Restore 必须逐字节重建并核对 content ID，所有集合和返回 bytes 均为 defensive copy。U0 不改变
24 表 Store identity，也不改变现有手工 Module Apply、Pure Chat、Agent、Workspace、Run、Attempt
或 UNKNOWN 语义。本地签名/Source Policy observation 属于 W2-U1；持久 Source/Index/Snapshot 与
current revocation 属于 U2；Candidate/Decision 与批准后 Apply 分别属于 U3/U4。

当前威胁模型把 Manifest、Index、路径、压缩包、Signature envelope、Key ID、WASM bytes 和候选
内容视为恶意输入；防御 key/signature substitution、摘要循环、同版本换包、stale snapshot、
重复拒绝候选和 Dry-run/Apply 漂移。U0 不声称实现 TUF、Sigstore、DSSE、in-toto、证书链、透明
日志、自动 rotation、网络 transport、OS sandbox 或恶意多租户隔离。

#### 5.2.2 W2-U1 本地 governed observation（已验收窄开发切片）

W2-U1 只能在现有 `module-verify` 的显式分支中消费 U0 合同。没有 supply flag 时必须调用原 legacy
verifier，成功 JSON 与失败边界逐字兼容；任一 supply flag 都选择 governed 分支，缺失、未知组合或
部分 signature tuple 必须失败关闭，不得静默回退 legacy。

governed 分支固定为：

```text
external exact SourcePolicy ID + canonical bytes
  → require LOCAL_DIRECTORY + DENY
  → optional external exact PublisherKey/Signature IDs + canonical bytes
  → invocation-scoped immutable revoked-key deny snapshot
  → stable local source/artifact roots
  → policy-tight first package scan
  → dotted-segment Module ID prefix check
  → optional Ed25519 verification over exact ArtifactDigest
  → policy-tight final digest/size scan + directory identity recheck
  → original freeagent.module-package-verification/v1 report
```

Source Policy、Publisher Key 与 detached Signature 都在 artifact 外，由 Operator 同时提供 exact content
ID 和不超过 64 KiB 的 stable ordinary canonical file。Artifact 内出现同名 `module.sig` 不会被特殊排除，
仍属于默认 ArtifactDigest 覆盖范围。是否要求签名只由 Policy 决定；Manifest、Key、Signature 或
一次成功 observation 都不能授予 ExecutionClass、Trust、Authority、Effect、Secret、Binding、Host
或 runtime access。unsigned direct verifier 必须拒绝未被 Policy 锚定的 Key、Signature 或撤销输入。
这不修改 U0 Snapshot wire：当 `SignatureRequired=false` 时，Discovery entry 的 `signature_id` 仍是可空的
authority-free observation 字段，也可以携带一个语法有效的非空 ID；Snapshot 构造不据此导入 Key、
验证签名或授予信任。U1 direct preflight 没有 Snapshot parent 可作锚点，因此 unsigned Policy 下必须
拒绝所有 Key/Signature/revocation side input，不能把 Snapshot 的可选字段解释成 direct-verifier 许可。

`max_package_bytes` 必须同时裁剪首次与最终 artifact scan。Module ID prefix 使用点分段边界：prefix
`vendor` 只允许 `vendor` 与 `vendor.*`。撤销集合在本次调用入口 defensive-copy 并保持 immutable，
只拒绝本次新候选观察；它不是持久 Store fact、全局 current revocation 或历史事实改写。

U1 不接受 `HTTPS_INDEX`，也不实例化任何 Source 网络 transport。Windows 本地路径必须在任何文件
访问前拒绝 UNC/device namespace，并拒绝 ADS、symlink 与 reparse point；该约束不声称可以识别映射
为普通盘符的网络盘。所有失败发生在 reservation、staging、Store writer、Secret、Host 与网络之前。

成功只返回原 verification report，不创建 Candidate reservation、grant、stage、Installation、Activation、
Binding、Catalog revision 或任何 Store fact，也不能作为 `module-apply` authority。现有手工 exact-grant
Apply 与九个 exact handler 保持原入口和语义。U2 已从唯一 Store 读取并闭合
SourcePolicy→Index→Snapshot observation parent 与 current revocation；U3 已按 §25 生成并审核无权限
Candidate/Review/Decision，U4 才能在真正 staging 前再次检查 current facts，再由 exact Operator approval
进入既有 Dry-run/Apply/CAS。
U1 自身仍不声明跨调用 TOCTOU 或持久撤销已解决。

#### 5.2.3 W2-U2 显式来源观察与不可变 Snapshot（已验收窄开发切片）

U2 只增加一条 observation-only 纵链，不进入 Runtime Assembly、Run、Attempt、Gateway 或 Apply：

```text
explicit module-source-register
  → exact SourcePolicy / optional PublisherKey CAS
explicit module-source-refresh
  → read exact Store refresh basis
  → one Local or HTTPS Index observation
  → BEGIN IMMEDIATE current recheck
  → immutable Index/Snapshot facts
explicit module-publisher-key-revoke
  → irreversible exact-revision deny fact
```

三个命令均要求 `--enable-module-discovery`，关闭时必须在打开 Store、构造 Source Provider 或执行 I/O
前失败。`module-source-register` 的首次创建也必须显式提交 expected policy revision 0；SourceID 不能
重新绑定 Kind 或 OriginDigest。签名 Policy 必须引用 Store 中 exact Publisher Key，revocation 全局、
不可逆，exact retry 不改变 key revision 或 timestamp。

Local Provider 只能读取 canonical absolute root 下固定 `index.json`，拒绝 UNC/device namespace、ADS、
symlink/reparse 与读取中目录/文件 identity drift。HTTPS Provider 必须另有显式 HTTPS enable 与至少一个
exact canonical origin allowlist；Policy OriginDigest 绑定 exact canonical index URL，运行期 allowlist
只绑定 canonical origin。每次 observation 构造新 client，只允许一个 HTTP/1.1 GET；禁止 proxy、
redirect、retry、HTTP/2、keep-alive、compression、cookie、credentials、conditional request 与自定义
header。DNS 结果上限为 16，任一 special-use/non-public 地址使整次 observation 失败；通过后确定性选择
一个 literal IP 拨号，TLS 最低 1.2，status/MIME/header/body/total time 均有界。

Store 在 Source I/O 前返回 defensive-copy 的 exact Source/Policy/Key basis；I/O 后必须在同一
`BEGIN IMMEDIATE` 中重验 Source identity、Policy ID/revision、Publisher Key ID/revision 与 revocation。
stale、revoked 或被篡改的 basis 零写。相同 current Index 的 exact retry 返回相同 Snapshot、observation
revision 与 timestamp；A→B→A 是新的 stale observation，不得复活旧 Snapshot 充当 retry。

Snapshot 只保存 authority-free SourcePolicy→Index→Snapshot parent、ordered entries 与观察 revision。
所有 entry 声明的 package size 总和受 Core-owned 1 GiB ceiling；全局 `ModuleID + opaque exact Version
→ ArtifactDigest` 映射同时约束跨 Source Snapshot、Installation 与 materialized Learning Version。同
ref/同 digest 可共享，异 digest 失败关闭。

Source Provider 不能写 Store、下载 package、读取 entry package path、验证 detached Signature、解析
Secret、创建 Host 或调用 Apply。U2 不创建 Candidate、Decision、reservation、stage、Installation、
Activation、Binding、Control/Catalog revision、Run、Attempt、Usage 或 external effect。Pure Chat 未显式
运行上述命令时对 Source Provider 与 discovery tables 零访问。Backup/Verify/Restore 只对 bundle 内
Store facts 做 canonical/parent/global-ref semantic verification，零网络、零 Source/包读取，也不自动
refresh。U3 已按 §25 从 exact current Snapshot 生成并审核无权限 Candidate/Review/Decision；U4 在真正
staging 前必须再次读取 current facts，并继续走唯一 `module-dry-run` / `module-apply` / CAS。

### 5.3 Install、Activate、Bind

```text
Install  = 由具体受权入口复验包和 ArtifactDigest 后登记；U1 observation 本身不是 authority
Activate = 创建不可变实例并由 Core 分配 ExecutionClass
Bind     = Assembly Compiler 将已激活实例写入 PortPlan
```

三者不得合并：

- Installed 不等于 Active。
- Active 不等于某个 Agent 或 Workspace 获得使用权。
- 升级产生新版本和新 Activation Record，不修改已有 Run。
- 撤权只能拒绝后续调用，不能改写历史快照；W2-R3 当前实现为停机 Disable，不声明热撤权。

```go
type ExecutionClass string

const (
    ExecutionDeclarative      ExecutionClass = "DECLARATIVE"
    ExecutionTrustedInProcess ExecutionClass = "TRUSTED_IN_PROCESS"
    ExecutionLocalProcess     ExecutionClass = "LOCAL_PROCESS"
    ExecutionRemote           ExecutionClass = "REMOTE"
    ExecutionWASM             ExecutionClass = "WASM"
)
```

S1 只实现 `DECLARATIVE` 和精确 ArtifactDigest allowlist 内的
`TRUSTED_IN_PROCESS`。`LOCAL_PROCESS`、`REMOTE` 和 MCP 属于 S2，S1 不得注册
伪实现或静默降级。

S2 的首个 `LOCAL_PROCESS` 授权只适用于 §7.8 的 MCP stdio Tool Adapter。它不能
绑定 Core 控制类 Port，也不能据此启用通用子进程、REMOTE 或其他 MCP 能力。

W2-R1 随后只为 `action.provider/v1 + REMOTE/freeagent-action-http/v1` 授予一个窄
`REMOTE` Activation。该增量不追溯修改 S1 或 MCP 的历史边界，不允许绑定 Core 控制类
Port，也不能泛化为其他 REMOTE、任意 HTTP 或不可信本地执行。

W2-R2 随后只为 `action.provider/v1 + WASM/freeagent-action-wasm/v1` 授予一个窄
`WASM` Activation。它不追溯修改 S1、MCP 或 R1 的历史边界，不允许绑定 Core 控制类 Port，
也不能泛化为其他 WASM 协议、WASI、Host imports、任意第三方进程内代码或生产级恶意多租户隔离。

无论处于哪个阶段，Core 控制类 Port 只能绑定 ArtifactDigest 位于 Core allowlist
的 `TRUSTED_IN_PROCESS` 实例；`LOCAL_PROCESS`、`REMOTE` 和 `WASM` 不得绑定 Core
控制类 Port。

### 5.4 Module Host

```go
type ModuleInvocation struct {
    InvocationID        string
    RunID               string
    MemberID            string
    MemberSnapshotDigest string
    Port                 PortRef
    BindingIndex         uint32
    Input                json.RawMessage
    Deadline             time.Time
}

type InvocationResult struct {
    InvocationID string
    Provider     ActivatedModuleRef
    Outcome      SUCCEEDED | FAILED | UNKNOWN
    Output       json.RawMessage
    UsageReceipt json.RawMessage
    UnknownClass string // optional; fixed safe enum, UNKNOWN only
}
```

`PreparedInvocation` 还必须由一次性 Gate 携带 `ConfigCanonical`。该值只能是当前
`PortBinding.ConfigRef` 指向并经 Core 恢复、摘要闭合后的 canonical 内容；调用方和
Adapter 不能按名称、环境或 CLI 参数重选配置。对于 `model.generate/v2`，Gate 在
PENDING Attempt 已持久化后、网络调用前注入精确 `model-binding-config/v2`；该瞬态字节
不新增 Store 列，也不构成第二份配置事实。动态 Secret 值仍不得进入该内容，只能由
composition root 的受控 Secret resolver 在 Adapter 内按引用临时取得。

Host 必须：

- 使用 `MemberSnapshotDigest + PortRef + BindingIndex` 取得并验证冻结的
  `PortBinding`，不得仅凭 PortRef 搜索 Provider。
- `InvocationGate` 返回的 invocation identity 必须与请求逐字段一致；调用方不能
  直接提交 `PortBinding`。
- Adapter 只能按精确 `ArtifactDigest + AdapterIdentity` 解析；禁止按 ModuleID、
  version、Port、健康度或“最新版本”搜索。
- 本地 Adapter Registry 在 composition root 构造唯一解析对象；解析策略与 eager 注册
  不可变，注册键只有 `ArtifactDigest + AdapterIdentity`。构造时拒绝空注册、重复精确键、
  空值、错误动态类型与 typed nil；运行期不提供枚举、按名称/版本/Port 查询或 fallback。
- 可选 Loader 只在已经通过 PublishedBasis、冻结 Binding、Authority 和 current Activation
  边界的 `ResolveExact` 首次调用中物化实现。私有 cache 与 in-flight map 是可丢弃的进程内
  派生状态：同一精确键最多一个并发 Loader，不同精确键可并行；只有非 nil 成功结果进入
  cache，普通失败、取消、typed nil 与 panic 均不形成成功缓存。panic 必须先清理并唤醒
  同键等待者再继续抛出。Loader 不得执行模块或外部效果、回调 Registry、模糊搜索或
  fallback；它必须在自身控制的阻塞边界响应 Context。
- 首个调用者的 Context 只管理当次物化；等待者可以按自己的 Context 退出。当物化仅因
  leader Context 结束而失败时，仍有效的等待者继续完成自己原本的精确解析；这只是无外部
  效果的本地构造接力，不是模型、Action、MCP、Channel 或 UNKNOWN 的语义重放。普通 Loader
  错误仍由本次同键等待者共享，不自动重试。
- Loader 与 Adapter 的错误链必须让 `errors.Is` 继续识别原始 `context.Canceled` 或
  `context.DeadlineExceeded`；不得把取消原因格式化成普通文本。读取构件时使用可取消的有界
  遍历和分块读取，取消不返回部分文件。第三方 MCP SDK 若在关闭竞态中丢失原因，本地边界
  必须合并自己的 child Context 原因；这只修正错误分类与同键接力条件，不授权 Action、模型、
  Channel、MCP 效果或 UNKNOWN 语义重放。
- `IsRegistered` 永不触发 Loader；同键已有 in-flight 时只等待该 attempt 或自身取消，然后
  读取 cache。对 lazy Registry，返回 true 只表示“已物化”，不证明可加载、Trust、Catalog
  成员或授权。Activation Resolver 必须继续使用由显式本地 grant/allowlist 构造的 eager
  精确注册视图，不能把 production lazy cache 当成第二权限事实源。
- 校验 ArtifactDigest、ActivationRevision、AdapterIdentity、撤权、预算、
  deadline、lease 和 AuthorityCeiling。
- 受控注入配置、Secret 句柄和作用域化 Module State。
- Host 将请求的 InvocationID 写入结果，并返回与冻结 Binding 完全相同的 Provider
  identity、结果和 Usage Receipt；Core 终态提交必须同时核对 InvocationID 与
  Provider，不能把另一调用的合法结果接到当前 Attempt。
- Adapter 可在 `UNKNOWN` 结果上附带一个 Host allowlist 内的固定 `UnknownClass`，只描述
  本地可观察边界；`SUCCEEDED/FAILED` 禁止携带该字段，未知枚举一律 fail-closed。字段
  缺失时保持兼容并由 Core 使用通用 `MODEL_UNKNOWN`。该字段禁止自由文本、原始 error、
  URL、Header、请求/响应正文、Secret、动态 ID、时间戳或耗时。
- 不选择模块、不改写 PortPlan、不读取其他 Workspace 状态。

S1 Host 只接受 `DECLARATIVE` 与 `TRUSTED_IN_PROCESS`。Adapter 的结果必须显式为
`SUCCEEDED`、`FAILED` 或 `UNKNOWN`；Host 不从普通 error 推断“肯定未执行”，也不
重试、不切换 Binding。`UNKNOWN` 由拥有该 Port 的 Core 消费者持久化并进入对账。

测试和本地纵向链可以显式注册 deterministic Echo Adapter。Echo 不是生产 fallback：
它只接受严格恢复成功的 canonical `ModelGenerateRequestV1`、精确
`model.generate/v2` Port 和构造时冻结的完整 Provider identity；它只回显最后一条
语义消息，不向正文加入 Run/Attempt/Invocation/时间戳。输出必须是 canonical
`ModelGenerateOutputV1`，Usage 必须是 canonical `ModelUsageReceiptV1`；Echo
未知的 token、cache 和 reasoning 字段保持 `null`，不得估算或伪造。

S3-C 真实使用验证允许显式选择一个精确 allowlist 的 DeepSeek
`model.generate/v2` Adapter，但它不是默认 Provider，也不是 OpenAI-compatible 通配
Adapter。首片边界冻结为：

- 只接受 Control/Catalog 已选择的精确 ArtifactDigest + AdapterIdentity，以及 Gate 注入
  的 `model-binding-config/v2`；公开模型 alias 和允许的观测 build 标识由本地 trust set
  精确列举，不冒充供应商未公开的内部权重 build。
- 只调用编译进 Adapter 的官方 HTTPS chat-completions endpoint；不接受模块配置覆盖
  endpoint，不跟随 redirect，不重试，不切换模型或 Binding。
- API key 只由显式运行时 Secret resolver 取得；seed、ConfigRef、Attempt、Usage、日志和
  测试证据均不得保存 Secret 值。
- 已取得明确 HTTP 响应的 Provider 拒绝返回 `FAILED`；请求可能已经发送但无法判定外部
  结果的 transport/timeout 返回 `UNKNOWN`；两者都不得语义重放。只有发送前的本地合同
  错误可以作为普通 error 拒绝 permit。
- 对未来的 `UNKNOWN`，Adapter 只允许返回
  `INVOKE_RETURNED_ERROR`、`NO_USABLE_RESPONSE` 或
  `RESPONSE_BODY_READ_INCOMPLETE` 三个固定观察位置码。它们只表示本地 `http.Client`
  返回错误、没有可读取响应，或响应体未完整读出；均不能证明 Provider 是否收到、执行或
  完成请求。旧 Attempt 不得追溯补码或据此改写终态。
- 首片只支持无 Action 的文本生成；出现 Action definitions 必须确定性 fail-closed，不能
  静默降级为普通文本。
- Provider Usage 只按原响应映射 input、cache hit、cache miss、output 和 reasoning token；
  缺失字段保持 UNKNOWN。安全 raw receipt 只保存 Usage 和非敏感请求元数据，不保存
  reasoning content、Authorization 或完整响应正文。
- 成功终态只提交 token Usage 与 `usage_status`。FAC2 基线已删除价格快照与估算派生，
  Adapter 不得引入任何金额字段、计价表或旁路账本。

该 Adapter 仍通过同一个 ModuleHost、Universal Loop、Current Store、Usage Ledger 和
Scheduler；它不增加 Provider 专用 Runtime、Store、重试器或第二账本。

模块不得直接访问 Core Store、全局 Secret 或可复用 AuthorityGrant。第三方模块
的新增不得要求修改 Universal Loop；它只能实现已发布 Port 或随 S2 工作包新增
一个版本化 Port。

Action 是唯一需要公开准备与私有执行分离的 Port。`sdk/moduleapi` 暴露
`ActionProviderV1.Describe/Prepare` 以及供 Host Adapter 实现者使用的版本化执行
request/result 数据类型，但不发布 Execute Port、执行路由或调用资格；实际 executor
接口只位于 `internal/modulehost`：

```go
type ActionExecutor interface { // only inside the repository's internal boundary
    ExecutePrepared(context.Context, ActionExecutionRequestV1) (ActionExecutionResultV1, error)
}
```

同一 exact Registry 对象可以同时实现公开 Provider 与上述内部接口，但不得建立第二张
executor Registry。只有 `internal/actiongateway` 可以取得并调用该接口；Loop、
LocalChat、模型 Adapter 与第三方 SDK 均不得解析或持有 executor。普通
`ModuleInvocation` 必须拒绝 Execute 操作，防止把 Gateway 绕成可由模型直接调用的
公共 Port。

## 6. 唯一 Assembly Compiler

### 6.1 接口

```go
Compile(context.Context, CompileInput) (CompileOutput, error)
CompileCompositeFamily(context.Context, CompositeCompileInput) (CompositeCompileOutput, error)

type CompositeCompileInput struct {
    Parent CompileInput
}

type CompositeCompileOutput struct {
    Parent   CompileOutput
    Children []CompositeChildCompileOutput
}

type CompositeChildCompileOutput struct {
    CompileOutput
    Assignment      CompositeAssignmentV1
    IntentCanonical []byte
    IntentDigest    string
}
```

普通 `CompileOutput` 不携带 Child 列表；只有显式 `CompileCompositeFamily` 才返回有界
family。调用方仍只提供 Parent `CompileInput`，Child 身份和 intent 由同一个 Compiler
确定性派生。

`CompileInput` 是一个不可变值，必须一次性包含：

| 内容 | 必需信息 |
|---|---|
| Admission intent | exact `AdmissionIntentV1` canonical bytes + digest |
| 生成身份 | RunID、MemberID、RecoveryRootRef |
| Composite identities | 调用方只提供 Parent 身份；选择 Composite 时唯一 Assembly Compiler 按 Parent intent digest + slot 确定性派生 Child RunID、MemberID、RecoveryRootRef 与 AdmissionKey |
| 发布基线 | TenantID、Control/Catalog refs 与 `control_current.pointer_revision` |
| 控制输入 | exact ControlSnapshot canonical bytes |
| 可用能力 | exact RuntimeCatalogGeneration canonical bytes |
| Action 定义 | 仅当 Profile 装配 `action.provider/v1` 时，已审核的有序 materialized definitions |

ControlSnapshot 冻结 tenant 内可选的 typed Agent、Workspace 与 Profile；
Profile 给出 Context/Scheduling PolicyRef、
可选 ModelProfileRef 和有序 BindingSpec。Assembly Compiler 只把该精确画像引用冻结到
成员快照；发布、Admission、Context 与 Loop 按引用闭合 canonical CONFIG，不读取
current 或 latest 画像。BindingSpec 只有 exact PortRef、InstanceID、ConfigRef、
AuthorityCeilingRef、有序 StaticContextRefs 与 FailurePolicy，不能携带 Provider
identity。StaticContextRefs 只能出现在 `context.provide/v1` BindingSpec。

Parent/Child + Composite 第一实现切片允许 `control-snapshot/v1` 增加下列 optional
配置；没有配置时字段必须省略，既有 Pure Chat canonical bytes 与读取闭包保持不变：

```go
type CompositeAgentDefinitionV1 struct {
    SchemaVersion        string // composite-agent/v1
    AgentID              string // coordinator Agent
    CoordinatorProfileID string
    Members              []CompositeAgentMemberV1
}

type CompositeAgentMemberV1 struct {
    SlotID             string
    AgentID            string
    ProfileID          string
    FocusID            string
    WeightBasisPoints  uint32
}
```

`ControlSnapshot.CompositeAgents` 是按 `AgentID` UTF-8 字节序排列的 optional
数组，同一 coordinator 只能出现一次。每个配置必须有 2..8 个 Member；Member 按
`SlotID` UTF-8 字节升序规范排列且唯一，所有 ID 非空，`WeightBasisPoints` 为 1..10000 的整数且
总和必须精确等于 10000。Coordinator 是本次 Admission 已选择的 Parent Agent，且
`CoordinatorProfileID` 必须等于 Parent Profile；Child
可以选择不同的 exact Agent/Profile，但所有引用必须来自同一 exact ControlSnapshot。
该配置只表达冻结 family 与 Context 分配，不授予权限，不参与模型 Provider 路由、RAG
rank、Skill 顺序、Reviewer 标准或任何“更适合”推断。

RuntimeCatalogGeneration 冻结 tenant、generation、Control ref/digest 和
`CatalogEntry{ActivatedModuleRef, Provides[]}`。CatalogEntry 按 InstanceID 排序，
Provides 按 exact PortRef 排序；Profile BindingSpec 的数组顺序保持语义顺序。
ControlSnapshot 不得包含 Catalog ID 或 digest，摘要依赖只能
`Control → Catalog → MemberSnapshot → Manifest`。

精确 schema version 为 `control-snapshot/v1` 与 `runtime-catalog/v1`；摘要域分别为
`freeagent.control-snapshot/v1` 和 `freeagent.runtime-catalog/v1`，计算时排除各自
digest 字段。Compiler 必须 strict restore canonical bytes，并证明：

```text
PublishedBasis
  → exact Control
  → exact Catalog linked to that Control
  → Profile BindingSpec
  → exact CatalogEntry/ActivatedModuleRef
```

禁止同时向 Compiler 传入一个 CatalogRef 和另一组可独立拼装的 CatalogEntries；
Store 仍须在 Admission 事务内重新校验 PublishedBasis。

调用 Compiler 前必须完成读取和物化；Compiler 不读取 Store、远程服务或可变
`current`，也不得自行扩展输入字段。

物化边界只允许读取必需 Core/Model Catalog 和控制输入中已经精确点名的模块引用，
不得枚举可选仓库。Role/Prompt 的精确声明式引用可以直接物化；没有 Skill、RAG、
Memory、MCP 或 Action 请求时，不得为了判断能力是否存在而读取对应仓库。

### 6.2 编译顺序

唯一合法顺序：

1. 校验 System、Profile、Agent、Workspace、Task 和策略引用。
2. 在读取任何可选能力仓库前推导是否为 `PURE_CHAT`。
3. 从已物化 RuntimeCatalog Snapshot 解析 required Port 和显式请求的 optional
   Port。
4. deny-first 合并权限、资源范围和 Effect 上限。
5. 将所有兼容范围解析为精确版本和不可变 Activation Record。
6. 按 Port 定义验证基数，冻结 PortPlan 和 Binding 顺序。
7. 若选择 Composite，校验唯一 coordinator 配置、2..8 个 Child、整数权重总和、同一
   Tenant/Workspace、深度 1、允许的首片 Port 与 family dispatch cap；再从 Parent
   `AdmissionIntentDigest + slot_id` 用冻结域分隔符派生 Child 的 RunID、MemberID、
   RecoveryRootRef 和 AdmissionKey。Compiler 不得读取时钟/随机数生成这些身份。未选择
   Composite 时不得读取或物化该 optional 配置。
8. 若存在 Action PortPlan，逐项闭合 materialized definition 的 BindingIndex、
   Config/Authority/Effect、结果上限与成员内唯一 PublicActionID；否则要求定义输入
   严格为空。
9. 生成全部 MemberExecutionSnapshot，并计算 `MemberSnapshotDigest`。
10. 用第 7 步已派生的 Child identity 先生成冻结有序 Child ref 的 Parent RunManifest 并
    计算 digest，最后生成引用该 Parent manifest digest 与 exact slot 的 Child
    RunManifest；不得建立 digest 环。

required 能力缺失、精确版本不可用、权限冲突、摘要不匹配、重复/空 PortPlan 或
不受支持的 ExecutionClass 必须拒绝 Admission。未实现能力不得被忽略或降级为
Pure Chat。

Compiler 返回后，Admission 边界必须在一个 Current Store 事务中写入
MemberExecutionSnapshot、RunManifest 和初始 LoopFrame。Composite 时该单一事务必须
原子写入整个 Parent/Child family；任一成员校验或写入失败则全部回滚，绝不能留下孤立
Parent 或 Child。Compiler 自身不持有 Store 写权限，也不存在第二个 family compiler。

编译失败直接返回带输入位置和原因的结构化错误；成功解释信息只能由编译输入和
MemberExecutionSnapshot 按需派生，不定义或发布独立 Compile Receipt。

### 6.3 确定性

AdmissionKey、AdmissionIntentDigest、RunID、MemberID、TaskInput、deadline 和预算
均属于已物化输入。同一完整
`CompileInput` 必须产生字节一致的：

- PortPlan 顺序；
- Binding 顺序；
- MemberExecutionSnapshot；
- MemberSnapshotDigest；
- RunManifest。

外部定义变化只能产生新 revision 和新 Run。Agent 自我编排只能提交新装配提案，不能
改变本 Run。Parent/Child + Composite 第一切片的 Child 只可由 Admission 在发布 family
前按冻结配置一次性创建；Universal Loop 绝不创建 Child。未来 Scheduler 即使获得动态
Admission 能力也只能提交新 family，不得修改已经发布的快照或补造当前 family 的 Child。

## 7. 冻结运行对象

### 7.1 MemberExecutionSnapshot

```go
type MemberExecutionSnapshot struct {
    SchemaVersion       string
    CompilerVersion     string
    Catalog             CatalogSnapshotRef
    MemberID            string
    Agent               AgentRef
    Profile             ProfileRef
    ModelProfile        *ModelProfileRef // optional
    Workspace           WorkspaceRef
    PortPlans           []PortPlan
    Actions             []FrozenActionDefinitionV1 // optional, omitempty
    ContextPolicy       PolicyRef
    SchedulingPolicy    PolicyRef
    MemberSnapshotDigest string
}
```

Role、Skill、RAG、Memory、MCP、人格和情感能力不得增加专用快照字段，只能从
PortPlan 派生。`Actions` 是唯一受控例外：它不是新的模块路由，而是
`action.provider/v1` 在 Admission 前已物化的
`PublicActionID → BindingIndex + ProviderActionID + Definition`
恢复映射；没有 Action PortPlan 时字段必须省略。`ModelProfile` 是对已经由唯一
`model.generate/v2` Binding 选定的
精确模型构建施加运行约束的可选内容引用，不是第二个模型 Port 或模块路由字段。
Composite assignment 不写入 Member snapshot；它属于下节 `RunManifest.Composite` 的
family 图。这样 Member snapshot 仍只描述一次成员装配，专业分工不成为第二个 Provider
路由或模块事实源。

`MemberSnapshotDigest` 使用
`SHA256("freeagent.member-snapshot/v1\0" + CanonicalJSON(snapshot))` 计算；
参与计算的 snapshot 必须排除 `MemberSnapshotDigest` 字段自身。

### 7.2 RunManifest

```go
type RunManifest struct {
    SchemaVersion     string
    CoreRuntimeVersion string
    AdmissionKey      string
    AdmissionIntentDigest string
    RunID             string
    TenantID          string
    Workspace         WorkspaceRef
    PrimaryAgent      AgentRef
    Members           []MemberSnapshotRef
    PrimaryMemberID   string
    TaskInputRef      string // TASK_INPUT ContentDigest
    TaskInputDigest   string // 必须与 TaskInputRef 相同
    ParentRunID       string // Child only; optional, omitempty
    CancellationScope string
    Deadline          time.Time
    RecoveryRootRef   string // 预分配 Store key，不得依赖任何 Manifest/Snapshot digest
    ConversationTurn *ConversationTurnRefV1 // optional, omitempty
    Composite         *CompositeRunNodeV1 // optional, omitempty
    ManifestDigest    string
}

type ConversationTurnRefV1 struct {
    SchemaVersion   string // conversation-turn-ref/v1
    ConversationID  string
    PrincipalID     string // evidence only; never projected into model messages
    TurnIndex       uint64 // 1-based; equals the post-CAS Conversation revision
    PredecessorRunID string // first turn only: omitted
}

type CompositeRunNodeV1 struct {
    SchemaVersion        string // composite-run-node/v1
    Role                 string // ROOT | CHILD | REVIEWER
    RepairRound          uint32 // 0 | 1; omitted when zero
    RootRunID            string
    ParentManifestDigest string // CHILD/REVIEWER only; optional, omitempty
    ParentSlotID         string // physical slot; CHILD/REVIEWER only
    Assignment           *CompositeAssignmentV1 // CHILD only; optional, omitempty
    Plan                 *CompositeRunPlanV1 // ROOT only; optional, omitempty
}

type CompositeRunPlanV1 struct {
    MergeLogicalStepID       string // composite.merge/v1
    FamilyModelDispatchLimit uint32 // Decision: 2*len(Children)+3
    Children                 []CompositeChildRunRefV1
    Reviewer                 *CompositeReviewerRunRefV1 // optional legacy/W5 reviewer
    Decision                 *CompositeDecisionPlanV1 // optional W5 decision
}

type CompositeChildRunRefV1 struct {
    SlotID               string
    ParentSlotID         string // repair physical slot only; optional
    RunID                string
    AdmissionKey         string
    MemberSnapshotDigest string
    Agent                AgentRef
    Profile              ProfileRef
    TaskInputRef         string
    Assignment           CompositeAssignmentV1
    Transfer             *WorkspaceTransferPlanV1 // cross-Workspace only
}

type CompositeReviewerRunRefV1 struct {
    ParentSlotID         string // repair Reviewer physical slot only; optional
    RunID                string
    AdmissionKey         string
    MemberSnapshotDigest string
    Agent                AgentRef
    Profile              ProfileRef
    TaskInputRef         string
    ReviewLogicalStepID  string
    Policy               string
    MaxOutputTokens      uint32
}

type CompositeDecisionPlanV1 struct {
    SchemaVersion  string // composite-decision-plan/v1
    RepairChildren []CompositeChildRunRefV1
    RepairReviewer CompositeReviewerRunRefV1
}

type CompositeAssignmentV1 struct {
    SlotID            string
    FocusID           string
    WeightBasisPoints uint32
}
```

每个 Run 始终必须恰有一个 Member 和一个 Workspace；Composite 通过多个单 Member Run
表达，绝不能把多个 Member 或 Workspace 塞入同一个 Run。W5 Decision family 含 N 个
Specialist 时预冻结初始 N Child、Reviewer0、repair N Child、Reviewer1 与 Root，共
`2N+3` 个物理 Run；repair 是否执行只改变既有 Run 状态，不改变图。既有 S1/S2 非
Composite Run 的 `ParentRunID` 与 `Composite` 必须全部省略。Manifest 发布后不可修改，
状态变化只能写入 LoopFrame 或 Ledger。
`AdmissionKey` 在 Tenant 内唯一；同一显式 User/Operator intent 的 client retry、
提交响应丢失、进程崩溃、恢复、Scheduler 重试和 continuation 必须复用同一个
AdmissionKey、AdmissionIntentDigest 与 RunID。相同 AdmissionKey 且 intent digest
相同时幂等返回已有 Run；digest 不同时 fail-closed。Runtime、Loop、Scheduler 和
Module 都无权通过新建 Run 绕过既有 Attempt；只有新的显式 User/Operator
Admission 可以分配新的 AdmissionKey 和 RunID。
`TaskInputRef` 必须是按 Current Store ContentDigest 公式计算的 `TASK_INPUT`
记录键，`TaskInputDigest` 必须与其相同。Manifest 和 MemberSnapshot 中的每个
PolicyRef 都必须指向已持久化的 `POLICY` ContentRecord；恢复不能依赖 seed alias、
当前 Control 或进程内 policy 对象。
`ManifestDigest` 使用
`SHA256("freeagent.run-manifest/v1\0" + CanonicalJSON(manifest))` 计算，并排除
`ManifestDigest` 字段自身。`RecoveryRootRef` 必须在计算摘要前分配，其值不得包含
或派生自 ManifestDigest、MemberSnapshotDigest。

#### 7.2.1 W1 Conversation 与不可变 turn

W1 的持久产品名只使用 **Conversation**。每一轮用户输入都必须创建一个新的、装配不可变
的 Run；不得把后续 USER 输入追加到旧 Run，也不得让一个 Run 代表多个聊天轮次。这里的
“不可变”指 Run identity、Manifest、成员装配和 turn 关系不可修改；Run/Frame/Attempt 的
生命周期仍按既有 CAS 状态机推进。

Conversation 创建时固定稳定的
`TenantID + PrincipalID + WorkspaceID + AgentID + ProfileID` 五元组。这里冻结的是稳定 ID，
不是把当时的 exact revision 永久复制到 Conversation：每个新 Run 仍按自己的 Admission
冻结 exact Workspace/Agent/Profile ref，所以新配置只影响新 Run，历史 Run 不被改写。
同一 Conversation 的后续 turn 不得切换上述五个稳定 ID；需要切换时，用户必须显式新建
Conversation。

loopback 产品入口以 `POST /v1/conversations` 创建上述 revision 0/head 为空的固定 scope
Conversation。首次创建返回 `201`；相同 ConversationID 与完全相同五元组的精确重试返回
同一持久记录和 `200`；同 ID 的任一 scope 漂移必须返回冲突。该入口不得 Admission Run、
调用模型或读取可选模块。后续 `POST /v1/chat` 必须提交刚观察到的 exact revision，并从
第二轮起提交 exact head RunID；服务不得替调用方刷新陈旧 head。两条 HTTP 路径都只能绑定
loopback，不构成公网 API。

Current Store 是 Conversation head 的唯一事实源。创建后 revision 为 0 且 head 为空；首轮
Admission 在发布 Run 的同一事务中 CAS 为 revision 1/head=新 Run，后续轮次只能从调用方
声明的 exact revision/head 做一次 CAS。Store 必须先用 `GetTerminalRunResult` 等价的完整
闭包证明当前 head 是**成功返回最终 Assistant 结果**的 Run，才允许把 head 推进到下一
Run。新 Run 一经成为 head，在其成功前不得并发创建下一 turn；若 head 为 PENDING、
MODEL_UNKNOWN/其他 UNKNOWN、明确 FAILED 或 CANCELLED，Conversation 一律 fail-closed，
不能跳过、分叉、自动重试或自动创建替代 Run。用户仍可显式新建一个 Conversation。

`ConversationTurnRefV1` 是 RunManifest 中唯一的 turn 关系冻结对象。`PrincipalID` 必须与
AdmissionIntent 及 Conversation owner 逐字节相同，只进入恢复/审计 evidence，绝不能进入
模型 prompt。`TurnIndex` 必须等于 Conversation CAS 后的 revision；首轮省略
`PredecessorRunID`，后续轮次必须逐字节等于 CAS 前 head。它与 `Composite` 在 W1 第一
切片中互斥。未使用 Conversation 时字段省略，
既有普通/Composite Run canonical bytes 不变。该结构只增加同一 Assembly Compiler、
Universal Loop 与 Current Store 的输入/恢复边，不创建 Conversation Runtime、Loop、
Store 或第二 History。

每个 turn 的 input/output/cache/reasoning token 继续只来自该 Run 的现有
ModelDispatchAttempt 与 `model_usage`；Conversation 不建立第二 Usage
ledger。Provider 未披露 token 或尚未对账时必须保持 `UNKNOWN`，不能写成 0
或由 Conversation 聚合反推。模型凭据只允许在调用边界通过运行时 SecretRef/受控 Host
解析；Secret 值不得进入 Conversation、Admission、Manifest、History、Usage、日志或备份。
备份/验证/恢复只能把 SecretRef 当不透明标识闭合，调用 Secret resolver 的次数必须为零。

Conversation 是 Core 对普通聊天的必要状态，不是可选知识/人格模块；因此 Pure Chat 可以
读取其 exact head 与 predecessor 投影。除此之外，未装配 Role、RAG、Memory、Skill、MCP、
Action、Channel 或 Team 时，对这些可选仓库、Host、Secret 和外部效果账本的访问仍必须为零。

本小节的第一原子合同已由 SQL、Store API、Assembly/Loop、Context Compiler、
CLI/HTTP Pure Chat 入口与 backup/verify/restore 实现闭合，当前状态为
`W1_CONVERSATION_ACCEPTED_DEVELOPMENT_SLICE`。该状态只验收**普通单 Agent Pure Chat**
的 Conversation 纵链：每轮一个不可变 Run、exact revision/head CAS、predecessor 恢复、
完整 USER+ASSISTANT pair 压缩/Drop 和备份后续转。`ConversationTurn` 与 Composite 仍互斥；
Action 和 Channel 上的 Conversation 不在本切片成熟性声明范围内。

当前实现证据包括：

- `TestCommitConversationTurnAdmissionPublishesFirstTurnAndProjection`、
  `TestCommitConversationTurnAdmissionExactRetryPrecedesCurrentAndHeadChecks` 与
  `TestCommitConversationTurnAdmissionConcurrentCASHasOneWinner` 闭合原子 Admission、
  exact retry 和并发单胜者；
- `TestChatServiceConversationRestoresCompletePairsAndAdvancesHeadAtomically` 闭合真实
  ChatService 两轮链，请求顺序固定为旧 `USER → ASSISTANT` pair 后跟当前 `USER`；
- `TestCompileV1ConversationAt85SummarizesOldestCompletePair`、
  `TestCompileV1ConversationAt100DropsMinimalCompletePairPrefix` 与
  `TestChatServiceConversationContextThresholdsKeepPairsIndivisible` 证明 85% 只做一次确定性摘要，
  100% 从最旧完整 pair 开始移除最小有序前缀，不拆分 turn；
- `TestRunConversationCommandsAndTwoTurnChat`、
  `TestPostConversationCreatesAndExactlyReusesConversation` 与
  `TestPostChatCarriesExactConversationCASAndReturnsNewRevision` 闭合 CLI/HTTP 产品输入边；
- `TestRunServeLoopbackAndGracefulShutdown` 闭合 HTTP Conversation 首次创建 `201`、精确重试
  `200`、`/v1/chat` 第一轮、服务重启后原请求精确重入与第二轮续聊；
- `TestConversationBundleRoundTripPreservesLinearClosureWithoutExecution` 闭合两轮
  backup/verify/restore、零模型执行与恢复后第三轮续转；篡改 owner/head/revision/
  turn/predecessor/Manifest 的负向矩阵继续 fail-closed。
- `TestRunConversationFiftyTurnsAcrossReentryBackupRestore` 以确定性本地 Provider
  闭合 50 个 Run/Attempt/Usage，在 turn 25 做 backup→verify→restore 后续聊，并在
  turn 25/50 做 exact retry、生成最终 bundle，Action/Memory/Channel 可选访问为零；
  Windows 用时 `21.964s`，WSL 用时 `15.594s`。这是本地重入/备份长链，
  不等于真实 DeepSeek 50 轮效果或 Usage 验收。
- `TestRunConversationFiftyTurnsAcrossNormalProcessRestart` 由两个正常退出的独立 OS 进程
  依次完成 1..25 和 26..50 轮；第二个进程只从持久 head/revision 恢复，最终闭合 50 个
  Run/Attempt/Usage，Action/Memory/Channel 为零。它证明正常进程退出边界，但同样不能替代
  真实 DeepSeek 2/50 轮。

这不等于 W1 完成。真实 DeepSeek 50 轮长对话和 W1 全工作包验收尚未执行；
完成前不得将本切片表述为通用多 Agent、Action/Channel Conversation 或发布就绪。
本次 HTTP 与正常进程重启没有增加第二 Runtime、Store、Loop、History、Usage
或账本，也没有把任何可选专业模块固化进 Agent；最初的轻量模块化边界保持不变。

##### 7.2.1.1 W1 当前收口增量

后续 `W1_COMPLETE_REAL_DEEPSEEK_50` 已完成真实 50 轮与恢复门禁；该状态不改写上面的历史
切片记录。`TestW1PureChatUnifiedZeroOptionalModulesAcceptanceV1` 又在生产 Composition、Universal
Loop、Action-capable ChatService、Module Host 与 Current Store 上集中证明：Knowledge、Memory、
静态 Skill、MCP/Action 和 Channel Provider 可以已安装、激活并 Catalog-visible，但只要未被
Profile/Workspace 选择，运行时只解析冻结 Model；MODEL_REQUEST 无动态 Context/Action，Memory、
Channel、Learning、Transfer、Composite 与可选 Content 均零增量。Conversation+Action/Channel/
Composite 的成熟度仍未因此扩大。

#### 7.2.2 Composite family 的冻结图

Root 是 coordinator；每个 Child 是 specialist，Reviewer 是独立普通 Agent。整个 family
必须同属一个 Tenant；每个 Run 仍只有一个 Workspace。Root、Reviewer0/1 留在 Root
Workspace，初始/repair Child 可以按冻结 `WorkspaceTransferPlanV1` 使用另一 Workspace，
并可使用与 Root 或其他 Child 不同的 exact Agent/Profile。最大深度固定为 1：Root 可以有
物理 Child/Reviewer，Child/Reviewer 的
`Composite.Plan` 必须省略且不能再成为 Parent。所有 manifest 的 `TaskInputRef` 与
`TaskInputDigest` 必须逐字节相同，均指向同一份 `TASK_INPUT`；specialist focus 只能由
冻结 `CompositeAssignment` 作为 Core 受保护上下文传递，不能改写、包装或派生新的
任务输入。

Root manifest 的 `Composite.Plan.Children` 必须冻结完整、有序的 2..8 个初始 ref。规范顺序是
`WeightBasisPoints` 降序，权重相同时按 `SlotID` UTF-8 字节升序；slot、RunID 与 Member
ref 均唯一，并逐项匹配 exact Control 配置和 Child snapshot。Parent ref 冻结 Child
RunID、AdmissionKey、MemberSnapshotDigest、Agent/Profile、TaskInputRef 与 Assignment，
但不引用 Child ManifestDigest。Compiler 必须先生成全部成员快照，
再生成 Parent manifest/digest，最后生成 Child manifest；Child 通过非空且一致的
`RunManifest.ParentRunID + Composite.ParentManifestDigest + Composite.ParentSlotID`
反向引用 Parent。该单向规则禁止
manifest digest cycle，也禁止事后增删或重排 Child。

每个 Child 的 AdmissionKey、RunID、MemberID 与 RecoveryRootRef 都必须在 Parent manifest
计算前，由 Root 的 `AdmissionIntentDigest + SlotID` 做域分离确定性派生。四个最终前缀依次为
`composite-child-admission-`、`composite-child-run-`、`composite-child-member-` 与
`composite-child-recovery-`；时间、current pointer、Parent/Child manifest digest 均不得进入
输入：

```text
seed = CanonicalJSON({parent_intent_digest, slot_id})

admission_digest = SHA256("freeagent.composite-child-admission-key/v1\0" + seed)
run_digest       = SHA256("freeagent.composite-child-run-id/v1\0" + seed)
member_digest    = SHA256("freeagent.composite-child-member-id/v1\0" + seed)
recovery_digest  = SHA256("freeagent.composite-child-recovery-root/v1\0" + seed)
```

Parent 的有序 Child refs 因此可在 Parent digest 前完整冻结；Child manifest 随后才反向引用
ParentManifestDigest。任何让 Child identity 依赖 ParentManifestDigest 的公式都会与 Parent ref
形成 digest cycle，必须拒绝。W5 Decision plan 还必须按相同 logical slot 顺序预冻结等量
repair Children、独立 repair physical ParentSlotID 与 Reviewer1；所有初始/repair/reviewer
Run identity 必须互异，并在一次 Admission 中全成或全败。

历史 S2 第一切片的 Composite 只允许：恰好一个 `model.generate/v2` Binding、声明式静态
`context.provide/v1`，以及 §7.5 已验收的本地只读 RAG
`knowledge-context-binding/v1`。任何 family member 选择 Memory、Action、MCP、Channel
或其他 Effect/外部 Port 时，整个 Admission 必须失败；不得仅降级该成员。该 S2 第一
切片当时不存在跨 Workspace `TransferEnvelope`、Reviewer、Learning、动态 task graph
或公平队列；S3-A/S3-B 后续分别按 §16/§18 的窄合同扩展 Scheduler 与 Reviewer，W5-D1/X1
当前扩展则只由 §20 授权。该历史段落不得覆盖当前 W5 语义。

整个 family 必须在一次 Admission 中原子发布。Parent 初始 continuation 固定为
`WAITING_CHILDREN`，全部 Child 初始为 `READY`；Universal Loop 只能消费该冻结图，不能
创建、替换或补造 Child。第一切片结果策略固定为 `ALL_REQUIRED`，不是可配置投票：任何
Child 明确 `FAILED` 都使 Parent 明确失败；任一 Child 的 Model Attempt 为
`MODEL_UNKNOWN` 时 Parent 保持等待对账；只有全部 Child 都有匹配冻结 manifest 的成功
终态结果时 Parent 才能开始唯一 merge 模型调用。

生产 `CompositeChatService` 可以在有界并发上限内用各 Child RunID 调用同一个
Universal Loop，再继续 Parent；结果无论实际完成先后，都必须按 Parent manifest 的冻结
顺序归位。这只是驱动已经 Admission 的 Run，不是 Scheduler、queue 或新 authority，
也不能改变冻结图或 merge 规范顺序。

#### 7.2.3 Family dispatch cap、Usage 与取消

`Composite.Plan.FamilyModelDispatchLimit` 是 family 唯一共享 dispatch 上限字段，必须精确等于
W5 Decision family 的 `2*len(Composite.Plan.Children) + 3`：初始 Children、Reviewer0、
repair Children、Reviewer1 与 Root merge 各预留一个物理 slot。未启用 Decision 的历史
S2/S3 family 仍按其冻结 N+1/N+2 cap 恢复。它只是静态 dispatch 数量上限，不是 token 池
或通用资源池。每个 Run 继续使用既有
独立 Usage Ledger；family token 只能从这些 Run 已提交的同一 `model_usage` 权威事实
按 Parent manifest 闭包派生查询投影，不能另记账或估算。PENDING 后转为
`MODEL_UNKNOWN` 的任一 dispatch 已消耗其唯一 family slot，永不退款、替换或重放；未激活或
skipped repair Run 的 `Attempt=nil`，不得伪造为已用 slot 或零 Usage。

Composite 根 intent 的 `CancellationScope` 必须为 `family`，Child 固定为
`inherited`；普通非 Composite Run 固定为 `run`。唯一请求 wire 是
`RunCancellationRequestV1` / `cancel-request/v1`，且仅含
`schema_version,root_run_id,root_manifest_digest,scope,reason_code`。外部请求 scope
只允许 `run|family`；`inherited` 只存在于 Child Manifest，不能直接请求。reason 只允许
`USER_REQUEST|OPERATOR_REQUEST|POLICY_ENFORCED|SHUTDOWN`。

唯一 Store 入口 `RequestRunCancellation` 只原子安装 first-write-wins 的不可逆 latch，
不推进 Run/Frame revision、不写终态，也不覆盖 PENDING/UNKNOWN。Family latch 只从
Parent 向冻结 Child 图传播，并在 Parent/Child 每一次新 Model/Host/Gateway permit 或
invocation 之前重验；Loop 后续观察 latch 并返回稳定取消 disposition。它不向上或横向
传播，也不创建新 Run，绝不能把已经存在的 Model `PENDING`/`MODEL_UNKNOWN` 或其他
`UNKNOWN` 改写成“未执行”、退款或自动重试。

### 7.3 S2.1 Context Compiler

Context Compiler 是 Universal Loop 在模型调用前使用的一个无副作用纯编译器，不是
第二个 Assembly Compiler。它只能消费 `LoadRunForLoop` 已恢复并校验的冻结
RunManifest、MemberExecutionSnapshot、Policy、PortPlan、ContentRecord 与 History；
不得读取当前 Control/Catalog、远程仓库、系统时钟或随机数，也不得调用模型生成
摘要。相同冻结输入必须产生字节一致的消息与单一 compilation 记录。

Composite Child 还必须从冻结 `CompositeAssignmentV1` 构造一条 Core-owned、受保护的
SYSTEM 消息，固定前缀为 `COMPOSITE_SPECIALIST_ASSIGNMENT_JSON:`，envelope 只含
`schema_version=composite-specialist-assignment-context/v1`、`slot_id` 与 `focus_id`。
该消息不能被 Provider/Child 正文覆盖，不参与 Summary/Drop，也不新增领域知识。同 Workspace
Child 随后仍原样使用 family 共同的 `TASK_INPUT` 作为当前 Task USER；跨 Workspace Child
只能使用 trusted compiler 从 Store-loaded TASK_INPUT（repair 时再加 exact repair basis）生成并
经双边 grant 验证的 `TASK_SUMMARY`，不得把完整任务 History/Memory/Knowledge 正文跨边界注入。

Composite Root merge 时，Context Compiler 必须恢复 Root manifest 中全部有序 Child ref 及其
exact terminal result；跨 Workspace result 还必须恢复并验证对应 RESULT envelope，闭合真实
terminal `MODEL_RESULT → specialist-contribution/v1`。每个结果以固定前缀
`UNTRUSTED_COMPOSITE_CHILD_RESULT_JSON:` 加 canonical USER envelope 进入模型，envelope
只含 `schema_version=composite-child-result-context/v1`、`slot_id`、`focus_id` 与 Child
result；其中任何文本都只能作为不可信数据，不能提升为指令、Context Provider 或权限。
Run/Manifest/Member/Attempt 等动态身份仅进入 compilation evidence，不进入 prompt。

Child result 总池固定为
`floor(input_budget_tokens * 5000 / 10000)`。先按每个 slot 的
`floor(pool * WeightBasisPoints / 10000)` 分配，再将因取整剩余的 token 按
`WeightBasisPoints` 降序、同权重 `SlotID` UTF-8 字节升序各补 1，直到分完；结果 envelope
也按同一顺序写入。分配是每个完整 envelope 的硬上限，不允许截断、摘要或借用其他
slot 配额；任一完整结果不能容纳时，Parent 在模型 permit 前以
`COMPOSITE_CHILD_RESULT_OVER_BUDGET` 明确失败。未用额度不转给其他 Child，且全部 Child
结果连同其他受保护内容仍须通过最终 request 的 100% 总预算检查。

`ALL_REQUIRED_CHILD_FAILED` 与 `COMPOSITE_CHILD_RESULT_OVER_BUDGET` 都必须在模型 permit
前通过 `CommitCoreDeterministicFailure` 的 attempt-free Core 终态事务提交。事件类型为
`CoreDeterministicFailureEventV1`，wire 为
`core-deterministic-failure-event/v1 {schema_version,run_id,reason}`，event kind 固定
`CORE_DETERMINISTIC_FAILURE`。这两个 reason 是当前唯一 allowlist；不得伪造
Model/Dispatch Attempt、Usage、History 或输出。

`context.provide/v1` 的 `ConfigRef` 从本切片起必须指向消费方拥有的 canonical
`context-binding-config/v1`：

```go
type ContextBindingConfigV1 struct {
    SchemaVersion string
    Placement     ContextPlacement // TRUSTED_INSTRUCTION | UNTRUSTED_DATA
    AllowSummary  bool
    AllowDrop     bool
    Parameters    json.RawMessage  // 有界 canonical object；Provider 不能改写上列字段
}
```

模块包只能声明能力请求，不能通过 Manifest、输出正文或 `Parameters` 自授 SYSTEM
位置、摘要权或 Drop 权。上述值来自消费方配置与本地策略，并由 Assembly Compiler
冻结。首片的 sensitivity 固定为冻结 Workspace scope，并继续受
AuthorityCeilingRef 限制，不增加 Trust/Sensitivity 表或授权对象族。
S2.1 的 `StaticContextRefs` 全部属于受保护装配上下文，因此 `AllowSummary` 和
`AllowDrop` 必须同时为 `false`；bootstrap 在发布前拒绝任一 `true`。字段暂留在
通用配置 wire 中不表示本版本已开放静态压缩或删除，后续能力必须用真实消费者和
精确协议版本启用。
`TRUSTED_INSTRUCTION` 可以进入 SYSTEM；`UNTRUSTED_DATA` 只能作为“固定前缀 +
canonical JSON envelope”的 USER 数据进入请求，不使用可被正文闭合的成对文本标记，
任何正文都不能把它提升为系统指令。

成员的 `ContextPolicy` 必须恢复为 `CONTEXT` PolicyDocument，body 使用 canonical
`context-policy/v1`：

```go
type ContextPolicyV1 struct {
    SchemaVersion        string
    ContextWindowTokens  uint64
    ReservedOutputTokens uint64
    RecentHistoryTurns   uint64
    EstimatorVersion     string
}
```

`ContextWindowTokens` 是消费方配置的冻结上限，且
`ReservedOutputTokens < ContextWindowTokens`。成员绑定可选 ModelProfile 时，有效
上限只能取两者最小值，不得扩大原策略，也不得在执行时读取模型“最新”元数据。
所有进入 canonical JSON 的 token 整数不得超过 IEEE-754/JCS 安全整数
`2^53-1`，以保证构造、摘要和恢复得到同一个值。
S2.1 固定使用版本化
`canonical-json-utf8-byte-upper-bound/v1` 保守估算器、
`head-tail-extractive/v1` 摘要器、8500 basis points 压缩水位和 10000 basis
points 满载水位；策略必须精确选择受支持的 estimator，版本和值写入编译记录，
不使用浮点比较。

Core 在构造请求单元前严格验证来源模块/Artifact、ContentRef、trust、Workspace
scope、冻结顺序与策略。验证通过后，S2.1 内部只保留真实参与计算的最小
`contextUnit{message, retention identity, retention eligibility}`；来源证据继续由
PortPlan、ContentRecord 和 compilation 记录闭合，不复制成第二套“ContextBlock
事实”。S2.1 基线消费者只包括静态 Context、已提交 History 与当前 Task；W1
Conversation 的已提交 History 按本节下述完整 USER+ASSISTANT turn 恢复。S2 RAG 按
§7.5 增加动态检索材料，S2 Memory 按 §7.6 增加可选的 Agent 轻量记忆
材料。S2 Action 仍复用同一个 Compiler 和同一估算/压缩/Drop 算法，只增加一个由
冻结 MaxResultBytes 可重算的 `reserved_action_result_tokens` 输入；模型二从已持久化
模型一请求精确追加 ACTION_RESULT envelope，不重新运行 Compiler。无 Action 时该
reserve 为零且 canonical compilation/request 保持不变。后续工作包只有在出现
真实消费者时才能按精确协议版本增加 token 估算、读取 Receipt 或其他 ContextBlock
元数据。

Manifest 含 `ConversationTurn` 时，Store 必须从其 `PredecessorRunID` 沿不可变 predecessor
链向前恢复，并在交给 Compiler 前证明 conversation ID、turn index、固定五元组和成功
终态连续闭合。每个 predecessor Run 恰好贡献一个完整 turn：USER 来自该 RunManifest 的
`TASK_INPUT` ContentRecord，ASSISTANT 来自该 Run 最终成功 Model Attempt 唯一对应的
`history_entries` 记录；按最旧到最新顺序进入请求。不得把 predecessor 的 History 行复制到
当前 Run，也不得另建 conversation history/summary 事实源。当前 Run 的
`history_entries` 仍只记录本轮最终 ASSISTANT，当前 USER 仍只由本轮 `TASK_INPUT` 表达。
任一 predecessor 不是成功终态、缺 USER/ASSISTANT、来源 Attempt 不闭合、turn 断裂或五元组
漂移，都必须在新 Model Attempt/permit 前 fail-closed。

Conversation 的持久摘要继续只使用本 Run 的唯一 `CONTEXT_COMPILATION` ContentRecord；
它不是可变 rolling-summary head。新 turn 对已验证 predecessor chain 最多执行一次同一
确定性摘要算法；`BeginModelDispatch` 成功后，PENDING、MODEL_UNKNOWN、重入、进程恢复和
backup/restore 必须逐字节复用冻结 compilation/request，不得重走 predecessor chain、重算
摘要或改用后来推进的 Conversation head。

Composite assignment 与 Parent merge Child results 属于该切片新增的受保护输入；它们
不建立第二个 Context Compiler。保护优先级为“系统/安全约束、Composite assignment、
当前任务/未完成动作、Child results、持久化摘要、近期 History、可选上下文”。为维持
缓存稳定前缀，实际 model request 的物理顺序固定为：

1. 必要时由 Core 添加的固定防注入 SYSTEM 约束；
2. 仅 Child 存在的受保护 Composite assignment SYSTEM；
3. 按 PortPlan/Binding/StaticContextRefs 冻结顺序排列的 Context 块：可信内容为
   SYSTEM，非可信内容为带固定前缀和 canonical JSON envelope 的 USER 数据；
4. 持久化摘要与保留的 History；Conversation 中每个旧 turn 始终按完整
   USER+ASSISTANT pair 相邻排列；
5. 仅 Parent merge 存在的有序 Child result USER envelopes；
6. 当前 Task USER；模型一到此结束；
7. 仅模型二追加固定前缀的 `UNTRUSTED_ACTION_RESULT_JSON:` USER envelope，且它是
   最后一条消息。

为避免重排冻结 Binding，`context.provide/v1` 的 placement 必须单调：全部
TRUSTED_INSTRUCTION 位于全部 UNTRUSTED_DATA 之前；反向交错时 fail-closed。近期
窗口由 `RecentHistoryTurns` 冻结，并按完整 Task/turn 起点对齐；Conversation 中它按
完整 USER+ASSISTANT pair 计数，不得只保护 Assistant。没有 Context
PortPlan 时不创建空块；未发生动态读取且低于 85% 时不创建编译记录，Pure Chat 仍
保持轻量。发生 §7.5、§7.6 或 Composite assignment/Parent merge 动态输入时，无论水位
如何都必须记录唯一 compilation。

`input_budget_tokens = context_window_tokens - reserved_output_tokens`，恢复水位为
`floor(input_budget_tokens * 8500 / 10000)`，满载水位为
`input_budget_tokens`。估算覆盖最终 canonical request；所有加法、乘法和比较都做
溢出检查。

- `estimate < restore_watermark`：不压缩、不 Drop、不写编译记录。
- `restore_watermark <= estimate < input_budget`（即 `>=85%` 且 `<100%`）：只触发一次
  确定性压缩。Compiler 选择最旧、连续、允许摘要且不属于近期窗口的完整 History turn
  范围；Conversation 中每个范围边界必须覆盖完整 USER+ASSISTANT pair，用一个
  head-tail extractive summary 替换；优先选择可回到恢复水位的最小范围，否则只
  接受能严格降低估算的最大范围。持久化后重新估算，不反复调用压缩器。
- 初始 `estimate >= input_budget`：直接进入 Drop，不先生成摘要。
- Drop 每批只移除当前最旧的一个允许删除的完整 Task/turn 单元；Conversation 中必须
  整轮移除 USER+ASSISTANT pair，随后立即重算；
  同时达到“请求可容纳且不高于恢复水位”时停止。因此结果是最小的有序旧前缀，
  不是固定比例或无差别清空。

SYSTEM、安全约束、Composite assignment、完整 Child result envelope、当前 Task、未完成
动作、冻结权限/装配证据、当前动态上下文证据与策略
规定的近期窗口不得 Drop。Drop 只改变本次请求视图，不删除 Store 中的原始
History。允许删除的旧单元全部耗尽仍不能达到恢复目标时，在调用模型前
fail-closed，不截断受保护内容。

达到阈值后，发生 §7.5/§7.6/Composite 动态输入，或存在 Action result reservation 时，Compiler
有且仅生成一个 canonical
`context-compilation/v1`；即使
没有合格的旧 History 可摘要，也必须记录
`NO_ELIGIBLE_SUMMARY_UNDER_BUDGET`，不能退回无记录轻路径。记录中同时
保存可选摘要、摘要源连续范围、逐批 Drop 单元身份/digest、Workspace scope、算法
版本、ContextPolicy ref、转换前后估算、目标水位、停止原因、最终 request digest，
以及可选 `action_result_reservation{max_envelope_bytes,estimated_tokens}` 与
`composite.child_results`。后者必须逐项保存 slot、Child RunID、Child ManifestDigest、
MemberSnapshotDigest、唯一 content-addressed terminal `ResultRef`、稳定的 terminal
Run revision、focus、weight、allocated tokens、estimated tokens，以及
`OriginalBytes == RetainedBytes && Truncated == false` 的完整-envelope 证明，并证明列表
顺序与 Parent manifest 一致。`ResultRef` 本身就是结果 digest，不复制第二个
`ResultDigest`；lease acquire/release 的 Frame revision 不进入证据。该 reservation
只由冻结 Actions 重算，不复制 Action 定义或创建第二策略对象。
Run、Member、logical step 与 MemberSnapshotDigest 已由引用它的唯一
ModelDispatchAttempt 绑定，不在内容记录中复制动态身份。它使用单一
`CONTEXT_COMPILATION` ContentRecord，不再拆成 Summary、Checkpoint 或 Drop 对象族。
该记录必须与 MODEL_REQUEST 和 PENDING Attempt 在同一个 `BeginModelDispatch` 事务
提交；事务失败不得调用 Provider。PENDING、MODEL_UNKNOWN 或精确重入只读取原
request/compilation，禁止重新编译或语义重放。

S2.1 的生产 Universal Loop 仍是“单次模型步骤后终结”的 Pure Chat 纵链，因此真实
生产入口可达低水位、无可摘要记录和受保护内容超限门禁；带旧 History 的成功摘要与
Drop 由同一纯 Compiler 和合成恢复闭包测试证明。本切片不据此宣称生产多轮会话已经
闭环，也不为测试新增第二个 continuation 或会话 Runtime。

### 7.4 ModelProfile

`ModelProfile` 是可选的、内容寻址的模型评测画像。它复用现有
`CONFIG/application-json` ContentRecord，不新增 ContentKind、数据库表、Port、Provider
或路由器：

```go
type ModelProfileRef struct {
    ID      string
    Version string
    Digest  string
}

type ModelTendencyV1 struct {
    MetricID         string
    ScoreBasisPoints uint16 // 0..10000
}

type ModelProfileV1 struct {
    SchemaVersion          string // model-profile/v1
    ID, Version            string
    Provider, Model        string
    ModelBuildID           string
    ModelConfigRef         string
    AdapterArtifactDigest  string
    AdapterIdentity        string
    ContextWindowTokens    uint64
    EvaluationSuite        string
    EvaluationVersion      string
    EvaluationResultDigest string
    CapabilityTendencies   []ModelTendencyV1
    ReliabilityTendencies  []ModelTendencyV1
}
```

规则：

- `ModelProfileRef.Digest` 必须等于该画像作为 `CONFIG/application-json` 的
  ContentDigest；恢复只接受精确 canonical bytes、已知字段和匹配的 ID/version/digest。
- 每个 tendency 分类内按 `MetricID` 的 UTF-8 字节序规范排序；同一分类内重复
  metric、越界分数和非规范文本均拒绝，空数组规范化为 `[]`。跨 capability 与
  reliability 的同名 metric 允许存在，完整键为 `(category, metric_id)`，消费者不得
  将两类无标签扁平合并。
- `ContextWindowTokens` 必须是 `1..2^53-1`；整个画像 canonical CONFIG 不得超过
  64 KiB，超限结果在返回引用、持久化或发布前失败关闭；Restore 对超限输入在
  canonicalize 前拒绝。
- Profile 必须逐字段匹配唯一冻结模型 Binding 的 provider、model、model build ID、
  ConfigRef、Adapter ArtifactDigest 与 AdapterIdentity。任一不一致必须在发布、
  Admission 或模型 Attempt/permit 之前失败关闭。
- 唯一当前运行效果是
  `effective_context_window = min(ContextPolicy.ContextWindowTokens,
  ModelProfile.ContextWindowTokens)`；其他 ContextPolicy 字段不变。如果收紧后不能容纳
  reserved output，发布和恢复均拒绝。较大的画像上限不得扩大策略。
- capability/reliability tendency 与评测摘要只记录可复现选择依据；本切片不根据分数
  自动选模、改写 prompt、增加预算或授予权限。`EvaluationResultDigest` 锁定外部评测
  结果身份；评测执行器与 evidence 导入在出现真实消费者后再接入，不成为当前恢复
  所需的第二事实源。
- Assembly Compiler 只把 Control Profile 中显式选择的精确引用冻结到成员快照；
  Runtime、Store 和恢复不得查询“最新画像”或据此选择另一 Binding。
- 没有 ModelProfile 的成员继续合法，canonical wire 不出现该字段，且 Context/Store
  路径不得进行画像 CONFIG 读取；Pure Chat 仍只使用显式 ContextPolicy。

ModelProfile 通过既有 MemberSnapshotDigest、RunManifest 内容闭包和完整数据库备份
恢复。它不改变 Agent、Workspace、Profile、模型模块之间的自由组合；新 Run 可以
重新选择不同精确画像，已发布 Run 不能漂移。

### 7.5 S2 RAG：`context.provide/v1` 动态上下文

这是首次公开发布前对 `context.provide/v1` 的首个动态只读协议，不是第二套 Port 或
兼容链。同一有序 PortPlan 中只允许两种 Binding 形状；动态 Binding 的具体协议只能
由冻结 `ContextBindingConfigV1.Parameters.schema_version` 精确分派，禁止按 ModuleID、
品牌、数组位置或运行时猜测：

```text
DECLARATIVE        + StaticContextRefs[1..N] = 静态上下文，零 Invoke
TRUSTED_IN_PROCESS + StaticContextRefs=[]    = 动态只读 Context，按协议精确 Invoke 一次
```

RAG 动态 Binding 的 `context-binding-config/v1` 必须为 `UNTRUSTED_DATA`、不可
Summary、不可 Drop，并且 `Parameters` 严格恢复为：

```go
type KnowledgeSourceRefV1 struct {
    ID      string
    Version string
    Digest  string
}

type KnowledgeContextBindingV1 struct {
    SchemaVersion     string // knowledge-context-binding/v1
    Source            KnowledgeSourceRefV1
    MaxHits           uint32
    MaxTotalTextBytes uint32
}
```

`MaxHits` 和 `MaxTotalTextBytes` 必须为正且不超过 Core 常量上限。Config 只能请求，
不能授权。`AuthorityCeilingRef` 必须严格恢复为 Core 所有的
`knowledge-authority-ceiling/v1`，绑定同一精确 source、允许的 scope rules、
`MaxHits` 和 `MaxTotalTextBytes`；有效上限取 Config 与 Ceiling 的逐项最小值。

```go
type KnowledgeObjectRefV1 struct {
    ID      string
    Version string
    Digest  string
}

type KnowledgeQueryScopeV1 struct {
    TenantID     string
    Workspace    KnowledgeObjectRefV1
    Agent        KnowledgeObjectRefV1
    TaskInputRef string
}

type KnowledgeScopeRuleV1 struct {
    TenantID     string
    WorkspaceID  string // exact or "*"
    AgentID      string // exact or "*"
    TaskInputRef string // exact digest or "*"
}

type KnowledgeAuthorityCeilingV1 struct {
    SchemaVersion     string // knowledge-authority-ceiling/v1
    Source            KnowledgeSourceRefV1
    AllowedScopes     []KnowledgeScopeRuleV1
    MaxHits           uint32
    MaxTotalTextBytes uint32
}
```

Core 只能从冻结 RunManifest 和 MemberSnapshot 构造 exact `KnowledgeQueryScopeV1`：
TenantID、WorkspaceRef、AgentRef 与 TaskInputRef 缺一不可。任务原文是本切片唯一 query；
不进行 query rewrite、标签路由或运行时扩大。Rule 必须规范排序、去重，TenantID 必须
exact；`*` 只能出现在另外三级。当前 exact scope 必须先被 Ceiling 允许，再交给
Provider；Task、Config、query 与 Provider 输出都只能收窄，不能扩权。

Module Host 输入输出严格为：

```go
type KnowledgeContextRequestV1 struct {
    SchemaVersion     string // knowledge-context-request/v1
    Source            KnowledgeSourceRefV1
    Scope             KnowledgeQueryScopeV1
    QueryText         string
    MaxHits           uint32
    MaxTotalTextBytes uint32
}

type KnowledgeDocumentRefV1 struct {
    ID      string
    Version string
    Digest  string
}

type KnowledgeHitV1 struct {
    Rank        uint32
    Document    KnowledgeDocumentRefV1
    ChunkID     string
    ChunkDigest string
    Text        string
    VisibleTo   []KnowledgeScopeRuleV1
}

type KnowledgeContextOutputV1 struct {
    SchemaVersion string // knowledge-context-output/v1
    RequestDigest string
    Source        KnowledgeSourceRefV1
    Hits          []KnowledgeHitV1
}
```

Rank 从 1 连续递增且为语义顺序，不保存浮点分数；同一输出不得重复 chunk。ChunkDigest
覆盖 Document、ChunkID、canonical 正文和规范化 VisibleTo，不覆盖 Rank。Core 必须
重新验证 request/source、每个 digest、当前 scope 可见性、条数和总字节；未知字段、
非 canonical 输出、越权、错序或摘要不闭合全部失败关闭。合法零命中是成功。

首片动态 Binding 必须是 `REQUIRED`，Provider 只能是确定性、只读、无网络、无 Secret、
无计费和无副作用的 `TRUSTED_IN_PROCESS` 实现；失败或 UNKNOWN 在模型 Attempt/permit
前终止，不能 fallback。远程/收费检索必须等待真实 Retrieval Attempt/Usage 合同。

Universal Loop 在同一 lease 内，按冻结 BindingIndex 用私有一次性只读 Gate 调用现有
Module Host；Host 仍只按 ArtifactDigest + AdapterIdentity 解析精确实现。严格验证后的
Hit 作为受保护 Context 单元交给同一个纯 Compiler，并固定包装为 USER：

```text
UNTRUSTED_CONTEXT_DATA_JSON:
<canonical JSON containing source/document/chunk/text only>
```

正文不得包含 RunID、AttemptID、InvocationID、BindingIndex、时间戳或 Provider 路由
元数据。所有动态 Binding 继续遵循 PortPlan 顺序；Runtime 只识别公开 wire contract，
不搜索、重排或切换 Provider。

`context-compilation/v1` 增加按 BindingIndex 有序的
`knowledge_retrievals,omitempty`。每项至少闭合 BindingIndex、ConfigRef、
AuthorityCeilingRef、request digest、exact scope、source、hits 与 output digest。
无 RAG 时字段省略，已有 canonical bytes 不变；发生 RAG 时即使最终请求低于 85%，
也以 `RETRIEVAL_EVIDENCE_BELOW_WATERMARK` 生成记录。检索材料是当前受保护证据，不参与
普通 Summary/Drop；100% 规则仍只逐个移除最旧 eligible History，仍超限则模型前失败。

知识 source 和 chunk 正文只存在于内容寻址模块 artifact；不复制到 Agent、Workspace、
MemberSnapshot 或专用表。未来向量索引只能是从 source revision/chunk digests 重建的
派生缓存，不能成为正文、ACL 或恢复事实源。

RAG 是否启用只由用户选择的精确 Profile 是否冻结动态 Binding 决定，不增加自动路由
或临时开关。默认 Profile 无动态 Binding，因而不恢复 RAG Config/Authority/artifact、
不枚举 Registry、无 RAG Host 调用。要求 RAG 的 Profile 缺能力时返回
`CAPABILITY_NOT_AVAILABLE`，不能静默 Pure Chat。

### 7.6 S2 Memory：Agent 轻量记忆

Memory 继续复用同一个 `context.provide/v1`、Module Host、Context Compiler、Universal
Loop 和 Current Store，不新增 Memory Port、Runtime、Ledger、缓存或后台 worker。
Memory 动态 Binding 的 `Parameters.schema_version` 固定为
`memory-context-binding/v1`；与 Knowledge 同时存在时严格按原 BindingIndex 执行，
`knowledge_retrievals` 与 `memory_reads` 的 BindingIndex 并集必须唯一且闭合原 PortPlan。
首片每个成员最多装配一个 Memory Binding。

Memory Binding 必须是 `REQUIRED + TRUSTED_IN_PROCESS + StaticContextRefs=[]`；它的
`context-binding-config/v1` 固定为 `UNTRUSTED_DATA`、`AllowSummary=false`、
`AllowDrop=false`。Provider 只允许本地、确定性、只读、无网络、无 Secret、无计费、
无副作用实现。失败、UNKNOWN、缺少 genesis、撤权、状态漂移或非法输出必须在模型
Attempt/permit 前 fail-closed；合法零选择是成功。

权威状态为 Current Store 中内容寻址的 `memory-snapshot/v1` 与 append-only revision，
归属 `TenantID + stable AgentID`。Snapshot 严格有界，只含五类 entry：

```text
FACT | PREFERENCE | TASK_SUMMARY | CATEGORY_COUNT | REPEATED_TERM_COUNT
```

每项携带稳定 key、Workspace visibility、来源 refs 及内容 digest。FACT/PREFERENCE
首片只接受 seed 或显式用户/Operator 确认；TASK_SUMMARY、分类计数与重复词计数只由
Core 的版本化确定性脚本从已提交 Task/Result 生成，并默认 `WORKSPACE_ONLY`。自动条目
必须带算法版本、算法配置摘要、来源 Task/Result 和 TTL；0 计数不保存，溢出拒绝。
计数只是检索/提示信号，不是知识权威，不得据此跳过 RAG、修改 PortPlan、模型权重或
权限。领域正文仍只归共享 RAG，Memory 不复制领域知识。

最小公开 wire 为：

```go
type MemoryContextBindingV1 struct {
    SchemaVersion       string // memory-context-binding/v1
    Kinds               []MemoryEntryKindV1
    MaxItems            uint32
    MaxTotalTextBytes   uint32
    CategoryRules       []MemoryCategoryRuleV1
    StopTerms           []string
    SummaryMaxTextBytes uint32 // TASK_SUMMARY 时至少 4
    EntryTTLSeconds     uint64
}

type MemoryAuthorityCeilingV1 struct {
    SchemaVersion       string // memory-authority-ceiling/v1
    TenantID            string
    AgentID             string
    AllowedWorkspaceIDs []string // exact IDs or the sole "*"
    AllowedKinds        []MemoryEntryKindV1
    MaxItems            uint32
    MaxTotalTextBytes   uint32
}

type MemoryContextRequestV1 struct {
    SchemaVersion     string // memory-context-request/v1
    Snapshot          MemorySnapshotRefV1
    Scope             MemoryQueryScopeV1
    QueryText         string
    EvaluatedAtUnixMS uint64
    Candidates        []MemoryCandidateV1
    MaxItems          uint32
    MaxTotalTextBytes uint32
}

type MemoryContextOutputV1 struct {
    SchemaVersion         string // memory-context-output/v1
    RequestDigest         string
    Snapshot              MemorySnapshotRefV1
    SelectedEntryDigests  []string
}
```

Core 从冻结 RunManifest、MemberSnapshot 与 TASK_INPUT 构造 exact Tenant、Workspace、
Agent、Task scope；实际 AgentID/WorkspaceID 不得使用 `*`，该字符只作为权限或可见性
数组中的单独 wildcard。Core 先按 snapshot owner、entry visibility、TTL、Config 与 Authority
Ceiling 裁剪，再把 candidates 交给 Provider。TenantID 与 AgentID 必须 exact；模块、
query 与输出只能收窄。输出只能选择 request 中唯一 candidate digest，顺序即 rank，
条数和总正文上限必须闭合；未知字段、重复、越权、过期或非 canonical 数据均拒绝。

新模型步骤在调用 Provider 前读取当前 Memory revision，并生成 request/output；
`BeginModelDispatch` 的 `BEGIN IMMEDIATE` 必须再次证明该 revision 仍是 current，严格
恢复 snapshot、Config、Authority 与输出，并把 `memory_reads,omitempty`、最终
MODEL_REQUEST 和 PENDING Attempt 原子提交。Memory 状态不加入 PortBinding，也不触发
Control/Catalog 重发。Begin 前崩溃可重新读取；Begin 成功后，PENDING、UNKNOWN、
终态重入、启动恢复和 backup/restore 只能读取原 compilation 指向的不可变 snapshot、
request 与 output，禁止再读 current revision、重新筛 TTL 或调用 Memory Provider。

Memory 投影使用相同固定 `UNTRUSTED_CONTEXT_DATA_JSON:` USER envelope，只包含选中
entry 的 kind/key/text 或稳定 `count_band`；精确 count 只留在 evidence。Tenant/Agent/
Workspace、revision、时间、Run、Attempt、
Invocation、BindingIndex 和路由元数据只留在证据，不进入 prompt。Memory 单元与 RAG
单元都是受保护上下文，不参与普通 Summary/Drop；100% 时仍只按序逐个删除最旧
eligible History，耗尽后仍超限则调用前失败。

首次 `SUCCEEDED` 终态在同一 SQLite 事务内，以最新 Agent revision 为 parent，用 Core
确定性脚本合并本次 Workspace-local TASK_SUMMARY、CATEGORY_COUNT 与
REPEATED_TERM_COUNT，追加一个新 snapshot/revision，并与 Result、Usage、History、
Frame、Event 原子提交。FAILED 与 MODEL_UNKNOWN 不更新；精确终态重入不得重复计数。
同一 Agent 的并发成功事务按 Store writer 顺序在最新 head 上合并，不能用请求时旧
snapshot 覆盖新状态。首片不增加补偿任务；终态事务任一步失败则整体回滚并保留原
Attempt 恢复语义。

分类与重复词计数只统计 canonical `TASK_INPUT`，不统计模型 `RESULT`；后者只参与有界
任务摘要。算法或 Binding 配置摘要改变时，同 key 的计数从本次 Task 新开序列，禁止把
不同语义的旧 count 合并后再改写配置摘要。

默认 Profile 不装配 Memory，因此不得查询 Memory revision、恢复 Memory Config/
Authority/artifact、枚举对应 Registry、调用 Host 或执行更新脚本。Memory-enabled
Profile 才承担一次本地读取/选择、相应 prompt token 和成功终态的一次有界 revision
追加；这不改变 Agent/Workspace 独立组合、模块可替换性、单 Store、单 Loop或共享 RAG
的事实归属。

### 7.7 S2 Action：公开 Describe/Prepare 与 Core 冻结定义

首版只新增精确 Port `action.provider/v1`。同一个 typed Provider 同时实现
`Describe` 与 `Prepare`；SDK 不发布 Execute Port：

```go
type EffectClass string

const (
    EffectNone              EffectClass = "none"
    EffectReadOnly          EffectClass = "read_only"
    EffectReversibleWrite   EffectClass = "reversible_write"
    EffectIrreversibleWrite EffectClass = "irreversible_write"
)

type ActionDescribeRequestV1 struct {
    SchemaVersion string          // action-describe-request/v1
    Parameters    json.RawMessage // canonical object；不含路由、Run 或权限凭据
}

type ActionDefinitionV1 struct {
    ProviderActionID        string
    Description             string
    InputSchema             json.RawMessage
    RequestedEffectClass    EffectClass
    RequestedMaxResultBytes uint32
}

type ActionRequestV1 struct {
    SchemaVersion    string // action-request/v1
    PublicActionID   string
    ProviderActionID string
    DefinitionDigest string
    CanonicalInput   json.RawMessage
}

type ActionProviderV1 interface {
    Describe(context.Context, ActionDescribeRequestV1) ([]ActionDefinitionV1, error)
    Prepare(context.Context, ActionRequestV1) (json.RawMessage, error)
}
```

消费方 `action-binding-config/v1` 冻结有序的
`{PublicActionID, ProviderActionID, LocalEffectClass, MaxResultBytes}` 与传给 Provider
的 canonical `Parameters`。PublicActionID 是 Agent/Workspace 组合时选择的稳定别名，
模型只看该别名；ProviderActionID 是 Describe/Prepare/executor 的真实标识。同一成员
内 PublicActionID 必须唯一，但不同 Binding 可以安全映射两个同名 Provider Action，
解析后仍禁止 fallback。模块不能写入本地别名、分类或上限。
`action-authority-ceiling/v1` 由 Core 所有，至少冻结 exact Tenant、允许的 Workspace、
允许的 ProviderActionID、`MaxEffectClass` 与 `MaxResultBytes`。Provider 声明只是请求；
Core 取 Provider 请求与本地分类中风险更高的一档，并取 Provider 请求、Binding 配置、
Authority 和 Core 16 KiB 硬上限中的最小结果上限。缺少映射/分类/上限、Action 不在
Ceiling、Workspace 不符或 Effect 越界均在发布 Run 前 fail-closed。

首版固定 `MaxActionsPerMemberV1=32`、`MaxActionDescriptionBytesV1=1024`、
`MaxActionSchemaBytesV1=16384`、成员全部 schema+description 合计 256 KiB，以及 schema
深度 8、单 object 64 个 properties、单 array 64 项和 1024 节点。`InputSchema` 是
canonical JSON Schema object 的有界可执行子集，而不是完整 JSON Schema 编译器：
根必须是 `type=object`、`additionalProperties=false`；递归支持 object、array、string、
integer、number、boolean、scalar `enum`，以及 properties、required、items、
min/maxLength、min/maxItems、minimum/maximum 和有界 title/description。string 必须有
maxLength，array 必须有 maxItems，object 必须禁止额外字段。
`pattern`、`$ref`、动态引用、组合器和未知关键字一律拒绝，避免引入第二套 regex 或
schema 引擎。该子集覆盖常见 Built-in/MCP 参数，
但不把未实现的完整 JSON Schema 语义悄悄塞入 v1。

Admission 只在已选 Profile 确实装配 Action PortPlan 时，按 BindingIndex 调用一次
Describe。该调用发生在 Assembly Compiler 之前，由 `internal/modulehost` 的
`AdmissionActionMaterializer` 使用精确 ArtifactDigest + AdapterIdentity 和已加载
PublishedBasis。该私有 Gate 的输入只能是同一次 basis revision、BindingSpec index、
CatalogEntry、canonical Config 与 Authority；输出只能是该 Binding 的有序定义。
它复用唯一 `ExactAdapterRegistry.ResolveExact`，不得获得 Run/Attempt、执行 permit、
通用 Invocation、Store writer、Provider 枚举或 fallback 权限，也不得调用 Prepare/
executor。当前切片只允许本地 allowlist 的 `TRUSTED_IN_PROCESS` Built-in 通过该 Gate。
Compiler 仍只消费显式已物化输入，不访问 Provider 或 Store；Admission 提交事务重新
验证同一 basis revision，漂移则整笔回滚。Core 按消费方映射生成 PublicActionID，按
PublicActionID 排序并要求成员内唯一，然后冻结：

```go
type AdmissionActionMaterializeInputV1 struct {
    PublishedBasisRevision uint64
    BindingSpecIndex       uint32
    Binding                PortBinding
    ConfigCanonical        json.RawMessage
    AuthorityCanonical     json.RawMessage
}

type MaterializedActionBindingV1 struct {
    BindingIndex uint32
    Provider     ActivatedModuleRef
    Definitions  []ActionDefinitionV1
}
```

Gate 必须逐字节证明 Binding 的 ConfigRef/AuthorityCeilingRef 对应上述 canonical 内容，
并只返回同一 exact Provider 的结果；任何额外 Store/Registry 搜索、basis 混用或输出
Provider 漂移都 fail-closed。

```go
type FrozenActionDefinitionV1 struct {
    PublicActionID    string
    ProviderActionID  string
    BindingIndex      uint32
    Description       string
    InputSchema       json.RawMessage
    EffectClass       EffectClass
    MaxResultBytes    uint32
    DefinitionDigest  string
}
```

`DefinitionDigest = SHA256("freeagent.action-definition/v1\0" +
CanonicalJSON({public_action_id,provider_action_id,description,input_schema,effect_class,
max_result_bytes}))`。BindingIndex 和完整 Provider 身份由同一 MemberSnapshotDigest
覆盖；不再另设必须由外部模块伪造的 DefinitionRevision。Describe 后若 PublishedBasis
在 Admission 事务内已漂移，整次 Admission 回滚并重新开始；已提交 Admission 的重入
直接恢复原 Run，绝不再次 Describe。恢复、PENDING、UNKNOWN 与 backup/restore 同样
只读冻结映射，禁止重新 Describe 或搜索同名工具。

模型合同在首次公开发布前一次性增加两个可选字段：

```go
type ModelActionDefinitionV1 struct {
    ActionID    string // PublicActionID
    Description string
    InputSchema json.RawMessage
}

type ModelActionRequestV1 struct {
    ActionID       string
    CanonicalInput json.RawMessage
}

type ModelGenerateRequestV1 struct {
    SchemaVersion string
    Messages      []ModelMessageV1
    Parameters    json.RawMessage
    Actions       []ModelActionDefinitionV1 // optional, omitempty
}

type ModelGenerateOutputV1 struct {
    SchemaVersion     string
    AssistantText     string                // 与 ActionRequest 恰有一个非空
    ActionRequest     *ModelActionRequestV1 // optional, omitempty
    ProviderRequestID string                // optional
}
```

模型只看到有序 PublicActionID、Description 与 InputSchema，不看到 ProviderActionID、
Binding、Effect、Authority、路由或
执行凭据。无 Action 的请求必须完全省略 `actions`，其 canonical bytes 与本次修订前
保持一致。Core 严格验证模型输入符合冻结 schema，再按冻结 ActionID 定位唯一
Binding；近似名称、遍历 Provider 或运行时重排均禁止。

Prepare 只获得 none/read_only grant，可重复但不得写状态。返回值是有界 canonical
object，可生成 provider-specific prepared payload，不要求再次符合模型输入 schema；
Core 不把 payload 内任何字段解释成路由、Effect、Authority、Attempt 或 permit。首版
prepared payload 硬上限 64 KiB、深度 32。Core 重新规范化并构造：

```go
type ActionProposalV1 struct {
    SchemaVersion         string // action-proposal/v1
    MemberSnapshotDigest string
    PublicActionID        string
    ProviderActionID      string
    DefinitionDigest      string
    CanonicalInput        json.RawMessage
    PreparedPayload       json.RawMessage
}
```

整个 canonical Proposal 只使用既有 ACTION_PROPOSAL ContentDigest 作为身份；不再复制
CanonicalInputDigest、PreparedPayloadDigest 或 ProposalDigest。验证时从原始字段重算
同一个 ContentDigest。所有绑定字段由 Core 生成，Provider 不能注入
BindingIndex、Effect、快照、Attempt、permit 或 Gateway 路由。ActionID 一旦解析，
Prepare 或执行失败都不得切换到另一 Binding。

模型一的 Context Compiler 必须在任何模型调用或 Effect 前，对每个冻结 Action 分别按
其 PublicActionID、DefinitionDigest 与 MaxResultBytes 计算最坏 canonical
`UNTRUSTED_ACTION_RESULT_JSON:` envelope，再取 envelope 总字节/同一估算器 token 最大
值，而不是只比较 MaxResultBytes。该 optional reservation 进入同一个
`context-compilation/v1`；有 Action 时低水位也记录，Store 从快照重算验证，无 Action
时字段省略且 Pure Chat wire 不变。Compiler 把 reservation 计入 85% 压缩/100% Drop
判断；无法按既有顺序规则保留完整模型二窗口时，模型一不得发送。Gateway 与终态事务
再次校验实际 canonical result 不超过该 Action 的冻结上限；Effect 后绝不截断、压缩
或改写 Provider 结果。executor 已可靠报告 SUCCEEDED 但结果非 canonical/超限时，
Effect 事实仍提交 SUCCEEDED，同时写有界 Core `RESULT_REJECTED` ActionResult 并以明确
Run 失败终止，不调用模型二；只有执行本身无法确认时才写 UNKNOWN。

执行数据合同最小且严格。它们位于 `sdk/moduleapi`，使 Host Adapter 能实现相同 wire；
但只有 Gateway 能构造执行 request 并取得内部 executor：

```go
type ActionExecutionOutcomeV1 string // SUCCEEDED | FAILED | UNKNOWN

type ActionExecutionRequestV1 struct {
    SchemaVersion    string // action-execution-request/v1
    AttemptID        string
    PublicActionID   string
    ProviderActionID string
    DefinitionDigest string
    MaxResultBytes   uint32
    PreparedPayload  json.RawMessage
}

type ActionExecutionResultV1 struct {
    SchemaVersion       string // action-execution-result/v1
    AttemptID           string
    Outcome             ActionExecutionOutcomeV1
    CanonicalResult     json.RawMessage // SUCCEEDED only
    ProviderReceipt     json.RawMessage // optional canonical object
    ExternalOperationID string          // optional
    ErrorClassification string          // FAILED only
    UnknownReason       string          // UNKNOWN when no stronger ref exists
}

type ActionResultV1 struct { // Core-owned ACTION_RESULT content
    SchemaVersion       string // action-result/v1
    PublicActionID      string
    DefinitionDigest   string
    Status              string          // AVAILABLE | RESULT_REJECTED
    Result              json.RawMessage // AVAILABLE only
    ErrorClassification string          // RESULT_REJECTED only
}
```

InvocationID/AttemptID 和精确 Provider identity 仍由现有 Module Host wrapper 绑定并由
Core 逐项核对，不能由 payload 自报覆盖。SUCCEEDED 必须有 canonical JSON result，且
不得带 error/unknown；FAILED 必须有明确 error classification、不得带 result；UNKNOWN
不得带最终 result，且必须有 external operation、真实 receipt 或 unknown reason 至少
一项。receipt 仅在 Provider 确实提供时保存，本地确定性 Built-in 不伪造 receipt。
result 使用冻结 MaxResultBytes；receipt/prepared payload 分别受 64 KiB、深度 32 和
有界节点数限制。非法 outcome 组合、Attempt/Provider 错配、普通 error 或可能已开始
Effect 后无法证明终态，均保守为 UNKNOWN。只有 outcome/身份已可靠闭合的 SUCCEEDED
结果正文非 canonical/超限时使用 Core `RESULT_REJECTED`，不能把已确认 Effect 错记成
UNKNOWN。

成功时 Core 只从执行结果生成上述 `ActionResultV1`，其 ACTION_RESULT ContentDigest 是
唯一结果身份。模型二 envelope 恰为前缀 `UNTRUSTED_ACTION_RESULT_JSON:` 加 canonical
`{schema_version:"action-result-context/v1",public_action_id,definition_digest,result}`；
不包含 AttemptID、Provider、receipt、时间或路由。Store 必须从模型一 MODEL_REQUEST 与
原 ACTION_RESULT 精确重建并验证模型二 RequestDigest，不能接受调用方自组装的近似值。
只有 Status=AVAILABLE 能进入该 builder；RESULT_REJECTED 明确终止 Run。

首片每个 Run 最多执行一个 Action，真实循环为
`模型一 → Prepare → Action/Gateway → 模型二最终回答`。模型一返回 Action 时，Store
在同一事务中提交模型终态与 Action `DispatchAttempt=PENDING`，不存在模型已完成而
动作未记账的中间恢复状态；事务提交后 Gateway 才可执行。Action 成功结果作为固定
前缀的 canonical `UNTRUSTED_ACTION_RESULT_JSON:` USER 数据追加到原模型请求，Action
定义和原消息前缀保持不变，再进行模型二。模型二再请求 Action 时以
`ACTION_LIMIT_REACHED` 明确失败，不创建第二个 DispatchAttempt。Action 明确失败终止
Run；UNKNOWN 进入对账。Memory 只在模型二最终回答成功时更新，不因模型一的 Action
请求或动作结果扩大。

首个生产消费者是本地确定性的 `text.stats` Built-in，EffectClass 为 `none`，用于
证明真实 Describe/Prepare/Gateway/Loop 纵链；Gateway 的可逆、不可逆、崩溃后 UNKNOWN
语义由经过同一生产代码路径、写入隔离临时目录的故障注入 Adapter 验证。该选择不把
Action 限制为无副作用；后续 Built-in 或 MCP 仍使用同一四级 Effect 与 Gateway。

### 7.8 S2 MCP 首个开发切片：LOCAL_PROCESS + stdio + Tool-only

本节记录已经接入并验收的**窄开发切片**；它不是生产就绪声明，也不是完整 MCP
实现。MCP 不成为新 Port、Runtime、Store、Ledger 或控制面；它只是
`action.provider/v1` 的一个 Adapter：公开面仍是既有 `Describe/Prepare`，执行面仍是
Gateway 独占的私有 executor。MCP server、Go SDK 类型和 JSON-RPC 类型不得泄漏到
Assembly Compiler、Universal Loop、PortPlan、RunManifest 或 Store 公共合同。

#### 7.8.1 固定兼容基线与范围

- 首片协议版本精确固定为 MCP `2025-11-25`，Go 依赖精确固定为官方
  `github.com/modelcontextprotocol/go-sdk` `v1.6.0`。选择该组合是为了使用已经发布的
  稳定版，不跟随未冻结的 latest。
- MCP `2026-07-28`、官方 Go SDK `v1.7.x` 及其 `server/discover`/新生命周期语义全部
  延后；当前 Adapter 不发送 discover，也不接受协商到其他协议版本。升级必须另行修订
  本规格和测试证据，不能由依赖更新自动发生。
- 传输只有 `LOCAL_PROCESS + stdio`，能力只有 `tools/list` 和 `tools/call`。首片不支持
  Secret/SecretRef 注入、REMOTE、Streamable HTTP、SSE、Resource、Prompt、Roots、
  Sampling、Elicitation、Tasks、日志订阅、`listChanged` 动态刷新、常驻池或任何自动
  发现；server 发起的 request/notification 也不能扩大该 Tool-only 能力面。
- Adapter 不扫描 PATH、用户 MCP 配置、端口或运行中进程。Operator 必须显式 Install
  精确模块包并 Activate；未装配 MCP 的 Profile 不触发任何探测。

#### 7.8.2 Operator-trusted 与精确 artifact grant

本地子进程默认继承 FreeAgent 进程的操作系统身份；stdio 协议和官方 SDK **不是 OS
沙箱**。因此首片只能激活 Operator 显式批准且完全信任、精确 ArtifactDigest allowlist
中的模块，
不能把不可信第三方包标记为“已隔离”。未来真正的不可信本地模块必须先增加独立 OS
sandbox 工作包，本节不预先宣称文件系统或网络隔离，也不把当前切片标记为生产就绪。
Module 不得直写 Core DB 或绕过 Authority 的不变量保持不变；但当前切片依靠 Operator
完全信任而非 OS 强制隔离，不能用来证明恶意本地进程无法访问主机资源。

现有 `ConfigRef + AuthorityCeilingRef + ActivatedModuleRef` 必须共同冻结并校验：精确
ArtifactDigest、`ExecutionClass=LOCAL_PROCESS`、版本化 AdapterIdentity、artifact 内
规范相对 entrypoint、无 shell 的有序 argv、artifact 内 working directory、固定协议
版本、允许的精确 tool name、本地 Effect 分类、结果上限和各阶段 deadline。entrypoint
必须解析到已复核 ArtifactDigest 的 artifact 树内普通文件；禁止 PATH 查找、shell
字符串、symlink/hardlink 逃逸和工作目录逃逸。子进程只获得 Core 固定的最小非 Secret
环境；首片 Config、Authority、Manifest、seed 和进程环境均不得携带 Secret 或
SecretRef。模块声明、server instructions、tool description/annotations 只是未受信
输入，不能授予 Trust、Effect、权限或结果上限。

完整 artifact 树的摘要验证是**显式选择该 MCP Adapter 时的使用边界**：每次
`Describe` 和 `Execute` 都必须在启动子进程前以 Operator grant 中的精确
`ArtifactDigest` 重新验证整个有界 artifact；全局启动恢复、Pure Chat、已持久化
UNKNOWN 的重入与 backup/restore 不得为此加载 artifact 或构造 Adapter。

#### 7.8.3 Describe：Admission 时的有界会话

只有显式选择 MCP Action Binding 的 Admission 才能启动一次有界 Describe 会话：

1. Adapter 先按精确 artifact grant 验证完整有界 artifact，再启动一个子进程；
   stdin/stdout 使用 UTF-8、每行一个
   JSON-RPC 2.0 消息。stdout 只允许协议消息，stderr 只作有界诊断日志；stdout 混入
   日志、空帧、超限帧或 malformed JSON 时 fail-closed。
2. Client 发送 `initialize`，`protocolVersion` 必须为 `2025-11-25`，client
   `capabilities` 必须显式为 `{}`，`clientInfo` 只声明本 Adapter 身份。响应 ID 必须精确匹配、
   版本必须仍为 `2025-11-25`，server capabilities 必须包含 `tools`；随后才发送
   `notifications/initialized`。版本不匹配、缺 tools capability 或初始化超时均使
   Admission 在 Run/Attempt 写入前失败。
3. Adapter 调用 `tools/list` 并消费全部分页。cursor 是不透明值；重复 cursor、页数/
   工具数/定义总量超限、重复 tool name、非法或超出既有 Action JSON Schema 子集的
   inputSchema 均拒绝 Admission。首片不订阅变更，也不在已发布 Run 中刷新定义。
4. Core 只保留 Binding 配置 allowlist 中的精确 tool name，按既有本地 alias、Effect
   和结果上限生成 `FrozenActionDefinitionV1`；MCP tool name 是
   ProviderActionID，PublicActionID 仍由消费方配置决定。完整定义冻结到成员快照后关闭
   会话；重入、恢复和 backup/restore 不重新 initialize 或 list。

JSON-RPC request ID 只能是非 null 的 string 或 integer，在单一会话的未完成请求中
唯一；响应必须逐值匹配。notification 不含 ID，也不得收到响应。每个 response 必须
在 `result` 与 `error` 中恰有一个；标准 JSON-RPC parse/invalid request/method/params/
internal error 都按调用阶段归类，不得把 error object 当作 ToolResult。

#### 7.8.4 Prepare、Gateway 与唯一调用

MCP Adapter 的 `Prepare` 必须是纯函数：它只用冻结 Definition、DefinitionDigest、
canonical 模型输入和精确 grant 校验 schema/tool allowlist，并生成有界 canonical
`mcp-prepared-tool-call/v1 {tool_name, arguments}`。它不启动进程、不执行 initialize/
list/call、不读当前 Catalog、不写 Store，也不产生新的摘要、路由、Effect 或权限。

Store 按既有事务提交 `DispatchAttempt=PENDING` 与 Proposal，Gateway 取得并消费一次性
permit 后，才允许私有 MCP executor：

1. 重新验证精确完整 ArtifactDigest 后，新建一个有界 stdio 子进程会话，执行同样的
   `initialize → initialized` 精确版本/capability 校验；不再调用 `tools/list`，也不
   改变已冻结 Definition。
2. 从原 Proposal 与冻结 Binding 重建一次且仅一次 `tools/call`，参数只能是精确
   `name=tool_name` 与 canonical `arguments`。同一 DispatchAttempt 不存在第二次 call、
   SDK 自动重试、Binding fallback 或替代进程重放。
3. 收到终态或达到 deadline 后立即收口会话。首片没有常驻 Host、连接池、预热进程或
   跨 Admission/Run 复用的 MCP session；进程生命周期不是 Agent、Workspace 或 Run
   生命周期。

所有阶段都必须有硬 deadline。正常关闭先停止发送、关闭 stdin 并在有界 grace 内等待
子进程退出；未退出则终止，仍未退出则强制杀死整个受管进程树，同时有界排空 stderr。
MCP `2025-11-25` 没有可替代该流程的 shutdown RPC。若已发送 `tools/call` 后发生 Core
取消/超时，Adapter 应在连接仍可写时发送 `notifications/cancelled`，但该 notification
和进程退出都不能证明工具没有执行。

#### 7.8.5 终态与恢复语义

- spawn/initialize/version/capability 失败，或能证明 `tools/call` 尚未写出时，是确定
  `FAILED`，外部效果为零。
- 精确匹配、由 server wire 返回的 JSON-RPC error response 与合法 ToolResult
  `isError=true` 是两种不同的确定 `FAILED` 终态；二者都保存有界真实诊断且不得自动
  重试。SDK 本地合成的 client-closing（包括本地 `-32003`）不是 server wire 终态。
- 精确匹配的合法 ToolResult 是执行成功。其 result 按既有 canonical/大小规则进入
  `SUCCEEDED`；若成功已被明确确认但结果正文不合法，则沿用既有
  `SUCCEEDED + RESULT_REJECTED`，不能把已确认 Effect 改写成 UNKNOWN。
- `tools/call` 帧可能已写出后出现 partial write、timeout、取消、EOF、本地
  client-closing、进程崩溃、响应 ID 不匹配、malformed/超限响应，或无法可靠判断工具
  是否执行时，必须提交原
  DispatchAttempt 为 `UNKNOWN`。取消通知、kill 或重启都不是“未执行”证据。
- 启动恢复、自动重入和人工对账只读取/更新现有 `dispatch_attempts`；不得重新
  initialize/list/call、重新 Prepare、启动替代进程或创建等价 Attempt。未来新的独立
  logical operation 可以启动新进程，但不能重放这个 UNKNOWN。

首片不新增 MCP 专用 ledger、job、session、capability、tool 表或 Port。所有 Proposal、
PENDING、SUCCEEDED/FAILED/UNKNOWN、receipt/evidence、CAS、备份和恢复继续以现有 Action
`dispatch_attempts` 为唯一权威事实。当前接受仅覆盖上述本地 stdio Tool 纵链；REMOTE/
HTTP 与其他 MCP 能力仍需独立合同、实现和证据。W2-R1 的 §22 是独立 REMOTE Action
协议，不是 MCP Streamable HTTP，也不扩大本 MCP 切片。

#### 7.8.6 Operator Module Apply v1 控制面

运行后安装、激活和绑定不引入第二个 Runtime 或动态插件系统。停机态 Operator 命令只接受
exact canonical `module-apply-plan/v1`。当前 Core-owned 表恰有以下 9 个 exact handler tuple；
handler key 固定为 `exact Port + runtime mode/protocol + verified consumer schema`：

1. `model.generate/v2 + TRUSTED_IN_PROCESS/go-in-process/v1 + model-binding-config/v2`，
   受控 DeepSeek Model；
2. `context.provide/v1 + DECLARATIVE/static/v1 + context-binding-config/v1`，声明式 Context；
3. `context.provide/v1 + TRUSTED_IN_PROCESS/go-in-process/v1 + knowledge-context-binding/v1`，
   本地确定性只读 Knowledge；
4. `context.provide/v1 + TRUSTED_IN_PROCESS/go-in-process/v1 + memory-context-binding/v1`，
   本地有界 Memory；
5. `action.provider/v1 + LOCAL_PROCESS/mcp-stdio/2025-11-25 + action-binding-config/v1`，
   本地 MCP stdio Action；
6. `action.provider/v1 + TRUSTED_IN_PROCESS/go-in-process/v1 + action-binding-config/v1`，
   编译进 Core 的 `text.stats` Action；
7. `channel.transport/v1 + TRUSTED_IN_PROCESS/go-in-process/v1 + channel-binding-config/v1`，
   first-party loopback Channel transport；
8. `action.provider/v1 + REMOTE/freeagent-action-http/v1 + action-binding-config/v1`，
   默认关闭且由 Operator 精确授权的窄 HTTPS Action；
9. `action.provider/v1 + WASM/freeagent-action-wasm/v1 + action-binding-config/v1`，
   默认关闭、零 Host capability 且由 Operator 精确授权的纯计算 WASM Action。

这里是 9 个 handler tuple，而不是 9 种 protocol；当前共有五组 mode/protocol pair。同一个
`context.provide/v1` 由消费方 exact Config 的 placement 与 parameters schema 区分静态 Context、
Knowledge 和 Memory，不新增专用 Port。计划的 tagged target 只允许 `PROFILE` 或
`WORKSPACE_CHANNEL_ENDPOINT`；第七个 Channel handler 只能绑定 Workspace-owned Endpoint，
其余八个 handler 只能绑定 Profile。`ENABLED` Plan 必须选择 exact Port，并在 module 中携带
exact `expected_runtime_request:{mode,protocol}`，同时携带消费方 Config、Authority ceiling、
FailurePolicy 与 Port 内顺序。该 request 只表达 Operator 对 Manifest runtime request 的预期；
Core 才使用完整 handler key 选择唯一 handler，并授予 ExecutionClass 与 AdapterIdentity。Plan 与
Manifest 都不能授予 Trust、ExecutionClass、AdapterIdentity、Authority、handler identity、
fallback 或 merge policy，也不能请求表外 Port、protocol 或实现。

`ENABLED` 在任何持久写入前必须：验证源包和计划的精确 Module/Version/ArtifactDigest，
验证 exact Port、Config 与 Authority canonical wire、目标 Profile 或 Workspace/Endpoint 及对应
Binding payload，并证明 Plan expectation 与 Manifest request 完全一致。声明式 Context 只能
恢复 Manifest entrypoint 中的 canonical `static-context/v1`，使用 exact deny-all Authority，且不
执行代码；MCP 路径构造一个不启动进程的 exact registration/activation probe；`text.stats` 路径
只复验固定 Module/Version/ArtifactDigest、consumer schema 与编译进 Core 的 handler identity，
不加载或调用 Provider；Model 路径只允许 fixed DeepSeek Module/Version/ArtifactDigest/adapter/provider
Instance 内 Flash↔Pro，并复验 Config、strict Authority 和可选 ModelProfile；
它不解析 Secret、不调用 Provider、不联网。Channel 路径只复验固定 loopback Module/Version/ArtifactDigest、
`channel-binding-config/v1` 与 Core-owned handler identity，不解析 Secret、不构造 Adapter、不联网。
REMOTE Action 路径只恢复 ArtifactDigest 覆盖的 canonical `content/` descriptor，并构造不执行
Provider 的 native Adapter；endpoint 与 SecretRef 必须来自消费方 Config 并命中本次 Operator 的
精确瞬时 grant，Secret value 不解析。WASM Action 路径同时重验 descriptor 与受摘要覆盖的 guest，
只运行有界静态 preflight 和 wazero compile validation；不得实例化 guest 或调用导出函数。Apply
绝不执行模型调用、MCP initialize、tools/list、tools/call、`text.stats`、Channel send、REMOTE POST
或 WASM guest，也不产生
ActionProposal 或 DispatchAttempt。Action/Channel 模块真正被某个新 Run 选择后，
Describe/Prepare/Execute 仍完全遵守 §7.7/§7.8 的原纵链。

artifact grant 按已选择 handler 精确分离：MCP handler 只接受与候选摘要相同的
`--allow-local-mcp-artifact`；编译进 Core 的 `text.stats`、loopback Channel 与窄 DeepSeek Model handler
只接受与候选摘要相同的 `--allow-trusted-in-process-artifact`；REMOTE Action handler 必须同时接受
与候选摘要相同的 `--allow-remote-action-artifact`、与 Config 完全相同的
`--allow-remote-action-endpoint` 和 `--allow-remote-action-secret-ref`。Model 还必须提供与 Authority 内
SecretRef identity 精确匹配的 transient `--allow-model-secret-ref`；它不进入 Plan、Store 或输出。
WASM Action handler 只接受与候选摘要相同的 `--allow-wasm-action-artifact`，且 parameters 必须为
exact `{}`、Config/descriptor/Authority Effect 均须为 `none`。四类 artifact grant 互斥；声明式 Context、Knowledge 与 Memory
三个 handler 禁止任何 grant。Knowledge/Memory 的 Manifest 虽请求 `TRUSTED_IN_PROCESS`，包本身
仍只是不可变数据，不因此取得 compiled Action grant、包内代码执行或通用进程内执行资格。

Knowledge Context 必须使用 `UNTRUSTED_DATA`、`allow_summary=false`、`allow_drop=false`、
`knowledge-context-binding/v1` 和 `REQUIRED` FailurePolicy；它不携带 StaticContext refs。
Manifest 必须只提供 exact `context.provide/v1`、没有 Requires 或 requested permissions，并以
canonical `content/` entrypoint 承载 `knowledge-source/v1`。Artifact 中恢复出的 source、Config
source 与 `knowledge-authority-ceiling/v1` source 必须完全相同；Authority Tenant 必须与计划
Tenant 相同，非通配 Workspace/Agent scope 必须存在于当前 Control。候选 Context 的全局语义
顺序必须保持全部 `TRUSTED_INSTRUCTION` 在 `UNTRUSTED_DATA` 之前，否则在发布前拒绝。

本地策略只把该不可变数据包绑定到固定内建
`freeagent.adapter.knowledge.lexical/v1`，并授予精确 `TRUSTED_IN_PROCESS` ExecutionClass；包与
Plan 都不能选择 Adapter、Trust 或扩大 Authority。Apply 可以构造并复验该只读 Adapter，但
不得调用 Knowledge provider，也不加载或执行包内代码。若未来允许包内代码、其他 REMOTE Host 或
自动执行计划，必须另建显式授权合同，不能沿用本数据包例外或 W2-R1 的单一
`freeagent-action-http/v1` 授权。

下述 Memory 装配边界同步 W3-M1 已验收合同，不把该实现重新归属于 W2-D。Memory Context
同样必须使用 `UNTRUSTED_DATA`、`allow_summary=false`、`allow_drop=false`、
`memory-context-binding/v1` 和 `REQUIRED` FailurePolicy，且不携带 StaticContext refs。Manifest
固定为只提供 exact `context.provide/v1`、没有 Requires/requested permissions 的
`TRUSTED_IN_PROCESS + go-in-process/v1` 数据包；本地策略唯一授予
`freeagent.adapter.memory.deterministic/v1`。Config 只能请求有界确定性算法，Core-owned
`memory-authority-ceiling/v1` 仍按 exact Tenant、Agent、Workspace、kind 和读取上限收窄权限。
Apply 只在 Control/Catalog 已成功发布并复验后闭合 Memory head：不存在时创建一次确定性的空
Genesis，存在时逐字节保留原 head；CAS 失败不得创建 head，post-CAS 闭合失败必须 fail-closed，
同一 canonical 计划重入只能修复该闭合，不能覆盖、合并或推进已有 Memory。Disable 仍只影响
未来 Binding，不删除历史 Memory revision。

Model 变体固定使用 `PROFILE` target 与唯一 required/index-0 `model.generate/v2` Binding。Plan 是
Config、strict `model-authority-ceiling/v1` 和 optional ModelProfile 的完整目标状态；省略 Profile
即清除旧 ref。Profile 提供时必须
匹配 provider/model/build/config/artifact/adapter 且只能收紧 ContextPolicy。首版只允许同一
`freeagent.builtin.model.deepseek@1.0.0` artifact/adapter/provider Instance 内 Flash↔Pro；新 publication
只影响新 Run，旧 Run 保持冻结。Model `DISABLED` 必须在写 Store 前拒绝，回滚只能发布新的
`ENABLED` Apply；原 `MODEL_UNKNOWN` 不得 fallback、换模型、建立替代 Attempt 或语义重放。

Channel 变体固定使用 `WORKSPACE_CHANNEL_ENDPOINT` target 与
`channel.transport/v1 + TRUSTED_IN_PROCESS/go-in-process/v1 + channel-binding-config/v1`。计划还必须
携带 Operator-owned Endpoint route 与有界 canonical `cursor_seed`；模块包不能选择 Workspace、
Endpoint、Target Agent/Profile、Cursor scope、SecretRef 或 Authority。`ENABLED` 只通过 Store 的单一
发布事务，在同一个 `BEGIN IMMEDIATE` 内提交下一 Control/Catalog 与该 Endpoint revision-0
`CURSOR_SEED`，任一步冲突均整笔回滚。精确重试即使该 Cursor scope 已推进到 revision 1 以上，也
必须读取并逐字节验证原始 revision-0 seed；不能拿当前最大 revision 替代发布事实。

Channel `module-dry-run` 仅验证候选包、当前 Basis、Endpoint 计划和 projected seed，不写 Store、
artifact root 或 Cursor，不解析 Secret、不构造 Adapter、不联网。`DISABLED` 只从下一 Control 删除
当前 Workspace 的目标 Endpoint，保留 Cursor revision、Ingress receipt、Run、DispatchAttempt、历史
Control/Catalog、Module History 与 evidence。多个 Workspace Endpoint 可以引用同一 Instance；仅当
最后一个 current Endpoint/Profile 引用消失时，下一 Catalog 才删除该 Instance。

artifact staging 必须是 artifact root 内的隐藏直系目录，不能依赖其父目录与 root 恰好位于
同一文件系统。final no-replace 发布及 no-launch Host 复验后，Apply 必须在首次 immutable
Store 写入前删除 staging 并同步 artifact root；清理失败即 fail-closed。复用 crash-left 或
预先存在的 exact final artifact 时，还必须有界同步全部普通文件并再次复验，不能只同步目录。

首个 `mcp-stdio/2025-11-25` Adapter 的 Action Config `parameters` 只接受精确 `{}`；这是
当前窄 Adapter 的版本化限制，不是 `action.provider/v1` 或统一外挂接口永久禁止参数。任何
放宽都必须由新的 Host/Config 合同显式验收，不能由 Plan 或模块包自授权。

对 `PROFILE` target，Control 中 `Profile.Bindings` 的全局顺序与同 Port 相对顺序均为语义。索引
只表示目标 Profile 的 exact Port Binding 子序列位置；实现可以在对应物理位置插入，但不得排序或
移动任何其他 Binding。同一 Profile、Port 和 Instance 最多一个 Binding。对
`WORKSPACE_CHANNEL_ENDPOINT` target，Endpoint 自身只携带一个 `REQUIRED` Channel Binding，不使用
Profile 的 `port_binding_index` 排序语义。同一 Workspace/Endpoint 唯一。Enable 若发现 Instance、
模块、Config、Authority、FailurePolicy、目标或位置已有不同事实，必须拒绝而非升级、替换、rebase
或 fallback。

除 Model 外，`DISABLED` 按 tagged target 只从下一 Control 移除目标 Profile 的 exact Port + Instance Binding，
或移除目标 Workspace 的 exact Endpoint。仅在整个下一 Control 已无任何消费者引用该 Instance 时
才从下一 Catalog 删除 Activation。禁用不删除 package、
activation、artifact、Run 或 Attempt；旧冻结 Run 在下一次 Host 使用前由既有 current
Activation check fail-closed。既有 UNKNOWN 原样保留并可继续按原 Attempt/Provider evidence
对账，绝不因禁用变成 FAILED、未执行或可重放。

Apply 必须在服务已停止、Store 独占 writer fence 已取得后先运行既有 Store-only recovery，
把 crash-left PENDING 按原规则收口为 UNKNOWN，并在 Control/Catalog CAS 前证明全局
PENDING 为零；恢复失败则 pointer 不变。UNKNOWN 不阻止 deny-only 禁用。首片没有在线热
切换、后台 watcher、自动决策、主动预热、模型调用、通用 REMOTE Host、Secret broker、卸载或
purge；W2-R1 只增量授权 §22 的单一 REMOTE 协议。因此不会回收可能仍待人工对账的外部效果事实。

停机态 `module-list`、`module-history` 与 `module-inspect` 的 Store 读取都只能经过
capability-restricted `ReadOnlyObserver`。`module-list` 观察当前 PublishedBasis、目标 Profile Binding
或 Workspace Endpoint Binding
以及 Catalog 对 immutable Installation/stored Manifest 的闭包；它不读取 artifact root。
`module-inspect` 只接受 current Catalog 中的 exact Instance，并在上述 Store 闭包之外复验
调用方给出的 artifact root 中的完整 digest/covered size，再输出非敏感 Manifest 摘要。

`module-history` 必须显式接收同一 Tenant 的 exact Control revision 与 Catalog generation。它先以
`LoadControlCatalogRevision` 恢复该不可变 pair，再通过既有窄 Installation 投影读取并解析 stored
Manifest，以闭合 Source、Activation、Port 与当时的目标 Profile Binding 或 Workspace Endpoint
Binding refs。历史查询不读取
artifact root，不输出 Manifest 正文或摘要，不构造 historical PublishedBasis，不查询或虚构历史
pointer revision，不枚举、不计算 diff，也不把不可达 Activation 猜成 DISABLED。mismatched pair
必须 fail-closed，唯一 live authority 仍是 `control_current`。

三个查询都不创建 Run、PortPlan、ModuleHost、Attempt、recovery 或外部效果，也不单独
materialize 或输出 Config、Authority、StaticContext 正文。
`module-disable` 不是第二条控制链：它只预拒绝非 DISABLED 计划和任何 Model Disable，然后原样进入本节既有
Apply/recovery/CAS。`module-dry-run` 使用同一 canonical plan 和同一当前状态/候选冻结判断，
但只能通过 `ReadOnlyObserver` 读取，并只能在系统 TEMP 验证 incoming artifact。它不得取得
writer、运行 recovery、发布 Control/Catalog、写 artifact root、启动 MCP process/protocol、
调用 Knowledge provider、模型、网络或 Secret resolver。输出的 candidate Basis 必须标记为
`PROJECTED_NOT_RESERVED` 或 `OBSERVED_CURRENT`；不得输出 future Activation identity/revision、
Config/Authority/StaticContext/Manifest 正文或主机路径。Model/Action/Channel PENDING 只汇总为
`startup_recovery_required=true`，UNKNOWN-only 为 false，均不修改或重放。操作面不增加
Role、Skill、Knowledge、Memory 或 MCP 品牌分支；Knowledge 与 Memory 只是 Config 选择的
Context Port 变体。历史查询仍是停机、调用方已知 revision 的窄投影；在线热查、自动发现/升级
和稳定公共控制面不属于该开发切片。

这些装配路径复用现有 Model/Knowledge/Memory Runtime、Store、Loop、artifact 闭包和通用 backup，
不新增 Port、Runtime、Store、表、执行账本或 backup schema。MCP、`text.stats` 与 R1 REMOTE 的
所有真实 Action 调用都继续经过唯一 Gateway 和 DispatchAttempt 账本；只有 MCP handler 启动
外部进程，编译进 Core 的 `text.stats` 不启动进程且无外部效果，REMOTE handler 只由 native
Adapter 在原 PENDING Attempt 后发起一次受限 HTTPS POST。未绑定 Knowledge/Memory
的 Pure Chat 继续零访问对应 artifact、Adapter 与检索路径，因此增强自由外挂与共享知识设计，
不把领域知识或 Agent Memory 固化进 Core、Agent 或 Workspace。

ArtifactDigest 不覆盖平台文件 mode。Current Backup Restore 对本切片的 exact MCP artifact
只能从已验证 Manifest/descriptor 派生安全模式：目录与 descriptor 指定的唯一 executable
为 `0700`，其他普通文件为 `0600`。来源的 setuid、setgid、sticky、group/world 位不得传播；
chmod 后须 fsync，并在不启动进程的情况下完成 Host 构造校验。

## 8. Pure Chat

`PURE_CHAT` 是 Assembly Compiler 根据已物化的 System、Profile、Agent、
Workspace 和 Task 请求推导出的装配模式，不是运行时 fallback。

Pure Chat 必须：

- 装配一个 `model.generate/v2` Binding。
- 不查询 Skill、RAG、Memory、Action 或 MCP 仓库。
- 不创建可选模块实例或空 PortPlan。
- 不读取 Action artifact/Registry/Authority，不执行 Describe、Prepare、Gateway、
  相似度搜索或“是否存在可用 Skill”的探测。
- 不解析 MCP Config/Authority/artifact，不构造 MCP SDK client，不启动本地进程，也不
  发送 initialize、`tools/list` 或 `tools/call`；这些零访问必须可计数验证。
- 不读取 REMOTE Action artifact/descriptor/Config/Authority/SecretRef，不构造 production
  lazy loader、native Adapter 或 Secret resolver，也不创建 Action DispatchAttempt 或发出 POST；
  即使 Store 中存在已安装但未选择的 REMOTE Activation，该零访问边界仍不改变。
- 不读取 Channel Endpoint、Identity/ACL、Cursor、Config、Authority、Secret 或 artifact，
  不构造 Channel Adapter/handler/worker，也不发送网络请求。
- 不读取或选择 CompositeAgents 配置，不创建 Parent/Child Run，不生成 assignment/result
  envelope；所有 Composite optional snapshot/manifest 字段必须省略。
- 使用 Core 系统约束、当前任务和 History 构造最小上下文。

进程开放流量前的 §9.4 全局共享账本 safety scan 是上述 Channel 零访问的唯一例外：
它可以读取同一 `dispatch_attempts` 的最小 Channel 状态投影并把遗留 PENDING 原位收口为
UNKNOWN，但不能读取任何 Channel 可选资源、构造 Adapter 或产生网络效果。它不属于
Pure Chat Admission 或单 Run 热路径。

无 Action 的 `model.generate/v2` 请求正文固定为
`ModelGenerateRequestV1{schema_version,messages,parameters}`，且不得序列化空
`actions`。`messages` 按
§7.3 的 Context Compiler 输出顺序构造；其中不得
放入 RunID、AttemptID、lease、时间戳或 Provider 路由元数据。Provider、模型、
权限和执行位置只来自冻结的 `PortBinding`。这样稳定系统/静态上下文自然处在正文
前缀，当前用户输入始终最后；参数只来自唯一 `model.generate/v2` Binding 的冻结
Config。相同语义的不同 Run 必须产生字节一致的 canonical request，缓存优化不需要
第二套请求协议，也不改变调用语义。

成功输出使用 `ModelGenerateOutputV1`；Usage 使用独立
`ModelUsageReceiptV1`。Usage 的每个数值都是 nullable，缺失就是 UNKNOWN，显式
零才是零；原始 Provider receipt 保留在 receipt 内。S1 Core 不接受任意第三方
JSON 冒充这三个版本化 wire value。

S1 的文本入口与声明式上下文分别使用精确的 `task-input/v1` 和
`static-context/v1` 内容格式；两者只携带版本与 NFC 文本。来源 Provider、顺序、
trust 和 authority 由对应冻结 Binding/Policy 表达，不在正文重复。后续多模态通过
新增版本化内容合同扩展，不改变 Manifest、PortPlan 或 Universal Loop 结构。

已随 CompileInput 物化且精确绑定的声明式 Role/Prompt/静态 Skill 可以保留在
Pure Chat 中；它们只能通过 `context.provide/v1` 提供不可变静态上下文，不得触发
仓库发现、状态读取、远程调用或 Effect。只要满足这些约束，该 Run 仍标记为
`PURE_CHAT`；其内容 digest 必须由对应 PortBinding.StaticContextRefs 冻结进
MemberExecutionSnapshot，成为唯一恢复边。不存在第二种 S1 Chat 模式。显式选择
Action-enabled Profile 时必须进入 §7.7 的真实链，不能静默丢弃 Action 后走 Pure
Chat；尚未实现的 MCP 能力、超出 §20 的通用动态图/多轮返工或其他 Port 必须返回明确的
`CAPABILITY_NOT_AVAILABLE`；§18 的历史单次 Reviewer gate 只有在冻结
Composite 定义显式携带合法 optional Reviewer 时进入；公平 Scheduler 只有在 §16 的
Operator 开关显式启用时
进入同一 Loop。Parent/Child + Composite 只在显式选择
`chat --composite` 且冻结 Control 中存在合法定义时进入；跨 Workspace 仅在 W5 Decision
plan、双边 grant 与 §20 REQUEST/RESULT 合同完整时允许。缺失定义或请求超出 depth-1、单
repair round 能力面必须失败关闭。既有非 Composite Pure Chat 的
canonical bytes、摘要与 optional 仓库/Provider 零访问计数不得因此改变。

## 9. 唯一 Universal Loop

### 9.1 公共接口

```go
type Loop interface {
    Run(context.Context, RunInput) (RunResult, error)
}

type RunInput struct {
    RunID       string
    MaxSteps    uint32
    MaxDuration time.Duration
}

type RunResult struct {
    RunID        string
    Disposition  Disposition
    FrameRevision uint64
    ReasonCode   string
}
```

Disposition 只有：

```text
YIELDED
WAITING_INPUT
WAITING_EXTERNAL
WAITING_RECONCILIATION
TERMINATED
```

- `YIELDED`：预算耗尽但 Run 仍可推进。
- `WAITING_INPUT`：需要新的受控输入。
- `WAITING_EXTERNAL`：已知外部操作仍在进行，且有可恢复查询引用。
- `WAITING_RECONCILIATION`：Attempt 是否完成无法确认，禁止继续产生同语义调用。
- `TERMINATED`：已成功完成、明确失败或取消。

正常等待和终止必须通过 Disposition 返回；`error` 只表示基础设施故障、损坏或
违反不变量。

`MaxSteps` 和 `MaxDuration` 只是单次调用上限，只能缩小 Manifest/Usage Ledger
冻结的 Run 预算，不能扩大它。Lease owner 由进程内部 Lease Manager 注入，公共
调用方不能指定 owner 或 lease epoch。

### 9.2 LoopFrame

```go
type LoopFrame struct {
    RunID                 string
    Revision              uint64
    OwnerID               string
    LeaseEpoch            uint64
    Step                  string
    UsageLedgerRef        string
    Continuation          json.RawMessage
    PendingModelAttemptID string
    PendingDispatchAttemptID string
    WaitingReason         string
    LastAuthoritativeEvent uint64
}
```

Frame 的每次更新必须使用 revision CAS 和 lease fencing。Loop 不得依赖未落盘的
权威状态；`UsageLedgerRef` 指向与 Frame 同步 CAS 更新的权威 Usage Ledger
revision，不复制第二份用量投影。continuation 必须带版本并可在进程重启后解码。
S1 的唯一规范 UsageLedgerRef 是
`usage-ledger/v1/<run_id>/<sequence>`：`run_id` 服从 256-byte opaque ID
规则，`sequence` 是不超过 SQLite `MaxInt64` 的无前导零十进制数，完整引用使用
292-byte 专用上限，不得套用 256-byte opaque ID 上限。解析时必须核对其中的
`run_id` 与当前 Run 完全相同。
`loop-continuation/v1` 只允许 `READY`、`WAITING_CHILDREN`、`MODEL_PENDING`、`ACTION_PENDING`、
`CHANNEL_PENDING`、`MODEL_READY_AFTER_ACTION`、`WAITING_RECONCILIATION` 和
`TERMINATED`。`READY` 与 `WAITING_CHILDREN` 不携带 Attempt 身份；普通调用终态必须同时冻结
`attempt_kind=MODEL|ACTION|CHANNEL`、`logical_step_id` 与原 `attempt_id`，恢复时不得通过
ID 前缀推断、重新生成或替换。唯一无 Attempt 的终态例外是 permit 前的 Composite Core
确定性失败：continuation 只携带有界 `core_failure_reason`，并由同 revision 的
`CORE_DETERMINISTIC_FAILURE` event 闭合。
两个 pending 指针最多一个非空，并且必须与 continuation 的 kind/state 精确对应；
`MODEL_READY_AFTER_ACTION` 引用已成功的原 Action Attempt，但两个 pending 指针均为空；
`CHANNEL_PENDING` 必须引用 `pending_dispatch_attempt_id` 与 `attempt_kind=CHANNEL`。

S1 Admission 不允许调用方选择初始运行状态。固定写入：

```text
runs.state                    = ADMITTED
runs.revision                 = 0
loop_frames.step              = READY
loop_frames.frame_revision    = 0
loop_frames.usage_ledger_ref  = usage-ledger/v1/<run_id>/0
loop_frames.continuation      =
  {"schema_version":"loop-continuation/v1","state":"READY"}
loop_frames.pending_model_attempt    = NULL
loop_frames.pending_dispatch_attempt = NULL
loop_frames.lease             = empty / epoch 0
loop_frames.last_event        = 0
run_events[0].event_kind      = RUN_ADMITTED
```

Composite family 在同一 Admission 事务中改用下列唯一初态；除这些差异外仍沿用上面的
revision、ledger、lease 与 Event 规则：

```text
Parent: runs.state                 = ADMITTED
Parent: loop_frames.step           = WAITING_CHILDREN
Parent: loop_frames.continuation   =
  {"schema_version":"loop-continuation/v1","state":"WAITING_CHILDREN"}
Parent: pending attempt pointers   = NULL
Child:  loop_frames.step           = READY
Child:  loop_frames.continuation   =
  {"schema_version":"loop-continuation/v1","state":"READY"}
```

`WAITING_CHILDREN` 的成员集合只能从 Parent manifest 的 immutable
`Composite.Plan.Children` 恢复，Frame 不得复制可变 Child list、完成计数或结果正文。

event 0 的 `RUN_EVENT_PAYLOAD` 只包含 `run_id`、`manifest_digest`、
`member_snapshot_digest` 和 `schema_version=run-admitted-event/v1`；created_at 只在
Event 行中记录，不进入 payload。空 Usage Ledger 用 sequence 0 的固定引用表示，
不为它创建占位 Attempt、Usage 行或第二张 ledger-head 表。

### 9.3 推进算法

每次 `Run` 是一次有界推进：

1. 生成进程内 owner，并由 Current Store 在一个事务中取得当前 Run/Frame head 的
   lease。
2. 读取并验证 RunManifest、MemberExecutionSnapshot、LoopFrame 和 Ledger。
3. 始终检查未终结 ModelDispatchAttempt；只有成员快照含 Action PortPlan 时才读取该
   Run 的 Action DispatchAttempt，只有 Channel-origin Run 才读取其 `CHANNEL_SEND`
   DispatchAttempt。恢复到任一 Model、Action 或 Channel PENDING 时只把原 Attempt 写为
   对应 UNKNOWN，不得再次调用 Adapter、Prepare 或 Gateway。§9.4 的一次全局启动安全
   扫描单独计量，不属于 Pure Chat 单 Run 热路径。
4. `WAITING_CHILDREN` 时按 Parent manifest 的规范顺序验证全部 exact Child：任一 Child
   `FAILED` 则以 `ALL_REQUIRED_CHILD_FAILED` 明确终止 Parent；任一 Child 存在
   `MODEL_UNKNOWN`/等待对账则 Parent 返回 `WAITING_RECONCILIATION`；尚有合法未终态 Child
   则返回 `WAITING_EXTERNAL`；只有全部成功且 result/digest/revision 闭合时才构造
   §7.3 的 merge Context，并准备 Parent 唯一 `composite.merge/v1` logical step。Loop 不得
   在这里创建、替换、重试或重排 Child。
5. READY 时读取 Core History并按冻结 PortPlan 收集 Context；含 `ConversationTurn` 时只按
   Manifest 的 predecessor 链恢复已成功旧 Run 的完整 USER+ASSISTANT pair，不读取可变
   Conversation head 来改写当前 Run，也不复制旧 `history_entries`；
   MODEL_READY_AFTER_ACTION 只恢复原模型一 MODEL_REQUEST 与 ACTION_RESULT，不重新
   收集 Context、RAG 或 Memory。
6. 构造不含动态身份的 canonical 模型请求；有冻结 Action 时追加有序
   `actions`，Action 成功后只在原请求消息末尾追加不可信结果 envelope。
7. `BeginModelDispatch` 先检查冻结 deadline、Run/family cancel latch 与静态 family
   dispatch cap：任一拒绝时不得创建 permit；deadline/取消可确定时原子提交相应终态，
   cap 损坏时 fail-closed。否则提交唯一 PENDING。只有本次返回
   `Created=true && InvokeAllowed=true` 才能
   武装进程内一次性 InvocationGate，并按冻结 `ArtifactDigest + AdapterIdentity`
   调用一次。
8. 若模型返回最终回答，普通 Run 在权威事务中写模型终态、Usage、History、可选 Memory
   与终态 Frame；Channel-origin Run 则通过冻结 `channel.transport/v1` 纯 Prepare，在
   同一事务写模型终态、Usage、History、`CHANNEL_SEND_PROPOSAL`、`CHANNEL_SEND/PENDING`
   与 `CHANNEL_PENDING` Frame，并取得一次性 Gateway permit。提交前不得发送。
   若模型一返回合法且可执行的 Action，则先通过精确 Provider 只读 Prepare，再在同一
   事务中写模型 SUCCEEDED、Usage、ACTION_PROPOSAL、Action PENDING 与 ACTION_PENDING
   Frame，并取得一次性 Gateway permit。合法模型输出后的未知 Action、非法参数、Prepare
   失败、预算/deadline 拒绝或模型二再次请求 Action，都只提交模型 SUCCEEDED + Usage
   并以明确原因终止 Run，不创建替代 DispatchAttempt。
9. Gateway 重新校验快照、Binding、Definition/Proposal、Effect、Authority、预算、撤权、
   Run/family cancel latch、lease 与 permit 后，调用同一精确 Adapter 的私有 executor
   一次，并提交 Action
   或 Channel 的 SUCCEEDED/FAILED/UNKNOWN。Action 成功转到
   `MODEL_READY_AFTER_ACTION`；模型二产生最终回答，若为 Channel-origin Run，再进入上述
   同一 Channel Gateway 链。Channel 成功后才终止 Run；明确失败终止，UNKNOWN 等待原
   Attempt 对账。
10. 释放精确 lease 后返回 Disposition；Release 失败不能宣称正常完成。

一次调用可以推进多个内部步骤，但不得超过 `MaxSteps` 或 `MaxDuration`。每个外部
调用前后都必须存在可恢复边界。S1 不存在独立 LoopPolicy Port、模块或持久化事实；
这里的策略就是 Universal Loop 内部的确定性 Core 状态机。

PENDING 之后的普通 Host/Gateway error 无法统一证明调用是否开始，因此模型调用保守
提交 `MODEL_UNKNOWN`，Action 与 Channel 调用保守提交 `UNKNOWN`；三者都不 retry、
不 fallback，也不把第三方错误文本写成权威语义。
同一个 Begin 结果的所有内存副本共享一个不可伪造 permit，最多只有一个 Gate 能
成功武装；布尔字段本身不授予调用权。
明确的 Provider `SUCCEEDED/FAILED/UNKNOWN` 只有在 canonical output/usage 与冻结
InvocationID、Provider 身份全部匹配时才能分别进入对应状态；非法结果保守进入
`MODEL_UNKNOWN`。公共 `RunResult` 不携带回答，终态回答由内部 Chat 入口从 Current
Store 的 `GetTerminalRunResult` 读取，不能塞入 `ReasonCode` 或进程内缓存。

Universal Loop 不得：

- 读取当前 Control Plane 重新编译 Run。
- 按模块名称、模型品牌或 Provider 健康度重新路由。
- 在 Binding 失败后自行寻找替代模块。
- 创建 Child Run、绕过 Gateway 直接执行 Action 副作用，或直接发送 Channel。
- 从 Frame、当前 Control 或运行中结果发现 Child；它只能消费 Parent manifest 的冻结 ref。
- 为未实现能力静默走 Pure Chat。

### 9.4 生产启动恢复门禁

生产 composition 必须在 Catalog、Adapter Registry、Module Host、SDK client 与
Universal Loop **构造前**完成一次 Store-only safety transaction；完成前不得开放
ChatService 或新 Admission：

1. Current Store 只读取最小 ledger/continuation 投影，按 RunID 确定序扫描拥有 Model
   `PENDING/MODEL_UNKNOWN`、Action `PENDING/UNKNOWN` 或 Channel
   `PENDING/UNKNOWN` Attempt 的 Run，并验证一个 Run 至多一个未决 Attempt、
   Attempt/Frame 指针及最小 lease/fencing 关系。
2. 遗留 PENDING 只能以原 Attempt ID、原 revision 和原 Frame 指针执行 CAS，原地收口为
   对应 UNKNOWN / `WAITING_RECONCILIATION`；不得经 Universal Loop 推进，不得创建
   permit、替代 Attempt 或新 Run。
3. 现有 UNKNOWN 和稳定状态只验证最小 ledger、continuation、pending pointer 与
   lease/fencing 投影；必要的 lease acquire/release 只能推进 lease 账本，不能改变
   History、Event、冻结装配或执行语义。
4. 该事务绝不加载 MemberExecutionSnapshot、ActionDefinition、Channel Endpoint、
   Identity/ACL、Config、Authority、Secret、artifact、Catalog/Registry、SDK client 或
   Provider，也不调用 Decode、Describe、Prepare、Gateway、模型、RAG、Memory、MCP 或
   Channel 网络。任何需要这些内容才能作出的恢复判断都必须延后到服务构造完成后的显式
   Run continuation 或显式选择 Adapter 的调用边界。
5. CAS 冲突、多个未决 Attempt、未知最小 continuation、非法 lease 状态或最小 ledger
   不完整都必须 fail-closed。事务完成后再次扫描，要求没有遗留 PENDING，原 UNKNOWN
   仍引用同一原 Attempt。

该门禁是默认 Pure Chat 唯一允许触及 Channel 事实的共享账本扫描；它只复用 Current
Store 的安全状态机与既有 lease/fencing，不复用 Universal Loop，
也不定义第二 Runtime、后台 worker 或模块策略。完整 MCP artifact 验证只发生在显式
选择 MCP Adapter 的 `Describe/Execute` 边界，不属于启动恢复。

## 10. ModelDispatchAttempt

### 10.1 权威状态

```text
PENDING
SUCCEEDED
FAILED
MODEL_UNKNOWN
```

每个 Attempt 至少冻结：

- AttemptID、RunID、MemberID 和 FrameRevision。
- LogicalOperationKey，由
  `SHA256("freeagent.model-operation/v1\0" + CanonicalJSON({run_id, member_id,
  logical_step_id}))` 计算。
- MemberSnapshotDigest 与精确 model Binding。
- canonical 请求或内容寻址 RequestRef、RequestDigest。
- Provider、模型和参数。
- deadline 与 Usage Ledger 引用。
- Provider request ID、原始回执、对账引用或固定脱敏 UNKNOWN 观察位置码。
- 终态、结果引用、错误分类和 UsageRecord 引用。

`BeginModelDispatch` 的输入不得让调用方重新提交 Member、Binding、Provider、
Model 或 parameters；这些值分别从冻结
`MemberExecutionSnapshot`、规范 model request 与 RunManifest
派生。只有首次原子创建 PENDING 的返回值可授予本进程一次调用资格；相同语义重入
只返回原 Attempt 且明确禁止再次调用。首次 Begin 发现 deadline 已过期时，必须在
同一事务直接写 `FAILED/DEADLINE_EXPIRED_BEFORE_DISPATCH`、无上报 Usage 与终止
Run/Frame，不创建 PENDING 或 permit。

### 10.2 调用顺序

```text
冻结请求
    → CAS/事务写 ModelDispatchAttempt=PENDING
    → 调用精确 model Binding
    → 原子写 Attempt 终态 + 模型结果 + History + Usage + LoopFrame
```

规则：

- PENDING 必须在网络调用前提交。
- 调用前 deadline 已过期时必须直接 FAILED，Adapter 调用次数为零；该终态的精确
  重入不得产生 permit。
- 只有确认成功才写 SUCCEEDED。
- 确认未完成且确认没有可用结果时才写 FAILED。
- 可能已发送、Provider 完成情况不明、已收到结果但终态无法落盘，均写或恢复为
  MODEL_UNKNOWN。
- 恢复时，无法由 Provider request ID 或可靠回执确认的 PENDING 必须转为
  MODEL_UNKNOWN。
- 同一 `(RunID, MemberID, LogicalStepID)` 最多只能有一个 Attempt。RequestDigest
  是该 Attempt 的冻结审计字段，不进入 key；否则通过重建略有差异的请求即可绕过
  UNKNOWN。
- MODEL_UNKNOWN 锁定当前 Run 的该逻辑步骤，只能查询或人工对账原 Attempt；
  不得自动重放、创建替代 Attempt、切换 Binding、创建 Child Run，或用自动新建
  Run 的方式绕过。新的显式 User/Operator Admission 才是新 intent；client retry、
  crash recovery、Scheduler retry 和 continuation 都必须恢复原 Run。

### 10.3 Action `DispatchAttempt` 与 Gateway

Action 复用同一 Attempt 安全语义，但不塞入模型专用表。状态只允许：

```text
PENDING → SUCCEEDED | FAILED | UNKNOWN
UNKNOWN → SUCCEEDED | FAILED  # 仅原 Attempt 有可靠对账证据
```

每个 DispatchAttempt 至少冻结 AttemptID、Run/Member/Frame、LogicalStepID、
MemberSnapshotDigest、精确 BindingIndex/Binding、PublicActionID、ProviderActionID、
DefinitionDigest、ACTION_PROPOSAL Ref、EffectClass、MaxResultBytes、deadline、
UsageLedgerRef、来源模型 Attempt、
外部操作/receipt/result/evidence refs、错误或未知原因与 revision。

LogicalOperationKey 固定为：

```text
SHA256(
  "freeagent.action-operation/v1\0"
  + CanonicalJSON({run_id, member_id, logical_step_id})
)
```

Binding、ActionID、输入与 Proposal ContentDigest 都不得进入 key；否则可通过改写它们绕过
UNKNOWN。同一逻辑步骤跨 ModelDispatchAttempt 与 DispatchAttempt 不得冲突。
首片逻辑步骤身份只由 Universal Loop 派生：Pure Chat 使用既有模型一步；Action Run
依次使用冻结的 `model-1`、`action-1`、`model-2` 语义步骤。Store 根据 Frame 与来源链
验证派生值，不接受外部调用方用任意新 step 绕过约束；首片的串行写事务还必须证明
该 Run/Member 尚无 DispatchAttempt。该阶段限制不成为永久表级唯一约束，未来多轮
Action 仍复用同一 Port、Loop 与账本。

模型一成功返回合法 Action 且 Prepare 成功后，唯一
`CommitModelActionAndBeginDispatch` 事务必须同时：

1. CAS 原 ModelDispatchAttempt、Run/Frame 和 lease。
2. 写入模型 Result/Usage，将合法 ActionRequest 对应的原模型 Attempt 提交为
   SUCCEEDED；只有按既有 Usage 规则出现可计量事实时才分配新 ledger sequence，否则
   ledger head 保持原值。
3. 重新验证冻结 Action 映射、schema、Proposal、Effect、Authority、deadline 与
   deny-only 撤权；Frame 与 DispatchAttempt 同时冻结第 2 步提交后的实际
   `UsageLedgerRef`（可能仍指向原 head）。FAC2 已删除金额预算闸门，这一步不存在
   以金额事实为由的拒绝路径。
4. 写入唯一 ACTION_PROPOSAL 与 `DispatchAttempt=PENDING`。
5. 将 Frame 从 MODEL_PENDING 原子切换为 ACTION_PENDING，并追加 Event。
6. 提交后才返回 Store 私有、进程内、共享原子消费位的一次性 Gateway permit。

该原子边界消除了“模型已提交但动作未记账”的中间状态。任一步失败全部回滚，
executor 调用次数必须为零；原模型 PENDING 在重启后按 MODEL_UNKNOWN 收口，不重放
模型或动作。若 wire 本身是合法 ModelGenerateOutput，但 Action 未知、输入不符、达到
一次 Action 上限、Prepare 确定失败或 deadline 已过，模型调用事实仍
必须以 MODEL_RESULT + Usage + ModelAttempt=SUCCEEDED 原子保存，同时以明确 Run 失败
原因终止，且不创建 DispatchAttempt/permit。只有模型输出 wire 本身 malformed 才把
Model Attempt 写为 FAILED。上述终止事务失败同样保留原 PENDING 并按 MODEL_UNKNOWN
恢复，不能凭内存结果补写另一条路径。

permit 绑定 AttemptID、MemberSnapshotDigest、BindingIndex、ACTION_PROPOSAL ContentDigest
与 expiry。
Gateway 必须再次验证 lease/fencing、current Activation deny-only 撤权、Authority、
Effect 和预算，成功消费 permit 后才按 exact ArtifactDigest + AdapterIdentity 取得同一
Adapter 的内部 ActionExecutor。permit 不序列化、不持久化、不放入全局 map；相同
Begin 的任何副本、重入或重启最多只有一次可执行资格。

`ActionExecutionResultV1` 必须满足 §7.7 的严格 wire 并绑定 AttemptID 与 Module Host
返回的精确 Provider。只有可靠证明动作成功或确认未执行/明确失败时才能写明确终态；
普通 error、超时、取消、身份错配、可能已开始执行或外部效果后终态事务失败均保守为
UNKNOWN；已确认 SUCCEEDED 的结果非 canonical/超限按 §7.7 写 RESULT_REJECTED，不改变
Effect 事实。唯一 `CommitActionDispatchOutcome` 原子写 result/receipt/
evidence、Attempt 终态、Run/Frame/Event；精确终态重入只逐字节验证，冲突终态
fail-closed。

SUCCEEDED + AVAILABLE 将 Frame 写为 `MODEL_READY_AFTER_ACTION` 并引用原 Action
Attempt；RESULT_REJECTED 明确终止 Run。下一模型
Attempt 必须从已持久化的模型一请求和 ACTION_RESULT 构造请求，记录
`source_dispatch_attempt_id`，不得重新运行 Describe、Prepare、RAG 或 Memory。
FAILED 终止；UNKNOWN 进入 WAITING_RECONCILIATION。UNKNOWN 不回到 PENDING，不自动
重放、换 Binding、换 Action、换 Proposal、创建替代 Attempt/Run 或执行补偿；可靠
证据只能通过唯一 `ReconcileActionDispatchOutcome` 事务更新原 Attempt。该事务必须
CAS 原 UNKNOWN Attempt revision、WAITING_RECONCILIATION Frame、lease/fencing 与同一
可靠 RECONCILIATION_EVIDENCE；确认成功且得到 AVAILABLE 结果时写原 Attempt=SUCCEEDED
并转 MODEL_READY_AFTER_ACTION；确认 Effect 成功但结果缺失、非 canonical 或超限时写
SUCCEEDED + RESULT_REJECTED 并转 TERMINATED；确认失败时写 FAILED 并转 TERMINATED；
证据仍不能确认时保持原 UNKNOWN/WAITING_RECONCILIATION。它不得
调用 Describe、Prepare、Gateway/executor，不得签发 permit、创建新 Attempt 或替换
Proposal。数据库暂时不可写时进程 fail-stop，下一次成功打开先把遗留 PENDING 收口
UNKNOWN。

## 11. Usage

每次 ModelDispatchAttempt 对应一条规范化 UsageRecord，并保留 Provider 原始回执：

```text
input_tokens
cached_input_tokens
uncached_input_tokens
output_tokens
reasoning_tokens
provider
model
usage_status
raw_receipt
```

语义：

- 未提供的数值为 `UNKNOWN`，不得写成 0。
- 已知字段必须为非负整数；仅在三个输入字段均已知时验证
  `input = cached + uncached`。
- Provider 将 reasoning token 计入其他字段时，必须同时保留原始语义和规范化
  字段说明，不得重复计入。
- FAC2 基线的 Usage 只有 token 事实与 `usage_status`；不得引入 billing version、
  price snapshot、币种或任何金额字段。
- Usage 与模型终态在同一权威事务中提交；无法确认时保持待对账状态。
- Observer 只能聚合 Usage，不得修改 Ledger。
- Prompt 缓存优化只能调整稳定前缀与动态后缀；Run ID、Attempt ID、时间戳等不参与
  判断的动态元数据不得进入正文。缓存优化不能删除权限、来源或未完成动作。

Composite 不改变上述每 Run/Attempt 账本。`GetCompositeFamilyUsageProjection` 返回进程内
只读 DTO `CompositeFamilyUsageProjectionV1`，其成员为
`CompositeFamilyRunUsageFactV1`、`CompositeFamilyAttemptUsageFactV1` 与
`CompositeFamilyUsageAggregateV1`；它没有 canonical wire/schema，也不持久化第二套账本。
W5 Decision 投影按冻结
`initial Children → Reviewer0 → RepairChildren → Reviewer1 → Root` 的完整 `2N+3` 物理图
对同一 `model_usage` 权威事实聚合；dormant/skipped repair Run 必须保留且 `Attempt=nil`，
subset/all repair 中实际存在的 Attempt 必须纳入 aggregate 与 cap。未启用 Decision 的历史
family 继续按其冻结图投影。不得新增 family currency balance、把无 Attempt 或 NULL
当零、把 UNKNOWN slot 退款，也不得用 dispatch cap 冒充 token 额度。每个
token 字段只在所有已用 slot 对应值均已知时聚合，否则保持 UNKNOWN；
`CompositeFamilyUsageAggregateV1` 只有 `AttemptSlotsUsed` 与五个 token 合计，
不存在第二类金额聚合。

## 12. 恢复不变量

本节描述服务构造完成后的显式 Run continuation，不是 §9.4 的全局启动 safety
transaction。该 continuation 只能读取：

```text
RunManifest
MemberExecutionSnapshot
LoopFrame
History
ModelDispatchAttempt / 后续 DispatchAttempt
Usage Ledger
内容寻址配置与 Activation Record
可选 ModelProfile CONFIG
可选冻结 ActionDefinition、ACTION_PROPOSAL 与 ACTION_RESULT
可选 Root manifest 的完整初始/repair Children、Reviewer0/1、各 Run manifests/snapshots、
terminal results、Workspace Transfer payload/envelope、历史 grant 与 cancellation latch
```

静态上下文只能从冻结
`MemberExecutionSnapshot.PortPlans[].Bindings[].StaticContextRefs` 枚举；不得从
当前 Profile、provider-specific Config 或 Admission 临时参数重建恢复边。

恢复不得读取当前 Profile、Agent、Workspace、Catalog 或模块“最新版本”重新作
装配决策。

恢复顺序：

1. 校验 Store identity、ManifestDigest 和 MemberSnapshotDigest。
2. 获取新 lease epoch；旧 owner 的写入由 fencing 拒绝。
3. 校验冻结 Activation Record 引用的完整性，但不要求历史模块当前仍处于 Active。
4. 检查所有未终结 Model/Action Attempt，并证明每个 Run 至多一个未决项。
5. Composite Parent 必须按冻结顺序逐项验证 Child 的 ParentManifestDigest/slot、共同
   Task/Tenant/Workspace、terminal revision/result 和 family dispatch cap；不得枚举 current
   Control、重新 Admission、创建缺失 Child 或替换 UNKNOWN Child。
6. 对可确认结果完成原 Attempt；对无法确认结果写对应 UNKNOWN 并返回
   `WAITING_RECONCILIATION`。
7. 使用 Frame continuation 和上次权威事件位置继续，不重复已提交 History 或
   Usage。

恢复入口必须先用 Tenant + AdmissionKey 解析原 Run；不得在未找到响应或 owner
变化时分配替代 RunID。相同 AdmissionKey 的 intent digest 不一致时必须 fail-closed。

只有准备再次调用某个 Binding 时，Host 才校验当前 ArtifactDigest、Active 状态、
撤权、权限和健康状态。实时撤权、模块下线或权限收紧只能阻止受影响的下一次调用，
不能阻止读取已终止 Run，也不能改写已发布快照。损坏、摘要不匹配、下一步缺少
required Provider 或未知 continuation 版本必须 fail-closed。
对于 MCP，完整 artifact 树验证只能在显式选中 Adapter 后的 `Describe` 或 Gateway
授权的 `Execute` 边界执行；启动扫描、UNKNOWN 对账和 backup/restore 均不得触发。

## 13. Channel 最小纵链

### 13.1 单一可外挂 Port 与 Workspace 归属

首个 Channel 工作包只增加一个精确 Port：`channel.transport/v1`。它继续使用既有
Manifest、Install/Activate、RuntimeCatalog、PortPlan、Exact Registry、ConfigRef 与
AuthorityCeilingRef；不增加 Channel Runtime、Channel Store、第二套 Port 或品牌分支。

Channel Endpoint 属于 Workspace 权限边界，不属于 Agent 固有知识或角色。当前
`ControlSnapshot` 可以为一个 Workspace 冻结零到多个 Endpoint；每个 Endpoint 至少闭合
Tenant、Workspace、EndpointID、Channel、Account、Conversation、TargetAgent、
TargetProfile、CursorScope、启用状态和一个 `REQUIRED` Binding。一个精确
`tenant + channel + account + conversation` 最多匹配一个启用 Endpoint。Endpoint 的
Binding 在 Run 创建前只允许从同一 PublishedBasis 的 Workspace 定义和 Catalog 精确解析；
Admission 成功后，同一个 Binding 立即冻结进本次 Run 的 MemberExecutionSnapshot。
普通 Chat 不创建 Channel PortPlan，也不得读取 Endpoint、Identity、Cursor、Channel
Config/Authority、artifact 或 Adapter。

第一版只支持“接收一条文本消息并回复原会话”。主动推送、群发、跨 Workspace 发送、
附件、编辑/撤回、轮询 worker 和公网 Provider 留待独立工作包。这个限制属于首个 Adapter
能力边界，不改变 Port 或 Agent/Workspace 的自由组合模型。

### 13.2 入站、身份、去重与 Cursor

Adapter 只能把已认证 Provider 请求规范化为有界的
`ChannelInboundEnvelopeV1{EndpointID, ProviderEventID, ExternalUserID, Message,
ReplyTarget, CursorBefore, CursorAfter}`。Adapter 不得选择或覆盖 Tenant、Workspace、
Principal、Agent、Profile、权限、Effect、AdmissionKey、Binding 或 Provider 身份。

首个 first-party loopback Adapter 还要求 `ReplyTarget` 同时携带 `account_id` 与
`conversation_id`，并分别与所选 Workspace Endpoint 冻结的 `AccountID`、`ConversationID`
精确相等；S2 不保留未正式部署的缺字段兼容 wire。该检查在 Decode、纯 Prepare 与 Gateway
Execute 边界各执行一次；
错配在读取 Secret 或发送网络请求前关闭失败，不能把“回复原会话”扩成跨会话发送。

Core 使用以下稳定身份；时间戳、Cursor、Binding、Activation revision、RunID 和当前
Control/Catalog revision 均不得进入该键：

```text
IngressKey = SHA256(
  "freeagent.channel-ingress/v1\0" +
  CanonicalJSON({tenant_id, endpoint_id, provider_event_id})
)
AdmissionKey = "channel/v1/" + IngressKey
```

唯一入站事务必须在一个 `BEGIN IMMEDIATE` 内：

1. 重验 Endpoint 唯一路由、启用状态、当前 PublishedBasis 和 exact Binding；
2. 重验 exact identity、Workspace membership、ACL epoch 与 `channel.receive`；
3. 先按 IngressKey 去重；同 envelope digest 返回原 receipt/Run，不再调用模型，摘要不同
   则完整性冲突；
4. CAS 当前 Cursor revision 与 opaque `CursorBefore`；
5. 追加 ingress receipt；
6. 复用现有 Admission 事务体创建或解析同一个 Run、MemberSnapshot、Manifest、Frame0
   与 Event0；
7. 提交后才允许 Universal Loop 推进。

Cursor 与入站去重只使用一张 append-only revision 表。每个 scope 的 revision 0 只能在
Endpoint 关闭时由 Operator 显式 init/import；当前 Cursor 由最大 revision 行派生，不建
可变 head、队列或 worker。Core 把 Cursor 当有界 opaque canonical bytes，不解释其数值、
时间或字符串顺序。exact duplicate 不推进 Cursor；stale、断链、fork 或同 revision 不同
内容全部 fail-closed。已通过 Provider 认证、且 Core 能从当前 Workspace 证明不存在 active
identity 的毒消息，可以写有界 `REJECTED` receipt、保留 canonical envelope 以支持精确去重
与审计，并推进 Cursor；它不是 hash-only 捷径。签名无效、缺稳定 EventID 或 malformed 的
请求不得推进；当前 active identity 的消息也不能由调用方任意标记为 `REJECTED`。

已提交 `ACCEPTED` 的 exact duplicate 先返回原 receipt/Run。若原 Run 仍可开始新工作，恢复
前必须通过当前 Endpoint、Identity/ACL 与 Binding 的 deny-only 检查；若原 Run 已
`TERMINATED` 或 `WAITING_RECONCILIATION`，只读取原持久化结果，不因当前撤权改写历史去重
事实，也不得借 duplicate 重发或创建新 Run/Attempt。

### 13.3 出站复用唯一 DispatchAttempt 与 Gateway

不得创建 `channel_outbox` 或第二外部效果账本。现有 `dispatch_attempts` 增加
`dispatch_kind = ACTION | CHANNEL_SEND`；Outbox 只是
`dispatch_kind=CHANNEL_SEND AND state IN (PENDING, UNKNOWN)` 的查询投影。
Action 字段与 Channel 字段按 kind 互斥闭合，但共同复用 logical operation key、冻结
Binding、Effect、deadline、result/receipt/evidence、revision 和
`PENDING -> SUCCEEDED | FAILED | UNKNOWN` 状态机。

成功的最终 ModelOutcome 与唯一 `CHANNEL_SEND_PROPOSAL`、`CHANNEL_SEND/PENDING`
DispatchAttempt、`CHANNEL_PENDING` Frame、Event 和一次性 permit 必须在同一事务提交；
提交前 Gateway 调用次数必须为零。Proposal 冻结 Endpoint、IngressKey、ReplyTarget、
Assistant 正文摘要和 prepared payload；Channel logical operation key 只使用
RunID、MemberID 与 LogicalStepID，不能把可改写的 Binding、目标或 ProposalDigest 加入
key 来绕过 UNKNOWN。

`CHANNEL_PENDING` 仍是同一个 Universal Loop 的 continuation，以
`attempt_kind=CHANNEL` 复用 `pending_dispatch_attempt_id`；Store 中对应的共享账本
分类是 `dispatch_kind=CHANNEL_SEND`，二者不得混淆或从 ID 前缀推断。不存在 Channel
Loop。现有唯一 Gateway 按
`dispatch_kind` 取得私有 ActionExecutor 或 ChannelExecutor，并在执行前重验
Frame/Attempt、fencing、MemberSnapshot 中 exact Binding、Ingress/Endpoint/目标、当前
deny-only 撤权、`channel.send`、Authority、`irreversible_write`、deadline、预算和
Proposal digest。模型、Skill、SDK 和公开 Module Port 均不能取得 executor。

Channel Adapter 对同一 Attempt 只能发出一次冻结请求，不得自行 retry、fallback、换
Endpoint、换 Binding 或生成替代 Attempt。HTTP POST 必须禁用自动 redirect；任何 3xx
不得跟随。只有协议能可靠证明未投递时才可写 FAILED；进入发送边界后的 timeout、EOF、
连接中断、redirect、回执身份不符、请求可能已写出、Adapter 普通 error 或效果后终态
持久化失败都保守写原 Attempt UNKNOWN。UNKNOWN 只能查询/人工对账原 Attempt，永不回
PENDING，也不得通过新 Run 或新 logical step 语义重放。

Channel SUCCEEDED 后 Run 才 TERMINATED；FAILED 以明确交付失败终止并保留已生成回答；
UNKNOWN 进入 WAITING_RECONCILIATION。启动 Store-only safety transaction 把遗留
CHANNEL_SEND/PENDING 原位 CAS 为 UNKNOWN，不加载 Adapter、Config、Authority、Secret
或 artifact，也不发送网络请求。

### 13.4 默认关闭、真实 Adapter、关停与完整备份

首个真实 Adapter 固定为 first-party `loopback-http` 文本 Channel，仅允许字面量
IPv4/IPv6 loopback endpoint，并且必须由显式 Channel 启用参数与已发布启用 Endpoint
双重授权。显式生产 composition 必须指定唯一 Tenant、Workspace 与 Endpoint；构造
Adapter 前还必须证明 Endpoint 启用且精确绑定、匹配 Cursor 已由 Operator 初始化、没有
未对账 Channel UNKNOWN，并通过同一 Store 的 semantic closure。不得自动选择 Endpoint、
自动初始化 Cursor 或自动对账。

普通 `chat` 和默认 `serve` 不启动 Channel handler/worker、不解析 Secret、不加载 Channel
artifact，也不在 Admission/单 Run 热路径读取 Channel 表或发送网络请求；§9.4 的进程级共享
账本扫描仍是唯一例外。Secret 只通过运行时 SecretRef resolver 注入，禁止持久化明文或写入
日志/诊断。

关停顺序固定为：停止新 ingress 和新 permit -> 等待 in-flight Gateway/权威终态提交 ->
有界 grace 后取消 -> 在 handler 全部退出后、Store 关闭前，以脱离已取消 lifecycle 且
自身有界的同一 Store-only safety routine 将不能确认的原 PENDING 原位收口为 UNKNOWN ->
再关闭 Store。该 routine 不加载 Adapter、Endpoint、Secret 或 artifact，不发送网络请求；
不得为了关停而重发。

完整备份继续使用同一个 FAC1 SQLite 与 artifact bundle，必须包含全部 ingress/cursor
revision、ACCEPTED/REJECTED 去重事实、Channel-origin Admission/Run、冻结 PortBinding、
Proposal、receipt/evidence 及同一 dispatch ledger 中的 PENDING/UNKNOWN。Verifier 必须
证明每个 Cursor scope 从 revision 0 连续、before/after 精确衔接、Ingress/Event 唯一、
ACCEPTED receipt 闭合 Run/Workspace/Principal/Binding、REJECTED 不引用 Run、Channel
DispatchAttempt 闭合最终 Model Attempt/Ingress/Endpoint/Proposal/Frame。Restore 不加载
Adapter、不监听、不调用 Gateway；恢复后 Channel 保持 disabled，直到 Operator 对账
UNKNOWN 并显式重新启用。

### 13.5 W2-E3 Operator Apply 与多 Workspace 隔离

`W2_E3_WORKSPACE_CHANNEL_APPLY_ACCEPTED_DEVELOPMENT_SLICE` 将本节 first-party loopback Provider 接入
§7.8.6 的统一 Operator Apply，但不扩大 Channel 协议范围。Binding target 必须是
`WORKSPACE_CHANNEL_ENDPOINT`；`ENABLED` 原子发布 Control/Catalog 与 revision-0 Cursor seed，Dry-run
零 Store 写入、零 Secret 解析、零网络。Disable 删除当前 Endpoint 而不删除 Cursor、Attempt、历史
Control/Catalog、Module History 或 evidence，共享 Instance 只在最后一个 current 引用消失时退出
Catalog。

集中验收 `TestW2E3ChannelMultiWorkspaceIsolationBackupV1` 以同一 Agent/Profile 和同一共享 Channel
Instance 绑定两个 Workspace/Endpoint，证明 Cursor、Run、receipt、终态和 Module History 不串流；
成功与 Channel `UNKNOWN` 的 exact duplicate 均不二次发送，一个 Endpoint 的未对账 `UNKNOWN` 不
阻塞另一个重新 composition 与运行。Backup/Restore 保存 5 条 ingress receipt、2 个 Cursor scope、
1 个 Channel `UNKNOWN` 和逐字节一致的历史 Binding 投影；恢复后 A 从 Cursor revision 2 推进至 3，
B 保持 revision 1。该证据未单独构造 `MODEL_UNKNOWN`，不声明公网 Channel、任意第三方进程内代码、
E4/E5、Beta、生产或 `RELEASE_READY` 完成。

### 13.6 W2-E4 DeepSeek Model 显式替换

`W2_E4_DEEPSEEK_MODEL_REPLACEMENT_ACCEPTED_DEVELOPMENT_SLICE / W2_E5_NEXT` 只在统一
Operator Apply 中增加第七个 exact handler tuple：
`model.generate/v2 + TRUSTED_IN_PROCESS/go-in-process/v1 + model-binding-config/v2`。它只接受
Core 内建的 exact DeepSeek artifact/adapter，保留同一 provider Instance，并且只允许在
`deepseek-v4-flash` 与 `deepseek-v4-pro` 之间显式替换。该状态不是通用 Model Provider
插件、任意进程内代码准入、自动选模或动态路由。

`module-apply-plan/v1` 对目标 Profile 的 Model Binding 表达完整期望状态：Apply 使用
Control/Catalog CAS 原子替换 Config、Authority 和可选 ModelProfile；省略
`model_profile` 表示清除旧 Profile，而不是隐式沿用。新 Profile 必须精确匹配替换后的
provider/model/build/config/artifact/adapter，且仍只能收紧 ContextPolicy。`DISABLED`
对 Model Port 明确禁止；本切片只有显式 Flash↔Pro replacement/rollback。

新 Apply 的 Authority 必须是 strict `model-authority-ceiling/v1`，绑定 exact Tenant、
DeepSeek provider、SecretRef 和官方 endpoint 允许位。Operator 只在本地命令边界提供与
Authority 相同的 transient SecretRef grant；grant 不进入 Plan、Store 或输出。dispatch 时
Authority SecretRef、冻结 Config 和本地 resolver identity 任一不匹配，都在 credential
lookup 和 HTTP 之前 fail-closed。

替换只影响之后的新 Run；
旧 Run 继续使用已冻结 MemberSnapshot/Config/Authority/ModelProfile。原
`MODEL_UNKNOWN` 不因替换、回滚或 exact retry 产生新 Attempt，也不 fallback 到另一模型或
语义重放。

主要集中验收为
`TestModuleApplyDeepSeekModelReplacementDryRunRetryRollbackAndOldRunFreeze`、
`TestModuleApplyDeepSeekModelPreflightFailsClosedBeforeMutation`、
`TestModuleApplyDeepSeekModelProfileBackupRestoreExactClosure`、
`TestModuleApplyDeepSeekModelProfileCanBeClearedByExplicitReplacement`、
`TestModuleApplyDeepSeekModelNewRunUsageAndUnknownNeverSubstitute` 和
`TestModuleApplyDeepSeekModelSameAgentDifferentWorkspaceProfiles`。聚焦 E4 命令、完整
`cmd/freeagent`、`internal/coreloop` 与 DeepSeek/ModuleHost/LocalChat/ChannelService 受影响包均已通过。
该验收没有新增 Store Schema/migration、表、Runtime、Loop、Gateway 或账本；下一唯一状态是
`W2_E5_NEXT`。

### 13.7 W2-E5-A Knowledge Requires 与 permission grant

E5-A 验收时状态为
`W2_E5_A_REQUIRES_PERMISSION_GRANT_ACCEPTED_DEVELOPMENT_SLICE / W2_E5_B_NEXT`。E5-A 不增加
handler 或 Port；它只把既有 Knowledge handler 的 Manifest 形状从 immutable legacy 扩为两个
exact 分支。共享 `moduleapi` classifier 接受 legacy 的零 Requires/零 permission，或 governed 的
唯一 `requires:[model.generate/v2]` 与唯一 `requested_permissions:[knowledge.read]`；任一半声明、
重复、额外值或重排均 fail-closed。Apply、Dry-run、生产加载、Current Store publication、exact
retry、公开 Verify 和 Backup/Restore 必须复用该 classifier，不能维护各自的近似判定。

governed Knowledge 仍只提供 `context.provide/v1`，并继续由固定内建只读 Adapter 执行。它的
Require 必须在同一 target Profile 中解析为唯一 exact Model Binding；跨 Profile、缺失或多个匹配
全部拒绝。解析必须沿同一权威链验证
`Binding → Catalog → exact Activation → Installation → canonical Manifest`，并闭合 exact Port、
module identity、artifact digest、runtime request 与 Provides。Require 只冻结结构依赖，不新增
模型调用、路由、fallback、第二 Assembly graph 或第二 Runtime。

`knowledge.read` 是不可信 Manifest 的请求，不是授权。Core 只在 Knowledge Config 与 Authority
source 相同、Tenant 相同、Workspace/Agent scope 均属于当前 Control，且 Binding/Authority 的
读取上限求交后授予有效访问。该 grant 从 publication facts 派生，不新增 grant Store、表或模型
可见的授权句柄；Manifest、Plan、Provider 和模型均不能自行扩大权限。

Apply 的 Store reader 与 Dry-run 的 immutable observer 在任何 `ALREADY_APPLIED`/`NO_CHANGE`
fast-path，以及 TEMP/staging、artifact publication 或 Store mutation 前，必须先调用完整 current
publication closure。损坏的 Binding/Catalog/Activation/Installation/Manifest、Requires、permission
或既有 Config/Authority closure 一律在新效果前拒绝。Backup/Restore 重放同一闭包；恢复不加载
Provider、不调用模型/网络/Secret，Disable 不改写旧 Run 或 immutable module history。

UNKNOWN 合同没有变化：Apply 的 outcome unknown 仍只属于 Operator publication；原
Model/Action/Channel UNKNOWN 只能对账原 Attempt，不能语义重放、切换 Provider 或创建替代
Attempt。E5-A 验收没有真实 API 调用。Windows `go test ./...` 的 `30` 个仓内 Go package 在
`221.9s` 内通过，另有 `1` 个 external compatibility package 由 `sdk/moduleapi` 嵌套测试编译验证；
`go vet ./...`、`go mod verify`、gofmt、Docs（`38`）、Capability Matrix（`49` 项/`0 stable`）、
License（`35` 个 Go dependency/`57` 个 distributed asset）、Branding 与 PublicTree 门禁均通过。
本切片没有新增 schema、migration、表、Port、账本或第二 Runtime/Store/Loop/Gateway。

E5-A 不是完整 E5，不开放通用 multi-Port provider/consumer、多个 ProviderBinding 合并、
REMOTE/WASM、任意第三方或不可信包内代码、自动发现或在线控制面。下一唯一状态是
`W2_E5_B_NEXT`。

### 13.8 W2-E5-B Document Insight 双 Port 执行记录

当前状态为
`W2_E5_B_DOCUMENT_INSIGHT_DUAL_PORT_ACCEPTED_DEVELOPMENT_SLICE`。E5-B 只验收一个受信、编译进
Core 的固定产品模块：`freeagent.builtin.document-insight@1.0.0`，ArtifactDigest 为
`838ff9ddd45186f0cdb26021d16902b2bfd7581c2dc0d7b72014cf4f48d0f7ea`，AdapterIdentity 为
`freeagent.adapter.document-insight/v1`。它的 Manifest 必须逐项闭合有序
`provides:[action.provider/v1, context.provide/v1]`、唯一 `requires:[model.generate/v2]`、唯一
`requested_permissions:[knowledge.read]` 与
`TRUSTED_IN_PROCESS/go-in-process/v1`；任一部分形状、顺序或 exact identity 漂移都 fail-closed。

Core-owned 通用 handler table 仍恰好包含 `7` 个互异 protocol tuple；protocol tuple 只由 exact Port、
请求的 RuntimeMode、RuntimeProtocol 与 consumer schema 确定。E5-B 不新增第八、第九个 protocol
tuple，也不向既有 tuple 塞入模糊覆盖项；分派器只在通用 tuple 选择前增加 `2` 个同时检查
ModuleID/ExactVersion/ArtifactDigest 的 Document Insight reserved exact selector：

```text
context.provide/v1
  + TRUSTED_IN_PROCESS/go-in-process/v1
  + knowledge-context-binding/v1
  + exact Document Insight module/version/digest

action.provider/v1
  + TRUSTED_IN_PROCESS/go-in-process/v1
  + action-binding-config/v1
  + exact Document Insight module/version/digest
```

reserved selector 命中产品 ModuleID 后，错误的 version、digest、Port、runtime 或 consumer schema
不得回落到 generic Knowledge 或 `text.stats` handler。既有 `7` 个 tuple 的解析顺序与行为保持不变，
也没有新增运行期注册、按名称发现、版本搜索或 fallback。

同一已冻结 Member 中，Context PortPlan 与 Action PortPlan 里由 Document Insight 贡献的两个 Binding
必须引用完全相同的 `ActivatedModuleRef`：ModuleID、Version、ArtifactDigest、InstanceID、
ExecutionClass、AdapterIdentity 与 ActivationRevision 七个字段全部相同；Context PortPlan 另保留
既有 `context.basic` Binding。一次新 Run 的 model-1 在同一个 canonical
`ContextCompilationV1` 中同时冻结真实 Knowledge retrieval 与非空 `ActionResultReservationV1`；
reservation 必须由该 Member 的 FrozenActionDefinition 重算，且预算闭包固定为：

```text
Estimate(model-1 request) + reservation.EstimatedTokens
  = OriginalEstimateTokens
  = FinalEstimateTokens

Estimate(model-2 request) <= FinalEstimateTokens
```

因此 RAG 与 Action 结果空间共用唯一 Context Compiler、85% watermark 与有序 Drop 规则；model-2
只从已持久化 model-1 request 追加唯一隔离的 untrusted Action result，不建立第二 compiler 或第二
上下文事实源。`MaxEnvelopeBytes` 是完整 Action result message content 的上界，
`EstimatedTokens` 使用既有 `canonical-json-utf8-byte-upper-bound/v1` 估算器覆盖序列化 message 与追加
逗号的保守增量，不代表 Provider 报告的真实 tokenizer token。

Document Insight 的公共 Context 调用由同一 artifact-scoped implementation 提供；Action 侧只公开
Describe/Prepare。generic `ModuleInvoker.Invoke` 对 Action Port 保持拒绝，私有 `ExecutePrepared` 只能由
既有共享 Gateway 持有效 permit 调用。Action DispatchAttempt 必须保存并复核与两个 Document
Insight Binding 完整相同的 `ActivatedModuleRef`，proposal/result/model-2 继续闭合同一
Run、MemberSnapshot、Binding、Definition
与 source Attempt。UNKNOWN、exact retry 和外部效果规则没有例外：UNKNOWN 只对账原 Attempt，禁止
语义重放、换 Provider/Binding 或创建替代 Attempt。

E5-B 集中产品验收从 Pure Chat 开始，经 Context-first 与 Action-second 两步 Apply，在同一 Run 中实际
完成一次 RAG、一次 Gateway `text.stats`、两个 Model Attempt 和一个 Action Attempt；exact Chat retry
新增 Run/Model/Action Attempt 均为零。Backup/Verify/Restore 后，RAG request/compilation、完整 Action
chain 与 MemberExecutionSnapshot canonical bytes 不变，恢复后的新 Run 再次消费双 Port；按正确顺序
Disable 后最终回到零可选模块加载的 Pure Chat。

实际门禁记录为：

```text
focused TestW2E5BDocumentInsightProductAcceptanceV1 -count=3 = PASS
go test -count=1 ./cmd/freeagent = PASS (package 185.746s; wall 186.968s)
go test -count=1 ./... -timeout 25m = PASS (30 repository packages, wall 217.539s;
  cmd/freeagent package 215.004s; 1 external compatibility package compiled by nested SDK test)
go vet ./... = PASS
go mod verify = PASS
gofmt -l . = PASS (empty output)
Docs = PASS (38 Markdown)
Capability Matrix = PASS (49 items / 0 stable)
License = PASS (35 Go dependencies / 59 distributed assets)
Branding = PASS
PublicTree = PASS
real API calls = 0
```

该 accepted 状态只证明上述固定、受信、编译进 Core 的双 Port 模块及其真实消费者纵链；它不代表
任意第三方进程内代码、通用 multi-Port 装配、同一 Port 多个 ProviderBinding、REMOTE/WASM、自动
发现、在线控制面、生产部署、公开 Beta 或 `RELEASE_READY` 已完成。

## 14. S1 与 S2 边界

### 14.1 S1 必须实现

- 新 `cmd/freeagent` 生产入口。
- 本规格唯一 Assembly Compiler 和 Universal Loop。
- MemberExecutionSnapshot、RunManifest、LoopFrame。
- 最小 RuntimeCatalog、JSON seed 和 Install/Activate/Bind。
- `DECLARATIVE` 与 allowlist `TRUSTED_IN_PROCESS` Host。
- Pure Chat，以及通过 PortPlan 接入的 Role/静态 Skill。
- `model.generate/v2`、ModelDispatchAttempt、MODEL_UNKNOWN、History 和 Usage。
- 只依赖 Current Store 的 CLI/HTTP 纵向链路。

S1 外部 Channel 必须断开。RAG、Memory、Action、MCP、多 Agent、Scheduler、
Learning 和在线 Control Plane 不注册；显式请求必须失败。

### 14.2 S2 逐项扩展

S2 按独立纵向工作包依次增加：

1. Context Compiler（已验收）。
2. ModelProfile（已验收）。
3. RAG（已验收）。
4. Memory（已验收）。
5. Action Describe/Prepare、Gateway、DispatchAttempt 和一个 Built-in Tool
   （已验收）。
6. MCP `LOCAL_PROCESS + stdio + Tool-only` Action Adapter（已验收窄开发切片；固定
   MCP `2025-11-25` + 官方 Go SDK `v1.6.0`，不代表完整 MCP 或生产就绪）。
7. Channel ingress、Cursor 与复用共享 DispatchAttempt/Gateway 的回复原会话纵链
   （默认关闭开发切片已验收；不存在 `channel_outbox`，Outbox 仅为账本查询投影）。
8. Parent/Child Run 与复合 Agent（第一实现切片已验收：同 Workspace、depth 1、2..8 Child、`ALL_REQUIRED`）。
9. 公平 Scheduler（S3-A 已验收）与单次 Reviewer 审核门（S3-B 已验收）；动态冲突聚合
   与返工循环延后。
10. Learning PR、Skill Draft、周期更新和在线 Control Plane。
11. REMOTE Action Host（W2-R1 已验收单一
    `action.provider/v1 + REMOTE/freeagent-action-http/v1`；其他 REMOTE 延后）。
12. WASM Action Host（W2-R2 已验收单一
    `action.provider/v1 + WASM/freeagent-action-wasm/v1`；其他 WASM、WASI、Host capability 与生产级
    不可信多租户隔离延后）。

每个工作包必须复用本规格的 Assembly Compiler、Universal Loop、PortPlan、
Module Protocol 和 Current Store；必须有真实生产消费者、安全负例和崩溃恢复
测试。不得预建第二套 Runtime、Store、装配器或未接线证明链。

## 15. S1 基线及 S2 Context、ModelProfile、RAG、Memory、Action、窄 MCP 与 Channel 验收

S1 与已验收 S2 开发切片保持满足以下条件；37–45 对应 Action，46–51 对应窄 MCP，
52–60 对应默认关闭 Channel：

1. 旧源码和旧数据库离线归档可读。
2. JSON seed 能重建最小 Agent、Workspace、Profile 和 RuntimeCatalog。
3. 生产只有一个 Assembly Compiler、一个 Universal Loop 和一个可写 Current Store。
4. `CLI/HTTP → Assembly → Manifest → Loop → Model → Store` 真实全链路通过。
5. 生产入口不再构造或调用旧 `runtime.Runtime.ProcessOne/DeliverOne`。
6. 生产依赖闭包不包含旧 Store、旧 migration、Legacy execution gate 或未接线
   evidence/shadow 链。
7. 同一编译输入产生字节一致的 PortPlan、Snapshot 和摘要。
8. 重复 PortPlan、空 PortPlan、required 缺失和非法 ExecutionClass 均 fail-closed。
9. Runtime/Host 不重排 Binding，不按名称或“最新版本”重新选择 Provider。
10. Pure Chat 的 Admission 与单 Run 热路径中，Skill、RAG、Memory、Action、MCP、Channel
    可选仓库及外部效果 ledger 读取次数均为零；唯一例外是进程开放流量前一次全局共享
    安全账本扫描，它不加载 Action/Channel Provider、定义、Endpoint、Identity/ACL、Secret、
    artifact、Config 或 Authority，也不产生外部效果。
11. Role/静态 Skill 只能通过 `context.provide/v1` 生效。
12. 未实现能力返回 `CAPABILITY_NOT_AVAILABLE`，没有静默 Pure Chat 降级。
13. Manifest 不能自授权；相同 ID+Version 的不同 ArtifactDigest 被拒绝。
14. ModelDispatchAttempt 在调用前落盘；同一逻辑步骤只有一个 Attempt；
    MODEL_UNKNOWN 不重放、不切换 Binding，自动重入也不能新建 Run 绕过。
15. Usage 的 input/cache/output/reasoning 缺失值保持 UNKNOWN，原始回执可追溯。
16. 在模型调用前、调用后、终态事务前后注入崩溃，均能按本规格恢复或进入对账。
17. lease/CAS/fencing 阻止双 owner 推进同一 LoopFrame。
18. Current Store 的备份、恢复和 identity/fingerprint 校验通过。
19. 新进程拒绝旧 Store identity；所有 writer 只写 Current Store。
20. 外部效果 PENDING/UNKNOWN 未清零或未对账时拒绝切换。
21. 外部 Channel 在整个 S1 保持断开。
22. 无 ModelProfile 时成员 canonical wire 不出现 `model_profile`，画像仓库读取为零，
    Pure Chat 请求字节与显式 ContextPolicy 行为不变；全体模型 Binding 因本次发布前
    必填 `model_build_id` 修订发生的 CONFIG digest 变化不冒充兼容旧 wire。
23. 带画像成员只从冻结 CONFIG 闭包恢复；provider/model/build/config/artifact/adapter
    任一不匹配都在 Run 或 Attempt/permit 写入前 fail-closed。
24. ModelProfile 只能收紧 ContextPolicy；较大上限不扩张，较小上限参与 85%/100%
    编译门禁，缺失所需 compilation 时不得绕过 Store 边界。
25. 重启和完整 backup/restore 后 ModelProfile ref 与 canonical CONFIG 字节不变，
    恢复不读取 current Control、Provider 最新元数据或第二事实源。
26. 动态 RAG 只复用 `context.provide/v1`；Binding 必须为 REQUIRED、
    `TRUSTED_IN_PROCESS` 且无静态引用，Config/Authority/source 与 Tenant、Workspace、
    Agent、Task 四级 scope 在模型 Attempt 前闭合。
27. RAG 按冻结 BindingIndex 精确调用一次；合法零命中与低于 85% 的命中都持久化
    retrieval evidence，恶意正文只能进入固定前缀的 canonical USER 数据封套。
28. 新 Attempt 必须由同一纯 Context Compiler 对完整 MODEL_REQUEST 与 compilation
    逐字节证明；篡改 source/chunk/config/authority/request 在 Adapter 或模型调用前
    fail-closed。
29. 已完成启动 safety transaction 后的显式 Run continuation 可验证原 canonical
    bytes、digest 与冻结 source 闭包，但不重新运行 Compiler、RAG Provider 或模型
    语义步骤；全局启动本身只读最小 Store ledger，绝不读取 artifact/Config/Authority。
30. 当前 Catalog 只参与实际调用前的 deny-only 撤权；默认无 RAG Profile 即使同一
    Catalog 含 RAG，也不读取知识 artifact、不解析知识 Adapter、不调用知识 Host。
31. Memory 与 Knowledge 只按冻结 Config `Parameters.schema_version` 经同一
    `context.provide/v1` 分派；首片最多一个 Memory Binding，ModuleID/品牌不能成为
    Runtime 分支。
32. 新 Memory Attempt 在 Begin 事务内证明 evidence snapshot 仍为当前 Agent head；
    此后 retry、PENDING、UNKNOWN、重启和 backup/restore 只读取冻结 revision，不能
    重筛 TTL、重调 Provider 或漂移到新 head。
33. Memory exact Tenant/Agent/Workspace、kind、TTL 和 limits 在 Attempt/permit 前闭合；
    `*` 不能作为实际 Agent/Workspace 身份，正文只进入不可信 USER envelope，计数不
    替代或关闭 RAG。
34. 首次 SUCCEEDED 在同一终态事务内以提交时最新 head 追加唯一 revision；FAILED/
    UNKNOWN 不追加，终态重入只验证原 revision，两个并发成功均重基且不丢贡献。
35. 默认 Pure Chat 不读取 Memory Store/Config/Authority/artifact/Registry/Host，也不
    运行更新脚本；显式启用但缺 genesis、Provider 或权限时 fail-closed。
36. 完整备份恢复逐字节保留 Memory revision/snapshot/head，并拒绝断裂 parent、错
    owner/source、伪造摘要和计数；生产 Store 不暴露终态事务之外的 revision append API。
37. Action Definition 只在显式 Action Profile 的 Admission 前按冻结 Binding 调用
    Describe；成员内 PublicActionID 冲突、缺本地 alias/Effect/结果上限或 Config/
    Authority 越权均零 Run 写入失败。默认 Pure Chat Admission 与单 Run 热路径对
    Action artifact/Registry/Host/Store/Gateway 全零；启动时 Store-only ledger safety
    transaction 单独计量且不加载任何可选模块内容。
38. 无 Action 时模型请求逐字节不变；有 Action 时只暴露 PublicActionID/Description/
    InputSchema，模型
    输入、DefinitionDigest、MemberSnapshotDigest 或冻结 Binding 映射被替换时均在
    Prepare 或执行前 fail-closed。
39. Describe/Prepare 无写 Effect；Prepare payload 只作为 Core 生成 Proposal 的输入，
    Provider 不能注入快照、路由、Effect、Attempt 或 permit。
40. 模型一 SUCCEEDED、Usage、post-model UsageLedgerRef、ACTION_PROPOSAL、Action
    PENDING、Frame 和 Event 在单一事务提交；该事务失败时 executor 零调用，permit
    并发消费最多成功一次；串行事务与冻结状态机证明首片每 Run/Member 最多一个 Action。
41. Gateway 是私有 executor 的唯一调用者；PENDING 先于任何 Effect，撤权、Authority、
    Effect、预算、lease 或 expiry 失败均零外部效果，ActionID 解析后无 fallback。
42. Action SUCCEEDED 只从冻结模型一请求追加不可信 ACTION_RESULT 后执行模型二；
    模型一已逐 Action 对完整最坏 envelope 预留同一估算器预算，结果超限不截断且以
    RESULT_REJECTED 保留已确认 Effect、明确终止；只有 AVAILABLE 才进入模型二。
    模型二
    RequestDigest 必须闭合原请求前缀、结果后缀和 source Model/Action Attempt，不重新
    Describe、Prepare、RAG 或 Memory，第二次 Action 请求明确失败且不创建替代
    DispatchAttempt。
43. Action 普通 error、效果后终态事务失败与进程 kill 均以 Store-only 启动事务把原
    PENDING CAS 为 UNKNOWN；重启、自动重入和 backup/restore 不加载 Provider/定义/
    artifact，也不执行 Prepare/Gateway/executor 或更换 Binding/Proposal/Run。
44. Action FAILED/UNKNOWN 不更新 Memory；UNKNOWN 仅由
    ReconcileActionDispatchOutcome CAS 原 Attempt 与 WAITING_RECONCILIATION Frame；
    仅模型二最终成功与既有 History/Usage/Memory
    在权威事务闭合。终态读取按 continuation 指向的 AttemptKind/AttemptID，不把模型一
    的 Action 请求伪装成最终回答。
45. `text.stats` 真实 production composition 纵链，以及隔离临时目录中的 Effect 故障
    注入、Windows/WSL/race、完整 backup/restore 与长链测试全部通过；阶段测试缓存、
    临时数据库和 effect sandbox 在取证后删除，已声明 Legacy/Temp/snapshot/evidence
    资产只读保留。
46. MCP 只作为 `action.provider/v1` 的 `LOCAL_PROCESS + stdio + Tool-only` Adapter；
    协议固定 `2025-11-25`、官方 Go SDK 固定 `v1.6.0`，不新增 MCP Runtime、Store、
    Ledger、Port、session pool 或发现服务。
47. 只有显式 Operator exact ArtifactDigest grant 和显式 MCP Action Binding 才能加载；
    `Describe/Execute` 各自在启动进程前有界验证完整 artifact。Pure Chat 与 Store-only
    启动事务对 MCP artifact、Config、Authority、Registry、SDK 和进程访问均为零。
48. Describe 的握手、分页、工具数/定义总量/帧/进程均有界；Prepare 是无进程、无 Store
    的纯函数；只有 Gateway 可消费原 PENDING permit 并发出一次 `tools/call`，无 SDK
    retry、fallback、替代进程或同 Attempt 第二次调用。
49. 精确 server wire JSON-RPC error 与合法 `isError=true` 是确定 FAILED；写出后 timeout、
    EOF、进程崩溃、本地 client-closing（包括 `-32003`）及其他无法证明未执行的结果均
    写原 UNKNOWN，不以关闭、取消或 kill 证明“未执行”。
50. 受管进程树在 deadline/取消/关闭后有界终止并回收；完整 backup/verify/restore 保留
    原 DispatchAttempt、Proposal、result/receipt/evidence，UNKNOWN 不 initialize/list/
    call、不重放且不生成替代 Attempt。
51. 当前验收仅覆盖 Operator 显式批准且完全信任的本地 stdio Tool 开发切片；精确摘要、
    空 env 和受管进程树不是 OS 文件系统/网络 sandbox。REMOTE/Streamable HTTP/SSE、
    Resource、Prompt、Secret/SecretRef、Sampling、Elicitation、Roots、Tasks、常驻 pool、
    动态订阅、auto-discovery、不可信第三方隔离和生产级部署继续 fail-closed，并需后续
    独立合同与门禁。
52. `channel.transport/v1` 只作为 Workspace Endpoint 的可选单 Binding Port；显式启用时
    必须恰有一个 `REQUIRED + TRUSTED_IN_PROCESS` Binding，普通 Agent/Profile 与 Pure
    Chat 不获得 Channel 固有能力，也不创建空 PortPlan。
53. 入站认证后，Endpoint/Identity/ACL/Binding、IngressKey 去重、append-only Cursor CAS、
    receipt 与 Run Admission 在一个事务闭合；exact duplicate 复用原 Run，断链/fork 与
    同 key 不同摘要 fail-closed，不能借重复请求重发。
54. Universal Loop 同时闭合 `model -> CHANNEL_SEND` 与
    `model-1 -> Action -> model-2 -> CHANNEL_SEND`；`CHANNEL_PENDING` 只以
    `attempt_kind=CHANNEL` 指向同一 `pending_dispatch_attempt_id`，不存在 Channel Loop。
55. Channel 只复用现有 `dispatch_attempts` 与 Gateway；模型终态、History、Proposal、
    `CHANNEL_SEND/PENDING`、Frame/Event 和一次性 permit 原子提交，提交前 executor 零调用，
    不存在 `channel_outbox`、第二外部效果账本或第二 Store。
56. first-party `loopback-http` 只允许字面量 loopback Endpoint；ReplyTarget 的
    `account_id + conversation_id` 在 Decode、Prepare 与 Execute 都必须精确匹配冻结
    Endpoint。Adapter 不 retry、不 fallback、不跟随 POST redirect，也不生成替代 Attempt；
    发送边界后的歧义结果保守写原 UNKNOWN。
57. 启动与关停都使用同一 Store-only PENDING-to-UNKNOWN safety routine；关停先停止新
    admission/permit 并排空已接纳 handler，再以有界、脱离已取消 lifecycle 的上下文收口，
    全程不加载 Channel 可选资源、不调用 Adapter/Gateway 且零网络。
58. Channel 默认关闭；只有显式启用参数、已发布启用 Endpoint、已初始化匹配 Cursor、
    无未对账 Channel UNKNOWN 且 Store semantic closure 通过时才构造首个 Adapter。
    默认 `chat/serve` 与 Pure Chat 对 Endpoint、Identity/ACL、Cursor、Config、Authority、
    Secret、artifact、Adapter 和网络访问均为零；唯一例外是开放流量前的共享账本扫描。
59. 完整 backup/verify/restore 保留并验证 ingress/cursor、Run/Frame/Event/History、最终
    Model、Proposal、`CHANNEL_SEND` 状态及 receipt/evidence 的双向语义闭包；restore 不
    构造 Adapter、不发送网络，并保持 Channel disabled。
60. 当前验收只覆盖默认关闭、Operator 显式装配的 first-party loopback 文本纵链；主动
    推送、群发、附件、跨 Workspace 发送、公网 Provider 与生产级部署继续 fail-closed，
    必须作为后续独立工作包，而不能扩张本开发切片的权限或绕过 Workspace 边界。

### 15.1 Parent/Child + Composite 第一实现切片验收

状态：`S2_PARENT_CHILD_COMPOSITE_ACCEPTED_DEVELOPMENT_SLICE`。本裁决来自当前源码树的
真实 production composition、故障注入、恢复、完整备份、Windows/Linux 全仓测试、
Linux Race 与六平台构建；不是由文档或测试名自行推导。

| 门禁 | 当前证据 |
|---|---|
| 原子 Admission、幂等与并发隔离 | `TestCommitCompositeRunFamilyPublishesProjectionAndIsIdempotent`、`TestCommitCompositeRunFamilyRollsBackEveryAuthoritativeWritePoint`、`TestCompositeAdmissionConcurrentIdentityAndFamilyIsolation` |
| 身份、单向 digest 图与能力拒绝 | `TestCompileCompositeFamilyIsDeterministicAndClosesParentChildren`、`TestCompositeChildIdentityDerivationGolden`、`TestCompositeAdmissionRejectsOptionalMutableAndEffectCapabilitiesWithoutWrites` |
| `ALL_REQUIRED`、attempt-free 失败与单次 merge | `TestCompositeRootFailedChildTerminatesAheadOfPendingWithoutModelAttempt`、`TestCompositeRootChildResultOverBudgetTerminatesBeforeModelPermit`、`TestCompositeRootMergesOnceWithoutCopyingChildResultsIntoHistory` |
| assignment、结果污染隔离与 50% 分配 | `TestCompileV1CompositeChildInjectsProtectedAssignmentOnly`、`TestCompileV1CompositeRootInjectsOrderedBudgetedUntrustedResults`、`TestContextCompilationV1CompositeEvidenceFailsClosed` |
| N+1 cap、UNKNOWN 与 family usage | `TestCompositeFamilyUsageProjectionCompleteKnownSumsAndReadOnlyRetry`、`TestCompositeFamilyUsageProjectionUnknownAndPendingConsumeSlots`、`TestCompositeFamilyUsageProjectionDoesNotCombineMixedCurrencies` |
| 统一取消与 permit 竞态 | `TestRunCancellationRequestV1RoundTrip`、`TestRequestRunCancellationFamilyAtomicIdempotentAndConflicts`、`TestRunCancellationRacesModelBeginAtOnePermitBoundary`、`TestRunCancellationBlocksNewExternalEffectPermits` |
| 重启、完整备份与篡改拒绝 | `TestCompositeReopenMatrixPreservesLedgerAndNeverRegrantsPendingMerge`、`TestCompositeBundleRoundTripPreservesFamilyResultsAndCancellation`、`TestCompositeSemanticClosureRejectsFamilyTampering`、`TestCompositeCoreDeterministicFailureBundleRoundTrip` |
| 生产入口与 Pure Chat 隔离 | `TestRunCompositeChatUsesProductionComposition`、`TestCompositeChatServiceRunsAtomicParallelFamilyAndReusesExactRetry`、`TestPureChatAdmissionKeepsCompositeOptionalPathEmpty` |

该 S2 accepted 状态只覆盖同 Tenant/Workspace、depth 1、2..8 Specialist、固定
`ALL_REQUIRED`、Model/声明式 Context/本地只读 RAG 的第一切片。该 S2 裁决当时尚未
包含 Reviewer、公平 Scheduler、动态图、Learning、跨 Workspace 与完整团队修复循环；
其中公平 Scheduler 与单次 Reviewer 审核门后续已经分别由本规格第 16、18 节单独验收，
其余能力仍不得由该历史裁决推导。

S1 全链路验收必须经过真实 `cmd/freeagent` composition root。S2 增量必须经过同一
Assembly/Context/Store/Loop/backup 生产组件闭包和真实消费者，不接受替代 Runtime
或未接线 evidence 链；直接打开裸 Store 的 Legacy 测试不能冒充全链路证据。

## 16. S3-A 公平 Scheduler 最小 Runtime 合同

状态：`S3_FAIR_SCHEDULER_ACCEPTED_DEVELOPMENT_SLICE`。本节固定并验收默认关闭的本地
公平执行入口，显式替代本文早期“尚未实现公平 Scheduler”的 S2 阶段性限制；该状态
本身不授权 Reviewer、动态图或跨 Workspace 内容传递，也不等同 S3-D 发布验收；单次
Reviewer 审核门后续由第 18 节独立验收。

### 16.1 单一 Loop 的 pre-leased 入口

Scheduler 不得先选 RunID 再调用现有 `Loop.Run`，因为这会让选择、限流与 lease acquisition
分裂；也不得先取得 lease 后再次调用会自行 acquire 的 public path。唯一允许的链为：

```text
Current Store 原子 ClaimFairRun
  → 返回 existing fenced RunLease
  → Core 内部 UniversalLoop.RunClaimed
  → 复用同一 advance / Gateway / outcome / UNKNOWN 路径
  → Universal Loop 释放该 lease
```

`RunClaimed` 只位于 internal Core，不加入模型、模块或普通 SDK 的 authority surface。它必须
验证 `RunInput.RunID == RunLease.RunID`，并与 direct `Run` 共用同一个输入校验、advance、
持久化和 release 实现；禁止复制第二状态机。lease ownership 转入 Loop 后，正常和错误出口
均由 Loop 尝试释放，仍受 owner/epoch/revision fencing。Scheduler 本身不取得模型、Action、
Channel、Memory 或知识权限。

重启后无法恢复旧调用者的临时 MaxSteps，因此首版 scheduled quantum 固定为
`max_steps=4, max_duration=2m`。这是调用方 ceiling，不得突破 Manifest dispatch cap、预算、
deadline、Gateway permit 或 UNKNOWN 禁令；当前 Pure/Composite 实际语义仍按自身冻结能力
提前终止。

### 16.2 production composition 与开关

Operator 配置在进程启动时冻结：

```text
enabled = false
global_workers = 4
max_active_per_workspace = 2
max_active_per_family = 2
algorithm = least-served-workspace/v1
```

配置缺失等同关闭；Agent、Profile、模块、模型输出和 Reviewer 均不能开启 Scheduler 或扩大
上限。首版不支持热切换；变更必须停止新 Admission、排空已 claim worker、关闭 Store 后
重启。关闭时 production composition 继续把原 `UniversalLoop` 注入 consumer，不构造
Scheduler worker、不扫描 runnable、不访问 `workspace_scheduler_state`，因此 Pure Chat 和
现有 Composite 走原 direct path。

启用时只构造一个持有同一 Current Store、同一 Universal Loop 和当前精确 Tenant Registry
的 Scheduler。它可以使用内存 wake/waiter 提高同步请求响应效率，但这些对象不得保存
候选、permit 或终态权威，重启可全部丢失；唯一 runnable/lease/result 仍在 Store。所有已
接入 consumer 必须经 Scheduler facade，禁止同一启用状态下旁路直调 `UniversalLoop.Run`。

首个 S3-A consumer 范围固定为 Pure Chat 与当前 depth-1 Composite。Channel Run 继续使用
既有 direct composition；Scheduler 候选必须排除具有 `channel_ingress_receipts.run_id`
映射的 Run，且 CLI/HTTP 对 Scheduler+Channel 的未实现组合显式拒绝，不能用缺少 Endpoint
runtime 的 Registry 静默执行。Channel Scheduler 接线需要后续独立原子扩展。

### 16.3 运行与关停

每个 worker 重复执行 `ClaimFairRun → RunClaimed`，Store claim 状态为
`NO_RUNNABLE` 时有界等待新 Admission/wake，`CAPACITY_EXHAUSTED` 时等待 lease 释放或短退避；
两者都不得 busy-loop。意外执行错误不能立即无限重取同一 READY Run；首版应 fail closed
停止该 worker/服务并向调用者返回错误，待 Operator 检查，而不是创建隔离队列或替代 Run。

正常关停顺序固定为：停止新 Admission → 停止新 claim → 等待已 claim worker 通过同一
Loop 收口并释放 lease → 运行既有 shutdown recovery → 关闭 Store。Startup Recovery 必须
先于 Scheduler worker，三类 PENDING 与 WAITING_RECONCILIATION 从不进入 fair claim，确保
UNKNOWN 只对账原 Attempt且不语义重放。

### 16.4 与最初设计的关系

公平算法只读取 Tenant、Workspace、family、Admission 时间、Frame step 与 lease，不读取
任务正文，不调用模型做调度，不修改 Agent/Profile/Workspace 内容，也不影响 RAG rank、
Skill 顺序或 Provider 选择。它是单 Runtime 上的可选执行策略，不是新 Agent 系统、DAG、
消息总线或第二控制平面；关闭时原始轻量路径不变。

### 16.5 当前实现与验收证据

当前 production composition 只在 Operator 显式设置 `--enable-fair-scheduler` 时构造一个
`runscheduler.Scheduler`；缺省关闭时仍由现有 consumer 直接调用同一个 Universal Loop，
不读取 `workspace_scheduler_state`。启用路径复用 `ClaimFairRun → RunClaimed`，没有复制
第二状态机；固定 worker permit、Scheduler 级 stable fatal latch、claim/close 线性化和
drain 失败时保留 Store 共同闭合并发与优雅关停。Startup recovery 之后、开放 Scheduler
之前还会执行完整 Current Store semantic closure，损坏的稳定投影不能被合成为正常结果。

当前证据覆盖：三个 Workspace 的确定性公平 claim、Workspace/family/global 三层上限、
Composite Child 并行与唯一 Root merge、terminal/PENDING/UNKNOWN 不被错误重放、同目标
并发单次执行、重启后计数保留、默认关闭零 Scheduler 状态访问，以及真实
`ClaimFairRun → bundle → verify → restore`。受影响包的 Windows `go test`、`go vet` 与
Linux/WSL `-race` 已在当前静止实现上通过。该证据只验收 S3-A 开发切片；全仓 Release、
六平台构建、长时间无饥饿、Public Stage 和最终发布文档仍留在 S3-D。

## 17. 原始设计影响

本规格通过 typed Agent/Workspace 输入、PortPlan 和统一 Module Protocol 保留自由
组合与模块外挂；已验收的 Parent/Child + Composite 第一切片仍让每个 Run 只有一个
Member，并复用同一 Assembly Compiler、Universal Loop 与 Current Store。其整数权重只
决定 Parent merge 的 50% Child-result Context 分配和规范顺序，不改变 RAG rank、Skill、
Reviewer、模型路由或权限。
Action 的私有 executor 只限制副作用执行权，不把具体工具固化进 Core；已验收的窄 MCP
本地 Tool 与后续第三方/远程能力都通过同一 Describe/Prepare + Host Adapter 接入。
Channel 同样只是 Workspace 通过 `channel.transport/v1` 外挂的可选能力；它复用同一
Loop、Gateway 与 Store，不把渠道品牌固化进 Agent，也不改变专业/复合 Agent 的编排。
默认聊天没有新增 schema token、发现读取或可选模块账本写入；仅有进程级共享账本安全
扫描，不读取 Channel 资源或产生网络效果。未选择 Composite 时新增 optional 字段必须
省略且无配置选择/child/result 读取，因此既有 Pure Chat canonical bytes 和零访问不变量
保持不变。阶段性未启用不等于删除最终能力，本次只删除重复执行路径和第二事实源。

## 18. S3-B Reviewer 审核门 Runtime 合同

状态：`S3_COMPOSITE_REVIEW_GATE_ACCEPTED_DEVELOPMENT_SLICE`。本节记录历史 S3-B：当时只授权
同 Workspace、depth-1 Composite 上的一个可选 Reviewer Run；当时不授权自动选 Reviewer、
返工循环、动态 Agent 图、跨 Workspace 内容传递或知识/Memory 写入。当前 W5 增量只由
§20 授权，不追溯改写本节 canonical bytes 与 N+2 历史合同。

### 18.1 Control、身份与关闭路径

`CompositeAgentDefinitionV1` 可选携带一个 `reviewer`：精确冻结 `agent_id`、`profile_id`、
`policy=RESULTS_GATE` 与 `max_output_tokens=1..1024`。Reviewer 由 Operator 在同一
ControlSnapshot 中显式选择；模型、Agent 和模块不能自荐或替换它。Reviewer 不参与
Specialist 的 10000 权重。

Reviewer-disabled 时该字段及 Root Plan 的对应字段都必须 `omitempty`；既有 Control、
Manifest、ContextCompilation、MODEL_REQUEST canonical bytes、N+1 family cap 和可选仓库
零读取保持不变。启用后 family 固定为 `ROOT + 2..8 CHILD + 1 REVIEWER`，cap=N+2。
`CHILD` wire 值继续表示 Specialist，不重命名；新增 role 只为 `REVIEWER`。

Reviewer RunID、AdmissionKey、MemberID 与 RecoveryRootRef 必须由 Parent intent digest 和
独立 domain 确定性派生。Parent、Specialists 与 optional Reviewer 继续由一次 family
Admission 原子发布；每个 Run 仍只有一个 Member。Reviewer Profile 必须有唯一
`model.generate/v2`，可使用冻结 Role/Skill 与本地只读 RAG，但不得绑定 Action、Channel、
MCP Tool、Memory 写、Secret、知识修改或远程检索。

### 18.2 单一 Loop 状态机

Reviewer 与 Root 都复用既有 `WAITING_CHILDREN` step 和 RunLease，不新增 Review Loop、
queue 或 worker。Reviewer 只有在全部 Specialist 成功后才可执行唯一
`composite.review/v1` Model Attempt；任一 Specialist PENDING/UNKNOWN 时保持等待，任一
Specialist FAILED 时 attempt-free 终止。Direct Composite consumer 的顺序固定为有界并行
Specialists → optional Reviewer → Root；公平 Scheduler 仅认领满足同一 Store 条件的 Run。

Reviewer PENDING/UNKNOWN 只允许原 Attempt 对账，不创建替代 Attempt、不更换 Provider、
不退款 family slot，也不让 Root runnable。Reviewer FAILED 或非法输出均终止 Reviewer；
Root 随后以 `COMPOSITE_REVIEW_FAILED` 或 `COMPOSITE_REVIEW_OUTPUT_INVALID` attempt-free
终止。Reviewer `REJECT` 使 Root 以 `COMPOSITE_REVIEW_REJECTED` 终止，绝不调用 merge。

### 18.3 输入、Verdict 与 Context

Reviewer 请求只包含冻结的 Reviewer 静态策略、原始 TASK_INPUT、Root plan 顺序的完整
Specialist 结果，以及每个结果的 slot/focus/weight、RunID、ManifestDigest、
MemberSnapshotDigest、ResultRef 和 terminal revisions；可以附加同一授权边界的本地只读
RAG。结果集先规范化为 `CompositeSpecialistResultSetV1`，其 domain-separated digest 进入
Reviewer 请求与 Verdict。不得注入其他 Workspace、Specialist 私有 Memory 或动态权限。

Reviewer Provider 返回的 `ModelGenerateOutputV1` 不得含 ActionRequest；
`assistant_text` 必须可作为单一 `ReviewVerdictV1` JSON object 精确解码，不接受代码
围栏、前后文本或未知字段。模型无需猜测对象键顺序；Core 在成功 outcome 前必须严格解码、
校验并重建 canonical Verdict，再以 canonical JSON 替换
`ModelGenerateOutputV1.assistant_text` 持久化。Provider 返回的非 canonical 键序或空白
不作为成功 MODEL_RESULT 的权威字节保留：

```text
schema_version = review-verdict/v1
family_digest = Root ManifestDigest
specialist_result_digest
decision = APPROVE | REJECT
issue_codes[]
affected_slot_ids[]
bounded_reason
```

issue code 只允许 `CONTRADICTION|MISSING_EVIDENCE|SCOPE_MISMATCH|UNSUPPORTED_CLAIM|
INCOMPLETE_COVERAGE|SECURITY_CONCERN`。数组必须 binary 排序且唯一；affected slot 必须属于
Root plan；reason 为非空 NFC、最多 4096 bytes。APPROVE 的两个数组必须为空；REJECT
必须至少包含一个 issue 和一个 affected slot。family/result digest 必须与当前冻结事实
精确一致。非法输出在终态持久化前转换为 `ModelAttemptFailed +
COMPOSITE_REVIEW_OUTPUT_INVALID`，Usage 仍保留。

Reviewer `max_output_tokens` 必须以 `min(frozen model max_tokens, reviewer ceiling)` 收紧并
写入其冻结 MODEL_REQUEST；不能只是估算字段。Root merge 仍使用完整 Specialist 结果，
并额外把精确 APPROVE Verdict 作为带固定不可信前缀、不可摘要/Drop/截断的输入；预算不足
时在 permit 前确定性失败。Reviewer-disabled 时不产生该输入，原 merge bytes 不变。

### 18.4 Store 原子审核门

不新增 Reviewer 表、permit 表、Review event 或 `REVIEW_VERDICT` ContentKind。严格
Verdict 由 Reviewer 唯一成功 Model Attempt 的既有 `MODEL_RESULT` 保存，其
`assistant_text` 必须逐字节等于 Core 重建的 canonical Verdict；Attempt、ResultRef、
Usage、Frame 与 RunEvent 继续由现有 Model outcome 事务原子持久化。

Root 的 `BeginModelDispatch(composite.merge/v1)` 必须在同一个 `BEGIN IMMEDIATE` 中重新
证明：family 未取消；全部 Specialist 成功；Reviewer 恰有一个终态 Attempt；ResultRef 与
MODEL_RESULT 闭合；Verdict 可严格恢复且为 APPROVE；family attempt count 未达到 N+2；
Root 尚无 merge Attempt。任何条件不成立均不得创建 PENDING。这样 MODEL_RESULT 本身就是
唯一审核事实，不需要第二份 permit 状态。

Reviewer terminal `APPROVE` 只授予未来 Root merge 的必要前置条件，不要求 Reviewer
outcome 事务立即创建 merge。Reviewer 已 APPROVE、Root 仍为 `WAITING_CHILDREN` 且尚无
`composite.merge/v1` Attempt，是合法、可重启、可备份恢复的中间态；唯一禁止的反向关系
是 merge 缺少精确闭合的 APPROVE。

完整 backup/verify/restore 必须验证 optional Reviewer relational row、Manifest/Control/
Member closure、最多一次 review Attempt、canonical Verdict、UNKNOWN 无 merge、
REJECT/FAILED/INVALID 无 merge、任意 merge 均有精确 APPROVE、APPROVE 后 merge 前中间态
合法、N+2 cap、取消传播和 Usage 顺序。S3-B 验收时 Current Store 保持 20 表；
这是历史 schema 证据；W5-F1 验收时沿用 24 表，W2-R1 后的当前 identity 以
`CURRENT_STORE_V1` §3.3 与 §26.1 为准。

### 18.5 对最初设计的影响

Reviewer 仍是可自由配置的普通 Agent/Profile，而不是超级权限或固定模型；新增重量只有
一个 optional Run、一个有界 wire contract 和启用时一次模型调用。Agent/Workspace 独立、
共享 RAG、单 Runtime/Store/Loop/Gateway 与默认 Pure Chat 轻量路径均不改变。

### 18.6 S3-B 历史实现与开发切片证据

Control/Assembly 已冻结 optional Reviewer 及独立确定性身份，Current Store 在一次 family
Admission 中发布 Parent、2..8 Specialists 与 Reviewer。Context Compiler 只向 Reviewer
提供完整有序 Specialist result set，并只向 Root 提供 exact APPROVE Verdict；Universal
Loop 已闭合 max_tokens ceiling、APPROVE、REJECT、FAILED、非法输出、UNKNOWN 与重入。
公平 Scheduler、取消、family Usage 和 direct Composite consumer 均使用现有 Run/lease
状态机，没有第二 worker 或 outcome writer。

完整 backup/verify/restore 已覆盖 Reviewer enabled/disabled、APPROVE 后尚未 merge、
REJECT/FAILED/INVALID/UNKNOWN 无 merge、canonical Verdict 与语义篡改拒绝。受影响包的
Windows 测试与 `go vet`、Linux/WSL 同范围 Race 已通过；S3-B 验收时的物理 Schema
为 20 表，
fingerprint `098bdf44ee765727dc41f84ee3a8af65069691226d815cde882bfa45a9ff6409`，
migration `29575` bytes / SHA-256
`98c2f1e520bcecb1b3777bcab6014c24d34d110cb6dc751d5bd93eee84e293ce`。该组数值只作历史证据；
W5-F1 验收时沿用 24 表；W2-R1 后的当前 fingerprint/hash/size 以
`CURRENT_STORE_V1` §3.3 与 §26.1 为准。

该状态不等于 S3-C 真实模型效果验证或 S3-D 发布验收。在 S3-B 当时，全仓 Release、全仓
Linux Race、六平台构建、Public Stage、最终 seal、自动 Reviewer、返工循环、动态图、跨
Workspace 内容传递与 Learning 均未完成；后续 W4/W5 增量必须以各自权威章节判断。

## 19. W4-L0 Learning 治理纯合同

W4-L0 只冻结 `internal/learningcontract` 的两份 canonical wire：
`learning-proposal/v1` 与 `learning-review-verdict/v1`。它们是后续可选 Learning 模块的
数据合同，不是已启用的 Runtime 能力。

`ProposalV1` 必须绑定 exact Workspace、Agent、Profile、Member snapshot、Run manifest、
ResultRef、目标 ModuleRef 与既有 canonical Knowledge/静态 Skill Draft。来源只能由
`AGENT_GENERATED / LOCAL_IMPORT / REMOTE_REFERENCE` 三种 admission evidence 确定性派生；
持久 wire 仅保存 mechanism、origin digest 与 revision digest，不保存 URL、路径、凭据或
原始来源材料。Agent-generated 来源的 origin/revision 同时固定为 exact proposer ResultRef。

Knowledge `content_fingerprint` 只覆盖排序后的正文与规范化可见范围多重集，排除 Source、
Document、Chunk 的可重命名 ID 及版本/派生摘要；exact DraftDigest 仍覆盖完整原始 Draft。
因此改名或版本号空转不能绕过语义去重，而正文或请求范围变化会产生新指纹。Draft 中的
可见范围仅是权限请求，未来 materialization 必须由本地 Authority 拒绝或裁剪。

Reviewer 模型只能输出严格、有界、完整字段的 APPROVE/REJECT wire；Verdict 必须绑定 exact
ProposalID、source fingerprint 与 content fingerprint，空 issue list 统一为 `[]`。当前合同
不携带 Reviewer 身份、Trust、安装、激活、Catalog CAS、Secret、Action 或 Channel 权限。

本切片没有修改 Assembly Compiler、Universal Loop、Port、Gateway、Catalog、Schema、Store、
Worker 或 Backup。W4-L1/L2 仍须在唯一 Current Store 与普通 Model Attempt 内完成权威 Run/
Result 反查、自审禁止、UNKNOWN 禁止语义重放和终态持久化；在这些消费者闭合前 Learning
产品能力继续标记为 `planned`。

### 19.1 W4 当前显式 Version 消费闭包

后续 W4-L1 至 L4 已按 Current Store 规格完成开发切片；L0 的历史边界不回填。当前显式消费仍
固定为 `Proposal → 独立 Reviewer APPROVE → immutable Version/handoff → Operator module-apply →
新 Run`。批准和物化都不得安装、激活、绑定或授予 Trust/Authority；stale Catalog CAS 必须零
部分发布。只有 Operator 以当前 exact pointer 显式 Apply 后，新 Run 才能冻结并消费该 Version，
旧 Run 保持原 Member/Manifest。`TestW4ApprovedLearningMaterializedSkillApplyIsConsumedOnlyByNewRun`
已用静态 Skill 闭合 exact Version/Artifact、REQUIRED + deny-all Binding、本地 DECLARATIVE
Trust/Adapter 与新旧 Run 边界。该闭包不授权自动 Review、Materialize、Apply 或扩权。

## 20. W5-D1 Decision family 与 W5-X1 Workspace Transfer 冻结合同

当时状态：`W5_X1_WORKSPACE_TRANSFER_ACCEPTED_DEVELOPMENT_SLICE / W5_F1_NEXT`。本节是
W5-D1/X1 Runtime 的冻结合同；它在 X1 验收时取代较早章节中仅适用于 S2/S3 历史切片的
“当前 family 同 Workspace”“Transfer 尚未实现”和“唯一 cap=N+1/N+2”等表述，但不改写
历史 canonical bytes、验收数字或恢复语义。后续 F1 只以 §21 增量收紧稳定性。

### 20.1 W5-D1 固定 Decision 图

W5 Decision 是 `CompositeRunPlanV1.Decision` 的显式 opt-in，不是模型动态编排接口。含 N 个
Specialist 的 family 必须在一次 Admission 中预冻结完整 `2N+3` 物理 Run：

```text
Initial Children[0..N-1]
  → Reviewer0
  → Repair Children[0..N-1]
  → Reviewer1
  → Root merge
```

Root plan 的 `Children` 表示 round 0 logical slots；`Decision.RepairChildren` 按相同 logical
slot/order 冻结不同 RunID、AdmissionKey、MemberSnapshotDigest 与物理 ParentSlotID；
`Decision.RepairReviewer` 是独立 Reviewer1。所有 Run 保持 depth 1、单 Member、单 Workspace，
identity 在模型执行前已存在。family cap 必须精确为 `2N+3`。

Specialist 只能输出严格、有界 `specialist-contribution/v1`。Host 从冻结 slot/Run 与真实
MODEL_RESULT 构造 `collaboration-contribution-set/v1`；Reviewer 只能输出严格
`collaboration-review-verdict/v1`，其 decision 闭集为 `APPROVE | REJECT | REPAIR_REQUIRED`。
模型不能自报或覆盖 family/Run/Workspace/weight/authority identity。

round 0 `APPROVE` 直接跳过全部 repair physical Runs 后允许 Root；`REJECT` 明确终止且无 Root
merge；`REPAIR_REQUIRED` 只能选择原 plan 中 affected logical slots，并且只允许一次 round 1。
未受影响 repair Child 必须写入可恢复 skipped 事实，且永不创建 Attempt；受影响 repair Child
闭合后才允许 Reviewer1。round 1 只允许 `APPROVE | REJECT`，禁止再次 repair。dormant/skipped
repair Run 始终保留在 graph/cancellation/backup/Usage Run 投影中，`Attempt=nil`。

UNKNOWN 只能对账原 Model Attempt；PENDING/MODEL_UNKNOWN 占用其物理 slot且不退款。重入、
Scheduler、恢复或并发相同请求不得创建替代 Run/Attempt、切换 Provider/Binding、重新执行已闭合
round 或凭进程缓存重建 verdict。Decision 初始/repair Specialist 的模型步骤使用冻结 Pure Chat
step；Reviewer 与 Root 分别使用冻结 review/merge step。

### 20.2 单 Run Workspace 与双边授权

整个 family 必须同 Tenant，但不再要求所有 Run 同 Workspace。Root、Reviewer0 与 Reviewer1
固定在 Root Workspace；每个初始/repair Child 可以保持同 Workspace，或按冻结
`WorkspaceTransferPlanV1` 绑定另一 Workspace。同 Workspace Child 的 `Transfer` 必须省略；跨
Workspace Child 的初始与对应 repair ref 必须使用相同 logical slot、target Workspace 与双边
grant 计划。

每个 Workspace owner 独立发布 `workspace-transfer-grant/v1`，冻结 owner/peer Workspace、revision、
enabled、send/receive payload kinds 和双向 byte ceiling。完整协作要求四个方向权限同时存在：

```text
REQUEST: Root sends TASK_SUMMARY; Child receives TASK_SUMMARY
RESULT:  Child sends SPECIALIST_RESULT; Root receives SPECIALIST_RESULT
```

Core 必须按 envelope 实际方向分别验证 source-send 与 target-receive，并采用双方 ceiling 的交集；
同 Tenant、不同 Workspace、grant owner/peer、ID/digest 与 frozen plan 任一不闭合即 fail-closed。
已发布 Run 引用的 historical grants 是恢复事实。当前 Control 仅可 deny-only 撤权：撤权可以阻止
尚未发生的新 REQUEST/RESULT，但不能替换历史 grant、改写 Manifest、重路由 Child 或创建替代
Run。模型、Module、Workspace peer 与 Provider 都不能自授权。

### 20.3 REQUEST/RESULT payload 与可信编译

Transfer payload kind 的闭集只有：

```text
TASK_SUMMARY
SPECIALIST_RESULT
```

REQUEST 使用 `workspace-task-summary/v1`。它只能由 trusted compiler 从 Store-loaded exact
`TASK_INPUT` 构造；round 1 还必须把上一 contribution-set digest 与 exact Reviewer verdict 作为
有界 repair basis。调用方或模型不能提交自组装 summary identity。跨 Workspace 不传完整
History、Memory、Knowledge/RAG 正文、Secret、SecretRef 或任意文件。

RESULT 不生成第二份正文。`workspace-transfer-envelope/v1` 必须通过真实 terminal
`MODEL_RESULT` 精确闭合 `specialist-contribution/v1`，并绑定 Tenant、方向、payload kind/schema、
TaskInputRef、source/target Workspace、双边 grant ID/digest、RootRunID、ChildRunID、logical
SlotID、PayloadRef 与 byte size。repair Child 还必须闭合其物理 ParentSlotID 与 round lineage。

Context Compiler 只消费 Store 已解析并验证的 immutable payload/envelope。跨 Workspace Child 的
模型请求只看到受控 `TASK_SUMMARY` 与其 assignment；Root/Reviewer 只看到经过 RESULT 验证且按
plan 排序的 contribution。Transfer metadata 属于 evidence，不授予模型读取其他 Workspace
仓库、Memory、History、Secret 或执行任意动作的能力。

### 20.4 原子调用边界、UNKNOWN 与恢复

跨 Workspace Child 的 `BeginModelDispatch` 必须在同一权威事务中重新验证 frozen family、历史
grant 与当前 deny-only 权限，并提交 REQUEST payload/envelope、MODEL_REQUEST、原
ModelDispatchAttempt=PENDING、Usage 占位与 Frame/Event；提交成功前 Provider 调用次数必须为
零。exact retry 只恢复原 REQUEST/Attempt，不重新编译 summary 或再次调用模型。

Provider 成功时，Core 先严格规范化 `specialist-contribution/v1`，再在同一 outcome 事务提交原
MODEL_RESULT、Usage、RESULT envelope、Attempt 与 Frame/Event。数据库终态持久化失败时不得从
进程缓存继续 family；恢复必须以原 Attempt 进入 UNKNOWN/对账边界。FAILED、deadline、非法
模型输出或 UNKNOWN 不得生成虚假 RESULT。UNKNOWN 只允许更新原 Attempt 的可靠 evidence，禁止
语义重放、换 Provider/Binding、生成替代 Transfer 或新 Run。

完整 backup/verify/restore 必须保留并双向验证完整 `2N+3` graph、dormant/skipped repair、
historical grants、Transfer plans、REQUEST/RESULT payload/envelope、Model Attempt/Usage/Result、
Frame/Event 与 cancellation。restore/open 不得读取 current Control 重组历史图、调用 Secret
resolver/Provider、产生新 envelope 或发送网络。Pure Chat 与未选择跨 Workspace 的 family 对
Transfer grant、Content 与编译路径保持零访问。

### 20.5 开发验收边界

源码验收覆盖完整 Decision 图、subset/all repair、round 0 approve/reject、round 1 approve/reject、
UNKNOWN 零重放、并发 exact retry、双边 grant 四方向、trusted summary、terminal result closure、
Usage `2N+3` 投影、语义篡改拒绝与完整 backup/restore。W5-X1D 的一次真实 DeepSeek 纵链记录为：

```text
HTTP 2xx = 4/4
physical Runs = 7
Attempts = 4
exact retry model calls = 0
REQUEST verified = true
RESULT verified = true
input/cached/uncached/output tokens = 3443/768/2675/1353
reasoning tokens = UNKNOWN
estimated cost = CNY 0.00539636
provider-reported cost = UNKNOWN
reconciled cost = UNKNOWN
```

该记录只证明此开发切片的真实 HTTP、恢复边界、Usage 与费用估算落账；不得表述为模型质量
基准、稳定缓存率、生产价格、生产 SLA 或整个 W5 完成。下一工作包为 `W5_F1_NEXT`。

## 21. W5-F1 Collaboration Stability 当前权威合同

状态：`W5_F1_COLLABORATION_STABILITY_ACCEPTED_DEVELOPMENT_SLICE / W5_COMPLETE_ACCEPTED_DEVELOPMENT_SLICE / W6_NEXT`。
F1 不定义新的协作协议或执行平面；它只把 §20 的可选 Decision/Transfer 能力接入既有公平
Scheduler、Universal Loop、UNKNOWN 和 Backup 边界，并冻结持续负载与失败恢复语义。

### 21.1 Decision-aware Scheduler 仍是唯一执行链

W5 Decision family 的 runnable 集合必须只由 Current Store 的
`loadCompositeDecisionFrontier` 从冻结图和权威终态派生。Scheduler 不得用“所有 sibling 已终止”
的 legacy 近似规则判断 Decision frontier；dormant/skipped repair Specialist 与 Reviewer 必须
保持 `Attempt=nil`、零 claim，并且不能阻塞 Reviewer0。未启用 Decision 的 ordinary Composite
继续走原 sibling 终态路径，原 canonical bytes 与行为均不改变。

唯一调度与执行链保持：

```text
ClaimFairRun
  → 同一 BEGIN IMMEDIATE 中选择 frontier、检查 Global/Workspace/Family cap、取得 lease、更新 served_units
  → RunClaimed
  → Universal Loop
```

不得另建 Scheduler、Queue、Worker、Runtime、Loop 或 runnable cache。原三层 cap、fenced lease、
`served_units` 公平账本、family cancellation、PENDING/UNKNOWN slot 占用与 exact retry 规则全部
保持。三个始终 runnable Workspace 的确定性本地验收完成 `900` 次 claim，第 `450` 次后重开
同一 Store；各 Workspace 恰好 `300` 次，首次服务不晚于第 `3` 次，最大服务间隔不超过 `3`。
这只证明单机、单 Store、tenant-bound 公平，不表示分布式公平、跨 Tenant 配额或生产 SLA。

### 21.2 纵链、排序与取消

Scheduler 实际纵链必须覆盖 round-0 APPROVE、单槽 repair→round-1 APPROVE 以及跨 Workspace
Transfer；只有 frontier 中的 Run 可以被 claim。Specialist 即使以反向时序完成，Host 仍必须按
frozen plan order 归位 contribution，Reviewer 与 Root 不得观察完成顺序；Store reopen 后
Run/Attempt/Result identity 与 canonical bytes 必须逐字不变。

family cancellation 必须原子覆盖跨 Workspace 的完整 `2N+3` family。取消后新
`BeginModelDispatch` 必须拒绝，且零 Transfer payload/envelope、零 Provider 调用；既有
PENDING/UNKNOWN 的原 Attempt 继续按原合同对账，不能通过取消删除或替换。Child 仍只能随 family
取消。Pure Chat 与未启用 Transfer 的 ordinary Composite 对 Transfer grant、ContentRecord、
compiler 与外部调用保持零访问。

### 21.3 UNKNOWN、RESULT 与恢复

Transfer `MODEL_UNKNOWN` 只能以可靠 canonical evidence 收口原 Attempt。evidence 必须闭合
Attempt identity、当前 revision、冻结 Provider/Binding 与真实终态：`SUCCEEDED` 在同一 outcome
事务生成唯一 RESULT envelope，`FAILED` 不生成 RESULT。无 evidence、stale revision、不同
Provider/Binding、不同 Attempt 或其他不可验证 evidence 必须 fail-closed、零部分写。exact retry
只能恢复原 REQUEST/Attempt，不取得新的 Provider permit。

完整 Decision+Transfer bundle 必须通过 Backup create→verify→restore→
`OpenExistingCurrentStore`→family resolve。恢复发现跨 Workspace PENDING 时，只允许将同一原
Attempt 置为 UNKNOWN；REQUEST 保留且不得生成 RESULT，exact retry 与 Universal Loop guard 都必须
零语义重放、零 Provider replay、零换 Provider/Binding、零替代 Attempt。

### 21.4 权限矩阵与验收边界

Runtime 必须对缺失/单边 grant、REQUEST/RESULT 四方向、Tenant、双方 Workspace exact version、
grant revision、direction、kind/schema/ref、真实 ContentRecord bytes/ref/size、32/48 KiB ceiling
执行完整 fail-closed 验证。撤权后新 Admission 必须拒绝；历史 exact retry 继续使用 Admission 时
冻结的授权并返回原事实，不能从 current Control 重授权、重路由或重编译。

F1 的真实 DeepSeek canary 恰好 `4/4` 次 HTTP 2xx；同一请求 exact retry 新 HTTP 与新 Model
Attempt 均为 `0`，REQUEST/RESULT 均 verified。Usage 为 input `3149`、cached input `768`、
uncached input `2381`、output `1492`，token-weighted cache hit rate `24.388695%`；冻结
PriceSnapshot estimated cost 为 `0.00538036 CNY`，reasoning token、provider-reported cost 和
reconciled cost 均为 `UNKNOWN`。它是独立 F1 canary，不覆盖 §20.5 的 X1D 历史数据，也不是稳定
缓存率、模型质量、生产价格或生产 SLA 基准。

Windows 全仓测试、`go vet ./...`、`go mod verify` 与 gofmt 门禁已通过。WSL ext4、Go 1.26.5、
GCC/CGO 的首轮七包 race 中，corecontract、controlcontract、assemblycompiler、runscheduler、
localchat 与 currentbackup 通过且为零 data race；currentstore 只因测试的 1 秒墙钟 TTL 失败，
并非 data race，故首轮七包命令不记为整体 exit 0。固定测试逻辑时间后，定向 race、单核慢调度及
最终完整 `go test -race -count=1 ./internal/currentstore` 均通过；最终命令 exit `0`，耗时
`466.241s`。F1 未新增 Runtime、Store、Loop、Gateway、Scheduler、Queue、Worker、Ledger、表、migration 或协议；仍为
24 表和 §20 对应的原 fingerprint/migration。`W5_COMPLETE_ACCEPTED_DEVELOPMENT_SLICE` 与 W2-D
裁决相互独立；W2-D 已凭自身集中纵链、逐 Entry/inspect P1 回归、Windows/WSL2 ext4 全仓和发布
门禁收口为 `W2_D_LOCAL_ASSEMBLY_ACCEPTED_DEVELOPMENT_SLICE`。W2-E2 又以编译进 Core、无外部
效果的 `text.stats` 闭合统一 Apply、真实 model→action→model、Disable 与 Backup/Restore，状态为
`W2_E2_TRUSTED_TEXT_STATS_APPLY_ACCEPTED_DEVELOPMENT_SLICE`。W2-E3 再以统一
`WORKSPACE_CHANNEL_ENDPOINT` Apply 闭合双 Workspace/Endpoint 隔离、Channel `UNKNOWN`、Disable 引用
与 Backup/Restore 后续推，状态为 `W2_E3_WORKSPACE_CHANNEL_APPLY_ACCEPTED_DEVELOPMENT_SLICE`。
W2-E4 又以第七个 exact handler 闭合同一 DeepSeek artifact/adapter/provider Instance 内 Flash↔Pro、
Price/Authority/Profile、Usage/UNKNOWN 与恢复边界，状态为
`W2_E4_DEEPSEEK_MODEL_REPLACEMENT_ACCEPTED_DEVELOPMENT_SLICE`。W2-E5-A 再以 governed Knowledge
闭合 shared classifier、same-Profile exact Model Require、Core-owned `knowledge.read` grant、完整
publication fast-path 与 Backup/Restore；其历史验收状态为
`W2_E5_A_REQUIRES_PERMISSION_GRANT_ACCEPTED_DEVELOPMENT_SLICE / W2_E5_B_NEXT`。W2-E5-B 随后以同一
受信 Document Insight Installation/Activation/Instance 闭合 Context+Action 双 Port、RAG+reservation、
完整 Provider identity、Gateway 与 Backup/Restore，当前状态为
`W2_E5_B_DOCUMENT_INSIGHT_DUAL_PORT_ACCEPTED_DEVELOPMENT_SLICE`。截至 E5-B 的六项 accepted 都不开放
任意第三方进程内 Action、公网/任意第三方 Channel Provider、跨 Provider Model、自动路由、通用
multi-Port、同一 Port 多 Binding 或 REMOTE/WASM。后续 W2-R1 只按 §22 接通一个窄 REMOTE Action
协议，W2-R2 再按 §23 接通一个窄纯计算 WASM Action 协议；Operator Module Apply 与 Module
Conformance 保持 experimental，其他 REMOTE/WASM、生产级不可信隔离、广义通用装配与 W6/W7
保持 planned。首次生产部署、公开 Beta 与 `RELEASE_READY` 均未获准。

## 22. W2-R1 REMOTE Action Host 历史接受合同

状态：`W2_R1_REMOTE_ACTION_HOST_ACCEPTED_DEVELOPMENT_SLICE`。本节只增量
收口 `action.provider/v1 + REMOTE/freeagent-action-http/v1`，并覆盖本文较早阶段中把全部
REMOTE 统一列为未实现的当前口径；那些段落的阶段状态、handler 数量和验收数据仍作为原时点
历史证据保留。R1 不增加 Runtime、Store、Loop、Gateway、Catalog pointer、Port 或效果账本。

### 22.1 装配、授权与第 8 个 handler

当前停机 Operator Apply 表恰有 8 个 exact handler tuple；R1 新增且只新增：

```text
action.provider/v1
  + REMOTE/freeagent-action-http/v1
  + action-binding-config/v1
```

Manifest 只请求 runtime。当前协议的 entrypoint 必须指向受 ArtifactDigest 覆盖的 canonical
`content/` descriptor；descriptor 只能定义有界 Action，不得携带 endpoint、Secret、proxy、
redirect、retry、fallback 或发现策略。未知 REMOTE protocol 可通过无授权的离线格式验证，但没有
上述 exact handler、本地策略和 Operator grant 就不能 Apply、Activate、Bind 或执行。

`action-binding-config/v1` 的 parameters 必须是 exact
`remote-action-http-binding-parameters/v1`，只保存 canonical HTTPS endpoint 与 SecretRef；Action
映射、EffectClass、结果上限、Tenant/Workspace scope 和 `REQUIRED` FailurePolicy 仍由消费方
Binding/Authority 裁剪。Enable/Dry-run 必须分别精确命中
`--allow-remote-action-artifact`、`--allow-remote-action-endpoint` 与
`--allow-remote-action-secret-ref`。三项均为本次 Operator 调用的瞬时 grant；Apply/Dry-run 只验证
descriptor、Config、Authority 和 grant，保持零 Secret lookup、零 Provider 调用、零 POST。

运行期默认关闭；只有显式 `--enable-remote-actions` 且每个所需 SecretRef 都有唯一
`--remote-action-secret-env <secret-ref>=<ENV_VAR>` 映射时才可构造该可选 Host。缺少开关、缺少或
重复映射、环境变量缺失、Activation/Binding/Authority 漂移均 fail-closed，不得静默降级、换
Provider 或转为本地实现。

### 22.2 Production lazy load 与唯一 Gateway

production 调用顺序固定为：

```text
frozen PortBinding + current Catalog/Activation
  → production exact lazy loader
  → ArtifactDigest + covered size + canonical Manifest/descriptor 复验
  → remoteactionhttp.NewFromArtifact
  → native Action Adapter
  → existing Action Gateway
  → original DispatchAttempt
```

Loader 只能在 exact Binding 已通过 Catalog、current Activation、Authority 与 deny-only 撤权检查后
按需物化；失败、取消、panic、typed nil、摘要/大小/descriptor 漂移均不得进入成功 cache。构件
篡改后的连续调用必须连续拒绝，不能以旧 cache 或 ModuleID/version 模糊命中绕过完整性检查。

Adapter 的 Describe/Prepare 必须保持离线、无 Secret、无网络；普通 `ModuleHost.Invoke` 不得用于
执行 Action。只有既有 `internal/actiongateway` 可以取得 private `ExecutePrepared`，并且必须先把
同一逻辑操作的唯一 `DispatchAttempt` 持久化为 `PENDING`。Gateway 逐字段核对 Run、Member、
Snapshot、Binding、Provider、Action Definition、Proposal、Effect、预算、lease、expiry 与 permit，
再调用 native Adapter；模型、Loop、SDK 和 Loader 都不能绕过 Gateway 直接执行。

### 22.3 HTTPS、SSRF、Secret 与 UNKNOWN

传输只允许一次 HTTPS/1.1 POST，TLS 最低 1.2；proxy、redirect、HTTP/2、keep-alive、自动 retry、
fallback 与动态发现全部关闭。连接前必须拒绝 private、loopback、link-local、multicast、NAT64、
文档/基准及其他 special-use 地址；IPv6 只接受 `2000::/3` 中未落入 deny-list 的公共地址。任何
解析或连接目标漂移都必须在外部效果前失败关闭，不能用后续重解析扩大 endpoint grant。

Secret value 只在 Gateway dispatch 时按 SecretRef 从显式环境映射晚绑定，并只用于 RFC 6750
Bearer header；使用后清零。Manifest、descriptor、Config、Control、Catalog、Snapshot、Attempt、
receipt/evidence、Usage、日志、错误和 Backup 只能保存 SecretRef identity，禁止保存 Secret bytes。

POST 前的确定性拒绝把原 Attempt 收口为 `FAILED` 且网络调用为零；Provider 的明确拒绝响应也只以
同一次 RoundTrip 收口原 Attempt。请求已进入传输而无法证明 Provider 终态时，原 Attempt 必须为
`UNKNOWN`。Gateway、重启、exact retry、Disable、恢复和对账都不得重新 Prepare、重新 POST、换
Provider/Binding 或创建替代 Attempt。若外部调用后终态持久化失败，原 PENDING 由启动/关停共享
safety transaction 原位收口为 UNKNOWN；只有可靠且精确绑定原 Attempt/revision/Provider 的
canonical evidence 才能继续对账。

### 22.4 Pure Chat、Backup 与验收边界

未选择 REMOTE Action 的 Pure Chat 必须对 REMOTE artifact/descriptor、production loader、Adapter、
Secret resolver、Gateway 和 Action DispatchAttempt 全部零访问；已安装或已激活但未绑定不能改变该
边界。Disable 不删除 immutable Installation/Activation、历史 Run 或 Attempt；新 Run 不再取得
Binding，旧 UNKNOWN 仍只能对账原 Attempt。

完整 Backup/Verify/Restore 必须保留并双向验证 Installation、Manifest、ArtifactDigest/covered
size、REMOTE Activation、Binding Config/Authority、SecretRef identity、Action Definition/Proposal、
DispatchAttempt、result/receipt/evidence、Frame/Event 与 terminal closure。Verify/Restore 不解析
Secret、不构造 production Adapter、不调用 Resolver/Gateway，也不发网；恢复后 crash-left PENDING
只按原规则转为 UNKNOWN，不能重放。

集中证据已真实穿过 production loader → native Adapter → Gateway：PENDING 后、POST 前的确定性
失败闭合原 Attempt；篡改构件连续拒绝且失败不缓存；Provider `FAILED + HTTP 422` 恰好一次
RoundTrip；Disable 后删除 artifact 再运行真实 Pure Chat，REMOTE artifact、Adapter、Resolver 与
Action Attempt 访问均为零。既有 hermetic 分层证据另覆盖 SUCCEEDED、UNKNOWN、终态持久化失败、
重启和完整 Backup/Restore。当前没有真实公网 HTTPS 第三方互操作证据，因此不能声称生产网络
SLA、任意 REMOTE、WASM、不可信本地隔离、公开 Beta 或 `RELEASE_READY`。该句保留 R1 收口时的
历史边界；当前 R2 权威合同以下一节为准。

## 23. W2-R2 WASM Action Host 历史验收合同

R2 收口时状态：`W2_R2_WASM_HOST_ACCEPTED_DEVELOPMENT_SLICE / W2_R3_UNTRUSTED_MODULE_ISOLATION_NEXT`。本节
只增量收口 exact `action.provider/v1 + WASM/freeagent-action-wasm/v1`，并覆盖本文较早阶段中把
全部 WASM 统一列为未实现的当前口径；较早段落的阶段状态、handler 数量与验收数据仍作为原时点
历史证据保留。R2 不增加第二 Runtime、Store、Loop、Gateway、Catalog pointer、Port 或效果账本。

### 23.1 第 9 个 handler、授权与默认关闭

Manifest 只可请求：

```text
RuntimeModeRequest = WASM
Protocol           = freeagent-action-wasm/v1
Port               = action.provider/v1
Consumer schema    = action-binding-config/v1
```

协议 entrypoint 必须是 ArtifactDigest 覆盖的 canonical `content/` descriptor；descriptor 再以
artifact-relative `content/` 路径指向唯一 guest module。Core 授予固定
`ExecutionClass=WASM` 与 `freeagent.adapter.action.wasm/v1`，模块、Manifest 与 Plan 不能自行选择
Trust、ExecutionClass、Adapter、权限或 fallback。Enable/Dry-run 必须精确命中瞬时
`--allow-wasm-action-artifact`；它与 LOCAL_PROCESS、TRUSTED_IN_PROCESS、REMOTE artifact grant
互斥。Binding 固定为 `REQUIRED`，parameters 必须是 exact `{}`，descriptor 请求、Binding 和
Authority 的 EffectClass 均必须为 `none`。

运行时默认关闭。只有显式 `--enable-wasm-actions` 且 exact current Catalog/Activation/Binding/
Authority 全部闭合时，production loader 才可按 ArtifactDigest+AdapterIdentity 惰性物化。开关不携带
Secret、endpoint、目录、网络 client 或其他 capability；未启用、未绑定或未在 Catalog 的构件均
fail-closed，不能静默降级为受信、本地进程或 REMOTE Provider。

### 23.2 ABI、零 Host capability 与资源上限

当前引擎固定为 `github.com/tetratelabs/wazero v1.12.0` 的 interpreter、Core WASM 1.0、wasm32。
guest 必须恰好导出 `memory`、`freeagent_alloc_v1(i32)->i32` 与
`freeagent_execute_v1(i32,i32)->i64`；禁止 imports、WASI、Host Module、start function、额外导出、
网络、文件系统与 Secret。每次执行新建并在返回前关闭 Runtime、compiled module 和 instance，
没有常驻 guest 或跨 Attempt 隐式状态。

硬上限至少包括：module `16 MiB`、canonical execution request `128 KiB`、result `32 KiB`、memory
初始 `32` 页/显式最大 `256` 页、table 最大 `65,536` elements、validation `5s`、execution `5s`，
以及全进程最多 `4` 个并发 guest instance。allocation-safe preflight 还在进入 wazero 前限制
type/function/global、code/body/local、data/element/custom section 与 `br_table` 的数量、聚合大小和
声明顺序；当前冻结值由 `internal/wasmaction` 常量与负例测试共同约束。R2 的目标是一个窄、可验证、
无 Host capability 的计算沙箱，不是 OS/container，也不声称已经完成生产级恶意多租户隔离、撤权
清场、供应链签名或任意第三方模块托管。

### 23.3 唯一 Gateway、FAILED 与 UNKNOWN

Describe/Prepare 只读取 artifact-covered descriptor并校验 canonical input，不构造 Runtime 或执行
guest；普通 `ModuleHost.Invoke` 对 WASM 固定拒绝。唯一执行顺序为：

```text
frozen ActionProposal/Binding/Provider
  → original DispatchAttempt durably PENDING
  → sole Action Gateway consumes one-shot permit
  → private ModuleHost.ExecutePrepared
  → native WASM Host
  → original Attempt terminal transaction
```

Host 必须重新闭合 execution request、Provider、Config 与纯计算 Effect，再取得全进程 slot、执行
guest 并把 canonical result/receipt 交还 Gateway。guest trap、调用超时/取消、非法 ABI、越界指针、
超限或非 canonical output 都是已知无外部效果的确定性 `FAILED`，只收口原 Attempt。wazero 当前
没有稳定 fuel/instruction counter，因此 receipt 必须明确
`instruction_metering=UNSUPPORTED`；它只记录引擎、ABI、输入/输出字节、观测 memory pages 与耗时，
禁止伪造 token、price、cost 或 instruction 数。

只有 Host/Store 事务或进程崩溃使 guest 返回后的终态无法持久化时，启动/关停共享 safety transaction
才可把遗留的同一原 `PENDING` Attempt 原位收口为
`UNKNOWN/RECOVERED_PENDING_AFTER_CRASH`。exact retry、重启、Disable、缺失 artifact 与恢复都不得
重新 Prepare、重新执行 guest、换 Provider/Binding 或创建替代 Run/Attempt；UNKNOWN 仍只能凭可靠、
精确绑定原 Attempt/revision/Provider 的 canonical evidence 对账。

### 23.4 Backup、Pure Chat 与验收边界

完整 Backup/Verify/Restore 必须保留 Installation/Manifest、descriptor、guest bytes、ArtifactDigest/
covered size、WASM Activation、Binding Config/Authority、Action Definition/Proposal、原 Attempt、
result/receipt/evidence、Frame/Event 与 terminal closure。Backup、Verify 和 Restore 只复制并逐字节
重验构件，不编译、不实例化、不执行 guest；非 Windows 恢复目录使用私有 `0700`，所有 WASM 包内
普通文件使用 `0600`，不产生 executable。

未绑定 WASM 的 Pure Chat 对其 artifact、descriptor、guest、loader、Adapter、Gateway 与 Action
Attempt 必须全部零访问。R2 集中长链已覆盖统一 Apply、显式 production enable、Catalog lazy load、
真实 model→ActionProposal→PENDING Attempt→Gateway→native WASM→model、canonical Usage receipt、
exact retry、完整 Backup/Restore，以及 guest 返回后终态事务失败、重启恢复为原 UNKNOWN、删除
artifact 后重入零新 Attempt/零 lazy load。安全负例另覆盖 imports/WASI/start/导出、资源声明、trap、
超时、取消、ABI、输出和并发实例上限。本切片未调用真实 API。

这些能力增加的是一个可选计算模块 Host，不把 Role、Persona、Memory、RAG、Skill、MCP、Channel、
REMOTE 或 WASM 固化进 Agent/Workspace；Pure Chat 继续可以零外挂启动，领域知识继续位于共享授权
RAG。Operator Module Apply 与 Module Conformance 仍为 `experimental`；R2 不代表任意第三方包、
生产级不可信隔离、签名/发现/升级、通用 multi-Port/多 Binding、在线控制面、公开 Beta 或
`RELEASE_READY` 已完成。R3 的当前覆盖合同见下一节。

## 24. W2-R3 不可信第三方 WASM 纯计算与停机撤权合同

状态：`W2_R3_UNTRUSTED_MODULE_ISOLATION_ACCEPTED_DEVELOPMENT_SLICE`。本节覆盖 §23 中关于“R3 尚未
完成”的历史口径；§23 的 R2 ABI、零 Host capability、资源上限、FAILED/UNKNOWN 与 Backup 语义继续
有效。R3 不增加 ExecutionClass、Runtime、Store、Loop、Gateway、Catalog pointer、Port 或效果账本。

### 24.1 威胁模型与授权

模块作者可控制 Manifest、descriptor、WASM bytes 和请求输入；Core、Gateway、Current Store、Operator、
Go、wazero 与 OS 属于信任基。R3 只接纳非 builtin 第三方 Module ID 的 exact
`action.provider/v1 + WASM/freeagent-action-wasm/v1 + action-binding-config/v1`、`EffectClass=none` 纯计算。

Apply/Dry-run 的 `--allow-wasm-action-artifact` 与运行期授权分离。`chat/serve` 必须同时设置
`--enable-wasm-actions` 和至少一个可重复的 `--allow-wasm-runtime-artifact <sha256>`。运行期 allowlist：

- 只接受 canonical lowercase SHA-256，重复值失败；
- 必须命中当前 Catalog 的 exact WASM artifact+Adapter identity，否则启动失败；
- 不写入 Store、Manifest、descriptor、Backup 或模块包，不能由模块自授；
- 全局开关不替代 exact digest 授权。恢复旧 Backup 后，历史 WASM 不会因全局开关自动恢复执行资格。

### 24.2 Adapter cache 与执行准入

WASM Adapter 只能冻结 artifact-scoped immutable facts：ModuleID、Version、ArtifactDigest、ExecutionClass
与 AdapterIdentity。不得缓存 Tenant、Workspace、InstanceID、ActivationRevision、Config、Authority 或
Binding 权限。同一 artifact 的多个合法 Activation 可共享只读 Adapter；若同一 digest 对应不同
ModuleID/version/artifact identity，必须失败关闭。

Gateway 顺序固定为：

```text
original Attempt PENDING
  → first current-Activation check
  → ResolveExact / build exact execution closure
  → second current-Activation check
  → private ExecutePrepared
```

第二次检查成功是 execution-admission linearization point。第一次检查前或两次检查之间撤权，原 Attempt
确定性 FAILED 且不得加载/执行 guest；第二次检查后撤权不改写已接纳事实，当前 `Effect=none` guest 可在
§23 的 5 秒 ceiling 内收口。terminal 或 UNKNOWN exact retry 永远只读冻结事实，不进入上述执行链。

### 24.3 停机 Disable

R3 不提供热撤权。唯一支持的操作流程是：停止新 Admission，排空或取消已接纳请求，执行 Store-only
PENDING recovery，进程退出并销毁 Registry cache，离线 `module-disable` exact CAS，再从禁用后的
Catalog 重启。活动 `serve` 独占 Store 时离线 Disable 必须返回 `STORE_BUSY` 且零状态变化。

Disable 只拒绝后续 Admission，不修改 Installation、Activation、历史 Run/Attempt、result、receipt、
evidence 或 UNKNOWN。共享 Instance 的单 Binding Disable 只撤销该 Binding；最后引用 Disable 后，新进程
不得再物化 Adapter。移除 runtime allowlist 并删除历史 artifact 后，Pure Chat 仍必须对 WASM artifact、
loader、Adapter、guest 与 Action Attempt 零访问。禁用后的 Backup/Restore 保留历史与 disabled current；
恢复更早活动备份是 Operator 显式回滚，不是隐式在线重授权。

### 24.4 非目标与下一入口

R3 不声称防御 Go/wazero/OS 漏洞，不提供硬 CPU、fuel、instruction 或 RSS 上限，不强杀运行中 guest，
也不是 OS/container、seccomp、namespace、restricted token 或生产恶意多租户隔离。LOCAL_PROCESS、REMOTE
和 TRUSTED_IN_PROCESS 不因此获得相同隔离声明。OS 级 sandbox 属于 Beta 后候选，未来仍通过统一
Module/Port 扩展，不进入 Core 分叉。R3 当时的供应链下一入口已由 §5.2.1 的 W2-U0 纯合同覆盖；
R3 当时的下一入口已由 U0/U1/U2 与下述 U3 合同覆盖。W6、公开 Beta 与 `RELEASE_READY` 仍未批准。

## 25. W2-U3 离线 Upgrade Review Runtime 历史合同

历史收口状态：`W2_U3_UPGRADE_REVIEW_ACCEPTED_DEVELOPMENT_SLICE / W2_U4_OPERATOR_APPLY_NEXT`。旧
NEXT marker 不是当前入口。U3 是默认关闭的
可信本地 Operator 审核切片，不是 updater、background worker、Module Runtime 或第二控制器。它复用唯一
Current Store、U0 supply contracts、U2 discovery facts 与既有共享 Handler Apply 判断边界；没有第二 Runtime、
Store、Loop、Gateway、Catalog、Attempt/Usage ledger 或 handler registry。

### 25.1 显式 Admission 与 authority-free 输入

`module-upgrade-review` 和 `module-upgrade-decide` 都必须显式设置
`--enable-module-upgrade-review`。未启用、flags 不完整、scope 混用或非法路径时必须在 Store/file/Registry/
Host/network 之前失败。两个命令仅用于服务停止、唯一 Current Store 可独占的可信本地 Operator 工作流。

首片唯一 Candidate kind 为 `EXACT_VERSION_CHANGE`：current 与 target Module ID 必须相同，opaque exact
Version、ArtifactDigest 与 InstanceID 必须不同。Core 不解析 SemVer、`latest`、版本顺序或 downgrade；
`--operator-requested-rollback` 只记录显式意图，不改变 admission。U0 `INSTALL` Candidate 继续是兼容纯
合同，U3 不在缺少 current Binding/Catalog/Activation/Installation 时推断 scope 或制造 current。

Operator 必须显式提供：

- Tenant 与 exact `PROFILE` 或 `WORKSPACE_CHANNEL_ENDPOINT` target；PROFILE 不携带 Workspace；
- expected pointer revision、current Instance/Activation/ModuleRef/ArtifactDigest、selected Port 与 binding ordinal；
- exact SourceID、current SnapshotID、target ModuleRef/ArtifactDigest/InstanceID；
- 本地 unpacked target artifact；signed Source 另提供 artifact 外的 exact detached Signature。

Index `PackagePath` 不是执行输入，U3 不读取它、不访问 Source Provider、不联网、不下载 package。Version、
Manifest、Index、Signature 或 Candidate 都不能自授 Trust、ExecutionClass、Authority、Effect、Secret、grant
或 Apply 权限。

### 25.2 Review basis、Manifest evidence 与判断

Review basis 必须从唯一 Store 重建，顺序固定为：

```text
SourcePolicy → Index → current Snapshot → exact entry
selected consumer Binding → PublishedBasis
PublishedBasis Catalog Instance → exact Activation revision → Installation
all published Bindings that reference the same current Instance
```

调用方只提供 exact selectors，不能提供 canonical bytes 或 derived Candidate 代替该闭包。Store 先读取一个
coherent pre-I/O basis；artifact I/O 后在写事务中重新构造并逐字段比较 live basis，提交成功点才是 Review
admission linearization point。stale pointer/Control/Catalog/Activation/Installation/Source/Key/Snapshot、Tenant/
scope mismatch、target Instance collision 或 parent drift 必须零部分写。

target artifact 必须通过同一有界 conformance verifier 的两次 evidence scan。参与 ArtifactDigest 的 exact
canonical Manifest 必须逐字节相同；signed Source 必须使用 Store current exact Publisher Key 验证 detached
Signature。canonical target Manifest 以既有 content-addressed Manifest record 保存，Review 只引用
`manifest_ref`。不得保存或输出 artifact/signature/reason 的主机路径、Source URL、Index `PackagePath`、
Signature bytes、Public Key bytes、Secret、Config/Authority body 或可反推出 material 的值。

`module-upgrade-review/v1` 是 inert content-addressed projection，至少冻结：

- exact CandidateID/ReviewKey、Tenant/scope、SupplyBasis 与 PublishedBasis；
- current Activation/Installation/Manifest safe summary；
- target ModuleRef/ArtifactDigest/size/SignatureID/Manifest safe summary；
- 完整 runtime mode/protocol/entrypoint 与 Provides/Requires/requested-permissions diff；
- current Instance 的全部 published Binding impacts；
- exact handler assessment 与 digest-only required grants；
- `WOULD_APPLY | CONFLICT | UNSUPPORTED` 结论和确定性 reason codes。

首片没有 `NO_CHANGE`，因为 Admission 已要求不同 Version 与 ArtifactDigest。任一 target port removal、Require
变化、requested permission 扩大、unknown/conflicting handler、published Binding incompatibility、target
Instance collision 或所需 grant 都必须在结论/reason 中闭合。Review 只预测；它不执行 grant 或 Host。

### 25.3 Candidate、Decision 与抑制

Candidate 是全局不可变 supply observation；同 exact Candidate 可以形成不同 Tenant/scope Review。Review
绑定 exact current/target evidence；Decision 终态绑定 exact `review_id`，不得用“同 Candidate”替代
“同 Review”。Candidate/Review/Decision canonical bytes 与 IDs 都必须恢复验证，exact retry 只能返回原事实。

`APPROVE` 只允许 Review conclusion 为 `WOULD_APPLY`，且在同一写事务中重新构造 current basis；stale、
Key revoked 或已有同 Tenant ReviewKey REJECT 时失败。APPROVE 仍不是 reservation、stage、grant、Install、
Activation、Binding 或 Apply authority。

`REJECT` 是显式 Tenant-wide supply deny，不是 scope-local incompatibility。新 REJECT 必须同时携带
`--confirm-tenant-wide-reject`，并以 `{tenant_id, review_key}` 抑制该 Tenant 后续全部 PROFILE/Workspace
scope 的相同 exact Source/ModuleRef/ArtifactDigest；它不能污染另一 Tenant。scope-specific incompatibility
应记录 `CONFLICT/UNSUPPORTED`，不得自动升级为 REJECT。REJECT 可绑定 historical/stale Review，因为它
只拒绝供应候选；但不能改写历史 current facts。

### 25.4 零效果、恢复与 U4 入口

U3 不能创建或修改 Installation、Activation、Control、Catalog、Run、Attempt、Usage、grant、Host、
Gateway permit 或外部效果；不能 stage、Install、Activate、Bind 或 Apply。Pure Chat、普通 Run Admission、
Universal Loop 与启动恢复未显式选择 U3 命令时，对 Candidate/Review/Decision、Source、artifact、signature
与 handler assessor 均零访问。

完整 Backup/Verify/Restore 必须重建 U2 parents、Candidate、Review、Decision、Manifest content evidence、
PublishedBasis 与全部 historical Binding impacts。Publisher Key 后续撤销不改写 historical signed Review，
但阻止依赖该 current Key 的新 Review/APPROVE。任何 parent/Manifest/publication/Decision tamper 均失败；
Backup/Restore 不访问 Source、网络、artifact 或 Host，也不自动 Review/Decide/Apply。

U3 收口时 Store identity 为 32 tables / fingerprint
`37258c1939308be83f21a109e59a19e32946734d1c53b1b262f876dc36e47dbd` / migration 57,652 bytes /
SHA-256 `e4f1047eb527b65ab050d444062f3d286cc2ad87d56e60b1cd7f4e96df87652d`；U2 的 29-table 与
R1/R2/R3 的 24-table identity 只保留为历史事实。

U4 入口门已经把原 CLI 私有的 9 条 generic/Model policy 与 2 条 Document Insight reserved exact selector
提取为 Review 和 Apply 共同调用的唯一 `internal/modulehandler` 实现，未复制第二张 handler 表或形成
不同判断。该 Handler-gate 自己不消费 APPROVE；其旧 NEXT marker 仅表示进入下述 U4 合同。

## 26. W2-U4 approved Declarative Profile Context Apply Runtime 合同

状态：`W2_U4_OPERATOR_APPLY_ACCEPTED_DEVELOPMENT_SLICE / W6_0_CONTROL_API_CONTRACT_NEXT`。本合同只为
一个 exact APPROVE 的 Declarative Profile Context replacement 复用既有 Apply/CAS；Operator Module Apply
整体、Module Conformance 和广义装配成熟度不变。

### 26.1 命令 Admission 与明确非授权输入

`module-upgrade-apply` 默认关闭，必须显式设置 `--enable-module-upgrade-apply`。请求必须提供 closed Store、
artifact root、显式 `--source-root`、exact Tenant/ReviewID/DecisionID、target artifact 与显式 `--signature`
（unsigned Source 使用空值，signed Source 提供 detached Signature）。Decision 必须是该 exact Tenant/Review
的 APPROVE；不能用 Candidate、ReviewKey、
“最新审批”或相同 target 替代。

首片只接受 Store current Policy 冻结的 `LOCAL_DIRECTORY + DENY` Source。`--source-root` 必须按 historical
U1 verifier 与 exact OriginDigest/Policy 校验；Index `PackagePath` 永不成为输入。HTTPS、网络、下载、隐式
路径或来源 fallback 均失败关闭。

全部 LOCAL_PROCESS、TRUSTED_IN_PROCESS、REMOTE、WASM artifact、REMOTE endpoint/SecretRef 与 Model
SecretRef grant flags 必须显式出现且值为空。缺失 flag 与非空值都不允许。Manifest、Review、Decision、
Signature 或 Source 不能自授 grant、Trust、ExecutionClass、Authority、Effect、Secret、Binding 或 Apply
权限。

### 26.2 唯一支持的 replacement shape

exact Review 必须只有一个 BindingImpact，并满足：

```text
target kind      = PROFILE
port             = context.provide/v1
runtime          = DECLARATIVE
protocol         = static/v1
control          = TRUSTED_INSTRUCTION
authority        = deny-all
merge mode       = ordered multi, exact existing ordinal replacement
```

old/current 与 target Instance 必须不同，ModuleID 相同，exact Version 与 ArtifactDigest 不同。selected ordinal
必须正好引用 old Instance；old Instance 只能被该一个 Control Binding 引用；target 尚未存在于 current
publication。Model、Channel、Action、`SINGLE` PortPlan、shared-current、多 BindingImpact、fanout、额外
grant、Port/Requires/permission shape 漂移全部确定性失败关闭，不得 generic fallback。

canonical `module-apply-plan/v1` 冻结 `replace_current_instance_id`。Apply 只能在原 PortPlan ordinal 将 old
Instance 原位替换为 target；其他 Binding 的 canonical bytes 与顺序必须逐字节保持。old Catalog entry 被移除，
target exact entry 被加入；不创建第二 PortPlan、reducer、merge/fallback 语义或批量 Apply。

### 26.3 双时点重验与唯一 Apply/CAS

候选执行顺序固定为：

```text
load exact historical Review + APPROVE
→ build canonical exact replacement plan
→ historical U1 governed source verification
→ current Store approval/source/pointer/binding/activation revalidation
→ rebuild and byte-compare the same exact plan
→ current U1 governed source verification
→ pre-stage current revalidation
→ existing stage/install/activation/Control-Catalog CAS
```

Review 与 Apply 必须复用同一 `internal/modulehandler` pure evaluator。这里的“复用 Dry-run/Apply 语义”指
共享 evaluator、canonical plan 和 existing Apply/CAS；不得虚构一个独立 dry-run admission、第二审批状态、
第二 handler table、第二 Runtime/Store/Loop/Gateway/Catalog 或旁路 publication。

任一 historical/current Source Policy、Publisher Key/revocation、Snapshot head/entry、Review/Decision、target
Manifest/artifact/signature、PublishedBasis、pointer、Control/Catalog、Activation/Installation、selected binding
或 suppression 漂移都必须在 staging 前失败。pre-stage 后的 CAS 冲突仍由 existing Apply conclusion 精确分类。

### 26.4 Run 冻结、重入、UNKNOWN 与恢复

旧 Run 的 MemberExecutionSnapshot、ActivatedModuleRef 与 static context 不得改写；replacement 成功后，
新 Run 才冻结 target。相邻 current exact retry 可以在 publication 仍等于该 plan 的唯一下一 revision 时只读
返回相同 PlanDigest/Pointer/Control/Catalog，且零 Source I/O、零 staging、零新 mutation。它是 adjacent plan
idempotency，不是 durable approval receipt；任一后续 publication 发生后，旧审批重试必须返回
`POINTER_CONFLICT`。

若 stage/Install/Activate/Control-Catalog publication 的终态无法证明，必须保留 existing exact Apply 的 UNKNOWN
结论；不得重跑语义、换 Provider、换 target、创建替代 Attempt 或伪造 terminal success。terminal/UNKNOWN
exact retry 仍只读冻结事实。

完整 Backup/Verify/Restore 必须保存 U2 Source、U3 Candidate/Review/Decision、target Manifest、Installation/
Activation 与 final Control/Catalog publication 的 exact semantic closure；验证和恢复零 Source/网络/Apply。
Pure Chat 在未显式调用命令时对 Source、Review、Decision、artifact、stage 和 handler assessor 零访问。

U4 不新增 Schema/table；在该历史切片收口时 Store 仍为 32 tables / fingerprint
`37258c1939308be83f21a109e59a19e32946734d1c53b1b262f876dc36e47dbd` / migration 57,652 bytes /
SHA-256 `e4f1047eb527b65ab050d444062f3d286cc2ad87d56e60b1cd7f4e96df87652d`。

U4 的历史入口 `W6_0_CONTROL_API_CONTRACT_NEXT` 只允许冻结轻量控制 API 合同；不授权自动 upgrade
worker、在线 Apply、第二控制器、Model/Channel/Action upgrade、广义模块自由装配、公开 Beta 或生产部署。

## 27. W6-0 控制 API 纯合同 Runtime 历史边界

W6-0 当时状态：`W6_0_CONTROL_API_CONTRACT_ACCEPTED_DEVELOPMENT_SLICE /
W6_1_APPLICATION_SERVICES_READ_API_NEXT`。

W6-0 在 `internal/controlapicontract` 冻结六份 pure canonical wire：`control-session/v1`、
`control-scope/v1`、`control-view-snapshot/v1`、`control-operation-request/v1`、
`control-operation-receipt/v1` 与 `control-event-cursor/v1`。每份 wire 都有独立 digest domain、closed
enum、strict restore/canonical canary 与 defensive-copy。控制 Web view 必须使用
`control-view-snapshot/v1` 和 `freeagent.control-view-snapshot/v1`；本规格既有 Core
`control-snapshot/v1` 和 `freeagent.control-snapshot/v1` 不变，二者不得相互 restore 或混用摘要。

Operation request 必须显式冻结 `DRY_RUN/MUTATE` intent；动态 request ID 与请求/观察时间不进入
semantic request。Receipt 重复冻结 principal、scope、operation 与 intent，但消费者仍必须从 exact
frozen request 构造或逐字段核对 receipt。`UNKNOWN` 只能读取原 exact receipt，禁止重放语义、替换
operation/provider 或创建替代 receipt。

`internal/controlapipolicy` 只冻结 transport-neutral 的 strong ETag/If-Match、keyset pagination、
idempotency digest、body/结构 ceiling、并发 ceiling 与 loopback threat model。W6-0 没有 listener、HTTP
handler、在线 session store、SSE、UI、production `cmd/freeagent` wiring、第二 Runtime/Store/writer/Gateway
或任何 external effect。

当时源码为 39 份 Markdown、37 个源码 package、35 个 production dependency-closure package；Store
仍是 32 表且 identity 不变。`W6_1_APPLICATION_SERVICES_READ_API_NEXT` 当时只授权默认关闭的
read/Dry-run application services；该 marker 不授权在线 mutation 或 durable receipt。

## 28. W6-1 Application Services / Read API Runtime 边界

历史收口：`W6_1_APPLICATION_SERVICES_READ_API_ACCEPTED_DEVELOPMENT_SLICE /
W6_2_CONTROLLED_MUTATIONS_AUDIT_NEXT`。

Control 保持 opt-in。默认关闭路径必须沿用原单 Chat listener Runtime，不得构造 Control listener、handoff、
session/cursor registry 或 Control Application Services，也不得为 Control 读取 Store。显式启用的唯一
`freeagent serve` 生产 composition 按以下顺序接线：

1. 在 Composition/Store/listener I/O 前完成 Control flag 与非管理员身份验证；打开唯一既有
   Composition/Current Store；
2. 从该 Store 构造只读 Modules service 与 effect-free `MODULE_DISABLE` Dry-run service，并创建
   startup-closed shared Admission gate；
3. 绑定 Chat listener 和独立 exact `tcp4 127.0.0.1:0` Control listener，从已绑定 Control socket 派生
   actual origin；再构造授权 scope set、process-local bootstrap/session registry、process-local cursor
   registry、Control HTTP handler 与共享 shutdown coordinator；
4. 在 Admission 仍关闭时启动两个 server，完成 owner-only exclusive handoff 和 readiness publication；
   任一端提前退出或 handoff 失败都以零已接纳请求关停。仅在这些步骤全部成功后一次性打开 shared
   Admission；
5. 关停时同时关闭新 Admission 与两个 listener，有界排空共享 Runtime，取消剩余请求，只做一次
   Store-only recovery 并关闭唯一 Store；30 秒 budget 不创建第二 recovery/Store owner。

bootstrap capability 5 分钟且只交换一次；session absolute lifetime 为 8 小时、idle lifetime 为 30 分钟。
Cookie 是 host-only 而不绑定端口，因此除 bootstrap exchange 外，Modules GET 与所有非安全方法都必须
同时携带 exact session credential 和 session-bound CSRF header；GET 缺失或错误 CSRF 返回 401，且在
Application Service 前失败。

W6-1 的 Runtime consumer 仅包括 scope-filtered Modules list/detail、keyset cursor/strong ETag，以及 exact
If-Match 的 `MODULE_DISABLE` Dry-run。Dry-run 拒绝 `Idempotency-Key` 和 confirmation header，只计算
projection；其 `DRY_RUN` receipt 由 exact frozen request/basis 构造、逐字段复验并仅随响应返回。它不调用
domain mutation，也不复用既有停机 CLI Apply/Disable 作为在线写路径。

该 W6-1 历史原子当时的源码证据为 39 份 Markdown、44 个源码 package、44 个 production dependency-closure package；
Store 是 identity 不变的 32 表。没有 Control mutation、durable receipt/table、SSE、UI、第二
Runtime/Store/writer/Gateway、后台 worker 或主动预热。`W6_2_CONTROLLED_MUTATIONS_AUDIT_NEXT` 只授权
下一阶段审计；任何首个 mutation 仍须先冻结并验收 receipt 与第一条 domain persisted fact 的原子闭包。

## 29. W6-2 受控操作审计与 confirmation 历史 Runtime 边界

历史状态：`W6_2_CONFIRMATION_CONTRACT_ACCEPTED_DEVELOPMENT_SLICE /
W6_2_DURABLE_RECEIPT_SCHEMA_NEXT`。Audit 历史 marker 为
`W6_2_CONTROLLED_MUTATIONS_AUDIT_ACCEPTED_DEVELOPMENT_SLICE / W6_2_CONFIRMATION_CONTRACT_NEXT`。

六项 operation 审计只允许未来考虑一个无 artifact/network/runtime refresh 的窄
`MODULE_DISABLE`：TENANT scope、PROFILE binding、exact `context.provide/v1`、`OPTIONAL`、
`DECLARATIVE`、trusted-instruction config、deny-all Authority。其余五项因 caller-owned artifact ingress、
reservation/异步 Attempt/Task/Run 或 staging/install/activation/grant 长链 deferred。HTTP 不得调用会打开
第二 Store/recovery 的 CLI Apply/Disable；未来 Store seam 只能复用唯一 publication builder 与 transaction。

该历史 Runtime 原子只新增纯合同和 process-local authority：

1. stable `ControlConfirmationStatementV1` 绑定 MUTATE 的 principal/capability/operation/scope/key/input/
   operation-evaluation/expected ref；raw proof、Boot/session/auth revision/time 不进入稳定 digest；
2. `ModuleDisableEvaluationV1` 冻结完整 effect-free candidate projection，严格验证 basis/Binding/catalog
   矩阵，但不调用 Store writer、Gateway、artifact loader、Provider 或 runtime refresh；
3. confirmation proof 为 32 random bytes、TTL 最多 2 分钟且不超过当前 session absolute expiry、全局 256、每 session 8。进程内 registry 只存
   domain-separated digest 及当前 session authority binding，线性化 Issue/Claim；只有证明零持久效果才能
   release，任何可能效果或 commit ambiguity 后必须 consume；
4. proof/proof digest 不进入 Store、Backup、日志、URL 或 error；进程重启、Close 或 expiry 后失效。

当时证据为 39 份 Markdown、45 个源码 package、44 个 production dependency-closure package；Store 仍为
32 表。没有 confirmation HTTP endpoint、durable receipt row、mutation route、第二 Runtime/Store/writer、
SSE/UI/worker；纯 SQLite 第一 mutation 候选内 `UNKNOWN` 不可达。

下一入口只允许一张 append-only receipt 表的 32→33 rebuild-only Schema/Backup 候选、exact canonical
closure、caps/quotas/unique 与同事务 atomicity；该表和写接线都尚未实现。

## 30. W6-2 durable receipt Schema 历史 Runtime 边界

历史收口状态：`W6_2_DURABLE_RECEIPT_SCHEMA_ACCEPTED_DEVELOPMENT_SLICE /
W6_2_MODULE_DISABLE_MUTATION_WIRING_NEXT`。

本原子没有增加新的 Runtime dispatch、Gateway、Provider、Adapter loader、background worker 或在线 handler。
新增的共享 `module-apply-plan/v1` 与 `ModuleDisableEvaluationV1` neutral exact-basis replay 都是无 I/O 的 pure
合同；`module-disable-publication-receipt/v1` 只描述 future publication 的 authority-free domain 事实。

该历史原子当时的 Store 为 33 表，只增加 append-only `control_operation_receipts`：

- exact resolver 从 Request 派生 principal/scope/operation/idempotency identity，同 digest 返回 original
  receipt，同 identity 不同 digest 冲突；
- 公开 commit 只接受 neutral replay 已确认 `NO_CHANGE` 且 current PublishedBasis 未变化的记录；它不能调用
  Runtime、Gateway、artifact loader、Provider 或 publication；
- 在该历史原子中，`APPLIED` 只有 strict contract、DDL/restore/verifier 形状，没有公开 insert；下一 wiring 必须复用唯一
  publication owner，在同一 `BEGIN IMMEDIATE` transaction 内提交 domain receipt、Control receipt 与
  Control/Catalog pointer CAS；不得从 HTTP 调用会自行打开 Store/recovery 的停机 CLI；
- Backup Create/Verify/Restore 会在 coherent snapshot 内严格验证现存 receipt 并用 immutable parents 重放
  neutral evaluator。单表没有外部 completeness anchor，因此不能声称检测整行删除。

该历史 Store fingerprint/migration 为
`51be9081a815e6379380b93e745db03d33ca2f358962f85ace63bce2534dcc10`、67,998 bytes /
`8feea38beb505ab4f371afe32eecd877744062bd149d0d24982839c5450fc952`。该历史原子的证据为 39 份 Markdown、
47 个源码 package、46 个 production dependency-closure package；当时没有 mutation route/handler、confirmation
endpoint、APPLIED public insert、第二 Runtime/Store/writer/Gateway、SSE/UI/worker；其下一入口只允许
`W6_2_MODULE_DISABLE_MUTATION_WIRING_NEXT`。

## 31. W6-2 MODULE_DISABLE mutation wiring 历史 Runtime 边界

历史收口状态：`W6_2_MODULE_DISABLE_MUTATION_WIRING_ACCEPTED_DEVELOPMENT_SLICE /
W6_3_WEB_SHELL_READ_ONLY_OVERVIEW_NEXT`。

本原子不新增 Runtime dispatch、Gateway、Provider、Adapter loader、worker 或 prewarm。仅显式
`--enable-control` 的既有 Control composition 使用同一个已打开 Store，接通 confirmation/mutate 两路；
default-off 不初始化 proof authority/coordinator。首片只允许 TENANT/PROFILE exact
`context.provide/v1`、existing Binding `OPTIONAL`、`DECLARATIVE static/v1`、trusted-instruction config 与
deny-all Authority；其余 mutation 和 runtime 形状失败关闭。

durable resolver 在 proof/current-basis 前执行；但每个 HTTP retry 仍必须重新通过当前 Origin、session、
session-bound CSRF 与 TENANT Permit。exact hit 的含义仅是不依赖旧 proof 或旧 session identity，不是匿名
resolver。miss 才 claim 最多 2 分钟且不超过当前 session 绝对 expiry 的 process-local proof、服务端重建
evaluation，并在 commit 前复验 current authorization 与 basis。

`NO_CHANGE/APPLIED` 都写 durable Control receipt；APPLIED domain receipt、Control/Catalog publication 与
pointer CAS 在同一 `BEGIN IMMEDIATE` transaction 内提交。它不执行 Provider/Gateway、artifact load、旧 Run
改写或 UNKNOWN replay；纯 SQLite 首片 UNKNOWN 不可达。Backup 复验现存 APPLIED 与 parent/replay/domain
tamper；无 external completeness anchor，不能发现任意整行 receipt 删除。forced receipt-insert failure 时
publication 全事务 rollback 是 publication-without-receipt 的原子性证据。

该历史原子当时为 39 份 Markdown、48 个源码 package、48 个 production dependency-closure package；Store
为 33 表且 fingerprint/migration 不变。没有其他 mutation、SSE/UI/worker、第二 Runtime/Store/writer/Gateway；
当时唯一下一入口是 `W6_3_WEB_SHELL_READ_ONLY_OVERVIEW_NEXT`。

## 32. W6-3 Web Shell / read-only Overview 历史 Runtime 边界

历史状态：`W6_3_WEB_SHELL_READ_ONLY_OVERVIEW_ACCEPTED_DEVELOPMENT_SLICE /
W6_4_MODULES_CONFIGURATION_UI_NEXT`。

W6-3 不新增 dispatch、Gateway、Provider、Adapter loader、scheduler、worker 或业务 mutation。它只在显式
`--enable-control` 的既有 composition 中，把同一 Current Store 的 `LoadControlOverviewV1` 接到
`OverviewServiceV1` 与 `GET /control/api/v1/overview`，并由 exact embedded static resolver 提供 Web Shell。
default-off 路径不构造这些可选依赖，既有 Runtime/Chat/Admission 生命周期不变。

Overview reader 使用一个只读事务和固定 item limit，读取 Workspaces、Runs、UNKNOWN、Learning、Module
Candidates 与 Usage；application service 在 Store 返回后重验 scope ownership、current basis、状态机、排序、
caps、causal intersections 与 source clock，构造 section/view/projection digests 和 strong ETag。HTTP adapter 在
编码前以同一 live Permit 再次验证 detached result；浏览器再验证 exact schema、scope、basis 与 digests。
任何层都不能用 Overview projection 作为 Control/Catalog publication、Run transition 或 retry authority。

该历史切片 Store 为 41 tables / 23 indexes / 56 triggers，fingerprint
`87d63a2e468ee27ee2c0e1918a92e580532825bde85dbb872b9c795bf577f2c1`，migration 143,588 bytes /
SHA-256 `5bd9743e988b82750f785c015f07c11fbcf6025da70148405aec11106b4efb22`。无业务 mutation UI、SSE、
durable client cache、第二 Runtime/Store/writer/Gateway、外部效果或主动预热。其历史下一入口为
`W6_4_MODULES_CONFIGURATION_UI_NEXT`。

## 33. W6-4 Modules configuration UI 历史 Runtime 边界

历史状态：`W6_4_MODULES_CONFIGURATION_UI_ACCEPTED_DEVELOPMENT_SLICE /
W6_5_SERVER_OWNED_MODULE_ARTIFACT_INGRESS_NEXT`。

W6-4 不新增 Runtime service、Store dependency、writer、dispatch、Gateway、Provider、Adapter loader、scheduler
或 worker。它只把既有 `ListModulesV1` / `GetModuleV1` 与 `MODULE_DISABLE` dry-run、confirmation、mutate
HTTP surface 交给 exact embedded Web Shell 消费；default-off composition、同一 loopback listener、session、
Origin/CSRF、Permit 与 current PublishedBasis authority 均不变。

浏览器严格复验 scope、source/view/basis、projection、ETag、cursor、evaluation、statement 与 durable receipt；
只有 TENANT/PROFILE `OPTIONAL` `context.provide/v1` 的既有窄候选能进入两次显式确认。proof 只发送一次，
不可判定结果只允许同 body/key/If-Match/evaluation 的 exact retry；可信成功后只 invalidate/refetch，不在客户端
构造 publication。UTF-8 scope 与 instance ID 使用显式 canonical raw-base64url carrier，marker 缺失时保留旧 raw
语义，未知/重复/noncanonical carrier 在 Application Service 前失败关闭。

W6-4 没有 artifact ingress、staging/install/activation、grant、其他 mutation、远程 listener、SSE、worker、
第二 Runtime/Store/writer 或自动升级。`W6_5_SERVER_OWNED_MODULE_ARTIFACT_INGRESS_NEXT` 是其历史下一入口，
不因本节自动获得实现或发布许可。

## 34. W6-5 server-owned module artifact ingress 历史 Runtime 边界

W6-5 收口时状态：`W6_5_SERVER_OWNED_MODULE_ARTIFACT_INGRESS_ACCEPTED_DEVELOPMENT_SLICE /
W6_6_SERVER_OWNED_MODULE_UPGRADE_REVIEW_NEXT`；W6.6 已在本节后收口，当前下一入口为
`P2_CONTROL_UI_I18N_ACCEPTED_DEVELOPMENT_SLICE`；当前下一入口为 `P3_SECOND_PROVIDER_NEXT`。

W6-5 不新增在线 Runtime route、Control operation、listener、Provider、Gateway、Adapter loader、scheduler、
worker 或 dispatch。唯一 composition 是默认关闭的可信本地 Operator CLI `module-artifact-ingress`；只有显式
`--enable-module-artifact-ingress` 才打开既有 Current Store 并执行一次有界离线 ingress。default-off、非法或
多余参数路径不得读 Source、发布文件或构造 Host/Runtime。

调用方只提供 exact Store selector 与瞬时 `source-root` / `artifact-root`；不提供 package path、URL、signature、
upload、HTTP 或 UI 输入。Core 从 Store-owned current Snapshot 重建 package path 与 SourcePolicy closure，且只接受
unsigned `LOCAL_DIRECTORY + DENY`。完整 conformance/digest/size/file-count 验证后，filesystem 先通过 hidden stage、
sync、digest-addressed no-replace publication 建立 durable inert object；随后唯一 Store 在一个
`BEGIN IMMEDIATE` 中重验 basis 并提交 immutable Artifact 与 append-only Admission。所有 source/artifact/Backup
descendant 必须相对 held parent handle 打开，并拒绝 link/reparse、hardlink、跨 filesystem/volume 与 identity/
change-time/namespace 漂移。artifact root、child 与全部 namespace ancestors 必须可信、私有且不可被低权限主体替换；
跨进程 root lease 覆盖 crash-stage recovery、publish、sync、复验与 root 级 256 trees / 512 MiB covered
content / 32,768 paths / 16,384 files / 16 MiB canonical path-name bytes 硬预算；这些预算不声称等于实际
filesystem allocation blocks。

新 ingress 的同 digest 目标缺失时必须始终以目录 `0700`、文件 `0600` 发布。既有同 digest 目标只有在完整
bytes/mode 复验后才可复用，且只接受两种 root-global 闭包：全部目录 `0700`、全部文件 `0600`；或全部目录
`0700`，仅 canonical `LOCAL_PROCESS + mcp-stdio/2025-11-25` descriptor 精确绑定的唯一 executable 文件为
`0700`，其余文件 `0600`。两种闭包都拒绝 special/setid/sticky 与 group/world 权限。artifact root 跨 Store
共享时，模式判定不得依赖任一单独 Store 的 Installation；后一种只是既有物理兼容态，不授予当前 Store
Installation、Activation 或 execution authority。W6-5 的 inert 精确定义为无 Store authority 且本切片不执行，
不等同于 OS executable bit 必须不存在。

Store commit 结果不明时不得猜测删除已发布 bytes；对象保持无 authority 的有界 inert orphan。只有 durable exact
Admission 可以在 head 前进后恢复原 selector；未提交 stale selector不能获得 authority。以后任一当前合格 selector
声明同一 digest 时仍必须先完成全包复验，才能复用该对象。

Artifact/Admission 不创建 Installation、Activation、Binding、grant、Review/Decision、Apply、Run、Attempt、
Secret、Host 或 execution，也不改变现有 Control/Web Shell。Backup artifact closure 是 installed 与 ingressed
digests 的去重并集；Source 已移除且没有 Installation 时仍能离线 Verify/Restore，恢复不联网、不重读 Source、
Install、Activate 或 execute；临时 DB/bundle/restore tree 使用可信私有 staging，copy 只读 held handles，并同步
publish 与 cleanup 涉及的 parents。W6-5 历史 FAC1 Store 为 43 tables / 25 explicit indexes / 64 triggers，fingerprint
`47981b9bf147ec81c6e98382f281db0d5395187a1f090f7ce183c116fba82a0d`，migration 150,301 bytes /
SHA-256 `6def433a59fa8f4876894572f1610cae499abc5b389ab5930ea697055419ca86`。

`W6_6_SERVER_OWNED_MODULE_UPGRADE_REVIEW_NEXT` 是 W6-5 收口时的历史 marker。W6.6 当前只允许
默认关闭的可信本地 server-owned Review/Decision 入口：从 immutable Admission 读取并复验 Artifact/Manifest
closure 与当前 basis，持久化 Review/Decision，并按 content identity exact retry。调用方不能提交 artifact path、
URL、signature bytes 或 target facts；跨 tenant、stale basis、物理篡改和不合格 Decision 失败关闭。Review/Decision
不自动 Install、Activate、Bind、Grant、Apply、Execute，不调用 Provider，也不创建 Runtime/Attempt/Usage/Effect。

当前 FAC2 为 UserVersion 2、42 tables / 26 explicit indexes / 64 triggers，fingerprint
`d5d876f327dc29dc6f4a10476652641172ab8e1f0451a8714fc450f58733541e`；`0002_server_owned_review.sql` 为
7,173 bytes / `3091a49ebcf724f573f91cc0fd22a7c58ebb52fa9d7ed552e32b6526ebeca3cb`。W6.6 已验收，当前下一阶段
为 `P2_CONTROL_UI_I18N_ACCEPTED_DEVELOPMENT_SLICE / P3_SECOND_PROVIDER_NEXT`；Review/Decision 仍不属于 Control UI mutation。
