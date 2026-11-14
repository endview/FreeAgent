package harness

import "strings"

// New stop reasons carry a stable code; legacy inference is confined here.
func runStopCode(reason string) string {
	text := strings.ToLower(strings.TrimPrefix(reason, settledLimitPrefix))
	for _, code := range []string{"context_structure_invalid", "context_window_full", "context_payload_limit", "context_history_read_limit", "context_fixed_input", "context_current_input", "summary_limit", "summary_incomplete", "summary_no_progress", "output_limit", "authentication", "quota", "rate_limit"} {
		if strings.HasPrefix(text, "harness: "+code+":") || strings.HasPrefix(text, "harness: "+code+"\n") || text == "harness: "+code {
			return code
		}
	}
	switch {
	case strings.Contains(text, "context_structure_invalid"), strings.Contains(text, ErrModelBoundary.Error()):
		return "context_structure_invalid"
	case strings.Contains(text, "summary did not fit"):
		return "summary_limit"
	case strings.Contains(text, "summary response incomplete"):
		return "summary_incomplete"
	case strings.Contains(text, "context budget"), strings.Contains(text, "exceeds context budget"):
		return "context_budget_unknown"
	case strings.Contains(text, "length"), strings.Contains(text, "max_tokens"), strings.Contains(text, "output_limit"):
		return "output_limit"
	case strings.Contains(text, "401"), strings.Contains(text, "authentication"):
		return "authentication"
	case strings.Contains(text, "402"), strings.Contains(text, "quota"), strings.Contains(text, "balance"):
		return "quota"
	case strings.Contains(text, "429"), strings.Contains(text, "rate_limit"):
		return "rate_limit"
	}
	return ""
}

func (err *ContextBudgetError) stopCode() string {
	switch err.Code {
	case "history_read_bound":
		return "context_history_read_limit"
	case "fixed_context_too_large":
		return "context_fixed_input"
	case "current_input_too_large":
		return "context_current_input"
	}
	if err.Usage.Resource == "estimated_tokens" {
		return "context_window_full"
	}
	return "context_payload_limit"
}

func contextStopText(code string) string {
	switch code {
	case "context_structure_invalid":
		return "上下文消息结构不完整，已保留记录。请用 /context 查看状态；这是工具调用与结果的关联问题，调整额度不会修复它。"
	case "context_window_full":
		return "当前请求超过模型输入窗口。请用 /context 查看占用，可用 /compact 手动压缩，或在已开启多对话时用 /new 新建对话。原始记录保留。"
	case "context_payload_limit":
		return "当前请求超过本地请求字节保护，未发送给模型。请用 /context 核对字节上限与占用，或用 /compact 压缩；这不代表厂商模型窗口已满。"
	case "context_history_read_limit":
		return "未压缩历史超过本地读取保护。请用 /context 查看状态，再手动压缩或新建对话；原始记录保留。"
	case "context_fixed_input", "context_current_input":
		return "当前输入、附件或固定材料超过请求预算，压缩旧历史可能无效。请用 /context 查看来源，并缩小输入或调整模型设置。"
	case "summary_limit", "summary_incomplete", "summary_no_progress":
		return "上下文摘要未完成，原摘要与历史保留。请用 /context 查看摘要上限及辅助配置；修正后可用 /compact 再压缩。"
	case "context_budget_unknown":
		return "旧记录显示上下文预算或压缩阶段受限，尚不能确认是模型硬窗口。请用 /context 查看当前占用，再选择 /compact 或 /new。"
	}
	return ""
}
