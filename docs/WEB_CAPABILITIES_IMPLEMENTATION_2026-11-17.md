# 联网能力实施记录：第一批

- 日期：2026-11-17。
- 源码版本：`v8.9.0-dev.1`，尚未发布或部署。
- 方案：[联网搜索、浏览器与 Computer Use](FREEAGENT_WEB_SEARCH_BROWSER_COMPUTER_USE_PLAN_2026-11-16.md)。
- 使用说明：[联网搜索](WEB_SEARCH.md)。

## 完成范围

| 部分 | 结果 |
| --- | --- |
| S0 源码与边界核实 | 复用现有工具、控制面、Key、能力快照、Attempt、上下文历史回读；不新增数据库/调度器 |
| 独立适配包 | `internal/webaccess` 负责连接/查询/结果映射、公共地址检查和正文抽取，不依赖 Harness 或人物扩展 |
| 搜索工具 | 统一 `web_search`；11 家适配器本地契约覆盖；固定输出预算，无后台常驻进程或自动重试 |
| 公共网页 | `web_fetch`、脚本/样式过滤、下载与输出限制、DNS/目标限制、重定向核验；不继承搜索 Key、Cookie 或代理 |
| 设置 | 全局方式、连接、Key 引用、预算；新增 Key、保存/回读、草稿、修订冲突、固定查询测试 |
| 入口一致性 | Web、CLI 和 AI 使用 `search.set_policy`；会话 `search` 选择保存在既有能力快照中 |
| 权限 | 全局关闭不可由会话开启；None 和辅助请求不启用新搜索；解除引用后才能删除/改地址的 Key 保护 |
| Responses | 请求级启用与能力声明分离；载荷计量与真实请求使用同一选择；来源转成可点击链接，保留原生块 |
| N1 原生接入 | DeepSeek 官方 Responses 工具名与型号匹配；Anthropic 基础搜索、JSON/SSE 引用与完整原生块续接；按厂商保留已回报的服务端工具用量 |
| 原有交互修正 | 网络开关不再要求启用普通 HTTP；手机设置详情隐藏导航，仍保留返回列表入口 |

配置 `web_search` 缺省为 nil，用于保留旧版模型声明行为；新增策略保存为明确的 off/auto/api/native。后续所有设置继续从单一控制面保存，连接测试不直接改动受保护的 Key 文件。

新增直接依赖为 `golang.org/x/net v0.62.0` 的 HTML tokenizer，BSD 许可证；不引入运行时 Node、浏览器或桌面 SDK。它负责结构化 HTML 读取，避免用正则表达式去除脚本。最终包体积与许可证清单在发行打包阶段记录。

## 验证证据

| 验证 | 结果与范围 |
| --- | --- |
| 适配器/网络单测 | 11 家合成响应、认证头、错误、空结果、超限、无重试、禁止跨地址转发秘密、公共地址限制和 HTML 抽取通过 |
| 独立 Core / PostgreSQL | 模型提出搜索 → 本地服务 → 结果持久化 → 再次模型请求 → 回答通过；429 成为错误结果且不重试；下一轮显式关闭搜索有效 |
| 设置与控制面 | 保存/回读、旧配置兼容、修订冲突、网络开关、模型凭据隔离、Key 引用保护、控制操作 schema 与前端覆盖检查通过；编译后的 CLI catalog 实际返回 `search.set_policy` |
| 前端基础 | TypeScript、token/HTML lint、116 项前端测试、生产构建通过；首屏 JS gzip 170,986 字节，低于既有 250,000 字节预算 |
| 真实 Edge | 独立 Owner/数据目录；保存、测试后再保存、切页草稿、跨页后旧修订冲突与响应式检查通过，未出现页面运行错误 |
| 设置导航 | 四组标题、图标、全部展开后滚动、旧链接、深色与 200% 检查，以及七种窗口尺寸通过 |
| 真实公网读取 | `https://1.1.1.1/cdn-cgi/trace` 通过 DNS/TLS/正文路径，204 字节正文；不使用模型/搜索凭据，不记录响应正文 |
| DNS 环境 | `example.com` 被本机 VPN 解析为 fake-IP 特殊地址，被预期保护拒绝；不把这一项写成普通域名已通过公网验收 |
| 构建 | 最终代码的 Windows / Linux amd64 CLI 构建通过，Windows 版本输出为 `v8.9.0-dev.1`；未据此宣称 Linux 浏览器/桌面操作已验收 |
| 并发与静态检查 | `webaccess`、Harness、ChatUI 与 CLI 的搜索/Provider/设置相关 `-race` 回归通过；三包 `go vet` 通过；原生块最终修正后另补定向回归 |
| 文档与凭据 | 五份相关文档本地链接有效；按本次凭据原值扫描 1,032 个仓库非忽略文件，命中为零 |

较大范围回归曾得到：`internal/chatui`、`cmd/freeagent` 全包通过；`internal/harness` 有一次 `TestOpenTasksReplacesAdaptersAndDisabledToolsDoNotStart` 报临时 workspace 不存在。该项及本次相关测试随后独立回归通过，目前没有复现；不能把那次全包运行写成全绿。

最终浏览器证据保存在忽略目录 `output/playwright/search-v89-final/core-4r665_in/browser/`；这些浏览器检查使用合成 Key 和本机 SearXNG fixture。随后用户提供的真实 API 验收独立执行，结果见[真实联网验收](VALIDATION_WEB_SEARCH_2026-11-17.md)，脱敏协议证据在 `output/web-acceptance-20261117/`。

## 后续批次与未验证项

2026-11-17 用户调整真实验收范围：Anthropic 官方 API 不测试，记录为 `NOT_RUN（用户指定跳过官方）`；允许测试用户提供的中转 Claude。随后完成四家搜索 API、DeepSeek 官方和中转模型的验证。官方与中转、原生与独立搜索的结论分别记录。

1. 剩余七家独立搜索尚无真实账号验收，不因同一公共接口测试通过就宣称全部可用。
2. N1 已接入 DeepSeek 原生工具及 Anthropic 基础服务端搜索。中转 GPT 原生完整流程通过；中转 Claude 的完整工具集应采用已验证的独立搜索路径。Anthropic 自动暂停续接与其他厂商原生协议仍待后续批次。
3. B1 / C1 预览后续在 `v8.9.0-dev.2` 完成，见[自动化扩展](AUTOMATION_EXTENSIONS.md)及[验收](VALIDATION_AUTOMATION_2026-11-17.md)。Core 只补通用图片产物与受众/仅本机契约，浏览器和桌面实现均为独立包。
4. 缓存、统一高级过滤、来源专用导航、更多平台原生 attribution 均按后续需要安排。本批次无自动付费备用源，无网络失败自动启动浏览器。
5. 搜索 `v8.9.0-dev.1` 已完成 GitHub/Release、Windows/Linux 可重复归档与独立验证并部署 Hytron；四组 CI 全通过。真实手机公网尚未进行。未覆盖明确的扩展选择，搜索测试凭据未复制到生产。

## 可复现检查

以下命令从仓库根目录运行，`go` 需在 PATH，PostgreSQL 目录使用仓库已有本机验证运行时：

```powershell
go test ./internal/webaccess -count=1
python scripts/test-postgres.py --runtime output/postgresql-runtime/windows/pgsql -- go test -count=1 -run 'TestSearch|TestResponses|TestHTTP|TestExtendedControl|TestFrontendSettingsCoverage|TestOpenTasksReplacesAdaptersAndDisabledToolsDoNotStart' ./internal/harness ./internal/chatui ./internal/webaccess
go build -o output/freeagent-search-ui.exe ./cmd/freeagent
python scripts/test-frontend.py --engine output/freeagent-search-ui.exe --runtime output/postgresql-runtime/windows/pgsql --output output/playwright/search-v89-final --channel msedge --only search,settings-navigation
```

浏览器检查必须使用与当前源码一致的生产 dist 和新编译的 binary；开发资源需要另用 `freeagentdev` 构建，不能仅传开发目录参数就假定生产 binary 已加载新资源。
