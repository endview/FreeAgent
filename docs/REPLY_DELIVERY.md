# 回复与一次性投递

适用：v8.6 开发版。渠道为可选组件；没有启用渠道时，GUI/CLI 的普通执行不加载平台连接。

## 普通回复

普通 Core 保持原回答，不提供完整一条/自然分段等表达模式；平台因长度和格式限制进行的必要分片仍可使用。原回答保留在 Core 历史。

安装并启用 AffectWeave 1.1 后，在设置 → 能力 → 扩展 → AffectWeave 选择默认回复方式；其自然段算法和默认值都属于扩展。当前会话选中该扩展后，输入区才显示 **AffectWeave · 回复方式**，可继承扩展设置、本轮完整一条或自然分段。本轮覆盖发送成功后清除，不改扩展默认值。未安装、停用或本轮未选中时，入口与策略都不可用。设置默认人物不会清掉表达设置。

CLI 和 API 使用通用能力选择，不保留专用 `reply-mode`。例如：

```text
freeagent run --session CHAT_ID --capabilities '{"extensions":["cc.endview.affectweave"],"extension_settings":{"cc.endview.affectweave":{"reply_mode":"paragraphs"}}}' "本轮问题"
/capabilities {"extensions":["cc.endview.affectweave"],"extension_settings":{"cc.endview.affectweave":{"reply_mode":"single"}}}
/capabilities inherit
```

JSON 中的选择替换本轮能力集合；如需其他 Skill/MCP/扩展也须一并列出。排队时保留当时的选择，重启恢复读取原 Run 快照。持久默认设置仍通过统一扩展设置与确认路径操作。

AffectWeave 在已有的 `turn.finished` 中返回具体 `reply_parts`，最多 3 个，额外段落合入最后一部分，代码与公式块不从内部拆开。Core 只校验每段非空、总量有界、顺序拼接逐字节等于原回答，不理解任何表达模式。失败时保留原回答并报告扩展错误；完整输出超出回调上限时不提供表达分段。停用或卸载发生在首次冻结之前时回到原回答；已经冻结/入队的组按原回执处理，不因设置变化改写或重发。

设置 → 连接与数据 → **投递授权** 只管理发送资源上限、间隔和一次性跟进规则。默认选择 1 张贴纸，可明确配置为最多 3 张；片段间隔 0–2000 毫秒。

Telegram 在回答完成后逐条交付，GUI/Web 继续显示原有流式输出。文字在模型选定的贴纸之前。整组内容、格式和顺序先持久化，随后才派发，重复回传同一结果不新建消息。

## 查看、停止与恢复

对话中的“渠道投递记录”显示逐项状态。每页 50 组，按创建时间倒序，可加载更早记录。CLI：

```text
freeagent delivery list --session CHAT_ID
freeagent delivery list --session CHAT_ID --cursor LAST_GROUP_CURSOR
freeagent delivery resume --session CHAT_ID --group GROUP_ID --digest REVIEW_DIGEST --confirm
freeagent delivery abandon --session CHAT_ID --group GROUP_ID --digest REVIEW_DIGEST --confirm
```

摘要由工具返回并绑定具体记录，界面用户只需核对内容并点击。恢复只处理从未派发的尾段；失败和 UNKNOWN 原项不会重发。某片段 UNKNOWN 或失败会暂停该组尾段，新请求仍可继续；旧组续送会等待已经开始的另一组发完。

停止会取消当前对话的未派发片段及候选，包括稍后才尝试入队的旧 Run 尾段。已派发请求仍可能到达；停止不能撤回它，迟到回执继续保留。放弃一组会留下持久取消标记，后来入队的同组片段也不能发送。

## 用户明确的一次性提醒

在原 Telegram 对话的投递面板选择发送时间、到期时间和固定正文，预览后确认。也可通过统一控制面 `delivery.schedule`，参数为 `run_id` 及 `candidate`：

```json
{"run_id":"RUN_ID","candidate":{"key":"reminder-1","text":"检查结果","after":"2026-11-09T09:00:00+08:00","expires":"2026-11-09T10:00:00+08:00","timezone":"Asia/Shanghai","reason":"用户明确要求的提醒"}}
```

仅发送已确认的固定文字或已知贴纸；到时不会执行命令或再次调用模型。最远 7 天；到期时间须晚于发送时间，间隔最多 24 小时。此明确的一次性授权不需要开启扩展主动跟进总开关。目标绑定原 Bot、空间和会话；会话移动、授权撤销或目标变化后取消，不改投新目标。相同来源 key 和相同内容返回原记录，改内容需新预览，不能换 key 重发 UNKNOWN。

## 扩展主动跟进

默认关闭。用户需要确认开启总开关，并为指定扩展、会话、时区、静默时段、每日上限、冷却和期限配置规则。界面新增规则初值为每日 1 次、冷却 6 小时，遇到新输入取消。只支持一次性的固定内容候选，没有周期性聊天或后台人物循环。

扩展必须声明 `delivery: true`、注册对应能力及 `delivery.check`，并被当前会话选用。Core 在到期时以新 invocation 请求扩展复核，再重新核对权限、规则、新输入和限额。复核不可用、过期或撤回就取消；不猜测人物事项是否仍适用。一次 transport poll 最多复核一个候选，避免积压超时相加。

TG 只负责发送与平台事实；人物动机属于 AffectWeave 自己的后续实现。Core 公共交付通过不代表人物主动表达已安装。开发接口见 [扩展指南](EXTENSION_DEVELOPMENT_GUIDE.md) 与 `extension-kit/SPEC.md`。

## 渠道准备与发送

平台格式和资源解析由渠道进程提供。Core 持久记录原文摘要和待准备结果，校验完整分片后冻结，沿用同一 outbox 顺序与逐项回执。准备未完成时显示“回答已完成，交付准备中”；这不代表已发送。

纯准备可重算，实际发送 UNKNOWN 不自动重投。确认过的发送绑定具体格式化内容、目标和摘要；格式变化不能沿用旧确认。全局配置里没有渠道且没有历史授权时只显示接入说明，已有授权和交付记录始终可管理。
