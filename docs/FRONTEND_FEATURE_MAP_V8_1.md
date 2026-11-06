# v8.1 迁移功能映射与 F1 决定

基线：v8.0.0-dev.1；2026-11-06。原入口清单来自 `internal/chatui/src/main.mjs` 的 92 个实际 API 调用位置，完整位置保存在 [api-call-inventory.json](../design/v8.1/api-call-inventory.json)。本表是迁移和验收清单，不代表新界面已完成。

## F1 视觉方向

用户已确认 `design/v8.1` 交互样例方向：浅灰侧栏、白色阅读区、蓝色操作强调、行内精确审批与二级设置。人物业务不会改变这个界面框架。token 以样例为基线，细节允许在对比度、实际设备和内容适配检查中修正。

unslop 审阅参考 Damir Sokolovsky 的 [unslop](https://github.com/heydamir/unslop)，CC BY 4.0：产品为持续执行的单 Core 工作台；气质为平静、精确、有条理；空间取自代码编辑器和阅读工作区；重点是能核对的具体变更；统一细节是工具耗时与状态沿对话排列。按间距→颜色→图标→文案→字形→布局→细节检查，避免把每段回答都变成卡片或用装饰抢占阅读空间。未为工作台新增营销图片、循环动效或其他运行依赖。

F1 实测：TypeScript strict、样例生产构建、桌面/375px/390px/横屏布局、内存秘密草稿、分隔条键盘/恢复、工具耗时、行内审批、队列转引导、UNKNOWN 保持、模拟 IME229、主题与减少动画。首次抽屉关闭未恢复导航按钮焦点，修复后复测通过。F1 不能代替真实手机、原生 WebView2、生产 CSP 或正式前端性能。

## 入口与目标

| 当前入口/API 家族 | 新组件/页面 | 阶段 | 必须保留的验收 |
| --- | --- | --- | --- |
| `environment`、`connect`、`server`、`events` | bootstrap、平台适配、全局连接 store | F2/F3 | capability 清除、单 SSE、失联/换代/版本刷新；多个客户端不互相退出 |
| `chats`、`spaces`、`chats/:id/pin` | 空间/会话导航 | F3 | 默认空间、置顶、分页/搜索、重命名、归类、删除、长列表仍可发现 |
| `chats/:id` 及轮次 before | ConversationPage、消息分页 | F3 | 完整轮、原文、复制、分支、滚动锚点、跨客户端接续 |
| `chats/:id` 执行子路由 | Composer、ExecutionPanel、ApprovalCard | F3 | 提交幂等、流式、停止/继续/结束、精确审批/拒绝/stale，不乐观判成功 |
| `control/confirmation`、控制目录与提案 | ControlProposalCard、设置表单 | F3/F4 | 同一操作/摘要/版本确认，GUI 点击、CLI 提示与 AI 提案保持语义 |
| `conversations/:id/permission` | 输入器权限选择 | F3 | Full 显式确认，冻结 Run/后续默认区分，远程权限上限 |
| `attach/inputs` → 公网 `remote/inputs` | QueueList、GuidanceAction | F3 | GUI 默认队列、可转引导、采用/删除/暂停、输入键不重复 |
| `attach/branch` → `remote/branch` | MessageActions、BranchPreview | F3 | 复用现有分支记录，不覆写原消息，分支权限重新校验 |
| `attach/context` → `remote/context`、`chats/:id/memory` | ContextPanel | F3 | 窗口占用、手动/自动压缩、取消、固定内容与来源，UNKNOWN 不改为成功 |
| 附件上传/列表/下载/删除、`attachments/reply` | AttachmentInput、ArtifactList | F3 | 精确范围、不可变结果下载、身份校验、取消与失败保留正文 |
| `keys`、`keys/test`、`keys/:id/models` | ProviderSettings | F4 | 草稿失焦/切页不清空；秘密不持久化；测试不意外保存或启用 |
| `providers`、`models/enable`、`enable-batch`、`default`、`disable` | Provider/Model 表单 | F4 | 列型号、自动填充、添加/删除/默认，当前默认删除必须明确替代 |
| `models/context`、`models/vision` | ModelCapabilitiesForm | F4 | 手动声明与真实验收区别、上下文限制、图片能力不隐式扩大 |
| `settings`、`permissions`、`settings/command-run`、`file-access`、`tools`、`context` | 通用、权限/执行、工具、上下文设置 | F4 | revision 冲突、保存范围、启用命令/网络/文件根，不改变 Full/沙箱语义 |
| `mcp` 及 update/test/delete/restart | MCPSettings | F4 | 新导入不信任 readOnlyHint、字段校验、秘密映射、状态/日志 |
| `skills`、view/update/delete/trust | SkillSettings | F4 | 导入路径、可见来源、工作区信任、停用和删除分开 |
| `extensions`、update/restart/clear-data/trust | ExtensionSettings | F4 | enabled/required、设置/环境映射、数据范围、失败/恢复；详情扩展位置 |
| `channels`、credentials、平台状态、Telegram access | ChannelSettings | F4 | 模型凭据与 Bot 凭据分开；默认一 Bot 一 Core；名单/撤权不提升工具权限 |
| `public-settings`、password/security/recovery、public session/login/logout | PublicSecuritySettings、PublicLogin | F4 | Cookie 检测、CSRF、锁定/IP策略、同源回跳；公网不开放 attach 管理面 |
| `attach/commands` → `remote/commands` | CommandSessions | F4 | 状态/耗时/输出游标、停止/有限延长/服务启动、UNKNOWN 不重跑 |
| `attachment-policy`、`settings/database`、doctor | DataSettings、DiagnosisPanel | F4 | PG 状态和备份/诊断范围，附件策略、更新前检查有实际入口 |
| desktop bridge、布局/主题/本地偏好 | PlatformAdapter、AppShell | F2/F5 | 外链、附件下载、文件拖拽防跳转、标题栏、高对比度、IME、退出与恢复 |

## 可调项与覆盖方式

Core 已有 `/api/control/catalog` 和 CLI `control catalog`，目前可枚举操作，但部分输入 schema 不完整。F2 用一份小型字段映射记录操作 kind、保存位置、scope、界面位置、CLI/AI 方式及生效时机；字段缺少界面映射时覆盖检查失败。秘密输入、user-only 和未实现能力明确标注，不能因为支持通用 JSON 就声称 AI 可以安全调整任意秘密。

原 API 清单包含动态路径和本地/public 分支；实际迁移按调用位置逐条核对，不仅按本表的路径前缀判断覆盖。Go 事件常量清单和 JSON 样本由真实发送入口导出；新增类型时测试要求前端处理、刷新或明确忽略。

正式入口现已替换为 React 前端，旧主入口和样式已移除；经过测试的安全渲染/逻辑 helper 保留在 `frontend/lib/presentation`，不再交付第二套正式页面。实际 Core 浏览器回归涵盖核心、Provider、工具/审批、设置、接续和桌面桥；具体结果见实施记录。原生 WebView2 交互、真实手机和多账号渠道仍未运行，不能由字段映射或浏览器 double 代替。
