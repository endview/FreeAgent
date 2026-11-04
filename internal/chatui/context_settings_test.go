package chatui

import (
	"github.com/endview/freeagent/internal/harness"
	"testing"
)

func TestContextSettingsUsesCoreLimitAndRejectsOmittedValue(t *testing.T) {
	ui := setupUI(t, harness.EchoModel{})
	before := ui.tasks.Config().TaskSummaryBytes
	code, raw := ui.request(t, "PATCH", "/api/settings/context", map[string]any{})
	if code != 400 || ui.tasks.Config().TaskSummaryBytes != before {
		t.Fatalf("missing value changed config: HTTP %d %s", code, raw)
	}
	for _, limit := range []int{0, 1024, 4096} {
		code, raw = ui.request(t, "PATCH", "/api/settings/context", map[string]int{"task_summary_bytes": limit})
		if code != 200 || ui.tasks.Config().TaskSummaryBytes != limit {
			t.Fatalf("summary setting %d: HTTP %d %s", limit, code, raw)
		}
	}
}
