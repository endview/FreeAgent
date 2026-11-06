# FreeAgent v8.0.1–v8.3 实施记录

开始日期：2026-11-06。实施范围与验收以[主计划](FREEAGENT_V8_0_1_TO_V8_3_DEVELOPMENT_PLAN_2026-11-06.md)为准；未运行的项目不计通过。

## GitHub 基线交付

- 已推送 v8.0 的 14 个本地提交与后续计划/开发指南，main 为 `81b00bb`。
- 已发布 [v8.0.0-dev.1 开发候选](https://github.com/endview/FreeAgent/releases/tag/v8.0.0-dev.1)，标签与两个便携包均对应程序提交 `0d55033`。
- 发布前重新核对 Windows 1097 项、Linux 766 项 manifest 文件及原始源码清单；无用户数据，5 项 Release 附件已上传并核对大小/状态。
- 既有验收和已知限制完整保留，未为这次文档上传虚报新的完整回归。

## v8.0.1

已实现，正在验收：SDK 工具契约、`doctor --export` 允许项诊断包、`doctor --preflight --backup` 更新检查。诊断不复制原始配置/日志/正文；更新检查读已有 Run/Attempt/命令和完整性校验后的控制日志，核对同一 Core 备份，不取得更新锁或擅自停止派发。

已运行：Python SDK 2 项、Node SDK 2 项及真实 Core/PG→stdio→工具→结果两种语言联测通过。诊断/更新前检查与现有 doctor 回归、执行计数和控制日志完整性检查通过。此前测试请求错误字段 `permission` 已改为实际 `mode` 并复测通过；相关 Windows 定向竞态复测通过（CLI / Harness）。

长期负载：Hytron 独立 `fa-v801-validation` 用户、`freeagent-v801-soak-20261106` unit、独立临时 PG 和数据，已启动 8 小时 TaskService 混合负载，验证聊天、去重、审批/拒绝、取消、备份和反复重开。尚在运行，不能计 PASS；它不覆盖真实 Provider、Web SSE 或命令进程长压测。首次准备脚本使用 Debian Python 尚不支持的 tar filter 参数失败，换成显式路径/文件类型校验后启动成功；未触及生产服务。

## v8.1

F1：`design/v8.1` 对话、工具耗时、审批和二级设置样例已运行；TypeScript strict 和 Vite 7 build 通过（初始 JS gzip 123.32 KB，仅样例，不能充当最终预算）。桌面/390px 截图已查看，窄屏改为列表→详情，新增主题、队列/引导和可调分隔条。

2026-11-06 用户明确选择“采用这个方向，继续迁移”，视觉方向已确认。[功能映射](FRONTEND_FEATURE_MAP_V8_1.md)已列 92 处实际 API 调用来源。F1 的 19 项浏览器交互/布局检查复测通过；原生 IME、WebView2、真实手机与正式应用性能仍 NOT_RUN。F2/F3/F4 正在实施；F5 未完成。

## v8.2 / v8.3（开工状态）

本节记录开工时尚未实现的状态；2026-11-07 已完成主体代码和多项真实验收，最新结果见文末。

## Hytron

本轮尚未更新常驻服务。用户已授权完成验收后更新；届时核对活跃任务、备份和当前原生 PG 数据，保留生产对话及凭据。

本轮只读预检：Core/渠道 active，Owner 正常、无活跃会话/命令。独立回归不修改常驻实例。

## 本轮补充核查（2026-11-06）

- GitHub v8.0 发布后 CI 暴露两个问题：命令后端不可用的测试预期错误；审批完成与查询交错可能混合旧视图和空闲执行槽。修正预期，增加有限重读的稳定快照与确定性竞态回归。旧标签不改写，不把本地修复冒充远端 CI 已绿。
- 快照在结构性事件期间重读，达到上限标记不稳定；model_text 不阻塞快照，流式快照纳入最新文字水位。原审批相关 Windows race 连续 10 次通过；新增快照定向 race 连续 5 次通过。新增会话分页/事件覆盖等定向 race 通过。
- 首轮 Hytron 长测在第 22 轮失败：固定 150ms 的提交超时可能先打断数据库准备，测试不能保证已进入模型。保留失败；改为模型开始信号后取消，启动独立 v2 长测。v2 当前第 89 轮/623 操作/9 次备份，无遗留 active/waiting/command，goroutine 3；不足 8 小时，仍 RUNNING。
- F2 采用 React 19.3.1、Vite 8.3.6 和 useSyncExternalStore；生产 CSP 下开发入口保持原认证/Origin 检查。生产构建不包含 gallery/开发标记，开发构建提供合成样本状态页。token/HTML 插入 lint、TypeScript strict、95 项逻辑/契约测试通过。首屏 gzip 约 136 KB 是当前中间产物，非最终性能验收。
- 事件使用 35 种轻量常量和实际发送入口 AST 覆盖检查；Go 导出真实投影合成样本，前端逐类明确处理策略。SSE 控制帧增加服务端版本，快照仍为权威。
- F3 新界面对独立真实 Core：Echo 收发、单份回答、切页单 SSE、清除启动 capability、刷新保留认证及生产 CSP 共 7 项通过。正在补工具/审批、队列/引导、附件、分支和上下文完整流程，不能用 Echo 替代这些验收。
- F4 Provider 对真实 Core 和本地受控模型列表：切页/失焦草稿、秘密仅内存、测试不清空、具体型号、默认关闭、自动填充、设默认、删除默认、删除 Provider、gallery 复用实际组件等 13 项通过。实测发现空 providers=null 渲染崩溃，修复后复测通过。
- 会话元数据增加稳定游标分页/标题搜索，绑定工作区/空间/查询；315 条同时间会话、跨工作区隔离、游标错过滤及 LIKE 特殊字符的真实 PG 测试通过，不改数据库结构。
- 权限/文件范围/命令/工具/上下文、MCP/Skill/扩展和后台命令界面已接入现有接口，仍待完整交互回归。尚未替换正式前端或升级 Hytron 常驻实例。


## 正式前端迁移补充（2026-11-06）

- Windows 全量 `go test -race -p 2 -count=1 -timeout=30m ./...` 已完成通过（原始日志 output/v801-windows-race.log）；随后新增历史分页、设备存储与控制预算，已运行相关定向竞态，最终完整回归仍需收束。
- F2/F3/F4 新界面已切换正式 dist，移除旧主入口/样式。经过测试的安全渲染与逻辑迁入 frontend/lib/presentation，只有一套正式页面。文档改为实际目录/命令，增加依赖来源、字段覆盖和可重复浏览器测试。
- TypeScript strict、token/HTML lint、96 项逻辑/合约/对比度测试通过。测试数量由 98 调整为 96：原旧配色的 7 项检查替换成新语义 token 的 5 项检查，保留对比度和最小字号约束；不删除安全检查使迁移过关。
- 生产 Go 服务 + 临时 PG：核心 7 项、Provider 12 项、工具/审批/队列/附件和去重 14 项、设置 8 项、分支/搜索/接续 8 项通过。Chromium/Firefox/WebKit 各 6 项窄屏检查通过。测试使用合成 Provider，不冒充真实 DeepSeek。
- 后补的代码/公式复制、回答 Markdown 下载和受限桥右键/粘贴/外链/主题测试通过；桥测试是浏览器 double，不能算原生 WebView2。Windows clipboard 换行正常化导致初始严格字节比较失败，改为等待写入并统一 CRLF 后复测。
- ControlPreview 修复实际 operation_id、会话绑定和具体值差异展示；Run 内提案从历史回到原对话。扩展预算复用 runtime.set_limits，更新输入 schema、快照和精确撤回，相关竞态通过。
- 发现并补齐原入口：会话重命名、Owner 私聊授权、空间当前对话、文件选择、来源/背景展示、变更历史/撤回、扩展移除、纯文本日志、窗口提醒、回答另存为 Markdown 和原始代码/公式复制。辅助 purpose 文字不进入普通回答。
- 长历史基准已建立：一万会话、五千轮，API 每组 200 有效样本。第一轮持续流式脚本受普通上下文限制和计时器频率影响，未满足 30 片段/秒，最终失败；不能计性能 PASS。只为隔离基准设置较大上下文/关闭压缩、校准发送节奏并按 90 秒完成的模型段运行，正在复测；不改变产品默认限制。
- 原生 WebView2/IME、真实手机、完整公网/真实 DeepSeek/最终归档仍未完成。v8.2、v8.3 仍未完成；没有升级 Hytron 常驻实例。


### v8.2 验收准备及已发现问题

- 固定基准完成：10,000 会话/5,000 轮，列表/搜索/历史各 200 样本 p95 约 30.8/12.8/12.0 ms；当前页打开约 408 ms，加载到 224 轮后输入约 23 ms。600 秒流式约 17,805 片段（分 7 段完成的模型调用，避免改变产品单调用超时），575 次按键 p95 8.7 ms，停止约 984 ms，JS heap 约 10–37 MB 回收波动。报告位于 output/frontend-benchmark-fixed/core-zfmdmzk8/browser/performance.json。没有用 JS heap 代替整个 WebView2 进程组。
- 列表查询 CTE 候选优化没有可重复收益且其他项受并行测试争用影响，已撤掉；不虚报优化百分比。保留原分页 SQL。
- 新增 3 类 × 5 次连续压缩的确定性流程通过，原始历史保留。真实 DeepSeek 同矩阵首轮：research 通过；coding 因测试未收窄 None，发生额外工具消息，不能以固定消息数断言原文损坏；operations 第五次摘要 length，旧摘要正确保留。
- 修正测试的 None 权限。摘要请求原先直接按 byteLimit/3 设置 token，不能可靠对应英文/混合文本；改为有界 token 配额，仍受单请求输出上限和返回 UTF-8 字节校验，旧摘要在失败时保留；相关压缩/预算回归与真实重跑进行中。
- 真实 DeepSeek Chat JSON/SSE、协作、工具/审批/拒绝、受控中断、模型替代保留原 UNKNOWN、自动压缩首轮通过。生产进程、配置和凭据未改，凭据未进入输出；Responses 模型在当前生产配置未声明，不能推断托管搜索能力。
- Windows 最新全量 race 发现两项旧 CLI JSON 断言尚用数组，而新 sessions/history 已按计划返回分页对象；修改为检查分页对象及原始内容，三个相关测试定向 race 通过。其余全量结果尚在收集。
- 原生测试只确认独立 Windows 桌面可启动/正常退出；Computer Use 两种入口初始化均报系统路径错误，UI 操作 NOT_RUN。无原生截图/IME/缩放通过声明。
- 增加标准库 Go 扩展示例，样例 race 和真实 Core/临时 PG/stdio 联测通过。它不等于 AffectWeave 已完成。AffectWeave Python 参考提交 d8be9d3255006c51a96c114e36949771ae26517c 的 28 项参考测试通过，业务迁写尚未开始。


## 2026-11-07：v8.1 / v8.2 收束与 v8.3 实装

- 正式 React 入口已替换旧前端；92 处原调用都映射到存在的正式组件，并标明实际验收范围。有意取消“保存 Key 自动启用 Provider”，保持显式开关。没有长期保留两套正式页面。
- Vite 8 两次生产构建文件完全一致。TypeScript strict/noUnused、lint、96 项 JS 合约/逻辑和三引擎浏览器检查通过；最新首屏 JS 约 141 KB gzip，人物管理页独立按需加载。
- Windows 本轮全量 `go test -race -p 2 -count=1 -timeout=30m -skip 'Live|RealProvider' ./cmd/... ./internal/... ./examples/...` PASS，日志 output/v83-windows-race.log，Harness 839.267 秒。最后新增的来源恢复/原 UNKNOWN 核对有针对性竞态复测；不把旧源码全量结果冒充最后补丁的全量。
- Debian 首轮全量只失败于已修正的两项旧 CLI 数组断言；最终 CLI 全包及新增压缩/分页/Go 示例竞态复测 PASS。v8.3 完整隔离回归另在执行，结果随后归档。
- DeepSeek 编码/研究/运维三类各连续五次压缩 PASS，共 15 次摘要、48 次普通请求，各场景原始 32 条消息保留；保留首次失败和 token 配额修复证据。
- AffectWeave 已在独立仓库完成 Go 规则、PG 存储、事务幂等/CAS、NDJSON 和有界人物片段。原 Python demo 移至 reference/python 历史目录，固定 5 类数值 fixture；生产不运行 Python/SQLite，不引入 Go SDK。
- Core 增量为现有扩展协议的稳定已采用输入身份、state 接口、manifest 标量设置白名单、独立受限 PG 初始化、本轮 variant 和原结果核对。输入 ID 包含会话，避免各会话序号重号。排队未采用内容不推进状态；引导保留实际采用时间。
- 状态写回丢 ACK 时原 ControlOperation 与 UNKNOWN 保留。只读查询扩展事务结果后，复用原验证 receipt 结束提案 Run，不重跑模型、工具或状态操作；直接操作与模型提案两条故障回归通过。
- 人物启用空间明确列出；私聊/群/参与者状态隔离。None 可使用显式启用的 passive_context，但 manifest 和 Host 均禁用模型/工具/委派/私有历史能力。扩展角色无权连接 Core 库。
- 配套备份/恢复使用原生 PG dump、Core 备份和原控制日志，核对原输入来源；只有限暂停人物写回。普通 Core 执行继续；缺源失败不发布完整包、暂停释放，恢复后状态连续，模型计数不增加。
- 人物 GUI 十项真实 Core/PG 浏览器检查 PASS：默认关闭、显式初始化、草稿保留、预览不写、批准、新轮人物选择、一次采用、拒绝不改、配套备份和无运行时错误。GUI/CLI/AI 复用同一持久确认；原生 WebView2 不由该测试代替。
- Maya、Nova 两个角色各三轮真实 DeepSeek，在 Windows 本机与 Hytron 隔离环境均 PASS，含扩展进程重开和 usage；凭据只在测试进程内存，日志扫描未出现秘密，生产配置与进程未改。
- 新增分页 `ext sync` 只补读原 Run/交付事实，游标绑定角色/会话，模型或工具不会重放；大量停机遗漏不依赖内存 SSE 水位。

尚未完成最终 GitHub v8.3 Release、最终归档验收和 Hytron 生产升级。8 小时隔离长测仍以真实结束结果为准。原生 IME/缩放/高对比度/手工 WebView2、真实手机、当前未配置的 Responses/托管搜索/其他 Provider、多账号群/Lark/QQ 继续 NOT_RUN，不记成 PASS。经验总结是计划中的独立后续可选批次，本次人物业务不兼任它。


### 最终范围补充

- v8.3 Hytron Debian 12 独立全量 race 已 PASS：AffectWeave 全包及 Core/CLI/ChatUI/桌面 stub/PG/渠道/示例；Harness 669.836 秒。后补原结果核对、分页补读和启动来源校验另跑定向复测，不重写首轮记录。
- Windows 真实 DeepSeek 人物复验使用 SSE/usage 显式声明，两个人物各三轮，进程重开通过；秘密由私有 SSH 在测试父进程内存交给子进程环境，没有文件、命令参数或日志凭据。
- stateful 扩展重启首次注入前核对其持久来源清单；领先缺源拒绝注入。输入来源/版本/参与者/群空间隔离、分页补读游标和不重放的竞态测试已补充。
- 原操作核对通过后保留原 UNKNOWN，追加原扩展事务结果证明，复用已有 attempt/verified receipt 并结束原提案 Run，不残留卡住的执行槽。
- AffectWeave 源码已推送独立仓库；Windows/Linux 可选归档各重复构建两次，文件字节一致，无 Go/Python/Node 用户运行前提。发布状态仍以实际 Release 为准。
