# 扩展状态契约 v2

承载协议仍为 `freeagent-ext/1`；有状态能力显式使用 `state_version: 2`。无状态工具不必升级协议。Core 与扩展 initialize 都声明状态修订，不匹配时拒绝，不执行旧状态处理器。

## 声明

```json
{
  "state": true,
  "state_version": 2,
  "storage": "postgresql",
  "storage_schema": "project_labels",
  "storage_version": "1",
  "state_operations": {
    "set_label": {
      "scope": "actor",
      "model": true,
      "input_schema": {
        "type": "object",
        "properties": {"label": {"type": "string", "maxLength": 128}},
        "required": ["label"],
        "additionalProperties": false
      }
    }
  }
}
```

最多 32 种操作；每份 payload schema 不超过 32 KiB，顶层必须是封闭 object。scope 只能是 global/space/actor。`model` 默认 false；外部模型即使声明为 true，也不能修改全局或其他参与者。Core 检查调用范围，扩展检查业务内容与资源授权。

## 预览和提交

`extensions.change_state` 输入示例：

```json
{
  "id": "com.example.labels",
  "scope": {"space_id": "local", "resource": "project-one", "actor_id": "owner"},
  "action": "set_label",
  "expected_revision": 3,
  "payload": {"label": "reviewed"}
}
```

Core 绑定 core_id 与会话空间，加入 `contract: 2`、操作声明摘要 `declaration_hash`。模型工具提案还绑定真实原 Run、已采用输入的 `origin.run_id/input_id`；模型不能通过请求参数指定可信执行身份。排队未采用输入不提供身份。`resource` 是有界不透明标识，不是文件路径或 SQL。

`state.preview` 接收该完整信封，返回 revision/before/after/summary。用户确认绑定具体 payload、范围、声明和预览。确认前复查原身份、当前权限、声明和 before/after；变化后重新预览。模型提出的持久变更仍必须确认。声明了 settings_schema 的扩展，其持久配置在用户和模型的控制预览入口均校验 schema；本轮设置还需属于声明的有限选项。

`state.apply` 接收 `{operation_id,digest,change}`；扩展在同一事务保存状态与原操作结果。`state.result` 只查询 `{operation_id,digest}`。原操作回执丢失保留 UNKNOWN；核对结果不能重放 apply、模型或工具。

`state.read` 接收 Core 绑定的 scope 与当前扩展 settings。角色、人物、记忆、关系等业务字段只存在于扩展 payload/返回值中。variant 和 settings 由原 Run 冻结，默认人物由扩展自己解释。事实同步使用每个原 Run 的选择与设置，不用当前人物重新解释历史。

## 存储、更新和恢复

PG 仍是唯一数据库。一个扩展声明一个普通 schema，使用独立数据库/受限角色；不授予 Core 或其他扩展库权限。禁止 fa/public/系统 schema、保留前缀及可拼接 SQL 的名称。初始化是用户显式管理操作，不发生在普通聊天或未启用扩展的启动中。

AffectWeave 声明 `aw` 和存储格式 `1`；更新不移动人物数据。连接文件绑定扩展 ID、Core 身份、数据库、角色、schema 和格式。旧连接文件补全绑定必须在停 Owner 后显式执行：

```text
freeagent ext storage-preflight --id cc.endview.affectweave --data-dir <data>
```

该操作验证现有唯一业务 schema、属主及受限角色，再只补元数据。声明冲突、多个候选或未核清的已派发写入会阻止更新；普通聊天不猜测旧 schema。

配套备份格式为 `freeagent-extension-pair-v2`，包括 schema、存储/状态版本、配套扩展版本、Core 备份、来源与原操作记录。只有限暂停扩展写回，Core 任务继续。恢复使用新目标、匹配包和受限角色，从声明 schema 的归档恢复对象与数据，不由 Core 初始化或枚举业务表。未知对象范围、损坏或来源冲突拒绝恢复；失败保留有效 Core，扩展保持停用。

旧待确认提案在新版本重新预览；已派发 UNKNOWN 先用原配套包核对，不能翻译执行。旧备份继续保留，恢复它时使用原配套包。停用不删数据，协议不构成 OS 沙箱。
