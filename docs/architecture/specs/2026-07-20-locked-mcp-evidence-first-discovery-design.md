# LockedMCP 证据优先发现设计

> S0 状态：`S2_SEMANTIC_SOURCE_ONLY`。仅供 S2 MCP 语义参考，不是当前规范或生产完成证明；当前架构权威见本目录 `README.md`。

日期：2026-07-20  
状态：用户已于 2026-07-20 书面确认方案 B，可进入实施  
适用范围：首次公开发布前的 LockedMCP alpha 契约纠正

## 1. 决策摘要

采用“方案 B：证据优先的原始分页结果”。

FreeAgent 不再接受调用方整理好的 Tool、Resource、ResourceTemplate 和
Prompt 条目作为发现事实。MCP Host 必须提交四类 `list` 方法的有界原始
结果页及其请求 cursor。SDK 校验完整 cursor 链，从原始页面唯一派生目录、
描述符摘要和 Prompt 参数契约，再冻结初始化证据、发现证据和执行锁。

本次属于未发布 alpha 契约的不兼容纠正：

- `LockedMCP.schema_version` 升为 3；
- `MCPDiscoveryEvidence.schema_version` 升为 2；
- LockedMCP v1、v2 均不具备执行资格；
- 不提供旧候选格式的兼容解码或语义回退；
- 候选 SQLite v2 测试数据后续由新构造器重建。

这项决定不改变核心架构。MCP 仍是可选外挂模块，纯聊天模式无需 MCP；
Role、Knowledge、Memory、Skill、Sidecar、Workspace 和 Agent 的独立装配
边界保持不变。SQLite 与 PostgreSQL 持久化同一份语言无关证据。

## 2. 为什么不保留当前结构

当前 `MCPDiscoveryInput` 只保存整理后的条目切片。它无法表示：

- `nextCursor`；
- 列表结果 `_meta`；
- 列表结果未知扩展字段；
- 首次请求与后续请求所使用的 cursor；
- 空终页；
- Host 是否沿 cursor 链读取到了终页。

因此，当前记录只能证明“某组条目被提交”，不能证明 Host 完成了 MCP
分页流程。该缺口不能通过给现有切片增加一个布尔值解决，因为布尔值仍是
调用方自行声明，而不是可重建证据。

## 3. 已拒绝的替代方案

### 3.1 可信 Host 目录投影

保留当前条目切片，由 Host 声明分页已经完成。

优点是修改量小。缺点是无法从持久化记录重建分页完成性，也无法绑定
`_meta`、cursor 和未知字段。它与“发现快照证明完整 MCP 发现响应”的设计
声明不一致，因此拒绝。

### 3.2 完整 JSON-RPC 传输转录

保存每个请求和响应的完整 JSON-RPC envelope、ID、时间及传输元数据。

该方案包含大量动态 ID 和非语义元数据，会放大持久化体积、降低提示缓存
稳定性，并把传输审计与运行装配耦合。分页证明并不需要这些字段，因此拒绝。

### 3.3 原始结果页加请求 cursor

保存稳定的请求 cursor 和原始 `result` 对象，不保存 JSON-RPC ID、时间或
传输头。该方案能够证明 Host 按 cursor 链读取到终页，又不会引入动态传输
噪声，因此采用。

## 4. 公开数据契约

下面的结构是语言无关契约的 Go 表示草图，不要求实现文件保持相同组织。

```go
type MCPListPage struct {
    // First page is JSON null. Later pages are the exact cursor returned by
    // the previous page. Pointer semantics preserve null versus empty string.
    RequestCursor *string         `json:"request_cursor"`
    Result        json.RawMessage `json:"result"`
}

type MCPDiscoveryInput struct {
    ToolPages             []MCPListPage `json:"tool_pages"`
    ResourcePages         []MCPListPage `json:"resource_pages"`
    ResourceTemplatePages []MCPListPage `json:"resource_template_pages"`
    PromptPages           []MCPListPage `json:"prompt_pages"`
}
```

每个 `Result` 是对应 MCP `List*Result` 的原始 result 对象，而不是 JSON-RPC
envelope。它必须完整保留：

- 必需的条目数组；
- 可选 `_meta`；
- 可选 `nextCursor`；
- 当前协议允许的未知字段。

已知字段按 MCP 2025-11-25 校验，未知字段只受通用 JSON 和资源上限约束，
不被旧 typed SDK 解码后重新序列化。

### 4.1 结果类型对应关系

| 页面字段 | MCP 方法 | 必需数组字段 |
|---|---|---|
| `tool_pages` | `tools/list` | `tools` |
| `resource_pages` | `resources/list` | `resources` |
| `resource_template_pages` | `resources/templates/list` | `resourceTemplates` |
| `prompt_pages` | `prompts/list` | `prompts` |

### 4.2 分页链规则

每一类页面独立满足：

1. 第一页 `request_cursor` 必须为 `null`。
2. 若第 N 页含 `nextCursor`，必须恰好存在第 N+1 页。
3. 第 N+1 页的 `request_cursor` 必须与第 N 页 `nextCursor` 字节相等。
4. 最后一页必须不含 `nextCursor`。
5. cursor 可以是空字符串；`null` 与空字符串不能混淆。
6. 同一方法的非首请求 cursor 不得重复。
7. cursor 链不完整、分叉、循环或多出页面时全部拒绝。
8. 页面顺序属于证据，不能排序。

这证明 Host 沿服务器返回的 cursor 链读取到了协议终页。它不声称能够证明
服务器没有故意隐藏条目。

### 4.3 Capability 与页面关系

- 声明 `capabilities.tools`：必须有至少一个 `tool_pages` 终结链。
- 未声明 `capabilities.tools`：`tool_pages` 必须为空。
- 声明 `capabilities.prompts`：必须有至少一个 `prompt_pages` 终结链。
- 未声明 `capabilities.prompts`：`prompt_pages` 必须为空。
- 声明 `capabilities.resources`：`resource_pages` 和
  `resource_template_pages` 都必须各有一个终结链；没有条目时提交空终页。
- 未声明 `capabilities.resources`：两类 Resource 页面都必须为空。

Tool 的 `execution.taskSupport` 与
`capabilities.tasks.requests.tools.call` 继续使用 Fix wave 3 的双层协商
规则。

## 5. 原始页面到冻结目录的派生

SDK 只能从规范化后的原始页面派生目录：

1. 顺序读取各页必需数组；
2. 对每个完整原始 descriptor 做 MCP 2025-11-25 已知字段校验；
3. 保留并规范化未知字段；
4. 校验同一目录的唯一身份；
5. 计算每个 descriptor 的领域隔离摘要；
6. 按冻结身份排序，形成稳定目录；
7. 分别计算 Tool、Resource、ResourceTemplate 和 Prompt catalog hash；
8. 以原始页面顺序计算 DiscoveryEvidence hash。

分页边界或未知页面字段变化会改变 DiscoveryEvidence hash；只改变服务器
返回顺序而不改变条目内容时，目录 hash 保持稳定，但发现证据 hash 会变化。

调用方不能同时提交派生条目，也不能覆盖派生摘要。

## 6. Prompt 参数契约

MCP Prompt wire descriptor 只有 `arguments`，不存在独立
`argumentSchema`。因此删除调用方可写的 `MCPPromptDiscovery.ArgumentSchema`。

SDK 从规范化的 `Prompt.arguments` 唯一派生参数 JSON Schema：

- 根类型为 `object`；
- 每个已声明参数对应一个 `type: "string"` property；
- 任一同名声明的 `required: true` 使该名字进入 `required`；
- `required` 按名字排序；
- `additionalProperties` 允许字符串值，以匹配 MCP 请求的字符串 map；
- title 和 description 留在原 descriptor，不复制到执行约束；
- 重复参数名不会产生多个 property，原始重复仍由 descriptor digest 绑定。

`LockedMCPPrompt` 在 v3 中使用
`ArgumentContractDigest`，由上述纯函数输出的 canonical JSON Schema
计算。外部输入不得提供该 Schema 或摘要。构造、CanonicalJSON、严格解码
和权威重放必须重新派生并核对同一摘要。

## 7. MCP 2025-11-25 兼容纠正

### 7.1 Unknown capability

`capabilities.extensions` 在 2025-11-25 中不是已知字段。它与其他未知
capability 一样原样保留并进入 InitializeEvidence hash，不套用后续协议
版本的 map-of-object 规则。

### 7.2 名称

Tool 名称字符集和 1–128 长度是 SHOULD 级建议，不作为 wire 拒绝条件。
Resource、ResourceTemplate、Prompt 和 PromptArgument 也不复用 Tool 名称
语法。

所有名称只要求：

- 是 JSON 字符串；
- 是有效 UTF-8；
- 受所在 64 KiB descriptor 与总证据上限约束。

策略层以后可以警告不符合推荐格式的 Tool 名称，但不能把警告伪装成
2025-11-25 wire 语法。

### 7.3 URI

Resource URI 在 wire 层按 2025-11-25 released schema 作为有界 JSON
字符串处理，并允许 fragment；发现层不另加 Tool 名称或传输 URL 语法。
ResourceTemplate 继续按 RFC 6570 校验。任何更窄的解析、协议或访问策略
必须在 Host/权限层明确表达，不能混入 wire 证据校验。

## 8. JSON Schema 2020-12 安全边界

### 8.1 递归兼容关键字

在可达 Schema 节点上显式拒绝 `$recursiveRef` 和 `$recursiveAnchor`。
它们是旧递归模型；本契约只允许受控的本地 `$ref` 与 `$dynamicRef`。
普通不被引用的 instance data 中同名字段仍保持透明。

### 8.2 `$id` 资源边界

Schema 资源索引必须按 URI 规则解析 `$id`：

- 空 `$id` 不创建新资源；
- `$id: "#"` 不创建新资源；
- 非空相对 ID 按父资源 base URI 解析；
- 非空绝对 ID 创建嵌入资源；
- 非空 ID 后的可选空 fragment 不改变其资源身份；
- 不同路径解析到同一资源 ID 时拒绝歧义；
- pointer 与 anchor 始终相对最近的有效资源边界解析。

### 8.3 兼容位置与 Pointer

- `additionalItems` 的 object/boolean 值按 pinned compiler 的兼容
  Schema 位置遍历；
- `definitions` 与 Schema-valued `dependencies` 保持现有处理；
- JSON Pointer 数组 token 只允许 `0` 或非零数字开头的十进制序列；
- `00`、`+0`、`-0` 和整数溢出全部拒绝；
- 所有 map key 排序后再索引、入队和报告错误，保证诊断确定性。

## 9. 资源上限与持久化闭包

继续采用：

- InitializeEvidence：1 MiB；
- DiscoveryEvidence：4 MiB；
- LockedMCP：4 MiB；
- 单个 descriptor/schema：64 KiB；
- JSON 深度：64；
- JSON 节点：32768；
- 每个目录条目：最多 256；
- 四类目录合计：最多 1024。

新增：

- 每类页面最多 256 页；
- 请求 cursor 最多 4096 字节；
- 所有原始页、cursor 和容器开销在 clone、parse、compile 前做
  overflow-safe aggregate preflight；
- 每个最终带 hash 的公开文档再次规范化并检查最终字节长度。

`NewMCPToolApprovalMapping` 必须在 clone 前检查权限和条目的聚合上限，并在
填入 `mapping_hash` 后检查最终 4 MiB 文档。`NewMemberModuleSnapshot` 必须在
填入 `snapshot_hash` 后检查最终 4 MiB 文档。任何成功构造的公开文档都必须
能够被对应严格 decoder 重新加载。

## 10. Hash 与版本

不兼容证据使用新的领域隔离：

- `freeagent.locked-mcp.v3`
- `freeagent.mcp-discovery-snapshot.v3`
- Prompt catalog 领域升级为 v2；
- Prompt 参数契约使用
  `freeagent.mcp-prompt-argument-contract.v2`。

旧 LockedMCP v1/v2、旧 DiscoveryEvidence v1 和旧 hash domain 不得被新
decoder、Host 或 ToolGateway 接受。不存在“结构看起来相同即可执行”的
兼容路径。

## 11. 构造、持久化与执行数据流

1. RuntimeCatalog 和 MemberModuleSnapshot 冻结 stage-one
   `EffectiveMCPServer`。
2. MCP Host 使用 stage-one server 完成 initialize。
3. Host 只对已协商 capability 调用对应 list 方法。
4. Host 逐页记录稳定请求 cursor 和原始 result，直到终页。
5. SDK 校验三类边界：initialize wire、分页发现 wire、跨文档 capability。
6. SDK 从原始页派生目录与 Prompt 参数契约。
7. Operator 审批映射只绑定派生 Tool descriptor/input-schema digest。
8. Repository 在同一事务中持久化 InitializeEvidence、
   DiscoveryEvidence、LockedMCP 和审批映射关联。
9. ToolGateway 只接受权威重建成功的 LockedMCP v3 record。

任何 initialize、分页、大小、Schema、审批或持久化失败都发生在工具调用
之前，不产生外部 Tool effect。

若运行期间收到协商过的 `list_changed`，Host 不得静默替换当前冻结目录。
生产 Host 集成阶段必须定义 fail-closed 的 stale-lock 处理和新目录审批
流程；该行为不由本次 assembly 构造器隐式决定。

## 12. 错误与恢复

- cursor 不连续、循环或无终页：发现失败，不生成锁；
- 页面超限或 descriptor 非法：发现失败，不生成锁；
- capability 与页面不一致：发现失败，不生成锁；
- Prompt 参数派生失败：发现失败，不生成锁；
- 最终持久化失败：不得把内存锁标记为可执行；
- 重启后任一文档无法严格解码、重算或交叉链接：Run 的 MCP 能力不可用；
- 不允许用 v1/v2 或新的服务器目录替代失败的 v3 记录；
- 上述失败是 pre-effect 失败，不进入 UNKNOWN 语义重放。

## 13. TDD 与验证矩阵

实现必须先增加失败测试并记录 RED，再修改生产代码。

### 13.1 分页证据

- 单页、空终页和多页成功；
- `_meta` 与未知字段保留；
- JSON 重排产生相同 canonical evidence；
- cursor 缺页、多页、错配、重复、循环、未终结；
- capability 缺失但有页面，以及 capability 存在但无页面；
- Resource 与 ResourceTemplate 两条独立链；
- 跨页重复身份；
- 页数、条目数、cursor、单项和总字节边界；
- 输入切片和 RawMessage 构造后突变不影响冻结记录；
- builder、CanonicalJSON、严格 decoder、ValidateAgainst 四条路径一致。

### 13.2 Prompt

- 无参数、可选参数、必需参数和 Unicode/空名字；
- 重复名字的稳定派生；
- 参数值只允许字符串；
- 不存在外部 ArgumentSchema 注入路径；
- descriptor 改动导致 descriptor/argument-contract 摘要变化并被重放拒绝。

### 13.3 Wire 与 Schema

- 2025-11-25 unknown `extensions` 任意 JSON 值保留；
- Unicode、空格和非 Tool 风格名称被 wire 层接受；
- fragment-bearing Resource URI 被接受；
- `$recursiveRef`/`$recursiveAnchor` 可达位置拒绝；
- `$id:"#"`、空、相对、绝对和末尾空 fragment；
- 资源内 pointer/anchor、循环与重复资源 ID；
- `additionalItems` dialect/anchor；
- 严格数组 pointer token；
- 多错误和重复 anchor 诊断在随机运行中稳定。

### 13.4 持久化闭包

- ApprovalMapping 最大边界 constructor→decoder 往返；
- 超界在 clone 前失败；
- MemberModuleSnapshot 填 hash 前后边界往返；
- v1/v2 真实旧形状被 v3 decoder 拒绝。

### 13.5 完成门禁

- focused RED/GREEN 证据；
- `go test ./sdk/moduleapi ./sdk/assembly -count=1`；
- `GOEXPERIMENT=jsonv2` 同矩阵；
- `-shuffle=on -count=20`；
- `go vet ./sdk/moduleapi ./sdk/assembly`；
- `go test ./... -run '^$' -count=1`；
- 新的独立协议、Schema 和持久化复审均为
  `0 Critical / 0 Important`。

## 14. 非目标

本规格不实现：

- MCP stdio/HTTP Host 与 ToolGateway 的生产接线；
- SQLite/PostgreSQL Repository；
- 数据库迁移工具；
- `list_changed` 的运行期状态机；
- MCP authorization/OAuth；
- 完整 JSON-RPC 传输审计；
- 对远端服务器诚实性的证明。

这些工作依赖本规格产生的稳定证据契约，在后续独立计划中实施。

## 15. 原始设计影响

不改变最初设计。

- 纯聊天仍可在零可选模块下运行；
- MCP、Skill、Role、Knowledge、Memory 和 Sidecar 仍按 Agent/Workspace
  自由装配；
- Core 只拥有证据、权限、预算、状态与审计，不拥有外部 MCP 服务器；
- 不引入 PAB；
- 不改变 85% 压缩、100% 顺序 Drop 的上下文策略；
- 不改变多 Workspace 公平调度、UNKNOWN 禁止语义重放或外部 effect
  终态规则；
- 仅把 MCP 的“完整发现”从可信声明升级为可重建证据。
