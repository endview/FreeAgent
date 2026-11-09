package harness

import "strings"

// Status text is public transport output. Never copy arbitrary diagnostics,
// which may contain private paths, request data or secrets, into this message.
func channelRunStatus(run Run) string {
	switch run.Status {
	case "completed":
		return "本轮已完成，没有文本回复。"
	case "stopped", "abandoned":
		return "本轮已停止。停止不会撤销已经发生的操作。"
	case "unknown":
		return "本轮存在结果不确定的调用，尚未确认为成功。请查看调用记录并核查实际结果。"
	case "waiting_confirmation":
		return "操作等待确认，请打开 GUI 或 CLI 查看。"
	case "running":
		return "本轮仍在运行。"
	}
	reason := strings.ToLower(run.StopReason)
	switch {
	case strings.Contains(reason, "context") && (strings.Contains(reason, "budget") || strings.Contains(reason, "full")):
		return "当前请求超过上下文硬预算。请调整辅助思考或有效输出额度，或手动压缩历史后继续；原始记录与未知结果保留。"
	case strings.Contains(reason, "length"), strings.Contains(reason, "max_tokens"), strings.Contains(reason, "output_limit"):
		return "模型达到单次输出上限，回复尚未完成。可发送“继续”，或在模型设置中调整输出预留。"
	case strings.Contains(reason, "context"), strings.Contains(reason, "capability"):
		return "当前模型上下文或能力不足。请检查模型窗口与能力设置，或压缩已完成的历史。"
	case strings.Contains(reason, "401"), strings.Contains(reason, "authentication"):
		return "模型认证失败。请在设置中检查 Provider 与 Key。"
	case strings.Contains(reason, "402"), strings.Contains(reason, "quota"), strings.Contains(reason, "balance"):
		return "模型服务额度不足。请充值或更换可用的 Key。"
	case strings.Contains(reason, "429"), strings.Contains(reason, "rate_limit"):
		return "模型服务请求受限。请稍后重试。"
	case strings.Contains(reason, "output"), strings.Contains(reason, "budget"):
		return "本轮达到输出或协作预算，请查看调用记录确认具体限额。"
	case run.Status == "limited":
		return "本轮达到已配置的限额，请查看调用记录。"
	default:
		return "本轮执行失败。请查看调用记录中的错误原因；已确认的结果仍保留。"
	}
}
