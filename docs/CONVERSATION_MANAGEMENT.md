# 会话管理、空间整理与回收站

适用：FreeAgent v8.8.0-dev.11，存储 schema 2。功能使用 Core 现有会话、控制日志、元数据和 outbox；不需要安装 AffectWeave。

## 日常操作

侧栏每个会话和当前会话顶部的「⋯」提供重命名、置顶/取消置顶、移到空间、选择多个会话和移到回收站。会话行右键、Shift+F10 打开相同菜单。改名输入框和 Web 其他区域保留原生右键。

桌面侧栏会话行按钮在悬停或键盘聚焦时显示，菜单打开后保持可见，触屏直接显示。主侧栏底部仅保留“设置”；空间整理、回收站与保留执行记录统一在设置的“空间管理”分组。

侧栏改名支持保存/取消、Enter/Esc 和中文候选词确认。标题为 1–80 个字符，不能包含换行或控制字符。其他窗口先改名时，本窗口保留草稿并提示冲突；不会静默覆盖。

“移到空间”支持搜索目标和创建普通空间。创建空间与移动是两次明确操作：创建成功后选中目标，再预览、确认移动；取消移动会留下已创建的空空间。移动保留会话 ID、模型、工作目录和明确能力排除，后续权限按目标空间重新计算。

移动到渠道空间会改变历史正文和附件的可见受众，预览会说明公开影响；不会重发整段历史。非 Owner 私聊空间会撤销原私人能力选择，保存明确的空选择，不回退为启用全部默认能力。当前/默认渠道会话需要指定同空间替代项，或选择“新建并切换”。单会话模式保持单会话，替代会话继承有效模型与明确能力选择，不继承历史、队列或主动发送授权。

空间菜单提供新建会话、重命名、新建普通空间、管理内容、空间设置和删除空间。本地基础空间不能删除。“全部空间”是过滤条件。

## 整理空间内容

进入“设置 → 空间管理 → 空间内容 → 会话列表”。“空间设置”子页保留名称、权限、多会话和默认会话等配置；附件整理使用列表上方的“附件与结果文件”。会话/空间菜单可直接进入相同页面，旧管理链接仍可用。

- 会话可按空间、标题、7/30/90 天前或指定时间、最近入口、置顶、空会话、分支、附件和待处理任务筛选；支持按活动时间、附件大小、轮次排序。
- 最近入口来自实际 Run 的入口记录。本机入口没有进一步区分 Web、桌面和 CLI；无记录时明确显示“未记录入口”。活动时间使用创建/执行记录，不因改名、置顶或分类移动变成刚刚聊过。
- “选择本页”选择已经显示的条目；“选择全部筛选结果”按分页取得确切 ID。正常全选默认排除置顶，可勾选“全选时包含置顶”。筛选/管理范围变化清空选择，翻页和排序保留选择。
- 每次管理最多 5000 个会话，按每批 500 个预览与执行。确认后的新匹配会话不加入。预览有效 15 分钟；对象、授权或待执行事项变化时跳过受影响项，保留已成功项。
- 批量操作显示逐项结果，可取消尚未开始的批次。已经提交的当前批次最多 500 项会继续完成；恢复成功项是新的操作。
- “附件与结果文件”只管理 Core 托管文件，显示所属会话、消息引用次数与大小。单独删除文件立即生效，不进入回收站；分支独立副本不受影响。正在执行或排队/派发中的关联文件须先处理任务或投递。

工作目录只显示和复制路径，不扫描或清理外部文件树。独立 Memory、Skill、已安装扩展的管理入口链接到原设置页；本页不处理扩展业务数据。

## 回收站

入口为“设置 → 空间管理 → 回收站”。

移入回收站后保留 168 小时，支持只读查看、下载附件、恢复、永久删除所选和清空。原空间删除后仍可按原空间筛选；默认恢复到本地。恢复不抢回渠道路由，不重启任务、不续送已取消消息、不重新开启主动发送。

原置顶会话不占正常列表名额；恢复时若 50 个置顶名额已满，恢复内容并明确提示未恢复置顶。重复提交同一回收请求不会延长期限，旧请求也不会再次回收已恢复的会话。

Owner 启动后检查、运行中每小时有界扫描。到期后有资格自动清理，不保证精确秒级执行；停机期间不运行，下次启动补处理。单批最多处理 32 个到期会话、64 个待清理文件；剩余内容后续扫描处理。检测到运行期间异常向前的系统时间跳变时跳过当批并记录异常。

删除空间时，“同时将其中的会话移到回收站”默认勾选；取消后移到本地。空间内已经回收的记录不会恢复或重新计时。删除渠道空间会解除该空间授权，空间配置本身不进入回收站。单次删除空间最多包含 5000 条会话记录，包括保留骨架；超出时明确拒绝，不部分删除空间。

## 运行中的任务与未确认事项

预览列出运行、待确认、排队输入、后台命令、在途投递和渠道路由。需要时使用“停止并结束这些任务”，等待安全边界后重新预览；不会批准原业务提案来换取删除资格。

已经可靠交接的 UNKNOWN 可以随会话回收，但原执行事实和防重放继续保留。失控进程、未可靠交接的执行/提交未知、尚在派发的消息会阻止清理。

“设置 → 空间管理 → 保留的执行记录”提供未决详情、用户确认已生效/未生效和结束提醒。用户确认与机器证据分开；结束提醒不等于未执行，也不授权重做。已回收或永久删除的会话不能发送、继续运行、分支或进入模型历史回读。其他分支的独立历史/附件仍可使用。

对话里整理当前会话要经过用户确认。Core 先保存确认、结清并结束提出建议的 Run，再执行移动/回收；不要求向已回收的会话再写一条模型回答。重启恢复沿用原记录，不重新调用模型。若对象或授权已变化，记录为需重新预览。

在线窗口收到变更后重新读取状态；断线/刷新后补查草稿关联会话。回收会话不自动重发待发送内容；永久删除清除对应持久草稿。本窗口尚存的未发送文字可供复制，网络错误不会当作删除而丢弃草稿。

## CLI 与对话入口

以下命令已经由独立数据库 CLI 测试覆盖。`DATA`、`CHAT_ID`、`SPACE_ID` 和 `PREVIEW_ID` 为用户替换的参数；保留命令间返回的同一个预览 ID。

```text
freeagent conversations list --data-dir DATA --limit 60
freeagent conversations list --data-dir DATA --view trash --space SPACE_ID
freeagent conversations list --data-dir DATA --entry telegram --before 2026-11-01T00:00:00Z
freeagent conversations preview --data-dir DATA --action trash --session CHAT_ID
freeagent conversations apply --data-dir DATA --operation PREVIEW_ID --yes
freeagent conversations result --data-dir DATA --operation PREVIEW_ID
```

恢复和永久删除同样先预览，再以返回 ID 执行：

```text
freeagent conversations preview --data-dir DATA --action restore --session CHAT_ID
freeagent conversations preview --data-dir DATA --action purge --session CHAT_ID
```

移动使用 `--action move --space SPACE_ID`，改名使用 `--action rename --title 新名称`，置顶/取消置顶使用 `pin`/`unpin`。多个确切会话 ID 以逗号分隔。路由替代使用 `--replacement new` 或同空间另一会话 ID；取消排队输入与未派发消息须带 `--cancel-pending`。

`conversations finish --operation PREVIEW_ID --yes` 只处理原预览中的确切任务，之后必须重新预览。`delete_space --space SPACE_ID` 默认回收其中会话；`--keep-conversations` 明确保留并移到本地。`--yes` 不能跳过预览或扩大对象集合。

CLI 本地执行和附着 Owner、Web 都使用相同控制记录。AI 通过 `control_read_state` 的 `domain=conversations` 取得 ID，并提出 `conversation.manage` 建议；改名、置顶、移动、回收、恢复始终需要用户确认。新永久删除/清空操作不向模型开放。旧 `space.delete` 保持保留会话的兼容行为，不能用旧 API 缺省参数触发新的危险默认值。

## 清理与保留清单

| 数据及归属 | 永久清理规则 |
| --- | --- |
| `fa.messages.conversation_id` | 删除原会话消息；渠道请求键先保存不可重放的最小墓碑 |
| `fa.attempts.run_id` → `fa.runs.conversation_id` | 删除普通执行正文；UNKNOWN/dispatched、已有未决引用及依赖的原 Run 快照继续保留 |
| `context/summary`、`context/maintenance`、`context/usage`、`context/compaction-suppression` | 按确切会话路径清理 |
| `context/omission`、`context/repair` | 按确切 Run 归属清理 |
| `input/CHAT/`、`chat/model`、`chat/reasoning`、`chat/pin`、`chat/capabilities`、`chat/memory`、`chat/agent`、`chat/collaboration` | 取消队列后删除会话选择；独立 Memory 内容保留 |
| `branch/request/CHAT/` | 删除源会话分支请求缓存；独立分支自己的消息、附件、来源和未决引用保留 |
| `attachment/ID`、`attachments/ID` | 先撤销下载，再保存 `conversation/file-cleanup/ID` 重试物理删除；失败的字节仍计入待清理占用 |
| `attachment-reply`、`channel/reply-parts`、`channel/render/.../reply/RUN/` | 按记录中确切 Run 删除正文缓存 |
| `channel/operation`、`channel/receipt`、`channel/inbound` 及 claim | 保留领取/终态/指纹/回执和去重关联；删除已无必要的已知终态正文；UNKNOWN 原内容继续保留供迟到协调 |
| `command/process` | 删除无未决引用的已终结普通命令记录；UNKNOWN/在途证据继续保留 |
| `conversation/lifecycle`、管理预览/回执、`attempt/unresolved`、继承未决、控制日志 | 保留用于旧请求幂等、原证据处理和防重放；不把忽略当作删除资格 |
| 工作目录、独立 Memory/Skill/扩展库、备份和外部平台副本 | 不随会话删除 |

保留的 Run/Attempt 可能含路径、参数、配置和部分文本。预览显示执行证据数量/估算字节与投递记录数量；永久清理不承诺擦除这些记录、控制日志或备份。文件撤权与物理释放分开显示，失败继续重试。

## 从旧版升级与回退

本次存储格式升级必须显式执行；不能只覆盖二进制后让旧程序写入新库。

1. 用旧版核对任务、队列、后台命令和投递；结束或可靠交接，停止 Owner 与渠道进程。迁移发现活跃 Run 或待处理投递会拒绝。
2. 使用新版执行下面命令。备份目录必须尚不存在；迁移先核验旧格式，生成并验证完整备份，再在独占事务内升级。

```text
freeagent store-upgrade --data-dir DATA --backup NEW_BACKUP --confirm
freeagent backup-verify --source NEW_BACKUP
```

3. 启动新版。既有正常会话保持正常，不因升级自动进入回收站。AffectWeave 的明确未选中状态和独立存储保持原样。
4. 回退时保留升级后的数据副本，再使用升级前程序与同一时点备份恢复到新目录；不要只切换旧二进制。新版恢复入口也可识别已验证的 schema 1 备份。

备份不替代凭据、工作目录及独立扩展存储的备份。公网会话管理仍要求既有登录、CSRF 与交互 Owner 身份；观察者和群成员不能读取管理清单或保留证据。

实施与设备边界见[验收记录](VALIDATION_CONVERSATION_MANAGEMENT_2026-11-16.md)。
