# 诊断导出与更新前检查

自 v8.0.1 引入，当前 v8.3 开发候选继续适用。使用当前 CLI，`--data-dir` 指实际的原生 PostgreSQL 数据目录；诊断不会自动重新请求模型、执行命令或重放 UNKNOWN。

## 启动失败

```text
freeagent doctor --data-dir <数据目录> --text
```

分别查看 config、store、keys、模型选择、sandbox、commands 和 public_web 的结果与 Next 操作。JSON 默认输出包括各检查的实际耗时。一个配置错误不会隐藏独立数据库或 Key 元数据问题。

Provider 默认关闭需要显式启用；doctor 不测试 Key 或网络。沙箱缺失不会自动切换到 host。数据库与配置损坏时保留原目录，先核对备份，不删除数据重建。

Windows 首次启动检查 `desktop.log`、`engine.log` 和 PG 启动日志的位置。WebView2 缺少时可以使用同一 Core 的本地浏览器入口。Linux PostgreSQL 不以 root 运行，使用专用服务账号；安装步骤见[服务器部署](SERVER_DEPLOYMENT.md)。

## 可分享的诊断包

```text
freeagent doctor --data-dir <数据目录> --export <新文件路径.zip>
```

导出只包含版本/平台、健康类别、近期失败计数和已有执行状态计数。原始日志、配置、服务地址、Key/Cookie/数据库密码、正文、附件和工作区文件不会被复制。失败状态也能导出供排障；doctor 仍保留非零退出码，不因导出成功就声称产品健康。

输出文件必须不存在。诊断包不是备份，不能用它恢复对话或配置；如需本机原始日志排障，单独查看，公开分享前仍需选择具体范围。

## 更新前检查

先创建并验证[当前格式备份](POSTGRESQL.md)，然后执行：

```text
freeagent doctor --data-dir <数据目录> --preflight --backup <备份目录>
```

检查当前配置、只读数据库、Owner、活跃/待确认 Run、运行中命令、待确认/部分应用控制操作及备份完整性和 Core 身份。备份属于其他 Core 或未提供备份时不报告可以更新。普通历史 UNKNOWN/已停止记录显示计数并原样保留，不自动重放；已确定失败的历史操作不永久卡住更新。

`ready_to_update` 只表示检查时观察到的状态，不是独占维护锁。真正替换程序前立即复查，并按既有安装器/服务步骤停止派发。检查到活跃任务时等待，不自动杀任务。在线用户仍可能提交新任务，因此不能拿几分钟前的检查结果直接替换运行中的程序。

同一 `Harness-pg-v1` 目录后续更新保留原生数据。不要用旧 SQLite 目录或旧备份替换它。工作区和加密/受限 Key 文件按各自说明单独保护；数据库备份不等于所有本机文件都已备份。

具体环境的 PASS/FAIL/NOT_RUN 见[实施记录](FREEAGENT_V8_0_1_TO_V8_3_IMPLEMENTATION_PROGRESS.md)。
