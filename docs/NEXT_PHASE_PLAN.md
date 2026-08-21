# FreeAgent 后续实施计划

> 状态：`HISTORICAL_BASELINE_NON_NORMATIVE`  
> 更新日期：2026-07-28  
> 当前用途：仅供旧阶段、旧实现和语义回溯，不再指导当前实施。  
> 当前架构权威：
> [`CORE_RUNTIME_V1`](specs/CORE_RUNTIME_V1.md)、
> [`CURRENT_STORE_V1`](specs/CURRENT_STORE_V1.md) 与
> [`CUTOVER_ACCEPTANCE`](CUTOVER_ACCEPTANCE.md)。
> 当前已经验收 S1 development baseline，Current Store 仍是首次公开发布前的
> Schema Draft；项目从未正式部署。本文归档时刚进入 S2.1；后续当前状态只以
> 上述三份权威文档为准。本文仍不能证明生产部署完成，也不再指导实施。

## 1. 目标

FreeAgent 将保持为一个默认轻量、可自由装配、可审计并可恢复的 Agent
运行核心：

- 新安装默认使用 `pure_chat`，不隐式加载 Role、RAG、Memory、Skill、
  MCP、Tool、可选 Host 或扩展 SecretRef；
- Agent、Workspace、共享授权 RAG、专业 Agent、复合 Agent、Reviewer 和
  多 Agent 协作继续独立组合；
- 旧方案曾把可信第一方扩展称为 `IN_PROCESS + CORE_TCB`；该名称已经退役，当前
  授权以本地 Activation、ExecutionClass、精确 Port 规则和 Binding 上限为准；
- 外部进程和远程扩展统一通过 Operator 显式注册的 MCP Server 接入；
- 配置激活只影响新 Run，活跃 Run 始终使用冻结快照；
- 所有模型、Tool、MCP 和 Channel 外部效果经过权限、预算、调用账本和
  `UNKNOWN` 边界。

本文曾是公开源码树的人工路线图，现在只保存旧阶段边界和语义来源。不得根据
本文继续扩建旧 Stage、evidence、shadow 或 Skill-specific 链。旧能力证据索引
见历史基线 [`RELEASE_MATURITY.md`](RELEASE_MATURITY.md)，但该矩阵不证明当前
生产入口已接通。

## 2. 不变量

后续任何实现不得破坏以下约束：

1. 同一 AgentVersion 可以进入不同 Workspace，但权限、预算、知识范围和
   装配结果按 Run 独立冻结。
2. Role、Persona、Knowledge/RAG、Memory、Skill、MCP、Tool 和 Team 都是
   可选模块；内容声明不能自行授予执行权限。
3. `pure_chat` 必须能够在零可选附件下完成普通多轮聊天。
4. 共享知识按 Tenant、Workspace、集合和数据作用域授权；Agent 不复制整套
   领域知识。
5. 上下文使用率达到 85% 时压缩；达到或超过 100% 时，只按时间顺序移除
   恢复到目标阈值所需的最小完整旧前缀，不删除持久化历史。
6. 外部效果终态不确定时进入 `UNKNOWN`，禁止语义重放。
7. Skill 和 MCP 不能自动下载、自动安装、自行扩权或绕过当前 Gateway；本文中的
   旧 `ToolGateway` 名称不再定义当前组件。
8. 当前配置通过不可变 Generation 与 CAS 激活；回滚创建新 Generation，
   不覆盖历史。
9. 新配置不静默改变活跃 Run；撤权可以立即阻止尚未准入的新调用。
10. SQLite 是默认后端；离线 CLI 用于灾难恢复、迁移、备份和人工对账，
    日常变更最终进入经过认证和授权的在线控制平面。

## 3. 阶段一：减重与默认边界

状态：已完成。全量 Go、文档、许可证、PublicTree 和能力矩阵门禁已通过；
独立审计未发现 Critical、Important 或 Minor 残留。

交付项：

- 移除旧专属 Channel 生态绑定和迁移入口，同时保留通用 Channel、Cursor
  CAS、Outbox、Effect Ledger、备份恢复及 Redirect 防护；
- 扩展执行模式收敛为可信进程内模块和显式 MCP；
- 删除具体外部情感项目的绑定，保留通用 Role 与 Persona；
- 新数据库默认审计激活 `pure_chat`；
- 仅迁移仍精确匹配历史系统默认的数据库，不覆盖 Operator 的显式选择；
- 使用中立 `test-channel` fixture 保留 Cursor、备份、恢复和去重覆盖。

退出条件：

- `pure_chat` 无任何可选附件或扩展凭据；
- 历史 Run 和显式 `legacy_full` 仍可恢复；
- 全量 Go 测试、文档、许可证、PublicTree 和能力矩阵门禁通过；
- 退役代码、活动文档、示例和发布声明无残留。

对原始设计的影响：默认路径更轻，扩展边界更通用；Agent、Workspace、RAG、
Role、Skill、MCP 和多 Agent 编排能力不减少。

## 4. 阶段二：统一装配与在线激活

状态：装配基线与 `pure_chat` V3 诊断影子已达到进入阶段三的出口；
最终 V3、生产切换和在线激活仍是跨阶段收敛项，等待阶段三/四的 sealed
resolver parents，不作为阶段三的前置阻塞。

当前实施切片：

- 最小 Core Loop 已与可选模块 Host 分离，`pure_chat@3` 为零附件装配；
- RuntimeCatalog 支持无 Skill 锚点的真实 Genesis；
- WorkspaceDefinition、AgentVersion、AssemblyProfile 和 TaskRequirements
  已进入 tenant-first、append-only、精确 Hash 加载的不可变仓库；
- Core Loop Assembly Compiler 已实现 availability 与 selection 分离；
- TaskAssemblySelection/Receipt、Workspace 级 Activation Generation、
  V2 影子冻结闭包及治理/模型 prewire 边界已完成；
- 仍缺 legacy-free Activation/Team/Selection/Receipt/member-snapshot、
  authenticated compiler、阶段三/四 sealed parents、final V3 持久化与
  唯一生产 Runtime 接线。

冻结格式与切换顺序见
[`2026-07-26-stage2-unified-assembly-cutover.md`](architecture/specs/2026-07-26-stage2-unified-assembly-cutover.md)。

### 4.1 单一装配事实源

目标链路：

```text
Operator configuration
        |
        v
RuntimeCatalog Generation
        |
        v
Assembly Compiler
        |
        v
MemberSnapshot / RunManifest
        |
        v
Runtime execution
```

实施项：

- RuntimeCatalog Generation 成为可用模块集合的唯一事实源；
- 内置 Profile 降为 Catalog 的受控输入，不再维护平行运行状态；
- Assembly Compiler 将 Workspace、AgentVersion、Profile、权限和候选附件
  编译为 MemberSnapshot；
- RunManifest 冻结完整 Agent、Workspace、Role、Memory/Context Policy、
  Generation、Skill、MCP、Team、Model Route、RAG、Authority 与 Budget 引用；
- 保留最小单 Agent Core Loop，使 `pure_chat` 不依赖任何可选 Orchestrator。

### 4.2 在线控制平面

在线 API 管理版本导入、审核、Stage、Generation 激活、回滚、撤销和
RestartRequest。所有写操作必须认证、授权、审计并使用幂等键；日常控制
不再要求停止 `serve` 后直接修改 SQLite。

### 4.3 热激活

- 激活以 CAS 切换当前不可变 Generation；
- 并发激活只有一个胜者；
- 新 Run 获取新 Generation，旧 Run 继续使用原快照；
- 激活失败不改变 current；
- 回滚创建新的回滚 Generation；
- 旧 Generation 只有在无 Run 和 Host lease 引用后才可回收。

退出条件：

- 在线并发激活、失败关闭、回滚、撤权和重启恢复测试通过；
- 冻结 Run 不发生配置漂移；
- `pure_chat` 仍保持零可选附件；
- 不再存在第二套可独立改变运行装配结果的事实源。

对原始设计的影响：只消除重复状态和停服配置，不改变自由编排语义。

## 5. 阶段三：Skill

状态：执行中。当前只实施不占用 schema v12、不中途接入生产 Runtime 的
sealed Skill/content/RAG parent 切片；现有非空 Skill/content
`MemberSnapshotV2` 门禁继续失败关闭。

阶段边界以
[`2026-07-28-stage3-sealed-content-parents.md`](architecture/specs/2026-07-28-stage3-sealed-content-parents.md)
为准。阶段二最终 V3 属于阶段三/四父对象齐备后的跨阶段收敛，不再形成
“阶段三等待最终 V3、最终 V3 又等待阶段三”的循环依赖。

当前完成：

- neutral Skill catalog ref 与严格双向转换；
- `skillcatalog -> governance` 依赖解环和上层 proposal bridge；
- 历史结构 V1 audit Restore、规范输入 wire/hash golden 与 V2
  非空 Skill 永久失败关闭；
- ContentPayload blob identity、Skill semantic package/manifest digest、
  `SKILL.md` payload 和资源后续独立闭包的精确字节锁。
- `ContentPayloadV1`、`SourceTagSetV1`、
  `ImmutableArtifactViewV1` 与 `SkillPackageEvidenceV1` 完整双父闭包；
- 唯一 lower `skillrefbridge`、历史 Unicode audit carve-out、新 authoring
  失败关闭，以及 Windows/WSL race/全仓非 SQLite 回归。
- `SkillPublicInternalRefMappingV1`、sealed catalog membership/generation/ref
  与 process-local exact membership view；完整父 Restore、规范 V1
  mapping/ref 字节兼容、确定性排序/去重、空 generation 历史和
  non-authorizing 方法集均已锁定。

当前实施：生命周期历史、审核结果、current activation CAS 与撤销检查。
已完成的 portable parents 只证明内容完整性和精确目录成员关系，不代表审核、
激活、current、上下文加载或执行权限。当前 exact lookup 在每次查询前完整
验证 generation；接入 Stage 3-B 生产 resolver 前，current resolution 必须在
一次完整验证后构建可复用的进程内 exact index，不能用索引跳过父链验证。

实施项：

- 持久化 Skill 内容、版本、来源、资源摘要、权限请求和生命周期；
- 支持 Operator 本地路径导入或上传，不在安装期执行包内代码；
- Agent 只能创建 Candidate，必须由高可信 Reviewer 或 Operator 审核；
- 来源与内容指纹去重，无内容变化不创建新版本；
- 已提交来源不重复提交，被拒绝来源默认永久阻断；
- 支持 Workspace、Agent 和 Profile 的 allow/deny 与挂载；
- Generation、MemberSnapshot 和 RunManifest 精确固定 Skill revision/hash；
- 按任务标签、关键词、风险和置信度匹配，按需读取 `SKILL.md`；
- 设置 Token、Top-K、递归深度和重复调用预算；
- 使用、降级、失败、回滚、撤销和恢复均写入审计。

退出条件：

- `pure_chat` 不扫描、不加载也不创建 Skill；
- Skill 内容不能扩大 Tool、网络、文件、Workspace 或 Secret 权限；
- 导入、审核、匹配、预算、拒绝、撤销和崩溃恢复测试通过。

对原始设计的影响：实现可治理的自生成能力，同时保持 Skill 可选和权限分离。

## 6. 阶段四：MCP

状态：待阶段三完成。

实施项：

- 建立 MCP Server Catalog 和显式注册审核流程；
- stdio 必须固定绝对 executable、参数、构件摘要和受控环境变量；
- HTTP 必须固定端点、传输策略和 SecretRef；
- 完成 initialize、Tool Discovery、内容哈希和审批映射；
- Discovery 漂移时失败关闭，不把发现结果直接转成权限；
- 将批准的 Tool 接入 ToolGateway、DispatchAttempt、Effect Ledger 和终态；
- 支持超时、取消、输入输出限制、并发上限、排空、撤权和崩溃恢复；
- `UNKNOWN` 禁止语义重放；
- 增加 `freeagent mcp test` 诊断入口；
- Generation 激活后，新 Run 使用新连接或新子进程。

禁止 PATH 模糊查找、Shell 拼接、`latest`、包管理器自动安装、网络自动下载
和 Server 自行扩权。

退出条件：

- stdio 真实子进程与 Streamable HTTP 真实会话测试通过；
- 子进程无孤儿、凭据不进入冻结明文、Redirect 和私网策略有效；
- DispatchAttempt、终态、`UNKNOWN`、恢复和撤权证据完整。

对原始设计的影响：提供通用外挂执行入口，不让外部能力进入可信核心。

## 7. 阶段五：生命周期与重启

状态：待阶段四完成。

实现三种模式：

- `NONE`：只激活 Generation，不重启服务；
- `CORE`：停止新准入、排空已准入调用、持久化终态、退出 Core，由部署
  Supervisor 拉起并重新验证 MCP；
- `FULL`：在 `CORE` 基础上停止所有 FreeAgent 管理的 MCP stdio、Channel
  会话、Watcher、Indexer 和 Worker，再整体恢复。

远程 MCP Server 只重新连接，不尝试重启。`FULL` 不删除数据库、知识、
记忆、Artifact、审计或调用账本。

退出条件：

- RestartRequest、排空状态和恢复证据可查询；
- 无重复外部效果、无丢失终态、无法确认的调用进入 `UNKNOWN`；
- Core 管理的子进程不成为孤儿；
- `NONE`、`CORE`、`FULL` 的权限和生效边界均有故障注入测试。

对原始设计的影响：增加运维选择，不改变 Agent 或 Workspace 业务语义。

## 8. 阶段六：发布与长链路

状态：待前五阶段完成。

交付项：

- 用当前规范替换过期历史架构记录；
- 更新 README、PRD、总体架构、RuntimeCatalog、Skill、MCP、热激活、
  Restart、RunManifest、权限模型和示例配置；
- 能力矩阵只按真实生产接线和证据晋级；
- 运行全量、shuffle、race、vet、gofmt、跨平台构建、故障注入、
  多 Workspace 长期公平调度、真实聊天/编程、Usage/Cache/Cost 对账；
- 运行 PublicTree、License、文档和供应链门禁；
- 重新生成公开发布封印和 Alpha 候选。

Skill 与 MCP 的成熟度规则：

- 只有类型或单元测试：`planned`；
- 有真实本地 Host 和生产接线：`experimental`；
- 权限负例、崩溃恢复、长链路与跨平台证据齐全后才可考虑 `stable`。

## 9. 当前能力条目

阶段性能力 ID：

- `first-party.chat.default-pure-profile`
- `core.catalog.online-activation`
- `core.catalog.generation-hot-swap`
- `extension-host.skill.local-governance`
- `extension-host.skill.runtime-loading`
- `extension-host.mcp.explicit-registration`
- `extension-host.mcp.runtime-execution`
- `extension-host.mcp.lifecycle-recovery`
- `core.lifecycle.restart-modes`

新增 ID 默认从 `planned` 开始，必须有可定位的生产代码、命名测试和恢复证据
才能晋级，不能用 Wire、Schema 或测试 Helper 代替可用性。

## 10. 最终验收

全部阶段完成时，FreeAgent 应满足：

1. 既能作为普通聊天机器人，也能通过显式装配成为单领域、多领域或多 Agent
   系统；
2. Agent 保持轻量，领域知识通过共享授权 RAG 按需检索；
3. Role/Persona、Memory、Skill、MCP、Tool 和 Team 均可选、可版本化、
   可撤销；
4. 配置在线激活且不会污染活跃 Run；
5. 外部效果具有真实调用账本、明确终态和可恢复的 `UNKNOWN`；
6. Usage、Reasoning、Cache Token 和费用可以持续对账；
7. 公开代码、文档、许可证、能力矩阵和发布证据彼此一致。
