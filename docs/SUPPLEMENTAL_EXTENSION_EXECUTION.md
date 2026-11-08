# 扩展的有界分析与主动执行

配套版本：FreeAgent v8.7.0-dev.1；AffectWeave 1.6.0-dev.1 是接入实例，Core 不依赖其实现。

## 能力与默认成本

`freeagent-ext/1` 增加可协商的 `context_analysis`、`scheduled_generation`。manifest 的声明和 initialize 返回必须一致，需 state v2、passive_context 和正数 model_calls/output_tokens 预算。声明不是用户授权。未配置启用规则时不创建主动执行定时器；未安装扩展时不启动其进程、数据库或分析。人物、情绪、表达决策全部属于扩展。

普通 passive_context 仍禁止模型、工具和委派。context_analysis 仅在 turn/chat、当前和冻结的 analysis_enabled 均开启时允许当前模型做一次文本分析。输入不超过 4096 估算 tokens，输出不超过 1024 tokens；每采用输入一次、每 Run 最多两次、共 2048 输出 tokens。调用使用原 Attempt，不附隐式 Memory、附件、其他扩展或工具，不重试、不替代。缺 usage 或 UNKNOWN 保留额度。分析失败使用确认片段继续普通回答，辅助 UNKNOWN 不把主回答标为 UNKNOWN。

默认分析期限 8 秒，可设 2–30 秒，随停止和 invocation 取消。扩展必须冻结整个片段与经历内容，不能在下一 hook 用最新数据重建原输入。持久候选仍由原控制面逐项预览、确认和 CAS。

## 主动执行授权

`delivery.set_policy` 复用既有控制确认。`enabled` 是扩展投递授权，`generation_enabled[extensionID]` 是该扩展主动对话的唯一总开关。规则还需 `generation:true`、扩展、conversation_id、resource、具体 source_refs/话题范围及时间窗口。旧固定提醒不会自动获得模型生成能力。

首版只支持获授权的 Telegram 单人私聊。默认空闲 4 小时、10:00/18:00 时间槽、评估期限 30 秒（可配 5–120 秒）；静默时间、目标授权和已选择资源始终重查。滚动 24 小时每目标最多评估两次、Core 合计八次，同时仅一个评估；发送仍受同一目标所有规则汇总的日限额、冷却和未回复限制。默认一天主动开场一次。预览也计入评估额度，但永不发送。

用户输入在其他归类会话中到达时，同一平台目标的输入水位也变化。水位不比较不同会话的本地消息序号。新输入、总开关关闭、规则/资源变化会取消未派发机会和在途评估；已派发内容根据真实 receipt 记录，不承诺撤回。

## 一次机会

机会复用 fa.meta；执行复用 Run/Attempt，交付复用 outbox。触发身份为 `identity.trigger.kind=scheduled`，`adopted_inputs=[]`。actor 表示可以读谁的授权数据，authorized_by 表示原规则授权；没有模拟用户消息或旧输入重新采用。

Core 调 `wake.evaluate`，传冻结规则、当前受限已交付历史、来源引用和截止时间。回调只允许一次当前模型的 `model.generate` 及 activity.report；不得调用工具、embedding、delivery.propose 或修改状态。一次模型同时决定 speak/skip 和最终文字，最多 600 Unicode 字符；引用只能来自已授权列表。来源形状校验不等于验证全部自然语言事实。

Core 持久接受 speak 后才用稳定关联键进入 outbox；派发前重查总开关、输入水位、授权和资源。生成 UNKNOWN 不发送、不重新生成；发送 UNKNOWN 不重投。扩展有候选而 Core 没有接受响应时不恢复执行。Owner 变化使未落定评估保持 UNKNOWN、未执行旧机会取消；已冻结结果仅续接原交付。恢复备份时关闭 generation_enabled 并取消复制的未终结机会。

## 回复表达与固定跟进

完成的 turn.finished 可调用 `delivery.reply` 选择已授权贴纸或反应；每种动作有原 Run 的一次性 claim，重复回调不重放。需要额外确认的动作拒绝并保留普通回答。候选资源必须来自当前目标，不能给原始平台 file ID。动态拆段算法仍由扩展返回原文无损的 reply_parts。

`delivery.propose` 是已冻结原文的固定一次性跟进；delivery.check 仅复核当下事项是否仍有效，无生成权限。turn.finished 的可选 wake_hint 只引用已获准规则/来源及 not_before/not_after；Core 补齐目标与身份，扩展不能选择另一收件人。

## 操作入口

设置 → 数据与连接中的投递授权，或已启用扩展详情中的同一表单：配置总开关、规则、查看记录和“只生成不发送”。保存失败不显示为已生效。会话中仍可查看原实际交付记录。

```text
freeagent delivery wake-list --session CHAT --data-dir DATA
freeagent delivery preview --session CHAT --rule RULE --key UNIQUE --confirm --data-dir DATA
```

预览命令需要显式确认额外模型用量。永久授权修改仍用 control plan/confirm、GUI 卡片或 AI 的具体控制提案，不因 Full 权限省略。

公共 schema：extension-kit/schema/methods/wake.evaluate.json、delivery.reply.json、turn.finished.json。测试和未运行条件见 VALIDATION_V8_7_2026-11-08.md。
