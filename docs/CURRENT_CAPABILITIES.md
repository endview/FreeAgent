# FreeAgent 当前能力清单

> 状态：`CURRENT_CAPABILITY_INVENTORY_V1`
>
> 既有 W5 路线状态：`W5_F1_COLLABORATION_STABILITY_ACCEPTED_DEVELOPMENT_SLICE / W5_COMPLETE_ACCEPTED_DEVELOPMENT_SLICE / W6_NEXT`
>
> 当前 P4 只读范围收口：`P4_MODULE_MANAGEMENT_UI_READ_ONLY_SLICE_ACCEPTED_DEVELOPMENT_SLICE`；公共 model stream contract、UNKNOWN/终态规则与 Secret resolver 边界已冻结，受控智谱 `glm-4.5` 已通过 production composition + Universal Loop 的脱敏真实实验。UNKNOWN、Store Verify、Backup constraints 与 server-owned Artifact/Admission 均已接入只读查询；当前下一入口切换为 `P5_BETA_GATE`。P2 Control UI 完整 i18n 已完成；P1/W6.6 已完成 server-owned Review/Decision 的服务端闭环；P4 只读范围仍不授权自动 install/activate/bind/grant/apply/execute
>
> 当前收口：`W6_5_SERVER_OWNED_MODULE_ARTIFACT_INGRESS_ACCEPTED_DEVELOPMENT_SLICE / W6_6_SERVER_OWNED_MODULE_UPGRADE_REVIEW_ACCEPTED_DEVELOPMENT_SLICE`；W6-5 接纳 inert Artifact/Admission，W6.6 只从 Current Store 与 server-owned Artifact 生成持久 Review/Decision。caller 不提供 artifact path、URL、signature 或 target facts；Review/Decision exact retry、跨 tenant、source/head stale、物理篡改与 backup/restore closure 已验收，且不触发 install/activate/bind/grant/apply/execute
>
> 历史 W6-2/W6-3/W6-4 记录中的 `W6_3_WEB_SHELL_READ_ONLY_OVERVIEW_NEXT`、`W6_4_MODULES_CONFIGURATION_UI_NEXT` 与 `W6_5_SERVER_OWNED_MODULE_ARTIFACT_INGRESS_NEXT` 只表示对应切片收口时的下一入口，现已由上面的 W6-5 accepted marker 取代
>
> 已验收前序里程碑：`W1_COMPLETE_REAL_DEEPSEEK_50 / W3_COMPLETE_ACCEPTED_DEVELOPMENT_SLICE / W4_L4_LEARNING_CYCLE_ACCEPTED_DEVELOPMENT_SLICE / W4_COMPLETE_ACCEPTED_DEVELOPMENT_SLICE / W5_X1_WORKSPACE_TRANSFER_ACCEPTED_DEVELOPMENT_SLICE`
>
> W4-L4 当前边界：`W4_L4_LEARNING_CYCLE_ACCEPTED_DEVELOPMENT_SLICE`；只覆盖显式启用、显式 Tick 的有界模型周期、Store-only 补偿与只读报告
>
> W5-X1 历史验收边界：`W5_X1_WORKSPACE_TRANSFER_ACCEPTED_DEVELOPMENT_SLICE`；只覆盖显式 Decision、最多一次预冻结 repair，以及同 Tenant、双边授权、两种 payload 的受限跨 Workspace 纵链
>
> W5-F1 历史边界：`W5_F1_COLLABORATION_STABILITY_ACCEPTED_DEVELOPMENT_SLICE / W5_COMPLETE_ACCEPTED_DEVELOPMENT_SLICE / W6_NEXT`；只补齐上述协作纵链的公平调度、稳定顺序、整族取消、UNKNOWN 对账、恢复、权限负例和零 Transfer canary
>
> 金额边界：`P0_MONEY_BUDGET_REMOVED_FAC2_BASELINE`；当前运行路径已删除 BudgetPolicy、CostPolicy、PriceSnapshot、费用估算与金额对账，只保留 token usage、输出上限、deadline、权限、UNKNOWN 对账与 exact retry。Current Store 家族改为 FAC2（`github.com/endview/freeagent/current-store-v2`），新运行时只读拒绝 FAC1 旧库，不迁移也不修改。本文件中出现的 `CNY`、估算费用与 PriceSnapshot 字样一律是删除前 FAC1 时期的冻结历史验收证据，不描述当前运行语义；冻结记录见 [`P0_BASELINE_FREEZE`](P0_BASELINE_FREEZE.md)
>
> 日期：2026-09-23

W2-R2 WASM Host 尚未生产可用。

本文件是当前源码树的开发能力状态索引。具体运行语义仍只由
[`CORE_RUNTIME_V1`](specs/CORE_RUNTIME_V1.md) 和
[`CURRENT_STORE_V1`](specs/CURRENT_STORE_V1.md) 定义；W6 控制 API 的跨层合同由
[`CONTROL_API_V1`](specs/CONTROL_API_V1.md) 定义；一次性验收证据仍属于
[`CUTOVER_ACCEPTANCE`](CUTOVER_ACCEPTANCE.md)。本清单不替代编码规格，也不把开发切片
转换为部署或发布许可。

当前版本、Store、migration、包数量与能力 ID/状态的可机械验证投影见
[`generated/runtime-facts.md`](generated/runtime-facts.md)；机器可读形式为
[`generated/runtime-facts.json`](generated/runtime-facts.json)。生成器只投影本清单中人工审定的
能力状态，不自动判断成熟度，也不改写历史验收记录。

当前仓库正在准备 `v0.1.0-dev.2` 本地 Developer Preview 候选。该轨道只包装既有
开发切片，不新增 Runtime feature，也不改变任何 `accepted/planned` 状态；它不是
公开 Release、生产版本、公开 Beta 或 `RELEASE_READY`。功能基线 `ecb5a12`
已生成六平台本地归档及供应链文件（Darwin build-only），Windows/Linux AMD64
原生离线安装和打包后备份/恢复预检通过。Linux 全仓 race 已在 ext4 干净源码上以
exit code 0 完成，53 个 package result、5,166 个测试 PASS、stderr 为空；最终文档
提交后的六平台归档和 Windows/Linux AMD64 原生复验也已通过。此前 Windows 全仓、
vet、module、事实、文档、许可证、公开树、金额禁入、能力矩阵、前端静态门禁、
真实浏览器管理面和 Linux 受影响 race 已通过。未 push、tag、上传、签名或公开发布。
完整证据见 [`RELEASE_CANDIDATE_2026-09-23`](RELEASE_CANDIDATE_2026-09-23.md)。

[`RELEASE_MATURITY`](RELEASE_MATURITY.md) 和
[`capabilities.v1.json`](../testdata/release/capabilities.v1.json) 是旧架构的历史归档，继续保持
`HISTORICAL_BASELINE_NON_NORMATIVE`。它们不再表达当前状态，也不得因为本清单而被回填或
重新分类。

当前 FAC2 Store 为 UserVersion 2、42 表、26 explicit indexes、64 triggers，fingerprint 为
`d5d876f327dc29dc6f4a10476652641172ab8e1f0451a8714fc450f58733541e`。冻结 bootstrap
`internal/currentstore/migrations/fac2/0001_current.sql` 仍为 149,239 bytes、SHA-256
`dbc3e724a1f7c030677c84a77a317f69ef2fe246985cc749559a9f3dd5a6dc5a`；当前追加的
`0002_server_owned_review.sql` 为 7,173 bytes、SHA-256
`3091a49ebcf724f573f91cc0fd22a7c58ebb52fa9d7ed552e32b6526ebeca3cb`。两段 migration
连续执行后的 facts digest 为 `b00a2163ad95ed6b3d35c030a69f2d641fd20b4117ecc811add04dec17904048`。
FAC2 在 FAC1 的 43 表基础上
删除 `model_price_snapshots` 并移除全部金额列；新运行时只读拒绝 FAC1 旧库，不迁移也不改写。
下列 FAC1 及更早身份一律是冻结历史证据。

删除前的 FAC1 Store 为 43 表、25 explicit indexes、64 triggers。W6-3 在 W6-2 的 33 表基础上增加
`run_observation_heads`、`run_observation_snapshots` 与六张 `overview_basis_*` / `overview_resource_*`
不可变 observation 表；W6-5 再增加 `module_artifacts` 与 append-only `module_artifact_admissions`。
该 FAC1 fingerprint 为
`47981b9bf147ec81c6e98382f281db0d5395187a1f090f7ce183c116fba82a0d`，migration 为
150,301 bytes，SHA-256 为
`6def433a59fa8f4876894572f1610cae499abc5b389ab5930ea697055419ca86`。W6-3/W6-4 的历史 41 表
identity 为 `87d63a2e468ee27ee2c0e1918a92e580532825bde85dbb872b9c795bf577f2c1`、143,588 bytes 与
`5bd9743e988b82750f785c015f07c11fbcf6025da70148405aec11106b4efb22`。W6-2 验收记录中的历史原句
“当前 FAC1 Store 为 33 表”仅描述当时在 W2-U3 的 32 表基础上增加 append-only
`control_operation_receipts` 的切片；其 fingerprint 为
`51be9081a815e6379380b93e745db03d33ca2f358962f85ace63bce2534dcc10`，migration 为 67,998 bytes，
SHA-256 为 `8feea38beb505ab4f371afe32eecd877744062bd149d0d24982839c5450fc952`。W2-U3 的历史 32 表
fingerprint `37258c1939308be83f21a109e59a19e32946734d1c53b1b262f876dc36e47dbd`、57,652 bytes 与
`e4f1047eb527b65ab050d444062f3d286cc2ad87d56e60b1cd7f4e96df87652d` 继续只表示 W2-U3、W2-U4、
W6-0、W6-1 与 W6-2 confirmation 当时的冻结身份。W2-U2 在原 24 表上增加
`module_publisher_keys`、`module_sources`、`module_discovery_snapshots`、
`module_discovery_module_refs` 与 `module_discovery_entries` 五张 observation fact 表；W2-U3 再增加
`module_upgrade_candidates`、`module_upgrade_reviews` 与 `module_candidate_decisions` 三张不可变审核
fact 表。W2-U2 的历史 29 表 identity
`6d2bded477e7b1d2c755bf47f5b41f63f1b2f7b568fd72496fc43fbceac3a5f1`、49,972 bytes 与
`d2bcc27bff2e17a058165f7c544b0c97cd1c99efca3264ab401631477611263e` 继续只表示 U2。W2-R2 的
24 表旧 identity `7d2e0850a0253a630d5e2264c720b10fbac3ad0fdb6f9e53fcd722c2dd2615a8`、
44,257 bytes 与 `2520299390885b4c23df85d2ff217629f6a458c368735e5bdc0df2eca37b8f2f`，W2-R1 的
24 表旧 identity `dcad8f8837ecc5832f444acbc2680a2c2debff5161319710f60e7f649e4aede5`、
44,237 bytes 与 `fd7270130b5b13e5dfe9e3bb0a3bc934d46eb0c3b4a0647b1a3fddd81773e665`，以及 W5-F1/W5-X1 的
24 表旧 identity `98d658e68907f77516ce0366a7b08bfa3851600326850590ed79584f581fa588`、
44,215 bytes 与 `0a6701405e471e1ee7f41ea89ed62b50e5a4723e71b320e50f073cdd3e6b727a`
继续只表示对应历史切片。W4-L4 的原 identity
`9941c957b0e6a1a1e1770b38cd06605025d12df30067d1271fe8f7b30f2f0518`、44,138 bytes、
`4cf260d368fb7b69e0a86dad99ff593bea216e9d0da341f607d000999e076be4`，W4-L3 的 22 表以及
更早 19/20/21 表切片的旧摘要只属于对应历史树，不因本段而被改写或升级验收状态。

当前 W1 已完成真实 DeepSeek 50 轮、恢复、精确重入、Usage 与清理验收；W3 已完成
M0/M1/M2 与 K0/K1/K2A/K2B/K3 的本地 RAG/Memory 实用化开发纵链及集中门禁。这里的
`accepted` 仍只表示当前范围的开发切片收口。W4-L1A/L1B 已闭合 Knowledge 与静态 Skill
Draft 的 Proposal Store；W4-L2 的独立 Reviewer 普通 Run/Model Attempt、审核状态投影与
UNKNOWN 对账边界已完成开发切片验收；W4-L3 的无权限不可变 Version、全局 ModuleRef 门禁、
安全 artifact 交接和备份恢复也已收口；W4-L4 又闭合了默认 24 小时间隔、显式启停和 Tick、
Store-only 补偿、只读周期报告及备份语义门禁。这里的周期不会自动发布、安装、激活、绑定或
扩权，也没有常驻 Worker、Queue 或后台 daemon；它不表示生产部署、公开 Beta 或远程向量库
已经完成。W5-X1 又闭合显式 Decision、`2N+3` 物理 Run 图、最多一次 repair、双边 Workspace
grant、受限 REQUEST/RESULT transfer、真实 Usage 和完整 Backup。W5-F1 追加持续负载公平性、
唯一 Scheduler 纵链、稳定归位、family 取消、UNKNOWN 原 Attempt 对账、恢复与权限失败关闭证据。
它仍没有自动 Reviewer、无限返工、动态图或任意 payload；项目从未正式部署，不是公开 Beta，
也未达到 `RELEASE_READY`。W2-E4 另行闭合了同一内建 DeepSeek 构件内 flash/pro 显式替换、
跨 Workspace/Profile、token Usage、Backup/Restore 与失败关闭矩阵；它没有开放自动选模、成本路由、
跨 Provider、多 Provider 或通用 Model 插件。W2-E5-A 与 E5-B 又分别闭合 governed Knowledge 的
Requires/grant 和固定 Document Insight 双 Port 产品纵链；两者都没有把 Operator Module Apply
提升为稳定公共合同，也没有开放任意第三方代码或广义通用装配。W2-R1 随后只把一个窄
REMOTE Action 协议接回同一 Apply、Catalog、Gateway、Attempt、恢复与 Backup 链；它没有建立
第二 Runtime、Store、Loop、Gateway、网络账本或通用远程插件系统。W2-R2 再把一个 exact WASM
Action 协议接回同一纵链，同时保持默认关闭、零宿主能力和有界本地执行；它不提供面向不可信代码的强
模块隔离。W2-U2 随后只在同一 Current Store 和 Backup 中加入显式来源观察事实；Source Provider
不能写 Store，Pure Chat 也不会扫描来源或联网。W2-U3 再加入 inert Candidate/Review/Decision 与
target Manifest content evidence；它只做离线审核。W2-U4 把其中一个 exact APPROVE 重接到既有
Apply/CAS，但只允许单个 Declarative Profile Context 原位替换，不开放自动升级或广义装配。

W6-0 历史切片冻结了 `control-session/v1`、`control-scope/v1`、`control-view-snapshot/v1`、
`control-operation-request/v1`、`control-operation-receipt/v1` 与 `control-event-cursor/v1` 六份
纯 canonical 合同。Web 视图使用独立的 `control-view-snapshot/v1` 与
`freeagent.control-view-snapshot/v1` 摘要域；Core 已有的 `control-snapshot/v1` 与
`freeagent.control-snapshot/v1` 不变。Operation request 显式区分 `DRY_RUN/MUTATE`，动态 request ID
和时间不进入 semantic request；`UNKNOWN` 只能返回 exact receipt，禁止重放。该切片没有 listener、
handler、session store、SSE、UI、production command wiring、Schema 或 receipt table；当时证据是 39 份
Markdown、37 个源码 package、35 个 production dependency-closure package 和 32 表 Store。

W6-1 已验收默认关闭的本地 Control surface：同一 `freeagent serve` 显式启用后，以 Chat listener 加独立
`tcp4 127.0.0.1:0` Control listener 共享唯一 Store、Application Services、startup-closed Admission 与
关停生命周期；owner-only handoff 交付 actual origin 和一次性 bootstrap，交换后 session 仅驻本进程。
W6-1 当时只提供 scope-filtered Modules list/detail 与 exact If-Match 的 `MODULE_DISABLE` Dry-run。Dry-run
effect-free，其 `DRY_RUN` receipt 只是响应事实，不是 durable receipt；既有停机 CLI Apply/Disable 是相邻
既有路径，不是 Control mutation。该历史原子当时的证据为 39 份 Markdown、44 个源码 package、44 个
production dependency-closure package，Store 仍为 identity 不变的 32 表。

W6-2 audit 的历史收口为 `W6_2_CONTROLLED_MUTATIONS_AUDIT_ACCEPTED_DEVELOPMENT_SLICE /
W6_2_CONFIRMATION_CONTRACT_NEXT`。六项 operation 中，首候选仅为 TENANT/PROFILE、exact
`context.provide/v1`、`OPTIONAL`、`DECLARATIVE`、trusted-instruction config、deny-all Authority 的
`MODULE_DISABLE`；Upgrade Review 的 caller-owned artifact path/signature、Learning 的 reservation/异步
Attempt/Task/Run、Upgrade Apply 与 Module Apply 的 staging/install/activation/grant 长链使其余五项 deferred。

W6-2 confirmation 历史原子新增稳定 `control-confirmation-statement/v1`、Request 的
`operation_evaluation_digest`、`control-module-disable-evaluation/v1` 与 process-local proof registry。
Proof 固定 32 random bytes、TTL 最多 2 分钟且不超过当前 session absolute expiry、全局最多 256、每 session 最多 8；registry 仅保留
domain-separated proof digest，并绑定当时 Boot/session/principal/auth revision/scope-set/capability/scope/
Statement/Request/expiry/state，不进入 Store、Backup 或日志。当时证据为 39 份 Markdown、
45 个源码 package、44 个 production dependency-closure package；当时 FAC1 Store 为 32 表。没有 durable receipt row、
mutation route 或 Schema/Backup 变化，纯 SQLite 首片 `UNKNOWN` 不可达。

durable receipt Schema 历史原子又冻结共享 `module-apply-plan/v1`、严格
`module-disable-publication-receipt/v1` 与 full PublishedBasis canonical。唯一新表以
principal + scope + operation + idempotency-key digest 为 identity，保存 exact Request/input/plan/
evaluation/Control receipt/pre-post basis；`APPLIED` 形状还要求独立 domain receipt。公开 Store 写入口只
允许重放验证后的 `NO_CHANGE`，exact retry 返回原行，同 identity 不同 Request 冲突；该历史原子没有公开
`APPLIED` insert，其下一 wiring 义务要求随 pointer publication 同事务写入。现存行 semantic verifier 会从不可变 Control/Catalog
parents 重放 neutral evaluator，并已接入 Backup Create/Verify/Restore 的 coherent snapshot 检查。因为单表
没有外部 completeness anchor，verifier 只验证现存行，不宣称能发现整行删除。该历史原子的证据为 39 份
Markdown、47 个源码 package、46 个 production dependency-closure package 和 33 表 Store；当时没有
mutation route、handler 或 confirmation endpoint。当前 wiring 收口见下述 `control-plane.online` 行。

## 状态含义

- `accepted`：满足当前范围内的本地消费者、失败边界和恢复门禁；只表示开发切片收口。
- `experimental`：入口与实现已经存在，但支持范围或公共合同仍可能变化。
- `unverified`：实现或专用路径已经存在，但本阶段要求的完整产品链或外部证据尚未闭合。
- `planned`：只有需求、设计或后续工作入口，当前不能作为可用能力调用。

## 当前清单

当前源码树的权威能力清单共 42 项；这不是历史发布 Capability Matrix 的 49 项，也不改写其归档。

| ID | 能力 | 状态 | 当前边界与证据 |
|---|---|---|---|
| `core.unique-runtime-store` | 唯一 Runtime、Assembly Compiler、Universal Loop、Current Store 与 Gateway | `accepted` | 生产依赖闭包排除旧 Runtime/Store；见 [`CORE_RUNTIME_V1`](specs/CORE_RUNTIME_V1.md) 与 [`CURRENT_STORE_V1`](specs/CURRENT_STORE_V1.md) |
| `core.pure-chat` | 本地 Pure Chat | `accepted` | 零可选模块可启动；CLI、loopback HTTP、重入与恢复进入同一 Core/Store |
| `core.context-compiler` | 85% 压缩、持久摘要复用与 100% 最小有序移除 | `accepted` | 使用唯一 Context Compiler；`>=85% 且 <100%` 可复用直接成功前驱中经验证的连续最旧 History 前缀摘要并重新计算当前 token，`>=100%` 仍只 Drop 恢复水位所需的最小完整旧前缀；不清空持久历史 |
| `core.model-profile` | 精确 ModelProfile | `accepted` | 只收紧 ContextPolicy，不自动选模或切换 Provider；W2-E4 可由 Operator 在窄 DeepSeek replacement 中显式设置或清除 exact Profile |
| `module.rag-local-readonly` | 本地共享授权 RAG | `accepted` | 可选标签路由、全 Binding 权限预检、独立 `NOT_SELECTED` shortcut 与严格 exact-question reuse 已闭合；复用条件不满足或知识 revision、权限、TTL、Memory head/证据变化时回退 fresh，真实命中 reuse 时 Knowledge Provider 零调用；远程向量库与知识更新不在本切片 |
| `module.memory-local-bounded` | Agent 轻量 Memory | `accepted` | 通过统一 Module Apply/Dry-run/Disable 装配；空 Head 只创建一次 Genesis、已有 Head 原样保留；同步、有界、按 Agent/Workspace 可见性隔离，大类/高频词计数只作复用门槛，`TASK_SUMMARY` 不冒充 Conversation Summary；后台学习不在本切片 |
| `core.action-gateway` | Action Proposal、Attempt 与 Gateway | `accepted` | 本地 `text.stats`、W2-R1 原生 REMOTE Adapter 与 W2-R2/R3 原生 WASM Adapter 均只能在原 Attempt 已持久化为 PENDING 后由唯一 Gateway 调用私有执行入口；W2-R3 在 lazy resolve 前和真正执行前各做一次 current Activation deny-only 检查，第二次成功是 execution-admission linearization point；FAILED/UNKNOWN 只收口原 Attempt，模型不能直接调用 executor |
| `extension.mcp-local-stdio-tool` | 本地 MCP stdio Tool | `accepted` | 仅 Operator 完全信任的精确构件、`2025-11-25`、Tool-only；没有 OS sandbox |
| `runtime.remote-action-http` | 窄 REMOTE HTTPS Action Host | `accepted` | `W2_R1_REMOTE_ACTION_HOST_ACCEPTED_DEVELOPMENT_SLICE`：只支持 `action.provider/v1 + REMOTE/freeagent-action-http/v1`。Apply 需要 exact artifact、HTTPS endpoint 与 SecretRef grant；runtime 默认关闭并要求显式 SecretRef→环境变量映射。描述与 Prepare 离线，只有唯一 Gateway 在原 PENDING Attempt 后执行一次 POST；无 proxy、redirect、retry、fallback 或发现，公共地址策略拒绝内网与 special-use 目标，歧义进入原 UNKNOWN。没有真实第三方互操作、WASM 或不可信本地隔离声明 |
| `runtime.wasm-action-host` | 窄 WASM Action Host | `accepted` | `W2_R3_UNTRUSTED_MODULE_ISOLATION_ACCEPTED_DEVELOPMENT_SLICE`：在 R2 exact 协议上允许非 builtin 第三方纯计算模块。Apply/Dry-run 仍要求 exact artifact grant；runtime 必须同时显式设置 `--enable-wasm-actions` 和至少一个重复型 `--allow-wasm-runtime-artifact <sha256>`，非 canonical、重复或非当前 Catalog digest 均失败关闭。wazero v1.12.0 interpreter、Core v1/wasm32、零 imports/WASI/Host Module/network/filesystem/Secret 及 R2 资源上限不变。停机 Disable 只撤销后续 Admission，历史 Run/Attempt/Activation 不改写；第二检查前撤权 guest=0，第二检查后已接纳的 `Effect=none` guest 可在既有 5 秒上限内收口。不是 OS/container、热撤权或生产恶意多租户隔离 |
| `channel.loopback-workspace` | Workspace loopback Channel | `accepted` | 默认关闭；统一 Workspace/Endpoint Apply 原子提交 Control/Catalog 与 revision-0 Cursor seed。Cursor、去重、Attempt、无重定向和恢复均进入同一 Store/Gateway；W2-E3 集中验收已闭合双 Workspace 隔离、duplicate 零重发、Endpoint-scoped UNKNOWN 与恢复后续推 |
| `agent.composite-depth1` | Parent、Specialist 与固定 merge | `accepted` | legacy S2 能力：同 Workspace、depth-1、2 至 8 个 Specialist、固定 `ALL_REQUIRED`；不冒充当前 W5 总能力 |
| `scheduler.local-fair` | 本地多 Workspace 公平调度 | `accepted` | W5-F1 默认关闭、tenant-bound、本地进程内且不读取任务正文；3 个始终 runnable Workspace 完成 900 次 claim，第 450 次后重开 Store，最终各 300 次；首次服务不晚于第 3 次，最大服务间隔不超过 3。Decision approve、单槽 repair 与 Transfer 走唯一 Scheduler，dormant/skipped repair Run `Attempt=nil` 且零 claim；这是有界开发证据，不是生产公平 SLA |
| `agent.reviewer-results-gate` | 单次 Reviewer 审核门 | `accepted` | legacy S3-B 能力：可选 `RESULTS_GATE`；不包含 W5-X1 Decision repair，也无自动 Reviewer、无限返工或动态图 |
| `runtime.exact-adapter-lazy` | ExactAdapter 按需物化 | `accepted` | 完整 exact key、同键合并、异键并行、失败不缓存；W2-R1 production loader 逐次重验 REMOTE artifact digest/covered size/descriptor；W2-R2/R3 loader 逐次重验 WASM artifact/descriptor/ABI 后才构造 Adapter。WASM Adapter cache 只保存 ModuleID、Version、ArtifactDigest、ExecutionClass 与 AdapterIdentity，不缓存 InstanceID、ActivationRevision、Tenant、Workspace、Config 或 Authority；权限仍由 Gateway 的 exact Activation/Binding 闭包决定 |
| `core.complete-backup-recovery` | Current Store 完整备份与恢复 | `accepted` | Store、引用构件、Channel Cursor、UNKNOWN、Knowledge reuse、Memory revision、Context Summary、Learning Proposal/Version/Schedule/Task，以及 W5-X1/F1 Decision/repair、双边 grant、Transfer payload/envelope 和终态进入同一 bundle 闭包。F1 完整 Decision+Transfer 已通过 backup→verify→restore→reopen；跨 Workspace PENDING 恢复为同一 Attempt UNKNOWN，Loop 语义重放为 0。Conversation compiler-owned model-1 会由冻结事实确定性重编译并逐字节核对请求与 Compilation，Learning 与 Transfer 事实均由只读语义验证器重建校验。W2-E4 要求 Backup current semantic gate 重验活动 DeepSeek Binding 的 Price/Authority/Profile closure；W2-E5-A 又重验 governed Knowledge 的 exact Manifest shape、同 Profile Model Require 与窄 `knowledge.read` grant；W2-E5-B 继续逐字节保留同一 Document Insight Run 的 RAG、Action 链与 MemberSnapshot；W2-R1 再保存 exact REMOTE Provider、Binding endpoint/SecretRef identity、Action closure 与原 Attempt 终态，恢复不解析 Secret、不联网也不重放 UNKNOWN；W2-R2 逐字节保存 exact WASM descriptor/module/Action closure，Backup/Verify/Restore 不编译、实例化或执行 guest，恢复后 UNKNOWN 仍不重放 |
| `core.explicit-schema-migration` | Current Store 显式前向迁移链 | `accepted` | `0001_current.sql` 保持字节冻结；当前 FAC2 为 UserVersion 2，W6.6 通过真实 `0002_server_owned_review.sql` 增加 Review 的 Admission/operator/request-digest 关系投影。`migrate` 在同一单写者 lease 内强制先创建并验证完整 backup，再执行连续前向步骤；普通 Runtime open 不迁移，未知/未来版本失败关闭；restore 只在私有 staging 副本上迁移后再原子发布；见 [`STORE_MIGRATIONS`](STORE_MIGRATIONS.md) |
| `core.effect-ledger-usage` | 外部效果账本与 token Usage 语义 | `accepted` | 模型、Action、Channel 先建 Attempt；Decision family 的 `2N+3` 表示物理 Run 图和 dispatch cap，不等于调用数，未激活 repair Run 保持 `Attempt=nil` 且不伪造零 Usage。当前 FAC2 运行路径只记录 token usage，不再记录估算费用、Provider 报告费用或对账费用。X1D 历史样本（删除前 FAC1 证据）为 7 physical Runs/4 Attempts/3 nil repair Runs，input 3,443、cached 768、uncached 2,675、output 1,353、估算 `0.00539636 CNY`。F1 Test-0808 独立历史样本为 4/4 HTTP 2xx、exact retry 新 HTTP/Attempt 0、input 3,149、cached 768、uncached 2,381、output 1,492、2/4 请求命中、token 加权 `24.388695%`、估算 `0.00538036 CNY`；两者 reasoning 与 Provider reported/reconciled cost 均 UNKNOWN，且这三个金额字段已随 P0 从合同与 Store 中删除 |
| `module.package-conformance` | Module Package Conformance | `experimental` | `module-verify` 无 supply flag 时保持 legacy 输出/错误逐字兼容；任一 supply flag 显式进入只接受 `LOCAL_DIRECTORY + DENY` 的 governed observation。该路径复验外置 exact Policy/Key/Signature IDs、Ed25519、点分段 Module ID prefix、Policy 收紧的首轮/最终 package scan 和调用期 immutable revocation deny snapshot。成功仍只输出 `freeagent.module-package-verification/v1`，不产生 reservation、grant、staging、Store fact 或 Apply authority |
| `module.supply-contracts-v1` | 模块签名、来源、发现与候选纯合同 | `accepted` | `W2_U0_SIGNING_SOURCE_CONTRACT_ACCEPTED_DEVELOPMENT_SLICE`：在 `sdk/moduleapi` 冻结 `module-publisher-key/signature/source-policy/discovery-index/discovery-snapshot/upgrade-candidate/candidate-decision` 七份 exact canonical wire。Ed25519 只签域分隔 ArtifactDigest；Source Policy 为本地 Operator 约束；Version 为不透明 exact string；Snapshot/Candidate 有 parent/content-ID 与跨刷新 review-key 闭包。所有 wire 有 unknown-field、canonical/content-ID canary、defensive-copy 和签名负例。该 U0 能力不联网、不改 24 表 Store（即当时的 Store）、不扫描/安装/Apply/执行模块，也不授予 Trust/Authority/Effect；U1/U2 后续接线不改写本行的 U0 历史范围 |
| `module.discovery-snapshot-v1` | 显式 Source observation 与不可变 Snapshot | `accepted` | `W2_U2_DISCOVERY_SNAPSHOT_ACCEPTED_DEVELOPMENT_SLICE`：`module-source-register`、`module-source-refresh`、`module-publisher-key-revoke` 均要求 `--enable-module-discovery`。Local 只读固定 `root/index.json`；HTTPS 另需显式开关与 exact HTTPS origin allowlist，并禁止 proxy、redirect、retry、HTTP/2、keep-alive、cookie、凭据和 special-use 地址。Store 在 Source I/O 前后以 exact revision/CAS 重验 Source Policy 与全局不可逆 Publisher Key revocation，冻结 Index/Snapshot parent、跨来源 ModuleRef→ArtifactDigest 唯一性与安全 `entry_count` 投影。该 observation-only 能力不下载包、不验证 entry signature、不生成 Candidate、Decision 或 Apply；Backup/Verify/Restore 零网络、零来源读取、零包获取 |
| `module.artifact-ingress-v1` | server-owned content-addressed module artifact ingress | `accepted` | `W6_5_SERVER_OWNED_MODULE_ARTIFACT_INGRESS_ACCEPTED_DEVELOPMENT_SLICE`：唯一入口是默认关闭的可信本地 Operator CLI `module-artifact-ingress`。`--source-root` / `--artifact-root` 只在进程内使用；caller 仅选择 Store-owned current Snapshot 的 unsigned `LOCAL_DIRECTORY + DENY` exact entry，不能提供 package path、URL 或 signature。完整 conformance/digest/size/file-count 验证后，filesystem durable-first 地以 hidden stage、sync、digest-addressed no-replace 方式 durable publish；唯一 Current Store 随后在一个 `BEGIN IMMEDIATE` 中重验 basis 并提交不可变 `module_artifacts` 与 append-only `module_artifact_admissions`。对象 inert，不 Install/Activate/Bind/grant/Review/execute；无 HTTP/upload/UI。Backup artifact set 是 installation ∪ ingress 的去重并集，支持 Source removed、零 Installation 的 offline restore。W6-5 的历史 FAC1 identity 保持冻结，当前 FAC2 identity 由 P0/W6.6 事实记录维护 |
| `module.upgrade-review-v1` | 离线 exact-version Candidate、Review 与 Decision | `accepted` | 保留 `W2_U3_UPGRADE_REVIEW_ACCEPTED_DEVELOPMENT_SLICE` 的 caller-owned 历史入口；新增 `W6_6_SERVER_OWNED_MODULE_UPGRADE_REVIEW_ACCEPTED_DEVELOPMENT_SLICE` 的默认关闭 `module-upgrade-review-server-owned` / `module-upgrade-decide-server-owned`。新入口只接受 Tenant/scope/Admission/Review identity、operator 和 reason 等精确输入，从 Current Store 持有的 Admission、canonical Manifest 与 server-owned Artifact 复建 basis；不接受 artifact path、URL、signature bytes 或调用方 target facts。Review/Decision 持久化并 content identity exact retry；错误区分 Admission 不存在、Artifact 篡改、Tenant/Review 冲突、Source stale 与 Review integrity。跨 tenant、source/head stale、物理 Artifact 篡改和 backup/restore 失败关闭；Review/Decision 不调用 Provider，且不 Install、Activate、Bind、grant、Apply、Execute。 |
| `module.upgrade-apply-v1` | 审批后 exact Declarative Profile Context 原位替换 | `accepted` | `W2_U4_OPERATOR_APPLY_ACCEPTED_DEVELOPMENT_SLICE`：`module-upgrade-apply` 默认关闭，必须显式绑定 exact Tenant/Review/APPROVE Decision，并只接受 `LOCAL_DIRECTORY + DENY` 的显式 `--source-root`；不读取 Index `PackagePath`。首片要求全部 grant flags 显式出现且为空，只接一个 PROFILE `context.provide/v1`、DECLARATIVE `static/v1`、`TRUSTED_INSTRUCTION`、deny-all Authority 的 BindingImpact。历史 U1 验证→current Store 重验→同一 exact plan→current U1 验证→pre-stage 后，复用唯一 Apply/CAS 将原 ordinal 原位替换；其他 Binding 字节与顺序不变。旧 Run 继续冻结旧 Provider，新 Run 使用 target。Model、Channel、Action、`SINGLE`、shared-current 与 fanout 失败关闭。Review/Apply 复用同一 evaluator，不虚构独立 dry-run admission；只有相邻 current exact retry 返回原结果，不建立 durable receipt，后续 publication 后返回 `POINTER_CONFLICT`。UNKNOWN 原样保留且禁止重放；Backup、Pure Chat 零访问与 32 表 Store 不变 |
| `module.assembly-local-v1` | 本地显式模块装配 v1 | `accepted` | `W2_D_LOCAL_ASSEMBLY_ACCEPTED_DEVELOPMENT_SLICE`：声明式 Role/静态 Skill、本地只读 Knowledge、W3-M1 已验收的本地有界 Memory 与 Operator 完全信任的本地 MCP Action 已在同一 Store/Catalog/Assembly 完成集中纵链；逐个 Catalog Entry 闭包和 unbound `module-inspect` P1 回归已通过，Windows 全仓 188.9 秒、WSL2 ext4 有效 START/COMPLETE 全仓 128.1 秒、vet/mod/gofmt、License 35/55 与 `PUBLIC_TREE_PASS` 已闭合。accepted 只限该窄本地 v1 |
| `operator.module-apply` | Operator Module Apply v1 | `experimental` | 停机态已支持统一 exact Apply、current-only `module-list`/`module-inspect`、调用方已知 exact pair 的 `module-history`、独立 deny-only `module-disable` 与只读 `module-dry-run`。Core 当前有 9 个唯一协议 tuple：第 8 个选择 exact REMOTE Action 并要求 artifact/endpoint/SecretRef grants，第 9 个选择 exact `action.provider/v1 + WASM/freeagent-action-wasm/v1 + action-binding-config/v1` 并要求 `--allow-wasm-action-artifact`。LOCAL_PROCESS、TRUSTED_IN_PROCESS、REMOTE、WASM 四类 artifact grant 互斥。Document Insight 仍通过两个固定摘要 selector 复用现有 Knowledge/Action tuple。U4 只在既有 evaluator/Apply/CAS 上增加一个 approved Declarative Profile Context exact replacement seam，不扩张其他 handler。Manifest request 不能自授权，history 保持原只读边界。入口仍是 `experimental`；任意模块的通用 multi-Port、同 Port 多 Binding、任意第三方进程内 Action、R1/R2 exact 合同外的 REMOTE/WASM、热加载、自动升级或第二控制器未完成 |
| `module.workspace-channel-apply` | Workspace Channel Endpoint 统一 Apply | `accepted` | `W2_E3_WORKSPACE_CHANNEL_APPLY_ACCEPTED_DEVELOPMENT_SLICE`：ENABLED 原子发布 Control/Catalog 与 revision-0 Cursor seed；Dry-run 零写、零 Secret、零网络，exact retry 复验原始 seed；Disable 保留 Cursor/Attempt/history/evidence，共享 Instance 仅在最后引用消失时退出当前 Catalog。双 Workspace/Endpoint 的 Cursor、Run、History、duplicate、UNKNOWN 与 Backup/Restore 隔离已闭合 |
| `module.deepseek-model-replacement` | DeepSeek Model 显式替换 | `accepted` | `W2_E4_DEEPSEEK_MODEL_REPLACEMENT_ACCEPTED_DEVELOPMENT_SLICE`：仅支持 `freeagent.builtin.model.deepseek@2.0.0` 同 Artifact/Adapter/Provider Instance 内 `deepseek-v4-flash`↔`deepseek-v4-pro`。Operator 显式 Apply/Dry-run/CAS 必须闭合 exact Config、`model-authority-ceiling/v1`、临时 exact SecretRef grant，以及可选且 exact 的 ModelProfile；省略画像表示清除。Model Disable 禁止，回滚使用新的 ENABLED Apply；只影响新 Run，旧 Run 保持冻结。UNKNOWN 不换模型、不建替代 Attempt、不语义重放。Store direct publication 与 Backup current semantic gate 均重验 Authority/Profile closure；跨 Workspace/Profile、token Usage、Backup/Restore 和负例矩阵已闭合。零新增 Schema/表/Runtime/Loop/Gateway/账本。原 E4 验收要求的 PriceSnapshot 闭合已随 P0 删除，其历史证据只保留在 FAC1 冻结记录中 |
| `module.document-insight-dual-port` | 固定 Document Insight 双 Port 产品模块 | `accepted` | `W2_E5_B_DOCUMENT_INSIGHT_DUAL_PORT_ACCEPTED_DEVELOPMENT_SLICE`：固定 `freeagent.builtin.document-insight@2.0.0`、Artifact `9cf2e60f4b6d30cfd93ea93245f4a6eadbd4f365b93f06b7decb389c4f4d4bfa`、Adapter `freeagent.adapter.document-insight/v1`，有序提供 `action.provider/v1` 与 `context.provide/v1`，要求唯一 `model.generate/v2` 并请求窄 `knowledge.read`。同一首个 Model Compilation 同时冻结真实 RAG retrieval 与精确 Action result reservation，随后经唯一 Gateway 形成 2 个 Model Attempt 和 1 个 Action Attempt；两个 PortPlan 中由 Document Insight 贡献的两个 Binding 及 Action Attempt 使用完整相同 `ActivatedModuleRef`，Context PortPlan 另保留 `context.basic`。Context→Action 两步 Apply、CAS、exact retry、Backup/Restore 与 Action→Context Disable 顺序已闭合。实现编译进 Core，包只携带不可变 JSON；不代表任意第三方代码、通用 multi-Port 或同 Port 多 Binding 合并 |
| `provider.deepseek-s3c` | DeepSeek 真实模型专用路径 | `unverified` | 历史 Pilot 停在 `3 COMPLETE + 1 PARTIAL` 和 4 个 `MODEL_UNKNOWN`；S3-C 未通过，不能推广为普通产品 Provider |
| `provider.deepseek-controlled` | 受控 DeepSeek 正常 Model Provider | `accepted` | 仅由显式 `--enable-deepseek` 启用，固定官方 endpoint，Secret 只在 dispatch 解析；production-adapter mock E2E 与 W1 真实 50 轮已闭合 POST、Usage/cache/reasoning UNKNOWN 与 UNKNOWN 零重放；不开放任意 OpenAI-compatible URL |
| `provider.zhipu-controlled` | 受控智谱 GLM Model Provider | `accepted` | P3 默认关闭的 exact `zhipu` / `glm-4.5` / compiled official endpoint；Secret 只在 dispatch 解析并清除。非流式与 SSE 协议 fixture、公共有界 accumulator、FAILED/UNKNOWN/截断终态和 production composition + Universal Loop 脱敏真实实验已闭合；不支持 Actions、自动选模、跨 Provider fallback、任意 endpoint/model 或用户可见流式 UI |
| `product.conversation-config` | Pure Chat Conversation | `accepted` | CLI create/get/chat 与 loopback HTTP 已接入 exact revision/head、服务重启、turn-25 backup/restore、正常 OS 进程 25+25 轮和真实 DeepSeek 50 轮；50/50 首次 Attempt 成功，精确 retry 不增加模型调用。统一验收又证明已安装但未选择的 Knowledge、Memory、Skill、MCP/Action、Channel、Learning 与 Team 零解析、零请求暴露、零可选状态增量。仅验收普通单 Agent 单模型 Pure Chat；Composite 与 Conversation 明确互斥，Conversation+Action/Channel 不在本切片成熟度声明内 |
| `module.general-assembly` | Agent、Workspace 与广义通用模块自由配置 | `planned` | 该范围不同于本地显式装配 v1、W2-E3 loopback Endpoint Apply、W2-E4 窄 DeepSeek replacement、E5-A Require/grant、固定 E5-B Document Insight 双 Port、R1/R2 窄 Action Host与 U4 单一 approved Context replacement：公开 Agent/Workspace/Profile 目录、公网或任意第三方 Channel Provider、任意第三方进程内 Action、任意模块组合的通用 multi-Port、同一 PortPlan 多 ProviderBinding、`knowledge.read` 之外的任意权限语言、跨 Provider/多 Provider Model、其他 REMOTE 协议、R2 exact 合同外的 WASM/ABI/Host、自动发现/升级、面向不可信代码的强隔离和稳定在线控制面尚未实现；窄 accepted 切片不把这些广义能力提前变为可用 |
| `knowledge.proposal-store` | W4 Learning Proposal Store 与有界审核 | `accepted` | W4-L1A/L1B 在唯一 Current Store 和同一 `learning_proposals` 表闭合 Knowledge/静态 Skill Proposal、Tenant+kind 三轴去重、权威 proposer lineage、exact retry 与 backup semantic gate；W4-L2 独立验收同表 review refs/state/revision、Reviewer 普通 Run/Attempt/Usage/MODEL_RESULT/UNKNOWN 与 Store 派生终态。真实 DeepSeek 验收为 `APPROVED/2`、Attempt `SUCCEEDED`，Usage 落账且 exact retry 零重放 |
| `knowledge.materialized-version` | 审核后不可变 Knowledge/Skill Version 与 Operator 交接 | `accepted` | W4-L3 继续复用同一表，以严格 0/7 投影保存 `APPROVED/2` 或 `APPROVED/3` 的 canonical Version、APPROVE Verdict digest、ArtifactDigest/size 和时间；跨 Tenant/kind 的全局 ModuleRef 唯一，Installation↔Version 两种顺序都只接受 exact Manifest/ArtifactDigest。`learning-materialize` 只导出可重建两文件包；安全 exact retry、并发发布、module-verify/dry-run、完整备份恢复与恢复后重导出已通过。专属 E2E 又证明 stale CAS 零发布、Operator exact Apply 后仅新 Run 冻结并消费审批 Version；不自动生成 Apply plan、安装、激活、绑定或扩权 |
| `knowledge.learning-cycle` | 有界 Learning PR 与 24 小时逻辑周期 | `accepted` | W4-L4 的新 Schedule 默认关闭；省略间隔时冻结为 86,400 秒，且必须以 exact revision 显式启用。`learning-cycle-tick` 必须指定 exact Tenant/Schedule 与显式 UTC `--observed-at`，每个到期窗口只经现有 Universal Loop 建立一个普通 Model Run/Attempt/Usage；模型只能返回 `NO_CHANGE` 或提交同 kind 的有界 Proposal，同窗重试不重放。`learning-cycle-reconcile` 仅补齐现有 Store 投影，不创建 Run/Attempt 或调用 Provider/Loop/Gateway/Secret/网络；`learning-cycle-report` 只读生成不持久化的半开窗 canonical 报告，单次上限 1,024 项并拒绝溢出。没有常驻 Worker、Queue 或后台 daemon，也不自动 Apply、安装、激活、绑定或扩权 |
| `collaboration.decision-repair-bounded` | 显式 Decision 与一次有界修复 | `accepted` | W5-X1 预冻结 `2N+3` 个物理 Run，最多激活一次 repair；只激活受影响槽位，其他 repair Run 明确跳过且 `Attempt=nil`。W5-F1 已验证 approve、单槽 repair 和 Transfer 的唯一 Scheduler 纵链、dormant/skipped 零 claim、完整跨 Workspace family 取消后零新 Attempt/payload/envelope；没有自动 Reviewer、无限返工或动态图 |
| `collaboration.cross-workspace` | 受控跨 Workspace 协作 | `accepted` | W5-X1 只允许同 Tenant、root/target 双边 grant；每个 Run 仍只绑定一个 Workspace。v1 仅传 Store-loaded `TASK_INPUT` 的确定性有界 `TASK_SUMMARY` 请求与 `SPECIALIST_RESULT` 返回，不传完整 History、Memory、知识正文、SecretRef 或任意 payload。X1D 的 4/4 HTTP 2xx、REQUEST/RESULT transfer 与 exact retry 零新 HTTP/Attempt 保持为历史证据；F1 追加 frozen plan 稳定归位、reopen 字节/身份不变、UNKNOWN 原 Attempt 对账、恢复和权限负例 |
| `control.api-contract-v1` | 轻量控制 API 纯合同与策略 | `accepted` | `W6_0_CONTROL_API_CONTRACT_ACCEPTED_DEVELOPMENT_SLICE`：冻结六份 pure canonical wire、独立 digest domain、`DRY_RUN/MUTATE` intent、强 ETag/If-Match、keyset pagination、限流与 loopback threat policy。Web view 明确为 `control-view-snapshot/v1`，不改写 Core `control-snapshot/v1`。动态 ID/时间不进入 semantic request；UNKNOWN 仅 exact receipt、禁止重放。W6-0 的无 Schema/receipt 历史边界保持不变；W6-2 confirmation 又增加 canonical-frozen Statement/evaluation。`W6_2_DURABLE_RECEIPT_SCHEMA_ACCEPTED_DEVELOPMENT_SLICE` 历史原子当时只批准一张 append-only receipt 表、exact resolver、NO_CHANGE commit 与 existing-row semantic/Backup closure，尚无公开 APPLIED insert；其 publication transaction 接线已由下一行的当前 wiring 切片验收 |
| `control-plane.online` | 默认关闭的本地 Control、只读 Overview 与模块管理查询 | `accepted` | 保留 W6-3/W6-4 的 Overview、Modules list/detail 与窄 `MODULE_DISABLE` workflow；P4 只读范围新增 tenant/workspace-scoped 的 UNKNOWN list/detail、Store verification、Backup constraints 与 Artifact Admission list/detail 查询，均使用 bounded reader、scope fence、projection digest、强 ETag 与 fail-closed error mapping。管理 UI 不接受服务器路径、Secret、请求体、签名材料或 replay material；UNKNOWN 不允许 resend、replay、换 Provider 或隐式 retry。P4 没有新增 lifecycle mutation、在线 Restore/CreateBundle、SSE、worker 或第二 Store/writer；浏览器验证与 Linux/安装门禁仍待 P5 |
| `release.public-beta` | 真实验证与公开 Beta | `planned` | W7 工作包；S3-D、Public Stage 与首次正式部署均未获批准 |

## W2-R1 REMOTE Action Host 验收边界

`W2_R1_REMOTE_ACTION_HOST_ACCEPTED_DEVELOPMENT_SLICE` 只覆盖一个原生、默认关闭的
`action.provider/v1 + REMOTE/freeagent-action-http/v1` Host：

- Manifest 仍只提出请求。当前协议的 entrypoint 必须是 ArtifactDigest 覆盖的 canonical
  `content/` descriptor；descriptor 只能声明有界 Action definitions，不能携带 endpoint、Secret、
  redirect、proxy、retry、fallback 或动态发现指令。未知 REMOTE 协议可通过离线格式验证，但没有
  exact Core handler 和 Operator grant 就不能 Activate、Bind 或执行；
- `module-apply` 与 `module-dry-run` 使用第 8 个 exact handler，并要求调用方分别提供 exact artifact、
  HTTPS endpoint 与 SecretRef grant。两条命令只验证并发布冻结事实，不解析 Secret value、不启动
  Provider、不发 POST；runtime 另以 `--enable-remote-actions` 和显式
  `--remote-action-secret-env <secret-ref>=<ENV_VAR>` 启用，默认关闭；
- production 路径固定为 Catalog → lazy loader → covered digest/size 验证 →
  `remoteactionhttp.NewFromArtifact` → native Adapter → 原 Gateway。Describe/Prepare 离线，generic
  `ModuleHost.Invoke` 被禁止；Gateway 只在原 DispatchAttempt 已为 PENDING 后调用一次
  `ExecutePrepared`；
- 原生传输只允许一次 HTTPS/1.1 POST，TLS 最低 1.2；不使用 proxy、redirect、keep-alive、HTTP/2、
  retry 或 fallback。连接前拒绝 private、loopback、link-local、multicast、NAT64、文档/基准及其他
  special-use 地址；IPv6 只接受 `2000::/3` 中未被 deny-list 覆盖的公共地址；
- Secret 按 RFC 6750 Bearer `b64token` 语法在 dispatch 时晚绑定，用后清零；Store、Backup、日志和
  错误只保存 SecretRef identity，不保存 Secret bytes。POST 前确定性拒绝为 FAILED；进入传输后
  无法证明结果时只把原 Attempt 置为 UNKNOWN，禁止重放、换 Provider 或创建替代 Attempt；
- 集中证据真实穿过 production loader/native Adapter/Gateway，并在 PENDING 后、POST 前确定性
  FAILED；篡改构件连续两次加载均拒绝且失败不缓存，Resolver 调用为 0；Provider `FAILED + HTTP 422` 恰好一次 RoundTrip；Disable 后删除 artifact 再运行真实 Pure Chat，
  Action Attempt、Adapter、Resolver 与 REMOTE artifact 访问均为 0。既有 hermetic 分层证据覆盖
  SUCCEEDED、UNKNOWN、终态持久化失败、重启与完整 Backup/Restore；本切片没有真实公网 HTTPS
  第三方互操作证据。

R1 没有增加 Runtime、Store、Loop、Gateway、Catalog pointer、效果账本或普通表；R1 收口时的 24 表只因
Activation 的 `REMOTE` execution class 约束而重算 identity。它增强了自由外挂 Action，同时保持
Agent/Workspace 轻量、共享 RAG 可选和 Pure Chat 零外挂。它不得被冒充为 WASM/不可信本地
隔离；后续 R2 的独立边界如下。

## W2-R2 WASM Action Host 验收边界

`W2_R2_WASM_HOST_ACCEPTED_DEVELOPMENT_SLICE` 只覆盖一个原生、默认关闭的 exact
`action.provider/v1 + WASM/freeagent-action-wasm/v1 + action-binding-config/v1` Host：

- Manifest 仍只提出请求，entrypoint 是 ArtifactDigest 覆盖的 canonical `content/` descriptor；
  descriptor 再精确引用同一 artifact 内的 wasm32 binary。Operator Apply/Dry-run 必须提供匹配
  candidate digest 的 `--allow-wasm-action-artifact`，并与 LOCAL_PROCESS、TRUSTED_IN_PROCESS、
  REMOTE artifact grant 互斥；两条命令只做有界预检与 compile validation，不实例化或执行 guest；
- production runtime 默认关闭，只有 `--enable-wasm-actions` 才允许 exact Catalog lazy loader 复验
  artifact digest/covered size、descriptor、module 与 ABI 后物化 Adapter。未绑定、未启用或 Disable
  后的 Pure Chat 对 artifact、compiler、instance 和 Action Attempt 为零访问；
- Host 固定为 `wazero v1.12.0` interpreter、Core WASM 1.0、wasm32。guest 只能导出
  `memory`、`freeagent_alloc_v1`、`freeagent_execute_v1`；imports、WASI、Host Module、网络、文件系统和
  Secret 均为零；
- ceiling 固定为 module 16 MiB、request 128 KiB、output 32 KiB、memory initial 最多 32 页且显式
  maximum 最多 256 页、table 最多 65,536 elements、全进程最多 4 个 guest instances、单次验证与
  执行最长 5 秒；
- Describe/Prepare 不执行 guest，generic `ModuleHost.Invoke` 明确拒绝 WASM。唯一 Gateway 只在原
  DispatchAttempt 已持久为 PENDING 后调用私有 `ExecutePrepared`；Usage receipt 的 engine 是
  `wazero-interpreter/v1.12.0`，`instruction_metering=UNSUPPORTED`，且无 token/cost/price；
- trap/timeout/ABI/output 确定性 FAILED，只把原 Attempt 收口为 FAILED。只有
  事务/崩溃遗留 PENDING 恢复原 Attempt UNKNOWN，即由 startup recovery 把同一 Attempt 收口为
  `UNKNOWN/RECOVERED_PENDING_AFTER_CRASH`；exact retry 禁止 replay/fallback、换 Provider
  或新建替代 Attempt；
- Backup/Restore 不编译、实例化或执行 guest；Create/Verify/Restore 逐字节闭合 Manifest、descriptor、WASM binary、Activation、Binding、
  Action result/receipt 和原 Attempt 终态，但不编译、实例化或执行 guest。恢复后删除 artifact，
  冻结 SUCCEEDED/UNKNOWN 的 exact retry 仍只返回原事实。

R2 没有增加第二 Runtime、Store、Loop、Gateway、Catalog pointer、效果账本或普通表；24 表 identity
只因 `WASM` execution class 约束而重算。它保留轻量 Agent/Workspace、共享授权 RAG 与零可选模块
Pure Chat。该 Host 不是 OS/container 或生产恶意多租户隔离；R2 收口时的下一入口为
`W2_R3_UNTRUSTED_MODULE_ISOLATION`，不得跳到供应链、W6 或 Beta。

## W2-R3 不可信第三方 WASM 纯计算隔离与撤权边界

`W2_R3_UNTRUSTED_MODULE_ISOLATION_ACCEPTED_DEVELOPMENT_SLICE` 只把 R2 Host 收口为显式授权的
第三方、零 Host capability、`Effect=none` 纯计算模块，并加入 deny-only 撤权验收；它没有新增 Runtime、
Store、Loop、Gateway、Catalog、Port 或效果账本。

- 运行期全局开关与 exact artifact allowlist 必须同时存在：`--enable-wasm-actions` 只开启能力类别，
  `--allow-wasm-runtime-artifact <sha256>` 才授予本次进程可物化的精确构件。allowlist 不持久化，非
  canonical、重复或不属于当前 WASM Catalog 的 digest 均在启动时失败关闭；
- WASM Adapter 只缓存 artifact-scoped immutable identity。同一构件可由多个合法 Activation 共享，
  但 Tenant、Workspace、InstanceID、ActivationRevision、Config、Authority 与 Binding 权限不进入缓存；
- Gateway 在 lazy resolution 前检查 current Activation，并在完整 exact execution closure 建立后、调用
  私有 executor 前再次检查。第二次检查成功是 execution-admission linearization point：此前撤权返回
  确定性 FAILED 且 guest=0；此后撤权不会改写已接纳调用，当前 `Effect=none` guest 仍受既有 5 秒上限；
- 当前只支持停机撤权：停止新 Admission、排空或取消已接纳请求、完成 Store-only PENDING recovery、
  退出并销毁 Registry cache，再离线执行 `module-disable` CAS 后重启。活动 serve 持有 Store 时 Disable
  返回 `STORE_BUSY` 且零变化；Disable 保留 Installation、Activation、历史 Run/Attempt 与 UNKNOWN 对账；
- 最后引用 Disable 后，新进程若不再提供 runtime allowlist，即使历史构件随后删除，Pure Chat 仍对
  WASM loader、Adapter、guest 与 Action Attempt 零访问。禁用后的 Backup/Restore 保留历史与 disabled
  current 状态；恢复更早的活动备份属于 Operator 显式回滚，不是在线绕过撤权。

威胁模型只假设模块作者可控制 Manifest、descriptor、WASM bytes 与输入；信任 Core、Gateway、Current
Store、Operator、Go、wazero 与 OS。当前不声称防御 Go/wazero/OS 漏洞，不提供 fuel/instruction/RSS
硬上限、运行中 guest 强杀、OS/container、seccomp、namespace、restricted token 或生产恶意多租户
隔离。R3 后续供应链工作从 W2-U0 开始，仍不得跳到 W6 或公开 Beta。

## W2-U0 模块供应链纯合同边界

`W2_U0_SIGNING_SOURCE_CONTRACT_ACCEPTED_DEVELOPMENT_SLICE` 只增加 `sdk/moduleapi` 中七份纯合同：

- Publisher Key ID 由 exact Ed25519 32-byte 公钥派生；detached Signature 只签域分隔后的 exact
  ArtifactDigest，错误 key/digest、截断 signature 和 key ID 替换均失败；
- Source Policy 只保存本地归一化 origin/root 的摘要、Source kind、网络模式、Publisher Key、Module
  ID 前缀及资源上限。它由 Operator 创建，不属于 Manifest；
- Discovery Index 允许显式空观察，按 Module ID、opaque Version、ArtifactDigest 和最多 4 KiB 的
  source-relative package path 稳定排序；同 exact ModuleRef 异 digest 冲突关闭；
- Snapshot 证明 exact Policy/Index parents；Candidate 必须来自其 entries，并以
  `{SourceID, ModuleRef, ArtifactDigest}` 派生跨刷新稳定 review key；Decision 只记录 APPROVE/REJECT，
  真正授权和跨刷新拒绝抑制仍由后续唯一 Store workflow 证明；
- 七份 wire 均有 hard-coded canonical/content-ID canary、严格 unknown-field/非 canonical 拒绝、
  defensive copy 和有界解析。

U0 没有网络、Store schema、artifact、Installation、Activation、Binding、Host 或外部效果；当时的
24 表 identity 不变，Pure Chat 和既有 Module Apply 语义不变。W2-U1 已在下述窄边界验收；U2 的
Store-backed observation 见其后的独立小节。

### W2-U1 已验收边界

历史状态：`W2_U1_SIGNING_SOURCE_POLICY_ACCEPTED_DEVELOPMENT_SLICE`。

- 无 supply flag 的 `module-verify` 继续走 legacy 路径；任一 supply flag 都显式选择 governed mode，
  不完整或混用参数失败关闭，不能静默回落；
- governed mode 只接受 `LOCAL_DIRECTORY + DENY`。Policy、Key、Signature canonical 文件与 exact IDs
  全部由 Operator 从 artifact 外部提供；Ed25519 Signature 只证明 exact ArtifactDigest，签名和 Policy
  都不授予 runtime、Trust、Authority、Effect 或 Apply 权限；
- `max_package_bytes` 同时约束首次 scan 与最终 scan。允许的 Module ID prefix 按点分段匹配：`vendor`
  只接受 `vendor` 或 `vendor.*`，不接受 `vendorx` 或 `vendor-*`；
- Publisher Key deny list 是本次调用冻结的 immutable snapshot。它只拒绝本次新候选观察，不是持久
  current revocation；unsigned direct verifier 拒绝没有 Policy 锚点的 Key、Signature 或撤销输入；
- U0 的 unsigned Discovery Snapshot 仍允许 entry `signature_id` 为空或携带语法有效的 authority-free
  observation ID；它不验证签名。该 wire 兼容性不能放宽 U1 direct preflight 的上述拒绝规则；
- HTTPS Policy 在 U1 拒绝，应用不创建 Source 网络 transport。Windows 拒绝 UNC/device namespace、
  ADS 与 symlink/reparse 路径；这不是“可检测所有映射网络盘”的安全声明；
- 成功仍返回原 `freeagent.module-package-verification/v1`，且不创建 reservation、grant、stage、Store
  fact、Installation、Activation 或 Binding，也不能作为现有 Apply 的 authority。手工 exact-grant
  `module-apply` 与九个 handler 保持原路径；
- U1 本身仍不持久化 Source/Discovery。U2 已在唯一 Store 中闭合 observation parent 与 current
  revocation；Candidate/Decision、Upgrade Apply 与 W6 仍不属于 U1。

### W2-U2 已验收边界

- 三个新命令均默认关闭：`module-source-register` 导入 exact Source Policy 与可选 Publisher Key，
  `module-source-refresh` 观察一个 exact Index，`module-publisher-key-revoke` 以 expected revision 做
  全局不可逆 Publisher Key revocation；三者都要求 `--enable-module-discovery`；
- Local Source 只允许 canonical absolute root 下固定 `root/index.json`，双次有界读取并复验文件与目录
  identity；拒绝 UNC/device namespace、ADS、symlink/reparse 与观察中漂移；
- HTTPS Source 另需 `--enable-https-module-discovery` 和重复型 exact HTTPS origin allowlist。Policy
  的 OriginDigest 绑定 canonical exact index URL；运行期 allowlist 绑定 canonical origin。每次只做一个
  HTTP/1.1 GET，DNS 结果中任一 special-use/non-public 地址都会拒绝；不使用 proxy、redirect、retry、
  HTTP/2、keep-alive、compression、cookie、凭据、条件请求或隐式 Header；
- Store 在 Source I/O 前读取 exact refresh basis，I/O 后于同一 `BEGIN IMMEDIATE` 内重验 Policy/Key
  revision、Source identity 与 revocation。SourceID 不得重新绑定 Kind/OriginDigest；相同 exact refresh
  返回相同 Snapshot/revision/timestamp，A→B→A 不冒充 exact retry；
- 全局 `ModuleID + opaque exact Version → ArtifactDigest` 映射同时约束跨 Source Snapshot、既有
  Installation 与 materialized Learning Version；相同 ref/同 digest 可共享，异 digest 失败关闭；
- Current Store 新增五张 typed fact 表并冻结为 29 表。Backup/Verify/Restore 只从已复制 Store 重建
  Policy/Index/Snapshot canonical closure，零网络、零 Source/包读取；恢复不触发 refresh；
- U2 是 observation-only：不下载模块包、不读取 entry package path、不验证 detached Signature、不创建
  Candidate/Decision/reservation/stage/Installation/Activation/Binding，也不调用现有 Apply。U3 已按下节
  负责无权限 Candidate 与审核；U4 仍须在真正 staging 前复验 current facts，并进入既有 Dry-run/Apply/CAS。

### W2-U3 历史验收边界

历史收口状态：`W2_U3_UPGRADE_REVIEW_ACCEPTED_DEVELOPMENT_SLICE / W2_U4_OPERATOR_APPLY_NEXT`。其中
`W2_U4_OPERATOR_APPLY_NEXT` 是进入 U4 前的历史 marker，不是当前入口。

- `module-upgrade-review` 与 `module-upgrade-decide` 默认关闭，必须显式
  `--enable-module-upgrade-review`；当前只面向停机、Store 独占的可信本地 Operator；
- 首片只允许同 Module ID、不同 opaque exact Version、不同 ArtifactDigest 与不同 target Instance 的
  `EXACT_VERSION_CHANGE`。Core 不解释 SemVer/`latest`/newer/downgrade；回滚意图须显式标记；
- Review 显式选择 Tenant、PROFILE 或 WORKSPACE_CHANNEL_ENDPOINT、current pointer/Instance/Activation/
  ModuleRef/ArtifactDigest、Source/Snapshot entry 和 target。Store 自行重建
  SourcePolicy→Index→Snapshot→entry 与 Binding→Catalog→exact Activation revision→Installation；
- target 必须是 Operator 提供的 local unpacked artifact；signed Source 另需 exact detached Signature。
  系统不读取 Index `PackagePath`、不联网下载。两次 verifier evidence 相同后，target canonical Manifest
  才以既有 `content_records` 保存；Review 不含本地主机路径、URL、Signature bytes、Secret 或
  Config/Authority 正文；
- `module-upgrade-review/v1` 冻结 safe supply/current/target Manifest summary、PublishedBasis、完整 runtime
  request、Provides/Requires/permission diff、全部 published Binding impacts、handler assessment 与
  digest-only required grants。结论只有 `WOULD_APPLY | CONFLICT | UNSUPPORTED`，没有 `NO_CHANGE`；
- Candidate 是全局不可变 supply fact，Review 是 Tenant/scope fact，Decision 终态绑定 exact Review。
  `APPROVE` 只允许 current basis 仍匹配的 `WOULD_APPLY`，但不是 authority；`REJECT` 必须显式
  `--confirm-tenant-wide-reject`，按 `{tenant_id, review_key}` 抑制同 Tenant 全 scope，不污染另一 Tenant；
- 三张 U3 表与 target Manifest evidence 进入 Backup/Verify/Restore semantic closure；历史 signed Review
  在 Key 撤销后保持可验证，但依赖 current live Key 的新 Review/APPROVE 失败关闭；parent、Manifest、
  publication、Decision 篡改均拒绝；
- Installation、Activation、Control、Catalog、Run、Attempt、Usage 与外部效果零变化；Pure Chat 对 U3
  facts、Source、artifact、signature 与 assessor 零访问。

U4 的历史入口门已经把原 CLI 私有的 9 条 generic/Model policy 与 2 条 Document Insight reserved
exact selector 提取为 Review 与 Apply 共同复用的唯一 pure policy/assessor、唯一
`internal/modulehandler` 实现，未复制第二张
handler 表。这条 `W2_U4_SHARED_HANDLER_GATE` 旧 NEXT marker 只描述 U4 开发前置，不是第二套策略表。

### W2-U4 已验收边界

历史状态：`W2_U4_OPERATOR_APPLY_ACCEPTED_DEVELOPMENT_SLICE / W6_0_CONTROL_API_CONTRACT_NEXT`。

- `module-upgrade-apply` 默认关闭，必须显式设置 `--enable-module-upgrade-apply`，并绑定 exact
  Tenant、ReviewID 与 APPROVE DecisionID；首片只接受受 `LOCAL_DIRECTORY + DENY` Policy 管理的
  显式 `--source-root`，不读取 Index `PackagePath`、不联网或下载；
- 所有 artifact/endpoint/SecretRef grant flags 必须显式提供且为空。唯一支持的 Review impact 是一个
  PROFILE 的 `context.provide/v1` Binding，target 必须为 `DECLARATIVE + static/v1 +
  TRUSTED_INSTRUCTION + deny-all Authority`；Model、Channel、Action、`SINGLE`、共享 current instance、
  fanout 或多个 BindingImpact 均失败关闭；
- production 顺序固定为历史 U1 governed source verification → current Store approval/source/current
  revalidation → 重新构造并逐字节确认同一 exact plan → current U1 source verification → pre-stage
  revalidation → 既有 Apply/CAS。Review 与 Apply 复用同一 pure evaluator；不存在另一份 handler 表，
  也不虚构独立 dry-run admission 或审批权威；
- canonical plan 用 exact replacement seam 把旧 Instance 在原 PortPlan ordinal 原位替换为 target。
  其余 Binding canonical bytes 与顺序不变；旧 Run 保持冻结旧 Provider/static context，新 Run 才冻结
  target；
- 相邻 current exact retry 可只读返回原 publication，零 source I/O、零 staging、零新 Store mutation。
  这是相邻 plan idempotency，不是 durable approval receipt；任一后续 publication 发生后，旧审批重试
  返回 `POINTER_CONFLICT`；
- Apply 写入外部效果之后若不能证明终态，原 Apply 结论保持 UNKNOWN；不得语义重放、换 Provider、
  创建替代 Attempt 或伪造成功。完整 Backup/Verify/Restore 保留 Review、Decision、Manifest 与最终
  publication；Pure Chat 仍对 Source/Review/artifact/stage/assessor 零访问；U4 不新增表，FAC1 Store
  继续是 32 表。

该历史入口 `W6_0_CONTROL_API_CONTRACT_NEXT` 当时只授权冻结控制 API 合同，不授权自动 Apply、后台
upgrade worker、第二控制器或广义模块自由装配；它已由下面的 W6-0 收口状态取代。

### W6-0 控制 API 合同历史验收边界

W6-0 当时状态：`W6_0_CONTROL_API_CONTRACT_ACCEPTED_DEVELOPMENT_SLICE / W6_1_APPLICATION_SERVICES_READ_API_NEXT`。

- 六份 wire 都是 strict canonical JSON、closed enum、独立 digest domain，并具有 defensive-copy 与
  restore/canonical canary；Web view 使用 `control-view-snapshot/v1`，不与 Core 的
  `control-snapshot/v1` 共用 schema 或摘要域；
- Operation request 显式携带 `DRY_RUN/MUTATE` intent。动态 request ID 与 observed/requested time
  不进入 semantic request；receipt 重复冻结 principal、scope、operation 与 intent，`UNKNOWN` 只允许
  返回原 exact receipt，不得语义重放；
- transport-neutral policy 只冻结 strong ETag/If-Match、keyset pagination、body/结构上限、并发与
  loopback threat model；W6-0 没有创建网络 listener、HTTP handler、在线 session、SSE 或 UI；
- `cmd/freeagent` production composition 不导入 W6-0 包，Store 不新增 Schema 或 receipt table。
  该 W6-0 历史原子当时的证据为 39 份 Markdown、37 个源码 package、35 个 production dependency-closure package，
  FAC1 Store 仍为 32 表并保持上述 fingerprint 与 migration 摘要；
- `W6_1_APPLICATION_SERVICES_READ_API_NEXT` 当时只授权默认关闭的 read/Dry-run application services；
  该 marker 已由下列 W6-1 收口取代，不改写 W6-0 的历史证据。

### W6-1 Application Services / Read API 已验收边界

历史收口状态：`W6_1_APPLICATION_SERVICES_READ_API_ACCEPTED_DEVELOPMENT_SLICE /
W6_2_CONTROLLED_MUTATIONS_AUDIT_NEXT`。

- `--enable-control` 默认 false；关闭路径不创建 Control listener、handoff、session/cursor registry 或
  Control Application Services。显式启用时先验证非管理员身份，复用唯一 Store，构造 Modules 与
  `MODULE_DISABLE` Dry-run services 和 startup-closed shared Admission；
- 随后绑定 Chat listener 与 exact `tcp4 127.0.0.1:0` Control listener，从实际 socket 得到 origin，构造
  scope/session/cursor/HTTP handler，启动两端但保持 Admission 关闭；owner-only exclusive handoff 与 readiness
  成功后才一次性开放 shared Admission。任一步失败都零接纳并关闭两端；
- bootstrap 5 分钟、session absolute 8 小时/idle 30 分钟；scope-filtered `GET /control/api/v1/modules`
  与 detail、keyset cursor、strong ETag/If-None-Match，以及 `POST /control/api/v1/modules/disable/dry-run`
  的 exact If-Match/CSRF/Origin 纵链已闭合。除 bootstrap exchange 外，Modules GET 与非安全方法都必须
  同时携带 exact session 和 session-bound CSRF；GET 缺失或错误 CSRF 返回 401，零 service 调用；
- `MODULE_DISABLE` 只实现 `DRY_RUN`，拒绝 `Idempotency-Key` 与 confirmation header，不调用任何 domain
  mutation。receipt 从 exact frozen request/basis 构造并逐字段复验，仅随本次响应返回，不写 Store；
- 该 W6-1 历史原子当时的证据为 39 份 Markdown、44 个源码 package、44 个 production dependency-closure package。
  FAC1 Store 仍为 32 表并保持上述 fingerprint 与 migration 摘要；没有 Control mutation、durable receipt、
  SSE、UI、第二 Store/writer、后台 worker 或主动预热。既有停机 CLI Apply/Disable 不得冒充 Control mutation；
  `W6_2_CONTROLLED_MUTATIONS_AUDIT_NEXT` 只开放审计入口，不等于首写已获批准。

### W6-2 受控操作审计与 confirmation 纯合同历史验收边界

历史状态：`W6_2_CONFIRMATION_CONTRACT_ACCEPTED_DEVELOPMENT_SLICE /
W6_2_DURABLE_RECEIPT_SCHEMA_NEXT`。

- audit 历史 marker 为 `W6_2_CONTROLLED_MUTATIONS_AUDIT_ACCEPTED_DEVELOPMENT_SLICE /
  W6_2_CONFIRMATION_CONTRACT_NEXT`。只选择窄 `MODULE_DISABLE` 首候选，其余五项 deferred；
- canonical-frozen Statement 逐字段绑定 MUTATE、scope/key/input/evaluation/expected ref，Request 的
  `confirmation_digest` 必须由它重建；raw proof 绝不进入稳定 digest；
- `ModuleDisableEvaluationV1` 冻结完整 candidate projection，只计算不 publication；process-local registry
  的 raw proof 为 32 bytes、最多 2 分钟且不超过当前 session absolute expiry/global 256/session 8，只保留 proof digest 与当前 session authority binding；
- proof、proof digest、Boot/session 不进入 Current Store/Backup/log；当时 32 表 Store、fingerprint、
  57,652-byte migration 与 Backup/Restore 不变；
- 没有 durable receipt row、mutation route、SSE/UI/worker，纯 SQLite 首片 `UNKNOWN` 不可达。当时的下一入口只
  允许单一 append-only receipt 表、exact canonical closure、caps/quotas/unique/atomic/Backup 的
  32→33 rebuild-only 候选；在该 confirmation 历史原子收口时尚未实现，也不授权写路由。

### W6-2 durable receipt Schema 历史验收边界

历史收口状态：`W6_2_DURABLE_RECEIPT_SCHEMA_ACCEPTED_DEVELOPMENT_SLICE /
W6_2_MODULE_DISABLE_MUTATION_WIRING_NEXT`。历史入口仍为
`W6_2_CONFIRMATION_CONTRACT_ACCEPTED_DEVELOPMENT_SLICE / W6_2_DURABLE_RECEIPT_SCHEMA_NEXT`。

- 该 W6-2 历史原子中的 Store 从 32 表显式 rebuild 到 33 表，只新增 STRICT、append-only
  `control_operation_receipts`；当时 fingerprint/migration 为
  `51be9081a815e6379380b93e745db03d33ca2f358962f85ace63bce2534dcc10`、67,998 bytes、
  `8feea38beb505ab4f371afe32eecd877744062bd149d0d24982839c5450fc952`；
- identity 只由 exact Request 派生的 principal/scope/operation/idempotency key digest 决定。resolver 在
  request digest 相同才返回原 receipt，不同则 typed conflict；公开 commit 只接受重放闭合且当前 basis
  不变的 `NO_CHANGE`；
- 每个 canonical 的 ceiling 为 Request/input/evaluation/Control receipt/pre/post/domain
  `8/8/64/16/8/8/128 KiB`，总 canonical 为 `256 KiB`；Tenant/Store quota 为 `1,024/8,192`。
  SQL 同时锁定唯一 identity、domain ID/digest、same-Tenant parent、Catalog→Control pair 与 append-only；
- `module-apply-plan/v1`、`control-published-pointer-ref/v1` 与
  `module-disable-publication-receipt/v1` 严格 canonical 合同已冻结。`APPLIED` 只有 DDL/restore/verifier
  形状，Store 当时没有公开 insert；该历史原子的下一 wiring 义务要求唯一 publication owner 在同一
  `BEGIN IMMEDIATE` transaction 内完成 domain receipt、Control receipt 与 Control/Catalog pointer CAS；
- semantic verifier 先做 metadata/quota/cap 检查，再严格 Restore canonical、核对 immutable
  Control/Catalog parents并重放 neutral evaluator；Backup Create/Verify/Restore 都在 coherent snapshot
  上调用它。该闭包只验证现存行；单一无锚 receipt 表不能证明整行从未被删除；
- 该历史原子的证据为 39 份 Markdown、47 个源码 package、46 个 production dependency-closure package、
  33 表 Store；当时没有 mutation route/handler、confirmation endpoint、APPLIED public insert、SSE/UI/worker；
  其唯一下一入口是
  `W6_2_MODULE_DISABLE_MUTATION_WIRING_NEXT`。

### W6-2 MODULE_DISABLE mutation wiring 历史验收边界

历史收口状态：`W6_2_MODULE_DISABLE_MUTATION_WIRING_ACCEPTED_DEVELOPMENT_SLICE /
W6_3_WEB_SHELL_READ_ONLY_OVERVIEW_NEXT`。

- `freeagent serve` 继续默认关闭 Control。只有既有显式 `--enable-control` 分支初始化 process-local
  confirmation authority 和 mutation coordinator，并在同一 Control listener 注册
  `POST /control/api/v1/modules/disable/confirmation` 与 `POST /control/api/v1/modules/disable/mutate` 两路；
  default-off 路径零 registry、零 mutation route、零 Store 额外访问；
- 首片严格限制为 TENANT/PROFILE、exact `context.provide/v1`，且 existing removed Binding 为
  `OPTIONAL`、`DECLARATIVE static/v1`、trusted-instruction config、deny-all Authority；复用唯一 neutral
  evaluator/publication builder，不调用自行打开 Store/recovery 的停机 CLI；
- durable resolver lookup-first：同 identity+同 request digest 返回 byte-stable original receipt，不依赖旧 proof 或旧 session identity；
  每次 HTTP 仍须重新通过当前 Origin/session/session-bound CSRF/TENANT Permit，
  不是匿名 resolver。同 identity 不同 request digest 在 proof/evaluation/current-basis 前冲突。miss 才 claim
  最多 2 分钟且不超过当前 session absolute expiry 的 process-local proof、重建 exact evaluation，并在提交前复验 current auth/basis；
- `NO_CHANGE` 与 `APPLIED` 都持久化 exact Control receipt。`APPLIED` 同时把唯一 domain receipt、
  Control/Catalog publication 与 pointer CAS 放进同一 `BEGIN IMMEDIATE` transaction；exact retry、重启后
  retry 与并发不同 key 最多一个 APPLIED 都不重复 publication。纯 SQLite `UNKNOWN` 不可达；
- Backup Create/Verify/Restore 验证现存 APPLIED roundtrip 与 parent/replay/domain tamper。无 external completeness anchor，
  不能发现任意整行 receipt 删除；forced receipt-insert failure 的全事务 rollback 证明
  publication-without-receipt 不会由该 seam 提交；
- 该历史原子当时为 39 份 Markdown、48 个源码 package、48 个 production dependency-closure package、
  33 表 Store，schema identity 不变。无其他 mutation、SSE/UI/worker、第二 Store/writer、Provider/Gateway
  效果或预热；当时唯一下一入口为 `W6_3_WEB_SHELL_READ_ONLY_OVERVIEW_NEXT`。

### W6-3 Web Shell / read-only Overview 历史验收边界

历史状态：`W6_3_WEB_SHELL_READ_ONLY_OVERVIEW_ACCEPTED_DEVELOPMENT_SLICE /
W6_4_MODULES_CONFIGURATION_UI_NEXT`。

- Control 保持默认关闭；default-off 不初始化 Overview service 或 static asset resolver，也不暴露 Web/API 路由；
- 显式 `--enable-control` 复用同一 exact loopback listener、Store、session、Origin/CSRF 与 Permit，新增 exact
  embedded assets 和 `GET /control/api/v1/overview`，无第三 listener、Store 或 writer；
- Overview 在单一只读事务中有界读取 Workspaces、Runs、UNKNOWN、Learning、Module Candidates 与 Usage，
  将 current PublishedBasis、每节 source digest、view/projection digest 与 strong ETag 绑定并在编码前复验；
- Web Shell 只从 server-authorized scopes/current Workspace refs 构造 Tenant/Workspace selector；TanStack Query
  key 含完整 scope，tab-scoped `sessionStorage` 只保存 rotating resume credential，CSRF 仅在内存。搜索、detail
  drawer、deep link 只过滤已授权结果，并明确区分 loading/empty/error/stale/permission-denied；
- 该历史切片 Store identity 为 41 tables / 23 indexes / 56 triggers，fingerprint
  `87d63a2e468ee27ee2c0e1918a92e580532825bde85dbb872b9c795bf577f2c1`，migration 143,588 bytes /
  SHA-256 `5bd9743e988b82750f785c015f07c11fbcf6025da70148405aec11106b4efb22`；
- W6-3 当时无业务 mutation UI、SSE、worker、durable client cache、第二 Store/writer、Provider/Gateway 效果或预热；
  其历史下一入口为 `W6_4_MODULES_CONFIGURATION_UI_NEXT`。

### W6-4 Modules configuration UI 历史验收边界

历史状态：`W6_4_MODULES_CONFIGURATION_UI_ACCEPTED_DEVELOPMENT_SLICE /
W6_5_SERVER_OWNED_MODULE_ARTIFACT_INGRESS_NEXT`。

- Control/default-off、loopback listener、session/Origin/CSRF/Permit、唯一 Store 与 publication owner 不变；
- Modules list/detail 严格验证 scope、source/view/basis、projection、strong ETag、cursor 与 current binding detail；
- 只有 TENANT/PROFILE OPTIONAL `context.provide/v1` 的既有窄候选能进入 dry-run；`WOULD_APPLY` 后才请求
  short-lived confirmation，并在原样 inert summary 后要求第二次显式确认；
- proof 首次发送即清除；不可判定结果只保留无 proof 的 exact tuple。APPLIED/durable NO_CHANGE 后不乐观写，
  只清状态并重新读取 Modules/Overview；
- deterministic assets、TypeScript/Go 定向矩阵和真实 loopback 浏览器 r2 → r3 mutation/refetch 均闭合；
- 无 artifact ingress、其他 operation/mutation、Schema、第二 Store/writer、SSE、worker 或远程 Control。

### W6-5 server-owned module artifact ingress 当前验收边界

W6-5 收口时的历史状态：`W6_5_SERVER_OWNED_MODULE_ARTIFACT_INGRESS_ACCEPTED_DEVELOPMENT_SLICE /
W6_6_SERVER_OWNED_MODULE_UPGRADE_REVIEW_NEXT`；当前 W6.6、P2、P3 与 P4 两批只读管理能力
均已收口，当前下一入口为 `P5_BETA_GATE`。

- 入口只有可信本地 Operator CLI `module-artifact-ingress`，并默认关闭；未显式
  `--enable-module-artifact-ingress` 时不打开 Store、不读 Source、不发布文件；
- source/artifact roots 只作瞬时可信 CLI 输入，不进入 durable canonical；调用方没有 package path、URL、
  signature 或 upload carrier，只能选择 Store-owned current Snapshot 中 unsigned
  `LOCAL_DIRECTORY + DENY` 的 exact Source/Snapshot/Module/ArtifactDigest；
- 文件系统 durable-first：完整复验后使用 hidden direct-child stage、sync 与 digest-addressed no-replace
  publication；已存在目标只在 exact full verification 后复用。随后唯一 Store 在同一
  `BEGIN IMMEDIATE` 中重新验证 basis，提交 immutable Artifact 与 append-only Admission；
- Source/artifact/Backup tree 使用 held-parent-handle 相对遍历，拒绝 link/reparse、hardlink、跨设备与 change/
  namespace 漂移；artifact root、children 与全部 ancestors 都必须可信私有。跨进程 root lease 覆盖 crash-stage
  recovery、publish、sync 与 root 级 256 trees / 512 MiB covered content / 32,768 paths / 16,384 files /
  16 MiB path-name bytes 硬预算（不声称等于实际 allocation blocks）；ambiguous commit 保留有界 inert bytes而不猜测删除；
- 新 ingress 的同 digest 目标缺失时一律发布为目录 `0700`、文件 `0600`。既有同 digest 目标经完整 bytes/mode
  复验后只允许两种 root-global 闭包：全部目录 `0700`、全部文件 `0600`；或全部目录 `0700`，仅 canonical
  `LOCAL_PROCESS + mcp-stdio/2025-11-25` descriptor 精确绑定的唯一 executable 文件为 `0700`，其余文件
  `0600`；两者均不得含 special/setid/sticky 或 group/world 权限。跨 Store 共用 artifact root 时不得以任一
  单独 Store 的 Installation 决定模式；后一种只是既有物理兼容态，不授予当前 Store authority。这里的 inert
  是无 Store authority 且本切片不执行，不等同于不存在 OS executable bit；
- 只有 durable exact Admission 可在 head 前进后恢复历史 selector；未提交 stale selector无 authority。以后任一
  current 合格 selector声明同一 digest 时，仍须全包复验才能复用 bytes；
- Artifact/Admission 只证明 supply observation，保持 inert；不产生 Installation、Activation、Binding、grant、
  Review/Decision、Apply、Host、execution、Secret 或 effect。没有 HTTP route、upload、UI、SSE 或 worker；
- Backup 将 Installation 与 ingress 的 artifact digest 去重合并，离线验证并恢复完整 Store/manifest/bytes；
  Source 已移除且零 Installation 的构件仍可离线恢复；临时 DB/bundle/restore tree 使用私有 staging 与 parent sync；
- W6-5 验收当时的 FAC1 Store 为 43 tables / 25 explicit indexes / 64 triggers，fingerprint
  `47981b9bf147ec81c6e98382f281db0d5395187a1f090f7ce183c116fba82a0d`，migration 150,301 bytes /
  SHA-256 `6def433a59fa8f4876894572f1610cae499abc5b389ab5930ea697055419ca86`；该身份已冻结。P0/W6.6
  后的当前 FAC2 Store 为 UserVersion 2、42 tables / 26 explicit indexes / 64 triggers，fingerprint
  `d5d876f327dc29dc6f4a10476652641172ab8e1f0451a8714fc450f58733541e`；`0001` bootstrap 保持
  149,239 bytes / `dbc3e724a1f7c030677c84a77a317f69ef2fe246985cc749559a9f3dd5a6dc5a`，`0002`
  为 7,173 bytes / `3091a49ebcf724f573f91cc0fd22a7c58ebb52fa9d7ed552e32b6526ebeca3cb`。W6.6 已完成，
  P2 Control UI i18n、P3 第二 Provider 与 P4 两批只读 service/UI 已完成，当前下一入口为 `P5_BETA_GATE`。

### W6.6 server-owned module Upgrade Review 当前验收边界

当前状态：`W6_6_SERVER_OWNED_MODULE_UPGRADE_REVIEW_ACCEPTED_DEVELOPMENT_SLICE / P2_CONTROL_UI_I18N_ACCEPTED_DEVELOPMENT_SLICE / P3_SECOND_PROVIDER_ACCEPTED_DEVELOPMENT_SLICE / P4_MODULE_MANAGEMENT_UI_READ_ONLY_SLICE_ACCEPTED_DEVELOPMENT_SLICE / P5_BETA_GATE`。

- 入口只有默认关闭的可信本地 Operator CLI `module-upgrade-review-server-owned` 与
  `module-upgrade-decide-server-owned`；请求只含 Tenant、scope、Admission/Review identity、operator、
  request digest 和 Decision reason，不接受 artifact directory、package path、URL、signature bytes 或
  调用方提交的 current/target facts；artifact root 是服务端注入的 trusted dependency；
- Review 通过 `module_artifact_admissions` 读取已接纳的 inert Artifact，复验 Admission↔Artifact↔Manifest
  closure 与 server-owned physical digest/size/file-count/mode；再由 Current Store 重建 Source/Snapshot、
  Current Installation、Binding/Activation/Catalog/Control basis，并复用既有 W2-U3 evaluator；
- Review/Decision 都持久化、content-ID exact retry，Admission ID/operator/request digest 同时进入 Review
  canonical 与 v2 SQL projection（`ArtifactAdmissionID`、`OperatorPrincipalID`、`ReviewRequestDigest`）；v1 caller-owned Review 继续只读兼容，v1→v2 只能通过显式 backup-fenced
  migration；
- 失败分类覆盖 Admission 不存在、Artifact 篡改、Tenant/Review 冲突、Source/Review stale 与 Review
  integrity。跨 tenant、source/head stale、物理篡改、Decision 非 `WOULD_APPLY` approve 均失败关闭；
- Review/Decision 是 inert 审核事实，不自动 Install、Activate、Bind、Grant、Apply、Execute，不调用
  不调用 Provider，不创建 Runtime/Attempt/Usage/Effect；Windows CLI 集成、exact retry、zero-effect、tamper、
  wrong-tenant、backup/verify/restore/reopen 已回归。

### P3 第二 Provider 当前验收边界

当前状态：`P3_PROVIDER_CONTRACT_FROZEN_DEVELOPMENT_SLICE / P3_SECOND_PROVIDER_ACCEPTED_DEVELOPMENT_SLICE / P4_MODULE_MANAGEMENT_UI_READ_ONLY_SLICE_ACCEPTED_DEVELOPMENT_SLICE / P5_BETA_GATE`。

- `corecontract.ModelStreamEventV1` 与 `ModelStreamAccumulatorV1` 是所有 Provider adapter 共用的流式边界；支持最后一帧携带文本分片与 Usage，EOF 无终态进入 `UNKNOWN`，超限进入 `TRUNCATED`，迟到分片拒绝；
- 既有 `modulehost.InvocationOutcome`、`moduleapi.ModelGenerateOutputV1` 与 `ModelUsageReceiptV2` 继续作为成功、失败、UNKNOWN、输出与 Usage 的唯一持久消费边界；不新增第二 Store、第二 retry loop、Provider fallback 或预算/金额语义；
- DeepSeek 现有 `APIKeyResolver` 的 SecretRef/短生命周期明文边界被写入 P3 contract；智谱 adapter 复用同一形态，凭据、原始响应、Prompt 与 private reasoning 不进入普通 contract、日志或备份；
- 第二 Provider 冻结为 `zhipu`，代表模型冻结为 `glm-4.5`，只允许 compiled official Chat Completions endpoint、exact model/build 和显式 `--enable-zhipu`；不开放任意 OpenAI-compatible URL 或模型枚举；
- 非流式严格要求 `finish_reason=stop`；流式将 SSE 映射到公共 `DELTA/USAGE/COMPLETED` 状态机，EOF 无终态为 `UNKNOWN`，`length`/tool 终态不冒充成功，超限为 `TRUNCATED`；Actions、vision 和用户可见 SSE/token UI 不在本轮支持矩阵；
- 2026-09-22 的 opt-in 真实实验通过 FAC2 seed、production composition、Universal Loop 与 `glm-4.5` 得到 `SUCCEEDED`，input/output token 字段存在；报告只保留脱敏元数据。`glm-5.3-flash` 的独立探测暴露 thinking 参数差异，因此不能把本验收扩大为全部 GLM 模型兼容；
- 不新增第二 Store、第二 Loop、adapter retry、Provider fallback、自动选模、预算或金额语义。P3 accepted 仍不等于公开 Beta 或 `RELEASE_READY`；P4 两批统一 application/control service 只读管理投影已实现，下一步是 `P5_BETA_GATE`。

### P4 模块管理 UI 当前只读边界

当前状态：`P4_MODULE_MANAGEMENT_UI_READ_ONLY_SLICE_ACCEPTED_DEVELOPMENT_SLICE / P5_BETA_GATE`；这不是完整生命周期管理，也不代表 `RELEASE_READY`。

- Control 层新增 `GET /control/api/v1/module-upgrade-reviews` 与按 ID 的 detail route；两者只依赖统一 `controlapp` read service，使用 tenant-scoped、bounded Store reader、重复 live authorization、projection digest 与强 ETag；不暴露 Store 的 unbounded audit API；
- application/service 边界使用中性 DTO。Review detail 仅返回安全字段、Decision 摘要和 server-owned Artifact/Admission 的来源、快照、Manifest digest、大小与文件数；不返回 canonical bytes、host path、URL、signature、Secret 或目标包正文；
- `#upgrade-reviews` 双语页面已接入 `zh-CN` / `en-US`，可查看 Review、Decision、UNKNOWN-safe read boundary 所需的审计字段和 Artifact provenance；没有 approve/reject/apply/install/activate/bind/resend/replay 按钮；
- 第二批已补齐 tenant/workspace-scoped、bounded 的 UNKNOWN list/detail、Store Verify、Backup constraints 与 server-owned Artifact Admission list/detail；复用 projection digest、strong ETag、scope fence 与 fail-closed error mapping。Backup UI 不在线创建或恢复，Restore 仍为离线 staging + atomic publish；
- 前端继续复用 `modules.ts` 的 scope headers、transport validation、ETag 与 fail-closed 事件；未新增 Store、Loop、retry、outcome 或权限规则。UI 不接受服务器路径、Secret、请求体、签名材料或 replay material；当前下一入口为 `P5_BETA_GATE`，mutation、在线 Restore/CreateBundle 与生命周期操作继续后置。

## W5-F1 权威验收边界

W5-F1 追加下列开发证据，不改写 W5-X1 的历史 canary、Run/Attempt 或 Usage：

- 三个始终 runnable Workspace 的 900 次 claim 在第 450 次后重开 Store，最终各 300 次；首次
  服务不晚于第 3 次，最大服务间隔不超过 3；
- Decision approve、单槽 repair 与 Decision+Transfer 均由唯一 Scheduler claim；dormant/skipped repair Run 保持 `Attempt=nil` 且零 claim；
- Specialist 反向完成后仍按 frozen plan 排列，Store reopen 后 canonical bytes 与身份不变；跨
  Workspace `2N+3` family 取消后不创建新 Attempt、Transfer payload 或 envelope；
- 可靠 evidence 只收口原 UNKNOWN Attempt；SUCCEEDED 恰好生成一个 RESULT，FAILED 不生成 RESULT，stale revision、替换 Provider、替换 Attempt 和无 evidence 均拒绝；
- 完整 Decision+Transfer backup→verify→restore→reopen 已通过；跨 Workspace PENDING 恢复为
  同一 Attempt UNKNOWN，Universal Loop 语义重放为 0；
- root/target 双边 grant、Tenant、Workspace revision、方向、kind/schema/ref/size、48 KiB
  ceiling 与撤权负例均失败关闭；Pure Chat 和 ordinary Composite 均为零 Transfer material。

工程门禁分两步闭合：Windows 全仓测试、`go vet`、`go mod verify` 与 gofmt 检查均通过；WSL
首轮受影响七包 race 中六包通过，CurrentStore 仅暴露 1 秒 TTL 墙钟测试竞态且没有 data race。
固定逻辑时间后，定向 race、单核慢调度和完整 CurrentStore race 均通过；完整 CurrentStore race 以 exit 0 在 466.241 秒结束，首轮七包命令本身不记作整体 exit 0。

F1 Test-0808 canary 为 4/4 HTTP 2xx，exact retry 新 HTTP/Attempt 均为 0；input/cached/uncached/
output 为 3,149/768/2,381/1,492，2/4 请求命中缓存，token 加权缓存命中率 `24.388695%`，
estimated cost `0.00538036 CNY`，reasoning token、Provider reported cost 与 reconciled cost
均为 UNKNOWN。该样本只证明当前窄开发纵链，不是部署、公开 Beta、生产 SLA 或
`RELEASE_READY` 证据。

W1 与 W3 能力补齐仍只使用唯一 Runtime、Current Store、Universal Loop、Context Compiler、
Usage Ledger 和完整 Backup；它没有把 Role、Persona、RAG、Memory、Skill、MCP、Action、
Channel 或 Team 固化进 Core，也没有改变零可选模块 Pure Chat。Agent 继续轻量，领域知识仍
位于共享授权 RAG，Memory 与 Conversation Summary 保持可选且语义分离。因此最初的共享知识、
模块自由外挂与 85%/100% 最小 Drop 设计不受破坏。

W2-C 只复用现有 Knowledge Runtime 纵链和统一 Module Apply/Dry-run：没有新增 Port、Runtime、
Store、表、Loop、Gateway 或 backup schema，也没有把 Knowledge 固化进 Agent/Workspace。在
W2-C 验收时，Collection 标签、词频/大类计数、重复检索免检、Memory、远程向量库与知识更新
尚属于 W3 以后；当前 W3 结果见下段。未绑定 Knowledge 的 Pure Chat 继续保持零可选模块访问。
该切片增强最初的自由外挂与共享知识设计，不改变其核心边界。

W2-D 已把本地显式装配 v1 收口为独立 `accepted` 开发切片，并增加调用方已知 exact pair 的
`module-history`。history 恢复 Control/Catalog 后还会通过既有窄 Installation 投影复验 stored
Manifest closure，但不读取 artifact root、不输出 Manifest 正文或摘要、不构造 historical
PublishedBasis，也不枚举、计算 diff 或派生 DISABLED 状态。共享包缓存仍逐 Entry 复验，inspect
也闭合未绑定 Entry；W3-M1 Memory Apply 只是同步既有已验收合同，不改写归属。Operator 入口与
Module Conformance 继续 `experimental`，广义通用装配、W6/W7 与 Beta 继续 `planned`。

W2-E2 又以受信、编译进 Core 且无外部效果的 `text.stats` 证明普通 Pure Chat Store 可以经统一
Apply 进入原 Action/Gateway 链；该切片为独立 `accepted`，没有改变 W2-D 历史，也没有把任意
第三方进程内 Action、`module.general-assembly`、W6/W7 或 Beta 提升为可用。

W2-E3 再把同一统一 Apply 收窄扩展到 Workspace-owned loopback Channel Endpoint。ENABLED 将
Control/Catalog 与 revision-0 Cursor seed 原子提交；Dry-run 不写 Store、不解析 Secret、不联网，
exact retry 在 Cursor 已推进后仍验证原 seed。集中验收以同一 Agent/Profile 和共享 Instance 绑定
双 Workspace/Endpoint，证明 Cursor、Run、receipt、终态与 Module History 不串流，成功和 Channel
`UNKNOWN` duplicate 不重发，一个 Endpoint 的未对账 `UNKNOWN` 不阻塞另一个；Disable 与
Backup/Restore 均保留历史并从下一 Cursor revision 继续。该窄 `accepted` 不开放公网 Channel、
任意第三方进程内代码，也不提升 Operator Module Apply、Module Conformance、W6/W7 或 Beta。

W2-E4 将第 7 个 Core-owned exact handler 收窄到
`model.generate/v2 + TRUSTED_IN_PROCESS/go-in-process/v1 + model-binding-config/v2`，且 selector
固定为 `freeagent.builtin.model.deepseek@2.0.0`。它只允许同一 Artifact、Adapter 和 Provider
Instance 内 flash/pro 的 Operator 显式 Apply/Dry-run/CAS；候选必须在任何 Store 写入、Secret
解析或网络调用前闭合 Config、`model-authority-ceiling/v1`、临时 exact SecretRef grant，以及可选
exact ModelProfile。省略 optional Profile
表示清除；Model Disable 拒绝，回滚仍发布新的 ENABLED Apply。原 E4 当时还要求闭合预存且
provider/model/billing 匹配的 PriceSnapshot，该要求已随 P0 金额退场删除。

E4 publication 只对新 Run 生效，旧 Run 的 Binding、Config、Authority、SecretRef
和 Profile 继续冻结。UNKNOWN 只允许对账原 Attempt，不能换模型、创建替代 Attempt 或语义重放。
Store direct publication 与 Backup current semantic gate 都重新验证 Authority/Profile closure；
集中验收已覆盖跨 Workspace/Profile、token Usage、Backup/Restore 和负例矩阵。它没有新增 Schema、
表、Runtime、Loop、Gateway、Catalog pointer 或效果账本，也不把自动选模、成本路由、跨 Provider、
多 Provider、REMOTE/WASM、Operator Module Apply overall、Module Conformance、W6/W7 或 Beta 提升为可用。

W2-E5-A 在原 Knowledge handler 上增加一个不可变 governed 版本：Manifest 仍只提供
`context.provide/v1`，但必须以 exact pair 同时声明 `model.generate/v2` Require 与
`knowledge.read` request；旧 permissionless 版本继续兼容。Manifest request 不授予权限。Core 从
同一 Profile 的 Binding→Catalog→Activation→Installation→Manifest 身份链以及 Config、
`knowledge-authority-ceiling/v1` 重算唯一 Model 依赖与有效 grant，按 Config/Authority 交集裁剪检索
限制；部分声明、未知 permission、缺失/歧义/跨 Profile/循环依赖或越过 scope 均失败关闭，也不保存
第二份依赖图或 grant 事实。

集中验收已闭合 Apply/Dry-run fast path 与 pre-staging 失败关闭、首次 publication、exact retry、公开
Verify、真实本地 RAG Chat、grant 从 `4/4096` 收窄至 `2/2048`、Disable、CAS，以及 enabled/disabled
Backup→verify→restore；历史请求与 Compilation 字节保持稳定，未绑定或 Disable 后的新 Run 对
Knowledge 零访问。四个受影响包 `./sdk/moduleapi`、`./cmd/freeagent`、`./internal/currentstore`、
`./internal/currentbackup` 完整回归；该 E5-A 历史验收当时 `go test ./...` 的 30 个仓内 Go package 用时 221.9 秒，另有
1 个 external compatibility package 由 `sdk/moduleapi` 嵌套测试在临时独立 module 中编译验证；vet/mod/gofmt、Docs
（38 份 Markdown）、Capability Matrix（49 项、稳定级 0 项）、License（35 个 Go dependency/57 个
distributed asset）、Branding 与 PublicTree 均通过。本切片未调用真实 API，也未新增 Schema、表、
Runtime、Store、Loop 或 Gateway。上述 57 个资产和 221.9 秒均是 E5-A 当时的历史证据；其 accepted
只证明 governed Knowledge 的单 Port Require/grant，当时下一入口为 E5-B，不能用来证明后续双 Port。

W2-E5-B 使用具有真实产品职责的固定模块
`freeagent.builtin.document-insight@2.0.0`：ArtifactDigest 为
`9cf2e60f4b6d30cfd93ea93245f4a6eadbd4f365b93f06b7decb389c4f4d4bfa`、size 为 1,097 bytes，
Adapter 为 `freeagent.adapter.document-insight/v1`。Manifest 有序提供 `action.provider/v1` 与
`context.provide/v1`，要求唯一 `model.generate/v2` 并请求唯一 `knowledge.read`；包只携带不可变
JSON，真实 RAG 与无外部副作用的 `text.stats` 实现均编译进 Core，不执行第三方包内代码。

集中产品验收从普通 Pure Chat Store 开始。Action-first Apply 以 `TARGET_CONFLICT` 零写拒绝；
Context→Action 两步 Apply 建立并复用唯一 Installation、Activation 和 Catalog instance。未绑定
Profile/Workspace 时 Provider 零加载；绑定后的同一 Run 首个 Model Compilation 同时冻结一条真实
Knowledge retrieval 与按冻结 Actions 精确重建的 `ActionResultReservation`，随后经唯一 Gateway
形成 2 个 Model Attempt 与 1 个 Action Attempt。两个 PortPlan 中由 Document Insight 贡献的两个
Binding 及 Action Attempt 的完整 `ActivatedModuleRef`（含 activation revision）相同；Context
PortPlan 另保留 `context.basic`。exact Chat retry 零新增 Run/Attempt。
Backup→verify→restore 后 RAG、Action 链和 MemberSnapshot canonical bytes 不变，恢复后的新 Run
继续消费双 Port。Context-first Disable 以 `PUBLICATION_FAILED` 零写拒绝；先 Disable Action 后仍
可真实 RAG，再 Disable Context 后 Catalog instance 消失并回到 Pure Chat；独立 CAS 竞争仅一个
winner。

最终源码上集中 E2E 连续 3 次通过；独立 `cmd/freeagent` package 输出为 185.746 秒、墙钟为
186.968 秒。该 E5-B 历史验收当时 30 个仓内 Go package 的全仓墙钟为 217.539 秒，其中 `cmd/freeagent` package 为
215.004 秒；另有 1 个 external compatibility package 由 SDK 嵌套测试编译验证。`go vet ./...`、
`go mod verify` 与 `gofmt -l .` 通过。Docs（38 份 Markdown）、Capability Matrix（49 项、稳定级 0 项）、
License（35 个 Go dependency / 59 个 distributed asset）、Branding 与 PublicTree 均通过。本切片未调用真实 API，未新增 Schema、表、Port、
Runtime、Store、Loop、Gateway、Catalog pointer、依赖图表、grant 表或效果账本；UNKNOWN 仍禁止
换 Provider、替代 Attempt 与语义重放。

`W2_E5_B_DOCUMENT_INSIGHT_DUAL_PORT_ACCEPTED_DEVELOPMENT_SLICE` 只证明上述固定受信产品切片。
Document Insight 向 Context 与 Action PortPlan 各贡献一个 Binding；Context PortPlan 同时保留既有
`context.basic`。它不是一次事务的双 Binding 批量 Apply，也不引入任意 Provider 的 reducer、merge、
fallback 或权重语义。任意模块组合的通用 multi-Port、任意
第三方进程内 Action、`knowledge.read` 之外的通用权限语言、REMOTE/WASM、不可信代码隔离、自动
发现/升级、在线控制面、Beta 与生产部署仍未完成；Operator Module Apply/Conformance 保持
`experimental`，`module.general-assembly` 保持 `planned`。

W3 在上述 W2-C 基础上完成了标签路由、独立 shortcut、严格 exact-question reuse、Memory
统一 Apply 与 Conversation Summary 持久复用；没有新增 Schema、表、ContentKind、Port、
Runtime、Store、Loop、Gateway、缓存服务或外部效果账本。完整备份当前对 Summary 唯一可能
出现的 Conversation compiler-owned model-1 执行确定性逐字节重编译；把同类 exact recompile
扩展到非 Conversation Pure Chat/Action/RAG/Memory/Composite 的全历史请求仍是非阻塞 P2，
不得据此宣称所有历史 `MODEL_REQUEST` 均已由 Backup 重编译。该 P2 不影响 W3 已闭合的
Conversation Summary 精确闭包，也不把 W4 Learning 提前变为可用能力。

W4-L1B 只把既有 `static-context/v1` 静态 Skill Draft 接入 W4-L1A 的同一 Proposal 表、
Admission、lineage、三轴去重和 Backup 门禁。已验收的 W4-L2 仍不新增 Runtime、Store、Loop、
Gateway、Port、ContentKind、Worker、Queue、Review 表或 Usage 账本；ReviewRequest 只是现有
`TASK_INPUT`，Verdict 只在现有 `MODEL_RESULT`，审核调用复用普通 Model Attempt/Usage/UNKNOWN。
Skill Draft 不进入 Context Compiler，审核也不会发布、安装、激活、绑定或扩权；因此 Pure Chat
零可选模块访问、轻量 Agent、共享 RAG 与统一外挂边界保持不变。

W4-L3 只为同一 Proposal 行增加一对一 Version 投影，并把 exact 模块包惰性导出到 Operator
交接目录；没有新增表、Runtime、Loop、Gateway、Port、Catalog pointer、Worker、Queue、模型
调用或外部效果。Version 不携带 Profile、Binding、Config、Trust、Authority、Secret 或发布
授权，Operator 仍需通过现有 dry-run/Apply 明确选择这些事实。未安装 Version 的完整备份只保存
权威 Proposal/Review/Version/Draft，并可在恢复后逐字节重建；它不会把临时 handoff 冒充活动
artifact。因此最初的轻量 Agent、共享知识与自由外挂方向不变，W4-L4 周期只会生成待后续审核的
无权限 Proposal，不会绕过这一交接边界。

W4-L4 只新增 `learning_cycle_schedules` 与 `learning_cycle_tasks` 两张 Current Store 投影表，
并复用现有 Universal Loop、Run/Attempt/Result/Usage、Proposal admission 与完整 Backup。Schedule
创建后默认关闭，Operator 以 exact revision 显式启用后，24 小时间隔只定义逻辑到期窗口；
产品命令仍须携带显式 UTC observation 执行一次 Tick。Tick 是 model-only proposer，只允许已配置的
Model Provider 调用及其 Secret 解析，不调用 Action/Module，也不授予 Apply、安装、激活、绑定、
Trust 或 Authority。
同一窗口的确定性身份和精确重入阻止重复 Run/Attempt/Proposal；UNKNOWN 证据对账后的遗漏投影可由
Store-only reconciliation 补齐，半开窗报告则只读生成 canonical bytes/digest 且不落盘。该切片没有
引入第二 Runtime/Store/Loop、常驻 Worker、Queue、后台 daemon 或自动治理控制面。

W5-X1 同样没有引入第二 Runtime、Store、Loop、Gateway、Scheduler 或 Usage 账本，也没有把
Workspace、Agent、Role、Memory、Knowledge 或 transfer 固化为不可替换的产品模式。legacy
同 Workspace Composite 与单次 S3-B Reviewer 继续保留；新 Decision 只在显式选择时冻结一次
repair 图，新 Workspace transfer 只在同 Tenant 双边 grant 下启用。每个 Run 的 Workspace
归属不变，撤权只影响新 Run；跨边界的两种 payload 均有界、类型化、内容寻址并进入完整
Backup。由此保留了轻量 Agent、共享授权 RAG、模块自由外挂、零可选模块 Pure Chat 和
UNKNOWN 禁止语义重放的最初设计。W5-F1 仅在这条既有纵链上补齐 Scheduler 与稳定性门禁，
没有新增第二套调度、传输、恢复或效果账本，也不扩大 W2-D 本地显式装配 v1 的窄 accepted
范围；Operator Module Apply 保持 `experimental`，广义通用装配与 W6/W7 保持 `planned`，
未部署边界不变。

## 使用规则

1. 新能力只有在真实产品消费者、失败与取消边界、恢复链和必要跨平台门禁同时成立后，
   才能进入 `accepted`。
2. `accepted` 不等于生产稳定、公开发布、SLA 或安全隔离承诺。
3. 缩窄范围时必须写在“当前边界与证据”中，不能用局部测试替代完整状态。
4. 状态变化只更新本清单和相应当前规格/验收证据；历史矩阵保持归档属性。
