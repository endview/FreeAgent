# FreeAgent

FreeAgent 是一个用 Go 编写的模块化 Agent 运行核心。它把身份、权限、任务状态、资源上限、外部效果和恢复留在可信内核中，把 Role/Persona、Knowledge/RAG、Memory、Skill、MCP、REMOTE/WASM Action Host、团队编排及渠道适配器设计为可选、可替换的装配单元。

> 金额边界：`P0_MONEY_BUDGET_REMOVED_FAC2_BASELINE`；当前运行路径已删除 BudgetPolicy、CostPolicy、PriceSnapshot、费用估算与金额对账，只保留 token usage、输出上限、deadline、权限、UNKNOWN 对账与 exact retry。Current Store 家族改为 FAC2（`github.com/endview/freeagent/current-store-v2`），新运行时只读拒绝 FAC1 旧库，不迁移也不修改。本文件中出现的 `CNY`、估算费用与 PriceSnapshot 字样一律是删除前 FAC1 时期的冻结历史验收证据，不描述当前运行语义；冻结记录见 [`P0_BASELINE_FREEZE`](P0_BASELINE_FREEZE.md)

项目已经形成并验收了可运行的 `S1_ACCEPTED_DEVELOPMENT_BASELINE`，完成了 S2.1
Context Compiler、S2 ModelProfile、本地 Action/Gateway、Operator 完全信任的本地 MCP
stdio Tool、默认关闭的 loopback Channel、Parent/Child + Composite 第一开发切片、默认关闭的
S3-A 公平 Scheduler、可选 S3-B Reviewer 审核门、W1 Conversation、W3 本地 RAG/Memory、
W4 Learning、W5-X1 有界 Decision/repair 与受控跨 Workspace 纵链，以及 W5-F1 协作稳定性。
当前增量状态是 `W6_5_SERVER_OWNED_MODULE_ARTIFACT_INGRESS_ACCEPTED_DEVELOPMENT_SLICE / W6_6_SERVER_OWNED_MODULE_UPGRADE_REVIEW_ACCEPTED_DEVELOPMENT_SLICE / P2_CONTROL_UI_I18N_ACCEPTED_DEVELOPMENT_SLICE / P3_SECOND_PROVIDER_ACCEPTED_DEVELOPMENT_SLICE / P4_MODULE_MANAGEMENT_UI_READ_ONLY_SLICE_ACCEPTED_DEVELOPMENT_SLICE`；P4 两批只读管理能力已完成，当前下一入口切换为 `P5_BETA_GATE`；
W2-R3 继续保持 `W2_R3_UNTRUSTED_MODULE_ISOLATION_ACCEPTED_DEVELOPMENT_SLICE`；
W2-D、W2-E2、W2-E3、W2-E4、W2-E5-A 与 W2-E5-B 仍分别保持各自窄 accepted 状态。前序六个
切片只覆盖本地显式装配 v1、受信 compiled `text.stats`、Workspace-owned loopback Channel、同一
内建 DeepSeek 构件内 flash/pro replacement、governed Knowledge 的 exact Model Require/窄
`knowledge.read` grant，以及固定 Document Insight Context+Action 双 Port 产品链。R1 只新增
默认关闭的 `action.provider/v1 + REMOTE/freeagent-action-http/v1` 原生 HTTPS Host；既有路线状态仍是
`W5_F1_COLLABORATION_STABILITY_ACCEPTED_DEVELOPMENT_SLICE / W5_COMPLETE_ACCEPTED_DEVELOPMENT_SLICE / W6_NEXT`。这些能力继续复用唯一
Assembly Compiler、Universal Loop、Current Store、Gateway、Usage/Attempt 账本和完整备份恢复。
E5-B 只验收固定 Document Insight 模块通过两个现有 PortPlan 被真实消费者使用；Document Insight
向 Context 与 Action PortPlan 各贡献一个 Binding，Context PortPlan 同时保留既有 `context.basic`。
Context→Action 仍是两次显式 Apply，不是任意模块的通用 multi-Port 或双 Binding 原子批量 Apply。
`W6_NEXT` 只是既有 W5 路线的历史 marker。R2 另行验收了默认关闭的 exact
`action.provider/v1 + WASM/freeagent-action-wasm/v1 + action-binding-config/v1` 本地 Action Host；
R3 在同一 Host 上验收了显式 exact runtime digest 授权的非 builtin 第三方纯计算模块、执行前二次
撤权检查与停机 Disable。它不是 OS/container 或生产恶意多租户隔离。W2-U0 又冻结了七份
authority-free 的 Ed25519、Source Policy、Discovery Snapshot、exact Candidate 与 Decision 纯合同；
该切片没有联网、Store schema、安装、Apply 或模块执行。W2-U1 又验收了默认关闭、显式
`LOCAL_DIRECTORY + DENY` 的候选观察路径。W2-U2 在唯一 Current Store 中闭合默认关闭的
Local/窄 HTTPS Index observation、不可变 Snapshot 与 Publisher Key current revocation。W2-U3 进一步
验收了默认关闭、仅限 `EXACT_VERSION_CHANGE` 的离线 Candidate/Review/Decision：Operator 必须显式
提供 exact current、scope、本地 unpacked target artifact，以及 signed Source 所需的 detached Signature；
系统不读取 Index `PackagePath`，不联网下载，也不 stage、Install、Activate、grant、Bind 或 Apply。
U3 当时的历史入口是 `W2_U3_UPGRADE_REVIEW_ACCEPTED_DEVELOPMENT_SLICE / W2_U4_OPERATOR_APPLY_NEXT`。
W2-U4 随后只为一个 exact APPROVE 的 Declarative Profile Context 原位替换接通既有 Apply/CAS；
Model、Channel、Action、`SINGLE`、shared-current、fanout 与自动升级仍失败关闭。W6-0 已冻结纯控制
API 合同；W6-1 又验收了默认关闭的双 listener、bootstrap/session、Modules list/detail 与 effect-free
`MODULE_DISABLE` Dry-run。W6-2 随后完成六项操作审计，并只验收稳定 ConfirmationStatement、
`MODULE_DISABLE` evaluation 与 32-byte、最多 2 分钟且不超过当前 session absolute expiry 的 process-local
proof registry。durable receipt Schema 历史原子又验收单一 append-only 表、exact resolver、NO_CHANGE
commit 与现存行 Backup semantic closure。当前 mutation-wiring 原子只在显式 `--enable-control` 下接通首个
窄 confirmation/mutate 两路；每次 HTTP 仍须当前 Origin/session/CSRF/TENANT Permit，durable hit 仅不依赖
旧 proof/session identity。NO_CHANGE/APPLIED receipt 持久化，APPLIED 与 Control/Catalog pointer publication
同事务，SQLite UNKNOWN 不可达。W6-3 又在同一默认关闭的 loopback Control listener 上接通嵌入式
React/TypeScript Web Shell 与 `GET /control/api/v1/overview`：只展示当前授权 Tenant/Workspace scope 内
有界的 Workspaces、Runs、UNKNOWN、Learning、Module Candidate 与 Usage，并以完整 scope query key、
强 ETag、服务端摘要复验和明确 loading/empty/error/stale/permission-denied 状态失败关闭。W6-4 随后以同一
authority 接通严格 Modules list/detail 与唯一 `MODULE_DISABLE` dry-run → short-lived confirmation → mutate
UI；proof/key 只驻内存，成功后只做已验证 refetch，不新增 operation、route、Store 或 writer。W6-5 再新增
默认关闭的可信本地 Operator CLI `module-artifact-ingress`：`source-root` / `artifact-root` 只作瞬时可信
CLI 输入，唯一可接纳对象是 Store-owned current Snapshot 中 unsigned `LOCAL_DIRECTORY + DENY` 的 exact
entry。它先以 no-replace 方式持久发布 content-addressed 惰性构件，再在唯一 Current Store 事务中写入不可变
Artifact 与 append-only Admission；不开放 HTTP/upload、调用方 package path、URL、signature 或 UI，也不
Install、Activate、Bind、grant、Review 或 execute。W6.6 随后从 Store-owned Admission
消费 server-owned inert Artifact，持久化 Review/Decision，并保持 exact retry、tenant/stale/tamper
失败关闭；它不自动 Install、Activate、Bind、grant、Apply 或 execute，也不调用 Provider。P2 Control UI 完整 i18n 已完成；P3 已接入默认关闭的受控智谱 `glm-4.5` Provider，并完成 production composition + Universal Loop 脱敏真实实验。P4 两批只读管理能力已收口，当前下一入口是
`P5_BETA_GATE`。其他 REMOTE/WASM 协议、热撤权、广义装配与 Beta 外围能力仍未完成。

P4 已完成两批只读管理能力收口：统一 application/control service、tenant/workspace-scoped Review/Decision/Artifact 与 UNKNOWN list/detail、Store verification、Backup constraints、Artifact Admission 查询、双语 `#upgrade-reviews` 管理展示、bounded query、projection digest、strong ETag 和 fail-closed 边界测试均已接入。UNKNOWN 只能查询原 Attempt 的证据和对账状态，禁止 resend、replay、换 Provider 或隐式 retry；Backup UI 不在线创建或恢复，Restore 仍是离线 staging + atomic publish；UI 不接受服务器路径、Secret、请求体、签名材料或 replay material。P4 不开放审核或 Decision mutation，也不提供 Install、Activate、Bind、Grant、Apply 或 Execute。
项目从未正式部署；所有 `accepted` 状态都只表示明确范围内的开发切片已验收，不等于可直接
用于生产、公开 Beta、首次部署或 SLA。`v0.1.1` 已作为早期 Developer Preview 发布，
但仍不代表生产支持或 `RELEASE_READY`。

当前源码树对应已发布的 `v0.1.1` Developer Preview。六平台归档、外层
`SHA256SUMS`、供应链文件和版本/revision 绑定已通过最终校验；Windows/Linux AMD64
原生离线安装及打包后 backup/verify/restore/continue 已复验，ARM64 为交叉构建，
Darwin 为 build-only。Release 详情和完整变更记录见
[`RELEASE_NOTES_v0.1.1`](RELEASE_NOTES_v0.1.1.md) 及
[GitHub Release](https://github.com/endview/FreeAgent/releases/tag/v0.1.1)。
旧的 `v0.1.0-dev.2` 候选材料仍保留为历史证据，不能作为当前版本状态。

W2-R2 WASM Host 尚未生产可用。

W1 当前状态为 `W1_COMPLETE_REAL_DEEPSEEK_50`。普通单 Agent Pure Chat 以每回合一个不可变
Run 和持久 Conversation revision/head 精确 CAS 推进，只恢复直接成功前驱的完整
`USER + ASSISTANT` pair。CLI、loopback HTTP、服务/正常进程重启、turn 25
backup→verify→restore、turn 25/50 exact retry 和本地确定性 50 轮均已闭合；真实官方 DeepSeek
验收也完成 50/50 个首次 Model Attempt，token 加权缓存命中率为 `90.3702992171%`，两次
exact retry 均未增加模型调用。Usage 只记录 provider 报告的 token；未知 reasoning token 仍明确
保持 UNKNOWN，不得补 0。W1 完成不表示 Conversation 已可与
Composite、Action 或 Channel 组合使用，也不表示生产多轮 SLA 或公开 Beta 已完成。

集中验收 `TestW1PureChatUnifiedZeroOptionalModulesAcceptanceV1` 进一步把原本分散的零访问证据
合并到一条生产纵链：Knowledge、Memory、静态 Skill、MCP/Action 和 Channel Provider 即使已经
安装、激活并进入 Catalog，只要未被选中，Pure Chat 仍只解析冻结 Model；请求无动态 Context/
Action，Memory、Channel、Learning、Transfer、Composite 和可选内容均零增量。

W3 当前状态为 `W3_COMPLETE_ACCEPTED_DEVELOPMENT_SLICE`。K1 已闭合确定性、无 I/O 的
Knowledge 路由；K2A/K2B 已闭合独立 `NOT_SELECTED` shortcut 和调用 Provider 前的全 Binding
配置、权限、作用域与资源上限预检；K3 已闭合严格的同 Conversation 最新 exact-question reuse，
复用时 Knowledge Provider 调用为零，任一 revision、ACL、TTL、Memory head 或证据变化都回退
fresh。M1 将 Memory 接入统一 Module Apply/Dry-run/Disable 与既有受治理更新路径；M2 复用直接
成功前驱中可验证的连续最旧 History 前缀摘要，不建立第二摘要或 Memory 写入路径。W4-L0 至
W4-L3 已在唯一 Current Store 闭合无权限 Learning 合同、Knowledge/静态 Skill Proposal、三轴
去重、独立 Reviewer 审核以及不可变 Version/Operator 交接。W4-L4 又验收了可选、默认关闭的
Learning Cycle：Schedule 创建后策略不可变且初始 disabled，未指定间隔时冻结为 86,400 秒；
只有显式 CAS 启停和显式 Tick 才能推进普通 model-only proposer Run。周期结果只能是
`PROPOSE` 或 `NO_CHANGE`，Store-only 补偿和只读报告不会调用模型或外部系统。W4 状态为
`W4_L4_LEARNING_CYCLE_ACCEPTED_DEVELOPMENT_SLICE / W4_COMPLETE_ACCEPTED_DEVELOPMENT_SLICE`；
自动 Review、Materialize、Apply、安装、激活、绑定、扩权、后台 worker、远程向量数据库和
生产部署仍未实现。

W5-X1 的历史验收状态为
`W5_X1_WORKSPACE_TRANSFER_ACCEPTED_DEVELOPMENT_SLICE`。它只在同一 Tenant 内，
以显式 Decision 和 root/target Workspace 双边授权建立受控纵链；每个 Run 仍只绑定一个
Workspace。v1 跨边界只传 `TASK_SUMMARY` 请求和 `SPECIALIST_RESULT` 返回，不传完整 History、
Memory、知识库正文、SecretRef 或任意 payload。`TASK_SUMMARY` 是对 Store-loaded `TASK_INPUT`
的确定性有界 extract，不是模型语义摘要，也不宣称检测任意自由文本 Secret。Decision family
预冻结 `2N+3` 个物理 Run，最多激活一次修复；未激活的 repair Run 持久存在但不创建 Attempt。
当前没有自动 Reviewer、无限返工或动态图。

2026-08-09 的 X1D 真实 DeepSeek canary 取得 4/4 HTTP 2xx，实际调用角色为 2 个 Specialist、
Reviewer 和 Root；REQUEST/RESULT transfer 均已验证。该 family 有 7 个物理 Run、4 个 Model
Attempt 和 3 个无 Attempt 的预冻结 repair Run；exact retry 新增 HTTP/Attempt 均为 0。Usage 为
input 3,443、cached input 768、uncached input 2,675、output 1,353，token 加权缓存命中率
`22.306128%`，reasoning 为 UNKNOWN，估算费用为 `0.00539636 CNY`，Provider reported 与
reconciled cost 均为 UNKNOWN。这些结果只证明上述窄开发纵链。

W5-F1 在不增加第二 Scheduler、Store 或 Loop 的前提下，补齐了协作稳定性证据。三个始终
runnable 的 Workspace 共完成 900 次 claim，在第 450 次后重开 Store，最终各得到 300 次；
每个 Workspace 的首次服务不晚于第 3 次 claim，最大服务间隔不超过 3。Decision approve、
单槽 repair 和 Workspace Transfer 都走同一个 Scheduler；dormant/skipped repair Run 保持
`Attempt=nil` 且从未被 claim。跨 Workspace Specialist 即使反向完成，结果仍按 frozen plan
归位，重开 Store 后 canonical bytes 与身份不变；取消覆盖完整跨 Workspace `2N+3` family，
取消后不再产生 Attempt、Transfer payload 或 envelope。

F1 还验证了 Transfer UNKNOWN 只以可靠 evidence 收口原 Attempt：SUCCEEDED 只生成一个
RESULT，FAILED 不生成 RESULT；stale revision、替换 Provider、替换 Attempt 或无 evidence
均拒绝。Decision+Transfer 已通过 backup→verify→restore→reopen；恢复时 PENDING 只转为同一
Attempt 的 UNKNOWN，Loop 语义重放为 0。双边 grant、Tenant、Workspace revision、方向、
kind/schema/ref/size/48 KiB ceiling 和撤权边界均有失败关闭负例；Pure Chat 与 ordinary
Composite 继续保持零 Transfer material。

独立的 F1 Test-0808 canary 为 4/4 HTTP 2xx，exact retry 新增 HTTP/Attempt 均为 0；Usage 为
input 3,149、cached input 768、uncached input 2,381、output 1,492，2/4 个请求命中缓存，token
加权缓存命中率 `24.388695%`，estimated cost `0.00538036 CNY`。reasoning token、Provider
reported cost 与 reconciled cost 均保持 UNKNOWN。该样本不改写上面的 X1D 历史数据，也不
构成生产或 SLA 结论。

工程门禁方面，Windows 全仓测试、`go vet`、`go mod verify` 与 gofmt 检查均已通过。WSL
首轮受影响七包 race 中六包通过，CurrentStore 只暴露一个 1 秒 TTL 墙钟测试竞态且没有 data
race；改用固定逻辑时间后，定向 race、单核慢调度和完整 CurrentStore race 均通过，后者以
exit 0 在 466.241 秒结束。这里不把首轮七包命令误记为整体 exit 0。

S3-C 真实 Pilot 的历史裁决仍保持 `3 COMPLETE + 1 PARTIAL`：第四个 Reviewer-on 矩阵位置因
4 个原 Attempt 进入 `MODEL_UNKNOWN` 而停止，Reviewer 本身尚未发起；这些 UNKNOWN 保留原
Attempt 且禁止语义重放，不能用 W1/W3 的后续回归改写。MCP 首片仍只允许 Operator 明确授权且
完全信任精确 ArtifactDigest 的 `LOCAL_PROCESS + stdio + Tool-only` Adapter；空环境和受管进程树
不是 OS 文件系统或网络 sandbox。准确状态与证据见
[`CURRENT_CAPABILITIES`](CURRENT_CAPABILITIES.md)、
[`CORE_RUNTIME_V1`](specs/CORE_RUNTIME_V1.md)、
[`CURRENT_STORE_V1`](specs/CURRENT_STORE_V1.md)、
[`CONTROL_API_V1`](specs/CONTROL_API_V1.md) 和
[`CUTOVER_ACCEPTANCE`](CUTOVER_ACCEPTANCE.md)。

## 目标架构与产品方向

以下条目描述 FreeAgent 的最终产品方向。当前可用范围以当前能力清单、三份长期编码规格和
最新阶段验收为准；W6 及以后能力不能仅凭本节推断为已实现。

- 默认 `pure_chat` Profile 不绑定 Role/Persona、Knowledge/RAG、Memory、Skill、MCP、Tool 或扩展 SecretRef，只做普通聊天。
- Workspace、Agent 和运行 Profile 分开版本化；同一个 Agent 可进入不同 Workspace，而不复制一整套领域知识。
- 复合 Agent、领域 Specialist 和独立 Reviewer 可以并行协作，同时保留各自侧重点和权重。
- 共享知识库按 Workspace、集合和数据权限检索，Agent 本身保持轻量。
- 上下文达到 85% 时触发一次压缩判定，有合格旧历史才持久化摘要；达到 100% 时按顺序移除满足目标所需的最小完整旧前缀，而不是清空全部历史。
- 模型、Tool 和 Channel 的外部效果进入持久账本；结果不确定时进入 `UNKNOWN`，禁止语义重放。
- Token、推理 Token、缓存命中与 `usage_status` 使用明确字段记录，可用于人工控制平面的用量比较。
- 多 Workspace 调度、恢复、备份、Channel cursor 和学习补偿均有持久化边界。

## 架构边界

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
       /          |          \
Context Port   Model Port   Action Describe/Prepare
       \          |          /
                  v
 Current Store / Attempt / Usage / History
                  |
                  v
       Gateway → private executor
```

通过 FreeAgent Core/Host 协议调用的普通模块不能绕过 Workspace 权限、资源上限、Gateway、
外部效果账本或最终持久化。当前 Operator-trusted `LOCAL_PROCESS` 是明确的宿主级完全
信任逃生口，并不提供 Workspace、文件系统、网络或 Secret 隔离；不可信进程外模块在
OS sandbox 工作包完成前不得启用。安装、升级和发现结果只影响新 Run；已经冻结的 Run
不允许静默漂移到新版本。

当前 Current Store 是首次公开发布前的 Schema Draft；S1 纵链、S2.1 Context
Compiler、S2 ModelProfile、Action/Gateway 和本地 MCP stdio Tool、默认关闭 Channel、
Composite 第一开发切片、默认关闭的 S3-A 公平 Scheduler、可选 S3-B Reviewer 审核门、
W1 真实 DeepSeek 50 轮 Conversation、W3 本地 RAG/Memory 实用化开发纵链，以及 W5-X1
有界 Decision/repair 与受控跨 Workspace 纵链已经验收。W4-L1A 至 L3 已在第 22 张表闭合
Proposal、Review 与不可变 Version 投影；W4-L4 新增 `learning_cycle_schedules` 和
`learning_cycle_tasks`。W5-X1 没有新增表，只为现有 Content Store 增加两个 Transfer
ContentKind；W2-R1/R2 也没有新增表，只把 `REMOTE` 与 `WASM` 纳入既有 Activation
execution-class 约束。
W2-U2 为显式来源 observation 增加 5 张 typed fact 表；W2-U3 再增加全局不可变 Candidate、
Tenant-scoped Review 与绑定 exact Review 的终态 Decision 三张表。W6-2 receipt Schema 只增加
`control_operation_receipts`。W6-3 为可验证只读 Overview observation closure 增加 8 张
`run_observation_*` / `overview_*` 表；W6-5 再增加 `module_artifacts` 与 append-only
`module_artifact_admissions`。P0 金额退场后的当前 FAC2 Store 在 W6.6 migration 后为 UserVersion 2、
42 张 ordinary tables、26 个 explicit indexes 与 64 个 triggers，Store fingerprint 为
`d5d876f327dc29dc6f4a10476652641172ab8e1f0451a8714fc450f58733541e`；`0001` bootstrap 为 149,239 bytes /
SHA-256 `dbc3e724a1f7c030677c84a77a317f69ef2fe246985cc749559a9f3dd5a6dc5a`，`0002` migration 为 7,173 bytes /
SHA-256 `3091a49ebcf724f573f91cc0fd22a7c58ebb52fa9d7ed552e32b6526ebeca3cb`。删除前的 FAC1 43 表
identity 为 `47981b9bf147ec81c6e98382f281db0d5395187a1f090f7ce183c116fba82a0d`、150,301 bytes /
`6def433a59fa8f4876894572f1610cae499abc5b389ab5930ea697055419ca86`，只作为冻结历史保留。
W6-3/W6-4 的历史 41 表 identity 为
`87d63a2e468ee27ee2c0e1918a92e580532825bde85dbb872b9c795bf577f2c1`、143,588 bytes /
`5bd9743e988b82750f785c015f07c11fbcf6025da70148405aec11106b4efb22`。W6-2 receipt Schema 的历史
33 表 identity 为 `51be9081a815e6379380b93e745db03d33ca2f358962f85ace63bce2534dcc10`、
67,998 bytes / `8feea38beb505ab4f371afe32eecd877744062bd149d0d24982839c5450fc952`。W2-U3 至 W6-2 confirmation 的
历史 32 表 identity 为 `37258c1939308be83f21a109e59a19e32946734d1c53b1b262f876dc36e47dbd`、
57,652 bytes / `e4f1047eb527b65ab050d444062f3d286cc2ad87d56e60b1cd7f4e96df87652d`。W2-U2 的历史 29 表
identity 为 `6d2bded477e7b1d2c755bf47f5b41f63f1b2f7b568fd72496fc43fbceac3a5f1`、49,972 bytes /
`d2bcc27bff2e17a058165f7c544b0c97cd1c99efca3264ab401631477611263e`。W2-R2 的旧 24 表
identity 为 `7d2e0850a0253a630d5e2264c720b10fbac3ad0fdb6f9e53fcd722c2dd2615a8`、44,257 bytes /
`2520299390885b4c23df85d2ff217629f6a458c368735e5bdc0df2eca37b8f2f`。W2-R1 的
24 表 identity `dcad8f8837ecc5832f444acbc2680a2c2debff5161319710f60e7f649e4aede5`、
44,237 bytes / `fd7270130b5b13e5dfe9e3bb0a3bc934d46eb0c3b4a0647b1a3fddd81773e665`，以及 W5-F1/W5-X1 的
旧 24 表 identity `98d658e68907f77516ce0366a7b08bfa3851600326850590ed79584f581fa588`、
44,215 bytes / `0a6701405e471e1ee7f41ea89ed62b50e5a4723e71b320e50f073cdd3e6b727a`，以及 W4-L4 的原 identity
`9941c957b0e6a1a1e1770b38cd06605025d12df30067d1271fe8f7b30f2f0518`、44,138 bytes、
`4cf260d368fb7b69e0a86dad99ff593bea216e9d0da341f607d000999e076be4` 及更早摘要只表示对应
历史切片。
此前 W1/W2/S2/S3 文档中的 19/20/21 表及其摘要继续只表示对应历史快照。
旧设计证据已统一降级，索引见[旧架构规格](architecture/specs/README.md)；
它们不构成第二套运行协议。

产品目标与功能架构见[产品需求文档](PRD.md)。

## 五分钟本地体验

要求：

- Go 1.26.5
- PowerShell、bash 或其他能设置环境变量并发送 HTTP 请求的终端

先创建一个新的 FAC1 Current Store 和内容寻址 artifact root。目标必须不存在：

```powershell
New-Item -ItemType Directory -Force data | Out-Null

go run ./cmd/freeagent init `
  --db data/current.sqlite `
  --artifact-root data/artifacts `
  --seed examples/current-v1.bootstrap.seed.json
```

直接进行一次确定性 Pure Chat。重复使用相同 `request-id`、消息和 deadline 会返回
原 Run，不会重新调用模型：

```powershell
$deadline = (Get-Date).ToUniversalTime().AddMinutes(10).ToString("o")

go run ./cmd/freeagent chat `
  --db data/current.sqlite `
  --artifact-root data/artifacts `
  --message "hello" `
  --request-id readme-demo-1 `
  --deadline $deadline
```

要继续多轮 Pure Chat，先显式创建一个固定身份与作用域的 Conversation；每个回合都必须
提交调用方刚刚观察到的 revision，第二回合起还必须提交精确 head Run：

```powershell
$conversation = "readme-conversation-1"

go run ./cmd/freeagent conversation-create `
  --db data/current.sqlite `
  --conversation $conversation

$turn1 = go run ./cmd/freeagent chat `
  --db data/current.sqlite `
  --artifact-root data/artifacts `
  --conversation $conversation `
  --conversation-revision 0 `
  --message "remember: the project is written in Go" `
  --request-id readme-conversation-turn-1 `
  --deadline $deadline | ConvertFrom-Json

$turn2 = go run ./cmd/freeagent chat `
  --db data/current.sqlite `
  --artifact-root data/artifacts `
  --conversation $conversation `
  --conversation-revision $turn1.conversation_revision `
  --conversation-head-run $turn1.run_id `
  --message "which language did I mention?" `
  --request-id readme-conversation-turn-2 `
  --deadline $deadline | ConvertFrom-Json

go run ./cmd/freeagent conversation-get `
  --db data/current.sqlite `
  --conversation $conversation
```

重复同一精确请求会返回原 Run；陈旧 revision/head 会冲突关闭，不能产生分叉。上下文只
沿成功 predecessor 链恢复完整的用户/助手 pair，摘要和 100% 有序移除也不能拆开 pair。
Conversation 表、Run 投影、head/revision 及其历史闭包都进入完整 backup/verify/restore；
恢复后可以从原 head 继续下一回合，而备份过程本身不会调用模型。

要体验 Composite，请复制默认 seed，并在顶层加入显式的 `composite_agents`。首片要求
2..8 个 Child、唯一 `slot_id`，且权重合计 10000；下面的最小配置复用示例中的
`assistant/pure-chat`，实际部署可为每个 slot 指定不同 Agent/Profile：

```json
"composite_agents": [{
  "schema_version": "composite-agent/v1",
  "agent_id": "assistant",
  "coordinator_profile_id": "pure-chat",
  "members": [
    {"slot_id":"architecture","agent_id":"assistant","profile_id":"pure-chat","focus_id":"structure","weight_basis_points":6000},
    {"slot_id":"implementation","agent_id":"assistant","profile_id":"pure-chat","focus_id":"details","weight_basis_points":4000}
  ]
}]
```

用该 seed 初始化全新的 Store 后，显式增加 `--composite`：

```powershell
go run ./cmd/freeagent chat `
  --composite `
  --db data/composite.sqlite `
  --artifact-root data/composite-artifacts `
  --message "design and implement a small service" `
  --request-id readme-composite-1 `
  --deadline $deadline
```

Child 会有界并行推进，输出仍按冻结 slot 顺序合并。该入口当前仅支持同 Workspace、
depth 1、固定 `ALL_REQUIRED`；该示例没有启用可选公平 Scheduler，Reviewer、Learning 和
跨 Workspace 内容传递仍未启用。

普通单 Agent Pure Chat 现在也可以显式选择受控 DeepSeek Provider。该入口固定官方
Chat Completions URL，只接受编译期 allowlist 中的精确 artifact、adapter、model alias 和
build ID，不是任意 OpenAI-compatible endpoint；默认关闭，密钥只在实际 dispatch 时由
运行进程从环境变量读取，不写入 seed、命令参数、Store、日志或报告。先初始化独立 Store：

```powershell
go run ./cmd/freeagent init `
  --db data/deepseek/current.sqlite `
  --artifact-root data/deepseek/artifacts `
  --seed examples/current-v1.deepseek.bootstrap.seed.json

if ([string]::IsNullOrWhiteSpace($env:FREEAGENT_DEEPSEEK_API_KEY)) {
  throw "set FREEAGENT_DEEPSEEK_API_KEY in this shell first"
}

$deadline = (Get-Date).ToUniversalTime().AddMinutes(10).ToString("o")
go run ./cmd/freeagent chat `
  --db data/deepseek/current.sqlite `
  --artifact-root data/deepseek/artifacts `
  --profile deepseek-chat `
  --message "hello" `
  --request-id deepseek-chat-1 `
  --deadline $deadline `
  --enable-deepseek
```

`serve` 使用同一组 `--enable-deepseek` / `--deepseek-api-key-env` runtime 开关；关闭时不会
解析凭据或构造 DeepSeek Adapter。该 Provider 可以被普通单 Agent Conversation 路径显式
选择。W1 已通过该受控入口完成真实官方 DeepSeek 50 轮、turn 25 备份恢复和 turn 25/50
移除运行时 Secret 后的 exact retry；50/50 个首次 Attempt 成功，精确重入未增加模型调用。
这是已验收开发纵链，不是生产部署、公开 Beta 或任意 OpenAI-compatible endpoint 支持。
该 seed 也没有装入可选 Role/RAG/Memory/Skill/MCP/Tool。

W4-L2 提供一个显式、opt-in 的真实 Learning Review 验证 runner。它使用临时 Store 与
artifact，先执行真实 proposer，再提交静态 Skill Proposal，最后用不同 Agent/Profile 的
普通 Reviewer Run 调用 DeepSeek。通过条件要求 Reviewer Attempt 为 `SUCCEEDED`、Proposal
终态为 `APPROVED` 或 `REJECTED`、Usage 含 input/output token，并验证 exact retry 不创建第二
Run/Attempt。runner 不接受命令行 key，不把凭据写入 Store、日志或报告：

```powershell
if ([string]::IsNullOrWhiteSpace($env:FREEAGENT_DEEPSEEK_API_KEY)) {
  throw "set FREEAGENT_DEEPSEEK_API_KEY in this shell first"
}

& .\scripts\Invoke-W4L2LiveLearningReview.ps1
```

该脚本只运行 `TestW4L2LiveDeepSeekLearningReview`，不会启用自动 Reviewer、发布、安装、
激活或周期更新。2026-08-09 的验收运行取得 PASS（4.08s）：Proposal 为 `APPROVED` revision 2，
Reviewer Attempt 为 `SUCCEEDED`，Usage 为 `PROVIDER_REPORTED`（input 1151、cached 0、uncached
1151、output 227、reasoning UNKNOWN），估算费用为 0.001605 CNY；exact retry 未创建新
Run/Attempt 或重放模型。当前验收状态仍以 [`CUTOVER_ACCEPTANCE`](CUTOVER_ACCEPTANCE.md) 为准。

W4-L3 对已经持久化为 `APPROVED/2` 或对账后 `APPROVED/3` 的 Proposal 提供显式、停机态的
不可变 Version 与 Operator 交接入口：

```powershell
go run ./cmd/freeagent learning-materialize `
  --db .local/current.sqlite `
  --artifact-root .local/artifacts `
  --tenant default `
  --proposal <proposal-sha256> `
  --proposal-revision 2 `
  --output-artifact .local/handoff/approved-module
```

命令只在同一 `learning_proposals` 行保存确定性 Version 投影，并把可重建的 exact 两文件模块包
导出到与数据库和活动 artifact root 分离的 Operator 目录。Knowledge 包固定为
`content/source.json`，静态 Skill 包固定为 `content/context.json`；两者都只提供
`context.provide/v1`。已有交接目录只有在逐字节、digest 和 size 完全相同时才作为精确重入；
符号链接、硬链接、特殊文件、超限文件和额外文件均拒绝。命令不会生成 Apply plan，也不会
Install、Activate、Bind、修改 Control/Catalog、获得 Trust/Authority/Secret 或调用模型/网络。
Operator 必须根据当前 Profile、Instance、exact Port、port binding index、
`expected_runtime_request`、Config、Authority、FailurePolicy 和 pointer revision 另行编写 exact
`module-apply-plan/v1`，先运行 `module-dry-run`，再决定是否显式 Apply。

专属验收 `TestW4ApprovedLearningMaterializedSkillApplyIsConsumedOnlyByNewRun` 已闭合实际交接：
成功 proposer → 独立 Reviewer APPROVE → inert materialize → stale pointer 零部分发布 → Operator
exact Apply → 新 Run 消费审批后的静态 Skill。旧 Run 始终保持原快照；测试复验 exact Version/
Artifact、deny-all Authority 和本地 `DECLARATIVE` Trust。该证据证明显式 Apply 可用，不授权自动
Review、Materialize、Apply 或扩权。

W4-L4 在同一 Runtime/Store/Loop 上增加显式 `learning-cycle-schedule-create/get/enable/disable`、
`learning-cycle-tick`、`learning-cycle-reconcile` 和 `learning-cycle-report`。Schedule 的 canonical
策略创建后不可变、初始 disabled，省略间隔时固定为 86,400 秒；启停必须携带 exact revision。
Store open、Restore、Pure Chat、启动恢复和时间流逝本身都不会 Tick。到期前 Tick 零写；错过多个
周期时只合并为最新到期的确定性逻辑窗口，不突发补跑；同一 Schedule 同时至多一个
`PENDING/RUN_ADMITTED` Task。

每个实际窗口只允许一个普通 standalone、single-member、model-only proposer Run/Attempt/Usage，
模型结果仅为 `PROPOSE` 或 `NO_CHANGE`。`PROPOSE` 也只能提交同 kind 的 Knowledge/静态 Skill
Proposal；source/content/target 任一轴已经占用（包括 `REJECTED` 来源）都不会反复提交。
`UNKNOWN` 只能用可靠 evidence 收口同一 Attempt，禁止新 Run/Attempt、换 Provider 或语义重放。
`learning-cycle-reconcile` 只补齐既有 Store Task，不调用模型、Provider、Gateway、MCP、Channel、
网络或 Secret；`learning-cycle-report` 只读生成确定性 canonical 投影，不持久化或自动发送。
Backup/Restore 会保存并语义复验 Schedule、Task、Run、Attempt、Result、Proposal 和 UNKNOWN
崩溃窗口。这里没有第二个 Runtime/Store/Loop/Gateway、后台 worker、Queue、自动 Review、
Materialize、Install、Activate、Bind、Apply、Catalog 变更或扩权。

2026-08-09 的 opt-in 真实 DeepSeek 验证取得 `PROPOSAL_SUBMITTED`：恰好一个 Run/Attempt，Usage 为
`PROVIDER_REPORTED`（input 614、output 90），估算费用为 0.000794 CNY，同一窗口 exact retry
没有新增模型调用。该结果只证明上述窄开发纵链，不表示后台自主更新、生产部署或公开 Beta。

S3-C 真实效果验证继续使用四份 `s3c-deepseek-v4-*.bootstrap.seed.json` 之一初始化全新
Store，并复用同一受控 Adapter 与 runtime Secret 规则：

```powershell
if ([string]::IsNullOrWhiteSpace($env:FREEAGENT_DEEPSEEK_API_KEY)) {
  throw "set FREEAGENT_DEEPSEEK_API_KEY in this shell first"
}

go run ./cmd/freeagent s3-eval `
  --db data/s3c-flash-reviewer-on.sqlite `
  --artifact-root data/s3c-flash-reviewer-on-artifacts `
  --scenario examples/s3c-architecture-clean.scenario.json `
  --repetitions 1 `
  --enable-fair-scheduler `
  --scheduler-global-workers 2 `
  --scheduler-workspace-workers 1 `
  --scheduler-family-workers 1 `
  --enable-deepseek
```

命令把三个 Workspace 同时提交给同一 production composition，并从同一 Store 只读生成
版本化 JSON：包括 Child/Reviewer/Root 的规范化结果、结构化 Reviewer verdict、Attempt、
token、缓存、推理 token、公平顺序、延迟和逐 Attempt 派生成本。报告包含模型回答，按测试
证据保护；它不包含 API key、Authorization、Provider 原始响应或思维链。任一 UNKNOWN、
非终态或证据不完整都会先输出部分报告再停止，后续 repetition 不会隐式重放。

上面的命令适合开发调试。正式采集 S3-C 真实实验 cell 时，优先使用
[`scripts/Invoke-S3CRealCell.ps1`](../scripts/Invoke-S3CRealCell.ps1)；它冻结二进制、seed、
scenario 与引用 artifact，只执行一次 `s3-eval`，随后备份并验证同一 Current Store，且不会
自动重试或覆盖已有 cell。完整参数、证据布局和冻结的对照矩阵见
[`docs/S3C_REAL_EXPERIMENT.md`](S3C_REAL_EXPERIMENT.md)。

若模型调用已经完成、但外层证据捕获在提交报告前失效，禁止重跑模型。Operator 可在确认
writer 已停止后使用 `s3-store-audit` 对保留的单文件 Current Store 做只读聚合取证；该命令
不输出 ID、digest、请求/回答正文、header 或 raw receipt，也不能伪装成原 S3 报告。具体
边界与命令见上述实验文档的“已执行模型调用后的无重放恢复”。需要保全完整 backup 证据
时，使用 [`scripts/Invoke-S3CRecoverCell.ps1`](../scripts/Invoke-S3CRecoverCell.ps1) 在原 cell
之外创建只读 clone 并运行 `backup`/`backup-verify`；它不会调用 `init`、`s3-eval` 或模型，
也不会把原失败 cell 或恢复记录改写为原 S3 报告。

已提交报告的 cell 还可使用 `s3-cell-audit --cell <cell>`。当前
`freeagent.s3-cell-audit/v2` 继续严格读取既有 eval-report/v1 与 exit/v1，重新验证
report/exit、两次只读校验归档 bundle 并重算公平性；同时从 report 独立重算固定
`CHILD/REVIEWER/ROOT` 三桶 role/cache，再通过只读 Store 审计交叉核对已验证 bundle 内的
`database.sqlite`。只有 `store_audit_verified` 与 `role_cache_cross_checked` 都成立时，
COMPLETE cell 才可能 PASS。命令只输出 Reviewer 阶段、token/cache/cost 覆盖率、已知小计和
延迟聚合，不打开 Runtime 或可写 Store，也不访问 Provider；PARTIAL 仍返回非零，绝不会被
审计结果升级为 `COMPLETE`。

首个真实 Flash cell 的模型阶段已经完成：12 个 Attempt 全部 SUCCEEDED，input/cached/
uncached/output 为 15,968/0/15,968/17,122，reasoning 与三类 Store 权威费用保持 UNKNOWN，
按冻结价格派生成本为 CNY 0.050212。旧捕获规则把模型回答中的普通 `Authorization` 术语
误判为泄漏；原 cell 未重放，已通过 `s3-store-audit` 和外置 recovery 完成无正文聚合取证及
backup/verify。由于原 S3 报告及 wall facts 无法无损恢复，该 cell 不进入效果配对数据集；
随后曾使用全新 `pilot2-*` 身份重新开始矩阵；当前已按实验文档 8.2 的裁决停止。

Pilot v2 的前三格均为 `COMPLETE`，共 36 次 Flash 成功调用；token 加权缓存命中率为
7.292%，派生成本 CNY 0.14555112。Scheduler off 与 2/1/1 的对应 cell 分别观察到最长连续
服务 3/1、最晚首次服务序号 7/3、最慢 family wall elapsed 42.905/96.970 秒；Scheduler
状态、并发上限、Provider 状态和顺序未解耦，因此不作因果归因。
第四格在 Reviewer 真正调用前形成 5 次成功与 4 个 `MODEL_UNKNOWN`；四个 UNKNOWN 均保留
原 Attempt 且未重放，剩余矩阵已经停止。后续纠偏切片只为未来调用增加三个固定、脱敏的
本地观察位置码；历史四条记录保持通用 `MODEL_UNKNOWN`，不能据此反推 Provider 是否收到
或执行请求。`s3-store-audit/v2` 对前三格 archive 的只读重算还确认：27 个 CHILD Attempt
合计 cache hit 56.721%，9 个 ROOT Attempt 为 0%，Reviewer 未调用；四个源文件审计期间均
保持不变。随后 `s3-cell-audit/v2` 对相同四个 archive 完成 report-derived 与
Store-derived role/cache 交叉核验：三个 COMPLETE 继续 PASS，PARTIAL 继续 FAIL，但其
`store_audit_verified` 与 `role_cache_cross_checked` 均为 true。完整证据与停止裁决见实验
文档 8.2。

也可以启动只监听 loopback 的同步 HTTP 服务：

```powershell
go run ./cmd/freeagent serve `
  --db data/current.sqlite `
  --artifact-root data/artifacts `
  --listen 127.0.0.1:8080
```

Control API 默认关闭；不传 `--enable-control` 时不会创建 Control listener、bootstrap/session 或读取
Control Store 视图。显式启用 W6-1 的窄本地面时，仍由同一个 `serve` 进程和唯一 Store 提供 Chat 与
Control 两个 listener，Control 固定绑定 OS 分配端口的 `tcp4 127.0.0.1:0`：

```powershell
go run ./cmd/freeagent serve `
  --db data/current.sqlite `
  --artifact-root data/artifacts `
  --listen 127.0.0.1:8080 `
  --enable-control `
  --control-handoff-path data/control-runtime/control-bootstrap.json
```

`--control-handoff-path` 必须是预先准备并可验证为 owner-only 的 runtime 目录中的未占用 leaf；进程也会
拒绝以 Unix root 或 Windows elevated/admin token 常驻。实际 Control origin 与一次性 5 分钟 bootstrap
capability 只写入该独占 handoff，不进入 stdout、日志、URL 或 Store；交换后得到 process-local session。
由于 host-only Cookie 不绑定 loopback 端口，除 bootstrap exchange 外，Modules GET 与非安全方法都必须
同时携带 exact session 和该 session 绑定的 CSRF header；GET 缺失或错误 CSRF 返回 401。
当前 HTTP 面只有 Modules list/detail 与 effect-free `MODULE_DISABLE` Dry-run；Dry-run 返回的
`DRY_RUN` receipt 也只在本次响应中存在，不是 durable receipt，不会调用既有离线 Apply/Disable 或产生
任何 Control mutation。

另开一个终端提交同步请求：

```powershell
$conversation = "readme-http-conversation-1"
$conversationBody = @{
  conversation_id = $conversation
  tenant = "default"
  principal = "local-operator"
  workspace = "local-chat"
  agent = "assistant"
  profile = "pure-chat"
} | ConvertTo-Json

$created = Invoke-WebRequest `
  -Method Post `
  -Uri http://127.0.0.1:8080/v1/conversations `
  -ContentType application/json `
  -Body $conversationBody
$created.StatusCode # 首次为 201；相同正文精确重试为 200

$deadline = (Get-Date).ToUniversalTime().AddMinutes(10).ToString("o")
$body = @{
  tenant = "default"
  principal = "local-operator"
  workspace = "local-chat"
  agent = "assistant"
  profile = "pure-chat"
  message = "hello over HTTP"
  request_id = "readme-http-1"
  deadline = $deadline
  conversation_id = $conversation
  conversation_revision = 0
} | ConvertTo-Json

Invoke-RestMethod `
  -Method Post `
  -Uri http://127.0.0.1:8080/v1/chat `
  -ContentType application/json `
  -Body $body
```

`POST /v1/conversations` 只创建 revision 0/head 为空的固定 scope Conversation，不创建 Run、
不调用模型；相同 ID 和完全相同 scope 的重试返回同一记录，任何 scope 漂移均返回冲突。
`POST /v1/chat` 携带 `conversation_id`、数值型 `conversation_revision`，并从第二回合起携带
`conversation_head_run_id`；响应返回新的 `conversation_id` 与 `conversation_revision`。
服务不会替调用方猜测或刷新陈旧 head。
开发聊天接口只能绑定 loopback 地址。它不是公开网络 API，也不应通过反向代理暴露。

## 当前开发边界

模块作者现在可以用只读的 `module-verify` 对裸开发包执行 canonical Manifest、路径安全、
文件型 entrypoint、单一 ArtifactDigest 与 covered size 验证。没有提供任何 supply flag 时继续走
legacy 路径，既有成功 JSON 与失败边界逐字节兼容：

```powershell
go run ./cmd/freeagent module-verify `
  --artifact sdk/moduleapi/testdata/compat/v1/declarative-role
```

成功仅表示包格式有效；命令不打开 Store，不 Install/Activate/Bind，不加载或执行模块，
也不访问模型、网络和 Secret。

任一 `--source-*`、`--publisher-*`、`--signature*` 或
`--revoked-publisher-key-id` 参数都会显式选择 governed observation；参数不完整时失败关闭。U1
只接受 Operator 指定的绝对本地目录和 exact `LOCAL_DIRECTORY + DENY` Source Policy。Policy、
Publisher Key 与 detached Ed25519 Signature 的 canonical 文件及各自 exact content ID 都位于 artifact
之外；是否必须签名由 Policy 决定，Manifest 和签名本身不能授予 Trust、Authority、Effect 或运行资格。
首次扫描和最终复验都受 Policy 的 `max_package_bytes` 限制，Module ID 只按点分段前缀匹配；本次调用的
重复型撤销 ID 构成不可变 deny snapshot，但不是持久或全局 current revocation。

governed 成功仍输出同一个 `freeagent.module-package-verification/v1`，不创建 reservation、grant、
staging、Store fact 或 Apply authority。U1 明确拒绝 HTTPS policy，且该路径不创建网络 transport；Windows
还拒绝 UNC/device namespace、ADS 与 symlink/reparse 路径，但不声称能识别映射为普通盘符的网络盘。
现有手工 exact-grant `module-apply` 不依赖此观察结果，行为保持不变。统一开发合同及命令参数见
[`MODULE_DEVELOPMENT_V1`](MODULE_DEVELOPMENT_V1.md)。

W2-U2 新增三个显式、默认关闭的 observation-only 命令：

```powershell
go run ./cmd/freeagent module-source-register `
  --enable-module-discovery `
  --db .local/current.sqlite `
  --policy .local/source-policy.json `
  --policy-id <exact-lowercase-sha256> `
  --expected-policy-revision 0

go run ./cmd/freeagent module-source-refresh `
  --enable-module-discovery `
  --db .local/current.sqlite `
  --source-id <source-id> `
  --local-directory <absolute-source-root>

go run ./cmd/freeagent module-publisher-key-revoke `
  --enable-module-discovery `
  --db .local/current.sqlite `
  --key-id <exact-lowercase-sha256> `
  --expected-key-revision 1
```

Local refresh 只读取固定 `<absolute-source-root>/index.json`。HTTPS refresh 还必须同时提供
`--enable-https-module-discovery`、`--https-index-url <exact-canonical-url>` 和至少一个重复型
`--allow-https-source-origin <exact-canonical-origin>`；它不使用 proxy、redirect、retry、HTTP/2、
keep-alive、cookie、凭据或 special-use 地址。Store 在 Source I/O 前后以 exact revision/CAS 重验
Policy、Key 与全局不可逆 revocation，然后只冻结 Policy/Index/Snapshot canonical closure 和安全
`entry_count` 输出。Source Provider 不能写 Store。

这三个命令不下载 package、不验证 Index entry 的 detached Signature、不生成 Candidate、Decision、
reservation、stage、Installation、Activation、Binding 或 Apply plan。Backup/Verify/Restore 也不会
访问 Source、网络或 package。W2-U2 的历史 Store 因五张 discovery fact 表扩为 29 表；Pure Chat 仍
不会扫描 Source 或触发 refresh。

W2-U3 历史切片新增两个同样默认关闭、只适用于可信本地 Operator 的离线命令：

```text
module-upgrade-review --enable-module-upgrade-review
  --db <closed-store> --tenant <tenant> --expected-pointer-revision <revision>
  --source-id <source> --snapshot-id <snapshot>
  --current-instance <instance> --current-activation <activation>
  --current-module <module> --current-version <opaque-version>
  --current-artifact-digest <sha256>
  --target-module <same-module> --target-version <different-opaque-version>
  --target-artifact-digest <sha256> --target-instance <new-instance>
  --port <port> --port-version <version> --port-binding-index <ordinal>
  --target-kind <PROFILE|WORKSPACE_CHANNEL_ENDPOINT> <exact-scope-flags>
  --artifact-directory <local-unpacked-artifact> [--signature <detached-signature>]
  [--operator-requested-rollback]

module-upgrade-decide --enable-module-upgrade-review
  --db <closed-store> --tenant <tenant> --review-id <sha256> --candidate-id <sha256>
  --decision <APPROVE|REJECT> --operator-principal <principal> --reason-file <local-file>
  [--confirm-tenant-wide-reject]
```

Review 只接受 `EXACT_VERSION_CHANGE`，从 Store 重建 exact
SourcePolicy→Index→Snapshot→entry、Binding→Catalog→Activation→Installation 与 PublishedBasis；
然后对显式本地 artifact 做两次一致性验证。目标 canonical Manifest 作为 content-addressed evidence
写入既有 `content_records`，Review 只输出/保存安全摘要、引用、差异、全部已发布 Binding impacts、
digest-only grant requirements，以及 `WOULD_APPLY | CONFLICT | UNSUPPORTED`。Version 是 opaque string；
Core 不判断 newer/downgrade，回滚意图只能由 Operator 显式标记。

`APPROVE` 只允许仍与 current basis 一致的 `WOULD_APPLY` Review，且只记录 U4 输入，不授予权限。
`REJECT` 必须显式携带 `--confirm-tenant-wide-reject`，并以 `{tenant_id, review_key}` 抑制同 Tenant 所有
scope 的后续重复候选；它不会影响其他 Tenant。两种 Decision 都绑定 exact `review_id`。U3 没有
package download、stage、Install、Activate、grant、Bind、Apply、Host 或外部效果；U3 收口时的历史 32 表
身份见上文；后来 W6-2 的历史 33 表身份不反向改写该证据。
U4 的入口门已经把原 CLI 私有的 9 条 generic/Model policy 与 2 条 Document Insight reserved exact selector 提取到唯一
`internal/modulehandler` 共享实现；Review 与既有 Apply 共同调用它，未复制第二张 handler 表。
该 Handler-gate 的 `W2_U4_OPERATOR_APPLY_NEXT` 是明确的历史 NEXT marker。

W2-U4 新增一个默认关闭、只适用于可信本地 Operator 的窄执行命令：

```text
module-upgrade-apply --enable-module-upgrade-apply
  --db <closed-store> --artifact-root <artifact-root> --source-root <local-source-root>
  --tenant <tenant> --review-id <sha256> --decision-id <sha256>
  --artifact <local-unpacked-target> --signature=<detached-signature-or-empty>
  --allow-local-mcp-artifact= --allow-trusted-in-process-artifact=
  --allow-remote-action-artifact= --allow-wasm-action-artifact=
  --allow-remote-action-endpoint= --allow-remote-action-secret-ref=
  --allow-model-secret-ref=
```

命令必须绑定 exact Tenant/Review/APPROVE Decision。首片只接 `LOCAL_DIRECTORY + DENY` 的显式
`--source-root`，不读取 Index `PackagePath`；全部 grant flags 必须显式出现且为空。唯一支持的 impact
是一个 PROFILE `context.provide/v1` Binding，target 必须为 `DECLARATIVE + static/v1 +
TRUSTED_INSTRUCTION + deny-all Authority`。Model、Channel、Action、`SINGLE`、shared-current、fanout
与多 impact 都失败关闭。

production 顺序固定为 historical U1 verify → current Store revalidate → same exact plan → current U1
verify → pre-stage → 既有 Apply/CAS。Review/Apply 复用同一 evaluator，不存在独立 dry-run admission。
旧 Instance 在 exact ordinal 原位替换，其他 Binding canonical bytes 与顺序不变；旧 Run 继续冻结旧
Provider，新 Run 使用 target。相邻 current exact retry 零 source/stage/Store mutation 并返回原 publication，
但不建立 durable receipt；后续 publication 后返回 `POINTER_CONFLICT`。UNKNOWN 原样保留且不重放。
Backup/Verify/Restore、Pure Chat 零访问与 32 表 Store 均保持不变。U4 历史状态为
`W2_U4_OPERATOR_APPLY_ACCEPTED_DEVELOPMENT_SLICE / W6_0_CONTROL_API_CONTRACT_NEXT`。

服务停止且 Current Store writer 独占时，当前 `module-apply` 可以把一个已验证的精确包显式
Enable 到某个 Profile 或 Workspace/Endpoint，也可以对非 Model Port 做 deny-only Disable。当前
Core-owned 通用表支持 9 个唯一的 exact protocol tuple，key 为
`exact Port + runtime mode/protocol + verified consumer schema`：DeepSeek Model、声明式 Context、
Knowledge、Memory、MCP Action、compiled `text.stats` Action、Channel transport、窄 REMOTE
Action 与窄 WASM Action。它们分别是
`model.generate/v2 + TRUSTED_IN_PROCESS/go-in-process/v1 + model-binding-config/v2`、
`context.provide/v1 + DECLARATIVE/static/v1 + context-binding-config/v1`、同一 Context Port 上
`TRUSTED_IN_PROCESS/go-in-process/v1` 配 `knowledge-context-binding/v1` 或
`memory-context-binding/v1`、`action.provider/v1 + LOCAL_PROCESS/mcp-stdio/2025-11-25 +
action-binding-config/v1`，同一 Action Port 上 `REMOTE/freeagent-action-http/v1 +
action-binding-config/v1`、`WASM/freeagent-action-wasm/v1 + action-binding-config/v1` 或
`TRUSTED_IN_PROCESS/go-in-process/v1 +
action-binding-config/v1`，以及 `channel.transport/v1 + TRUSTED_IN_PROCESS/go-in-process/v1 +
channel-binding-config/v1`。这里是 9 个唯一 tuple，不是 9 种 protocol。

E5-B 当时没有增加新 tuple，而是在该通用表之前增加 2 个 Document Insight reserved exact selector：
一个复用 `context.provide/v1 + TRUSTED_IN_PROCESS/go-in-process/v1 +
knowledge-context-binding/v1`，另一个复用 `action.provider/v1 +
TRUSTED_IN_PROCESS/go-in-process/v1 + action-binding-config/v1`。两者还必须同时精确匹配
`freeagent.builtin.document-insight@1.0.0` 与 ArtifactDigest
`838ff9ddd45186f0cdb26021d16902b2bfd7581c2dc0d7b72014cf4f48d0f7ea`；错误身份不得回落到普通
Knowledge 或 `text.stats` handler。这只是固定产品身份的两条 Core policy，不是运行期注册或通用
multi-Port 发现机制。

R1 随后增加第 8 个 REMOTE Action tuple，R2 再增加第 9 个 exact WASM Action tuple。九者共用
唯一 Current Store/Catalog、Assembly 和 exact Binding 规则。除 Channel 外的八者绑定 Profile；Channel
tuple 的 target 固定为 `WORKSPACE_CHANNEL_ENDPOINT`，由 Workspace/Endpoint 精确定位，不绑定
Profile。
`ENABLED module.expected_runtime_request:{mode,protocol}` 只表达 Operator 对 Manifest request 的
精确预期；Core 才使用完整 key 选择 handler 并授予 ExecutionClass/AdapterIdentity。Plan 和
Manifest 不能授予 Trust、ExecutionClass、AdapterIdentity、Authority 或 handler identity。
Apply 不自动发现、升级、热加载或调用模型/Action，不执行声明式内容，也不启动 MCP 进程或发送
REMOTE POST；Knowledge/Memory 包只提供不可变数据，REMOTE 包只提供受摘要覆盖的 descriptor。
WASM Apply/Dry-run 只执行有界预检和 compile validation，不实例化或执行 guest；四类 artifact
grant 必须按 handler 精确匹配并互斥，任何路径都不在 Apply/Dry-run 解析 Secret。
真实 Action 调用仍只走原 Gateway。计划格式、handler-specific grant、canonical 示例、状态和
错误码见同一开发合同。该入口当前仍为 `EXPERIMENTAL`。

W2-D 的本地显式装配 v1 当前是 `W2_D_LOCAL_ASSEMBLY_ACCEPTED_DEVELOPMENT_SLICE`。集中纵链已在
同一 production Apply/Assembly/Chat/Gateway/Store/Backup/history 路径闭合五类本地模块；共享包
缓存逐 Catalog Entry 复验和 unbound `module-inspect` 完整闭包两项 P1 也已修复并回归。最终
Windows 全仓测试（188.9 秒）、有效 WSL2 ext4 全仓测试（128.1 秒）、vet/mod/gofmt、License
35/55 与 PublicTree 均通过。该 accepted 不提升仍为 `EXPERIMENTAL` 的 Operator Module Apply、
Module Conformance，也不提升仍为 `PLANNED` 的广义通用装配、W6/W7 或公开 Beta。

W2-E2 当前为 `W2_E2_TRUSTED_TEXT_STATS_APPLY_ACCEPTED_DEVELOPMENT_SLICE`。它从普通 Pure Chat
Store 和官方 unpacked `text.stats` artifact 开始，经 production `module-dry-run/module-apply`
安装，不依赖 Action seed；闭合未绑定 Profile 零加载、真实 model→action→model、exact retry
零新增、Backup/Restore 后继续运行与历史账本不变，以及 Disable 后新 Run 回到 Pure Chat。该
网络无关验收没有调用真实 API，也不开放任意第三方进程内 Action。

W2-E3 当前为 `W2_E3_WORKSPACE_CHANNEL_APPLY_ACCEPTED_DEVELOPMENT_SLICE`。`ENABLED` Channel Apply
把 Control/Catalog publication 与 revision-0 `CURSOR_SEED` 原子提交；Dry-run 不写 Store、不解析
Secret 且不联网，exact retry 即使 Cursor 已继续推进也复验原始 revision-0 seed。Disable 只移除
当前 Workspace Endpoint；Cursor、Attempt、历史 Control/Catalog 与 evidence 保留，共享 Instance
直到最后一个引用消失才退出当前 Catalog。集中验收闭合了同一 Agent/Profile、同一 Channel
Instance 下双 Workspace/Endpoint 的 Cursor、Run 与 history 隔离，成功和 Channel `UNKNOWN`
duplicate 零重发，一个 Endpoint 的未对账 `UNKNOWN` 不阻塞另一个，并在 Backup/Restore 后从各自
下一 Cursor revision 继续。该证据不代表公网 Channel、任意第三方进程内代码、Beta，也未获得首次部署许可。

W2-E4 当前为 `W2_E4_DEEPSEEK_MODEL_REPLACEMENT_ACCEPTED_DEVELOPMENT_SLICE`。它只允许
`freeagent.builtin.model.deepseek@1.0.0` 在同一 Artifact、Adapter 和 Provider Instance 内，以
Operator 显式 Apply/Dry-run/CAS 将唯一 `model.generate/v2` Binding 从 `deepseek-v4-flash`
替换为 `deepseek-v4-pro` 或反向替换。候选必须闭合 exact Config、
`model-authority-ceiling/v1` 与临时提供且精确匹配的 SecretRef grant；ModelProfile 可选，但提供时
必须 exact 匹配，省略时表示清除当前画像。原 E4 要求的 PriceSnapshot 预存在已随 P0 金额退场删除。Model Port 不支持 Disable，回滚仍是一次新的 `ENABLED` Apply。

E4 publication 只影响新 Run；旧 Run 继续使用已冻结 Binding/Config/Authority/SecretRef/
Profile。`UNKNOWN` 不允许换模型、创建替代 Attempt 或语义重放。Current Store 的
direct publication 与 Backup current semantic gate 都会重新验证 Authority/Profile closure。
集中验收已覆盖跨 Workspace/Profile、显式替换与回滚、token Usage、Backup/Restore 和失败关闭
负例矩阵；本切片没有新增 Schema、表、Runtime、Loop、Gateway、Catalog pointer 或效果账本。
在 E4 Model Port 内，自动选模、成本路由、跨 Provider 替换与多 Provider 仍未实现；REMOTE/WASM Action 仅支持 R1/R2 exact 合同，广义通用装配仍未实现。

W2-E5-A 当前为 `W2_E5_A_REQUIRES_PERMISSION_GRANT_ACCEPTED_DEVELOPMENT_SLICE`。新的不可变
governed Knowledge 版本仍只提供 `context.provide/v1`，但 Manifest 同时声明 exact
`model.generate/v2` Require 并请求 `knowledge.read`；旧 permissionless Knowledge 版本继续兼容。
Manifest 只提出请求，不能自授予权限。Core 会从同一 Profile 的 exact Binding、Catalog、Activation、
Installation、Manifest、Config 与 `knowledge-authority-ceiling/v1` 重算唯一 Model 依赖和有效 grant，
并把检索限制裁剪到 Config 与 Authority 的交集；缺失、歧义、跨 Profile、循环、部分 Manifest、
未知 permission 或越过 Tenant/Workspace/Agent scope 都失败关闭，不持久化第二份依赖或 grant 事实。

E5-A 集中验收闭合了 production Apply/Dry-run、真实本地 RAG Chat、grant 从 `4/4096` 收窄到
`2/2048`、Disable 后新 Run 零 Knowledge 而历史字节不变、CAS 竞争，以及 enabled/disabled
Backup→verify→restore。首次发布、exact retry、公开 Store Verify、Backup 与生产 loader 都重验同一
确定性闭包。四个受影响包 `./sdk/moduleapi`、`./cmd/freeagent`、`./internal/currentstore` 和
`./internal/currentbackup` 已完整回归；该 E5-A 历史验收当时 `go test ./...` 的 30 个仓内 Go package 用时 221.9 秒，另有
1 个 external compatibility package 由 `sdk/moduleapi` 嵌套测试在临时独立 module 中编译验证；`go vet ./...`、
`go mod verify`、`gofmt -l .`、Docs（38 份 Markdown）、Capability Matrix（49 项、稳定级 0 项）、
License（35 个 Go dependency/57 个 distributed asset）、Branding 与 PublicTree 均通过。本切片未调用
真实 API，也没有新增 Schema、表、Runtime、Store、Loop 或 Gateway。

W2-E5-B 当前为
`W2_E5_B_DOCUMENT_INSIGHT_DUAL_PORT_ACCEPTED_DEVELOPMENT_SLICE`。固定模块
`freeagent.builtin.document-insight@1.0.0` 的 ArtifactDigest 为
`838ff9ddd45186f0cdb26021d16902b2bfd7581c2dc0d7b72014cf4f48d0f7ea`、covered size 为 `1097`
bytes、Adapter 为 `freeagent.adapter.document-insight/v1`，Runtime 为
`TRUSTED_IN_PROCESS/go-in-process/v1`。Manifest 必须按顺序只提供 `action.provider/v1`、
`context.provide/v1`，只要求 `model.generate/v2`，并只请求 `knowledge.read`。模块 artifact 只携带
不可变 JSON；RAG 与无外部副作用的 `text.stats` 实现编译进 Core，不执行第三方包内代码。

Document Insight 由 Context-first、Action-second 两次显式 Apply 发布，同一模块通过两个不同
PortPlan 消费，并向 Context 与 Action PortPlan 各贡献一个 Binding；Context PortPlan 保留既有
`context.basic`。两个 Document Insight Binding 及 Action Attempt 使用完整相同的
`ActivatedModuleRef`。Action-first 以 `TARGET_CONFLICT` 零写拒绝。绑定后的同一 Run 同时执行真实
本地 RAG 与经唯一 Gateway 的 `text.stats`，形成 2 个 Model Attempt 和 1 个 Action Attempt；exact
Chat retry 不新增 Run 或 Attempt。Backup→verify→restore 后 RAG、Action 链与 MemberSnapshot
canonical bytes 不变，新 Run 继续消费双 Port。Context-first Disable 以 `PUBLICATION_FAILED` 零写
拒绝；先 Disable Action 后仍可 RAG，再 Disable Context 后回到 Pure Chat；CAS 竞争只有一个 winner。

E5-B 聚焦产品验收连续 3 次通过；独立 `cmd/freeagent` package 输出为 185.746 秒、墙钟为
186.968 秒。该 E5-B 历史验收当时的 30 个仓内 Go package 全仓墙钟为 217.539 秒，其中
`cmd/freeagent` package 为 215.004 秒；另有 1 个 external compatibility package 由 SDK 嵌套测试
编译验证。`go vet ./...`、`go mod verify` 与 `gofmt -l .` 通过；Docs（38 份 Markdown）、
Capability Matrix（49 项、稳定级 0 项）、License（35 个 Go dependency/59 个 distributed asset）、
Branding 与 PublicTree 均通过。本切片未调用真实 API，也未新增
Schema、表、Port、Runtime、Store、Loop、Gateway、Catalog pointer、grant 表或效果账本。

该 accepted 只证明上述固定受信产品切片。任意模块的通用 multi-Port、同一 PortPlan 多
ProviderBinding/merge/fallback、任意第三方进程内 Action、通用权限语言、REMOTE/WASM、不可信代码
隔离、自动发现/升级、在线控制面、Beta 与生产部署仍未完成。

前序 W2-R1 开发切片保持
`W2_R1_REMOTE_ACTION_HOST_ACCEPTED_DEVELOPMENT_SLICE`。它只接通
`action.provider/v1 + REMOTE/freeagent-action-http/v1`：Manifest entrypoint 是受 ArtifactDigest
覆盖的 `content/` descriptor，endpoint 与 SecretRef 由 Operator Binding 授予，Secret value 只在
Gateway dispatch 时从显式环境映射晚绑定。`module-apply`/`module-dry-run` 要求 exact artifact、
HTTPS endpoint 与 SecretRef 三项 grant，但保持零 Secret lookup、零 Provider、零 POST；正常
`chat/serve` 还必须显式启用 `--enable-remote-actions` 和
`--remote-action-secret-env <secret-ref>=<ENV_VAR>`，默认关闭。

production 执行固定经过 Catalog → lazy loader → digest/size/descriptor 复验 → native Adapter →
原 Gateway。Describe/Prepare 离线，generic Invoke 禁止；只有原 DispatchAttempt 已持久为 PENDING
后才允许恰好一次 HTTPS/1.1 POST。Host 禁止 proxy、redirect、HTTP/2、keep-alive、retry、fallback
和动态发现，并拒绝 private、loopback、link-local、multicast、NAT64、文档/基准及其他
special-use 地址。POST 前拒绝为 FAILED；进入传输后无法证明结果时只把原 Attempt 置为 UNKNOWN，
禁止语义重放、换 Provider 或替代 Attempt。

集中测试已真实穿过 production loader/native Adapter/Gateway，在 PENDING 后、POST 前确定性失败；
篡改构件不缓存，Provider `FAILED + HTTP 422` 只有一次 RoundTrip，Disable 后删除 artifact 的真实
Pure Chat 对 REMOTE artifact/Adapter/Secret/Action Attempt 全部零访问。SUCCEEDED、UNKNOWN、终态
持久化失败、重启和 Backup/Restore 由 hermetic 分层/长链证据闭合；本切片没有真实公网 HTTPS
第三方互操作证据，也不代表其他 REMOTE、WASM、不可信隔离、生产网络 SLA 或 Beta 已完成。

W2-R2 收口时状态为
`W2_R2_WASM_HOST_ACCEPTED_DEVELOPMENT_SLICE / W2_R3_UNTRUSTED_MODULE_ISOLATION_NEXT`。它只支持
exact `action.provider/v1 + WASM/freeagent-action-wasm/v1 + action-binding-config/v1`，由 Operator
以 `--allow-wasm-action-artifact <sha256>` 精确授权；该 grant 与 LOCAL_PROCESS、
TRUSTED_IN_PROCESS、REMOTE 三类 artifact grant 互斥。Apply/Dry-run 复验 descriptor、Action
mapping、摘要、ABI 与资源上限，只 compile-validation，不实例化或执行 guest。运行期默认关闭，
只有 `chat`/`serve` 同时显式传入 `--enable-wasm-actions` 和至少一个
`--allow-wasm-runtime-artifact <sha256>`，且 digest 属于当前 exact WASM Catalog，才允许 lazy loader
物化 Adapter；未绑定、未启用、未获运行期 exact 授权或 Disable 后的 Pure Chat 不读取 WASM
artifact，也不编译或实例化 guest。

WASM Host 固定使用 `wazero v1.12.0` interpreter、Core WASM 1.0 与 wasm32。guest 只能导出
`memory`、`freeagent_alloc_v1` 和 `freeagent_execute_v1`；imports、WASI、Host Module、网络、文件系统和
Secret 全部为零。当前 ceiling 为 module 16 MiB、request 128 KiB、output 32 KiB、memory initial
最多 32 页且显式 maximum 最多 256 页、table 最多 65,536 elements、全进程最多 4 个并发 guest
instance，单次验证/执行最长 5 秒。唯一 Gateway 只能在原 DispatchAttempt 已持久为 PENDING 后调用
私有 `ExecutePrepared`。receipt 的 engine 固定为 `wazero-interpreter/v1.12.0`，并明确记录
`instruction_metering=UNSUPPORTED`；不写 token、price、cost 或 guest diagnostics。

guest trap、超时、ABI 或输出拒绝均确定性收口原 Attempt 为 FAILED；只有既有事务提交失败或崩溃
遗留 PENDING 才在 startup recovery 中把同一 Attempt 置为
`UNKNOWN/RECOVERED_PENDING_AFTER_CRASH`，禁止语义重放、fallback、换 Provider 或创建替代
Attempt。Backup/Verify/Restore 逐字节保存 descriptor、WASM 和冻结 Action 事实，但不编译、实例化
或执行 guest；恢复后的冻结 SUCCEEDED/UNKNOWN exact retry 也不重新访问 artifact。该 R2 切片是窄
in-process WASM capability boundary，不是 OS/container sandbox，也不提供面向恶意多租户的强隔离。

W2-R3 当前状态为 `W2_R3_UNTRUSTED_MODULE_ISOLATION_ACCEPTED_DEVELOPMENT_SLICE`。它不引入第二
Runtime、Store、Loop、Gateway、Catalog、Port 或效果账本，只在 R2 边界上增加三项收口：

- runtime 总开关与非持久化 exact digest allowlist 必须同时存在；非 canonical、重复或非当前 Catalog
  的 digest 启动即失败关闭，模块包不能自授运行资格；
- WASM Adapter 只缓存 ModuleID、Version、ArtifactDigest、ExecutionClass 与 AdapterIdentity；Tenant、
  Workspace、Instance、Activation revision、Config、Authority 和 Binding 权限仍由当前 Store/Gateway
  的 exact 闭包决定，同一构件可以安全服务多个合法 Activation；
- Gateway 在 lazy resolution 前与私有执行入口前各做一次 current Activation 检查。第二次成功是
  execution-admission linearization point；此前撤权 guest=0，此后已经接纳的 `Effect=none` guest 可在
  既有 5 秒上限内完成。当前撤权必须停机排空、退出销毁 cache、离线 `module-disable` CAS 后重启；
  活动服务占用 Store 时返回 `STORE_BUSY`，不提供热撤权或运行中 guest 强杀。

Disable 不改写 Installation、Activation、历史 Run/Attempt 或 UNKNOWN。最后引用 Disable 后，新进程
移除 runtime allowlist 并删除历史 artifact，Pure Chat 仍保持 loader/Adapter/guest/Action Attempt 零
访问；禁用后的 Backup/Restore 保留历史和 disabled current 状态。此切片只约束模块作者可控制的
Manifest、descriptor、guest bytes 与输入，不声称防御 Go/wazero/OS 漏洞，也不提供 fuel/instruction/
RSS 硬上限、OS/container 或生产恶意多租户隔离。

同一停机操作面现在还提供当前/精确历史观察与独立禁用命令：

```powershell
go run ./cmd/freeagent module-list `
  --db .local/current.sqlite `
  --tenant default

go run ./cmd/freeagent module-history `
  --db .local/current.sqlite `
  --tenant default `
  --control-revision 2 `
  --catalog-generation 2

go run ./cmd/freeagent module-inspect `
  --db .local/current.sqlite `
  --artifact-root .local/artifacts `
  --tenant default `
  --instance role-architect

go run ./cmd/freeagent module-disable `
  --db .local/current.sqlite `
  --artifact-root .local/artifacts `
  --plan .local/disable-module.json

go run ./cmd/freeagent module-dry-run `
  --db .local/current.sqlite `
  --artifact-root .local/artifacts `
  --plan .local/enable-role.json `
  --artifact .\unpacked-role-module
```

三个查询的 Store 访问都只通过 immutable/query-only 的 `ReadOnlyObserver`；服务必须停止，
数据库必须无 WAL/SHM sidecar。`module-list` 返回 current PublishedBasis 与 Binding refs，并闭合
当前 Catalog、Installation 和 stored Manifest；它不读取 artifact root。`module-inspect` 只接受
current exact Instance，并额外复验调用方给出的 artifact root 后返回非敏感 Manifest 摘要。
`module-history` 必须由调用方给出 exact Control/Catalog revision pair；它恢复该 pair，并通过
既有窄 Installation 投影解析 stored Manifest 以闭合 Source、Activation、Port 与当时的有序
Binding refs。history 不读取 artifact root、不输出 Manifest 正文或摘要、不构造 historical
PublishedBasis、不枚举或计算 diff，也不把孤立 Activation 猜成 DISABLED。三个查询都不单独
materialize 或输出 Config、Authority、StaticContext 正文，也不启动模块、模型、网络、Secret
resolver 或恢复流程。`module-disable`
只接受 canonical `DISABLED module-apply-plan/v1`，继续复用原 Apply/recovery/CAS；它不删除
Installation、Activation、artifact、历史 Run、Attempt 或 evidence。在线热查和自动升级仍未实现。

`module-dry-run` 接受与 Apply 相同的 canonical plan、artifact 和 handler-specific 摘要 grant：
MCP 只接受匹配候选摘要的 `--allow-local-mcp-artifact`，compiled `text.stats` 只接受匹配摘要的
`--allow-trusted-in-process-artifact`；两者互斥，声明式 Context、Knowledge 与 Memory 禁止 grant。
命令只通过
`ReadOnlyObserver` 观察 self-contained Store，并只在系统 TEMP 中验证候选包。它返回 canonical
`freeagent.module-dry-run-result/v1`：状态仅为 `WOULD_APPLY`、`NO_CHANGE` 或
`ALREADY_APPLIED`，同时给出完整 observed/candidate Basis、受限变化摘要和
`startup_recovery_required`。`PROJECTED_NOT_RESERVED` 只是绑定 observed Basis 的可丢弃预测，
不是授权、reservation、receipt 或可提交对象；命令不恢复 PENDING、不取得 writer、不写 Store
或 artifact root，也不启动 MCP、模型、网络或 Secret resolver。Knowledge dry-run 会复验
Artifact、Config、Authority、source 与 scope 闭包并构造同一个内建只读 Adapter，但绝不调用
Knowledge provider。历史查询保持独立的 exact revision 只读路径，不参与 dry-run 候选计算。

W3 在该 Knowledge Apply 基础上完成了本地实用化纵链：K1 用无 I/O 纯函数生成
`FRESH_RAG/NOT_SELECTED` 决策；K2A 为未选择路径保留独立 Authority-only shortcut；K2B 在
任何 Provider 调用前完成全部 Binding 的配置、协议、Authority、scope 与 limits 预检；K3 只
复用同一 Conversation 中最新的 exact-question 成功候选，候选无效时立即 fresh，不向更老
候选穿透。fresh、reuse 和 shortcut 使用相互独立的证据；实际 reuse 时 Knowledge Provider
调用为零。该纵链仍只支持本地确定性 Knowledge；远程向量数据库以及把 Learning Proposal
自动审核、物化并 Apply 为活动知识的路径尚未实现。
未绑定 Knowledge 的 Pure Chat 请求与零可选模块访问边界不变。

默认 bootstrap seed 只激活精确 allowlist 内的确定性 Echo Model 和声明式基础 Context，
用于验证 Core/Store/恢复链；独立 RAG seed 额外装配一个本地、确定性、只读、无计费
的共享知识 artifact；独立 Memory seed 显式装配一个本地、有界、无计费的轻量 Memory
artifact；独立 Action seed 是显式装配本地、确定性、无副作用 `text.stats` Built-in 的快捷
bootstrap。它不是唯一安装入口：普通 Pure Chat Store 也可通过 canonical plan、匹配摘要的
`--allow-trusted-in-process-artifact` 和统一 `module-dry-run/module-apply` 安装相同模块。
任意 OpenAI-compatible endpoint、除精确 DeepSeek allowlist 外的外部模型、远程向量数据库/
计费 RAG、Learning 的自动审核/物化/Apply 与后台学习式 Memory、远程
Action、MCP Streamable HTTP/Resource/Prompt/Secret、公网 Channel、Reviewer 自动选择、
无限返工、公平动态图、任意 Workspace 间内容交换和在线控制平面尚未进入当前已验收开发范围。
W5-X1 只例外允许双边授权后的 `TASK_SUMMARY` 请求与 `SPECIALIST_RESULT` 返回，并且最多一次
预冻结修复；它不开放 History、Memory、知识正文、SecretRef 或自由 payload。
默认关闭的受控 DeepSeek Adapter 只在显式 Operator 开关和精确 artifact/config/runtime
闭合时接入；默认关闭的 S3-A 公平 Scheduler 已按显式 Operator 开关接入，不读取任务内容；默认关闭
loopback Channel 已接入；带合法 optional `composite_agents` 的 Store 可通过
`chat --composite` 运行 Parent/Child specialist family；Composite 定义显式携带
`reviewer` 时，Specialist 成功后由同一 Loop 执行一次 Reviewer gate，只有 canonical
`APPROVE` 才允许 Root merge。显式请求未装配能力
必须失败关闭，不能静默降级。

S2.1 Context Compiler 已在唯一 Universal Loop 内接线：低于 85% 不创建压缩记录，
达到 85% 时最多生成一个可恢复摘要，达到 100% 时不先摘要，而是按历史顺序只移除
恢复到 context 容量目标所需的最小完整旧前缀。静态 Context 和最近窗口受保护；不可信 Context
使用固定前缀与 canonical JSON 数据信封进入用户消息，不能伪装成系统指令。编译结果
与模型请求、Attempt 在同一事务中持久化，并由完整备份逐字节保存。W3-M2 只考虑同一
Conversation 的直接成功前驱：`<85%` 忽略候选，`>=85% 且 <100%` 仅在本轮所选连续最旧原始
pair 前缀与候选范围完全一致时逐字节复用摘要，否则确定性重新生成；`>=100%` 忽略候选并
最小 Drop 完整旧 pair。当前 token 估算始终重算，禁止 predecessor penetration 和
summary-of-summary，也没有新增摘要表、ContentKind 或第二 Compiler。

Pure Chat 的 Conversation 开发切片保持“每个回合一个 Run、每个 Run 一个模型步骤”。
它通过固定 scope 与精确 revision/head CAS 串联成功 Run，并把前驱的完整
`USER TASK_INPUT + ASSISTANT MODEL_RESULT` pair 交给唯一 Context Compiler；85% 摘要与
100% 最小有序移除都以 pair 为不可拆分单位。确定性本地 50 轮产品测试覆盖陈旧 head
防分叉、跨命令重入与 Store 重新打开、第 25 轮 backup/verify/restore 后继续、第 25/50 轮精确重入、最终
bundle，以及 50 个 Run/Attempt/Usage 的闭合；Windows 用时 21.964 秒，WSL 用时
15.594 秒。`TestRunConversationFiftyTurnsAcrossNormalProcessRestart` 还让两个正常退出的
独立 OS 进程依次完成 25+25 轮，并再次验证 50 Run/Attempt/Usage 与
Action/Memory/Channel 零访问。`TestRunServeLoopbackAndGracefulShutdown` 覆盖 HTTP
Conversation 首次创建 `201`、精确重试 `200`、`/v1/chat` 续聊和服务重启后的下一轮。
显式 Action Profile 仍最多执行
“模型一 → 一个 Action → 模型二”，但 W1 Conversation 不接受 Action；Composite 和
Conversation 在入口显式互斥，Channel 也没有获得 Conversation 成熟度声明。该切片仍不是
生产多轮 SLA。W1 的真实 DeepSeek 50 回合现已单独验收完成；该验收只覆盖普通单 Agent
Pure Chat，不把上述组合边界、生产部署或公开 Beta 标记为完成。

ModelProfile 是一个可选、内容寻址的 `CONFIG`：它锁定精确 provider、model build、
Binding config、adapter artifact/identity、评测版本与能力/可靠性倾向。当前唯一运行
效果是把 ContextPolicy 的窗口上限取两者最小值；它不能自动选模、放宽资源上限、修改权限
或按品牌进入特殊分支。默认示例不绑定画像，因此无画像 Pure Chat 不多读 CONFIG，
也不会增加新的 Port、Store 表或路由器。

本地 RAG 继续复用 `context.provide/v1`。知识正文保存在共享、内容寻址的模块 artifact 中，
不复制进 Agent 或 Workspace；Run Assembly 冻结精确 Binding、Config、Authority 和 Source
revision。W3 的路由与权限预检发生在 Provider 前，低置信、并列、证据不足或任一 ACL/
revision/TTL/Memory counter proof 变化都安全回退 fresh。相同问题只允许同 Conversation
最新 exact 候选复用，且 reuse 不调用 Knowledge Provider；`NOT_SELECTED` 不伪装成 zero-hit。
默认 Profile 不绑定 RAG，即使 Catalog 中存在知识模块，也不会读取其 artifact 或调用 Host。

Agent 轻量 Memory 也复用 `context.provide/v1`。M1 让它通过同一
`module-dry-run/module-apply/module-disable` 操作面装配：空 Head 只创建显式 Genesis，已有 Head
原样保留，Disable 只影响未来 Binding；运行时更新继续走既有成功终态的受治理 Apply 路径，
没有第二条 Memory 写入链。它按 Tenant + 固定 AgentID 保存 append-only revision，并用
Workspace visibility 隔离显式轻量事实/偏好、`TASK_SUMMARY` 和用户输入的有界分类/重复词
计数；失败、`MODEL_UNKNOWN` 和精确重入均不更新。`TASK_SUMMARY` 只表示 Agent 基础记忆，
不能冒充 M2 的 Conversation Summary。默认 Profile 不绑定 Memory，因此保持零访问。

首个 Action 切片新增 `action.provider/v1` 的公开 `Describe/Prepare`，但 executor 只对
Gateway 私有。模型只能看到公开 ActionID 与输入 Schema；Core 生成并冻结
`ActionProposal`，在任何执行前原子提交 `DispatchAttempt=PENDING`。当前 `text.stats`
只做本地文本统计；成功结果以不可信 USER 数据进入模型二。失败明确终止，UNKNOWN、
进程中断或效果后终态写入失败均保留原 Attempt 等待对账，禁止重放或切换 Provider。
默认 Pure Chat 的 Admission 与单 Run 热路径不读取 Action artifact、定义或
`dispatch_attempts`，也不增加工具 Schema token；进程启动/关停时对共享 Attempt ledger
执行的最小 Store-only 安全扫描不加载 Action 模块，也不构成该 Run 的可选能力访问。

W2-E2 已证明上述 `text.stats` 也能从普通 Pure Chat Store 经统一 Apply 进入同一生产链。Dry-run
零写且不加载 Provider，Apply 不执行 Action；只有新 Run 的冻结 Action Binding 被显式选择后，
Core 才按 exact handler 惰性加载 compiled Adapter，并让调用经过原 Proposal、DispatchAttempt、
Gateway 与模型二。该 accepted 不把 `--allow-trusted-in-process-artifact` 变成通用进程内代码开关。

首个 MCP 切片只是现有 Action Provider 的一个可选 Host Adapter。Operator 必须在
`init` 时用可重复的 `--allow-local-mcp-artifact <sha256>` 精确批准本地模块；只有显式
选择该 Action Binding 的 Admission 才会启动一次有界 `initialize → tools/list`，执行
仍由同一 Gateway 和 `DispatchAttempt` 账本控制。每次执行使用一个短生命周期进程和
一次 `tools/call`；超时、部分写入、崩溃或本地 SDK 关闭歧义进入原 Attempt 的
`UNKNOWN`，不自动重试。默认 Pure Chat、启动账本扫描和历史 UNKNOWN 均不读取 MCP
Config/Authority/artifact，不构造 SDK client，也不启动进程。

该首片不包含 restricted token、namespace/seccomp 或文件系统/网络策略沙箱。空环境只
阻止 FreeAgent 主进程环境变量被自动继承，受管进程树只保证有界取消和关停；二者都不
限制子进程使用当前 OS 用户权限访问宿主。因此精确摘要批准表示 Operator 对该构件的
宿主级完全信任，而不是把不可信代码变安全。

后续模型和受控模块仍通过版本化 Port、ArtifactDigest、Activate 与 Bind 接入，不需要
修改 Universal Loop。FreeAgent 只会经 SecretRef 或受控 Host 注入凭据，不能把凭据写入
seed、RunManifest、日志或示例文件；这项注入规则不构成对当前完全信任
`LOCAL_PROCESS` 的宿主文件访问隔离。

## 配置与导入

当前可执行 bootstrap 输入有：

- `examples/current-v1.bootstrap.seed.json`：当前 Pure Chat 的 Tenant、Workspace、
  Agent、Profile、Context Policy、声明式 Context 和精确 Echo artifact 锁。
- `examples/current-v1.rag.bootstrap.seed.json`：在同一 Core 上显式增加本地共享 RAG
  Binding、Config、Authority 和精确 knowledge artifact 锁。
- `examples/current-v1.memory.bootstrap.seed.json`：在同一 Core 上显式增加 Agent 轻量
  Memory Binding、Config、Authority 和精确 deterministic memory artifact 锁。
- `examples/current-v1.action.bootstrap.seed.json`：在同一 Core 上显式增加本地
  `text.stats` Action Binding、Config、Authority 和精确 artifact 锁。
- `examples/current-v1.deepseek.bootstrap.seed.json`：单 Agent Pure Chat 的受控 DeepSeek
  v4 flash Provider；不装配可选 Context/RAG/Memory/Action，runtime Secret 仅来自环境变量。

Composite 不需要新的模块包；在任一兼容 seed 顶层增加 optional `composite_agents`
即可冻结 coordinator 与 2..8 个 specialist slot。默认 seed 故意省略该字段，以证明
Pure Chat canonical bytes、装配和可选读取不受影响。

MCP 模块包是平台/可执行文件相关的外部构件，因此仓库不提交伪造的通用二进制示例。
包含 `LOCAL_PROCESS` MCP assertion 的自定义 canonical seed 必须与精确 artifact 一起
提供，并通过 `--allow-local-mcp-artifact` 逐个授权；未授权时 `init` 在创建数据库、
正式 artifact root 或子进程前失败关闭。

该示例 Seed 显式绑定了一个可替换的基础静态 Context，用来提供最小系统提示；它
不是 Core 的隐式依赖。零声明式 Context 的 Pure Chat 同样有测试覆盖，仍只装配模型
和 Universal Loop。

以下文件仍是后续工作包的设计输入，不是当前 `cmd/freeagent` 可导入的生产配置：

- `examples/workspaces*.seed.json`；
- `examples/agents.seed.json`；
- `examples/knowledge-seed.json`。

`init`、`backup`、`backup-verify` 和 `restore` 都是显式离线操作；执行前必须停止
`serve`。普通 `chat`/`serve` 不会自动 seed、migrate、repair 或删除数据库。

## 能力成熟度摘要

下表逐行区分已经接入当前唯一 Runtime/Current Store 的能力与仅有旧实现证据的
能力；任何一行都不构成生产部署或 SLA 承诺。

当前开发状态的四分类权威索引见
[`CURRENT_CAPABILITIES`](CURRENT_CAPABILITIES.md)。其中 `accepted` 只表示明确范围内的
开发切片收口，不表示部署或生产稳定；受控 DeepSeek 正常 Provider 与 W1 Conversation 已完成
真实 50 轮验收，W3 本地 RAG/Memory 实用化已完成集中验收。S3-C 专用 DeepSeek Pilot 路径仍
是 `unverified`；W2-D 本地显式装配 v1、W2-E2 受信 `text.stats` 统一 Apply、W2-E3 Workspace
Channel Apply、W2-E4 DeepSeek Model replacement、W2-E5-A governed Knowledge Require/grant 与
W2-E5-B 固定 Document Insight 双 Port 已在各自窄范围标记 `accepted`，Module Conformance 与
Operator Module Apply 仍是 `experimental`；W2-R1 默认关闭的 exact REMOTE Action HTTP Host 与
W2-R2 默认关闭的 exact WASM Action Host、W2-R3 第三方纯计算/停机撤权与 W2-U0 供应链纯合同
都已在各自窄范围标记 `accepted`；W2-U1 的显式本地 governed observation、W2-U2 的持久
Source/Index/Snapshot observation、W2-U3 的离线 exact-version Candidate/Review/Decision，以及 W2-U4
的 approved Declarative Profile Context 原位替换也已完成各自窄开发验收。U3 的 `APPROVE` 本身不产生
权限或 publication；U4 只在 exact Tenant/Review/APPROVE 与 current/source 重验后复用既有 Apply/CAS。
W6-0 又冻结了六份 pure canonical 控制 API wire 与 transport-neutral policy；其历史入口是
`W6_1_APPLICATION_SERVICES_READ_API_NEXT`。W6-1 已以
`W6_1_APPLICATION_SERVICES_READ_API_ACCEPTED_DEVELOPMENT_SLICE` 验收默认关闭的本地双 listener、
bootstrap/session、Modules list/detail 和 effect-free `MODULE_DISABLE` Dry-run；其历史下一入口为
`W6_2_CONTROLLED_MUTATIONS_AUDIT_NEXT`。该 audit 已以
`W6_2_CONTROLLED_MUTATIONS_AUDIT_ACCEPTED_DEVELOPMENT_SLICE / W6_2_CONFIRMATION_CONTRACT_NEXT`
收口，confirmation 历史原子固定为
`W6_2_CONFIRMATION_CONTRACT_ACCEPTED_DEVELOPMENT_SLICE / W6_2_DURABLE_RECEIPT_SCHEMA_NEXT`，
广义通用装配保持 `planned`。W4-L0
纯合同、W4-L1A Knowledge Proposal Store、W4-L1B 静态 Skill Draft Store、W4-L2 有界
Learning Review、W4-L3 不可变 Version/Operator 交接、W4-L4 显式 Learning Cycle、W5-X1
有界 Decision/repair 与受控跨 Workspace 纵链，以及 W5-F1 协作稳定性已完成对应开发切片。
自动 Review/Materialize/Apply、安装/激活/绑定、后台周期 worker、远程向量数据库，以及
其他 Control mutation、SSE 和 W7 公开 Beta 继续保持未实现。receipt Schema 的历史原子以
`W6_2_DURABLE_RECEIPT_SCHEMA_ACCEPTED_DEVELOPMENT_SLICE / W6_2_MODULE_DISABLE_MUTATION_WIRING_NEXT`
验收 33 表 rebuild、NO_CHANGE commit/resolver 与 Backup semantic closure；随后 W6-2 wiring 历史原子以
`W6_2_MODULE_DISABLE_MUTATION_WIRING_ACCEPTED_DEVELOPMENT_SLICE / W6_3_WEB_SHELL_READ_ONLY_OVERVIEW_NEXT`
接通首个窄在线 `MODULE_DISABLE` confirmation/mutate surface 和同事务 APPLIED publication；这是历史 marker。
当前增量状态为 `W6_5_SERVER_OWNED_MODULE_ARTIFACT_INGRESS_ACCEPTED_DEVELOPMENT_SLICE /
W6_6_SERVER_OWNED_MODULE_UPGRADE_REVIEW_NEXT`；W6-3 的 W6-4 NEXT 与 W6-4 的 W6-5 NEXT
均只保留为历史入口。
W6-1 的
process-local Dry-run receipt 与 W6-2 的 process-local confirmation proof 仍不成为持久事实或第二 authority。
旧 49 项能力矩阵继续只是历史归档。

| 范围 | 当前状态 |
|---|---|
| 本地 Pure Chat | W1 状态为 `W1_COMPLETE_REAL_DEEPSEEK_50`；CLI/loopback HTTP、服务与正常进程重启、turn 25 backup/restore、exact retry、本地确定性 50 轮和真实 DeepSeek 50/50 首次 Attempt 已闭合；统一零模块验收又证明已安装但未选择的 Knowledge、Memory、Skill、MCP/Action、Channel、Learning 与 Team 均不进入请求或产生可选状态增量；仅代表普通单 Agent Pure Chat 开发纵链，不代表生产 SLA、组合模式或公开 Beta |
| 受控 DeepSeek Provider | `accepted` 且默认关闭；固定官方 endpoint，通过显式 runtime 开关接入，W1 真实 50 轮已闭合 POST、Usage/cache 与 UNKNOWN 零重放；不开放任意 OpenAI-compatible URL，S3-C 专用 Pilot 的历史 `3 COMPLETE + 1 PARTIAL` 仍独立保持 `unverified` |
| 共享授权 RAG | W3 本地开发纵链已验收 K1 确定性路由、K2A 独立 shortcut、K2B 全 Binding Provider 前预检和 K3 严格 exact-question reuse；reuse 时 Provider 零调用，失效时 fresh；远程向量数据库、计费远程检索和 Learning Proposal 的自动 Apply 未实现 |
| Agent 轻量 Memory | W3 已验收统一 Module Apply/Dry-run/Disable 装配、Agent/Workspace 有界 revision/计数和成功终态更新；`TASK_SUMMARY` 与 Conversation Summary 明确分离；W4-L4 Store-only 补偿不读写 Memory，也不是后台 Memory 学习队列或 worker |
| Learning Proposal / 有界审核 / Version 交接 | W4-L1A/L1B 已在唯一 Current Store 与同一 `learning_proposals` 表接入 Knowledge Proposal 和静态 Skill Draft，并闭合 Tenant+kind 三轴去重、exact proposer lineage、精确重入与 backup semantic gate；W4-L2 在同一表加入唯一 review refs 和有界 state/revision，真实 DeepSeek 验收得到 `APPROVED/2`、Reviewer Attempt `SUCCEEDED`、Usage 落账和 exact retry 零重放。W4-L3 继续复用同一表，以严格 0/7 投影保存无权限不可变 Version，跨 Tenant/kind 保持全局 ModuleRef 唯一，并在 Installation→Version、Version→Installation 两个顺序都要求 Manifest/ArtifactDigest 完全一致；`learning-materialize` 只导出可重建两文件包。专属 E2E 已证明 stale CAS 零发布、Operator exact Apply 后仅新 Run 消费审批版本；仍不会自动生成 Apply plan、安装、激活、绑定或扩权 |
| 显式 Learning Cycle | W4-L4 已验收默认关闭、默认间隔 86,400 秒、exact revision 启停和显式 Tick；每个逻辑窗口只复用普通 model-only Run/Attempt/Usage，结果仅为 `PROPOSE/NO_CHANGE`，错过多周期只合并最新到期窗口。Store-only reconcile 不调用任何外部系统，report 只读且不持久化；无后台 worker、Queue、自动 Review/Materialize/Apply 或主动发送 |
| Parent/Child + Specialist（legacy S2） | 同 Workspace、depth-1、2..8 Child 的第一切片已接入当前 Assembly/Loop/Store/backup，并由 `chat --composite` 消费；该历史切片不代表 W5 的跨 Workspace 边界 |
| Reviewer 审核门（legacy S3-B） | 默认关闭的单次 `RESULTS_GATE` 已接入同一 Assembly/Runtime/Store/Loop/backup；S3-C Pilot 已在 Reviewer 调用前停止并保持历史 `unverified`，不能用后续 W5-X1 canary 改写 |
| Decision 与一次有界修复 | W5-X1 `accepted` 开发切片；显式 Decision 预冻结 `2N+3` 个物理 Run，最多激活一次 repair，未激活/跳过的 repair Run 保留且 `Attempt=nil`。W5-F1 已验证 approve、单槽 repair 与 Transfer 走唯一 Scheduler，dormant/skipped Run 零 claim；无自动 Reviewer、无限返工或动态图 |
| 受控跨 Workspace 协作 | W5-X1 `accepted` 开发切片；同 Tenant、root/target 双边 grant、每 Run 单 Workspace，v1 仅允许确定性有界 `TASK_SUMMARY` 请求和 `SPECIALIST_RESULT` 返回。X1D 已验证 4/4 HTTP 2xx、REQUEST/RESULT transfer、7 physical Runs/4 Attempts/3 nil repair Runs，exact retry 新 HTTP/Attempt 均为 0；F1 追加稳定归位、整族取消、UNKNOWN 原 Attempt 对账、完整恢复、权限负例和零 Transfer canary |
| 多 Workspace 公平调度 | W5-F1 `accepted` 开发切片；默认关闭、tenant-bound、不读取任务正文。3 个持续 runnable Workspace 共 900 次 claim，第 450 次后重开 Store，最终各 300 次，首次服务不晚于第 3 次、最大服务间隔不超过 3；这是有界本地证据，不是生产公平 SLA |
| 上下文压缩/有序移除 | W3-M2 已在唯一 Compiler/Store/Loop 上闭合直接成功前驱的可验证摘要复用、当前 token 重算和新 Conversation compiler-owned model-1 Attempt 的 Store 字节级复编译；`<85%` 不压缩、`85%–100%` 精确范围复用或确定性 fresh、`>=100%` 最小 Drop 完整旧 pair；仍不构成生产多轮 SLA |
| ModelProfile | S2 已接入 Assembly/Context/Store/backup；当前只做精确绑定与 ContextPolicy 收紧，不自动选模 |
| Usage/Cost | 每个真实 Attempt 已持久化，Decision family 的 `2N+3` 只表示物理 Run 图与 dispatch cap，不等于实际调用数；未激活 repair Run 保持 `Attempt=nil` 且不伪造零 Usage。X1D 历史样本仍为 7 physical Runs、4 Attempts、input 3,443、cached 768、uncached 2,675、output 1,353、估算 `0.00539636 CNY`；F1 Test-0808 独立样本为 input 3,149、cached 768、uncached 2,381、output 1,492、2/4 请求命中、token 加权 `24.388695%`、估算 `0.00538036 CNY`。两者 reasoning 与 Provider reported/reconciled cost 均 UNKNOWN；不新增账本 |
| Prompt Cache 统计与优化 | 三个完整 cell 的 token 加权命中率为 7.292%，PARTIAL cell 的整格命中率未知；剩余矩阵已停止，任何后续优化需另行冻结实验，不自动预热或重放 |
| 外部效果、UNKNOWN、恢复与备份 | W1、W3、W5-X1、F1 与 W2-R2 均进入同一 Store/backup 闭包。F1 已验证完整 Decision+Transfer backup→verify→restore→reopen，PENDING 恢复为同一 Attempt UNKNOWN 且 Loop 零重放；W2-R2 进一步验证 WASM 终态和三文件 artifact 闭包恢复时不编译、实例化或执行 guest，crash-left PENDING 只恢复原 Attempt UNKNOWN。可靠 evidence 只对账原 Attempt，X1D/F1/R2 exact retry 均不增加 Provider/guest 调用，S3-C 历史 UNKNOWN 仍原位保留且禁止语义重放 |
| Telegram、Lark、QQ、Weixin 适配器 | 旧专用 Channel 实现已物理退役；S2 将通过统一 Module/Port 协议重建并重新取得真实互操作证据 |
| 公开 Workspace/Agent/Profile/Task 装配与不可变目录 | `PLANNED`；现有停机本地显式入口不等于公开目录或稳定在线控制面 |
| MCP Host | 首个 Operator-fully-trusted `LOCAL_PROCESS + stdio + Tool-only` 纵链已接入 Action/Gateway/Store/backup；无 OS sandbox，不接受不可信第三方；Streamable HTTP、Resource、Prompt、Secret、自动发现与常驻进程仍未实现 |
| Skill 治理与可信 `TRUSTED_IN_PROCESS` 模块扩展 | 旧专用 Skill 证明链已物理退役；声明式 Skill 与后续执行能力统一通过 Module/Port 重建并逐项验证 |
| Module Package Conformance | `EXPERIMENTAL`；`module-verify` 无 supply flag 时保持 legacy 输出/错误逐字兼容；任一 supply flag 显式选择仅限 `LOCAL_DIRECTORY + DENY` 的 governed observation，复验 exact 外置 Policy/Key/Signature ID、Ed25519、点分段 Module ID 前缀、Policy 收紧的首轮/最终扫描和调用期撤销 deny snapshot。两条路径成功都只输出 `freeagent.module-package-verification/v1`，不产生 reservation、grant、staging、Store fact 或 Apply authority |
| W2-D 本地显式装配 v1 | `ACCEPTED / W2_D_LOCAL_ASSEMBLY_ACCEPTED_DEVELOPMENT_SLICE`；Role/静态 Skill、本地 Knowledge、W3-M1 Memory 与受信本地 MCP 已进入同一 Store/Catalog/Assembly，集中产品纵链、逐 Entry/inspect P1 回归、Windows/WSL2 ext4 全仓及发布门禁均已闭合；accepted 仅限该窄本地 v1 |
| W2-E2 受信 `text.stats` 统一 Apply | `ACCEPTED / W2_E2_TRUSTED_TEXT_STATS_APPLY_ACCEPTED_DEVELOPMENT_SLICE`；从普通 Pure Chat Store 经统一 dry-run/apply 安装 compiled `text.stats`，真实 model→action→model、exact retry、Disable、Backup/Restore 和未绑定零加载已闭合；网络无关验收未调用真实 API，不开放任意第三方进程内 Action |
| W2-E3 Workspace Channel Apply | `ACCEPTED / W2_E3_WORKSPACE_CHANNEL_APPLY_ACCEPTED_DEVELOPMENT_SLICE`；统一 Apply 已覆盖 `WORKSPACE_CHANNEL_ENDPOINT`，原子发布 Control/Catalog 与 revision-0 Cursor seed；双 Workspace/Endpoint 隔离、duplicate 零重发、Endpoint-scoped Channel `UNKNOWN`、Disable 引用保留和 Backup/Restore 后续推已闭合；仅限 first-party loopback 开发切片 |
| W2-E4 DeepSeek Model replacement | `ACCEPTED / W2_E4_DEEPSEEK_MODEL_REPLACEMENT_ACCEPTED_DEVELOPMENT_SLICE`；仅支持 `freeagent.builtin.model.deepseek@1.0.0` 同 Artifact/Adapter/Provider Instance 内 flash↔pro 的显式 Apply/Dry-run/CAS。Config、`model-authority-ceiling/v1`、临时 exact SecretRef grant 与可选 exact ModelProfile 全部失败关闭（原要求的预存 PriceSnapshot 闭合已随 P0 删除）；省略画像即清除，Model Disable 禁止，回滚使用新的 ENABLED Apply。只影响新 Run，旧 Run 冻结；UNKNOWN 不替换 Attempt、不换模型、不重放。跨 Workspace/Profile、token Usage、Backup/Restore 与负例矩阵已闭合；零新增 Schema/表/Runtime/Loop/Gateway/账本 |
| W2-E5-A Requires 与窄 grant | `ACCEPTED / W2_E5_A_REQUIRES_PERMISSION_GRANT_ACCEPTED_DEVELOPMENT_SLICE`；governed Knowledge 仍只提供一个 Context Port，但以 exact Manifest 要求同一 Profile 唯一 `model.generate/v2` 并请求 `knowledge.read`。有效 grant 由 Core 从不可变发布事实、Config 与 Authority 重算并取交集；真实本地 RAG、收窄、Disable、CAS、Backup/Restore 和失败图已闭合。该状态只证明 E5-A 当时的单 Port Require/grant |
| W2-E5-B Document Insight 双 Port | `ACCEPTED / W2_E5_B_DOCUMENT_INSIGHT_DUAL_PORT_ACCEPTED_DEVELOPMENT_SLICE`；固定 `freeagent.builtin.document-insight@1.0.0` 以同一 Installation/Activation/Instance/Adapter 通过 Context 与 Action 两个 PortPlan 被真实产品链消费。Context→Action 两步 Apply、同 Run RAG+Gateway `text.stats`、2 Model+1 Action Attempt、exact retry、CAS、Backup/Restore 与 Action→Context Disable 已闭合；Document Insight 向两个 PortPlan 各贡献一个 Binding，Context PortPlan 保留既有 `context.basic`，实现编译进 Core，包只携带不可变 JSON |
| W2-R1 窄 REMOTE Action HTTP Host | `ACCEPTED / W2_R1_REMOTE_ACTION_HOST_ACCEPTED_DEVELOPMENT_SLICE`；只支持 exact `action.provider/v1 + REMOTE/freeagent-action-http/v1`，默认关闭。Apply/Dry-run 需要 artifact、HTTPS endpoint 与 SecretRef 三项瞬时 exact grant，且保持离线；运行期还需显式 Host 开关与 SecretRef→环境变量映射。production lazy loader、原生 Adapter 与唯一 Gateway 已闭合到一次 POST 及 FAILED/UNKNOWN 边界；UNKNOWN 禁止重放、换 Provider 或新建替代 Attempt。篡改构件、Disable 后 Pure Chat 零访问、Secret/SSRF/redirect/proxy/fallback 边界及完整 Backup/Restore 已覆盖；没有真实公网第三方 HTTPS E2E、WASM 或不可信隔离声明 |
| W2-R2/R3 窄 WASM Action Host | `ACCEPTED / W2_R3_UNTRUSTED_MODULE_ISOLATION_ACCEPTED_DEVELOPMENT_SLICE`；R2 的 exact `action.provider/v1 + WASM/freeagent-action-wasm/v1 + action-binding-config/v1`、零 Host capability 与资源上限保持不变。R3 允许显式授权的非 builtin 第三方纯计算构件；Operator Apply/Dry-run 仍要求 `--allow-wasm-action-artifact`，运行时必须同时要求 `--enable-wasm-actions` 与 exact `--allow-wasm-runtime-artifact`。Gateway 二次 current Activation 检查、artifact-scoped Adapter cache 和停机 Disable 已闭合；历史 Run/Attempt/UNKNOWN 不改写。仍不承诺 OS/container、热撤权或生产恶意多租户隔离 |
| W2-U0 供应链纯合同 | `ACCEPTED / W2_U0_SIGNING_SOURCE_CONTRACT_ACCEPTED_DEVELOPMENT_SLICE`；冻结 Ed25519 Publisher Key/Signature、Operator Source Policy、Discovery Index/Snapshot、exact Candidate/Decision 七份 canonical wire。Version 保持 opaque，Candidate 只能来自 exact Snapshot，并具有跨刷新稳定 review key；unknown field、canonical/content-ID canary、defensive copy 与签名负例已闭合。没有网络、Store schema、artifact、安装、Apply、Host 或权限。U1/U2 后续接线不改写 U0 历史成熟度 |
| W2-U1/U2 来源观察 | `ACCEPTED / W2_U2_DISCOVERY_SNAPSHOT_ACCEPTED_DEVELOPMENT_SLICE`；U1 保留无 Store 的 local governed `module-verify`。U2 新增默认关闭的 Source register/refresh/Publisher Key revoke，Local 固定 `root/index.json`，HTTPS 需独立开关与 exact origin allowlist；Source I/O 前后 CAS、不可逆 revocation、跨 Source/Installation/Learning 的 ModuleRef→ArtifactDigest 门禁及历史 29 表 Backup closure 已闭合。U2 本身不获取包、不生成 Candidate/Decision、不 staging/Apply |
| W2-U3 升级审核 | `ACCEPTED / W2_U3_UPGRADE_REVIEW_ACCEPTED_DEVELOPMENT_SLICE`；默认关闭、可信本地 Operator、仅 `EXACT_VERSION_CHANGE`。Review 显式选择 exact Tenant/scope/current/Snapshot/target artifact，签名 Source 另提供 detached Signature；Store 保存全局 Candidate、Tenant-scoped Review、exact-Review Decision 与目标 Manifest content evidence。只产生 `WOULD_APPLY/CONFLICT/UNSUPPORTED`；`APPROVE` 只允许 current `WOULD_APPLY`，`REJECT` 必须显式确认 Tenant-wide `{tenant_id, review_key}` 抑制。U3 自身无下载、stage、Install、Activate、grant、Bind、Apply、Host 或外部效果；当时 `W2_U4_OPERATOR_APPLY_NEXT` 是明确的历史 marker |
| W2-U4 approved upgrade Apply | `ACCEPTED / W2_U4_OPERATOR_APPLY_ACCEPTED_DEVELOPMENT_SLICE`；默认关闭并绑定 exact Tenant/Review/APPROVE Decision，只接受 `LOCAL_DIRECTORY + DENY` 的显式 source root，绝不读取 `PackagePath`；全部 grant flags 显式且为空。首片仅允许单 PROFILE `context.provide/v1`、DECLARATIVE `static/v1`、`TRUSTED_INSTRUCTION`、deny-all 的原 ordinal replacement。historical U1 verify→current Store revalidate→same exact plan→current U1 verify→pre-stage 后复用唯一 Apply/CAS；其他 Binding 字节/顺序和旧 Run 不变，新 Run 使用 target。Model/Channel/Action/`SINGLE`/shared-current/fanout 失败关闭。共享 evaluator，无独立 dry-run admission；相邻 exact retry 无 durable receipt，后续 publication 后 `POINTER_CONFLICT`；UNKNOWN 不重放，Backup/Pure Chat/32 表不变。下一入口为 `W6_0_CONTROL_API_CONTRACT_NEXT` |
| W6-0 控制 API 合同 | `ACCEPTED / W6_0_CONTROL_API_CONTRACT_ACCEPTED_DEVELOPMENT_SLICE`；冻结 `control-session/scope/view-snapshot/operation-request/operation-receipt/event-cursor` 六份 v1 pure canonical wire。Web view 使用 `control-view-snapshot/v1`，Core `control-snapshot/v1` 不变；request 显式区分 `DRY_RUN/MUTATE`，动态 ID/时间不参与 semantic request，UNKNOWN 只返回 exact receipt 且禁止重放。没有 listener、handler、session store、SSE、UI、production command wiring、Schema 或 receipt table；当时为 39 份 Markdown、37 个源码 package、35 个 production closure package 和 32 表 Store。下一入口为 `W6_1_APPLICATION_SERVICES_READ_API_NEXT` |
| W6-1 Application Services / Read API | `ACCEPTED / W6_1_APPLICATION_SERVICES_READ_API_ACCEPTED_DEVELOPMENT_SLICE`；Control 默认关闭；显式启用时同一 `serve` 进程以 Chat listener 加独立 `tcp4 127.0.0.1:0` Control listener 共享唯一 Store、Application Services、Admission 与生命周期。owner-only handoff、一次性 bootstrap、process-local session、scope-filtered Modules list/detail、keyset cursor/ETag 与 exact If-Match 的 `MODULE_DISABLE` Dry-run 已闭合。Dry-run effect-free，receipt 仅为 process-local 响应；没有 Control mutation、durable receipt、SSE、UI、第二 Store、后台 worker或主动预热。该历史原子当时的证据为 39 份 Markdown、44 个源码 package、44 个 production dependency-closure package；Store 为 32 表且 identity 不变。其历史下一入口为 `W6_2_CONTROLLED_MUTATIONS_AUDIT_NEXT` |
| W6-2 audit / confirmation 纯合同 | `ACCEPTED / W6_2_CONFIRMATION_CONTRACT_ACCEPTED_DEVELOPMENT_SLICE`；六项 audit 只选择 TENANT/PROFILE `context.provide/v1`、`OPTIONAL`、`DECLARATIVE`、trusted-instruction + deny-all 的 `MODULE_DISABLE` 为首候选，其余五项因 artifact ingress、异步 reservation/Attempt/Task/Run 或 staging/install/activation/grant 长链 deferred。稳定 Statement 绑定 operation evaluation；proof 固定 32 bytes、最多 2 分钟且不超过当前 session absolute expiry、全局 256/每 session 8，registry 只存 proof digest 且不进入 Store/Backup/log。当时为 39 份 Markdown、45 个源码 package、44 个 production closure package、32 表 Store；无 durable row、mutation route，首片 `UNKNOWN` 不可达。下一入口仅为 `W6_2_DURABLE_RECEIPT_SCHEMA_NEXT` |
| W6-2 durable receipt Schema | `ACCEPTED / W6_2_DURABLE_RECEIPT_SCHEMA_ACCEPTED_DEVELOPMENT_SLICE`；共享 `module-apply-plan/v1`、full PublishedBasis 与 `module-disable-publication-receipt/v1` 已冻结；33 表 Store 只增加 append-only `control_operation_receipts`，提供 exact identity resolver、仅 NO_CHANGE 的公开 commit 与现存行 semantic verifier。Backup Create/Verify/Restore 在 coherent snapshot 内验证现存行，但无 external completeness anchor，不能声称发现任意整行 receipt 删除。APPLIED 在该历史原子只有 DDL/restore/verifier 形状；其历史下一入口为 `W6_2_MODULE_DISABLE_MUTATION_WIRING_NEXT` |
| W6-2 MODULE_DISABLE mutation wiring | `ACCEPTED / W6_2_MODULE_DISABLE_MUTATION_WIRING_ACCEPTED_DEVELOPMENT_SLICE`；仅显式 `--enable-control` 下开放 `POST /control/api/v1/modules/disable/confirmation` 与 `/mutate`。唯一候选为 TENANT/PROFILE、exact `context.provide/v1`、existing Binding `OPTIONAL`、`DECLARATIVE static/v1`、trusted-instruction config、deny-all Authority。每次 HTTP 都仍须重新通过当前 Origin+session+session-bound CSRF+TENANT Permit；durable lookup-first 的 exact hit 只是不依赖旧 proof 或旧 session identity，并非匿名 resolver。miss 才 claim 最多 2 分钟且不超过当前 session absolute expiry 的 process-local proof，并复验 current auth/basis。`NO_CHANGE` 与 `APPLIED` 都写 durable Control receipt；APPLIED domain receipt、Control/Catalog publication 与 pointer CAS 在唯一 Store 的同一 `BEGIN IMMEDIATE` transaction 原子提交，纯 SQLite `UNKNOWN` 不可达。Backup 验证现存 APPLIED 与 parent/replay/domain tamper，但无 external completeness anchor；publication-without-receipt 由 forced receipt-insert failure 全事务 rollback 证明。该历史原子当时为 39 份 Markdown、48 个源码 package、48 个 production closure package、33 表 Store；默认关闭，无其他 mutation、SSE/UI/worker 或第二 Store/writer。其历史下一入口为 `W6_3_WEB_SHELL_READ_ONLY_OVERVIEW_NEXT` |
| W6-3 Web Shell / read-only Overview | `ACCEPTED / W6_3_WEB_SHELL_READ_ONLY_OVERVIEW_ACCEPTED_DEVELOPMENT_SLICE`；仅既有显式 `--enable-control` 分支嵌入 exact static assets，并开放 `GET /control/api/v1/overview`。浏览器以 tab-scoped resume credential、memory-only CSRF、服务端授权 scope 与 current Workspace refs 构造选择器；Overview 有界返回 Workspaces/Runs/UNKNOWN/Learning/Module Candidates/Usage，query cache key 含完整 scope，strong ETag 和 projection/section/basis digests 均复验。Web Shell 明确区分 loading/empty/error/stale/permission-denied，提供只读搜索、detail drawer 与 deep link；无业务 mutation、SSE、worker、durable client cache、第二 Store/writer。该历史切片 Store 为 41 tables / 23 indexes / 56 triggers，fingerprint `87d63a2e468ee27ee2c0e1918a92e580532825bde85dbb872b9c795bf577f2c1`，migration 143,588 bytes / SHA-256 `5bd9743e988b82750f785c015f07c11fbcf6025da70148405aec11106b4efb22`。其历史下一入口为 `W6_4_MODULES_CONFIGURATION_UI_NEXT` |
| W6-4 Modules configuration UI | `ACCEPTED / W6_4_MODULES_CONFIGURATION_UI_ACCEPTED_DEVELOPMENT_SLICE`；复用既有默认关闭 Control、Modules list/detail、`MODULE_DISABLE` dry-run/confirmation/mutate 与唯一 Store publication owner。浏览器严格验证 UTF-8 scope/detail carrier、schema/digest/ETag/cursor/evaluation/statement/receipt；只有 TENANT/PROFILE OPTIONAL `context.provide/v1` 窄候选可经两次显式确认写入。proof/key/CSRF 不进入 URL、DOM、日志或 durable cache；不可判定结果只允许无 proof 的 exact retry，成功只 invalidate/refetch。未新增 operation、route、service、Schema、writer、artifact ingress、其他 mutation、SSE 或 worker。其历史下一入口仅为 `W6_5_SERVER_OWNED_MODULE_ARTIFACT_INGRESS_NEXT` |
| W6-5 server-owned module artifact ingress | `ACCEPTED / W6_5_SERVER_OWNED_MODULE_ARTIFACT_INGRESS_ACCEPTED_DEVELOPMENT_SLICE`；只新增默认关闭、可信本地 Operator CLI `module-artifact-ingress`。`--source-root` 与 `--artifact-root` 是瞬时可信输入且不得持久化；caller 只能选择 Store-owned current Snapshot 中 unsigned `LOCAL_DIRECTORY + DENY` 的 exact Source/Snapshot/Module/ArtifactDigest，不能提供 package path、URL 或 signature。Source/artifact/Backup tree 以 held-parent-handle 相对遍历并拒绝 link/reparse、hardlink、跨设备和 identity/change/namespace 漂移；artifact root、children 与全部 ancestors 都必须可信私有。跨进程 root lease 覆盖 crash-stage recovery、digest-addressed no-replace publish、sync 与 root 级 256 trees / 512 MiB covered content / 32,768 paths / 16,384 files / 16 MiB path-name bytes 硬预算（不声称等于实际 allocation blocks）。ambiguous commit 只留下无 authority 的有界 inert bytes；durable exact Admission 才能恢复历史 selector，未提交 stale selector不能采用，后来 current 合格 selector仍须全包复验。随后唯一 Current Store 在一个 `BEGIN IMMEDIATE` 中提交不可变 `module_artifacts` 与 append-only `module_artifact_admissions`。无 HTTP/upload/UI、Install/Activate/Bind/grant/Review/execute。Backup closure 是 installation ∪ ingress 的去重并集，并可在 Source 已离线且零 Installation 时从私有 staging 离线 restore。W6-5 的 FAC1 Store identity 只作历史；当前 FAC2 为 UserVersion 2、42 tables / 26 explicit indexes / 64 triggers，fingerprint `d5d876f327dc29dc6f4a10476652641172ab8e1f0451a8714fc450f58733541e`，并由 `0002_server_owned_review.sql` 完成 Review projection migration。W6.5 的历史下一入口为 `W6_6_SERVER_OWNED_MODULE_UPGRADE_REVIEW_NEXT` |
| W6-6 server-owned Upgrade Review | `ACCEPTED / W6_6_SERVER_OWNED_MODULE_UPGRADE_REVIEW_ACCEPTED_DEVELOPMENT_SLICE`；默认关闭的可信本地 `module-upgrade-review-server-owned` 与 `module-upgrade-decide-server-owned` 只接受 Tenant、scope、Admission/Review identity、operator principal、request digest 与 Decision reason。服务端从 immutable Admission 读取并复验 Artifact/Manifest/Source/Snapshot/Installation/Binding/Activation/Catalog/Control closure，复用 W2-U3 evaluator，持久化 Review/Decision 并按 content identity exact retry。调用方不能提供 artifact path、URL、signature bytes 或 target facts；跨 tenant、stale basis、物理篡改和不合格 Decision 失败关闭。Review/Decision 不自动 Install、Activate、Bind、Grant、Apply、Execute，不调用 Provider，不创建 Runtime/Attempt/Usage/Effect。P4 将只通过统一 service 暴露只读投影，不改变上述边界 |
| P2 Control UI 完整 i18n | `ACCEPTED / P2_CONTROL_UI_I18N_ACCEPTED_DEVELOPMENT_SLICE`；现有 handoff、loading、权限、Overview、Modules、确认/变更状态、错误和无障碍标签均接入 `zh-CN` / `en-US`。客户端生成的范围标签、摘要、日期与数字格式使用 locale runtime；服务端错误、事实值、ID、digest 和协议枚举保持原样。静态门禁校验两套 catalog 的 key/插值一致性、直接翻译引用存在性与 JSX/属性硬编码；通过 typecheck、SSR/Modules/i18n 测试、静态策略和 production build。P4 后续可见文案继续要求双语同改 |
| P3 第二真实 Provider | `ACCEPTED DEVELOPMENT SLICE / P3_PROVIDER_CONTRACT_FROZEN_DEVELOPMENT_SLICE / P3_SECOND_PROVIDER_ACCEPTED_DEVELOPMENT_SLICE`；共享 `ModelStreamEventV1` / 有界 accumulator 冻结终态、最后分片、Usage 缺失、超限、取消、迟到分片与 EOF→UNKNOWN。新增默认关闭的 exact 智谱 `zhipu` / `glm-4.5` adapter、seed、受编译信任 artifact 与 runtime Secret resolver；离线覆盖非流式和 SSE 协议、FAILED/UNKNOWN/TRUNCATED，真实实验通过 production composition、FAC2 Current Store 与 Universal Loop 获得 `SUCCEEDED` 及 input/output token 字段。Actions、vision、自动选模、跨 Provider fallback、任意 endpoint/model、用户可见流式 UI、价格/预算均未实现；不代表 `RELEASE_READY`。已进入 P4 只读管理能力，当前下一入口为 `P5_BETA_GATE` |
| P4 模块管理 UI 第一批 | `IMPLEMENTED / P4_MODULE_MANAGEMENT_UI_READ_ONLY_SLICE_IMPLEMENTED`；统一 application/control service 提供 tenant-scoped、bounded 的 Review/Decision/Artifact list/detail；HTTP 复验 scope、权限、projection digest 与 ETag；双语 `#upgrade-reviews` 页面只读展示安全 projection、Decision 与 Admission provenance。无 approve/reject/apply/install/activate/bind/resend/replay；下一批为 UNKNOWN/Backup/Verify/Artifact 查询证据与浏览器验证，不提前开放 mutation |
| P4 模块管理 UI 第二批 | `ACCEPTED DEVELOPMENT SLICE / P4_MODULE_MANAGEMENT_UI_READ_ONLY_EXTENSIONS_ACCEPTED_DEVELOPMENT_SLICE`；新增 tenant/workspace-scoped、bounded 的 UNKNOWN list/detail、Store Verify、Backup constraints 与 server-owned Artifact Admission list/detail 查询，复用 projection digest、strong ETag、scope fence 与 fail-closed error mapping。UNKNOWN 禁止 resend/replay/换 Provider/隐式 retry；Backup UI 不在线创建或恢复；不接受服务器路径、Secret、请求体、签名材料或 replay material；Install/Activate/Bind/Grant/Review/Apply/Execute 继续分离。下一入口为 `P5_BETA_GATE` |
| P5 Beta gate | `TECHNICAL_EVIDENCE_COMPLETE / P5_BETA_GATE`；Windows 全仓、vet、module、事实、文档、许可证、公开树、金额禁入、能力矩阵、前端静态门禁、真实浏览器管理面、Linux 受影响 race 与 Linux ext4 全仓 race 已通过；Windows/Linux AMD64 原生安装及打包后 backup/verify/restore/continue 已在功能基线通过，文档收口后的最终归档与 smoke 复验仍待完成。技术证据完成不自动裁定 `RELEASE_READY`；详见 [`RELEASE_CANDIDATE_2026-09-23`](RELEASE_CANDIDATE_2026-09-23.md) |
| Operator Module Apply | `EXPERIMENTAL`；停机且 Store 独占时可用同一 exact Port/Binding 计划显式 Enable 声明式 Role/静态 Skill、本地确定性只读 Knowledge（含 E5-A governed 版本）、W3-M1 本地有界 Memory、受完全信任的本地 MCP Action、受信 compiled `text.stats` Action、Workspace-owned loopback Channel Endpoint、窄 DeepSeek Model replacement、固定 Document Insight、exact REMOTE Action HTTP 或 exact WASM Action。除 Model 外可按既有合同 Disable；通用表为 9 个唯一 protocol tuple，Document Insight 只通过 2 个优先级更高的 reserved exact selector 复用现有 Context/Action tuple，且必须 Context→Action 分两次 Apply。REMOTE Apply 另需 artifact/endpoint/SecretRef 三项瞬时 exact grant；WASM Apply 只需并必须匹配 `--allow-wasm-action-artifact`。LOCAL_PROCESS、TRUSTED_IN_PROCESS、REMOTE、WASM 四类 artifact grant 互斥。ENABLED module 必须携带非授权的 exact runtime expectation；Model 另需临时 exact SecretRef grant，Manifest permission request 不能自授权。current-only list/inspect、调用方已知 exact pair 的 history、独立 deny-only disable 与只读 dry-run 已接入。history 通过窄 Installation 投影复验 stored Manifest closure，但不读取 artifact root、不输出 Manifest 正文/摘要、不构造 historical PublishedBasis；无 revision 枚举、diff、DISABLED 推导、历史 artifact inspect、热加载、自动升级、第二 Store/Loop/Gateway 或 OS sandbox |
| 广义通用模块装配 | `PLANNED`；公开 Agent/Workspace/Profile 目录、公网或任意第三方 Channel Provider、任意第三方进程内 Action、任意模块组合的通用 multi-Port、同一 PortPlan 多 ProviderBinding/merge/fallback、`knowledge.read` 之外的任意权限语言、跨 Provider/多 Provider Model、R1 exact 合同之外的 REMOTE、R2 exact 合同之外的 WASM/ABI/Host、自动发现/升级、面向不可信代码的强隔离和稳定在线控制面尚未实现；不由本地 v1、W2-E3 loopback Endpoint Apply、W2-E4 窄 DeepSeek replacement、E5-A Require/grant、固定 E5-B Document Insight 双 Port、R1/R2 窄 Host 或 exact history 提前声明可用 |
| ExactAdapter 按需物化 | 已验收开发优化切片；必需 Model eager，可选 exact Adapter 首次使用加载；同键 singleflight、异键并行；可取消构件扫描、backup manifest 分块读取与 MCP cause 保留已闭合，cache 不授予权限或改变 S3 门禁 |

W6-5A 的物理模式合同进一步限定：新 ingress 的同 digest 目标缺失时始终发布为目录 `0700`、文件 `0600`；
既有同 digest 目标只有在完整 bytes/mode 复验后才可复用，且只接受两种 root-global 闭包：全目录 `0700` /
全文件 `0600`；或全目录 `0700`，仅 canonical `LOCAL_PROCESS + mcp-stdio/2025-11-25` descriptor 精确绑定
的唯一 executable 文件为 `0700`，其余文件 `0600`。两者均拒绝 special/setid/sticky 与 group/world 权限。
artifact root 跨 Store 共用时不得依赖任一单独 Store 的 Installation 判断模式；后一种只是既有物理兼容态，
不授予当前 Store Installation、Activation 或 execution authority。这里的 inert 是无 Store authority 且本切片
不执行，不等同于 OS executable bit 必须不存在。

这是一个早期实现候选，不承诺生产 SLA。旧测试名称和历史状态见
[历史能力成熟度矩阵](RELEASE_MATURITY.md)；当前切换状态只由三份长期编码规格与
一次性切换清单的最新验收记录确定，当前能力分组由上述当前能力清单统一索引。

## 开发

```powershell
go test -count=1 -timeout=30m ./...
go vet ./...
```

物理减重后的 S1 树已经封存；S2.1 与 ModelProfile 历史切片各自保留对应证据。包含
RAG、Memory、Action、MCP、Channel 与 Composite 的 S2 历史静止树已重新执行 Windows/Linux
21 包全仓测试、两端 vet、Linux 全仓 Race、完整备份恢复和六平台构建；没有创建或改写
受保护的外部 release-evidence 或 source seal。S3-A 与 S3-B 当前开发切片已完成各自
受影响包的 Windows 测试/vet 与 WSL Race；S3-B 覆盖 APPROVE、REJECT、FAILED、非法输出、
UNKNOWN 零重放、Reviewer-disabled bytes 和完整 backup/restore。W1 的真实 DeepSeek 50 轮、
恢复、精确重入、Usage 与清理已经验收；W3-M0/M1/M2 与 K0/K1/K2A/K2B/K3 已通过 Windows
全仓测试、vet、`go mod verify`、备份恢复和独立复审的集中门禁。上述均为开发验收证据。
六平台构建、Public Stage 和最终 seal 已完成并用于 `v0.1.1` Release；归档、SBOM、
checksum 与 unsigned provenance 仍未签名。更早批次仍只能作为对应历史树的记录。
由于项目从未正式部署，本轮生产切换为
`NOT_APPLICABLE`，而不是伪造一次 GO/NO-GO；首次生产部署仍未批准，详见
[`CUTOVER_ACCEPTANCE`](CUTOVER_ACCEPTANCE.md)。CI 还包含
Windows/Linux 测试、单一 `linux-race` 门禁和多平台交叉构建。每个非
`permanent` 发布任务的 SPDX SBOM、checksum 与未签名 provenance 边界见
[发布供应链输出](RELEASE_SUPPLY_CHAIN.md)。提交前不要把本地数据库、
模型凭据、日志、备份或测试产生的发布证据加入仓库。

贡献约定见 [CONTRIBUTING.md](../CONTRIBUTING.md)，安全问题报告方式见 [SECURITY.md](../SECURITY.md)。
模块作者入口见 [MODULE_DEVELOPMENT_V1](MODULE_DEVELOPMENT_V1.md)。

## 许可证

FreeAgent 使用 [GNU Affero General Public License v3.0 only](../LICENSE)，SPDX 标识为 `AGPL-3.0-only`。

如果你修改 FreeAgent 并通过网络让用户与修改版交互，AGPL 第 13 节通常要求向这些用户提供对应版本的完整源代码获取方式。此说明只是许可证提示，不构成额外条款或法律意见。
