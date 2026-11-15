# AffectWeave 回复分段：FreeAgent 宿主配套登记

- 日期：2026-11-16（Asia/Shanghai）
- 核查基线：FreeAgent v8.8.0-dev.5 / AffectWeave 1.6.0-dev.2
- 状态：通用配套已实现并通过浏览器和模拟投递验收；最终发行/部署结果见[验收记录](VALIDATION_CONTINUATION_2026-11-16.md)。
- **表达主方案已归入 [AffectWeave 仓库](https://github.com/endview/affectweave)：`docs/AFFECTWEAVE_REPLY_SEGMENTATION_PLAN_2026-11-16.md`。该文件是唯一表达实现依据。**
- 本文只登记 FreeAgent 通用选择、界面和渠道配套，不维护第二份分段策略。关联 [UNKNOWN 与压缩主计划](UNKNOWN_AND_COMPACTION_CONTINUATION_PLAN_2026-11-15.md)。

## 1. 功能归属与版本

| 仓库 / 版本 | 负责什么 | 边界 |
| --- | --- | --- |
| **AffectWeave 1.6.0-dev.3** | 分条上下文偏好、人物模式规则、安全空行切分、超上限均衡合并、原文分隔符归属与无损输出 | 本项主交付；复用已有 context/delivery/reply_parts 契约，可独立于新 Core 发布 |
| **FreeAgent v8.8.0-dev.7** | 已保存能力与有效值反馈、经复现的通用选择缺陷、TG 首条引用、消息计数和 outbox 未决入口 | 不增加人物或表达策略；不阻塞 dev.6 的 UNKNOWN/压缩修复 |

扩展配置页面由 FreeAgent 自身前端代码呈现，不等于表达逻辑进入 Core。字段语义、默认模式、分条提示和算法在 AffectWeave 维护；Host 只校验无损内容、权限、顺序和回执。卸载或未选中扩展时，普通 Core 只做平台长度要求的必要分片。

## 2. 已确认的运行证据

- 全局已启用 AffectWeave，未保存回复方式。最近 15 个 TG Run 中，7 个已选扩展且完成非空回答；冻结设置无模式覆盖，reply_parts 和实际文字投递均为一项，采用扩展内置 single。
- **同一个会话**在 11 月 15 日 21:00:57（北京时间）保存了 conversation.set_capabilities，操作为 applied、user_direct，保存结果不含 AffectWeave；21:01 后普通请求沿用该选择。全局开关仍开，此次并非 /new 建新会话。日志证明经交互入口保存，不证明用户有意取消。
- 当前会话的显式选择不含扩展/人物；21:58 的压缩维护 Run 不能证明普通请求的能力恢复。现有错误 UNKNOWN 另按主计划协调，不借分段修复重放旧请求。
- 本地复现：默认模式+空行=1 段、自然分段+空行=2 段、自然分段+单换行=1 段。扩展主方案已撤回单换行猜测，改为模型表达空行边界后由扩展验证。
- 旧 UI 移走的是输入器工具栏中的控件，能力弹层仍保留，见 [UI 修正](UI_CONSISTENCY_FOLLOWUP_2026-11-15.md)。不会把控件放回工具栏。
- 当前 TG 普通答案由完成结果进入冻结/投递流程，未发现流式编辑完整正文后再补发分段；当前每个片段都携带相同回复引用，首条引用需要修正。

尚未复现浏览器保存失败、快照未到达覆盖空集合或切页串写，不能把候选原因当作事实。私有诊断位于忽略目录 output/affect-reply-review-20261116/，公开文档不保留真实对话或内部会话标识。

## 3. 通用选择与界面

现有优先级为：请求明确能力集合（含空集合）→ 会话已保存集合 → 无会话记录时按本地/Owner 私聊全局默认或公开空间空集合解析。全局启用不能覆盖已经明确保存的空选择。

/new 不复制旧会话能力/人物覆盖，按新会话默认规则解析；/switch 使用目标会话选择；移动空间保留既有隔离。先核对日志、会话记录、普通 Run 快照和实际上下文附加，再定位具体缺陷。

- 回归页面未就绪、快速切页、刷新、目录变化、冲突、停用再启用及显式取消。不得把加载中的空状态自动保存，也不得自动恢复用户取消的能力；有复现才修缺陷，不新建继承系统。
- 在扩展详情常用区域呈现其默认字段，复用原保存表单；高级 JSON 与可视字段使用同一草稿。
- 已有能力弹层的继承项显示有效值与来源，使用通用 manifest/default/保存值；没有默认声明才显示“由扩展决定”。前端不计算人物阈值，不承诺本轮段数。
- 保留下一次页面提交与长期默认的区别，不新增回复模式层级，不恢复工具栏控件。
- 草稿、冲突、错误、保存按钮、刷新回读、人物与表达设置交替保存都是已有框架的回归验收，不另做设置框架。

现版本即可在 Web 打开正确 TG 会话，选中扩展/人物，再在扩展高级配置保存自然分段；真实模型请求显式要求空行分条。完整步骤在 AffectWeave 主方案 §2；本次未修改线上设置。

## 4. TG 交付与 dev.7 未决清单

整组回答仅第一个实际文字消息引用原用户消息，后续逻辑段及平台分片不重复附 reply-to；目标与主题路由保持原绑定。远程 outbox 和直接派发均覆盖，旧冻结组不回写。

沿用现有逐项状态，只补逻辑段数、平台消息总数和已确认发送数。原文切片与平台渲染文本分别验证：原文不裁剪，格式化并校准 entities 后冻结，不在实际派发前 TrimSpace。安全分段与均衡选择均不在宿主实现。

群聊沿用既有授权、触发和能力选择，非 Owner 公开空间不自动继承私聊能力，不新增分段权限开关。typing 与审批/进度通知不计为答案正文，不能先发完整正文再发片段。

dev.7 清单引用原 outbox operation，显示已确认发出、未知及未派发尾段，链接现有续送/放弃；不复制为 Attempt 或新表。Web/CLI 随回执可见，TG 在用户后续查询时说明，不因发送未知再次自动外发告警。

用户明确确认“仅续送未派发部分”后，正常渠道轮询发送尾段；未知项不重发，“已审阅不确定性”不变成“已送达”。迟到回执不自动续送，用户可以放弃尾段。

## 5. 验收与兼容

- 固定两段文本用于独立 Core/PG + 模拟 Provider/TG：两次发送、两份回执，完成前无答案 send/edit，完成后无额外完整副本；新 Core 只有首条引用。验证 entities、平台必要分片及重启幂等。
- 第 2 段 UNKNOWN、第 3 段暂停；用户确认后仅送第 3 段。放弃、失效授权、忽略和迟到回执不重发未知段。
- 真人检查使用现有生产单一消费进程和真实 DeepSeek，请求它用空行分为两条简短回复，按实际输出、消息 ID 和回执核对。模型未遵循时如实记录，不要求固定文案；独立实例不得同时轮询生产 token，未获真人发送授权记 NOT_RUN。

| 组合 | 行为 |
| --- | --- |
| 旧 Core dev.5/dev.6 + 新 AffectWeave dev.3 | 新表达规则可工作；旧 UI 文案、每段引用和旧投递面板仍是旧宿主行为，不能宣称已有新未决清单 |
| 新 Core dev.7 + 旧 AffectWeave dev.2 | 通用界面可解析旧 manifest 默认值、渠道修正生效；仍无新提示和均衡算法，Core 不代做 |
| 新 Core + 新 AffectWeave | 完整方案，各自独立版本与回执可核对 |
| 未安装、停用或未选中 | 普通回答及平台必要分片，扩展表达入口/策略不生效 |

两仓库分别记录提交、包、Release 和 Hytron 安装版本。先核对会话选择与配置，再独立交付 AffectWeave；FreeAgent 通用项随 dev.7，不拖住 dev.6。升级前检查活动任务/未投递组，不重发旧答案，不改变人物、分析或主动对话开关。

## 6. 源码落点

- 选择：[capability_selection.go](../internal/harness/capability_selection.go)、[conversation_create.go](../internal/harness/conversation_create.go)、[taskservice.go](../internal/harness/taskservice.go)。
- 界面：[扩展字段](../internal/harness/extension_turn_settings.go)、[能力弹层](../internal/chatui/frontend/features/conversations/Capabilities.tsx)、[会话提交](../internal/chatui/frontend/features/conversations/ConversationPage.tsx)、[扩展详情](../internal/chatui/frontend/features/settings/CapabilitySettings.tsx)。
- 交付：[channel_text.go](../internal/harness/channel_text.go)、[channel_group_queue.go](../internal/harness/channel_group_queue.go)、[channel_runtime.go](../internal/harness/channel_runtime.go)、[channel_outbox.go](../internal/harness/channel_outbox.go)、[续送/放弃](../internal/harness/channel_delivery_control.go)、[TG 渲染](../internal/channels/telegram_format.go)。

AffectWeave 的表达实现和测试请修改该扩展仓库；此清单不是将表达策略迁入 FreeAgent 的授权。
