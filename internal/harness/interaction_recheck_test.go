package harness

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func recheckContextLimited(t *testing.T) (*Service, *TaskService, Run) {
	t.Helper()
	core, tasks, chat, _ := summaryFixture(t)
	core.Config.HistoryPageSize = 3
	core.Config.MaxOutputTokens = 128
	probe, err := core.buildContext(t.Context(), Run{ConversationID: chat, Prompt: "unfinished context task"})
	if err != nil {
		t.Fatal(err)
	}
	usage, err := core.requestUsage(Run{}, probe)
	if err != nil {
		t.Fatal(err)
	}
	core.Config.MaxContextBytes = usage.Bytes + usage.ReservedBytes - 200
	if err := WriteConfig(core.Store.DataDir(), core.Config); err != nil {
		t.Fatal(err)
	}
	result, err := tasks.Submit(t.Context(), chat, "limited-review", "unfinished context task")
	if !errors.Is(err, ErrContextLimit) || result.Run.Status != "limited" {
		t.Fatalf("limit fixture: %s %v", result.Run.Status, err)
	}
	return core, tasks, result.Run
}

func TestRecheckTextContinueAfterSavingContextBudget(t *testing.T) {
	core, tasks, run := recheckContextLimited(t)
	applyInteractionControl(t, core, ControlSetRuntimeLimits, map[string]int{"max_context_bytes": 131072})
	result, err := tasks.Submit(t.Context(), run.ConversationID, "typed-continue", "继续")
	if err != nil || result.Run.Status != "completed" || result.Run.ContinuationOf != run.ID {
		t.Fatalf("typed continuation did not recover: %s %v", result.Run.Status, err)
	}
	again, err := tasks.Submit(t.Context(), run.ConversationID, "typed-continue", "继续")
	if err != nil || !again.Duplicate || again.Run.ID != result.Run.ID {
		t.Fatal("continuation retry lost receipt", err)
	}
}

func TestRecheckInPlaceContinuationHasRequestReceipt(t *testing.T) {
	core, tasks, run, calls := limitedFixture(t)
	// An unchanged configuration (including a previously explicit model
	// upgrade) must continue the original Run without resetting consumption.
	if err := core.Store.FinishRun(t.Context(), run.ID, "limited", "context budget reached"); err != nil {
		t.Fatal(err)
	}
	result, err := tasks.Submit(t.Context(), run.ConversationID, "resume-in-place", "继续")
	if err != nil || result.Run.ID != run.ID || result.Run.Status != "completed" || result.Run.Steps < run.Steps {
		t.Fatalf("in-place continuation: %s %v", result.Run.Status, err)
	}
	accepted, found, err := tasks.RequestStatus(t.Context(), run.ConversationID, "resume-in-place")
	if err != nil || !found || accepted.ID != run.ID {
		t.Fatalf("no durable continuation receipt: found=%t error=%v", found, err)
	}
	count := calls.Load()
	again, err := NewTaskService(core).Submit(t.Context(), run.ConversationID, "resume-in-place", "继续")
	if err != nil || !again.Duplicate || again.Run.ID != run.ID || calls.Load() != count {
		t.Fatal("retried continuation executed again", err)
	}
	if _, err := tasks.Submit(t.Context(), run.ConversationID, "resume-in-place", "different task"); !errors.Is(err, ErrRequestKey) {
		t.Fatal("continuation key reused for different content", err)
	}
}

func TestRecheckLimitedContinuationHonorsNewTurnOptions(t *testing.T) {
	for _, kind := range []string{"permission", "reasoning", "capabilities", "model", "memory", "attachment"} {
		t.Run(kind, func(t *testing.T) {
			calls := &atomic.Int32{}
			core, _, _ := testHarness(t, modelFunc(func(context.Context, ModelRequest, func(string)) (ModelResponse, error) {
				calls.Add(1)
				return ModelResponse{Content: "remaining answer"}, nil
			}))
			core.Config.Models["second"] = ModelConfig{Provider: "echo"}
			writeSkill(t, core.Store.DataDir()+"/skills", "optional", "optional fixture", "manual", "Only explicitly selected")
			core.Config.Skills.Enabled = map[string]bool{"optional": true}
			tasks := NewTaskService(core)
			first, err := tasks.SubmitWithOptions(t.Context(), "", "prior-options", "first task", RunOptions{Capabilities: &CapabilitySelection{Skills: []string{"optional"}}})
			if err != nil {
				t.Fatal(err)
			}
			run := first.Run
			if err := core.Store.FinishRun(t.Context(), run.ID, "limited", "context budget reached"); err != nil {
				t.Fatal(err)
			}
			options := RunOptions{}
			switch kind {
			case "permission":
				options.Permission = PermissionNone
			case "reasoning":
				options.Reasoning = &ReasoningSelection{Mode: "default"}
			case "capabilities":
				options.Capabilities = &CapabilitySelection{}
			case "model":
				options.ModelID = "second"
			case "memory":
				options.Memory = &MemorySelection{TaskPrivate: true}
			case "attachment":
				attachment, err := tasks.UploadAttachment(t.Context(), run.ConversationID, "continuation.txt", strings.NewReader("additional reviewed context"))
				if err != nil {
					t.Fatal(err)
				}
				options.Attachments = []string{attachment.ID}
			}
			result, err := tasks.SubmitWithOptions(t.Context(), run.ConversationID, "new-options", "继续", options)
			if err != nil {
				t.Fatal(err)
			}
			if result.Run.ID == run.ID || result.Run.ContinuationOf != run.ID {
				t.Fatal("changed options silently continued frozen Run")
			}
			snapshot, err := decodeRunSnapshot(result.Run.ConfigJSON)
			if err != nil {
				t.Fatal(err)
			}
			if kind == "permission" && snapshot.Permission.Mode != PermissionNone {
				t.Fatal("ignored narrower permission")
			}
			if kind == "reasoning" && (snapshot.Reasoning == nil || *snapshot.Reasoning != *options.Reasoning) {
				t.Fatal("ignored reasoning")
			}
			if kind == "capabilities" && len(snapshot.Capabilities.Skills) != 0 {
				t.Fatal("ignored excluded skill")
			}
			if kind == "model" && result.Run.ModelID != "second" {
				t.Fatal("ignored explicit model")
			}
			if kind == "memory" && (snapshot.Memory == nil || !snapshot.Memory.Selection.TaskPrivate) {
				t.Fatal("ignored memory selection")
			}
			if kind == "attachment" && (len(snapshot.InputAttachments) != 1 || snapshot.InputAttachments[0].ID != options.Attachments[0]) {
				t.Fatal("ignored attachment")
			}
			old, err := core.Store.GetRun(t.Context(), run.ID)
			if err != nil || old.Steps != run.Steps || old.ConfigJSON != run.ConfigJSON {
				t.Fatal("changed prior accounting or snapshot", err)
			}
			count := calls.Load()
			again, err := tasks.SubmitWithOptions(t.Context(), run.ConversationID, "new-options", "继续", options)
			if err != nil || !again.Duplicate || again.Run.ID != result.Run.ID || count != calls.Load() {
				t.Fatal("continued twice", err)
			}
		})
	}
}

func TestRecheckContinuationOptionsKeepFrozenAndUnknownBoundaries(t *testing.T) {
	for _, unknown := range []bool{false, true} {
		t.Run(map[bool]string{false: "same choices", true: "unknown blocks new choices"}[unknown], func(t *testing.T) {
			core, tasks, previous, calls := limitedFixture(t)
			if err := core.Store.FinishRun(t.Context(), previous.ID, "limited", "context budget reached"); err != nil {
				t.Fatal(err)
			}
			snapshot, err := decodeRunSnapshot(previous.ConfigJSON)
			if err != nil {
				t.Fatal(err)
			}
			options := RunOptions{ModelID: previous.ModelID, Permission: snapshot.Permission.Mode, Capabilities: snapshot.Capabilities, Reasoning: snapshot.Reasoning}
			count := calls.Load()
			if unknown {
				options.Permission = PermissionNone
				if _, err := core.Store.db.ExecContext(t.Context(), `UPDATE fa.attempts SET status='unknown' WHERE run_id=$1 AND kind='tool'`, previous.ID); err != nil {
					t.Fatal(err)
				}
			}
			result, err := tasks.SubmitWithOptions(t.Context(), previous.ConversationID, "boundary", "继续", options)
			if unknown {
				if err == nil || result.Run.ID != "" || calls.Load() != count {
					t.Fatal("unknown result escaped with changed options", err)
				}
				if _, found, err := tasks.RequestStatus(t.Context(), previous.ConversationID, "boundary"); err != nil || found {
					t.Fatal("rejected options consumed request key", err)
				}
			} else if err != nil || result.Run.ID != previous.ID || result.Run.Steps < previous.Steps {
				t.Fatal("same choices replaced original accounting", err)
			}
		})
	}
}

func TestRecheckUncertainMaintenanceRemainsVisible(t *testing.T) {
	for _, status := range []string{"pending", "dispatched", "unknown"} {
		t.Run(status, func(t *testing.T) {
			core, tasks, run, _ := limitedFixture(t)
			if _, err := core.Store.db.ExecContext(t.Context(), `INSERT INTO fa.meta(key,value) VALUES($1,'compaction')`, maintenanceMetaPrefix+run.ID); err != nil {
				t.Fatal(err)
			}
			if err := core.Store.FinishRun(t.Context(), run.ID, "failed", "maintenance failure"); err != nil {
				t.Fatal(err)
			}
			if _, err := core.Store.db.ExecContext(t.Context(), `UPDATE fa.attempts SET status=$1 WHERE run_id=$2 AND kind='tool'`, status, run.ID); err != nil {
				t.Fatal(err)
			}
			latest, err := tasks.Latest(t.Context(), run.ConversationID)
			if err != nil || latest.ID != run.ID {
				t.Fatal("uncertain maintenance hidden", err)
			}
			if _, err := tasks.ContinueLatest(t.Context(), run.ConversationID); err == nil {
				t.Fatal("uncertain maintenance continued")
			}
		})
	}
}

func TestRecheckFailedCompactionDoesNotHideUnfinishedTask(t *testing.T) {
	core, tasks, run := recheckContextLimited(t)
	applyInteractionControl(t, core, ControlSetRuntimeLimits, map[string]int{"max_context_bytes": 131072})
	original := core.Model
	core.Model = modelFunc(func(_ context.Context, r ModelRequest, _ func(string)) (ModelResponse, error) {
		return ModelResponse{Content: strings.Repeat("x", 2048)}, nil
	})
	compact, err := tasks.ManageContext(t.Context(), run.ConversationID, ContextCommand{Action: "compact", SummaryBytes: 512})
	if err == nil || compact.RunID == "" {
		t.Fatal("expected deterministic summary failure")
	}
	latest, err := tasks.Latest(t.Context(), run.ConversationID)
	if err != nil || latest.ID != run.ID {
		t.Fatalf("failed maintenance hid unfinished Run: latest=%s original=%s error=%v", latest.ID, run.ID, err)
	}
	core.Model = original
	next, err := tasks.ContinueLatest(t.Context(), run.ConversationID)
	if err != nil || next.Run.ContinuationOf != run.ID {
		t.Fatal("continue latest after failed compression", err)
	}
	failed, err := core.Store.GetRun(t.Context(), compact.RunID)
	if err != nil || failed.Status != "failed" {
		t.Fatal("lost failed maintenance evidence", err)
	}
}

func TestRecheckSuccessorRetainsExplicitReasoning(t *testing.T) {
	core, tasks, run, _ := limitedFixture(t)
	selected := ReasoningSelection{Mode: "default"}
	result, err := tasks.ContinueWithOptions(t.Context(), run.ID, RunOptions{Reasoning: &selected})
	if err != nil {
		t.Fatal(err)
	}
	saved, err := decodeRunSnapshot(result.Run.ConfigJSON)
	if err != nil || saved.Reasoning == nil || *saved.Reasoning != selected {
		t.Fatal("successor ignored reasoning choice", err)
	}
	_ = core
}

func TestRecheckProviderRemovalCannotExpandAfterPreview(t *testing.T) {
	for _, direct := range []bool{false, true} {
		t.Run(map[bool]string{true: "direct", false: "proposal"}[direct], func(t *testing.T) {
			core, _, _ := testHarness(t, EchoModel{})
			core.Keys = NewKeyStore(filepath.Join(t.TempDir(), "keys.json"))
			profile, err := core.Keys.Add(KeyInput{Name: "review-provider", BaseURL: "https://example.test/v1", Source: KeySourceEnv, EnvName: "FREEAGENT_RECHECK_FIXTURE"})
			if err != nil {
				t.Fatal(err)
			}
			model := map[string]any{"id": "old", "provider": "openai-compatible", "key": profile.ID, "model": "fixture"}
			applyInteractionControl(t, core, ControlSetModelProfile, model)
			control, _ := tasksControl(t, core)
			var op ControlOperation
			if direct {
				op, err = control.PlanKeyProfileChange(t.Context(), KeyControlRequest{Ref: profile.ID, Remove: true, DisableModels: true})
			} else {
				op, err = control.PlanControlChange(t.Context(), ControlPlanRequest{Kind: ControlRemoveKey, Arguments: mustJSON(map[string]any{"id": profile.ID, "disable_models": true}), Authorization: ControlUserDirect})
			}
			if err != nil {
				t.Fatal(err)
			}
			model["id"] = "new-after-preview"
			applyInteractionControl(t, core, ControlSetModelProfile, model)
			cap, err := control.IssueInteractiveCapability(t.Context(), "", "review")
			if err != nil {
				t.Fatal(err)
			}
			code, err := control.RenderConfirmation(t.Context(), op.ID, "", "review")
			if err == nil {
				_, err = control.ConfirmControlChange(t.Context(), op.ID, controlAuth(cap, "", "review", code.Code))
			}
			if !errors.Is(err, ErrControlStaleRevision) {
				t.Fatalf("changed removal targets not rejected: %v", err)
			}
			if _, err := core.Keys.Find(profile.ID); err != nil {
				t.Fatal("credential removed", err)
			}
			if _, ok := core.Config.Models["new-after-preview"]; !ok {
				t.Fatal("unreviewed model removed")
			}
		})
	}
}

func tasksControl(t *testing.T, core *Service) (*ControlService, error) {
	t.Helper()
	c, e := NewTaskService(core).Control()
	if e != nil {
		t.Fatal(e)
	}
	return c, e
}

func TestRecheckDefaultModelUpdateInvalidatesProfileDraft(t *testing.T) {
	core, _, _ := testHarness(t, EchoModel{})
	control, _ := tasksControl(t, core)
	_, revisions := control.SettingsSnapshot()
	resource := "config.model:" + core.Config.DefaultModel
	revision := revisions[resource]
	if revision == 0 {
		revision = 1
	}
	applyInteractionControl(t, core, ControlSetModelProfile, map[string]any{"id": core.Config.DefaultModel, "provider": "echo", "default": true, "capabilities": map[string]int{"context_window_tokens": 131072}})
	_, err := control.PlanControlChange(t.Context(), ControlPlanRequest{Kind: ControlSetModelProfile, Authorization: ControlUserDirect, Arguments: mustJSON(map[string]any{"id": core.Config.DefaultModel, "capabilities": map[string]int{"context_window_tokens": 0}}), ExpectedRevisions: map[string]uint64{resource: revision}})
	if !errors.Is(err, ErrControlStaleRevision) {
		t.Fatalf("old model draft accepted after default profile update: %v", err)
	}
}
