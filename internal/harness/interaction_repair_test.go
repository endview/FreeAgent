package harness

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func applyInteractionControl(t *testing.T, core *Service, kind string, args any) {
	t.Helper()
	control, err := NewTaskService(core).Control()
	if err != nil {
		t.Fatal(err)
	}
	op, err := control.PlanControlChange(t.Context(), ControlPlanRequest{Kind: kind, Arguments: mustJSON(args), Authorization: ControlUserDirect})
	if err != nil {
		t.Fatal(err)
	}
	capability, err := control.IssueInteractiveCapability(t.Context(), "", "fixture")
	if err != nil {
		t.Fatal(err)
	}
	code, err := control.ControlCodeForTests(t.Context(), op.ID, "", "fixture")
	if err != nil {
		t.Fatal(err)
	}
	result, err := control.ConfirmControlChange(t.Context(), op.ID, controlAuth(capability, "", "fixture", code))
	if err != nil || !result.Applied {
		t.Fatal("control failed", err)
	}
}

func TestSavedSummaryDefaultAndLimitedRecoveryUseNewConfig(t *testing.T) {
	core, tasks, chat, _ := summaryFixture(t)
	if err := WriteConfig(core.Store.DataDir(), core.Config); err != nil {
		t.Fatal(err)
	}
	if _, err := tasks.ManageContext(t.Context(), chat, ContextCommand{Action: "compact", SummaryBytes: 1024}); err != nil {
		t.Fatal(err)
	}
	applyInteractionControl(t, core, ControlSetRuntimeLimits, map[string]int{"task_summary_bytes": 8192})
	state, err := tasks.ContextState(t.Context(), chat)
	if err != nil || state.ByteLimit != 8192 || state.Summary.ByteLimit != 1024 {
		t.Fatalf("default/summary confused: %+v %v", state, err)
	}
	// Use a separate conversation with enough retained history to hit the hard
	// context budget before any tool can execute.
	core2, tasks2, chat2, _ := summaryFixture(t)
	core2.Config.HistoryPageSize = 3
	core2.Config.MaxOutputTokens = 128
	probe, err := core2.buildContext(t.Context(), Run{ConversationID: chat2, Prompt: "new task needing retained context"})
	if err != nil {
		t.Fatal(err)
	}
	u, err := core2.requestUsage(Run{}, probe)
	if err != nil {
		t.Fatal(err)
	}
	core2.Config.MaxContextBytes = u.Bytes + u.ReservedBytes - 200
	if err := WriteConfig(core2.Store.DataDir(), core2.Config); err != nil {
		t.Fatal(err)
	}
	limited, err := tasks2.Submit(t.Context(), chat2, "limited", "new task needing retained context")
	if !errors.Is(err, ErrContextLimit) {
		t.Fatal("not limited", err)
	}
	applyInteractionControl(t, core2, ControlSetRuntimeLimits, map[string]int{"task_summary_bytes": 8192, "max_context_bytes": 131072})
	compact, err := tasks2.ManageContext(t.Context(), chat2, ContextCommand{Action: "compact", SummaryBytes: 8192})
	if err != nil || !compact.Changed || compact.RunID == limited.Run.ID {
		t.Fatalf("new-config compaction: %+v %v", compact, err)
	}
	next, err := tasks2.Continue(t.Context(), limited.Run.ID)
	if err != nil || next.Run.Status != "completed" || next.Run.ID == limited.Run.ID || next.Run.ContinuationOf != limited.Run.ID {
		t.Fatalf("continuation: %s %v", next.Run.Status, err)
	}
	old, _ := core2.Store.GetRun(t.Context(), limited.Run.ID)
	if old.ConfigJSON != limited.Run.ConfigJSON {
		t.Fatal("rewrote original snapshot")
	}
}

func TestOversizedSummaryPausesSameConfigurationAcrossTurns(t *testing.T) {
	core, _, chat, _ := summaryFixture(t)
	enablePreemptiveCompaction(t, core, chat)
	summaries, notices := 0, 0
	core.Emit = func(event Event) {
		if event.Kind == EventContextCompactionFailed {
			notices++
		}
	}
	core.Model = modelFunc(func(_ context.Context, r ModelRequest, _ func(string)) (ModelResponse, error) {
		if r.Purpose == "compaction" {
			summaries++
			if summaries == 2 && !strings.Contains(r.Messages[0].Content, "1024 UTF-8 bytes") {
				t.Error("repair omitted measured byte count")
			}
			return ModelResponse{Content: strings.Repeat("x", 1024), Provider: &ProviderResponse{FinishReason: "stop", Protocol: "chat", UsageStatus: "missing"}}, nil
		}
		return ModelResponse{Content: "ok"}, nil
	})
	for i := 0; i < 3; i++ {
		result, err := NewTaskService(core).Submit(t.Context(), chat, fmt.Sprintf("oversize-%d", i), "next")
		if err != nil || result.Run.Status != "completed" {
			t.Fatal(err)
		}
	}
	if summaries != 2 || notices != 1 {
		t.Fatalf("repeated oversized compaction: summaries=%d notices=%d", summaries, notices)
	}
	state, err := NewTaskService(core).ContextState(t.Context(), chat)
	if err != nil || !strings.Contains(state.AutomaticPaused, "字节上限") {
		t.Fatal(state.AutomaticPaused, err)
	}
}

func TestNoneModeReadsSettingsAndOnlyProposesPersistentChanges(t *testing.T) {
	calls := 0
	core, _, _ := testHarness(t, modelFunc(func(_ context.Context, r ModelRequest, _ func(string)) (ModelResponse, error) {
		calls++
		if !r.DenyOperations || len(r.Tools) != 2 || !controlOnlyDefinitions(r.Tools) {
			t.Fatalf("None exposed execution tools: %v", r.Tools)
		}
		if calls == 1 {
			return ModelResponse{ToolCalls: []ToolCall{makeCall("read", controlReadTool, map[string]string{"domain": "runtime", "operation": ControlSetRuntimeLimits})}}, nil
		}
		return ModelResponse{ToolCalls: []ToolCall{makeCall("plan", controlPlanTool, map[string]any{"kind": ControlSetRuntimeLimits, "arguments": map[string]int{"task_summary_bytes": 8192}})}}, nil
	}))
	core.controlToolsActive = true
	core.Config.Execution.PermissionMode = PermissionNone
	chat := conversation(t, core)
	old := core.Config.TaskSummaryBytes
	result, err := NewTaskService(core).SubmitWithOptions(t.Context(), chat, "settings-only", "change summary default", RunOptions{Permission: PermissionNone})
	if err != nil || result.Run.Status != "waiting_confirmation" || core.Config.TaskSummaryBytes != old {
		t.Fatalf("proposal did not wait: %s %v", result.Run.Status, err)
	}
	if calls != 2 {
		t.Fatal(calls)
	}
}

func TestNoneModeWithoutProviderToolsStillChats(t *testing.T) {
	core, _, _ := testHarness(t, modelFunc(func(_ context.Context, r ModelRequest, _ func(string)) (ModelResponse, error) {
		if len(r.Tools) != 0 || !r.DenyOperations {
			t.Fatal("unsupported tools advertised")
		}
		return ModelResponse{Content: "plain answer"}, nil
	}))
	core.controlToolsActive = true
	model := core.Config.Models[core.Config.DefaultModel]
	model.Capabilities.Tools = "none"
	core.Config.Models[core.Config.DefaultModel] = model
	chat := conversation(t, core)
	result, err := NewTaskService(core).SubmitWithOptions(t.Context(), chat, "no-tools", "hello", RunOptions{Permission: PermissionNone})
	if err != nil || result.Run.Status != "completed" {
		t.Fatal("chat-only model failed", err)
	}
}

func TestControlCatalogSchemasAndCapabilitiesPatchPreserveWindow(t *testing.T) {
	for _, entry := range ControlOperationCatalog() {
		var schema map[string]any
		if entry.ModelProposable && (json.Unmarshal(entry.InputSchema, &schema) != nil || schema["type"] != "object") {
			t.Errorf("missing typed schema: %s", entry.Kind)
		}
	}
	core, _, _ := testHarness(t, EchoModel{})
	applyInteractionControl(t, core, ControlSetModelProfile, map[string]any{"id": core.Config.DefaultModel, "capabilities": map[string]int{"context_window_tokens": 131072}})
	applyInteractionControl(t, core, ControlSetModelProfile, map[string]any{"id": core.Config.DefaultModel, "capabilities": map[string]int{"output_tokens": 1024}})
	model := core.Config.Models[core.Config.DefaultModel]
	if model.Provider != "echo" || model.Capabilities.ContextTokens != 131072 || model.Capabilities.OutputTokens != 1024 {
		t.Fatalf("patch overwrote profile: %+v", model)
	}
	applyInteractionControl(t, core, ControlSetModelProfile, map[string]any{"id": core.Config.DefaultModel, "capabilities": map[string]any{"vision": VisionCapabilities{Enabled: true, PNG: true, MaxImageBytes: 1024, MaxSide: 128, MaxImages: 1, MaxRequestBytes: 2048}}})
	applyInteractionControl(t, core, ControlSetModelProfile, map[string]any{"id": core.Config.DefaultModel, "capabilities": map[string]any{"vision": map[string]any{}}})
	if core.Config.Models[core.Config.DefaultModel].Capabilities.Vision.Enabled {
		t.Fatal("empty vision declaration silently retained the enabled flag")
	}
	control, _ := NewTaskService(core).Control()
	core.controlToolsActive = true
	tools := &controlTools{service: core, control: control, base: deniedTools{}}
	r, err := tools.readState(t.Context(), controlStateQuery{Domain: "models", Operation: ControlSetModelProfile})
	if err != nil || len(r.Content) > 12000 || !strings.Contains(r.Content, "context_window_tokens") || strings.Contains(r.Content, "extension_states") {
		t.Fatal("unbounded/incomplete model settings read", err, len(r.Content))
	}
	if n := len(mustJSON(tools.controlDefinitions())); n > 3000 || len(tools.controlDefinitions()) != 2 {
		t.Fatal("catalog returned to every request", n)
	}
}

func TestNonSecretProfileControlsReuseConfirmationAndRejectSecretFields(t *testing.T) {
	core, _, _ := testHarness(t, EchoModel{})
	chat := conversation(t, core)
	core.Keys = NewKeyStore(filepath.Join(t.TempDir(), "keys.json"))
	profile, err := core.Keys.Add(KeyInput{Name: "fixture", BaseURL: "https://example.test/v1", Source: KeySourceEnv, EnvName: "FREEAGENT_NONSECRET_FIXTURE", Note: "old note"})
	if err != nil {
		t.Fatal(err)
	}
	control, err := NewTaskService(core).Control()
	if err != nil {
		t.Fatal(err)
	}
	applyInteractionControl(t, core, ControlSetKeyProfile, map[string]any{"id": profile.ID, "name": "renamed", "note": ""})
	saved, err := core.Keys.Find(profile.ID)
	if err != nil || saved.Name != "renamed" || saved.Note != "" || saved.EnvName != profile.EnvName || saved.BaseURL != profile.BaseURL {
		t.Fatal("metadata not preserved", err)
	}
	for _, args := range []map[string]any{
		{"id": profile.ID, "name": "x", "secret": "never-store"},
		{"id": profile.ID, "base_url": "https://other.example.test"},
	} {
		if _, err := control.PlanControlChange(t.Context(), ControlPlanRequest{Kind: ControlSetKeyProfile, Arguments: mustJSON(args), Authorization: ControlModelProposed, ConversationID: chat}); err == nil {
			t.Fatal("model acquired secret/destination write")
		}
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	args := map[string]any{"id": "safe-fixture", "config": map[string]any{"command": executable, "cwd": core.Config.Workspace, "enabled": false, "args": []string{"--fixture"}}}
	applyInteractionControl(t, core, ControlSetMCPProfile, args)
	if item, ok := core.Config.MCPServers["safe-fixture"]; !ok || item.Enabled || item.Command != executable || len(item.Args) != 1 {
		t.Fatal("MCP safe profile not applied")
	}
	bad := map[string]any{"id": "bad-fixture", "config": map[string]any{"command": executable, "cwd": core.Config.Workspace, "env": map[string]string{"TOKEN": "do-not-store"}}}
	if _, err := control.PlanControlChange(t.Context(), ControlPlanRequest{Kind: ControlSetMCPProfile, Arguments: mustJSON(bad), Authorization: ControlModelProposed, ConversationID: chat}); err == nil {
		t.Fatal("model acquired literal env write")
	}
	op, err := control.PlanControlChange(t.Context(), ControlPlanRequest{Kind: ControlRemoveMCP, Arguments: mustJSON(map[string]string{"id": "safe-fixture"}), Authorization: ControlModelProposed, ConversationID: chat})
	if err != nil || op.Status != ControlPending {
		t.Fatal("non-secret remove proposal unavailable", err)
	}
	if _, ok := core.Config.MCPServers["safe-fixture"]; !ok {
		t.Fatal("model proposal applied without confirmation")
	}
}

func TestTelegramSettingsProposalLinksToAuthenticatedWebAndReportsApply(t *testing.T) {
	calls := 0
	r, tasks, _, _, sent := telegramRuntimeFixture(t, modelFunc(func(_ context.Context, request ModelRequest, _ func(string)) (ModelResponse, error) {
		calls++
		if calls == 1 {
			return ModelResponse{ToolCalls: []ToolCall{makeCall("state", controlReadTool, map[string]string{"domain": "models", "operation": ControlSetModelProfile})}}, nil
		}
		found := false
		for _, m := range request.Messages {
			if m.Role == "tool" && strings.Contains(m.Content, `"models"`) && strings.Contains(m.Content, `"capabilities"`) {
				found = true
			}
		}
		if !found {
			t.Error("channel control state omitted current model")
		}
		return ModelResponse{ToolCalls: []ToolCall{makeCall("settings", controlPlanTool, map[string]any{"kind": ControlSetRuntimeLimits, "arguments": map[string]int{"task_summary_bytes": 8192}})}}, nil
	}))
	tasks.core.controlToolsActive = true
	tasks.core.approvalBinding = newID() + newID() // OpenTaskService supplies the owner's public approval binding.
	c := tasks.Config()
	c.PublicWeb = DefaultPublicWebConfig()
	c.PublicWeb.Enabled = true
	c.PublicWeb.Origin = "https://fixture.example.test"
	tasks.core.setConfig(c)
	if err := WriteConfig(tasks.DataDir(), c); err != nil {
		t.Fatal(err)
	}
	admitTelegramFixture(t, r, 90, 7, "private")
	if err := r.HandleUpdate(t.Context(), telegramMessageUpdate(2, 20, 90, 7, "change summary setting", "private")); err != nil {
		t.Fatal(err)
	}
	space, err := tasks.ChannelSpace(t.Context(), ChannelTarget{Source: SpaceSourceTelegram, ApplicationID: "1", ChatID: "90"})
	if err != nil {
		t.Fatal(err)
	}
	run, err := tasks.Latest(t.Context(), space.DefaultConversationID)
	if err != nil || run.Status != "waiting_confirmation" {
		t.Fatal("proposal status", run.Status, err)
	}
	message := (*sent)[len(*sent)-1]
	if len(message.Callback) != 0 || !strings.Contains(message.Text, "https://fixture.example.test/?chat="+run.ConversationID) || !strings.Contains(message.Text, "run="+run.ID) {
		t.Fatalf("no exact Web handoff: %+v", message)
	}
	if tasks.Config().TaskSummaryBytes == 8192 {
		t.Fatal("TG proposal applied without interactive approval")
	}
	ctx := WithPublicEntry(t.Context(), "public:settings-fixture", "192.0.2.20")
	preview, err := tasks.Preview(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	control, err := tasks.Control()
	if err != nil {
		t.Fatal(err)
	}
	principal := InteractivePrincipal(ctx, "fixture")
	cap, err := control.IssueInteractiveCapability(ctx, run.ConversationID, principal)
	if err != nil {
		t.Fatal(err)
	}
	confirmation, err := control.RenderConfirmation(ctx, preview.ControlOperationID, run.ConversationID, principal)
	if err != nil {
		t.Fatal(err)
	}
	result, err := tasks.ConfirmControl(ctx, run.ID, preview, controlAuth(cap, run.ConversationID, principal, confirmation.Code), true)
	if err != nil || result.Run.Status != "completed" || tasks.Config().TaskSummaryBytes != 8192 {
		t.Fatal("authenticated confirmation failed", err)
	}
	before := len(*sent)
	if err := r.Deliver(t.Context(), result); err != nil || len(*sent) != before+1 {
		t.Fatal("applied result did not return to channel", err)
	}
	snapshot, err := tasks.core.snapshot()
	if err != nil {
		t.Fatal(err)
	}
	maintenance, _, err := tasks.core.Store.createRun(t.Context(), run.ConversationID, "maintenance-fixture", "compaction", snapshot, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := tasks.core.Store.FinishRun(t.Context(), maintenance.ID, "failed", "summary_limit"); err != nil {
		t.Fatal(err)
	}
	scope, err := tasks.ExtensionConversationScope(t.Context(), "com.example.fixture", run.ConversationID)
	if err != nil || scope.SpaceID != space.ID || scope.ActorID != "telegram:user:7" {
		t.Fatal("maintenance lost the adopted channel actor", scope, err)
	}
}

func TestDisabledMCPDoesNotHideConversation(t *testing.T) {
	core, _, _ := testHarness(t, EchoModel{})
	tasks := NewTaskService(core)
	chat := conversation(t, core)
	if _, err := tasks.Submit(t.Context(), chat, "history", "retained history"); err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	core.Config.MCPServers = map[string]MCPServerConfig{"fixture": {Enabled: true, Command: executable, Cwd: core.Config.Workspace}}
	if err := WriteConfig(core.Store.DataDir(), core.Config); err != nil {
		t.Fatal(err)
	}
	if err := tasks.SelectCapabilities(t.Context(), chat, CapabilitySelection{MCPServers: []string{"fixture"}}); err != nil {
		t.Fatal(err)
	}
	applyInteractionControl(t, core, ControlSetMCPPolicy, map[string]any{"id": "fixture", "enabled": false})
	view, err := tasks.Chat(t.Context(), chat, "")
	if err != nil || len(view.Turns) != 1 || len(view.Capabilities.MCPServers) != 0 {
		t.Fatalf("disabled MCP hid history: %+v %v", view, err)
	}
}

func TestBranchRetainsExplicitCapabilityExclusions(t *testing.T) {
	core, _, _ := testHarness(t, EchoModel{})
	tasks := NewTaskService(core)
	chat := conversation(t, core)
	writeSkill(t, core.Store.DataDir()+"/skills", "optional", "optional fixture", "manual", "Only explicitly selected")
	core.Config.Skills.Enabled = map[string]bool{"optional": true}
	if err := tasks.SelectCapabilities(t.Context(), chat, CapabilitySelection{}); err != nil {
		t.Fatal(err)
	}
	if _, err := tasks.Submit(t.Context(), chat, "source", "source"); err != nil {
		t.Fatal(err)
	}
	messages, err := tasks.Messages(t.Context(), chat)
	if err != nil {
		t.Fatal(err)
	}
	branch, err := tasks.BranchConversation(t.Context(), chat, BranchCommand{RequestKey: "branch", MessageID: messages[0].ID, Mode: "rerun"})
	if err != nil {
		t.Fatal(err)
	}
	selected, err := tasks.SelectedCapabilities(t.Context(), branch.Conversation.ID)
	if err != nil || len(selected.Skills) != 0 {
		t.Fatal("branch expanded capabilities", selected, err)
	}
}

func TestQueueIgnoresListenerChangeAndCancelsExpiredGuidance(t *testing.T) {
	core, _, _ := testHarness(t, EchoModel{})
	tasks := NewTaskService(core)
	chat := conversation(t, core)
	if _, err := tasks.AcceptInput(t.Context(), chat, InputCommand{Action: "queue", RequestKey: "queued", Prompt: "once"}); err != nil {
		t.Fatal(err)
	}
	applyInteractionControl(t, core, ControlSetServerPolicy, map[string]any{"port": 24680})
	run, err := tasks.RunQueued(t.Context(), chat)
	if err != nil || run.Run.Status != "completed" {
		t.Fatalf("unrelated change stopped queue: %v", err)
	}
	if next, err := tasks.RunQueued(t.Context(), chat); err != nil || next.Run.ID != "" {
		t.Fatal("queue repeated", err)
	}
	item, err := tasks.AcceptInput(t.Context(), chat, InputCommand{Action: "steer", RunID: run.Run.ID, RequestKey: "late", Prompt: "late"})
	if err != nil || item.Status != "target_ended" {
		t.Fatal("late fixture", err)
	}
	for range 2 {
		item, err = tasks.CancelInput(t.Context(), chat, item.Key)
		if err != nil || item.Status != "cancelled" {
			t.Fatal("expired guidance not cancelled", err)
		}
	}
}

func TestQueuedGuidanceExplicitlyDiscardsNewRunOptions(t *testing.T) {
	core, _, _ := testHarness(t, EchoModel{})
	tasks := NewTaskService(core)
	chat := conversation(t, core)
	input, err := tasks.AcceptInput(t.Context(), chat, InputCommand{Action: "queue", RequestKey: "guide-options", Prompt: "adopt this", Capabilities: &CapabilitySelection{}, Reasoning: &ReasoningSelection{Mode: "default"}})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := core.snapshot()
	if err != nil {
		t.Fatal(err)
	}
	run, _, err := core.Store.CreateRun(t.Context(), chat, "active-fixture", "active", snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tasks.SteerInput(t.Context(), chat, input.Key, run.ID); err == nil {
		t.Fatal("options were silently discarded")
	}
	converted, err := tasks.SteerInputWithOptions(t.Context(), chat, input.Key, run.ID, true)
	if err != nil || converted.Action != "steer" || converted.Capabilities != nil || converted.Reasoning != nil || converted.MessageID != input.MessageID {
		t.Fatal("explicit conversion", converted, err)
	}
	unchanged, err := core.Store.GetRun(t.Context(), run.ID)
	if err != nil || unchanged.ConfigJSON != run.ConfigJSON {
		t.Fatal("guidance changed current Run options", err)
	}
}
