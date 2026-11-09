# FreeAgent 上下文压缩可靠性修复计划与九厂商官方研究

创建：2026-11-08；实施方案修订：2026-11-09（Asia/Shanghai）。
状态：实施中。P0、第一阶段代码与回归已完成；第一阶段以 v8.7.1-dev.1 打包、验收和更新 Hytron，第二阶段随后继续。程序部署结果单独核对。

正文用于实施；九厂商研究、型号差异和历史实测分别保存在附录 A、B、C。**有冲突时，以对应模型厂商官方资料为准；官方资料自身冲突保留为 UNKNOWN。** 中转实测和本机切换器只提供兼容性观察，不覆盖官方能力。

## 1. 结论与交付边界

Hytron 一轮 Telegram 请求约 254 秒后失败：10 次摘要调用耗时约 215 秒，7 次截断，多次 `output_tokens=reasoning_tokens=4096` 且正文为空。辅助思考未设置，沿用 DeepSeek 默认；十次调用也包含正常历史分块。

压缩配置不合适、同额度恢复无效、失败直接终止父 Run，是主要链路；读取旧发行资料而未使用设置工具是另一问题。

交付分为两阶段：

1. **先修线上可靠性并更新 Hytron**：配置止血、预算/恢复、跨轮抑制、安全降级和必要提示。
2. **再做发布预设与 Provider 适配**：只实现发布型号及 GPT 6.1 Sol、Opus 5.5，旧型号留作研究。

保持轻内核：复用 Config、Run、Attempt、Recovery、`fa.meta`、上下文事件和控制面；不增加数据库表或列，不增加独立预算、能力发现或恢复服务。不恢复总步数、整轮总时长限制；AffectWeave 的人物、分析、主动对话和表达策略仍属于扩展。

## 2. 已核实的实现与线上状态

2026-11-09 10:45（Asia/Shanghai）修复前只读检查：Hytron Core 与渠道均运行，检查时无活跃 Run；数据目录实际为 `/var/lib/freeagent/data/Harness-pg-v1`。下表保留该基线；P0 后辅助思考已显式关闭。

| 核查项 | 结果与实施含义 |
| --- | --- |
| 辅助思考 | [service.go](../internal/harness/service.go) 读取 `Reasoning.Auxiliary`；线上 `deepseek-flash` 已有 `control=deepseek`，辅助项为空，现有代码可关闭 |
| 有效输出额度 | 全局 4096，模型未声明 `capabilities.output_tokens`，摘要上限 4096 字节。[requestLimits](../internal/harness/model_selection.go) 取运行额度与非零模型上限的较小值；解耦不会自动增大额度 |
| AffectWeave | 安装 `1.6.0-dev.2`，`analysis_enabled=false`；辅助设置也影响以后开启的扩展分析 |
| Anthropic 请求 | [ModelRequest](../internal/harness/model.go) 与 [Anthropic 编码](../internal/harness/provider_anthropic.go) 均无 `temperature/top_p/top_k`。压缩无 tools；普通请求最多用 `tool_choice=auto`。当前 API 版本头为 `2023-06-01`，adaptive/effort 尚未实现 |
| Attempt 存储 | [store.go](../internal/harness/store.go) 已有请求/响应 JSON，能存 Reasoning、额度、Usage、耗时、失败与 `requested_reasoning`。返回档位可加为 JSON 可选字段，无需加列 |
| 通用恢复 | [model_recovery.go](../internal/harness/model_recovery.go) 会加精简提示并禁用工具，但思考强度和额度不变，仍可能空正文截断 |
| 旧部署资料 | 工作区确有 `AffectWeave-1.0.0-dev.1-linux-amd64` 目录及同名 `.tar.gz`；故障轮实际读取过其中 `extension.json`、`README.md` |

本轮检查记录保存在忽略目录 `output/context-plan-review-20261109/`；不复制凭据或私人聊天正文到计划。

## 3. 不等待代码的 Hytron 配置止血

已执行：独立 PostgreSQL 与合成历史触发两次真实压缩，随后一次主回答接续成功，总计约 9.7 秒；四项约束及 UNKNOWN 全部保留。辅助请求明确关闭，后续主回答仍报告思考用量。验证使用 50% 阈值与 40000 字节窗口加速触发，输出额度沿用线上 4096，并非生产历史回放。

通过既有控制面已将线上辅助思考设为 disabled，核对仅该项变化；主回答、额度与 `analysis_enabled=false` 保留，未重启生产、未向生产 TG 发送测试消息。配置已备份，证据在忽略目录 `output/compaction-reliability-20261109/`。以下保留执行步骤作为验收依据。

1. 核对活跃 Run、模型配置与 `analysis_enabled=false`，保存可恢复快照，不打断任务。
2. 独立验证目录/Owner/会话使用同一 DeepSeek 端点、现有额度和合成长历史，设 `Reasoning.Auxiliary.mode=disabled`，验证**真实 Core 压缩及下一轮接续**。不使用生产私聊或向其发送消息。
3. 检查 Attempt 的关闭编码、完整摘要、关键约束/UNKNOWN、用量/次数/耗时及主回答选择。缺失用量记 UNKNOWN，不以请求值冒充实际档位。
4. 通过后，用现有设置/`model.set_profile` 关闭线上辅助思考，保留主回答与额度，确认后续调度生效并更新私有部署记录。
5. 失败先调查，保留原配置和证据；不自动加额度。止血验证不代替修复后的代码验收。

## 4. 压缩预算与模型配置

### 4.1 成品大小与模型额度分开

- `TaskSummaryBytes` 只限制最终保存的 UTF-8 摘要。修正 [summaryRequest](../internal/harness/context_compaction.go) 中的 `min(limits.MaxOutputTokens, byteLimit)`，实际请求使用 `requestLimits()` 得到的有效输出额度；返回后独立检查摘要字节数。
- 有效额度仍为 4096 时，解耦后不变；需结合辅助配置、无效重试拦截和跨轮抑制。加额度走已有 GUI/CLI/AI 设置与确认。
- 上下文窗口 token、Core 请求字节保护、输出 token 与摘要字节不能混算。先预留思考和正文额度，再计算可装配输入；回执已计入 output 的 reasoning 不重复相加。4096 不是所有模型的通用安全值。
- `length/MAX_TOKENS/incomplete` 的部分正文不替换有效摘要；HTTP 200、出现正文或参数被接受均不等于摘要完成。用量缺失记 UNKNOWN，不按可见思考字符数补造 token。

输出额度可能在正文出现前被思考用尽，参见 [OpenAI](https://developers.openai.com/api/docs/guides/reasoning)、[Claude](https://platform.claude.com/docs/en/build-with-claude/thinking-steering-and-cost)、[Gemini](https://aistudio.google.com/docs/thinking?generate-content=true)。Claude 可省略可见思考块，Meta 不返回私有推理正文；是否显示思考与实际 usage 分开。

### 4.2 辅助思考选择

复用设置 → 模型与 Key → 模型能力中的主回答/辅助思考及 `model.set_profile`，不新增专用选择器。

| 已核实的模型能力 | 辅助建议 |
| --- | --- |
| 默认不思考，或无可调机制 | 保持原模式；有合法关闭参数才发送，不把未知能力当作不思考 |
| 默认思考且允许关闭 | 建议显式关闭，例如 Hytron 的 DeepSeek；主回答独立 |
| 必须思考且可调档 | 建议最低已核实档，例如 GPT Sol/Opus 5.5 的 low、Muse 的 minimal；低档仍可能用尽额度 |
| 必须思考且不可调，或协议未知 | 保持已声明语义，在批准额度内执行；不猜参数、不自动换模型 |

“服务商默认”表示不发送该控制字段，绝不暗中转换为关闭。建议只在选择/保存模型时生成，已有配置正常确认后修改；保存 Key、启动和切页不自动探测或覆盖。主回答和辅助选择冻结到实际 Run/Attempt。

### 4.3 官方预设与用户声明分开（第二阶段）

采用三种明确状态，避免把过时的内置表变成封闭白名单：

| 状态 | 处理 |
| --- | --- |
| 已有官方依据且适配器能表达 | 作为预设提供，保存来源与核对日期 |
| 对具体型号/协议有官方明确不支持的依据 | 提交前拦截，并显示原因；例如 Opus 5.5 关闭、Muse none、GPT 6.1 Sol none/minimal |
| 内置资料未覆盖或尚未核实 | 允许用户在已有模型配置中显式声明，GUI/CLI/AI 一致标为“未经官方核对”；通过正常保存与确认后发送 |

仅“不在旧枚举里”不构成拒绝理由。GPT 6.1 Sol 官方预设仍是 `low/medium/high/xhigh/max`、默认 `medium`；不把 CPA 的 `ultra` 纳入官方推荐，但可以按用户声明的未核实端点行为记录。第三方成功不改变官方标签；官方明确禁用项不能仅靠这个标签绕过。

用户声明须满足类型、长度、互斥及实际编码约束，不开放任意 JSON 或未实现协议。未知值只由用户主动配置；官方限制改变时据新来源修正规则。

## 5. 恢复规则与跨轮失败抑制

### 5.1 通用恢复负责可重试错误

- 在通用恢复中拦截“**输出截断且正文为空时，以相同思考配置和额度自动重发**”，同时覆盖主回答与摘要。检查响应、结束原因和 usage，不把 transport/UNKNOWN 当作截断。
- 有 reasoning 用量才把原因写成思考消耗；字段缺失时只说明“输出额度耗尽，未产生完整正文，思考用量未知”。不靠改一条缩短提示绕过这个判断。
- 非空部分输出的既有安全恢复、429、暂时不可用等继续使用 `ModelRetries`、Recovery 预约和 `Retry-After`。保留认证、权限、配额、取消、WebSearch 等已有不可重试边界；结果未知沿用既有通用恢复规则，不转成内容修复或自动工具重放。
- 每次真正的 Provider 派发保存独立 Attempt，统计来自实际派发记录，不能把一个外层 `generate()` 当作一次请求。

### 5.2 压缩只额外负责内容修复

整次逻辑压缩最多一次**内容修复**，用于已完整结束但摘要为空、或超过成品字节上限的响应；对同一源内容明确压缩目标。所有源块共享这一次机会，正常分块数量不受它限制。

内容修复后遇到 429，仍按 `ModelRetries` 等待重试；网络恢复不刷新内容修复机会。两者分别计数，修复请求也受空正文截断拦截。

复用 Recovery/`fa.meta` 记录关联和消耗，重连/重复入口/重启不刷新次数；用户新发起的手动压缩才算新操作。不重写主回答的其它恢复规则。

### 5.3 同会话跨轮抑制配置性失败

一次摘要已明确因 `output_limit` 且空正文失败后，在**同一会话、相同有效压缩配置**下，后续轮次跳过预防性自动压缩，继续用上一份有效摘要和合法原上下文。429、网络中断、摘要提交 UNKNOWN、数据库失败不创建这种标记。

- `fa.meta` 复用一条会话记录关联失败 Attempt、原因和配置指纹，重启后仍有效；不保存提示词或 Key，不另建对象。
- 指纹覆盖实际端点绑定、模型及协议、辅助思考选择、有效输出额度、摘要成品上限和摘要策略版本；无关的标题、主题、主回答思考调整不清除标记。相关配置变更，或用户手动压缩成功后解除；手动压缩失败仍保留。
- 同配置失败只提示一次，TG 复用发送去重；GUI/CLI 保留可查询状态与失败证据，不每轮刷屏。
- **达到硬预算不能直接放行，也不机械重试已知无效的请求。** 重新校验是否有有效配置变化或明确的人工重试；同配置仍无改善条件时明确停止，保留进度并给出设置入口。硬预算的阻塞结果仍须告知用户，不能被“提示一次”吞掉。
- 只有当前轮内容或压缩收益问题时，仍可只抑制本轮；不要把所有暂时失败升级为会话长期禁用。跨轮标记必须绑定上面的明确证据。

TG 对支持关闭的模型可以提示：“压缩思考耗尽了输出额度，已暂停同配置的预防性压缩。可回复‘把压缩思考关掉’。”后续走现有 `control_read_state → control_plan_change → 确认`；这句话不是自动授权。不可关闭的模型改为建议调低辅助强度或调整额度。上下文已无法容纳设置请求时，给出 GUI/CLI 的直接设置入口，不能为提案发送超硬预算请求。

## 6. 父 Run 终态与协议连续性

当前 `Store.failModel()` 会直接让非 Supplemental 错误终止 Run。按已持久化的 `Purpose=compaction` 分开记录失败与裁决父 Run；不能只在上层忽略错误，也不能伪装成 Supplemental 请求。

| 场景 | 处理 |
| --- | --- |
| 预防性压缩明确失败，重新装配的原请求仍在硬预算内 | 保留有效摘要和失败 Attempt，继续主回答；按第 5 节决定本轮或跨轮抑制 |
| 压缩完整但实际上下文没有缩小 | 保留旧摘要，按同样硬预算规则继续或停止；本轮不立即反复压缩 |
| 当前或后续工具返回使请求超硬预算 | 不发送超预算请求；已知同配置失败不重发，清楚结束并给出调整入口 |
| 取消、授权/配置变化、存储错误、摘要提交 UNKNOWN | 不进入普通降级继续；保持原有停止、冲突或 UNKNOWN 语义 |
| 认证、配额等也会使主回答失败 | 明确报错，不继续发送注定失败的主回答 |
| 手动压缩 | 自己的维护 Run 如实成功或失败，不把失败显示为完成 |

降级前重读输入/插话、摘要、权限与运行状态并复核预算，不覆盖取消；复用冻结材料，不重复调用扩展 Hook。全部源块有效后才原子替换摘要；失败不覆盖旧摘要、不删除或裁掉未覆盖历史。

仅完成且来源完整的消息组进入摘要覆盖。人可读摘要输入排除私有思考，但原始记录和未完成工具回合所需的 reasoning items、thinking/signature、thought signature、reasoning_content 原样保留；摘要与模型生成的设置请求均不构成授权。第一阶段回归现有协议连续性，第二阶段验收新增 Provider 的私有块，不把新增协议实现设为线上修复前置。

本轮继续使用 Core 文本摘要。[OpenAI](https://developers.openai.com/api/docs/guides/compaction)、[Anthropic](https://platform.claude.com/docs/en/build-with-claude/compaction)、[xAI](https://docs.x.ai/developers/advanced-api-usage/context-compaction) 的服务端 compaction 仅留作后续显式能力；不叠加两套自动压缩，不跨 Provider 复制不透明状态。

## 7. 状态、记录与部署清理

### 7.1 状态和设置入口

复用 `context_compacting/context_compacted/context_compaction_failed` 及当前活动区，显示阶段、实际调用次数、耗时、失败后继续或停止的结果。GUI/CLI 不新增任务面板；TG 适配同一事实，避免只给 failed/UNKNOWN 的笼统描述。跨轮跳过状态按第 5.3 节只提示一次。

设置工具说明明确先 `control_read_state`、再按返回能力 `control_plan_change`，提案继续确认。当前运行时状态优先于旧文件和历史摘要；不硬编码 AffectWeave 名字，也不通过删除历史解决选错工具。提示效果需要真实模型验收。

实施发现原整份模型更新需要 Key 引用，而模型只应读取有界状态。现有 `model.set_profile` 增加仅含 `id` 与 `auxiliary_reasoning` 的专用形式；其它参数不得混入，保留 Key、主回答及其它能力。控制状态返回思考声明，真实 DeepSeek 已验证“读取 → 提案”，未确认不应用；没有新增控制子系统。

### 7.2 Attempt 中的证据

请求档位与额度写入现有 `request_json`，响应的实际 usage、finish、失败原因、延迟及请求档位写入 `response_json`。`requested_reasoning` 表示请求值，不能冒充服务端独立回报。

返回档位可加为已有响应 JSON 的有限、脱敏可选字段；缺失时留空，不能从请求值推断。如果必须加列才可实现，则本次删去该回显要求。不得保存整份原始 HTTP 响应或秘密。

### 7.3 处理工作区中的旧发行资料

部署暂存、解包与归档放在 Bot 工作区外。第一阶段核实旧包不是当前安装目标、不是符号链接且无任务占用后，归档**已确认的 AffectWeave 1.0 目录及压缩包**，记录原路径和新位置以便恢复。

不批量清理用户文件、对话、附件或安装目标；部署验收检查临时资料残留和当前控制面版本。归档仅减少误导，Full/host 仍可访问工作区外。

## 8. 两阶段实施与发布

### 8.1 第一阶段：线上可靠性，不等待 Provider

| 顺序 | 工作与退出条件 | 主要位置 |
| --- | --- | --- |
| P0 | 执行第 3 节隔离真实验证；通过后配置线上辅助关闭并记录结果 | 现有模型设置、独立验证会话、私有部署记录 |
| A | 复现空正文截断、恢复叠加、父 Run 提前失败、预算耦合和跨轮重复等待 | 现有 reasoning/context/recovery 测试 |
| B | 通用恢复拦住无效重发；内容修复单独一次；429 保留既有恢复；解耦输出额度；安全降级；跨轮抑制 | `model_recovery.go`、`context_fragments.go`、`context_compaction.go`、`context_prepare.go`、`service.go`、`store.go` |
| D1 | 状态与一次性提示、辅助设置生效说明、设置工具指引；Attempt 沿用现有 JSON | 当前活动区、CLI/渠道映射、Provider 设置、`control_tools.go` |
| E1 | 第 9 节第一阶段验收通过后打包并更新 Hytron；归档旧工作区发行资料，核对 TG/Web 接续 | Windows 与 Hytron 隔离回归、发布与部署流程 |

第一阶段完成即发布部署，不等 C。P0 可在代码前完成，但不能代替 A/B/D1/E1；上线前按现有流程核对活跃任务与备份，保留真实失败记录。

### 8.2 第二阶段：在售预设与必要 Provider 适配

第一阶段交付后立即继续，原 C 与新增协议的 D2/E2 独立验收：

- **只冻结发布清单。** 候选为 GPT 6.1 Sol、Sonnet 4.6/Opus 5.5、Gemini 3.8 Flash、grok-4.7、GLM 5.3、Kimi K3.1 Pro、MiniMax M3.1 Flash、Muse Spark 1.3、DeepSeek Flash；发布前核实准确 ID、协议和可用范围。附录其它型号不自动进入实现。
- 新连接退出 GPT-5、Gemini 2.5 Flash、GLM 4.5、Kimi K2.5、MiniMax M2.5 的旧推荐，避免使用映射未核实的 `grok-4` 短别名。已有用户配置不静默改名或切换，允许显式重新选择；不为退役型号新增兼容代码或测试矩阵。
- 在 `keys.go/reasoning.go` 与现有适配器补开关/档位/默认、用户声明、Anthropic adaptive/effort 及发布型号连续性；GUI/CLI/AI 一致，共用编码而不跨厂商推断能力。
- 原生 Anthropic 压缩继续不发送采样参数、不强制工具。官方限制与 API 版本相关：Opus 5.5 拒绝强制工具调用和非默认采样值；官方 `2026-10-21` API 进一步拒绝携带任何采样值，不能将所有 adaptive 型号一概而论。按发布使用的具体版本测试，不能为了摘要“稳定”加入 `temperature=0`。[官方兼容限制](https://platform.claude.com/docs/en/build-with-claude/thinking#limits-and-feature-compatibility)
- 完成第 9 节第二阶段项，真实端点不可用单列 NOT_RUN/阻塞，不计 Core 失败，也不声称已原生验收。

五个旧预设已弃用或进入下线流程，Kimi/MiniMax/Gemini 已影响新接入。第二阶段目标为 **2026-11-18** MiniMax 通知下线前更新推荐、**2026-12-01** Gemini 自动路由变化前完成替换核对；完整适配若阻塞，先发布移除失效推荐的最小修正，列明未支持项。MiniMax 日期冲突保留在附录 A。

## 9. 验收清单与阶段归属

保留原验收编号便于跟踪。**第一阶段：1–12、15；第二阶段：13、14、16、17。** 新阶段不增加对退役型号的实现义务。每项记录实际执行结果，不用源码检查、序列化样例或中转成功替代原生端点验收。

1. **第一阶段**：模拟 `reasoning_tokens=output_tokens=4096`、正文为空、finish=length；压缩与主回答均保留失败证据，不按相同额度和思考设置自动重发。缺 reasoning usage 的同类响应不误报思考用量，非空输出的现有安全恢复不退化。
2. **第一阶段**：主回答显式思考、辅助关闭；仅辅助请求编码关闭参数，DeepSeek Chat/Responses 分别使用现有合法字段。“服务商默认”仍是不发送，不猜未知协议。
3. **第一阶段**：多块摘要、空/超长内容修复、429/暂时不可用组合；整次压缩至多一次内容修复，内容修复后仍有独立的既有限流恢复与 Retry-After，网络恢复不刷新内容修复次数；实际调用数等于 Attempt 派发数。
4. **第一阶段**：正常分块不受修复次数误限；原始历史、当前已支持协议的工具与结果配对、UNKNOWN 事实完整保留。
5. **第一阶段**：80% 预防阈值下失败且原请求合法，主 Run 正常完成。连续三轮及 Owner 重启后，同会话同配置不再自动尝试，提示仅一次；其它会话不受影响。相关配置修改/手动压缩成功解除标记，无关设置修改不解除；429 不持久抑制。另测完整摘要无实际缩减，仅本轮不反复压缩。
6. **第一阶段**：跳过预防压缩后仍逐次检查硬预算；硬预算已满时不超额发送、不静默丢历史、不重跑已知无效请求，准确告知调整/手动压缩入口。
7. **第一阶段**：压缩期间取消、插话、设置变更、Owner 重启；不复活取消任务、不漏插话、不重复有状态扩展 Hook、不重放工具，也不刷新内容修复与通用恢复的已耗次数。
8. **第一阶段**：摘要写入故障、提交 UNKNOWN 保持旧摘要或按证据核查，不进入普通降级，也不建立配置性失败抑制标记。
9. **第一阶段**：手动压缩失败准确结束维护 Run；主回答失败、工具 UNKNOWN、扩展分析按原规则运行。确认辅助配置的共用范围，无新增 AffectWeave 核心职责。
10. **第一阶段**：合成中文长历史与已知待办/UNKNOWN，以真实 DeepSeek 对照 Provider 默认与辅助关闭，记录请求数、用量、延迟、完整摘要和下一轮可用性；默认失败如实记录，不用生产私聊作公开样本。P0 已有符合该条件的证据可复用，代码修改后重测受影响路径。
11. **第一阶段**：设置请求经控制工具生成待确认提案；TG 回复“把压缩思考关掉”不会直接执行或开启主动对话。核对旧发行资料已归档，模型读到当前控制面状态；若请求已超硬预算，提供直接设置入口。
12. **第一阶段**：Windows、Hytron Debian 12 相关 Go/PG、竞态及受影响 UI 逻辑回归；实际 TG/Web 接续与包启动。未实际运行的项目标 NOT_RUN，部署前后核对任务、配置和用户数据。
13. **第二阶段**：仅对发布清单验证默认、开关、档位及编码；Sonnet 默认关闭不与 effort high 混淆，Opus/GPT Sol 不关闭，Muse none 被拒。Anthropic 压缩无采样参数或强制工具；补 adaptive/effort 后仍满足所选 API 版本。Muse contributor 未发布则不额外实现其 max 限制。
14. **第二阶段**：只验证新发布型号/协议的差异，包括实际选择的 Gemini、GLM、Kimi、MiniMax 的控制字段与默认。GLM 4.5、Kimi K2.x、Gemini 2.5 等仅留附录，不再为其新增序列化/拒绝测试；若发布多个入口，逐入口验收。
15. **第一阶段**：成品 4096 字节、有效输出额度大于 4096 时，请求不被压回 4096 token，成品仍受字节限制；有效额度本身为 4096 时不会虚增。请求档位/额度、用量/耗时/失败均持久化到现有 Attempt；返回档位缺失不伪造，无 schema 改动。
16. **第二阶段**：新增 Provider/型号的原生协议私有块与工具接续；不丢失、不当正文渲染或摘要化，不构成持久设置授权。只有完成的来源组被摘要覆盖；未覆盖的型号不声称验证完成。
17. **第二阶段**：失效预设退出新推荐、现有配置不静默替换、明确不支持值被拦截；未核实值可由用户声明并正确标记，非法类型/冲突参数拒绝。第三方成功不扩大官方枚举，官方来源更新后不继续引用过时结论。

第一阶段退出：相同配置不再每轮陷入无效压缩等待，合法时能继续、硬预算时能正确停止，线上辅助关闭产生可用摘要，记录/权限/确认/UNKNOWN 不退化。第二阶段退出：发布清单与实际适配一致，预设可用性及原生验收范围有明确记录；不能因研究表完整而声称所有厂商已实现。

## 10. 交付状态与未决项

- **已完成**：P0 真实隔离验证及线上辅助关闭；第一阶段代码、故障回归、Windows/Debian 12 竞态、前端与真实 Edge 验收；真实 DeepSeek 默认/关闭对照及设置工具提案。详见[第一阶段验收记录](VALIDATION_V8_7_1_2026-11-09.md)。
- **进行中**：第一阶段重复打包、便携验收、Hytron 程序更新与旧资料归档；随后实施第二阶段。尚未执行的部署步骤不标为完成。
- **历史实测**：附录 C 是前次中转请求和隔离 Core 接续的结果，只证明当时请求，不代替本次修复后验收。
- **NOT_RUN**：其它厂商原生真实验收、真实手机和原生 IME/WebView2 手动复查；第二阶段尚未交付。
- **UNKNOWN**：`grok-4` 短别名当前映射、MiniMax M2.5 官方页停用状态冲突、Muse 省略 effort 的固定档位。它们不阻塞第一阶段，第二阶段用明确型号或保留未知标记。
- **阻塞口径**：真实端点/权限/服务不可用按具体型号列出，不能计为 Core 功能失败，也不能跳过后宣称通过。

验证记录：保留早期三个编码基线结果；第一阶段新增故障、竞态、浏览器与真实请求验收已实际执行。部署、第二阶段与真人确认各自记录，不以某一层通过代替其它层。

## 附录 A：九厂商原预设核对（研究资料）

以下“当前预设”指 2026-11-08 至 2026-11-09 核查的源码，并非下一版全部保留的推荐。新版代码范围以第 8.2 节发布清单为准。

“默认”指对应官方端点省略控制字段时的行为；FreeAgent 推荐的辅助配置另列。低档不等于关闭，模型支持思考也不等于每次都会实际消耗思考 token。

| 厂商 / 当前预设模型 | 官方思考默认与可调项 | 建议的压缩处理 | 预设状态 / 来源 |
| --- | --- | --- | --- |
| OpenAI / `gpt-5` | 默认 `medium`；`minimal/low/medium/high`，没有 `none` | 建议辅助 `minimal` 或 `low`，保留思考额度；不能发送关闭 | 模型页已标 Deprecated；下一版本不再作为新连接的默认推荐。[模型](https://developers.openai.com/api/docs/models/gpt-5)、[GPT-5 指南](https://developers.openai.com/api/docs/guides/latest-model/gpt-5) |
| Anthropic / `claude-sonnet-4-6` | 省略 `thinking` 时关闭；可显式 `adaptive`，旧 `enabled+budget_tokens` 已弃用。effort 为 `low/medium/high/max`，默认 `high`；不含 `xhigh` | 压缩使用显式关闭，或保留经核实的默认关闭；不要把 effort 默认 high 误解为默认开启思考 | [思考配置表](https://platform.claude.com/docs/en/build-with-claude/thinking)、[effort](https://platform.claude.com/docs/en/build-with-claude/effort) |
| Gemini / `gemini-2.5-flash` | 默认动态思考。generateContent 使用 `thinkingBudget=-1` 动态、`0` 关闭、`0..24576` 预算；当前 OpenAI 兼容入口使用 `reasoning_effort`，`none` 可关闭，`minimal/low/medium/high` 映射到官方预算 | 当前兼容入口辅助设 `none`；不可把 Gemini 3/4 的等级接口或 2.5 Pro 的不可关闭规则套给它 | 已弃用，API 新接入受限；官方说明 2026-12-01 后自动路由至 3.5 Flash-Lite，控制参数和工具签名规则会变化。[预算](https://aistudio.google.com/docs/thinking?generate-content=true)、[兼容接口](https://aistudio.google.com/docs/openai)、[模型状态](https://aistudio.google.com/docs/models/gemini-2.5-flash) |
| xAI / `grok-4` | 当前 `grok-4` 短别名究竟映射到哪个版本，未取得明确的官方映射。官方已说明 `grok-4-0709` 等旧 ID 退役并重定向；当前 `grok-4.7` 默认 `high`、不可关闭，支持 `low/medium/high/xhigh` | 先选择明确的现行官方模型；对 4.7 建议辅助 `low`，不能从旧别名推断关闭或档位 | 不给未核实别名贴已验证能力。[现行推理](https://docs.x.ai/developers/model-capabilities/text/reasoning)、[旧模型退役](https://docs.x.ai/developers/migration/may-15-retirement) |
| GLM / `glm-4.5` | 默认动态思考；`thinking.type=enabled/disabled`。官方深度思考文档将 effort 控制限定到 5.2 及以上，不能给 4.5 加任意等级 | 辅助显式 `disabled`；保留完整工具回执 | 4.5 页面提示即将下线，须更换新连接推荐。[4.5 模型](https://docs.bigmodel.cn/cn/guide/models/text/glm-4.5)、[思考参数](https://docs.bigmodel.cn/cn/guide/capabilities/thinking) |
| Kimi / `kimi-k2.5` | 历史官方模型卡支持 Thinking / Instant，Instant 使用 `thinking.type=disabled`；没有已核实的分级 effort 契约 | 当前官方 API 已列该模型下线，新连接不再使用它；历史托管/自部署端点按其真实契约另判 | 当前 K3/K3.1 Pro 始终思考，支持 `low/high/max`，默认 `max`，不可用 K2.x 的 `thinking` 参数代替。[官方模型卡](https://huggingface.co/moonshotai/Kimi-K2.5)、[下线列表](https://platform.kimi.com/docs/models)、[思考](https://platform.kimi.com/docs/guide/use-thinking-models)、[强度](https://platform.kimi.com/docs/guide/use-reasoning-effort) |
| MiniMax / `MiniMax-M2.5`，Anthropic 兼容入口 | 当前文档不再给 M2.5 建立新的分级思考契约；不能将 M3.1 参数倒推为 M2.5 能力。官方通知：2026-10-19 起停止向新用户开放、2026-11-18 正式下线；套餐旧名已转至 M3.1 Flash。同页历史表又直接列“已下线”，存在时间状态冲突 | 新连接停止推荐 M2.5；已有账号的当前路由与可用性单列核实。使用现行模型时按下一节分流 | 不把旧模型卡/名称当作当前 API 生效版本。[下线通知](https://platform.minimax.cn/docs/faq/models)、[Anthropic 兼容](https://platform.minimax.cn/docs/api-reference/text-anthropic-api) |
| Muse / `muse-spark-1.3` | 始终带推理，`none` 返回 400；支持 `minimal/low/medium/high/xhigh/max`。省略时由模型决定等级，官方页面未给固定的数值默认档 | 建议辅助 `minimal`，通过保留约束的摘要验收后再采用；不能声称默认 medium 或关闭 | `muse-spark-1.3-contributor` 不支持 max，不能只按名称前缀复制配置。[推理](https://dev.meta.ai/docs/reasoning)、[Spark 模型](https://dev.meta.ai/docs/muse-spark) |
| DeepSeek / `deepseek-flash` | 默认开启、默认 `high`。支持开关，原生档位 `low/high/max`；Chat 用 `thinking.type` 与 `reasoning_effort`，Responses 用 `reasoning.effort`，`none` 关闭 | Hytron 辅助请求建议显式关闭；主回答独立。保留历史错误记录 | 官方明确给出若干兼容档位到原生档位的映射，可按官方处理，不能外推给其它厂商。[思考控制](https://api-docs.deepseek.com/guides/thinking_mode/)、[当前模型名](https://api-docs.deepseek.com/) |

这些模型名称与网页状态可能继续变化。预设更新时必须连同官方来源和核对日期更新；不在 Core 启动时自动抓取网站或探测模型。

## 附录 B：同厂商、型号及协议差异（研究资料）

保留对照用于解释能力差异，不要求为每个历史型号增加实现或验收。

| 具体模型 / 接口 | 官方事实 | 压缩建议 |
| --- | --- | --- |
| GPT 6.1 Sol，官方 API | `low/medium/high/xhigh/max`；默认 `medium`，不支持 `none/minimal`。工具调用要求 Responses | 辅助 `low`，输出额度包含思考；不添加原生 `ultra`。[官方模型](https://developers.openai.com/api/docs/models/gpt-6.1-sol) |
| GPT-5.1，官方 API | `none/low/medium/high`，默认 `none`；该旧模型已标 Deprecated | 用于说明默认不思考这一类别，不能拿它为 GPT 6.1 Sol 推导关闭能力，也不新推荐这个旧模型。[官方模型](https://developers.openai.com/api/docs/models/gpt-5.1) |
| Claude Opus 5.5，Messages | adaptive 始终启用，不能关闭；`low/medium/high/xhigh/max`，默认 `medium` | 辅助 `low`，明确配置足够的总输出额度。[官方 effort](https://platform.claude.com/docs/en/build-with-claude/effort) |
| Claude Sonnet 4.6，Messages | 默认不思考；effort 默认 high，但支持的四档不含 xhigh | 保持辅助关闭；不要因 UI 展示 high 自动开启思考。[官方配置](https://platform.claude.com/docs/en/build-with-claude/thinking) |
| Gemini 2.5 Flash-Lite / Gemini 2.5 Pro | 前者默认不思考且可设 0；后者默认动态、不能关闭 | 前者辅助保持关闭；后者采用合法的低预算，不能传 0。[官方预算表](https://aistudio.google.com/docs/thinking?generate-content=true) |
| Gemini 3.8 Flash | 默认思考 medium，支持 low/medium/high；generateContent 原生使用 thinkingLevel，兼容入口按官方映射 | 辅助 low，不照搬 2.5 的 thinkingBudget=0。[官方等级](https://aistudio.google.com/docs/thinking)、[兼容映射](https://aistudio.google.com/docs/openai) |
| GLM 5.3 / 5.3-FLASH | 不支持 disabled；API effort 为 low/high/max，默认 max；Coding Plan 别名映射与普通 API 分开 | 辅助 low，不给它套 GLM 4.5 的关闭策略；具体变体以独立官方条目为准。[官方参数](https://docs.bigmodel.cn/cn/guide/capabilities/thinking) |
| Kimi K3 / K3.1 Pro | 始终思考、保留式思考；low/high/max，默认 max；不传 K2.x thinking 控制 | 辅助 low，完整保存需要回传的 assistant 协议数据。[官方思考](https://platform.kimi.com/docs/guide/use-thinking-models) |
| MiniMax-M3，Anthropic 兼容 | 省略 thinking 默认关闭，可用 adaptive 开启、disabled 保持关闭；该模型无已声明的分级 effort 控制 | 与 FreeAgent 当前 MiniMax 协议一致时，辅助明确关闭。[官方 Anthropic 兼容](https://platform.minimax.cn/docs/api-reference/text-anthropic-api) |
| MiniMax-M3，OpenAI 兼容 | 省略 thinking 默认开启，disabled 关闭 | 同一个模型换入口也必须重新核对默认值，辅助显式 disabled。[官方 OpenAI 兼容](https://platform.minimax.cn/docs/api-reference/text-openai-api) |
| MiniMax-M3.1-Flash | 两种入口均强制思考；low/medium/high/xhigh/max，默认 max；disabled/none 被拒绝 | 辅助 low。原生字段分别是 output_config.effort / reasoning_effort；reasoning_split 只控制显示，不是关闭思考。[官方兼容说明](https://platform.minimax.cn/docs/api-reference/text-anthropic-api) |

## 附录 C：历史实测摘要（2026-11-08）

使用用户授权的中转端点及合成中文长历史。完整逐请求数据与脚本保存在本机忽略目录 `output/provider-probe-20261108/`，报告为 `PROVIDER_REASONING_COMPACTION_REPORT_20261108.md`；这里仅保留决策所需结论。

| 实测 | 结果与局限 |
| --- | --- |
| GPT 6.1 Sol、Opus 5.5，4096 token | 直接摘要和当前 Core 两轮自动压缩接续通过；各两次摘要、一次回答，四项约束保留。不能据此认为任意低额度都安全 |
| Opus 5.5，512 token | adaptive high 一次输出 512，其中思考 370，摘要截断并遗漏 UNKNOWN；low 同额度也截断。中转拒绝显式关闭，要求 adaptive；实验脚本支持不等于产品已支持 |
| GPT Responses 边界 | 请求额度 128，回执 397/665 且完整结束；属于端点额度偏差。none 非官方项，档位回显可能有映射；不据此改写官方定义 |
| 早期 GLM、DeepSeek | 1024 输出额度出现 length 和空正文；其它模型可用性错误单列未验收，不计 Core 失败 |
| CPA 目录补测 | GPT 六个客户端值在 4096 额度下均完成，ultra 只证明该端点兼容观察；Claude medium/xhigh/max 完成，low/high 两次断连保持未验收 |

502、断连及没有最终响应的情况记可用性/UNKNOWN，不能标记档位已验证。耗时、可见思考和客户端回显均不能代替真实 usage 或官方定义。官方预设与用户声明按正文第 4.3 节区分，不再使用历史 CPA 表覆盖原生能力。

这些历史测试没有修改生产 Core 或 Hytron 配置；本计划仍待实施。后续分别记录请求额度、用量、请求档位及确实存在的响应回显，不以成功正文掩盖额度偏差。
