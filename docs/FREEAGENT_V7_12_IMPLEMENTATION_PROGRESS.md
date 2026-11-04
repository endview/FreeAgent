# FreeAgent v7.12 实施与验收

日期：2026-11-04。候选：`v7.12.0-dev.1`。源码基线：
`f28a2a0d60e4eed2bcec51d74ed57ed346ee9664`。

四阶段产品实现、普通测试、针对性竞态检测、浏览器模拟渠道操作及两平台包级
验收已完成。本轮交付为未提交工作树构建，程序版本带 `+dirty`，manifest 明确
`source_dirty=true`；尚未提交、推送或发布 GitHub Release，生产 Hytron 未升级。
这不是已经完成真实 TG 新鉴权验收的声明。

规划依据：[v7.12 计划](FREEAGENT_V7_12_CHANNEL_ACCESS_AND_RUNTIME_CLARITY_PLAN_2026-11-04.md)。

部署前补充：Hytron 首次安装尝试发现同一 v10 数据库的旧占用索引在 Owner
更新前被严格 schema 校验拒绝，服务已恢复旧程序。修复仅识别该格式中确切的
原索引定义，仍先只读核验完整 schema 与完整性，再由独占 Owner 更新索引；
任意其他索引或旧数据格式仍拒绝。新增回归检查只读不写、更新保留 limited/
pending 及冻结快照、重复打开幂等、未知 schema 拒绝且不改文件。实际服务器
升级、回退点与最终检查另记于私有部署记录，不改写首次候选的历史验收口径。

## 实际实现

### 权限与命令

- `runtime_facts.go` 统一冻结 Run、保存默认值、原始发起人、实际后端、
  readiness 与即时阻断原因，供模型状态工具、GUI、CLI/TG `/mode` 使用。
- 未匹配命令规则按 None/Ask/Workspace/Full 处理。显式 deny、ask 及非空
  可执行文件白名单继续约束 Full；不把通用解释器标为只读工具。
- 冻结配置记录 `command_rules_version=1`。旧记录可以读取，但不能在新规则
  下直接续接；必须检查原证据并由用户明确开始新工作。
- 外部模型状态只投影相关空间和请求者需要的信息，不返回管理员申请列表、
  无关提案、确认凭据或其有效期。管理员新请求与 TG 原发起人续接不混同。

### 压缩与确认

- 新安装 `task_summary_bytes=4096`；已有省略/0 仍关闭。GUI 开关、摘要上限、
  CLI 和模型提案复用 `runtime.set_limits`，没有增加第二份配置真值。
- 有效可见历史先过滤、再计算输入预算；私有工具正文和渠道维护命令不触发
  无谓压缩。保留正在执行的工具组与所有原始历史，摘要调用计入原 Run 预算。
- 已准入 None 用户可直接 `/compact` 当前渠道公开对话；重复操作且没有新增
  可压缩内容时不多调用模型。维护结果保留真实发起人，不成为下一次压缩素材。
- `context_manage inspect` 不再把当前 Run 自身当作不可提案的原因；真正未决
  调用仍阻止压缩。GUI 和每请求最多一次的 TG 进度说明来自实际压缩事件。
- 独立直接设置提案绝对有效期 10 分钟，重新渲染不续期；到期清理临时秘密。
  模型提案继续依附 Run，验证资源后可更新确认凭据，旧会话/Owner 凭据失效。
  失效或拒绝同时收束待确认 Attempt 与 Run，UNKNOWN 提交不冒充确定失败。

### Telegram 新准入

- `/start`、群 `/start@Bot` 创建真实数字 Bot/chat/person 绑定的申请；8 位申请码
  仅帮助核对和定位，不能兑换资格。待申请为有界 Owner 内存：32 条、1 小时，
  去重不延长到期，60 秒通知节流，重启或 Bot 身份变化后失效。
- 管理页显示申请、数字身份、私聊/群、人员和空间上限及最终权限；使用页面内
  确认卡片批准，也可手动添加、停用、核对既有空间后授权。
- CLI `channel telegram access` 支持申请码/ID 和 `--confirm`；AI 只可提出
  `telegram.set_access`，持久化仍需管理员确认。批准和关联空间在同一事务中。
- 唯一准入状态复用 Store/meta。输入、斜杠命令、群背景、媒体、按钮、工具派发
  和出站统一检查。准入 revision 改变使旧 Run/按钮失效，重新授权不复活旧凭据。
- 撤权取消当前 Owner 的实际请求并停止相关命令；撤权提交与命令启动/发送
  互斥。已发生作用不伪造回滚，已派发且不确定的结果不自动重发。
- 停用保留空间与历史；删除空间同时撤权并按原规则将对话移回本地。
  `/status` 不暴露管理员私有未投递结果。重复保存相同准入为 no-op。
- 删除 TG 旧通用配对入口，不把旧配对自动转为准入。Lark/QQ 保留原配对，
  仅对受影响的共享实现做模拟回归，没有建设新渠道账号平台。

### 文件和限额收束

- 导出结果区分下载快照已就绪、等待渠道派发与最终投递回执；移除误导性的
  裸 `delivered=false`。提示明确提供下载附件，无需输出哈希或回写后重复导出。
- 同一 Run、同一规范化源文件复用相同内容快照，默认仅选择最新成功未发送
  版本；不同目录同名文件不合并。明确 `keep_version` 可保留多个版本。
- 选择与发送 claim 互斥。dispatching/sent/unknown 的版本冻结，旧快照和
  receipt 不删除，新导出失败不丢掉前一个成功选择。
- 普通步骤/时间/输出限额无未决调用、待确认或活跃命令时结算并释放占用；
  SQLite 仍保留 `limited`、原始原因与累计消费，没有新增暂停状态或独立表。
- 只有用户整条“继续”、`/continue` 或对应按钮创建一个事务关联的后继 Run，
  原发起人与当前权限上限保留，支持更窄临时权限、模型选择和附件。
  UNKNOWN/后台命令不能借此绕过，协作子任务仍受父任务累计预算约束。

## 实际运行的测试

| 验证 | 结果与口径 |
| --- | --- |
| Windows 普通 Go | 所有包通过；全量命令排除 `Live\|RealProvider`，最后修正无效渠道 binding 夹具后重跑 Harness 全包，182.566 秒 PASS |
| Go 静态检查 | `go vet ./...` PASS；`git diff --check` PASS |
| Windows 原生 race | Harness、ChatUI 针对性用例 PASS，分别 40.553 / 6.754 秒，无竞态报告；不是全仓库 race 声明 |
| Debian 12 普通 Go | 非 root `freeagent` 用户，最终 Linux Harness、ChatUI 测试程序全包 PASS，真实沙箱用例运行 |
| ChatUI JS | 86 项 PASS，0 失败、0 跳过；Vite 重建嵌入资源 PASS |
| 真实 DeepSeek Flash | 自动压缩长对话 PASS：1 次摘要、5 次普通调用，2 次后续请求使用摘要，10 条原消息保留，摘要覆盖 6 条、revision 1，None 权限；后续回答保留项目标识与 Markdown 交付要求 |
| 真实浏览器、模拟 TG | Edge headless 点击 PASS：身份确认、取消不授权、批准、None 上限、停用保留空间、重新授权、0/1024 压缩设置、375px 无横向溢出，JS 异常 0 |
| Windows 便携包 | 新目录、空格及 `& !` 路径、实际桌面首次启动/重开、唯一 CLI 引擎、HTTP、正常关闭、Key 路径、无捆绑状态、离线 Chat、无效 manifest 拒绝及提取后二进制 smoke PASS |
| Debian 12 便携包 | uid 990、16 项 PASS，含真实 Bubblewrap、显式 host、GUI/CLI attach 与去重、后台停止、Owner 停止清理、重启记录及备份恢复；该包级脚本不发真实 Provider/渠道请求 |

首次 Linux 测试布局把临时数据放进可执行安装授权根目录，触发了预期的沙箱
保护拒绝；改为独立 `bin/` 与 `tmp/` 后最终全包通过。首次 Windows 全量中，
旧测试夹具使用无效外部 binding 而没有可压缩公开历史；修正身份夹具后通过，
没有放宽可见历史过滤。早期渠道通知夹具也已补齐实际响应，不把失败日志删除
或当成最终结果。最终测试证据与这些早期失败分开保存。

本地证据在忽略目录 `output/`：`v7.12-native-final.log`、
`v7.12-harness-native-final.log`、`v7.12-debian-results-final.json`、
`v7.12-debian-harness-final.log`、`v7.12-debian-chatui-final.log`、
`v7.12-windows-race-final.log`、`v7.12-js-tests-final.log`、
`v7.12-provider-compaction-final.log`、`v7.12-windows-portable-preflight.log`、
`v7.12-linux-portable-preflight.json`、`playwright/v712-browser-acceptance.json`。
真实 Provider 凭据只在服务器子进程环境使用，不写入仓库或发行包。

## 交付和部署

两平台便携包使用固定 Go 1.26.5、当前嵌入前端和确定性归档工具构建，不携带
验证数据、模型 Key、Bot Token 或运行状态。最终交付另附两次一致构建、源码清单、
归档完整性和被验收二进制对应记录；补充本记录不改变被测试的产品代码。

升级前备份、处理旧命令 Run。已有 Telegram 空间不会被删除，但管理员需要在
新页面核对身份并授权，旧 `/pair` 码不能再放行。已有自动压缩关闭值不会被
悄悄开启。Full 仍是当前服务 OS 用户的 host 权限，不等于 root 或沙箱隔离。

Hytron 验证使用 `/var/tmp/freeagent-v712-validation` 独立目录和端口，没有
启动真实 Bot 的竞争 poller，也没有更改生产 unit、Nginx/证书、名单或用户历史。
本轮浏览器模拟 fixture 已停止。

## 未完成、NOT_RUN、UNKNOWN 与风险

- **未完成的外部验收**：v7.12 TG 新准入的真实双账号、群成员变化、真实按钮
  回调和文件回传为 NOT_RUN。缺少受控测试账号/群，不使用 v7.11 的真实私聊
  PASS 代替本版证据；实现和模拟验收已完成。
- **按用户要求延期**：Lark/QQ 新鉴权与真实账号验收；本版不以此作为阻塞。
- **未运行**：Linux 原生 race、Windows WebView2 内完整人工交互、视觉 Provider、
  当前候选的八小时长测及公网证书重新验收。桌面自动启动/重开和浏览器点击
  已实际运行，但不扩大为这些项目的 PASS。
- **UNKNOWN**：本轮真实 Provider 无结果不明；包级结果没有遗留 UNKNOWN。
  模拟未知派发、取消、重启和禁止重发用例已通过，未知事实不会被标为完成。
- **尚未执行的发布操作**：GitHub 提交/推送/Release 与生产部署。这些属于
  NOT_RUN，没有外部发布失败或权限阻塞；本次先交付可复核候选包。
- **剩余风险**：真实 TG 平台行为仍需以上外部验收；群消息发出后群成员可见，
  名单不能撤回已公开内容。未签名 amd64 预览；Linux 沙箱联网、宿主可见沙箱
  端口、Windows 公网入口和 QQ 任意文件上传继续不支持。

产品实现和本地/服务器模拟验收没有已知阻塞项。部署新 TG 准入之前，必须完成
管理员核对授权；真实多账号验收所缺的账号和群需后续具备条件再执行。
