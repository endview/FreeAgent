# Provider 专项：思考模式、强度与协议接续

日期：2026-11-07，实施更新：2026-11-08。状态：DeepSeek/OpenAI 类型化控制已实现；DeepSeek Chat/Responses 真实请求与工具接续已通过，OpenAI 真实请求尚未运行，详见 [验收记录](VALIDATION_V8_5_2026-11-08.md)。对应 FreeAgent v8.4-A，使用说明见 [配置](CONFIGURATION.md)。

## 1. 归属、代码和数据

本专项属于 **FreeAgent 的模型配置与 Provider 适配层**，服务 GUI、CLI、TG 和任何调用宿主模型的扩展。它不是 TG 配置，也不是 AffectWeave 的人格计算。

| 工作 | 所在位置 | 责任 |
| --- | --- | --- |
| 能力声明、允许值和用户选择 | `internal/harness/config.go`、`model_selection.go`、现有配置控制代码 | Core 校验/冻结用户选择，不推断渠道特有等级 |
| 通用请求/响应、用量与必要协议数据 | `internal/harness/model.go`、Run/Attempt 保存路径 | 持久接续和调用证据；平台字段按来源绑定 |
| Provider 字段映射与 JSON/SSE 解析 | `internal/harness/openai.go`、`responses.go`、`provider_anthropic.go`、`provider_sse.go` | 校验该协议/模型的开关、等级和接续要求 |
| 模型设置与本轮选择 | `internal/chatui/frontend/features/settings/Providers.tsx`、对话输入区；`cmd/freeagent` 和控制入口 | GUI/CLI/AI 统一设置与真实有效状态 |
| 扩展的辅助模型请求 | `internal/harness/extensions.go` | 使用相同 Provider 适配器，检查用途/预算，记录实际参数 |

当前代码在 `internal/harness` 包内；职责划分不要求本次重命名包、创建 Provider 微服务或拆仓库。模型凭据仍在 Core 模型 Key 命名空间；有效选项冻结到原 Run/Attempt。TG 与 AffectWeave 不保存 Provider Key、端点副本或厂商参数表。

AffectWeave 可按宿主授权声明一次调用的用途及资源上限；`thinking`、`reasoning_effort` 等字段映射只在本适配层。AffectWeave 自己的情绪敏感度、恢复速度、表达强度都不是模型思考等级。

## 2. v8.4-A：Provider 思考模式与强度

### 2.1 以具体能力表达，不按品牌写死

设置分为三件事：**是否支持思考控制、思考模式、思考强度**。`继承/Provider 默认` 与 `关闭` 必须不同；`低强度` 也不等于关闭。

每个模型配置保存明确的控制类型和可用值，例如不支持、开关、等级、预算、自动/自适应；实际选项由 Provider＋协议＋模型声明决定。未核实显示未知，不把 `/models` 返回的名称列表当完整能力认证。

核心保存用户意图及有效选择；适配器负责字段映射和约束。禁止向所有 OpenAI-compatible 服务一律添加 `reasoning_effort`，也不以模型名字含某个字符串就自动开启参数。

### 2.2 已核对的字段与实施优先级

2026-11-07 已实际读取官方文档，需纠正“DeepSeek 只有开关”这一过度简化：当前 DeepSeek 文档同时提供开关和强度。

| Provider / 协议 | 请求映射 | 必须核对的差异 |
| --- | --- | --- |
| DeepSeek Chat | `thinking.type = enabled/disabled`；支持时添加 `reasoning_effort` | 当前文档示例使用 deepseek-flash，列出 low/high/max；默认和别名映射按具体服务核验 |
| DeepSeek Responses | `reasoning.effort` | 当前文档以 none 表示关闭，其他可用值按该模型核验；不能照搬 Chat 字段 |
| OpenAI Responses | `reasoning.effort` | 可用等级、默认值及是否允许 none 依具体模型而异 |
| OpenAI Chat | `reasoning_effort` | 仅在该模型支持此协议和任务时使用；不假定所有 GPT 都支持 Chat 工具调用 |
| 其他 OpenAI-compatible 服务 | 明确选择其已验证的控制方式 | 未声明时不发送，不借兼容协议推断私有字段 |
| Anthropic / Gemini 等 | 独立适配其开关、adaptive、effort 或预算语义 | 本批保留类型化接入点；未核对、未实现的选项不提前展示成可用 |

DeepSeek 官方还要求：携带 tools 的后续请求需要按其契约保留并传回相关 `reasoning_content`，不能只加开关而继续丢弃这些数据。OpenAI Responses 已有 `ProviderItems` 基础，应继续保留其 reasoning 项，并验证恢复和工具接续。官方来源见本文第 3 节。

### 2.3 设置位置与生效

- 设置 → Provider → 模型：配置模型支持的控制类型、允许选项和默认选择。
- 输入区模型旁：显示当前会话/本轮的“继承、思考开关、强度”等实际可用选项。只展示该模型明确声明的可用选项；未声明时显示可进入模型能力设置的提示，不把整组入口静默隐藏。
- CLI 与聊天命令经同一 Core 读取和调整；具体命令在实现时与已有 slash/control 路径对齐，文档不能把拟议命令写成已可运行。
- 模型提出修改持久默认值或会话默认值继续确认；用户直接选择的本轮参数冻结在本轮快照中。正在进行的调用不被全局设置变化偷偷改写。
- 模型切换后重新校验，未知或不支持的旧选项不能原样透传；明确显示继承/不支持/需要重新选择。
- 模型替代、压缩、路由、子任务和扩展分析各自记录实际参数与用途。避免额外分析无意继承最昂贵档位；使用明确的辅助调用选择，不暗改用户主回答的设置。

### 2.4 接续、预算和失败处理

1. ModelRequest、Run 快照、请求摘要、Provider metadata 和 GUI 状态同步增加有效设置，记录“请求了什么”；服务端没有回显时，不宣称已观察到服务端实际档位。
2. reasoning、签名/加密项等协议数据与用户可见 Content 分开。JSON 与 SSE 都解析；仅思考阶段不误判为无输出，不把思考 JSON 当回答或工具结果。
3. DeepSeek 工具链保存并按要求回传原始协议内容；跨 Provider/协议不能转用其私有数据。Anthropic 若后续启用思考，先补 thinking/signature 等必要接续，不能沿用当前忽略逻辑。
4. 历史压缩保留有效工具组与协议接续边界；不能先开启思考，再让压缩删除必须回传的内容。无法形成合法请求时明确失败/切换到用户允许的方案，不重放工具补历史。
5. 推理 token 计入相应 Provider 的输出/总量口径；可获得时显示推理用量，否则标未知。预算耗尽而没有可见答案不能记成正常完成。
6. 未知字段、非法等级、协议不兼容、400 错误明确归类。可在派发前拒绝非法配置，不自动删参数重发并宣称原设置成功。
7. 用户取消及时传递；UNKNOWN 按现有模型恢复语义保留。相关记录不得泄露 Key，也不将私有推理内容作为全局 Memory/经验总结默认素材。

## 3. 本专项验收与资料

| 范围 | 必须覆盖 |
| --- | --- |
| 思考字段 | 各协议实际序列化、默认/关闭/等级、非法值、模型切换、JSON/SSE、工具后续、压缩、重启、预算耗尽与取消 |
| 思考接续 | DeepSeek reasoning_content 必要回传、Responses reasoning item、跨协议隔离；不可只用一次问答通过验收 |

同一模型配置在 GUI/CLI/TG 中产生相同的有效请求；未启用任何渠道或 AffectWeave 时同样可用。真实 DeepSeek 与 OpenAI 测试按实际凭据/模型权限分别报告，缺条件写 NOT_RUN。

2026-11-07 已实际读取 [DeepSeek Thinking Mode](https://api-docs.deepseek.com/guides/thinking_mode) 与 [OpenAI Reasoning models](https://developers.openai.com/api/docs/guides/reasoning)。实现时逐模型验证，不能把文档选项等同于每个兼容代理的行为。

实施后更新 `docs/CONFIGURATION.md`、Provider 使用说明与实际验收报告。AffectWeave 的算法方案只引用本契约，不复制厂商参数映射。

## v8.5.1：Web 入口修正

输入区模型选择始终带“模型：”标识；思考继承/Provider 默认值带“思考：”标识。已声明模型显示支持的开关/等级；未声明时显示“思考：未声明”，点击进入模型与 Key 的能力设置。不能因为服务兼容 OpenAI 协议，就替未知模型自动发送思考字段。

本轮选择只进入原 Run/排队记录，不修改模型默认；切换模型后恢复继承并重新校验。DeepSeek Chat 的开关与其声明的等级分别处理，GPT 类只显示该模型声明的等级；没有 none 的模型不显示关闭选项。辅助调用设置继续独立。

浏览器验收使用隔离 Core、真实 PG 和本机合成 Provider，检查控制器可见、设置跳转、模型切换、窄屏以及实际 HTTP 请求字段；不把合成 Provider 称为真实厂商验收。已有生产 deepseek-flash 需要通过标准控制入口补上其官方能力声明，保存前保留备份，默认请求方式保持 Provider 默认。
