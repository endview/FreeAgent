# v8.1 可调项覆盖说明

本清单对应 `frontend/settings-map.json` 与 `TestFrontendSettingsCoverage`，不是第二套配置 schema。新增字段/操作需要审阅目的地，普通测试不自动更新清单。实际控件由浏览器验收核对；仅有目的地不能证明已实现。

## 保存、范围和生效

- 产品配置保存当前 Core 数据目录的 `config.json`，秘密分别由 keys/渠道凭据入口保存。Config 版本、工具代次、模型窗口来源是运行时元数据或派生值，不提供任意编辑框。
- 大部分配置只影响新 Run；已有 Run 保留冻结快照。权限降低按现有 Core 立即收紧规则处理。server 监听、公网地址/socket 变化明确重启后生效。
- 会话、空间、能力选择、Memory/固定内容、队列和控制操作的真实记录由 PostgreSQL 保存，作用于明确的会话/空间/工作区/人员；不能用 GUI 自己的内存伪造成功。
- 界面布局、主题和允许的消息草稿仅属于当前设备客户端；Provider/Bot/MCP 秘密不进入设备草稿存储。MCP 原始 env 和 Key 值只能由用户安全输入，AI 只能提出非秘密变更或引用已有秘密名称。
- 全局文件范围联动 computer_files/computer_roots/execution.additional_roots；GUI 的“文件范围”是该同一配置的入口，不提供互相矛盾的独立开关。默认沙箱和执行网络策略在“高级策略”。
- 所有产品持久变更走现有 ControlService；GUI 用户点击或 CLI 提示确认，AI 提案始终预览具体内容与版本。执行文件/命令仍遵守原权限模式。
- CLI `control catalog` 枚举操作，`control plan --kind <kind> --arguments <JSON>` 提案并提示确认；对话经 control 工具提出同一种操作。秘密专用输入不向模型开放。完整执行配置的原子组合提案可通过 CLI/AI 完成，GUI 提供各字段的具体入口。
- HTTP 工具策略、协作/Memory/扩展预算属于高级策略；当前对话 Memory 的选入/排除在上下文面板，持久内容管理在 Memory 设置。人员上限在渠道已验证身份处编辑。

## Config 叶字段到界面

| 字段 | GUI 目的地 |
| --- | --- |
| `attachments.jpeg` | 附件 |
| `attachments.max_batch_bytes` | 附件 |
| `attachments.max_export_bytes` | 附件 |
| `attachments.max_files` | 附件 |
| `attachments.max_input_bytes` | 附件 |
| `attachments.max_pixels` | 附件 |
| `attachments.png` | 附件 |
| `attachments.quota_bytes` | 附件 |
| `attachments.staging_hours` | 附件 |
| `attachments.text` | 附件 |
| `auto.assessment_model` | 高级策略 |
| `auto.enabled` | 高级策略 |
| `auto.max_assessments` | 高级策略 |
| `auto.max_candidates` | 高级策略 |
| `auto.max_upgrades` | 高级策略 |
| `auto.models` | 高级策略 |
| `auto_compact_percent` | 上下文 |
| `channels.connections` | 渠道 |
| `collaboration.child_limits.max_call_output_bytes` | 高级策略 |
| `collaboration.child_limits.max_output_bytes` | 高级策略 |
| `collaboration.child_limits.max_seconds` | 高级策略 |
| `collaboration.child_limits.max_steps` | 高级策略 |
| `collaboration.limits.max_active_seconds` | 高级策略 |
| `collaboration.limits.max_children` | 高级策略 |
| `collaboration.limits.max_concurrent_children` | 高级策略 |
| `collaboration.limits.max_model_calls` | 高级策略 |
| `collaboration.limits.max_output_bytes` | 高级策略 |
| `collaboration.limits.max_tool_calls` | 高级策略 |
| `collaboration.max_replans` | 高级策略 |
| `collaboration.mode` | 高级策略 |
| `collaboration.models` | 高级策略 |
| `collaboration.paths` | 高级策略 |
| `collaboration.shared_memory.append` | 高级策略 |
| `collaboration.shared_memory.entries` | 高级策略 |
| `collaboration.shared_memory.kinds` | 高级策略 |
| `collaboration.shared_memory.modify` | 高级策略 |
| `collaboration.shared_memory.read` | 高级策略 |
| `collaboration.synthesis` | 高级策略 |
| `collaboration.tools` | 高级策略 |
| `commands.allowed_executables` | 命令执行 |
| `commands.enabled` | 命令执行 |
| `commands.execution` | 命令执行 |
| `commands.inherit_environment` | 命令执行 |
| `commands.max_output_bytes` | 命令执行 |
| `commands.rules` | 命令执行 |
| `commands.services.allow_local_listen` | 命令执行 |
| `commands.services.default_seconds` | 命令执行 |
| `commands.services.enabled` | 命令执行 |
| `commands.services.max_total_seconds` | 命令执行 |
| `commands.timeout_seconds` | 命令执行 |
| `computer_files` | 文件范围 |
| `computer_roots` | 文件范围 |
| `default_model` | 模型与 Key |
| `default_selection` | 高级策略 |
| `execution.additional_roots` | 文件范围 |
| `execution.blocked_paths` | 文件范围 |
| `execution.file_scope` | 文件范围 |
| `execution.network_policy.allowed_domains` | 高级策略 |
| `execution.network_policy.mode` | 高级策略 |
| `execution.permission_mode` | 权限与执行 |
| `execution.sandbox_mode` | 高级策略 |
| `extensions.enabled` | 扩展 |
| `extensions.envFrom` | 扩展 |
| `extensions.max_context_chars` | 高级策略 |
| `extensions.max_model_calls_per_turn` | 高级策略 |
| `extensions.required` | 扩展 |
| `extensions.settings` | 扩展 |
| `extensions.trusted_workspaces` | 扩展 |
| `history_page_size` | 高级策略 |
| `http.allowed_origins` | 高级策略 |
| `http.enabled` | 高级策略 |
| `http.max_output_bytes` | 高级策略 |
| `http.timeout_seconds` | 高级策略 |
| `instructions` | 高级策略 |
| `max_context_bytes` | 上下文 |
| `max_output_tokens` | 上下文 |
| `max_tool_result_bytes` | 高级策略 |
| `mcpServers.$id.args` | MCP |
| `mcpServers.$id.command` | MCP |
| `mcpServers.$id.cwd` | MCP |
| `mcpServers.$id.enabled` | MCP |
| `mcpServers.$id.env` | MCP |
| `mcpServers.$id.envFrom` | MCP |
| `mcpServers.$id.hiddenTools` | MCP |
| `mcpServers.$id.trustReadOnlyHint` | MCP |
| `memory.knowledge_enabled` | 高级策略 |
| `memory.max_context_bytes` | 高级策略 |
| `memory.max_document_bytes` | 高级策略 |
| `memory.max_entries` | 高级策略 |
| `model_retries` | 上下文 |
| `models.$id.capabilities.context_bytes` | 模型与 Key |
| `models.$id.capabilities.context_window_source` | 模型与 Key |
| `models.$id.capabilities.context_window_tokens` | 模型与 Key |
| `models.$id.capabilities.embeddings` | 模型与 Key |
| `models.$id.capabilities.error_policy` | 模型与 Key |
| `models.$id.capabilities.finish_reason` | 模型与 Key |
| `models.$id.capabilities.output_tokens` | 模型与 Key |
| `models.$id.capabilities.parallel_tools` | 模型与 Key |
| `models.$id.capabilities.protocol` | 模型与 Key |
| `models.$id.capabilities.streaming` | 模型与 Key |
| `models.$id.capabilities.strength` | 模型与 Key |
| `models.$id.capabilities.token_limit` | 模型与 Key |
| `models.$id.capabilities.tools` | 模型与 Key |
| `models.$id.capabilities.usage` | 模型与 Key |
| `models.$id.capabilities.vision.enabled` | 模型与 Key |
| `models.$id.capabilities.vision.jpeg` | 模型与 Key |
| `models.$id.capabilities.vision.max_image_encoded_bytes` | 模型与 Key |
| `models.$id.capabilities.vision.max_images` | 模型与 Key |
| `models.$id.capabilities.vision.max_request_bytes` | 模型与 Key |
| `models.$id.capabilities.vision.max_side` | 模型与 Key |
| `models.$id.capabilities.vision.png` | 模型与 Key |
| `models.$id.capabilities.web_search` | 模型与 Key |
| `models.$id.key` | 模型与 Key |
| `models.$id.model` | 模型与 Key |
| `models.$id.provider` | 模型与 Key |
| `providers` | 模型与 Key |
| `public_web.absolute_hours` | 公网安全 |
| `public_web.enabled` | 公网安全 |
| `public_web.idle_minutes` | 公网安全 |
| `public_web.max_permission` | 公网安全 |
| `public_web.max_sessions` | 公网安全 |
| `public_web.origin` | 公网安全 |
| `public_web.security.allowlist` | 公网安全 |
| `public_web.security.allowlist_enabled` | 公网安全 |
| `public_web.security.auto_blacklist` | 公网安全 |
| `public_web.security.blacklist_after_locks` | 公网安全 |
| `public_web.security.blacklist_minutes` | 公网安全 |
| `public_web.security.cookie_check` | 公网安全 |
| `public_web.security.denylist` | 公网安全 |
| `public_web.security.denylist_enabled` | 公网安全 |
| `public_web.security.failure_limit` | 公网安全 |
| `public_web.security.failure_lock` | 公网安全 |
| `public_web.security.failure_window_minutes` | 公网安全 |
| `public_web.security.global_failure_limit` | 公网安全 |
| `public_web.security.global_guard` | 公网安全 |
| `public_web.security.global_lock_minutes` | 公网安全 |
| `public_web.security.global_window_minutes` | 公网安全 |
| `public_web.security.lock_minutes` | 公网安全 |
| `public_web.security.session_binding` | 公网安全 |
| `public_web.socket` | 公网安全 |
| `public_web.socket_group` | 公网安全 |
| `server.port` | 数据库与备份 |
| `skills.enabled` | Skill |
| `skills.trusted_workspaces` | Skill |
| `system_prompt` | 高级策略 |
| `task_summary_bytes` | 上下文 |
| `tool_generation` | 只读元数据 |
| `tools.command_run` | 内置工具 |
| `tools.file` | 内置工具 |
| `tools.memory` | 内置工具 |
| `tools.network` | 内置工具 |
| `version` | 只读元数据 |
| `workspace` | 通用 |

## 控制操作到入口

| 操作 | GUI 目的地 |
| --- | --- |
| `attachments.delete` | 附件 |
| `attachments.set_policy` | 附件 |
| `auto.set_policy` | 高级策略 |
| `channels.set_policy` | 渠道 |
| `collaboration.set_policy` | 高级策略 |
| `commands.set_policy` | 命令执行 |
| `config.set_workspace` | 通用 |
| `conversation.branch` | 对话输入/导航 |
| `conversation.rename` | 对话输入/导航 |
| `conversation.set_capabilities` | 对话输入/导航 |
| `conversation.set_model` | 对话输入/导航 |
| `conversation.set_pinned` | 对话输入/导航 |
| `execution.set_file_scope` | 文件范围 |
| `execution.set_network` | 高级策略 |
| `execution.set_permission` | 权限与执行 |
| `execution.set_profile` | 权限与执行 |
| `execution.set_sandbox` | 高级策略 |
| `extensions.clear_data` | 扩展 |
| `extensions.import` | 扩展 |
| `extensions.remove` | 扩展 |
| `extensions.restart` | 扩展 |
| `extensions.set_policy` | 扩展 |
| `http.set_policy` | 高级策略 |
| `keys.probe` | 模型与 Key |
| `keys.remove` | 模型与 Key |
| `keys.set_profile` | 模型与 Key |
| `mcp.remove` | MCP |
| `mcp.restart` | MCP |
| `mcp.set_policy` | MCP |
| `mcp.set_profile` | MCP |
| `memory.set_pin` | Memory 与固定内容 |
| `memory.set_policy` | 高级策略 |
| `memory.set_selection` | Memory 与固定内容 |
| `model.remove` | 模型与 Key |
| `model.set_default` | 模型与 Key |
| `model.set_profile` | 模型与 Key |
| `person.set_permission` | 渠道 |
| `prompt.set_policy` | 高级策略 |
| `providers.set_enabled` | 模型与 Key |
| `public_web.set_policy` | 公网安全 |
| `runtime.set_limits` | 高级策略 |
| `server.set_policy` | 数据库与备份 |
| `skills.import` | Skill |
| `skills.remove` | Skill |
| `skills.set_policy` | Skill |
| `space.create` | 空间与会话 |
| `space.delete` | 空间与会话 |
| `space.move_conversation` | 空间与会话 |
| `space.rename` | 空间与会话 |
| `space.set_active` | 空间与会话 |
| `space.set_default` | 空间与会话 |
| `space.set_multi` | 空间与会话 |
| `space.set_permission` | 空间与会话 |
| `telegram.set_access` | 渠道 |
| `tools.set_enabled` | 内置工具 |

相关入口以 [功能映射](FRONTEND_FEATURE_MAP_V8_1.md) 和 [开发指南](FRONTEND_DEVELOPMENT_GUIDE_V8_1.md) 为准。公网继续保留受限查询和专用 remote 操作，不为覆盖本表放开本机 attach 管理接口。
