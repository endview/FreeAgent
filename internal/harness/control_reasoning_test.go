package harness

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestControlAuxiliaryReasoningPreservesProfileAndRequiresConfirmation(t *testing.T) {
	core, _, _ := testHarness(t, EchoModel{})
	keys := testKeyStore(t)
	t.Setenv("FREEAGENT_REASONING_CONTROL_FIXTURE", "fixture-only-no-network")
	key := addTestProfile(t, keys, "https://api.deepseek.com", KeySourceEnv, "FREEAGENT_REASONING_CONTROL_FIXTURE", "")
	core.Keys = keys
	model := ModelConfig{Provider: "openai-compatible", Key: key, Model: "deepseek-flash", Capabilities: ModelCapabilities{
		ContextTokens: 131072, OutputTokens: 8192,
		Reasoning: ReasoningCapabilities{Control: "deepseek", Efforts: "low,high,max", Default: ReasoningSelection{Mode: "enabled", Effort: "high"}, Auxiliary: ReasoningSelection{Mode: "enabled", Effort: "high"}},
	}}
	core.Config.Models[core.Config.DefaultModel] = model
	enableTestProviders(&core.Config)
	if err := WriteConfig(core.Store.DataDir(), core.Config); err != nil {
		t.Fatal(err)
	}
	control, err := NewTaskService(core).Control()
	if err != nil {
		t.Fatal(err)
	}
	state, err := control.ReadControlStateForModel(t.Context())
	if err != nil || len(state.Config.Models) != 1 || state.Config.Models[0].Reasoning != model.Capabilities.Reasoning {
		t.Fatalf("current model reasoning is unavailable to the model: %+v %v", state.Config.Models, err)
	}
	chat := conversation(t, core)
	op, err := control.PlanControlChange(t.Context(), ControlPlanRequest{Kind: ControlSetModelProfile,
		Arguments:     mustJSON(map[string]any{"id": core.Config.DefaultModel, "auxiliary_reasoning": ReasoningSelection{Mode: "disabled"}}),
		Authorization: ControlModelProposed, ConversationID: chat})
	if err != nil || op.Status != ControlPending || !reflect.DeepEqual(core.Config.Models[core.Config.DefaultModel], model) {
		t.Fatalf("proposal applied before confirmation or required a Key: %+v %v", op, err)
	}
	if _, err := control.ConfirmControlChange(t.Context(), op.ID, ControlAuthorization{Source: ControlModelProposed, ConversationID: chat}); err == nil {
		t.Fatal("proposal accepted without interactive confirmation")
	}
	capability, err := control.IssueInteractiveCapability(t.Context(), chat, "user")
	if err != nil {
		t.Fatal(err)
	}
	code, err := control.ControlCodeForTests(t.Context(), op.ID, chat, "user")
	if err != nil {
		t.Fatal(err)
	}
	result, err := control.ConfirmControlChange(t.Context(), op.ID, controlAuth(capability, chat, "user", code))
	expected := model
	expected.Capabilities.Reasoning.Auxiliary = ReasoningSelection{Mode: "disabled"}
	if err != nil || !result.Applied || !reflect.DeepEqual(core.Config.Models[core.Config.DefaultModel], expected) {
		t.Fatalf("narrow change replaced other model fields: %+v %v", core.Config.Models[core.Config.DefaultModel], err)
	}
	for _, raw := range []string{
		`{"id":"default","provider":"openai-compatible","auxiliary_reasoning":{"mode":"disabled"}}`,
		`{"id":"default","capabilities":null,"auxiliary_reasoning":{"mode":"disabled"}}`,
		`{"id":"absent","auxiliary_reasoning":{"mode":"disabled"}}`,
	} {
		if err := applyConfigControl(&core.Config, ControlSetModelProfile, json.RawMessage(raw), t.Context(), core.Store.DataDir()); err == nil {
			t.Fatal("ambiguous or absent profile was accepted", raw)
		}
	}
}
