# 上下文记录来源与写入契约

所有主请求、摘要、分片和回读共用 `contextVisible` 及原始记录读取投影。`conversationRecordRows` 只是一段按需 SQL，不是一份持久化上下文。旧行的类型和已知投递归属集中兼容；完整性摘要仍使用 `raw_json` 原值。

| 写入位置 | 类型/含义 | 投影与回归 |
| --- | --- | --- |
| `store.go / insertMessage` | `user_input`、`model_output`、`tool_result`；模型/工具/NOT_RUN/核查统一入口 | 类型校验；完整调用组；`TestContextProjection*`、`TestConversationMessageWritersHaveProjectionRules` |
| 同入口，明确的定时生成 Run | `generated_candidate`，包括自动触发提示与生成过程 | 当前执行可续接；不进入后续历史或摘要。确认投递后才发布；`TestGeneratedCandidateOnlyBecomesHistoryAfterConfirmedPublication` |
| `channel_messages.go / ReceiveChannelText` | 文本、引用、媒体描述等用户输入；命令标为 `notification` | 已验证平台身份；命令不作为新模型要求；渠道与输入队列回归 |
| `input_queue.go` | `user_input`，Deferred 表示待采纳 | 采纳事务重新分配可见顺序，稳定请求身份不变；`TestSteering*`、`TestExtensionInputCacheRefreshesOnlyAfterAdoption` |
| `spaces.go / publishConversation` | `published_content` | 用户明确发布到目标空间；不提升来源的指令权限；空间权限回归 |
| `conversation_branch.go` | 按当前受众复制语义消息；授权的已发表内容保持为资料 | 不复制旧摘要、旧执行 Run 或旧回读游标；分支回归 |
| `conversation_records.go / saveChannelReceipt` | `delivery_receipt` 或 `notification`；在 meta 保存原 Run、投递键、状态及可确定的源消息 | 新回执不写第二条模型助手消息；UNKNOWN/失败不冒充送达；分段与原文展示/导出共用可见记录 |
| 同入口，确认送达的生成内容 | `published_content` | 是已发表的原创内容；不再重复注入生成候选；异步到达不会拆开完整工具组 |
| `channel_attachments.go`、`channel_delivery_queue.go` | 已确认文件投递，调用上述同一回执入口 | 导出不等于送达；不暴露未确认正文；渠道附件、公开导出与回复定位回归 |
| `context_tools.go / read` 的已捕获工具结果 | `tool_result`，附 `ContextReadback` 来源/版本/范围 | 同授权读取；重复正文不反复摘要，后续纠错保留；`TestContextRead*`、`TestSummaryReferenceRepairAndReadbackCorrections` |

`notification` 在 UI 和操作记录中可查，但不充当普通模型助手回答。`delivery_receipt` 在完整视图去重，在公开视图只提供确认公开的部分。旧回执仅从精确的请求键、原 Run 与投递元数据关联，禁止使用“前面最近一个 Run”猜测归属。

增加新类型必须更新 `storedMessageKind`、旧记录兼容、受众和排序规则及测试。增加直接 `INSERT INTO fa.messages` 的路径会触发写入清单回归失败，必须说明为何不能复用统一入口。原始记录不被投影类型补全、预览或摘要改写。

各扩展的输入材料绑定真实已采纳输入。相同输入的重建复用块和附件；新增引导后重新准备输入相关块。附加模型调用和状态更新沿用现有输入身份及扩展幂等协议，不按扩展名称特殊处理。Core 的内部来源和 Provider 私有思考不会随着普通历史 DTO 传给扩展。
