# AffectWeave / FreeAgent 宿主适配

更新：AffectWeave 1.1 使用[通用状态契约 v2](EXTENSION_STATE_CONTRACT_V2.md)。Core 只使用 scope.resource 和 payload；人物 ID 与业务字段由本扩展适配。数据库保留 aw schema 和既有存储格式 1。

参考：AffectWeave Python 提交 `d8be9d3255006c51a96c114e36949771ae26517c`。生产实现仅 Go；旧 Python 只作历史规则对照，不随生产包运行。

## 身份、时间和上下文

- Core 提供自己的持久 ID、已验证的空间/会话/Run、已采用消息 ID、来源人物 ID、消息采用时间；不接受模型或昵称提供这些身份。
- 排队未采用消息不进入扩展。初始输入、引导输入从既有消息记录读取；每个 adopted ID 只推进一次，重试/工具后重入读已冻结版本。
- 消息序号在每个会话内递增，因此 adopted ID 使用“会话ID/消息序号”，不使用伪造的全局序号；引导输入保存实际 adopted_at。
- 扩展数据键为 Core/空间/角色/人物。角色授权使用明确空间列表，无隐式全局共享；相同空间跨会话连续，群空间与私聊隔离。
- 扩展只返回有界人物片段，不重复原始聊天或 Core 摘要。Core 继续核算总预算，不把人物数据当成权限。

## 状态管理与原操作核对

在现有 `freeagent-ext/1` 上增加可选 `state` 能力：`state.read`、`state.preview`、`state.apply`、`state.result`。manifest 显式声明；普通无状态扩展不受影响。

`passive_context` 允许 None 聊天使用显式选择的人物数据；manifest 禁止工具、工作流、额外模型预算、私有历史/工作区 needs 和操作检查。Host API 仅允许 activity.report。普通标量设置由 manifest.model_settings 白名单及 settings_schema 约束；env_from 仍用户专用。完整管理页为 FreeAgent 自带按需加载代码。

GUI、CLI、AI 复用 `extensions.change_state` 控制操作。预览包括具体 before/after、范围和扩展 revision；批准绑定同一内容。角色创建/编辑/删除、状态重置/删除、模型提出的关系/情绪/经历变化都由这条边界处理。普通事实登记和固定规则衰减不等于隐式学习。

扩展在单个 PG 事务中保存变更与原 ControlOperation ID/摘要/结果。同 ID 同内容返回原结果，不同内容拒绝；Core 在派发前持久记录发送意图。回执丢失保留 UNKNOWN，重新进入只查询 `state.result`，不再次调用 apply。原进程未确定结束且无结果时保持 UNKNOWN；若明确未提交，只允许新预览/新授权。

## 事实、交付和恢复

- `turn.finished` 只表示生成/执行结束，不能证明客户端显示或 Telegram 发出，更不证明用户已读。
- 限定范围的事实读取复用现有消息/Run/渠道 delivery meta；按持久消息 ID 翻页，原 Run 的迟到回执可按原 Run 补查。无第二套事件总线。
- 发送成功/部分/失败/UNKNOWN 分别记录；后续反馈需要原事实身份，模型自评不提升证据等级。长期反馈仍需控制确认。
- Core 提供有限来源边界核对。扩展落后可补读已有事实，领先缺源/不同 Core 的备份拒绝启用；不补跑模型和工具。
- 配套备份核对扩展 state.sources 返回的有界分页来源，包括原会话、Run、空间、参与者和原输入内容。普通上下文补读本会话近期同角色最多 16 个 Run、192 KiB；大量历史的显式补读另走分页操作，不能以这个窗口冒充无限历史恢复。

## PG 和备份

仅显式初始化存储时，在同一 PG 实例创建独立数据库、受限角色和私有连接文件；扩展不获得 Core 连接或模型凭据。当前状态有界，输入冻结、事实、经历和原操作结果分表；不无限扩大单个人物 JSON。

配套备份只暂停扩展写回，等待在途变更结束并核对原回执；普通 Core 任务继续。先后生成扩展 dump、Core 备份和匹配清单；失败不发布完整标记，释放暂停。恢复验证身份和持久来源边界，不能混搭两份库。暂停设置期限并在 finally 释放，不建设通用协调器。

## 有意语义差异

Python `prepare_turn` 每次按当前 aggregate 生成上下文，Go 按已采用输入冻结；Python `commit_agent_output` 立即应用长期提案，Go 生成事实与持久提案分开，后者确认；Python delivery 二元成功/失败改为真实 sent/partial/failed/unknown/未观察；Core 权限与模型回答链路不被扩展覆盖。

## v8.7 补充：分析与主动执行

见 [有界扩展执行](SUPPLEMENTAL_EXTENSION_EXECUTION.md)。人物/情绪/表达算法不得进入 Core；分析与主动执行分别声明能力、默认关闭。扩展通过原 model.generate 使用当前模型，不保存 Provider Key；原 Run/Attempt/outbox 保留唯一权威。未启用时不增加调度进程或定时器。AffectWeave 1.6 使用此契约，旧版宿主不做兼容分支。
