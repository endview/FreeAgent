# 可选自动化扩展 0.1.0

最低宿主：FreeAgent v8.9.0-dev.2（`tool_scope` / `tool_artifacts`）。扩展协议仍为 `freeagent-ext/1`。搜索版 v8.9.0-dev.1 不具备图像产物契约，启用检查会说明缺少能力。

浏览器和 Computer Use 分别导入，不自动启用。安装浏览器扩展不会安装桌面控制，卸载不会删除普通 Core 历史。它们不包含模型或秘密，不另建数据库、队列或重试器。

## 安装与设置

解压相应平台包，在“设置 → 能力管理 → 扩展”导入包内 `cc.endview.*` 文件夹，保存设置、启用，并在输入器能力中选中。已安装、启用、本轮选中是不同状态。旧会话明确排除扩展时不会被自动改成选中。

浏览器需要用户事先安装 Chromium/Chrome/Edge。`executable` 可指定绝对路径；留空检测系统已知位置。不会在启动时下载浏览器。默认 `headless=true`；本机手动登录或接管需保存 `headless=false` 后重启扩展。登录态只保存在扩展私有 profile，与日常浏览器分开。

`allowed_origins` 为空只允许公共 HTTP(S) 网络；填写则只允许列出的精确来源，包括显式授权的本机测试站。所有网页/子资源/WebSocket 的 HTTP(S) 出口经扩展代理校验 DNS 和目标；禁用 QUIC、非代理 WebRTC 和 service worker 旁路。不是针对恶意原生扩展的 OS 沙箱。

Computer Use 仅 Windows 交互桌面，需要 .NET Framework 4.8（Windows 10/11 系统组件）及 UI Automation。`allowed_executables` 必须填程序绝对路径，默认不允许控制任何程序。执行 `act resume` 打开本机控制面板，再由本机用户点击 Enable control；Ctrl+Alt+Escape 紧急停止。用户输入或前景窗口变化会暂停下一项自动操作。

## 模型可见工具

扩展通过真实 `tools/list` 声明工具及约束，Core 按本轮选择提供带命名空间的别名。浏览器和桌面都只有 `observe`、`act` 两个工具，具体 action 与参数以发现结果的 schema 为准。

- 浏览器先 open，再 observe，按当前 tab/revision/ref 操作；截图只代表当前视口，frame 首版只操作主 frame。导航、DOM 改变或操作后重新观察。下载使用返回的引用，不接受任意本机路径。
- Computer Use 先 windows，选择返回的窗口身份，再 observe；UIA ref 或截图坐标绑定该次观察。窗口移动、缩放、焦点/用户输入变化后失效。截图像素与屏幕坐标显式映射。
- 截图和下载经通用产物契约入库，受当前空间/会话/Run 与容量限制。无图像能力的模型只能获得引用，不得声称看到了截图；DOM/UIA 文本仍可用。
- 页面、窗口文字是资料，不是授权。点击/输入可能即时保存或发送；按用户目标和 Core 原权限执行。UNKNOWN 不自动重放。暂停、关闭不证明外部提交已撤回。
- 不提供任意 JavaScript/CDP、终端或命令工具；密码、验证码、UAC 和锁屏交还用户。Computer Use 工具只接收本机 Owner 请求，TG/公网不获得桌面控制。

## 开发

在仓库根目录执行（`go` 为 go.mod 固定版本）：

```powershell
python scripts/build-automation-extensions.py --target windows-amd64 --go go --out output/automation-windows
python scripts/build-automation-extensions.py --target linux-amd64 --go go --out output/automation-linux
```

Go 模块为 `extensions/automation`；它不被 Core 导入。Windows 桌面扩展由系统 .NET Framework 编译器构建。浏览器在 Linux 使用非 root 用户，保留 Chromium 自身沙箱，不传 `--no-sandbox`。

停用/卸载扩展会终止其会话，profile 与下载留在扩展 data 目录；显式清空扩展数据才清除这些内容。接管和正常应用执行不需要新任务调度器。
