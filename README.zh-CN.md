# FreeAgent Harness

一个精简的本地 Harness：持久对话、连续模型与工具调用、流式输出、确认修改、
取消和中断恢复。基础文件任务不需要安装 MCP。

当前是 **尚未发布的 v7.0.1-dev.1 开发候选**。采用独立的 Harness-v5 数据目录和 v7 存储格式，
不支持旧配置、数据库、备份、命令或 SDK，不迁移、不导入，也不扫描旧安装。
v7 控制服务、GUI/CLI attach、Windows 沙箱命令启动器和当前 Provider 适配器已实现。
2026-10-21 已通过 v7.0 验收：DeepSeek 真实 Chat、工具、Responses 与联网搜索，
便携包 CLI、原生 Windows race 和桌面手动检查；见 [v7.0 进度](docs/FREEAGENT_V7_0_IMPLEMENTATION_PROGRESS.md)。

协作默认关闭；Chat 内 `/collaboration once` 可启动一次分工建议，无需手写 JSON。
支持按需串行与有限只读并行、授权结果传递、有限重规划和可选综合。父任务累计
上限与子任务上限同时约束，UNKNOWN 不自动重放。支持检查、暂停、取消、
未派发任务修订和精确确认；见 [Collaboration](docs/COLLABORATION.md)。

## 开始

Windows 便携预览包解压后双击 `FreeAgent.exe`，不会出现控制台黑框；CLI 位于
`bin\freeagent.exe`。无需安装 Go 或 Node；Key 按当前 Windows 用户加密保存，
数据使用包内独立的 `data/Harness-v5`。便携包未签名，不导入旧安装；见
[便携说明](docs/PORTABLE.md)。

v7 桌面版双击 `FreeAgent.exe` 时不会出现控制台黑框，并使用系统中的
Microsoft WebView2 运行时。若 WebView2 不可用，会在默认浏览器中打开同一个
本地 Chat，并显示停止提示。部分 Windows 10 安装需要另外安装 WebView2 Runtime。

构建需要 Go 1.26.5；构建后的可执行文件无需 Go 环境。

```powershell
go build -o ./bin/freeagent.exe ./cmd/freeagent
$exe = (Resolve-Path ./bin/freeagent.exe).Path
$data = Join-Path $env:LOCALAPPDATA 'FreeAgent-Harness-Preview'
& $exe init --data-dir $data --workspace (Get-Location).Path
& $exe key add --name main --url 'https://your-provider.example/v1'
& $exe key test main
& $exe config model --data-dir $data --id main --key main --model 'your-tool-capable-model'
& $exe --data-dir $data
```

可选网页 Chat 使用同一任务服务，启动：

```powershell
& $exe web --data-dir $data
```

在浏览器打开本次启动输出的地址。服务仅绑定 `127.0.0.1`，地址包含本次启动的
访问凭证，应保密；不是旧 Web 管理平台。关闭浏览器不会停止任务，请使用页面停止按钮。
支持多行中文、Markdown、代码、表格、LaTeX、草稿、精确审批、证据和同会话换模型。
协作及高级记忆管理仍通过 CLI 显式使用；Echo 不能证明真实模型能力。
修改前端后，先用 Node 24 / pnpm 在 `internal/chatui` 安装锁定依赖，
运行 `pnpm test`、`pnpm build`，再构建 Go。

`init` 创建的数据目录没有模型；配置模型前，任务以退出码 2 结束。Key 档案保存
服务地址和 key，存放在同一用户所有数据目录共用的 `keys.json`
（`os.UserConfigDir()/FreeAgent/keys.json`，便携包为 `data/keys.json`，
可用 `FREEAGENT_KEYS_FILE` 指定）。地址可以是服务根地址、`/v1` 地址或完整的
`/chat/completions`、`/responses` 地址。

`key add` 默认隐藏输入 key；`--key-env <变量>` 改为引用进程环境变量，
`--key-stdin` 供脚本从标准输入读取一行，`--no-key` 仅允许回环地址。
key 从不作为命令参数传入。Windows 上保存的 key 用 DPAPI 按当前用户加密。
`key list` 和 `doctor` 只显示来源和简短提示，不显示 key。`key test` 发送一次
`GET <base>/models`，`key models` 列出服务返回的模型名；只有这两个命令会联网。

`config model --key <key 档案>` 只保存档案 id，第一个模型自动成为默认。
`--provider echo` 是离线确定性测试模型，只检查持久化，不会推理或自行选择工具。

适配器支持 Chat Completions 和 Responses 的 JSON/SSE、函数调用、无状态输出项续轮，
并解析显式的 provider 侧 `web_search_call`。DeepSeek 联网搜索需要
`deepseek-v4-pro`、Responses 协议和 `web_search=supported`，不等于开启 FreeAgent
本地 HTTP 工具；搜索由 DeepSeek 服务端执行。

每个运行命令都可指定 `--data-dir`；默认位置为
`os.UserConfigDir()/FreeAgent/Harness-v5`，Windows 通常位于 `APPDATA`。
配置命令不联网、不启动 MCP、不读取 key 值。`config model --id <名称>`
注册模型，`--default` 选择默认模型；Chat 内 `/model` 选择后续任务使用的模型。
未完成任务仍使用保存的模型；修改已绑定配置会阻止下一次派发。
继续原会话用 `chat --resume <id>`；仅运行 `chat` 会创建新会话。

## 日常使用

- `config show` 查看配置摘要，不输出指令正文、MCP 参数或环境值。
- `key list`、`key add`、`key edit <id|名称>`、`key test`、`key models` 管理 key 档案；
  `key edit` 用 `--key-prompt` 重新隐藏输入。`key remove` 在给定数据目录仍有模型使用时拒绝，
  `--disable-models [--new-default <id>]` 同时删除这些模型，`--force` 保留它们，派发时报缺失；
  不检查其他数据目录。
- `config model --id <名称> --key <key 档案> --model <模型名>` 注册模型；
  `--provider echo` 配置离线测试模型；`--id <名称> --default` 只切换默认模型。
- `config model --id <名称> --remove [--new-default <id>]` 删除模型及其 Auto/协作引用；
  删除默认模型且仍有其他模型时必须指定 `--new-default`。
- `config workspace --path <目录>` 修改工作区，仅在没有未完成任务时允许。
- `run --model <名称>` 仅指定本次任务；`continue --model <名称>` 仅在完整工具组边界切换。
- `/more` 或 `continue --max-steps <数值> --max-seconds <数值>` 显式增加有限执行额度，不清零历史，不重放 UNKNOWN。
- `config commands --enabled --allow <绝对可执行路径>` 启用可选命令和白名单；
  `--allow` 可重复，只限制可运行的程序路径，不限制参数和子进程。
  `--git-write` 允许沙箱命令写入 `.git`，hooks 和 config 仍为只读。
- `config http --enabled --origin <scheme://host[:port]>` 启用可选 GET；
  `--origin` 可重复，每个完整 URL 仍须确认。`--enabled=false` 关闭可选工具。
- `config context --summary-bytes 4096` 显式开启任务内摘要；零表示关闭。
- `/memory` 查看、保存、编辑、停用、删除或明确分享记忆；保存不会自动选择为后续输入。
- `/memory select` 选择后续任务可使用的私有条目与知识集合；`/memory sources` 查看实际来源。
- `/settings memory` 或 `config memory --knowledge` 显式开启默认关闭的本地知识库。
- `sessions` 列出会话；`chat --resume <id>` 继续对话。
- `run --session <id> --request <key> --json "<任务>"` 支持一次任务与结构化输出。
- 默认的 Ask 模式下，文件修改、创建、具体批量变更、命令、HTTP 和所有 MCP 调用均要求确认；
  `run --permission workspace` 让工作区内的内置文件修改和沙箱命令直接执行，
  `--permission full --confirm-full-access` 跳过工具审批并在宿主机运行命令。
  模型提出的设置变更始终需要确认：CLI 显示短确认码，Chat 显示确认卡片。
  非交互任务停在 `waiting_confirmation`，
  用 `continue --run <id> --interactive` 继续，或先 `preview --run <id>` 查看，
  再 `confirm --run <id> --digest <digest> --approve` 批准；不带 `--approve` 即拒绝。
  摘要与待确认调用不一致时拒绝执行，需要重新预览。
- `inspect --run <id>` 查看运行与调用事实；`abandon --run <id>` 结束未完成任务。
- `control state|plan|confirm|reject|revert-plan` 查看和修改设置，与 GUI、AI 对话使用同一个控制服务。
- 桌面版或 `web` 正在使用数据目录时，`run`、`continue`、`preview`、`confirm`、`control`、
  `config`、`key` 和 `memory` 会连接到该进程继续操作，`sessions` 和 `inspect` 显示只读视图；
  交互式 `chat` 需要独占数据目录。
- 退出码：0 完成，1 失败，2 用法或配置错误，3 等待确认，4 停止或达到上限，5 结果未知。
- `tools list` 与 `doctor` 不联网、不启动外部程序；
  `tools test <server>` 明确启动受信程序，只握手和发现工具。
- `backup --out <新目录>`、`backup-verify --source <备份>`、
  `restore --source <备份> --data-dir <新目录>` 只处理当前格式。

参数须放在任务文字之前。Chat 支持 `/history`、`/sessions`、`/resume <序号>`、
`/new`、`/model`、`/collaboration`、`/settings`、`/memory`、`/status`、`/continue`、`/abandon`、`/file <路径>`、`/begin` ... `/end`、
`/exit`。任务中 Ctrl+C 取消当前任务，尽可能回到输入；空闲时 Ctrl+C 退出。
`run --template <工作区文件>` 显式选取普通文件模板。去重要求同一会话和同一请求 key；
省略 `--session` 是新会话，不是重试。

## 记忆与知识

保存时选择“当前 Agent（仅当前任务或跨任务保留）”“当前任务共享区”
“个人或项目知识库”或“不保存”。普通聊天不会自动学习或保存长期记忆；
换模型不换 Agent，临时专家不会因同名而共享私有资料。

知识库首版仅支持工作区内有界 UTF-8 文本和 Markdown。启用后用 `/memory import`
导入具体文件，再用 `/memory select` 选择集合。模型按需调用本地全文检索；
不会每轮默认检索，也不会无条件注入整个集合。支持来源、字节范围、更新和删除。

停用不向新的请求提供正文，保留供用户恢复；删除移除当前正文、正文修订和索引，
不会撤回已发送的 Provider 请求、普通历史中的片段或旧备份。复制到共享区或
知识库必须确认具体内容。详见 [记忆说明](docs/MEMORY.md)。

## 边界

内置列举、读取、搜索、受控编辑、创建和批量变更默认只处理选定工作目录，除非显式选择了
额外目录或全部非保护路径；拒绝链接、硬链接、
`.git` 和 Harness 数据目录。编辑检查 SHA-256 冲突，但不是事务文件系统；
不要同时修改同一个文件。MCP 是受信本地代码，不提供恶意程序沙箱。
批量批准绑定具体内容，每项结果独立记录；部分成功不会回滚或自动重放。
Windows 下命令在 AppContainer 沙箱中运行：Ask 模式只读，Workspace 模式可写工作区且不联网；
只有 Full access 在宿主机以当前用户运行，并在回执中标记 `host_escape_hatch`。
命令记录实际退出、超时、截断和观察到的文件变化；停止不等于没有副作用。
HTTP 仅允许显式来源，远程需 HTTPS 和公共地址；显式回环来源可用 HTTP。
不使用代理、Cookie、凭据转发、重定向或自动重试；GET 仍可能产生外部副作用。

目录列举按名称排序，支持 `offset`、`max_entries` 和 `next_offset` 分页；
超过 10,000 条的目录需缩小范围。文件读取保持完整 UTF-8 字符，
即使一页的字节数小于一个中文字符或 Emoji，也能继续向后读取。

UNKNOWN 不会自动重放。放弃不抹掉未知事实，同工作目录的后续任务仍会收到提醒。
上下文按完整旧 Run 裁剪，原始历史保留；继续任务不重置步数与累计时间。
可选任务内摘要保留来源，不成为跨会话记忆。CLI 和第二本地调用方共用
[同一任务服务](docs/COMPOSITION.md)，证据来自运行时记录而非模型声称。

生产结构为 CLI、Harness、MCP 传输和可选回环 Chat；都复用同一任务服务。
不包含模块治理、隐式学习、默认自动 RAG、Channel、Web 管理台或后台调度。

详细配置、限制与验证见 [英文入口](README.md)、[配置](docs/CONFIGURATION.md)、
[恢复](docs/RECOVERY.md) 和 [v7.0 进度](docs/FREEAGENT_V7_0_IMPLEMENTATION_PROGRESS.md)。
本地 fixture 通过不代表真实模型验收或正式发布。
