# FreeAgent

<center>
  <strong>以 Go 建構的受治理、本地優先 Agent 執行階段</strong>
</center>

<center>
  FreeAgent 將身分、權限、預算、外部效果與復原能力保留在可稽核的核心中；
  Role、Knowledge、Memory、Skill、Action、Channel 與編排能力則是明確、可替換的組裝單元。
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

## FreeAgent 解決什麼問題？

許多 Agent 框架把模型放在系統中心。FreeAgent 的出發點不同：Agent 是一個
擁有持久狀態、明確權限與可回復效果的受治理執行階段。

核心負責那些事後難以稽核的部分：

- Workspace、Agent 與 Profile 的身分；
- 模組組裝與權限上限；
- 預算、Usage 與 Attempt 帳本；
- 上下文編譯與會話歷史；
- 外部效果及其 `UNKNOWN` 結果；
- 備份、還原與精確重試語意。

可選能力圍繞這個核心組裝。預設 Pure Chat Profile 可以在零可選模組的狀態下
執行；明確組裝則可加入本地 RAG、有界 Memory、受信任本地 Action、MCP 工具、
loopback Channel、Model Profile，或窄範圍 Remote/WASM Action Host。

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

模組透過精確的 Port 與 Binding 貢獻能力，不能繞過 Workspace 權限、預算、
Gateway、效果帳本或最終持久化。

## 核心亮點

| 領域 | FreeAgent 提供 |
| --- | --- |
| 受治理組裝 | 明確 Apply、Dry-run、Disable、CAS、權限上限，以及啟用模組的精確 runtime expectation。 |
| 持久執行 | 不可變 Run 與 Attempt、精確重試、持久會話、Usage 記錄，以及 `UNKNOWN` 禁止語意重放。 |
| 上下文控制 | 上下文編譯在 85% 閾值觸發摘要，在 100% 閾值執行最小、有序的歷史移除。 |
| 可選能力 | 本地 RAG、有界 Memory、受信任本地 Action、MCP stdio 工具、loopback Channel、Model Profile，以及窄範圍 Remote/WASM Action Host。 |
| 復原能力 | 涵蓋 Store 狀態、構件、Cursor、歷史與模組事實的完整備份、驗證與還原。 |
| 本地控制面 | 可選 loopback Control listener，提供唯讀 Overview 與窄範圍、需確認的模組停用流程。 |
| 供應鏈 | 依目標產生封存檔、SPDX SBOM、未簽署 provenance 與 SHA-256 checksum manifest。 |

FreeAgent 仍處於早期 Developer Preview。已驗收的開發切片見
[CURRENT_CAPABILITIES](docs/CURRENT_CAPABILITIES.md)；這些狀態不代表生產支援、
可用性、相容性或升級 SLA。

## 下載

目前 Developer Preview 為 **v0.1.0-dev.1**。它只包裝既有開發切片，
不新增新的 Runtime 功能波次。

| 目標平台 | 封存檔 |
| --- | --- |
| Windows AMD64 | `freeagent-v0.1.0-dev.1-windows-amd64.zip` |
| Windows ARM64 | `freeagent-v0.1.0-dev.1-windows-arm64.zip` |
| Linux AMD64 | `freeagent-v0.1.0-dev.1-linux-amd64.tar.gz` |
| Linux ARM64 | `freeagent-v0.1.0-dev.1-linux-arm64.tar.gz` |
| Darwin AMD64，僅建置 | `freeagent-v0.1.0-dev.1-darwin-amd64-build-only.tar.gz` |
| Darwin ARM64，僅建置 | `freeagent-v0.1.0-dev.1-darwin-arm64-build-only.tar.gz` |

請從 [GitHub Releases](https://github.com/endview/freeagent/releases) 下載封存檔與
相鄰的 `SHA256SUMS`，先驗證 checksum，再解壓縮到新目錄，並在使用前確認版本輸出。
Windows 與 Linux 封存檔已完成真實本機安裝驗證；Darwin 封存檔為交叉建置產物，
本 Preview 不宣稱已通過原生 macOS 安裝驗證。

完整安裝與升級說明見 [INSTALL](docs/INSTALL.md)。

## 五分鐘離線體驗

內建 quickstart 使用確定性 echo 模型，不需要 API key、網路存取或付費 Provider。

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

第二回合只能從第一回合回傳的精確成功 head 繼續。過期的 revision 或 head
會失敗關閉，而不是分叉會話。

包含備份、驗證、還原與本機服務的完整流程見
[QUICKSTART](docs/QUICKSTART.md)。

## 安全邊界

- HTTP chat 與可選 Control surface 只繫結字面 loopback 位址。
- Control 預設關閉，使用程序內 session 與 owner-only bootstrap handoff。
- Runtime 資料庫、artifact root、備份與 handoff 檔案均由 Operator 擁有。
- 備份或還原前停止所有寫入程序；不要讓兩個 writer 同時使用一個 Store。
- 目前模組系統不是通用不可信外掛宿主。
- 本 Developer Preview 的封存檔、SBOM、provenance 與 checksum manifest 均未簽署。
- 真實 DeepSeek 存取需要明確啟用、另行提供 secret、可能產生費用，且不屬於離線封裝 smoke test。

啟用 Control 或真實模型 Provider 前，請先閱讀
[KNOWN_LIMITATIONS](docs/KNOWN_LIMITATIONS.md)。安全回報方式見
[SECURITY](SECURITY.md)。

## 從原始碼建置

需求：

- Go 1.26.5 或更新版本
- PowerShell、bash，或其他能設定環境變數並傳送 HTTP 請求的 shell

```powershell
go test -count=1 -timeout=30m ./...
go vet ./...
go run ./cmd/freeagent --help
```

倉庫開發除了普通 Go 工具鏈，也使用本機 release gate 涵蓋文件、License、
公共樹、安裝 smoke、race 測試與跨平台建置。

## 文件

- [快速開始](docs/QUICKSTART.md)
- [安裝指南](docs/INSTALL.md)
- [目前能力](docs/CURRENT_CAPABILITIES.md)
- [專案狀態](docs/PROJECT_STATUS.md)
- [核心執行階段規格](docs/specs/CORE_RUNTIME_V1.md)
- [Current Store 規格](docs/specs/CURRENT_STORE_V1.md)
- [Control API 規格](docs/specs/CONTROL_API_V1.md)
- [模組開發](docs/MODULE_DEVELOPMENT_V1.md)
- [發布供應鏈](docs/RELEASE_SUPPLY_CHAIN.md)
- [已知限制](docs/KNOWN_LIMITATIONS.md)
- [貢獻指南](CONTRIBUTING.md)
- [安全政策](SECURITY.md)

## 授權

Licensed as `AGPL-3.0-only`; see [LICENSE](LICENSE).
