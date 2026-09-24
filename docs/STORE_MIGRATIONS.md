# Current Store Migration Operations

本文件描述 Current Store 的显式前向迁移边界。普通 `chat`、`serve`、只读观察和
`OpenExistingCurrentStore` 只接受当前版本，永远不会自动初始化、修复或迁移数据库。

## 当前基线

- 当前 schema 家族：`github.com/endview/freeagent/current-store-v2`（FAC2）
- `PRAGMA application_id`：`1178682162`（`0x46414332`，ASCII `FAC2`）
- 当前 `UserVersion`：`2`
- 已知版本：`1 -> 9f4f146f3b914f434f12d61245e120e2e2806dcc48256e55744ddc55f29ba561`；`2 -> d5d876f327dc29dc6f4a10476652641172ab8e1f0451a8714fc450f58733541e`
- 冻结 bootstrap migration：`migrations/fac2/0001_current.sql`，149,239 bytes
- 冻结 migration SHA-256：`dbc3e724a1f7c030677c84a77a317f69ef2fe246985cc749559a9f3dd5a6dc5a`
- 当前真实 `0002_server_owned_review.sql` 为 7,173 bytes / SHA-256 `3091a49ebcf724f573f91cc0fd22a7c58ebb52fa9d7ed552e32b6526ebeca3cb`，把 Review projection 绑定到 server-owned Admission、operator principal 与 request digest。对 v1 执行 `migrate` 必须先生成并验证完整备份，再在 owner lease 下应用 `0002`。

## FAC1 边界

删除金额预算体系之前的 FAC1（`current-store-v1`，43 表，migration `migrations/0001_current.sql`，
150,301 bytes / SHA-256 `6def433a59fa8f4876894572f1610cae499abc5b389ab5930ea697055419ca86`）是另一个
schema 家族，不是 FAC2 的低版本。

- FAC1 数据库在 `PRAGMA application_id` 这一步即失败关闭并返回 `IdentityError`，不读取
  `user_version`，不获取 writer，也不写入任何字节。
- 不存在 `FAC1 -> FAC2` migration step，且不会新增；金额字段是删除而不是置零或改名，
  没有可逆的前向转换。
- `migrations/0001_current.sql` 保持字节冻结，只作为历史证据存在，不参与任何运行路径。
- 负例证据：`TestFAC1StoreIsRejectedReadOnlyWithoutModification`、
  `TestFAC1MigrationBytesRemainFrozen`。

## 显式迁移

必须先停止使用该 Store 的 `serve` 和其他 writer，再执行：

```powershell
freeagent migrate `
  --db <store> `
  --artifact-root <artifact-root> `
  --backup-out <backup-out>
```

该命令在一个 callback-scoped owner lease 内完成以下顺序：

1. 识别并校验已注册的 `UserVersion -> schema fingerprint`。
2. 创建完整 backup bundle，并在发布前重新验证数据库与 artifact 闭包。
3. 依次执行所有已注册、连续、只前向的 migration step。
4. 每一步在独立 `BEGIN IMMEDIATE` 中执行，提交前重算目标 fingerprint。
5. 用当前完整语义校验器验证最终 Store，再释放 owner lease。

如果备份目标已存在、备份失败、Store 正在使用、版本未知、迁移链不连续或 fingerprint
不匹配，命令失败关闭，不会绕过备份继续迁移。

## Restore 行为

Backup manifest 保留生成时的 `user_version` 与 fingerprint。`restore` 接受本二进制注册的
已知版本；原始 bundle 先按其版本验证。若版本落后，迁移只发生在尚未发布的私有 staging
数据库上。迁移后的数据库与 artifact 闭包再次验证后，数据库才作为最后的 commit marker
原子发布。原 bundle 字节保持不变。

只读恢复会保留已经终结的 Model Attempt 参数并逐字节校验其冻结闭包；它不会把旧的空参数解释为
新的调用许可。任何新建或重试 dispatch 仍必须经过当前显式 `max_tokens` 与 output reserve 准入，
旧 Attempt 不能通过恢复兼容路径重新调用 Provider。

## 增加下一版本

真实 schema 变化必须同时完成以下事项：

1. 保持 `migrations/fac2/0001_current.sql` 字节不变，新增连续命名的 `0002_*.sql`。
2. 将 `UserVersion` 递增到 `2`，登记 v2 的已知良好 fingerprint。
3. 在 `migrationSteps` 注册唯一的 `1 -> 2` loader，不允许跳版本或回退步骤。
4. 保留 v1 的只读身份与语义验证能力。
5. 使用真实 v1 backup 执行 `backup-verify -> restore -> migrate -> current semantic verify`。
6. 更新生成事实并运行 `go generate ./...`，不得手工改写生成文件。

没有真实 schema 变化时不得创建占位 migration，也不得只修改 `migrations/fac2/0001_current.sql`
后重算哈希。`0001_current.sql` 已字节冻结；后续变化必须追加连续 migration，并更新运行事实和恢复闭包。
