# FreeAgent Harness

精簡的本機 Harness，提供持久對話、連續模型與工具呼叫、串流輸出、
修改確認、取消和中斷恢復。內建檔案工具不需安裝 MCP。

目前為尚未發布的 `v7.0.0-dev.1` 開發候選，使用獨立的 `Harness-v5` 資料目錄與 v7 格式；
不相容、不遷移、不匯入舊版本資料。UNKNOWN 不會自動重播，
放棄任務也不會消除未知事實。
已完成精簡核心、同一任務服務、可選 HTTP GET/命令白名單與預設關閉的
受控協作實作，以及明確授權的私有、任務共享與本機知識庫記憶。
協作有明確角色、工具、上下文及父子預算，不自動重播 UNKNOWN。

`web --data-dir <新資料目錄>` 明確啟動可選的回環 Chat，與 CLI 共用同一任務服務。
從本次啟動輸出的私密位址開啟頁面，支援 Markdown、程式碼、LaTeX、精確審批、
停止和恢復。關閉瀏覽器不會停止任務；這不是旧 Web 管理平台或背景服務。
修改前端後先於 `internal/chatui` 執行 `pnpm test`、`pnpm build`，再建置 Go。

Windows 便攜包解壓後雙擊 `FreeAgent.exe` 不會出現主控台黑框；CLI 位於
`bin\freeagent.exe`，Key 會以目前 Windows 使用者加密保存。桌面版使用系統中的
Microsoft WebView2 執行階段。若 WebView2 不可用，會在預設瀏覽器開啟同一個
本機 Chat，並顯示停止提示。部分 Windows 10 安裝需要另外安裝 WebView2 Runtime。

`init` 建立的資料目錄沒有模型。先用 `key add --name <名稱> --url <位址>` 建立
Key 檔案（預設隱藏輸入 key，也可用 `--key-env <變數>`；key 不作為命令參數），
再用 `config model --id <名稱> --key <Key 檔案> --model <模型名>` 新增模型，
第一個模型自動成為預設。`key test` 只發送一次 `GET <base>/models`。
待確認的呼叫可用 `preview --run <id>` 查看，`confirm --run <id> --digest <digest> --approve`
批准；不帶 `--approve` 即拒絕。退出碼：0 完成，1 失敗，2 用法或設定錯誤，
3 等待確認，4 停止或達到上限，5 結果未知。

建置與指令範例見 [English](README.md)，完整中文說明見
[简体中文](README.zh-CN.md)。[Configuration](docs/CONFIGURATION.md)
與 [Recovery](docs/RECOVERY.md) 說明目前格式和操作邊界。

使用 `/memory` 明確保存內容、選擇後續輸入並查看來源。知識庫預設關閉，
僅支援有界 UTF-8 文字與 Markdown、全文檢索、更新、停用和刪除。
不掃描歷史、不自動保存、不在背景建立索引。資料目錄為 `Harness-v5`；
見 [Memory](docs/MEMORY.md)。

MCP 僅適用於受信任的本機程式，不是沙箱。2026-10-21 已通過 v7.0 驗收：
DeepSeek 真實 Chat、工具、審批、中斷、協作、Responses 與聯網搜尋，以及便攜包 CLI、
原生 Windows race 和桌面手動檢查；廣泛品質與其他 Provider 未經驗收。見
[v7.0 進度](docs/FREEAGENT_V7_0_IMPLEMENTATION_PROGRESS.md) 和
[未簽名便攜預覽包](docs/PORTABLE.md)，不應視為正式支援承諾。
