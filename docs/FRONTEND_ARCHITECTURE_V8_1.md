# FreeAgent v8.1 前端框架

日期：2026-11-06。状态：目标架构草案；当前产品仍为 v8.0 原生 JavaScript。技术组合的小样结果见 [UI 框架评估](FREEAGENT_V8_1_UI_FRAMEWORK_EVALUATION_2026-11-06.md)。

本框架属于 [后续实施计划](FREEAGENT_V8_0_1_TO_V8_3_DEVELOPMENT_PLAN_2026-11-06.md) 的 v8.1 交付，不以文档存在代替实际迁移与验收。

按主计划最新第 5 节执行：F1 可交互样例经用户确认视觉方向后冻结 token，再进入 F2；审批与控制面提案在 F3 交付。F2 先验证 Vite 8，失败才固定 Vite 7，并二选一确定小型 store 或 Zustand。

## 1. 职责

前端负责展示 Core 的会话与执行投影，接收用户操作，管理本地界面状态。Core 负责权限、审批、任务、模型调用、上下文、持久设置和执行事实。

```text
Windows WebView2 / Web browser
             ↓
React AppShell / pages / FreeAgent components
             ↓
typed client + snapshot/event store + platform adapter
             ↓
existing Go API / TaskService / ControlService
```

开发使用 React、TypeScript、Vite、Tailwind CSS 和按需引入的 shadcn/Radix 组件；React 主版本和 shadcn CLI 固定，提交 `components.json`。生产由 Go 提供静态 dist，Node 不成为桌面或服务器运行依赖。使用自建外壳、query 小型路由，不采用 shadcn Sidebar、React Router、Storybook 或第二套服务端缓存；设置表单先不引入表单库。

## 2. 目录与依赖方向

目标仍位于 `internal/chatui`：

```text
src/
  app/                 bootstrap, AppShell, providers, route selection
  pages/               conversation, settings, capabilities, extension detail
  dev/                 gallery and sanitized fixtures, excluded from release
  features/
    conversations/     spaces, search, pagination, pins
    composer/          draft, attachments, IME, queue, steering
    messages/          markdown, stream, raw content, citations
    execution/         progress, attempts, commands, receipts
    approvals/         preview, confirm, decline, stale handling
    settings/          providers, permissions, context, connections, data
    capabilities/      skills, MCP, extensions, scoped selections
  components/
    ui/                reviewed shadcn/Radix source and small primitives
    freeagent/         RunStatus, ApprovalCard, ToolActivity, DraftForm
  lib/
    api/               transport, errors, DTO parsing
    events/            fetch SSE, cursor/generation reconciliation
    store/             stable subscribed runtime projection
    platform/          WebView2 bridge and browser alternatives
    rendering/         existing safe Markdown/math adapter
    storage/           explicitly allowed non-secret UI preferences
  styles/              tokens, global base, bounded markdown styles
  test/                fixtures, helpers, component setup
```

`components/ui` 不调用 API 或了解 Run。`components/freeagent` 通过参数展示领域状态。`features` 发起操作并订阅状态。`lib/api` 不依赖页面。跨 feature 的共享能力放在明确的模块，避免大文件改名后继续承担所有职责。

新增通用模块以第二个实际使用点为依据。保持项目内目录，不预建 monorepo、远程组件市场或多包设计平台。

分组就是既有空间，GUI/CLI 呈现为会话归类文件夹；空间内再按置顶和时间分段。扩展详情页在应用代码内有限注册并按需加载，输入区从 Core 能力列表渲染。AffectWeave v8.3 复用该位置，插件不能向前端注入任意代码。

## 3. 状态分工

| 状态 | 来源/位置 | 规则 |
| --- | --- | --- |
| Conversation/Run/Attempt/权限/审批/配置版本 | Core snapshot/API | 服务端权威；前端只能请求和显示 |
| 流式临时文字/事件游标/连接状态 | 应用级 event store | 快照校对；断线不构造完成或重发任务 |
| 页面、展开项、选中详情 | React 局部状态/路由 | 不影响执行事实 |
| 输入草稿/待提交请求键 | 按环境与会话隔离的 draft 模块 | 采用现有幂等提交规则；回执未知先核对 |
| Provider Key 未保存草稿 | 页面树外的专用内存 draft store | 切设置页保留，离开实例/登出清理，不持久缓存 |
| 布局、主题等界面偏好 | F2 核实来源稳定性后确定本地或 Core 受限客户端存储 | 只允许明确字段，绑定实例/身份/设备或客户端；不保存令牌、Key 或请求日志 |

F2 在自建小型 store（`useSyncExternalStore`）与 Zustand 中二选一，不叠加第二套缓存。运行投影订阅范围限制到当前 feature/Run，输入器、设置表单不随每个 token 重建。

只有用户自己输入的消息可以先行显示，绑定临时 ID 与原请求键，回执合并而不重复。发送按钮可显示“提交中”，但完成、批准、保存成功必须等待权威结果；审批和执行状态不做乐观更新。

草稿在组件外按表单/会话保存，避免 Tabs/Dialog 卸载丢失；滚动位置按会话保存。F2 核实桌面端口回退和 WebView2 数据目录：端口不稳定时不能依赖按 origin 隔离的 localStorage 保证便携连续性；采用稳定来源或受限客户端存储，不能只按客户端类型合并不同用户的数据。公网主动登出清理该身份草稿，秘密始终只留专用内存。

消息分支的数据模型在 F2 对照现有 Core 记录确定，不先假定切换同一条消息或新建会话。

## 4. 认证、路由与生命周期

- bootstrap 先解析并清除本地启动 hash；capability 交给 transport。沿用当前 sessionStorage 处理时也不能让它进入通用持久状态、URL 或日志。
- 远程 Cookie/CSRF 按现有 public API 处理，HTTP 错误转为稳定的用户状态。
- 本地与公网共用领域投影和前端数据类型，但请求仍经过各自认证与路由白名单。优先复用 `/api/chats`、`/api/events` 等受限接口，缺字段补现有响应；公网不开放 `/api/attach/*`，不另造公网执行层。
- 初版路径保持 `/`；query 参数表示页面和子页，采用有限枚举。环境/身份切换时清理旧实例状态。
- 根级 connection manager 管理 SSE，卸载页面只取消页面订阅。React StrictMode 的重复 setup/cleanup 不能产生双连接或副作用。
- `AbortController` 中断的是请求或订阅；Run 停止必须发出 Core 的显式停止操作并核对结果。
- 桌面文件选择、通知、外链、忙碌标志和关闭通知集中在 platform adapter。Web 环境提供相应交互或明确不可用原因。
- 页面刷新/关闭、待确认、后台命令、其他在线客户端与 Owner 保留规则继续通过真实生命周期验收。
- 核实刷新、HMR、睡眠/唤醒与多本地标签页的连接宽限；后台计时器不是存活保证。已有客户端确已正常回收时明确恢复流程，不自动重发任务。
- 只缓存带哈希的公开静态资源；index、API、认证和私有内容不缓存。通过握手/快照版本与 `vite:preloadError` 提示刷新；未保存 Key 时不自动刷新，先提示正常保存、明确放弃后重输或暂不刷新。只恢复按规则保存的非秘密草稿，不建立秘密草稿持久库；首次主题由符合 CSP 的本地 bootstrap 设置。

## 5. SSE 与流式内容

复用既有带请求头的 fetch SSE。一个应用实例保持一个逻辑事件连接，事件按 generation/seq 和所属会话合并；generation 变化时序号不跨代比较。F2 核实快照包含的事件水位与一致性，快照查询结束时的最大 seq 不能直接当作快照已覆盖的事实。

加载快照时有界缓存事件，应用后只处理水位之后的事件；重复事件幂等，跳号/过期/溢出默认重读授权快照。有需要才复用现有有限事件补读，不建持久事件库和通用回放协议。

实际发送入口使用轻量事件常量清单，覆盖测试核对前端处理/刷新/明确忽略策略；Go JSON 样本另做字段解析校验。样本不能证明未出现类型的覆盖，不建 Go/TS 全协议生成系统。运行时未知版本类型保留诊断并提示刷新，不使整页崩溃。

流式文字只更新当前消息，按帧合并频繁小片段，后台使用有界定时器兜底；定时器也被节流时，在恢复前台后重新校对快照。已稳定 Markdown 与未闭合尾部区别处理；未闭合代码块/公式先显示纯文本，闭合后再按需加载高亮/KaTeX，不强制反复完整解析历史。每条消息设独立错误边界。

流片段不作为持久最终回答；收到最终 snapshot 后采用保存结果。保留有界缓存、过期游标处理、恢复旧页锚点和原文复制。使用 `aria-live` 通知状态变化，不逐 token 播报。

会话导航使用虚拟列表。消息区使用完整轮次分页与 `content-visibility: auto`，不支持时回退普通布局；v8.1 不做消息区虚拟化，保留已加载内容的查找、跨消息选中复制和滚动锚点。浏览器 Ctrl+F 不涵盖尚未加载的历史。

## 6. FreeAgent UI 组件体系

### 基础组件

按需建立 Button、IconButton、Input、Textarea、Field、Dialog、AlertDialog、Popover、DropdownMenu、Tooltip、Tabs、Switch、Checkbox、Select、Separator、Resizable 和通知。

原生 HTML 能表达的小控件优先使用原生元素。需要复杂焦点/菜单行为时使用 Radix。Modal、Popover 的 Portal container 和层级统一，避免局部变换/滚动容器吞掉弹层。

消息区使用原生滚动容器；Radix ScrollArea 仅用于短弹层列表。运行中审批使用内联卡片，不抢输入焦点；业务模态用于用户主动发起的危险操作。窄屏导航抽屉按导航语义处理焦点与背景。

### 领域组件

- `RunStatus`：按真实状态提供名称、原因和可用操作。
- `ToolActivity`：显示执行中/已完成/失败/未知和实际耗时，展开 receipt。
- `ApprovalCard`：F3 实现工具和控制面确认，绑定精确预览、目标、版本及审批身份；stale 时返回重新预览；内联显示且不主动抢焦点。
- `MessageBody`：安全 Markdown、公式、代码与复制。
- `Composer`：中文输入、附件、当前能力、排队与引导。
- `SettingsSection`/`DraftForm`：字段分组、草稿、保存中、冲突、错误和生效范围。
- `CapabilityPanel`：使用 Core 的能力模型；AffectWeave 等扩展以明确配置页接入。

这些组件共享视觉语言，不共享未经授权的秘密或跨空间内容。Provider 全部既有操作、公网登录/锁定/Cookie 错误、上下文压缩、后台命令、首次启动和全局断连/PG 故障均列入页面功能映射。

## 7. 样式与设计规范

- F1 的对话/执行审批/设置可交互样例经用户确认后冻结 token；计划确认和历史小样通过不替代视觉确认。
- 用语义 token 表达 background、surface、foreground、muted、border、accent、focus；明确运行、排队、待确认、UNKNOWN、失败、取消、完成及读/写/命令/网络与 diff 颜色，Full 在输入区持续可见。
- className 主要承担布局与静态变体；颜色/尺寸不逐页面发明。复杂 Markdown 与可变尺寸可用有边界的 CSS。
- 动态宽高使用受控 CSS 变量；状态使用静态 class 映射，不拼接 `bg-${value}`。
- 使用 4px 间距节奏；中文系统字体、正文约 15px/1.7，禁止中文负字距，不打包中文字体；代码用等宽字体，页面设 `lang="zh-CN"`。
- Lucide React 显式导入图标。操作图标有名称；状态同时包含文字；暗色主题独立检验。
- 成功通知默认约两秒；需要处理的错误留在相关字段/执行卡片，关闭通知不清除事实。
- 面板按像素设计并设置上下限，核实库的像素约束；不足时自写分隔条。键盘调整、双击恢复默认；输入框自动增高，拖拽调整其最大高度。
- 窄屏使用侧栏抽屉、设置列表→详情，输入字号至少 16px，触控目标至少 44px；处理 visualViewport、dvh 与安全区。focus 保留 outline，forced-colors 下可见。
- 浅色/深色/跟随系统，首屏不闪错主题；原生标题栏跟随应用实际主题。
- lint 限制直接调色板色值和 HTML 插入，Markdown 渲染组件为明确例外。
- unslop 用于审核信息层级、间距和冗余装饰；最终视觉应以真实聊天与设置内容检验。

## 8. 安全呈现与依赖边界

Markdown 采用现有允许的链接/图片/HTML 规则和 KaTeX 限制。React 转义不能覆盖所有 HTML 插入风险；只有受控渲染适配器可输出 HTML，禁止业务代码把模型文本直接送入 `dangerouslySetInnerHTML`。

生产脚本、样式、图标、字体均同源随包。渲染依赖、组件来源和许可证进入发行清单。shadcn CLI 是开发工具；生产构建不运行在线组件下载。

扩展管理配置通过 Core API，插件描述数据不成为任意 JS/HTML 注入入口。AffectWeave 完整管理页也遵守同一结构。

开发环境使用受限 Vite 代理，或 `vite build --watch` 加开发 Core 磁盘产物；保持认证与来源边界，发行包不能包含放宽检查入口。生产 CSP 在真实 Go 服务下验收，运行时注入 style 的库逐项检查。

`?view=gallery` 只在开发构建存在，使用真实契约的合成/脱敏样本；发行清单验证页和 fixture 均被排除。F3 起新旧开发界面可连接同一个隔离 Core、以不同客户端标识对照，不镜像重放操作。F5 删除旧生产入口。

## 9. 迁移退出条件

v8.1 必须把现有功能清单逐项映射到新页面：普通对话、空间/历史、模型、权限、能力、上下文、附件、运行详情、审批、队列/引导、后台命令、渠道、公网、PG/备份排障。

框架验收包含：类型/构建、纯函数和组件测试、浏览器主要流程、中文 IME、失焦草稿、断连/重启/旧审批、长历史与流式性能、原生 WebView2 关闭接续。

实际包体积、冷/热首屏、流式响应、内存和可重复构建纳入结果；基准使用同一机器、固定数据及同一浏览器。隔离小样的成绩不能替代该退出条件。

F2 校准并冻结性能初值：首次交互前全部 JS gzip 总量不超过 250 KB，流式中输入到绘制 p95 < 50ms，打开 200 轮固定样本会话的默认页 1 秒内可操作。统计本实例 WebView2 全部子进程；预算调整说明原因。真实手机、公网 NPM 不缓冲、Windows IME/Safari 输入法、125%/150% 缩放、拖放/下载/外链、主题标题栏和多标签连接均按主计划 5.10 实测，缺条件记 NOT_RUN。
