package harness

import (
	"context"
	"testing"
)

func applySpaceDefaultsFixture(t *testing.T, tasks *TaskService, space string, selection CapabilitySelection) {
	t.Helper()
	ctx := t.Context()
	control, err := tasks.Control()
	if err != nil {
		t.Fatal(err)
	}
	op, err := control.PlanControlChange(ctx, ControlPlanRequest{Kind: ControlSpaceCapabilities, Arguments: mustJSON(SpaceControlArguments{SpaceID: space, Capabilities: &selection}), Authorization: ControlUserDirect})
	if err != nil {
		t.Fatal(err)
	}
	capability, err := control.IssueInteractiveCapability(ctx, "", "owner")
	if err != nil {
		t.Fatal(err)
	}
	code, err := control.RenderConfirmation(ctx, op.ID, "", "owner")
	if err != nil {
		t.Fatal(err)
	}
	if result, e := control.ConfirmControlChange(ctx, op.ID, ControlAuthorization{Source: ControlUserDirect, Principal: "owner", Code: code.Code, Capability: capability}); e != nil || !result.Applied {
		t.Fatal(result, e)
	}
}

func TestSpaceCapabilityDefaultsInitializeOnlyNewConversationsAndRevokeLive(t *testing.T) {
	_, tasks, target, old := replyExtensionFixture(t, true, true, true, "parts-valid")
	ctx := context.Background()
	space, _ := tasks.ChannelSpace(ctx, target)
	if err := WriteConfig(tasks.DataDir(), tasks.Config()); err != nil {
		t.Fatal(err)
	}
	if err := tasks.SelectCapabilities(ctx, old, CapabilitySelection{}); err != nil {
		t.Fatal(err)
	}
	control, err := tasks.Control()
	if err != nil {
		t.Fatal(err)
	}
	if err = control.refreshBaseline(); err != nil {
		t.Fatal(err)
	}
	applySpaceDefaultsFixture(t, tasks, space.ID, CapabilitySelection{Extensions: []string{deliveryTestExtension}})
	first, err := tasks.NewConversationInSpace(ctx, space.ID, "after default")
	if err != nil {
		t.Fatal(err)
	}
	chosen, err := tasks.SelectedCapabilities(ctx, first.ID)
	if err != nil || !contains(chosen.Extensions, deliveryTestExtension) {
		t.Fatal(chosen, err)
	}
	previous, err := tasks.SelectedCapabilities(ctx, old)
	if err != nil || len(previous.Extensions) != 0 {
		t.Fatal("old explicit empty was changed", previous, err)
	}
	applySpaceDefaultsFixture(t, tasks, space.ID, CapabilitySelection{})
	second, err := tasks.NewConversationInSpace(ctx, space.ID, "after clear")
	if err != nil {
		t.Fatal(err)
	}
	chosen, err = tasks.SelectedCapabilities(ctx, second.ID)
	if err != nil || len(chosen.Extensions) != 0 {
		t.Fatal(chosen, err)
	}
	chosen, _ = tasks.SelectedCapabilities(ctx, first.ID)
	if !contains(chosen.Extensions, deliveryTestExtension) {
		t.Fatal("default change rewrote old conversation")
	}
	config := tasks.Config()
	config.Extensions.Enabled[deliveryTestExtension] = false
	tasks.core.setConfig(config)
	chosen, err = tasks.SelectedCapabilities(ctx, first.ID)
	if err != nil || len(chosen.Extensions) != 0 {
		t.Fatal("defaults bypassed disable", chosen, err)
	}
}
