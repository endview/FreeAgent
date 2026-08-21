# FreeAgent Module Development v1

> 状态：`EXPERIMENTAL` 的模块作者与离线包验证合同。当前阶段标记是 `W6_5_SERVER_OWNED_MODULE_ARTIFACT_INGRESS_ACCEPTED_DEVELOPMENT_SLICE / W6_6_SERVER_OWNED_MODULE_UPGRADE_REVIEW_NEXT`；W6-5 只新增默认关闭的可信本地 Operator CLI，把 Store-owned current Snapshot 中 unsigned `LOCAL_DIRECTORY + DENY` exact entry 对应的已验证包持久复制为 server-owned、content-addressed、inert Artifact，并记录 append-only Admission。不提升 Module Conformance、Operator Module Apply 或广义在线控制面的成熟度；没有 HTTP/upload、caller package path/URL/signature、UI、Install/Activate/Bind/grant/Review/execute。W6-0、W6-1、W6-2、W6-3、W6-4、U4 与 U3 的历史收口继续保留，但旧 NEXT marker 都不是当前入口。W2-R3 的窄边界保持不变。
>
> W2-U2 历史状态：`W2_U2_DISCOVERY_SNAPSHOT_ACCEPTED_DEVELOPMENT_SLICE`。
>
> W2-R2 WASM Host 尚未生产可用。

FreeAgent 的 Role、Skill、RAG、Memory、MCP、模型和 Channel 都通过同一套 Module/Port
边界接入。Control 配置选择模块（当前主要由 Profile bindings 选择，Channel 另由
Workspace endpoint binding 选择），Admission 再把精确引用冻结到
MemberExecutionSnapshot；Universal Loop 不按模块名称或品牌增加分支。

## 1. 最小包结构

```text
my-module/
├── module.yaml
└── content/
    └── context.json
```

`module.yaml` 虽沿用文件名，v1 内容必须是**无 BOM、无前后空白、精确 RFC 8785 canonical
JSON object**；FreeAgent 不使用通用 YAML parser。Manifest 最小示例：

```json
{"api_version":"freeagent.module/v1","id":"example.role.architect","provides":[{"exact_version":"v1","name":"context.provide"}],"runtime":{"entrypoint":"content/context.json","mode":"DECLARATIVE","protocol":"static/v1"},"version":"1.0.0"}
```

R1 的窄 REMOTE Action 包仍只有不可变数据，不携带可执行文件：

```text
remote-action-module/
├── module.yaml
└── content/
    └── actions.json
```

`actions.json` 必须是 exact `freeagent-action-http-descriptor/v1`，只含有界 Action definitions。
Endpoint 与 SecretRef 属于 Operator-owned Binding，Secret value 属于 dispatch 时的 Host 注入；三者
都不能由 descriptor 自授权，Secret value 也永远不能进入包。

R2 的窄 WASM Action 包同样是显式三文件闭包：

```text
wasm-action-module/
├── module.yaml
└── content/
    ├── actions.json
    └── action.wasm
```

`module.yaml` 的 runtime 固定为 `WASM/freeagent-action-wasm/v1`，entrypoint 指向 canonical
`content/actions.json`；descriptor 固定为 `freeagent-action-wasm-descriptor/v1`，再以
`module_path` 精确引用同一 ArtifactDigest 覆盖的 wasm32 binary。包、Manifest 或 descriptor
都不能请求 Host import、WASI、网络、文件系统或 Secret。

包内普通文件与 canonical Manifest 共同生成本次包的单一 `ArtifactDigest`。路径必须是规范化的
UTF-8 相对路径；symlink、hardlink、reparse point、设备文件、路径逃逸、大小写或 Unicode
冲突都会被拒绝。detached Signature、Publisher Key、Source Policy 和安装回执属于 Operator-side
envelope，应放在 artifact 目录之外；验证器不会根据文件名或不可信 Manifest 自动排除它们。

## 2. Manifest 只提出请求

Manifest 可以声明：

- Module ID 与精确版本；
- 请求的 runtime mode、protocol 与 entrypoint；
- `provides`、`requires` 的 exact `PortRef`；
- `requested_permissions`；
- 惰性的 `config_schema`、`lifecycle` 与 `health` canonical object。

Manifest 不能授予 `ExecutionClass`、Trust、Authority、Effect、FailurePolicy 或 Secret，
也不能决定是否 fallback。未知字段会被拒绝。格式有效不等于当前 Host 支持该 Port 或
runtime，更不等于获得安装或执行资格。

当前声明语义：

| Runtime request | Entrypoint 语义 | 当前边界 |
|---|---|---|
| `DECLARATIVE/static/v1` | 必须是包内 `content/` 普通文件 | 可作为声明式模块进入后续本地策略检查 |
| `TRUSTED_IN_PROCESS/go-in-process/v1` | opaque adapter identity | 仅审核、allowlist、固定摘要的可信实现可被本地激活 |
| `LOCAL_PROCESS/mcp-stdio/2025-11-25` | 必须是包内 `content/` 普通文件 | 当前只有 Operator 完全信任的窄 MCP Tool 开发路径；没有 OS sandbox |
| `REMOTE/freeagent-action-http/v1` | 必须是包内 `content/` descriptor 普通文件 | R1 只支持 `action.provider/v1`；Host 默认关闭，执行还需 exact Operator grant |
| `WASM/freeagent-action-wasm/v1` | 必须是包内 `content/` descriptor，且 descriptor 引用同包 wasm32 binary | R2 只支持 `action.provider/v1`；Host 默认关闭，Apply 与 runtime 各需显式 Operator 开关 |
| `REMOTE/<other opaque protocol>` | opaque install-time request | 格式层接受声明；没有 exact Core handler，不能因此获得 Host、网络或 Secret 能力 |

未来语法有效的 Port 也可通过包格式验证；能否 Activate/Bind 由本地版本化策略决定。

## 3. 离线验证

不提供任何 supply flag 的 legacy 调用为：

```powershell
go run ./cmd/freeagent module-verify --artifact path/to/my-module
```

上例中的 Go tool 在冷 module cache 中可能自行解析依赖；需要严格离线时应使用已构建的
`freeagent` 二进制，或在构建阶段显式关闭 Go Proxy/toolchain 下载。`module-verify` 验证器
本身没有 HTTP client，也不会向网络发请求。

legacy 调用的既有成功输出与错误边界保持逐字节兼容。任一 supply flag 都会显式进入 governed
observation；参数缺失或混合错误时直接失败，不会回落 legacy。签名来源的完整调用形状为：

```powershell
go run ./cmd/freeagent module-verify `
  --artifact <absolute-source-root>\example-module `
  --source-root <absolute-source-root> `
  --source-policy <absolute-envelope-root>\source-policy.json `
  --source-policy-id <exact-lowercase-sha256> `
  --publisher-key <absolute-envelope-root>\publisher-key.json `
  --publisher-key-id <exact-lowercase-sha256> `
  --signature <absolute-envelope-root>\module-signature.json `
  --signature-id <exact-lowercase-sha256>
```

`--revoked-publisher-key-id <exact-lowercase-sha256>` 可重复，用于本次调用的 deny snapshot。unsigned
Policy 只允许 `--source-root`、`--source-policy` 与 `--source-policy-id`；直接 verifier 会拒绝未被
Policy 锚定的 Key、Signature 或撤销输入。

不要把这条 direct-preflight 规则与 U0 Discovery Snapshot 混用。unsigned Snapshot entry 的
`signature_id` 是可空的 authority-free observation 字段，也允许携带语法有效的非空 ID；Snapshot
本身不读取 Key、不验证签名、不授予信任。U1 direct preflight 没有 Snapshot parent 锚点，所以在
unsigned Policy 下禁止全部 Key/Signature/revocation side input。

成功时 stdout 只输出一行稳定 JSON：

```json
{"schema_version":"freeagent.module-package-verification/v1","package_api_version":"freeagent.module/v1","module":{"id":"example.role.architect","exact_version":"1.0.0"},"artifact_digest":"<lowercase-sha256>","artifact_size_bytes":123,"covered_file_count":2,"runtime_request":{"mode":"DECLARATIVE","protocol":"static/v1","entrypoint":"content/context.json"},"provides":[{"name":"context.provide","exact_version":"v1"}],"requires":[],"requested_permissions":[]}
```

该命令：

- 不需要 `--db`，不打开 Current Store；
- 不安装、不激活、不绑定、不访问 Secret 服务；为计算摘要会读取 artifact 内全部普通文件，
  但不解析或输出其正文；
- 不动态加载或执行目标包中的 Go adapter/MCP 构件，不调用模型、Tool 或网络；
- 对文件型 runtime以及当前 `REMOTE/freeagent-action-http/v1` 检查 entrypoint 存在；其他
  REMOTE 协议仍把 entrypoint 当作不授予能力的 opaque request；
- 计算 digest/size 后再做第二遍流式全包复核；
- 不在报告中输出绝对路径、文件正文、Manifest 原文或权限授予结论。

governed observation 另外固定以下规则：

- U1 只接受 exact `LOCAL_DIRECTORY + DENY` Source Policy。`HTTPS_INDEX` 即使合同语法有效也在此
  入口拒绝，应用不会为 U1 创建 HTTP client、RoundTripper 或其他 Source 网络 transport；
- Source Policy、Publisher Key 与 detached Signature 的 canonical 文件及各自 exact content ID 都由
  Operator 在 artifact 外提供。签名算法固定 Ed25519，且只绑定域分隔后的 exact ArtifactDigest；
- 是否要求签名、允许的 Module ID 前缀与 `max_package_bytes` 只由 Source Policy 决定。Module ID
  前缀按点分段匹配：`vendor` 只匹配 `vendor` 或 `vendor.*`，不匹配 `vendorx` 或 `vendor-*`；
- Policy 的 `max_package_bytes` 同时收紧首次 package scan 和最终 digest/size scan；不会先用较宽的
  默认 256 MiB 上限读取整个包，再在扫描后拒绝；
- Publisher Key 撤销列表在调用入口复制并冻结，只是 invocation-scoped immutable deny snapshot。
  它拒绝本次新候选观察，不是持久 Store 权威、全局 current revocation 或历史改写；
- Source root 与 artifact root 必须是稳定的 canonical absolute directory，artifact 必须位于 source
  内。Windows 明确拒绝 UNC/device namespace、NTFS ADS 和 symlink/reparse point；该检查不声称能
  识别映射成普通盘符的网络盘；
- 最终 scan 与目录 identity 复验用于发现本次 preflight 内的漂移；成功不等于跨调用 reservation。

失败时不输出成功报告，进程返回非零。legacy 路径继续只公开
`MANIFEST_INVALID / ARTIFACT_INVALID / ENTRYPOINT_ABSENT / ARTIFACT_DRIFT /
VERIFY_CANCELLED / INTERNAL_ERROR` 之一；governed 路径还可能公开
`SOURCE_INPUT_INVALID / SOURCE_DENIED / SIGNATURE_REQUIRED / SIGNATURE_INVALID /
PUBLISHER_KEY_REVOKED / SOURCE_PREFLIGHT_DRIFT`。不可信 Manifest 值、包内/绝对路径、Policy/Key/
Signature 正文和底层 writer 错误不会进入 CLI 错误。Install、Activate 与实际 Host 选择边界仍必须根据本地策略和预期
摘要验证，不能把一次离线检查当作永久信任证明。具体验证频率属于 Host 合同：受信进程内
Adapter 在首次精确物化时完整复验，随后只复用同一进程内的派生实现 cache，但每次调用仍做
Authority/current Activation 检查；LOCAL_PROCESS MCP 在每次 `Describe` 和 `Execute` 启动
进程前都完整复验 artifact；当前 REMOTE Adapter 首次 production lazy load 时也会有界复验 exact
Manifest、descriptor、digest 与 covered size，但 Secret 只在 Gateway dispatch 晚绑定。三者不能
互相类推。

### 3.1 W2-U2 显式 Source observation

U2 的三个 Store-backed 命令与 `module-verify` 相互独立，并且全部默认关闭：

```text
module-source-register
  --enable-module-discovery --db <store>
  --policy <canonical-file> --policy-id <sha256>
  [--publisher-key <canonical-file> --publisher-key-id <sha256>]
  --expected-policy-revision <revision>

module-source-refresh
  --enable-module-discovery --db <store> --source-id <source-id>
  (--local-directory <absolute-root> |
   --enable-https-module-discovery --https-index-url <canonical-url>
   --allow-https-source-origin <canonical-origin>...)

module-publisher-key-revoke
  --enable-module-discovery --db <store>
  --key-id <sha256> --expected-key-revision <revision>
```

`module-source-register` 以 exact CAS 创建或更新 Operator-owned Source Policy；首次注册也必须显式
提交 `--expected-policy-revision 0`。SourceID 一旦注册，不能改绑到另一 Kind 或 OriginDigest。
签名 Policy 必须同时提供匹配的 Publisher Key；Key revocation 是全局、不可逆且 exact retry 幂等的
Current Store 事实，不删除或改写历史 Snapshot。

Local refresh 只读 canonical root 下的固定 `index.json`（即 `root/index.json`），不接受调用者另选
文件名；读取前后复验目录/文件 identity，并拒绝 UNC/device namespace、ADS、symlink/reparse 与
观察中漂移。HTTPS refresh 除通用 discovery 开关外还必须显式启用 HTTPS，并提供至少一个 exact
canonical origin allowlist。Policy OriginDigest 绑定 exact canonical index URL，运行期 allowlist 绑定
canonical origin；两者不能互相替代。请求固定为一次 HTTP/1.1 GET，不使用 proxy、redirect、retry、
HTTP/2、keep-alive、compression、cookie、凭据、条件请求或自定义 Header；解析 DNS 后只要任一
结果为 special-use/non-public 地址就整次拒绝，再确定性选择一个 literal IP 拨号。

refresh 顺序固定为：

```text
read exact Store refresh basis
  → observe one bounded Index outside the Store transaction
  → BEGIN IMMEDIATE
  → recheck Source Policy/Key revision and revocation
  → derive and persist exact immutable Snapshot
  → verify full discovery semantic closure
  → COMMIT
```

相同 current Index 的 exact retry 返回相同 Snapshot、observation revision 与 timestamp；A→B→A 不会
冒充 exact retry。Store 还在跨 Source Snapshot、既有 Installation 与 materialized Learning Version
之间保持全局 `ModuleID + opaque exact Version → ArtifactDigest` 一致；同 ref/同 digest 可共享，异
digest 失败关闭。Index 所有 entry 声明的 package size 总和另受 Core-owned 1 GiB ceiling 约束。

成功 refresh 只输出 Source/Policy/Index/Snapshot identity、observation revision、`entry_count` 与
observed time，不输出 path、URL、Policy/Key canonical bytes 或 Index 正文。U2 不读取 entry package
path、不下载模块包、不读取 detached Signature、不做包级 `module-verify`、不生成 Candidate/Decision、
不 staging/Install/Activate/Bind/Apply，也不授予 runtime、Trust、Authority 或 Effect。Backup、Verify
与 Restore 只重验 bundle 内的 Store canonical closure，零网络、零 Source/包读取；Restore 不自动
refresh。U3 已按下节负责无权限 Candidate/review/decision；U4 仍须在真正 staging 前重验 current facts，
并复用既有 `module-dry-run` / `module-apply` / CAS。

### 3.2 W2-U3 离线升级审核

U3 不从 Index `PackagePath` 获取包，也不实现 updater。Operator 必须自行提供一个本地 unpacked target
artifact；signed Source 还必须从 artifact 外提供 exact detached Signature。两个入口均默认关闭：

```text
module-upgrade-review --enable-module-upgrade-review
  --db <closed-current-store> --tenant <tenant>
  --expected-pointer-revision <revision>
  --source-id <source> --snapshot-id <snapshot>
  --current-instance <instance> --current-activation <activation>
  --current-module <module> --current-version <opaque-version>
  --current-artifact-digest <sha256>
  --target-module <same-module> --target-version <different-opaque-version>
  --target-artifact-digest <sha256> --target-instance <new-instance>
  --port <port> --port-version <version> --port-binding-index <ordinal>
  --target-kind <PROFILE|WORKSPACE_CHANNEL_ENDPOINT>
  (--profile <profile> | --workspace <workspace> --endpoint <endpoint>)
  --artifact-directory <local-unpacked-artifact>
  [--signature <detached-signature>] [--operator-requested-rollback]

module-upgrade-decide --enable-module-upgrade-review
  --db <closed-current-store> --tenant <tenant>
  --review-id <sha256> --candidate-id <sha256>
  --decision <APPROVE|REJECT> --operator-principal <principal>
  --reason-file <bounded-local-file> [--confirm-tenant-wide-reject]
```

首片只接受 `EXACT_VERSION_CHANGE`：Module ID 必须相同，opaque exact Version、ArtifactDigest 与 target
Instance 必须都不同。Core 不解释 SemVer、`latest`、newer 或 downgrade；`--operator-requested-rollback`
只把 Operator 意图写为 Review reason，不降低其他门禁。U0 的 `INSTALL` Candidate 仍是纯合同，不允许
U3 在没有 current Binding/Activation/Installation 时猜测 scope。

Review 的权威输入顺序固定为：

```text
exact current Snapshot entry
  → Store rebuilds SourcePolicy / Index / Snapshot parents
  → exact PROFILE or WORKSPACE_CHANNEL_ENDPOINT Binding
  → current Catalog Instance / Activation revision / Installation
  → explicit local artifact double verification
  → exact target Manifest content evidence
  → all published Binding impacts + unique shared-handler assessment
  → inert module-upgrade-review/v1
```

target canonical Manifest 是参与 ArtifactDigest 的 verifier evidence，作为
`CONTENT_MODULE_MANIFEST/application/json` content-addressed record 写入既有 `content_records`。Review
只携带 Manifest safe summary/ref、完整 runtime mode/protocol/entrypoint、Provides/Requires/permissions diff、
所有引用 current Instance 的 published Binding impacts、PublishedBasis、handler assessment 和 digest-only
required grants；主机路径、Index `PackagePath`、URL、signature bytes、Secret、Config/Authority 正文都不得
进入 Review、CLI 输出或 U3 专表。Review 结论只能是：

- `WOULD_APPLY`：九个现有 exact handler/selector 中唯一兼容路径可确定，全部 published Binding 可投影；
- `CONFLICT`：scope/config/authority/Binding、target Instance、port/require/permission 或已发布事实冲突；
- `UNSUPPORTED`：target runtime/Port/consumer schema 不存在现有 exact handler。

U3 没有 `NO_CHANGE`，因为入口已要求 target Version 与 ArtifactDigest 均不同。额外 artifact、endpoint、
SecretRef 等只显示为 digest-only grant requirement，绝不在 U3 授予。

Candidate 是全局不可变 supply fact；Review 属于 exact Tenant/scope 并绑定 exact current/target evidence；
Decision 终态绑定 exact `review_id`。`APPROVE` 只允许 current basis 仍匹配的 `WOULD_APPLY` Review，且
仍不是 reservation、grant、Install 或 Apply authority。`REJECT` 是有意的 Tenant-wide supply deny，必须
显式设置 `--confirm-tenant-wide-reject`，按 `{tenant_id, review_key}` 跨该 Tenant 的 PROFILE/Workspace
scope 抑制重复候选；不能影响另一 Tenant。scope-specific incompatibility 应保持 `CONFLICT/UNSUPPORTED`，不得
冒充 Tenant-wide reject。

Candidate、Review、Decision 与 target Manifest evidence 进入唯一 Store 的 Backup/Verify/Restore semantic
closure；U3 不下载、stage、Install、Activate、grant、Bind、Apply，不调用 Host/Model/Action/Channel，
也不产生 Run、Attempt、Usage 或外部效果。Pure Chat 对 U3 facts 与 artifact 保持零访问。

U3 收口后的 U4 强制入口门把原 CLI 私有的 9 条 generic/Model policy 与 2 条 Document Insight reserved
exact selector 抽取到 Review 和 Apply 共同复用的唯一 `internal/modulehandler` 实现；未复制第二张 handler
表，也未让 Review/Apply 漂移。其“下一原子片重验 approval/current basis 并进入既有 Apply/CAS”的描述
属于历史 Handler-gate；当前窄 U4 已按本文件后文合同完成。

## 4. 生命周期边界

```text
verify package → Install → Activate → Bind → freeze Run → selected Host use
```

- `module-verify`：只证明本次观察到的包格式、路径闭包和摘要有效；governed 成功也只返回同一
  `freeagent.module-package-verification/v1`，不产生 reservation、grant、stage、Store fact 或 Apply
  authority。
- Install：登记精确 Module ID + Version + ArtifactDigest；同版本不同摘要必须拒绝。
- Activate：由本地策略授予 ExecutionClass 和 adapter identity；Manifest 不能自授权。
- Bind：消费方冻结 Config、AuthorityCeiling、FailurePolicy 与有序 ProviderBinding。
- Run Admission：把精确选择写入 MemberExecutionSnapshot；运行中不重新发现或换版本。
- Exact materialization：只按冻结的 ArtifactDigest + AdapterIdentity 构造无外部效果的本地
  实现；同键并发首次加载可合并，cache 不保存 Trust、Catalog 或授权结论。当前 REMOTE loader
  也只缓存 descriptor 派生的 native Adapter，不缓存 endpoint、SecretRef 或 Secret material。需要读取包内容的
  Loader 使用 `ScanArtifactDirectoryContext` 等有界 Context API，取消时不得返回部分文件，
  也不得把 Context Value 变成选择、权限或缓存键。WASM loader 只有在 runtime 显式启用且 exact
  Catalog Binding 首次使用时，才重验 descriptor、binary、ABI 与资源 ceiling 并构造 Adapter；未选择
  时不得读取、编译或实例化 guest。
- Execute：模型只能提出受 schema 约束的 Action；外部效果必须经过原 DispatchAttempt、
  Gateway permit 和 UNKNOWN 禁止语义重放边界。

## 5. Operator Module Apply v1

当前成熟度分层记录：本地显式装配 v1 为
`W2_D_LOCAL_ASSEMBLY_ACCEPTED_DEVELOPMENT_SLICE`，只在冻结的本地 v1 范围 accepted；固定
Document Insight 双 Port 纵链另以
`W2_E5_B_DOCUMENT_INSIGHT_DUAL_PORT_ACCEPTED_DEVELOPMENT_SLICE` 验收；窄 REMOTE Action Host 另以
`W2_R1_REMOTE_ACTION_HOST_ACCEPTED_DEVELOPMENT_SLICE` 验收；窄 WASM Action Host 另以
`W2_R2_WASM_HOST_ACCEPTED_DEVELOPMENT_SLICE` 验收；本节 Operator 命令合同总体仍为
`EXPERIMENTAL`。包含更多 Port、任意第三方进程内模块、通用 multi-Port/多 Binding、其他 REMOTE、
R2 exact 合同外的 WASM/ABI/Host、自动发现和在线控制面的广义通用装配仍为 `PLANNED`。这些成熟度
不能互相替代。

当前运行后控制面切片在停机状态下保留 9 个 protocol handler tuple。基础 handler key 是
`exact Port + runtime mode/protocol + verified consumer schema`；固定产品行还可由 Core 追加精确
Module identity selector：

- `context.provide/v1 + DECLARATIVE/static/v1 + context-binding-config/v1`，用于声明式 Role、
  Prompt 或静态 Skill；
- `context.provide/v1 + TRUSTED_IN_PROCESS/go-in-process/v1 + knowledge-context-binding/v1`，
  用于本地确定性只读 Knowledge 数据包；
- `context.provide/v1 + TRUSTED_IN_PROCESS/go-in-process/v1 + memory-context-binding/v1`，
  用于本地有界确定性 Memory 数据包；
- `action.provider/v1 + LOCAL_PROCESS/mcp-stdio/2025-11-25 + action-binding-config/v1`，用于
  受完全信任的本地 MCP Action；
- `action.provider/v1 + REMOTE/freeagent-action-http/v1 + action-binding-config/v1`，用于
  默认关闭、由 Operator 精确授权 artifact/endpoint/SecretRef 的窄 HTTPS Action；
- `action.provider/v1 + WASM/freeagent-action-wasm/v1 + action-binding-config/v1`，用于
  默认关闭、由 Operator 精确授权 artifact 的窄本地 WASM Action；
- `action.provider/v1 + TRUSTED_IN_PROCESS/go-in-process/v1 + action-binding-config/v1`，用于
  受信、编译进 Core 的 `text.stats` Action；
- `channel.transport/v1 + TRUSTED_IN_PROCESS/go-in-process/v1 + channel-binding-config/v1`，用于
  first-party Workspace-scoped loopback Channel Endpoint；
- `model.generate/v1 + TRUSTED_IN_PROCESS/go-in-process/v1 + model-binding-config/v1`，用于
  同一内建 DeepSeek artifact/adapter/provider Instance 内的 Flash/Pro 显式替换。

九个 tuple 共用同一个 canonical `module-apply-plan/v1`、Installation/Activation、Current
Store、Control/Catalog CAS 和 Assembly Compiler。计划显式选择 `ENABLED` 或 `DISABLED`，
并要求调用者给出当前 `expected_pointer_revision`；默认不自动发现、升级、替换、重排或
选择模块。

Core 在这 9 个 tuple 前还检查 2 个 Document Insight 保留 selector。两行分别是：

- `context.provide/v1 + TRUSTED_IN_PROCESS/go-in-process/v1 + knowledge-context-binding/v1`；
- `action.provider/v1 + TRUSTED_IN_PROCESS/go-in-process/v1 + action-binding-config/v1`。

这两行还必须同时精确匹配 Module ID、Version 与 ArtifactDigest，不能只按 Port/runtime/schema
落入普通 Knowledge 或 `text.stats` handler。固定合同为：

- Module：`freeagent.builtin.document-insight@1.0.0`；ArtifactDigest：
  `838ff9ddd45186f0cdb26021d16902b2bfd7581c2dc0d7b72014cf4f48d0f7ea`；covered size：
  `1097` bytes；
- Adapter identity：`freeagent.adapter.document-insight/v1`；runtime request：
  `TRUSTED_IN_PROCESS/go-in-process/v1`；entrypoint：`content/source.json`；
- `provides` 必须按 canonical 顺序恰好为
  `[action.provider/v1, context.provide/v1]`，`requires` 必须恰好为
  `[model.generate/v1]`，`requested_permissions` 必须恰好为 `[knowledge.read]`；
- Context 必须使用 governed `knowledge-context-binding/v1` 与
  `knowledge-authority-ceiling/v1` 收窄 source/scope/limit；Action 只映射
  `text.stats`，EffectClass 固定为 `none`，结果上限固定为 `256` bytes；两个 Binding 的
  FailurePolicy 都是 `REQUIRED`；
- 同一 Installation、Activation、Catalog Instance 和 Adapter 同时服务两个 Port。任一 ID、版本、
  摘要、Port、顺序、runtime、entrypoint、Require 或 permission 漂移都 fail-closed，且不得回退到
  普通单 Port handler。

这里的“2 个 selector”只是固定产品身份的两条 Core policy 行，不是两个模块、第二套 Runtime，
也不是通用 multi-Port 注册机制。

`ENABLED` 计划包含目标 Tenant、Profile、Instance、exact `port`、精确 Module
ID/Version/ArtifactDigest、module 内 exact `expected_runtime_request:{mode,protocol}`，以及统一
Binding 的 `port_binding_index`、Config、Authority ceiling 和 FailurePolicy。索引是目标 Profile
内同一个 exact Port Binding 子序列的 0-based
插入位置，不是模型看到的 Action 排序或 fallback 权重；其他 Port Binding 的相对顺序必须
保持不变。同一 Profile、Port 和 Instance 最多出现一次。`expected_runtime_request` 只表达
Operator 对待安装构件 Manifest request 的预期；Core 使用 exact Port、该 request 与已验证
consumer schema 选择唯一 handler。Plan 和 Manifest 都不能授予 Trust、ExecutionClass、
AdapterIdentity、Authority 或 handler identity，也不能借该字段选择任意实现。

声明式 Context 的 Config 必须是 exact `TRUSTED_INSTRUCTION`、`allow_summary=false`、
`allow_drop=false`、`parameters={}`；Authority 必须是 exact deny-all
`authority-ceiling/v1`。Core 从已经验证的 Manifest entrypoint 恢复一个 canonical
`static-context/v1`，把其内容摘要写入 Binding 的 `StaticContextRefs`。这条路径不执行包内
代码、不读取 Secret，也不接受任一种 artifact grant。

普通单 Port Knowledge 不是声明式静态 Context，也不新增 `knowledge.retrieve` Port。只有 Config 为 exact
`UNTRUSTED_DATA`、`allow_summary=false`、`allow_drop=false`，且 `parameters.schema_version` 为
`knowledge-context-binding/v1` 时，同一 `context.provide/v1` 才选择 Knowledge 变体。其
FailurePolicy 必须为 `REQUIRED`，Binding 不携带 `StaticContextRefs`。Manifest 必须只提供该
Port，并固定声明 `TRUSTED_IN_PROCESS + go-in-process/v1`；entrypoint 必须是 canonical `content/` 路径中的
`knowledge-source/v1`。Artifact 中恢复出的 source、Config source 与
`knowledge-authority-ceiling/v1` source 必须完全相同；Authority 的 Tenant 必须等于计划 Tenant，
非通配 Workspace/Agent scope 必须存在于当前 Control。Context 的语义顺序继续要求全部
`TRUSTED_INSTRUCTION` 位于 `UNTRUSTED_DATA` 之前。

普通单 Port Knowledge Manifest 只有两个 exact 形状。legacy 形状的 `requires` 与
`requested_permissions` 都为空，继续作为 immutable 兼容路径；E5-A governed 形状必须恰好是
`requires:[model.generate/v1]` 与 `requested_permissions:[knowledge.read]`。共享 `moduleapi`
classifier 被 Apply、Dry-run、生产加载、Current Store publication、exact retry、公开 Verify 与
Backup/Restore 复用；permission-only、require-only、重复、额外或重排值均拒绝。governed Require
必须解析到同一 target Profile 内唯一 exact Model Binding，并沿
`Binding → Catalog → exact Activation → Installation → canonical Manifest` 闭合；跨 Profile、
缺失或歧义均 fail-closed。该 Require 不触发额外模型调用，也不开放通用 multi-Port。

`knowledge.read` 只是 Manifest 请求。Core 只从 exact Knowledge Config/Authority 的同一 source、
Tenant、当前 Control 内 Workspace/Agent scope 与读取上限交集派生有效 grant；Manifest、Plan 与
Provider 不能自授权，也不新增 grant 表或第二授权 Store。

普通单 Port Knowledge 的本地策略唯一授予 `TRUSTED_IN_PROCESS` 和固定内建
`freeagent.adapter.knowledge.lexical/v1`；Manifest 与 Plan 都不能选择 Adapter、Trust 或扩大
Authority。该路径没有额外 CLI digest grant，因为包只是不可变数据，执行实现是编译进 Core 的
固定只读 Adapter；任何未来允许包内代码、REMOTE Host 或自动执行计划的放宽，都必须新增独立
授权合同和验收。

以下 Memory 内容同步 W3-M1 已验收合同，不把实现重新归属于 W2-D。Memory 使用同一
`context.provide/v1`，但 Config 必须是 exact `UNTRUSTED_DATA`、
`allow_summary=false`、`allow_drop=false`，且 `parameters.schema_version` 为
`memory-context-binding/v1`；FailurePolicy 固定为 `REQUIRED`，Binding 不携带 StaticContext refs。
Manifest 同样只提供该 Port、没有 requires/requested permissions，并固定声明
`TRUSTED_IN_PROCESS + go-in-process/v1`。本地策略唯一授予
`freeagent.adapter.memory.deterministic/v1`；Core-owned `memory-authority-ceiling/v1` 按 exact
Tenant、Agent、Workspace、kind 和读取上限收窄权限。Apply 仅在 Control/Catalog CAS 成功并复验
后补齐一次空 Genesis；已有 Memory head 逐字节保留，CAS 失败不得写入，重入不能覆盖、合并或推进
已有 head。Disable 只影响未来 Binding，不删除历史 Memory revision。

普通单 Port Knowledge 与 Memory 的 Manifest 虽请求 `TRUSTED_IN_PROCESS/go-in-process/v1`，
它们仍禁止 `--allow-trusted-in-process-artifact`。除固定 compiled `text.stats` 外，唯一窄例外是
上述 exact Document Insight identity：其 Context 与 Action 两步都必须提供与固定候选摘要完全
相同的 trusted-in-process artifact grant。Core 必须先命中保留 selector，再接受该 grant；错误
Module/Version/Digest/Port 或普通 Knowledge/Memory 包不能借此获得信任。该参数不是通用 Trust
开关，也不允许任意数据包携带或执行进程内代码。

首个 MCP stdio Adapter 的 Action Config `parameters` 必须是精确 canonical `{}`。这是当前
Adapter 的窄实现限制，不是统一 Module/Port 协议的永久限制；未来支持参数化 Provider 时必须
另行版本化并通过 Config schema、Authority 与 Host 验收，不能由本命令静默放宽。

compiled `text.stats` 只接受固定的 `action-binding-config/v1` 与
`action-authority-ceiling/v1`：唯一公开/Provider ActionID 都是 `text.stats`、EffectClass 为
`none`、结果上限不超过编译实现的 256 bytes，并受 exact Tenant/Workspace scope 限制。包只能
请求该 runtime tuple；Core handler 与本地 grant 才决定能否激活，不能泛化为任意进程内 Action。

R1 REMOTE Action 同样只接受 `action-binding-config/v1` 与
`action-authority-ceiling/v1`，FailurePolicy 固定为 `REQUIRED`。Config `parameters` 必须是 exact
`remote-action-http-binding-parameters/v1`，只保存 canonical HTTPS endpoint 与 SecretRef；Action
映射、EffectClass、结果上限和 Tenant/Workspace scope 继续由 Binding 与 Authority 裁剪。启用时
必须同时精确提供：

- `--allow-remote-action-artifact <sha256>`；
- `--allow-remote-action-endpoint <https-url>`；
- `--allow-remote-action-secret-ref <secret-ref>`。

这些是本次 Operator 调用的瞬时 grant，不写入 Manifest，也不允许 Secret value 进入 plan。Apply 与
Dry-run 只验证 descriptor、Config、Authority 和三项 grant，保持零 Secret lookup、零网络、零
Provider 调用。生产 `chat/serve` 还必须显式使用 `--enable-remote-actions`，并用可重复的
`--remote-action-secret-env <secret-ref>=<ENV_VAR>` 提供晚绑定映射；未启用、无映射、重复映射或
运行时环境变量缺失都失败关闭。

R2 WASM Action 也只接受 `action-binding-config/v1` 与 `action-authority-ceiling/v1`，FailurePolicy
固定为 `REQUIRED`，Config `parameters` 必须是 exact `{}`，全部 Action EffectClass 固定为 `none`。
Manifest 必须只提供 `action.provider/v1`，不得携带 Require 或 requested permission；descriptor 的
Action mapping、结果上限和 ABI 必须与 Binding/Authority 交集完全闭合。启用时必须精确提供：

- `--allow-wasm-action-artifact <sha256>`。

该瞬时 grant 只批准本次候选摘要，不能授予网络、文件系统、Secret、WASI 或 Host import。它与
`--allow-local-mcp-artifact`、`--allow-trusted-in-process-artifact`、
`--allow-remote-action-artifact` 四类互斥。Apply/Dry-run 会做有界 binary section preflight 与
wazero compile validation，但不实例化或执行 guest。生产 `chat/serve` 必须同时显式传入
`--enable-wasm-actions` 与至少一个 `--allow-wasm-runtime-artifact <sha256>`；该运行期 allowlist 由
Operator 本地策略授予，不持久化，也不能由 Manifest、descriptor 或模块包请求。非 canonical、重复或
非当前 WASM Catalog digest 均失败关闭；未启用或未获 exact 运行授权时不得读取或编译 artifact。

WASM ABI 固定为 Core WASM 1.0、wasm32，只允许三个 exports：`memory`、
`freeagent_alloc_v1(i32)->i32` 与 `freeagent_execute_v1(i32,i32)->i64`。imports、start、WASI 和 Host
Module 均拒绝，因此没有网络、文件系统或 Secret 能力。Host 使用 `wazero v1.12.0` interpreter；
module/request/output ceiling 分别为 16 MiB/128 KiB/32 KiB，memory initial 最多 32 页且必须声明
maximum 最多 256 页，table 最多 65,536 elements，全进程最多并发 4 个 guest instance，单次验证与
执行最多 5 秒。

唯一 Gateway 只能在原 DispatchAttempt 已持久为 PENDING 后调用私有 `ExecutePrepared`。Usage
receipt 只记录 Host-observed input/output/memory/elapsed，engine 固定为
`wazero-interpreter/v1.12.0`，并明确写 `instruction_metering=UNSUPPORTED`；不得出现 token、price、
cost、raw trap 或 guest diagnostic。trap、timeout、ABI 和 output rejection 均确定性 FAILED；只有
既有事务提交失败或崩溃遗留 PENDING 才把同一 Attempt 恢复为
`UNKNOWN/RECOVERED_PENDING_AFTER_CRASH`，禁止 replay、fallback、换 Provider 或替代 Attempt。

Document Insight 的 Context→Action 启用不是一个隐式多 Port 事务，而是两个显式、可审计的
`module-apply-plan/v1` CAS：

1. 先对 `context.provide/v1` 执行 Dry-run 与 Apply；该步创建或复用唯一
   Installation/Activation/Catalog Instance，并闭合同一 Profile 的 Model Require 与
   Core-owned `knowledge.read` grant；
2. 再以新的 `expected_pointer_revision` 对 `action.provider/v1` 执行 Dry-run 与 Apply；该步必须
   复用前一步完全相同的 Module/Artifact/Activation/Adapter identity。Action-first 在 TEMP/staging、
   artifact publication 和 Store 写入之前以 `TARGET_CONFLICT` 拒绝。

禁用顺序严格为 Action→Context：先禁用 `action.provider/v1`，保留仍有效的 Context/RAG 与 Catalog Instance；
再禁用 `context.provide/v1`，无剩余引用时才从 current Catalog 移除 Instance。Context-first Disable
在 Action 仍存在时以 `PUBLICATION_FAILED` 零写拒绝。该顺序不新增跨计划事务，也不外推为通用
multi-Port、多个 ProviderBinding 合并或 fallback 能力。

`DISABLED` 计划按目标 Profile + exact Port + Instance 删除精确 Binding；它不需要读取或运行
目标 artifact。只有当 Control 中已无任何 Profile 或 Channel 引用该 Instance 时，才从下一
Catalog 删除对应 Activation；immutable installation、activation、content、历史 Run 和
Attempt 均保留。旧冻结 Run 保持原精确引用；需要 current Activation 的实际执行仍按既有
deny-only 校验，因此撤销不重写历史，也不重放 UNKNOWN。

Apply 必须复用唯一 Current Store 和唯一 Control/Catalog pointer：

1. 只在 `chat/serve` 已停止且取得 Store writer fence 后执行；活动 owner 时拒绝。先运行既有
   Store-only recovery，把 crash-left PENDING 按原规则收口为 UNKNOWN，并在发布前证明
   PENDING 为零；UNKNOWN 原样保留且不阻止 deny-only 禁用。
2. `ENABLED` 先离线验证源包，再复制到 artifact root 内的隐藏直系临时目录并复验精确
   digest/size；该位置保证与 final artifact 同文件系统。声明式包只恢复静态 Context；
   Knowledge/Memory 数据包只构造固定内建 Adapter；MCP 包构造 registration 时不得启动进程或
   执行 initialize/list/call；`text.stats` 与 exact Document Insight 只复验各自固定 compiled
   handler/Adapter；REMOTE Action 只恢复受摘要覆盖的 descriptor 并构造 native Adapter，不解析
   Secret、不发 POST，也不加载或执行包内 Provider 代码；WASM Action 只做 descriptor/binary/ABI/
   resource preflight 与 compile validation，不实例化或执行 guest。
3. final artifact 使用 no-replace 发布；已存在时只能接受完整 digest/size 精确一致，并须
   有界同步全部普通文件、再次复验且完成 no-launch Host 构造。发布后必须先删除隐藏临时
   目录并同步 artifact root，任一清理或同步失败都在首次 Store 写入前 fail-closed。
4. 复用同 Module ID+Version 的既有 Installation；不存在时才 Install。Instance 的新
   Activation revision 使用该 Instance 已有最大 revision + 1，避免崩溃遗留的不可达
   Activation 与下一次计划冲突。
5. `Install → Activate → PutContent` 只产生不可变 staging 事实；只有
   `PublishControlCatalog` 的 `expected → expected+1` CAS 让新 Control/Catalog 同时可见。
6. 计划 canonical bytes 的 digest 确定性派生 SnapshotID/GenerationID。精确重试返回
   `ALREADY_APPLIED`，当前状态已完全相同时返回 `NO_CHANGE`；任何不同配置、不同模块、
   不同位置或 stale pointer 均拒绝，不做隐式 rebase。

Apply 在上述 fast-path 判断、TEMP/staging、artifact publication 与首次 Store 写入之前，必须通过
Store view 重放完整 current publication closure；Dry-run 的 immutable observer 同样在
`ALREADY_APPLIED`/`NO_CHANGE` 或候选计算前执行该校验。损坏的
Binding/Catalog/Activation/Installation/Manifest、Requires、permission 或既有 Config/Authority
闭包不能冒充成功。校验失败不产生 candidate publication、新 artifact 或新 Store 行。

文件系统与 SQLite 不能组成一个跨介质事务，因此崩溃可能留下不可达 artifact、Installation、
Activation 或 Content；它们不在 current Catalog 中，不授予执行权，可由同一计划精确重试
复用。首片不为此新增 apply ledger、第二 Store、GC 或自动清理。若 CAS 提交结果无法可靠确认，
CLI 只能重读同一 pointer 和确定性 ID：精确命中才算成功，无法判定则报告
`APPLY_OUTCOME_UNKNOWN`。这只是 Operator 控制面调用结果，不得映射、修改或重放任何
Model/Action/Channel Attempt。

完整备份恢复时，ArtifactDigest 仍只覆盖规范化内容而不覆盖平台权限位。Restore 因而从已
验证的 Manifest 重建可移植的私有模式；MCP 包的目录与唯一 executable 为 `0700`、其余普通
文件为 `0600`，声明式、Knowledge、Memory、Document Insight、REMOTE descriptor 与 WASM 三文件
包不产生 executable。恢复不传播来源的 setuid、group/world 位，并在未发布 staging 中完成文件
fsync、精确内容复验和必要的 no-launch Host 校验。恢复不会执行声明式内容、调用 Knowledge/Memory
provider、启动 MCP 进程、解析 REMOTE Secret、联网，或编译、实例化、执行 WASM guest。对 governed
Knowledge 与固定
Document Insight，Backup verify 与 Restore 还必须重放对应共享 Manifest classifier、完整 module
identity、same-Profile Require 与 `knowledge.read` grant 闭包；Document Insight 还必须闭合 ordered
双 Port Provides、固定摘要和 Adapter identity。任一部分声明或身份篡改都 fail-closed。

该命令不接收模型 Key，不调用模型或网络；R1 即使装配 REMOTE/HTTP 也只发布冻结事实，R2 即使
装配 WASM 也不实例化或执行 guest。在线热加载、动态 Go plugin、通用 Secret broker、自动升级、
卸载或后台控制器仍未实现。它增加的是可选模块的显式装配入口，
不是第二条 Runtime 或效果执行链；真实 Action 仍只能走原
`ActionProposal → DispatchAttempt → Gateway → private executor`。

### 5.1 最小计划与命令

下面五行是按当前编码器顺序生成的完整 canonical JSON；计划文件必须只包含对应单行，不得追加
换行或改变字段顺序。第一行启用本地 MCP Action，第二行启用声明式 Role/Skill，第三行从普通
Pure Chat Store 启用受信 compiled `text.stats`，第四行启用 exact WASM Action，第五行禁用指定
Context Binding。Knowledge 与
Memory 使用与第二行相同的 Port/Binding 外形，但 runtime expectation、Config、Authority 与
artifact 必须满足各自上文的 exact 闭包，不能把示例中的声明式策略原样复用。前两行的重复摘要
只是 wire 示例，不代表仓库提供对应可安装构件；第三行使用仓库真实 `text.stats` 锁。第四行的
digest/size 也是占位值，必须替换为真实三文件 WASM artifact 闭包。Document Insight 不复用这些
示例，必须使用上文固定 identity，并分别提交 Context 与 Action 计划：

```json
{"binding":{"authority_ceiling":{"allowed_provider_action_ids":["text.stats"],"allowed_workspace_ids":["local-chat"],"max_effect_class":"none","max_result_bytes":256,"schema_version":"action-authority-ceiling/v1","tenant_id":"default"},"config":{"actions":[{"local_effect_class":"none","max_result_bytes":256,"provider_action_id":"text.stats","public_action_id":"text.stats"}],"parameters":{},"schema_version":"action-binding-config/v1"},"failure_policy":"REQUIRED","port_binding_index":0},"desired_state":"ENABLED","expected_pointer_revision":1,"instance_id":"mcp-text-stats","module":{"artifact_digest":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","artifact_size_bytes":1234,"exact_version":"1.0.0","expected_runtime_request":{"mode":"LOCAL_PROCESS","protocol":"mcp-stdio/2025-11-25"},"id":"example.mcp.text_stats"},"port":{"exact_version":"v1","name":"action.provider"},"profile_id":"pure-chat","schema_version":"module-apply-plan/v1","tenant_id":"default"}
```

```json
{"binding":{"authority_ceiling":{"effects":[],"filesystem_roots":[],"network_allowlist":[],"schema_version":"authority-ceiling/v1","secret_refs":[]},"config":{"allow_drop":false,"allow_summary":false,"parameters":{},"placement":"TRUSTED_INSTRUCTION","schema_version":"context-binding-config/v1"},"failure_policy":"REQUIRED","port_binding_index":0},"desired_state":"ENABLED","expected_pointer_revision":1,"instance_id":"role-architect","module":{"artifact_digest":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","artifact_size_bytes":1234,"exact_version":"1.0.0","expected_runtime_request":{"mode":"DECLARATIVE","protocol":"static/v1"},"id":"example.role.architect"},"port":{"exact_version":"v1","name":"context.provide"},"profile_id":"pure-chat","schema_version":"module-apply-plan/v1","tenant_id":"default"}
```

```json
{"binding":{"authority_ceiling":{"allowed_provider_action_ids":["text.stats"],"allowed_workspace_ids":["local-chat"],"max_effect_class":"none","max_result_bytes":256,"schema_version":"action-authority-ceiling/v1","tenant_id":"default"},"config":{"actions":[{"local_effect_class":"none","max_result_bytes":256,"provider_action_id":"text.stats","public_action_id":"text.stats"}],"parameters":{},"schema_version":"action-binding-config/v1"},"failure_policy":"REQUIRED","port_binding_index":0},"desired_state":"ENABLED","expected_pointer_revision":1,"instance_id":"text-stats","module":{"artifact_digest":"2331b8b5209f4acffd7fef96f55ce1e0841bc3c4e39d90581efb941ecae9b955","artifact_size_bytes":1936,"exact_version":"1.0.0","expected_runtime_request":{"mode":"TRUSTED_IN_PROCESS","protocol":"go-in-process/v1"},"id":"freeagent.builtin.action.text_stats"},"port":{"exact_version":"v1","name":"action.provider"},"profile_id":"pure-chat","schema_version":"module-apply-plan/v1","tenant_id":"default"}
```

```json
{"binding":{"authority_ceiling":{"allowed_provider_action_ids":["example.wasm.echo"],"allowed_workspace_ids":["local-chat"],"max_effect_class":"none","max_result_bytes":256,"schema_version":"action-authority-ceiling/v1","tenant_id":"default"},"config":{"actions":[{"local_effect_class":"none","max_result_bytes":256,"provider_action_id":"example.wasm.echo","public_action_id":"wasm.echo"}],"parameters":{},"schema_version":"action-binding-config/v1"},"failure_policy":"REQUIRED","port_binding_index":0},"desired_state":"ENABLED","expected_pointer_revision":1,"instance_id":"wasm-action-instance","module":{"artifact_digest":"cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc","artifact_size_bytes":4096,"exact_version":"1.0.0","expected_runtime_request":{"mode":"WASM","protocol":"freeagent-action-wasm/v1"},"id":"example.wasm.action"},"port":{"exact_version":"v1","name":"action.provider"},"profile_id":"pure-chat","schema_version":"module-apply-plan/v1","tenant_id":"default"}
```

```json
{"desired_state":"DISABLED","expected_pointer_revision":2,"instance_id":"role-architect","port":{"exact_version":"v1","name":"context.provide"},"profile_id":"pure-chat","schema_version":"module-apply-plan/v1","tenant_id":"default"}
```

grant 由所选 handler 决定：MCP 启用计划只接受与候选相同摘要的
`--allow-local-mcp-artifact`；compiled `text.stats` 与上述 exact Document Insight 两步只接受与各自
候选相同摘要的 `--allow-trusted-in-process-artifact`；REMOTE Action 必须同时提供候选摘要、exact
HTTPS endpoint 与 SecretRef 三项 remote grant；WASM Action 只接受匹配候选摘要的
`--allow-wasm-action-artifact`。LOCAL_PROCESS、TRUSTED_IN_PROCESS、REMOTE、WASM 四类 artifact
grant 互斥；声明式、普通单 Port
Knowledge 与 Memory 启用只接收 artifact，禁止任一种 grant；禁用计划禁止 artifact 与 grant 参数：

```powershell
go run ./cmd/freeagent module-apply `
  --db .local/current.sqlite `
  --artifact-root .local/artifacts `
  --plan .local/enable-module.json `
  --artifact .\unpacked-module `
  --allow-local-mcp-artifact <artifact-sha256>

go run ./cmd/freeagent module-apply `
  --db .local/current.sqlite `
  --artifact-root .local/artifacts `
  --plan .local/enable-role.json `
  --artifact .\unpacked-role-module

go run ./cmd/freeagent module-apply `
  --db .local/current.sqlite `
  --artifact-root .local/artifacts `
  --plan .local/enable-knowledge.json `
  --artifact .\unpacked-knowledge-module

go run ./cmd/freeagent module-apply `
  --db .local/current.sqlite `
  --artifact-root .local/artifacts `
  --plan .local/enable-text-stats.json `
  --artifact examples/bootstrap-artifacts/freeagent.builtin.action.text_stats/1.0.0 `
  --allow-trusted-in-process-artifact 2331b8b5209f4acffd7fef96f55ce1e0841bc3c4e39d90581efb941ecae9b955

go run ./cmd/freeagent module-apply `
  --db .local/current.sqlite `
  --artifact-root .local/artifacts `
  --plan .local/enable-remote-action.json `
  --artifact .\unpacked-remote-action `
  --allow-remote-action-artifact <artifact-sha256> `
  --allow-remote-action-endpoint https://api.example.com/v1/action `
  --allow-remote-action-secret-ref secret.example.remote

go run ./cmd/freeagent module-apply `
  --db .local/current.sqlite `
  --artifact-root .local/artifacts `
  --plan .local/enable-wasm-action.json `
  --artifact .\unpacked-wasm-action `
  --allow-wasm-action-artifact <artifact-sha256>

go run ./cmd/freeagent module-apply `
  --db .local/current.sqlite `
  --artifact-root .local/artifacts `
  --plan .local/disable-module.json
```

Apply 完成不等于允许运行 REMOTE 或 WASM Action。只有普通 `chat` 或 `serve` 入口显式追加相应
runtime flags，Catalog 选中的 Adapter 才可能在唯一 Gateway 中执行：

```powershell
--enable-remote-actions `
--remote-action-secret-env secret.example.remote=FREEAGENT_REMOTE_EXAMPLE_TOKEN
```

```powershell
--enable-wasm-actions `
--allow-wasm-runtime-artifact <sha256>
```

REMOTE 映射参数只含环境变量名称，不含 Secret value；不得把真实 Token 写进命令行、plan、
Manifest、descriptor、文档或测试夹具。WASM 总开关不授予构件执行资格；runtime artifact allowlist
只批准本进程的 exact digest，也不授予额外 capability，仍保持零 imports/WASI/Host Module/network/
filesystem/Secret。

成功 stdout 是单行 `freeagent.module-apply-result/v1`，`status` 只可能为：

| 状态 | 含义 |
|---|---|
| `APPLIED` | 本计划完成唯一 Control/Catalog CAS，并经完整 publication 与目标状态复验 |
| `ALREADY_APPLIED` | `expected+1` 已是该计划的完整 canonical publication；没有再次发布或执行模块 |
| `NO_CHANGE` | 当前 pointer 上已经是相同目标状态；没有创建新 revision |

失败不输出本地路径或底层错误，只公开固定代码：`INVALID_FLAGS`、`PLAN_INVALID`、
`GRANT_REQUIRED`、`ARTIFACT_INVALID`、`STORE_BUSY`、`STORE_INVALID`、`RECOVERY_FAILED`、
`POINTER_CONFLICT`、`TARGET_CONFLICT`、`PUBLICATION_FAILED`、`APPLY_OUTCOME_UNKNOWN`、
`CANCELLED` 或 `INTERNAL_ERROR`。`APPLY_OUTCOME_UNKNOWN` 不能自动重试语义效果；Operator
只能在服务仍停止时用同一 canonical 计划重入并依靠 exact-retry 判定。

### 5.1 当前 Binding 观察与独立禁用

W2-B1 在同一停机操作面增加三个命令；`--tenant` 必须显式给出 exact identity：

```powershell
go run ./cmd/freeagent module-list `
  --db .local/current.sqlite `
  --tenant default `
  --profile pure-chat

go run ./cmd/freeagent module-inspect `
  --db .local/current.sqlite `
  --artifact-root .local/artifacts `
  --tenant default `
  --instance role-architect

go run ./cmd/freeagent module-disable `
  --db .local/current.sqlite `
  --artifact-root .local/artifacts `
  --plan .local/disable-module.json
```

`module-list` 输出单行 canonical `freeagent.module-binding-list/v1`。它只列当前 Control 的
Profile Binding，并逐项闭合到当前 Catalog 与 immutable Installation；可选 `--profile`
是 exact 过滤，不存在时返回 `PROFILE_NOT_FOUND`，存在但无 Binding 时返回 `"bindings":[]`。
结果携带完整 PublishedBasis，并保持 Control 的 canonical Profile 顺序与每个 Profile 的
Binding 语义顺序；`port_binding_index` 是该 Profile 内同 exact Port 子序列的原 0-based
位置，不能因过滤或输出重排而改变。

`module-inspect` 输出单行 canonical `freeagent.module-inspection/v1`，只接受当前 Catalog 中的
exact Instance。它复验 `<artifact-root>/<artifact-digest>` 的完整 digest、covered size 与
stored canonical Manifest 闭包，返回 Installation/Activation identity、Catalog Provides、
所有当前 Profile Binding site，以及 Runtime request、Provides、Requires、requested
permission kinds 等非敏感摘要。默认输出只有 refs；不得输出 artifact root、Config、
Authority、StaticContext 正文、Manifest 原文或 Secret。Instance 不在当前 Catalog 时返回
`INSTANCE_NOT_FOUND`；artifact 或 Manifest 不闭合时返回 `ARTIFACT_INVALID`。

上述两个 current 查询命令只使用 immutable/query-only `ReadOnlyObserver`；服务必须停止，数据库必须
self-contained 且无 WAL/SHM sidecar。它们不取得 writer fence、不恢复 PENDING、不启动模型、
MCP、Module Host、网络或 Secret resolver，也不创建 Run、Attempt 或外部效果。

`module-disable` 只接受 exact canonical `module-apply-plan/v1` 且
`desired_state=DISABLED`；它没有 artifact、grant、force、自动 revision 或隐式 rebase 参数。
通过预检后直接复用 `module-apply` 的 Store owner、startup recovery、唯一 Control/Catalog CAS
和 `freeagent.module-apply-result/v1`。它不卸载、不 purge、不删除 artifact 或历史事实。

精确历史查询见下一节；在线热查、自动发现/升级和通用管理控制台仍未提供；
Operator Module Apply 总体成熟度继续是 `EXPERIMENTAL`。

### 5.2 精确历史 Binding 查询

`module-history` 查询一个调用方已经保存的 exact 历史 Control/Catalog pair；它不枚举 revision，
也不计算启用/禁用 diff。revision 可来自此前 `module-list.basis`、`module-dry-run.observed_basis`
或 Apply 验收记录：

```powershell
go run ./cmd/freeagent module-history `
  --db .local/current.sqlite `
  --tenant default `
  --control-revision 2 `
  --catalog-generation 2 `
  --profile pure-chat
```

成功 stdout 是单行 canonical `freeagent.module-binding-history/v1`，直接携带 exact
`ControlSnapshotRef`、`CatalogGenerationRef`、可选 Profile 过滤、Source/Activation refs 和当时的
有序 Binding refs。命令先通过 immutable/query-only
`ReadOnlyObserver.LoadControlCatalogRevision` 恢复 exact pair，再通过既有窄 Installation 投影读取
并解析 stored Manifest，以闭合 Source、Activation、Port 与 Binding；这不是 artifact 复验。
历史 pointer revision 没有独立事实源，因此输出不得构造 historical PublishedBasis 或猜测旧
pointer。命令不需要也不读取 artifact root，不输出 Manifest 正文或摘要，不读取或输出
Config/Authority/StaticContext 正文，不运行 recovery，不启动 Host、模型、网络、MCP 或 Secret
resolver。mismatched pair 返回 `HISTORY_NOT_FOUND`；不可达 Activation 不属于任何历史 Binding，
不能据此推断为 DISABLED，也不得枚举 revision 或计算启用/禁用 diff。

### 5.3 只读变更预测

`module-dry-run` 使用与 `module-apply` 完全相同的 canonical plan 与参数约束：ENABLED 必须给出
artifact，本地 MCP 必须给出同一 exact MCP digest grant，compiled `text.stats` 与 exact Document
Insight 必须给出同一 trusted-in-process digest grant；REMOTE Action 必须给出 exact artifact、
endpoint 与 SecretRef 三项 grant；WASM Action 必须给出 exact WASM artifact grant；四类 artifact
grant 互斥，声明式、普通单 Port Knowledge 与
Memory 模块禁止 grant，DISABLED 禁止 artifact 和全部 grant。Document Insight 的
Dry-run 也必须按 Context→Action 顺序观察同一逐次 pointer，不能把两个预测当成一项 reservation
或一次原子 Apply。

```powershell
go run ./cmd/freeagent module-dry-run `
  --db .local/current.sqlite `
  --artifact-root .local/artifacts `
  --plan .local/enable-role.json `
  --artifact .\unpacked-role-module
```

成功 stdout 是单行 canonical `freeagent.module-dry-run-result/v1`，状态只可能为
`WOULD_APPLY`、`NO_CHANGE` 或 `ALREADY_APPLIED`。结果包含完整 `observed_basis`、完整
`candidate_basis`、`startup_recovery_required` 和有限变化枚举：Installation 为
`NONE/CREATE/REUSE`，Activation 为 `NONE/CREATE/REUSE_CURRENT`，Binding 为
`NONE/INSERT/REMOVE`，Catalog 为 `NONE/ADD_INSTANCE/RETAIN_INSTANCE/REMOVE_INSTANCE`。
Binding 只公开 index、Content refs、FailurePolicy，不公开 Config、Authority、StaticContext、
Manifest 正文、主机路径或 Secret。

`PROJECTED_NOT_RESERVED` candidate 只绑定本次 observed Basis，是可丢弃预测，不是授权、
reservation、receipt 或可回传执行的对象；真实 Apply 必须重新 recovery、观察、验包、计算和
CAS。命令只以 immutable/query-only `ReadOnlyObserver` 打开 self-contained Store，incoming
artifact 只复制到系统 TEMP；成功、失败和取消都必须清理。它不取得 writer、不把 PENDING
改为 UNKNOWN、不重放 UNKNOWN、不写 Store/source/artifact root，不启动 MCP process/protocol、
模型、网络或 Secret resolver，也不执行 `text.stats`、Document Insight、REMOTE Action 或 WASM
guest。对 Knowledge 与
Document Insight Context，它会复验 exact Artifact/Config/Authority/source/scope 闭包并构造固定
内建只读 Adapter，但不得调用 provider。`startup_recovery_required=true`
只表示存在 Model/Action/Channel PENDING，不表示能够 Apply。

本地 Knowledge 与 W3-M1 Memory 已接到既有统一 Apply/Dry-run 和生产 Context 纵链；它们没有
新增 Port、Runtime、Store、Loop、Gateway 或 backup schema。本地显式装配 v1 当前为
`W2_D_LOCAL_ASSEMBLY_ACCEPTED_DEVELOPMENT_SLICE`：五类本地模块集中纵链、逐 Entry/inspect
闭包修复、Windows/WSL2 ext4 全仓与发布门禁已经闭合；accepted 只适用于该窄范围。Operator
Module Apply 与本模块作者/包 Conformance 合同仍为 `experimental`。W2-E2 已以受信、编译进 Core
且无外部效果的 `text.stats` 验收统一 Apply、真实 model→action→model、Disable 与 Backup/Restore，
状态为 `W2_E2_TRUSTED_TEXT_STATS_APPLY_ACCEPTED_DEVELOPMENT_SLICE`；它不开放任意第三方进程内
Action。W2-E3 已验收 first-party Workspace loopback Channel Apply；W2-E4 已验收同一内建
DeepSeek Instance 内 Flash/Pro 显式替换。E5-A 的历史状态为
`W2_E5_A_REQUIRES_PERMISSION_GRANT_ACCEPTED_DEVELOPMENT_SLICE / W2_E5_B_NEXT`，只闭合 governed
Knowledge 的 shared classifier、same-Profile Model Require、Core-owned `knowledge.read` grant、
full publication fast-path 与 Backup/Restore。该批 Windows `go test ./...` 的 `30` 个仓内 Go package
在 `221.9s` 内通过；另有 `1` 个 external compatibility package 由 `sdk/moduleapi` 嵌套测试在临时
独立 module 中编译验证；
vet、mod verify、gofmt、Docs（`38`）、Capability Matrix（`49` 项/`0 stable`）、License（`35` 个
Go dependency/`57` 个 distributed asset）、Branding 与 PublicTree 门禁通过，且没有真实 API
调用或新增 schema/table/Runtime/Store/Loop/Gateway。这些数字继续只属于 E5-A 历史切片。

前序固定双 Port 增量保持为
`W2_E5_B_DOCUMENT_INSIGHT_DUAL_PORT_ACCEPTED_DEVELOPMENT_SLICE`。网络无关产品 E2E
`TestW2E5BDocumentInsightProductAcceptanceV1` 以 `-count=3` 通过；独立完整
`cmd/freeagent` package 输出为 `185.746s`、墙钟为 `186.968s`；`30` 个仓内 Go package 的全仓
墙钟为 `217.539s`，其中 `cmd/freeagent` package 为 `215.004s`；另有 `1` 个 external compatibility
package 由 SDK 嵌套测试编译验证。`go vet ./...`、`go mod verify` 与全仓 gofmt 门禁通过；Docs 为
`38` 份 Markdown，Capability Matrix 为 `49` 项/`0 stable`，License 为 `35` 个 Go dependency/
`59` 个 distributed asset，Branding 与 PublicTree 均通过。本切片没有调用真实 API。

E5-B 只证明上述固定、受信、编译进 Core 的一个双 Port 产品纵链。任意第三方进程内 Action、
通用 multi-Port、多个 ProviderBinding 合并、REMOTE/WASM、不可信包内代码、自动发现和稳定在线
控制面仍未进入该入口；Operator Module Apply 与 Module Conformance 继续为 `experimental`，
广义通用装配继续为 `planned`，W6/W7 与 Beta 也未完成。

其后的 R1 增量保持为
`W2_R1_REMOTE_ACTION_HOST_ACCEPTED_DEVELOPMENT_SLICE`。R1 增加第 8 个
exact handler，但继续复用同一 Module Apply、Installation/Activation、Catalog、Assembly、Action
Proposal、DispatchAttempt、Gateway 与 Backup：

- production 集中链已经从 Catalog 穿过 lazy loader、`remoteactionhttp.NewFromArtifact`、native
  Adapter 与唯一 Gateway；Secret resolver 在原 Attempt 已持久为 PENDING 后被调用，并在 POST 前
  拒绝，原 Attempt 确定性进入 FAILED，exact retry 不新增 Attempt 或 resolver 调用；
- production lazy loader 连续两次拒绝已篡改 descriptor，失败不缓存且 Secret resolver 为 0；
  Disable 后删除 artifact 再运行真实 Pure Chat，REMOTE artifact、Adapter、resolver 与 Action
  Attempt 均为 0；Provider 返回合法 `FAILED + HTTP 422` 时只发生一次 RoundTrip；
- 既有 hermetic Adapter/长链测试覆盖 SUCCEEDED、UNKNOWN、终态持久化失败、重启和完整
  Backup/Restore。UNKNOWN 继续禁止重放、换 Provider 或替代 Attempt；没有真实公网 HTTPS
  第三方互操作证据，不能把该开发切片冒充生产网络 SLA。

R1 没有增加普通表、第二 Runtime/Store/Loop/Gateway、Catalog pointer 或效果账本。R1 自身不
验收其他 REMOTE、WASM 或不可信本地隔离；WASM 由后续独立 R2 切片验收。

R2 收口时的增量为
`W2_R2_WASM_HOST_ACCEPTED_DEVELOPMENT_SLICE / W2_R3_UNTRUSTED_MODULE_ISOLATION_NEXT`。R2 增加
第 9 个 exact `action.provider/v1 + WASM/freeagent-action-wasm/v1 + action-binding-config/v1`
handler，但继续复用同一 Apply、Catalog、Assembly、Action Proposal、原 DispatchAttempt、唯一
Gateway 与 Backup：

- `--allow-wasm-action-artifact` 是 Operator 对 exact candidate digest 的瞬时 grant，且与
  LOCAL_PROCESS、TRUSTED_IN_PROCESS、REMOTE artifact grants 互斥；Apply/Dry-run 验证完整三文件
  artifact、descriptor/Binding mapping、ABI 和上限，但零 guest execution；
- runtime 默认关闭，R2 当时由 `--enable-wasm-actions` 允许 Catalog-selected lazy loader；未选择或 Disable
  后零 artifact read/compile/instance。执行固定使用 wazero v1.12.0 interpreter、Core v1/wasm32，零
  imports/WASI/Host Module/network/filesystem/Secret，并受 16 MiB module、128 KiB request、32 KiB
  output、32/256 memory pages、65,536 table elements、全进程 4 instances、5 秒 ceiling 约束；
- 唯一 Gateway 在原 PENDING Attempt 后调用私有执行入口。receipt 明确
  `instruction_metering=UNSUPPORTED` 且无 token/cost/price；trap、timeout、ABI/output 都是确定性
  FAILED；只有事务/崩溃遗留 PENDING 恢复原 Attempt UNKNOWN，禁止 replay/fallback；
- Backup/Verify/Restore 保存三文件字节闭包、Action result/receipt 与终态，但不编译、实例化或执行
  guest。该 R2 边界不是 OS/container，也不提供面向恶意多租户的强隔离。

R3 当前状态为 `W2_R3_UNTRUSTED_MODULE_ISOLATION_ACCEPTED_DEVELOPMENT_SLICE`。模块作者需要遵守：

- 非 builtin Module ID 可以使用上述 WASM 协议，但运行资格必须由 Operator 的 exact runtime digest
  allowlist 单独授予；模块包不能声明 TrustClass、ControlClass、Authority、runtime allowlist 或撤权策略；
- Adapter 仅由 ModuleID、Version、ArtifactDigest、ExecutionClass 与 AdapterIdentity 识别。同一 artifact
  可跨多个合法 Activation 共享，InstanceID、ActivationRevision、Tenant、Workspace、Config、Authority
  与 Binding 权限不会被缓存进 Adapter；
- Gateway 在解析前和实际执行前重验 current Activation。第二次检查前撤权时 guest=0；第二次检查后
  已接纳的 `Effect=none` guest 可在既有 5 秒 ceiling 内完成，Disable 不改写历史 Attempt；
- v1 撤权只支持停机流程，不支持热撤权或运行中 guest 强杀。最后引用 Disable 后，重启时移除 runtime
  allowlist，历史 artifact 即可删除而不影响 Pure Chat；活动服务持有 Store 时离线命令返回 `STORE_BUSY`。

R3 仍只是 wazero 进程内零 Host capability 的纯计算 containment。OS/container、seccomp、namespace、
restricted token、fuel/instruction/RSS 硬上限、Go/wazero/OS 漏洞防护与生产恶意多租户隔离属于后续候选，
不属于本模块开发合同。

W2-U0 已增加纯 Go 供应链合同；U1 只把其中的签名和本地 Source Policy 接入当前
`module-verify` candidate preflight，没有接入 Module Apply、Store 或网络。U2 随后只增加
Store-backed Source/Index/Snapshot observation；U3 再增加无权限的离线 Candidate/Review/Decision。
三者自身都不产生 Apply authority：

U3 历史状态：`W2_U3_UPGRADE_REVIEW_ACCEPTED_DEVELOPMENT_SLICE / W2_U4_OPERATOR_APPLY_NEXT`。

- `ModulePublisherKeyV1` 与 `ModuleSignatureV1` 固定 Ed25519、canonical padded base64 和 exact
  ArtifactDigest 域分隔签名；模块作者不能借此声明 Trust 或权限；
- `ModuleSourcePolicyV1` 属于本地 Operator。Manifest、Index 或模块包不能创建、放宽或替换它；
- `ModuleDiscoveryIndexV1` 和 `ModuleDiscoverySnapshotV1` 只是有界、不可变、无权限的候选观察；
- `ModuleUpgradeCandidateV1` 只表示操作者选定的 exact Version/ArtifactDigest 差异。Version 仍是不透明
  字符串，不支持 SemVer range、`latest` 或自动更新；
- `ModuleCandidateDecisionV1` 仍只记录 `APPROVE/REJECT` 数据；U3 Store 把它绑定 exact Review，
  `APPROVE` 重验 current `WOULD_APPLY`，`REJECT` 需显式确认 Tenant-wide ReviewKey 抑制，但都不安装或 Apply；
- 七份 wire 都要求 exact RFC 8785 canonical JSON、未知字段拒绝、content ID 核对和 defensive copy。

当前模块包即使携带 `module.sig`，也不会因此自动取得安装或运行资格；默认 ArtifactDigest 也不会按
文件名排除它，因此 Policy、Key 与 Signature envelope 必须放在 artifact 外。U1 成功不建立
reservation、grant、stage 或 Store fact，也不能作为 Apply authority。签名本身不授予 runtime、Trust、
ExecutionClass、Authority、Effect、Secret 或 Binding。

现有手工 `module-apply` 继续直接复验 candidate，并使用 LOCAL_PROCESS、TRUSTED_IN_PROCESS、REMOTE、
WASM 各自既有的瞬时 exact artifact grant；它不要求先取得 U1 observation，也不把 U1 结果当成可复用
授权。九个 exact handler、Dry-run/Apply CAS、默认关闭运行时和 UNKNOWN 边界保持不变。

U2 已从唯一 Current Store 读取并闭合 SourcePolicy→Index→Snapshot observation parent 与 current
revocation，但不预留候选、不获取 package，也不调用 Apply。U3 已生成并审核 Candidate/Review/Decision，
自身仍不获取 package 或发布。U4 把唯一的 9 条 generic/Model policy 与 2 条 Document Insight reserved
exact selector 提取为 Review/Apply 共享实现，并增加默认关闭的 `module-upgrade-apply` 窄入口：

- 必须显式绑定 exact Tenant/Review/APPROVE Decision；只接受 `LOCAL_DIRECTORY + DENY` 的显式
  `--source-root`，不读取 Index `PackagePath`；`--signature` 必须显式出现（unsigned 使用空值），所有
  artifact/endpoint/SecretRef grant flags 必须显式且为空；
- 模块作者不能选择 upgrade scope。首片只允许一个 PROFILE `context.provide/v1` impact，且 target 必须
  精确为 `DECLARATIVE + static/v1 + TRUSTED_INSTRUCTION + deny-all Authority`。Model、Channel、Action、
  `SINGLE`、shared-current、fanout、多 impact 与任何 grant 都失败关闭；
- Core 依次完成 historical U1 verification、current Store revalidation、same exact plan、current U1
  verification 与 pre-stage revalidation，再复用既有 Apply/CAS。Review 与 Apply 共享同一 evaluator，
  不存在独立 dry-run admission、第二 handler 表或模块自授予；
- 旧 Instance 只能在原 ordinal 原位替换；其他 Binding canonical bytes/order 和旧 Run 不变，新 Run
  才使用 target。相邻 current exact retry 只是 plan idempotency，不是 durable receipt；后续 publication
  后返回 `POINTER_CONFLICT`；UNKNOWN 不重放；
- U4 不新增 Store 表，Backup/Verify/Restore 与 Pure Chat 零访问边界不变。其历史状态为
  `W2_U4_OPERATOR_APPLY_ACCEPTED_DEVELOPMENT_SLICE / W6_0_CONTROL_API_CONTRACT_NEXT`。

因此 U1 report、U3 `APPROVE`、Signature 或 Manifest 仍不能单独授权安装；U4 也不开放自动升级、
Model/Channel/Action replacement、通用 multi-Port/多 Binding 或广义自由装配。

### W6-0 控制 API 合同对模块作者的历史边界

W6-0 当时状态为 `W6_0_CONTROL_API_CONTRACT_ACCEPTED_DEVELOPMENT_SLICE /
W6_1_APPLICATION_SERVICES_READ_API_NEXT`。W6-0 只冻结
`control-session/v1`、`control-scope/v1`、`control-view-snapshot/v1`、
`control-operation-request/v1`、`control-operation-receipt/v1` 和 `control-event-cursor/v1` 六份
pure canonical wire。Web view 不能冒充或改写 Core `control-snapshot/v1`；operation request 必须显式
区分 `DRY_RUN/MUTATE`，动态 ID/时间不参与 semantic request，UNKNOWN 只返回原 exact receipt 且禁止
重放。该切片没有 listener、handler、session store、SSE、UI、production command wiring、Schema 或
receipt table；当时 39 份 Markdown、37 个源码 package、35 个 production closure package 与 32 表
Store 保持不变。模块作者不能据此假定在线安装、升级、撤权或 mutation 已可用；历史
`W6_1_APPLICATION_SERVICES_READ_API_NEXT` 只允许后续接入默认关闭的只读/Dry-run application services。

### W6-1 Modules 读取与 Disable Dry-run 对模块作者的边界

历史收口为 `W6_1_APPLICATION_SERVICES_READ_API_ACCEPTED_DEVELOPMENT_SLICE /
W6_2_CONTROLLED_MUTATIONS_AUDIT_NEXT`。

- Control 默认关闭。显式启用时，同一 `serve` 以 Chat listener 加 exact `tcp4 127.0.0.1:0` Control
  listener 共享唯一 Store、Application Services、startup-closed Admission 与生命周期；owner-only
  bootstrap handoff 和 process-local session 不向模块授予任何新能力。除 bootstrap exchange 外，Modules
  GET 与非安全方法都要求 exact session 和 session-bound CSRF；GET 缺失或错误 CSRF 返回 401；
- Modules list/detail 只返回 scope-filtered、allowlisted、脱敏投影，不返回 artifact path、Manifest/Config/
  Authority 正文、Secret material 或跨 Workspace 数据；模块包不能控制 cursor、ETag、scope 或 capability；
- `MODULE_DISABLE` 只开放 effect-free Dry-run。exact If-Match、当前 published basis、binding target、instance
  与 port 必须全部匹配；`Idempotency-Key` 和 confirmation header 被拒绝。返回的 `DRY_RUN` receipt 只驻
  响应/进程，不是 durable receipt，不调用既有停机 CLI Apply/Disable，也不改变 Activation、Binding、
  Control/Catalog publication 或 artifact；
- 该 W6-1 历史原子当时为 39 份 Markdown、44 个源码 package、44 个 production dependency-closure package；Store 是
  identity 不变的 32 表。没有 Control mutation、durable receipt、SSE、UI、第二 Store/writer、后台 worker
  或主动预热。下一入口只审计受控 mutation，不授权模块自行安装、升级、撤权、grant、Bind 或 Apply。

### W6-2 confirmation 对模块作者的历史边界

该 confirmation 历史原子的状态为 `W6_2_CONFIRMATION_CONTRACT_ACCEPTED_DEVELOPMENT_SLICE /
W6_2_DURABLE_RECEIPT_SCHEMA_NEXT`；audit 历史收口为
`W6_2_CONTROLLED_MUTATIONS_AUDIT_ACCEPTED_DEVELOPMENT_SLICE / W6_2_CONFIRMATION_CONTRACT_NEXT`。

- 六项 audit 只保留 TENANT/PROFILE、exact `context.provide/v1`、`OPTIONAL`、`DECLARATIVE`、
  trusted-instruction config 与 deny-all Authority 的 `MODULE_DISABLE` 作为未来首候选。模块不能通过
  Manifest 把该候选扩成 `REQUIRED`、Workspace endpoint、grant、artifact/network/Provider/runtime 操作；
- canonical-frozen ConfirmationStatement 将 exact typed input 与 `ModuleDisableEvaluationV1` digest 绑定。模块正文、
  artifact path、Authority/Config 内容与 raw proof 都不能进入该稳定 Statement；
- raw proof 为 process-local 32-byte、最多 2 分钟且不超过当前 session absolute expiry 的 capability，全局 256、每 session 8，registry 只存 proof digest，
  不进入 Store、Backup 或日志，也不授予模块安装、激活、Bind、Apply 或 Host 能力；
- 当时 39 份 Markdown、45 个源码 package、44 个 production dependency-closure package、32 表 Store；
  无 durable row、mutation route 或 Schema 变化，首片 `UNKNOWN` 不可达。

下一入口只允许一张 append-only receipt 表的 32→33 rebuild-only 候选和 Backup/atomic closure；尚未实现，
模块作者不得据此假定在线 Disable 或其他 mutation 已可用。

### W6-2 durable receipt Schema 对模块作者的历史边界

历史收口状态为 `W6_2_DURABLE_RECEIPT_SCHEMA_ACCEPTED_DEVELOPMENT_SLICE /
W6_2_MODULE_DISABLE_MUTATION_WIRING_NEXT`。confirmation 的历史入口仍是
`W6_2_CONFIRMATION_CONTRACT_ACCEPTED_DEVELOPMENT_SLICE / W6_2_DURABLE_RECEIPT_SCHEMA_NEXT`。

- 共享 `module-apply-plan/v1` 只表达 TENANT/PROFILE `context.provide/v1` 的 exact Disable 计划；
  `module-disable-publication-receipt/v1` 只接受 `OPTIONAL`、`DECLARATIVE`、trusted-instruction config、
  deny-all Authority，并排除 principal/request/key/proof/session/time；
- 该 W6-2 历史原子中的 33 表 Store 只增加 append-only `control_operation_receipts`。其公开 Store 写入口只提交经过
  neutral evaluator 重放的 `NO_CHANGE`；exact retry 返回原行，同 identity 不同 Request 冲突；
- 在该历史原子中，`APPLIED` 只有 strict contract、DDL 与 semantic verifier 形状，没有公开 insert。模块不得自行写 receipt、
  发布 pointer 或把 receipt 当作 grant；其下一 wiring 必须由唯一 Store publication owner 把 domain receipt、Control
  receipt 与 Control/Catalog pointer CAS 放进同一事务；
- Backup Create/Verify/Restore 严格验证现存 receipt canonical 与 immutable parents。因为没有外部
  completeness anchor，这不证明整行从未删除，也不授予模块任何恢复、安装、激活、Bind、Apply 或 Host 权限；
- 该历史原子的证据为 39 份 Markdown、47 个源码 package、46 个 production dependency-closure package、33 表 Store；
  fingerprint/migration 为 `51be9081a815e6379380b93e745db03d33ca2f358962f85ace63bce2534dcc10`、
  67,998 bytes / `8feea38beb505ab4f371afe32eecd877744062bd149d0d24982839c5450fc952`。
  当时没有在线 mutation route/handler、confirmation endpoint、SSE/UI/worker。

### W6-2 MODULE_DISABLE mutation wiring 对模块作者的历史边界

历史收口状态为 `W6_2_MODULE_DISABLE_MUTATION_WIRING_ACCEPTED_DEVELOPMENT_SLICE /
W6_3_WEB_SHELL_READ_ONLY_OVERVIEW_NEXT`。

- 本在线入口不是通用模块 mutation。它只允许 TENANT/PROFILE exact `context.provide/v1`，并要求待移除的
  existing Binding 已经是 `OPTIONAL`、`DECLARATIVE static/v1`、trusted-instruction config 与 deny-all
  Authority；Manifest、module package、UI 或 proof 都不能扩大这一资格；
- 仅显式 `--enable-control` 时存在 confirmation/mutate 两路。confirmation 是 effect-free evaluation，raw proof
  仍是短期 process-local authority；mutate exact retry 每次仍须当前 Origin/session/session-bound CSRF/TENANT
  Permit，先查 durable receipt 的 hit 只是不依赖旧 proof 或旧 session identity，并非匿名 resolver；miss 才
  claim 当前 proof 并重验 authorization/basis；
- `NO_CHANGE` 不产生 domain effect；`APPLIED` 只由唯一 Store owner 在同一事务提交 domain receipt、
  Control receipt、Control/Catalog publication 与 pointer CAS。模块不能自行插入 receipt、调用 Store seam、
  触发 Provider/Gateway 或把 receipt 作为安装、激活、Bind、Apply、Host 或权限 grant；
- Backup 只验证现存 APPLIED 与 tamper；没有 external completeness anchor，不能证明任意整行 receipt 未被删除。
  publication-without-receipt 由 receipt insert 强制失败时整个 publication rollback 证明；
- 该历史原子当时为 39 份 Markdown、48 个源码 package、48 个 production dependency-closure package、
  33 表 Store。无其他 mutation、SSE/UI/worker 或第二 Store/writer；当时下一入口只允许只读 Web Shell/Overview。

### W6-3 Web Shell / read-only Overview 对模块作者的历史边界

历史状态为 `W6_3_WEB_SHELL_READ_ONLY_OVERVIEW_ACCEPTED_DEVELOPMENT_SLICE /
W6_4_MODULES_CONFIGURATION_UI_NEXT`。

- Overview 的 Module Candidate 只是 Store-derived、scope-filtered 的只读审核投影；它不是 package approval、
  installation、activation、Binding、Apply、Disable、grant、reservation 或 Host authority；
- Web Shell 只能搜索和查看已授权 Overview，不能直接调用 Module Host，也没有 Modules mutation UI、SSE、worker
  或 durable client cache。Manifest、页面状态、local/session storage 都不能选择写入 scope；
- 该历史切片 Store 为 41 tables / 23 indexes / 56 triggers，fingerprint
  `87d63a2e468ee27ee2c0e1918a92e580532825bde85dbb872b9c795bf577f2c1`，migration 143,588 bytes /
  SHA-256 `5bd9743e988b82750f785c015f07c11fbcf6025da70148405aec11106b4efb22`。这些 observation facts 不扩大
  Module contract 或权限；
- `W6_4_MODULES_CONFIGURATION_UI_NEXT` 是该切片当时的下一入口；后续页面仍必须消费唯一
  Application Services/Store publication owner，不得从只读 projection 或历史 receipt 推导写 authority。

### W6-4 Modules configuration UI 对模块作者的历史边界

历史状态为 `W6_4_MODULES_CONFIGURATION_UI_ACCEPTED_DEVELOPMENT_SLICE /
W6_5_SERVER_OWNED_MODULE_ARTIFACT_INGRESS_NEXT`。

- UI 只能列出/查看当前授权 scope 内的 validated instances/bindings，并只对既有 evaluator 证明合格的
  TENANT/PROFILE OPTIONAL `context.provide/v1` Binding 提供 Disable；Manifest 或作者元数据不能自授权；
- UTF-8 instance ID 仍按 Module API 的 NFC/C0+DEL 合同处理；浏览器 detail carrier 只是无歧义传输编码，
  不改变 ModuleRef/instance identity、artifact digest、execution mode 或 Host authority；
- 操作必须先 dry-run，再请求 short-lived confirmation，并在完整 inert summary 后二次确认。浏览器不生成
  Apply plan、publication、receipt、grant 或 replacement mutation；
- 可信成功后只重新读取 server-owned publication。没有 package upload、path/URL/signature ingress、staging、
  install、activate、bind、grant、Host dispatch、自动升级或通用 module configuration；
- `W6_5_SERVER_OWNED_MODULE_ARTIFACT_INGRESS_NEXT` 是该切片当时的历史下一入口；W6-4 本身不改变
  本文件的离线包作者边界。

### W6-5 server-owned module artifact ingress 对模块作者的当前边界

当前状态为 `W6_5_SERVER_OWNED_MODULE_ARTIFACT_INGRESS_ACCEPTED_DEVELOPMENT_SLICE /
W6_6_SERVER_OWNED_MODULE_UPGRADE_REVIEW_NEXT`。

- 模块作者不能从 Manifest、Index、页面或包内数据主动调用 ingress。只有可信本地 Operator 显式运行默认关闭的
  `module-artifact-ingress --enable-module-artifact-ingress`，并选择 Store-owned current Snapshot 的 exact
  Source/Snapshot/Module/ArtifactDigest；package path 必须由该 Snapshot entry 提供；
- 该入口只接受 unsigned `LOCAL_DIRECTORY + DENY`。调用方不能提供 package path、URL、signature 或 upload；
  `--source-root` 与 `--artifact-root` 只是本次进程的可信路径输入，不成为作者可控制的 durable identity；
- 包仍须通过本文件既有 canonical Manifest、路径、digest、size、文件数和 conformance 限制。通过只表示文件系统
  先完成 digest-addressed、no-replace durable publication，随后唯一 Store 同事务记录 immutable Artifact 与
  append-only Admission；不表示 Installation、Activation、Binding、grant、Review、Apply、Host 或 execution；
- Source、artifact 与 Backup tree 均从固定 root handle 开始并相对 held parent handle 逐组件读取；link/reparse、
  hardlink、跨 filesystem/volume 或 identity/change-time/namespace 漂移失败关闭。artifact root、child 与全部祖先
  必须是可信 owner 的私有、不可被低权限主体替换的 namespace；跨进程 lease 覆盖 stage recovery、发布与
  root 级 256 trees / 512 MiB covered content / 32,768 paths / 16,384 files / 16 MiB path-name bytes 硬预算；
- 新 ingress 的同 digest 目标不存在时，所有目录固定为 `0700`、所有文件固定为 `0600`。既有同 digest 目标只有
  完整 bytes/mode 复验为两种 root-global 闭包之一时可复用：全目录 `0700` / 全文件 `0600`；或全目录
  `0700`，仅 canonical `LOCAL_PROCESS + mcp-stdio/2025-11-25` descriptor 精确绑定的唯一 executable 文件
  为 `0700`，其余文件 `0600`。两者都拒绝 special/setid/sticky 与 group/world 权限。artifact root 跨 Store
  共享时模式判定不得依赖任一单独 Store 的 Installation；后一种只是已有物理兼容态，不授予当前 Store
  Install/Activate/execute authority。W6-5 的 inert 是无 Store authority 且本切片不执行，而非要求
  OS executable bit 不存在；
- 相同 digest 的既有 server-owned 目录只有在完整字节复验后才能复用。commit 结果不明的对象保持有界 inert，
  不得猜测删除，也不能被 Manifest 或作者元数据提升为 authority；只有 durable exact Admission 可恢复历史 selector，
  未提交的 stale selector无权采用，后续当前合格 selector仍须全包复验；
- Backup artifact closure 是 Installation 与 ingress 的去重并集；Source 离线且没有 Installation 时仍可恢复同一
  inert Artifact/Admission，恢复不得联网、重读 Source、Install、Activate 或 execute；
- 当前 Store 为 43 tables / 25 explicit indexes / 64 triggers，fingerprint
  `47981b9bf147ec81c6e98382f281db0d5395187a1f090f7ce183c116fba82a0d`，migration 150,301 bytes /
  SHA-256 `6def433a59fa8f4876894572f1610cae499abc5b389ab5930ea697055419ca86`。下一入口仅为
  `W6_6_SERVER_OWNED_MODULE_UPGRADE_REVIEW_NEXT`，W6-5 不创建 Review/Decision。

## 6. 兼容夹具与测试分级

冻结的原创合成夹具位于 `sdk/moduleapi/testdata/compat/v1/`：

- `declarative-role`：最小声明式 Role 包；
- `go-action-provider`：从独立临时 Go module 离线编译的 `ActionProviderV1` 合同实现。

`v1` 夹具发布后保持字节稳定；破坏性合同另建新版本。`examples/` 是可演进教程，不是
兼容夹具。测试证据分为“合成包验证 → 外部 SDK 编译 → Host 集成 → 真实第三方互操作”；
前三级不能作为真实互操作或部署依据。W2-R1 的 production pre-POST failure chain、hermetic
RoundTripper 与 Backup/Restore 属于 Host 集成/分层证据，不等于真实公网 Provider 互操作。

夹具必须是项目原创或明确可再分发内容，不复制第三方源码、二进制、payload、商标或
Logo。更深的 Runtime、Store、Gateway 与 UNKNOWN 合同以
[`CORE_RUNTIME_V1`](specs/CORE_RUNTIME_V1.md) 和
[`CURRENT_STORE_V1`](specs/CURRENT_STORE_V1.md) 为准。
