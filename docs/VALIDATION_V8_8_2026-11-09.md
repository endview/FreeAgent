# FreeAgent v8.8.0-dev.1：模型思考与协议接续

这是[压缩可靠性统一计划](FREEAGENT_CONTEXT_COMPACTION_RELIABILITY_PLAN_2026-11-08.md)的第二阶段。第一阶段 v8.7.1 已先完成打包、便携验收并更新 Hytron；本版保留其压缩预算、恢复和跨轮抑制修复。数据格式仍为 Harness-pg-v1，没有新增表、列、恢复服务或扩展业务职责。

## 实际实现

- 新连接退出过时型号；官方声明与用户配置分开。保存的模型、Key、主回答/辅助选择与额度不随升级改写，也不自动探测端点。
- 原生 Anthropic 支持 adaptive、effort 与允许时的关闭。Provider 默认仍省略参数；Sonnet 4.6 默认关闭不等于 effort high，Opus 5.5 不支持关闭。请求不增加采样参数或强制工具。
- 有界自定义档位可显式声明并在 GUI 标为“未经官方核对”；已知官方禁用项仍拦截。旧配置可打开修正，新保存及派发前校验。
- 现有 `model.set_profile` 接受互斥的 `{id, reasoning}` 与 `{id, auxiliary_reasoning}`，不需要重发 Key 或其它模型属性；仍走原确认。CLI 支持只带模型 ID 和思考选项的更新。
- Anthropic thinking/signature/redacted 块、Gemini thought signature、Chat reasoning_content、Responses 私有项按产生它们的端点、Key 配置和模型接续；排除正文、摘要、扩展历史及经验总结。Kimi 等保留式思考在普通多轮对话中也回传，不以当前是否提供工具为条件。
- 请求档位与实际响应回显分别保存在现有 Attempt JSON；未返回时留空。第三方回显不证明原生执行，也不冒充实际 token 用量。

## 发布预设

以下是创建连接/添加模型时的建议，不是九家原生 API 真实验收结论。主回答默认省略控制字段；建议辅助档位只在明确添加模型时生成。完整来源与限制留在统一计划附录及代码的日期化 `reasoning_info`。

| 厂商 | 型号 / 协议 | 官方思考与辅助建议 |
| --- | --- | --- |
| OpenAI | `gpt-6.1-sol` / Responses | 始终思考，默认 medium；low/medium/high/xhigh/max；辅助 low；工具走 Responses |
| Anthropic | `claude-sonnet-4-6`、`claude-opus-5-5` / Messages | Sonnet 默认关闭、effort 默认 high，辅助关闭；Opus 始终 adaptive、默认 medium，辅助 low；Sonnet 不提供 xhigh |
| Gemini | `gemini-3.8-flash` / OpenAI 兼容 Chat | 不提供关闭；minimal/low/medium/high；辅助 minimal；不从旧型号推断固定默认强度 |
| xAI | `grok-4.7` / Responses | 始终思考，默认 high；low/medium/high/xhigh；辅助 low；保留加密 reasoning 项 |
| GLM | `glm-5.3` / OpenAI 兼容 Chat | 始终思考，默认 max；low/high/max；辅助 low；未发布 Coding Plan 专属映射 |
| Kimi | `kimi-k3.1-pro` / OpenAI 兼容 Chat | 始终保留式思考，默认 max；low/high/max；辅助 low；不发送 K2.x 的 thinking 参数 |
| MiniMax | `MiniMax-M3.1-Flash` / OpenAI 兼容 Chat | 新预设地址 `https://api.minimax.cn/v1`；始终思考、默认 max；low/medium/high/xhigh/max；辅助 low |
| Muse | `muse-spark-1.3` / OpenAI 兼容 Chat | 始终思考；minimal/low/medium/high/xhigh/max；省略档位无固定值；辅助 minimal |
| DeepSeek | `deepseek-flash` / OpenAI 兼容 Chat | 默认开启/high，可关闭；low/high/max；辅助关闭；已有主回答和额度不变 |

## 已运行与边界

| 验证 | 结果 |
| --- | --- |
| 第一阶段完整基线 | Windows/Debian 全仓竞态及必要补跑、真实 DeepSeek 对照、103 项前端逻辑与四组 Edge 通过，详见 v8.7.1 记录 |
| Windows 第二阶段针对性 Go/PG `-race` | Harness、CLI、ChatUI 三包通过，430 项 pass、2 项 skip、0 fail；最终普通多轮接续和预设调整另行补测通过 |
| Debian 12 第二阶段针对性 Go/PG `-race`、vet、核心边界 | 独立用户、unit、PG 和目录通过；426 项 pass、5 项 skip、0 fail；最终接续/预设补测也通过 |
| 协议与持久化 | Anthropic JSON/SSE 的 adaptive/effort、空 thinking 字段、签名与已隐藏思考块的顺序，Gemini 工具签名，Responses 加密项来源绑定、请求/回显区分，既有消息和 Attempt JSON 重开后保留，摘要隐私均覆盖 |
| 前端 | 107 项逻辑测试、TypeScript、lint、生产构建通过；首屏 JS gzip 约 147 KB，低于 250 KB |
| 真实 Edge | core/provider/settings/continuity 通过。初次 Provider 组因字段文案更新而停在旧定位器；更新定位器后 Provider/设置/接续完整补跑通过，不把初次调用记作全通过 |
| 真实 DeepSeek | 隔离合成历史压缩、设置提案、Chat JSON/SSE 默认/开关/档位和工具接续通过；最终无工具普通多轮 JSON/SSE 接续也通过；不使用生产聊天作测试素材、不发生产 TG 测试消息 |
| 其它厂商原生端点 | **NOT_RUN**：没有本次可用的原生凭据；本地协议样例和历史中转结果不代替原生验收 |
| DeepSeek Responses | 第二阶段未配置 Responses 验收型号，真实相关子项明确 SKIP/NOT_RUN；未把 Chat 成功扩展为 Responses 成功 |
| 设备/真人 | 2026-11-09 用户确认 Hytron v8.8 的 TG 与公网 Web 聊天均正常，完成部署后真人收发复验；未据此推断具体设备或浏览器。真实手机、原生 IME/WebView2 手工复查仍 NOT_RUN；Windows 随包桌面首启/重开由便携验收实际执行 |

归档重复构建、最终便携验收与 Hytron 程序切换按本版提交执行，回执保存在本机忽略目录 `output/compaction-reliability-20261109/` 及私有部署记录。升级逐项核对原记录、凭据、配置、人物数据、TG 与 HTTPS 健康，不用构建成功冒充部署成功。

仍需结合模型实际输出额度使用辅助设置。最低思考档也可能耗尽 token；程序不会自动加额度、换模型、重放副作用或在硬预算已满时强行派发。官方默认、公开文档和真实端点可用性可能继续变化，未核实项保持未核实。
