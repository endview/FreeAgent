# 旧架构规格索引

> 状态：`HISTORICAL_INDEX_NON_NORMATIVE`
>
> 本目录原有的 11 份规格已经退出当前架构权威。它们只能用于 S0 语义提取或
> S2 设计参考，不再指导当前实现、Schema、生产接线或发布完成度判断。

当前两份长期编码规格是：

- [`CORE_RUNTIME_V1`](../../specs/CORE_RUNTIME_V1.md)
- [`CURRENT_STORE_V1`](../../specs/CURRENT_STORE_V1.md)

一次性切换清单是：

- [`CUTOVER_ACCEPTANCE`](../../CUTOVER_ACCEPTANCE.md)

其中 Core 和 Current Store 已形成 `S1_ACCEPTED_DEVELOPMENT_BASELINE`，Store
Schema 仍是首次公开发布前的 Draft。项目从未正式部署，因此本轮生产切换为
`NOT_APPLICABLE`；它们不表示首次生产部署或公开发布已经批准。

## 文件状态

| 文件 | 状态 | 允许用途 |
|---|---|---|
| `2026-07-18-compatibility-remediation-b-design.md` | `HISTORICAL_NON_NORMATIVE` | 兼容、迁移和 fail-closed 语义回溯 |
| `2026-07-19-task3-lossless-persistence-design.md` | `HISTORICAL_NON_NORMATIVE` | 无损、幂等、恢复和终态语义回溯 |
| `2026-07-20-locked-mcp-evidence-first-discovery-design.md` | `S2_SEMANTIC_SOURCE_ONLY` | MCP 发现、版本和权限语义参考 |
| `2026-07-20-storage-backend-b-design.md` | `HISTORICAL_NON_NORMATIVE` | 后端抽象经验回溯 |
| `2026-07-21-runtime-catalog-authority-design.md` | `HISTORICAL_NON_NORMATIVE` | Catalog、不可变引用和本地授权语义回溯 |
| `2026-07-22-governance-rule-wire-design.md` | `HISTORICAL_NON_NORMATIVE` | 已替代草案的审查轨迹 |
| `2026-07-22-governance-rule-wire-v1-lock.md` | `HISTORICAL_NON_NORMATIVE` | 权限裁剪和 fail-closed 语义回溯 |
| `2026-07-22-p0-spec-lock-attestation.md` | `HISTORICAL_NON_NORMATIVE` | 当时的审计记录 |
| `2026-07-26-stage2-unified-assembly-cutover.md` | `HISTORICAL_NON_NORMATIVE` | 旧生产断点与风险回溯 |
| `2026-07-28-stage3-sealed-content-parents.md` | `S2_SEMANTIC_SOURCE_ONLY` | 内容寻址和完整性语义参考 |
| `2026-07-28-stage3-skill-lifecycle-lock.md` | `S2_SEMANTIC_SOURCE_ONLY` | Skill 版本、审核和生命周期语义参考 |

## 使用规则

1. 不得根据本目录文档新增旧 evidence、shadow、compatibility projection 或
   Skill-specific Core 链。
2. 不得把旧文档中的 `LOCKED`、`PASS`、`stable` 或“执行中”解释为当前状态。
3. 需要保留的语义必须重新写入两份当前编码规格、一次性切换清单，或明确登记为
   S2 工作包。
4. README、PRD、CI 和生产代码不得再把本目录文件作为当前架构规范。
