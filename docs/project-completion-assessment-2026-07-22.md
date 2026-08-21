# FreeAgent 项目完成度评估

> 状态：`HISTORICAL_BASELINE_NON_NORMATIVE`
>
> 本文只记录 2026-07-23 旧架构的阶段评估，不代表当前实现、发布成熟度或生产
> 接线状态。当前权威仅为 `CORE_RUNTIME_V1`、`CURRENT_STORE_V1` 与一次性
> `CUTOVER_ACCEPTANCE`。

评估日期：2026-07-23  
评估范围：P0 规范闭包、P1-A / P1-B / P1-C 有界基础与 R0-0 至 R0-3 公开整理  
发布定位：**AGPL-3.0-only 早期公开预览 / P1 bounded foundation**

## 1. 结论

FreeAgent 已完成 P0 规范冻结和 P1-A、P1-B、P1-C 的有界基础实现。P0 Governance rule wire 已标记 `LOCKED`，模型/权限与内容/存储两条独立复审及锁定后差异复审均为 `0 Critical / 0 Important / 0 Minor`；P1 最终独立审计同样为 `0C / 0I / 0M`。普通全量测试和 shuffled 全量测试均通过。

这表示可信内核的精确目录、冻结装配、SQLite 所有权和运行生命周期基础通过了本轮验收。它不表示完整 Governance 4B2、开放扩展 Host、CoreModel 执行权限闭包、真实外部渠道互操作或 PostgreSQL 已完成。

从产品成熟度看，当前版本适合定位为“早期公开预览 / 有界基础”，不适合宣传为面向正式部署的完整多 Agent 平台。公开源码目录仍有第 8 节列出的上传前清理项，完成后再建立 Git 仓库或上传。

## 2. 分阶段完成度

| 阶段 | 本轮判定 | 已完成 | 明确边界 |
| --- | --- | --- | --- |
| P0 Spec Closure | 完成 | 448 条关系的规范闭包、seed、双重独立复审和锁定差异证明 | 这是规范冻结，不是全部生产能力实现 |
| P1-A RuntimeCatalog | 有界基础通过 | tenant-scoped exact current CAS、sealed membership、严格 exact load、无 fallback、SQLite 原子发布、重启与租户隔离 | 尚未实现 P2 完整治理 leaf/current-watermark authority |
| P1-B MemberSnapshotV2 / RunManifestV2 | 有界基础通过 | canonical 冻结、严格恢复、成员集合闭包、`pure_chat` 空装配、CoreModel 结构父引用 | 非空 Skill/content authority 仍失败关闭；CoreModel route 不授予执行权限 |
| P1-C Freeze / Lease / Finalize / Recovery | 有界基础通过 | 原子冻结、generation lease、quiescence proof、终态持久化、恢复、并发和故障回滚 | 当前只证明空 execution inventory；legacy/P2 execution row 失败关闭 |
| P2–P5 | 未实施完毕 | 仅保留设计、契约或局部基础 | 不属于本轮完成范围，不得提前标记为 `stable` |

P0 的可复算摘要和锁定差异见 [`P0 规范锁定证明`](architecture/specs/2026-07-22-p0-spec-lock-attestation.md)。

## 3. P1 实现结果

### 3.1 P1-A：RuntimeCatalog

已实现：

- tenant-scoped、精确版本的 current CAS；
- append-only generation 与严格 `LoadExact`；
- 只能由精确查询产生 sealed membership resolution view；
- 运行使用时重新核验 generation、ordinal、provider 和 entry；
- SQLite 原子 publish、有限 BUSY/LOCKED retry、重启恢复、并发 CAS 和租户隔离；
- Skill 不属于 executable provider identity。

本节所评估的同名 `internal/runtimecatalog` 与历史
`internal/store/sqlite/runtime_catalog.go` 均已从当前源码树物理退役；这里只记录
2026-07-23 的历史结论，代码只能从仓库外 S0 离线源码归档追溯。当前产品仍保留
RuntimeCatalog 的逻辑职责，但由唯一 Current Store 与统一 Module/Port 边界重新承载，
不能据本节推断存在同名运行包。

### 3.2 P1-B：MemberSnapshotV2 / RunManifestV2

已实现：

- MemberSnapshotV2 的规范化冻结字段和有序集合；
- RunManifestV2 的 canonical encoding、严格 restore 和成员集合闭包；
- `pure_chat` 的明确空数组和零可选装配；
- forged candidate、版本不一致、集合不一致和 ordinal 不一致失败关闭；
- CoreModel route parent 的结构校验。

当前边界：

- 包不实现完整 Governance leaf 语义；
- 非空 Skill/content authority 返回 `ErrAuthorityUnavailable`；
- CoreModel route 只是可审计结构，`AuthorizesModelExecution` 保持 false；
- 因此公开的“每成员 Module/Skill/MCP exact locks”仍是 `planned`，不因内部冻结结构存在而晋级。

本节当时对应的 `internal/modulebinding` 与 `internal/runmanifest` 已从当前源码树物理
退役，不再提供指向空目录的“当前代码”链接；历史实现只能从仓库外 S0 离线源码归档
追溯。当前 S1 的冻结装配由现行两份长期规格及唯一 Current Store/Compiler 实现裁决。

### 3.3 P1-C：运行生命周期

已实现：

- sealed freeze bundle 和跨父对象验证；
- generation lease、原子 Freeze、Finalize 和 Recovery；
- quiescence proof 与 terminal ordinal 内部绑定；
- SUSPENDED/WAITING 保留 lease，真正终态在效果静止后释放；
- 并发、重启、幂等、故障回滚、corruption fail-closed 和跨 sibling 一致性。

当前边界：P1 只证明 execution inventory 为空。若发现 legacy/P2 execution row，系统拒绝由 P1 路径归一化或终结；Freeze 本身也不构成模型调用权限。

本节当时对应的 `internal/runlifecycle` 与历史
`internal/store/sqlite/run_lifecycle.go` 已从当前源码树物理退役；代码只能从仓库外
S0 离线源码归档追溯，不能把本节当作当前运行入口或当前包清单。

### 3.4 SQLite 所有权与关闭边界

本轮还闭合了以下 P1 运行风险：

- 单进程和跨进程唯一 Store owner；
- sibling 只由 owner 私有派生，不能递归铸造；
- 物理连接统一验证 `foreign_keys`、`secure_delete`、normal locking 和 busy timeout；文件数据库要求 WAL，`:memory:` 数据库要求 memory journal mode；
- BadConn replacement 保持 PRAGMA；
- 路径、URI、symlink、dangling link 和 hardlink 失败关闭；
- 在线 Store 与离线 backup 互斥；
- 在 P1 空 execution inventory 边界内，Finalize/Recovery 使用原子持久化和有限重试；非空效果的完整闭包属于 P2–P5。

这些约束强化的是存储和生命周期可信边界，没有把 SQLite 变成 Agent 模块依赖；SQLite 仍只是默认后端。

## 4. 原始设计不变量

| 最初设计 | 当前状态 | 本轮影响 |
| --- | --- | --- |
| 轻量、模块化核心 | 保持 | P0/P1 增加证据、权限门和恢复边界，不强制装入产品模块 |
| Agent / Workspace / Profile 自由编排 | 保持 | 冻结的是一次运行的精确选择，不限制下一次配置或组合 |
| Role/Persona、Knowledge/RAG、Memory、Skill、MCP 可选外挂 | 保持 | 可信内部能力使用精确固定的 `IN_PROCESS` 模块，进程外能力使用显式 MCP；未完成能力失败关闭而非内置替代 |
| `pure_chat` 安全默认且零可选模块 | 保持 | 新安装和未被 Operator 修改的历史默认使用 `pure_chat`；允许零 Role/Persona、零 RAG、零 Memory、零 Skill、零 MCP、零 Tool 和空 provider/content 集合 |
| 共享、按域触发的 RAG | 保持 | Agent 可继续轻量化；知识检索由作用域、标签、权限和缓存约束 |
| Skill 是内容，不是执行权限 | 保持 | 执行能力必须来自精确 Module/MCP 授权，Skill 不能自行扩大权限 |
| 85% 压缩、100% 顺序 Drop | 保持 | 85% 触发压缩；100% 只移除使占用回落所需的最小完整旧前缀，不清空全部上下文 |
| UNKNOWN 禁止语义重放 | 保持 | 没有 NOT_EXECUTED 证据时，模型、工具和渠道效果不能重放原语义请求 |
| CoreModel 位于 RuntimeCatalog 外 | 保持 | P1 只冻结结构父引用，不借目录授予 CoreModel 执行权 |
| cold-family 默认关闭 | 保持 | 只允许显式控制与成本比较，不自动决策或制造预热请求 |
| 特定人格或情感系统不进入核心 | 保持 | 将来只能作为可选外挂模块扩展人格、情感或行为 |
| SQLite 默认 | 保持 | PostgreSQL 仍是后续后端目标 |
| 多 Workspace 公平与学习治理 | 保持 | 第一方 SQLite 调度、24h 更新、去重、拒绝来源和补偿扫描已有测试基础 |

可信核心固定的是身份、权限、状态、预算、外部效果、冻结和恢复边界；它不规定 Agent 必须具有什么角色、性格、领域知识或外挂模块。

## 5. 能力矩阵

[`capabilities.v1.json`](../testdata/release/capabilities.v1.json) 是能力状态与证据绑定的唯一机器可读权威。本评估不再复制总数或逐项列表，避免退役能力、矩阵清理和证据晋级后出现双重真相。原始数量只能描述当时的证据分类，不能换算为项目完成百分比。

当前判定原则保持：

- 缺少真实外部互操作证据的渠道或协议能力只能是 `unverified`；
- Skill 治理、MCP stdio/Streamable HTTP、每成员精确扩展锁等未完成闭包只能是 `planned` 或 `experimental`；
- 可信 `IN_PROCESS` 模块与显式 MCP 是唯一扩展执行模式；已退役的协议兼容层或 Host 模式必须从当前能力矩阵移除，不能继续计入产品能力；
- `pure_chat` 无可选 attachment 的多轮连续性在取得要求的真实证据前不得提前晋级。

## 6. P2–P5 后续范围

这些内容没有纳入本轮实现：

- **P2：完整 Governance 4B2 运行实现。** 四类 leaf、完整 resolver、DataScope、resolved policy、grant、非空 Skill/content authority 和生产 verifier。
- **P3：扩展执行平面。** Sandbox/CapabilityBroker、生产 MCP discovery/execution、MemberGateway、内容 provenance/撤销和完整 Reconciliation V2。
- **P4：CoreModel 执行权限与账本闭包。** route/build/config/TCB、模型执行授权、UsageCounterRule、价格/费用、Budget、owner-fenced attempt/ledger 和模型 UNKNOWN reconciliation。
- **P5：后端与生产发布闭包。** PostgreSQL、双后端 conformance、SQLite ↔ PostgreSQL transfer/cutover、真实 Host 互操作、完整 race 和长链路发布证据。

后续实现仍须逐项回答“是否改变最初设计”。凡是把可选模块变成核心依赖、给 Skill 隐式执行权限、给 `pure_chat` 注入可选内容、恢复全局 fallback、允许 UNKNOWN 语义重放或把特定人格/情感系统内置进核心的方案，都应直接拒绝。

## 7. 验证结果与限制

本轮通过：

- P0 双重独立语义复审与双重锁定后差异复审：`0C / 0I / 0M`；
- P1 最终独立审计：`0C / 0I / 0M`；
- Go 工具链：`go1.26.5 windows/amd64`；
- `gofmt` 检查；
- `go mod verify`；
- `go vet ./...`；
- `go test -count=1 -timeout=20m ./...`；
- `go test -shuffle=on -count=1 -timeout=20m ./...`；
- Windows amd64 和 Linux amd64、`CGO_ENABLED=0` CLI 构建；
- construction、branding、capability matrix 和敏感信息门禁；
- SQLite 重启、并发、故障回滚、所有权、backup 互斥、UNKNOWN 和 lifecycle 长链路测试。

限制：

- 本机没有可用的 CGO/GCC race 环境，Go race detector 未执行，race 结论仍为 `UNPROVED`；CI 中存在 Linux/GCC/CGO race gate，不等于该 gate 已实际通过；
- 外部渠道真实互操作没有纳入本轮；
- reasoning-token 供应商语义和 `pure_chat` 真实多轮仍为 `unverified`；
- 当前公开目录没有 Git 历史，因此本评估记录文件 SHA、日期、命令和 Go 工具链，不引用不存在的 commit SHA；
- 普通与 shuffled 全量测试证明当前测试覆盖内的行为，不替代真实生产流量、外部服务故障或长期 soak test。

## 8. 发布判定

完成以下公开目录清理后可使用的发布标签：

> FreeAgent — AGPL-3.0-only early public preview / P1 bounded foundation

R0-0 至 R0-3 已封存；R0-4 的退役施工资产清理与跨平台阻断修复均已实现。规范现位于 `docs/architecture/specs`，迁移清单固定旧新路径、大小和 SHA-256；施工计划、6 个退役脚本、唯一空施工目录及其中的本机绝对路径已移除。永久 PublicTree 门禁继续拒绝退役文件名和该次版本迁移演练的固定产物名，不恢复一次性迁移编排。R0-4 的最终完成判定由仓外只读 snapshot metadata 与验证 evidence 共同给出，本评估不替代或提前声明该机械判定。

R0-4 复验还发现并修复了两个既有的跨平台阻断：CapabilityMatrix 门禁改为同时兼容 Windows PowerShell 5.1 和 PowerShell Core；可选 MCP stdio Host 的健康关停改为先让子进程从 stdin 收到 EOF，再由 `exec.Cmd.Wait` 回收 stdout，已记录的消息超限故障仍立即释放 stdout。后者是明确披露的窄范围运行时语义修复，只影响 MCP stdio 进程关停，不改变 Agent/Workspace/模块装配、权限、记忆、知识、预算、UNKNOWN 或上下文契约，也不把 MCP 成熟度提升为`生产就绪`。

GitHub 上传前仍需：

1. 清零公开树与文档门禁的剩余过渡红项，将 GitHub Actions 从可变 major tag 固定到审核过的 commit SHA，并原子接入三项永久门禁；
2. 在全新 clean staging、Git index、fresh checkout 和受信远端 CI 中完成发布证明。

除上述明确披露的 MCP stdio 健康关停修复外，剩余事项是公开打包和供应链收尾，不改变 P0/P1 架构与权限契约，也不应借机删除已经锁定的架构证据。

允许声明：P0 规范已冻结，P1 有界可信基础通过本轮审计和本地测试。

不得声明：面向正式部署的完整多 Agent 平台、开放插件生态或 MCP 扩展生态已`生产就绪`、CoreModel 完整执行权限已闭合、PostgreSQL 已支持，或用未加权能力条目数量推导项目完成百分比。
