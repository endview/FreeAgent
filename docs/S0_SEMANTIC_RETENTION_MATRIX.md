# S0 语义保留与重写矩阵

状态：`S0_BASELINE_COMPLETE`

审计日期：2026-07-30

主方案基线：`freeagent-architecture-simplification-plan.md`

S0 审计时执行主方案 SHA-256：
`69660A7FB30CDDF36E2D81A3270EEAF6AE9C0AE5994CF0484F8ECD8AC461216A`

## 1. 文档定位

本文是 S0 的代码、Store、规格、种子与门禁审计结果，不是第三套架构。
发生冲突时，以仓库内 `CORE_RUNTIME_V1`、`CURRENT_STORE_V1` 两份编码规格和
一次性 `CUTOVER_ACCEPTANCE` 清单为公开权威；仓库外主方案的 SHA-256 只用于
记录本轮执行 provenance，不能成为公开实现的隐藏规范。

本文只回答四个问题：

1. 旧实现中哪些业务语义需要保留；
2. 哪些实现必须围绕新 Core 和新 Current Store 重写；
3. 哪些能力必须推迟到 S2；
4. 哪些旧链必须退出生产权威路径。

本文中的 `DELETE` 表示从生产入口、启动装配、当前
Schema、CI 权威和正式文档中删除，不要求在 S0 立刻物理删除所有历史源码。
历史源码在语义提取结束前可以保留为只读参考，但不得继续扩展。

## 2. 处置标签

| 标签 | 含义 | 是否允许直接复用旧实现 |
|---|---|---|
| `PRESERVE` | 保留业务规则、不变量、失败语义或用户可见行为 | 只在新契约下重新验证后允许 |
| `REWRITE` | 围绕唯一 Core、PortPlan、Universal Loop 和 Current Store 重写 | 不允许把旧表、旧证据对象或旧执行链直接当作新权威 |
| `S2_DEFER` | 不进入 S1 Pure Chat 的 Admission、Store、Worker 或生产入口 | S2 逐项接回，每项独立验收 |
| `DELETE` | 从生产装配、当前 Schema、正式规格和权威门禁中移除 | 可暂存为历史参考，不可被生产读取或写入 |

一个领域可以同时具有多个标签。例如
`PRESERVE + REWRITE + S2_DEFER` 表示保留设计意图，但不保留旧实现，
并且不进入 S1。

## 3. S0 审计时规模快照

以下数字来自 2026-07-30 的 S0 仓库静态扫描，只描述当时规模，不代表当前树规模
或成熟度。

| 项目 | S0 审计值 | 审计含义 |
|---|---:|---|
| 仓库文件 | 984 | 当前公开树较大 |
| Go 文件 | 904 | 实现面明显超过精简 Core |
| Go 测试文件 | 401 | 测试数量多，但不能替代生产接线证明 |
| PowerShell 测试脚本 | 12 | 多数属于发布、文档与资产门禁 |
| SQLite migration | 11 | 现有迁移链为 `0001` 至 `0011` |
| SQLite 表声明 | 167 | 当前 Store 远大于 S1 Pure Chat 所需范围 |
| SQLite 索引声明 | 108 | 与旧多能力 Store 高度耦合 |
| SQLite trigger 声明 | 494 | 迁移、兼容和证据闭包复杂度很高 |
| 旧架构规格 | 11 份 | 共 19,607 行、1,443,972 字节 |
| `scripts` | 25 个文件 | 共 33,765 行、1,507,957 字节 |
| release workflow | 1,546 行 | 当前发布门禁本身也需要减重 |
| Skill 专用内部目录 | 10 个 | 共 139 个文件、2,172,516 字节 |
| 仓库内运行数据库 | 0 | 本仓库没有可供真实业务状态盘点的 DB 文件 |

Skill 专用内部目录是：

- `internal/skillbindingactivation`
- `internal/skillbindingcontribution`
- `internal/skillbindingcurrent`
- `internal/skillbindingresolver`
- `internal/skillcatalog`
- `internal/skillcontentcatalog`
- `internal/skillcontentlifecycle`
- `internal/skillgovernancebridge`
- `internal/skillpackage`
- `internal/skillrefbridge`

这是 S0 审计时的历史清单。进入 S1 后，`internal/skillbindingresolver`、
`internal/skillcontentlifecycle` 及其唯一生产反向消费者 `internal/membersnapshot`
已经物理退役；语义来源只保留在 S0 离线源码归档中，不再参与当前全仓测试或
生产装配。

结论：当前复杂度主要不是来自核心聊天循环，而是来自旧 Store、证据闭包、
兼容投影、Stage 2/3 shadow 链和 Skill 专用生命周期。S0 不应在这些横向资产
上继续补丁式建设。

## 4. S0 历史生产断点

S0 审计时的生产链存在一个由源码直接证明的闭合断点：

```text
OpenSQLiteRuntime
  -> 初始化旧 RuntimeCatalog 与 builtin pure_chat assembly
  -> 开启 UNIFIED_CORE_V1 Admission
  -> IngestWithProfile 写入 unified Run admission
  -> main 启动旧 runtime.Runtime.ProcessOne
  -> scheduler / execution gate 只允许 LEGACY_COMPAT_V1
  -> ErrUnifiedAssemblyExecutionUnavailable
  -> WAITING_RECONCILIATION / recovery parked
```

直接证据：

1. `internal/store/backend/sqlite_runtime.go` 在生产 Store 启动时调用
   `RequireUnifiedAssemblyAdmission`。
2. `internal/store/sqlite/store.go` 的 `IngestWithProfile` 在该开关生效且
   Profile 为 builtin `pure_chat` 时构造并持久化 `UNIFIED_CORE_V1`。
3. `cmd/freeagent/main.go` 仍然组装 `runtime.Runtime`，并让 task worker 调用
   `engine.ProcessOne`。
4. `internal/store/sqlite/scheduler.go` 的 ready-single 和 continuation 查询
   明确限定 `admission_mode='LEGACY_COMPAT_V1'`。
5. `internal/store/sqlite/run_admission_execution_gate.go` 明确说明该边界只是
   “unified Freeze/authority chain 完成前的临时执行边界”，并对非
   `LEGACY_COMPAT_V1` 返回 `ErrUnifiedAssemblyExecutionUnavailable`。
6. `internal/store/sqlite/store.go` 与 `internal/store/sqlite/multi_agent.go`
   会把相关任务停在 `WAITING_RECONCILIATION`，错误码为
   `UNIFIED_ASSEMBLY_EXECUTION_UNAVAILABLE`。

因此，S0 当时的问题不是“缺少更多 Stage 3 证据”，而是新 Admission 与旧执行
Runtime 没有形成一条可运行的生产链。继续增加 shadow、parent、attestation
或 Skill 专用证据，不能修复这个断点。

S1 的首要验收必须是：

```text
Input -> Admission -> Freeze -> RunManifest -> Universal Loop
      -> Provider -> durable terminal state
```

整条链只能使用新 Core 和新 Current Store，不能在中途退回
`LEGACY_COMPAT_V1`。

当前 S1 候选实现已经把 `cmd/freeagent` 接到 `Current Store → ChatService →
Assembly Compiler → Universal Loop`，并以静态依赖门禁拒绝旧 Runtime、旧 Store、
旧 Channel 和 Legacy execution gate 进入候选二进制。本文仍保留上述 S0 断点作为
决策 provenance；当前是否允许正式切换只由 `CUTOVER_ACCEPTANCE` 判断。

## 5. 逐域语义保留矩阵

| 领域 | 当前证据与问题 | S0 处置 | S1/S2 交付边界 | 对原始设计的影响 |
|---|---|---|---|---|
| Tenant / Workspace 身份 | 旧 Store 已有租户、Workspace、路由与 ACL，但分散在大量表和投影中 | `PRESERVE` + `REWRITE` | S1 只保留最小稳定身份、版本和授权边界 | 不改变“多 Workspace 可自由组合”；删除的是旧持久化形状 |
| Agent 身份与版本 | `examples/agents.seed.json` 和旧 Store 保留 specialist、composite、reviewer 等概念 | `PRESERVE` + `REWRITE` | S1 只需可运行的单 Agent；复合编排进入 S2 | Agent 仍是可成长个体，不再被 Workspace 或某领域知识固化 |
| Profile | `appprofile` 已有 builtin Profile 与快照，但绑定旧 capability 和 legacy provider | `PRESERVE` + `REWRITE` | S1 用最小 `pure_chat` Profile；Profile 只声明装配意图 | 保留“纯聊天或专业模式可配置切换” |
| Workspace 与 Agent 关系 | 旧实现已有 bootstrap agent、关联 Workspace 和只读引用 | `PRESERVE` + `REWRITE` | S1 保留显式 Workspace/Agent 绑定；跨 Workspace 引用按最小权限实现 | 不把 Workspace 重新变成固定人格或知识容器 |
| PortPlan | 旧 schema 使用 modules、skills、mcp_servers 三套特殊 binding | `PRESERVE` + `REWRITE` | 同一精确版本 Port 至多一个 `PortPlan`；Port 版本固定 Binding 基数，数组只表达冻结调用顺序，Binding 只携带 `REQUIRED/OPTIONAL` | 强化统一外挂标准，不增加 Port 版本之外的通用编排语义 |
| Assembly Compiler | `internal/assemblycompiler` 有大量旧 authority/evidence 输入，未接通生产执行 | `PRESERVE` + `REWRITE` | S1 只编译 Pure Chat 必需 Port；输出不可变运行快照 | 保留自由编排，删除证明对象堆叠 |
| RunManifest | 当前存在 V3 shadow 与多层 seal，但生产仍走旧 Runtime | `PRESERVE` + `REWRITE` | S1 生成唯一、最小、可重放但不含动态噪音的 Manifest | 保留冻结与审计，改善 prompt cache 稳定性 |
| MemberExecutionSnapshot | 旧 exact member 与 Skill snapshot 对语义有参考价值，但对象层级过深 | `PRESERVE` + `REWRITE` | S1 只保留运行所需成员快照；Skill exact snapshot 只作 S2 语义输入 | 保留复合 Agent 的成员权重、角色和权限冻结 |
| Universal Loop | `sdk/loopapi` 仅有 `YIELDED`、`WAITING`、`TERMINATED`，且没有生产接线 | `PRESERVE` + `REWRITE` | S1 明确 `WAITING_INPUT`、`WAITING_EXTERNAL`、`WAITING_RECONCILIATION` 和终态 | 保留自主循环，状态更少且可恢复 |
| Pure Chat | 已有 builtin Profile 和本地聊天入口，但 unified Run 无法通过旧执行门 | `PRESERVE` + `REWRITE` | S1 唯一必须端到端跑通的业务模式 | 不削弱后续能力，只先建立可信最小核心 |
| Model Provider / 路由 | 旧 executor、候选路由、strict dispatch 有可复用失败语义，但 authority 对象过多 | `PRESERVE` + `REWRITE` | S1 通过统一 Provider Port 调用模型；稳定前缀与动态尾部严格分离 | 保留多模型个性评测和任务选模空间 |
| Model DispatchAttempt | 已有真实调用账本、结果提交和恢复思想，旧实现分散在 Store 与 evidence 包 | `PRESERVE` + `REWRITE` | S1 使用最小 Attempt 状态机；外部请求前持久化，结果后终态持久化 | 保留成本、UNKNOWN、恢复和审计能力 |
| UNKNOWN | 旧代码已有禁止语义重放、对账与 `WAITING_RECONCILIATION` 语义 | `PRESERVE` + `REWRITE` | S1 对模型外部效果保留 UNKNOWN；同一 Admission 自动重入恢复原 Run，同一逻辑步骤只允许一个 Attempt | 完全符合原设计的安全与成本约束，不新增专用表 |
| 完整 Context Compiler / 85% / 100% | `internal/contextcompiler` 测试证明 85% 压缩、100% 按顺序 Drop，不是全部清空 | `PRESERVE` + `S2_DEFER` | S1 只保留最小 History、稳定 prompt 和必要持久化；完整编译、压缩与 Drop 在 S2 重写 | 保留已经确认的上下文策略，但不扩大 S1 |
| History / 摘要 | 有 checkpoint 与 summary 相关实现，但与旧 Store 强耦合 | `PRESERVE` + `REWRITE` + `S2_DEFER` | S1 只持久化完整 History；Context Summary 随 S2 Context Compiler 接入同一 Store，恢复后不得依赖进程内状态 | 保留降低 token 消耗与污染的方向，不为 S1 新增第 17 表 |
| Prompt cache | 已有 cache control、family 和 usage 统计，但旧 payload 含动态证据 | `PRESERVE` + `REWRITE` | S1 固定稳定前缀，动态 ID 放尾部或排除；冷预热只保留手动可选配置 | 不改变 Agent 判断逻辑，只改善缓存结构 |
| Usage / 成本 | 已有 budget、input/output/cache 字段和测试，但字段语义需要按供应商 usage 统一 | `PRESERVE` + `REWRITE` | S1 记录请求、输入、缓存命中、输出、推理、费用和来源置信度 | 保留成本估算和控制平面决策基础 |
| Module Package / Manifest | 旧 module/profile 注册表可参考，但 Trust、Control、Effect 和 failure policy 权威混杂 | `PRESERVE` + `REWRITE` | 模块只声明需求；Activate 分配 ExecutionClass，消费方 Binding 冻结 AuthorityCeiling 与 `REQUIRED/OPTIONAL` | 提升外挂自由度，同时维持本地权限权威 |
| Install / Activate / Bind | 旧 catalog、activation、binding 有完整但过重的证据闭包 | `PRESERVE` + `REWRITE` | S1 只实现最小可验证生命周期和 Current binding；不带旧 attestation 图 | 保留自由装卸模块，不把模块写死进 Core |
| Module Host | 当前 builtin host 是 capability 特判，第三方任意 Provider 的执行契约不完整 | `PRESERVE` + `REWRITE` | S1 建立统一 Resolve/Invoke 边界；S2 再接更多 Provider 类型 | 为 Skill、MCP、RAG、记忆和未来情感模块提供同一入口 |
| Action / Gateway | 旧 Tool gateway 有副作用门与测试，但通用 Action 执行链未闭合 | `PRESERVE` + `REWRITE` + `S2_DEFER` | S1 仅保留模型调用所需效果安全；S2 固定使用 `ActionProposal → DispatchAttempt → Gateway → Module Host 私有 executor` | 不允许模型直接执行任意外部模块 |
| 第三方 Tool | 当前 Tool 作为 builtin capability 特判 | `PRESERVE` + `S2_DEFER` | S2 作为普通 Port Provider 接回 | Tool 不再是 Core 特殊对象 |
| 声明式静态 Skill | 旧 exact Skill snapshot 对语义有参考价值，但专用对象链过深 | `PRESERVE` + `REWRITE` | S1 只通过通用 Module/Port 接入静态 Skill；旧 exact snapshot 仅作语义来源 | 保留最小静态 Skill，不保留 Skill 专用 Core |
| Skill Draft / 动态生命周期 / 自编写 | 10 个专用目录、139 个文件，生命周期和证据链远超 S1 所需 | `PRESERVE` + `S2_DEFER` + `DELETE` | S2 通过统一模块接口实现 draft、review、activate 与更新；旧专用生产链退出 | 保留可外挂 Skill 和 AI 自编写 Skill |
| MCP | 旧 locked-MCP 规格与 SDK 有发现和 evidence 设计，但属于旧架构 | `PRESERVE` + `S2_DEFER` + `DELETE` | S2 作为协议适配模块，不进入 Core schema | 保留通用 MCP 支持并降低耦合 |
| RAG / 共享知识库 | `knowledge-seed.json`、gateway 和 collection scope 有业务语义 | `PRESERVE` + `S2_DEFER` | S2 作为共享 Knowledge Port；Agent 仅保存轻量命中统计 | 保留共享知识库和按上下文标签检索 |
| Agent 轻量记忆 | 已有 memory store、摘要与 learner，但与 worker 和旧表耦合 | `PRESERVE` + `S2_DEFER` | S2 接入轻量事实、偏好、计数和摘要；领域知识仍归共享 RAG | 避免 Workspace/Agent 内部知识膨胀 |
| Learning PR | 已有学习任务、补偿扫描和审核概念 | `PRESERVE` + `S2_DEFER` | S2 实现提交、去重、拒绝来源封禁、高可信审核和可选周期报告 | 完整保留 GitHub 式自学习治理 |
| 复合 Agent | 旧 team DAG、member snapshot、reviewer 和权重可作语义证据 | `PRESERVE` + `S2_DEFER` | S2 在新 Core 上重新实现结构型复合 Agent与专业型单 Agent | 保留架构型与填充型 Agent 的不同侧重点 |
| 多 Workspace 公平调度 | 当前 scheduler 有 lane/fairness 代码和测试，但 SQL 与 167 表旧 Store 绑定 | `PRESERVE` + `S2_DEFER` | S1 不搬运旧 fairness SQL；S2 与 Scheduler、Reviewer、Parent/Child Run 一起重写 | 防止高流量 Workspace 饿死其他 Workspace |
| 外部 Channel | Telegram、Lark、QQ、Weixin 和 cursor 语义已有实现 | `PRESERVE` + `S2_DEFER` | S1 不连接外部 Channel；S2 按统一 ingress/egress Port 逐个恢复 | 不删除渠道能力，只避免污染 Pure Chat 切换 |
| Channel cursor / 备份 | 已有完整备份纳入 cursor 的测试语义 | `PRESERVE` + `REWRITE` + `S2_DEFER` | S1 验证 Core 备份恢复；Channel cursor 随 Channel 在 S2 接回 | 保留无损恢复目标 |
| 权限 / Trust / Effect | 旧治理对象多，但本地授予、fail-closed、Workspace ACL 语义正确 | `PRESERVE` + `REWRITE` | S1 最小权限矩阵；模块声明不拥有最终授权 | 保留 Agent 对 Workspace 的具体操作权限 |
| 优雅关停 | `service_lifecycle` 已有 ingress、claim、effect 分阶段关闭和 drain 测试 | `PRESERVE` + `REWRITE` | S1 在新 worker/store 上重建同等顺序并做真实恢复测试 | 不影响功能，只防止终态和副作用丢失 |
| 兼容投影 | `assemblycompat`、legacy bootstrap 与多份兼容规格形成第二权威 | `DELETE` | 不进入新 Current Store；首个新 Admission 前只允许整体离线快照回滚，禁止运行时 fallback、旧 Store 依赖或并行 writer | 用户可见语义由新 Core 接管，不再双写 |
| Evidence / attestation 图 | `assemblyevidence`、governance wire、content parents、Stage 2/3 seal 很大但未修复执行断点 | `DELETE` | 新 Core 只保留必要 digest、snapshot 和 audit event | 安全不变量保留，证明对象数量大幅减少 |
| Shadow 链 | `runmanifestv3/pure_chat_shadow_v1.go` 和 composite shadow test 属于旁路验证 | `DELETE` | S1 只有一个正式运行路径，不保留永久 shadow | 避免双架构和双事实来源 |
| 双 Store / 双写迁移 | 旧规格支持 SQLite/PostgreSQL 双后端和双向迁移 | `DELETE` | S1 只允许一个 Current Store identity 和一次性切换 | 后续可重新增加后端，但必须共享同一 Store 契约 |
| 旧 config schema | `schemas/config/v1alpha1` 对 Skill、MCP、module 分别建模并含 legacy evidence | `PRESERVE` + `REWRITE` | S1 只保留 Core、PortPlan、PortBinding；S2 能力复用同一扩展点 | 配置更开放，类型特判更少 |
| 文档与 CI 权威 | README、PRD、maturity 和哈希门禁仍指向旧 P0/Stage 规格 | `REWRITE` + `DELETE` | 两份编码规格和一次性切换清单成为唯一架构权威 | 防止实现被旧文档强制拉回 |

## 6. 旧规格降级清单

`docs/architecture/specs` 下 11 份旧规格不得继续作为当前实现的规范性输入。
S0 按类别降级，不再维护逐文件状态机：

| 旧规格类别 | 当前问题 | 降级后的用途 | 处置 |
|---|---|---|---|
| compatibility、lossless persistence、双后端迁移 | 固化 legacy projection、旧迁移链和双 Store | 提取 fail-closed、幂等、恢复、终态和后端抽象语义 | `PRESERVE` + `DELETE` |
| RuntimeCatalog、governance wire、P0 attestation | authority/evidence 图过重，旧 PASS 被误作当前生产证明 | 提取不可变 catalog、本地授权和权限裁剪语义 | `PRESERVE` + `DELETE` |
| Stage 2 unified assembly、Stage 3 content parents | shadow、parent、seal 继续扩张，但 unified 执行仍断开 | 提取生产断点、必要 digest 与内容寻址语义 | `PRESERVE` + `DELETE` |
| Locked MCP、Stage 3 Skill lifecycle | 把可外挂能力固化成专用 Core 子系统 | 提取 exact version、digest、发现、权限和 review 语义 | `PRESERVE` + `S2_DEFER` + `DELETE` |

还必须同时降级以下入口：

- `docs/NEXT_PHASE_PLAN.md` 中旧 Stage 2、Stage 3、Stage 4 路线；
- `README.md` 和 `docs/PRD.md` 对旧 P0 lock attestation 的权威引用；
- `docs/project-completion-assessment-2026-07-22.md` 的历史完成结论；
- `docs/RELEASE_MATURITY.md` 对 36 项能力使用的旧完成标签；
- `docs/architecture/specs-relocation.v1.json` 对旧规格路径、字节数和摘要的硬锁；
- `scripts/Test-Docs.ps1` 与 `scripts/Test-Docs.Tests.ps1` 中对应的旧规格硬编码；
- `testdata/release/capabilities.v1.json` 中把 S2 能力误列为已完成的条目。

降级后可以保留文件，但必须满足：

1. 文件头明确 `HISTORICAL_NON_NORMATIVE` 或 `S2_SEMANTIC_SOURCE_ONLY`；
2. README、PRD、CI 和新代码不得把它作为当前架构权威；
3. 不再为旧对象补充新 evidence、shadow 或 attestation；
4. 所有保留语义必须重新写入两份编码规格、一次性切换清单或 S2 backlog。

## 7. 证据强弱

### 7.1 证据等级

| 等级 | 定义 | 可以证明什么 |
|---|---|---|
| A | 生产代码路径、持久化实现和可执行测试相互印证 | 当前代码实际行为或明确断点 |
| B | 有实现与单元/集成测试，但未接入唯一生产入口 | 可复用语义，不等于当前生产入口证据 |
| C | 只有文档、矩阵、命名测试或静态声明 | 设计意图，不能证明运行效果 |
| D | 所需正式规格、新 Store 或端到端证据缺失/不完整 | 尚未交付 |

### 7.2 历史 S0 证据与当前 S1 候选状态

| 结论 | 等级 | 证据 | 限制 |
|---|---|---|---|
| S0 时 unified Admission 与 legacy-only execution 不闭合 | A | 历史 backend open、ingest、scheduler、execution gate、recovery 代码 | 历史断点；当前 S1 候选已由唯一 composition/Loop/Current Store 纵链替换 |
| 旧 SQLite schema 过重 | A | 11 个 migration、167 表、108 索引、494 trigger | 历史语义来源；未进入当前生产依赖闭包 |
| 85% 压缩和 100% 顺序 Drop 语义已存在 | A | `internal/contextcompiler/compiler_test.go` | 必须移植到新 Context Store 后重验 |
| UNKNOWN 禁止隐式语义重放 | A/B | runtime、store、reconciliation 代码与测试 | 旧对象模型不能直接进入新 Store |
| 优雅关停顺序和 drain 语义 | A/B | 历史 lifecycle 语义；当前候选 HTTP graceful/forced shutdown 测试 | 已在当前 S1 候选链重验，正式切换仍受 operator gate 约束 |
| 备份、cursor、恢复语义 | A/B | 当前 S1 完整 bundle/restore 测试与历史 Channel cursor 测试 | Core 备份已进入候选链；Channel Cursor 随 S2 Channel 工作包接入 |
| Workspace routing 与 ACL fail-closed | A/B | ingress、workspace catalog 和 Store 测试 | 当前结构与旧 schema 耦合 |
| 多 Workspace fairness | B | scheduler lane 实现与测试 | SQL 强绑定旧 Store |
| Module/Profile 可配置 | B | registry、profile、modulehost 实现与测试 | 当前仍是 builtin capability 特判 |
| Skill exact snapshot 与版本闭包 | B | Skill 专用包和大量测试 | 绝大部分未证明接入唯一生产运行路径 |
| 复合 Agent、team DAG、reviewer、权重 | B | runtime/team 与 member snapshot 测试 | S1 不应携带这些表和 worker |
| 49 项 capability matrix 自洽 | C | `Test-CapabilityMatrix.ps1` 通过 | 该门禁只核对声明与测试名称，不执行对应 Go 测试 |
| 36 项能力的旧完成标签 | C | `docs/RELEASE_MATURITY.md` | 与真实 unified execution 断点冲突 |
| `CORE_RUNTIME_V1` 已冻结 | A/B | 编码规格、唯一生产纵链与定向测试相互印证 | 当前为已验收的 S1 开发基线，不代表发布完成 |
| `CURRENT_STORE_V1` S1 Schema Draft 已实现 | A/B | `0001_current.sql`、Store API、init/open/backup/restore 与恢复测试 | 首次公开发布前仍可按门禁显式重建，Schema v1 尚未发布冻结 |
| `CUTOVER_ACCEPTANCE` 已按“从未部署”收口 | A | 当前证据与 operator 方案 A | 本轮生产切换标记为 `NOT_APPLICABLE`；未来首次部署仍须重新执行门禁 |
| 最小 Core seed 与当前恢复链已存在 | A | canonical seed、精确 artifact 锁、真实 init/chat/backup/restore/restart 测试 | 证明 S1 开发基线，不替代正式部署验收 |

关键判断：测试文件数量和旧文档中的 PASS 不能覆盖生产接线断点。S0 只能把
A/B 级证据中的业务语义带入新规格，不能把旧对象图整体搬入新 Core。

## 8. Seed 与归档资产

### 8.1 现有 JSON seed

| 文件 | SHA-256 | 可保留内容 | 处理结论 / 当前用途 |
|---|---|---|---|
| `examples/current-v1.bootstrap.seed.json` | `19DF7FBCCBECD4B1F72151FB2847F840FC61D8901CCE87C8C524C69B146F1F12` | 单一 Tenant、Workspace、Agent、`pure-chat` Profile、精确 Echo 与声明式 Context artifact 锁、Operator 策略输入、Activation 校验断言和默认装配 | S1 历史输入锁；后续当前值由 `CUTOVER_ACCEPTANCE` 的对应切片锁定 |
| `examples/agents.seed.json` | `2403CC966A39292D4FB11605C845C7B1557747190078C6499C299449D67016DD` | specialist、composite、reviewer、权重与 traits 样例 | 依赖旧 trust、collection 和 builtin legacy assistant |
| `examples/knowledge-seed.json` | `35970C7BB94967F5CA9D2110054B12ADB0036795D3AB3435A46A13708AC12517` | 共享知识条目格式 | RAG 属于 S2 |
| `examples/workspaces.local.seed.json` | `F6965AEC20325798FB4C01B9BB66B16E39FCB0ACBA61A4EBD6EE089C0E858DE5` | 最接近本地 Pure Chat 的 Workspace 样例 | 仍带 Channel identity/route，且缺 Tenant、Profile、Catalog 闭包 |
| `examples/workspaces.seed.json` | `35F4C9AB0F90DF1F5F1681F1A6CE41E29A5B0201C328FEF8270893923CD2265B` | 多渠道 Workspace 配置样例 | Telegram、Lark、QQ、Weixin 属于 S2 |

S1 历史 `current-v1.bootstrap.seed.json` 已覆盖最小 seed 结构，并绑定两个普通模块；
其中当时的模型锁为：

- `freeagent.builtin.model.echo@1.0.0`：
  `ArtifactDigest=ba6d32d3146bfe763ccdc3aa1cec1c17a6d13fdb559c73211058be460c001742`；
- `freeagent.builtin.context.basic@1.0.0`：
  `ArtifactDigest=5fd284dffdd51e8398d147b38ca220fbb939cec53a750b8189904483ee03d526`。

`ExecutionClass` 与 `AdapterIdentity` 只作为本地 Core 重算后的校验断言，不是
seed 自授权。当前已确认：

- 一个 Tenant；
- 一个 Workspace；
- 一个 Agent；
- 一个 `pure_chat` Profile；
- 一个最小 Model Provider 和一个可选声明式 Context Provider；
- 所需最小 install、activation、bind；
- 明确的权限、资源、成本、调度和配置策略输入；policy alias 不是运行时 typed ref；
- 零 Channel、零 RAG、零 Memory、零 MCP、零 Tool；
- 不包含凭证值；
- 固定顺序与规范 JSON 编码。

ModelProfile 发布前合同修订后，当前 seed SHA-256 为
`34F712989FD31120C2C4CCC026D36ED9E365813747E6DE8BF2CA85AF9DDB5A3D`；它仍默认不绑定
画像，但模型 Binding 新增精确 `model_build_id`。对应 Echo artifact 已重算为
`884C16B339B6F9154A6F64B23E800010F0B29074C1AD9A01C540F9C9ED9182F7`，package bytes
为 `36,320`。项目从未正式部署，因此这是一次显式开发基线重建，不修改上述 S1
历史证据锁，也不引入兼容 decoder。

历史 S0 Draft 恢复演练已执行；同一结构现已由当前 S1 生产 composition 与命令链
完成 `init → chat → backup → backup-verify → restore → chat` 自动重验：

```text
empty Current Store
  -> import seed
  -> close
  -> reopen
  -> export/read canonical snapshots
  -> compare bytes and digests
```

以下报告只记录历史 S0 rehearsal，保存在仓库外的离线 snapshot root，实际本机
路径不进入公开树。历史报告 SHA-256 为
`9400B8CF4AA20D1E796DE4075B9642F56C7171D9282316B9BC0903794AC10ADB`。
16 表 Draft 的两次 seed rebuild 得到相同 canonical 业务语义。当前 S1 候选已在
真实 `0001_current.sql`、Current Store API 和 CLI/HTTP composition 上重新证明
Store identity、no-overwrite、Admission 幂等、Control/Catalog 摘要、Operator
策略输入、空 SecretRef 与无副作用上限。历史 seed 验证报告 SHA-256 为
`9CDE7646B794CB7E09EBE191839CA797DE569CE776A06E13549E6E03FB7B96FF`。

当前 `cmd/freeagent/main_test.go`、`long_chain_test.go` 与 `currentbackup` 测试共同覆盖
真实 init/chat/retry/restart/backup/restore；严格 seed 解码与幂等测试仍只证明各自
边界，不能单独替代完整纵链。

### 8.2 源码归档

`scripts/New-PublicStaging.ps1` 与 `scripts/Test-PublicStaging.ps1` 已具有逐文件
SHA-256、文件树 seal 和 manifest 思路，可以作为 S0 源码封存脚本的语义来源。

`scripts/New-CIReleaseWorkspace.ps1` 依赖 Git revision，而当前目录没有 `.git`，
不能直接作为本次 S0 归档入口。

S0 归档要求：

1. 从同一静止源码快照生成 archive；
2. archive 外部再计算一个 SHA-256；
3. 记录生成时间、文件数量、排除规则和主方案摘要；
4. archive 不包含运行数据、凭证、构建缓存；
5. 在开始 S1 代码切换前完成一次解包校验。

当前最终 S0 离线基线为
`freeagent-s0-final-baseline-20260730T022135.zip`，SHA-256 为
`C51EDA49D492D49E26523411A9C9D76F9EC9D61059F8978C29920CC651414237`。
较早的 `freeagent-s0-source-prebaseline-20260729T222052.zip` / `39E806...` 只作为
prebaseline 历史记录，不再作为当前切换基线。

当前仓库没有 `.db`、`.sqlite` 或 `.sqlite3` 文件，因此仅凭源码仍不能宣称真实部署
Store 已清空、已迁移或无 UNKNOWN。S0 的仓库/语义基线已经完成；项目所有者随后在
`CUTOVER_ACCEPTANCE` 方案 A 中确认从未正式部署，本轮生产切换因此标记为
`NOT_APPLICABLE`。未来首次
部署仍需重新执行路径、writer 与外部状态门禁。

## 9. S0 收口时门禁结果

以下是 S0 收口时的审计结果，不代表当前树的最终发布门禁状态：

| 门禁 | 结果 | 解释 |
|---|---|---|
| Branding | PASS | 只证明品牌检查通过 |
| Capability matrix | PASS | 49 项声明自洽；不执行对应 Go 测试 |
| License | PASS | 174 项门禁自测通过；真实仓库闭包为 37 个 Go 依赖、33 个分发资产 |
| Docs | PASS | 158 项门禁自测通过；当前规格权威索引和历史降级闭合 |
| Public tree | PASS | 222 项门禁断言通过；真实公开树扫描为 `PUBLIC_TREE_PASS` |
| 规格交叉评审 | PASS | S0 当时的交叉评审已收口；后续变更仍须重新检查主方案与两份编码规格的一致性 |
| Store Draft 演练 | PASS | 16 表、两次重建、备份恢复、no-overwrite 与 7 个 PolicyRef 均通过 |

门禁修复没有通过白名单掩盖真实问题：历史规格已经退出当前权威，私有绝对路径
夹具已改为运行时构造；SecretRef/SecretBinding 只允许精确的引用元数据表达式，
直接字符串、拼接字符串和未知 accessor 仍由负例证明会被拒绝。

## 10. S0 最小交付清单

S0 只交付以下内容：

1. 本语义保留矩阵；
2. `CORE_RUNTIME_V1`；
3. `CURRENT_STORE_V1` 的 S1 Draft；
4. `CUTOVER_ACCEPTANCE`；
5. 一份源码 archive 及其 SHA-256；
6. 一份单一、确定性、最小 JSON seed；
7. 一次 empty Store 恢复演练记录；
8. 一份把真实数据路径、PENDING、UNKNOWN、outbox 和外部 effect 盘点转交给
   `CUTOVER_ACCEPTANCE` 的显式 NO-GO 清单；
9. 一份旧规格降级和旧 CI 权威解除记录；
10. 一份 S1 开工前门禁结果。

S0 明确不交付：

- 新的 Stage 2/3 evidence；
- 新的 shadow compare；
- 新的 Skill 专用生命周期；
- 新的 MCP、RAG、Memory、Learning PR 或 Channel 生产接线；
- 新的双写、兼容投影或双 Store；
- S1 业务代码、Schema identity 或生产入口切换。

## 11. S0 验收条件

只有同时满足以下条件，S0 才能标记完成：

1. 两份编码规格和一份切换清单存在，并且名称、对象、状态和错误语义一致；
2. README、PRD、maturity、CI 不再把旧 P0/Stage 规格当作当前权威；
3. 每个旧领域都在本矩阵中有明确处置，不存在“先保留以后再说”；
4. 新 seed 能在空 Store 上恢复，并在 reopen 后得到相同 canonical bytes/digest；
5. 源码 archive 可解包、可核验，摘要已记录；
6. 仓库内状态盘点完成；无法由源码证明的真实路径、writer、UNKNOWN、PENDING、
   outbox 和外部 effect 已作为 `CUTOVER_ACCEPTANCE` operator gate 明确保持 NO-GO；
7. Docs、License、Public tree 门禁通过，或在
   `CUTOVER_ACCEPTANCE` 中有逐项、限时、非放宽式豁免；
8. 没有任何 S1 Run 会写入旧 Store；
9. 旧二进制如需保留，只能在首个新 Admission 前参与整体离线快照回滚，不能成为
   运行时 fallback、旧 Store 只读依赖或并行 writer；
10. S0 基线验收前没有开始 S1 代码切换。

## 12. 原始设计影响检查

| 原始设计意图 | S0/S1 影响 | 结论 |
|---|---|---|
| Agent 与 Workspace 自由编排 | 身份和绑定重写为最小 Current Store；不再依赖 legacy profile/assembly 表 | 保留 |
| 角色、记忆、知识库可选 | S1 只有 Pure Chat；Memory/RAG 在 S2 通过普通 Port 接回 | 保留且边界更清楚 |
| Skill 与 MCP 可自由外挂 | 删除 Skill/MCP 专用 Core，统一为 Module + PortPlan + PortBinding | 保留并增强开放性 |
| 可扩展第三方模块 | Module 只能声明需求，本地策略授予权限，Gateway 控制副作用 | 保留并提高安全性 |
| 未来可外挂 PAB 等人格情感模块 | Core 不增加任何 PAB 专用表、状态或协议；未来走统一 Port | 保留未来扩展，不引入当前耦合 |
| 单领域强 Agent 与复合架构 Agent 并存 | S1 只验证单 Agent；权重、成员快照和 team DAG 语义进入 S2 | 保留，实施后移 |
| 多 Workspace 并行与公平 | S1 不搬旧 fairness/team slot SQL；S2 与可信 Scheduler 一起重写 | 保留，实施后移 |
| 共享 RAG，Agent 内部轻量 | RAG 和学习进入 S2；Core 只保留未来 Port 和轻量身份 | 保留 |
| 自学习采用 PR、去重、拒绝来源不再使用 | 作为 S2 Learning Port 的强不变量，不进入 S1 | 保留 |
| 85% 压缩、100% 顺序 Drop | S2.1 已在唯一 Context Compiler/Store/Loop 中收口；不删除持久 History | 保留并已实现首个纵链 |
| 多模型评测、画像与上下文工程 | 可选 ModelProfile 已冻结精确模型构建并只收紧 ContextPolicy；自动评测/选模仍延后 | 保留并已实现最小纵链 |
| 缓存命中和成本统计 | 稳定 prompt 前缀、真实 usage、费用和手动 cache 优化保留 | 保留 |
| UNKNOWN 不语义重放 | 进入 Core effect 状态机 | 保留 |
| 默认不自动生成预热请求 | 冷 family 预热不进入自动控制闭环，仅可手动开启并估算成本 | 保留 |
| 高度自由但 Core 轻量 | 大量专用 evidence、shadow、兼容表和双 Store 退出生产 | 明显改善 |

总体结论：本次减重不删除原始产品方向；删除的是旧实现把每种能力做成 Core
特例、专用证据链和专用 Store 对象的方式。S1 先证明一个最小、唯一、可恢复
的 Pure Chat Core，S2 再通过同一 Module/Port 标准逐项恢复专业能力。

## 13. S0 收口后约定的 S1 路径

S0 当时要求只按 `CORE_RUNTIME_V1`、`CURRENT_STORE_V1` 和
`CUTOVER_ACCEPTANCE` 实施 S1 原子 Core/Store 切换。当前 S1 开发基线已经验收；
operator 方案 A 确认项目从未正式部署，因此本轮生产切换标记为
`NOT_APPLICABLE`。S2.1 Context Compiler、ModelProfile、首个本地 RAG、Agent 轻量
Memory 与 Action/Gateway 纵链已按主方案收口，下一原子纵链为 MCP；仍不得双写旧 Store、恢复 Legacy Runtime 或并行
预建后续 S2 能力。
