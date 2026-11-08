# v8.5.0-dev.1 实施与验收

> 这是 v8.5.0 的历史验收，不代表当前表达策略归属。v8.5.1 已将模式、默认值及自然段算法迁入 AffectWeave，并移除 reply.suggest；新行为须以对应修正验收记录为准。

更新：2026-11-08。本批接续 v8.3.0-dev.2，包含 v8.3.1 TG 基础、v8.4 思考与表达、经验总结 0.1 和 v8.5 交付。代码保留已有未提交改动；开发归档标记 `+dirty`。没有新增经验库、调度服务、Core 人物算法或默认渠道连接。

## 已实现

| 模块 | 实际内容 |
| --- | --- |
| TG | Goldmark AST 转 native entities；UTF-16/grapheme 安全分片；明确格式拒绝才降级；短效 typing；静态/动画/视频贴纸及预览；已确认的空间贴纸集合；同会话真实消息反应；匿名计数事实 |
| Provider | 具体模型声明的 DeepSeek/OpenAI Chat/Responses 参数；主回答/辅助默认与本轮选择；JSON/SSE 推理用量；来源绑定的私有协议接续，保留显式空字段；GUI/CLI/AI 配置 |
| Core 交付 | 自然分段；整组原子入队；文字/贴纸顺序与逐条回执；UNKNOWN 暂停尾段；停止/放弃的持久取消标记；迟到 429 不复活已取消消息；续送组让正在发送的组先完成；游标分页 |
| 一次性投递 | 固定文字或获准贴纸；具体时间、到期与目标；扩展规则默认关闭；静默/时区、额度、冷却、新输入取消、撤销和发送前复核；复用原 outbox |
| 扩展契约 | `delivery` 显式声明；`reply.suggest`、资源、提案/查询/撤回及 `delivery.check`；invocation 与范围校验；复核期间禁止模型/工具/委派/再提案；版本化 schema/开发文档 |
| 经验总结 | 独立可选 Skill 0.1.0；GUI/CLI/TG 选择来源和草稿；来源摘要、秘密检查、UNKNOWN/局限/验证保留；确认后发布手动 Skill 或私有 Memory；不自动启用/固定 |

关键补强覆盖：撤销授权与派发的串行边界、会话移动后拒绝旧目的地、停止尾段、放弃后迟到入队、部分发送续送相互等待、入队中断和 UNKNOWN 记录/暂停的原子性。附件预览复用当前 Run 的真实附件引用，协议层在工具结果组之后发送带来源说明的图像数据，不伪造新用户输入或新 Run。

最终 Linux 全量竞态还发现并行命令与模型执行绑定工具视图时，读取了未加锁的可变控制指针。已改为从一次加锁获取的不可变视图复制策略和控制指针，并去掉配置回退中的递归读锁；新增并发回归。失败轮次保留，修复后重新验证。

后续全仓 race 又暴露旧工作流取消测试的固定 100ms 启动假设：在低配额 CPU 下可能在派发前超时。测试改为扩展实际收到 `workflow.run`、报告开始后才取消，并同时验证派发前取消没有 UNKNOWN/Attempt。生产路径也补正启动失败后的返回值，返回已持久化状态和保存错误，不再返回旧的 `running` 快照。

## 实际验证状态

| 检查 | 当前结果 |
| --- | --- |
| Go/真实临时 PG 定向回归 | PASS：TG、渠道、交付、思考、经验总结、扩展 NDJSON、附件与配置映射；最新广覆盖 Harness 97.082s |
| Windows 相关竞态 | PASS：Harness 150.151s，ChatUI、渠道进程、TG/wire 均通过；最终控制指针、接续构建与工作流取消三项重复 5 次 PASS（38.794s） |
| Windows 全量 | PASS：`go test ./... -count=1 -timeout=30m`；最终源码 Harness 662.387s、整次 753.048s，包含接续加锁和工作流早期结算修复 |
| Hytron Debian 12 Go/竞态 | PASS（分包复核）：完整 Harness race 652.161s；控制指针与并行执行重复 10 次 57.937s、工作流取消边界重复 10 次 26.540s。其他包在前一全仓 race 中逐包 PASS；该命令因旧 100ms 取消测试失败，修正后对 Harness 整包重跑，未把原失败命令改称 PASS |
| 前端逻辑/类型/lint/build | 102 项 PASS；TypeScript strict、token/HTML lint、生产构建 PASS；首屏 JS gzip 146650 bytes（预算 250000） |
| 浏览器 | PASS：生产 CSP、对话流式、单事件流、历史/分支/接续；Provider/思考和投递设置交互 32 项已通过 |
| DeepSeek 官方真实请求 | PASS：Windows 的 Chat/Responses × JSON/SSE × 默认/关闭/low/high/max，及四组工具调用后接续，共 28 次请求；Debian 12 重复这些真实请求并通过完整文件工具、审批与接续验收（41.497s）。推理用量有报告；服务端真实档位仍不能独立观察 |
| TG 官方真实只读 | PASS：getMe、getStickerSet、getFile/download，静态图转换；没有 getUpdates 或任何发送操作 |
| Skill 独立包 | PASS：0.1.0 归档 CRC、逐成员一致性、合成示例与可导入内容 |
| Skill 发布历史目录 | Windows 的内容篡改项 PASS，创建目录软链项因权限 SKIP；Debian 12 两项均实际执行且 PASS，没有跳过 |
| 静态检查与秘密扫描 | `go vet ./...`、`git diff --check` PASS；发行源码常见凭据模式扫描未发现候选 |
| 便携归档、重复构建与新目录启动 | Windows 新目录两次无参数桌面启动及 CLI 预验收 PASS；Debian 12 非 root 便携预验收 PASS。最终归档的两次构建、完整性与新目录启动结果随交付目录中的 `PACKAGE_VALIDATION.md` 提供 |

源码检查与运行结果按上表分别记录，归档构建后的结果放在独立交付报告中，避免验包后修改源码清单。早期 C: 临时集群遇到磁盘满，以及未配置更长 timeout 时的 Harness 包总计十分钟超时，均是失败记录；没有当作 PASS。当前临时 PG、Go cache 与浏览器依赖使用专用 D: 测试目录。既有被自动审批拒绝删除的 C: 临时目录未改用其他工具删除。

## 未运行与独立后续

- OpenAI 真实思考：NOT_RUN，没有为本批配置有效的目标模型访问条件；已运行请求/响应契约测试。
- TG 真人发送/typing/reaction、动画与视频原样发送、真实匿名群事件：NOT_RUN，需要明确授权的测试收件人/群、相应素材及客户端观察。API mock、真实资源读取不替代这些结果。
- 真实视觉模型的贴纸理解：NOT_RUN；当前 DeepSeek 配置没有视觉声明。实际图像传输由合成 HTTP/PG 测试验证，不能宣称模型理解动画。
- AffectWeave 人物主动表达：独立仓库的后续事项。本批只完成公共 Host/交付契约，不自动打开小鸢或其他人物的主动发送。
- Lark/QQ、自定义/付费表情发送、完整动画解析和周期性社交不属于本批。

UNKNOWN 只用于已经派发但结果不确定的实际操作；上述未运行项不称 UNKNOWN。测试中的故障注入 UNKNOWN 已验证保留证据、不自动重发。

## 使用与发布

[渠道](CHANNELS.md)、[模型配置](CONFIGURATION.md)、[回复投递](REPLY_DELIVERY.md)、[经验总结](EXPERIENCE_SUMMARY.md)、[扩展开发](EXTENSION_DEVELOPMENT_GUIDE.md) 已同步实际入口。普通用户不用核对哈希，确认卡片和 CLI 摘要继续在内部绑定具体内容。

开发包不含用户数据库、附件、Key、Bot token 或私人角色；PostgreSQL 数据格式和 schema 未改变。现有 PG、聊天与 Bot 凭据保留。主动跟进、自主 reaction 和模型贴纸开关不会因升级自动开启。
