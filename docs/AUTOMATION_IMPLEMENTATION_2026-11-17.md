# B1 / C1 实施记录

基线：v8.9.0-dev.1 搜索版已独立交付。宿主候选 v8.9.0-dev.2；Browser 0.1.1 / Computer Use 0.1.0。

## 代码归属

- `extensions/automation` 是独立 Go module；Browser 使用系统 Chromium/Chrome/Edge 与 WebSocket CDP。`.NET Framework` 实现 Windows Computer Use，只依赖 Windows 自带 UIA/WinForms/GDI/SendInput；没有 Python/Node 运行时要求。
- Core 不导入自动化 module。新增 `tool_scope`、`localOnly` 和 `tool_artifacts` 是通用扩展接口；不同业务的合成图像扩展也实际通过 Core/PG 联测。
- 设置页从现有 schema 展示有限字符串、布尔和字符串列表，复用原保存和草稿；不建立第三方脚本 UI 或另一个配置来源。
- 截图沿用附件存储、受众、图像预算和三类 Provider 发送；只在请求投影保留最新工具图片，历史原记录和 UNKNOWN 不被改写。
- 不新增数据库表、Run 枚举、调度器、人物字段或渠道专用分支。AffectWeave 仓库与版本未改变。

## 已验证与交付

实际范围见[验收](VALIDATION_AUTOMATION_2026-11-17.md)。使用和停用见[操作说明](AUTOMATION_EXTENSIONS.md)。构建通过 `scripts/build-automation-extensions.py`，各平台包具有源提交、文件清单和校验和。Linux 扩展归档显式保留可执行权限。

普通 Core 的依赖检查通过，没有增加自动化运行时；未安装/未启用时没有浏览器和桌面进程。浏览器进程只在 open 时启动，status 不启动浏览器。Windows Job 与 Linux 进程树生命周期分别测试。

原生 Edge 在本机没有成功启动，真实流程采用可用的 Chrome；不自动换成用户日常 profile，也不传 `--no-sandbox`。Hytron 安装 Chromium 仅用于用户已授权的独立 Linux 验收，不为生产会话安装或启用扩展。

## 预览版边界

首版不提供 iframe 元素操作、远程交互视频、Linux/macOS 桌面控制、自动登录/验证码、任意 JS/CDP 或统一浏览器下载器。真实桌面已测 150%，100%/125% 和多屏保留 NOT_RUN；数学坐标测试另列，不能代替实际设备。

这两个预览能力分别可用；未完成项目不会影响已经交付的搜索版。后续新增平台继续按所属扩展实现并验证，不向 Core 引入业务算法。
