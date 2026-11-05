# PostgreSQL 与数据维护

v8.0 起仅使用 PostgreSQL 15；随包固定版本为 15.19，数据目录为 `Harness-pg-v1`。
旧版本目录不会读取、导入或自动转换。PG 保存会话、执行、审批、上下文、显式 Memory
和知识记录；未来重知识扩展仍是可选能力。

## 日常启动

Windows 双击 `FreeAgent.exe`；Linux 普通用户运行 `./FreeAgent`。首次启动自动初始化
本机 PG、随机回环端口、独立数据库和受限运行角色，不需要手动建库。Linux 的 PG 不能
以 root 运行；服务器使用 `deploy/install-server.py` 创建专用用户。Debian 12 运行时需
系统 `libc6` 和 `zlib1g`，命令沙箱另需 bubblewrap。

最后一个客户端离开时，运行任务、待确认和后台命令仍保留 Owner；空闲后先停止渠道和
Owner，再停止自己启动的 PG。外部 PG 不随 Core 关闭。常驻 Bot 使用 systemd。

设置 → 数据与运行 → PostgreSQL 显示版本、实例方式、大小、连接和等待情况，不显示
数据库密码。`doctor --text` 检查配置、数据库及能力；不发送模型请求。

`postgres.json`、`postgres-maintenance.json` 是本机秘密；不要发送给模型或放入发行包。
Windows 使用受保护 ACL，Linux 使用私有目录/文件权限。默认仅监听 127.0.0.1，使用
SCRAM 认证；不要向公网开放 PG 端口。

## 使用本机已有 PG

显式设置 `FREEAGENT_PG_SERVICE_FILE` 指向私有 JSON，再对**新目录**执行 `init`。
该文件包含 `host`（数字回环地址）、`port`、`user`、`password`、`database`（通常 postgres）
和 `bin`（PG 15 工具目录）。初始化身份需要建库、建角色、建扩展权限；普通运行只使用
产品为该 Core 创建的独立受限角色，不保存管理密码到 Core 的运行配置。

服务文件留在受保护的维护位置；不要作为命令行参数粘贴密码。此方式的 PG 由系统维护，
Core 不负责启停。一个 Core 一个逻辑库和独立角色，可以共用 PG 服务；不同 Core 不互读
状态，不提供跨主机接管或远程 PG/TLS。

## 在线备份与恢复

```text
freeagent backup --data-dir <当前目录> --out <不存在的新备份目录>
freeagent backup-verify --source <备份目录>
freeagent restore --source <备份目录> --data-dir <不存在的新数据目录>
```

备份不暂停普通任务。数据库导出与附件引用采用同一 PG 快照；文件不可变，删除/回收与
快照复制互斥。备份含数据库、配置、选定指令和引用附件；Provider/渠道凭据、PG 管理
密码及浏览器登录状态须另行私下保管。`backup-verify` 核对清单、文件和摘要；完整恢复
还会将 dump 装入新库，检查身份/schema 和附件引用再发布新目录。

恢复保留同一 Core 的逻辑身份；它用于恢复原 Core，不能把两个相同身份的副本同时当作
独立 Core 运行。恢复前停止原副本；不支持自动克隆/跨主机接管。运行中快照里的未决
调用在恢复时保持 UNKNOWN，不自动重放工具。启用渠道前检查原实例已停，并重新核对
凭据、积压和授权。旧版 SQLite 备份只作为旧版档案保管，不能用 v8.0 restore 导入。

## 排障

- 初始化失败：查看 `postgresql.log` / `engine.log`，确认可写目录、磁盘空间和随包运行时。
  不删除已有数据。尝试新目录需保留失败目录供核查。
- 端口占用：停止占用者或离线维护本机配置，再明确重启；不偷偷接入另一个实例。
- PG 或 Owner 连接丢失：停止新执行和客户端准入；重开后核对 UNKNOWN。拿到新代次不能
  证明旧命令没有副作用。
- schema/身份不匹配：拒绝写入；选择正确目录或恢复到新库，不自动降级或修补未知 schema。
- 附件提交未知：正文和事务核对标记保留；确认原事务结束前不回收。

## 构建与测试

先用 `scripts/prepare-postgres.py` 准备固定运行时，再把其目录交给发行脚本的
`--postgres-runtime`（或构建环境 `FREEAGENT_POSTGRES_RUNTIME`）。Windows 准备脚本
只提取 VC 运行库，不安装系统组件。Linux 运行时在 Debian 12 构建，可在 Debian 12
容器内准备后复制给发布任务；不使用更新 glibc 的产物冒充 Debian 12 便携版。
版本、来源与逐文件清单随运行时发布；可重复打包比较使用同一份已验证运行时输入。

```text
python scripts/test-postgres.py --runtime <准备好的 postgresql> -- go test -p 2 ./cmd/... ./internal/... ./examples/...
```

真实存储测试使用临时 PG 服务和各自独立库，不能用 SQLite 或 SQL 转译模拟 PG。
真实 Provider/Telegram 需要外部条件；未运行的验收须明确列出。


大历史基准可通过相同包装器运行 `go run scripts/benchmark-postgres.go`。它会在临时服务内
创建独立 Core，生成 10 万 Run、40 万消息、30 万 Attempt（约 1.8 GiB），以 Echo 测量
真实 TaskService 的任务与启动耗时，并保留同步提交。它不测试真实模型等待或冷缓存性能。
