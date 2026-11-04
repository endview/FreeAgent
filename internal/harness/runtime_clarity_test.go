package harness

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

func TestUnmatchedCommandRulesFollowModeAndExplicitRulesStillConstrainFull(t *testing.T) {
	for _, mode := range []PermissionMode{PermissionNone, PermissionAsk, PermissionWorkspace, PermissionFull} {
		t.Run(string(mode), func(t *testing.T) {
			service, commands, _ := commandFixture(t)
			service.Config.Commands.Rules = []CommandRule{{Program: "unrelated-fixture-program", Decision: CommandRuleDeny}}
			profile, err := buildExecutionProfile(service.Config, PermissionSelection{Mode: mode, Source: "turn"})
			if err != nil {
				t.Fatal(err)
			}
			commands.Profile = profile
			commands.Config = commandConfigForProfile(service.Config.Commands, profile)
			service.permission = &PermissionSelection{Mode: mode, Source: "turn"}
			prepared, err := commands.Prepare(t.Context(), helperCall(t, "ok"))
			if mode == PermissionNone {
				if err == nil {
					t.Fatal("None acquired commands")
				}
				return
			}
			if errors.Is(err, ErrSandboxUnsupported) {
				t.Skip("OS sandbox unavailable; no host fallback")
			}
			if err != nil {
				t.Fatal("unmatched command denied", err)
			}
			if got := service.needsApproval(prepared); got != (mode == PermissionAsk) {
				t.Fatalf("mode=%s approval=%t", mode, got)
			}
			if mode != PermissionFull {
				return
			}
			program, _ := os.Executable()
			commands.Config.Rules = []CommandRule{{Program: program, Decision: CommandRuleAsk}}
			prepared, err = commands.Prepare(t.Context(), helperCall(t, "ok"))
			if err != nil || !service.needsApproval(prepared) {
				t.Fatal("Full bypassed explicit ask", err)
			}
			commands.Config.Rules = []CommandRule{{Program: program, Decision: CommandRuleDeny}}
			if _, err := commands.Prepare(t.Context(), helperCall(t, "ok")); err == nil || !strings.Contains(err.Error(), "explicit deny") {
				t.Fatal("Full bypassed explicit deny", err)
			}
			commands.Config.Rules = nil
			commands.Config.AllowedExecutables = []string{program + ".different"}
			if _, err := commands.Prepare(t.Context(), helperCall(t, "ok")); err == nil || !strings.Contains(err.Error(), "allowlist") {
				t.Fatal("Full bypassed allowlist", err)
			}
		})
	}
}

func TestRuntimeFactsKeepFrozenFullSeparateFromSavedAskAndRejectOldRules(t *testing.T) {
	service, _, _ := commandFixture(t)
	ctx := t.Context()
	chat := conversation(t, service)
	service.permission = &PermissionSelection{Mode: PermissionFull, Source: "turn"}
	raw, err := service.snapshot()
	if err != nil {
		t.Fatal(err)
	}
	run, _, err := service.Store.CreateRun(ctx, chat, "facts", "inspect", raw)
	if err != nil {
		t.Fatal(err)
	}
	facts, err := NewTaskService(service).RuntimeFacts(ctx, chat, run.ID)
	if err != nil || facts.Permission != PermissionFull || facts.Commands.Backend != CommandExecutionHostExplicit || facts.Commands.Status != "ready" {
		t.Fatal(facts, err)
	}
	control, err := NewTaskService(service).Control()
	if err != nil {
		t.Fatal(err)
	}
	state, err := control.ReadControlState(ctx)
	if err != nil || state.Config.PermissionMode != "ask" {
		t.Fatal(state.Config, err)
	}
	var snapshot map[string]any
	_ = json.Unmarshal([]byte(raw), &snapshot)
	delete(snapshot, "command_rules_version")
	run.ConfigJSON = string(mustJSON(snapshot))
	if _, err := service.forRun(run); !errors.Is(err, ErrConfigChanged) {
		t.Fatal("old semantics gained authority", err)
	}
}

func TestDirectControlExpiresAbsolutelyButRunProposalRemainsRecoverable(t *testing.T) {
	service, _, _ := testHarness(t, EchoModel{})
	control, err := NewTaskService(service).Control()
	if err != nil {
		t.Fatal(err)
	}
	op, err := control.PlanControlChange(t.Context(), ControlPlanRequest{Kind: ControlSetRuntimeLimits, Arguments: json.RawMessage(`{"task_summary_bytes":1024}`), Authorization: ControlUserDirect})
	if err != nil {
		t.Fatal(err)
	}
	control.mu.Lock()
	record := control.operations[op.ID]
	record.Operation.CreatedAt = time.Now().Add(-11 * time.Minute).UTC().Format(time.RFC3339Nano)
	control.operations[op.ID] = record
	control.mu.Unlock()
	if _, err := control.RenderConfirmation(t.Context(), op.ID, "", "fixture"); !errors.Is(err, ErrControlExpired) {
		t.Fatal("expired standalone reissued", err)
	}
	operations, err := control.ListControlOperations(t.Context())
	if err != nil || len(operations) != 1 || operations[0].Status != ControlStale {
		t.Fatal(operations, err)
	}
	model, err := control.PlanControlChange(t.Context(), ControlPlanRequest{Kind: ControlSetRuntimeLimits, Arguments: json.RawMessage(`{"task_summary_bytes":0}`), Authorization: ControlModelProposed, ConversationID: conversation(t, service)})
	if err != nil {
		t.Fatal(err)
	}
	control.mu.Lock()
	record = control.operations[model.ID]
	record.Operation.CreatedAt = time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano)
	control.operations[model.ID] = record
	control.mu.Unlock()
	if _, err := control.RenderConfirmation(t.Context(), model.ID, model.ConversationID, "fixture"); err != nil {
		t.Fatal("Run proposal could not refresh", err)
	}
	visible, err := control.ReadControlStateForModel(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, op := range visible.Pending {
		if op.ExpiresAt != "" {
			t.Fatal("model received credential expiry")
		}
	}
}

func TestContextInspectInsideCurrentRunCanProposeAndDefaultsAreExplicit(t *testing.T) {
	service, _, _ := testHarness(t, EchoModel{})
	chat := conversation(t, service)
	snapshot, _ := service.snapshot()
	run, _, err := service.Store.CreateRun(t.Context(), chat, "inspect", "inspect", snapshot)
	if err != nil {
		t.Fatal(err)
	}
	tasks := NewTaskService(service)
	outside, _ := tasks.ContextState(t.Context(), chat)
	inside, err := tasks.contextState(t.Context(), chat, run.ID, "")
	if err != nil || outside.BlockingRunID != run.ID || inside.Blocked != "" || !inside.CanPropose {
		t.Fatal(outside, inside, err)
	}
	if DefaultConfig(service.Config.Workspace).TaskSummaryBytes != 4096 {
		t.Fatal("new install default disabled")
	}
	var saved Config
	raw := mustJSON(service.Config)
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(raw, &fields)
	delete(fields, "task_summary_bytes")
	_ = json.Unmarshal(mustJSON(fields), &saved)
	if saved.TaskSummaryBytes != 0 {
		t.Fatal("omitted existing setting was enabled")
	}
}

func TestRunControlProposalCanRebindFreshPublicSessionAfterOwnerRestart(t *testing.T) {
	service, files, _ := testHarness(t, EchoModel{})
	chat := conversation(t, service)
	snapshot, _ := service.snapshot()
	run, _, err := service.Store.CreateRun(t.Context(), chat, "proposal", "change setting", snapshot)
	if err != nil {
		t.Fatal(err)
	}
	model, err := service.Store.startModel(t.Context(), run, `{}`, 12)
	if err != nil {
		t.Fatal(err)
	}
	call := makeCall("proposal", controlPlanTool, map[string]any{"kind": ControlSetRuntimeLimits, "arguments": map[string]int{"task_summary_bytes": 1024}})
	if err := service.Store.saveModel(t.Context(), run, model, ModelResponse{ToolCalls: []ToolCall{call}}); err != nil {
		t.Fatal(err)
	}
	attempts, _ := service.Store.ListAttempts(t.Context(), run.ID)
	attempt := attempts[len(attempts)-1]
	control, err := NewTaskService(service).Control()
	if err != nil {
		t.Fatal(err)
	}
	oldCtx := WithPublicEntry(t.Context(), "public:old-session", "192.0.2.1")
	op, err := control.PlanControlChange(oldCtx, ControlPlanRequest{Kind: ControlSetRuntimeLimits, Arguments: json.RawMessage(`{"task_summary_bytes":1024}`), Authorization: ControlModelProposed, ConversationID: chat})
	if err != nil {
		t.Fatal(err)
	}
	if err := control.bindAttempt(op.ID, attempt.ID); err != nil {
		t.Fatal(err)
	}
	if err := service.Store.FinishRun(t.Context(), run.ID, "waiting_confirmation", StopReasonControlProposal); err != nil {
		t.Fatal(err)
	}
	oldCode, err := control.RenderConfirmation(oldCtx, op.ID, chat, "public:old-session")
	if err != nil {
		t.Fatal(err)
	}
	freshCtx := WithPublicEntry(t.Context(), "public:new-session", "192.0.2.1")
	if _, err := control.RenderConfirmation(freshCtx, op.ID, chat, "public:new-session"); !errors.Is(err, ErrControlUserInteractionRequired) {
		t.Fatal("live credential transferred across sessions", err)
	}
	restarted := NewService(service.Store, service.Config, Adapters{Model: EchoModel{}, Tools: files})
	fresh, err := NewTaskService(restarted).Control()
	if err != nil {
		t.Fatal(err)
	}
	newCode, err := fresh.RenderConfirmation(freshCtx, op.ID, chat, "public:new-session")
	if err != nil || newCode.Code == oldCode.Code {
		t.Fatal("fresh owner could not restore proposal", err)
	}
	if fresh.operations[op.ID].Operation.RemotePrincipal != "public:new-session" {
		t.Fatal("proposal retained stale session binding")
	}
}
