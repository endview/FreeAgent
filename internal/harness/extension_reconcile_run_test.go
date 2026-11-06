package harness

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExtensionLostACKModelProposalResolvesOriginalUnknown(t *testing.T) {
	core, _, _ := testHarness(t, EchoModel{})
	root := installStateTest(t, core)
	core.controlToolsActive = true
	if _, err := core.Control(); err != nil {
		t.Fatal(err)
	}
	var modelCalls int
	core.Model = modelFunc(func(context.Context, ModelRequest, func(string)) (ModelResponse, error) {
		modelCalls++
		return ModelResponse{ToolCalls: []ToolCall{{ID: "proposal", Name: controlPlanTool, Arguments: mustJSON(map[string]any{"kind": ControlChangeExtensionState, "arguments": ExtensionStateChange{ID: "com.example.state-test", Scope: ExtensionStateScope{SpaceID: "local", RoleID: "maya", ActorID: "owner"}, Action: "apply_proposal", ExpectedRevision: 1, SourceID: "input", Proposal: json.RawMessage(`{}`)}})}}}, nil
	})
	tasks := NewTaskService(core)
	chat := conversation(t, core)
	result, err := tasks.Submit(t.Context(), chat, "state-proposal", "propose a character change")
	if err != nil || result.Run.Status != "waiting_confirmation" {
		t.Fatal(result, err)
	}
	preview, err := tasks.Preview(t.Context(), result.Run.ID)
	if err != nil {
		t.Fatal(err)
	}
	control, _ := tasks.Control()
	cap, _ := control.IssueInteractiveCapability(t.Context(), chat, "user")
	confirmation, err := control.RenderConfirmation(t.Context(), preview.ControlOperationID, chat, "user")
	if err != nil {
		t.Fatal(err)
	}
	_, err = tasks.ConfirmControl(t.Context(), result.Run.ID, preview, controlAuth(cap, chat, "user", confirmation.Code), true)
	if !errors.Is(err, ErrUnknownResult) {
		t.Fatal("expected uncertain commit", err)
	}
	original, err := core.Store.GetAttempt(t.Context(), preview.Attempt)
	if err != nil || original.Status != "unknown" {
		t.Fatal(original, err)
	}
	if _, err = control.ReconcileExtensionChange(t.Context(), preview.ControlOperationID); err != nil {
		t.Fatal(err)
	}
	run, attempts, _, err := tasks.Inspect(t.Context(), result.Run.ID)
	if err != nil || run.Status != "completed" {
		t.Fatal(run, err)
	}
	found := false
	for _, a := range attempts {
		if a.ID == original.ID {
			found = a.Status == "unknown" && a.Resolution == "verified_effect"
		}
	}
	if !found {
		t.Fatal("original unknown evidence overwritten or unresolved", attempts)
	}
	if _, err = control.ReconcileExtensionChange(t.Context(), preview.ControlOperationID); err != nil {
		t.Fatal(err)
	}
	writes, err := os.ReadFile(filepath.Join(root, "writes"))
	if err != nil || strings.Count(string(writes), "applied") != 1 || modelCalls != 1 {
		t.Fatal("reconciliation replayed work", string(writes), modelCalls, err)
	}
}
