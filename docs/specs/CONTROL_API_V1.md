# CONTROL_API_V1

> Current phase override (2026-09-24): P2 Control UI i18n, the exact `zhipu` / `glm-4.5` Provider, and the read-only P4 management UI are accepted development slices; the current next gate is `P5_BETA_GATE`. `v0.1.1` is a published Developer Preview, not production support or a public Beta.

状态：`W6_5_SERVER_OWNED_MODULE_ARTIFACT_INGRESS_ACCEPTED_DEVELOPMENT_SLICE / W6_6_SERVER_OWNED_MODULE_UPGRADE_REVIEW_ACCEPTED_DEVELOPMENT_SLICE / P2_CONTROL_UI_I18N_ACCEPTED_DEVELOPMENT_SLICE / P3_SECOND_PROVIDER_ACCEPTED_DEVELOPMENT_SLICE / P4_MODULE_MANAGEMENT_UI_READ_ONLY_SLICE_ACCEPTED_DEVELOPMENT_SLICE / P5_BETA_GATE`；W6-4 的默认关闭 Control/Web Shell、strict Modules list/detail 与唯一 `MODULE_DISABLE` UI 继续作为历史 accepted 边界。W6-5 不新增任何 Control operation、HTTP route、upload、UI、listener、Application Service 或在线 authority；它只新增独立、默认关闭的可信本地 Operator CLI，接纳 Store-owned current Snapshot 中 unsigned `LOCAL_DIRECTORY + DENY` exact entry 为 server-owned inert Artifact。W6.6 只从该 Artifact/Admission 生成持久 Review/Decision，调用方不提供 artifact path、URL、signature bytes 或 target facts；不自动 Install、Activate、Bind、Grant、Apply、Execute，也不调用 Provider。W6-2/W6-3 的 confirmation、durable receipt、同事务 publication 与 read-only Overview 边界保持不变；无其他 mutation、SSE、后台 worker或第二 Store/writer。
规范词：本文中的“必须”“不得”“应当”均为实现约束。

## 1. 目标与边界

本规格冻结 Control Plane 的纯数据合同、授权边界和威胁模型，并记录 W6-1 已验收的窄生产消费链、W6-2
confirmation/receipt/mutation wiring、W6-3 Web Shell/read-only Overview 与 W6-4 strict Modules UI；W6-5
只在本文冻结“不得经 Control 暴露 artifact ingress 或 W6.6 Review/Decision mutation”的跨层边界。Control 是 Core
之上的可选观察与运维入口，不是第二 Runtime、第二 Store、第二 writer、第二
Gateway 或新的事实源。

W6-0 只定义六份主合同：

1. <code>control-session/v1</code>；
2. <code>control-scope/v1</code>；
3. <code>control-view-snapshot/v1</code>；
4. <code>control-operation-request/v1</code>；
5. <code>control-operation-receipt/v1</code>；
6. <code>control-event-cursor/v1</code>。

源码中已有的 <code>control-snapshot/v1</code> 仍是
<code>internal/controlcontract.ControlSnapshot</code> 的权威 desired-configuration
合同。它的名称、摘要域和 wire bytes 均不得改变。Control API 的 scope-filtered、
脱敏观察投影只能使用 <code>control-view-snapshot/v1</code>；该投影不得作为
Control/Catalog 发布输入，也不得伪装成 Core 快照。

W6-0 明确不做：

- 不启动 listener，不增加路由、HTTP handler、SSE 或 React 资产；
- 不增加 Store 表、migration、receipt 表、后台 worker、队列或主动预热；
- 不开放远程监听、用户名密码、公网 TLS 终止或多用户管理平台；
- 不把 Carbon 的 Task、Board、Worker 或 Work Log 语义引入 Core；
- 不读取 Secret material、原始模型正文、RAG/Memory 正文、主机路径或任意
  content-addressed bytes。

因此在 W6-0 历史原子收口时，Store 仍是 32 张 ordinary table，既有 schema fingerprint、
migration bytes 和 Backup/Restore 语义均保持不变；这不是当前 W6-5 identity。

W6-0 的历史收口为
<code>W6_0_CONTROL_API_CONTRACT_ACCEPTED_DEVELOPMENT_SLICE /
W6_1_APPLICATION_SERVICES_READ_API_NEXT</code>，当时证据是 39 份 Markdown、37 个源码 package、
35 个 production dependency-closure package。W6-1 的历史收口为
<code>W6_1_APPLICATION_SERVICES_READ_API_ACCEPTED_DEVELOPMENT_SLICE /
W6_2_CONTROLLED_MUTATIONS_AUDIT_NEXT</code>，当时证据是 39 份 Markdown、44 个源码 package、
44 个 production dependency-closure package。W6-2 audit 随后以
<code>W6_2_CONTROLLED_MUTATIONS_AUDIT_ACCEPTED_DEVELOPMENT_SLICE /
W6_2_CONFIRMATION_CONTRACT_NEXT</code> 收口；confirmation 原子为 39 份 Markdown、45 个源码
package、44 个 production dependency-closure package。上述阶段都保持同一 32 表 Store identity；
W6-1 的 process-local session/cursor/<code>DRY_RUN</code> receipt 与 W6-2 的 proof registry 都不持久化。
receipt Schema 历史原子为 39 份 Markdown、47 个源码 package、46 个 production dependency-closure
package 与 33 表 Store。后续 mutation-wiring 历史原子为 39 份 Markdown、48 个源码 package、48 个
production dependency-closure package；当时 Store 仍为 identity 不变的 33 表。

## 2. 公共编码规则

六份合同及其辅助结构必须满足：

- wire 为单个 UTF-8 JSON object；拒绝未知字段、重复 key、尾随 JSON、
  非有限数字和非 canonical 输入；
- 服务端冻结或持久化摘要前使用项目统一 Canonical JSON；摘要为带独立 domain
  separator 的小写十六进制 SHA-256；
- opaque ID 为 1–256 bytes、UTF-8/NFC、无首尾空白和控制字符；客户端不得解析
  服务端签发的 ID、ETag、cursor 或 receipt ref；
- wire 时间统一为 JSON-safe Unix microseconds；服务端时间、correlation ID 和传输重试次数不得进入
  semantic request digest；
- 数组必须是非 <code>null</code>；set 语义数组按规范键排序并拒绝重复，sequence
  语义数组保留冻结顺序；
- decode、restore 和 getter 必须 defensive-copy byte slice、raw JSON 和数组；
- schema version、enum、digest domain 或排序键不认识时失败关闭，不做兼容猜测；
- 合同不得携带 Cookie、bootstrap capability、CSRF token、Secret/SecretRef material、
  Authorization、Config/Authority 正文、原始 prompt/result、主机绝对路径、SQL、
  stack trace 或环境变量。

辅助 exact ref 使用下列最小形态：

~~~text
ExpectedResourceRefV1 = {
  kind,                // operation 固定的有限 enum
  resource_id,
  revision,            // JSON-safe；Learning Proposal 首次审核允许 exact 0
  digest               // 该 revision 的 exact authoritative digest
}
~~~

同一 operation 必须固定 ref 的 kind 与转换规则，不能让客户端任选 identity 强度。
除 <code>LEARNING_PROPOSAL_REVIEW</code> 的 <code>SUBMITTED/0</code> 外，当前六项
operation 的 expected revision 必须为正数。

## 3. 六份主合同

### 3.1 control-session/v1

<code>control-session/v1</code> 是服务端认证结果的只读元数据，不是 bearer
credential：

~~~text
ControlSessionV1 = {
  schema_version: "control-session/v1",
  boot_id,
  session_id,                 // 公开元数据，不是 bearer credential
  principal_id,              // 只由服务端认证结果产生
  scope_set_digest,
  authorization_revision,
  capabilities: [],          // 排序、去重的本地授予 enum
  issued_at_unix_micros,
  expires_at_unix_micros
}
~~~

客户端不得提交或覆盖 <code>principal_id</code>、capability 或 scope ceiling。
Session 只存在于当前进程内存，进程重启、Control listener 关闭、idle expiry、
absolute expiry 或显式注销后立即失效；它不进入 Current Store、日志、Backup、
URL 或 localStorage。Cookie 中只保存随机 session credential；JSON 中的
<code>session_id</code> 不具备认证能力。<code>scope_set_digest</code> 绑定服务端冻结的
exact scope ceiling set，授权检查仍须读取进程内 session registry 并复验 revision。
absolute lifetime 最大 8 小时，idle lifetime 最大 30 分钟；wire 的
<code>expires_at_unix_micros</code> 不得超过该 8 小时上限。

### 3.2 control-scope/v1

每个请求都必须携带 exact scope；UI 当前选中项不构成权限：

~~~text
ControlScopeV1 = {
  schema_version: "control-scope/v1",
  tenant_id,
  kind: "TENANT" | "WORKSPACE",
  workspace_id                 // 仅 WORKSPACE 必填
}
~~~

- <code>TENANT</code> 必须省略 <code>workspace_id</code>，且只允许拥有显式
  tenant-wide capability 的 session 使用；
- <code>WORKSPACE</code> 必须包含一个 exact Workspace ID；多个 Workspace 的 ceiling
  由 session registry 的 canonical scope set 管理，不把 wildcard/prefix/set 语义塞入单一 scope；
- scope digest 使用 <code>freeagent.control-scope/v1</code> 对完整 canonical object 计算；
- 请求 scope 必须是 session ceiling 的子集；查询还必须逐资源复验 Tenant、
  Workspace 和 capability，不能只检查入口 scope；
- 不可见与不存在的资源必须使用同一 non-enumerating 结果；跨 Workspace edge
  仅在两端均获授权时返回；
- scope 不能由路径、Host、前端路由、Cookie、旧 query cache 或 resource ID
  前缀推断。

### 3.3 control-view-snapshot/v1

该合同是瞬时、安全、scope-filtered 的观察基线：

~~~text
ControlViewSnapshotV1 = {
  schema_version: "control-view-snapshot/v1",
  scope: ControlScopeV1,
  scope_digest,
  observed_at_unix_micros,
  basis,                     // safe exact PublishedBasisRefV1 projection
  sections: [{
    kind,                    // endpoint 固定的有限 enum
    source_revision,
    source_digest,
    item_count,
    truncated
  }],
}
~~~

<code>sections</code> 按 <code>kind</code> 排序并拒绝重复。W6-1 为具体资源
另定义窄 DTO；本合同只汇总授权后的 refs、revision 和 count，不能塞入任意
<code>map[string]any</code>、raw row 或正文。

合同 ID 使用 <code>freeagent.control-view-snapshot/v1</code> 对完整 canonical
object 计算。强 ETag 另由 W6-1 的 scoped page projection 计算，必须覆盖 source basis、
scope、filter、sort 与 semantic response bytes；观察时间不得被单独冒充资源 revision。

该对象不持久化、不进入 Backup、不授予能力，也不能替代
<code>control-snapshot/v1</code>、Current pointer、RunManifest 或任何 domain
receipt。

### 3.4 control-operation-request/v1

该合同统一 Dry-run 与未来 mutation 的稳定语义输入：

~~~text
ControlOperationRequestV1 = {
  schema_version: "control-operation-request/v1",
  principal_id,              // 服务端注入
  capability,                // operation registry 导出
  intent: "DRY_RUN" | "MUTATE",
  operation,                 // 服务端 registry 中的 exact enum
  scope: ControlScopeV1,
  scope_digest,
  idempotency_key_digest,    // 仅 MUTATE 必填
  input_digest,              // operation-specific typed body 的摘要，不携带正文
  operation_evaluation_digest, // 仅 MUTATE 必填；绑定向用户展示的稳定候选
  expected_ref,
  confirmation_digest        // 仅 MUTATE 必填；必须是下述 Statement 摘要
}
~~~

原始 operation input 最大 1 MiB，必须先由 operation 选择 closed typed decoder，
通过对应 domain 合同验证后只把摘要写入本 envelope；不允许通用 map、自由 SQL、
路径、URI、Secret 或任意 content bytes。

合同 ID 使用 <code>freeagent.control-operation-request/v1</code> 对完整 canonical
object 计算。<code>DRY_RUN</code> 必须同时省略 idempotency-key、operation-evaluation 与 confirmation
digest，且只能产生无效果的 Dry-run receipt；<code>MUTATE</code> 必须同时携带三项有效摘要，且
<code>confirmation_digest</code> 必须等于从同一 Request 其余稳定字段重建的 exact
<code>ControlConfirmationStatementV1</code> 摘要，不得接受任意 challenge/proof 摘要。
<code>MUTATE</code> 不得产生 Dry-run receipt。同一 body 在两个 intent 下必须得到不同 request digest。
Transport 的原始 <code>Idempotency-Key</code>、Cookie、CSRF token、服务端 request ID、
correlation ID 与服务器时间不进入 wire；只在 MUTATE 中保留 key digest。HTTP 层只能把强
<code>If-Match</code> 和 expected revision 规范化进 <code>expected_ref</code>；
body 不得覆盖 header 前置条件。审计时间进入 receipt 或 transport-local audit，
不得改变同一 principal/scope/operation/intent/body/basis 的 semantic request digest。

W6-0 不实现 consumer；W6-1 已仅由 Modules 只读与 <code>MODULE_DISABLE</code> Dry-run
Application Service 消费这些 inert facts。W6-2 confirmation 原子仍未接 mutation consumer。在
W6-2 单独通过持久 receipt Schema 与原子性设计前，mutation
路由必须不存在或返回有限禁用错误，不得调用 domain mutation。既有停机 CLI Apply/Disable
属于相邻既有路径，不能作为 Control mutation consumer。

#### 3.4.1 control-confirmation-statement/v1

<code>control-confirmation-statement/v1</code> 是用户确认的稳定 mutation 含义，不是 bearer proof：

~~~text
ControlConfirmationStatementV1 = {
  schema_version: "control-confirmation-statement/v1",
  principal_id,
  capability,                  // operation 固定要求
  intent: "MUTATE",
  operation,
  scope: ControlScopeV1,
  scope_digest,
  idempotency_key_digest,
  input_digest,
  operation_evaluation_digest,
  expected_ref
}
~~~

摘要域固定为 <code>freeagent.control-confirmation-statement/v1</code>，canonical wire 上限 8 KiB。
Statement 必须逐字段绑定 principal、所需 capability、<code>MUTATE</code>、operation、exact scope 及其
digest、idempotency-key digest、typed input digest、稳定 operation evaluation digest 与 exact expected
ref。它故意排除 raw proof/challenge、Boot ID、Session ID、authorization revision、scope-set digest、
Cookie、CSRF、请求时间和 transport metadata；这些动态事实只能由当前进程的 proof authority 复验，
不得使同一稳定 Request 在 challenge 刷新或重启后改变 request digest。

#### 3.4.2 control-module-disable-evaluation/v1

首候选的稳定展示合同固定为 <code>control-module-disable-evaluation/v1</code>，摘要域为
<code>freeagent.control-module-disable-evaluation/v1</code>，canonical wire 上限 64 KiB。它绑定
<code>MODULE_DISABLE</code>、typed input digest、exact expected published pointer ref 与完整
<code>ModuleDisableProjectionV1</code>，并失败关闭验证 <code>NO_CHANGE</code>、
<code>ALREADY_APPLIED</code>、<code>WOULD_APPLY</code> 三种投影矩阵、相邻 pointer/control/catalog
revision、exact Binding removal 与 catalog retain/remove。它不包含 observed/completed time、Dry-run
receipt/request digest、Boot/session/auth revision、raw proof、路径、artifact、网络、Provider 或 runtime
refresh。该合同当前只证明用户看到的候选可稳定寻址，不执行 publication。

### 3.5 control-operation-receipt/v1

该合同是操作结果的安全证明 envelope，不天然意味着新增 Store 行：

~~~text
ControlOperationReceiptV1 = {
  schema_version: "control-operation-receipt/v1",
  request_digest,
  idempotency_key_digest,
  principal_id,
  scope_digest,
  operation,
  intent,
  status: "DRY_RUN" | "NO_CHANGE" | "APPLIED" | "REJECTED" | "UNKNOWN",
  error_code,
  pre_ref,
  post_ref,
  domain_receipt,
  replay_disposition,
  completed_at_unix_micros
}
~~~

Receipt 不得复制 Config、Authority、Secret、请求正文、模型正文、主机路径或
外部效果状态。<code>UNKNOWN</code> 必须引用原 domain attempt/receipt；它不是
“可重试”状态。

Receipt 必须由已经 canonical-freeze 的 exact <code>ControlOperationRequestV1</code>
构造，不能由调用方再次抄写请求字段。读取或恢复 Receipt 时，消费方也必须先按
<code>request_digest</code> 解析同一权限边界内的 exact frozen Request，再逐字段复验
<code>principal_id</code>、<code>scope_digest</code>、<code>operation</code>、
<code>intent</code> 和 <code>idempotency_key_digest</code>；Receipt 带
<code>pre_ref</code> 时还必须与 Request 的 <code>expected_ref</code> 绑定。Receipt
自身只能验证 <code>request_digest</code> 的 SHA-256 字段形态和本 envelope 的内部约束，
不能只靠该摘要自证它引用了哪一份 Request。exact Request 缺失、摘要不匹配或任一重复
字段不一致时必须以 <code>INTEGRITY_FAILURE</code> 失败关闭，不得返回、缓存或重放该
Receipt。W6-0 只冻结这项消费方义务，不实现 Request/Receipt resolver。

状态、intent、幂等键、效果与 replay 必须满足完整闭包：

| status | intent | idempotency-key digest | 新效果与 refs | replay disposition |
|---|---|---|---|---|
| <code>DRY_RUN</code> | 仅 <code>DRY_RUN</code> | 必须省略 | <code>error_code=NONE</code>；必须有 exact <code>pre_ref</code>；不得有 <code>post_ref</code> 或 domain receipt；零效果 | <code>NO_RETRY</code> |
| <code>NO_CHANGE</code> | 仅 <code>MUTATE</code> | 必须是有效摘要 | <code>error_code=NONE</code>；exact pre/post refs 必须完全相同；不得有 domain receipt；零新 domain 效果 | <code>RETURN_EXACT_RECEIPT</code>，不得重做 domain 操作 |
| <code>APPLIED</code> | 仅 <code>MUTATE</code> | 必须是有效摘要 | <code>error_code=NONE</code>；必须有满足该 operation 冻结约束的 exact pre/post refs 与 exact domain receipt ref；只引用已提交效果，不复制效果状态 | <code>RETURN_EXACT_RECEIPT</code>，不得重做 domain 操作 |
| <code>REJECTED</code> | <code>DRY_RUN</code> 或 <code>MUTATE</code> | Dry-run 必须省略；Mutate 必须是有效摘要 | 必须是非 <code>NONE</code>、非 <code>OUTCOME_UNKNOWN</code> 错误；不得有 post/domain refs；零效果 | <code>NO_RETRY</code> |
| <code>UNKNOWN</code> | 仅 <code>MUTATE</code> | 必须是有效摘要 | 仅 <code>OUTCOME_UNKNOWN</code>；必须有 exact pre-ref 和原 <code>OUTCOME_UNKNOWN</code> domain receipt ref；不得有 post-ref；效果不确定 | <code>EXACT_RECEIPT_ONLY_NO_REPLAY</code>；禁止语义重放、替代 Attempt 或换 Provider |

<code>APPLIED</code> 的 pre/post 转换必须由 operation-specific validator 验证，禁止
用一条宽泛的“revision 增大”规则代替：

| operation | expected kind | exact APPLIED pre/post 约束 |
|---|---|---|
| <code>MODULE_APPLY</code> | <code>PUBLISHED_POINTER</code> | 同 kind/id，revision <code>+1</code>，digest 必须变化 |
| <code>MODULE_DISABLE</code> | <code>PUBLISHED_POINTER</code> | Disable 复用唯一 Apply/Catalog CAS；同 kind/id，revision <code>+1</code>，digest 必须变化。Activation 是不可变事实，不能作为 mutation head |
| <code>MODULE_UPGRADE_REVIEW</code> | <code>PUBLISHED_POINTER</code> | pre/post 必须完全相同；新增或 exact-retry 得到的 U3 Review 由 <code>MODULE_UPGRADE_REVIEW</code> domain receipt ref 证明。本 enum 只表示创建 Review，不包含 Approve/Reject 决策 |
| <code>MODULE_UPGRADE_APPLY</code> | <code>PUBLISHED_POINTER</code> | 同 kind/id，revision <code>+1</code>，digest 必须变化；domain kind 有意复用唯一 <code>MODULE_APPLY</code> 链 |
| <code>LEARNING_PROPOSAL_REVIEW</code> | <code>LEARNING_PROPOSAL</code> | 同 kind/id，digest 必须变化；仅允许 <code>0→2</code>、<code>1→2</code> 或同 Attempt 对账的 <code>2→3</code> |
| <code>LEARNING_CYCLE_RUN</code> | <code>LEARNING_SCHEDULE</code> | 新 due window 为同 kind/id、revision <code>+1</code> 且 digest 变化；续行已存在 open Task 时允许 pre/post 完全相同，实际 Task/Run 由 <code>LEARNING_CYCLE</code> domain receipt ref 证明 |

<code>NO_CHANGE</code> 只表示没有创建、认领或推进 domain 效果。Learning Task 的业务
终态 <code>NO_CHANGE</code> 若已经创建 Task/Run/Attempt，在 Control 层仍是
<code>APPLIED</code>。验证、授权或 stale precondition 在任何受治理效果/Attempt 前失败才是
<code>REJECTED</code>；已知的 <code>REVIEW_FAILED</code>、Task <code>FAILED</code> 或
<code>INVALID_RESULT</code> 是已经持久化的业务终态，属于 <code>APPLIED</code>。一旦越过
admission/CAS/外部 Attempt 而终态不可证明，只能返回 <code>UNKNOWN</code> 并禁止语义重放。

W6-0、W6-1 与 W6-2 confirmation 历史原子都不创建 <code>control_operation_receipts</code> 表。Dry-run
receipt 只证明本次响应与 exact basis，process-local confirmation proof 也不提供跨进程幂等保证。

历史 receipt Schema 原子已经单独批准一张窄、append-only
<code>control_operation_receipts</code> 表，并完成：

1. 更新 Schema identity、migration、Backup Create/Verify/Restore semantic gate；
2. 只保存可恢复的 exact canonical+digest：Request、typed input、operation evaluation、Control receipt、
   pre/post 完整 <code>PublishedBasisRefV1</code> 与 domain-native receipt；不得只存摘要后猜测内容；
3. <code>APPLIED</code> receipt insert、domain-native receipt、Control/Catalog publication 与 pointer CAS
   现在只通过窄 <code>CommitModuleDisableControlOperationV1</code> 位于同一
   <code>BEGIN IMMEDIATE</code> transaction；不得暴露通用 APPLIED insert 或 backfill；
4. 唯一键固定为 <code>(principal_id, scope_digest, operation, idempotency_key_digest)</code>；同 identity+
   同 request digest 在 proof/current-basis 检查前返回 exact receipt，不同 digest 返回冲突；
5. 保存历史 <code>authorization_revision</code> 与 <code>scope_set_digest</code> 仅供审计，绝不用于当前授权；
   不保存 Boot/session/Cookie/CSRF/raw proof/proof digest；
6. 首片只持久化 <code>NO_CHANGE</code>/<code>APPLIED</code>，不持久化 <code>REJECTED</code>；纯 SQLite
   transaction 内 <code>UNKNOWN</code> 不可达，commit ambiguity 只能通过 exact receipt lookup 解析；
7. 单行上限为 Request 8 KiB、typed input 8 KiB、evaluation 64 KiB、Control receipt 16 KiB、
   pre/post basis 各 8 KiB、domain receipt 128 KiB、canonical 总量 256 KiB；Tenant 最多 1024 行、
   Store 最多 8192 行，容量耗尽失败关闭且不驱逐历史 receipt；
8. 表与 API 均 append-only，唯一 domain receipt digest 防止不同 key 认领同一效果。Schema 历史原子的
   <code>CommitModuleDisableControlReceiptV1</code> 仍只允许 neutral replay 闭合且 basis 不变的
   <code>NO_CHANGE</code>；当前 specialized operation commit 另在同事务中只接通本首片的
   <code>NO_CHANGE/APPLIED</code>，不提供任意 operation publication。

W6-2 receipt Schema 历史原子的 rebuild-only 曾把 ordinary table 从 32 增至 33；当时 schema fingerprint 为
<code>51be9081a815e6379380b93e745db03d33ca2f358962f85ace63bce2534dcc10</code>，migration 为
67,998 bytes / SHA-256
<code>8feea38beb505ab4f371afe32eecd877744062bd149d0d24982839c5450fc952</code>。历史 32 表 identity、
57,652-byte migration 与摘要继续只表示 W2-U3 至 W6-2 confirmation。当前 W6-3 Store identity 见 §10.7；
Control HTTP 仍只保留 W6-2 的 confirmation/mutate 两路与 W6-3 的只读 Overview，其余 operation 仍是 read/Dry-run 或不存在。

#### 3.5.1 module-disable-publication-receipt/v1

首候选的 domain-native receipt 只能使用
<code>module-disable-publication-receipt/v1</code>。ID/digest 域分别固定为
<code>freeagent.module-disable-publication-id/v1</code> 与
<code>freeagent.module-disable-publication-receipt/v1</code>，不得与 Control Request/Receipt 摘要域复用。
该 canonical object 只记录 domain 事实：exact restored disabled plan canonical 及重新计算的 plan digest、
pre/post 完整 <code>PublishedBasisRefV1</code>、被移除 Binding 的 target/profile/instance/port/ordinal、
ConfigRef、AuthorityCeilingRef、StaticContextRefs、FailurePolicy，以及 exact catalog retain/remove change。
它不得包含 principal、Control request/key/confirmation digest、raw proof、session 或时间。

Backup/Restore 与 durable resolver 必须从 immutable predecessor Control/Catalog 重新运行唯一 neutral
Disable evaluator，并逐字节验证 plan、Binding removal、catalog change、生成的 Control/Catalog 与 post
basis；不能只验证摘要形态。<code>NO_CHANGE</code> 行没有 domain receipt；<code>APPLIED</code> 行必须有且只能有
一个 exact domain receipt，部分唯一约束禁止不同 idempotency key 认领同一效果。当前源码已有该 strict
wire、table、exact resolver 与 verifier；historical generic commit 仍仅支持 <code>NO_CHANGE</code>，窄
specialized operation commit 只为本首片原子提交 <code>NO_CHANGE/APPLIED</code>。

### 3.6 control-event-cursor/v1

事件只提示客户端失效并重新读取 Store；它不是事实流：

~~~text
ControlEventCursorV1 = {
  schema_version: "control-event-cursor/v1",
  boot_id,
  scope_digest,
  event_revision,
  view_snapshot_digest,
  sources: [{
    source,
    revision,
    digest
  }],
  issued_at_unix_micros
}
~~~

<code>sources</code> 排序、去重；<code>event_revision</code> 在当前 boot 的 exact
scope stream 内严格递增。Wire 本身是 content-addressed inert事实；未来 transport
token/page token 还必须另用当前 boot 内存密钥做认证保护，不能把本合同摘要误当 HMAC。

boot、connection、scope、view、filter 或 source revision 不匹配，token 篡改，
revision gap，慢客户端丢通知，服务重启或 token 过期时，都必须返回
<code>CURSOR_STALE</code> 并要求完整 refetch；不得猜测续点。事件不得携带
Secret、正文、Authority material 或跨 scope payload。Mutation 成功与否只能由
同步 operation receipt 判定，不能依赖事件。

W6-0 不实现 SSE；W6-1 也不得因已存在本合同而隐式启动事件流。

## 4. 查询、分页、过滤与缓存

W6-1 的每个 list endpoint 必须通过独立 Application Service 暴露有限查询：

- 只允许 keyset pagination；默认 page size 50，最大 100，拒绝 offset/page number；
- 排序必须是 endpoint 固定的稳定总序，最后包含唯一 resource identity；
- page cursor 必须认证并绑定 boot ID、principal、scope digest、resource kind、
  filter digest、sort version、source revision 和 last key；
- cursor 不可验证或来源 revision 改变时返回 <code>CURSOR_STALE</code>，不得静默
  从新快照继续；
- filter 是 endpoint 固定的 typed allowlist；拒绝任意 SQL、regex、glob、路径、
  未知 enum 和跨 scope ID；
- filter set canonical 排序，filter digest 进入 cursor 和 ETag；
- 空结果使用空数组，并返回确定的 next-cursor absence。

强 ETag 统一为引号包围的 lowercase SHA-256，至少覆盖 schema version、principal
可见 scope、exact source basis、filter、sort version 和 response semantic bytes。
读请求可使用 <code>If-None-Match</code>；未来写请求必须使用强
<code>If-Match</code> 或 operation 明确要求的 expected revision。弱 ETag
不得用于 mutation，ETag 也不能替代授权。

## 5. Idempotency、revision 与 UNKNOWN

- <code>Idempotency-Key</code> 只用于未来 <code>MUTATE</code>，必须是
  16–128 bytes 的可打印 ASCII opaque value；不得放进 URL、日志或 response；
- Store 只允许保存带 domain separator 的 key digest，不保存原 key；
- lookup identity 必须含服务端认证 principal、exact scope、operation 和 key digest；
- 同 identity + 同 request digest 返回原 receipt，零 domain 重放；同 identity +
  不同 digest 返回 <code>IDEMPOTENCY_CONFLICT</code>；
- stale ETag、revision、PublishedBasis 或 Dry-run basis 返回
  <code>REVISION_CONFLICT</code>，不得自动 rebase；
- response 丢失后的 exact retry 只能恢复原 receipt。Session 重建不改变 principal
  维度，但不得放宽 scope；
- <code>UNKNOWN</code> 禁止语义重放、换 Provider、替代 Attempt、重新 Dry-run
  后偷换 basis 或生成“等价”请求。只能读取或对账原 Attempt，并把原 receipt
  CAS 到协议允许的终态。

## 6. 有限错误合同

辅助错误 envelope 固定为
<code>{schema_version:"control-error/v1", code, correlation_id,
message, retry_after_seconds?}</code>。<code>message</code> 是稳定、安全的用户提示，
不得包含 SQL、路径、请求/body、header、Cookie、token、Secret、stack 或内部错误链。

首批有限错误码：

| code | 语义 |
|---|---|
| <code>NONE</code> | 仅用于成功 receipt 的无错误哨兵；不得作为 error response code |
| <code>INVALID_REQUEST</code> | JSON、schema、字段、enum 或 limit 非法 |
| <code>UNAUTHENTICATED</code> | credential 缺失或无效 |
| <code>SESSION_EXPIRED</code> | session 已失效，需重新 bootstrap |
| <code>FORBIDDEN</code> | session 无所需 capability 或 scope ceiling |
| <code>NOT_FOUND</code> | 资源不存在或对该 principal 不可见 |
| <code>CONFLICT</code> | 当前状态与请求约束冲突，但不属于 exact revision 漂移 |
| <code>PRECONDITION_REQUIRED</code> | 缺少 If-Match、expected revision 或 Dry-run basis |
| <code>REVISION_CONFLICT</code> | exact basis 已过期；不自动 rebase |
| <code>IDEMPOTENCY_CONFLICT</code> | 同 key 对应不同 request digest |
| <code>CURSOR_INVALID</code> | cursor 格式、签名或绑定非法 |
| <code>CURSOR_STALE</code> | cursor gap、重启或 source revision 已变化 |
| <code>RESOURCE_EXHAUSTED</code> | body、page、并发或时间 limit 超限 |
| <code>STORE_BUSY</code> | 唯一 Store 当前不能安全完成请求 |
| <code>STORE_UNAVAILABLE</code> | Store closed、未打开或生命周期已停止 |
| <code>INTEGRITY_FAILURE</code> | schema、digest、Backup 或语义闭包失败 |
| <code>OUTCOME_UNKNOWN</code> | 原操作效果不确定；禁止重放 |
| <code>CANCELLED</code> | 请求在产生新效果前被显式取消 |
| <code>INTERNAL_ERROR</code> | 已脱敏的未分类服务端错误 |

HTTP status 只是 transport 映射；客户端必须按 <code>code</code> 和 receipt 判断，
不得把 timeout/5xx 推断为失败后重试。scope 外资源与不存在资源统一
<code>NOT_FOUND</code>，避免枚举。

## 7. W6-1 认证与 listener 已验收接线合同

以下能力不属于 W6-0 历史切片，已在 W6-1 的窄默认关闭生产链中验收：

- Control 默认关闭。唯一 <code>freeagent serve</code> 进程显式启用后，使用独立
  <code>tcp4</code> listener；首个安全默认值是
  <code>127.0.0.1:0</code>，由 OS 选择端口；
- 只接受 literal <code>127.0.0.1</code>。拒绝 <code>localhost</code>、DNS 名称、
  wildcard、非 loopback、IPv4-mapped 地址和隐式 redirect；
- Control 与 Chat/Channel 使用两个 listener，但共享同一已打开的 Store 和同一组
  Application Services；不得打开第二 Store 或创建第二 writer；
- 显式启用 Control 后若安全检查或 bind 失败，整个 serve 在接受任何请求前失败关闭，
  不能静默退回无 Control 模式；
- 服务必须拒绝在 Unix root 或 Windows elevated/admin token 下常驻；无法可靠判断
  elevation 时也拒绝启动，不尝试自行降权。

### 7.1 Bootstrap 与 session

- 每次 Control 启动生成至少 256-bit CSPRNG bootstrap capability，5 分钟过期且最多
  成功交换一次；
- 服务端 registry 只保留带 domain separator 的 capability digest、Boot ID、到期时间和
  unused/consumed 状态；原始 capability 不进入六份 wire；
- capability 通过用户显式选择或应用私有 runtime 目录中的 owner-only handoff 文件交付；
  文件必须在 owner-only 父目录中以独占创建语义写入，最大 4 KiB，只含 schema、literal
  Control origin、原始 capability 和到期时间。父目录/文件 owner 或 ACL 无法验证，或命中
  symlink/reparse/pre-existing leaf 时，Control 必须在开放 Admission 前失败关闭，并关闭已
  bind 的 Control listener、清除 capability/handoff，保证零请求被接纳；
- capability 只可通过专用 POST 的 exact bounded JSON body 提交，不得进入 URL query、
  fragment、header、Cookie、Store、Backup、日志、错误或浏览器 history；
- 客户端读取 handoff 后立即删除文件并只在内存持有；服务端在成功交换、过期、关停或
  任意第二次使用后清除 digest 并固定 consumed，失败响应不得枚举不存在/过期/已用；
- 交换成功后签发至少 256-bit 随机 session credential，并在响应 body 返回只驻前端内存、
  与该 session 绑定的 CSRF token；
- Cookie 必须 host-only、<code>HttpOnly</code>、<code>SameSite=Strict</code>、
  <code>Path=/control/</code>，不得设置 Domain，也不得成为持久 Cookie；
- CSRF token 与 session 绑定，只保存在前端内存，通过
  <code>X-FreeAgent-CSRF</code> 发送；不得进入 localStorage、URL 或日志；
- Cookie 是 host-only 而不绑定 loopback 端口。除 bootstrap exchange 外，Modules GET 与所有
  非安全方法都必须同时验证 exact session credential 和 session-bound CSRF header；GET 缺失、
  重复、非法或错误 CSRF 必须返回 HTTP 401/<code>UNAUTHENTICATED</code>，且不得调用 Application
  Service。CSRF 不能只保护写请求；
- 所有身份、scope ceiling 和 capability 由本地启动策略授予，客户端自报 actor、
  role、Tenant 或 Workspace 不能提升权限。

### 7.2 Host、Origin、CSP 与资源限制

- 每个请求的 Host 必须逐字节等于 listener 实际 `127.0.0.1:{port}`；拒绝代理转发头和
  POST redirect；
- bootstrap 必须有 exact same-origin Origin 与一次性 capability；它发生在 session/CSRF
  签发前，因此不要求尚不存在的 session-bound CSRF。session 建立后的安全读与所有非安全方法
  都必须验证 exact session 与 session-bound CSRF；非安全方法还必须验证 exact Origin，安全读
  若带 Origin 也必须 exact match。不得返回 wildcard CORS；
- 只接受 endpoint 声明的 method 和
  <code>application/json</code>；拒绝 method override、multipart、form 和
  sniffed content type；
- HTML 固定 CSP：
  <code>default-src 'self'; script-src 'self'; style-src 'self';
  connect-src 'self'; img-src 'self' data:; object-src 'none';
  base-uri 'none'; frame-ancestors 'none'; form-action 'none'; font-src 'none'</code>；
  禁止 inline script、<code>eval</code>、外部 CDN、字体和 analytics；
- 同时设置 <code>X-Content-Type-Options: nosniff</code>、
  <code>Referrer-Policy: no-referrer</code> 和禁止 framing；
- request target 最大 8 KiB，headers 总计最大 16 KiB 且 header field count 最大 64，
  bootstrap JSON body 最大 4 KiB，普通 Control operation body 最大 1 MiB，response
  semantic body 最大 1 MiB；
- read-header timeout 5 秒、单请求 deadline 30 秒、idle timeout 60 秒；
  Control 全局并发上限 32、单 session 上限 8，超限失败关闭且不排无界队列；
- session absolute lifetime 最大 8 小时、idle lifetime 最大 30 分钟；关停的有界
  shutdown deadline 最大 30 秒。

### 7.3 双 listener 生命周期

实际生产启动顺序固定为：在 Composition/Store/listener I/O 前验证 Control flags 与非管理员身份 →
打开唯一既有 Composition/Store → 从该 Store 构造 Modules read 与 <code>MODULE_DISABLE</code>
Dry-run Application Services并创建 startup-closed shared Admission gate → 绑定 Chat listener 与
Control exact <code>tcp4 127.0.0.1:0</code> listener → 从已 bind Control socket 得到 exact actual
<code>127.0.0.1:{port}</code> origin → 构造 scope set、bootstrap/session registry、cursor registry、
HTTP handler 与共享 shutdown coordinator → 在 Admission 仍关闭时启动两个 server → 以 owner-only、
exclusive-create 语义完成 handoff → 发布不含 Control origin/credential 的 readiness → 仅在上述步骤
全部成功后一次性开放 shared Admission。bind/Serve 并不等于可接纳请求；任一安全检查、listener、
server、handoff 或 readiness 步骤失败时，必须删除未完成 handoff、清除 bootstrap capability、关闭
所有已 bind listener 并以零已接纳请求退出，不得局部提供服务或退回无 Control 模式。

关停顺序固定为：同时关闭 Chat/Control 新 Admission → 关闭两个 listener → 有界排空
两个入口及共享 Run → 取消剩余工作 → 在两边均排空后只执行一次 Store-only recovery →
关闭唯一 Store。整个有界关停 deadline 为 30 秒；到期后取消剩余工作，但不得因此启动
第二次 recovery 或跳过唯一 Store 的既定所有权。Control handler 不得自行 recovery、
迁移、repair 或关闭 Store。

## 8. Application Service 边界

Control HTTP 只能调用 W6-1 的窄 Application Services。Handler 不得导入 SQLite driver、
拼 SQL、构造私有 executor、加载模块 artifact 或直接调用 Gateway。既有停机 CLI Apply/Disable
保持相邻既有路径；W6-1 handler 不得调用或包装它们，也不得把 CLI plan idempotency
冒充 Control durable receipt。

W6-0 的纯度门禁只约束新合同/策略 package 的 production Go 文件之**直接 import**：
<code>internal/controlapicontract</code> 只允许项目内
<code>sdk/moduleapi</code>，<code>internal/controlapipolicy</code> 只允许项目内
<code>internal/controlapicontract</code> 与 <code>sdk/moduleapi</code>；两者都不得直接
导入 <code>net/http</code>、<code>database/sql</code>、<code>os</code>、
<code>path/filepath</code> 或 <code>internal/currentstore</code>。该门禁不声称允许依赖的
transitive closure 完全不含这些标准库包，也不把测试文件的测试辅助 import 误称为
production 依赖。

以下 Store 能力不得直接包装成 HTTP endpoint：

- 任意全量 <code>List*</code> 或无 scope/limit 的 scan；
- <code>GetContent</code> 及任何返回 canonical content bytes 的通用读取；
- <code>ScanStartupRecovery</code>、<code>RecoverStartupPending</code>、
  <code>ScanUnsettledActionDispatchRecords</code> 和其他 recovery/reconcile scan；
- migration、seed、Backup/Restore、semantic repair、raw table dump；
- Secret resolution、ModuleHost、Provider executor 或 external effect dispatch。

W6-1 只能新增 resource-specific、scope-filtered、keyset-paginated、脱敏 DTO 查询。
每个 service 必须在查询前验证 scope，在映射每行时再次验证 ownership/capability，
并只返回 endpoint allowlist 字段。原始模型/task/RAG/Memory 内容、SecretRef
material、Config/Authority 正文、主机路径和未授权跨 Workspace 数据保持不可见。

Carbon 只作为 W6 前端的信息架构参考：Board 是现有 Module、Proposal、UNKNOWN 和
周期任务的只读分类投影；拖拽不改变原生状态。不得新增通用 Task Store、Worker
Store、Work Log 事实源或第二调度器。

## 9. 威胁模型

本规格防御：

- DNS rebinding、跨站表单/脚本、恶意 Origin、Host 欺骗和开放重定向；
- 未认证浏览器、过期 session、CSRF、scope 越权与资源枚举；
- cursor/ETag/idempotency 篡改、stale basis 和丢失响应后的重复提交；
- 过大/慢请求、无界分页/并发以及错误、日志和 DTO 中的敏感信息泄漏；
- UI、SSE 或 query cache 被误当成权威事实源。

本地 loopback 不是 OS sandbox。本规格不承诺防御：

- 同一 OS 用户下可读进程内存、注入进程或控制本地浏览器的恶意进程；
- 管理员或系统最高权限、调试器、内核、文件系统 ACL 或宿主机已失陷；
- 浏览器、扩展、供应链或 FreeAgent 二进制本身已被攻破；
- wazero、Go runtime 或 OS 的未知漏洞；
- 远程多租户、敌对公网、企业 SSO、TLS termination 或审计合规。

命中上述非目标时必须停止 Control、轮换进程内 credential 并按外部事件响应处理，
不能声称 loopback、SameSite 或 CSP 已形成强隔离。

## 10. W6-0 历史验收与 W6-1 当前验收矩阵

### 10.1 W6-0 历史纯合同/策略验收

本节记录 W6-0 当时只验收纯数据结构、canonical 规则和 transport-neutral policy 的边界，
不得用 W6-1 的后续消费链改写历史：

| 类别 | 必须通过 |
|---|---|
| 名称唯一性 | Core <code>control-snapshot/v1</code> bytes/digest 不变；Web 只使用 <code>control-view-snapshot/v1</code> |
| Canonical | 六合同稳定 round-trip、未知/重复字段和非 canonical 输入失败、defensive copy |
| Scope | tenant/workspace 合同形态、scope set 排序/去重和 ceiling 子集 policy 冻结；不声称逐资源查询复验已经接线 |
| 请求摘要 | correlation/time/header 顺序不影响摘要；scope/input/expected basis 任一变化必改摘要 |
| Receipt 合同 | status×intent×key×effect/replay 矩阵失败关闭；冻结 exact Request 逐字段绑定义务；不声称 resolver 或 Store receipt 已实现 |
| 分页过滤 policy | keyset 总序、page limit、filter canonical 与 cursor 必须绑定的字段已冻结；不实现 cursor codec、签名或 endpoint |
| ETag/revision policy | strong ETag 与 If-Match/expected revision 的纯校验规则、stale basis 零自动 rebase 已冻结；不实现 HTTP consumer |
| Idempotency policy | raw key 形态、digest identity、冲突和 UNKNOWN 零语义重放规则已冻结；不声明 Store lookup/receipt 已实现 |
| Secret | 六合同、错误、cursor、ETag 均不出现 token、Cookie、Secret、正文、Authority 或路径 |
| Receipt 决策 | W6-0 零新表；MUTATE 默认拒绝；未来首写必须先验收同事务 append-only receipt |
| 架构 | W6-0 无 listener/handler/SSE/UI/第二 Store/worker/Task Store；Pure Chat 零 Control/可选模块访问 |
| Store | ordinary table 数、fingerprint、migration bytes、Backup/Restore 语义完全不变 |
| 依赖纯度 | 仅验证 production direct imports 的窄 allowlist；不得把该结果表述成 transitive dependency closure |
| 文档 | Web snapshot 名称已改正；W6-0 边界明确排除 listener 与在线写 |

W6-0 通过全部合同测试、文档门禁与既有全量门禁后记录的历史 marker 保持为：

~~~text
W6_0_CONTROL_API_CONTRACT_ACCEPTED_DEVELOPMENT_SLICE
→ W6_1_APPLICATION_SERVICES_READ_API_NEXT
MARKDOWN = 39
SOURCE_PACKAGES = 37
PRODUCTION_DEPENDENCY_CLOSURE = 35
STORE_TABLES = 32
~~~

### 10.2 W6-1 当前消费方验收

| 类别 | 已验收边界 |
|---|---|
| Default-off | 不传 <code>--enable-control</code> 时不创建 Control listener、handoff、bootstrap/session/cursor registry 或 Control Application Services，也不因 Control 读取 Store；单独传 handoff path 在 Store/listener I/O 前失败 |
| 双 listener | 同一 <code>freeagent serve</code> 以 Chat listener 加独立 exact <code>tcp4 127.0.0.1:0</code> Control listener，共享唯一 Store、Application Services、Admission、request context、shutdown budget 与 coordinator；无第二 Store/writer |
| 启动/关停 | 两个 server 都在 startup-closed Admission 后启动；actual origin、owner-only exclusive handoff 与 readiness 完成后只开放一次；失败零请求。关停同时阻止新 Admission、关闭双 listener、有界排空、取消、只做一次 recovery/Store close，deadline 30 秒 |
| Bootstrap/session | bootstrap 至少 256-bit、5 分钟、一次性；session credential/CSRF 至少 256-bit 且 process-local，absolute 8 小时、idle 30 分钟；credential material 不进入 stdout、日志、URL、Store 或 Backup |
| Read auth | Cookie 是 host-only 而不绑定端口；除 bootstrap exchange 外，Modules GET 与非安全方法都要求 exact session + session-bound <code>X-FreeAgent-CSRF</code>。GET 缺失/错误 CSRF 返回 401/<code>UNAUTHENTICATED</code>，零 Application Service 调用 |
| Modules read | <code>GET /control/api/v1/modules</code> 与 <code>GET /control/api/v1/modules/{instance_id}</code> 只返回逐资源 scope/capability 复验后的脱敏 DTO；list 使用认证 keyset cursor，list/detail 使用 strong ETag，cursor 篡改/stale/restart 与跨 Workspace/敏感字段负例失败关闭 |
| Disable Dry-run | <code>POST /control/api/v1/modules/disable/dry-run</code> 要求 exact Origin、session+CSRF、If-Match 与 canonical body，拒绝 <code>Idempotency-Key</code>/confirmation header；只计算 effect-free <code>MODULE_DISABLE</code> projection，不执行 mutation |
| Receipt | <code>DRY_RUN</code> receipt 从 exact frozen request/basis 构造并逐字段复验，<code>post_ref</code>/<code>domain_receipt</code> 为空，只随当前响应存在；不是 durable receipt，不提供跨进程幂等 |
| 架构排除 | 零 Control mutation、durable receipt/table、SSE、UI、第二 Store/writer、后台 worker、Task Store或主动预热；既有停机 CLI Apply/Disable 不属于本 Control surface |
| W6-1 历史证据 | 39 份 Markdown、44 个源码 package、44 个 production dependency-closure package；当时 Store 为 32 表且 fingerprint、57,652-byte migration、Backup/Restore 语义不变 |

### 10.3 W6-2 受控操作审计历史收口

W6-2 先逐项审计六个 operation，未借 audit marker 接通任何写路由。历史收口为
<code>W6_2_CONTROLLED_MUTATIONS_AUDIT_ACCEPTED_DEVELOPMENT_SLICE /
W6_2_CONFIRMATION_CONTRACT_NEXT</code>，结论固定如下：

| 顺序 | operation | 审计结论 |
|---:|---|---|
| 1 | <code>MODULE_DISABLE</code> | 唯一首候选；仅 exact <code>TENANT</code> scope、<code>PROFILE</code> binding target、<code>context.provide/v1</code>、<code>OPTIONAL</code>、<code>DECLARATIVE</code>、trusted-instruction config、deny-all Authority。只允许移除一个既有 Binding 并在唯一 Current Store transaction 发布相邻 Control/Catalog/pointer；不允许 Model/Action/Channel/Workspace endpoint、<code>REQUIRED</code>、grant、路径、artifact、网络、Provider 或 runtime refresh |
| 2 | <code>MODULE_UPGRADE_REVIEW</code> | Store seam 可保持窄，但现有入口仍接受调用方 artifact path/signature；须先有 server-owned、digest-addressed artifact ingress，故 deferred |
| 3 | <code>LEARNING_PROPOSAL_REVIEW</code> | 现有 review 可能进入 reservation/异步 Attempt；须先冻结第一持久事实与 Control receipt 的同事务归属，故 deferred |
| 4 | <code>LEARNING_CYCLE_RUN</code> | 现有 cycle 会创建/续行 Task/Run；须先冻结 reservation、异步恢复和 UNKNOWN 语义，故 deferred |
| 5 | <code>MODULE_UPGRADE_APPLY</code> | 涉及 artifact staging/install/activation 与 approved replacement 长链，超出首片的本地声明式移除，故 deferred |
| 6 | <code>MODULE_APPLY</code> | 包含最宽的 artifact/grant/bind/apply 表面，不能以通用 CLI plan 或第二 Store 冒充在线原子性，故 deferred |

首候选未来不得从 HTTP 调用 CLI <code>applyModulePlanV1</code>，因为该路径会自行打开 Store/recovery。
Store seam 必须复用唯一 publication builder，并在同一 <code>BEGIN IMMEDIATE</code> transaction 内完成
domain receipt、Control/Catalog 与 pointer CAS。

### 10.4 W6-2 confirmation 纯合同验收

| 类别 | 已验收边界 |
|---|---|
| 稳定 Statement | <code>control-confirmation-statement/v1</code> 将 MUTATE 的 principal/capability/operation/scope/key/input/evaluation/expected ref 全部绑定；Request 的 <code>confirmation_digest</code> 必须由这些字段重建，challenge 刷新与重启不会改变稳定 request digest |
| Disable evaluation | <code>control-module-disable-evaluation/v1</code> 稳定冻结向用户展示的完整候选投影，严格关闭 disposition/basis/Binding/catalog 组合；只计算，不发布 |
| Proof registry | raw proof 固定 32 random bytes、TTL 最多 2 分钟且不超过当前 session absolute expiry、全局最多 256、每 session 最多 8；registry 仅保留 domain-separated proof digest，并绑定 Boot ID、Session ID、principal、authorization revision、scope-set digest、capability、scope、Statement/Request digest、expiry 与 state |
| Claim 生命周期 | Issue/Claim 线性化；proof 只能从 ISSUED 到 CLAIMED。只有已证明零持久效果时可 <code>ReleaseNoEffect</code>；任何可能已有持久效果或 commit ambiguity 后必须 Consume，不得重新开放 proof |
| Secret/持久化排除 | raw proof 与 proof digest 均不进入 Request/Receipt、Current Store、Backup、日志、URL 或错误；registry 重启即失效，Close 清除全部内存状态 |
| Durable retry 义务 | 未来 MUTATE 必须在 auth+canonical typed body/key/request 后，先按 identity 查 durable receipt；同 request digest 先返回 exact receipt，不再要求旧 proof 或旧 session。只有 miss 才 claim 当前 proof、复验当前 auth/basis 并进入同一事务 |
| 架构排除 | 无 confirmation HTTP endpoint、无 mutation route、无 durable receipt row、无 Schema/migration/Backup 改动、无第二 Store/writer、无 SSE/UI/worker；首片纯 SQLite publication 内 <code>UNKNOWN</code> 不可达 |
| 历史证据 | 39 份 Markdown、45 个源码 package、44 个 production dependency-closure package；当时 Store 仍为 32 表且 identity/migration/Backup/Restore 不变 |

### 10.5 W6-2 durable receipt Schema 历史验收

| 类别 | 已验收边界 |
|---|---|
| Schema | 33 张 ordinary tables，只新增 STRICT、append-only <code>control_operation_receipts</code>；fingerprint <code>51be9081a815e6379380b93e745db03d33ca2f358962f85ace63bce2534dcc10</code>；migration 67,998 bytes / SHA-256 <code>8feea38beb505ab4f371afe32eecd877744062bd149d0d24982839c5450fc952</code> |
| Pure contracts | 共享 <code>module-apply-plan/v1</code>、exact full <code>PublishedBasisRefV1</code> freeze/restore 与 <code>module-disable-publication-receipt/v1</code>；candidate IDs、publication ID 与 receipt digest 都用独立 domain |
| Identity/resolver | 唯一 identity 为 principal/scope/operation/idempotency-key digest；同 request digest 返回 original exact receipt，同 identity 不同 digest typed conflict，miss typed not-found |
| Public commit | 只提交 exact neutral replay 后确定 basis 未变的 <code>NO_CHANGE</code>；不能 publication、不能插入 <code>APPLIED</code>、不能认领 domain effect |
| SQL limits | Request/input/evaluation/Control receipt/pre/post/domain 上限为 8/8/64/16/8/8/128 KiB，总量 256 KiB；Tenant 1,024、Store 8,192；identity/domain ID/domain digest 唯一，same-Tenant parents、Catalog→Control pair 和 append-only triggers 失败关闭 |
| Semantic closure | metadata/quota/cap 优先；逐行 strict Restore canonical、复验 immutable Control/Catalog parents并重放唯一 neutral evaluator。Backup Create/Verify/Restore 在 coherent snapshot 内调用；只验证现存行，不声明能发现无外部 completeness anchor 的整行删除 |
| APPLIED 历史边界 | DDL、strict restore 与 verifier 允许 APPLIED 形状，但该历史原子没有公开 insert/publication seam；其下一 wiring 义务要求唯一 Store owner 在同一 <code>BEGIN IMMEDIATE</code> transaction 内提交 domain receipt、Control receipt、Control/Catalog 与 pointer CAS |
| 排除 | 无 mutation route/handler、confirmation endpoint、APPLIED public insert、SSE/UI/worker 或第二 Store/writer；proof/proof digest/session 不持久化 |
| 历史证据 | 39 份 Markdown、47 个源码 package、46 个 production dependency-closure package、33 表 Store |

### 10.6 W6-2 MODULE_DISABLE mutation wiring 历史验收

| 类别 | 已验收边界 |
|---|---|
| 生产开关与路由 | Control 继续默认关闭；只有既有显式 <code>--enable-control</code> 初始化 proof registry/coordinator，并注册 <code>POST /control/api/v1/modules/disable/confirmation</code> 与 <code>/mutate</code>。没有第二 mutation flag 或 sibling write route |
| 唯一资格 | TENANT scope、PROFILE target、exact <code>context.provide/v1</code>；existing removed Binding 必须是 <code>OPTIONAL</code>、<code>DECLARATIVE static/v1</code>、trusted-instruction config、deny-all Authority。其他 scope/port/policy/operation 失败关闭 |
| 当前 transport auth | confirmation 与 mutate 每次 HTTP 请求都必须重新通过 exact Origin、当前 session、session-bound CSRF 与 TENANT Permit。exact durable hit “不要求旧 proof/session”仅表示不依赖旧 proof 或旧 session identity，不是匿名 durable resolver |
| Proof lifetime | raw proof 固定 32 bytes；有效期最多 2 分钟且不超过当前 session absolute expiry。registry 仍 process-local、全局 256/每 session 8，只存 proof digest，不进入 Store/Backup/log/URL/error |
| Lookup-first | transport canonical/auth 后，durable resolver 先于 proof/current-basis；same request 返回 original exact receipt，同 identity 不同 request 在 proof/evaluation/current-basis 前冲突。miss 才 claim proof、服务端重建 canonical evaluation，并在 effect commit 前复验 current auth/basis |
| Atomic outcomes | <code>NO_CHANGE</code> 与 <code>APPLIED</code> 都持久化 exact Control receipt。APPLIED domain receipt、Control/Catalog publication 与 pointer CAS 在唯一 Store 的同一 <code>BEGIN IMMEDIATE</code> transaction 提交；禁止 APPLIED backfill，纯 SQLite <code>UNKNOWN</code> 不可达 |
| Retry/concurrency | exact retry、丢失响应、Store reopen、进程重启/新 session 均返回原 exact receipt；不同 key 并发最多一个 APPLIED。commit 调用或 ambiguity 后 proof consume；只有确定零持久效果才 release |
| Backup/atomic evidence | Backup Create/Verify/Restore 验证现存 APPLIED exact roundtrip 与 parent/replay/domain tamper；无 external completeness anchor，不声明发现任意整行 receipt 删除。forced receipt-insert failure 证明 publication/domain/Control receipt/pointer CAS 全事务 rollback |
| 排除 | 默认关闭；无其他 mutation、SSE/UI/worker、第二 Store/writer、CLI Store reopen、Provider/Gateway dispatch、artifact effect、自动升级或预热 |
| W6-2 历史证据 | 39 份 Markdown、48 个源码 package、48 个 production dependency-closure package、33 表 Store；当时 schema fingerprint/migration 不变 |

### 10.7 W6-3 Web Shell / read-only Overview 验收

| 类别 | 已验收边界 |
|---|---|
| 默认关闭与 composition | 只有既有显式 <code>--enable-control</code> 分支构造 Overview service 与 exact embedded static resolver；default-off 零 optional Web dependency、零相关 route，不新增 listener、Store 或 writer |
| Static surface | 只开放 <code>/control/ui/</code>、<code>/control/ui/index.html</code> 与 exact CSS/app/React/TanStack assets；支持 GET/HEAD/ETag，拒绝路径穿越、未知资产与 source map，并发送 no-store、严格 CSP、nosniff、same-origin/deny framing 等安全 headers |
| Overview GET | <code>GET /control/api/v1/overview</code> 无 query/body，只接受 exact strong <code>If-None-Match</code>；每次仍需 Origin/session/session-bound CSRF 与 TENANT/WORKSPACE Permit，跨 scope 在 application service 前失败关闭 |
| Projection | 单一只读事务有界返回 Workspaces、Runs、UNKNOWN、Learning、Module Candidates、Usage；绑定 exact PublishedBasis、每节 source digest、view snapshot、projection digest 与 strong ETag，读取后到编码前再次验证 Permit、scope、状态机、排序、caps、causal intersection 与 source clock |
| Browser authority | selector 只来自 server-authorized scopes/current Workspace refs；TanStack Query key 含完整 scope。rotating resume credential 仅保留在 tab-scoped <code>sessionStorage</code>，CSRF 仅驻内存；无 localStorage authority 或 durable client cache |
| UI states | 响应式 read-only Overview、search、detail drawer 与 deep link 已闭合；loading、empty、error、stale、permission-denied 明确分离，权限撤销会清空 transport/query cache |
| Schema | 该 W6-3 历史切片 Store 为 41 tables / 23 indexes / 56 triggers；fingerprint <code>87d63a2e468ee27ee2c0e1918a92e580532825bde85dbb872b9c795bf577f2c1</code>；migration 143,588 bytes / SHA-256 <code>5bd9743e988b82750f785c015f07c11fbcf6025da70148405aec11106b4efb22</code> |
| 排除 | W6-3 当时无业务 mutation UI、SSE、worker、第二 Store/writer、remote listener、Provider/Gateway dispatch、自动升级或预热；其历史下一入口为 <code>W6_4_MODULES_CONFIGURATION_UI_NEXT</code> |

## 11. W6 历史收口与当前入口

W6-1 通过 Application Service、HTTP、production composition、双 listener 生命周期、default-off、
安全负例、完整 E2E、文档与能力门禁后，只能记录：

~~~text
W6_1_APPLICATION_SERVICES_READ_API_ACCEPTED_DEVELOPMENT_SLICE
→ W6_2_CONTROLLED_MUTATIONS_AUDIT_NEXT
CONTROL_LISTENER = DEFAULT_OFF_TCP4_127.0.0.1_0
CHAT_CONTROL_LISTENERS = 2_SHARED_ADMISSION
BOOTSTRAP_SESSION = PROCESS_LOCAL
READ_API = MODULES_LIST_DETAIL
DRY_RUN = MODULE_DISABLE_EFFECT_FREE
DRY_RUN_RECEIPT = PROCESS_LOCAL_RESPONSE
CONTROL_MUTATION = NONE
DURABLE_RECEIPT_TABLE = NONE
SSE = NONE
UI = NONE
BACKGROUND_WORKER = NONE
MARKDOWN = 39
SOURCE_PACKAGES = 44
PRODUCTION_DEPENDENCY_CLOSURE = 44
STORE_SCHEMA = UNCHANGED_32_TABLES
~~~

`W6_2_CONTROLLED_MUTATIONS_AUDIT_NEXT` 只允许审计第一个受控 mutation，不授权写路由、Schema
或发布成熟度。任何首写前仍必须单独批准窄 append-only receipt schema 与 Backup/Restore 变化，
证明 receipt 和第一条 domain persisted fact 在唯一 Current Store transaction 中原子提交，并闭合
exact retry、异 digest conflict、崩溃窗口、UNKNOWN 禁止重放与恢复长链。在这项批准前，mutation
路由必须不存在或失败关闭；也不得通过既有停机 CLI Apply/Disable 绕过本合同。

上述 audit 已按 §10.3 收口，confirmation 纯合同也已按 §10.4 验收。其历史 marker 固定为：

~~~text
W6_2_CONFIRMATION_CONTRACT_ACCEPTED_DEVELOPMENT_SLICE
→ W6_2_DURABLE_RECEIPT_SCHEMA_NEXT
FIRST_MUTATION_CANDIDATE = TENANT_PROFILE_CONTEXT_PROVIDE_OPTIONAL_DECLARATIVE_MODULE_DISABLE
CONFIRMATION_STATEMENT = STABLE_CANONICAL
OPERATION_EVALUATION = MODULE_DISABLE_ONLY
CONFIRMATION_PROOF = PROCESS_LOCAL_32_BYTES_2_MINUTES
PROOF_CAPACITY = GLOBAL_256_SESSION_8
CONTROL_MUTATION = NONE
DURABLE_RECEIPT_TABLE = NONE
UNKNOWN_FIRST_SLICE = UNREACHABLE
MARKDOWN = 39
SOURCE_PACKAGES = 45
PRODUCTION_DEPENDENCY_CLOSURE = 44
STORE_SCHEMA = UNCHANGED_32_TABLES
~~~

<code>W6_2_DURABLE_RECEIPT_SCHEMA_NEXT</code> 只授权设计并验收 §3.5 的单一 append-only receipt
表候选、32→33 rebuild-only Schema 变更与 Backup/Restore/semantic closure；它不授权 mutation handler、
publication 接线或成熟度提升。该 Schema 原子现已按 §10.5 单独验收；其历史 marker 固定为：

~~~text
W6_2_DURABLE_RECEIPT_SCHEMA_ACCEPTED_DEVELOPMENT_SLICE
→ W6_2_MODULE_DISABLE_MUTATION_WIRING_NEXT
MARKDOWN = 39
SOURCE_PACKAGES = 47
PRODUCTION_DEPENDENCY_CLOSURE = 46
STORE_SCHEMA = 33_TABLES
SCHEMA_FINGERPRINT = 51be9081a815e6379380b93e745db03d33ca2f358962f85ace63bce2534dcc10
MIGRATION_BYTES = 67998
MIGRATION_SHA256 = 8feea38beb505ab4f371afe32eecd877744062bd149d0d24982839c5450fc952
DURABLE_RECEIPT_TABLE = APPEND_ONLY
PUBLIC_COMMIT = NO_CHANGE_ONLY
APPLIED_PUBLIC_INSERT = NONE
CONTROL_MUTATION = NONE
~~~

<code>W6_2_MODULE_DISABLE_MUTATION_WIRING_NEXT</code> 是 receipt Schema 收口时的历史入口。W6-2 wiring 的历史
收口 marker 固定为：

~~~text
W6_2_MODULE_DISABLE_MUTATION_WIRING_ACCEPTED_DEVELOPMENT_SLICE
→ W6_3_WEB_SHELL_READ_ONLY_OVERVIEW_NEXT
MARKDOWN = 39
SOURCE_PACKAGES = 48
PRODUCTION_DEPENDENCY_CLOSURE = 48
STORE_SCHEMA = 33_TABLES
CONTROL_LISTENER = DEFAULT_OFF
MUTATION_ROUTES = MODULE_DISABLE_CONFIRMATION_AND_MUTATE_ONLY
MUTATION_SCOPE = TENANT_PROFILE_CONTEXT_PROVIDE_OPTIONAL_DECLARATIVE_TRUSTED_DENY_ALL
DURABLE_LOOKUP = BEFORE_PROOF_AND_CURRENT_BASIS
OUTCOMES = NO_CHANGE_AND_APPLIED
APPLIED_PUBLICATION = SAME_BEGIN_IMMEDIATE
UNKNOWN = UNREACHABLE
SSE_UI_WORKER = NONE
~~~

<code>W6_3_WEB_SHELL_READ_ONLY_OVERVIEW_NEXT</code> 只授权默认关闭的 Web Shell 与授权 scope 内只读
Overview；它现已按 §10.7 验收。下列 W6-4 marker 现为历史记录：

~~~text
W6_4_MODULES_CONFIGURATION_UI_ACCEPTED_DEVELOPMENT_SLICE
→ W6_5_SERVER_OWNED_MODULE_ARTIFACT_INGRESS_NEXT
CONTROL_LISTENER = DEFAULT_OFF
STATIC_WEB_SHELL = EXACT_EMBEDDED_OVERVIEW_AND_MODULES
OVERVIEW_ROUTE = GET_SCOPE_FILTERED_ONLY
OVERVIEW_SECTIONS = WORKSPACES_RUNS_UNKNOWN_LEARNING_MODULE_CANDIDATES_USAGE
OVERVIEW_AUTHORITY = NONE
OVERVIEW_ETAG = STRONG_VERIFIED
STORE_SCHEMA = 41_TABLES_23_INDEXES_56_TRIGGERS
SCHEMA_FINGERPRINT = 87d63a2e468ee27ee2c0e1918a92e580532825bde85dbb872b9c795bf577f2c1
MIGRATION_BYTES = 143588
MIGRATION_SHA256 = 5bd9743e988b82750f785c015f07c11fbcf6025da70148405aec11106b4efb22
MODULES_READ_UI = STRICT_LIST_DETAIL
BUSINESS_MUTATION_UI = MODULE_DISABLE_ONLY
SSE_WORKER = NONE
~~~

<code>W6_5_SERVER_OWNED_MODULE_ARTIFACT_INGRESS_NEXT</code> 只表示 W6-4 收口时的历史下一入口；W6-4
本身不实现 artifact ingress。

W6-5 历史 marker 固定为：

~~~text
W6_5_SERVER_OWNED_MODULE_ARTIFACT_INGRESS_ACCEPTED_DEVELOPMENT_SLICE
→ W6_6_SERVER_OWNED_MODULE_UPGRADE_REVIEW_NEXT
CONTROL_HTTP_ARTIFACT_INGRESS = NONE
INGRESS_ENTRY = TRUSTED_LOCAL_OPERATOR_CLI_DEFAULT_OFF
CALLER_PACKAGE_PATH_URL_SIGNATURE_UPLOAD_UI = NONE
SOURCE_POLICY = UNSIGNED_LOCAL_DIRECTORY_DENY_ONLY
FILESYSTEM_PUBLICATION = DURABLE_FIRST_CONTENT_ADDRESSED_NO_REPLACE
STORE_COMMIT = IMMUTABLE_ARTIFACT_AND_APPEND_ONLY_ADMISSION_SAME_BEGIN_IMMEDIATE
ARTIFACT_AUTHORITY = NONE
BACKUP_ARTIFACT_SET = INSTALLATION_UNION_INGRESS
STORE_SCHEMA = 43_TABLES_25_EXPLICIT_INDEXES_64_TRIGGERS
SCHEMA_FINGERPRINT = 47981b9bf147ec81c6e98382f281db0d5395187a1f090f7ce183c116fba82a0d
MIGRATION_BYTES = 150301
MIGRATION_SHA256 = 6def433a59fa8f4876894572f1610cae499abc5b389ab5930ea697055419ca86
~~~

当前 marker 固定为：

~~~text
W6_6_SERVER_OWNED_MODULE_UPGRADE_REVIEW_ACCEPTED_DEVELOPMENT_SLICE
→ P2_CONTROL_UI_I18N_ACCEPTED_DEVELOPMENT_SLICE
→ P3_SECOND_PROVIDER_ACCEPTED_DEVELOPMENT_SLICE
→ P4_MODULE_MANAGEMENT_UI_READ_ONLY_SLICE_ACCEPTED_DEVELOPMENT_SLICE
→ P5_BETA_GATE
CONTROL_HTTP_UPGRADE_REVIEW = NONE
REVIEW_ENTRY = TRUSTED_LOCAL_OPERATOR_CLI_DEFAULT_OFF
CALLER_ARTIFACT_PATH_URL_SIGNATURE_TARGET_FACTS = NONE
REVIEW_INPUT = STORE_OWNED_ADMISSION_AND_SERVER_OWNED_ARTIFACT
REVIEW_DECISION = DURABLE_CONTENT_ID_EXACT_RETRY
AUTOMATIC_INSTALL_ACTIVATE_BIND_GRANT_APPLY_EXECUTE = NONE
PROVIDER_CALL = NONE
STORE_SCHEMA = FAC2_USER_VERSION_2_42_TABLES_26_EXPLICIT_INDEXES_64_TRIGGERS
SCHEMA_FINGERPRINT = d5d876f327dc29dc6f4a10476652641172ab8e1f0451a8714fc450f58733541e
MIGRATION_0002_BYTES = 7173
MIGRATION_0002_SHA256 = 3091a49ebcf724f573f91cc0fd22a7c58ebb52fa9d7ed552e32b6526ebeca3cb
~~~

## 12. W6-4 Modules 配置/写入 UI 历史已验收合同

本节冻结 `W6_4_MODULES_CONFIGURATION_UI_ACCEPTED_DEVELOPMENT_SLICE` 的最窄实现边界。该状态只批准
既有 `MODULE_DISABLE` 的浏览器 consumer，不批准新的 operation 或后端 authority。

### 12.1 唯一业务写操作

W6-4 不新增 operation、HTTP route、Application Service、Store seam、Schema、writer 或 publication
owner。浏览器只能依次消费已验收的四类表面：

1. `GET /control/api/v1/modules` 与 exact module detail；
2. `POST /control/api/v1/modules/disable/dry-run`；
3. `POST /control/api/v1/modules/disable/confirmation`；
4. `POST /control/api/v1/modules/disable/mutate`。

#### 12.1.1 UTF-8 ID 的无歧义 HTTP 载体

Control scope 与 Module instance ID 的领域合同仍是 canonical NFC UTF-8 opaque ID；HTTP 不得把领域
ID 收窄为浏览器 `ByteString` 或裸路径字符集。W6-4 冻结以下显式载体：

- 浏览器在 Overview 与全部 Modules 请求中发送
  `X-FreeAgent-Scope-ID-Encoding: base64url-utf8-v1`；`X-FreeAgent-Tenant-ID` 与可选
  `X-FreeAgent-Workspace-ID` 的值是对应 ID **原始 UTF-8 bytes** 的 RFC 4648 raw URL-safe base64，
  无 padding，且必须是 canonical re-encode。marker 缺失时只保留既有 raw header 客户端语义；marker
  重复、未知、空值、非法 UTF-8、非 canonical base64url 或解码后非法 ID 一律在 authority/admission
  之前失败关闭。
- 新浏览器对 detail 的每个 `instance_id` 都把原始 UTF-8 bytes 编为同样的 raw base64url 单路径段，
  并发送 `X-FreeAgent-Module-Instance-ID-Encoding: base64url-utf8-v1`。marker 缺失时保留既有
  path-safe raw segment；因此任何合法旧 ID（包括看似编码前缀的文本）不会被重解释。该 marker
  只允许在 exact detail GET；list、Overview、dry-run、confirmation 与 mutate 携带它必须失败关闭。
- detail 载体不使用 percent-escaped path；padding、非 canonical unused bits、空编码、未知/重复 marker、
  C0/DEL、非 NFC、超限或解码失败均返回有限 `INVALID_REQUEST`/`NOT_FOUND`，不得调用 Application
  Service。解码后的 exact ID 才进入 scope 复验、projection 与 digest/ETag 合同。

唯一候选仍是 exact Tenant scope 中的 Profile `context.provide/v1` Binding；现有 Binding 必须由
server-owned evaluator 证明为 `OPTIONAL`、`DECLARATIVE static/v1`、trusted-instruction config 与 deny-all
Authority。浏览器从 module detail 中看到的 target/Port/failure policy 只是用户选择的惰性候选，
不是资格或 authority；不可从 Overview、digest/ref 字符串、历史 receipt 或 DOM 状态推导可写性。

### 12.2 授权与浏览器状态机

- list/detail 每次仍要求 `OBSERVE`、exact Origin、当前 session、session-bound CSRF 与当前
  scope Permit；不持有 `OPERATE_MODULES` 时可以读取，但不得呈现可用的写控件。
- mutation 只在当前选择为 `TENANT`、session 同时持有 `OBSERVE` 与 `OPERATE_MODULES`、
  且用户显式选择一个 Profile OPTIONAL `context.provide/v1` Binding 时可进入。Workspace scope
  及其他 Port/target/policy 一律只读。
- 状态机固定为 `READY → DRY_RUNNING → DRY_RUN_RESULT`。只有同一 exact body/Published Pointer
  上的 `WOULD_APPLY` 允许创建一个新的内存 idempotency key 并进入 `CONFIRMING →
  CONFIRM_READY`；`NO_CHANGE` 与 `ALREADY_APPLIED` 只展示并重新读取，不发送 MUTATE。
- `CONFIRM_READY` 必须原样展示 server 返回的 inert evaluation/statement 摘要和过期时间，
  并要求第二次明确用户操作。MUTATE 必须复用同一 body、If-Match、idempotency key 与
  operation-evaluation digest；raw proof 只在首次发送时放入 header，发送一经启动即从可重用 UI
  状态中清除。
- 只有 transport 结果不可判定时可显示“exact retry”；retry 必须复用同一 body/key/
  If-Match/evaluation digest，不带旧 proof、不自动 rebase、不替换候选。已知的
  `REVISION_CONFLICT`、`IDEMPOTENCY_CONFLICT`、proof 拒绝或 permission/session 失效不是不可判定结果。
- `APPLIED` 或 durable `NO_CHANGE` 成功后只能清理操作状态并 invalidate/refetch Modules 与
  Overview；禁止乐观改写本地 module/basis。新响应必须再次经过 strict schema/scope/basis/
  digest 验证。

### 12.3 秘密、导航与失败关闭

- raw confirmation proof、raw idempotency key 和可重试请求只存在于当前页面内存；不得进入
  `sessionStorage`、`localStorage`、URL/hash、DOM、日志、error、TanStack Query cache 或 durable client
  cache。现有 rotating resume credential 仍是 `sessionStorage` 中唯一持久授权材料。
- scope、module/binding 选择、session/authorization revision 或导航一变，必须清理 proof/key/
  evaluation/exact-retry。proof 本地过期后也必须清理；server TTL 与当前 Permit 仍是唯一权威。
- `401/SESSION_EXPIRED`、`403/FORBIDDEN`、stale precondition/cursor、响应 digest 错误、未知/重复字段、
  超限响应、非 exact content type/ETag 都必须失败关闭。权限撤销时清除 Modules/Overview
  transport 表示和所有写状态。

### 12.4 明确排除

W6-4 不开放 `MODULE_APPLY`、upgrade review/apply、Learning review/cycle、Model/Action/Channel
Disable、Workspace endpoint mutation、artifact path/URL/signature ingress、grant、staging/install/activation、Provider/
Gateway dispatch、SSE、worker、自动发现/升级/预热、第二 Store/writer 或远程 Control listener。完成
该已验收原子仍只是一项默认关闭的本地 development slice，只能声明本节列出的窄能力，
不得宣称通用 module configuration。

### 12.5 验收证据与下一入口

- strict TypeScript schema/digest/ETag/cursor、UTF-8 scope/detail carrier、operation projection、receipt 与
  exact-retry 负例矩阵，以及 Go HTTP/Application/contract 定向测试均通过；
- committed embedded assets 由两次 deterministic build 逐字节复现，license/distributed-asset/NOTICE
  digest 链闭合；
- 真实 loopback 浏览器验收完成 handoff → Modules list/detail → `WOULD_APPLY` → short-lived confirmation →
  第二次显式确认 → `APPLIED`，Published Pointer 从 revision 2 推进到 3，目标 Binding 经重新读取后消失；
- raw proof、idempotency key 与 CSRF 未进入 URL、DOM、日志或 durable client cache；浏览器 console error 为零。

历史状态是 `W6_4_MODULES_CONFIGURATION_UI_ACCEPTED_DEVELOPMENT_SLICE /
W6_5_SERVER_OWNED_MODULE_ARTIFACT_INGRESS_NEXT`。下一原子只能设计由服务端拥有、内容寻址且失败关闭的
module artifact ingress；在独立合同、实现与门禁闭合前，不得借 W6-4 UI 引入 path/URL/signature、
staging/install/activation、grant、自动升级或远程 Control authority。

## 13. W6-5 server-owned module artifact ingress 已验收合同

历史状态是 `W6_5_SERVER_OWNED_MODULE_ARTIFACT_INGRESS_ACCEPTED_DEVELOPMENT_SLICE /
W6_6_SERVER_OWNED_MODULE_UPGRADE_REVIEW_NEXT`。该切片不是新的 Control operation：

- 不新增 HTTP route、upload、Web UI、remote listener、session/CSRF surface、SSE 或 worker；现有 Control
  default-off composition 与 W6-4 Modules UI 不得调用 ingress；
- 唯一入口是可信本地 Operator CLI `module-artifact-ingress`，且必须显式
  `--enable-module-artifact-ingress`。`source-root` 与 `artifact-root` 是瞬时可信 CLI 输入，不进入
  Control Request/Receipt、Store canonical 或 Backup manifest；
- caller 只能选择 exact SourceID/SnapshotID/ModuleRef/ArtifactDigest，不能提交 package path、URL、signature
  或 upload。package path 来自 Store-owned current Snapshot entry，且只接受 unsigned
  `LOCAL_DIRECTORY + DENY`；
- filesystem 必须先完成 full verification、hidden-stage sync 和 digest-addressed no-replace durable publication；
  唯一 Current Store 随后在同一 `BEGIN IMMEDIATE` 重验 current basis并提交 immutable Artifact 与
  append-only Admission。Source/artifact/Backup tree 使用 held-parent-handle 相对遍历并拒绝 link/reparse、
  hardlink、跨设备与 identity/change/namespace 漂移；artifact root、children 与全部 ancestors 必须可信私有，
  跨进程 root lease 覆盖 crash recovery、publish、sync 与 root 级 256 trees / 512 MiB covered content /
  32,768 paths / 16,384 files / 16 MiB path-name bytes 硬预算（不声称等于实际 allocation blocks）；
- 新 ingress 的同 digest 目标缺失时必须固定发布为目录 `0700`、文件 `0600`。既有同 digest 目标经完整
  bytes/mode 复验后只接受两种 root-global 闭包：全部目录 `0700`、全部文件 `0600`；或全部目录 `0700`，
  仅 canonical `LOCAL_PROCESS + mcp-stdio/2025-11-25` descriptor 精确绑定的唯一 executable 文件为 `0700`，
  其余文件 `0600`。两者均拒绝 special/setid/sticky 与 group/world 权限。artifact root 跨 Store 共享时不得
  依赖任一单独 Store 的 Installation 判断模式；后一种只是已有物理兼容态，不授予当前 Store Installation、
  Activation、execution 或 Control authority。W6-5 的 inert 表示无 Store authority 且本切片不执行，不等同于
  OS executable bit 不存在；
- 物理 orphan、Artifact 或 Admission 均无 Control/mutation authority。ambiguous commit 保留有界 inert bytes；
  只有 durable exact Admission 可以恢复历史 selector，未提交 stale selector不能采用，后来 current 合格 selector
  仍须全包复验；
- W6-5 不创建 Upgrade Review/Decision、Installation、Activation、Binding、grant、Apply、Run/Attempt 或
  execution。Backup 只把 installed 与 ingressed artifact digests 去重合并，并支持 Source 已移除、零
  Installation 的 offline restore；
- 当前 Store identity 是 43 tables / 25 explicit indexes / 64 triggers，fingerprint
  `47981b9bf147ec81c6e98382f281db0d5395187a1f090f7ce183c116fba82a0d`，migration 150,301 bytes /
  SHA-256 `6def433a59fa8f4876894572f1610cae499abc5b389ab5930ea697055419ca86`。

`W6_6_SERVER_OWNED_MODULE_UPGRADE_REVIEW_NEXT` 是 W6-5 收口时的历史 marker；不得借该 marker
在 W6-5 中新增 Review、Install、Activate、Bind、grant、Apply、execute 或在线 mutation。

## 14. W6.6 server-owned Upgrade Review 已验收跨层边界

W6.6 的可信本地 Operator CLI 已完成 server-owned Review/Decision 闭环，但仍不是 Control HTTP
operation。调用方只提交 Tenant、scope、Admission/Review identity、operator principal、request digest
和 Decision reason；服务端读取 Admission 与 Artifact，复验 current basis 并复用 W2-U3 evaluator。
Review/Decision 持久化并按 content identity exact retry；跨 Tenant、stale basis、Artifact tamper、Review
integrity 与不合格 Decision 均失败关闭。该切片没有 HTTP/upload/UI/SSE/worker，不自动 Install、Activate、
Bind、Grant、Apply、Execute，不调用 Provider，也不创建 Runtime/Attempt/Usage/Effect。

Control UI 的 P2 i18n、P3 第二 Provider 与 P4 只读管理面已完成，当前下一入口是 `P5_BETA_GATE`。P2 只国际化现有页面，不因 W6.6 已验收而开放
Upgrade Review mutation；Review/Decision 与 Artifact UI 仍由 P4 在统一 application/control service 之上单独验收。
