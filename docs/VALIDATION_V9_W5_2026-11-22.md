# v9 W5 验收

2026-11-22，[v9.0.0-dev.5](https://github.com/endview/FreeAgent/releases/tag/v9.0.0-dev.5) 已发布并部署 Hytron，产物对应 `5b7be3120cf7`。真人 Telegram 平台验收单独记录，不以模拟端点、成功连接或已获准的历史聊天替代。

- Windows 隔离 PostgreSQL：公共网页 A 断连产生已完成的读取失败 Attempt；B/C 正常读到，不自动重发 A。模型显式提出新读取时带新调用，并按原规则确认。重启只补足已记录读取标记的 `web_fetch` 结果，旧 UNKNOWN 及其他 HTTP 工具不改变。
- Telegram 模拟 API 发送与 outbox：确认 3,601 秒未截短；等待跨话题、跨连接器重启保存；重复回执不延长；其他聊天照常发送；到时按顺序续发，等待超过授权期限则停止。未知投递不走限流重试，迟到成功仍沿用原回执规则。
- v4：最小文本连接器通过认证协议、同一 TaskService 和 outbox 完成消息往返，未获文件、命令等执行权。未注册和未声明话题不可入站；v3 客户端登记被 v4 Core 拒绝，v4 客户端拒绝 v3 bootstrap。飞书、QQ 声明仅包含现有能力。Core 的生产依赖检查及 Go vet 通过。
- Windows 全量回归中的 Harness、CLI、桌面程序及其它包通过；首次 Web 后端回归发现旧接入测试需补 v4 登记、空间默认能力遗漏设置覆盖映射。补齐后整个 Web 后端包重跑通过。116 项前端测试、类型检查和 lint 通过。
- 最终提交的 Windows/Linux 各两次干净构建一致，两端便携包通过。隔离 Debian 12 最终渠道、网页读取、上下文压缩及未决行为竞态回归通过。第一次 Linux 候选的旧接入测试失败已保留记录，未使用第一次候选发布。Windows 桌面窗口实测未做。
- GitHub 五份附件摘要与本地一致。Hytron 配对更新后两 unit、wire v4、Telegram 轮询、doctor 和公网 HTTPS/CSP 正常；对话、Run、消息、Attempt、权限元数据及受保护配置核对一致。没有改权限、能力选择或 AffectWeave 设置，也没有发送真人测试消息。
- 真人私聊、群、论坛话题、双账号跨成员确认：NOT_RUN，正式 v9.0.0 未发布。

## dev.6：Telegram 停收修复

- Telegram 群更新携带负数 `chat_id`。连接器沿用了仅处理正数 `message_id` 的函数，导致该更新被 Core 拒绝，后面的群聊、私聊都无法推进。隔离 PG 和模拟 Telegram 复现旧版队首重试；修复后队首群状态落库，后续私聊通过，命名话题回复在原话题取得发送回执。保留丢失确认与重启后的去重检查。
- Windows、Debian 发行包中实际连接器通过回归；各两次干净构建一致，便携包和隔离 Debian 竞态检查通过。[dev.6](https://github.com/endview/FreeAgent/releases/tag/v9.0.0-dev.6) 五份附件摘要已验证，Hytron 已更新到 `a763f6dcad5a`。
- 生产先验证备份和旧数据一致，再启用新连接器。原游标正常推进，6 条平台积压从已有更新中处理完；确认新建私聊、群聊各 1 个已完成 Run，并分别有 Telegram 发送回执。没有另外发出测试消息、改准入或调整 AffectWeave。该结果只验证故障恢复，不计为真人话题、跨账号确认或完整工具验收。

## dev.7：入站确认与防堵

- 畸形路由仅在未入队或执行时记录确定拒绝；用原更新的摘要复用拒绝记录，正文和请求人不落入记录。重启后同一事件能够读取结果，不同内容复用同一 ID 不获确认。
- 发行连接器通过端到端模拟：确认拒绝后推进，随后 `pending` 即使返回 202 也保持游标，连接器重启仍从同一更新继续，得到确定结果后才处理下一条；丢失回执后不重复派发。Windows 渠道和 Web 回归、Core 依赖检查和 Go vet 通过；独立 Debian 最终包启动和渠道竞态回归通过。
- Edge 在 1440、768、390 像素验证诊断等待、重试计数、恢复与最近推进时间；无新增溢出。116 项前端测试、TypeScript、lint、构建通过。Windows/Linux 分别双次干净构建一致，便携包均通过；Windows 桌面窗口未实测。
- [dev.7](https://github.com/endview/FreeAgent/releases/tag/v9.0.0-dev.7) 五项发行附件摘要已核对，产物对应 `f4517e874753`。Hytron 仅使用隔离单元，未使用常驻 Bot 凭据，常驻仍是 dev.6。真人话题、双账号设置和工具验收未做。
