# v9 W1 验收记录

日期：2026-11-22。版本 v9.0.0-dev.1，源码 eec966586ad93ba944d3e9052ce941ca8997e8a3。**已发布并更新 Hytron。** [Release](https://github.com/endview/FreeAgent/releases/tag/v9.0.0-dev.1)。本记录不代表 v9 全部工作包完成。

- Windows 独立 PostgreSQL：`./cmd/... ./internal/... ./examples/...` 全量回归通过（跳过 Live/RealProvider）；渠道/Telegram/桥接、新增的落盘前背景丢弃、诊断作用域、明确拒绝不会阻塞轮询和 v3/v4 拒绝用例通过。
- 前端 116 项单元检查、TypeScript、生产构建与 token/lint 检查通过。真实隔离 Core 的诊断刷新、隐私模式和群区分在 1440/768/390 三个宽度通过，无页面脚本错误或横向溢出。
- 第一次 Windows 全量运行因 C 盘空间不足失败，隔离实例已停止；后续数据库和临时产物改用 D 盘，重新运行后才判定全量结果。
- Hytron 只读检查：当前程序仍 v8.9.0-dev.4；两个 unit active，活跃任务为零。目标超级群只留存三条消息；getMe 显示全局隐私模式开启，Bot 为普通成员，群未开话题。没有修改这些状态。
- 真人 TG 收发与群内确认：NOT_RUN。没有使用用户 token 启动第二份轮询，没有代发测试消息。

- Windows/Linux 各两次干净构建，归档与源码清单一致；Windows ZIP 与独立 Debian 12 的 Linux 包验收通过。Hytron 的非 root、独立 unit/端口/数据目录中，渠道/Telegram/诊断/桥接相关 Go race 测试通过，没有使用生产 Bot token。
- 五份 Release 附件的 GitHub SHA256 摘要全部与本地一致。Windows 归档 SHA256：`7b6191eb686a6d1175b425b59bc2d0de54b2b146bc58b743faf4e3eb7f1b7749`；Linux：`294b0ad10b6ac3ad1a8d593e1d43c1868b760de536aa7c423b1db0a284103e7e`。原生桌面外壳本批 NOT_RUN。
- Hytron 维护前再次确认空闲并验证产品备份，保留离线数据与原程序后平滑切换。两个 unit 运行匹配的新程序，TG 用 v4 重连，实时 getMe 观测已进入诊断。10 个会话、124 个 Run、853 条消息、615 个 Attempt 与 22 项授权/选择/输入元数据逐项保持，配置/凭据/扩展绑定文件哈希一致，schema 未变。公网 HTTPS 200 与 CSP 核对通过；首次 HTTP 探测失败后已重新核实，未把该次失败记为通过。
- 原群消息不回复的逐条历史原因仍不能由旧记录证明。本版提供之后的实测定位能力；没有改变平台隐私、群准入、人物、主动发送或用户的显式未选中扩展。
