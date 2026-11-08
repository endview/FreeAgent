# FreeAgent v8.1 前端架构

实现位置：`internal/chatui/frontend`。React 19、TypeScript strict、Vite 8、Tailwind 4、shadcn 源码和 Radix；使用现有 Go 服务、TaskService 和 ControlService。发行包内只有静态资源，用户无需 Node。

2026-11-06 用户确认浅灰侧栏、白色阅读区、蓝色操作强调和二级设置。正式 `dist` 已切换到新入口，旧 `src/main.mjs` 和旧样式已移除。实际验收和未验设备见[实施记录](FREEAGENT_V8_0_1_TO_V8_3_IMPLEMENTATION_PROGRESS.md)，本文件不代替验收。

## 目录与职责

```text
internal/chatui/
  frontend/
    app/                    挂载前认证与主题、查询参数路由、外壳
    components/             审批、控制提案、分隔条和 Radix 基础控件
    features/conversations/ 会话输入、历史、引导/队列、上下文和能力
    features/messages/      安全 Markdown、原文和单消息错误边界
    features/settings/      二级设置、字段草稿、变更历史
    lib/api/                认证请求、错误、DTO 与关键响应校验
    lib/events/             每种事件的显式处理策略
    lib/store/              useSyncExternalStore 权威快照与单 SSE
    lib/presentation/       从既有实现迁入的受测逻辑/安全渲染函数
    lib/drafts.ts           表单内存、消息草稿和原请求键，分别管理
    lib/platform.ts         设备偏好与受限 WebView2 桥
    styles/                 FreeAgent token 与按需 Markdown 样式
    dev/                    合成状态页；不进入发行产物
    settings-map.json       Config 叶字段/Control 操作的界面目的地
  test/                     Node 逻辑、Go JSON 合约样本和状态测试
  scripts/                  lint、字体处理、真实浏览器验收
  vite.config.ts            唯一构建配置
  dist/                     Go embed 的正式产物
```

基础展示组件通过 props 接收状态；页面调用现有 API。Core 决定授权、审批和执行结果，前端不能自行把批准、生成完成或接收消息当作执行成功。没有 React Router、Zustand、TanStack Query、表单框架、Storybook 或插件 UI 运行时。

## 数据流与连续性

- 每个页面实例一条全局 fetch SSE；切页不重连。事件只表达进展，重连、代次变化、跳号或有界缓冲溢出重新读取快照。
- 快照提供 `generation / snapshot_seq / snapshot_stable / server_version`。Go 在结构事件交错时有限重读，流式文字在锁内合入覆盖水位；不稳定快照不能冒充已完成。
- 流式更新按帧合并，并以有界定时器兜底；恢复前台重新校对。辅助模型的 purpose 流不显示为普通回答。
- 提交丢回执时保留原请求键。刷新后先查原状态，不生成新键重放。消息分支创建新会话，不能回滚已执行的文件/命令。
- 已完成消息独立 memo；Markdown、KaTeX、高亮和完整设置按需加载。消息区原生滚动、按完整轮分页和 content-visibility；仅会话导航虚拟化。
- 单消息渲染错误退回原文。只有审阅过的 Markdown 组件允许 HTML 插入，HTML 输入禁用、TeX/高亮有界、链接协议受限；复制按钮复制原始代码/公式。
- 原文按需读取，不能把页面截断文本当完整复制或历史导出。CLI 普通 history 有界；完整 JSONL 导出需显式选择新文件。

## 草稿、秘密和客户端归属

Provider/渠道/MCP 秘密只保存在专用表单内存，不进入 localStorage、sessionStorage 或设备草稿。页面切换/失焦不清空未保存表单，保存冲突保留草稿。退出公网身份清理该身份的草稿。

浏览器消息草稿按 Core 环境存入本标签页 sessionStorage；布局按环境留在设备 localStorage。WebView2 端口可以回退，故通过白名单桥保存布局和允许的消息/附件草稿，目录是当前便携 WebView profile 下的 `freeagent-client`，按 Core 数据目录和工作区摘要隔离。没有跨设备偏好同步服务。

本地 capability 在 React 挂载前读取，随即从地址栏清除；本标签页保留授权用于刷新。公网沿用 Cookie/CSRF，不开放 `/api/attach/*`；只将既有 context/commands/cancel/inputs/branch 映射到服务端受限的 remote 路由。

## 设置、组件和扩展

全局设置集中在二级导航，本轮模型/权限/能力在输入区。所有可调项映射见 [覆盖说明](FRONTEND_SETTINGS_COVERAGE_V8_1.md)。布局是设备视图偏好，不能宣称修改其他窗口或执行权限。秘密输入、运行时元数据和只在本机可用的管理入口明确标注。

模型提出的持久修改复用具体内容、范围、版本确认；普通工具仍使用既有权限模式。历史中的 Run 提案回到原对话确认，不能从普通设置卡片绕开 Run 审批。撤回预览也绑定原资源版本。

shadcn Button/Dialog 源码固定来源及许可证保留，其他基础交互使用同一 Radix 包。应用外壳和像素分隔条自建；键盘和双击恢复可用。主题采用语义 token，UNKNOWN 与失败颜色/文字不同，禁止页面直接写调色板色值。

扩展详情由 FreeAgent 自己的前端代码有限注册并 lazy 加载。AffectWeave 复用扩展设置和本轮能力入口，不运行第三方提供的浏览器脚本。新增共享模块以实际复用点为依据，不预建组件平台。

能力入口的显示、停用后的历史保留和业务归属遵循[扩展开发原则](EXTENSION_DEVELOPMENT_PRINCIPLES.md)。可以保留简短的能力发现入口；完整配置页与本轮动作应按实际安装、启用和选择状态提供，后端继续独立校验。

## 开发与发行

使用 `pnpm dev` 生成开发磁盘产物，由带 `freeagentdev` 构建标签的独立 Core 同源服务。开发标记、绝对目录与 symlink 校验仍生效；不放宽 Host、Origin、认证或生产 CSP。普通发行构建始终 embed，不接受开发资源环境变量。

开发模式有 `?view=gallery`，使用真实组件和合成状态。生产模式移除此代码与标记。资源带内容哈希，assets 长期缓存，HTML/API 不缓存；服务端版本变化或 Vite preload 失败提示刷新。

依赖用途、许可证与体积见[依赖说明](FRONTEND_DEPENDENCIES_V8_1.md)。开发命令、增加设置/事件/组件的完整路径见[开发指南](FRONTEND_DEVELOPMENT_GUIDE_V8_1.md)。
