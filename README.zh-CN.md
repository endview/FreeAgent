# FreeAgent

<center>
  <strong>用 Go 构建的受治理、本地优先 Agent 运行时</strong>
</center>

<center>
  FreeAgent 把身份、权限、预算、外部效果和恢复能力保留在可审计的核心中；
  Role、Knowledge、Memory、Skill、Action、Channel 与编排能力则作为显式、可替换的装配单元存在。
</center>

<center>
  <a href="README.md">English</a>
  ·
  <a href="README.zh-CN.md">简体中文</a>
  ·
  <a href="README.zh-TW.md">繁體中文</a>
</center>

<center>
  <img alt="FreeAgent preview, Go, platform, runtime, and license badges" src="docs/assets/readme-badges.svg">
</center>

## FreeAgent 解决什么问题？

许多 Agent 框架把模型放在系统中心。FreeAgent 的出发点不同：Agent 是一个
拥有持久状态、显式权限和可恢复效果的受治理运行时。

核心负责那些事后难以审计的部分：

- Workspace、Agent 与 Profile 的身份；
- 模块装配和权限上限；
- 预算、Usage 与 Attempt 账本；
- 上下文编译和会话历史；
- 外部效果及其 `UNKNOWN` 结果；
- 备份、恢复和精确重试语义。

可选能力围绕这个核心装配。默认 Pure Chat Profile 可以在零可选模块的状态下
运行；显式装配则可以加入本地 RAG、有界 Memory、受信任本地 Action、MCP 工具、
loopback Channel、Model Profile，或窄范围的 Remote/WASM Action Host。

```text
Control Plane + RuntimeCatalog
              |
              v
      Assembly Compiler
              |
              v
MemberExecutionSnapshot + RunManifest
              |
              v
          Universal Loop
     /         |          \
Context Port  Model Port  Action Port
     \         |          /
              v
Current Store / Attempt / Usage / History
              |
              v
     Gateway -> private executor
```

模块通过精确的 Port 与 Binding 贡献能力，不能绕过 Workspace 权限、预算、
Gateway、效果账本或最终持久化。

## 核心亮点

| 领域 | FreeAgent 提供 |
| --- | --- |
| 受治理装配 | 显式 Apply、Dry-run、Disable、CAS、权限上限，以及启用模块的精确 runtime expectation。 |
| 持久执行 | 不可变 Run 与 Attempt、精确重试、持久会话、Usage 记录，以及 `UNKNOWN` 禁止语义重放。 |
| 上下文控制 | 上下文编译在 85% 阈值触发摘要，在 100% 阈值执行最小、有序的历史移除。 |
| 可选能力 | 本地 RAG、有界 Memory、受信任本地 Action、MCP stdio 工具、loopback Channel、Model Profile，以及窄范围 Remote/WASM Action Host。 |
| 恢复能力 | 覆盖 Store 状态、构件、Cursor、历史和模块事实的完整备份、验证与恢复。 |
| 本地控制面 | 可选 loopback Control listener，提供只读 Overview 和窄范围、需确认的模块禁用流程。 |
| 供应链 | 按目标生成归档、SPDX SBOM、未签名 provenance 和 SHA-256 checksum manifest。 |

FreeAgent 仍处于早期 Developer Preview。已验收的开发切片见
[CURRENT_CAPABILITIES](docs/CURRENT_CAPABILITIES.md)；这些状态不表示生产支持、
可用性、兼容性或升级 SLA。

## 下载

当前 Developer Preview 为 **v0.1.0-dev.2**。它只包装既有开发切片，
不新增新的 Runtime 功能波次。

| 目标平台 | 归档 |
| --- | --- |
| Windows AMD64 | `freeagent-v0.1.0-dev.2-windows-amd64.zip` |
| Windows ARM64 | `freeagent-v0.1.0-dev.2-windows-arm64.zip` |
| Linux AMD64 | `freeagent-v0.1.0-dev.2-linux-amd64.tar.gz` |
| Linux ARM64 | `freeagent-v0.1.0-dev.2-linux-arm64.tar.gz` |
| Darwin AMD64，仅构建 | `freeagent-v0.1.0-dev.2-darwin-amd64-build-only.tar.gz` |
| Darwin ARM64，仅构建 | `freeagent-v0.1.0-dev.2-darwin-arm64-build-only.tar.gz` |

请从 [GitHub Releases](https://github.com/endview/FreeAgent/releases) 下载归档和
相邻的 `SHA256SUMS`，先校验 checksum，再解压到新目录，并在使用前确认版本输出。
Windows 与 Linux AMD64 包需要真实本地安装验证。ARM64 包为交叉构建，
Darwin 包在本 Preview 中不声明已通过原生 macOS 安装验证。

完整安装与升级说明见 [INSTALL](docs/INSTALL.md)。

## 五分钟离线体验

内置 quickstart 使用确定性 echo 模型，不需要 API key、网络访问或付费 Provider。

```powershell
$FreeAgent = (Resolve-Path .\bin\freeagent-windows-amd64.exe).Path
$Runtime = Join-Path $PWD 'data\quickstart'
New-Item -ItemType Directory -Force $Runtime | Out-Null

& $FreeAgent init `
  --db (Join-Path $Runtime 'current.sqlite') `
  --artifact-root (Join-Path $Runtime 'artifacts') `
  --seed .\config\current-v1.bootstrap.seed.json

& $FreeAgent conversation-create `
  --db (Join-Path $Runtime 'current.sqlite') `
  --conversation preview-conversation

$Deadline = (Get-Date).ToUniversalTime().AddMinutes(10).ToString('o')
$Turn1 = & $FreeAgent chat `
  --db (Join-Path $Runtime 'current.sqlite') `
  --artifact-root (Join-Path $Runtime 'artifacts') `
  --conversation preview-conversation `
  --conversation-revision 0 `
  --message 'remember: the package works offline' `
  --request-id preview-turn-1 `
  --deadline $Deadline | ConvertFrom-Json

$Turn2 = & $FreeAgent chat `
  --db (Join-Path $Runtime 'current.sqlite') `
  --artifact-root (Join-Path $Runtime 'artifacts') `
  --conversation preview-conversation `
  --conversation-revision $Turn1.conversation_revision `
  --conversation-head-run $Turn1.run_id `
  --message 'what did I ask you to remember?' `
  --request-id preview-turn-2 `
  --deadline $Deadline | ConvertFrom-Json
```

第二回合只能从第一回合返回的精确成功 head 继续。过期的 revision 或 head
会失败关闭，而不是分叉会话。

包含备份、验证、恢复和本地服务的完整流程见
[QUICKSTART](docs/QUICKSTART.md)。

## 安全边界

- HTTP chat 与可选 Control surface 只绑定字面 loopback 地址。
- Control 默认关闭，使用进程内 session 和 owner-only bootstrap handoff。
- Runtime 数据库、artifact root、备份和 handoff 文件均由 Operator 拥有。
- 备份或恢复前停止所有写入进程；不要让两个 writer 同时使用一个 Store。
- 当前模块系统不是通用不可信插件宿主。
- 本 Developer Preview 的归档、SBOM、provenance 和 checksum manifest 均未签名。
- 真实 DeepSeek 访问需要显式启用、单独提供 secret、可能产生费用，且不属于离线包 smoke test。

启用 Control 或真实模型 Provider 前，请先阅读
[KNOWN_LIMITATIONS](docs/KNOWN_LIMITATIONS.md)。安全报告方式见
[SECURITY](SECURITY.md)。

## 从源码构建

要求：

- Go 1.26.5 或更高版本
- PowerShell、bash，或其他能够设置环境变量并发送 HTTP 请求的 shell

```powershell
go test -count=1 -timeout=30m ./...
go vet ./...
go run ./cmd/freeagent --help
```

仓库开发除普通 Go 工具链外，还使用本地 release gate 覆盖文档、License、
公共树、安装 smoke、race 测试和跨平台构建。

## 文档

- [快速开始](docs/QUICKSTART.md)
- [安装指南](docs/INSTALL.md)
- [当前能力](docs/CURRENT_CAPABILITIES.md)
- [项目状态](docs/PROJECT_STATUS.md)
- [核心运行时规格](docs/specs/CORE_RUNTIME_V1.md)
- [Current Store 规格](docs/specs/CURRENT_STORE_V1.md)
- [Control API 规格](docs/specs/CONTROL_API_V1.md)
- [模块开发](docs/MODULE_DEVELOPMENT_V1.md)
- [发布供应链](docs/RELEASE_SUPPLY_CHAIN.md)
- [已知限制](docs/KNOWN_LIMITATIONS.md)
- [贡献指南](CONTRIBUTING.md)
- [安全策略](SECURITY.md)

## 许可证

Licensed as `AGPL-3.0-only`; see [LICENSE](LICENSE).
