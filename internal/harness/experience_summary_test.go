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

func installSummaryFixture(t *testing.T, core *Service) {
	t.Helper()
	source, err := filepath.Abs(filepath.Join("..", "..", "optional-skills", "experience-summary"))
	if err != nil {
		t.Fatal(err)
	}
	tasks := NewTaskService(core)
	if _, err := tasks.ImportSkill(t.Context(), source); err != nil {
		t.Fatal(err)
	}
	if core.Config.Skills.Enabled == nil {
		core.Config.Skills.Enabled = map[string]bool{}
	}
	core.Config.Skills.Enabled["experience-summary"] = true
	if err := WriteConfig(core.Store.DataDir(), core.Config); err != nil {
		t.Fatal(err)
	}
}

func TestExperienceSourcesPrivacyDraftAndConfirmedMemory(t *testing.T) {
	calls := 0
	core, _, _ := testHarness(t, modelFunc(func(_ context.Context, req ModelRequest, _ func(string)) (ModelResponse, error) {
		calls++
		if req.DenyOperations {
			if len(req.Tools) != 0 {
				t.Error("summary exposed tools")
			}
			last := req.Messages[len(req.Messages)-1].Content
			if strings.Contains(last, "PRIVATE-CONTINUATION") || strings.Contains(last, "sk-fixturesecret123456") {
				t.Error("summary leaked excluded protocol data or credentials")
			}
			return ModelResponse{Content: `{"kind":"memory","title":"验收","body":"部署后先核对运行版本。","limitations":["仍需按环境复核"]}`}, nil
		}
		return ModelResponse{Content: "confirmed deployment", ChatContinuation: &ChatContinuation{Source: contentHash([]byte("fixture")), Content: "PRIVATE-CONTINUATION"}}, nil
	}))
	installSummaryFixture(t, core)
	tasks := NewTaskService(core)
	chat := conversation(t, core)
	original, err := tasks.Submit(t.Context(), chat, "original", "test sk-fixturesecret123456")
	if err != nil {
		t.Fatal(err)
	}
	selection := SourceAnalysisSelection{Skill: "experience-summary", RunIDs: []string{original.Run.ID}}
	preview, err := tasks.PreviewSourceAnalysis(t.Context(), chat, selection)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(preview.Source, "PRIVATE-CONTINUATION") || strings.Contains(preview.Source, "sk-fixturesecret123456") {
		t.Fatal("source privacy violated")
	}
	other := conversation(t, core)
	if _, err := tasks.PreviewSourceAnalysis(t.Context(), other, selection); !errors.Is(err, ErrConversationSpace) {
		t.Fatal("cross-conversation source accepted", err)
	}
	if _, err := tasks.AnalyzeSources(t.Context(), chat, "draft", selection, "wrong"); !errors.Is(err, ErrApprovalChanged) {
		t.Fatal("stale source accepted", err)
	}
	result, err := tasks.AnalyzeSources(t.Context(), chat, "draft", selection, preview.Digest)
	if err != nil || result.Run.Status != "completed" {
		t.Fatal(result.Run.Status, err)
	}
	snapshot, err := decodeRunSnapshot(result.Run.ConfigJSON)
	if err != nil || !snapshot.AnalysisOnly || len(snapshot.AnalysisSource) == 0 {
		t.Fatal("source not persisted with draft", err)
	}
	receipt, err := tasks.SaveAnalysisMemory(t.Context(), chat, result.Run.ID, "publish-memory", "验收", "部署后先核对运行版本。")
	if err != nil {
		t.Fatal(err)
	}
	repeated, err := tasks.SaveAnalysisMemory(t.Context(), chat, result.Run.ID, "publish-memory", "验收", "部署后先核对运行版本。")
	if err != nil || !repeated.Duplicate || receipt.EntryID != repeated.EntryID {
		t.Fatal(repeated, err)
	}
	if _, err := tasks.SaveAnalysisMemory(t.Context(), chat, result.Run.ID, "bad", "credential", "api_key=sk-fixturesecret123456"); err == nil {
		t.Fatal("credential was published")
	}
	if calls != 2 {
		t.Fatalf("unexpected analysis calls %d", calls)
	}
}

func TestExperienceSkillPublicationConfirmationAndVersionConflict(t *testing.T) {
	core, _, _ := testHarness(t, EchoModel{})
	control, err := NewControlService(NewTaskService(core))
	if err != nil {
		t.Fatal(err)
	}
	chat := conversation(t, core)
	document := "---\nname: deployment-notes\ndescription: Verified synthetic deployment method\ntrigger: manual\n---\nInputs: selected release.\nSteps: verify version after deployment.\nVerification: compare actual runtime.\nLimits: environment-specific, do not replay UNKNOWN commands.\n"
	args := SkillPublication{ID: "deployment-notes", Document: document}
	operation, err := control.PlanControlChange(t.Context(), ControlPlanRequest{Kind: ControlPublishSkill, Arguments: mustJSON(args), Authorization: ControlModelProposed, ConversationID: chat})
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(core.Store.DataDir(), "skills", args.ID, "SKILL.md")
	if _, err := os.Stat(target); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("planning wrote a Skill")
	}
	capability, err := control.IssueInteractiveCapability(t.Context(), chat, "user")
	if err != nil {
		t.Fatal(err)
	}
	confirmation, err := control.RenderConfirmation(t.Context(), operation.ID, chat, "user")
	if err != nil {
		t.Fatal(err)
	}
	result, err := control.ConfirmControlChange(t.Context(), operation.ID, controlAuth(capability, chat, "user", confirmation.Code))
	if err != nil || !result.Applied {
		t.Fatal(result, err)
	}
	raw, err := os.ReadFile(target)
	if err != nil || string(raw) != document {
		t.Fatal(err)
	}
	if core.Config.Skills.Enabled[args.ID] {
		t.Fatal("publication silently enabled Skill")
	}
	args.ExpectedSHA256 = contentHash(raw)
	args.Document = document + "Updated notes.\n"
	pending, err := control.PlanControlChange(t.Context(), ControlPlanRequest{Kind: ControlPublishSkill, Arguments: mustJSON(args), Authorization: ControlModelProposed, ConversationID: chat})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte(document+"external change"), 0600); err != nil {
		t.Fatal(err)
	}
	capability, err = control.IssueInteractiveCapability(t.Context(), chat, "user")
	if err != nil {
		t.Fatal(err)
	}
	confirmation, err = control.RenderConfirmation(t.Context(), pending.ID, chat, "user")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := control.ConfirmControlChange(t.Context(), pending.ID, controlAuth(capability, chat, "user", confirmation.Code)); err == nil {
		t.Fatal("stale Skill content was overwritten")
	}
	for _, id := range []string{".", "..", "...", ".revisions", "trailing."} {
		if validCapabilityID(id) {
			t.Fatalf("unsafe path identifier %q accepted", id)
		}
	}
}

func TestExperienceOptionalSkillGateAndChangedMethod(t *testing.T) {
	calls := 0
	core, _, _ := testHarness(t, modelFunc(func(context.Context, ModelRequest, func(string)) (ModelResponse, error) {
		calls++
		return ModelResponse{Content: "synthetic source"}, nil
	}))
	tasks := NewTaskService(core)
	chat := conversation(t, core)
	source, err := tasks.Submit(t.Context(), chat, "source", "selected evidence")
	if err != nil {
		t.Fatal(err)
	}
	selection := SourceAnalysisSelection{Skill: "experience-summary", RunIDs: []string{source.Run.ID}}
	if _, err := tasks.PreviewSourceAnalysis(t.Context(), chat, selection); err == nil {
		t.Fatal("uninstalled optional Skill was accepted")
	}
	installSummaryFixture(t, core)
	preview, err := tasks.PreviewSourceAnalysis(t.Context(), chat, selection)
	if err != nil {
		t.Fatal(err)
	}
	core.Config.Skills.Enabled["experience-summary"] = false
	if _, err := tasks.AnalyzeSources(t.Context(), chat, "disabled", selection, preview.Digest); err == nil {
		t.Fatal("disabled optional Skill was accepted")
	}
	core.Config.Skills.Enabled["experience-summary"] = true
	path := filepath.Join(core.Store.DataDir(), "skills", "experience-summary", "SKILL.md")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(raw, []byte("\nSynthetic method revision.\n")...), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := tasks.AnalyzeSources(t.Context(), chat, "changed", selection, preview.Digest); !errors.Is(err, ErrApprovalChanged) {
		t.Fatal("changed method accepted an old digest", err)
	}
	for _, invalid := range []SourceAnalysisSelection{
		{RunIDs: selection.RunIDs, FromMessage: 1},
		{RunIDs: selection.RunIDs, ThroughMessage: 2},
		{RunIDs: selection.RunIDs, FromMessage: 2, ThroughMessage: 1},
		{RunIDs: []string{source.Run.ID, source.Run.ID}},
	} {
		if _, err := tasks.PreviewSourceAnalysis(t.Context(), chat, invalid); err == nil {
			t.Fatalf("invalid selection accepted: %+v", invalid)
		}
	}
	if calls != 1 {
		t.Fatalf("preview/gating invoked the model: %d calls", calls)
	}
}

func TestExperienceUnknownEvidenceAndAnalysisSourceIsolation(t *testing.T) {
	analysisCalls := 0
	core, _, _ := testHarness(t, modelFunc(func(_ context.Context, req ModelRequest, _ func(string)) (ModelResponse, error) {
		if !req.DenyOperations {
			return ModelResponse{Content: "synthetic source answer"}, nil
		}
		analysisCalls++
		if len(req.Messages) != 2 || len(req.Tools)+len(req.Memory)+len(req.Extensions) != 0 {
			t.Fatalf("analysis acquired unrelated context or capabilities: %+v", req)
		}
		payload := req.Messages[1].Content
		for _, excluded := range []string{"UNSELECTED-HISTORY", "UNSELECTED-SYSTEM", "UNSELECTED-MEMORY"} {
			if strings.Contains(payload, excluded) || strings.Contains(req.Messages[0].Content, excluded) {
				t.Fatalf("unselected context leaked: %s", excluded)
			}
		}
		if !strings.Contains(payload, `"state":"unknown"`) || !strings.Contains(payload, "effect remains UNKNOWN") {
			t.Fatal("UNKNOWN evidence was lost")
		}
		return ModelResponse{Content: `{"kind":"draft","body":"Outcome remains UNKNOWN.","limitations":["Do not blindly retry."],"verification":"Inspect the original receipt."}`}, nil
	}))
	core.Config.SystemPrompt = "UNSELECTED-SYSTEM"
	installSummaryFixture(t, core)
	tasks := NewTaskService(core)
	chat := conversation(t, core)
	source, err := tasks.Submit(t.Context(), chat, "source", "selected synthetic attempt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tasks.Submit(t.Context(), chat, "unselected", "UNSELECTED-HISTORY"); err != nil {
		t.Fatal(err)
	}
	ref := retainedMemory(t, tasks, chat, "unselected-memory", "UNSELECTED-MEMORY")
	if _, err := tasks.SaveMemory(t.Context(), chat, MemoryMutation{RequestKey: "pin-unselected", Action: "pin", Scope: ref.Scope, EntryID: ref.EntryID, ExpectedRevision: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := core.Store.db.ExecContext(t.Context(), `INSERT INTO fa.attempts(id,run_id,kind,call_id,status,request_json,response_json) VALUES($1,$2,'tool','fixture-effect','unknown',$3,$4)`, newID(), source.Run.ID, string(mustJSON(ToolCall{ID: "fixture-effect", Name: "command_run", Arguments: json.RawMessage(`{"command":"synthetic only"}`)})), string(mustJSON(ToolResult{Content: "effect remains UNKNOWN"}))); err != nil {
		t.Fatal(err)
	}
	if err := core.Store.FinishRun(t.Context(), source.Run.ID, "failed", "synthetic interrupted effect"); err != nil {
		t.Fatal(err)
	}
	selection := SourceAnalysisSelection{Skill: "experience-summary", RunIDs: []string{source.Run.ID}}
	preview, err := tasks.PreviewSourceAnalysis(t.Context(), chat, selection)
	if err != nil {
		t.Fatal(err)
	}
	result, err := tasks.AnalyzeSources(t.Context(), chat, "unknown-draft", selection, preview.Digest)
	if err != nil || result.Run.Status != "completed" || !strings.Contains(result.Answer, "UNKNOWN") {
		t.Fatal(result, err)
	}
	repeated, err := tasks.AnalyzeSources(t.Context(), chat, "unknown-draft", selection, preview.Digest)
	if err != nil || !repeated.Duplicate || repeated.Run.ID != result.Run.ID || analysisCalls != 1 {
		t.Fatal("summary retry created another model request", repeated, err, analysisCalls)
	}
	var first int64
	if err := core.Store.db.QueryRowContext(t.Context(), `SELECT min(id) FROM fa.messages WHERE run_id=$1`, source.Run.ID).Scan(&first); err != nil {
		t.Fatal(err)
	}
	ranged, err := tasks.PreviewSourceAnalysis(t.Context(), chat, SourceAnalysisSelection{Skill: "experience-summary", RunIDs: selection.RunIDs, FromMessage: first, ThroughMessage: first})
	if err != nil || strings.Contains(ranged.Source, "effect remains UNKNOWN") || !strings.Contains(ranged.Source, `"state":"unknown"`) || !strings.Contains(ranged.Source, "range_caveat") {
		t.Fatal("message range exposed an out-of-range tool result or lost state caveats", ranged.Source, err)
	}
	if _, err := tasks.SaveAnalysisMemory(t.Context(), conversation(t, core), result.Run.ID, "cross-space-save", "draft", "wrong scope"); !errors.Is(err, ErrMemoryDenied) {
		t.Fatal("cross-conversation Memory publication accepted", err)
	}
}

func TestExperienceSkillPublicationDeclineAndRepeatConfirmation(t *testing.T) {
	core, _, _ := testHarness(t, EchoModel{})
	control, err := NewControlService(NewTaskService(core))
	if err != nil {
		t.Fatal(err)
	}
	chat := conversation(t, core)
	args := SkillPublication{ID: "declined-experience", Document: "---\nname: declined-experience\ndescription: Synthetic publication\ntrigger: manual\n---\nUnverified synthetic steps; UNKNOWN requires inspection.\n"}
	op, err := control.PlanControlChange(t.Context(), ControlPlanRequest{Kind: ControlPublishSkill, Arguments: mustJSON(args), Authorization: ControlModelProposed, ConversationID: chat})
	if err != nil {
		t.Fatal(err)
	}
	capability, err := control.IssueInteractiveCapability(t.Context(), chat, "user")
	if err != nil {
		t.Fatal(err)
	}
	confirmation, err := control.RenderConfirmation(t.Context(), op.ID, chat, "user")
	if err != nil {
		t.Fatal(err)
	}
	declined, err := control.RejectControlChange(t.Context(), op.ID, controlAuth(capability, chat, "user", ""))
	if err != nil || declined.Applied || declined.Operation.Status != ControlRejected {
		t.Fatal(declined, err)
	}
	if _, err := control.ConfirmControlChange(t.Context(), op.ID, controlAuth(capability, chat, "user", confirmation.Code)); err == nil {
		t.Fatal("declined publication was subsequently applied")
	}
	if _, err := os.Stat(filepath.Join(core.Store.DataDir(), "skills", args.ID)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("declined publication wrote a Skill", err)
	}
}
