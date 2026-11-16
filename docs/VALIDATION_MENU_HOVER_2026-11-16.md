# v8.8.0-dev.11 设置导航与会话菜单验收

日期：2026-11-16（Asia/Shanghai）。用户已确认四组导航和空间管理设计。本版仅调整前端与交互，不新增 Core 或扩展业务规则，存储仍为 schema 2。

## 前端实际结果

| 检查 | 结果 |
| --- | --- |
| 类型、token/HTML lint、生产构建 | 通过；首屏 JS gzip 170,812 字节，预算 250,000 字节 |
| 前端逻辑与契约 | 116 项通过，包含旧管理链接映射和空间参数适用范围 |
| 主侧栏 | 底部仅保留设置按钮，三个管理入口进入设置 |
| 分组与图标 | 工作台设置、能力管理、连接与数据、空间管理；标题 18.2px/项目 14px，无前置组图标，22 个项目均有图标 |
| 空间管理 | 空间内容、回收站、保留的执行记录；空间内容包含会话列表和原空间设置，附件整理入口保留 |
| 菜单直达 | 会话多选跳转保留已选 ID，空间菜单/筛选保留目标空间；旧 manage/trash/retained/spaces 链接可用 |
| 折叠、搜索、返回 | 折叠组可搜索，前进后退正确定位，窄屏返回列表和重新进入可用 |
| 滚动 | 全部展开并滚到底部后切换回收站/保留记录，左侧 scrollTop 保持；内容独立滚动 |
| 会话流程 | 改名/Esc/保存/并发草稿、右键/Shift+F10、移动、回收/恢复/永久清理、多窗口、批量与空间删除默认勾选通过 |
| 设置流程 | 实际保存与刷新回读、上下文自动/手动目标、网络/公网、投递提案确认/拒绝/失效、草稿切页保留通过 |
| Echo 与连接 | 真实 Core Echo、刷新保持授权、生产 CSP、单一全局 SSE，通过 |
| 视口与主题 | 1440×960、1200×800、1024×768、768×1024、390×844、320×568、844×390；深色及 CSS 200% 缩放无横向溢出 |

使用本机 Edge、独立 PostgreSQL、合成会话，不操作生产 TG 或真实人物资料。最终导航回归 12 项、管理 18 项、设置 19 项、核心对话 7 项，共 56 个检查项；结果文件记录实际通过项目及错误。

证据分别位于忽略目录 `output/playwright/settings-nav-release/`、`settings-nav-remaining/`；早期截图复核位于 `settings-nav-final/`。滚动回归在原型上也检查，底部点击前后均为 415px，原型修复不代替正式程序验证。

正式组件保留 React DOM，导航滚动与右侧内容分开。设置验收脚本按照组的 `aria-expanded` 展开，避免首次 CSS 加载时将已展开组误关闭。未使用强制点击来绕过隐藏控件。

## 检查边界

真实手机触控/软键盘、人工原生 WebView2 缩放和中文输入法、Firefox/WebKit 的本批完整设置流程、真人 TG 收发均未重新执行，记为 NOT_RUN。窄屏与触屏模拟、自动原生桌面启动不替代这些项目。

## 最终归档、发布与部署

已发布 [v8.8.0-dev.11](https://github.com/endview/FreeAgent/releases/tag/v8.8.0-dev.11)。最终源码、标签、发行程序与 Hytron 部署均为 `48d30db8a08a8a995e04d5a6ba26fc0a53a83810`。两平台从干净提交各构建两次，归档字节完全一致；五份 Release 附件上传 digest 及匿名完整下载 SHA256 匹配。后续文档提交不移动标签、不重写程序归档。

| 归档 | 字节数 | SHA256 |
| --- | --- | --- |
| Windows amd64 portable.zip | 58,139,597 | `242251c730c761e689c43cba6154a9a09e8fdc6bd76aa8bba34e39016ee7c70a` |
| Linux amd64 portable.tar.gz | 34,234,448 | `693190acb963b4bf98eb0b17168d54967c8ab7f695491ccf27ab6ed738fbb5fc` |

两平台源文件清单一致；`SOURCE_SHA256SUMS.txt` 自身 SHA256 为 `abddbc37d3bde4f538b7ed25e63e3df89bd64ab09b0fe621ce524121b38f725f`。

| 最终归档运行检查 | 实际结果 |
| --- | --- |
| Windows 原生桌面与便携 | 首启/重开/关闭、共享 Web 保持、完整性、CLI/Web、初始化、Key、错误 manifest 拒绝、提取二进制冒烟全部通过 |
| 独立非 root Debian 12 | 最终便携包、真实沙箱、备份恢复、重复回归、管理和 race 全部通过，合计 246.116 秒 |
| Linux 管理专项 | Harness 29.353 秒、chatui 13.589 秒、CLI 1.573 秒，通过 |
| Linux 相关 race | Harness 28.854 秒、chatui 10.317 秒，通过 |
| Linux 五次重复回归 | 40.280 秒，通过 |
| Linux 示例取消 race | 13.666 秒，通过 |

## Hytron 实际部署

维护前确认没有新活跃聊天/命令和待投递操作，完成已验证 Core 备份、独立扩展导出与停机一致副本，再使用 `cancel_active=false` 平滑交接。最终切换和检查耗时 5.739 秒，仍为 schema 2，没有再次迁移数据库或结束用户任务。

7 个会话、114 个 Run、692 条消息、526 条 Attempt，配置、凭据、授权、PG 身份、附件、能力选择逐项核对保留。两条 pending 输入逐字节保持且队列暂停；AffectWeave 1.6.0-dev.3 程序与业务表保持、全局启用且存储就绪，TG 会话明确未选中状态保持。没有生产会话被回收或永久删除。

Core/渠道 active、NRestarts=0，doctor 通过、TG 重连。HTTPS 200/CSP、匿名会话未登录状态、入口资源与 Owner 字节一致通过；另外直接核对公网按需加载的 `SettingsPage-uwW6brGp.js`，与 Owner 相同，包含四组导航和最终项目；CSS 实际包含 18.2px 标题和分组样式。受限 socket 挂载保持，没有修改 NPM 或其他应用。

本次没有发送真人 TG 测试消息或调用生产模型。备份路径和维护回执同步私有 VPS 文档；本机证据在忽略目录 `output/release-settings-navigation-20261116/`。旧 schema 1 回退仍需旧程序和同一时点备份，并先保全升级后的新增记录，不能只换二进制。

## GitHub CI 状态

[最终提交 Harness Tests](https://github.com/endview/FreeAgent/actions/runs/43883345438) 的 Ubuntu 标准已通过；收尾时 Ubuntu race 和 Windows 标准仍在运行。Windows race 在 `Prepare Windows PostgreSQL` 失败，日志为 `Pinned runtime download differs: vc_redist.x64.exe`，尚未进入 Go 竞态测试；同一提交 Windows 标准任务已通过相同准备步骤。

保留固定 VC 运行库哈希及可信的原运行时，没有将新下载字节静默接纳。针对失败任务提交重跑时，GitHub 因同一 run 的其他任务尚在运行而拒绝，未声称重跑成功。此次独立最终归档与相关 race 实测已通过，但不能将该依赖失败或仍在运行的全量 CI 标成通过。标签工作流单独按 GitHub 实际状态查看。
