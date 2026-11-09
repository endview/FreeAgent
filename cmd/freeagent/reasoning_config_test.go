package main

import (
	"encoding/json"
	"flag"
	"reflect"
	"testing"

	"github.com/endview/freeagent/internal/harness"
)

func TestReasoningOnlyCLIPlanPreservesModel(t *testing.T) {
	current := harness.DefaultConfig(t.TempDir())
	model := harness.ModelConfig{Provider: "anthropic", Key: "k-12345678", Model: "claude-opus-5-5", Capabilities: harness.ModelCapabilities{OutputTokens: 8192, Reasoning: harness.ReasoningCapabilities{Control: "anthropic", Efforts: "low,medium,high,xhigh,max", Default: harness.ReasoningSelection{Effort: "high"}, Auxiliary: harness.ReasoningSelection{Effort: "low"}}}}
	current.Models["claude"] = model
	for _, args := range [][]string{{"--reasoning-effort", "max"}, {"--aux-reasoning-effort", "low"}, {"--reasoning-mode", "default"}} {
		inv := configInvocation{ModelID: "claude", Flags: flag.NewFlagSet("fixture", flag.ContinueOnError)}
		inv.Flags.StringVar(&inv.Model.Capabilities.Reasoning.Default.Effort, "reasoning-effort", "", "")
		inv.Flags.StringVar(&inv.Model.Capabilities.Reasoning.Auxiliary.Effort, "aux-reasoning-effort", "", "")
		inv.Flags.StringVar(&inv.Model.Capabilities.Reasoning.Default.Mode, "reasoning-mode", "", "")
		if err := inv.Flags.Parse(args); err != nil {
			t.Fatal(err)
		}
		kind, raw, err := inv.modelPlan(t.Context(), t.TempDir(), current)
		var fields map[string]json.RawMessage
		if err != nil || kind != harness.ControlSetModelProfile || json.Unmarshal(raw, &fields) != nil || len(fields) != 2 || fields["reasoning"] == nil || fields["key"] != nil {
			t.Fatal("narrow plan requires unrelated fields", string(raw), err)
		}
		var declared harness.ReasoningCapabilities
		if err := json.Unmarshal(fields["reasoning"], &declared); err != nil {
			t.Fatal(err)
		}
		if declared.Control != model.Capabilities.Reasoning.Control || declared.Efforts != model.Capabilities.Reasoning.Efforts {
			t.Fatal("declaration lost")
		}
		if !reflect.DeepEqual(current.Models["claude"], model) {
			t.Fatal("CLI preview changed config")
		}
	}
}
