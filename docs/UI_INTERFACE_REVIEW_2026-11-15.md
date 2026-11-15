# Interface Design：工作台交互审查与修正

本轮按“审查 → 设计 → 实测”执行。使用本机安装的 `web-design-guidelines` 和 `interface-design`，浏览器验证沿用项目固定版本的 Playwright 与独立 Core/PostgreSQL。

## 审查结果

基线为 v8.8.0-dev.3。先检查实际代码，再在 Chromium 的 1440×900、390×844 下打开十类设置页面，使用合成数据复现交互。

| 优先级 | 位置 | 问题与复现 |
| --- | --- | --- |
| P1 | `frontend/features/settings/GeneralSettings.tsx` | 尺寸输入框在每次输入时验证范围；全选、清空再键入 300 不能完成正常修改 |
| P1 | `frontend/features/settings/SettingsPage.tsx` | 手机直接打开带 section 的链接仍显示分类列表，URL 指向的详情不可见 |
| P1 | `frontend/features/settings/Providers.tsx`、`frontend/lib/drafts.ts` | Provider 草稿切页保留，但缺少未保存状态，刷新/关闭没有草稿丢失提醒 |
| P2 | `frontend/features/settings/shared.tsx` | 字段帮助文字未通过 aria-describedby 关联；Key 的保存/留空规则不能作为输入的描述被读取 |
| P2 | `frontend/features/settings/RuntimeSettings.tsx` | 上下文的压缩策略、token 输出与请求字节上限平铺；自动摘要仍显示不可编辑的旧数字，没有区分实际自动目标与手动配置 |
| P2 | `frontend/styles/workbench.css` | 空 Provider 列表仍占一列；设置页在宽屏留白与低高度下的操作位置缺少明确层级 |
| P2 | `frontend/components/Choice.tsx`、`frontend/components/ui/button.tsx` | 长选择项缺少一致的宽高边界；按钮使用 transition-all；字段级错误、帮助、触控目标缺少统一规则 |
| P2 | `frontend/styles/workbench.css`、`frontend/components/ui/dialog.tsx` | 实图复查发现手机 Switch 被统一按钮高度拉成圆形；弹窗关闭区域只有图标大小 |

基线浏览器结果：五项明确失败（手机深链接、尺寸重输、Provider 未保存状态、帮助描述关联、刷新草稿保护）；切页草稿保留、十页横向边界、控件名称与运行时错误检查通过。证据位于忽略目录 `output/playwright/interface-review-20261115/before/`。

## 设计约束

- 使用者需要在阅读回答、调整能力与核对执行之间切换；页面优先告诉人“此处改什么、作用在哪里、是否已保存”。
- 沿用已确认的浅灰导航、白色阅读区、蓝色主操作与中文系统字体。复用现有语义 token、Radix 和 Button，不增加运行时依赖。
- 工作台领域：会话、上下文窗口、运行、确认、能力、作用范围、执行证据。视觉来自阅读纸面、灰色工具区、蓝色操作、绿色完成、琥珀等待与紫色未知状态；颜色只表达动作或状态。
- 特征是“配置与执行分开表达”：设置作用范围、保存状态、确认卡片、人物下一次选择、最近实际请求各自有明确位置。
- 设置保留内容轴，按用途分组；上下文按“自动压缩 → 请求预算与恢复”组织。自动目标不冒充固定字节值，人工上限按需出现。
- 采用 4px 间距基准，沿用设置标题 23px/600（窄屏 21px）、组标题 16px/600、字段 14px、说明 13px；Switch 与弹窗关闭区域为 44px。保持原生滚动、可见焦点和暗色模式。
- 草稿仍只保存在页面内存；本轮为 Provider 连接配置、运行设置、公网配置和附件策略接入刷新/关闭提醒。切换设置页不会多出确认，因为原有草稿可以恢复。复杂人物编辑与只读选择没有一律接入。

## 验收

| 检查 | 实际结果 |
| --- | --- |
| 前端 | 113 项逻辑测试、TypeScript strict、lint、生产构建通过；首屏 JS gzip 152,656 字节，预算 250,000 字节 |
| 专项交互 | 25 项通过。包含尺寸重输/非法值、字段校验、秘密帮助文本、草稿切页/撤销/保存、保存冲突、自动与手动摘要、搜索、手机深链接/返回/焦点、320px 下的选择弹层及 Esc 焦点恢复 |
| 12 组尺寸 | 2560×1080、1920×1080、1440×900、1366×768、1280×720、1024×768、768×1024、430×932、390×844、375×667、320×568、844×390。深浅主题和减少动态效果交替；对话、设置、能力弹层、上下文弹窗和保存字段边界通过 |
| 原有流程 | settings、provider、interaction、core 四组全部通过。真实 Core + 合成模型服务验证模型列表、思考设置、审批、冲突、附件、接续、草稿和 CSP |
| 浏览器 | Chromium、Firefox、WebKit 的 Echo 收发、IME 229 防误发、手机输入字号、设置入口和横向边界通过 |
| AffectWeave | 实际 1.6.0-dev.2 程序与独立 PG，共 26 项通过。导入、人物确认、分析/默认人物连续保存、实际上下文附加、备份和停用通过；详情页另检查桌面、平板、手机竖屏和横屏，44px 点击区与横向轨道分离 |
| 视觉 | 逐图检查，修复重复分隔线、空资源占列、旧顶部间距和手机开关变形；提供真实截图前后对照 |

测试脚本修正了等待 Core 就绪、创建报告目录、手机深链接定位与导航后响应正文不可读时的诊断处理。模拟的 409 用于验证失败保留草稿；生产模型、TG 与用户数据没有参与本轮测试。

证据在忽略目录 `output/playwright/interface-review-20261115/`：`before` 为审查基线，`usability-final` 为 25 项检查，`flows` 为原有流程，`compatibility-final` 保存三浏览器结果，`final` 保存最终比例与扩展验收，`review-comparison.png` 为对照图。

## 尚未覆盖

本轮是已复现问题的交互修正，不是所有功能的新视觉重构。Memory、MCP、渠道、人物编辑等复杂表单仍需逐一核对草稿基线后接入同样的保存提示，不能盲目把“读取/选择”认作修改。

真实手机软键盘、原生 Windows 125%/150% 系统缩放和辅助技术人工验收未运行；浏览器视口与 WebKit 模拟不能代替真机。没有发布新版本、推送 GitHub 或升级 Hytron。
