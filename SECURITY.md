# Security policy

FreeAgent 仍处于早期开发阶段，目前不承诺生产 SLA 或长期安全支持窗口。

## 报告安全问题

请不要在公开 Issue 中提交尚未修复的漏洞、有效凭据、真实用户数据、数据库副本或可直接利用的攻击步骤。

仓库发布到 GitHub 后，请优先使用该仓库的 **Report a vulnerability** 私密安全报告入口联系维护者。报告中建议包含：

- 受影响的版本或源码快照；
- 可复现的最小配置和步骤；
- 预期边界与实际行为；
- 对 Tenant、Workspace、权限、预算、外部效果或数据保密性的影响；
- 已知缓解措施。

维护者会先确认收到报告，再根据影响范围决定修复、公告和版本安排。请在修复公开前给予合理协调时间。

## 特别敏感的边界

FreeAgent 的高风险边界包括：

- Workspace/Tenant 身份、ACL 和数据作用域隔离；
- SecretRef、模型 API Key 和渠道凭据；
- ToolGateway、显式 MCP 扩展和 Channel 的外部效果；
- 可信 `IN_PROCESS` 模块的构件身份、审核状态和 `CORE_TCB` 边界；不可信代码不得被隐式提升为进程内模块；
- `pure_chat` 默认不得隐式加载 Role/Persona、Knowledge/RAG、Memory、Skill、MCP、Tool 或扩展 SecretRef；
- `DispatchAttempt`、Outbox、`UNKNOWN` 与禁止语义重放；
- 预算、Usage、Cost 和缓存统计的完整性；
- SQLite 迁移、备份、恢复、Channel cursor 和隐私删除；
- 可执行 Skill、模块构件和远程端点的供应链。

## W2-U1/U2/U3/U4 模块来源、审核与窄 Apply 边界

U4 历史状态：`W2_U4_OPERATOR_APPLY_ACCEPTED_DEVELOPMENT_SLICE / W6_0_CONTROL_API_CONTRACT_NEXT`。
前序 U2 状态保留为 `W2_U2_DISCOVERY_SNAPSHOT_ACCEPTED_DEVELOPMENT_SLICE`，不由 U3/U4 改写。

当前 `module-verify` 的 governed 分支已完成 W2-U1 窄开发验收。没有 supply flag
时走原 legacy verifier；任一 supply flag 都显式选择只接受 `LOCAL_DIRECTORY + DENY` 的本地观察，
不会静默联网或回落为无策略验证。

- Source Policy、Publisher Key 与 detached Ed25519 Signature 必须从 artifact 外显式提供 canonical
  文件和 exact content ID；Manifest 或签名不能授予 runtime、Trust、Authority、Effect、Secret、
  Binding、安装或执行资格；
- Policy 的包大小上限同时约束首次和最终扫描，Module ID prefix 按点分段匹配；最终 digest/size 与
  directory identity 复验只能发现本次 preflight 内的漂移，不是跨调用 reservation；
- revoked Publisher Key 列表只是本次调用的 immutable deny snapshot，不是持久或全局 current
  revocation。unsigned direct verifier 会拒绝没有 Policy 锚点的 Key、Signature 或撤销输入；
- U1 拒绝 HTTPS Source Policy，也不会创建 Source 网络 transport。Windows 路径拒绝 UNC/device
  namespace、ADS 和 symlink/reparse point；这不代表可以识别已映射为普通盘符的网络盘；
- 成功报告仍是无权限的 `freeagent.module-package-verification/v1`，不产生 reservation、grant、stage、
  Store fact 或 Apply authority。现有手工 exact-grant Apply 不消费该报告作为授权。

W2-U2 已在唯一 Current Store 中闭合 SourcePolicy→Index→Snapshot observation parent 与 current
Publisher Key revocation。三个命令 `module-source-register`、`module-source-refresh` 和
`module-publisher-key-revoke` 均默认关闭并要求显式 `--enable-module-discovery`：

- Local 只允许固定 `root/index.json`，拒绝 UNC/device namespace、ADS、symlink/reparse 与观察中
  identity drift；HTTPS 另需独立开关、exact URL 与 exact HTTPS origin allowlist；
- HTTPS 不使用 proxy、redirect、retry、HTTP/2、keep-alive、cookie、凭据或 special-use/non-public
  地址。DNS 任一结果不安全即整次拒绝，不能靠挑选另一个地址绕过；
- Source I/O 前读取 exact Store basis，I/O 后在写事务内再次检查 Policy/Key revision、Source identity
  与 revocation；stale 或 revoked basis 零写；
- SourceID 不得改绑 Kind/OriginDigest；同一 Module ID + opaque exact Version 在 Snapshot、Installation
  与 materialized Learning Version 间不得对应不同 ArtifactDigest；
- U2 只观察 Index 并冻结 Snapshot，不下载 package、不验证 entry Signature、不生成 Candidate、Decision、
  reservation、stage 或 Apply。Backup/Verify/Restore 也不得联网、读取 Source 或获取 package。

W2-U3 只接通默认关闭、可信本地 Operator 显式触发的离线 `EXACT_VERSION_CHANGE` Review/Decision。
其历史状态为 `W2_U3_UPGRADE_REVIEW_ACCEPTED_DEVELOPMENT_SLICE / W2_U4_OPERATOR_APPLY_NEXT`；旧
NEXT marker 不再是当前入口：

- `module-upgrade-review` 和 `module-upgrade-decide` 都必须显式携带
  `--enable-module-upgrade-review`；关闭或参数不完整时不得打开 Store、读取 artifact/reason/signature，
  也不得构造 Host、Registry、Gateway 或网络；
- Review 必须显式选择 exact Tenant、PROFILE 或 WORKSPACE_CHANNEL_ENDPOINT scope、current
  Instance/Activation/ModuleRef/ArtifactDigest、Snapshot entry、target Instance 与本地 unpacked artifact。
  PROFILE 不得伪造 Workspace 归属；Version 是 opaque string，Core 不解释 SemVer、`latest` 或
  newer/downgrade；显式回滚只记录 `OPERATOR_REQUESTED_ROLLBACK`；
- Store 从 current Snapshot 重建 SourcePolicy→Index→Snapshot→entry parent，再沿 selected Binding→
  Catalog→exact Activation revision→Installation 重建 current。Review 提交前后都重验 current
  PublishedBasis；stale、异 digest、target Instance collision、未知 handler、权限扩大或任一 published
  Binding 不兼容均失败关闭或形成明确 `CONFLICT/UNSUPPORTED`；
- Operator 必须提供本地 target artifact；signed Source 另需 exact detached Signature。实现不读取 Index
  `PackagePath`、不从 Source 或网络下载。target canonical Manifest 被写为既有 `content_records` 的
  content-addressed evidence；Review/CLI 不保存或输出本地主机路径、URL、signature bytes、Secret、
  Config/Authority 正文，只保留安全摘要、引用与 digest-only grant requirements；
- `APPROVE` 只允许 current basis 仍匹配的 `WOULD_APPLY` Review；它只是 U4 输入，不是 reservation、
  grant、安装或 Apply authority。`REJECT` 必须显式设置 `--confirm-tenant-wide-reject`，按
  `{tenant_id, review_key}` 抑制该 Tenant 全部 scope 的同一供应候选，不得污染另一 Tenant；
- Candidate、Review 和 Decision 都不可变且进入同一 Backup/Verify/Restore semantic closure。签名 Key
  后续撤销不改写历史 Review，但会阻止依赖该 current Key 的新 Review/APPROVE；tampered parent、Manifest
  evidence、publication 或 Decision 必须拒绝恢复；
- U3 没有 package download、stage、Install、Activate、grant、Bind、Apply、Host、Run、Attempt、Usage
  或外部效果；Pure Chat 对三张 U3 表、artifact、signature、Source 与 handler assessor 保持零访问。

U4 已把原 CLI 私有的 9 条 generic/Model policy 与 2 条 Document Insight reserved exact selector
提取为 Review 与 Apply 复用的唯一 `internal/modulehandler` 共享实现；没有复制第二张表，也不建立
旁路授权。`module-upgrade-apply` 的安全边界是：

- 默认关闭；必须显式设置 `--enable-module-upgrade-apply`，绑定 exact Tenant、ReviewID 与 APPROVE
  DecisionID，并在服务停止、Store writer 独占时运行。关闭、partial flags 或非法值必须先于 Store、
  source、artifact、stage 或 Registry I/O 失败；
- 首片只接受 `LOCAL_DIRECTORY + DENY` 的显式 `--source-root`；不读取 Index `PackagePath`，不接受
  HTTPS、下载或隐式路径。`--signature` 必须显式出现，unsigned Source 使用空值；所有 artifact/endpoint/
  SecretRef grant flags 必须显式出现且为空；Manifest、
  Review 或 Decision 都不能自行授予权限；
- 唯一支持的影响是一个 PROFILE `context.provide/v1` Binding；target 必须精确为
  `DECLARATIVE + static/v1 + TRUSTED_INSTRUCTION + deny-all Authority`。Model、Channel、Action、
  `SINGLE`、shared-current、fanout、多 impact 或任何 grant 均失败关闭；
- staging 前必须依序完成 historical U1 governed source verification、current Store approval/source/
  binding revalidation、同一 exact plan 字节确认、current U1 verification 和 pre-stage revalidation。
  Review/Apply 只复用同一 pure evaluator，不存在独立 dry-run admission、隐式 approval authority 或
  第二份 handler policy；
- canonical replacement 只能在原 ordinal 将 old Instance 原位替换为 target；其他 Binding 字节与顺序
  不变。旧 Run 的冻结 facts 不得改写，新 Run 才能使用 target；
- 只有 publication 仍为相邻 current 时 exact retry 才返回原结果且不再读取 source 或 stage。这是
  adjacent plan idempotency，不是 durable approval receipt；任何后续 publication 后必须返回
  `POINTER_CONFLICT`；
- 无法证明 Apply 终态时保持 UNKNOWN，禁止语义重放、换 Provider、替代 Attempt 或伪造成功。
  Backup/Verify/Restore 必须保存 Review/Decision/Manifest/final publication 并零网络恢复；Pure Chat
  对 U4 facts/source/artifact/stage 保持零访问；U4 不新增表，在该历史切片收口时 Store 仍为 32 表。

任何把 U1 report、一次调用的 deny list、U2 Snapshot、U3 `APPROVE`、detached Signature 或相邻 retry
结果当成通用安装/执行权限或 durable receipt 的行为，都应按安全问题处理。

## W6-0 控制 API 纯合同历史安全边界

W6-0 当时状态：`W6_0_CONTROL_API_CONTRACT_ACCEPTED_DEVELOPMENT_SLICE / W6_1_APPLICATION_SERVICES_READ_API_NEXT`。
W6-0 只冻结六份 pure canonical wire 与 transport-neutral policy；它没有 listener、HTTP handler、在线
session store、SSE、UI、production `cmd/freeagent` 接线、Schema 或 receipt table。

- Web view 只能使用 `control-view-snapshot/v1` 与 `freeagent.control-view-snapshot/v1`；不得覆盖或
  混用 Core `control-snapshot/v1` 与 `freeagent.control-snapshot/v1`；
- request 必须显式区分 `DRY_RUN/MUTATE`，动态 request ID 与请求时间不得进入 semantic request。
  `UNKNOWN` 只能读取原 exact receipt，任何语义重放、替换 operation 或构造替代 receipt 都是安全问题；
- W6-0 时 loopback、Host/Origin、bootstrap handoff、session、CSRF、body/并发上限均只是后续实现必须满足的
  policy，不代表网络服务已经存在；W6-1/2 不得以这些合同冒充已完成的安全控制；
- W6-0 当时的 32 表 Store 没有 control receipt。该历史切片不授权任何在线写入；后续 receipt Schema
  验收也不能反向改写 W6-0 的安全边界。

## W6-1 默认关闭的本地 Control 安全边界

历史收口：`W6_1_APPLICATION_SERVICES_READ_API_ACCEPTED_DEVELOPMENT_SLICE / W6_2_CONTROLLED_MUTATIONS_AUDIT_NEXT`。

- 不传 `--enable-control` 时不得创建 Control listener、handoff、bootstrap/session registry、cursor registry
  或 Control Application Services，也不得因 Control 读取 Store；`--control-handoff-path` 单独出现必须在
  打开 Store 和 listener 前失败；
- 显式启用时必须先拒绝 Unix root 或 Windows elevated/admin token，复用唯一 Current Store，在关闭的
  shared Admission 后分别绑定 Chat listener 与 exact `tcp4 127.0.0.1:0` Control listener。actual origin、
  owner-only exclusive-create handoff 与 readiness 成功后才一次性开放两边 Admission，失败必须零接纳退出；
- bootstrap capability 5 分钟过期且只成功交换一次；session 与 CSRF 都是 process-local，session absolute
  lifetime 8 小时、idle lifetime 30 分钟。capability、credential 与 CSRF 不得进入 stdout、日志、URL、
  Store、Backup 或持久浏览器存储。host-only Cookie 不绑定 loopback 端口，因此除 bootstrap exchange 外，
  Modules GET 与所有非安全方法都必须同时携带 exact session 和 session-bound CSRF header；GET 缺失或
  错误 CSRF 必须返回 401/`UNAUTHENTICATED`，且不得调用 Application Service；
- W6-1 历史原子当时只开放 scope-filtered Modules list/detail 与 exact If-Match 的 `MODULE_DISABLE` Dry-run。Dry-run 拒绝
  `Idempotency-Key` 和 confirmation header，不调用 domain mutation，也不调用既有停机 CLI Apply/Disable；
  返回的 `DRY_RUN` receipt 只证明本次响应与 frozen request/basis，是 process-local response，不是 durable
  receipt；
- 该 W6-1 历史原子当时为 39 份 Markdown、44 个源码 package、44 个 production dependency-closure package；Store 是
  identity 不变的 32 表。没有 Control mutation、durable receipt table、SSE、UI、第二 Store/writer、后台
  worker 或主动预热。下一阶段只允许审计受控 mutation；在独立批准原子 durable receipt/domain fact 前，
  所有 mutation 路由必须继续不存在或失败关闭。

任何绕过这些边界的行为都应视为潜在安全问题，即使最终回复内容看起来正常。

## W6-2 confirmation 历史安全边界

该 confirmation 历史原子的状态：`W6_2_CONFIRMATION_CONTRACT_ACCEPTED_DEVELOPMENT_SLICE / W6_2_DURABLE_RECEIPT_SCHEMA_NEXT`。
审计历史收口为 `W6_2_CONTROLLED_MUTATIONS_AUDIT_ACCEPTED_DEVELOPMENT_SLICE /
W6_2_CONFIRMATION_CONTRACT_NEXT`。

- 首 mutation 候选仅允许 TENANT scope、PROFILE target、exact `context.provide/v1`、`OPTIONAL`、
  `DECLARATIVE`、trusted-instruction config 与 deny-all Authority 的 `MODULE_DISABLE`；Model/Action/Channel、
  Workspace endpoint、`REQUIRED`、grant、path、artifact、network、Provider 与 runtime refresh 均排除；
- `ControlConfirmationStatementV1` 绑定 principal、所需 capability、MUTATE、operation、exact scope/key/input、
  `operation_evaluation_digest` 与 expected ref。raw proof 不得充当 `confirmation_digest`，否则 challenge
  刷新或进程重启会破坏稳定 request/idempotency identity；
- `ModuleDisableEvaluationV1` 只冻结 effect-free 候选及 exact Binding/catalog 差异，不执行 publication；
- raw proof 固定 32 random bytes、最多 2 分钟且不超过当前 session absolute expiry、全局最多 256、每 session 最多 8。registry 只保存
  domain-separated proof digest，并绑定当前 Boot/session/principal/auth revision/scope-set/capability/scope/
  Statement/Request/expiry/state；raw proof 与 proof digest 均不得进入 Store、Backup、日志、URL 或错误；
- proof claim 后，只有证明零持久效果才能 release；存在任何可能效果或 commit ambiguity 必须 consume。
  未来 exact retry 必须先查 durable receipt，同 digest 直接返回原 receipt，不要求旧 proof/session；
- 当时为 39 份 Markdown、45 个源码 package、44 个 production dependency-closure package、32 表 Store。
  没有 confirmation HTTP endpoint、durable receipt row、mutation route、Schema/Backup 变化、SSE/UI/worker；
  纯 SQLite 首片 `UNKNOWN` 不可达。

`W6_2_DURABLE_RECEIPT_SCHEMA_NEXT` 当时只允许审计一张 append-only receipt 表的 32→33 rebuild-only 候选、
exact canonical closure、容量/唯一键/同事务 publication 与 Backup/Restore；在该历史 Schema 原子单独验收前不得接通写路由。

## W6-2 durable receipt Schema 历史安全边界

历史收口状态：`W6_2_DURABLE_RECEIPT_SCHEMA_ACCEPTED_DEVELOPMENT_SLICE /
W6_2_MODULE_DISABLE_MUTATION_WIRING_NEXT`。该状态只批准持久化结构和验证边界，不批准在线 mutation。

- 该历史原子当时的 Store 精确为 33 表；只增加 STRICT、append-only `control_operation_receipts`。当时
  fingerprint 是 `51be9081a815e6379380b93e745db03d33ca2f358962f85ace63bce2534dcc10`，migration 是
  67,998 bytes / SHA-256 `8feea38beb505ab4f371afe32eecd877744062bd149d0d24982839c5450fc952`；
- 唯一 durable identity 必须从 canonical Request 的 principal/scope/operation/idempotency-key digest
  派生；exact resolver 只有在 request digest 相同才返回原 receipt，同 identity 的不同 Request 必须冲突；
- 该历史原子的公开 commit 只允许当时 current basis 上经 neutral evaluator 重放闭合的 `NO_CHANGE`。它不能发布
  Control/Catalog、不能插入 `APPLIED`、不能声称 domain effect；
- SQL 必须保持 append-only、same-Tenant Control/Catalog parent、Catalog→Control pair、identity 与 domain
  ID/digest 唯一，并执行 `8/8/64/16/8/8/128 KiB` canonical ceiling、`256 KiB` 总量、Tenant 1,024 与
  Store 8,192 quota；
- 在该历史原子中，`APPLIED` 只有严格 domain receipt/restore/verifier 与 DDL 形状，没有公开 insert；下一
  mutation wiring 必须由唯一 Store publication owner 在同一 `BEGIN IMMEDIATE` transaction 内完成 domain receipt、Control
  receipt 与 Control/Catalog pointer CAS；事后 backfill 属于安全缺陷；
- semantic verifier 只读取 coherent snapshot，严格恢复每个 canonical、验证不可变 parents 并重放 neutral
  evaluator；Backup Create/Verify/Restore 均调用它。单一 receipt 表没有外部 completeness anchor，因此只能
  验证现存行，不能声称检测整行删除；
- 该历史原子的证据为 39 份 Markdown、47 个源码 package、46 个 production dependency-closure package、
  33 表 Store；当时没有 mutation route/handler、confirmation endpoint、SSE/UI/worker；proof/proof digest/session 仍不得进入
  Store、Backup、日志、URL 或错误。

## W6-2 MODULE_DISABLE mutation wiring 历史安全边界

历史收口状态：`W6_2_MODULE_DISABLE_MUTATION_WIRING_ACCEPTED_DEVELOPMENT_SLICE /
W6_3_WEB_SHELL_READ_ONLY_OVERVIEW_NEXT`。

- Control 仍默认关闭；只有显式 `--enable-control` 才初始化 confirmation registry/coordinator，并注册
  `POST /control/api/v1/modules/disable/confirmation` 与
  `POST /control/api/v1/modules/disable/mutate`。没有第二 mutation flag、第二 Store/writer 或 sibling write route；
- 唯一允许的 domain 形状是 TENANT scope、PROFILE target、exact `context.provide/v1`，且被移除的 existing
  Binding 必须为 `OPTIONAL`、`DECLARATIVE static/v1`、trusted-instruction config 与 deny-all Authority。
  Workspace、Model/Channel/Action、`SINGLE`、shared-current、fanout 和其他 operation 都失败关闭；
- confirmation 稳定绑定 evaluation/Statement/Request digest；raw 32-byte proof 仍是最多 2 分钟且不超过当前 session absolute expiry 的 process-local
  authority，不能持久化或记录。mutation 的 durable resolver 在 proof/current-basis 前执行：same identity+
  same request digest 返回原 exact receipt，不依赖旧 proof 或旧 session identity；每次 HTTP 仍要求重新通过
  当前 exact Origin、session、session-bound CSRF 与 TENANT Permit，不能匿名调用 resolver。
  miss 才 claim proof，并在效果提交前复验 current authorization 与 exact basis；
- `NO_CHANGE` 与 `APPLIED` 都只通过唯一 Current Store 提交 durable Control receipt。`APPLIED` 的 exact
  domain receipt、Control/Catalog publication 与 pointer CAS 在同一 `BEGIN IMMEDIATE` transaction 内原子
  提交；纯 SQLite 首片 `UNKNOWN` 不可达。任何 commit ambiguity 只能通过 exact resolver 裁决；
- Backup Create/Verify/Restore 覆盖现存 APPLIED receipt 的 exact roundtrip 与 parent/replay/domain tamper。
  该 verifier 仍没有 external completeness anchor，不能宣称发现任意整行 receipt 删除；
  publication-without-receipt 的原子性证据是强制 receipt insert 失败时整个 publication transaction rollback，
  不是全表 absence 证明或事后 backfill；
- 该历史原子当时有 39 份 Markdown、48 个源码 package、48 个 production dependency-closure package、33 表 Store；
  fingerprint/migration 为 `51be9081a815e6379380b93e745db03d33ca2f358962f85ace63bce2534dcc10`、
  67,998 bytes / `8feea38beb505ab4f371afe32eecd877744062bd149d0d24982839c5450fc952`。
  没有其他 mutation、SSE/UI/worker、Provider/Gateway dispatch 或主动预热。

## W6-3 Web Shell / read-only Overview 历史安全边界

历史状态：`W6_3_WEB_SHELL_READ_ONLY_OVERVIEW_ACCEPTED_DEVELOPMENT_SLICE /
W6_4_MODULES_CONFIGURATION_UI_NEXT`。

- Control 继续默认关闭；未显式 `--enable-control` 时不构造 Overview service/static resolver，不暴露
  `/control/ui/` 或 `/control/api/v1/overview`，也不增加 listener、Store 或 writer；
- 显式启用后，static routes 只解析 exact embedded asset 名称，拒绝路径穿越与 source map；响应使用
  `no-store`、严格 CSP、`nosniff`、same-origin/deny framing 等既有安全 headers；
- Overview GET 仍要求 exact same-origin session、session-bound CSRF 与 TENANT/WORKSPACE Permit。服务在读取后、
  编码前再次验证 Permit/scope、current basis、section/projection digests、ownership、排序、状态机、上限与
  strong ETag；跨 Tenant/Workspace、未来时间、伪造 truncation 或自洽重签 tamper 都失败关闭；
- Web Shell 只从 server-authorized scopes 和 current Workspace refs 构造 selector；query key 含完整 scope。
  rotating resume credential 仅在 tab-scoped `sessionStorage`，CSRF 仅在内存；无 localStorage authority、
  durable client cache、业务 mutation、SSE 或 worker；
- 该 W6-3 历史切片 Store 为 41 tables / 23 indexes / 56 triggers，fingerprint
  `87d63a2e468ee27ee2c0e1918a92e580532825bde85dbb872b9c795bf577f2c1`，migration 143,588 bytes /
  SHA-256 `5bd9743e988b82750f785c015f07c11fbcf6025da70148405aec11106b4efb22`。新增 observation facts 仅由既有
  production lifecycle 同事务发布并由 Backup semantic closure 验证，不构成第二 authority；
- `W6_4_MODULES_CONFIGURATION_UI_NEXT` 是该切片当时的下一入口；其他 mutation、SSE、远程 listener、
  第二 Store/writer、自动升级或后台 worker 均不由 W6-3 授权。

## W6-4 Modules configuration UI 历史安全边界

历史状态：`W6_4_MODULES_CONFIGURATION_UI_ACCEPTED_DEVELOPMENT_SLICE /
W6_5_SERVER_OWNED_MODULE_ARTIFACT_INGRESS_NEXT`。

- Control 继续默认关闭；W6-4 不新增 route、Application Service、Store seam、Schema、writer、listener 或
  publication owner，只消费既有 Modules list/detail 与 `MODULE_DISABLE` 三路 POST；
- list/detail 与 operation 每次都要求 exact Origin、当前 session、session-bound CSRF、scope Permit 与当前
  PublishedBasis。UTF-8 scope/instance carrier 必须 canonical raw-base64url；未知、重复、noncanonical 或
  route-scoped marker 漂移在 authority/admission 前失败关闭；
- 只有 TENANT/PROFILE、`OPTIONAL`、`context.provide/v1`、DECLARATIVE/static、trusted-instruction、deny-all
  的现有 Binding 可显示写控件。浏览器候选、DOM、Overview、历史 receipt 与 digest/ref 均不是 authority；
- UI 必须先 dry-run；只有同一 exact body/If-Match 的 `WOULD_APPLY` 可请求最多两分钟的 confirmation，并在
  原样摘要后要求第二次显式确认。proof 首次发送即从可重用状态清除；不可判定结果只允许无 proof 的 exact
  body/key/If-Match/evaluation retry，禁止 replacement、rebase 或 optimistic publication；
- raw proof、idempotency key、CSRF 与 exact-retry body 不得进入 URL/hash、DOM、日志、error、query cache、
  session/local storage 或 durable client cache；sessionStorage 仍只保留 rotating resume credential；
- 该历史切片 Store identity 与 W6-3 相同。没有 artifact path/URL/signature ingress、staging/install/activation、grant、
  其他 mutation、SSE、worker、远程 Control、第二 Store/writer 或自动升级。

`W6_5_SERVER_OWNED_MODULE_ARTIFACT_INGRESS_NEXT` 是 W6-4 收口时的历史下一入口，不赋予 W6-4
任何 artifact ingress 或更宽发布权限。

## W6-5 server-owned module artifact ingress 当前安全边界

当前状态：`W6_5_SERVER_OWNED_MODULE_ARTIFACT_INGRESS_ACCEPTED_DEVELOPMENT_SLICE /
W6_6_SERVER_OWNED_MODULE_UPGRADE_REVIEW_ACCEPTED_DEVELOPMENT_SLICE`；当前下一入口为
`P2_CONTROL_UI_I18N_NEXT`。

- 唯一入口是默认关闭的可信本地 Operator CLI `module-artifact-ingress`；必须显式携带
  `--enable-module-artifact-ingress`。关闭、参数缺失或多余参数时不得打开 Current Store、读取 Source、
  发布 artifact 或构造 Runtime/Host。没有 HTTP route、upload、Web UI、远程 listener 或后台 worker；
- `--source-root` 与 `--artifact-root` 只是本次进程内的可信 CLI 路径输入，不得写入 Store、Backup、日志、
  canonical Admission 或返回为 authority。调用方只能给出 Source/Snapshot/Module/ArtifactDigest selector，
  不能给出 package path、URL 或 signature；package path 必须由 Store-owned current Snapshot entry 提供；
- Source package、artifact tree 与 Backup bundle 的读取必须从已打开并固定 identity 的 root handle 开始；每个
  descendant 都相对仍持有的 parent handle 打开。symlink/junction/reparse、普通文件 hardlink、跨 filesystem/volume、
  identity/size/change-time 漂移与目录 namespace 漂移都失败关闭，禁止先检查后按绝对路径重开；
- Store 必须重建 exact current SourcePolicy→Snapshot→entry closure，且只接受 unsigned
  `LOCAL_DIRECTORY + DENY`。Signature-required policy、带 SignatureID entry、非本地 Source、网络允许策略、
  stale head、Module/version/digest/size 不一致或路径与 root identity 漂移都失败关闭；
- 文件系统是第一 durable 线性化阶段：构件经完整 conformance/digest/size/file-count 验证后写入
  artifact-root 的隐藏 stage，sync 后以 digest 为目录名 no-replace 发布；已存在目录只能在完整复验相同时
  复用。artifact-root、stage、每个 child 及全部 namespace ancestors 都必须满足 owner/private/non-replaceable
  平台边界；跨进程 root lease 覆盖 orphan-stage recovery、物理配额扫描、stage、publish、sync 与最终复验。
  root 最多保留 256 个 digest tree、512 MiB covered content、32,768 个 artifact-relative path、16,384
  个普通文件与 16 MiB canonical path-name bytes，单文件最多 64 MiB；这些是内容与 namespace 硬预算，
  不声称等于文件系统实际 allocation blocks；
- 新 ingress 在同 digest 目标缺失时必须始终以目录 `0700`、文件 `0600` 发布。既有同 digest 目标只有在完整
  bytes/mode 复验后才可复用，且只接受两种 root-global 闭包：全部目录 `0700`、全部文件 `0600`；或全部
  目录 `0700`，仅 canonical `LOCAL_PROCESS + mcp-stdio/2025-11-25` descriptor 精确绑定的唯一 executable
  文件为 `0700`，其余文件 `0600`。两种闭包都拒绝 special/setid/sticky 与任意 group/world 权限。artifact
  root 跨 Store 共用时，这一模式判定不得依赖任一单独 Store 的 Installation；后一种只是既有物理兼容态，
  不授予当前 Store Installation、Activation 或 execution authority。W6-5 的 `inert` 只表示无 Store authority
  且本切片不执行，不等同于 OS executable bit 必须不存在；
- Store commit 结果不明时不得猜测删除已发布对象；它保持无 authority 的有界 inert orphan。只有已经存在的 exact
  Admission 才能在 Snapshot head 前进后按原 selector 恢复结果；未提交的 stale selector 不能获得 authority。
  以后任一当前、合格且声明同一 digest 的 exact selector都只能在重新完成全包复验后复用这些 bytes；
- 文件发布成功后，唯一 Current Store 在一个 `BEGIN IMMEDIATE` 中重新验证 basis，并原子写入不可变
  `module_artifacts` 与 append-only `module_artifact_admissions`。Admission 只记录 supply observation，绝不授予
  installation、activation、Binding、execution、Authority、Secret、effect、grant 或 Review；
- Backup artifact closure 是既有 Installation artifacts 与 ingressed artifacts 的去重并集。Create/Verify/Restore
  必须离线验证 Store/manifest/artifact closure；即使 Source 已移除且没有 Installation，也必须恢复同一 inert
  Artifact 与 Admission，且不得联网、重开 Source、Install、Activate 或 execute。临时 DB、bundle 与 restore
  tree 必须在可信私有 staging 中创建，所有 copy 只从 held handle 读取，并在 publish/cleanup 后同步相关 parent；
- W6-5 历史 FAC1 Store 为 43 tables / 25 explicit indexes / 64 triggers，fingerprint
  `47981b9bf147ec81c6e98382f281db0d5395187a1f090f7ce183c116fba82a0d`，migration 150,301 bytes /
  SHA-256 `6def433a59fa8f4876894572f1610cae499abc5b389ab5930ea697055419ca86`。当前 FAC2 Store 为
  UserVersion 2、42 tables / 26 explicit indexes / 64 triggers，fingerprint
  `d5d876f327dc29dc6f4a10476652641172ab8e1f0451a8714fc450f58733541e`；`0001` 保持字节冻结，
  `0002_server_owned_review.sql` 为 7,173 bytes / SHA-256
  `3091a49ebcf724f573f91cc0fd22a7c58ebb52fa9d7ed552e32b6526ebeca3cb`。

`W6_6_SERVER_OWNED_MODULE_UPGRADE_REVIEW_NEXT` 是 W6-5 收口时的历史 marker。W6.6 当前只允许
默认关闭的可信本地 `module-upgrade-review-server-owned` 与 `module-upgrade-decide-server-owned`：
Review/Decision 从已接纳 Admission 重建 basis，持久化并 exact retry；调用方不能提供 artifact path、URL、
signature bytes 或 target facts。跨 tenant、stale basis、Artifact tamper 和不合格 Decision 均失败关闭，
且不自动 Install、Activate、Bind、Grant、Apply、Execute，不调用 Provider。
