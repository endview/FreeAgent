# 搜索策略与待确认操作恢复

本次修复对应 v8.9.0-dev.4，源于 Hytron 上一轮有八条执行记录的待确认任务。日期沿用 2026-11-17 的故障批次。

## 已确认原因

线上这轮保存的搜索方式和当前方式都是 `auto`，网络工具开启、无独立搜索连接，固定工具包含 `web_fetch`。读取精确预览时返回 HTTP 409 / `config`，并非用户实际修改了这轮配置。

`forRun` 从保存的运行快照重建工具策略时漏了 WebSearch；重新生成的工具清单因此丢失网页读取/搜索定义，与原快照比较不一致，误报配置变化。另有独立问题：审批后装配搜索请求用被裁减的 Config.Tools 判断网络开关，可能错误关闭本轮搜索。

修复从已有 Config 快照生成有界搜索策略，并沿用同一冻结工具策略的网络开关。继续保留精确快照比较、工具预览摘要、公网确认绑定及当前权限检查；没有删掉配置校验，也没有新增快照字段、数据库表或业务流程框架。

## 交互

预览读取失败时不再持续声称“正在读取精确变更”，显示“预览未就绪”，提供“重新读取预览”和用户明确确认后的“结束任务”。重读只读取预览，不批准工具；批准仍绑定具体 Run、Attempt 和精确内容。读取失败时清除可用审批及验证码状态，不能用旧预览批准。

已有已完成操作不重放，无法预览的操作也不直接当作拒绝后自动继续。输入草稿保留，移动宽度下按钮换行。通用设置审批保留原有流程。

## 验证

- 新增独立 PG 回归先在补丁前复现：自动/API 模式读预览均返回 ErrConfigChanged；修复后通过。
- 十组组合：旧配置、关闭、自动、API、原生模式 × 继承/对话关闭。保存审批期间改变后续任务的搜索策略，再按 Owner 重启方式重建服务：预览摘要不变，仅明确批准后写文件，后续模型请求的搜索路径不变。
- 网页读取审批可明确拒绝，保存拒绝结果，无 HTTP 派发回执，无真实网络读取。
- Windows Search、精确审批、真实配置漂移拒绝、公网认证/确认与权限回归通过 `-race`。
- 真实 Chrome + 独立 TLS PublicServer：真实登录后重试预览、刷新恢复、批准一次；再验证预览失败可明确结束，未批准文件没有创建。模型调用计数确认重读/刷新未触发新模型请求。
- 1440/1024/768/390/320 五种宽度无横向溢出，输入草稿保留；桌面与窄屏截图已检查。使用本机合成模型，不调用付费 Provider，不发送真人 TG 消息。
- TypeScript、token/HTML lint、116 项前端测试、生产构建与 Go vet 通过；首屏 JS gzip 171,312 字节。

浏览器检查复用 `TestPublicSearchApprovalRealBrowser`，设置与上批次 HTTPS 测试相同的 opt-in 环境变量，见[公网搜索验收](VALIDATION_SEARCH_PUBLIC_UI_2026-11-17.md)。真实手机与原生桌面窗口启闭本次未重测。

## 发布与部署

已推送并发布 [v8.9.0-dev.4](https://github.com/endview/FreeAgent/releases/tag/v8.9.0-dev.4)，程序、标签、两平台归档与 Hytron 对应提交 `14156833eb1deb9b6749ee08f409d8b0a55c3cff`。Windows/Linux 各两次干净构建的归档和源码清单相同，八个附件的 API 摘要与本地一致，匿名下载地址均返回 200。Browser 0.1.1、Computer Use 0.1.0 沿用上版独立原包，来源提交记录在 BUILD_VERIFICATION.json。

Windows 最终便携归档完整性、版本、初始化、Key、Web/CLI 与进程退出检查通过；原生桌面窗口启闭本次未重测。Debian 12 在独立非 root 用户、数据与端口下验证最终 Linux 归档，并完成 Public/Search/审批回归 `-race`，111.38 秒通过；不使用生产模型或 TG 凭据。

Hytron 更新前验证 Core 与 AffectWeave 备份；无活跃执行后平滑交接，升级与连续性核对耗时 5.722 秒，schema 2 无迁移。8 个会话、121 个 Run、726 条消息、554 个 Attempt、原配置、凭据、扩展业务记录及能力选择均保持。原待确认 Run 和八个 Attempt 逐项不变；升级后读取原操作预览从 HTTP 409 恢复为 **200**，仍等待用户决定，没有批准、拒绝、重放或结束它。两条旧排队输入仍暂停。

Core/渠道 active、doctor 通过、TG connected；公网 HTTPS/CSP/匿名登录态正常，入口 JS/CSS 与 Owner 一致。AffectWeave 1.6.0-dev.3 程序、配置与存储保持。本次没有发送真人 TG 测试消息。

[全量 CI](https://github.com/endview/FreeAgent/actions/runs/44044632588)仍在运行；已完成的定向回归和归档检查不代表全量 CI 已通过。

本机证据保存在忽略目录 `output/approval-snapshot-20261117/`，服务器独立验证和维护结果分别保存在 `/var/tmp/freeagent-approval-snapshot-release-20261117/` 与 `/var/tmp/freeagent-approval-snapshot-production-20261117/`。具体备份与回退说明记录在私有维护文档。
