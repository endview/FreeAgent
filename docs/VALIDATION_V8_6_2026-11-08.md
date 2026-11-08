# v8.6.0-dev.1：轻内核边界整改验收

日期：2026-11-08。配套版本为 AffectWeave `1.1.0-dev.1`、经验总结 Skill `0.1.1`、渠道 wire `3`。原工作区和已有 v8.5 修改保留；不引入双数据库、后台学习、插件市场或新的工作流运行时。

## 实际实现

| 批次 | 结果 |
| --- | --- |
| A：经验总结 | 方法使用 Skill 的 `invocation: selected_sources`；GUI/CLI/TG 复用通用分析入口。冻结方法、来源、模型和请求幂等性；无工具、Memory、人物或普通完整历史注入。非同名 incident-review 不改 Core 即可使用。保存仍显式核对，停用后历史可读。普通 slash/skill_load 不能绕过选源限制。 |
| B：状态与存储 | 状态能力修订 2，主协议仍 freeagent-ext/1；显式操作范围、有限 payload、真实原 Run/已采用输入及声明摘要。Core 不解释人物字段和动作后缀。扩展声明单 schema、独立 PG 库/受限角色，更新预检核实实际 schema/属主，不仅相信绑定文件。配套备份 v2 在新目标恢复，不枚举 aw 表或清表。 |
| C：渠道依赖 | 默认 Core 仅使用中性 channelwire，不导入平台实现。渠道纯准备格式和资源，Core 校验后原子冻结整组，再逐项派发；纯准备可重算，实际发送 UNKNOWN 不重放。取消/撤权拦截迟到准备。界面区分准备和发送状态；无渠道配置仅显示发现说明，历史规则仍可管理。 |

人物表达设置和算法继续由 AffectWeave 提供。Provider 思考控制继续属于模型适配。PG 仍是唯一 Store；未安装/停用的能力不自动建业务库或启动业务进程。

## 实际运行的源码检查

数据库测试使用隔离的真实 PostgreSQL 15.19，AffectWeave 验收包含独立 Go 进程；渠道包含独立可执行进程连接受控 Telegram HTTP 服务，不能称为真实官方发送。

| 检查 | 实际结果 |
| --- | --- |
| Windows 完整常规 Go 测试 | PASS；cmd、internal、examples；Harness 758.733 秒、CLI 186.987 秒、ChatUI 82.055 秒、桌面 21.918 秒。显式排除 Live/RealProvider。 |
| Debian 12 完整常规 Go 测试 | PASS；独立非生产 unit，650.231 秒，显式排除 Live/RealProvider。 |
| Debian 12 受影响 Core 竞态检测 | PASS；193.258 秒；随后另对最终控制/配置/绑定修正补测，结果保存在交付记录。 |
| Windows Core 竞态检测 | Harness 广泛边界测试 PASS，217.292 秒；固定前端产物后，最终控制/来源分析/存储/回复与 ChatUI 补测 PASS（Harness 21.381 秒、ChatUI 2.064 秒）。其他受影响渠道/PG 包检查通过；没有匹配用例的包明确记 no tests to run。 |
| AffectWeave 真实 PG 全仓竞态 | Windows PASS（extension 3.465 秒、storage 3.512 秒）；Debian 12 PASS，整轮 6.588 秒。 |
| Go vet | Windows 和 Debian 12 的 cmd、internal、examples 均 PASS。 |
| 前端逻辑/类型/lint/生产构建 | 102 项 PASS；TypeScript strict 与 token/HTML 规则 PASS。生产资源无开发入口，首屏 JS gzip 146926 字节，预算 250000 字节。 |
| 真实 Edge + Core/PG/AffectWeave | PASS；人物/表达/精确确认/独立存储/备份/停用 19 项，通用来源分析与另一方法 7 项，普通 Core/CSP/刷新/唯一事件流 7 项。设置及接续等补验结果随交付记录列出。 |
| 默认生产依赖 | PASS；cmd/freeagent 与 internal/harness 不含 internal/channels、internal/channelplugin 或 AffectWeave。检查脚本已加入 CI。 |

源码主测试完成后新增的 schema 预检和用户持久设置校验以对应最终补测为准，不把较早全仓结果冒充最终每个字节都重跑全仓。

## 调查、修复与重测

浏览器检查发现并修复了两个真实问题：人物页把扩展的 role_id 设置误写成 resource；快速切换人物/扩展时保存请求可相互冲突。前者恢复扩展自己的设置字段，并在控制预览时校验已声明 settings_schema；后者等待上一保存完成，以 Core 返回值更新界面。修正后上述真实浏览器流程通过。

首次隔离 PG 回归受到 C 盘空间耗尽影响。停止并清理本轮隔离实例后，测试 scratch 和 Go cache 改到 D 盘，完整测试重新通过。旧可再生成 Go 缓存移到 D 盘保留，没有清理用户项目、对话或生产资料。

一次并行 Vite 构建使 Go embed 构建期间的资源列表失效；该轮 ChatUI 编译失败不算通过。冻结最终 dist 后重新运行相关检查，通过。最终归档构建期间不再重建前端。

## 默认成本

同机、同配置、独立真实 PG，以整改前后未裁剪 Windows CLI 各取一个样本：

| 指标 | 整改前 | 整改后 |
| --- | ---: | ---: |
| 程序大小（字节） | 31864320 | 30933504 |
| Owner 就绪（毫秒） | 280.04 | 269.23 |
| 空闲工作集（字节） | 20832256 | 20107264 |
| Owner 子进程 | 0 | 0 |
| 普通离线对话模型调用 | 1 | 1 |
| 模型消息 UTF-8 字节 | 1028 | 1028 |
| Memory/扩展片段使用 | 0/0 | 0/0 |

这是单次成本核对，不是统计基准，不能据此承诺稳定的启动或内存提升比例。独立测试 PG 由测试入口管理，不计作可选业务进程；发行包大小与本表程序大小不是同一指标。

## 更新与交付边界

配套更新 Core、渠道与 AffectWeave；状态修订和 wire 不匹配明确拒绝。保留 aw 数据与存储格式 1，停 Owner 后显式执行 storage-preflight 补全已有绑定。旧提案重新预览；已派发不确定写入先用原包核对。旧备份使用原配套包恢复，不能把旧备份直接覆盖更新后对话。

最终 Windows/Linux 归档、可重复构建、原生 WebView2 检查、Hytron 配套更新及数据连续性结果，记录在交付目录的 DELIVERY/DEPLOYMENT_VALIDATION 文件中。本文只记录已执行的源码阶段结果，不能据此推断已经上传 GitHub 或完成生产更新。

- NOT_RUN：本轮新的真实付费 Provider 请求、真实 TG 主动发送及用户客户端观察、真实手机、其他真实聊天平台。历史验收仍是历史证据。
- UNKNOWN：实际发送或状态写入不确定时继续保留原记录，不由模拟测试或模型判断补成成功。
- 尚未实施：AffectWeave 的自动分析、情绪交互、人格演进和人物主动动机；这些属于其独立后续方案。
- 原生扩展仍是用户信任的同 OS 身份程序；协议和受限 PG 角色不等于恶意原生代码沙箱。
