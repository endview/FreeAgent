# FreeAgent 产品需求文档

状态：`W6_5_SERVER_OWNED_MODULE_ARTIFACT_INGRESS_ACCEPTED_DEVELOPMENT_SLICE / W6_6_SERVER_OWNED_MODULE_UPGRADE_REVIEW_NEXT`；W6-1/W6-2 的默认关闭 Control、bootstrap/session、Modules read/Dry-run、窄 `MODULE_DISABLE` confirmation/mutate、durable receipt 与 APPLIED 同事务 publication 保持不变。W6-3 exact Overview 与 W6-4 strict Modules UI 继续作为历史 accepted 切片。W6-5 只新增默认关闭的可信本地 Operator CLI `module-artifact-ingress`：source/artifact roots 是瞬时可信输入，调用方只选择 Store-owned current Snapshot 中 unsigned `LOCAL_DIRECTORY + DENY` 的 exact entry；无 HTTP/upload、caller package path、URL、signature 或 UI。构件先以 content-addressed、durable、no-replace 方式发布，随后唯一 Current Store 在同一事务写入 inert Artifact 与 append-only Admission；不 Install、Activate、Bind、grant、Review 或 execute。Backup closure 是 installation ∪ ingress，并支持 Source 离线、零 Installation 的恢复。Current Store 为 43 tables / 25 explicit indexes / 64 triggers，fingerprint `47981b9bf147ec81c6e98382f281db0d5395187a1f090f7ce183c116fba82a0d`，migration 150,301 bytes / SHA-256 `6def433a59fa8f4876894572f1610cae499abc5b389ab5930ea697055419ca86`。前序 W2/W5/W6 历史边界、未正式部署和非生产成熟度判断均不变。  
W2-U2 历史状态：`W2_U2_DISCOVERY_SNAPSHOT_ACCEPTED_DEVELOPMENT_SLICE`  
Reviewer status：`S3_COMPOSITE_REVIEW_GATE_ACCEPTED_DEVELOPMENT_SLICE`  
Conversation status：`W1_COMPLETE_REAL_DEEPSEEK_50`  
RAG/Memory status：`W3_COMPLETE_ACCEPTED_DEVELOPMENT_SLICE`  
Learning status：`W4_L0_CONTRACT_ACCEPTED / W4_L1A_KNOWLEDGE_PROPOSAL_STORE_ACCEPTED_DEVELOPMENT_SLICE / W4_L1B_STATIC_SKILL_PROPOSAL_STORE_ACCEPTED_DEVELOPMENT_SLICE / W4_L2_LEARNING_REVIEW_ACCEPTED_DEVELOPMENT_SLICE / W4_L3_LEARNING_VERSION_ACCEPTED_DEVELOPMENT_SLICE / W4_L4_LEARNING_CYCLE_ACCEPTED_DEVELOPMENT_SLICE / W4_COMPLETE_ACCEPTED_DEVELOPMENT_SLICE`  
Collaboration status：`W5_F1_COLLABORATION_STABILITY_ACCEPTED_DEVELOPMENT_SLICE / W5_COMPLETE_ACCEPTED_DEVELOPMENT_SLICE / W6_NEXT`  
W6-0 historical Control API contract status：`W6_0_CONTROL_API_CONTRACT_ACCEPTED_DEVELOPMENT_SLICE / W6_1_APPLICATION_SERVICES_READ_API_NEXT`  
W6-1 historical Control Application Services status：`W6_1_APPLICATION_SERVICES_READ_API_ACCEPTED_DEVELOPMENT_SLICE / W6_2_CONTROLLED_MUTATIONS_AUDIT_NEXT`  
W6-4 historical close：`W6_4_MODULES_CONFIGURATION_UI_ACCEPTED_DEVELOPMENT_SLICE / W6_5_SERVER_OWNED_MODULE_ARTIFACT_INGRESS_NEXT`  
Current incremental status：`W6_5_SERVER_OWNED_MODULE_ARTIFACT_INGRESS_ACCEPTED_DEVELOPMENT_SLICE / W6_6_SERVER_OWNED_MODULE_UPGRADE_REVIEW_NEXT`  
W6-2 historical close：`W6_2_MODULE_DISABLE_MUTATION_WIRING_ACCEPTED_DEVELOPMENT_SLICE / W6_3_WEB_SHELL_READ_ONLY_OVERVIEW_NEXT`  
日期：2026-08-17  
许可证：AGPL-3.0-only

W2-R2 WASM Host 尚未生产可用。

以上 `accepted/complete` 只表示本文明确范围内的开发纵链和门禁已闭合。FreeAgent 从未正式
部署，当前不可直接用于生产，也不是公开 Beta；W4-L1A/L1B 只完成 Knowledge 与静态 Skill
Draft 的 Proposal Store，W4-L2 只验收单次有界审核，W4-L3 只验收无权限不可变 Version 与
Operator artifact 交接，W4-L4 只验收默认关闭、显式启停和 Tick 的有界 Learning Cycle、
Store-only 补偿及只读报告。W5-X1 只验收同 Tenant、显式 Decision、双边授权的受限跨
Workspace 纵链与最多一次预冻结修复；W5-F1 只补齐该边界的公平调度、顺序、取消、UNKNOWN、
恢复、权限和零 Transfer 稳定性门禁。远程向量数据库、自动 Review/Materialize/Apply、安装、
激活、绑定、扩权、后台周期 worker、自动 Reviewer、无限返工和动态图仍未实现。项目从未正式
部署，不是公开 Beta，也未达到 `RELEASE_READY`。W2-U3 的 `APPROVE` 只是无权限审核事实，
`REJECT` 是需要显式确认的 Tenant-wide supply deny；两者都不发布模块。W2-E4 只验收同一内建 DeepSeek
Artifact/Adapter/Provider Instance 内 flash/pro 的 Operator 显式替换；它不是通用 Model
marketplace，也不允许在 UNKNOWN 后换模型或创建替代 Attempt。W2-R1 只验收上述 exact REMOTE
Action HTTP Host 的开发切片；它不代表真实公网 HTTPS 第三方互操作或生产网络 SLA。W2-R2 另行
验收 exact WASM Action Host，W2-R3 只在该 Host 上增加第三方纯计算授权与停机撤权；二者都不代表
任意 WASM、OS/container、生产恶意多租户隔离、供应链或公开发布已经完成。

当前架构已完成 S0，并验收可运行的 S1 开发基线。当前权威是
[`CORE_RUNTIME_V1`](specs/CORE_RUNTIME_V1.md)、
[`CURRENT_STORE_V1`](specs/CURRENT_STORE_V1.md) 与
[`CONTROL_API_V1`](specs/CONTROL_API_V1.md)，以及
[`CUTOVER_ACCEPTANCE`](CUTOVER_ACCEPTANCE.md)。Current Store 仍是首次公开发布前的
Schema Draft，但当前 Pure Chat 纵链、S2.1 Context Compiler、可选 ModelProfile、
首个本地共享 RAG、Agent 轻量 Memory、Action/Gateway、MCP 本地 stdio Tool、默认关闭的
exact REMOTE Action HTTP Host、exact WASM Action Host、Workspace-scoped loopback Channel、Parent/Child + Specialist Composite 第一纵链，
默认关闭的 S3-A 公平 Scheduler、可选的 S3-B Reviewer 审核门、W4-L1A 至 L4 的
Proposal、Review、Version/Operator 交接和显式 Learning Cycle、W5-X1 的有界
Decision/repair 与受控跨 Workspace 纵链，以及 W5-F1 协作稳定性已经实现。W4-L2 Learning Review 独立于 S3-B，
W4-L4 也只在显式 Tick 时复用普通 model-only Run。Action 首片
只启用本地、确定性、无副作用的 `text.stats`，但已经闭合 Proposal、DispatchAttempt、
Gateway 私有 executor、UNKNOWN、模型二和完整备份恢复。MCP 首片只是该 Action 边界的
Operator-fully-trusted `LOCAL_PROCESS` Adapter，不增加 Port、Runtime、Store 或效果账本；R1 的
REMOTE Host 也只为 exact `freeagent-action-http/v1` 提供默认关闭的 Core-owned native Adapter，
不执行包内代码。当前没有 OS 文件系统/网络 sandbox，不能运行不可信第三方代码。
Channel 首片复用同一 Universal Loop、Current Store、`dispatch_attempts` 与 Gateway，
已经闭合 Cursor、去重、原子 Admission、UNKNOWN、严格回复目标、优雅关停和完整备份；
它不是公网 Provider。包含 Composite 修改的 S2 历史静止树已重新通过全仓、跨平台与 Race
门禁，不沿用上一棵源码树的测试数量或 seal。
Composite 首片以一个 Parent coordinator 和 2..8 个 Child specialist 表达同 Workspace、
depth-1 family；它复用同一个 Assembly Compiler、Universal Loop 和 Current Store，支持
有界并行 Child 推进、固定 `ALL_REQUIRED` merge、统一取消、family usage 派生和完整
backup/restore。公平 Scheduler 只负责同一 Store/本地进程内的可选公平 claim，不读取任务
内容；关闭时不访问其状态。W5-F1 已在三个持续 runnable Workspace、900 次 claim 和中途
Store reopen 下闭合有界无饥饿证据，但不把本地结果写成生产公平 SLA。Reviewer 是 Control 显式选择的普通 Agent/Profile。legacy S3-B
只在全部 Specialist 成功后执行一次 `RESULTS_GATE`；Core 把合法输出规范化为 canonical
Verdict 后写入既有 MODEL_RESULT，只有 `APPROVE` 才允许 Root merge。W5-X1 在同一 Decision
family 中预冻结最多一次 repair，并允许双边授权后的 `TASK_SUMMARY` 请求与
`SPECIALIST_RESULT` 返回；W5-F1 让 approve、单槽 repair 与 Transfer 通过该唯一 Scheduler，
并保持 dormant/skipped repair Run 零 claim。自动选择 Reviewer、无限返工、动态图、隐式启用
Learning 与任意 Workspace 间内容交换仍未包含。
必需 Model Adapter 继续 eager 构造；可选 Adapter 只在既有授权链选中的精确 Binding 首次
使用时物化。同一 `ArtifactDigest + AdapterIdentity` 合并一次无外部效果构造，不同精确键
并行；派生 cache 不持久化，也不缓存 Trust、Catalog、Authority 或 current Activation。
W1 Conversation 已在同一 Store/Loop/Context/History/Usage/Attempt 纵链实现
head/revision CAS、一轮一 Run、完整 USER+ASSISTANT predecessor pair、本地多轮、85% 摘要、
100% 最小完整前缀 Drop、CLI 续聊和 backup/restore 后继续。loopback HTTP 已支持严格 scope
的 `POST /v1/conversations`：首次创建返回 201、精确重试返回同一记录和 200；`POST /v1/chat`
可按 exact revision/head 续聊，产品 E2E 覆盖服务重启。另一个正常退出长链由两个独立 OS
进程分别完成 25+25 轮并闭合 50 Run/Attempt/Usage，Action/Memory/Channel 零访问。
Conversation 与 Composite 明确互斥，Conversation+Action 与 Conversation+Channel 尚未声明
成熟。正常 DeepSeek Provider 已转为显式启用、固定官方 endpoint 的产品 Adapter，且其
`estimated_cost` 会按冻结 PriceSnapshot 与完整 token 在 Usage 成功终态事务中持久化；未知
价格/token、未报告费用和未对账费用仍保持 UNKNOWN，reasoning 不重复收费。真实官方 DeepSeek
50 轮验收已完成：50/50 个首次 Model Attempt 成功，turn 25 backup→verify→restore、turn 25/50
移除运行时 Secret 后的 exact retry 均通过，token 加权缓存命中率为 `90.3702992171%`，重入未
增加模型调用。该 W1 完成结论只覆盖普通单 Agent Pure Chat，不表示上述组合边界、生产多轮 SLA
或公开 Beta 已完成。

W3 已完成本地 RAG/Memory 实用化开发纵链。K1 提供确定性、无 I/O 的 Knowledge 路由；K2A
使用独立 Authority-only `NOT_SELECTED` shortcut，K2B 在任何 Provider 调用前完成全部 Binding
的配置、协议、Authority、scope 与 limits 预检；K3 只复用同一 Conversation 中最新的
exact-question 成功候选，候选失效立即 fresh，不向更老候选穿透，实际 reuse 时 Knowledge
Provider 调用为零。M1 将 Memory 接入统一 Module Apply/Dry-run/Disable 与既有受治理更新路径；
M2 只复用直接成功前驱中可验证的连续最旧 History 前缀摘要，禁止 predecessor penetration 和
summary-of-summary，并对新 Conversation compiler-owned model-1 Attempt 执行 Store 字节级
复编译。该 W3 结论不包含远程向量数据库、后台 Learning worker 或生产部署；W4 的显式
Learning Cycle 是独立、可选的后续纵链，不会被 W3 Memory 或 Pure Chat 隐式启用。

项目所有者已确认 FreeAgent 从未正式部署，因此本轮生产切换为
`NOT_APPLICABLE_NEVER_DEPLOYED`，并批准进入 S2；这不代表首次生产部署或公开发布
已经批准。当前源码树使用 43 表、25 explicit indexes、64 triggers 的 Current Store。W6-3 曾新增 8 张只读 Overview
observation closure 表；W6-5 新增 `module_artifacts` 与 append-only `module_artifact_admissions`；U2 在 R2 的 24 表上增加 Publisher Key、
Source、Snapshot、全局 ModuleRef→ArtifactDigest 与 Snapshot Entry 五张 typed observation fact 表，
U3 再增加全局不可变 Candidate、Tenant-scoped Review 与绑定 exact Review 的终态 Decision 三张表。
W6-2 receipt Schema 再只增加 `control_operation_receipts`。当前 fingerprint 为
`47981b9bf147ec81c6e98382f281db0d5395187a1f090f7ce183c116fba82a0d`，migration 为
150,301 bytes / SHA-256 `6def433a59fa8f4876894572f1610cae499abc5b389ab5930ea697055419ca86`。
W6-3/W6-4 的历史 41 表 identity 为
`87d63a2e468ee27ee2c0e1918a92e580532825bde85dbb872b9c795bf577f2c1`、143,588 bytes /
`5bd9743e988b82750f785c015f07c11fbcf6025da70148405aec11106b4efb22`。
W6-2 receipt Schema 的历史 33 表 identity 为
`51be9081a815e6379380b93e745db03d33ca2f358962f85ace63bce2534dcc10`、67,998 bytes /
`8feea38beb505ab4f371afe32eecd877744062bd149d0d24982839c5450fc952`。
W2-U3 至 W6-2 confirmation 的历史 32 表 identity 为
`37258c1939308be83f21a109e59a19e32946734d1c53b1b262f876dc36e47dbd`、57,652 bytes /
`e4f1047eb527b65ab050d444062f3d286cc2ad87d56e60b1cd7f4e96df87652d`。
W2-U2 的历史 29 表 identity 为
`6d2bded477e7b1d2c755bf47f5b41f63f1b2f7b568fd72496fc43fbceac3a5f1`、49,972 bytes /
`d2bcc27bff2e17a058165f7c544b0c97cd1c99efca3264ab401631477611263e`。
W2-R2 的旧 24 表 identity `7d2e0850a0253a630d5e2264c720b10fbac3ad0fdb6f9e53fcd722c2dd2615a8`、
44,257 bytes / `2520299390885b4c23df85d2ff217629f6a458c368735e5bdc0df2eca37b8f2f`，W2-R1 的旧 24 表 identity `dcad8f8837ecc5832f444acbc2680a2c2debff5161319710f60e7f649e4aede5`、
44,237 bytes / `fd7270130b5b13e5dfe9e3bb0a3bc934d46eb0c3b4a0647b1a3fddd81773e665` 只保留为历史快照；
W5-F1/W5-X1 的旧 24 表 identity
`98d658e68907f77516ce0366a7b08bfa3851600326850590ed79584f581fa588`、44,215 bytes /
`0a6701405e471e1ee7f41ea89ed62b50e5a4723e71b320e50f073cdd3e6b727a` 只保留为历史快照；
W5-X1 未新增表，只在现有 Content Store 增加两个 Transfer ContentKind。W4-L4 的原 identity
`9941c957b0e6a1a1e1770b38cd06605025d12df30067d1271fe8f7b30f2f0518`、44,138 bytes、
`4cf260d368fb7b69e0a86dad99ff593bea216e9d0da341f607d000999e076be4`，以及 W4-L3 22 表和
更早 19/20/21 表旧 hash 仅证明对应历史切片，
S3-D 全仓 Release、六平台构建与 Public Stage 尚未执行。

## 1. 产品定义

FreeAgent 是一个高度模块化、可自行托管的 Agent 运行核心。它面向希望同时获得“自由装配”和“可靠运行”的开发者与团队：用户可以把它配置成普通聊天机器人、单领域专家、跨领域复合 Agent，或由 Composite、Specialist、Reviewer 组成的协作团队，而无需更换底层运行系统。

FreeAgent 不把角色、人格、领域知识、记忆或具体 Tool 固定进 Agent 内核。Agent 保存轻量身份、能力倾向、权重和策略请求；实际 Role/Persona、Knowledge/RAG、Memory、Skill、MCP、模型与团队策略通过版本化模块装配。可信内核保留 Core-mediated 协议路径不能绕过的身份、权限、状态、预算、外部效果、审计、恢复和冻结装配职责；当前没有 OS sandbox 的 Operator-trusted `LOCAL_PROCESS` 属于宿主级完全信任边界，不得被描述为可隔离的不可信普通模块。

## 2. 要解决的问题

现有 Agent 框架通常偏向以下某一方向：

- 以角色和任务为中心，适合快速构建多 Agent 团队，但运行权限和恢复语义较弱；
- 以图或工作流为中心，持久化清晰，但 Agent、Workspace 和模块的自由组合需要较多应用代码；
- 以持久人格或记忆为中心，个体连续性强，但上下文和 Token 成本容易增长；
- 以低代码应用为中心，RAG 和工作流易用，但不一定适合作为可嵌入的可信运行核心；
- 以编码沙箱为中心，工具执行强，但领域 Agent、共享知识和跨 Workspace 治理不是主要抽象。

FreeAgent 需要同时处理：

1. 多 Workspace 与多 Agent 并发协作；
2. 复合 Agent 的结构判断和 Specialist 的专业填充；
3. 轻量 Agent 与共享授权 RAG；
4. 可选记忆、自学习、知识更新与审核治理；
5. 可按模型实际能力调整的上下文工程；
6. Token、推理 Token、缓存与费用核算；
7. Tool、MCP、Skill 和外部模块的开放装配；
8. 崩溃恢复、外部效果一次性语义和不可确定结果对账。

## 3. 目标用户

- 需要自行托管 Agent 服务的个人开发者；
- 需要多个专业 Agent 协作的软件与研究团队；
- 需要共享知识库、权限隔离和成本审计的组织；
- 希望把现有 Skill、MCP Server、模型或业务模块接入统一运行核心的扩展开发者；
- 需要研究 Agent 自学习、模型个性化上下文和长期运行行为的实验者。

## 4. 产品原则

### 4.1 最小可运行

默认 `pure_chat` 在不绑定 Role、Persona、Knowledge/RAG、Memory、Skill、MCP、Tool、Channel、可选 Host 或扩展 SecretRef 时必须能完成普通多轮聊天。任何可选模块都不能成为内核的隐式启动依赖。

W1 已让零可选模块 Pure Chat 通过本地多轮、精确重入、阈值、HTTP 服务重启、两个正常退出
OS 进程 25+25 轮、备份恢复续聊和真实 DeepSeek 50/50 首次 Attempt 验收。该结论是普通单
Agent Pure Chat 的开发纵链完成，不是产品级生产 SLA、首次部署或公开 Beta 验收。

### 4.2 Agent 与知识分离

Agent 不复制完整领域知识库。共享 Knowledge Store 以 Tenant、Workspace、集合和数据作用域
授权；当前 K1 根据冻结标签、match terms 和本地有界统计确定性选择 `FRESH_RAG` 或
`NOT_SELECTED`，K3 再对 `FRESH_RAG` 叠加严格 exact-question reuse。统计只提供复用门槛，
不能成为知识事实或授权依据。

### 4.3 请求不能扩大权限

Agent、Task、Skill 和模块只能在上层允许范围内请求或收窄能力，不能扩大 Tenant、Workspace 或 Profile 的权限、预算、数据作用域和外部效果上限。任意层的 deny 优先于 allow。

### 4.4 冻结后不漂移

每个 Run 通过 MemberExecutionSnapshot 与 RunManifest 冻结 Agent、Workspace、Profile、
精确 PortPlan、配置摘要和权限上限。后续安装、升级、发现变化或默认值激活只影响
新 Run。

### 4.5 外部效果先记账

模型、Tool 和 Channel 调用都先建立 `DispatchAttempt` 或等价持久记录。调用结果无法确定时进入 `UNKNOWN`；系统允许查询、补证和人工结案，但禁止再次执行同一语义请求来“试试看”。

### 4.6 统计不替代判断

类别计数、高频词、缓存观察和历史成功率可以减少重复检索与成本，但不能替代需要新鲜知识、权限确认或高风险事实核验的查询。

## 5. 核心概念

| 概念 | 责任 |
|---|---|
| Tenant | 最高配置、身份和资源隔离边界 |
| Workspace | 成员、ACL、路由、知识范围、预算、模块允许/拒绝和公平调度边界 |
| AgentVersion | 不可变个体版本；保存能力标签、领域权重、信任/成本倾向和装配请求 |
| Profile | 可复用的版本化装配意图与策略输入 |
| Task | 针对一次任务的受限临时要求及其来源证据 |
| Team/Member | Composite、Specialist、Reviewer 等运行角色及每成员独立装配 |
| RunManifest | 一次 Run 的权威冻结证据 |
| RuntimeCatalog | 已发现、审核、安装、启用和可租用的 Module、Skill 与 MCP Server 版本 |
| DispatchAttempt | 模型、Tool 或 Channel 外部调用的真实账本 |
| ContextBlock / Receipt | 实际进入模型的上下文、摘要、移除范围和读取证据；不是第二装配事实源 |

`RuntimeCatalog` 是长期产品中的逻辑控制平面职责，不要求对应一个同名 Go 包或第二套
运行目录。S1 的安装、激活、绑定事实由唯一 Current Store 与统一 Module/Port 契约承载；
S2 只在这条边界上扩展发现、审核和租用能力。

## 6. 功能架构

```text
Control Plane + RuntimeCatalog
            |
            v
    Assembly Compiler
            |
            v
MemberExecutionSnapshot + RunManifest
            |
            v
          Universal Loop
      /       |       |       \
Context   Model   Action     Channel
  Port     Port   Describe/  send Port
                  Prepare       |
      \       |       |       /
                 v
Current Store / Attempt / Usage / History / Cursor
                 |
                 v
      Gateway → private executor / adapter

Workspace Channel ingress → Cursor/dedupe/Admission → Universal Loop

S2 modules: RAG / Memory / Action / MCP / Channel / Composite / Learning
             all reuse the same Port, Loop and Store boundaries
```

当前长期编码边界以 [`CORE_RUNTIME_V1`](specs/CORE_RUNTIME_V1.md) 与
[`CURRENT_STORE_V1`](specs/CURRENT_STORE_V1.md) 两份规格为准；一次性切换状态与
门禁以 [`CUTOVER_ACCEPTANCE`](CUTOVER_ACCEPTANCE.md) 为准。Core
与 S0 验收基线已经冻结，Current Store 与 S1 Pure Chat 纵链已作为开发基线验收；
项目从未正式部署，因此本轮没有生产切换，这也不构成生产部署完成证明。
Channel 是默认关闭的可选 Workspace 装配；它没有引入第二 Runtime、第二 Store、Outbox
或独立 Loop。
Parent/Child + Composite 第一切片也已接入同一 Core；它不是完整 Multi-Agent 平台，
当前仅覆盖同 Workspace 的 depth-1 Parent + 2..8 Specialist。
Conversation 复用同一 Current Store、Universal Loop 与 Context Compiler；当前开发切片只
支持普通单模型 Pure Chat，CLI 可创建/读取/继续；loopback HTTP 可创建固定 scope
Conversation，并按 exact revision/head 继续。
Conversation 与 Composite 明确互斥，Action/Channel 组合尚未进入该切片的成熟度声明。
旧 P0/Stage 规格只作历史或 S2 语义来源，
索引见[旧架构规格](architecture/specs/README.md)。

### 6.1 Workspace

Workspace 必须支持：

- 成员、角色、ACL epoch 和 Channel route；
- 同一 Tenant 内的多个 Workspace 并发运行；
- Workspace 之间的公平调度和无重复 claim；
- root/target Workspace 各自显式授予、同 Tenant 的定向 transfer grant；
- grant 冻结 peer、方向、允许的 payload kind、revision 和 limits，任一侧缺失即失败关闭；
- Module、Skill、MCP、Agent、Tool 和 Knowledge allow/deny；
- 权限、数据作用域、预算与外部效果 ceiling；
- 独立版本和撤权后失败关闭；
- SecretRef 而非明文凭据。

同一个 AgentVersion 可以进入多个 Workspace，但每次 Run 必须冻结独立的 Workspace 授权和
装配结果，不能通过另一个 Workspace 获得权限。W5-X1 中每个 Run 仍只绑定一个 Workspace；
跨 Workspace Specialist 只有在双方 grant 都通过 Admission 后才可启动。撤权只影响新 Run，
已经冻结并准入的 Run 不会静默漂移。v1 仅允许 `TASK_SUMMARY` 请求与 `SPECIALIST_RESULT` 返回，
不传完整 History、Memory、知识库正文、SecretRef 或任意 payload。

### 6.2 Agent 与团队

AgentVersion 支持：

- 任意命名空间化类型标签，而非封闭枚举；
- 多个领域的 capability、authority 和 weighted aliases；
- 信任度与成本权重；
- Module、Skill、MCP 和 Tool 请求；
- Knowledge collection ceiling；
- 可选 Role/Persona attachment。

团队策略应同时保留两类 Agent：

- Composite：偏结构、权重、依赖、冲突、思考和整合；
- Specialist：在前端、后端、网络、法律、金融等具体方向提供高密度专业内容；
- Reviewer：与内容生产者保持独立，对证据、权限、冲突和最终输出做审核。

产品目标允许团队并行执行、交换结构化提案、提出冲突并进行有界修复。legacy S3-B 只实现
一次 Reviewer 审核门；当前 W5-X1 已实现显式 Decision 下最多一次预冻结 repair。启用审核时，
未经 Reviewer APPROVE 的草稿不能进入 Root merge；当前仍没有自动 Reviewer、无限返工或动态图。

当前已验收的是其中的第一层 Composite：一个 Parent coordinator 在同一 Tenant/Workspace
内驱动 2..8 个 depth-1 Specialist，Child 可使用不同 Agent/Profile，并按冻结权重分配
Parent 输入预算中 50% 的结果池。当前只允许 Model、声明式 Context 与已验收本地只读
RAG；结果策略固定 `ALL_REQUIRED`。该 legacy S2 Composite 裁决本身不包含 Reviewer、修复、
公平 Scheduler、Learning 或跨 Workspace 协作；默认关闭的公平 Scheduler 和单次 Reviewer
审核门后来分别作为独立 S3-A/S3-B 开发切片验收。

W5-X1 在同一 Runtime/Store/Loop 上扩展该协作模型：显式 Decision 为 N 个 Specialist 预冻结
`2N+3` 个物理 Run，顺序由初始 Specialist、Reviewer、repair Specialist、repair Reviewer 和
Root 构成；最多一次 repair，只激活受影响槽位，其余 repair Run 明确跳过并保持 `Attempt=nil`。
这不是 `2N+3` 次必然模型调用。跨 Workspace Specialist 仍是普通单 Workspace Run，只通过
双边 grant 交换两种受限 payload。`TASK_SUMMARY` 是 Store-loaded `TASK_INPUT` 的确定性有界
extract，不是语义摘要，也不宣称检测任意自由文本 Secret。W4 Learning Cycle 继续保持独立
可选，不会由协作链隐式启用。

### 6.3 Knowledge 与 RAG

核心知识库由 Agent 共享，但每次检索必须同时满足：

- Tenant 和 Workspace 授权；
- AgentVersion knowledge ceiling；
- Task 数据作用域；
- collection 新鲜度与当前 revision；
- 检索缓存作用域和 epoch。

Agent 可使用脚本维护简单类别计数和高频词计数。高频且已验证的稳定知识可使用轻量路径；变化快、高风险或低置信内容必须重新检索确认。

当前 W3 本地纵链已经实现：K1 无 I/O 纯路由；K2A 的独立 `NOT_SELECTED` shortcut；K2B 的
全 Binding Provider 前预检；K3 的同 Conversation 最新 exact-question reuse。fresh retrieval、
reuse 与 shortcut 分别持久化独立证据；最新 exact 候选只要 TTL、ACL、activation、Source
revision、Compilation 或 Memory counter proof 任一不满足就立即 fresh，不向更老候选穿透。
实际 reuse 不调用 Knowledge Provider，UNKNOWN 和既有 Attempt 的 exact retry 仍禁止语义重放。
首版只使用本地确定性 Knowledge Source；远程向量数据库未实现。

W4-L0 已冻结无权限的
Proposal/Review canonical 合同和来源/内容指纹；W4-L1A/L1B 已在唯一 Current Store 与同一表
接入 Knowledge Proposal 和静态 Skill Draft、Tenant+kind 的 source/content/target 三轴去重、
权威 Run/Member/Result lineage、精确重入与 backup semantic gate。已验收的 W4-L2 在同一表
加入 `review_run_id`、`reviewer_attempt_id` 与有界 state/revision 投影，并让独立 Reviewer 复用
普通 Run、Model Attempt、Usage、MODEL_RESULT 和 MODEL_UNKNOWN。W4-L3 已实现无权限不可变
Version 与惰性 Operator artifact 交接。W4-L4 已实现可选、默认关闭的 Learning Cycle；省略
周期时 Schedule 固定为 86,400 秒，但只有 exact revision 显式启用和显式 Tick 才会运行。
周期到达至多产生候选 Proposal 或 `NO_CHANGE`，不能直接修改知识库；自动 Review、
Materialize、Apply、安装、激活、绑定和扩权仍未实现。

### 6.4 Memory 与自学习

Memory 是可选模块，不是 Agent 身份的必填字段。当前 Memory 继续遵守以下数据边界：

- Tenant、Workspace、Agent、Subject 作用域；
- 记忆压缩、来源和有效期；
- 撤权、隐私删除和备份语义。

后台“回复成功后入队、lease/commit、常驻补偿扫描”仍未实现，不应解释为 W3 Memory 已具备
学习队列或 worker。W4-L4 的 `learning-cycle-reconcile` 只是显式、Store-only 地收口既有
Learning Task，不读取或写入 Memory，也不会创建 Run/Attempt 或调用模型和外部系统。

当前 W3-M1 已把 Memory 接入统一 `module-dry-run/module-apply/module-disable`：不存在 Head
时只创建显式空 Genesis，已有 Head 必须逐字节保留，Disable 只影响未来 Binding。运行时
Memory 更新继续复用既有成功终态的受治理 Apply 路径，不建立第二条写入链。Memory 按
Tenant + 固定 AgentID 保存 append-only revision，按 Workspace visibility 过滤条目，只记录
显式轻量事实/偏好、`TASK_SUMMARY` 和来自用户 `TASK_INPUT` 的有界分类/重复词计数；读取快照
与模型 Attempt 原子冻结，只有首次 `SUCCEEDED` 终态能基于最新 head 追加 revision，失败、
`MODEL_UNKNOWN` 和精确重入不更新。

W3-M2 中的 Conversation Summary 与上述 Agent Memory 明确分离：它只复用直接成功前驱
ContextCompilation 中覆盖原始 USER/ASSISTANT 完整 pair 的连续最旧前缀；`TASK_SUMMARY` 不能
冒充 Conversation Summary。Memory 仍不承载共享 RAG 领域正文，也没有后台学习队列、周期
worker 或自动 PR；W4-L4 的显式逻辑周期和 Store-only 补偿不改变该边界。

W4 的产品目标允许 Agent 提出 Knowledge 或 Skill 变更，但不能自我批准。变更采用类似 Pull
Request 的流程：来源和内容指纹去重；Skill V1 由独立高可信 Reviewer Agent 审核，Operator
负责后续发布、激活、回滚和撤销。被拒绝来源永久拦截，不提供解除或自动重新考虑 API；
需要再次引入能力时必须使用新的、内容已变化的来源与版本，并重新完成审核。W4-L0 已冻结
exact proposer lineage、非持久来源 evidence 派生、语义去重和严格 Verdict wire；这些合同不授予
审核、安装、激活或权限。W4-L1A/L1B 已在同一个 Proposal Store 中接入 Knowledge 与静态
Skill Draft：提交时从终态成功 Model Run 反查 exact Manifest/Member/Attempt/Result，按
Tenant+kind 对 source、content 与 target 三轴去重，exact retry 返回原记录，完整
backup/restore 会复验 canonical Proposal、Draft 和 lineage。

W4-L2 只增加一次受治理审核。`learning-review-request/v1` 作为现有 `TASK_INPUT`
Text 完整携带 Proposal 与 Draft；审核 Draft 上限为 64 KiB，禁止截断、摘要或分块冒充完整输入。
Reviewer 必须由 Operator/Control 显式选择，与 proposer 同 Tenant、同 exact Workspace，但使用
不同 Run、Member、Agent logical ID 和 Profile logical ID；其装配只允许一个 required
`model.generate/v1` Binding、无 Action/Channel，且显式 `max_tokens <= 1024`。Review Admission
与 `SUBMITTED/0 → REVIEW_PENDING/1` 原子提交；Store 只从绑定 Run 的唯一普通 Attempt 派生
`APPROVED/REJECTED/REVIEW_FAILED/REVIEW_UNKNOWN@2`。UNKNOWN 只能由同一 Attempt 的可靠对账
证据收口到 revision 3，禁止新 Run、新 Attempt、换 Reviewer、换 Provider 或语义重放。

Skill Draft 继续使用既有 `static-context/v1`，审核本身不会创建 STATIC_CONTEXT、Version、
Installation、Activation、Binding、Catalog 变更或发布事实。当前有显式构造的可选
`LearningReviewService` 和 opt-in `Invoke-W4L2LiveLearningReview.ps1` 真实 DeepSeek 验证入口；
后者要求 Reviewer `SUCCEEDED`、Proposal `APPROVED|REJECTED`、Usage token 完整及 exact retry
零重放。2026-08-09 的真实 DeepSeek 验收为 PASS（4.08s）：Proposal `APPROVED` revision 2，
Reviewer Attempt `SUCCEEDED`；Usage 为 `PROVIDER_REPORTED`，input 1151、cached 0、uncached 1151、
output 227、reasoning UNKNOWN，估算费用 0.001605 CNY，exact retry 未重放。它们不构成公开
Review CLI/HTTP、自动 Reviewer 或周期 Worker。

W4-L3 在同一 Proposal 行加入 all-or-none 的 `learning-materialized-version/v1` canonical/ID、
ArtifactDigest/size、APPROVE Verdict digest 与 `materialized_at` 投影，不新增 Version 表、发布表、
Queue 或效果账本。只有 Store 重新验证为 `APPROVED/2` 或同一 UNKNOWN Attempt 对账后的
`APPROVED/3` 才能物化；其余状态在任何 Version 或文件写入前失败。Version 精确绑定 Proposal、
review revision、Reviewer Attempt、Tenant、kind、source/content/draft fingerprint 和全局
ModuleRef，但不包含 Profile、Instance、Binding、Config、Trust、Authority、Secret 或 Catalog
revision。`materialized_at` 不得早于 Proposal 审核投影时间，精确重入保留原 Version 与原时间。

Knowledge/Skill 候选分别确定性重建为 `module.yaml + content/source.json` 和
`module.yaml + content/context.json`。全局 ModuleRef 在 Tenant 与 kind 之间也唯一；Version 与
既有 Installation 无论谁先创建，都只有 Manifest 和 ArtifactDigest 完全相同才可共存。产品命令
`learning-materialize` 只把 exact 包导出到与 Store/活动 artifact root 分离的 Operator 交接目录，
不生成 Apply plan；只读 exact retry、并发单发布、安全路径/文件校验、完整备份恢复和恢复后
逐字节重建已闭合。Operator 仍须现场选择 Profile、Instance、exact Port、binding index、
`expected_runtime_request`、Config、Authority、FailurePolicy 与 expected pointer revision，并
另行运行现有 `module-dry-run/module-apply`。因此 W4-L3 不是
发布或授权。

当前专属纵链又证明：经真实 proposer lineage、独立 Reviewer `APPROVED/2` 形成的静态 Skill，
materialize 后仍无 Installation/Activation/Binding；stale expected pointer 失败且零部分发布；只有
Operator 使用正确 CAS 显式 Apply 后，新 Run 才冻结 exact Version/Artifact 并实际消费审批正文，
旧 Run 保持原 Member bytes。Binding 仍为 REQUIRED、exact deny-all Authority，ExecutionClass/
Adapter identity 由本地策略授予。该纵链不构成自动 Apply 授权。

W4-L4 只增加一个可选的显式 Learning Cycle。Schedule 的 canonical 策略创建后不可变，初始
disabled；未指定 `interval_seconds` 时冻结为 86,400 秒，enable/disable 必须使用 exact revision
CAS。Tick 必须显式指定 exact Tenant/Schedule 与 UTC observed-at；Store open、Restore、Pure
Chat 和启动恢复都不会自动 Tick。到期前保持零写；到期时创建确定性逻辑窗口；错过多个周期只
合并为最新到期窗口，不突发补跑；同一 Schedule 同时至多一个 `PENDING/RUN_ADMITTED` Task。

每个实际窗口的 proposer 复用普通 standalone、single-member、model-only Run/Attempt/Usage/
`MODEL_UNKNOWN`。Learning Result 只允许 `PROPOSE` 或 `NO_CHANGE`；`PROPOSE` 只能进入同 kind 的
Knowledge 或静态 Skill Proposal，不会自动 Review、Materialize、Install、Activate、Bind、
Apply、修改 Catalog 或扩权。source/content/target 三轴只要已有占用就不重复提交，其中包括
`REJECTED` 来源。UNKNOWN 只允许依据可靠 evidence 对账同一 Attempt，禁止新 Run/Attempt、
换 Provider 或语义重放。

显式 `learning-cycle-reconcile` 只收口既有 Task 的 Store 投影，不调用模型、Provider、Loop、
Gateway、MCP、Channel、网络或 Secret；`learning-cycle-report` 是确定性、只读、不持久化也不
自动发送的 canonical 半开窗投影。Backup/Restore 保存并语义复验 Schedule、Task、Run、Attempt、
Result、Proposal 和 UNKNOWN 崩溃窗口。没有后台 worker、第二定时器、Queue、第二 Runtime、
Store、Loop 或 Gateway。2026-08-09 的 opt-in 真实 DeepSeek 验证取得
`PROPOSAL_SUBMITTED`：恰好一个 Run/Attempt，Usage input 614、output 90，估算费用
0.000794 CNY，同一窗口重试零重放。该证据仍不是生产部署或公开 Beta。

### 6.5 Context

上下文编译按预测占用率处理：

- `<85%`：不压缩；
- `>=85% 且 <100%`：只运行一次确定性压缩；有合格旧历史时持久化至多一个可恢复
  摘要，没有合格旧历史时记录稳定停止原因，不转入 Drop；
- 初始预测达到或超过 `100%`：不先摘要，直接按顺序移除达到 85% 恢复水位所需的
  最小完整旧前缀，满足后立即停止。

移除以完整 Task/turn 为单位，不切开当前 Tool 协议或一个连续 Task。当前用户输入、最近保护窗口、冻结权限/装配证据和本次检索证据不可移除；如果保护下限仍无法放入模型窗口，则在模型调用前失败。

移除只影响当前模型请求，不删除持久历史、审计、记忆或工件。

W3-M2 已在唯一 Context Compiler 上闭合持久摘要复用：只考虑同一 Conversation 的直接成功
前驱，不向更老前驱穿透，也不允许 summary-of-summary。`<85%` 忽略候选；`>=85% 且 <100%`
仅当候选的 SourceTurnDigests/Text 与本轮所需连续最旧原始 pair 前缀完全一致时复用，否则执行
确定性 fresh；`>=100%` 忽略候选并最小 Drop 完整旧 pair。复用时当前请求的 token 估算重新
计算，CoreLoop 与 Store 对新 Conversation compiler-owned model-1 Attempt 使用同一选择器和
Compiler，并逐字节验证
MODEL_REQUEST/ContextCompilation。该开发验收没有新增摘要表、ContentKind、Port、Memory kind
或第二 Runtime/Store，也不等于生产多轮 SLA。

### 6.6 模型评测与路由

模型 Profile 记录可复现评测，而不是品牌刻板印象。建议维度：

- reasoning、factuality、instruction following；
- tool use、search、coding；
- long context、uncertainty calibration；
- over-refusal、verbosity 等行为倾向；
- 样本量、评测版本、模型精确 ID 和日期。

当前已实现的最小 `model-profile/v1` 锁定精确 provider、model build、Binding config、
adapter artifact/identity、评测 suite/version/result digest 和有序能力/可靠性倾向。
Profile 是可选 CONFIG；绑定后只能将有效上下文窗口收紧为
`min(ContextPolicy, ModelProfile)`。它不能自动选择或切换模型，不能增加预算、权限，
也不能让 Universal Loop 感知模型品牌。无画像 Pure Chat 保持原请求与零额外画像读取。

W2-E4 已验收一个更窄的显式替换入口：Operator 可在同一内建 DeepSeek 构件、Adapter 与
Provider Instance 内，用 Apply/Dry-run/CAS 在 `deepseek-v4-flash` 和 `deepseek-v4-pro` 之间
替换唯一 Model Binding，并可同时设置或清除 exact ModelProfile。这仍不是 Profile 自动选模。
后续有跨 Provider 或多个并行 Model 消费者时，不同工作负载才可以使用显式权重提出更广义的
模型装配，并派生尽量短、稳定、标准化的上下文策略。强判断模型与低幻觉模型可以通过
Judge/Reply/Reviewer 组合协作，但每个新 Run 仍必须冻结精确 Binding，所有调用继续受同一权限
和预算控制。

### 6.7 Usage、Cost 与 Cache

系统持续记录：

- provider 原始 input/output/cached/reasoning token；
- 本地预算预留与最终提交；
- 模型、SKU、价格表版本和费用；
- cache family、命中/未命中和策略 generation；
- 请求级、任务级、Workspace 级与模型级汇总。

当前 DeepSeek 纵链只在 Provider/model/billing version 与 Attempt 冻结 PriceSnapshot 精确闭合、
且计价所需 token 完整时派生 `estimated_cost`，并与模型 Usage 成功终态在同一事务提交。价格
或所需 token 未知时保持 `NULL/UNKNOWN`；reasoning token 保留独立事实但不重复收费；
Provider 没有报告或尚未对账时，`provider_reported_cost` 与 `reconciled_cost` 继续为 UNKNOWN。
Conversation 不建立第二费用账本。

W2-E4 的 Model Apply 不能携带或创建价格：对应 PriceSnapshot 必须已经存在 Current Store，且
provider、model 与 billing version 必须和候选 Config 完全匹配，之后才允许 publication。新 Run
冻结新价格引用；旧 Run 与旧 Usage 继续指向原 PriceSnapshot。替换未增加第二 Usage/Cost 账本。

W5-X1 的 Decision Usage 同时区分物理 Run 与真实 Attempt。完整 family 固定投影 `2N+3` 个
physical Runs，但只有实际 dispatch 才创建 Attempt；未激活或已跳过的 repair Run 保持
`Attempt=nil`，不能伪造为零 token/零费用。family aggregate 只汇总真实 Attempt，实际存在的
repair Attempt 必须计入 dispatch cap、token 与费用。

2026-08-09 的 X1D 真实 DeepSeek canary 为 4/4 HTTP 2xx，调用角色为 2 个 Specialist、Reviewer
和 Root；共 7 physical Runs、4 Attempts、3 个无 Attempt 的 repair Run，exact retry 新增
HTTP/Attempt 均为 0。REQUEST 与 RESULT transfer 均已验证。Usage 为 input `3,443`、cached input
`768`、uncached input `2,675`、output `1,353`，token 加权缓存命中率 `22.306128%`，reasoning
UNKNOWN，estimated cost `0.00539636 CNY`，Provider reported 与 reconciled cost 均为 UNKNOWN。
这些数值只属于本次验收样本。

独立的 F1 Test-0808 canary 同样取得 4/4 HTTP 2xx，exact retry 新增 HTTP/Attempt 均为 0。
Usage 为 input `3,149`、cached input `768`、uncached input `2,381`、output `1,492`；请求级
cache hit 为 `2/4`，token 加权缓存命中率为 `24.388695%`，estimated cost 为
`0.00538036 CNY`。reasoning token、Provider reported cost 与 reconciled cost 均为 UNKNOWN。
该组数据独立于 X1D，只用于 F1 窄纵链验收，不构成缓存、费用或生产 SLA 承诺。

W1 真实 50 轮的权威 Usage 合计为 input `41,642`、cached input `37,632`、uncached input
`4,010`、output `710`；token 加权缓存命中率为 `90.3702992171%`，请求级命中为
`46/50 = 92%`。50/50 reasoning token 均为 UNKNOWN，不能记作 0；冻结 PriceSnapshot 派生的
estimated cost 为 `0.00618264 CNY`，Provider reported 与 reconciled cost 仍为 50/50 UNKNOWN。
这些是验收样本事实，不构成未来费用、缓存命中率或生产 SLA 承诺。

缓存优化只对已经准入的真实请求进行有界串行，不主动生成预热流量。自动决策默认关闭；人工控制平面可以比较开/关两种状态下的预计成本和置信区间。

### 6.8 Tool、Skill、可信模块与 MCP

- 模块作者可先使用只读 `module-verify` 验证 canonical Manifest、包路径、文件型
  entrypoint、本次包的单一 ArtifactDigest 与 covered size。没有 supply flag 时必须保持 legacy
  路径及既有输出/错误逐字节兼容；任一 supply flag 都显式切换到只接受
  `LOCAL_DIRECTORY + DENY` 的 governed candidate observation，部分参数不得回落 legacy。
- governed observation 的 Source Policy、Publisher Key、detached Ed25519 Signature canonical bytes
  及各自 exact content ID 都由 Operator 从 artifact 外部提供。是否要求签名、允许的点分段 Module ID
  前缀和包大小上限只由 Policy 决定；Manifest、Key 或 Signature 不能授予 Trust、ExecutionClass、
  Authority、Effect、Secret、Binding 或运行资格。首次 package scan 和最终 digest/size scan 都必须
  使用 Policy 收紧后的 `max_package_bytes`，而不是先按较宽默认值扫描再拒绝。
- 撤销输入只是在单次调用开始时冻结的 immutable deny snapshot；它拒绝被选中的 exact Publisher
  Key，但不是 Store 中的 current revocation，也不改写任何历史事实。unsigned direct verifier 必须拒绝
  没有 Policy 锚点的 Key、Signature 或撤销输入。U1 不接受 `HTTPS_INDEX`，不创建网络 transport；
  Windows 显式拒绝 UNC/device namespace、ADS 和 symlink/reparse 路径，但不承诺识别盘符映射的网络盘。
- U0 unsigned Discovery Snapshot 的 entry `signature_id` 是可空的 authority-free observation 字段，
  允许携带语法有效的非空 ID，但不验证签名；U1 direct preflight 没有 Snapshot parent 锚点，因此不能
  用该兼容字段绕过 unsigned Policy 的 side-input 拒绝。
- 两种 `module-verify` 成功都只输出既有 `freeagent.module-package-verification/v1`。结果不产生
  reservation、grant、staging、Store fact 或 Apply authority，不执行 Install/Activate/Bind，不访问
  Secret、模型、Host 或网络。U2 已在唯一 Store 中闭合 SourcePolicy→Index→Snapshot observation
  parent 与 current revocation；U3 已生成并审核无权限 Candidate/Review/Decision，U4 才能在真正
  staging 前重新检查 current basis 并消费 `APPROVE`。统一作者合同见
  `MODULE_DEVELOPMENT_V1`。现有手工 exact-artifact-grant `module-apply` 与九个 exact handler 不消费
  U1 report 作为授权，路径和语义保持不变。
- U2 的 `module-source-register`、`module-source-refresh` 与 `module-publisher-key-revoke` 均默认关闭并
  要求显式 `--enable-module-discovery`。Local Provider 只读固定 `root/index.json` 并拒绝
  UNC/device/ADS/symlink/reparse 与观察中漂移；HTTPS Provider 另需 exact URL、独立启用开关和 exact
  HTTPS origin allowlist，拒绝 proxy、redirect、retry、HTTP/2、keep-alive、cookie、凭据与任一
  special-use/non-public DNS 结果。
- Store 在 Source I/O 前后重验 exact Policy/Key revision、Source identity 与全局不可逆 Publisher Key
  revocation；跨 Source Snapshot、Installation 与 materialized Learning Version 共享同一全局
  `ModuleID + opaque exact Version → ArtifactDigest` 冲突门禁。U2 只持久化 Publisher Key、Source、
  Index/Snapshot 及其 entries；不读取 package path、不下载包、不验证 entry Signature，也不生成
  Candidate、Decision、Apply plan 或运行权限。Backup/Verify/Restore 零网络、零来源读取、零包获取。
- W2-U3 的 `module-upgrade-review` 和 `module-upgrade-decide` 都默认关闭，必须显式
  `--enable-module-upgrade-review`，并只在服务停止、唯一 Store 可独占的可信本地 Operator 边界运行。
  首片只允许同一 Module ID、不同 opaque exact Version 与不同 ArtifactDigest 的
  `EXACT_VERSION_CHANGE`；U0 的 `INSTALL` Candidate 仍只是纯合同，U3 不为“无 current”猜测 scope。
- Review 必须显式选择 Tenant、PROFILE 或 WORKSPACE_CHANNEL_ENDPOINT、exact current
  Instance/Activation/ModuleRef/ArtifactDigest、current pointer revision、Source/Snapshot entry、target
  Instance 与本地 unpacked artifact。Store 从 current facts 重建
  SourcePolicy→Index→Snapshot→entry 和 Binding→Catalog→Activation→Installation，不能信任调用方自带
  canonical/digest。PROFILE 不伪造 Workspace；同 current Instance 的全部 published Binding refs 都进入
  impact 检查。
- 对 target artifact 的双次 package verification 产生参与 ArtifactDigest 的 exact canonical Manifest；
  signed Source 另需 exact live Publisher Key 与 detached Signature。系统不读取 Index `PackagePath`、
  不从 Source/网络下载。target Manifest 写入既有 `content_records` 作为 content-addressed evidence；
  Review 只保留安全 Manifest summary/ref、完整 runtime request、diff、handler、全部 Binding impacts、
  PublishedBasis 和 digest-only required grants，不保存主机路径、URL、signature bytes、Secret 或
  Config/Authority 正文。
- `module-upgrade-review/v1` 只允许 `WOULD_APPLY | CONFLICT | UNSUPPORTED`；没有 `NO_CHANGE`，因为
  首片入口要求不同 Version 和 ArtifactDigest。Version 继续是 opaque string，Core 不判断 newer/downgrade；
  Operator 只能通过显式 `--operator-requested-rollback` 标记回滚意图。target port removal、Require 变化、
  requested permission 扩大、未知/冲突 handler、target Instance collision 与额外 grant 都必须确定性展示。
- Candidate 是全局不可变 supply observation；Review 属于 exact Tenant/scope；Decision 终态绑定 exact
  `review_id`。`APPROVE` 只接受 current basis 仍匹配的 `WOULD_APPLY` Review，仍不是 reservation、grant、
  Install 或 Apply authority。`REJECT` 必须显式设置 `--confirm-tenant-wide-reject`，并以
  `{tenant_id, review_key}` 抑制同 Tenant 所有 scope 的重复候选；另一 Tenant 不受影响。
- U3 对 Installation、Activation、Control、Catalog、Run、Attempt、Usage 与外部效果保持零变化；三张
  U3 fact 表和目标 Manifest evidence 进入同一 Backup/Verify/Restore semantic closure，Pure Chat 对其零访问。
  U3 收口时的 `W2_U3_UPGRADE_REVIEW_ACCEPTED_DEVELOPMENT_SLICE / W2_U4_OPERATOR_APPLY_NEXT` 是
  历史 marker。U4 Handler-gate 随后把 9 条 generic/Model policy 与 2 条 Document Insight reserved
  exact selector 提取为 Review 与 Apply 共同复用的唯一 `internal/modulehandler` 实现，没有复制第二张表。
- W2-U4 的 `module-upgrade-apply` 默认关闭，必须显式设置 `--enable-module-upgrade-apply`，并绑定 exact
  Tenant/Review/APPROVE Decision。首片只接受 `LOCAL_DIRECTORY + DENY` 的显式 `--source-root`，不读取
  Index `PackagePath`，不联网或下载；`--signature` 必须显式出现（unsigned 使用空值），全部
  artifact/endpoint/SecretRef grant flags 必须显式且为空。
- 唯一支持的 Review impact 是一个 PROFILE 的 `context.provide/v1` Binding；target 精确为
  `DECLARATIVE + static/v1 + TRUSTED_INSTRUCTION + deny-all Authority`。Model、Channel、Action、
  `SINGLE`、shared-current、fanout、多 BindingImpact 与任意 grant 均失败关闭。
- production 顺序固定为 historical U1 governed source verification → current Store revalidation →
  same exact plan → current U1 verification → pre-stage revalidation → 既有 Apply/CAS。Review/Apply 复用
  同一 evaluator；不得另建 handler 表，也不虚构独立 dry-run admission、reservation 或 approval authority。
- canonical replacement 只在原 PortPlan ordinal 把旧 Instance 原位替换为 target，其他 Binding bytes/order
  不变。旧 Run 保持冻结旧 Provider/static content，新 Run 才使用 target。相邻 current exact retry 返回原
  publication 且零 source/stage/Store mutation，但不是 durable receipt；后续 publication 后必须返回
  `POINTER_CONFLICT`。
- UNKNOWN 原样保留并禁止语义重放、换 Provider 或替代 Attempt。U4 的 Review/Decision/Manifest/final
  publication 进入完整 Backup/Verify/Restore；Pure Chat 对 Source/Review/artifact/stage 零访问；不新增表，
  Store 保持 32 表。U4 历史状态为
  `W2_U4_OPERATOR_APPLY_ACCEPTED_DEVELOPMENT_SLICE / W6_0_CONTROL_API_CONTRACT_NEXT`。
- `module-apply` 当前只作为开发期停机入口，允许显式 Operator 请求把已验证包以精确
  ArtifactDigest 安装，并为 tagged target `PROFILE | WORKSPACE_CHANNEL_ENDPOINT` 启用下列 Binding：
  声明式 Role/静态 Skill Context Binding、本地确定性只读 Knowledge Context Binding（包括 E5-A
  governed exact 版本）、本地有界 Memory Context Binding、受完全信任的本地 MCP Action Binding、
  受信 compiled `text.stats` Action Binding、first-party loopback Channel Binding、窄 DeepSeek Model
  Binding、固定 Document Insight 的 Context/Action Binding，或 exact
  `action.provider/v1 + REMOTE/freeagent-action-http/v1` / `action.provider/v1 +
  WASM/freeagent-action-wasm/v1` Action Binding。Model Port 明确拒绝 Disable；其他已支持 Port 可按
  各自合同禁用。
- Core-owned 通用 handler 表当前有 9 个唯一的 exact protocol tuple；handler key 是 exact Port、
  runtime mode/protocol 与 verified consumer schema。E5-B 当时没有增加新 tuple，而是在该表之前增加
  2 个 Document Insight reserved exact selector：
  `context.provide/v1 + TRUSTED_IN_PROCESS/go-in-process/v1 + knowledge-context-binding/v1` 与
  `action.provider/v1 + TRUSTED_IN_PROCESS/go-in-process/v1 + action-binding-config/v1`。两者还必须
  精确匹配 `freeagent.builtin.document-insight@1.0.0`、ArtifactDigest
  `838ff9ddd45186f0cdb26021d16902b2bfd7581c2dc0d7b72014cf4f48d0f7ea` 和 covered size `1097`
  bytes；错误 identity 不得回落到普通 Knowledge 或 `text.stats` handler。R1 随后增加第 8 个
  `action.provider/v1 + REMOTE/freeagent-action-http/v1 + action-binding-config/v1` tuple；R2 再增加
  第 9 个 exact `action.provider/v1 + WASM/freeagent-action-wasm/v1 + action-binding-config/v1`
  tuple。除 Channel 外的 Profile
  target 与 Channel 的 Workspace/Endpoint target 规则不变；所有 handler/selector 共用唯一 Current
  Store、Catalog CAS 和 exact Binding 规则。
- `ENABLED module.expected_runtime_request` 只表达 Operator 对 Manifest request 的预期；Core 才
  选择 handler 并授予 ExecutionClass/AdapterIdentity。Plan/Manifest 不能授予 Trust、Authority
  或 handler identity。Apply 期间不调用模型/Action、不执行声明式内容、不启动目标进程、不解析
  Secret、不访问网络；Knowledge/Memory/Document Insight 包只提供不可变数据，REMOTE 包只提供
  ArtifactDigest 覆盖的 descriptor。WASM 包提供 descriptor 与同 digest 覆盖的 binary，Apply/Dry-run
  只做有界 preflight 与 compile validation，不实例化或执行 guest。immutable Installation/Activation/Content
  只是 staging。相同请求可精确重试；无法证明结果时保持 UNKNOWN，禁止语义重放。该入口尚未升级
  为稳定公共控制平面。
- `module-list` 与 `module-inspect` 在服务停止、数据库 self-contained 时只通过
  immutable/query-only 的 `ReadOnlyObserver` 查看当前 Control/Catalog。`module-list` 返回 current
  Basis/Binding refs，并通过窄 Installation 投影闭合 stored Manifest；它不读取 artifact root。
  `module-inspect` 只接受 current exact Instance，并额外复验调用方给出的 artifact root 后返回
  非敏感 Manifest 摘要。两者不创建 Run、Host、Attempt 或外部效果，也不单独读取、materialize
  或输出 Config、Authority、StaticContext 正文。独立
  `module-disable` 只接受 exact canonical DISABLED plan，并复用同一 Apply/recovery/CAS；
  `model.generate/v1` 明确拒绝 Disable，其他禁用不删除历史或不可变事实。
- `module-history` 在同一停机只读边界内查询调用方给出的 exact Control revision + Catalog
  generation。它恢复该不可变 pair，并通过既有窄 Installation 投影读取、解析 stored Manifest，
  以闭合 Source、Activation、Port 与当时的有序 Profile Binding refs。该路径不读取 artifact
  root、不输出 Manifest 正文或摘要、不构造 historical PublishedBasis、不虚构历史 pointer
  revision、不枚举、不计算差异，也不把未发布 Activation 猜成已禁用；mismatched pair
  fail-closed。它不增加表、Store API、Runtime、Host 或外部效果。
- `module-dry-run` 接受同一 canonical plan 和 handler-specific 授权参数：MCP 只接受匹配摘要的
  `--allow-local-mcp-artifact`，compiled `text.stats`、loopback Channel、窄 DeepSeek Model 与固定
  Document Insight 的 Context/Action 两步只接受匹配摘要的
  `--allow-trusted-in-process-artifact`；Model 还必须提供精确匹配 Authority 中
  SecretRef identity 的临时 `--allow-model-secret-ref`。R1 REMOTE Action 必须同时提供
  `--allow-remote-action-artifact <sha256>`、`--allow-remote-action-endpoint <https-url>` 与
  `--allow-remote-action-secret-ref <secret-ref>` 三项 exact 瞬时 grant；任一缺失或不匹配都在
  staging/Store 写入前失败关闭。R2 WASM Action 必须提供 exact
  `--allow-wasm-action-artifact <sha256>`，且只接受 EffectClass `none`、FailurePolicy `REQUIRED` 与
  `parameters={}`。LOCAL_PROCESS、TRUSTED_IN_PROCESS、REMOTE、WASM 四类 Artifact grant 互斥，
  声明式 Context、普通 Knowledge 与
  Memory 禁止这些外部 Artifact grant；固定 Document Insight 是唯一额外窄例外，必须先命中
  reserved exact selector，不能泛化为通用 Trust 开关。其
  `knowledge.read` 只由既有不可变发布事实、Config 与 Authority 闭合，不接受 CLI 自授权。
  它只使用 query-only Observer 与系统
  TEMP 候选副本。它返回 observed Basis、标记为 `PROJECTED_NOT_RESERVED` 或
  `OBSERVED_CURRENT` 的 candidate Basis、变化枚举和是否存在 crash-left PENDING；不取得
  writer、不 recovery、不写 Store/artifact root、不启动 MCP/模型/网络/Secret。真实 Apply
  仍必须重新观察、验证和 CAS。对 Knowledge，它复验 Artifact、Config、Authority、source 与
  scope 闭包，并可构造固定内建词法 Adapter，但不得调用 provider。在线热查和自动模块管理仍
  属于后续工作。
- Apply/Dry-run 即使装配了 REMOTE Action，也只验证 descriptor、Config、Authority 与三项 grant，
  保持零 Secret lookup、零 Provider 调用和零网络。正常 `chat`/`serve` 还必须显式使用
  `--enable-remote-actions`，并通过可重复的
  `--remote-action-secret-env <secret-ref>=<ENV_VAR>` 提供晚绑定映射；默认关闭，缺失、重复或无效映射
  均失败关闭。映射只保存环境变量名称，Secret value 不进入命令参数、Manifest、plan 或 descriptor。
- Apply/Dry-run 即使装配了 WASM Action，也只验证 exact 三文件 artifact、descriptor/Binding mapping、
  ABI、binary section 与资源 ceiling，不实例化或执行 guest。正常 `chat`/`serve` 必须同时显式使用
  `--enable-wasm-actions` 与至少一个 exact `--allow-wasm-runtime-artifact <sha256>`；allowlist 不持久化，
  非 canonical、重复或非当前 Catalog digest 均失败关闭。默认关闭时不得读取、编译或实例化 artifact。
- Document Insight 的固定 Adapter 为 `freeagent.adapter.document-insight/v1`，Runtime request 为
  `TRUSTED_IN_PROCESS/go-in-process/v1`。Manifest 必须按顺序只提供 `action.provider/v1`、
  `context.provide/v1`，只 Require `model.generate/v1`，并只请求 `knowledge.read`；任一 identity、
  顺序、Port、runtime、Require 或 permission 漂移都失败关闭。模块 artifact 只携带不可变 JSON，
  RAG 与无外部副作用的 `text.stats` 实现编译进 Core，不执行第三方包内代码。
- Document Insight 不是一次双 Binding 原子批量 Apply。Operator 必须先对 Context Dry-run/Apply，
  再以新的 expected pointer revision 对 Action Dry-run/Apply；第二步复用完全相同的 Installation、
  Activation、Catalog Instance 和 Adapter identity。Action-first 以 `TARGET_CONFLICT` 零写拒绝。
  Disable 顺序相反：Context-first 在 Action 仍存在时以 `PUBLICATION_FAILED` 零写拒绝；先 Disable
  Action 后仍保留 RAG，再 Disable Context 才从 current Catalog 移除无引用 Instance。Document
  Insight 向两个不同 PortPlan 各贡献一个 Binding；Context PortPlan 同时保留既有 `context.basic`。
  本切片不形成任意 Provider 的 reducer、merge 或 fallback 语义。
- 当前各层成熟度必须分开：W2-D 本地显式装配 v1 为
  `W2_D_LOCAL_ASSEMBLY_ACCEPTED_DEVELOPMENT_SLICE`，只在冻结的本地 v1 范围 accepted；E5-A 为
  `W2_E5_A_REQUIRES_PERMISSION_GRANT_ACCEPTED_DEVELOPMENT_SLICE`，只验收 governed Knowledge 的
  exact Require 与窄 grant；E5-B 为
  `W2_E5_B_DOCUMENT_INSIGHT_DUAL_PORT_ACCEPTED_DEVELOPMENT_SLICE`，只验收上述固定 Document Insight
  双 Port 产品链；R1 为 `W2_R1_REMOTE_ACTION_HOST_ACCEPTED_DEVELOPMENT_SLICE`，只验收默认关闭的
  exact REMOTE Action HTTP Host；R2 为 `W2_R2_WASM_HOST_ACCEPTED_DEVELOPMENT_SLICE`，只验收默认
  关闭的 exact WASM Action Host；R3 为 `W2_R3_UNTRUSTED_MODULE_ISOLATION_ACCEPTED_DEVELOPMENT_SLICE`，
  只验收显式授权的第三方 WASM 纯计算与停机撤权。Operator Module Apply 合同仍为 `experimental`。包含
  公网或任意第三方 Channel Provider、任意第三方进程内模块、任意模块组合的通用 multi-Port、同一 PortPlan
  多 ProviderBinding、任意权限语言、R1 exact 合同以外的 REMOTE、R2 exact 合同以外的
  WASM/ABI/Host、自动发现和在线控制面的
  广义通用装配仍为 `planned`。
- W2-E2 为 `W2_E2_TRUSTED_TEXT_STATS_APPLY_ACCEPTED_DEVELOPMENT_SLICE`，只验收普通 Pure Chat
  Store 经统一 Apply 安装 compiled `text.stats` 后的原 Gateway/Attempt 纵链、Disable 与
  Backup/Restore；不开放任意第三方进程内 Action，也不改变上述三层成熟度。
- W2-E3 为 `W2_E3_WORKSPACE_CHANNEL_APPLY_ACCEPTED_DEVELOPMENT_SLICE`，只验收 first-party loopback
  `channel.transport/v1 + TRUSTED_IN_PROCESS/go-in-process/v1 + channel-binding-config/v1` 通过统一
  Apply 绑定 `WORKSPACE_CHANNEL_ENDPOINT`。ENABLED 原子提交 Control/Catalog 与 revision-0 Cursor
  seed；Dry-run 零 Store 写入、零 Secret 解析、零网络，Disable 保留 Cursor、Attempt、历史
  Control/Catalog 与 evidence，共享 Instance 只在最后一个引用消失时退出当前 Catalog。
- W2-E4 为 `W2_E4_DEEPSEEK_MODEL_REPLACEMENT_ACCEPTED_DEVELOPMENT_SLICE`，只验收
  `freeagent.builtin.model.deepseek@1.0.0` 在同一 Artifact、Adapter 和 Provider Instance 内，使用
  Operator 显式 Apply/Dry-run/CAS 执行 `deepseek-v4-flash`↔`deepseek-v4-pro`。它只替换唯一
  `model.generate/v1` Binding 的 Config、Authority 和可选 ModelProfile；省略 optional
  `model_profile` 表示清除。候选必须使用 `model-authority-ceiling/v1`，并通过临时 exact SecretRef
  grant；PriceSnapshot 必须预先存在 Store 且 provider/model/billing 完全匹配，模块包不能提供
  PriceSnapshot 或 Secret。Model Disable 禁止，回滚使用新的 ENABLED Apply。
- E4 publication 只影响新 Run；旧 Run 的 Binding、Config、Authority、SecretRef、PriceSnapshot
  和 Profile 保持冻结。UNKNOWN 不换模型、不建立替代 Attempt，也不语义重放。Store direct
  publication 与 Backup current semantic gate 都重验 Price/Authority/Profile closure；集中验收
  已覆盖跨 Workspace/Profile、Usage/cost、Backup/Restore 和失败关闭负例矩阵。该切片没有新增
  Schema、表、Runtime、Loop、Gateway、Catalog pointer 或效果账本，也不支持 Echo↔DeepSeek、
  自动选模、成本路由、跨 Provider、多 Provider、REMOTE/WASM 或广义通用装配。
- W2-E5-A 为 `W2_E5_A_REQUIRES_PERMISSION_GRANT_ACCEPTED_DEVELOPMENT_SLICE`。新的不可变
  governed Knowledge 版本仍只提供 `context.provide/v1`，但同时声明 exact `model.generate/v1`
  Require 并请求 `knowledge.read`；旧 permissionless 版本保持兼容。Manifest 只能请求，不能授权。
  Core 从同一 Profile 的 exact Binding→Catalog→Activation→Installation→Manifest 身份链、Config 与
  `knowledge-authority-ceiling/v1` 重算唯一依赖和有效 grant，并把检索上限裁剪为 Config/Authority
  交集；不保存第二份依赖图或 grant 事实。
- 首次 publication、exact retry、Apply/Dry-run、公开 Store Verify、Backup create/verify/restore 与
  生产 Knowledge loader 都执行同一确定性语义闭包。缺失、歧义、跨 Profile、循环、部分 Manifest、
  未知 permission、错误 Authority schema 或越过 Tenant/Workspace/Agent scope 均在任何 staging、
  Store 写入、Secret 解析或网络调用前失败关闭。Disable 只撤销新 Run 的 Knowledge 使用，历史事实
  和既有 Attempt/evidence 不删除。
- E5-A 集中验收覆盖真实本地 RAG Chat、grant 从 `4/4096` 收窄至 `2/2048`、未绑定/Disable 后
  Pure Chat 零 Knowledge、CAS 竞争，以及 enabled/disabled Backup→verify→restore。四个受影响包
  `./sdk/moduleapi`、`./cmd/freeagent`、`./internal/currentstore` 与 `./internal/currentbackup` 完整回归；
  该 E5-A 历史验收当时 `go test ./...` 的 30 个仓内 Go package 用时 221.9 秒，另有 1 个 external compatibility package
  由 `sdk/moduleapi` 嵌套测试在临时独立 module 中编译验证；`go vet ./...`、`go mod verify`、`gofmt -l .`、Docs（38 份
  Markdown）、Capability Matrix（49 项、稳定级 0 项）、License（35 个 Go dependency/57 个 distributed
  asset）、Branding 与 PublicTree 均通过。本切片未调用真实 API，也未新增 Schema、表、Runtime、
  Store、Loop 或 Gateway。
- E5-A 沿用现有单 Port Knowledge handler，是 E5-B 之前的历史窄切片。W2-E5-B 当前为
  `W2_E5_B_DOCUMENT_INSIGHT_DUAL_PORT_ACCEPTED_DEVELOPMENT_SLICE`：同一固定 Document Insight
  module 通过 Context 与 Action 两个不同 PortPlan 消费，并向两个 PortPlan 各贡献一个 Binding；
  Context PortPlan 同时保留既有 `context.basic`。两个 Document Insight Binding 及 Action Attempt
  使用完整相同 `ActivatedModuleRef`。
- E5-B 集中产品验收从 Pure Chat 开始，经 Context-first、Action-second 两步 Apply，在同一 Run 中
  实际完成本地 RAG、经唯一 Gateway 的 `text.stats`、2 个 Model Attempt 与 1 个 Action Attempt；
  exact Chat retry 零新增 Run/Attempt。Backup→verify→restore 后 RAG、Action 链与
  MemberExecutionSnapshot canonical bytes 不变，新 Run 继续消费双 Port。Action→Context Disable
  后回到 Pure Chat，错误顺序零写拒绝，独立 CAS 竞争只有一个 winner。
- E5-B 聚焦产品验收连续 3 次通过；独立 `cmd/freeagent` package 输出为 185.746 秒、墙钟为
  186.968 秒。该 E5-B 历史验收当时的 30 个仓内 Go package 全仓墙钟为 217.539 秒，其中
  `cmd/freeagent` package 为 215.004 秒；另有 1 个 external compatibility package 由 SDK 嵌套测试
  编译验证。`go vet ./...`、`go mod verify` 与 `gofmt -l .` 通过；Docs（38 份 Markdown）、Capability
  Matrix（49 项、稳定级 0 项）、License（35 个 Go dependency/59 个 distributed asset）、Branding
  与 PublicTree 均通过。本切片未调用真实 API，也未新增 Schema、
  表、Port、Runtime、Store、Loop、Gateway、Catalog pointer、grant 表或效果账本。
- E5-B 不开放公网/任意第三方 Provider 或 Action、任意模块的通用 multi-Port、同一 PortPlan 多
  ProviderBinding/merge/fallback、任意权限语言、REMOTE/WASM、不可信代码隔离、自动发现/升级或
  W6 在线控制平面。
- Production Host 按冻结的 ArtifactDigest + AdapterIdentity 首次物化可选 Adapter；同键并发加载
  合并、异键并行，失败不缓存。R1 production 链固定为 Catalog → lazy loader → Artifact digest/
  covered size/descriptor 复验 → `remoteactionhttp.NewFromArtifact` → native Adapter；该 cache 只保存
  descriptor 派生实现，不保存 endpoint、SecretRef、Secret material、Trust、Catalog 或授权结论，
  也不能替代每次调用前的 Authority/current Activation 检查。构件扫描可取消且不返回部分文件；
  MCP 子 Context 的取消/超时原因必须跨 SDK 包装保留，供关停、错误分类和同键接力判断使用，但
  不能据此重放任何外部效果。
- Action 必须经过 `ActionProposal → PreparedActionRef → DispatchAttempt → Gateway → Module Host
  私有 ExecutePrepared` 的最终权限、预算、效果分类和 `UNKNOWN` 处理；执行入口不暴露给模型。
  R1 的 Describe/Prepare 均为离线纯操作，generic `ModuleHost.Invoke` 明确拒绝 REMOTE Action；只有
  唯一 Gateway 在原 DispatchAttempt 已持久为 `PENDING` 并取得一次性 permit 后，才能调用私有
  `ExecutePrepared`。
- R1 Transport 只允许一次 HTTPS POST，不使用 proxy、redirect、HTTP/2、keepalive、retry 或
  fallback。解析和连接只允许公共 IPv4，以及经 special-use 拒绝表裁剪后的 IPv6 `2000::/3`；
  private、loopback、link-local、multicast、NAT64、文档/基准和其他 special-use 地址均失败关闭。
- REMOTE Secret 仅通过运行时 SecretRef→环境变量映射晚绑定，并作为 RFC 6750 Bearer material
  使用。SecretRef 可以进入冻结的 Binding/Store/Backup，Secret 字节不得进入 Current Store、
  artifact、Backup Bundle、日志或错误，也不得由模型、Manifest、descriptor 或 Provider 回显。
- POST 前的确定性拒绝进入原 Attempt 的 `FAILED`；一旦进入传输而无法证明远端结果，只能把原
  Attempt 置为 `UNKNOWN`。`UNKNOWN` 禁止语义重放、换 Provider 或创建替代 Attempt；exact retry
  只读取/对账原事实，不产生第二次 POST。
- R2 production WASM 路径固定为 Catalog → lazy loader → digest/covered size/descriptor/binary/ABI
  复验 → `freeagent.adapter.action.wasm/v1` → 原 Gateway。Host 固定使用 `wazero v1.12.0`
  interpreter、Core WASM 1.0 与 wasm32。guest 只能导出 `memory`、`freeagent_alloc_v1` 与
  `freeagent_execute_v1`；imports、WASI、Host Module、网络、文件系统和 Secret 全部为零。
- WASM ceiling 固定为 module 16 MiB、request 128 KiB、output 32 KiB、memory initial 最多 32 页且
  显式 maximum 最多 256 页、table 最多 65,536 elements、全进程最多 4 个 guest instances，单次
  验证与执行最长 5 秒。Describe/Prepare 不执行 guest，generic `ModuleHost.Invoke` 明确拒绝 WASM；
  唯一 Gateway 只在原 DispatchAttempt 已持久为 PENDING 后调用私有 `ExecutePrepared`。
- WASM Usage receipt 的 engine 固定为 `wazero-interpreter/v1.12.0`，并明确记录
  `instruction_metering=UNSUPPORTED`；它只保存 Host-observed input/output/memory/elapsed，不含 token、
  price、cost、raw trap 或 guest diagnostic。guest trap、timeout、ABI 与 output rejection 全部是原
  Attempt 的确定性 `FAILED`。
- 只有既有 Host 事务提交失败或崩溃遗留 PENDING 才由 startup recovery 把同一 Attempt 置为
  `UNKNOWN/RECOVERED_PENDING_AFTER_CRASH`；禁止 guest 语义重放、fallback、换 Provider 或创建替代
  Attempt。Backup/Create/Verify/Restore 保存 exact Manifest、descriptor、WASM binary、Action
  result/receipt 与终态，但不编译、实例化或执行 guest；恢复后冻结终态的 exact retry 也不重新访问
  artifact。该边界不是 OS/container sandbox，也不提供面向恶意多租户的强隔离。
- 未绑定、未启用或 Disable 后的 Pure Chat 对 REMOTE artifact、descriptor、Adapter、Secret
  resolver、Action Attempt 和网络保持零访问；对 WASM artifact、compiler 与 guest instance 同样为
  零访问。安装存在本身不能触发加载、解析 Secret、联网或执行 guest。
- Skill 是版本化的流程知识包，不是隐式权限。安装后按需加载，不自动进入所有 Agent 上下文。
- 第一方或经 Operator 审核的内部扩展只能作为精确 ArtifactDigest、本地 allowlist
  授权的 `TRUSTED_IN_PROCESS` 模块运行；该路径不接受不可信代码。
- 首个进程外扩展已经支持 Operator 精确 ArtifactDigest 授权的本地 MCP
  `2025-11-25 + stdio + Tool-only`。Describe 只在显式 Binding 的 Admission 中有界
  发现，Prepare 不启动进程，`tools/call` 只能由 Gateway 对已持久化 PENDING Attempt
  执行一次；歧义结果进入 UNKNOWN，禁止重放。
- 当前 stdio 进程使用空环境并受进程树管理，但没有 restricted token、namespace/
  seccomp 或文件系统/网络 sandbox，仍拥有 FreeAgent 当前 OS 用户的宿主权限。因此
  该能力只接受 Operator 完全信任的精确构件；不可信第三方和完整宿主隔离尚未实现。
- MCP Streamable HTTP、Resource、Prompt、Secret、Sampling、Elicitation、Roots、自动发现、
  动态订阅和常驻进程仍是后续工作；未来 Resource/Prompt 必须作为不可信上下文输入，
  也不能自行激活或扩大权限。
- FreeAgent 只通过 SecretRef 或受控 Host 注入凭据；这不构成对当前完全信任
  `LOCAL_PROCESS` 的宿主文件隔离。安装、升级、发现和激活仅影响新 Run，已冻结 Run
  继续使用精确版本。

上述模块或 MCP 扩展只有通过公开接口、conformance fixture 和必要的真实互操作测试后才能标为稳定。

### 6.9 Channel

Channel 是 Workspace 的可选入口/出口模块，不是 Agent 身份或 Core 启动的必填项。当前
首片只提供第一方 loopback 开发适配器，并遵守以下边界：

- 默认关闭；只有 Operator 显式选择唯一 Tenant、Workspace、Endpoint 与 SecretRef，
  且预置匹配 Cursor、Store 语义闭合、该 Endpoint 无未对账 Channel `UNKNOWN` 时才构造；
- ingress 以 Workspace Cursor 单调 CAS，ACCEPTED/REJECTED receipt 和事件 identity/hash
  去重；ACCEPTED 与 Run Admission 原子提交，REJECTED 不能伪造拒绝当前有效身份；
- 最终回复、History、`CHANNEL_SEND` PENDING 与一次性 permit 原子提交；无 Action 时为
  模型→Channel，有 Action 时为模型一→一个 Action→模型二→Channel，发送只由 Gateway
  对同一 `dispatch_attempts` 账本执行；
- 终态与 `WAITING_RECONCILIATION` 的重复 ingress 只读返回原结果，不重新发送；仍可运行的
  旧 Run 在继续前按当前 Endpoint、Identity、ACL 与 Binding 做 deny-only 复核；
- 回复目标必须同时包含并精确匹配冻结 Endpoint 的 `account_id` 与 `conversation_id`；
  loopback 只执行一次 literal-loopback POST，不使用 proxy、隐式重定向、retry 或 fallback；
- 关停先停止新 Core/Channel Admission，再排空已接收 handler；遗留 PENDING 在关闭 Store
  前由 Store-only recovery 原位转为 UNKNOWN，不加载 adapter 或发送网络请求；
- Cursor、receipt、Run、History、Attempt、外部 receipt/evidence 与终态投影进入同一个
  完整 backup/verify/restore 语义闭包。
- 统一 Channel Apply 已验收同一 Agent/Profile、同一共享 Instance 下双 Workspace/Endpoint：两侧
  Cursor、Run、receipt、终态和 Module History 不串流，成功与 `UNKNOWN` 的 exact duplicate 都不
  二次发送，一个 Endpoint 的未对账 Channel `UNKNOWN` 不阻塞另一个；恢复后各自从下一 Cursor
  revision 继续。exact Apply retry 即使 Cursor 已推进，也按原 revision-0 seed 复验，而不是把当前
  Cursor 冒充发布 seed。

未启用 Channel 的 Pure Chat 不读取 Channel config、Cursor、receipt、SecretRef 或 artifact，
不构造 adapter，不开放 ingress。Telegram、Lark 等公网 Provider、平台 webhook 认证、远程
凭据生命周期与真实第三方互操作均延后；新增 Provider 必须实现统一 Channel Port/Host
合同，不能建立自己的 Runtime、Store、Outbox 或重放策略。

## 7. 典型产品模式

### 7.1 `pure_chat`（默认）

只绑定模型和单 Agent loop。无 Role/Persona、Knowledge/RAG、Memory、Skill、MCP、Tool、Channel、可选 Host 或扩展 SecretRef，适合本机聊天、嵌入式客服或最小验证。

### 7.2 Domain RAG

绑定可选 Role、共享授权 RAG 和领域标签。Agent 自身保持轻量，遇到领域任务时检索相应 collection。

### 7.3 Team

Composite 解析任务并分配领域权重，多个 Specialist 并行产出，Reviewer 独立串行审核。
legacy S3-B 已实现一次审核门；W5-X1 已实现显式 Decision 下最多一次预冻结修复，以及双边
授权的受限跨 Workspace Specialist；W5-F1 已补齐这条纵链的公平调度、顺序、取消、恢复与
权限稳定性。自动 Reviewer、无限返工、动态图和任意内容交换仍属后续。

### 7.4 Custom platform

Operator 自行提供 Profile、Skill、显式 MCP Server、Channel Provider，以及经过审核的可信 `TRUSTED_IN_PROCESS` 模块，把 FreeAgent 仅作为权限、状态、预算、账本和恢复核心。

## 8. 非功能要求

### 8.1 可靠性

- 所有状态迁移可重启恢复；
- 外部调用具备 pre-wire/post-wire 状态；
- 终态持久化失败可补偿，不重复外部效果；
- 迁移、配置和冻结哈希不允许部分提交；
- 优雅关停先停止准入，再排空已接收效果。

### 8.2 安全

- 默认失败关闭；
- 明文 Secret 不进入配置快照、RunManifest、日志或账本；
- HTTP 禁止隐式 POST 重定向；
- 远程端点执行 DNS、最终连接地址和 Workspace 出站策略检查；
- Module、Skill、MCP 和 Agent 经 Core/Host 协议的调用不能绕过内核权限；当前无
  OS sandbox 的 Operator-trusted `LOCAL_PROCESS` 是显式宿主级完全信任边界，不接收
  不可信代码；
- 隐私删除覆盖持久状态、索引、缓存、工件和外部状态验证。

### 8.3 可观测性

每次 Run 应能追溯：

- 选择了哪些 Agent、模型和模块；
- 哪些权限与预算生效；
- 使用了哪些上下文、知识和摘要；
- 每次外部调用的状态和最终证据；
- Token、缓存和费用；
- 降级、拒绝、恢复和人工决策原因。

### 8.4 性能与资源

- 单机 SQLite 版本优先保证正确性和可恢复性；
- 多 Workspace 在持续负载下不能饥饿；
- 上下文、配置、附件和响应均有明确大小上限；
- 检索、Tool、模型、可信模块和 MCP 调用具有 deadline 与并发上限；
- 统计和学习任务不能阻塞用户回复交付。

## 9. 同类项目参考与差异

本节仅借鉴公开架构思想，不复制实现。

| 项目 | 借鉴点 | FreeAgent 的不同选择 |
|---|---|---|
| [LangGraph](https://docs.langchain.com/oss/python/langgraph/persistence) | checkpoint、持久状态、恢复和 human-in-the-loop | FreeAgent 把 Workspace ACL、外部效果账本、每成员装配和 `UNKNOWN` 作为内核一等对象，而不以应用图作为唯一顶层抽象 |
| [CrewAI](https://docs.crewai.com/) | 角色化 Agent、Crew 与结构化 Flow 的组合 | FreeAgent 保留 Composite/Specialist/Reviewer，但 Role 可选；Agent 身份与共享 Knowledge、权限和运行装配分离 |
| [OpenAI Agents SDK](https://openai.github.io/openai-agents-python/) | 小型 Agent loop、handoff、guardrail、MCP 与 tracing | FreeAgent 更强调自行托管的持久账本、模型中立、Workspace 隔离、冻结版本和离线对账 |
| [Dify](https://docs.dify.ai/guides/knowledge-base/retrieval) | 模型接入、RAG、工作流和应用运营的一体化 | FreeAgent 首先是可嵌入运行核心，不以低代码 UI 或完整应用平台为首版目标 |
| [Letta](https://docs.letta.com/guides/core-concepts/memory/context-hierarchy) | 持久 Agent、分层上下文和共享 Memory block | FreeAgent 默认让 Agent 保持轻量，把大部分领域知识放入授权 RAG；Memory 是可选模块且受治理和预算约束 |
| [OpenHands](https://docs.openhands.dev/sdk/arch/overview) | SDK、Tools、Workspace 与 Agent Server 分层，沙箱和可复用专业 Agent | FreeAgent 不限定软件工程场景，并把多 Workspace 公平调度、共享知识、成本缓存和外部效果一致性作为通用能力 |

这些资料于 2026-07-19 通过各项目官方文档核对。FreeAgent 的目标不是把所有功能堆进一个框架，而是让产品层能力可以替换，同时让权限、预算和恢复边界始终一致。

## 10. 成熟度与验收

当前开发能力状态统一使用：

- `accepted`：当前明确范围内的消费者、失败边界和恢复门禁已经闭合，但不表示生产稳定；
- `experimental`：实现存在，但支持范围或公共合同仍可能变化；
- `unverified`：代码或专用路径存在，但要求的完整产品链或外部证据尚未闭合；
- `planned`：只有需求、设计或后续工作入口，当前不对可用性作声明。

逐项权威状态见[当前能力清单](CURRENT_CAPABILITIES.md)。该清单只索引开发状态；具体语义仍
由三份当前长期编码规格定义，验收证据仍由一次性切换清单记录。

旧实现的证据名称和历史状态见[历史能力成熟度矩阵](RELEASE_MATURITY.md)。
该矩阵不再证明当前生产成熟度；当前编码边界以三份长期规格为准，切换状态以
一次性 `CUTOVER_ACCEPTANCE` 的验收结果为准。

当前 S1 开发基线已经封存，S2.1 与 ModelProfile 又针对当前树重新运行以下技术链路；更早、物理减重
前的同名证据仅作历史参考：

1. 显式 init、Pure Chat、CLI/loopback HTTP 与重启精确 retry；
2. 唯一 Assembly Compiler、Universal Loop 和 Current Store；
3. ModelDispatchAttempt、MODEL_UNKNOWN、Usage、History 与启动恢复；
4. 真实进程 Kill、终态事务失败、优雅/强制关停与完整 backup/restore；
5. 64 请求长链路不存在重复 Run、Attempt 或 logical operation；
6. 未实现能力 fail-closed；Channel 默认关闭，未装配时对 Channel 配置、Cursor、SecretRef、
   adapter 与网络保持零访问。

MCP 首片进一步验证了精确 operator grant、官方 Go SDK `v1.6.0` 与协议
`2025-11-25` 握手、分页/总量边界、stdout 污染拒绝、一次 `tools/call`、受管进程树
关停、明确 JSON-RPC/Tool error 与歧义 UNKNOWN 的区分、重启/完整备份零重放，以及
历史 MCP PENDING/UNKNOWN 存在时 Pure Chat 启动仍对 MCP artifact/SDK/process 零访问。

Operator Module Apply v1 开发切片进一步验证了停机 writer fence、包内同根隐藏
staging、完整构件校验、声明式 Context 恢复或 no-launch Host 构造、
Install/Activate/Content 与唯一 Catalog CAS、精确重试、Enable/Disable 引用保留、CAS 前后
故障收敛和 PENDING→UNKNOWN。声明式 Role/Skill 已有双 Workspace/Profile 隔离与完整
backup/restore 证据；MCP 构件恢复按受信 Manifest 重建可移植权限，且不启动模块。该证据不
解除 S3-C 的 `3 COMPLETE + 1 PARTIAL`，也不批准 S3-D 或 Public Stage。

W2-B1 进一步闭合停机态 current-only list/inspect 与独立 deny-only disable：查询通过
immutable/query-only Observer 返回完整 Basis、精确 Binding refs 和非敏感 Manifest 摘要，
不启动模块或建立效果；Disable 继续使用原 Apply/recovery/CAS 并保留全部不可变历史。
W2-B2 在同一边界增加只读 dry-run：共享 Apply 的当前状态判断与候选冻结逻辑，在系统 TEMP
验包，预测 ENABLE/DISABLE 的 Installation、Activation、Binding 和 Catalog 变化。测试证明
`WOULD_APPLY` candidate Basis 与无并发写入时随后真实 Apply 的最终 Basis 一致；NO_CHANGE/
ALREADY_APPLIED 不读取 source；MCP 不起进程；PENDING 只报告恢复需求，UNKNOWN 不重放；
数据库、sidecar、owner lock、artifact root、source bytes/mode 和 TEMP 均无新增残留，且该
W2-C 验收树当时仍为 21 表、冻结 schema fingerprint 不变。该历史结果不是授权、reservation
或 receipt。

W2-C 原位扩展同一个 Apply/Dry-run 与 `context.provide/v1`：`UNTRUSTED_DATA` 加
`knowledge-context-binding/v1` 才选择本地只读 Knowledge；Manifest 只能声明精确
`TRUSTED_IN_PROCESS + go-in-process/v1` 数据入口，本地策略固定授予
`freeagent.adapter.knowledge.lexical/v1`。Artifact entrypoint、Config 与 Authority 必须指向
同一个 exact `knowledge-source/v1`，Tenant 与当前 Control 中的 scope 必须闭合，Binding 固定为
`REQUIRED` 且不携带 StaticContext refs。模块包不能选择 Adapter、Trust 或 Authority，也不会
执行包内代码；dry-run 不调用 provider。

W2-C 当时只复用现有 Knowledge Runtime、Store、Loop 与通用 artifact backup；Knowledge Context
不绕行副作用 Gateway，也没有新增 Port、Runtime、Store、Gateway、表、执行账本或 backup
schema。它不包含 W3 的 Collection 标签、计数、
重复检索免检、Memory、远程向量库或知识更新；当时也未包含 disabled/history 与稳定公共/在线
控制面。因此它增强自由外挂和共享知识的最初设计，但不能单独表示 W2 总工作包完成；当前
Memory Apply 与 exact `module-history` 状态分别以 W3-M1 和本节 §6.8 的后续记录为准。

W2-D 当前状态为 `W2_D_LOCAL_ASSEMBLY_ACCEPTED_DEVELOPMENT_SLICE`。该 accepted 只把本地、停机、
显式的 Role/Skill、Knowledge、W3-M1 Memory 与受信 MCP 装配边界统一起来；Memory 内容只是同步
W3-M1 已验收合同，不改写实现归属。新增 `module-history` 对调用方已知的 exact Control/Catalog
pair 执行窄只读查询，并通过既有 Installation 投影复验 stored Manifest closure；它不读取
artifact root、不输出 Manifest 正文/摘要、不构造 historical PublishedBasis，也不枚举、计算 diff
或派生 DISABLED 状态。集中产品纵链、逐 Catalog Entry 和 unbound inspect 两项 P1 回归、Windows
全仓 188.9 秒、有效 WSL2 ext4 全仓 128.1 秒、vet/mod/gofmt、License 35/55 与 PublicTree 均已
闭合；Operator 入口与 Module Conformance 继续 `experimental`，广义通用装配继续 `planned`。

W2-E2 在不改写上述 W2-D 历史的前提下追加验收：网络无关的
`TestW2E2TextStatsApplyAcceptanceV1` 从普通 Pure Chat Store 与官方 unpacked `text.stats` artifact
开始，不使用 Action seed，经 production dry-run/apply 闭合零写/零提前加载、真实
model→action→model、exact retry 零新增、Backup/Restore 后旧账本不变且新 Run 仍可调用，以及
Disable 后新 Run 回到 Pure Chat。当前状态为
`W2_E2_TRUSTED_TEXT_STATS_APPLY_ACCEPTED_DEVELOPMENT_SLICE`；本证据未调用真实 API，不能作为模型
效果、缓存率或任意第三方进程内执行的验收。

W2-E3 继续以只追加方式验收 Workspace Channel Apply。统一计划的 target 固定为
`WORKSPACE_CHANNEL_ENDPOINT`；`ENABLED` 在同一 `BEGIN IMMEDIATE` 内发布 Control/Catalog 并写入
revision-0 `CURSOR_SEED`，Dry-run 不写 Store、不解析 Secret、不联网。集中测试
`TestW2E3ChannelMultiWorkspaceIsolationBackupV1` 以同一 Agent/Profile 和共享 Channel Instance
绑定两个 Workspace/Endpoint，证明 Cursor、Run、receipt、终态与 Module History 隔离，成功与
Channel `UNKNOWN` duplicate 零重发，B Endpoint 的未对账 `UNKNOWN` 不阻塞 A；Disable B 后 A 的
引用保留当前 Catalog entry，B 的 Cursor/Attempt/history/evidence 不删除。Backup/Restore 保留 5 条
ingress receipt、2 个 Cursor scope 与 1 个 Channel `UNKNOWN`，恢复后 A 从 revision 2 推进至 3，B
保持 revision 1，历史 Module Binding 投影逐字节一致。当前状态为
`W2_E3_WORKSPACE_CHANNEL_APPLY_ACCEPTED_DEVELOPMENT_SLICE`；未单独构造 `MODEL_UNKNOWN`，也不声明
公网 Channel、任意第三方进程内代码、Beta，也未获得首次部署许可。

W2-E4 又在同一 Apply/Catalog CAS 上增加唯一第 7 个 Core handler：
`model.generate/v1 + TRUSTED_IN_PROCESS/go-in-process/v1 + model-binding-config/v1`，exact selector
固定为 `freeagent.builtin.model.deepseek@1.0.0`。集中验收覆盖 flash→pro、pro→flash 回滚、Dry-run
零写、exact retry、stale CAS、同一 Agent 跨 Workspace/Profile、可选 Profile 设置与省略清除、
Price/Authority/SecretRef/Profile mismatch、Usage/cost、旧 Run 冻结、UNKNOWN 原 Attempt 零替代，
以及 Backup/Restore 后 Config、Authority、SecretRef、PriceSnapshot 与 Profile 不漂移。Store direct
publication 和 Backup current semantic gate 均重建并拒绝不闭合的 current Model 事实。当前状态为
`W2_E4_DEEPSEEK_MODEL_REPLACEMENT_ACCEPTED_DEVELOPMENT_SLICE`；它没有调用第二 Runtime、Store、
Loop、Gateway 或账本，也没有把 Operator Module Apply overall 提升出 `experimental`。

W2-E5-A 随后继续复用第 3 个 Knowledge handler，不增加 handler tuple。不可变 governed Knowledge
Manifest 保持单一 `context.provide/v1` Provide，同时以 exact pair 声明 `model.generate/v1` Require
和 `knowledge.read` request；legacy permissionless Manifest 继续通过，permission-only、require-only
及任何扩展形态失败关闭。Apply/Dry-run 会在 fast path 或 staging 前复验当前完整 publication 和
同一 Profile Model 身份链；Store 的首次 publication、exact retry、公开 Verify 与 Backup semantic
gate 也从不可变事实重算依赖与 grant。生产验收闭合 RAG Chat、grant 收窄、Disable、CAS、完整
恢复与历史字节稳定。该历史切片状态为
`W2_E5_A_REQUIRES_PERMISSION_GRANT_ACCEPTED_DEVELOPMENT_SLICE`；当时 `go test ./...` 的 30 个仓内
Go package 为 221.9 秒，另有 1 个 external compatibility package 由 SDK 嵌套测试编译验证，
四受影响包完整回归，vet/mod/gofmt、Docs 38、Capability 49 项/稳定级 0 项、License 35/57、Branding
与 PublicTree 全部通过，且未调用真实 API、未新增 Schema、表、Runtime、Store、Loop 或 Gateway。
该证据只验收真实 Requires 与窄 grant，不能用于证明后续双 Port。

W2-E5-B 随后以同一受信 `freeagent.builtin.document-insight@1.0.0` Installation/Activation/Instance
闭合 Context+Action 双 Port。Manifest 固定有序 Provides、唯一 Model Require 与唯一
`knowledge.read` request；两个 reserved exact selector 复用现有 Context/Action tuple，因此唯一协议
tuple 总数仍为 7。产品链以两次 Apply 发布，同一 Run 实际执行 RAG 与 Gateway `text.stats`；两个
Document Insight 的两个 Binding 及 Action Attempt 使用完整相同的 `ActivatedModuleRef`，Context
PortPlan 另保留 `context.basic`。exact retry 零新增，Backup/Restore 后
RAG、Action 与 MemberSnapshot 字节不变，按 Action→Context 顺序 Disable 后回到 Pure Chat，CAS
竞争一个 winner。当前状态为
`W2_E5_B_DOCUMENT_INSIGHT_DUAL_PORT_ACCEPTED_DEVELOPMENT_SLICE`。

E5-B 聚焦产品验收连续 3 次通过；独立 `cmd/freeagent` package 输出为 185.746 秒、墙钟为
186.968 秒。该 E5-B 历史验收当时的 30 个仓内 Go package 全仓墙钟为 217.539 秒，其中
`cmd/freeagent` package 为 215.004 秒；另有 1 个 external compatibility package 由 SDK 嵌套测试
编译验证。vet/mod/gofmt、Docs 38、Capability Matrix 49/0、License 35/59、Branding 与 PublicTree
均通过。本切片没有真实 API 调用，也
没有新增 Schema、表、Port、Runtime、Store、Loop、Gateway、Catalog pointer、grant 表或效果账本。
它只证明固定受信产品模块，不证明任意模块通用 multi-Port、同一 PortPlan 多 ProviderBinding、
REMOTE/WASM、第三方不可信代码隔离、Beta 或生产部署。

其后的 W2-R1 前序状态保持为
`W2_R1_REMOTE_ACTION_HOST_ACCEPTED_DEVELOPMENT_SLICE`。R1 只接通
`action.provider/v1 + REMOTE/freeagent-action-http/v1` 与 native
`freeagent.adapter.action.remote-http/v1`：Manifest entrypoint 必须是 ArtifactDigest 覆盖的
`content/` descriptor；endpoint 与 SecretRef 由 Operator-owned Binding 提供，Secret value 只在
Gateway dispatch 时晚绑定。Apply/Dry-run 需要 exact artifact、HTTPS endpoint 与 SecretRef 三项
瞬时 grant，但始终离线；运行时默认关闭，只有显式 `--enable-remote-actions` 和有效的
`--remote-action-secret-env <secret-ref>=<ENV_VAR>` 映射才能进入执行链。

R1 的集中生产路径已从 Catalog 穿过 lazy loader、digest/size/descriptor 复验、
`remoteactionhttp.NewFromArtifact`、native Adapter、已持久化 PENDING permit 与唯一 Gateway，并由
`TestW2R1NativeRemoteProductionChainFailsClosedBeforePOSTV1` 在 POST 前闭合确定性 `FAILED`；
`TestW2R1ProductionLazyLoaderRejectsTamperedArtifactV1` 证明篡改构件连续拒绝且失败不缓存；
`TestW2R1DisabledRemoteProviderHasZeroProductionAccessV1` 证明 Disable 后删除 artifact 的真实 Pure Chat
对 REMOTE artifact、Adapter、Secret resolver 与 Action Attempt 零访问。Provider 返回合法
`FAILED + HTTP 422` 时也只有一次 RoundTrip。

SUCCEEDED、传输后歧义 `UNKNOWN`、UNKNOWN 禁止重放、终态持久化失败、重启与完整 Backup/Restore
由 hermetic native Adapter 和长链分层证据闭合。生产集中链本身只闭合到 pre-POST `FAILED`；当前
没有真实公网 HTTPS 第三方互操作证据，不得把该开发切片写成生产网络 SLA。R1 没有新增普通表、
第二 Runtime/Store/Loop/Gateway、Catalog pointer 或效果账本；R1 自身不验收其他 REMOTE、WASM、
不可信本地隔离、供应链、自动发现/升级、Beta 或生产部署。

W2-R2 收口时状态为
`W2_R2_WASM_HOST_ACCEPTED_DEVELOPMENT_SLICE / W2_R3_UNTRUSTED_MODULE_ISOLATION_NEXT`。它增加
第 9 个 exact `action.provider/v1 + WASM/freeagent-action-wasm/v1 + action-binding-config/v1`
handler 与 `freeagent.adapter.action.wasm/v1`，但没有增加第二 Runtime、Store、Loop、Gateway、
Catalog pointer、效果账本或 ordinary table。Operator Apply/Dry-run 要求 exact
`--allow-wasm-action-artifact`；R2 当时运行时必须显式 `--enable-wasm-actions`，其余保持默认关闭和
Catalog lazy load。

`TestW2R2WASMActionProductionLongChainBackupRestoreV1` 已闭合 Apply → production composition →
model→ActionProposal→原 PENDING Attempt→唯一 Gateway→native WASM→model、canonical Usage receipt、
exact retry 零新 Run/Attempt/guest、Backup/Restore 与恢复后删除 artifact 的冻结终态零重放。
`TestW2R2WASMActionPendingReopenBecomesUnknownWithoutReplayV1` 又证明 guest 返回后终态事务失败只
遗留原 PENDING；startup recovery 仅把该 Attempt 置为
`UNKNOWN/RECOVERED_PENDING_AFTER_CRASH`，删除 artifact 后重入仍不 lazy load、不建新 Attempt、
不执行 guest。默认关闭负例由 `TestW2R2WASMActionBoundRuntimeDefaultsOffV1` 闭合。

R2 Host 的 wazero/Core v1/wasm32、零 imports/WASI/Host Module/network/filesystem/Secret、资源 ceiling、
私有 Gateway 执行、`instruction_metering=UNSUPPORTED`、deterministic FAILED 与 Backup no-execution
边界见 6.8。该 accepted 只证明窄 WASM capability boundary，不是 OS/container，也不提供面向恶意多
租户隔离；R3 在其后独立收口如下。

W2-R3 当前状态为 `W2_R3_UNTRUSTED_MODULE_ISOLATION_ACCEPTED_DEVELOPMENT_SLICE`。它允许非 builtin
第三方 Module ID 使用相同纯计算协议，但需要进程级 exact digest allowlist；WASM Adapter 只保存
artifact-scoped immutable identity，不缓存 Tenant、Workspace、Instance、Activation revision、Config
或 Authority。同一 artifact 可被多个合法 Activation 共享，权限仍由唯一 Gateway 的 exact current
Activation/Binding 闭包决定。

Gateway 在 lazy resolution 前与私有 executor 前分别检查 current Activation，第二次成功是
execution-admission linearization point。此前撤权确定性 FAILED 且 guest=0；此后已接纳的
`Effect=none` guest 可在既有 5 秒 ceiling 内完成。当前撤权只支持停机流程：关闭 Admission、排空或
取消、Store-only PENDING recovery、退出销毁 cache、离线 `module-disable` CAS、重启。活动服务持有
Store 时返回 `STORE_BUSY` 且零变化；Disable 不改写历史 Installation/Activation/Run/Attempt/UNKNOWN。
最后引用 Disable 后，移除 runtime allowlist 并删除历史 artifact，Pure Chat 仍零 WASM 加载。

R3 没有新增 Schema、表、Runtime、Store、Loop、Gateway、Catalog、Port 或账本，也不声称 OS/container、
热撤权、fuel/instruction/RSS 硬上限、运行中 guest 强杀或生产恶意多租户隔离。其后 W2-U0 只冻结
供应链纯合同，W2-U1 验收本地 governed observation，W2-U2 再以 observation-only 边界持久化
Source/Index/Snapshot 与 current revocation。随后 U3 闭合 Candidate/Decision，U4 闭合窄 approved
Declarative Profile Context replacement。W6-0 随后冻结六份 pure canonical 控制 API wire 与
transport-neutral policy；其历史入口是 `W6_1_APPLICATION_SERVICES_READ_API_NEXT`。W6-1 已闭合默认
关闭的 read/Dry-run Control surface；其当时入口是 `W6_2_CONTROLLED_MUTATIONS_AUDIT_NEXT`。

随后完成的 W3-M0/M1/M2 与 K0/K1/K2A/K2B/K3 在上述 W2-C 基础上闭合了本地 RAG/Memory
实用化：Conversation + Knowledge/Memory 使用同一历史复编译；Memory 进入统一 Module Apply；
Knowledge 支持确定性标签路由、独立 shortcut、全 Binding preflight 和严格 exact-question
reuse；Conversation Summary 只复用直接成功前驱的可验证 raw-prefix，并由 Store 与 Backup
确定性重编译验证。fresh/reuse/shortcut 证据分离，reuse 不调用 Provider，默认 Pure Chat 仍对
RAG/Memory 零访问。W3 没有新增 Schema、表、Port、Runtime、Store、Loop、Gateway 或第二摘要/
缓存服务；远程向量数据库仍未实现。W4 Learning 已作为独立、默认关闭的显式周期纵链验收，
不改变这里的 W3 与 Pure Chat 边界。

Channel 首片进一步验证了显式 Workspace/Endpoint 启用门禁、Cursor
ACCEPTED/REJECTED 与事件去重、ACCEPTED/Run 原子 Admission、同一
`dispatch_attempts`/Gateway/UNKNOWN 账本、模型一→Action→模型二→Channel、终态与
`WAITING_RECONCILIATION` 重入只读、严格 account/conversation 目标、无重定向/重试、
优雅关停和完整 backup/restore。当前只验收默认关闭的第一方 loopback 开发适配器；
当前静止树门禁已随 Composite 批次重新执行，公网 Provider 与第三方互操作尚未验收。

Composite 首片进一步验证了 whole-family 原子 Admission、确定性 Child 身份和单向
manifest digest 图、有界并行推进与稳定结果顺序、受保护 assignment、不可信 Child-result
envelope、50% 硬分配、attempt-free Core failure、N+1 dispatch cap、统一取消与
cancel-vs-permit 线性化、family Usage/Cost 的 NULL/混合币种语义，以及完整
backup/verify/restore 和语义篡改拒绝。真实 `chat --composite` production composition、
Pure Chat golden bytes/零可选访问、Windows/Linux 全仓测试、Linux Race 和六平台构建均
已通过。该段 S2 证据不包含 Reviewer、公平 Scheduler、Learning 或跨 Workspace；公平
Scheduler 的后续独立证据以两份正式规格的 S3-A 章节为准。

Reviewer 审核门进一步验证了 optional Control/Manifest、独立确定性 Run 身份、whole-family
原子 Admission、完整有序 Specialist result set、Reviewer `max_tokens` ceiling、canonical
`ReviewVerdictV1` 成功终态、APPROVE-only merge，以及 REJECT/FAILED/INVALID 无 merge、
UNKNOWN 零重放和 Reviewer-disabled bytes 不变。Reviewer 与 Root 仍复用同一个
Universal Loop、Current Store、RunLease 和模型 Attempt；APPROVE 已持久化但 Root 尚未
merge 是可备份恢复的合法中间态。该 S3-B 历史开发树当时的 Schema 为 20 表；真实外部模型的审核质量、
token/延迟收益和长时间多 Workspace 效果留在 S3-C/S3-D。

W5-X1 在不改写上述历史裁决的前提下，进一步验收了显式 Decision、完整 `2N+3` 物理 Run 图、
最多一次预冻结 repair、受影响槽位激活、未激活 repair 的 `Attempt=nil`，以及同 Tenant、
root/target 双边 grant 的受控跨 Workspace Specialist。REQUEST 仅携带从 Store-loaded
`TASK_INPUT` 确定性有界提取的 `TASK_SUMMARY`，RESULT 仅携带 `SPECIALIST_RESULT`；两者的
payload/envelope、grant、Run/Attempt/Usage 与终态均进入同一 Store 和完整 Backup 语义闭包。
该切片没有第二 Runtime、Store、Loop、Gateway 或 Usage 账本，仍为 24 张 ordinary tables，
只新增两个 Transfer ContentKind。

2026-08-09 的 X1D 真实 DeepSeek 验收完成 2 个 Specialist、Reviewer 与 Root 的 4/4 HTTP 2xx，
其中恰好一个 Specialist 跨 Workspace；REQUEST/RESULT transfer 均验证通过。family 为 7 个
physical Runs、4 个 Attempts 和 3 个无 Attempt 的 repair Run，exact retry 新 HTTP/Attempt 均为 0。
Usage 为 input 3,443、cached 768、uncached 2,675、output 1,353、reasoning UNKNOWN，token 加权
缓存命中率 `22.306128%`，estimated cost `0.00539636 CNY`，Provider reported/reconciled cost
UNKNOWN。以上数据仅证明 X1D 的窄纵链，整体发布边界仍以本文开头为准；
S3-C 的 `3 COMPLETE + 1 PARTIAL` 与 4 个 `MODEL_UNKNOWN` 仍按原 Attempt 保留且禁止语义重放。

W5-F1 在上述 X1 历史事实之上追加协作稳定性验收，不改写其 Run/Attempt 或 Usage 数据：

- 三个始终 runnable Workspace 共完成 900 次 claim，在第 450 次后关闭并重开 Store；最终每个
  Workspace 恰好 300 次，首次服务不晚于第 3 次 claim，最大服务间隔不超过 3；
- Decision approve、单槽 repair 和 Decision+Transfer 均经唯一 Scheduler；dormant/skipped
  repair Run 保持 `Attempt=nil` 且零 claim；
- 跨 Workspace Specialist 即使反向完成，仍按 frozen plan 稳定归位；Store reopen 后
  canonical bytes 与身份不变；
- family cancellation 覆盖跨 Workspace 的完整 `2N+3` family，取消后零新 Attempt、Transfer
  payload 与 envelope；
- Transfer UNKNOWN 只凭可靠 evidence 对账原 Attempt：SUCCEEDED 只生成一个 RESULT，FAILED
  不生成 RESULT；stale revision、替换 Provider、替换 Attempt 或无 evidence 均失败关闭；
- 完整 Decision+Transfer 已通过 backup→verify→restore→reopen；PENDING 恢复为同一 Attempt
  UNKNOWN，Universal Loop 的语义重放计数为 0；
- 双边 grant、Tenant、Workspace revision、方向、kind/schema/ref/size、48 KiB ceiling 与撤权
  均有权限负例；Pure Chat 和 ordinary Composite 均保持零 Transfer material。

工程门禁分两步闭合：Windows 全仓测试、`go vet`、`go mod verify` 与 gofmt 检查均通过；WSL
首轮受影响七包 race 中六包通过，CurrentStore 仅暴露 1 秒 TTL 墙钟测试竞态且没有 data race。
固定逻辑时间后，定向 race、单核慢调度和完整 CurrentStore race 均通过，完整门禁以 exit 0
在 466.241 秒结束；首轮七包命令本身不记作整体 exit 0。

F1 Test-0808 canary 的 4/4 HTTP 2xx、零新增 exact retry、Usage/cache/cost 数据见 6.7；该证据
仍只表示开发切片收口。项目未部署；W2-D 本地显式装配 v1 后续已在窄范围 accepted，但 Operator
Module Apply 保持 `experimental`，Module Conformance 仍处实验期，广义通用装配与 W6/W7 保持
`planned`，不能据此标记 `RELEASE_READY`。

W1 Conversation 的首个开发切片在 2026-08-06 记录为
`W1_CONVERSATION_ACCEPTED_DEVELOPMENT_SLICE`；该历史状态只描述当时尚待真实调用收口的源码树，
不覆盖 2026-08-08 的 W1 最终开发验收。该首片进一步验证了固定 owner/scope、head/revision CAS、一轮一 Run、
成功 head 才可继续、完整 USER+ASSISTANT predecessor pair、pair 不可分割的摘要/Drop、精确
retry、stale head 拒绝，以及 backup/verify/restore 后继续下一轮。CLI 已能创建、读取和
继续 Conversation；loopback HTTP `POST /v1/conversations` 已闭合严格 scope、首次 201 与
精确重试 200，`/v1/chat` 的创建后续聊和服务重启 E2E 也已闭合。正常 DeepSeek Provider
已从历史实验入口分离并通过 mock 生产 Adapter E2E，冻结 PriceSnapshot + 完整 token 的
`estimated_cost` 已与 Usage 同事务持久化。确定性本地
`TestRunConversationFiftyTurnsAcrossReentryBackupRestore` 已在 Windows 21.964s、WSL 15.594s
通过，覆盖 50 Run/Attempt/Usage、turn 25 backup→verify→restore 后续聊、turn 25/50 精确
重入、最终 bundle 与 Action/Memory/Channel 零访问；该本地测试本身不等于真实 DeepSeek 50 轮。
`TestRunConversationFiftyTurnsAcrossNormalProcessRestart` 还通过两个正常退出的独立 OS 进程
分别执行 1..25 与 26..50 轮，再次验证相同的 50 Run/Attempt/Usage 与零可选模块访问。
Windows/WSL 全仓 `go test`、`go vet` 与 `go mod verify` 已通过。

2026-08-08 的后续真实验收已将 W1 收口为 `W1_COMPLETE_REAL_DEEPSEEK_50`：50/50 个首次
Attempt 成功，turn 25 backup→verify→restore 与 turn 25/50 exact retry 通过，自动重试关闭，
Model/Action/Channel PENDING 和 UNKNOWN 均为 0，测试 Secret 与可再生资产已清理。该状态仍
只表示普通单 Agent Pure Chat 开发纵链完成；Conversation 与 Composite 互斥，Conversation+
Action/Channel 不声明成熟，不能据此标记生产部署或公开 Beta 完成。

`TestW1PureChatUnifiedZeroOptionalModulesAcceptanceV1` 在同一生产 Composition/Loop/Store 中进一步
证明：Knowledge、Memory、静态 Skill、MCP/Action 和 Channel 已安装、激活并 Catalog-visible 但
未选择时，仅冻结 Model 被解析；请求无动态 Context/Action，Memory、Learning、Channel、Transfer、
Composite 与可选 Content 均零增量。该集中证据不扩大 Conversation 组合成熟度。

S2.1 进一步验证了 `<85%` 不压缩、`==85%` 生成单一 compilation（有合格旧历史时
最多一次确定性摘要，否则记录稳定停止原因）、`==100%` 直接按顺序移除最小完整旧
前缀、静态 Context 与最近窗口保护、不可信数据封装、编译记录与模型 Attempt 原子
持久化、重启不重编译，以及完整 backup/restore 后规范字节与引用不变。
这些证据不冒充尚未实现的生产多轮聊天。

ModelProfile 进一步验证了精确模型 build/config/artifact/adapter 闭包、发布与
Admission 前失败关闭、只收紧不扩大 ContextPolicy、较小画像参与 85% 门禁、无画像
零额外读取，以及重启和完整 backup/restore 后 ref 与 canonical CONFIG 字节不变。
评测执行器、基于倾向的自动选模和在线画像更新尚未启用。

RAG 已进一步通过 W3 验证共享知识 artifact、四级 scope、确定性路由、独立
`NOT_SELECTED` shortcut、全 Binding Provider 前预检、严格 latest exact-question reuse、污染
隔离、deny-only 撤权，以及 PENDING/UNKNOWN、重启和完整 backup/restore 不重放。Catalog 中
存在知识模块不会迫使无 RAG Profile 读取 artifact；reuse 时 Provider 零调用，任一 revision、
权限、TTL、Memory head 或证据变化立即 fresh。远程向量数据库、远程计费检索以及把周期
Proposal 自动审核、物化并 Apply 为活动知识的路径仍未实现；W4-L4 只提供显式逻辑周期。

Memory 已进一步通过 W3-M1/M2 验证统一 Module Apply/Dry-run/Disable 装配、Genesis 幂等、
Agent/Workspace 独立作用域、严格 canonical 配置与快照、任务输入专属计数、Begin 时 head
校验、首次成功终态原子 revision、并发 Run 顺序合并，以及失败/UNKNOWN/重入不写入。
`TASK_SUMMARY` 与 Conversation Summary 明确分离；M2 只复用直接成功前驱的连续最旧 raw pair
前缀且禁止 summary-of-summary。默认 Profile 即使 Catalog 中存在 Memory 也保持零访问；后台
Learning/Memory worker 仍未实现。W4-L4 的默认 24 小时间隔 Schedule、显式 Tick 与 Store-only
补偿属于独立可选能力，不会隐式访问 Memory。

新增的声明式 Role 与静态 Skill 仍复用 `context.provide/v1`，并已加入当前树的真实
Install → Activate → Bind → Pure Chat E2E；该 E2E 与当前树全仓技术证据已经重验
通过，不为 Skill 引入专用 Runtime 或隐式执行权限。旧专用 Skill、Channel 实现已经退役；
声明式 Skill 与首个 loopback Channel 已分别通过统一 Module/Port、Universal Loop 和
Current Store 工作包重建，不恢复旧 Runtime。

完整产品目标的最终验收还包括：

1. 最小聊天在零可选模块下可运行；
2. 同一 Agent 在不同 Workspace 的权限和装配互不泄漏；
3. Specialist 可并行产出，Reviewer 可在其后独立审核并冻结结果；
4. 共享 RAG 不跨数据作用域；
5. 85% 压缩和 100% 最小完整前缀移除行为确定；
6. 模型、Tool、Channel `UNKNOWN` 不发生语义重放；
7. Usage、Cost、Cache 和预算字段可对账；
8. 崩溃、重启、关停、备份和恢复通过长链路测试；
9. Module、Skill 与 MCP 只有在真实 Host 测试完成后升级成熟度；
10. 模块包格式可以离线、确定性验证，但验证结果不能冒充安装、授权或互操作证据；
11. README、Schema、示例和能力矩阵与实际代码一致。

## 11. 路线图

### S0：语义与基线（已完成）

- 冻结两份编码规格和一次性切换清单；
- 提取旧实现中需要保留的业务语义；
- 建立最小 canonical seed、源码归档和旧规格降级边界。

### S1：唯一 Core 开发基线（已验收；从未正式部署）

- 一个 Assembly Compiler、Universal Loop 和 Current Store；
- Pure Chat、声明式 Context、exact Model、History、Usage 与恢复；
- 显式 init、完整 backup/restore、CLI 与 loopback HTTP；
- 清理旧生产接线并完成发布与 operator gates。

### W1：真实聊天与配置闭环（已完成真实 DeepSeek 50 轮开发验收）

- 已完成同一 Store/Loop 上的 Conversation head/revision CAS、一轮一 Run、历史 pair、阈值、
  CLI 与 loopback HTTP 创建/续聊、服务重启、正常 OS 进程 25+25 轮和 backup/restore 后续聊；
- 该 W1 完成快照的 Store 为 21 表；没有增加 Session 实体、第二 Runtime/Store/History/Usage
  或效果账本；W2-U3 的历史 32 表与 W6-2 的历史 33 表值均以本文开头为准；
- DeepSeek 冻结价格估算已经进入原 Usage 终态事务；真实 50/50 首次 Attempt、turn 25
  backup/restore 和 turn 25/50 exact retry 已闭合；
- 已安装但未选择的 Knowledge、Memory、Skill、MCP/Action、Channel、Learning 与 Team 由统一
  Pure Chat 验收证明零解析、零请求暴露与零可选持久化增量；
- Conversation 与 Action/Channel/Composite 的后续组合边界、生产部署和公开 Beta 仍未闭合。

上述 W1 补齐没有新增第二 Runtime、Store、Loop、History、Usage 或费用账本，也没有把任何
可选专业能力固化进 Agent；最初的轻量 Agent、共享授权知识和模块自由外挂方向保持不变。

### S2：按真实纵向工作包扩展

1. Context Compiler（S2.1 已完成）；
2. ModelProfile（已完成）；
3. RAG（W3 已完成 K0–K3 本地共享知识实用化开发纵链；远程向量数据库未实现）；
4. Memory 与 Context Summary（W3 已完成 M0–M2；统一装配、基础记忆与摘要复用语义分离）；
5. Action/Gateway 与一个 Built-in Tool（已完成首个本地 `text.stats` 纵链）；
6. MCP（已完成首个 Operator-trusted 本地 stdio Tool 纵链）；
7. Channel（已完成首个默认关闭的 Workspace-scoped loopback 纵链；公网 Provider 延后）；
8. Parent/Child Run 与复合 Agent（已完成同 Workspace、depth-1、2..8 Specialist 的第一切片）；
9. Scheduler/公平调度（S3-A 已验收）；Reviewer 单次审核门（S3-B 已验收）；
10. Operator Module Apply v1（停机态显式 Apply/Dry-run、非 Model Disable 与 exact history 已覆盖
    Role/Skill、Knowledge、W3-M1 Memory、本地 MCP Action、受信 compiled `text.stats`、
    Workspace-owned loopback Channel Endpoint，以及同一内建 DeepSeek 构件内 flash/pro Model
    replacement；E5-A 又为 governed Knowledge 接通 exact `model.generate/v1` Require 与窄
    `knowledge.read` grant；E5-B 以固定 Document Insight 模块闭合 Context+Action 两个 PortPlan 的
    真实 RAG/Gateway 产品链；R1 又以默认关闭的 exact
    `REMOTE/freeagent-action-http/v1` 接入原 Action/Gateway/Attempt/Backup 纵链；R2 再以默认关闭的
    exact `WASM/freeagent-action-wasm/v1` 接入同一纵链；R3 再为该协议闭合第三方纯计算授权、二次撤权
    检查和停机 Disable。W2-D、W2-E2、W2-E3、W2-E4、W2-E5-A、W2-E5-B、W2-R1、W2-R2 与 W2-R3
    已按各自窄范围 accepted。Operator 合同与 Module
    Conformance 仍 `experimental`；自动选模、成本路由、跨 Provider/多 Provider、任意第三方进程内
    Action、公网或任意第三方 Channel Provider、任意模块的通用 multi-Port、同一 PortPlan 多
    ProviderBinding、任意权限语言、R1 exact 合同以外的 REMOTE、R2 exact 合同以外的
    WASM/ABI/Host、OS/container 与生产恶意多租户隔离、热撤权和广义装配仍 `planned`；W2-U0
    供应链纯合同、W2-U1 本地签名/来源观察、W2-U2 持久 Source/Snapshot observation 与 W2-U3
    离线 exact-version Candidate/Review/Decision 已完成窄开发验收，当时下一功能入口为 W2-U4
    Operator Apply。唯一的 9 条 generic/Model policy 与 2 条 Document Insight reserved exact selector 已从 CLI 提取为 Review/Apply 共享实现，
    未复制第二表；approved Apply 在该历史时点仍未完成接线，随后已按窄 U4 合同验收）；
11. Learning PR、Skill Draft 与 24 小时逻辑周期（W4 已完成开发切片：L0 合同、L1A/L1B
    Proposal Store、L2 单次有界 Reviewer、L3 无权限不可变 Version/Operator artifact 交接、
    L4 默认关闭且显式 Tick 的 Learning Cycle、Store-only 补偿和只读报告；自动
    Review/Materialize/Apply、安装、激活、绑定、扩权及后台 worker 未实现）；
12. 有界 Decision/repair 与受控跨 Workspace 协作（W5-X1 已完成开发切片）；持续负载公平性、
    稳定归位、整族取消、UNKNOWN 对账、恢复与权限负例（W5-F1 已完成开发切片）；在线 Control
    Plane（W6）与公开 Beta（W7）仍是后续主路线，不由固定 E5-B 或窄 R1/R2 产品切片提前启动或声明可用。

### W5-X1：有界协作与 Workspace transfer（已验收开发切片）

- 保留 legacy 同 Workspace Composite 和单次 S3-B Reviewer，同时增加显式 Decision family；
- 预冻结 `2N+3` 个物理 Run，最多一次 repair，未激活槽位不创建 Attempt；
- 每个 Run 仍绑定单一 Workspace，只在同 Tenant、双边 grant 下交换 `TASK_SUMMARY` 与
  `SPECIALIST_RESULT`；
- X1D 真实 canary、exact retry、Usage、UNKNOWN 和完整 backup/restore 已闭合；
- 不包含自动 Reviewer、无限返工、动态图、任意 payload、生产部署或公开发布。

### W5-F1：协作稳定性（已验收开发切片）

- 不读取任务正文、不引入第二 Scheduler/Store/Loop；三个持续 runnable Workspace 在 900 次
  claim 和第 450 次 Store reopen 后各获 300 次服务，首次服务不晚于第 3 次，最大间隔不超过 3；
- Decision approve、单槽 repair 与 Transfer 使用唯一 Scheduler，dormant/skipped Run
  `Attempt=nil` 且零 claim；frozen plan 顺序和身份在反向完成、Store reopen 后保持稳定；
- 跨 Workspace `2N+3` family 取消后零新 Attempt/payload/envelope，UNKNOWN 只对账原 Attempt，
  完整 backup/restore/reopen、权限负例与 Pure Chat/ordinary Composite 零 Transfer canary 已闭合；
- Test-0808 canary 为 4/4 HTTP 2xx，exact retry 新 HTTP/Attempt 为 0；该本地开发证据不构成
  生产公平 SLA、正式部署、公开 Beta 或 `RELEASE_READY`。

### W6：轻量在线 Control Plane（W6-2 首个窄 mutation wiring 已验收）

- W6-0 已冻结 `control-session/scope/view-snapshot/operation-request/operation-receipt/event-cursor` 六份
  v1 pure canonical wire。Web view 独占 `control-view-snapshot/v1` 与
  `freeagent.control-view-snapshot/v1`；Core `control-snapshot/v1` 与摘要域保持不变；
- Operation request 显式区分 `DRY_RUN/MUTATE`，动态 request ID 与时间不进入 semantic request；
  `UNKNOWN` 只能读取原 exact receipt，不得语义重放；
- W6-0 的历史收口是 `W6_0_CONTROL_API_CONTRACT_ACCEPTED_DEVELOPMENT_SLICE /
  W6_1_APPLICATION_SERVICES_READ_API_NEXT`。它当时不含 listener、handler、session、production wiring、
  Schema 或 receipt table，证据为 39 份 Markdown、37 个源码 package、35 个 production closure package
  与 32 表 Store；
- W6-1 在同一个 `freeagent serve` 中增加默认关闭的 Control 接线。显式启用后，Chat listener 与独立
  `tcp4 127.0.0.1:0` Control listener 共享唯一 Store、Application Services、startup-closed Admission 和
  关停生命周期；actual origin 与一次性 bootstrap 只经 owner-only handoff 交付，交换得到 process-local
  session。host-only Cookie 不绑定端口，因此除 bootstrap exchange 外，Modules GET 与非安全方法都要求
  exact session 加 session-bound CSRF header；GET 缺失或错误 CSRF 返回 401；
- W6-1 当时的 HTTP 能力仅为 scope-filtered Modules list/detail 和 exact If-Match 的 effect-free
  `MODULE_DISABLE` Dry-run。Dry-run 拒绝 idempotency/confirmation authority，不执行 mutation，返回的
  `DRY_RUN` receipt 不持久化；既有停机 CLI Apply/Disable 仍是独立既有路径，不能冒充 Control
  mutation；
- W6-1 历史收口是 `W6_1_APPLICATION_SERVICES_READ_API_ACCEPTED_DEVELOPMENT_SLICE /
  W6_2_CONTROLLED_MUTATIONS_AUDIT_NEXT`，证据为 39 份 Markdown、44 个源码 package、44 个 production
  dependency-closure package 与 identity 不变的 32 表 Store；
- W6-2 audit 逐项检查六个 operation，只把 TENANT/PROFILE、exact `context.provide/v1`、`OPTIONAL`、
  `DECLARATIVE`、trusted-instruction config、deny-all Authority 的 `MODULE_DISABLE` 留作首候选。
  Upgrade Review 因 caller-owned artifact path/signature、Learning 两项因 reservation/异步 Attempt/Task/Run、
  Upgrade Apply 与 Module Apply 因 staging/install/activation/grant 等更宽长链全部 deferred；该 audit 的
  历史收口为 `W6_2_CONTROLLED_MUTATIONS_AUDIT_ACCEPTED_DEVELOPMENT_SLICE /
  W6_2_CONFIRMATION_CONTRACT_NEXT`；
- confirmation 原子新增稳定 `control-confirmation-statement/v1` 与
  `control-module-disable-evaluation/v1`。Statement 的 `operation_evaluation_digest` 绑定向用户展示的 exact
  candidate；raw proof 另为 32 random bytes、最多 2 分钟且不超过当前 session absolute expiry、全局 256/每 session 8，registry 只存 domain-separated
  proof digest，不进 Store/Backup/log；
- 该 confirmation 历史原子的状态是 `W6_2_CONFIRMATION_CONTRACT_ACCEPTED_DEVELOPMENT_SLICE /
  W6_2_DURABLE_RECEIPT_SCHEMA_NEXT`，证据为 39 份 Markdown、45 个源码 package、44 个 production
  dependency-closure package 与 identity 不变的 32 表 Store。Control mutation、durable row、SSE、UI、
  第二 Store/writer、worker 与主动预热均不存在，首片 `UNKNOWN` 不可达；下一入口只允许验收单一
  append-only receipt 表、32→33 rebuild-only Schema/Backup 候选和同事务闭包，不授权 mutation route。
- receipt Schema 历史收口状态是 `W6_2_DURABLE_RECEIPT_SCHEMA_ACCEPTED_DEVELOPMENT_SLICE /
  W6_2_MODULE_DISABLE_MUTATION_WIRING_NEXT`。共享 `module-apply-plan/v1`、full PublishedBasis 与
  `module-disable-publication-receipt/v1` 已冻结；33 表只新增 append-only `control_operation_receipts`；
- Store resolver 从 exact Request 派生唯一 identity；同 digest 返回原 receipt，同 identity 不同 Request
  冲突。公开 commit 只接受 neutral replay 闭合、basis 未变化的 `NO_CHANGE`；不能发布 pointer 或插入
  `APPLIED`；
- semantic verifier 严格 Restore Request/input/plan/evaluation/Control receipt/pre-post basis/domain receipt，
  核对不可变 Control/Catalog parents并重放 neutral evaluator；Backup Create/Verify/Restore 都在 coherent
  snapshot 内调用。它只验证现存行；单表没有外部 completeness anchor，不能证明整行未被删除；
- 该历史原子的证据为 39 份 Markdown、47 个源码 package、46 个 production dependency-closure package，Store
  identity 为 33 表 / fingerprint `51be9081a815e6379380b93e745db03d33ca2f358962f85ace63bce2534dcc10` /
  migration 67,998 bytes / SHA-256 `8feea38beb505ab4f371afe32eecd877744062bd149d0d24982839c5450fc952`。
  当时仍无 mutation route/handler、confirmation endpoint、APPLIED public insert、SSE/UI/worker；其历史
  下一入口是 `W6_2_MODULE_DISABLE_MUTATION_WIRING_NEXT`。
- mutation-wiring 的历史收口状态是 `W6_2_MODULE_DISABLE_MUTATION_WIRING_ACCEPTED_DEVELOPMENT_SLICE /
  W6_3_WEB_SHELL_READ_ONLY_OVERVIEW_NEXT`。仅显式 `--enable-control` 下接通首个窄 confirmation/mutate
  两路、lookup-first、`NO_CHANGE/APPLIED` durable receipt 与同事务 publication；其他 mutation、SSE/UI/
  worker 和第二 Store/writer 当时仍不存在。
- W6-3 的历史收口状态是 `W6_3_WEB_SHELL_READ_ONLY_OVERVIEW_ACCEPTED_DEVELOPMENT_SLICE /
  W6_4_MODULES_CONFIGURATION_UI_NEXT`。同一默认关闭 Control listener 提供 exact embedded Web Shell 和
  scope-filtered Overview GET；服务端与浏览器双重验证 scope/basis/section/projection/ETag，UI 只做授权范围内
  Tenant/Workspace selector、Overview cards、search、detail drawer/deep link，并明确 loading/empty/error/
  stale/permission-denied。该 W6-3 历史切片 Store identity 为 41 tables / 23 indexes / 56 triggers、fingerprint
  `87d63a2e468ee27ee2c0e1918a92e580532825bde85dbb872b9c795bf577f2c1`、migration 143,588 bytes /
  SHA-256 `5bd9743e988b82750f785c015f07c11fbcf6025da70148405aec11106b4efb22`。该切片当时无业务 mutation UI、SSE、
  worker、durable client cache 或第二 Store/writer。
- W6-4 的历史状态是 `W6_4_MODULES_CONFIGURATION_UI_ACCEPTED_DEVELOPMENT_SLICE /
  W6_5_SERVER_OWNED_MODULE_ARTIFACT_INGRESS_NEXT`。它只把现有 Modules list/detail 与 `MODULE_DISABLE`
  三段 operation 接入浏览器：严格验证 authority/digests/ETag/cursor，只有窄 TENANT/PROFILE OPTIONAL Binding
  经两次显式确认可写；proof/key/CSRF 不进入 durable/visible client state，不可判定结果只允许 exact retry，
  APPLIED/NO_CHANGE 后只重新读取。没有 artifact ingress、其他 operation/mutation、Schema、第二 Store/writer、
  SSE、worker、远程 Control 或自动升级。
- 当前 W6-5 状态是 `W6_5_SERVER_OWNED_MODULE_ARTIFACT_INGRESS_ACCEPTED_DEVELOPMENT_SLICE /
  W6_6_SERVER_OWNED_MODULE_UPGRADE_REVIEW_NEXT`。唯一新入口是显式
  `module-artifact-ingress --enable-module-artifact-ingress` 的可信本地 Operator CLI；source/artifact roots
  只在调用期使用，不进入 Store/Backup canonical。caller 不提供 package path、URL 或 signature，只能选择
  Store-owned current Snapshot 的 unsigned `LOCAL_DIRECTORY + DENY` exact entry。filesystem durable-first、
  digest-addressed/no-replace publication 后，唯一 Store 在同一 `BEGIN IMMEDIATE` 写入不可变 Artifact 与
  append-only Admission；对象保持 inert。没有 HTTP/upload/UI、Install/Activate/Bind/grant/Review/execute。
  Source/artifact/Backup tree 使用 held-parent-handle 相对遍历并拒绝 link/reparse、hardlink、跨设备及 change/
  namespace 漂移；artifact root、children 与全部 ancestors 都须可信私有，跨进程 lease 覆盖 crash recovery、
  publish、sync 与 root 级 256 trees / 512 MiB covered content / 32,768 paths / 16,384 files / 16 MiB
  path-name bytes 硬预算（不声称等于实际 allocation blocks）。ambiguous commit 的物理对象保持有界 inert；只有 durable exact
  Admission 可恢复历史 selector，未提交 stale selector不能采用。新 ingress 的同 digest 目标缺失时始终以目录
  `0700`、文件 `0600` 发布；既有同 digest 目标经完整 bytes/mode 复验后只接受两种 root-global 闭包：全目录
  `0700` / 全文件 `0600`；或全目录 `0700`，仅 canonical `LOCAL_PROCESS + mcp-stdio/2025-11-25` descriptor
  精确绑定的唯一 executable 文件为 `0700`，其余文件 `0600`。两者都拒绝 special/setid/sticky 与 group/world
  权限。artifact root 跨 Store 共享时模式判定不得依赖任一单独 Store 的 Installation；后一种只是既有物理兼容态，
  不授予当前 Store Installation/Activation/execution authority。W6-5 的 inert 是无 Store authority 且本切片不执行，
  不等同于 OS executable bit 不存在。Backup 以 Installation 与 ingress 的去重并集
  闭合，并从私有 staging 支持 uninstalled/offline restore。

每个工作包只有在 Host、Store owner、真实生产消费者、安全负例和恢复测试全部完成
后才能 Activate/Publish；不建设第二套 Runtime、Store 或装配事实源。

## 12. 对最初设计的影响

本 PRD 不改变最初方向。它继续保留：

- 轻量 Agent 与共享授权知识库；
- 多 Workspace、公平调度和跨 Agent 协作；
- Composite 偏结构、Specialist 偏专业内容、Reviewer 独立审核；
- 85% 压缩与 100% 时按顺序移除“刚好足够”的完整旧前缀；
- 模型评测驱动的上下文工程；
- 自学习提案、审核、去重和被拒来源拦截；
- Token、缓存和费用核算；
- 所有 Role/Persona、Memory、Knowledge/RAG、Skill、可信模块、MCP、REMOTE/WASM Action Host 和
  Channel 适配器均可选择、替换或不安装；`pure_chat` 不依赖其中任何可选项。未绑定 REMOTE Action 时，
  Pure Chat 对其 artifact、descriptor、SecretRef/Secret resolver、Adapter、Action Attempt 和网络访问
  均为 0；未绑定或未启用 WASM Action 时，对其 artifact、compiler、instance 与 Action Attempt 也
  均为 0；未启用 Channel 时也不会读取其配置、Cursor、凭据或构件。

新增的不可变装配、账本和失败关闭规则只约束“怎么安全运行”，不决定“Agent 必须是什么”。
Channel 与 R1/R2 REMOTE/WASM Action Host 都只是可选 Port/Provider，不成为第二 Runtime、第二 Gateway 或
固定入口。因此这些变更提高开放度和可恢复性，不会把系统重新变成固定产品模式。
