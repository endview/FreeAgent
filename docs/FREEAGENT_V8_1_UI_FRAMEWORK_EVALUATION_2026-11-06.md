# FreeAgent v8.1 UI 框架评估

日期：2026-11-06。状态：本文件保留选型时的历史小样证据。正式前端现已采用 React 19 / Vite 8，当前实现与命令以[前端架构](FRONTEND_ARCHITECTURE_V8_1.md)、[开发指南](FRONTEND_DEVELOPMENT_GUIDE_V8_1.md)和[实施记录](FREEAGENT_V8_0_1_TO_V8_3_IMPLEMENTATION_PROGRESS.md)为准。

实施排期与退出条件见 [v8.0.1—v8.3 实施计划](FREEAGENT_V8_0_1_TO_V8_3_DEVELOPMENT_PLAN_2026-11-06.md)。本文件保留选型依据与小样结果。

后续实施决策已修订：F1 用户确认可交互样例后冻结视觉；F2 优先验证 Vite 8，未通过才固定 Vite 7；小型 store 与 Zustand 二选一。分组复用空间，审批在 F3，开发 gallery 替代 Storybook，正式要求见主计划第 5 节。下文的 Vite 7 小样结果保留其原始验证范围，不代表 Vite 8 已验。

减重后的实现约束：公网复用受限查询，断线默认重读快照；事件覆盖用实际常量清单和字段样本，不建回放/协议生成平台。未保存 Key 时提示保存或放弃后再刷新，不增加秘密草稿持久化。

## 1. 结论

建议采用 **React + TypeScript + Vite + Tailwind CSS 4 + shadcn/ui（Radix Primitives 基础）**。

三种 UI 工具承担不同职责：Tailwind 生成样式；Radix 处理复杂控件的交互语义；shadcn/ui 把可修改的组件源码交给项目。FreeAgent 在它们之上维护自己的视觉规则、业务组件和开发指南。

本轮实际构建了隔离小样，验证组件组合与当前产品 CSP。它证明技术组合可行，不代表完整前端、全部浏览器或原生桌面已经完成验收。

v8.1 的交付需同时包含：可运行前端框架、产品界面迁移、组件规范、开发指南及自动化验收。相关草案见 [前端架构](FRONTEND_ARCHITECTURE_V8_1.md) 和 [开发指南](FRONTEND_DEVELOPMENT_GUIDE_V8_1.md)。

## 2. FreeAgent 的具体约束

| 现状 | 对选型的要求 |
| --- | --- |
| Go 用 `go:embed dist` 携带前端 | 构建为同源静态资源；Node 只参与开发和构建 |
| Windows WebView2 与公网 Web 共用前端 | 运行时由同一 API/事件契约驱动；浏览器能力差异集中适配 |
| 本地启动 capability、远程 Cookie/CSRF 两种认证 | 统一 transport，保留各入口的实际认证方式 |
| fetch 实现带凭据头的 SSE | 复用消息契约；断线重连先核对快照，不重发任务 |
| 流式 Markdown、代码、公式、附件 | 独立内容组件，保持原文复制、渲染限制与安全边界 |
| 拖拽会话栏、阅读区与输入框 | 面板布局有键盘替代、尺寸约束和可恢复默认值 |
| Provider 草稿、待确认、UNKNOWN、队列、引导 | 这些是产品行为，不能交给通用 UI 组件推断 |
| `main.mjs` 约 3440 行 | 引入组件、类型和状态边界有明确维护收益 |
| 当前构建为 Vite 7.3.6 | F2 优先验证 Vite 8 和兼容插件，通过后固定；失败保留 Vite 7，其他依赖不无条件升级 |

现有的 `CHAT_CONTRACT.md` 等文档有历史语义，迁移时必须结合当前代码和验收修订。不能用旧文档中“自动压缩默认关闭”等过期描述覆盖当前产品行为。

## 3. 各层的作用与代价

### React 与 TypeScript

React 负责组件生命周期和局部更新；TypeScript 表达 API、事件、草稿、布局与控件契约。对本项目最直接的收益，是把复杂页面的手动 DOM 更新与全局事件分发拆开。

代价是迁移成本和浏览器运行时代码。类型也不能替代服务端校验：API 返回值需要边界解析，尤其是事件、审批和权限状态。

### Tailwind CSS 4

使用官方 Vite 插件生成静态 CSS，适合把间距、布局和状态样式与组件放在一起。通过语义化 CSS 变量统一颜色、字体、边界和状态。

风险主要来自使用方式：任意值散落、超长 className、动态拼接类名无法被扫描、Preflight 改变原 Markdown/表单默认样式。框架须限制这些问题，不能只装依赖。

Tailwind 4 的官方基础要求为 Chrome/Edge Chromium 111+、Safari 16.4+、Firefox 128+。具体使用的新 CSS 能力可能要求更高版本；JS 构建 target 不能补齐不支持的 CSS。[官方兼容性](https://tailwindcss.com/docs/compatibility)

### Radix Primitives

提供 Dialog、Popover、DropdownMenu、Tabs、Select 等交互基础，覆盖焦点、键盘和 ARIA 的重要部分。它不定义 FreeAgent 的外观、审批规则或数据状态；产品仍需提供名称、说明、焦点目标和真实行为测试。[官方介绍](https://www.radix-ui.com/primitives/docs/overview/introduction) · [无障碍说明](https://www.radix-ui.com/primitives/docs/overview/accessibility)

建议统一采用 Primitives；业务组件通过 FreeAgent 的 `components/ui` 使用它。一个项目选择一种组件基础，避免同类控件混合不同焦点/Portal 实现。

### shadcn/ui

这是源码分发方式：引入选定组件后，代码成为项目维护范围，可按 FreeAgent 规范调整。它不是安装一个主题包后就获得所有页面，也不会替项目完成业务逻辑。[官方说明](https://ui.shadcn.com/docs)

当前官方工具支持不同组件基础，配置时应显式选 Radix 并核对生成源码。不是所有 shadcn 组件都来自 Radix：本次读取的 Resizable 使用 `react-resizable-panels`，Button 使用变体工具和 Slot，图标来自 Lucide。

主要成本是上游修复需要审阅合并。新增或更新组件时保留来源、固定版本、查看变更，不能用 CLI 无差别覆盖已定制组件。

## 4. 方案比较

以下为依据当前项目的工程判断，不是四套完整应用的实测排行榜。

| 方案 | 优点 | 主要成本 | 判断 |
| --- | --- | --- | --- |
| 原生 TypeScript + 现有 CSS | 迁移最少、运行时较小 | 手工处理复杂 DOM、弹层、焦点、草稿和增量更新 | 适合小修，完整工作台重构收益有限 |
| React + Tailwind，自写控件 | 组件和视觉自由度高 | 对弹窗/菜单/组合框重新承担键盘与无障碍维护 | 不推荐作为全部控件策略 |
| React + Tailwind + 直接 Radix | 控件行为成熟、设计自由 | 每个控件的样式、变体、组合约定都需自行建立 | 可行，适合已有完整设计系统的团队 |
| React + Tailwind + shadcn/Radix | 复用基础源码并可定制，开发效率与长期控制较平衡 | 需要维护引入源码及其依赖 | 推荐，用少量基础组件建立 FreeAgent UI |

框架负责降低维护成本，视觉质量取决于实际布局、内容层级和组件设计。unslop 用于评审这些设计，不作为运行时依赖。

## 5. 推荐的依赖范围

| 类别 | 选择 | 使用规则 |
| --- | --- | --- |
| 运行与构建 | React/React DOM、TypeScript、Vite、React 插件 | 固定兼容版本；生产部署只需要静态资源 |
| 样式 | Tailwind CSS、`@tailwindcss/vite`、CSS 变量 | 明确源码扫描范围；产品 token 为唯一主题来源 |
| 基础组件 | 按需引入的 shadcn Radix 源码 | Button、Dialog、AlertDialog、Popover、DropdownMenu、Tooltip、Switch、Tabs 等逐项引入 |
| 变体与 class 合并 | cva、clsx、tailwind-merge | 仅在组件层集中使用，避免每个业务文件各写合并逻辑 |
| 图标 | lucide-react | 逐图标导入；替代原来的全局 DOM 图标扫描 |
| 面板布局 | 优先核实 react-resizable-panels | 核实像素约束；不足时小型分隔条。阅读宽度单独受控，输入器拖拽设最大高度 |
| Markdown/高亮/公式 | 适配现有渲染规则并按需加载高亮/KaTeX | 保留原文与行为测试；逐条合成语料对照 |
| 组件测试 | Vitest、Testing Library、user-event | 迁移时引入；本轮没有安装或运行这些组件测试 |
| 实际浏览器 | Playwright | 验证本地浏览器、真实 SSE 与 Web 接续；原生 WebView2 另测 |

状态管理保留局部 React 状态；F2 在自建可订阅小型 store 和 Zustand 中二选一，不叠加第二套缓存。设置表单先不引入表单库；会话导航使用虚拟列表，消息区使用分页与 `content-visibility: auto`，v8.1 不虚拟化消息内容。使用开发状态页而不引入 Storybook；路由使用 query 小型路由，不引入 React Router。

业务对话组件自行维护。不能让现成聊天 SDK 直连 Provider，或接管 Run、审批、重试和消息记录。

## 6. 关键集成发现

### 6.1 CSP 与同源资源

产品当前为 `script-src 'self'`、`style-src 'self' 'unsafe-inline'`、`connect-src 'self'`，字体也仅同源。生产构建的小样在这一策略下运行成功，没有脚本或样式 CSP 错误。

Radix 的 Portal/定位和拖拽面板会使用样式变量/内联定位；这与当前 style-src 相容。无需因此开放外部脚本、字体或 `unsafe-eval`。Vite HMR 的开发便利不能成为扩大生产策略的理由。

### 6.2 路由与启动凭据

当前 Go 静态服务没有单页应用任意深路径回退；本地启动又用 URL hash 临时接收 capability，随后清除。

首选根路径配合明确 query 参数表达页面，例如 `/?view=settings&page=providers`。路由值白名单处理，启动凭据继续由独立 bootstrap 消费；不直接引入会抢占 hash 的路由。若后续改 pathname 路由，必须同时实现并测试 Go fallback。

### 6.3 关闭和 reconnect

React 挂载/卸载、切页面和 StrictMode 会影响订阅生命周期。应用根只管理一个逻辑 SSE 连接；业务页面订阅投影，切页不触发任务取消、Owner 停止或重复发送。

实际关闭通知仍走桌面/客户端适配。不能把普通组件 cleanup 当作整个客户端离开，也不能把浏览器断线当作 Run 取消。

### 6.4 依赖与源码扫描

2026-11-06 核查 npm 元数据，最新版 `@vitejs/plugin-react` 的 peer 范围要求 Vite 8。本轮保持 Vite 7.3.6，使用兼容的 React 插件 5.2.0，完成构建。

Tailwind 默认扫描在该输出目录小样中产生 60.17 kB CSS。改为只扫描 `src` 后为 22.90 kB，全部交互检查通过。产品必须明确扫描边界，动态状态用静态类名映射，不扫描报告、生成产物或整个工作区。

## 7. 隔离小样实际结果

目录：`output/v81-framework-evaluation`，未修改产品依赖、运行代码或生产服务。测试使用合成名称和占位 Key。

| 项目 | 实际结果 |
| --- | --- |
| TypeScript strict 检查 | PASS |
| Vite 7.3.6 生产构建 | PASS |
| 与产品一致的 CSP | PASS，当前 Chromium 下无 CSP 错误 |
| Dialog 名称、初始焦点、Tab 焦点限制 | PASS |
| Esc 关闭及返回触发按钮 | PASS |
| 设置草稿切页保留，秘密占位内容不写浏览器存储 | PASS |
| 深浅主题 token 切换 | PASS；不等同于完整对比度审计 |
| 分栏键盘和鼠标调整 | PASS |
| 所有资源同源，无外部字体/CDN | PASS |
| 最终 JS | 353223 bytes；Python gzip 为 111804 bytes |
| 最终 CSS | 22901 bytes；Python gzip 为 4973 bytes |

gzip 数字只是压缩估算；当前服务没有因此自动启用 HTTP 压缩。该小样不含 FreeAgent 完整消息渲染、真实模型或事件系统，不能与现有全应用直接做性能比值。

实际小样版本：React/React DOM 19.3.1、TypeScript 7.0.2、Tailwind 与 Vite 插件 4.3.4、radix-ui 1.8.1、react-resizable-panels 4.18.1；构建 Node 24.22.1、pnpm 11.25.0。版本是本次验证组合，正式迁移仍需检查锁文件和兼容契约。

早期脚本参数引号导致一次测试未执行，改用文件运行；第二次拖拽断言试图超过最大宽度，改成测试缩窄后通过。浏览器另给出小样密码框不在 form 内的提示，开发指南已要求语义化表单；这不计为完整表单验收。

证据：`output/v81-framework-evaluation/result.json`、`source.json`、锁文件与浏览器检查脚本；截图 `output/playwright/v81-framework-evaluation-final.png`。

**尚未运行**：原生 WebView2、最低支持版本浏览器、移动端布局、真实中文 IME、完整 SSE/Core 接入、全应用性能。这些进入 v8.1 产品验收，不用小样代替。

## 8. 实施顺序

1. F1 建立功能映射和目标栈可交互样例；用户确认视觉方向后冻结设计 token。
2. F2 建立框架目录、认证 transport、类型、开发状态页与契约测试，验证 Vite 8、存储与消息分支模型。
3. F3 迁移对话、流式、执行、审批及控制面提案、引导/队列、附件与上下文，连接真实 Core 对照旧界面。
4. F4 迁移设置、Provider、能力管理、数据连接、公网登录、首次启动与后台命令，完成映射清单。
5. F5 逐项回归、性能/真实设备测量和开发指南实操，然后替换正式 dist，删除已替代的生产 UI 入口。

按垂直流程逐步迁移可以降低风险；最终交付一套前端。视觉样例只是审阅材料，不作为第二套长期产品。

## 9. 官方来源

以下均于 2026-11-06 读取：

- [Tailwind Vite 接入](https://tailwindcss.com/docs/installation/using-vite)
- [Tailwind 浏览器兼容性](https://tailwindcss.com/docs/compatibility)
- [shadcn/ui 定位](https://ui.shadcn.com/docs)
- [shadcn Vite 安装](https://ui.shadcn.com/docs/installation/vite)
- [shadcn 主题](https://ui.shadcn.com/docs/theming) 与 [CLI](https://ui.shadcn.com/docs/cli)
- [Radix Primitives](https://www.radix-ui.com/primitives/docs/overview/introduction) 与 [Accessibility](https://www.radix-ui.com/primitives/docs/overview/accessibility)
- [实际读取的 Dialog 源码目录](https://github.com/shadcn-ui/ui/tree/main/apps/v4/registry/new-york-v4/ui)

源码取自固定提交，完整 commit 记在小样 `source.json`；许可证已保留。框架建议由上述能力与 FreeAgent 当前约束推导，不把官方宣传当本项目的性能证明。
