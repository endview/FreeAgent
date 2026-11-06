# FreeAgent 扩展开发指南

核对日期：2026-11-07。代码基线：FreeAgent v8.3.0-dev.1 开发候选；下述命令按已实现代码说明。现行协议：`freeagent-ext/1`。

本指南说明现行工具、上下文、状态和持久确认接口。AffectWeave 的已实现增量见第 11 节；验收范围与外部条件不足的项目分开记录。

## 1. 先选最小接入方式

| 需求 | 优先方式 |
| --- | --- |
| 可复用的方法、步骤、提示规则 | Skill；不必启动扩展进程 |
| 向模型提供独立工具 | 现有 MCP，或本文的扩展工具入口 |
| 有界上下文增强、宿主模型调用、显式工作流、自己的持久业务状态 | `freeagent-ext/1` 本地进程 |
| Telegram 等平台收发与身份传递 | 独立渠道协议，见[渠道指南](CHANNELS.md)；不注册成模型 Provider |

扩展不限 Go、Python 或 Node。FreeAgent 不在启动时安装其依赖。启用者负责信任扩展代码，扩展作者负责可执行入口和所需运行时的发行。

Core 负责会话、模型/工具、上下文装配、权限、审批、Run/Attempt 和实际结果。扩展负责一个明确业务。不要在扩展里再建 Provider/Key 管理、聊天循环、自动重试引擎或插件市场。

## 2. 已有开发材料与当前限制

仓库已有 [Extension Kit](../extension-kit/README.md)、[协议](../extension-kit/SPEC.md)、[作者约定](../extension-kit/AUTHORING.md)、[manifest schema](../extension-kit/schema/extension.schema.json)、[Python SDK](../extension-kit/sdk/python/freeagent_ext.py) 和 [Node SDK](../extension-kit/sdk/node/freeagent-ext.mjs)。协议字段最终与当前宿主实现核对，不能单靠 JSON schema 或 SDK 自测判断兼容。

**v8.0.1 已修正工具 SDK 契约：** Python/Node SDK 与工具模板现统一使用 `tool.call`、字符串 `content`、`is_error`。原 v8.0 的 `tools/call`/MCP 内容数组差异已复现后修正，并通过真实 Core/PG→SDK stdio 工具调度回归。工具发现和 SDK 自测仍不能代替该联测。

Python SDK 是小型串行实现，内部 Host 调用同步读写 stdio；不能并发调用多个 `ctx.host.*`，也不要在 handler 返回后留后台任务使用旧 invocation。需要并发的业务先串行化，实际有需求时再完善有限并发和取消联测。

## 3. 十分钟离线工作流

需要当前 CLI 和可执行的 `python`。下面从已解压的 Windows 便携包根目录运行，不初始化产品数据、不连接 PG 或真实 Provider。`ext check` / `ext test` 会执行扩展代码，临时目录不是 OS 沙箱，只对自己信任的源码运行。

```powershell
$fa = (Resolve-Path '.\bin\freeagent.exe').Path
& $fa --version
python --version

$lab = Join-Path ([System.IO.Path]::GetTempPath()) ('freeagent-ext-guide-' + [guid]::NewGuid().ToString('N'))
& $fa ext new --out $lab workflow com.example.guide
$extension = Join-Path $lab 'com.example.guide'
& $fa ext check --path $extension

$fixture = Join-Path $lab 'fixture.json'
[System.IO.File]::WriteAllText($fixture, @'
{
  "workflow": "run",
  "input": {"text": "hello"},
  "expect": {"output": "Replace this generated workflow: hello"}
}
'@, [System.Text.UTF8Encoding]::new($false))
& $fa ext test --path $extension --fixture $fixture --host mock
```

最后应返回 `matched: true` 和 `Replace this generated workflow: hello`。将生成的 `main.py` 中 `workflow.run` 业务替换为自己的实现，同时更新 fixture。`ext new` 不覆盖已有目录。

Linux 使用同一 CLI 参数，将入口换为 `./bin/freeagent`；开发环境若只有 `python3`，在生成的 `extension.json` 中将 `run.command` 改为实际解释器名或绝对路径。不要依赖仅在交互 Shell 中生效的 alias。本文命令本轮在 Windows 实测，Linux 重跑情况须单独记录。

三种模板命令分别为：

```text
freeagent ext new --out <父目录> context com.example.context
freeagent ext new --out <父目录> workflow com.example.workflow
freeagent ext new --out <父目录> tool com.example.tool
```

CLI 生成模板是自包含的最小 stdio 程序。Kit 中的 SDK 模板依赖相对位置的 `sdk/`：发布时把所用 SDK 文件放入扩展包并调整 import，不要只复制 `main.py` 和 manifest 后假定它能独立运行。

## 4. manifest 和目录

一个上下文扩展的最小示例：

```json
{
  "protocol": "freeagent-ext/1",
  "id": "com.example.context",
  "version": "0.1.0",
  "name": "Bounded context",
  "description": "Adds one bounded context block.",
  "run": {"command": "python", "args": ["main.py"]},
  "needs": ["message"],
  "context": {
    "phases": ["turn"],
    "purposes": ["chat"],
    "max_chars": 800,
    "placement": ["tail"]
  },
  "timeout_ms": 2000,
  "required": false
}
```

| 字段 | 当前含义 |
| --- | --- |
| `protocol` / `id` / `version` | 协议固定为 `freeagent-ext/1`；ID 使用含点的稳定名称；握手身份和版本必须匹配 manifest |
| `run` | 直接启动进程，不经过 Shell；相对 cwd 和含路径分隔符的相对可执行文件须留在扩展目录；裸命令由宿主 PATH 定位 |
| `needs` | 可选 `message`、`history:1` 至 `history:64`、`workspace`、`tool_results`、`output`、`status`；只请求需要的输入 |
| `context` | phase 为 `turn` / `step`，purpose 为 `chat` / `child` / `planning` / `synthesis`；placement 为 `tail` / `system`；默认字符预算 2000 |
| `events` | 当前只支持 `turn.finished`、`operation.check` |
| `workflows` | 名称、说明、对象输入 schema、`agent_callable`；需要用户专门触发时显式设为 false |
| `tools` | 是否提供 `tools/list` 与扩展 `tool.call` |
| `settings_schema` | 本地对象 schema，不包含秘密值；对应 Core 中本扩展的 settings |
| `env` | 环境变量名；通过环境引用或用户显式 Key 映射取值，不把秘密写入 manifest |
| `budget` | 本扩展在一个 Run 内的模型调用和输出额度；不能把 0 理解成禁止调用，见第 7 节 |
| `timeout_ms` / `required` | 默认 2000ms，允许 100—60000ms；握手至少给 10 秒。required 失败会阻止本轮，普通可选扩展失败记录后可跳过 |

目录规则：

```text
<data-dir>/extensions/<id>/extension.json       用户扩展
<workspace>/.freeagent/extensions/<id>/...    工作区扩展，须显式信任该工作区
<data-dir>/extensions/<id>/data/              宿主提供的扩展私有文件目录
<data-dir>/logs/ext-<id>.log                  stderr 日志（名称会作安全化处理）
```

当前 `extensions.import` 只复制普通文件，最多 256 个、合计 32 MiB，不接受符号链接；它不是通用依赖安装器。此上限已用独立 Go/PG 扩展实际导入及恢复验证；仍不在启动时下载解释器或安装依赖。

## 5. 传输、握手和生命周期

stdin/stdout 使用 **JSON-RPC 2.0 NDJSON**：每行一个完整 UTF-8 JSON，换行结束并 flush；stdout 不能输出启动横幅或调试日志，诊断写 stderr。当前单帧上限 16 MiB，各方法还有更小的业务限制。

Core 首次使用时启动进程，发送：

```json
{"jsonrpc":"2.0","id":"init-1","method":"initialize","params":{"protocol":"freeagent-ext/1","host_version":"v8.0.0-dev.1","workspace":"C:\\work","data_dir":"C:\\data\\extensions\\com.example.context\\data","settings":{}}}
```

扩展回复相同请求 ID：

```json
{"jsonrpc":"2.0","id":"init-1","result":{"id":"com.example.context","version":"0.1.0","provides":["context"],"events":[],"workflows":[]}}
```

`provides` 只列实际实现的 `context`、`workflow`、`tools`；事件和工作流名称也要与 manifest 对齐。`host_version` 用于诊断，当前兼容检查以协议为准；未来新增必需能力必须明确校验，不能因版本字符串更大就假定支持。

同一宿主会复用配置签名相同且健康的进程；manifest、settings、环境映射或工作区改变可能导致下次使用重建。不得依赖进程内存实现持久连续性，也不要假定一个进程只服务一个会话。

`shutdown` 请求释放资源；`$/cancel` 是带 invocation 的通知，不回复。超时/断管后宿主可以关闭并回收进程，扩展须能从持久结果恢复。停止不等于副作用回滚，不应自行 fork 脱离宿主的常驻任务。

## 6. Core 调用扩展

| 方法 | 输入重点 | 返回重点 |
| --- | --- | --- |
| `context.prepare` | `invocation`、phase、purpose、可选 step、settings、data_dir 和 needs 选出的字段 | `blocks`：稳定 ID、text、placement、label、priority；不是工具执行结果 |
| `turn.finished` | 已结算 status/output，输出最多 64 KiB；run_id、conversation_id 及所选上下文 | 可选 `activity`，只用于显示，不是交付证明 |
| `operation.check` | 有界、脱敏的 kind/target/preview/parameters/items | `decision: allow/ask/deny` 与 reason；纯检查，不修改参数 |
| `workflow.run` | invocation、name、对象 input、settings 与所选上下文 | 字符串 `output`、可选 activity |
| `tools/list` | 发现请求 | tools 列表：name、description、inputSchema、readOnly |
| `tool.call` | invocation、name、对象 arguments | 字符串 `content`、布尔 `is_error`、可选 activity |

例如上下文返回：

```json
{"blocks":[{"id":"format","text":"回答中先列出已确认事实。","placement":"tail","label":"Format","priority":50}]}
```

最多 128 个 block；总量按字符预算裁剪，Core 再核算最终上下文。历史也会被裁剪，不保证提供完整会话；块只追加上下文，不删除 Core 不变量或改写历史。`context.prepare` 按可重复查询设计，不能将每次调用都当成新用户输入。

`operation.check` 只能收紧决策。`allow` 不绕过权限；显式启用的策略扩展返回 `ask` 时，即使 Full 也走现有工具确认。不要把所有普通文件/命令操作改成控制面提案确认。

扩展工具协议**不是完整 MCP 工具协议**。MCP 服务器使用独立 MCP 接入；不能把 MCP 的 `tools/call`、内容数组、`isError` 原样当作这里的宿主契约。

## 7. 扩展调用 Host

每个请求必须携带宿主刚提供且仍有效的 `invocation`，不能自行生成或跨回调保存复用。invocation 是调用授权，不是持久事件 ID。宿主按扩展身份、当前 Run、取消状态和权限验证。

| Host 方法 | 作用和限制 |
| --- | --- |
| `model.generate` | 使用本轮当前模型，省略 model 或用 `current`；不拿 Provider Key。非空 tools 当前不支持；额外调用进入 Core 记录 |
| `model.embed` | 当前模型明确支持 embeddings 才可用；不支持则返回不可用，不私自换服务 |
| `tool.call` | name 与 arguments（也接受 args）；走现有工具与权限边界，不能递归调用其他扩展工具 |
| `task.delegate` | 现有前台协作；每个活跃 Run 一次计划，受父/子预算约束，子任务不能再扩展递归或自行重规划 |
| `activity.report` | 有界进度/说明；不构成文件写入、发送完成或用户已读证据 |

模型请求示例：

```json
{"jsonrpc":"2.0","id":"host-1","method":"model.generate","params":{"invocation":"本次回调实际提供的值","model":"current","messages":[{"role":"user","content":"用一句话概括这些合成内容。"}],"max_tokens":128}}
```

当前 `schema` 参数只会在响应能解析成 JSON 时增加 `json` 字段，**不保证 Provider 受 schema 约束，也不替扩展完成结构校验**。扩展应自行验证候选结果，不合法时返回明确失败，不能写入长期状态。

模型预算按同一 Run/扩展共享，换 hook 不获得新预算；目前该共享计数在 Host 内存中，不是跨 Owner 重启的独立持久额度账本。默认宿主上限最多 128 次；manifest 的正数可进一步缩小。`model_calls: 0` 是未另设上限，`output_tokens: 0` 是未声明扩展输出预算，不是零调用/零输出。缺少真实 usage 时输出按字节保守计数。建议为额外分析显式设置较小正数，并把长调用 timeout 配合好。

Host 每个扩展进程最多同时处理 16 个请求；这不表示小型 SDK 能安全并发发起它们。

| 错误码 | 含义与处理 |
| --- | --- |
| `-32000`，too many concurrent host requests | 本次请求未执行；等在途请求结束后可重试该拒绝请求，不做忙循环 |
| `-32001` | 额度不足，停止额外工作 |
| `-32002` | 已取消，停止并报告已有结果 |
| `-32003` | 权限拒绝或需要工具审批；交回 Core，不自行执行 |
| `-32004` | 宿主能力不可用；显示缺少的前提，不静默换模型/服务 |
| `-32005` | invocation 无效或过期；不能换一个 token 猜测重试 |
| `-32006` | 结果未知，保留 UNKNOWN；禁止无证据重新执行副作用 |

## 8. 安装、启用、配置与会话选择

在已有独立开发数据目录/PG 环境中操作；不要把生产 Bot 数据目录当开发夹。当前没有 `ext install` / `ext enable` 命令，生命周期修改通过 GUI 或现有控制面。

| 操作 | 当前入口 |
| --- | --- |
| 查看/导入 | 设置 → 能力管理 → 扩展；导入绝对目录，默认不启用 |
| CLI 查看可调类型 | `freeagent control catalog` |
| 导入 | `control plan --kind extensions.import --arguments <JSON>`，字段 source；用户操作 |
| 开关/必需/设置/环境引用 | `control plan --kind extensions.set_policy --arguments <JSON>`，字段 id 与 enabled/required/settings/env_from |
| 工作区信任 | 同一 set_policy，字段 trusted_workspace（绝对路径）、trusted；信任范围是该工作区 |
| 重启/移除/清数据 | `extensions.restart` / `extensions.remove` / `extensions.clear_data`，字段 id；破坏性操作先备份 |
| 当前会话/本轮 | 输入器能力选择；全局启用不等于对所有空间授权，不覆盖已有 Run 的能力快照 |
| 日志 | `freeagent ext logs --data-dir <开发数据目录> <id>` |

CLI 示例（`$data` 指已初始化的开发数据目录，沿用第 3 节 `$fa`、`$extension`）：

```powershell
$arguments = @{ source = $extension } | ConvertTo-Json -Compress
& $fa control plan --data-dir $data --kind extensions.import --arguments $arguments
$arguments = @{ id = 'com.example.guide'; enabled = $true } | ConvertTo-Json -Compress
& $fa control plan --data-dir $data --kind extensions.set_policy --arguments $arguments
```

`control plan` 会显示预览并询问用户，**不是保证只读的 dry run**；按提示确认才应用。它可 attach 已有 Owner。真实工作流入口为：

```powershell
& $fa ext run --data-dir $data --path $extension --workflow run --input-file $inputFile --request guide-run-001 --host live --json
```

`$inputFile` 是自己准备的对象 JSON 文件。该入口需要已配置模型；第一次可自动导入并启用扩展，并创建会话/Run，不能当作无副作用检查。当前 `ext run` 的打开路径未实现遇到活跃 Owner 自动 attach；需安排独立开发实例，不能为此停生产 Owner。固定 `--session`、`--request` 才能核对同一工作流请求。

AI 可提出开关和 manifest.model_settings 白名单中的标量普通设置；字段必须存在于 settings_schema，类型校验后仍走确认。任意 settings、env_from、导入/移除/重启继续 user-only。长期人物状态使用 extensions.change_state，具体内容与 revision 先预览再确认；秘密不进入这条接口。

`env` 默认只继承很少的系统项；声明其他变量后才取值。显式 `env_from` 的 `key:<id>` 映射会把该 Key 的秘密值交给进程，这不是通用模型调用的必要条件；通常直接用 Host 模型接口。Core 数据库凭据和 Provider Key 不应交给 AffectWeave。

## 9. 权限、状态和恢复

扩展是用户信任的本机代码。当前 stdio 进程**没有因此获得 AppContainer/Bubblewrap 隔离**；`needs` 是协议输入选择，不是文件系统访问隔离。Host 方法受到 Core 权限检查，扩展直接用操作系统 API 的行为不经过 Host。不能把普通扩展描述成可运行不可信代码的安全沙箱。

持久业务状态用扩展自己的数据域，PG 逻辑库与受限角色可共用实例，但当前没有通用 Host `db.query` 或自动分配 PG 的接口。不要直连 Core 的 `fa.*` 私表；缓存、日志和一次性 fixture 不需要另建数据库。

模型提出的长期人物/关系/Memory/Skill 修改必须先确认。当前协议不会自动拦截扩展自己的写盘，因此仅返回 `activity` 不能实现确认；缺少正式写回接口时保留候选、不应用。Kit 的 `adaptive-memory` 是自动写入本地 JSON 的机制示例，不是 v8 的记忆确认设计，也不是 AffectWeave 或“经验总结”的实现模板。

有状态扩展须用真实输入/操作身份去重、期望版本校验及事务，避免把整个生命周期无限积累为一个 JSON 大对象。超时后先查询原操作结果；查不到且原调用是否结束未知时继续保留 UNKNOWN。取消、模型声明和 `activity` 都不能证明事务回滚。

## 10. 测试、调试和发行

| 检查 | 能证明什么 | 不能证明什么 |
| --- | --- | --- |
| `ext check` | 启动/握手、声明一致、context 或工具发现探测 | 不实际执行工具、工作流和 turn.finished；不证明无副作用 |
| `ext test --host mock` | 当前支持 context 和 workflow 的离线 fixture，工作流可回放模拟 Host 结果 | 不证明真实 Provider、权限/PG、工具运行或投递成功 |
| SDK 自测 | SDK 自身已覆盖行为 | 不证明与当前 Core wire 契约一致 |
| Core 联测 | 实际方法名、输入/结果、权限、取消和 UNKNOWN | 不替代两个平台最终包和真实服务验收 |

fixture 的 `expect` 按列出的顶层字段比较 JSON，不是通用 JSONPath/部分数组匹配。工作流可直接检查 output；上下文回放应另外核对文本、placement 和裁剪结果。省略 expect 时 `matched: true` 不代表业务断言完整。

按需求加入重复请求、丢 ACK、旧 revision、未授权空间、坏 JSON、超限、错误秘密输出、进程退出、EOF/超时及取消用例。工具至少通过一次当前 Core 的真实调度，不能只测 `tools/list`。仅使用合成或脱敏样本。

常见排障：

- 找不到进程：核对启动器实际 PATH 或使用包内/绝对入口，检查最终包是否含运行时。
- 无法解析协议：检查 stdout 是否混入 print、是否漏换行/flush、返回字段是否是当前 Core 的类型。
- `method not found`：检查 SDK/宿主方法名，v8.0 的工具 SDK 需要升级到修正后的版本。
- 2 秒后失败：检查 manifest timeout 和初始化/模型耗时；不把提高超时当作 UNKNOWN 自动重试许可。
- 拒绝启用：核对工作区信任、全局开关、本空间授权、会话选择与 required。
- 参数或状态冲突：重新读取状态并重新预览；不要修改旧确认记录后继续提交。

发布包需要固定源码/依赖、许可证、准确的最低宿主要求、运行时说明、启用/停用/更新/备份/卸载步骤。Windows 与 Debian 12 分别从新目录验证，不要求用户全局安装构建工具；不在启动时 pip/npm install。停用不等于删除数据，更新不覆盖 data。

## 11. v8.3 已实现的可选状态扩展

完整字段与状态表见 [宿主契约](AFFECTWEAVE_HOST_CONTRACT_V8_3.md)。没有新增 Go SDK；其他语言继续使用相同 NDJSON。

| manifest/字段 | 已实现行为 |
| --- | --- |
| `state: true` | initialize 可以声明 state；宿主调用 state.read/preview/apply/result |
| `storage: "postgresql"` | 用户显式 extensions.init_storage 后创建独立库和受限角色；宿主只交付 FREEAGENT_EXTENSION_PG_FILE，未初始化不临时降级 |
| `passive_context: true` | 可为 None 聊天注入已授权人物片段；不能有工具/工作流/模型预算/私有历史 needs，Host 仅允许 activity.report |
| `model_settings` | 列出 settings_schema 中允许 AI 提案的标量字段；未列出和 env_from 保持用户专用 |
| `identity.adopted_inputs` | Core/空间/会话/Run，以及已采用消息“会话ID/序号”、可信参与者和时间；不包含排队未采用输入 |
| `variant` | 现有 CapabilitySelection.extension_variants 中的本轮人物选择，冻结在原 Run |
| `facts` | 从原 Run/消息/渠道 delivery meta 有界补读；生成、部分、UNKNOWN 独立，不重发模型或工具 |

`extensions.change_state` 参数包括 id、scope、action、expected_revision 和声明的具体 payload。Core 覆盖 core_id、限制会话范围，扩展负责角色授权/CAS；前端显示精确 before/after。CLI control plan/confirm 和 AI control_plan_change 使用同一控制面。任意扩展数据库 SQL 不向模型开放。

状态提交前 Core 持久记录派发意图；扩展事务内同时保存变更与原操作结果。回执丢失只调用 state.result 核对；同 ID 不同内容拒绝，不能确认时保留 UNKNOWN。使用 `control reconcile-extension --operation <原ID>` 核对；Run 内原 UNKNOWN 保留，后补已核实 receipt，模型与工具不重放。

```text
freeagent ext state --id cc.endview.affectweave --role maya --space local --data-dir <data>
freeagent ext backup --id cc.endview.affectweave --to <new-paired-backup> --data-dir <data>
freeagent ext verify-backup --from <paired-backup>
freeagent ext restore --from <paired-backup> --data-dir <new-data> --package <matching-extension-folder>
```

GUI 人物详情由 FreeAgent 自己的前端代码按需加载，不运行第三方 JS。独立可选 Go 包在 AffectWeave 仓库构建，用户无需 Go/Python/Node。停用保留数据库；移除程序不会隐式删除独立库。`clear-data` 只清扩展工作目录，不伪称删除独立 PG。持久状态清理使用具体 state 动作和确认。

配套备份只有限暂停扩展写回，Core 派发继续；核对来源、原结果和两份备份。恢复要求原 Owner 已停、目标新目录、同版本扩展；失败保留有效 Core，关闭扩展。不存在通用跨库事务或自动重放平台。

## 12. 本轮验证记录

2026-11-06 使用当前 v8.0.0-dev.1 Windows CLI、Python 3.13，在独立目录实际运行：

- PASS：3 类 ext new 与对应 ext check；生成模板 context/workflow mock；SDK prompt-router/triage-cluster mock。
- PASS：Python SDK 自带 2 项单元测试。
- 历史 FAIL，已修复：v8.0 的 Python 工具模板收到 `-32601`；v8.0.1 修正 Python/Node SDK 与模板后，实际 Core 调度联测通过。
- NOT_RUN：本轮 live PG 工作流、真实 Provider、Linux 包、AffectWeave 自身实现和测试。文中控制面/live 命令已核对源码，未对用户实例执行。

实际源码入口：[CLI](../cmd/freeagent/extensions.go)、[扩展宿主](../internal/harness/extensions.go)、[工具桥接](../internal/harness/extension_tools.go)、[控制面](../internal/harness/control.go)、[GUI API](../internal/chatui/extensions.go)。


## 13. 经实际宿主验证的 Go 最小入口

[go-context](../extension-kit/examples/go-context) 是独立零依赖 Go 程序，使用同一 freeagent-ext/1 NDJSON；不是新 SDK。进程接收 initialize/context.prepare/shutdown，stdout 只写协议，最多四个并发 invocation，取消通知按 invocation 分开处理。它不提供 PG、人物状态、控制提案或交付证明。

在仓库根目录执行：

```powershell
go -C extension-kit/examples/go-context test -race ./...
go -C extension-kit/examples/go-context build -trimpath -ldflags="-s -w" -o go-context.exe .
```

Windows 把该目录的 extension.json 中 `run.command` 改为 `./go-context.exe`；Linux 保持 `./go-context` 并构建对应二进制。将程序和 manifest 放到选定用户扩展目录，再从能力管理明确启用/选择。不要把输出留在源码目录后误提交；正式扩展发行保留许可证。

真实 Core 联测命令：

```powershell
python scripts/test-postgres.py --runtime output/postgresql-runtime/windows/pgsql -- go test -count=1 ./internal/harness -run '^TestGoContextExampleThroughActualCore$'
```

该测试在新数据目录编译当前 Go 样例，经真实 stdio/Host/context.prepare 把标记送入模型请求，再关闭进程；没有真实 Provider 或生产数据库。Windows 和 Hytron Debian 12 的实际宿主测试均已运行通过。样例自身 race 测试通过。Go 版本与主仓库一致，不引入编译代码到 Core 或并行生产人物引擎。
