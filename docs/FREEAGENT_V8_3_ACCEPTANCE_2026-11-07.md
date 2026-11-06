# FreeAgent v8.3.0-dev.2 验收与交付

日期：2026-11-07，Asia/Shanghai。此版本是明确保留设备/外部平台缺口的开发候选，不宣称完整稳定环境认证。

程序源码：FreeAgent `d1f0633d1d7add59b0c352e1aa8625e135d4e38a`；AffectWeave `b81212504da48e0c62b86622c8100816bcb5961e`。主程序和独立扩展均以已提交、无用户数据的源码构建。

## 实际实现

- v8.0.1：修正 Python/Node 扩展工具契约，新增允许项诊断导出与更新前检查；检查不自动终止用户工作，也不是更新锁。
- v8.1：唯一正式 React/TypeScript/Vite 8 工作台；可调整布局、主题、二级设置、Provider 草稿与模型管理、空间/置顶/搜索/分页、审批与控制提案、队列/引导、上下文、附件和后台命令。92 处旧调用已映射；前端和扩展开发指南已更新。
- v8.2：建立长历史和持续流式性能基准，修复压缩输出 token 与字节预算的错误对应。保留原历史和失败前摘要，不以重复模型/工具执行恢复未知副作用。
- v8.3：独立 Go AffectWeave，可选人物/关系/经历和有界上下文。Core 只补已采用输入身份、有限状态接口、白名单设置、独立受限 PG、原操作核对和配套备份；关闭时没有人物进程或分析请求。无人物业务进入 Core，生产不运行 Python/SQLite。
- 状态修改绑定具体内容/版本确认；丢回执按原操作 ID 查询事务结果，不重发 apply。保留原 UNKNOWN，核对后追加原结果证明。恢复/补读只采用持久事实，不重跑模型或工具。

## 已运行检查

| 检查 | 实际结果和边界 |
| --- | --- |
| Windows 全量竞态 | 最终源码 d1f0633 的 GitHub 全量 race PASS，Harness 1060.742 秒；普通全量 894.386 秒，另含 CLI/ChatUI/桌面/渠道/PG/示例和 vet/build/notices。早期本机 839.267 秒结果仍保留，不用它替代最终 CI；扩展真实集成由显式提供二进制的独立测试验证 |
| Debian 12 全量竞态 | PASS；Hytron 独立用户/unit/数据，含 AffectWeave 与 Core；Harness 669.836 秒。最终相关定向 106.405 秒 PASS |
| GitHub 当前提交 CI | [Windows/Ubuntu 普通与 race 共四项全量 PASS](https://github.com/endview/FreeAgent/actions/runs/42266648888)；候选 1 Windows 的测试生命周期竞态已修，原断言保留 |
| AffectWeave 自身 | PASS；独立 PG 全量/race/vet、固定 Python 参考的 5 类数值 fixture；自身 CI PASS，数值容差 1e-12 |
| JS/类型/构建 | PASS；96 项 JS/契约/逻辑检查，TypeScript strict、token/HTML lint、Vite 8 生产构建；不含 gallery/maps，首屏 JS 140,821 bytes gzip |
| 浏览器 | PASS；真实隔离 Core/PG 的核心、Provider、工具/审批、设置、接续、桥检查；Chromium/Firefox/WebKit 各 6 项。桥 double 不是原生 WebView2 |
| 人物管理界面 | PASS；最终归档中的实际 Core/PG，10 项：默认关闭、独立 PG 初始化、草稿、预览不写、批准、选择、一次采用、拒绝、配套备份及无运行错误 |
| 最终包真实模型界面接续 | PASS；独立 PG/最终包/Edge/真实 DeepSeek，10 项：GUI 回复、刷新、CLI 同会话接续、GUI 收到 CLI 结果、回到空闲、每页单 SSE、CSP 与无运行错误；凭据仅进程环境，输出/测试文件扫描无 Key |
| 真实 DeepSeek | PASS；Chat JSON/SSE、工具/审批/拒绝、受控中断与模型替代保留 UNKNOWN；编码/研究/运维各 5 次连续压缩，共 15 次摘要，原文各 32 条消息保留 |
| 真实人物多轮 | PASS；Windows 与 Hytron 的 Maya/Nova 各三轮，含进程重开/usage；Windows 复验显式 SSE。该项不是 TG 多账号/群或原生 UI 测试 |
| 8 小时混合负载 | v8.0.1 阶段 TaskService PASS；28,809 秒、1,298 轮、9,086 操作、130 备份，结束无 active/waiting/command；取消产生的 1,298 条 UNKNOWN Attempt 是预期。终点 goroutine 1、heap 约 1.5 MB。不是最终 v8.3 所有补丁/浏览器/SSE/命令/真实 Provider 8 小时长测 |
| 最终 Windows ZIP | PASS；完整性、无随包用户数据、首次桌面启动、重开、引擎路径、HTTP、关闭保留其他客户端、CLI 初始化/对话/备份恢复等实际通过；路径含空格和特殊符号 |
| 最终 Debian tar.gz | PASS；普通用户在中文/空格/特殊符号路径启动，CLI/Web attach、去重、真实沙箱、Full/host、后台停止、Owner 退出、重启记录、UNKNOWN 与备份恢复通过 |
| 重复构建 | PASS；Windows/Linux 均两次 clean 构建，完整归档及源码清单字节一致；AffectWeave 双平台同样一致 |

Windows/PG 日志与测试版本分开记录。最初的数组断言、临时目录 ACL、压缩输出上限与首轮长测的固定取消时机错误均保留失败记录，修正后复测；未删除测试或放宽实际断言来掩盖产品问题。

## 性能实测

一万会话、五千轮；API 每组 200 样本，列表/搜索/历史 p95 约 30.8/12.8/12.0 ms。600 秒约 30 片段/秒流式，575 次输入 p95 8.7 ms，停止约 984 ms；JS heap 在约 10–37 MB 内回收波动，不等于整组 WebView2 进程内存。候选 SQL 优化没有可重复收益，已撤回，不声称凭空提速。

## 发布与更新

FreeAgent 候选 1 已发布并程序更新 Hytron；用户已确认原密码登录、公网逐步输出和 TG 回传，服务端核对真实 TG completed/finished/sent。候选 2 最终双平台包已验收、四项全量 CI 通过并发布；Hytron 已更新至同一候选 2：原 5 会话、19 Run、57 消息、21 Attempt 逐项保留，Core 身份、配置/PG/Key/公网密码文件及代理 socket 目录未变；Core/渠道 active，doctor 正常，HTTPS 200。更新前的在线备份已验证，并保留私有离线归档。AffectWeave 已发布 [1.0.0-dev.1](https://github.com/endview/affectweave/releases/tag/v1.0.0-dev.1)。

本次沿用原生 `Harness-pg-v1`，不重建数据库，不恢复 SQLite 兼容，不重新配对现有 Bot。程序升级保留 Key、当前 PG 数据和公网凭据；备份范围与恢复步骤见现有维护指南。

## NOT_RUN 与剩余范围

- 干净 Windows VM、完整管理员桌面流程、Windows 真实磁盘写满；CI 管理员目录 ACL 修复不替代这些设备流程。
- 原生中文 IME、125%/150% 缩放、高对比度、手工 WebView2；本轮原生启动/关闭通过，但 UI 自动化工具初始化报系统路径错误。
- 真实手机与完整跨设备矩阵；三引擎手机宽度检查不是实际手机。
- 当前未配置的 Responses、托管搜索、其他 Provider；不从 DeepSeek Chat 推断支持。
- TG 多账号/群/回调/附件及 Lark/QQ 真实矩阵；已有模拟/身份隔离回归不替代真人实测。
- 独立“经验总结”批次未在人物扩展中实现；共享知识治理、多 Core 自主调度和分布式能力也未实施。

UNKNOWN 只表示实际派发结果不确定的事实，不用它标记未运行的测试。

候选 1 Windows CI 的恢复测试在旧 supervisor 仍可写入时伪造新 Owner，这是不符合实际 Owner 换代的测试前提；修正为先排空旧 writer。原 Full/UNKNOWN 核查断言未改，Windows/Debian race 各连续 20 次 PASS（45.839 / 39.987 秒）。候选 2 两个归档重新验收通过并重复构建一致；最终包真实 DeepSeek GUI→CLI→GUI 的 10 项复验通过，生产执行代码未修改。

标签发布另外触发了同源码的自动 CI/复构；本报告引用已完成的四组主线 CI 和已实际完成的本地双次构建/最终包检查，不将仍在进行的自动复跑计作额外 PASS。版本 8.0.1—8.3 的主体以开发候选交付；上列设备/外部条件边界和独立经验总结批次仍明确保留。
