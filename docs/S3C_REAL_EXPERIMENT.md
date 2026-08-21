# S3-C 真实实验单 Cell 操作说明

本文说明如何使用 `scripts/Invoke-S3CRealCell.ps1` 执行一个可审计的 S3-C
实验 cell。该脚本是 Operator 层安全封装，不是新的 Runtime、Store、Loop、Gateway、
Scheduler 或实验账本。

> 当前裁决（2026-08-05）：本轮模型效果矩阵已在 `3 COMPLETE + 1 PARTIAL` 处停止；另有
> 一个未进入模型边界的失败物理 cell 记录。剩余六个效果矩阵位置不得启动，S3-C 未验收，
> S3-D 不得开始。第 4 节只保留冻结时的历史实验协议，不是当前可执行清单；终态以第 8.2 节
> 为准。

## 1. 边界与不变量

一次脚本调用只创建并执行一个 cell，顺序固定为：

```text
capacity preflight -> freeze inputs -> init -> s3-eval (exactly once) -> backup -> backup-verify
```

- cell 目录通过 Windows 排他创建；已存在的 `CellId` 会直接失败，绝不覆盖。
- 输出根与未来 cell 必须和全部输入 source/artifact tree 不相交，避免递归快照污染。
- `freeagent` 二进制、seed、scenario，以及 seed 中全部
  `artifact_relative_path` 引用都会复制到 cell 并计算 SHA-256。
- 创建 cell 前，Runner 会在 `OutputRoot` 所在卷检查可用空间。最低要求是去重后的
  已验证待冻结输入（二进制、seed、scenario 和引用 artifact）总字节再加 256 MiB
  安全余量；不足时以 `OUTPUT_SPACE_INSUFFICIENT` 退出，不创建 cell、不调用
  `freeagent`，也不会换盘、删文件或重试。
- `init` 和 `s3-eval` 只使用冻结副本，不再读取源文件。
- API key 只能由指定的进程环境变量解析，既不是命令行参数，也不会写入 plan。
- `s3-eval` 只调用一次。非零退出、`MODEL_UNKNOWN`、部分报告或超时均不自动重跑。
- stdout 和 stderr 按原始字节分别捕获。合法 S3-C 部分报告也不重新序列化。
- `exit.json` 是 cell 内唯一的终态文件，只创建一次；脚本不维护可变实验账本。
- `work/` 原始 Current Store 与 artifact tree 始终保留，脚本不做自动清理。

公开参数只有二进制、seed、scenario、输出根、cell ID、Scheduler 配置、重复次数和
密钥环境变量名。脚本不接受密钥值。

## 2. 前置条件

1. Windows PowerShell 5.1。
2. 已构建的独立 `freeagent.exe`。
3. 一个 S3-C bootstrap seed 和一个 S3-C scenario。
4. seed 引用的 artifact 必须位于 seed 所在目录之下，且不能经过符号链接或其他
   reparse point。
5. 运行者已通过可信方式把 DeepSeek key 注入当前进程环境。不要使用命令行明文、
   `.env`、`setx` 或 PowerShell transcript。
6. 真实实验建议把 `OutputRoot` 明确放在有足够容量的 D 盘目录；固定的 256 MiB
   只是启动安全余量，不是整个实验的容量上界。Current Store、模型捕获和归档仍会继续
   增长，Operator 应在每个 cell 前检查 D 盘剩余空间。

交互式本地终端可用下面的无回显方式注入。示例只给出环境变量名，不包含密钥值：

```powershell
$secure = Read-Host 'DeepSeek API key' -AsSecureString
$pointer = [Runtime.InteropServices.Marshal]::SecureStringToBSTR($secure)
$plain = $null
try {
    $plain = [Runtime.InteropServices.Marshal]::PtrToStringBSTR($pointer)
    [Environment]::SetEnvironmentVariable(
        'FREEAGENT_DEEPSEEK_TEST_KEY',
        $plain,
        'Process'
    )
} finally {
    if ($null -ne $plain) { $plain = $null }
    [Runtime.InteropServices.Marshal]::ZeroFreeBSTR($pointer)
}
```

实验完成并退出父进程后，该进程环境随之消失。若当前会话曾开启 transcript，应先关闭
并换用未记录的新会话。

## 3. 单 Cell 调用

Pilot 默认 `Repetitions=1`；脚本硬上限为 3。一个 cell 内的重复仅由产品
`s3-eval` 在上一轮证据完整且成功时继续，Operator 封装不会补跑。

Scheduler 关闭：

```powershell
$repo = (Resolve-Path '.').Path
$runner = Join-Path $repo 'scripts\Invoke-S3CRealCell.ps1'
$freeagent = Join-Path $repo 'bin\freeagent.exe'
$evidence = Join-Path $env:LOCALAPPDATA 'FreeAgent\evidence\s3c'
powershell.exe -NoLogo -NoProfile -NonInteractive -ExecutionPolicy Bypass `
  -File $runner `
  -FreeAgentPath $freeagent `
  -SeedPath (Join-Path $repo 'examples\s3c-deepseek-v4-pro-reviewer-off.bootstrap.seed.json') `
  -ScenarioPath (Join-Path $repo 'examples\s3c-architecture-clean.scenario.json') `
  -OutputRoot $evidence `
  -CellId pro-reviewer-off-clean-scheduler-off-r01 `
  -SchedulerMode Off `
  -Repetitions 1 `
  -SecretEnvironmentName FREEAGENT_DEEPSEEK_TEST_KEY
```

Scheduler 开启时必须同时冻结三个正整数限制，Workspace/family 限制不能大于全局限制：

```powershell
powershell.exe -NoLogo -NoProfile -NonInteractive -ExecutionPolicy Bypass `
  -File $runner `
  -FreeAgentPath $freeagent `
  -SeedPath (Join-Path $repo 'examples\s3c-deepseek-v4-pro-reviewer-on.bootstrap.seed.json') `
  -ScenarioPath (Join-Path $repo 'examples\s3c-architecture-trap.scenario.json') `
  -OutputRoot $evidence `
  -CellId pro-reviewer-on-trap-scheduler-on-r01 `
  -SchedulerMode On `
  -SchedulerGlobalWorkers 4 `
  -SchedulerWorkspaceWorkers 2 `
  -SchedulerFamilyWorkers 2 `
  -Repetitions 1 `
  -SecretEnvironmentName FREEAGENT_DEEPSEEK_TEST_KEY
```

每次对照实验必须使用新的 `CellId`。不要通过删除或重命名旧 cell 来复用身份。

## 4. 对照矩阵（历史冻结协议）

公平调度对照固定 model、reviewer、scenario、repetitions 和并发限制，只改变
`SchedulerMode=Off/On`。Reviewer 效果对照固定 model、scenario、Scheduler 开启状态及
限制，只更换 reviewer-off/reviewer-on seed。

本轮 Flash 与 Pro 的所有 cell 固定共用 `FREEAGENT_DEEPSEEK_TEST_KEY`，以保持测试账号身份
一致；cell plan 只记录这个变量名，不记录值。Runner 仍只接受可移植格式
`[A-Za-z_][A-Za-z0-9_]*`，未来若更换凭据也不得把值写入命令、seed 或证据。

本轮原定由 Operator 逐个执行 pilot cell 并检查终态，再决定是否人工发起下一个 cell。
控制台不得根据统计结果自动生成预热请求或自动重放失败/UNKNOWN 任务。该协议现已被
第 8.2 节的停止裁决截断。

### 4.1 冻结的历史 Pilot 清单（不得继续执行）

Pilot 最多包含 10 个**模型效果矩阵位置**，全部 `Repetitions=1`；这不等同于物理 cell
目录数。若某个物理 cell 在模型调用前失败，它不计入效果矩阵，但其身份仍永久保留且不得
复用。冻结协议要求严格按下表顺序手工发起，只有紧邻的前一个矩阵位置为 `COMPLETE` 才可
继续；任何 `PARTIAL`、`NO_VALID_REPORT`、UNKNOWN、超时或其他非 COMPLETE 终态都会停止
剩余位置并先进行人工对账。该表现在只记录历史协议，不是矩阵执行器、队列、账本或继续
执行授权。

| 顺序 | 组 | 模型 | 场景 | Reviewer | Scheduler | 限制 G/W/F | Attempt 上限 | Cell ID |
|---:|---|---|---|---|---|---|---:|---|
| 1 | Fairness | Flash | clean | off | off | 0/0/0 | 12 | `pilot-f01-flash-clean-reviewer-off-sched-off` |
| 2 | Fairness | Flash | clean | off | on | 2/1/1 | 12 | `pilot-f02-flash-clean-reviewer-off-sched-on` |
| 3 | Reviewer | Flash | clean | off | on | 4/2/2 | 12 | `pilot-r01-flash-clean-reviewer-off-sched-on` |
| 4 | Reviewer | Flash | clean | on | on | 4/2/2 | 15 | `pilot-r02-flash-clean-reviewer-on-sched-on` |
| 5 | Reviewer | Flash | trap | off | on | 4/2/2 | 12 | `pilot-r03-flash-trap-reviewer-off-sched-on` |
| 6 | Reviewer | Flash | trap | on | on | 4/2/2 | 15 | `pilot-r04-flash-trap-reviewer-on-sched-on` |
| 7 | Reviewer | Pro | clean | off | on | 4/2/2 | 12 | `pilot-r05-pro-clean-reviewer-off-sched-on` |
| 8 | Reviewer | Pro | clean | on | on | 4/2/2 | 15 | `pilot-r06-pro-clean-reviewer-on-sched-on` |
| 9 | Reviewer | Pro | trap | off | on | 4/2/2 | 12 | `pilot-r07-pro-trap-reviewer-off-sched-on` |
| 10 | Reviewer | Pro | trap | on | on | 4/2/2 | 15 | `pilot-r08-pro-trap-reviewer-on-sched-on` |

命名模板为
`pilot-{f|r}{两位顺序}-{model}-{scenario}-reviewer-{off|on}-sched-{off|on}`。
Fairness 的 scheduler-on cell 与 Reviewer 组中的 Flash/clean/reviewer-off cell 即使配置
相近，也保持为两个独立 cell，不复用身份或证据。

上限计算如下：Fairness 为 `2 x 12 = 24`；Reviewer 组包含四个
model/scenario 组合，每个组合为 `12 + 15 = 27`，合计 `4 x 27 = 108`；整个 Pilot
最多 `24 + 108 = 132` 个模型 Attempt。12 表示三个 Workspace 各最多 3 个 Specialist
加 1 个 Root；Reviewer-on 额外增加每 Workspace 1 个 Reviewer，因此为 15。失败、
REJECT 或 UNKNOWN 可能使实际 Attempt 数少于上限，绝不能为“补齐数量”而重跑。

该清单是 pilot，不把固定执行顺序误当成因果实验。Provider 缓存状态、网络负载和服务端
排队不受 FreeAgent 控制；因此 cache hit、费用和 wall time 先按实际观察值报告，并保留 cell
顺序。若未来要检验这些外部变量，必须在 UNKNOWN 完成对账后另行提出、冻结并批准使用全新
Cell ID 的新实验；本文不授权反向顺序复核。控制台不会自行预热、扩充矩阵或发起复核。

## 5. Cell 证据布局

```text
<OutputRoot>/<CellId>/
  inputs/
    bin/<frozen-freeagent.exe>
    seed-root/seed.json
    seed-root/<referenced-artifacts...>
    scenario.json
  work/
    current.sqlite
    artifacts/
  archive/store.bundle/
  input-locks.json
  cell-plan.json
  init.json
  init.stderr.txt
  report.json
  s3-eval.stderr.txt
  backup.json
  backup.stderr.txt
  backup-verify.json
  backup-verify.stderr.txt
  exit.json
```

若 stdout 不是协议要求的单个 JSON object，会以 `*.stdout.incomplete` 保留安全原字节，
不会冒充正式 JSON。`report.json` 还必须满足：

- `schema_version` 为 `freeagent.s3-eval-report/v1`；
- 退出码为 0 当且仅当 `first_error` 为空；
- `repetitions_requested` 与命令请求一致；
- 成功时 `repetitions_attempted == repetitions_requested`；
- `repetition_reports` 数量与 attempted 一致。

`exit.json` 同时记录 report SHA-256、各阶段退出码与超时标记、备份验证结果，以及
`automatic_retry=false` 和 `raw_work_retained=true`。

## 6. 终态解释

| status | 含义 | 下一步 |
|---|---|---|
| `COMPLETE` | 报告协议有效、模型阶段退出 0、备份验证成功 | 可纳入人工对照分析 |
| `PARTIAL` | 模型阶段非零但部分报告协议有效，且备份验证成功 | 保留原证据，人工审查，禁止语义重放 |
| `NO_VALID_REPORT` | 模型阶段未形成合法报告，但原始输出和备份可保留 | 人工检查 incomplete、stderr 和 Store |
| `INIT_FAILED` | 新 Store 初始化未成功 | 不进入模型阶段 |
| `ARCHIVE_FAILED` | backup 或 backup-verify 未完成 | 保留 raw work，人工补充离线取证；不得重跑模型 |
| `SECRET_LEAK_BLOCKED` | 捕获内容命中 exact secret 或疑似真实 Authorization/Bearer 凭据值 | 泄漏捕获不提交；轮换凭据并人工调查 |
| `SENSITIVE_CAPTURE_CLEANUP_FAILED` | 不安全 pending 捕获无法确认删除 | 立即隔离输出目录并按泄漏事件处理；不得声称已清除 |
| `HARNESS_ERROR` | Operator 封装自身失败 | 依据稳定 failure code 排查，不复用 cell ID |

`OUTPUT_SPACE_INSUFFICIENT` 和 `OUTPUT_SPACE_QUERY_FAILED` 都发生在 cell 排他创建之前，
因此没有 `exit.json`；Runner 以非零状态返回，并只在 stderr 输出通用 failure code。
容量检查与后续写入之间仍可能出现并发空间变化，后续 `CreateNew` 和既有失败收口负责
安全停止，不会触发自动清理、改盘或重试。

超时上界固定为：`init/backup/backup-verify` 各 30 分钟，`s3-eval` 6 小时。模型阶段
超时按潜在外部效果处理，退出码记为 124，不重跑；若 Store 可用，脚本仍尝试离线备份。
每个子进程进入 kill-on-close Windows Job Object；超时后进程树终止、二次等待和流排空
各自还有 30 秒固定上界，不存在无界 `Wait`。

Reviewer `REJECT` 是业务终态，不是失败重试许可。Reviewer 或模型为 UNKNOWN 时，只能
沿原 Attempt 做人工对账，不能以新 Request ID 重新提交同义任务。

## 7. 密钥与输出防泄漏

创建 cell 前，脚本会扫描二进制、seed、scenario 和引用 artifacts 是否包含 exact
secret；命中时不创建 cell。每个子进程结束后，stdout/stderr 的 pending 文件会在提交前
扫描：

- exact secret 的 UTF-8、UTF-16LE、UTF-16BE 字节；
- 大小写不敏感的 Bearer scheme，后接至少 16 字符，并具有数字/token 标点、大小写混合，
  或长随机串高多样性特征的疑似凭据值；
- `Authorization` 后接冒号或等号，再接同样的疑似凭据值，包括 Bearer、Basic 和
  Token scheme。

exact secret 始终无条件阻断。单独出现 `Authorization`、`Bearer authentication`、
`Bearer redacted`，以及 `redacted`、`masked`、`placeholder`、`example`、`dummy`、
`YOUR_*` 等明确脱敏值不会触发阻断。这样正常错误文本、配置字段名和模型对认证机制的讨论
可以作为证据保留；未知的长 token 即使不等于当前 API key，仍会失效关闭。

命中时整份 pending 捕获被删除并复查路径不存在，控制台与 `exit.json` 只出现通用状态，
不打印匹配内容。若删除或复查失败，终态为 `SENSITIVE_CAPTURE_CLEANUP_FAILED`，不会伪称
清理成功。
这是一道保守的失效关闭边界，不替代输出目录 ACL、磁盘加密、凭据轮换和供应商侧审计。

## 8. 已执行模型调用后的无重放恢复

若 `s3-eval` 已退出 0，但旧扫描规则随后写入 `SECRET_LEAK_BLOCKED` 并删除 pending 报告，
禁止再次运行 `s3-eval`。这已经产生外部模型效果；换用新 Cell ID 也仍是语义重放。

当前 Store 不能无歧义重建权威 `freeagent.s3-eval-report/v1`：自动生成的 Request ID 和
进程外 monotonic wall time 只存在于原调用内存中，Store 保存的是单向派生的 admission key、
Run/Attempt 和 Usage 事实。用新 ID、空 ID或推测时间补齐都会伪造实验事实。因此：

- 可以从 Store 生成明确标注为 `store-forensics` 的只读调查摘要，但不得命名为 S3 报告；
- 可以恢复完整 backup/verify 证据，但不能把原 cell 改写为 `COMPLETE`；
- 原 Pilot 仍停在该 cell，不能以“补报告”为理由重放模型。

不触碰原失败 cell 的归档方案为：

1. 将原 cell 视为已封存，只读记录其 `exit.json`、`input-locks.json` 和 raw work 哈希；
2. 在 cell 之外排他创建新的 recovery 目录；持有原 owner fence 直到源树复哈结束，并拒绝
   SQLite WAL/SHM/journal、reparse point、hard link 或输入输出路径重叠；
3. 用 no-follow、`CreateNew` 的快照复制器把 `work/current.sqlite`、`work/artifacts` 和必要
   源元数据原字节复制到 recovery raw clone，逐文件核对源/目标 SHA-256；clone 的空 owner
   lock 是显式 derived file，避免 `backup` 在 raw clone 中悄悄新增文件；
4. 只对 clone 调用冻结二进制的 `backup`，随后对新 bundle 调用 `backup-verify`；不得直接
   对原 Store 调用 backup，因为 offline fence 和 SQLite read-residue 处理可能触碰源目录；
5. recovery 元数据绑定原 cell ID、原 `exit.json` 摘要、clone 文件摘要和 bundle manifest，
   并明确 `semantic_replay=false`、`report_recovered=false`。

`s3-store-audit` 已提供最小的 operator-only 只读取证入口。它不需要 API key，不调用
Provider，不创建 Run/Attempt，不打开 writer/WAL，不执行恢复写入，也不新增表、账本或
Runtime：

```powershell
freeagent s3-store-audit `
  --db <cell>\work\current.sqlite `
  --expect-families 3 `
  --expect-runs 12 `
  --expect-attempts 12 `
  --require-all-terminal `
  --require-all-succeeded
```

输出只包含 family/Run/Attempt/result 数量、角色和终态计数、Provider/Model 分类、规范化
token/cache、权威成本字段状态与冻结 PriceSnapshot 派生成本；不包含 Workspace/Run/Attempt
ID、digest、请求或回答正文、header、raw receipt。命令先拒绝 WAL/SHM/journal sidecar，使用
SQLite `mode=ro + query_only + immutable`，并只在内存中比较审计前后 Store 文件哈希。
`freeagent.s3-store-audit/v2` 还必须输出固定 `CHILD/REVIEWER/ROOT` 三桶的 `role_cache`。
每桶只包含 Attempt 数、逐 token 字段 known/unknown 行数、完整 total、已知小计、cached input
的 positive/zero/unknown 行数和可计算时的 cache-hit ratio。角色没有 Attempt 时，Attempt 与
coverage 计数可以为已知零，但 token total、小计和 ratio 必须保持 `null`，不能伪造“零
消耗”；某一 token 字段存在 UNKNOWN 时只令该字段完整 total 为 `null`，并在 known subtotal
中保留已知事实。cache-hit ratio 只由 cached/uncached 两字段决定，两者任一不完整或分母为
零时才保持 `null`；reasoning/output 缺失不影响该比率。三桶计数与已知小计必须和全局
Attempt、token coverage/known subtotal 严格闭合，否则审计失败。
`no_sidecars_verified` 不能证明不存在一个闲置 writer，Operator 仍须先停止 writer；
`source_unchanged` 只证明审计期间主文件字节未变。合法 `MODEL_UNKNOWN`/
`WAITING_RECONCILIATION` 记为 `nonterminal_runs`，不会被误报为损坏，也不会调用终态读取；
只有实际终态闭包不一致才增加 `invalid_run_closures`。该摘要仍不是原
`freeagent.s3-eval-report/v1`，不能把失败 cell 改写为 `COMPLETE`。

对已经提交 `report.json` 的 cell，还应运行聚合式 `s3-cell-audit`：

```powershell
freeagent s3-cell-audit --cell <cell>
```

当前输出协议为 `freeagent.s3-cell-audit/v2`，输入仍严格接受既有
`freeagent.s3-eval-report/v1` 与 `freeagent.s3c-real-cell-exit/v1`。命令校验
`exit.json` 的四阶段终态、report SHA-256、时间和 cell 身份，第一次只读验证
`archive/store.bundle` 后，从 report 独立重算固定 `CHILD/REVIEWER/ROOT` 三桶 role/cache，
再对已验证 Manifest 指向的 `archive/store.bundle/database.sqlite` 执行一次
`s3-store-audit/v2` 只读审计并逐字段比较，最后再次验证整个 bundle；公平性仍从
`service_order` 重算。COMPLETE 会冻结并核对精确 family/run/attempt 数量，且要求 Store
全部终态、全部模型 Attempt 成功；PARTIAL 不是完整 Run 清单，不施加数量/全终态断言，但
仍验证 Store 自身完整性并核对 report 已声明的 role/cache，最终状态始终保持 FAIL。

输出中的 `store_audit_verified` 表示 archive Store 自身通过只读、无 sidecar、前后字节不变
和语义校验；`role_cache_cross_checked` 表示 report-derived 与 Store-derived 三桶完全一致。
PASS 同时要求两者为 true。新增失败只使用固定码
`ROLE_CACHE_COVERAGE_INCONSISTENT`、`ARCHIVE_STORE_AUDIT_FAILED` 和
`ARCHIVE_ROLE_CACHE_MISMATCH`，不透传底层错误或动态角色。其余输出仍只包含 Reviewer
执行阶段计数、token/费用覆盖率、已知小计、缓存、延迟和公平性聚合，不输出任务、回答、
Reviewer 理由、任何内部 ID、digest、header、receipt 或 Secret。非 `COMPLETE` cell 会
输出安全的部分聚合后返回非零；这仍是调查结果，不能升级原终态。该交叉校验证明的是聚合
role/cache 一致，不是逐 Attempt 身份或密码学上的 report↔bundle 绑定，也不证明 Provider
缓存算法、接收状态或执行结果。Operator 仍须先停止所有 writer。
代价是每个 cell 在原两次 bundle 验证之间额外执行一次 archive DB 哈希、完整性/语义校验和
Run/Usage 聚合，复杂度随 DB 与 Attempt 数增长；它只发生在离线 Operator 审计，不增加在线
Runtime 延迟、模型 token、Provider 调用或默认 Pure Chat 成本。

上述 clone → backup → backup-verify 归档流程由独立的
[`scripts/Invoke-S3CRecoverCell.ps1`](../scripts/Invoke-S3CRecoverCell.ps1) 提供：

```powershell
powershell.exe -NoLogo -NoProfile -NonInteractive -ExecutionPolicy Bypass `
  -File (Join-Path $repo 'scripts\Invoke-S3CRecoverCell.ps1') `
  -SourceCellPath <failed-cell> `
  -OutputRoot <external-recovery-root> `
  -RecoveryId <new-recovery-id>
```

命令只接受已有且带完整 `exit.json` 的非 `COMPLETE` cell；`RecoveryId` 必须对应不存在的
新目录。它按 `input-locks.json` 重验并只执行源 cell 的冻结二进制，命令集合固定为 `backup`
和 `backup-verify`。源 owner-lock 不存在、被占用，源/目标重叠，或任一 sidecar/reparse/
hard-link 检查失败时，在创建 recovery 目录前停止。它从 `cell-plan.json` 取得密钥环境变量
名称，但不读取值，并在两个子进程中移除该变量及常见凭据环境变量。

成功或创建目录后的失败都只原子发布一次 `recovery.json`；记录绑定源 cell、源
`exit.json`/`input-locks.json`/`cell-plan.json`/冻结二进制摘要、逐文件 copy verification、
最终 clone locks、两个阶段事实和 bundle manifest。记录始终声明
`semantic_replay=false`、`report_recovered=false`、`automatic_retry=false`。失败目录、raw
clone、bundle 和已安全捕获的原始字节证据原样保留；不得复用同一 `RecoveryId` 重试。
`s3-store-audit` 仍只解决原 Store 的无重放、无正文聚合取证，不会自动调用该归档工具。

### 8.1 2026-08-05 首格事故与 Pilot v2 裁决

首个真实 cell `pilot-f01-flash-clean-reviewer-off-sched-off` 已执行完模型阶段，
`s3-eval` 退出码为 0；旧捕获规则随后把模型正常讨论中的 `Authorization` 单词误判为
凭据泄漏，删除 pending 报告并写入 `SECRET_LEAK_BLOCKED`。原 cell 保持封存，既不改写
`exit.json`，也不再次执行 `s3-eval`。

只读 `s3-store-audit` 与外置 recovery 给出的可复核事实为：

| 事实 | 结果 |
|---|---:|
| Composite families / Runs / Attempts | 3 / 12 / 12 |
| 终态 / 已验证结果 / UNKNOWN | 12 / 12 / 0 |
| 角色 | 9 CHILD + 3 ROOT |
| 模型状态 | 12 SUCCEEDED，均为 `deepseek-v4-flash` |
| input / cached / uncached / output tokens | 15,968 / 0 / 15,968 / 17,122 |
| cache hit ratio | 0% |
| reasoning tokens | UNKNOWN |
| Store 三类权威费用 | UNKNOWN |
| 冻结价格派生成本 | CNY 0.050212 |

恢复记录 `pilot-f01-flash-clean-reviewer-off-sched-off-recovery-001` 已完成 raw clone、
`backup` 和 `backup-verify`，并再次通过相同 Store 聚合审计；原源字节未变，恢复目录未发现
类 `sk-` 凭据值。恢复记录固定声明 `semantic_replay=false`、`report_recovered=false`。
由于原 Request ID 和 monotonic wall facts 已随 pending 报告删除，该 cell 只作为事故与冷态
Usage 证据，不进入 Scheduler/Reviewer 配对效果数据集，也不能被称为 `COMPLETE`。

Operator 据此冻结新的 Pilot v2：配置、顺序、上限和逐格人工终态检查均与 4.1 相同，但十个
矩阵位置全部使用全新身份，将原 `pilot-` 前缀替换为 `pilot2-`。原事故 cell 的 12 次调用不
计入 v2 的 132 Attempt 上限；累计外部效果上限因此为 144。此前同一账号已经发生过调用，
所以 v2 不能声明为冷启动；当前证据不能单独证明 Provider cache 是否或如何被改变。v2 只
报告实际观察值和执行顺序，也不主动生成预热请求或重置缓存。

本裁决只恢复 Operator 证据链和更换实验身份，不改变 Agent、Workspace、Reviewer、
Scheduler、Current Store、Universal Loop 或 Gateway 的任何产品语义。

### 8.2 2026-08-05 Pilot v2 实际结果与停止裁决

Pilot v2 严格逐格执行。前三个效果矩阵位置形成 `COMPLETE`。第四个 Reviewer-on 位置第一次
发起时在模型前发生 `INPUT_SNAPSHOT_FAILED`；该物理 cell 只有 `exit.json`，`init` 和
`s3-eval` 均未启动，因此没有模型外部效果，也不计为一个效果矩阵位置。故障同一时段 C 盘
容量接近耗尽，独立复制复核随后全部通过；两者只有时间相关性，未证明根因。该事故身份保留
且不复用。Runner 已增加“去重后的冻结输入总字节 + 256 MiB”容量 preflight，低空间现在会
在 cell 创建和 executable 调用前以 `OUTPUT_SPACE_INSUFFICIENT` 停止。

使用新身份执行同一 Reviewer-on 配置后形成 `PARTIAL`：9 个 CHILD Attempt 已进入模型边界，
其中 5 个 `SUCCEEDED`、4 个 `MODEL_UNKNOWN`；三个 Reviewer Run 已随 family 冻结，但
Reviewer Attempt、Reviewer result/verdict 和 Root Attempt 均为 0。四个 UNKNOWN 的持久化
原因全部被分类为 `MODEL_UNKNOWN`，而非 `HOST_ERROR_AFTER_PENDING` 或无效 Provider
result；相应 Usage 为 `PENDING_RECONCILIATION`。当前 Store 审计未发现已知写入失败证据，
但没有逐 Attempt canonical provider evidence，不能进一步确定 Provider 侧最终效果。
report SHA、原 Store、归档 bundle 和无 Secret 扫描均已验证，原 Attempt 没有重放。

2026-08-05 的纠偏切片只面向未来调用：DeepSeek Adapter 在返回 `UNKNOWN` 时可附带
`INVOKE_RETURNED_ERROR`、`NO_USABLE_RESPONSE` 或
`RESPONSE_BODY_READ_INCOMPLETE` 三个固定观察位置码，ModuleHost 拒绝自由文本和非
UNKNOWN 携带该字段，Core 只把合法码写入原 Attempt 的既有 `unknown_reason`。这些码只表示
本地 `http.Client` 的可观察位置，不是 Provider 已收到、执行或完成请求的证据。当前四个
UNKNOWN 没有历史事实可供回填，保持原值且不得重分类或重放。`s3-store-audit` 因固定桶集合
扩展升级为 `/v2`；没有新增顶层证据来源、Store 字段、表或 writer。

因此当前共有四个已进入模型的有效矩阵位置（`3 COMPLETE + 1 PARTIAL`），另有一个模型前
失败且不可复用身份的物理 cell 记录；剩余六个矩阵位置停止。物理目录数与效果矩阵位置数
不得混用。

截至停止点的结果如下：

| 顺序 | 配置 | 终态 | Attempt 结果 | input / cached / uncached / output | cache hit | 冻结价格派生成本 |
|---:|---|---|---|---:|---:|---:|
| 1 | Flash / clean / Reviewer off / Scheduler off | COMPLETE | 12 SUCCEEDED | 15,168 / 1,152 / 14,016 / 17,076 | 7.595% | CNY 0.04819104 |
| 2 | Flash / clean / Reviewer off / Scheduler 2/1/1 | COMPLETE | 12 SUCCEEDED | 16,132 / 1,152 / 14,980 / 17,243 | 7.141% | CNY 0.04948904 |
| 3 | Flash / clean / Reviewer off / Scheduler 4/2/2 | COMPLETE | 12 SUCCEEDED | 16,096 / 1,152 / 14,944 / 16,452 | 7.157% | CNY 0.04787104 |
| 4 | Flash / clean / Reviewer on / Scheduler 4/2/2 | PARTIAL | 5 SUCCEEDED + 4 MODEL_UNKNOWN | 已知小计 1,127 / 640 / 487 / 6,336 | 全 cell UNKNOWN | 已知小计 CNY 0.0131718 |

前三个完整 cell 合计 36 次成功调用：input 47,396、cached 3,456、uncached 43,940、output
50,771，token 加权 cache hit 为 7.292%，冻结价格派生成本 CNY 0.14555112。相对“全部输入
按 cache miss 计价”的 CNY 0.148938，实际缓存只节省 CNY 0.00338688（2.274%）。每个完整
cell 都恰有 9/12 个 Attempt 命中缓存，每个命中 128 tokens。新的
`s3-store-audit/v2` 只读聚合已在四个 archive DB 上通过，四次均为
`source_unchanged=true`。前三个完整 cell 的角色交叉表为：

| 角色 | Attempt | input | cached | uncached | output | cache hit |
|---|---:|---:|---:|---:|---:|---:|
| CHILD | 27 | 6,093 | 3,456 | 2,637 | 34,971 | 56.721% |
| ROOT | 9 | 41,303 | 0 | 41,303 | 15,800 | 0% |
| REVIEWER | 0 | — | — | — | — | UNKNOWN |

因此本轮 27 个 CHILD Attempt 全部观察到正 cached input，9 个 ROOT Attempt 全部为已知零；
总体 7.292% 被 ROOT 的大量动态输入稀释。该结果证明缓存事实按角色分布，不证明 Provider
缓存算法或具体因果；“稳定 Child 前缀”和“动态 merge 输入”仍只是待验证解释。reasoning
tokens 在全部已知成功调用中仍为 UNKNOWN。PARTIAL cell 的 9 个 CHILD Attempt 中，5 个
有 Usage 且均观察到正缓存，已知小计仍为 1,127/640/487/6,336；另 4 个 UNKNOWN 无 Usage，
所以该角色的完整 total 与 cache-hit ratio 继续保持 UNKNOWN。Reviewer/Root Attempt 均为 0，
其 token 不是已知零，而是没有调用事实。

同一批四个 archive 随后又由 `s3-cell-audit/v2` 完成长链路只读复核。三个 COMPLETE cell
均为 PASS，各自保持 9 CHILD + 3 ROOT Attempt；PARTIAL 仍因原有
`EXIT_NOT_COMPLETE`、报告错误与 family/reviewer 未闭合返回 FAIL，并保持 9 CHILD、0
REVIEWER、0 ROOT Attempt。四次均得到 `store_audit_verified=true`、
`role_cache_cross_checked=true`、`bundle_unchanged=true` 和 `source_unchanged=true`；这没有
修改 report、exit、bundle、原 Store 或四个 UNKNOWN，也没有恢复剩余矩阵。

Scheduler on/off 的对应 cell 中均观察到 Jain index 为 1 且无饥饿；其余观察值分别为：
longest consecutive 3/1、最大 prefix imbalance 3/1、prefix gap area 18/8、最晚首次服务
序号 7/3，最慢 family wall elapsed 42.905/96.970 秒。Scheduler 状态、并发上限、Provider
状态和执行顺序没有被解耦，因此这些差异不作因果归因，也不能被称为某一种机制的收益或
代价。

Reviewer-on 配置没有产生任何 Reviewer Attempt，因此本轮不能评价 Reviewer 的质量、token
或延迟收益。`case_kind` 当前只是场景标签，不是外部质量真值；即使未来得到 APPROVE，也
只能证明门控闭合，不能直接证明回答质量提升。后续效果实验必须另配冻结的离线 rubric 或
盲评，且不得把待测 Reviewer 自身当作唯一裁判。

本轮 v2 共观察到 41 个 SUCCEEDED 和 4 个未对账 MODEL_UNKNOWN；只计已知 Usage 的派生
成本为 CNY 0.15872292，完整实际成本保持 UNKNOWN。公开 DeepSeek Chat Completions 合同只在
成功响应中返回 completion ID，未提供可把聚合控制台统计逐条绑定回原 Attempt 的查询或
幂等重取接口；没有 canonical provider evidence 时，这四个 UNKNOWN 不能被猜测改写。
因此剩余六个效果矩阵位置不启动，S3-C 保持未验收，S3-D 不得开始。

## 9. 与原始架构的关系

该脚本只编排已有 CLI：`init`、`s3-eval`、`backup`、`backup-verify`。它没有：

- 创建第二套 Runtime、Store、Loop 或 Gateway；
- 绕过 Composite Agent、Reviewer、Scheduler 或 Current Store；
- 新增数据库表、状态机、队列、自动决策或实验账本；
- 把 UNKNOWN 改写为失败或零值；
- 删除 raw 工作树。

因此它不改变 FreeAgent “模块可外挂、Agent/Workspace 自由编排、核心保持轻量”的原始
方向，只把一次真实调用的输入冻结、输出隔离和终态保全标准化。磁盘容量 preflight
同样只属于 Operator 层的启动保护，不新增 Runtime、Store、账本或调度决策，也不改变
Agent、Workspace、Reviewer、Scheduler、Universal Loop 或 Gateway 的产品语义。
`s3-cell-audit/v2` 也只复用现有 bundle verifier 与 Store auditor 增强离线证据交叉校验，
没有新增 Store observer、表、writer、Runtime、Loop、Gateway 或 Provider 调用；UNKNOWN、
S3-C 停止点和 S3-D 禁令均不改变。

## 10. 离线自检

测试使用本地编译的 fake executable，不访问网络，也不读取真实 API key：

```powershell
powershell.exe -NoLogo -NoProfile -NonInteractive -ExecutionPolicy Bypass `
  -File (Join-Path $repo 'scripts\Invoke-S3CRealCell.Tests.ps1')

powershell.exe -NoLogo -NoProfile -NonInteractive -ExecutionPolicy Bypass `
  -File (Join-Path $repo 'scripts\Invoke-S3CRecoverCell.Tests.ps1')
```

覆盖成功、合法部分 JSON、cell 冲突、路径重叠、协议伪装、缺失密钥、输入密钥、exact
secret、疑似 Bearer/Authorization 凭据值、安全认证术语保留，以及容量不足时零子进程调用
等路径。当前基线为 13 个场景、79 个断言全部通过。

恢复工具的自包含测试另外覆盖成功、源/目标重叠、artifact reparse、SQLite sidecar、
RecoveryId 冲突、backup 失败、owner writer 占用和 `COMPLETE` 源拒绝；逐案验证源树字节
不变、真实 NTFS alternate data stream 被拒绝、密钥环境未进入子进程，且
`init`/`s3-eval`/模型调用次数为零。当前 NTFS 基线为 10 个场景、64 个断言全部通过；不支持
named data stream 的卷必须由测试明确报告 `SKIP`，不会把该用例伪装成通过。
