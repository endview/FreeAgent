# v0.1.0-dev.2 候选发布证据

核对日期：2026-09-23
候选仓库：`freeagent-release-candidate-20260923`
分支：`master`（本地候选副本）
功能基线提交：`ecb5a124b8033776c169543f53f994594796f8e6`
版本：`v0.1.0-dev.2`
候选状态：`LOCAL_DEVELOPER_PREVIEW_VALIDATED / RELEASE_READY_PENDING`

## 结论

P0–P4 开发切片已收口。功能基线已完成 Windows/Linux AMD64 原生离线安装与备份恢复，
并在 ext4 干净源码上完成 Linux 全仓 race：53 个 package result、5,166 个测试终态
为 PASS，exit code 为 0，stderr 为空。最终文档提交后的六平台归档、供应链文件和
Windows/Linux AMD64 原生 smoke 也已通过。未 push、未 tag、未上传、未签名、未公开
发布；归档存在不等于公开 Release、公开 Beta 或 `RELEASE_READY`。

## 当前候选证据

| 检查 | 状态 | 证据 |
| --- | --- | --- |
| Windows 全仓 Go 测试 | `PASS` | `go1.26.5 windows/amd64`，`go test ./... -count=1 -timeout=45m`，2026-09-23；最终文档修改另做定向回归 |
| Windows `go vet` | `PASS` | `go vet ./...` |
| Go module 校验 | `PASS` | `go mod verify` |
| Go 格式与 diff 检查 | `PASS` | `gofmt -l` 无输出；`git diff --check` |
| 事实生成 | `PASS` | `go generate ./...` 后生成的 `docs/generated/runtime-facts.*` 已同步 |
| 文档、许可证、金额禁入 | `PASS` | `Test-Docs.ps1`、`Test-License.ps1`、`Test-MoneyBanList.ps1` |
| 公开树、品牌、能力矩阵 | `PASS` | `Test-PublicTree.ps1`、`Test-Branding.ps1`、`Test-CapabilityMatrix.ps1` |
| Control UI 静态/类型/测试/生产构建 | `PASS` | `Test-ControlWeb.ps1`；双语 catalog 和嵌入 dist 已复验 |
| 真实浏览器管理面 | `PASS` | handoff、Overview、Modules、Upgrade Reviews、Store Management；`zh-CN`/`en-US` 切换；console 0 errors / 0 warnings |
| 修复后 Linux 受影响 race | `PASS` | WSL2 原生 `go test -race ./cmd/freeagent -run 'TestProductionControl.*(Management\|Overview)' -count=1 -timeout=15m` |
| Linux 全仓 race | `PASS` | `ecb5a12` 的 ext4 clean source，WSL Debian，Go 1.26.5/GCC 14.2，`go test -json -race -count=1 -timeout=240m ./...`；53 个 package result、5,166 个测试 PASS、exit code 0、stderr 为空；完整 JSONL 保存在仓库外 `linux-race-ext4-20260923/` |
| Windows 原生安装 smoke | `PASS` | 最终 clean candidate Windows AMD64 归档；版本绑定候选 commit；Echo seed、两轮聊天、exact retry、serve 重启、backup/verify/restore/continue、Control/loopback 均通过 |
| Linux 原生安装 smoke | `PASS` | 最终 clean candidate Linux AMD64 归档；版本绑定候选 commit；初始化、两轮聊天、exact retry、服务重启、backup/verify/restore/continue 均通过；`TERMINATED / MODEL_SUCCEEDED` 是正确终态 |
| 目标平台打包后备份/恢复 | `PASS` | 最终 Windows/Linux AMD64 归档均完成离线备份、验证、恢复和继续运行 |
| 当前候选供应链归档 | `PASS` | 最终六平台归档（Darwin build-only）、每包 SPDX SBOM、checksum、unsigned provenance；外层 `SHA256SUMS` 6/6 通过 |
| `RELEASE_READY` | `PENDING OWNER DECISION` | 必须由项目所有者在剩余证据完成后显式裁定 |

## 发布前阻塞

1. 用户此前在对话中暴露过智谱 API Key。该凭据不得进入源码、日志、备份、制品或文档；
   在任何共享、上传或发布前必须先在智谱后台撤销并重新生成。
2. 项目所有者完成 `RELEASE_READY` 显式裁定后，才允许另行执行 tag、上传或公开发布。

## 已确认的范围边界

- 当前 Provider 只承诺 exact `zhipu` / `glm-4.5` compiled support matrix，不代表全部 GLM
  模型或任意 OpenAI-compatible endpoint。
- P4 管理 UI 是只读投影；不提供 Review/Decision mutation、Install、Activate、Bind、Grant、
  Apply、Execute、在线 Restore 或在线 CreateBundle。
- UNKNOWN 只能查询原 Attempt 证据和对账状态，不允许 resend、replay、换 Provider 或隐式 retry。
- 当前 FAC2 Store 为 UserVersion 2；金额预算、价格、费用和成本路由不属于当前运行语义。
