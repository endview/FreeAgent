# Contributing to FreeAgent

感谢你帮助改进 FreeAgent。项目当前优先保证模块开放性和可信内核边界，而不是快速增加默认内置能力。

## 设计原则

- Role、Persona、Knowledge/RAG、Memory、Skill、MCP、Team 和 Channel 都应保持可选、可替换。
- `pure_chat` 是安全默认；未绑定任何可选模块、Tool 或扩展 SecretRef 的 Agent 必须仍能完成普通聊天。
- 进程内扩展必须是经审核、固定精确构件的可信 `IN_PROCESS` 模块；不可信或进程外扩展必须通过显式配置的 MCP 边界接入，不能获得内核旁路。
- Agent、Module 或 Skill 只能收窄权限，不能扩大 Workspace、Tenant 或 Profile 授予的权限。
- 外部效果必须经过持久账本、最终授权、幂等与 `UNKNOWN` 处理，不能通过重试做语义重放。
- 已冻结 Run 不受后续安装、升级、发现结果或配置激活影响。
- 文档必须区分本地测试、真实互操作验证和生产 SLA。

如果改动会影响这些原则，请在提案中明确说明影响和迁移方案。

## 开发流程

1. 先为行为变化添加会失败的聚焦测试。
2. 实现满足测试的最小改动。
3. 运行受影响包测试，再运行全量测试。
4. 更新公开契约、Schema、示例和能力成熟度矩阵。
5. 对权限、预算、外部效果、持久化或恢复变更补充失败关闭测试。

基本检查：

```text
gofmt -w <changed-go-files>
go mod verify
go test -count=1 ./...
go vet ./...
```

不要提交模型或渠道凭据、真实用户数据、数据库、日志、备份、构建产物或本地发布证据。
唯一的构建产物例外是 `internal/controlweb/dist` 中由
`scripts/Test-ControlWeb.ps1` 仓外重建并逐字节验证的嵌入式静态资产；不得提交
`node_modules`、npm 缓存、临时目录、源映射或该目录之外的前端构建输出。
模块/SDK 兼容夹具必须是项目原创或已明确允许再分发的合成材料；不要复制第三方源码、
Manifest、payload、Logo 或二进制。夹具和本地 Host 测试都不能替代真实互操作证据。

## 能力声明

新增能力只有在能力矩阵中绑定了可执行测试证据后，才能标为 `stable`。本地 fixture 不能替代真实外部平台或协议互操作验证；缺少该证据时应使用 `unverified`、`experimental` 或 `planned`。

## 许可证

提交到本项目的代码和文档将按仓库的 `AGPL-3.0-only` 许可证发布。请只提交你有权提供的内容，并保留第三方材料所要求的许可和通知。
