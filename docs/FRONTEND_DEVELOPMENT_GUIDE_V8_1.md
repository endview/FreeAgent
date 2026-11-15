# FreeAgent v8.1 前端开发指南

工作目录为 `internal/chatui`，Node 24.22.1、pnpm 11.25.0；Go 工具链以仓库 go.mod 为准。发行包无需 Node。目录和数据规则见[架构](FRONTEND_ARCHITECTURE_V8_1.md)。

## 构建与测试

在仓库根目录：

```powershell
pnpm --dir internal/chatui install --frozen-lockfile
pnpm --dir internal/chatui typecheck
pnpm --dir internal/chatui lint
pnpm --dir internal/chatui test
pnpm --dir internal/chatui build
go build -o output/freeagent-ui.exe ./cmd/freeagent
python scripts/test-frontend.py --engine output/freeagent-ui.exe --runtime output/postgresql-runtime/windows/pgsql --channel msedge
```

`--runtime` 指向已准备的 PG runtime 根目录，在 Debian 使用 Linux runtime 并以非 root 身份运行；浏览器安装用 `pnpm --dir internal/chatui exec playwright install chromium firefox webkit`，Linux 系统库按 Playwright 的安装提示准备。Windows 可用已安装 Edge 的 `--channel msedge`，省去另一份 Chromium；Firefox/WebKit 仍需安装。

浏览器脚本建立全新数据/工作区和独立端口，使用临时 PG、Echo 与回环合成 Provider，不接入生产 Owner。结果保存在 output/frontend-acceptance 的本次子目录；原始运行目录和 owner.log 可能含本地 capability，不作为可公开附件。脚本退出经 handoff 清理测试 Owner，外层停止临时 PG。

逻辑测试没有伪装成原生 IME 或手机验收。`browser-browsers.cjs` 的 229 检查是合成键盘事件；真实手机、Windows IME 与原生 WebView2 另行记录。所有测试失败先调查，不能删安全测试来使构建通过。

Go 契约/API 检查需要 PG 时：

```powershell
python scripts/test-postgres.py --runtime output/postgresql-runtime/windows/pgsql -- go test -race -p 2 -count=1 -timeout=30m ./internal/chatui ./internal/harness ./cmd/freeagent ./cmd/freeagent-desktop
```

## 同源开发

一个终端 `pnpm --dir internal/chatui dev` 持续生成 `.next-dist`。在独立数据目录初始化 Core，另一个终端构建 `go build -tags freeagentdev -o output/freeagent-ui-dev.exe ./cmd/freeagent`，给该进程设置 `FREEAGENT_UI_DEV_ROOT` 为 `.next-dist` 的绝对路径后启动 `web`。初次构建先运行 `pnpm --dir internal/chatui build:dev`。

不要在开发环境变量中放 Key；所有 `VITE_*` 都可能进入浏览器产物。无需开放 CORS、关闭 Origin/Host 或放松 CSP。开发 Core 的同源响应使用与生产相同的安全策略。发行程序忽略开发目录变量。

仅开发构建的 `?view=gallery` 用合成样本检查长中文、窄屏、错误、审批和 UNKNOWN。生产构建检查不包含 Gallery chunk、源码 map 或开发标记。旧正式入口已删除，不维护两套交付前端。

## 增加一个设置项

以 `extension_max_model_calls_per_turn` 为实际例子：

1. 在 Core Config/校验中定义有界标量。沿用 `runtime.set_limits`；不要另建 settings 平台。
2. 同时修改 apply、资源快照、精确 revert、只读 config/state 投影和 Control catalog 输入 schema。否则保存成功也可能在撤回时清空其他预算。
3. `AdvancedSettings.tsx` 添加 typed 字段，使用 ControlPreview。预览展示具体值，批准后才落库；错误/拒绝保留原配置。
4. 在 `frontend/settings-map.json` 核对字段和操作目的地；`TestFrontendSettingsCoverage` 对新增遗漏失败。显式设置 `FREEAGENT_UPDATE_FRONTEND_SETTINGS=1` 运行该测试才重生成，之后移除环境变量，审阅差异。
5. CLI 复用 `control plan --kind runtime.set_limits --arguments '{"extension_max_model_calls_per_turn":3}'`，按终端提示确认；AI 通过已有 control 工具提出同一操作，秘密值不进入模型参数。
6. 增加部分更新/精确撤回和实际 GUI 行为回归。不要把字段映射存在当作控件已经可用。

Config 保存位置与作用域、GUI/CLI/AI 入口和生效时机见[覆盖说明](FRONTEND_SETTINGS_COVERAGE_V8_1.md)。UI 偏好必须写明当前设备，不能借 Core 控制操作虚报另一客户端已调整。

## 增加一种事件

1. 在 `internal/harness/event_kinds.go` 注册常量，实际发送点使用常量。
2. `frontend/lib/events/policy.ts` 为该类型写明 refresh/stream/ignore 策略；只有确需即时显示才新增局部 reducer。
3. 增加必要 TS 字段。更新 `contract_fixture_test.go` 的合成真实 Go 投影，经 `FREEAGENT_UPDATE_FRONTEND_CONTRACT=1` 显式重生成样本，再移除环境变量。
4. 跑 Go `TestFrontendEvent*` / `TestFrontendContractFixture` 和 `pnpm test`；实际发送入口覆盖检查防止绕过清单。
5. 测重复、跳号、代次变化与快照加载期间到达；不把查询结束时最大 seq 直接假设为快照已覆盖。

不建设通用 Go/TS 协议生成器或持久事件回放库。

## 引入 shadcn/Radix 组件

`components.json` 已提交；CLI 固定为 shadcn 4.23.0，使用 `pnpm ui:add <组件>`。命令会下载源码，必须审阅产物；升级 CLI 时显式改脚本并复测，不使用 latest。

保留来源/许可证和本项目修改说明，使用现有 token 及 `lib/utils.ts`。禁止添加另一个主题系统；基础交互优先已有 Radix。生产 CSP 下检查运行时 style、键盘/焦点、强制颜色和窄屏。Markdown 是唯一 HTML 插入边界，第三方依赖不能扩大它。

## 新增扩展详情页

在 `features/settings` 的应用代码中增加详情模块，用 React.lazy 加载；由已启用扩展 ID 的有限路由表选择。服务端只返回数据和本轮能力目录，不提供待执行脚本 URL。页内仍经现有授权/确认 API，不从扩展获得 Core 数据库或 Provider 秘密。v8.3 的具体页面与扩展协议另列实际能力，不能把本段当作已经可调用的新 API。

## 性能与排障

生产首屏 JS 预算 250 KiB gzip，Markdown/KaTeX/高亮和设置页按需加载。不要从旧的 HTML 展示 helper 导入纯计算函数，否则会把渲染库拉入首屏。

长历史与 10 分钟流式基准：

```powershell
python scripts/test-frontend.py --engine output/freeagent-ui.exe --runtime output/postgresql-runtime/windows/pgsql --channel msedge --benchmark
```

该基准仅对自己的临时库生成一万会话和五千轮，输出 API 200 样本统计、分页、输入延迟和 JS heap；不等于真实模型延迟或 WebView2 全进程组内存。正式重复构建、全量 Go/竞态、打包和原生设备验收另行执行。

请求失败先看界面分类和 Core 记录；UNKNOWN 不是普通重试按钮。可用 `freeagent doctor --export <新.zip>` 导出允许项诊断，不发送原始配置、owner.log、凭据、正文或数据库连接文件到公开 issue。

## 设置页的交互约定

- 所有设置内容位于唯一 `.settings-content` 容器中，整体居中并约束最大宽度。不要重新给每个 `.settings-detail` 直接子元素设置自动边距，也不要按页面补 `margin-left`；直接字段与按钮必须和标题共用内容轴。
- 勾选项使用 `.check-line`，保留原生 checkbox 与标签关联；可见框 18px、文字 14px、标签点击区至少 44px。渠道项用响应式网格排列，避免原生内联标签互相挤压。
- 公网登录复用 `LoginPage` 与既有 Cookie/CSRF 流程。实例地址只取当前 `window.location.host`；帮助与错误区域不得展示真实会话、固定部署域名或推测的锁定倒计时。密码保持在内存，提交即清除；登录交互可通过 `scripts/test-frontend.py --only login` 验证（仍需指定独立引擎与 PostgreSQL runtime）。
- 扩展提供的单次请求设置放在能力弹层里，输入器工具栏保持模型、思考、权限、能力等常用入口。单次请求草稿与会话能力选择分别保存，不能把临时表达选择写成全局默认。
- `shared.tsx` 的 `Field` 为原生控件与 `Choice` 关联帮助、原生校验错误和 `aria-invalid`。自定义控件需要继续传递这些属性，不把错误只放在页尾。
- `SettingsGroup` 按用途组织相关字段；复用语义 token。宽容器使用说明/字段两列，小容器堆叠；不要靠隐藏内容消除溢出。
- `useFormDraft(id, initial, true)` 用于确有持久保存行为的表单。第三项返回 dirty；成功保存后 `change(saved)` 再 `clearFormDraft(id)` 更新基线。它保留切页草稿，仅在有修改时接入浏览器刷新/关闭提醒；秘密仍不进浏览器持久存储。
- 浏览角色、筛选列表、只读工具输入不启用刷新保护。服务端自动刷新数据的表单要区分读入与用户编辑，不能把新快照误标为未保存。
- `DraftStatus` 与保存操作成组放在正常文档流中，避免遮挡字段；继续编辑后收起旧的保存成功提示。即时开关明确标注，和需要确认的设置分开。
- 点击区域和可见形状分别设计：Switch 具有 44×44px 点击区域及 40×24px 轨道，Dialog 关闭按钮具有 44×44px 区域。不要用通用高度规则把开关拉成圆形。

新增检查可运行：

```powershell
python scripts/test-frontend.py --engine output/freeagent-ui.exe --runtime output/postgresql-runtime/windows/pgsql --channel chrome --only usability,layout
```

该命令使用独立 Core 和合成数据。审查发现、设计取舍及实际结果见 [2026-11-15 交互审查](UI_INTERFACE_REVIEW_2026-11-15.md)。

## A+B 工作台维护

2026-11-15 的布局与轮次规则见 [正式迁移记录](UI_WORKBENCH_IMPLEMENTATION_2026-11-15.md)。对话样式在 `styles/conversation.css`，设置沿用 `workbench.css`。调整颜色只修改语义 token，新增功能保持一条全局事件流。

- 修改最近动态排序时，运行 `test/activity.test.mjs`：等待、失败、UNKNOWN 同属优先组，随后按更新时间排列；同一记录状态变化不能新增一行。已核实或已有重试的旧尝试仍留在完整链中。
- 扩展执行链展示字段时，同时核对普通快照与分页的 `projectChatActivity` 投影；每轮仅解码一次配置绑定。不要为界面另存 Run 或完整历史缓存。
- 模型、思考和权限选择属于后续任务；历史回答使用生成来源或明确标注的旧快照。能力全局启用、轮次选择、实际附加证据分别展示。
- 详情弹窗只在用户主动打开时聚焦，关闭后返回触发控件；实时更新不抢输入焦点。链条分页保留滚动位置，文本与长路径允许换行。

专项验收（先构建实际 Go 引擎）：

```powershell
pnpm --dir internal/chatui typecheck
pnpm --dir internal/chatui test
pnpm --dir internal/chatui lint
pnpm --dir internal/chatui build
go build -o output/freeagent-ui-ab.exe ./cmd/freeagent
python scripts/test-frontend.py --engine output/freeagent-ui-ab.exe --runtime output/postgresql-runtime/windows/pgsql --channel chrome --only workbench,tool-core,provider,settings,interaction,browsers
```

`workbench` 的多模型切换走真实 Core 与本地合成模型服务；270 条异常链使用明确的 UI 固定数据。分页、完整数量及跨会话/游标隔离另由 `internal/harness/chat_activity_test.go` 在真实独立 PostgreSQL 下验证。
