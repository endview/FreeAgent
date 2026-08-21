# FreeAgent P0 规范锁定证明

> S0 状态：`HISTORICAL_NON_NORMATIVE`。仅保存当时的审计记录，不是当前架构权威或生产完成证明；当前架构权威见本目录 `README.md`。

日期：2026-07-22  
状态：PASS / LOCKED

## 1. 证明范围

本证明只确认 P0 Governance rule wire v1 的规范闭包、关系清单和锁定过程。它不表示规范中描述的 P2–P5 生产能力已经实现，也不授予任何运行时权限。

锁定对象：

- 父规范：[`2026-07-21-runtime-catalog-authority-design.md`](2026-07-21-runtime-catalog-authority-design.md)
- 锁定规范：[`2026-07-22-governance-rule-wire-v1-lock.md`](2026-07-22-governance-rule-wire-v1-lock.md)

## 2. 最终摘要

| 对象 | SHA-256 |
| --- | --- |
| 父规范 | `2e360594f8c8d93790c0dd583daade602d3ca5285ed98abfc75e0c4e52598495` |
| 已独立复审的候选版本 | `40c90fb1e1b9e2e8852718b73ea16274adead52413c4ce73dd93a6bc4449f07d` |
| 最终 LOCKED 版本 | `4324cd5048fb213413e6e418c4a2b0cda44c316300a75aa8e99a22980ccf0269` |

最终关系清单：

| 指标 | 值 |
| --- | ---: |
| Relations | 448 |
| PA / PH / EL / D | 322 / 88 / 26 / 12 |
| PA / PH / EL / 全量边界 | 322 / 410 / 436 / 448 |
| Canonical identity bytes | 143,771 |
| Framed SHA-256 | `d70c39d30e8efa3375304a5e28a8de46c17b7e2113d7170c63aff94f2bba4e9f` |

当时受检入的结构 seed 位于
`internal/store/transfer/checked_in_relation_inventory_seed.go`；该旧 Store/transfer
文件已在 S1 物理退役，只能从 S0 离线源码归档追溯。它当时也只证明关系
wire/class 清单，不是 descriptor、ImportPlan、transfer root 或 feature authority。

## 3. 独立复审结论

锁定前完成了两条相互独立的复审路径：

- 模型、权限、CoreModel、UNKNOWN、结算和 sender closure：`0 Critical / 0 Important / 0 Minor`；
- 内容、存储、Context、MCP、provenance、purge/tombstone 和 seed 重算：`0 Critical / 0 Important / 0 Minor`。

两条路径均独立重算出 448 条关系、143,771 identity bytes 和相同的 framed SHA-256，并确认旧的 444 条关系、旧 identity 长度、旧摘要及旧 D 数量没有残留。

## 4. 锁定差异证明

已复审候选版本的第 3 行是：

```text
状态：方案 B 4B2 实施前闭包修订候选（2026-07-22，替代同日旧草案；完成独立复审前不得标记 locked/stable）
```

最终版本只把该行改为：

```text
状态：LOCKED（2026-07-22；模型/权限与内容/存储独立复审均为 0 Critical / 0 Important / 0 Minor）
```

可复算结果：

- 在内存中把最终文件第 3 行恢复为旧状态行，完整文件 SHA-256 精确恢复为 `40c90fb1e1b9e2e8852718b73ea16274adead52413c4ce73dd93a6bc4449f07d`；
- 分别删除新旧文件的第 3 行后，正文均为 217,696 bytes；
- 两份正文 SHA-256 均为 `367007ed29ee6d24ac04f84c63724e06d8f092b82e78499dbfb337dbe7081fca`，且逐字节相等；
- 父规范在锁定前后保持同一 SHA-256。

两条独立复审路径随后再次检查该差异，均返回 `0 Critical / 0 Important / 0 Minor`，确认 `LOCKED` 状态有效且没有语义正文变化。

## 5. 机械验证

本轮已通过：

- checked-in relation inventory seed 的精确传输测试；
- transfer 包及全仓 `go vet`；
- 普通全量测试与 `-shuffle=on` 全量测试；
- SHA 门禁、关系数量、分类边界、canonical bytes 和 framed digest 重算。

## 6. 对最初设计的影响

本次锁定只冻结可信内核的身份、权限、证据、账本、外部效果和恢复边界，不把 Role、Knowledge、Memory、Skill、MCP、人格或领域知识强制装入核心。

因此以下原始设计保持不变：

- Agent、Workspace、Profile 与可选模块继续自由组合；
- `pure_chat` 继续允许零可选 provider/content/secret/host；
- Skill 不是隐式执行权限；
- CoreModel 仍位于 RuntimeCatalog 之外；
- UNKNOWN 仍禁止语义重放；
- 任何特定人格或情感系统仍不进入核心；
- SQLite 仍是默认后端；
- P2–P5 必须分别实现和验收，不能由本次规范锁定自动晋级。
