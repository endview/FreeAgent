# v8.8 交互、设置保存与上下文恢复修复

日期：2026-11-11。对应 [30 项核查记录](FREEAGENT_V8_8_INTERACTION_SETTINGS_AUDIT_2026-11-09.md)。

最新发布状态：本文件所列修复已推送 GitHub，并于 2026-11-11 更新到 Hytron。部署详情见文末；下文各轮“未部署/未推送”为当轮完成时的历史状态。

本文前半记录首轮修复；文末“第二轮复查”记录后续新发现的问题与独立验证结果，不能用首轮测试替代这些新改动的测试。

本次修复在现有 Core、控制操作日志、Run/Attempt、输入队列和客户端草稿机制上完成；没有引入新的配置平台、审批系统、数据库结构或常驻扩展。

## 使用变化

- 对话输入区的 **上下文** 面板中，修改“摘要目标”后可以点 **保存默认设置**。这会保存 Core 默认值并开启自动压缩；“仅本次手动压缩”只影响这一次。面板分别显示当前默认值与已有摘要生成时的上限，并可跳转到自动压缩、模型窗口和辅助思考设置。
- 摘要上限仍是 **UTF-8 字节**，不是输出 token。允许 1～16,384；关闭自动压缩在全局上下文设置中操作。模型请求的输出额度和摘要保存上限继续分开。
- 上下文限额停止后，可以保存新配置、手动压缩，再继续。旧 Run 的配置与已执行记录不被改写；新配置恢复使用接续 Run。显式模型升级仍保留原 Run 的累计用量。未知结果、未决工具和后台命令仍受原有检查保护。
- Provider 模型窗口、输出额度、思考及能力使用同一份草稿和同一个预览保存动作。旧窗口的过期配置会报冲突，不能静默覆盖新设置。
- TG 提出的产品设置变更仍须管理员确认。启用了公网 Web 时，消息提供原会话和 Run 的 Web 链接；登录后在卡片批准或拒绝，实际结果继续回传原渠道。不会把任意已配对 TG 用户升级为管理员。
- 公网可使用 MCP、Skill、扩展及 Memory 管理入口；没有开放整个 attach API。MCP/扩展进程日志保留本机入口，公网按钮提前说明；Memory 公网接口不接受 CLI 文件导入导出或身份复用。
- 普通浏览器可以预览并确认 Core 所在机器的工作区路径。当前 GUI 的外观可直接在输入框操作：`/view theme dark`、`/view theme light`、`/view theme system`、`/view sidebar 252`、`/view reading 820`、`/view composer 190`、`/view reset`。这些是明确的客户端命令，只修改当前窗口，不调用模型或改变其他客户端。

## 对应问题与实现

“完成”指问题对应代码路径已修复；生产环境升级和真人渠道验收不包含在本次本地结果中。

| 编号 | 修复内容 | 回归依据 |
| --- | --- | --- |
| 01 | 上下文面板增加持久保存及设置跳转 | 浏览器 `summary_save_read_reopen` |
| 02 | 当前默认上限不再被旧摘要上限覆盖；新配置下可压缩并接续受限任务 | `TestSavedSummaryDefaultAndLimitedRecoveryUseNewConfig`；保留原 Run 配置、步骤和执行证据 |
| 03 | 统一模型能力草稿，使用编辑时修订；支持按能力字段修改；显式提交的 vision/reasoning 对象替换该声明 | `model_window_survives_sequential_saves`；`TestControlCatalogSchemasAndCapabilitiesPatchPreserveWindow` |
| 04 | 控制读取按领域/操作查询；每轮工具说明不再包含完整操作目录；扩展状态必须显式读取且有大小限制 | 控制读取与工具说明体积回归；TG 当前模型状态用例 |
| 05 | TG 设置提案提供同会话 Web 确认链接；应用结果回传 | `TestTelegramSettingsProposalLinksToAuthenticatedWebAndReportsApply`，使用模拟平台与已认证 Web 身份 |
| 06 | 公网管理控件与明确 API 白名单一致；扩展 scope 根据当前会话和已采纳消息解析 | 公网 API 会话/CSRF 测试；TG scope 在压缩失败后仍正确 |
| 07 | 无秘密管理操作可提出精确提案，沿用确认机制；Key 仅允许既有条目的名称/备注提案；MCP 不接受模型写入 env 明文 | `TestNonSecretProfileControlsReuseConfirmationAndRejectSecretFields` |
| 08 | 为现有可提案操作补齐参数结构，按需返回；模型状态含窗口、能力、思考、输出及保存的 Key 引用 | 目录 schema 完整性、能力局部更新、TG 当前模型用例 |
| 09 | None 模式保留控制读取和提案，仍不允许文件/命令/网络执行；不支持工具的模型保持普通聊天可用 | `TestNoneModeReadsSettingsAndOnlyProposesPersistentChanges`、`TestNoneModeWithoutProviderToolsStillChats` |
| 10 | 区分默认/旧摘要/本次目标及作用范围；Web Memory 可读取正文和编辑；增加工作区编辑、客户端外观命令；AffectWeave 初值读取已保存设置 | 浏览器设置与外观命令回归；公网 Memory 保存/回读；扩展 scope 用例 |
| 11 | 摘要修复提示含实际 UTF-8 字节数和更保守目标；同配置确定失败跨轮暂停预防性压缩；提示指向真实额度/占用来源 | `TestOversizedSummaryPausesSameConfigurationAcrossTurns` 及既有压缩可靠性测试 |
| 12 | 增加真实 Core+PG 的保存、确认、回读、重开及恢复用例；保留原有轻量字段映射检查 | 新增 Go 回归和 `browser-interaction.cjs`，扩充 Provider 浏览器用例 |
| 13 | 附件、公网安全、渠道、模型、高级策略、基础指令及投递表单绑定编辑时修订；过期保存失败并保留草稿 | `TestSettingsRejectStaleBrowserDrafts`；设置浏览器回归 |
| 14 | 模型选择跟随新快照；只有用户明确修改时才在提交前写回 | `external_model_change_not_overwritten` |
| 15 | A 的接收回执只消费 A 本次内容，保留 B 及后续编辑 | `cross_chat_receipt_preserves_draft`、JS 草稿回归 |
| 16 | env 来源 Provider 可保存不改变来源的元数据 | `TestProviderMetadataSupportsUnchangedEnvAndExplicitEmptyNote` |
| 17 | 显式空备注能清除；遗漏 note 字段仍保留原值 | 同上，包含控制计划层回归 |
| 18 | 外站顶层文档导航可进入登录页；跨站 API/变更仍拒绝 | `TestPublicDocumentLinkAllowedWhileCrossSiteAPIStaysDenied` |
| 19 | 空间名称界面与 Core 均限制 80 字符；失败不清空草稿并返回具体错误 | 字段约束、现有空间测试与浏览器设置检查 |
| 20 | 查到原请求已接收时，同样消费本次附件选择，不带入下一轮 | `lost_submit_receipt_clears_only_accepted_input` |
| 21 | 只有附件时明确提示补充处理要求 | `attachment_only_explanation` |
| 22 | 提交接口等待 Run/消息事务提交才返回 202；附件批次在前端和 Core 双重校验 | `TestSubmitReceiptRequiresDurableInput`、`TestInvalidAttachmentsAreRejectedBeforeSubmitReceipt`、浏览器附件用例 |
| 23 | 已停用/移除的 MCP 从有效能力中剔除，不阻断历史读取 | `TestDisabledMCPDoesNotHideConversation` |
| 24 | 分支创建保留幂等键；首条执行复用统一发送/回执/草稿流程 | 浏览器分支自动发送和丢回执重试；JS 分支请求键持久化 |
| 25 | 分支继承原会话显式能力选择，包括空选择和排除项 | `TestBranchRetainsExplicitCapabilityExclusions` |
| 26 | 新排队消息的配置绑定排除无关监听端口；真实执行配置变化仍拦截，可取消后退回输入框重新核对 | `TestQueueIgnoresListenerChangeAndCancelsExpiredGuidance` 及既有队列校验 |
| 27 | target_ended 插话可真正取消，并可退回输入框 | Core 幂等取消及 `expired_guidance_cancel` |
| 28 | 转引导时明确确认舍弃新任务的思考/能力选项；不修改运行中配置 | `TestQueuedGuidanceExplicitlyDiscardsNewRunOptions` 与既有队列回归；显式思考选择无确认仍拒绝 |
| 29 | 会话切换/新建时清空旧队列视图；异步结果绑定所属会话 | `new_chat_has_no_old_queue` |
| 30 | 区分目标会话加载中与用户明确新建；加载完成前保留输入并禁止提交/上传 | `initial_loading_keeps_target` |

补充修复：控制保存后通知其他页面重新取数，设置页没有选中会话时也能处理 Owner 事件；干净模型表单完整同步服务器回读，防止已清除字段残留；首次新增 MCP 时初始化空配置表；MCP 提案使用完整配置摘要绑定重新输入，保留现有秘密内存清理规则；Memory 列表只含元数据时，展开和编辑会单独读取正文。

## 验证记录

所有测试使用独立 PostgreSQL、合成数据、Echo 或本机模拟 Provider/TG。没有调用用户真实 Provider、向真人发送消息或修改 Hytron 常驻服务。

| 检查 | 结果 |
| --- | --- |
| TypeScript strict、前端 lint、109 项 JS 测试 | PASS |
| Vite 生产构建及资源检查 | PASS；首屏 JS gzip 149,481 字节，低于既有 250,000 字节预算 |
| 全仓库 Go 编译检查 `go test ./... -run '^$'` | PASS，属于编译检查，不能计为执行了全部测试 |
| ChatUI + PG 全套 | PASS；97 个顶层测试，含子测试 130 项，81.777 秒 |
| Harness + PG 全套 | PASS；586 个顶层测试，含子测试 968 项，676.985 秒；28 个顶层条件用例跳过（含子测试 67 项），未计入通过 |
| Windows 定向 `go test -race` | PASS；发送接收、过期表单、Owner 结果投递、None 控制、队列和新配置上下文恢复 |
| Chromium 真实浏览器：interaction / provider / settings | PASS；12 + 27 + 16，共 55 个检查点；包括跨窗口清除/恢复模型思考声明 |
| 模拟 TG → 当前模型设置读取 → 提案 → 已认证 Web 确认 → TG 结果 | PASS；包括未确认不应用、无 TG 管理回调、压缩维护失败不改变参与者默认值 |
| 真实 Provider、真人 TG、公网生产站点、原生 WebView2 | NOT_RUN，本次没有部署或进行这些真实入口的验收 |

首轮 Harness 全套在 600 秒测试超时后退出，不计为通过。最终将测试时限设为 1800 秒，全套在 676.985 秒完成。后续全套发现上下文限额结算会影响显式模型升级，已修复；相关路由、上下文、压缩、接续及新增控制操作测试重跑通过。两处新增测试夹具的问题（缺少模型提案会话、缺少真实 Owner 会初始化的公共审批绑定）已更正，不用放松产品校验来通过测试。投递策略新增返回修订元数据后，浏览器断言分别比较配置与修订，不把元数据写回配置。

最终证据位于忽略的本地 `output/`：

- `interaction-repair-harness-verified.jsonl`、`interaction-repair-chatui-verified.jsonl`：完整 Go 测试逐项记录。
- `interaction-repair-race-verified.log`：Windows 定向竞态回归。
- `interaction-repair-all-browser-final-20261111/core-2u77njk6/browser/`：55 个检查点结果和截图。
- 完整 Harness 套件后只增加了 TG 安全状态投影/秘密过滤，以及对应的定向验证；后续引导选项回归通过。最终产物的前端类型、lint、构建与资源预算重新检查通过。

可复跑命令：

```powershell
python scripts/test-postgres.py --runtime output/postgresql-runtime/windows/pgsql -- C:/Users/endin/.cache/codex-go/1.26.5/go/bin/go.exe test ./internal/harness ./internal/chatui -count=1 -timeout 1800s
python scripts/test-frontend.py --engine output/interaction-repair-20261111/freeagent.exe --runtime output/postgresql-runtime/windows/pgsql --channel chrome --only interaction,provider,settings
```

浏览器运行前需先构建当前 `internal/chatui/dist`，再构建嵌入该产物的 CLI。使用已存在的 `scripts/test-postgres.py` / `scripts/test-frontend.py`，不会接管已运行的 Owner。

## 生效范围与实际限制

- 当前是工作区中的源代码修复和隔离验证。未创建发行标签、推送 GitHub、生成新的完整便携归档或更新 Hytron；线上页面需要部署后才会得到这些改动。
- 确定的摘要内容失败会暂停同配置的**预防性**压缩；到达硬预算仍不会突破模型/请求限额，也不会把超长摘要按字节截断后冒充完整摘要。用户可修改配置后重试，或使用明确的手动压缩入口。
- 旧队列的历史配置摘要不被重新解释为新版绑定。无法继续的旧排队消息可退回输入框，在当前模型/权限下由用户重新发送。
- 控制提案与工具执行审批继续分开：Full 不取消产品设置确认；None 下的控制提案也不产生文件或命令执行权限。秘密、Provider 目的地址变更及需要本机的恢复操作仍使用专用入口。

## 第二轮复查（2026-11-11）

再次沿实际操作链检查后，新增修复以下 14 组问题。沿用现有控制日志、Run/Attempt、`fa.meta` 回执与前端草稿，不增加数据库结构或独立配置系统。

| 编号 | 发现与影响 | 修复与针对性验证 |
| --- | --- | --- |
| 31 | 保存上下文新额度后直接输入“继续”仍被活动任务挡住 | 识别受限 Run 的文本接续，走已有 Continue；`TestRecheckTextContinueAfterSavingContextBudget` 验证接续和重试 |
| 32 | 手动压缩失败成为最新 Run，遮住原来未完成的任务，CLI/渠道 ContinueLatest 选错目标 | Latest 跳过已确定结束的维护任务，保留失败记录；有 pending/dispatched/UNKNOWN 的维护任务仍可见并阻止接续；两个维护回归用例覆盖 |
| 33 | 新建接续 Run 时漏掉显式思考选择 | 接续保存本次 Reasoning；`TestRecheckSuccessorRetainsExplicitReasoning` |
| 34 | 删除 Provider 的预览之后新增引用模型，确认旧预览会额外删除未展示的模型 | 预览绑定当时模型引用和配置快照，变化后要求重新预览；专用 Key 入口、通用提案入口都测试；未引用 Key 的删除仍允许在无关任务运行时操作 |
| 35 | 模型 profile 同时设默认，只更新模型列表版本，旧单模型表单可覆盖它 | 同步绑定并递增受影响的单模型版本；删除模型同样使旧表单失效；`TestRecheckDefaultModelUpdateInvalidatesProfileDraft` |
| 36 | 工作区第一次保存成功，第二次保存仍使用旧版本；地址栏还留着旧会话 | 保存后重取工作区和版本，清除选中会话及 URL 的 chat/run；桌面回调复用同一收尾函数；浏览器连续切换回归 |
| 37 | 附件策略冲突后草稿虽保留，却没有有效的重载入口 | 增加明确放弃草稿并读取当前策略的按钮；冲突→重载→再次保存浏览器回归 |
| 38 | 公网安全设置冲突后同样无法恢复 | 增加重载入口，同步黑白名单文本和版本；浏览器验证保存后的实际值 |
| 39 | 设置已应用但确认响应丢失，界面仍显示旧确认卡片 | 按同一 operation ID 和 digest 读取现有回执，仅 applied/rejected 算对应操作完成；未收到确定回执仍保留错误和卡片，不自动重复变更；两项浏览器用例分别验证 |
| 40 | Memory 已保存但响应丢失，点击重试会另存一条 | 表单保留原请求键；结果未核对前锁定原内容，可重试或明确核对后重新编辑；浏览器验证只产生一条 Memory |
| 41 | 原 Run 就地接续没有新输入键回执，刷新或重试可能创建独立新任务 | 在现有 meta 中保存接续键→Run 的引用，派发前提交；请求状态可查询，重复输入不再执行，同键不同内容拒绝；不重置原 Run 消耗 |
| 42 | 历史任务卡片的操作默认指向“最新 Run”，可能作用到另一任务 | 卡片的继续/结束/审批预览/确认明确传 run_id，服务端校验属于当前会话；只读取 Run 状态，不为定位目标加载全部 Attempt；HTTP 测试覆盖旧卡片和跨会话拒绝 |
| 43 | 投递授权冲突后无重载入口；服务器省略零值/false/空数组时，合并草稿会保留旧值 | 增加读取当前授权，按完整默认值加服务端快照替换草稿；浏览器覆盖冲突后恢复为 0 再保存 |
| 44 | 刷新后 URL 已提前选中会话，草稿稍后载入却未同步输入框，恢复发送被空输入阻挡 | 草稿作用域就绪后同步正文与附件，同时保留启动等待期间的新输入；浏览器覆盖延迟初始化、刷新后草稿恢复和丢失发送回执后的防重放 |

渠道配置操作失败后也重新读取权威列表，保留新增连接草稿并提示，避免一直对旧列表重试。

### 第二轮验证

- Harness 定向集合：159 个顶层测试通过，含子测试 263 项通过；6 项条件用例跳过，234.590 秒。覆盖控制、请求键、接续、恢复、上下文、压缩与模型路由。不是全 Harness 套件重跑。
- ChatUI + PostgreSQL 全套：98 个顶层测试、含子测试 131 项通过，83.200 秒。
- Windows `-race`：本轮 Recheck、提交回执、设置冲突、接续及模型升级通过；调整未引用 Key 删除后，Key/删除/运行中修改定向竞态再次通过。
- TypeScript strict、lint、109 项 JS 测试和 Vite 生产构建通过；最终首屏 JS gzip 149,645 字节，预算 250,000 字节。
- 全仓库 Go 编译检查通过；仅编译检查，不计作全仓库测试通过。
- Chromium 真实浏览器：recheck 8 项、interaction 12 项、provider 27 项、settings 16 项、tool-core 17 项，共 80 个检查点通过。前四组在 `output/interaction-recheck-browser-accepted/core-f8a08fu4/browser/`；工具整组最终重跑在 `output/interaction-recheck-tool-core-accepted/core-b5sttu0f/browser/`。均使用最终产品代码。tool-core 初次在“精确预览”尚未加载时读取占位文案而断言失败，改为等待真实目标文件的预览后重跑整组通过；没有放宽“批准前文件不存在”的检查。
- 真实 Provider、真人 Telegram、Hytron 公网入口、Linux 及原生 WebView2：本轮 NOT_RUN。测试均使用本机独立 Core/PG 和合成或模拟数据。

6 项跳过为：5 项需要真实 Provider 的压缩/恢复验收，以及需要独立 AffectWeave 二进制的被动上下文验收；不计入通过。较大 Harness 集合跑完后仅调整“未引用 Key 的删除锁定”，该路径通过后续普通及竞态测试；ChatUI 全套在该调整之后通过。

复查过程中先复现后修复。较大回归发现过“未引用 Key 删除被运行锁阻拦”的修复回归，已经更正后重测；浏览器组合测试发现测试用投递规则未还原，修正为显式提交空 rules/零间隔；随后完整工具流程暴露了第 44 项启动草稿恢复问题，没有把这些失败计作通过。

本轮证据：`output/interaction-recheck-before.log`、`interaction-recheck-targeted.log`、`interaction-recheck-harness.jsonl`、`interaction-recheck-chatui-verified.jsonl`、`interaction-recheck-race.log`、`interaction-recheck-delete-race.log`、`interaction-recheck-js.log`、`interaction-recheck-compile.log`。浏览器前后对照记录也留在本机忽略的 `output/interaction-recheck-browser-*` 下。

改动仍为本地源代码与嵌入的前端产物。没有提交/推送 GitHub、打包发布或更新 Hytron。上线后仍需要真实入口回归；本地通过不等于线上已生效。

第二轮可复跑的浏览器入口（先构建前端产物和 CLI）：

```powershell
python scripts/test-frontend.py --engine output/interaction-recheck-20261111/freeagent.exe --runtime output/postgresql-runtime/windows/pgsql --channel chrome --only recheck,interaction,provider,settings,tool-core
```

## 第三轮收尾与便携包（2026-11-11）

用户要求继续修复后，确认 01～44 项已经落地，再复现并补齐三个边界：

| 编号 | 问题 | 实际修复 |
| --- | --- | --- |
| 45 | 控制提案已预览，但确认状态请求失败后，“批准”一直禁用且没有重试入口 | 增加“重新读取确认状态”，只重新读取同一提案的确认信息，不重发设置变更；旧提案异步响应仍受卸载/目标变化检查 |
| 46 | 高级策略“重新读取此项”先清除草稿，再发请求；网络失败会丢失输入，错误也没有显示 | 读取成功后才替换值及版本；失败保留草稿并显示错误，读取期间新输入保留，离开页面取消读取；没有新增缓存层 |
| 47 | 受限 Run 可以就地继续，但本次显式模型、思考、能力、权限、Memory 或附件会被冻结的旧快照覆盖 | 有新选项时，仅在原任务结果确定、无未决工具/后台命令的边界创建接续 Run，实际保存这些选项；选项相同则继续原 Run，保留累计记录。更低权限生效，接续不能扩大旧权限；UNKNOWN 不能借此绕过 |

实际验证：

- 修复前 Go 回归复现了四类被忽略的选项（模型、思考、能力、权限）；两项浏览器故障注入复现了确认读取无重试和重载丢草稿。
- 最终 Windows `-race` + 独立 PostgreSQL：Harness 14 个顶层测试（含子测试 29 项）、ChatUI 3 个顶层测试（含子测试 6 项）全部通过。包括六类显式选项、同选项保留原 Run、UNKNOWN 阻止接续及防重复执行。
- TypeScript strict、前端 lint、109 项 JS、Vite 生产构建、前端资源检查通过；首屏 JS gzip 149,725 字节。
- 全仓库 Go 编译检查通过，未把编译检查计为运行所有测试。
- 使用便携包内的 CLI 跑 Chromium：recheck 11、settings 16、provider 27、tool-core 17，共 71 项通过。包含重新读取失败/重试、读取期间继续输入、设置保存和真实 Core 工具审批。
- 便携包使用固定 Go 1.26.5 和经过文件清单验证的 PostgreSQL 15.19；Windows 桌面入口、CLI 和独立渠道程序全部重新构建。产物为本地未提交源码的开发构建，版本标记带 `+dirty`，没有作为正式 GitHub Release 发布。
- `scripts/test-portable.ps1 -SkipDesktop` 对实际 ZIP 解压后通过：含中文/空格/`& !` 的新路径、归档清单、无随包用户数据、首次初始化、拒绝重复初始化、Key 持久化、损坏清单拒绝、显式 CLI 版本、引擎 Web 对话、stdin 关闭后退出，以及解压后二进制 smoke。原生桌面分支明确 NOT_RUN；完整结果在 `output/interaction-followup-portable.log`。
- 真实 Provider、真人 TG、Hytron 公网、Linux、原生 WebView2 本轮 NOT_RUN。没有更新 Hytron。

本轮证据：`output/interaction-followup-options-before.log`、`interaction-followup-options-after.log`、`interaction-followup-race.jsonl`、`interaction-followup-js.log`、`interaction-followup-compile.log`；浏览器最终记录为 `output/interaction-followup-browser-verified/core-wq0yix_g/browser/`。

便携包：`bin/portable/interaction-fixes-20261111-verified/FreeAgent-v8.8.0-dev.1-windows-amd64-portable.zip`。归档中的程序与上述最终浏览器测试一致；包含前两轮及本轮全部产品代码修复。归档不包含用户数据库或凭据。

## GitHub 同步与 Hytron 部署（2026-11-11）

按用户指定顺序，先推送 `endview/FreeAgent` 的 `main`，再从干净提交 [cd6f1a8](https://github.com/endview/FreeAgent/commit/cd6f1a82275f56c5dd6a5423b7187fd5d07ef647) 构建并部署 Linux 便携程序。版本号沿用 `v8.8.0-dev.1`，实际程序构建提交已更新。

Linux 更新前，在 Debian 12 的独立非 root 用户、unit、端口及数据目录完成：

- Core 边界检查通过。
- 定向 `-race`：Harness 22 个顶层/含子测试 37 项，ChatUI 3 个顶层/含子测试 6 项通过。
- 完整 ChatUI：98 个顶层/含子测试 131 项通过。
- Linux 便携包 16 个检查通过，包括首次启动、CLI/Web 接续及防重、实际 Linux 命令沙箱、显式 Full 命令、后台命令停止、重启保留记录、UNKNOWN 与备份恢复。真实 Provider/真人渠道用例为 NOT_RUN。

生产更新前检查无运行中任务、后台命令或未完成投递，完成 Core 在线备份并验证、独立 AffectWeave 备份，以及停机后的 PG 一致副本。新程序预检首次因临时发布目录缺少服务用户的遍历权限而被拒，生产未停止；修正公开程序目录权限后预检通过，私有备份/日志/凭据仍受限。平滑交接使用 `cancel_active=false`，随后原子切换程序路径。

部署后：Core/渠道 active、NRestarts=0，TG connected，doctor 正常；公网 HTTPS 200、CSP 正常，3 个 JS/CSS 入口资源与本次构建逐字节一致。原 5 会话、68 Run、323 消息、204 Attempt 的逐条摘要核对不变；配置、Key、公网登录、渠道授权、Core 身份、代理 socket 目录及 AffectWeave 1.6.0-dev.2 人物业务数据均保留。没有向生产私聊发送测试消息或运行真实模型验收，不把连接健康当作真人收发验收。

旧程序与配套备份保留，回退前仍须保全更新后的新数据；具体私有维护路径记在本机 VPS 运维文档。没有修改 NPM 或其他 VPS 应用。本次未创建 GitHub 标签或 Release；GitHub Actions 全套在部署核验时仍运行中，不计为通过。

本机部署证据：`output/interaction-deploy-20261111/`，含隔离 Linux 测试逐项记录、便携验收、备份准备、切换回执和最终健康结果。用户刷新公网页面即可获取新前端。
