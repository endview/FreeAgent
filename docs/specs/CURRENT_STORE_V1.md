# CURRENT_STORE_V1

> Current phase override (2026-09-24): W6.6, P2, the exact `zhipu` / `glm-4.5` P3 Provider, and the read-only P4 management UI are accepted development slices. `v0.1.1` is a published Developer Preview, not production support or a public Beta. P3 adds no Store table or writer; see [`CURRENT_CAPABILITIES`](../CURRENT_CAPABILITIES.md).

状态：**S1 Schema Draft / S1–W5 已验收开发基线 + W2-D 本地显式装配 v1、W2-E2 受信 `text.stats` 统一 Apply、W2-E3 Workspace Channel Apply、W2-E4 DeepSeek Model replacement、W2-E5-A Requires/permission grant、W2-E5-B Document Insight 双 Port、W2-R1 窄 REMOTE Action Host、W2-R2 窄 WASM Action Host、W2-R3 第三方纯计算/停机撤权、W2-U2 Source/Snapshot observation、W2-U3 Upgrade Review、W2-U4 approved Declarative Profile Context replacement、W6-0 控制 API 纯合同、W6-1 Application Services / Read API、W6-2 confirmation/durable receipt/MODULE_DISABLE wiring、W6-3 Web Shell/read-only Overview、W6-4 Modules configuration UI、W6-5 server-owned module artifact ingress 与 W6.6 server-owned Upgrade Review、P2 Control UI i18n、P3 第二 Provider、P4 只读模块管理 UI 已验收开发切片；当前下一入口为 `P5_BETA_GATE`。P3 不新增 Store 表或 writer。FAC2 当前 UserVersion 为 2；首次公开发布前仍可显式重建**
Machine status: `S1_SCHEMA_DRAFT_DEVELOPMENT_BASELINE_ACCEPTED`  
Schema status: `S1_SCHEMA_DRAFT`  
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
W4 Learning status: `W4_L4_LEARNING_CYCLE_ACCEPTED_DEVELOPMENT_SLICE / W4_COMPLETE_ACCEPTED_DEVELOPMENT_SLICE`  
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
W6.6 server-owned Upgrade Review status: `W6_6_SERVER_OWNED_MODULE_UPGRADE_REVIEW_ACCEPTED_DEVELOPMENT_SLICE / P2_CONTROL_UI_I18N_ACCEPTED_DEVELOPMENT_SLICE / P3_SECOND_PROVIDER_ACCEPTED_DEVELOPMENT_SLICE / P4_MODULE_MANAGEMENT_UI_READ_ONLY_SLICE_ACCEPTED_DEVELOPMENT_SLICE / P5_BETA_GATE`
Implementation status: `S1_ACCEPTED_DEVELOPMENT_BASELINE / S2.1_CONTEXT_COMPILER_ACCEPTED_DEVELOPMENT_SLICE / S2_MODEL_PROFILE_ACCEPTED_DEVELOPMENT_SLICE / S2_RAG_ACCEPTED_DEVELOPMENT_SLICE / S2_MEMORY_ACCEPTED_DEVELOPMENT_SLICE / S2_ACTION_GATEWAY_ACCEPTED_DEVELOPMENT_SLICE / S2_MCP_LOCAL_PROCESS_STDIO_TOOL_ACCEPTED_DEVELOPMENT_SLICE / S2_CHANNEL_ACCEPTED_DEVELOPMENT_SLICE / S2_PARENT_CHILD_COMPOSITE_ACCEPTED_DEVELOPMENT_SLICE / S3_FAIR_SCHEDULER_ACCEPTED_DEVELOPMENT_SLICE / S3_COMPOSITE_REVIEW_GATE_ACCEPTED_DEVELOPMENT_SLICE / W2_D_LOCAL_ASSEMBLY_ACCEPTED_DEVELOPMENT_SLICE / W2_E2_TRUSTED_TEXT_STATS_APPLY_ACCEPTED_DEVELOPMENT_SLICE / W2_E3_WORKSPACE_CHANNEL_APPLY_ACCEPTED_DEVELOPMENT_SLICE / W2_E4_DEEPSEEK_MODEL_REPLACEMENT_ACCEPTED_DEVELOPMENT_SLICE / W2_E5_A_REQUIRES_PERMISSION_GRANT_ACCEPTED_DEVELOPMENT_SLICE / W2_E5_B_DOCUMENT_INSIGHT_DUAL_PORT_ACCEPTED_DEVELOPMENT_SLICE / W2_R1_REMOTE_ACTION_HOST_ACCEPTED_DEVELOPMENT_SLICE / W2_R2_WASM_HOST_ACCEPTED_DEVELOPMENT_SLICE / W2_R3_UNTRUSTED_MODULE_ISOLATION_ACCEPTED_DEVELOPMENT_SLICE / W2_U2_DISCOVERY_SNAPSHOT_ACCEPTED_DEVELOPMENT_SLICE / W2_U3_UPGRADE_REVIEW_ACCEPTED_DEVELOPMENT_SLICE / W2_U4_OPERATOR_APPLY_ACCEPTED_DEVELOPMENT_SLICE / W1_COMPLETE_REAL_DEEPSEEK_50 / W4_L1A_KNOWLEDGE_PROPOSAL_STORE_ACCEPTED_DEVELOPMENT_SLICE / W4_L1B_STATIC_SKILL_PROPOSAL_STORE_ACCEPTED_DEVELOPMENT_SLICE / W4_L2_LEARNING_REVIEW_ACCEPTED_DEVELOPMENT_SLICE / W4_L3_LEARNING_VERSION_ACCEPTED_DEVELOPMENT_SLICE / W4_L4_LEARNING_CYCLE_ACCEPTED_DEVELOPMENT_SLICE / W4_COMPLETE_ACCEPTED_DEVELOPMENT_SLICE / W5_X1_WORKSPACE_TRANSFER_ACCEPTED_DEVELOPMENT_SLICE / W5_F1_COLLABORATION_STABILITY_ACCEPTED_DEVELOPMENT_SLICE / W5_COMPLETE_ACCEPTED_DEVELOPMENT_SLICE / W6_1_APPLICATION_SERVICES_READ_API_ACCEPTED_DEVELOPMENT_SLICE / W6_2_CONFIRMATION_CONTRACT_ACCEPTED_DEVELOPMENT_SLICE / W6_2_DURABLE_RECEIPT_SCHEMA_ACCEPTED_DEVELOPMENT_SLICE / W6_2_MODULE_DISABLE_MUTATION_WIRING_ACCEPTED_DEVELOPMENT_SLICE / W6_3_WEB_SHELL_READ_ONLY_OVERVIEW_ACCEPTED_DEVELOPMENT_SLICE / W6_4_MODULES_CONFIGURATION_UI_ACCEPTED_DEVELOPMENT_SLICE / W6_5_SERVER_OWNED_MODULE_ARTIFACT_INGRESS_ACCEPTED_DEVELOPMENT_SLICE`  
适用范围：FreeAgent Current Store、W2-D 本地显式装配 v1、W2-E2/E3/E4/E5-A/E5-B/R1/R2/R3/U2/U3/U4 窄增量、Pure Chat/Context/ModelProfile/RAG/Memory/Action/窄 MCP 本地 Tool/窄 REMOTE Action/窄 WASM Action/显式 Source/Snapshot observation/离线 Upgrade Review/approved Declarative Profile Context replacement/可选 Channel、Parent/Child + Composite、默认关闭的公平 Scheduler、可选 Reviewer、W5 单轮 repair Decision family、同 Tenant 跨 Workspace Transfer 及其 F1 公平/取消/恢复稳定性、W1 Conversation、W4 Learning 开发切片、W6-1 默认关闭的只读/Dry-run Control consumer、W6-2 process-local confirmation authority、durable receipt Schema、MODULE_DISABLE operation commit、NO_CHANGE/APPLIED 同事务 publication、W6-3 Overview observation closure、W6-4 对既有 Modules/Disable surface 的浏览器 consumer、W6-5 server-owned inert Artifact/append-only Admission 与 installation∪ingress Backup closure、W6.6 server-owned Review/Decision closure，以及一次性干净切换
配套规格：`CORE_RUNTIME_V1`、`CUTOVER_ACCEPTANCE`

> 金额边界：`P0_MONEY_BUDGET_REMOVED_FAC2_BASELINE`；当前 Store 语义已删除 `model_price_snapshots` 表、`BudgetPolicy`/`CostPolicy` 与全部金额列，`LoopFrame` 只保留 `UsageLedgerRef`。本文件中出现的 `CNY`、估算费用与 PriceSnapshot 字样一律是删除前 FAC1 时期的冻结历史验收证据，不描述当前运行语义；冻结记录见 [`P0_BASELINE_FREEZE`](../P0_BASELINE_FREEZE.md)

本文中的“必须”“不得”“应当”均为实现约束。

## 1. 目标与边界

Current Store 是 FreeAgent Core 唯一可写的权威事实库。已验收基线只持久化真实
生产链路需要的状态；当前开发 Schema 另含 W4-L1A/L1B 的最小 Knowledge/静态 Skill Proposal
候选事实、W4-L2 Reviewer 引用和状态/revision、W4-L3 一对一不可变 Version，以及 W4-L4
默认关闭的 Schedule 与不可变逻辑窗口 Task。W5-D1 在原 Composite 图上预冻结一次有界 repair
round；W5-X1 允许同 Tenant 的 Child 按冻结计划绑定另一 Workspace，并用双边 grant 与不可变
Transfer envelope 传递最小摘要/结果；W5-F1 在同一 Store/Scheduler/Attempt/Backup 边界内收口
公平、稳定归位、取消、重启、UNKNOWN 原 Attempt 对账和权限负例。W4 与 W5 的计划内开发切片
完成仍不表示 W2 通用装配总工作包、后台 Worker、自动治理、生产 SLA、W6/W7 或正式部署完成。
下图包含当前已验收能力边界：

```text
Control/Seed
  → Install / Activate / RuntimeCatalog
  → MemberExecutionSnapshot / RunManifest
  → Run / LoopFrame / History
  → optional Conversation head / predecessor chain
  → optional ModelProfile CONFIG
  → optional Agent Memory revision
  → optional Knowledge/Static Skill Learning Proposal
  → optional Learning Reviewer ordinary Run / Model Attempt / Usage / MODEL_UNKNOWN
  → optional approved Learning Version projection / inert Operator handoff artifact
  → optional disabled-by-default Learning Schedule / immutable logical-window Task
  → explicit Tick / ordinary proposer Run / Store-only reconcile / read-only report
  → Context Compilation / Model Request
  → ModelDispatchAttempt / MODEL_UNKNOWN
  → optional ActionProposal / DispatchAttempt / UNKNOWN
  → optional Channel ingress receipt / Cursor projection
  → optional CHANNEL_SEND / DispatchAttempt / UNKNOWN
  → optional atomic Parent/Child family / Child results / cancellation latch
  → optional Reviewer Run / canonical Verdict / APPROVE-only merge gate
  → optional W5 Decision graph / one bounded repair round / second Reviewer
  → optional bilateral Workspace REQUEST / RESULT transfer envelopes
  → Usage / Cost
```

本 Draft 追求三个目标：

1. 用一份新 identity 和一份 `0001_current.sql` 完成干净切换。
2. 让模型调用前后均存在可恢复边界，且 UNKNOWN 不被语义重放。
3. 只创建已启用纵向工作包中有真实生产消费者的表，不为后续能力预建空结构。

历史 S1 基线明确不包含：

- RAG、Memory、Action、Context Summary 和可变 Module State。S2 RAG、Memory 与
  Action 现按本文冻结合同加入同一 Context/Store/Loop 链，不改变默认 Pure Chat 行为。
- Channel、Learning 和公平 Scheduler。当前 S2 Channel 开发切片已将 Cursor/Event
  投影与 `CHANNEL_SEND` 接入同一个 Current Store/Loop/Gateway，但不把它们追溯为 S1；
  MCP 同样不属于 S1，当前只接入下述窄 `LOCAL_PROCESS + stdio + Tool-only` 开发切片；
  W4-L1A/L1B 当前开发切片只加入 Knowledge/静态 Skill Proposal 的最小候选表；已验收的
  W4-L2 只扩展同一表并复用普通 Run/Attempt/Usage/UNKNOWN，两者均不得追溯为 S1。
- Parent/Child Run 与复合 Agent 不属于历史 S1；其第一实现切片现已在同一 Current Store
  验收。动态任务图、公平 Scheduler、Reviewer、Learning 与跨 Workspace
  TransferEnvelope 当时均未进入该切片；W4-L1A/L1B Proposal 与后续 W4-L2 审核投影
  不改写这项历史事实。

其余延后能力在 S2 有真实消费者时再加入同一个 Current Store。不得因此建立第二套
Store、第二套 Ledger 或旁路事实源。

`CORE_RUNTIME_V1` §7.8 的首个 MCP 开发切片已按 Scheme A 接入并验收。它不把 MCP
加入 S1，也不声明完整 MCP 或生产就绪：该切片只复用 Action/Gateway/
`dispatch_attempts` 的现有事实，且只有显式 Operator exact ArtifactDigest grant 的
`LOCAL_PROCESS + stdio + Tool-only` Action Adapter 可以 Activate。REMOTE/HTTP 及其他
MCP 能力仍不得借该 MCP 切片接入。W2-R1 后续只按 §26 为同一 Action/Gateway/
`dispatch_attempts` 纵链增加一个独立、默认关闭的
`REMOTE/freeagent-action-http/v1` Host；它不把 REMOTE/HTTP 追溯为 MCP 能力，也不开放其他
REMOTE、完整 MCP 或 WASM。

## 2. 核心不变量

1. 一个运行中的 FreeAgent 部署只有一个物理 SQLite owner 和一个可写
   Current Store。
2. Core Store API 是唯一写入口；Module、Provider、Observer 和 Channel adapter
   不得直接写库。
3. 同一权威事实只归属一张表。其他位置只能保存
   `Ref + ExactVersion/Revision + Digest` 或经过校验的查询投影。
4. `MemberExecutionSnapshot`、`RunManifest`、Activation 和已发布 Catalog
   不可变；修改必须创建新 revision/generation。
5. 所有影响恢复的 JSON 使用 `CORE_RUNTIME_V1` 定义的 Canonical JSON。
6. 所有摘要使用小写十六进制 SHA-256，并带明确 domain separator。
7. `NULL` 表示 UNKNOWN；已知的数值零才写 `0`。
8. 任何实际网络模型调用前必须提交 `ModelDispatchAttempt=PENDING`；若冻结 deadline
   在事务内已过期，则直接提交 FAILED 终态且绝不发网。
9. `MODEL_UNKNOWN` 只能对账原 Attempt，不得新建等价 Attempt、自动重放或切换
   Binding。
10. 任何 Action executor 或 Channel sender 调用前必须提交
    `DispatchAttempt=PENDING` 与对应的完整 Proposal；`UNKNOWN` 只更新原 Attempt，
    不得重新 Prepare/执行/发送、换 Binding/Proposal 或自动生成新 Run。
11. 进程启动不迁移、不修复、不清空、不自动导入 seed。
12. 新程序只打开本规格的新 identity；任何其他 identity 一律 fail-closed。
13. 旧 Store、旧 migration、legacy decoder、shadow/evidence 表和双写不属于
    Current Store。
14. 每个 Run 始终恰好一个 Member；Composite 是一个 Parent Run 加 2..8 个 Child Run，
    整个 family 必须在同一事务原子发布，不能留下孤立成员。
15. 当前 family 必须同 Tenant、深度 1，且每个 Run 仍只绑定一个 Workspace。Root 与 Reviewer
    留在 Root Workspace；Child 可以按冻结计划绑定另一 Workspace，并冻结不同 Agent/Profile。
    Parent manifest 是唯一有序的完整物理图根。
16. family cancellation 是 first-write-wins latch，只向冻结 Child 图向下传播，并在每次
    新 permit 前检查；它不得覆盖 Model/Dispatch PENDING、MODEL_UNKNOWN 或 UNKNOWN。
17. W1 Conversation 只在本 Current Store 增加一个 head/CAS 事实；聊天 Session 不是
    持久实体，不能建立 Session Store、表、Runtime 或恢复协议。
18. 一个 Conversation turn 对应一个装配不可变 Run。只有成功 head 可以通过 CAS 接受
    下一 turn；PENDING、UNKNOWN、FAILED 或 CANCELLED head 均 fail-closed。
19. Learning Reviewer 必须是与 proposer 同 Tenant、同 exact Workspace，但 Run、Member、
    Agent logical ID 与 Profile logical ID 均独立的普通 one-Member、model-only Run；不得包含
    Conversation、Composite、Channel、Action 或第二个 Binding。
20. Learning Review 只允许同一 Proposal 的唯一 `review_run_id` 与唯一
    `reviewer_attempt_id`。`REVIEW_UNKNOWN` 只能由原 Model Attempt 的可靠 reconciliation
    evidence 收口；不得创建替代 Run/Attempt、切换 Reviewer/Provider 或语义重放。
21. Learning Schedule canonical 创建后不可变且默认关闭；启用/禁用只允许使用 exact revision
    CAS。未显式调用 Tick 时，Pure Chat、Store open、Backup/Restore 与启动恢复均不得扫描或执行。
22. 每个到期逻辑窗口最多一个 Task；错过多个周期只合并为最新到期窗口，同一 Schedule 同时最多
    一个 `PENDING|RUN_ADMITTED`。Task Admission 与普通 Run 发布必须位于同一事务。
23. Learning Task `UNKNOWN/2` 只允许对账原 Model Attempt 并收口到 revision 3；不得新建 Run、
    Attempt、切换 Agent/Profile/Provider 或语义重放。
24. Learning reconcile 只能收口既有 Store 事实；report 只能生成不持久化的半开窗 canonical
    投影。两者均不得构造 Loop、Provider、Gateway、Secret resolver、Module Host 或网络调用。
25. W5 Decision family 在 Admission 时必须预发布完整 `2N+3` 物理 Run：初始 N 个 Specialist、
    Reviewer0、N 个 repair Specialist、Reviewer1 与 Root。未激活或被跳过的 repair Run 仍是
    不可变恢复事实，但不得伪造 Model Attempt 或 Usage。
26. 跨 Workspace REQUEST 与 RESULT 必须分别验证方向正确的 source-send/target-receive 双边
    grant。历史 grant 是已发布 Run 的冻结恢复事实；当前 Control 只可 deny-only 撤权，不得
    替换历史 grant、重编译或重新路由。
27. Workspace Transfer payload 只允许 `TASK_SUMMARY` 与 `SPECIALIST_RESULT`；不得传递完整
    History、Memory、知识库正文、Secret 或 SecretRef。UNKNOWN 只能对账原 Attempt，禁止
    语义重放、换 Provider/Binding 或创建替代 Run。

## 3. Store identity

### 3.1 S1 historical identity

S1 已冻结并实现下列身份：

| 字段 | 值 |
|---|---|
| `schema_identity` | `github.com/endview/freeagent/current-store-v1` |
| SQLite `application_id` | `0x46414331`，十进制 `1178682161`，ASCII `FAC1` |
| SQLite `user_version` | `1` |
| `generator_id` | `freeagent-current-store-draft-v1` |
| migration 文件 | 仅 `0001_current.sql` |

`application_id`、`user_version`、`schema_identity`、`schema_fingerprint` 和
`generator_id` 必须同时匹配。只匹配其中一项不能视为 Current Store。

### 3.1.1 Current FAC2 identity

P0 金额退场建立 FAC2，W6.6 通过显式前向 migration 将其推进到 UserVersion 2：

| 字段 | 当前值 |
|---|---|
| `schema_identity` | `github.com/endview/freeagent/current-store-v2` |
| SQLite `application_id` | `0x46414332`，十进制 `1178682162`，ASCII `FAC2` |
| SQLite `user_version` | `2` |
| `generator_id` | `freeagent-current-store-v2` |
| migration 文件 | `0001_current.sql`（字节冻结）与 `0002_server_owned_review.sql` |
| 当前 schema fingerprint | `d5d876f327dc29dc6f4a10476652641172ab8e1f0451a8714fc450f58733541e` |

当前 FAC2 为 42 ordinary tables、26 explicit indexes、64 triggers。`0001_current.sql` 的
bootstrap digest 为 `dbc3e724a1f7c030677c84a77a317f69ef2fe246985cc749559a9f3dd5a6dc5a`
（149,239 bytes）；`0002_server_owned_review.sql` 为 7,173 bytes，digest 为
`3091a49ebcf724f573f91cc0fd22a7c58ebb52fa9d7ed552e32b6526ebeca3cb`。普通 Runtime open 不执行
migration；v1→v2 只允许在 offline owner lease 下先完成并验证 backup，再执行连续前向步骤，失败关闭。
未知或未来 UserVersion、非 FAC2 application_id 与任何 fingerprint drift 均拒绝。

### 3.2 初始化与打开

当前实现提供互相分离的入口：

```text
InitFreshCurrentStore
OpenExistingCurrentStore
VerifyCurrentStoreReadOnly
PrepareClosedCurrentStoreForPublication

currentbackup.CreateBundle
currentbackup.VerifyBundle
currentbackup.RestoreBundle
currentbackup.VerifyCurrentStoreSemanticClosure
```

- `InitFreshCurrentStore` 只能操作显式指定的全新路径；目标已存在时拒绝覆盖。
- 新建 SQLite 文件必须处于 `application_id=0`、`user_version=0` 且没有应用对象
  的空白状态，随后在一个初始化事务中执行 `0001_current.sql`。
- `OpenExistingCurrentStore` 只验证并打开完全匹配的新 Store，不执行 migration、
  seed、repair 或 recovery mutation。
- `VerifyCurrentStoreReadOnly` 使用只读连接，不能经过会写入的普通启动路径。
- `PrepareClosedCurrentStoreForPublication` 只把已经关闭且已验证的 FAC2 Store
  整理为可原子发布的单文件；它不导入 seed，也不启动 Runtime。
- `VerifyCurrentStoreSemanticClosure` 使用只读连接执行与完整 bundle 相同的数据库语义
  闭包检查，不加载 artifact、Adapter 或 Secret；显式 Channel composition 在启动恢复后、
  构造 Adapter 前调用它。
- 普通服务入口不得把空文件自动初始化为 Current Store。

生产初始化只允许通过显式命令
`freeagent init --db <new.sqlite> --seed <seed.json>` 进入；`chat`、`serve` 和普通
`OpenExistingCurrentStore` 遇到缺失数据库时都必须失败，不能隐式初始化。

任何不匹配的新旧数据库都返回结构化 identity 错误。新二进制不得探测旧版本后
选择 decoder，也不得把其他 `application_id` 改写为 `FAC2`。FAC1 数据库不迁移、不修改，
由旧版本或外部 operator-held archive 保全。

### 3.3 Schema fingerprint

`schema_fingerprint` 覆盖 `0001_current.sql` 创建的所有非 SQLite 内部对象：

1. 从 `sqlite_schema` 读取 `sql IS NOT NULL` 且名称不以 `sqlite_` 开头的行。
2. 每行规范化为
   `type + NUL + name + NUL + tbl_name + NUL + normalized_sql + NUL`；
   末尾 NUL 是行边界，防止相邻 SQL 文本产生拼接歧义。
3. 按 `type`、`name` 的 UTF-8 字节序排序。
4. 使用 domain separator
   `freeagent.current-store.schema-fingerprint.v1\n` 拼接后计算 SHA-256。

`normalized_sql` 只统一 UTF-8、换行和首尾空白；不得做可能改变 SQL 语义的重写。
实现使用 `ComputeSchemaFingerprint` 从 `0001_current.sql` 生成预期 fingerprint，
并同时固定到：

- 二进制内的只读常量；
- `store_meta.schema_fingerprint`；
- 备份 manifest；
- 架构验收 golden file。

FAC1 历史 Store 为 43 张 ordinary table、25 个 explicit indexes 与 64 个 triggers。W6-3 在 W6-2 的 33 表上
增加 `run_observation_heads`、`run_observation_snapshots` 及六张 `overview_basis_*` /
`overview_resource_*` observation 表；W6-5 再增加 `module_artifacts` 与 append-only
`module_artifact_admissions`；W6-2 则曾在 W2-U3 的 32 表上只增加 append-only
`control_operation_receipts`。W2-U2 在 R2 的 24 表上增加 Publisher Key、Source、Snapshot、全局
ModuleRef→ArtifactDigest 与 Snapshot Entry 五张 observation fact 表，U3 再增加全局 Candidate、
Tenant-scoped Review 与 exact-Review terminal Decision 三张不可变 fact 表；没有新增第二 Store、Gateway、
Catalog pointer、handler registry 或效果账本。FAC1 历史 `0001_current.sql` 对应的 schema
fingerprint 为：

```text
47981b9bf147ec81c6e98382f281db0d5395187a1f090f7ce183c116fba82a0d
```

该值是 FAC1 历史二进制中的只读常量。当前 FAC2 的 `ExpectedSchemaFingerprint` 为
`d5d876f327dc29dc6f4a10476652641172ab8e1f0451a8714fc450f58733541e`；运行时计算值、二进制
预期值、`store_meta` 值和备份 manifest 中的值任一不一致时均拒绝打开或恢复。
`TestSchemaFingerprintIsFrozenAndDetectsDrift` 固定该 golden，并证明新增 schema
对象会改变 fingerprint；迁移内容变化时必须先更新 fingerprint，再按 Draft
重建流程重新验收。

FAC1 历史 `0001_current.sql` 文件 SHA-256 为：

```text
6def433a59fa8f4876894572f1610cae499abc5b389ab5930ea697055419ca86
```

FAC1 历史迁移构件大小为 `150301` bytes。当前 FAC2 `0001_current.sql` 为 149,239 bytes /
`dbc3e724a1f7c030677c84a77a317f69ef2fe246985cc749559a9f3dd5a6dc5a`，`0002_server_owned_review.sql`
为 7,173 bytes / `3091a49ebcf724f573f91cc0fd22a7c58ebb52fa9d7ed552e32b6526ebeca3cb`。每个文件摘要记录
迁移构件本身；它不能替代从实际 SQLite schema 重算的 `schema_fingerprint`，二者均须独立匹配。

上述第一组 fingerprint、migration SHA-256 与 size 对应 **43-table / 25-explicit-index / 64-trigger
FAC1 W6-5 server-owned module artifact ingress historical schema**。当前 FAC2 W6.6 schema 为
**42-table / 26-explicit-index / 64-trigger / UserVersion 2**，并通过 `0002_server_owned_review.sql`
加入 Review 的 Admission/operator/request-digest 关系投影。W6-3/W6-4 的历史 41 表 identity
`87d63a2e468ee27ee2c0e1918a92e580532825bde85dbb872b9c795bf577f2c1`、migration `143588` bytes /
SHA-256 `5bd9743e988b82750f785c015f07c11fbcf6025da70148405aec11106b4efb22` 只表示对应历史切片。
W6-2 durable receipt Schema 的历史 33 表 identity
`51be9081a815e6379380b93e745db03d33ca2f358962f85ace63bce2534dcc10`、migration `67998` bytes /
SHA-256 `8feea38beb505ab4f371afe32eecd877744062bd149d0d24982839c5450fc952` 只表示 W6-2 历史切片。
W2-U3 至 W6-2 confirmation 的历史 32 表 identity
`37258c1939308be83f21a109e59a19e32946734d1c53b1b262f876dc36e47dbd`、migration `57652` bytes /
SHA-256 `e4f1047eb527b65ab050d444062f3d286cc2ad87d56e60b1cd7f4e96df87652d` 继续只表示对应历史切片。
W2-U2 的历史 29 表 identity
`6d2bded477e7b1d2c755bf47f5b41f63f1b2f7b568fd72496fc43fbceac3a5f1`、migration `49972` bytes /
SHA-256 `d2bcc27bff2e17a058165f7c544b0c97cd1c99efca3264ab401631477611263e` 继续只表示 U2 历史切片。
W2-R2 的旧 24 表 identity
`7d2e0850a0253a630d5e2264c720b10fbac3ad0fdb6f9e53fcd722c2dd2615a8`、migration `44257` bytes /
SHA-256 `2520299390885b4c23df85d2ff217629f6a458c368735e5bdc0df2eca37b8f2f` 继续只表示 R2 历史
切片。W2-R1 的旧 24 表 identity
`dcad8f8837ecc5832f444acbc2680a2c2debff5161319710f60e7f649e4aede5`、migration `44237` bytes /
SHA-256 `fd7270130b5b13e5dfe9e3bb0a3bc934d46eb0c3b4a0647b1a3fddd81773e665` 继续只表示 R1 历史
切片。W5-F1/W5-X1 的旧 24 表 identity
`98d658e68907f77516ce0366a7b08bfa3851600326850590ed79584f581fa588`、migration `44215` bytes /
SHA-256 `0a6701405e471e1ee7f41ea89ed62b50e5a4723e71b320e50f073cdd3e6b727a` 继续只表示对应历史
切片，不得被全局替换。W5-X1 当时没有新增表，只把两个已有真实消费者的 Workspace Transfer
ContentKind 纳入同一 `content_records` kind 约束并重算 identity。W1 曾在 S3-A 20 表历史基线上加入第 21 张
`conversations` 和三个 nullable `runs` turn 投影；W4-L1A 加入第 22 张
`learning_proposals`，W4-L1B 把同一表的 kind 约束扩为 `KNOWLEDGE | SKILL`，W4-L2 再在
同一表加入唯一 Reviewer Run/Attempt 引用与状态/revision 约束；W4-L3 继续复用该表，加入
all-or-none Version canonical/ID、ArtifactDigest/size、APPROVE Verdict digest 与时间投影；
W4-L4 最后加入第 23、24 张 `learning_cycle_schedules` 与 `learning_cycle_tasks`。W4-L4 的旧
fingerprint/hash/size 只在 §23.6 作为当时的历史证据保留。
W4-L3 的 22 表 fingerprint `a744ea62d0ead80b5eb0669409f0ab4cd0d297b662e67181b0a88f760b2c5024`
和 migration `37810` bytes / SHA-256
`025acc044f53a71c674d5963c171806e87095911025a129f65b4be33a0601070` 只作已验收历史切片证据。
W4-L2 fingerprint `feed0dd35f98e4f177934092d93a1b3d7f834f0fae3b076e722aa6bcd4722484`
和 migration `35743` bytes / SHA-256
`715c2904f417ecf8732f554eda5c47af1a6c45eecbf8e21f408f51badb70519c` 只作已验收历史切片证据。
L1B 的 fingerprint
`18722b0c8a18e9f6563e65cf30d0ebc50b723aa6f6465aa4b95d83ba001bc422` 和 migration
`34592` bytes / SHA-256
`75b7fe488932e0f562c84073a659c9803ffcd4bfdb4efa500b19720c7648c47f` 只作已验收历史切片证据。
当前数值包含 §23.6 的 W4-L4 能力、§24 的 W5-X1 ContentKind 约束、§25 的 F1 稳定性合同、
§26 的 REMOTE Activation、§27 的 WASM Activation execution-class 约束、§30 的 U2 observation facts
与 §31 的 U3 review facts、W6-3 的 Run/resource/basis observation closure，以及 W6-5 的 inert Artifact/Admission；F1、R1 和 R2 均未新增表。
该数值不表示后台 Worker、自动 Materialize/Apply、主动外部检索、W2 通用装配总工作包、P2/P3/P4/P5
或生产部署已经实现；W6.6 的 Review/Decision 也不自动产生生命周期外部效果。

## 4. 数据编码约定

| 类型 | 存储约定 |
|---|---|
| ID | Core 生成的 opaque `TEXT`；不得由 Module 自选，也不得进入无关模型正文 |
| Revision/Epoch/Sequence | 非负 `INTEGER`，单调递增 |
| 时间 | UTC Unix microseconds `INTEGER`；未知为 `NULL` |
| Canonical JSON | UTF-8 `BLOB`，写入前完成规范化与摘要校验 |
| Digest | 64 字符小写十六进制 `TEXT` |
| Token | 非负 `INTEGER`；未知为 `NULL` |
| Secret | 只保存 `SecretRef` 身份，禁止保存明文 Secret |

所有连接必须启用并验证：

```text
PRAGMA foreign_keys = ON
PRAGMA trusted_schema = OFF
```

正式路径上的唯一写 owner 还必须启用并验证：

```text
PRAGMA journal_mode = WAL
PRAGMA synchronous = FULL
```

只读 verifier 不负责切换 journal mode。初始化临时库使用
`journal_mode=DELETE`；正式发布后才由写 owner 将最终路径切换为 WAL。服务使用
受控 `busy_timeout`，但锁等待超时不能被解释为业务重试许可。

### 4.1 确定性数据与运行元数据

下列字段是运行元数据，不参与 Control、Catalog、Snapshot、Manifest 或 seed
语义摘要：

```text
store_instance_id
created_at / published_at / installed_at
本地物理行 ID
backup created_at / tool version
```

业务引用使用 seed 中显式保存的稳定 Tenant、Workspace、Agent、Profile、Module
Instance ID 和 revision。Run deadline、模型 deadline 等由业务契约明确要求的时间
仍属于对应 canonical 对象。

备份恢复必须原样保留 `store_instance_id` 和运行元数据；seed 重建必须生成新的
`store_instance_id` 和初始化时间。两种流程不得使用同一套“字节一致”比较标准。

## 5. FAC2 当前最小表集与迁移后投影

FAC1 的 `0001_current.sql` 43 表谱系只作为历史记录。当前 FAC2 固定为 **42 张 ordinary table**；
`0001_current.sql` 提供 UserVersion 1 的 42 表 bootstrap，`0002_server_owned_review.sql` 将 UserVersion
推进到 2，并只为 `module_upgrade_reviews` 增加 server-owned Admission/operator/request-digest 投影列和
查询索引，不增加 ordinary table。前 20 张构成已验收的 S1–S3 历史基线：
Memory、Action/Gateway、Channel 和公平 Scheduler 各只在有真实消费者时加入一张必要表。
W1 第一开发切片加入 `conversations`；W4-L1A/L1B 当前开发切片只再加入并复用
`learning_proposals`，W4-L2 Review 与 W4-L3 Version 继续复用该表；W4-L4 只新增 Schedule 与
Task 两张窄表。W5-D1/X1 继续保持当时的 24 表，只复用 family Run/Attempt/Usage 与
`content_records`；W2-U2 再增加五张 Source/Snapshot observation fact 表；W2-U3 增加三张不可变
Candidate/Review/Decision fact 表；W6-2 durable receipt Schema 只增加第 33 张 append-only
`control_operation_receipts`；W6-3 再增加第 34–41 张 Run/resource/basis observation closure 表；W6-5 增加
当前第 41 张 immutable `module_artifacts` 与第 42 张 append-only `module_artifact_admissions`。没有 Session、
conversation history/summary/usage 第二表族、
ActionProposal 专表、Scheduler job/queue 表、Channel Outbox 专表、Learning Review/
Version/report/queue/worker 专表、Upgrade queue/reservation/stage/downloader/Apply 专表、兼容表、shadow 表
或未接线证明表。

Parent/Child + Composite 第一切片严格保持 **19 张 ordinary table**：不新增 family、
child、queue、scheduler、reviewer 或 cancellation 表，只在既有 `runs` 增加
`parent_run_id`、`parent_manifest_digest`、`parent_slot_id`、`cancel_request_ref` 四列。
这是 S2 历史事实；S3-A 随后只新增当时的第 20 张 Scheduler 当前计数表，
S3-B 保持 20 表。W1 当时新增第 21 张 `conversations`，W4-L1A 新增第 22 张
`learning_proposals`，W4-L1B 复用该表接纳静态 Skill Draft，W4-L2 扩展审核投影，W4-L3
扩展一对一 Version 投影；W4-L4 新增第 23、24 张 `learning_cycle_schedules` 与
`learning_cycle_tasks`；W2-U2 再新增第 25–29 张 Publisher Key、Source、Snapshot、全局
ModuleRef 与 ordered entry observation fact 表；W2-U3 新增第 30–32 张 Candidate、Review 与 Decision 表。
W6-2 receipt Schema 新增第 33 张 `control_operation_receipts`；W6-3 新增第 34–41 张 observation 表；W6-5
新增第 42、43 张 Artifact/Admission 表。
上述 19→43 的表数谱系属于金额退场前的 FAC1 历史，只证明 FAC1 各切片当时的事实。
FAC2 基线在 `P0_MONEY_BUDGET_REMOVED_FAC2_BASELINE` 中删除 `model_price_snapshots`，
当前 ordinary table 总数为 **42**；FAC1 的 43 表数值不再是当前门禁值。
当前 fingerprint、migration digest 与大小已经按 §3.3 重算并冻结；
S2/S3 的 19/20 表及 W1 的 21 表数值只作对应历史验收证据保留。
下表编号是当前 FAC2 42 表集合的呈现序号，大体沿用能力首次引入顺序，
既不是 SQL `CREATE TABLE` 的物理排列顺序，也不再等同于 FAC1 的历史引入编号；
表名集合必须与 migration 的 42 张 ordinary table 完全一致；当前 identity 以 §3.3 为准。

| # | 表 | 权威事实 | 最小键与重要字段 |
|---:|---|---|---|
| 1 | `store_meta` | 当前 Store 身份与创建信息 | singleton PK、store instance ID、schema identity/version/fingerprint、generator ID、created at |
| 2 | `control_snapshots` | Tenant 的不可变已发布期望配置 | snapshot ID、tenant ID、revision、canonical JSON、digest、published at |
| 3 | `content_records` | Core 管理的不可变内容寻址对象 | content digest PK、kind、media type、canonical bytes、size、created at |
| 4 | `module_installations` | 已验证模块包与未受信 Manifest | installation ID、module ID、exact version、manifest ref、唯一 artifact digest |
| 5 | `module_activations` | Core 分配的不可变执行实例 | activation ID、tenant ID、instance ID、installation ID、activation revision、execution class、adapter identity |
| 6 | `runtime_catalog_generations` | Tenant 的不可变可用模块目录 | generation ID、tenant ID、generation、control snapshot ref、canonical JSON、digest |
| 7 | `control_current` | 每个 Tenant 唯一的 Control/Catalog 发布指针 | tenant ID PK、snapshot ID、catalog generation ID、pointer revision |
| 8 | `member_execution_snapshots` | Run 成员的冻结 PortPlan 与授予结果 | `(run ID, member ID)` 主键、Agent/Workspace/Profile refs、control/catalog refs、canonical JSON、digest |
| 9 | `runs` | Admission 幂等映射、nullable Conversation/family 投影与 Run 生命周期当前状态 | run ID、tenant/workspace IDs、admission key/intent digest、state/disposition/revision；含 Conversation ID/turn/predecessor 三列与 parent run/manifest/slot、cancel request ref |
| 10 | `run_manifests` | Run 的不可变恢复根 | run ID PK、canonical JSON、digest、published at |
| 11 | `loop_frames` | Run 当前续跑位置与 owner fencing | run ID PK、frame revision、step、usage ledger ref、continuation、model/action 两个互斥 pending refs、waiting reason、last authoritative event、lease owner/epoch/expiry |
| 12 | `run_events` | 仅追加的权威状态变更审计序列 | run ID + event sequence、event kind、from/to revision、payload ref/digest、created at |
| 13 | `model_dispatch_attempts` | 一次冻结模型调用及其对账状态 | attempt ID、logical operation key、run/member/frame refs、binding/request refs、state、provider request/result/error/evidence refs、revision |
| 14 | `model_usage` | Attempt 的规范化 token Usage | attempt ID PK、run ID、ledger sequence、revision、token 字段、usage 状态、raw receipt ref |
| 15 | `history_entries` | 已提交给 Run 的规范消息历史 | run ID + history sequence、member/role、content ref/digest、source attempt ref、created at |
| 16 | `agent_memory_revisions` | Agent 轻量记忆的不可变 revision 链 | tenant ID + stable agent ID + revision、snapshot ref、source attempt ref、created at |
| 17 | `dispatch_attempts` | Action 与 Channel 外部效果执行及对账的唯一账本 | dispatch kind、attempt/logical operation、run/member/frame、冻结 binding、按 kind 互斥的 action/channel proposal 字段、effect、state/result/receipt/evidence、revision |
| 18 | `channel_ingress_receipts` | Workspace-scoped Cursor revision、入站 Event 去重及 Channel Admission 映射 | tenant/workspace/endpoint/scope/revision、before/after Cursor refs、binding digest、disposition、Ingress/Event/Envelope identity、Principal/ACL/Admission/Run refs |
| 19 | `workspace_scheduler_state` | 每个 Tenant/Workspace 的重启安全公平服务计数 | `(tenant_id, workspace_id)` 主键、served units、revision、updated at；不保存 runnable、permit 或结果 |
| 20 | `conversations` | Conversation 固定五元组、当前 head 与 CAS revision | conversation ID PK、tenant/principal/workspace/agent/profile IDs、nullable unique head run ID、revision、created/updated at |
| 21 | `learning_proposals` | W4-L1A/L1B 的不可变 Knowledge/静态 Skill 候选、去重投影与精确 proposer lineage；W4-L2 审核状态；W4-L3 无权限不可变 Version | Proposal ID PK、tenant/kind、source/content/draft fingerprints、target ref、Proposal/Draft canonical bytes、proposer Run/Manifest/Member/Attempt/Result refs、nullable unique `review_run_id`/`reviewer_attempt_id`、state/revision；all-or-none Version canonical/size/ID、ArtifactDigest/size、APPROVE Verdict digest、materialized at；created/updated at |
| 22 | `learning_cycle_schedules` | 默认关闭的不可变周期策略与 Store-owned enablement/watermark | `(tenant_id, schedule_id)` PK、Schedule canonical/digest、enabled、revision、nullable last scheduled、next due、created/updated at |
| 23 | `learning_cycle_tasks` | 每个逻辑到期窗口的不可变 request、普通 Run/Attempt/Result/Proposal 投影与终态 | TaskID PK、tenant/schedule/digest、scheduled for、request canonical/digest、state/revision、nullable unique Run/Attempt、Result/Proposal refs、created/updated at；每 Schedule 同时最多一个 open Task |
| 24 | `module_publisher_keys` | Operator 导入的 exact Publisher Key 与全局不可逆 current revocation | PublisherKeyID PK、canonical bytes、revision、imported/revoked at；revocation 不改写历史 Snapshot |
| 25 | `module_sources` | SourceID 的 current Source Policy 与 observation head | SourceID PK、Policy ID/canonical、Kind、OriginDigest、nullable PublisherKeyID、policy/observation revision、nullable current Snapshot、registered/updated at；Kind/OriginDigest 不可改绑 |
| 26 | `module_discovery_snapshots` | 一次不可变 Source observation 的完整 parent closure | SnapshotID PK、Source/Policy/Index IDs 与 canonical bytes、Policy/Key/observation revisions、observed at；不保存路径或 URL 明文 |
| 27 | `module_discovery_module_refs` | 全局 `ModuleID + opaque exact Version → ArtifactDigest` 冲突门禁 | ModuleRef PK、ArtifactDigest、首次 Source/Snapshot；与 Installation/materialized Learning Version 双向闭合 |
| 28 | `module_discovery_entries` | Snapshot 的有序 authority-free Index entries | SnapshotID+ordinal PK、ModuleRef、ArtifactDigest/size、package path、nullable SignatureID；只作 observation，不是 Candidate 或安装授权 |
| 29 | `module_upgrade_candidates` | 全局不可变 exact-version supply Candidate | CandidateID PK、canonical bytes、stable ReviewKey、Source/Snapshot、current/target ModuleRef 与 ArtifactDigest、admitted at；不含 Tenant、scope、路径或 authority |
| 30 | `module_upgrade_reviews` | Tenant/scope 的不可变 Upgrade Review | ReviewID PK、canonical bytes、Candidate/ReviewKey、PROFILE 或 WORKSPACE_CHANNEL_ENDPOINT、current/target Instance、target Manifest content ref、exact Installation/Activation revision、PublishedBasis、结论、created at |
| 31 | `module_candidate_decisions` | 绑定 exact Review 的终态 Operator Decision | ReviewID PK、DecisionID/canonical、Tenant、Candidate/ReviewKey、APPROVE/REJECT、decided at；partial unique Tenant-wide REJECT `(tenant_id, review_key)` |
| 32 | `control_operation_receipts` | `MODULE_DISABLE` 的 append-only durable Control receipt | receipt digest PK；Tenant、唯一 principal/scope/operation/idempotency identity；auth revision/scope-set audit；Request/input/evaluation/Control receipt/pre-post basis canonical+digest；nullable APPLIED domain receipt；Control/Catalog parent IDs与 canonical size caps |
| 33 | `run_observation_snapshots` | 每次 Run 生命周期变化的不可变 Overview 投影 | Run ID + observation sequence；state/disposition/revision、scope、更新时间、前序与 snapshot digests |
| 34 | `run_observation_heads` | 每个 Run 的当前 observation head | Run ID PK；最新 sequence/digest 与精确 Run 当前投影；只允许相邻 CAS 推进 |
| 35 | `overview_resource_snapshots` | Model/Action/Channel/Learning/Module Review 的不可变有界投影 | resource kind + ID + sequence；scope/state/revision/time、causal Run observation 与 previous digest |
| 36 | `overview_resource_heads` | 每个 Overview resource 的当前 head | resource kind + ID；最新 sequence/digest、scope/state/revision/time 与 source identity |
| 37 | `overview_resource_transition_carriers` | mutation-time exact resource snapshot carrier | 与 resource snapshot 同一四元组；绑定当次 head/source，append-only 且不可更新/替换/删除 |
| 38 | `overview_basis_snapshots` | Control/Catalog PublishedBasis 的不可变 Overview 投影 | projection digest；Tenant、Control/Catalog exact refs、pointer revision、source time 与 workspace count |
| 39 | `overview_basis_workspaces` | basis snapshot 的有序 Workspace refs | projection digest + ordinal；Workspace exact ID/version/digest，连续且有界 |
| 40 | `overview_basis_heads` | 每个 Tenant 当前 basis observation head | Tenant PK；snapshot/catalog/pointer exact tuple、projection digest、source time 与 workspace count |
| 41 | `module_artifacts` | server-owned、content-addressed、inert module artifact 事实 | ArtifactDigest PK；exact ModuleRef、Manifest content ref、artifact size、covered file count、ingressed at；不可替换或删除 |
| 42 | `module_artifact_admissions` | exact supply observation 到 Artifact 的 append-only Admission | AdmissionID PK；canonical record、Source/Policy revision、Snapshot/observation/entry ordinal、ArtifactDigest、admitted at；不授予 install/activate/bind/grant/review/execute |

#### W1 Conversation 已实现 Schema 锁

W1 当时的 migration 已在**同一个** Current Store 中增加且只增加第 21 张
`conversations`，并向既有 `runs` 增加三个 nullable turn 投影；不得
建立 Session 表、conversation history 表、summary head、第二 Store 或第二 Runtime。

`conversations` 的最小权威字段冻结为：

```text
conversation_id PRIMARY KEY
tenant_id
principal_id
workspace_id
agent_id
profile_id
head_run_id NULLABLE UNIQUE → runs.run_id
revision INTEGER >= 0
created_at
updated_at
```

`revision=0` 时 `head_run_id` 必须为空；`revision>=1` 时 head 必须非空，并指向同一
Conversation 中 `conversation_turn_index=revision` 的 Run。五个 scope/identity ID 与
`conversation_id` 创建后不可更新。它们是稳定 ID；每个 Run 的 exact Workspace/Agent/Profile
revision/digest 仍只由该 Run 的 MemberSnapshot/Manifest 冻结，因此配置更新只影响新 Run。

`runs` 只增加：

```text
conversation_id NULLABLE → conversations.conversation_id
conversation_turn_index NULLABLE INTEGER >= 1
conversation_predecessor_run_id NULLABLE → runs.run_id
```

非 Conversation Run 三列必须全空。Conversation Run 的 ID/index 必须同时非空；turn 1 的
predecessor 必须为空，turn `n>1` 的 predecessor 必须是同一 Conversation 的 turn `n-1`。
`(conversation_id, conversation_turn_index)` 唯一；同一非空 predecessor 不得形成两个后继。
`RunManifest.ConversationTurn` 必须与这三个投影逐字节闭合，并额外冻结
`principal_id`；后者必须与 `conversations.principal_id` 相同且只用于恢复/审计，不能进入
prompt。投影不能覆盖 Manifest。这样 backup/restore 的语义校验无需依赖未持久化的
AdmissionIntent canonical bytes，也能发现 Conversation owner 被篡改。

显式创建 Conversation 只写 revision 0/head NULL。每次 turn Admission 在写 Run、Snapshot、
Manifest、Frame、Event 的**同一事务**中，以
`conversation_id + expected revision + expected head` CAS head 到新 Run；后续 turn 的旧 head
必须先通过完整 terminal-result closure 证明成功。CAS 冲突、head PENDING、任一 UNKNOWN、
FAILED、CANCELLED 或关系断裂都整笔零写入失败。失败 head 不回退、不跳过、不分叉；用户
只能显式新建 Conversation。

由于 FreeAgent 从未正式部署且 Schema 仍是首次发布前 Draft，该变更已按既有
Scheme A 替换唯一 `0001_current.sql`，重算 21 表 fingerprint/migration hash，并同步
init/open/backup/restore/fixture 门禁。该 21 表数值只属于 W1 历史切片；当前 43 表数值
以 §3.3 为准。本段状态为
`W1_CONVERSATION_ACCEPTED_DEVELOPMENT_SLICE`；该状态不等于 W1 完成。

历史 S2.1 Context Compiler 切片没有增加专用表；当时表数仍为 16。它只扩展
`model_dispatch_attempts.context_compilation_ref`（nullable FK → `content_records`），
并增加一个有真实消费者的 `CONTEXT_COMPILATION` Content kind。低于 85% 时该 ref
必须为空；达到 85% 后（包括无合格旧 History 的停止路径）它必须指向与该
Attempt/request 精确匹配的单一编译记录。

历史 S2 ModelProfile 切片同样没有增加表、列或 ContentKind；当时 schema fingerprint
保持不变。
画像与模型 Binding config 都复用 `CONFIG/application-json`，其唯一恢复边由
`MemberExecutionSnapshot.ModelProfile` 的可选内容摘要给出；不保存“当前画像”指针、
选择回执或 Provider 元数据副本。
模型 Binding config schema 同时在本次发布前基线中新增必填 `model_build_id`，因此 seed
和相关 CONFIG digest 会改变；方案 A 下通过显式重建开发库收口，不增加兼容 decoder，
也不改变当时的 16 表 schema/fingerprint；Memory 切片因真实可变状态消费者把 Draft
改为 17 表，Action 工作包再因真实 Gateway 消费者改为 18 表并重算 fingerprint。

首个 MCP `LOCAL_PROCESS + stdio + Tool-only` 切片当时继续保持 **18 表**，不新增表、列、
ContentKind、MCP ledger、session、job、tool cache 或 Usage 表。Scheme A 已把
`module_activations.execution_class` 的 `CHECK` 从原两个值扩为：

```sql
CHECK (execution_class IN (
    'DECLARATIVE', 'TRUSTED_IN_PROCESS', 'LOCAL_PROCESS'
))
```

该变化已按 §14 的 Scheme A（方案 A）写入 `0001_current.sql`、重算并冻结 fingerprint，
并通过显式开发 Store 重建接入；没有新增 `0002`、兼容 decoder、运行时 migration 或
双写。`LOCAL_PROCESS` 仍不构成自授权，必须同时通过 Core allowlist、精确 artifact grant
和 Action Binding 校验。其后的 S2 Channel 切片只新增 `channel_ingress_receipts`，并把
既有 `dispatch_attempts` 泛化为 `ACTION|CHANNEL_SEND`，从而形成当时的 S2 19 表基线；它没有
建立 `channel_outbox` 或第二外部效果账本。

W2-R1 当前只把同一约束增量扩为：

```sql
CHECK (execution_class IN (
    'DECLARATIVE', 'TRUSTED_IN_PROCESS', 'LOCAL_PROCESS', 'REMOTE', 'WASM'
))
```

`REMOTE` 与 `WASM` 均不构成自授权。只有 exact `action.provider/v1 +
REMOTE/freeagent-action-http/v1` handler、精确 artifact/HTTPS endpoint/SecretRef Operator grant、
Binding/Authority、current Catalog 与运行期开关全部闭合时，才可发布并使用 REMOTE Activation；
只有 exact `action.provider/v1 + WASM/freeagent-action-wasm/v1` handler、精确 artifact Operator grant、
纯计算 Binding/Authority、current Catalog 与运行期开关全部闭合时，才可发布并使用 WASM Activation。
其他 REMOTE/WASM 与 Core 控制类 Port 继续拒绝。R1 与 R2 的窄 migration 增量只重算 §3.3 identity，
不新增表、列、第二 Store 或第二执行账本。

### 5.1 表级约束

#### `store_meta`

- 必须恰有一行，singleton key 固定。
- `schema_identity`、`schema_version` 和 `schema_fingerprint` 写入后不可修改。
- `store_instance_id` 标识本次初始化的物理 Store，不作为 Schema identity。

#### Control 与 Catalog

- `control_snapshots` 对 `(tenant_id, revision)` 和 `(tenant_id, digest)` 唯一。
- `runtime_catalog_generations` 对 `(tenant_id, generation)` 和
  `(tenant_id, digest)` 唯一。
- `control_current` 是每个 Tenant 唯一允许更新的发布指针；它同时冻结精确
  Control Snapshot 与 RuntimeCatalog generation，并必须 CAS `pointer_revision`。
- Control 与 Catalog canonical wire 分别使用 `control-snapshot/v1` 和
  `runtime-catalog/v1`；其 digest 依次使用
  `freeagent.control-snapshot/v1`、`freeagent.runtime-catalog/v1` 域并排除自身
  digest 字段。
- 相同 revision/generation 的字节一致重放可幂等成功；摘要或内容不同则
  fail-closed。
- Control Snapshot 可包含 Agent、Workspace、Profile 和期望模块配置，但不能成为
  已发布 Run 的恢复输入；Run 恢复只读其冻结快照。
- Control Snapshot 不得包含生成后的 Catalog ID 或 CatalogDigest。摘要依赖只能
  单向流动：

```text
ControlDigest
  → CatalogDigest
  → MemberSnapshotDigest
  → ManifestDigest
```

任何向前一层摘要的反向引用都必须拒绝。
Catalog 中每个 Entry 必须绑定一个完整 `ActivatedModuleRef` 和其提供的 exact
PortRef；Control Profile 的 BindingSpec 只点名 InstanceID 和消费方
Config/Ceiling/有序 StaticContextRefs/FailurePolicy，不能提供或覆盖 Provider
identity。StaticContextRefs 仅允许用于 `context.provide/v1`，必须是无重复的
`STATIC_CONTEXT` ContentDigest 数组。发布和 Admission
都必须把 Catalog Entry 逐字段核对到 `module_activations` 及其
`module_installations`，不能只检查 InstanceID 存在。

Control Profile 可以显式携带一个可选 `ModelProfileRef`。发布时必须严格恢复该
`CONFIG` 为 `model-profile/v1`，再与唯一 `model.generate/v2` Binding 的 CONFIG、
provider/model/model build、ArtifactDigest、AdapterIdentity 和 ContextPolicy 逐项
核对；缺失、类型错误或收紧后不能容纳 reserved output 时整次发布回滚。

已验收的 Composite 第一切片允许 Control Snapshot 携带 optional `CompositeAgents`。Store
只保存并 strict restore `CORE_RUNTIME_V1` 冻结的 canonical config：每个 coordinator
唯一、2..8 个唯一 slot、整数 `weight_basis_points` 总和精确 10000，且 Agent/Profile ref
均在同一 snapshot 内可达。它只是 Admission 输入，不能成为 Provider 路由、RAG rank、
Skill/Reviewer 顺序或权限；未选择 Composite 时 Store 不得为它做额外查询或投影。

#### `runs`

- `(tenant_id, admission_key)` 必须唯一，且 `admission_key`、`admission_intent_digest`
  写入后不可修改。
- 相同 key + 相同 intent digest 的 Admission 重试幂等返回已有 Run；相同 key +
  不同 digest 必须 fail-closed。
- client retry、提交响应丢失、crash recovery、Scheduler retry、continuation 和
  S2 child retry 必须解析并恢复原 RunID，不能自动插入替代 Run。
- 只有新的显式 User/Operator Admission 可以创建新的 admission key 和 RunID。
- Parent/Child + Composite 第一切片只增加下列四个 nullable 列，不增加其他 family 列或表：

  ```text
  parent_run_id
  parent_manifest_digest
  parent_slot_id
  cancel_request_ref
  ```

- `parent_run_id + parent_manifest_digest + parent_slot_id` 必须全部为 NULL，或全部非 NULL；
  前者表示普通 Run/Parent，后者只表示 Child。`parent_run_id` 不可自指；即时复合 FK
  `(parent_run_id,parent_manifest_digest)` 必须匹配已经在同一 Admission 事务写入的
  `run_manifests(run_id,digest)` 唯一键；`(parent_run_id,parent_slot_id)` 必须唯一。
  当前 parent-first 写入顺序不需要 deferred constraint，事务失败仍整体回滚且不得有孤儿。
- `cancel_request_ref` 是 nullable FK → `content_records(content_digest)`，且引用 kind 必须为
  `RUN_CANCELLATION`。它只能由 NULL 单调变为一个 ref；first-write-wins，任何覆盖、清空或
  第二 ref 都必须 CAS 失败。普通 Run 只设置自身；family cancel 在一个事务设置 Parent 与
  全部 frozen Child 为同一 ref。Child 的 inherited scope 不能接受独立外部取消。
- 非 Composite Pure Chat 的三个 parent 列始终为 NULL；`cancel_request_ref` 在初始且未取消
  的路径为 NULL，显式 run-scope 取消后可以按统一合同引用 `RUN_CANCELLATION`。其 snapshot/
  manifest/content canonical bytes 和零可选读取路径不得因这些查询投影改变。

#### `content_records`

当前已实现基线允许下列有生产消费者的 kind；`CONTEXT_COMPILATION` 由 S2.1 启用，
`RUN_CANCELLATION` 由统一取消合同启用，最后两个 Workspace Transfer kind 由 W5-X1 启用：

```text
MODULE_MANIFEST
CONFIG
AUTHORITY_CEILING
TASK_INPUT
POLICY
MODEL_REQUEST
MODEL_RESULT
ACTION_PROPOSAL
ACTION_RESULT
CONTEXT_COMPILATION
MEMORY_SNAPSHOT
PROVIDER_RECEIPT
STATIC_CONTEXT
RECONCILIATION_EVIDENCE
RUN_EVENT_PAYLOAD
CHANNEL_CURSOR
CHANNEL_INGRESS_ENVELOPE
CHANNEL_SEND_PROPOSAL
CHANNEL_SEND_RESULT
RUN_CANCELLATION
WORKSPACE_TRANSFER_PAYLOAD
WORKSPACE_TRANSFER_ENVELOPE
```

- `content_digest` 按
  `SHA256("freeagent.content-record/v1\0" + kind + NUL + media_type + NUL
  + canonical_bytes)` 计算。
- 相同 digest 的重复写入必须逐字节验证；内容不同立即报完整性错误。
- 本表不能演变成任意 KV Store；新增 kind 必须先有权威消费者与恢复测试。
- `RUN_CANCELLATION` 只能保存 canonical
  `cancel-request/v1 {schema_version,root_run_id,root_manifest_digest,scope,reason_code}`；
  `scope` 只允许 `run|family`，reason code 必须来自有界 allowlist。请求时间只写 RunEvent/
  行运行元数据，不进入 canonical 内容或摘要；Child 的 `inherited` 是 manifest scope，
  不伪造第二份 cancellation content。
- `WORKSPACE_TRANSFER_PAYLOAD` 只保存 trusted compiler 生成的 canonical
  `workspace-task-summary/v1`；`SPECIALIST_RESULT` 不复制模型正文，而由 envelope 精确引用
  已存在的 terminal `MODEL_RESULT` 并验证其 `specialist-contribution/v1` 投影。
- `WORKSPACE_TRANSFER_ENVELOPE` 只保存 canonical `workspace-transfer-envelope/v1`，冻结
  Tenant、方向、payload kind/schema/ref/size、source/target Workspace、双边 grant ID/digest、
  Root/Child/slot 与 TaskInputRef。它是不可变审计边，不是第二消息队列或 Transfer 账本。
- `TASK_INPUT` 是 RunManifest 的任务输入恢复根；Manifest 的
  `TaskInputRef/TaskInputDigest` 都必须等于该 ContentDigest。
- `POLICY` 的 canonical bytes 必须是 `CORE_RUNTIME_V1` 定义的
  `PolicyDocument`。`PolicyRef.id/version` 与文档字段匹配，`PolicyRef.digest`
  等于该 ContentDigest；alias 不能作为恢复引用。
- `CONFIG` 仍是通用内容 kind，但使用方必须按冻结引用要求的精确 schema 严格恢复。
  `ModelProfileRef` 只能引用 canonical `model-profile/v1`；模型 Binding 的 `ConfigRef`
  只能引用 canonical `model-binding-config/v2`。两者互换、未知字段或仅摘要形状相似
  均必须失败关闭。
  ModelProfile CONFIG 上限为 64 KiB；开放的 Provider generation parameters 仍必须
  通过 Activated Provider 的精确 schema allowlist，已知凭据/endpoint 键族由通用
  合同先拒绝，任意自造别名不被当成内容级秘密扫描能力。当前 composition 只激活
  参数为空对象的 exact Echo；第三方模型 Provider 在其 schema 校验接入真实 Host 前
  不得 Activate/Publish。
- `ACTION_PROPOSAL` 只能保存 canonical `action-proposal/v1`：冻结
  MemberSnapshotDigest、PublicActionID、ProviderActionID、DefinitionDigest、原
  CanonicalInput 与有界 PreparedPayload。ContentDigest 是 Proposal 唯一身份，不再
  保存可漂移的子摘要或第二 ProposalDigest。
- `ACTION_RESULT` 只能保存 canonical `action-result/v1`：冻结 PublicActionID、
  DefinitionDigest 与 `AVAILABLE|RESULT_REJECTED`。AVAILABLE 必须携带不超过快照/
  Attempt MaxResultBytes（Core 硬上限 16 KiB）的 canonical result；executor 已可靠
  确认 SUCCEEDED 但结果非 canonical/超限时，只能写 Core 有界 RESULT_REJECTED +
  ErrorClassification，并明确终止 Run，不把已确认 Effect 错记 UNKNOWN。FAILED/UNKNOWN
  不得伪造 ACTION_RESULT。
  Store 仅为 AVAILABLE 使用 Core 唯一 builder 生成固定
  `UNTRUSTED_ACTION_RESULT_JSON:` envelope，
  不接受调用方传入自组装模型二正文。

#### Install 与 Activate

- `module_installations` 对 `(module_id, exact_version)` 唯一。
- 相同 ID+Version 对应不同 `artifact_digest` 时必须拒绝，不允许覆盖。
- Install/Activate 只有全部 immutable 字段（包括 Core 生成的
  InstallationID/ActivationID）一致时才是幂等重放；不得把不同 ID 静默别名为旧
  记录。
- Module Manifest 只声明运行模式、能力和权限请求；Installation 不保存最终
  `ExecutionClass`、Core 控制权或 Effect 上限。
- Core 在 Activation 时根据本地 allowlist、隔离条件和 Adapter 注册表分配
  `ExecutionClass` 与 `AdapterIdentity`。Manifest 中的请求不能成为最终值。
- 首个 `LOCAL_PROCESS` 只允许精确 ArtifactDigest allowlist 中、Operator 显式批准且
  **完全信任**的
  MCP stdio Tool Adapter。Activation 必须同时闭合精确 artifact-relative entrypoint、
  无 shell argv、working directory、固定 MCP `2025-11-25`、版本化 AdapterIdentity
  和无 Secret grant；本地进程与官方 Go SDK 不提供 OS sandbox，不能以
  `LOCAL_PROCESS` 名义授予 REMOTE、Core 控制类 Port 或通用进程执行权。空环境、精确
  摘要和受管进程树不等于文件系统/网络 sandbox；因此不可信第三方和生产级隔离尚未
  验收。Module 不得直写 Core DB 或绕过 Authority 的不变量继续保留，但本切片以
  Operator 完全信任为前提，不能用它证明对恶意本地进程的 OS 强制隔离。
- 模块包身份只有一个 `ArtifactDigest`。`manifest_ref` 是内容记录引用，不得再定义
  第二个 `ManifestDigest` 作为模块包身份。
- `module_activations` 不保存 `REQUIRED/OPTIONAL`、fallback 或 Binding 顺序；这些
  属于具体 `MemberExecutionSnapshot.PortPlan`。
- `module_activations` 也不保存具体 Agent/Workspace 的 `ConfigRef`、
  `AuthorityCeilingRef` 或 `StaticContextRefs`；Assembly Compiler 在 Bind 时由
  消费方策略计算，并只冻结到对应 PortBinding。
- Activation 记录不可变。撤销或收紧权限通过发布新 Catalog 完成，不改写旧
  Run 使用的记录。
- Module Host 可以读取当前 `control_current` 指向 Catalog 的 deny-only 撤权结果；
  该检查只能拒绝旧 Activation，不能替换 Binding、扩大权限或提供 fallback。
- 模块包字节由 content-addressed artifact store 保存；数据库只持有精确摘要与
  引用。备份必须包含所有被保留 `module_installations` 或 `module_artifacts` 行引用的 artifact，按 digest
  去重；当前 Catalog、Run 或 Installation 是否引用一个已 ingress 对象不能缩小此范围。

#### Agent Memory revisions

首片只新增以下一张表；不要拆分 facts、preferences、summaries、counters 或可变 head：

```sql
CREATE TABLE agent_memory_revisions (
    tenant_id TEXT NOT NULL,
    agent_id TEXT NOT NULL,
    revision INTEGER NOT NULL CHECK (revision >= 1),
    snapshot_ref TEXT NOT NULL REFERENCES content_records(content_digest),
    source_attempt_id TEXT REFERENCES model_dispatch_attempts(attempt_id),
    created_at INTEGER NOT NULL CHECK (created_at > 0),
    PRIMARY KEY (tenant_id, agent_id, revision),
    UNIQUE (snapshot_ref),
    UNIQUE (source_attempt_id),
    CHECK (
        (revision = 1 AND source_attempt_id IS NULL) OR
        (revision > 1 AND source_attempt_id IS NOT NULL)
    )
) STRICT;
```

revision 1 genesis 只能由显式 init/seed 或受控 Store API 创建，普通启动不得隐式生成。旧 snapshot
是已持久化 Attempt、审计和 backup/restore 的恢复闭包，本切片不做 GC。Core Store API
是唯一 writer；Memory Provider 只能经窄只读接口得到已裁剪 candidates，不能获得
SQLite handle 或写 revision。

#### Snapshot、Manifest 与 Run

- `member_execution_snapshots` 对 `(run_id, member_id)` 唯一，写入后不可更新。
- `run_manifests` 对 `run_id` 一对一，写入后不可更新。
- Manifest 含 `ConversationTurn` 时，`runs` 的 conversation ID/index/predecessor 投影必须
  精确相同，Manifest 的 PrincipalID 还必须闭合 Conversation owner，且 W1 第一切片不得
  同时含 `Composite`。无 Conversation 时该 Manifest 字段与
  三个 Run 投影全部省略，既有普通/Composite canonical bytes 不变。
- S1 Admission 必须校验恰好一个 Member、一个 Workspace，且 `ParentRunID` 为空。
  Parent/Child 第一切片仍要求**每个 Run**恰好一个 Member/Workspace；它以一个 Parent Run
  加 2..8 个 Child Run 表达，绝不开放多 Member Run。
- Manifest 内每个 Member ref 必须与同一事务写入的 snapshot ID/digest 完全匹配。
- Composite family 必须同 Tenant、深度 1；每个 Run 仍恰好一个 Workspace。Root/Reviewer
  固定在 Root Workspace；同 Workspace Child 不带 Transfer，跨 Workspace 的初始/repair Child
  必须携带同一冻结双边 grant 计划。Parent manifest 保存按 weight 降序、同权重 slot UTF-8
  稳定排序的完整初始 Child ref，并在 Decision plan 中预冻结 repair Children/Reviewer；Child
  manifest 保存 Parent RunID/ManifestDigest/slot。Parent 只引用 Child Run/MemberSnapshot，
  不引用 Child ManifestDigest；Store 必须先验证全部 snapshot，再计算 Parent digest，最后
  验证 Child manifests，拒绝 digest cycle、slot 漂移或事后增删 Child。
- family 全部 manifest 必须引用同一 `TASK_INPUT`；Child 的 focus 只来自 snapshot 中受保护的
  frozen assignment，不能存第二任务输入。Parent 初始 Frame 为 `WAITING_CHILDREN`，Child
  初始 Frame 为 `READY`；整个图与初态必须在一次事务提交。
- 静态上下文的唯一恢复边是冻结
  `MemberExecutionSnapshot.PortPlans[].Bindings[].StaticContextRefs`；不得另建
  Admission 边带列表或聚合副本。
- Action 定义的唯一恢复边是成员快照中可选的有序
  `Actions[]{PublicActionID,ProviderActionID,BindingIndex,Description,InputSchema,
  EffectClass,MaxResultBytes,DefinitionDigest}`。它必须逐项闭合唯一
  `action.provider/v1` PortPlan；无 Action
  PortPlan 时字段省略，Store 不得查询 Action artifact、Config、Authority 或 Registry。
  Admission 后恢复禁止重新 Describe、改写定义或按 ActionID 搜索当前 Provider。
- `CONTEXT_COMPILATION` 只能保存 `context-compilation/v1`。Store 严格恢复其
  canonical wire，并验证 Policy/Workspace 投影、request digest、最旧可删 History
  前缀、近期保护窗、逐项 Drop 链和用共享算法重算的 summary 正文；token 数值由
  单一 Core Compiler 计算，Store 只验证数值链闭合，不重新运行 Compiler 或声称独立
  复算估算。它是一次模型步骤的单一审计与恢复闭包，不是第二份 History、可变
  Memory、任意 checkpoint KV 或摘要对象族。
- Conversation 的 eligible History 单元必须是 predecessor Run 的完整
  USER+ASSISTANT pair。`<85%` 不摘要；`>=85% 且 <100%` 对最旧连续 eligible pairs 最多
  执行一次确定性摘要；初始 `>=100%` 不先摘要，按时间顺序每次移除一个最旧完整 pair
  并重算，达到恢复水位即停止，因此保存的是达到目标所需的最小完整旧前缀。Drop 只
  改变本 Run 请求视图，不删除 predecessor Run、TASK_INPUT 或 History。每 Run 的摘要
  只持久化在该 Run 的唯一 compilation 中，不增加 Conversation summary head。
- Composite Child/Parent 即使低于 85% 也必须有 compilation。Child evidence 必须匹配
  frozen assignment；Parent 的 `composite.child_results` 必须逐项闭合 Parent manifest
  顺序、Child manifest/member/result ref/digest/terminal revision、focus/weight、50% pool
  allocation 与 estimate。Child result 只能是完整、不截断的 canonical untrusted USER
  envelope；任一超出其分配或 attempt 前发生篡改都必须零 permit 失败。
- Action-enabled 模型一即使低于 85% 也必须在同一 compilation 中保存可选
  `action_result_reservation{max_envelope_bytes,estimated_tokens}`。Store 对每个冻结 Action
  以 PublicActionID、DefinitionDigest 和 MaxResultBytes 重建最坏完整 envelope 后取
  最大值，并验证它已进入同一 85%/100% 估算链；无 Action 时字段省略且既有 Pure Chat
  canonical wire 不变。
- S2 RAG 不增加 ContentKind、表或列。动态检索证据直接位于同一
  `context-compilation/v1.knowledge_retrievals`；无 RAG 时字段省略。每项必须闭合冻结
  BindingIndex、ConfigRef、AuthorityCeilingRef、exact Tenant/Workspace/Agent/Task
  scope、Knowledge Source、连续 Rank hits、chunk/output/request digest。Store 必须从
  同一 MemberSnapshot 和已加载 ContentRecord 逐项验证，不能相信 Provider 自报权限。
- 动态 `context.provide/v1` Binding 必须是 `TRUSTED_IN_PROCESS`、REQUIRED、无
  StaticContextRefs；Config 必须是 `UNTRUSTED_DATA` 且不可 Summary/Drop。Store 只按
  `Parameters.schema_version` 精确分派 `knowledge-context-binding/v1` 或
  `memory-context-binding/v1`，不得把任意动态 Binding 默认解释为 Knowledge。对应
  `AUTHORITY_CEILING` 必须恢复为同协议的精确 v1 ceiling 并允许当前 exact scope；
  Config 只能请求，Ceiling 才能授权。任一不匹配在 Attempt/permit 写入前 fail-closed。
- `MEMORY_SNAPSHOT` 只能保存 canonical `memory-snapshot/v1`，归属 exact Tenant 与
  stable AgentID；每个 entry 还必须闭合 Workspace visibility、来源、TTL、算法与自身
  digest。自动 TASK_SUMMARY、CATEGORY_COUNT、REPEATED_TERM_COUNT 默认仅当前
  Workspace 可见；FACT/PREFERENCE 只有显式确认来源才可为 Agent-global。Memory
  文本始终是不可信 USER 数据，计数不是知识权威，不能抑制 RAG。
- Memory exact query scope 中的 AgentID 与 WorkspaceID 不得使用 `*`；该字符只保留给
  `AllowedWorkspaceIDs`/`VisibleWorkspaceIDs` 中的单独 wildcard，不能伪装成实际对象
  身份。
- CATEGORY_COUNT 与 REPEATED_TERM_COUNT 只从 canonical TASK_INPUT 计算，不统计模型
  RESULT；算法或 Binding 配置摘要变化时，同 key 计数必须开启新序列，不能把旧语义
  累加后伪装成新配置生成。TASK_SUMMARY 的输出上限至少为 4 bytes。
- `agent_memory_revisions` 是 append-only 投影。每个 `(tenant_id, agent_id)` 必须从
  revision 1 genesis 连续递增；revision 1 的 `source_attempt_id` 为空，其余 revision
  必须唯一引用同 Agent 的 SUCCEEDED model Attempt。`snapshot_ref` 唯一引用
  `MEMORY_SNAPSHOT`；snapshot owner/revision/parent/source 必须逐项与表投影闭合。
  当前 head 由最大 revision 派生，不增加可变 head 表、facts 分表或 Memory ledger。
- `runs.state/disposition` 只表示 Run 生命周期；
  `loop_frames.step/waiting_reason` 只表示循环位置。scope 等标量是受 Store API
  维护的查询投影，不得覆盖 Manifest 或 Snapshot 的装配事实。
- `loop_frames.pending_attempt_id` 继续只外键到 ModelDispatchAttempt；
  `pending_dispatch_attempt_id` 外键到共享的 `ACTION|CHANNEL_SEND` DispatchAttempt，并用
  SQL CHECK 保证两个 pending pointer 最多一个非空。`READY` 与 `WAITING_CHILDREN` 都不得
  携带 Attempt identity；其他需要 Attempt 的 continuation 显式携带
  `attempt_kind=MODEL|ACTION|CHANNEL`，不得根据 ID 前缀猜测；`CHANNEL` continuation
  必须指向 `dispatch_kind=CHANNEL_SEND` 的原 Attempt。
- 一个事务提交后，不允许存在缺少 Manifest、成员快照或初始 LoopFrame 的 Run。
- `run_events` 是审计序列，不是第二个可独立重放的状态机。恢复读取当前
  `runs + loop_frames`，并用 event sequence 检查连续性。
- `history_entries` 只追加。已提交 sequence 不更新、不覆盖；重复提交必须按
  digest 幂等判定。
- Conversation 不复制 predecessor 的 `history_entries`。每个已成功 predecessor Run 的
  USER 只来自其 Manifest 指向的 `TASK_INPUT`，ASSISTANT 只来自其最终成功 Model Attempt
  对应的唯一 History 行；Store 按 runs predecessor 链和 turn index 恢复完整 pair。当前
  Run 仍只追加本轮最终 ASSISTANT。缺 pair、来源 Attempt 错配、链断裂或非成功 predecessor
  都在新 Model Attempt/permit 前 fail-closed。

#### Model Attempt

`model_dispatch_attempts.state` 只允许：

```text
PENDING
SUCCEEDED
FAILED
MODEL_UNKNOWN
```

状态转换只允许：

```text
PENDING       → SUCCEEDED | FAILED | MODEL_UNKNOWN
MODEL_UNKNOWN → SUCCEEDED | FAILED    # 仅原 Attempt 有可靠对账证据
SUCCEEDED     → 无
FAILED        → 无
```

- `LogicalOperationKey` 必须按下式计算并全局唯一：

```text
SHA256(
  "freeagent.model-operation/v1\0"
  + CanonicalJSON({
      run_id,
      member_id,
      logical_step_id
    })
)
```

- 精确 Binding 冻结在 Attempt 中，但不得进入 `LogicalOperationKey`；否则换
  Binding 会产生新 key 并绕过 UNKNOWN 保护。
- RequestDigest 同样不得进入 key；它作为冻结审计字段保存。同一
  `(run_id, member_id, logical_step_id)` 最多一条 Attempt，使用同 key 提交不同
  RequestDigest 必须 fail-closed。
- Attempt 冻结 `MemberSnapshotDigest`、精确 model Binding、RequestRef/Digest、
  provider、model、参数和 deadline。
- `MODEL_UNKNOWN` 不得转回 `PENDING`。
- 对账必须更新原 Attempt；不得为相同 `LogicalOperationKey` 插入新 Attempt。
- MODEL_UNKNOWN 不得通过自动创建新 Run、Child Run 或新 logical step 绕过；
  client/Scheduler/continuation 重入必须先经 `runs` 的 Admission 幂等映射恢复原
  Run。
- `SUCCEEDED` 必须有 result ref；`FAILED` 必须有明确 error classification；
  `MODEL_UNKNOWN` 必须保留已知 provider request ID、receipt、对账 evidence 或未知原因。
  未知原因可以是 ModuleHost allowlist 内的固定脱敏观察位置码；它只记录本地调用边界，
  不构成 Provider 已收到、执行或完成请求的证据。旧记录没有该码时保持原值，不得回填、
  重分类或触发语义重放。
- Provider request ID 只能作为对账线索，不能替代 Core Attempt ID。

#### 外部效果 `DispatchAttempt`（Action 与 Channel 共用）

`dispatch_attempts` 是唯一外部效果账本，`dispatch_kind` 只允许
`ACTION|CHANNEL_SEND`。表级共同字段包括 Attempt/LogicalOperation、Run/Member/Frame、
唯一 `source_model_attempt_id`、MemberSnapshotDigest、BindingIndex/Binding、Effect、
`max_result_bytes`（数据库硬上限 64 KiB）、deadline、UsageLedgerRef、四态状态、
external operation、result/receipt/reconciliation evidence、revision 与时间。具体
Provider/Binding 的更低结果上限仍由冻结快照和 Store API 校验，不能借数据库硬上限扩权。

两种 kind 的身份字段由 SQL `CHECK` 严格互斥：

```text
ACTION:
  public_action_id + provider_action_id + definition_digest + proposal_ref
  channel_endpoint_id/channel_ingress_key/channel_proposal_ref 必须为空

CHANNEL_SEND:
  channel_endpoint_id + channel_ingress_key + channel_proposal_ref
  public_action_id/provider_action_id/definition_digest/proposal_ref 必须为空
```

每个 DispatchAttempt 从 PENDING 开始，状态转换只允许：

```text
PENDING → SUCCEEDED | FAILED | UNKNOWN
UNKNOWN → SUCCEEDED | FAILED  # 仅有可靠证据更新原 Attempt
```

- ACTION LogicalOperationKey 使用
  `SHA256("freeagent.action-operation/v1\0" +
  CanonicalJSON({run_id,member_id,logical_step_id}))`；CHANNEL_SEND 使用独立
  `freeagent.channel-send-operation/v1` 域。Binding、内容和 Proposal digest 不进入 key；
  同一 `(run_id,member_id,logical_step_id)` 仍由唯一约束阻止替代 Attempt。
- `binding_json` 必须等于成员快照对应 BindingIndex 的 canonical PortBinding。
  ACTION 的 ActionID/Definition/Effect/MaxResultBytes/Proposal 与冻结 Action 映射闭合；
  CHANNEL_SEND 的 Endpoint/IngressKey/Proposal 与 ACCEPTED ingress、冻结 Channel Binding
  和 source final ModelAttempt 闭合。
- ACTION_PROPOSAL 内联有界 CanonicalInput 与 PreparedPayload；CHANNEL_SEND_PROPOSAL
  冻结 exact Endpoint、Ingress、reply target 与有界发送正文。各自 ContentDigest 是唯一
  Proposal 身份，不建立 job/outbox/observation 副本；Provider 无权提交路由、Effect、
  Attempt 或 permit。
- PENDING 不得带任何 outcome 字段；SUCCEEDED 必须有与 kind 匹配的 RESULT，receipt
  可空；FAILED 必须有明确 error；UNKNOWN 必须至少保留 external operation、receipt、
  evidence 或 unknown reason 之一。已知 external operation/receipt/evidence 是单调事实，
  终态转换不能擦除。
- 对 `CHANNEL_SEND`，`RECONCILIATION_EVIDENCE` 只能由原 UNKNOWN 的专用
  reconciliation API 写入；普通 Channel Gateway outcome 夹带 evidence 必须在写事务前
  拒绝。ACTION 保持其既有 outcome/evidence 合同，不因 Channel 泛化被静默改写。
- `UNKNOWN` 不得回到 PENDING；对账只 CAS 原 Attempt。ACTION 不得再次
  Describe/Prepare/Execute；CHANNEL_SEND 不得再次发送；两者均不得换 Binding/Proposal、
  生成替代 Attempt 或自动新 Run。
- `model_dispatch_attempts.source_dispatch_attempt_id` 只有
  `MODEL_READY_AFTER_ACTION` 创建的模型二 Attempt 可以非空，且必须唯一指向同 Run、
  同 Member 的 SUCCEEDED ACTION Attempt。模型一、Pure Chat 与 Channel 最终模型 Attempt
  必须为空；Channel send 反向以 `source_model_attempt_id` 绑定最终模型结果。
- 首个 `text.stats` 是本地 Built-in，首个 loopback Channel 也没有独立 Usage 合同，
  因此不伪造 Usage 行；任何远程外部效果在激活前必须先冻结真实 Usage 合同，
  不得把缺失值写成零。
- MCP Tool 继续以 ACTION kind 复用本表；MCP request ID、短生命周期 process 或 stdio
  session 都不是新的业务身份，不建专用账本。CHANNEL_SEND 同样不建物理
  `channel_outbox`；所谓 Outbox 只是该 kind 的 PENDING/UNKNOWN 状态投影。

#### Usage

- 每个 Attempt 在创建 `PENDING` 时同时创建一行 `model_usage`，revision 从 0
  开始，未知字段和 `ledger_sequence` 保持 `NULL`。
- 首次提交可计入用量账本的 Usage 时，在 Run 内分配单调且唯一的
  `ledger_sequence`；`LoopFrame.UsageLedgerRef` 指向
  `(run_id, last_ledger_sequence)`，不复制一份“剩余额度”。
- `UsageLedgerRef` 的唯一 wire 格式为
  `usage-ledger/v1/<run_id>/<ledger_sequence>`；RunID 仍使用 256-byte opaque ID
  规则，sequence 不超过 SQLite `MaxInt64` 且不得有前导零。完整引用使用
  292-byte 专用上限，恢复时必须拒绝其他 Run 的引用。
- token 与其他运行用量事实一旦随 Attempt 终态提交便不可改写。后续任何 Usage
  对账更新必须 CAS `model_usage.revision`；不得覆盖已提交的 token 事实，
  也不追溯改变已经执行的 Runtime Usage Ledger 位置。
- `input_tokens`、`cached_input_tokens`、`uncached_input_tokens`、
  `output_tokens`、`reasoning_tokens` 均为独立语义字段。
- 仅当三个输入字段均已知时检查
  `input_tokens = cached_input_tokens + uncached_input_tokens`。
- Provider 将 reasoning token 计入其他字段时，必须保留原始 receipt 和规范化
  说明，避免重复计入。
- `model_usage` 只保存 token 事实与 `usage_status`。FAC2 基线已删除
  `model_price_snapshots` 表及 `estimated_cost`、`provider_reported_cost`、
  `reconciled_cost`、`currency`、`billing_version`、`price_snapshot_id` 全部列；
  不得以任何形式重新引入金额列、价格表、金额外键或永久为 `NULL` 的金额字段。
- Usage 缺失时保持 `NULL/UNKNOWN`，不得伪造零值；显式零保持零。
- Observer 只能读取或在独立派生库聚合，不能修改 `model_usage`。
- Composite 的唯一 shared dispatch budget 是 Parent manifest 中静态
  `Composite.Plan.FamilyModelDispatchLimit`。W5 Decision family 含 N 个 Specialist 时必须精确为
  `2N+3`：初始 N 次、Reviewer0、最多 N 次 repair、Reviewer1 与 Root merge 各一个物理 slot。
  未启用 Decision 的历史 family 继续按其冻结 N+1/N+2 合同恢复，不能被当前规则改写。
  PENDING/MODEL_UNKNOWN 已消耗 slot，不退款、不替换。每个 Run 继续使用本节原
  `model_usage`/Usage Ledger；family token usage 只能按 immutable family 图对这些相同行派生
  聚合，UNKNOWN 仍为 UNKNOWN，不新增通用 pool、balance、reservation 或 family
  usage 表。
- 唯一只读投影 `GetCompositeFamilyUsageProjection(rootRunID)` 返回进程内 DTO
  `CompositeFamilyUsageProjectionV1`，其中包含 `CompositeFamilyRunUsageFactV1`、
  `CompositeFamilyAttemptUsageFactV1` 与 `CompositeFamilyUsageAggregateV1`；没有 canonical
  wire/schema，也不持久化第二账本。W5 Decision family 在一个 SQLite read transaction 中按
  `initial Children → Reviewer0 → RepairChildren → Reviewer1 → Root` 恢复完整 `2N+3` 物理
  Run 投影；dormant/skipped repair Run 保留且 `Attempt=nil`，无 Attempt 不合成零。subset/all
  repair 中实际存在的 Attempt 必须纳入 aggregate 与 cap。每个 token 字段只有在全部已用 slot 对应
  Usage 值均非 NULL 时才返回总和，否则保持 UNKNOWN；PENDING/MODEL_UNKNOWN 仍计入
  `AttemptSlotsUsed`。聚合只覆盖 token 字段，不存在第二类金额聚合。
  普通 Run、非 Root Run、repair round/Root/Parent/物理 slot、Admission、Member、Agent/Profile、
  TaskInput、Assignment、logical step、Attempt/Usage 或 dispatch cap 闭包损坏均
  fail-closed。

## 6. 事实归属

| 事实 | 唯一权威归属 | 非权威引用 |
|---|---|---|
| 当前期望配置与模块目录 | `control_current → control_snapshots + runtime_catalog_generations` | Snapshot 保存两者的精确 ref/digest |
| 模块包身份 | `module_installations` | Activation/Catalog 保存 installation ref/digest |
| 本地执行实例 | `module_activations` | PortBinding 保存 activation ref/revision |
| 实际成员装配与绑定授权 | `member_execution_snapshots` | Manifest 保存 member snapshot refs/digests |
| Run 恢复根 | `run_manifests` | Run row 不复制 canonical Manifest |
| 当前循环位置 | `loop_frames` | Event 只审计变化 |
| 模型调用状态 | `model_dispatch_attempts` | Frame 保存当前 pending Attempt ref |
| Action 定义与路由 | `member_execution_snapshots.Actions + 对应 PortPlan` | 模型请求只投影 PublicActionID/Description/InputSchema |
| Channel Cursor 与入站去重 | `channel_ingress_receipts` 的 append-only revision/event 序列 | 当前 Cursor 是每个 Tenant/Endpoint/Scope 最大 revision 的 `cursor_after_ref` 投影 |
| Action/Channel 外部效果执行与对账 | `dispatch_attempts`（`ACTION|CHANNEL_SEND`） | Frame 保存互斥的 pending dispatch ref；kind-specific Proposal/Result 是内容引用 |
| 模型 token 用量 | `model_usage` | 报表仅派生；后续 Usage 对账按 revision CAS |
| Composite family 图与取消 | Parent/Child `run_manifests` + `runs` 四个 nullable 投影列 | Frame/Event/compilation 只引用 exact immutable closure；family usage 是 `model_usage` 派生查询 |
| Conversation 固定身份与当前 head | `conversations`（W1 开发切片已实现） | `runs` 只保存 turn index/predecessor 查询投影；Manifest 冻结 `ConversationTurnRefV1` |
| 单 Run 回答历史 | `history_entries` | Conversation 历史由 predecessor Run 的 `TASK_INPUT + 最终 ASSISTANT` 派生，不复制旧行 |
| Agent 轻量记忆 | `agent_memory_revisions → MEMORY_SNAPSHOT content_records` | ContextCompilation 只保存本 Attempt 冻结的 exact revision/evidence；当前 head 由最大 revision 派生 |
| Learning 周期策略与水位 | `learning_cycle_schedules` | CLI/报告只读取或使用 exact Tenant/Schedule/digest/revision，不复制 canonical 策略 |
| Learning 逻辑窗口、执行与 Proposal 投影 | `learning_cycle_tasks → run_manifests/model_dispatch_attempts/content_records/learning_proposals` | Tick/reconcile/report 只返回脱敏引用；Task 不取代普通 Run/Attempt/Usage 或 Proposal 账本 |

Canonical JSON 是 Snapshot、Manifest、Catalog 等复合对象的权威字节；表内用于查询的
tenant、workspace、member、revision 等标量必须在写入时从 canonical 对象校验，
且不能被独立修改。

## 7. 事务边界

### 7.1 新 Store 初始化

底层 Store 初始化固定为：

```text
InitFreshCurrentStore
  → 验证目标为全新路径
  → 在目标同目录创建 0600 临时文件并使用 journal_mode=DELETE
  → 验证 application_id=0、user_version=0 且没有应用对象
  → BEGIN IMMEDIATE
   → 依次执行冻结的 0001_current.sql 与连续 0002_server_owned_review.sql
  → 计算并校验 schema fingerprint
  → 写 store_meta 并设置 application_id/user_version
  → COMMIT
  → 关闭并确认不存在 -wal/-shm/-journal
  → 用只读 verifier 重新打开验证
  → no-replace 原子发布主数据库文件
  → 再次只读验证且 store_instance_id 不变
```

任一步失败都删除本次尚未发布的临时文件；不得修改用户已有文件。初始化使用临时
路径并在完整验证后原子发布到最终路径。禁止在 WAL 尚未 checkpoint 或仍存在
`-wal/-shm/-journal` 时只重命名主数据库文件。

生产 `freeagent init` 在此基础上完成 seed 与 artifact 的一次性发布：

```text
准备并验证 seed/assertions
  → 分别在数据库和 artifact 目标的同一父目录 staging
  → 复制并校验全部 content-addressed artifacts，fsync staging tree
  → InitFreshCurrentStore(staged database)
  → OpenExistingCurrentStore，并只通过正常 Store API 导入 seed
  → 关闭 Store
  → PrepareClosedCurrentStoreForPublication(staged database)
  → no-replace 发布 artifact root，重新精确校验并 fsync
  → no-replace 发布 database，fsync 父目录；database 是 init commit marker
```

`PrepareClosedCurrentStoreForPublication` 必须取得与正常写启动相同的物理 owner
fence；存在活动 owner 时拒绝。它在 fence 内重新只读验证，执行
`wal_checkpoint(TRUNCATE)` 并要求 `busy=0、remaining=0`，切换到
`journal_mode=DELETE`，拒绝全部 SQLite sidecar，fsync 主文件，再次验证
identity/fingerprint 和 `store_instance_id`。它不得 seed、migrate、repair 或启动
Runtime。

生产 init 先发布 artifact、最后发布数据库。只有数据库存在才表示整组初始化已
提交。若进程在 artifact 发布后、数据库发布前崩溃，重试仅在数据库仍不存在且现有
artifact root 与本次 prepared seed 的完整 digest、size 和根目录条目精确一致时
复用该孤儿目录；存在额外、缺失、符号链接或摘要不符时一律失败。该窄恢复只属于
确定性 init，不把“已有 artifact 目录”放宽为通用覆盖或合并语义。

### 7.2 Module Install

模块包在事务外完成解包、大小限制、Manifest schema、artifact digest、签名/本地
allowlist 和静态安全检查。随后在一个短事务中：

1. 写入缺失的 immutable content。
2. 插入 `module_installations`。
3. 对完全相同记录幂等返回；同 ID+Version 不同 digest 则回滚。

检查失败的包不得留下“半安装”记录。

### 7.3 Seed/Control/Catalog 发布

JSON seed 只通过正常 Store API 导入，不允许绕过约束直接写表。一次发布事务必须：

1. 校验唯一 `control_current.pointer_revision`。
2. 写入缺失的 content；校验 Catalog 引用的每个 immutable Activation 已存在且
   与 Installation/Manifest/本地授权结果逐字段一致。Activation 可以在发布前
   staging，因为未进入 Catalog 的 Activation 不授予运行可用性；也可以由同一
   受控发布事务写入，但两种路径不得并存为不同事实源。
3. 插入 immutable Control Snapshot。
4. 插入引用该 Control Snapshot 的 immutable RuntimeCatalog generation。
5. 用一次 CAS 同时更新 `control_current` 的 snapshot ID、catalog generation ID
   和 pointer revision。
6. 再次验证两者 tenant、revision、单向摘要引用和 digest 配对后提交。

普通 publication 仍只写上述 Control/Catalog pair。W2-E3 的 `WORKSPACE_CHANNEL_ENDPOINT`
`ENABLED` Apply 是同一事务的窄扩展：它必须在这次 `BEGIN IMMEDIATE` 中同时插入该 Endpoint 的
revision-0 `CURSOR_SEED`；不得先发布 Endpoint 再补 seed，也不得先写 seed 再发布 Control。

CAS 失败则整笔发布回滚。结构上不存在第二个 current pointer，因此不能让新
Control 配旧 Catalog 对其他事务可见。

#### 7.3.1 Operator Module Apply v1

首个运行后模块控制面必须复用本节唯一发布事务，不新增 apply table、receipt、current
pointer 或 writer。命令只在服务停止且取得正常 Store owner fence 后运行；它先执行既有
Store-only startup/shutdown recovery，把全部 crash-left PENDING 按原状态机收口为 UNKNOWN，
并在发布 CAS 前再次证明全局 PENDING 为零。恢复失败或存在活动 owner 时不得改变 current
pointer。既有 UNKNOWN 不阻止 deny-only 禁用，也不得被删除、重分类或重放。

输入是 exact canonical `module-apply-plan/v1`：共同字段为 desired state、Tenant、tagged
`binding_target`（`PROFILE | WORKSPACE_CHANNEL_ENDPOINT`）、Instance、exact Port 和
`expected_pointer_revision`；`ENABLED` 还必须携带精确 Module ID、Version、ArtifactDigest、
`expected_runtime_request:{mode,protocol}`，以及与 target 匹配的 Binding payload。Profile target
使用 `port_binding_index`、Config、Authority ceiling 与 FailurePolicy；Channel target 还必须携带
Operator-owned Endpoint route 和有界 canonical `cursor_seed`。Model Profile target 还可以携带
完整目标状态中的可选 `model_profile`；提供时必须与目标 Model 精确闭合，省略时明确清除旧
ModelProfile ref，而不是隐式沿用。`DISABLED` 禁止携带 artifact、
runtime request、Binding、Endpoint 或 seed payload，只删除 tagged target 的精确 Binding/Endpoint。
`model.generate/v2` 不接受 `DISABLED`，回滚必须提交新的 `ENABLED` Apply。
计划 digest 确定性派生下一 SnapshotID 与 GenerationID；Plan 不含时间戳、nonce、
Trust、ExecutionClass、adapter identity、handler identity 或自动 fallback。

`expected_runtime_request` 只表达 Operator 对待安装构件 Manifest runtime request 的精确预期，
不是授权。Core 使用 exact Port、该 request 与已验证 consumer schema 选择唯一固定 handler；Plan
与 Manifest 都不能授予 Trust、ExecutionClass、AdapterIdentity、Authority 或 handler identity，
也不能借该字段启用本地策略表之外的协议或实现。

`ENABLED` 的物理与 Store 顺序固定为：

1. 在 Store 写入前离线验证源 artifact，复制到 artifact root 内的隐藏直系临时目录，再次
   按 exact digest/size 复验；该位置与 final target 必然同文件系统。声明式 Context 只恢复
   canonical `static-context/v1`；Knowledge/Memory 数据包只构造固定内建只读 Adapter；MCP
   registration probe 不启动目标进程。
2. no-replace 发布 `<artifact-root>/<artifact-digest>` 并同步。已有目录只在完整复验精确一致、
   全部普通文件完成有界 fsync、再次复验且 no-launch Host 构造闭合时复用；任何不同字节、
   链接、额外或缺失条目均拒绝且不得覆盖。final 复验后必须先删除隐藏 staging 并同步
   artifact root；失败时不得开始 Install/Activate/PutContent。
3. 先按 `(module_id, exact_version)` 读取既有 Installation；精确 manifest/digest 一致则复用
   其真实 InstallationID，不一致则冲突，不存在才调用 `InstallModule`。
4. 若 current Catalog 已有同 Instance 且精确 provider identity 一致则复用；否则按该
   Tenant+Instance 已有最大 ActivationRevision + 1 创建新的 immutable Activation，避免上次
   CAS 前崩溃留下的不可达 Activation 与重试冲突。
5. 以 `PutContent` 精确写入 Config 与 Authority；声明式 Context 还写入从已验证 entrypoint
   恢复的 STATIC_CONTEXT。构造下一 Control/Catalog 后，只通过
   `PublishControlCatalog(expected, expected+1)` 同时公开。

Channel handler 不走两段式 Cursor 初始化。它固定使用
`PublishControlCatalogWithChannelCursorSeed`，在同一 `BEGIN IMMEDIATE` 中完成上述
Control/Catalog CAS、验证下一 Control 中唯一启用 Endpoint 与 exact Binding、插入 revision-0
`CURSOR_SEED` receipt，并返回同一 publication 与 seed 事实。seed 必须闭合 Tenant、Workspace、
Endpoint、Cursor scope、binding digest、canonical before/after ref 与 revision 0；event、Admission、
Run identity 必须为空。任一 existing seed、identity、binding、digest 或 CAS 冲突均整笔零写回滚。

exact retry 在 current pointer 已是本计划 `expected+1` 时，除逐字节重建 Control/Catalog 外，还必须
读取 revision 0 的 `CURSOR_SEED` 并与原计划逐字段比对。即使该 scope 的当前 Cursor 已经推进到更高
revision，也只能验证原始 seed，不能要求 current 等于 0，更不能把当前 Cursor 冒充发布 seed。
Channel Dry-run 只通过 ReadOnlyObserver 验证 projected publication/seed；不写 Store 或 artifact
root、不运行 recovery、不解析 Secret、不构造 Adapter、不联网。

下述 Memory 变体同步 W3-M1 已验收合同，不把实现重新归属于 W2-D。它只接受 exact
`UNTRUSTED_DATA + memory-context-binding/v1`、Core-owned
`memory-authority-ceiling/v1` 和固定内建 `freeagent.adapter.memory.deterministic/v1`。只有下一
Control/Catalog 已成功发布并重读闭合后，Apply 才能以现有 `agent_memory_revisions` 为唯一事实源
补齐一次空 Genesis；已有 head 必须逐字节保留。CAS 失败不得写 Memory，post-CAS Genesis 失败
必须 fail-closed，同一计划重入只允许幂等完成该闭合。Disable 不删除或改写任何 Memory revision。

`PROFILE` target 的 Binding 顺序不得排序。`port_binding_index` 是计划 exact Port Binding 子序列内的
0-based 插入位置：小于数量时插到对应同 Port Binding 前，等于数量时紧随最后一个同 Port
Binding；当前没有该 Port Binding 时追加到 Profile 的 Binding 列表末尾。其他 Port Binding 的
相对顺序保持不变。同一目标 Profile、Port 和 Instance 最多一个。`WORKSPACE_CHANNEL_ENDPOINT`
target 不使用 Profile 顺序；Endpoint 自身固定携带一个 `REQUIRED` Channel Binding，同一
Workspace/Endpoint 唯一。这些规则比通用 Control 格式更窄，用于保证 Apply/Disable 无歧义。

`DISABLED` 按 tagged target 从下一 Control 删除目标 Profile 的精确 Binding 或目标 Workspace 的
精确 Channel Endpoint。若其他 Profile 或 Endpoint 仍引用同一 Instance，Catalog entry 保留；
最后一个引用消失时删除下一 Catalog entry，但不删除 Cursor、ingress receipt、任何 immutable
Store row、artifact、Run、Attempt、Module History 或 evidence。未来新的调用会被 current Activation
检查拒绝；已有 UNKNOWN 仍能按原 Attempt 和 frozen Provider 走原对账 CAS。

跨文件系统与 SQLite 崩溃可能留下不可达 artifact、Installation、Activation 或 Content；它们
在进入 current Catalog 前不授予执行权，不需要第二事务表。精确重试先识别：current pointer
已经是 `expected+1` 且确定性 SnapshotID/GenerationID 与 canonical bytes 完全一致时返回
`ALREADY_APPLIED`；pointer 仍为 expected 时可继续复用相同 staging；任何其他 pointer 均为
conflict，不自动 rebase。若 commit 返回异常，重读仍不能区分旧/新 pointer，则公开
`APPLY_OUTCOME_UNKNOWN`，但该词只描述 Operator 发布结果，不得写入或修改 Model/Action/
Channel Attempt。

错误码必须服从重读事实：只有 current pointer 已离开 expected 且不是本计划的完整
`expected+1` publication 时才是 `POINTER_CONFLICT`；pointer 仍为 expected 的 immutable row、
closure 或 CAS 前冲突属于 `PUBLICATION_FAILED`，不能伪装成另一个计划已经取得 pointer。

为验证 `ALREADY_APPLIED` 的整份 canonical publication，Store 只提供窄只读
`LoadControlCatalogRevision`：按 Tenant + Control revision + Catalog generation 恢复前一对
不可变合同，并证明 Catalog 精确闭合到 Control。Operator Apply 用它从 canonical Plan 重建
预期 publication，再逐字节比较 current refs/canonical。Runtime、Assembly、Admission 和模块
不得把该历史查询当作 latest、fallback、重装配或第二 current pointer；唯一 live authority
仍只有 `control_current`。

Core 通过窄只读 API
`LoadPublishedBasis(ctx, tenantID) (PublishedBasis, ControlSnapshot,
CatalogGeneration, error)` 加载当前编译基础。该 API 必须严格恢复并校验 current
pointer、两张 immutable row 的 tenant/revision/digest/canonical projection，以及
Catalog 对 Control 的单向摘要闭包；Tenant 尚无 current pointer 时返回明确的
not-found。返回值必须与 Store 内部缓冲区分离，且不得暴露 SQL handle、任意查询或
第二份 current 投影。需要 canonical wire bytes 的调用方只能通过对应 contract
constructor 从已验证的返回值重建。

停机态 `module-list`、`module-history` 与 `module-inspect` 的 Store 访问必须使用
`OpenReadOnlyObserver`，不得以 `OpenExistingCurrentStore` 取得 writer fence。Observer 只接受
现有、self-contained 且无 WAL/SHM sidecar 的数据库，以 SQLite immutable/query-only 打开，
不运行 recovery、不发布 Control/Catalog、不暴露 `*Store` 或通用 SQL。`module-list` 使用
`LoadPublishedBasis` 观察 current Basis，并通过既有窄 Installation 投影闭合 stored Manifest；
`module-inspect` 只对 current exact Instance 使用同一 Store 闭包，artifact root 的完整复验由
命令在 Observer 之外完成。

`module-history` 不增加 Store API、历史 pointer 或派生状态。调用方必须显式提供同一 Tenant 的
exact Control revision 与 Catalog generation；命令以既有 `LoadControlCatalogRevision` 恢复并
验证 Catalog 对 Control 的不可变闭包，再通过既有 `GetModuleInstallationByIdentity` 窄投影读取、
解析 stored Manifest，以闭合 Source、Activation、Port 与当时的目标 Profile Binding 或 Workspace
Endpoint Binding refs。历史输出
直接携带 `ControlSnapshotRef + CatalogGenerationRef` 和 refs，不读取 artifact root，不输出
Manifest 正文或摘要，不构造 historical PublishedBasis，不虚构旧 `pointer_revision`，不得把未进入
Catalog 的 orphan Activation 推断成已禁用，也不得枚举、fallback、计算 diff 或重新装配。不存在
或 mismatched pair 返回明确 not-found；唯一 current pointer 继续只有 `control_current`。

三个查询都不会独立读取、materialize 或输出 Config、Authority、StaticContext 正文，也不会
取得 writer、运行 recovery 或建立外部效果。

`module-disable` 只接受 canonical `DISABLED module-apply-plan/v1`，随后复用本节唯一 writer、
startup recovery 与 PublishControlCatalog CAS。list/history/inspect/disable 不增加表、migration、
backup 格式、current pointer、receipt 或账本；禁用继续保留 immutable Installation、
Activation、Content、artifact、历史 Run/Attempt/evidence，只改变未来 Control/Catalog。

`module-dry-run` 只允许通过 `OpenReadOnlyObserver` 执行完整 current-state 判断和候选投影。
Observer 可以增加 `LoadControlCatalogRevision`、latest Activation、Content 和完整
`ScanStartupRecovery` 的窄只读包装，但不得暴露 `*Store`、SQL、writer、lease、recovery 或
publication 能力。扫描只把 Model/Action/Channel PENDING 汇总为一个恢复需求布尔；UNKNOWN
不算可恢复项且不得被重放。incoming artifact 只允许在系统 TEMP 副本中规范化和验证；Store、
source、artifact root 的 bytes/mode 与 sidecar 集合必须不变，清理失败不得返回成功。
candidate Basis 是绑定 observed Basis 的 `PROJECTED_NOT_RESERVED` 投影，不预留 pointer revision、
Activation 或 receipt；后续 Apply 必须重新取得 owner、运行 recovery、观察并执行唯一 CAS。
该命令不增加表、migration、backup 格式、current pointer 或账本。

Loop 获取精确 lease 后，只能通过窄只读
`LoadRunForLoop(ctx, lease)` 读取同一 SQLite snapshot 内的 Manifest、唯一
MemberSnapshot、Frame、ModelDispatchAttempt/Usage、History 与全部必需 ContentRecord。
它先从已恢复的 MemberSnapshot 和 Run 的 Channel ingress 归属判断是否含
`action.provider/v1` 或 `channel.transport/v1` PortPlan：只有相应可选能力存在时才查询/
恢复对应 kind 的 DispatchAttempt；默认 Pure Chat 不含二者时只断言
`pending_dispatch_attempt_id IS NULL`，且单 Run 热路径不得发出任何
`dispatch_attempts` 或 `channel_ingress_receipts` SQL。该 API
校验 lease owner/epoch/Run revision/Frame
revision/expiry、Frame 与未决 Attempt 的唯一对应、Attempt/Usage 一对一关系、
UsageLedgerRef 与连续 Usage ledger head、History 与成功 Attempt 的来源关系、
Event head 连续性及冻结内容闭包；READY/TERMINATED 不得隐藏 Model
PENDING/MODEL_UNKNOWN 或 ACTION/CHANNEL_SEND PENDING/UNKNOWN，同一 Run 不得出现两个
未决 Attempt。Channel-origin Run 还必须恢复唯一 ACCEPTED ingress、Envelope 与
Cursor/Event identity 闭包。它不得读取 `control_current` 重新编译 Run。可选能力仍只表现为
`PortPlan + ContentRecord`，不增加 Role、Skill、RAG 等专用恢复字段。

仅当 manifest 含 `ConversationTurn` 时，`LoadRunForLoop` 才能沿冻结 predecessor 逐 Run
点查，不得按可变 `conversations.head_run_id` 替换当前 Run 的 predecessor。它必须验证 turn
index 连续、固定五元组一致、每个 predecessor 为成功终态，并按最旧到最新返回完整
`TASK_INPUT(USER) + final history_entries(ASSISTANT)` pair。它不得把旧 History 插入当前
Run、创建 Session/summary head，或读取可选模块来补全缺失 turn。已存在
`context_compilation_ref` 的 PENDING、MODEL_UNKNOWN、重入和恢复只加载原 canonical
compilation/request，不能重新遍历链、再次摘要或重做 Drop。

仅当已恢复 manifest 为 Composite ROOT 时，`LoadRunForLoop` 才能按冻结 Plan 点查 exact
初始 Children、Reviewer、repair Children/Reviewer、各自 manifests/snapshots、terminal Model
results/revisions、Transfer evidence 与 cancellation refs。它必须验证 same Tenant/TASK_INPUT、
每 Run 单 Workspace、Root/Reviewer Workspace、Child Transfer plan、depth=1、parent digest/物理
slot 反向闭包和 dispatch cap；不得按 `parent_run_id` 扫描发现未冻结 Run，也不得读取 current
Control 重新组图或用当前 grant 替换历史 grant。该闭包在 dormant/skipped repair、
`WAITING_CHILDREN`、merge Attempt 恢复和终态 exact retry 中保持可重算；普通 Run/Pure Chat
不发出这些 family/transfer 查询。

生产 Loop 只有 RunID 时使用
`AcquireCurrentRunLease(ctx, {RunID, OwnerID, TTL})`。该方法必须在同一个
`BEGIN IMMEDIATE` 事务内读取当前 Run/Frame head、判断空/过期 lease，并对该
head 完成 epoch 与 Frame revision 的 CAS 推进；不得先读 revision、再通过第二个
事务获取 lease。精确 revision 版本的 `AcquireRunLease` 继续用于已有内部 CAS
场景，两者操作同一张 `loop_frames`，不是第二套 Lease Manager。

Run 已终态后，CLI/HTTP 只能通过只读
`GetTerminalRunResult(ctx, runID)` 获取回答或明确失败。该方法在一个 SQLite
snapshot 内按 continuation 的 `attempt_kind + attempt_id` 核对 Run/Frame、终态
Model/Dispatch Attempt、Usage、History 来源与 Result Content；最终模型成功必须有且
只有一个来自模型二（或无 Action 时模型一）的 ASSISTANT History，模型一的 Action
请求不能冒充回答。Action 或 Channel 明确失败可以作为终态 failure，UNKNOWN 绝不是
终态；Channel 成功终态还必须闭合唯一 CHANNEL_SEND_RESULT 与 ingress/event identity。
Composite permit 前的确定性失败是唯一无 Attempt 例外：continuation 的
`core_failure_reason` 必须与同 revision 的 `CORE_DETERMINISTIC_FAILURE` event/payload
逐字节闭合。事件类型固定为 `CoreDeterministicFailureEventV1`，wire 固定为
`core-deterministic-failure-event/v1`，且只能由 `CommitCoreDeterministicFailure` 写入；
不得存在 Model/Dispatch Attempt、Usage 或 History。
失败不得伪造输出。返回 canonical bytes 必须是防御性副本。该读取不获取 lease、不写
状态，也不依赖进程内 Provider 响应。

### 7.4 Run Admission

Assembly Compiler 可以在事务外编译，但发布前必须在一个事务中重新验证其读取的
Control/Catalog pointer revisions。Admission 原子写入：

1. 写入尚不存在的 `TASK_INPUT`、`POLICY`、`STATIC_CONTEXT`、`CONFIG` 和
   `AUTHORITY_CEILING` immutable ContentRecord。
2. `runs` 初始行。
3. 所有 `member_execution_snapshots`。
4. `run_manifests`。
5. revision 0 且 `LastAuthoritativeEvent=0` 的 `loop_frames`。
6. sequence 0 的 `run_events`。

提交前校验所有 ref/digest、Workspace scope、Agent/Profile refs、PortPlan 与
authority ceiling，并验证 S1 只有一个 Member、一个 Workspace、`ParentRunID`
为空。`control_current` 在编译后发生变化时 Admission 失败并重新编译，不能偷偷
发布旧新混合装配。
TaskInputRef、MemberSnapshot 内全部 PolicyRef、静态上下文、Config
和 AuthorityCeiling 必须在提交前已存在，或由上述同一事务写入；每个 kind、bytes
与 digest 必须匹配。其中静态上下文必须从冻结 PortBinding.StaticContextRefs
枚举，不接受调用方另传的 ad-hoc required 列表。只保存 ref 而没有可恢复 bytes 的
Admission 必须回滚。
成员存在 `ModelProfileRef` 时，该画像 CONFIG 也属于同一 Admission 内容闭包；提交前
必须再次验证其与冻结模型 Binding/Config/Activation 和 ContextPolicy 精确匹配。
没有画像时不得探测或加载任何未引用画像 CONFIG。
存在 Action PortPlan 时，Admission 还必须逐项恢复 `action-binding-config/v1` 与
`action-authority-ceiling/v1`，证明私有 AdmissionActionMaterializer 输入绑定同一
PublishedBasis revision、BindingSpec index、exact Provider、Config 与 Authority，
并闭合 PublicActionID→ProviderActionID、BindingIndex、Description、InputSchema、
Effect、MaxResultBytes 与 DefinitionDigest，再把有序映射包含进 MemberSnapshotDigest。
Describe 发生在事务外；materializer 不得获得 Run/permit/Store writer/通用 Invocation/
Prepare/executor 权限，提交事务必须重新验证 basis pointer，漂移时整笔回滚。无 Action
PortPlan 时 materialized definitions 必须为空，且不得读取 Action CONFIG/Authority/
artifact/Registry。
S1 的 `TASK_INPUT` 与 `STATIC_CONTEXT` 必须分别严格恢复
`task-input/v1` 与 `static-context/v1`，且使用 canonical `application/json`；
任意 JSON 不能因 ContentDigest 正确就被当成可执行聊天语义。

W1 Conversation 复用同一个 Admission API 和事务。显式 `CreateConversation` 只创建
固定五元组、revision 0、head NULL，不读取 Control/Catalog、模型或可选模块。Conversation
turn 提交时除上述 1–6 项外，还必须：

1. 验证 intent 的 conversation ID、expected revision/head 与唯一 row 完全匹配；首轮
   expected revision 为 0/head 省略。
2. 后续轮次先按 `GetTerminalRunResult` 的相同强度证明旧 head 有最终成功 Assistant
   结果、完整 Usage/History/Attempt closure，并证明固定
   Tenant/Principal/Workspace/Agent/Profile ID 未漂移；本轮 Manifest ConversationTurn 的
   PrincipalID 必须等于 Conversation row 与 AdmissionIntent 的 PrincipalID。
3. 写入与 `RunManifest.ConversationTurn` 完全一致的 runs turn 投影，并 CAS
   Conversation revision/head 到本 Run；任一写入或 CAS 失败整笔回滚。

同一 AdmissionKey + intent digest 的精确重入若已提交，必须返回 head 已指向的同一 Run，
即使该 Run 仍 PENDING/UNKNOWN；这只是恢复原 turn，不是继续下一 turn。不同 intent 想从
PENDING、MODEL_UNKNOWN/其他 UNKNOWN、FAILED 或 CANCELLED head 继续时必须 fail-closed，
不得回退 head、跳过该 turn 或创建 sibling。Run 终态不再改写 Conversation row；下一 turn
只读取现有 head 的权威终态后做新的 CAS。

Parent/Child + Composite 第一切片复用上面同一个 Admission API 与事务，不增加 family
writer。事务开始前可以由唯一 Assembly Compiler 完成纯编译，但事务内不得调用模型、RAG
Provider、Module Host 或其他外部服务。提交前必须额外证明：

1. Root 从 exact Control 选择唯一 coordinator 配置；有 2..8 个唯一 slot，整数权重合计
   10000；所有 Run 同 Tenant/Workspace、每 Run 一个 Member、深度恰为 1，Child Agent/Profile
   可以不同但都在同一 PublishedBasis 可达。
2. 整个 family 只含 `model.generate/v2`、声明式静态 Context 和已验收本地只读 RAG；任一
   Member 含 Memory、Action、MCP、Channel、Effect、跨 Workspace、Reviewer/Learning 或
   其他 Port 时整笔零写入失败。
3. Parent 与全部 Child 的 `TaskInputRef/Digest` 相同；Child assignment 逐项匹配 frozen
   slot/focus/weight。Parent manifest 先按规范序闭合 Child Run/Member refs 并计算 digest，
   Child manifest 再引用该 digest/slot，不存在反向 Child manifest digest 引用。
4. Parent intent scope 为 `family`；Child scope 为 `inherited`，Child AdmissionKey 由
   Parent `AdmissionIntentDigest + SlotID` 按固定域确定性派生，调用者不能提交或替换；
   Parent/Child manifest digest 不参与身份输入。
5. Parent `runs`/snapshot/manifest/`WAITING_CHILDREN` Frame/Event 与全部 Child
   `runs`/snapshot/manifest/`READY` Frame/Event、四个 nullable run 投影和引用内容一次性
   写入；family dispatch cap 精确为 `child_count + 1`。任一步失败全部回滚。

Root `(tenant_id,admission_key)` 已存在且 intent digest 相同时，重试必须读取并验证原
immutable family closure 后返回同一 Parent 与 Child RunID；不得再次编译、补造 Child 或
发布部分替代图。digest 不同、原图不完整或 slot 映射漂移必须 fail-closed，不能“修复”。

插入 `runs` 前先按 `(tenant_id, admission_key)` 查询：不存在时才允许分配并插入
RunID；已存在且 intent digest 相同则返回现有 Run，不再次编译或发布；已存在但
digest 不同则 fail-closed。该唯一约束与 Run、Snapshot、Manifest、Frame、初始
Event 必须在同一事务中提交，因此“提交成功但响应丢失”不会产生第二个 Run。

### 7.5 Loop 推进

每次权威推进使用一个短事务，并同时校验：

```text
run_id
expected run revision
expected frame revision
lease owner ID
lease epoch
lease 未过期
```

事务按需要原子写 History、Run/Event 与下一 LoopFrame。
`LoopFrame.UsageLedgerRef` 指向 Usage Ledger 的已提交 revision；
`LastAuthoritativeEvent` 必须与本事务追加的最后 event sequence 一致。模型网络
请求不允许包在 SQLite 事务中。

Composite Parent 的 `WAITING_CHILDREN` 推进只按 immutable manifest 点查：任一 Child
明确 FAILED 时以 `ALL_REQUIRED_CHILD_FAILED` 终止 Parent；任一 Child 为
MODEL_UNKNOWN/等待对账时 Parent Frame 仍保持 `WAITING_CHILDREN`，但返回/投影
`WAITING_RECONCILIATION` disposition；尚有非终态 Child 时保持 Frame 并返回
`WAITING_EXTERNAL`；只有全部 exact terminal result 成功才可 CAS 到 Parent merge 的模型 Begin。Store/
Loop 不创建 Child、不扫描 current Control、不改变 slot/weight，也不提供 fair queue。

`RequestRunCancellation` 是单独短事务：普通 `run` scope 只 CAS 自身空
`cancel_request_ref`；`family` scope 必须从 Parent manifest 恢复 exact 2..8 Child，并在
同一事务把同一 ref first-write-wins 地写入 Parent 与所有 Child。已存在任一不同 ref、图
不完整或调用方直接取消 `inherited` Child 时全部失败。该 API 只安装不可逆 latch，不修改
Run/Frame revision、终态或已有 Model/Dispatch PENDING、MODEL_UNKNOWN/UNKNOWN。Loop 后续
在取得任何新 permit 前观察 latch 并返回稳定取消 disposition；绝不能覆盖原状态、声称
未执行、退款或重放。

### 7.6 模型调用前

```text
冻结 canonical request
  → BEGIN IMMEDIATE
  → 校验 Run/Frame CAS 与 lease epoch
  → 校验 run/family cancel_request_ref 仍为空
  → Composite 时校验 frozen family dispatch cap、slot 与 Parent/Child closure
  → 若装配 Memory，校验 compilation 的 snapshot revision 仍是当前 Agent head
  → 插入 ModelDispatchAttempt=PENDING
  → 插入 revision=0、ledger_sequence=NULL 的 model_usage
  → Frame revision+1 并设置 pending_attempt_id
  → 追加 RunEvent
  → 更新 LastAuthoritativeEvent
  → COMMIT
  → 才允许调用冻结的 model Binding
```

PENDING 提交失败时不得发出网络请求。
所有能返回 Model InvocationGate 或 Gateway permit 的事务都必须在**同一写事务**重验
有效 cancel latch；只在事务外预检查不构成授权。Composite Parent merge 还必须重验全部
Child terminal result 与 `context-compilation/v1.composite.child_results` 的 50% allocation/
规范顺序；Child/Parent 已有一个相应模型 Attempt（包括 MODEL_UNKNOWN）即视为 dispatch
slot 已消费，不得创建替代 Attempt 或退款。
`BeginModelDispatch` 从冻结 Run 派生 Member、唯一 `model.generate/v2` Binding、
Provider/Model 与 parameters；调用方只能提供 Attempt/逻辑步骤、
规范请求、收窄后的 deadline，以及 Context Compiler 产生的可选
`context-compilation/v1` canonical bytes。只有新建事务返回
`InvokeAllowed=true`；相同 Attempt 的精确重入返回原记录并强制
`InvokeAllowed=false`，不同 RequestDigest、deadline 或 Attempt 身份
fail-closed。这样提交响应丢失也不会成为再次发网的许可。

Store 必须先从冻结成员闭包恢复基础 ContextPolicy，并在存在 ModelProfile 时应用
`min(base_window, profile_window)` 得到唯一有效策略。画像不得改变其他策略字段，
也不得选择另一模型 Binding。若携带 Context Compiler 产物，Store 必须在同一事务内：

1. 严格恢复 `context-compilation/v1`，验证 Run、Member、logical step、
   MemberSnapshotDigest 与该 compilation 的引用关系，并验证 compilation 内的
   ContextPolicy、Workspace scope、摘要/Drop 连续性与全部动态 BindingIndex；
   Run/Member/step/digest 只保留
   在 Attempt，不复制进 compilation bytes；
2. 重算 MODEL_REQUEST digest，并要求等于编译记录绑定的 request digest；
3. 对每个 Memory read，严格恢复 snapshot、request/output、Config/Authority 与 exact
   Tenant/Agent/Workspace/Task scope，证明 revision 仍为当前 head；
4. 写入 CONTEXT_COMPILATION、MODEL_REQUEST、Attempt=PENDING、Usage、Frame 和 Event；
5. 将 `model_dispatch_attempts.context_compilation_ref` 指向该记录。

任一校验或写入失败必须整笔回滚且不生成 invocation permit。精确重入必须携带与原
Attempt 完全相同的 compilation bytes；缺失、新增或 bytes 不同均冲突。低于 85%
的无动态 Context 轻路径不得伪造空 compilation；只要冻结成员含动态 RAG/Memory
Binding，或记录了对应输出，即使低于 85% 也必须携带 compilation。未携带 compilation 时，Store 必须
先证明成员没有动态 Binding，再使用 Context Compiler 的同一版本化估算器证明最终
request 严格低于有效恢复水位。达到或超过
有效 85% 水位却缺失 compilation 时，必须在 PENDING、Usage、Frame、Event 和 permit
写入前失败关闭。

若首次 Begin 在同一事务中确认规范化 deadline 已过期，则不建立短暂 PENDING：
原子写入 `FAILED/DEADLINE_EXPIRED_BEFORE_DISPATCH` Attempt、
`NO_USAGE_REPORTED` Usage、TERMINATED Run/Frame 和终态 RunEvent，返回
`Created=true && InvokeAllowed=false` 且不生成 permit。该事务任一步失败必须全部
回滚；精确重入只读回原 FAILED Attempt。

`InvokeAllowed` 不能只是可复制的布尔值。新建结果必须同时携带 Current Store
私有、进程内、共享的一次性 permit；结果副本共享同一个原子消费位，公开字段被
改写、调用方自行构造结果、精确重入或进程重启都没有可消费 permit。InvocationGate
只有成功消费该 permit 后才能授权 Adapter。permit 不序列化、不持久化，也不使用
全局 Attempt map。

模型二是唯一不重新运行 Context Compiler 的 Begin 形状：Frame 必须为
`MODEL_READY_AFTER_ACTION` 并引用同 Run/Member 的 SUCCEEDED DispatchAttempt；Store
恢复该 Action 的 source model Attempt、原 MODEL_REQUEST、ACTION_RESULT 与冻结
ActionDefinition，使用 Core 的单一 builder 证明新 request 只在原 messages 末尾追加
一个 canonical `UNTRUSTED_ACTION_RESULT_JSON:` USER envelope，原 `actions` 与
parameters 逐字节不变。新 ModelDispatchAttempt 的 `source_dispatch_attempt_id` 指向
该 Action；context_compilation_ref 必须为空。任一错链、重新 Describe/Prepare/RAG/
Memory、额外消息或第二 Action Attempt 均在 PENDING/Usage/Frame/Event 前拒绝。
模型一 compilation 必须已经用同一估算器按冻结 MaxResultBytes 预留最坏 envelope，
并把该增量纳入 85%/100% 水位决策；模型二 Begin 重算实际完整请求仍在有效窗口内。
无法闭合预留、ACTION_RESULT 非 AVAILABLE/超限或 RequestDigest 不是“原 MODEL_REQUEST
精确前缀 + 原 ACTION_RESULT 精确后缀”时视为已持久化闭包损坏并 fail-closed，模型二
调用次数为零，不得截断结果、重新编译或改写终态；正常 RESULT_REJECTED 在 Action
终态事务已经明确终止 Run，不会进入 MODEL_READY_AFTER_ACTION。

### 7.7 模型调用后

成功、明确失败或可靠对账结果必须在一个事务中：

1. CAS 原 Attempt revision 和当前 Frame 的 `pending_attempt_id`。
2. 写入 immutable result/receipt/evidence content。
3. 更新原 Attempt 的状态与 ref。
4. CAS 更新同一 Attempt 的 `model_usage`；存在可计量 Usage 时分配下一个
   `ledger_sequence`。
5. 最终 AssistantText 成功且 Run 装配 Memory 时，以当前最新 Agent revision 为 parent，用 Core 的有界
   确定性脚本合并 Workspace-local summary/category/term observation，插入一个
   `MEMORY_SNAPSHOT` 并追加唯一 `agent_memory_revisions` 行；
6. 最终 AssistantText 成功时追加唯一 History entry。
7. 更新 Run/LoopFrame 的 UsageLedgerRef、WaitingReason、
   LastAuthoritativeEvent，并追加 RunEvent。
8. 提交。

相同 Provider 回执的重复终态提交必须幂等；不同结果试图终结同一 Attempt 时
fail-closed 并进入完整性告警。
Memory revision 与最终 Result、Usage、History、Frame、Event 必须同事务提交；模型一
返回 ActionRequest 时走 §7.8，不写 History/Memory。FAILED 与 MODEL_UNKNOWN 不写
Memory。相同终态重入通过 Attempt 终态和唯一 source_attempt_id
返回原结果，不得重复计数。并发 Run 均在事务内读取最新 head 并做合并，不能用请求时
旧 snapshot 覆盖当前状态；事务失败则全部回滚，不增加补偿 worker 或第二 pending 状态。
终态提交必须同时携带由 Module Host 返回并由 Core 绑定的 `InvocationID` 与精确
`ActivatedModuleRef`；InvocationID 必须等于原 AttemptID，Provider 必须逐字段等于
冻结 Binding 的 Provider。来自其他 Attempt 或其他 Provider 的合法 payload 也不得
被写入当前 Attempt。

### 7.8 模型 Action 输出与 Action 调用前

模型一返回 ActionRequest 后，Core 先在事务外完成冻结 ActionID/schema 校验，并通过
同一 exact Provider 的公开 Prepare 取得 canonical payload。Prepare 只有
none/read_only grant。随后唯一 `CommitModelActionAndBeginDispatch` 执行：

```text
BEGIN IMMEDIATE
  → CAS 原 Model PENDING、Run/Frame 与 lease
  → 写 MODEL_RESULT/Usage，将模型 Attempt 置为 SUCCEEDED；仅有可计量事实时按既有规则
    分配 ledger_sequence，否则 ledger head 保持原值
  → 以 post-model UsageLedgerRef 验证 Action 映射、Config/Authority、Proposal、
    Effect、结果上限、deadline 与撤权
  → 写 ACTION_PROPOSAL
  → 插入 DispatchAttempt=PENDING
  → 将同一 post-model UsageLedgerRef 写入 DispatchAttempt/Frame，并把 pending_attempt_id
    从 Model 原子切换为 pending_dispatch_attempt_id
  → Frame=ACTION_PENDING，追加 Event 并更新 LastAuthoritativeEvent
COMMIT
  → 才返回一次性 Gateway permit
```

该事务不写 ASSISTANT History 或 Memory。Action Attempt 必须唯一引用来源模型 Attempt；
同一 logical step 还要跨两个 Attempt 表检查冲突。任何校验、写入或 COMMIT 失败都整笔
回滚，executor 调用次数为零；原模型仍为 PENDING，重启只收口 MODEL_UNKNOWN。模型
wire malformed 才允许把 Model Attempt 写为 FAILED。若 ModelGenerateOutput wire 合法，
但输入非法、未知 Action、超出一动作上限、Prepare 确定失败或 deadline 已过，必须由
单独权威事务保存 MODEL_RESULT + Usage、提交 ModelAttempt=SUCCEEDED 并以对应
`MODEL_ACTION_REJECTED` 原因终止；不创建 Action PENDING 或 permit。FAC2 已删除金额预算
闸门，合法 wire 下不存在以金额事实为由的拒绝路径。post-model UsageLedgerRef 始终指向提交后
的实际 ledger head（可能不变）。该终止事务失败时原模型仍为
PENDING，恢复按 MODEL_UNKNOWN，不能在内存中把成功结果接到替代路径。

Gateway permit 与模型 permit 相同，是 Store 私有、进程内、共享原子消费位；额外绑定
AttemptID、MemberSnapshotDigest、BindingIndex、ACTION_PROPOSAL ContentDigest 与 expiry。Gateway
消费前再次核对 lease/fencing、current Activation deny-only 撤权、Authority、Effect、
预算与 deadline，并把冻结的 effective MaxResultBytes 放入私有
`action-execution-request/v1`，成功后才允许内部 executor。事务不得跨 executor 调用
保持打开。

MCP Binding 使用完全相同的事务和 permit：Admission Describe 的
`initialize + 分页 tools/list` 不产生 DispatchAttempt；Prepare 是无进程、无 RPC、无
Store 写入的纯函数；只有上述 PENDING 已提交且 Gateway permit 消费成功后，私有
executor 才能新建有界 stdio 会话，完成 `initialize → initialized` 并发送一次且仅一次
`tools/call`。它不得在执行时重新 list、换工具、创建第二账本或通过 SDK 重试。

### 7.9 Action 调用后

唯一 `CommitActionDispatchOutcome` 在一个事务中：

1. CAS 原 DispatchAttempt revision、`pending_dispatch_attempt_id`、Run/Frame 与 lease。
2. 核对 InvocationID=AttemptID、精确 Provider/Binding、ACTION_PROPOSAL ContentDigest
   与 `action-execution-result/v1` 状态组合。
3. 写 immutable ACTION_RESULT、PROVIDER_RECEIPT 或 RECONCILIATION_EVIDENCE。
4. 更新原 Attempt 的 SUCCEEDED/FAILED/UNKNOWN 与外部操作引用。
5. SUCCEEDED + AVAILABLE 写 `MODEL_READY_AFTER_ACTION`；SUCCEEDED + RESULT_REJECTED
   与 FAILED 写 TERMINATED；UNKNOWN 写 WAITING_RECONCILIATION。三者都清空 pending
   pointer、追加 Event 并更新 head。
6. 提交。

相同终态精确重入只验证并返回 `Applied=false`；不同终态冲突 fail-closed。UNKNOWN 后
只能由唯一 `ReconcileActionDispatchOutcome` 事务携带可靠 RECONCILIATION_EVIDENCE，
CAS 原 UNKNOWN Attempt revision、WAITING_RECONCILIATION Frame 与 lease/fencing，并只
更新原 Attempt：成功且结果 AVAILABLE 才进入 `MODEL_READY_AFTER_ACTION`；确认 Effect
成功但结果缺失、非 canonical 或超限时写 SUCCEEDED + RESULT_REJECTED 并终止；确认
失败时写 FAILED 并终止；仍不能确认时保持 UNKNOWN/WAITING_RECONCILIATION。该事务不得
Prepare、执行、调用 Gateway、签发 permit、换 Binding/Proposal 或生成替代 Attempt。
Action FAILED/UNKNOWN 不写 Memory；SUCCEEDED 也只提供模型二输入，Memory 仍等模型二
最终回答成功后由既有唯一 writer 更新。

MCP 的精确匹配 JSON-RPC error 与合法 `isError=true` ToolResult 分别作为确定 FAILED
提交；匹配的合法成功结果按现有 SUCCEEDED/RESULT_REJECTED 规则提交。若
`tools/call` 可能已经写出后发生 partial write、timeout、取消、EOF、进程崩溃、响应 ID
错配或 malformed/超限响应，则保守提交原 Attempt 为 UNKNOWN。关闭 stdin、发送
`notifications/cancelled`、终止/强杀子进程或随后重启服务都不能降格为“确定未执行”，
也不能授权第二次 call。

## 8. 外部调用后持久化失败

模型、Action 或 Channel send 已经可能到达 Provider/executor、但终态事务没有提交时，数据库中
的权威事实仍是原 `PENDING`。进程必须停止推进该 Run，不得：

- 在内存中把结果当作已提交；
- 生成第二个 Attempt；
- 切换 Provider/Binding/Action/Proposal；
- 把同一模型请求重新发送，或重新 Describe、Prepare、执行 Action/Channel send；
- 用新 History 掩盖未决状态。

恢复进程取得新 lease 后只处理原 Attempt：

1. 有可靠 provider request/external operation ID 且 Provider 支持幂等查询时，只查询
   原调用。
2. 能确认成功则将原 Attempt 写为 `SUCCEEDED`；Action 只有 AVAILABLE 结果才进入模型二，
   否则写 RESULT_REJECTED 并终止；Channel 写入 CHANNEL_SEND_RESULT 并终止原 Run。
3. 能确认请求未执行或确认失败则写为 `FAILED`。
4. 无法可靠确认则模型写为 `MODEL_UNKNOWN`、Action/Channel 写为 `UNKNOWN`，Run 进入
   `WAITING_RECONCILIATION`。

若数据库本身暂时不可写，进程无法立即持久化 UNKNOWN；它必须 fail-stop。下次成功
打开 Store 时先扫描遗留 PENDING，再允许该 Run 继续。

模型 UNKNOWN 与共享外部效果 UNKNOWN 后续都只允许凭可靠 Provider 证据或人工签署的
`RECONCILIATION_EVIDENCE` 更新原 Attempt。UNKNOWN 不是自动重试队列。

## 9. CAS、lease 与 fencing

### 9.1 CAS

所有可变行都带 revision：

- `control_current.pointer_revision`
- `runs.revision`
- `loop_frames.frame_revision`
- `model_dispatch_attempts.revision`
- `dispatch_attempts.revision`
- `model_usage.revision`

更新语句必须包含 expected revision，并检查恰好影响一行。零行表示竞争或过期，
不能 blind retry；调用方必须重新读取并重新判断。

### 9.2 Run lease

S1 的 lease 只保护单个 Run 的 Loop 推进，不是 S2 的多 Workspace 公平调度器。

- 获取 lease 要求 expected Run/Frame revision 匹配，且当前 lease 为空或已过期；
  成功后将 `lease_epoch` 增加 1，并写 owner 与 expiry。
- 只有 RunID 的重启调用使用 `AcquireCurrentRunLease`，在同一写事务内读取并取得
  当前 head；它不得削弱同样的空/过期判断、epoch 单调性或 Run revision 闭包。
- 续租要求 owner、epoch、expected Run/Frame revision 都精确匹配，且 lease 尚未
  过期。
- 只有 lease 过期后，新 owner 才能 CAS 获取并再次增加 epoch。
- 所有后续权威写入都携带 epoch；旧 owner 即使晚到也因 fencing 失败。
- 释放 lease 要求精确 fencing token，只清空 owner 与 expiry，保留最后一个
  epoch；下一次获取继续递增，不得归零或复用旧 fencing token。
- Acquire、Renew、Release 都是 lease-only Frame CAS：每次成功只将
  `frame_revision` 增加 1，不修改 `runs.revision`，也不追加权威 RunEvent。
- wall clock 只用于判断何时可以尝试接管；安全性来自单调 epoch，而不是时钟精度。
- 进程关闭时应尽力释放 lease，但正确性不能依赖优雅释放。

### 9.3 SQLite writer 规则

- 一个进程内使用一个受控写队列或等价的串行事务协调器。
- 不在事务内调用模型、Module Host、文件系统网络挂载或其他外部服务。
- 锁冲突、I/O 错误和 CAS 失败分别分类；不得统一成可重试错误。
- 任一完整性、foreign key、identity 或 fingerprint 错误立即停止 Admission。

## 10. 启动与恢复

服务启动顺序固定：

1. 只读校验文件 header、identity、fingerprint、integrity 和必要 PRAGMA。
2. 以 Current Store owner 身份打开写连接。
3. 在 Catalog、Registry、Module Host、SDK client 与 Universal Loop 构造前，执行一次
   Store-only safety transaction。它只按 RunID 读取最小 Attempt/Frame/continuation/
   lease 投影，验证一个 Run 至多一个未决项及指针关系。
4. 遗留 Model PENDING 与共享 `dispatch_attempts` 中 ACTION/CHANNEL_SEND PENDING 只用原
   AttemptID、原 revision、原 kind 和原 Frame pointer 做 CAS，原地写为对应 UNKNOWN /
   `WAITING_RECONCILIATION`；不得经过 Universal Loop，不得创建 permit、替代 Attempt
   或新 Run。
5. 现有 UNKNOWN 与稳定状态只校验最小 ledger/continuation/lease 投影。必要的 lease
   acquire/release 只允许推进 lease 账本，不得改变 History、Event、冻结装配或执行语义。
6. 再执行同一确定性扫描，要求没有遗留 PENDING，原 UNKNOWN 仍引用同一原 Attempt；
   CAS 冲突、多个未决 Attempt、未知最小 continuation、非法 lease 或 ledger 不完整均
   fail-closed。
7. safety transaction 成功后才加载当前 Control/Catalog、构造 Registry/Loop，并开放
   ChatService 与新 Admission；启动不会自动发布、seed 或迁移。

该启动事务绝不加载 `member_execution_snapshots` 的 canonical 成员正文、Action
Definition、Channel Endpoint Config/Authority、Secret、artifact、Catalog/Registry、
SDK client 或 Provider，也不调用 Describe、Prepare、Gateway、发送、模型、RAG、Memory
或 MCP。它只复用 Current
Store 的安全状态机和 lease/fencing，不复用 Universal Loop，不增加第二恢复器或后台
worker。完整 MCP artifact 验证只能在启动完成后显式选中 Adapter 的 `Describe/Execute`
边界执行。

同一限制适用于 Composite：启动 Store-only safety transaction 不加载 Parent/Child
manifests、Child results 或 assignment，不 fan-out、不 merge、不创建 Child，也不因
`cancel_request_ref` 把原 Model/Dispatch PENDING 或 UNKNOWN 改成取消。它只按既有最小
Attempt/Frame 投影把遗留 PENDING 收口为原 UNKNOWN。Family 图、取消引用和结果的完整
语义闭包只在启动完成后的显式 `LoadRunForLoop` continuation 或只读 verifier 中校验。

该进程开放流量前的一次全局 Store-only shared-ledger transaction 是 Pure Chat 零
Action/MCP/Channel 可选资源访问口径的唯一例外，必须单独计量：它会扫描同一个
`dispatch_attempts` 的最小 kind/state 投影，但不读取 `channel_ingress_receipts`、
Channel Endpoint/Envelope/Config/Authority、Secret、artifact、Registry 或 Adapter。
W1 Conversation 启用后，Pure Chat 可以读取 Core-owned `conversations` 与 exact predecessor
Run/Content/History，这是普通聊天的必要状态，不计为可选模块访问。Pure Chat Admission 与
单 Run 热路径仍不得读取 `dispatch_attempts` 或任何 Channel 表，
也不得加载 Action/MCP/Channel Provider、definition、artifact、Config、Authority、Host、
Gateway 或 SDK。

优雅关停必须先在线性化点关闭 HTTP Admission，再等待已接纳 handler 排空；排空后、
关闭 Store 前，以有界 `context.WithoutCancel` 派生上下文执行与启动相同的 Store-only
PENDING→UNKNOWN 收口。该路径不得构造或调用 Channel Adapter，不得读 Secret 或发网；
若收口失败则关停 fail-closed，不能在结果尚未落账时把进程内响应当成权威终态。

启动完成后的显式 Run continuation 才可读取：

```text
RunManifest
MemberExecutionSnapshot
LoopFrame
History
ModelDispatchAttempt
ACTION/CHANNEL_SEND DispatchAttempt（仅相应可选 Run）
Channel ingress receipt/Envelope（仅 Channel-origin Run）
ModelUsage
ContentRecord
精确 Installation / Activation
可选 Parent/Child manifests/snapshots/terminal results 与 RUN_CANCELLATION ref
```

其中 ContentRecord 必须闭合 TaskInput、Budget/Permission/Cost/Context/
Scheduling Policy、静态上下文、Config 和 AuthorityCeiling；任一引用不可达、
kind 不符或 digest 不符都必须 fail-closed。静态上下文引用只从冻结
PortBinding.StaticContextRefs 枚举。存在 `context_compilation_ref` 时还必须严格
恢复该记录，且其 request digest 必须等于原 Attempt 的 MODEL_REQUEST；
PENDING/UNKNOWN 恢复不得重新运行 Context Compiler。

存在 Action 时还必须从成员快照恢复冻结 Definition 与 exact Binding，校验来源模型
Attempt、ACTION_PROPOSAL、PreparedPayload、Effect、ACTION_RESULT/receipt/evidence 和
模型二 source_dispatch 链。恢复与闭包验证绝不重新 Describe、Prepare、调用 Gateway
或 executor，也不读取当前同名 Action 定义。

存在 Channel ingress 时还必须恢复唯一 ACCEPTED receipt、Cursor/Event chain、Envelope、
Endpoint/Binding 与 source final ModelAttempt；CHANNEL_SEND Proposal/Result/receipt/evidence
必须逐项闭合。恢复与闭包验证绝不读取 Secret、构造 Adapter、调用 Gateway 或发送网络。

存在 RAG evidence 时，还必须恢复同一 source artifact 并逐字节验证
request/compilation；不得读取当前 Control、最新知识 revision、可变索引或再次调用
RAG Provider。

成员存在 ModelProfile 时，恢复只按成员快照中的精确 ref 加载画像 CONFIG，并与冻结
模型 Binding config/Activation 重新核对。不得查询 current Control、“最新画像”或
评测服务。没有画像的成员不增加 CONFIG 读取。

不得读取当前 Profile、Agent、Workspace、Catalog 或模块“最新版本”重新装配。
唯一例外是 Module Host 在实际调用前读取当前 `control_current`，对冻结
Activation 执行 deny-only 撤权检查；它不得据此替换 Binding、重新排序、扩大权限
或选择“最新版本”。缺少 artifact、digest 不匹配、continuation 版本未知或
required Provider 不可用时 fail-closed。

## 11. Usage 与缓存口径

本 Store 只记录 Provider 返回或 Core 可证明的事实，不推测缺失 token：

| 字段 | `NULL` | `0` | 正数 |
|---|---|---|---|
| `cached_input_tokens` | Provider 未提供/无法确认 | 明确报告未命中 | 明确命中数量 |
| `uncached_input_tokens` | Provider 未提供/无法确认 | 明确无未缓存输入 | 明确未缓存数量 |
| `reasoning_tokens` | 未披露 | 明确为零 | 明确披露数量 |

建议派生指标，但不得回写 Ledger：

```text
cache_hit_rate =
  cached_input_tokens / (cached_input_tokens + uncached_input_tokens)

known_input_coverage =
  count(input breakdown known) / count(all attempts)
```

当分母字段 UNKNOWN 时，指标也是 UNKNOWN，而不是 0%。缓存前缀优化、Judge payload
稳定化等属于请求构建策略；Store 只记录精确 RequestDigest 和结果，不替 Runtime
改变 prompt。

W1 的“每轮统计”直接按 Conversation turn 对应的 Run 查询既有 Attempt 与 `model_usage`，
不新增 Conversation Usage 表。单字段缺失保持 NULL/UNKNOWN；
turn 或 Conversation 派生合计只要所需分量存在 UNKNOWN，对应合计也必须是 UNKNOWN，
不得按 0 补齐。input/output/cache/reasoning token 必须继续区分事实来源；FAC2 基线下
Usage 只有 token 事实与 `usage_status`，不存在第二个账本、金额派生指标或价格对账口径。

## 12. 备份

### 12.1 前置条件

当前备份入口是
`currentbackup.CreateBundle(ctx, sourceDB, artifactRoot, destination, toolVersion)`，
采用离线 owner fence 与 SQLite Backup API 生成一致性快照：

1. 停止 Admission。
2. 等待当前短事务结束。
3. 停止所有 writer，并确认唯一 owner 已关闭写连接。
4. `CreateBundle` 取得与正常 Store 相同的物理 owner fence；活动 owner 存在时
   fail-closed，而不是等待后偷偷读取。
5. 分别记录 Model PENDING/MODEL_UNKNOWN、ACTION PENDING/UNKNOWN、CHANNEL_SEND
   PENDING/UNKNOWN 与 Learning `PENDING|RUN_ADMITTED|UNKNOWN`；并记录 Channel ingress/Cursor
   派生计数。它们可以被备份，但恢复后仍必须沿原事实对账，不能触发替代执行。
6. 使用 SQLite Backup API 生成临时 `database.sqlite`，绝不以普通文件复制读取
   活动 source db/wal/shm。

备份不得触发 migration、seed 或启动 recovery mutation。

SQLite 的只读打开也可能在 source 旁产生 sidecar，因此 `CreateBundle` 在第一次读取
前冻结 `-wal/-shm/-journal` 基线，并遵守以下清理不变量：

- 预先存在的 sidecar 必须是普通文件，且永远不由备份删除或替换；
- 只清理由本次读取新产生的文件；新 `-wal` 或 `-journal` 仅在大小恰好为零时可删；
- 新 `-shm` 仅在确认为普通文件时可删；
- 新的非普通 sidecar，或非空 `-wal/-journal`，视为完整性错误并保留现场；
- 清理在释放 owner fence 和发布正式 bundle 之前完成；失败时不得留下正式备份目标。

### 12.2 完整备份包

```text
backup/
  database.sqlite
  artifacts/<sha256>/...
  manifest.json
```

`manifest.json` 固定包含：

- `format_version=freeagent.current-store-backup/v1`、UTC 创建时间和工具版本；
- Store identity（application/user version、schema identity/fingerprint、generator）
  和 `store_instance_id`；
- database file SHA-256 与字节数；
- Control/Catalog current refs/digests；
- `module_installations.artifact_digest ∪ module_artifacts.artifact_digest` 的去重并集中，每个 artifact
  的路径、digest 和字节数；
- artifact 总数，以及 `model_pending`、`model_unknown`、`action_pending`、
  `action_unknown`、`channel_ingress_receipts`、`channel_cursor_scopes`、
  `channel_send_pending`、`channel_send_unknown` 八个互不混淆的派生计数；
- `manifest_digest`。

`manifest_digest` 必须排除自身字段后计算，避免循环定义：

```text
SHA256(
  "freeagent.backup-manifest/v1\0"
  + CanonicalJSON(manifest_without_manifest_digest)
)
```

Artifact 路径必须是使用 `/` 的规范相对路径；绝对路径、`..` 和大小写碰撞必须
拒绝。

ArtifactDigest 与 backup manifest 不把平台 mode 当作包身份。Bundle 中的 artifact 普通文件
按私有 `0600` 复制；Restore 不信任或传播来源权限位。只有被数据库 Installation 引用的
digest，才在未发布 staging 内根据已验证 Manifest 与 exact MCP descriptor 重建当前支持的
LOCAL_PROCESS 运行所需 mode；ingress-only digest 的全部普通文件保持 `0600`。

`CreateBundle`、`VerifyBundle` 与恢复校验读取 `module.yaml`、bundle
`manifest.json` 和构件普通文件时必须使用有界、可取消的 Context 读取。两个 manifest
读取与构件摘要扫描至多每 32 KiB 检查一次取消；预检树哈希也必须在自身有界分块间
检查取消。取消或读取失败不得返回部分内容/部分树。`context.Canceled` 与
`context.DeadlineExceeded` 保持原 cause，不得伪装成 bundle 或 artifact 完整性损坏；
普通格式、路径、大小和摘要错误仍按原完整性分类处理。该规则不改变成功字节、
ArtifactDigest、manifest digest 或备份格式。

如 Operator 不希望备份未激活安装，必须先在独立、已审计的 GC 事务中证明它不被
任何 Catalog 或冻结 Run 引用并删除对应 `module_installations` 行；备份过程中
不得临时省略数据库仍保留的 Installation 构件。W6-5 不提供 ingress GC；只要
`module_artifacts` 仍保留该 digest，即使没有 Installation，也必须备份对应 inert artifact。

即使没有外部 artifact，`artifacts` 列表也必须显式为空。当前 Channel Cursor/Event、
Ingress、CHANNEL_SEND、W4 Proposal/Review/Version、W4-L4 Schedule/Task，以及 W6-5
Module Artifact/Admission 投影已经位于
同一数据库，并进入同一完整备份协议；禁止只备份“主要表”。

`learning_proposals` 不增加 bundle 文件族、artifact 或 manifest 计数。SQLite snapshot 必须
逐字节保存 Proposal canonical、exact Draft canonical、全部去重投影、state/revision、
review refs 和 proposer lineage。`CreateBundle`、`VerifyBundle`、`RestoreBundle` 与
`VerifyCurrentStoreSemanticClosure` 使用同一个只读语义门禁，并必须逐项证明：

- Proposal canonical 与 exact Draft 可由 `learning-proposal/v1` 严格恢复，ProposalID、
  source/content fingerprint、draft digest 和 target ref 均可重算；
- SQL 中的 Tenant、kind、fingerprint、target、Run/Manifest/Member/Result 投影与 canonical
  wire 完全一致；
- proposer Run、RunManifest、唯一 Member snapshot、Agent/Profile/Workspace 与成功终态
  Model Attempt/`MODEL_RESULT` 形成同一精确 lineage，PENDING、MODEL_UNKNOWN、FAILED、
  Action 结果或其他 Run 的 Attempt 均不能作为来源；
- 非 `SUBMITTED/0` Proposal 的 `review_run_id`、`reviewer_attempt_id` 与状态/revision 形状
  完全匹配 §23.4；Reviewer 是同 Tenant、同 exact Workspace 且独立 Agent/Profile 的唯一普通
  model-only Run，Review TaskInput 完整绑定 Proposal/Draft；
- `REVIEW_PENDING` 只保留无 Attempt、PENDING 或原 Attempt 已终态待投影的合法窗口；Review
  终态必须由唯一普通 Attempt、Usage、MODEL_RESULT/Verdict 或失败事实派生，revision 3 必须
  引用同一 UNKNOWN Attempt 的可靠 reconciliation evidence；
- verify/restore 不调用模型、Module、Knowledge Provider、网络、Secret resolver 或任何外部效果。

`learning_cycle_schedules` 与 `learning_cycle_tasks` 同样不增加 bundle 文件族、artifact 或
manifest 派生计数。统一只读 semantic gate 还必须逐项证明：

- Schedule canonical/digest、Tenant/Schedule identity、默认周期、enabled/revision、
  `last_scheduled_for` 与 `next_due_at` 可严格重建；水位必须位于 exact 周期网格且单调；
- TaskID 可由 exact Schedule digest、`scheduled_for` 与 request digest 确定性重算；
  `(tenant_id,schedule_id,scheduled_for)` 唯一，Schedule 外键、request canonical/digest/size、
  state/revision 与 nullable Run/Attempt/Result/Proposal 形状全部一致；
- `PENDING/0`、`RUN_ADMITTED/1`、全部 revision 2 终态和同一 UNKNOWN Attempt 对账后的 revision 3
  终态均可保真恢复；孤立 Run/Attempt/Result/Proposal、第二 open Task、非法 occupied ref、
  off-grid 水位或协调篡改均 fail-closed；
- Restore/Open 即使 Schedule 已到期也不执行 Tick；下一次显式 Tick 仍只合并创建一个最新窗口。
  `UNKNOWN/2` 只能使用原 Attempt 的可靠 reconciliation evidence 收口，禁止语义重放；
- `learning-cycle-report/v1` 是临时只读投影，不持久化、不进入 backup；Store-only reconcile 和
  report 在 verify/restore 前后均不得加载 Provider、Loop、Gateway、Secret 或网络。

结构 FK、database digest 或 source/content UNIQUE 约束不能替代上述语义闭包。该备份支持只
证明候选与已有有界 Review 事实可恢复，不把 W4-L2 标记为 accepted，也不授予发布、安装、
激活、Authority 或周期执行能力。

当前 W1 开发切片中，同一 SQLite snapshot 必须自然包含 `conversations`、全部 turn Run 投影和
predecessor closure。`VerifyBundle`/restore 后语义校验必须证明每个 head 指向同一
Conversation 的 `turn_index=revision` Run，链从 1 连续、固定五元组不漂移，且每个后继的
Manifest/ref（包括与 Conversation owner 相同、仅用于 evidence 的 PrincipalID）、TASK_INPUT
与最终 ASSISTANT History 完整闭合；FAILED/UNKNOWN head 可以被
保真备份，但恢复后仍不能继续该 Conversation。备份、验证、恢复和上述闭包校验不得调用
模型、可选模块或 Secret resolver；SecretRef 只作为不透明标识复制/核对，Secret 值不得
进入 database、artifact bundle 或 manifest。

当前证据中，`TestConversationBundleRoundTripPreservesLinearClosureWithoutExecution`
以两轮已成功 Conversation 创建完整 bundle，执行 verify/restore，证明模型 Adapter
调用数在备份过程中不变，再从恢复后 exact head 续转第三轮。
`TestRunConversationFiftyTurnsAcrossReentryBackupRestore` 进一步用确定性本地 Provider
闭合 50 个 Run/Attempt/Usage，在 turn 25 执行 backup→verify→restore 后续聊，并对
turn 25/50 做 exact retry、最终再生成 bundle；Action/Memory/Channel 的可选访问为零。
该 50 轮只是本地重入与备份长链证据，不等于真实 DeepSeek 50 轮效果/费用验收。
`TestRunConversationFiftyTurnsAcrossNormalProcessRestart` 则由两个正常退出的独立 OS 进程
分别提交 1..25 与 26..50 轮，第二个进程只从持久 head/revision 恢复；最终再次闭合 50 个
Run/Attempt/Usage，并证明 Action/Memory/Channel 权威表仍为零。两类测试证明不同恢复边界，
均不能代替真实 DeepSeek 2/50 轮。

ModelProfile 不改变备份格式：其 ref 已由成员快照覆盖，canonical bytes 位于同一
`content_records` 表。完整数据库备份与恢复必须逐字节保留二者并能重新闭合引用。

Action 同样不增加 bundle 文件族：SQLite snapshot 自然包含冻结 Definitions、
ACTION_PROPOSAL/RESULT、DispatchAttempt 与互斥 Frame 指针，artifact 继续由现有
Installation 闭包覆盖。`VerifyBundle` 必须逐项验证 source model、BindingIndex/
Definition/Effect、Proposal/Input/Payload、result/receipt/evidence、模型二 source 链与
model/action 对应派生计数；结构 FK 通过不能替代语义验证。外部世界的实际副作用不属于数据库备份，
manifest 不得伪造“效果已回滚/已完成”；PENDING/UNKNOWN 恢复后仍只对账原 Attempt。

Channel 也不增加 bundle 文件族。`VerifyBundle` 与
`VerifyCurrentStoreSemanticClosure` 必须验证：每个 scope 的 Cursor revision 从 0 连续且
before/after canonical bytes 衔接；IngressKey/ProviderEvent/Envelope identity 可重算；
ACCEPTED receipt 与唯一 Run/Workspace/Principal/ACL/Member Channel Binding 双向闭合；
REJECTED 不引用 Run；每个 Channel-origin Run 至多且在最终模型成功后恰有一个
CHANNEL_SEND，并与 source ModelAttempt、Endpoint、Ingress、Binding、Proposal、终态
Frame/Event/History/Result/receipt/evidence 双向闭合。Provider receipt 与 reconciliation
evidence 必须是有界 canonical JSON object；删除终态 send、添加孤立 send、篡改 Frame/
Event/History、Cursor 或闭包字段都必须失败。结构 FK 与 manifest 计数均不能替代这些
语义检查。

Parent/Child + Composite 同样不增加 bundle 文件族或表。当前 verifier 要求 SQLite snapshot
逐字节包含 `runs` 四列、完整 `2N+3` Decision family 的 snapshots/manifests/frames/events、
初始/实际 repair/Reviewer/Root 的 MODEL_RESULT/`model_usage`、dormant/skipped repair 事实、
Workspace Transfer payload/envelope、Parent merge compilation/result 及 `RUN_CANCELLATION` content。
`VerifyBundle` 与 `VerifyCurrentStoreSemanticClosure` 必须双向证明：每个 Parent manifest
有且仅有 2..8 个规范有序 Child；每个 Child 的 run projection 与 manifest 反向引用同一
Parent digest/slot；same Tenant/TASK_INPUT、one Member/Workspace per Run、Root/Reviewer Workspace、
双边历史 grant 与 REQUEST/RESULT 方向、depth 1、weight sum、dispatch cap、Decision repair lineage、
终态投影和 Child-result evidence 全部闭合；无 manifest
引用的 `parent_run_id` 行、缺 Child、孤立 result 或超配额 merge 都必须失败。
`cancel_request_ref` 非空时必须恢复 exact canonical request，并证明普通 scope 只绑定一个
Run、family scope 在 Parent 与全部 frozen Child 上为同一 ref；PENDING/UNKNOWN 状态不得被
伪装成 cancellation 已证明未执行。结构 FK、表数与文件 digest 不能替代这些语义检查。

### 12.3 备份验证

`currentbackup.VerifyBundle` 是独立只读 verifier；`CreateBundle` 也必须先用它验证
staging，再允许正式发布。验证至少覆盖：

```text
PRAGMA integrity_check
PRAGMA foreign_key_check
全部冻结 identity 字段校验
schema fingerprint 校验
database SHA-256 校验
Canonical manifest、自摘要和未知字段校验
所有 artifact digest/size 与 Installation 完整闭包
Control/Catalog current ref/digest 可达性
bundle 精确树（没有缺失项或额外项）
```

verifier 拒绝绝对路径、`..`、大小写碰撞、符号链接/reparse point、hard link、
device/special file，并对 manifest、单 artifact 文件、artifact 总量和 database 使用
显式上限。树在验证前后各扫描一次，期间变化即失败。

失败的临时包不得覆盖已有备份。staging 必须先完成文件与目录 fsync；随后释放
source owner fence，最后 no-replace 原子发布并 fsync 目标父目录。fence 释放失败时
不得出现“API 返回失败但正式 bundle 已可见”的模糊状态。

## 13. 恢复

恢复入口是
`currentbackup.RestoreBundle(ctx, bundle, destinationDB, destinationArtifactRoot)`，
只能写入两个显式指定、互相分离且都不存在的目标：

1. 先调用独立 `VerifyBundle` 验证 manifest、数据库和全部 artifact。
2. 在各目标的同一父目录 staging，复制精确数据库 bytes 与 artifact tree。每个 artifact
   目录固定为 `0700`、普通文件固定为 `0600`；仅当该 digest 被数据库 Installation 引用时，
   才把已验证 `LOCAL_PROCESS + mcp-stdio/2025-11-25` Manifest 的 canonical descriptor
   指定的唯一 executable 重建为 `0700`。ingress-only digest 的所有普通文件保持 `0600`；
   不得传播 setuid、setgid、sticky、group/world 位。
3. 重新校验 database digest/size、identity/fingerprint、current refs、Attempt 计数、
   全部 Installation-artifact 闭包和 artifact 根精确性；chmod 后逐文件 fsync，并以 exact
   Module/Artifact/Adapter identity 完成 no-launch Host 构造校验，然后再同步目录。
4. 先 no-replace 发布 artifact root 并 fsync 其父目录；一旦该 root 可见，立即取得与
   Ingress/Apply 相同的跨进程 writer lease，并在 lease 内重新证明 exact artifact set、
   完整物理闭包与硬预算。
5. 只有上述 lease 内复验通过，才最后 no-replace 发布 database 并 fsync 其父目录；
   database 是 restore commit marker，只有它存在才表示本次 restore 已提交。
6. artifact root 可见后的失败不得猜测它仍未被其他 Store 引用并递归删除；只保留经过
   no-replace/闭包验证、无 Store authority 的有界 orphan。任一初始目标已存在仍拒绝覆盖，
   Operator 必须核验并移走未带 database commit marker 的 orphan 后再以全新目标重试。
7. 在同一 writer lease 内，对已发布路径重新计算 database digest，并再次校验 Store、
   current refs、Attempt 计数、artifact digest/size、闭包和精确根；通过并释放 lease 后
   才允许普通 owner 打开。
8. 启动 Store-only safety transaction 扫描 Model 与共享 Dispatch 两类 ledger 中的
   PENDING/UNKNOWN，并把原 PENDING CAS 为对应 UNKNOWN；不得加载冻结模块内容，也不得
   自动重放模型、Describe、Prepare、MCP SDK/process、Action executor 或 Channel sender。

恢复命令不得调用会迁移、seed、repair 或启动 worker 的 `Open`。这里的恢复是
backup restore：必须保留原 `store_instance_id`、运行元数据、数据库字节和所有
权威行。与 §7.1 的确定性 init 不同，当前 backup restore 不接纳已存在的孤儿
artifact 目标；跨进程崩溃若留下只有 artifact、没有 database 的目录，数据库
commit marker 明确表示恢复未完成，Operator 必须先核验并移走该目录，再以两个
全新目标重试。恢复演练至少覆盖：

- Control/Catalog、Manifest/Snapshot/Frame 的 digest 一致；
- History sequence 与 RunEvent sequence 连续；
- PENDING 在恢复后仍为原 Attempt；
- MODEL_UNKNOWN 不生成新 `LogicalOperationKey`；
- Action PENDING/UNKNOWN 保留原 Proposal/Binding/Effect/外部引用且不执行；
- Channel Cursor/Event/Ingress canonical bytes 与全部派生计数不变；CHANNEL_SEND
  PENDING/UNKNOWN 保留原 Endpoint/Ingress/Proposal/result/receipt/evidence，恢复、验证与
  reopen 均不解析 Secret、不构造 Adapter、不发送网络请求；
- Composite Parent/Child run projections、manifest digest 图、slot/weight/assignment、共同
  TASK_INPUT、Child terminal results、merge compilation evidence 与 cancel refs 逐字节不变；
  restore/reopen 不 fan-out、不 merge、不重新 Admission、不补造 Child，也不对 UNKNOWN 退款；
- Usage 的 NULL/0 语义不改变；
- artifact 缺失或被篡改时 fail-closed；
- 恢复到已有路径时 no-overwrite。

### 13.1 Operator CLI

当前 CLI 与上述 API 一一对应；未指定 `--artifact-root` 时默认使用
`<db>.artifacts`：

```text
freeagent init \
  --db <new-current.sqlite> \
  --seed <seed.json> \
  [--artifact-root <new-or-exact-init-orphan-root>]

freeagent backup \
  --db <closed-current.sqlite> \
  [--artifact-root <artifact-root>] \
  --out <new-bundle-directory>

freeagent backup-verify --bundle <existing-bundle-directory>

freeagent restore \
  --bundle <existing-bundle-directory> \
  --db <new-current.sqlite> \
  [--artifact-root <new-artifact-root>]
```

`init` 输出数据库、artifact root、seed identity/revision、artifact locks 和默认
Assembly；`backup` 与 `backup-verify` 输出 bundle 绝对路径及完整 manifest；
`restore` 输出新数据库、artifact root 和保留下来的 `store_instance_id`。这些命令
均是显式 Operator 操作，不得被 `chat`、`serve` 或启动恢复流程隐式调用。

## 14. 发布前开发库重建

FAC1/S1 至 W6-5 的 Draft 变化曾采用 Scheme A，不创建 `0012` 或 runtime migration；这些段落只描述
历史重建纪律。当前 FAC2 已冻结 `0001_current.sql`，并由 W6.6 引入真实、连续的
`0002_server_owned_review.sql`；后续 schema 变化不得修改 `0001`，必须按显式 offline migration 流程追加：

首个 MCP 切片已采用 Scheme A（方案 A）：只扩展
`module_activations.execution_class CHECK` 以接受 `LOCAL_PROCESS`，保持 18 表及全部既有
列/事实归属不变，并执行本节显式开发库重建。当前 fingerprint 与 migration SHA 见
§3.3；没有运行时 migration、兼容 decoder 或双写。MCP Activation 仍要求 Operator
精确 grant，默认不开启。

S2 Channel 切片继续采用同一首次发布前 Scheme A：开发资产没有正式部署 writer 或待
对账外部效果，因此直接更新同一 `0001_current.sql`，新增唯一
`channel_ingress_receipts` 并泛化既有 `dispatch_attempts`，形成当时的 S2 19 表基线；旧开发
资产只读隔离，不通过运行时 migration、兼容 decoder、双写或第二 Store 接入。

Parent/Child + Composite 第一切片继续采用该 Scheme A：只修改同一个
`0001_current.sql`，给 `runs` 加四列及必要 index/constraint，并把 `RUN_CANCELLATION` 加入
既有 ContentKind allowlist；ordinary table 仍为 19。§3.3 已记录重算后的
fingerprint/migration SHA/size；完整开发树门禁而非单个哈希构成当前验收依据。

S3-A 随后在同一 Scheme A 中加入当时的第 20 张
`workspace_scheduler_state`，S3-B 没有增表；这两组 20 表 fingerprint/hash
现只作历史验收证据保留。W1 Conversation 第一开发切片再次按 Scheme A
替换唯一 `0001_current.sql`，只加入 `conversations` 与 `runs` 三个 nullable turn
投影，形成当时的 21 表基线。W1 的 21 表 fingerprint/hash/size 只作历史验收证据保留。

W4-L1A Knowledge Proposal Store 继续采用同一 Scheme A：再次替换唯一
`0001_current.sql`，只加入第 22 张 `learning_proposals`。它没有 runtime migration、兼容
decoder、双写或第二 Store；旧 19/20/21 表开发资产只读隔离，不作为 writer 接回当前
Runtime。W4-L1A/L1B/L2/L3 的 22 表 identity 现只作对应历史证据。

W4-L4 继续采用同一首次发布前 Scheme A：在唯一 `0001_current.sql` 中新增第 23、24 张
`learning_cycle_schedules` 与 `learning_cycle_tasks`，不建立 `0002`、runtime migration、兼容
decoder、双写或第二 Store。W4-L3 的 22 表 identity 只保留为历史证据；当前 43 表 identity
只以 §3.3 为准。

W5-X1/F1 当时继续保持 24 表，只扩展既有 ContentKind 约束；对应旧 identity 由 §24/§25 作为
历史证据保留。W2-R1 随后仍按 Scheme A 只把 `REMOTE` 加入既有
`module_activations.execution_class` CHECK，不增表、不建立 `0002` 或 runtime migration；当前
identity 只以 §3.3 与 §26.1 为准。REMOTE Activation 仍必须由 Core 从 exact handler、Operator
grant、Config/Authority 与 current Catalog 重算，不能由 seed 或 Manifest 自授权；seed 可以保存
SecretRef identity，但禁止 Secret value/bytes。

1. 停止服务、worker、对账进程及全部 writer。
2. 确认没有未说明的 `PENDING` 或 `MODEL_UNKNOWN`；需要保留者先完成对账或保留
   完整离线备份。
3. 创建带 SHA-256 的源码、旧开发数据库和 artifact 离线归档。
4. 导出确定性 `seed.json` 和配套 `seed-artifacts/`。seed 必须包含：
   - 稳定的 Tenant、Workspace、Agent、Profile ID、version 和 revision；
   - ControlSnapshotID/revision、CatalogGenerationID/generation 和唯一 current
     pointer revision；
   - Control 与 Catalog 期望配置；
   - 精确模块锁 `(ModuleID, Version, ArtifactDigest)`；
   - artifact 的规范相对路径、digest 和字节数；
   - 稳定的 InstanceID、ActivationRevision；
   - 期望 `ExecutionClass` 与 `AdapterIdentity`，仅作为 Core 重算后的校验断言；
   - Operator 控制的权限、Effect 和配置策略输入及 SecretRef，不含 Secret
     明文；seed 内的 policy alias 只是导入请求，不是最终 `PolicyRef`。

   seed 不含 Run、History、Attempt、Usage，也不能携带由 Module Manifest 自授的
   Trust、Core 控制权或最终 AuthorityCeiling。
5. 显式删除选定的开发数据库；启动程序不得代替操作者删除。
6. 修改当前 `0001_current.sql`，重新生成并固定 schema fingerprint。
7. 以全新数据库目标执行 `freeagent init --db ... --seed ... --artifact-root ...`；
   该命令内部在 staging 路径调用 `InitFreshCurrentStore`，不得由普通启动代替。
8. init 必须依次验证 seed artifacts、调用 Install、由 Core 根据当前本地策略重新
   计算 Activation，再调用 Publish。计算结果与 seed 中 ExecutionClass、
   AdapterIdentity 或 ArtifactDigest 断言不一致时 fail-closed；不得直接写表。
   Core 必须用 seed 的 operator policy inputs 和本地上限生成 canonical policy，
   再发布 `{id, version, digest}` typed `PolicyRef`；alias 本身不得进入运行快照。
   导入完成后关闭 Store，经 `PrepareClosedCurrentStoreForPublication` 收口 WAL，按
   artifact-first/database-final 发布，数据库作为 commit marker。
9. 运行 identity、FK、fingerprint、备份/恢复和 Pure Chat 全链路验收。

Seed rebuild 不是 backup restore。它必须生成新的 `store_instance_id` 和运行
时间，不比较数据库 SHA-256、运行元数据或本地物理行 ID。重建演练只比较：

- 稳定业务 ID/version/revision；
- 精确模块锁与 artifact digest；
- 规范化 Control/Catalog bytes 和 digest；
- 使用固定 AdmissionKey、AdmissionIntentDigest、RunID、TaskInput 与 deadline
  时生成的 Snapshot/Manifest bytes 和 digest。

S2 在首次公开发布前加入真实能力时，可以继续修改 Draft 的
`0001_current.sql`，但每次仍执行上述完整重建。Action/Channel 启用后还必须：

- ACTION/CHANNEL_SEND 外部效果 PENDING/UNKNOWN 为零、已确认未执行或已完成人工对账；
- Channel 保持断开；
- 完整备份必须包含 Channel Cursor/Event、Ingress 和共享 Dispatch ledger；
- 重建后重新导入或人工选择 Cursor，完成前不得重新连接 Channel。

首次公开发布冻结 Schema v1。只有发布后才允许从 `0002` 开始正常向前 migration。
任何阶段都不存在 `0012`、旧 identity upgrade、双写或运行时冷回退。

## 15. Channel Store 合同

首个 Channel 工作包把 Draft 扩为 **19 张 ordinary table**：只新增
`channel_ingress_receipts`。不得新增 `channel_outbox`、cursor head、Channel job、Channel
Store 或第二外部效果账本；既有 `dispatch_attempts` 泛化后同时承载 Action 与
`CHANNEL_SEND`，Outbox 只是该表的状态投影。

本节状态为 `S2_CHANNEL_ACCEPTED_DEVELOPMENT_SLICE`。它证明默认关闭的单一 loopback
Channel 纵链复用现有 Assembly/Current Store/Universal Loop/Gateway/backup；不声明任意
第三方 Channel、远程多端点调度或生产发布就绪。

### 15.1 `channel_ingress_receipts`

一行同时是一个 Cursor revision 和一个入站去重事实。最小字段必须闭合：Tenant、
Workspace、Endpoint、CursorScope、CursorRevision、CursorBeforeRef、CursorAfterRef、
EndpointBindingDigest、disposition、reason、created_at；revision 0 的 `CURSOR_SEED` 不带
event/admission/run，其余 `ACCEPTED|REJECTED` 带 IngressKey、ProviderEventIDDigest 与
EnvelopeDigest，并由 Content Store 保留有界 canonical Envelope，以支持精确去重与审计，
不是 hash-only shortcut。ACCEPTED 还必须带 Principal、ACL epoch、AdmissionKey 和 RunID；
REJECTED 仅在当前 Workspace 能证明不存在 active exact identity 时写入，且不得引用 Run。

最小唯一性为：

```text
PRIMARY KEY (tenant_id, endpoint_id, cursor_scope_key, cursor_revision)
UNIQUE (tenant_id, endpoint_id, ingress_key)
UNIQUE (tenant_id, endpoint_id, provider_event_id_digest)
```

revision 0 只允许 Endpoint disabled 时由显式 init/import API 写入。后续写入在同一
`BEGIN IMMEDIATE` 中读取最大 revision，验证 `expected_revision + CursorBeforeRef`，并只
插入 `revision+1`；当前 Cursor 由最大 revision 的 CursorAfterRef 派生，不维护可变 head。
exact duplicate 先按 IngressKey 返回原行，不追加 revision；同 key 不同 EnvelopeDigest、
stale cursor、断链或 fork 全部 fail-closed。

ACCEPTED exact duplicate 的历史结果与当前权限分离：仍可能推进原 Run 的状态必须重新通过
当前 Endpoint/Identity/ACL/Binding deny-only 门禁；`TERMINATED` 与
`WAITING_RECONCILIATION` 只返回原结果，绝不重发、重编译或创建替代 Run/Attempt。

ACCEPTED 的 ingress receipt、现有 Admission closure、Run、MemberSnapshot、Manifest、
Frame0 与 Event0 必须在同一个事务提交。调用方不得先推进 Cursor 再创建 Run，或先创建
Channel Run 再补 receipt。该事务复用既有 Admission 写入函数，不创建第二 Run ID 体系、
Store 或调度器。

### 15.2 泛化后的唯一 `dispatch_attempts`

`dispatch_attempts.dispatch_kind` 只允许 `ACTION|CHANNEL_SEND`。共同字段继续保存 Attempt、
logical operation、Run/Member/Frame、source final ModelAttempt、MemberSnapshotDigest、
BindingIndex/Binding、Effect、deadline、budget state、四态终态、external operation、
result/receipt/evidence、revision 与时间。ActionID/ProviderActionID/DefinitionDigest/
ACTION_PROPOSAL 与 ChannelEndpoint/IngressKey/CHANNEL_SEND_PROPOSAL 按 kind 互斥，SQL
CHECK 必须拒绝混合或缺失闭包。

最终成功模型终态与 `CHANNEL_SEND_PROPOSAL`、CHANNEL_SEND/PENDING、
`loop_frames.pending_dispatch_attempt_id`、`CHANNEL_PENDING` continuation、History、Usage
和 Event 在一个事务提交；事务提交后才返回共享原子一次性 Gateway permit。Channel
SUCCEEDED/FAILED/UNKNOWN 只 CAS 原 Attempt、Frame、Run 和 receipt delivery projection；
UNKNOWN 不回 PENDING，不创建替代 Attempt，也不自动重发。
`RECONCILIATION_EVIDENCE` 只能由原 UNKNOWN 的 reconciliation API 写入；普通 Gateway
outcome 即使携带 canonical evidence 也必须在事务前拒绝。

启动 Store-only scan 必须扫描同一 dispatch 表中两种 kind 的 PENDING。ACTION 继续闭合
Action Frame；CHANNEL_SEND 把原 Attempt/CHANNEL_PENDING Frame 原位收口为 UNKNOWN/
WAITING_RECONCILIATION。该扫描只读 ledger、Frame、Run 和必要 ContentDigest，不读取
Member canonical body、Endpoint Config/Authority、Secret、artifact、Registry 或 Adapter。

### 15.3 备份、恢复与派生计数

完整 SQLite snapshot 自然包含全部 Channel 权威行，但 verifier 仍必须做语义验证：Cursor
revision 从 0 连续、before/after 引用逐字节衔接、Ingress/Event 唯一；ACCEPTED 精确闭合
Admission/Run/Workspace/Principal/MemberSnapshot Channel Binding；REJECTED 不引用 Run；
CHANNEL_SEND 精确闭合最终 ModelAttempt、Ingress、Endpoint、Binding、Proposal、Frame 和
result/receipt/evidence。终态投影还必须满足：SUCCEEDED/FAILED 的 Run 与 Frame 均为
TERMINATED 且 continuation 精确指向原 CHANNEL_SEND Attempt；UNKNOWN 为
WAITING_RECONCILIATION；PENDING 为 CHANNEL_PENDING。最终 Assistant History 必须唯一引用
source final ModelAttempt，不能由 send result 冒充回答。lease-only CAS 可以推进当前
Frame revision 而不追加 Event，因此 verifier 校验 Event 的连续 from/to revision 和
`to_revision <= current frame revision`，不得错误要求它永远等于经过 lease churn 的当前值。

Backup manifest 的派生计数增加 `channel_ingress_receipts`、`channel_cursor_scopes`、
`channel_send_pending` 与 `channel_send_unknown`。这些计数只描述本地权威事实，不声称外部
消息已撤回、回滚或重新投递。Verify/Restore 不加载 Channel Adapter、不解析 Secret、
不发送网络请求；Restore 后 Channel 保持 disabled，Operator 对账 UNKNOWN 后才能显式
连接。

Provider receipt 与 reconciliation evidence 必须分别引用正确 ContentKind，并且是最多
64 KiB、最大深度 32、最多 64K nodes 的 canonical JSON object。UNKNOWN→SUCCEEDED/FAILED
时已知 receipt、external operation 和 evidence 只能保留或补充，不能擦除；FAILED 可以
保留真实收到的 receipt/evidence，不能把“失败终态”错误解释成“从未产生外部效果”。

### 15.4 显式启用、默认零访问与关停

生产 Channel composition 默认不构造 Adapter、不读取 Channel Cursor/Ingress/Config、
不解析 Secret，也不发网。显式启用时必须选择唯一 Tenant/Workspace/Endpoint，要求
Operator 已导入匹配 EndpointBindingDigest 的 Cursor seed，并在构造 Adapter 前完成：

1. 全局 Store-only PENDING→UNKNOWN 启动恢复；
2. `VerifyCurrentStoreSemanticClosure` 的只读完整语义闭包校验；
3. 选中 Endpoint 当前启用、Identity/ACL/Binding deny-only 门禁；
4. 该 Endpoint 不存在未对账 CHANNEL_SEND UNKNOWN。

缺任一条件均 fail-closed。ACCEPTED duplicate 若原 Run 仍可推进，必须重新通过当前
Endpoint/Identity/ACL/Binding deny-only 门禁；已经 TERMINATED 或
WAITING_RECONCILIATION 的 duplicate 只返回原持久化结果，不做语义重放、不创建替代 Run/
Attempt，也不再次发送。

优雅关停遵循 §10：关闭 HTTP Admission、排空已接纳 handler 后，使用有界且与外层取消
分离的上下文运行同一个 Store-only recovery，再关闭 Store。关停恢复只允许把原
CHANNEL_SEND PENDING 收口为 UNKNOWN/WAITING_RECONCILIATION；不得加载 Endpoint 正文、
Secret、artifact、Registry 或 Adapter。

### 15.5 W2-E3 Workspace Channel Apply 持久化验收

`W2_E3_WORKSPACE_CHANNEL_APPLY_ACCEPTED_DEVELOPMENT_SLICE` 复用现有表与 §7.3.1 的唯一
publication，把 first-party loopback Channel 的 Binding target 固定为
`WORKSPACE_CHANNEL_ENDPOINT`。`ENABLED` 以 `PublishControlCatalogWithChannelCursorSeed` 在同一
`BEGIN IMMEDIATE` 内提交 Control、Catalog 和 revision-0 `CURSOR_SEED`；Dry-run 零写、零 Secret
解析、零网络。exact retry 始终验证原始 revision-0 seed，即使当前 Cursor 已继续推进。Disable
仅删除 current Endpoint；Cursor、receipt、Run、Attempt、历史 Control/Catalog、Module History 与
evidence 继续保留，共享 Instance 只在最后引用消失时退出 current Catalog。

集中验收 `TestW2E3ChannelMultiWorkspaceIsolationBackupV1` 以同一 Agent/Profile、同一共享 Instance
绑定双 Workspace/Endpoint，证明各自 Cursor、Run、receipt、终态和 Module History 不串流；成功与
Channel `UNKNOWN` 的 exact duplicate 都不二次发送，一个 Endpoint 的未对账 `UNKNOWN` 不阻塞
另一个。Bundle 保存 5 条 ingress receipt、2 个 Cursor scope、0 个 Channel PENDING 与 1 个 Channel
`UNKNOWN`；Restore 后 A Cursor 从 revision 2 推进到 3，B 保持 revision 1，历史 Binding 投影逐字节
一致。该证据只覆盖 Channel delivery `UNKNOWN`，未单独构造 `MODEL_UNKNOWN`，也不声明公网或任意
第三方 Channel、任意第三方进程内代码、Beta、生产或 `RELEASE_READY`。

### 15.6 W2-E4 DeepSeek Model replacement publication 闭包

`W2_E4_DEEPSEEK_MODEL_REPLACEMENT_ACCEPTED_DEVELOPMENT_SLICE / W2_E5_NEXT` 复用现有
Content、Control/Catalog publication、Run snapshot 与 Backup 关系，不新增
Store Schema/migration、表、pointer、Runtime 或账本。第七个 exact handler 只能把目标 Profile 中同一
DeepSeek Instance 的 Model Binding 在 Flash/Pro 之间替换；`DISABLED` 必须在 Store 写入前
拒绝。

Apply 把 Config、Authority 和 optional ModelProfile 作为完整期望状态，在同一
Control/Catalog CAS publication 中发布。省略 `model_profile` 即清除旧 ref；提供新
Profile 时，publication 必须证明它与新 Binding 的 provider/model/build/config/artifact/
adapter 精确匹配，并且不扩张 ContextPolicy。旧 Control/Catalog 和已冻结 Run 不回写；
只有 publication 后的新 Run 冻结新闭包。

Model Authority 只允许 strict `model-authority-ceiling/v1`；历史
bootstrap 只保留 exact byte-equal deny-all Authority 兼容身份，其他未知 Schema/字节全部
fail-closed。新 Authority 的 SecretRef 必须与本地 transient grant 精确一致；grant 不持久化。

`VerifyPublishedControlCatalogClosureV1` 在 Backup verify/restore 语义门禁中重放当前
publication，因此即使当前 Model Binding 尚未产生 Run，也必须重验 Config、
Authority 和 optional ModelProfile 的完整闭包。原 `MODEL_UNKNOWN` 只保留
原 Attempt/permit/Usage 事实；replacement、rollback 和 exact retry 不得新建 Attempt、调用新
Provider、fallback 或语义重放。

关键 Store 负例是
`TestPublishControlCatalogRejectsUnknownModelAuthoritySchemaWithoutWrites`；Profile 清除与
Backup/Restore 由
`TestModuleApplyDeepSeekModelProfileCanBeClearedByExplicitReplacement` 和
`TestModuleApplyDeepSeekModelProfileBackupRestoreExactClosure` 覆盖。完整
FAC2 基线删除金额体系后，原 PriceSnapshot publication 闭包负例与缺价 Backup gate 一并退役，
不再有等价断言需要保留。完整
`internal/currentstore` 与 `internal/currentbackup` 包测试分别以 84.252s 和 89.202s 通过。

### 15.7 W2-E5-A governed Knowledge publication 闭包

E5-A 验收时状态为
`W2_E5_A_REQUIRES_PERMISSION_GRANT_ACCEPTED_DEVELOPMENT_SLICE / W2_E5_B_NEXT`。E5-A 复用现有
Installation、Activation、Content、Control/Catalog pointer 和 Backup bundle，不新增 schema、
migration、表、pointer、grant row、Runtime 或账本。

Current Store publication 对每个已绑定 Profile node 沿
`Binding → Catalog → exact Activation → Installation → canonical Manifest` 重建身份。Catalog 与
stored Activation 的 tenant/instance/revision/execution/adapter 必须一致；Activation 的
InstallationID 及 Catalog 的 module/version/artifact digest 必须闭合唯一 Installation；canonical
Manifest 的 identity、runtime 与 Provides 必须闭合相同 Catalog/Binding。调用方不能用仅存在于
Control/Catalog 的表面事实绕过 immutable module facts。

共享 `moduleapi` classifier 只允许 Knowledge Manifest 的两个 exact 形状：legacy 零 Requires/零
permission，或 governed 的唯一 `model.generate/v2` Require 与唯一 `knowledge.read` 请求。Store
publication、exact retry、公开 `VerifyPublishedControlCatalogClosureV1` 与 Backup semantic gate
均使用该 classifier；permission-only、require-only、重复、额外值和其他部分形状都拒绝，避免
Apply/Runtime/Backup 对同一 Manifest 得出不同结论。

governed Require 必须在同一 Profile 中命中唯一 exact Model Binding，并闭合其完整 module identity
链；跨 Profile、缺失、歧义或 cycle 均拒绝。`knowledge.read` 有效 grant 由 Store 从 exact
Knowledge Binding、Config/Authority 同一 source、Control Tenant、已存在的 Workspace/Agent scope
和读取限制派生；Manifest 请求本身不授予权限，也不持久化第二份 grant 状态。Workspace Channel
Endpoint 仍禁止 Requires 与 requested permissions，不能借 E5-A 绕开 Profile 范围。

Apply 的 Store view 和 Dry-run 的 immutable observer 在 `ALREADY_APPLIED`/`NO_CHANGE` 或任何候选
写入前，先用同一 public verifier 重放完整 current publication closure。损坏的旧
Installation/Manifest、Activation、Catalog、Binding、Requires 或 grant closure 因而不会通过
fast-path。Backup create/verify/restore/reopen 继续重放该闭包；Restore 不执行模块、Provider、模型
或网络。Disable 只改变 future current Binding，历史 Run/Attempt、retrieval、Installation、
Activation、Content 和 canonical bytes 保留。

UNKNOWN 语义不变：Store 不因 dependency/grant 校验创建替代 Attempt；原 Model/Action/Channel
UNKNOWN 只允许用原 Attempt 的可靠 evidence 对账，禁止语义重放或换 Provider。E5-A 没有真实 API
调用。`go test ./...` 的 `30` 个仓内 Go package 在 `221.9s` 内通过；另有 `1` 个 external
compatibility package 由 `sdk/moduleapi` 嵌套测试编译验证；vet、mod verify、gofmt、Docs（`38`）、
Capability Matrix（`49` 项/`0 stable`）、License（`35` 个 Go dependency/`57` 个 distributed
asset）、Branding 与 PublicTree 门禁通过。

该状态不等于完整 E5，不开放通用 multi-Port provider/consumer、多个 ProviderBinding 合并、
REMOTE/WASM、任意第三方或不可信包内代码，也不新增第二 Store/Loop/Gateway。下一唯一状态是
`W2_E5_B_NEXT`。

### 15.8 W2-E5-B Document Insight 双 Port Store 闭包

当前状态为
`W2_E5_B_DOCUMENT_INSIGHT_DUAL_PORT_ACCEPTED_DEVELOPMENT_SLICE`。E5-B 没有新增 schema、migration、
表、pointer、grant row、Runtime、Store、Loop、Gateway 或效果账本；它只复用既有 immutable
Installation/Activation、Catalog generation、Control snapshot、ContentRecord、Run/Attempt 与完整
Backup bundle。

本次 accepted E2E 中，`freeagent.builtin.document-insight@1.0.0` 的 exact ArtifactDigest
`838ff9ddd45186f0cdb26021d16902b2bfd7581c2dc0d7b72014cf4f48d0f7ea` 由同一 Instance 的两个 Port
复用一个 Installation、一个 Activation revision 和一个 Catalog entry；这里不新增 Tenant 级 singleton
约束，不同 InstanceID 仍按既有 Control/Catalog identity 合同独立装配。Catalog entry 必须公开完整有序
`[action.provider/v1, context.provide/v1]`，并且它的完整 Activation identity 与两个 Profile Binding
冻结出的 Document Insight Provider 完全相同。同一 Instance 的 Context/Action 两个 Binding 不能借
同一包分别创建不同 Installation、Activation 或 Catalog entry，也不能以同 ModuleID 的近似
version/digest 互换。

Current Store publication 仍沿
`Binding → Catalog → exact Activation → Installation → canonical Manifest` 重建每个 bound node。
对于同一 Profile 中相同 InstanceID 且 manifest canonical bytes 相同的多 Port nodes，Store 必须先按
instance 分组，再对该 instance 的 Manifest Requires 与 requested permissions **恰好闭合一次**。
Document Insight 的 `knowledge.read` 请求必须在该组内找到唯一、同 instance、governed
`context.provide/v1` Binding，并闭合相同 source、Tenant、Workspace/Agent scope 与读取 ceiling；仅有
Action Binding 的部分 assembly 必须拒绝，重复 Binding 也不得把一次 permission request 变成多次授权。
这只是 per-instance publication verification，不持久化第二份 grant 或派生 module graph。

产品 Apply 固定为两个独立、显式 CAS 步骤：

```text
initial current pointer revision 1
  → Context Apply：创建唯一 Installation/Activation，Catalog 加入完整双 Port instance，
    仅绑定 context.provide/v1，CAS 到 pointer revision 2
  → Action Apply：复用同一 Installation/Activation/Catalog instance，
    新增 action.provider/v1 Binding，CAS 到 pointer revision 3
```

Action-first 在 artifact staging、TEMP 或 Store mutation 前以 `TARGET_CONFLICT` 零写拒绝。每一步都必须
提交 expected current pointer revision；stale pointer、并发 publication 或 publication closure 变化只允许
一个 CAS winner，失败方不得留下 content、snapshot、catalog、installation、activation 或 artifact
部分状态。Apply/Dry-run 的 current fast-path 也必须先重放完整 public verifier，不能用
`ALREADY_APPLIED/NO_CHANGE` 绕过损坏的历史闭包。

Disable 顺序同样由 publication closure 而不是 CLI 约定保证。Action Binding 仍存在时先删 Context，
必须以 `PUBLICATION_FAILED` 拒绝且 current pointer、Control/Catalog identity、各 mutation table cardinality
与 artifact tree 不变。正确顺序为：先 Disable Action 并 CAS 到 revision `4`，保留 Context Binding、
Catalog instance 与真实 RAG；再 Disable Context 并 CAS 到 revision `5`，删除最后一个 current Binding 与
Catalog instance，使新 Run 回到 Pure Chat。Immutable Installation、Activation、历史 Run、Action/RAG
evidence 和 canonical bytes 不删除、不回写。

完整 Backup create→verify→restore→reopen 必须精确保留并双向验证：唯一 Installation/Activation、完整
双 Port Catalog entry、Control bindings/current pointer、historical MemberExecutionSnapshot、两个 Model
Attempt、一个 Action DispatchAttempt/Proposal/Result，以及同一 model-1 的 RAG request 与
ContextCompilation。验收要求恢复前后 Action chain 结构相等，RAG request/compilation 与 Member snapshot
逐字节相等；Restore 不加载或执行 Provider。恢复后的首次新 Run 必须重新从恢复的 current closure 冻结
相同完整 `ActivatedModuleRef`，并实际完成 RAG→Gateway Action→model-2；exact retry 不新增 Run 或 Attempt。

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

该 accepted 状态只覆盖上述固定、受信、编译进 Core 的 Document Insight 双 Port 实例。它不代表任意
第三方进程内代码、通用 multi-Port 模块、同一 Port 多个 ProviderBinding、REMOTE/WASM、自动发现、
在线控制面、生产部署、公开 Beta 或 `RELEASE_READY` 已完成。

## 16. S2 延后清单

下列状态是最终 Current Store 的候选组成部分，但除已单独验收的 S3-A 公平 Scheduler
当前计数、S3-B 复用既有 Run/Attempt/Content/Event 的 Reviewer gate、W4-L1A/L1B
Proposal Store、W4-L2 review refs/state/revision、W4-L3 一对一 Version 投影、W4-L4
Schedule/Task、W2-U2 五张 Source/Snapshot observation fact 表、W2-U3 三张不可变
Candidate/Review/Decision fact 表、W6-2 一张 durable receipt 表、W6-3 八张 read-only Overview
observation closure 表，以及 W6-5 两张 inert Artifact/append-only Admission 表外，不进入当前 43 张表：

| 能力 | 何时加入 | 最低门禁 |
|---|---|---|
| RAG 远程/计费检索 | 出现首个远程或计费 Provider 时 | Retrieval Attempt、UNKNOWN、Usage、对账与恢复 |
| MCP 超出首个本地 Tool 切片的能力 | 出现 Secret、REMOTE/HTTP、Resource/Prompt、自动发现或动态订阅消费者时 | 独立权限/Secret/传输合同、相应 Attempt/Usage、UNKNOWN、恢复与撤权 |
| 通用动态任务图与多轮返工 | 超出 W5-D1 已冻结的 depth-1、单 repair round 图时 | 动态 authority、图 revision、更高循环上限、lease/fencing、UNKNOWN 与恢复 |
| Learning 后台调度、自动 Review/Apply 与主动外部检索 | 出现首个后台 Worker、自动治理或主动检索消费者时 | 独立权限/资源/停止边界；复用唯一 Run/Attempt/UNKNOWN，不得把 W4-L4 显式 Tick 或 Store-only reconcile 解释为后台执行授权 |

新增能力必须复用本规格的 identity、Store owner、CAS、Attempt 和备份协议。无状态
模块无需为“看起来完整”而建表。

Parent/Child + Composite 第一实现切片已经验收。它只用
既有 19 表加 `runs` 四列表达 depth-1 family、cancel latch 与 immutable manifest 图；不
预建 queue/scheduler/reviewer/learning/transfer 表。S3-A 随后只加入第 20 张
`workspace_scheduler_state` 当前计数表并复用原 RunLease；没有 scheduler queue/job 或
第二事实源。S3-B Reviewer 随后复用既有 family Run、Model Attempt、MODEL_RESULT、
RunEvent 与 Usage 接入，仍未预建 Reviewer 专表、permit 或 event kind。W5-D1/X1 随后继续
复用同一 family/Attempt/Usage 图与 `content_records`，只新增两个 ContentKind；没有 Transfer
表、队列、第二 Ledger、第二 Runtime/Store/Loop/Gateway。
当前 W1 Conversation 切片在该 S3 20 表历史基线上只新增一张
`conversations`，并复用既有 Run/Manifest/Content/History/Attempt/Usage；没有预建
Session、rolling summary head 或 Conversation 专用 Usage/Cost 表。
W4-L1A/L1B 只新增并复用 `learning_proposals`，保存不可变 Knowledge/静态 Skill 候选、
Tenant+kind 内三轴去重投影和 proposer lineage。已验收的 W4-L2 继续复用同一表，并通过既有
Run/Manifest/Member/Model Attempt/MODEL_RESULT/Usage/UNKNOWN 表族闭合一次有界审核；没有
Learning Review 专表、拒绝集合专表或第二事实源。W4-L3 继续复用 Proposal 行保存一对一无权限
Version；W4-L4 只增加 Schedule/Task 两张窄表，显式 Tick 才执行，reconcile 只收口原事实，report
只读且不持久化。仍没有后台 Worker、queue、自动 Review/Apply 或主动检索。

S2.1 已加入单一 Context Compilation 记录，随后 ModelProfile 只复用既有 CONFIG 与
成员快照引用；S2 RAG 也只把证据嵌入该 compilation。S2 Memory 因真实跨 Run 可变
状态消费者加入一张 `agent_memory_revisions` 表与一个 `MEMORY_SNAPSHOT` ContentKind，
但不增加 Memory read/receipt/ledger、facts 分表、第二 Store 或第二 Runtime。
S2 Action 因真实 Built-in/Gateway 消费者再加入一张 `dispatch_attempts` 表和两个
ContentKind；没有 ActionProposal 表、Action Usage 空表、Gateway job/outbox、第二
Ledger 或第二 Runtime。
S2 Channel 因真实 Cursor/Event 与入站去重消费者加入一张
`channel_ingress_receipts`，并向既有 `dispatch_attempts` 增加 CHANNEL_SEND kind 及四个
Channel ContentKind；没有 Channel Outbox/head/job、第二 Gateway、第二 Store 或第二 Loop。
首个 MCP 本地 Tool Adapter 已作为窄开发切片接入，且不增加表或 ContentKind；它只把一个精确
`LOCAL_PROCESS` executor 接到同一 Action Gateway。MCP `2025-11-25` 与官方 Go SDK
`v1.6.0` 是固定基线；REMOTE/Streamable HTTP/SSE、Secret/SecretRef、Resource、Prompt、
Sampling、Elicitation、Roots、Tasks、常驻 pool、动态订阅与自动发现仍属于后续独立
工作包。本切片不声明完整 MCP 或生产就绪。
Context/RAG 的完整备份自然包含 compilation ContentRecord、Attempt ref 与其请求闭包；
restore 后必须重算
digest，并证明原 PENDING/MODEL_UNKNOWN Attempt 仍引用同一 compilation，不能在
恢复时生成新摘要、Drop 决策或 request。

S2 RAG 的精确 Knowledge Source 属于已安装模块 artifact，因此现有完整 bundle 必须
像其他 retained installation 一样包含它。Restore 后重新打开 Store，必须通过
`LoadRunForLoop` 证明 ArtifactDigest、source revision、Config/Authority、检索证据和
MODEL_REQUEST 逐字节闭合。`VerifyBundle` 的文件级通过不能替代该 Run 闭包测试。

首片只允许本地确定性、只读、无计费、无副作用的检索，所以不增加 Retrieval Attempt
或 Usage 表：Begin 前崩溃可从同一不可变 artifact 重做；Begin 成功后只能恢复原
compilation/request，禁止重检索。未来 REMOTE、MCP 或计费检索必须先增加能表达
PENDING/UNKNOWN/Usage 的真实调用账本，不能复用这个例外。

Memory 完整备份继续使用同一 SQLite snapshot 与 bundle manifest，不新增文件格式。
`VerifyBundle` 和 restore 后的闭包校验必须按 Tenant/Agent 枚举 revision：从 genesis
连续、snapshot kind/canonical/owner/revision/parent/source Attempt 全部闭合；恢复后
每个 snapshot bytes、current maximum revision/ref 与原库一致。已持久化 Attempt 只
验证 compilation 指向的旧 snapshot，不读取当前 head、不重新筛 TTL、不调用 Provider、
不重新生成摘要或计数。结构 FK 通过不能替代上述语义校验。

## 17. 禁止项

实现与评审必须拒绝：

- `0012` 或把旧 migration 继续接到新 Store。
- 新旧 Store 双写、shadow write、evidence write 或运行时冷回退。
- 旧 identity decoder、自动 upgrade、旧 Store 只读运行时依赖。
- 启动时自动删除、重建、迁移、seed 或修改 `application_id`。
- Module、Provider、Observer 直接拿 SQLite handle。
- 在外部调用期间持有数据库事务。
- UNKNOWN 自动重试、换 Provider、换 Binding、生成新 Attempt，或自动创建新 Run
  绕过原逻辑步骤。
- 把 Action executor 暴露为公共 Port、让模型/SDK 直接执行，或在恢复时重新
  Describe/Prepare/Gateway。
- 为 Channel 建第二 Store、Outbox/head/job/ledger，绕过 Gateway 直接发送，在
  startup/shutdown/backup/restore/verify 中读取 Secret 或发网，或对 CHANNEL_SEND UNKNOWN
  自动重发。
- 为 MCP 建第二账本、自动重试或常驻进程池；绕过 Gateway 直接 `tools/call`；在 Pure
  Chat 中读取 MCP artifact/Config/Authority、构造 SDK client、启动进程或执行发现。
- 将 UNKNOWN token/cost 写成 0。
- 用当前 Control/Catalog 重新解释已发布 Run。
- 备份只复制 `.db` 而遗漏 WAL 一致性、artifact 或后续 Channel Cursor。
- 为尚未进入当前纵向工作包的能力预建空表。
- 把 Composite 实现成一个 Run 多 Member、非原子逐个插入 Child、深度超过 1，或让 Loop/
  Scheduler 在运行中发现、创建、替换、重排 Child。
- 把 fair queue、Reviewer、Learning 或跨 Workspace Transfer 追溯写成 S2 第一切片能力，或用
  weight 改写 RAG rank、Skill/Reviewer 顺序、模型路由和权限。它们只能由各自后续权威章节授权。
- 把 Child result 当 SYSTEM/可信指令、截断或跨 slot 借用 50% allocation，或省略
  `context-compilation/v1` 的 exact result evidence。
- 用取消覆盖 PENDING/MODEL_UNKNOWN/UNKNOWN、向上/横向传播 inherited cancel，或把
  UNKNOWN dispatch slot 退款后重放。
- 新建 family balance/usage pool，或以派生聚合替代每 Run 的原 `model_usage`。
- 在迁移、fingerprint、实现、故障恢复与完整 backup/restore 门禁齐备前把
  Parent/Child + Composite 标记为 accepted。

## 18. S1/S2 历史验收基线

既有基线与已验收开发切片必须继续满足 1–36；37–45 对应 Action，46–51 对应窄 MCP，
52–59 对应 S2 Channel。
这里的 MCP 验收只覆盖 Operator 完全信任的本地 stdio Tool 纵链，不声明完整 MCP、
不可信第三方隔离、生产就绪或最终发布门禁已经完成：

1. 该 S2 历史 `0001_current.sql` 创建且仅创建当时的 19 张 ordinary table 及必要
   index/trigger。
2. 全部冻结 identity 字段与生成的 schema fingerprint 在 init/open/backup/restore
   全链路一致；初始化与 publication preparation 后不存在
   `-wal/-shm/-journal`。
3. 新进程对任何非 `FAC1` Store fail-closed，且代码依赖闭包无旧 migration/
   decoder。
4. 普通启动不执行 migration、seed、repair 或自动清库。
5. Install 对同 ID+Version 不同 ArtifactDigest fail-closed；ExecutionClass 只由
   Core 在 Activate 时分配，seed/Manifest 不能自授权。
6. 唯一 `control_current` pointer 在一次 CAS 中同时发布 Control 与 Catalog。
7. Admission 提交后不存在缺 Manifest、Snapshot 或 Frame 的 Run；S1 恰好一个
   Member、一个 Workspace 且没有 ParentRun；相同 AdmissionKey + intent digest
   返回同一 Run，相同 key + 不同 digest fail-closed。
8. 相同编译输入生成字节一致的 Snapshot、Manifest 和 digest。
9. stale revision、stale lease owner 与 stale epoch 均不能推进 Loop；Usage
   对账也必须按 revision CAS。
10. 实际网络调用前可以证明 PENDING 与 Usage 占位行已经提交；已过期 deadline
    可以证明直接 FAILED、无 permit 且模型调用次数为零。
11. 在“调用前、调用后、终态事务前、终态事务中”注入崩溃，均恢复到唯一原
    Attempt。
12. MODEL_UNKNOWN 不自动重放、不切换 Binding、不产生替代 Attempt，也不能由
    client/Scheduler/continuation 自动创建新 Run 或新 logical step 绕过。
13. 成功终态、可选 Memory revision、Usage、History、Frame 和 Event 原子提交。
14. Usage 的 input/cache/output/reasoning/cost 缺失值保持 NULL。
15. Pure Chat Admission 与单 Run 热路径只访问默认聊天所需表，RAG/Memory/Action/MCP/
    Channel/Learning 读取和写入次数均为零；进程开放流量前一次 Store-only shared-ledger
    safety transaction 是唯一例外，单独计量且只读最小 Attempt kind/state 投影，不读取
    Channel ingress/Endpoint/Envelope/Secret，也不加载 Member/Action Definition/Config/
    Authority/artifact/Registry/SDK/Provider。
16. 备份与恢复通过 integrity、FK、identity、fingerprint、database/artifact
    digest、精确 artifact 闭包、安全文件树和 no-overwrite 验证；Create 不遗留
    本次只读产生的 source sidecar。
17. Backup restore 后 `store_instance_id`、Model 与两种 Dispatch kind 的
    PENDING/UNKNOWN、History
    sequence、Usage 和冻结装配语义不变；seed rebuild 只比较规范化业务语义。
18. init 与 restore 均证明 artifact-first、database-final commit marker；init
    只恢复与 prepared seed 精确一致的孤儿 artifact root，restore 对已有目标仍
    no-overwrite。
19. S1 全链路验收经过真实生产 composition root 和 CLI；S2 增量经过同一生产
    Assembly/Context/Store/Loop/backup 组件闭包和真实消费者，而不是替代 Runtime 或
    只调用裸 Store 的 Legacy 测试。
20. Context Compiler 精确证明 `<85%` 无记录、`==85%` 生成单一 compilation（有
    合格旧历史时最多一次摘要，否则记录 `NO_ELIGIBLE_SUMMARY_UNDER_BUDGET`）、
    `==100%` 直接 Drop、最小最旧 History 前缀、近期窗口和受保护内容 fail-closed。
21. 非可信 Context 只进入 canonical JSON USER 数据封套；静态 Context 不得摘要或
    Drop，bootstrap 在发布前拒绝相反配置。
22. compilation/request/PENDING Attempt 同事务提交；伪造摘要、非前缀 Drop、缺失或
    漂移 compilation 均零写入、零 permit。PENDING 重开与 backup/restore 保留原
    request/compilation bytes 和 ref，且不重新编译或调用 Adapter。
23. ModelProfile 发布和 Admission 只接受精确 CONFIG 闭包；模型 build、config、
    artifact 或 adapter 不匹配时，在 Run/Attempt/permit 写入前失败关闭。
24. ModelProfile 只收紧 ContextPolicy。无画像路径零额外读取；较大画像不扩张；
    较小画像的有效 85% 水位同时约束 compilation 校验与 nil-compilation 轻路径。
25. `LoadRunForLoop` 重开及完整 backup/restore 后，画像 ref/canonical bytes 与原冻结
    闭包逐字节一致，且恢复不访问 current Control 或 Provider 最新元数据。
26. 动态 RAG 的 Config、Authority、source 与四级 scope 在 Publish、Admission、
    `LoadRunForLoop` 和新 Attempt 前使用同一冻结闭包；任何扩大或错配均原子拒绝。
27. 新 RAG Attempt 只接受同一 Context Compiler 产生的完整 request/compilation
    bytes；低水位也必须有 retrieval evidence，额外 USER/SYSTEM 消息不得通过入库。
28. 已持久化 Attempt 的 Load、retry、PENDING 与 MODEL_UNKNOWN 只验证冻结 evidence，
    不调用 `CompileV1`、Registry、Knowledge Provider 或模型 Adapter。
29. production startup 必须在 Catalog/Registry/Loop 构造前完成 Store-only safety
    transaction：只读最小 ledger/continuation/lease 投影，原地 CAS 原 PENDING→UNKNOWN；
    UNKNOWN/稳定状态只做最小安全校验。它绝不加载 Member、Action Definition、Config、
    Authority、artifact、Registry、SDK 或 Provider。显式 Run continuation 与实际调用边界
    才按冻结 Binding 验证相应内容，且不得改用当前版本。
30. 实际调用前只允许 current Catalog 对原 Provider+Port 做 deny-only 撤权；无 RAG
    Binding 的默认路径不得读取知识 artifact、解析知识 Adapter 或调用知识 Host。
31. 动态 Context 必须按 Config Parameters schema 严格分派 Knowledge/Memory；二者的
    BindingIndex 并集唯一且保持 PortPlan 顺序，不允许 ModuleID/品牌分支。
32. 新 Memory Attempt 在 Begin 事务内证明 snapshot revision 仍为当前 Agent head；
    Begin 后重开、PENDING、UNKNOWN 与 backup/restore 只读冻结 snapshot/request/output，
    current head 推进不得改变原请求。
33. Tenant/Agent/Workspace/entry kind/TTL/limits 的越权或漂移均在 Attempt/permit 前
    零写入失败；Memory 正文只进入不可信 USER envelope，计数不能关闭或替代 RAG。
34. 首次 SUCCEEDED 终态恰好追加一个 revision；FAILED/UNKNOWN 不追加，终态精确重入
    不重复计数，两个并发成功都基于提交时最新 head 合并且不丢贡献。
35. 无 Memory Binding 的 Pure Chat 对 Memory revision、snapshot、Config/Authority、
    artifact、Registry、Host 和更新脚本的读写/调用计数均为零；显式选择 Memory 但
    genesis、Provider 或权限缺失时 fail-closed，不能静默聊天。
36. 完整备份恢复保持所有 Memory revision/snapshot canonical bytes 与 current head
    一致，并拒绝断裂 parent、错 owner、错 source Attempt、伪造摘要或计数。
37. Action Profile Admission 通过无执行/Store 权限的私有 materializer 冻结唯一
    PublicActionID→ProviderActionID/Binding/Definition 映射；冲突、缺本地 alias/Effect/
    结果上限、Config/Authority/Workspace 越权或 basis 漂移均零 Run 写入失败。无
    Action Profile 的 Admission 与单 Run 热路径对 artifact/Config/Authority/Registry/
    Describe/SQL 访问全部为零；启动时一次 Store-only ledger safety transaction 单独
    计量且不加载任何可选模块内容。
38. ACTION_PROPOSAL 的 snapshot/public+provider action/definition/input/payload 由 Store
    从冻结 closure 重算唯一 ContentDigest；任一篡改在 DispatchAttempt/permit/executor
    前 fail-closed。
39. 模型一 SUCCEEDED/Usage、post-model UsageLedgerRef、Proposal、
    DispatchAttempt=PENDING、互斥 Frame 指针和 Event 在单一事务提交；失败全回滚，
    permit 只产生一次且 executor 零调用，串行事务与冻结状态机证明首片每 Run/Member
    最多一条而不永久锁死未来多轮 Action。
40. Gateway 消费 permit 前复核 lease/fencing、Binding、撤权、Authority、Effect
    与 deadline；外部效果前可证明 PENDING 已提交，Loop/SDK/模型无法访问 executor。
41. SUCCEEDED/FAILED/UNKNOWN 只由唯一 Action 终态 API 写入；严格 execution-result wire、
    身份或真实回执错配均拒绝，receipt 可空且不伪造；已确认 SUCCEEDED 的非 canonical/
    超限 result 规范为 RESULT_REJECTED 并终止 Run，已持久化 AVAILABLE 超限则是闭包
    损坏。相同终态幂等，不同终态冲突；UNKNOWN 只由
    ReconcileActionDispatchOutcome 更新原 Attempt/Frame。
    Action FAILED/UNKNOWN 不更新 Memory。
42. Action 成功后模型二只从持久化模型一请求追加不可信 ACTION_RESULT，并用
    `source_dispatch_attempt_id` 闭合；模型一已逐 Action 按完整最坏 envelope 预留同一
    估算器预算，RESULT_REJECTED 明确终止且不进模型二，
    模型二 RequestDigest 精确闭合原请求前缀和结果后缀；不重新 Describe/Prepare/RAG/
    Memory，第二 Action 请求不创建新 DispatchAttempt。
43. Action PENDING、外部效果后终态失败和 kill 均由 Store-only 启动事务把原 Attempt
    CAS 为 UNKNOWN；恢复、自动重入与 backup/restore 不加载冻结模块内容，对 Describe/
    Prepare/Gateway/executor 调用为零，也不换 Binding/Proposal/Run。
44. Action 切片当时完整 bundle 的四类 model/action 计数、Proposal/Result/receipt/evidence、冻结 Definition 与模型
    source 链逐字节保真；外部效果不被备份 manifest 伪造成已回滚或已确认。
45. Action 切片当时的 18 表 fingerprint、真实 `text.stats` production composition、隔离 Effect 故障注入、
    Windows/WSL/race、优雅/强制关停和长链通过；取证后删除本阶段可再生测试缓存、临时
    DB/二进制/effect sandbox，Legacy/Temp/snapshot/evidence 资产只读保留。
46. `LOCAL_PROCESS + stdio + Tool-only` 只作为 `action.provider/v1` Adapter；MCP 固定
    `2025-11-25`、官方 Go SDK 固定 `v1.6.0`；该 MCP 切片当时保持 18 表，唯一 Action ledger 是
    `dispatch_attempts`，无 MCP Runtime/Store/Ledger/Port/session pool。
47. 只有 Operator exact ArtifactDigest grant 和显式 MCP Action Binding 才能惰性加载；
    `Describe/Execute` 各自在进程启动前有界验证完整 artifact。Pure Chat 与 Store-only
    启动事务对 MCP Config/Authority/artifact/Registry/SDK/process 全零。
48. Describe 的 initialize、精确版本/capability、全部分页、工具数/定义总量/帧均有界；
    Prepare 纯函数不启动进程、不读写 Store；Gateway 是唯一 executor 调用者，同一
    DispatchAttempt 恰好一次 `tools/call`，无 retry/fallback/替代进程。
49. 精确 server wire JSON-RPC error 和合法 `isError=true` 写确定 FAILED；写出后 timeout、
    EOF、进程崩溃、本地 client-closing（包括 `-32003`）或无法证明未执行均写原 UNKNOWN，
    关闭、取消与 kill 不构成“未执行”证明。
50. 完整 backup/verify/restore 逐字节保留 MCP 的原 Action Proposal、DispatchAttempt、
    result/receipt/evidence；UNKNOWN 的重启、自动重入与恢复不 initialize/list/call、不
    重放且不创建替代 Attempt。
51. 当前切片仅适用于 Operator 完全信任的本地开发模块；空 env、精确 digest 与受管
    进程树不是 OS 文件系统/网络 sandbox。REMOTE/Streamable HTTP/SSE、Resource、Prompt、
    Secret/SecretRef、Sampling、Elicitation、Roots、Tasks、常驻 pool、动态订阅、
    auto-discovery 和不可信第三方隔离均延后并继续 fail-closed。
52. 该 S2 Channel/Composite 历史 `0001_current.sql` 恰有 19 张 ordinary table，schema fingerprint 为
    `bf2c20dbed0316a829b0a96535e8b816c628036535fe499cf824a25b7d470245`；迁移构件为
    29055 bytes，SHA-256 为
    `7d7162110f972bdde54c6147e4e89544246b83d18a25e96469fb6ffe51c6fd8a`。
53. Workspace-scoped Cursor seed、revision CAS、before/after chain、IngressKey 与
    ProviderEvent 去重由 `channel_ingress_receipts` 表达；ACCEPTED receipt 与完整 Admission
    在一个事务提交，stale/fork/conflict 全回滚且不留下 Run 或半推进 Cursor。
54. REJECTED 保存有界 canonical Envelope 以保证确定性去重和审计，但只有 Store 在同一
    事务证明不存在 active exact identity 时才能写；它不能覆盖可授权事件，也不能转换成
    ACCEPTED。exact duplicate 不追加 Cursor revision。
55. `dispatch_attempts` 是 ACTION|CHANNEL_SEND 唯一外部效果账本；最终模型成功、History、
    CHANNEL_SEND_PROPOSAL、PENDING、Frame/Event 与一次性 Gateway permit 原子闭合。无
    `channel_outbox`，Gateway 之外不能调用 sender，同一 Attempt 不重发、不 fallback。
56. Channel SUCCEEDED/FAILED/UNKNOWN 只 CAS 原 Attempt；普通 outcome 不能注入 reconciliation
    evidence，UNKNOWN 只能由有证据的专用 API 对账。终态或 WAITING_RECONCILIATION duplicate
    返回原持久化结果，绝不恢复为 PENDING、创建替代 Run/Attempt 或再次发送。
57. 启动和优雅关停均使用同一个 Store-only safety recovery，把原 CHANNEL_SEND PENDING
    收口为 UNKNOWN/WAITING_RECONCILIATION；关停先关闭 Admission、排空 handler，再使用
    有界 detached context 收口。两条路径对 Endpoint/Secret/artifact/Adapter/network 均零访问。
58. 完整 backup/verify/restore 保存 Channel Cursor/Event/Ingress、四项 Channel 派生计数、
    source final ModelAttempt、Proposal/Result/receipt/evidence 与 Run/Frame/Event/History
    双向闭包；删终态 send、孤立 send、篡改闭包或非法 canonical object 均 fail-closed，
    verify/restore 不构造 Adapter、不读 Secret、不发网。
59. Channel 默认关闭。显式 composition 必须选定唯一 Workspace/Endpoint、存在匹配 Cursor
    seed、通过当前 deny-only 门禁和全库语义闭包，且没有该 Endpoint 未对账 UNKNOWN，才可
    构造 Adapter；默认 Pure Chat 除全局 shared-ledger safety scan 外零 Channel 读取。

当前源码中的聚焦验收命令固定为：

```text
go test -count=1 ./sdk/moduleapi ./internal/corecontract ./internal/controlcontract ./internal/assemblycompiler ./internal/bootstrapseed ./internal/contextcompiler ./internal/currentstore ./internal/currentbackup ./internal/modulehost ./internal/exactadapter ./internal/actiongateway ./internal/mcpstdio ./internal/coreloop ./internal/localchat ./internal/channelservice ./internal/loopbackchannel ./cmd/freeagent
```

关键证据映射如下；测试改名时必须同步更新本节：

| 不变量 | 主要测试 |
|---|---|
| 19 表、冻结 fingerprint、schema drift | `TestMigration0001CreatesExactlyCurrentStoreTables`、`TestSchemaFingerprintIsFrozenAndDetectsDrift` |
| 显式 init、no-overwrite、只读 open 不隐式创建 | `TestInitVerifyOpenAndCloseCurrentStore`、`TestInitNeverOverwritesAndConcurrentInitPublishesOnce`、`TestOpenExistingNeverInitializesMissingEmptyOrForeignStore` |
| WAL 收口与 publication owner fence | `TestPrepareClosedCurrentStoreForPublication`、`TestPrepareClosedCurrentStoreForPublicationRejectsActiveOwner` |
| 生产 init、DB commit marker、精确孤儿 artifact 恢复 | `TestProductionCompositionInitChatAndRestartIdempotency`、`TestInitializeProductionDataNeverOverwritesTargets`、`TestInitializeProductionDataResumesExactOrphanArtifactRoot` |
| 完整 bundle、source sidecar 清理、PENDING/NULL 与 MODEL_UNKNOWN/显式零保真 | `TestFullBundleRoundTripPreservesIdentityPendingAndNullUsage`、`TestFullBundleRoundTripPreservesOriginalModelUnknownAndKnownZeroUsage` |
| 篡改、活动 owner、no-overwrite、失败无正式目标 | `TestVerifyBundleRejectsTamperedDatabaseArtifactAndManifest`、`TestCreateBundleRejectsActiveStoreOwner`、`TestNoOverwriteAndCreateFailureLeaveNoFormalTarget` |
| CLI init/chat 与 backup/verify/restore 后精确重试 | `TestRunInitAndChatCommands`、`TestRunBackupVerifyRestoreCommands` |
| Store-only 启动 safety transaction、原 Attempt CAS 与零可选内容访问 | `TestScanStartupRecoveryIsGlobalDeterministicAndReadOnly`、`TestScanStartupRecoveryValidatesContextAndStoreLifecycle`、`TestProductionStartupRecoveryClosesPendingWithoutArtifactInspection`、`TestHistoricalMCPUnsettledIsZeroAccessDuringPureChatStartup`、`TestOpenProductionCompositionFailsClosedDuringStartupRecovery` |
| 真实进程 Kill 与终态事务失败不重放 | `TestProductionCompositionRecoversRealProcessKillWithoutModelReplay`、`TestProductionCompositionRecoversTerminalTransactionFailureWithoutReplay` |
| 真实 HTTP retry/restart、优雅与强制关停 | `TestRunServeLoopbackAndGracefulShutdown`、`TestForcedHTTPShutdownReportsUndrainedHandlerAndCancelsOnlyAfterGrace` |
| 64 请求、restart、backup/restore 长链路 | `TestProductionLongChainSurvivesRetryRestartBackupAndRestore` |
| 85/100 精确边界、最小旧前缀与重复内容 turn 身份 | `TestCompileV1AtExactSoftWatermarkSummarizesExactlyOnce`、`TestCompileV1AtExactFullBudgetUsesDirectDrop`、`TestCompileV1RepeatedContentHasSequenceDistinctDropUnits` |
| 污染隔离、静态保留声明拒绝与受保护超限 | `TestCompileV1UntrustedContextUsesCanonicalJSONEnvelope`、`TestCompileV1RejectsStaticRetentionClaimsAndPlacementReordering`、`TestUniversalLoopRejectsProtectedFullBudgetBeforeInvocation` |
| compilation 原子写入、摘要重算与兼容旁路关闭 | `TestBeginModelDispatchPersistsAndLoadsExactContextCompilation`、`TestBeginModelDispatchRejectsForgedSummaryTextBeforePendingWrite`、`TestBuildPureChatRequestV1CannotDiscardRequiredCompilation` |
| 高水位 PENDING 重开不重编译/不重放 | `TestUniversalLoopReopensHighWatermarkPendingWithoutRecompileOrReplay` |
| compilation backup/verify/restore/reopen 字节保真 | `TestFullBundleRoundTripPreservesContextCompilationClosureByteExactly` |
| ModelProfile canonical、装配、Seed/发布与 Admission 闭包 | `TestModelProfileV1CanonicalRoundTripSortsAndDefensivelyCopies`、`TestCurrentCompilerFreezesOptionalModelProfileWithoutChangingRouting`、`TestOptionalModelProfileImportsExactContentAndPublicationRef`、`TestPublishControlCatalogRejectsInvalidModelProfileBeforeRun`、`TestCommitRunAdmissionRejectsUnavailableModelProfileBeforeWrites` |
| 画像只收紧、无画像零发现与 nil-compilation 门禁 | `TestTightenContextPolicyV1ForModelProfileNeverExpands`、`TestPreparePureChatRequestV1WithoutProfileDoesNotDiscoverProfileContent`、`TestPreparePureChatRequestV1LargerModelProfileDoesNotChangeRequestBytes`、`TestBeginModelDispatchRejectsNilCompilationAfterProfileTightening`、`TestBeginModelDispatchWithoutContextCompilationKeepsNullableClosureEmpty` |
| ModelProfile 重开与完整 backup/restore 字节保真 | `TestLoadRunForLoopReopensByteExactModelProfileClosure`、`TestFullBundleRoundTripPreservesIdentityPendingAndNullUsage` |
| RAG 命中、零命中、四级 scope、污染隔离与低水位 evidence | `TestPrepareChatRequestInvokesExactKnowledgeOnce`、`TestDeterministicKnowledgeZeroHitSucceeds`、`TestDynamicKnowledgeAdmissionRejectsEachScopeLevelWithoutWrites`、`TestCompileV1KnowledgeInjectionRemainsUntrustedUserData`、`TestCompileV1KnowledgeBelowWatermarkPersistsEvidenceAndIsByteStable` |
| 新 Attempt compiler 证明与恢复不重编译 | `TestBeginModelDispatchRejectsKnowledgeRequestNotProducedByCompiler`、`TestFrozenKnowledgeAttemptValidationDoesNotRecompile` |
| deny-only 撤权与零 Adapter 调用 | `TestBeginModelDispatchRevokedCurrentProviderLeavesNoAttempt`、`TestUniversalLoopRevokedBetweenBeginAndHostFailsWithoutAdapter`、`TestUniversalLoopRecoversPendingAfterRevocationWithoutReplay` |
| 惰性知识加载与多 Instance | `TestProductionCompositionDoesNotReadUnboundKnowledgeArtifact`、`TestProductionRegistrySharesArtifactAdapterAcrossCatalogInstances`、`TestProductionCompositionLazilyLoadsExactKnowledgeAndRejectsArtifactDrift` |
| 选中 RAG 时惰性 artifact/source 校验与完整备份 | `TestProductionCompositionLazilyLoadsExactKnowledgeAndRejectsArtifactDrift`、`TestFullBundleRoundTripPreservesRAGArtifactAndClosureWithoutRetrieval` |
| Memory wire、最小 UTF-8 摘要与 wildcard 身份隔离 | `TestMemoryContextBindingV1FreezesAlgorithmConfigAndDynamicEnvelope`、`TestBuildSuccessfulRevisionSmallSummaryBudgetRetainsCompleteRune`、`TestBuildSuccessfulRevisionRejectsWildcardExactObjectIdentities` |
| Memory current-head Begin、冻结恢复、篡改与 scope/cardinality | `TestDynamicMemoryBeginFreezesCurrentHeadAndRecoveryKeepsExactRevision`、`TestMemoryEvidenceRecomputesRequestOutputAndPrompt`、`TestFrozenMemoryBindingsRequireExactScopeAndAtMostOne` |
| Memory 成功终态原子更新、幂等、并发重基与失败回滚 | `TestSuccessfulModelOutcomeAtomicallyAdvancesAgentMemory`、`TestSuccessfulModelOutcomeReplayVerifiesExactMemoryRevision`、`TestConcurrentSuccessfulRunsRebaseOnCommitTimeMemoryHead`、`TestMemoryAppendFailureRollsBackWholeModelOutcome` |
| 默认零 Memory 与显式生产长链 | `TestNewAttemptWithoutMemoryNeverConsultsAgentMemoryStore`、`TestDefaultProductionCompositionHasNoMemoryStateArtifactOrLoad`、`TestMemoryProductionLongChainIsLazyAtomicAndRestartSafe` |
| Memory 完整备份与语义闭包 | `TestFullBundleRoundTripPreservesIdentityPendingAndNullUsage`、`TestFullBackupRejectsTamperedAgentMemorySemanticClosure`、`TestFullBackupRejectsDiscontinuousAgentMemoryChain` |
| Action Admission 与 Pure Chat 零访问 | `TestActionChatServiceLoadsSelectedBindingMaterialsInOrder`、`TestPureChatHasZeroActionAccessWithAvailableUnselectedProviders`、`TestActionMaterialLoadingIgnoresUnselectedProfiles` |
| Action 原子 Begin、逐写入回滚与一次性 permit | `TestCommitModelActionAndBeginDispatchIsAtomicAndOneShot`、`TestCommitModelActionAndBeginDispatchDenialsAreAtomic`、`TestCommitModelActionAndBeginDispatchRollsBackEveryAuthoritativeWrite` |
| Gateway 二次安全门、并发 permit 与结果归类 | `TestGatewayExecuteRechecksPendingClosureBeforeExecutor`、`TestGatewayExecuteCopiedGrantInvokesExecutorExactlyOnce`、`TestGatewayExecuteDistinguishesPostEffectIdentityAndResultFailures` |
| Action 三类终态、幂等/错配拒绝与 UNKNOWN CAS | `TestCommitActionDispatchOutcomeProjectsAuthoritativeRunState`、`TestCommitActionDispatchOutcomeIsExactAndRejectsIdentityDrift`、`TestReconcileActionUnknownCASesOriginalAttemptWithoutReplay`、`TestReconcileActionUnknownCanCASOriginalAttemptToFailure` |
| 模型二 source/Usage/终态闭合 | `TestSecondModelDispatchClosesSuccessfulActionRun`、`TestProductionActionCompositionRunsModelActionModelChain` |
| Action Kill、效果后终态失败与启动稳定态 | `TestProductionStartupRecoversKilledActionWithoutExecutorReplay`、`TestProductionStartupRecoversActionTerminalCommitFailureWithoutReplay`、`TestProductionStartupLeavesModelReadyAfterActionForLaterRun` |
| Action 完整备份与 reconciliation evidence | `TestFullBundleRoundTripPreservesActionClosureWithoutExecution` |
| MCP exact grant、惰性加载与真实 model→tool→model 纵链 | `TestProductionMCPCompositionRequiresGrantAndRunsOnceAcrossRestart`、`TestAdapterDescribePrepareExecuteRealStdio`、`TestAdapterPrepareAndGenericInvokeNeverStartProcess` |
| MCP wire FAILED、发送后 UNKNOWN、阻塞写取消与无重试 | `TestAdapterToolErrorIsConfirmedFailed`、`TestAdapterJSONRPCErrorIsConfirmedFailedWithBoundedDiagnostic`、`TestAdapterCrashAfterCallIsAmbiguousAndNotRetried`、`TestAdapterBlockedToolCallWriteReachesDeadlineWithoutRetry`、`TestCommandTransportCanceledBlockedWriteClosesAndReaps` |
| MCP 完整 artifact 使用边界、扫描上限与漂移拒绝 | `TestVerifyArtifactDirectoryDigestDetectsWholePackageDrift`、`TestScanArtifactDirectoryEnforcesPathFileAndByteLimits`、`TestAdapterRejectsExecutableDriftWithoutStartingProcess`、`TestAdapterRejectsNonExecutableArtifactWithoutChangingMode`、`TestAdapterRejectsDescriptorAndSiblingDriftWithoutStartingProcess` |
| MCP 工具定义总量与 Tool-only 单向能力面 | `TestAdapterRejectsInvalidDiscoveryAndStdoutPollution`（`aggregate-overflow`、`unsolicited-notification`、`unsolicited-request` 子测试） |
| MCP Connect/Describe cleanup 与已确认执行终态归属 | `TestAdapterReportsConnectAndDescribeCleanupFailures`、`TestAdapterConfirmedExecutionKeepsTerminalWhenCleanupFails` |
| MCP UNKNOWN backup/restore 与启动零访问 | `TestProductionMCPUnknownIsNeverReplayedAcrossBackupRestore`、`TestHistoricalMCPUnsettledIsZeroAccessDuringPureChatStartup` |
| Channel Cursor/Event、入站原子 Admission、去重与 REJECTED 权限 | `TestChannelIngressAcceptedCommitsCursorAndRunAtomicallyAndDedupes`、`TestChannelIngressStaleCursorRollsBackWholeAdmission`、`TestRejectedChannelIngressAdvancesOnceAndCannotBecomeAccepted`、`TestRejectedChannelIngressCannotOverrideActiveIdentity` |
| CHANNEL_SEND 原子 Begin、一次性 permit、三态终态与原 UNKNOWN 对账 | `TestModelChannelBeginIsAtomicIdempotentAndPermitIsOneShot`、`TestChannelSucceededAndFailedTerminateWithoutLosingAnswer`、`TestChannelUnknownNeverReplaysOrReturnsPendingAndReconcilesOriginal`、`TestChannelOrdinaryOutcomeCannotInjectReconciliationEvidence` |
| Channel duplicate 历史幂等与当前 deny-only 权限 | `TestServiceAdmitsAndResumesOnlyTheOriginalRun`、`TestServiceAcceptedDuplicateDoesNotResumeAfterCurrentRevocation`、`TestServiceUnknownDuplicateReturnsOriginalAfterRouteRevocation` |
| Channel 完整备份、终态反向闭包与零外部访问 | `TestChannelBundleRoundTripPreservesCursorAndUnknownWithoutExternalAccess`、`TestChannelSemanticVerifierRejectsBrokenClosure`、`TestChannelSemanticVerifierRejectsDeletedTerminalSend`、`TestChannelBackupVerifierHasNoExecutionDependency`、`TestVerifyCurrentStoreSemanticClosureRejectsChannelIdentityDrift` |
| 默认关闭、显式启用与真实 HTTP duplicate 不重发 | `TestDefaultProductionCompositionNeverTouchesConfiguredChannel`、`TestExplicitProductionChannelCompositionRegistersExactEndpoint`、`TestExplicitProductionChannelCompositionRejectsUnreconciledUnknown`、`TestExplicitChannelServeEndToEndAndDuplicateDoesNotResend` |
| 精确 reply target、一次 POST、无 redirect/retry 与模糊失败 UNKNOWN | `TestReplyTargetIsBoundAtInboundPrepareAndExecute`、`TestPrepareSendIsPureAndExecutePostsExactlyOnceWithoutSecretLeak`、`TestExecutePreparedNeverFollowsRedirect`、`TestExecutePreparedAmbiguousHTTPFailuresAreUnknownAndNeverRetried` |
| HTTP Admission drain 与 Store-only 关停恢复 | `TestDrainingHTTPHandlerRejectsConcurrentRequestsAfterAdmissionCloses`、`TestDrainingHTTPHandlerConcurrentAdmissionBoundary`、`TestServeHTTPDrainsActiveRequestBeforeCancellation`、`TestProductionShutdownRecoveryUsesBoundedDetachedContext` |

### 18.1 Parent/Child + Composite 第一实现切片验收证据

状态：`S2_PARENT_CHILD_COMPOSITE_ACCEPTED_DEVELOPMENT_SLICE`。该 S2 切片当时的 schema 事实为 19 表、
fingerprint `bf2c20dbed0316a829b0a96535e8b816c628036535fe499cf824a25b7d470245`、
migration `29055` bytes / SHA-256
`7d7162110f972bdde54c6147e4e89544246b83d18a25e96469fb6ffe51c6fd8a`。

| 门禁 | 该 S2 历史静止树测试证据 |
|---|---|
| schema、原子 Admission 与并发 | `TestSchemaFingerprintIsFrozenAndDetectsDrift`、`TestCommitCompositeRunFamilyPublishesProjectionAndIsIdempotent`、`TestCommitCompositeRunFamilyRollsBackEveryAuthoritativeWritePoint`、`TestCompositeAdmissionConcurrentIdentityAndFamilyIsolation` |
| family 形状、身份与单向摘要 | `TestCompileCompositeFamilyIsDeterministicAndClosesParentChildren`、`TestCompositeChildIdentityDerivationGolden`、`TestCompositeCompileInputExposesOnlyParentIdentity`、`TestCompositeAdmissionRejectsBrokenFamilyShapeWithoutWrites` |
| 可选能力和跨 Workspace 零写拒绝 | `TestCompositeAdmissionRejectsOptionalMutableAndEffectCapabilitiesWithoutWrites`、`TestCompositeChildSideEffectPortGateRejectsActionAndChannel` |
| assignment、污染隔离、50% 分配和超限 | `TestCompileV1CompositeChildInjectsProtectedAssignmentOnly`、`TestCompileV1CompositeRootInjectsOrderedBudgetedUntrustedResults`、`TestCompileV1CompositeRootOverBudgetFailsWithoutCompilation`、`TestContextCompilationV1CompositeEvidenceFailsClosed` |
| `ALL_REQUIRED` 与 attempt-free Core failure | `TestCommitCoreDeterministicFailureIsAttemptFreeAtomicAndIdempotent`、`TestCompositeRootFailedChildTerminatesAheadOfPendingWithoutModelAttempt`、`TestCompositeRootChildResultOverBudgetTerminatesBeforeModelPermit` |
| dispatch cap、UNKNOWN 与 family usage | `TestCompositeFamilyUsageProjectionCompleteKnownSumsAndReadOnlyRetry`、`TestCompositeFamilyUsageProjectionKeepsEachMixedNullUnknown`、`TestCompositeFamilyUsageProjectionUnknownAndPendingConsumeSlots`、`TestCompositeFamilyUsageProjectionDoesNotCombineMixedCurrencies`、`TestCompositeFamilyUsageProjectionRejectsNonRootAndTamperedClosure`、`TestCompositeFamilyUsageProjectionRejectsNonCanonicalCostFact` |
| 统一取消和 permit 线性化 | `TestRunCancellationRequestV1RoundTrip`、`TestRequestRunCancellationFamilyAtomicIdempotentAndConflicts`、`TestRunCancellationRacesModelBeginAtOnePermitBoundary`、`TestRunCancellationDoesNotRewriteOrReplayModelUnknown`、`TestRunCancellationBlocksNewExternalEffectPermits` |
| 重启、完整备份和篡改拒绝 | `TestCompositeReopenMatrixPreservesLedgerAndNeverRegrantsPendingMerge`、`TestCompositeBundleRoundTripPreservesFamilyResultsAndCancellation`、`TestCompositeSemanticClosureRejectsFamilyTampering`、`TestCompositeBackupRestoreStartupRecoveryKeepsUnknownNonReplayable`、`TestCompositeCoreDeterministicFailureBundleRoundTrip`、`TestCompositeCoreDeterministicFailureSemanticTampering` |
| 生产入口与 Pure Chat 隔离 | `TestRunCompositeChatUsesProductionComposition`、`TestCompositeChatServiceRunsAtomicParallelFamilyAndReusesExactRetry`、`TestPureChatAdmissionKeepsCompositeOptionalPathEmpty`、`TestCompileV1BelowWatermarkPreservesPureChatBytes` |

该 S2 历史静止树还通过 Windows/Linux 全仓 21 包测试、两端 `go vet`、Linux 全仓 Race，以及
Windows/Linux/Darwin × amd64/arm64 六平台构建。该 S2 裁决只覆盖同 Workspace、depth 1、
2..8 Specialist、固定 `ALL_REQUIRED` 的第一切片；它当时不包含 Reviewer、公平 Scheduler、
动态图、Learning 与跨 Workspace。公平 Scheduler 后续已由第 19 节单独验收。

## 19. S3-A 公平 Scheduler 最小 Store 合同

状态：`S3_FAIR_SCHEDULER_ACCEPTED_DEVELOPMENT_SLICE`。本节是当前 S3-A 实现与验收权威，
仅在 Operator 显式启用 Scheduler 时替代本文早期“尚无 fair queue”的 S2 阶段性限制；
旧段落中的 19 表与 schema 数值继续作为已验收 S2 历史基线，不得冒充当前 S3-A
development candidate。

### 19.1 唯一新增状态

S3-A 只新增第 20 张 ordinary table：

```sql
CREATE TABLE workspace_scheduler_state (
    tenant_id TEXT NOT NULL,
    workspace_id TEXT NOT NULL,
    served_units INTEGER NOT NULL CHECK (served_units > 0),
    revision INTEGER NOT NULL CHECK (revision > 0),
    updated_at INTEGER NOT NULL CHECK (updated_at > 0),
    PRIMARY KEY (tenant_id, workspace_id)
) STRICT;
```

生产行延迟到该 Workspace 第一次成功 claim 时创建，首值为 `served_units=1, revision=1`；
零值只用于缺行时的选择计算。`served_units` 是成功取得一次符合条件 Run lease 的累计
调度量子，不是 token、终态成功数或运行时长。执行机会授予后，即使 Provider 后续
FAILED/UNKNOWN 也不回退；无 runnable、容量不足和事务回滚不递增。两个计数达到 SQLite
INTEGER 上限时 fail closed，不回绕、不自动归一化；`updated_at` 只用于观测，禁止参与公平
排序。

该表不是 queue，也不保存候选 Run、permit、worker 或模型结果。可运行事实仍只来自
`runs + loop_frames + RunLease`，禁止新增 `scheduler_jobs`、消息队列或第二审计表达。

### 19.2 原子 claim

Store 只提供窄的 `ClaimFairRun` 类 API，不提供生产使用的 `ListRunnable → AcquireLease`
两步接口。请求必须包含 production composition 已装配的精确 Tenant、owner、TTL 和
三层正数上限。候选限定到该 Tenant，避免用错误 Registry/Catalog 执行其他 Tenant；
`global_workers` 统计同一 Store/本地进程全部未过期 lease，Workspace 和 family 分别按
`(tenant_id, workspace_id)` 与 frozen root RunID 统计。普通 Run 自成 family。

一次 `BEGIN IMMEDIATE` 必须不可分割地完成：

1. 从当前 Run/Frame/lease 投影派生合法 runnable；
2. 校验 Store-global、Workspace、family 三层 active lease 数；
3. 按 `served_units → workspace_id(binary) → runs.created_at → run_id(binary)` 选择；
4. 对当前 Run/Frame revision 与 lease epoch CAS 取得既有 fenced `RunLease`；
5. 对唯一 Workspace 状态执行 `served_units + 1, revision + 1`；
6. 一次提交 lease 与计数，任一步失败全部回滚。

返回状态严格区分：`CLAIMED`、`NO_RUNNABLE`、`CAPACITY_EXHAUSTED`。后两者不得写任何
权威行；`CAPACITY_EXHAUSTED` 表示存在语义上可推进候选但至少一个有效 lease 上限阻止
claim。`BEGIN IMMEDIATE` 内的候选与 lease CAS 不得把真实竞争伪装为无工作；不可能的
CAS 失败按 integrity/conflict fail closed。

### 19.3 runnable 与恢复

首版 runnable 仅为投影闭合且无 pending pointer 的：

- `READY`；
- `MODEL_READY_AFTER_ACTION`；
- Root `WAITING_CHILDREN`，且存在 direct Child、全部 direct Specialist Child 已
  `TERMINATED`，因此本次推进可以确定性提交 family failure 或唯一 merge。

带取消 latch、持有未过期 lease、`MODEL_PENDING`、`ACTION_PENDING`、
`CHANNEL_PENDING`、`WAITING_RECONCILIATION`、`TERMINATED` 全部排除。PENDING 与 UNKNOWN
继续由既有 Store-only Startup Recovery 和原 Attempt 对账路径收口；Scheduler 不创建
Attempt、不换 Provider、不语义重放。S3-B 只能通过正式规格扩展 `WAITING_CHILDREN` 的
Reviewer 依赖闭合条件，不能放宽 PENDING/UNKNOWN 禁令。

claim 返回的现有 `RunLease` 就是唯一 permit。Universal Loop 必须直接消费该 lease；禁止
先释放再获取或二次 acquire。Scheduler 关闭时不调用本节读取/写入接口，Current Store
中可不存在任何 Scheduler 状态行，现有 Pure Chat/Composite 行为保持 S2 合同。

### 19.4 backup 与原始设计影响

SQLite snapshot、bundle digest、verify、restore 必须包含第 20 张表并逐行保真。语义校验
可以证明字段范围、唯一键、引用到至少一个同 Tenant/Workspace Run，以及 restore 前后
一致；在不建立第二历史账本的前提下，不能从 Run 终态反推 `served_units`，也不能声称
证明不存在历史删除。不得为此向 `run_events` 复制调度计数。

本节只增加一张当前计数表和一个可选 Store 事务；不读取任务正文，不把专业知识写入
Workspace，不改变 Agent/Profile 权限，也不增加 Runtime、Store、Loop 或 Gateway。
Scheduler 缺失或关闭时保持零额外状态访问，Pure Chat 仍是最轻路径。

### 19.5 S3-A 历史开发切片证据

S3-A 验收时的实现与本合同闭合：

1. 该 S3-A 历史 migration 恰有 20 张 ordinary table；fingerprint 为
   `098bdf44ee765727dc41f84ee3a8af65069691226d815cde882bfa45a9ff6409`，migration 为
   `29575` bytes / SHA-256
   `98c2f1e520bcecb1b3777bcab6014c24d34d110cb6dc751d5bd93eee84e293ce`；该组数值只作
   S3-A 验收证据保留，W5-F1 当时沿用的 24 表 identity 由 §24/§25 保留，W2-R1 后的
   当前数值以 §3.3 与 §26.1 为准；
2. `ClaimFairRun` 的三态、least-served 顺序、global/Workspace/family cap、直接 lease
   计入、事务回滚、并发全局上限、重启保真、取消/PENDING 排除均有 Store 测试；
3. `RunClaimed` 证明 Scheduler lease 被同一 Universal Loop 消费，RunID/lease 不匹配时
   fail closed 并释放原 lease；
4. 三个真实 Workspace、Composite Child 并行与唯一 Root merge、Child UNKNOWN 不 claim
   Root、不生成 merge Attempt、不语义重放，以及 terminal retry 零新增计数均通过真实
   Chat/Composite consumer；
5. Scheduler 自身覆盖同目标并发单次执行、执行异常稳定 fatal latch、Close 后零新 claim、
   terminal/PENDING/UNKNOWN 稳定投影和 caller-worker 上限；
6. production composition 覆盖显式开关、默认关闭零 Scheduler semantic/state 访问、
   Scheduler+Channel 拒绝、启动 semantic gate，以及 drain 失败不提前关闭 Store；
7. 完整备份使用真实 `ClaimFairRun` 状态，经过 create/verify/restore 后逐行一致，并拒绝
   orphan Workspace 与 counter/revision 漂移；
8. Windows 受影响包全量测试和 `go vet`、Linux/WSL 同范围 Race 已通过。

该 accepted 状态只代表默认关闭、本地 tenant-bound Pure Chat + depth-1 Composite 公平
执行开发切片。长时间无饥饿、全仓 Release、六平台构建、Public Stage、Reviewer、动态
任务图、跨 Workspace 与正式部署仍不由本节宣称完成；单次 Reviewer 审核门后续仅由
第 21 节独立验收。

## 20. 对最初设计的影响

本规格只替换持久化底座，不改变 FreeAgent 最初目标：

显式 init、publication preparation、完整备份和 database commit marker 只约束
Operator 的物理发布/恢复边界，不向 Agent、Workspace、Port 或 Module 增加新的
业务类型，也不要求外挂模块理解 SQLite 或备份格式。

- Agent 与 Workspace 继续独立，并通过 Assembly 自由组合。
- Role、Skill 通过统一 Module Protocol 接入；RAG、Memory、Action、MCP、人格和情感模块
  在 S2 仍使用同一接口标准外挂。
- 单一专业 Agent 由原 MemberSnapshot/Manifest 表达；已验收的 Composite 第一切片
  以 one Member/run 的 Parent manifest 图表达。整数权重只用于 Parent merge 的 50%
  Child-result Context allocation 与稳定顺序，不影响 RAG rank、Skill、Reviewer、模型路由
  或权限。
- 知识库共享、Agent 轻量记忆和自学习没有被删除；多 Workspace 公平调度现作为
  默认关闭的 S3-A 可选策略接入，同样不把领域知识或队列复制进 Pure Chat。
- 开放性来自 Port、Module Host、精确 Binding 与可替换模块，不来自多套 Runtime
  或多套 Store。
- `dispatch_attempts` 只冻结一次外部效果的安全事实；`ACTION|CHANNEL_SEND` 的 kind
  扩展没有把 Action、Channel、Agent 或 Workspace 的业务内容固定进 Core。私有
  executor/sender 限制的是副作用执行权；当前窄 MCP、Channel 与后续第三方/远程能力
  都通过同一 Port/Prepare + Host Adapter/Gateway 边界接入。
- 首个 MCP 切片只增加可选 `LOCAL_PROCESS` Adapter；默认关闭、Pure Chat 零访问，
  不新增 Store、Port 或 Loop 分支。它保持 Agent/Workspace 自由编排与外挂模块目标，
  同时把不受 OS sandbox 保护的本地进程明确限制为 Operator 完全信任的开发模块；
  不可信第三方与生产级隔离仍未验收。
- 首个 Channel 切片同样默认关闭，只新增 Workspace-scoped Cursor/Event/Ingress 投影并
  复用同一 Loop/Gateway/外部效果账本。它没有创建固定 Channel Runtime、每 Workspace
  独立 Store 或物理 Outbox，因此不妨碍 Agent/Workspace 自由编排，也不限制后续按统一
  `channel.transport/v1` 接入其他 Adapter。
- Parent/Child + Composite 目标继续只有一个 Store、一个 Assembly Compiler 与一个 Loop；
  S2 的 19 表加四个 nullable run 投影足以闭合 depth-1 family，S3-A 第 20 表只保存公平
  计数。未选择 Composite 时三个 parent
  列为 NULL、optional canonical family 字段省略且 family 额外查询为零；普通 Run 只有在
  显式 run-scope 取消后才设置 `cancel_request_ref`。因此既有 Pure Chat 请求字节和可选模块
  零访问不变量保持不变。公平 Scheduler 与可选单次 Reviewer 审核门已接线；Knowledge/静态
  Skill Proposal Store 已进入 W4-L1A/L1B 开发切片；W4-L2 的显式单次 Learning Reviewer、
  普通 Model Attempt 与状态投影、W4-L3 无权限 Version/Operator artifact 交接，以及 W4-L4
  默认关闭 Schedule、显式 Tick、Store-only reconcile 与只读 report 已验收开发切片。W5-D1
  已增加预冻结的单次 repair round，W5-X1 已增加受双边 grant 约束的最小跨 Workspace
  REQUEST/RESULT 传递；自动选择/替换 Reviewer、通用动态图、多轮返工、后台周期 Worker 与
  自动发布/安装/激活/绑定仍是后续能力。

因此，当前 43 表、单写、无兼容链仍是减重，不是收窄最终扩展能力。第 21 表只保存 Core
Conversation 的固定 ID、head 和 CAS revision；第 22 表保存不可变 Knowledge/静态 Skill 候选、
去重投影、proposer lineage、已验收的审核 refs/state/revision 与一对一无权限 Version，不固化行业知识，也不授予
Trust、Authority、Secret、发布、安装或激活能力；第 23、24 表只保存可选 Learning 的周期策略、
水位与逻辑任务收据；第 25–29 表只保存显式 Source/Index/Snapshot observation 与 current
Publisher Key revocation；第 30–32 表只保存 global Candidate、Tenant-scoped Review、exact-Review
Decision 与 target Manifest evidence 引用，不保存 Apply authority。两类 `APPROVED` 都只是审核结论，
不是发布或权限 grant。Skill Draft 也不进入
Context Compiler；第 33 表只保存有界、append-only Control receipt canonical；W6-2 receipt Schema 历史原子的
公开 commit 仅允许 `NO_CHANGE`，不授予在线 mutation 或 APPLIED backfill。
第 34–41 表只保存 read-only Overview observation closure；第 42 表保存 server-owned inert Artifact，
第 43 表保存 supply observation Admission，均不授予 runtime 权限。上述 W1/W4/U2/U3/W6 新增表都不把
Role、RAG、Memory、Skill、Persona 或 Team 固化进
Workspace。默认 Pure Chat
不读取 Action/Channel 可选状态或增加工具/Channel schema token，唯一例外只是启动与
关停时对共享 Attempt ledger 的最小安全扫描。

## 21. S3-B Reviewer 审核门最小 Store 合同

状态：`S3_COMPOSITE_REVIEW_GATE_ACCEPTED_DEVELOPMENT_SLICE`。本节与
`CORE_RUNTIME_V1` 第 18 节共同构成已实现的 Reviewer 编码合同；后续扩展仍不得绕过
本合同增加新表、新队列、第二 outcome writer 或第二 Runtime。

### 21.1 图与 Admission

Root Plan 可选增加一个 `reviewer` ref，冻结 Reviewer RunID、AdmissionKey、
MemberSnapshotDigest、Agent/Profile、TaskInputRef、`composite.review/v1`、
`RESULTS_GATE` 和 `max_output_tokens`。Reviewer relational row 使用现有
`parent_run_id/root manifest/parent_slot_id` 投影，保留 slot 固定为 `__reviewer__`；普通
Specialist slot 禁止使用该值。Reviewer Manifest role=`REVIEWER`、scope=`inherited`、
Assignment/Plan 为空并精确引用 Root ManifestDigest。

`CommitCompositeRunFamily` 的 input/result 只增加 optional Reviewer；Parent、2..8
Specialists 与 Reviewer 必须在同一个 `BEGIN IMMEDIATE` 全成或全败。disabled 时 optional
字段省略且 existing family bytes/rows/query count 不变。enabled family 恰有 N+2 Runs；
Reviewer 不进入 Specialist Children 数组，也不参与 10000 权重。

### 21.2 既有记录承载审核事实

Reviewer 初始 Frame 复用 `WAITING_CHILDREN`，等待 Specialist 终态。它最多创建一个
logical_step=`composite.review/v1` 的 `model_dispatch_attempts`；Reviewer outcome 继续只写
既有 MODEL_REQUEST、MODEL_RESULT、model_usage、Frame 与 RunEvent。Provider 输出先由
Core 严格解码、校验并重建 `ReviewVerdictV1`；成功 MODEL_RESULT 中
`ModelGenerateOutputV1.assistant_text` 必须逐字节等于 canonical Verdict。Provider 返回的
非 canonical 键序或空白不作为成功终态的权威字节保留；Store 不复制 Verdict，不新增
`REVIEW_VERDICT` ContentKind、gate event 或 permit row。

`CompositeSpecialistResultSetV1` 从 Root plan 与 Child terminal records 规范派生，正文仍归
各 Child MODEL_RESULT。Reviewer Verdict 的 family digest、result-set digest、issue/slot
集合和边界按 Runtime 第 18.3 节严格恢复；无法证明即 integrity failure，不能静默降级。

### 21.3 原子 permit 与终态

`BeginModelDispatch` 对三类 family member 使用同一事务门禁：Specialist 保持原规则；
Reviewer 必须证明全部 Specialist SUCCEEDED；Root merge 在写 PENDING 前必须证明 Reviewer
terminal ResultRef 精确闭合且 Verdict=`APPROVE`。cap 在 disabled/enabled 时分别为 N+1/N+2，
PENDING 与 UNKNOWN 均占 slot且不退款。REJECT、FAILED、INVALID、UNKNOWN 或缺失 Reviewer
事实都禁止 merge Attempt。

Reviewer PENDING 的启动/关停恢复仍只转原 Attempt 为 MODEL_UNKNOWN；UNKNOWN 对账只更新
原 Attempt。Provider 已返回但终态事务失败时不得从进程缓存重建 Verdict。family cancel
必须覆盖 Reviewer，Reviewer/Root 的 lease 与 Scheduler 三层上限继续使用现有表与 fencing。

Reviewer terminal `APPROVE` 与 Root merge 是两个可分别持久化、调度和恢复的状态迁移。
Reviewer 已 APPROVE、Root 仍为 `WAITING_CHILDREN` 且尚无 merge Attempt 是合法中间态；
它可以被 snapshot、verify、restore 并在之后取得 Root lease。Store 只强制反向蕴含：
一旦存在 `composite.merge/v1` Attempt，就必须在创建它的同一事务中重新证明 exact
Reviewer APPROVE。

### 21.4 读取、备份与 Schema

Root Loop projection 增加 optional Reviewer terminal view；Reviewer projection复用 Root
冻结 plan 读取 ordered Specialist results。所有返回字节必须 defensive copy。完整 semantic
closure 与 backup/verify/restore 必须拒绝：orphan/重复 Reviewer、Control/Manifest/ref 漂移、
第二 review Attempt、非法 Verdict、UNKNOWN 后出现 merge、REJECT/FAILED/INVALID 后出现
merge、merge 缺 exact APPROVE/closure、cap 超限或 cancel latch 缺失；不得拒绝
APPROVE 已持久化但 Root 尚未 merge 的合法中间态。

S3-B 验收时没有改变当时的 `0001_current.sql`：ordinary table count 保持 `20`，
fingerprint 为
`098bdf44ee765727dc41f84ee3a8af65069691226d815cde882bfa45a9ff6409`，migration 为
`29575` bytes / SHA-256
`98c2f1e520bcecb1b3777bcab6014c24d34d110cb6dc751d5bd93eee84e293ce`。这是 S3-B 历史
验收快照，不得冒充当前 schema；W5-F1 当时的 identity 由 §24/§25 保留，W2-R1 后的当前
24 表数值以 §3.3 与 §26.1 为准。后续若发现必须
修改物理 Schema，必须先回到两份正式规格重新冻结，不能隐式增加表或 runtime migration。

### 21.5 原始设计影响

新增状态完全属于现有 family Run/Attempt/Content/Event 图。Reviewer 仍由 Control 自由选择
精确 Agent/Profile，可外挂只读 Role/Skill/RAG，但不获得额外系统权限；关闭时 Pure Chat、
普通 Composite 与 Agent/Workspace 自由装配路径保持原字节和零访问。

### 21.6 S3-B 历史实现与开发切片证据

Store 测试覆盖 optional Reviewer whole-family 原子 Admission、确定性 ref closure、全部
Specialist 成功前无 review permit、APPROVE 前无 merge permit、REJECT/FAILED/INVALID
attempt-free Root 终态、UNKNOWN 零重放、N+2 cap、取消传播、family Usage 顺序，以及公平
Scheduler 只在依赖闭合后 claim Reviewer/Root。Reviewer-disabled 的行、query 与 canonical
bytes 保持原路径。

完整 semantic closure 与 backup/verify/restore 覆盖 enabled/disabled、canonical Verdict、
APPROVE 后尚未 merge、APPROVE merge、REJECT/FAILED/INVALID/UNKNOWN 无 merge、第二 review
Attempt、关系/ref/digest/cap/cancel 篡改拒绝。受影响包的 Windows 测试与 `go vet`、
Linux/WSL 同范围 Race 已通过。

该状态只验收 S3-B 本地开发切片。S3-C 真实模型效果、S3-D 全仓 Release、全仓 Linux Race、
六平台构建、Public Stage、最终 seal、首次正式部署、动态任务图、返工循环、Learning
产品能力及 Review/Version/周期/补偿与跨 Workspace 内容传递在 S3-B 当时均未完成；随后
W4-L1A/L1B Proposal Store 与 W4-L2 有界 Review 已验收开发切片，完整 Learning 产品
能力仍未完成。

## 22. W1 Conversation 第一开发切片

状态：`W1_CONVERSATION_ACCEPTED_DEVELOPMENT_SLICE`。

本节同时锁定合同并记录已验收的**普通单 Agent Pure Chat**
Conversation 纵向切片。`0001_current.sql`、Store API、Assembly/Loop、Context Compiler、
CLI/HTTP 和 backup/verify/restore 已按下列不变量闭合：

1. 仅新增 `conversations` 一个 Core 表与 runs 三个 turn 投影；无 Session/history/summary/
   usage 第二表族，无第二 Store/Runtime/Loop。
2. Conversation 固定 Tenant/Principal/Workspace/Agent/Profile 稳定 ID；每 Run 继续冻结
   exact refs，新配置只影响新 Run；RunManifest ConversationTurn 额外冻结 PrincipalID，
   供备份/恢复验证 owner 且不得进入 prompt。
3. 创建为 revision 0/head NULL；turn Admission 与 Run 全闭包同事务 CAS，精确重入返回原
   Run，并发 sibling 恰有一个成功。
4. 只有已证明最终回答成功的 head 可继续；PENDING、MODEL_UNKNOWN/其他 UNKNOWN、FAILED、
   CANCELLED 全部 fail-closed，除显式新建 Conversation 外无自动旁路。
5. predecessor 链逐 turn 恢复完整 USER `TASK_INPUT` + 最终 ASSISTANT History pair；不复制
   predecessor `history_entries`，断链/漂移/非成功来源在 permit 前拒绝。
6. `<85%` 不摘要；`>=85% 且 <100%` 最多一次确定性摘要；初始 `>=100%` 逐轮移除达到
   恢复水位所需的最小完整最旧前缀，不删除持久原文。
7. PENDING/UNKNOWN/重入/重启复用原 compilation/request bytes，模型调用和 Context
   Compiler 均无语义重放。
8. 每 turn 的 token/cache/reasoning/cost 只来自原 Usage/Price facts，缺失保持 UNKNOWN；
   Conversation 不建第二账本。
9. backup/verify/restore 保真 head/turn/predecessor/summary/Attempt closure，Secret resolver
   调用为零；恢复后成功 head 可继续，失败或 UNKNOWN head 仍被封闭。
10. Pure Chat 仅增加 Core Conversation/Run/Content/History 必要读取；未装配的 RAG、Memory、
    Skill、MCP、Action、Channel、Persona 或 Team 仍保持零访问。

开发切片证据：

| 门禁 | 当前证据 |
|---|---|
| W1 当时 21 表与三投影 | `TestMigration0001CreatesExactlyCurrentStoreTables`、`TestSchemaFingerprintIsFrozenAndDetectsDrift`、`TestCommitConversationTurnAdmissionPublishesFirstTurnAndProjection` |
| 创建、固定 scope、exact retry 与并发 CAS | `TestCreateConversationReadAndExactRetry`、`TestCreateConversationRejectsSameIDWithDifferentFixedScope`、`TestCommitConversationTurnAdmissionExactRetryPrecedesCurrentAndHeadChecks`、`TestCommitConversationTurnAdmissionConcurrentCASHasOneWinner` |
| head 成功门与无分叉 | `TestCommitConversationTurnAdmissionRejectsStaleHeadWithoutRun`、`TestCommitConversationTurnAdmissionBlocksPendingPredecessor`、`TestConversationSemanticClosureAllowsUnknownHeadWithoutSuccessor`、`TestConversationSemanticClosureRejectsSuccessorAfterUnknownHead` |
| 完整 pair、85% 与 100% | `TestCompileV1ConversationHistoryPreservesCompletePairOrder`、`TestCompileV1ConversationAt85SummarizesOldestCompletePair`、`TestCompileV1ConversationAt100DropsMinimalCompletePairPrefix`、`TestChatServiceConversationContextThresholdsKeepPairsIndivisible` |
| 产品入口 | `TestRunConversationCommandsAndTwoTurnChat`、`TestPostConversationCreatesAndExactlyReusesConversation`、`TestPostChatCarriesExactConversationCASAndReturnsNewRevision`、`TestRunServeLoopbackAndGracefulShutdown`；HTTP create 首次 201、精确重试 200，服务重启后继续原 head |
| backup/restore 与语义篡改拒绝 | `TestConversationBundleRoundTripPreservesLinearClosureWithoutExecution`、`TestConversationSemanticVerifierRejectsBrokenClosure` |
| 50 轮本地长链 | `TestRunConversationFiftyTurnsAcrossReentryBackupRestore`：50 Run/Attempt/Usage，turn 25 backup→verify→restore 后续聊，turn 25/50 exact retry，最终 bundle，Action/Memory/Channel 零访问；Windows `21.964s`、WSL `15.594s` |
| 正常 OS 进程退出/重启 | `TestRunConversationFiftyTurnsAcrossNormalProcessRestart`：两个独立进程顺序完成 25+25 轮，最终 50 Run/Attempt/Usage，Action/Memory/Channel 零访问 |

CLI 已支持 `conversation-create`、`conversation-get` 和显式 revision/head 续转；loopback
`POST /v1/conversations` 只创建固定 scope 的 revision 0 Conversation，首次返回 201，
相同 ID/scope 精确重试返回同一记录和 200，scope 漂移冲突关闭；它不创建 Run 或调用模型。
`POST /v1/chat` 可续转该 Conversation 并返回新 revision。DeepSeek 正常 Provider 已通过显式
开关接入同一产品链，但上表 50 轮是确定性本地 Provider 测试，**真实 DeepSeek 2/50 轮尚未执行**。

因此当前只能标记 `W1_CONVERSATION_ACCEPTED_DEVELOPMENT_SLICE`，不等于 W1 complete、
首次发布或多 Agent Conversation 完成。`ConversationTurn` 与 Composite 仍互斥；Action/Channel
Conversation 没有得到本切片的成熟性声明。W1 全工作包验收需在真实 50 轮及对应
长链数据完成后另行裁决。

上述 HTTP 与正常进程恢复均复用现有 Store API、Conversation CAS、Usage 行和
唯一 writer；没有新增 Session、第二 Store/Loop/History/Usage，也没有让未装配的
Role/RAG/Memory/Skill/MCP/Action/Channel/Team 进入 Pure Chat。因此最初的轻量模块化设计
保持不变。

后续 W1 已由真实 DeepSeek 50 轮收口；该事实不改写本节第一开发切片的历史数字。
`TestW1PureChatUnifiedZeroOptionalModulesAcceptanceV1` 进一步用已安装、激活、Catalog-visible 但
未选择的 Knowledge、Memory、静态 Skill、MCP/Action 与 Channel Provider，集中证明只有冻结
Model 被解析，MODEL_REQUEST 无动态 Context/Action，Memory、Channel、Learning、Transfer、
Composite 和可选 Content 均零增量。

## 23. W4 Learning 合同、Proposal Store 与有界 Review

### 23.1 W4-L0 历史边界

W4-L0 只冻结纯 `learning-proposal/v1` 与 `learning-review-verdict/v1` canonical 合同。当时没有
Learning 表、ContentKind、writer、queue、worker、lease、Runtime 分支或 backup closure；
当时的 21 表及对应 fingerprint、migration SHA/size 只作 W1/W4-L0 历史证据，当前物理身份
只以 §3.3 为准。

L0 合同本身不授权 Learning 审核、拒绝、发布、安装、激活、Catalog 变更或扩权；
`learning-review-verdict/v1` 的存在也不表示已有 Learning Reviewer 消费者或审核状态机。

### 23.2 W4-L1A Knowledge Proposal Store

状态：`W4_L1A_KNOWLEDGE_PROPOSAL_STORE_ACCEPTED_DEVELOPMENT_SLICE`；Learning 产品能力仍为
`planned`。本切片只在唯一 Current
Store 加入第 22 张 `learning_proposals`，并只接纳 `KNOWLEDGE` 候选。本 L1A 历史切片中的
每条记录均为不可变 `SUBMITTED/0`；`created_at` 必须等于 `updated_at`，当时没有 update、
delete 或释放接口，也没有 Review terminal、Version、发布、Schedule 或补偿状态。

提交必须在单个 Store 事务内由 Core 重新构造 `learning-proposal/v1`，而不是信任调用方
自报 ProposalID、fingerprint 或 digest。原始 `SourceEvidenceV1` 只用于 admission-time
派生，URL、路径、凭据、原始来源材料及 origin/revision material 不得持久化；SQL 只保存
Proposal canonical、exact Draft canonical 和可重算投影。

相同 ProposalID、Proposal canonical 与 Draft canonical 的 exact retry 返回原记录且不新增
事实；相同 ProposalID 但 canonical 事实变化属于 integrity failure。每个 Tenant 与 kind 内
同时冻结三条独立唯一轴：`source_fingerprint`、`content_fingerprint`、
`target_id + target_version`。任一轴已由其他 Proposal 占用时必须拒绝且零写入；当前接口不
提供删除或释放来绕过去重。

proposer lineage 必须闭合到同一 Tenant/Workspace 的唯一普通 one-Member Run、exact
RunManifest、Member snapshot、Agent/Profile ref，以及该 Member 唯一成功终态 Model
Attempt 的 exact `MODEL_RESULT`。PENDING、MODEL_UNKNOWN、FAILED、Action 结果、其他 Run
或不一致的 Manifest/Member/ResultRef 均不得成为 Proposal 来源。

读回和统一语义门禁必须严格恢复 Proposal/Draft，重算 ProposalID、source/content
fingerprint、DraftDigest 与 target ref，并证明所有 SQL 投影和 proposer Attempt ID 与
canonical lineage 完全一致。结构 FK、UNIQUE 或数据库文件摘要不能替代该语义证明。

`CreateBundle`、`VerifyBundle`、`RestoreBundle` 与 Current Store 启动门禁复用同一个只读
Learning 语义校验器；它们只复制和验证同一 SQLite 中的候选事实，不调用模型、Module、
Knowledge Provider、网络、Secret resolver 或外部效果，也不增加第二 Store、bundle 文件族
或第二事实源。

Proposal 只是一份待治理候选，不授予 Trust、Authority、Secret、审核、发布、安装、激活或
Catalog 变更能力。Knowledge Draft 的可见范围仍只是请求，不能绕过本地 Authority 裁剪。
Pure Chat、Assembly Compiler、Universal Loop、Port、Gateway 与 Worker 在 L1A 当时均无
Learning 入口；第 22 表及其备份闭包不能被解释为 Learning Reviewer、Version、Schedule、
补偿或产品能力已经可用。后续 W4-L2 变化只以 §23.4 为准。

### 23.3 W4-L1B 静态 Skill Draft Store

状态：`W4_L1B_STATIC_SKILL_PROPOSAL_STORE_ACCEPTED_DEVELOPMENT_SLICE`；完整 Learning 产品
能力仍为 `planned`。本切片不新增表，只把同一 `learning_proposals` 的 `proposal_kind` 约束
扩为 `KNOWLEDGE | SKILL`，并让两个窄 Store 入口复用同一 Admission、lineage、exact retry、
三轴去重和语义门禁。本 L1B 历史切片当时仍为 22 张 ordinary table；schema fingerprint 为
`18722b0c8a18e9f6563e65cf30d0ebc50b723aa6f6465aa4b95d83ba001bc422`，migration 为
`34,592` bytes / SHA-256
`75b7fe488932e0f562c84073a659c9803ffcd4bfdb4efa500b19720c7648c47f`。§23.2 的 L1A 数值继续
只是对应历史树证据；W5-F1 验收时的物理身份由 §24.1 与 §25.4 保留，W2-R1 后的当前
identity 只以 §3.3 与 §26.1 为准。

静态 Skill Draft 必须是现有 `static-context/v1` 的 exact canonical bytes；不增加 Skill wire、
Role 推断、Skill Runtime 或专用 Port。Store 在复制、解析和开启事务前要求完整 Draft canonical
为 1 至 1 MiB，并继续让 L0 合同严格恢复 schema、UTF-8/NFC 文本、DraftDigest 与独立域的
ContentFingerprint。超限、非 canonical、错误 kind 或 Knowledge Draft 冒充 Skill 均以
admission error 拒绝，不泄露为 SQLite CHECK 错误。

每个 Tenant 与 kind 内仍分别冻结 source、content、target-version 三条唯一轴；Knowledge 与
Skill 的跨 kind 候选相互隔离。`moduleapi.Ref` 在真正物化时仍必须服从全局模块版本、既有
Installation 与 Operator Apply/Catalog CAS；该发布冲突门禁属于 W4-L3，L1B 不提前物化、保留
版本或授予发布权。

Skill Proposal 在 L1B 历史切片中仍只有不可变 `SUBMITTED/0`。提交不会 PutContent、Install、Activate、Bind、
改 Control/Catalog、调用模型、进入 Context Compiler 或获得 Trust/Authority/Secret。完整
Backup 继续复制同一 SQLite，并由统一 Learning semantic gate 严格恢复两类 Proposal；混合
Knowledge+Skill 的 create→verify→restore→reopen 及 kind、Draft、target 投影篡改拒绝已经闭合，
没有新增 bundle 文件族或 verifier 旁路。

因此 L1B 只增强受治理的可选 Skill Draft 入口，不增加第二 Runtime、Store、Loop、Gateway、
Catalog pointer、Worker 或外部效果账本，也不改变 Pure Chat 零可选模块访问、轻量 Agent、共享
RAG、Workspace 隔离或 UNKNOWN 禁止语义重放。后续 L2 的审核状态不能回写或扩大这项已验收
L1B 裁决。

### 23.4 W4-L2 Learning Review 增量验收（2026-08-09）

历史状态：`W4_L2_LEARNING_REVIEW_ACCEPTED_DEVELOPMENT_SLICE / W4_L3_NEXT`。本节只验收一次
有界 Learning Review 纵链。本 L2 切片当时仍为 22 张 ordinary table；其历史 schema fingerprint、
migration SHA-256 与 size 只以 §3.3 明确标注的 W4-L2 历史值为准；W5-F1 验收时的物理身份由
§24.1 和 §25.4 保留，W2-R1 后的当前 identity 只以 §3.3 与 §26.1 为准。L2 不新增 Review 表、Runtime、Loop、Gateway、Port、
ContentKind、Worker、Queue、定时器或 Usage 账本。

`learning-review-request/v1` 是现有 `task-input/v1` 的 Text 值，而不是新的 ContentKind。它
完整嵌入 exact Proposal canonical 与 Draft canonical，并绑定 ProposalID、source/content
fingerprint、DraftDigest、固定 `PROPOSAL_GATE` 规则、固定模型指令、
`learning-review-verdict/v1` 输出版本和 `max_output_tokens`。ReviewRequest wire 上限为
96 KiB；进入审核的完整 Draft 上限为 64 KiB，Proposal Store 的 1 MiB Draft 保存上限不变。
超限候选必须保持 `SUBMITTED/0`；不得截断、摘要、Drop、分块或只给摘要后冒充完整审核。

Reviewer 必须由受信 Operator/Control 显式选择。它与 proposer 必须同 Tenant、同 exact
Workspace，但 Reviewer RunID、Member、Agent logical ID 和 Profile logical ID 均不同；只改
version 或 digest 不能绕过独立性检查。Reviewer 是一个普通、非 Conversation、非 Composite、
非 Channel 的 one-Member Run，只允许一个 required `model.generate/v2` Binding，无 Action，
冻结参数必须显式满足 `1 <= max_tokens <= 1024`。跨 Workspace 审核、自动选模、动态 RAG/
Memory、Action 或其他副作用 Port 均不属于 L2。

`learning_proposals` 只新增两个 nullable unique 引用并扩展 Store-owned 状态投影：

| state/revision | `review_run_id` | `reviewer_attempt_id` | 含义 |
|---|---|---|---|
| `SUBMITTED/0` | NULL | NULL | 仅 Proposal；尚未绑定审核 |
| `REVIEW_PENDING/1` | exact Reviewer Run | NULL | Run 已原子发布；可处于无 Attempt、PENDING 或终态待投影窗口 |
| `APPROVED/2`、`REJECTED/2`、`REVIEW_FAILED/2` | exact Reviewer Run | exact original Attempt | 从唯一普通 Attempt 与结果派生的首次终态 |
| `REVIEW_UNKNOWN/2` | exact Reviewer Run | exact original Attempt | 原 Attempt 为 `MODEL_UNKNOWN`；禁止语义重放 |
| `APPROVED/3`、`REJECTED/3`、`REVIEW_FAILED/3` | exact Reviewer Run | 同一 original Attempt | 仅由该 UNKNOWN Attempt 的可靠 canonical reconciliation evidence 收口 |

Reviewer Run 的发布与 `SUBMITTED/0 → REVIEW_PENDING/1` 必须在同一 `BEGIN IMMEDIATE`
事务完成。同一 Proposal 只能绑定一个 Reviewer Run；同一 exact Admission 重入返回原事实，
不同 Reviewer 并发竞争只有一个赢家。`REVIEW_PENDING` 的恢复只读取或继续原 Run/Attempt，
不得建立替代审核。

唯一 Universal Loop 继续执行普通 Model Attempt，并使用既有 `model_dispatch_attempts`、
`model_usage`、`MODEL_RESULT`、Run terminal 与 `MODEL_UNKNOWN` 语义。Store-only
`FinalizeLearningReview` 不接受调用方自报 Attempt、Verdict 或状态：

- 合法 `APPROVE`/`REJECT` Verdict 分别派生 `APPROVED`/`REJECTED`；
- Provider FAILED 派生 `REVIEW_FAILED`；
- Provider 成功但产生 ActionRequest、非单一严格 Verdict 或不匹配 Proposal 身份时仍保留真实
  SUCCEEDED Attempt、MODEL_RESULT 与 Usage，并把 Proposal 投影为 `REVIEW_FAILED`；
- `MODEL_UNKNOWN` 派生 `REVIEW_UNKNOWN`，只能对账同一 Attempt；禁止新 Run、新 Attempt、
  换 Reviewer、换 Provider 或自动重放。

统一 Learning semantic gate 与 Backup/Verify/Restore 必须重建 exact Review TaskInput，验证
Reviewer 独立性、唯一 Run/Attempt、Usage/Result/Verdict、状态/revision 和 UNKNOWN evidence
闭包。Proposal、Draft、Reviewer Run/Member、Attempt、Result、Verdict、状态、引用或时间被
篡改均 fail-closed；source/content/target 三轴在全部审核状态继续占用，`REJECTED` 不释放来源。

`internal/localchat.LearningReviewService` 是显式构造的可选 application workflow；普通
ChatService 不会隐式启用它。opt-in `scripts/Invoke-W4L2LiveLearningReview.ps1` 只执行真实
DeepSeek 验证测试，要求 Reviewer Attempt `SUCCEEDED`、Proposal `APPROVED|REJECTED`、Usage
含 input/output token 且 exact retry 零重放。2026-08-09 的真实验收运行 PASS（4.08s）：Proposal
`APPROVED` revision 2，Reviewer Attempt `SUCCEEDED`；Usage 为 `PROVIDER_REPORTED`，input 1151、
cached 0、uncached 1151、output 227、reasoning UNKNOWN，估算费用 0.001605 CNY；exact retry
未创建新 Run/Attempt 或重放。runner 使用临时 Store/artifact 和进程环境凭据，不构成公开
CLI/HTTP 或自动审核入口。

本切片全仓 `go test -count=1 ./...` 通过（168.4s），`go vet ./...`、`go mod verify`、Docs
（38 份 Markdown）、License（35 个 Go dependency/55 个 distributed asset）、Branding 与
PublicTree 门禁通过。完整 Backup create→verify→restore→reopen 覆盖全部审核状态和篡改拒绝；
精确重入只返回原事实，UNKNOWN 仍只能对账原 Attempt。

L2 的 `APPROVED` 只是一项审核结论，不是 Trust、Authority、Secret、Version、发布、安装、
激活、Binding 或 Catalog grant。本切片没有产品级自动 Admission、自动 Reviewer、不可变
Version、Operator 发布衔接、默认 24 小时周期、补偿扫描或自动决策；这些能力继续保持
`planned`。未调用 Learning 时 Pure Chat 继续零访问，Agent/Workspace 与共享 RAG 的模块化边界
不变。

### 23.5 W4-L3 不可变 Version 与 Operator 交接增量验收（2026-08-09）

历史状态：`W4_L3_LEARNING_VERSION_ACCEPTED_DEVELOPMENT_SLICE / W4_L4_NEXT`。L3 不增加第 23 张
表；它只把审核通过的同一 Proposal 行投影为无权限、可复算的一对一 Version。本 L3 历史物理
identity 为：

```text
ordinary tables = 22
schema fingerprint = a744ea62d0ead80b5eb0669409f0ab4cd0d297b662e67181b0a88f760b2c5024
migration bytes = 37810
migration SHA-256 = 025acc044f53a71c674d5963c171806e87095911025a129f65b4be33a0601070
```

`learning_proposals` 新增以下七项 all-or-none 投影：

```text
version_canonical
version_size_bytes
version_id
artifact_digest
artifact_size_bytes
approval_verdict_digest
materialized_at
```

七项必须全 NULL 或全非 NULL；严格 scanner 以 `0/7` 判断 absent/present，任何 `1..6/7` 形状
均为完整性错误。present 时 Proposal 必须为 `APPROVED/2` 或 `APPROVED/3`，Version canonical、
独立域 VersionID、Proposal/Draft、Reviewer Run/Attempt、exact APPROVE Verdict、SQL 摘要/大小与
时间必须全部重建一致。`materialized_at` 必须为正且不早于 Proposal `updated_at`；精确重入返回
原 Version 和原时间，不更新 review state/revision。

唯一 wire 为 `learning-materialized-version/v1`，只含 ProposalID/revision、Reviewer Run/Attempt、
APPROVE Verdict digest、Tenant/kind、source/content/draft fingerprint、target ModuleRef 与
ArtifactDigest/size。它不含 Profile、Instance、Binding、Config、Trust、Authority、Effect、
Secret、Catalog revision 或发布许可。只允许：

```text
Knowledge: module.yaml + content/source.json
  TRUSTED_IN_PROCESS request / go-in-process/v1 / context.provide/v1

Skill: module.yaml + content/context.json
  DECLARATIVE request / static/v1 / context.provide/v1
```

Manifest 不声明 Requires、requested permissions 或可选生命周期能力。ArtifactDigest 只覆盖 exact
canonical Manifest 与 Proposal 中已保存的 exact Draft；ArtifactSize 只计算这两份字节，不包含
时间、路径、文件模式或交接元数据。

同一 Module ID + exact version 是跨 Tenant、跨 Knowledge/Skill 的全局不可变身份。Materialize
在同一 `BEGIN IMMEDIATE` 中检查已有 Installation；`InstallModule` 也在写任何 Manifest content
前对称检查已有 Version。两者无论谁先创建，只有 canonical Manifest 与 ArtifactDigest 完全相同
才可共存；其他情况冲突且零写。相同 Proposal/revision 精确重入零写，并发物化只有一个 creator。

产品入口 `learning-materialize` 先持久化或读取 Version，关闭 Store 后再重建 exact artifact，并
导出到与 Current Store 和活动 artifact root 分离的 Operator-owned 临时目录。输出路径在物化前后
均重新解析和隔离检查；exporter 还拒绝 symlink/reparse parent、root/file symlink、hardlink、
特殊文件、超限文件、额外路径、digest/size/逐字节漂移。发布使用同父目录私有 staging、逐文件
sync、完整复验和 no-replace；已有只读目标只有 exact 相同时才返回 `created=false`，文件模式不
参与 identity。导出失败不回滚已持久化 Version，Operator 可修正路径后按同一 revision 精确重试。

该命令不生成 `module-apply-plan/v1`。Profile、Instance、exact Port、port binding index、
`expected_runtime_request`、Config、Authority、FailurePolicy 与 expected pointer revision 必须由
Operator 依据当前 Control/Catalog 另行选择，并先运行现有 `module-dry-run`。L3 不调用
InstallModule、ActivateModule、PublishControlCatalog、模型、
网络、Gateway、Secret resolver 或 Module Provider，也没有可语义重放的 L3 UNKNOWN。

完整 Backup 只把 Proposal、Review、Version canonical 与 Draft 作为未安装 Version 的权威闭包；
临时 handoff 不进入 bundle，活动 artifact root 也不要求存在该 digest。Knowledge 与 Skill 的
create→verify→restore→reopen 已证明 Version 全字段、时间和 canonical bytes 精确保留，恢复后可由
产品命令重新导出相同 Manifest/Payload/entrypoint/digest/size；精确重试不创建第二 Version 或
artifact。若 Operator 后续显式 Apply，已安装 artifact 继续进入既有 Installation backup 闭包。

定向验收覆盖确定性 canary、所有非 APPROVED 状态、defensive copy、exact retry、并发单写、跨
Tenant/kind 全局冲突、Installation 双向相容/冲突/并发、0/7 与 chronology 篡改、安全导出、
`module-verify/module-dry-run` 零写，以及 Backup/Restore/产品重新导出。W4-L3 没有改变 Pure Chat、
轻量 Agent、共享 RAG 或统一外挂设计；自动 Apply、24 小时周期、可选报告与补偿扫描仍属于 W4-L4。

后续专属验收 `TestW4ApprovedLearningMaterializedSkillApplyIsConsumedOnlyByNewRun` 又闭合了产品
交接后的显式纵链：stale pointer 失败且零部分发布，正确 Operator Apply 才建立 Installation/
Activation/Binding；旧 Run 的 Member bytes/digest 不变，新 Run 冻结 exact Version/Artifact/
Catalog 并实际消费审批后的静态 Skill。Binding 仍为 REQUIRED、deny-all Authority，ExecutionClass/
Adapter identity 由本地策略授予。该证据不允许 Learning 自动 Apply。

### 23.6 W4-L4 Learning Cycle 增量验收（2026-08-09）

历史状态（W4-L4 验收当时）：`W4_L4_LEARNING_CYCLE_ACCEPTED_DEVELOPMENT_SLICE /
W4_COMPLETE_ACCEPTED_DEVELOPMENT_SLICE / W5_NEXT`。L4 采用“持久化周期策略 + 不可变逻辑窗口
Task + 普通 proposer Run + Store-only 收口”的最小纵链，只新增第 23、24 张
`learning_cycle_schedules` 与 `learning_cycle_tasks`：

```text
ordinary tables = 24
schema fingerprint = 9941c957b0e6a1a1e1770b38cd06605025d12df30067d1271fe8f7b30f2f0518
migration bytes = 44138
migration SHA-256 = 4cf260d368fb7b69e0a86dad99ff593bea216e9d0da341f607d000999e076be4
```

W4-L3 的 22 表 identity 继续按 §3.3/§23.5 作为历史证据保留；新二进制只接受上述 24 表
identity。没有第二 Store、Runtime、Loop、Gateway、Queue、报告表、文件 sidecar、常驻后台
Worker 或第二定时系统。

纯合同新增 `learning-cycle-schedule/v1`、`learning-cycle-request/v1`、
`learning-cycle-result/v1` 与 `learning-cycle-report/v1`。Schedule canonical 冻结无 Secret 的
Tenant、service principal、Workspace/Agent/Profile logical ID、Knowledge/Skill kind、目标
Module ID、objective、Knowledge 可见范围请求、首次到期时间、周期与模型输出上限；创建后不可变，
修改策略必须创建新 ScheduleID。周期未指定时规范化为 `86,400` 秒；新 Schedule 总是
`enabled=false, revision=0`，启用/禁用只改变 Store-owned policy bit 并要求 exact revision CAS。

Tick 必须由产品命令显式提供 exact Tenant、Schedule 与微秒精度 UTC `observed-at`。Pure Chat、
Store open、Backup/Restore、启动和关停恢复不会自动扫描 Schedule。到期前零写；时钟回拨不回退
水位；错过多个周期只合并为最新到期窗口，不突发补跑多个模型。同一 Schedule 同时最多一个
`PENDING|RUN_ADMITTED`；`(tenant_id,schedule_id,scheduled_for)` 唯一。TaskID 由 Schedule digest、
逻辑窗口和 exact request digest 确定性派生；`PENDING/0` 与水位在同一 `BEGIN IMMEDIATE` 创建，
普通 Run 发布和 `PENDING/0 → RUN_ADMITTED/1` 位于另一原子 Admission 事务。

v1 proposer 是 standalone、single-member、非 Conversation/Composite/Channel 的普通 model-only
Run，只允许一个 required `model.generate/v2`，固定一次有界模型步骤；Action、MCP、Channel、
动态 RAG/Memory 与其他 Port 均禁止。模型结果只接受严格且恰好五字段的
`learning-cycle-result/v1`：`PROPOSE|NO_CHANGE` 必须精确绑定 request digest，Knowledge 与 Skill
两个 union arm 的未使用字段仍必须显式为空。模型不能自报 Tenant、Workspace、Agent、Profile、
Target、Trust、Authority、Secret 或发布许可。

本地代码从合法 `PROPOSE` 重建既有 `knowledge-source/v1` 或 `static-context/v1`，再调用唯一
Proposal Admission；它不自动 Review、Materialize、Install、Activate、Bind、Apply、改 Catalog
或扩权。Task 状态严格为：

```text
PENDING/0 -> RUN_ADMITTED/1
RUN_ADMITTED/1 -> PROPOSAL_SUBMITTED | NO_CHANGE |
  SOURCE_OCCUPIED | CONTENT_OCCUPIED | TARGET_OCCUPIED |
  FAILED | INVALID_RESULT | UNKNOWN
```

首次终态为 revision 2。`UNKNOWN/2` 只允许使用同一原 Attempt 的可靠 reconciliation evidence
前进到非 UNKNOWN revision 3；禁止新 Run、新 Attempt、换 Agent/Profile/Provider 或语义重放。
Provider 成功但 wire 非严格合法时，原 SUCCEEDED Attempt、MODEL_RESULT 与 Usage 保真，Task 只
投影为 `INVALID_RESULT`，不能把无效正文当作 Proposal。

Store-only `ReconcileLearningCycleTasks` 只扫描 exact Tenant/Schedule 下的
`PENDING|RUN_ADMITTED|UNKNOWN`，输出 `PENDING_UNADMITTED|OPEN_NOT_READY|FINALIZED|EXACT_TERMINAL`。
它只调用 Store 派生 Finalize，不创建或执行 Run/Attempt，不调用 Loop、Provider、Action、MCP、
Channel、Gateway、Secret resolver 或网络。`BuildLearningCycleReport` 生成指定 Schedule 和
半开时间窗 `[start,end)` 的确定性 `learning-cycle-report/v1`，按逻辑窗口/TaskID 排序，只包含
Task、Run、state 与 Proposal 引用；报告无 generated-at、不持久化、不用模型总结、不写文件、
不发 Channel。两项操作均以 1024 条为上限并探测第 1025 条，超限明确失败而不是截断。

CLI 已提供 `learning-cycle-schedule-create/get/enable/disable`、显式
`learning-cycle-tick`、Store-only `learning-cycle-reconcile` 与只读 `learning-cycle-report`。
只有 Tick 构造 production composition；其余命令只打开 Current Store。Tick/reconcile 输出脱敏
Schedule/Task/Run/Attempt/Proposal 引用和可用的同 Attempt Usage，不输出 objective、request/result
或模型正文；未知 token 继续编码为 JSON `null`。

完整 Backup create→verify→restore→reopen 已覆盖 Schedule enabled/revision/水位、开放、完成、失败、
INVALID 与 UNKNOWN Task、普通 Run/Attempt/Usage/Result/Proposal closure。Restore/Open 不隐式 Tick；
reconcile 对 UNKNOWN 只收口同一 Attempt。协调篡改、off-grid 水位、非法 state/revision/ref、第二
open Task 与孤立执行事实均由 semantic gate 拒绝。report 本身不持久化、不进入 bundle。

首次真实 DeepSeek canary 的模型调用成功，但返回 wire 不满足严格五字段 union，因此按设计闭合为
`INVALID_RESULT`；该证据保留为严格合同发现。固定指令 skeleton 与纯陈述 objective 后进行唯一一次
修正重跑，Task 闭合为 `PROPOSAL_SUBMITTED`，Usage 为 `PROVIDER_REPORTED`：input 614、output 90，
冻结估算费用 `0.000794 CNY`；全链恰好一个 Run、一个 Model Attempt，同窗口 retry 为零语义重放。
临时凭据、Store、artifact 与模型正文均未进入源码、配置或本文。

W4 complete 表示计划内 Proposal、独立 Review、无权限 Version 与显式 Learning Cycle 开发切片均已
收口；在该历史验收点，下一工作包标记为 `W5_NEXT`。它不代表后台 Worker、自动
Review/Materialize/Apply、主动外部
检索、生产部署或公开 Beta；Agent/Workspace 继续轻量，Knowledge/Skill 继续作为可选共享模块，
Pure Chat 未显式构造 Learning composition 时保持零 Schedule/Task 访问。

## 24. W5-D1 Decision family 与 W5-X1 Workspace Transfer 冻结规格

当时状态：`W5_X1_WORKSPACE_TRANSFER_ACCEPTED_DEVELOPMENT_SLICE / W5_F1_NEXT`。本节在 X1
验收时覆盖并取代本文较早章节中仅适用于 S2/S3 历史切片的“当前同 Workspace”、
“N+1/N+2 是唯一 family cap”和“Transfer 尚未实现”等表述；那些段落的历史事实与当时验收
数字继续保留。后续 F1 只以 §25 增量收紧稳定性，不回填本节状态、canary 或验收数字。

### 24.1 Store identity 与唯一事实源

W5-X1 没有新增表、writer、队列、Transfer Ledger、Runtime、Store、Loop 或 Gateway。当前唯一
可写 Store 仍为 24 张 ordinary table；只在现有 `content_records` allowlist 中加入
`WORKSPACE_TRANSFER_PAYLOAD` 与 `WORKSPACE_TRANSFER_ENVELOPE`：

```text
ordinary tables = 24
schema fingerprint = 98d658e68907f77516ce0366a7b08bfa3851600326850590ed79584f581fa588
migration bytes = 44215
migration SHA-256 = 0a6701405e471e1ee7f41ea89ed62b50e5a4723e71b320e50f073cdd3e6b727a
```

Decision/Transfer 的权威恢复事实仍由原 `run_manifests`、`runs`、`content_records`、
`model_dispatch_attempts`、`model_usage`、Frame/Event 与 terminal MODEL_RESULT 组成。Transfer
payload/envelope 是不可变 ContentRecord；它们不是可消费队列、可变 current pointer 或第二外部
效果账本。

### 24.2 完整物理 family 与 Usage 投影

含 N 个 Specialist 的 W5 Decision family 必须在一次 Admission 中预发布完整 `2N+3` 物理图：

```text
Initial Children[0..N-1]
  → Reviewer0
  → Repair Children[0..N-1]
  → Reviewer1
  → Root
```

模型只能决定 `APPROVE | REJECT | REPAIR_REQUIRED` 及 repair affected slots，不能创建 Run、改变
图或申请第二次 repair。未激活或明确 skipped 的 repair Run 继续作为冻结恢复事实存在，且
`Attempt=nil`；subset repair 只允许受影响 slot 创建 Attempt，all repair 才允许全部 repair
Specialist 创建 Attempt。Reviewer1 只有在至少一个 repair contribution 闭合后才可运行。

`GetCompositeFamilyUsageProjection` 必须按上述物理顺序返回全部 Run，实际存在的初始、Reviewer、
repair 与 Root Attempt 全部进入 `AttemptSlotsUsed`、token/cost aggregate 和 `2N+3` cap。投影必须
严格验证 repair round、Root/Parent、物理 ParentSlotID、AdmissionKey、MemberSnapshot、
Agent/Profile、TaskInput、Assignment 与 logical step；Decision 初始/repair Specialist 使用冻结
Pure Chat step，Reviewer/Root 分别使用冻结 review/merge step。dormant/skipped Run 不得被省略，
也不得以零 Usage 冒充 Attempt。

### 24.3 单 Run Workspace 与双边 grant

整个 family 必须同 Tenant，每个 Run 仍只绑定一个 Workspace。Root、Reviewer0、Reviewer1 留在
Root Workspace；初始/repair Child 可以按 Root plan 绑定另一 Workspace。同 Workspace Child 不得
伪造 Transfer plan；跨 Workspace Child 必须在发布时冻结一对独立 owner grant 及其 exact digest：

- REQUEST：Root grant 必须允许向 Child Workspace 发送 `TASK_SUMMARY`，Child grant 必须允许接收；
- RESULT：Child grant 必须允许向 Root Workspace 发送 `SPECIALIST_RESULT`，Root grant 必须允许接收；
- 两个方向分别应用 source send、target receive 的 payload kind 与 byte ceiling 交集，任一方向不
  完整则整个 Admission 零写失败；
- repair Child 必须复用对应 logical slot 的相同 Workspace 与双边 grant 计划，不得漂移或重路由。

已发布 Run 所引用的 historical grant 是不可变恢复事实。恢复、重入和 exact retry 必须使用该
历史 grant；当前 Control 只提供 deny-only 撤权门，撤权可阻止尚未发生的新 transfer，但不能替换
历史 grant、改写 Manifest、迁移 Workspace 或创建替代 Run。

### 24.4 Payload、编译与持久化边界

W5-X1 的 payload kind 闭集只有：

```text
TASK_SUMMARY
SPECIALIST_RESULT
```

REQUEST 只能由 trusted compiler 在调用边界从 Store-loaded exact `TASK_INPUT` 和可选 repair basis
生成 `workspace-task-summary/v1`。repair basis 必须闭合上一 contribution set 与 Reviewer verdict；
不得从调用方正文、模型自报 identity 或 current Control 拼装。系统不得跨 Workspace 传递完整
History、Memory、Knowledge/RAG 正文、Secret、SecretRef 或任意文件。

RESULT envelope 不复制结果正文，必须精确闭合同一 Child 的真实 terminal
`MODEL_RESULT → specialist-contribution/v1`，并验证 direction、Root/Child/slot、TaskInput、grant、
payload digest/size/schema 与物理 repair slot。跨 Workspace evidence 只进入受控 Context
Compilation；模型不得选择、伪造或执行 Transfer。

REQUEST payload/envelope 与原模型 `PENDING` Attempt 在同一 Begin 事务提交；事务失败全部回滚。
成功 outcome 的 RESULT envelope 与原 terminal MODEL_RESULT/Usage 在同一权威事务闭合。过期、
FAILED、UNKNOWN 或非法输出不得伪造 RESULT envelope。`MODEL_UNKNOWN` 及其他 UNKNOWN 只能对账
原 Attempt，禁止语义重放、换 Provider/Binding、重新生成 REQUEST 或创建替代 Run。

### 24.5 Backup、恢复与验收证据

完整 backup/verify/restore 必须包含并双向验证：完整 `2N+3` family、dormant/skipped repair
状态、初始与 repair 的 Transfer plan、历史 grant canonical/digest、REQUEST/RESULT payload 与
envelope、terminal MODEL_RESULT、Usage、Frame/Event 和 cancellation。restore/open 不能读取当前
Control 重路由、调用 Provider/Secret resolver、生成新 envelope 或发送网络。

实现证据包括 Decision 原子 Admission/repair transition/UNKNOWN、完整 Usage 投影、跨 Workspace
双边授权、trusted summary、terminal result closure、exact retry、语义篡改拒绝及完整
backup/restore 测试。W5-X1D 还完成了一次真实 DeepSeek 开发验收：

```text
HTTP 2xx = 4/4
physical Runs = 7
Attempts = 4
exact retry model calls = 0
REQUEST verified = true
RESULT verified = true
input tokens = 3443
cached input tokens = 768
uncached input tokens = 2675
output tokens = 1353
reasoning tokens = UNKNOWN
estimated cost = CNY 0.00539636
provider-reported cost = UNKNOWN
reconciled cost = UNKNOWN
```

该记录只证明本次 W5-X1D 纵链的真实调用与持久化边界；不得外推为模型质量基准、稳定缓存率、
生产价格、生产 SLA 或整个 W5 完成。下一工作包为 `W5_F1_NEXT`。

## 25. W5-F1 Collaboration Stability 当前权威规格

状态：`W5_F1_COLLABORATION_STABILITY_ACCEPTED_DEVELOPMENT_SLICE / W5_COMPLETE_ACCEPTED_DEVELOPMENT_SLICE / W6_NEXT`。
本节只增量收紧 §24 的公平调度、稳定归位、取消、恢复、UNKNOWN 与权限边界；它不修改既有
canonical wire、`2N+3` family、Transfer 协议或 Store identity，也不改写 §24 的 X1D 历史证据。

### 25.1 唯一 runnable frontier 与公平 claim

W5 Decision family 的 runnable 集合必须且只能由 Store-loaded
`loadCompositeDecisionFrontier` 派生。预冻结但 dormant 或已 skipped 的 repair Specialist/
Reviewer 仍属于完整物理图、取消、Usage Run 投影和 Backup closure，但必须保持 `Attempt=nil`，
不得作为未终止 sibling 阻塞 Reviewer0，也不得进入公平 Scheduler 候选或获得 claim。

调度执行仍是唯一链：

```text
ClaimFairRun
  → BEGIN IMMEDIATE 内派生 Store frontier、检查三层 cap、取得 fenced lease、递增 served_units
  → RunClaimed
  → Universal Loop
```

三层 cap 继续是 `GlobalWorkers / MaxActivePerWorkspace / MaxActivePerFamily`。lease CAS、
`served_units`、取消 latch、Model/Dispatch PENDING 与 UNKNOWN 语义均不改变；不得建立第二套
Scheduler、Queue、Worker 或 runnable 投影。未启用 Decision 的 legacy Composite 继续使用原
sibling 终态规则与原调度路径，不能被 Decision frontier 改写。

单机、单 Store、tenant-bound 的确定性公平证据固定为：三个始终 runnable Workspace、`900`
次 claim、第 `450` 次后关闭并重开同一 Store；每个 Workspace 恰好服务 `300` 次，首次服务均
不晚于第 `3` 次 claim，最大服务间隔不超过 `3`。该证据不声明分布式公平、跨 Tenant 配额或
生产 SLA。

### 25.2 稳定顺序、取消与零 Transfer canary

Specialist 可以反向完成，但 contribution、Reviewer 输入和 Root merge 必须始终按冻结 plan
顺序归位。关闭并通过 `OpenExistingCurrentStore` 重开后，Run/Attempt/Result identity、排序和
canonical bytes 必须不变，不得按完成时序重新排序。

family cancellation 必须在原子事务中覆盖跨 Workspace 的完整 `2N+3` 物理 family。取消后任何
成员均不得 Begin 新 Model Attempt，并且不得生成 Transfer payload/envelope 或其他外部效果；
它不覆盖已经存在的 PENDING/UNKNOWN，也不放宽“Child 只能随 family 取消”的原边界。Pure Chat
和未启用 Transfer 的 ordinary Composite 必须保持零 Transfer grant 读取、零 Transfer
ContentRecord、零 payload/envelope 与零相关外部调用。

### 25.3 原 Attempt UNKNOWN 对账

Transfer `MODEL_UNKNOWN` 只允许以可靠、canonical reconciliation evidence 收口原 Attempt。
evidence 必须精确绑定原 Attempt、当前 Attempt revision、冻结 Provider/Binding 与可验证终态：

- `SUCCEEDED` 必须在同一权威 outcome 中生成且只生成一个 RESULT envelope；
- `FAILED` 必须闭合原 Attempt 且不得生成 RESULT；
- 无 evidence、stale revision、不同 Provider/Binding、不同 Attempt 或无法验证的 evidence 必须
  fail-closed、零部分写；
- exact Begin retry 只能返回原 REQUEST/Attempt，不取得 Provider 调用许可，不生成替代 Run、
  Attempt、Transfer 或 RESULT。

完整 Decision+Transfer backup 必须通过 create→verify→restore→
`OpenExistingCurrentStore`→family resolve。跨 Workspace PENDING 在恢复时只能把同一原 Attempt
置为 UNKNOWN；REQUEST 继续保留，exact retry 和 Universal Loop guard 均不得 Provider replay，
也不得伪造 RESULT。

### 25.4 权限、Backup 与物理身份

Admission、Begin、outcome、Backup verify、restore 与 reopen 必须共同验证：双方 grant 均存在，
REQUEST/RESULT 四方向，Tenant，双方 Workspace exact version 与 grant revision，direction，
payload kind/schema/ref，真实 ContentRecord bytes/ref/size，以及双方 byte ceiling 交集。当前上限
分别保持 `TASK_SUMMARY` 32 KiB、`SPECIALIST_RESULT` 48 KiB；缺失或单边 grant、stale revision、
换 Workspace、方向反转、kind/schema/ref/size/ceiling 漂移均须 fail-closed。撤权后新 Admission
必须拒绝；历史 exact retry 继续使用 Admission 时冻结的授权并返回原事实，不能从 current Control
重授权、重路由、重编译或替换历史 grant。

F1 没有增加 Runtime、Store、Loop、Gateway、Scheduler、Queue、Worker、Transfer/Usage Ledger、
表、migration 或协议。F1 验收时的历史物理身份逐字沿用 §24.1：24 张 ordinary table、fingerprint
`98d658e68907f77516ce0366a7b08bfa3851600326850590ed79584f581fa588`、migration `44,215`
bytes / SHA-256 `0a6701405e471e1ee7f41ea89ed62b50e5a4723e71b320e50f073cdd3e6b727a`；
W2-R1 后的当前 identity 只以 §3.3 与 §26.1 为准。

### 25.5 F1 开发验收边界

Scheduler 真实纵链覆盖 round-0 APPROVE、单槽 repair→round-1 APPROVE 与跨 Workspace
Transfer；稳定顺序/reopen、完整 `2N+3` 取消、UNKNOWN 可靠 evidence、完整
Decision+Transfer Backup/Restore、权限矩阵、Pure Chat 与 ordinary Composite 零 Transfer canary
均由源码测试闭合。F1 的独立真实 DeepSeek canary 为：

```text
HTTP 2xx = 4/4
exact retry model calls = 0
REQUEST verified = true
RESULT verified = true
input tokens = 3149
cached input tokens = 768
uncached input tokens = 2381
output tokens = 1492
token-weighted cache hit rate = 24.388695%
reasoning tokens = UNKNOWN
estimated cost = CNY 0.00538036
provider-reported cost = UNKNOWN
reconciled cost = UNKNOWN
```

Windows 全仓测试、`go vet ./...`、`go mod verify` 与 gofmt 门禁已通过。WSL ext4、Go 1.26.5、
GCC/CGO 的首轮七包 race 中，corecontract、controlcontract、assemblycompiler、runscheduler、
localchat 与 currentbackup 通过且为零 data race；currentstore 的唯一失败是测试使用 1 秒墙钟
TTL，并非 data race，因此不得把该首轮七包命令记为整体 exit 0。测试改用固定逻辑时间后，定向
race 与单核慢调度均通过，最终完整 `go test -race -count=1 ./internal/currentstore` exit `0`，
耗时 `466.241s`。

本 canary 不覆盖 §24 的 X1D 历史数据，也不是稳定缓存率、模型质量、生产价格或生产 SLA 基准。
`W5_COMPLETE_ACCEPTED_DEVELOPMENT_SLICE` 只表示 W5 四个原子开发切片闭合；
W2-D 已按自身门禁收口为 `W2_D_LOCAL_ASSEMBLY_ACCEPTED_DEVELOPMENT_SLICE`，但只覆盖本地显式
装配 v1；W2-E2 进一步以受信、编译进 Core 的 `text.stats` 闭合统一 Apply、Gateway 与备份恢复，
状态为 `W2_E2_TRUSTED_TEXT_STATS_APPLY_ACCEPTED_DEVELOPMENT_SLICE`；W2-E3 又闭合统一 Workspace
Channel Endpoint Apply、双 Workspace/Endpoint 隔离、Disable 引用与 Backup/Restore 后续推，状态为
`W2_E3_WORKSPACE_CHANNEL_APPLY_ACCEPTED_DEVELOPMENT_SLICE`；W2-E4 再闭合同一 DeepSeek
artifact/adapter/provider Instance 内 Flash↔Pro 及 Price/Authority/Profile/Usage/UNKNOWN/Backup
闭包，状态为 `W2_E4_DEEPSEEK_MODEL_REPLACEMENT_ACCEPTED_DEVELOPMENT_SLICE`；W2-E5-A 再闭合
governed Knowledge shared classifier、same-Profile exact Model Require、Core-owned
`knowledge.read` grant、完整 publication fast-path 与 Backup/Restore，其历史验收状态为
`W2_E5_A_REQUIRES_PERMISSION_GRANT_ACCEPTED_DEVELOPMENT_SLICE / W2_E5_B_NEXT`。W2-E5-B 随后以同一
受信 Document Insight Installation/Activation/Catalog instance 闭合 Context+Action 双 Port、per-instance
permission、两步 CAS Apply、Disable 顺序及 exact Backup closure，当前状态为
`W2_E5_B_DOCUMENT_INSIGHT_DUAL_PORT_ACCEPTED_DEVELOPMENT_SLICE`。截至 E5-B 的六项 accepted 都不改变
Operator Module Apply 与 Module Conformance 的 experimental，也不把任意第三方进程内 Action、公网或
任意第三方 Channel Provider、跨 Provider Model、自动路由、通用 multi-Port、同一 Port 多 Binding、
REMOTE/WASM 或 W6/W7 提升为 accepted。后续 W2-R1 只按 §26 接通一个窄 REMOTE Action 协议；其他
REMOTE 保持 planned。W2-R2 再按 §27 接通一个窄纯计算 WASM Action 协议；其他 WASM、生产级
不可信隔离与上述广义范围保持 planned。该段记录 W2-R2 收口时的历史边界；`W6_NEXT` 是 W5 历史 marker，当时下一入口为
`W2_R3_UNTRUSTED_MODULE_ISOLATION`，不表示首次生产部署、公开 Beta 或 `RELEASE_READY` 已获准。

## 26. W2-R1 REMOTE Action Host 历史接受 Store 闭包

状态：`W2_R1_REMOTE_ACTION_HOST_ACCEPTED_DEVELOPMENT_SLICE`。本节只为
`action.provider/v1 + REMOTE/freeagent-action-http/v1` 收口当前 Store、Attempt、恢复与 Backup
事实；它覆盖本文较早阶段中把全部 REMOTE 统一列为未实现的当前口径，但不改写 W5/E4/E5 的
历史 identity、handler 数量、阶段状态或验收数据。

### 26.1 当前物理身份与 REMOTE Activation

W2-R1 没有新增普通表、列、Runtime、Store、Loop、Gateway、Catalog pointer 或效果账本。当前唯一
FAC1 Store identity 为：

```text
ordinary tables = 24
schema fingerprint = dcad8f8837ecc5832f444acbc2680a2c2debff5161319710f60e7f649e4aede5
migration bytes = 44237
migration SHA-256 = fd7270130b5b13e5dfe9e3bb0a3bc934d46eb0c3b4a0647b1a3fddd81773e665
```

其中 migration 构件大小即 `44,237` bytes；逗号只用于本文可读展示，不进入 identity 计算。

唯一 schema 变化是 `module_activations.execution_class` 接受 `REMOTE`。Installation 继续保存
canonical Manifest ref、Module identity、ArtifactDigest 与 covered size；Activation 继续保存 Core
授予的 exact Tenant/Instance/Installation/revision、`REMOTE` 与 native Adapter identity；current
Catalog 和 MemberExecutionSnapshot 继续只引用 immutable Activation/Binding。Manifest、包和 Plan
都不能自行授予 ExecutionClass、Authority、endpoint、Secret 或执行资格。

只有 exact `action.provider/v1 + REMOTE/freeagent-action-http/v1 + action-binding-config/v1`
handler 可消费该 Activation。未知 REMOTE protocol 即使通过离线 Manifest
格式验证，也没有 current handler 或执行权。其他 REMOTE、WASM、不可信本地隔离、签名/发现/升级
与在线控制面不属于本闭包。

### 26.2 Config、SecretRef 与 Secret bytes

REMOTE Binding Config 与 Authority 继续作为 `CONFIG/application-json` 和对应不可变内容记录进入
既有 publication、Snapshot、Run 与 Backup 闭包。Config parameters 必须是 exact
`remote-action-http-binding-parameters/v1`，只保存 canonical HTTPS endpoint 与 SecretRef identity；
Action 映射、EffectClass、最大结果字节和 Tenant/Workspace scope 由 Binding 与 Authority 裁剪。

SecretRef **可以**持久化，因为它是不透明身份和恢复/授权闭包的一部分；Secret value/bytes
**禁止**进入 Manifest、descriptor、Plan、Config/Authority、Control/Catalog 或任何旁路字段，也禁止进入
Snapshot、Run/Frame/Event、Proposal、`dispatch_attempts`、result/receipt/evidence、Usage、日志、
错误、SQLite WAL 或 Backup。实际 Secret 只由运行期显式映射在 Gateway dispatch 边界瞬时解析，
Store API、Backup verify、Restore、启动恢复、Disable 和 Pure Chat 均不得调用 Secret resolver。

Operator Enable/Dry-run 的 artifact、HTTPS endpoint 与 SecretRef grant 是调用期瞬时事实，不写入
Store。Store 只保存经过这些 grant 裁剪后发布的 immutable Installation/Activation/Config/Authority/
Binding；后续生产调用仍须同时通过 current Activation、deny-only 撤权、运行期开关与 SecretRef
映射，不能把历史 Apply grant 当成可复用 Authority。

### 26.3 DispatchAttempt、PENDING→UNKNOWN 与禁止语义重放

REMOTE Action 复用现有 `dispatch_attempts` 的 `ACTION` kind，不建立 remote request、outbox、job、
retry 或网络专表。原 Action Definition、Proposal、Run/Member/Snapshot/Binding/Provider、Effect、
预算、lease、expiry 与 logical operation key 必须在既有 Begin 事务中闭合，并在任何 POST 前提交
唯一 `PENDING` Attempt；只有原 Gateway 可以消费该 permit 并调用一次 private
`ExecutePrepared`。

POST 前的确定性拒绝把原 Attempt 收口为 `FAILED` 且外部效果为零；明确 Provider 响应也只收口
这一个 Attempt。请求已进入传输而无法证明终态时，必须把原 Attempt 收口为 `UNKNOWN`。若外部
调用后终态事务失败或进程在 PENDING 窗口退出，启动/关停共用的 Store-only safety transaction
只能把同一原 Attempt `PENDING → UNKNOWN`；它不得加载 artifact/descriptor/Adapter、解析 Secret、
调用 Gateway 或发网。

exact retry、Universal Loop 重入、服务重启、Disable、Backup/Restore 与人工对账都只能读取或更新
原 Attempt。UNKNOWN 禁止重新 Describe/Prepare/POST，禁止换 Provider/Binding，禁止创建等价或
替代 Attempt/Run，也禁止把取消、超时、缺 artifact、Disable 或 Secret 缺失解释为“确定未执行”。
只有可靠 canonical evidence 精确绑定原 Attempt、当前 revision、冻结 Provider/Binding 和真实终态
时，才可通过既有 reconciliation CAS 收口；stale、错 Provider、错 Attempt 或无法验证的 evidence
必须 fail-closed、零部分写。

### 26.4 Backup、Restore 与 Pure Chat 零访问

完整 Backup/Verify/Restore 必须复制并双向验证：Installation/stored Manifest、REMOTE Activation、
ArtifactDigest/covered size、Catalog/Binding、Config/Authority、SecretRef identity、Action
Definition/Proposal、DispatchAttempt、result/receipt/evidence、Frame/Event 与 terminal closure。
REMOTE descriptor artifact 与其他 retained installation 一样进入 bundle；restore 只从已验证内容
重建私有普通文件，不产生 executable，不解析 Secret、不构造 production Adapter、不调用 Resolver/
Gateway，也不联网。Restore/Open 不隐式启用 REMOTE；遗留 PENDING 只按 §26.3 原位转 UNKNOWN。

未选择 REMOTE Action 的 Pure Chat 对 REMOTE Installation artifact/descriptor、Config/Authority、
SecretRef resolver、production lazy loader、native Adapter、Gateway 与 Action DispatchAttempt 必须
全部零访问。已安装或已激活但未绑定不能改变该边界；Disable 后即使 artifact 已被 Operator 删除，
后续 Pure Chat 仍不得尝试加载或修复它。Disable 保留 immutable Installation/Activation、历史 Run、
Attempt 与 UNKNOWN 对账事实，不执行 purge 或隐式 GC。

R1 验收已覆盖真实 production lazy loader/native Adapter/Gateway 纵链、PENDING 后 POST 前失败、
篡改构件失败不缓存、Provider `FAILED + HTTP 422` 恰好一次 RoundTrip、Disable 后真实 Pure Chat
零 REMOTE 访问，以及 SUCCEEDED、UNKNOWN、外部效果后终态持久化失败、重启和完整
Backup/Restore。当前没有真实公网 HTTPS 第三方互操作证据；本闭包不代表生产网络 SLA、其他
REMOTE、WASM、不可信隔离、公开 Beta 或 `RELEASE_READY`。该句保留 R1 收口时的历史边界；当前
R2 Store 权威闭包以下一节为准。

## 27. W2-R2 WASM Action Host 历史 Store 闭包

R2 收口时状态：`W2_R2_WASM_HOST_ACCEPTED_DEVELOPMENT_SLICE / W2_R3_UNTRUSTED_MODULE_ISOLATION_NEXT`。本节只为
exact `action.provider/v1 + WASM/freeagent-action-wasm/v1` 收口当前 Activation、Attempt、恢复与
Backup 事实；它覆盖较早阶段中把全部 WASM 统一列为未实现的当前口径，但不改写 R1/W5/E4/E5 的
历史 identity、handler 数量、阶段状态或验收数据。

### 27.1 当前物理身份与 WASM Activation

R2 没有新增普通表、列、Store、Loop、Gateway、Catalog pointer 或效果账本。当前唯一 FAC1 Store
identity 为：

```text
ordinary tables = 24
schema fingerprint = 7d2e0850a0253a630d5e2264c720b10fbac3adfdb6f9e53fcd722c2dd2615a8
migration bytes = 44257
migration SHA-256 = 2520299390885b4c23df85d2ff217629f6a458c368735e5bdc0df2eca37b8f2f
```

唯一 schema 变化是在 `module_activations.execution_class` 的既有枚举中加入 `WASM`。Installation
仍保存 canonical Manifest ref、Module identity、ArtifactDigest 与 covered size；Activation 仍保存
Core 授予的 exact Tenant/Instance/Installation/revision、`WASM` 与
`freeagent.adapter.action.wasm/v1`；Catalog、Binding、MemberExecutionSnapshot 与 Run 继续只引用
不可变事实。Manifest、artifact、descriptor 与 Plan 均不能自授 Trust、ExecutionClass、Adapter、
Authority、Host capability 或执行资格。

只有 exact `action.provider/v1 + WASM/freeagent-action-wasm/v1 + action-binding-config/v1` handler
可消费该 Activation。其他 WASM protocol、WASI、imports、Host Module、网络、文件、Secret、Core
控制类 Port、任意第三方进程内代码、签名/发现/升级和在线控制面均不属于本闭包。

### 27.2 Config、Usage 与 DispatchAttempt

WASM Binding Config 与 Authority 继续作为既有不可变 `CONFIG/application-json` 内容进入 publication、
Snapshot、Run 与 Backup。Config parameters 必须是 exact `{}`；Action 映射只允许
`EffectClass=none`，descriptor 请求、Binding 与 Authority 交集也必须为 `none`。Operator 的 exact
artifact grant 与运行期开关都是瞬时事实，不进入 Store；Store 不保存 guest Runtime、instance、
compiled module、Host handle、文件路径、网络或 Secret capability。

WASM Action 复用现有 `dispatch_attempts` 的 `ACTION` kind，不建立 wasm request、job、session、
instance、retry、meter 或专用 ledger 表。原 Definition、Proposal、Run/Member/Snapshot/Binding/
Provider、Effect、预算、lease、expiry 与 logical operation key 必须在 Begin 事务闭合，并在任何 guest
执行前提交唯一 `PENDING` Attempt；只有原 Gateway 可以消费一次 permit 并调用 private
`ExecutePrepared`。

成功 result 与 provider receipt 都作为既有 content-addressed 记录冻结。receipt 只允许 Host 观测到的
engine、ABI、input/output bytes、memory pages 与 elapsed milliseconds；当前 wazero 无稳定 fuel，
因此 `instruction_metering` 必须为 `UNSUPPORTED`，token、price、cost 与伪造 instruction count 均不得
持久化。guest trap、超时/取消、非法 ABI、越界/超限或非 canonical output 都是确定性 `FAILED`，只
收口原 Attempt，不产生 UNKNOWN 或替代 Attempt。

### 27.3 PENDING 恢复、精确重入与禁止重放

若 guest 返回后终态 Store transaction 失败或进程在原 PENDING 窗口退出，启动/关停共享的 Store-only
safety transaction 只可把同一原 Attempt 原位转为
`UNKNOWN/RECOVERED_PENDING_AFTER_CRASH`。该扫描不能读取 artifact、descriptor 或 guest bytes，
不能构造 loader/Adapter/Runtime，不能编译、实例化或执行 WASM。

exact retry、Universal Loop 重入、重启、Disable、缺失 artifact、Backup/Restore 与人工对账只能读取
或更新原 Attempt。UNKNOWN 禁止重新 Describe/Prepare/Execute，禁止换 Provider/Binding 或创建等价/
替代 Run/Attempt。只有可靠 canonical evidence 精确绑定原 Attempt、当前 revision、冻结 Provider/
Binding 和真实终态时，才能复用既有 reconciliation CAS；stale、错 Provider、错 Attempt 或无法验证
的 evidence 必须 fail-closed、零部分写。

### 27.4 Backup、Restore 与轻量边界

完整 Backup/Verify/Restore 必须复制并双向验证 Installation/stored Manifest、descriptor、guest bytes、
WASM Activation、ArtifactDigest/covered size、Catalog/Binding、Config/Authority、Action Definition/
Proposal、原 DispatchAttempt、result/receipt/evidence、Frame/Event 与 terminal closure。Restore 只从
已验证内容重建私有普通文件；不编译、不实例化、不执行 guest，也不隐式启用 WASM。非 Windows
恢复目录使用 `0700`，所有 WASM 包内普通文件使用 `0600`，不产生 executable。

未选择 WASM Action 的 Pure Chat 对相应 Installation artifact、descriptor、guest、Config/Authority、
production loader、Adapter、Gateway 与 Action Attempt 必须全部零访问。已安装或已激活但未绑定不能
改变该边界；Disable 保留 immutable Installation/Activation、历史 Run/Attempt 与 UNKNOWN 对账事实，
不执行 purge 或隐式 GC。

R2 验收覆盖统一 Apply/Dry-run、显式 runtime enable、production lazy load、真实 Gateway→native
WASM 成功纵链、canonical Usage、exact retry、完整 Backup/Restore、guest 返回后终态持久化失败、
重启恢复为原 UNKNOWN，以及删除 artifact 后 exact retry 零新 Run/Attempt/guest load。当前闭包
不代表 OS/container、生产级恶意多租户隔离、任意第三方包、其他 WASM、签名/发现/升级、公开 Beta
或 `RELEASE_READY`。R3 的当前覆盖合同见下一节。

## 28. W2-R3 不可信第三方 WASM 纯计算撤权 Store 闭包

状态：`W2_R3_UNTRUSTED_MODULE_ISOLATION_ACCEPTED_DEVELOPMENT_SLICE`。R3 不新增普通表、列、writer、
Catalog pointer、revocation generation、撤权表、效果账本或第二 Store。当前 ordinary table count、schema
fingerprint、migration bytes 与 migration SHA-256 与 R2 完全相同。

运行期 `--allow-wasm-runtime-artifact` 是进程内 Operator grant，不写入 Current Store 或 Backup。Store
只保存既有 Installation、Activation、Binding、Control/Catalog revision 与 Run/Attempt 冻结事实；模块
Manifest、descriptor 或 Config 不能把 runtime allowlist 变成持久权限。恢复旧 Backup 后是否重新允许运行，
仍由当前进程的开关、exact allowlist 与恢复后的 current Catalog 三者共同决定。

Gateway 的两次 current Activation 检查都读取同一权威 Store/Catalog pointer；第二次检查成功是执行准入
点，但不创建新 revision、lease 或 revocation row。两次检查之间 Disable 只能发生在测试用受控 seam；
生产 v1 的 `module-disable` 保持 Store 独占、停机 exact CAS，活动服务持有 Store 时返回 `STORE_BUSY`。

Disable 发布新的 Control/Catalog revision，仅从 current PortPlan 移除目标 Binding，并在最后引用消失时
从 current Catalog 移除 Instance；它不得删除或改写不可变 Installation、Activation、历史 MemberSnapshot、
Run、DispatchAttempt、result、receipt、evidence 或 UNKNOWN。共享 Instance 的单 Binding Disable 不等于
artifact 全局撤销。禁用后的 Backup/Verify/Restore 必须保留历史和 disabled current 状态；恢复更早的
活动 Backup 是 Operator 显式回滚。terminal/UNKNOWN exact retry 只读历史冻结事实，零 guest 重放。

R3 仍不是在线撤权控制面、OS/container sandbox 或生产恶意多租户隔离。下一入口是默认关闭的模块签名、
来源策略、发现与升级；相关来源、签名或升级状态在取得独立合同前不得擅自新增 Store 字段或表。

## 29. W2-U1 非持久本地来源观察边界

U1 收口时状态为 `W2_U1_SIGNING_SOURCE_POLICY_ACCEPTED_DEVELOPMENT_SLICE / W2_U2_DISCOVERY_SNAPSHOT_NEXT`；
本节记录已验收的 U1 非持久观察边界，不把它扩大为 Store-backed admission。

U1 的显式 `LOCAL_DIRECTORY + DENY` `module-verify` 分支不打开 Current Store，也不创建 Source、Policy、
Key、Signature、revocation、Candidate、reservation、grant、stage、Installation、Activation、Binding、
Control/Catalog revision、Run、Attempt、Usage 或 audit row。Publisher Key deny list 只是单次调用复制并
冻结的 immutable snapshot，不进入 Backup，也不是 current revocation authority。governed 成功仅返回原
`freeagent.module-package-verification/v1` observation，不能作为现有 `module-apply` 的 authority。

因此 U1 收口时 Store 继续为 24 张 ordinary table；schema fingerprint、migration bytes 与 migration SHA-256
完全沿用 §27.1，U1 不引入 migration。现有手工 exact-grant Apply 和九个 handler 继续从各自原入口
取得 authority，不要求或消费 U1 observation。

U2 已在唯一 Current Store 中定义并读取 SourcePolicy→Index→Snapshot observation parent 与 current
revocation；U3/U4 才定义 Candidate/Decision 与批准后 staging。U1 的调用期 deny snapshot 或 verification
report 不得被直接持久化后冒充这些权威事实。当前 U2 Store 闭包见下一节。

## 30. W2-U2 Source、Snapshot 与 current revocation Store 闭包

状态：`W2_U2_DISCOVERY_SNAPSHOT_ACCEPTED_DEVELOPMENT_SLICE / W2_U3_UPGRADE_REVIEW_NEXT`。
U2 在唯一 FAC1 Store 中增加且只增加 §5 的第 25–29 张表；不新增 Candidate/Decision/Apply、package、
audit、queue、worker、Runtime、Loop、Gateway、Catalog pointer 或效果账本表。

### 30.1 Source Policy 与 Publisher Key

`RegisterModuleSource` 只接受 exact canonical Source Policy 与可选 Publisher Key canonical bytes：

- 首次创建必须 `ExpectedPolicyRevision=0`；更新必须 exact CAS；完全相同的 Policy 是零 revision/timestamp
  变化的 exact retry；
- SourceID 永远不能从已冻结 Kind/OriginDigest 改绑到另一来源；新 Policy 会清空 current Snapshot head，
  但不删除历史 Snapshot 或 observation revision；
- signed Policy 必须引用 exact Publisher Key；unsigned Policy 不能顺带导入 Key。相同 Key ID 的 canonical
  bytes 必须相同；
- `RevokeModulePublisherKey` 需要 live exact revision，写入后全局不可逆。exact retry 返回原 revoked
  revision/timestamp；撤销不删除或改写历史 Source/Snapshot。

Store 不保存 local path 或 HTTPS URL 明文。`module_sources` 只保存 Policy 已冻结的 Kind 与
OriginDigest；完整 exact Policy canonical bytes进入同一行，供每次 refresh basis 与 Backup 语义重验。

### 30.2 Refresh basis、事务与精确重入

`ReadModuleSourceRefreshBasis` 在 Source I/O 前返回 defensive-copy 的 exact Source/Policy/Key current
事实。Source Provider 在事务外只观察一个 Index。`CommitModuleSourceRefresh` 随后以
`BEGIN IMMEDIATE` 重验：

```text
SourceID + Kind + OriginDigest
SourcePolicyID + PolicyRevision + canonical bytes
PublisherKeyID + KeyRevision + canonical bytes + not revoked
```

任一 stale、revoked、缺失或 canonical drift 都必须零写。Store 自己重新解析 Index、重建 Snapshot 与
content IDs，不能信任 Provider 给出的派生对象。提交前必须运行完整 discovery semantic closure；现有
tamper 会使本次新写整体回滚。

当前 head 已是同一 Snapshot 时为 exact retry，返回相同 observation revision、Snapshot 与 observed-at。
若中间已提交 B，则再次提交旧 A 不得命中旧 Snapshot 充当 retry。每个新 observation 只把当前
`module_sources.current_snapshot_id` 指向新 Snapshot；历史 Snapshot/entries 不可变。

### 30.3 全局 ModuleRef 与 observation-only 边界

`module_discovery_module_refs` 使同一 `ModuleID + opaque exact Version` 在所有 Source/Snapshot 中只能
对应一个 ArtifactDigest。该门禁与既有 `module_installations`、materialized Learning Version 双向对称：
无论先出现哪一类事实，之后的异 digest 都失败关闭；同 ref/同 digest 可以共享。Snapshot entry 顺序、
ArtifactSize、package path 与 nullable SignatureID 只表示来源观察；所有 entry 声明大小总和受 Core-owned
1 GiB ceiling。

U2 Store API 不读取 package path、不下载包、不验证 detached Signature、不创建 UpgradeCandidate、
CandidateDecision、reservation、stage、Installation、Activation、Binding、Control/Catalog revision、Run、
Attempt 或 Usage，也不调用现有 `module-verify` / `module-dry-run` / `module-apply`。因此 Snapshot、Key 或
Source fact 都不是 Trust、Authority、Effect 或执行资格。

### 30.4 Backup、Restore 与 Pure Chat

完整 Backup 的同一只读数据库事务必须调用 discovery semantic verifier，逐字节重建并验证 Key/Policy/
Index/Snapshot canonical bytes、content IDs、parent/revision/head、entry ordinal 和全局 ModuleRef closure。
Restore/Open 后保持完全相同的历史与 current head/revocation；Backup、Verify、Restore 不构造 Source
Provider，不读取 local root，不做 DNS/HTTP，不获取 package，也不自动 refresh。

没有显式执行 U2 CLI 命令时，Pure Chat、Assembly、Run Admission、Universal Loop、Gateway 与启动恢复
不得扫描或更新这五张表，也不得构造 Local/HTTPS Source Provider。U3 可以在后续独立合同中只从 exact
current Snapshot 生成无权限 Candidate/Decision；U4 在真正 staging 前还必须重新检查 current Policy、
Key revocation、Snapshot 与批准事实，并继续复用唯一 Apply/CAS。U3 当前 Store 闭包见下一节。

## 31. W2-U3 Candidate、Review 与 Decision Store 历史闭包

历史收口状态：`W2_U3_UPGRADE_REVIEW_ACCEPTED_DEVELOPMENT_SLICE / W2_U4_OPERATOR_APPLY_NEXT`。旧
NEXT marker 不是当前入口。U3 在唯一 FAC1
Store 中增加且只增加 §5 的第 30–32 张表；复用既有 `content_records` 保存 target canonical Manifest
evidence。没有 queue、reservation、stage、downloader、worker、suppression task、audit ledger、Runtime、
Loop、Gateway、Catalog pointer、handler registry 或效果账本。

### 31.1 三类不可变事实与 authority 边界

`module_upgrade_candidates` 保存全局、Tenant-free 的 exact supply Candidate：U0 canonical bytes/ID、stable
ReviewKey、Source/Snapshot parent、current 与 target ModuleRef/ArtifactDigest。它只接
`EXACT_VERSION_CHANGE`，因此 current/target ModuleID 相同而 exact Version 与 ArtifactDigest 不同。Candidate
不保存 scope、path、URL、Signature bytes、grant 或 authority；同一 Candidate 可被多个 Tenant/scope Review
引用。

`module_upgrade_reviews` 保存 Tenant-scoped、content-addressed `module-upgrade-review/v1` 与必要索引。每个
Review 精确绑定：

- CandidateID/ReviewKey；
- PROFILE 或 WORKSPACE_CHANNEL_ENDPOINT target；
- current Instance、Installation、Activation identity/revision；
- exact pointer、Control snapshot/revision/digest 与 Catalog generation/revision/digest；
- target Manifest `content_records` ref；
- `WOULD_APPLY | CONFLICT | UNSUPPORTED` conclusion。

Review canonical 另冻结完整安全投影与全部 published Binding impacts；表列只为索引和 parent constraint，
不得成为第二份可独立修改的事实。target Manifest content record 必须使用 `ContentModuleManifest` 与
`application/json`，其 canonical bytes 必须参与 target ArtifactDigest 的 conformance evidence，并与 Review
safe summary/ref 双向闭合。不得把调用方独立读取的 Manifest、Index `PackagePath` 或主机路径当作证据。

`module_candidate_decisions` 以 `review_id` 为主键，把一个 U0 `module-candidate-decision/v1` 终态绑定 exact
Review。DecisionID/canonical 不要求全局唯一，因此相同 authority-free U0 Decision canonical 可以分别绑定
多个合法 Review；Review parent 才是 Store 归属。APPROVE 与 REJECT 都不可修改或删除。

所有三类事实只表达观察/审核。它们不创建 Installation、Activation、Binding、Control/Catalog revision、
Run、Attempt、Usage、grant、Host 或 external effect，也不是 U4 Apply authority。

### 31.2 Review admission transaction

`ReadModuleUpgradeReviewBasis` 只读一个 coherent pre-I/O snapshot，必须从调用者的 exact selectors 重建：

```text
current SourcePolicy / PublisherKey / Snapshot / entry
exact Candidate + stable ReviewKey
current PublishedBasis
selected Binding + all Bindings that reference current Instance
Catalog Instance → exact Activation revision → Installation
```

它不读取 package path、不下载 package、不写 Candidate/Review。Operator 在事务外验证显式本地 target
artifact 与 optional detached Signature。`CommitModuleUpgradeReview` 是 post-I/O linearization point：

1. 从 Review canonical、Candidate canonical、Snapshot canonical 重建所有 content IDs；
2. 验证 target Manifest canonical/ref/summary 与 Review target；
3. `BEGIN IMMEDIATE`；
4. 若 ReviewID 已存在，先验证 exact canonical 与全库 U3 semantic closure，再返回原 record；
5. 否则重新构造 live review basis，逐字段比较 pre-I/O basis；
6. 原子插入 Candidate（或核对 exact existing）、target Manifest content record 与 Review；
7. 运行完整 U2+U3 semantic closure 后提交。

任一 stale Source/Policy/Key/Snapshot/current pointer/Control/Catalog/Activation/Installation/Binding、target
Instance collision、Candidate/Review ID collision、Manifest evidence mismatch 或已有 Tenant ReviewKey REJECT
均必须整笔零写。相同 Review exact retry 不重新读取 artifact、Source 或 Host；CLI full command 仍可重做
本地 verification，但 Store 只能返回同一 immutable fact。

### 31.3 Decision transaction 与 Tenant-wide REJECT

`DecideModuleCandidate` 必须先按 `review_id` 查询 existing Decision。完全相同的 lost-response retry 在验证
canonical 与 semantic closure 后返回原 record；异 canonical、异 Tenant 或异 Decision 拒绝。

新 `APPROVE` 必须：

- confirmation flag 为 false；
- CandidateID/Tenant 与 exact Review parent 相同；
- Review conclusion 为 `WOULD_APPLY`；
- 同 Tenant/ReviewKey 尚无 REJECT；
- 在同一 `BEGIN IMMEDIATE` 中重建 live review basis，并与 frozen Review 完全一致。

新 `REJECT` 必须由调用层和 Store 同时收到 explicit `ConfirmTenantWideReject=true`；APPROVE 不得携带该
确认。REJECT 不要求 current basis 仍活动，因为它只拒绝供应候选，但 partial unique index
`(tenant_id, review_key) WHERE decision='REJECT'` 保证同 Tenant 全 scope 只有一个 deny fact。一个 Tenant
的 REJECT 不得阻止另一 Tenant；同 Tenant 已有 REJECT 必须阻止既存或新建 Review 的后续 APPROVE。

confirmation 只用于新 REJECT。exact retry 在 existing fast path 成功后可直接返回历史 fact，不能因为
调用方丢失原 confirmation 而改变或否认已提交结果。

### 31.4 Semantic closure、Backup 与 Pure Chat

`VerifyModuleUpgradeSemanticClosureV1` 必须在一个 coherent query snapshot 中：

- 先执行完整 U2 discovery semantic closure；
- 重建每个 Candidate canonical/ID/ReviewKey 与 Snapshot entry parent，并拒绝 orphan Candidate；
- 重建每个 Review canonical/ID、target Manifest content evidence、historical Source/Key/Snapshot basis、
  PublishedBasis、Control/Catalog publication、selected Binding、exact Activation/Installation/current Manifest
  及全部 published Binding impacts；
- 重建每个 exact-Review Decision canonical/ID/parent；APPROVE parent 必须是 `WOULD_APPLY`；
- 验证 Tenant-wide REJECT 唯一性及所有 bidirectional table/canonical projections。

Publisher Key 后续 revocation 不使 historical signed Review 无效：历史 Review 绑定当时的 key revision 与
VERIFIED status；只要历史 parent 未篡改，Backup 可继续恢复。新的 Review/APPROVE 必须读取 current
revocation 并失败。删除或篡改 target Manifest、Candidate/Snapshot parent、Publication、Activation、
Installation、Binding impact 或 Decision 均使 Verify/Backup/Restore fail-closed。

完整 Backup 在同一只读数据库事务中调用 U3 semantic verifier，并通过既有 content closure 保存 target
Manifest evidence。Verify/Restore 不构造 Source Provider、不访问网络、不读取 host artifact/signature/
reason 文件、不运行 handler/Host，也不自动 Review/Decide/Apply。Restore 后 current REJECT 继续抑制同
Tenant ReviewKey；历史 APPROVE 仍只是 inert input。

没有显式 U3 命令时，Pure Chat、Assembly、Admission、Loop、Gateway、startup recovery 与 ordinary Store
open 不得扫描或修改第 30–32 张表，也不得读取 target Manifest evidence、artifact 或 signature。U3 current
identity 为：

```text
ordinary tables = 32
schema fingerprint = 37258c1939308be83f21a109e59a19e32946734d1c53b1b262f876dc36e47dbd
migration bytes = 57652
migration SHA-256 = e4f1047eb527b65ab050d444062f3d286cc2ad87d56e60b1cd7f4e96df87652d
```

U2 的 29 表与 R1/R2/R3 的 24 表 identity 继续只作对应历史证据。U4 入口门已经把原 CLI 私有的
9 条 generic/Model policy 与 2 条 Document Insight reserved exact selector 提取为 Review 与 Apply
共同复用的唯一 `internal/modulehandler` 实现，未复制第二张 handler 表。该旧 Handler-gate 只表示
进入下述 U4；不会建立第二份 Store authority。

## 32. W2-U4 approved Declarative Profile Context Apply Store 闭包

状态：`W2_U4_OPERATOR_APPLY_ACCEPTED_DEVELOPMENT_SLICE / W6_0_CONTROL_API_CONTRACT_NEXT`。U4 不增加
任何表、列、索引、trigger、ContentKind 或 Store owner；继续复用第 1–7 张表的 Installation、Activation、
Control/Catalog publication 和 current pointer，以及第 25–32 张表的 Source/Snapshot/Review/Decision facts。

### 32.1 Historical approval load 与 current revalidation

`LoadModuleUpgradeApplyApproval` 只读并重建 exact Tenant/ReviewID/DecisionID 的 historical closure：

- Decision 是 exact Review 的 APPROVE，Review conclusion 为 `WOULD_APPLY`；
- historical Source/Snapshot/entry/target Manifest 与 U1 supply facts；
- historical PublishedBasis、Control/Catalog、selected BindingImpact、Activation/Installation；
- frozen Config/Authority 与 current/target module identity。

Load 必须验证 historical canonical/IDs/parents，但不得把 current drift 错当成历史篡改。所有返回的
Control/Catalog/Profile/Workspace/Agent/Binding/Config/Authority nested slices 与 maps 都必须 defensive-copy，
调用者不能修改 Store projection。

`RevalidateCurrentModuleUpgradeApplyApproval` 在 staging 前重建 current Source Policy/key/revocation/head、
exact entry/target Manifest、pointer/Control/Catalog、selected binding impact、Activation/Installation、suppression
与 source candidate。任一漂移、wrong Tenant/Review/Decision、REJECT、共享 current、fanout 或 target collision
都失败关闭；不得回退到 historical current 或调用方提供的 derived values。

### 32.2 Canonical replacement 与 publication

首片 Admission 还要求：

```text
decision              = exact APPROVE
source                = LOCAL_DIRECTORY + DENY, explicit root
grant flags           = all explicitly present and empty
binding impacts       = exactly one
target kind           = PROFILE
port                  = context.provide/v1
runtime/protocol      = DECLARATIVE / static/v1
control/authority     = TRUSTED_INSTRUCTION / deny-all
port mode             = not SINGLE
replacement           = old exact ordinal → target exact instance
```

historical U1 verification 成功后，Store current revalidation 必须重新构造 canonical
`module-apply-plan/v1`，并与初始 plan 逐字节/PlanDigest 相同；current U1 verification 与 pre-stage Store
revalidation 再次成功后，才调用既有 Apply transaction。Review 与 Apply 使用同一 pure evaluator，不持久化
独立 dry-run admission、approval receipt、stage reservation 或第二 handler result。

Apply transaction 只把旧 Instance 在原 PortPlan ordinal 原位替换为 target。其他 Binding canonical bytes/
order 必须不变；old Catalog entry 移除、target exact entry 加入；Control snapshot、Catalog generation 与
`control_current.pointer_revision` 仍在唯一 transaction/CAS 中发布。旧 Run/MemberExecutionSnapshot 不改写，
新 Run 才从新 publication 冻结 target。

### 32.3 Retry、UNKNOWN、Backup 与零访问

只有 publication 仍是 Review frozen base 的相邻 revision，且 current Control/Catalog 与 exact plan 一致时，
同审批重试才返回原 publication。该 fast path 零 Source/artifact/stage I/O、零 Store mutation；它只是
adjacent plan idempotency，不是 durable approval receipt。任一后续 publication 后，旧审批必须返回
`POINTER_CONFLICT`，不得尝试重放或覆盖 current。

existing Apply 在事务/进程失败后无法证明终态时，原结论保持 UNKNOWN；U4 不创建替代 Attempt、替代
publication 或语义重放。terminal/UNKNOWN exact retry 只读冻结事实。

Backup/Verify/Restore 必须保存并重验 U2 Source、U3 Candidate/Review/Decision/Manifest 与 U4 最终
Installation/Activation/Control/Catalog publication 的 exact closure。Verify/Restore 零 Source、零网络、零
artifact/stage、零 Apply；恢复 historical approval 不自动重新授权。没有显式 U4 命令时，Pure Chat、
ordinary Store open、Assembly、Admission、Loop、Gateway 与 startup recovery 不扫描 Review/Decision，
不构造 Source Provider，也不读取 artifact 或 stage。

U4 current identity 与 U3 完全相同：

```text
ordinary tables = 32
schema fingerprint = 37258c1939308be83f21a109e59a19e32946734d1c53b1b262f876dc36e47dbd
migration bytes = 57652
migration SHA-256 = e4f1047eb527b65ab050d444062f3d286cc2ad87d56e60b1cd7f4e96df87652d
```

U4 的历史入口 `W6_0_CONTROL_API_CONTRACT_NEXT` 不授权第二 Store writer、在线 Apply、自动 upgrade
worker、Model/Channel/Action replacement、广义通用装配、公开 Beta 或生产部署。

## 33. W6-0 控制 API 纯合同 Store 历史边界

W6-0 当时状态：`W6_0_CONTROL_API_CONTRACT_ACCEPTED_DEVELOPMENT_SLICE /
W6_1_APPLICATION_SERVICES_READ_API_NEXT`。

W6-0 的六份 pure canonical wire 与 transport-neutral policy 不进入 Current Store schema。特别地，
`control-view-snapshot/v1` / `freeagent.control-view-snapshot/v1` 是 Web 只读视图合同，不替换本规格既有
`control-snapshot/v1` / `freeagent.control-snapshot/v1`，也不得写入 `control_current` 冒充 Runtime
publication。Operation request 的 `DRY_RUN/MUTATE` intent、动态 ID/时间排除规则与 exact UNKNOWN receipt
语义目前仅为跨层合同，不创建 durable fact。

W6-0 没有 `control_operation_receipts` 或其他 receipt/session/cursor table，没有 migration、index、trigger、
backup ContentKind、Store owner 或第二 writer 变化；production `cmd/freeagent` 也不导入这些合同包。
该 W6-0 历史原子当时的 identity 精确为：

```text
ordinary tables = 32
schema fingerprint = 37258c1939308be83f21a109e59a19e32946734d1c53b1b262f876dc36e47dbd
migration bytes = 57652
migration SHA-256 = e4f1047eb527b65ab050d444062f3d286cc2ad87d56e60b1cd7f4e96df87652d
```

当时源码证据为 39 份 Markdown、37 个源码 package 与 35 个 production dependency-closure package。
`W6_1_APPLICATION_SERVICES_READ_API_NEXT` 只允许 read/Dry-run application services，并要求 Store 保持只读。

## 34. W6-1 Application Services / Read API Store 边界

历史收口：`W6_1_APPLICATION_SERVICES_READ_API_ACCEPTED_DEVELOPMENT_SLICE /
W6_2_CONTROLLED_MUTATIONS_AUDIT_NEXT`。

W6-1 复用 `freeagent serve` 已打开的唯一 Current Store。Modules Application Service 只通过窄 Store reader
读取 exact current PublishedBasis，再按已授权 Tenant/Workspace scope 投影 allowlisted list/detail DTO；
Control handler 不持有 SQLite driver、SQL、migration、Backup/Restore、recovery、Secret resolution、artifact
loader 或第二连接所有权。除 bootstrap exchange 外，GET 与非安全方法必须先验证 exact session 与
session-bound CSRF；GET 缺失或错误 CSRF 返回 401，并在任何 Store read 前失败。

`MODULE_DISABLE` Dry-run 只从 exact If-Match 与当前 published basis 计算 candidate projection。它不进入
Apply/CAS，不更新 Control/Catalog pointer、Activation、Binding 或 artifact，也不调用既有停机 CLI
Apply/Disable。返回的 `control-operation-receipt/v1` 状态是 `DRY_RUN`，仅存在于进程内响应；W6-1 不创建
session、cursor、receipt、operation、audit 或 query-cache 表，也不把 handoff/credential/CSRF 写入 Store、
Backup 或日志。

该 W6-1 历史原子当时的 identity 精确为：

```text
ordinary tables = 32
schema fingerprint = 37258c1939308be83f21a109e59a19e32946734d1c53b1b262f876dc36e47dbd
migration bytes = 57652
migration SHA-256 = e4f1047eb527b65ab050d444062f3d286cc2ad87d56e60b1cd7f4e96df87652d
```

该 W6-1 历史原子当时的源码证据为 39 份 Markdown、44 个源码 package 与 44 个 production dependency-closure package。
没有 Control mutation、durable receipt、SSE、UI、第二 Store/writer、后台 worker 或主动预热。
`W6_2_CONTROLLED_MUTATIONS_AUDIT_NEXT` 只允许审计首个受控 mutation；若要启用，仍须另行批准窄
`control_operation_receipts` schema，并把 receipt 与 domain first persisted fact 在唯一 Current Store
transaction 中原子提交。在该批准前 mutation 必须保持不存在或失败关闭。

## 35. W6-2 confirmation 与 Receipt Schema 历史入口

历史状态：`W6_2_CONFIRMATION_CONTRACT_ACCEPTED_DEVELOPMENT_SLICE /
W6_2_DURABLE_RECEIPT_SCHEMA_NEXT`。Audit 历史 marker 为
`W6_2_CONTROLLED_MUTATIONS_AUDIT_ACCEPTED_DEVELOPMENT_SLICE / W6_2_CONFIRMATION_CONTRACT_NEXT`。

本切片只增加 canonical Statement/evaluation 与 process-local proof registry，不写 Current Store：raw proof
固定 32 bytes、最多 2 分钟且不超过当前 session absolute expiry、global 256/session 8；registry 仅保留 domain-separated proof digest 与当前
Boot/session/principal/auth revision/scope-set/capability/scope/Statement/Request/expiry/state。proof 与 proof
digest 都不进入 Store、Backup、日志、URL 或错误。当时仍精确为 32 ordinary tables、历史 fingerprint、
57,652-byte migration 及既有 Backup/Restore semantic closure；39 份 Markdown、45 个源码 package、44 个
production dependency-closure package。无 durable row、mutation route，纯 SQLite 首片 `UNKNOWN` 不可达。

`W6_2_DURABLE_RECEIPT_SCHEMA_NEXT` 的候选只能新增一张 append-only
`control_operation_receipts` ordinary table，并在明确采用开发期 rebuild-only 时把 32→33、migration、
fingerprint 与 Backup goldens 一并变更。尚未实现的冻结草案为：

- 保存 exact canonical+digest 的 Request、typed input、operation evaluation、Control receipt、pre/post 完整
  `PublishedBasisRefV1` 与 domain-native `module-disable-publication-receipt/v1`；不允许 digest-only resolver；
- domain receipt 仅含 exact restored disabled plan+recomputed digest、pre/post full basis、removed Binding 的
  target/profile/instance/port/ordinal/Config/Authority/StaticContextRefs/FailurePolicy 与 catalog change；排除
  principal/request/key/confirmation/proof/session/time，ID/digest 分别使用独立
  `freeagent.module-disable-publication-id/v1` / `freeagent.module-disable-publication-receipt/v1` 域；
- 唯一 identity 为 `(principal_id, scope_digest, operation, idempotency_key_digest)`；历史
  `authorization_revision`/`scope_set_digest` 只审计不授权，绝不保存 Boot/session/proof/proof digest；
- Request/input/evaluation/receipt/pre/post/domain/canonical-total ceilings 分别为 8/8/64/16/8/8/128/256 KiB；
  Tenant 1024、Store 8192，满额失败关闭且不驱逐；表和 Store API 都 append-only；
- receipt insert、domain receipt、Control/Catalog 与 pointer CAS 必须在同一 `BEGIN IMMEDIATE` transaction；
  首片只持久化 `NO_CHANGE/APPLIED`。same identity+same request digest 在 proof/current 检查前返回 exact
  receipt；异 digest 冲突；已有 candidate pointer 却无 receipt 是 integrity failure，不得回填；
- Backup Create/Verify/Restore 必须复验所有 canonical/digest、父 ref、唯一 domain effect 与
  `MODULE_DISABLE` semantic replay；任何缺失、篡改、孤儿或重复认领都失败关闭。

这些是 confirmation 收口时冻结的下一原子候选，不是当时的 Schema 事实，也不授权 mutation handler 或
publication wiring。

## 36. W6-2 durable receipt Schema 历史验收边界

历史收口状态：`W6_2_DURABLE_RECEIPT_SCHEMA_ACCEPTED_DEVELOPMENT_SLICE /
W6_2_MODULE_DISABLE_MUTATION_WIRING_NEXT`。

### 36.1 该历史原子当时的 identity 与唯一新增表

```text
ordinary tables = 33
schema fingerprint = 51be9081a815e6379380b93e745db03d33ca2f358962f85ace63bce2534dcc10
migration bytes = 67998
migration SHA-256 = 8feea38beb505ab4f371afe32eecd877744062bd149d0d24982839c5450fc952
```

`control_operation_receipts` 是唯一新增 ordinary table。它是 STRICT、append-only，不建立第二 Store、writer、
publication owner、session store、proof store 或效果账本。identity 只由 exact Request 派生的
`(principal_id, scope_digest, operation, idempotency_key_digest)` 决定；receipt digest 为主键，domain ID 与
domain digest 分别部分唯一。SQL trigger 同时锁定同 Tenant Control/Catalog parents、Catalog→Control pair、
replace/conflicting insert、update/delete 与 Tenant 1,024 / Store 8,192 quota。

单行 canonical ceilings 固定为 Request/input/evaluation/Control receipt/pre/post basis/domain receipt
`8/8/64/16/8/8/128 KiB`，总 canonical 最多 `256 KiB`。status 只允许 `NO_CHANGE/APPLIED`；
`NO_CHANGE` 必须 pre=post 且没有 domain receipt，`APPLIED` 必须 pre/post 变化并带唯一 exact
`MODULE_DISABLE` domain receipt。

### 36.2 exact resolver 与公开 NO_CHANGE commit

`ResolveControlOperationReceiptV1` 只接受 exact Request canonical+digest，不接受并行 principal/scope/key
assertions。不存在返回 typed not-found；同 identity 且相同 request digest 才恢复原 receipt；同 identity 不同
request digest 返回 typed conflict。所有返回 canonical 都 defensive-copy、strict Restore 并做 cross-closure。

`CommitModuleDisableControlReceiptV1` 在该 Schema 历史原子中是唯一公开写入口，只允许 neutral evaluator 重放后确定
`NO_CHANGE` 且 current PublishedBasis 未变化的完整记录。exact retry 返回原行；该 API 不能发布
Control/Catalog、不能插入 `APPLIED`、不能创建或认领 domain effect。

### 36.3 strict contracts 与 semantic replay

- 共享 `module-apply-plan/v1` 严格冻结 TENANT/PROFILE `context.provide/v1` Disable 计划与 candidate IDs；
- `PublishedBasisRefV1` 使用唯一 Published Pointer digest freeze/restore，不复制第二套 basis identity；
- `module-disable-publication-receipt/v1` 只包含 exact disabled plan、pre/post full basis、removed Binding 的
  target/instance/port/ordinal/Config/Authority/StaticContextRefs/FailurePolicy 与 catalog change，排除
  principal/request/key/confirmation/proof/session/time；publication ID 与 receipt digest 使用独立 domain；
- `VerifyControlOperationReceiptSemanticClosureV1` 先枚举有界 metadata/quota/caps，再逐行严格 Restore
  Request/input/plan/evaluation/Control receipt/pre/post basis/domain receipt，验证 immutable Control/Catalog
  parents、same-Tenant edge 与唯一 neutral evaluator replay。

### 36.4 Backup 与 completeness 边界

Backup Create 的 source/staged snapshot、Verify、Restore staged/final 与 standalone semantic gate 都在 coherent
SQLite snapshot 内调用 receipt semantic verifier。现存 receipt 的 canonical 篡改、parent 缺失/错 Tenant、
重复 identity/domain claim、错误 NO_CHANGE/APPLIED 矩阵或 replay 偏差都失败关闭。

该闭包不宣称能发现整行删除：单一 append-only receipt 表没有外部 completeness anchor。其下一 mutation
wiring 只能对 exact 请求通过 resolver 判断“已有 receipt / miss”；不得把全表扫描的 absence 解释为历史上
从未提交过效果，也不得事后 backfill 已发布 pointer。

### 36.5 APPLIED 与下一入口

在该历史原子中，`APPLIED` 只有 strict wire、DDL/restore 与 verifier 形状，没有公开 insert/publication
seam；下一 wiring 必须由唯一 Current Store publication owner 在同一 `BEGIN IMMEDIATE` transaction 内提交 domain receipt、Control
receipt、Control/Catalog publication 与 pointer CAS；任何先 publication 后 receipt 或反向顺序都不合格。

该历史原子的证据为 39 份 Markdown、47 个源码 package、46 个 production dependency-closure package；
当时没有 mutation route/handler、confirmation endpoint、APPLIED public insert、SSE/UI/worker；其下一入口只允许
`W6_2_MODULE_DISABLE_MUTATION_WIRING_NEXT`。

## 37. W6-2 MODULE_DISABLE mutation wiring 历史 Store 边界

历史收口状态：`W6_2_MODULE_DISABLE_MUTATION_WIRING_ACCEPTED_DEVELOPMENT_SLICE /
W6_3_WEB_SHELL_READ_ONLY_OVERVIEW_NEXT`。

`EvaluateModuleDisableControlOperationV1` effect-free 重建 exact candidate evaluation；
`CommitModuleDisableControlOperationV1` 是唯一 APPLIED publication seam。它先在 Store transaction 内再次
resolver identity，再只允许 TENANT/PROFILE exact `context.provide/v1`、existing Binding `OPTIONAL`、
`DECLARATIVE static/v1`、trusted-instruction config 与 deny-all Authority。NO_CHANGE 只 append Control
receipt；APPLIED 在同一个 `BEGIN IMMEDIATE` 内 append domain/Control receipt、发布 Control/Catalog 并对
pointer CAS。已有 candidate publication 而无 receipt 是 integrity failure，不能 backfill。

lookup-first 由 coordinator 在已重新通过当前 Origin/session/CSRF/TENANT Permit 后调用：exact hit 可不依赖旧
proof或旧 session identity，但不是匿名 Store resolver；miss 才进入 process-local proof/current basis 路径。
纯 SQLite commit 没有 UNKNOWN outcome；丢失响应、重启与并发只以 exact receipt resolver 裁决。

Backup Create/Verify/Restore 对现存 APPLIED 行做 exact roundtrip，并拒绝 immutable parent、evaluation replay 与
domain receipt tamper。无 external completeness anchor，semantic verifier 不声明发现任意整行 receipt 删除。
Store 测试通过强制 receipt insert abort，证明 publication/domain/Control receipt/pointer CAS 全事务 rollback；
这是 publication-without-receipt 的原子性证据，不是全表 completeness claim。

该历史原子当时为 33 ordinary tables；fingerprint/migration 为
`51be9081a815e6379380b93e745db03d33ca2f358962f85ace63bce2534dcc10`、67,998 bytes /
`8feea38beb505ab4f371afe32eecd877744062bd149d0d24982839c5450fc952`。当时证据为 39 份 Markdown、48 个源码
package、48 个 production dependency-closure package；没有第二 Store/writer、其他 mutation、SSE/UI/worker；
当时下一入口只允许 `W6_3_WEB_SHELL_READ_ONLY_OVERVIEW_NEXT`。

## 38. W6-3 Web Shell / read-only Overview 历史 Store 边界

历史状态：`W6_3_WEB_SHELL_READ_ONLY_OVERVIEW_ACCEPTED_DEVELOPMENT_SLICE /
W6_4_MODULES_CONFIGURATION_UI_NEXT`。

W6-3 不创建第二查询库或物化缓存。所有正常 production lifecycle 在原事务中维护 Run observation、
resource snapshot/head/transition carrier 与 PublishedBasis snapshot/head/workspace rows；Overview reader 在单一只读
事务中只从这些不可变事实构造有界 projection。Run/resource current rows 与 observation heads 必须逐字段匹配；
resource snapshots 与 transition carriers 必须一一对应；basis head 必须精确引用当前 Control/Catalog pointer 与
连续 Workspace ordinals。UPDATE/DELETE/OR REPLACE、缺失 carrier、协调重签或历史因果替换均失败关闭。

该 W6-3 历史切片 Store identity 为：

```text
ordinary tables = 41
indexes = 23
triggers = 56
schema fingerprint = 87d63a2e468ee27ee2c0e1918a92e580532825bde85dbb872b9c795bf577f2c1
migration bytes = 143588
migration SHA-256 = 5bd9743e988b82750f785c015f07c11fbcf6025da70148405aec11106b4efb22
```

初始化、OpenExisting、standalone full semantic gate 与 Backup Create/Verify/Restore 都验证 schema identity、
foreign keys、immutable observation history、head/cardinality/exact tuple、Run causal closure 与 current basis。
这些 observation facts 只为 read-only Overview 提供可验证来源，不授予 mutation、调度、重放、Provider/Gateway
效果或新的 authority。其历史下一入口为 `W6_4_MODULES_CONFIGURATION_UI_NEXT`，不得借 Overview tables
绕过唯一 Store publication owner。

## 39. W6-4 Modules configuration UI 历史 Store 边界

历史状态：`W6_4_MODULES_CONFIGURATION_UI_ACCEPTED_DEVELOPMENT_SLICE /
W6_5_SERVER_OWNED_MODULE_ARTIFACT_INGRESS_NEXT`。

W6-4 不增加或修改任何 table、index、trigger、migration、transaction owner、receipt row、Backup member 或
Store API。41 tables / 23 indexes / 56 triggers、fingerprint
`87d63a2e468ee27ee2c0e1918a92e580532825bde85dbb872b9c795bf577f2c1` 与 migration 143,588 bytes /
SHA-256 `5bd9743e988b82750f785c015f07c11fbcf6025da70148405aec11106b4efb22` 保持不变。

Modules UI 的 list/detail 只读取现有 Store-derived application projection；`MODULE_DISABLE` 成功仍由既有唯一
publication owner 在同一 `BEGIN IMMEDIATE` transaction 中提交 Control/Catalog/pointer 与 durable receipt。
浏览器不得把缓存、历史 receipt、Overview rows、digest/ref 或本地 optimistic state 当作 Store authority。

`W6_5_SERVER_OWNED_MODULE_ARTIFACT_INGRESS_NEXT` 是 W6-4 收口时的历史下一入口；W6-4 不创建 artifact row、
staging/install/activation/grant 或第二 writer。

## 40. W6-5 server-owned module artifact ingress 当前 Store 边界

当前状态：`W6_5_SERVER_OWNED_MODULE_ARTIFACT_INGRESS_ACCEPTED_DEVELOPMENT_SLICE /
W6_6_SERVER_OWNED_MODULE_UPGRADE_REVIEW_NEXT`。

W6-5 只增加两张 supply-chain fact 表：

1. `module_artifacts` 以 ArtifactDigest 为唯一 identity，保存 exact ModuleRef、Manifest content ref、artifact size、
   covered file count 与 ingress time；它是 server-owned content-addressed inert object，不是 Installation；
2. `module_artifact_admissions` 保存 canonical `module-artifact-ingress-record/v1`、exact SourcePolicy revision、
   Snapshot/observation revision/entry ordinal 与 ArtifactDigest parent；AdmissionID 从 canonical record 派生，整表
   append-only，不能 update/delete，也不授予任何 runtime authority。

唯一 CLI 在 filesystem durable publication 后调用 Store。`ReadModuleArtifactIngressBasisV1` 只读取并重建 Store-owned
current Snapshot 中 unsigned `LOCAL_DIRECTORY + DENY` exact entry；package path 来自 Store entry，source/artifact root
都不进入 durable row。`CommitModuleArtifactIngressV1` 在一个 `BEGIN IMMEDIATE` 中先做 exact-retry lookup，再重建
current basis、验证 Manifest content identity，并原子插入/复用 immutable Artifact 与插入 append-only Admission；
stale、冲突、quota 或 semantic closure failure 全事务 rollback。Artifact/Admission 不创建 Installation、Activation、
Binding、grant、Review/Decision、Apply、Run/Attempt、Host、Secret 或 execution。

filesystem publication 的 root、child 与全部 namespace ancestors 必须是可信 owner 的 private/non-replaceable
boundary；Source、artifact 与 Backup 的每个 descendant 都相对 held parent handle 打开，并拒绝 link/reparse、
hardlink、跨 filesystem/volume 与 identity/change-time/namespace 漂移。持久 OS root lease 跨进程串行化 crash-stage
recovery、双遍物理 closure/配额、stage、no-replace publish、sync 与最终复验；root 最多 256 个 digest tree、
512 MiB covered content、32,768 个 artifact-relative path、16,384 个普通文件与 16 MiB canonical path-name
bytes，每文件最多 64 MiB。这些是内容/namespace 硬预算，不等于实际 filesystem allocation blocks。commit
结果不明时保留有界 inert object而不猜测删除。

新 ingress 的同 digest 目标缺失时必须始终以目录 `0700`、文件 `0600` 发布。既有同 digest 目标只有在完整
bytes/mode 复验后才可复用，且只接受两种 root-global 闭包：全部目录 `0700`、全部文件 `0600`；或全部目录
`0700`，仅 canonical `LOCAL_PROCESS + mcp-stdio/2025-11-25` descriptor 精确绑定的唯一 executable 文件为
`0700`，其余文件 `0600`。两种闭包都拒绝 special/setid/sticky 与任意 group/world 权限。artifact root 跨
Store 共用时，这一物理模式判断不得依赖任一单独 Store 的 Installation；后一种只是既有物理兼容态，不授予
当前 Store Installation、Activation 或 execution authority。W6-5 的 inert 精确定义为无 Store authority 且
本切片不执行，不能等同于 OS executable bit 不存在。

exact retry 分两类：若 exact Admission 已 durable，selector 即使已不是 current head 也只返回该已验证结果；如果没有
Admission，historical/stale selector必须失败且不能采用 orphan。后续任一 current、合格 selector声明同一 digest 时，
可以在全包 bytes/mode/size/file-count 复验后复用物理对象，但该复用本身不授予 authority。

Backup artifact set 固定为 distinct `(module_installations.artifact_digest ∪ module_artifacts.artifact_digest)`；Create、
Verify 与 Restore 同时验证两类 Store parent、Manifest 和完整 artifact bytes。一个只被 Admission 引用、没有
Installation 且 Source 已移除的对象仍必须完全离线 round-trip；同一 digest 同时 installed+ingressed 时 bundle 只含
一份 bytes。恢复不得重读 Source、联网、Install、Activate 或 execute；临时 SQLite、bundle 与 restore tree 只能在
可信私有 staging 中创建，copy 从 held handles 读取，publish/rename/cleanup 后同步受影响 parents。

FAC1 W6-5 历史 Store identity 为：

```text
ordinary tables = 43
explicit indexes = 25
triggers = 64
schema fingerprint = 47981b9bf147ec81c6e98382f281db0d5395187a1f090f7ce183c116fba82a0d
migration bytes = 150301
migration SHA-256 = 6def433a59fa8f4876894572f1610cae499abc5b389ab5930ea697055419ca86
```

`W6_6_SERVER_OWNED_MODULE_UPGRADE_REVIEW_NEXT` 是 W6-5 收口时的历史 marker；它只授权下一原子设计
读取这些 inert facts，W6-5 不创建 Upgrade Review/Decision，也不 Install、Activate、Bind、grant、Apply 或 execute。

## 41. W6.6 server-owned Upgrade Review 当前 Store 边界

当前状态为 `W6_6_SERVER_OWNED_MODULE_UPGRADE_REVIEW_ACCEPTED_DEVELOPMENT_SLICE / P2_CONTROL_UI_I18N_ACCEPTED_DEVELOPMENT_SLICE / P3_SECOND_PROVIDER_ACCEPTED_DEVELOPMENT_SLICE / P4_MODULE_MANAGEMENT_UI_READ_ONLY_SLICE_ACCEPTED_DEVELOPMENT_SLICE / P5_BETA_GATE`。
W6.6 不新增第二 Store、writer、HTTP route、UI、SSE、worker、Provider 或执行路径。两个默认关闭的可信
Operator CLI 只接受 Tenant、scope、Admission/Review identity、operator principal、request digest 与
Decision reason；artifact path、URL、signature bytes 和 caller-supplied current/target facts 一律不是 authority
输入。服务端从 `module_artifact_admissions` 读取 Admission，复验 Artifact/Manifest/Source/Snapshot/Installation/
Binding/Activation/Catalog/Control closure 与物理 digest/size/file-count/mode，再复用 W2-U3 evaluator。

`ReviewV1` 的 canonical 与 SQL projection 同时绑定 `ArtifactAdmissionID`、`OperatorPrincipalID` 和
`ReviewRequestDigest`。Review/Decision 使用 content identity exact retry；同一请求返回原持久事实，冲突、跨 Tenant、
stale basis、物理篡改、Review integrity 或不合格 Decision 均失败关闭。备份 Create/Verify/Restore 必须同时保留
Admission、Artifact 与 Review/Decision closure；恢复后 reopen 结果不改变。W6.6 Review/Decision 只产生 inert
审核事实，不 Install、Activate、Bind、Grant、Apply、Execute，不调用 Provider，不创建 Runtime/Attempt/Usage/Effect。

当前 FAC2 identity 为：

```text
UserVersion = 2
ordinary tables = 42
explicit indexes = 26
triggers = 64
schema fingerprint = d5d876f327dc29dc6f4a10476652641172ab8e1f0451a8714fc450f58733541e
0001_current.sql = 149239 bytes / dbc3e724a1f7c030677c84a77a317f69ef2fe246985cc749559a9f3dd5a6dc5a
0002_server_owned_review.sql = 7173 bytes / 3091a49ebcf724f573f91cc0fd22a7c58ebb52fa9d7ed552e32b6526ebeca3cb
```
