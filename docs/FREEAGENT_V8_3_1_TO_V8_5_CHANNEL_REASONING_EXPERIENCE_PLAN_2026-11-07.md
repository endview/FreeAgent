# FreeAgent 后续实施方案：TG 表达、模型思考、经验总结与主动投递

> 2026-11-08 边界修正（v8.5.1）：完整一条/继承/自然分段、表达默认值与算法属于可选 AffectWeave。普通 Core 不提供这组模式或模型分段动作；只接受已选扩展返回的具体无损部分，并复用通用发送、平台长度分片和回执。相关输入区控件由已安装、启用并选中的扩展设置元数据提供。以下交付职责按这一边界解释，细节见 REPLY_DELIVERY.md。

日期：2026-11-07（Asia/Shanghai），实施更新：2026-11-08。状态：源码与本地回归已完成；实际结果、归档记录入口与真实平台缺条件项见 [本批验收记录](VALIDATION_V8_5_2026-11-08.md)。线上实例没有随本地实现自动更新。

基线为已交付的 FreeAgent `v8.3.0-dev.2`。本文件接续 v8.0.1—v8.3 计划，集中处理用户新增的日常使用问题。版本安排为建议的交付批次，实际发布以对应验收通过为准。

## 0. 职责归属、仓库与文档入口

**总计划只负责排期、依赖和交界验收；各模块的详细规则在自己的专项方案维护。** 同一功能可能需要两层配合，必须分别标明业务决策者和执行者，不能因它最先在 TG 或 AffectWeave 中使用就全部写进那一层。

| 模块 | 所属仓库与运行位置 | 负责什么 | 详细方案 |
| --- | --- | --- | --- |
| TG 渠道 | FreeAgent 仓库；可选 `freeagent-channel`；`internal/channels`、`internal/channelplugin` | Markdown/entities、TG 长度拆分、typing、贴纸 API、reaction API、平台事件/回执 | [TG 专项](TG_CHANNEL_EXPRESSION_PLAN_2026-11-07.md) |
| Provider 适配 | FreeAgent 仓库；现有 `internal/harness` 模型适配文件 | 模型思考开关/等级、请求字段、JSON/SSE、协议接续与用量 | [Provider 专项](PROVIDER_REASONING_CONTROL_PLAN_2026-11-07.md) |
| Core 通用交付 | FreeAgent 仓库；`internal/harness/channel_*`、权限/控制/扩展接口 | 目标授权、逻辑分段、顺序、取消、时机、有限主动触发、逐条证据 | [Core 投递专项](CORE_REPLY_DELIVERY_PLAN_2026-11-07.md) |
| AffectWeave | 独立 affectweave 仓库；`internal/affect`、`internal/storage`、`internal/extension` | 人物/情绪/关系/经历，人物为何想说、如何分段、选择何种表达的倾向 | 该仓库 `docs/AFFECTWEAVE_NEXT_IMPLEMENTATION_PLAN_2026-11-07.md`，特别是人物表达接入部分 |
| 经验总结 | FreeAgent 仓库中的独立可选 Skill 包；`optional-skills/experience-summary/` | 提炼方法、Skill/Memory 产物建议、来源和局限；通过普通 Core Run 执行 | [经验总结专项](EXPERIENCE_SUMMARY_V0_1_IMPLEMENTATION_PLAN_2026-11-07.md) |
| GUI/CLI | FreeAgent 仓库；`internal/chatui/frontend`、`cmd/freeagent` | 各模块的操作、状态与确认入口 | 各专项的设置/入口表；人物页仍是 FreeAgent 自带 UI |

各路径相对于所属仓库。上述划分不要求拆更多进程或服务，也不要求把现有 Provider 文件从 `internal/harness` 搬出去。独立可选能力不等于必须拥有独立进程或仓库。

### 0.1 容易混淆的事项

| 需求 | 决策/业务归属 | 通用支撑与平台执行 |
| --- | --- | --- |
| 两句话分两条发 | 用户/普通模型可指定；人物风格由 AffectWeave 建议 | Core 建逻辑部分与顺序；TG 根据平台限制再拆分 |
| 角色主动关心一次 | AffectWeave 判断动机和拟表达内容 | Core 核对明确授权与触发，TG 发送；无需人物的任务通知直接由 Core 发起 |
| 看懂贴纸、觉得哪张合适 | 普通视觉模型；人物选择偏好可由 AffectWeave 提供 | TG 获取资源/预览，Core 控制可见性，Provider 传递图像请求 |
| 点赞说明什么 | AffectWeave 可生成有来源的解释候选 | TG 报告反应事件，Core 保存事实；不直接改关系 |
| 人物表达强度与模型思考强度 | 前者 AffectWeave，后者 Provider 配置 | 数值与设置分别保存，不能共用一个“强度”字段 |
| 人物经历与总结出来的经验 | 前者 AffectWeave 私有状态，后者经验总结 Skill 的产物 | Core 仅按明确权限与确认执行读写，不自动互相导入 |

### 0.2 数据与文档的唯一维护位置

- Core 保存真实消息/附件/执行/投递、目的地权限、贴纸集合可用范围和主动触发授权；PG 中的既有记录仍是权威来源。
- TG 只保留平台游标、短效活动和可重建缓存；Bot Key 沿用 Core 渠道命名空间。它不复制 Core 权限库。
- Provider 配置与 Key 属于 Core 模型命名空间，协议接续数据随原调用保存；AffectWeave 和 TG 不各存一套。
- AffectWeave 自己的 PG 保存人物状态、表达偏好和提案来源；只引用 Core 提供的作用域/资源/回执，不持有可独立派发的 outbox。
- 经验总结使用现有 Skill 文件和 Core Memory；没有新的经验数据库。前端草稿是编辑状态，不能取代后端的权威配置。
- 四份 FreeAgent 专项分别维护对应业务细节；人物算法与表达动机只在 [AffectWeave 仓库](https://github.com/endview/affectweave)的上述方案维护。公共 Host/渠道契约由 FreeAgent 维护，扩展记录所需能力及实测版本。跨仓只引用契约，不复制维护同一套策略。

FreeAgent v8.5 的交付能力完成，不等于 AffectWeave 的人物主动表达已经接入；后者要在独立仓库完成调用、反馈和实测后分别报告。

## 1. 目标与开工前源码核查结论

目标是让同一 FreeAgent Core 在本地与 Telegram 中都能正常使用：模型思考参数准确，文本格式可读，工作状态可见，贴纸和表情能收能发，扩展可按已授权范围请求分段或主动表达。经验总结保持独立可选能力。

| 项目 | 开工前基线 | 本轮计划 |
| --- | --- | --- |
| 经验总结 0.1 | 只有计划，未交付可用的选择来源、总结、确认发布流程 | 可选 Skill＋总结入口，产物复用 Skill/Memory |
| TG Markdown | `Send/Edit` 发送纯文本，无格式转换，现有测试还要求不带 `parse_mode` | Markdown 解析后转换为 TG 原生富文本 |
| TG 正在输入 | 无 `sendChatAction` 调用链 | 随真实执行状态发送短效活动提示 |
| 长文本拆分 | 已有按长度拆分 | 修复 Unicode/格式边界和顺序；另支持自然段分条表达 |
| 分段表达 | 缺少一个回答对应多个有序表达部分的正式契约 | 复用消息、outbox 与回执，补顺序、取消和部分送达 |
| 主动发送 | 有任务结果回传和授权通知，无通用扩展主动投递入口 | 明确目标、触发来源、有效期和授权的最小入口 |
| 接收贴纸 | `TelegramMessage` 无 sticker 字段；仅识别 photo/document，纯贴纸消息可能直接忽略 | 收到单张贴纸必须成为有效消息，并提供可用预览或准确降级 |
| 查看贴纸包 | 无 `getStickerSet`、目录和作用域选择 | 从收到的贴纸或指定包名/链接读取，按需预览和选择 |
| 发送贴纸 | 无 `sendSticker` | 从已知、获准的贴纸引用发送，并核对逐条回执 |
| 普通 emoji | 文本本来可以包含 Unicode emoji | 改善模型可知的表达方式，不新造 emoji 格式 |
| 消息表情回应 | 无 `setMessageReaction`，无对应输入事件 | 对明确消息添加/更换/移除 Bot 自己的反应 |
| 模型思考配置 | ModelConfig、Chat/Responses 请求没有统一可用的模式/等级设置 | 按 Provider＋协议＋具体模型适配 |
| 思考内容接续 | Responses 已保留 reasoning item；Chat 缺 DeepSeek reasoning_content；Anthropic 忽略 thinking 块 | 保留协议需要的接续数据，和可见回答分开 |

源码位置：[TG API](../internal/channels/telegram.go)、[渠道进程](../internal/channelplugin/telegram.go)、[渠道协议](../internal/channelwire/protocol.go)、[投递](../internal/harness/channel_runtime.go)、[outbox](../internal/harness/channel_outbox.go)、[模型配置](../internal/harness/config.go)、[Chat](../internal/harness/openai.go)、[Responses](../internal/harness/responses.go)、[Anthropic](../internal/harness/provider_anthropic.go)。

额外发现两项应先复现并修正的传输风险：Core 目前按 3500 个 rune 拆分，TG 校验使用 UTF-16 单元，emoji 较多时两者长度不同；outbox 按哈希 key 排序，不代表同一回答的文本顺序。不能直接在这些基础上增加多条表达。

## 2. 交付安排与边界

| 批次 | 交付 | 依赖与退出条件 |
| --- | --- | --- |
| v8.3.1：TG 基础体验 | Markdown、输入状态、Unicode 拆分、确定的消息顺序和 UNKNOWN 处理 | 模拟回归和真实私聊可用，不引入新人物业务 |
| v8.4-A：思考参数 | DeepSeek/OpenAI 思考设置、协议接续、用量、GUI/CLI/AI 一致操作 | 真实请求字段与多轮工具调用通过，不以开关显示代替验收 |
| v8.4-B：TG 表达 | 贴纸收取/预览/包选择/发送，普通 emoji，消息反应 | 三类贴纸发送和规定的识别范围、反应对象/回执实际验证 |
| 经验总结 0.1：独立批次 | 来源选择、草稿、确认、Skill/Memory 发布与管理 | 不依赖 AffectWeave、主动发送或人格演进，可在前述批次间独立交付 |
| v8.5：交付与扩展接入 | 扩展具体部分的有序文本/贴纸交付、受限主动投递；自然分段策略属于 AffectWeave | 逐段证据、授权、取消、部分送达与恢复正确 |

默认顺序：先 v8.3.1，再 v8.4-A/B；经验总结尽早独立收束，然后推进 v8.5。AffectWeave 的情绪、人物参数和人格演进沿用其独立方案，不作为这些 FreeAgent 版本的前置条件。

共同边界：

- Core 负责身份、会话、执行、权限、表达动作授权、持久确认和真实结果；渠道处理平台 API、格式转换和传输，不拥有第二个 Agent。
- 保持单 Core、唯一 PG、当前原生数据连续性。复用 Run、Attempt、消息、附件、outbox 和控制操作，不引入独立任务平台、消息总线或常驻总结服务。
- 全局设置集中管理；本轮模型与表达选择放在输入区。可调整的内容必须有同一 Core 的 GUI、CLI、AI 操作映射。
- 模型提出的持久设置、可用贴纸包范围、主动触发规则和 Skill/Memory 修改继续具体确认。一次回复内已授权的表达无需每个标点、emoji 或贴纸都弹设置确认。
- Lark/QQ 本批不新增真实适配范围；通用字段允许其明确报告不支持，不为三个平台各建业务分支。
- 交付提供可下载归档与操作说明；普通答复不要求用户核对哈希。内部内容绑定与完整性检查继续保留。

## 3. TG Markdown 和正在输入

详细设计、代码位置与本模块验收见[TG Markdown 和正在输入专项](TG_CHANNEL_EXPRESSION_PLAN_2026-11-07.md)。TG 渠道负责渲染、平台拆分与 sendChatAction；Core 提供真实执行状态和有序交付。

## 4. Provider 思考模式与强度

详细设计、代码位置与本模块验收见[Provider 思考模式与强度专项](PROVIDER_REASONING_CONTROL_PLAN_2026-11-07.md)。模型配置与 Provider 适配器负责开关/等级/协议接续，所有入口共用；与人物情绪参数分开。

## 5. TG 贴纸、贴纸包与 emoji

详细设计、代码位置与本模块验收见[TG 贴纸、贴纸包与 emoji专项](TG_CHANNEL_EXPRESSION_PLAN_2026-11-07.md)。TG 负责资源/API/格式，Core 负责资源授权和发送记录；AffectWeave 只能提出人物表达偏好。

## 6. TG 消息表情回应

详细设计、代码位置与本模块验收见[TG 消息表情回应专项](TG_CHANNEL_EXPRESSION_PLAN_2026-11-07.md)。TG 上报/设置平台反应，Core 保存明确目标与事实；是否影响人物关系只由独立 AffectWeave 流程判断。

## 7. Core 分段与统一交付

详细设计、代码位置与本模块验收见[Core 分段与统一交付专项](CORE_REPLY_DELIVERY_PLAN_2026-11-07.md)。表达意图来自用户/模型/扩展；Core 固定逻辑部分与顺序，渠道做平台分片。

## 8. Core 受限主动投递

详细设计、代码位置与本模块验收见[Core 受限主动投递专项](CORE_REPLY_DELIVERY_PLAN_2026-11-07.md)。Core 实施目标授权、时机、频率、取消和回执；AffectWeave 的主动表达动机与内容策略在其仓库维护。

## 9. 经验总结 0.1 独立能力

详细设计、代码位置与本模块验收见[经验总结 0.1 独立能力专项](EXPERIENCE_SUMMARY_V0_1_IMPLEMENTATION_PLAN_2026-11-07.md)。总结方法位于独立可选 Skill；Core 只提供来源、执行、确认和产物管理，不写入人物业务。

## 10. 设置与实现位置

### 10.1 设置映射

| 设置位置 | 内容 | CLI/AI 和生效要求 |
| --- | --- | --- |
| Provider → 模型 | 思考控制类型、支持选项、默认模式/强度 | 同一控制面，持久修改具体确认，新调用采用 |
| 输入区 | 本轮思考选项、回复表达方式、当前能力 | 显示继承来源；当前 Run 快照明确 |
| 渠道 → Telegram → 展示与交互 | 富文本、输入状态、模型反应、贴纸表达、包选择 | 有类型与范围的配置；普通文字不因关闭交互而不可用 |
| 通用交付设置（新增分组）→ 主动投递 | 目标、允许扩展、一次性候选/触发规则、静默与频率 | Core 配置；TG 页仅显示本渠道目标和跳转入口，避免另一份配置 |
| 能力 → Skill；会话轮次 → 总结经验 | 安装/启用 Skill；在轮次下选择来源、生成草稿与发布 | 一次普通 Run；GUI/CLI/TG 负责显式选择来源，产物修改复用 Skill/Memory 控制确认 |
| 能力 → AffectWeave | 人物表达偏好与候选原因；只读显示宿主授权 | 人物偏好保存在扩展；授权跳转 Core 通用交付设置，不复制 TG Key、权限或队列 |

全部可调字段建立“保存位置/范围 → GUI → CLI/AI → 生效时机”清单；只有实际支持的能力才显示可用。常规提示沿用 2 秒收起，关键失败和 UNKNOWN 保留在可展开记录，不用常驻红条遮挡输入。

### 10.2 代码责任

| 位置 | 改动 |
| --- | --- |
| `internal/channels` | TG Markdown/entities、活动、贴纸/集合、反应 API 与平台错误分类 |
| `internal/channelwire` | 最小内容类型、资源引用、渲染/分片、活动、反应及回执字段 |
| `internal/channelplugin` | 平台渲染、文件获取、短效活动；按 Core 指定的派发顺序传输，回报事实 |
| `internal/harness/channel_*` | 目标授权、原始内容、用户选择的逻辑表达部分、outbox 顺序/期限/取消、逐条事实与引用关联 |
| `internal/harness/config.go`、model/provider 文件 | 能力和有效思考参数、协议映射、持久接续、用量与恢复 |
| `internal/chatui/frontend` | 模型选项、TG 设置/贴纸预览、反应与交付状态、经验总结界面 |
| `cmd/freeagent` 与现有控制入口 | 同一能力的 CLI/AI 操作与可读诊断 |
| `optional-skills/experience-summary/` | 提炼步骤、输出约束、来源/局限解释；业务不放默认 Core |
| 独立 affectweave 仓库 | 人物表达动机/偏好/候选、读取真实结果后的解释；不写在本仓库 |

具体依赖、字段与协议小版本在对应阶段冻结；有语义变化的渠道进程与 Core 配套升级，不静默让旧进程误解新内容类型，也不建立长期双协议兼容层。

## 11. 验收与发布

### 11.1 模块验收与交界验证

详细测试分别写入四份专项方案，不在总计划复制维护。交界必须额外验证：

- 未启用 AffectWeave 时，TG 富文本/活动/贴纸/反应、Provider 思考设置和经验总结各自正常工作。
- 未启用渠道时，普通 Core 与 AffectWeave 人物分析正常；需要外部投递的候选明确报告渠道不可用。
- 相同 Provider 设置跨 GUI/CLI/TG 生效；渠道和人物不能私自修改它。
- AffectWeave 提案 → Core 拒绝/受理 → 渠道 partial/UNKNOWN/完成 → 回传事实，每个环节按自己的语义验收。
- 一个模块失败不会伪造另一模块成功；每批完成情况分列 Core、TG、Provider、AffectWeave、经验总结。

### 11.2 实测条件与分级

- Windows 与 Hytron Debian 12 使用独立数据、端口和验证进程完成 Go/PG、渠道 mock、相关前端/CLI 测试；涉及并发和生命周期的改动跑 race。
- TG 真实测试使用明确授权的测试私聊和测试消息；群、匿名反应、第二个账号等缺条件则单列 NOT_RUN，不为验收给无关联系人发消息。
- DeepSeek 真实检查包含思考关闭/开启及其支持强度、SSE 和工具链。OpenAI 需要有效目标模型访问条件；没有凭据时完成 mock/契约测试并保留真实验收缺口。
- 动画/视频贴纸的“预览识别”与“原样发送”分别验；没有完整运动解析就不写“已理解动画”。
- 主动发送实测必须有已授权目标、具体触发和可取消项，不能把演示脚本随便发出一条消息当完成。
- UNKNOWN 专指实际派发不确定；未实施、未运行、缺外部条件分别列出。

### 11.3 数据与发行

保留当前 PG、聊天、Key、Bot 接入和私人角色。必要字段向前更新有版本检查和配套备份；回退遇到结构不兼容时使用对应备份，不暗中清库。

新增消息/贴纸/反应/主动候选记录进入现有备份与诊断范围；导出诊断默认不带私人内容或 token。体积和查询有上限，临时活动不入长期库。

每批提供 Windows/Linux 可下载归档、真实测试记录、未运行项与使用说明。Hytron 更新前核对任务、待发消息、UNKNOWN、配置与备份；正常回归不停止常驻 Bot，不修改 NPM 或其他服务。新的主动发送和模型自主反应开关不在更新时自动打开。

## 12. 官方资料、已知限制与开工顺序

### 12.1 资料核对

本次已实际读取：

- [DeepSeek Thinking Mode](https://api-docs.deepseek.com/guides/thinking_mode)：开关、强度、协议差异和 reasoning_content 的工具接续要求。当前页面示例使用 deepseek-flash，后续实施仍需按实际 API 行为核验。
- [OpenAI Reasoning models](https://developers.openai.com/api/docs/guides/reasoning)：reasoning effort、模型相关的可用值/默认值、Responses/Chat 字段与用量语义。
- [Telegram 官方 Bot API 实现](https://github.com/tdlib/telegram-bot-api/blob/master/telegram-bot-api/Client.cpp)：实际核对 sendChatAction、sendSticker、getStickerSet、setMessageReaction、格式 entities 与 sticker 类型字段。

[Telegram Bot API 文档](https://core.telegram.org/bots/api)在本机直接读取时 TLS 握手失败，因此本轮使用官方仓库源码核对接口存在与参数路径；没有把未读到的最新平台限额、账户资格或客户端行为标为已验证。实施前需补文档/实测核对，不能用猜测补齐。

当前计划不承诺自动取得用户已安装的全部贴纸包、不承诺读取所有私聊反应事件、不承诺所有模型/代理支持相同思考参数。这些以明确接口、真实权限和测试结果为准。

### 12.2 实际开工顺序

1. 记录工作树和版本；建立 TG 富文本、输入活动、Unicode 拆分与消息顺序的回归，交付 v8.3.1。
2. 完成模型思考能力表、请求/响应和工具接续，再接 GUI/CLI/AI 设置；同时完成 TG 贴纸与 reaction 的闭环。
3. 将经验总结 0.1 作为独立可用流程交付，不等待 AffectWeave 人格算法完成。
4. 在可靠的 outbox 上补表达分段、逐段回执和受限主动投递，再供 AffectWeave 显式申请使用。
5. 阶段结束按实际实现、测试、NOT_RUN、UNKNOWN 和风险汇报，验收通过后再安排生产更新。

2026-11-07 制定方案时仅完成源码与官方资料核查。2026-11-08 的实际实现、测试、未运行项与交付状态以[本批验收记录](VALIDATION_V8_5_2026-11-08.md)为准；本轮没有向真人 TG 发送测试消息或调整生产人物配置。
