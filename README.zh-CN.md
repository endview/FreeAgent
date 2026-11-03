# FreeAgent Harness

一个精简的本地 Harness：持久对话、模型与工具调用、流式输出、精确审批、
取消与中断恢复。Windows GUI、CLI、Linux Web 和可选外部渠道共用 Owner、
TaskService 与 SQLite Store，基础文件操作不需要安装 MCP。

当前已发布 **v7.11.0-dev.1 开发预览**。
[Windows / Linux 便携包](https://github.com/endview/FreeAgent/releases/tag/v7.11.0-dev.1)
· [English](README.md) · [v7.11 实施与验收](docs/FREEAGENT_V7_11_EXECUTION_DIAGNOSTICS_PROGRESS.md)。

v7.11 重点减少命令观察和文件搜索的重复扫描工作，增加 `doctor --text`
离线诊断与下一步处理建议，以及服务器安装器的 `--check` 预检。每次文件读取
仍检查实际路径和句柄；Provider 测试与配置开关由用户显式操作。

使用独立的 `Harness-v8` 数据目录、Store v10 和 backup-v4。不导入旧配置、
数据库或备份；请解压到新目录，保留旧程序自己的数据。便携包未签名，
不包含用户数据或凭据，无需安装 Go 或 Node。

## 开始使用

Windows 解压后双击 `FreeAgent.exe`；CLI 位于 `bin\freeagent.exe`。
桌面使用系统 WebView2，缺少运行时时改为打开本地浏览器。
Linux amd64 解压后执行 `./FreeAgent`，在浏览器打开本次启动输出的私有地址。
默认数据在包内 `data/Harness-v8`，工作区在 `workspace`。

新安装没有模型。先在 Key 管理页面添加服务地址和 Key、选择模型，再显式开启
对应 Provider。Provider 默认关闭；保存配置不会自动联网。测试连接和获取模型
是用户主动发起的请求，关闭 Provider 会阻止后续模型派发，但保留 Key 和模型条目。

Key 不接受命令行参数。CLI 支持隐藏输入、`--key-stdin` 或 `--key-env`；
Windows 保存时使用当前用户的 DPAPI，Linux 使用受限权限文件。
`key list` 和 `doctor` 不显示 Key。

在 Windows 便携包的解压目录使用同一数据和会话（把占位值换成实际会话 ID）：

```powershell
$exe = (Resolve-Path '.\bin\freeagent.exe').Path
$data = Join-Path (Get-Location).Path 'data\Harness-v8'
& $exe sessions --data-dir $data
& $exe chat --data-dir $data --resume '<会话ID>'
```

GUI 或服务在线时，CLI 经 attach 连接同一 Owner。断开 attach 或关闭公网
浏览器不会自动停止服务端工作；停止、继续、结束任务和在线备份使用同一套记录与审批。
详见 [Windows 便携说明](docs/PORTABLE.md)和 [Linux 便携说明](docs/LINUX_PORTABLE.md)。

## 对话、权限与工具

- 新空间和外部发起人默认 None。文件与命令按实际权限模式执行；模型提出的
  产品设置变更需要已有 GUI 卡片或 CLI 提示确认，不要求手打确认码。
- 持久上下文摘要保留原始历史，支持手动 `/compact` 和可选自动压缩。
  摘要请求沿用任务预算和调用记录，不隐式学习人格或建立后台知识治理。
- 支持工作区文件工具、可选命令和网络工具、MCP，以及当前 Chat Completions、
  Responses、Anthropic 协议适配。联网能力必须由所选 Provider 或工具实际提供。
- 可选协作默认关闭，子任务与父任务累计预算同时约束；中断或事实不明的执行
  保留 UNKNOWN，不自动重放。
- 可选 Telegram、Lark、QQ 使用同一空间和权限核心。
  [Telegram 私聊文字与真实模型回传已验收](docs/FREEAGENT_TELEGRAM_PRIVATE_LIVE_ACCEPTANCE_2026-11-03.md)；
  群聊、第二账号和真实文件收发仍待验，Lark/QQ 保留本地模拟结果。QQ 任意文件上传不支持。

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

真实视觉 Provider、真实 Telegram/Lark/QQ 文件账号和系统信任公网证书仍为
NOT_RUN。临时 CA 验证不等于公网可信证书验收。完整结果、来源与限制见
[v7.11 实施记录](docs/FREEAGENT_V7_11_EXECUTION_DIAGNOSTICS_PROGRESS.md)。
[v7.10 发布验收 JSON](https://github.com/endview/FreeAgent/releases/download/v7.10.0-dev.1/FreeAgent-v7.10.0-dev.1-acceptance.json)
作为对应旧提交的历史证据保留。
