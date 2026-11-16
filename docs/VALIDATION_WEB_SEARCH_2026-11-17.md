# 联网搜索真实验收记录

- 日期：2026-11-17；开发工作树：`v8.9.0-dev.1`。
- 授权范围：用户提供的 DeepSeek、Tavily、博查、Brave 搜索凭据，以及指定中转的 GPT / Claude；Anthropic 官方 API 按用户要求跳过。
- 环境：Windows 本机，独立临时 PostgreSQL、工作区、Key 引用和会话；未修改 Hytron / 常驻 TG，也未向真实收件人发送消息。
- 凭据仅保存在本机用户绑定的 DPAPI 加密文件，测试子进程按环境变量读取；仓库和本报告不保存凭据。

## 1. 独立搜索

通过 FreeAgent 的 `internal/webaccess.Client` 调用真实服务。每家使用英文官网查询和中文查询，各请求最多三条来源，检查 URL、规范化结果和输出预算；没有自动重试。

| 服务 | 结果 | 实际观测 |
| --- | --- | --- |
| DeepSeek Search | PASS | 两次均返回三条来源；约 358–386 ms，规范化结果 2.7 KB 左右 |
| Tavily | PASS | 两次均返回三条来源；约 2.08–2.35 s，规范化结果约 2.1 / 6.8 KB |
| 博查 | PASS | 两次均返回三条来源；约 224–262 ms，规范化结果约 1.6 / 4.9 KB |
| Brave Search | PASS，附首次网络异常 | 首次 Go 请求在连接阶段中断；单独协议检查返回 200，随后 Go 两次查询均成功，约 1.05 / 1.54 s，三条来源；未在原请求上自动重试 |

这些数据只描述本机、当时账号与查询，不作为服务商速度排名或持续可用率。其余七家仍只有本地契约覆盖，没有真实账号结论。

## 2. 模型原生联网与完整 Core 流程

协议探测和完整 Core 验收分别记录。完整流程要求实际搜索、读取独立测试文件、回传结果、回答来源及测试标记，并在后续关闭搜索时仍能接续历史。

| 配置 | 协议探测 | FreeAgent 完整流程 | 采用方式 |
| --- | --- | --- | --- |
| DeepSeek 官方 `deepseek-flash` / Responses | PASS：返回 `web_search_call`、引用和 usage；与普通 function 共存及结果回传通过 | PASS：流式搜索 + `file_read` + 最终回答 + 搜索关闭后的续接；三个模型请求 | 可使用原生搜索；其他模型仍需按能力选择 |
| 指定中转 `gpt-6.1-sol` / Responses | JSON 搜索首次断连；另做普通连接检查与 SSE 搜索均通过，SSE 有完整终止事件及搜索块 | PASS：三个模型请求完成搜索/文件读取/续接，两个已回报搜索活动 | 中转显式选择原生模式；自动模式仍按既有规则使用搜索连接 |
| 指定中转 `claude-opus-5-5` / Messages，仅搜索或小工具集 | PASS：原生搜索返回 server tool/result、引用及用量；小型只读函数组合也能完成 | 见下一行，不能用简单探测替代完整流程 | 仅证明这些请求组合可用 |
| 同一中转 Claude，FreeAgent 完整内置工具集 + 原生搜索 | 请求确实包含版本化原生搜索声明 | 未通过：JSON 与 SSE 检查均读到了文件，但没有返回原生搜索证据；模型说明未实际联网 | 当前不把这个混合组合标成已验收，不默认优先它 |
| 同一中转 Claude + Tavily 独立搜索 | 使用普通 function 工具，不启用托管搜索 | PASS：三个模型请求，独立搜索调用与文件读取真实落库，后续关闭搜索正常 | 当前建议使用此路径 |
| Anthropic 官方 | NOT_RUN（用户指定跳过官方） | NOT_RUN | 不用中转结果代替官方实测 |

中转地址为用户指定的 `api4.baobaodns.com`。`/models` 当时返回 40 个型号，包含上述 GPT 与 Claude 名称。响应里的模型名属于接口回显，不能独立证明中转背后的实际部署身份。

Claude 混合检查中记录了实际出站工具类型：完整文件工具与 `web_search_20250305:web_search` 均存在。未发现 FreeAgent 漏发声明；目前只能得出该中转/模型/工具组合下没有完成原生搜索，不能笼统宣称所有 Claude 原生工具不支持混用。

## 3. 根据实测补齐的实现

1. DeepSeek 官方 Responses 使用 `deepseek_web_search` / `deepseek_read_url`，按官方文档匹配 `deepseek-flash`；保留完整原生输出块，不能误用通用 OpenAI 工具名。
2. Anthropic 基础原生搜索与普通函数区分；server tool/result 不转成本地可执行调用，引用转换为可点击来源。
3. 续接原样保留原生块中的数组内容、签名、引用索引与未知字段。原先将服务端结果当字符串解析会丢失/拒绝数据，已用回归用例覆盖。
4. SSE 处理服务端输入和引用 delta；已提供的服务端搜索/抓取用量进入现有 Attempt 的 ModelUsage，不新增数据库列。
5. 对模型明确声明已选择的服务端联网能力，并区分命令沙箱网络；声明本身不充当成功证据。
6. `pause_turn` 保留块并返回明确暂停结果，不伪装完成或隐式重放。自动暂停续接仍待后续实现，不把本次普通续接测试视为它已经通过。

规范依据：[DeepSeek 原生联网](https://api-docs.deepseek.com/zh-cn/guides/web_tools/)、[DeepSeek Search / Fetch](https://api-docs.deepseek.com/zh-cn/guides/search/)、[Anthropic Web Search](https://platform.claude.com/docs/en/agents-and-tools/tool-use/web-search-tool)。官方接口定义、实际中转行为和产品适配测试分别作为证据，不相互替代。

## 4. 内容质量边界

一次 DeepSeek 原生搜索回答“Go 最新稳定版本”为 `go1.27.3`，并附上 Go 下载页引用；同一验收期间，Go 官方 JSON 接口列出 `go1.27.4` 与 `go1.26.11`。因此该次回答在“最新补丁版本”上没有达到事实核验标准，不能因为工具调用和引用格式通过就当作内容正确。

询问最新版本、日期或准确数字时，应继续读取官方原文并对照。模型可能使用旧的搜索摘要、缓存或不完整页面，引用指向官方域名也不能独自证明该段结论正确。官方核对入口：[Go 下载数据](https://go.dev/dl/?mode=json)。

本次没有进行多服务商搜索质量排名、所有模型矩阵、长时间压测、真实手机公网和 Anthropic 官方账号验证。

## 5. 本地证据与后续

脱敏协议证据保存在忽略目录 `output/web-acceptance-20261117/`；首轮搜索测量的保留情况另列：

- 首轮 DeepSeek、Tavily、博查的结果来自本次对话中保留的工具输出，第 1 节记录其测量值。首轮合并 JSON 在 Brave 独立复核时被覆盖，没有声称它仍可独立回放；后续运行已改为分文件保存。Brave 的成功复核结果保存在 `search-adapters.json`。
- `native-adapters-initial-sse.json`：DeepSeek 首次完整 Core 验收和 Claude 混合差异。
- `native-adapters-relay-gpt-sse.json`、`relay-gpt-diagnostics.json`：中转 GPT 的协议与 Core 结果。
- `claude-native-x-api-only.json`、`claude-mixed-both-headers.json`：Claude 小请求组合的协议复核。
- `native-adapters-relay-claude-none.json`、`native-adapters-relay-claude-sse.json`：完整工具集下的原生搜索未通过记录。
- `native-adapters-relay-claude-api-sse.json`：Claude 使用 Tavily 的完整流程结果。

本地运行器不包含凭据明文；原生响应可能包含不可公开的协议签名，因此不把整个证据目录打进公开发行包。发布时只使用本报告和脱敏测试结论。

相关 Go 回归已在独立 PostgreSQL 下通过 `-race` 检查，覆盖搜索、Responses、Anthropic、思考配置与设置入口；前端 TypeScript、lint 和 116 项测试通过。原生块续接另用本地样本检查未知字段、引用和实际回报用量，未为重复取证再次调用付费 API。

最终代码的 `go vet`、Windows / Linux amd64 构建及 Windows 版本输出检查通过。五份相关文档的本地链接检查通过；对仓库 1,032 个非忽略文件按本次提供的凭据原值扫描，命中为零。

当前结果支持继续准备搜索开发版；不自动修改用户既有模型、联网默认值或生产环境。浏览器与 Computer Use 仍按后续独立扩展批次实施。
