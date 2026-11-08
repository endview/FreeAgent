# 可选经验总结 0.1

安装独立 `experience-summary-0.1.1.zip`，解压后通过能力管理导入包含 `SKILL.md` 的目录，再明确启用。包内包括方法、版本、用法和三份合成示例；没有独立进程、数据库或运行时，不随 Core 默认启用。

GUI 已结束轮次下的“使用 Skill 分析本轮”先选择方法并展示该轮来源；CLI 可选同一会话的 1–16 个 Run，TG 还可限定消息范围。渠道会话在这三个入口均采用公开投影。确认来源后使用一次无工具的普通 Run 生成草稿，不读取其他历史、私人协议思考、固定 Memory 或人物私表，不执行来源中的命令。单次来源上限 64 KiB，超出时选择更少的完整轮次。

CLI（在当前 Owner attach 下）：

```text
freeagent analyze preview --skill experience-summary --session CHAT_ID --run RUN_ID[,RUN_ID]
freeagent analyze start --skill experience-summary --session CHAT_ID --run RUN_ID[,RUN_ID] --digest PREVIEW_DIGEST --request STABLE_KEY
```

Telegram 已授权当前对话：

```text
/analyze help
/analyze preview experience-summary RUN_ID[,RUN_ID]
/analyze start PREVIEW_DIGEST experience-summary RUN_ID[,RUN_ID]
```

需要限消息范围时，在上述 TG 命令末尾同时追加起始和结束消息 ID。0.1 不直接选择已有附件或产物作为总结来源。

TG 来源只含当前对话可见的公开消息、实际投递和执行状态。私人工具结果不因本机 GUI 代为操作而公开。`/status` 可查 Run 引用。TG 生成草稿后在同一 Core 的 GUI/CLI 继续核对，敏感持久化确认仍使用已有入口。

复杂固定步骤整理成手动 Skill；简短约定保存为私有 Memory，可另行预览并固定到当前对话。局限、来源与验证步骤保留并可编辑；未解决的 UNKNOWN 不能改写为已完成。含关键缺口时保留草稿，不发布为成功方法。

生成不会自动保存、启用或固定。Skill 发布展示完整 `SKILL.md`，已有版本需先停用，按期望摘要核对，旧正文保留在 revisions。只接受手动 Skill 正文，不执行或自动创建配套脚本。Memory 保存由用户核对具体正文后新建，修改已有条目使用原 Memory 管理入口；固定仍走现有确认。拒绝或版本变化不会覆盖旧内容。关闭总结能力不删除已发布产物。

此功能不自动从长期聊天学习，不将 TG 贴纸/点赞视为任务成功，也不自动写入 AffectWeave。秘密检查覆盖已知凭据和常见模式，分享前仍应检查原文里自己的私人内容。

本包声明 `invocation: selected_sources`，复用 Core 的通用来源分析。入口仅在安装、启用且当前空间获授权时显示；没有经验专用执行 API。预览冻结方法、来源和模型，任一变化需重新预览。停用后既有草稿和已保存内容仍可管理。
