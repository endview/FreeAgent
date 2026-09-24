# 发布供应链输出

> 性质：发布控制平面的操作说明，不是 Runtime 编码规格、模块协议或生产切换完成
> 证明。当前编码边界只由 `CORE_RUNTIME_V1` 与 `CURRENT_STORE_V1` 定义；实际切换
> 状态只由 `CUTOVER_ACCEPTANCE` 判定。

## 当前发布边界

`v0.1.1` 已作为早期 Developer Preview 发布。六平台归档（Darwin build-only）、
SPDX SBOM、checksum、unsigned provenance 和外层 `SHA256SUMS` 已生成、验证并上传到
[GitHub Release](https://github.com/endview/FreeAgent/releases/tag/v0.1.1)；
Windows/Linux AMD64 已完成原生系统上的离线 package smoke 与
backup/verify/restore/continue。发布归档对应提交 `67d305316204f7dd77f51bc905ea863ec181fced`，
版本/revision 与供应链集合已在发布前复核。归档、SBOM、provenance 和 checksum
manifest 仍未签名，且该 Developer Preview 不代表生产支持、公开 Beta 或 `RELEASE_READY`。
旧的 `v0.1.0-dev.2` 候选记录仅用于历史证据，见
[`RELEASE_CANDIDATE_2026-09-23`](RELEASE_CANDIDATE_2026-09-23.md)。

当前 P4 管理面只提供 bounded read models：UNKNOWN、Store Verify、Backup constraints、
server-owned Artifact Admission 与 Review/Decision/Artifact 查询均为只读；不在线创建
Backup、不在线 Restore，不接受服务器路径、Secret、签名材料或 replay material，也不
允许 UNKNOWN resend、replay、换 Provider 或隐式 retry。当前运行时没有重新引入预算、
金额、价格或费用字段；Provider 支持范围仍以 exact `zhipu` / `glm-4.5` 等已登记
矩阵为准，不扩展为全部 GLM 模型或任意 endpoint。

FreeAgent 的 CI 发布任务在完成测试或构建后，会为每个非 `permanent`
任务生成一组供应链文件。它们只存在于仓库外的 artifact root，不会写回
source、clean staging 或 Git 工作树。

## 输出集合

每个上传制品包含原有测试、race 或二进制载荷，以及以下三个公共文件：

| 路径 | 作用 |
|---|---|
| `supply-chain/sbom.spdx.json` | SPDX 2.3 JSON SBOM，记录主包、Go 模块、随发布分发的文件及许可证关系 |
| `supply-chain/provenance.unsigned.v1.json` | in-toto Statement v1 形状的 SLSA Provenance v1 描述，绑定本次 revision、任务类型、目标平台、输入清单、载荷和 SBOM |
| `supply-chain/checksums.sha256` | 对载荷、SBOM 和 unsigned provenance 的 SHA-256 清单 |

三类文件使用 UTF-8、无 BOM、LF 结尾。JSON 采用确定性键顺序和紧凑编码；
checksum 行按 artifact-root-relative 路径的 ordinal 顺序排列，格式固定为：

```text
<64 位小写 SHA-256><两个空格><artifact-root-relative path>
```

`checksums.sha256` 不包含自身，避免自引用。完整 artifact root 的
`ArtifactSetSha256` 由控制器另行计算并写入任务输出及阶段回执，因此也不会被
放回自身哈希集合中。

## 生成和上传顺序

非 `permanent` 任务采用固定顺序：

1. 验证基础载荷与 clean staging；
2. 从已验证载荷、固定依赖许可证清单、分发文件清单、`go.mod`、`go.sum`
   和外部 module cache 生成 SBOM；
3. 生成绑定载荷与 SBOM 的 unsigned provenance；
4. 生成 checksum 清单；
5. 由基础载荷快照和三个生成文件的精确路径、长度、SHA-256 在内存中计算
   `ExpectedArtifactSetSha256`，发布前再次确认基础载荷未变化；
6. 重新验证包含供应链文件的完整 artifact root；结果必须与
   `ExpectedArtifactSetSha256`、预期文件数和总字节数完全一致；
7. 若进入已批准的公开发布任务，上传整个 artifact root；本地 Developer Preview
   只保留在本机，不执行上传；
8. 无论上传是否成功，都以冻结摘要重新验证本地集合并写入 Seal 回执。

供应链目录必须原先不存在。生成器先在受信临时目录创建和回读三个文件，再以
同卷目录重命名发布；已有目录、部分输出、输入在生成期间变化、路径逃逸、
reparse traversal、重复 JSON 键、清单不匹配或摘要不匹配都会失败关闭。
Generate 到最终 Verify 之间发生的载荷或元数据替换也不能产生新的可接受摘要，
因为 Finalize 只能使用生成器预先返回的 expected set pin。

## 本地校验

在解压后的 artifact root 中，可先校验 checksum：

```bash
sha256sum --check supply-chain/checksums.sha256
```

随后至少确认：

- SPDX 文档的 `spdxVersion` 为 `SPDX-2.3`；
- provenance 的 `_type` 为 `https://in-toto.io/Statement/v1`；
- provenance 的 `predicateType` 为 `https://slsa.dev/provenance/v1`；
- provenance 的 `subject` 与实际载荷及 SBOM 的名称、SHA-256 完全一致；
- checksum 清单没有重复路径、绝对路径、反斜杠或
  `supply-chain/checksums.sha256` 自引用；
- CI 记录的 `artifact_set_sha256` 与 Seal 阶段重新计算值相同。

只拿到下载文件时，可以验证文件内部的完整性闭包；要验证它确实对应某次 CI
运行，还必须同时核对该运行保存的 revision、任务输出和阶段证据。

## 明确的安全边界

`provenance.unsigned.v1.json` 是未签名的描述文件，不是签名 attestation，
不提供来源身份认证，也不证明任何 SLSA 构建级别。文件内明确声明：

```text
authentication=none
envelope=absent
securityClaim=informational-only
```

checksum 能发现内容变化，但攻击者若能同时替换文件和 checksum，单靠该清单
不能证明真实性。当前 R0-6C 也不声称解决 Finalize、上传服务和 Seal 之间的
服务器端摘要绑定；签名、GitHub artifact attestation、服务端下载复核及剩余
同 runner TOCTOU 属于后续 R0-6E。

参考规范：

- [SPDX specification](https://github.com/spdx/spdx-spec)
- [in-toto Attestation Statement v1](https://github.com/in-toto/attestation/blob/main/spec/v1/statement.md)
- [SLSA specification](https://github.com/slsa-framework/slsa)

## 对核心架构的影响

本机制只位于 CI/release 控制平面，不参与 Agent 运行时装配，也不授予任何新
权限。它不改变 Agent、Workspace、Profile、Role/Persona、Memory、
Knowledge/RAG、Skill、可信 `TRUSTED_IN_PROCESS` Module、显式 MCP 扩展或 Channel
的可选组合，不改变默认 `pure_chat`、Model Provider、SecretRef、UNKNOWN、
上下文压缩/有序 Drop 或 cold-family 策略。
