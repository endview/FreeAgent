package harness

func ContextByteLimitMode(config Config) string {
	if config.ContextByteLimitMode == "" {
		return "legacy_manual"
	}
	return config.ContextByteLimitMode
}

func contextByteLimit(config Config, manual, window int) int {
	if config.ContextByteLimitMode != "auto" {
		return manual
	}
	if window <= 0 {
		return 128 << 10
	}
	return min(8<<20, max(256<<10, window*8))
}

// Explicit policy separates scheduling from artifact size. Missing fields
// preserve old behavior; equality to a historical default never implies auto.
func AutomaticCompactionEnabled(config Config) bool {
	if config.AutomaticCompaction != nil {
		return *config.AutomaticCompaction
	}
	return config.TaskSummaryBytes > 0
}

func SummaryTargetMode(config Config) string {
	if config.SummaryTargetMode == "" {
		return "legacy_manual"
	}
	return config.SummaryTargetMode
}

func summaryPolicy(config Config, window ContextWindow) (bytes, tokens int, source string) {
	source = SummaryTargetMode(config)
	if source != "auto" {
		bytes = config.TaskSummaryBytes
		if bytes == 0 {
			bytes = 4096
		}
		return bytes, 0, source
	}
	// The token target is soft; stored UTF-8 bytes retain their own hard cap.
	// An unknown model window is explicitly a conservative local fallback.
	tokens = min(2048, max(256, window.InputBudget/10))
	if window.Tokens == 0 {
		bytes = min(maxSummaryBytes, max(1024, (window.ByteLimit-window.OutputReserve*4)/10))
		return maxSummaryBytes, min(2048, max(256, bytes/3)), "auto_local_budget"
	}
	return maxSummaryBytes, tokens, "auto_model_window"
}

func contextPolicySnapshot(config Config) map[string]any {
	return map[string]any{"context_byte_limit_mode": ContextByteLimitMode(config), "automatic_compaction": AutomaticCompactionEnabled(config), "summary_target_mode": SummaryTargetMode(config), "task_summary_bytes": config.TaskSummaryBytes}
}
