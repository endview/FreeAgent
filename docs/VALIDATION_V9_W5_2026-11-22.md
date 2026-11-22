# v9 W5 验收

2026-11-22，[v9.0.0-dev.5](https://github.com/endview/FreeAgent/releases/tag/v9.0.0-dev.5) 已发布并部署 Hytron，产物对应 `5b7be3120cf7`。真人 Telegram 平台验收单独记录，不以模拟端点、成功连接或已获准的历史聊天替代。

- Windows 隔离 PostgreSQL：公共网页 A 断连产生已完成的读取失败 Attempt；B/C 正常读到，不自动重发 A。模型显式提出新读取时带新调用，并按原规则确认。重启只补足已记录读取标记的 `web_fetch` 结果，旧 UNKNOWN 及其他 HTTP 工具不改变。
- Telegram 模拟 API 发送与 outbox：确认 3,601 秒未截短；等待跨话题、跨连接器重启保存；重复回执不延长；其他聊天照常发送；到时按顺序续发，等待超过授权期限则停止。未知投递不走限流重试，迟到成功仍沿用原回执规则。
- v4：最小文本连接器通过认证协议、同一 TaskService 和 outbox 完成消息往返，未获文件、命令等执行权。未注册和未声明话题不可入站；v3 客户端登记被 v4 Core 拒绝，v4 客户端拒绝 v3 bootstrap。飞书、QQ 声明仅包含现有能力。Core 的生产依赖检查及 Go vet 通过。
- Windows 全量回归中的 Harness、CLI、桌面程序及其它包通过；首次 Web 后端回归发现旧接入测试需补 v4 登记、空间默认能力遗漏设置覆盖映射。补齐后整个 Web 后端包重跑通过。116 项前端测试、类型检查和 lint 通过。
- 最终提交的 Windows/Linux 各两次干净构建一致，两端便携包通过。隔离 Debian 12 最终渠道、网页读取、上下文压缩及未决行为竞态回归通过。第一次 Linux 候选的旧接入测试失败已保留记录，未使用第一次候选发布。Windows 桌面窗口实测未做。
- GitHub 五份附件摘要与本地一致。Hytron 配对更新后两 unit、wire v4、Telegram 轮询、doctor 和公网 HTTPS/CSP 正常；对话、Run、消息、Attempt、权限元数据及受保护配置核对一致。没有改权限、能力选择或 AffectWeave 设置，也没有发送真人测试消息。
- 真人私聊、群、论坛话题、双账号跨成员确认：NOT_RUN，正式 v9.0.0 未发布。
