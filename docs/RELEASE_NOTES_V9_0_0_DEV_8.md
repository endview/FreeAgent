# v9.0.0-dev.8：实例管理账号

本次预发行交付管理账号计划 A 批。现有公网用户使用用户名 `admin` 和原密码重新登录一次；旧密码格式不变，不改 Telegram 准入、确认名单、会话能力选择或 AffectWeave 配置。

- 新服务器可经 SSH 使用 `freeagent admin init --username NAME` 创建账号，并用 `freeagent public enable --origin https://HOST` 预览、开启公网入口。HTTPS、反代组和受限 Unix socket 仍由部署方准备。账号恢复使用 `freeagent admin reset-password`。
- Web 支持修改用户名、管理登录会话、再次验密后写入 Provider Key 与 Telegram Bot Token。Bot 核对只调用 `getMe`，不消费更新。秘密不回读；验密过期清空秘密输入，保留其他字段。
- 远程提出的扩权或新增宿主执行配置保留为同一条提案。本机用 `freeagent control inspect --operation ID` 查看，使用 `control confirm` 或 `control reject` 决定。公网 MCP 测试不启动候选进程，单项导入可准备本机提案。Web 对话交接后可继续发新消息。
- 未创建账号或公网入口损坏，不阻止原有本机及渠道使用。没有数据库结构迁移。

设置全量盘点与保存回归（B 批）以及可选 Telegram 私聊管理绑定（C 批）不在本次预发行；现有 TG 会话确认和工具审批不变。真人 Telegram 发信和真实模型不计入隔离测试。

操作与安全边界见 [公网 Web 指南](PUBLIC_WEB.md)。
