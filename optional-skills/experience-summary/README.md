# 经验总结 0.1

独立包版本：0.1.1（见 `VERSION`）。发行文件：`experience-summary-0.1.1.zip`，解压得到 `experience-summary/` 目录。

独立可选 Skill，不启动进程、不创建数据库、不自动整理对话。

使用现有“能力 → Skill → 导入”选择本目录，然后明确启用。也可把本目录复制到 `<data-dir>/skills/experience-summary/`，再通过 GUI、CLI 或 AI 控制提案启用。发行时本包独立提供，不默认安装到活跃目录。

在一轮已结束的对话中在“使用 Skill 分析本轮”中选择 experience-summary，先预览被选取的来源，再启动一次普通只分析 Run。也可使用 `freeagent analyze preview|start --skill experience-summary --session ID --run ID`；开始需要预览返回的 digest 和稳定 request key。

已获准访问的 Telegram 用户可发送 `/analyze help`，用 `/status` 取得当前对话 Run ID，再发送 `/analyze preview experience-summary RUN_ID`。核对完整来源后，照预览返回的 `/analyze start DIGEST experience-summary RUN_ID` 明确启动；多个 Run 用逗号分隔，可在末尾加起止消息 ID。来源、方法或范围变化后须重新预览。同一人重复启动同一 digest 复用已有 Run。此命令只读取当前对话、本 TG 空间的公开消息与已确认送达的回复；私有模型输出和工具内容不交给 TG。

结果仍是草稿。复杂方法通过 `skills.publish` 精确预览确认；短约定通过现有 Memory 保存/固定入口。发布不会运行命令。替换 Skill 要提供当前 SHA-256、先停用它；旧文档保留在 `skills/.revisions/`，不会注入模型上下文。

TG 结果可在同一对话的 GUI“经验草稿与发布”中编辑、预览和保存；TG 不代替本机敏感审批。来源、来源注意事项、局限和验收方法会一并显示并写入实际保存正文。Memory 保存后仍需另行确认固定；生成的 Skill 默认停用。停用本总结 Skill 不删除已确认产物。

凭据检查覆盖当前可解析的已配置秘密及常见 Key/口令格式，不能保证识别所有未知秘密。请在发布预览中核对内容与范围。

`examples/` 提供三份合成输入和预期候选：可重复部署、短约定 Memory、结果 UNKNOWN 的失败过程。它们说明输出格式与证据要求，不是模型质量评分或真实执行记录，不包含生产聊天记录或凭据。包不需要另一套运行时；没有选择本 Skill 时不会发起总结模型请求。

本包声明 `invocation: selected_sources`，复用 Core 的通用来源分析。入口仅在安装、启用且当前空间获授权时显示；没有经验专用执行 API。预览冻结方法、来源和模型，任一变化需重新预览。停用后既有草稿和已保存内容仍可管理。
