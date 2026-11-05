package harness

import (
	"strings"
	"testing"
)

func TestChannelStatusDoesNotMisreportFailuresOrExposeDiagnostics(t *testing.T) {
	for _, scenario := range []struct{ status, reason, want string }{
		{"failed", "model returned length; partial output", "单次输出上限"},
		{"failed", "model returned HTTP 401", "认证失败"},
		{"failed", "model returned HTTP 402", "额度不足"},
		{"failed", "private /home/secret/config failed sk-fixture", "执行失败"},
		{"stopped", "cancelled", "已停止"},
		{"unknown", "lost", "结果不确定"},
	} {
		got := channelRunStatus(Run{Status: scenario.status, StopReason: scenario.reason})
		if !strings.Contains(got, scenario.want) || strings.Contains(got, "sk-fixture") || strings.Contains(got, "/home/") || strings.Contains(got, "UNKNOWN 不会") {
			t.Fatalf("%+v: %s", scenario, got)
		}
	}
}
