# 未确认操作与继续执行

适用 v8.8.0-dev.7。Attempt 的 UNKNOWN 表示事实仍未知；已可靠交接的操作不会单独锁住整个会话，也不会变成“成功”。本轮结束不等于目标全部完成。

## 如何处理

Web 对话的“未确认事项”显示稳定编号、原操作和处理结果。先只读核查，再选择“确认已生效”“确认未生效”或“结束提醒”。确认写明来源为用户；忽略只结束提醒，原未知事实和重复执行保护保留。

CLI 使用相同记录，可附加 `--data-dir DATA`：

```text
freeagent unresolved list --session CHAT
freeagent unresolved list --session CHAT --after LAST_NUMBER
freeagent unresolved ignore --session CHAT --item NUMBER --revision REVIEWED_REVISION --confirm
freeagent unresolved confirm-applied --session CHAT --item NUMBER --revision REVIEWED_REVISION --confirm
freeagent unresolved confirm-not-applied --session CHAT --item NUMBER --revision REVIEWED_REVISION --confirm
```

每页最多 50 项。NUMBER 是会话内固定编号；修订来自刚读取的记录。重复提交相同用户决定幂等，陈旧修订不覆盖新证据。分支继承原项引用和编号，重新开始不是回滚。

模型通过现有 `context_manage` 的 `unresolved` 动作分页读取，`offset` 表示上页最后的稳定编号；`resolve` 使用 `item`、`resolution`、`note`。`model_note` 只保存模型意见。忽略/用户确认必须经过原工具审批，包括 Full；不能把模型自述当作用户授权。

首版只向 Owner 完整授权视图提供未决详情，受限公开视图不泄露数量、编号或私有正文。内部重复执行检查仍覆盖适用操作，包括列表之外的记录。

## 能继续与必须停止的边界

允许交接的业务调用包括受控 MCP/扩展工具、带文件回执的同步写入和已确认清理的前台命令。交接在原执行锁和一个 PG 事务内完成：登记事实、分配编号、补齐 tool 结果、取消同批尚未派发的写调用。可信只读调用可按原权限继续。原始 UNKNOWN Attempt 保留。

存储/提交不确定、未分类来源、协作子任务、失控/仍存活进程和权限撤回不会被一律转成“先做别的”。提交回执丢失时读取原交接结果，确认原子记录后才继续；无法确认则阻塞。重启不自动生成回答或重放动作，已交接的中断轮次安全结束为 failed。

完全相同的业务调用按工具/后端、工作区/执行环境、渠道目标及规范参数检查，再次派发需要新确认。它不识别所有改写命令或替代工具造成的等效作用。用户确认“未生效”可解除此项额外确认；普通权限、其他未决和后台命令/outbox 的保护继续适用。

## 渠道部分交付

渠道以原 outbox 为权威，不复制为另一份 Attempt。Web“渠道投递记录”和 CLI `delivery list` 显示逻辑段数、实际平台消息和逐项回执。TG `/status` 在获准视图中说明已发、未知和未派发数量。

中间消息 UNKNOWN 时尾段暂停。用户在既有入口明确续送后，只发送从未派发的尾段；未知段不重发。忽略、迟到回执和重启都不自动续送。报告本身发送未知时不递归外发告警，仍可从 Web/CLI 查看。

## 旧版误判的精确维护

```text
freeagent doctor --reconcile-unknown --data-dir DATA
freeagent doctor --reconcile-unknown --apply-digest PREVIEW_DIGEST --data-dir DATA
```

先空闲核对和验证备份，再预览。当前只接受可由原冻结工具/能力及已知前置拒绝实现证明的 `control_read_state` 扩展读取拒绝，不按数量或“缺日志”猜测。应用保留原 Attempt，请求/响应不改，追加“已核实未执行”的版本化协调依据；幂等补齐协议后，只解除已无其他阻塞的原 Run。维护 API 仅本机交互 Owner 可用，公网不可调用。

升级后的新元数据与自动字节模式不应由旧程序写入。回退先保留升级后数据，使用升级前程序和同一时点的完整备份；不要只切换旧二进制。
