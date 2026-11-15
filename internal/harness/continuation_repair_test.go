package harness

import (
	"context"
	"encoding/json"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strconv"
	"strings"
	"testing"
)

func TestReadDenialContinuesAndRetainsControlSchema(t *testing.T) {
	calls := 0
	core, _, _ := testHarness(t, modelFunc(func(_ context.Context, request ModelRequest, _ func(string)) (ModelResponse, error) {
		calls++
		if request.Purpose == "verification" {
			t.Fatal("read-only denial invoked verifier")
		}
		if calls == 1 {
			return ModelResponse{ToolCalls: []ToolCall{makeCall("read", controlReadTool, map[string]string{"domain": "extensions", "operation": ControlSetRuntimeLimits, "extension_id": "cc.endview.affectweave"})}}, nil
		}
		last := request.Messages[len(request.Messages)-1]
		if last.Role != "tool" || !strings.Contains(last.Content, "extension_not_selected") || !strings.Contains(last.Content, "context_byte_limit_mode") {
			t.Fatalf("missing partial schema: %s", last.Content)
		}
		return ModelResponse{Content: "Select the extension before reading its state."}, nil
	}))
	core.controlToolsActive = true
	core.Config.ModelRetries = 2
	if err := WriteConfig(core.Store.DataDir(), core.Config); err != nil {
		t.Fatal(err)
	}
	result, err := core.Run(t.Context(), conversation(t, core), "read-denied", "check settings")
	if err != nil || result.Run.Status != "completed" || calls != 2 {
		t.Fatal(result.Run.Status, calls, err)
	}
	attempts, err := core.Store.ListAttempts(t.Context(), result.Run.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range attempts {
		if a.Status == "unknown" {
			t.Fatal("read denial remained unknown")
		}
	}
}

func TestUnknownSourcesRequireExplicitClassification(t *testing.T) {
	files, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		if !strings.HasSuffix(file.Name(), ".go") || strings.HasSuffix(file.Name(), "_test.go") {
			continue
		}
		f, err := parser.ParseFile(token.NewFileSet(), file.Name(), nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || len(call.Args) < 3 {
				return true
			}
			selector, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || selector.Sel.Name != "markUnknown" {
				return true
			}
			if literal, ok := call.Args[2].(*ast.BasicLit); ok && literal.Kind == token.STRING {
				reason, _ := strconv.Unquote(literal.Value)
				if unknownSource(reason) == "unclassified" {
					t.Errorf("%s: classify new UNKNOWN source %q", file.Name(), reason)
				}
			}
			return true
		})
	}
	if unknownSource("new backend failure") != "unclassified" {
		t.Fatal("unknown sources must block")
	}
}

func TestHandoffCrashBoundariesAndLateEvidence(t *testing.T) {
	for _, stage := range []string{"before_handoff", "after_handoff", "after_final", "storage_uncertain"} {
		t.Run(stage, func(t *testing.T) {
			core, _, _ := testHarness(t, EchoModel{})
			chat := conversation(t, core)
			snapshot, err := core.snapshot()
			if err != nil {
				t.Fatal(err)
			}
			run, _, err := core.Store.CreateRun(t.Context(), chat, "crash", "create once", snapshot)
			if err != nil {
				t.Fatal(err)
			}
			call := makeCall("write", "file_create", map[string]string{"path": "once.txt", "content": "only once"})
			tx, err := core.Store.db.BeginTx(t.Context(), nil)
			if err != nil {
				t.Fatal(err)
			}
			if err = insertMessage(t.Context(), tx, run, ModelMessage{Role: "assistant", ToolCalls: []ToolCall{call}}); err != nil {
				t.Fatal(err)
			}
			if err = tx.Commit(); err != nil {
				t.Fatal(err)
			}
			a, err := core.Store.startTool(t.Context(), run, call)
			if err != nil {
				t.Fatal(err)
			}
			if err = core.Store.dispatchTool(t.Context(), a, 0); err != nil {
				t.Fatal(err)
			}
			if _, err = core.Store.db.ExecContext(t.Context(), `INSERT INTO fa.meta(key,value) VALUES($1,$2)`, "attempt/dispatch-contract/"+a.ID, `{"effect":"modify","business":false,"record_message":true}`); err != nil {
				t.Fatal(err)
			}
			result := ToolResult{Content: "partial evidence", Outcome: &ToolOutcome{Status: "unknown"}, Receipts: []ToolReceipt{{Kind: "file", Status: "unknown"}}}
			raw := string(mustJSON(result))
			reason := "tool outcome unknown"
			if stage == "storage_uncertain" {
				reason = "tool output accounting stopped; effects may have occurred"
			}
			if err = core.Store.markUnknown(t.Context(), a, reason, raw); err != nil {
				t.Fatal(err)
			}
			a.Status, a.ResponseJSON = "unknown", raw
			if stage == "after_handoff" || stage == "after_final" {
				if err = core.handoffTool(t.Context(), run, a, call, nil); err != nil {
					t.Fatal(err)
				}
			}
			if stage == "after_final" {
				if err = core.Store.FinishRunOutput(t.Context(), run, "completed", "", "partial work complete"); err != nil {
					t.Fatal(err)
				}
			}
			for range 2 {
				if err = core.Store.recover(t.Context()); err != nil {
					t.Fatal(err)
				}
			}
			saved, err := core.Store.GetRun(t.Context(), run.ID)
			if err != nil {
				t.Fatal(err)
			}
			if stage == "storage_uncertain" {
				if saved.Status != "unknown" {
					t.Fatal("storage uncertainty handed off", saved.Status)
				}
				return
			}
			want := "failed"
			if stage == "after_final" {
				want = "completed"
			}
			if saved.Status != want {
				t.Fatal(saved.Status, want)
			}
			ok, err := core.Store.handoffRecorded(t.Context(), run.ID, a.ID, call.ID)
			if err != nil || !ok {
				t.Fatal(ok, err)
			}
			var count int
			if err = core.Store.db.QueryRowContext(t.Context(), `SELECT count(*) FROM fa.messages WHERE run_id=$1 AND body_json->>'ToolCallID'=$2`, run.ID, call.ID).Scan(&count); err != nil || count != 1 {
				t.Fatal("duplicate result", count, err)
			}
			if _, err = core.Run(t.Context(), chat, "new-run", "independent question"); err != nil {
				t.Fatal(err)
			}
			if err = core.Store.markUnknown(t.Context(), a, "tool outcome unknown", `{"Content":"late observation"}`); err != nil {
				t.Fatal(err)
			}
			late, err := core.Store.GetRun(t.Context(), run.ID)
			if err != nil || late.Status != want {
				t.Fatal("late evidence relocked old run", late.Status, err)
			}
			items, err := NewTaskService(core).Unresolved(t.Context(), chat, 0)
			if err != nil || len(items) != 1 || items[0].Number != 1 {
				t.Fatal(items, err)
			}
		})
	}
}

func TestUnknownHandoffContinuesBlocksBatchAndSurvivesRestart(t *testing.T) {
	calls, writes := 0, 0
	core, files, _ := testHarness(t, modelFunc(func(_ context.Context, r ModelRequest, _ func(string)) (ModelResponse, error) {
		calls++
		if calls == 1 {
			return ModelResponse{ToolCalls: []ToolCall{makeCall("first", "file_create", map[string]string{"path": "result.txt", "content": "done"}), makeCall("second", "file_create", map[string]string{"path": "must-not-execute.txt", "content": "bad"})}}, nil
		}
		if err := completeMessageGroups(r.Messages); err != nil {
			t.Fatal(err)
		}
		return ModelResponse{Content: "Independent work finished."}, nil
	}))
	core.Tools = uncertainCreateTools{files, &writes}
	core.Approve = func(context.Context, ToolCall, PreparedTool) (bool, error) { return true, nil }
	chat := conversation(t, core)
	result, err := core.Run(t.Context(), chat, "handoff", "do work")
	if err != nil || result.Run.Status != "completed" || writes != 1 || !strings.Contains(result.Answer, "#1") {
		t.Fatal(result.Run.Status, writes, result.Answer, err)
	}
	items, err := NewTaskService(core).Unresolved(t.Context(), chat, 0)
	if err != nil || len(items) != 1 || items[0].Number != 1 {
		t.Fatal(items, err)
	}
	attempts, err := core.Store.ListAttempts(t.Context(), result.Run.ID)
	if err != nil {
		t.Fatal(err)
	}
	unknown, skipped := false, false
	for _, a := range attempts {
		if a.Status == "unknown" {
			unknown = true
			if a.Resolution != "handed_off" {
				t.Fatal(a)
			}
		}
		skipped = skipped || strings.Contains(a.ResponseJSON, "本次未执行")
	}
	if !unknown || !skipped {
		t.Fatal(attempts)
	}
	if err := core.Store.recover(t.Context()); err != nil {
		t.Fatal(err)
	}
	saved, err := core.Store.GetRun(t.Context(), result.Run.ID)
	if err != nil || saved.Status != "completed" {
		t.Fatal(saved.Status, err)
	}
	if _, err := core.Run(t.Context(), chat, "next", "continue independent work"); err != nil {
		t.Fatal(err)
	}
	item := items[0]
	if err := NewTaskService(core).ResolveUnresolved(t.Context(), chat, item.ID, item.Revision, "ignored", "", "test-user"); err != nil {
		t.Fatal(err)
	}
	var original ToolCall
	for _, a := range attempts {
		if a.Status == "unknown" {
			_ = json.Unmarshal([]byte(a.RequestJSON), &original)
		}
	}
	original.ID = "changed-call-id"
	replay, err := core.replayUnresolved(t.Context(), result.Run, original)
	if err != nil || !replay {
		t.Fatal("ignore removed replay protection", err)
	}
	tasks := NewTaskService(core)
	messages, err := tasks.Messages(t.Context(), chat)
	if err != nil {
		t.Fatal(err)
	}
	branch, err := tasks.BranchConversation(t.Context(), chat, BranchCommand{RequestKey: "inherit-unresolved", MessageID: messages[0].ID, Mode: "rerun"})
	if err != nil {
		t.Fatal(err)
	}
	inherited, err := tasks.Unresolved(t.Context(), branch.Conversation.ID, 0)
	if err != nil || len(inherited) != 1 || inherited[0].ID != item.ID || inherited[0].Number != 1 {
		t.Fatal(inherited, err)
	}
	if err = tasks.ResolveUnresolved(t.Context(), branch.Conversation.ID, item.ID, inherited[0].Revision, "user_confirmed_not_applied", "user checked", "test-user"); err != nil {
		t.Fatal(err)
	}
	if replay, err = core.replayUnresolved(t.Context(), result.Run, original); err != nil || replay {
		t.Fatal("explicit not-applied confirmation did not release extra replay gate", replay, err)
	}
	confirmed, err := tasks.Unresolved(t.Context(), chat, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err = tasks.ResolveUnresolved(t.Context(), chat, item.ID, confirmed[0].Revision, "model_note", "model opinion only", "model_opinion"); err != nil {
		t.Fatal(err)
	}
	annotated, err := tasks.Unresolved(t.Context(), chat, 0)
	if err != nil || annotated[0].Actor != "test-user" || annotated[0].Resolution != "user_confirmed_not_applied" || annotated[0].ModelNote != "model opinion only" {
		t.Fatal(annotated, err)
	}
}

func TestRecentRetentionCountsWholeSuffix(t *testing.T) {
	core, _, _ := testHarness(t, EchoModel{})
	core.Config.SummaryTargetMode = "auto"
	core.Config.MaxOutputTokens = 128
	core.Config.MaxContextBytes = 16000
	chat := conversation(t, core)
	through := insertProjectionFixture(t, core, chat, []ModelMessage{{Role: "user", Content: "hello"}, {Role: "assistant", Content: strings.Repeat("finished response ", 800)}, {Role: "user", Content: "continue"}, {Role: "assistant", Content: strings.Repeat("finished more ", 500)}})
	retained, err := core.retainRecentUserText(t.Context(), Run{ConversationID: chat}, 0, through)
	if err != nil || retained == 0 {
		t.Fatal("short human input pinned long suffix", retained, err)
	}
}

func TestAutomaticBytesPreserveLegacyAndImages(t *testing.T) {
	config := DefaultConfig(t.TempDir())
	binding := ModelBinding{Config: ModelConfig{Capabilities: ModelCapabilities{ContextTokens: 128000}}}
	window := contextWindow(config, configLimits(config), binding, 4096)
	if window.ByteLimit != 1024000 || window.ByteLimitMode != "auto" {
		t.Fatal(window)
	}
	config.ContextByteLimitMode = ""
	legacy := contextWindow(config, configLimits(config), binding, 4096)
	if legacy.ByteLimit != 128<<10 || legacy.ByteLimitMode != "legacy_manual" {
		t.Fatal(legacy)
	}
}

func TestLegacyReadDenialReconciliationPreservesEvidenceAndIsIdempotent(t *testing.T) {
	core, _, _ := testHarness(t, EchoModel{})
	core.controlToolsActive = true
	chat := conversation(t, core)
	snapshot, err := core.snapshot()
	if err != nil {
		t.Fatal(err)
	}
	run, _, err := core.Store.CreateRun(t.Context(), chat, "legacy", "read extension", snapshot)
	if err != nil {
		t.Fatal(err)
	}
	call := makeCall("denied", controlReadTool, map[string]string{"extension_id": "cc.endview.affectweave"})
	tx, err := core.Store.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = insertMessage(t.Context(), tx, run, ModelMessage{Role: "assistant", ToolCalls: []ToolCall{call}}); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	a, err := core.Store.startTool(t.Context(), run, call)
	if err != nil {
		t.Fatal(err)
	}
	if err = core.Store.dispatchTool(t.Context(), a, 0); err != nil {
		t.Fatal(err)
	}
	original := string(mustJSON(ToolResult{Outcome: &ToolOutcome{Status: "unknown", Dispatched: true}}))
	if err = core.Store.markUnknown(t.Context(), a, "tool outcome unknown", original); err != nil {
		t.Fatal(err)
	}
	preview, err := core.Store.ReconcileReadDenials(t.Context(), "")
	if err != nil || len(preview.Attempts) != 1 {
		t.Fatal(preview, err)
	}
	if _, err = core.Store.ReconcileReadDenials(t.Context(), strings.Repeat("0", 64)); !errors.Is(err, ErrApprovalChanged) {
		t.Fatal(err)
	}
	for range 2 {
		applied, err := core.Store.ReconcileReadDenials(t.Context(), preview.Digest)
		if err != nil || !applied.Applied {
			t.Fatal(applied, err)
		}
	}
	all, err := core.Store.ListAttempts(t.Context(), run.ID)
	if err != nil || len(all) != 1 || all[0].Status != "unknown" || all[0].ResponseJSON != original || all[0].Resolution != "not_dispatched" {
		t.Fatal(all, err)
	}
	if _, err = core.Run(t.Context(), chat, "after-reconcile", "continue"); err != nil {
		t.Fatal(err)
	}
}

func TestEmergencyOmissionKeepsHistoryAndCheckpoint(t *testing.T) {
	core, _, _ := testHarness(t, EchoModel{})
	core.controlToolsActive = true
	core.Config.MaxOutputTokens = 128
	chat := conversation(t, core)
	for i := 0; i < 3; i++ {
		r, err := core.Run(t.Context(), chat, newID(), strings.Repeat("earlier bounded facts ", 300))
		if err != nil || r.Run.Status != "completed" {
			t.Fatal(err)
		}
	}
	core.Config.MaxContextBytes = 14000
	snapshot, err := core.snapshot()
	if err != nil {
		t.Fatal(err)
	}
	run, _, err := core.Store.CreateRun(t.Context(), chat, "omission", "new independent question", snapshot)
	if err != nil {
		t.Fatal(err)
	}
	request, err := core.buildContextWithSummary(t.Context(), run, nil)
	if err != nil {
		t.Fatal(err)
	}
	usage, err := core.requestUsage(run, request)
	if err != nil || !usage.OverLimit {
		t.Fatal("fixture did not exceed limit", usage.Bytes, err)
	}
	reduced, err := core.emergencyContext(t.Context(), run, request)
	if err != nil {
		t.Fatal(err)
	}
	after, err := core.requestUsage(run, reduced)
	if err != nil || after.OverLimit {
		t.Fatal(after, err)
	}
	if err := completeMessageGroups(reduced.Messages); err != nil {
		t.Fatal(err)
	}
	summary, _, err := core.Store.contextSummary(t.Context(), chat, false)
	if err != nil || summary != nil {
		t.Fatal("omission advanced checkpoint", err)
	}
	again, err := core.applyContextOmission(t.Context(), run, request)
	if err != nil || len(again.Messages) != len(reduced.Messages) {
		t.Fatal(err)
	}
	core.Config.AutomaticCompaction = new(bool)
	_, err = core.emergencyContext(t.Context(), run, request)
	if !errors.Is(err, ErrContextLimit) {
		t.Fatal("disabled automatic history was omitted", err)
	}
}
