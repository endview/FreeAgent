# FreeAgent Harness

一个精简的本地 Harness：持久对话、模型与工具调用、流式输出、精确审批、
取消与中断恢复。Windows GUI、CLI、Linux Web 和可选外部渠道共用 Owner、
TaskService 与 PostgreSQL Store，基础文件操作不需要安装 MCP。

已发布 **v8.3.0-dev.2 开发候选**，包含 React/TypeScript 工作台、排障工具、长历史访问和可选独立 Go AffectWeave 扩展接口。实际检查及设备边界见[验收报告](docs/FREEAGENT_V8_3_ACCEPTANCE_2026-11-07.md)，实施过程见[记录](docs/FREEAGENT_V8_0_1_TO_V8_3_IMPLEMENTATION_PROGRESS.md)。
[下载 Windows / Linux 便携包](https://github.com/endview/FreeAgent/releases/tag/v8.3.0-dev.2)
· [English](README.md) · [v8.0 实施记录](docs/FREEAGENT_V7_14_TO_V8_0_IMPLEMENTATION_PROGRESS.md)。

本地开发候选已推进到 **v8.5.0-dev.1**：TG 富文本、输入状态、贴纸与表情、模型思考控制、独立可选经验总结，以及有序回复和经授权的一次性跟进。实际验证见[本批记录](docs/VALIDATION_V8_5_2026-11-08.md)，用法见[回复交付](docs/REPLY_DELIVERY.md)和[经验总结](docs/EXPERIENCE_SUMMARY.md)。上面的 GitHub 已发布版本链接继续指向原发行版；升级不会自动开启主动发送。

本批按 [v8.3.1–v8.5 实施计划](docs/FREEAGENT_V8_3_1_TO_V8_5_CHANNEL_REASONING_EXPERIENCE_PLAN_2026-11-07.md) 推进。

当前工作区为 **v8.8.0-dev.1**：压缩成品字节与输出额度解耦，拦截空正文的无效重试，同配置失败跨轮抑制；更新在售模型预设，支持 Anthropic 自适应思考和各协议私有接续。配置、确认与 Attempt 仍复用现有核心；不增加数据库结构或人物职责。实际测试与其它厂商原生验收边界见[本次验收](docs/VALIDATION_V8_8_2026-11-09.md)。GitHub 下载链接仍指向已发布版本。

v8.6 的轻内核整改保持：经验方法使用可选来源分析 Skill；扩展状态采用中性信封、明确作用域和声明 schema；Telegram 格式与平台动作由独立渠道程序提供。开发继续遵循[扩展原则](docs/EXTENSION_DEVELOPMENT_PRINCIPLES.md)。

[AffectWeave Go 可选包](https://github.com/endview/affectweave/releases/tag/v1.0.0-dev.1)独立发行，默认关闭；普通 Core 不因此启动人物进程或创建扩展库。

v8.0 统一使用随包 PostgreSQL 15.19；增加有限执行恢复、未知工具结果只读核查、
可选渠道进程、固定 Memory 与二级设置。旧历史只留备份，不导入新库。
数据库启停和维护见 [PostgreSQL 使用说明](docs/POSTGRESQL.md)。

v7.12 使用 Telegram `/start` 身份申请、管理员批准与统一名单替换旧通用配对码。
接入只授予聊天，不自动提升工具权限；撤权保留空间与历史，阻止后续派发和回传。
运行状态区分当前 Run 的冻结权限与保存设置；未匹配命令规则按权限模式处理，
Full 仍遵守显式 deny/ask 和非空可执行文件白名单，旧命令快照不能静默续接。
新安装默认开启 4096 字节自动压缩，已有关闭设置保持关闭；同时修正提案到期、
附件最终版本选择和普通限额结算，只有用户明确继续才创建关联的下一轮。

使用独立的 `Harness-pg-v1` 数据目录、PostgreSQL v1 Store 和 backup-v1。不导入旧配置、
数据库或备份；请解压到新目录，保留旧程序自己的数据。便携包未签名，
不包含用户数据或凭据，无需安装 Go 或 Node。

## 开始使用

Windows 解压后双击 `FreeAgent.exe`；CLI 位于 `bin\freeagent.exe`。
桌面使用系统 WebView2，缺少运行时时改为打开本地浏览器。
Linux amd64 解压后执行 `./FreeAgent`，在浏览器打开本次启动输出的私有地址。
默认数据在包内 `data/Harness-pg-v1`，工作区在 `workspace`。

新安装没有模型。先在 Key 管理页面添加服务地址和 Key、选择模型，再显式开启
对应 Provider。Provider 默认关闭；保存配置不会自动联网。测试连接和获取模型
是用户主动发起的请求，关闭 Provider 会阻止后续模型派发，但保留 Key 和模型条目。

Key 不接受命令行参数。CLI 支持隐藏输入、`--key-stdin` 或 `--key-env`；
Windows 保存时使用当前用户的 DPAPI，Linux 使用受限权限文件。
`key list` 和 `doctor` 不显示 Key。

在 Windows 便携包的解压目录使用同一数据和会话（把占位值换成实际会话 ID）：

```powershell
$exe = (Resolve-Path '.\bin\freeagent.exe').Path
$data = Join-Path (Get-Location).Path 'data\Harness-pg-v1'
& $exe sessions --data-dir $data
& $exe chat --data-dir $data --resume '<会话ID>'
```

GUI 或服务在线时，CLI 经 attach 连接同一 Owner。断开 attach 或关闭公网
浏览器不会自动停止服务端工作；停止、继续、结束任务和在线备份使用同一套记录与审批。
详见 [Windows 便携说明](docs/PORTABLE.md)和 [Linux 便携说明](docs/LINUX_PORTABLE.md)。

## 对话、权限与工具

- 新空间和外部发起人默认 None。文件与命令按实际权限模式执行；模型提出的
  产品设置变更需要已有 GUI 卡片或 CLI 提示确认，不要求手打确认码。
- 持久上下文摘要保留原始历史，支持手动 `/compact` 和可开关的自动压缩。
  新安装默认开启；已获准聊天的 None 用户可压缩当前渠道公开上下文。
  摘要请求沿用任务预算和调用记录，不隐式学习人格或建立后台知识治理。
- 支持工作区文件工具、可选命令和网络工具、MCP，以及当前 Chat Completions、
  Responses、Anthropic 协议适配。联网能力必须由所选 Provider 或工具实际提供。
- 可选协作默认关闭，子任务与父任务累计预算同时约束；中断或事实不明的执行
  保留 UNKNOWN，不自动重放。
- 可选 Telegram、Lark、QQ 使用同一空间和权限核心。Telegram 新准入见
  [渠道说明](docs/CHANNELS.md)；原空间需管理员核对后重新授权，旧码不兑换资格。
  [v7.11 Telegram 私聊真实回传](docs/FREEAGENT_TELEGRAM_PRIVATE_LIVE_ACCEPTANCE_2026-11-03.md)
  仅作为历史证据，不能替代本版双账号、群聊、回调及文件实测。
  Lark/QQ 保留原配对与本地模拟结果，新的鉴权延期；QQ 任意文件上传不支持。

Windows 命令隔离使用 AppContainer；Linux 可选 Bubblewrap。沙箱不可用时
拒绝该请求，不回退宿主执行。沙箱内 Git 元数据只读。
Full 显式选择 host 执行，具有当前 OS 用户的权限，不代表管理员权限或 OS 隔离。
普通 Linux 项目的硬链接可以共享目录外 inode，写入会影响其别名。

## 附件与服务器

支持有界 UTF-8 文本、PNG/JPEG 输入、不可变文件导出、回复另存和包含附件的
备份。None 可以处理用户明确上传的内容；图片只有在模型声明视觉能力时派发。
真实视觉 Provider 的语义理解尚未验收，见 [附件说明](docs/ATTACHMENTS.md)。

Debian 12 可安装专用非 root systemd 服务，使用管理命令检查 Owner、备份、
启停与恢复。默认仅回环监听；远程本地访问可使用 SSH 隧道。
详见 [服务器部署与 Linux 沙箱](docs/SERVER_DEPLOYMENT.md)。

公网 Web 是 Linux 上独立、默认关闭的选项：Caddy HTTPS → 受限 Unix socket
→ 同一 Owner。使用单管理员、Argon2id、CSRF 和可撤销会话；错误次数锁定、
自动黑名单、IP 名单、登录 Cookie 检测等可逐项开关。
远程权限上限默认 Full，与空间及原发起人权限取低，新空间仍为 None。
证书、代理和登录配置需按 [公网 Web 说明](docs/PUBLIC_WEB.md)显式启用。
不要直接公开本地 Owner 端口。Windows 公网入口、Linux 沙箱联网和宿主可访问的
沙箱服务端口不支持。

## 构建与验收

开发扩展前先阅读[扩展开发原则：保持 Core 轻量](docs/EXTENSION_DEVELOPMENT_PRINCIPLES.md)，实际接入方式和命令见[开发指南](docs/EXTENSION_DEVELOPMENT_GUIDE.md)及 [Extension Kit](extension-kit/README.md)。

源码构建需要 Go 1.26.5；前端修改使用 Node 24 / pnpm 安装锁定依赖，运行
测试并重建嵌入资源。正式便携包要求干净源码、固定工具链和两次一致构建。

```powershell
pnpm --dir internal/chatui install --frozen-lockfile
pnpm --dir internal/chatui test
pnpm --dir internal/chatui build
go build -o ./bin/freeagent.exe ./cmd/freeagent
```

已发布的 v7.10 实际通过 Windows / Debian 普通与原生 race、84 项 JS 测试、WebView2
接续、真实 systemd / Bubblewrap、附件和临时 CA 下的 HTTPS / JSON / SSE。
独立 v7.8 生命周期构建完成 28,800 秒长测，480 次采样、无额外重启；最终
v7.10 二进制另有功能、归档和 HTTPS 验收，未宣称它也运行了八小时。
该版两个便携包均两次干净构建一致，公开 Release 的 6 个资产已回下载校验。
v7.11 的针对性测试、性能对比和交付结果单独记录在其实施文档中。
v7.12 已通过 Windows/Debian 普通测试、Windows 针对性 race、86 项 JS、
真实浏览器模拟渠道操作、真实 DeepSeek 自动压缩及两平台包级验证；
尚未完成真实 TG 新准入的双账号/群/回调/文件验证，详见本版实施记录。

真实视觉 Provider、真实 Telegram/Lark/QQ 文件账号和系统信任公网证书仍为
NOT_RUN。临时 CA 验证不等于公网可信证书验收。完整结果、来源与限制见
[v7.11 实施记录](docs/FREEAGENT_V7_11_EXECUTION_DIAGNOSTICS_PROGRESS.md)。
[v7.10 发布验收 JSON](https://github.com/endview/FreeAgent/releases/download/v7.10.0-dev.1/FreeAgent-v7.10.0-dev.1-acceptance.json)
作为对应旧提交的历史证据保留。

### v8.7 development candidate

Optional extensions can request explicitly authorized, bounded input analysis and scheduled generation. Both default off; character policy remains in AffectWeave. Core reuses Run/Attempt, confirmation and the channel outbox, with no personality dependency or always-on scheduler. See [execution contract](docs/SUPPLEMENTAL_EXTENSION_EXECUTION.md) and [validation](docs/VALIDATION_V8_7_2026-11-08.md).
